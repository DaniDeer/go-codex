package ports

import (
	"context"
	"sync"
	"sync/atomic"

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
//	nethttp.LatestAdapter, zeromq.LatestAdapter, mcpgo.LatestAdapter
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
//	    ports.PortOptions{Patterns: []ports.Pattern{
//	        ports.RESTPattern{Method: "GET", Path: "/readings/latest"},
//	    }}))
//
//	// main.go — wiring only
//	handle, _ := ports.RESTHandle[struct{}, db.Reading](domain.Latest)
//	must(domain.Latest.Bind(ctx, nethttp.LatestAdapter(mux, handle, nethttp.Options{})))
//	go domain.Latest.Feed(ctx, readings)
//
// Lifecycle:
//  1. [NewLatestPort] — declare port with payload codec (+ Patterns).
//  2. [Bind] — register serving adapters (fan-out: many transports, one cache).
//  3. [Feed] — drain the stream into the cell (usually in a goroutine).
type LatestPort[T any] struct {
	name    string
	codec   codex.Codec[T]
	params  []IOParam
	handles map[string]any
	specs   map[string]any
	obs     stats.Observer

	cell atomic.Pointer[T]
	wg   sync.WaitGroup
}

// NewLatestPort creates a LatestPort with the given name and payload codec.
// opts configures Patterns, observer, and (optionally) shared
// [PortOptions.RESTBuilder]/[PortOptions.ReqReplyBuilder]/[PortOptions.MCPBuilder]
// builders. Any [RESTPattern]/[ReqReplyPattern]/[MCPPattern] in opts.Patterns
// is built eagerly with request codec codex.Struct[struct{}]() (requests
// carry no payload — the response is always the cached value) and response
// codec = the port's codec. Handles are retrievable via
// [RESTHandle][struct{}, T], [ReqReplyHandle][struct{}, T], and
// [MCPHandle][struct{}, T]. Returns [PatternRegisterError] if a declared
// Pattern fails to build.
func NewLatestPort[T any](name string, codec codex.Codec[T], opts PortOptions) (*LatestPort[T], error) {
	handles, specs, err := buildDualCodecPatternHandles(name, opts.Patterns,
		codex.Struct[struct{}](), codec,
		opts.RESTBuilder, opts.ReqReplyBuilder, opts.MCPBuilder, true)
	if err != nil {
		return nil, err
	}
	return &LatestPort[T]{
		name:    name,
		codec:   codec,
		params:  opts.Params,
		handles: handles,
		specs:   specs,
		obs:     opts.Observer,
	}, nil
}

// patternHandle implements the unexported patternHolder interface used by
// [RESTHandle], [ReqReplyHandle], and [MCPHandle].
func (p *LatestPort[T]) patternHandle(kind string) (any, bool) {
	v, ok := p.handles[kind]
	return v, ok
}

// patternSpec implements the unexported patternHolder interface used by
// [RegisterREST], [RegisterReqReply], and [RegisterMCP].
func (p *LatestPort[T]) patternSpec(kind string) (any, bool) {
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
