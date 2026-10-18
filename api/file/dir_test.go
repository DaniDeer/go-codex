package file_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	adapterfile "github.com/DaniDeer/go-codex/adapters/file"
	"github.com/DaniDeer/go-codex/api/file"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/stats"
	"github.com/DaniDeer/go-codex/validate"
)

func newDirClient(t *testing.T) (*file.Client, string) {
	t.Helper()
	dir := t.TempDir()
	backend := adapterfile.NewBackend()
	client := file.NewClient()
	if err := client.Attach(backend); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	return client, dir
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func TestListDir_SingleLevel_EntryPatternFiltersAndParses(t *testing.T) {
	client, dir := newDirClient(t)
	writeFile(t, filepath.Join(dir, "alice.json"), "{}")
	writeFile(t, filepath.Join(dir, "bob.json"), "{}")
	writeFile(t, filepath.Join(dir, ".gitkeep"), "")

	nonEmpty := codex.String().Refine(validate.NonEmptyString)
	d := file.NewDir(dir, file.WithEntryPattern(file.EntryPattern{
		Template: "{name}.json",
		Params:   []file.EntryParam{{Name: "name", Codec: &nonEmpty}},
	}))
	lister := d.WithList(file.ListDir{})

	entries, err := client.ListDir(context.Background(), lister, nil)
	if err != nil {
		t.Fatalf("ListDir: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries (gitkeep excluded), got %d: %+v", len(entries), entries)
	}
	names := map[string]bool{}
	for _, e := range entries {
		names[e.Vars["name"]] = true
	}
	if !names["alice"] || !names["bob"] {
		t.Fatalf("expected alice/bob captured, got %+v", names)
	}
}

func TestListDir_NoEntryPattern_ReturnsEveryEntry(t *testing.T) {
	client, dir := newDirClient(t)
	writeFile(t, filepath.Join(dir, "a.txt"), "x")
	writeFile(t, filepath.Join(dir, "b.txt"), "y")

	d := file.NewDir(dir)
	lister := d.WithList(file.ListDir{})
	entries, err := client.ListDir(context.Background(), lister, nil)
	if err != nil {
		t.Fatalf("ListDir: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	for _, e := range entries {
		if e.Vars != nil {
			t.Fatalf("expected nil Vars with no EntryPattern, got %+v", e.Vars)
		}
	}
}

func TestListDir_Recursive_DescendsSubdirectories(t *testing.T) {
	client, dir := newDirClient(t)
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	writeFile(t, filepath.Join(dir, "top.json"), "{}")
	writeFile(t, filepath.Join(dir, "sub", "nested.json"), "{}")

	d := file.NewDir(dir, file.WithRecursive(true))
	lister := d.WithList(file.ListDir{})
	entries, err := client.ListDir(context.Background(), lister, nil)
	if err != nil {
		t.Fatalf("ListDir: %v", err)
	}
	// Recursive listing, like "ls -R", reports BOTH files and
	// directories at every depth — top.json, the "sub" directory
	// itself, and sub/nested.json.
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries (recursive, dirs included), got %d: %+v", len(entries), entries)
	}
}

func TestListDir_CreateIfMissing_UsesMkdir(t *testing.T) {
	client, dir := newDirClient(t)
	missing := filepath.Join(dir, "newdir")

	d := file.NewDir(missing)
	lister := d.WithList(file.ListDir{})
	entries, err := client.ListDir(context.Background(), lister, nil, file.DirOptions{CreateIfMissing: true})
	if err != nil {
		t.Fatalf("ListDir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected empty freshly-created dir, got %d entries", len(entries))
	}
	if _, err := os.Stat(missing); err != nil {
		t.Fatalf("expected dir to be created: %v", err)
	}
}

func TestListDir_CreateIfMissing_DryRun_DoesNotCreate(t *testing.T) {
	client, dir := newDirClient(t)
	missing := filepath.Join(dir, "newdir")

	d := file.NewDir(missing)
	lister := d.WithList(file.ListDir{})
	if _, err := client.ListDir(context.Background(), lister, nil, file.DirOptions{CreateIfMissing: true, DryRun: true}); err == nil {
		t.Fatalf("expected an error reading the still-missing dir under DryRun")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("expected dir NOT to be created under DryRun, stat err: %v", err)
	}
}

func TestDeleteDir_Idempotent_MissingIsSuccessNoError(t *testing.T) {
	client, dir := newDirClient(t)
	missing := filepath.Join(dir, "gone")

	d := file.NewDir(missing)
	deleter := d.WithDelete(file.DeleteDir{})
	deleted, err := client.DeleteDir(context.Background(), deleter, nil)
	if err != nil {
		t.Fatalf("expected idempotent success, got: %v", err)
	}
	if len(deleted) != 0 {
		t.Fatalf("expected no deleted paths, got %+v", deleted)
	}
}

func TestDeleteDir_Strict_MissingReturnsDirNotFoundError(t *testing.T) {
	client, dir := newDirClient(t)
	missing := filepath.Join(dir, "gone")

	d := file.NewDir(missing)
	deleter := d.WithDelete(file.DeleteDir{})
	_, err := client.DeleteDir(context.Background(), deleter, nil, file.DirOptions{Strict: true})
	var notFound file.DirNotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("expected DirNotFoundError, got %v", err)
	}
}

