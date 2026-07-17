// Package websocket-client demonstrates websocket Phase 2: a CLIENT-side
// DialDuplexAdapter connecting one go-codex process to another over a real
// WebSocket connection, plus AsyncAPI spec generation for the socket.
//
//   - SERVER: DuplexPort + DuplexSocketAdapter (Phase 1) — echoes commands
//   - CLIENT: a second DuplexPort (types mirrored: receives Update, sends
//     Command) bound with DialDuplexAdapter — auto-reconnect with backoff,
//     gap SocketErrors on the Errors channel, session generations c1,c2,…
//   - SPEC: ports.RegisterSocket renders the socket as an AsyncAPI channel
//     (Subscribe = inbound frames, Publish = outbound frames) — OpenAPI
//     cannot express WebSocket, AsyncAPI can
//
// # Running
//
// go run ./examples/websocket-client
package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	adapterws "github.com/DaniDeer/go-codex/adapters/websocket"
	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/ports"
	"github.com/DaniDeer/go-codex/stream"
	"github.com/DaniDeer/go-codex/validate"
)

// ── Shared frame types (in a real system: a shared contract package) ─────────

// Command flows client → server.
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

// Update flows server → client.
type Update struct {
	Text string
}

var updateCodec = codex.Struct[Update](
	codex.RequiredField("text", codex.String(),
		func(u Update) string { return u.Text },
		func(u *Update, v string) { u.Text = v },
	),
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// ── SERVER process: duplex port served over WS (Phase 1) ─────────────
	serverPort := codex.Must(ports.NewDuplexPort[Command, Update]("server-live",
		commandCodec, updateCodec, ports.PortOptions{
			Patterns: []ports.Pattern{ports.SocketPattern{Path: "/live/{room}"}},
			Buffer:   8,
		}))
	mux := http.NewServeMux()
	hub := adapterws.NewHub(0)
	sHandle, _ := ports.SocketHandle[Command, Update](serverPort)
	if err := serverPort.Bind(ctx, adapterws.DuplexSocketAdapter(mux, hub,
		adapterws.NewUpgrader(adapterws.UpgraderOptions{CheckOrigin: func(*http.Request) bool { return true }}),
		sHandle, adapterws.DuplexSocketAdapterOptions{})); err != nil {
		panic(err)
	}
	srv := httptest.NewServer(mux)
	defer srv.Close()
	time.Sleep(50 * time.Millisecond)

	// Server pipeline: ack every command back to its sender.
	go func() {
		replies := stream.Map(ctx, serverPort.Inbound(ctx),
			func(f ports.Framed[Command]) (ports.Framed[Update], error) {
				info, _ := hub.SessionInfo(f.Session)
				return ports.Framed[Update]{Session: f.Session,
					Payload: Update{Text: fmt.Sprintf("ack %s in %s", f.Payload.Action, info["room"])}}, nil
			}, stream.MapOptions{Name: "ack"})
		serverPort.Feed(ctx, replies)
	}()

	// ── CLIENT process: dial adapter on a mirrored duplex port ───────────
	// Types are swapped: the client RECEIVES Update and SENDS Command.
	clientPort := codex.Must(ports.NewDuplexPort[Update, Command]("client-live",
		updateCodec, commandCodec, ports.PortOptions{
			Patterns: []ports.Pattern{ports.SocketPattern{Path: "/live/{room}"}},
			Buffer:   8,
		}))
	cHandle, _ := ports.SocketHandle[Update, Command](clientPort)
	wsBase := "ws" + strings.TrimPrefix(srv.URL, "http")
	if err := clientPort.Bind(ctx, adapterws.DialDuplexAdapter(
		adapterws.NewDialer(adapterws.DialerOptions{}),
		wsBase, map[string]string{"room": "ops"}, // path vars → /live/ops
		cHandle, adapterws.DialAdapterOptions{})); err != nil {
		panic(err)
	}

	// Pump a command until the ack arrives — frames sent before the dial
	// completes are DROPPED by design (gap semantics: reconnects surface as
	// SocketError, frames during a gap are lost, never silently queued).
	outVals := make(chan ports.Framed[Command])
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
				case outVals <- ports.Framed[Command]{Payload: Command{Action: "deploy", Value: 3}}:
				case <-pumpCtx.Done():
					return
				}
			}
		}
	}()
	go clientPort.Feed(ctx, stream.Stream[ports.Framed[Command]]{Values: outVals, Errors: outErrs})

	inbound := clientPort.Inbound(ctx)
	deadline := time.After(5 * time.Second)
recvLoop:
	for {
		select {
		case f := <-inbound.Values:
			fmt.Printf("client [%s] received: %s\n", f.Session, f.Payload.Text)
			stopPump()
			break recvLoop
		case <-inbound.Errors:
			// dropped-frame notices while the connection establishes — expected
		case <-deadline:
			panic("timeout")
		}
	}

	// ── SPEC: the socket as an AsyncAPI channel ───────────────────────────
	b := events.NewBuilder(events.Info{Title: "Live Ops Socket", Version: "1.0.0"})
	b.AddServer("prod", events.Server{URL: "live.example.com", Protocol: "ws"})
	if err := ports.RegisterSocket[Command, Update](b, serverPort); err != nil {
		panic(err)
	}
	doc, err := b.AsyncAPISpec()
	if err != nil {
		panic(err)
	}
	raw, _ := doc.MarshalJSON()
	spec := string(raw)
	fmt.Println("asyncapi channel present:", strings.Contains(spec, "/live/{room}"))
	fmt.Println("ws protocol present:", strings.Contains(spec, `"ws"`))
	fmt.Println("both frame directions present:",
		strings.Contains(spec, "Inbound socket frames") && strings.Contains(spec, "Outbound socket frames"))
}
