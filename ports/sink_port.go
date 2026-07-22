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

// SinkAdapter[T] is an adapter that consumes items from a [SinkPort].
//
// Activate runs until src terminates (Values channel closed) or ctx is cancelled.
// The adapter must drain src — ignoring items blocks the pipeline.
//
// Implemented by transport binding constructors:
//
//	mqtt5.PublishAdapter, mqtt.PublishAdapter, nethttp.SSEAdapter,
//	nethttp.DrainCallAdapter, zeromq.PublishAdapter, file.DrainWriteAdapter,
//	file.DrainWriteFileAdapter, sql.DrainInsertAdapter, redis.DrainSetAdapter,
//	websocket.BroadcastSocketAdapter
type SinkAdapter[T any] interface {
	// Activate consumes src until it terminates or ctx is cancelled.
	Activate(ctx context.Context, src gstream.Stream[T])
	// AdapterName returns a descriptor for observability.
	AdapterName() string
}

// boundSink holds the per-adapter channel pair used for fan-out delivery.
type boundSink[T any] struct {
	adapter SinkAdapter[T]
	ch      chan T
	errCh   chan error
}

// SinkPort[T] is a typed, protocol-agnostic outbound IO enforcement point.
// It represents a pipeline → external boundary.
//
// Declare in domain/pipeline code. Bind one or more [SinkAdapter]s in main.go.
// Multiple adapters produce fan-out: every item from [Feed] is broadcast to all
// bound adapters. A failure in one adapter does not stop delivery to others.
//
//	// domain/pipeline.go
//	var OEEResults = ports.NewSinkPort[OEE]("oee-results", OEECodec, ports.PortOptions{Buffer: 8})
//
//	// main.go
//	domain.OEEResults.Bind(ctx, mqtt5.PublishAdapter(client, alertHandle, fmt, publishOpts))
//	domain.OEEResults.Bind(ctx, nethttp.SSEAdapter(mux, sseHandle, sseOpts)) // fan-out
//
// Lifecycle:
//  1. [NewSinkPort] — declare port.
//  2. [Bind] — register adapters (fan-out sinks). All Bind calls before Feed.
//  3. [Feed] — connect the upstream stream; blocks until src terminates AND
//     all adapter goroutines have finished draining their per-adapter channels.
type SinkPort[T any] struct {
	name   string
	codec  codex.Codec[T]
	params []IOParam
	obs    stats.Observer
	buffer int

	// eventBuilder/restBuilder are stored (not used eagerly) so
	// PluginEventPattern/PluginRESTPattern/PluginFilePattern/
	// PluginCachePattern can register against the SAME shared builder
	// every other Pattern-carrying declaration in the service uses.
	eventBuilder *events.Builder
	restBuilder  *rest.Builder

	handlesMu sync.Mutex
	handles   map[string]any
	specs     map[string]any

	mu       sync.Mutex
	sinks    []*boundSink[T]
	adapters []string // AdapterName() of every bound SinkAdapter, in Bind order
	wg       sync.WaitGroup

	// Push lifecycle (Start/Push/Close) — mutually exclusive with Feed.
	feedMu   sync.RWMutex
	feedMode feedMode
	pushCh   chan T
	pushDone chan struct{}
}

// feedMode tracks which feed style owns the port.
type feedMode int

const (
	feedModeNone   feedMode = iota
	feedModeStream          // Feed(ctx, src) — one-shot, stream-driven
	feedModePush            // Start/Push/Close — long-lived, request-driven
	feedModeClosed          // Close called
)

// NewSinkPort creates a SinkPort with the given name and payload codec.
// opts configures IO params, buffer size, observer, and (optionally) shared
// RESTBuilder/EventBuilder references for later [PluginEventPattern]/
// [PluginRESTPattern]/[PluginFilePattern]/[PluginCachePattern] calls —
// declare the port's communication Pattern separately (see [PortOptions]).
func NewSinkPort[T any](name string, codec codex.Codec[T], opts PortOptions) (*SinkPort[T], error) {
	return &SinkPort[T]{
		name:         name,
		codec:        codec,
		params:       opts.Params,
		obs:          opts.Observer,
		buffer:       opts.Buffer,
		eventBuilder: opts.EventBuilder,
		restBuilder:  opts.RESTBuilder,
		handles:      map[string]any{},
		specs:        map[string]any{},
	}, nil
}

