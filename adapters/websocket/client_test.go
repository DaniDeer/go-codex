package websocket_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	adapterws "github.com/DaniDeer/go-codex/adapters/websocket"
	"github.com/DaniDeer/go-codex/ports"
	"github.com/DaniDeer/go-codex/stream"
)

// fakeDialer scripts a sequence of dial outcomes: an error, or a fakeSocket.
type fakeDialer struct {
	mu      sync.Mutex
	script  []any // error | *fakeSocket
	next    int
	dialed  int
	lastURL string
}

func (f *fakeDialer) Dial(_ context.Context, url string) (adapterws.Socket, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dialed++
	f.lastURL = url
	if f.next >= len(f.script) {
		return nil, errors.New("no more dials scripted")
	}
	v := f.script[f.next]
	f.next++
	if err, ok := v.(error); ok {
		return nil, err
	}
	return v.(*fakeSocket), nil
}

func sourceHandle(t *testing.T, path string) ports.Socket[command, struct{}] {
	t.Helper()
	port, err := ports.NewSourcePort[command]("feed", commandCodec, ports.PortOptions{})
	if err != nil {
		t.Fatalf("port: %v", err)
	}
	h, err := port.PluginSocketPattern(ports.SocketPattern{Path: path})
	if err != nil {
		t.Fatalf("PluginSocketPattern: %v", err)
	}
	return h
}

// ── C-W1: reconnect with gap errors + session generations ────────────────────

func TestDialSource_ReconnectGapVisibility(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sock1, sock2 := newFakeSocket(), newFakeSocket()
	dialer := &fakeDialer{script: []any{
		errors.New("refused"), // attempt 1 fails → dial gap error
		sock1,                 // c1 connects
		sock2,                 // c2 connects after c1 drops
	}}

	adapter := adapterws.DialSourceAdapter(dialer, "ws://test", nil,
		sourceHandle(t, "/feed"), adapterws.DialAdapterOptions{MaxBackoff: 5 * time.Millisecond})

	dst := make(chan command, 8)
	errs := make(chan error, 8)
	done := make(chan struct{})
	go func() {
		adapter.Activate(ctx, dst, errs)
		close(done)
	}()

	// First frame on c1, then drop c1 (close), frame on c2.
	sock1.inbound <- []byte(`{"action":"a","value":1}`)
	v1 := <-dst
	_ = sock1.Close() // connection lost → gap error, reconnect to sock2
	sock2.inbound <- []byte(`{"action":"b","value":2}`)
	v2 := <-dst

	if v1.Action != "a" || v2.Action != "b" {
		t.Errorf("frames wrong: %+v %+v", v1, v2)
	}

	// Collect gap errors: at least the failed dial + the c1 drop.
	var dialGap, readGap bool
	deadline := time.After(2 * time.Second)
	for !(dialGap && readGap) {
		select {
		case e := <-errs:
			var se adapterws.SocketError
			if errors.As(e, &se) {
				switch se.Op {
				case "dial":
					dialGap = true
				case "read":
					readGap = true
					if se.Session != "c1" {
						t.Errorf("gap session: want c1, got %s", se.Session)
					}
				}
			}
		case <-deadline:
			t.Fatalf("timeout: dialGap=%v readGap=%v", dialGap, readGap)
		}
	}

	if dialer.lastURL != "ws://test/feed" {
		t.Errorf("URL: %s", dialer.lastURL)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Activate did not stop on ctx cancel")
	}
}

// ── C-W2: ctx cancel stops the retry loop ─────────────────────────────────────

func TestDialSource_CtxCancelStopsRetry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	dialer := &fakeDialer{script: []any{}} // every dial errors

	adapter := adapterws.DialSourceAdapter(dialer, "ws://test", nil,
		sourceHandle(t, "/feed"), adapterws.DialAdapterOptions{MaxBackoff: time.Millisecond})

	dst := make(chan command)
	errs := make(chan error, 64)
	done := make(chan struct{})
	go func() {
		adapter.Activate(ctx, dst, errs)
		close(done)
	}()
	time.Sleep(20 * time.Millisecond) // a few failed attempts
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("retry loop did not stop")
	}
	if dialer.dialed == 0 {
		t.Error("expected dial attempts")
	}
}

// ── C-W3: duplex client — outbound during gap dropped ────────────────────────

func TestDialDuplex_OutboundDuringGapDropped(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Dialer that never connects: all outbound frames hit a down connection.
	dialer := &fakeDialer{}

	port, _ := ports.NewDuplexPort[command, update]("up", commandCodec, updateCodec,
		ports.PortOptions{Buffer: 4})
	handle, err := port.PluginSocketPattern(ports.SocketPattern{Path: "/up"})
	if err != nil {
		t.Fatalf("PluginSocketPattern: %v", err)
	}

	if err := port.Bind(ctx, adapterws.DialDuplexAdapter(dialer, "ws://down", nil, handle,
		adapterws.DialAdapterOptions{MaxBackoff: time.Millisecond})); err != nil {
		t.Fatalf("bind: %v", err)
	}

	outVals := make(chan ports.Framed[update], 1)
	outVals <- ports.Framed[update]{Payload: update{Text: "lost"}}
	close(outVals)
	outErrs := make(chan error)
	close(outErrs)
	go port.Feed(ctx, stream.Stream[ports.Framed[update]]{Values: outVals, Errors: outErrs})

	inbound := port.Inbound(ctx)
	deadline := time.After(2 * time.Second)
	for {
		select {
		case e := <-inbound.Errors:
			if errors.Is(e, adapterws.ErrFrameDropped) {
				return // dropped frame surfaced — done
			}
		case <-deadline:
			t.Fatal("timeout waiting for ErrFrameDropped")
		}
	}
}

