package mqtt5

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/stats"
	pahomqtt5 "github.com/eclipse/paho.golang/paho"
	"github.com/google/uuid"
)

// reqreplyPkgPath is api/reqreply's import path — used to distinguish a
// genuine reqreply.Route[Req,Resp]/*reqreply.RouteHandle[Req,Resp] value
// (for ANY Req/Resp) from an unrelated/wrong-package value passed by
// caller mistake to [AttachServer]/[AttachClient]'s resulting
// [reqreply.ServerTransport]/[reqreply.ClientTransport].
const reqreplyPkgPath = "github.com/DaniDeer/go-codex/api/reqreply"

// recoverRouteHandleValue reflects routeAny into a *reqreply.RouteHandle[Req,Resp]
// reflect.Value, accepting EITHER shape [reqreply.Client.Call]'s confirmed
// dual mode allows: a raw, unregistered reqreply.Route[Req,Resp] (calls
// its ClientHandle() method reflectively to derive one, mirroring
// [adapters/nethttp]'s recoverHandle) OR an already-registered
// *reqreply.RouteHandle[Req,Resp] (used as-is). Returns the handle's
// reflect.Value (always a pointer) and its Elem() struct value for field
// access.
func recoverRouteHandleValue(routeAny any) (reflect.Value, reflect.Value, error) {
	rv := reflect.ValueOf(routeAny)
	if !rv.IsValid() {
		return reflect.Value{}, reflect.Value{}, reqreply.TransportTypeMismatchError{
			Want: "reqreply.Route[Req, Resp] or *reqreply.RouteHandle[Req, Resp]", Got: fmt.Sprintf("%T", routeAny),
		}
	}
	t := rv.Type()
	switch {
	case t.PkgPath() == reqreplyPkgPath && strings.HasPrefix(t.Name(), "Route["):
		// Raw, unregistered Route — derive a *RouteHandle via ClientHandle(),
		// mirroring reqreply.Client.Call's confirmed dual-mode acceptance:
		// GlobalSecurity stays invisible here (ClientHandle sources no
		// Server), the SAME accepted limitation rest.Route.ClientHandle has.
		handleVal := rv.MethodByName("ClientHandle").Call(nil)[0]
		return handleVal, handleVal.Elem(), nil
	case t.Kind() == reflect.Ptr && t.Elem().PkgPath() == reqreplyPkgPath && strings.HasPrefix(t.Elem().Name(), "RouteHandle["):
		// Already-registered *RouteHandle — GlobalSecurity IS populated
		// (from Server.AddGlobalSecurity, via Route.Register), visible and
		// enforced below exactly as it would be for handle.Security.
		return rv, rv.Elem(), nil
	default:
		return reflect.Value{}, reflect.Value{}, reqreply.TransportTypeMismatchError{
			Want: "reqreply.Route[Req, Resp] or *reqreply.RouteHandle[Req, Resp]", Got: fmt.Sprintf("%T", routeAny),
		}
	}
}

// resolveCallFormatReflect type-asserts overrideAny (a
// [reqreply.ClientCallOptions.RequestFormats]/[reqreply.ClientCallOptions.
// ResponseFormats] value) against declaredFieldType (the reflect.Type of
// the route's own []format.Format[Req]/[]format.Format[Resp] field, e.g.
// elem.FieldByName("RequestFormats").Type()) — the reflection-only
// equivalent of [resolveCallFormat] (which can't be called here directly:
// it's generic over T, and this dispatcher never knows T at compile
// time). Returns reflect.Zero(declaredFieldType) (an empty slice of the
// right type) when overrideAny is nil — passing it to
// [reqreply.RouteHandle.EncodeRequestWithFormats]/
// [reqreply.RouteHandle.DecodeResponseWithFormats]'s variadic `formats`
// parameter then correctly falls through to THEIR OWN declared-field
// fallback (`len(formats) == 0`), so this dispatcher never needs to read
// the declared field itself for the fallback case. Returns an error on a
// type mismatch — callers wrap it in [CallError] with the
// direction-appropriate [ErrorKind], mirroring [resolveCallFormat]'s own
// error shape.
func resolveCallFormatReflect(overrideAny any, declaredFieldType reflect.Type) (reflect.Value, error) {
	if overrideAny == nil {
		return reflect.Zero(declaredFieldType), nil
	}
	v := reflect.ValueOf(overrideAny)
	if v.Type() != declaredFieldType {
		return reflect.Value{}, fmt.Errorf("format option: want %s, got %T", declaredFieldType, overrideAny)
	}
	return v, nil
}

