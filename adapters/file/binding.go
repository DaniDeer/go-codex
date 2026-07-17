// Package file provides protocol-agnostic file IO adapter bindings for the ports package.
//
// All adapters are stdlib-only (no external dependencies). They implement the
// [ports.SourceAdapter], [ports.SinkAdapter], and [ports.IOAdapter] interfaces and
// are wired to pipelines via [ports.SourcePort.Bind], [ports.SinkPort.Bind], and
// [ports.IOPort.Bind].
//
// Sources (use with [ports.SourcePort]):
//   - [ScanAdapter] — decodes a newline-delimited file (NDJSON, CSV, etc.) line by line
//   - [WatchAdapter] — emits file paths for new files created in a directory
//
// Intermediate (use with [ports.IOPort]):
//   - [ReadEachAdapter] — reads a complete typed file for each upstream item (enrichment)
//
// Sinks (use with [ports.SinkPort]):
//   - [DrainWriteAdapter] — encodes each item and writes it as a line to an [io.Writer]
//   - [DrainWriteFileAdapter] — writes each item as a complete typed file (whole-file overwrite)
//   - [DrainPatchAdapter] — applies each item as an untyped map[string]any partial
//     update (JSON Merge Patch semantics) via [ports.File.Patch]; map-based formats
//     (JSON/YAML/TOML/[format.New]) only
//   - [DrainPatchEncodedAdapter] — applies each item as a typed partial update via
//     [ports.PatchEncoded]; same map-based-format restriction
package file

import (
	"bufio"
	"context"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/ports"
	"github.com/DaniDeer/go-codex/stats"
	gstream "github.com/DaniDeer/go-codex/stream"
)

// ── ScanAdapter ───────────────────────────────────────────────────────────────

// ScanAdapterOptions configures [ScanAdapter].
type ScanAdapterOptions struct {
	Observer stats.Observer
	Buffer   int
}

// ScanAdapter returns a [ports.SourceAdapter] that reads a file line-by-line,
// decoding each line. When the file is fully read the adapter exits. Use with
// [ports.SourcePort.Bind]:
//
//	domain.Readings.Bind(ctx, file.ScanAdapter("readings.ndjson", format.JSON(readingCodec),
//	    file.ScanAdapterOptions{}))
func ScanAdapter[T any](path string, fmt format.Format[T], opts ScanAdapterOptions) ports.SourceAdapter[T] {
	return &fileScanAdapter[T]{path: path, fmt: fmt, opts: opts}
}

type fileScanAdapter[T any] struct {
	path string
	fmt  format.Format[T]
	opts ScanAdapterOptions
}

func (a *fileScanAdapter[T]) AdapterName() string { return "file.ScanAdapter" }

func (a *fileScanAdapter[T]) Activate(ctx context.Context, dst chan<- T, errs chan<- error) {
	f, err := os.Open(a.path)
	if err != nil {
		select {
		case errs <- ScanError{Path: a.path, Err: err}:
		case <-ctx.Done():
		}
		return
	}

	name := filepath.Base(a.path)
	rawCh := make(chan []byte, a.opts.Buffer)
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

	s := gstream.FromCodec(ctx, rawCh, a.fmt, gstream.SourceOptions{Buffer: a.opts.Buffer, Name: name, Observer: a.opts.Observer})
	valCh := s.Values
	errCh := s.Errors
	for valCh != nil || errCh != nil {
		select {
		case <-ctx.Done():
			return
		case v, ok := <-valCh:
			if !ok {
				valCh = nil
				continue
			}
			select {
			case dst <- v:
			case <-ctx.Done():
				return
			}
		case e, ok := <-errCh:
			if !ok {
				errCh = nil
				continue
			}
			select {
			case errs <- e:
			case <-ctx.Done():
				return
			}
		}
	}
}

// ── WatchAdapter ──────────────────────────────────────────────────────────────

// WatchAdapterOptions configures [WatchAdapter].
type WatchAdapterOptions struct {
	Observer stats.Observer
	Buffer   int
}

// WatchAdapter returns a [ports.SourceAdapter] that emits file paths for new
// files created in dir. Runs until ctx is cancelled. Use with [ports.SourcePort.Bind]:
//
//	domain.NewFiles.Bind(ctx, file.WatchAdapter("/data/incoming", 5*time.Second,
//	    file.WatchAdapterOptions{}))
func WatchAdapter(dir string, interval time.Duration, opts WatchAdapterOptions) ports.SourceAdapter[string] {
	return &fileWatchAdapter{dir: dir, interval: interval, opts: opts}
}

