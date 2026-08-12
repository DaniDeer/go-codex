package ports_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/ports"
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

// fileItemPatch is a separate patch type for PatchEncoded tests — only the
// fields that should be patchable. Fields absent from this struct are always
// preserved in the file regardless of what value fileItem has.
type fileItemPatch struct {
	Value int `json:"value"`
}

// fileItemPatchCodec encodes only the Value field — used with PatchEncoded.
var fileItemPatchCodec = codex.Struct[fileItemPatch](
	codex.RequiredField("value",
		codex.Int().Refine(validate.RangeInt(0, 1000)),
		func(p fileItemPatch) int { return p.Value },
		func(p *fileItemPatch, i int) { p.Value = i },
	),
)

var staticFile = ports.NewFile("item.json", format.JSON(fileItemCodec))

var templateFile = ports.NewFile(
	"data/{category}/{id}.json",
	format.JSON(fileItemCodec),
	ports.FilePathParam{Name: "category", Description: "Item category"}.
		WithCodec(codex.String().Refine(validate.NonEmptyString)),
	ports.FilePathParam{Name: "id", Description: "Item ID (numeric)"},
)

// fileObserverSpy captures FileObserver callbacks for assertions.
type fileObserverSpy struct {
	stats.NoopObserver

	reads   []fileObsCall
	writes  []fileObsCall
	deletes []fileObsCall
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

func (o *fileObserverSpy) RecordFileDelete(path string, success bool, d time.Duration) {
	o.deletes = append(o.deletes, fileObsCall{path, success, d})
}

var _ stats.FileObserver = (*fileObserverSpy)(nil)

// ── FilePathParam.WithCodec ───────────────────────────────────────────────────

func TestFilePathParam_WithCodec_SetsCodec(t *testing.T) {
	c := codex.String().Refine(validate.NonEmptyString)
	p := ports.FilePathParam{Name: "sensor"}.WithCodec(c)
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
	f := ports.NewFile(
		"data/{category}/{id}.json",
		format.JSON(fileItemCodec),
		ports.FilePathParam{Name: "category"},
		ports.FilePathParam{Name: "id"},
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
	var missing ports.MissingFilePathVarError
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
	var paramErr ports.FilePathParamError
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
	e := ports.FilePathParamError{Name: "date", Value: "bad", Err: errors.New("invalid")}
	s := e.Error()
	if !strings.Contains(s, "date") || !strings.Contains(s, "bad") {
		t.Errorf("Error() missing param name or value: %q", s)
	}
}

func TestBuildPath_MissingFilePathVarError_ErrorString(t *testing.T) {
	e := ports.MissingFilePathVarError{Name: "date"}
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
	var missing ports.MissingFilePathVarError
	if !errors.As(err, &missing) {
		t.Fatalf("expected MissingFilePathVarError, got %T", err)
	}
}

func TestValidatePathVars_InvalidVar_ReturnsParamError(t *testing.T) {
	err := templateFile.ValidatePathVars(map[string]string{"category": "", "id": "42"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var paramErr ports.FilePathParamError
	if !errors.As(err, &paramErr) {
		t.Fatalf("expected FilePathParamError, got %T", err)
	}
}

func TestValidatePathVars_NoCodecs_AlwaysNil(t *testing.T) {
	f := ports.NewFile("data/{a}/{b}.json", format.JSON(fileItemCodec),
		ports.FilePathParam{Name: "a"},
		ports.FilePathParam{Name: "b"},
	)
	// Even missing vars are fine — ValidatePathVars only validates codecs
	err := f.ValidatePathVars(map[string]string{})
	if err != nil {
		t.Fatalf("expected nil for no-codec params, got: %v", err)
	}
}

// ── PathParamSchemas ──────────────────────────────────────────────────────────

func TestPathParamSchemas_ReturnsSchemaForParamsWithCodec(t *testing.T) {
	schemas := templateFile.PathParamSchemas()
	// templateFile has "category" with codec and "id" without codec.
	if _, ok := schemas["category"]; !ok {
		t.Error("expected schema for 'category' (has codec)")
	}
	if _, ok := schemas["id"]; ok {
		t.Error("expected no schema for 'id' (no codec)")
	}
}

func TestPathParamSchemas_EmptyWhenNoCodecs(t *testing.T) {
	f := ports.NewFile("data/{a}/{b}.json", format.JSON(fileItemCodec),
		ports.FilePathParam{Name: "a"},
		ports.FilePathParam{Name: "b"},
	)
	schemas := f.PathParamSchemas()
	if len(schemas) != 0 {
		t.Errorf("expected empty map, got %d entries", len(schemas))
	}
}

func TestPathParamSchemas_StaticPath_EmptyMap(t *testing.T) {
	schemas := staticFile.PathParamSchemas()
	if len(schemas) != 0 {
		t.Errorf("expected empty map for static file, got %d entries", len(schemas))
	}
}

// ── Read ──────────────────────────────────────────────────────────────────────

func TestRead_HappyPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "item.json")
	_ = os.WriteFile(path, []byte(`{"name":"widget","value":42}`), 0644)

	f := ports.NewFile(path, format.JSON(fileItemCodec))
	item, err := f.Read(nil, ports.FileOptions{})
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
		ports.FileOptions{Observer: spy},
	)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var paramErr ports.FilePathParamError
	if !errors.As(err, &paramErr) {
		t.Fatalf("expected FilePathParamError, got %T", err)
	}
	// No I/O means no observer callbacks
	if len(spy.reads) != 0 {
		t.Errorf("expected no observer reads, got %d", len(spy.reads))
	}
}

func TestRead_FileNotFound_ReturnsFileReadError(t *testing.T) {
	f := ports.NewFile("/nonexistent/path/item.json", format.JSON(fileItemCodec))
	_, err := f.Read(nil, ports.FileOptions{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var readErr ports.FileReadError
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

	f := ports.NewFile(path, format.JSON(fileItemCodec))
	_, err := f.Read(nil, ports.FileOptions{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var decErr ports.FileDecodeError
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
	f := ports.NewFile(path, format.JSON(fileItemCodec))

	_, err := f.Write(nil, fileItem{Name: "gadget", Value: 7}, ports.FileOptions{})
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
	_, err := templateFile.Write(
		map[string]string{"category": "", "id": "1"},
		fileItem{Name: "x", Value: 1},
		ports.FileOptions{Observer: spy},
	)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var paramErr ports.FilePathParamError
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
	f := ports.NewFile(path, format.JSON(fileItemCodec))

	// value 9999 violates RangeInt(0,1000) — encode/marshal should fail constraint
	_, err := f.Write(nil, fileItem{Name: "x", Value: 9999}, ports.FileOptions{})
	if err == nil {
		t.Fatal("expected error for out-of-range value, got nil")
	}
	var encErr ports.FileEncodeError
	if !errors.As(err, &encErr) {
		t.Fatalf("expected FileEncodeError, got %T: %v", err, err)
	}
	if encErr.Unwrap() == nil {
		t.Error("Unwrap() must return non-nil")
	}
}

func TestWrite_IOFailure_ReturnsFileWriteError(t *testing.T) {
	// Write to non-existent directory
	f := ports.NewFile("/nonexistent/dir/item.json", format.JSON(fileItemCodec))
	_, err := f.Write(nil, fileItem{Name: "x", Value: 1}, ports.FileOptions{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var writeErr ports.FileWriteError
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
	f := ports.NewFile(path, format.JSON(fileItemCodec))

	_, err := f.Write(nil, fileItem{Name: "x", Value: 1}, ports.FileOptions{Perm: 0600})
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

func TestWrite_CreateDirs_CreatesMissingParents(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a", "b", "c", "item.json")
	f := ports.NewFile(path, format.JSON(fileItemCodec))

	_, err := f.Write(nil, fileItem{Name: "gadget", Value: 7}, ports.FileOptions{CreateDirs: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected file to exist after CreateDirs write: %v", err)
	}
	if !strings.Contains(string(data), "gadget") {
		t.Errorf("written file missing expected content: %s", data)
	}
}

func TestWrite_CreateDirsFalse_DefaultUnchanged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "missing", "item.json")
	f := ports.NewFile(path, format.JSON(fileItemCodec))

	// CreateDirs defaults to false — behavior must be identical to before
	// this option existed: FileWriteError for a missing parent directory.
	_, err := f.Write(nil, fileItem{Name: "x", Value: 1}, ports.FileOptions{})
	if err == nil {
		t.Fatal("expected error for missing parent directory, got nil")
	}
	var writeErr ports.FileWriteError
	if !errors.As(err, &writeErr) {
		t.Fatalf("expected FileWriteError, got %T: %v", err, err)
	}
}

func TestWrite_CreateDirs_MkdirFailure_ReturnsFileWriteError(t *testing.T) {
	dir := t.TempDir()
	// Create a FILE where a directory is expected — MkdirAll must fail.
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0644); err != nil {
		t.Fatalf("setup WriteFile: %v", err)
	}
	path := filepath.Join(blocker, "sub", "item.json")
	f := ports.NewFile(path, format.JSON(fileItemCodec))

	_, err := f.Write(nil, fileItem{Name: "x", Value: 1}, ports.FileOptions{CreateDirs: true})
	if err == nil {
		t.Fatal("expected error for MkdirAll through a file path segment, got nil")
	}
	var writeErr ports.FileWriteError
	if !errors.As(err, &writeErr) {
		t.Fatalf("expected FileWriteError, got %T: %v", err, err)
	}
}

func TestWrite_CreateDirs_ReturnsCreatedDirs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a", "b", "item.json")
	f := ports.NewFile(path, format.JSON(fileItemCodec))

	created, err := f.Write(nil, fileItem{Name: "x", Value: 1}, ports.FileOptions{CreateDirs: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(created) != 2 {
		t.Fatalf("createdDirs = %v, want 2 entries (a, a/b)", created)
	}
}

func TestWrite_CreateDirsFalse_ReturnsEmptyCreatedDirs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "item.json")
	f := ports.NewFile(path, format.JSON(fileItemCodec))

	created, err := f.Write(nil, fileItem{Name: "x", Value: 1}, ports.FileOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(created) != 0 {
		t.Errorf("createdDirs = %v, want empty", created)
	}
}

func TestWrite_DryRun_CreateDirs_ComputesWithoutCreating(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a", "b", "item.json")
	f := ports.NewFile(path, format.JSON(fileItemCodec))

	created, err := f.Write(nil, fileItem{Name: "x", Value: 1}, ports.FileOptions{CreateDirs: true, DryRun: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(created) != 2 {
		t.Fatalf("createdDirs = %v, want 2 entries (would-be-created)", created)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "a")); statErr == nil {
		t.Error("DryRun must not actually create directories")
	}
	if _, statErr := os.Stat(path); statErr == nil {
		t.Error("DryRun must not actually write the file")
	}
}

func TestWrite_DryRun_StillRunsEncode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "item.json")
	f := ports.NewFile(path, format.JSON(codex.Struct[fileItem](
		codex.RequiredField("name", codex.String().Refine(validate.MinLen(3)),
			func(i fileItem) string { return i.Name },
			func(i *fileItem, v string) { i.Name = v },
		),
		codex.RequiredField("value", codex.Int(),
			func(i fileItem) int { return i.Value },
			func(i *fileItem, v int) { i.Value = v },
		),
	)))

	_, err := f.Write(nil, fileItem{Name: "ab", Value: 1}, ports.FileOptions{DryRun: true})
	if err == nil {
		t.Fatal("expected FileEncodeError for name failing MinLen(3), got nil")
	}
	var encErr ports.FileEncodeError
	if !errors.As(err, &encErr) {
		t.Fatalf("expected FileEncodeError, got %T: %v", err, err)
	}
}

func TestWrite_Strict_ExistingFile_ReturnsFileAlreadyExistsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "item.json")
	f := ports.NewFile(path, format.JSON(fileItemCodec))

	if _, err := f.Write(nil, fileItem{Name: "first", Value: 1}, ports.FileOptions{}); err != nil {
		t.Fatalf("setup Write: %v", err)
	}

	_, err := f.Write(nil, fileItem{Name: "second", Value: 2}, ports.FileOptions{Strict: true})
	if err == nil {
		t.Fatal("expected error for Strict write to an existing file, got nil")
	}
	var existsErr ports.FileAlreadyExistsError
	if !errors.As(err, &existsErr) {
		t.Fatalf("expected FileAlreadyExistsError, got %T: %v", err, err)
	}

	// Original content must survive untouched.
	got, readErr := f.Read(nil, ports.FileOptions{})
	if readErr != nil {
		t.Fatalf("Read: %v", readErr)
	}
	if got.Name != "first" {
		t.Errorf("Name = %q, want %q (Strict write must not overwrite)", got.Name, "first")
	}
}

func TestWrite_Strict_MissingFile_WritesNormally(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "item.json")
	f := ports.NewFile(path, format.JSON(fileItemCodec))

	_, err := f.Write(nil, fileItem{Name: "gadget", Value: 7}, ports.FileOptions{Strict: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, readErr := f.Read(nil, ports.FileOptions{})
	if readErr != nil {
		t.Fatalf("Read: %v", readErr)
	}
	if got.Name != "gadget" {
		t.Errorf("Name = %q, want %q", got.Name, "gadget")
	}
}

func TestWrite_Strict_DryRun_ExistingFile_ReportsWithoutWriting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "item.json")
	f := ports.NewFile(path, format.JSON(fileItemCodec))

	if _, err := f.Write(nil, fileItem{Name: "first", Value: 1}, ports.FileOptions{}); err != nil {
		t.Fatalf("setup Write: %v", err)
	}

	_, err := f.Write(nil, fileItem{Name: "second", Value: 2}, ports.FileOptions{Strict: true, DryRun: true})
	if err == nil {
		t.Fatal("expected FileAlreadyExistsError, got nil")
	}
	var existsErr ports.FileAlreadyExistsError
	if !errors.As(err, &existsErr) {
		t.Fatalf("expected FileAlreadyExistsError, got %T: %v", err, err)
	}
	got, readErr := f.Read(nil, ports.FileOptions{})
	if readErr != nil {
		t.Fatalf("Read: %v", readErr)
	}
	if got.Name != "first" {
		t.Errorf("Name = %q, want %q (DryRun must not overwrite)", got.Name, "first")
	}
}

// ── Delete ────────────────────────────────────────────────────────────────────

func TestFile_Delete_RemovesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "item.json")
	f := ports.NewFile(path, format.JSON(fileItemCodec))
	if _, err := f.Write(nil, fileItem{Name: "x", Value: 1}, ports.FileOptions{}); err != nil {
		t.Fatalf("setup Write: %v", err)
	}

	existed, err := f.Delete(nil, ports.FileOptions{})
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if !existed {
		t.Error("existed = false, want true")
	}
	if _, err := f.Read(nil, ports.FileOptions{}); err == nil {
		t.Error("expected Read to fail after Delete")
	}
}

