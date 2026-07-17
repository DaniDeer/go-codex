// Package websocket-duplex demonstrates the DuplexPort — the sixth port
// type — served over a real WebSocket endpoint (adapters/websocket):
//
//   - ONE declaration: ports.NewDuplexPort + SocketPattern{Path: "/live/{room}"}
//   - clients send typed Command frames, the pipeline replies with typed
//     Update frames — TARGETED to the sender's session, plus a broadcast
//   - session routing uses hub.SessionInfo (which {room} a session joined)
//   - lifecycle wired through the app package (supervised Feed goroutine)
//   - observer pattern: app.Options.Observer pre-injects a stats.Observer
//     into ctx; the adapter fires RecordRequest (upgrade), RecordSubscribe/
//     RecordPublish (frames), RecordValidationError (bad frame) — no
//     adapter-specific observer interface needed
//   - logging: stats.NewFanout combines the metrics observer with a
//     stats.LoggingObserver — every observer event is ALSO a structured
//     slog line, from the same single injection point
//
// A real gorilla client dials the loopback server, so the full upgrade →
// frame → codec → pipeline → targeted-reply path runs. Exits 0.
//
// # Running
//
// go run ./examples/websocket-duplex
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"time"

	adapterws "github.com/DaniDeer/go-codex/adapters/websocket"
	"github.com/DaniDeer/go-codex/app"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/ports"
	"github.com/DaniDeer/go-codex/stats"
	"github.com/DaniDeer/go-codex/stream"
	"github.com/DaniDeer/go-codex/validate"
	gorillaws "github.com/gorilla/websocket"
)

// ── Domain ────────────────────────────────────────────────────────────────────

// Command is an inbound client frame.
type Command struct {
	Action string
	Value  int
}

var commandCodec = codex.Struct[Command](
	codex.RequiredField("action", codex.String().Refine(validate.NonEmptyString),
		func(c Command) string { return c.Action },
		func(c *Command, v string) { c.Action = v },
	),
	codex.RequiredField("value", codex.Int(),
		func(c Command) int { return c.Value },
		func(c *Command, v int) { c.Value = v },
	),
)

// Update is an outbound server frame.
type Update struct {
	Room string
	Text string
}

var updateCodec = codex.Struct[Update](
	codex.RequiredField("room", codex.String(),
		func(u Update) string { return u.Room },
		func(u *Update, v string) { u.Room = v },
	),
	codex.RequiredField("text", codex.String(),
		func(u Update) string { return u.Text },
		func(u *Update, v string) { u.Text = v },
	),
)

// Live is the duplex boundary — declared once, no adapter imports needed
// in real domain code.
var Live = codex.Must(ports.NewDuplexPort[Command, Update]("live",
	commandCodec, updateCodec, ports.PortOptions{
		Patterns: []ports.Pattern{ports.SocketPattern{Path: "/live/{room}"}},
		Buffer:   8,
	}))

// ── Observer: pure counters over the transport-agnostic hooks ────────────────
//
// The websocket adapter needs NO new observer extension — it fires the same
// hooks every transport uses: RecordRequest once per upgrade attempt,
// RecordSubscribe per inbound frame, RecordPublish per outbound frame, and
// RecordValidationError for codec failures. In production replace the
// counters with Prometheus/OTel instruments — the interface is identical.
type socketMetrics struct {
	stats.NoopObserver // satisfies everything not explicitly implemented
	mu                 sync.Mutex
	upgrades           map[int]int // HTTP status → count (101 = success)
	framesIn           int
	framesOut          int
	validationFailures []string // "location/field"
}

func (m *socketMetrics) RecordRequest(_, _ string, status int, _ time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.upgrades == nil {
		m.upgrades = map[int]int{}
	}
	m.upgrades[status]++
}

func (m *socketMetrics) RecordSubscribe(_ string, success bool, _ time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if success {
		m.framesIn++
	}
}

func (m *socketMetrics) RecordPublish(_ string, success bool, _ time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if success {
		m.framesOut++
	}
}

func (m *socketMetrics) RecordValidationError(location, _, field string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.validationFailures = append(m.validationFailures, location+"/"+field)
}

