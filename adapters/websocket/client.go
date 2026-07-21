package websocket

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/DaniDeer/go-codex/ports"
	"github.com/DaniDeer/go-codex/stats"
	gstream "github.com/DaniDeer/go-codex/stream"
	gorillaws "github.com/gorilla/websocket"
)

// Dialer is the narrow client-side surface: it opens a [Socket] to a ws://
// or wss:// URL. [NewDialer] adapts gorilla/websocket; tests provide fakes.
type Dialer interface {
	Dial(ctx context.Context, url string) (Socket, error)
}

// DialerOptions configures [NewDialer].
type DialerOptions struct {
	// Subprotocols lists requested Sec-WebSocket-Protocol values.
	Subprotocols []string
	// RequestHeader is sent with the upgrade request (auth tokens, …).
	RequestHeader http.Header
	// PingInterval is the client keepalive cadence. Default 30s.
	PingInterval time.Duration
	// ReadLimit caps inbound frame size in bytes. Default 1 MiB.
	ReadLimit int64
}

// NewDialer returns a [Dialer] backed by gorilla/websocket. Dialed
// connections run the same shim-owned keepalive as server connections
// (ping every PingInterval, pong deadline, ReadLimit).
func NewDialer(opts DialerOptions) Dialer {
	if opts.PingInterval <= 0 {
		opts.PingInterval = 30 * time.Second
	}
	if opts.ReadLimit <= 0 {
		opts.ReadLimit = 1 << 20
	}
	return &gorillaDialer{opts: opts}
}

type gorillaDialer struct{ opts DialerOptions }

func (g *gorillaDialer) Dial(ctx context.Context, url string) (Socket, error) {
	d := gorillaws.Dialer{Subprotocols: g.opts.Subprotocols}
	conn, resp, err := d.DialContext(ctx, url, g.opts.RequestHeader)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		return nil, err
	}
	conn.SetReadLimit(g.opts.ReadLimit)
	pongWait := 2 * g.opts.PingInterval
	_ = conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(pongWait))
	})
	s := &gorillaSocket{conn: conn, done: make(chan struct{})}
	go s.keepalive(g.opts.PingInterval)
	return s, nil
}

// DialAdapterOptions configures the Dial*-family adapters.
type DialAdapterOptions struct {
	// MaxBackoff caps the exponential reconnect backoff. Default 30s
	// (initial step 250ms, doubling per consecutive failure, reset after
	// a successful read).
	MaxBackoff time.Duration
	// Observer receives RecordRequest per dial attempt and
	// RecordSubscribe/RecordPublish per frame. Resolved from ctx when nil.
	Observer stats.Observer
}

func (o DialAdapterOptions) maxBackoff() time.Duration {
	if o.MaxBackoff <= 0 {
		return 30 * time.Second
	}
	return o.MaxBackoff
}

const initialBackoff = 250 * time.Millisecond

