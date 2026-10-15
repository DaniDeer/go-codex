package file_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	adapterfile "github.com/DaniDeer/go-codex/adapters/file"
	"github.com/DaniDeer/go-codex/api/file"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
)

type record struct {
	Name  string
	Value int
}

func recordCodec() codex.Codec[record] {
	return codex.Struct[record](
		codex.RequiredField("name", codex.String(),
			func(r record) string { return r.Name },
			func(r *record, v string) { r.Name = v }),
		codex.RequiredField("value", codex.Int(),
			func(r record) int { return r.Value },
			func(r *record, v int) { r.Value = v }),
	)
}

func newResource(t *testing.T, dir string) file.Resource[record] {
	t.Helper()
	return file.NewResource[record](filepath.Join(dir, "{name}.json"), format.JSON(recordCodec()),
		file.PathParam{Name: "name"},
	)
}

func TestClient_ReadCreateDelete_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	res := newResource(t, dir)
	backend := adapterfile.NewBackend()
	client := file.NewClient()
	if err := client.Attach(backend); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	ctx := context.Background()
	vars := map[string]string{"name": "alice"}

	creator := res.WithCreate(file.Create{})
	if err := client.Create(ctx, creator, record{Name: "alice", Value: 42}, vars); err != nil {
		t.Fatalf("Create: %v", err)
	}

	reader := res.WithRead(file.Read{})
	gotAny, err := client.Read(ctx, reader, vars)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	got, ok := gotAny.(record)
	if !ok {
		t.Fatalf("Read: want record, got %T", gotAny)
	}
	if got.Name != "alice" || got.Value != 42 {
		t.Fatalf("Read: got %+v", got)
	}

	deleter := res.WithDelete(file.Delete{})
	existed, err := client.Delete(ctx, deleter, vars)
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if !existed {
		t.Fatalf("Delete: want existed=true")
	}

	// Idempotent absence.
	existed2, err := client.Delete(ctx, deleter, vars)
	if err != nil {
		t.Fatalf("Delete (2nd): %v", err)
	}
	if existed2 {
		t.Fatalf("Delete (2nd): want existed=false")
	}
}

func TestClient_Read_NotFound_ReturnsFileNotFoundError(t *testing.T) {
	dir := t.TempDir()
	res := newResource(t, dir)
	client := file.NewClient()
	if err := client.Attach(adapterfile.NewBackend()); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	reader := res.WithRead(file.Read{})
	_, err := client.Read(context.Background(), reader, map[string]string{"name": "missing"})
	var notFound file.FileNotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("want FileNotFoundError, got %v (%T)", err, err)
	}
}

func TestClient_List_ReturnsDecodedEntries(t *testing.T) {
	dir := t.TempDir()
	res := newResource(t, dir)
	client := file.NewClient()
	if err := client.Attach(adapterfile.NewBackend()); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	ctx := context.Background()
	creator := res.WithCreate(file.Create{})
	for _, name := range []string{"a", "b"} {
		if err := client.Create(ctx, creator, record{Name: name, Value: 1}, map[string]string{"name": name}); err != nil {
			t.Fatalf("Create(%s): %v", name, err)
		}
	}
	lister := res.WithList(file.List{})
	gotAny, err := client.List(ctx, lister, nil)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	got, ok := gotAny.([]record)
	if !ok {
		t.Fatalf("List: want []record, got %T", gotAny)
	}
	if len(got) != 2 {
		t.Fatalf("List: want 2 entries, got %d", len(got))
	}
}

func TestClient_Exists_AbsentReturnsFalseNotError(t *testing.T) {
	dir := t.TempDir()
	res := newResource(t, dir)
	client := file.NewClient()
	if err := client.Attach(adapterfile.NewBackend()); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	exister := res.WithExists(file.Exists{})
	_, ok, err := client.Exists(context.Background(), exister, map[string]string{"name": "ghost"})
	if err != nil {
		t.Fatalf("Exists: unexpected error %v", err)
	}
	if ok {
		t.Fatalf("Exists: want ok=false for absent path")
	}
}

func TestClient_Exists_PresentReturnsTrue(t *testing.T) {
	dir := t.TempDir()
	res := newResource(t, dir)
	client := file.NewClient()
	if err := client.Attach(adapterfile.NewBackend()); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	ctx := context.Background()
	vars := map[string]string{"name": "bob"}
	if err := client.Create(ctx, res.WithCreate(file.Create{}), record{Name: "bob", Value: 1}, vars); err != nil {
		t.Fatalf("Create: %v", err)
	}
	_, ok, err := client.Exists(ctx, res.WithExists(file.Exists{}), vars)
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if !ok {
		t.Fatalf("Exists: want ok=true for present path")
	}
}

func TestClient_Copy_UsesNativeFastPathOnFilesystem(t *testing.T) {
	dir := t.TempDir()
	res := newResource(t, dir)
	client := file.NewClient()
	if err := client.Attach(adapterfile.NewBackend()); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	ctx := context.Background()
	srcVars := map[string]string{"name": "src"}
	dstVars := map[string]string{"name": "dst"}
	if err := client.Create(ctx, res.WithCreate(file.Create{}), record{Name: "src", Value: 7}, srcVars); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := client.Copy(ctx, res.WithCopy(file.Copy{}), srcVars, dstVars); err != nil {
		t.Fatalf("Copy: %v", err)
	}
	gotAny, err := client.Read(ctx, res.WithRead(file.Read{}), dstVars)
	if err != nil {
		t.Fatalf("Read dst: %v", err)
	}
	if got := gotAny.(record); got.Value != 7 {
		t.Fatalf("Copy: dst value = %d, want 7", got.Value)
	}
	// Source must still exist after Copy.
	if _, err := os.Stat(filepath.Join(dir, "src.json")); err != nil {
		t.Fatalf("Copy: source should still exist: %v", err)
	}
}

