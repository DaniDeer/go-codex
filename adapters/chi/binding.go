package chi

import (
	"context"
	"net/http"

	gochi "github.com/go-chi/chi/v5"

	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/ports"
	gstream "github.com/DaniDeer/go-codex/stream"
)

// ── IngestAdapter ─────────────────────────────────────────────────────────────

// IngestAdapterOptions configures [IngestAdapter].
type IngestAdapterOptions struct {
	Options Options
	Buffer  int
}

// IngestAdapter returns a [ports.SourceAdapter] that accepts HTTP requests as
// pipeline items via a chi router. When Activate is called it registers a handler
// on r and runs until ctx is cancelled. Use with [ports.SourcePort.Bind]:
//
//	domain.SensorReadings.Bind(ctx, chi.IngestAdapter(
//	    r, ingestHandle, chi.IngestAdapterOptions{Buffer: 8}))
func IngestAdapter[T any](
	r gochi.Router,
	handle *rest.RouteHandle[T, struct{}],
	opts IngestAdapterOptions,
) ports.SourceAdapter[T] {
	return &chiIngestAdapter[T]{r: r, handle: handle, opts: opts}
}

type chiIngestAdapter[T any] struct {
	r      gochi.Router
	handle *rest.RouteHandle[T, struct{}]
	opts   IngestAdapterOptions
}

func (a *chiIngestAdapter[T]) AdapterName() string { return "chi.IngestAdapter" }

func (a *chiIngestAdapter[T]) Activate(ctx context.Context, dst chan<- T, errs chan<- error) {
	ch := make(chan T, a.opts.Buffer)
	wrappedOpts := a.opts.Options
	wrappedOpts.ErrorHandler = chiRemapStatus(a.opts.Options.ErrorHandler, func(err error) int {
		var pfe PipelineFullError
		if chiIsErrorAs(err, &pfe) {
			return http.StatusServiceUnavailable
		}
		return 0
	})
	h := Handler(a.handle, func(_ context.Context, req T) (struct{}, error) {
		select {
		case ch <- req:
			return struct{}{}, nil
		default:
			return struct{}{}, PipelineFullError{
				Path:     a.handle.Descriptor.Path,
				Capacity: cap(ch),
			}
		}
	}, wrappedOpts)
	a.r.Method(a.handle.Descriptor.Method, a.handle.Descriptor.Path, h)

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case v, ok := <-ch:
				if !ok {
					return
				}
				select {
				case dst <- v:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	<-ctx.Done()
}

// chiIsErrorAs is a type-assert helper used only within binding.go.
func chiIsErrorAs[T any](err error, target *T) bool {
	if v, ok := err.(T); ok {
		*target = v
		return true
	}
	return false
}

// ── SSEAdapter ────────────────────────────────────────────────────────────────

// SSEAdapterOptions configures [SSEAdapter].
type SSEAdapterOptions struct {
	Options          Options
	SSEStreamOptions SSEStreamOptions
}

// SSEAdapter returns a [ports.SinkAdapter] that serves each SinkPort item as
// an SSE event to all connected clients via a chi router. When Activate is
// called it registers an [SSEFromHub]-backed handler. Use with [ports.SinkPort.Bind]:
//
//	domain.OEEResults.Bind(ctx, chi.SSEAdapter(r, sseHandle, chi.SSEAdapterOptions{}))
func SSEAdapter[Event any](
	r gochi.Router,
	handle *rest.SSERouteHandle[struct{}, Event],
	opts SSEAdapterOptions,
) ports.SinkAdapter[Event] {
	return &chiSSEAdapter[Event]{r: r, handle: handle, opts: opts}
}

type chiSSEAdapter[Event any] struct {
	r      gochi.Router
	handle *rest.SSERouteHandle[struct{}, Event]
	opts   SSEAdapterOptions
}

func (a *chiSSEAdapter[Event]) AdapterName() string { return "chi.SSEAdapter" }

func (a *chiSSEAdapter[Event]) Activate(ctx context.Context, src gstream.Stream[Event]) {
	hub := gstream.NewBroadcastHub(ctx, src, 32)
	sseOpts := a.opts.SSEStreamOptions
	if sseOpts.Topic == "" {
		sseOpts.Topic = a.handle.Descriptor.Path
	}
	fn := SSEFromHub[struct{}, Event](hub, sseOpts)
	RegisterSSE(a.r, a.handle, fn, a.opts.Options)
	<-ctx.Done()
}

// ── PipelineAdapter ───────────────────────────────────────────────────────────

// PipelineAdapterOptions configures [PipelineAdapter].
type PipelineAdapterOptions struct {
	Options Options
}

// PipelineAdapter returns a [ports.ToolAdapter] that registers the pipeline
// function as an HTTP endpoint via [PipelineHandler] on a chi router. When
// [ports.ToolPort.Bind] is called it registers the handler. Use with [ports.ToolPort.Bind]:
//
//	domain.OEEToolPort.Bind(ctx, chi.PipelineAdapter(r, httpHandle, chi.PipelineAdapterOptions{}))
func PipelineAdapter[Req, Resp any](
	r gochi.Router,
	handle *rest.RouteHandle[Req, Resp],
	opts PipelineAdapterOptions,
) ports.ToolAdapter[Req, Resp] {
	return &chiPipelineAdapter[Req, Resp]{r: r, handle: handle, opts: opts}
}

type chiPipelineAdapter[Req, Resp any] struct {
	r      gochi.Router
	handle *rest.RouteHandle[Req, Resp]
	opts   PipelineAdapterOptions
}

func (a *chiPipelineAdapter[Req, Resp]) AdapterName() string { return "chi.PipelineAdapter" }

func (a *chiPipelineAdapter[Req, Resp]) Bind(
	_ context.Context,
	fn func(context.Context, Req) gstream.Stream[Resp],
) error {
	h := PipelineHandler(a.handle, func(ctx context.Context, req Req) gstream.Stream[Resp] {
		return fn(ctx, req)
	}, a.opts.Options)
	a.r.Method(a.handle.Descriptor.Method, a.handle.Descriptor.Path, h)
	return nil
}
