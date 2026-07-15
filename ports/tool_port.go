package ports

import (
	"context"
	"errors"
	"sync"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/stats"
	gstream "github.com/DaniDeer/go-codex/stream"
)

// ToolAdapter[In, Out] is an adapter that receives requests from a transport
// backend and routes each one through the pipeline function, returning the
// response to the caller.
//
// Implemented by transport binding constructors:
//
//	mcpgo.ToolPipelineAdapter, mcpgo.ToolLatestAdapter,
//	nethttp.PipelineAdapter, chi.PipelineAdapter,
//	zeromq.ServeAdapter, mqtt5.ServeAdapter
type ToolAdapter[In, Out any] interface {
	// Bind registers fn as the handler for this transport backend.
	// fn is the pipeline function set on the [ToolPort] via [ToolPort.SetPipeline].
	// Returns an error if registration fails (e.g. route conflict, connection error).
	Bind(ctx context.Context, fn func(context.Context, In) gstream.Stream[Out]) error
	// AdapterName returns a descriptor for [PortBindError] and observability.
	AdapterName() string
}

// ToolPort[In, Out] is a typed, protocol-agnostic server-side request/response
// IO enforcement point. It represents the "handle this request" boundary — the
// complement of [IOPort] (which is client-side).
//
// Declare in domain/pipeline code. Set the pipeline function with [SetPipeline].
// Bind to one or more transports in main.go. Multiple Bind calls expose the same
// pipeline on multiple transports simultaneously (MCP + HTTP + ZeroMQ).
//
//	// domain/pipeline.go — zero transport imports
//	var OEEToolPort = ports.NewToolPort[OEEIn, OEEResult](
//	    "oee-calc", oeeInCodec, oeeResultCodec, ports.PortOptions{})
//
//	func init() {
//	    OEEToolPort.SetPipeline(func(ctx context.Context, req OEEIn) gstream.Stream[OEEResult] {
//	        return gstream.Apply(ctx, gstream.Single(ctx, req), oeeCalcFn, gstream.ApplyOptions{})
//	    })
//	}
//
//	// main.go — serve on multiple transports
//	domain.OEEToolPort.Bind(ctx, mcpgo.ToolPipelineAdapter(server, handle, opts))
//	domain.OEEToolPort.Bind(ctx, nethttp.PipelineAdapter(mux, httpHandle, opts))
type ToolPort[In, Out any] struct {
	name     string
	inCodec  codex.Codec[In]
	outCodec codex.Codec[Out]
	params   []IOParam
	obs      stats.Observer

	mu sync.Mutex
	fn func(context.Context, In) gstream.Stream[Out]
}

// NewToolPort creates a ToolPort with the given name, request codec, and response codec.
// name is used for observability, error context, and future spec generation.
// opts configures IO params and observer.
func NewToolPort[In, Out any](
	name string,
	inCodec codex.Codec[In],
	outCodec codex.Codec[Out],
	opts PortOptions,
) *ToolPort[In, Out] {
	return &ToolPort[In, Out]{
		name:     name,
		inCodec:  inCodec,
		outCodec: outCodec,
		params:   opts.Params,
		obs:      opts.Observer,
	}
}

// Name returns the port's declared name.
func (p *ToolPort[In, Out]) Name() string { return p.name }

// Params returns the port's declared [IOParam] slice.
func (p *ToolPort[In, Out]) Params() []IOParam { return p.params }

// InCodec returns the port's request codec.
func (p *ToolPort[In, Out]) InCodec() codex.Codec[In] { return p.inCodec }

// OutCodec returns the port's response codec.
func (p *ToolPort[In, Out]) OutCodec() codex.Codec[Out] { return p.outCodec }

// SetPipeline sets the domain pipeline function that handles each request.
// Must be called before [Bind]. Calling SetPipeline again replaces the function.
//
// The pipeline function must be safe for concurrent calls — each request starts
// an independent invocation.
func (p *ToolPort[In, Out]) SetPipeline(fn func(context.Context, In) gstream.Stream[Out]) {
	p.mu.Lock()
	p.fn = fn
	p.mu.Unlock()
}

// Bind registers the pipeline with a transport adapter. Can be called multiple
// times to expose the same pipeline on multiple transports.
//
// Returns [PortBindError] wrapping [PortNoPipelineError] if [SetPipeline] was
// not called before Bind. Returns [PortBindError] wrapping the adapter error if
// the adapter's Bind call fails (e.g. route conflict).
func (p *ToolPort[In, Out]) Bind(ctx context.Context, a ToolAdapter[In, Out]) error {
	obs := p.obs
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}

	p.mu.Lock()
	fn := p.fn
	p.mu.Unlock()

	if fn == nil {
		err := PortBindError{
			Port:    p.name,
			Adapter: a.AdapterName(),
			Err:     PortNoPipelineError{Port: p.name},
		}
		obs.RecordRequest("port.bind", p.name+"/"+a.AdapterName(), 500, 0)
		return err
	}

	return bindWithObserver(WithParams(ctx, p.params), obs, p.name, a.AdapterName(), func(spanCtx context.Context) error {
		if err := a.Bind(spanCtx, fn); err != nil {
			if errors.As(err, new(PortBindError)) {
				return err
			}
			return PortBindError{
				Port:    p.name,
				Adapter: a.AdapterName(),
				Err:     err,
			}
		}
		return nil
	})
}
