package zeromq

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/stats"
)

// recvPollInterval is the receive timeout used by blocking loops so that
// context cancellation is checked at least this often.
const recvPollInterval = 100 * time.Millisecond

// statusOK and statusError are the status frame values for REQ/REP framing.
var (
	statusOK    = []byte("ok")
	statusError = []byte("error")
)

// SubscribeOptions configures [Subscribe].
type SubscribeOptions struct {
	// OnError, when non-nil, is called with a typed [SubscribeError] on decode
	// or application handler failure. If nil, errors are silently discarded.
	OnError func(SubscribeError)

	// Observer, when non-nil, receives per-message lifecycle events:
	// [stats.Observer.RecordSubscribe] is called with success=true on clean
	// handler completion and success=false on any failure. Per-field payload
	// validation errors are reported via [stats.Observer.RecordValidationError]
	// with location "payload".
	// Defaults to [stats.NoopObserver] when nil.
	Observer stats.Observer
}

// PublishOptions configures [Publish].
type PublishOptions struct {
	// Observer, when non-nil, receives per-publish lifecycle events:
	// [stats.Observer.RecordPublish] is called with success=true on broker send
	// and success=false on encode failure or send error. Per-field payload encode
	// errors are reported via [stats.Observer.RecordValidationError] with location
	// "payload". Topic variable errors are reported with location "topic_var".
	// Defaults to [stats.NoopObserver] when nil.
	Observer stats.Observer
}

// ServeOptions configures [Serve].
type ServeOptions struct {
	// OnError, when non-nil, is called with a typed [ServeError] on decode,
	// handler, or encode failure. The REP socket always sends an error reply
	// frame to avoid leaving the REQ peer stuck; OnError is informational.
	// If nil, errors are silently discarded (the error reply is still sent).
	OnError func(ServeError)

	// Observer, when non-nil, receives per-request lifecycle events:
	// [stats.Observer.RecordRequest] is called with method "ZMQ-REP", the
	// route path, status 200 on success, and status 0 on failure.
	// Per-field validation errors are reported with location "body".
	// Defaults to [stats.NoopObserver] when nil.
	Observer stats.Observer
}

// CallOptions configures [Call] and [CallDealer].
type CallOptions struct {
	// Observer, when non-nil, receives per-call lifecycle events:
	// [stats.Observer.RecordRequest] is called with method "ZMQ-REQ" or
	// "ZMQ-DEALER", the route path, status 200 on success, and status 0 or
	// 500 on failure. Per-field decode errors are reported with location "body".
	// Topic variable errors are reported with location "topic_var".
	// Defaults to [stats.NoopObserver] when nil.
	Observer stats.Observer

	// Vars, when non-nil, substitutes {varName} placeholders in the route topic
	// template before encoding. Uses [reqreply.RouteHandle.BuildTopic] to
	// resolve and codec-validate each variable.
	//
	// In ZMQ REQ/REP, the resolved topic is used for observer reporting only —
	// the actual routing is socket-based. Validation still runs on each variable.
	//
	// Example — template topic "compute/{tenantID}/add":
	//
	//	zeromq.Call(ctx, sock, handle, req,
	//	    zeromq.CallOptions{Vars: map[string]string{"tenantID": "acme"}})
	//
	// Returns [CallError] wrapping [reqreply.RouteParamError] or
	// [reqreply.MissingRouteParamError] on validation failure.
	Vars map[string]string
}

