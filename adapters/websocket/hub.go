package websocket

import (
	"fmt"
	"sync"

	"github.com/DaniDeer/go-codex/ports"
)

// Hub is the session registry shared between an adapter and the caller.
// Construct one per socket endpoint with [NewHub] and pass it to the adapter
// constructor; query [Hub.SessionInfo] from pipeline code to route by
// upgrade-time path vars (e.g. the {room} a session joined).
//
// Delivery policy (by design): each session has a buffered outbound queue
// (default 16). A full queue DROPS the frame and reports a [SocketError] —
// a slow client never blocks the pipeline or other sessions (the
// stream.BroadcastHub precedent).
type Hub struct {
	mu      sync.Mutex
	nextID  uint64
	buffer  int
	entries map[ports.Session]*hubEntry
}

type hubEntry struct {
	sock  Socket
	out   chan []byte
	info  map[string]string
	done  chan struct{}
	onErr func(ports.Session, error)
}

// NewHub creates a Hub. buffer is the per-session outbound queue size;
// <= 0 means the default 16.
func NewHub(buffer int) *Hub {
	if buffer <= 0 {
		buffer = 16
	}
	return &Hub{buffer: buffer, entries: map[ports.Session]*hubEntry{}}
}

// SessionInfo returns the upgrade-time path vars recorded for a session
// (e.g. {"room": "a"}), or (nil, false) when the session is not connected.
func (h *Hub) SessionInfo(s ports.Session) (map[string]string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	e, ok := h.entries[s]
	if !ok {
		return nil, false
	}
	info := make(map[string]string, len(e.info))
	for k, v := range e.info {
		info[k] = v
	}
	return info, true
}

// Sessions returns the currently connected session IDs.
func (h *Hub) Sessions() []ports.Session {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]ports.Session, 0, len(h.entries))
	for s := range h.entries {
		out = append(out, s)
	}
	return out
}

// register adds a connected socket and starts its single writer goroutine.
// onErr receives write failures for this session (session passed explicitly
// — the callback may fire before register returns).
func (h *Hub) register(sock Socket, info map[string]string, onErr func(ports.Session, error)) ports.Session {
	h.mu.Lock()
	h.nextID++
	sess := ports.Session(fmt.Sprintf("s%d", h.nextID))
	e := &hubEntry{
		sock:  sock,
		out:   make(chan []byte, h.buffer),
		info:  info,
		done:  make(chan struct{}),
		onErr: onErr,
	}
	h.entries[sess] = e
	h.mu.Unlock()

	go func() {
		for data := range e.out {
			if err := e.sock.WriteMessage(data); err != nil {
				if e.onErr != nil {
					e.onErr(sess, err)
				}
				return // writer exits; unregister closes the socket
			}
		}
	}()
	return sess
}

// unregister removes a session and closes its socket and writer queue.
func (h *Hub) unregister(sess ports.Session) {
	h.mu.Lock()
	e, ok := h.entries[sess]
	if ok {
		delete(h.entries, sess)
	}
	h.mu.Unlock()
	if !ok {
		return
	}
	close(e.out)
	close(e.done)
	_ = e.sock.Close()
}

// send queues data for one session. Returns false when the session is
// unknown or its queue is full (frame DROPPED — slow-client policy).
func (h *Hub) send(sess ports.Session, data []byte) (ok, known bool) {
	h.mu.Lock()
	e, exists := h.entries[sess]
	h.mu.Unlock()
	if !exists {
		return false, false
	}
	select {
	case e.out <- data:
		return true, true
	default:
		return false, true // queue full: drop
	}
}

// closeAll unregisters every session (close handshake per socket).
func (h *Hub) closeAll() {
	h.mu.Lock()
	sessions := make([]ports.Session, 0, len(h.entries))
	for s := range h.entries {
		sessions = append(sessions, s)
	}
	h.mu.Unlock()
	for _, s := range sessions {
		h.unregister(s)
	}
}
