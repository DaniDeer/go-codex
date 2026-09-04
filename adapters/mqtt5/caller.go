package mqtt5

import (
	"context"
	"fmt"
	"reflect"
	"time"

	pahomqtt5 "github.com/eclipse/paho.golang/paho"
	"golang.org/x/sync/errgroup"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/middleware"
	asyncapi "github.com/DaniDeer/go-codex/render/asyncapi/v3"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/stats"
)

// caller bundles the three params [subscribe] and [(*caller).ServeSubscribers]
// would otherwise each repeat — client, router, and an optional
// [events.Client] — mirroring [nethttp.caller]'s param-reduction value for
// REST. Unlike [nethttp.caller], caller has NO WithBaseURL equivalent: an
// [MQTTClient] is bound to exactly ONE already-connected broker at
// construction (TCP/TLS handshake already done) — there is no shared,
// reusable transport independent of the broker target the way
// *http.Client is independent of the target host, so a cheap "rebase" is
// not meaningful here. Construct a brand-new caller via [newCaller] to
// target a different broker.
type caller struct {
	client MQTTClient
	router MQTTRouter
	events *events.Client
}

// newCaller returns a [*caller] bundling client, router, and eventsClient.
// eventsClient is NILABLE — pass nil for a caller that never touches the
// AsyncAPI spec at all, mirroring [events.Subscriber.Handle]'s own
// "client optional, nil = spec-free" contract.
func newCaller(client MQTTClient, router MQTTRouter, eventsClient *events.Client) *caller {
	return &caller{client: client, router: router, events: eventsClient}
}

// subscribe is the value-based convenience tier — mirrors [nethttp.Call]
// taking a Route value directly. It builds the handle internally via
// sub.Handle(caller.events) (caller.events MAY be nil for a spec-free
// handle — the common case for an application subscribing without also
// registering a spec), then behaves identically to
// [SubscribeWithHandle]. fn is STILL a call-time param, matching the
// imperative "here's my handler, start consuming now" mental model.
//
// Net delta over the pre-redesign handle-based call:
//
//	// OLD (still available as SubscribeWithHandle):
//	err := mqtt5.SubscribeWithHandle(ctx, client, router, handle, qos, fn, opts)
//
//	// NEW:
//	caller := mqtt5.newCaller(client, router, nil) // nil = no spec
//	err := mqtt5.subscribe(ctx, caller, sub, qos, fn, opts)
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
	return subscribeWithHandle(ctx, caller.client, caller.router, handle, qos, fn, opts, formats...)
}

// serveOneSubscriber builds a scratch, single-channel [*events.Client],
// registers sub+fn+opts into it via [events.Subscriber.WithHandler]/
// [events.Subscriber.Register], and blocks serving just that one channel
// via [(*caller).ServeSubscribers] — pure sugar, implemented on top of
// the same registry/dispatch path as the whole-client entry point, not a
// bypass. caller's OWN client/router are reused; caller's OWN events
// field (if any) is left untouched — a fresh, throwaway [*events.Client]
// is used for the scratch registration instead, mirroring
// [nethttp.ServeOne]'s "build a scratch single-route Builder" shape.
func serveOneSubscriber[T any](
	ctx context.Context,
	caller *caller,
	sub events.Subscriber[T],
	qos byte,
	fn func(context.Context, T) error,
	opts SubscribeOptions,
	formats ...format.Format[T],
) error {
	opts.QoS = qos
	scratch := events.NewClient(events.WithInfo(events.Info{}))
	if err := sub.WithHandler(fn).WithOptions(opts).Register(scratch); err != nil {
		return err
	}
	_ = formats // formats are captured by the built handle's Formats/SubscribeFormats, not needed here directly.
	one := newCaller(caller.client, caller.router, scratch)
	return one.ServeSubscribers(ctx)
}