// Subscribe blocks and processes incoming messages from a SUB or PULL socket.
// Each message is decoded using handle's codec and delivered to fn.
//
// For SUB sockets, Subscribe registers handle.Topic as the ZMQ subscription
// prefix filter before entering the receive loop. For PULL sockets the filter
// is a no-op — call [FramedSocket.SetSubscription]("") separately if needed.
//
// Messages are expected in [topic, payload] frame format. The topic frame is
// used for observer reporting; the payload frame is decoded by the codec.
//
// The loop runs until ctx is cancelled (returns nil) or a socket error occurs
// (returns the error). Run Subscribe in a dedicated goroutine.
//
// The optional formats parameter overrides the channel handle's default JSON
// codec. Priority: call-time formats > handle.SubscribeFormats > handle.Formats
// > handle.Decode (JSON fallback).
//
// Example (PUB/SUB sensor readings):
//
//	go func() {
//	    if err := zeromq.Subscribe(ctx, sock, readingsHandle, func(ctx context.Context, r SensorReading) error {
//	        return store.Save(ctx, r)
//	    }, zeromq.SubscribeOptions{Observer: obs}); err != nil {
//	        log.Error("subscribe stopped", "err", err)
//	    }
//	}()
func Subscribe[T any](
	ctx context.Context,
	sock FramedSocket,
	handle *events.ChannelHandle[T],
	fn func(context.Context, T) error,
	opts SubscribeOptions,
	formats ...format.Format[T],
) error {
	obs := opts.Observer
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}
	if err := sock.SetSubscription(handle.Topic); err != nil {
		return SocketError{Op: "set_subscription", Err: err}
	}
	if err := sock.SetRecvTimeout(recvPollInterval); err != nil {
		return SocketError{Op: "set_recv_timeout", Err: err}
	}
	effectiveFmts := formats
	if len(effectiveFmts) == 0 {
		effectiveFmts = handle.SubscribeFormats
	}
	if len(effectiveFmts) == 0 {
		effectiveFmts = handle.Formats
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		frames, err := sock.RecvFrames()
		if errors.Is(err, ErrTimeout) {
			continue
		}
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				return SocketError{Op: "recv", Err: err}
			}
		}
		if len(frames) < 2 {
			continue // malformed: expect [topic, payload]
		}
		topic := string(frames[0])
		payload := frames[1]
		start := time.Now()

		var value T
		var decErr error
		if len(effectiveFmts) > 0 {
			value, decErr = effectiveFmts[0].Unmarshal(payload)
		} else {
			value, decErr = handle.Decode(payload)
		}
		if decErr != nil {
			stats.ReportErrors(obs, "payload", decErr)
			obs.RecordSubscribe(topic, false, time.Since(start))
			if opts.OnError != nil {
				opts.OnError(SubscribeError{Kind: KindDecode, Topic: topic, Err: decErr})
			}
			continue
		}

		// Merge topic variables declared via events.NewTopicParam into the
		// SAME decoded value — additive, only runs when the channel has
		// merge-capable topic params (backward compatible: identical
		// behavior to today when none are declared). Mirrors mqtt5's
		// makeSubscribeMessageHandler wiring.
		if mergeFields := handle.MergeFields(); len(mergeFields) > 0 {
			vars, varErr := TopicVarsFromMessage(handle, topic)
			if varErr != nil {
				stats.ReportErrors(obs, "topic_var", varErr)
				obs.RecordSubscribe(topic, false, time.Since(start))
				if opts.OnError != nil {
					opts.OnError(SubscribeError{Kind: KindDecode, Topic: topic, Err: varErr})
				}
				continue
			}
			if mergeErr := codex.DecodeVars(&value, vars, mergeFields...); mergeErr != nil {
				stats.ReportErrors(obs, "topic_var", mergeErr)
				obs.RecordSubscribe(topic, false, time.Since(start))
				if opts.OnError != nil {
					opts.OnError(SubscribeError{Kind: KindDecode, Topic: topic, Err: mergeErr})
				}
				continue
			}
		}

		var spanCtx = ctx
		if to, ok := obs.(stats.TraceObserver); ok {
			spanCtx = to.StartSpan(ctx, "zmq.subscribe", topic)
		}
		fnErr := fn(spanCtx, value)
		if to, ok := obs.(stats.TraceObserver); ok {
			to.EndSpan(spanCtx, fnErr)
		}
		if fnErr != nil {
			obs.RecordSubscribe(topic, false, time.Since(start))
			if opts.OnError != nil {
				opts.OnError(SubscribeError{Kind: KindHandler, Topic: topic, Err: fnErr})
			}
			continue
		}
		obs.RecordSubscribe(topic, true, time.Since(start))
	}
}

