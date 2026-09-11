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
	_, elem, err := recoverRouteHandleValue(routeAny)
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

		decodeResults := decodeField.Call([]reflect.Value{reflect.ValueOf(payload)})
		if errI, _ := decodeResults[1].Interface().(error); errI != nil {
			stats.ReportErrors(obs, "body", errI)
			obs.RecordRequest("ZMQ-REP", path, 0, time.Since(start))
			sendErrorReply(sock, errI)
			if t.opts.OnError != nil {
				t.opts.OnError(ServeError{Kind: KindDecode, Err: errI})
			}
			continue
		}
		reqVal := decodeResults[0]

		fnResults := fnVal.Call([]reflect.Value{reflect.ValueOf(ctx), reqVal})
		if errI, _ := fnResults[1].Interface().(error); errI != nil {
			obs.RecordRequest("ZMQ-REP", path, 0, time.Since(start))
			sendErrorReply(sock, errI)
			if t.opts.OnError != nil {
				t.opts.OnError(ServeError{Kind: KindHandler, Err: errI})
			}
			continue
		}
		respVal := fnResults[0]

		encodeResults := encodeField.Call([]reflect.Value{respVal})
		if errI, _ := encodeResults[1].Interface().(error); errI != nil {
			obs.RecordRequest("ZMQ-REP", path, 0, time.Since(start))
			sendErrorReply(sock, errI)
			if t.opts.OnError != nil {
				t.opts.OnError(ServeError{Kind: KindEncode, Err: errI})
			}
			continue
		}
		respPayload, _ := encodeResults[0].Interface().([]byte)

		if sendErr := sock.SendFrames([][]byte{statusOK, respPayload}); sendErr != nil {
			obs.RecordRequest("ZMQ-REP", path, 0, time.Since(start))
			if t.opts.OnError != nil {
				t.opts.OnError(ServeError{Kind: KindEncode, Err: sendErr})
			}
			continue
		}
		obs.RecordRequest("ZMQ-REP", path, 200, time.Since(start))
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

	sock, ok := t.sockets[path]
	if !ok {
		obs.RecordRequest("ZMQ-REQ", path, 0, time.Since(start))
		return nil, MissingSocketError{Topic: path}
	}

	encodeRequestField := elem.FieldByName("EncodeRequest")   // func(Req) ([]byte, error)
	decodeResponseField := elem.FieldByName("DecodeResponse") // func([]byte) (Resp, error)
	reqType := encodeRequestField.Type().In(0)

	reqVal := reflect.ValueOf(reqAny)
	if !reqVal.IsValid() || reqVal.Type() != reqType {
		obs.RecordRequest("ZMQ-REQ", path, 0, time.Since(start))
		return nil, reqreply.TransportTypeMismatchError{Topic: path, Want: reqType.String(), Got: fmt.Sprintf("%T", reqAny)}
	}

	encodeResults := encodeRequestField.Call([]reflect.Value{reqVal})
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

	decodeResults := decodeResponseField.Call([]reflect.Value{reflect.ValueOf(frames[1])})
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
// resolving the future exactly once when it completes.
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
	_, elem, err := recoverRouteHandleValue(routeAny)
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

			decodeResults := decodeField.Call([]reflect.Value{reflect.ValueOf(pl)})
			if errI, _ := decodeResults[1].Interface().(error); errI != nil {
				stats.ReportErrors(obs, "body", errI)
				obs.RecordRequest("ZMQ-ROUTER", path, 0, time.Since(start))
				sendRouterErrorReply(sock, id, errI)
				if t.opts.OnError != nil {
					t.opts.OnError(ServeError{Kind: KindDecode, Err: errI})
				}
				return
			}
			reqVal := decodeResults[0]

			fnResults := fnVal.Call([]reflect.Value{reflect.ValueOf(ctx), reqVal})
			if errI, _ := fnResults[1].Interface().(error); errI != nil {
				obs.RecordRequest("ZMQ-ROUTER", path, 0, time.Since(start))
				sendRouterErrorReply(sock, id, errI)
				if t.opts.OnError != nil {
					t.opts.OnError(ServeError{Kind: KindHandler, Err: errI})
				}
				return
			}
			respVal := fnResults[0]

			encodeResults := encodeField.Call([]reflect.Value{respVal})
			if errI, _ := encodeResults[1].Interface().(error); errI != nil {
				obs.RecordRequest("ZMQ-ROUTER", path, 0, time.Since(start))
				sendRouterErrorReply(sock, id, errI)
				if t.opts.OnError != nil {
					t.opts.OnError(ServeError{Kind: KindEncode, Err: errI})
				}
				return
			}
			respPayload, _ := encodeResults[0].Interface().([]byte)

			if sendErr := sock.SendFrames([][]byte{id, emptyDelimiter, statusOK, respPayload}); sendErr != nil {
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
// decode) via reflection against routeAny/reqAny.
func (t *dealerClientTransport) Call(ctx context.Context, routeAny any, reqAny any) (any, error) {
	return t.call(ctx, routeAny, reqAny)
}

func (t *dealerClientTransport) call(ctx context.Context, routeAny any, reqAny any) (any, error) {
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

	sock, ok := t.sockets[path]
	if !ok {
		obs.RecordRequest("ZMQ-DEALER", path, 0, time.Since(start))
		return nil, MissingSocketError{Topic: path}
	}

	encodeRequestField := elem.FieldByName("EncodeRequest")
	decodeResponseField := elem.FieldByName("DecodeResponse")
	reqType := encodeRequestField.Type().In(0)

	reqVal := reflect.ValueOf(reqAny)
	if !reqVal.IsValid() || reqVal.Type() != reqType {
		obs.RecordRequest("ZMQ-DEALER", path, 0, time.Since(start))
		return nil, reqreply.TransportTypeMismatchError{Topic: path, Want: reqType.String(), Got: fmt.Sprintf("%T", reqAny)}
	}

	encodeResults := encodeRequestField.Call([]reflect.Value{reqVal})
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

	decodeResults := decodeResponseField.Call([]reflect.Value{reflect.ValueOf(frames[2])})
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
// [clientTransport.CallAsync].
func (t *dealerClientTransport) CallAsync(ctx context.Context, routeAny any, reqAny any) (any, error) {
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

var _ reqreply.ClientTransport = (*dealerClientTransport)(nil)
