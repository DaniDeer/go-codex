package zeromq

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/stats"
)

// reqreplyPkgPath is api/reqreply's import path — used to distinguish a
// genuine reqreply.Route[Req,Resp]/*reqreply.RouteHandle[Req,Resp] value
// (for ANY Req/Resp) from an unrelated/wrong-package value passed by
// caller mistake to [AttachServer]/[AttachClient]/[AttachRouterServer]/
// [AttachDealerClient]'s resulting [reqreply.ServerTransport]/
// [reqreply.ClientTransport]. Mirrors [adapters/mqtt5]'s identical
// constant.
const reqreplyPkgPath = "github.com/DaniDeer/go-codex/api/reqreply"

// recoverRouteHandleValue reflects routeAny into a *reqreply.RouteHandle[Req,Resp]
// reflect.Value, accepting EITHER shape [reqreply.Client.Call]'s confirmed
// dual mode allows: a raw, unregistered reqreply.Route[Req,Resp] (calls
// its ClientHandle() method reflectively to derive one) OR an
// already-registered *reqreply.RouteHandle[Req,Resp] (used as-is).
// Mirrors [adapters/mqtt5]'s identical helper (duplicated rather than
// shared — the two adapter packages do not import each other).
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
		handleVal := rv.MethodByName("ClientHandle").Call(nil)[0]
		return handleVal, handleVal.Elem(), nil
	case t.Kind() == reflect.Ptr && t.Elem().PkgPath() == reqreplyPkgPath && strings.HasPrefix(t.Elem().Name(), "RouteHandle["):
		return rv, rv.Elem(), nil
	default:
		return reflect.Value{}, reflect.Value{}, reqreply.TransportTypeMismatchError{
			Want: "reqreply.Route[Req, Resp] or *reqreply.RouteHandle[Req, Resp]", Got: fmt.Sprintf("%T", routeAny),
		}
	}
}

// resolveCallFormatReflect type-asserts overrideAny (a
// [reqreply.ClientCallOptions.RequestFormats]/[reqreply.ClientCallOptions.
// ResponseFormats] value, or a [CallOptions.RequestFormats]/
// [ResponseFormats] value) against declaredFieldType (the reflect.Type of
// the route's own []format.Format[Req]/[]format.Format[Resp] field) — the
// reflection-only equivalent of [resolveCallFormat] (which can't be
// called here directly: it's generic over T, and this dispatcher never
// knows T at compile time). Returns reflect.Zero(declaredFieldType) (an
// empty slice of the right type) when overrideAny is nil — passing it to
// [reqreply.RouteHandle.EncodeRequestWithFormats]/[DecodeResponseWithFormats]/
// [DecodeWithFormats]/[EncodeWithFormats]'s variadic `formats` parameter
// then correctly falls through to THEIR OWN declared-field fallback.
// Mirrors [adapters/mqtt5]'s identical helper (duplicated rather than
// shared — the two adapter packages do not import each other).
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

// sendHandlerErrorReplyReflect is the reflection-based counterpart of
// [sendHandlerErrorReply] — used by [serverTransport.Serve], which has
// no concretely-typed *reqreply.RouteHandle[Req,Resp] to call the generic
// function with. errorResponseForMethod is
// rv.MethodByName("ErrorResponseFor"). Mirrors [sendHandlerErrorReply]'s
// logic exactly: consults ErrorResponseFor(err) first; on a match, sends
// the declared codec-backed typed payload instead of plain text; on no
// match, or a mapping/encoding failure within the matched pattern, falls
// back to [sendErrorReply]'s plain-text behavior unchanged.
func sendHandlerErrorReplyReflect(sock FramedSocket, errorResponseForMethod reflect.Value, err error, obs stats.Observer) {
	results := errorResponseForMethod.Call([]reflect.Value{reflect.ValueOf(err)})
	resp, _ := results[0].Interface().(reqreply.ErrorPatternResponse)
	matched, _ := results[1].Interface().(bool)
	mapErr, _ := results[2].Interface().(error)
	if matched && mapErr == nil {
		_ = sock.SendFrames([][]byte{statusError, resp.Body})
		return
	}
	if matched && mapErr != nil {
		stats.ReportErrors(obs, "error_pattern", mapErr)
	}
	sendErrorReply(sock, err)
}

// sendRouterHandlerErrorReplyReflect is [sendHandlerErrorReplyReflect]'s
// ROUTER-socket counterpart — preserves the identity frame, mirroring
// [sendRouterHandlerErrorReply]'s logic exactly.
func sendRouterHandlerErrorReplyReflect(sock FramedSocket, identity []byte, errorResponseForMethod reflect.Value, err error, obs stats.Observer) {
	results := errorResponseForMethod.Call([]reflect.Value{reflect.ValueOf(err)})
	resp, _ := results[0].Interface().(reqreply.ErrorPatternResponse)
	matched, _ := results[1].Interface().(bool)
	mapErr, _ := results[2].Interface().(error)
	if matched && mapErr == nil {
		_ = sock.SendFrames([][]byte{identity, emptyDelimiter, statusError, resp.Body})
		return
	}
	if matched && mapErr != nil {
		stats.ReportErrors(obs, "error_pattern", mapErr)
	}
	sendRouterErrorReply(sock, identity, err)
}

