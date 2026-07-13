package nethttp

import (
	"context"
	"net/http"
	"time"

	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/ports"
	gstream "github.com/DaniDeer/go-codex/stream"
)

// ── IngestAdapter ─────────────────────────────────────────────────────────────

// IngestAdapterOptions configures [IngestAdapter].
type IngestAdapterOptions struct {
	// Options are passed to [HandlerIngest] (observer, security, error handler).
	Options Options
	// Buffer is the per-port ingest channel buffer size. Default 0.
	Buffer int
}

// IngestAdapter returns a [ports.SourceAdapter] that accepts HTTP requests as
// pipeline items. When Activate is called it registers a handler with mux and
// runs until ctx is cancelled. Use with [ports.SourcePort.Bind]:
//
//	domain.SensorReadings.Bind(ctx, nethttp.IngestAdapter(
//	    mux, ingestHandle, nethttp.IngestAdapterOptions{Buffer: 8}))
func IngestAdapter[T any](
	mux *http.ServeMux,
	handle *rest.RouteHandle[T, struct{}],
	opts IngestAdapterOptions,
) ports.SourceAdapter[T] {
	return &nethttpIngestAdapter[T]{mux: mux, handle: handle, opts: opts}
}

type nethttpIngestAdapter[T any] struct {
	mux    *http.ServeMux
	handle *rest.RouteHandle[T, struct{}]
	opts   IngestAdapterOptions
}

func (a *nethttpIngestAdapter[T]) AdapterName() string { return "nethttp.IngestAdapter" }

