package mqtt5

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/DaniDeer/go-codex/adapters/internal/scopesmerge"
	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/internal/route"
	"github.com/DaniDeer/go-codex/stats"
	pahomqtt5 "github.com/eclipse/paho.golang/paho"
)

// reqreplyServerSetup bundles every field (*serverTransport).Serve
// previously computed inline, once per Serve call, before entering its
// per-message dispatch closure — Phase 4 events/reqreply family,
// Workstream A's step 7 (docs/roadmap/adapter-integration-architecture.md).
// mqtt5 has only ONE server-socket variant (unlike zeromq's REQ/REP vs.
// ROUTER split), so this extraction is pure refactor-for-future-reuse-by-
// convention, establishing the same setup-core/dispatch-one shape zeromq's
// own reqreply_dispatch_core.go already proved — not a merge of two
// existing duplicates.
type reqreplyServerSetup struct {
	rv                            reflect.Value
	elem                          reflect.Value
	path                          string
	reqType                       reflect.Type
	fnVal                         reflect.Value
	decodeMergedWithFormatsMethod reflect.Value
	encodeWithFormatsMethod       reflect.Value
	observeErrorResponseForMethod reflect.Value
	deadLetterForMethod           reflect.Value
	hasMergeFields                bool
	hasPropertyMergeFields        bool
	impls                         []reqreply.ServerImplementation
	middlewareHandlers            []reqreply.MiddlewareHandler
	requestHeaderParams           []UserPropertyParam
	effectiveQoS                  byte
	effectiveRetained             bool
}

// reqreplyServerSetupCore recovers routeAny/fnAny into a
// [reqreplyServerSetup], running every ONCE-per-Serve-call check: route
// handle recovery, handler shape validation, declarative
// security/middleware coverage (via [reqreply.CheckCoverage]), capability
// application + requirement coverage (via [events.ApplyCapabilities]/
// [reqreply.VerifyCapabilityCoverage]), and request-header-param
// resolution — assembled verbatim from (*serverTransport).Serve's former
// inline setup block (pure extraction, zero behavior change).
func reqreplyServerSetupCore(routeAny, fnAny any, caps []Capability, obs stats.Observer) (*reqreplyServerSetup, error) {
	rv, elem, err := recoverRouteHandleValue(routeAny)
	if err != nil {
		return nil, err
	}
	path := elem.FieldByName("Topic").String()

	decodeField := elem.FieldByName("Decode") // func([]byte) (Req, error)
	encodeField := elem.FieldByName("Encode") // func(Resp) ([]byte, error)
	reqType := decodeField.Type().Out(0)      // Req
	wantFnType := reflect.FuncOf(
		[]reflect.Type{reflect.TypeOf((*context.Context)(nil)).Elem(), reqType},
		[]reflect.Type{encodeField.Type().In(0), reflect.TypeOf((*error)(nil)).Elem()},
		false,
	)
	fnVal := reflect.ValueOf(fnAny)
	if !fnVal.IsValid() || fnVal.Type() != wantFnType {
		return nil, reqreply.TransportTypeMismatchError{Topic: path, Want: wantFnType.String(), Got: fmt.Sprintf("%T", fnAny)}
	}

	decodeMergedWithFormatsMethod := rv.MethodByName("DecodeMergedWithFormats")
	encodeWithFormatsMethod := rv.MethodByName("EncodeWithFormats")
	mergeFieldsMethod := rv.MethodByName("MergeFields")
	hasMergeFields := mergeFieldsMethod.Call(nil)[0].Len() > 0
	propertyMergeFieldsMethod := rv.MethodByName("PropertyMergeFields")
	hasPropertyMergeFields := propertyMergeFieldsMethod.Call(nil)[0].Len() > 0
	observeErrorResponseForMethod := rv.MethodByName("ObserveErrorResponseFor")
	deadLetterForMethod := rv.MethodByName("DeadLetterFor")

	impls, _ := elem.FieldByName("Implementations").Interface().([]reqreply.ServerImplementation)
	if err := validateServerImplementationShapes(path, impls); err != nil {
		return nil, err
	}
	middlewareHandlers, _ := elem.FieldByName("MiddlewareHandlers").Interface().([]reqreply.MiddlewareHandler)
	coverageReqs, _, _ := effectiveSecurity(elem)
	if err := reqreply.CheckCoverage(path, coverageReqs, impls, middlewareHandlers); err != nil {
		return nil, err
	}

	requirements, _ := elem.FieldByName("Requirements").Interface().([]reqreply.CapabilityRequirement)
	wire := WireAttributes{QoS: 1} // unchanged existing defaults: QoS 1, Retained false
	events.ApplyCapabilities(caps, &wire, obs, path)
	effectiveQoS, effectiveRetained := wire.QoS, wire.Retained
	if err := reqreply.VerifyCapabilityCoverage(path, requirements, caps); err != nil {
		return nil, err
	}

	requestHeaderSpecs, _ := elem.FieldByName("RequestHeaderParams").Interface().([]reqreply.HeaderParamSpec)
	requestHeaderParams := userPropertyParamsFromHeaderSpecs(requestHeaderSpecs)

	return &reqreplyServerSetup{
		rv:                            rv,
		elem:                          elem,
		path:                          path,
		reqType:                       reqType,
		fnVal:                         fnVal,
		decodeMergedWithFormatsMethod: decodeMergedWithFormatsMethod,
		encodeWithFormatsMethod:       encodeWithFormatsMethod,
		observeErrorResponseForMethod: observeErrorResponseForMethod,
		deadLetterForMethod:           deadLetterForMethod,
		hasMergeFields:                hasMergeFields,
		hasPropertyMergeFields:        hasPropertyMergeFields,
		impls:                         impls,
		middlewareHandlers:            middlewareHandlers,
		requestHeaderParams:           requestHeaderParams,
		effectiveQoS:                  effectiveQoS,
		effectiveRetained:             effectiveRetained,
	}, nil
}

