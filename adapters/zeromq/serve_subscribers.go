package zeromq

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/middleware"
	asyncapi "github.com/DaniDeer/go-codex/render/asyncapi/v3"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/stats"
)

// ── ServeSubscribers / ServeOneSubscriber ─────────────────────────────────────
//
// Design note — why ServeSubscribers uses ONE shared receive loop, NOT one
// goroutine per subscribe route:
//
// The internal caller type bundles exactly ONE [FramedSocket] (confirmed via
// docs/design/d-0002-pubsub-workflow-simplification.md's zeromq Caller
// subsection — 2 fields, sock+events, no per-route socket). A
// [FramedSocket] is a thin wrapper around a single underlying ZMQ socket
// instance; ZMQ sockets are NOT safe for concurrent use from multiple
// goroutines (this is true of every real binding, e.g. pebbe/zmq4 — see
// [FramedSocket]'s own doc comment). Since every [events.SubscriberEntry]
// walked by ServeSubscribers shares the SAME caller.sock, running one
// goroutine per route (each independently calling sock.RecvFrames())
// would race on the same socket. ZeroMQ PUB/SUB sockets DO support
// subscribing to multiple prefixes on ONE socket via repeated
// [FramedSocket.SetSubscription] calls — so ServeSubscribers calls
// SetSubscription once per registered channel's resolved filter, then
// runs a SINGLE shared receive loop that dispatches each incoming
// [topic, payload] message to whichever registered route's topic
// template structurally matches (see [dispatchToRoute]) — safe,
// correct, and still lets every registered channel be consumed from one
// ServeSubscribers call.

// subscriberRoute holds one compiled, ready-to-dispatch
// [events.SubscriberEntry] — built once by [buildSubscriberRoute] before
// the shared receive loop starts, so a malformed attached
// [middleware.ServerImplementation] Fn fails loudly at ServeSubscribers
// construction time, never silently at message time.
type subscriberRoute struct {
	topic      string // the channel's topic template, e.g. "sensors/{sensorID}/data"
	filter     string // the resolved ZMQ subscription prefix filter
	handleVal  reflect.Value
	next       reflect.Value // func(context.Context, T) error — Handler wrapped with general-purpose MW
	securityFn reflect.Value // opts.SecurityFunc equivalent extracted from HandlerOpts; may be invalid (zero Value)
	// implSecurity holds every attached security-shaped
	// [middleware.ServerImplementation] (from [events.Subscriber.SubscribeMW]),
	// run in attachment order after securityFn, mirroring
	// [runSubscribeSecurityImpls]'s ordering on the generic
	// [SubscribeWithHandle] path.
	implSecurity []middleware.ServerImplementation
	secReqs      []route.SecurityRequirement
	onError      func(SubscribeError)
	observer     stats.Observer
}

