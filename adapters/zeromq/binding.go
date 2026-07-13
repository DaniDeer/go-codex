package zeromq

import (
	"context"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/ports"
	gstream "github.com/DaniDeer/go-codex/stream"
)

// ── SubscribeAdapter ──────────────────────────────────────────────────────────

// SubscribeAdapter returns a [ports.SourceAdapter] backed by the ZeroMQ PUB/SUB
// receive loop. Use with [ports.SourcePort.Bind]:
//
//	domain.SensorReadings.Bind(ctx, zeromq.SubscribeAdapter(
//	    sock, sensorHandle,
//	    format.JSON(ReadingCodec),
//	    zeromq.SubscribeAdapterOptions{Buffer: 8},
//	))
//
// Internally calls [SubscribeStream] and relays items to the port's channels.
func SubscribeAdapter[T any](
	sock FramedSocket,
	handle *events.ChannelHandle[T],
	fmt format.Format[T],
	opts SubscribeAdapterOptions,
) ports.SourceAdapter[T] {
	return &zmqSubscribeAdapter[T]{sock: sock, handle: handle, fmt: fmt, opts: opts}
}

// SubscribeAdapterOptions configures [SubscribeAdapter].
type SubscribeAdapterOptions struct {
	Buffer int
}

type zmqSubscribeAdapter[T any] struct {
	sock   FramedSocket
	handle *events.ChannelHandle[T]
	fmt    format.Format[T]
	opts   SubscribeAdapterOptions
}

func (a *zmqSubscribeAdapter[T]) AdapterName() string { return "zeromq.SubscribeAdapter" }

func (a *zmqSubscribeAdapter[T]) Activate(ctx context.Context, dst chan<- T, errs chan<- error) {
	s := SubscribeStream(ctx, a.sock, a.handle, a.fmt, gstream.SourceOptions{Buffer: a.opts.Buffer})
	valCh := s.Values
	errCh := s.Errors
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
	DrainPublish(ctx, a.sock, a.handle, src, a.fmt, a.opts)
}

// ── CallAdapter ───────────────────────────────────────────────────────────────

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
	return CallStream(ctx, a.sock, a.handle, src, a.opts)
}
