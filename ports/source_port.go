package ports

import (
	"context"
	"fmt"
	"sync"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/api/rest"
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
	name   string
	codec  codex.Codec[T]
	params []IOParam
	obs    stats.Observer
	buffer int

	// eventBuilder/restBuilder are stored (not used eagerly — construction
	// builds no handle) so PluginEventPattern/PluginRESTPattern/
	// PluginFilePattern can register against the SAME shared builder every
	// other Pattern-carrying declaration in the service uses.
	eventBuilder *events.Builder
	restBuilder  *rest.Builder

	handlesMu sync.Mutex
	handles   map[string]any
	specs     map[string]any

	ch    chan T
	errCh chan error
	wg    sync.WaitGroup

	adaptersMu sync.Mutex
	adapters   []string // AdapterName() of every bound SourceAdapter, in Bind order
}

// NewSourcePort creates a SourcePort with the given name and payload codec.
// opts configures IO params, buffer size, observer, and (optionally) shared
// RESTBuilder/EventBuilder references for later [PluginEventPattern]/
// [PluginRESTPattern]/[PluginFilePattern] calls — declare the port's
// communication Pattern separately, at whatever point in your wiring code
// makes sense (see [PortOptions]).
func NewSourcePort[T any](name string, codec codex.Codec[T], opts PortOptions) (*SourcePort[T], error) {
	return &SourcePort[T]{
		name:         name,
		codec:        codec,
		params:       opts.Params,
		obs:          opts.Observer,
		buffer:       opts.Buffer,
		eventBuilder: opts.EventBuilder,
		restBuilder:  opts.RESTBuilder,
		handles:      map[string]any{},
		specs:        map[string]any{},
		ch:           make(chan T, opts.Buffer),
		errCh:        make(chan error, opts.Buffer),
	}, nil
}

// pluginPattern is the shared engine behind every PluginXxxPattern method:
// build ONE pattern's handle via the existing multi-pattern dispatch (called
// with a single-element slice), merge the result into this port's handle/spec
// storage, and hand back the raw value for the specific kind just plugged
// in. Returns [PatternRegisterError] if kind was already plugged in (a
// second Plugin call for the same Pattern kind is a programming error, not a
// runtime condition) or if the Pattern itself fails to build.
func (p *SourcePort[T]) pluginPattern(pattern Pattern, kind string) (any, error) {
	p.handlesMu.Lock()
	if _, exists := p.handles[kind]; exists {
		p.handlesMu.Unlock()
		return nil, PatternRegisterError{Port: p.name, Kind: kind, Err: fmt.Errorf("pattern of kind %q already plugged in", kind)}
	}
	p.handlesMu.Unlock()

	handles, specs, err := buildEventPatternHandles(p.name, []Pattern{pattern}, p.codec, roleSource, p.eventBuilder, p.restBuilder)
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

// PluginEventPattern registers pattern (against the EventBuilder supplied to
// [NewSourcePort]'s [PortOptions], or a private single-use builder if none)
// and returns the resulting [events.ChannelHandle] directly — bind a
// [SourceAdapter] to it (e.g. mqtt5.SubscribeAdapter) immediately after.
func (p *SourcePort[T]) PluginEventPattern(pattern EventPattern) (*events.ChannelHandle[T], error) {
	v, err := p.pluginPattern(pattern, patternKindEvent)
	if err != nil {
		return nil, err
	}
	h, _ := v.(*events.ChannelHandle[T])
	return h, nil
}

// PluginRESTPattern registers pattern as an HTTP ingest route (request body
// = the port's payload, empty response) and returns the resulting
// [rest.RouteHandle] directly — bind e.g. nethttp/chi's IngestAdapter to it.
func (p *SourcePort[T]) PluginRESTPattern(pattern RESTPattern) (*rest.RouteHandle[T, struct{}], error) {
	v, err := p.pluginPattern(pattern, patternKindREST)
	if err != nil {
		return nil, err
	}
	h, _ := v.(*rest.RouteHandle[T, struct{}])
	return h, nil
}

// PluginFilePattern registers pattern and returns the resulting [File]
// directly — bind e.g. file.ScanAdapter/file.WatchAdapter to it.
func (p *SourcePort[T]) PluginFilePattern(pattern FilePattern) (File[T], error) {
	v, err := p.pluginPattern(pattern, patternKindFile)
	if err != nil {
		return File[T]{}, err
	}
	h, _ := v.(File[T])
	return h, nil
}

// PluginSQLPattern registers pattern's Table/Op metadata — SQLPattern builds
// no handle (metadata-only; retrieve it later via [SQLMeta], or rely on
// [Bind]'s automatic [WithSQLMeta] propagation to the bound adapter's
// context). Returns an error only if pattern was already plugged in.
func (p *SourcePort[T]) PluginSQLPattern(pattern SQLPattern) error {
	_, err := p.pluginPattern(pattern, patternKindSQL)
	return err
}

// PluginSocketPattern registers pattern as an inbound-only socket (clients
// send T frames; the port never sends back) and returns the resulting
// [Socket][T, struct{}] directly.
func (p *SourcePort[T]) PluginSocketPattern(pattern SocketPattern) (Socket[T, struct{}], error) {
	v, err := p.pluginPattern(pattern, patternKindSocket)
	if err != nil {
		return Socket[T, struct{}]{}, err
	}
	h, _ := v.(Socket[T, struct{}])
	return h, nil
}

// patternHandle implements the unexported patternHolder interface used
// internally by [SQLMeta] (SQLPattern metadata retrieval — the one
// pattern-lookup accessor NOT superseded by the Plugin model, since
// SQLPattern builds no handle to hand back synchronously).
func (p *SourcePort[T]) patternHandle(kind string) (any, bool) {
	p.handlesMu.Lock()
	defer p.handlesMu.Unlock()
	v, ok := p.handles[kind]
	return v, ok
}

// patternSpec implements the unexported patternHolder interface used by
// [RegisterREST], [RegisterEvent], [RegisterReqReply], and [RegisterMCP].
func (p *SourcePort[T]) patternSpec(kind string) (any, bool) {
	p.handlesMu.Lock()
	defer p.handlesMu.Unlock()
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