func TestClient_Move_UsesNativeRenameOnFilesystem(t *testing.T) {
	dir := t.TempDir()
	res := newResource(t, dir)
	client := file.NewClient()
	if err := client.Attach(adapterfile.NewBackend()); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	ctx := context.Background()
	srcVars := map[string]string{"name": "src2"}
	dstVars := map[string]string{"name": "dst2"}
	if err := client.Create(ctx, res.WithCreate(file.Create{}), record{Name: "src2", Value: 9}, srcVars); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := client.Move(ctx, res.WithMove(file.Move{}), srcVars, dstVars); err != nil {
		t.Fatalf("Move: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "src2.json")); !os.IsNotExist(err) {
		t.Fatalf("Move: source should no longer exist, stat err = %v", err)
	}
	gotAny, err := client.Read(ctx, res.WithRead(file.Read{}), dstVars)
	if err != nil {
		t.Fatalf("Read dst: %v", err)
	}
	if got := gotAny.(record); got.Value != 9 {
		t.Fatalf("Move: dst value = %d, want 9", got.Value)
	}
}

func TestClient_Mkdir_CreatesMissingParents(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "a", "b", "c")
	res := file.NewResource[record](nested, format.JSON(recordCodec()))
	client := file.NewClient()
	if err := client.Attach(adapterfile.NewBackend()); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	created, err := client.Mkdir(context.Background(), res.WithMkdir(file.Mkdir{}), nil)
	if err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	if !created {
		t.Fatalf("Mkdir: want created=true")
	}
	if info, err := os.Stat(nested); err != nil || !info.IsDir() {
		t.Fatalf("Mkdir: directory not created: %v", err)
	}
}

