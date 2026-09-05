package mqtt

import (
	"context"
	"fmt"
	"reflect"
	"time"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/middleware"
	asyncapi "github.com/DaniDeer/go-codex/render/asyncapi/v3"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/stats"
)

// caller bundles an already-connected [pahomqtt.Client] with an
// [events.Client] registry — the mqtt v3 mirror of mqtt5's caller/zeromq's
// caller, letting a declared [events.Subscriber] be swapped onto a
// different broker connection without touching its declaration.
//
// Unlike mqtt5's caller (which also bundles an MQTTRouter), caller has NO
// router field — MQTT 3.1.1 ([github.com/eclipse/paho.mqtt.golang]) has NO
// router concept at all: [pahomqtt.Client.Subscribe] itself takes the
// per-topic [pahomqtt.MessageHandler] directly, and the client's own
// connection loop dispatches incoming messages to it — there is nothing
// else to bundle. This is GENUINELY NEW capability for this package (not a
// mechanical rename): today's callers build a [subscribeHandler] closure
// and wire it into their own client.Subscribe call BY HAND; [subscribe]
// does that FOR the caller, uniformly with mqtt5/zeromq's own caller-based
// workflow, while [subscribeHandler] itself stays completely unchanged as
// the lower-level primitive underneath.
type caller struct {
	client pahomqtt.Client
	events *events.Client
}

// newCaller bundles client (an already-connected [pahomqtt.Client] — see
// [NewSecuredClient] for connect-level credential validation) with ev (an
// [events.Client] registry populated via [events.Subscriber.Register]/
// [events.Subscriber.Handle]). ev is NILABLE — pass nil for a caller that
// never touches the AsyncAPI spec at all, mirroring
// [events.Subscriber.Handle]'s own "client optional, nil = spec-free"
// contract; [(*caller).ServeSubscribers] is then a safe, immediate no-op
// (nothing registered to walk).
func newCaller(client pahomqtt.Client, ev *events.Client) *caller {
	return &caller{client: client, events: ev}
}

// Compile-time assertion that *caller satisfies [events.SubscriberServer].
var _ events.SubscriberServer = (*caller)(nil)

// Subscribe is the value-based convenience built on top of the existing,
// unchanged [subscribeHandler] primitive: it builds sub's [events.ChannelHandle]
// via sub.Handle(caller.events), builds the [pahomqtt.MessageHandler] closure
// via [subscribeHandler] (decoding, topic-var merge, security enforcement,
// observer calls all delegated to it, unchanged), then calls
// caller.client.Subscribe itself — the wiring today's callers otherwise do
// by hand.
//
// The broker subscription filter resolves opts.TopicFilter if non-empty,
// else a filter derived from the handle's topic via [deriveWildcardFilter]
// (fixes the templated-topic bug this NEW entry point would otherwise
// inherit from day one — see
// docs/design/d-0002-pubsub-workflow-simplification.md's wildcard bug-fix
// subsection). A non-templated topic's behavior is unchanged.
//
// Every attached [events.ChannelHandle.Implementations] Fn (from
// [events.Subscriber.SubscribeMW]) is validated EAGERLY here, before the
// broker subscription is made, via [validateSubscribeImplementationShapes]
// — a malformed Fn fails loudly and immediately, never silently at message
// time. General-purpose Fns wrap fn ([wrapSubscribeGeneral]); security-shaped
// Fns run per-message ([runSubscribeSecurityImpls]).
//
// Call Subscribe once per channel per connection. Cancelling ctx does NOT
// unsubscribe from the broker — call caller's underlying client.Unsubscribe
// explicitly if needed.
func subscribe[T any](
	ctx context.Context,
	caller *caller,
	sub events.Subscriber[T],
	qos byte,
	fn func(context.Context, T) error,
	opts SubscribeOptions,
	formats ...format.Format[T],
) error {
	handle, err := sub.Handle(caller.events)
	if err != nil {
		return err
	}
	return subscribeHandle(ctx, caller.client, handle, qos, fn, opts, formats...)
}