// dialLoop opens connections until ctx is done, invoking onConn with each
// established socket and its session generation ("c1", "c2", …). Every
// failed attempt AND every drop (onConn return) emits a gap [SocketError]
// via emitErr — consumers KNOW frames may have been lost. Backoff doubles
// per consecutive failure and resets after a connection that delivered at
// least one successful read (reported by onConn).
func dialLoop(
	ctx context.Context,
	dialer Dialer,
	url, path string,
	opts DialAdapterOptions,
	obs stats.Observer,
	emitErr func(error) bool,
	onConn func(sess ports.Session, sock Socket) (hadTraffic bool),
) {
	backoff := initialBackoff
	generation := 0
	for {
		if ctx.Err() != nil {
			return
		}
		start := time.Now()
		sock, err := dialer.Dial(ctx, url)
		if err != nil {
			obs.RecordRequest(http.MethodGet, path, http.StatusServiceUnavailable, time.Since(start))
			if !emitErr(SocketError{Path: path, Op: "dial", Err: err}) {
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff *= 2; backoff > opts.maxBackoff() {
				backoff = opts.maxBackoff()
			}
			continue
		}
		obs.RecordRequest(http.MethodGet, path, http.StatusSwitchingProtocols, time.Since(start))
		generation++
		sess := ports.Session(fmt.Sprintf("c%d", generation))

		hadTraffic := onConn(sess, sock)
		_ = sock.Close()
		if ctx.Err() != nil {
			return
		}
		// Connection dropped — surface the gap (frames may have been lost).
		if !emitErr(SocketError{Path: path, Session: sess, Op: "read",
			Err: fmt.Errorf("connection lost, reconnecting (gap: frames may have been missed)")}) {
			return
		}
		if hadTraffic {
			backoff = initialBackoff
		} else {
			if backoff *= 2; backoff > opts.maxBackoff() {
				backoff = opts.maxBackoff()
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
	}
}

// buildDialURL joins baseURL and the handle's path template expanded with
// vars (declared PathParam codecs validate each value via BuildPath).
func buildDialURL[In, Out any](baseURL string, handle ports.Socket[In, Out], vars map[string]string) (string, error) {
	p, err := handle.Route.BuildPath(vars)
	if err != nil {
		return "", SocketError{Path: handle.Path, Op: "dial", Err: err}
	}
	return baseURL + p, nil
}

// ── DialSourceAdapter ─────────────────────────────────────────────────────────

// DialSourceAdapter returns a [ports.SourceAdapter] that dials an external
// WebSocket endpoint and feeds every decoded inbound frame into the port —
// consume an external feed. baseURL is scheme+host ("ws://host:port" or
// "wss://…"); the path comes from the handle's template expanded with vars.
//
//	domain.Ticks.Bind(ctx, websocket.DialSourceAdapter(
//	    websocket.NewDialer(websocket.DialerOptions{}),
//	    "wss://feed.example.com", map[string]string{"symbol": "ABC"},
//	    handle, websocket.DialAdapterOptions{}))
//
// Reconnect semantics (BY DESIGN — no silent loss): the adapter
// auto-reconnects with exponential backoff; every failed attempt and every
// drop emits a [SocketError] (Op "dial"/"read") on the port's Errors
// channel, and the session generation ("c1", "c2", …) advances per
// connection.
func DialSourceAdapter[T any](
	dialer Dialer,
	baseURL string,
	vars map[string]string,
	handle ports.Socket[T, struct{}],
	opts DialAdapterOptions,
) ports.SourceAdapter[T] {
	return &wsDialSourceAdapter[T]{dialer: dialer, baseURL: baseURL, vars: vars, handle: handle, opts: opts}
}

type wsDialSourceAdapter[T any] struct {
	dialer  Dialer
	baseURL string
	vars    map[string]string
	handle  ports.Socket[T, struct{}]
	opts    DialAdapterOptions
}

func (a *wsDialSourceAdapter[T]) AdapterName() string { return "websocket.DialSourceAdapter" }

func (a *wsDialSourceAdapter[T]) Activate(ctx context.Context, dst chan<- T, errs chan<- error) {
	obs := a.opts.Observer
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}
	emitErr := func(err error) bool {
		select {
		case errs <- err:
			return true
		case <-ctx.Done():
			return false
		}
	}
	url, err := buildDialURL(a.baseURL, a.handle, a.vars)
	if err != nil {
		emitErr(err)
		return
	}
	dialLoop(ctx, a.dialer, url, a.handle.Path, a.opts, obs, emitErr,
		func(sess ports.Session, sock Socket) bool {
			traffic := false
			closeOnCancel(ctx, sock, func() {
				readLoop(sock, a.handle.Path, sess, a.handle.InFormat, obs, nil,
					func(v T) bool {
						traffic = true
						select {
						case dst <- v:
							return true
						case <-ctx.Done():
							return false
						}
					},
					emitErr)
			})
			return traffic
		})
}

// closeOnCancel runs fn, closing sock when ctx is cancelled first (which
// unblocks a blocked ReadMessage).
func closeOnCancel(ctx context.Context, sock Socket, fn func()) {
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = sock.Close()
		case <-done:
		}
	}()
	fn()
	close(done)
}

// ── DialSinkAdapter ───────────────────────────────────────────────────────────

// DialSinkAdapter returns a [ports.SinkAdapter] that dials an external
// WebSocket endpoint and publishes every port item as a frame. Frames that
// arrive while the connection is down are DROPPED with a [SocketError]
// wrapping [ErrFrameDropped] (consistent with the server-side slow-client
// policy); the drop is reported via OnError-style stream error emission to
// the observer only — SinkAdapter has no error channel, so gaps surface
// through RecordPublish(success=false).
func DialSinkAdapter[T any](
	dialer Dialer,
	baseURL string,
	vars map[string]string,
	handle ports.Socket[struct{}, T],
	opts DialAdapterOptions,
) ports.SinkAdapter[T] {
	return &wsDialSinkAdapter[T]{dialer: dialer, baseURL: baseURL, vars: vars, handle: handle, opts: opts}
}

