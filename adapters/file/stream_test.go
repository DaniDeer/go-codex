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
