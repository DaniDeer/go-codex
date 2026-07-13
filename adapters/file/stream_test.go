package file_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	fileadapter "github.com/DaniDeer/go-codex/adapters/file"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/stats"
	gstream "github.com/DaniDeer/go-codex/stream"
	"github.com/DaniDeer/go-codex/validate"
)

type reading struct {
	Name  string
	Value float64
}

var readingCodec = codex.Struct[reading](
	codex.RequiredField("name", codex.String().Refine(validate.NonEmptyString),
		func(r reading) string { return r.Name },
		func(r *reading, v string) { r.Name = v },
	),
	codex.RequiredField("value", codex.Float64().Refine(validate.PositiveFloat),
		func(r reading) float64 { return r.Value },
		func(r *reading, v float64) { r.Value = v },
	),
)

// ── ScanStream ────────────────────────────────────────────────────────────────

func TestScanStream_DecodesLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "readings.ndjson")
	content := `{"name":"s1","value":1.0}` + "\n" + `{"name":"s2","value":2.0}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	ctx := context.Background()
	s, err := fileadapter.ScanStream(ctx, path, format.JSON(readingCodec),
		gstream.SourceOptions{Name: "test.ndjson"})
	if err != nil {
		t.Fatalf("ScanStream: %v", err)
	}

	vals, errs := gstream.Collect(ctx, s)
	if len(errs) != 0 {
		t.Errorf("want 0 errors, got %d: %v", len(errs), errs)
	}
	if len(vals) != 2 {
		t.Fatalf("want 2 values, got %d", len(vals))
	}
	if vals[0].Name != "s1" || vals[1].Name != "s2" {
		t.Errorf("unexpected values: %+v", vals)
	}
}

func TestScanStream_InvalidPathReturnsError(t *testing.T) {
	ctx := context.Background()
	_, err := fileadapter.ScanStream(ctx, "/nonexistent/path/file.json",
		format.JSON(readingCodec), gstream.SourceOptions{})
	if err == nil {
		t.Fatal("want error for nonexistent path, got nil")
	}
	var se fileadapter.ScanError
	if !errors.As(err, &se) {
		t.Errorf("want ScanError, got %T", err)
	}
}

func TestScanStream_DecodeErrorGoesToErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.ndjson")
	content := "not-json\n" + `{"name":"s1","value":1.0}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	ctx := context.Background()
	s, err := fileadapter.ScanStream(ctx, path, format.JSON(readingCodec),
		gstream.SourceOptions{})
	if err != nil {
		t.Fatalf("ScanStream: %v", err)
	}

	vals, errs := gstream.Collect(ctx, s)
	if len(vals) != 1 {
		t.Errorf("want 1 good value, got %d", len(vals))
	}
	if len(errs) != 1 {
		t.Errorf("want 1 decode error, got %d", len(errs))
	}
}

// ── WatchStream ───────────────────────────────────────────────────────────────

func TestWatchStream_EmitsNewFiles(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	s := fileadapter.WatchStream(ctx, dir, 20*time.Millisecond, gstream.SourceOptions{})

	// Create a file after starting the watch.
	time.Sleep(30 * time.Millisecond)
	path := filepath.Join(dir, "new.json")
	if err := os.WriteFile(path, []byte("{}"), 0600); err != nil {
		t.Fatalf("create file: %v", err)
	}

	vals, errs := gstream.Collect(ctx, s)
	if len(errs) != 0 {
		t.Errorf("want 0 errors, got %d: %v", len(errs), errs)
	}
	if len(vals) == 0 {
		t.Error("want at least 1 file path emitted")
	}
	found := false
	for _, v := range vals {
		if strings.HasSuffix(v, "new.json") {
			found = true
		}
	}
	if !found {
		t.Errorf("new.json not in emitted paths: %v", vals)
	}
}

