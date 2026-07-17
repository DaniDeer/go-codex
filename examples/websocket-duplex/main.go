// Package websocket-duplex demonstrates the DuplexPort — the sixth port
// type — served over a real WebSocket endpoint (adapters/websocket):
//
//   - ONE declaration: ports.NewDuplexPort + SocketPattern{Path: "/live/{room}"}
//   - clients send typed Command frames, the pipeline replies with typed
//     Update frames — TARGETED to the sender's session, plus a broadcast
//   - session routing uses hub.SessionInfo (which {room} a session joined)
//   - lifecycle wired through the app package (supervised Feed goroutine)
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
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	adapterws "github.com/DaniDeer/go-codex/adapters/websocket"
	"github.com/DaniDeer/go-codex/app"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/ports"
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

func main() {
	a := app.New(app.Options{ShutdownTimeout: 2 * time.Second})
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

	// ── Ordered teardown via app ──────────────────────────────────────────
	if err := a.Shutdown(); err != nil {
		fmt.Println("shutdown:", err)
	}
	fmt.Println("clean shutdown")
}