// publishHandlerErrorReplyReflect is the reflection-based counterpart of
// [publishHandlerErrorReply] — used by [serverTransport.Serve], which has
// no concretely-typed *reqreply.RouteHandle[Req,Resp] to call the generic
// function with. errorResponseForMethod is
// rv.MethodByName("ErrorResponseFor") — see [serverTransport]'s doc
// comment. Mirrors [publishHandlerErrorReply]'s logic exactly: consults
// ErrorResponseFor(err) first; on a match, publishes the declared
// codec-backed typed payload instead of plain text; on no match, or a
// mapping/encoding failure within the matched pattern, falls back to
// [publishErrorReply]'s plain-text behavior unchanged.
func publishHandlerErrorReplyReflect(
	ctx context.Context,
	client MQTTClient,
	errorResponseForMethod reflect.Value,
	responseTopic string,
	correlationData []byte,
	err error,
	obs stats.Observer,
) {
	if responseTopic == "" {
		return
	}
	results := errorResponseForMethod.Call([]reflect.Value{reflect.ValueOf(err)})
	resp, _ := results[0].Interface().(reqreply.ErrorPatternResponse)
	matched, _ := results[1].Interface().(bool)
	mapErr, _ := results[2].Interface().(error)
	if matched && mapErr == nil {
		props := &pahomqtt5.PublishProperties{
			ContentType:     errorReplyContentType,
			CorrelationData: correlationData,
		}
		_, _ = client.Publish(ctx, &pahomqtt5.Publish{
			Topic:      responseTopic,
			QoS:        1,
			Payload:    resp.Body,
			Properties: props,
		})
		return
	}
	if matched && mapErr != nil {
		stats.ReportErrors(obs, "error_pattern", mapErr)
	}
	publishErrorReply(ctx, client, responseTopic, correlationData, err)
}

// effectiveSecurity resolves elem's effective security requirements
// (Security falling back to GlobalSecurity) and its SecuritySchemes,
// flattened into the two maps [validateSecurityCredentials] expects —
// mirrors [resolveClientSecurity]'s reqreply analogue (REST's own
// version lives in adapters/nethttp/clienttransport.go).
func effectiveSecurity(elem reflect.Value) (reqs []route.SecurityRequirement, schemeTypes map[string]route.SecurityScheme, schemeCodecs map[string]*codex.Codec[string]) {
	reqs, _ = elem.FieldByName("Security").Interface().([]route.SecurityRequirement)
	if reqs == nil {
		reqs, _ = elem.FieldByName("GlobalSecurity").Interface().([]route.SecurityRequirement)
	}
	schemes, _ := elem.FieldByName("SecuritySchemes").Interface().(map[string]reqreply.SecurityScheme)
	schemeTypes = make(map[string]route.SecurityScheme, len(schemes))
	schemeCodecs = make(map[string]*codex.Codec[string], len(schemes))
	for name, s := range schemes {
		schemeTypes[name] = s.SecurityScheme
		schemeCodecs[name] = s.Codec
	}
	return reqs, schemeTypes, schemeCodecs
}

// ── Server side ──────────────────────────────────────────────────────────

// serverTransport implements [reqreply.ServerTransport], wrapping client+
// router+opts — built by [AttachServer]. A reflection shim (mirroring
// [adapters/nethttp]'s clientTransport/[adapters/mqtt5]'s events
// transport): Go forbids generic methods, so Serve recovers the concrete
// Req/Resp types at runtime via reflection against the type-erased
// *reqreply.RouteHandle[Req,Resp] fields (Decode/Encode are struct FIELDS
// holding func values here, not methods — reflect.Value.Call works
// identically either way).
//
// Capability parity with [Serve] (Phase 0 of
// docs/roadmap/reqreply-middleware.md, SHIPPED): route-declared
// [reqreply.RouteHandle.RequestFormats]/[reqreply.RouteHandle.Formats],
// [reqreply.NewTopicParam] merge-field topic-var merging, and
// [reqreply.ErrorPattern]-typed error replies (for handler/encode
// failures) are all honored via [reqreply.RouteHandle.DecodeMergedWithFormats]/
// [reqreply.RouteHandle.EncodeWithFormats]/[reqreply.RouteHandle.ErrorResponseFor],
// reached via reflection against rv (the *RouteHandle[Req,Resp] pointer
// Value) — the same technique already used for the Decode/Encode struct
// fields, just applied to METHODS instead.
type serverTransport struct {
	client MQTTClient
	router MQTTRouter
	opts   ServeOptions
}

