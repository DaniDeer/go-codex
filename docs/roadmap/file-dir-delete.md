# Delete a File / Directory (incl. non-empty) — `ports`

> **Status:** Design complete — not yet implemented.
> [← Back to Roadmap](index.md)
>
> Motivated by a gap in the shipped `ports.File[T]`/`ports.Dir` surface:
> neither has any way to REMOVE a filesystem path. `File` has
> `Read`/`Write`/`Update`/`Patch`; `Dir` has `List`; nothing deletes.

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
accident?

## Scope decisions

| In scope (Phase 1) | Out of scope |
|---|---|
| `File[T].Delete(vars, opts) error` — removes the file at the built path | Batch/glob delete (multiple files in one call) — compose with `Dir.List` + a loop instead, same as any other per-item operation |
| `Dir.Delete(vars, opts) error` — removes an EMPTY directory by default | Deleting only SOME of a directory's contents (selective delete) — compose with `Dir.List` (`EntryPattern`-filtered) + `File.Delete` per discovered entry instead |
| `DirOptions.DeleteRecursive bool` — explicit, SEPARATE opt-in for removing a NON-empty directory and everything inside it (`os.RemoveAll`) — deliberately NOT tied to `Dir`'s own declared `WithRecursive` (a LISTING-depth option; reusing it for deletion would let an innocuous listing declaration silently enable a destructive default) | A "dry-run"/"what would be deleted" preview mode — `Dir.List` (already shipped) already serves as the preview step a caller can run first |
| **Idempotent "ensure absent" semantics** — deleting an already-missing file or directory is a SUCCESS, not an error, for both `File.Delete` and `Dir.Delete` (uniform policy, one mental model — same reasoning `PartialField` used to keep presence uniform) | RFC-style "delete must confirm prior existence" strict mode — no concrete use case for it; can be requested later as an opt-in if one appears |
| Proactive, cross-platform-safe non-empty-directory detection (`os.ReadDir` count check BEFORE removal) instead of relying on OS-specific `ENOTEMPTY`-style error matching | Any dependency on `syscall`-level error codes — stays `os`/`io/fs`-only, matching every other `ports.File`/`ports.Dir` operation |
| New `stats.FileDeleteObserver` optional extension (`RecordFileDelete`) — additive, does NOT modify the existing `stats.FileObserver` interface (adding a method to an existing interface would break every current implementer) | Reusing `RecordFileWrite` for delete events — rejected: overloads an existing observer method's documented meaning ("encode or filesystem [write] error") with a semantically different operation |

## API surface

### `ports/file.go` additions

```go
// Delete removes the file at the built path. Missing files are NOT an
// error — Delete is idempotent "ensure absent" semantics: if the file is
// already gone, Delete succeeds.
//
// Errors:
//   - [FilePathParamError] / [MissingFilePathVarError] — path variable validation failure (no I/O)
//   - [FileDeleteError] — os.Remove failure OTHER than "already absent"
//     (e.g. permission denied, path is actually a non-empty directory)
func (fh File[T]) Delete(vars map[string]string, opts FileOptions) error
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
}

// Delete removes the directory at the built path. Missing directories
// are NOT an error (idempotent "ensure absent", same policy as
// [File.Delete]). A non-empty directory is refused with
// [DirNotEmptyError] UNLESS [DirOptions.DeleteRecursive] is true, in
// which case the directory and everything inside it is removed
// (os.RemoveAll).
//
// Errors:
//   - [DirPathParamError] / [MissingDirPathVarError] — directory path variable validation failure (no I/O)
//   - [DirNotEmptyError] — non-empty directory, DeleteRecursive not set
//   - [DirDeleteError] — os.Remove/os.RemoveAll failure (e.g. permission denied)
func (d Dir) Delete(vars map[string]string, opts DirOptions) error
```

### `stats/observer.go` addition

```go
// FileDeleteObserver is an optional extension to Observer for delete
// lifecycle events on [ports.File.Delete] and [ports.Dir.Delete]. Purely
// additive — existing Observer implementations need not change.
type FileDeleteObserver interface {
    // RecordFileDelete is called after every Delete attempt. path is the
    // concrete path (after template substitution); success is false for
    // any error OTHER than the idempotent "already absent" case (which
    // records success=true — the postcondition holds).
    RecordFileDelete(path string, success bool, duration time.Duration)
}
```

