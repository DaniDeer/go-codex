package ports_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/ports"
	"github.com/DaniDeer/go-codex/stats"
	"github.com/DaniDeer/go-codex/validate"
)

// dirObserverSpy captures FileObserver.RecordFileRead callbacks (Dir.List
// reuses the same observer extension File.Read does).
type dirObserverSpy struct {
	stats.NoopObserver
	reads []fileObsCall
}

func (o *dirObserverSpy) RecordFileRead(path string, success bool, d time.Duration) {
	o.reads = append(o.reads, fileObsCall{path, success, d})
}

var _ stats.FileObserver = (*dirObserverSpy)(nil)

func nonEmptyStringCodec() codex.Codec[string] {
	return codex.String().Refine(validate.NonEmptyString)
}

// ── BuildPath / MatchPath ─────────────────────────────────────────────────────

func TestDir_BuildPath_ValidatesVars(t *testing.T) {
	c := nonEmptyStringCodec()
	d := ports.NewDir("configs/{env}", ports.DirPathParam{Name: "env"}.WithCodec(c))

	path, err := d.BuildPath(map[string]string{"env": "prod"})
	if err != nil {
		t.Fatalf("BuildPath: %v", err)
	}
	if path != "configs/prod" {
		t.Errorf("BuildPath = %q, want %q", path, "configs/prod")
	}

	if _, err := d.BuildPath(map[string]string{"env": ""}); err == nil {
		t.Fatal("BuildPath: want error for empty env (fails NonEmptyString), got nil")
	} else {
		var paramErr ports.DirPathParamError
		if !errors.As(err, &paramErr) {
			t.Fatalf("BuildPath error = %T, want ports.DirPathParamError", err)
		}
	}

	if _, err := d.BuildPath(nil); err == nil {
		t.Fatal("BuildPath: want error for missing env var, got nil")
	} else {
		var missingErr ports.MissingDirPathVarError
		if !errors.As(err, &missingErr) {
			t.Fatalf("BuildPath error = %T, want ports.MissingDirPathVarError", err)
		}
	}
}

func TestDir_MatchPath_ValidatesAgainstTemplate(t *testing.T) {
	c := nonEmptyStringCodec()
	d := ports.NewDir("configs/{env}", ports.DirPathParam{Name: "env"}.WithCodec(c))

	vars, err := d.MatchPath("configs/prod")
	if err != nil {
		t.Fatalf("MatchPath: %v", err)
	}
	if vars["env"] != "prod" {
		t.Errorf("MatchPath vars = %v, want env=prod", vars)
	}
}

func TestDir_MatchPath_ReturnsMismatchError(t *testing.T) {
	d := ports.NewDir("configs/{env}")
	_, err := d.MatchPath("other/path/structure")
	if err == nil {
		t.Fatal("MatchPath: want error for structurally mismatched path, got nil")
	}
	var mismatchErr ports.DirPathMismatchError
	if !errors.As(err, &mismatchErr) {
		t.Fatalf("MatchPath error = %T, want ports.DirPathMismatchError", err)
	}
}

// ── List ──────────────────────────────────────────────────────────────────────

func TestDir_List_DefaultPath_CurrentDirectory(t *testing.T) {
	d := ports.NewDir(".")
	entries, err := d.List(nil, ports.DirOptions{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	// The current test working directory always has at least one .go file.
	found := false
	for _, e := range entries {
		if e.Kind == ports.EntryFile {
			found = true
			break
		}
	}
	if !found {
		t.Error("List(\".\") returned no file entries, want at least one")
	}
}

func TestDir_List_ReturnsFilesAndDirs(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "a.json"), "{}")
	if err := os.Mkdir(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}

	d := ports.NewDir(root)
	entries, err := d.List(nil, ports.DirOptions{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var gotFile, gotDir bool
	for _, e := range entries {
		switch e.Name {
		case "a.json":
			gotFile = e.Kind == ports.EntryFile
		case "sub":
			gotDir = e.Kind == ports.EntryDir
		}
	}
	if !gotFile {
		t.Error("expected a.json entry with Kind == EntryFile")
	}
	if !gotDir {
		t.Error("expected sub entry with Kind == EntryDir")
	}
}

func TestDir_List_NoEntryPattern_ReturnsPlainNames(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "a.json"), "{}")
	mustWriteFile(t, filepath.Join(root, ".gitkeep"), "")

	d := ports.NewDir(root)
	entries, err := d.List(nil, ports.DirOptions{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("List returned %d entries, want 2 (no filtering without EntryPattern)", len(entries))
	}
	for _, e := range entries {
		if e.Vars != nil {
			t.Errorf("entry %q Vars = %v, want nil (no EntryPattern set)", e.Name, e.Vars)
		}
	}
}

func TestDir_List_EntryPattern_ExtractsVars(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "temp-sensor.json"), "{}")

	c := nonEmptyStringCodec()
	d := ports.NewDir(root, ports.WithEntryPattern(ports.EntryPattern{
		Template: "{useCase}.json",
		Params:   []ports.EntryParam{{Name: "useCase", Codec: &c}},
	}))

	entries, err := d.List(nil, ports.DirOptions{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("List returned %d entries, want 1", len(entries))
	}
	if entries[0].Vars["useCase"] != "temp-sensor" {
		t.Errorf("Vars[useCase] = %q, want %q", entries[0].Vars["useCase"], "temp-sensor")
	}
}

