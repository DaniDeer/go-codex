package ports

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	apimcp "github.com/DaniDeer/go-codex/api/mcp"
	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/stats"
	gstream "github.com/DaniDeer/go-codex/stream"
)

// LatestAdapter[T] serves the most recent value of a [LatestPort] to clients.
//
// Serve runs the transport endpoint. latest returns the most recent value and
// false while no value has arrived yet. Serve MAY return immediately after
// registration (HTTP mux, MCP server) or block until ctx is done (ZeroMQ REP
// loop) — [LatestPort.Bind] runs it in a supervised goroutine either way.
//
// Implemented by transport binding constructors:
//
//	nethttp.LatestAdapter, chi.LatestAdapter, zeromq.LatestAdapter,
//	mcpgo.LatestAdapter
type LatestAdapter[T any] interface {
	// Serve runs the endpoint, answering each request with latest().
	Serve(ctx context.Context, latest func() (T, bool)) error
	// AdapterName returns a descriptor for [PortBindError] and observability.
	AdapterName() string
}

// LatestPort[T] is a typed, protocol-agnostic reactive-cache port: a
// continuously updated "current state" value served to request/response
// clients. It represents a pipeline → query boundary.
//
// [LatestPort.Feed] drains a stream into an atomic cell; bound adapters
// answer every request from that cell — no per-request pipeline run, no DB
// query. The cache outlives the stream: when src terminates, adapters keep
// serving the last value.
//
//	// domain/ports — declared like every other boundary
//	var Latest = codex.Must(ports.NewLatestPort[db.Reading]("rest/latest", readingCodec,
//	    ports.PortOptions{RESTBuilder: RESTBuilder}))
//
//	// main.go — wiring: plug in the Pattern, get the handle, bind
//	handle := codex.Must(domain.Latest.PluginRESTPattern(ports.RESTPattern{
//	    Method: "GET", Path: "/readings/latest",
//	}))
//	must(domain.Latest.Bind(ctx, nethttp.LatestAdapter(mux, handle, nethttp.Options{})))
//	go domain.Latest.Feed(ctx, readings)
//
// Lifecycle:
//  1. [NewLatestPort] — declare port with payload codec.
//  2. Plug in a Pattern via [LatestPort.PluginRESTPattern]/etc.
//  3. [Bind] — register serving adapters (fan-out: many transports, one cache).
//  4. [Feed] — drain the stream into the cell (usually in a goroutine).
type LatestPort[T any] struct {
	name   string
	codec  codex.Codec[T]
	params []IOParam
	obs    stats.Observer

	restBuilder     *rest.Server
	reqReplyBuilder *reqreply.Builder
	mcpBuilder      *apimcp.Builder

	handlesMu sync.Mutex
	handles   map[string]any
	specs     map[string]any

	cell atomic.Pointer[T]
	wg   sync.WaitGroup
}