// MissingSocketError is returned by [AttachServer]/[AttachRouterServer]
// when server has a route registered whose topic is not a key in the
// sockets map, and by [AttachClient]/[AttachDealerClient]'s resulting
// transport when a call is made for a route whose topic is likewise
// missing. Unlike MQTT5's single shared client+router, ZMQ REQ/REP and
// ROUTER/DEALER sockets are point-to-point (one socket per logical
// contract, no topic-multiplexing built in the way a SUB socket has) —
// so [reqreply.Server.RegisteredTopics] is used to validate full
// topic/socket coverage at Attach time, before [reqreply.Server.Serve]
// ever runs, rather than discovering a missing socket only when a
// request for it actually arrives.
type MissingSocketError struct {
	// Topic is the route topic with no corresponding socket entry.
	Topic string
}

func (e MissingSocketError) Error() string {
	return fmt.Sprintf("zeromq: no socket registered for route topic %q (add it to the sockets map passed to AttachServer/AttachClient/AttachRouterServer/AttachDealerClient)", e.Topic)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e MissingSocketError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("topic", e.Topic),
	)
}

// ── REQ/REP server ───────────────────────────────────────────────────────

// serverTransport implements [reqreply.ServerTransport] for ZMQ REQ/REP
// sockets, wrapping a topic→socket map — built by [AttachServer]. A
// reflection shim (mirrors [adapters/mqtt5]'s identical technique): Go
// forbids generic methods, so Serve recovers the concrete Req/Resp types
// at runtime via reflection against the type-erased
// *reqreply.RouteHandle[Req,Resp] fields (Decode/Encode are struct
// FIELDS holding func values, not methods — reflect.Value.Call works
// identically either way).
//
// v1 scope (documented, matching [adapters/mqtt5]'s reqreply shim's own
// precedent): route-declared [reqreply.RouteHandle.RequestFormats]/
// [reqreply.RouteHandle.Formats] overrides and [reqreply.ErrorPattern]-
// typed error replies are NOT honored by this shim — it always uses
// handle.Decode/handle.Encode (JSON) and always sends plain-text error
// replies via [sendErrorReply]. A caller needing either of these should
// use [Serve] directly (fully featured, completely unaffected by this
// addition) instead of the [reqreply.Server]/[AttachServer] workflow.
//
// Serve BLOCKS until ctx is cancelled or a fatal socket error occurs —
// unlike [adapters/mqtt5]'s non-blocking Serve (which registers +
// returns immediately), ZMQ has no built-in dispatch loop of its own to
// delegate to, so this shim runs its own receive loop, exactly like
// [Serve] does. This is the confirmed BLOCKING-transport branch
// [reqreply.Server.Serve]'s concurrent dispatch (one goroutine per
// route) is designed to accommodate.
type serverTransport struct {
	sockets map[string]FramedSocket
	opts    ServeOptions
}

// AttachServer binds server+sockets (via an internal ServerTransport
// shim) as server's [reqreply.ServerTransport] — the "attach the adapter
// to the server" step behind [reqreply.Server.Serve]. sockets maps each
// registered route's topic to the REP socket handling it (REQ/REP is
// point-to-point, so one socket serves exactly one route/topic — unlike
// pub/sub's topic-multiplexed SUB socket). opts (0 or 1 value) configures
// every route dispatched through this transport uniformly.
//
// Returns [MissingSocketError] if server has a registered route whose
// topic has no entry in sockets — checked upfront, at Attach time, not
// discovered later when a request for it arrives. Returns
// [reqreply.ServerTransportAlreadyAttachedError] if server already has a
// transport attached.
//
//	server := reqreply.NewServer(reqreply.Info{Title: "Compute API", Version: "1.0.0"})
//	handle, _ := ComputeRoute.WithHandler(computeHandler).Register(server)
//	err := zeromq.AttachServer(server, map[string]zeromq.FramedSocket{"compute/add": repSock})
//	err = server.Serve(ctx) // blocks, dispatching ComputeRoute (and every other registered route) concurrently
func AttachServer(server *reqreply.Server, sockets map[string]FramedSocket, opts ...ServeOptions) error {
	var o ServeOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	for _, topic := range server.RegisteredTopics() {
		if _, ok := sockets[topic]; !ok {
			return MissingSocketError{Topic: topic}
		}
	}
	return server.Attach(&serverTransport{sockets: sockets, opts: o})
}

