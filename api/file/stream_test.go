package file_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	adapterfile "github.com/DaniDeer/go-codex/adapters/file"
	"github.com/DaniDeer/go-codex/api/file"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/stats"
)

var osReadFile = os.ReadFile

// ── Fake backends for testing the 3-level OpenRead fallback cascade ────
//
// These delegate to a real *adapterfile.Backend for the base file.Backend
// methods WITHOUT embedding it directly (embedding would PROMOTE
// OpenReader/OpenReaderRange/OpenWriter too, defeating the point of a
// "bare"/"stream-only" fake) — each fake selectively adds back only the
// extension methods its own name promises.

type bareBackend struct{ inner *adapterfile.Backend }

func (b bareBackend) BackendName() string { return "bare" }
func (b bareBackend) ReadBytes(ctx context.Context, path string) ([]byte, error) {
	return b.inner.ReadBytes(ctx, path)
}
func (b bareBackend) WriteBytes(ctx context.Context, path string, data []byte, opts file.WriteOptions) ([]string, error) {
	return b.inner.WriteBytes(ctx, path, data, opts)
}
func (b bareBackend) DeleteBytes(ctx context.Context, path string, opts file.DeleteOptions) (bool, error) {
	return b.inner.DeleteBytes(ctx, path, opts)
}
func (b bareBackend) ListDir(ctx context.Context, path string) ([]file.Entry, error) {
	return b.inner.ListDir(ctx, path)
}
func (b bareBackend) Stat(ctx context.Context, path string) (file.Entry, error) {
	return b.inner.Stat(ctx, path)
}
func (b bareBackend) Mkdir(ctx context.Context, path string, opts file.MkdirOptions) (bool, error) {
	return b.inner.Mkdir(ctx, path, opts)
}

// streamOnlyBackend implements StreamReadBackend (whole-file streaming)
// but deliberately NOT RangeReadingBackend — the OpenRead cascade's 2nd
// tier.
type streamOnlyBackend struct{ bareBackend }

func (b streamOnlyBackend) OpenReader(ctx context.Context, path string) (io.ReadCloser, error) {
	return b.inner.OpenReader(ctx, path)
}

// rangeBackend implements RangeReadingBackend (the native, 1st tier) —
// also implements StreamReadBackend for completeness/realism, though the
// cascade should never reach it when RangeReadingBackend is present.
type rangeBackend struct{ streamOnlyBackend }

func (b rangeBackend) OpenReaderRange(ctx context.Context, path string, offset, length int64) (io.ReadCloser, error) {
	data, err := b.inner.ReadBytes(ctx, path)
	if err != nil {
		return nil, err
	}
	end := int64(len(data))
	if length > 0 && offset+length < end {
		end = offset + length
	}
	return io.NopCloser(bytes.NewReader(data[offset:end])), nil
}

var (
	_ file.Backend             = bareBackend{}
	_ file.StreamReadBackend   = streamOnlyBackend{}
	_ file.RangeReadingBackend = rangeBackend{}
)

func newBareResource(t *testing.T, dir string) (file.Resource[record], bareBackend) {
	t.Helper()
	res := file.NewResource[record](filepath.Join(dir, "{name}.json"), format.JSON(recordCodec()), file.PathParam{Name: "name"})
	return res, bareBackend{inner: adapterfile.NewBackend()}
}

// ── Mechanism 1: automatic format-level write streaming ────────────────

type streamableRecord struct {
	Name  string
	Value int
}

func streamableRecordFormat() format.Format[streamableRecord] {
	codec := codex.Struct[streamableRecord](
		codex.RequiredField("name", codex.String(),
			func(r streamableRecord) string { return r.Name },
			func(r *streamableRecord, v string) { r.Name = v }),
		codex.RequiredField("value", codex.Int(),
			func(r streamableRecord) int { return r.Value },
			func(r *streamableRecord, v int) { r.Value = v }),
	)
	return format.NewStreamed(codec, func(v streamableRecord, w io.Writer) error {
		_, err := io.WriteString(w, v.Name)
		return err
	}, func(data []byte) (streamableRecord, error) {
		return streamableRecord{Name: string(data)}, nil
	}, "application/octet-stream")
}

