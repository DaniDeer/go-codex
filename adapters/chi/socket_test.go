package chi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	gochi "github.com/go-chi/chi/v5"
	gorillaws "github.com/gorilla/websocket"

	adapterchi "github.com/DaniDeer/go-codex/adapters/chi"
	adapterws "github.com/DaniDeer/go-codex/adapters/websocket"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/ports"
	"github.com/DaniDeer/go-codex/stream"
	"github.com/DaniDeer/go-codex/validate"
)

type wsCmd struct {
	Action string
	Value  int
}

var wsCmdCodec = codex.Struct[wsCmd](
	codex.RequiredField("action", codex.String().Refine(validate.NonEmptyString),
		func(c wsCmd) string { return c.Action },
		func(c *wsCmd, v string) { c.Action = v },
	),
	codex.RequiredField("value", codex.Int(),
		func(c wsCmd) int { return c.Value },
		func(c *wsCmd, v int) { c.Value = v },
	),
)

type wsUpd struct {
	Text string
}

var wsUpdCodec = codex.Struct[wsUpd](
	codex.RequiredField("text", codex.String(),
		func(u wsUpd) string { return u.Text },
		func(u *wsUpd, v string) { u.Text = v },
	),
)

// Chi socket: 503 before Activate installs the handler; full loopback
// round-trip after (constructor-time swap-handler registration — chi's Mux
// is not registration-safe while serving).
func TestChiDuplexSocket_SwapHandlerAndRoundTrip(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	router := gochi.NewRouter()
	hub := adapterws.NewHub(0)
	up := adapterws.NewUpgrader(adapterws.UpgraderOptions{
		CheckOrigin: func(*http.Request) bool { return true },
	})

	port, err := ports.NewDuplexPort[wsCmd, wsUpd]("chi-live", wsCmdCodec, wsUpdCodec,
		ports.PortOptions{Buffer: 4})
	if err != nil {
		t.Fatalf("port: %v", err)
	}
	handle, err := port.PluginSocketPattern(ports.SocketPattern{Path: "/live/{room}"})
	if err != nil {
		t.Fatalf("PluginSocketPattern: %v", err)
	}

	// Constructor registers the swap handler — BEFORE Activate/Bind.
	adapter := adapterchi.DuplexSocketAdapter(router, hub, up, handle,
		adapterws.DuplexSocketAdapterOptions{})
	if got := adapter.AdapterName(); got != "chi.DuplexSocketAdapter" {
		t.Errorf("AdapterName: %s", got)
	}

	srv := httptest.NewServer(router)
	defer srv.Close()

	// Not yet active → 503.
	resp, err := http.Get(srv.URL + "/live/kitchen")
	if err != nil {
		t.Fatalf("pre-activate get: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("pre-activate: want 503, got %d", resp.StatusCode)
	}

	// Bind (starts Activate → installs the real handler).
	if err := port.Bind(ctx, adapter); err != nil {
		t.Fatalf("bind: %v", err)
	}
	time.Sleep(20 * time.Millisecond)

	// Server pipeline: echo action back to the sender's session.
	go func() {
		replies := stream.Map(ctx, port.Inbound(ctx),
			func(f ports.Framed[wsCmd]) (ports.Framed[wsUpd], error) {
				info, _ := hub.SessionInfo(f.Session)
				return ports.Framed[wsUpd]{Session: f.Session,
					Payload: wsUpd{Text: f.Payload.Action + "@" + info["room"]}}, nil
			}, stream.MapOptions{})
		port.Feed(ctx, replies)
	}()

	// Real gorilla client through the chi router.
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/live/kitchen"
	client, _, err := gorillaws.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	if err := client.WriteMessage(gorillaws.TextMessage, []byte(`{"action":"on","value":1}`)); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, data, err := client.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	// chi populated r.PathValue → SessionInfo carries the room.
	if !strings.Contains(string(data), "on@kitchen") {
		t.Errorf("want on@kitchen echo, got %s", data)
	}
}