func TestClient_BatchDelete_PartialFailure_ReturnsPerKeyResults(t *testing.T) {
	dir := t.TempDir()
	res := newResource(t, dir)
	client := file.NewClient()
	if err := client.Attach(adapterfile.NewBackend()); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	ctx := context.Background()
	if err := client.Create(ctx, res.WithCreate(file.Create{}), record{Name: "exists", Value: 1}, map[string]string{"name": "exists"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	varsList := []map[string]string{
		{"name": "exists"},
		{"name": "absent"},
	}
	results, err := client.BatchDelete(ctx, res.WithBatchDelete(file.BatchDelete{}), varsList)
	if err != nil {
		t.Fatalf("BatchDelete: unexpected call-level error %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("BatchDelete: want 2 results, got %d", len(results))
	}
	if !results[0].Existed || results[0].Err != nil {
		t.Fatalf("BatchDelete[0]: want existed=true, no error; got %+v", results[0])
	}
	if results[1].Existed || results[1].Err != nil {
		t.Fatalf("BatchDelete[1]: want existed=false (idempotent absence), no error; got %+v", results[1])
	}
}

func TestClient_Read_AcceptsRawBuilderOrPreBuiltHandle(t *testing.T) {
	dir := t.TempDir()
	res := newResource(t, dir)
	client := file.NewClient()
	if err := client.Attach(adapterfile.NewBackend()); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	ctx := context.Background()
	vars := map[string]string{"name": "dual"}
	if err := client.Create(ctx, res.WithCreate(file.Create{}), record{Name: "dual", Value: 5}, vars); err != nil {
		t.Fatalf("Create: %v", err)
	}

	bareAny, err := client.Read(ctx, res.WithRead(file.Read{}), vars)
	if err != nil {
		t.Fatalf("Read (bare builder): %v", err)
	}

	handle, err := res.WithRead(file.Read{}).Handle(client)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	handleAny, err := client.Read(ctx, handle, vars)
	if err != nil {
		t.Fatalf("Read (pre-built handle): %v", err)
	}

	if bareAny.(record) != handleAny.(record) {
		t.Fatalf("dual-accept mismatch: bare=%+v handle=%+v", bareAny, handleAny)
	}
}

func TestClient_Attach_Exclusive_SecondCallReturnsBackendAlreadyAttachedError(t *testing.T) {
	client := file.NewClient()
	if err := client.Attach(adapterfile.NewBackend()); err != nil {
		t.Fatalf("Attach (1st): %v", err)
	}
	err := client.Attach(adapterfile.NewBackend())
	var already file.BackendAlreadyAttachedError
	if !errors.As(err, &already) {
		t.Fatalf("want BackendAlreadyAttachedError, got %v (%T)", err, err)
	}
}

func TestClient_Read_NoBackendAttached_ReturnsNoBackendAttachedError(t *testing.T) {
	dir := t.TempDir()
	res := newResource(t, dir)
	client := file.NewClient()
	_, err := client.Read(context.Background(), res.WithRead(file.Read{}), map[string]string{"name": "x"})
	var noBackend file.NoBackendAttachedError
	if !errors.As(err, &noBackend) {
		t.Fatalf("want NoBackendAttachedError, got %v (%T)", err, err)
	}
}

func TestClient_Create_AccessModeReadOnly_RejectsBeforeBackendCall(t *testing.T) {
	dir := t.TempDir()
	res := newResource(t, dir)
	client := file.NewClient()
	if err := client.Attach(adapterfile.NewBackend()); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	creator := res.WithCreate(file.Create{}).WithCapabilities(file.AccessModeReadOnly)
	err := client.Create(context.Background(), creator, record{Name: "ro", Value: 1}, map[string]string{"name": "ro"})
	if err == nil {
		t.Fatalf("want rejection error for AccessModeReadOnly Create")
	}
	if _, statErr := os.Stat(filepath.Join(dir, "ro.json")); !os.IsNotExist(statErr) {
		t.Fatalf("Create should NOT have reached the backend: file exists")
	}
}

func TestPatcher_HandlePatch_AppliesPartialUpdate(t *testing.T) {
	dir := t.TempDir()
	res := newResource(t, dir)
	client := file.NewClient()
	if err := client.Attach(adapterfile.NewBackend()); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	ctx := context.Background()
	vars := map[string]string{"name": "patchme"}
	if err := client.Create(ctx, res.WithCreate(file.Create{}), record{Name: "patchme", Value: 1}, vars); err != nil {
		t.Fatalf("Create: %v", err)
	}

	type valuePatch struct {
		Value *int
	}
	patchCodec := codex.PartialStruct[valuePatch](
		codex.PartialField("value", codex.Int(),
			func(p valuePatch) *int { return p.Value },
			func(p *valuePatch, v *int) { p.Value = v }),
	)
	newValue := 99
	patcher := file.WithPatch(res, patchCodec, file.Patch{})
	if err := client.Patch(ctx, patcher, valuePatch{Value: &newValue}, vars); err != nil {
		t.Fatalf("Patch: %v", err)
	}
	gotAny, err := client.Read(ctx, res.WithRead(file.Read{}), vars)
	if err != nil {
		t.Fatalf("Read after patch: %v", err)
	}
	got := gotAny.(record)
	if got.Value != 99 || got.Name != "patchme" {
		t.Fatalf("Patch: got %+v, want Value=99 Name=patchme (unchanged)", got)
	}
}

// ── Metadata axis (MetadataBackend / MetadataParam) ───────────────────────

type recordWithOwner struct {
	Name  string
	Value int
	Owner string
}

func recordWithOwnerCodec() codex.Codec[recordWithOwner] {
	return codex.Struct[recordWithOwner](
		codex.RequiredField("name", codex.String(),
			func(r recordWithOwner) string { return r.Name },
			func(r *recordWithOwner, v string) { r.Name = v }),
		codex.RequiredField("value", codex.Int(),
			func(r recordWithOwner) int { return r.Value },
			func(r *recordWithOwner, v int) { r.Value = v }),
	)
}

func TestClient_Metadata_RoundTripsViaMergeField(t *testing.T) {
	dir := t.TempDir()
	res := file.NewResource[recordWithOwner](filepath.Join(dir, "{name}.json"), format.JSON(recordWithOwnerCodec()),
		file.PathParam{Name: "name"},
		file.NewMetadataParam("owner", codex.String(),
			func(r recordWithOwner) string { return r.Owner },
			func(r *recordWithOwner, v string) { r.Owner = v }),
	)
	client := file.NewClient()
	if err := client.Attach(adapterfile.NewBackend()); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	ctx := context.Background()
	vars := map[string]string{"name": "widget"}

	if err := client.Create(ctx, res.WithCreate(file.Create{}), recordWithOwner{Name: "widget", Value: 1, Owner: "alice"}, vars); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// The sidecar metadata file must exist alongside the resource.
	if _, err := os.Stat(filepath.Join(dir, "widget.json.meta.json")); err != nil {
		t.Fatalf("expected metadata sidecar file to exist: %v", err)
	}

	gotAny, err := client.Read(ctx, res.WithRead(file.Read{}), vars)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	got := gotAny.(recordWithOwner)
	if got.Owner != "alice" {
		t.Fatalf("Read: want Owner=alice merged back from metadata, got %+v", got)
	}
}

func TestClient_Metadata_AbsentSidecar_LeavesFieldZeroValue(t *testing.T) {
	dir := t.TempDir()
	res := file.NewResource[recordWithOwner](filepath.Join(dir, "{name}.json"), format.JSON(recordWithOwnerCodec()),
		file.PathParam{Name: "name"},
		file.NewMetadataParam("owner", codex.String(),
			func(r recordWithOwner) string { return r.Owner },
			func(r *recordWithOwner, v string) { r.Owner = v }),
	)
	client := file.NewClient()
	if err := client.Attach(adapterfile.NewBackend()); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	ctx := context.Background()
	vars := map[string]string{"name": "noowner"}

	// Create via a resource WITHOUT the metadata merge field declared,
	// so no sidecar is ever written for this path.
	plainRes := file.NewResource[recordWithOwner](filepath.Join(dir, "{name}.json"), format.JSON(recordWithOwnerCodec()),
		file.PathParam{Name: "name"},
	)
	if err := client.Create(ctx, plainRes.WithCreate(file.Create{}), recordWithOwner{Name: "noowner", Value: 2}, vars); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "noowner.json.meta.json")); !os.IsNotExist(err) {
		t.Fatalf("expected NO metadata sidecar file, stat err = %v", err)
	}

	gotAny, err := client.Read(ctx, res.WithRead(file.Read{}), vars)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	got := gotAny.(recordWithOwner)
	if got.Owner != "" {
		t.Fatalf("Read: want Owner zero-value when no sidecar exists, got %q", got.Owner)
	}
}

// ── Router value type ──────────────────────────────────────────────────

func TestRouter_Group_PrefixesResourcePath(t *testing.T) {
	dir := t.TempDir()
	// A relative resource template ("{name}.json", no dir baked in) — the
	// Router's OWN prefix supplies the absolute temp subdirectory,
	// exactly mirroring how examples/file-api/main.go replaces its
	// manual filepath.Join with a Router prefix. A SEPARATE, absolute
	// "plain" resource (writing directly into dir, never under
	// "grouped/") proves the Router-grouped Read looks in the RIGHT
	// place — using it (not a relative-template write, which would
	// otherwise escape t.TempDir() into the process's real CWD).
	res := file.NewResource[record]("{name}.json", format.JSON(recordCodec()), file.PathParam{Name: "name"})
	plainRes := newResource(t, dir)
	backend := adapterfile.NewBackend()
	client := file.NewClient()
	if err := client.Attach(backend); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	ctx := context.Background()

	rt := file.NewRouter(filepath.Join(dir, "grouped"))
	reader := res.WithRead(file.Read{})
	handle, err := reader.Handle(client, file.WithRouter(rt))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	vars := map[string]string{"name": "prefixed"}
	// Create directly at the UN-prefixed (plain, dir-rooted) path first,
	// to prove the Router-grouped Read looks in "grouped/" instead.
	if err := client.Create(ctx, plainRes.WithCreate(file.Create{}), record{Name: "prefixed", Value: 1}, vars); err != nil {
		t.Fatalf("Create (unprefixed): %v", err)
	}
	if _, err := client.Read(ctx, handle, vars); err == nil {
		t.Fatalf("Read via Router-grouped handle unexpectedly found the UN-prefixed file")
	}

	// Now create under the prefix via the SAME Router-grouped mechanism
	// (a Creator built through the same Router) and confirm Read finds it.
	if err := os.MkdirAll(filepath.Join(dir, "grouped"), 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	createHandle, err := res.WithCreate(file.Create{}).Handle(client, file.WithRouter(rt))
	if err != nil {
		t.Fatalf("Handle (create): %v", err)
	}
	if err := client.Create(ctx, createHandle, record{Name: "prefixed", Value: 2}, vars); err != nil {
		t.Fatalf("Create (grouped): %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "grouped", "prefixed.json")); err != nil {
		t.Fatalf("expected file under grouped/ prefix: %v", err)
	}
	gotAny, err := client.Read(ctx, handle, vars)
	if err != nil {
		t.Fatalf("Read via Router-grouped handle: %v", err)
	}
	if got := gotAny.(record); got.Value != 2 {
		t.Fatalf("Read via Router-grouped handle: got Value=%d, want 2", got.Value)
	}
}

func TestRouter_Register_RunsFailFastCoverageCheck(t *testing.T) {
	dir := t.TempDir()
	res := newResource(t, dir)
	client := file.NewClient()
	if err := client.Attach(adapterfile.NewBackend()); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	rt := file.NewRouter("x").
		Route(res.WithRead(file.Read{})).
		Route(res.WithCreate(file.Create{}))
	if err := rt.Register(client); err != nil {
		t.Fatalf("Register: unexpected error for a Resource with no declared requirements: %v", err)
	}
}

func TestRouter_Routes_ReportsPrefixedPathsAndMiddlewareNames(t *testing.T) {
	dir := t.TempDir()
	res := newResource(t, dir)

	rt := file.NewRouter("grouped").
		Route(res.WithRead(file.Read{})).
		Route(res.WithCreate(file.Create{}))

	entries := rt.Routes()
	if len(entries) != 2 {
		t.Fatalf("Routes: want 2 entries, got %d", len(entries))
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Path, "grouped/") {
			t.Fatalf("Routes: entry %+v missing grouped/ prefix", e)
		}
	}
}

// ── BoundMiddleware (Security + GrantedScopes dispatch) ────────────────

type grantedOut struct {
	GrantedScopes map[string][]string
}

func TestReader_ReadBoundMW_SecuritySatisfied_Succeeds(t *testing.T) {
	dir := t.TempDir()
	res := newResource(t, dir)
	client := file.NewClient()
	if err := client.Attach(adapterfile.NewBackend()); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	ctx := context.Background()
	vars := map[string]string{"name": "secured"}
	if err := client.Create(ctx, res.WithCreate(file.Create{}), record{Name: "secured", Value: 1}, vars); err != nil {
		t.Fatalf("Create: %v", err)
	}

	bm := file.BoundSecurityReadMiddleware[record, struct{}, grantedOut](
		"apiKey", file.SecurityScheme{}, []string{"read"},
		func(ctx context.Context, v *record, in struct{}) (grantedOut, error) {
			return grantedOut{GrantedScopes: map[string][]string{"apiKey": {"read"}}}, nil
		},
	)
	reader := res.WithRead(file.Read{}).ReadBoundMW(bm).WithSecurity(file.SecurityRequirement{"apiKey": {"read"}})
	gotAny, err := client.Read(ctx, reader, vars)
	if err != nil {
		t.Fatalf("Read: unexpected error: %v", err)
	}
	if got := gotAny.(record); got.Name != "secured" {
		t.Fatalf("Read: got %+v", got)
	}
}

func TestReader_ReadBoundMW_SecurityUnmet_RejectsBeforeBackendCall(t *testing.T) {
	dir := t.TempDir()
	res := newResource(t, dir)
	client := file.NewClient()
	if err := client.Attach(adapterfile.NewBackend()); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	ctx := context.Background()
	vars := map[string]string{"name": "secured2"}
	if err := client.Create(ctx, res.WithCreate(file.Create{}), record{Name: "secured2", Value: 1}, vars); err != nil {
		t.Fatalf("Create: %v", err)
	}

	bm := file.BoundSecurityReadMiddleware[record, struct{}, grantedOut](
		"apiKey", file.SecurityScheme{}, []string{"read"},
		func(ctx context.Context, v *record, in struct{}) (grantedOut, error) {
			// Grants a DIFFERENT scope than required - should fail CheckScopes.
			return grantedOut{GrantedScopes: map[string][]string{"apiKey": {"wrong-scope"}}}, nil
		},
	)
	reader := res.WithRead(file.Read{}).ReadBoundMW(bm).WithSecurity(file.SecurityRequirement{"apiKey": {"read"}})
	_, err := client.Read(ctx, reader, vars)
	var secErr file.SecurityRejectedError
	if !errors.As(err, &secErr) {
		t.Fatalf("want SecurityRejectedError, got %v (%T)", err, err)
	}
}

func TestReader_ReadBoundMW_WrongTTypeMismatch_RecordedAtHandleTime(t *testing.T) {
	dir := t.TempDir()
	res := newResource(t, dir)
	client := file.NewClient()
	if err := client.Attach(adapterfile.NewBackend()); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	type otherType struct{ X int }
	bm := file.NewBoundReadMiddleware[otherType, struct{}, struct{}](
		file.NewDeclaration("test-mw", codex.Struct[struct{}](), codex.Struct[struct{}]()),
		func(ctx context.Context, v *otherType, in struct{}) (struct{}, error) { return struct{}{}, nil },
	)
	reader := res.WithRead(file.Read{}).ReadBoundMW(bm)
	_, err := reader.Handle(client)
	var mismatch file.BoundMiddlewareReqMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("want BoundMiddlewareReqMismatchError, got %v (%T)", err, err)
	}
}

func TestDeleter_DeleteBoundMW_SecurityUnmet_RejectsBeforeBackendCall(t *testing.T) {
	dir := t.TempDir()
	res := newResource(t, dir)
	client := file.NewClient()
	if err := client.Attach(adapterfile.NewBackend()); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	ctx := context.Background()
	vars := map[string]string{"name": "delsecured"}
	if err := client.Create(ctx, res.WithCreate(file.Create{}), record{Name: "delsecured", Value: 1}, vars); err != nil {
		t.Fatalf("Create: %v", err)
	}

	bm := file.BoundSecurityDeleteMiddleware[struct{}, grantedOut](
		"apiKey", file.SecurityScheme{}, []string{"delete"},
		func(ctx context.Context, in struct{}) (grantedOut, error) {
			return grantedOut{GrantedScopes: nil}, nil
		},
	)
	deleter := res.WithDelete(file.Delete{}).DeleteBoundMW(bm).WithSecurity(file.SecurityRequirement{"apiKey": {"delete"}})
	_, err := client.Delete(ctx, deleter, vars)
	var secErr file.SecurityRejectedError
	if !errors.As(err, &secErr) {
		t.Fatalf("want SecurityRejectedError, got %v (%T)", err, err)
	}
	// Confirm the file was NOT deleted (backend call never reached).
	if _, statErr := os.Stat(filepath.Join(dir, "delsecured.json")); statErr != nil {
		t.Fatalf("file should still exist (rejected before backend call): %v", statErr)
	}
}

func TestReader_ReadBoundMW_MultipleAttachments_RunInOrder(t *testing.T) {
	dir := t.TempDir()
	res := newResource(t, dir)
	client := file.NewClient()
	if err := client.Attach(adapterfile.NewBackend()); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	ctx := context.Background()
	vars := map[string]string{"name": "ordered"}
	if err := client.Create(ctx, res.WithCreate(file.Create{}), record{Name: "ordered", Value: 1}, vars); err != nil {
		t.Fatalf("Create: %v", err)
	}

	var order []string
	first := file.NewBoundReadMiddleware[record, struct{}, struct{}](
		file.NewDeclaration("first", codex.Struct[struct{}](), codex.Struct[struct{}]()),
		func(ctx context.Context, v *record, in struct{}) (struct{}, error) {
			order = append(order, "first")
			return struct{}{}, nil
		},
	)
	second := file.NewBoundReadMiddleware[record, struct{}, struct{}](
		file.NewDeclaration("second", codex.Struct[struct{}](), codex.Struct[struct{}]()),
		func(ctx context.Context, v *record, in struct{}) (struct{}, error) {
			order = append(order, "second")
			return struct{}{}, nil
		},
	)
	reader := res.WithRead(file.Read{}).ReadBoundMW(first).ReadBoundMW(second)
	if _, err := client.Read(ctx, reader, vars); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(order) != 2 || order[0] != "first" || order[1] != "second" {
		t.Fatalf("want [first second] in order, got %v", order)
	}
}

// ── ErrorPattern auto-invocation ─────────────────────────────────────────

type notFoundResponse struct {
	Code    string
	Message string
}

func TestReader_ErrorPatterns_AutoInvokedOnBackendError(t *testing.T) {
	dir := t.TempDir()
	res := newResource(t, dir)
	client := file.NewClient()
	if err := client.Attach(adapterfile.NewBackend()); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	ctx := context.Background()

	pattern := file.NewMappedErrorPattern(func(e file.FileNotFoundError) (notFoundResponse, error) {
		return notFoundResponse{Code: "NOT_FOUND", Message: "the thing is missing: " + e.Path}, nil
	})
	patterns := file.Append(file.ErrorPatterns[notFoundResponse]{}, pattern)

	reader := res.WithRead(file.Read{}).ErrorPatterns(patterns)
	_, err := client.Read(ctx, reader, map[string]string{"name": "ghost2"})
	var matched file.ErrorPatternMatchedError
	if !errors.As(err, &matched) {
		t.Fatalf("want ErrorPatternMatchedError, got %v (%T)", err, err)
	}
	resp, ok := matched.Payload.(notFoundResponse)
	if !ok {
		t.Fatalf("Payload type = %T, want notFoundResponse", matched.Payload)
	}
	if resp.Code != "NOT_FOUND" {
		t.Fatalf("resp.Code = %q, want NOT_FOUND", resp.Code)
	}
	// errors.As must still reach the ORIGINAL error through Unwrap.
	var notFound file.FileNotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("want errors.As to still reach the original FileNotFoundError")
	}
}

