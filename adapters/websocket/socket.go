package websocket

import (
	"net/http"
	"sync"
	"time"

	gorillaws "github.com/gorilla/websocket"
)

// Socket is the narrow per-connection surface the adapters use. Constructors
// accept [Upgrader]/Socket — never a concrete client type — so unit tests run
// against hand-written fakes and the adapter stays decoupled from gorilla.
//
// ReadMessage blocks until a frame arrives, the peer closes, or the
// connection breaks. WriteMessage must be safe for use from the hub's single
// writer goroutine (one writer per connection — the adapter guarantees no
// concurrent writes). Close performs the close handshake; it must be safe to
// call concurrently with a blocked ReadMessage (unblocking it).
type Socket interface {
	ReadMessage() ([]byte, error)
	WriteMessage(data []byte) error
	Close() error
}

// Upgrader upgrades an HTTP request to a [Socket]. [NewUpgrader] adapts
// gorilla/websocket; tests provide fakes.
type Upgrader interface {
	Upgrade(w http.ResponseWriter, r *http.Request) (Socket, error)
}

// UpgraderOptions configures [NewUpgrader].
type UpgraderOptions struct {
	// Subprotocols lists acceptable Sec-WebSocket-Protocol values.
	// Usually taken from the port's [ports.Socket].Subprotocols.
	Subprotocols []string
	// PingInterval is the keepalive ping cadence. Default 30s.
	PingInterval time.Duration
	// ReadLimit caps inbound frame size in bytes. Default 1 MiB.
	ReadLimit int64
	// CheckOrigin overrides gorilla's same-origin policy. Nil keeps the
	// default (reject cross-origin browser connections).
	CheckOrigin func(r *http.Request) bool
}

// NewUpgrader returns an [Upgrader] backed by gorilla/websocket. Each
// upgraded connection runs an adapter-owned keepalive: pings every
// PingInterval, expects a pong within 2×PingInterval, and enforces
// ReadLimit. This file is the only place the adapter touches gorilla.
func NewUpgrader(opts UpgraderOptions) Upgrader {
	if opts.PingInterval <= 0 {
		opts.PingInterval = 30 * time.Second
	}
	if opts.ReadLimit <= 0 {
		opts.ReadLimit = 1 << 20
	}
	return &gorillaUpgrader{
		up: gorillaws.Upgrader{
			Subprotocols: opts.Subprotocols,
			CheckOrigin:  opts.CheckOrigin,
		},
		opts: opts,
	}
}

type gorillaUpgrader struct {
	up   gorillaws.Upgrader
	opts UpgraderOptions
}

func (g *gorillaUpgrader) Upgrade(w http.ResponseWriter, r *http.Request) (Socket, error) {
	conn, err := g.up.Upgrade(w, r, nil)
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

type gorillaSocket struct {
	conn    *gorillaws.Conn
	writeMu sync.Mutex // serializes data writes with keepalive pings
	done    chan struct{}
	once    sync.Once
}

func (s *gorillaSocket) keepalive(interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-s.done:
			return
		case <-t.C:
			s.writeMu.Lock()
			err := s.conn.WriteControl(gorillaws.PingMessage, nil, time.Now().Add(10*time.Second))
			s.writeMu.Unlock()
			if err != nil {
				return
			}
		}
	}
}

func (s *gorillaSocket) ReadMessage() ([]byte, error) {
	_, data, err := s.conn.ReadMessage()
	return data, err
}

func (s *gorillaSocket) WriteMessage(data []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.conn.WriteMessage(gorillaws.TextMessage, data)
}

func (s *gorillaSocket) Close() error {
	s.once.Do(func() { close(s.done) })
	s.writeMu.Lock()
	_ = s.conn.WriteControl(gorillaws.CloseMessage,
		gorillaws.FormatCloseMessage(gorillaws.CloseNormalClosure, ""),
		time.Now().Add(2*time.Second))
	s.writeMu.Unlock()
	return s.conn.Close()
}
