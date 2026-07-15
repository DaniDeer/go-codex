package ports

import (
	"context"
	"errors"
	"sync"

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
//	sql.QueryEachAdapter, file.ReadEachAdapter
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
	handles   map[string]any
	specs     map[string]any
	obs       stats.Observer

	mu      sync.Mutex
	adapter IOAdapter[Req, Resp]
}

// NewIOPort creates an IOPort with the given name, request codec, and response
// codec. opts configures Patterns, IO params, observer, and (optionally) shared
// [PortOptions.RESTBuilder]/[PortOptions.ReqReplyBuilder]/[PortOptions.MCPBuilder]
// builders. Any [RESTPattern]/[ReqReplyPattern]/[MCPPattern] in opts.Patterns is
// built eagerly into a handle retrievable via [RESTHandle]/[ReqReplyHandle]/
// [MCPHandle] via Register — fail-fast, and identical to a hand-registered
// route/tool when the matching builder option is supplied. Returns
// [PatternRegisterError] if a declared Pattern fails to build.
func NewIOPort[Req, Resp any](
	name string,
	reqCodec codex.Codec[Req],
	respCodec codex.Codec[Resp],
	opts PortOptions,
) (*IOPort[Req, Resp], error) {
	handles, specs, err := buildDualCodecPatternHandles(name, opts.Patterns, reqCodec, respCodec,
		opts.RESTBuilder, opts.ReqReplyBuilder, opts.MCPBuilder)
	if err != nil {
		return nil, err
	}
	return &IOPort[Req, Resp]{
		name:      name,
		reqCodec:  reqCodec,
		respCodec: respCodec,
		params:    opts.Params,
		handles:   handles,
		specs:     specs,
		obs:       opts.Observer,
	}, nil
}

// patternHandle implements the unexported patternHolder interface used by
// [RESTHandle], [EventHandle], [ReqReplyHandle], and [MCPHandle].
func (p *IOPort[Req, Resp]) patternHandle(kind string) (any, bool) {
	v, ok := p.handles[kind]
	return v, ok
}

// patternSpec implements the unexported patternHolder interface used by
// [RegisterREST], [RegisterEvent], [RegisterReqReply], and [RegisterMCP].
func (p *IOPort[Req, Resp]) patternSpec(kind string) (any, bool) {
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
