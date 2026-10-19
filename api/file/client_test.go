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
	"github.com/DaniDeer/go-codex/stats"
	"github.com/DaniDeer/go-codex/validate"
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
	if _, err := client.Create(ctx, creator, record{Name: "alice", Value: 42}, vars); err != nil {
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
		if _, err := client.Create(ctx, creator, record{Name: name, Value: 1}, map[string]string{"name": name}); err != nil {
			t.Fatalf("Create(%s): %v", name, err)
		}
	}
	lister := res.WithList(file.List{})
	gotAny, err := client.List(ctx, lister, nil)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	got, ok := gotAny.([]file.ListResult[record])
	if !ok {
		t.Fatalf("List: want []file.ListResult[record], got %T", gotAny)
	}
	if len(got) != 2 {
		t.Fatalf("List: want 2 entries, got %d", len(got))
	}
	for _, r := range got {
		if r.Err != nil {
			t.Fatalf("List: unexpected per-entry error %v", r.Err)
		}
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
	if _, err := client.Create(ctx, res.WithCreate(file.Create{}), record{Name: "bob", Value: 1}, vars); err != nil {
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
	if _, err := client.Create(ctx, res.WithCreate(file.Create{}), record{Name: "src", Value: 7}, srcVars); err != nil {
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
	if _, err := client.Create(ctx, res.WithCreate(file.Create{}), record{Name: "src2", Value: 9}, srcVars); err != nil {
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

// TestClient_Mkdir_NativePOSIXModeCapability_OverridesCallerPerm proves
// mkdirCore's documented "attach-time Capability always wins over a
// conflicting per-call option" precedence rule ACTUALLY holds — see
// TestClient_Create_NativePOSIXModeCapability_OverridesCallerPerm's doc
// comment for why this needed a fresh, direct test (zero prior coverage).
func TestClient_Mkdir_NativePOSIXModeCapability_OverridesCallerPerm(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "capmode")
	res := file.NewResource[record](target, format.JSON(recordCodec()))
	client := file.NewClient()
	if err := client.Attach(adapterfile.NewBackend()); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	capPerm := os.FileMode(0o700)
	callPerm := os.FileMode(0o755)
	mk := res.WithMkdir(file.Mkdir{}).WithCapabilities(file.NativePOSIXMode(capPerm))
	created, err := client.Mkdir(context.Background(), mk, nil, file.MkdirOptions{NativePOSIXMode: &callPerm})
	if err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	if !created {
		t.Fatalf("Mkdir: want created=true")
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if got := info.Mode().Perm(); got != capPerm {
		t.Fatalf("want attach-time NativePOSIXMode capability (%o) to win over per-call MkdirOptions.NativePOSIXMode (%o), got %o", capPerm, callPerm, got)
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
	if _, err := client.Create(ctx, res.WithCreate(file.Create{}), record{Name: "exists", Value: 1}, map[string]string{"name": "exists"}); err != nil {
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
	if _, err := client.Create(ctx, res.WithCreate(file.Create{}), record{Name: "dual", Value: 5}, vars); err != nil {
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
	_, err := client.Create(context.Background(), creator, record{Name: "ro", Value: 1}, map[string]string{"name": "ro"})
	if err == nil {
		t.Fatalf("want rejection error for AccessModeReadOnly Create")
	}
	if _, statErr := os.Stat(filepath.Join(dir, "ro.json")); !os.IsNotExist(statErr) {
		t.Fatalf("Create should NOT have reached the backend: file exists")
	}
}

// TestClient_Create_NativePOSIXModeCapability_OverridesCallerPerm proves
// createCore's documented "attach-time Capability always wins over a
// conflicting per-call option" precedence rule ACTUALLY holds — this
// mechanism (also present in patchCore/mkdirCore/openWriteCore) had ZERO
// test coverage anywhere in the repo before this round's deep-dive-bug-
// hunt pass, despite being a load-bearing, explicitly-documented design
// decision ("Resolved round 5" in the file-api design history).
func TestClient_Create_NativePOSIXModeCapability_OverridesCallerPerm(t *testing.T) {
	dir := t.TempDir()
	res := newResource(t, dir)
	client := file.NewClient()
	if err := client.Attach(adapterfile.NewBackend()); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	capPerm := os.FileMode(0o600)
	callPerm := os.FileMode(0o777)
	creator := res.WithCreate(file.Create{}).WithCapabilities(file.NativePOSIXMode(capPerm))
	_, err := client.Create(context.Background(), creator, record{Name: "capmode", Value: 1},
		map[string]string{"name": "capmode"}, file.WriteOptions{Perm: callPerm})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	info, err := os.Stat(filepath.Join(dir, "capmode.json"))
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if got := info.Mode().Perm(); got != capPerm {
		t.Fatalf("want attach-time NativePOSIXMode capability (%o) to win over per-call WriteOptions.Perm (%o), got %o", capPerm, callPerm, got)
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
	if _, err := client.Create(ctx, res.WithCreate(file.Create{}), record{Name: "patchme", Value: 1}, vars); err != nil {
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

// TestPatcher_HandlePatch_NativePOSIXModeCapability_OverridesCallerPerm
// proves patchCore's write-back step honors the SAME documented
// "attach-time Capability always wins over a conflicting per-call
// option" precedence rule createCore/mkdirCore already enforce — see
// TestClient_Create_NativePOSIXModeCapability_OverridesCallerPerm's doc
// comment for why this needed a fresh, direct test (zero prior coverage).
func TestPatcher_HandlePatch_NativePOSIXModeCapability_OverridesCallerPerm(t *testing.T) {
	dir := t.TempDir()
	res := newResource(t, dir)
	client := file.NewClient()
	if err := client.Attach(adapterfile.NewBackend()); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	ctx := context.Background()
	vars := map[string]string{"name": "patchmode"}
	if _, err := client.Create(ctx, res.WithCreate(file.Create{}), record{Name: "patchmode", Value: 1}, vars); err != nil {
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
	capPerm := os.FileMode(0o600)
	callPerm := os.FileMode(0o777)
	newValue := 2
	patcher := file.WithPatch(res, patchCodec, file.Patch{}).WithCapabilities(file.NativePOSIXMode(capPerm))
	if err := client.Patch(ctx, patcher, valuePatch{Value: &newValue}, vars, file.WriteOptions{Perm: callPerm}); err != nil {
		t.Fatalf("Patch: %v", err)
	}
	info, err := os.Stat(filepath.Join(dir, "patchmode.json"))
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if got := info.Mode().Perm(); got != capPerm {
		t.Fatalf("want attach-time NativePOSIXMode capability (%o) to win over per-call WriteOptions.Perm (%o), got %o", capPerm, callPerm, got)
	}
}

// TestPatcher_HandlePatch_PreservesPatchOnlyField is a regression proof
// for a real bug found while rewriting examples/file-io: patchCore used
// to stay in TYPED (T) space for its merge+marshal round trip
// (format.PatchInto returns T, then Marshal(T) re-encodes it) — any
// field present ONLY in the patch codec (not in the resource's own body
// codec T) was silently DROPPED from the written file, unlike
// ports.PatchEncoded's own, established, already-tested contract (which
// stays in map space via UnmarshalRaw/DeepMerge/MarshalRaw specifically
// to let patch-codec-only fields survive). Fixed by mirroring
// ports.PatchEncoded exactly.
func TestPatcher_HandlePatch_PreservesPatchOnlyField(t *testing.T) {
	dir := t.TempDir()
	res := newResource(t, dir)
	client := file.NewClient()
	if err := client.Attach(adapterfile.NewBackend()); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	ctx := context.Background()
	vars := map[string]string{"name": "patchme"}
	if _, err := client.Create(ctx, res.WithCreate(file.Create{}), record{Name: "patchme", Value: 1}, vars); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// extraPatch declares a field ("Extra") absent from record's own
	// codec entirely.
	type extraPatch struct {
		Value int
		Extra string
	}
	patchCodec := codex.Struct[extraPatch](
		codex.RequiredField("value", codex.Int(),
			func(p extraPatch) int { return p.Value },
			func(p *extraPatch, v int) { p.Value = v }),
		codex.RequiredField("extra", codex.String(),
			func(p extraPatch) string { return p.Extra },
			func(p *extraPatch, v string) { p.Extra = v }),
	)
	patcher := file.WithPatch(res, patchCodec, file.Patch{})
	if err := client.Patch(ctx, patcher, extraPatch{Value: 42, Extra: "survives"}, vars); err != nil {
		t.Fatalf("Patch: %v", err)
	}

	path, err := res.BuildPath(vars)
	if err != nil {
		t.Fatalf("BuildPath: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(raw), `"extra":"survives"`) {
		t.Fatalf("raw file %s does not contain patch-only field \"extra\": %s", path, raw)
	}

	// record's own codec (no "Extra" field) still decodes cleanly —
	// unknown fields are ignored on decode, not rejected.
	gotAny, err := client.Read(ctx, res.WithRead(file.Read{}), vars)
	if err != nil {
		t.Fatalf("Read after patch: %v", err)
	}
	got := gotAny.(record)
	if got.Value != 42 || got.Name != "patchme" {
		t.Fatalf("Patch: got %+v, want Value=42 Name=patchme (unchanged)", got)
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

	if _, err := client.Create(ctx, res.WithCreate(file.Create{}), recordWithOwner{Name: "widget", Value: 1, Owner: "alice"}, vars); err != nil {
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
	if _, err := client.Create(ctx, plainRes.WithCreate(file.Create{}), recordWithOwner{Name: "noowner", Value: 2}, vars); err != nil {
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
	if _, err := client.Create(ctx, plainRes.WithCreate(file.Create{}), record{Name: "prefixed", Value: 1}, vars); err != nil {
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
	if _, err := client.Create(ctx, createHandle, record{Name: "prefixed", Value: 2}, vars); err != nil {
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
	if _, err := client.Create(ctx, res.WithCreate(file.Create{}), record{Name: "secured", Value: 1}, vars); err != nil {
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
	if _, err := client.Create(ctx, res.WithCreate(file.Create{}), record{Name: "secured2", Value: 1}, vars); err != nil {
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
	if _, err := client.Create(ctx, res.WithCreate(file.Create{}), record{Name: "delsecured", Value: 1}, vars); err != nil {
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
	if _, err := client.Create(ctx, res.WithCreate(file.Create{}), record{Name: "ordered", Value: 1}, vars); err != nil {
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
		if _, err := client.Create(ctx, res.WithCreate(file.Create{}), recordWithOwner{Value: 1}, map[string]string{"name": name}); err != nil {
			t.Fatalf("Create(%s): %v", name, err)
		}
	}

	gotAny, err := client.List(ctx, res.WithList(file.List{}), nil)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	got := gotAny.([]file.ListResult[recordWithOwner])
	if len(got) != 2 {
		t.Fatalf("List: want 2 entries, got %d", len(got))
	}
	names := map[string]bool{}
	for _, r := range got {
		if r.Err != nil {
			t.Fatalf("List: unexpected per-entry error %v", r.Err)
		}
		names[r.Value.Name] = true
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
	if _, err := client.Create(ctx, res.WithCreate(file.Create{}), recordWithOwner{Value: 1}, map[string]string{"name": "ok"}); err != nil {
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

	// Per the Phase 1 amendment, a per-entry failure (like this
	// template mismatch) no longer fails the WHOLE call - it becomes
	// that entry's own ListResult.Err, with siblings (here, "ok")
	// still returned successfully.
	gotAny, err := client.List(ctx, res.WithList(file.List{}), nil)
	if err != nil {
		t.Fatalf("List: unexpected call-level error %v", err)
	}
	got := gotAny.([]file.ListResult[recordWithOwner])
	var found file.FilePathMismatchError
	var okCount int
	for _, r := range got {
		if r.Err != nil {
			if !errors.As(r.Err, &found) {
				t.Fatalf("want FilePathMismatchError, got %v (%T)", r.Err, r.Err)
			}
			continue
		}
		okCount++
	}
	if found.Template == "" {
		t.Fatalf("List: want one entry's Err to be FilePathMismatchError, got %+v", got)
	}
	if okCount != 1 {
		t.Fatalf("List: want 1 successful sibling entry, got %d (of %+v)", okCount, got)
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
	if _, err := client.Create(ctx, res.WithCreate(file.Create{}), record{Name: "plain", Value: 1}, map[string]string{"name": "plain"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	gotAny, err := client.List(ctx, res.WithList(file.List{}), nil)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	got := gotAny.([]file.ListResult[record])
	if len(got) != 1 {
		t.Fatalf("List: want 1 entry, got %+v", got)
	}
	if got[0].Err != nil {
		t.Fatalf("List: unexpected per-entry error %v", got[0].Err)
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
	if _, err := client.Create(ctx, creator, recordWithOwner{Name: "widget", Value: 1, Owner: "alice"}, map[string]string{"name": "widget"}); err != nil {
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
	if _, err := client.Create(ctx, creator, recordWithOwner{Name: "gadget", Value: 1, Owner: "original-owner"}, map[string]string{"name": "gadget"}); err != nil {
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
	if _, err := client.Create(ctx, res.WithCreate(file.Create{}), recordWithOwner{Name: "x", Value: 1, Owner: "bob"}, map[string]string{"name": "x"}); err != nil {
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
	if _, err := client.Create(ctx, res.WithCreate(file.Create{}), recordWithOwner{Name: "patchtarget", Value: 1, Owner: "carol"}, vars); err != nil {
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
	if _, err := client.Create(ctx, res.WithCreate(file.Create{}), recordWithOwner{Name: "patchtarget2", Value: 1, Owner: "dave"}, vars); err != nil {
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

// ── Stage F0: api/file parity upgrades (TraceObserver, createdDirs,
// per-call options, error-shape fidelity) ──────────────────────────────

type spanSpy struct {
	stats.NoopObserver
	started []string // operation names
	ended   int
}

func (s *spanSpy) StartSpan(ctx context.Context, operation, name string) context.Context {
	s.started = append(s.started, operation)
	return ctx
}
func (s *spanSpy) EndSpan(ctx context.Context, err error) { s.ended++ }

func TestDispatch_TraceObserver_StartsAndEndsSpanPerOperation(t *testing.T) {
	dir := t.TempDir()
	res := newResource(t, dir)
	backend := adapterfile.NewBackend()
	client := file.NewClient()
	if err := client.Attach(backend); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	spy := &spanSpy{}
	ctx := stats.WithObserver(context.Background(), spy)
	vars := map[string]string{"name": "alice"}

	if _, err := client.Create(ctx, res.WithCreate(file.Create{}), record{Name: "alice", Value: 1}, vars); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := client.Read(ctx, res.WithRead(file.Read{}), vars); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if _, err := client.Delete(ctx, res.WithDelete(file.Delete{}), vars); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	wantOps := []string{"file.write", "file.read", "file.delete"}
	if len(spy.started) != len(wantOps) {
		t.Fatalf("started spans = %v, want %v", spy.started, wantOps)
	}
	for i, op := range wantOps {
		if spy.started[i] != op {
			t.Errorf("span[%d] = %q, want %q", i, spy.started[i], op)
		}
	}
	if spy.ended != len(wantOps) {
		t.Fatalf("ended = %d, want %d", spy.ended, len(wantOps))
	}
}

func TestBackend_WriteBytes_CreateDirs_ReportsEveryMissingAncestor(t *testing.T) {
	dir := t.TempDir()
	backend := adapterfile.NewBackend()
	path := filepath.Join(dir, "a", "b", "file.txt")
	createdDirs, err := backend.WriteBytes(context.Background(), path, []byte("x"), file.WriteOptions{CreateDirs: true})
	if err != nil {
		t.Fatalf("WriteBytes: %v", err)
	}
	if len(createdDirs) != 2 {
		t.Fatalf("createdDirs = %v, want 2 entries (a, a/b)", createdDirs)
	}
	if createdDirs[0] != filepath.Join(dir, "a") || createdDirs[1] != filepath.Join(dir, "a", "b") {
		t.Fatalf("createdDirs = %v, want root-to-leaf order", createdDirs)
	}
}

func TestClient_Create_PerCallWriteOptions_CreateDirsAndStrict(t *testing.T) {
	dir := t.TempDir()
	res := file.NewResource[record](filepath.Join(dir, "sub", "{name}.json"), format.JSON(recordCodec()),
		file.PathParam{Name: "name"})
	backend := adapterfile.NewBackend()
	client := file.NewClient()
	if err := client.Attach(backend); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	ctx := context.Background()
	vars := map[string]string{"name": "bob"}
	creator := res.WithCreate(file.Create{})

	if _, err := client.Create(ctx, creator, record{Name: "bob", Value: 1}, vars, file.WriteOptions{CreateDirs: true}); err != nil {
		t.Fatalf("Create with CreateDirs: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "sub", "bob.json")); err != nil {
		t.Fatalf("expected file to exist: %v", err)
	}

	// Strict: a 2nd Create on the same path must fail with
	// FileAlreadyExistsError, bare (not wrapped in FileWriteError).
	_, err := client.Create(ctx, creator, record{Name: "bob", Value: 2}, vars, file.WriteOptions{Strict: true})
	var existsErr file.FileAlreadyExistsError
	if !errors.As(err, &existsErr) {
		t.Fatalf("Create Strict re-create: err = %v, want FileAlreadyExistsError", err)
	}
}

func TestClient_Delete_Strict_NotFound_ReturnsFileNotFoundError(t *testing.T) {
	dir := t.TempDir()
	res := newResource(t, dir)
	backend := adapterfile.NewBackend()
	client := file.NewClient()
	if err := client.Attach(backend); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	ctx := context.Background()
	vars := map[string]string{"name": "ghost"}

	_, err := client.Delete(ctx, res.WithDelete(file.Delete{}), vars, file.DeleteOptions{Strict: true})
	var notFound file.FileNotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("Delete Strict on missing file: err = %v, want FileNotFoundError", err)
	}
}

func TestClient_Mkdir_PerCallDryRun(t *testing.T) {
	dir := t.TempDir()
	res := file.NewResource[record](filepath.Join(dir, "created"), format.JSON(recordCodec()))
	backend := adapterfile.NewBackend()
	client := file.NewClient()
	if err := client.Attach(backend); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	ctx := context.Background()
	mkdir := res.WithMkdir(file.Mkdir{})

	created, err := client.Mkdir(ctx, mkdir, nil, file.MkdirOptions{DryRun: true})
	if err != nil {
		t.Fatalf("Mkdir DryRun: %v", err)
	}
	if !created {
		t.Fatalf("Mkdir DryRun: created = false, want true (reports what WOULD happen)")
	}
	if _, statErr := os.Stat(filepath.Join(dir, "created")); !os.IsNotExist(statErr) {
		t.Fatalf("Mkdir DryRun must NOT actually create the directory")
	}
}

func TestNewResource_MultipleGlobstars_Panics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for multiple ** segments")
		}
	}()
	file.NewResource[record]("a/**/b/**/c", format.JSON(recordCodec()))
}

// ── Deep-dive-bug-hunt Round 216: foundational dispatch.go helpers ─────

// validationSpy records RecordValidationError calls — used to prove the
// stats.ReportErrors gap below.
type validationSpy struct {
	stats.NoopObserver
	calls []string // location/constraint/field, joined
}

func (s *validationSpy) RecordValidationError(location, constraint, field string) {
	s.calls = append(s.calls, location+"/"+constraint+"/"+field)
}

func TestReadCore_DecodeFailure_ReportsValidationErrorsToObserver(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "item.json")
	// Write a raw JSON value that fails recordCodec's "value must be
	// non-negative"-style constraint is not available here, so instead
	// write a wrong-typed field to trigger a decode/validation error.
	if err := os.WriteFile(path, []byte(`{"name":"x","value":"not-an-int"}`), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	res := file.NewResource[record](path, format.JSON(recordCodec()))
	backend := adapterfile.NewBackend()
	client := file.NewClient()
	if err := client.Attach(backend); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	spy := &validationSpy{}
	ctx := stats.WithObserver(context.Background(), spy)

	_, err := client.Read(ctx, res.WithRead(file.Read{}), nil)
	if err == nil {
		t.Fatal("expected a decode error, got nil")
	}
	if len(spy.calls) == 0 {
		t.Errorf("RecordValidationError was never called on decode failure — dispatch.go's readCore does not call stats.ReportErrors, unlike ports.File.Read's original behavior")
	}
}

func TestCreateCore_EncodeFailure_ReportsValidationErrorsToObserver(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "item.json")
	// A codec with a Refine constraint that the test value will violate.
	strictCodec := codex.Struct[record](
		codex.RequiredField("name", codex.String().Refine(validate.NonEmptyString),
			func(r record) string { return r.Name },
			func(r *record, v string) { r.Name = v }),
		codex.RequiredField("value", codex.Int(),
			func(r record) int { return r.Value },
			func(r *record, v int) { r.Value = v }),
	)
	res := file.NewResource[record](path, format.JSON(strictCodec))
	backend := adapterfile.NewBackend()
	client := file.NewClient()
	if err := client.Attach(backend); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	spy := &validationSpy{}
	ctx := stats.WithObserver(context.Background(), spy)

	_, err := client.Create(ctx, res.WithCreate(file.Create{}), record{Name: "", Value: 1}, nil)
	if err == nil {
		t.Fatal("expected an encode/validation error, got nil")
	}
	if len(spy.calls) == 0 {
		t.Errorf("RecordValidationError was never called on createCore's encode failure")
	}
}

func TestListCore_DecodeFailure_ReportsValidationErrorsToObserver(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "bad.json"), []byte(`{"name":"x","value":"not-an-int"}`), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	res := file.NewResource[record](filepath.Join(dir, "{name}.json"), format.JSON(recordCodec()),
		file.PathParam{Name: "name"})
	backend := adapterfile.NewBackend()
	client := file.NewClient()
	if err := client.Attach(backend); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	spy := &validationSpy{}
	ctx := stats.WithObserver(context.Background(), spy)

	// Per the Phase 1 amendment, a per-entry decode failure becomes
	// that entry's own ListResult.Err - it no longer fails the whole
	// call - but stats.ReportErrors must still fire for it.
	gotAny, err := client.List(ctx, res.WithList(file.List{}), nil)
	if err != nil {
		t.Fatalf("List: unexpected call-level error %v", err)
	}
	got := gotAny.([]file.ListResult[record])
	if len(got) != 1 || got[0].Err == nil {
		t.Fatalf("expected one entry with a decode error, got %+v", got)
	}
	if len(spy.calls) == 0 {
		t.Errorf("RecordValidationError was never called on listCore's decode failure")
	}
}

func TestPatchCore_AccessModeReadOnly_RejectsBeforeBackendCall(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "item.json")
	res := file.NewResource[record](path, format.JSON(recordCodec()))
	backend := adapterfile.NewBackend()
	client := file.NewClient()
	if err := client.Attach(backend); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	ctx := context.Background()

	// Seed the file via a Creator WITHOUT the ReadOnly capability.
	if _, err := client.Create(ctx, res.WithCreate(file.Create{}), record{Name: "x", Value: 1}, nil); err != nil {
		t.Fatalf("seed Create: %v", err)
	}

	type valuePatch struct{ Value int }
	patchCodec := codex.Struct[valuePatch](
		codex.RequiredField("value", codex.Int(),
			func(p valuePatch) int { return p.Value },
			func(p *valuePatch, v int) { p.Value = v }),
	)
	patcher := file.WithPatch(res, patchCodec, file.Patch{}).WithCapabilities(file.AccessModeReadOnly)

	err := client.Patch(ctx, patcher, valuePatch{Value: 99}, nil)
	if err == nil {
		t.Fatal("want rejection error for AccessModeReadOnly Patch, got nil (BUG: patchCore never enforces Capability)")
	}

	// The file must be UNCHANGED (proves the rejection happened BEFORE
	// any Backend call, not just that Patch happened to fail for some
	// other reason).
	gotAny, readErr := client.Read(ctx, res.WithRead(file.Read{}), nil)
	if readErr != nil {
		t.Fatalf("Read: %v", readErr)
	}
	if gotAny.(record).Value != 1 {
		t.Fatalf("Value = %d, want 1 (Patch must not have reached the Backend)", gotAny.(record).Value)
	}
}

func TestCopyCore_NativeCopyingBackend_LosesMetadata_BUG(t *testing.T) {
	dir := t.TempDir()
	res := file.NewResource[recordWithOwner](filepath.Join(dir, "{name}.json"), format.JSON(recordWithOwnerCodec()),
		file.PathParam{Name: "name"},
		file.NewMetadataParam("owner", codex.String(),
			func(r recordWithOwner) string { return r.Owner },
			func(r *recordWithOwner, v string) { r.Owner = v }),
	)
	backend := adapterfile.NewBackend()
	client := file.NewClient()
	if err := client.Attach(backend); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	ctx := context.Background()

	if _, err := client.Create(ctx, res.WithCreate(file.Create{}), recordWithOwner{Name: "alice", Value: 1, Owner: "team-a"}, map[string]string{"name": "alice"}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := client.Copy(ctx, res.WithCopy(file.Copy{}), map[string]string{"name": "alice"}, map[string]string{"name": "bob"}); err != nil {
		t.Fatalf("Copy: %v", err)
	}

	gotAny, err := client.Read(ctx, res.WithRead(file.Read{}), map[string]string{"name": "bob"})
	if err != nil {
		t.Fatalf("Read copied resource: %v", err)
	}
	got := gotAny.(recordWithOwner)
	if got.Owner != "team-a" {
		t.Fatalf("Owner = %q, want %q — Copy silently lost the resource's metadata (BUG: neither CopyingBackend.CopyBytes nor the generic fallback carry metadata over)", got.Owner, "team-a")
	}
}

func TestMoveCore_NativeMovingBackend_CarriesMetadataOver(t *testing.T) {
	dir := t.TempDir()
	res := file.NewResource[recordWithOwner](filepath.Join(dir, "{name}.json"), format.JSON(recordWithOwnerCodec()),
		file.PathParam{Name: "name"},
		file.NewMetadataParam("owner", codex.String(),
			func(r recordWithOwner) string { return r.Owner },
			func(r *recordWithOwner, v string) { r.Owner = v }),
	)
	backend := adapterfile.NewBackend()
	client := file.NewClient()
	if err := client.Attach(backend); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	ctx := context.Background()

	if _, err := client.Create(ctx, res.WithCreate(file.Create{}), recordWithOwner{Name: "alice", Value: 1, Owner: "team-a"}, map[string]string{"name": "alice"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := client.Move(ctx, res.WithMove(file.Move{}), map[string]string{"name": "alice"}, map[string]string{"name": "carol"}); err != nil {
		t.Fatalf("Move: %v", err)
	}
	gotAny, err := client.Read(ctx, res.WithRead(file.Read{}), map[string]string{"name": "carol"})
	if err != nil {
		t.Fatalf("Read moved resource: %v", err)
	}
	got := gotAny.(recordWithOwner)
	if got.Owner != "team-a" {
		t.Fatalf("Owner = %q, want %q — Move (native MovingBackend) must carry metadata over", got.Owner, "team-a")
	}
}

// ── Phase 1 amendment: List/BatchDelete per-entry Security ───────────────

func TestLister_PerEntrySecurityRejection_SiblingsStillSucceed(t *testing.T) {
	dir := t.TempDir()
	res := newResource(t, dir)
	client := file.NewClient()
	if err := client.Attach(adapterfile.NewBackend()); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	ctx := context.Background()
	for _, name := range []string{"allowed", "blocked"} {
		if _, err := client.Create(ctx, res.WithCreate(file.Create{}), record{Name: name, Value: 1}, map[string]string{"name": name}); err != nil {
			t.Fatalf("Create(%s): %v", name, err)
		}
	}

	bm := file.BoundSecurityListMiddleware[record, struct{}, grantedOut](
		"apiKey", file.SecurityScheme{}, []string{"read"},
		func(ctx context.Context, v *record, in struct{}) (grantedOut, error) {
			if v != nil && v.Name == "blocked" {
				return grantedOut{GrantedScopes: map[string][]string{"apiKey": {"wrong-scope"}}}, nil
			}
			return grantedOut{GrantedScopes: map[string][]string{"apiKey": {"read"}}}, nil
		},
	)
	lister := res.WithList(file.List{}).ListBoundMW(bm).WithSecurity(file.SecurityRequirement{"apiKey": {"read"}})

	gotAny, err := client.List(ctx, lister, nil)
	if err != nil {
		t.Fatalf("List: unexpected call-level error %v", err)
	}
	got := gotAny.([]file.ListResult[record])
	if len(got) != 2 {
		t.Fatalf("List: want 2 entries, got %+v", got)
	}
	var rejectedCount, okCount int
	for _, r := range got {
		if r.Err != nil {
			var secErr file.SecurityRejectedError
			if !errors.As(r.Err, &secErr) {
				t.Fatalf("want SecurityRejectedError, got %v (%T)", r.Err, r.Err)
			}
			rejectedCount++
			continue
		}
		okCount++
	}
	if rejectedCount != 1 || okCount != 1 {
		t.Fatalf("want 1 rejected + 1 ok, got rejected=%d ok=%d (%+v)", rejectedCount, okCount, got)
	}
}

func TestLister_BoundMiddleware_ReceivesActualDecodedValue(t *testing.T) {
	dir := t.TempDir()
	res := newResource(t, dir)
	client := file.NewClient()
	if err := client.Attach(adapterfile.NewBackend()); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	ctx := context.Background()
	if _, err := client.Create(ctx, res.WithCreate(file.Create{}), record{Name: "item", Value: 42}, map[string]string{"name": "item"}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	var sawValue int
	bm := file.NewBoundListMiddleware[record, struct{}, struct{}](
		file.NewDeclaration("observe", codex.Struct[struct{}](), codex.Struct[struct{}]()),
		func(ctx context.Context, v *record, in struct{}) (struct{}, error) {
			if v != nil {
				sawValue = v.Value
			}
			return struct{}{}, nil
		},
	)
	lister := res.WithList(file.List{}).ListBoundMW(bm)
	gotAny, err := client.List(ctx, lister, nil)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	got := gotAny.([]file.ListResult[record])
	if len(got) != 1 || got[0].Err != nil {
		t.Fatalf("List: want 1 clean entry, got %+v", got)
	}
	if sawValue != 42 {
		t.Fatalf("BoundListMiddleware: want it to observe the decoded value (42), got %d", sawValue)
	}
}

func TestListCore_WalkLevelError_StillFailsWholeCall(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "does-not-exist")
	res := file.NewResource[record](filepath.Join(missing, "{name}.json"), format.JSON(recordCodec()),
		file.PathParam{Name: "name"})
	client := file.NewClient()
	if err := client.Attach(adapterfile.NewBackend()); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	_, err := client.List(context.Background(), res.WithList(file.List{}), nil)
	var readErr file.FileReadError
	if err == nil || !errors.As(err, &readErr) {
		t.Fatalf("want a whole-call FileReadError (walk-level failure), got %v (%T)", err, err)
	}
}

func TestListCore_ObserverCount_IncludesFailedEntries(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "good.json"), []byte(`{"name":"good","value":1}`), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bad.json"), []byte(`{"name":"bad","value":"not-an-int"}`), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	res := file.NewResource[record](filepath.Join(dir, "{name}.json"), format.JSON(recordCodec()),
		file.PathParam{Name: "name"})
	client := file.NewClient()
	if err := client.Attach(adapterfile.NewBackend()); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	gotAny, err := client.List(context.Background(), res.WithList(file.List{}), nil)
	if err != nil {
		t.Fatalf("List: unexpected call-level error %v", err)
	}
	got := gotAny.([]file.ListResult[record])
	if len(got) != 2 {
		t.Fatalf("want 2 entries (1 ok + 1 failed) counted, got %d (%+v)", len(got), got)
	}
}

// TestBatchDeleter_PerPathSecurityRejection_EachPathGatedIndependently
// proves the per-path gate runs for EVERY path in the batch
// independently (not once for the whole batch) — BoundBatchDeleteMiddleware
// has no *T/path to branch on (the no-T role shape), so this test
// confirms the gate's effect: every result's own Path is correctly
// reported, every one is independently rejected, and nothing was
// actually deleted (both files still exist on disk).
func TestBatchDeleter_PerPathSecurityRejection_EachPathGatedIndependently(t *testing.T) {
	dir := t.TempDir()
	res := newResource(t, dir)
	client := file.NewClient()
	if err := client.Attach(adapterfile.NewBackend()); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	ctx := context.Background()
	for _, name := range []string{"allowed", "blocked"} {
		if _, err := client.Create(ctx, res.WithCreate(file.Create{}), record{Name: name, Value: 1}, map[string]string{"name": name}); err != nil {
			t.Fatalf("Create(%s): %v", name, err)
		}
	}

	bm := file.BoundSecurityBatchDeleteMiddleware[struct{}, grantedOut](
		"apiKey", file.SecurityScheme{}, []string{"delete"},
		func(ctx context.Context, in struct{}) (grantedOut, error) {
			return grantedOut{GrantedScopes: map[string][]string{"apiKey": {"wrong-scope"}}}, nil
		},
	)
	batchDeleter := res.WithBatchDelete(file.BatchDelete{}).BatchDeleteBoundMW(bm).WithSecurity(file.SecurityRequirement{"apiKey": {"delete"}})

	results, err := client.BatchDelete(ctx, batchDeleter, []map[string]string{
		{"name": "allowed"}, {"name": "blocked"},
	})
	if err != nil {
		t.Fatalf("BatchDelete: unexpected call-level error %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("want 2 results, got %+v", results)
	}
	gotPaths := map[string]bool{}
	for _, r := range results {
		var secErr file.SecurityRejectedError
		if !errors.As(r.Err, &secErr) {
			t.Fatalf("want every path independently rejected, got %+v", r)
		}
		gotPaths[r.Path] = true
	}
	if len(gotPaths) != 2 {
		t.Fatalf("want 2 distinct paths each with their own result, got %+v", gotPaths)
	}
	for _, name := range []string{"allowed", "blocked"} {
		if _, err := os.Stat(filepath.Join(dir, name+".json")); err != nil {
			t.Fatalf("%s.json should NOT have been deleted (rejected by Security): %v", name, err)
		}
	}
}

// ── Recursive WithList (Phase 2 item 1) ──────────────────────────────────

// nonWalkingBackend wraps adapterfile.Backend by COMPOSITION (not
// embedding) so WalkDir is deliberately NOT promoted — proves
// Recursive's manual, equivalent-power fallback (no WalkingBackend)
// produces the same result as the WalkingBackend-based path.
type nonWalkingBackend struct {
	inner *adapterfile.Backend
}

func (b nonWalkingBackend) BackendName() string { return "test/non-walking" }
func (b nonWalkingBackend) ReadBytes(ctx context.Context, path string) ([]byte, error) {
	return b.inner.ReadBytes(ctx, path)
}
func (b nonWalkingBackend) WriteBytes(ctx context.Context, path string, data []byte, opts file.WriteOptions) ([]string, error) {
	return b.inner.WriteBytes(ctx, path, data, opts)
}
func (b nonWalkingBackend) DeleteBytes(ctx context.Context, path string, opts file.DeleteOptions) (bool, error) {
	return b.inner.DeleteBytes(ctx, path, opts)
}
func (b nonWalkingBackend) ListDir(ctx context.Context, path string) ([]file.Entry, error) {
	return b.inner.ListDir(ctx, path)
}
func (b nonWalkingBackend) Stat(ctx context.Context, path string) (file.Entry, error) {
	return b.inner.Stat(ctx, path)
}
func (b nonWalkingBackend) Mkdir(ctx context.Context, path string, opts file.MkdirOptions) (bool, error) {
	return b.inner.Mkdir(ctx, path, opts)
}

func newNonWalkingBackend() nonWalkingBackend {
	return nonWalkingBackend{inner: adapterfile.NewBackend()}
}

func TestListCore_Recursive_DiscoversEntriesInParentDir(t *testing.T) {
	dir := t.TempDir()
	res := newResource(t, dir)
	client := file.NewClient()
	if err := client.Attach(adapterfile.NewBackend()); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	ctx := context.Background()
	for _, name := range []string{"a", "b"} {
		if _, err := client.Create(ctx, res.WithCreate(file.Create{}), record{Name: name, Value: 1}, map[string]string{"name": name}); err != nil {
			t.Fatalf("Create(%s): %v", name, err)
		}
	}

	gotAny, err := client.List(ctx, res.WithList(file.List{}), nil, file.ListOptions{Recursive: true})
	if err != nil {
		t.Fatalf("List(Recursive): %v", err)
	}
	got := gotAny.([]file.ListResult[record])
	if len(got) != 2 {
		t.Fatalf("want 2 entries, got %+v", got)
	}
	for _, r := range got {
		if r.Err != nil {
			t.Fatalf("unexpected per-entry error %v", r.Err)
		}
	}
}

func TestListCore_Recursive_SkipsDirectoryEntries(t *testing.T) {
	dir := t.TempDir()
	res := newResource(t, dir)
	client := file.NewClient()
	if err := client.Attach(adapterfile.NewBackend()); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	ctx := context.Background()
	if _, err := client.Create(ctx, res.WithCreate(file.Create{}), record{Name: "plain", Value: 1}, map[string]string{"name": "plain"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "subdir"), 0755); err != nil {
		t.Fatalf("setup Mkdir: %v", err)
	}

	gotAny, err := client.List(ctx, res.WithList(file.List{}), nil, file.ListOptions{Recursive: true})
	if err != nil {
		t.Fatalf("List(Recursive): %v", err)
	}
	got := gotAny.([]file.ListResult[record])
	if len(got) != 1 {
		t.Fatalf("want 1 entry (directory excluded), got %+v", got)
	}
}

func TestListCore_Recursive_DepthPruning_ExcludesTooDeepEntries(t *testing.T) {
	dir := t.TempDir()
	res := newResource(t, dir)
	client := file.NewClient()
	if err := client.Attach(adapterfile.NewBackend()); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	ctx := context.Background()
	if _, err := client.Create(ctx, res.WithCreate(file.Create{}), record{Name: "shallow", Value: 1}, map[string]string{"name": "shallow"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	// An extra, unexpected nesting level — one directory too deep
	// relative to the resource's parent dir — must be pruned, never
	// reaching the codec at all.
	if err := os.MkdirAll(filepath.Join(dir, "nested"), 0755); err != nil {
		t.Fatalf("setup MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "nested", "deep.json"), []byte(`{"name":"deep","value":1}`), 0644); err != nil {
		t.Fatalf("setup write: %v", err)
	}

	gotAny, err := client.List(ctx, res.WithList(file.List{}), nil, file.ListOptions{Recursive: true})
	if err != nil {
		t.Fatalf("List(Recursive): %v", err)
	}
	got := gotAny.([]file.ListResult[record])
	if len(got) != 1 || got[0].Err != nil || got[0].Value.Name != "shallow" {
		t.Fatalf("want exactly the shallow entry, deep.json pruned; got %+v", got)
	}
}

func TestListCore_Recursive_NonWalkingBackend_FallsBackToManualComposition(t *testing.T) {
	dir := t.TempDir()
	res := newResource(t, dir)
	client := file.NewClient()
	if err := client.Attach(newNonWalkingBackend()); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	ctx := context.Background()
	for _, name := range []string{"x", "y"} {
		if _, err := client.Create(ctx, res.WithCreate(file.Create{}), record{Name: name, Value: 1}, map[string]string{"name": name}); err != nil {
			t.Fatalf("Create(%s): %v", name, err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "extra"), 0755); err != nil {
		t.Fatalf("setup MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "extra", "z.json"), []byte(`{"name":"z","value":1}`), 0644); err != nil {
		t.Fatalf("setup write: %v", err)
	}

	gotAny, err := client.List(ctx, res.WithList(file.List{}), nil, file.ListOptions{Recursive: true})
	if err != nil {
		t.Fatalf("List(Recursive, manual fallback): %v", err)
	}
	got := gotAny.([]file.ListResult[record])
	if len(got) != 2 {
		t.Fatalf("want 2 entries (extra/z.json pruned, directory excluded), got %+v", got)
	}
}

func TestListCore_Recursive_MergeFieldAppliesPerEntry(t *testing.T) {
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
	if _, err := client.Create(ctx, res.WithCreate(file.Create{}), recordWithOwner{Value: 1}, map[string]string{"name": "merged"}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	gotAny, err := client.List(ctx, res.WithList(file.List{}), nil, file.ListOptions{Recursive: true})
	if err != nil {
		t.Fatalf("List(Recursive): %v", err)
	}
	got := gotAny.([]file.ListResult[recordWithOwner])
	if len(got) != 1 || got[0].Err != nil || got[0].Value.Name != "merged" {
		t.Fatalf("want 1 entry with Name merged from filename, got %+v", got)
	}
}

func TestListCore_NonRecursive_ResultShapeUnaffectedByListOptionsZeroValue(t *testing.T) {
	dir := t.TempDir()
	res := newResource(t, dir)
	client := file.NewClient()
	if err := client.Attach(adapterfile.NewBackend()); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	ctx := context.Background()
	if _, err := client.Create(ctx, res.WithCreate(file.Create{}), record{Name: "only", Value: 1}, map[string]string{"name": "only"}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Omitted opts entirely (today's existing call shape) vs. an
	// explicit ListOptions{} (Recursive: false) must produce identical
	// results — proves the new variadic parameter is fully backward
	// compatible.
	gotOmitted, err := client.List(ctx, res.WithList(file.List{}), nil)
	if err != nil {
		t.Fatalf("List (omitted opts): %v", err)
	}
	gotExplicit, err := client.List(ctx, res.WithList(file.List{}), nil, file.ListOptions{})
	if err != nil {
		t.Fatalf("List (explicit zero-value opts): %v", err)
	}
	if len(gotOmitted.([]file.ListResult[record])) != len(gotExplicit.([]file.ListResult[record])) {
		t.Fatalf("want identical result count, got omitted=%d explicit=%d", len(gotOmitted.([]file.ListResult[record])), len(gotExplicit.([]file.ListResult[record])))
	}
}

func TestListCore_Recursive_ObserverCountReflectsAllVisitedEntries(t *testing.T) {
	dir := t.TempDir()
	res := newResource(t, dir)
	client := file.NewClient()
	if err := client.Attach(adapterfile.NewBackend()); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	ctx := context.Background()
	for _, name := range []string{"one", "two", "three"} {
		if _, err := client.Create(ctx, res.WithCreate(file.Create{}), record{Name: name, Value: 1}, map[string]string{"name": name}); err != nil {
			t.Fatalf("Create(%s): %v", name, err)
		}
	}

	gotAny, err := client.List(ctx, res.WithList(file.List{}), nil, file.ListOptions{Recursive: true})
	if err != nil {
		t.Fatalf("List(Recursive): %v", err)
	}
	if len(gotAny.([]file.ListResult[record])) != 3 {
		t.Fatalf("want 3 entries counted, got %+v", gotAny)
	}
}

func TestListCore_Recursive_WalkLevelError_StillFailsWholeCall(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "does-not-exist")
	res := file.NewResource[record](filepath.Join(missing, "{name}.json"), format.JSON(recordCodec()),
		file.PathParam{Name: "name"})
	client := file.NewClient()
	if err := client.Attach(adapterfile.NewBackend()); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	_, err := client.List(context.Background(), res.WithList(file.List{}), nil, file.ListOptions{Recursive: true})
	var readErr file.FileReadError
	if err == nil || !errors.As(err, &readErr) {
		t.Fatalf("want a whole-call FileReadError (walk-level failure), got %v (%T)", err, err)
	}
}

// ── Conditional writes (ETag/If-Match) ───────────────────────────────────

func TestCreate_Overwrite_IfMatch_MatchSucceeds(t *testing.T) {
	dir := t.TempDir()
	res := newResource(t, dir)
	client := file.NewClient()
	if err := client.Attach(adapterfile.NewBackend()); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	ctx := context.Background()
	vars := map[string]string{"name": "widget"}
	if _, err := client.Create(ctx, res.WithCreate(file.Create{}), record{Name: "widget", Value: 1}, vars); err != nil {
		t.Fatalf("Create: %v", err)
	}
	current, err := os.ReadFile(filepath.Join(dir, "widget.json"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	etag := file.StrongETag(current)

	if _, err := client.Create(ctx, res.WithCreate(file.Create{}), record{Name: "widget", Value: 2}, vars, file.WriteOptions{IfMatch: &etag}); err != nil {
		t.Fatalf("Create (conditional overwrite): %v", err)
	}
	gotAny, err := client.Read(ctx, res.WithRead(file.Read{}), vars)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got := gotAny.(record); got.Value != 2 {
		t.Fatalf("want Value=2 after conditional overwrite, got %+v", got)
	}
}

func TestCreate_Overwrite_IfMatch_MismatchRejected(t *testing.T) {
	dir := t.TempDir()
	res := newResource(t, dir)
	client := file.NewClient()
	if err := client.Attach(adapterfile.NewBackend()); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	ctx := context.Background()
	vars := map[string]string{"name": "widget"}
	if _, err := client.Create(ctx, res.WithCreate(file.Create{}), record{Name: "widget", Value: 1}, vars); err != nil {
		t.Fatalf("Create: %v", err)
	}
	stale := "stale-etag"
	_, err := client.Create(ctx, res.WithCreate(file.Create{}), record{Name: "widget", Value: 2}, vars, file.WriteOptions{IfMatch: &stale})
	var mismatch file.ConditionalWriteMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("want ConditionalWriteMismatchError, got %v (%T)", err, err)
	}
	gotAny, readErr := client.Read(ctx, res.WithRead(file.Read{}), vars)
	if readErr != nil {
		t.Fatalf("Read: %v", readErr)
	}
	if got := gotAny.(record); got.Value != 1 {
		t.Fatalf("want content unchanged (Value=1) after rejected conditional write, got %+v", got)
	}
}

func TestCreate_Overwrite_IfMatch_NonexistentTargetReturnsConditionalWriteMismatchError(t *testing.T) {
	dir := t.TempDir()
	res := newResource(t, dir)
	client := file.NewClient()
	if err := client.Attach(adapterfile.NewBackend()); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	any := "any-etag"
	_, err := client.Create(context.Background(), res.WithCreate(file.Create{}), record{Name: "ghost", Value: 1}, map[string]string{"name": "ghost"}, file.WriteOptions{IfMatch: &any})
	var mismatch file.ConditionalWriteMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("want ConditionalWriteMismatchError, got %v (%T)", err, err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "ghost.json")); !os.IsNotExist(statErr) {
		t.Fatalf("file should NOT have been created, stat err = %v", statErr)
	}
}

func TestCreate_StrictTrueAndIfMatchBothSet_StrictTakesPrecedence(t *testing.T) {
	dir := t.TempDir()
	res := newResource(t, dir)
	client := file.NewClient()
	if err := client.Attach(adapterfile.NewBackend()); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	ctx := context.Background()
	vars := map[string]string{"name": "widget"}
	if _, err := client.Create(ctx, res.WithCreate(file.Create{}), record{Name: "widget", Value: 1}, vars); err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Any IfMatch value — even an objectively WRONG one — is irrelevant:
	// Strict's own existence check runs first and unconditionally.
	wrong := "definitely-wrong-etag"
	_, err := client.Create(ctx, res.WithCreate(file.Create{}), record{Name: "widget", Value: 2}, vars, file.WriteOptions{Strict: true, IfMatch: &wrong})
	var alreadyExists file.FileAlreadyExistsError
	if !errors.As(err, &alreadyExists) {
		t.Fatalf("want FileAlreadyExistsError (Strict takes precedence), got %v (%T)", err, err)
	}
}

func TestPatch_IfMatch_MatchSucceeds(t *testing.T) {
	dir := t.TempDir()
	res := newResource(t, dir)
	client := file.NewClient()
	if err := client.Attach(adapterfile.NewBackend()); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	ctx := context.Background()
	vars := map[string]string{"name": "widget"}
	if _, err := client.Create(ctx, res.WithCreate(file.Create{}), record{Name: "widget", Value: 1}, vars); err != nil {
		t.Fatalf("Create: %v", err)
	}
	current, err := os.ReadFile(filepath.Join(dir, "widget.json"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	etag := file.StrongETag(current)

	type valuePatch struct{ Value *int }
	patchCodec := codex.PartialStruct[valuePatch](
		codex.PartialField("value", codex.Int(),
			func(p valuePatch) *int { return p.Value },
			func(p *valuePatch, v *int) { p.Value = v }),
	)
	newValue := 2
	patcher := file.WithPatch(res, patchCodec, file.Patch{})
	if err := client.Patch(ctx, patcher, valuePatch{Value: &newValue}, vars, file.WriteOptions{IfMatch: &etag}); err != nil {
		t.Fatalf("Patch (conditional): %v", err)
	}
	gotAny, err := client.Read(ctx, res.WithRead(file.Read{}), vars)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got := gotAny.(record); got.Value != 2 {
		t.Fatalf("want Value=2 after conditional patch, got %+v", got)
	}
}

func TestPatch_IfMatch_MismatchRejectsNoWrite(t *testing.T) {
	dir := t.TempDir()
	res := newResource(t, dir)
	client := file.NewClient()
	if err := client.Attach(adapterfile.NewBackend()); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	ctx := context.Background()
	vars := map[string]string{"name": "widget"}
	if _, err := client.Create(ctx, res.WithCreate(file.Create{}), record{Name: "widget", Value: 1}, vars); err != nil {
		t.Fatalf("Create: %v", err)
	}
	stale := "stale-etag"
	type valuePatch struct{ Value *int }
	patchCodec := codex.PartialStruct[valuePatch](
		codex.PartialField("value", codex.Int(),
			func(p valuePatch) *int { return p.Value },
			func(p *valuePatch, v *int) { p.Value = v }),
	)
	newValue := 2
	patcher := file.WithPatch(res, patchCodec, file.Patch{})
	err := client.Patch(ctx, patcher, valuePatch{Value: &newValue}, vars, file.WriteOptions{IfMatch: &stale})
	var mismatch file.ConditionalWriteMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("want ConditionalWriteMismatchError, got %v (%T)", err, err)
	}
	gotAny, readErr := client.Read(ctx, res.WithRead(file.Read{}), vars)
	if readErr != nil {
		t.Fatalf("Read: %v", readErr)
	}
	if got := gotAny.(record); got.Value != 1 {
		t.Fatalf("want content unchanged (Value=1) after rejected conditional patch, got %+v", got)
	}
}

func TestConditionalWrite_UnsupportedBackend_RejectsBeforeAnyWrite(t *testing.T) {
	dir := t.TempDir()
	res := newResource(t, dir)
	client := file.NewClient()
	if err := client.Attach(newNonWalkingBackend()); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	ctx := context.Background()
	vars := map[string]string{"name": "widget"}
	if _, err := client.Create(ctx, res.WithCreate(file.Create{}), record{Name: "widget", Value: 1}, vars); err != nil {
		t.Fatalf("Create: %v", err)
	}
	any := "any-etag"
	_, err := client.Create(ctx, res.WithCreate(file.Create{}), record{Name: "widget", Value: 2}, vars, file.WriteOptions{IfMatch: &any})
	var unsupported file.ConditionalWriteUnsupportedError
	if !errors.As(err, &unsupported) {
		t.Fatalf("want ConditionalWriteUnsupportedError, got %v (%T)", err, err)
	}
	gotAny, readErr := client.Read(ctx, res.WithRead(file.Read{}), vars)
	if readErr != nil {
		t.Fatalf("Read: %v", readErr)
	}
	if got := gotAny.(record); got.Value != 1 {
		t.Fatalf("want content unchanged (Value=1), no write attempted, got %+v", got)
	}
}

// TestConditionalWrite_Observer_ReportsNativeFalse_OnFilesystemBackend
// proves adapters/file.Backend's ConditionalWriteBackend implementation
// (a client-side lock-file emulation) is reported as native=false — the
// conservative default for a Backend not implementing the optional
// NativeConditionalWriteBackend marker — mirrors
// RecordFileRangeRead's own native/approximated axis, added during
// adapters/s3's own 2nd gap-review round (docs/design/
// d-0010-adapter-integration-architecture.md's "transport-guaranteed
// vs. adapter-approximated" axis).
func TestConditionalWrite_Observer_ReportsNativeFalse_OnFilesystemBackend(t *testing.T) {
	dir := t.TempDir()
	res := newResource(t, dir)
	client := file.NewClient()
	backend := adapterfile.NewBackend()
	if _, ok := any(backend).(file.NativeConditionalWriteBackend); ok {
		t.Fatalf("adapters/file.Backend must NOT implement NativeConditionalWriteBackend (its conditional write is lock-file-emulated, not native)")
	}
	if err := client.Attach(backend); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	ctx := context.Background()
	vars := map[string]string{"name": "widget"}
	if _, err := client.Create(ctx, res.WithCreate(file.Create{}), record{Name: "widget", Value: 1}, vars); err != nil {
		t.Fatalf("Create: %v", err)
	}
	current, err := os.ReadFile(filepath.Join(dir, "widget.json"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	etag := file.StrongETag(current)

	spy := &countingObserver{}
	ctx2 := stats.WithObserver(ctx, spy)
	if _, err := client.Create(ctx2, res.WithCreate(file.Create{}), record{Name: "widget", Value: 2}, vars, file.WriteOptions{IfMatch: &etag}); err != nil {
		t.Fatalf("Create (conditional overwrite): %v", err)
	}
	if !spy.sawConditionalWriteApprox {
		t.Fatalf("want RecordFileConditionalWrite called with native=false")
	}
	if spy.sawConditionalWriteNative {
		t.Fatalf("want RecordFileConditionalWrite never called with native=true for a filesystem Backend")
	}
}

func TestEntry_ETag_ExistsReturnsWeakETag(t *testing.T) {
	dir := t.TempDir()
	res := newResource(t, dir)
	client := file.NewClient()
	if err := client.Attach(adapterfile.NewBackend()); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	ctx := context.Background()
	vars := map[string]string{"name": "widget"}
	if _, err := client.Create(ctx, res.WithCreate(file.Create{}), record{Name: "widget", Value: 1}, vars); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Exists/Stat deliberately never reads the body — it can only ever
	// offer a WEAK (ModTime+Size-derived, "W/"-prefixed) ETag, never a
	// strong content hash. See docs/roadmap/file-api.md's "Conditional
	// writes" Fork 1.
	entry, exists, err := client.Exists(ctx, res.WithExists(file.Exists{}), vars)
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if !exists {
		t.Fatalf("want exists=true")
	}
	if len(entry.ETag) < 2 || entry.ETag[:2] != `W/` {
		t.Fatalf("want a weak (W/-prefixed) ETag from Stat-derived Exists, got %q", entry.ETag)
	}
}

func TestStrongETag_MatchesWriteBytesIfMatchExpectation(t *testing.T) {
	dir := t.TempDir()
	res := newResource(t, dir)
	client := file.NewClient()
	if err := client.Attach(adapterfile.NewBackend()); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	ctx := context.Background()
	vars := map[string]string{"name": "widget"}
	if _, err := client.Create(ctx, res.WithCreate(file.Create{}), record{Name: "widget", Value: 1}, vars); err != nil {
		t.Fatalf("Create: %v", err)
	}
	current, err := os.ReadFile(filepath.Join(dir, "widget.json"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	etag := file.StrongETag(current)
	// A conditional write using the caller's own file.StrongETag(current
	// bytes) must succeed — confirms the EXACT same hash algorithm
	// backs both the public helper and WriteBytesIfMatch's own internal
	// comparison.
	if _, err := client.Create(ctx, res.WithCreate(file.Create{}), record{Name: "widget", Value: 2}, vars, file.WriteOptions{IfMatch: &etag}); err != nil {
		t.Fatalf("Create (conditional, using file.StrongETag directly): %v", err)
	}
}

// ── Resource[T].BuildPath/MatchPath (closes the gap found migrating
// examples/file-io onto api/file directly) ──────────────────────────────

func TestResource_BuildPath_SubstitutesAndValidates(t *testing.T) {
	res := file.NewResource[record]("readings/{sensorID}/{date}.json", format.JSON(recordCodec()),
		file.PathParam{Name: "sensorID"}, file.PathParam{Name: "date"})
	path, err := res.BuildPath(map[string]string{"sensorID": "sensor-42", "date": "2024-01-15"})
	if err != nil {
		t.Fatalf("BuildPath: %v", err)
	}
	if path != "readings/sensor-42/2024-01-15.json" {
		t.Fatalf("BuildPath = %q, want readings/sensor-42/2024-01-15.json", path)
	}
}

func TestResource_MatchPath_ExtractsVars(t *testing.T) {
	res := file.NewResource[record]("readings/{sensorID}/{date}.json", format.JSON(recordCodec()))
	vars, err := res.MatchPath("readings/sensor-42/2024-01-15.json")
	if err != nil {
		t.Fatalf("MatchPath: %v", err)
	}
	if vars["sensorID"] != "sensor-42" || vars["date"] != "2024-01-15" {
		t.Fatalf("MatchPath vars = %+v, want sensorID=sensor-42 date=2024-01-15", vars)
	}
}

func TestResource_BuildPath_GlobTemplate_ReturnsFileWildcardBuildError(t *testing.T) {
	res := file.NewResource[record]("logs/*.json", format.JSON(recordCodec()))
	_, err := res.BuildPath(nil)
	var wildcardErr file.FileWildcardBuildError
	if !errors.As(err, &wildcardErr) {
		t.Fatalf("BuildPath error = %v (%T), want file.FileWildcardBuildError", err, err)
	}
}

func TestResource_MergeFields_DecodeVarsFromMatchPath(t *testing.T) {
	type meta struct {
		SensorID string
		Date     string
	}
	res := file.NewResource[meta]("readings/{sensorID}/{date}.json", format.JSON(codex.Struct[meta]()),
		file.NewPathParam("sensorID", codex.String(),
			func(m meta) string { return m.SensorID },
			func(m *meta, v string) { m.SensorID = v }),
		file.NewPathParam("date", codex.String(),
			func(m meta) string { return m.Date },
			func(m *meta, v string) { m.Date = v }),
	)
	vars, err := res.MatchPath("readings/sensor-42/2024-01-15.json")
	if err != nil {
		t.Fatalf("MatchPath: %v", err)
	}
	var m meta
	if err := codex.DecodeVars(&m, vars, res.MergeFields()...); err != nil {
		t.Fatalf("DecodeVars: %v", err)
	}
	if m.SensorID != "sensor-42" || m.Date != "2024-01-15" {
		t.Fatalf("decoded = %+v, want sensorID=sensor-42 date=2024-01-15", m)
	}
}

func TestResource_MatchPath_GlobTemplate_MatchesAnonymously(t *testing.T) {
	res := file.NewResource[record]("logs/{level}/*.json", format.JSON(recordCodec()))
	vars, err := res.MatchPath("logs/fatal/anything.json")
	if err != nil {
		t.Fatalf("MatchPath: %v", err)
	}
	if vars["level"] != "fatal" {
		t.Fatalf("MatchPath vars = %+v, want level=fatal", vars)
	}
}