// AttachServer binds server+client+router (via an internal ServerTransport
// shim) as server's [reqreply.ServerTransport] — the "attach the adapter
// to the server" step behind [reqreply.Server.Serve]. opts (0 or 1 value)
// configures every route dispatched through this transport uniformly
// (Observer, UserPropertyParams, SecurityFunc, OnError) — see
// [serverTransport]'s doc comment for this shim's documented v1 scope.
//
// Returns [reqreply.ServerTransportAlreadyAttachedError] if server
// already has a transport attached.
//
//	server := reqreply.NewServer(reqreply.Info{Title: "Compute API", Version: "1.0.0"})
//	handle, _ := ComputeRoute.WithHandler(computeHandler).Register(server)
//	_ = mqtt5.AttachServer(server, client, router)
//	err := server.Serve(ctx) // dispatches ComputeRoute concurrently with every other registered route
func AttachServer(server *reqreply.Server, client MQTTClient, router MQTTRouter, opts ...ServeOptions) error {
	var o ServeOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	return server.Attach(&serverTransport{client: client, router: router, opts: o})
}

// Serve implements [reqreply.ServerTransport]. Mirrors [Serve]'s core
// logic (decode → merge-field topic vars → security enforcement → call
// fn → encode → publish reply) via reflection against routeAny/fnAny —
// see [serverTransport]'s doc comment for this shim's documented v1
// scope. Registers a router handler and subscribes, then returns nil
// immediately — NON-BLOCKING, matching [Serve]'s own existing contract
// exactly (this is the confirmed non-blocking-transport branch
// [reqreply.Server.Serve]'s concurrent dispatch relies on).
func (t *serverTransport) Serve(ctx context.Context, routeAny any, fnAny any) error {
	obs := t.opts.Observer
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}

	rv, elem, err := recoverRouteHandleValue(routeAny)
	if err != nil {
		return err
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
		return reqreply.TransportTypeMismatchError{Topic: path, Want: wantFnType.String(), Got: fmt.Sprintf("%T", fnAny)}
	}

	// decodeMergedWithFormatsMethod is *RouteHandle[Req,Resp].
	// DecodeMergedWithFormats(payload []byte, topicVars map[string]string,
	// formats ...format.Format[Req]) (Req, error) — reached via rv (the
	// pointer Value recoverRouteHandleValue returns), not elem, since this
	// is a METHOD, not a field. Honors handle.RequestFormats AND merges
	// NewTopicParam topic vars in one call; behaves identically to a
	// plain Decode when the route declares neither (confirmed via its own
	// godoc) — closes Phase 0 work items 1 and half of 2 (server-side
	// RequestFormats honoring + merge-field support).
	decodeMergedWithFormatsMethod := rv.MethodByName("DecodeMergedWithFormats")
	// encodeWithFormatsMethod is *RouteHandle[Req,Resp].EncodeWithFormats(
	// resp Resp, formats ...format.Format[Resp]) ([]byte, error) — honors
	// handle.Formats, falling back to plain Encode — closes the other
	// half of Phase 0 work item 2 (server-side Formats honoring).
	encodeWithFormatsMethod := rv.MethodByName("EncodeWithFormats")
	// mergeFieldsMethod is *RouteHandle[Req,Resp].MergeFields() []codex.
	// FieldCodec[Req] — used ONLY to decide whether topic-var extraction
	// (matchTopicTemplate) is worth attempting at all, mirroring [Serve]'s
	// own "if len(mergeFields) > 0" guard exactly (avoids a spurious
	// topic-var-mismatch rejection for routes that declare no merge
	// params and whose concrete topic doesn't cleanly "match" the
	// template for unrelated reasons).
	mergeFieldsMethod := rv.MethodByName("MergeFields")
	hasMergeFields := mergeFieldsMethod.Call(nil)[0].Len() > 0
	// errorResponseForMethod is *RouteHandle[Req,Resp].ErrorResponseFor(err
	// error) (ErrorPatternResponse, bool, error) — closes Phase 0 work item 3.
	errorResponseForMethod := rv.MethodByName("ErrorResponseFor")

	t.router.RegisterHandler(path, func(msg *pahomqtt5.Publish) {
		start := time.Now()
		msgCtx := context.WithValue(ctx, contextKey{}, msg)

		// TraceObserver span — mirrors the escape hatch's [Serve] exactly
		// (span name "mqtt5.serve"): a capability that was silently absent
		// from this reflection-based dispatcher before Serve/Call became
		// thin wrappers delegating here (found and closed as part of that
		// delegation, not originally part of Phase 0's 4 work items).
		var spanCtx = msgCtx
		var serveErr error
		if to, ok := obs.(stats.TraceObserver); ok {
			spanCtx = to.StartSpan(msgCtx, "mqtt5.serve", path)
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

		if propErr := validateUserProperties(msg, t.opts.UserPropertyParams); propErr != nil {
			obs.RecordValidationError("user_property", stats.ConstraintName(propErr), userPropertyName(propErr))
			serveErr = propErr
			obs.RecordRequest("MQTT5-REP", path, 0, time.Since(start))
			publishErrorReply(spanCtx, t.client, responseTopic, correlationData, propErr)
			if t.opts.OnError != nil {
				t.opts.OnError(ServeError{Kind: KindSecurity, Err: propErr})
			}
			return
		}

		// Topic-var extraction ONLY runs when the route declares
		// NewTopicParam merge fields — mirrors [Serve]'s own "if
		// len(mergeFields) > 0" guard exactly, avoiding a spurious
		// topic-var-mismatch rejection for routes that declare no merge
		// params (matchTopicTemplate is skipped entirely for them, same
		// as before this change).
		var topicVars map[string]string
		if hasMergeFields {
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
				publishErrorReply(spanCtx, t.client, responseTopic, correlationData, varErr)
				if t.opts.OnError != nil {
					t.opts.OnError(ServeError{Kind: KindDecode, Err: varErr})
				}
				return
			}
		}

		// DecodeMergedWithFormats honors handle.RequestFormats (falling
		// back to plain Decode) AND merges topicVars in one call — closes
		// Phase 0 work items 1 and 2 (server-side).
		decodeResults := decodeMergedWithFormatsMethod.CallSlice([]reflect.Value{
			reflect.ValueOf(msg.Payload),
			reflect.ValueOf(topicVars),
			elem.FieldByName("RequestFormats"),
		})
		if errI, _ := decodeResults[1].Interface().(error); errI != nil {
			stats.ReportErrors(obs, "body", errI)
			serveErr = errI
			obs.RecordRequest("MQTT5-REP", path, 0, time.Since(start))
			publishErrorReply(spanCtx, t.client, responseTopic, correlationData, errI)
			if t.opts.OnError != nil {
				t.opts.OnError(ServeError{Kind: KindDecode, Err: errI})
			}
			return
		}
		reqVal := decodeResults[0]

		secReqs, schemeTypes, schemeCodecs := effectiveSecurity(elem)
		if len(secReqs) > 0 {
			var userProps pahomqtt5.UserProperties
			if msg.Properties != nil {
				userProps = msg.Properties.User
			}
			if name, credErr := validateSecurityCredentials(userProps, secReqs, schemeTypes, schemeCodecs); credErr != nil {
				if secObs, ok := obs.(stats.SecurityObserver); ok {
					secObs.RecordSecurityRejection(path, firstScheme(secReqs))
				}
				wrapped := reqreply.SecurityCredentialError{Scheme: name, Err: credErr}
				serveErr = wrapped
				obs.RecordRequest("MQTT5-REP", path, 0, time.Since(start))
				publishErrorReply(spanCtx, t.client, responseTopic, correlationData, wrapped)
				if t.opts.OnError != nil {
					t.opts.OnError(ServeError{Kind: KindSecurity, Err: wrapped})
				}
				return
			}
			if t.opts.SecurityFunc != nil {
				if err := t.opts.SecurityFunc(msgCtx, msg, secReqs); err != nil {
					if secObs, ok := obs.(stats.SecurityObserver); ok {
						secObs.RecordSecurityRejection(path, firstScheme(secReqs))
					}
					wrapped := reqreply.SecurityError{Err: err}
					serveErr = wrapped
					obs.RecordRequest("MQTT5-REP", path, 0, time.Since(start))
					publishErrorReply(spanCtx, t.client, responseTopic, correlationData, wrapped)
					if t.opts.OnError != nil {
						t.opts.OnError(ServeError{Kind: KindSecurity, Err: wrapped})
					}
					return
				}
			}
		}

		fnResults := fnVal.Call([]reflect.Value{reflect.ValueOf(spanCtx), reqVal})
		if errI, _ := fnResults[1].Interface().(error); errI != nil {
			serveErr = errI
			obs.RecordRequest("MQTT5-REP", path, 0, time.Since(start))
			publishHandlerErrorReplyReflect(spanCtx, t.client, errorResponseForMethod, responseTopic, correlationData, errI, obs)
			if t.opts.OnError != nil {
				t.opts.OnError(ServeError{Kind: KindHandler, Err: errI})
			}
			return
		}
		respVal := fnResults[0]

		// EncodeWithFormats honors handle.Formats, falling back to plain
		// Encode — closes the response-direction half of Phase 0 work
		// item 2 (server-side).
		encodeResults := encodeWithFormatsMethod.CallSlice([]reflect.Value{respVal, elem.FieldByName("Formats")})
		if errI, _ := encodeResults[1].Interface().(error); errI != nil {
			serveErr = errI
			obs.RecordRequest("MQTT5-REP", path, 0, time.Since(start))
			publishHandlerErrorReplyReflect(spanCtx, t.client, errorResponseForMethod, responseTopic, correlationData, errI, obs)
			if t.opts.OnError != nil {
				t.opts.OnError(ServeError{Kind: KindEncode, Err: errI})
			}
			return
		}
		respPayload, _ := encodeResults[0].Interface().([]byte)

		if responseTopic != "" {
			replyProps := &pahomqtt5.PublishProperties{}
			if correlationData != nil {
				replyProps.CorrelationData = correlationData
			}
			if _, pubErr := t.client.Publish(spanCtx, &pahomqtt5.Publish{
				Topic:      responseTopic,
				QoS:        1,
				Payload:    respPayload,
				Properties: replyProps,
			}); pubErr != nil {
				serveErr = pubErr
				obs.RecordRequest("MQTT5-REP", path, 0, time.Since(start))
				if t.opts.OnError != nil {
					t.opts.OnError(ServeError{Kind: KindEncode, Err: pubErr})
				}
				return
			}
		}
		obs.RecordRequest("MQTT5-REP", path, 200, time.Since(start))
	})

	if _, err := t.client.Subscribe(ctx, &pahomqtt5.Subscribe{
		Subscriptions: []pahomqtt5.SubscribeOptions{{Topic: path, QoS: 1}},
	}); err != nil {
		t.router.UnregisterHandler(path)
		return BrokerError{Op: "subscribe", Err: err}
	}
	return nil
}