## Structured errors (all implement `slog.LogValuer`)

| Type | Fields | Fires when |
|---|---|---|
| `FileDeleteError{Path, Err}` | `Path string; Err error` | `os.Remove` fails for a reason OTHER than "already absent" |
| `DirDeleteError{Path, Err}` | `Path string; Err error` | `os.Remove`/`os.RemoveAll` fails for a reason OTHER than "already absent" |
| `DirNotEmptyError{Path}` | `Path string` | `Dir.Delete` called on a non-empty directory without `DeleteRecursive` |

All follow the established `Error()`/`Unwrap()` (where an inner `Err`
exists)/`LogValue()` pattern from `ports/file.go`'s existing error types
— no new pattern introduced. `DirNotEmptyError` has no `Err` field (no
inner error — it's a proactive, deterministic check via `os.ReadDir`'s
entry count, not an OS-level error being wrapped), so no `Unwrap()`.

## Observer integration

New `stats.FileDeleteObserver` extension (see API surface above),
type-asserted in both `File.Delete` and `Dir.Delete` exactly like
`stats.FileObserver` is type-asserted in `Read`/`Write`/`List` today —
guarded, nil-safe, defaults to no-op when the configured observer doesn't
implement it.

## Unit test plan

| Test | Verifies |
|---|---|
| `TestFile_Delete_RemovesFile` | An existing file is removed; a subsequent Read fails |
| `TestFile_Delete_MissingFile_IdempotentSuccess` | Deleting an already-absent file returns nil, not an error |
| `TestFile_Delete_PathVarError_NoIO` | Missing/invalid path var returns `FilePathParamError`/`MissingFilePathVarError` before any I/O |
| `TestFile_Delete_ObserverRecordsFileDelete` | `stats.FileDeleteObserver.RecordFileDelete` fires on success (both real-delete and idempotent-absent cases) |
| `TestDir_Delete_RemovesEmptyDir` | An empty directory is removed |
| `TestDir_Delete_NonEmptyWithoutRecursive_ReturnsDirNotEmptyError` | A directory containing files is refused by default |
| `TestDir_Delete_NonEmptyWithRecursive_RemovesAll` | `DeleteRecursive: true` removes the directory and its contents |
| `TestDir_Delete_MissingDir_IdempotentSuccess` | Deleting an already-absent directory returns nil, not an error |
| `TestDir_Delete_PathVarError_NoIO` | Missing/invalid directory path var returns typed error before any I/O |
| `TestDir_Delete_ObserverRecordsFileDelete` | Same observer extension fires for `Dir.Delete` too (shared, not duplicated) |

## Files to create / change

| File | Responsibility |
|---|---|
| `ports/file.go` | `File[T].Delete` method; `FileDeleteError` type |
| `ports/dir.go` | `Dir.Delete` method; `DirOptions.DeleteRecursive` field; `DirDeleteError`/`DirNotEmptyError` types |
| `stats/observer.go` | New `FileDeleteObserver` optional extension; `NoopObserver`/`LoggingObserver`/`fanout` implement it |
| `stats/observer_test.go` | Compile-time assertion that `NoopObserver`/`LoggingObserver`/`fanout` satisfy `FileDeleteObserver` |
| `ports/file_test.go` | `TestFile_Delete_*` tests above |
| `ports/dir_test.go` | `TestDir_Delete_*` tests above |
| `.github/instructions/go-codex.instructions.md` | `ports` row (`File.Delete`/`Dir.Delete`/`DirOptions.DeleteRecursive`), `stats` row (`FileDeleteObserver`) |
| `docs/features/ports.md` | `FilePattern`/`Dir` subsections — short note + example snippet each |
| `examples/file-io/main.go` | New `Delete` demo scene (extends the already-focused example, same precedent as the `CreateDirs` scene) |
| `examples/dir-io/main.go` | New `Delete` demo scene, covering empty-dir delete, non-empty refusal, and `DeleteRecursive` |

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
4. **New `stats.FileDeleteObserver` extension, not reusing
   `RecordFileWrite` — RESOLVED**: delete is a genuinely distinct
   lifecycle event from write; a new optional, purely-additive interface
   follows the same established pattern as `SQLObserver`/
   `SecurityObserver`, and does not touch the existing `FileObserver`
   interface (which would break current implementers).

No open design decisions remain — ready for implementation.