// Serve implements [reqreply.ServerTransport]. Mirrors [Serve]'s core
// receive loop (recv → decode → call fn → encode → send reply) via
// reflection against routeAny/fnAny — see [serverTransport]'s doc
// comment for this shim's documented v1 scope and blocking contract.
func (t *serverTransport) Serve(ctx context.Context, routeAny any, fnAny any) error {
	rv, elem, err := recoverRouteHandleValue(routeAny)
	if err != nil {
		return err
	}
	path := elem.FieldByName("Topic").String()
	sock, ok := t.sockets[path]
	if !ok {
		return MissingSocketError{Topic: path}
	}

	obs := t.opts.Observer
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}

	decodeField := elem.FieldByName("Decode") // func([]byte) (Req, error)
	encodeField := elem.FieldByName("Encode") // func(Resp) ([]byte, error)
	reqType := decodeField.Type().Out(0)
	wantFnType := reflect.FuncOf(
		[]reflect.Type{reflect.TypeOf((*context.Context)(nil)).Elem(), reqType},
		[]reflect.Type{encodeField.Type().In(0), reflect.TypeOf((*error)(nil)).Elem()},
		false,
	)
	fnVal := reflect.ValueOf(fnAny)
	if !fnVal.IsValid() || fnVal.Type() != wantFnType {
		return reqreply.TransportTypeMismatchError{Topic: path, Want: wantFnType.String(), Got: fmt.Sprintf("%T", fnAny)}
	}

	// decodeWithFormatsMethod/encodeWithFormatsMethod honor
	// handle.RequestFormats/Formats, falling back to plain Decode/Encode
	// — closes Phase 0 work item 1 (server-side). No merge-field support
	// is added here (unlike mqtt5): zeromq's REQ/REP wire format carries
	// no topic frame at all (routing is entirely socket-based, one
	// socket per concrete topic, never a template) — NewTopicParam
	// merge-field decode was never applicable here, confirmed via the
	// escape hatch's own [Serve], which never called MergeFields/
	// DecodeVars either.
	decodeWithFormatsMethod := rv.MethodByName("DecodeWithFormats")
	encodeWithFormatsMethod := rv.MethodByName("EncodeWithFormats")
	// errorResponseForMethod is *RouteHandle[Req,Resp].ErrorResponseFor —
	// closes Phase 0 work item 2 (server-side).
	errorResponseForMethod := rv.MethodByName("ErrorResponseFor")

	if err := sock.SetRecvTimeout(recvPollInterval); err != nil {
		return SocketError{Op: "set_recv_timeout", Err: err}
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		frames, recvErr := sock.RecvFrames()
		if errors.Is(recvErr, ErrTimeout) {
			continue
		}
		if recvErr != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				return SocketError{Op: "recv", Err: recvErr}
			}
		}
		if len(frames) == 0 {
			continue
		}
		start := time.Now()
		payload := frames[0]

		// TraceObserver span — mirrors the escape hatch's [serveRequest]
		// exactly (span name "zmq.serve"): a capability that was
		// silently absent from this reflection-based dispatcher (found
		// and closed as part of Phase 0/0b, mirroring the SAME gap found
		// and fixed for mqtt5).
		spanCtx := ctx
		var serveErr error
		if to, ok := obs.(stats.TraceObserver); ok {
			spanCtx = to.StartSpan(ctx, "zmq.serve", path)
		}
		endSpan := func() {
			if to, ok := obs.(stats.TraceObserver); ok {
				to.EndSpan(spanCtx, serveErr)
			}
		}

		// DecodeWithFormats honors handle.RequestFormats, falling back
		// to plain Decode — closes Phase 0 work item 1 (server-side).
		decodeResults := decodeWithFormatsMethod.CallSlice([]reflect.Value{reflect.ValueOf(payload), elem.FieldByName("RequestFormats")})
		if errI, _ := decodeResults[1].Interface().(error); errI != nil {
			stats.ReportErrors(obs, "body", errI)
			serveErr = errI
			obs.RecordRequest("ZMQ-REP", path, 0, time.Since(start))
			sendErrorReply(sock, errI)
			endSpan()
			if t.opts.OnError != nil {
				t.opts.OnError(ServeError{Kind: KindDecode, Err: errI})
			}
			continue
		}
		reqVal := decodeResults[0]

		fnResults := fnVal.Call([]reflect.Value{reflect.ValueOf(spanCtx), reqVal})
		if errI, _ := fnResults[1].Interface().(error); errI != nil {
			serveErr = errI
			obs.RecordRequest("ZMQ-REP", path, 0, time.Since(start))
			sendHandlerErrorReplyReflect(sock, errorResponseForMethod, errI, obs)
			endSpan()
			if t.opts.OnError != nil {
				t.opts.OnError(ServeError{Kind: KindHandler, Err: errI})
			}
			continue
		}
		respVal := fnResults[0]

		// EncodeWithFormats honors handle.Formats, falling back to plain
		// Encode — closes the response-direction half of Phase 0 work
		// item 1 (server-side).
		encodeResults := encodeWithFormatsMethod.CallSlice([]reflect.Value{respVal, elem.FieldByName("Formats")})
		if errI, _ := encodeResults[1].Interface().(error); errI != nil {
			serveErr = errI
			obs.RecordRequest("ZMQ-REP", path, 0, time.Since(start))
			sendHandlerErrorReplyReflect(sock, errorResponseForMethod, errI, obs)
			endSpan()
			if t.opts.OnError != nil {
				t.opts.OnError(ServeError{Kind: KindEncode, Err: errI})
			}
			continue
		}
		respPayload, _ := encodeResults[0].Interface().([]byte)

		if sendErr := sock.SendFrames([][]byte{statusOK, respPayload}); sendErr != nil {
			serveErr = sendErr
			obs.RecordRequest("ZMQ-REP", path, 0, time.Since(start))
			endSpan()
			if t.opts.OnError != nil {
				t.opts.OnError(ServeError{Kind: KindEncode, Err: sendErr})
			}
			continue
		}
		obs.RecordRequest("ZMQ-REP", path, 200, time.Since(start))
		endSpan()
	}
}