func TestFile_Delete_MissingFile_IdempotentSuccess(t *testing.T) {
	dir := t.TempDir()
	f := ports.NewFile(filepath.Join(dir, "missing.json"), format.JSON(fileItemCodec))

	existed, err := f.Delete(nil, ports.FileOptions{})
	if err != nil {
		t.Fatalf("Delete: unexpected error: %v", err)
	}
	if existed {
		t.Error("existed = true, want false")
	}
}

func TestFile_Delete_PathVarError_NoIO(t *testing.T) {
	_, err := templateFile.Delete(map[string]string{"category": "", "id": "1"}, ports.FileOptions{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var paramErr ports.FilePathParamError
	if !errors.As(err, &paramErr) {
		t.Fatalf("expected FilePathParamError, got %T", err)
	}
}

func TestFile_Delete_DryRun_DoesNotRemoveFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "item.json")
	f := ports.NewFile(path, format.JSON(fileItemCodec))
	if _, err := f.Write(nil, fileItem{Name: "x", Value: 1}, ports.FileOptions{}); err != nil {
		t.Fatalf("setup Write: %v", err)
	}

	existed, err := f.Delete(nil, ports.FileOptions{DryRun: true})
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if !existed {
		t.Error("existed = false, want true")
	}
	if _, err := f.Read(nil, ports.FileOptions{}); err != nil {
		t.Error("DryRun must not actually remove the file")
	}
}

