package zeromq

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/internal/route"
	"github.com/DaniDeer/go-codex/stats"
)

// reqreplyClientSetup bundles every field BOTH `(*clientTransport).call`
// (the plain REQ variant) and `(*dealerClientTransport).call` (the
// DEALER variant) need, once setup — Phase 4 events/reqreply family,
// Workstream C's client-side setup/dispatch-one split, mirroring the
// server-side `reqreplyServerSetup`/`reqreplyServerSetupCore` split
// exactly. Setup-phase recovery/validation has NO protocol dependency
// (capability application/coverage, format-override resolution, the
// observability-only BuildTopic path override, and implementation-shape
// validation do not know or care whether the underlying socket is REQ or
// DEALER) — confirmed identical, textually, between both `call`
// functions before this extraction.
type reqreplyClientSetup struct {
	rv                       reflect.Value
	elem                     reflect.Value
	reqType                  reflect.Type
	respType                 reflect.Type
	requestFormatsOverride   reflect.Value
	responseFormatsOverride  reflect.Value
	clientImpls              []reqreply.ClientImplementation
	clientMiddlewareHandlers []reqreply.ClientMiddlewareHandler
	secReqs                  []reqreply.SecurityRequirement
}

// reqreplyClientRecoverAndResolvePath runs the part of setup that MUST
// complete before a caller's own TraceObserver span starts (matching
// both pre-extraction `call` bodies' exact span placement — immediately
// after the observability-only BuildTopic override resolves, before
// format-override resolution): route handle recovery, capability
// application + requirement coverage, request type-check, and the
// OBSERVABILITY-only BuildTopic path override (vars). label is the
// [stats.Observer.RecordRequest] protocol tag ("ZMQ-REQ"/"ZMQ-DEALER").
func reqreplyClientRecoverAndResolvePath(
	routeAny, reqAny any,
	sock FramedSocket, obs stats.Observer, start time.Time, label string,
	opts CallOptions,
) (rv, elem reflect.Value, reqVal reflect.Value, path string, err error) {
	rv, elem, err = recoverRouteHandleValue(routeAny)
	if err != nil {
		return reflect.Value{}, reflect.Value{}, reflect.Value{}, "", err
	}
	path = elem.FieldByName("Topic").String()

	// Capability application + Client-side Tier 1 coverage check — same
	// mechanism both variants already shared (now literally shared).
	applyCapabilities(sock, opts.Capabilities, obs, path)
	if requirements, ok := elem.FieldByName("Requirements").Interface().([]reqreply.CapabilityRequirement); ok {
		if covErr := reqreply.VerifyCapabilityCoverage(path, requirements, opts.Capabilities); covErr != nil {
			obs.RecordRequest(label, path, 0, time.Since(start))
			return reflect.Value{}, reflect.Value{}, reflect.Value{}, path, covErr
		}
	}

	reqType := elem.FieldByName("EncodeRequest").Type().In(0)
	reqVal = reflect.ValueOf(reqAny)
	if !reqVal.IsValid() || reqVal.Type() != reqType {
		obs.RecordRequest(label, path, 0, time.Since(start))
		return reflect.Value{}, reflect.Value{}, reflect.Value{}, path, reqreply.TransportTypeMismatchError{Topic: path, Want: reqType.String(), Got: fmt.Sprintf("%T", reqAny)}
	}

	// Resolve an OBSERVABILITY-only path override — a BuildTopic failure
	// IS FATAL (matching the former, now-deleted CallDealer escape hatch
	// exactly); does NOT affect socket selection, sock is already
	// resolved by the caller.
	if mergeFieldsMethod := rv.MethodByName("MergeFields"); mergeFieldsMethod.Call(nil)[0].Len() > 0 || opts.Vars != nil {
		var vars map[string]string
		if mergeFieldsMethod.Call(nil)[0].Len() > 0 {
			encodeVarsResults := rv.MethodByName("EncodeVars").Call([]reflect.Value{reqVal})
			vars, _ = encodeVarsResults[0].Interface().(map[string]string)
		}
		if opts.Vars != nil {
			merged := make(map[string]string, len(vars)+len(opts.Vars))
			for k, v := range vars {
				merged[k] = v
			}
			for k, v := range opts.Vars {
				merged[k] = v
			}
			vars = merged
		}
		buildTopicResults := rv.MethodByName("BuildTopic").Call([]reflect.Value{reflect.ValueOf(vars)})
		if errI, _ := buildTopicResults[1].Interface().(error); errI != nil {
			stats.ReportErrors(obs, "topic_var", errI)
			obs.RecordRequest(label, path, 0, time.Since(start))
			return reflect.Value{}, reflect.Value{}, reflect.Value{}, path, CallError{Err: errI}
		}
		path = buildTopicResults[0].String()
	}

	return rv, elem, reqVal, path, nil
}