// subscribeHandle is the shared implementation behind [subscribe] and
// [serveOneSubscriber] — validates attached implementation shapes,
// wraps fn with any general-purpose [events.Subscriber.SubscribeMW]
// attachment, resolves the broker subscription filter, builds the
// [pahomqtt.MessageHandler] via [subscribeHandler] (unchanged), and calls
// client.Subscribe.
func subscribeHandle[T any](
	ctx context.Context,
	client pahomqtt.Client,
	handle *events.ChannelHandle[T],
	qos byte,
	fn func(context.Context, T) error,
	opts SubscribeOptions,
	formats ...format.Format[T],
) error {
	if err := validateSubscribeImplementationShapes[T](handle.Implementations); err != nil {
		return err
	}

	var secReqs []route.SecurityRequirement
	if handle.Descriptor.Subscribe != nil {
		secReqs = handle.Descriptor.Subscribe.Security
	}
	if secReqs == nil {
		secReqs = handle.GlobalSecurity
	}

	wrapped := wrapSubscribeGeneral(fn, handle.Implementations)
	finalFn := func(ctx context.Context, v T) error {
		if len(secReqs) > 0 {
			msg, _ := MessageFromContext(ctx)
			if err := runSubscribeSecurityImpls(ctx, msg, &v, secReqs, handle.Implementations); err != nil {
				return err
			}
		}
		return wrapped(ctx, v)
	}

	filter := opts.TopicFilter
	if filter == "" {
		filter = deriveWildcardFilter(handle.Topic)
	}

	handler := subscribeHandler(ctx, client, handle, finalFn, opts, formats...)
	token := client.Subscribe(filter, qos, handler)
	token.Wait()
	return token.Error()
}

// runSubscribeSecurityImpls runs every attached
// [middleware.ServerImplementation] whose Fn matches the security shape
// (func(context.Context, pahomqtt.Message, *T) (map[string][]string, error))
// IN ATTACHMENT ORDER (fail-fast on the first one whose OWN extraction
// errors), merges their returned grants into ONE map, then performs a
// SINGLE [middleware.CheckScopes] call — the mqtt v3 mirror of
// adapters/nethttp's runSecurityMiddlewareReflect/mqtt5's
// runSubscribeSecurityImpls, via a plain type assertion (T is concrete at
// this generic call site). General-purpose wrapping-shaped Fns are
// silently skipped here (consumed instead by [wrapSubscribeGeneral]).
func runSubscribeSecurityImpls[T any](ctx context.Context, msg pahomqtt.Message, value *T, secReqs []route.SecurityRequirement, impls []middleware.ServerImplementation) error {
	granted := make(map[string][]string)
	for _, impl := range impls {
		fn, ok := impl.Fn.(func(context.Context, pahomqtt.Message, *T) (map[string][]string, error))
		if !ok {
			continue // general-purpose or nil
		}
		if len(impl.Satisfies) > 0 && len(secReqs) == 0 {
			continue
		}
		g, err := fn(ctx, msg, value)
		if err != nil {
			return err
		}
		for k, v := range g {
			granted[k] = v
		}
	}
	return middleware.CheckScopes(secReqs, granted)
}

// wrapSubscribeGeneral wraps fn with every general-purpose Fn found in
// impls (shape func(next func(context.Context, T) error) func(context.Context,
// T) error), OUTERMOST-in, in attachment order (impls[0] is outermost —
// the first .SubscribeMW call wraps everything else).
func wrapSubscribeGeneral[T any](fn func(context.Context, T) error, impls []middleware.ServerImplementation) func(context.Context, T) error {
	for i := len(impls) - 1; i >= 0; i-- {
		wrap, ok := impls[i].Fn.(func(func(context.Context, T) error) func(context.Context, T) error)
		if !ok {
			continue
		}
		fn = wrap(fn)
	}
	return fn
}

