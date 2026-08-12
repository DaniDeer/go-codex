# Delete a File / Directory + Symmetric Create-Side Safety Options — `ports`

> **Status:** Design complete — not yet implemented.
> [← Back to Roadmap](index.md)
>
> Motivated by a gap in the shipped `ports.File[T]`/`ports.Dir` surface:
> neither has any way to REMOVE a filesystem path. `File` has
> `Read`/`Write`/`Update`/`Patch`; `Dir` has `List`; nothing deletes. Once
> `DryRun`/`Strict` existed for Delete, the natural next question was
> whether the SAME two concepts apply symmetrically to the CREATE side
> (`File.Write`'s already-shipped `CreateDirs`, `Dir.List`'s already-shipped
> `CreateIfMissing`) — they do, with `Strict` reinterpreted as the
> precondition-mirror of Delete's `Strict` (Delete's Strict demands
> PRE-existence; Create's Strict demands PRE-non-existence).

## Motivation

`ports.File[T]` and `ports.Dir` cover create/read/update/list, but a
caller who needs to remove a stale reading file, a whole discovered
use-case config, or a scratch working directory has no declarative path
today — they'd have to reach for `os.Remove`/`os.RemoveAll` directly,
bypassing `BuildPath`'s own template/codec validation entirely (the same
gap `Dir.List` closed for directory enumeration, and `File.Write`'s
`CreateDirs` closed for auto-creating missing parents).

The question this roadmap answers: **can go-codex offer a declarative,
codec-validated `Delete` on both `File[T]` and `Dir`**, covering all
three cases the user asked about — a single file, an empty directory,
and a directory that still contains files — while keeping the
destructive (non-empty-directory) case explicit and hard to trigger by
accident, and letting a caller PREVIEW a destructive delete via `DryRun`
before committing to it?

**Symmetric follow-up**: `File.Write`'s `CreateDirs` and `Dir.List`'s
`CreateIfMissing` (both already shipped — see the earlier `CreateDirs`/
`CreateIfMissing` round) are the CREATE-side counterparts to Delete —
and once `DryRun`/`Strict` exist for Delete, they belong on the create
side too, for the SAME reason: a caller should be able to preview a
mutation before committing to it (`DryRun`), and opt into a stricter
precondition than the permissive default (`Strict`). The precondition
mirrors exactly: Delete's `Strict` demands the path ALREADY existed;
Create's `Strict` demands the path did NOT already exist (refuse to
overwrite/reuse) — the same field name, reused with a
method-contextual, precondition-mirrored meaning, avoiding a
proliferation of near-duplicate `StrictDelete`/`StrictCreate` fields on
an already-multi-purpose `FileOptions`/`DirOptions`.

## Scope decisions

