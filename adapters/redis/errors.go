package redis

import (
	"errors"
	"fmt"
	"log/slog"
)

// ErrCacheMiss is the sentinel for a missing cache key. [Commands.Get]
// implementations return it (wrapped or bare) when the key does not exist;
// [GetAdapter] maps it to skip-or-error per [GetAdapterOptions.MissIsError].
// Test with errors.Is — it survives [CacheError] wrapping via Unwrap.
var ErrCacheMiss = errors.New("redis: cache miss")

// CacheError wraps any cache operation failure: key building, transport,
// encode/decode, or a miss surfaced as an error.
type CacheError struct {
	// Key is the expanded cache key (e.g. "user:42"). May be empty when key
	// building itself failed.
	Key string
	// Op is the operation: "get", "set", or "del".
	Op string
	// Err is the underlying error.
	Err error
}

func (e CacheError) Error() string {
	return fmt.Sprintf("redis: %s %s: %v", e.Op, e.Key, e.Err)
}

// Unwrap allows [errors.Is] and [errors.As] to reach the underlying error,
// including [ErrCacheMiss] and [codex.ValidationErrors].
func (e CacheError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e CacheError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("key", e.Key),
		slog.String("op", e.Op),
		slog.Any("err", e.Err),
	)
}
