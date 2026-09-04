package ports

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/DaniDeer/go-codex/api/llm"
	apimcp "github.com/DaniDeer/go-codex/api/mcp"
	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/stats"
	gstream "github.com/DaniDeer/go-codex/stream"
)

// IOAdapter[Req, Resp] transforms each upstream Req into zero or more Resp
// values for an [IOPort].
//
// Transform applies the adapter's IO operation (HTTP call, SQL query, file read,
// MQTT request-reply) to each item in src. One Req may produce zero, one, or
// multiple Resp values (e.g. a SQL row query returns N rows per request).
//
// Implemented by transport binding constructors:
//
//	nethttp.CallAdapter, mqtt5.CallAdapter, zeromq.CallAdapter,
//	sql.QueryEachAdapter, file.ReadEachAdapter, redis.GetAdapter,
//	redis.SetAdapter
type IOAdapter[Req, Resp any] interface {
	// Transform applies the adapter's IO operation to each item in src.
	// May emit 0..N Resp values per Req item. The stream terminates when
	// src terminates or ctx is cancelled.
	Transform(ctx context.Context, src gstream.Stream[Req]) gstream.Stream[Resp]
	// AdapterName returns a descriptor for [PortBindError] and observability.
	AdapterName() string
}

// IOPort[Req, Resp] is a typed, protocol-agnostic intermediate IO enforcement point.
// It transforms each upstream Req into zero or more downstream Resp values through
// a single bound [IOAdapter].
//
// The same pipeline code works regardless of whether the enrichment source is an
// HTTP service, SQL query, file, or MQTT request-reply — only the Bind call in
// main.go changes.
//
//	// domain/pipeline.go — no adapter imports
//	var Calibration = ports.NewIOPort[SensorReading, CalibratedReading](
//	    "calibration", ReadingCodec, calibratedCodec,
//	    ports.PortOptions{Params: []ports.IOParam{{Name: "sensorID", Required: true}}})
//
//	// main.go — swap between HTTP / SQL / file without changing pipeline code
//	domain.Calibration.Bind(ctx, nethttp.CallAdapter(httpClient, baseURL, handle, callOpts))
//	// domain.Calibration.Bind(ctx, sql.QueryEachAdapter(db, calibCodec, queryFn, opts))
//	// domain.Calibration.Bind(ctx, file.ReadEachAdapter(calibFile, varsFor, combine))
//
// Lifecycle:
//  1. [NewIOPort] — declare port with request and response codecs.
//  2. [Bind] — set exactly one adapter in main.go.
//  3. [Connect] — attach to upstream stream; returns response stream.
type IOPort[Req, Resp any] struct {
	name      string
	reqCodec  codex.Codec[Req]
	respCodec codex.Codec[Resp]
	params    []IOParam
	obs       stats.Observer

	// Builders are stored (not used eagerly) so PluginRESTPattern/
	// PluginReqReplyPattern/PluginMCPPattern/PluginSQLPattern/
	// PluginCachePattern/PluginLLMPattern can register against the SAME
	// shared builder every other Pattern-carrying declaration in the
	// service uses.
	restBuilder     *rest.Server
	reqReplyBuilder *reqreply.Builder
	mcpBuilder      *apimcp.Builder
	llmBuilder      *llm.Builder

	handlesMu sync.Mutex
	handles   map[string]any
	specs     map[string]any

	mu      sync.Mutex
	adapter IOAdapter[Req, Resp]
}

