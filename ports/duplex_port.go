package ports

import (
	"context"
	"errors"
	"sync"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/stats"
	gstream "github.com/DaniDeer/go-codex/stream"
)

// Session identifies one connected peer for the lifetime of its connection.
// Adapters mint a unique Session per accepted connection; the pipeline
// echoes it back on outbound [Framed] values to address a targeted reply.
type Session string

// Framed pairs a payload with the session it came from (inbound) or is
// addressed to (outbound). A zero Session on an outbound frame means
// broadcast to all connected sessions.
type Framed[T any] struct {
	Session Session
	Payload T
}

// DuplexAdapter[In, Out] runs a bidirectional session endpoint for a
// [DuplexPort].
//
// Activate runs until ctx is cancelled or src terminates: decoded inbound
// frames go to dst (errors to errs — neither channel may be closed by the
// adapter, the port owns channel lifecycle), and outbound frames are
// consumed from src and delivered to their sessions (zero Session =
// broadcast). The adapter must DRAIN src — ignoring outbound frames blocks
// the pipeline.
//
// Implemented by transport binding constructors:
//
//	websocket.DuplexSocketAdapter
type DuplexAdapter[In, Out any] interface {
	// Activate runs the endpoint until ctx is cancelled or src terminates.
	// Must not close dst or errs.
	Activate(ctx context.Context, dst chan<- Framed[In], errs chan<- error, src gstream.Stream[Framed[Out]]) error
	// AdapterName returns a descriptor for [PortBindError] and observability.
	AdapterName() string
}

// DuplexPort[In, Out] is a typed, protocol-agnostic bidirectional session
// boundary — the sixth port type. External peers send In frames and receive
// Out frames over persistent, identified sessions (WebSocket connections,
// framed TCP conns, …).
//
// Declare in domain/pipeline code; bind exactly ONE [DuplexAdapter] in
// main.go (like [IOPort] — session identity across multiple transports is
// deliberately unsupported).
//
//	// domain — no adapter imports
//	var Live = codex.Must(ports.NewDuplexPort[Command, Update]("live",
//	    commandCodec, updateCodec, ports.PortOptions{
//	        Patterns: []ports.Pattern{ports.SocketPattern{Path: "/live/{room}"}},
//	    }))
//
//	// main.go
//	must0(domain.Live.Bind(ctx, websocket.DuplexSocketAdapter(mux, upgrader, handle, opts)))
//	commands := domain.Live.Inbound(ctx)      // Stream[Framed[Command]]
//	go app.Go("live-feed", func(ctx) error {  // targeted replies + broadcasts
//	    domain.Live.Feed(ctx, updates); return nil
//	})
//
// Session routing in the pipeline composes with the stream routing
// operators: stream.GroupBy by Framed.Session yields per-client
// sub-streams; a stream.Map from Framed[In] to Framed[Out] preserves the
// session for targeted replies.
//
// Lifecycle:
//  1. [NewDuplexPort] — declare port with both codecs.
//  2. [DuplexPort.Bind] — register the single adapter.
//  3. [DuplexPort.Inbound] — obtain the inbound stream.
//  4. [DuplexPort.Feed] — drive outbound frames (blocks until src ends).
type DuplexPort[In, Out any] struct {
	name     string
	inCodec  codex.Codec[In]
	outCodec codex.Codec[Out]
	params   []IOParam
	handles  map[string]any
	specs    map[string]any
	obs      stats.Observer
	buffer   int

	mu      sync.Mutex
	adapter DuplexAdapter[In, Out]

	inCh    chan Framed[In]
	inErrCh chan error
	outCh   chan Framed[Out]
	outErr  chan error
	wg      sync.WaitGroup
}