func TestFile_Delete_Strict_MissingFile_ReturnsFileNotFoundError(t *testing.T) {
	dir := t.TempDir()
	f := ports.NewFile(filepath.Join(dir, "missing.json"), format.JSON(fileItemCodec))

	_, err := f.Delete(nil, ports.FileOptions{Strict: true})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var notFoundErr ports.FileNotFoundError
	if !errors.As(err, &notFoundErr) {
		t.Fatalf("expected FileNotFoundError, got %T: %v", err, err)
	}
}

func TestFile_Delete_Strict_ExistingFile_RemovesNormally(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "item.json")
	f := ports.NewFile(path, format.JSON(fileItemCodec))
	if _, err := f.Write(nil, fileItem{Name: "x", Value: 1}, ports.FileOptions{}); err != nil {
		t.Fatalf("setup Write: %v", err)
	}

	existed, err := f.Delete(nil, ports.FileOptions{Strict: true})
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if !existed {
		t.Error("existed = false, want true")
	}
}

func TestFile_Delete_ObserverRecordsFileDelete(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "item.json")
	f := ports.NewFile(path, format.JSON(fileItemCodec))
	if _, err := f.Write(nil, fileItem{Name: "x", Value: 1}, ports.FileOptions{}); err != nil {
		t.Fatalf("setup Write: %v", err)
	}

	spy := &fileObserverSpy{}
	if _, err := f.Delete(nil, ports.FileOptions{Observer: spy}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(spy.deletes) != 1 || !spy.deletes[0].success {
		t.Fatalf("RecordFileDelete calls = %+v, want one successful call", spy.deletes)
	}

	// Idempotent-absent case still records success.
	spy2 := &fileObserverSpy{}
	if _, err := f.Delete(nil, ports.FileOptions{Observer: spy2}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(spy2.deletes) != 1 || !spy2.deletes[0].success {
		t.Fatalf("RecordFileDelete calls = %+v, want one successful call (idempotent-absent)", spy2.deletes)
	}

	// Strict-triggered FileNotFoundError still records (success=false).
	spy3 := &fileObserverSpy{}
	if _, err := f.Delete(nil, ports.FileOptions{Observer: spy3, Strict: true}); err == nil {
		t.Fatal("expected error, got nil")
	}
	if len(spy3.deletes) != 1 || spy3.deletes[0].success {
		t.Fatalf("RecordFileDelete calls = %+v, want one failed call", spy3.deletes)
	}
}

// ── Update ────────────────────────────────────────────────────────────────────

func TestUpdate_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "item.json")
	f := ports.NewFile(path, format.JSON(fileItemCodec))

	// Write initial value
	if _, err := f.Write(nil, fileItem{Name: "widget", Value: 10}, ports.FileOptions{}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Update: increment value
	if _, err := f.Update(nil, func(item fileItem) fileItem {
		item.Value += 5
		return item
	}, ports.FileOptions{}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	// Read back and verify
	result, err := f.Read(nil, ports.FileOptions{})
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

func TestUpdate_PropagatesCreatedDirs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "item.json")
	f := ports.NewFile(path, format.JSON(fileItemCodec))
	if _, err := f.Write(nil, fileItem{Name: "widget", Value: 10}, ports.FileOptions{}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// No dirs missing here (the file already exists in dir) — createdDirs
	// should be empty since CreateDirs is off by default.
	created, err := f.Update(nil, func(item fileItem) fileItem {
		item.Value++
		return item
	}, ports.FileOptions{})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if len(created) != 0 {
		t.Errorf("createdDirs = %v, want empty (CreateDirs not set)", created)
	}
}

func TestUpdate_ReadFailure_Propagates(t *testing.T) {
	f := ports.NewFile("/nonexistent/item.json", format.JSON(fileItemCodec))
	_, err := f.Update(nil, func(item fileItem) fileItem { return item }, ports.FileOptions{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var readErr ports.FileReadError
	if !errors.As(err, &readErr) {
		t.Fatalf("expected FileReadError, got %T", err)
	}
}

// ── FileObserver ──────────────────────────────────────────────────────────────

func TestFileObserver_ReadSuccess_CallsRecordFileRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "item.json")
	_ = os.WriteFile(path, []byte(`{"name":"x","value":1}`), 0644)
	f := ports.NewFile(path, format.JSON(fileItemCodec))

	spy := &fileObserverSpy{}
	_, err := f.Read(nil, ports.FileOptions{Observer: spy})
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
	f := ports.NewFile("/nonexistent/item.json", format.JSON(fileItemCodec))
	spy := &fileObserverSpy{}
	_, _ = f.Read(nil, ports.FileOptions{Observer: spy})
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
	f := ports.NewFile(path, format.JSON(fileItemCodec))

	spy := &fileObserverSpy{}
	_, _ = f.Read(nil, ports.FileOptions{Observer: spy})
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
	f := ports.NewFile(path, format.JSON(fileItemCodec))

	spy := &fileObserverSpy{}
	_, _ = f.Write(nil, fileItem{Name: "x", Value: 1}, ports.FileOptions{Observer: spy})
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
	f := ports.NewFile(path, format.JSON(fileItemCodec))

	spy := &fileObserverSpy{}
	_, _ = f.Write(nil, fileItem{Name: "x", Value: 9999}, ports.FileOptions{Observer: spy})
	if len(spy.writes) != 1 {
		t.Fatalf("expected 1 write callback, got %d", len(spy.writes))
	}
	if spy.writes[0].success {
		t.Error("expected success=false on encode failure")
	}
}

func TestFileObserver_WriteIOFailure_CallsRecordFileWriteFalse(t *testing.T) {
	f := ports.NewFile("/nonexistent/dir/item.json", format.JSON(fileItemCodec))
	spy := &fileObserverSpy{}
	_, _ = f.Write(nil, fileItem{Name: "x", Value: 1}, ports.FileOptions{Observer: spy})
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
	f := ports.NewFile(path, format.JSON(fileItemCodec))

	// No panic on nil observer
	_, _ = f.Write(nil, fileItem{Name: "x", Value: 1}, ports.FileOptions{Observer: nil})
	_, _ = f.Read(nil, ports.FileOptions{Observer: nil})
}

// ── Error type surfaces ───────────────────────────────────────────────────────

func TestFileReadError_ErrorAndUnwrap(t *testing.T) {
	inner := errors.New("permission denied")
	e := ports.FileReadError{Path: "/a/b.json", Err: inner}
	if !strings.Contains(e.Error(), "/a/b.json") {
		t.Errorf("Error() missing path: %q", e.Error())
	}
	if !errors.Is(e, inner) {
		t.Error("errors.Is should find inner error via Unwrap")
	}
}

func TestFileDecodeError_ErrorAndUnwrap(t *testing.T) {
	inner := errors.New("invalid json")
	e := ports.FileDecodeError{Path: "/a/b.json", Err: inner}
	if !strings.Contains(e.Error(), "/a/b.json") {
		t.Errorf("Error() missing path: %q", e.Error())
	}
	if !errors.Is(e, inner) {
		t.Error("errors.Is should find inner error via Unwrap")
	}
}

func TestFileEncodeError_ErrorAndUnwrap(t *testing.T) {
	inner := errors.New("marshal failed")
	e := ports.FileEncodeError{Path: "/a/b.json", Err: inner}
	if !strings.Contains(e.Error(), "/a/b.json") {
		t.Errorf("Error() missing path: %q", e.Error())
	}
	if !errors.Is(e, inner) {
		t.Error("errors.Is should find inner error via Unwrap")
	}
}

func TestFileWriteError_ErrorAndUnwrap(t *testing.T) {
	inner := errors.New("disk full")
	e := ports.FileWriteError{Path: "/a/b.json", Err: inner}
	if !strings.Contains(e.Error(), "/a/b.json") {
		t.Errorf("Error() missing path: %q", e.Error())
	}
	if !errors.Is(e, inner) {
		t.Error("errors.Is should find inner error via Unwrap")
	}
}

// ── slog.LogValuer on file error types ────────────────────────────────────────

func TestFilePathParamError_LogValue(t *testing.T) {
	e := ports.FilePathParamError{Name: "date", Value: "bad", Err: errors.New("invalid")}
	v := e.LogValue()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("expected GroupValue, got %v", v.Kind())
	}
	attrs := v.Group()
	keys := make(map[string]bool, len(attrs))
	for _, a := range attrs {
		keys[a.Key] = true
	}
	for _, want := range []string{"param", "value", "cause"} {
		if !keys[want] {
			t.Errorf("LogValue missing attr %q", want)
		}
	}
}