func TestReader_ErrorPatterns_NoMatch_ReturnsOriginalErrorUnchanged(t *testing.T) {
	dir := t.TempDir()
	res := newResource(t, dir)
	client := file.NewClient()
	if err := client.Attach(adapterfile.NewBackend()); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	ctx := context.Background()

	// Pattern declared for a DIFFERENT error type - should never match a
	// FileNotFoundError, leaving it completely unwrapped.
	pattern := file.NewErrorPattern[file.FileAlreadyExistsError]()
	patterns := file.Append(file.ErrorPatterns[file.FileAlreadyExistsError]{}, pattern)

	reader := res.WithRead(file.Read{}).ErrorPatterns(patterns)
	_, err := client.Read(ctx, reader, map[string]string{"name": "ghost3"})
	var matched file.ErrorPatternMatchedError
	if errors.As(err, &matched) {
		t.Fatalf("want NO ErrorPatternMatchedError wrapping for a non-matching pattern, got %v", err)
	}
	var notFound file.FileNotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("want plain FileNotFoundError, got %v (%T)", err, err)
	}
}

// ── List path-var merge field ────────────────────────────────────────────

func TestLister_MergesPathVarIntoEachEntry(t *testing.T) {
	dir := t.TempDir()
	res := file.NewResource[recordWithOwner](filepath.Join(dir, "{name}.json"), format.JSON(recordWithOwnerCodec()),
		file.NewPathParam("name", codex.String(),
			func(r recordWithOwner) string { return r.Name },
			func(r *recordWithOwner, v string) { r.Name = v }),
	)
	client := file.NewClient()
	if err := client.Attach(adapterfile.NewBackend()); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	ctx := context.Background()
	for _, name := range []string{"first", "second"} {
		// Omit Name from the written JSON body entirely (only Value is
		// codec-required) - proves the entry's Name comes PURELY from
		// the merge field, not from the stored bytes.
		if err := client.Create(ctx, res.WithCreate(file.Create{}), recordWithOwner{Value: 1}, map[string]string{"name": name}); err != nil {
			t.Fatalf("Create(%s): %v", name, err)
		}
	}

	gotAny, err := client.List(ctx, res.WithList(file.List{}), nil)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	got := gotAny.([]recordWithOwner)
	if len(got) != 2 {
		t.Fatalf("List: want 2 entries, got %d", len(got))
	}
	names := map[string]bool{}
	for _, r := range got {
		names[r.Name] = true
	}
	if !names["first"] || !names["second"] {
		t.Fatalf("List: want Name merged in from each entry's filename, got %+v", got)
	}
}

