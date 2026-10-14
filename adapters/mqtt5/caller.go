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
	"github.com/DaniDeer/go-codex/internal/route"
	asyncapi "github.com/DaniDeer/go-codex/render/asyncapi/v3"
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
// [subscribeWithHandle]. fn is STILL a call-time param, matching the
// imperative "here's my handler, start consuming now" mental model.
//
// Net delta over the pre-redesign handle-based call:
//
//	// OLD (still available as SubscribeWithHandle):
//	err := mqtt5.SubscribeWithHandle(ctx, client, router, handle, qos, fn, opts)
//
//	// NEW: QoS is supplied via opts.Capabilities (Phase 4 — the sole mechanism).
//	caller := mqtt5.newCaller(client, router, nil) // nil = no spec
//	err := mqtt5.subscribe(ctx, caller, sub, fn, opts)
func subscribe[T any](
	ctx context.Context,
	caller *caller,
	sub events.Subscriber[T],
	fn func(context.Context, T) error,
	opts SubscribeOptions,
	formats ...format.Format[T],
) error {
	handle, err := sub.Handle(caller.events)
	if err != nil {
		return err
	}
	return subscribeWithHandle(ctx, caller.client, caller.router, handle, fn, opts, formats...)
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
	fn func(context.Context, T) error,
	opts SubscribeOptions,
	formats ...format.Format[T],
) error {
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
		obsForCap := opts.Observer
		if obsForCap == nil {
			obsForCap = stats.ObserverFromContext(ctx)
		}
		// opts.Capabilities is the SOLE mechanism (docs/design/
		// d-0006-protocol-native-capabilities.md Phase 4/4b — the former
		// events.Subscribe.QoS declared-fallback field was REMOVED
		// entirely, closing that parallel non-Capability-shaped
		// declaration path), applied via the API-layer-owned
		// [events.ApplyCapabilities].
		var wire WireAttributes
		events.ApplyCapabilities(opts.Capabilities, &wire, obsForCap, info.topic)
		if covErr := events.VerifyCapabilityCoverage(info.topic, info.requirements, opts.Capabilities); covErr != nil {
			return covErr
		}
		filter := opts.TopicFilter
		if filter == "" {
			filter = deriveWildcardFilter(info.topic)
		}
		filter = applySharedSubscriptionGroup(filter, opts.SharedSubscriptionGroup)

		g.Go(func() error {
			return c.serveOneEntry(gctx, filter, wire.QoS, opts, info)
		})
	}
	return g.Wait()
}