type wsDialSinkAdapter[T any] struct {
	dialer  Dialer
	baseURL string
	vars    map[string]string
	handle  ports.Socket[struct{}, T]
	opts    DialAdapterOptions
}

func (a *wsDialSinkAdapter[T]) AdapterName() string { return "websocket.DialSinkAdapter" }

func (a *wsDialSinkAdapter[T]) Activate(ctx context.Context, src gstream.Stream[T]) {
	obs := a.opts.Observer
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}
	url, err := buildDialURL(a.baseURL, a.handle, a.vars)
	if err != nil {
		obs.RecordPublish(a.handle.Path, false, 0)
		gstream.Drain(ctx, src, func(context.Context, T) error { return nil }, nil,
			gstream.DrainOptions{Observer: obs}) // drain contract even on config error
		return
	}

	// Connection manager: current socket swapped by a background dial loop.
	conn := newManagedConn(ctx, a.dialer, url, a.handle.Path, a.opts, obs)
	conn.startWriteOnly()
	defer conn.stop()

	gstream.Drain(ctx, src,
		func(_ context.Context, v T) error {
			start := time.Now()
			data, err := a.handle.OutFormat.Marshal(v)
			if err != nil {
				stats.ReportErrors(obs, "payload", err)
				obs.RecordPublish(a.handle.Path, false, time.Since(start))
				return nil
			}
			if err := conn.write(data); err != nil {
				obs.RecordPublish(a.handle.Path, false, time.Since(start))
				return nil
			}
			obs.RecordPublish(a.handle.Path, true, time.Since(start))
			return nil
		},
		nil,
		gstream.DrainOptions{Observer: obs})
}

// ── DialDuplexAdapter ─────────────────────────────────────────────────────────

// DialDuplexAdapter returns a [ports.DuplexAdapter] that maintains ONE dialed
// connection to an external duplex endpoint: inbound frames arrive tagged
// with the current session generation ("c1", "c2", … — a generation CHANGE
// is the visible reconnect-gap marker); outbound frames go to the live
// connection regardless of their Session value (there is only one peer) and
// are DROPPED with [ErrFrameDropped] while the connection is down.
//
//	must0(domain.Upstream.Bind(ctx, websocket.DialDuplexAdapter(
//	    websocket.NewDialer(websocket.DialerOptions{}),
//	    "wss://partner.example.com", map[string]string{"room": "ops"},
//	    handle, websocket.DialAdapterOptions{})))
func DialDuplexAdapter[In, Out any](
	dialer Dialer,
	baseURL string,
	vars map[string]string,
	handle ports.Socket[In, Out],
	opts DialAdapterOptions,
) ports.DuplexAdapter[In, Out] {
	return &wsDialDuplexAdapter[In, Out]{dialer: dialer, baseURL: baseURL, vars: vars, handle: handle, opts: opts}
}

type wsDialDuplexAdapter[In, Out any] struct {
	dialer  Dialer
	baseURL string
	vars    map[string]string
	handle  ports.Socket[In, Out]
	opts    DialAdapterOptions
}

func (a *wsDialDuplexAdapter[In, Out]) AdapterName() string { return "websocket.DialDuplexAdapter" }