// pluginPattern is the shared engine behind every PluginXxxPattern method —
// see [SourcePort.pluginPattern] for the full rationale (identical here,
// role fixed to roleSink).
func (p *SinkPort[T]) pluginPattern(pattern Pattern, kind string) (any, error) {
	p.handlesMu.Lock()
	if _, exists := p.handles[kind]; exists {
		p.handlesMu.Unlock()
		return nil, PatternRegisterError{Port: p.name, Kind: kind, Err: fmt.Errorf("pattern of kind %q already plugged in", kind)}
	}
	p.handlesMu.Unlock()

	handles, specs, err := buildEventPatternHandles(p.name, []Pattern{pattern}, p.codec, roleSink, p.eventBuilder, p.restBuilder)
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
// [NewSinkPort]'s [PortOptions], or a private single-use builder if none)
// and returns the resulting [events.ChannelHandle] directly — bind a
// [SinkAdapter] to it (e.g. mqtt5.PublishAdapter) immediately after.
func (p *SinkPort[T]) PluginEventPattern(pattern EventPattern) (*events.ChannelHandle[T], error) {
	v, err := p.pluginPattern(pattern, patternKindEvent)
	if err != nil {
		return nil, err
	}
	h, _ := v.(*events.ChannelHandle[T])
	return h, nil
}

// PluginRESTPattern registers pattern as an SSE route (events are the port's
// payload, requests carry no body) and returns the resulting
// [rest.SSERouteHandle] directly — bind e.g. nethttp.SSEAdapter to it.
func (p *SinkPort[T]) PluginRESTPattern(pattern RESTPattern) (*rest.SSERouteHandle[struct{}, T], error) {
	v, err := p.pluginPattern(pattern, patternKindREST)
	if err != nil {
		return nil, err
	}
	h, _ := v.(*rest.SSERouteHandle[struct{}, T])
	return h, nil
}

// PluginFilePattern registers pattern and returns the resulting [File]
// directly — bind e.g. file.DrainWriteFileAdapter to it.
func (p *SinkPort[T]) PluginFilePattern(pattern FilePattern) (File[T], error) {
	v, err := p.pluginPattern(pattern, patternKindFile)
	if err != nil {
		return File[T]{}, err
	}
	h, _ := v.(File[T])
	return h, nil
}

// PluginCachePattern registers pattern and returns the resulting [Cache]
// directly — bind e.g. redis's cache adapter to it.
func (p *SinkPort[T]) PluginCachePattern(pattern CachePattern) (Cache[T], error) {
	v, err := p.pluginPattern(pattern, patternKindCache)
	if err != nil {
		return Cache[T]{}, err
	}
	h, _ := v.(Cache[T])
	return h, nil
}

// PluginSQLPattern registers pattern's Table/Op metadata — SQLPattern builds
// no handle (metadata-only; retrieve it later via [SQLMeta], or rely on
// [Bind]'s automatic [WithSQLMeta] propagation to the bound adapter's
// context). Returns an error only if pattern was already plugged in.
func (p *SinkPort[T]) PluginSQLPattern(pattern SQLPattern) error {
	_, err := p.pluginPattern(pattern, patternKindSQL)
	return err
}

// PluginSocketPattern registers pattern as an outbound-only socket (the
// port sends T frames; it never receives) and returns the resulting
// [Socket][struct{}, T] directly.
func (p *SinkPort[T]) PluginSocketPattern(pattern SocketPattern) (Socket[struct{}, T], error) {
	v, err := p.pluginPattern(pattern, patternKindSocket)
	if err != nil {
		return Socket[struct{}, T]{}, err
	}
	h, _ := v.(Socket[struct{}, T])
	return h, nil
}

// patternHandle implements the unexported patternHolder interface used
// internally by [SQLMeta] (see [SourcePort.patternHandle] for rationale).
func (p *SinkPort[T]) patternHandle(kind string) (any, bool) {
	p.handlesMu.Lock()
	defer p.handlesMu.Unlock()
	v, ok := p.handles[kind]
	return v, ok
}

// patternSpec implements the unexported patternHolder interface used by
// [RegisterREST], [RegisterEvent], [RegisterReqReply], and [RegisterMCP].
func (p *SinkPort[T]) patternSpec(kind string) (any, bool) {
	p.handlesMu.Lock()
	defer p.handlesMu.Unlock()
	v, ok := p.specs[kind]
	return v, ok
}

// Name returns the port's declared name.
func (p *SinkPort[T]) Name() string { return p.name }

// Params returns the port's declared [IOParam] slice.
func (p *SinkPort[T]) Params() []IOParam { return p.params }

// Codec returns the port's payload codec.
func (p *SinkPort[T]) Codec() codex.Codec[T] { return p.codec }

// BoundAdapters returns the [SinkAdapter.AdapterName] of every adapter bound
// so far, in Bind order — the real, non-fabricated adapter identities this
// port delivers to. Used by documentation/spec tooling (see [PipelineSpec])
// instead of hand-typed descriptions.
func (p *SinkPort[T]) BoundAdapters() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, len(p.adapters))
	copy(out, p.adapters)
	return out
}

// Bind registers a [SinkAdapter] to receive items from this port. The adapter's
// Activate goroutine starts immediately. Multiple Bind calls produce fan-out.
// Bind must be called before [Feed].
func (p *SinkPort[T]) Bind(ctx context.Context, a SinkAdapter[T]) {
	ch := make(chan T, p.buffer)
	errCh := make(chan error, p.buffer)
	bs := &boundSink[T]{adapter: a, ch: ch, errCh: errCh}

	p.mu.Lock()
	p.sinks = append(p.sinks, bs)
	p.adapters = append(p.adapters, a.AdapterName())
	p.mu.Unlock()

	obs := p.obs
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}
	adapterCtx := adapterContext(ctx, p.params, p.handles)

	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		_ = bindWithObserver(adapterCtx, obs, p.name, a.AdapterName(), func(spanCtx context.Context) error {
			a.Activate(spanCtx, gstream.Stream[T]{Values: ch, Errors: errCh})
			return nil
		})
	}()
}

