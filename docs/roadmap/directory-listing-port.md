# Directory Listing Port — `ports`

> **Status:** SHIPPED. `ports.Dir`/`DirPathParam`/`EntryPattern` are
> implemented in `ports/dir.go` (tests in `ports/dir_test.go`). The
> flagship consumer, `examples/go-edge-models/models/iotedge.NewConfigDir`,
> demonstrates discovering iotedge use-case config files by filename (see
> `configdir.go`/`configdir_test.go` and the "ports.Dir" section of
> `examples/go-edge-models/main.go`). Kept here as design history/
> rationale — see `docs/features/ports.md`'s "`Dir` — listing a
> directory's entries" subsection and
> `.github/instructions/go-codex.instructions.md`'s `ports` row for the
> current, user-facing documentation.
> [← Back to Roadmap](index.md)
>
> Motivated by a concrete consumer: `examples/go-edge-models`'s iotedge
> config files, where each file in a directory represents one "use case"
> and the filename IS the use case's name — the caller needs to discover
> which use cases exist on disk (like a directory `ls`) before it can
> `ports.File.Read`/`Patch` any specific one, with the SAME
> declare-the-allowed-shape-via-a-codec discipline `ports.File`'s
> `FilePathParam` already applies to individual file paths.

## Motivation

`ports.File[T]` already supports validating a CONCRETE, already-discovered
path against a declared template + per-variable codec (`BuildPath`,
`MatchPath`) — but it has no way to DISCOVER those concrete paths itself.
Today a caller wanting to enumerate "which use-case files exist in this
directory" must reach for `os.ReadDir`/`filepath.WalkDir`/`filepath.Glob`
directly, then feed each discovered path into `File.MatchPath` by hand —
bypassing go-codex's own path-template/codec vocabulary for the
DIRECTORY side of the operation entirely (there is no equivalent of
`FilePathParam` for "is this a directory I'm allowed to list", and no
declarative way to express "for each entry found, parse its FILENAME the
same way a `File` path template parses a full path").

The question this roadmap answers: **can go-codex offer a `ports.File`-like
declarative surface for LISTING a directory's entries** (files and
subdirectories), reusing the existing `internal/templatematch` core the
same way `ports.File` already does, so that:

1. The directory PATH itself (which may have its own `{var}` segments,
   e.g. `"configs/{env}"`) is validated via a `DirPathParam`-like codec,
   mirroring `FilePathParam` exactly.
2. Each individual ENTRY found inside that directory can OPTIONALLY be
   parsed via its own filename template + codec (e.g. `"{useCase}.json"`
   extracting `useCase`), the same declarative shape `File`'s own path
   template already provides — so "list only files matching an expected
   naming convention, with typed/validated extracted variables" becomes a
   built-in capability instead of hand-rolled filtering logic.

## Scope decisions