// ServeSubscribers implements [events.SubscriberServer]. It walks every
// [events.Subscriber] registered against caller.events via
// [events.Subscriber.Register] (see [events.Client.SubscriberEntries]) and
// starts consuming each one, ONE GOROUTINE PER SUBSCRIBE ROUTE, blocking
// until ctx is cancelled or all goroutines exit. Entries where
// !HasHandler() are skipped defensively — [events.Subscriber.Register]
// already guarantees a handler, so this should never trigger in practice.
//
// Per-channel dispatch options ([SubscribeOptions], including QoS and
// TopicFilter) are recovered from [events.ChannelHandle.HandlerOpts] —
// attached via [events.Subscriber.WithOptions] — exactly like
// [adapters/nethttp]'s resolveOptions pattern; a nil HandlerOpts uses
// SubscribeOptions{} (QoS 0, no explicit TopicFilter).
//
// reflect.Value.Call is used to invoke the type-erased Handler and Decode
// functions stored on each entry's *[events.ChannelHandle][T] (T is erased
// at this call site — entries are a heterogeneous []events.SubscriberEntry)
// — isolated ENTIRELY inside this package, mirroring
// [adapters/nethttp]'s own generic dispatch mechanism for [rest.Serve].
func (c *caller) ServeSubscribers(ctx context.Context) error {
	if c.events == nil {
		return nil
	}
	entries := c.events.SubscriberEntries()

	g, gctx := errgroup.WithContext(ctx)
	for _, entry := range entries {
		if !entry.HasHandler() {
			continue // defensive — Register() already guarantees a handler.
		}
		entry := entry
		info, err := extractErasedSubscriberHandle(entry.Handle())
		if err != nil {
			return err
		}
		if err := validateSubscribeImplementationShapesReflect(info.msgType, info.implementations); err != nil {
			return err
		}
		opts, err := resolveSubscribeOptions(info.topic, info.handlerOptsAny)
		if err != nil {
			return err
		}
		filter := opts.TopicFilter
		if filter == "" {
			filter = deriveWildcardFilter(info.topic)
		}

		g.Go(func() error {
			return c.serveOneEntry(gctx, filter, opts, info)
		})
	}
	return g.Wait()
}

// serveOneEntry registers and subscribes exactly one erased subscriber
// entry against c.client/c.router, blocking until ctx is cancelled.
func (c *caller) serveOneEntry(ctx context.Context, filter string, opts SubscribeOptions, info erasedSubscriberHandle) error {
	obs := opts.Observer
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}

	handler := makeErasedSubscribeMessageHandler(ctx, info, obs, opts)
	c.router.RegisterHandler(filter, handler)

	_, err := c.client.Subscribe(ctx, &pahomqtt5.Subscribe{
		Subscriptions: []pahomqtt5.SubscribeOptions{{Topic: filter, QoS: opts.QoS}},
	})
	if err != nil {
		c.router.UnregisterHandler(filter)
		return BrokerError{Op: "subscribe", Err: err}
	}
	<-ctx.Done()
	return nil
}

// OptionsShapeError is returned by [(*caller).ServeSubscribers] when a
// channel's [events.ChannelHandle.HandlerOpts] value's concrete type is
// not [SubscribeOptions] — mirrors adapters/nethttp's OptionsShapeError.
type OptionsShapeError struct {
	Topic string
	Got   any
}

func (e OptionsShapeError) Error() string {
	return fmt.Sprintf("mqtt5: %s: WithOptions value has wrong type: want mqtt5.SubscribeOptions, got %T", e.Topic, e.Got)
}

// resolveSubscribeOptions type-asserts a channel's type-erased HandlerOpts
// (from [events.Subscriber.WithOptions]) to [SubscribeOptions]. nil means
// WithOptions was never called — the adapter's zero-value SubscribeOptions
// apply (QoS 0, no explicit TopicFilter).
func resolveSubscribeOptions(topic string, handlerOpts any) (SubscribeOptions, error) {
	if handlerOpts == nil {
		return SubscribeOptions{}, nil
	}
	opts, ok := handlerOpts.(SubscribeOptions)
	if !ok {
		return SubscribeOptions{}, OptionsShapeError{Topic: topic, Got: handlerOpts}
	}
	return opts, nil
}

// erasedSubscriberHandle holds every field [ServeSubscribers]'s per-entry
// dispatch needs, recovered via reflect from a type-erased
// *[events.ChannelHandle][T] (entry.Handle() returns any). Non-generic
// fields (Topic, Descriptor, SecuritySchemes, GlobalSecurity,
// Implementations, HandlerOpts) are recovered via a direct type assertion
// on their already-concrete, non-generic Go types; only Decode/Handler
// (genuinely T-generic) are kept as raw reflect.Value, invoked later via
// reflect.Value.Call.
type erasedSubscriberHandle struct {
	topic           string
	descriptor      asyncapi.ChannelItem
	securitySchemes map[string]events.SecurityScheme
	globalSecurity  []route.SecurityRequirement
	implementations []middleware.ServerImplementation
	handlerOptsAny  any
	decodeFn        reflect.Value
	handlerFn       reflect.Value
	msgType         reflect.Type
}