type fileWatchAdapter struct {
	dir      string
	interval time.Duration
	opts     WatchAdapterOptions
}

func (a *fileWatchAdapter) AdapterName() string { return "file.WatchAdapter" }

func (a *fileWatchAdapter) Activate(ctx context.Context, dst chan<- string, errs chan<- error) {
	known := make(map[string]struct{})
	ticker := time.NewTicker(a.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			entries, err := os.ReadDir(a.dir)
			if err != nil {
				select {
				case errs <- WatchError{Dir: a.dir, Err: err}:
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
				abs := filepath.Join(a.dir, name)
				select {
				case dst <- abs:
				case <-ctx.Done():
					return
				}
			}
		}
	}
}

// ── ReadEachAdapter ───────────────────────────────────────────────────────────

// ReadEachAdapterOptions configures [ReadEachAdapter].
type ReadEachAdapterOptions struct {
	Observer    stats.Observer
	FileOptions ports.FileOptions
	Buffer      int
}

// ReadEachAdapter returns a [ports.IOAdapter] that reads a complete typed file
// for each In item, combining the result. Use with [ports.IOPort.Bind]:
//
//	domain.Calibration.Bind(ctx, file.ReadEachAdapter(calibrationFile,
//	    func(r SensorReading) map[string]string { return map[string]string{"id": r.SensorID} },
//	    func(r SensorReading, c CalibrationData) CalibratedReading { return ... },
//	    file.ReadEachAdapterOptions{}))
//
// When the bound [ports.IOPort] declares Params, each varsFor result is validated
// with [ports.ValidateParams] before the file read; a validation failure is
// delivered as [ReadError] wrapping [codex.ValidationErrors].
func ReadEachAdapter[In, T, Resp any](
	f ports.File[T],
	varsFor func(In) map[string]string,
	combine func(In, T) Resp,
	opts ReadEachAdapterOptions,
) ports.IOAdapter[In, Resp] {
	return &fileReadEachAdapter[In, T, Resp]{f: f, varsFor: varsFor, combine: combine, opts: opts}
}

type fileReadEachAdapter[In, T, Resp any] struct {
	f       ports.File[T]
	varsFor func(In) map[string]string
	combine func(In, T) Resp
	opts    ReadEachAdapterOptions
}

func (a *fileReadEachAdapter[In, T, Resp]) AdapterName() string { return "file.ReadEachAdapter" }