// validateSubscribeImplementationShapes checks every attached impl.Fn
// against the two shapes [events.Subscriber.SubscribeMW] recognizes for T
// — the security shape (func(context.Context, pahomqtt.Message, *T)
// (map[string][]string, error)) or the general-purpose wrapping shape
// (func(next func(context.Context, T) error) func(context.Context, T) error)
// — EAGERLY at [subscribe]/[(*caller).ServeSubscribers] construction time
// rather than deferring to the first incoming message. Mirrors
// adapters/nethttp's validateImplementationShapesReflect (via a plain type
// switch here since T is concrete at this generic call site — no reflect
// needed).
func validateSubscribeImplementationShapes[T any](impls []middleware.ServerImplementation) error {
	for _, impl := range impls {
		if impl.Fn == nil {
			continue
		}
		switch impl.Fn.(type) {
		case func(context.Context, pahomqtt.Message, *T) (map[string][]string, error):
		case func(func(context.Context, T) error) func(context.Context, T) error:
		default:
			return middleware.MiddlewareShapeError{
				Name:     impl.Name,
				Expected: "func(context.Context, pahomqtt.Message, *T) (map[string][]string, error) or func(func(context.Context, T) error) func(context.Context, T) error",
				Got:      fmt.Sprintf("%T", impl.Fn),
			}
		}
	}
	return nil
}

// ServeSubscribers implements [events.SubscriberServer]: it walks every
// [events.Subscriber] registered against caller.events via
// [events.Subscriber.Register], subscribes each one against caller.client,
// and blocks until ctx is cancelled.
//
// Concurrency model, chosen to match MQTT 3.1.1's ACTUAL client
// architecture (checked against [github.com/eclipse/paho.mqtt.golang]'s own
// code, not assumed): unlike a REST server (one goroutine per in-flight
// request, dispatched by net/http) or a hypothetical "one blocking receive
// loop per subscription" design, [pahomqtt.Client.Subscribe] is a
// REGISTRATION call — it returns almost immediately, and the underlying
// client already runs its OWN internal goroutine(s) (driven by the single
// physical connection's read loop) that invoke every registered
// [pahomqtt.MessageHandler] as messages arrive. There is no per-route
// blocking receive loop to spin up here, unlike, say, a hypothetical
// polling-based transport. So ServeSubscribers' own job is simply: (1)
// subscribe every entry (registering its message handler with the broker),
// collecting every subscribe failure into one [MultiSubscribeError] without
// returning early (so one bad channel doesn't prevent every other channel
// from being subscribed), then (2) block on ctx.Done() — actual message
// dispatch happens on goroutines paho.mqtt.golang itself owns and manages,
// entirely outside this function's own call stack.
//
// Entries with HasHandler() == false are skipped defensively (never
// actually reachable in practice — [events.Subscriber.Register] rejects a
// handler-less Subscriber eagerly — but mirrors
// [events.SubscriberEntry.HasHandler]'s own documented belt-and-braces
// contract).
//
// Per-channel QoS is read from the channel's [events.ChannelHandle.HandlerOpts]
// (attached via [events.Subscriber.WithOptions] with a [SubscribeOptions]
// value) — HandlerOpts.QoS. A HandlerOpts value of the wrong concrete type
// is a hard [OptionsShapeError], collected the same way as a subscribe
// failure. nil HandlerOpts (WithOptions never called) uses QoS 0 and an
// auto-derived TopicFilter, the zero-ceremony default.
//
// Dispatch is fully reflection-based (Handle() returns a type-erased
// *[events.ChannelHandle][T] — [events.SubscriberEntry] is intentionally
// non-generic so ServeSubscribers can walk a HETEROGENEOUS collection of
// channels with different payload types in one call): reflect.Value.Call
// invokes the ALREADY-CONCRETE Decode/Handler/Implementations[i].Fn closures
// recovered from the handle, mirroring adapters/nethttp's Serve. Channels
// declaring merge-capable topic params (via [events.NewTopicParam]) are NOT
// currently supported through ServeSubscribers (a documented scope
// limitation of this reflection-only dispatch path — [subscribe]/
// [serveOneSubscriber], which stay fully generic, support every
// existing feature including topic-var merging without any such gap).
func (c *caller) ServeSubscribers(ctx context.Context) error {
	if c.events == nil {
		return nil
	}
	entries := c.events.SubscriberEntries()

	var subErrs []SubscribeEntryError
	for _, e := range entries {
		if !e.HasHandler() {
			continue
		}
		if err := subscribeEntryReflect(ctx, c.client, e); err != nil {
			subErrs = append(subErrs, SubscribeEntryError{Topic: e.Topic(), Err: err})
		}
	}
	if len(subErrs) > 0 {
		return MultiSubscribeError{Errors: subErrs}
	}

	<-ctx.Done()
	return nil
}