// Publish encodes msg using handle's codec and sends it to a PUB or PUSH socket.
//
// The message is framed as [topic, payload]:
//   - topic: the resolved topic string (after BuildTopic if vars are provided)
//   - payload: the codec-encoded message bytes
//
// For PUB sockets the topic frame is used for ZMQ prefix-filter matching.
// For PUSH sockets the topic frame is sent but ignored by PULL receivers.
//
// vars controls topic resolution:
//   - nil: use handle.Topic directly (static topics).
//   - non-nil: call handle.BuildTopic(vars) to resolve a template topic.
//
// The optional formats parameter overrides the channel handle's default JSON
// codec. Priority: call-time formats > handle.PublishFormats > handle.Formats
// > handle.Encode (JSON fallback).
func Publish[T any](
	ctx context.Context,
	sock FramedSocket,
	handle *events.ChannelHandle[T],
	msg T,
	vars map[string]string,
	opts PublishOptions,
	formats ...format.Format[T],
) error {
	obs := opts.Observer
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}
	start := time.Now()
	var err error
	if to, ok := obs.(stats.TraceObserver); ok {
		ctx = to.StartSpan(ctx, "zmq.publish", handle.Topic)
		defer func() { to.EndSpan(ctx, err) }()
	}

	topic := handle.Topic
	if vars != nil {
		var buildErr error
		topic, buildErr = handle.BuildTopic(vars)
		if buildErr != nil {
			err = buildErr
			stats.ReportErrors(obs, "topic_var", buildErr)
			obs.RecordPublish(handle.Topic, false, time.Since(start))
			return err
		}
	}

	effectiveFmts := formats
	if len(effectiveFmts) == 0 {
		effectiveFmts = handle.PublishFormats
	}
	if len(effectiveFmts) == 0 {
		effectiveFmts = handle.Formats
	}
	var payload []byte
	if len(effectiveFmts) > 0 {
		payload, err = effectiveFmts[0].Marshal(msg)
	} else {
		payload, err = handle.Encode(msg)
	}
	if err != nil {
		stats.ReportErrors(obs, "payload", err)
		obs.RecordPublish(topic, false, time.Since(start))
		return PublishEncodeError{Topic: topic, Err: err}
	}

	if err = sock.SendFrames([][]byte{[]byte(topic), payload}); err != nil {
		obs.RecordPublish(topic, false, time.Since(start))
		return SocketError{Op: "send", Err: err}
	}
	obs.RecordPublish(topic, true, time.Since(start))
	return nil
}

// PublishHandle is the single-call convenience wrapper around [Publish]: it
// derives the topic vars map from msg automatically, using the channel's
// merge-capable topic params ([events.ChannelHandle.MergeFields] +
// [codex.EncodeVars]) — one struct in, no manual vars map, mirroring
// [mqtt5.PublishHandle]'s convenience for MQTT 5 events.
//
// [Publish] remains available as the lower-level escape hatch for callers
// that build the vars map themselves (e.g. no merge fields declared, or
// vars come from a non-struct source).
//
//	err := zeromq.PublishHandle(ctx, sock, sensorChannel, reading, zeromq.PublishOptions{})
func PublishHandle[T any](
	ctx context.Context,
	sock FramedSocket,
	handle *events.ChannelHandle[T],
	msg T,
	opts PublishOptions,
	formats ...format.Format[T],
) error {
	vars, err := codex.EncodeVars(msg, handle.MergeFields()...)
	if err != nil {
		return err
	}
	if len(vars) == 0 {
		vars = nil
	}
	return Publish(ctx, sock, handle, msg, vars, opts, formats...)
}

