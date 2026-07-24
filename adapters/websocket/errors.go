package websocket

import (
	"fmt"
	"log/slog"

	"github.com/DaniDeer/go-codex/ports"
)

// SocketError wraps a websocket operation failure.
type SocketError struct {
	// Path is the declared upgrade path template (e.g. "/live/{room}").
	Path string
	// Session identifies the affected peer. Empty for upgrade failures
	// (no session exists yet) and broadcast-level errors.
	Session ports.Session
	// Op is the operation: "upgrade", "read", "write", or "close".
	Op string
	// Err is the underlying error. For a dropped frame (slow client) this
	// is [ErrFrameDropped].
	Err error
}

func (e SocketError) Error() string {
	if e.Session == "" {
		return fmt.Sprintf("websocket: %s %s: %v", e.Op, e.Path, e.Err)
	}
	return fmt.Sprintf("websocket: %s %s [%s]: %v", e.Op, e.Path, e.Session, e.Err)
}

// Unwrap allows [errors.Is] and [errors.As] to reach the underlying error.
func (e SocketError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e SocketError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("path", e.Path),
		slog.String("session", string(e.Session)),
		slog.String("op", e.Op),
		slog.Any("err", e.Err),
	)
}

// ErrFrameDropped is the sentinel wrapped in a [SocketError] when a slow
// client's outbound queue is full and the frame is dropped (at-most-once
// delivery policy — a lagging session never blocks the pipeline).
var ErrFrameDropped = fmt.Errorf("websocket: outbound queue full, frame dropped")
