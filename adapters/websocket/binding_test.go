package websocket_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	adapterws "github.com/DaniDeer/go-codex/adapters/websocket"
	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/ports"
	"github.com/DaniDeer/go-codex/stats"
	"github.com/DaniDeer/go-codex/stream"
	"github.com/DaniDeer/go-codex/validate"
	gorillaws "github.com/gorilla/websocket"
)

// ── Fixtures ──────────────────────────────────────────────────────────────────

type command struct {
	Action string
	Value  int
}

var commandCodec = codex.Struct[command](
	codex.RequiredField("action", codex.String().Refine(validate.NonEmptyString),
		func(c command) string { return c.Action },
		func(c *command, v string) { c.Action = v },
	),
	codex.RequiredField("value", codex.Int(),
		func(c command) int { return c.Value },
		func(c *command, v int) { c.Value = v },
	),
)

type update struct {
	Text string
}

var updateCodec = codex.Struct[update](
	codex.RequiredField("text", codex.String(),
		func(u update) string { return u.Text },
		func(u *update, v string) { u.Text = v },
	),
)

func duplexHandle(t *testing.T, path string, opts ...rest.RouteOpt) ports.Socket[command, update] {
	t.Helper()
	port, err := ports.NewDuplexPort[command, update]("live", commandCodec, updateCodec,
		ports.PortOptions{Patterns: []ports.Pattern{ports.SocketPattern{Path: path, Opts: opts}}})
	if err != nil {
		t.Fatalf("port: %v", err)
	}
	h, ok := ports.SocketHandle[command, update](port)
	if !ok {
		t.Fatal("no socket handle")
	}
	return h
}

// fakeSocket is a scripted, in-memory Socket.
type fakeSocket struct {
	mu       sync.Mutex
	inbound  chan []byte // frames the "client" sends
	written  [][]byte    // frames the server wrote to this client
	writeErr error
	closed   bool
}

func newFakeSocket() *fakeSocket {
	return &fakeSocket{inbound: make(chan []byte, 16)}
}

func (f *fakeSocket) ReadMessage() ([]byte, error) {
	data, ok := <-f.inbound
	if !ok {
		return nil, io.EOF
	}
	return data, nil
}

func (f *fakeSocket) WriteMessage(data []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.writeErr != nil {
		return f.writeErr
	}
	f.written = append(f.written, data)
	return nil
}

func (f *fakeSocket) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.closed {
		f.closed = true
		close(f.inbound)
	}
	return nil
}

func (f *fakeSocket) writtenFrames() [][]byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([][]byte, len(f.written))
	copy(out, f.written)
	return out
}

// fakeUpgrader hands out scripted sockets in order.
type fakeUpgrader struct {
	mu    sync.Mutex
	socks []*fakeSocket
	next  int
}

func (f *fakeUpgrader) Upgrade(_ http.ResponseWriter, _ *http.Request) (adapterws.Socket, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.next >= len(f.socks) {
		return nil, errors.New("no more sockets scripted")
	}
	s := f.socks[f.next]
	f.next++
	return s, nil
}

// wsObserver counts observer events.
type wsObserver struct {
	stats.NoopObserver
	mu         sync.Mutex
	requests   []int // status codes
	subOK      int
	subFail    int
	pubOK      int
	pubFail    int
	valdFields []string
}

func (o *wsObserver) RecordRequest(_, _ string, status int, _ time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.requests = append(o.requests, status)
}

func (o *wsObserver) RecordSubscribe(_ string, success bool, _ time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if success {
		o.subOK++
	} else {
		o.subFail++
	}
}

func (o *wsObserver) RecordPublish(_ string, success bool, _ time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if success {
		o.pubOK++
	} else {
		o.pubFail++
	}
}

func (o *wsObserver) RecordValidationError(_, _, field string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.valdFields = append(o.valdFields, field)
}

// connect performs an upgrade request against the registered mux handler.
func connect(t *testing.T, mux *http.ServeMux, target string) {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, target, nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
}

// ── W1: duplex inbound frame, session tag ─────────────────────────────────────

