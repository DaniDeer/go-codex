package ports

import (
	"context"
	"sync"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/stats"
	gstream "github.com/DaniDeer/go-codex/stream"
)

// PipePort[T] is a named, codec-validated pipeline waypoint — the primary
// tool for segmenting a computation pipeline into named, observable stages.
// It is a thin convenience wrapper over existing primitives ([SourcePort],
// [SinkPort], [gstream.Map]/[gstream.Drain]) — not a new adapter model.
//
// Declare stages flexibly, wire them together, then start them; the
// topology does not change afterward — matching every other port type's
// "declare → bind → start" lifecycle.
//
// # Primary use: computation stage segmentation
//
// Declare PipePorts between pipeline stages and connect them with [Chain]:
//
//	Raw   := codex.Must(ports.NewPipePort[RawData]("raw", rawCodec, ...))
//	Clean := codex.Must(ports.NewPipePort[CleanData]("clean", cleanCodec, ...))
//
//	// Chain wires Raw → validate → Clean in one call (Map+Drain+Push).
//	ports.Chain(ctx, Raw, validate, Clean)
//
//	// Side observer: tap Raw without touching the pipeline.
//	Raw.OutputPort("log").Bind(ctx, ports.ChanSinkAdapter(logCh))
//
//	Raw.Connect(ctx)
//	Clean.Connect(ctx)
//
// # Secondary use: IO/adapter bridging
//
// Wire transport adapters to input/output ports for fan-in/fan-out — same
// declare-then-start rule as any other port:
//
//	ingest := Broadcast.InputPort("from-mqtt")
//	ingest.Bind(ctx, mqtt5.SubscribeAdapter(...))
//	sse := Broadcast.OutputPort("to-sse")
//	sse.Bind(ctx, chi.SSEAdapter(...))
//	Broadcast.Connect(ctx)
//
// Ordering rule (single rule, applies to a given pipe): all [InputPort],
// [OutputPort], [Stream], and [Chain] registrations for a pipe must happen
// before that pipe's [Connect] call. [Push] has no such restriction — it
// may be called at any time; items buffer (up to the configured Buffer
// size) until [Connect] starts draining them.
//
// Zero new adapter interfaces — PipePort reuses [SourcePort] and [SinkPort]
// primitives.
type PipePort[T any] struct {
	name   string
	codec  codex.Codec[T]
	params []IOParam
	obs    stats.Observer
	buffer int

	mu        sync.Mutex
	in        map[string]*SourcePort[T]
	out       map[string]*SinkPort[T]
	streamChs []chan T // registered by Stream() calls before Connect
	connected bool     // guards against double Connect

	// pushCh is allocated eagerly so Push works before Connect (buffers
	// until Connect's consumer goroutine starts draining it).
	pushCh chan T
}

// NewPipePort creates a PipePort with the given name and payload codec.
// opts.Buffer defaults to 8 when <= 0. opts.Patterns are NOT built
// eagerly (the pipe itself generates no schema — schema comes from the
// individual SourcePort/SinkPort ports declared via InputPort/OutputPort).
func NewPipePort[T any](name string, codec codex.Codec[T], opts PortOptions) (*PipePort[T], error) {
	buf := opts.Buffer
	if buf <= 0 {
		buf = 8
	}
	return &PipePort[T]{
		name:   name,
		codec:  codec,
		params: opts.Params,
		obs:    opts.Observer,
		buffer: buf,
		in:     map[string]*SourcePort[T]{},
		out:    map[string]*SinkPort[T]{},
		pushCh: make(chan T, buf),
	}, nil
}

// Name returns the pipe's declared name.
func (p *PipePort[T]) Name() string { return p.name }

// Params returns the pipe's declared [IOParam] slice.
func (p *PipePort[T]) Params() []IOParam { return p.params }

// Codec returns the pipe's payload codec.
func (p *PipePort[T]) Codec() codex.Codec[T] { return p.codec }

// InputPort returns a named [SourcePort] connected to the pipe's internal
// hub. Multiple calls with the same name return the same port. Bind
// [SourceAdapter]s to the returned port; items flow into the pipe.
func (p *PipePort[T]) InputPort(name string) *SourcePort[T] {
	p.mu.Lock()
	defer p.mu.Unlock()
	if sp, ok := p.in[name]; ok {
		return sp
	}
	sp, _ := NewSourcePort[T](p.name+"/in/"+name, p.codec, PortOptions{Buffer: p.buffer, Params: p.params, Observer: p.obs})
	p.in[name] = sp
	return sp
}

// OutputPort returns a named [SinkPort] connected to the pipe's internal
// hub. Multiple calls with the same name return the same port. Bind
// [SinkAdapter]s to the returned port; items flow out of the pipe.
func (p *PipePort[T]) OutputPort(name string) *SinkPort[T] {
	p.mu.Lock()
	defer p.mu.Unlock()
	if sp, ok := p.out[name]; ok {
		return sp
	}
	sp, _ := NewSinkPort[T](p.name+"/out/"+name, p.codec, PortOptions{Buffer: p.buffer, Params: p.params, Observer: p.obs})
	p.out[name] = sp
	return sp
}