func (a *fileReadEachAdapter[In, T, Resp]) Transform(ctx context.Context, src gstream.Stream[In]) gstream.Stream[Resp] {
	obs := a.opts.Observer
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}
	fileOpts := a.opts.FileOptions
	if fileOpts.Observer == nil {
		fileOpts.Observer = obs
	}
	if fileOpts.Context == nil {
		fileOpts.Context = ctx
	}
	params := ports.ParamsFromContext(ctx)

	outCh := make(chan Resp, a.opts.Buffer)
	errCh := make(chan error, a.opts.Buffer)

	go func() {
		defer close(outCh)
		defer close(errCh)
		valCh := src.Values
		srcErrCh := src.Errors
		for valCh != nil || srcErrCh != nil {
			select {
			case <-ctx.Done():
				return
			case v, ok := <-valCh:
				if !ok {
					valCh = nil
					continue
				}
				vars := a.varsFor(v)
				if err := ports.ValidateParams(params, vars); err != nil {
					re := ReadError{Err: err}
					select {
					case errCh <- re:
					case <-ctx.Done():
						return
					}
					continue
				}
				t, err := a.f.Read(vars, fileOpts)
				if err != nil {
					re := ReadError{Err: err}
					select {
					case errCh <- re:
					case <-ctx.Done():
						return
					}
					continue
				}
				select {
				case outCh <- a.combine(v, t):
				case <-ctx.Done():
					return
				}
			case e, ok := <-srcErrCh:
				if !ok {
					srcErrCh = nil
					continue
				}
				select {
				case errCh <- e:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return gstream.Stream[Resp]{Values: outCh, Errors: errCh}
}

// ── ReadAdapter ───────────────────────────────────────────────────────────────

// ReadAdapter returns a [ports.IOAdapter] that reads a complete typed file for
// each In item and emits the file content directly as the response — the
// 2-type complement of [ReadEachAdapter] (whose independent file-content type
// and combine func serve enrichment). Pairs with a [ports.FilePattern]
// declared on a ports.IOPort[In, Resp], where the file content IS the port's
// response type:
//
//	calibFile, _ := ports.FileHandle[CalibrationData](domain.Calibration)
//	domain.Calibration.Bind(ctx, file.ReadAdapter(calibFile,
//	    func(r SensorReading) map[string]string {
//	        return map[string]string{"sensorID": r.SensorID}
//	    },
//	    file.ReadEachAdapterOptions{}))
//
// When the bound [ports.IOPort] declares Params, each varsFor result is
// validated with [ports.ValidateParams] before the file read; a validation
// failure is delivered as [ReadError] wrapping [codex.ValidationErrors].
func ReadAdapter[In, Resp any](
	f ports.File[Resp],
	varsFor func(In) map[string]string,
	opts ReadEachAdapterOptions,
) ports.IOAdapter[In, Resp] {
	return &fileReadAdapter[In, Resp]{
		inner: &fileReadEachAdapter[In, Resp, Resp]{
			f:       f,
			varsFor: varsFor,
			combine: func(_ In, t Resp) Resp { return t },
			opts:    opts,
		},
	}
}

type fileReadAdapter[In, Resp any] struct {
	inner *fileReadEachAdapter[In, Resp, Resp]
}

func (a *fileReadAdapter[In, Resp]) AdapterName() string { return "file.ReadAdapter" }

func (a *fileReadAdapter[In, Resp]) Transform(ctx context.Context, src gstream.Stream[In]) gstream.Stream[Resp] {
	return a.inner.Transform(ctx, src)
}

// ── DrainWriteAdapter ─────────────────────────────────────────────────────────

// DrainWriteAdapterOptions configures [DrainWriteAdapter].
type DrainWriteAdapterOptions struct {
	Path      string
	Separator string
	Observer  stats.Observer
	OnError   func(error)
}

// DrainWriteAdapter returns a [ports.SinkAdapter] that encodes each item and
// writes it as a line to w. Use with [ports.SinkPort.Bind]:
//
//	f, _ := os.Create("results.ndjson")
//	domain.OEEResults.Bind(ctx, file.DrainWriteAdapter(f, format.JSON(oeeCodec),
//	    file.DrainWriteAdapterOptions{Path: "results.ndjson"}))
func DrainWriteAdapter[T any](
	w io.Writer,
	fmt format.Format[T],
	opts DrainWriteAdapterOptions,
) ports.SinkAdapter[T] {
	return &fileDrainWriteAdapter[T]{w: w, fmt: fmt, opts: opts}
}

type fileDrainWriteAdapter[T any] struct {
	w    io.Writer
	fmt  format.Format[T]
	opts DrainWriteAdapterOptions
}

func (a *fileDrainWriteAdapter[T]) AdapterName() string { return "file.DrainWriteAdapter" }

func (a *fileDrainWriteAdapter[T]) Activate(ctx context.Context, src gstream.Stream[T]) {
	sep := a.opts.Separator
	if sep == "" {
		sep = "\n"
	}
	onErr := a.opts.OnError
	obs := a.opts.Observer
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}
	gstream.Drain(ctx, src,
		func(_ context.Context, v T) error {
			data, err := a.fmt.Marshal(v)
			if err != nil {
				stats.ReportErrors(obs, "file", err)
				we := WriteError{Path: a.opts.Path, Err: err}
				if onErr != nil {
					onErr(we)
				}
				return nil
			}
			if _, err := a.w.Write(append(data, []byte(sep)...)); err != nil {
				we := WriteError{Path: a.opts.Path, Err: err}
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

// ── DrainWriteFileAdapter ─────────────────────────────────────────────────────

// DrainWriteFileAdapterOptions configures [DrainWriteFileAdapter].
type DrainWriteFileAdapterOptions struct {
	Observer    stats.Observer
	FileOptions ports.FileOptions
	OnError     func(error)
}

// DrainWriteFileAdapter returns a [ports.SinkAdapter] that writes each item as
// a complete typed file (whole-file overwrite). Use with [ports.SinkPort.Bind]:
//
//	domain.OEEResults.Bind(ctx, file.DrainWriteFileAdapter(resultFile,
//	    func(oee OEE) map[string]string { return map[string]string{"machineID": oee.MachineID} },
//	    file.DrainWriteFileAdapterOptions{}))
//
// When the bound [ports.SinkPort] declares Params, each varsFor result is
// validated with [ports.ValidateParams] before the file write; a validation
// failure is reported to Options.OnError as [WriteError] wrapping
// [codex.ValidationErrors] and the item is otherwise skipped (not written).
func DrainWriteFileAdapter[T any](
	f ports.File[T],
	varsFor func(T) map[string]string,
	opts DrainWriteFileAdapterOptions,
) ports.SinkAdapter[T] {
	return &fileDrainWriteFileAdapter[T]{f: f, varsFor: varsFor, opts: opts}
}

type fileDrainWriteFileAdapter[T any] struct {
	f       ports.File[T]
	varsFor func(T) map[string]string
	opts    DrainWriteFileAdapterOptions
}

func (a *fileDrainWriteFileAdapter[T]) AdapterName() string { return "file.DrainWriteFileAdapter" }

func (a *fileDrainWriteFileAdapter[T]) Activate(ctx context.Context, src gstream.Stream[T]) {
	obs := a.opts.Observer
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}
	fileOpts := a.opts.FileOptions
	if fileOpts.Observer == nil {
		fileOpts.Observer = obs
	}
	if fileOpts.Context == nil {
		fileOpts.Context = ctx
	}
	onErr := a.opts.OnError
	params := ports.ParamsFromContext(ctx)
	gstream.Drain(ctx, src,
		func(_ context.Context, v T) error {
			var vars map[string]string
			if a.varsFor != nil {
				vars = a.varsFor(v)
			}
			if err := ports.ValidateParams(params, vars); err != nil {
				if onErr != nil {
					onErr(WriteError{Err: err})
				}
				return nil
			}
			if err := a.f.Write(vars, v, fileOpts); err != nil {
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

// ── DrainPatchAdapter ──────────────────────────────────────────────────────────

// DrainPatchAdapterOptions configures [DrainPatchAdapter].
type DrainPatchAdapterOptions struct {
	Observer    stats.Observer
	FileOptions ports.FileOptions
	OnError     func(error)
}

// DrainPatchAdapter returns a [ports.SinkAdapter] that applies each item as a
// partial update (JSON Merge Patch semantics) to an existing typed file via
// [ports.File.Patch], instead of overwriting the whole file like
// [DrainWriteFileAdapter]. f is built by hand (not via [ports.FilePattern]):
// the stream's item type (map[string]any) is deliberately different from f's
// own type T, the same way [ReadEachAdapter] takes an independent content
// type. Use with [ports.SinkPort.Bind]:
//
//	domain.ConfigUpdates.Bind(ctx, file.DrainPatchAdapter(configFile,
//	    func(_ map[string]any) map[string]string { return nil },
//	    file.DrainPatchAdapterOptions{}))
//
// f's format must be map-based (JSON, YAML, TOML, or [format.New]); otherwise
// every item fails with [ports.FilePatchNotSupportedError] (passed through
// to Options.OnError unchanged — see below).
//
// When the bound [ports.SinkPort] declares Params, each varsFor result is
// validated with [ports.ValidateParams] before the patch; a validation
// failure is reported to Options.OnError as [WriteError] wrapping
// [codex.ValidationErrors] and the item is otherwise skipped (not patched).
// A [ports.File.Patch] failure (including [ports.FilePatchNotSupportedError])
// is passed to Options.OnError unchanged, mirroring [DrainWriteFileAdapter]'s
// treatment of [ports.File.Write] failures.
func DrainPatchAdapter[T any](
	f ports.File[T],
	varsFor func(map[string]any) map[string]string,
	opts DrainPatchAdapterOptions,
) ports.SinkAdapter[map[string]any] {
	return &fileDrainPatchAdapter[T]{f: f, varsFor: varsFor, opts: opts}
}

type fileDrainPatchAdapter[T any] struct {
	f       ports.File[T]
	varsFor func(map[string]any) map[string]string
	opts    DrainPatchAdapterOptions
}

func (a *fileDrainPatchAdapter[T]) AdapterName() string { return "file.DrainPatchAdapter" }

func (a *fileDrainPatchAdapter[T]) Activate(ctx context.Context, src gstream.Stream[map[string]any]) {
	obs := a.opts.Observer
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}
	fileOpts := a.opts.FileOptions
	if fileOpts.Observer == nil {
		fileOpts.Observer = obs
	}
	if fileOpts.Context == nil {
		fileOpts.Context = ctx
	}
	onErr := a.opts.OnError
	params := ports.ParamsFromContext(ctx)
	gstream.Drain(ctx, src,
		func(_ context.Context, v map[string]any) error {
			var vars map[string]string
			if a.varsFor != nil {
				vars = a.varsFor(v)
			}
			if err := ports.ValidateParams(params, vars); err != nil {
				if onErr != nil {
					onErr(WriteError{Err: err})
				}
				return nil
			}
			if err := a.f.Patch(vars, v, fileOpts); err != nil {
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

// ── DrainPatchEncodedAdapter ───────────────────────────────────────────────────

// DrainPatchEncodedAdapterOptions configures [DrainPatchEncodedAdapter].
type DrainPatchEncodedAdapterOptions struct {
	Observer    stats.Observer
	FileOptions ports.FileOptions
	OnError     func(error)
}

// DrainPatchEncodedAdapter returns a [ports.SinkAdapter] that applies each
// item as a typed partial update to an existing typed file via
// [ports.PatchEncoded]. Unlike [DrainPatchAdapter]'s untyped map[string]any
// patches, patchCodec fields not present in f's own codec are still
// persisted — the right choice for intentionally adding new fields to a file.
// f is built by hand (not via [ports.FilePattern]): the stream's item type P
// is deliberately different from f's own type T. Use with
// [ports.SinkPort.Bind]:
//
//	domain.ConfigUpdates.Bind(ctx, file.DrainPatchEncodedAdapter(configFile, configPatchCodec,
//	    func(_ AppConfigPatch) map[string]string { return nil },
//	    file.DrainPatchEncodedAdapterOptions{}))
//
// f's format must be map-based (JSON, YAML, TOML, or [format.New]); otherwise
// every item fails with [ports.FilePatchNotSupportedError] (passed through
// to Options.OnError unchanged — see below).
//
// When the bound [ports.SinkPort] declares Params, each varsFor result is
// validated with [ports.ValidateParams] before the patch; a validation
// failure is reported to Options.OnError as [WriteError] wrapping
// [codex.ValidationErrors] and the item is otherwise skipped (not patched).
// A [ports.PatchEncoded] failure (including [ports.FilePatchNotSupportedError])
// is passed to Options.OnError unchanged, mirroring [DrainWriteFileAdapter]'s
// treatment of [ports.File.Write] failures.
func DrainPatchEncodedAdapter[T, P any](
	f ports.File[T],
	patchCodec codex.Codec[P],
	varsFor func(P) map[string]string,
	opts DrainPatchEncodedAdapterOptions,
) ports.SinkAdapter[P] {
	return &fileDrainPatchEncodedAdapter[T, P]{f: f, patchCodec: patchCodec, varsFor: varsFor, opts: opts}
}

type fileDrainPatchEncodedAdapter[T, P any] struct {
	f          ports.File[T]
	patchCodec codex.Codec[P]
	varsFor    func(P) map[string]string
	opts       DrainPatchEncodedAdapterOptions
}

func (a *fileDrainPatchEncodedAdapter[T, P]) AdapterName() string {
	return "file.DrainPatchEncodedAdapter"
}

func (a *fileDrainPatchEncodedAdapter[T, P]) Activate(ctx context.Context, src gstream.Stream[P]) {
	obs := a.opts.Observer
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}
	fileOpts := a.opts.FileOptions
	if fileOpts.Observer == nil {
		fileOpts.Observer = obs
	}
	if fileOpts.Context == nil {
		fileOpts.Context = ctx
	}
	onErr := a.opts.OnError
	params := ports.ParamsFromContext(ctx)
	gstream.Drain(ctx, src,
		func(_ context.Context, v P) error {
			var vars map[string]string
			if a.varsFor != nil {
				vars = a.varsFor(v)
			}
			if err := ports.ValidateParams(params, vars); err != nil {
				if onErr != nil {
					onErr(WriteError{Err: err})
				}
				return nil
			}
			if err := ports.PatchEncoded(a.f, vars, a.patchCodec, v, fileOpts); err != nil {
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