func TestDuplex_InboundFrame_SessionTagged(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mux := http.NewServeMux()
	hub := adapterws.NewHub(0)
	sock := newFakeSocket()
	up := &fakeUpgrader{socks: []*fakeSocket{sock}}

	port, _ := ports.NewDuplexPort[command, update]("live", commandCodec, updateCodec,
		ports.PortOptions{Patterns: []ports.Pattern{ports.SocketPattern{Path: "/live/{room}"}}, Buffer: 4})
	handle, _ := ports.SocketHandle[command, update](port)

	if err := port.Bind(ctx, adapterws.DuplexSocketAdapter(mux, hub, up, handle,
		adapterws.DuplexSocketAdapterOptions{})); err != nil {
		t.Fatalf("bind: %v", err)
	}
	time.Sleep(20 * time.Millisecond) // adapter registers handler in Activate

	connect(t, mux, "/live/kitchen")
	sock.inbound <- []byte(`{"action":"on","value":7}`)

	inbound := port.Inbound(ctx)
	select {
	case f := <-inbound.Values:
		if f.Payload.Action != "on" || f.Payload.Value != 7 {
			t.Errorf("payload wrong: %+v", f.Payload)
		}
		if f.Session == "" {
			t.Error("want session tag")
		}
		// W-session-info: upgrade path vars exposed.
		info, ok := hub.SessionInfo(f.Session)
		if !ok || info["room"] != "kitchen" {
			t.Errorf("SessionInfo: want room=kitchen, got %v %v", info, ok)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for inbound frame")
	}
}

// ── W2: upgrade rejection on invalid path var ─────────────────────────────────

func TestDuplex_UpgradeRejected_InvalidPathVar(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mux := http.NewServeMux()
	hub := adapterws.NewHub(0)
	up := &fakeUpgrader{}
	obs := &wsObserver{}

	uuidish := codex.String().Refine(codex.Constraint[string]{
		Name:    "roomFormat",
		Check:   func(s string) bool { return strings.HasPrefix(s, "room-") },
		Message: func(string) string { return "must start with room-" },
	})
	handle := duplexHandle(t, "/live/{room}", rest.PathParam{Name: "room"}.WithCodec(uuidish))

	adapter := adapterws.DuplexSocketAdapter(mux, hub, up, handle,
		adapterws.DuplexSocketAdapterOptions{Observer: obs})
	port, _ := ports.NewDuplexPort[command, update]("live2", commandCodec, updateCodec, ports.PortOptions{})
	_ = port.Bind(ctx, adapter)
	time.Sleep(20 * time.Millisecond)

	r := httptest.NewRequest(http.MethodGet, "/live/BAD", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("want 422, got %d", w.Code)
	}
	obs.mu.Lock()
	defer obs.mu.Unlock()
	if len(obs.requests) != 1 || obs.requests[0] != http.StatusUnprocessableEntity {
		t.Errorf("want RecordRequest(422), got %v", obs.requests)
	}
	if len(obs.valdFields) == 0 {
		t.Error("want path validation report")
	}
}

// ── W3: frame decode failure keeps connection open ────────────────────────────

func TestDuplex_DecodeFailure_ConnectionStaysOpen(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mux := http.NewServeMux()
	hub := adapterws.NewHub(0)
	sock := newFakeSocket()
	up := &fakeUpgrader{socks: []*fakeSocket{sock}}
	obs := &wsObserver{}

	port, _ := ports.NewDuplexPort[command, update]("live3", commandCodec, updateCodec,
		ports.PortOptions{Patterns: []ports.Pattern{ports.SocketPattern{Path: "/live"}}, Buffer: 4})
	handle, _ := ports.SocketHandle[command, update](port)
	_ = port.Bind(ctx, adapterws.DuplexSocketAdapter(mux, hub, up, handle,
		adapterws.DuplexSocketAdapterOptions{Observer: obs}))
	time.Sleep(20 * time.Millisecond)

	connect(t, mux, "/live")
	sock.inbound <- []byte(`{"action":"","value":1}`) // fails NonEmptyString
	sock.inbound <- []byte(`{"action":"ok","value":2}`)

	inbound := port.Inbound(ctx)
	var gotErr error
	var gotVal *ports.Framed[command]
	deadline := time.After(2 * time.Second)
	for gotErr == nil || gotVal == nil {
		select {
		case e := <-inbound.Errors:
			if gotErr == nil {
				gotErr = e
			}
		case v := <-inbound.Values:
			vv := v
			gotVal = &vv
		case <-deadline:
			t.Fatal("timeout")
		}
	}
	var se adapterws.SocketError
	if !errors.As(gotErr, &se) || se.Op != "read" {
		t.Errorf("want SocketError{read}, got %v", gotErr)
	}
	if gotVal.Payload.Action != "ok" {
		t.Errorf("connection should stay open; got %+v", gotVal)
	}
	obs.mu.Lock()
	defer obs.mu.Unlock()
	if obs.subFail != 1 || obs.subOK != 1 {
		t.Errorf("want 1 fail + 1 ok subscribe, got %d/%d", obs.subFail, obs.subOK)
	}
}

// ── W4/W5: targeted vs broadcast delivery ─────────────────────────────────────

func TestDuplex_TargetedAndBroadcast(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mux := http.NewServeMux()
	hub := adapterws.NewHub(0)
	sockA, sockB := newFakeSocket(), newFakeSocket()
	up := &fakeUpgrader{socks: []*fakeSocket{sockA, sockB}}

	port, _ := ports.NewDuplexPort[command, update]("live4", commandCodec, updateCodec,
		ports.PortOptions{Patterns: []ports.Pattern{ports.SocketPattern{Path: "/live"}}, Buffer: 8})
	handle, _ := ports.SocketHandle[command, update](port)
	_ = port.Bind(ctx, adapterws.DuplexSocketAdapter(mux, hub, up, handle,
		adapterws.DuplexSocketAdapterOptions{}))
	time.Sleep(20 * time.Millisecond)

	connect(t, mux, "/live")
	connect(t, mux, "/live")

	// Learn session IDs via inbound frames.
	sockA.inbound <- []byte(`{"action":"hello","value":1}`)
	inbound := port.Inbound(ctx)
	var sessA ports.Session
	select {
	case f := <-inbound.Values:
		sessA = f.Session
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}

	outVals := make(chan ports.Framed[update], 2)
	outVals <- ports.Framed[update]{Session: sessA, Payload: update{Text: "just-you"}}
	outVals <- ports.Framed[update]{Payload: update{Text: "everyone"}}
	close(outVals)
	outErrs := make(chan error)
	close(outErrs)
	go port.Feed(ctx, stream.Stream[ports.Framed[update]]{Values: outVals, Errors: outErrs})

	waitFor(t, func() bool { return len(sockA.writtenFrames()) == 2 && len(sockB.writtenFrames()) == 1 })
	a, b := sockA.writtenFrames(), sockB.writtenFrames()
	if !strings.Contains(string(a[0]), "just-you") || !strings.Contains(string(a[1]), "everyone") {
		t.Errorf("sockA frames wrong: %s", a)
	}
	if !strings.Contains(string(b[0]), "everyone") {
		t.Errorf("sockB frames wrong: %s", b)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}

// ── W6: slow client frame dropped ─────────────────────────────────────────────

func TestBroadcast_SlowClient_FrameDropped(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mux := http.NewServeMux()
	hub := adapterws.NewHub(1) // queue of 1
	slow := newFakeSocket()
	slow.writeErr = nil
	up := &fakeUpgrader{socks: []*fakeSocket{slow}}

	var mu sync.Mutex
	var seen []error
	port, _ := ports.NewSinkPort[update]("updates", updateCodec,
		ports.PortOptions{Patterns: []ports.Pattern{ports.SocketPattern{Path: "/updates"}}})
	handle, _ := ports.SocketHandle[struct{}, update](port)

	// Block the writer goroutine by pre-filling: use a socket whose
	// WriteMessage blocks until released.
	blocker := make(chan struct{})
	blockingSock := &blockingSocket{fakeSocket: slow, gate: blocker}
	up.socks[0] = nil
	up2 := &fakeUpgrader2{sock: blockingSock}

	adapter := adapterws.BroadcastSocketAdapter(mux, hub, up2, handle,
		adapterws.BroadcastSocketAdapterOptions{OnError: func(err error) {
			mu.Lock()
			seen = append(seen, err)
			mu.Unlock()
		}})

	vals := make(chan update)
	errCh := make(chan error)
	close(errCh)

	activateDone := make(chan struct{})
	go func() {
		adapter.Activate(ctx, stream.Stream[update]{Values: vals, Errors: errCh})
		close(activateDone)
	}()
	time.Sleep(20 * time.Millisecond) // handler registered
	connectMux(mux, "/updates")       // session registered, writer blocked on gate

	vals <- update{Text: "a"} // writer picks up, blocks in WriteMessage
	vals <- update{Text: "b"} // fills queue (size 1)
	vals <- update{Text: "c"} // queue full → dropped
	close(vals)

	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		for _, e := range seen {
			if errors.Is(e, adapterws.ErrFrameDropped) {
				return true
			}
		}
		return false
	})
	close(blocker)
	cancel()
	<-activateDone

	mu.Lock()
	defer mu.Unlock()
	dropped := false
	for _, e := range seen {
		if errors.Is(e, adapterws.ErrFrameDropped) {
			dropped = true
		}
	}
	if !dropped {
		t.Errorf("want ErrFrameDropped among %v", seen)
	}
}