func TestWatchStream_DoesNotDuplicateFiles(t *testing.T) {
	dir := t.TempDir()
	// Create a file before starting watch.
	path := filepath.Join(dir, "existing.json")
	if err := os.WriteFile(path, []byte("{}"), 0600); err != nil {
		t.Fatalf("create file: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	s := fileadapter.WatchStream(ctx, dir, 20*time.Millisecond, gstream.SourceOptions{})
	vals, _ := gstream.Collect(ctx, s)

	// existing.json is seen on first poll and emitted once; subsequent polls skip it.
	count := 0
	for _, v := range vals {
		if strings.HasSuffix(v, "existing.json") {
			count++
		}
	}
	if count != 1 {
		t.Errorf("existing.json should be emitted exactly once, got %d", count)
	}
}

// ── DrainWrite ────────────────────────────────────────────────────────────────

func TestDrainWrite_EncodesAndWrites(t *testing.T) {
	ctx := context.Background()
	ch := make(chan reading, 2)
	ch <- reading{Name: "s1", Value: 1.0}
	ch <- reading{Name: "s2", Value: 2.0}
	close(ch)
	src := gstream.From(ctx, ch)

	var buf bytes.Buffer
	fileadapter.DrainWrite(ctx, &buf, src, format.JSON(readingCodec),
		fileadapter.DrainWriteOptions{})

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Errorf("want 2 lines, got %d: %q", len(lines), buf.String())
	}
	if !strings.Contains(lines[0], "s1") {
		t.Errorf("line 0 missing s1: %q", lines[0])
	}
}

func TestDrainWrite_WriteErrorGoesToOnError(t *testing.T) {
	ctx := context.Background()
	ch := make(chan reading, 1)
	ch <- reading{Name: "s1", Value: 1.0}
	close(ch)
	src := gstream.From(ctx, ch)

	var gotErr error
	fileadapter.DrainWrite(ctx, &errWriter{err: fmt.Errorf("disk full")}, src,
		format.JSON(readingCodec),
		fileadapter.DrainWriteOptions{
			Path:    "/data/out.ndjson",
			OnError: func(e error) { gotErr = e },
		})

	var we fileadapter.WriteError
	if !errors.As(gotErr, &we) {
		t.Errorf("want WriteError, got %T", gotErr)
	}
}

// TestDrainWrite_ObserverReceivesEncodeError verifies that the Observer set on
// DrainWriteOptions receives RecordValidationError when the codec rejects an item.
func TestDrainWrite_ObserverReceivesEncodeError(t *testing.T) {
	ctx := context.Background()

	// A codec that rejects negative values — causes Marshal to fail.
	type item struct{ V float64 }
	c := codex.Struct[item](
		codex.RequiredField("v", codex.Float64().Refine(validate.PositiveFloat),
			func(x item) float64 { return x.V },
			func(x *item, v float64) { x.V = v },
		),
	)
	ch := make(chan item, 1)
	ch <- item{V: -1} // negative → fails PositiveFloat constraint on Marshal
	close(ch)
	src := gstream.From(ctx, ch)

	var valErrCount int
	spy := &fileValidationSpy{onValErr: func() { valErrCount++ }}

	var buf bytes.Buffer
	fileadapter.DrainWrite(ctx, &buf, src, format.JSON(c),
		fileadapter.DrainWriteOptions{
			Observer: spy,
			OnError:  func(_ error) {}, // absorb the WriteError
		})

	if valErrCount == 0 {
		t.Error("want RecordValidationError called for codec-rejected item, got 0 calls")
	}
}

// TestDrainWrite_ContextObserver verifies that the observer stored in ctx via
// stats.WithObserver is used when DrainWriteOptions.Observer is nil.
func TestDrainWrite_ContextObserver(t *testing.T) {
	type item struct{ V float64 }
	c := codex.Struct[item](
		codex.RequiredField("v", codex.Float64().Refine(validate.PositiveFloat),
			func(x item) float64 { return x.V },
			func(x *item, v float64) { x.V = v },
		),
	)
	ch := make(chan item, 1)
	ch <- item{V: -1}
	close(ch)

	var valErrCount int
	spy := &fileValidationSpy{onValErr: func() { valErrCount++ }}
	ctx := stats.WithObserver(context.Background(), spy)

	src := gstream.From(ctx, ch)
	var buf bytes.Buffer
	fileadapter.DrainWrite(ctx, &buf, src, format.JSON(c),
		fileadapter.DrainWriteOptions{}) // no explicit Observer — resolved from ctx

	if valErrCount == 0 {
		t.Error("want context observer RecordValidationError called, got 0 calls")
	}
}

// fileValidationSpy is a minimal stats.Observer that counts RecordValidationError calls.
type fileValidationSpy struct {
	stats.NoopObserver
	onValErr func()
}

func (s *fileValidationSpy) RecordValidationError(_, _, _ string) {
	if s.onValErr != nil {
		s.onValErr()
	}
}

// ── Error type tests ──────────────────────────────────────────────────────────

func attrKeysFile(lv slog.Value) map[string]bool {
	keys := make(map[string]bool)
	for _, a := range lv.Group() {
		keys[a.Key] = true
	}
	return keys
}

func TestScanError_LogValue(t *testing.T) {
	e := fileadapter.ScanError{Path: "/data/file.ndjson", Err: fmt.Errorf("no such file")}
	lv := e.LogValue()
	if lv.Kind() != slog.KindGroup {
		t.Fatalf("want KindGroup, got %v", lv.Kind())
	}
	keys := attrKeysFile(lv)
	for _, k := range []string{"path", "err"} {
		if !keys[k] {
			t.Errorf("LogValue missing %q", k)
		}
	}
}

func TestWatchError_LogValue(t *testing.T) {
	e := fileadapter.WatchError{Dir: "/data", Err: fmt.Errorf("permission denied")}
	lv := e.LogValue()
	if lv.Kind() != slog.KindGroup {
		t.Fatalf("want KindGroup, got %v", lv.Kind())
	}
	keys := attrKeysFile(lv)
	for _, k := range []string{"dir", "err"} {
		if !keys[k] {
			t.Errorf("LogValue missing %q", k)
		}
	}
}

func TestWriteError_LogValue(t *testing.T) {
	e := fileadapter.WriteError{Path: "/data/out.ndjson", Err: fmt.Errorf("disk full")}
	lv := e.LogValue()
	if lv.Kind() != slog.KindGroup {
		t.Fatalf("want KindGroup, got %v", lv.Kind())
	}
	keys := attrKeysFile(lv)
	for _, k := range []string{"path", "err"} {
		if !keys[k] {
			t.Errorf("LogValue missing %q", k)
		}
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

type errWriter struct{ err error }

func (w *errWriter) Write([]byte) (int, error) { return 0, w.err }

// ── TapWriteFile / DrainWriteFile ─────────────────────────────────────────────

func TestTapWriteFile_WritesEachItem(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "result.json")

	type item struct{ V int }
	c := codex.Struct[item](
		codex.RequiredField("v", codex.Int(), func(x item) int { return x.V }, func(x *item, v int) { x.V = v }),
	)
	f := format.NewFile(path, format.JSON(c))

	ch := make(chan item, 2)
	ch <- item{V: 1}
	ch <- item{V: 2}
	close(ch)
	src := gstream.From(ctx, ch)

	out := fileadapter.TapWriteFile(ctx, f, src, nil, fileadapter.TapWriteFileOptions{})
	// Drain the output stream to let Tap fire
	vals, _ := gstream.Collect(ctx, out)
	if len(vals) != 2 {
		t.Errorf("want 2 items through Tap, got %d", len(vals))
	}

	// Last write wins — file contains item{V:2}
	got, err := f.Read(nil, format.FileOptions{})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.V != 2 {
		t.Errorf("want V=2 (last write), got V=%d", got.V)
	}
}

func TestTapWriteFile_ErrorCallsOnError(t *testing.T) {
	ctx := context.Background()

	type item struct{ V int }
	c := codex.Struct[item](
		codex.RequiredField("v", codex.Int().Refine(validate.MinInt(1)),
			func(x item) int { return x.V }, func(x *item, v int) { x.V = v }),
	)
	// Use a path that doesn't exist and can't be created:
	f := format.NewFile("/nonexistent/dir/result.json", format.JSON(c))

	ch := make(chan item, 1)
	ch <- item{V: 1} // positive, valid
	close(ch)

	var gotErr error
	out := fileadapter.TapWriteFile(ctx, f, gstream.From(ctx, ch), nil,
		fileadapter.TapWriteFileOptions{OnError: func(e error) { gotErr = e }})
	gstream.Collect(ctx, out)

	if gotErr == nil {
		t.Error("want write error for nonexistent dir, got nil")
	}
}

func TestDrainWriteFile_ConsumesStream(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "result.json")

	type item struct{ V int }
	c := codex.Struct[item](
		codex.RequiredField("v", codex.Int(), func(x item) int { return x.V }, func(x *item, v int) { x.V = v }),
	)
	f := format.NewFile(path, format.JSON(c))

	ch := make(chan item, 3)
	ch <- item{V: 10}
	ch <- item{V: 20}
	ch <- item{V: 30}
	close(ch)

	fileadapter.DrainWriteFile(ctx, f, gstream.From(ctx, ch), nil,
		fileadapter.DrainWriteFileOptions{})

	// Last write wins — file contains item{V:30}
	got, err := f.Read(nil, format.FileOptions{})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.V != 30 {
		t.Errorf("want V=30 (last write), got V=%d", got.V)
	}
}

// ── ReadEachStream ─────────────────────────────────────────────────────────────

func TestReadEachStream_HappyPath(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	type config struct{ Factor float64 }
	c := codex.Struct[config](
		codex.RequiredField("factor", codex.Float64(), func(x config) float64 { return x.Factor }, func(x *config, v float64) { x.Factor = v }),
	)

	// Write two config files.
	for id, factor := range map[string]float64{"a": 2.0, "b": 3.0} {
		f := format.NewFile(filepath.Join(dir, id+".json"), format.JSON(c))
		if err := f.Write(nil, config{Factor: factor}, format.FileOptions{}); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	type reading struct{ ID string }
	configFile := format.NewFile(filepath.Join(dir, "{id}.json"), format.JSON(c))

	src := gstream.From(ctx, func() <-chan reading {
		ch := make(chan reading, 2)
		ch <- reading{ID: "a"}
		ch <- reading{ID: "b"}
		close(ch)
		return ch
	}())

	out := fileadapter.ReadEachStream(ctx, configFile, src,
		func(r reading) map[string]string { return map[string]string{"id": r.ID} },
		func(r reading, cfg config) float64 { return cfg.Factor },
		fileadapter.ReadEachStreamOptions{})

	vals, errs := gstream.Collect(context.Background(), out)
	if len(errs) != 0 {
		t.Fatalf("want 0 errors, got %v", errs)
	}
	if len(vals) != 2 {
		t.Errorf("want 2 values, got %d", len(vals))
	}
}

func TestReadEachStream_ReadErrorGoesToStreamErrors(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	type config struct{ Factor float64 }
	c := codex.Struct[config](
		codex.RequiredField("factor", codex.Float64(), func(x config) float64 { return x.Factor }, func(x *config, v float64) { x.Factor = v }),
	)
	configFile := format.NewFile(filepath.Join(dir, "{id}.json"), format.JSON(c))

	type reading struct{ ID string }
	src := gstream.From(ctx, func() <-chan reading {
		ch := make(chan reading, 1)
		ch <- reading{ID: "missing"}
		close(ch)
		return ch
	}())

	var gotErr error
	out := fileadapter.ReadEachStream(ctx, configFile, src,
		func(r reading) map[string]string { return map[string]string{"id": r.ID} },
		func(r reading, cfg config) float64 { return cfg.Factor },
		fileadapter.ReadEachStreamOptions{OnError: func(e error) { gotErr = e }})

	_, errs := gstream.Collect(context.Background(), out)
	if len(errs) == 0 {
		t.Fatal("want error in Stream.Errors, got none")
	}
	var re fileadapter.ReadError
	if !errors.As(errs[0], &re) {
		t.Errorf("want ReadError, got %T: %v", errs[0], errs[0])
	}
	if gotErr == nil {
		t.Error("want OnError called, got nil")
	}
}

func TestReadEachStream_UpstreamErrorForwarded(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	type config struct{ V int }
	c := codex.Struct[config](
		codex.RequiredField("v", codex.Int(), func(x config) int { return x.V }, func(x *config, v int) { x.V = v }),
	)
	configFile := format.NewFile(filepath.Join(dir, "{id}.json"), format.JSON(c))

	type reading struct{ ID string }

	valCh := make(chan reading)
	errCh := make(chan error, 1)
	errCh <- fmt.Errorf("upstream failure")
	close(errCh)
	close(valCh)

	src := gstream.Stream[reading]{Values: valCh, Errors: errCh}

	out := fileadapter.ReadEachStream(ctx, configFile, src,
		func(r reading) map[string]string { return nil },
		func(r reading, cfg config) int { return cfg.V },
		fileadapter.ReadEachStreamOptions{})

	_, errs := gstream.Collect(context.Background(), out)
	if len(errs) == 0 {
		t.Fatal("want upstream error forwarded to Stream.Errors")
	}
}