// NewIOPort creates an IOPort with the given name, request codec, and response
// codec. opts configures IO params, observer, and (optionally) shared
// RESTBuilder/ReqReplyBuilder/MCPBuilder/LLMBuilder references for later
// [PluginRESTPattern]/[PluginReqReplyPattern]/[PluginMCPPattern]/
// [PluginSQLPattern]/[PluginCachePattern]/[PluginLLMPattern] calls — declare
// the port's communication Pattern separately (see [PortOptions]), or use one
// of the protocol-named convenience constructors ([NewRestPort],
// [NewReqReplyPort], [NewMCPPort], [NewSQLPort]) for the common
// single-Pattern case.
func NewIOPort[Req, Resp any](
	name string,
	reqCodec codex.Codec[Req],
	respCodec codex.Codec[Resp],
	opts PortOptions,
) (*IOPort[Req, Resp], error) {
	return &IOPort[Req, Resp]{
		name:            name,
		reqCodec:        reqCodec,
		respCodec:       respCodec,
		params:          opts.Params,
		obs:             opts.Observer,
		restBuilder:     opts.RESTBuilder,
		reqReplyBuilder: opts.ReqReplyBuilder,
		mcpBuilder:      opts.MCPBuilder,
		llmBuilder:      opts.LLMBuilder,
		handles:         map[string]any{},
		specs:           map[string]any{},
	}, nil
}