type blockingSocket struct {
	*fakeSocket
	gate chan struct{}
}

func (b *blockingSocket) WriteMessage(data []byte) error {
	<-b.gate
	return b.fakeSocket.WriteMessage(data)
}

type fakeUpgrader2 struct{ sock adapterws.Socket }

func (f *fakeUpgrader2) Upgrade(_ http.ResponseWriter, _ *http.Request) (adapterws.Socket, error) {
	return f.sock, nil
}

func connectMux(mux *http.ServeMux, target string) {
	r := httptest.NewRequest(http.MethodGet, target, nil)
	mux.ServeHTTP(httptest.NewRecorder(), r)
}

// ── W7: unknown target session ────────────────────────────────────────────────

func TestDuplex_UnknownSession_Error(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mux := http.NewServeMux()
	hub := adapterws.NewHub(0)
	up := &fakeUpgrader{}

	port, _ := ports.NewDuplexPort[command, update]("live5", commandCodec, updateCodec,
		ports.PortOptions{Patterns: []ports.Pattern{ports.SocketPattern{Path: "/live"}}, Buffer: 4})
	handle, _ := ports.SocketHandle[command, update](port)
	_ = port.Bind(ctx, adapterws.DuplexSocketAdapter(mux, hub, up, handle,
		adapterws.DuplexSocketAdapterOptions{}))

	outVals := make(chan ports.Framed[update], 1)
	outVals <- ports.Framed[update]{Session: "ghost", Payload: update{Text: "x"}}
	close(outVals)
	outErrs := make(chan error)
	close(outErrs)
	go port.Feed(ctx, stream.Stream[ports.Framed[update]]{Values: outVals, Errors: outErrs})

	inbound := port.Inbound(ctx)
	select {
	case e := <-inbound.Errors:
		var se adapterws.SocketError
		if !errors.As(e, &se) || se.Session != "ghost" || se.Op != "write" {
			t.Errorf("want SocketError{ghost, write}, got %v", e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}
}

// ── W8: ctx cancel closes sessions, Activate returns ──────────────────────────

func TestIngest_CtxCancel_ClosesSessions(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	mux := http.NewServeMux()
	hub := adapterws.NewHub(0)
	sock := newFakeSocket()
	up := &fakeUpgrader{socks: []*fakeSocket{sock}}

	port, _ := ports.NewSourcePort[command]("cmds", commandCodec,
		ports.PortOptions{Patterns: []ports.Pattern{ports.SocketPattern{Path: "/cmds"}}, Buffer: 4})
	handle, _ := ports.SocketHandle[command, struct{}](port)

	done := make(chan struct{})
	adapter := adapterws.IngestSocketAdapter(mux, hub, up, handle,
		adapterws.IngestSocketAdapterOptions{})
	dst := make(chan command, 4)
	errs := make(chan error, 4)
	go func() {
		adapter.Activate(ctx, dst, errs)
		close(done)
	}()
	time.Sleep(20 * time.Millisecond)
	connectMux(mux, "/cmds")
	sock.inbound <- []byte(`{"action":"go","value":1}`)
	<-dst

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Activate did not return after ctx cancel")
	}
	sock.mu.Lock()
	closed := sock.closed
	sock.mu.Unlock()
	if !closed {
		t.Error("socket not closed on shutdown")
	}
	if got := hub.Sessions(); len(got) != 0 {
		t.Errorf("want 0 sessions after shutdown, got %v", got)
	}
}

// ── W9: SocketError LogValue + errors.As ──────────────────────────────────────

func TestSocketError_LogValue_And_Chain(t *testing.T) {
	inner := errors.New("boom")
	err := error(adapterws.SocketError{Path: "/live/{room}", Session: "s1", Op: "write", Err: inner})
	if !errors.Is(err, inner) {
		t.Error("chain must reach inner")
	}
	if got := err.Error(); got != "websocket: write /live/{room} [s1]: boom" {
		t.Errorf("Error() = %q", got)
	}
	v := err.(adapterws.SocketError).LogValue()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("want KindGroup, got %v", v.Kind())
	}
	keys := map[string]bool{}
	for _, a := range v.Group() {
		keys[a.Key] = true
	}
	for _, want := range []string{"path", "session", "op", "err"} {
		if !keys[want] {
			t.Errorf("missing key %q", want)
		}
	}
}

