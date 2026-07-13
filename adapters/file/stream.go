// Package file provides stream bridge helpers for file-based sources and sinks.
//
// All helpers are stdlib-only (no external dependencies). They integrate with the
// go-codex stream package for codec-aware file I/O.
//
// Sources:
//   - [ScanStream] — decodes a newline-delimited file (NDJSON, CSV, etc.) line by line
//   - [WatchStream] — emits file paths for new files created in a directory
//
// Sinks:
//   - [DrainWrite] — encodes each stream item and writes it as a line to an io.Writer
package file

import (
	"bufio"
	"context"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/stats"
	gstream "github.com/DaniDeer/go-codex/stream"
)

// ── ScanStream ────────────────────────────────────────────────────────────────

// ScanStream opens path and emits each newline-terminated line as a decoded T.
// Decode or validation failures are sent to [gstream.Stream.Errors] as
// [gstream.StreamDecodeError]. I/O errors (open, read) are sent as [ScanError].
// The stream terminates after EOF or ctx cancellation.
//
// ScanStream is bounded — it terminates when the file is fully read. Use
// [gstream.Collect] for bounded aggregation, or chain into other operators.
func ScanStream[T any](
	ctx context.Context,
	path string,
	fmt format.Format[T],
	opts gstream.SourceOptions,
) (gstream.Stream[T], error) {
	f, err := os.Open(path)
	if err != nil {
		return gstream.Stream[T]{}, ScanError{Path: path, Err: err}
	}

	if opts.Name == "" {
		opts.Name = filepath.Base(path)
	}

	rawCh := make(chan []byte, opts.Buffer)
	go func() {
		defer close(rawCh)
		defer f.Close()
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			select {
			case <-ctx.Done():
				return
			case rawCh <- []byte(scanner.Text()):
			}
		}
	}()

	return gstream.FromCodec(ctx, rawCh, fmt, opts), nil
}

// ── WatchStream ───────────────────────────────────────────────────────────────

// WatchStream emits the absolute path of each new file created in dir.
// It polls dir using [os.ReadDir] at interval — no external inotify dependency.
// [WatchError] is sent to Stream.Errors on each failed ReadDir but the stream
// continues until ctx is cancelled.
//
// Only newly seen files (not present in the previous poll) are emitted.
// Deleted files are not reported. Subdirectories are not traversed.
func WatchStream(
	ctx context.Context,
	dir string,
	interval time.Duration,
	opts gstream.SourceOptions,
) gstream.Stream[string] {
	values := make(chan string, opts.Buffer)
	errs := make(chan error, opts.Buffer)
	go func() {
		defer close(values)
		defer close(errs)
		known := make(map[string]struct{})
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				entries, err := os.ReadDir(dir)
				if err != nil {
					select {
					case errs <- WatchError{Dir: dir, Err: err}:
					case <-ctx.Done():
						return
					}
					continue
				}
				for _, e := range entries {
					if e.IsDir() {
						continue
					}
					name := e.Name()
					if _, seen := known[name]; seen {
						continue
					}
					known[name] = struct{}{}
					abs := filepath.Join(dir, name)
					select {
					case values <- abs:
					case <-ctx.Done():
						return
					}
				}
			}
		}
	}()
	return gstream.Stream[string]{Values: values, Errors: errs}
}

// ── DrainWrite ────────────────────────────────────────────────────────────────

// DrainWriteOptions configures [DrainWrite].
type DrainWriteOptions struct {
	// OnError, when non-nil, is called for encode failures ([WriteError]) or
	// upstream stream errors.
	OnError func(error)
	// Path is the file path, used in [WriteError] context. Empty is allowed (any io.Writer).
	Path string
	// Separator is written after each item. Defaults to "\n" when empty.
	Separator string
	// Observer receives per-item encode/write lifecycle events.
	// [stats.Observer.RecordValidationError] fires for encode failures (per-field
	// codec constraint violations reported via [stats.ReportErrors]).
	// Defaults to [stats.NoopObserver] when nil.
	Observer stats.Observer
}

// DrainWrite encodes each value item from src using fmt and writes it to w,
// followed by opts.Separator (default "\n"). Encode or write failures are
// passed to opts.OnError as [WriteError]. Upstream stream errors are forwarded
// to opts.OnError unchanged.
//
// Blocks until src terminates or ctx is cancelled.
//
// Use DrainWrite to persist a stream to a newline-delimited file (NDJSON):
//
//	f, _ := os.Create("oee-results.ndjson")
//	defer f.Close()
//	file.DrainWrite(ctx, f, oeeStream, format.JSON(oeeCodec),
//	    file.DrainWriteOptions{Path: "oee-results.ndjson", OnError: logErr})
func DrainWrite[T any](
	ctx context.Context,
	w io.Writer,
	src gstream.Stream[T],
	fmt format.Format[T],
	opts DrainWriteOptions,
) {
	sep := opts.Separator
	if sep == "" {
		sep = "\n"
	}
	onErr := opts.OnError
	obs := opts.Observer
	if obs == nil {
		obs = stats.NoopObserver{}
	}

	gstream.Drain(ctx, src,
		func(_ context.Context, v T) error {
			data, err := fmt.Marshal(v)
			if err != nil {
				stats.ReportErrors(obs, "file", err)
				we := WriteError{Path: opts.Path, Err: err}
				if onErr != nil {
					onErr(we)
				}
				return nil
			}
			if _, err := w.Write(append(data, []byte(sep)...)); err != nil {
				we := WriteError{Path: opts.Path, Err: err}
				if onErr != nil {
					onErr(we)
				}
			}
			return nil
		},
		func(e error) {
			if onErr != nil {
				onErr(e)
			}
		},
		gstream.DrainOptions{Observer: obs},
	)
}