// reqreplyServerDispatchOne runs the full user-property-validation →
// topic-var → decode → property-merge → security-credential → paired-
// security-middleware → codec-middleware → unified-scopes-check →
// handler → encode → reply pipeline for ONE received message —
// assembled verbatim from (*serverTransport).Serve's former baseHandler
// closure (pure extraction, zero behavior change).
func reqreplyServerDispatchOne(
	ctx context.Context,
	setup *reqreplyServerSetup,
	client MQTTClient,
	obs stats.Observer,
	legacyUserPropertyParams []UserPropertyParam,
	onError func(ServeError),
	msg *pahomqtt5.Publish,
) {
	rv, elem, path, reqType := setup.rv, setup.elem, setup.path, setup.reqType
	decodeMergedWithFormatsMethod := setup.decodeMergedWithFormatsMethod
	encodeWithFormatsMethod := setup.encodeWithFormatsMethod
	observeErrorResponseForMethod := setup.observeErrorResponseForMethod
	deadLetterForMethod := setup.deadLetterForMethod
	hasMergeFields := setup.hasMergeFields
	hasPropertyMergeFields := setup.hasPropertyMergeFields
	impls := setup.impls
	middlewareHandlers := setup.middlewareHandlers
	requestHeaderParams := setup.requestHeaderParams
	effectiveQoS, effectiveRetained := setup.effectiveQoS, setup.effectiveRetained
	fnVal := setup.fnVal

	start := time.Now()
	msgCtx := context.WithValue(ctx, contextKey{}, msg)

	// TraceObserver span — mirrors the escape hatch's [Serve] exactly
	// (span name "mqtt5.serve"). EnsureDispositionBox is called
	// unconditionally, at zero cost to adapters/callers that never call
	// SetDisposition — mqtt5 reqreply has no acknowledgement concept of
	// its own today, but the plumbing is proven end-to-end here for a
	// future ack-capable adapter (e.g. AMQP) to consume without further
	// core changes. See docs/design/d-0006-protocol-native-capabilities.md's §8.
	var spanCtx = stats.EnsureDispositionBox(msgCtx)
	var serveErr error
	if to, ok := obs.(stats.TraceObserver); ok {
		spanCtx = to.StartSpan(spanCtx, "mqtt5.serve", path)
	}
	defer func() {
		if to, ok := obs.(stats.TraceObserver); ok {
			to.EndSpan(spanCtx, serveErr)
		}
	}()

	var responseTopic string
	var correlationData []byte
	if msg.Properties != nil {
		responseTopic = msg.Properties.ResponseTopic
		correlationData = msg.Properties.CorrelationData
	}

	// Session-review fix (F3): User Property param validation is now
	// ErrorPattern/DeadLetter-eligible, closing an asymmetry with
	// REST's own wired header-param validation (Category A row 5) —
	// previously went straight to the plain-text publishErrorReply
	// fallback, bypassing ObserveErrorResponseFor entirely.
	if propErr := validateUserProperties(msg, legacyUserPropertyParams); propErr != nil {
		obs.RecordValidationError("user_property", stats.ConstraintName(propErr), userPropertyName(propErr))
		serveErr = propErr
		obs.RecordRequest("MQTT5-REP", path, 0, time.Since(start))
		publishHandlerErrorReplyReflect(spanCtx, client, observeErrorResponseForMethod, responseTopic, correlationData, propErr, obs, nil, effectiveQoS, effectiveRetained)
		tryDeadLetterReflect(spanCtx, client, deadLetterForMethod, obs, msg.Topic, msg.Payload, propertyVarsFromUserProperties(msg), propErr, effectiveQoS, effectiveRetained)
		if onError != nil {
			onError(ServeError{Kind: KindSecurity, Err: propErr})
		}
		return
	}
	if propErr := validateUserProperties(msg, requestHeaderParams); propErr != nil {
		obs.RecordValidationError("user_property", stats.ConstraintName(propErr), userPropertyName(propErr))
		serveErr = propErr
		obs.RecordRequest("MQTT5-REP", path, 0, time.Since(start))
		publishHandlerErrorReplyReflect(spanCtx, client, observeErrorResponseForMethod, responseTopic, correlationData, propErr, obs, nil, effectiveQoS, effectiveRetained)
		tryDeadLetterReflect(spanCtx, client, deadLetterForMethod, obs, msg.Topic, msg.Payload, propertyVarsFromUserProperties(msg), propErr, effectiveQoS, effectiveRetained)
		if onError != nil {
			onError(ServeError{Kind: KindSecurity, Err: propErr})
		}
		return
	}

	// Topic-var extraction ONLY runs when the route declares
	// NewTopicParam merge fields — mirrors [Serve]'s own "if
	// len(mergeFields) > 0" guard exactly.
	var topicVars map[string]string
	if hasMergeFields || len(middlewareHandlers) > 0 {
		var varErr error
		topicVars, varErr = matchTopicTemplate(path, msg.Topic)
		if varErr == nil {
			validateResults := rv.MethodByName("ValidateTopicVars").Call([]reflect.Value{reflect.ValueOf(topicVars)})
			if errI, _ := validateResults[0].Interface().(error); errI != nil {
				varErr = errI
			}
		}
		if varErr != nil {
			stats.ReportErrors(obs, "topic_var", varErr)
			serveErr = varErr
			obs.RecordRequest("MQTT5-REP", path, 0, time.Since(start))
			publishHandlerErrorReplyReflect(spanCtx, client, observeErrorResponseForMethod, responseTopic, correlationData, varErr, obs, nil, effectiveQoS, effectiveRetained)
			tryDeadLetterReflect(spanCtx, client, deadLetterForMethod, obs, msg.Topic, msg.Payload, propertyVarsFromUserProperties(msg), varErr, effectiveQoS, effectiveRetained)
			if onError != nil {
				onError(ServeError{Kind: KindDecode, Err: varErr})
			}
			return
		}
	}

	// DecodeMergedWithFormats honors handle.RequestFormats (falling back
	// to plain Decode) AND merges topicVars in one call.
	decodeResults := decodeMergedWithFormatsMethod.CallSlice([]reflect.Value{
		reflect.ValueOf(msg.Payload),
		reflect.ValueOf(topicVars),
		elem.FieldByName("RequestFormats"),
	})
	if errI, _ := decodeResults[1].Interface().(error); errI != nil {
		stats.ReportErrors(obs, "body", errI)
		serveErr = errI
		obs.RecordRequest("MQTT5-REP", path, 0, time.Since(start))
		publishHandlerErrorReplyReflect(spanCtx, client, observeErrorResponseForMethod, responseTopic, correlationData, errI, obs, nil, effectiveQoS, effectiveRetained)
		tryDeadLetterReflect(spanCtx, client, deadLetterForMethod, obs, msg.Topic, msg.Payload, propertyVarsFromUserProperties(msg), errI, effectiveQoS, effectiveRetained)
		if onError != nil {
			onError(ServeError{Kind: KindDecode, Err: errI})
		}
		return
	}
	reqVal := decodeResults[0]

	// Route-level (Middleware-free) property merge.
	if hasPropertyMergeFields {
		reqPropVars := propertyVarsFromUserProperties(msg)
		reqPtr := reflect.New(reqType)
		reqPtr.Elem().Set(reqVal)
		mergeResults := rv.MethodByName("MergePropertyVars").Call([]reflect.Value{reqPtr, reflect.ValueOf(reqPropVars)})
		if errI, _ := mergeResults[0].Interface().(error); errI != nil {
			stats.ReportErrors(obs, "property_var", errI)
			serveErr = errI
			obs.RecordRequest("MQTT5-REP", path, 0, time.Since(start))
			publishHandlerErrorReplyReflect(spanCtx, client, observeErrorResponseForMethod, responseTopic, correlationData, errI, obs, nil, effectiveQoS, effectiveRetained)
			tryDeadLetterReflect(spanCtx, client, deadLetterForMethod, obs, msg.Topic, msg.Payload, propertyVarsFromUserProperties(msg), errI, effectiveQoS, effectiveRetained)
			if onError != nil {
				onError(ServeError{Kind: KindDecode, Err: errI})
			}
			return
		}
		reqVal = reqPtr.Elem()
	}

	secReqs, schemeTypes, schemeCodecs := effectiveSecurity(elem)
	if len(secReqs) > 0 {
		var userProps pahomqtt5.UserProperties
		if msg.Properties != nil {
			userProps = msg.Properties.User
		}
		if name, credErr := validateSecurityCredentials(userProps, secReqs, schemeTypes, schemeCodecs); credErr != nil {
			if secObs, ok := obs.(stats.SecurityObserver); ok {
				secObs.RecordSecurityRejection(path, route.FirstSchemeName(secReqs))
			}
			wrapped := reqreply.SecurityCredentialError{Scheme: name, Err: credErr}
			serveErr = wrapped
			obs.RecordRequest("MQTT5-REP", path, 0, time.Since(start))
			publishHandlerErrorReplyReflect(spanCtx, client, observeErrorResponseForMethod, responseTopic, correlationData, wrapped, obs, nil, effectiveQoS, effectiveRetained)
			tryDeadLetterReflect(spanCtx, client, deadLetterForMethod, obs, msg.Topic, msg.Payload, propertyVarsFromUserProperties(msg), wrapped, effectiveQoS, effectiveRetained)
			if onError != nil {
				onError(ServeError{Kind: KindSecurity, Err: wrapped})
			}
			return
		}
	}
	// runServerSecurityMiddleware runs UNCONDITIONALLY (not gated on
	// len(secReqs)>0) — an implementation with an EMPTY Satisfies is a
	// general-purpose presence/format check that always runs.
	granted, err := runServerSecurityMiddleware(msgCtx, msg, impls, secReqs)
	if err != nil {
		if secObs, ok := obs.(stats.SecurityObserver); ok {
			secObs.RecordSecurityRejection(path, route.FirstSchemeName(secReqs))
		}
		wrapped := reqreply.SecurityError{Err: err}
		serveErr = wrapped
		obs.RecordRequest("MQTT5-REP", path, 0, time.Since(start))
		publishHandlerErrorReplyReflect(spanCtx, client, observeErrorResponseForMethod, responseTopic, correlationData, wrapped, obs, nil, effectiveQoS, effectiveRetained)
		tryDeadLetterReflect(spanCtx, client, deadLetterForMethod, obs, msg.Topic, msg.Payload, propertyVarsFromUserProperties(msg), wrapped, effectiveQoS, effectiveRetained)
		if onError != nil {
			onError(ServeError{Kind: KindSecurity, Err: wrapped})
		}
		return
	}

	// docs/design/d-0003-codec-declared-middlewares.md's Addendum:
	// codec-backed Middleware[In,Out] dispatch — runs AFTER the paired
	// security Fn above.
	var middlewarePropertyVars map[string]string
	if len(middlewareHandlers) > 0 {
		reqPropVars := propertyVarsFromUserProperties(msg)
		reqPtr := reflect.New(reqType)
		reqPtr.Elem().Set(reqVal)
		_, outPropVars, outs, mwName, failKind, mwErr := reqreply.DispatchServerMiddlewareHandlers(spanCtx, reqPtr, middlewareHandlers, topicVars, reqPropVars)
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
			effErr := mwErr
			if me, ok := mwErr.(reqreply.MiddlewareError); ok && failKind == "fn" &&
				reqreply.IsSecuritySatisfyingHandler(middlewareHandlers, mwName) {
				if secObs, ok := obs.(stats.SecurityObserver); ok {
					secObs.RecordSecurityRejection(path, route.FirstSchemeName(secReqs))
				}
				effErr = reqreply.SecurityError{Err: me.Err}
				kind = KindSecurity
			}
			serveErr = effErr
			obs.RecordRequest("MQTT5-REP", path, 0, time.Since(start))
			publishHandlerErrorReplyReflect(spanCtx, client, observeErrorResponseForMethod, responseTopic, correlationData, effErr, obs, nil, effectiveQoS, effectiveRetained)
			tryDeadLetterReflect(spanCtx, client, deadLetterForMethod, obs, msg.Topic, msg.Payload, propertyVarsFromUserProperties(msg), effErr, effectiveQoS, effectiveRetained)
			if onError != nil {
				onError(ServeError{Kind: kind, Err: effErr})
			}
			_ = mwName
			return
		}
		reqVal = reqPtr.Elem()
		middlewarePropertyVars = outPropVars
		satisfies := make([][]string, len(middlewareHandlers))
		for i, h := range middlewareHandlers {
			satisfies[i] = h.Satisfies
		}
		scopesmerge.MergeHandlerGrants(granted, satisfies, outs)
	}

	// docs/design/d-0007-declarative-middleware-layering.md's Rollout
	// Phase C: ONE, UNIFIED CheckScopes call covering grants from BOTH
	// the legacy Implementations path AND any bound MiddlewareHandler's
	// own GrantedScopes.
	if len(secReqs) > 0 {
		if err := reqreply.CheckScopes(secReqs, granted); err != nil {
			if secObs, ok := obs.(stats.SecurityObserver); ok {
				secObs.RecordSecurityRejection(path, route.FirstSchemeName(secReqs))
			}
			wrapped := reqreply.SecurityError{Err: err}
			serveErr = wrapped
			obs.RecordRequest("MQTT5-REP", path, 0, time.Since(start))
			publishHandlerErrorReplyReflect(spanCtx, client, observeErrorResponseForMethod, responseTopic, correlationData, wrapped, obs, nil, effectiveQoS, effectiveRetained)
			tryDeadLetterReflect(spanCtx, client, deadLetterForMethod, obs, msg.Topic, msg.Payload, propertyVarsFromUserProperties(msg), wrapped, effectiveQoS, effectiveRetained)
			if onError != nil {
				onError(ServeError{Kind: KindSecurity, Err: wrapped})
			}
			return
		}
	}

	fnResults := fnVal.Call([]reflect.Value{reflect.ValueOf(spanCtx), reqVal})
	handlerErr, _ := fnResults[1].Interface().(error)
	disposition := stats.ResolveDisposition(spanCtx, handlerErr)
	if dispObs, ok := obs.(stats.DispositionObserver); ok {
		dispObs.RecordDisposition(path, disposition)
	}
	if handlerErr != nil {
		// docs/design/d-0004-reqreply-workflow-simplification.md's Phase
		// 1.3 "Dispatch ordering" rule: a [reqreply.ForwardError] is a
		// DELIBERATE instruction to republish elsewhere, not a failure —
		// it MUST skip ErrorPattern matching AND DeadLetter entirely.
		var fwd reqreply.ForwardError
		if errors.As(handlerErr, &fwd) {
			serveErr = handlerErr
			obs.RecordRequest("MQTT5-REP", path, 0, time.Since(start))
			if responseTopic != "" {
				fwdProps := &pahomqtt5.PublishProperties{
					ContentType:     forwardContentType,
					CorrelationData: correlationData,
					User:            pahomqtt5.UserProperties{{Key: forwardTopicPropertyKey, Value: fwd.TargetTopic}},
				}
				if len(middlewarePropertyVars) > 0 {
					fwdProps.User = append(fwdProps.User, userPropertiesFromMap(middlewarePropertyVars)...)
				}
				if len(fwd.Properties) > 0 {
					fwdProps.User = append(fwdProps.User, forwardPropertiesToUserProperties(fwd.Properties)...)
				}
				_, _ = client.Publish(spanCtx, &pahomqtt5.Publish{
					Topic:      responseTopic,
					QoS:        effectiveQoS,
					Retain:     effectiveRetained,
					Payload:    fwd.Payload,
					Properties: fwdProps,
				})
			}
			if onError != nil {
				onError(ServeError{Kind: KindForward, Err: handlerErr})
			}
			return
		}
		serveErr = handlerErr
		obs.RecordRequest("MQTT5-REP", path, 0, time.Since(start))
		publishHandlerErrorReplyReflect(spanCtx, client, observeErrorResponseForMethod, responseTopic, correlationData, handlerErr, obs, middlewarePropertyVars, effectiveQoS, effectiveRetained)
		tryDeadLetterReflect(spanCtx, client, deadLetterForMethod, obs, msg.Topic, msg.Payload, propertyVarsFromUserProperties(msg), handlerErr, effectiveQoS, effectiveRetained)
		if onError != nil {
			onError(ServeError{Kind: KindHandler, Err: handlerErr})
		}
		return
	}
	respVal := fnResults[0]

	encodeResults := encodeWithFormatsMethod.CallSlice([]reflect.Value{respVal, elem.FieldByName("Formats")})
	if errI, _ := encodeResults[1].Interface().(error); errI != nil {
		serveErr = errI
		obs.RecordRequest("MQTT5-REP", path, 0, time.Since(start))
		publishHandlerErrorReplyReflect(spanCtx, client, observeErrorResponseForMethod, responseTopic, correlationData, errI, obs, middlewarePropertyVars, effectiveQoS, effectiveRetained)
		tryDeadLetterReflect(spanCtx, client, deadLetterForMethod, obs, msg.Topic, msg.Payload, propertyVarsFromUserProperties(msg), errI, effectiveQoS, effectiveRetained)
		if onError != nil {
			onError(ServeError{Kind: KindEncode, Err: errI})
		}
		return
	}
	respPayload, _ := encodeResults[0].Interface().([]byte)

	if responseTopic != "" {
		replyProps := &pahomqtt5.PublishProperties{}
		if correlationData != nil {
			replyProps.CorrelationData = correlationData
		}
		if len(middlewarePropertyVars) > 0 {
			replyProps.User = userPropertiesFromMap(middlewarePropertyVars)
		}
		if _, pubErr := client.Publish(spanCtx, &pahomqtt5.Publish{
			Topic:      responseTopic,
			QoS:        effectiveQoS,
			Retain:     effectiveRetained,
			Payload:    respPayload,
			Properties: replyProps,
		}); pubErr != nil {
			serveErr = pubErr
			obs.RecordRequest("MQTT5-REP", path, 0, time.Since(start))
			tryDeadLetterReflect(spanCtx, client, deadLetterForMethod, obs, msg.Topic, msg.Payload, propertyVarsFromUserProperties(msg), pubErr, effectiveQoS, effectiveRetained)
			if onError != nil {
				onError(ServeError{Kind: KindEncode, Err: pubErr})
			}
			return
		}
	}
	obs.RecordRequest("MQTT5-REP", path, 200, time.Since(start))
}
