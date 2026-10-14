package mqtt

import (
	"context"
	"reflect"
	"time"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"

	"github.com/DaniDeer/go-codex/adapters/internal/scopesmerge"
	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/internal/middleware"
	"github.com/DaniDeer/go-codex/internal/route"
	asyncapi "github.com/DaniDeer/go-codex/render/asyncapi/v3"
	"github.com/DaniDeer/go-codex/stats"
)

// erasedSubscriberHandle holds every field [subscribeDispatchCore] needs,
// recovered via reflect from a type-erased *[events.ChannelHandle][T] —
// the mqtt v3 sibling of adapters/mqtt5's identical-shaped struct.
// Simpler than mqtt5's: v3 has no User-Property/property-merge mechanism
// at all, so there is no mergePropertyVarsMethod/propertyMergeFieldsLen
// field — PURELY topic-var merge via [matchTopicTemplate]/
// DecodeMergedWithFormats, which [events.ChannelHandle] already exposes
// generically regardless of adapter.
type erasedSubscriberHandle struct {
	topic              string
	descriptor         asyncapi.ChannelItem
	globalSecurity     []events.SecurityRequirement
	implementations    []events.ServerImplementation
	msgType            reflect.Type
	middlewareHandlers []events.MiddlewareHandler
	// dispatchSubscribeMiddleware/errorResponseForMethod/deadLetterForMethod/
	// decodeMergedMethod are handleVal's own
	// [events.ChannelHandle.DispatchSubscribeMiddleware]/
	// [events.ChannelHandle.ErrorResponseFor]/
	// [events.ChannelHandle.DeadLetterFor]/
	// [events.ChannelHandle.DecodeMergedWithFormats] methods
	// (reflect-bound once here) — mirrors adapters/mqtt5's identical
	// reflection pattern.
	dispatchSubscribeMiddleware reflect.Value
	errorResponseForMethod      reflect.Value
	deadLetterForMethod         reflect.Value
	decodeMergedMethod          reflect.Value
}

// buildErasedSubscriberHandleFields fills in every [erasedSubscriberHandle]
// field recoverable from an already-obtained (handleVal, elem) pair —
// the single shared constructor BOTH `transport.go`'s
// [(*transport).Subscribe] (the ports.Pattern-binding path) and
// `caller.go`'s [subscribeEntryReflect] (the Client.Attach/
// ServeSubscribers path) use — mirrors adapters/mqtt5's identical
// alignment step from Phase 4's Workstream A.
func buildErasedSubscriberHandleFields(handleVal, elem reflect.Value) erasedSubscriberHandle {
	descriptor, _ := elem.FieldByName("Descriptor").Interface().(asyncapi.ChannelItem)
	globalSecurity, _ := elem.FieldByName("GlobalSecurity").Interface().([]events.SecurityRequirement)
	implementations, _ := elem.FieldByName("Implementations").Interface().([]events.ServerImplementation)
	middlewareHandlers, _ := elem.FieldByName("MiddlewareHandlers").Interface().([]events.MiddlewareHandler)
	decodeMergedMethod := handleVal.MethodByName("DecodeMergedWithFormats")
	return erasedSubscriberHandle{
		topic:                       elem.FieldByName("Topic").String(),
		descriptor:                  descriptor,
		globalSecurity:              globalSecurity,
		implementations:             implementations,
		msgType:                     decodeMergedMethod.Type().Out(0),
		middlewareHandlers:          middlewareHandlers,
		dispatchSubscribeMiddleware: handleVal.MethodByName("DispatchSubscribeMiddleware"),
		errorResponseForMethod:      handleVal.MethodByName("ErrorResponseFor"),
		deadLetterForMethod:         handleVal.MethodByName("DeadLetterFor"),
		decodeMergedMethod:          decodeMergedMethod,
	}
}