| In scope (Phase 1) | Out of scope |
|---|---|
| `File[T].Delete(vars, opts) (existed bool, err error)` — removes the file at the built path | Batch/glob delete (multiple files in one call) — compose with `Dir.List` + a loop instead, same as any other per-item operation |
| `Dir.Delete(vars, opts) (deleted []string, err error)` — removes an EMPTY directory by default | Deleting only SOME of a directory's contents (selective delete) — compose with `Dir.List` (`EntryPattern`-filtered) + `File.Delete` per discovered entry instead |
| `DirOptions.DeleteRecursive bool` — explicit, SEPARATE opt-in for removing a NON-empty directory and everything inside it (`os.RemoveAll`) — deliberately NOT tied to `Dir`'s own declared `WithRecursive` (a LISTING-depth option; reusing it for deletion would let an innocuous listing declaration silently enable a destructive default) | A "dry-run"/"what would be deleted" preview mode — `Dir.List` (already shipped) already serves as the preview step a caller can run first |
| **Idempotent "ensure absent" semantics is the DEFAULT** — deleting an already-missing file or directory is a SUCCESS, not an error, for both `File.Delete` and `Dir.Delete` (uniform policy, one mental model — same reasoning `PartialField` used to keep presence uniform) | A THIRD "warn but don't fail" mode between idempotent and strict — two clear modes (idempotent default, strict opt-in) is simpler than three |
| **`Strict` option on both `FileOptions`/`DirOptions`** — opts OUT of idempotency: when true, `Delete` on an already-missing path returns a typed `FileNotFoundError`/`DirNotFoundError` instead of silently succeeding. Default `false` (idempotent stays the default — existing callers see no change) | — |
| Proactive, cross-platform-safe non-empty-directory detection (`os.ReadDir` count check BEFORE removal) instead of relying on OS-specific `ENOTEMPTY`-style error matching | Any dependency on `syscall`-level error codes — stays `os`/`io/fs`-only, matching every other `ports.File`/`ports.Dir` operation |
| **Extend the existing `stats.FileObserver` interface directly** with `RecordFileDelete` — a breaking interface change, explicitly ACCEPTABLE (go-codex has one consumer), and simpler/clearer than a parallel extension interface for the SAME file-lifecycle domain `FileObserver` already models (Read/Write/Delete are three verbs of one concept, not three separate concerns) | Reusing `RecordFileWrite` for delete events — rejected: overloads an existing observer method's documented meaning ("encode or filesystem [write] error") with a semantically different operation |
| **`DryRun` option on both `FileOptions` and `DirOptions`** — reports what a real Delete call would do/return WITHOUT mutating the filesystem; `Dir.Delete`'s signature changes to `([]string, error)` (the affected-paths list) so dry-run is actually useful, not just a yes/no; `File.Delete`'s signature changes to `(existed bool, error)` for symmetry and DryRun usefulness | A generic "simulate any operation" framework — DryRun scoped ONLY to Delete/Write(CreateDirs)/List(CreateIfMissing) — Read has no mutating side effect needing a preview |
| **`DryRun`/`Strict` extended to `File.Write`'s `CreateDirs`** — `DryRun` skips the mutating `os.MkdirAll`/`os.WriteFile` calls but still runs `format.Marshal` (surfacing encode errors) and computes the WOULD-BE-created directory list; `Strict` on Write means "the FILE must NOT already exist" (`O_CREATE\|O_EXCL` semantics, new `FileAlreadyExistsError`) — the create-side precondition-mirror of Delete's Strict. Requires changing `File.Write`'s signature to `(createdDirs []string, err error)` (confirmed with the user, accepting the ripple through existing callers) | Making `Strict` govern the PARENT DIRECTORY's pre-existence for Write — rejected: `os.MkdirAll` is inherently idempotent/harmless either way; the FILE's own pre-existence is the meaningful, useful precondition to guard |
| **`DryRun`/`Strict` extended to `Dir.List`'s `CreateIfMissing`** — `DryRun` skips only the mutating `os.MkdirAll` call (the Strict existence check still runs, so `DirAlreadyExistsError` still surfaces under DryRun); `Strict` means "the DIRECTORY must NOT already exist" (new `DirAlreadyExistsError`) — same precondition-mirror. No signature change needed: a DryRun+CreateIfMissing call on a genuinely-missing directory naturally surfaces `DirReadError` (since nothing was actually created), which IS the informative "creation would have been needed" signal | A richer "what would List return after creation" preview — a freshly-created directory is always empty, so there is nothing more informative to report than the boolean/error outcome already available |

## API surface

### `ports/file.go` additions

