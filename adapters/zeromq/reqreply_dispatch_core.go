package zeromq

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/DaniDeer/go-codex/adapters/internal/scopesmerge"
	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/internal/route"
	"github.com/DaniDeer/go-codex/stats"
)

// reqreplyServerSetup bundles every field BOTH `(*serverTransport).Serve`
// (the plain REQ/REP variant) and `(*routerServerTransport).Serve` (the
// ROUTER variant) need, once setup — Phase 4 events/reqreply family,
// Workstream C's reqreply setup/dispatch-one split (point 3 of this
// family's design). Setup-phase recovery/validation has NO protocol
// dependency (HWM/Conflate/coverage/middleware-wrapping do not know or
// care whether the underlying socket is REQ/REP or ROUTER) — confirmed
// identical, textually, between both Serve functions before this
// extraction.
type reqreplyServerSetup struct {
	rv                            reflect.Value
	elem                          reflect.Value
	path                          string
	reqType                       reflect.Type
	decodeWithFormatsMethod       reflect.Value
	encodeWithFormatsMethod       reflect.Value
	observeErrorResponseForMethod reflect.Value
	deadLetterForMethod           reflect.Value
	hasPropertyMergeFields        bool
	impls                         []reqreply.ServerImplementation
	middlewareHandlers            []reqreply.MiddlewareHandler
	secReqs                       []reqreply.SecurityRequirement
	// dispatchFn is fnVal wrapped by every general-purpose (UNPAIRED)
	// HandleMW implementation, OUTERMOST-in — the paired security Fns
	// run SEPARATELY, inside [reqreplyServerDispatchOne], since they
	// need pointer access to *Req.
	dispatchFn reflect.Value
}

// reqreplyServerSetupCore recovers routeAny/fnAny into a
// [reqreplyServerSetup], running every ONCE-per-Serve-call check: route
// handle recovery, handler shape validation, declarative
// security/middleware coverage (via [reqreply.CheckCoverage]), capability
// application + requirement coverage (via applyCapabilities/
// [reqreply.VerifyCapabilityCoverage]), and general-purpose middleware
// wrapping. Called identically by both Serve variants at the top of
// each, before their own socket-specific recv loop starts.
func reqreplyServerSetupCore(routeAny, fnAny any, sock FramedSocket, obs stats.Observer, caps []Capability) (*reqreplyServerSetup, error) {
	rv, elem, err := recoverRouteHandleValue(routeAny)
	if err != nil {
		return nil, err
	}
	path := elem.FieldByName("Topic").String()

	decodeField := elem.FieldByName("Decode")
	encodeField := elem.FieldByName("Encode")
	reqType := decodeField.Type().Out(0)
	wantFnType := reflect.FuncOf(
		[]reflect.Type{reflect.TypeOf((*context.Context)(nil)).Elem(), reqType},
		[]reflect.Type{encodeField.Type().In(0), reflect.TypeOf((*error)(nil)).Elem()},
		false,
	)
	fnVal := reflect.ValueOf(fnAny)
	if !fnVal.IsValid() || fnVal.Type() != wantFnType {
		return nil, reqreply.TransportTypeMismatchError{Topic: path, Want: wantFnType.String(), Got: fmt.Sprintf("%T", fnAny)}
	}

	decodeWithFormatsMethod := rv.MethodByName("DecodeWithFormats")
	encodeWithFormatsMethod := rv.MethodByName("EncodeWithFormats")
	hasPropertyMergeFields := rv.MethodByName("PropertyMergeFields").Call(nil)[0].Len() > 0
	observeErrorResponseForMethod := rv.MethodByName("ObserveErrorResponseFor")
	deadLetterForMethod := rv.MethodByName("DeadLetterFor")

	respType := encodeField.Type().In(0)
	securityFnType := buildPairedSecurityFnType(reqType)
	generalDecoratorFnType := buildGeneralDecoratorFnType(reqType, respType)
	impls, _ := elem.FieldByName("Implementations").Interface().([]reqreply.ServerImplementation)
	if err := validateServerImplementationShapes(path, impls, securityFnType, generalDecoratorFnType); err != nil {
		return nil, err
	}
	middlewareHandlers, _ := elem.FieldByName("MiddlewareHandlers").Interface().([]reqreply.MiddlewareHandler)
	secReqs := effectiveSecurity(elem)
	if err := reqreply.CheckCoverage(path, secReqs, impls, middlewareHandlers); err != nil {
		return nil, err
	}

	requirements, _ := elem.FieldByName("Requirements").Interface().([]reqreply.CapabilityRequirement)
	applyCapabilities(sock, caps, obs, path)
	if err := reqreply.VerifyCapabilityCoverage(path, requirements, caps); err != nil {
		return nil, err
	}

	dispatchFn := applyGeneralServerMiddleware(fnVal, impls)

	return &reqreplyServerSetup{
		rv:                            rv,
		elem:                          elem,
		path:                          path,
		reqType:                       reqType,
		decodeWithFormatsMethod:       decodeWithFormatsMethod,
		encodeWithFormatsMethod:       encodeWithFormatsMethod,
		observeErrorResponseForMethod: observeErrorResponseForMethod,
		deadLetterForMethod:           deadLetterForMethod,
		hasPropertyMergeFields:        hasPropertyMergeFields,
		impls:                         impls,
		middlewareHandlers:            middlewareHandlers,
		secReqs:                       secReqs,
		dispatchFn:                    dispatchFn,
	}, nil
}

