package ports

import (
	"context"
	"errors"
	"fmt"
	"sync"

	apimcp "github.com/DaniDeer/go-codex/api/mcp"
	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/api/rest"
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
//	mcpgo.ToolPipelineAdapter,
//	nethttp.PipelineAdapter, chi.PipelineAdapter,
//	zeromq.ServeAdapter, mqtt5.ServeAdapter
//
// (For cache-serving tools — answer from a continuously updated value instead
// of running the pipeline per call — use [LatestPort] + mcpgo.LatestAdapter.)
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

	// Builders are stored (not used eagerly) so PluginRESTPattern/
	// PluginReqReplyPattern/PluginMCPPattern can register against the SAME
	// shared builder every other Pattern-carrying declaration in the
	// service uses.
	restBuilder     *rest.Builder
	reqReplyBuilder *reqreply.Builder
	mcpBuilder      *apimcp.Builder

	handlesMu sync.Mutex
	handles   map[string]any
	specs     map[string]any

	mu sync.Mutex
	fn func(context.Context, In) gstream.Stream[Out]
}

// NewToolPort creates a ToolPort with the given name, request codec, and
// response codec. name is used for observability, error context, and spec
// generation. opts configures IO params, observer, and (optionally) shared
// RESTBuilder/ReqReplyBuilder/MCPBuilder references for later
// [PluginRESTPattern]/[PluginReqReplyPattern]/[PluginMCPPattern] calls — a
// ToolPort exposed over multiple transports (e.g. HTTP + MCP) plugs in one
// Pattern per transport. Use the protocol-named convenience constructors
// ([NewRestToolPort], [NewMCPToolPort]) for the common single-Pattern case.
func NewToolPort[In, Out any](
	name string,
	inCodec codex.Codec[In],
	outCodec codex.Codec[Out],
	opts PortOptions,
) (*ToolPort[In, Out], error) {
	return &ToolPort[In, Out]{
		name:            name,
		inCodec:         inCodec,
		outCodec:        outCodec,
		params:          opts.Params,
		obs:             opts.Observer,
		restBuilder:     opts.RESTBuilder,
		reqReplyBuilder: opts.ReqReplyBuilder,
		mcpBuilder:      opts.MCPBuilder,
		handles:         map[string]any{},
		specs:           map[string]any{},
	}, nil
}

// pluginPattern is the shared engine behind every PluginXxxPattern method —
// see [SourcePort.pluginPattern] for the full rationale. cacheAllowed is
// always false for ToolPort (a cache is not a tool surface).
func (p *ToolPort[In, Out]) pluginPattern(pattern Pattern, kind string) (any, error) {
	p.handlesMu.Lock()
	if _, exists := p.handles[kind]; exists {
		p.handlesMu.Unlock()
		return nil, PatternRegisterError{Port: p.name, Kind: kind, Err: fmt.Errorf("pattern of kind %q already plugged in", kind)}
	}
	p.handlesMu.Unlock()

	handles, specs, err := buildDualCodecPatternHandles(p.name, []Pattern{pattern}, p.inCodec, p.outCodec,
		p.restBuilder, p.reqReplyBuilder, p.mcpBuilder, false)
	if err != nil {
		return nil, err
	}

	p.handlesMu.Lock()
	for k, v := range handles {
		p.handles[k] = v
	}
	for k, v := range specs {
		p.specs[k] = v
	}
	p.handlesMu.Unlock()
	return handles[kind], nil
}

// PluginRESTPattern registers pattern and returns the resulting
// [rest.RouteHandle] directly — bind e.g. nethttp.PipelineAdapter to it.
func (p *ToolPort[In, Out]) PluginRESTPattern(pattern RESTPattern) (*rest.RouteHandle[In, Out], error) {
	v, err := p.pluginPattern(pattern, patternKindREST)
	if err != nil {
		return nil, err
	}
	h, _ := v.(*rest.RouteHandle[In, Out])
	return h, nil
}

// PluginReqReplyPattern registers pattern and returns the resulting
// [reqreply.RouteHandle] directly.
func (p *ToolPort[In, Out]) PluginReqReplyPattern(pattern ReqReplyPattern) (*reqreply.RouteHandle[In, Out], error) {
	v, err := p.pluginPattern(pattern, patternKindReqReply)
	if err != nil {
		return nil, err
	}
	h, _ := v.(*reqreply.RouteHandle[In, Out])
	return h, nil
}

// PluginMCPPattern registers pattern and returns the resulting
// [apimcp.ToolHandle] directly — bind e.g. mcpgo.ToolPipelineAdapter to it.
func (p *ToolPort[In, Out]) PluginMCPPattern(pattern MCPPattern) (*apimcp.ToolHandle[In, Out], error) {
	v, err := p.pluginPattern(pattern, patternKindMCP)
	if err != nil {
		return nil, err
	}
	h, _ := v.(*apimcp.ToolHandle[In, Out])
	return h, nil
}

// PluginFilePattern registers pattern and returns the resulting [File]
// directly, built from the port's OUT codec (the file's content is the
// port's response).
func (p *ToolPort[In, Out]) PluginFilePattern(pattern FilePattern) (File[Out], error) {
	v, err := p.pluginPattern(pattern, patternKindFile)
	if err != nil {
		return File[Out]{}, err
	}
	h, _ := v.(File[Out])
	return h, nil
}

// NewRestToolPort is a thin convenience constructor: [NewToolPort] followed
// immediately by [ToolPort.PluginRESTPattern] — see [NewRestPort] for the
// full rationale (identical here).
func NewRestToolPort[In, Out any](name string, inCodec codex.Codec[In], outCodec codex.Codec[Out],
	pattern RESTPattern, opts PortOptions,
) (*ToolPort[In, Out], *rest.RouteHandle[In, Out], error) {
	p, err := NewToolPort[In, Out](name, inCodec, outCodec, opts)
	if err != nil {
		return nil, nil, err
	}
	h, err := p.PluginRESTPattern(pattern)
	if err != nil {
		return nil, nil, err
	}
	return p, h, nil
}

// NewMCPToolPort is [NewRestToolPort]'s MCPPattern counterpart.
func NewMCPToolPort[In, Out any](name string, inCodec codex.Codec[In], outCodec codex.Codec[Out],
	pattern MCPPattern, opts PortOptions,
) (*ToolPort[In, Out], *apimcp.ToolHandle[In, Out], error) {
	p, err := NewToolPort[In, Out](name, inCodec, outCodec, opts)
	if err != nil {
		return nil, nil, err
	}
	h, err := p.PluginMCPPattern(pattern)
	if err != nil {
		return nil, nil, err
	}
	return p, h, nil
}

// patternHandle implements the unexported patternHolder interface used
// internally by [SQLMeta] (see [SourcePort.patternHandle] for rationale).
func (p *ToolPort[In, Out]) patternHandle(kind string) (any, bool) {
	p.handlesMu.Lock()
	defer p.handlesMu.Unlock()
	v, ok := p.handles[kind]
	return v, ok
}

// patternSpec implements the unexported patternHolder interface used by
// [RegisterREST], [RegisterEvent], [RegisterReqReply], and [RegisterMCP].
func (p *ToolPort[In, Out]) patternSpec(kind string) (any, bool) {
	p.handlesMu.Lock()
	defer p.handlesMu.Unlock()
	v, ok := p.specs[kind]
	return v, ok
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

	return bindWithObserver(adapterContext(ctx, p.params, p.handles), obs, p.name, a.AdapterName(), func(spanCtx context.Context) error {
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
