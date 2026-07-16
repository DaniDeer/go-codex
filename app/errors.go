package app

import (
	"fmt"
	"log/slog"
)

// GoroutineError wraps a supervised goroutine's non-nil return. The first
// GoroutineError cancels the app (fail-fast); all of them appear in the
// errors.Join result of [App.Run]/[App.Shutdown].
type GoroutineError struct {
	// Name is the name passed to [App.Go].
	Name string
	// Err is the error the goroutine returned.
	Err error
}

func (e GoroutineError) Error() string {
	return fmt.Sprintf("app: goroutine %q failed: %v", e.Name, e.Err)
}

// Unwrap allows errors.Is and errors.As to traverse the inner error.
func (e GoroutineError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e GoroutineError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("name", e.Name),
		slog.Any("err", e.Err),
	)
}

// HookError wraps a shutdown hook's non-nil return — including
// context.DeadlineExceeded when the hook exceeded [Options.ShutdownTimeout].
// A failing hook never stops later hooks; all HookErrors appear in the
// errors.Join result of [App.Run]/[App.Shutdown].
type HookError struct {
	// Name is the name passed to [App.OnShutdown].
	Name string
	// Err is the error the hook returned.
	Err error
}

func (e HookError) Error() string {
	return fmt.Sprintf("app: shutdown hook %q failed: %v", e.Name, e.Err)
}

// Unwrap allows errors.Is and errors.As to traverse the inner error.
func (e HookError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e HookError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("name", e.Name),
		slog.Any("err", e.Err),
	)
}