// NewLatestPort creates a LatestPort with the given name and payload codec.
// opts configures observer and (optionally) shared RESTBuilder/
// ReqReplyBuilder/MCPBuilder references for later [PluginRESTPattern]/
// [PluginReqReplyPattern]/[PluginMCPPattern] calls. Each plugs in with
// request codec codex.Struct[struct{}]() (requests carry no payload — the
// response is always the cached value) and response codec = the port's
// codec, returning the resulting handle directly.
func NewLatestPort[T any](name string, codec codex.Codec[T], opts PortOptions) (*LatestPort[T], error) {
	return &LatestPort[T]{
		name:            name,
		codec:           codec,
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
// see [SourcePort.pluginPattern] for the full rationale. Request codec is
// always codex.Struct[struct{}](); response codec is the port's own codec.
func (p *LatestPort[T]) pluginPattern(pattern Pattern, kind string) (any, error) {
	p.handlesMu.Lock()
	if _, exists := p.handles[kind]; exists {
		p.handlesMu.Unlock()
		return nil, PatternRegisterError{Port: p.name, Kind: kind, Err: fmt.Errorf("pattern of kind %q already plugged in", kind)}
	}
	p.handlesMu.Unlock()

	handles, specs, err := buildDualCodecPatternHandles(p.name, []Pattern{pattern},
		codex.Struct[struct{}](), p.codec, p.restBuilder, p.reqReplyBuilder, p.mcpBuilder, true, nil, false)
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
// [rest.RouteHandle][struct{}, T] directly.
func (p *LatestPort[T]) PluginRESTPattern(pattern RESTPattern) (*rest.RouteHandle[struct{}, T], error) {
	v, err := p.pluginPattern(pattern, patternKindREST)
	if err != nil {
		return nil, err
	}
	h, _ := v.(*rest.RouteHandle[struct{}, T])
	return h, nil
}

// PluginReqReplyPattern registers pattern and returns the resulting
// [reqreply.RouteHandle][struct{}, T] directly.
func (p *LatestPort[T]) PluginReqReplyPattern(pattern ReqReplyPattern) (*reqreply.RouteHandle[struct{}, T], error) {
	v, err := p.pluginPattern(pattern, patternKindReqReply)
	if err != nil {
		return nil, err
	}
	h, _ := v.(*reqreply.RouteHandle[struct{}, T])
	return h, nil
}

// PluginMCPPattern registers pattern and returns the resulting
// [apimcp.ToolHandle][struct{}, T] directly.
func (p *LatestPort[T]) PluginMCPPattern(pattern MCPPattern) (*apimcp.ToolHandle[struct{}, T], error) {
	v, err := p.pluginPattern(pattern, patternKindMCP)
	if err != nil {
		return nil, err
	}
	h, _ := v.(*apimcp.ToolHandle[struct{}, T])
	return h, nil
}

// PluginFilePattern registers pattern and returns the resulting [File]
// directly, built from the port's payload codec.
func (p *LatestPort[T]) PluginFilePattern(pattern FilePattern) (File[T], error) {
	v, err := p.pluginPattern(pattern, patternKindFile)
	if err != nil {
		return File[T]{}, err
	}
	h, _ := v.(File[T])
	return h, nil
}

// PluginCachePattern registers pattern and returns the resulting [Cache]
// directly — the cached value is the port's own reactive-cache cell.
func (p *LatestPort[T]) PluginCachePattern(pattern CachePattern) (Cache[T], error) {
	v, err := p.pluginPattern(pattern, patternKindCache)
	if err != nil {
		return Cache[T]{}, err
	}
	h, _ := v.(Cache[T])
	return h, nil
}

// patternHandle implements the unexported patternHolder interface used
// internally by [SQLMeta] (see [SourcePort.patternHandle] for rationale).
func (p *LatestPort[T]) patternHandle(kind string) (any, bool) {
	p.handlesMu.Lock()
	defer p.handlesMu.Unlock()
	v, ok := p.handles[kind]
	return v, ok
}

// patternSpec implements the unexported patternHolder interface used by
// [RegisterREST], [RegisterReqReply], and [RegisterMCP].
func (p *LatestPort[T]) patternSpec(kind string) (any, bool) {
	p.handlesMu.Lock()
	defer p.handlesMu.Unlock()
	v, ok := p.specs[kind]
	return v, ok
}

// Name returns the port's declared name.
func (p *LatestPort[T]) Name() string { return p.name }

// Params returns the port's declared [IOParam] slice.
func (p *LatestPort[T]) Params() []IOParam { return p.params }

// Codec returns the port's payload codec.
func (p *LatestPort[T]) Codec() codex.Codec[T] { return p.codec }

// Latest returns the most recently fed value, or (zero, false) while no
// value has arrived yet. This is the same read side handed to every bound
// adapter.
func (p *LatestPort[T]) Latest() (T, bool) {
	ptr := p.cell.Load()
	if ptr == nil {
		var zero T
		return zero, false
	}
	return *ptr, true
}

// Bind registers a [LatestAdapter] to serve the cached value. Multiple Bind
// calls produce fan-out: many transports, one cache. The adapter's Serve runs
// in a supervised goroutine wrapped in the standard "port.bind" observer
// event — Serve may return immediately after registration or block until ctx
// is done; both shapes are correct.
func (p *LatestPort[T]) Bind(ctx context.Context, a LatestAdapter[T]) error {
	obs := p.obs
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}
	adapterCtx := adapterContext(ctx, p.params, p.handles)

	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		_ = bindWithObserver(adapterCtx, obs, p.name, a.AdapterName(), func(spanCtx context.Context) error {
			return a.Serve(spanCtx, p.Latest)
		})
	}()
	return nil
}

// Feed drains src into the port's atomic cell: each value replaces the cached
// one; src errors are dropped (they do not affect responses — matching the
// pre-port HandlerLatest/ServeLatest semantics). Feed returns when src
// terminates or ctx is cancelled; bound adapters KEEP SERVING the last value
// after Feed returns — the cache outlives the stream by design.
//
// Call in a goroutine when the pipeline must continue concurrently:
//
//	go domain.Latest.Feed(ctx, readings)
func (p *LatestPort[T]) Feed(ctx context.Context, src gstream.Stream[T]) {
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
			p.cell.Store(&v2)
		case _, ok := <-errCh:
			if !ok {
				errCh = nil
			}
			// src errors are dropped — responses always come from the cell.
		}
	}
}