// pluginPattern is the shared engine behind every PluginXxxPattern method —
// see [SourcePort.pluginPattern] for the full rationale. cacheAllowed is
// always true for IOPort (an IOPort's response can be cached).
func (p *IOPort[Req, Resp]) pluginPattern(pattern Pattern, kind string) (any, error) {
	p.handlesMu.Lock()
	if _, exists := p.handles[kind]; exists {
		p.handlesMu.Unlock()
		return nil, PatternRegisterError{Port: p.name, Kind: kind, Err: fmt.Errorf("pattern of kind %q already plugged in", kind)}
	}
	p.handlesMu.Unlock()

	handles, specs, err := buildDualCodecPatternHandles(p.name, []Pattern{pattern}, p.reqCodec, p.respCodec,
		p.restBuilder, p.reqReplyBuilder, p.mcpBuilder, true, p.llmBuilder, true)
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
// [rest.RouteHandle] directly — bind e.g. nethttp.CallAdapter to it.
func (p *IOPort[Req, Resp]) PluginRESTPattern(pattern RESTPattern) (*rest.RouteHandle[Req, Resp], error) {
	v, err := p.pluginPattern(pattern, patternKindREST)
	if err != nil {
		return nil, err
	}
	h, _ := v.(*rest.RouteHandle[Req, Resp])
	return h, nil
}

// PluginLLMPattern registers pattern and returns the resulting
// [llm.CallHandle] directly — bind e.g. openai.CallAdapter to it.
//
// LLMPattern is only supported on IOPort — returns [PatternRegisterError]
// wrapping a descriptive error if called on any other port type's
// declaration path (this method only exists on IOPort, so the restriction is
// enforced by the type system; the error only surfaces if a shared
// buildDualCodecPatternHandles caller passes llmAllowed=false, which never
// happens for IOPort itself).
func (p *IOPort[Req, Resp]) PluginLLMPattern(pattern LLMPattern) (*llm.CallHandle[Req, Resp], error) {
	v, err := p.pluginPattern(pattern, patternKindLLM)
	if err != nil {
		return nil, err
	}
	h, _ := v.(*llm.CallHandle[Req, Resp])
	return h, nil
}

// PluginReqReplyPattern registers pattern and returns the resulting
// [reqreply.RouteHandle] directly — bind e.g. mqtt5's ReqReply call adapter
// to it.
func (p *IOPort[Req, Resp]) PluginReqReplyPattern(pattern ReqReplyPattern) (*reqreply.RouteHandle[Req, Resp], error) {
	v, err := p.pluginPattern(pattern, patternKindReqReply)
	if err != nil {
		return nil, err
	}
	h, _ := v.(*reqreply.RouteHandle[Req, Resp])
	return h, nil
}

// PluginMCPPattern registers pattern and returns the resulting
// [apimcp.ToolHandle] directly.
func (p *IOPort[Req, Resp]) PluginMCPPattern(pattern MCPPattern) (*apimcp.ToolHandle[Req, Resp], error) {
	v, err := p.pluginPattern(pattern, patternKindMCP)
	if err != nil {
		return nil, err
	}
	h, _ := v.(*apimcp.ToolHandle[Req, Resp])
	return h, nil
}

// PluginFilePattern registers pattern and returns the resulting [File]
// directly, built from the port's RESPONSE codec (the file's content is the
// port's response — a per-item retrieval reads a File[Resp]).
func (p *IOPort[Req, Resp]) PluginFilePattern(pattern FilePattern) (File[Resp], error) {
	v, err := p.pluginPattern(pattern, patternKindFile)
	if err != nil {
		return File[Resp]{}, err
	}
	h, _ := v.(File[Resp])
	return h, nil
}

// PluginSQLPattern registers pattern's Table/Op metadata — SQLPattern builds
// no handle (metadata-only; retrieve it later via [SQLMeta], or rely on
// [Bind]'s automatic [WithSQLMeta] propagation to the bound adapter's
// context). Returns an error only if pattern was already plugged in.
func (p *IOPort[Req, Resp]) PluginSQLPattern(pattern SQLPattern) error {
	_, err := p.pluginPattern(pattern, patternKindSQL)
	return err
}

// PluginCachePattern registers pattern and returns the resulting [Cache]
// directly (cached value = the port's response) — bind e.g. redis's cache
// adapter to it.
func (p *IOPort[Req, Resp]) PluginCachePattern(pattern CachePattern) (Cache[Resp], error) {
	v, err := p.pluginPattern(pattern, patternKindCache)
	if err != nil {
		return Cache[Resp]{}, err
	}
	h, _ := v.(Cache[Resp])
	return h, nil
}

// NewRestPort is a thin convenience constructor: [NewIOPort] followed
// immediately by [IOPort.PluginRESTPattern] — for the common case where
// exactly one RESTPattern is known upfront. Always expressible by unwrapping
// into the two separate calls; use those directly instead for late Pattern
// binding or a port carrying more than one Pattern kind.
func NewRestPort[Req, Resp any](name string, reqCodec codex.Codec[Req], respCodec codex.Codec[Resp],
	pattern RESTPattern, opts PortOptions,
) (*IOPort[Req, Resp], *rest.RouteHandle[Req, Resp], error) {
	p, err := NewIOPort[Req, Resp](name, reqCodec, respCodec, opts)
	if err != nil {
		return nil, nil, err
	}
	h, err := p.PluginRESTPattern(pattern)
	if err != nil {
		return nil, nil, err
	}
	return p, h, nil
}

// NewReqReplyPort is [NewRestPort]'s ReqReplyPattern counterpart.
func NewReqReplyPort[Req, Resp any](name string, reqCodec codex.Codec[Req], respCodec codex.Codec[Resp],
	pattern ReqReplyPattern, opts PortOptions,
) (*IOPort[Req, Resp], *reqreply.RouteHandle[Req, Resp], error) {
	p, err := NewIOPort[Req, Resp](name, reqCodec, respCodec, opts)
	if err != nil {
		return nil, nil, err
	}
	h, err := p.PluginReqReplyPattern(pattern)
	if err != nil {
		return nil, nil, err
	}
	return p, h, nil
}

// NewMCPPort is [NewRestPort]'s MCPPattern counterpart.
func NewMCPPort[Req, Resp any](name string, reqCodec codex.Codec[Req], respCodec codex.Codec[Resp],
	pattern MCPPattern, opts PortOptions,
) (*IOPort[Req, Resp], *apimcp.ToolHandle[Req, Resp], error) {
	p, err := NewIOPort[Req, Resp](name, reqCodec, respCodec, opts)
	if err != nil {
		return nil, nil, err
	}
	h, err := p.PluginMCPPattern(pattern)
	if err != nil {
		return nil, nil, err
	}
	return p, h, nil
}

// NewSQLPort is [NewRestPort]'s SQLPattern counterpart — SQLPattern builds no
// handle, so this returns just the port and error.
func NewSQLPort[Req, Resp any](name string, reqCodec codex.Codec[Req], respCodec codex.Codec[Resp],
	pattern SQLPattern, opts PortOptions,
) (*IOPort[Req, Resp], error) {
	p, err := NewIOPort[Req, Resp](name, reqCodec, respCodec, opts)
	if err != nil {
		return nil, err
	}
	if err := p.PluginSQLPattern(pattern); err != nil {
		return nil, err
	}
	return p, nil
}

// patternHandle implements the unexported patternHolder interface used
// internally by [SQLMeta] (see [SourcePort.patternHandle] for rationale).
func (p *IOPort[Req, Resp]) patternHandle(kind string) (any, bool) {
	p.handlesMu.Lock()
	defer p.handlesMu.Unlock()
	v, ok := p.handles[kind]
	return v, ok
}

// patternSpec implements the unexported patternHolder interface used by
// [RegisterREST], [RegisterEvent], [RegisterReqReply], and [RegisterMCP].
func (p *IOPort[Req, Resp]) patternSpec(kind string) (any, bool) {
	p.handlesMu.Lock()
	defer p.handlesMu.Unlock()
	v, ok := p.specs[kind]
	return v, ok
}

// Name returns the port's declared name.
func (p *IOPort[Req, Resp]) Name() string { return p.name }

// Params returns the port's declared [IOParam] slice.
func (p *IOPort[Req, Resp]) Params() []IOParam { return p.params }

// ReqCodec returns the port's request codec.
func (p *IOPort[Req, Resp]) ReqCodec() codex.Codec[Req] { return p.reqCodec }

// RespCodec returns the port's response codec.
func (p *IOPort[Req, Resp]) RespCodec() codex.Codec[Resp] { return p.respCodec }

// Bind sets the [IOAdapter] for this port. Exactly one adapter is allowed —
// calling Bind a second time returns [PortBindError]. Bind must be called
// before [Connect].
func (p *IOPort[Req, Resp]) Bind(ctx context.Context, a IOAdapter[Req, Resp]) error {
	obs := p.obs
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}
	return bindWithObserver(ctx, obs, p.name, a.AdapterName(), func(_ context.Context) error {
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.adapter != nil {
			return PortBindError{
				Port:    p.name,
				Adapter: a.AdapterName(),
				Err:     errors.New("IOPort already has an adapter bound; only one adapter is allowed"),
			}
		}
		p.adapter = a
		return nil
	})
}