// buildSubscriberRoute compiles entry into a [subscriberRoute] via
// reflect — mirrors [adapters/nethttp.buildRouteHandler]'s "walk a
// heterogeneous collection using only the handle's ALREADY-concrete
// exported closures, invoked via reflect.Value.Call" technique, since
// ServeSubscribers is non-generic and entry.Handle() returns
// *events.ChannelHandle[T] type-erased to any.
func buildSubscriberRoute(entry events.SubscriberEntry) (*subscriberRoute, error) {
	handleAny := entry.Handle()
	hv := reflect.ValueOf(handleAny)
	if hv.Kind() != reflect.Pointer || hv.IsNil() {
		return nil, fmt.Errorf("zeromq.ServeSubscribers: expected non-nil *events.ChannelHandle[T], got %T", handleAny)
	}
	elem := hv.Elem()
	topic := entry.Topic()

	handlerVal := elem.FieldByName("Handler")
	if !handlerVal.IsValid() || handlerVal.IsNil() {
		return nil, fmt.Errorf("zeromq.ServeSubscribers: topic %q has no handler (internal error — HasHandler should have skipped it)", topic)
	}
	msgType := handlerVal.Type().In(1) // T

	impls, _ := elem.FieldByName("Implementations").Interface().([]middleware.ServerImplementation)
	if err := validateSubscribeImplementationShapesReflect(topic, msgType, impls); err != nil {
		return nil, err
	}

	descriptor, _ := elem.FieldByName("Descriptor").Interface().(asyncapi.ChannelItem)
	globalSecurity, _ := elem.FieldByName("GlobalSecurity").Interface().([]route.SecurityRequirement)
	var secReqs []route.SecurityRequirement
	if descriptor.Subscribe != nil {
		secReqs = descriptor.Subscribe.Security
	}
	if secReqs == nil {
		secReqs = globalSecurity
	}

	handlerOptsAny := elem.FieldByName("HandlerOpts").Interface()
	resolved, err := resolveSubscribeOptsReflect(topic, handlerOptsAny)
	if err != nil {
		return nil, err
	}

	filter := resolved.topicFilter
	if filter == "" {
		filter = deriveTopicPrefix(topic)
	}

	next := handlerVal
	for i := len(impls) - 1; i >= 0; i-- {
		fnVal := reflect.ValueOf(impls[i].Fn)
		if !fnVal.IsValid() || fnVal.Kind() != reflect.Func || fnVal.Type().NumIn() != 1 {
			continue // security-shaped or nil — not the general-purpose shape
		}
		next = fnVal.Call([]reflect.Value{next})[0]
	}

	// Every security-shaped impl.Fn (NumIn == 3) is collected separately
	// from the general-purpose ones already folded into next above — see
	// [subscriberRoute.implSecurity]'s doc comment.
	var securityImpls []middleware.ServerImplementation
	for _, impl := range impls {
		fnVal := reflect.ValueOf(impl.Fn)
		if fnVal.IsValid() && fnVal.Kind() == reflect.Func && fnVal.Type().NumIn() == 3 {
			securityImpls = append(securityImpls, impl)
		}
	}

	return &subscriberRoute{
		topic:        topic,
		filter:       filter,
		handleVal:    hv,
		next:         next,
		securityFn:   resolved.securityFn,
		implSecurity: securityImpls,
		secReqs:      secReqs,
		onError:      resolved.onError,
		observer:     resolved.observer,
	}, nil
}

// validateSubscribeImplementationShapesReflect is
// [validateSubscribeImplementationShapes]'s reflect-based equivalent —
// msgType is the route's T, recovered at runtime via reflect (see
// [buildSubscriberRoute]), letting ServeSubscribers build the EXACT
// expected security/general-purpose Fn shapes dynamically instead of via
// a static type parameter. Mirrors
// [adapters/nethttp.validateImplementationShapesReflect].
func validateSubscribeImplementationShapesReflect(topic string, msgType reflect.Type, impls []middleware.ServerImplementation) error {
	securityType := reflect.FuncOf(
		[]reflect.Type{
			reflect.TypeOf((*context.Context)(nil)).Elem(),
			reflect.PointerTo(msgType),
			reflect.TypeOf([]route.SecurityRequirement(nil)),
		},
		[]reflect.Type{reflect.TypeOf((*error)(nil)).Elem()},
		false,
	)
	handlerFnType := reflect.FuncOf(
		[]reflect.Type{reflect.TypeOf((*context.Context)(nil)).Elem(), msgType},
		[]reflect.Type{reflect.TypeOf((*error)(nil)).Elem()},
		false,
	)
	generalType := reflect.FuncOf([]reflect.Type{handlerFnType}, []reflect.Type{handlerFnType}, false)

	for _, impl := range impls {
		if impl.Fn == nil {
			continue
		}
		fnType := reflect.TypeOf(impl.Fn)
		if fnType == securityType || fnType == generalType {
			continue
		}
		return middleware.MiddlewareShapeError{
			Name:     impl.Name,
			Expected: "func(context.Context, *T, []route.SecurityRequirement) error or func(func(context.Context, T) error) func(context.Context, T) error",
			Got:      fmt.Sprintf("%T", impl.Fn),
		}
	}
	return nil
}

