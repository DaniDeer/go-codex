// Package file provides stream bridge helpers for file-based sources and sinks.
//
// All helpers are stdlib-only (no external dependencies). They integrate with the
// go-codex stream package for codec-aware file I/O.
//
// Sources:
//   - [ScanStream] — decodes a newline-delimited file (NDJSON, CSV, etc.) line by line
//   - [WatchStream] — emits file paths for new files created in a directory
//   - [ReadEachStream] — reads a complete typed file for each upstream item (enrichment)
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

// Deprecated: Use [ScanAdapter] with [ports.SourcePort] instead.
//
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

// Deprecated: Use [WatchAdapter] with [ports.SourcePort] instead.
//
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

// Deprecated: Use [DrainWriteAdapter] with [ports.SinkPort] instead.
//
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
		obs = stats.ObserverFromContext(ctx)
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

// ── ReadEachStream ────────────────────────────────────────────────────────────

// ReadEachStreamOptions configures [ReadEachStream].
type ReadEachStreamOptions struct {
	// OnError, when non-nil, is called for file read errors ([ReadEachError])
	// and upstream stream errors. When nil, errors are only sent to Stream.Errors.
	OnError func(error)
	// Observer receives per-read lifecycle events.
	// Resolved from ctx when nil.
	Observer stats.Observer
	// FileOptions configures permission, context, and observer for each read.
	// Observer and Context within FileOptions are set automatically from this
	// option's Observer and ctx if not already set.
	FileOptions format.FileOptions
	// Buffer sets the channel buffer size for the output stream.
	Buffer int
}