// Serve runs a blocking REP loop: receives requests, calls fn, sends replies.
// It is the server side of a ZMQ REQ/REP contract.
//
// Message framing:
//   - Incoming request: [payload]
//   - Reply on success: ["ok", encoded_response]
//   - Reply on failure: ["error", error_message]
//
// The REP socket always sends a reply (even on error) to avoid leaving the
// REQ peer blocked. Per-error details are delivered via [ServeOptions.OnError].
//
// The loop runs until ctx is cancelled (returns nil) or a socket error occurs.
// Run Serve in a dedicated goroutine.
//
// Format overrides are applied via [reqreply.RouteHandle.WithRequestFormats] and
// [reqreply.RouteHandle.WithFormats] on the handle before calling Serve.
//
// Example (REP compute server):
//
//	go func() {
//	    if err := zeromq.Serve(ctx, sock, computeHandle, handler, zeromq.ServeOptions{Observer: obs}); err != nil {
//	        log.Error("serve stopped", "err", err)
//	    }
//	}()
func Serve[Req, Resp any](
	ctx context.Context,
	sock FramedSocket,
	handle *reqreply.RouteHandle[Req, Resp],
	fn func(context.Context, Req) (Resp, error),
	opts ServeOptions,
) error {
	obs := opts.Observer
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}
	if err := sock.SetRecvTimeout(recvPollInterval); err != nil {
		return SocketError{Op: "set_recv_timeout", Err: err}
	}
	path := handle.Topic
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		frames, err := sock.RecvFrames()
		if errors.Is(err, ErrTimeout) {
			continue
		}
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				return SocketError{Op: "recv", Err: err}
			}
		}
		if len(frames) == 0 {
			continue
		}
		start := time.Now()
		serveRequest(ctx, sock, handle, fn, obs, opts, path, frames[0], start)
	}
}

// serveRequest handles one REQ/REP exchange. Extracted from the Serve loop to
// allow explicit (non-deferred) span management within a loop body.
func serveRequest[Req, Resp any](
	ctx context.Context,
	sock FramedSocket,
	handle *reqreply.RouteHandle[Req, Resp],
	fn func(context.Context, Req) (Resp, error),
	obs stats.Observer,
	opts ServeOptions,
	path string,
	payload []byte,
	start time.Time,
) {
	var spanCtx = ctx
	var serveErr error
	if to, ok := obs.(stats.TraceObserver); ok {
		spanCtx = to.StartSpan(ctx, "zmq.serve", path)
	}
	defer func() {
		if to, ok := obs.(stats.TraceObserver); ok {
			to.EndSpan(spanCtx, serveErr)
		}
	}()

	// decode request
	var req Req
	if len(handle.RequestFormats) > 0 {
		req, serveErr = handle.RequestFormats[0].Unmarshal(payload)
	} else {
		req, serveErr = handle.Decode(payload)
	}
	if serveErr != nil {
		stats.ReportErrors(obs, "body", serveErr)
		obs.RecordRequest("ZMQ-REP", path, 0, time.Since(start))
		sendErrorReply(sock, serveErr)
		if opts.OnError != nil {
			opts.OnError(ServeError{Kind: KindDecode, Err: serveErr})
		}
		return
	}

	// call handler
	var resp Resp
	resp, serveErr = fn(spanCtx, req)
	if serveErr != nil {
		obs.RecordRequest("ZMQ-REP", path, 0, time.Since(start))
		sendErrorReply(sock, serveErr)
		if opts.OnError != nil {
			opts.OnError(ServeError{Kind: KindHandler, Err: serveErr})
		}
		return
	}

	// encode response
	var respPayload []byte
	if len(handle.Formats) > 0 {
		respPayload, serveErr = handle.Formats[0].Marshal(resp)
	} else {
		respPayload, serveErr = handle.Encode(resp)
	}
	if serveErr != nil {
		obs.RecordRequest("ZMQ-REP", path, 0, time.Since(start))
		sendErrorReply(sock, serveErr)
		if opts.OnError != nil {
			opts.OnError(ServeError{Kind: KindEncode, Err: serveErr})
		}
		return
	}

	if sendErr := sock.SendFrames([][]byte{statusOK, respPayload}); sendErr != nil {
		serveErr = sendErr
		obs.RecordRequest("ZMQ-REP", path, 0, time.Since(start))
		if opts.OnError != nil {
			opts.OnError(ServeError{Kind: KindEncode, Err: sendErr})
		}
		return
	}
	obs.RecordRequest("ZMQ-REP", path, 200, time.Since(start))
}