// Connect transforms each item from src through the bound adapter, returning
// the response stream. Returns an error stream containing [PortNoAdapterError]
// if no adapter was bound.
//
// Call after [Bind]. The returned stream terminates when src terminates or ctx
// is cancelled.
func (p *IOPort[Req, Resp]) Connect(ctx context.Context, src gstream.Stream[Req]) gstream.Stream[Resp] {
	p.mu.Lock()
	a := p.adapter
	p.mu.Unlock()

	if a == nil {
		errCh := make(chan error, 1)
		errCh <- PortNoAdapterError{Port: p.name}
		close(errCh)
		valCh := make(chan Resp)
		close(valCh)
		return gstream.Stream[Resp]{Values: valCh, Errors: errCh}
	}

	return a.Transform(adapterContext(ctx, p.params, p.handles), src)
}

// Call is a convenience wrapper around [IOPort.Connect] for a single
// request/response, non-pipeline use: it wraps req in a one-item stream via
// [gstream.Single], calls Connect, and collects exactly one result via
// [gstream.Collect]. Returns [PortNoAdapterError] if no adapter has been
// bound, or [PortNoResponseError] if the bound adapter produces zero values
// for req.
//
// Use Call for a single idiomatic Go request/response; use [IOPort.Connect]
// directly when driving many items through the bound adapter as part of a
// larger stream pipeline. Both connect to the SAME declared port and the
// SAME [IOPort.Bind] call — plain Go request/response is a first-class
// consumption style, not a fallback.
func (p *IOPort[Req, Resp]) Call(ctx context.Context, req Req) (Resp, error) {
	out := p.Connect(ctx, gstream.Single(ctx, req))
	vals, errs := gstream.Collect(ctx, out)
	var zero Resp
	if len(errs) > 0 {
		return zero, errs[0]
	}
	if len(vals) == 0 {
		return zero, PortNoResponseError{Port: p.name}
	}
	return vals[0], nil
}