// resolvedSubscribeOpts is the reflect-recovered subset of a
// [SubscribeOptions.HandlerOpts][T] value that ServeSubscribers needs —
// recovered field-by-field via reflect (rather than a whole-struct type
// assertion, which is not possible against a generic type from
// non-generic code) since ServeSubscribers never knows T at compile time.
type resolvedSubscribeOpts struct {
	topicFilter string
	onError     func(SubscribeError)
	observer    stats.Observer
	securityFn  reflect.Value // func(context.Context, *T, []route.SecurityRequirement) error; invalid (zero Value) if unset
}

// zeromqPkgPath is this package's import path, used by
// resolveSubscribeOptsReflect to distinguish a genuine
// [SubscribeOptions][T] value (for ANY T) from an unrelated/wrong-package
// struct passed by caller mistake to [events.Subscriber.WithOptions].
const zeromqPkgPath = "github.com/DaniDeer/go-codex/adapters/zeromq"

// resolveSubscribeOptsReflect recovers SubscribeOptions[T] fields from
// handlerOptsAny (a [Subscriber.WithOptions]-attached value, or nil).
// Mirrors [adapters/nethttp.resolveOptions]'s "type-erased HandlerOpts"
// pattern, adapted to a field-by-field reflect recovery since
// SubscribeOptions is itself generic over T (which ServeSubscribers, a
// non-generic method, cannot name to build the exact expected struct
// type to type-assert against). Returns [OptionsShapeError] when
// handlerOptsAny is non-nil but is not a [SubscribeOptions][T] value for
// any T — mirrors [adapters/mqtt5.OptionsShapeError]/
// [adapters/mqtt.OptionsShapeError]'s identical caller-mistake check; a
// consistency review found this reflect-based recovery previously
// discarded a wrong-shaped value SILENTLY (no error, no diagnostic),
// unlike mqtt5/mqtt's explicit type assertion — fixed to match.
func resolveSubscribeOptsReflect(topic string, handlerOptsAny any) (resolvedSubscribeOpts, error) {
	var out resolvedSubscribeOpts
	if handlerOptsAny == nil {
		return out, nil
	}
	v := reflect.ValueOf(handlerOptsAny)
	t := v.Type()
	if v.Kind() != reflect.Struct || t.PkgPath() != zeromqPkgPath || !strings.HasPrefix(t.Name(), "SubscribeOptions[") {
		return out, OptionsShapeError{Topic: topic, Got: handlerOptsAny}
	}
	if f := v.FieldByName("TopicFilter"); f.IsValid() && f.Kind() == reflect.String {
		out.topicFilter = f.String()
	}
	if f := v.FieldByName("OnError"); f.IsValid() && f.Kind() == reflect.Func && !f.IsNil() {
		if fn, ok := f.Interface().(func(SubscribeError)); ok {
			out.onError = fn
		}
	}
	if f := v.FieldByName("Observer"); f.IsValid() {
		if obs, ok := f.Interface().(stats.Observer); ok {
			out.observer = obs
		}
	}
	if f := v.FieldByName("SecurityFunc"); f.IsValid() && f.Kind() == reflect.Func && !f.IsNil() {
		out.securityFn = f
	}
	return out, nil
}

// matchTopicTemplate is defined in topicvars.go — non-generic (operates
// purely on strings), so ServeSubscribers's shared receive loop can call
// it directly without any reflect involvement, matching each incoming
// concrete topic against every route's template in turn.

// dispatchToRoute finds the first route whose topic template structurally
// matches the concrete topic and processes the message on it. A topic
// matching NO route is dropped silently — this is expected, not an
// error: a broader byte-prefix subscription (see [deriveTopicPrefix])
// necessarily receives some non-matching concrete topics sharing the
// same prefix too (confirmed safe via
// docs/design/d-0002-pubsub-workflow-simplification.md's bug-fix subsection —
// the SAME reasoning [SubscribeWithHandle]'s own merge-field mismatch
// handling already relies on).
func dispatchToRoute(ctx context.Context, routes []*subscriberRoute, topic string, payload []byte) {
	for _, r := range routes {
		vars, err := matchTopicTemplate(r.topic, topic)
		if err != nil {
			continue
		}
		r.processMessage(ctx, topic, payload, vars)
		return
	}
}