func TestDir_List_EntryPattern_ExcludesNonMatchingEntries(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "temp-sensor.json"), "{}")
	mustWriteFile(t, filepath.Join(root, ".gitkeep"), "")
	mustWriteFile(t, filepath.Join(root, "README.md"), "")

	d := ports.NewDir(root, ports.WithEntryPattern(ports.EntryPattern{
		Template: "{useCase}.json",
	}))

	entries, err := d.List(nil, ports.DirOptions{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("List returned %d entries, want 1 (non-matching entries silently excluded): %+v", len(entries), entries)
	}
	if entries[0].Name != "temp-sensor.json" {
		t.Errorf("entry Name = %q, want %q", entries[0].Name, "temp-sensor.json")
	}
}

func TestDir_List_EntryPattern_PropagatesParamCodecError(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "ab.json"), "{}") // "useCase" == "ab", structurally matches but fails MinLen(3)

	c := codex.String().Refine(validate.MinLen(3))
	d := ports.NewDir(root, ports.WithEntryPattern(ports.EntryPattern{
		Template: "{useCase}.json",
		Params:   []ports.EntryParam{{Name: "useCase", Codec: &c}},
	}))

	_, err := d.List(nil, ports.DirOptions{})
	if err == nil {
		t.Fatal("List: want error for useCase=\"ab\" failing MinLen(3), got nil")
	}
	var paramErr ports.DirEntryParamError
	if !errors.As(err, &paramErr) {
		t.Fatalf("List error = %T, want ports.DirEntryParamError", err)
	}
	if paramErr.Name != "useCase" {
		t.Errorf("DirEntryParamError.Name = %q, want %q", paramErr.Name, "useCase")
	}
}

