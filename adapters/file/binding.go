package file

import (
	"context"
	"io"
	"time"

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
	s, err := ScanStream(ctx, a.path, a.fmt, gstream.SourceOptions{Buffer: a.opts.Buffer, Observer: a.opts.Observer})
	if err != nil {
		select {
		case errs <- err:
		case <-ctx.Done():
		}
		return
	}
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
	s := WatchStream(ctx, a.dir, a.interval, gstream.SourceOptions{Buffer: a.opts.Buffer, Observer: a.opts.Observer})
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

// ── ReadEachAdapter ───────────────────────────────────────────────────────────

// ReadEachAdapter returns a [ports.IOAdapter] that reads a complete typed file
// for each In item, combining the result. Use with [ports.IOPort.Bind]:
//
//	domain.Calibration.Bind(ctx, file.ReadEachAdapter(calibrationFile,
//	    func(r SensorReading) map[string]string { return map[string]string{"id": r.SensorID} },
//	    func(r SensorReading, c CalibrationData) CalibratedReading { return ... },
//	    file.ReadEachAdapterOptions{}))
func ReadEachAdapter[In, T, Resp any](
	f format.File[T],
	varsFor func(In) map[string]string,
	combine func(In, T) Resp,
	opts ReadEachAdapterOptions,
) ports.IOAdapter[In, Resp] {
	return &fileReadEachAdapter[In, T, Resp]{f: f, varsFor: varsFor, combine: combine, opts: opts}
}

// ReadEachAdapterOptions configures [ReadEachAdapter].
type ReadEachAdapterOptions struct {
	Observer    stats.Observer
	FileOptions format.FileOptions
	Buffer      int
}

type fileReadEachAdapter[In, T, Resp any] struct {
	f       format.File[T]
	varsFor func(In) map[string]string
	combine func(In, T) Resp
	opts    ReadEachAdapterOptions
}

func (a *fileReadEachAdapter[In, T, Resp]) AdapterName() string { return "file.ReadEachAdapter" }

func (a *fileReadEachAdapter[In, T, Resp]) Transform(ctx context.Context, src gstream.Stream[In]) gstream.Stream[Resp] {
	return ReadEachStream(ctx, a.f, src, a.varsFor, a.combine, ReadEachStreamOptions{
		Observer:    a.opts.Observer,
		FileOptions: a.opts.FileOptions,
		Buffer:      a.opts.Buffer,
	})
}

// ── DrainWriteAdapter ─────────────────────────────────────────────────────────

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

// DrainWriteAdapterOptions configures [DrainWriteAdapter].
type DrainWriteAdapterOptions struct {
	Path      string
	Separator string
	Observer  stats.Observer
	OnError   func(error)
}

type fileDrainWriteAdapter[T any] struct {
	w    io.Writer
	fmt  format.Format[T]
	opts DrainWriteAdapterOptions
}

func (a *fileDrainWriteAdapter[T]) AdapterName() string { return "file.DrainWriteAdapter" }

func (a *fileDrainWriteAdapter[T]) Activate(ctx context.Context, src gstream.Stream[T]) {
	DrainWrite(ctx, a.w, src, a.fmt, DrainWriteOptions{
		Path:      a.opts.Path,
		Separator: a.opts.Separator,
		Observer:  a.opts.Observer,
		OnError:   a.opts.OnError,
	})
}

// ── DrainWriteFileAdapter ─────────────────────────────────────────────────────

// DrainWriteFileAdapter returns a [ports.SinkAdapter] that writes each item as
// a complete typed file (whole-file overwrite). Use with [ports.SinkPort.Bind]:
//
//	domain.OEEResults.Bind(ctx, file.DrainWriteFileAdapter(resultFile,
//	    func(oee OEE) map[string]string { return map[string]string{"machineID": oee.MachineID} },
//	    file.DrainWriteFileAdapterOptions{}))
func DrainWriteFileAdapter[T any](
	f format.File[T],
	varsFor func(T) map[string]string,
	opts DrainWriteFileAdapterOptions,
) ports.SinkAdapter[T] {
	return &fileDrainWriteFileAdapter[T]{f: f, varsFor: varsFor, opts: opts}
}

// DrainWriteFileAdapterOptions configures [DrainWriteFileAdapter].
type DrainWriteFileAdapterOptions struct {
	Observer    stats.Observer
	FileOptions format.FileOptions
	OnError     func(error)
}

type fileDrainWriteFileAdapter[T any] struct {
	f       format.File[T]
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
	gstream.Drain(ctx, src,
		func(_ context.Context, v T) error {
			var vars map[string]string
			if a.varsFor != nil {
				vars = a.varsFor(v)
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