// reqreplyClientResolveDispatchSetup runs the remainder of setup, AFTER
// a caller's own TraceObserver span has started (matching both
// pre-extraction `call` bodies' exact placement): per-call format
// override resolution (callOpts > opts > declared) and client
// implementation shape validation. rv/elem/path come from
// [reqreplyClientRecoverAndResolvePath]'s return.
func reqreplyClientResolveDispatchSetup(
	rv, elem reflect.Value,
	obs stats.Observer, start time.Time, label string, path string,
	opts CallOptions, callOpts reqreply.ClientCallOptions,
) (*reqreplyClientSetup, error) {
	reqType := elem.FieldByName("EncodeRequest").Type().In(0)

	requestOverrideAny := callOpts.RequestFormats
	if requestOverrideAny == nil {
		requestOverrideAny = opts.RequestFormats
	}
	requestFormatsOverride, fmtErr := resolveCallFormatReflect(requestOverrideAny, elem.FieldByName("RequestFormats").Type())
	if fmtErr != nil {
		obs.RecordRequest(label, path, 0, time.Since(start))
		return nil, CallError{Err: fmtErr}
	}
	responseOverrideAny := callOpts.ResponseFormats
	if responseOverrideAny == nil {
		responseOverrideAny = opts.ResponseFormats
	}
	responseFormatsOverride, fmtErr := resolveCallFormatReflect(responseOverrideAny, elem.FieldByName("Formats").Type())
	if fmtErr != nil {
		obs.RecordRequest(label, path, 0, time.Since(start))
		return nil, CallError{Err: fmtErr}
	}

	// Security Fn-shape dispatch — clientImpls are the
	// [reqreply.Route.ClientMW]-attached implementations, validated for
	// shape EAGERLY, mirroring the server side's identical build-time
	// check.
	respType := elem.FieldByName("DecodeResponse").Type().Out(0)
	clientImpls, _ := elem.FieldByName("ClientImplementations").Interface().([]reqreply.ClientImplementation)
	credentialFnType := buildPairedSecurityFnType(reqType)
	generalDecoratorFnType := buildGeneralDecoratorFnType(reqType, respType)
	if err := validateClientImplementationShapes(clientImpls, credentialFnType, generalDecoratorFnType); err != nil {
		obs.RecordRequest(label, path, 0, time.Since(start))
		return nil, err
	}
	secReqs := effectiveSecurity(elem)
	clientMiddlewareHandlers, _ := elem.FieldByName("ClientMiddlewareHandlers").Interface().([]reqreply.ClientMiddlewareHandler)

	return &reqreplyClientSetup{
		rv:                       rv,
		elem:                     elem,
		reqType:                  reqType,
		respType:                 respType,
		requestFormatsOverride:   requestFormatsOverride,
		responseFormatsOverride:  responseFormatsOverride,
		clientImpls:              clientImpls,
		clientMiddlewareHandlers: clientMiddlewareHandlers,
		secReqs:                  secReqs,
	}, nil
}

// reqreplyClientRecvOne polls sock for exactly one reply, respecting
// ctx.Done() — the recv-poll mechanics BOTH REQ and DEALER variants
// already shared identically (only how the returned frames are
// interpreted differs, left to each [reqreplyClientSink]).
func reqreplyClientRecvOne(ctx context.Context, sock FramedSocket) ([][]byte, error) {
	if err := sock.SetRecvTimeout(recvPollInterval); err != nil {
		return nil, fmt.Errorf("set recv timeout: %w", err)
	}
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		frames, recvErr := sock.RecvFrames()
		if errors.Is(recvErr, ErrTimeout) {
			continue
		}
		if recvErr != nil {
			return nil, fmt.Errorf("recv: %w", recvErr)
		}
		return frames, nil
	}
}

// reqreplyClientSink abstracts the ONE genuine per-variant difference
// left once setup AND recv-poll mechanics are shared: how a request is
// framed when sent, and how many/which frames make up a reply. REQ has
// no addressing concept (2-frame reply: `[status, payload]`); DEALER
// must send an explicit empty delimiter frame (mimicking REQ's hidden
// envelope) and receive one back (3-frame reply: `[delimiter, status,
// payload]`). recvReply returns the LOGICAL frames with any
// transport-specific envelope prefix already stripped — downstream
// parsing in [reqreplyClientCallOne] is then IDENTICAL regardless of
// variant: frames[0] is the status marker, frames[1:] is the rest.
// label is the [stats.Observer.RecordRequest] protocol tag.
type reqreplyClientSink struct {
	label       string
	sendRequest func(payload []byte) error
	recvReply   func(ctx context.Context) (logicalFrames [][]byte, err error)
}