// ── WL: gorilla loopback round-trip (shim correctness) ────────────────────────

func TestGorillaLoopback_RoundTrip(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mux := http.NewServeMux()
	hub := adapterws.NewHub(0)
	up := adapterws.NewUpgrader(adapterws.UpgraderOptions{
		CheckOrigin: func(*http.Request) bool { return true },
	})

	port, _ := ports.NewDuplexPort[command, update]("loop", commandCodec, updateCodec,
		ports.PortOptions{Patterns: []ports.Pattern{ports.SocketPattern{Path: "/loop"}}, Buffer: 4})
	handle, _ := ports.SocketHandle[command, update](port)
	_ = port.Bind(ctx, adapterws.DuplexSocketAdapter(mux, hub, up, handle,
		adapterws.DuplexSocketAdapterOptions{}))
	time.Sleep(20 * time.Millisecond)

	srv := httptest.NewServer(mux)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/loop"
	client, _, err := gorillaws.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	// Client → server.
	if err := client.WriteMessage(gorillaws.TextMessage, []byte(`{"action":"ping","value":42}`)); err != nil {
		t.Fatalf("client write: %v", err)
	}
	inbound := port.Inbound(ctx)
	var sess ports.Session
	select {
	case f := <-inbound.Values:
		if f.Payload.Action != "ping" {
			t.Errorf("payload: %+v", f.Payload)
		}
		sess = f.Session
	case <-time.After(2 * time.Second):
		t.Fatal("timeout inbound")
	}

	// Server → client (targeted).
	outVals := make(chan ports.Framed[update], 1)
	outVals <- ports.Framed[update]{Session: sess, Payload: update{Text: "pong"}}
	close(outVals)
	outErrs := make(chan error)
	close(outErrs)
	go port.Feed(ctx, stream.Stream[ports.Framed[update]]{Values: outVals, Errors: outErrs})

	_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, data, err := client.ReadMessage()
	if err != nil {
		t.Fatalf("client read: %v", err)
	}
	if !strings.Contains(string(data), "pong") {
		t.Errorf("want pong frame, got %s", data)
	}
}