// ── C-W4: duplex client round-trip via fake ───────────────────────────────────

func TestDialDuplex_RoundTrip(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sock := newFakeSocket()
	dialer := &fakeDialer{script: []any{sock}}

	port, _ := ports.NewDuplexPort[command, update]("up2", commandCodec, updateCodec,
		ports.PortOptions{Buffer: 4})
	handle, err := port.PluginSocketPattern(ports.SocketPattern{Path: "/up"})
	if err != nil {
		t.Fatalf("PluginSocketPattern: %v", err)
	}
	_ = port.Bind(ctx, adapterws.DialDuplexAdapter(dialer, "ws://test", nil, handle,
		adapterws.DialAdapterOptions{MaxBackoff: time.Millisecond}))

	// Inbound: server sends a frame to us.
	sock.inbound <- []byte(`{"action":"tick","value":9}`)
	inbound := port.Inbound(ctx)
	select {
	case f := <-inbound.Values:
		if f.Payload.Action != "tick" || f.Session != "c1" {
			t.Errorf("inbound: %+v", f)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout inbound")
	}

	// Outbound: we send a frame.
	outVals := make(chan ports.Framed[update], 1)
	outVals <- ports.Framed[update]{Payload: update{Text: "hello"}}
	close(outVals)
	outErrs := make(chan error)
	close(outErrs)
	go port.Feed(ctx, stream.Stream[ports.Framed[update]]{Values: outVals, Errors: outErrs})

	waitFor(t, func() bool { return len(sock.writtenFrames()) == 1 })
	if !strings.Contains(string(sock.writtenFrames()[0]), "hello") {
		t.Errorf("outbound frame: %s", sock.writtenFrames()[0])
	}
}

// ── C-W5: gorilla dial loopback (real shim both sides) ────────────────────────

func TestGorillaDial_Loopback(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Server side: phase-1 duplex adapter echoing via pipeline.
	mux := http.NewServeMux()
	hub := adapterws.NewHub(0)
	serverPort, _ := ports.NewDuplexPort[command, update]("srv", commandCodec, updateCodec,
		ports.PortOptions{Buffer: 4})
	sHandle, err := serverPort.PluginSocketPattern(ports.SocketPattern{Path: "/loop"})
	if err != nil {
		t.Fatalf("PluginSocketPattern: %v", err)
	}
	_ = serverPort.Bind(ctx, adapterws.DuplexSocketAdapter(mux, hub,
		adapterws.NewUpgrader(adapterws.UpgraderOptions{CheckOrigin: func(*http.Request) bool { return true }}),
		sHandle, adapterws.DuplexSocketAdapterOptions{}))
	time.Sleep(20 * time.Millisecond)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Server pipeline: echo each command as an update to the sender.
	go func() {
		replies := stream.Map(ctx, serverPort.Inbound(ctx),
			func(f ports.Framed[command]) (ports.Framed[update], error) {
				return ports.Framed[update]{Session: f.Session, Payload: update{Text: f.Payload.Action}}, nil
			}, stream.MapOptions{})
		serverPort.Feed(ctx, replies)
	}()

	// Client side: dial adapter on a second duplex port (In/Out swapped:
	// the client RECEIVES update frames and SENDS command frames).
	clientPort, _ := ports.NewDuplexPort[update, command]("cli", updateCodec, commandCodec,
		ports.PortOptions{Buffer: 4})
	cHandle, err := clientPort.PluginSocketPattern(ports.SocketPattern{Path: "/loop"})
	if err != nil {
		t.Fatalf("PluginSocketPattern: %v", err)
	}
	wsBase := "ws" + strings.TrimPrefix(srv.URL, "http")
	_ = clientPort.Bind(ctx, adapterws.DialDuplexAdapter(
		adapterws.NewDialer(adapterws.DialerOptions{}), wsBase, nil, cHandle,
		adapterws.DialAdapterOptions{}))

	// Pump pings until the echo arrives — frames sent before the dial
	// completes are DROPPED by design (gap semantics), so a single
	// fire-and-forget frame could be lost during connection establishment.
	outVals := make(chan ports.Framed[command])
	outErrs := make(chan error)
	pumpCtx, stopPump := context.WithCancel(ctx)
	go func() {
		defer close(outVals)
		defer close(outErrs)
		t := time.NewTicker(25 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-pumpCtx.Done():
				return
			case <-t.C:
				select {
				case outVals <- ports.Framed[command]{Payload: command{Action: "ping", Value: 1}}:
				case <-pumpCtx.Done():
					return
				}
			}
		}
	}()
	go clientPort.Feed(ctx, stream.Stream[ports.Framed[command]]{Values: outVals, Errors: outErrs})

	inbound := clientPort.Inbound(ctx)
	deadline := time.After(3 * time.Second)
	for {
		select {
		case f := <-inbound.Values:
			if f.Payload.Text == "ping" {
				stopPump()
				return // full round trip
			}
		case <-inbound.Errors:
			// dropped-frame notices during connection establishment — keep going
		case <-deadline:
			stopPump()
			t.Fatal("timeout waiting for echo")
		}
	}
}