// subscribeDispatchCore builds the [pahomqtt.MessageHandler] both
// `transport.go`'s [(*transport).Subscribe] (the ports.Pattern-binding
// path) and `caller.go`'s [subscribeEntryReflect] (the Client.Attach/
// ServeSubscribers path) register with the broker client — the ONE
// shared reflect-based dispatch core for mqtt v3's Subscribe side,
// mirroring adapters/mqtt5's identical Workstream A consolidation.
//
// This is the literal merge point for TWO of mqtt v3's three Subscribe
// dispatch implementations (per the roadmap's "Events/reqreply family
// dispatch-core consolidation design" point 1: 3 → 2, not 3 → 1 — the
// compile-time-generic [subscribeHandler][T] escape hatch in adapter.go
// cannot share a function with the reflect paths; Go has no reflective
// generic instantiation).
//
// Investigation BEFORE this merge found a genuine 3-way, 2-direction
// divergence (mirroring mqtt5's pre-case-6 state, but shaped
// differently): `transport.Subscribe` had topic-var support (via
// [matchTopicTemplate]) but NO Disposition tracking and NO
// security-kind-detection on a failing middleware handler;
// `subscribeEntryReflect` had Disposition tracking AND the
// security-kind-detection fix but NO topic-var support at all (decoded
// the payload directly, ignoring the channel's own topic template).
// This function is the "add parity" superset of both — every feature
// either path had is now present on both, by construction, since there
// is only one function.
//
// wrappedFn is the handler reflect.Value, ALREADY shape-validated and
// ALREADY wrapped via the caller's own general-purpose decorator
// composition — exactly once, before any message is dispatched.
// formatsOverride is the resolved per-call formats override (zero value
// when the dispatch path has no override mechanism — mqtt v3's
// [SubscribeOptions] has no Formats field at all, so
// `subscribeEntryReflect`'s own call site always passes the zero value,
// mirroring mqtt5's identical, deliberately-unfixed asymmetry).
func subscribeDispatchCore(
	ctx context.Context,
	client pahomqtt.Client,
	obs stats.Observer,
	info erasedSubscriberHandle,
	wrappedFn reflect.Value,
	formatsOverride reflect.Value,
	onErrorFn func(SubscribeError),
) pahomqtt.MessageHandler {
	middlewareHandlersLen := len(info.middlewareHandlers)
	middlewareSatisfies := make([][]string, middlewareHandlersLen)
	for i, h := range info.middlewareHandlers {
		middlewareSatisfies[i] = h.Satisfies
	}

	var secReqs []events.SecurityRequirement
	if info.descriptor.Subscribe != nil {
		secReqs = info.descriptor.Subscribe.Security
	}
	if secReqs == nil {
		secReqs = info.globalSecurity
	}

	// dispatchFailure is the shared ErrorChannel→DeadLetter→OnError
	// fallback triplet every pipeline step below consults on failure —
	// mirrors adapter.go's [tryPublishErrorChannel]/[tryDeadLetter]/
	// opts.OnError exact fallback order, and mqtt5's identical
	// reflection-based dispatchFailure closure.
	dispatchFailure := func(kind ErrorKind, sourceTopic string, payload []byte, failErr error) {
		errResults := info.errorResponseForMethod.Call([]reflect.Value{reflect.ValueOf(&failErr).Elem()})
		resp, _ := errResults[0].Interface().(events.ErrorChannelResponse)
		matched, _ := errResults[1].Interface().(bool)
		matchErrI, _ := errResults[2].Interface().(error)
		if matched && matchErrI == nil && resp.Action == events.ErrorRespond {
			token := client.Publish(resp.Topic, defaultQoS, false, resp.Body)
			token.Wait()
			return
		}
		if !matched {
			dlResults := info.deadLetterForMethod.Call([]reflect.Value{
				reflect.ValueOf(obs), reflect.ValueOf(sourceTopic), reflect.ValueOf(payload), reflect.ValueOf(&failErr).Elem(),
			})
			dlTopic, _ := dlResults[0].Interface().(string)
			dlBody, _ := dlResults[1].Interface().([]byte)
			dlOk, _ := dlResults[2].Interface().(bool)
			if dlOk {
				token := client.Publish(dlTopic, defaultQoS, false, dlBody)
				token.Wait()
				return
			}
		}
		if onErrorFn != nil {
			onErrorFn(SubscribeError{Kind: kind, Topic: sourceTopic, Err: failErr})
		}
	}

	return func(_ pahomqtt.Client, msg pahomqtt.Message) {
		start := time.Now()
		// msgCtx stores msg, closing a gap where [MessageFromContext]
		// never worked through some dispatch paths — mirrors
		// adapters/mqtt5's identical, pre-decode placement.
		msgCtx := middleware.EnsureContextFields(context.WithValue(ctx, contextKey{}, msg))

		vars, matchErr := matchTopicTemplate(info.topic, msg.Topic())
		if matchErr != nil {
			return // broader wildcard subscription received a non-matching topic — expected, not an error.
		}

		decodeResults := info.decodeMergedMethod.CallSlice([]reflect.Value{reflect.ValueOf(msg.Payload()), reflect.ValueOf(vars), formatsOverride})
		if decErr, _ := decodeResults[1].Interface().(error); decErr != nil {
			stats.ReportErrors(obs, "payload", decErr)
			obs.RecordSubscribe(msg.Topic(), false, time.Since(start))
			dispatchFailure(KindDecode, msg.Topic(), msg.Payload(), decErr)
			return
		}
		valuePtr := reflect.New(info.msgType)
		valuePtr.Elem().Set(decodeResults[0])

		// Implementations-based security (SubscribeMW) — runs
		// UNCONDITIONALLY. mqtt v3 has no built-in codec-based credential
		// check — Implementations IS the entire security mechanism.
		granted := make(map[string][]string)
		if len(info.implementations) > 0 {
			g, secErr := runSubscribeSecurityImplsReflect(msgCtx, msg, valuePtr, secReqs, info.implementations)
			if secErr != nil {
				if secObs, ok := obs.(stats.SecurityObserver); ok {
					secObs.RecordSecurityRejection(msg.Topic(), route.FirstSchemeName(secReqs))
				}
				obs.RecordSubscribe(msg.Topic(), false, time.Since(start))
				dispatchFailure(KindSecurity, msg.Topic(), msg.Payload(), events.SecurityError{Err: secErr})
				return
			}
			granted = g
		}

		// Codec-backed middleware dispatch (SubscribeBoundMW and bundled
		// .Use()) — SAME pre-handler dispatch point the security
		// Implementations check just ran at. mqtt v3 has no property
		// mechanism — supplies a nil property-value map.
		//
		// CORRECTED (this merge): `transport.Subscribe`'s pre-merge
		// version distinguished IsFnError (→ KindHandler, wrapped as
		// [events.MiddlewareError]) from the encode/"in" case
		// (→ KindDecode) and reported `stats.ReportErrors` in both
		// branches, but never checked whether the failing Fn was a
		// Security-carrying handler. `subscribeEntryReflect`'s pre-merge
		// version had the OPPOSITE gap: it checked
		// [events.IsSecuritySatisfyingHandler] (calling
		// RecordSecurityRejection + reporting KindSecurity when true) but
		// never distinguished IsFnError otherwise (always fell back to
		// KindDecode) and never called stats.ReportErrors at all. This is
		// the full superset of both: IsFnError → KindHandler (further
		// refined to KindSecurity when the failing Fn is
		// Security-carrying) with stats.ReportErrors+MiddlewareError
		// wrapping; the encode/"in" case stays KindDecode.
		if middlewareHandlersLen > 0 {
			mwResults := info.dispatchSubscribeMiddleware.Call([]reflect.Value{
				reflect.ValueOf(msgCtx), valuePtr, reflect.ValueOf(vars), reflect.Zero(reflect.TypeOf(map[string]string(nil))),
			})
			outs, _ := mwResults[0].Interface().([]any)
			if mwErr, _ := mwResults[1].Interface().(error); mwErr != nil {
				obs.RecordSubscribe(msg.Topic(), false, time.Since(start))
				dispatchErr, _ := events.AsMiddlewareDispatchError(mwErr)
				if dispatchErr.IsFnError {
					kind := KindHandler
					if events.IsSecuritySatisfyingHandler(info.middlewareHandlers, dispatchErr.Name) {
						if secObs, ok := obs.(stats.SecurityObserver); ok {
							secObs.RecordSecurityRejection(msg.Topic(), route.FirstSchemeName(secReqs))
						}
						kind = KindSecurity
					}
					stats.ReportErrors(obs, "middleware:fn", dispatchErr.Err)
					dispatchFailure(kind, msg.Topic(), msg.Payload(), events.MiddlewareError{Name: dispatchErr.Name, Err: dispatchErr.Err})
				} else {
					stats.ReportErrors(obs, "middleware:in", dispatchErr.Err)
					dispatchFailure(KindDecode, msg.Topic(), msg.Payload(), dispatchErr.Err)
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
					secObs.RecordSecurityRejection(msg.Topic(), route.FirstSchemeName(secReqs))
				}
				obs.RecordSubscribe(msg.Topic(), false, time.Since(start))
				dispatchFailure(KindSecurity, msg.Topic(), msg.Payload(), events.SecurityError{Err: err})
				return
			}
		}

		// EnsureDispositionBox is called unconditionally, at zero cost to
		// adapters/callers that never call SetDisposition — mqtt v3 has
		// no acknowledgement concept of its own today, but the plumbing
		// is proven end-to-end here for a future ack-capable adapter to
		// consume without further core changes — brings
		// `transport.Subscribe` to parity with `subscribeEntryReflect`'s
		// pre-existing Disposition tracking.
		msgCtx = stats.EnsureDispositionBox(msgCtx)
		results := wrappedFn.Call([]reflect.Value{reflect.ValueOf(msgCtx), valuePtr.Elem()})
		handlerErr, _ := results[0].Interface().(error)
		disposition := stats.ResolveDisposition(msgCtx, handlerErr)
		if dispObs, ok := obs.(stats.DispositionObserver); ok {
			dispObs.RecordDisposition(msg.Topic(), disposition)
		}
		if handlerErr != nil {
			obs.RecordSubscribe(msg.Topic(), false, time.Since(start))
			dispatchFailure(KindHandler, msg.Topic(), msg.Payload(), handlerErr)
			return
		}
		obs.RecordSubscribe(msg.Topic(), true, time.Since(start))
	}
}