```go
// FileOptions gains (shared across Write AND Delete — see each method's
// own doc for the context-specific meaning):
type FileOptions struct {
    // ...existing fields (Observer, Perm, Context, CreateDirs, DirPerm)...

    // DryRun, when true:
    //   - on [File.Delete]: reports what WOULD happen (via existed/err)
    //     WITHOUT removing the file.
    //   - on [File.Write]: reports which directories WOULD be created
    //     (via createdDirs) WITHOUT creating them or writing the file —
    //     format.Marshal still runs, so encode errors still surface.
    // Has no effect on Read/Update/Patch beyond what Write/Delete
    // already propagate. Default false.
    DryRun bool

    // Strict, when true:
    //   - on [File.Delete]: requires the file to have existed —
    //     [FileNotFoundError] instead of idempotent "ensure absent" success.
    //   - on [File.Write]: requires the file to NOT already exist —
    //     [FileAlreadyExistsError] instead of silently overwriting
    //     (O_CREATE|O_EXCL semantics; the create-side precondition-mirror
    //     of Delete's Strict). Has no effect on [File.Read]/[Patch].
    // Default false — existing callers see no change unless they opt in.
    Strict bool
}

// Write builds the concrete path from vars, encodes v, and writes it to
// the file — unless [FileOptions.Strict] is set AND the file already
// exists, in which case it returns [FileAlreadyExistsError] and performs
// no write (O_CREATE|O_EXCL semantics). If [FileOptions.CreateDirs] is
// set, missing parent directories are created first (or, under
// [FileOptions.DryRun], only COMPUTED — see createdDirs below).
//
// createdDirs lists the parent directories that were created (or, under
// DryRun, that WOULD be created) — empty when CreateDirs is false or no
// parent directories were missing. Under DryRun, NOTHING is created or
// written — createdDirs and any [FileAlreadyExistsError] reflect exactly
// what a real call would do.
//
// Errors:
//   - [FilePathParamError] / [MissingFilePathVarError] — path variable validation failure (no I/O)
//   - [FileEncodeError] — format encode/validation failure
//   - [FileAlreadyExistsError] — file already exists AND [FileOptions.Strict] is set
//   - [FileWriteError] — os.MkdirAll (when CreateDirs is set) or os.WriteFile failure
func (fh File[T]) Write(vars map[string]string, v T, opts FileOptions) (createdDirs []string, err error)

// Delete removes the file at the built path. By default, Delete is
// idempotent "ensure absent" semantics: if the file is already gone,
// Delete succeeds. Set [FileOptions.Strict] to require the file to have
// existed — a missing file then returns [FileNotFoundError] instead.
// existed reports whether the file was present before the call (true
// even under [FileOptions.DryRun], which performs the same existence
// check but skips the actual os.Remove).
//
// Errors:
//   - [FilePathParamError] / [MissingFilePathVarError] — path variable validation failure (no I/O)
//   - [FileNotFoundError] — file did not exist AND [FileOptions.Strict] is set
//   - [FileDeleteError] — os.Remove failure OTHER than "already absent"
//     (e.g. permission denied, path is actually a non-empty directory)
func (fh File[T]) Delete(vars map[string]string, opts FileOptions) (existed bool, err error)

// WriteHandle and Update propagate the SAME (createdDirs, err)/(err)
// shape change — see "Files to create / change" for the exact ripple.
func WriteHandle[T any](fh File[T], v T, opts FileOptions) (createdDirs []string, err error)
func (fh File[T]) Update(vars map[string]string, fn func(T) T, opts FileOptions) (createdDirs []string, err error)
```

### `ports/dir.go` additions