func TestMissingFilePathVarError_LogValue(t *testing.T) {
	e := ports.MissingFilePathVarError{Name: "sensor"}
	v := e.LogValue()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("expected GroupValue, got %v", v.Kind())
	}
	attrs := v.Group()
	if len(attrs) == 0 || attrs[0].Key != "param" {
		t.Errorf("expected first attr key=param, got %+v", attrs)
	}
}

func TestFileReadError_LogValue(t *testing.T) {
	e := ports.FileReadError{Path: "/a/b.json", Err: errors.New("not found")}
	v := e.LogValue()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("expected GroupValue, got %v", v.Kind())
	}
	keys := make(map[string]bool)
	for _, a := range v.Group() {
		keys[a.Key] = true
	}
	for _, want := range []string{"path", "cause"} {
		if !keys[want] {
			t.Errorf("LogValue missing attr %q", want)
		}
	}
}

func TestFileDecodeError_LogValue(t *testing.T) {
	e := ports.FileDecodeError{Path: "/a/b.json", Err: errors.New("bad json")}
	v := e.LogValue()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("expected GroupValue, got %v", v.Kind())
	}
}

func TestFileEncodeError_LogValue(t *testing.T) {
	e := ports.FileEncodeError{Path: "/a/b.json", Err: errors.New("constraint")}
	v := e.LogValue()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("expected GroupValue, got %v", v.Kind())
	}
}

func TestFileWriteError_LogValue(t *testing.T) {
	e := ports.FileWriteError{Path: "/a/b.json", Err: errors.New("disk full")}
	v := e.LogValue()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("expected GroupValue, got %v", v.Kind())
	}
}

func TestFilePatchNotSupportedError_LogValue(t *testing.T) {
	e := ports.FilePatchNotSupportedError{Path: "/a/b.gob"}
	v := e.LogValue()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("expected GroupValue, got %v", v.Kind())
	}
	if len(v.Group()) == 0 || v.Group()[0].Key != "path" {
		t.Errorf("expected first attr key=path, got %+v", v.Group())
	}
}

func TestFilePatchNotSupportedError_ErrorAndUnwrap(t *testing.T) {
	e := ports.FilePatchNotSupportedError{Path: "/a/b.gob"}
	if !strings.Contains(e.Error(), "/a/b.gob") {
		t.Errorf("Error() missing path: %q", e.Error())
	}
}

// ── IsPatchable ───────────────────────────────────────────────────────────────

func TestIsPatchable_JSON_True(t *testing.T) {
	f := format.JSON(fileItemCodec)
	if !f.IsPatchable() {
		t.Error("JSON format must be patchable")
	}
}

func TestIsPatchable_YAML_True(t *testing.T) {
	f := format.YAML(fileItemCodec)
	if !f.IsPatchable() {
		t.Error("YAML format must be patchable")
	}
}

func TestIsPatchable_TOML_True(t *testing.T) {
	f := format.TOML(fileItemCodec)
	if !f.IsPatchable() {
		t.Error("TOML format must be patchable")
	}
}

func TestIsPatchable_Gob_False(t *testing.T) {
	f := format.Gob(fileItemCodec)
	if f.IsPatchable() {
		t.Error("Gob format must not be patchable")
	}
}

func TestIsPatchable_Binary_False(t *testing.T) {
	f := format.Binary(codex.Bytes())
	if f.IsPatchable() {
		t.Error("Binary format must not be patchable")
	}
}

// ── File.Patch happy paths ────────────────────────────────────────────────────

func TestPatch_JSON_OnlyPatchedFieldChanges(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "item.json")
	f := ports.NewFile(path, format.JSON(fileItemCodec))

	// Write initial value
	if _, err := f.Write(nil, fileItem{Name: "original", Value: 42}, ports.FileOptions{}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Patch only Value — Name must survive unchanged
	if err := f.Patch(nil, map[string]any{"value": 99}, ports.FileOptions{}); err != nil {
		t.Fatalf("Patch: %v", err)
	}

	got, err := f.Read(nil, ports.FileOptions{})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.Name != "original" {
		t.Errorf("Name changed unexpectedly: want %q, got %q", "original", got.Name)
	}
	if got.Value != 99 {
		t.Errorf("Value not patched: want 99, got %d", got.Value)
	}
}

func TestPatch_YAML_HappyPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "item.yaml")
	f := ports.NewFile(path, format.YAML(fileItemCodec))

	if _, err := f.Write(nil, fileItem{Name: "yaml-item", Value: 10}, ports.FileOptions{}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if err := f.Patch(nil, map[string]any{"value": 20}, ports.FileOptions{}); err != nil {
		t.Fatalf("Patch: %v", err)
	}

	got, _ := f.Read(nil, ports.FileOptions{})
	if got.Name != "yaml-item" || got.Value != 20 {
		t.Errorf("unexpected result: %+v", got)
	}
}

