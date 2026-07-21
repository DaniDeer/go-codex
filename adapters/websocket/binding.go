package websocket

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/ports"
	"github.com/DaniDeer/go-codex/stats"
	gstream "github.com/DaniDeer/go-codex/stream"
)

// Mux is the subset of *http.ServeMux the adapters use for route
// registration (Go 1.22 pattern syntax, e.g. "GET /live/{room}").
type Mux interface {
	Handle(pattern string, handler http.Handler)
}

// templateVarRe matches {varName} placeholders in a path template.
var templateVarRe = regexp.MustCompile(`\{([^}]+)\}`)

// upgradeAndValidate performs the shared upgrade path: extract + validate
// path vars against the handle's route, upgrade, and record the request.
// Returns (nil, nil, false) after having written an error response.
func upgradeAndValidate(
	w http.ResponseWriter,
	r *http.Request,
	route interface {
		ValidatePathParams(map[string]string) error
		ValidateQuery(map[string]string) error
		ValidateHeaders(map[string]string) error
	},
	path string,
	upgrader Upgrader,
	obs stats.Observer,
) (Socket, map[string]string, bool) {
	start := time.Now()
	// All template vars (for SessionInfo), not just declared PathParams —
	// route.ValidatePathParams below validates the declared subset.
	vars := map[string]string{}
	for _, m := range templateVarRe.FindAllStringSubmatch(path, -1) {
		vars[m[1]] = r.PathValue(m[1])
	}
	if err := route.ValidatePathParams(vars); err != nil {
		var pe rest.PathParamError
		if errors.As(err, &pe) {
			obs.RecordValidationError("path", stats.ConstraintName(pe.Err), pe.Name)
		}
		obs.RecordRequest(http.MethodGet, path, http.StatusUnprocessableEntity, time.Since(start))
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return nil, nil, false
	}
	query := queryValues(r)
	if err := route.ValidateQuery(query); err != nil {
		stats.ReportErrors(obs, "query", err)
		obs.RecordRequest(http.MethodGet, path, http.StatusUnprocessableEntity, time.Since(start))
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return nil, nil, false
	}
	headers := headerValues(r)
	if err := route.ValidateHeaders(headers); err != nil {
		stats.ReportErrors(obs, "header", err)
		obs.RecordRequest(http.MethodGet, path, http.StatusUnprocessableEntity, time.Since(start))
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return nil, nil, false
	}
	for k, v := range query {
		vars[k] = v
	}
	for k, v := range headers {
		vars[k] = v
	}
	sock, err := upgrader.Upgrade(w, r)
	if err != nil {
		// gorilla writes its own error response on upgrade failure.
		obs.RecordRequest(http.MethodGet, path, http.StatusBadRequest, time.Since(start))
		return nil, nil, false
	}
	obs.RecordRequest(http.MethodGet, path, http.StatusSwitchingProtocols, time.Since(start))
	return sock, vars, true
}

func queryValues(r *http.Request) map[string]string {
	m := make(map[string]string, len(r.URL.Query()))
	for k, vals := range r.URL.Query() {
		if len(vals) > 0 {
			m[k] = vals[0]
		}
	}
	return m
}

func headerValues(r *http.Request) map[string]string {
	m := make(map[string]string, len(r.Header))
	for k, vals := range r.Header {
		if len(vals) > 0 {
			m[k] = vals[0]
		}
	}
	return m
}

// muxPattern renders the Go 1.22 ServeMux pattern for the handle path.
func muxPattern(path string) string {
	if strings.HasPrefix(path, "GET ") {
		return path
	}
	return "GET " + path
}

// ── IngestSocketAdapter ───────────────────────────────────────────────────────

// IngestSocketAdapterOptions configures [IngestSocketAdapter].
type IngestSocketAdapterOptions struct {
	// Observer receives per-connection RecordRequest and per-frame
	// RecordSubscribe events. Resolved from ctx when nil.
	Observer stats.Observer
}

// IngestSocketAdapter returns a [ports.SourceAdapter] that accepts WebSocket
// connections on the handle's path and feeds every decoded inbound frame
// (from ALL connected clients) into the port. The inbound-only socket —
// the server never pushes. Use with [ports.SourcePort.Bind]:
//
//	domain.Commands.Bind(ctx, websocket.IngestSocketAdapter(
//	    mux, hub, websocket.NewUpgrader(websocket.UpgraderOptions{}),
//	    handle, websocket.IngestSocketAdapterOptions{}))
//
// Frame decode/validation failures go to the port's Errors channel as
// [SocketError] (per-field reports with location "payload"); the connection
// stays open — one bad frame does not disconnect a client.
func IngestSocketAdapter[T any](
	mux Mux,
	hub *Hub,
	upgrader Upgrader,
	handle ports.Socket[T, struct{}],
	opts IngestSocketAdapterOptions,
) ports.SourceAdapter[T] {
	return &wsIngestAdapter[T]{mux: mux, hub: hub, upgrader: upgrader, handle: handle, opts: opts}
}