// serveOneEntry registers and subscribes exactly one erased subscriber
// entry against c.client/c.router, blocking until ctx is cancelled. qos
// is the RESOLVED wire-level QoS (declared fallback + Capabilities
// already applied by [(*caller).ServeSubscribers]).
func (c *caller) serveOneEntry(ctx context.Context, filter string, qos byte, opts SubscribeOptions, info erasedSubscriberHandle) error {
	obs := opts.Observer
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}

	// General-purpose wrapping composes ONCE, outside the per-message
	// loop — mirrors `transport.go`'s identical
	// `fnVal = wrapHandlerGeneralReflect(fnVal, implementations)` timing
	// (Phase 3 case 6's per-message-re-wrap fix).
	wrappedFn := wrapHandlerGeneralReflect(info.handlerFn, info.implementations)
	// formatsOverride resolves opts.Formats (previously always the zero
	// value — SubscribeOptions had no Formats field at all, closed by
	// `deep-dive-bug-hunt`'s api/events follow-up) against the SAME
	// decodeMergedMethod variadic slice type `transport.Subscribe`'s own
	// `resolveFormatsOverride` call already resolves against.
	formatsOverride, formatsErr := resolveFormatsOverride(opts.Formats, info.decodeMergedMethod.Type().In(2))
	if formatsErr != nil {
		return formatsErr
	}

	// subscribeDispatchCore is the ONE shared reflect-based dispatch core
	// (Phase 4 events/reqreply family, Workstream A) — also used by
	// `transport.go`'s [(*transport).Subscribe] (the ports.Pattern-
	// binding path). See its own doc comment for the full unification
	// rationale.
	handler := subscribeDispatchCore(ctx, c.client, obs, info, wrappedFn, formatsOverride, opts.UserPropertyParams, opts.OnError)
	c.router.RegisterHandler(filter, handler)

	_, err := c.client.Subscribe(ctx, &pahomqtt5.Subscribe{
		Subscriptions: []pahomqtt5.SubscribeOptions{{Topic: filter, QoS: qos}},
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
	implementations []events.ServerImplementation
	handlerOptsAny  any
	handlerFn       reflect.Value
	msgType         reflect.Type
	// decodeMergedMethod is handleVal's own
	// [events.ChannelHandle.DecodeMergedWithFormats] method (reflect-bound
	// once here) — CORRECTED (Phase 3 case 6's 3-way reconciliation):
	// this field previously bound the plain Decode field, which has no
	// topic-template-var-merge capability at all, a confirmed gap
	// relative to `transport.go`'s identical reflection pattern (the
	// ports.Pattern-binding path), which has always used this exact
	// method. ServeSubscribers now derives topic vars via
	// [matchTopicTemplate] (see [makeErasedSubscribeMessageHandler]) and
	// passes them into THIS method, exactly mirroring `transport.go`.
	decodeMergedMethod reflect.Value
	// mergePropertyVarsMethod/propertyMergeFieldsLen are handleVal's own
	// [events.ChannelHandle.MergePropertyVars]/
	// [events.ChannelHandle.PropertyMergeFields] (reflect-bound once
	// here) — mirrors `transport.go`'s identical reflection pattern,
	// added here as part of the same case-6 reconciliation above.
	mergePropertyVarsMethod reflect.Value
	propertyMergeFieldsLen  int
	// requirements holds the channel's declared [events.CapabilityRequirement]
	// values, consulted by [events.CheckCapabilityCoverage].
	requirements []events.CapabilityRequirement
	// middlewareHandlers holds every attached codec-backed bound
	// [events.MiddlewareHandler] (from [events.Subscriber.SubscribeMW]'s
	// bound path) — CORRECTED: a prior revision of this struct had no
	// field for these at all, meaning Client.Attach+ServeSubscribers (the
	// PRIMARY recommended workflow) never dispatched bound codec-backed
	// middleware (Security or general-purpose) through this path — only
	// through the lower-level `subscribeWithHandle`/escape-hatch
	// functions. Found via an end-to-end example exercising
	// GrantedScopes through Client.Attach for the first time.
	middlewareHandlers []events.MiddlewareHandler
	// dispatchSubscribeMiddleware is handleVal's own
	// [events.ChannelHandle.DispatchSubscribeMiddleware] method
	// (reflect-bound once here, called per-message in
	// [makeErasedSubscribeMessageHandler]) — mirrors `transport.go`'s
	// identical reflection pattern.
	dispatchSubscribeMiddleware reflect.Value
	// errorResponseForMethod/deadLetterForMethod are handleVal's own
	// [events.ChannelHandle.ErrorResponseFor]/[events.ChannelHandle.DeadLetterFor]
	// methods (reflect-bound once here) — CORRECTED: this
	// ServeSubscribers dispatch previously consulted NEITHER mechanism
	// at ANY failure point, unlike [subscribeWithHandle] (the escape
	// hatch) and `transport.go`'s Subscribe (the ports.Pattern reflection
	// path), both of which already wire the full ErrorChannel→
	// DeadLetter→OnError fallback triplet. Mirrors `transport.go`'s
	// identical reflection pattern — see [makeErasedSubscribeMessageHandler]'s
	// dispatchFailure closure.
	errorResponseForMethod reflect.Value
	deadLetterForMethod    reflect.Value
}

// buildErasedSubscriberHandleFields fills in every [erasedSubscriberHandle]
// field recoverable from an already-obtained (handleVal, elem) pair —
// handleVal the reflect.Value of *[events.ChannelHandle][T] itself (for
// its own methods, e.g. DecodeMergedWithFormats), elem its Elem() (for
// its own fields, e.g. Topic/Descriptor). Shared by BOTH
// [extractErasedSubscriberHandle] (ServeSubscribers' per-entry path,
// which also populates handlerFn/handlerOptsAny from elem's own Handler/
// HandlerOpts fields below) and `transport.go`'s [(*transport).Subscribe]
// (the ports.Pattern-binding path, which supplies its OWN, separately
// call-time-validated handler function instead of reading elem's Handler
// field — msgType is derived from decodeMergedMethod's return type here
// specifically so BOTH callers can rely on it without needing a non-nil
// Handler field) — this is the Phase 4 events/reqreply family,
// Workstream A alignment step: previously `transport.go` re-derived every
// one of these fields independently via ad hoc `elem.FieldByName(...)`
// calls scattered through its own body; this is now the single place
// that logic lives.
func buildErasedSubscriberHandleFields(handleVal, elem reflect.Value) erasedSubscriberHandle {
	descriptor, _ := elem.FieldByName("Descriptor").Interface().(asyncapi.ChannelItem)
	securitySchemes, _ := elem.FieldByName("SecuritySchemes").Interface().(map[string]events.SecurityScheme)
	globalSecurity, _ := elem.FieldByName("GlobalSecurity").Interface().([]route.SecurityRequirement)
	implementations, _ := elem.FieldByName("Implementations").Interface().([]events.ServerImplementation)
	requirements, _ := elem.FieldByName("Requirements").Interface().([]events.CapabilityRequirement)
	middlewareHandlers, _ := elem.FieldByName("MiddlewareHandlers").Interface().([]events.MiddlewareHandler)
	decodeMergedMethod := handleVal.MethodByName("DecodeMergedWithFormats")
	return erasedSubscriberHandle{
		topic:                       elem.FieldByName("Topic").String(),
		descriptor:                  descriptor,
		securitySchemes:             securitySchemes,
		globalSecurity:              globalSecurity,
		implementations:             implementations,
		msgType:                     decodeMergedMethod.Type().Out(0),
		requirements:                requirements,
		middlewareHandlers:          middlewareHandlers,
		dispatchSubscribeMiddleware: handleVal.MethodByName("DispatchSubscribeMiddleware"),
		errorResponseForMethod:      handleVal.MethodByName("ErrorResponseFor"),
		deadLetterForMethod:         handleVal.MethodByName("DeadLetterFor"),
		decodeMergedMethod:          decodeMergedMethod,
		mergePropertyVarsMethod:     handleVal.MethodByName("MergePropertyVars"),
		propertyMergeFieldsLen:      handleVal.MethodByName("PropertyMergeFields").Call(nil)[0].Len(),
	}
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
	info := buildErasedSubscriberHandleFields(hv, elem)
	info.handlerOptsAny = elem.FieldByName("HandlerOpts").Interface()
	info.handlerFn = handlerFn
	return info, nil
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
// PublishMW dispatch (deliberate symmetry — see
// docs/design/d-0002-pubsub-workflow-simplification.md's "General-purpose
// (non-spec) Fn shapes" subsection).
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
func validateSubscribeImplementationShapesReflect(msgType reflect.Type, impls []events.ServerImplementation) error {
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
		return events.MiddlewareShapeError{
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
// runSubscribeSecurityImplsReflect runs every attached security-shaped
// legacy Implementations Fn and returns the merged grants map — it NO
// LONGER calls [events.CheckScopes] itself (previously did, in
// isolation). Callers (both `transport.go`'s ports.Pattern Subscribe AND
// `caller.go`'s own [makeErasedSubscribeMessageHandler]) merge this
// return with any bound MiddlewareHandler's own `GrantedScopes` (via
// [scopesmerge.MergeHandlerGrants]) before a SINGLE, UNIFIED CheckScopes
// call — mirrors `adapter.go`'s already-fixed `runSubscribeSecurityImpls`
// exactly (docs/design/d-0007-declarative-middleware-layering.md's
// "Prerequisite for Phase 2 (api/events)", belatedly also applied here).
func runSubscribeSecurityImplsReflect(ctx context.Context, msg *pahomqtt5.Publish, msgPtr reflect.Value, secReqs []route.SecurityRequirement, impls []events.ServerImplementation) (map[string][]string, error) {
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
			return nil, err
		}
		g, _ := results[0].Interface().(map[string][]string)
		for k, v := range g {
			granted[k] = v
		}
	}
	return granted, nil
}

// wrapHandlerGeneralReflect is [wrapSubscribeGeneral]'s reflect-based
// equivalent — handlerVal is a func(context.Context, T) error reflect.Value
// (T erased).
func wrapHandlerGeneralReflect(handlerVal reflect.Value, impls []events.ServerImplementation) reflect.Value {
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
// regardless of which specific [subscribe]/[subscribeWithHandle]/
// [publish]/[ServeSubscribers] call site is used.
//
// Delegates its shared, adapter-agnostic part (ctx-injection of obs via
// [stats.WithObserver], and draining [stats.DiagnosticsFromContext] into
// [stats.Observer.RecordValidationError]) to [events.Observability] — see
// that function's own doc comment for the full rationale. This wrapper
// adds the genuinely mqtt5-specific remainder on top:
// [stats.Observer.RecordSubscribe]/RecordPublish and a
// [stats.TraceObserver] span.
//
// Direction (subscribe vs. publish) is detected via [MessageFromContext]:
// present (the subscribe-side dispatch path always stores it) ->
// [stats.Observer.RecordSubscribe]; absent -> [stats.Observer.RecordPublish].
func Observability[T any](topic string, obs stats.Observer) func(func(context.Context, T) error) func(context.Context, T) error {
	return func(next func(context.Context, T) error) func(context.Context, T) error {
		shared := events.Observability[T](obs)(next)
		return func(ctx context.Context, msg T) error {
			start := time.Now()
			var spanCtx = ctx
			if to, ok := obs.(stats.TraceObserver); ok {
				spanCtx = to.StartSpan(ctx, "mqtt5.observability", topic)
			}
			err := shared(spanCtx, msg)
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
