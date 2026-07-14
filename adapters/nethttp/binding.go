package nethttp

import (
	"context"
	"net/http"
	"time"

	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/ports"
	"github.com/DaniDeer/go-codex/stats"
	gstream "github.com/DaniDeer/go-codex/stream"
)

// ── IngestAdapter ─────────────────────────────────────────────────────────────

// IngestAdapterOptions configures [IngestAdapter].
type IngestAdapterOptions struct {
	Options Options
	Buffer  int
}

// IngestAdapter returns a [ports.SourceAdapter] that accepts HTTP requests as
// pipeline items. When Activate is called it registers a handler with mux.
// Use with [ports.SourcePort.Bind]:
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
	// Inline HandlerIngest logic: wrap opts.ErrorHandler for PipelineFullError → 503.
	wrappedOpts := a.opts.Options
	wrappedOpts.ErrorHandler = remapStatus(a.opts.Options.ErrorHandler, func(err error) int {
		var pfe PipelineFullError
		if isErrorAs(err, &pfe) {
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
	a.mux.Handle(a.handle.Descriptor.Method+" "+a.handle.Descriptor.Path, h)

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

// isErrorAs is a type-assert helper used by binding.go only.
func isErrorAs[T any](err error, target *T) bool {
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

// SSEAdapter returns a [ports.SinkAdapter] that serves each item from the SinkPort
// as an SSE event to all connected clients. Use with [ports.SinkPort.Bind]:
//
//	domain.OEEResults.Bind(ctx, nethttp.SSEAdapter(mux, sseHandle, nethttp.SSEAdapterOptions{}))
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
	<-ctx.Done()
}

// ── CallAdapter ───────────────────────────────────────────────────────────────

// CallStreamOptions configures [CallAdapter].
type CallStreamOptions struct {
	// Vars, when non-nil, substitutes {varName} placeholders in the route's path template.
	// The same map is used for every request (static path vars only).
	Vars     map[string]string
	CallOpts CallOptions
	// Buffer is the output Stream channel buffer size. Default 0.
	Buffer int
}

// CallAdapter returns a [ports.IOAdapter] that sends each item as an HTTP request,
// emitting responses downstream. Use with [ports.IOPort.Bind]:
//
//	domain.Calibration.Bind(ctx, nethttp.CallAdapter(
//	    httpClient, "http://svc:8080", calibHandle, nethttp.CallStreamOptions{}))
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
	values := make(chan Resp, a.opts.Buffer)
	errs := make(chan error, a.opts.Buffer)
	go func() {
		defer close(values)
		defer close(errs)
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
				resp, err := Call(ctx, a.client, a.baseURL, a.handle, req, a.opts.Vars, a.opts.CallOpts)
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

// ── PollAdapter ───────────────────────────────────────────────────────────────

// PollStreamOptions configures [PollAdapter].
type PollStreamOptions struct {
	// Vars, when non-nil, substitutes {varName} placeholders in the route's path template.
	// The same map is used for every poll (static path vars only).
	Vars     map[string]string
	Observer stats.Observer
	Buffer   int
}

// PollAdapter returns a [ports.SourceAdapter] that polls an HTTP endpoint at
// interval, emitting each response. Use with [ports.SourcePort.Bind]:
//
//	domain.Configs.Bind(ctx, nethttp.PollAdapter(
//	    client, "http://config-svc", configHandle, ConfigReq{}, 5*time.Minute, nethttp.PollStreamOptions{}))
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
	obs := a.opts.Observer
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}
	values := make(chan Resp, a.opts.Buffer)
	errChan := make(chan error, a.opts.Buffer)
	go func() {
		defer close(values)
		defer close(errChan)
		ticker := time.NewTicker(a.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				resp, err := Call(ctx, a.client, a.baseURL, a.handle, a.req, a.opts.Vars, CallOptions{Observer: obs})
				if err != nil {
					select {
					case errChan <- err:
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
			}
		}
	}()
	valCh := values
	errCh := errChan
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

// DrainCallOptions configures [DrainCallAdapter].
type DrainCallOptions struct {
	// Vars, when non-nil, substitutes {varName} placeholders in the route's path template.
	Vars     map[string]string
	OnError  func(error)
	CallOpts CallOptions
}

// DrainCallAdapter returns a [ports.SinkAdapter] that calls an HTTP endpoint
// for each item (fire-and-forget; response discarded). Use with [ports.SinkPort.Bind]:
//
//	domain.Events.Bind(ctx, nethttp.DrainCallAdapter(
//	    client, "http://audit-svc", auditHandle, nethttp.DrainCallOptions{}))
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
	onErr := a.opts.OnError
	gstream.Drain(ctx, src,
		func(ctx context.Context, item Req) error {
			if _, err := Call(ctx, a.client, a.baseURL, a.handle, item, a.opts.Vars, a.opts.CallOpts); err != nil {
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
		gstream.DrainOptions{Observer: a.opts.CallOpts.Observer},
	)
}