func TestDeleteDir_NonEmpty_RequiresDeleteRecursive(t *testing.T) {
	client, dir := newDirClient(t)
	target := filepath.Join(dir, "nonempty")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	writeFile(t, filepath.Join(target, "f.txt"), "x")

	d := file.NewDir(target)
	deleter := d.WithDelete(file.DeleteDir{})
	_, err := client.DeleteDir(context.Background(), deleter, nil)
	var notEmpty file.DirNotEmptyError
	if !errors.As(err, &notEmpty) {
		t.Fatalf("expected DirNotEmptyError, got %v", err)
	}

	deleted, err := client.DeleteDir(context.Background(), deleter, nil, file.DirOptions{DeleteRecursive: true})
	if err != nil {
		t.Fatalf("DeleteDir recursive: %v", err)
	}
	if len(deleted) == 0 {
		t.Fatalf("expected non-empty affected list")
	}
	if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
		t.Fatalf("expected dir removed, stat err: %v", statErr)
	}
}

func TestDeleteDir_DryRun_ReportsWithoutRemoving(t *testing.T) {
	client, dir := newDirClient(t)
	target := filepath.Join(dir, "keepme")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	writeFile(t, filepath.Join(target, "f.txt"), "x")

	d := file.NewDir(target)
	deleter := d.WithDelete(file.DeleteDir{})
	affected, err := client.DeleteDir(context.Background(), deleter, nil, file.DirOptions{DeleteRecursive: true, DryRun: true})
	if err != nil {
		t.Fatalf("DeleteDir DryRun: %v", err)
	}
	if len(affected) == 0 {
		t.Fatalf("expected affected list under DryRun")
	}
	if _, statErr := os.Stat(target); statErr != nil {
		t.Fatalf("expected dir to survive DryRun: %v", statErr)
	}
}

func TestDirCapability_AccessModeReadOnly_RejectsDeleteDirBeforeBackendCall(t *testing.T) {
	client, dir := newDirClient(t)
	target := filepath.Join(dir, "protected")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	d := file.NewDir(target)
	deleter := d.WithDelete(file.DeleteDir{}).WithCapabilities(file.AccessModeReadOnly)
	_, err := client.DeleteDir(context.Background(), deleter, nil)
	if err == nil {
		t.Fatalf("expected AccessMode ReadOnly to reject DeleteDir")
	}
	if _, statErr := os.Stat(target); statErr != nil {
		t.Fatalf("expected dir to survive the rejected delete: %v", statErr)
	}
}