var _ reqreply.ServerTransport = (*serverTransport)(nil)

// ── REQ/REP client ───────────────────────────────────────────────────────

// clientTransport implements [reqreply.ClientTransport] for ZMQ REQ
// sockets, wrapping a topic→socket map — built by [AttachClient]. See
// [serverTransport]'s doc comment for this shim's identical reflection
// technique and documented v1 scope.
type clientTransport struct {
	sockets map[string]FramedSocket
	opts    CallOptions
}

// AttachClient binds client+sockets (via an internal ClientTransport
// shim) as client's [reqreply.ClientTransport] — the "attach the adapter
// to the client" step behind [reqreply.Client.Call]/
// [reqreply.Client.CallAsync]. sockets maps each route's topic to the
// REQ socket used to call it. opts (0 or 1 value) configures every call
// dispatched through this transport uniformly.
//
// Returns [reqreply.ClientTransportAlreadyAttachedError] if client
// already has a transport attached. A call for a route whose topic has
// no entry in sockets returns [MissingSocketError] at call time (no
// upfront coverage check on the client side — unlike [AttachServer],
// the client may only ever need a subset of the server's registered
// routes).
//
//	client := reqreply.NewClient()
//	_ = zeromq.AttachClient(client, map[string]zeromq.FramedSocket{"compute/add": reqSock})
//	respAny, err := client.Call(ctx, ComputeRoute, ComputeReq{X: 1, Y: 2})
func AttachClient(client *reqreply.Client, sockets map[string]FramedSocket, opts ...CallOptions) error {
	var o CallOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	return client.Attach(&clientTransport{sockets: sockets, opts: o})
}

// Call implements [reqreply.ClientTransport]. Mirrors [Call]'s core logic
// (encode request → send → await reply → decode) via reflection against
// routeAny/reqAny — see [clientTransport]'s doc comment. opts carries a
// per-call format override — see [clientTransport.call].
func (t *clientTransport) Call(ctx context.Context, routeAny any, reqAny any, opts ...reqreply.ClientCallOptions) (any, error) {
	var o reqreply.ClientCallOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	return t.call(ctx, routeAny, reqAny, o)
}