// Connect starts the internal hub goroutine that fans items from all input
// ports AND items pushed via [Push] to all output ports and to [Stream]
// consumers. Call after all InputPort/OutputPort registrations, adapter
// Bind calls, and [Stream] calls. The hub runs until ctx is cancelled.
//
// Connect must be called at most once per pipe; a second call is a no-op
// (logged via the observer as a failed "port.bind" event) — the topology is
// fixed once a pipe is connected, matching the "declare, then start, never
// change" contract every port type in this package follows.
func (p *PipePort[T]) Connect(ctx context.Context) {
	obs := p.obs
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}

	p.mu.Lock()
	if p.connected {
		p.mu.Unlock()
		obs.RecordRequest("port.bind", p.name+"/pipe.Connect", 500, 0)
		return
	}
	p.connected = true

	srcs := make([]*SourcePort[T], 0, len(p.in))
	for _, sp := range p.in {
		srcs = append(srcs, sp)
	}

	sinks := make([]*SinkPort[T], 0, len(p.out))
	for _, sp := range p.out {
		sinks = append(sinks, sp)
	}

	streamChs := p.streamChs
	pushCh := p.pushCh
	p.mu.Unlock()

	// Build per-sink channels for fan-out.
	type sinkChan struct {
		ch  chan T
		eCh chan error
	}
	var sinkChans []sinkChan
	for _, s := range sinks {
		ch := make(chan T, p.buffer)
		eCh := make(chan error, p.buffer)
		sinkChans = append(sinkChans, sinkChan{ch: ch, eCh: eCh})
		go func(s *SinkPort[T], ch chan T, eCh chan error) {
			s.Feed(ctx, gstream.From(ctx, ch))
		}(s, ch, eCh)
	}

	// Build list of ALL output channels: sink fan-out + stream consumers.
	allOut := make([]sinkChan, 0, len(sinkChans)+len(streamChs))
	allOut = append(allOut, sinkChans...)
	for _, ch := range streamChs {
		allOut = append(allOut, sinkChan{ch: ch})
	}

	// fanOut sends v to all output channels.
	fanOut := func(v T) {
		for _, oc := range allOut {
			select {
			case oc.ch <- v:
			case <-ctx.Done():
				return
			}
		}
	}
	fanOutErr := func(e error) {
		for _, oc := range allOut {
			if oc.eCh != nil {
				select {
				case oc.eCh <- e:
				default:
				}
			}
		}
	}

	var wg sync.WaitGroup

	// Merge from all InputPort SourcePorts.
	for _, sp := range srcs {
		wg.Add(1)
		go func(sp *SourcePort[T]) {
			defer wg.Done()
			src := sp.Stream(ctx)
			valCh, errCh := src.Values, src.Errors
			for valCh != nil || errCh != nil {
				select {
				case <-ctx.Done():
					return
				case v, ok := <-valCh:
					if !ok {
						valCh = nil
						continue
					}
					obs.RecordSubscribe(p.name, true, 0)
					fanOut(v)
				case e, ok := <-errCh:
					if !ok {
						errCh = nil
						continue
					}
					obs.RecordSubscribe(p.name, false, 0)
					fanOutErr(e)
				}
			}
		}(sp)
	}

	// Listen for pushed items.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case v, ok := <-pushCh:
				if !ok {
					return
				}
				fanOut(v)
			}
		}
	}()

	go func() {
		wg.Wait()
		for _, sc := range sinkChans {
			close(sc.ch)
			close(sc.eCh)
		}
		for _, ch := range streamChs {
			close(ch)
		}
	}()
}

// Push sends one item into the pipe's distribution — it fans out to all
// OutputPort adapters AND [Stream] consumers. Use this to feed the pipe
// from an upstream computation stage (see [Chain] for the common case of
// wiring two PipePorts together).
//
// Push may be called at any time, before or after [Connect]: items buffer
// (up to the configured Buffer size) until Connect's consumer goroutine
// starts draining them. Blocks with backpressure once the buffer is full;
// returns ctx.Err() if ctx is cancelled while blocked.
func (p *PipePort[T]) Push(ctx context.Context, v T) error {
	select {
	case p.pushCh <- v:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Stream returns a [gstream.Stream] of all items flowing through the pipe —
// the primary interface for connecting computation stages between PipePorts.
// Must be called before [Connect]. Most callers should prefer [Chain], which
// wraps Stream+Map+Drain+Push for the common one-transform-per-stage case.
//
//	validated := gstream.Map(ctx, Raw.Stream(ctx), validate, gstream.MapOptions{})
func (p *PipePort[T]) Stream(ctx context.Context) gstream.Stream[T] {
	p.mu.Lock()
	defer p.mu.Unlock()
	ch := make(chan T, p.buffer)
	p.streamChs = append(p.streamChs, ch)
	return gstream.From(ctx, ch)
}

// Chain wires two PipePorts together through a transform function — the
// primary tool for connecting pipeline stages. It is a convenience wrapper
// over [PipePort.Stream], [gstream.Map], [gstream.Drain], and [PipePort.Push]
// — nothing Chain does cannot be written by hand, it just removes the
// boilerplate of repeating that composition once per stage:
//
//	// Without Chain:
//	validated := gstream.Map(ctx, Raw.Stream(ctx), validate, gstream.MapOptions{})
//	go gstream.Drain(ctx, validated, func(_ context.Context, v Validated) error {
//	    return Clean.Push(ctx, v)
//	}, nil, gstream.DrainOptions{})
//
//	// With Chain:
//	ports.Chain(ctx, Raw, validate, Clean)
//
// Chain must be called before from's [PipePort.Connect] (it registers a
// [Stream] consumer). It may be called before or after to's Connect — items
// buffer in to's push channel until to.Connect starts draining them. Errors
// from fn or from the drain loop are silently dropped, matching
// [gstream.Drain]'s nil-onError behavior; observe fn failures inside fn
// itself if that matters to your pipeline.
func Chain[In, Out any](ctx context.Context, from *PipePort[In], fn func(In) (Out, error), to *PipePort[Out]) {
	mapped := gstream.Map(ctx, from.Stream(ctx), fn, gstream.MapOptions{})
	go gstream.Drain(ctx, mapped, func(_ context.Context, v Out) error {
		return to.Push(ctx, v)
	}, nil, gstream.DrainOptions{})
}