func TestPatch_TOML_HappyPath(t *testing.T) {
	type cfg struct {
		Port int    `toml:"port"`
		Host string `toml:"host"`
	}
	cfgCodec := codex.Struct[cfg](
		codex.RequiredField("port", codex.Int(), func(c cfg) int { return c.Port }, func(c *cfg, v int) { c.Port = v }),
		codex.RequiredField("host", codex.String(), func(c cfg) string { return c.Host }, func(c *cfg, v string) { c.Host = v }),
	)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	f := ports.NewFile(path, format.TOML(cfgCodec))

	if _, err := f.Write(nil, cfg{Port: 8080, Host: "localhost"}, ports.FileOptions{}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Patch only port; host must survive
	if err := f.Patch(nil, map[string]any{"port": int64(9090)}, ports.FileOptions{}); err != nil {
		t.Fatalf("Patch: %v", err)
	}

	got, _ := f.Read(nil, ports.FileOptions{})
	if got.Host != "localhost" {
		t.Errorf("Host changed: want localhost, got %q", got.Host)
	}
	if got.Port != 9090 {
		t.Errorf("Port not patched: want 9090, got %d", got.Port)
	}
}

// ── Patch — Gob and Binary return FilePatchNotSupportedError (no I/O) ─────────

func TestPatch_Gob_ReturnsPatchNotSupportedError_NoIO(t *testing.T) {
	// Use a non-existent path — error must come before I/O
	f := ports.NewFile("/nonexistent/item.gob", format.Gob(fileItemCodec))
	err := f.Patch(nil, map[string]any{"name": "x"}, ports.FileOptions{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var patchErr ports.FilePatchNotSupportedError
	if !errors.As(err, &patchErr) {
		t.Fatalf("expected FilePatchNotSupportedError, got %T: %v", err, err)
	}
}

func TestPatch_Binary_ReturnsPatchNotSupportedError_NoIO(t *testing.T) {
	f := ports.NewFile("/nonexistent/img.png", format.Binary(codex.Bytes()))
	err := f.Patch(nil, map[string]any{}, ports.FileOptions{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var patchErr ports.FilePatchNotSupportedError
	if !errors.As(err, &patchErr) {
		t.Fatalf("expected FilePatchNotSupportedError, got %T: %v", err, err)
	}
}

// ── Patch — constraint violation ──────────────────────────────────────────────

func TestPatch_ConstraintViolation_ReturnsFileDecodeError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "item.json")
	f := ports.NewFile(path, format.JSON(fileItemCodec))

	if _, err := f.Write(nil, fileItem{Name: "widget", Value: 10}, ports.FileOptions{}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// value 9999 violates RangeInt(0, 1000)
	err := f.Patch(nil, map[string]any{"value": 9999}, ports.FileOptions{})
	if err == nil {
		t.Fatal("expected error for out-of-range value, got nil")
	}
	var decErr ports.FileDecodeError
	if !errors.As(err, &decErr) {
		t.Fatalf("expected FileDecodeError, got %T: %v", err, err)
	}
}

// ── Patch — FileReadError when file absent ────────────────────────────────────

func TestPatch_FileNotFound_ReturnsFileReadError(t *testing.T) {
	f := ports.NewFile("/nonexistent/item.json", format.JSON(fileItemCodec))
	err := f.Patch(nil, map[string]any{"name": "x"}, ports.FileOptions{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var readErr ports.FileReadError
	if !errors.As(err, &readErr) {
		t.Fatalf("expected FileReadError, got %T: %v", err, err)
	}
}

// ── Patch — Observer ──────────────────────────────────────────────────────────

func TestPatch_Observer_ReadAndWriteBothCalled(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "item.json")
	f := ports.NewFile(path, format.JSON(fileItemCodec))
	spy := &fileObserverSpy{}

	if _, err := f.Write(nil, fileItem{Name: "x", Value: 1}, ports.FileOptions{}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	spy.reads = nil
	spy.writes = nil

	if err := f.Patch(nil, map[string]any{"value": 2}, ports.FileOptions{Observer: spy}); err != nil {
		t.Fatalf("Patch: %v", err)
	}
	if len(spy.reads) != 1 || !spy.reads[0].success {
		t.Errorf("expected 1 successful read callback, got %+v", spy.reads)
	}
	if len(spy.writes) != 1 || !spy.writes[0].success {
		t.Errorf("expected 1 successful write callback, got %+v", spy.writes)
	}
}

func TestPatch_Observer_ReadFailure_CallsRecordFileReadFalse(t *testing.T) {
	f := ports.NewFile("/nonexistent/item.json", format.JSON(fileItemCodec))
	spy := &fileObserverSpy{}
	_ = f.Patch(nil, map[string]any{"name": "x"}, ports.FileOptions{Observer: spy})
	if len(spy.reads) != 1 || spy.reads[0].success {
		t.Errorf("expected 1 failed read callback, got %+v", spy.reads)
	}
	if len(spy.writes) != 0 {
		t.Errorf("expected no write callbacks on read failure, got %d", len(spy.writes))
	}
}

// ── Patch — PathParam errors propagated ──────────────────────────────────────

func TestPatch_PathVarError_NoIO(t *testing.T) {
	spy := &fileObserverSpy{}
	err := templateFile.Patch(
		map[string]string{"category": "", "id": "1"},
		map[string]any{"name": "x"},
		ports.FileOptions{Observer: spy},
	)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var paramErr ports.FilePathParamError
	if !errors.As(err, &paramErr) {
		t.Fatalf("expected FilePathParamError, got %T: %v", err, err)
	}
	if len(spy.reads) != 0 {
		t.Errorf("expected no observer reads, got %d", len(spy.reads))
	}
}

// ── ports.PatchEncoded (free function) ──────────────────────────────────────
//
// PatchEncoded is a free function because Go methods cannot introduce new type
// parameters. The patch type P is separate from the file type T.

func TestPatchEncoded_HappyPath_OnlyPatchCodecFieldsChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "item.json")
	f := ports.NewFile(path, format.JSON(fileItemCodec))

	if _, err := f.Write(nil, fileItem{Name: "original", Value: 10}, ports.FileOptions{}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// fileItemPatchCodec only encodes "value" — "name" is NOT in the patch type.
	// Name must survive unchanged.
	if err := ports.PatchEncoded(f, nil, fileItemPatchCodec,
		fileItemPatch{Value: 99}, ports.FileOptions{}); err != nil {
		t.Fatalf("PatchEncoded: %v", err)
	}

	got, err := f.Read(nil, ports.FileOptions{})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.Name != "original" {
		t.Errorf("Name changed: want %q, got %q", "original", got.Name)
	}
	if got.Value != 99 {
		t.Errorf("Value not patched: want 99, got %d", got.Value)
	}
}

func TestPatchEncoded_FieldNotInPatchCodec_PreservedInFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "item.json")
	f := ports.NewFile(path, format.JSON(fileItemCodec))

	if _, err := f.Write(nil, fileItem{Name: "widget", Value: 42}, ports.FileOptions{}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Patch with Value=0 via patch codec — 0 is valid (RangeInt(0,1000) passes).
	// "name" is NOT in fileItemPatchCodec, so it is always preserved.
	if err := ports.PatchEncoded(f, nil, fileItemPatchCodec,
		fileItemPatch{Value: 0}, ports.FileOptions{}); err != nil {
		t.Fatalf("PatchEncoded: %v", err)
	}

	got, _ := f.Read(nil, ports.FileOptions{})
	if got.Name != "widget" {
		t.Errorf("Name changed: want %q, got %q", "widget", got.Name)
	}
	if got.Value != 0 {
		t.Errorf("Value not patched to 0: got %d", got.Value)
	}
}

func TestPatchEncoded_EncodeFailure_ReturnsFileEncodeError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "item.json")
	f := ports.NewFile(path, format.JSON(fileItemCodec))

	if _, err := f.Write(nil, fileItem{Name: "x", Value: 1}, ports.FileOptions{}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Value 9999 violates RangeInt(0, 1000) in the patch codec — encode fails before I/O.
	err := ports.PatchEncoded(f, nil, fileItemPatchCodec,
		fileItemPatch{Value: 9999}, ports.FileOptions{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var encErr ports.FileEncodeError
	if !errors.As(err, &encErr) {
		t.Fatalf("expected FileEncodeError, got %T: %v", err, err)
	}
}

func TestPatchEncoded_NonMapIntermediate_ReturnsPatchNotSupportedError(t *testing.T) {
	// Use a scalar patch codec — Encode returns int, not map[string]any.
	dir := t.TempDir()
	path := filepath.Join(dir, "item.json")
	f := ports.NewFile(path, format.JSON(fileItemCodec))

	if _, err := f.Write(nil, fileItem{Name: "x", Value: 1}, ports.FileOptions{}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Scalar codec — intermediate is int, not map[string]any.
	err := ports.PatchEncoded(f, nil, codex.Int(), 99, ports.FileOptions{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var patchErr ports.FilePatchNotSupportedError
	if !errors.As(err, &patchErr) {
		t.Fatalf("expected FilePatchNotSupportedError, got %T: %v", err, err)
	}
}

func TestPatchEncoded_Observer_ReadAndWriteBothCalled(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "item.json")
	f := ports.NewFile(path, format.JSON(fileItemCodec))
	spy := &fileObserverSpy{}

	if _, err := f.Write(nil, fileItem{Name: "x", Value: 1}, ports.FileOptions{}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	spy.reads = nil
	spy.writes = nil

	if err := ports.PatchEncoded(f, nil, fileItemPatchCodec,
		fileItemPatch{Value: 5}, ports.FileOptions{Observer: spy}); err != nil {
		t.Fatalf("PatchEncoded: %v", err)
	}
	// PatchEncoded → Patch → RecordFileRead + RecordFileWrite both called
	if len(spy.reads) != 1 || !spy.reads[0].success {
		t.Errorf("expected 1 successful read, got %+v", spy.reads)
	}
	if len(spy.writes) != 1 || !spy.writes[0].success {
		t.Errorf("expected 1 successful write, got %+v", spy.writes)
	}
}

func TestPatchEncoded_GobFile_ReturnsPatchNotSupportedError(t *testing.T) {
	f := ports.NewFile("/tmp/x.gob", format.Gob(fileItemCodec))
	err := ports.PatchEncoded(f, nil, fileItemPatchCodec,
		fileItemPatch{Value: 1}, ports.FileOptions{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var patchErr ports.FilePatchNotSupportedError
	if !errors.As(err, &patchErr) {
		t.Fatalf("expected FilePatchNotSupportedError, got %T: %v", err, err)
	}
}

// ── Field survival: patchCodec fields not in file codec ARE written ───────────

// fileItemWithExtra is used to define a patch codec that has an extra field
// "tag" not present in fileItemCodec. This verifies that PatchEncoded writes
// fields declared in the patch codec even when the file codec doesn't know them.
type fileItemWithTag struct {
	Value int    `json:"value"`
	Tag   string `json:"tag"` // not in fileItemCodec
}

var fileItemWithTagCodec = codex.Struct[fileItemWithTag](
	codex.RequiredField("value",
		codex.Int().Refine(validate.RangeInt(0, 1000)),
		func(p fileItemWithTag) int { return p.Value },
		func(p *fileItemWithTag, i int) { p.Value = i },
	),
	codex.RequiredField("tag",
		codex.String().Refine(validate.NonEmptyString),
		func(p fileItemWithTag) string { return p.Tag },
		func(p *fileItemWithTag, s string) { p.Tag = s },
	),
)

func TestPatchEncoded_PatchCodecFieldNotInFileCodec_WrittenToFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "item.json")
	f := ports.NewFile(path, format.JSON(fileItemCodec))

	if _, err := f.Write(nil, fileItem{Name: "widget", Value: 10}, ports.FileOptions{}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// fileItemWithTagCodec has "tag" which is NOT in fileItemCodec.
	// After PatchEncoded, "tag" must appear in the JSON file.
	if err := ports.PatchEncoded(f, nil, fileItemWithTagCodec,
		fileItemWithTag{Value: 20, Tag: "featured"},
		ports.FileOptions{}); err != nil {
		t.Fatalf("PatchEncoded: %v", err)
	}

	// Read raw bytes — "tag" should be present even though fileItemCodec ignores it.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(raw), `"tag"`) || !strings.Contains(string(raw), `"featured"`) {
		t.Errorf("expected tag field in file, got: %s", raw)
	}

	// File codec fields are also correctly updated.
	got, err := f.Read(nil, ports.FileOptions{})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.Value != 20 {
		t.Errorf("Value not updated: want 20, got %d", got.Value)
	}
	if got.Name != "widget" {
		t.Errorf("Name changed: want %q, got %q", "widget", got.Name)
	}
}

// ── Field survival: Patch drops unknown fields ────────────────────────────────

func TestPatch_UnknownFieldInExistingFile_Dropped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "item.json")

	// Seed file with an extra field not in fileItemCodec.
	if err := os.WriteFile(path, []byte(`{"name":"widget","value":10,"unknown":"drop-me"}`), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	f := ports.NewFile(path, format.JSON(fileItemCodec))
	if err := f.Patch(nil, map[string]any{"value": 99}, ports.FileOptions{}); err != nil {
		t.Fatalf("Patch: %v", err)
	}

	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "unknown") || strings.Contains(string(raw), "drop-me") {
		t.Errorf("unknown field survived Patch — should be dropped: %s", raw)
	}
	if !strings.Contains(string(raw), `"value":99`) && !strings.Contains(string(raw), `"value": 99`) {
		t.Errorf("value not patched: %s", raw)
	}
}

func TestPatch_UnknownFieldInPatchMap_Dropped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "item.json")
	f := ports.NewFile(path, format.JSON(fileItemCodec))

	if _, err := f.Write(nil, fileItem{Name: "x", Value: 1}, ports.FileOptions{}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Patch with a key not in fileItemCodec — should be silently dropped.
	if err := f.Patch(nil, map[string]any{"value": 5, "ghost_field": "nope"}, ports.FileOptions{}); err != nil {
		t.Fatalf("Patch: %v", err)
	}

	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "ghost_field") {
		t.Errorf("unknown patch field written — should be dropped: %s", raw)
	}
	got, _ := f.Read(nil, ports.FileOptions{})
	if got.Value != 5 {
		t.Errorf("value not patched: want 5, got %d", got.Value)
	}
}

// ── Context propagation for TraceObserver ──────────────────────────────────

type fileTraceSpy struct {
	stats.NoopObserver
	mu  sync.Mutex
	ops []string
}

func (s *fileTraceSpy) StartSpan(ctx context.Context, operation, name string) context.Context {
	s.mu.Lock()
	s.ops = append(s.ops, operation)
	s.mu.Unlock()
	return ctx
}

func (s *fileTraceSpy) EndSpan(_ context.Context, _ error) {}

type parentKey struct{}

func TestFile_TraceObserver_ContextPropagation(t *testing.T) {
	spy := &fileTraceSpy{}
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	f := ports.NewFile(path, format.JSON(codex.String()))

	parentCtx := context.Background()
	parentCtx = context.WithValue(parentCtx, parentKey{}, "value")
	opts := ports.FileOptions{Observer: spy, Context: parentCtx}
	f.Read(nil, opts)
	// file doesn't exist — TraceObserver guard fires before the read error

	spy.mu.Lock()
	defer spy.mu.Unlock()
	if len(spy.ops) == 0 {
		t.Error("want at least 1 StartSpan call from Read")
	}
	if spy.ops[0] != "file.read" {
		t.Errorf("want file.read, got %s", spy.ops[0])
	}
}

func TestFile_TraceObserver_ContextNilFallback(t *testing.T) {
	spy := &fileTraceSpy{}
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	f := ports.NewFile(path, format.JSON(codex.String()))

	// No Context in FileOptions — should use context.Background()
	f.Read(nil, ports.FileOptions{Observer: spy})
	// file doesn't exist — still fires TraceObserver

	spy.mu.Lock()
	defer spy.mu.Unlock()
	if len(spy.ops) == 0 {
		t.Error("want StartSpan call even with nil Context")
	}
}

func ExampleNewFile() {
	// Declare a typed file descriptor once — no I/O at declaration time.
	// The format (JSON here) and path template are captured in the value.
	type Config struct {
		Host string
		Port int
	}

	cfgCodec := codex.Struct[Config](
		codex.RequiredField("host", codex.String(),
			func(c Config) string { return c.Host },
			func(c *Config, v string) { c.Host = v },
		),
		codex.RequiredField("port", codex.Int(),
			func(c Config) int { return c.Port },
			func(c *Config, v int) { c.Port = v },
		),
	)

	// Static path — pass nil for vars on every call.
	path := os.TempDir() + "/example-config.json"
	cfgFile := ports.NewFile(path, format.JSON(cfgCodec))

	// Write — encodes + validates before writing.
	_, _ = cfgFile.Write(nil, Config{Host: "localhost", Port: 8080}, ports.FileOptions{})

	// Read — reads + decodes + validates constraints.
	cfg, _ := cfgFile.Read(nil, ports.FileOptions{})
	fmt.Printf("host=%s port=%d\n", cfg.Host, cfg.Port)
	// Output:
	// host=localhost port=8080
}

// ── T8: context observer ──────────────────────────────────────────────────────

func TestFileRead_ContextObserver_UsedWhenOptsNil(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.json")
	type item struct{ V int }
	codec := codex.Struct[item](
		codex.RequiredField("v", codex.Int(), func(x item) int { return x.V }, func(x *item, v int) { x.V = v }),
	)
	f := ports.NewFile(path, format.JSON(codec))
	if _, err := f.Write(nil, item{V: 42}, ports.FileOptions{}); err != nil {
		t.Fatalf("write: %v", err)
	}

	var fileCalled bool
	spy := &fileObsSpy{onFileRead: func() { fileCalled = true }}
	ctx := stats.WithObserver(context.Background(), spy)

	_, err := f.Read(nil, ports.FileOptions{Context: ctx})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !fileCalled {
		t.Error("want FileObserver.RecordFileRead called from context observer, got nothing")
	}
}

func TestFileRead_ExplicitObserver_BeatsContext(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.json")
	type item struct{ V int }
	codec := codex.Struct[item](
		codex.RequiredField("v", codex.Int(), func(x item) int { return x.V }, func(x *item, v int) { x.V = v }),
	)
	f := ports.NewFile(path, format.JSON(codec))
	_, _ = f.Write(nil, item{V: 1}, ports.FileOptions{})

	var explicitCalled, contextCalled bool
	explicit := &fileObsSpy{onFileRead: func() { explicitCalled = true }}
	ctxSpy := &fileObsSpy{onFileRead: func() { contextCalled = true }}
	ctx := stats.WithObserver(context.Background(), ctxSpy)

	_, err := f.Read(nil, ports.FileOptions{Context: ctx, Observer: explicit})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !explicitCalled {
		t.Error("want explicit observer called")
	}
	if contextCalled {
		t.Error("want context observer NOT called when explicit is set")
	}
}

type fileObsSpy struct {
	stats.NoopObserver
	onFileRead func()
}

func (s *fileObsSpy) RecordFileRead(_ string, _ bool, _ time.Duration) {
	if s.onFileRead != nil {
		s.onFileRead()
	}
}

// ── Binary + File integration (Observer) ─────────────────────────────────────

func TestBinary_FileObserver_WriteSuccess(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/out.bin"
	spy := &fileObserverSpy{}

	f := ports.NewFile(path, format.Binary(codex.Bytes()))
	_, err := f.Write(nil, []byte{0x01, 0x02, 0x03}, ports.FileOptions{Observer: spy})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(spy.writes) != 1 || !spy.writes[0].success {
		t.Errorf("expected 1 successful write, got %+v", spy.writes)
	}
}

func TestBinary_FileObserver_WriteConstraintFail(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/out.bin"
	spy := &fileObserverSpy{}

	prefix := []byte{0xAA, 0xBB}
	f := ports.NewFile(path, format.Binary(codex.Bytes().Refine(validate.HasPrefix(prefix))))
	_, err := f.Write(nil, []byte{0x00, 0x01}, ports.FileOptions{Observer: spy})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var encErr ports.FileEncodeError
	if !errors.As(err, &encErr) {
		t.Fatalf("expected FileEncodeError, got %T: %v", err, err)
	}
	if len(spy.writes) != 1 || spy.writes[0].success {
		t.Errorf("expected 1 failed write callback, got %+v", spy.writes)
	}
}

func TestBinary_FileObserver_ReadSuccess(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/data.bin"
	data := []byte{0x10, 0x20, 0x30}
	if err := writeRawFile(path, data); err != nil {
		t.Fatal(err)
	}
	spy := &fileObserverSpy{}

	f := ports.NewFile(path, format.Binary(codex.Bytes()))
	got, err := f.Read(nil, ports.FileOptions{Observer: spy})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Errorf("read mismatch: want %v, got %v", data, got)
	}
	if len(spy.reads) != 1 || !spy.reads[0].success {
		t.Errorf("expected 1 successful read, got %+v", spy.reads)
	}
}

func TestBinary_FileObserver_ReadConstraintFail(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/data.bin"
	if err := writeRawFile(path, []byte{0x00, 0x01, 0x02}); err != nil {
		t.Fatal(err)
	}
	spy := &fileObserverSpy{}

	prefix := []byte{0xAA, 0xBB}
	f := ports.NewFile(path, format.Binary(codex.Bytes().Refine(validate.HasPrefix(prefix))))
	_, err := f.Read(nil, ports.FileOptions{Observer: spy})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var decErr ports.FileDecodeError
	if !errors.As(err, &decErr) {
		t.Fatalf("expected FileDecodeError, got %T: %v", err, err)
	}
	if len(spy.reads) != 1 || spy.reads[0].success {
		t.Errorf("expected 1 failed read callback, got %+v", spy.reads)
	}
}

// writeRawFile writes raw bytes to path.
func writeRawFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0644)
}

// ── MatchPath ─────────────────────────────────────────────────────────────────

var matchPathFile = ports.NewFile("readings/{sensorID}/{date}.json", format.JSON(fileItemCodec),
	ports.FilePathParam{Name: "sensorID"}.WithCodec(codex.String().Refine(validate.NonEmptyString)),
	ports.FilePathParam{Name: "date"}.WithCodec(codex.String().Refine(validate.Date)),
)

func TestFile_MatchPath_HappyPath(t *testing.T) {
	vars, err := matchPathFile.MatchPath("readings/sensor-42/2024-01-15.json")
	if err != nil {
		t.Fatalf("MatchPath: %v", err)
	}
	if vars["sensorID"] != "sensor-42" {
		t.Errorf("vars[sensorID]: want %q, got %q", "sensor-42", vars["sensorID"])
	}
	if vars["date"] != "2024-01-15" {
		t.Errorf("vars[date]: want %q, got %q", "2024-01-15", vars["date"])
	}
}

func TestFile_MatchPath_SegmentCountMismatch(t *testing.T) {
	_, err := matchPathFile.MatchPath("readings/sensor-42/extra/2024-01-15.json")
	if err == nil {
		t.Fatal("expected FilePathMismatchError")
	}
	var me ports.FilePathMismatchError
	if !errors.As(err, &me) {
		t.Fatalf("expected FilePathMismatchError, got %T: %v", err, err)
	}
}

func TestFile_MatchPath_LiteralSegmentMismatch(t *testing.T) {
	_, err := matchPathFile.MatchPath("wrong-prefix/sensor-42/2024-01-15.json")
	if err == nil {
		t.Fatal("expected FilePathMismatchError")
	}
	var me ports.FilePathMismatchError
	if !errors.As(err, &me) {
		t.Fatalf("expected FilePathMismatchError, got %T: %v", err, err)
	}
}

func TestFile_MatchPath_ExtractedVarFailsCodec(t *testing.T) {
	_, err := matchPathFile.MatchPath("readings/sensor-42/not-a-date.json")
	if err == nil {
		t.Fatal("expected FilePathParamError for invalid date")
	}
	var pe ports.FilePathParamError
	if !errors.As(err, &pe) {
		t.Fatalf("expected FilePathParamError, got %T: %v", err, err)
	}
	if pe.Name != "date" {
		t.Errorf("FilePathParamError.Name: want %q, got %q", "date", pe.Name)
	}
}

func TestFile_MatchPath_BuildPath_RoundTrip(t *testing.T) {
	path := "readings/sensor-42/2024-01-15.json"
	vars, err := matchPathFile.MatchPath(path)
	if err != nil {
		t.Fatalf("MatchPath: %v", err)
	}
	rebuilt, err := matchPathFile.BuildPath(vars)
	if err != nil {
		t.Fatalf("BuildPath: %v", err)
	}
	if rebuilt != path {
		t.Errorf("round-trip: want %q, got %q", path, rebuilt)
	}
}

func TestFilePathMismatchError_LogValue(t *testing.T) {
	err := ports.FilePathMismatchError{Template: "a/{x}", Path: "a/b/c"}
	lv := err.LogValue()
	if lv.Kind() != slog.KindGroup {
		t.Fatalf("LogValue: want KindGroup, got %v", lv.Kind())
	}
	attrs := lv.Group()
	keys := make(map[string]bool, len(attrs))
	for _, a := range attrs {
		keys[a.Key] = true
	}
	for _, want := range []string{"template", "path"} {
		if !keys[want] {
			t.Errorf("LogValue missing attribute %q", want)
		}
	}
}

func ExampleFile_MatchPath() {
	f := ports.NewFile("readings/{sensorID}/{date}.json", format.JSON(fileItemCodec),
		ports.FilePathParam{Name: "sensorID"}.WithCodec(codex.String().Refine(validate.NonEmptyString)),
		ports.FilePathParam{Name: "date"}.WithCodec(codex.String().Refine(validate.Date)),
	)
	vars, err := f.MatchPath("readings/sensor-42/2024-01-15.json")
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println(vars["sensorID"])
	fmt.Println(vars["date"])
	// Output:
	// sensor-42
	// 2024-01-15
}

// ── NewFilePathParam / MergeFields ────────────────────────────────────────────

// readingMeta is File's declared type T for these tests: SensorID/Date come
// from the path (merge fields), Value comes from the JSON body — both sides
// share the SAME T, since File[T].MergeFields() returns []codex.FieldCodec[T].
type readingMeta struct {
	SensorID string
	Date     string
	Value    float64
}

// readingValueCodec only declares "value" — SensorID/Date are populated
// exclusively via the path-var merge, never by the body codec.
var readingValueCodec = codex.Struct[readingMeta](
	codex.RequiredField("value", codex.Float64(),
		func(r readingMeta) float64 { return r.Value },
		func(r *readingMeta, v float64) { r.Value = v }),
)

func TestFile_NewFilePathParam_RegistersSpecAndMergeField(t *testing.T) {
	f := ports.NewFile("readings/{sensorID}/{date}.json", format.JSON(readingValueCodec),
		ports.NewFilePathParam("sensorID", codex.String().Refine(validate.NonEmptyString),
			func(r readingMeta) string { return r.SensorID },
			func(r *readingMeta, v string) { r.SensorID = v }),
		ports.NewFilePathParam("date", codex.String().Refine(validate.Date),
			func(r readingMeta) string { return r.Date },
			func(r *readingMeta, v string) { r.Date = v }),
	)

	// Spec/validation path unchanged: BuildPath still works exactly as with
	// plain FilePathParam.
	path, err := f.BuildPath(map[string]string{"sensorID": "sensor-42", "date": "2024-01-15"})
	if err != nil {
		t.Fatalf("BuildPath: %v", err)
	}
	if path != "readings/sensor-42/2024-01-15.json" {
		t.Errorf("BuildPath: got %q", path)
	}

	// Merge fields registered — usable with codex.DecodeVars.
	if len(f.MergeFields()) != 2 {
		t.Fatalf("MergeFields: want 2 fields, got %d", len(f.MergeFields()))
	}

	vars, err := f.MatchPath("readings/sensor-42/2024-01-15.json")
	if err != nil {
		t.Fatalf("MatchPath: %v", err)
	}
	var meta readingMeta
	if err := codex.DecodeVars(&meta, vars, f.MergeFields()...); err != nil {
		t.Fatalf("DecodeVars: %v", err)
	}
	if meta.SensorID != "sensor-42" || meta.Date != "2024-01-15" {
		t.Errorf("unexpected merged meta: %+v", meta)
	}
}

func TestFile_NewFilePathParam_WithDescription(t *testing.T) {
	p := ports.NewFilePathParam("sensorID", codex.String(),
		func(r readingMeta) string { return r.SensorID },
		func(r *readingMeta, v string) { r.SensorID = v },
	).WithDescription("Sensor identifier")
	if p.Description != "Sensor identifier" {
		t.Errorf("Description: want %q, got %q", "Sensor identifier", p.Description)
	}
}

func TestFile_NewFilePathParam_TypeMismatchPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected NewFile to panic on merge-field type mismatch")
		}
	}()
	// fileItem != readingMeta — a deliberate type mismatch between File's T
	// (fileItem, from format.JSON(fileItemCodec)) and the merge field's T
	// (readingMeta, from NewFilePathParam's get/set args).
	_ = ports.NewFile("x/{sensorID}", format.JSON(fileItemCodec),
		ports.NewFilePathParam("sensorID", codex.String(),
			func(r readingMeta) string { return r.SensorID },
			func(r *readingMeta, v string) { r.SensorID = v }),
	)
}

