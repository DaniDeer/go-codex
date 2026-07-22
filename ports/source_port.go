package ports

import (
	"context"
	"sync"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/stats"
	gstream "github.com/DaniDeer/go-codex/stream"
)

// SourceAdapter[T] is an adapter that produces items for a [SourcePort].
//
// Activate runs until ctx is cancelled, writing items to dst and errors to errs.
// It must NOT close either channel — [SourcePort] owns channel lifecycle.
//
// Implemented by transport binding constructors:
//
//	mqtt5.SubscribeAdapter, mqtt.SubscribeAdapter, nethttp.IngestAdapter,
//	nethttp.PollAdapter, zeromq.SubscribeAdapter, file.ScanAdapter,
//	file.WatchAdapter, sql.QueryAdapter, websocket.IngestSocketAdapter
type SourceAdapter[T any] interface {
	// Activate runs the adapter until ctx is cancelled, writing items to dst
	// and errors to errs. Must not close either channel.
	Activate(ctx context.Context, dst chan<- T, errs chan<- error)
	// AdapterName returns a descriptor for [PortBindError] and observability.
	AdapterName() string
}

// SourcePort[T] is a typed, protocol-agnostic inbound IO enforcement point.
// It represents an external → pipeline boundary.
//
// Declare in domain/pipeline code alongside [codex.Codec] definitions.
// Bind one or more [SourceAdapter]s in main.go. Multiple adapters produce
// fan-in: items from all adapters are merged into a single stream.
//
//	// domain/pipeline.go — no adapter imports
//	var SensorReadings = ports.NewSourcePort[SensorReading]("sensor-readings",
//	    ReadingCodec,
//	    ports.PortOptions{
//	        Params: []ports.IOParam{
//	            {Name: "sensorID", Required: true}.WithCodec(sensorIDCodec),
//	        },
//	        Buffer: 8,
//	    })
//
//	// main.go — protocol decision
//	domain.SensorReadings.Bind(ctx,
//	    mqtt5.SubscribeAdapter(client, router, sensorHandle, 0, fmt, opts))
//
// Lifecycle:
//  1. [NewSourcePort] — declare port with schema.
//  2. [Bind] — register adapters (fan-in sources). All Bind calls before Stream.
//  3. [Stream] — obtain the merged stream; pass to pipeline operators.
type SourcePort[T any] struct {
	name    string
	codec   codex.Codec[T]
	params  []IOParam
	handles map[string]any
	specs   map[string]any
	obs     stats.Observer
	buffer  int

	ch    chan T
	errCh chan error
	wg    sync.WaitGroup

	adaptersMu sync.Mutex
	adapters   []string // AdapterName() of every bound SourceAdapter, in Bind order
}

// NewSourcePort creates a SourcePort with the given name and payload codec.
// opts configures Patterns, IO params, buffer size, observer, and (optionally)
// a shared [PortOptions.EventBuilder]. Any [EventPattern] in opts.Patterns is
// built eagerly into a handle retrievable via [EventHandle] via
// events.Channel.Register — fail-fast, and identical to a hand-registered
// channel when opts.EventBuilder is supplied. Returns [PatternRegisterError]
// if a declared Pattern fails to build (e.g. a duplicate topic on a shared
// EventBuilder, or a topic/param mismatch).
func NewSourcePort[T any](name string, codec codex.Codec[T], opts PortOptions) (*SourcePort[T], error) {
	handles, specs, err := buildEventPatternHandles(name, opts.Patterns, codec, roleSource, opts.EventBuilder, opts.RESTBuilder)
	if err != nil {
		return nil, err
	}
	return &SourcePort[T]{
		name:    name,
		codec:   codec,
		params:  opts.Params,
		handles: handles,
		specs:   specs,
		obs:     opts.Observer,
		buffer:  opts.Buffer,
		ch:      make(chan T, opts.Buffer),
		errCh:   make(chan error, opts.Buffer),
	}, nil
}

// patternHandle implements the unexported patternHolder interface used by
// [RESTHandle], [EventHandle], [ReqReplyHandle], and [MCPHandle].
func (p *SourcePort[T]) patternHandle(kind string) (any, bool) {
	v, ok := p.handles[kind]
	return v, ok
}

// patternSpec implements the unexported patternHolder interface used by
// [RegisterREST], [RegisterEvent], [RegisterReqReply], and [RegisterMCP].
func (p *SourcePort[T]) patternSpec(kind string) (any, bool) {
	v, ok := p.specs[kind]
	return v, ok
}

// Name returns the port's declared name.
func (p *SourcePort[T]) Name() string { return p.name }

// Params returns the port's declared [IOParam] slice.
func (p *SourcePort[T]) Params() []IOParam { return p.params }

// Codec returns the port's payload codec.
func (p *SourcePort[T]) Codec() codex.Codec[T] { return p.codec }

// BoundAdapters returns the [SourceAdapter.AdapterName] of every adapter
// bound so far, in Bind order — the real, non-fabricated adapter identities
// this port ingests from. Used by documentation/spec tooling (see
// [PipelineSpec]) instead of hand-typed descriptions.
func (p *SourcePort[T]) BoundAdapters() []string {
	p.adaptersMu.Lock()
	defer p.adaptersMu.Unlock()
	out := make([]string, len(p.adapters))
	copy(out, p.adapters)
	return out
}

// Bind activates a [SourceAdapter] and merges its output into this port's stream.
// Multiple Bind calls produce fan-in: items from all adapters are merged.
//
// Bind is non-blocking — the adapter runs in its own goroutine. Bind must be
// called before [Stream].
func (p *SourcePort[T]) Bind(ctx context.Context, a SourceAdapter[T]) {
	obs := p.obs
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}
	adapterCtx := adapterContext(ctx, p.params, p.handles)

	p.adaptersMu.Lock()
	p.adapters = append(p.adapters, a.AdapterName())
	p.adaptersMu.Unlock()

	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		_ = bindWithObserver(adapterCtx, obs, p.name, a.AdapterName(), func(spanCtx context.Context) error {
			a.Activate(spanCtx, p.ch, p.errCh)
			return nil
		})
	}()
}

// Stream returns the merged [gstream.Stream] from all bound adapters.
// Call after all [Bind] calls. The stream terminates when all adapters exit.
//
// If no adapters were bound, the stream closes immediately (zero items).
//
// Stream should be called at most once.
func (p *SourcePort[T]) Stream(ctx context.Context) gstream.Stream[T] {
	go func() {
		p.wg.Wait()
		close(p.ch)
		close(p.errCh)
	}()
	return gstream.Stream[T]{Values: p.ch, Errors: p.errCh}
}
