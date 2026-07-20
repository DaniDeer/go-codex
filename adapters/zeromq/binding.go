package zeromq

import (
	"context"
	"errors"
	"fmt"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/ports"
	"github.com/DaniDeer/go-codex/stats"
	gstream "github.com/DaniDeer/go-codex/stream"
)

// ── SubscribeAdapter ──────────────────────────────────────────────────────────

// SubscribeAdapterOptions configures [SubscribeAdapter].
type SubscribeAdapterOptions struct {
	Buffer int
}

// SubscribeAdapter returns a [ports.SourceAdapter] backed by the ZeroMQ PUB/SUB
// receive loop. Use with [ports.SourcePort.Bind]:
//
//	domain.SensorReadings.Bind(ctx, zeromq.SubscribeAdapter(
//	    sock, sensorHandle,
//	    format.JSON(ReadingCodec),
//	    zeromq.SubscribeAdapterOptions{Buffer: 8},
//	))
func SubscribeAdapter[T any](
	sock FramedSocket,
	handle *events.ChannelHandle[T],
	fmt format.Format[T],
	opts SubscribeAdapterOptions,
) ports.SourceAdapter[T] {
	return &zmqSubscribeAdapter[T]{sock: sock, handle: handle, fmt: fmt, opts: opts}
}

type zmqSubscribeAdapter[T any] struct {
	sock   FramedSocket
	handle *events.ChannelHandle[T]
	fmt    format.Format[T]
	opts   SubscribeAdapterOptions
}

func (a *zmqSubscribeAdapter[T]) AdapterName() string { return "zeromq.SubscribeAdapter" }