type wsIngestAdapter[T any] struct {
	mux      Mux
	hub      *Hub
	upgrader Upgrader
	handle   ports.Socket[T, struct{}]
	opts     IngestSocketAdapterOptions
}

func (a *wsIngestAdapter[T]) AdapterName() string { return "websocket.IngestSocketAdapter" }

func (a *wsIngestAdapter[T]) Activate(ctx context.Context, dst chan<- T, errs chan<- error) {
	obs := a.opts.Observer
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}
	var wg sync.WaitGroup

	a.mux.Handle(muxPattern(a.handle.Path), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sock, vars, ok := upgradeAndValidate(w, r, a.handle.Route, a.handle.Path, a.upgrader, obs)
		if !ok {
			return
		}
		sess := a.hub.register(sock, vars, nil)
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer a.hub.unregister(sess)
			readLoop(sock, a.handle.Path, sess, a.handle.InFormat, obs,
				func(v T) (T, error) {
					return a.handle.MergeInbound(v, vars)
				},
				func(v T) bool {
					select {
					case dst <- v:
						return true
					case <-ctx.Done():
						return false
					}
				},
				func(err error) bool {
					select {
					case errs <- err:
						return true
					case <-ctx.Done():
						return false
					}
				})
		}()
	}))

	<-ctx.Done()
	a.hub.closeAll() // unblocks all ReadMessage calls
	wg.Wait()        // no sends after Activate returns (R54 contract)
}

// readLoop reads frames until the socket closes or emit/emitErr report ctx
// cancellation. Decode failures are reported and the loop continues.
func readLoop[T any](
	sock Socket,
	path string,
	sess ports.Session,
	f interface{ Unmarshal([]byte) (T, error) },
	obs stats.Observer,
	merge func(T) (T, error),
	emit func(T) bool,
	emitErr func(error) bool,
) {
	for {
		data, err := sock.ReadMessage()
		if err != nil {
			return // peer closed / connection broke / hub closed the socket
		}
		start := time.Now()
		v, err := f.Unmarshal(data)
		if err != nil {
			stats.ReportErrors(obs, "payload", err)
			obs.RecordSubscribe(path, false, time.Since(start))
			if !emitErr(SocketError{Path: path, Session: sess, Op: "read", Err: err}) {
				return
			}
			continue
		}
		if merge != nil {
			v, err = merge(v)
			if err != nil {
				stats.ReportErrors(obs, "payload", err)
				obs.RecordSubscribe(path, false, time.Since(start))
				if !emitErr(SocketError{Path: path, Session: sess, Op: "read", Err: err}) {
					return
				}
				continue
			}
		}
		if !emit(v) {
			return
		}
		obs.RecordSubscribe(path, true, time.Since(start))
	}
}

// ── BroadcastSocketAdapter ────────────────────────────────────────────────────

// BroadcastSocketAdapterOptions configures [BroadcastSocketAdapter].
type BroadcastSocketAdapterOptions struct {
	// OnError receives encode failures, dropped-frame notices
	// ([ErrFrameDropped]), and upstream stream errors.
	OnError func(error)
	// Observer receives per-connection RecordRequest and per-frame
	// RecordPublish events. Resolved from ctx when nil.
	Observer stats.Observer
}

// BroadcastSocketAdapter returns a [ports.SinkAdapter] that pushes every
// port item to ALL connected WebSocket clients — the WS sibling of the SSE
// adapter. Inbound frames from clients are discarded. Use with
// [ports.SinkPort.Bind]:
//
//	domain.Updates.Bind(ctx, websocket.BroadcastSocketAdapter(
//	    mux, hub, websocket.NewUpgrader(websocket.UpgraderOptions{}),
//	    handle, websocket.BroadcastSocketAdapterOptions{}))
//
// Slow clients: a session whose outbound queue is full has the frame
// DROPPED for that session only (reported via OnError as a [SocketError]
// wrapping [ErrFrameDropped]); other sessions are unaffected.
func BroadcastSocketAdapter[T any](
	mux Mux,
	hub *Hub,
	upgrader Upgrader,
	handle ports.Socket[struct{}, T],
	opts BroadcastSocketAdapterOptions,
) ports.SinkAdapter[T] {
	return &wsBroadcastAdapter[T]{mux: mux, hub: hub, upgrader: upgrader, handle: handle, opts: opts}
}