// Call encodes req, sends it to a REQ socket, and decodes the reply.
// It is the client side of a ZMQ REQ/REP contract.
//
// Message framing:
//   - Outgoing request:  [payload]
//   - Expected reply OK: ["ok", encoded_response]
//   - Server error reply:["error", message] → returns [CallError]
//
// ctx cancellation is honoured during the reply receive loop. Call blocks
// until a reply arrives, ctx is cancelled, or a socket error occurs.
//
// Format overrides are applied via [reqreply.RouteHandle.WithRequestFormats] and
// [reqreply.RouteHandle.WithFormats] on the handle before calling Call.
//
// Example (REQ compute client):
//
//	result, err := zeromq.Call(ctx, sock, computeHandle.ClientHandle(), req,
//	    zeromq.CallOptions{Observer: obs})
func Call[Req, Resp any](
	ctx context.Context,
	sock FramedSocket,
	handle *reqreply.RouteHandle[Req, Resp],
	req Req,
	opts CallOptions,
) (Resp, error) {
	obs := opts.Observer
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}
	var zero Resp
	start := time.Now()
	path := handle.Topic
	var callErr error

	// Resolve template topic vars if provided.
	if opts.Vars != nil {
		var buildErr error
		path, buildErr = handle.BuildTopic(opts.Vars)
		if buildErr != nil {
			stats.ReportErrors(obs, "topic_var", buildErr)
			obs.RecordRequest("ZMQ-REQ", handle.Topic, 0, time.Since(start))
			callErr = CallError{Err: buildErr}
			return zero, callErr
		}
	}

	if to, ok := obs.(stats.TraceObserver); ok {
		ctx = to.StartSpan(ctx, "zmq.request", path)
		defer func() { to.EndSpan(ctx, callErr) }()
	}

	// encode request
	var payload []byte
	if len(handle.RequestFormats) > 0 {
		payload, callErr = handle.RequestFormats[0].Marshal(req)
	} else {
		payload, callErr = handle.EncodeRequest(req)
	}
	if callErr != nil {
		stats.ReportErrors(obs, "body", callErr)
		obs.RecordRequest("ZMQ-REQ", path, 0, time.Since(start))
		callErr = CallError{Err: callErr}
		return zero, callErr
	}

	// send
	if err := sock.SendFrames([][]byte{payload}); err != nil {
		callErr = CallError{Err: fmt.Errorf("send: %w", err)}
		obs.RecordRequest("ZMQ-REQ", path, 0, time.Since(start))
		return zero, callErr
	}

	// configure recv timeout for ctx-cancellation polling
	if err := sock.SetRecvTimeout(recvPollInterval); err != nil {
		callErr = CallError{Err: fmt.Errorf("set recv timeout: %w", err)}
		return zero, callErr
	}

	// receive reply
	var frames [][]byte
	for {
		select {
		case <-ctx.Done():
			callErr = CallError{Err: ctx.Err()}
			obs.RecordRequest("ZMQ-REQ", path, 0, time.Since(start))
			return zero, callErr
		default:
		}
		var recvErr error
		frames, recvErr = sock.RecvFrames()
		if errors.Is(recvErr, ErrTimeout) {
			continue
		}
		if recvErr != nil {
			callErr = CallError{Err: fmt.Errorf("recv: %w", recvErr)}
			obs.RecordRequest("ZMQ-REQ", path, 0, time.Since(start))
			return zero, callErr
		}
		break
	}

	// validate framing
	if len(frames) < 2 {
		callErr = CallError{Err: fmt.Errorf("malformed reply: expected [status, payload], got %d frame(s)", len(frames))}
		obs.RecordRequest("ZMQ-REQ", path, 0, time.Since(start))
		return zero, callErr
	}

	// check status
	if string(frames[0]) == "error" {
		callErr = CallError{Err: fmt.Errorf("server error: %s", frames[1])}
		obs.RecordRequest("ZMQ-REQ", path, 500, time.Since(start))
		return zero, callErr
	}

	// decode response
	var resp Resp
	if len(handle.Formats) > 0 {
		resp, callErr = handle.Formats[0].Unmarshal(frames[1])
	} else {
		resp, callErr = handle.DecodeResponse(frames[1])
	}
	if callErr != nil {
		stats.ReportErrors(obs, "body", callErr)
		callErr = CallError{Err: fmt.Errorf("decode response: %w", callErr)}
		obs.RecordRequest("ZMQ-REQ", path, 0, time.Since(start))
		return zero, callErr
	}
	obs.RecordRequest("ZMQ-REQ", path, 200, time.Since(start))
	return resp, nil
}