// extractErasedSubscriberHandle recovers an [erasedSubscriberHandle] from
// handleAny (a *[events.ChannelHandle][T] stored as any).
func extractErasedSubscriberHandle(handleAny any) (erasedSubscriberHandle, error) {
	hv := reflect.ValueOf(handleAny)
	if hv.Kind() != reflect.Pointer || hv.IsNil() {
		return erasedSubscriberHandle{}, fmt.Errorf("mqtt5.ServeSubscribers: expected non-nil *events.ChannelHandle[T], got %T", handleAny)
	}
	elem := hv.Elem()
	handlerFn := elem.FieldByName("Handler")
	if handlerFn.IsNil() {
		return erasedSubscriberHandle{}, fmt.Errorf("mqtt5.ServeSubscribers: channel has no handler (internal error — HasHandler should have skipped it)")
	}
	descriptor, _ := elem.FieldByName("Descriptor").Interface().(asyncapi.ChannelItem)
	securitySchemes, _ := elem.FieldByName("SecuritySchemes").Interface().(map[string]events.SecurityScheme)
	globalSecurity, _ := elem.FieldByName("GlobalSecurity").Interface().([]route.SecurityRequirement)
	implementations, _ := elem.FieldByName("Implementations").Interface().([]middleware.ServerImplementation)
	return erasedSubscriberHandle{
		topic:           elem.FieldByName("Topic").String(),
		descriptor:      descriptor,
		securitySchemes: securitySchemes,
		globalSecurity:  globalSecurity,
		implementations: implementations,
		handlerOptsAny:  elem.FieldByName("HandlerOpts").Interface(),
		decodeFn:        elem.FieldByName("Decode"),
		handlerFn:       handlerFn,
		msgType:         handlerFn.Type().In(1),
	}, nil
}

// subscribeSecurityFnType builds the reflect.Type for the security shape
// (func(context.Context, *pahomqtt5.Publish, *T) (map[string][]string, error))
// for the given erased msgType T.
func subscribeSecurityFnType(msgType reflect.Type) reflect.Type {
	return reflect.FuncOf(
		[]reflect.Type{
			reflect.TypeOf((*context.Context)(nil)).Elem(),
			reflect.TypeOf((*pahomqtt5.Publish)(nil)),
			reflect.PointerTo(msgType),
		},
		[]reflect.Type{
			reflect.TypeOf(map[string][]string(nil)),
			reflect.TypeOf((*error)(nil)).Elem(),
		},
		false,
	)
}

// generalWrapFnType builds the reflect.Type for the general-purpose
// wrapping shape (func(next func(context.Context, T) error) func(context.Context, T) error)
// for the given erased msgType T. Shared by both SubscribeMW and
// PublishMW dispatch (deliberate symmetry — see docs/roadmap/
// d-0002-pubsub-workflow-simplification.md's "General-purpose (non-spec) Fn
// shapes" subsection).
func generalWrapFnType(msgType reflect.Type) reflect.Type {
	handlerType := reflect.FuncOf(
		[]reflect.Type{reflect.TypeOf((*context.Context)(nil)).Elem(), msgType},
		[]reflect.Type{reflect.TypeOf((*error)(nil)).Elem()},
		false,
	)
	return reflect.FuncOf([]reflect.Type{handlerType}, []reflect.Type{handlerType}, false)
}

// validateSubscribeImplementationShapesReflect is
// [validateSubscribeImplementationShapes]'s reflect-based equivalent, used
// by [(*caller).ServeSubscribers] where T is erased.
func validateSubscribeImplementationShapesReflect(msgType reflect.Type, impls []middleware.ServerImplementation) error {
	secT := subscribeSecurityFnType(msgType)
	genT := generalWrapFnType(msgType)
	for _, impl := range impls {
		if impl.Fn == nil {
			continue
		}
		fnType := reflect.TypeOf(impl.Fn)
		if fnType == secT || fnType == genT {
			continue
		}
		return middleware.MiddlewareShapeError{
			Name:     impl.Name,
			Expected: "func(context.Context, *pahomqtt5.Publish, *T) (map[string][]string, error) or func(func(context.Context, T) error) func(context.Context, T) error",
			Got:      fmt.Sprintf("%T", impl.Fn),
		}
	}
	return nil
}