type wsBroadcastAdapter[T any] struct {
	mux      Mux
	hub      *Hub
	upgrader Upgrader
	handle   ports.Socket[struct{}, T]
	opts     BroadcastSocketAdapterOptions
}

func (a *wsBroadcastAdapter[T]) AdapterName() string { return "websocket.BroadcastSocketAdapter" }

func (a *wsBroadcastAdapter[T]) Activate(ctx context.Context, src gstream.Stream[T]) {
	obs := a.opts.Observer
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}
	onErr := func(err error) {
		if a.opts.OnError != nil {
			a.opts.OnError(err)
		}
	}
	var wg sync.WaitGroup

	a.mux.Handle(muxPattern(a.handle.Path), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sock, vars, ok := upgradeAndValidate(w, r, a.handle.Route, a.handle.Path, a.upgrader, obs)
		if !ok {
			return
		}
		sess := a.hub.register(sock, vars, func(s ports.Session, err error) {
			onErr(SocketError{Path: a.handle.Path, Session: s, Op: "write", Err: err})
		})
		wg.Add(1)
		go func() { // discard inbound; detect close to unregister
			defer wg.Done()
			defer a.hub.unregister(sess)
			for {
				if _, err := sock.ReadMessage(); err != nil {
					return
				}
			}
		}()
	}))

	gstream.Drain(ctx, src,
		func(_ context.Context, v T) error {
			start := time.Now()
			success := true
			for _, s := range a.hub.Sessions() {
				out := v
				if len(a.handle.OutMergeFields()) > 0 {
					info, ok := a.hub.SessionInfo(s)
					if !ok {
						success = false
						onErr(SocketError{Path: a.handle.Path, Session: s, Op: "write", Err: errUnknownSession})
						continue
					}
					var err error
					out, err = a.handle.MergeOutbound(out, info)
					if err != nil {
						success = false
						stats.ReportErrors(obs, "payload", err)
						onErr(SocketError{Path: a.handle.Path, Session: s, Op: "write", Err: err})
						continue
					}
				}
				data, err := a.handle.OutFormat.Marshal(out)
				if err != nil {
					success = false
					stats.ReportErrors(obs, "payload", err)
					onErr(SocketError{Path: a.handle.Path, Session: s, Op: "write", Err: err})
					continue
				}
				if sent, known := a.hub.send(s, data); !sent {
					success = false
					err := ErrFrameDropped
					if !known {
						err = errUnknownSession
					}
					onErr(SocketError{Path: a.handle.Path, Session: s, Op: "write", Err: err})
				}
			}
			obs.RecordPublish(a.handle.Path, success, time.Since(start))
			return nil
		},
		onErr,
		gstream.DrainOptions{Observer: obs})

	a.hub.closeAll()
	wg.Wait()
}

// ── DuplexSocketAdapter ───────────────────────────────────────────────────────

// DuplexSocketAdapterOptions configures [DuplexSocketAdapter].
type DuplexSocketAdapterOptions struct {
	// Observer receives per-connection RecordRequest, per-inbound-frame
	// RecordSubscribe, and per-outbound-frame RecordPublish events.
	// Resolved from ctx when nil.
	Observer stats.Observer
}

// DuplexSocketAdapter returns a [ports.DuplexAdapter]: inbound frames from
// every connected client arrive session-tagged on the port's Inbound stream;
// outbound frames fed to the port are delivered to their target session
// (zero Session = broadcast). Use with [ports.DuplexPort.Bind]:
//
//	must0(domain.Live.Bind(ctx, websocket.DuplexSocketAdapter(
//	    mux, hub, websocket.NewUpgrader(websocket.UpgraderOptions{}),
//	    handle, websocket.DuplexSocketAdapterOptions{})))
//
// Query hub.SessionInfo(session) from pipeline code for the upgrade-time
// path vars (e.g. the {room} a session joined). Write failures, unknown
// target sessions, and dropped frames (slow client) surface as
// [SocketError] on the port's Errors channel.
func DuplexSocketAdapter[In, Out any](
	mux Mux,
	hub *Hub,
	upgrader Upgrader,
	handle ports.Socket[In, Out],
	opts DuplexSocketAdapterOptions,
) ports.DuplexAdapter[In, Out] {
	return &wsDuplexAdapter[In, Out]{mux: mux, hub: hub, upgrader: upgrader, handle: handle, opts: opts}
}