func TestDir_List_Recursive_DescendsSubdirectories(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "top.json"), "{}")
	if err := os.MkdirAll(filepath.Join(root, "nested"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	mustWriteFile(t, filepath.Join(root, "nested", "deep.json"), "{}")

	d := ports.NewDir(root, ports.WithRecursive(true))
	entries, err := d.List(nil, ports.DirOptions{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	var gotTop, gotNestedDir, gotDeep bool
	for _, e := range entries {
		switch e.RelPath {
		case "top.json":
			gotTop = true
		case "nested":
			gotNestedDir = e.Kind == ports.EntryDir
		case filepath.ToSlash(filepath.Join("nested", "deep.json")):
			gotDeep = true
		}
	}
	if !gotTop || !gotNestedDir || !gotDeep {
		t.Errorf("recursive List missing expected entries: top=%v nestedDir=%v deep=%v (entries=%+v)", gotTop, gotNestedDir, gotDeep, entries)
	}
}

func TestDir_List_Recursive_EntryPattern_MatchesFullRelPath(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "prod"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	mustWriteFile(t, filepath.Join(root, "prod", "temp-sensor.json"), "{}")
	mustWriteFile(t, filepath.Join(root, "orphan.json"), "{}") // no env segment — should be excluded

	c := nonEmptyStringCodec()
	d := ports.NewDir(root, ports.WithRecursive(true), ports.WithEntryPattern(ports.EntryPattern{
		Template: "{env}/{useCase}.json",
		Params:   []ports.EntryParam{{Name: "env", Codec: &c}, {Name: "useCase", Codec: &c}},
	}))

	entries, err := d.List(nil, ports.DirOptions{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("List returned %d entries, want 1 (only the full-RelPath match): %+v", len(entries), entries)
	}
	if entries[0].Vars["env"] != "prod" || entries[0].Vars["useCase"] != "temp-sensor" {
		t.Errorf("Vars = %v, want env=prod useCase=temp-sensor", entries[0].Vars)
	}
}

func TestDir_List_NonRecursive_StaysSingleLevel(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "nested"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	mustWriteFile(t, filepath.Join(root, "nested", "deep.json"), "{}")

	d := ports.NewDir(root) // default: no WithRecursive
	entries, err := d.List(nil, ports.DirOptions{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, e := range entries {
		if e.Name == "deep.json" {
			t.Error("non-recursive List must not descend into subdirectories")
		}
	}
}

func TestDir_List_PropagatesReadError(t *testing.T) {
	d := ports.NewDir(filepath.Join(t.TempDir(), "does-not-exist"))
	_, err := d.List(nil, ports.DirOptions{})
	if err == nil {
		t.Fatal("List: want error for non-existent directory, got nil")
	}
	var readErr ports.DirReadError
	if !errors.As(err, &readErr) {
		t.Fatalf("List error = %T, want ports.DirReadError", err)
	}
}

func TestDir_List_CreateIfMissing_CreatesEmptyDir(t *testing.T) {
	root := filepath.Join(t.TempDir(), "a", "b", "c")
	d := ports.NewDir(root)

	entries, err := d.List(nil, ports.DirOptions{CreateIfMissing: true})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("List on freshly-created dir = %d entries, want 0", len(entries))
	}
	if info, statErr := os.Stat(root); statErr != nil || !info.IsDir() {
		t.Errorf("expected directory %q to have been created", root)
	}
}

func TestDir_List_CreateIfMissingFalse_DefaultUnchanged(t *testing.T) {
	// CreateIfMissing defaults to false — behavior must be identical to
	// before this option existed: DirReadError for a missing directory.
	// (Same assertion as TestDir_List_PropagatesReadError, restated here
	// to document the default explicitly alongside the new option.)
	d := ports.NewDir(filepath.Join(t.TempDir(), "does-not-exist"))
	_, err := d.List(nil, ports.DirOptions{})
	if err == nil {
		t.Fatal("List: want error for missing directory when CreateIfMissing is false, got nil")
	}
	var readErr ports.DirReadError
	if !errors.As(err, &readErr) {
		t.Fatalf("List error = %T, want ports.DirReadError", err)
	}
}

func TestDir_List_CreateIfMissing_MkdirFailure_ReturnsDirReadError(t *testing.T) {
	root := t.TempDir()
	// Create a FILE where a directory is expected — MkdirAll must fail.
	blocker := filepath.Join(root, "blocker")
	mustWriteFile(t, blocker, "x")

	d := ports.NewDir(filepath.Join(blocker, "sub"))
	_, err := d.List(nil, ports.DirOptions{CreateIfMissing: true})
	if err == nil {
		t.Fatal("List: want error for MkdirAll through a file path segment, got nil")
	}
	var readErr ports.DirReadError
	if !errors.As(err, &readErr) {
		t.Fatalf("List error = %T, want ports.DirReadError", err)
	}
}

func TestDir_List_ObserverRecordsFileRead(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "a.json"), "{}")

	obs := &dirObserverSpy{}
	d := ports.NewDir(root)
	if _, err := d.List(nil, ports.DirOptions{Observer: obs}); err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(obs.reads) != 1 || !obs.reads[0].success {
		t.Fatalf("RecordFileRead calls = %+v, want one successful call", obs.reads)
	}

	obs2 := &dirObserverSpy{}
	missing := ports.NewDir(filepath.Join(root, "missing"))
	if _, err := missing.List(nil, ports.DirOptions{Observer: obs2}); err == nil {
		t.Fatal("List: want error for missing directory")
	}
	if len(obs2.reads) != 1 || obs2.reads[0].success {
		t.Fatalf("RecordFileRead calls = %+v, want one failed call", obs2.reads)
	}
}

// ── error types: Error()/LogValue() shape ────────────────────────────────────

func TestDirPathParamError_LogValue(t *testing.T) {
	err := ports.DirPathParamError{Name: "env", Value: "", Err: errors.New("must not be empty")}
	v := err.LogValue()
	if v.Kind().String() != "Group" {
		t.Fatalf("LogValue kind = %s, want Group", v.Kind())
	}
	attrs := v.Group()
	keys := map[string]bool{}
	for _, a := range attrs {
		keys[a.Key] = true
	}
	for _, want := range []string{"param", "value", "cause"} {
		if !keys[want] {
			t.Errorf("LogValue missing key %q, got %v", want, attrs)
		}
	}
}

func TestDirEntryParamError_LogValue(t *testing.T) {
	err := ports.DirEntryParamError{Entry: "temp.json", Name: "useCase", Value: "", Err: errors.New("empty")}
	v := err.LogValue()
	attrs := v.Group()
	keys := map[string]bool{}
	for _, a := range attrs {
		keys[a.Key] = true
	}
	for _, want := range []string{"entry", "param", "value", "cause"} {
		if !keys[want] {
			t.Errorf("LogValue missing key %q, got %v", want, attrs)
		}
	}
}

func TestDirReadError_Unwrap(t *testing.T) {
	inner := errors.New("permission denied")
	err := ports.DirReadError{Path: "/root", Err: inner}
	if !errors.Is(err, inner) {
		t.Error("errors.Is(DirReadError, inner) = false, want true")
	}
}

// ── test helpers ──────────────────────────────────────────────────────────────

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}
