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
//	file.WatchAdapter, sql.QueryAdapter
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

	ch    chan T
	errCh chan error
	wg    sync.WaitGroup
}

// NewSourcePort creates a SourcePort with the given name and payload codec.
// opts configures IO params, buffer size, and observer.
func NewSourcePort[T any](name string, codec codex.Codec[T], opts PortOptions) *SourcePort[T] {
	return &SourcePort[T]{
		name:   name,
		codec:  codec,
		params: opts.Params,
		obs:    opts.Observer,
		buffer: opts.Buffer,
		ch:     make(chan T, opts.Buffer),
		errCh:  make(chan error, opts.Buffer),
	}
}

// Name returns the port's declared name.
func (p *SourcePort[T]) Name() string { return p.name }

// Params returns the port's declared [IOParam] slice.
func (p *SourcePort[T]) Params() []IOParam { return p.params }

// Codec returns the port's payload codec.
func (p *SourcePort[T]) Codec() codex.Codec[T] { return p.codec }

// Bind activates a [SourceAdapter] and merges its output into this port's stream.
// Multiple Bind calls produce fan-in: items from all adapters are merged.
//
// Bind is non-blocking — the adapter runs in its own goroutine. Bind must be
// called before [Stream].
func (p *SourcePort[T]) Bind(ctx context.Context, a SourceAdapter[T]) {
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		a.Activate(ctx, p.ch, p.errCh)
	}()
}

// Stream returns the merged [gstream.Stream] from all bound adapters.
// Call after all [Bind] calls. The stream terminates when all adapters exit.
//
// If no adapters were bound, the stream closes immediately (zero items).
//
// Stream should be called at most once.
func (p *SourcePort[T]) Stream(ctx context.Context) gstream.Stream[T] {
	obs := p.obs
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}
	_ = obs

	go func() {
		p.wg.Wait()
		close(p.ch)
		close(p.errCh)
	}()
	return gstream.Stream[T]{Values: p.ch, Errors: p.errCh}
}