// runSubscribeSecurityImplsReflect is [runSubscribeSecurityImpls]'s
// reflect-based equivalent — msgPtr is an addressable *T reflect.Value (T
// erased).
func runSubscribeSecurityImplsReflect(ctx context.Context, msg *pahomqtt5.Publish, msgPtr reflect.Value, secReqs []route.SecurityRequirement, impls []middleware.ServerImplementation) error {
	granted := make(map[string][]string)
	for _, impl := range impls {
		fnVal := reflect.ValueOf(impl.Fn)
		if !fnVal.IsValid() || fnVal.Kind() != reflect.Func || fnVal.Type().NumIn() != 3 {
			continue // general-purpose or nil
		}
		if len(impl.Satisfies) > 0 && len(secReqs) == 0 {
			continue
		}
		results := fnVal.Call([]reflect.Value{reflect.ValueOf(ctx), reflect.ValueOf(msg), msgPtr})
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

// wrapHandlerGeneralReflect is [wrapSubscribeGeneral]'s reflect-based
// equivalent — handlerVal is a func(context.Context, T) error reflect.Value
// (T erased).
func wrapHandlerGeneralReflect(handlerVal reflect.Value, impls []middleware.ServerImplementation) reflect.Value {
	h := handlerVal
	for i := len(impls) - 1; i >= 0; i-- {
		fnVal := reflect.ValueOf(impls[i].Fn)
		if !fnVal.IsValid() || fnVal.Kind() != reflect.Func || fnVal.Type().NumIn() != 1 {
			continue // security-shaped or nil
		}
		h = fnVal.Call([]reflect.Value{h})[0]
	}
	return h
}

// makeErasedSubscribeMessageHandler builds the [pahomqtt5.MessageHandler]
// [(*caller).serveOneEntry] registers with the router — the
// [ServeSubscribers] dispatch equivalent of [makeSubscribeMessageHandler],
// operating on an erased [erasedSubscriberHandle] via reflect.Value.Call
// instead of a generic *[events.ChannelHandle][T]. Runs the SAME
// built-in codec-based credential check + Implementations-based security
// check + general-purpose wrapping [makeSubscribeMessageHandler] runs;
// does NOT support [events.ChannelHandle.MergeFields]-based topic-var
// auto-merge (a known simplification of this reflect dispatch path — see
// this package's doc.go/the accompanying roadmap phase notes).
func makeErasedSubscribeMessageHandler(ctx context.Context, info erasedSubscriberHandle, obs stats.Observer, opts SubscribeOptions) pahomqtt5.MessageHandler {
	return func(msg *pahomqtt5.Publish) {
		start := time.Now()
		msgCtx := context.WithValue(ctx, contextKey{}, msg)
		if msg.Properties != nil && len(msg.Properties.User) > 0 {
			msgCtx = context.WithValue(msgCtx, userPropsKey{}, msg.Properties.User)
		}

		valuePtr := reflect.New(info.msgType)
		decodeResults := info.decodeFn.Call([]reflect.Value{reflect.ValueOf(msg.Payload)})
		if err, _ := decodeResults[1].Interface().(error); err != nil {
			stats.ReportErrors(obs, "payload", err)
			obs.RecordSubscribe(msg.Topic, false, time.Since(start))
			if opts.OnError != nil {
				opts.OnError(SubscribeError{Kind: KindDecode, Topic: msg.Topic, Err: err})
			}
			return
		}
		valuePtr.Elem().Set(decodeResults[0])

		if propErr := validateUserProperties(msg, opts.UserPropertyParams); propErr != nil {
			obs.RecordValidationError("user_property", stats.ConstraintName(propErr), userPropertyName(propErr))
			obs.RecordSubscribe(msg.Topic, false, time.Since(start))
			if opts.OnError != nil {
				opts.OnError(SubscribeError{Kind: KindSecurity, Topic: msg.Topic, Err: propErr})
			}
			return
		}

		var secReqs []route.SecurityRequirement
		if info.descriptor.Subscribe != nil {
			secReqs = info.descriptor.Subscribe.Security
		}
		if secReqs == nil {
			secReqs = info.globalSecurity
		}
		if len(secReqs) > 0 {
			if err := runErasedBuiltinSecurityCheck(msg, secReqs, info.securitySchemes); err != nil {
				if secObs, ok := obs.(stats.SecurityObserver); ok {
					secObs.RecordSecurityRejection(msg.Topic, firstScheme(secReqs))
				}
				obs.RecordSubscribe(msg.Topic, false, time.Since(start))
				if opts.OnError != nil {
					opts.OnError(SubscribeError{Kind: KindSecurity, Topic: msg.Topic, Err: err})
				}
				return
			}
			if opts.SecurityFunc != nil {
				if err := opts.SecurityFunc(msgCtx, msg, secReqs); err != nil {
					if secObs, ok := obs.(stats.SecurityObserver); ok {
						secObs.RecordSecurityRejection(msg.Topic, firstScheme(secReqs))
					}
					obs.RecordSubscribe(msg.Topic, false, time.Since(start))
					if opts.OnError != nil {
						opts.OnError(SubscribeError{Kind: KindSecurity, Topic: msg.Topic, Err: events.SecurityError{Err: err}})
					}
					return
				}
			}
		}

		if len(info.implementations) > 0 {
			if err := runSubscribeSecurityImplsReflect(msgCtx, msg, valuePtr, secReqs, info.implementations); err != nil {
				if secObs, ok := obs.(stats.SecurityObserver); ok {
					secObs.RecordSecurityRejection(msg.Topic, firstScheme(secReqs))
				}
				obs.RecordSubscribe(msg.Topic, false, time.Since(start))
				if opts.OnError != nil {
					opts.OnError(SubscribeError{Kind: KindSecurity, Topic: msg.Topic, Err: events.SecurityError{Err: err}})
				}
				return
			}
		}

		wrappedHandler := wrapHandlerGeneralReflect(info.handlerFn, info.implementations)
		var spanCtx = msgCtx
		if to, ok := obs.(stats.TraceObserver); ok {
			spanCtx = to.StartSpan(msgCtx, "mqtt5.subscribe", msg.Topic)
		}
		results := wrappedHandler.Call([]reflect.Value{reflect.ValueOf(spanCtx), valuePtr.Elem()})
		if to, ok := obs.(stats.TraceObserver); ok {
			fnErr, _ := results[0].Interface().(error)
			to.EndSpan(spanCtx, fnErr)
		}
		if fnErr, _ := results[0].Interface().(error); fnErr != nil {
			stats.ReportErrors(obs, "topic_var", fnErr)
			obs.RecordSubscribe(msg.Topic, false, time.Since(start))
			if opts.OnError != nil {
				opts.OnError(SubscribeError{Kind: KindHandler, Topic: msg.Topic, Err: fnErr})
			}
			return
		}
		obs.RecordSubscribe(msg.Topic, true, time.Since(start))
	}
}

// runErasedBuiltinSecurityCheck runs the SAME built-in codec-based
// credential check [makeSubscribeMessageHandler] runs, against a
// map[string]events.SecurityScheme (already non-generic — no reflect
// needed for this part).
func runErasedBuiltinSecurityCheck(msg *pahomqtt5.Publish, secReqs []route.SecurityRequirement, schemes map[string]events.SecurityScheme) error {
	schemeTypes := make(map[string]route.SecurityScheme, len(schemes))
	schemeCodecs := make(map[string]*codex.Codec[string], len(schemes))
	for name, s := range schemes {
		schemeTypes[name] = s.SecurityScheme
		schemeCodecs[name] = s.Codec
	}
	var userProps pahomqtt5.UserProperties
	if msg.Properties != nil {
		userProps = msg.Properties.User
	}
	if name, err := validateSecurityCredentials(userProps, secReqs, schemeTypes, schemeCodecs); err != nil {
		return events.SecurityCredentialError{Scheme: name, Err: err}
	}
	return nil
}

// Observability builds a general-purpose
// `func(next func(context.Context, T) error) func(context.Context, T) error`
// closure — attach it via `sub.SubscribeMW(nil, mqtt5.Observability[T](topic, obs))`
// or `pub.PublishMW(nil, mqtt5.Observability[T](topic, obs))` for
// declare-time, per-channel observability, mirroring
// [adapters/nethttp.Observability]'s role on the REST side (see
// docs/design/d-0002-pubsub-workflow-simplification.md's "General-purpose
// (non-spec) Fn shapes" subsection). topic identifies the channel in
// recorded stats — pass the channel's own topic (the same string passed
// to [events.NewChannel]).
//
// This is an ADDITIONAL, opt-in mechanism — [SubscribeOptions.Observer]/
// [PublishOptions.Observer] (resolved from ctx when nil) remain the
// PRIMARY, zero-ceremony path for the common case; Observability is for
// callers who want observability declared ONCE, consistently applied
// regardless of which specific [subscribe]/[SubscribeWithHandle]/
// [Publish]/[ServeSubscribers] call site is used.
//
// Direction (subscribe vs. publish) is detected via [MessageFromContext]:
// present (the subscribe-side dispatch path always stores it) ->
// [stats.Observer.RecordSubscribe]; absent -> [stats.Observer.RecordPublish].
func Observability[T any](topic string, obs stats.Observer) func(func(context.Context, T) error) func(context.Context, T) error {
	return func(next func(context.Context, T) error) func(context.Context, T) error {
		return func(ctx context.Context, msg T) error {
			start := time.Now()
			var spanCtx = ctx
			if to, ok := obs.(stats.TraceObserver); ok {
				spanCtx = to.StartSpan(ctx, "mqtt5.observability", topic)
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

var _ events.SubscriberServer = (*caller)(nil)