// serveOneSubscriber builds a throwaway [events.Client] + [*caller]
// internally, registers sub (with fn attached via [events.Subscriber.WithHandler])
// as its only subscriber, then serves just it — pure sugar, implemented as
// "build a scratch single-subscriber Client, call sub.Register, call
// ServeSubscribers." Mirrors [adapters/nethttp.ServeOne].
//
// A package-level generic FUNCTION, not a method on [*caller] — Go does
// not support generic methods (only generic types/functions), the same
// reason [adapters/nethttp.ServeOne] is a package function rather than a
// method on a REST-side caller type.
//
// qos (this function's own call-time parameter) always takes precedence —
// it overwrites opts.QoS before attaching opts, mirroring [subscribe]'s own
// qos-parameter precedence (opts.QoS otherwise only matters to
// [(*caller).ServeSubscribers], which has no call-time qos parameter of its
// own to prefer). Blocks until ctx is cancelled or the subscribe itself
// fails.
func serveOneSubscriber[T any](
	ctx context.Context,
	caller *caller,
	sub events.Subscriber[T],
	qos byte,
	fn func(context.Context, T) error,
	opts SubscribeOptions,
	formats ...format.Format[T],
) error {
	scratch := events.NewClient(events.WithInfo(events.Info{Title: "ServeOneSubscriber", Version: "0.0.0"}))
	opts.QoS = qos
	sub = sub.WithHandler(fn).WithOptions(opts)
	if err := sub.Register(scratch); err != nil {
		return err
	}
	oneCaller := newCaller(caller.client, scratch)
	return oneCaller.ServeSubscribers(ctx)
}

// SubscribeEntryError pairs a subscribed channel's topic with what went
// wrong subscribing it — one entry of a [MultiSubscribeError].
type SubscribeEntryError struct {
	Topic string
	Err   error
}

func (e SubscribeEntryError) Error() string {
	return fmt.Sprintf("%s: %v", e.Topic, e.Err)
}

func (e SubscribeEntryError) Unwrap() error { return e.Err }

// MultiSubscribeError is returned by [(*caller).ServeSubscribers] when one or
// more registered channels fail to subscribe. Carries EVERY individual
// failure found — not just the first — mirroring
// [adapters/nethttp.MultiRouteError]. Unwrap() []error (Go 1.20+
// multi-error support) lets errors.As/Is reach into ANY individual
// channel's error directly.
type MultiSubscribeError struct {
	Errors []SubscribeEntryError
}

func (e MultiSubscribeError) Error() string {
	s := fmt.Sprintf("%d channel(s) failed to subscribe:", len(e.Errors))
	for _, se := range e.Errors {
		s += fmt.Sprintf("\n  - %s", se.Error())
	}
	return s
}

func (e MultiSubscribeError) Unwrap() []error {
	out := make([]error, len(e.Errors))
	for i, se := range e.Errors {
		out[i] = se
	}
	return out
}

// OptionsShapeError is returned when a [events.Subscriber.WithOptions]
// value's concrete type is not [SubscribeOptions] — mirrors
// [adapters/nethttp.OptionsShapeError].
type OptionsShapeError struct {
	Topic string
	Got   any
}