// Deprecated: Use [ReadEachAdapter] with [ports.IOPort] instead.
//
// ReadEachStream reads a complete typed file for each item in src and combines
// the result using combine. This is the enrichment bridge: for each upstream In
// item, varsFor derives the file path variables, the file is read and decoded,
// and combine pairs the original item with the file content to produce Out.
//
// Read failures are sent to [gstream.Stream.Errors] as [ReadError] and
// also passed to opts.OnError when set. The upstream item that triggered the
// failure is dropped. Upstream stream errors are forwarded to Stream.Errors.
//
// Example — enrich sensor readings with calibration data stored per-sensor:
//
//	calibrated := file.ReadEachStream(ctx, calibrationFile, sensorStream,
//	    func(r SensorReading) map[string]string { return map[string]string{"sensorID": r.SensorID} },
//	    func(r SensorReading, c CalibrationData) CalibratedReading {
//	        return CalibratedReading{Reading: r, Offset: c.Offset}
//	    },
//	    file.ReadEachStreamOptions{})
func ReadEachStream[In, T, Out any](
	ctx context.Context,
	f format.File[T],
	src gstream.Stream[In],
	varsFor func(In) map[string]string,
	combine func(In, T) Out,
	opts ReadEachStreamOptions,
) gstream.Stream[Out] {
	outCh := make(chan Out, opts.Buffer)
	errCh := make(chan error, opts.Buffer)

	obs := opts.Observer
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}

	fileOpts := opts.FileOptions
	if fileOpts.Observer == nil {
		fileOpts.Observer = obs
	}
	if fileOpts.Context == nil {
		fileOpts.Context = ctx
	}
	onErr := opts.OnError

	go func() {
		defer close(outCh)
		defer close(errCh)
		for {
			select {
			case <-ctx.Done():
				return
			case v, ok := <-src.Values:
				if !ok {
					// Values exhausted — drain remaining errors before closing.
					for {
						select {
						case e, ok := <-src.Errors:
							if !ok {
								return
							}
							if onErr != nil {
								onErr(e)
							}
							select {
							case errCh <- e:
							case <-ctx.Done():
								return
							}
						case <-ctx.Done():
							return
						}
					}
				}
				t, err := f.Read(varsFor(v), fileOpts)
				if err != nil {
					re := ReadError{Err: err}
					if onErr != nil {
						onErr(re)
					}
					select {
					case errCh <- re:
					case <-ctx.Done():
						return
					}
					continue
				}
				select {
				case outCh <- combine(v, t):
				case <-ctx.Done():
					return
				}
			case e, ok := <-src.Errors:
				if !ok {
					return
				}
				if onErr != nil {
					onErr(e)
				}
				select {
				case errCh <- e:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return gstream.Stream[Out]{Values: outCh, Errors: errCh}
}

// ── TapWriteFile / DrainWriteFile ─────────────────────────────────────────────

// TapWriteFileOptions configures [TapWriteFile].
type TapWriteFileOptions struct {
	// OnError, when non-nil, is called for write or encode failures.
	// Errors are typed: [format.FileWriteError], [format.FileEncodeError],
	// [format.FilePathParamError], [format.MissingFilePathVarError].
	OnError func(error)

	// Observer receives per-write lifecycle events via the [format.File]
	// observer integration ([stats.FileObserver.RecordFileWrite]).
	// Resolved from ctx when nil.
	Observer stats.Observer

	// FileOptions configures permission, context, and observer for each write.
	// Observer and Context within FileOptions are set automatically from this
	// option's Observer and ctx if not already set.
	FileOptions format.FileOptions
}

// Deprecated: Use [DrainWriteFileAdapter] with [ports.SinkPort] or [DrainWriteFile] instead.
//
// TapWriteFile writes each stream item as a complete typed file on every item
// using [format.File.Write]. The stream continues flowing after each write —
// use TapWriteFile when file write is one of multiple side-effects (publish,
// dashboard, file).
//
// Use [DrainWriteFile] when the file write is the terminal step and the stream
// should be fully consumed.
//
// Errors from [format.File.Write] are passed to opts.OnError and are typed
// ([format.FileWriteError], [format.FileEncodeError]) — use [errors.As] for
// structured handling.
func TapWriteFile[T any](
	ctx context.Context,
	f format.File[T],
	src gstream.Stream[T],
	vars map[string]string,
	opts TapWriteFileOptions,
) gstream.Stream[T] {
	fileOpts := opts.FileOptions
	if fileOpts.Observer == nil {
		obs := opts.Observer
		if obs == nil {
			obs = stats.ObserverFromContext(ctx)
		}
		fileOpts.Observer = obs
	}
	if fileOpts.Context == nil {
		fileOpts.Context = ctx
	}
	onErr := opts.OnError
	return gstream.Tap(ctx, src, func(v T) {
		if err := f.Write(vars, v, fileOpts); err != nil {
			if onErr != nil {
				onErr(err)
			}
		}
	})
}

// DrainWriteFileOptions configures [DrainWriteFile].
type DrainWriteFileOptions struct {
	// OnError, when non-nil, is called for write or encode failures and
	// upstream stream errors.
	OnError func(error)

	// Observer receives per-write lifecycle events.
	// Resolved from ctx when nil.
	Observer stats.Observer

	// FileOptions configures permission, context, and observer for each write.
	FileOptions format.FileOptions
}

// Deprecated: Use [DrainWriteFileAdapter] with [ports.SinkPort] instead.
//
// DrainWriteFile writes each stream item as a complete typed file (terminal sink)
// using [format.File.Write]. Blocks until src terminates or ctx is cancelled.
//
// Use [TapWriteFile] when the file write is one of multiple side-effects and the
// stream should keep flowing.
func DrainWriteFile[T any](
	ctx context.Context,
	f format.File[T],
	src gstream.Stream[T],
	vars map[string]string,
	opts DrainWriteFileOptions,
) {
	obs := opts.Observer
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}
	fileOpts := opts.FileOptions
	if fileOpts.Observer == nil {
		fileOpts.Observer = obs
	}
	if fileOpts.Context == nil {
		fileOpts.Context = ctx
	}
	onErr := opts.OnError
	gstream.Drain(ctx, src,
		func(_ context.Context, v T) error {
			if err := f.Write(vars, v, fileOpts); err != nil {
				if onErr != nil {
					onErr(err)
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