// call's named return values (result, err) let the deferred
// TraceObserver span-end observe the eventual error from EVERY return
// statement in this function without touching each one individually —
// mirrors [adapters/mqtt5]'s identical pattern.
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

	sock, ok := t.sockets[path]
	if !ok {
		obs.RecordRequest("ZMQ-REQ", path, 0, time.Since(start))
		return nil, MissingSocketError{Topic: path}
	}

	reqType := elem.FieldByName("EncodeRequest").Type().In(0)
	reqVal := reflect.ValueOf(reqAny)
	if !reqVal.IsValid() || reqVal.Type() != reqType {
		obs.RecordRequest("ZMQ-REQ", path, 0, time.Since(start))
		return nil, reqreply.TransportTypeMismatchError{Topic: path, Want: reqType.String(), Got: fmt.Sprintf("%T", reqAny)}
	}

	// Resolve an OBSERVABILITY-only path override (span name/RecordRequest
	// argument), mirroring the escape hatch's [CallHandle]/[Call]
	// CallOptions.Vars precedence EXACTLY: auto-derive from req via
	// RouteHandle.EncodeVars when the route declares NewTopicParam merge
	// fields, then let an explicit t.opts.Vars entry win on the same key.
	// Does NOT affect socket selection (sock is already resolved above,
	// via the route's literal registered topic — zeromq REQ/REP routing
	// is socket-based, never topic-template-based, confirmed via the
	// escape hatch's own doc comment on [CallHandle]). A BuildTopic
	// failure here IS FATAL (mirrors the escape hatch's [Call] exactly —
	// confirmed via TestCall_Vars_MissingVar_ReturnsCallError, which
	// expects a CallError{MissingRouteParamError} BEFORE anything is
	// ever sent) — an earlier revision of this fix wrongly treated it as
	// a non-fatal, ignorable observability-labeling concern, which left
	// the socket send/recv path to run with NO reply ever coming,
	// hanging indefinitely.
	if mergeFieldsMethod := rv.MethodByName("MergeFields"); mergeFieldsMethod.Call(nil)[0].Len() > 0 || t.opts.Vars != nil {
		var vars map[string]string
		if mergeFieldsMethod.Call(nil)[0].Len() > 0 {
			encodeVarsResults := rv.MethodByName("EncodeVars").Call([]reflect.Value{reqVal})
			vars, _ = encodeVarsResults[0].Interface().(map[string]string)
		}
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
		buildTopicResults := rv.MethodByName("BuildTopic").Call([]reflect.Value{reflect.ValueOf(vars)})
		if errI, _ := buildTopicResults[1].Interface().(error); errI != nil {
			stats.ReportErrors(obs, "topic_var", errI)
			obs.RecordRequest("ZMQ-REQ", path, 0, time.Since(start))
			return nil, CallError{Err: errI}
		}
		path = buildTopicResults[0].String()
	}

	// TraceObserver span — mirrors the escape hatch's [Call] exactly
	// (span name "zmq.request"): a capability that was silently absent
	// from this reflection-based dispatcher, found and closed as part of
	// Phase 0/0b, mirroring the SAME gap found and fixed for mqtt5.
	if to, ok := obs.(stats.TraceObserver); ok {
		ctx = to.StartSpan(ctx, "zmq.request", path)
		defer func() { to.EndSpan(ctx, err) }()
	}

	// Resolve per-call format overrides, in priority order:
	// callOpts.RequestFormats/ResponseFormats (the NEW, Attach-based
	// reqreply.ClientCallOptions parameter) > t.opts.RequestFormats/
	// ResponseFormats (the mqtt5.CallOptions-style fields the escape
	// hatch's Call/CallHandle have always supported) > nil (route-
	// declared, or plain EncodeRequest/DecodeResponse) — mirrors
	// [adapters/mqtt5]'s identical priority chain exactly.
	requestOverrideAny := callOpts.RequestFormats
	if requestOverrideAny == nil {
		requestOverrideAny = t.opts.RequestFormats
	}
	requestFormatsOverride, fmtErr := resolveCallFormatReflect(requestOverrideAny, elem.FieldByName("RequestFormats").Type())
	if fmtErr != nil {
		obs.RecordRequest("ZMQ-REQ", path, 0, time.Since(start))
		return nil, CallError{Err: fmtErr}
	}
	responseOverrideAny := callOpts.ResponseFormats
	if responseOverrideAny == nil {
		responseOverrideAny = t.opts.ResponseFormats
	}
	responseFormatsOverride, fmtErr := resolveCallFormatReflect(responseOverrideAny, elem.FieldByName("Formats").Type())
	if fmtErr != nil {
		obs.RecordRequest("ZMQ-REQ", path, 0, time.Since(start))
		return nil, CallError{Err: fmtErr}
	}

	// EncodeRequestWithFormats honors the per-call/t.opts override
	// (falling back to route-declared RequestFormats, then plain
	// EncodeRequest) — closes Phase 0 work item 1 (client-side).
	encodeResults := rv.MethodByName("EncodeRequestWithFormats").CallSlice([]reflect.Value{reqVal, requestFormatsOverride})
	if errI, _ := encodeResults[1].Interface().(error); errI != nil {
		stats.ReportErrors(obs, "body", errI)
		obs.RecordRequest("ZMQ-REQ", path, 0, time.Since(start))
		return nil, CallError{Err: errI}
	}
	payload, _ := encodeResults[0].Interface().([]byte)

	if err := sock.SendFrames([][]byte{payload}); err != nil {
		obs.RecordRequest("ZMQ-REQ", path, 0, time.Since(start))
		return nil, CallError{Err: fmt.Errorf("send: %w", err)}
	}

	if err := sock.SetRecvTimeout(recvPollInterval); err != nil {
		return nil, CallError{Err: fmt.Errorf("set recv timeout: %w", err)}
	}

	var frames [][]byte
	for {
		select {
		case <-ctx.Done():
			obs.RecordRequest("ZMQ-REQ", path, 0, time.Since(start))
			return nil, CallError{Err: ctx.Err()}
		default:
		}
		var recvErr error
		frames, recvErr = sock.RecvFrames()
		if errors.Is(recvErr, ErrTimeout) {
			continue
		}
		if recvErr != nil {
			obs.RecordRequest("ZMQ-REQ", path, 0, time.Since(start))
			return nil, CallError{Err: fmt.Errorf("recv: %w", recvErr)}
		}
		break
	}

	if len(frames) < 2 {
		obs.RecordRequest("ZMQ-REQ", path, 0, time.Since(start))
		return nil, CallError{Err: fmt.Errorf("malformed reply: expected [status, payload], got %d frame(s)", len(frames))}
	}

	if string(frames[0]) == "error" {
		obs.RecordRequest("ZMQ-REQ", path, 500, time.Since(start))
		return nil, CallError{Err: fmt.Errorf("server error: %s", frames[1])}
	}

	// DecodeResponseWithFormats honors the per-call/t.opts override
	// (falling back to route-declared Formats, then plain
	// DecodeResponse) — closes the response-direction half of Phase 0
	// work item 1 (client-side).
	decodeResults := rv.MethodByName("DecodeResponseWithFormats").CallSlice([]reflect.Value{reflect.ValueOf(frames[1]), responseFormatsOverride})
	if errI, _ := decodeResults[1].Interface().(error); errI != nil {
		stats.ReportErrors(obs, "body", errI)
		obs.RecordRequest("ZMQ-REQ", path, 0, time.Since(start))
		return nil, CallError{Err: fmt.Errorf("decode response: %w", errI)}
	}
	obs.RecordRequest("ZMQ-REQ", path, 200, time.Since(start))
	return decodeResults[0].Interface(), nil
}