```go
// DirOptions gains:
type DirOptions struct {
    // ...existing fields (Observer, Context, CreateIfMissing, CreatePerm)...

    // DeleteRecursive, when true, allows [Dir.Delete] to remove a
    // NON-empty directory and everything inside it (os.RemoveAll).
    // Default false: Delete on a non-empty directory returns
    // [DirNotEmptyError] instead of silently recursing. Deliberately
    // SEPARATE from the Dir's own declared [WithRecursive] (a
    // listing-depth option) — a destructive operation must be opted into
    // explicitly, per call, never inherited from an unrelated listing
    // declaration.
    DeleteRecursive bool

    // DryRun, when true:
    //   - on [Dir.Delete]: returns the list of paths that WOULD be
    //     removed WITHOUT actually removing anything. Reports the SAME
    //     errors a real call would (e.g. [DirNotEmptyError]).
    //   - on [Dir.List] (with CreateIfMissing set): skips the mutating
    //     os.MkdirAll call only — a genuinely-missing directory then
    //     naturally surfaces [DirReadError] from List's own os.ReadDir
    //     (the informative "creation would have been needed" signal);
    //     the Strict existence check below still runs regardless.
    // Default false.
    DryRun bool

    // Strict, when true:
    //   - on [Dir.Delete]: requires the directory to have existed —
    //     [DirNotFoundError] instead of idempotent "ensure absent" success.
    //   - on [Dir.List] (only meaningful together with CreateIfMissing):
    //     requires the directory to NOT already exist — [DirAlreadyExistsError]
    //     instead of silently reusing it (the create-side
    //     precondition-mirror of Delete's Strict). Checked via os.Stat
    //     BEFORE os.MkdirAll, so it still fires correctly under DryRun.
    //     Has no effect when CreateIfMissing is false.
    // Default false — existing callers see no change unless they opt in.
    Strict bool
}

// Delete removes the directory at the built path (and, if
// [DirOptions.DeleteRecursive] is set, everything inside it). By
// default, Delete is idempotent "ensure absent" semantics: a missing
// directory is not an error (returns a nil, empty slice). Set
// [DirOptions.Strict] to require the directory to have existed — a
// missing directory then returns [DirNotFoundError] instead. A
// non-empty directory is refused with [DirNotEmptyError] UNLESS
// DeleteRecursive is true.
//
// deleted is the list of concrete paths removed — or, under
// [DirOptions.DryRun], the list that WOULD have been removed, with
// nothing actually deleted. Non-recursive: at most one path (the
// directory itself, once confirmed empty). Recursive: every path under
// the tree (root included).
//
// Errors:
//   - [DirPathParamError] / [MissingDirPathVarError] — directory path variable validation failure (no I/O)
//   - [DirNotFoundError] — directory did not exist AND [DirOptions.Strict] is set
//   - [DirNotEmptyError] — non-empty directory, DeleteRecursive not set
//   - [DirDeleteError] — os.ReadDir/os.Remove/os.RemoveAll failure (e.g. permission denied)
func (d Dir) Delete(vars map[string]string, opts DirOptions) (deleted []string, err error)
```

### `stats/observer.go` change (BREAKING — acceptable, single consumer)

```go
// FileObserver gains a third lifecycle method — Delete joins Read/Write
// as the three verbs of the file-I/O lifecycle this interface already
// models. Every current implementer in this repo (NoopObserver,
// LoggingObserver, fanout) gets RecordFileDelete added directly; any
// spy/observer that EMBEDS stats.NoopObserver (the established idiom —
// every FileObserver implementer in this repo already does this) needs
// ZERO changes, since it inherits NoopObserver's no-op default
// automatically.
type FileObserver interface {
    RecordFileRead(path string, success bool, duration time.Duration)
    RecordFileWrite(path string, success bool, duration time.Duration)

    // RecordFileDelete is called after every [ports.File.Delete]/
    // [ports.Dir.Delete] attempt (including DryRun calls). success is
    // false for any error OTHER than the idempotent "already absent"
    // case (which reports success=true — the postcondition holds).
    RecordFileDelete(path string, success bool, duration time.Duration)
}
```

## Structured errors (all implement `slog.LogValuer`)

| Type | Fields | Fires when |
|---|---|---|
| `FileDeleteError{Path, Err}` | `Path string; Err error` | `os.Remove` fails for a reason OTHER than "already absent" |
| `FileNotFoundError{Path}` | `Path string` | `File.Delete` called on an already-missing file with `FileOptions.Strict` set |
| `FileAlreadyExistsError{Path}` | `Path string` | `File.Write` called on an already-existing file with `FileOptions.Strict` set |
| `DirDeleteError{Path, Err}` | `Path string; Err error` | `os.Remove`/`os.RemoveAll` fails for a reason OTHER than "already absent" |
| `DirNotFoundError{Path}` | `Path string` | `Dir.Delete` called on an already-missing directory with `DirOptions.Strict` set |
| `DirAlreadyExistsError{Path}` | `Path string` | `Dir.List` (with `CreateIfMissing`) called on an already-existing directory with `DirOptions.Strict` set |
| `DirNotEmptyError{Path}` | `Path string` | `Dir.Delete` called on a non-empty directory without `DeleteRecursive` |

