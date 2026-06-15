package format_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/stats"
	"github.com/DaniDeer/go-codex/validate"
)

// ── test helpers ──────────────────────────────────────────────────────────────

type fileItem struct {
	Name  string `json:"name"`
	Value int    `json:"value"`
}

var fileItemCodec = codex.Struct[fileItem](
	codex.RequiredField("name",
		codex.String().Refine(validate.NonEmptyString),
		func(v fileItem) string { return v.Name },
		func(v *fileItem, s string) { v.Name = s },
	),
	codex.RequiredField("value",
		codex.Int().Refine(validate.RangeInt(0, 1000)),
		func(v fileItem) int { return v.Value },
		func(v *fileItem, i int) { v.Value = i },
	),
)

var staticFile = format.NewFile("item.json", format.JSON(fileItemCodec))

var templateFile = format.NewFile(
	"data/{category}/{id}.json",
	format.JSON(fileItemCodec),
	format.FilePathParam{Name: "category", Description: "Item category"}.
		WithCodec(codex.String().Refine(validate.NonEmptyString)),
	format.FilePathParam{Name: "id", Description: "Item ID (numeric)"},
)

// fileObserverSpy captures FileObserver callbacks for assertions.
type fileObserverSpy struct {
	stats.NoopObserver

	reads  []fileObsCall
	writes []fileObsCall
}

type fileObsCall struct {
	path    string
	success bool
	dur     time.Duration
}

func (o *fileObserverSpy) RecordFileRead(path string, success bool, d time.Duration) {
	o.reads = append(o.reads, fileObsCall{path, success, d})
}

func (o *fileObserverSpy) RecordFileWrite(path string, success bool, d time.Duration) {
	o.writes = append(o.writes, fileObsCall{path, success, d})
}

var _ stats.FileObserver = (*fileObserverSpy)(nil)

// ── FilePathParam.WithCodec ───────────────────────────────────────────────────

func TestFilePathParam_WithCodec_SetsCodec(t *testing.T) {
	c := codex.String().Refine(validate.NonEmptyString)
	p := format.FilePathParam{Name: "sensor"}.WithCodec(c)
	if p.Codec == nil {
		t.Fatal("expected Codec to be set, got nil")
	}
	if p.Name != "sensor" {
		t.Errorf("Name changed: got %q", p.Name)
	}
}

// ── BuildPath ─────────────────────────────────────────────────────────────────