// CallAsync implements [reqreply.ClientTransport]. Non-blocking
// counterpart to [clientTransport.Call]: recovers a *reqreply.Future[Resp]
// (as any) via routeAny's [reqreply.FutureFactory], then runs the SAME
// dispatch [clientTransport.Call] does in a background goroutine,
// resolving the future exactly once when it completes. opts is [Call]'s
// identical per-call format-override parameter, applied inside the
// background goroutine.
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

// ── ROUTER/DEALER server ─────────────────────────────────────────────────

// routerServerTransport implements [reqreply.ServerTransport] for ZMQ
// ROUTER sockets, wrapping a topic→socket map — built by
// [AttachRouterServer]. Mirrors [serverTransport]'s reflection technique
// and documented v1 scope; dispatches each request in its own goroutine
// (mirroring [ServeRouter]'s own per-request concurrency), preserving
// the identity frame so the reply reaches the correct DEALER peer.
type routerServerTransport struct {
	sockets map[string]FramedSocket
	opts    ServeOptions
}

// AttachRouterServer binds server+sockets (via an internal
// ServerTransport shim) as server's [reqreply.ServerTransport] for
// ROUTER sockets — the ROUTER/DEALER counterpart of [AttachServer].
// sockets maps each registered route's topic to the ROUTER socket
// handling it.
//
// Returns [MissingSocketError] if server has a registered route whose
// topic has no entry in sockets, checked upfront at Attach time.
// Returns [reqreply.ServerTransportAlreadyAttachedError] if server
// already has a transport attached.
//
//	err := zeromq.AttachRouterServer(server, map[string]zeromq.FramedSocket{"compute/add": routerSock})
func AttachRouterServer(server *reqreply.Server, sockets map[string]FramedSocket, opts ...ServeOptions) error {
	var o ServeOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	for _, topic := range server.RegisteredTopics() {
		if _, ok := sockets[topic]; !ok {
			return MissingSocketError{Topic: topic}
		}
	}
	return server.Attach(&routerServerTransport{sockets: sockets, opts: o})
}

// Serve implements [reqreply.ServerTransport]. Mirrors [ServeRouter]'s
// core receive loop (recv identity+payload → dispatch to its own
// goroutine → decode → call fn → encode → send identity-addressed
// reply) via reflection against routeAny/fnAny.
func (t *routerServerTransport) Serve(ctx context.Context, routeAny any, fnAny any) error {
	rv, elem, err := recoverRouteHandleValue(routeAny)
	if err != nil {
		return err
	}
	path := elem.FieldByName("Topic").String()
	sock, ok := t.sockets[path]
	if !ok {
		return MissingSocketError{Topic: path}
	}

	obs := t.opts.Observer
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}

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
		return reqreply.TransportTypeMismatchError{Topic: path, Want: wantFnType.String(), Got: fmt.Sprintf("%T", fnAny)}
	}

	// decodeWithFormatsMethod/encodeWithFormatsMethod/errorResponseForMethod
	// close Phase 0 work items 1-2 (server-side) — same rationale as
	// [serverTransport.Serve] (no merge-field support: ROUTER frames
	// carry no topic either, routing is entirely socket-based).
	decodeWithFormatsMethod := rv.MethodByName("DecodeWithFormats")
	encodeWithFormatsMethod := rv.MethodByName("EncodeWithFormats")
	errorResponseForMethod := rv.MethodByName("ErrorResponseFor")

	if err := sock.SetRecvTimeout(recvPollInterval); err != nil {
		return SocketError{Op: "set_recv_timeout", Err: err}
	}

	var wg sync.WaitGroup
	for {
		select {
		case <-ctx.Done():
			wg.Wait()
			return nil
		default:
		}
		frames, recvErr := sock.RecvFrames()
		if errors.Is(recvErr, ErrTimeout) {
			continue
		}
		if recvErr != nil {
			select {
			case <-ctx.Done():
				wg.Wait()
				return nil
			default:
				return SocketError{Op: "recv", Err: recvErr}
			}
		}
		if len(frames) < 3 {
			continue // expect [identity, delimiter, payload]
		}
		identity := frames[0]
		payload := frames[2]

		wg.Add(1)
		go func(id, pl []byte) {
			defer wg.Done()
			start := time.Now()

			// TraceObserver span — mirrors [serverTransport.Serve]'s
			// identical addition (span name "zmq.serve"); closes the
			// SAME gap found for the ROUTER variant.
			spanCtx := ctx
			var serveErr error
			if to, ok := obs.(stats.TraceObserver); ok {
				spanCtx = to.StartSpan(ctx, "zmq.serve", path)
			}
			defer func() {
				if to, ok := obs.(stats.TraceObserver); ok {
					to.EndSpan(spanCtx, serveErr)
				}
			}()

			decodeResults := decodeWithFormatsMethod.CallSlice([]reflect.Value{reflect.ValueOf(pl), elem.FieldByName("RequestFormats")})
			if errI, _ := decodeResults[1].Interface().(error); errI != nil {
				stats.ReportErrors(obs, "body", errI)
				serveErr = errI
				obs.RecordRequest("ZMQ-ROUTER", path, 0, time.Since(start))
				sendRouterErrorReply(sock, id, errI)
				if t.opts.OnError != nil {
					t.opts.OnError(ServeError{Kind: KindDecode, Err: errI})
				}
				return
			}
			reqVal := decodeResults[0]

			fnResults := fnVal.Call([]reflect.Value{reflect.ValueOf(spanCtx), reqVal})
			if errI, _ := fnResults[1].Interface().(error); errI != nil {
				serveErr = errI
				obs.RecordRequest("ZMQ-ROUTER", path, 0, time.Since(start))
				sendRouterHandlerErrorReplyReflect(sock, id, errorResponseForMethod, errI, obs)
				if t.opts.OnError != nil {
					t.opts.OnError(ServeError{Kind: KindHandler, Err: errI})
				}
				return
			}
			respVal := fnResults[0]

			encodeResults := encodeWithFormatsMethod.CallSlice([]reflect.Value{respVal, elem.FieldByName("Formats")})
			if errI, _ := encodeResults[1].Interface().(error); errI != nil {
				serveErr = errI
				obs.RecordRequest("ZMQ-ROUTER", path, 0, time.Since(start))
				sendRouterHandlerErrorReplyReflect(sock, id, errorResponseForMethod, errI, obs)
				if t.opts.OnError != nil {
					t.opts.OnError(ServeError{Kind: KindEncode, Err: errI})
				}
				return
			}
			respPayload, _ := encodeResults[0].Interface().([]byte)

			if sendErr := sock.SendFrames([][]byte{id, emptyDelimiter, statusOK, respPayload}); sendErr != nil {
				serveErr = sendErr
				obs.RecordRequest("ZMQ-ROUTER", path, 0, time.Since(start))
				if t.opts.OnError != nil {
					t.opts.OnError(ServeError{Kind: KindEncode, Err: sendErr})
				}
				return
			}
			obs.RecordRequest("ZMQ-ROUTER", path, 200, time.Since(start))
		}(identity, payload)
	}
}