func TestLister_NonMatchingEntry_ReturnsFilePathMismatchError(t *testing.T) {
	dir := t.TempDir()
	res := file.NewResource[recordWithOwner](filepath.Join(dir, "{name}.json"), format.JSON(recordWithOwnerCodec()),
		file.NewPathParam("name", codex.String(),
			func(r recordWithOwner) string { return r.Name },
			func(r *recordWithOwner, v string) { r.Name = v }),
	)
	client := file.NewClient()
	if err := client.Attach(adapterfile.NewBackend()); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	ctx := context.Background()
	if err := client.Create(ctx, res.WithCreate(file.Create{}), recordWithOwner{Value: 1}, map[string]string{"name": "ok"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	// A stray file with a VALID body (so Unmarshal succeeds first) but an
	// extension that genuinely does not match the template's literal
	// ".json" suffix — "{name}.json"'s regex requires a literal ".json"
	// ending, so "stray.txt" (no .json at all) cannot match under ANY
	// name value, surfacing the path-mismatch this test proves.
	if err := os.WriteFile(filepath.Join(dir, "stray.txt"), []byte(`{"name":"x","value":5}`), 0644); err != nil {
		t.Fatalf("write stray file: %v", err)
	}

	_, err := client.List(ctx, res.WithList(file.List{}), nil)
	var mismatch file.FilePathMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("want FilePathMismatchError, got %v (%T)", err, err)
	}
}

func TestLister_NoMergeFieldsDeclared_UnaffectedRegression(t *testing.T) {
	dir := t.TempDir()
	res := newResource(t, dir)
	client := file.NewClient()
	if err := client.Attach(adapterfile.NewBackend()); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	ctx := context.Background()
	if err := client.Create(ctx, res.WithCreate(file.Create{}), record{Name: "plain", Value: 1}, map[string]string{"name": "plain"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	gotAny, err := client.List(ctx, res.WithList(file.List{}), nil)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(gotAny.([]record)) != 1 {
		t.Fatalf("List: want 1 entry, got %+v", gotAny)
	}
}

// ── BoundMiddleware metadata integration (Create/Patch) ──────────────────

type mwMetaIn struct {
	Owner string
}

type mwMetaOut struct {
	Owner string
}

func TestBoundCreateMiddleware_MetadataIn_PopulatesInFromIntendedMetadata(t *testing.T) {
	dir := t.TempDir()
	res := file.NewResource[recordWithOwner](filepath.Join(dir, "{name}.json"), format.JSON(recordWithOwnerCodec()),
		file.PathParam{Name: "name"},
		file.NewMetadataParam("owner", codex.String(),
			func(r recordWithOwner) string { return r.Owner },
			func(r *recordWithOwner, v string) { r.Owner = v }),
	)
	client := file.NewClient()
	if err := client.Attach(adapterfile.NewBackend()); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	ctx := context.Background()

	var gotIn mwMetaIn
	bm := file.NewBoundCreateMiddleware[recordWithOwner, mwMetaIn, mwMetaOut](
		file.NewDeclaration[mwMetaIn, mwMetaOut]("create-mw", codex.Struct[mwMetaIn](), codex.Struct[mwMetaOut]()),
		func(ctx context.Context, v *recordWithOwner, in mwMetaIn) (mwMetaOut, error) {
			gotIn = in
			return mwMetaOut{}, nil
		},
	).WithMetadataIn(file.NewMetadataParam("owner", codex.String(),
		func(m mwMetaIn) string { return m.Owner },
		func(m *mwMetaIn, v string) { m.Owner = v }))
	creator := res.WithCreate(file.Create{}).CreateBoundMW(bm)
	if err := client.Create(ctx, creator, recordWithOwner{Name: "widget", Value: 1, Owner: "alice"}, map[string]string{"name": "widget"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if gotIn.Owner != "alice" {
		t.Fatalf("BoundCreateMiddleware.In.Owner = %q, want %q (derived from v.Owner via the resource's own metadataMergeFields)", gotIn.Owner, "alice")
	}
}

func TestBoundCreateMiddleware_MetadataOut_MergesIntoPersistedMetadata(t *testing.T) {
	dir := t.TempDir()
	res := file.NewResource[recordWithOwner](filepath.Join(dir, "{name}.json"), format.JSON(recordWithOwnerCodec()),
		file.PathParam{Name: "name"},
		file.NewMetadataParam("owner", codex.String(),
			func(r recordWithOwner) string { return r.Owner },
			func(r *recordWithOwner, v string) { r.Owner = v }),
	)
	client := file.NewClient()
	if err := client.Attach(adapterfile.NewBackend()); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	ctx := context.Background()

	bm := file.NewBoundCreateMiddleware[recordWithOwner, mwMetaIn, mwMetaOut](
		file.NewDeclaration[mwMetaIn, mwMetaOut]("create-mw-out", codex.Struct[mwMetaIn](), codex.Struct[mwMetaOut]()),
		func(ctx context.Context, v *recordWithOwner, in mwMetaIn) (mwMetaOut, error) {
			// Overrides whatever the resource's own v.Owner would have
			// derived - proves the middleware's OWN contribution wins.
			return mwMetaOut{Owner: "middleware-override"}, nil
		},
	).WithMetadataOut(file.NewMetadataParam("owner", codex.String(),
		func(m mwMetaOut) string { return m.Owner },
		func(m *mwMetaOut, v string) { m.Owner = v }))
	creator := res.WithCreate(file.Create{}).CreateBoundMW(bm)
	if err := client.Create(ctx, creator, recordWithOwner{Name: "gadget", Value: 1, Owner: "original-owner"}, map[string]string{"name": "gadget"}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	gotAny, err := client.Read(ctx, res.WithRead(file.Read{}), map[string]string{"name": "gadget"})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	got := gotAny.(recordWithOwner)
	if got.Owner != "middleware-override" {
		t.Fatalf("Owner = %q, want %q (middleware's WithMetadataOut contribution should win)", got.Owner, "middleware-override")
	}
}

func TestBoundReadMiddleware_MetadataIn_AlwaysZeroValue(t *testing.T) {
	dir := t.TempDir()
	res := file.NewResource[recordWithOwner](filepath.Join(dir, "{name}.json"), format.JSON(recordWithOwnerCodec()),
		file.PathParam{Name: "name"},
		file.NewMetadataParam("owner", codex.String(),
			func(r recordWithOwner) string { return r.Owner },
			func(r *recordWithOwner, v string) { r.Owner = v }),
	)
	client := file.NewClient()
	if err := client.Attach(adapterfile.NewBackend()); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	ctx := context.Background()
	if err := client.Create(ctx, res.WithCreate(file.Create{}), recordWithOwner{Name: "x", Value: 1, Owner: "bob"}, map[string]string{"name": "x"}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	var gotIn mwMetaIn
	seen := false
	bm := file.NewBoundReadMiddleware[recordWithOwner, mwMetaIn, mwMetaOut](
		file.NewDeclaration[mwMetaIn, mwMetaOut]("read-mw", codex.Struct[mwMetaIn](), codex.Struct[mwMetaOut]()),
		func(ctx context.Context, v *recordWithOwner, in mwMetaIn) (mwMetaOut, error) {
			gotIn = in
			seen = true
			return mwMetaOut{}, nil
		},
	)
	reader := res.WithRead(file.Read{}).ReadBoundMW(bm)
	if _, err := client.Read(ctx, reader, map[string]string{"name": "x"}); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !seen {
		t.Fatalf("BoundReadMiddleware Fn was never invoked")
	}
	// Even though the stored metadata has owner="bob", In must stay
	// zero-valued - Read's metadata is only available AFTER the Backend
	// call, but the Security/BoundMiddleware gate runs BEFORE it.
	if gotIn.Owner != "" {
		t.Fatalf("BoundReadMiddleware.In.Owner = %q, want \"\" (permanently zero-valued for Read)", gotIn.Owner)
	}
}

func TestBoundPatchMiddleware_MetadataIn_AlwaysZeroValue(t *testing.T) {
	dir := t.TempDir()
	res := file.NewResource[recordWithOwner](filepath.Join(dir, "{name}.json"), format.JSON(recordWithOwnerCodec()),
		file.PathParam{Name: "name"},
		file.NewMetadataParam("owner", codex.String(),
			func(r recordWithOwner) string { return r.Owner },
			func(r *recordWithOwner, v string) { r.Owner = v }),
	)
	client := file.NewClient()
	if err := client.Attach(adapterfile.NewBackend()); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	ctx := context.Background()
	vars := map[string]string{"name": "patchtarget"}
	if err := client.Create(ctx, res.WithCreate(file.Create{}), recordWithOwner{Name: "patchtarget", Value: 1, Owner: "carol"}, vars); err != nil {
		t.Fatalf("Create: %v", err)
	}

	type valuePatch struct{ Value *int }
	patchCodec := codex.PartialStruct[valuePatch](
		codex.PartialField("value", codex.Int(),
			func(p valuePatch) *int { return p.Value },
			func(p *valuePatch, v *int) { p.Value = v }),
	)

	var gotIn mwMetaIn
	bm := file.NewBoundPatchMiddleware[recordWithOwner, mwMetaIn, mwMetaOut](
		file.NewDeclaration[mwMetaIn, mwMetaOut]("patch-mw", codex.Struct[mwMetaIn](), codex.Struct[mwMetaOut]()),
		func(ctx context.Context, v *recordWithOwner, in mwMetaIn) (mwMetaOut, error) {
			gotIn = in
			return mwMetaOut{}, nil
		},
	).WithMetadataIn(file.NewMetadataParam("owner", codex.String(),
		func(m mwMetaIn) string { return m.Owner },
		func(m *mwMetaIn, v string) { m.Owner = v }))
	patcher := file.WithPatch(res, patchCodec, file.Patch{}).PatchBoundMW(bm)
	newValue := 99
	if err := client.Patch(ctx, patcher, valuePatch{Value: &newValue}, vars); err != nil {
		t.Fatalf("Patch: %v", err)
	}
	if gotIn.Owner != "" {
		t.Fatalf("BoundPatchMiddleware.In.Owner = %q, want \"\" (Patch's base T isn't available at invoke time)", gotIn.Owner)
	}
}

func TestBoundPatchMiddleware_MetadataOut_MergesIntoPersistedMetadata(t *testing.T) {
	dir := t.TempDir()
	res := file.NewResource[recordWithOwner](filepath.Join(dir, "{name}.json"), format.JSON(recordWithOwnerCodec()),
		file.PathParam{Name: "name"},
		file.NewMetadataParam("owner", codex.String(),
			func(r recordWithOwner) string { return r.Owner },
			func(r *recordWithOwner, v string) { r.Owner = v }),
	)
	client := file.NewClient()
	if err := client.Attach(adapterfile.NewBackend()); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	ctx := context.Background()
	vars := map[string]string{"name": "patchtarget2"}
	if err := client.Create(ctx, res.WithCreate(file.Create{}), recordWithOwner{Name: "patchtarget2", Value: 1, Owner: "dave"}, vars); err != nil {
		t.Fatalf("Create: %v", err)
	}

	type valuePatch struct{ Value *int }
	patchCodec := codex.PartialStruct[valuePatch](
		codex.PartialField("value", codex.Int(),
			func(p valuePatch) *int { return p.Value },
			func(p *valuePatch, v *int) { p.Value = v }),
	)

	bm := file.NewBoundPatchMiddleware[recordWithOwner, mwMetaIn, mwMetaOut](
		file.NewDeclaration[mwMetaIn, mwMetaOut]("patch-mw-out", codex.Struct[mwMetaIn](), codex.Struct[mwMetaOut]()),
		func(ctx context.Context, v *recordWithOwner, in mwMetaIn) (mwMetaOut, error) {
			return mwMetaOut{Owner: "patched-by-middleware"}, nil
		},
	).WithMetadataOut(file.NewMetadataParam("owner", codex.String(),
		func(m mwMetaOut) string { return m.Owner },
		func(m *mwMetaOut, v string) { m.Owner = v }))
	patcher := file.WithPatch(res, patchCodec, file.Patch{}).PatchBoundMW(bm)
	newValue := 42
	if err := client.Patch(ctx, patcher, valuePatch{Value: &newValue}, vars); err != nil {
		t.Fatalf("Patch: %v", err)
	}

	gotAny, err := client.Read(ctx, res.WithRead(file.Read{}), vars)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	got := gotAny.(recordWithOwner)
	if got.Owner != "patched-by-middleware" {
		t.Fatalf("Owner = %q, want %q", got.Owner, "patched-by-middleware")
	}
	if got.Value != 42 {
		t.Fatalf("Value = %d, want 42 (patch itself should still apply normally)", got.Value)
	}
}