// reqreplyClientCallOne runs the full credential → codec-middleware →
// encode → send → recv → reply-classify (forward/error/ordinary) →
// decode pipeline for ONE call — the Call-one mirror of
// [reqreplyServerDispatchOne], assembled verbatim from the two
// confirmed near-identical `innerCall`/`wrappedCall` bodies both
// `(*clientTransport).call` and `(*dealerClientTransport).call`
// implemented independently. innerCall is wrapped via [reflect.MakeFunc]
// into a concretely-typed `func(context.Context, Req) (Resp, error)`
// value so every attached general-purpose ClientMW decorator (itself a
// real, concretely-typed Go closure) can wrap it.
func reqreplyClientCallOne(
	ctx context.Context,
	setup *reqreplyClientSetup,
	sink reqreplyClientSink,
	obs stats.Observer,
	start time.Time,
	path string,
	reqVal reflect.Value,
) (any, error) {
	errType := reflect.TypeOf((*error)(nil)).Elem()
	zeroResp := reflect.Zero(setup.respType)

	innerType := reflect.FuncOf(
		[]reflect.Type{reflect.TypeOf((*context.Context)(nil)).Elem(), setup.reqType},
		[]reflect.Type{setup.respType, errType},
		false,
	)
	innerCall := reflect.MakeFunc(innerType, func(args []reflect.Value) []reflect.Value {
		// ctx is read from args[0], NOT the outer captured ctx variable
		// — a general-purpose ClientMW decorator wrapping this closure
		// may call next(modifiedCtx, req) with a context it mutated;
		// shadowing the outer name here means every subsequent use of
		// ctx in this closure sees that decorator's context.
		ctx := args[0].Interface().(context.Context)
		innerReqVal := args[1]

		// Paired credential Fns write a credential field INTO a fresh
		// *Req copy, BEFORE encode.
		if len(setup.clientImpls) > 0 {
			reqPtr := reflect.New(setup.reqType)
			reqPtr.Elem().Set(innerReqVal)
			if credErr := runPairedClientCredential(ctx, reqPtr, setup.clientImpls, setup.secReqs); credErr != nil {
				wrapped := reqreply.SecurityCredentialError{Scheme: route.FirstSchemeName(setup.secReqs), Err: credErr}
				if secObs, ok := obs.(stats.SecurityObserver); ok {
					secObs.RecordSecurityRejection(path, route.FirstSchemeName(setup.secReqs))
				}
				obs.RecordRequest(sink.label, path, 0, time.Since(start))
				return []reflect.Value{zeroResp, reflect.ValueOf(CallError{Err: wrapped}).Convert(errType)}
			}
			innerReqVal = reqPtr.Elem()
		}

		// Codec-backed ClientMiddlewareHandler dispatch — runs AFTER the
		// paired credential Fns above.
		if len(setup.clientMiddlewareHandlers) > 0 {
			_, _, mwName, mwErr := reqreply.DispatchClientMiddlewareIn(ctx, innerReqVal, setup.clientMiddlewareHandlers)
			if mwErr != nil {
				_, isFnErr := mwErr.(reqreply.MiddlewareError)
				loc := "middleware:in"
				if isFnErr {
					loc = "middleware:fn"
				}
				stats.ReportErrors(obs, loc, mwErr)
				obs.RecordRequest(sink.label, path, 0, time.Since(start))
				// A FAILING handler whose OWN Satisfies is non-empty (a
				// Security-carrying ClientMiddlewareHandler, confirmed
				// via Name lookup) keeps Security's own, DISTINCT
				// fallback (reqreply.SecurityError).
				effErr := mwErr
				if me, ok := mwErr.(reqreply.MiddlewareError); ok && isFnErr {
					for _, h := range setup.clientMiddlewareHandlers {
						if h.Name == mwName && len(h.Satisfies) > 0 {
							if secObs, ok := obs.(stats.SecurityObserver); ok {
								secObs.RecordSecurityRejection(path, route.FirstSchemeName(setup.secReqs))
							}
							effErr = reqreply.SecurityError{Err: me.Err}
							break
						}
					}
				}
				return []reflect.Value{zeroResp, reflect.ValueOf(CallError{Err: effErr}).Convert(errType)}
			}
		}

		encodeResults := setup.rv.MethodByName("EncodeRequestWithFormats").CallSlice([]reflect.Value{innerReqVal, setup.requestFormatsOverride})
		if errI, _ := encodeResults[1].Interface().(error); errI != nil {
			stats.ReportErrors(obs, "body", errI)
			obs.RecordRequest(sink.label, path, 0, time.Since(start))
			return []reflect.Value{zeroResp, reflect.ValueOf(CallError{Err: errI}).Convert(errType)}
		}
		payload, _ := encodeResults[0].Interface().([]byte)

		if sendErr := sink.sendRequest(payload); sendErr != nil {
			obs.RecordRequest(sink.label, path, 0, time.Since(start))
			return []reflect.Value{zeroResp, reflect.ValueOf(CallError{Err: fmt.Errorf("send: %w", sendErr)}).Convert(errType)}
		}

		frames, recvErr := sink.recvReply(ctx)
		if recvErr != nil {
			obs.RecordRequest(sink.label, path, 0, time.Since(start))
			return []reflect.Value{zeroResp, reflect.ValueOf(CallError{Err: recvErr}).Convert(errType)}
		}

		// Dispatch ordering: a Forward reply (frames[0] == statusForward)
		// is recognized BEFORE an ErrorPattern reply.
		if string(frames[0]) == string(statusForward) && len(frames) >= 3 {
			// RecordRequest status choice: 200, NOT 500 — a Forward is
			// NOT a failure of this round trip.
			obs.RecordRequest(sink.label, path, 200, time.Since(start))
			return []reflect.Value{zeroResp, reflect.ValueOf(reqreply.ForwardError{
				TargetTopic: string(frames[1]), Payload: frames[2],
			}).Convert(errType)}
		}

		if string(frames[0]) == "error" {
			obs.RecordRequest(sink.label, path, 500, time.Since(start))
			// A matched ErrorPattern reply is 3 logical frames:
			// [status, code, body]. The plain-text fallback stays
			// 2 logical frames: [status, body].
			if len(frames) >= 3 {
				code := string(frames[1])
				body := frames[2]
				decodeResults := setup.rv.MethodByName("DecodeErrorFor").Call([]reflect.Value{reflect.ValueOf(code), reflect.ValueOf(body)})
				errResp, _ := decodeResults[0].Interface().(reqreply.ErrorPatternResponse)
				matched, _ := decodeResults[1].Interface().(bool)
				decErr, _ := decodeResults[2].Interface().(error)
				if matched && decErr == nil {
					return []reflect.Value{zeroResp, reflect.ValueOf(CallError{Err: ErrorPatternResponse{
						Code: errResp.Code, Value: errResp.Value, Body: errResp.Body,
					}}).Convert(errType)}
				}
			}
			// UNCHANGED fallback — 2-frame reply, unmatched code, or
			// decode failed. frames[len(frames)-1] is the body in both
			// the 2-frame and unmatched-3-frame case.
			return []reflect.Value{zeroResp, reflect.ValueOf(CallError{Err: fmt.Errorf("server error: %s", frames[len(frames)-1])}).Convert(errType)}
		}

		if len(setup.clientMiddlewareHandlers) > 0 {
			// zeromq has neither a reply-topic nor a property mechanism
			// — both maps are always empty; a route declaring a
			// REQUIRED WithResponseTopic/WithResponseProperty naturally
			// fails here, no special-casing needed.
			if mwErr := reqreply.DispatchClientMiddlewareOut(ctx, nil, nil, setup.clientMiddlewareHandlers); mwErr != nil {
				stats.ReportErrors(obs, "middleware:out", mwErr)
				obs.RecordRequest(sink.label, path, 0, time.Since(start))
				return []reflect.Value{zeroResp, reflect.ValueOf(CallError{Err: mwErr}).Convert(errType)}
			}
		}

		decodeResults := setup.rv.MethodByName("DecodeResponseWithFormats").CallSlice([]reflect.Value{reflect.ValueOf(frames[1]), setup.responseFormatsOverride})
		if errI, _ := decodeResults[1].Interface().(error); errI != nil {
			stats.ReportErrors(obs, "body", errI)
			obs.RecordRequest(sink.label, path, 0, time.Since(start))
			return []reflect.Value{zeroResp, reflect.ValueOf(CallError{Err: fmt.Errorf("decode response: %w", errI)}).Convert(errType)}
		}
		obs.RecordRequest(sink.label, path, 200, time.Since(start))
		return []reflect.Value{decodeResults[0], reflect.Zero(errType)}
	})

	// applyGeneralClientMiddleware wraps innerCall with every
	// general-purpose ClientMW implementation, OUTERMOST-in.
	wrappedCall := innerCall
	for i := len(setup.clientImpls) - 1; i >= 0; i-- {
		if len(setup.clientImpls[i].Satisfies) > 0 {
			continue
		}
		decoratorVal := reflect.ValueOf(setup.clientImpls[i].Fn)
		wrappedCall = decoratorVal.Call([]reflect.Value{wrappedCall})[0]
	}

	finalResults := wrappedCall.Call([]reflect.Value{reflect.ValueOf(ctx), reqVal})
	if errI, _ := finalResults[1].Interface().(error); errI != nil {
		return nil, errI
	}
	return finalResults[0].Interface(), nil
}
