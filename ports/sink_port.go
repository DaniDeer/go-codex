package ports

import (
	"context"
	"sync"

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
//	file.DrainWriteFileAdapter, sql.DrainInsertAdapter
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
	name    string
	codec   codex.Codec[T]
	params  []IOParam
	handles map[string]any
	specs   map[string]any
	obs     stats.Observer
	buffer  int

	mu    sync.Mutex
	sinks []*boundSink[T]
	wg    sync.WaitGroup
}

// NewSinkPort creates a SinkPort with the given name and payload codec.
// opts configures Patterns, IO params, buffer size, and observer. Any
// [EventPattern] in opts.Patterns is built eagerly into a handle retrievable
// via [EventHandle].
func NewSinkPort[T any](name string, codec codex.Codec[T], opts PortOptions) *SinkPort[T] {
	handles, specs := buildEventPatternHandles(opts.Patterns, codec)
	return &SinkPort[T]{
		name:    name,
		codec:   codec,
		params:  opts.Params,
		handles: handles,
		specs:   specs,
		obs:     opts.Observer,
		buffer:  opts.Buffer,
	}
}

// patternHandle implements the unexported patternHolder interface used by
// [RESTHandle], [EventHandle], [ReqReplyHandle], and [MCPHandle].
func (p *SinkPort[T]) patternHandle(kind string) (any, bool) {
	v, ok := p.handles[kind]
	return v, ok
}

// patternSpec implements the unexported patternHolder interface used by
// [RegisterREST], [RegisterEvent], [RegisterReqReply], and [RegisterMCP].
func (p *SinkPort[T]) patternSpec(kind string) (any, bool) {
	v, ok := p.specs[kind]
	return v, ok
}

// Name returns the port's declared name.
func (p *SinkPort[T]) Name() string { return p.name }

// Params returns the port's declared [IOParam] slice.
func (p *SinkPort[T]) Params() []IOParam { return p.params }

// Codec returns the port's payload codec.
func (p *SinkPort[T]) Codec() codex.Codec[T] { return p.codec }

// Bind registers a [SinkAdapter] to receive items from this port. The adapter's
// Activate goroutine starts immediately. Multiple Bind calls produce fan-out.
// Bind must be called before [Feed].
func (p *SinkPort[T]) Bind(ctx context.Context, a SinkAdapter[T]) {
	ch := make(chan T, p.buffer)
	errCh := make(chan error, p.buffer)
	bs := &boundSink[T]{adapter: a, ch: ch, errCh: errCh}

	p.mu.Lock()
	p.sinks = append(p.sinks, bs)
	p.mu.Unlock()

	obs := p.obs
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}
	adapterCtx := WithParams(ctx, p.params)

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
func (p *SinkPort[T]) Feed(ctx context.Context, src gstream.Stream[T]) {
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
