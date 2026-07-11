package zeromq

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/stats"
	gstream "github.com/DaniDeer/go-codex/stream"
)

// ── SubscribeStream ───────────────────────────────────────────────────────────

// SubscribeStream bridges a ZeroMQ SUB or PULL socket into a typed stream.
// A goroutine reads [topic, payload] frame pairs from sock; each payload is
// decoded by fmt and forwarded to the returned [gstream.Stream]. Decode or
// validation failures are sent to Stream.Errors as [gstream.StreamDecodeError].
//
// The stream closes when ctx is cancelled or a socket error occurs. Socket
// errors are not delivered to Stream.Errors — they terminate the goroutine
// silently so the caller can restart the subscription with a fresh socket.
//
// opts.Buffer controls the internal channel buffer size (default 0).
func SubscribeStream[T any](
	ctx context.Context,
	sock FramedSocket,
	handle *events.ChannelHandle[T],
	fmt format.Format[T],
	opts gstream.SourceOptions,
) gstream.Stream[T] {
	rawCh := make(chan []byte, opts.Buffer)
	go func() {
		defer close(rawCh)
		if err := sock.SetSubscription(handle.Topic); err != nil {
			return
		}
		if err := sock.SetRecvTimeout(recvPollInterval); err != nil {
			return
		}
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			frames, err := sock.RecvFrames()
			if err != nil {
				// ErrTimeout is the poll signal; any other error terminates.
				if !isTimeout(err) {
					return
				}
				continue
			}
			if len(frames) < 2 {
				continue // malformed: expect [topic, payload]
			}
			select {
			case rawCh <- frames[1]:
			case <-ctx.Done():
				return
			}
		}
	}()
	if opts.Name == "" {
		opts.Name = handle.Topic
	}
	return gstream.FromCodec(ctx, rawCh, fmt, opts)
}

// ── DrainPublish ──────────────────────────────────────────────────────────────

// DrainPublishOptions configures [DrainPublish].
type DrainPublishOptions struct {
	// Vars, when non-nil, substitutes {varName} placeholders in the channel
	// handle's topic template. Validated per [events.ChannelHandle.BuildTopic].
	// The same map is used for every item (static topic vars only).
	// For per-item topic var substitution, use [gstream.Drain] with [Publish] directly.
	Vars map[string]string

	// OnError, when non-nil, is called for encode failures ([PublishEncodeError]),
	// socket send failures ([SocketError]), or upstream stream errors.
	OnError func(error)

	// Observer receives per-publish lifecycle events via [stats.Observer.RecordPublish].
	Observer stats.Observer
}

// DrainPublish publishes each value item from src to sock using handle's codec.
// Encode failures are delivered to opts.OnError as [PublishEncodeError].
// Socket send failures are delivered to opts.OnError as [SocketError].
// Items from Stream.Errors are forwarded to opts.OnError unchanged.
// Blocks until src terminates or ctx is cancelled.
func DrainPublish[T any](
	ctx context.Context,
	sock FramedSocket,
	handle *events.ChannelHandle[T],
	src gstream.Stream[T],
	fmt format.Format[T],
	opts DrainPublishOptions,
) {
	onErr := opts.OnError
	pubOpts := PublishOptions{Observer: opts.Observer}

	gstream.Drain(ctx, src,
		func(ctx context.Context, v T) error {
			if err := Publish(ctx, sock, handle, v, opts.Vars, pubOpts, fmt); err != nil {
				if onErr != nil {
					onErr(err)
				}
				return nil // Drain onError already called
			}
			return nil
		},
		func(e error) {
			if onErr != nil {
				onErr(e)
			}
		},
		gstream.DrainOptions{},
	)
}

// ── AsPipelineFunc ────────────────────────────────────────────────────────────

// AsPipelineFunc converts a pipeline handler function into the plain handler
// function signature accepted by [Serve] and [ServeRouter].
//
// Internally: wraps req as [gstream.Single], calls fn to build the pipeline,
// then collects the result via [gstream.Collect]. Errors take precedence over
// values. If the pipeline emits no value, [PipelineNoResponseError] is returned.
//
// Use AsPipelineFunc when the handler body benefits from [gstream.Tap] for
// declarative intermediate observation, [gstream.Apply] for multi-step forge
// function composition, or [gstream.MapErr] for per-step error recovery:
//
//	zeromq.Serve(ctx, sock, oeeHandle,
//	    zeromq.AsPipelineFunc(func(ctx context.Context, req SensorReq) gstream.Stream[OEEResult] {
//	        s  := gstream.Single(ctx, req)
//	        s   = gstream.Apply(ctx, s, validateFn, gstream.ApplyOptions{Observer: obs})
//	        s   = gstream.Tap(ctx, s, func(v ValidatedReq) { slog.Info("valid", "id", v.ID) })
//	        out := gstream.Apply(ctx, s, oeeCalcFn, gstream.ApplyOptions{Observer: obs})
//	        return gstream.Tap(ctx, out, func(r OEEResult) { auditLog.Write(r) })
//	    }),
//	    zeromq.ServeOptions{Observer: obs})
//
// For simple single-step handlers, use a plain fn directly with [Serve].
func AsPipelineFunc[Req, Resp any](
	fn func(context.Context, Req) gstream.Stream[Resp],
) func(context.Context, Req) (Resp, error) {
	return func(ctx context.Context, req Req) (Resp, error) {
		pipeline := fn(ctx, req)
		vals, errs := gstream.Collect(ctx, pipeline)
		var zero Resp
		if len(errs) > 0 {
			return zero, errs[0]
		}
		if len(vals) == 0 {
			return zero, PipelineNoResponseError{Topic: "zeromq"}
		}
		return vals[0], nil
	}
}