var _ reqreply.ServerTransport = (*serverTransport)(nil)

// ── Client side ──────────────────────────────────────────────────────────

// clientTransport implements [reqreply.ClientTransport], wrapping client+
// router+opts — built by [AttachClient]. Capability parity with [Call]/
// [CallHandle] (Phase 0 of docs/roadmap/reqreply-middleware.md, SHIPPED):
// route-declared [reqreply.RouteHandle.RequestFormats]/
// [reqreply.RouteHandle.Formats], a per-call [reqreply.ClientCallOptions]
// format override, AND [reqreply.NewTopicParam] merge-field topic-var
// derivation (mirroring [CallHandle]'s auto-derive-from-req convenience)
// are all honored — see [serverTransport]'s doc comment for the shared
// reflection technique.
type clientTransport struct {
	client MQTTClient
	router MQTTRouter
	opts   CallOptions
}

// AttachClient binds client+mqttClient+router (via an internal
// ClientTransport shim) as client's [reqreply.ClientTransport] — the
// "attach the adapter to the client" step behind [reqreply.Client.Call]/
// [reqreply.Client.CallAsync]. opts (0 or 1 value) configures every call
// dispatched through this transport uniformly (Timeout, QoS,
// CredentialFunc, ReplyTopicPrefix/Builder).
//
// Returns [reqreply.ClientTransportAlreadyAttachedError] if client
// already has a transport attached.
//
//	client := reqreply.NewClient()
//	_ = mqtt5.AttachClient(client, mqttClient, router)
//	respAny, err := client.Call(ctx, ComputeRoute, ComputeReq{X: 1, Y: 2})
func AttachClient(client *reqreply.Client, mqttClient MQTTClient, router MQTTRouter, opts ...CallOptions) error {
	var o CallOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	return client.Attach(&clientTransport{client: mqttClient, router: router, opts: o})
}