// CallHandle is the single-call convenience wrapper around [Call]: it
// derives [CallOptions.Vars] from req automatically, using the route's
// merge-capable topic params ([reqreply.RouteHandle.MergeFields] +
// [codex.EncodeVars]) — mirrors [nethttp.CallHandle]/[mqtt5.CallHandle].
//
// An explicit [CallOptions.Vars] takes PRECEDENCE over the derived value.
// [Call] remains the lower-level escape hatch.
//
// Note: this convenience is CLIENT-SIDE only. ZMQ REQ/REP routing is
// socket-based, not topic-based — [Serve]'s incoming messages carry no
// per-message topic string to extract vars FROM (unlike MQTT's
// broker-routed topics), so there is no server-side decode-merge
// equivalent for zeromq. The resolved topic here is used only for codec
// validation and observer reporting, matching [CallOptions.Vars]'s
// existing documented behavior.
//
//	resp, err := zeromq.CallHandle(ctx, sock, computeRoute, req, zeromq.CallOptions{})
func CallHandle[Req, Resp any](
	ctx context.Context,
	sock FramedSocket,
	handle *reqreply.RouteHandle[Req, Resp],
	req Req,
	opts CallOptions,
) (Resp, error) {
	var zero Resp
	derived, err := codex.EncodeVars(req, handle.MergeFields()...)
	if err != nil {
		return zero, err
	}
	if len(derived) == 0 {
		derived = nil
	}
	if opts.Vars != nil {
		merged := make(map[string]string, len(derived)+len(opts.Vars))
		for k, v := range derived {
			merged[k] = v
		}
		for k, v := range opts.Vars {
			merged[k] = v
		}
		opts.Vars = merged
	} else {
		opts.Vars = derived
	}
	return Call(ctx, sock, handle, req, opts)
}

// sendErrorReply sends an error reply frame to the REQ peer.
// Always called in the Serve loop on handler, decode, or encode failures
// to prevent the REQ socket from blocking indefinitely.
func sendErrorReply(sock FramedSocket, err error) {
	_ = sock.SendFrames([][]byte{statusError, []byte(err.Error())})
}

// emptyDelimiter is the empty frame separating the identity stack from the
// payload in DEALER/ROUTER ZMQ envelope format.
var emptyDelimiter = []byte{}

// ServeRouter runs a blocking ROUTER loop. Each incoming request is dispatched
// concurrently in its own goroutine. Identity frames are extracted automatically
// and re-prepended to every reply so the DEALER peer can correlate responses.
//
// ROUTER message framing (server receives):
//
//	[identity, "", payload]
//
// Reply framing (server sends):
//
//	[identity, "", "ok", encoded_response]
//	[identity, "", "error", error_message]
//
// The loop runs until ctx is cancelled; it waits for all in-flight goroutines
// to drain before returning nil. A socket recv error stops the loop immediately.
//
// Errors are delivered via [ServeOptions.OnError] using the same [ServeError]
// type as [Serve]. Format overrides are applied via [reqreply.RouteHandle.WithRequestFormats]
// and [reqreply.RouteHandle.WithFormats] before calling ServeRouter.
//
// Example (ROUTER compute server):
//
//	go func() {
//	    if err := zeromq.ServeRouter(ctx, sock, handle, handler,
//	        zeromq.ServeOptions{Observer: obs}); err != nil {
//	        log.Error("serve stopped", "err", err)
//	    }
//	}()
func ServeRouter[Req, Resp any](
	ctx context.Context,
	sock FramedSocket,
	handle *reqreply.RouteHandle[Req, Resp],
	fn func(context.Context, Req) (Resp, error),
	opts ServeOptions,
) error {
	obs := opts.Observer
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}
	if err := sock.SetRecvTimeout(recvPollInterval); err != nil {
		return SocketError{Op: "set_recv_timeout", Err: err}
	}
	path := handle.Topic

	var wg sync.WaitGroup
	for {
		select {
		case <-ctx.Done():
			wg.Wait()
			return nil
		default:
		}
		frames, err := sock.RecvFrames()
		if errors.Is(err, ErrTimeout) {
			continue
		}
		if err != nil {
			select {
			case <-ctx.Done():
				wg.Wait()
				return nil
			default:
				return SocketError{Op: "recv", Err: err}
			}
		}
		// Expect at least [identity, delimiter, payload].
		if len(frames) < 3 {
			continue
		}
		identity := frames[0]
		// frames[1] = empty delimiter
		payload := frames[2]

		wg.Add(1)
		go func(id, pl []byte) {
			defer wg.Done()
			start := time.Now()
			serveRouterRequest(ctx, sock, handle, fn, obs, opts, path, id, pl, start)
		}(identity, payload)
	}
}