func main() {
	// app.New pre-injects the observer into app.Context() via
	// stats.WithObserver — the SINGLE injection point. Every adapter whose
	// options leave Observer nil resolves it from ctx automatically.
	//
	// NewFanout combines metrics AND structured logging: LoggingObserver
	// logs every observer event via slog (upgrade requests, per-frame
	// subscribe/publish, validation failures), while socketMetrics counts.
	// SocketError implements slog.LogValuer, so logged errors render as
	// structured path/session/op groups for free.
	metrics := &socketMetrics{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	obs := stats.NewFanout(metrics, stats.NewLoggingObserver(logger.With("component", "websocket")))
	a := app.New(app.Options{Observer: obs, ShutdownTimeout: 2 * time.Second})
	ctx := a.Context()

	// ── Bind the transport in main (protocol decision lives here) ────────
	mux := http.NewServeMux()
	hub := adapterws.NewHub(0)
	upgrader := adapterws.NewUpgrader(adapterws.UpgraderOptions{
		CheckOrigin: func(*http.Request) bool { return true }, // demo only
	})
	handle, _ := ports.SocketHandle[Command, Update](Live)
	if err := Live.Bind(ctx, adapterws.DuplexSocketAdapter(mux, hub, upgrader, handle,
		adapterws.DuplexSocketAdapterOptions{})); err != nil {
		panic(err)
	}

	srv := httptest.NewServer(mux)
	defer srv.Close()
	time.Sleep(50 * time.Millisecond) // adapter registers its handler in Activate

	// ── Pipeline: Command → targeted Update (session preserved by Map) ───
	replies := stream.Map(ctx, Live.Inbound(ctx),
		func(f ports.Framed[Command]) (ports.Framed[Update], error) {
			info, _ := hub.SessionInfo(f.Session)
			return ports.Framed[Update]{
				Session: f.Session, // targeted reply to the sender
				Payload: Update{
					Room: info["room"],
					Text: fmt.Sprintf("ack %s=%d", f.Payload.Action, f.Payload.Value),
				},
			}, nil
		}, stream.MapOptions{Name: "ack"})

	// IMPORTANT: consume pipeline errors BEFORE Feed. Errors fed into a
	// DuplexPort's outbound stream are re-surfaced on the port's inbound
	// Errors channel (that is how write failures reach you) — an unfiltered
	// Inbound→Map→Feed loop would recycle them forever. The observer has
	// already counted the validation failure; here we just drop it.
	replies = stream.MapErr(ctx, replies,
		func(err error) (ports.Framed[Update], bool, error) {
			var zero ports.Framed[Update]
			return zero, false, nil // silence — counted by the observer
		})

	a.Go("live-feed", func(ctx context.Context) error {
		Live.Feed(ctx, replies)
		return nil
	})

	// ── A real client dials in, joins room "kitchen", sends a command ────
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/live/kitchen"
	client, _, err := gorillaws.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		panic(err)
	}
	defer client.Close()

	// First frame is INVALID (empty action fails NonEmptyString) — it lands
	// on the port's Errors channel and fires RecordValidationError; the
	// connection stays open.
	if err := client.WriteMessage(gorillaws.TextMessage,
		[]byte(`{"action":"","value":0}`)); err != nil {
		panic(err)
	}
	if err := client.WriteMessage(gorillaws.TextMessage,
		[]byte(`{"action":"set-temp","value":21}`)); err != nil {
		panic(err)
	}

	_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, frame, err := client.ReadMessage()
	if err != nil {
		panic(err)
	}
	fmt.Printf("client received: %s\n", frame)
	fmt.Printf("connected sessions: %d\n", len(hub.Sessions()))

	// ── Observer summary ──────────────────────────────────────────────────
	metrics.mu.Lock()
	fmt.Printf("metrics: upgrades(101)=%d frames_in=%d frames_out=%d validation_failures=%v\n",
		metrics.upgrades[http.StatusSwitchingProtocols],
		metrics.framesIn, metrics.framesOut, metrics.validationFailures)
	metrics.mu.Unlock()

	// ── Ordered teardown via app ──────────────────────────────────────────
	if err := a.Shutdown(); err != nil {
		fmt.Println("shutdown:", err)
	}
	fmt.Println("clean shutdown")
}