// reqreplyReplySink abstracts the ONE genuine per-variant difference left
// once setup is shared: how a reply/error-reply/forward-reply is actually
// framed and sent — REQ/REP has no addressing concept (2 frames: status +
// payload); ROUTER must prepend/echo an identity frame (4 frames: identity
// + empty delimiter + status + payload). label is the
// [stats.Observer.RecordRequest] protocol label ("ZMQ-REP"/"ZMQ-ROUTER").
type reqreplyReplySink struct {
	label       string
	sendSuccess func(respPayload []byte) error
	sendError   func(ctx context.Context, err error)
	sendForward func(fwd reqreply.ForwardError)
}

// reqreplyServerDispatchOne runs the full decode → property-merge →
// paired-security → codec-middleware → unified-scopes-check → handler →
// encode → reply pipeline for ONE received request — the ServeOne-
// request mirror both `(*serverTransport).Serve`'s sequential loop body
// and `(*routerServerTransport).Serve`'s per-goroutine loop body
// delegate to, assembled verbatim from the two confirmed near-identical
// bodies (only the reply-framing/identity-prefixing genuinely differs,
// now captured by sink).
func reqreplyServerDispatchOne(
	ctx context.Context,
	setup *reqreplyServerSetup,
	sink reqreplyReplySink,
	sockets map[string]FramedSocket,
	obs stats.Observer,
	payload []byte,
	onError func(ServeError),
) {
	start := time.Now()

	// EnsureDispositionBox is called unconditionally, at zero cost to
	// adapters/callers that never call SetDisposition — zeromq reqreply
	// has no acknowledgement concept of its own today, but the plumbing
	// is proven end-to-end here for a future ack-capable adapter (e.g.
	// AMQP) to consume without further core changes. See
	// docs/design/d-0006-protocol-native-capabilities.md's §8.
	spanCtx := stats.EnsureDispositionBox(ctx)
	var serveErr error
	if to, ok := obs.(stats.TraceObserver); ok {
		spanCtx = to.StartSpan(spanCtx, "zmq.serve", setup.path)
	}
	defer func() {
		if to, ok := obs.(stats.TraceObserver); ok {
			to.EndSpan(spanCtx, serveErr)
		}
	}()

	decodeResults := setup.decodeWithFormatsMethod.CallSlice([]reflect.Value{reflect.ValueOf(payload), setup.elem.FieldByName("RequestFormats")})
	if errI, _ := decodeResults[1].Interface().(error); errI != nil {
		stats.ReportErrors(obs, "body", errI)
		serveErr = errI
		obs.RecordRequest(sink.label, setup.path, 0, time.Since(start))
		sink.sendError(spanCtx, errI)
		tryDeadLetterReflect(sockets, setup.deadLetterForMethod, obs, setup.path, payload, errI)
		if onError != nil {
			onError(ServeError{Kind: KindDecode, Err: errI})
		}
		return
	}
	reqVal := decodeResults[0]

	// Route-level (Middleware-free) property merge — zeromq's REQ/REP
	// and ROUTER wire formats carry no property channel at all, so vars
	// is always nil; a Required direct PropertyParam fails naturally.
	if setup.hasPropertyMergeFields {
		reqPtr := reflect.New(setup.reqType)
		reqPtr.Elem().Set(reqVal)
		mergeResults := setup.rv.MethodByName("MergePropertyVars").Call([]reflect.Value{reqPtr, reflect.ValueOf(map[string]string(nil))})
		if errI, _ := mergeResults[0].Interface().(error); errI != nil {
			stats.ReportErrors(obs, "property_var", errI)
			serveErr = errI
			obs.RecordRequest(sink.label, setup.path, 0, time.Since(start))
			sink.sendError(spanCtx, errI)
			tryDeadLetterReflect(sockets, setup.deadLetterForMethod, obs, setup.path, payload, errI)
			if onError != nil {
				onError(ServeError{Kind: KindDecode, Err: errI})
			}
			return
		}
		reqVal = reqPtr.Elem()
	}

	// Paired security Fns run BETWEEN decode and dispatch, reading/
	// optionally enriching the decoded value via a fresh, addressable
	// *Req copy — reqVal is re-read afterward to pick up any mutation.
	if len(setup.impls) > 0 {
		reqPtr := reflect.New(setup.reqType)
		reqPtr.Elem().Set(reqVal)
		if secErr := runPairedServerSecurity(spanCtx, reqPtr, setup.impls, setup.secReqs); secErr != nil {
			wrapped := reqreply.SecurityError{Err: secErr}
			if secObs, ok := obs.(stats.SecurityObserver); ok {
				secObs.RecordSecurityRejection(setup.path, route.FirstSchemeName(setup.secReqs))
			}
			serveErr = wrapped
			obs.RecordRequest(sink.label, setup.path, 0, time.Since(start))
			sink.sendError(spanCtx, wrapped)
			tryDeadLetterReflect(sockets, setup.deadLetterForMethod, obs, setup.path, payload, wrapped)
			if onError != nil {
				onError(ServeError{Kind: KindSecurity, Err: wrapped})
			}
			return
		}
		reqVal = reqPtr.Elem()
	}

	// Codec-backed Middleware[In,Out] dispatch — runs AFTER the paired
	// security Fns above, reading/enriching the SAME reqVal. zeromq's
	// legacy runPairedServerSecurity has NO grants concept at all (plain
	// error, no CheckScopes call ever existed there) — granted starts
	// empty and is populated SOLELY by any bound MiddlewareHandler's own
	// GrantedScopes, merged below.
	granted := make(map[string][]string)
	satisfies := make([][]string, len(setup.middlewareHandlers))
	for i, h := range setup.middlewareHandlers {
		satisfies[i] = h.Satisfies
	}
	if len(setup.middlewareHandlers) > 0 {
		reqPtr := reflect.New(setup.reqType)
		reqPtr.Elem().Set(reqVal)
		_, _, outs, mwName, failKind, mwErr := reqreply.DispatchServerMiddlewareHandlers(spanCtx, reqPtr, setup.middlewareHandlers, nil, nil)
		if mwErr != nil {
			kind := KindDecode
			loc := "middleware:in"
			switch failKind {
			case "fn":
				kind = KindMiddleware
				loc = "middleware:fn"
			case "out":
				kind = KindEncode
				loc = "middleware:out"
			}
			stats.ReportErrors(obs, loc, mwErr)
			// A FAILING handler whose OWN Satisfies is non-empty (i.e. a
			// Security-carrying MiddlewareHandler, confirmed via Name
			// lookup) keeps Security's own, DISTINCT fallback
			// (reqreply.SecurityError) — mirrors REST's
			// isSecuritySatisfyingHandler precedent exactly.
			effErr := mwErr
			if me, ok := mwErr.(reqreply.MiddlewareError); ok && failKind == "fn" &&
				reqreply.IsSecuritySatisfyingHandler(setup.middlewareHandlers, mwName) {
				if secObs, ok := obs.(stats.SecurityObserver); ok {
					secObs.RecordSecurityRejection(setup.path, route.FirstSchemeName(setup.secReqs))
				}
				effErr = reqreply.SecurityError{Err: me.Err}
				kind = KindSecurity
			}
			serveErr = effErr
			obs.RecordRequest(sink.label, setup.path, 0, time.Since(start))
			sink.sendError(spanCtx, effErr)
			tryDeadLetterReflect(sockets, setup.deadLetterForMethod, obs, setup.path, payload, effErr)
			if onError != nil {
				onError(ServeError{Kind: kind, Err: effErr})
			}
			return
		}
		reqVal = reqPtr.Elem()
		scopesmerge.MergeHandlerGrants(granted, satisfies, outs)
	}

	// CheckScopes covers any bound MiddlewareHandler's GrantedScopes —
	// gated on [scopesmerge.HasSatisfyingHandler] (at least one BOUND
	// MiddlewareHandler is Security-shaped), NOT on secReqs alone.
	// zeromq's legacy Fn shape (runPairedServerSecurity) is PURE binary
	// accept/reject, with NO grants concept — a route relying SOLELY on
	// it, or on a general-purpose bound middleware, must keep its
	// EXISTING "Fn success = request proceeds" semantics UNCHANGED.
	if len(setup.secReqs) > 0 && scopesmerge.HasSatisfyingHandler(satisfies) {
		if err := reqreply.CheckScopes(setup.secReqs, granted); err != nil {
			wrapped := reqreply.SecurityError{Err: err}
			if secObs, ok := obs.(stats.SecurityObserver); ok {
				secObs.RecordSecurityRejection(setup.path, route.FirstSchemeName(setup.secReqs))
			}
			serveErr = wrapped
			obs.RecordRequest(sink.label, setup.path, 0, time.Since(start))
			sink.sendError(spanCtx, wrapped)
			tryDeadLetterReflect(sockets, setup.deadLetterForMethod, obs, setup.path, payload, wrapped)
			if onError != nil {
				onError(ServeError{Kind: KindSecurity, Err: wrapped})
			}
			return
		}
	}

	fnResults := setup.dispatchFn.Call([]reflect.Value{reflect.ValueOf(spanCtx), reqVal})
	handlerErr, _ := fnResults[1].Interface().(error)
	disposition := stats.ResolveDisposition(spanCtx, handlerErr)
	if dispObs, ok := obs.(stats.DispositionObserver); ok {
		dispObs.RecordDisposition(setup.path, disposition)
	}
	if handlerErr != nil {
		// docs/design/d-0004-reqreply-workflow-simplification.md's Phase
		// 1.4 "Dispatch ordering" rule: a [reqreply.ForwardError] is a
		// DELIBERATE instruction to republish elsewhere, not a failure —
		// it MUST skip ErrorPattern matching AND DeadLetter entirely,
		// sending the reserved forward wire signal instead.
		var fwd reqreply.ForwardError
		if errors.As(handlerErr, &fwd) {
			serveErr = handlerErr
			obs.RecordRequest(sink.label, setup.path, 0, time.Since(start))
			sink.sendForward(fwd)
			if onError != nil {
				onError(ServeError{Kind: KindForward, Err: handlerErr})
			}
			return
		}
		serveErr = handlerErr
		obs.RecordRequest(sink.label, setup.path, 0, time.Since(start))
		sink.sendError(spanCtx, handlerErr)
		tryDeadLetterReflect(sockets, setup.deadLetterForMethod, obs, setup.path, payload, handlerErr)
		if onError != nil {
			onError(ServeError{Kind: KindHandler, Err: handlerErr})
		}
		return
	}
	respVal := fnResults[0]

	encodeResults := setup.encodeWithFormatsMethod.CallSlice([]reflect.Value{respVal, setup.elem.FieldByName("Formats")})
	if errI, _ := encodeResults[1].Interface().(error); errI != nil {
		serveErr = errI
		obs.RecordRequest(sink.label, setup.path, 0, time.Since(start))
		sink.sendError(spanCtx, errI)
		tryDeadLetterReflect(sockets, setup.deadLetterForMethod, obs, setup.path, payload, errI)
		if onError != nil {
			onError(ServeError{Kind: KindEncode, Err: errI})
		}
		return
	}
	respPayload, _ := encodeResults[0].Interface().([]byte)

	if sendErr := sink.sendSuccess(respPayload); sendErr != nil {
		serveErr = sendErr
		obs.RecordRequest(sink.label, setup.path, 0, time.Since(start))
		// A socket-level rejection of the SUCCESSFULLY-encoded reply is
		// ALSO dead-letterable — mirrors every OTHER Category-A failure
		// point in this dispatch, which already consults DeadLetterFor.
		tryDeadLetterReflect(sockets, setup.deadLetterForMethod, obs, setup.path, payload, sendErr)
		if onError != nil {
			onError(ServeError{Kind: KindEncode, Err: sendErr})
		}
		return
	}
	obs.RecordRequest(sink.label, setup.path, 200, time.Since(start))
}
