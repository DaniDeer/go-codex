package file_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	fileadapter "github.com/DaniDeer/go-codex/adapters/file"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/ports"
	gstream "github.com/DaniDeer/go-codex/stream"
)

// ── shared helpers ─────────────────────────────────────────────────────────────

type item struct{ V int }

var itemCodec = codex.Struct[item](
	codex.RequiredField("v", codex.Int(), func(x item) int { return x.V }, func(x *item, v int) { x.V = v }),
)

// ── ScanAdapter ───────────────────────────────────────────────────────────────

func TestScanAdapter_DecodesLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.ndjson")
	if err := os.WriteFile(path, []byte(`{"v":1}`+"\n"+`{"v":2}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	p, err := ports.NewSourcePort[item]("scan", itemCodec, ports.PortOptions{Buffer: 4})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	p.Bind(ctx, fileadapter.ScanAdapter(path, format.JSON(itemCodec), fileadapter.ScanAdapterOptions{}))
	vals, errs := gstream.Collect(ctx, p.Stream(ctx))
	if len(errs) != 0 {
		t.Fatalf("want 0 errors, got %d: %v", len(errs), errs)
	}
	if len(vals) != 2 {
		t.Errorf("want 2 values, got %d", len(vals))
	}
}

func TestScanAdapter_InvalidPathError(t *testing.T) {
	ctx := context.Background()
	p, err := ports.NewSourcePort[item]("scan", itemCodec, ports.PortOptions{Buffer: 4})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	p.Bind(ctx, fileadapter.ScanAdapter("/nonexistent/path.ndjson", format.JSON(itemCodec), fileadapter.ScanAdapterOptions{}))
	_, errs := gstream.Collect(ctx, p.Stream(ctx))
	if len(errs) == 0 {
		t.Fatal("want error for missing file, got none")
	}
	var se fileadapter.ScanError
	if !errors.As(errs[0], &se) {
		t.Errorf("want ScanError, got %T", errs[0])
	}
}

// ── WatchAdapter ──────────────────────────────────────────────────────────────

func TestWatchAdapter_EmitsNewFiles(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p, err := ports.NewSourcePort[string]("watch", codex.String(), ports.PortOptions{Buffer: 4})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	p.Bind(ctx, fileadapter.WatchAdapter(dir, 20*time.Millisecond, fileadapter.WatchAdapterOptions{}))
	s := p.Stream(ctx)

	// Create a file after watch starts.
	time.Sleep(30 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hi"), 0600); err != nil {
		t.Fatal(err)
	}

	var got string
	select {
	case v := <-s.Values:
		got = v
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timeout waiting for file event")
	}
	cancel()
	if filepath.Base(got) != "a.txt" {
		t.Errorf("want a.txt, got %q", got)
	}
}

// ── DrainWriteAdapter ─────────────────────────────────────────────────────────

func TestDrainWriteAdapter_EncodesAndWrites(t *testing.T) {
	ctx := context.Background()
	var buf bytes.Buffer

	ch := make(chan item, 2)
	ch <- item{V: 1}
	ch <- item{V: 2}
	close(ch)

	p, err := ports.NewSinkPort[item]("write", itemCodec, ports.PortOptions{Buffer: 4})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	p.Bind(ctx, fileadapter.DrainWriteAdapter(&buf, format.JSON(itemCodec), fileadapter.DrainWriteAdapterOptions{}))
	p.Feed(ctx, gstream.From(ctx, ch))

	out := buf.String()
	if out == "" {
		t.Error("want non-empty output, got empty")
	}
}

// ── ReadEachAdapter ───────────────────────────────────────────────────────────

func TestReadEachAdapter_HappyPath(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	type config struct{ Factor float64 }
	c := codex.Struct[config](
		codex.RequiredField("factor", codex.Float64(), func(x config) float64 { return x.Factor }, func(x *config, v float64) { x.Factor = v }),
	)
	for id, factor := range map[string]float64{"a": 2.0, "b": 3.0} {
		f := ports.NewFile(filepath.Join(dir, id+".json"), format.JSON(c))
		if err := f.Write(nil, config{Factor: factor}, ports.FileOptions{}); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	type reading struct{ ID string }
	configFile := ports.NewFile(filepath.Join(dir, "{id}.json"), format.JSON(c))

	inCh := make(chan reading, 2)
	inCh <- reading{ID: "a"}
	inCh <- reading{ID: "b"}
	close(inCh)

	adapter := fileadapter.ReadEachAdapter(configFile,
		func(r reading) map[string]string { return map[string]string{"id": r.ID} },
		func(r reading, cfg config) float64 { return cfg.Factor },
		fileadapter.ReadEachAdapterOptions{})

	src := gstream.From(ctx, inCh)
	out := adapter.Transform(ctx, src)
	vals, errs := gstream.Collect(ctx, out)
	if len(errs) != 0 {
		t.Fatalf("want 0 errors, got %v", errs)
	}
	if len(vals) != 2 {
		t.Errorf("want 2 values, got %d", len(vals))
	}
}

func TestReadEachAdapter_ReadErrorGoesToStreamErrors(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	type config struct{ Factor float64 }
	c := codex.Struct[config](
		codex.RequiredField("factor", codex.Float64(), func(x config) float64 { return x.Factor }, func(x *config, v float64) { x.Factor = v }),
	)
	configFile := ports.NewFile(filepath.Join(dir, "{id}.json"), format.JSON(c))

	type reading struct{ ID string }
	inCh := make(chan reading, 1)
	inCh <- reading{ID: "missing"}
	close(inCh)

	adapter := fileadapter.ReadEachAdapter(configFile,
		func(r reading) map[string]string { return map[string]string{"id": r.ID} },
		func(r reading, cfg config) float64 { return cfg.Factor },
		fileadapter.ReadEachAdapterOptions{})

	out := adapter.Transform(ctx, gstream.From(ctx, inCh))
	_, errs := gstream.Collect(ctx, out)
	if len(errs) == 0 {
		t.Fatal("want error in Stream.Errors, got none")
	}
	var re fileadapter.ReadError
	if !errors.As(errs[0], &re) {
		t.Errorf("want ReadError, got %T: %v", errs[0], errs[0])
	}
}

func TestReadEachAdapter_ParamValidationError(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	type config struct{ Factor float64 }
	c := codex.Struct[config](
		codex.RequiredField("factor", codex.Float64(), func(x config) float64 { return x.Factor }, func(x *config, v float64) { x.Factor = v }),
	)
	configFile := ports.NewFile(filepath.Join(dir, "{id}.json"), format.JSON(c))

	type reading struct{ ID string }
	inCh := make(chan reading, 1)
	inCh <- reading{ID: ""} // missing required "id" value
	close(inCh)

	adapter := fileadapter.ReadEachAdapter(configFile,
		func(r reading) map[string]string { return map[string]string{"id": r.ID} },
		func(r reading, cfg config) float64 { return cfg.Factor },
		fileadapter.ReadEachAdapterOptions{})

	params := []ports.IOParam{{Name: "id", Required: true}}
	ctx = ports.WithParams(ctx, params)

	out := adapter.Transform(ctx, gstream.From(ctx, inCh))
	_, errs := gstream.Collect(ctx, out)
	if len(errs) == 0 {
		t.Fatal("want param validation error in Stream.Errors, got none")
	}
	var re fileadapter.ReadError
	if !errors.As(errs[0], &re) {
		t.Fatalf("want ReadError, got %T: %v", errs[0], errs[0])
	}
	var ve codex.ValidationErrors
	if !errors.As(re, &ve) {
		t.Errorf("want ReadError to wrap codex.ValidationErrors, got %v", re.Err)
	}
}

func TestDrainWriteFileAdapter_ParamValidationError(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	resultFile := ports.NewFile(filepath.Join(dir, "{machineID}.json"), format.JSON(itemCodec))

	ch := make(chan item, 1)
	ch <- item{V: 1}
	close(ch)

	var caught error
	adapter := fileadapter.DrainWriteFileAdapter(resultFile,
		func(item) map[string]string { return map[string]string{} }, // missing "machineID"
		fileadapter.DrainWriteFileAdapterOptions{OnError: func(e error) { caught = e }})

	params := []ports.IOParam{{Name: "machineID", Required: true}}
	p, err := ports.NewSinkPort[item]("write-file", itemCodec, ports.PortOptions{Buffer: 4, Params: params})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	p.Bind(ctx, adapter)
	p.Feed(ctx, gstream.From(ctx, ch))

	if caught == nil {
		t.Fatal("want OnError to be called with a param validation error")
	}
	var we fileadapter.WriteError
	if !errors.As(caught, &we) {
		t.Fatalf("want WriteError, got %T: %v", caught, caught)
	}
	var ve codex.ValidationErrors
	if !errors.As(we, &ve) {
		t.Errorf("want WriteError to wrap codex.ValidationErrors, got %v", we.Err)
	}
	// The file must not have been written since validation failed.
	if _, err := os.Stat(filepath.Join(dir, ".json")); err == nil {
		t.Error("want no file written when param validation fails")
	}
}

// ── DrainPatchAdapter ─────────────────────────────────────────────────────────

func TestDrainPatchAdapter_AppliesPartialUpdate(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	f := ports.NewFile(path, format.JSON(itemCodec))
	if err := f.Write(nil, item{V: 1}, ports.FileOptions{}); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	ch := make(chan map[string]any, 1)
	ch <- map[string]any{"v": 2}
	close(ch)

	mapCodec := codex.Map(codex.String(), codex.Any())
	p, err := ports.NewSinkPort[map[string]any]("patch", mapCodec, ports.PortOptions{Buffer: 4})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	var caught error
	p.Bind(ctx, fileadapter.DrainPatchAdapter(f,
		func(map[string]any) map[string]string { return nil },
		fileadapter.DrainPatchAdapterOptions{OnError: func(e error) { caught = e }}))
	p.Feed(ctx, gstream.From(ctx, ch))

	if caught != nil {
		t.Fatalf("unexpected error: %v", caught)
	}
	got, err := f.Read(nil, ports.FileOptions{})
	if err != nil {
		t.Fatalf("read patched file: %v", err)
	}
	if got.V != 2 {
		t.Errorf("want V=2 after patch, got %d", got.V)
	}
}

func TestDrainPatchAdapter_NotSupportedForGob(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.gob")
	f := ports.NewFile(path, format.Gob(itemCodec))
	if err := f.Write(nil, item{V: 1}, ports.FileOptions{}); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	ch := make(chan map[string]any, 1)
	ch <- map[string]any{"v": 2}
	close(ch)

	mapCodec := codex.Map(codex.String(), codex.Any())
	p, err := ports.NewSinkPort[map[string]any]("patch-gob", mapCodec, ports.PortOptions{Buffer: 4})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	var caught error
	p.Bind(ctx, fileadapter.DrainPatchAdapter(f,
		func(map[string]any) map[string]string { return nil },
		fileadapter.DrainPatchAdapterOptions{OnError: func(e error) { caught = e }}))
	p.Feed(ctx, gstream.From(ctx, ch))

	var pe ports.FilePatchNotSupportedError
	if !errors.As(caught, &pe) {
		t.Fatalf("want FilePatchNotSupportedError passed through unwrapped, got %T: %v", caught, caught)
	}
}

func TestDrainPatchAdapter_ParamValidationError(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	f := ports.NewFile(filepath.Join(dir, "{machineID}.json"), format.JSON(itemCodec))

	ch := make(chan map[string]any, 1)
	ch <- map[string]any{"v": 2}
	close(ch)

	var caught error
	adapter := fileadapter.DrainPatchAdapter(f,
		func(map[string]any) map[string]string { return map[string]string{} }, // missing "machineID"
		fileadapter.DrainPatchAdapterOptions{OnError: func(e error) { caught = e }})

	mapCodec := codex.Map(codex.String(), codex.Any())
	params := []ports.IOParam{{Name: "machineID", Required: true}}
	p, err := ports.NewSinkPort[map[string]any]("patch-validate", mapCodec, ports.PortOptions{Buffer: 4, Params: params})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	p.Bind(ctx, adapter)
	p.Feed(ctx, gstream.From(ctx, ch))

	if caught == nil {
		t.Fatal("want OnError to be called with a param validation error")
	}
	var we fileadapter.WriteError
	if !errors.As(caught, &we) {
		t.Fatalf("want WriteError, got %T: %v", caught, caught)
	}
	var ve codex.ValidationErrors
	if !errors.As(we, &ve) {
		t.Errorf("want WriteError to wrap codex.ValidationErrors, got %v", we.Err)
	}
}

// ── DrainPatchEncodedAdapter ──────────────────────────────────────────────────

type itemPatch struct{ V int }

var itemPatchCodec = codex.Struct[itemPatch](
	codex.RequiredField("v", codex.Int(), func(x itemPatch) int { return x.V }, func(x *itemPatch, v int) { x.V = v }),
)

func TestDrainPatchEncodedAdapter_AppliesTypedPartialUpdate(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	f := ports.NewFile(path, format.JSON(itemCodec))
	if err := f.Write(nil, item{V: 1}, ports.FileOptions{}); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	ch := make(chan itemPatch, 1)
	ch <- itemPatch{V: 3}
	close(ch)

	p, err := ports.NewSinkPort[itemPatch]("patch-encoded", itemPatchCodec, ports.PortOptions{Buffer: 4})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	var caught error
	p.Bind(ctx, fileadapter.DrainPatchEncodedAdapter(f, itemPatchCodec,
		func(itemPatch) map[string]string { return nil },
		fileadapter.DrainPatchEncodedAdapterOptions{OnError: func(e error) { caught = e }}))
	p.Feed(ctx, gstream.From(ctx, ch))

	if caught != nil {
		t.Fatalf("unexpected error: %v", caught)
	}
	got, err := f.Read(nil, ports.FileOptions{})
	if err != nil {
		t.Fatalf("read patched file: %v", err)
	}
	if got.V != 3 {
		t.Errorf("want V=3 after patch, got %d", got.V)
	}
}

func TestDrainPatchEncodedAdapter_ParamValidationError(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	f := ports.NewFile(filepath.Join(dir, "{machineID}.json"), format.JSON(itemCodec))

	ch := make(chan itemPatch, 1)
	ch <- itemPatch{V: 3}
	close(ch)

	var caught error
	adapter := fileadapter.DrainPatchEncodedAdapter(f, itemPatchCodec,
		func(itemPatch) map[string]string { return map[string]string{} }, // missing "machineID"
		fileadapter.DrainPatchEncodedAdapterOptions{OnError: func(e error) { caught = e }})

	params := []ports.IOParam{{Name: "machineID", Required: true}}
	p, err := ports.NewSinkPort[itemPatch]("patch-encoded-validate", itemPatchCodec, ports.PortOptions{Buffer: 4, Params: params})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	p.Bind(ctx, adapter)
	p.Feed(ctx, gstream.From(ctx, ch))

	if caught == nil {
		t.Fatal("want OnError to be called with a param validation error")
	}
	var we fileadapter.WriteError
	if !errors.As(caught, &we) {
		t.Fatalf("want WriteError, got %T: %v", caught, caught)
	}
	var ve codex.ValidationErrors
	if !errors.As(we, &ve) {
		t.Errorf("want WriteError to wrap codex.ValidationErrors, got %v", we.Err)
	}
}

// ── Error type LogValue tests ─────────────────────────────────────────────────

func TestScanError_LogValue(t *testing.T) {
	e := fileadapter.ScanError{Path: "/some/file.csv", Err: errors.New("io error")}
	v := e.LogValue()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("want KindGroup, got %v", v.Kind())
	}
	keys := groupKeys(v)
	for _, k := range []string{"path", "err"} {
		if !keys[k] {
			t.Errorf("missing attribute %q", k)
		}
	}
	if errors.Unwrap(e) == nil {
		t.Error("Unwrap must return inner error")
	}
}

func TestWatchError_LogValue(t *testing.T) {
	e := fileadapter.WatchError{Dir: "/watched", Err: errors.New("perm denied")}
	v := e.LogValue()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("want KindGroup, got %v", v.Kind())
	}
	keys := groupKeys(v)
	for _, k := range []string{"dir", "err"} {
		if !keys[k] {
			t.Errorf("missing attribute %q", k)
		}
	}
}

func TestWriteError_LogValue(t *testing.T) {
	e := fileadapter.WriteError{Path: "/out.json", Err: errors.New("disk full")}
	v := e.LogValue()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("want KindGroup, got %v", v.Kind())
	}
	keys := groupKeys(v)
	for _, k := range []string{"path", "err"} {
		if !keys[k] {
			t.Errorf("missing attribute %q", k)
		}
	}
}

func TestReadError_LogValue(t *testing.T) {
	e := fileadapter.ReadError{Err: errors.New("file not found")}
	v := e.LogValue()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("want KindGroup, got %v", v.Kind())
	}
	if errors.Unwrap(e) == nil {
		t.Error("Unwrap must return inner error")
	}
}

func groupKeys(v slog.Value) map[string]bool {
	keys := map[string]bool{}
	for _, a := range v.Group() {
		keys[a.Key] = true
	}
	return keys
}

// ── ReadAdapter (2-type, FilePattern pairing) ─────────────────────────────────

func TestReadAdapter_HappyPath_ViaFilePattern(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	type config struct{ Factor float64 }
	c := codex.Struct[config](
		codex.RequiredField("factor", codex.Float64(), func(x config) float64 { return x.Factor }, func(x *config, v float64) { x.Factor = v }),
	)
	for id, factor := range map[string]float64{"a": 2.0, "b": 3.0} {
		f := ports.NewFile(filepath.Join(dir, id+".json"), format.JSON(c))
		if err := f.Write(nil, config{Factor: factor}, ports.FileOptions{}); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	type reading struct{ ID string }
	readingCodec := codex.Struct[reading](
		codex.RequiredField("id", codex.String(), func(x reading) string { return x.ID }, func(x *reading, v string) { x.ID = v }),
	)

	// Declare the file on the port itself — FilePattern with the port's
	// RESPONSE codec; the handle comes back out via FileHandle.
	p, err := ports.NewIOPort[reading, config]("calibration", readingCodec, c, ports.PortOptions{
		Patterns: []ports.Pattern{
			ports.FilePattern{Path: filepath.Join(dir, "{id}.json")},
		},
	})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	f, ok := ports.FileHandle[config](p)
	if !ok {
		t.Fatal("want FileHandle to be present")
	}
	if err := p.Bind(ctx, fileadapter.ReadAdapter(f,
		func(r reading) map[string]string { return map[string]string{"id": r.ID} },
		fileadapter.ReadEachAdapterOptions{})); err != nil {
		t.Fatalf("bind: %v", err)
	}

	inCh := make(chan reading, 2)
	inCh <- reading{ID: "a"}
	inCh <- reading{ID: "b"}
	close(inCh)

	vals, errs := gstream.Collect(ctx, p.Connect(ctx, gstream.From(ctx, inCh)))
	if len(errs) != 0 {
		t.Fatalf("want 0 errors, got %v", errs)
	}
	if len(vals) != 2 {
		t.Fatalf("want 2 values, got %d", len(vals))
	}
	sum := vals[0].Factor + vals[1].Factor
	if sum != 5.0 {
		t.Errorf("want file contents 2.0+3.0, got sum %v", sum)
	}
}

func TestReadAdapter_ReadErrorGoesToStreamErrors(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	type config struct{ Factor float64 }
	c := codex.Struct[config](
		codex.RequiredField("factor", codex.Float64(), func(x config) float64 { return x.Factor }, func(x *config, v float64) { x.Factor = v }),
	)
	configFile := ports.NewFile(filepath.Join(dir, "{id}.json"), format.JSON(c))

	type reading struct{ ID string }
	inCh := make(chan reading, 1)
	inCh <- reading{ID: "missing"}
	close(inCh)

	adapter := fileadapter.ReadAdapter(configFile,
		func(r reading) map[string]string { return map[string]string{"id": r.ID} },
		fileadapter.ReadEachAdapterOptions{})
	if got := adapter.AdapterName(); got != "file.ReadAdapter" {
		t.Errorf("want AdapterName file.ReadAdapter, got %q", got)
	}

	_, errs := gstream.Collect(ctx, adapter.Transform(ctx, gstream.From(ctx, inCh)))
	if len(errs) == 0 {
		t.Fatal("want error in Stream.Errors, got none")
	}
	var re fileadapter.ReadError
	if !errors.As(errs[0], &re) {
		t.Errorf("want ReadError, got %T: %v", errs[0], errs[0])
	}
}

func TestReadAdapter_ParamValidationError(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	type config struct{ Factor float64 }
	c := codex.Struct[config](
		codex.RequiredField("factor", codex.Float64(), func(x config) float64 { return x.Factor }, func(x *config, v float64) { x.Factor = v }),
	)
	configFile := ports.NewFile(filepath.Join(dir, "{id}.json"), format.JSON(c))

	type reading struct{ ID string }
	inCh := make(chan reading, 1)
	inCh <- reading{ID: ""} // missing required "id" value
	close(inCh)

	adapter := fileadapter.ReadAdapter(configFile,
		func(r reading) map[string]string { return map[string]string{"id": r.ID} },
		fileadapter.ReadEachAdapterOptions{})

	ctx = ports.WithParams(ctx, []ports.IOParam{{Name: "id", Required: true}})
	_, errs := gstream.Collect(ctx, adapter.Transform(ctx, gstream.From(ctx, inCh)))
	if len(errs) == 0 {
		t.Fatal("want param validation error in Stream.Errors, got none")
	}
	var re fileadapter.ReadError
	if !errors.As(errs[0], &re) {
		t.Fatalf("want ReadError, got %T: %v", errs[0], errs[0])
	}
	var ve codex.ValidationErrors
	if !errors.As(re, &ve) {
		t.Errorf("want ReadError to wrap codex.ValidationErrors, got %v", re.Err)
	}
}

// ── G1: file merge-field convenience (ReadMerged / WriteHandle wiring) ───────

// sensorConfig is a merge-capable type: SensorID comes from the path
// (declared via ports.NewFilePathParam), Factor comes from the JSON body.
type sensorConfig struct {
	SensorID string
	Factor   float64
}

var sensorConfigCodec = codex.Struct[sensorConfig](
	codex.RequiredField("factor", codex.Float64(),
		func(c sensorConfig) float64 { return c.Factor },
		func(c *sensorConfig, v float64) { c.Factor = v }),
)

func newSensorConfigFile(dir string) ports.File[sensorConfig] {
	return ports.NewFile(filepath.Join(dir, "{sensorID}.json"), format.JSON(sensorConfigCodec),
		ports.NewFilePathParam("sensorID", codex.String(),
			func(c sensorConfig) string { return c.SensorID },
			func(c *sensorConfig, v string) { c.SensorID = v }),
	)
}

// G1: ReadEachAdapter merges path vars into the decoded value via
// ports.File.ReadMerged — the sensorID extracted for the file lookup is
// ALSO populated onto the returned struct automatically.
func TestReadEachAdapter_MergesPathVarsIntoDecodedValue(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	configFile := newSensorConfigFile(dir)
	// Body JSON deliberately carries ONLY "factor" — SensorID must come
	// exclusively from the path-var merge.
	if err := os.WriteFile(filepath.Join(dir, "sensor-1.json"), []byte(`{"factor":2.0}`), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}

	type reading struct{ ID string }
	inCh := make(chan reading, 1)
	inCh <- reading{ID: "sensor-1"}
	close(inCh)

	adapter := fileadapter.ReadEachAdapter(configFile,
		func(r reading) map[string]string { return map[string]string{"sensorID": r.ID} },
		func(_ reading, cfg sensorConfig) sensorConfig { return cfg },
		fileadapter.ReadEachAdapterOptions{})

	vals, errs := gstream.Collect(ctx, adapter.Transform(ctx, gstream.From(ctx, inCh)))
	if len(errs) != 0 {
		t.Fatalf("want 0 errors, got %v", errs)
	}
	if len(vals) != 1 || vals[0].SensorID != "sensor-1" || vals[0].Factor != 2.0 {
		t.Errorf("want merged SensorID+Factor, got %v", vals)
	}
}

// G1: DrainWriteFileAdapter derives path vars PER-ITEM from each item's own
// merge fields when varsFor is nil — two items with different SensorIDs
// must write to two different concrete paths.
func TestDrainWriteFileAdapter_DerivesVarsPerItem_WhenVarsForNil(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	configFile := newSensorConfigFile(dir)

	ch := make(chan sensorConfig, 2)
	ch <- sensorConfig{SensorID: "sensor-a", Factor: 1.5}
	ch <- sensorConfig{SensorID: "sensor-b", Factor: 2.5}
	close(ch)

	p, err := ports.NewSinkPort[sensorConfig]("write-file", sensorConfigCodec, ports.PortOptions{Buffer: 4})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	// varsFor left nil -> per-item derivation via ports.WriteHandle.
	p.Bind(ctx, fileadapter.DrainWriteFileAdapter(configFile, nil, fileadapter.DrainWriteFileAdapterOptions{}))
	p.Feed(ctx, gstream.From(ctx, ch))

	for _, name := range []string{"sensor-a.json", "sensor-b.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("want file %q to exist, got: %v", name, err)
		}
	}
}

// Explicit (non-nil) varsFor still wins — regression guard matching
// today's behavior when set.
func TestDrainWriteFileAdapter_ExplicitVarsForStillWins(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	configFile := newSensorConfigFile(dir)

	ch := make(chan sensorConfig, 1)
	ch <- sensorConfig{SensorID: "ignored", Factor: 1.5}
	close(ch)

	p, err := ports.NewSinkPort[sensorConfig]("write-file-static", sensorConfigCodec, ports.PortOptions{Buffer: 4})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	p.Bind(ctx, fileadapter.DrainWriteFileAdapter(configFile,
		func(sensorConfig) map[string]string { return map[string]string{"sensorID": "static-id"} },
		fileadapter.DrainWriteFileAdapterOptions{}))
	p.Feed(ctx, gstream.From(ctx, ch))

	if _, err := os.Stat(filepath.Join(dir, "static-id.json")); err != nil {
		t.Errorf("want static path used, got: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "ignored.json")); err == nil {
		t.Error("want derived path NOT used when varsFor is explicit")
	}
}