func (e OptionsShapeError) Error() string {
	return fmt.Sprintf("mqtt: %s: WithOptions value has wrong type: want mqtt.SubscribeOptions, got %T", e.Topic, e.Got)
}

// resolveHandlerOpts type-asserts a channel's type-erased HandlerOpts
// (from [events.Subscriber.WithOptions]) to [SubscribeOptions]. nil means
// WithOptions was never called — QoS 0 and auto-derived TopicFilter apply.
func resolveHandlerOpts(topic string, handlerOpts any) (SubscribeOptions, error) {
	if handlerOpts == nil {
		return SubscribeOptions{}, nil
	}
	opts, ok := handlerOpts.(SubscribeOptions)
	if !ok {
		return SubscribeOptions{}, OptionsShapeError{Topic: topic, Got: handlerOpts}
	}
	return opts, nil
}

// subscribeEntryReflect subscribes ONE heterogeneous [events.SubscriberEntry]
// against client, using reflect to invoke the entry's already-concrete
// Decode/Handler/Implementations[i].Fn closures without knowing T
// statically — see [(*caller).ServeSubscribers]'s doc comment for the full
// dispatch rationale.
func subscribeEntryReflect(ctx context.Context, client pahomqtt.Client, entry events.SubscriberEntry) error {
	hv := reflect.ValueOf(entry.Handle())
	if hv.Kind() != reflect.Pointer || hv.IsNil() {
		return fmt.Errorf("mqtt.ServeSubscribers: expected non-nil *events.ChannelHandle[T], got %T", entry.Handle())
	}
	elem := hv.Elem()

	topic := elem.FieldByName("Topic").String()
	descriptor, _ := elem.FieldByName("Descriptor").Interface().(asyncapi.ChannelItem)
	globalSecurity, _ := elem.FieldByName("GlobalSecurity").Interface().([]route.SecurityRequirement)
	impls, _ := elem.FieldByName("Implementations").Interface().([]middleware.ServerImplementation)
	handlerOptsAny := elem.FieldByName("HandlerOpts").Interface()
	handlerVal := elem.FieldByName("Handler")
	if handlerVal.IsNil() {
		return fmt.Errorf("mqtt.ServeSubscribers: topic %s has no handler (internal error — HasHandler should have skipped it)", topic)
	}

	opts, err := resolveHandlerOpts(topic, handlerOptsAny)
	if err != nil {
		return err
	}
	obs := opts.Observer
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}

	if err := validateSubscribeImplementationShapesReflect(topic, handlerVal.Type().In(1), impls); err != nil {
		return err
	}

	var secReqs []route.SecurityRequirement
	if descriptor.Subscribe != nil {
		secReqs = descriptor.Subscribe.Security
	}
	if secReqs == nil {
		secReqs = globalSecurity
	}

	// Wrap the handler with every general-purpose implementation,
	// outermost-in — reflect mirror of [wrapSubscribeGeneral].
	msgHandlerType := handlerVal.Type() // func(context.Context, T) error
	wrapped := handlerVal
	for i := len(impls) - 1; i >= 0; i-- {
		fnVal := reflect.ValueOf(impls[i].Fn)
		if !fnVal.IsValid() || fnVal.Type() != reflect.FuncOf([]reflect.Type{msgHandlerType}, []reflect.Type{msgHandlerType}, false) {
			continue
		}
		wrapped = fnVal.Call([]reflect.Value{wrapped})[0]
	}

	decodeVal := elem.FieldByName("Decode")
	valueType := decodeVal.Type().Out(0)

	// SubscribeFormats > Formats > Decode, mirroring SubscribeHandler's own
	// priority chain.
	fmts := elem.FieldByName("SubscribeFormats")
	if fmts.Len() == 0 {
		fmts = elem.FieldByName("Formats")
	}

	filter := opts.TopicFilter
	if filter == "" {
		filter = deriveWildcardFilter(topic)
	}

	handler := func(_ pahomqtt.Client, msg pahomqtt.Message) {
		start := time.Now()
		var decodeResults []reflect.Value
		if fmts.Len() > 0 {
			decodeResults = fmts.Index(0).MethodByName("Unmarshal").Call([]reflect.Value{reflect.ValueOf(msg.Payload())})
		} else {
			decodeResults = decodeVal.Call([]reflect.Value{reflect.ValueOf(msg.Payload())})
		}
		valuePtr := reflect.New(valueType)
		valuePtr.Elem().Set(decodeResults[0])
		if errI, _ := decodeResults[1].Interface().(error); errI != nil {
			obs.RecordSubscribe(msg.Topic(), false, time.Since(start))
			if opts.OnError != nil {
				opts.OnError(SubscribeError{Kind: KindDecode, Topic: msg.Topic(), Err: errI})
			}
			return
		}

		msgCtx := context.WithValue(ctx, contextKey{}, msg)

		if len(secReqs) > 0 {
			if err := runSubscribeSecurityImplsReflect(msgCtx, msg, valuePtr, secReqs, impls); err != nil {
				if secObs, ok := obs.(stats.SecurityObserver); ok {
					secObs.RecordSecurityRejection(msg.Topic(), firstScheme(secReqs))
				}
				obs.RecordSubscribe(msg.Topic(), false, time.Since(start))
				if opts.OnError != nil {
					opts.OnError(SubscribeError{Kind: KindSecurity, Topic: msg.Topic(), Err: err})
				}
				return
			}
		}

		results := wrapped.Call([]reflect.Value{reflect.ValueOf(msgCtx), valuePtr.Elem()})
		if errI, _ := results[0].Interface().(error); errI != nil {
			obs.RecordSubscribe(msg.Topic(), false, time.Since(start))
			if opts.OnError != nil {
				opts.OnError(SubscribeError{Kind: KindHandler, Topic: msg.Topic(), Err: errI})
			}
			return
		}
		obs.RecordSubscribe(msg.Topic(), true, time.Since(start))
	}

	token := client.Subscribe(filter, opts.QoS, handler)
	token.Wait()
	return token.Error()
}