// Call implements [reqreply.ClientTransport]. Mirrors [Call]'s core logic
// (resolve reply topic → encode request → security credential resolution
// → publish → await reply → decode) via reflection against
// routeAny/reqAny — see [clientTransport]'s doc comment.
func (t *clientTransport) Call(ctx context.Context, routeAny any, reqAny any, opts ...reqreply.ClientCallOptions) (any, error) {
	var o reqreply.ClientCallOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	return t.call(ctx, routeAny, reqAny, o)
}

// call's named return values (result, err) let the deferred TraceObserver
// span-end below observe the eventual error from EVERY return statement in
// this function without needing to touch each one individually — Go
// assigns to named return variables on any `return`, explicit or bare,
// mirroring [Call]'s own `var callErr error` + assign-before-every-return
// pattern with less repetition.
func (t *clientTransport) call(ctx context.Context, routeAny any, reqAny any, callOpts reqreply.ClientCallOptions) (result any, err error) {
	obs := t.opts.Observer
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}
	start := time.Now()

	rv, elem, err := recoverRouteHandleValue(routeAny)
	if err != nil {
		return nil, err
	}
	path := elem.FieldByName("Topic").String()

	// TraceObserver span — mirrors the escape hatch's [Call] exactly (span
	// name "mqtt5.request"): a capability that was silently absent from
	// this reflection-based dispatcher before Serve/Call became thin
	// wrappers delegating here (found and closed as part of that
	// delegation, not originally part of Phase 0's 4 work items).
	if to, ok := obs.(stats.TraceObserver); ok {
		ctx = to.StartSpan(ctx, "mqtt5.request", path)
		defer func() { to.EndSpan(ctx, err) }()
	}

	encodeRequestField := elem.FieldByName("EncodeRequest") // func(Req) ([]byte, error)
	reqType := encodeRequestField.Type().In(0)

	reqVal := reflect.ValueOf(reqAny)
	if !reqVal.IsValid() || reqVal.Type() != reqType {
		obs.RecordRequest("MQTT5-REQ", path, 0, time.Since(start))
		return nil, reqreply.TransportTypeMismatchError{Topic: path, Want: reqType.String(), Got: fmt.Sprintf("%T", reqAny)}
	}

	// Resolve per-call format overrides, in priority order:
	// callOpts.RequestFormats/ResponseFormats (the NEW, Attach-based
	// reqreply.ClientCallOptions parameter, Phase 0) > t.opts.
	// RequestFormats/ResponseFormats (the mqtt5.CallOptions fields the
	// escape hatch's Call/CallHandle have always supported — a fresh
	// clientTransport is built per Call invocation via Call[Req,Resp]'s
	// thin-wrapper form, so this IS effectively per-call too) > nil
	// (meaning "use the route's own declared RequestFormats/Formats, or
	// plain EncodeRequest/DecodeResponse") — closes Phase 0 work item 2
	// (client-side per-call override half) WITHOUT dropping the
	// pre-existing t.opts.RequestFormats/ResponseFormats behavior.
	requestOverrideAny := callOpts.RequestFormats
	if requestOverrideAny == nil {
		requestOverrideAny = t.opts.RequestFormats
	}
	requestFormatsOverride, fmtErr := resolveCallFormatReflect(requestOverrideAny, elem.FieldByName("RequestFormats").Type())
	if fmtErr != nil {
		obs.RecordRequest("MQTT5-REQ", path, 0, time.Since(start))
		return nil, CallError{Kind: KindEncode, Err: fmtErr}
	}
	responseOverrideAny := callOpts.ResponseFormats
	if responseOverrideAny == nil {
		responseOverrideAny = t.opts.ResponseFormats
	}
	responseFormatsOverride, fmtErr := resolveCallFormatReflect(responseOverrideAny, elem.FieldByName("Formats").Type())
	if fmtErr != nil {
		obs.RecordRequest("MQTT5-REQ", path, 0, time.Since(start))
		return nil, CallError{Kind: KindDecode, Err: fmtErr}
	}

	// Topic-var derivation — closes Phase 0 work item 4 (client-side
	// merge-field parity): when the route declares NewTopicParam merge
	// fields, derive vars from req via RouteHandle.EncodeVars (the
	// reflection-callable wrapper around codex.EncodeVars). t.opts.Vars
	// (the SAME CallOptions.Vars field the escape hatch's CallHandle/Call
	// already expose) takes PRECEDENCE over the derived value for the
	// same key — mirrors CallHandle's exact merge precedence. Either a
	// non-empty derived map OR a non-nil t.opts.Vars triggers a
	// RouteHandle.BuildTopic rebuild (mirrors Call's own "opts.Vars !=
	// nil" check, generalized to ALSO cover the derived-only case
	// CallHandle adds on top of it).
	var vars map[string]string
	hasMergeFields := rv.MethodByName("MergeFields").Call(nil)[0].Len() > 0
	if hasMergeFields {
		encodeVarsResults := rv.MethodByName("EncodeVars").Call([]reflect.Value{reqVal})
		if errI, _ := encodeVarsResults[1].Interface().(error); errI != nil {
			obs.RecordRequest("MQTT5-REQ", path, 0, time.Since(start))
			return nil, CallError{Kind: KindEncode, Err: errI}
		}
		vars, _ = encodeVarsResults[0].Interface().(map[string]string)
	}
	// NOTE: t.opts.Vars != nil (not len(...) > 0) — an explicit, even
	// EMPTY, Vars map must still trigger BuildTopic below, so a route
	// with an unresolved template var (e.g. "{tenantID}") surfaces
	// MissingRouteParamError instead of silently publishing the raw
	// template string as a topic. Mirrors the escape hatch's own
	// "opts.Vars != nil" check exactly (reqreply.go's original Call).
	if t.opts.Vars != nil {
		merged := make(map[string]string, len(vars)+len(t.opts.Vars))
		for k, v := range vars {
			merged[k] = v
		}
		for k, v := range t.opts.Vars {
			merged[k] = v
		}
		vars = merged
	}
	if hasMergeFields || t.opts.Vars != nil {
		buildTopicResults := rv.MethodByName("BuildTopic").Call([]reflect.Value{reflect.ValueOf(vars)})
		if errI, _ := buildTopicResults[1].Interface().(error); errI != nil {
			reportRouteParamErrors(errI, obs)
			obs.RecordRequest("MQTT5-REQ", path, 0, time.Since(start))
			return nil, CallError{Kind: KindEncode, Err: errI}
		}
		path = buildTopicResults[0].String()
	}

	timeout := t.opts.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	qos := t.opts.QoS
	if qos == 0 {
		qos = 1
	}

	var replyTopic, subscribeFilter string
	if t.opts.ReplyTopicBuilder != nil {
		replyTopic, subscribeFilter = t.opts.ReplyTopicBuilder()
		if replyTopic == "" {
			obs.RecordRequest("MQTT5-REQ", path, 0, time.Since(start))
			return nil, CallError{Kind: KindEncode, Err: fmt.Errorf("reply topic builder returned empty response topic")}
		}
		if subscribeFilter == "" {
			subscribeFilter = replyTopic
		}
	} else {
		prefix := t.opts.ReplyTopicPrefix
		if prefix == "" {
			prefix = "replies"
		}
		replyTopic = prefix + "/" + uuid.New().String()
		subscribeFilter = replyTopic
	}

	corrID := uuid.New()
	corrData := corrID[:]
	replyCh := make(chan *pahomqtt5.Publish, 1)

	t.router.RegisterHandler(replyTopic, func(msg *pahomqtt5.Publish) {
		if msg.Properties == nil || string(msg.Properties.CorrelationData) != string(corrData) {
			return
		}
		select {
		case replyCh <- msg:
		default:
		}
	})

	if _, err := t.client.Subscribe(ctx, &pahomqtt5.Subscribe{
		Subscriptions: []pahomqtt5.SubscribeOptions{{Topic: subscribeFilter, QoS: qos}},
	}); err != nil {
		t.router.UnregisterHandler(replyTopic)
		obs.RecordRequest("MQTT5-REQ", path, 0, time.Since(start))
		return nil, CallError{Kind: KindEncode, Err: fmt.Errorf("subscribe reply topic: %w", err)}
	}
	var once sync.Once
	cleanup := func() {
		once.Do(func() {
			t.router.UnregisterHandler(replyTopic)
			_, _ = t.client.Unsubscribe(ctx, &pahomqtt5.Unsubscribe{Topics: []string{subscribeFilter}})
		})
	}
	defer cleanup()

	// EncodeRequestWithFormats honors the per-call override (falling back
	// to route-declared RequestFormats, then plain EncodeRequest) — closes
	// Phase 0 work item 2 (client-side).
	encodeResults := rv.MethodByName("EncodeRequestWithFormats").CallSlice([]reflect.Value{reqVal, requestFormatsOverride})
	if errI, _ := encodeResults[1].Interface().(error); errI != nil {
		stats.ReportErrors(obs, "body", errI)
		obs.RecordRequest("MQTT5-REQ", path, 0, time.Since(start))
		return nil, CallError{Kind: KindEncode, Err: errI}
	}
	payload, _ := encodeResults[0].Interface().([]byte)

	secReqs, schemeTypes, schemeCodecs := effectiveSecurity(elem)
	userProps := append(pahomqtt5.UserProperties(nil), t.opts.UserProperties...)
	var credProps []UserProperty
	if len(secReqs) > 0 && t.opts.CredentialFunc != nil {
		var credErr error
		credProps, credErr = t.opts.CredentialFunc(ctx, secReqs)
		if credErr != nil {
			obs.RecordRequest("MQTT5-REQ", path, 0, time.Since(start))
			return nil, credErr
		}
		userProps = append(userProps, credProps...)
	}
	if len(secReqs) > 0 && credProps != nil {
		if name, credErr := validateSecurityCredentials(userProps, secReqs, schemeTypes, schemeCodecs); credErr != nil {
			if secObs, ok := obs.(stats.SecurityObserver); ok {
				secObs.RecordSecurityRejection(path, firstScheme(secReqs))
			}
			obs.RecordRequest("MQTT5-REQ", path, 0, time.Since(start))
			return nil, reqreply.SecurityCredentialError{Scheme: name, Err: credErr}
		}
	}

	reqProps := &pahomqtt5.PublishProperties{ResponseTopic: replyTopic, CorrelationData: corrData}
	if len(userProps) > 0 {
		reqProps.User = userProps
	}
	if _, err := t.client.Publish(ctx, &pahomqtt5.Publish{
		Topic:      path,
		QoS:        qos,
		Payload:    payload,
		Properties: reqProps,
	}); err != nil {
		obs.RecordRequest("MQTT5-REQ", path, 0, time.Since(start))
		return nil, CallError{Kind: KindEncode, Err: fmt.Errorf("publish request: %w", err)}
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		obs.RecordRequest("MQTT5-REQ", path, 0, time.Since(start))
		return nil, CallError{Kind: KindTimeout, Err: ctx.Err()}
	case <-timer.C:
		obs.RecordRequest("MQTT5-REQ", path, 0, time.Since(start))
		return nil, CallError{Kind: KindTimeout, Err: fmt.Errorf("no reply within %s", timeout)}
	case replyMsg := <-replyCh:
		if isErrorReply(replyMsg) {
			obs.RecordRequest("MQTT5-REQ", path, 500, time.Since(start))
			return nil, CallError{Kind: KindHandler, Err: fmt.Errorf("server error: %s", replyMsg.Payload)}
		}
		// DecodeResponseWithFormats honors the per-call override (falling
		// back to route-declared Formats, then plain DecodeResponse) —
		// closes the response-direction half of Phase 0 work item 2
		// (client-side).
		decodeResults := rv.MethodByName("DecodeResponseWithFormats").CallSlice([]reflect.Value{reflect.ValueOf(replyMsg.Payload), responseFormatsOverride})
		if errI, _ := decodeResults[1].Interface().(error); errI != nil {
			stats.ReportErrors(obs, "body", errI)
			obs.RecordRequest("MQTT5-REQ", path, 0, time.Since(start))
			return nil, CallError{Kind: KindDecode, Err: fmt.Errorf("decode response: %w", errI)}
		}
		obs.RecordRequest("MQTT5-REQ", path, 200, time.Since(start))
		return decodeResults[0].Interface(), nil
	}
}

