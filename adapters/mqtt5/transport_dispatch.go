package mqtt5

import (
	"context"
	"fmt"
	"reflect"
	"time"

	pahomqtt5 "github.com/eclipse/paho.golang/paho"

	"github.com/DaniDeer/go-codex/adapters/internal/scopesmerge"
	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/internal/middleware"
	"github.com/DaniDeer/go-codex/internal/route"
	"github.com/DaniDeer/go-codex/stats"
)

// This file holds the PUBLISH-side reflection-only mirrors of adapter.go's
// generic security/general-middleware dispatch helpers
// (runPublishSecurityImpls[T]/wrapPublishGeneral[T]/
// validatePublishImplementationShapes[T]) plus a few small
// HandlerOpts-extraction/security-requirement-resolution helpers shared
// by BOTH transport.go's Publish and Subscribe — needed because
// transport.go's Client.Publish/Subscribe shim recovers T only at
// runtime (via reflection against the type-erased *events.ChannelHandle),
// so it cannot call the generic versions directly.
//
// The SUBSCRIBE-side equivalents (subscribeSecurityFnType/
// generalWrapFnType/validateSubscribeImplementationShapesReflect/
// runSubscribeSecurityImplsReflect/wrapHandlerGeneralReflect/
// runErasedBuiltinSecurityCheck) ALREADY EXIST in caller.go, built for
// [(*caller).ServeSubscribers]'s own reflection-only dispatch — this file
// reuses them directly rather than duplicating (confirming, via this
// reuse, that docs/design/d-0006-protocol-native-capabilities.md's Phase
// 4e design review correctly identified this as ALREADY-established,
// proven precedent, not a new technique). generalWrapFnType is shared by
// BOTH subscribe- and publish-side general-purpose decorator dispatch
// (see its own doc comment in caller.go).
//
// Per docs/design/d-0006-protocol-native-capabilities.md's Phase 4e:
// events.ServerImplementation/ClientImplementation are ALREADY
// non-generic (Fn any) — impl.Fn's boxed value is ALREADY a concretely-T
// Go closure built at declare time, reachable via
// reflect.ValueOf(impl.Fn).Call(...) without this package ever needing to
// know T at compile time. This is the SAME technique
// adapters/mqtt5/reqreply_transport.go's Call and
// adapters/nethttp/clienttransport.go's Call already use for their own
// client-side security dispatch.

var (
	dispatchCtxType   = reflect.TypeOf((*context.Context)(nil)).Elem()
	dispatchErrType   = reflect.TypeOf((*error)(nil)).Elem()
	dispatchUserProps = reflect.TypeOf([]UserProperty(nil))
	dispatchSecReqs   = reflect.TypeOf([]events.SecurityRequirement(nil))
)

// buildPublishSecurityFnType returns the reflect.Type every
// security-shaped [events.ClientImplementation.Fn] must match for a
// channel whose payload type is tType —
// func(context.Context, *T, []events.SecurityRequirement) ([]UserProperty, error)
// — the reflection-only mirror of [runPublishSecurityImpls][T]'s fixed
// shape.
func buildPublishSecurityFnType(tType reflect.Type) reflect.Type {
	return reflect.FuncOf(
		[]reflect.Type{dispatchCtxType, reflect.PointerTo(tType), dispatchSecReqs},
		[]reflect.Type{dispatchUserProps, dispatchErrType},
		false,
	)
}

// validateClientImplementationShapesReflect is the reflection-only mirror
// of [validatePublishImplementationShapes][T] — every attached
// [events.ClientImplementation.Fn] must match EITHER secFnType or
// generalFnType, checked EAGERLY (once, before any network activity) — a
// malformed Fn fails loudly via [events.MiddlewareShapeError], never
// silently.
func validateClientImplementationShapesReflect(impls []events.ClientImplementation, secFnType, generalFnType reflect.Type) error {
	for _, impl := range impls {
		if impl.Fn == nil {
			continue
		}
		fnVal := reflect.ValueOf(impl.Fn)
		if fnVal.Type() == secFnType || fnVal.Type() == generalFnType {
			continue
		}
		return events.MiddlewareShapeError{
			Name:     impl.Name,
			Expected: fmt.Sprintf("%s or %s", secFnType, generalFnType),
			Got:      fmt.Sprintf("%T", impl.Fn),
		}
	}
	return nil
}