All follow the established `Error()`/`Unwrap()` (where an inner `Err`
exists)/`LogValue()` pattern from `ports/file.go`'s existing error types
— no new pattern introduced. `DirNotEmptyError`/`FileNotFoundError`/
`DirNotFoundError`/`FileAlreadyExistsError`/`DirAlreadyExistsError` have
no `Err` field (no inner error — all five are proactive, deterministic
checks via `os.Stat`/`os.ReadDir`, not an OS-level error being wrapped),
so none implement `Unwrap()`. `FileAlreadyExistsError` is the one
exception where a REAL (non-DryRun) call detects the condition
atomically via `O_EXCL` rather than a separate `os.Stat` — see API
surface — but the error type itself still carries no wrapped OS error,
for a consistent shape with its four siblings.

## Observer integration

`RecordFileDelete` joins `RecordFileRead`/`RecordFileWrite` directly on
`stats.FileObserver` (breaking change to the interface, resolved below) —
called via the SAME type-assertion guard `File.Read`/`Write`/`Dir.List`
already use (`if fo, ok := obs.(stats.FileObserver); ok { ... }`), so
`File.Delete`/`Dir.Delete` need no new plumbing beyond the third method
call. Fires on every Delete attempt, including `DryRun` calls (DryRun
changes only filesystem mutation, not observability — a dry run that
determines "this would succeed" reports success=true, same as a real
run would).

`File.Write`'s existing `RecordFileWrite` call is unchanged in shape —
still fires exactly once per `Write` call, `success=false` for a
`Strict`-triggered `FileAlreadyExistsError` (same as any other write
failure) and for a `DryRun` call's outcome (same principle as Delete's
DryRun above). No new observer plumbing needed for the create-side
symmetry beyond this.

## Unit test plan