// runSubscribeSecurityImplsReflect is [runSubscribeSecurityImpls]'s
// reflect-based equivalent — valuePtr is an addressable *T reflect.Value
// (T erased).
func runSubscribeSecurityImplsReflect(ctx context.Context, msg pahomqtt.Message, valuePtr reflect.Value, secReqs []route.SecurityRequirement, impls []middleware.ServerImplementation) error {
	granted := make(map[string][]string)
	expectedType := reflect.FuncOf(
		[]reflect.Type{
			reflect.TypeOf((*context.Context)(nil)).Elem(),
			reflect.TypeOf((*pahomqtt.Message)(nil)).Elem(),
			valuePtr.Type(),
		},
		[]reflect.Type{
			reflect.TypeOf(map[string][]string(nil)),
			reflect.TypeOf((*error)(nil)).Elem(),
		},
		false,
	)
	for _, impl := range impls {
		fnVal := reflect.ValueOf(impl.Fn)
		if !fnVal.IsValid() || fnVal.Type() != expectedType {
			continue // general-purpose or nil
		}
		if len(impl.Satisfies) > 0 && len(secReqs) == 0 {
			continue
		}
		results := fnVal.Call([]reflect.Value{reflect.ValueOf(ctx), reflect.ValueOf(msg), valuePtr})
		if err, _ := results[1].Interface().(error); err != nil {
			return err
		}
		g, _ := results[0].Interface().(map[string][]string)
		for k, v := range g {
			granted[k] = v
		}
	}
	return middleware.CheckScopes(secReqs, granted)
}