| In scope (Phase 1) | Out of scope |
|---|---|
| A new standalone port type, `ports.Dir` (NOT a method on `ports.File[T]` — a directory listing has no single, typed payload the way a file's contents do; it is independent of any specific `File[T]`) | Extending `ports.File[T]` itself with a `List` method — rejected: conflates two different concerns (one file's typed content vs. a directory's untyped entry names) |
| `DirPathParam` — validates `{var}` segments in the DIRECTORY's own path template, exact mirror of `FilePathParam`'s codec/`WithCodec` shape | Wildcard/glob segments in the directory template itself (e.g. `configs/*/`) — `{var}` placeholders only, same as `FilePathParam` today; see [Wildcard/Glob Path Template Segments](path-template-wildcards.md) for a follow-on sketch covering BOTH `FilePathParam` and `DirPathParam` together |
| Optional per-entry `EntryPattern` (filename template + named codecs, e.g. `"{useCase}.json"`) — when set, each entry's filename is matched/validated the same way `File.MatchPath` matches a full path; when NOT set, entries are returned as plain names with no parsing | Recursive filename-pattern matching across subdirectory boundaries in one call — `EntryPattern` matches only the LEAF filename per Phase 1 (see "Open design decisions" #3 for how a full-relative-path variant might extend this) |
| Single-level listing (default, like `ls`) AND an opt-in recursive mode (like `ls -R`/`find`) — both via one `Dir` declaration, `Recursive bool` option | A generic recursive WALK/streaming API (`ports.SourceAdapter`-shaped) — Phase 1 returns one `[]DirEntry` slice per `List` call, not a stream; see [Streaming Walk Adapter for Files & Directories](dir-walk-adapter.md), a separate idea-only sketch, for a natural Phase 2 if a use case appears |
| Default target path = `"."` (current directory) when the `Dir`'s own template has no `{var}` segments and the caller passes no override | Path resolution relative to something OTHER than the process's current working directory (e.g. an `fs.FS` root) — Phase 1 is `os`-rooted, exactly like `ports.File` today |
| Distinguishing files vs. directories in each `DirEntry` (`EntryKind`) | Distinguishing further OS-level types (symlinks, sockets, devices) — Phase 1 treats anything that is not a directory as a "file" (matches `os.DirEntry.IsDir()`'s own binary distinction) |

## Toolchain / dependency decisions

No new dependency — built entirely on `os.ReadDir`/`filepath.WalkDir`
(stdlib) and the existing `internal/templatematch` core `ports.File`
already uses for path-template matching. Symmetric with `ports.File`'s
own "stdlib only, no new dependency" precedent.

## API surface

New file: `ports/dir.go`.

```go
// DirPathParam describes a {varName} placeholder in a [Dir] path template —
// exact mirror of [FilePathParam]. Implements the [DirOpt] interface.
type DirPathParam struct {
    Name  string
    Codec *codex.Codec[string]
}

func (p DirPathParam) WithCodec(c codex.Codec[string]) DirPathParam
func (p DirPathParam) applyDir(db *dirBuilder)

// EntryKind distinguishes a listed entry's kind.
type EntryKind int

const (
    EntryFile EntryKind = iota
    EntryDir
)

// EntryParam describes a {varName} placeholder in an [EntryPattern]'s
// filename template — exact mirror of [DirPathParam]/[FilePathParam], but
// scoped to one entry's LEAF filename rather than a full path.
type EntryParam struct {
    Name  string
    Codec *codex.Codec[string]
}

// EntryPattern optionally declares the expected SHAPE for entries inside a
// [Dir], matched against each entry's RelPath (NOT just its leaf Name) —
// e.g. Template: "{useCase}.json" for non-recursive listings, or
// "{env}/{useCase}.json" to span subdirectory segments when [WithRecursive]
// is set (RelPath == Name for a non-recursive listing, so a leaf-only
// template like "{useCase}.json" still works unchanged there — matching
// against RelPath uniformly means Phase 1 needs no separate recursive-only
// matching rule). When a [Dir] has no EntryPattern set, List returns every
// entry with Vars == nil (no parsing attempted, nothing filtered). When
// set, List uses the SAME [internal/templatematch] core [File.MatchPath]
// uses today to extract + validate each entry's variables, and SILENTLY
// EXCLUDES any entry whose RelPath does not match the template's shape at
// all (EntryPattern acts as both a filter AND a parser — e.g. a stray
// ".gitkeep" alongside "{useCase}.json" files never appears in the result).
type EntryPattern struct {
    Template string
    Params   []EntryParam
}

// DirEntry is one listed filesystem entry. When [Dir.EntryPattern] is set,
// every DirEntry in a [Dir.List] result already matched it (non-matching
// entries are excluded, not returned with a flag — see [EntryPattern]'s
// own doc).
type DirEntry struct {
    Name    string            // leaf filename, e.g. "temp-sensor.json"
    RelPath string            // path relative to the listed directory (== Name unless Recursive)
    Kind    EntryKind
    Vars    map[string]string // populated only if Dir.EntryPattern is set; nil otherwise
}

// Dir declares a directory-listing port — the [ports.File]-equivalent
// declarative surface for enumerating a directory's entries instead of
// reading one file's typed content.
type Dir struct {
    // unexported: template, params []DirPathParam, entry EntryPattern, recursive bool
}

type DirOpt interface{ applyDir(*dirBuilder) }

// WithEntryPattern and WithRecursive are [DirOpt]s (mirrors [FilePathParam]
// being passed directly as a [FileOpt]).
func WithEntryPattern(p EntryPattern) DirOpt
func WithRecursive(recursive bool) DirOpt

// NewDir declares the directory port for template (may contain {var}
// segments, validated via DirPathParam opts) — mirrors [NewFile]'s shape
// exactly, pure/no I/O at declaration time.
func NewDir(template string, opts ...DirOpt) Dir

// BuildPath mirrors [File.BuildPath] — builds + validates the concrete
// directory path from vars.
func (d Dir) BuildPath(vars map[string]string) (string, error)

// MatchPath mirrors [File.MatchPath] — the inverse of BuildPath: matches an
// already-discovered directory path (e.g. from the caller's own
// filepath.WalkDir) against the Dir's OWN path template (NOT the
// EntryPattern — this validates the DIRECTORY's location, same as
// File.MatchPath validates a FILE's location) and returns the extracted
// variable values, validated against each registered [DirPathParam.Codec].
// Returns [DirPathMismatchError] on a structural mismatch, [DirPathParamError]
// on a codec failure.
func (d Dir) MatchPath(path string) (map[string]string, error)

// DirOptions mirrors [FileOptions] (Observer, Context — no format, a
// directory listing has no typed payload).
type DirOptions struct {
    Observer stats.Observer
    Context  context.Context
}

// List builds the concrete directory path from vars (defaulting to "." when
// the Dir's template has no {var}s and vars is empty/nil — see Scope
// decisions), reads its entries via os.ReadDir (or filepath.WalkDir when
// Recursive), and — if an EntryPattern is declared — matches/validates each
// entry's filename via the same core File.MatchPath uses.
//
// Errors:
//   - [DirPathParamError] / [MissingDirPathVarError] — directory path variable validation failure (no I/O)
//   - [DirReadError] — os.ReadDir/filepath.WalkDir failure
//   - [DirEntryParamError] — an entry's RelPath matched EntryPattern's template shape but failed a param's codec. An entry whose RelPath does NOT match the template's shape at all is silently excluded, not an error — see [EntryPattern]'s own doc.
func (d Dir) List(vars map[string]string, opts DirOptions) ([]DirEntry, error)
```

## Structured errors (all implement `slog.LogValuer`)

| Type | Fields | Mirrors |
|---|---|---|
| `DirPathParamError{Name, Value string, Err error}` | directory path variable failed its codec (from `BuildPath` or `MatchPath`) | `FilePathParamError` |
| `MissingDirPathVarError{Name string}` | `vars` missing a declared directory `{var}` (from `BuildPath`) | `MissingFilePathVarError` |
| `DirPathMismatchError{Template, Path string}` | `MatchPath` given a path that doesn't structurally match the `Dir`'s own template | `FilePathMismatchError` |
| `DirReadError{Path string, Err error}` | `os.ReadDir`/`filepath.WalkDir` failure | `FileReadError` |
| `DirEntryParamError{Entry, Name, Value string, Err error}` | an entry's `RelPath` matched `EntryPattern.Template`'s shape but a param codec failed | `FilePathParamError` |

All follow the established `Error()`/`Unwrap()`/`LogValue()` pattern from
`codex/errors.go`/`ports/file.go`'s existing error types — no new pattern
introduced.

## Observer integration

Uses `stats.FileObserver`'s existing `RecordFileRead` extension (a
directory listing IS a read operation, just of directory metadata instead
of file content) — type-asserted exactly like `ports.File.Read` already
does, via the same `recordFileRead(obs, path, success, duration)` helper
in `ports/file.go` (reused, not duplicated). No new observer extension.

## Unit test plan

| Test | Verifies |
|---|---|
| `TestDir_BuildPath_ValidatesVars` | `{var}` segments in the directory template validate against `DirPathParam.Codec`, mirrors `TestFile_BuildPath_*` |
| `TestDir_MatchPath_ValidatesAgainstTemplate` | An already-discovered directory path matching the `Dir`'s own template extracts + validates vars, mirrors `TestFile_MatchPath_*` |
| `TestDir_MatchPath_ReturnsMismatchError` | A structurally non-matching path surfaces `DirPathMismatchError` |
| `TestDir_List_DefaultPath_CurrentDirectory` | No vars + no template vars → lists `"."` |
| `TestDir_List_ReturnsFilesAndDirs` | A mixed directory returns entries with correct `EntryKind` for each |
| `TestDir_List_NoEntryPattern_ReturnsPlainNames` | Without `WithEntryPattern`, every `DirEntry.Vars` is nil and nothing is filtered |
| `TestDir_List_EntryPattern_ExtractsVars` | With `WithEntryPattern("{useCase}.json", ...)`, a matching file's `Vars["useCase"]` is extracted + validated |
| `TestDir_List_EntryPattern_ExcludesNonMatchingEntries` | A file whose `RelPath` doesn't match `EntryPattern.Template`'s shape at all (e.g. `.gitkeep`) is silently absent from the result — not an error, not returned with an unmatched flag |
| `TestDir_List_EntryPattern_PropagatesParamCodecError` | An entry matching the template shape but failing a param codec surfaces `DirEntryParamError` |
| `TestDir_List_Recursive_DescendsSubdirectories` | `WithRecursive(true)` surfaces entries from nested subdirectories with correct `RelPath` |
| `TestDir_List_Recursive_EntryPattern_MatchesFullRelPath` | In recursive mode, an `EntryPattern` spanning subdirectory segments (e.g. `"{env}/{useCase}.json"`) matches against the full `RelPath`, not just the leaf filename |
| `TestDir_List_NonRecursive_StaysSingleLevel` | Default (no `WithRecursive`) does NOT descend into subdirectories — matches `ls`, not `ls -R` |
| `TestDir_List_PropagatesReadError` | A non-existent/unreadable directory surfaces `DirReadError` |
| `TestDir_List_ObserverRecordsFileRead` | `stats.FileObserver.RecordFileRead` fires on success and on `DirReadError` |

## Files to create

| File | Responsibility |
|---|---|
| `ports/dir.go` | NEW — `Dir`, `DirPathParam`, `EntryPattern`, `EntryParam`, `DirEntry`, `EntryKind`, `NewDir`, `List`, `BuildPath`, all structured error types |
| `ports/dir_test.go` | NEW — full unit test plan above |
| `.github/instructions/go-codex.instructions.md` | `ports` package summary row gets `Dir`/`NewDir`/`List` mention |
| `docs/concepts/*.md` or `docs/features/*.md` | New subsection/page documenting the directory-listing port (mirrors `ports.File`'s own docs) |
| `examples/go-edge-models/models/iotedge/` | Flagship consumer: declare a `Dir` for the iotedge config-files directory with an `EntryPattern` extracting the use-case name from each filename, demonstrated in `main.go` alongside the existing `ConfigFile`/`ModuleFieldsPatch` demos |

## Out of scope (Phase 2)

- A streamed/`ports.SourceAdapter[DirEntry]`-shaped walk/watch adapter for
  reactive directory-change monitoring (this already partially exists as
  `adapters/file.WatchAdapter`, which watches for CHANGE events on a
  fixed directory — `ports.Dir.List` is a one-shot snapshot, a
  complementary capability, not a replacement) — see
  [Streaming Walk Adapter for Files & Directories](dir-walk-adapter.md),
  a separate idea-only sketch, not scoped into this feature's Phase 1.
- `fs.FS`-rooted listing (embed.FS, in-memory FS, etc.) — Phase 1 is
  `os`-rooted only, matching `ports.File`'s own current scope.
- Glob/wildcard segments in the directory path template itself — see
  [Wildcard/Glob Path Template Segments](path-template-wildcards.md), a
  separate idea-only sketch covering this for BOTH `FilePathParam` and
  `DirPathParam` symmetrically (not scoped into this feature's Phase 1).

## Resolved design decisions

1. **Unmatched-entry policy when `EntryPattern` is set — RESOLVED:
   silently EXCLUDE.** A file whose `RelPath` does not match the entry
   template's shape at all (e.g. a stray `.gitkeep`/`README.md` alongside
   `{useCase}.json` files) never appears in `List`'s returned slice —
   `EntryPattern` is both a filter AND a parser. This removes the need
   for a `DirEntry.Matched` flag entirely: every `DirEntry` returned when
   `EntryPattern` is set has ALREADY matched it, full stop.
2. **`Dir.MatchPath` — RESOLVED: include it.** `Dir` gets a `MatchPath`
   method mirroring `File.MatchPath` exactly (the inverse of `BuildPath`)
   for API symmetry with `ports.File`, even without an immediate driving
   use case in `go-edge-models` today — validates an already-discovered
   DIRECTORY path (not an entry) against the `Dir`'s own path template.
3. **Recursive mode's entry-pattern scope — RESOLVED: match against
   `RelPath` uniformly, in both modes.** `EntryPattern.Template` always
   matches a `DirEntry`'s `RelPath`, never just its leaf `Name`. In a
   non-recursive listing `RelPath == Name`, so a leaf-only template like
   `"{useCase}.json"` behaves identically to before; in a recursive
   listing, a template CAN span subdirectory segments (e.g.
   `"{env}/{useCase}.json"`) to match a nested entry's full relative
   path. One matching rule, no special-casing based on `Recursive`.

No open design decisions remain — ready for implementation.