// CallAsync implements [reqreply.ClientTransport]. Non-blocking
// counterpart to [clientTransport.Call]: recovers a *reqreply.Future[Resp]
// (as any) via routeAny's [reqreply.FutureFactory] (confirmed zero-
// reflection-for-generics mechanism, see [reqreply.RouteHandle.NewFutureAny]),
// then runs the SAME dispatch [clientTransport.Call] does in a background
// goroutine, resolving the future exactly once when it completes — the
// "send here, resolve elsewhere" mechanism [reqreply.Client.CallAsync]
// documents.
func (t *clientTransport) CallAsync(ctx context.Context, routeAny any, reqAny any, opts ...reqreply.ClientCallOptions) (any, error) {
	var o reqreply.ClientCallOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	handleVal, _, err := recoverRouteHandleValue(routeAny)
	if err != nil {
		return nil, err
	}
	ff, ok := handleVal.Interface().(reqreply.FutureFactory)
	if !ok {
		return nil, reqreply.TransportTypeMismatchError{Want: "reqreply.FutureFactory", Got: fmt.Sprintf("%T", routeAny)}
	}
	future, resolve := ff.NewFutureAny()
	go func() {
		resp, err := t.call(ctx, routeAny, reqAny, o)
		resolve(resp, err)
	}()
	return future, nil
}

var _ reqreply.ClientTransport = (*clientTransport)(nil)