// wrapClientGeneralDecoratorReflect wraps fn (a reflect.Value of type
// handlerFnType — func(context.Context, T) error) with every
// general-purpose Fn found in impls (generalFnType shape), OUTERMOST-in,
// in attachment order — the reflection-only mirror of
// [wrapPublishGeneral][T]. Security-shaped Fns are silently skipped
// (consumed instead by [runPublishSecurityImplsReflect]).
func wrapClientGeneralDecoratorReflect(fn reflect.Value, impls []events.ClientImplementation, generalFnType reflect.Type) reflect.Value {
	for i := len(impls) - 1; i >= 0; i-- {
		fnVal := reflect.ValueOf(impls[i].Fn)
		if !fnVal.IsValid() || fnVal.Type() != generalFnType {
			continue
		}
		fn = fnVal.Call([]reflect.Value{fn})[0]
	}
	return fn
}

// runPublishSecurityImplsReflect is the reflection-only mirror of
// [runPublishSecurityImpls][T]. ctxVal/valuePtr are reflect.Value
// wrappers around (context.Context, *T) — valuePtr MUST be addressable
// (a credential Fn may write into it for in-payload embedding).
func runPublishSecurityImplsReflect(ctxVal, valuePtr reflect.Value, secReqs []events.SecurityRequirement, impls []events.ClientImplementation, secFnType reflect.Type) ([]UserProperty, error) {
	reqSchemes := make(map[string]bool, len(secReqs))
	for _, req := range secReqs {
		for scheme := range req {
			reqSchemes[scheme] = true
		}
	}
	secReqsVal := reflect.ValueOf(secReqs)
	var combined []UserProperty
	for _, impl := range impls {
		fnVal := reflect.ValueOf(impl.Fn)
		if !fnVal.IsValid() || fnVal.Type() != secFnType {
			continue // general-purpose or nil
		}
		if len(impl.Satisfies) > 0 {
			matched := false
			for _, s := range impl.Satisfies {
				if reqSchemes[s] {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		results := fnVal.Call([]reflect.Value{ctxVal, valuePtr, secReqsVal})
		if fnErr, _ := results[1].Interface().(error); fnErr != nil {
			return combined, fnErr
		}
		props, _ := results[0].Interface().([]UserProperty)
		combined = append(combined, props...)
	}
	return combined, nil
}

// handlerOptsStructValue returns handlerOptsField's boxed struct value as
// a [reflect.Value], or (zero, false) when handlerOptsField is invalid,
// nil, or not a struct (a declared channel with no
// [Subscriber.WithOptions]/[Publisher.WithOptions] value is the common,
// valid case).
func handlerOptsStructValue(handlerOptsField reflect.Value) (reflect.Value, bool) {
	if !handlerOptsField.IsValid() || handlerOptsField.IsNil() {
		return reflect.Value{}, false
	}
	v := reflect.ValueOf(handlerOptsField.Interface())
	if v.Kind() != reflect.Struct {
		return reflect.Value{}, false
	}
	return v, true
}

// subscribeHandlerOptsFields extracts every non-T-dependent field
// [SubscribeOptions] carries (OnError, UserPropertyParams, Capabilities —
// SubscribeOptions itself is NOT generic, so these are plain,
// concretely-typed fields regardless of the channel's T) from a
// type-erased [events.ChannelHandle.HandlerOpts] value. Returns zero
// values when handlerOptsField is nil/wrong-shape.
func subscribeHandlerOptsFields(handlerOptsField reflect.Value) (onError func(SubscribeError), userPropertyParams []UserPropertyParam, caps []Capability) {
	v, ok := handlerOptsStructValue(handlerOptsField)
	if !ok {
		return nil, nil, nil
	}
	onError, _ = v.FieldByName("OnError").Interface().(func(SubscribeError))
	userPropertyParams, _ = v.FieldByName("UserPropertyParams").Interface().([]UserPropertyParam)
	caps, _ = v.FieldByName("Capabilities").Interface().([]Capability)
	return onError, userPropertyParams, caps
}

// publishHandlerOptsFields is [subscribeHandlerOptsFields]'s publish-side
// sibling — extracts [PublishOptions][T]'s non-T-dependent fields
// (ContentType, UserProperties, Capabilities — PublishOptions[T] has NO
// field whose TYPE depends on T, so these are reachable regardless of
// the channel's T).
func publishHandlerOptsFields(handlerOptsField reflect.Value) (contentType string, userProps []UserProperty, caps []Capability) {
	v, ok := handlerOptsStructValue(handlerOptsField)
	if !ok {
		return "", nil, nil
	}
	contentType, _ = v.FieldByName("ContentType").Interface().(string)
	userProps, _ = v.FieldByName("UserProperties").Interface().([]UserProperty)
	caps, _ = v.FieldByName("Capabilities").Interface().([]Capability)
	return contentType, userProps, caps
}

// resolveSecReqsReflect resolves the effective [events.SecurityRequirement]
// slice for a channel's Subscribe or Publish operation (opField is
// "Subscribe" or "Publish"), falling back to GlobalSecurity — the
// reflection-only mirror of adapter.go's repeated
// `if handle.Descriptor.Subscribe != nil { secReqs = ... }; if secReqs
// == nil { secReqs = handle.GlobalSecurity }` pattern. elem is the
// [events.ChannelHandle] struct value (NOT a pointer) recovered via
// [recoverHandle].
func resolveSecReqsReflect(elem reflect.Value, opField string) []events.SecurityRequirement {
	descriptor := elem.FieldByName("Descriptor")
	opPtr := descriptor.FieldByName(opField)
	if opPtr.IsValid() && !opPtr.IsNil() {
		secField := opPtr.Elem().FieldByName("Security")
		if secField.IsValid() {
			if secReqs, _ := secField.Interface().([]events.SecurityRequirement); secReqs != nil {
				return secReqs
			}
		}
	}
	global, _ := elem.FieldByName("GlobalSecurity").Interface().([]events.SecurityRequirement)
	return global
}

// resolveFormatsOverride type-asserts overrideAny (a
// [events.ClientPublishOptions.Formats]/[events.ClientSubscribeOptions.Formats]
// value) against expectedType (the reflected handle method's variadic
// slice parameter type, e.g. []format.Format[T]) — the reflection-based
// mirror of adapters/nethttp's resolveFormatsArg, performed via
// [reflect.Value] comparison since T is runtime-only here. A nil
// overrideAny resolves to the slice type's zero value (nil slice —
// equivalent to "no override").
func resolveFormatsOverride(overrideAny any, expectedType reflect.Type) (reflect.Value, error) {
	if overrideAny == nil {
		return reflect.Zero(expectedType), nil
	}
	v := reflect.ValueOf(overrideAny)
	if v.Type() != expectedType {
		return reflect.Value{}, fmt.Errorf("mqtt5: format override: want %s, got %T", expectedType, overrideAny)
	}
	return v, nil
}

// subscribeDispatchFailure returns the shared ErrorChannel→DeadLetter→
// OnError fallback triplet — extracted (Phase 3,
// docs/roadmap/adapter-integration-architecture.md's "Phase 3 results")
// from the two byte-for-byte-identical `dispatchFailure` closures
// previously hand-duplicated in `transport.go`'s `(*transport).Subscribe`
// and `caller.go`'s `makeErasedSubscribeMessageHandler`. Both call sites
// already resolved errorResponseForMethod/deadLetterForMethod via
// reflection against the SAME *events.ChannelHandle[T] shape before
// calling this — only the recovery PATH differed (fresh per-call in
// transport.go vs. pre-extracted once into erasedSubscriberHandle in
// caller.go), not the shape itself, which is why this extraction is a
// pure, zero-behavior-change deduplication.
//
// This is deliberately SCOPED to only the error-dispatch triplet, not the
// full per-message dispatch body those two functions still each
// implement independently — Phase 3's investigation while writing this
// extraction found the two bodies have genuinely diverged beyond mere
// duplication (topic-template-var matching and PropertyMergeField
// merge-into-value support exist only in transport.go's path; Disposition
// tracking exists only in caller.go's path; caller.go's
// wrapHandlerGeneralReflect call also runs per-message instead of once,
// unlike transport.go's). Reconciling those real differences into one
// shared dispatch core is an explicit behavior decision, not a
// mechanical refactor — see the roadmap's "Phase 3 results" for the full
// finding, deferred pending that decision rather than papered over here.
func subscribeDispatchFailure(
	ctx context.Context,
	client MQTTClient,
	obs stats.Observer,
	errorResponseForMethod, deadLetterForMethod reflect.Value,
	onErrorFn func(SubscribeError),
) func(kind ErrorKind, sourceTopic string, payload []byte, failErr error) {
	return func(kind ErrorKind, sourceTopic string, payload []byte, failErr error) {
		errResults := errorResponseForMethod.Call([]reflect.Value{reflect.ValueOf(&failErr).Elem()})
		resp, _ := errResults[0].Interface().(events.ErrorChannelResponse)
		matched, _ := errResults[1].Interface().(bool)
		matchErrI, _ := errResults[2].Interface().(error)
		if matched && matchErrI == nil && resp.Action == events.ErrorRespond {
			_, _ = client.Publish(ctx, &pahomqtt5.Publish{Topic: resp.Topic, QoS: defaultQoS, Payload: resp.Body})
			return
		}
		if !matched {
			dlResults := deadLetterForMethod.Call([]reflect.Value{
				reflect.ValueOf(obs), reflect.ValueOf(sourceTopic), reflect.ValueOf(payload), reflect.ValueOf(&failErr).Elem(),
			})
			dlTopic, _ := dlResults[0].Interface().(string)
			dlBody, _ := dlResults[1].Interface().([]byte)
			dlOk, _ := dlResults[2].Interface().(bool)
			if dlOk {
				_, _ = client.Publish(ctx, &pahomqtt5.Publish{Topic: dlTopic, QoS: defaultQoS, Payload: dlBody})
				return
			}
		}
		if onErrorFn != nil {
			onErrorFn(SubscribeError{Kind: kind, Topic: sourceTopic, Err: failErr})
		}
	}
}

// subscribeDispatchCore builds the [pahomqtt5.MessageHandler] both
// `transport.go`'s [(*transport).Subscribe] (the ports.Pattern-binding
// path) and `caller.go`'s [(*caller).serveOneEntry] (the Client.Attach/
// ServeSubscribers path) register with their router — the ONE shared
// reflect-based dispatch core Phase 3 case 6's parity fix made possible:
// once topic-var/property-merge support and Disposition tracking were
// brought to parity across both paths (and the per-message re-wrap
// hoisted to run once), the two paths' per-message bodies became
// byte-for-byte identical, not merely behaviorally equivalent — this is
// the literal merge that fix deliberately deferred as "too risky" at the
// time (case 6's own writeup), now safe to perform because the ONLY
// genuine divergence left (Disposition-only vs. topic-var-only support)
// was exactly what that fix closed.
//
// Per Phase 4's "Events/reqreply family dispatch-core consolidation
// design" point 1: this covers 2 of mqtt5's 3 Subscribe dispatch
// implementations — the compile-time-generic
// [makeSubscribeMessageHandler][T] escape hatch (adapter.go) is NOT
// folded in here; Go has no reflective generic instantiation, so a
// function taking reflect.Value cannot also be called with a concrete T
// without boxing through any first, defeating the whole point of the
// generic path's existence. Resolved count: 3 → 2 implementations, not
// 3 → 1 — matching the REST family's nethttp precedent (buildRouteHandler
// reflect vs. handlerFunc generic stayed two functions, unified only via
// a shared construction-time core).
//
// wrappedFn is the handler reflect.Value, ALREADY shape-validated and
// ALREADY wrapped via [wrapHandlerGeneralReflect] by the caller — exactly
// once, before any message is dispatched (mirrors both prior bodies'
// identical once-per-setup wrap timing). formatsOverride is the
// resolved per-call formats override (zero value when the dispatch path
// has no override mechanism — see caller.go's [makeErasedSubscribeMessageHandler]
// doc comment, now folded into this function, for why ServeSubscribers'
// own call site always passes the zero value here).
func subscribeDispatchCore(
	ctx context.Context,
	client MQTTClient,
	obs stats.Observer,
	info erasedSubscriberHandle,
	wrappedFn reflect.Value,
	formatsOverride reflect.Value,
	userPropertyParams []UserPropertyParam,
	onErrorFn func(SubscribeError),
) pahomqtt5.MessageHandler {
	middlewareHandlersLen := len(info.middlewareHandlers)
	middlewareSatisfies := make([][]string, middlewareHandlersLen)
	for i, h := range info.middlewareHandlers {
		middlewareSatisfies[i] = h.Satisfies
	}

	var secReqs []route.SecurityRequirement
	if info.descriptor.Subscribe != nil {
		secReqs = info.descriptor.Subscribe.Security
	}
	if secReqs == nil {
		secReqs = info.globalSecurity
	}

	// dispatchFailure is the shared ErrorChannel→DeadLetter→OnError
	// fallback triplet every pipeline step below consults on failure
	// (extracted in Phase 3, shared by both paths even before this
	// literal merge — see [subscribeDispatchFailure]'s own doc comment).
	dispatchFailure := subscribeDispatchFailure(ctx, client, obs, info.errorResponseForMethod, info.deadLetterForMethod, onErrorFn)

	return func(msg *pahomqtt5.Publish) {
		start := time.Now()
		// msgCtx stores msg AND its User Properties pre-decode — a caller
		// reading these inside its handler must see them regardless of
		// which dispatch path (generic/reflect, Subscribe/ServeSubscribers)
		// decoded the message.
		msgCtx := middleware.EnsureContextFields(context.WithValue(ctx, contextKey{}, msg))
		if msg.Properties != nil && len(msg.Properties.User) > 0 {
			msgCtx = context.WithValue(msgCtx, userPropsKey{}, msg.Properties.User)
		}

		vars, matchErr := matchTopicTemplate(info.topic, msg.Topic)
		if matchErr != nil {
			return // broader wildcard subscription received a non-matching topic — expected, not an error.
		}

		decodeResults := info.decodeMergedMethod.CallSlice([]reflect.Value{reflect.ValueOf(msg.Payload), reflect.ValueOf(vars), formatsOverride})
		if decErr, _ := decodeResults[1].Interface().(error); decErr != nil {
			stats.ReportErrors(obs, "payload", decErr)
			obs.RecordSubscribe(msg.Topic, false, time.Since(start))
			dispatchFailure(KindDecode, msg.Topic, msg.Payload, decErr)
			return
		}
		valuePtr := reflect.New(info.msgType)
		valuePtr.Elem().Set(decodeResults[0])

		// Property vocabulary axis: extract real MQTT5 User Properties
		// into a plain map for codec-Middleware dispatch AND
		// channel-level MergedPropertyParam merge fields — kept SEPARATE
		// from topic vars (independent namespace/consumer).
		var propertyVars map[string]string
		if middlewareHandlersLen > 0 || info.propertyMergeFieldsLen > 0 {
			propertyVars = make(map[string]string)
			if msg.Properties != nil {
				for _, p := range msg.Properties.User {
					propertyVars[p.Key] = p.Value
				}
			}
			if info.propertyMergeFieldsLen > 0 {
				mergeResults := info.mergePropertyVarsMethod.Call([]reflect.Value{valuePtr, reflect.ValueOf(propertyVars)})
				if mergeErr, _ := mergeResults[0].Interface().(error); mergeErr != nil {
					stats.ReportErrors(obs, "property_var", mergeErr)
					obs.RecordSubscribe(msg.Topic, false, time.Since(start))
					dispatchFailure(KindDecode, msg.Topic, msg.Payload, mergeErr)
					return
				}
			}
		}

		// User Property param validation — runs before security
		// enforcement.
		if propErr := validateUserProperties(msg, userPropertyParams); propErr != nil {
			obs.RecordValidationError("user_property", stats.ConstraintName(propErr), userPropertyName(propErr))
			obs.RecordSubscribe(msg.Topic, false, time.Since(start))
			dispatchFailure(KindSecurity, msg.Topic, msg.Payload, propErr)
			return
		}

		// Built-in codec-based credential check — runs BEFORE any
		// declarative SubscribeMW security implementation. Deliberately
		// does NOT consult ErrorChannel/DeadLetter — falls straight to
		// OnError.
		if len(secReqs) > 0 {
			if err := runErasedBuiltinSecurityCheck(msg, secReqs, info.securitySchemes); err != nil {
				if secObs, ok := obs.(stats.SecurityObserver); ok {
					secObs.RecordSecurityRejection(msg.Topic, route.FirstSchemeName(secReqs))
				}
				obs.RecordSubscribe(msg.Topic, false, time.Since(start))
				if onErrorFn != nil {
					onErrorFn(SubscribeError{Kind: KindSecurity, Topic: msg.Topic, Err: err})
				}
				return
			}
		}

		// Implementations-based security check (from SubscribeMW) — runs
		// UNCONDITIONALLY.
		granted := make(map[string][]string)
		if len(info.implementations) > 0 {
			g, err := runSubscribeSecurityImplsReflect(msgCtx, msg, valuePtr, secReqs, info.implementations)
			if err != nil {
				if secObs, ok := obs.(stats.SecurityObserver); ok {
					secObs.RecordSecurityRejection(msg.Topic, route.FirstSchemeName(secReqs))
				}
				obs.RecordSubscribe(msg.Topic, false, time.Since(start))
				dispatchFailure(KindSecurity, msg.Topic, msg.Payload, events.SecurityError{Err: err})
				return
			}
			granted = g
		}

		// Codec-backed middleware dispatch (SubscribeBoundMW and bundled
		// .Use()) — SAME pre-handler dispatch point the security
		// Implementations check just ran at, reusing the SAME
		// topicVars/propertyVars derived above. A bound MiddlewareHandler's
		// own GrantedScopes-carrying Out (via scopesmerge.MergeHandlerGrants)
		// is merged into the SAME `granted` map BEFORE the unified
		// CheckScopes call below.
		if middlewareHandlersLen > 0 {
			mwResults := info.dispatchSubscribeMiddleware.Call([]reflect.Value{
				reflect.ValueOf(msgCtx), valuePtr, reflect.ValueOf(vars), reflect.ValueOf(propertyVars),
			})
			outs, _ := mwResults[0].Interface().([]any)
			if mwErr, _ := mwResults[1].Interface().(error); mwErr != nil {
				obs.RecordSubscribe(msg.Topic, false, time.Since(start))
				dispatchErr, _ := events.AsMiddlewareDispatchError(mwErr)
				if dispatchErr.IsFnError {
					stats.ReportErrors(obs, "middleware:fn", dispatchErr.Err)
					dispatchFailure(KindHandler, msg.Topic, msg.Payload, events.MiddlewareError{Name: dispatchErr.Name, Err: dispatchErr.Err})
				} else {
					stats.ReportErrors(obs, "middleware:in", dispatchErr.Err)
					dispatchFailure(KindDecode, msg.Topic, msg.Payload, dispatchErr.Err)
				}
				return
			}
			scopesmerge.MergeHandlerGrants(granted, middlewareSatisfies, outs)
		}

		// ONE unified CheckScopes call covering grants from BOTH the
		// legacy Implementations path AND any bound MiddlewareHandler's
		// own GrantedScopes (merged above).
		if len(secReqs) > 0 {
			if err := events.CheckScopes(secReqs, granted); err != nil {
				if secObs, ok := obs.(stats.SecurityObserver); ok {
					secObs.RecordSecurityRejection(msg.Topic, route.FirstSchemeName(secReqs))
				}
				obs.RecordSubscribe(msg.Topic, false, time.Since(start))
				dispatchFailure(KindSecurity, msg.Topic, msg.Payload, events.SecurityError{Err: err})
				return
			}
		}

		// EnsureDispositionBox is called unconditionally, at zero cost to
		// adapters/callers that never call SetDisposition — mqtt5 has no
		// acknowledgement concept of its own today, but the plumbing is
		// proven end-to-end here for a future ack-capable adapter to
		// consume without further core changes.
		spanCtx := stats.EnsureDispositionBox(msgCtx)
		if to, ok := obs.(stats.TraceObserver); ok {
			spanCtx = to.StartSpan(spanCtx, "mqtt5.subscribe", msg.Topic)
		}
		fnResults := wrappedFn.Call([]reflect.Value{reflect.ValueOf(spanCtx), valuePtr.Elem()})
		handlerErr, _ := fnResults[0].Interface().(error)
		if to, ok := obs.(stats.TraceObserver); ok {
			to.EndSpan(spanCtx, handlerErr)
		}
		disposition := stats.ResolveDisposition(spanCtx, handlerErr)
		if dispObs, ok := obs.(stats.DispositionObserver); ok {
			dispObs.RecordDisposition(msg.Topic, disposition)
		}
		if handlerErr == nil {
			obs.RecordSubscribe(msg.Topic, true, time.Since(start))
			return
		}
		stats.ReportErrors(obs, "topic_var", handlerErr)
		obs.RecordSubscribe(msg.Topic, false, time.Since(start))
		dispatchFailure(KindHandler, msg.Topic, msg.Payload, handlerErr)
	}
}