// NewDuplexPort creates a DuplexPort with the given name and codec pair:
// inCodec validates inbound frames, outCodec outbound frames. Any
// [SocketPattern] in opts.Patterns is built eagerly into a handle
// retrievable via [SocketHandle]. Returns [PatternRegisterError] if a
// declared Pattern fails to build or is not supported on a DuplexPort.
func NewDuplexPort[In, Out any](
	name string,
	inCodec codex.Codec[In],
	outCodec codex.Codec[Out],
	opts PortOptions,
) (*DuplexPort[In, Out], error) {
	handles, specs, err := buildDuplexPatternHandles(name, opts.Patterns, inCodec, outCodec, opts.RESTBuilder)
	if err != nil {
		return nil, err
	}
	return &DuplexPort[In, Out]{
		name:     name,
		inCodec:  inCodec,
		outCodec: outCodec,
		params:   opts.Params,
		handles:  handles,
		specs:    specs,
		obs:      opts.Observer,
		buffer:   opts.Buffer,
		inCh:     make(chan Framed[In], opts.Buffer),
		inErrCh:  make(chan error, opts.Buffer),
		outCh:    make(chan Framed[Out], opts.Buffer),
		outErr:   make(chan error, opts.Buffer),
	}, nil
}

// patternHandle implements the unexported patternHolder interface.
func (p *DuplexPort[In, Out]) patternHandle(kind string) (any, bool) {
	v, ok := p.handles[kind]
	return v, ok
}

// patternSpec implements the unexported patternHolder interface.
func (p *DuplexPort[In, Out]) patternSpec(kind string) (any, bool) {
	v, ok := p.specs[kind]
	return v, ok
}

// Name returns the port's declared name.
func (p *DuplexPort[In, Out]) Name() string { return p.name }

// Params returns the port's declared [IOParam] slice.
func (p *DuplexPort[In, Out]) Params() []IOParam { return p.params }

// InCodec returns the inbound frame codec.
func (p *DuplexPort[In, Out]) InCodec() codex.Codec[In] { return p.inCodec }

// OutCodec returns the outbound frame codec.
func (p *DuplexPort[In, Out]) OutCodec() codex.Codec[Out] { return p.outCodec }

// Bind registers the single [DuplexAdapter] and starts it in a supervised
// goroutine. Exactly one adapter is allowed — a second Bind returns
// [PortBindError].
func (p *DuplexPort[In, Out]) Bind(ctx context.Context, a DuplexAdapter[In, Out]) error {
	p.mu.Lock()
	if p.adapter != nil {
		p.mu.Unlock()
		return PortBindError{Port: p.name, Adapter: a.AdapterName(),
			Err: errors.New("DuplexPort already has an adapter bound; only one adapter is allowed")}
	}
	p.adapter = a
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
			return a.Activate(spanCtx, p.inCh, p.inErrCh,
				gstream.Stream[Framed[Out]]{Values: p.outCh, Errors: p.outErr})
		})
	}()
	return nil
}

// Inbound returns the stream of decoded inbound frames. Call after [Bind].
// The stream terminates when the adapter exits. Inbound should be called at
// most once.
func (p *DuplexPort[In, Out]) Inbound(ctx context.Context) gstream.Stream[Framed[In]] {
	go func() {
		p.wg.Wait()
		close(p.inCh)
		close(p.inErrCh)
	}()
	return gstream.Stream[Framed[In]]{Values: p.inCh, Errors: p.inErrCh}
}

// Feed connects an upstream stream of outbound frames to the bound adapter.
// Frames with a zero Session broadcast to all connected sessions; a non-zero
// Session targets one peer. Blocks until src terminates or ctx is cancelled;
// on return the outbound channels are closed, signalling the adapter that no
// more outbound frames will come.
//
// Call in a goroutine when the pipeline must continue concurrently.
func (p *DuplexPort[In, Out]) Feed(ctx context.Context, src gstream.Stream[Framed[Out]]) {
	defer close(p.outCh)
	defer close(p.outErr)
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
			select {
			case p.outCh <- v:
			case <-ctx.Done():
				return
			}
		case e, ok := <-errCh:
			if !ok {
				errCh = nil
				continue
			}
			select {
			case p.outErr <- e:
			case <-ctx.Done():
				return
			}
		}
	}
}