// ── ReadMerged / WriteHandle (one struct, one call for ports.File) ───────────

// G1-1: File.ReadMerged merges path vars into the decoded value when the
// file declares merge-capable path params — regression guard included via
// G1-2 below (identical to Read when none declared).
func TestReadMerged_MergesPathVarsIntoDecodedValue(t *testing.T) {
	dir := t.TempDir()
	f := ports.NewFile(filepath.Join(dir, "readings/{sensorID}/{date}.json"), format.JSON(readingValueCodec),
		ports.NewFilePathParam("sensorID", codex.String().Refine(validate.NonEmptyString),
			func(r readingMeta) string { return r.SensorID },
			func(r *readingMeta, v string) { r.SensorID = v }),
		ports.NewFilePathParam("date", codex.String().Refine(validate.Date),
			func(r readingMeta) string { return r.Date },
			func(r *readingMeta, v string) { r.Date = v }),
	)
	path, err := f.BuildPath(map[string]string{"sensorID": "sensor-42", "date": "2024-01-15"})
	if err != nil {
		t.Fatalf("BuildPath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	// Body JSON deliberately carries ONLY "value" — SensorID/Date must come
	// exclusively from the path-var merge.
	if err := os.WriteFile(path, []byte(`{"value":22.5}`), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	reading, err := f.ReadMerged(map[string]string{"sensorID": "sensor-42", "date": "2024-01-15"}, ports.FileOptions{})
	if err != nil {
		t.Fatalf("ReadMerged: %v", err)
	}
	if reading.SensorID != "sensor-42" || reading.Date != "2024-01-15" {
		t.Errorf("want merged path vars, got %+v", reading)
	}
	if reading.Value != 22.5 {
		t.Errorf("Value: want 22.5, got %v", reading.Value)
	}
}

// G1-1 (regression guard): a file with NO merge-capable path params behaves
// identically to a bare Read.
func TestReadMerged_NoMergeFields_MatchesPlainRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "item.json")
	_ = os.WriteFile(path, []byte(`{"name":"widget","value":42}`), 0644)

	f := ports.NewFile(path, format.JSON(fileItemCodec))
	viaRead, err := f.Read(nil, ports.FileOptions{})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	viaReadMerged, err := f.ReadMerged(nil, ports.FileOptions{})
	if err != nil {
		t.Fatalf("ReadMerged: %v", err)
	}
	if viaRead != viaReadMerged {
		t.Errorf("ReadMerged should match plain Read when no merge fields declared: %+v vs %+v", viaRead, viaReadMerged)
	}
}

// G1-2: WriteHandle derives path vars from v's own merge-field-declared
// struct fields — no manual vars map needed.
func TestWriteHandle_DerivesVarsFromValue(t *testing.T) {
	dir := t.TempDir()
	f := ports.NewFile(filepath.Join(dir, "readings/{sensorID}/{date}.json"), format.JSON(readingValueCodec),
		ports.NewFilePathParam("sensorID", codex.String().Refine(validate.NonEmptyString),
			func(r readingMeta) string { return r.SensorID },
			func(r *readingMeta, v string) { r.SensorID = v }),
		ports.NewFilePathParam("date", codex.String().Refine(validate.Date),
			func(r readingMeta) string { return r.Date },
			func(r *readingMeta, v string) { r.Date = v }),
	)

	if err := os.MkdirAll(filepath.Join(dir, "readings/sensor-99"), 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	reading := readingMeta{SensorID: "sensor-99", Date: "2024-02-01", Value: 3.5}
	if _, err := ports.WriteHandle(f, reading, ports.FileOptions{}); err != nil {
		t.Fatalf("WriteHandle: %v", err)
	}

	wantPath := filepath.Join(dir, "readings/sensor-99/2024-02-01.json")
	if _, err := os.Stat(wantPath); err != nil {
		t.Fatalf("want file at derived path %q, got: %v", wantPath, err)
	}
}

// G1-2 (regression guard): WriteHandle with no merge fields declared behaves
// identically to a bare Write(nil, v, opts) call.
func TestWriteHandle_NoMergeFields_MatchesPlainWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "item.json")
	f := ports.NewFile(path, format.JSON(fileItemCodec))

	item := fileItem{Name: "widget", Value: 42}
	if _, err := ports.WriteHandle(f, item, ports.FileOptions{}); err != nil {
		t.Fatalf("WriteHandle: %v", err)
	}
	got, err := f.Read(nil, ports.FileOptions{})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got != item {
		t.Errorf("want %+v, got %+v", item, got)
	}
}