func TestDirBoundMiddleware_SecurityGate_RejectsBeforeBackendCall(t *testing.T) {
	client, dir := newDirClient(t)
	target := filepath.Join(dir, "guarded")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	type grantedOut struct {
		GrantedScopes map[string][]string
	}
	bm := file.BoundSecurityDeleteDirMiddleware[struct{}, grantedOut](
		"apiKey", file.SecurityScheme{}, []string{"delete"},
		func(ctx context.Context, in struct{}) (grantedOut, error) {
			return grantedOut{GrantedScopes: nil}, nil // never grants "delete"
		},
	)

	d := file.NewDir(target)
	deleter := d.WithDelete(file.DeleteDir{}).DeleteDirBoundMW(bm).WithSecurity(file.SecurityRequirement{"apiKey": {"delete"}})
	_, err := client.DeleteDir(context.Background(), deleter, nil)
	if err == nil {
		t.Fatalf("expected Security gate to reject DeleteDir before any Backend call")
	}
	if _, statErr := os.Stat(target); statErr != nil {
		t.Fatalf("expected dir to survive the rejected delete: %v", statErr)
	}
}

func TestDirRouter_GroupedDir_ComposesPrefixOncePerHandle(t *testing.T) {
	client, dir := newDirClient(t)
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	writeFile(t, filepath.Join(dir, "sub", "x.json"), "{}")

	rt := file.NewRouter(dir)
	d := file.NewDir("sub")
	lister := d.WithList(file.ListDir{})
	handle, err := lister.Handle(client, file.WithRouter(rt))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	entries, err := client.ListDir(context.Background(), handle, nil)
	if err != nil {
		t.Fatalf("ListDir via grouped handle: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry under the router-prefixed dir, got %d", len(entries))
	}
}

func TestDirErrorPattern_AutoInvoked_OnBackendError(t *testing.T) {
	client, dir := newDirClient(t)
	target := filepath.Join(dir, "gone")

	type MappedErr struct{ Msg string }
	patterns := file.Append(file.ErrorPatterns[MappedErr](nil), file.NewMappedErrorPattern(
		func(e file.DirNotFoundError) (MappedErr, error) {
			return MappedErr{Msg: "not found: " + e.Path}, nil
		},
	))

	d := file.NewDir(target)
	deleter := d.WithDelete(file.DeleteDir{}).ErrorPatterns(patterns)
	_, err := client.DeleteDir(context.Background(), deleter, nil, file.DirOptions{Strict: true})
	if err == nil {
		t.Fatalf("expected an error")
	}
	var matched file.ErrorPatternMatchedError
	if !errors.As(err, &matched) {
		t.Fatalf("expected ErrorPatternMatchedError, got %v (%T)", err, err)
	}
	mapped, ok := matched.Payload.(MappedErr)
	if !ok {
		t.Fatalf("expected Payload to be MappedErr, got %T", matched.Payload)
	}
	if mapped.Msg == "" {
		t.Fatalf("expected a populated Msg")
	}
}

type dirObserverSpy struct {
	stats.NoopObserver
	listCalled   bool
	deleteCalled bool
}

func (s *dirObserverSpy) RecordFileList(path string, success bool, count int, d time.Duration) {
	s.listCalled = true
}
func (s *dirObserverSpy) RecordFileDelete(path string, success bool, d time.Duration) {
	s.deleteCalled = true
}

func TestDirObserver_RecordFileList_And_RecordFileDelete_Called(t *testing.T) {
	client, dir := newDirClient(t)
	writeFile(t, filepath.Join(dir, "a.txt"), "x")

	spy := &dirObserverSpy{}
	ctx := stats.WithObserver(context.Background(), spy)

	d := file.NewDir(dir)
	lister := d.WithList(file.ListDir{})
	if _, err := client.ListDir(ctx, lister, nil); err != nil {
		t.Fatalf("ListDir: %v", err)
	}
	if !spy.listCalled {
		t.Fatalf("expected RecordFileList to be called")
	}

	target := filepath.Join(dir, "todelete")
	_ = os.MkdirAll(target, 0o755)
	deleter := file.NewDir(target).WithDelete(file.DeleteDir{})
	if _, err := client.DeleteDir(ctx, deleter, nil); err != nil {
		t.Fatalf("DeleteDir: %v", err)
	}
	if !spy.deleteCalled {
		t.Fatalf("expected RecordFileDelete to be called")
	}
}