// serveRouterRequest handles one ROUTER request in its own goroutine.
func serveRouterRequest[Req, Resp any](
	ctx context.Context,
	sock FramedSocket,
	handle *reqreply.RouteHandle[Req, Resp],
	fn func(context.Context, Req) (Resp, error),
	obs stats.Observer,
	opts ServeOptions,
	path string,
	identity []byte,
	payload []byte,
	start time.Time,
) {
	var spanCtx = ctx
	var serveErr error
	if to, ok := obs.(stats.TraceObserver); ok {
		spanCtx = to.StartSpan(ctx, "zmq.serve", path)
	}
	defer func() {
		if to, ok := obs.(stats.TraceObserver); ok {
			to.EndSpan(spanCtx, serveErr)
		}
	}()

	// decode request
	var req Req
	if len(handle.RequestFormats) > 0 {
		req, serveErr = handle.RequestFormats[0].Unmarshal(payload)
	} else {
		req, serveErr = handle.Decode(payload)
	}
	if serveErr != nil {
		stats.ReportErrors(obs, "body", serveErr)
		obs.RecordRequest("ZMQ-ROUTER", path, 0, time.Since(start))
		sendRouterErrorReply(sock, identity, serveErr)
		if opts.OnError != nil {
			opts.OnError(ServeError{Kind: KindDecode, Err: serveErr})
		}
		return
	}

	// call handler
	var resp Resp
	resp, serveErr = fn(spanCtx, req)
	if serveErr != nil {
		obs.RecordRequest("ZMQ-ROUTER", path, 0, time.Since(start))
		sendRouterErrorReply(sock, identity, serveErr)
		if opts.OnError != nil {
			opts.OnError(ServeError{Kind: KindHandler, Err: serveErr})
		}
		return
	}

	// encode response
	var respPayload []byte
	if len(handle.Formats) > 0 {
		respPayload, serveErr = handle.Formats[0].Marshal(resp)
	} else {
		respPayload, serveErr = handle.Encode(resp)
	}
	if serveErr != nil {
		obs.RecordRequest("ZMQ-ROUTER", path, 0, time.Since(start))
		sendRouterErrorReply(sock, identity, serveErr)
		if opts.OnError != nil {
			opts.OnError(ServeError{Kind: KindEncode, Err: serveErr})
		}
		return
	}

	if sendErr := sock.SendFrames([][]byte{identity, emptyDelimiter, statusOK, respPayload}); sendErr != nil {
		serveErr = sendErr
		obs.RecordRequest("ZMQ-ROUTER", path, 0, time.Since(start))
		if opts.OnError != nil {
			opts.OnError(ServeError{Kind: KindEncode, Err: sendErr})
		}
		return
	}
	obs.RecordRequest("ZMQ-ROUTER", path, 200, time.Since(start))
}

// sendRouterErrorReply sends an error reply to a ROUTER peer, preserving identity frames.
func sendRouterErrorReply(sock FramedSocket, identity []byte, err error) {
	_ = sock.SendFrames([][]byte{identity, emptyDelimiter, statusError, []byte(err.Error())})
}