// validateSubscribeImplementationShapesReflect is
// [validateSubscribeImplementationShapes]'s reflect-based equivalent,
// mirroring adapters/nethttp's validateImplementationShapesReflect exactly
// — valueType is the channel's payload type T, recovered at runtime via
// reflect (see [subscribeEntryReflect]).
func validateSubscribeImplementationShapesReflect(topic string, valueType reflect.Type, impls []middleware.ServerImplementation) error {
	generalType := reflect.FuncOf([]reflect.Type{
		reflect.FuncOf([]reflect.Type{
			reflect.TypeOf((*context.Context)(nil)).Elem(), valueType,
		}, []reflect.Type{reflect.TypeOf((*error)(nil)).Elem()}, false),
	}, []reflect.Type{
		reflect.FuncOf([]reflect.Type{
			reflect.TypeOf((*context.Context)(nil)).Elem(), valueType,
		}, []reflect.Type{reflect.TypeOf((*error)(nil)).Elem()}, false),
	}, false)
	securityType := reflect.FuncOf(
		[]reflect.Type{
			reflect.TypeOf((*context.Context)(nil)).Elem(),
			reflect.TypeOf((*pahomqtt.Message)(nil)).Elem(),
			reflect.PointerTo(valueType),
		},
		[]reflect.Type{
			reflect.TypeOf(map[string][]string(nil)),
			reflect.TypeOf((*error)(nil)).Elem(),
		},
		false,
	)
	for _, impl := range impls {
		if impl.Fn == nil {
			continue
		}
		fnType := reflect.TypeOf(impl.Fn)
		if fnType == generalType || fnType == securityType {
			continue
		}
		return middleware.MiddlewareShapeError{
			Name:     impl.Name,
			Expected: "func(context.Context, pahomqtt.Message, *T) (map[string][]string, error) or func(func(context.Context, T) error) func(context.Context, T) error",
			Got:      fmt.Sprintf("%T", impl.Fn),
		}
	}
	return nil
}

// Observability returns a general-purpose [events.Subscriber.SubscribeMW]/
// [events.Publisher.PublishMW]-attachable Fn (shape func(next
// func(context.Context, T) error) func(context.Context, T) error) that
// records lifecycle events around next — the pub/sub analogue of
// [adapters/nethttp.Observability]. topic identifies the channel in
// recorded stats — pass the channel's own topic (the same string passed to
// [events.NewChannel]). Attach it UNPAIRED (mw nil) so it runs
// unconditionally:
//
//	sub.SubscribeMW(nil, mqtt.Observability[SensorReading]("sensors/{sensorID}/data", obs))
//
// Direction (subscribe vs. publish) is detected via [MessageFromContext]:
// present (the subscribe-side dispatch path always stores it) ->
// [stats.Observer.RecordSubscribe]; absent -> [stats.Observer.RecordPublish]
// — mirrors [mqtt5.Observability] exactly.
//
// This is an ADDITIONAL, opt-in, declare-time attachment point — it does
// NOT replace [SubscribeOptions.Observer]/[PublishOptions.Observer], which
// remain the PRIMARY, zero-ceremony per-call mechanism (see
// docs/design/d-0002-pubsub-workflow-simplification.md's "General-purpose Fn
// shapes" subsection).
func Observability[T any](topic string, obs stats.Observer) func(func(context.Context, T) error) func(context.Context, T) error {
	return func(next func(context.Context, T) error) func(context.Context, T) error {
		return func(ctx context.Context, msg T) error {
			start := time.Now()
			spanCtx := ctx
			if to, ok := obs.(stats.TraceObserver); ok {
				spanCtx = to.StartSpan(ctx, "mqtt.observability", topic)
			}
			err := next(spanCtx, msg)
			if to, ok := obs.(stats.TraceObserver); ok {
				to.EndSpan(spanCtx, err)
			}
			if _, isSubscribe := MessageFromContext(ctx); isSubscribe {
				obs.RecordSubscribe(topic, err == nil, time.Since(start))
			} else {
				obs.RecordPublish(topic, err == nil, time.Since(start))
			}
			return err
		}
	}
}