var _ reqreply.ServerTransport = (*routerServerTransport)(nil)

// ── DEALER client ────────────────────────────────────────────────────────

// dealerClientTransport implements [reqreply.ClientTransport] for ZMQ
// DEALER sockets, wrapping a topic→socket map — built by
// [AttachDealerClient]. Mirrors [CallDealer]'s envelope framing (empty
// delimiter + payload).
type dealerClientTransport struct {
	sockets map[string]FramedSocket
	opts    CallOptions
}

// AttachDealerClient binds client+sockets (via an internal
// ClientTransport shim) as client's [reqreply.ClientTransport] for
// DEALER sockets — the ROUTER/DEALER counterpart of [AttachClient].
//
//	client := reqreply.NewClient()
//	_ = zeromq.AttachDealerClient(client, map[string]zeromq.FramedSocket{"compute/add": dealerSock})
func AttachDealerClient(client *reqreply.Client, sockets map[string]FramedSocket, opts ...CallOptions) error {
	var o CallOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	return client.Attach(&dealerClientTransport{sockets: sockets, opts: o})
}

// Call implements [reqreply.ClientTransport]. Mirrors [CallDealer]'s core
// logic (encode → send with empty-delimiter envelope → await reply →
// decode) via reflection against routeAny/reqAny. opts carries a
// per-call format override — see [dealerClientTransport.call].
func (t *dealerClientTransport) Call(ctx context.Context, routeAny any, reqAny any, opts ...reqreply.ClientCallOptions) (any, error) {
	var o reqreply.ClientCallOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	return t.call(ctx, routeAny, reqAny, o)
}