func TestBuildPath_StaticPath_NilVars(t *testing.T) {
	path, err := staticFile.BuildPath(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path != "item.json" {
		t.Errorf("want item.json, got %q", path)
	}
}

func TestBuildPath_TemplateVars_NoCodec(t *testing.T) {
	f := format.NewFile(
		"data/{category}/{id}.json",
		format.JSON(fileItemCodec),
		format.FilePathParam{Name: "category"},
		format.FilePathParam{Name: "id"},
	)
	path, err := f.BuildPath(map[string]string{"category": "sensors", "id": "42"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path != "data/sensors/42.json" {
		t.Errorf("want data/sensors/42.json, got %q", path)
	}
}

func TestBuildPath_TemplateVars_CodecPasses(t *testing.T) {
	path, err := templateFile.BuildPath(map[string]string{"category": "sensors", "id": "42"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path != "data/sensors/42.json" {
		t.Errorf("want data/sensors/42.json, got %q", path)
	}
}

func TestBuildPath_MissingVar_ReturnsMissingError(t *testing.T) {
	_, err := templateFile.BuildPath(map[string]string{"category": "sensors"}) // "id" missing
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var missing format.MissingFilePathVarError
	if !errors.As(err, &missing) {
		t.Fatalf("expected MissingFilePathVarError, got %T: %v", err, err)
	}
	if missing.Name != "id" {
		t.Errorf("expected missing param name 'id', got %q", missing.Name)
	}
}

func TestBuildPath_InvalidVar_ReturnsFilePathParamError(t *testing.T) {
	// "category" codec rejects empty string
	_, err := templateFile.BuildPath(map[string]string{"category": "", "id": "42"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var paramErr format.FilePathParamError
	if !errors.As(err, &paramErr) {
		t.Fatalf("expected FilePathParamError, got %T: %v", err, err)
	}
	if paramErr.Name != "category" {
		t.Errorf("expected param name 'category', got %q", paramErr.Name)
	}
	if paramErr.Value != "" {
		t.Errorf("expected Value=%q, got %q", "", paramErr.Value)
	}
	if paramErr.Unwrap() == nil {
		t.Error("Unwrap() must return non-nil inner error")
	}
}

func TestBuildPath_FilePathParamError_ErrorString(t *testing.T) {
	e := format.FilePathParamError{Name: "date", Value: "bad", Err: errors.New("invalid")}
	s := e.Error()
	if !strings.Contains(s, "date") || !strings.Contains(s, "bad") {
		t.Errorf("Error() missing param name or value: %q", s)
	}
}

func TestBuildPath_MissingFilePathVarError_ErrorString(t *testing.T) {
	e := format.MissingFilePathVarError{Name: "date"}
	s := e.Error()
	if !strings.Contains(s, "date") {
		t.Errorf("Error() missing param name: %q", s)
	}
}

// ── ValidatePathVars ──────────────────────────────────────────────────────────

func TestValidatePathVars_AllValid_ReturnsNil(t *testing.T) {
	err := templateFile.ValidatePathVars(map[string]string{"category": "sensors", "id": "42"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidatePathVars_MissingVar_ReturnsMissingError(t *testing.T) {
	// "category" has a codec registered; omitting it triggers MissingFilePathVarError.
	// "id" has no codec so it is never checked by ValidatePathVars.
	err := templateFile.ValidatePathVars(map[string]string{"id": "42"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var missing format.MissingFilePathVarError
	if !errors.As(err, &missing) {
		t.Fatalf("expected MissingFilePathVarError, got %T", err)
	}
}

func TestValidatePathVars_InvalidVar_ReturnsParamError(t *testing.T) {
	err := templateFile.ValidatePathVars(map[string]string{"category": "", "id": "42"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var paramErr format.FilePathParamError
	if !errors.As(err, &paramErr) {
		t.Fatalf("expected FilePathParamError, got %T", err)
	}
}

func TestValidatePathVars_NoCodecs_AlwaysNil(t *testing.T) {
	f := format.NewFile("data/{a}/{b}.json", format.JSON(fileItemCodec),
		format.FilePathParam{Name: "a"},
		format.FilePathParam{Name: "b"},
	)
	// Even missing vars are fine — ValidatePathVars only validates codecs
	err := f.ValidatePathVars(map[string]string{})
	if err != nil {
		t.Fatalf("expected nil for no-codec params, got: %v", err)
	}
}

// ── Read ──────────────────────────────────────────────────────────────────────

func TestRead_HappyPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "item.json")
	_ = os.WriteFile(path, []byte(`{"name":"widget","value":42}`), 0644)

	f := format.NewFile(path, format.JSON(fileItemCodec))
	item, err := f.Read(nil, format.FileOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if item.Name != "widget" || item.Value != 42 {
		t.Errorf("got %+v", item)
	}
}

func TestRead_PathVarError_NoIO(t *testing.T) {
	spy := &fileObserverSpy{}
	_, err := templateFile.Read(
		map[string]string{"category": "", "id": "1"},
		format.FileOptions{Observer: spy},
	)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var paramErr format.FilePathParamError
	if !errors.As(err, &paramErr) {
		t.Fatalf("expected FilePathParamError, got %T", err)
	}
	// No I/O means no observer callbacks
	if len(spy.reads) != 0 {
		t.Errorf("expected no observer reads, got %d", len(spy.reads))
	}
}

func TestRead_FileNotFound_ReturnsFileReadError(t *testing.T) {
	f := format.NewFile("/nonexistent/path/item.json", format.JSON(fileItemCodec))
	_, err := f.Read(nil, format.FileOptions{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var readErr format.FileReadError
	if !errors.As(err, &readErr) {
		t.Fatalf("expected FileReadError, got %T: %v", err, err)
	}
	if readErr.Path != "/nonexistent/path/item.json" {
		t.Errorf("unexpected path: %q", readErr.Path)
	}
	if readErr.Unwrap() == nil {
		t.Error("Unwrap() must return non-nil")
	}
}

func TestRead_InvalidContent_ReturnsFileDecodeError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "item.json")
	// value 9999 exceeds RangeInt(0, 1000)
	_ = os.WriteFile(path, []byte(`{"name":"widget","value":9999}`), 0644)

	f := format.NewFile(path, format.JSON(fileItemCodec))
	_, err := f.Read(nil, format.FileOptions{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var decErr format.FileDecodeError
	if !errors.As(err, &decErr) {
		t.Fatalf("expected FileDecodeError, got %T: %v", err, err)
	}
	if decErr.Unwrap() == nil {
		t.Error("Unwrap() must return non-nil")
	}
}

// ── Write ─────────────────────────────────────────────────────────────────────

func TestWrite_HappyPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "item.json")
	f := format.NewFile(path, format.JSON(fileItemCodec))

	err := f.Write(nil, fileItem{Name: "gadget", Value: 7}, format.FileOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "gadget") {
		t.Errorf("written file missing expected content: %s", data)
	}
}

func TestWrite_PathVarError_NoIO(t *testing.T) {
	spy := &fileObserverSpy{}
	err := templateFile.Write(
		map[string]string{"category": "", "id": "1"},
		fileItem{Name: "x", Value: 1},
		format.FileOptions{Observer: spy},
	)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var paramErr format.FilePathParamError
	if !errors.As(err, &paramErr) {
		t.Fatalf("expected FilePathParamError, got %T", err)
	}
	if len(spy.writes) != 0 {
		t.Errorf("expected no observer writes, got %d", len(spy.writes))
	}
}

func TestWrite_EncodeFailure_ReturnsFileEncodeError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "item.json")
	f := format.NewFile(path, format.JSON(fileItemCodec))

	// value 9999 violates RangeInt(0,1000) — encode/marshal should fail constraint
	err := f.Write(nil, fileItem{Name: "x", Value: 9999}, format.FileOptions{})
	if err == nil {
		t.Fatal("expected error for out-of-range value, got nil")
	}
	var encErr format.FileEncodeError
	if !errors.As(err, &encErr) {
		t.Fatalf("expected FileEncodeError, got %T: %v", err, err)
	}
	if encErr.Unwrap() == nil {
		t.Error("Unwrap() must return non-nil")
	}
}

func TestWrite_IOFailure_ReturnsFileWriteError(t *testing.T) {
	// Write to non-existent directory
	f := format.NewFile("/nonexistent/dir/item.json", format.JSON(fileItemCodec))
	err := f.Write(nil, fileItem{Name: "x", Value: 1}, format.FileOptions{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var writeErr format.FileWriteError
	if !errors.As(err, &writeErr) {
		t.Fatalf("expected FileWriteError, got %T: %v", err, err)
	}
	if writeErr.Unwrap() == nil {
		t.Error("Unwrap() must return non-nil")
	}
}

func TestWrite_CustomPerm_SetOnFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "item.json")
	f := format.NewFile(path, format.JSON(fileItemCodec))

	err := f.Write(nil, fileItem{Name: "x", Value: 1}, format.FileOptions{Perm: 0600})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat failed: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("expected perm 0600, got %o", info.Mode().Perm())
	}
}

// ── Update ────────────────────────────────────────────────────────────────────

func TestUpdate_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "item.json")
	f := format.NewFile(path, format.JSON(fileItemCodec))

	// Write initial value
	if err := f.Write(nil, fileItem{Name: "widget", Value: 10}, format.FileOptions{}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Update: increment value
	if err := f.Update(nil, func(item fileItem) fileItem {
		item.Value += 5
		return item
	}, format.FileOptions{}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	// Read back and verify
	result, err := f.Read(nil, format.FileOptions{})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if result.Value != 15 {
		t.Errorf("expected Value=15, got %d", result.Value)
	}
	if result.Name != "widget" {
		t.Errorf("expected Name=widget, got %q", result.Name)
	}
}

func TestUpdate_ReadFailure_Propagates(t *testing.T) {
	f := format.NewFile("/nonexistent/item.json", format.JSON(fileItemCodec))
	err := f.Update(nil, func(item fileItem) fileItem { return item }, format.FileOptions{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var readErr format.FileReadError
	if !errors.As(err, &readErr) {
		t.Fatalf("expected FileReadError, got %T", err)
	}
}

// ── FileObserver ──────────────────────────────────────────────────────────────

func TestFileObserver_ReadSuccess_CallsRecordFileRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "item.json")
	_ = os.WriteFile(path, []byte(`{"name":"x","value":1}`), 0644)
	f := format.NewFile(path, format.JSON(fileItemCodec))

	spy := &fileObserverSpy{}
	_, err := f.Read(nil, format.FileOptions{Observer: spy})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(spy.reads) != 1 {
		t.Fatalf("expected 1 read callback, got %d", len(spy.reads))
	}
	if !spy.reads[0].success {
		t.Error("expected success=true")
	}
	if spy.reads[0].path != path {
		t.Errorf("expected path=%q, got %q", path, spy.reads[0].path)
	}
}

func TestFileObserver_ReadFileNotFound_CallsRecordFileReadFalse(t *testing.T) {
	f := format.NewFile("/nonexistent/item.json", format.JSON(fileItemCodec))
	spy := &fileObserverSpy{}
	_, _ = f.Read(nil, format.FileOptions{Observer: spy})
	if len(spy.reads) != 1 {
		t.Fatalf("expected 1 read callback, got %d", len(spy.reads))
	}
	if spy.reads[0].success {
		t.Error("expected success=false on file-not-found")
	}
}

func TestFileObserver_ReadDecodeFailure_CallsRecordFileReadFalse(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "item.json")
	_ = os.WriteFile(path, []byte(`{"name":"x","value":9999}`), 0644)
	f := format.NewFile(path, format.JSON(fileItemCodec))

	spy := &fileObserverSpy{}
	_, _ = f.Read(nil, format.FileOptions{Observer: spy})
	if len(spy.reads) != 1 {
		t.Fatalf("expected 1 read callback, got %d", len(spy.reads))
	}
	if spy.reads[0].success {
		t.Error("expected success=false on decode failure")
	}
}

func TestFileObserver_WriteSuccess_CallsRecordFileWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "item.json")
	f := format.NewFile(path, format.JSON(fileItemCodec))

	spy := &fileObserverSpy{}
	_ = f.Write(nil, fileItem{Name: "x", Value: 1}, format.FileOptions{Observer: spy})
	if len(spy.writes) != 1 {
		t.Fatalf("expected 1 write callback, got %d", len(spy.writes))
	}
	if !spy.writes[0].success {
		t.Error("expected success=true")
	}
	if spy.writes[0].path != path {
		t.Errorf("expected path=%q, got %q", path, spy.writes[0].path)
	}
}

func TestFileObserver_WriteEncodeFailure_CallsRecordFileWriteFalse(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "item.json")
	f := format.NewFile(path, format.JSON(fileItemCodec))

	spy := &fileObserverSpy{}
	_ = f.Write(nil, fileItem{Name: "x", Value: 9999}, format.FileOptions{Observer: spy})
	if len(spy.writes) != 1 {
		t.Fatalf("expected 1 write callback, got %d", len(spy.writes))
	}
	if spy.writes[0].success {
		t.Error("expected success=false on encode failure")
	}
}

func TestFileObserver_WriteIOFailure_CallsRecordFileWriteFalse(t *testing.T) {
	f := format.NewFile("/nonexistent/dir/item.json", format.JSON(fileItemCodec))
	spy := &fileObserverSpy{}
	_ = f.Write(nil, fileItem{Name: "x", Value: 1}, format.FileOptions{Observer: spy})
	if len(spy.writes) != 1 {
		t.Fatalf("expected 1 write callback, got %d", len(spy.writes))
	}
	if spy.writes[0].success {
		t.Error("expected success=false on I/O failure")
	}
}

func TestFileObserver_NilObserver_NoPanic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "item.json")
	f := format.NewFile(path, format.JSON(fileItemCodec))

	// No panic on nil observer
	_ = f.Write(nil, fileItem{Name: "x", Value: 1}, format.FileOptions{Observer: nil})
	_, _ = f.Read(nil, format.FileOptions{Observer: nil})
}

// ── Error type surfaces ───────────────────────────────────────────────────────

func TestFileReadError_ErrorAndUnwrap(t *testing.T) {
	inner := errors.New("permission denied")
	e := format.FileReadError{Path: "/a/b.json", Err: inner}
	if !strings.Contains(e.Error(), "/a/b.json") {
		t.Errorf("Error() missing path: %q", e.Error())
	}
	if !errors.Is(e, inner) {
		t.Error("errors.Is should find inner error via Unwrap")
	}
}

func TestFileDecodeError_ErrorAndUnwrap(t *testing.T) {
	inner := errors.New("invalid json")
	e := format.FileDecodeError{Path: "/a/b.json", Err: inner}
	if !strings.Contains(e.Error(), "/a/b.json") {
		t.Errorf("Error() missing path: %q", e.Error())
	}
	if !errors.Is(e, inner) {
		t.Error("errors.Is should find inner error via Unwrap")
	}
}

func TestFileEncodeError_ErrorAndUnwrap(t *testing.T) {
	inner := errors.New("marshal failed")
	e := format.FileEncodeError{Path: "/a/b.json", Err: inner}
	if !strings.Contains(e.Error(), "/a/b.json") {
		t.Errorf("Error() missing path: %q", e.Error())
	}
	if !errors.Is(e, inner) {
		t.Error("errors.Is should find inner error via Unwrap")
	}
}

func TestFileWriteError_ErrorAndUnwrap(t *testing.T) {
	inner := errors.New("disk full")
	e := format.FileWriteError{Path: "/a/b.json", Err: inner}
	if !strings.Contains(e.Error(), "/a/b.json") {
		t.Errorf("Error() missing path: %q", e.Error())
	}
	if !errors.Is(e, inner) {
		t.Error("errors.Is should find inner error via Unwrap")
	}
}