// Feed connects an upstream [gstream.Stream] to this port, broadcasting each
// item to all bound adapters. Upstream stream errors are forwarded to all
// adapters. Blocks until src terminates or ctx is cancelled.
//
// Call in a goroutine when the pipeline must continue concurrently:
//
//	go domain.OEEResults.Feed(ctx, oeeStream)
//
// When Feed returns it closes all per-adapter channels, signalling each
// adapter's Activate to stop.
//
// Feed is mutually exclusive with the [SinkPort.Start]/[SinkPort.Push]/
// [SinkPort.Close] lifecycle: a port is either stream-fed (Feed) or
// request-fed (Push), never both.
func (p *SinkPort[T]) Feed(ctx context.Context, src gstream.Stream[T]) {
	p.feedMu.Lock()
	if p.feedMode == feedModeNone {
		p.feedMode = feedModeStream
	}
	p.feedMu.Unlock()
	p.feed(ctx, src)
}

// feed is the shared broadcast/drain path used by both Feed and Start.
func (p *SinkPort[T]) feed(ctx context.Context, src gstream.Stream[T]) {
	p.mu.Lock()
	sinks := p.sinks
	p.mu.Unlock()

	obs := p.obs
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}

	gstream.Drain(ctx, src,
		func(_ context.Context, v T) error {
			for _, s := range sinks {
				select {
				case s.ch <- v:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			return nil
		},
		func(e error) {
			for _, s := range sinks {
				select {
				case s.errCh <- e:
				default:
				}
			}
		},
		gstream.DrainOptions{Observer: obs},
	)

	for _, s := range sinks {
		close(s.ch)
		close(s.errCh)
	}
	// Wait for all adapter goroutines to finish draining their channels.
	// This ensures Feed blocks until all items have been delivered end-to-end.
	p.wg.Wait()
}

// Start begins the request-driven feed lifecycle: it creates a port-owned
// channel and drains it through the same broadcast path as [SinkPort.Feed],
// in a background goroutine. Items are submitted with [SinkPort.Push] and the
// lifecycle ends with [SinkPort.Close].
//
// Start replaces the hand-rolled pattern of a channel + go Feed(ctx,
// gstream.From(ctx, ch)) + done-channel. ctx should be a long-lived context
// (it bounds the drain goroutine, not any single Push).
//
// Start is mutually exclusive with Feed and must be called at most once;
// violations are reported via [PortNotStartedError] from Push (Start itself
// is a no-op if the port is already owned).
func (p *SinkPort[T]) Start(ctx context.Context) {
	p.feedMu.Lock()
	if p.feedMode != feedModeNone {
		p.feedMu.Unlock()
		return
	}
	p.feedMode = feedModePush
	p.pushCh = make(chan T, p.buffer)
	p.pushDone = make(chan struct{})
	ch, done := p.pushCh, p.pushDone
	p.feedMu.Unlock()

	go func() {
		defer close(done)
		p.feed(ctx, gstream.From(ctx, ch))
	}()
}

// Push submits one item to all bound adapters through the running Start
// lifecycle. It blocks until the item is accepted (backpressure, consistent
// with Feed's Drain semantics) or ctx is cancelled.
//
// Returns [PortNotStartedError] before [SinkPort.Start], after
// [SinkPort.Close], or when the port is Feed-driven; returns ctx.Err() when
// cancelled while blocked.
func (p *SinkPort[T]) Push(ctx context.Context, v T) error {
	// Hold the read lock for the duration of the send: Close takes the write
	// lock, so it waits for in-flight Push calls before closing the channel —
	// no send-on-closed-channel race. The drain goroutine keeps consuming, so
	// a blocked Push always progresses (or unblocks via ctx).
	p.feedMu.RLock()
	defer p.feedMu.RUnlock()
	if p.feedMode != feedModePush {
		return PortNotStartedError{Port: p.name, Op: "push"}
	}
	select {
	case p.pushCh <- v:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close ends the Start lifecycle: it waits for in-flight [SinkPort.Push]
// calls, closes the port-owned channel, and blocks until the drain goroutine
// and all bound adapters have finished. Push calls after Close return
// [PortNotStartedError]. Close is a no-op if Start was never called.
func (p *SinkPort[T]) Close() error {
	p.feedMu.Lock()
	if p.feedMode != feedModePush {
		p.feedMu.Unlock()
		return nil
	}
	p.feedMode = feedModeClosed
	close(p.pushCh)
	done := p.pushDone
	p.feedMu.Unlock()

	<-done
	return nil
}