| Test | Verifies |
|---|---|
| `TestFile_Delete_RemovesFile` | An existing file is removed (`existed == true`); a subsequent Read fails |
| `TestFile_Delete_MissingFile_IdempotentSuccess` | Deleting an already-absent file returns `existed == false, err == nil` (Strict defaults to false) |
| `TestFile_Delete_PathVarError_NoIO` | Missing/invalid path var returns `FilePathParamError`/`MissingFilePathVarError` before any I/O |
| `TestFile_Delete_DryRun_DoesNotRemoveFile` | `FileOptions.DryRun: true` reports `existed == true, err == nil` but the file is still present afterward |
| `TestFile_Delete_Strict_MissingFile_ReturnsFileNotFoundError` | `FileOptions.Strict: true` on an already-missing file returns `FileNotFoundError` instead of idempotent success |
| `TestFile_Delete_Strict_ExistingFile_RemovesNormally` | `Strict` has no effect when the file DOES exist — same happy path as the non-strict case |
| `TestFile_Delete_ObserverRecordsFileDelete` | `stats.FileObserver.RecordFileDelete` fires on success (real-delete, idempotent-absent, and DryRun cases) and on the Strict-triggered `FileNotFoundError` |
| `TestDir_Delete_RemovesEmptyDir` | An empty directory is removed; `deleted == []string{path}` |
| `TestDir_Delete_NonEmptyWithoutRecursive_ReturnsDirNotEmptyError` | A directory containing files is refused by default |
| `TestDir_Delete_NonEmptyWithRecursive_RemovesAll` | `DeleteRecursive: true` removes the directory and its contents; `deleted` lists every removed path |
| `TestDir_Delete_MissingDir_IdempotentSuccess` | Deleting an already-absent directory returns `nil, nil` (empty slice, no error; Strict defaults to false) |
| `TestDir_Delete_PathVarError_NoIO` | Missing/invalid directory path var returns typed error before any I/O |
| `TestDir_Delete_DryRun_NonRecursive_ReportsWithoutRemoving` | `DirOptions.DryRun: true` on an empty dir returns `deleted == []string{path}` but the directory still exists afterward |
| `TestDir_Delete_DryRun_Recursive_ListsAllAffectedPaths` | `DryRun + DeleteRecursive` on a non-empty tree returns every path that WOULD be removed, with nothing actually removed |
| `TestDir_Delete_DryRun_NonEmptyWithoutRecursive_StillReturnsDirNotEmptyError` | DryRun reports the SAME error a real call would (does not suppress `DirNotEmptyError`) |
| `TestDir_Delete_Strict_MissingDir_ReturnsDirNotFoundError` | `DirOptions.Strict: true` on an already-missing directory returns `DirNotFoundError` instead of idempotent success |
| `TestDir_Delete_ObserverRecordsFileDelete` | Same `stats.FileObserver.RecordFileDelete` fires for `Dir.Delete` too (shared, not duplicated), including the Strict-triggered `DirNotFoundError` case |
| `TestWrite_CreateDirs_ReturnsCreatedDirs` | `Write` with `CreateDirs: true` on missing parents returns the list of directories actually created |
| `TestWrite_CreateDirsFalse_ReturnsEmptyCreatedDirs` | `Write` without `CreateDirs` (or when no parents were missing) returns a nil/empty `createdDirs` |
| `TestWrite_DryRun_CreateDirs_ComputesWithoutCreating` | `FileOptions.DryRun: true` with `CreateDirs: true` on missing parents returns the WOULD-BE-created list, but no directories or file are actually created |
| `TestWrite_DryRun_StillRunsEncode` | A `DryRun` write with an encode-constraint-failing value still returns `FileEncodeError` (DryRun doesn't skip validation, only the mutating I/O) |
| `TestWrite_Strict_ExistingFile_ReturnsFileAlreadyExistsError` | `FileOptions.Strict: true` on an already-existing file returns `FileAlreadyExistsError`, no write performed |
| `TestWrite_Strict_MissingFile_WritesNormally` | `Strict` has no effect when the file does NOT already exist — same happy path as the non-strict case |
| `TestWrite_Strict_DryRun_ExistingFile_ReportsWithoutWriting` | `Strict + DryRun` on an already-existing file reports `FileAlreadyExistsError` without touching the filesystem |
| `TestUpdate_PropagatesCreatedDirs` / `TestWriteHandle_PropagatesCreatedDirs` | `Update`/`WriteHandle` propagate the same `(createdDirs, err)` shape from the underlying `Write` call |
| `TestDir_List_CreateIfMissing_Strict_ExistingDir_ReturnsDirAlreadyExistsError` | `CreateIfMissing: true` + `Strict: true` on an already-existing directory returns `DirAlreadyExistsError`, nothing created/modified |
| `TestDir_List_CreateIfMissing_Strict_MissingDir_CreatesNormally` | `Strict` has no effect when the directory does NOT already exist — creates and lists normally |
| `TestDir_List_CreateIfMissing_DryRun_MissingDir_ReturnsDirReadError` | `CreateIfMissing` + `DryRun` on a genuinely-missing directory returns `DirReadError` (nothing created — the informative "would need creation" signal) |
| `TestDir_List_CreateIfMissing_Strict_DryRun_ExistingDir_StillReturnsDirAlreadyExistsError` | The Strict existence check still fires under DryRun (proactive check, not gated by the DryRun-skipped mutation) |

## Files to create / change

| File | Responsibility |
|---|---|
| `ports/file.go` | `File[T].Delete(vars, opts) (existed bool, err error)` method (NEW); `File[T].Write` signature changes to `(createdDirs []string, err error)` (BREAKING); `WriteHandle`/`Update` signature changes to match; `Patch`'s internal `fh.Write(...)` call updated to discard `createdDirs` (its own signature stays `error` — Patch requires the file to already exist, so `CreateDirs` never has anything to do by the time Patch calls Write); `FileOptions.DryRun`/`Strict` fields (shared, context-specific meaning per method); `FileDeleteError`/`FileNotFoundError`/`FileAlreadyExistsError` types |
| `ports/dir.go` | `Dir.Delete(vars, opts) (deleted []string, err error)` method (NEW); `DirOptions.DeleteRecursive`/`DryRun`/`Strict` fields (shared, context-specific meaning per method — `DryRun`/`Strict` also apply to the ALREADY-SHIPPED `List`+`CreateIfMissing`); `DirDeleteError`/`DirNotEmptyError`/`DirNotFoundError`/`DirAlreadyExistsError` types |
| `adapters/file/binding.go` | `DrainWriteFileAdapter`'s internal `a.f.Write(vars, v, fileOpts)` call updated to discard the new `createdDirs` return (`if _, err := ...`) |
| `stats/observer.go` | `FileObserver` gains `RecordFileDelete` directly (BREAKING — acceptable); `NoopObserver`/`LoggingObserver`/`fanout` gain the method (mechanical, all 3 implementers live in this one file) |
| `stats/observer_test.go` | Compile-time assertion that `NoopObserver`/`LoggingObserver`/`fanout` satisfy the updated `FileObserver` (already exists for the interface — just needs re-verifying, not a new assertion) |
| `ports/file_test.go` | `TestFile_Delete_*` (7) + `TestWrite_CreateDirs_*`/`TestWrite_Strict_*`/`TestUpdate_*`/`TestWriteHandle_*` (8) tests above; EVERY existing `TestWrite_*`/`TestUpdate_*`/`TestWriteHandle_*` test updated for the new two-return-value signature (mechanical) |
| `ports/dir_test.go` | `TestDir_Delete_*` (10) + `TestDir_List_CreateIfMissing_Strict_*`/`_DryRun_*` (4) tests above |
| `.github/instructions/go-codex.instructions.md` | `ports` row (`File.Delete`/`Dir.Delete`/`File.Write`'s new signature/`DirOptions.DeleteRecursive`/`DryRun`/`Strict` on both Options), `stats` row (`FileObserver.RecordFileDelete`, note the breaking addition) |
| `docs/features/ports.md` | `FilePattern`/`Dir` subsections — short note + example snippet each, including DryRun and Strict examples for BOTH Delete and Create (`CreateDirs`/`CreateIfMissing`) |
| `examples/file-io/main.go` | New `Delete` demo scene + update the existing `CreateDirs` scene for the new `Write` signature/DryRun/Strict; 8 existing `.Write(`/`.Update(` call sites updated (mechanical `_, err :=`) |
| `examples/dir-io/main.go` | New `Delete` demo scene, covering empty-dir delete, non-empty refusal, `DeleteRecursive`, `DryRun` (both non-recursive and recursive), and `Strict`; update the existing `CreateIfMissing` scene for `DryRun`/`Strict` |
| `examples/file-patch-sink/main.go`, `examples/adapters-nethttp/main.go`, `examples/adapters-chi/main.go`, `examples/adapters-openai/main.go`, `examples/pattern-custom-format/main.go`, `examples/flat-key-patch/main.go`, `examples/http-trace-span-propagation/main.go` | Mechanical `.Write(...)` call-site updates for the new two-return-value signature (`if _, err := ... ; err != nil`) — no behavior change |

## Out of scope (Phase 2)

- Batch/glob delete — compose `Dir.List` + a loop of `File.Delete` calls instead (same pattern the `dir-io` example already demonstrates for read).
- A "trash"/soft-delete (move-instead-of-remove) mode — no concrete use case yet.
- `fs.FS`-rooted deletion — Phase 1 stays `os`-rooted, matching `ports.File`/`ports.Dir`'s current scope.

## Resolved design decisions

1. **Idempotent "ensure absent" semantics — RESOLVED**: deleting an
   already-missing file or directory is a SUCCESS for both `File.Delete`
   and `Dir.Delete` — one uniform policy, confirmed with the user.
2. **Recursive directory delete requires an EXPLICIT, SEPARATE opt-in —
   RESOLVED**: `DirOptions.DeleteRecursive` is its own field, deliberately
   NOT tied to `Dir`'s declared `WithRecursive` (a listing-depth option)
   — confirmed with the user specifically to prevent an innocuous listing
   declaration from silently enabling destructive recursive deletion.
3. **Non-empty-directory detection is proactive and cross-platform-safe
   — RESOLVED**: `Dir.Delete` checks `os.ReadDir`'s entry count itself
   before attempting removal, rather than pattern-matching OS-specific
   `ENOTEMPTY`-style errors from a failed `os.Remove` call.
4. **`RecordFileDelete` lives directly on `stats.FileObserver` — RESOLVED
   (revised from the original "new `FileDeleteObserver` extension"
   design)**: since go-codex has exactly one consumer, a breaking
   interface change is acceptable, and is actually the SIMPLER, CLEARER
   choice here — Read/Write/Delete are three verbs of the same
   file-I/O-lifecycle concept `FileObserver` already models, not three
   separate concerns needing separate interfaces. Blast radius confirmed
   minimal: every current `FileObserver` implementer in this repo
   (`NoopObserver`, `LoggingObserver`, `fanout`, plus every test/example
   spy) either lives in `stats/observer.go` directly or EMBEDS
   `stats.NoopObserver` (the established idiom throughout this repo) — an
   embedding-based implementer needs ZERO code changes, since it
   inherits the new no-op method automatically.
5. **`DryRun` — RESOLVED (new, added in this revision)**: both
   `FileOptions` and `DirOptions` gain a `DryRun bool` field. A dry run
   reports EXACTLY what a real call would do/return — including the SAME
   errors (e.g. `DirNotEmptyError`) — without mutating the filesystem.
   This required changing both method signatures to report USEFUL
   information beyond a bare `error`: `File.Delete` returns
   `(existed bool, err error)`; `Dir.Delete` returns
   `(deleted []string, err error)` (the affected-paths list — without
   this, a `DryRun` on a recursive delete could only answer "would this
   succeed," not "what would it remove," which is the actually useful
   question before running a destructive recursive delete).
6. **`Strict` — RESOLVED (new, added in this revision)**: both
   `FileOptions` and `DirOptions` gain a `Strict bool` field, default
   `false`. Idempotent "ensure absent" (decision #1) remains the
   DEFAULT behavior — `Strict` is the opt-OUT for callers who need
   "delete must confirm the path existed," returning
   `FileNotFoundError`/`DirNotFoundError` (new, no `Err` field — a
   proactive `os.Stat` check, not a wrapped OS error) instead of
   silently succeeding. Two clear modes (idempotent default, strict
   opt-in) rather than a third in-between mode.
7. **`DryRun`/`Strict` extended symmetrically to the CREATE side
   (`File.Write`'s `CreateDirs`, `Dir.List`'s `CreateIfMissing`) —
   RESOLVED (new, added in this revision)**: confirmed with the user.
   `Strict` is REINTERPRETED per direction as the precondition-mirror of
   Delete's `Strict` — Delete demands PRE-existence; Create demands
   PRE-non-existence (refuse to overwrite/reuse) — the SAME field name,
   context-specific meaning, avoiding `StrictDelete`/`StrictCreate`
   field proliferation on an already-multi-purpose Options struct.
   `File.Write`'s signature changes to `(createdDirs []string, err
   error)` for full symmetry with `Dir.Delete`'s richer return — the
   user explicitly confirmed accepting the ripple through
   `WriteHandle`/`Update`/`adapters/file.DrainWriteFileAdapter`/all
   existing example call sites (mechanical `_, err :=` updates,
   catalogued in "Files to create / change"). `Dir.List` needs NO
   signature change — a `DryRun`+`CreateIfMissing` call on a
   genuinely-missing directory naturally surfaces `DirReadError` (since
   nothing gets created), which already IS the informative "creation
   would have been needed" signal; `File.Write`'s `Strict` uses atomic
   `O_CREATE|O_EXCL` semantics (race-free) for the real call, while a
   `DryRun`+`Strict` preview uses a plain `os.Stat` check (dry runs
   don't need atomicity since nothing is committed).

No open design decisions remain — ready for implementation.