// callErrMethod calls handleVal's exported method name with a single
// concrete (non-generic) argument arg, returning the (error) result —
// used for [events.ChannelHandle.ValidateTopic]/ValidateTopicVars, whose
// signatures do not depend on T.
func callErrMethod(handleVal reflect.Value, name string, arg any) error {
	m := handleVal.MethodByName(name)
	results := m.Call([]reflect.Value{reflect.ValueOf(arg)})
	err, _ := results[0].Interface().(error)
	return err
}

func (r *subscriberRoute) reportError(se SubscribeError) {
	if r.onError != nil {
		r.onError(se)
	}
}

// processMessage runs the full decode → merge → security → handler
// pipeline for one incoming message on this route — the ServeSubscribers
// mirror of [SubscribeWithHandle]'s inline per-message logic, expressed
// via reflect since T is erased here.
func (r *subscriberRoute) processMessage(ctx context.Context, topic string, payload []byte, vars map[string]string) {
	obs := r.observer
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}
	start := time.Now()

	if err := callErrMethod(r.handleVal, "ValidateTopic", topic); err != nil {
		stats.ReportErrors(obs, "topic", err)
		obs.RecordSubscribe(topic, false, time.Since(start))
		r.reportError(SubscribeError{Kind: KindDecode, Topic: topic, Err: err})
		return
	}
	if err := callErrMethod(r.handleVal, "ValidateTopicVars", vars); err != nil {
		stats.ReportErrors(obs, "topic_var", err)
		obs.RecordSubscribe(topic, false, time.Since(start))
		r.reportError(SubscribeError{Kind: KindDecode, Topic: topic, Err: err})
		return
	}

	decodeResults := r.handleVal.MethodByName("DecodeMerged").Call([]reflect.Value{
		reflect.ValueOf(payload), reflect.ValueOf(vars),
	})
	if decErr, _ := decodeResults[1].Interface().(error); decErr != nil {
		stats.ReportErrors(obs, "payload", decErr)
		obs.RecordSubscribe(topic, false, time.Since(start))
		r.reportError(SubscribeError{Kind: KindDecode, Topic: topic, Err: decErr})
		return
	}
	valueVal := decodeResults[0]

	if r.securityFn.IsValid() {
		msgPtr := reflect.New(valueVal.Type())
		msgPtr.Elem().Set(valueVal)
		out := r.securityFn.Call([]reflect.Value{reflect.ValueOf(ctx), msgPtr, reflect.ValueOf(r.secReqs)})
		if secErr, _ := out[0].Interface().(error); secErr != nil {
			if secObs, ok := obs.(stats.SecurityObserver); ok {
				secObs.RecordSecurityRejection(topic, firstSchemeName(r.secReqs))
			}
			obs.RecordSubscribe(topic, false, time.Since(start))
			r.reportError(SubscribeError{Kind: KindSecurity, Topic: topic, Err: events.SecurityError{Err: secErr}})
			return
		}
		valueVal = msgPtr.Elem()
	}
	for _, impl := range r.implSecurity {
		fnVal := reflect.ValueOf(impl.Fn)
		if len(impl.Satisfies) > 0 && len(r.secReqs) == 0 {
			continue
		}
		msgPtr := reflect.New(valueVal.Type())
		msgPtr.Elem().Set(valueVal)
		out := fnVal.Call([]reflect.Value{reflect.ValueOf(ctx), msgPtr, reflect.ValueOf(r.secReqs)})
		if secErr, _ := out[0].Interface().(error); secErr != nil {
			if secObs, ok := obs.(stats.SecurityObserver); ok {
				secObs.RecordSecurityRejection(topic, firstSchemeName(r.secReqs))
			}
			obs.RecordSubscribe(topic, false, time.Since(start))
			r.reportError(SubscribeError{Kind: KindSecurity, Topic: topic, Err: events.SecurityError{Err: secErr}})
			return
		}
		valueVal = msgPtr.Elem()
	}

	var spanCtx = ctx
	if to, ok := obs.(stats.TraceObserver); ok {
		spanCtx = to.StartSpan(ctx, "zmq.subscribe", topic)
	}
	results := r.next.Call([]reflect.Value{reflect.ValueOf(spanCtx), valueVal})
	fnErr, _ := results[0].Interface().(error)
	if to, ok := obs.(stats.TraceObserver); ok {
		to.EndSpan(spanCtx, fnErr)
	}
	if fnErr != nil {
		obs.RecordSubscribe(topic, false, time.Since(start))
		r.reportError(SubscribeError{Kind: KindHandler, Topic: topic, Err: fnErr})
		return
	}
	obs.RecordSubscribe(topic, true, time.Since(start))
}