func (a *wsDialDuplexAdapter[In, Out]) Activate(
	ctx context.Context,
	dst chan<- ports.Framed[In],
	errs chan<- error,
	src gstream.Stream[ports.Framed[Out]],
) error {
	obs := a.opts.Observer
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}
	emitErr := func(err error) bool {
		select {
		case errs <- err:
			return true
		case <-ctx.Done():
			return false
		}
	}
	url, err := buildDialURL(a.baseURL, a.handle, a.vars)
	if err != nil {
		emitErr(err)
		<-ctx.Done()
		return nil
	}

	conn := newManagedConn(ctx, a.dialer, url, a.handle.Path, a.opts, obs)
	defer conn.stop()

	// Inbound: forward frames from whichever connection is live.
	inboundDone := make(chan struct{})
	go func() {
		defer close(inboundDone)
		conn.readEach(func(sess ports.Session, sock Socket) bool {
			traffic := false
			readLoop(sock, a.handle.Path, sess, a.handle.InFormat, obs, nil,
				func(v In) bool {
					traffic = true
					select {
					case dst <- ports.Framed[In]{Session: sess, Payload: v}:
						return true
					case <-ctx.Done():
						return false
					}
				},
				emitErr)
			return traffic
		}, emitErr)
	}()

	// Outbound: drain src, writing to the live connection.
	valCh, errCh := src.Values, src.Errors
	for valCh != nil || errCh != nil {
		select {
		case <-ctx.Done():
			valCh, errCh = nil, nil
		case frame, ok := <-valCh:
			if !ok {
				valCh = nil
				continue
			}
			start := time.Now()
			data, err := a.handle.OutFormat.Marshal(frame.Payload)
			if err != nil {
				stats.ReportErrors(obs, "payload", err)
				obs.RecordPublish(a.handle.Path, false, time.Since(start))
				if !emitErr(SocketError{Path: a.handle.Path, Session: frame.Session, Op: "write", Err: err}) {
					valCh, errCh = nil, nil
				}
				continue
			}
			if err := conn.write(data); err != nil {
				obs.RecordPublish(a.handle.Path, false, time.Since(start))
				if !emitErr(SocketError{Path: a.handle.Path, Session: frame.Session, Op: "write", Err: err}) {
					valCh, errCh = nil, nil
				}
				continue
			}
			obs.RecordPublish(a.handle.Path, true, time.Since(start))
		case e, ok := <-errCh:
			if !ok {
				errCh = nil
				continue
			}
			if !emitErr(e) {
				valCh, errCh = nil, nil
			}
		}
	}

	<-ctx.Done()
	conn.stop()
	<-inboundDone
	return nil
}

// ── managed connection (shared by DialSink/DialDuplex) ───────────────────────

// managedConn owns the reconnecting dialed connection: readEach drives the
// dial loop and hands each live socket to the reader; write targets the
// current socket, failing with ErrFrameDropped while down.
type managedConn struct {
	ctx    context.Context
	cancel context.CancelFunc
	dialer Dialer
	url    string
	path   string
	opts   DialAdapterOptions
	obs    stats.Observer

	sockCh chan Socket // current live socket (capacity 1; empty = down)
}

func newManagedConn(ctx context.Context, dialer Dialer, url, path string, opts DialAdapterOptions, obs stats.Observer) *managedConn {
	cctx, cancel := context.WithCancel(ctx)
	m := &managedConn{ctx: cctx, cancel: cancel, dialer: dialer, url: url, path: path,
		opts: opts, obs: obs, sockCh: make(chan Socket, 1)}
	return m
}

// readEach runs the dial loop, exposing each live socket for write() and
// invoking read for the connection's lifetime. Blocks until ctx is done.
func (m *managedConn) readEach(read func(ports.Session, Socket) bool, emitErr func(error) bool) {
	dialLoop(m.ctx, m.dialer, m.url, m.path, m.opts, m.obs, emitErr,
		func(sess ports.Session, sock Socket) bool {
			m.sockCh <- sock
			var traffic bool
			closeOnCancel(m.ctx, sock, func() {
				traffic = read(sess, sock)
			})
			// Connection ended — remove it from the write slot (if still there).
			select {
			case <-m.sockCh:
			default:
			}
			return traffic
		})
}

// startWriteOnly runs the dial loop for write-only use (DialSinkAdapter —
// inbound frames are discarded; the read loop only detects disconnects).
func (m *managedConn) startWriteOnly() {
	go m.readEach(func(_ ports.Session, sock Socket) bool {
		for {
			if _, err := sock.ReadMessage(); err != nil {
				return true // treat any completed connection as having had traffic
			}
		}
	}, func(error) bool { return m.ctx.Err() == nil }) // gaps observable via RecordPublish failures
}

// write sends data on the live connection, or fails with ErrFrameDropped
// while the connection is down.
func (m *managedConn) write(data []byte) error {
	select {
	case sock := <-m.sockCh:
		err := sock.WriteMessage(data)
		// Return the socket to the slot for the next write.
		select {
		case m.sockCh <- sock:
		default:
		}
		if err != nil {
			return SocketError{Path: m.path, Op: "write", Err: err}
		}
		return nil
	default:
		return SocketError{Path: m.path, Op: "write", Err: ErrFrameDropped}
	}
}

func (m *managedConn) stop() { m.cancel() }