// ── Example ───────────────────────────────────────────────────────────────────

func ExampleDuplexSocketAdapter() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mux := http.NewServeMux()
	hub := adapterws.NewHub(0)
	sock := newFakeSocket()
	up := &fakeUpgrader{socks: []*fakeSocket{sock}}

	port, _ := ports.NewDuplexPort[command, update]("example", commandCodec, updateCodec,
		ports.PortOptions{Patterns: []ports.Pattern{ports.SocketPattern{Path: "/live/{room}"}}, Buffer: 4})
	handle, _ := ports.SocketHandle[command, update](port)
	_ = port.Bind(ctx, adapterws.DuplexSocketAdapter(mux, hub, up, handle,
		adapterws.DuplexSocketAdapterOptions{}))
	time.Sleep(20 * time.Millisecond)

	// A client connects to /live/kitchen and sends a command.
	r := httptest.NewRequest(http.MethodGet, "/live/kitchen", nil)
	mux.ServeHTTP(httptest.NewRecorder(), r)
	sock.inbound <- []byte(`{"action":"set-temp","value":21}`)

	f := <-port.Inbound(ctx).Values
	info, _ := hub.SessionInfo(f.Session)
	fmt.Printf("%s in room %s wants %d\n", f.Payload.Action, info["room"], f.Payload.Value)
	// Output: set-temp in room kitchen wants 21
}