// ── CallStream ────────────────────────────────────────────────────────────────

// CallStreamOptions configures [CallStream].
type CallStreamOptions struct {
	// Vars, when non-nil, substitutes {varName} placeholders in the route topic
	// template before encoding each request. The same Vars map is used for every
	// request item in the stream. For per-item topic var substitution, use
	// [gstream.Drain] with [Call] directly.
	//
	// Each variable value is validated against its registered [reqreply.RouteParam]
	// codec (if any) before the first request is sent.
	Vars map[string]string

	// Observer receives per-call lifecycle events.
	Observer stats.Observer
	// Buffer is the output Stream channel buffer size. Default 0.
	Buffer int
}

// CallStream sends each request item from src to handle using a REQ socket,
// emitting each decoded response to the returned [gstream.Stream]. Protocol
// errors, encode failures, or decode failures are sent to Stream.Errors as
// [CallError]. The stream terminates when src closes or ctx is cancelled.
//
// Requests are issued sequentially — REQ sockets are inherently synchronous.
// Use [CallDealerStream] for concurrent pipelining with a DEALER socket.
func CallStream[Req, Resp any](
	ctx context.Context,
	sock FramedSocket,
	handle *reqreply.RouteHandle[Req, Resp],
	src gstream.Stream[Req],
	opts CallStreamOptions,
) gstream.Stream[Resp] {
	values := make(chan Resp, opts.Buffer)
	errs := make(chan error, opts.Buffer)
	go func() {
		defer close(values)
		defer close(errs)
		callOpts := CallOptions{Observer: opts.Observer, Vars: opts.Vars}
		valCh := src.Values
		errCh := src.Errors
		for valCh != nil || errCh != nil {
			select {
			case <-ctx.Done():
				return
			case req, ok := <-valCh:
				if !ok {
					valCh = nil
					continue
				}
				resp, err := Call(ctx, sock, handle, req, callOpts)
				if err != nil {
					select {
					case errs <- err:
					case <-ctx.Done():
						return
					}
					continue
				}
				select {
				case values <- resp:
				case <-ctx.Done():
					return
				}
			case e, ok := <-errCh:
				if !ok {
					errCh = nil
					continue
				}
				select {
				case errs <- e:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return gstream.Stream[Resp]{Values: values, Errors: errs}
}

// ── ServeLatest ───────────────────────────────────────────────────────────────

// ServeLatestOptions configures [ServeLatest].
type ServeLatestOptions struct {
	// OnError, when non-nil, is called for socket errors ([ServeLatestError]),
	// no-value conditions ([NoLatestValueError]), or encode failures.
	// The underlying [Serve] loop errors (decode, handler, encode) are
	// delivered through this same callback.
	OnError func(error)

	// Observer receives per-request lifecycle events via [stats.Observer.RecordRequest].
	Observer stats.Observer
}

// ServeLatest runs a blocking REP loop that replies to every incoming request
// with the most recently emitted value from src.
//
// A background goroutine reads src.Values and atomically stores each new value.
// When a request arrives but no value has been produced yet, the REP socket
// sends an error reply and opts.OnError is called with [NoLatestValueError].
//
// The Req payload is decoded and validated by handle (standard [Serve] behaviour)
// but is not used to compute the response — the response is always the latest value.
//
// Returns nil when ctx is cancelled, or a [SocketError] on socket failure.
//
// Use ServeLatest for "get current OEE", "get latest sensor reading", or any
// "current state" ZMQ endpoint backed by a continuous stream computation.
func ServeLatest[Req, Resp any](
	ctx context.Context,
	sock FramedSocket,
	handle *reqreply.RouteHandle[Req, Resp],
	src gstream.Stream[Resp],
	opts ServeLatestOptions,
) error {
	// Store the latest value from src atomically.
	var latest atomic.Pointer[Resp]
	go func() {
		valCh := src.Values
		errCh := src.Errors
		for valCh != nil || errCh != nil {
			select {
			case <-ctx.Done():
				return
			case v, ok := <-valCh:
				if !ok {
					valCh = nil
					continue
				}
				v2 := v
				latest.Store(&v2)
			case _, ok := <-errCh:
				if !ok {
					errCh = nil
				}
				// errors from src are silently dropped — latest value is unaffected
			}
		}
	}()

	onErr := opts.OnError
	serveOpts := ServeOptions{
		Observer: opts.Observer,
	}
	if onErr != nil {
		serveOpts.OnError = func(se ServeError) { onErr(se) }
	}

	return Serve(ctx, sock, handle, func(ctx context.Context, _ Req) (Resp, error) {
		ptr := latest.Load()
		if ptr == nil {
			noVal := NoLatestValueError{Topic: handle.Topic}
			if onErr != nil {
				onErr(noVal)
			}
			var zero Resp
			return zero, fmt.Errorf("%w", noVal)
		}
		return *ptr, nil
	}, serveOpts)
}

// ── helpers ───────────────────────────────────────────────────────────────────

func isTimeout(err error) bool { return err == ErrTimeout }