// call's named return values (result, err) let the deferred
// TraceObserver span-end observe the eventual error from EVERY return
// statement — mirrors [clientTransport.call]'s identical pattern.
func (t *dealerClientTransport) call(ctx context.Context, routeAny any, reqAny any, callOpts reqreply.ClientCallOptions) (result any, err error) {
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

	sock, ok := t.sockets[path]
	if !ok {
		obs.RecordRequest("ZMQ-DEALER", path, 0, time.Since(start))
		return nil, MissingSocketError{Topic: path}
	}

	reqType := elem.FieldByName("EncodeRequest").Type().In(0)
	reqVal := reflect.ValueOf(reqAny)
	if !reqVal.IsValid() || reqVal.Type() != reqType {
		obs.RecordRequest("ZMQ-DEALER", path, 0, time.Since(start))
		return nil, reqreply.TransportTypeMismatchError{Topic: path, Want: reqType.String(), Got: fmt.Sprintf("%T", reqAny)}
	}

	// Resolve an OBSERVABILITY-only path override — mirrors
	// [clientTransport.call]'s identical block (see its comment for the
	// full rationale: does NOT affect socket selection, sock is already
	// resolved above; a BuildTopic failure IS FATAL, matching the escape
	// hatch's [CallDealer] exactly).
	if mergeFieldsMethod := rv.MethodByName("MergeFields"); mergeFieldsMethod.Call(nil)[0].Len() > 0 || t.opts.Vars != nil {
		var vars map[string]string
		if mergeFieldsMethod.Call(nil)[0].Len() > 0 {
			encodeVarsResults := rv.MethodByName("EncodeVars").Call([]reflect.Value{reqVal})
			vars, _ = encodeVarsResults[0].Interface().(map[string]string)
		}
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
		buildTopicResults := rv.MethodByName("BuildTopic").Call([]reflect.Value{reflect.ValueOf(vars)})
		if errI, _ := buildTopicResults[1].Interface().(error); errI != nil {
			stats.ReportErrors(obs, "topic_var", errI)
			obs.RecordRequest("ZMQ-DEALER", path, 0, time.Since(start))
			return nil, CallError{Err: errI}
		}
		path = buildTopicResults[0].String()
	}

	// TraceObserver span — mirrors [clientTransport.call]'s identical
	// addition (span name "zmq.request").
	if to, ok := obs.(stats.TraceObserver); ok {
		ctx = to.StartSpan(ctx, "zmq.request", path)
		defer func() { to.EndSpan(ctx, err) }()
	}

	// Resolve per-call format overrides — mirrors [clientTransport.call]'s
	// identical priority chain (callOpts > t.opts > declared).
	requestOverrideAny := callOpts.RequestFormats
	if requestOverrideAny == nil {
		requestOverrideAny = t.opts.RequestFormats
	}
	requestFormatsOverride, fmtErr := resolveCallFormatReflect(requestOverrideAny, elem.FieldByName("RequestFormats").Type())
	if fmtErr != nil {
		obs.RecordRequest("ZMQ-DEALER", path, 0, time.Since(start))
		return nil, CallError{Err: fmtErr}
	}
	responseOverrideAny := callOpts.ResponseFormats
	if responseOverrideAny == nil {
		responseOverrideAny = t.opts.ResponseFormats
	}
	responseFormatsOverride, fmtErr := resolveCallFormatReflect(responseOverrideAny, elem.FieldByName("Formats").Type())
	if fmtErr != nil {
		obs.RecordRequest("ZMQ-DEALER", path, 0, time.Since(start))
		return nil, CallError{Err: fmtErr}
	}

	encodeResults := rv.MethodByName("EncodeRequestWithFormats").CallSlice([]reflect.Value{reqVal, requestFormatsOverride})
	if errI, _ := encodeResults[1].Interface().(error); errI != nil {
		stats.ReportErrors(obs, "body", errI)
		obs.RecordRequest("ZMQ-DEALER", path, 0, time.Since(start))
		return nil, CallError{Err: errI}
	}
	payload, _ := encodeResults[0].Interface().([]byte)

	if err := sock.SendFrames([][]byte{emptyDelimiter, payload}); err != nil {
		obs.RecordRequest("ZMQ-DEALER", path, 0, time.Since(start))
		return nil, CallError{Err: fmt.Errorf("send: %w", err)}
	}

	if err := sock.SetRecvTimeout(recvPollInterval); err != nil {
		return nil, CallError{Err: fmt.Errorf("set recv timeout: %w", err)}
	}

	var frames [][]byte
	for {
		select {
		case <-ctx.Done():
			obs.RecordRequest("ZMQ-DEALER", path, 0, time.Since(start))
			return nil, CallError{Err: ctx.Err()}
		default:
		}
		var recvErr error
		frames, recvErr = sock.RecvFrames()
		if errors.Is(recvErr, ErrTimeout) {
			continue
		}
		if recvErr != nil {
			obs.RecordRequest("ZMQ-DEALER", path, 0, time.Since(start))
			return nil, CallError{Err: fmt.Errorf("recv: %w", recvErr)}
		}
		break
	}

	// Expect [delimiter, status, payload].
	if len(frames) < 3 {
		obs.RecordRequest("ZMQ-DEALER", path, 0, time.Since(start))
		return nil, CallError{Err: fmt.Errorf("malformed reply: expected [delimiter, status, payload], got %d frame(s)", len(frames))}
	}

	if string(frames[1]) == "error" {
		obs.RecordRequest("ZMQ-DEALER", path, 500, time.Since(start))
		return nil, CallError{Err: fmt.Errorf("server error: %s", frames[2])}
	}

	decodeResults := rv.MethodByName("DecodeResponseWithFormats").CallSlice([]reflect.Value{reflect.ValueOf(frames[2]), responseFormatsOverride})
	if errI, _ := decodeResults[1].Interface().(error); errI != nil {
		stats.ReportErrors(obs, "body", errI)
		obs.RecordRequest("ZMQ-DEALER", path, 0, time.Since(start))
		return nil, CallError{Err: fmt.Errorf("decode response: %w", errI)}
	}
	obs.RecordRequest("ZMQ-DEALER", path, 200, time.Since(start))
	return decodeResults[0].Interface(), nil
}

// CallAsync implements [reqreply.ClientTransport]. Non-blocking
// counterpart to [dealerClientTransport.Call] — same mechanism as
// [clientTransport.CallAsync]. opts is [Call]'s identical per-call
// format-override parameter, applied inside the background goroutine.
func (t *dealerClientTransport) CallAsync(ctx context.Context, routeAny any, reqAny any, opts ...reqreply.ClientCallOptions) (any, error) {
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

var _ reqreply.ClientTransport = (*dealerClientTransport)(nil)
