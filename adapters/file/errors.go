package file

import (
	"fmt"
	"log/slog"
)

// ScanError is sent to [Stream.Errors] by [ScanStream] when opening or reading
// the file fails. When Err wraps [gstream.StreamDecodeError], the failure was a
// codec decode error on a specific line; otherwise it is an I/O error.
type ScanError struct {
	// Path is the file path passed to ScanStream.
	Path string
	// Err is the underlying I/O or decode error.
	Err error
}

func (e ScanError) Error() string {
	return fmt.Sprintf("file scan %s: %v", e.Path, e.Err)
}

// Unwrap allows [errors.Is] and [errors.As] to traverse the underlying error.
func (e ScanError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e ScanError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("path", e.Path),
		slog.Any("err", e.Err),
	)
}

// WatchError is sent to [Stream.Errors] by [WatchStream] when a directory read
// fails during a poll cycle. The stream continues on the next poll interval.
type WatchError struct {
	// Dir is the directory being watched.
	Dir string
	// Err is the underlying os.ReadDir error.
	Err error
}

func (e WatchError) Error() string {
	return fmt.Sprintf("file watch %s: %v", e.Dir, e.Err)
}

// Unwrap allows [errors.Is] and [errors.As] to traverse the underlying error.
func (e WatchError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e WatchError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("dir", e.Dir),
		slog.Any("err", e.Err),
	)
}

// WriteError is passed to [DrainWriteOptions.OnError] by [DrainWrite] when
// encoding or writing an item to the writer fails.
type WriteError struct {
	// Path is the file path, when known. Empty when writing to a non-file writer.
	Path string
	// Err is the underlying encode or write error.
	Err error
}

func (e WriteError) Error() string {
	if e.Path != "" {
		return fmt.Sprintf("file write %s: %v", e.Path, e.Err)
	}
	return fmt.Sprintf("file write: %v", e.Err)
}

// Unwrap allows [errors.Is] and [errors.As] to traverse the underlying error.
func (e WriteError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e WriteError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("path", e.Path),
		slog.Any("err", e.Err),
	)
}