type wsDuplexAdapter[In, Out any] struct {
	mux      Mux
	hub      *Hub
	upgrader Upgrader
	handle   ports.Socket[In, Out]
	opts     DuplexSocketAdapterOptions
}

func (a *wsDuplexAdapter[In, Out]) AdapterName() string { return "websocket.DuplexSocketAdapter" }

func (a *wsDuplexAdapter[In, Out]) Activate(
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
	var wg sync.WaitGroup

	a.mux.Handle(muxPattern(a.handle.Path), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sock, vars, ok := upgradeAndValidate(w, r, a.handle.Route, a.handle.Path, a.upgrader, obs)
		if !ok {
			return
		}
		sess := a.hub.register(sock, vars, nil)
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer a.hub.unregister(sess)
			readLoop(sock, a.handle.Path, sess, a.handle.InFormat, obs,
				func(v In) (In, error) {
					return a.handle.MergeInbound(v, vars)
				},
				func(v In) bool {
					select {
					case dst <- ports.Framed[In]{Session: sess, Payload: v}:
						return true
					case <-ctx.Done():
						return false
					}
				},
				emitErr)
		}()
	}))

	// Outbound: drain src until it terminates or ctx is cancelled.
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
			if frame.Session == "" {
				success := true
				for _, s := range a.hub.Sessions() {
					perSession := frame.Payload
					if len(a.handle.OutMergeFields()) > 0 {
						info, ok := a.hub.SessionInfo(s)
						if !ok {
							success = false
							if !emitErr(SocketError{Path: a.handle.Path, Session: s, Op: "write", Err: errUnknownSession}) {
								valCh, errCh = nil, nil
								break
							}
							continue
						}
						var err error
						perSession, err = a.handle.MergeOutbound(perSession, info)
						if err != nil {
							success = false
							stats.ReportErrors(obs, "payload", err)
							if !emitErr(SocketError{Path: a.handle.Path, Session: s, Op: "write", Err: err}) {
								valCh, errCh = nil, nil
								break
							}
							continue
						}
					}
					encoded, err := a.handle.OutFormat.Marshal(perSession)
					if err != nil {
						success = false
						stats.ReportErrors(obs, "payload", err)
						if !emitErr(SocketError{Path: a.handle.Path, Session: s, Op: "write", Err: err}) {
							valCh, errCh = nil, nil
							break
						}
						continue
					}
					if sent, known := a.hub.send(s, encoded); !sent {
						success = false
						err := ErrFrameDropped
						if !known {
							err = errUnknownSession
						}
						if !emitErr(SocketError{Path: a.handle.Path, Session: s, Op: "write", Err: err}) {
							valCh, errCh = nil, nil
							break
						}
					}
				}
				obs.RecordPublish(a.handle.Path, success, time.Since(start))
				continue
			}

			out := frame.Payload
			if len(a.handle.OutMergeFields()) > 0 {
				info, ok := a.hub.SessionInfo(frame.Session)
				if !ok {
					obs.RecordPublish(a.handle.Path, false, time.Since(start))
					if !emitErr(SocketError{Path: a.handle.Path, Session: frame.Session, Op: "write", Err: errUnknownSession}) {
						valCh, errCh = nil, nil
					}
					continue
				}
				merged, mergeErr := a.handle.MergeOutbound(out, info)
				if mergeErr != nil {
					stats.ReportErrors(obs, "payload", mergeErr)
					obs.RecordPublish(a.handle.Path, false, time.Since(start))
					if !emitErr(SocketError{Path: a.handle.Path, Session: frame.Session, Op: "write", Err: mergeErr}) {
						valCh, errCh = nil, nil
					}
					continue
				}
				out = merged
			}
			data, err := a.handle.OutFormat.Marshal(out)
			if err != nil {
				stats.ReportErrors(obs, "payload", err)
				obs.RecordPublish(a.handle.Path, false, time.Since(start))
				if !emitErr(SocketError{Path: a.handle.Path, Session: frame.Session, Op: "write", Err: err}) {
					valCh, errCh = nil, nil
				}
				continue
			}
			if sent, known := a.hub.send(frame.Session, data); !sent {
				err := ErrFrameDropped
				if !known {
					err = errUnknownSession
				}
				if !emitErr(SocketError{Path: a.handle.Path, Session: frame.Session, Op: "write", Err: err}) {
					valCh, errCh = nil, nil
					continue
				}
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

	// Keep serving inbound until ctx is done, then close all sessions.
	<-ctx.Done()
	a.hub.closeAll()
	wg.Wait()
	return nil
}

var errUnknownSession = errors.New("websocket: unknown session")