// ServeSubscribers implements [events.SubscriberServer]: it walks every
// [events.Subscriber] registered against c.events via
// [events.Subscriber.Register] (via [events.Client.SubscriberEntries])
// and starts consuming all of them via ONE shared receive loop on
// c.sock (see this file's top-of-file design note for why a shared loop
// is used instead of one goroutine per route). Blocks until ctx is
// cancelled (returns nil) or a socket error occurs (returns the error).
//
// Entries with !HasHandler() are skipped defensively — [SubscriberEntry]
// guarantees every entry returned by SubscriberEntries has a handler, but
// ServeSubscribers checks anyway rather than trusting that invariant
// blindly.
//
// Returns nil immediately if c.events is nil or has no registered
// subscribers — a Caller built purely for [subscribe]/[Publish] calls
// (never [Subscriber.Register]) has nothing for ServeSubscribers to
// walk.
func (c *caller) ServeSubscribers(ctx context.Context) error {
	if c.events == nil {
		return nil
	}
	entries := c.events.SubscriberEntries()
	routes := make([]*subscriberRoute, 0, len(entries))
	for _, e := range entries {
		if !e.HasHandler() {
			continue
		}
		r, err := buildSubscriberRoute(e)
		if err != nil {
			return err
		}
		routes = append(routes, r)
	}
	if len(routes) == 0 {
		return nil
	}

	for _, r := range routes {
		if err := c.sock.SetSubscription(r.filter); err != nil {
			return SocketError{Op: "set_subscription", Err: err}
		}
	}
	if err := c.sock.SetRecvTimeout(recvPollInterval); err != nil {
		return SocketError{Op: "set_recv_timeout", Err: err}
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		frames, err := c.sock.RecvFrames()
		if errors.Is(err, ErrTimeout) {
			continue
		}
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				return SocketError{Op: "recv", Err: err}
			}
		}
		if len(frames) < 2 {
			continue // malformed: expect [topic, payload]
		}
		dispatchToRoute(ctx, routes, string(frames[0]), frames[1])
	}
}

// serveOneSubscriber is a convenience shortcut mirroring
// [adapters/nethttp.ServeOne]: builds a throwaway [events.Client] and
// internal caller (reusing caller.sock, so no second socket is ever
// opened), registers sub against it via [events.Subscriber.WithHandler]/
// [events.Subscriber.Register], and calls the internal ServeSubscribers
// path — for the common case of consuming EXACTLY ONE channel via the
// whole-client path without needing an [events.Client] pre-populated by
// the caller. Blocks exactly like ServeSubscribers.
//
// Unlike [subscribe]/[SubscribeWithHandle], serveOneSubscriber takes no
// call-time formats parameter — the internal ServeSubscribers reflect-
// based dispatch decodes via [events.ChannelHandle.DecodeMerged], which is
// JSON-only (it does not consult SubscribeFormats/Formats); declare a
// non-JSON format directly on the channel handle before calling
// serveOneSubscriber if needed.
func serveOneSubscriber[T any](
	ctx context.Context,
	caller *caller,
	sub events.Subscriber[T],
	fn func(context.Context, T) error,
	opts SubscribeOptions[T],
) error {
	ev := events.NewClient(events.WithInfo(events.Info{}))
	scoped := newCaller(caller.sock, ev)
	if opts.TopicFilter != "" || opts.OnError != nil || opts.Observer != nil || opts.SecurityFunc != nil {
		sub = sub.WithOptions(opts)
	}
	if err := sub.WithHandler(fn).Register(ev); err != nil {
		return err
	}
	return scoped.ServeSubscribers(ctx)
}