func TestWriteHandle_PropagatesCreatedDirs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a", "b", "item.json")
	f := ports.NewFile(path, format.JSON(fileItemCodec))

	created, err := ports.WriteHandle(f, fileItem{Name: "x", Value: 1}, ports.FileOptions{CreateDirs: true})
	if err != nil {
		t.Fatalf("WriteHandle: %v", err)
	}
	if len(created) != 2 {
		t.Errorf("createdDirs = %v, want 2 entries", created)
	}
}

// ── Glob path template segments ──────────────────────────────────────────────

func TestFile_MatchPath_GlobTemplate_MatchesExternallyDiscoveredPath(t *testing.T) {
	f := ports.NewFile("logs/app-*/**/errors/*.log", format.JSON(fileItemCodec))
	vars, err := f.MatchPath("logs/app-1/a/b/errors/x.log")
	if err != nil {
		t.Fatalf("MatchPath: %v", err)
	}
	if len(vars) != 0 {
		t.Errorf("vars = %v, want empty (glob segments never capture)", vars)
	}

	if _, err := f.MatchPath("logs/app-1/errors/wrong-suffix.txt"); err == nil {
		t.Fatal("MatchPath: want mismatch error for wrong file extension")
	}
}

func TestFile_MatchPath_GlobTemplate_NamedVarStillCaptured(t *testing.T) {
	f := ports.NewFile("readings/{sensorID}/*.json", format.JSON(fileItemCodec),
		ports.FilePathParam{Name: "sensorID"},
	)
	vars, err := f.MatchPath("readings/sensor-42/2024-01-15.json")
	if err != nil {
		t.Fatalf("MatchPath: %v", err)
	}
	if vars["sensorID"] != "sensor-42" {
		t.Errorf("vars[sensorID] = %q, want %q", vars["sensorID"], "sensor-42")
	}
}

func TestFile_BuildPath_GlobTemplate_ReturnsFileWildcardBuildError(t *testing.T) {
	f := ports.NewFile("logs/app-*/errors/*.log", format.JSON(fileItemCodec))
	_, err := f.BuildPath(nil)
	var wantErr ports.FileWildcardBuildError
	if !errors.As(err, &wantErr) {
		t.Fatalf("BuildPath err = %v, want FileWildcardBuildError", err)
	}
}

func TestNewFile_Glob_PanicsOnMultipleGlobstar(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("NewFile: want panic on multiple ** segments")
		}
	}()
	ports.NewFile("a/**/b/**/c.json", format.JSON(fileItemCodec))
}