// Activate delegates to [Subscribe] (rather than hand-rolling frame
// reads + [gstream.FromCodec] as it did before merge-field support was
// added) so that topic-var merging (when the channel declares merge-capable
// [events.NewTopicParam] fields) is applied automatically — mirroring
// mqtt5.SubscribeAdapter's wiring. An internal buffered pair of channels
// preserves [SubscribeAdapterOptions.Buffer]'s existing sizing behavior.
func (a *zmqSubscribeAdapter[T]) Activate(ctx context.Context, dst chan<- T, errs chan<- error) {
	valCh := make(chan T, a.opts.Buffer)
	errCh := make(chan error, a.opts.Buffer)
	go func() {
		defer close(valCh)
		defer close(errCh)
		subOpts := SubscribeOptions{
			OnError: func(se SubscribeError) {
				select {
				case errCh <- se:
				case <-ctx.Done():
				}
			},
		}
		_ = Subscribe(ctx, a.sock, a.handle, func(_ context.Context, v T) error {
			select {
			case valCh <- v:
			case <-ctx.Done():
			}
			return nil
		}, subOpts, a.fmt)
	}()
	for valCh != nil || errCh != nil {
		select {
		case <-ctx.Done():
			return
		case v, ok := <-valCh:
			if !ok {
				valCh = nil
				continue
			}
			select {
			case dst <- v:
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
}

// ── PublishAdapter ────────────────────────────────────────────────────────────

// DrainPublishOptions configures [PublishAdapter] publish behaviour.
type DrainPublishOptions struct {
	// Vars substitutes {varName} placeholders in the channel handle's topic
	// template.
	//
	// When nil, topic vars are derived PER-ITEM from each item's own
	// merge-field-declared struct fields (the same convenience
	// [PublishHandle] provides) — every item may resolve to a different
	// concrete topic. When set to a non-nil map (including an explicitly
	// empty one), that map is used as-is for every item (static topic vars
	// only) — the escape hatch, unchanged from prior behavior.
	Vars map[string]string
	// OnError, when non-nil, is called for encode failures ([PublishEncodeError]),
	// socket send failures ([SocketError]), or upstream stream errors.
	OnError func(error)
	// Observer receives per-publish lifecycle events.
	Observer stats.Observer
}

// PublishAdapter returns a [ports.SinkAdapter] that publishes each item via ZeroMQ.
// Use with [ports.SinkPort.Bind]:
//
//	domain.OEEResults.Bind(ctx, zeromq.PublishAdapter(sock, alertHandle, format.JSON(OEECodec),
//	    zeromq.DrainPublishOptions{}))
func PublishAdapter[T any](
	sock FramedSocket,
	handle *events.ChannelHandle[T],
	fmt format.Format[T],
	opts DrainPublishOptions,
) ports.SinkAdapter[T] {
	return &zmqPublishAdapter[T]{sock: sock, handle: handle, fmt: fmt, opts: opts}
}

type zmqPublishAdapter[T any] struct {
	sock   FramedSocket
	handle *events.ChannelHandle[T]
	fmt    format.Format[T]
	opts   DrainPublishOptions
}

func (a *zmqPublishAdapter[T]) AdapterName() string { return "zeromq.PublishAdapter" }

func (a *zmqPublishAdapter[T]) Activate(ctx context.Context, src gstream.Stream[T]) {
	onErr := a.opts.OnError
	pubOpts := PublishOptions{Observer: a.opts.Observer}
	gstream.Drain(ctx, src,
		func(ctx context.Context, v T) error {
			var err error
			if a.opts.Vars == nil {
				err = PublishHandle(ctx, a.sock, a.handle, v, pubOpts, a.fmt)
			} else {
				err = Publish(ctx, a.sock, a.handle, v, a.opts.Vars, pubOpts, a.fmt)
			}
			if err != nil {
				if onErr != nil {
					onErr(err)
				}
			}
			return nil
		},
		func(e error) {
			if onErr != nil {
				onErr(e)
			}
		},
		gstream.DrainOptions{Observer: a.opts.Observer},
	)
}

// ── CallAdapter ───────────────────────────────────────────────────────────────

// CallStreamOptions configures [CallAdapter].
type CallStreamOptions struct {
	// Vars substitutes {varName} placeholders in the route topic template.
	//
	// When nil, vars are derived PER-ITEM from each item's own
	// merge-field-declared struct fields (the same convenience [CallHandle]
	// provides). When set to a non-nil map (including an explicitly empty
	// one), that map is used as-is for every request (static vars only) —
	// the escape hatch, unchanged from prior behavior.
	Vars map[string]string
	// Observer receives per-call lifecycle events.
	Observer stats.Observer
	// Buffer is the output Stream channel buffer size. Default 0.
	Buffer int
}

// CallAdapter returns a [ports.IOAdapter] that performs ZeroMQ request-reply
// for each upstream item. Use with [ports.IOPort.Bind]:
//
//	domain.Calibration.Bind(ctx, zeromq.CallAdapter(sock, calibHandle, zeromq.CallStreamOptions{}))
func CallAdapter[Req, Resp any](
	sock FramedSocket,
	handle *reqreply.RouteHandle[Req, Resp],
	opts CallStreamOptions,
) ports.IOAdapter[Req, Resp] {
	return &zmqCallAdapter[Req, Resp]{sock: sock, handle: handle, opts: opts}
}

type zmqCallAdapter[Req, Resp any] struct {
	sock   FramedSocket
	handle *reqreply.RouteHandle[Req, Resp]
	opts   CallStreamOptions
}

func (a *zmqCallAdapter[Req, Resp]) AdapterName() string { return "zeromq.CallAdapter" }

func (a *zmqCallAdapter[Req, Resp]) Transform(ctx context.Context, src gstream.Stream[Req]) gstream.Stream[Resp] {
	values := make(chan Resp, a.opts.Buffer)
	errs := make(chan error, a.opts.Buffer)
	go func() {
		defer close(values)
		defer close(errs)
		callOpts := CallOptions{Observer: a.opts.Observer, Vars: a.opts.Vars}
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
				var resp Resp
				var err error
				if a.opts.Vars == nil {
					resp, err = CallHandle(ctx, a.sock, a.handle, req, callOpts)
				} else {
					resp, err = Call(ctx, a.sock, a.handle, req, callOpts)
				}
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

// ── ServeAdapter ──────────────────────────────────────────────────────────────

// ServeAdapter returns a [ports.ToolAdapter] that registers the pipeline
// function as a ZeroMQ REP server via [Serve]. When [ports.ToolPort.Bind] is
// called, the pipeline function is wrapped as an [AsPipelineFunc] handler and
// [Serve] is started in a background goroutine. Use with [ports.ToolPort.Bind]:
//
//	domain.OEEToolPort.Bind(ctx, zeromq.ServeAdapter(repSock, handle, zeromq.ServeOptions{}))
func ServeAdapter[Req, Resp any](
	sock FramedSocket,
	handle *reqreply.RouteHandle[Req, Resp],
	opts ServeOptions,
) ports.ToolAdapter[Req, Resp] {
	return &zmqServeAdapter[Req, Resp]{sock: sock, handle: handle, opts: opts}
}

type zmqServeAdapter[Req, Resp any] struct {
	sock   FramedSocket
	handle *reqreply.RouteHandle[Req, Resp]
	opts   ServeOptions
}

func (a *zmqServeAdapter[Req, Resp]) AdapterName() string { return "zeromq.ServeAdapter" }

func (a *zmqServeAdapter[Req, Resp]) Bind(
	ctx context.Context,
	fn func(context.Context, Req) gstream.Stream[Resp],
) error {
	go Serve(ctx, a.sock, a.handle, AsPipelineFunc(fn), a.opts) //nolint:errcheck
	return nil
}

// ── LatestAdapter ─────────────────────────────────────────────────────────────

// LatestAdapter returns a [ports.LatestAdapter] that serves a
// [ports.LatestPort]'s cached value over a blocking REP loop — the
// port-based successor to [ServeLatest] (which owns its own cache cell; the
// port owns it here). Use with [ports.LatestPort.Bind]; the port runs the
// blocking Serve in a supervised goroutine:
//
//	handle, _ := ports.ReqReplyHandle[struct{}, OEE](domain.Latest)
//	must(domain.Latest.Bind(ctx, zeromq.LatestAdapter(sock, handle, zeromq.ServeLatestOptions{})))
//	go domain.Latest.Feed(ctx, oeeStream)
//
// When a request arrives before the first value, the REP socket sends an
// error reply and opts.OnError receives [NoLatestValueError] (same semantics
// as ServeLatest).
func LatestAdapter[Resp any](
	sock FramedSocket,
	handle *reqreply.RouteHandle[struct{}, Resp],
	opts ServeLatestOptions,
) ports.LatestAdapter[Resp] {
	return &zmqLatestAdapter[Resp]{sock: sock, handle: handle, opts: opts}
}

type zmqLatestAdapter[Resp any] struct {
	sock   FramedSocket
	handle *reqreply.RouteHandle[struct{}, Resp]
	opts   ServeLatestOptions
}

func (a *zmqLatestAdapter[Resp]) AdapterName() string { return "zeromq.LatestAdapter" }

func (a *zmqLatestAdapter[Resp]) Serve(ctx context.Context, latest func() (Resp, bool)) error {
	onErr := a.opts.OnError
	serveOpts := ServeOptions{Observer: a.opts.Observer}
	if onErr != nil {
		serveOpts.OnError = func(se ServeError) {
			var nv NoLatestValueError
			if errors.As(se.Err, &nv) {
				onErr(nv)
			} else {
				onErr(se)
			}
		}
	}
	// Blocking REP loop until ctx is done — the port's supervised goroutine
	// accommodates this Serve shape.
	return Serve(ctx, a.sock, a.handle, func(_ context.Context, _ struct{}) (Resp, error) {
		v, ok := latest()
		if !ok {
			var zero Resp
			return zero, fmt.Errorf("%w", NoLatestValueError{Topic: a.handle.Topic})
		}
		return v, nil
	}, serveOpts)
}