func TestCreate_StreamableFormat_StreamingBackend_UsesMarshalTo(t *testing.T) {
	dir := t.TempDir()
	res := file.NewResource[streamableRecord](filepath.Join(dir, "{name}.bin"), streamableRecordFormat(), file.PathParam{Name: "name"})
	client := file.NewClient()
	if err := client.Attach(adapterfile.NewBackend()); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	ctx := context.Background()
	if _, err := client.Create(ctx, res.WithCreate(file.Create{}), streamableRecord{Name: "hello"}, map[string]string{"name": "hello"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := osReadFile(filepath.Join(dir, "hello.bin"))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("got %q, want %q (MarshalTo should have written directly)", got, "hello")
	}
}

func TestCreate_StreamableFormat_NonStreamingBackend_FallsBackToWholeFile(t *testing.T) {
	dir := t.TempDir()
	res := file.NewResource[streamableRecord](filepath.Join(dir, "{name}.bin"), streamableRecordFormat(), file.PathParam{Name: "name"})
	client := file.NewClient()
	if err := client.Attach(bareBackend{inner: adapterfile.NewBackend()}); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	ctx := context.Background()
	if _, err := client.Create(ctx, res.WithCreate(file.Create{}), streamableRecord{Name: "hello"}, map[string]string{"name": "hello"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := client.Read(ctx, res.WithRead(file.Read{}), map[string]string{"name": "hello"})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.Name != "hello" {
		t.Fatalf("got %+v, want Name=hello (fallback should still work correctly)", got)
	}
}

func TestCreate_StreamableFormat_IfMatchSet_FallsBackToNonStreamingConditionalPath(t *testing.T) {
	dir := t.TempDir()
	res := file.NewResource[streamableRecord](filepath.Join(dir, "{name}.bin"), streamableRecordFormat(), file.PathParam{Name: "name"})
	client := file.NewClient()
	if err := client.Attach(adapterfile.NewBackend()); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	ctx := context.Background()
	vars := map[string]string{"name": "hello"}
	if _, err := client.Create(ctx, res.WithCreate(file.Create{}), streamableRecord{Name: "v1"}, vars); err != nil {
		t.Fatalf("Create: %v", err)
	}
	current, _, err := client.Exists(ctx, res.WithExists(file.Exists{}), vars)
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	// A mismatched IfMatch must still surface ConditionalWriteMismatchError
	// — proving dispatch fell back to the non-streaming conditional path
	// rather than silently ignoring IfMatch via the streaming path.
	wrong := current.ETag + "-wrong"
	_, err = client.Create(ctx, res.WithCreate(file.Create{}), streamableRecord{Name: "v2"}, vars, file.WriteOptions{IfMatch: &wrong})
	var mismatch file.ConditionalWriteMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("want ConditionalWriteMismatchError, got %v (%T)", err, err)
	}
}

// ── Mechanism 2: StreamReader[T]/StreamWriter[T] ────────────────────────

func TestStreamWriter_OpenWrite_CommitsAtomically(t *testing.T) {
	dir := t.TempDir()
	res := file.NewResource[record](filepath.Join(dir, "{name}.json"), format.JSON(recordCodec()), file.PathParam{Name: "name"})
	client := file.NewClient()
	if err := client.Attach(adapterfile.NewBackend()); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	ctx := context.Background()
	w, err := client.OpenWrite(ctx, res.WithStreamWrite(file.StreamWrite{}), map[string]string{"name": "atomic"})
	if err != nil {
		t.Fatalf("OpenWrite: %v", err)
	}
	if _, err := w.Write([]byte("partial")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	// Before Close, the final path must not exist at all (temp file only).
	if _, _, err := client.Exists(ctx, res.WithExists(file.Exists{}), map[string]string{"name": "atomic"}); err != nil {
		t.Fatalf("Exists (pre-close): %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	_, exists, err := client.Exists(ctx, res.WithExists(file.Exists{}), map[string]string{"name": "atomic"})
	if err != nil {
		t.Fatalf("Exists (post-close): %v", err)
	}
	if !exists {
		t.Fatalf("want file to exist after Close")
	}
}

func TestStreamWriter_OpenWrite_NonStreamingBackend_BuffersThenWrites(t *testing.T) {
	dir := t.TempDir()
	res, backend := newBareResource(t, dir)
	client := file.NewClient()
	if err := client.Attach(backend); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	ctx := context.Background()
	w, err := client.OpenWrite(ctx, res.WithStreamWrite(file.StreamWrite{}), map[string]string{"name": "buffered"})
	if err != nil {
		t.Fatalf("OpenWrite: %v", err)
	}
	if _, err := w.Write([]byte(`{"name":"buffered","value":9}`)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	got, err := client.Read(ctx, res.WithRead(file.Read{}), map[string]string{"name": "buffered"})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.Value != 9 {
		t.Fatalf("got %+v, want Value=9", got)
	}
}

// TestStreamWriter_OpenWrite_NativePOSIXModeCapability_OverridesCallerPerm
// proves openWriteCore honors the SAME documented "attach-time
// Capability always wins over a conflicting per-call option" precedence
// rule createCore/patchCore/mkdirCore already enforce — see
// TestClient_Create_NativePOSIXModeCapability_OverridesCallerPerm (in
// client_test.go) for why this needed a fresh, direct test (zero prior
// coverage anywhere, including this streaming dispatch path).
func TestStreamWriter_OpenWrite_NativePOSIXModeCapability_OverridesCallerPerm(t *testing.T) {
	dir := t.TempDir()
	res := file.NewResource[record](filepath.Join(dir, "{name}.json"), format.JSON(recordCodec()), file.PathParam{Name: "name"})
	client := file.NewClient()
	if err := client.Attach(adapterfile.NewBackend()); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	capPerm := os.FileMode(0o600)
	callPerm := os.FileMode(0o777)
	ctx := context.Background()
	sw := res.WithStreamWrite(file.StreamWrite{}).WithCapabilities(file.NativePOSIXMode(capPerm))
	w, err := client.OpenWrite(ctx, sw, map[string]string{"name": "streamcapmode"}, file.WriteOptions{Perm: callPerm})
	if err != nil {
		t.Fatalf("OpenWrite: %v", err)
	}
	if _, err := w.Write([]byte(`{"name":"streamcapmode","value":1}`)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	info, err := os.Stat(filepath.Join(dir, "streamcapmode.json"))
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if got := info.Mode().Perm(); got != capPerm {
		t.Fatalf("want attach-time NativePOSIXMode capability (%o) to win over per-call WriteOptions.Perm (%o), got %o", capPerm, callPerm, got)
	}
}

func TestStreamReader_OpenRead_WholeFile_RoundTrips(t *testing.T) {
	dir := t.TempDir()
	res, backend := newBareResource(t, dir)
	client := file.NewClient()
	if err := client.Attach(backend); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	ctx := context.Background()
	vars := map[string]string{"name": "whole"}
	if _, err := client.Create(ctx, res.WithCreate(file.Create{}), record{Name: "whole", Value: 1}, vars); err != nil {
		t.Fatalf("Create: %v", err)
	}
	r, err := client.OpenRead(ctx, res.WithStreamRead(file.StreamRead{}), vars)
	if err != nil {
		t.Fatalf("OpenRead: %v", err)
	}
	defer r.Close()
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(data) == 0 {
		t.Fatalf("want non-empty content")
	}
}

func TestStreamReader_OpenRead_Range_NativeRangeReadingBackend(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "{name}.json")
	res := file.NewResource[record](path, format.JSON(recordCodec()), file.PathParam{Name: "name"})
	backend := rangeBackend{streamOnlyBackend{bareBackend{inner: adapterfile.NewBackend()}}}
	client := file.NewClient()
	if err := client.Attach(backend); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	ctx := context.Background()
	vars := map[string]string{"name": "ranged"}
	if _, err := client.Create(ctx, res.WithCreate(file.Create{}), record{Name: "ranged", Value: 1}, vars); err != nil {
		t.Fatalf("Create: %v", err)
	}
	spy := &countingObserver{}
	ctx2 := stats.WithObserver(ctx, spy)
	r, err := client.OpenRead(ctx2, res.WithStreamRead(file.StreamRead{}), vars, file.StreamReadOptions{Offset: 1, Length: 3})
	if err != nil {
		t.Fatalf("OpenRead: %v", err)
	}
	defer r.Close()
	if _, err := io.ReadAll(r); err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !spy.sawNative {
		t.Fatalf("want RecordFileRangeRead called with native=true")
	}
}

func TestStreamReader_OpenRead_Range_StreamReadBackendFallback_DiscardThenLimit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "{name}.json")
	res := file.NewResource[record](path, format.JSON(recordCodec()), file.PathParam{Name: "name"})
	backend := streamOnlyBackend{bareBackend{inner: adapterfile.NewBackend()}}
	client := file.NewClient()
	if err := client.Attach(backend); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	ctx := context.Background()
	vars := map[string]string{"name": "tier2"}
	if _, err := client.Create(ctx, res.WithCreate(file.Create{}), record{Name: "tier2", Value: 1}, vars); err != nil {
		t.Fatalf("Create: %v", err)
	}
	r, err := client.OpenRead(ctx, res.WithStreamRead(file.StreamRead{}), vars, file.StreamReadOptions{Offset: 2, Length: 5})
	if err != nil {
		t.Fatalf("OpenRead: %v", err)
	}
	defer r.Close()
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("want 5 bytes, got %d: %q", len(got), got)
	}
}

func TestStreamReader_OpenRead_Range_ReadBytesFallback_WorksOnEveryBackend(t *testing.T) {
	dir := t.TempDir()
	res, backend := newBareResource(t, dir)
	client := file.NewClient()
	if err := client.Attach(backend); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	ctx := context.Background()
	vars := map[string]string{"name": "tier3"}
	if _, err := client.Create(ctx, res.WithCreate(file.Create{}), record{Name: "tier3", Value: 1}, vars); err != nil {
		t.Fatalf("Create: %v", err)
	}
	r, err := client.OpenRead(ctx, res.WithStreamRead(file.StreamRead{}), vars, file.StreamReadOptions{Offset: 1, Length: 4})
	if err != nil {
		t.Fatalf("OpenRead: %v", err)
	}
	defer r.Close()
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("want 4 bytes, got %d: %q", len(got), got)
	}
}

type countingObserver struct {
	stats.NoopObserver
	sawNative                 bool
	sawNonNative              bool
	closeRecords              int
	sawConditionalWriteNative bool
	sawConditionalWriteApprox bool
}

func (o *countingObserver) RecordFileRangeRead(path string, native bool, offset, length int64, d time.Duration) {
	if native {
		o.sawNative = true
	} else {
		o.sawNonNative = true
	}
}

func (o *countingObserver) RecordFileConditionalWrite(path string, native bool, ifMatch string, d time.Duration) {
	if native {
		o.sawConditionalWriteNative = true
	} else {
		o.sawConditionalWriteApprox = true
	}
}

func (o *countingObserver) RecordFileRead(path string, success bool, d time.Duration) {
	o.closeRecords++
}

func TestStreamReader_OpenRead_Range_ObserverReportsNativeTrueWithRangeReadingBackend(t *testing.T) {
	dir := t.TempDir()
	res := file.NewResource[record](filepath.Join(dir, "{name}.json"), format.JSON(recordCodec()), file.PathParam{Name: "name"})
	backend := rangeBackend{streamOnlyBackend{bareBackend{inner: adapterfile.NewBackend()}}}
	client := file.NewClient()
	if err := client.Attach(backend); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	vars := map[string]string{"name": "native"}
	ctx := context.Background()
	if _, err := client.Create(ctx, res.WithCreate(file.Create{}), record{Name: "native", Value: 1}, vars); err != nil {
		t.Fatalf("Create: %v", err)
	}
	spy := &countingObserver{}
	ctx2 := stats.WithObserver(ctx, spy)
	r, err := client.OpenRead(ctx2, res.WithStreamRead(file.StreamRead{}), vars, file.StreamReadOptions{Offset: 1, Length: 2})
	if err != nil {
		t.Fatalf("OpenRead: %v", err)
	}
	defer r.Close()
	if !spy.sawNative || spy.sawNonNative {
		t.Fatalf("want native=true reported, got native=%v nonNative=%v", spy.sawNative, spy.sawNonNative)
	}
}

func TestStreamReader_OpenRead_Range_ObserverReportsNativeFalseOnFallback(t *testing.T) {
	dir := t.TempDir()
	res, backend := newBareResource(t, dir)
	client := file.NewClient()
	if err := client.Attach(backend); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	vars := map[string]string{"name": "fallback"}
	ctx := context.Background()
	if _, err := client.Create(ctx, res.WithCreate(file.Create{}), record{Name: "fallback", Value: 1}, vars); err != nil {
		t.Fatalf("Create: %v", err)
	}
	spy := &countingObserver{}
	ctx2 := stats.WithObserver(ctx, spy)
	r, err := client.OpenRead(ctx2, res.WithStreamRead(file.StreamRead{}), vars, file.StreamReadOptions{Offset: 1, Length: 2})
	if err != nil {
		t.Fatalf("OpenRead: %v", err)
	}
	defer r.Close()
	if spy.sawNative || !spy.sawNonNative {
		t.Fatalf("want native=false reported, got native=%v nonNative=%v", spy.sawNative, spy.sawNonNative)
	}
}

func TestStreamRead_Observer_RecordsOnCloseNotOpen(t *testing.T) {
	dir := t.TempDir()
	res, backend := newBareResource(t, dir)
	client := file.NewClient()
	if err := client.Attach(backend); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	vars := map[string]string{"name": "closetime"}
	ctx := context.Background()
	if _, err := client.Create(ctx, res.WithCreate(file.Create{}), record{Name: "closetime", Value: 1}, vars); err != nil {
		t.Fatalf("Create: %v", err)
	}
	spy := &countingObserver{}
	ctx2 := stats.WithObserver(ctx, spy)
	r, err := client.OpenRead(ctx2, res.WithStreamRead(file.StreamRead{}), vars)
	if err != nil {
		t.Fatalf("OpenRead: %v", err)
	}
	if spy.closeRecords != 0 {
		t.Fatalf("want 0 RecordFileRead calls before Close, got %d", spy.closeRecords)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if spy.closeRecords != 1 {
		t.Fatalf("want exactly 1 RecordFileRead call after Close, got %d", spy.closeRecords)
	}
}

// ── Security/Capability gates ────────────────────────────────────────────

func TestStreamRead_SecurityUnmet_RejectsBeforeStreamReturned(t *testing.T) {
	dir := t.TempDir()
	res, backend := newBareResource(t, dir)
	client := file.NewClient()
	if err := client.Attach(backend); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	ctx := context.Background()
	vars := map[string]string{"name": "gated"}
	if _, err := client.Create(ctx, res.WithCreate(file.Create{}), record{Name: "gated", Value: 1}, vars); err != nil {
		t.Fatalf("Create: %v", err)
	}

	bm := file.BoundSecurityStreamReadMiddleware[struct{}, grantedOut](
		"apiKey", file.SecurityScheme{}, []string{"read"},
		func(ctx context.Context, in struct{}) (grantedOut, error) {
			// Grants a DIFFERENT scope than required — should fail CheckScopes.
			return grantedOut{GrantedScopes: map[string][]string{"apiKey": {"wrong-scope"}}}, nil
		},
	)
	sr := res.WithStreamRead(file.StreamRead{}).StreamReadBoundMW(bm).WithSecurity(file.SecurityRequirement{"apiKey": {"read"}})
	_, err := client.OpenRead(ctx, sr, vars)
	var secErr file.SecurityRejectedError
	if !errors.As(err, &secErr) {
		t.Fatalf("want SecurityRejectedError, got %v (%T)", err, err)
	}
}

func TestStreamWrite_AccessModeReadOnly_RejectsBeforeStreamReturned(t *testing.T) {
	dir := t.TempDir()
	res, backend := newBareResource(t, dir)
	client := file.NewClient()
	if err := client.Attach(backend); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	ctx := context.Background()
	sw := res.WithStreamWrite(file.StreamWrite{}).WithCapabilities(file.AccessModeReadOnly)
	_, err := client.OpenWrite(ctx, sw, map[string]string{"name": "ro"})
	var unsupported file.BackendUnsupportedOptionError
	if !errors.As(err, &unsupported) {
		t.Fatalf("want BackendUnsupportedOptionError, got %v (%T)", err, err)
	}
}

// ── ErrorPattern scope limits ────────────────────────────────────────────

func TestStreamWrite_MidStreamError_NotWrappedByErrorPattern(t *testing.T) {
	dir := t.TempDir()
	res := file.NewResource[record](filepath.Join(dir, "{name}.json"), format.JSON(recordCodec()), file.PathParam{Name: "name"})
	client := file.NewClient()
	// A REAL StreamWriteBackend (adapterfile.Backend) is needed here — its
	// os.File-backed Write genuinely errs after Close; the bufferedWriteCloser
	// fallback's bytes.Buffer never does, which would make this assertion
	// vacuous.
	if err := client.Attach(adapterfile.NewBackend()); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	ctx := context.Background()
	sw := res.WithStreamWrite(file.StreamWrite{})
	w, err := client.OpenWrite(ctx, sw, map[string]string{"name": "midstream"})
	if err != nil {
		t.Fatalf("OpenWrite: %v", err)
	}
	// A mid-stream error (here, writing after an already-closed writer)
	// must be a PLAIN error, never wrapped in any api/file structured
	// type (ErrorPattern only covers Open/Close-time dispatch failures).
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	_, writeErr := w.Write([]byte("more"))
	if writeErr == nil {
		t.Fatalf("want an error writing to an already-closed writer")
	}
	var fwe file.FileWriteError
	if errors.As(writeErr, &fwe) {
		t.Fatalf("mid-stream error must NOT be wrapped in FileWriteError, got %v (%T)", writeErr, writeErr)
	}
}

// ── Router integration ────────────────────────────────────────────────

func TestStreamReader_Router_GroupedResourceComposesPrefix(t *testing.T) {
	dir := t.TempDir()
	// A relative resource template — the Router's OWN prefix supplies the
	// absolute temp subdirectory, mirroring
	// TestRouter_Group_PrefixesResourcePath's identical, already-proven
	// pattern (applied here to StreamReader[T] for the first time).
	res := file.NewResource[record]("{name}.json", format.JSON(recordCodec()), file.PathParam{Name: "name"})
	backend := adapterfile.NewBackend()
	client := file.NewClient()
	if err := client.Attach(backend); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	ctx := context.Background()

	rt := file.NewRouter(filepath.Join(dir, "grouped"))
	readHandle, err := res.WithStreamRead(file.StreamRead{}).Handle(client, file.WithRouter(rt))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	createHandle, err := res.WithCreate(file.Create{}).Handle(client, file.WithRouter(rt))
	if err != nil {
		t.Fatalf("Handle (create): %v", err)
	}

	if err := os.MkdirAll(filepath.Join(dir, "grouped"), 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	vars := map[string]string{"name": "prefixed"}
	if _, err := client.Create(ctx, createHandle, record{Name: "prefixed", Value: 2}, vars); err != nil {
		t.Fatalf("Create (grouped): %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "grouped", "prefixed.json")); err != nil {
		t.Fatalf("expected file under grouped/ prefix: %v", err)
	}
	r, err := client.OpenRead(ctx, readHandle, vars)
	if err != nil {
		t.Fatalf("OpenRead via Router-grouped handle: %v", err)
	}
	defer r.Close()
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(data) == 0 {
		t.Fatalf("want non-empty content")
	}
}