func (a *nethttpIngestAdapter[T]) Activate(ctx context.Context, dst chan<- T, errs chan<- error) {
	ch := make(chan T, a.opts.Buffer)
	h := HandlerIngest(a.handle, ch, a.opts.Options)
	key := a.handle.Descriptor.Method + " " + a.handle.Descriptor.Path
	a.mux.Handle(key, h)

	// relay from the handler channel to the port's dst
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

// ── SSEAdapter ────────────────────────────────────────────────────────────────

// SSEAdapterOptions configures [SSEAdapter].
type SSEAdapterOptions struct {
	// Options are passed to [RegisterSSE].
	Options Options
	// SSEStreamOptions configure the per-connection stream handler.
	SSEStreamOptions SSEStreamOptions
}

// SSEAdapter returns a [ports.SinkAdapter] that serves each item from the SinkPort
// as an SSE event to all connected clients. When Activate is called it registers
// an SSEFromHub-backed handler with mux. Use with [ports.SinkPort.Bind]:
//
//	domain.OEEResults.Bind(ctx, nethttp.SSEAdapter(mux, sseHandle,
//	    nethttp.SSEAdapterOptions{}))
func SSEAdapter[Event any](
	mux *http.ServeMux,
	handle *rest.SSERouteHandle[struct{}, Event],
	opts SSEAdapterOptions,
) ports.SinkAdapter[Event] {
	return &nethttpSSEAdapter[Event]{mux: mux, handle: handle, opts: opts}
}

type nethttpSSEAdapter[Event any] struct {
	mux    *http.ServeMux
	handle *rest.SSERouteHandle[struct{}, Event]
	opts   SSEAdapterOptions
}

func (a *nethttpSSEAdapter[Event]) AdapterName() string { return "nethttp.SSEAdapter" }

func (a *nethttpSSEAdapter[Event]) Activate(ctx context.Context, src gstream.Stream[Event]) {
	hub := gstream.NewBroadcastHub(ctx, src, 32)
	sseOpts := a.opts.SSEStreamOptions
	if sseOpts.Topic == "" {
		sseOpts.Topic = a.handle.Descriptor.Path
	}
	fn := SSEFromHub[struct{}, Event](hub, sseOpts)
	RegisterSSE(a.mux, a.handle, fn, a.opts.Options)
	// Block until src terminates (hub runs the stream goroutine).
	<-ctx.Done()
}

// ── CallAdapter ───────────────────────────────────────────────────────────────

// CallAdapter returns a [ports.IOAdapter] that sends each item as an HTTP request,
// emitting responses downstream. Use with [ports.IOPort.Bind]:
//
//	domain.Calibration.Bind(ctx, nethttp.CallAdapter(
//	    httpClient, "http://svc:8080", calibHandle,
//	    nethttp.CallAdapterOptions{}))
func CallAdapter[Req, Resp any](
	client *http.Client,
	baseURL string,
	handle *rest.RouteHandle[Req, Resp],
	opts CallStreamOptions,
) ports.IOAdapter[Req, Resp] {
	return &nethttpCallAdapter[Req, Resp]{client: client, baseURL: baseURL, handle: handle, opts: opts}
}

type nethttpCallAdapter[Req, Resp any] struct {
	client  *http.Client
	baseURL string
	handle  *rest.RouteHandle[Req, Resp]
	opts    CallStreamOptions
}

func (a *nethttpCallAdapter[Req, Resp]) AdapterName() string { return "nethttp.CallAdapter" }

func (a *nethttpCallAdapter[Req, Resp]) Transform(ctx context.Context, src gstream.Stream[Req]) gstream.Stream[Resp] {
	return CallStream(ctx, a.client, a.baseURL, a.handle, src, a.opts)
}

// ── PollAdapter ───────────────────────────────────────────────────────────────

// PollAdapter returns a [ports.SourceAdapter] that polls an HTTP endpoint at
// interval, emitting each response. Use with [ports.SourcePort.Bind]:
//
//	domain.Configs.Bind(ctx, nethttp.PollAdapter(
//	    client, "http://config-svc", configHandle, ConfigReq{},
//	    5*time.Minute, nethttp.PollAdapterOptions{}))
func PollAdapter[Req, Resp any](
	client *http.Client,
	baseURL string,
	handle *rest.RouteHandle[Req, Resp],
	req Req,
	interval time.Duration,
	opts PollStreamOptions,
) ports.SourceAdapter[Resp] {
	return &nethttpPollAdapter[Req, Resp]{
		client: client, baseURL: baseURL, handle: handle,
		req: req, interval: interval, opts: opts,
	}
}

type nethttpPollAdapter[Req, Resp any] struct {
	client   *http.Client
	baseURL  string
	handle   *rest.RouteHandle[Req, Resp]
	req      Req
	interval time.Duration
	opts     PollStreamOptions
}

func (a *nethttpPollAdapter[Req, Resp]) AdapterName() string { return "nethttp.PollAdapter" }

func (a *nethttpPollAdapter[Req, Resp]) Activate(ctx context.Context, dst chan<- Resp, errs chan<- error) {
	s := PollStream(ctx, a.client, a.baseURL, a.handle, a.req, a.interval, a.opts)
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

// ── DrainCallAdapter ──────────────────────────────────────────────────────────

// DrainCallAdapter returns a [ports.SinkAdapter] that calls an HTTP endpoint
// for each item (fire-and-forget; response is discarded). Use with [ports.SinkPort.Bind]:
//
//	domain.Events.Bind(ctx, nethttp.DrainCallAdapter(
//	    client, "http://audit-svc", auditHandle,
//	    nethttp.DrainCallOptions{}))
func DrainCallAdapter[Req, Resp any](
	client *http.Client,
	baseURL string,
	handle *rest.RouteHandle[Req, Resp],
	opts DrainCallOptions,
) ports.SinkAdapter[Req] {
	return &nethttpDrainCallAdapter[Req, Resp]{client: client, baseURL: baseURL, handle: handle, opts: opts}
}

type nethttpDrainCallAdapter[Req, Resp any] struct {
	client  *http.Client
	baseURL string
	handle  *rest.RouteHandle[Req, Resp]
	opts    DrainCallOptions
}

func (a *nethttpDrainCallAdapter[Req, Resp]) AdapterName() string { return "nethttp.DrainCallAdapter" }

func (a *nethttpDrainCallAdapter[Req, Resp]) Activate(ctx context.Context, src gstream.Stream[Req]) {
	DrainCall(ctx, a.client, a.baseURL, a.handle, src, a.opts)
}
