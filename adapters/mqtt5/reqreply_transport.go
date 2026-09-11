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
// v1 scope (documented, matching [adapters/mqtt5]'s events.Transport shim's
// own precedent): route-declared [reqreply.RouteHandle.RequestFormats]/
// [reqreply.RouteHandle.Formats] overrides, [reqreply.NewTopicParam]
// merge-field topic-var merging, and [reqreply.ErrorPattern]-typed error
// replies are NOT honored by this shim — it always uses handle.Decode/
// handle.Encode (JSON) and always sends plain-text error replies via
// [publishErrorReply]. A caller needing any of these should use [Serve]
// directly (fully featured, completely unaffected by this addition)
// instead of the [reqreply.Server]/[AttachServer] workflow.
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

	_, elem, err := recoverRouteHandleValue(routeAny)
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

	t.router.RegisterHandler(path, func(msg *pahomqtt5.Publish) {
		start := time.Now()
		msgCtx := context.WithValue(ctx, contextKey{}, msg)

		var responseTopic string
		var correlationData []byte
		if msg.Properties != nil {
			responseTopic = msg.Properties.ResponseTopic
			correlationData = msg.Properties.CorrelationData
		}

		if propErr := validateUserProperties(msg, t.opts.UserPropertyParams); propErr != nil {
			obs.RecordValidationError("user_property", stats.ConstraintName(propErr), userPropertyName(propErr))
			obs.RecordRequest("MQTT5-REP", path, 0, time.Since(start))
			publishErrorReply(msgCtx, t.client, responseTopic, correlationData, propErr)
			if t.opts.OnError != nil {
				t.opts.OnError(ServeError{Kind: KindSecurity, Err: propErr})
			}
			return
		}

		decodeResults := decodeField.Call([]reflect.Value{reflect.ValueOf(msg.Payload)})
		if errI, _ := decodeResults[1].Interface().(error); errI != nil {
			stats.ReportErrors(obs, "body", errI)
			obs.RecordRequest("MQTT5-REP", path, 0, time.Since(start))
			publishErrorReply(msgCtx, t.client, responseTopic, correlationData, errI)
			if t.opts.OnError != nil {
				t.opts.OnError(ServeError{Kind: KindDecode, Err: errI})
			}
			return
		}
		reqVal := decodeResults[0]

		// NOTE — v1 scope: [reqreply.NewTopicParam] merge-field topic-var
		// merging (the [Serve] function's own MergeFields+DecodeVars step)
		// is NOT performed by this shim — codex.DecodeVars needs a
		// concretely-typed *Req and []codex.FieldCodec[Req], neither of
		// which this reflection-only dispatcher can safely reconstruct
		// without knowing Req at compile time. A route relying on
		// NewTopicParam merge should use [Serve] directly (fully
		// featured, completely unaffected by this addition) — mirrors
		// [adapters/mqtt5]'s events.Transport shim's own documented
		// scope-down precedent.

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
				obs.RecordRequest("MQTT5-REP", path, 0, time.Since(start))
				publishErrorReply(msgCtx, t.client, responseTopic, correlationData, wrapped)
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
					obs.RecordRequest("MQTT5-REP", path, 0, time.Since(start))
					publishErrorReply(msgCtx, t.client, responseTopic, correlationData, wrapped)
					if t.opts.OnError != nil {
						t.opts.OnError(ServeError{Kind: KindSecurity, Err: wrapped})
					}
					return
				}
			}
		}

		fnResults := fnVal.Call([]reflect.Value{reflect.ValueOf(msgCtx), reqVal})
		if errI, _ := fnResults[1].Interface().(error); errI != nil {
			obs.RecordRequest("MQTT5-REP", path, 0, time.Since(start))
			publishErrorReply(msgCtx, t.client, responseTopic, correlationData, errI)
			if t.opts.OnError != nil {
				t.opts.OnError(ServeError{Kind: KindHandler, Err: errI})
			}
			return
		}
		respVal := fnResults[0]

		encodeResults := encodeField.Call([]reflect.Value{respVal})
		if errI, _ := encodeResults[1].Interface().(error); errI != nil {
			obs.RecordRequest("MQTT5-REP", path, 0, time.Since(start))
			publishErrorReply(msgCtx, t.client, responseTopic, correlationData, errI)
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
			if _, pubErr := t.client.Publish(msgCtx, &pahomqtt5.Publish{
				Topic:      responseTopic,
				QoS:        1,
				Payload:    respPayload,
				Properties: replyProps,
			}); pubErr != nil {
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
// router+opts — built by [AttachClient]. See [serverTransport]'s doc
// comment for this shim's identical reflection technique and documented
// v1 scope (route-declared RequestFormats/Formats overrides not honored;
// use [Call]/[CallHandle] directly for full control).
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
// routeAny/reqAny — see [clientTransport]'s doc comment for this shim's
// documented v1 scope.
func (t *clientTransport) Call(ctx context.Context, routeAny any, reqAny any) (any, error) {
	return t.call(ctx, routeAny, reqAny)
}

func (t *clientTransport) call(ctx context.Context, routeAny any, reqAny any) (any, error) {
	obs := t.opts.Observer
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}
	start := time.Now()

	_, elem, err := recoverRouteHandleValue(routeAny)
	if err != nil {
		return nil, err
	}
	path := elem.FieldByName("Topic").String()

	encodeRequestField := elem.FieldByName("EncodeRequest")   // func(Req) ([]byte, error)
	decodeResponseField := elem.FieldByName("DecodeResponse") // func([]byte) (Resp, error)
	reqType := encodeRequestField.Type().In(0)

	reqVal := reflect.ValueOf(reqAny)
	if !reqVal.IsValid() || reqVal.Type() != reqType {
		obs.RecordRequest("MQTT5-REQ", path, 0, time.Since(start))
		return nil, reqreply.TransportTypeMismatchError{Topic: path, Want: reqType.String(), Got: fmt.Sprintf("%T", reqAny)}
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

	encodeResults := encodeRequestField.Call([]reflect.Value{reqVal})
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
		decodeResults := decodeResponseField.Call([]reflect.Value{reflect.ValueOf(replyMsg.Payload)})
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
func (t *clientTransport) CallAsync(ctx context.Context, routeAny any, reqAny any) (any, error) {
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
		resp, err := t.call(ctx, routeAny, reqAny)
		resolve(resp, err)
	}()
	return future, nil
}

var _ reqreply.ClientTransport = (*clientTransport)(nil)