// CallDealer encodes req and sends it via a DEALER socket using the ZMQ envelope
// format (empty delimiter + payload), then synchronously waits for one reply.
//
// DEALER message framing (client sends):
//
//	["", payload]
//
// Expected reply framing (client receives):
//
//	["", "ok", encoded_response]
//	["", "error", error_message]
//
// For concurrent use, call CallDealer from multiple goroutines; each invocation
// manages its own independent send/recv cycle.
//
// ctx cancellation is honoured during the reply receive loop.
//
// Errors are wrapped in [CallError], the same type used by [Call].
//
// Example (DEALER compute client):
//
//	result, err := zeromq.CallDealer(ctx, sock, handle, ComputeReq{X: 3, Y: 4},
//	    zeromq.CallOptions{Observer: obs})
func CallDealer[Req, Resp any](
	ctx context.Context,
	sock FramedSocket,
	handle *reqreply.RouteHandle[Req, Resp],
	req Req,
	opts CallOptions,
) (Resp, error) {
	obs := opts.Observer
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}
	var zero Resp
	start := time.Now()
	path := handle.Topic
	var callErr error

	// Resolve template topic vars if provided.
	if opts.Vars != nil {
		var buildErr error
		path, buildErr = handle.BuildTopic(opts.Vars)
		if buildErr != nil {
			stats.ReportErrors(obs, "topic_var", buildErr)
			obs.RecordRequest("ZMQ-DEALER", handle.Topic, 0, time.Since(start))
			callErr = CallError{Err: buildErr}
			return zero, callErr
		}
	}

	if to, ok := obs.(stats.TraceObserver); ok {
		ctx = to.StartSpan(ctx, "zmq.request", path)
		defer func() { to.EndSpan(ctx, callErr) }()
	}

	// encode request
	var payload []byte
	if len(handle.RequestFormats) > 0 {
		payload, callErr = handle.RequestFormats[0].Marshal(req)
	} else {
		payload, callErr = handle.EncodeRequest(req)
	}
	if callErr != nil {
		stats.ReportErrors(obs, "body", callErr)
		obs.RecordRequest("ZMQ-DEALER", path, 0, time.Since(start))
		callErr = CallError{Err: callErr}
		return zero, callErr
	}

	// send with empty delimiter (DEALER envelope)
	if err := sock.SendFrames([][]byte{emptyDelimiter, payload}); err != nil {
		callErr = CallError{Err: fmt.Errorf("send: %w", err)}
		obs.RecordRequest("ZMQ-DEALER", path, 0, time.Since(start))
		return zero, callErr
	}

	// configure recv timeout for ctx-cancellation polling
	if err := sock.SetRecvTimeout(recvPollInterval); err != nil {
		callErr = CallError{Err: fmt.Errorf("set recv timeout: %w", err)}
		return zero, callErr
	}

	// receive reply
	var frames [][]byte
	for {
		select {
		case <-ctx.Done():
			callErr = CallError{Err: ctx.Err()}
			obs.RecordRequest("ZMQ-DEALER", path, 0, time.Since(start))
			return zero, callErr
		default:
		}
		var recvErr error
		frames, recvErr = sock.RecvFrames()
		if errors.Is(recvErr, ErrTimeout) {
			continue
		}
		if recvErr != nil {
			callErr = CallError{Err: fmt.Errorf("recv: %w", recvErr)}
			obs.RecordRequest("ZMQ-DEALER", path, 0, time.Since(start))
			return zero, callErr
		}
		break
	}

	// validate framing: expect ["", status, payload]
	if len(frames) < 3 {
		callErr = CallError{Err: fmt.Errorf("malformed reply: expected [\"\", status, payload], got %d frame(s)", len(frames))}
		obs.RecordRequest("ZMQ-DEALER", path, 0, time.Since(start))
		return zero, callErr
	}
	// frames[0] = empty delimiter

	// check status
	if string(frames[1]) == "error" {
		callErr = CallError{Err: fmt.Errorf("server error: %s", frames[2])}
		obs.RecordRequest("ZMQ-DEALER", path, 500, time.Since(start))
		return zero, callErr
	}

	// decode response
	var resp Resp
	if len(handle.Formats) > 0 {
		resp, callErr = handle.Formats[0].Unmarshal(frames[2])
	} else {
		resp, callErr = handle.DecodeResponse(frames[2])
	}
	if callErr != nil {
		stats.ReportErrors(obs, "body", callErr)
		callErr = CallError{Err: fmt.Errorf("decode response: %w", callErr)}
		obs.RecordRequest("ZMQ-DEALER", path, 0, time.Since(start))
		return zero, callErr
	}
	obs.RecordRequest("ZMQ-DEALER", path, 200, time.Since(start))
	return resp, nil
}
