# Glob Path Template Segments — `ports`

> **Status:** Design complete — not yet implemented.
> [← Back to Roadmap](index.md)
>
> Follow-on to the now-SHIPPED Directory Listing Port (`ports.Dir`/
> `DirPathParam`, see `docs/features/ports.md`'s "`Dir` — listing a
> directory's entries" subsection — its roadmap doc was retired once
> shipped) — split out as its own roadmap entry since it is a genuinely
> separate design question that affects BOTH `ports.File`'s existing
> `FilePathParam` and `ports.Dir`'s `DirPathParam`, not something scoped
> into either feature's Phase 1.
>
> **Design history note:** an earlier draft of this doc modeled the
> wildcard syntax directly on MQTT's topic wildcard convention (`+`/`#`,
> whole-segment-only matching, reserved-key captures). That direction was
> explicitly REJECTED during design review — filesystem paths already
> have their own, much more familiar wildcard convention (shell glob /
> `path/filepath.Match`), and users reaching for `ports.File`/`ports.Dir`
> think in terms of THAT convention, not MQTT topics. This doc replaces
> the MQTT-mirrored design entirely with a filesystem-glob design.

## Motivation

Today both `FilePathParam` (`ports.File`) and `DirPathParam` (`ports.Dir`)
support ONLY named `{varName}` placeholders in a path template — every
segment is either literal text or a named, individually-codec-validated
variable. Neither supports an anonymous glob wildcard (e.g. "any `.json`
file under `logs/`" or "every subdirectory regardless of name, at any
depth") without declaring (and validating) a named variable for a segment
the caller doesn't actually care about.

This design gives `ports.File`/`ports.Dir` templates the SAME wildcard
vocabulary a shell/`path/filepath.Match` user already knows:

- `*` — matches any sequence of non-separator characters WITHIN a single
  path segment; may share a segment with literal text, e.g. `"app-*.json"`
  or `"logs-*/errors"`.
- `?` — matches any single non-separator character.
- `[...]` / `[^...]` / `[a-z]` — character class, same as `path/filepath.Match`.
- `**` — "globstar": matches ZERO OR MORE full path segments, usable
  anywhere in the template (not restricted to the last segment), e.g.
  `"data/**/errors"` matches `data/errors`, `data/a/errors`,
  `data/a/b/errors`, etc.

None of these wildcards produce a named capture — exactly like a shell
glob, a wildcard match is anonymous and discarded, never appearing in
`DirEntry.Vars`. Only named `{varName}` placeholders populate `Vars`,
completely unchanged from today.

## Scope decisions

| In scope (Phase 1) | Out of scope |
|---|---|
| `*`, `?`, `[...]` — full `path/filepath.Match` vocabulary, applied per path segment (a segment containing any of these is a "glob segment") | A new pattern language beyond `filepath.Match` + `**` — no brace expansion (`{a,b}`), no extglob |
| `**` — globstar; matches zero or more WHOLE segments; usable anywhere in the template (start, middle, or end), following the common "globstar" convention (bash `globstar`, most modern glob libraries) | **At most one `**` per template** — a template with two or more `**` markers PANICS at `NewDir`/`NewFile` declaration time (same precedent as the existing var-name-collision panic — a structural template error, not a runtime condition). This is a deliberate simplification: with exactly one `**`, matching reduces to simple prefix/suffix segment arithmetic (the template's segments before `**` must match the path's leading segments, the segments after `**` must match its trailing segments, and the path must have at least `len(prefix)+len(suffix)` segments) — no backtracking, no ambiguity, no "first match wins" judgment call. Multiple `**` markers would require a real backtracking matcher (regex-equivalent complexity) for negligible real-world benefit; rejected as not worth the risk. |
| **Mutually exclusive per segment**: a segment is EITHER a named `{varName}` segment (today's behavior, including `"{date}.json"` segment-sharing with literal text) OR a glob segment (`*`/`?`/`[...]`/`**`) — never both in the same segment. Simpler to parse, no ambiguous overlap between "does `{env}` or `*` win here" | Mixing `{varName}` and glob wildcards in the same segment (e.g. `"{env}-*.json"`) — rejected: avoids ambiguous parsing/precedence rules for negligible benefit |
| **No captures from glob wildcards** — `*`/`?`/`[...]`/`**` matches are anonymous, exactly like a shell glob; `DirEntry.Vars` only ever contains named `{varName}` captures, completely unchanged from today | A reserved capture key for the glob-matched remainder (the earlier MQTT-style draft's `Vars["**"]` idea) — rejected along with the MQTT-mirrored design entirely |
| **`Dir.List` glob-discovery mode**: when a `Dir`'s own template contains a glob segment, `List` switches from "list the one directory built from `vars`" to "discover every directory matching the glob template," aggregating entries across all matches into one `[]DirEntry` result | A dedicated `Dir.Glob`/`Dir.Discover` method distinct from `List` — rejected: `List` already IS "enumerate directory contents"; a globbed `Dir` is still describing "the directories I want to list," just more than one at a time |
| **`vars` remains a filter for named segments only, in glob-discovery mode**: a named `{varName}` segment supplied in `vars` is matched literally (narrows discovery to that value); one NOT supplied becomes a per-match capture (same relaxation `Dir.List` already needs for glob-discovery, independent of the glob segments themselves) | Requiring every named var to be supplied even in glob-discovery mode — rejected: contradicts the "discover what you don't already know" motivation |
| **`NewDir` validates at declaration time** that `DirPathParam` names and `EntryPattern`'s `EntryParam` names never collide — panics on a collision (programming error, same precedent as `NewFile`/`NewCache`'s existing panics) | Silently letting one var clobber the other, or namespacing them — rejected: added ceremony for a case that should never happen |
| **`BuildPath`/`MatchPath` reject a glob-enabled template** with a new typed error (`FileWildcardBuildError`/`DirWildcardBuildError`) — building or matching a SINGLE concrete path from a template that can match multiple paths is undefined | Making `MatchPath` "smart" about globs (e.g. returning the first match) — rejected: `MatchPath`'s contract is "this ONE path against this ONE template"; a globbed template isn't that contract anymore, `List` is |
| **`FilePathParam` gets the SAME glob segment syntax**, for symmetry, usable via `File`'s own `MatchPath` (matching an externally-discovered path) | A new bulk-discovery method on `File` (`File.Glob`) mirroring `Dir.List`'s glob-discovery — rejected: `Dir.List` (discover) + `File.Read`/`Write`/`Delete` per discovered entry (already demonstrated in `examples/dir-io`'s `ports.SourcePort` pipeline composition) is the natural, ALREADY-SHIPPED composition |

## API surface

### `ports/dir.go` / `ports/file.go` additions

```go
// Glob segment syntax — reused by both FilePathParam-backed [File] path
// templates and [Dir] templates (own path + EntryPattern), modeled on
// the shell glob / path/filepath.Match vocabulary already familiar to
// filesystem users:
//
//   *       matches any sequence of non-separator characters within one
//           path segment; may share a segment with literal text
//           (e.g. "app-*.json", "logs-*/errors").
//   ?       matches any single non-separator character.
//   [...]   character class ([abc], [^abc], [a-z]) — same rules as
//           path/filepath.Match.
//   **      "globstar" — matches zero or more WHOLE path segments;
//           usable anywhere in the template (not restricted to the last
//           segment), e.g. "data/**/errors" matches "data/errors",
//           "data/a/errors", "data/a/b/errors", ...
//
// A segment is EITHER a named {varName} placeholder (today's behavior,
// unchanged, including "{date}.json" segment-sharing with literal text)
// OR a glob segment containing */?/[...] — never both in the same
// segment. Glob matches are always anonymous: they never populate
// DirEntry.Vars, exactly like a shell glob has no named captures.
// A template containing "**" or any glob segment is "glob-enabled".

// Dir.List, when d's own template is glob-enabled, switches to
// glob-discovery mode: instead of listing the ONE directory built from
// vars, it discovers EVERY directory matching the glob template (via
// filepath.WalkDir from the template's literal prefix, testing each
// visited directory's remaining segments against the glob) and
// aggregates their entries into one result.
//
// vars remains a filter for named {varName} segments only (glob
// segments contribute no vars): a named segment supplied in vars
// narrows discovery to that literal value; an unsupplied named segment
// becomes a per-match capture, same relaxation glob-discovery already
// needs. Each returned DirEntry's Vars map contains directory-level
// named captures merged with any EntryPattern-captured vars.
//
// Errors:
//   - [DirEntryParamError] — an entry's RelPath matched EntryPattern's shape but failed a param's codec (unchanged)
//   - [DirReadError] — filepath.WalkDir failure during discovery (unchanged error type, new trigger)
// No error for zero matching directories — returns an empty slice, same
// precedent as List on an empty (non-glob) directory today.
func (d Dir) List(vars map[string]string, opts DirOptions) ([]DirEntry, error)

// BuildPath/MatchPath reject a glob-enabled template outright — a
// glob-enabled template does not describe a single concrete path.
func (d Dir) BuildPath(vars map[string]string) (string, error)  // returns DirWildcardBuildError
func (d Dir) MatchPath(path string) (map[string]string, error)  // returns DirWildcardBuildError

// FilePathParam-backed File templates get the same glob syntax, usable
// via MatchPath (matching an externally-discovered path found via
// Dir.List's own glob-discovery, or the caller's own filepath.WalkDir).
// File.BuildPath also rejects a glob-enabled template
// (FileWildcardBuildError) — File has no List-equivalent bulk-discovery
// method; compose Dir.List (discover) + File.Read/Write/Delete
// (per-entry) instead, exactly as examples/dir-io already demonstrates.
func (fh File[T]) MatchPath(path string) (map[string]string, error)  // glob-enabled templates supported
func (fh File[T]) BuildPath(vars map[string]string) (string, error)  // returns FileWildcardBuildError
```

### Matching algorithm

Segment-by-segment comparison between the template and a concrete path:

- A literal or named-var segment matches exactly as today (`MatchNonWildcard`).
- A glob segment (containing `*`/`?`/`[...]`) matches via `path/filepath.Match(pattern, segment)` — Go's stdlib already implements this exact vocabulary correctly, no need to reimplement it.
- A `**` segment matches zero or more whole segments. Because Phase 1 caps templates at **one `**` per template**, this is deterministic arithmetic, not backtracking: split the template's segments into a prefix (before `**`) and a suffix (after `**`); the prefix must match the path's leading segments in order, the suffix must match its trailing segments in order, and the path must have `len(path segments) >= len(prefix)+len(suffix)` — everything in between is the (uncaptured) globstar match. A second `**` in the same template is rejected at declaration time (`NewDir`/`NewFile`), not at match time.

No new external dependency — `path/filepath.Match` covers `*`/`?`/`[...]` entirely; only `**`'s prefix/suffix split is new code.

**Accepted risk — wildcard-first-segment discovery, mitigated by `WithBaseDir`**: a glob-enabled `Dir` template MAY start with a wildcard segment (e.g. `"*/errors"` or `"**/secret.json"`), which has no literal anchor near the start. To give callers explicit control over where `Dir.List`'s glob-discovery walk begins (rather than silently defaulting to a potentially huge scan), `Dir` gains a new declaration-time option:

```go
// WithBaseDir sets the filesystem root Dir.List's glob-discovery mode
// walks from, prepended (via filepath.Join) to the template's own
// literal prefix (the longest run of non-glob segments at the start of
// the template, possibly empty). Defaults to "." (the current working
// directory) when unset — this keeps existing literal-prefixed
// templates' behavior completely unchanged (filepath.Join(".", "configs")
// == "configs"), and gives wildcard-first templates (e.g. "*/errors",
// "**/secret.json") an explicit, safe anchor instead of an implicit,
// unbounded cwd scan.
//
// Only meaningful for glob-enabled Dir templates in List's
// glob-discovery mode — ignored otherwise (BuildPath/List's non-glob
// path already resolves relative to cwd exactly as today).
func WithBaseDir(path string) DirOpt
```

This unifies both cases under one mechanism: the actual glob-discovery walk root is always `filepath.Join(baseDir, literalPrefix)`, where `baseDir` defaults to `"."`. Callers with a wildcard-first template are expected to set `WithBaseDir` explicitly when they want to bound the scan (e.g. `WithBaseDir("/var/data")` for `"**/secret.json"` instead of scanning from cwd); `Dir.List` still works without it (falls back to `.`), but `docs/features/ports.md` will document the performance caveat and recommend `WithBaseDir` for any wildcard-first template.

## Structured errors (all implement `slog.LogValuer`)

| Type | Fields | Fires when |
|---|---|---|
| `DirWildcardBuildError{Template string}` | directory template | `Dir.BuildPath`/`Dir.MatchPath` called on a glob-enabled template |
| `FileWildcardBuildError{Template string}` | file template | `File.BuildPath` called on a glob-enabled template |

`Dir.List`'s existing `DirReadError`/`DirEntryParamError` are reused
unchanged for glob-discovery mode's own failure modes (no new error
types needed there — same triggers, new code path).

**Multiple `**` markers panic at declaration time** (`NewDir`/`NewFile`),
same precedent as the existing var-name-collision panic — a template
with two or more `**` segments is a programming error, not a runtime
condition, so it fails loud immediately rather than returning a typed
error from `List`/`MatchPath`/`BuildPath`.

## Observer integration

No new observer extension. `Dir.List`'s glob-discovery mode still fires
`stats.FileObserver.RecordFileRead` exactly once per `List` call (not
once per discovered directory) — consistent with `List`'s existing
"one call, one read event" contract; per-directory failures during the
walk still surface via the returned error, same as today.

## Unit test plan

| Test | Verifies |
|---|---|
| `TestDir_List_Glob_SingleSegment_DiscoversAllMatchingDirs` | `"logs/app-*"` discovers entries across every matching `logs/app-<anything>` directory |
| `TestDir_List_Glob_Globstar_MatchesZeroOrMoreSegments` | `"data/**/errors"` matches `data/errors`, `data/a/errors`, `data/a/b/errors` |
| `TestNewDir_Glob_PanicsOnMultipleGlobstar` | A template with two `**` segments (e.g. `"a/**/b/**/c"`) panics at `NewDir` declaration time |
| `TestDir_List_Glob_WildcardFirstSegment_WalksFromTemplateRoot` | A template starting with a wildcard segment (e.g. `"*/errors"`) with no `WithBaseDir` discovers matches by walking from `.`, documenting the accepted wider-scan tradeoff |
| `TestDir_List_Glob_WithBaseDir_AnchorsWildcardFirstSegment` | Same template WITH `WithBaseDir("/some/root")` walks from `/some/root` instead of `.` |
| `TestDir_List_Glob_WithBaseDir_JoinsWithLiteralPrefix` | A literal-prefixed template (e.g. `"configs/*/errors"`) combined with `WithBaseDir("/root")` walks from `/root/configs`, confirming existing literal-prefix behavior is unchanged when `WithBaseDir` is unset (defaults to `.`) |
| `TestDir_List_Glob_QuestionMarkAndCharClass` | `"logs/app-?.json"` / `"logs/app-[0-9].json"` match correctly per `filepath.Match` semantics |
| `TestDir_List_Glob_NoVarsCaptured` | A glob-only template's discovered `DirEntry.Vars` is empty (no anonymous captures) |
| `TestDir_List_Glob_NamedVarSupplied_FiltersLiterally` | `"logs/{env}/app-*"` with `vars={"env":"prod"}` only discovers `prod` directories |
| `TestDir_List_Glob_NamedVarUnsupplied_CapturedPerMatch` | Same template with `vars=nil` discovers ALL envs, each `DirEntry.Vars["env"]` populated per match |
| `TestDir_List_Glob_MergesDirAndEntryVars` | Directory-level named captures and `EntryPattern`-captured vars both appear in the same `DirEntry.Vars` map |
| `TestNewDir_Glob_PanicsOnVarNameCollision` | `DirPathParam`/`EntryParam` sharing a name panics at `NewDir` declaration time |
| `TestDir_BuildPath_GlobTemplate_ReturnsDirWildcardBuildError` | `BuildPath` on a glob-enabled `Dir` returns the new typed error |
| `TestDir_MatchPath_GlobTemplate_ReturnsDirWildcardBuildError` | `MatchPath` likewise |
| `TestFile_MatchPath_GlobTemplate_MatchesExternallyDiscoveredPath` | `File`'s `MatchPath` works against a glob-enabled template, including `**` |
| `TestFile_BuildPath_GlobTemplate_ReturnsFileWildcardBuildError` | `File.BuildPath` on a glob-enabled template returns the new typed error |
| `TestDir_List_NoGlob_Unaffected` | A regular (non-glob) `Dir` template's `List`/`BuildPath`/`MatchPath` behave EXACTLY as before — zero regression |

## Files to create / change

| File | Responsibility |
|---|---|
| `ports/dir.go` | Glob detection in `NewDir`/`List`/`BuildPath`/`MatchPath`; glob-discovery mode in `List`; `DirWildcardBuildError` type; declaration-time var-name-collision panic; declaration-time multiple-`**` panic; new `WithBaseDir` `DirOpt` |
| `ports/file.go` | Glob detection in `MatchPath`/`BuildPath`; `FileWildcardBuildError` type |
| `internal/templatematch` (or a new sibling `internal/globmatch`) | New matching function implementing per-segment `filepath.Match` + `**` globstar expansion — kept separate from `MatchMQTTWildcard`/`MatchNonWildcard` since the semantics no longer overlap (whole-segment-only vs. shell-glob) |
| `ports/dir_test.go` | New glob tests above |
| `ports/file_test.go` | New glob `MatchPath`/`BuildPath` tests above |
| `.github/instructions/go-codex.instructions.md` | `ports` row — glob segment syntax, glob-discovery mode, new error types |
| `docs/features/ports.md` | `Dir`/`FilePattern` subsections — glob syntax + glob-discovery example |
| `examples/dir-io/main.go` | New glob demo scene (glob-discovery across multiple matching directories) |

## Out of scope (Phase 2)

- `File.Glob` (bulk multi-file discovery + read in one call) — compose `Dir.List` (globbed) + `File.Read` per discovered entry instead, same pattern `examples/dir-io` already demonstrates.
- `fs.FS`-rooted glob discovery — Phase 1 stays `os`-rooted (`filepath.WalkDir`), matching every other `ports.File`/`ports.Dir` operation's current scope.
- Brace expansion (`{a,b}`) or other extglob syntax beyond `path/filepath.Match` + `**` — Phase 1 is deliberately minimal.

## Resolved design decisions

1. **Wildcard vocabulary — RESOLVED: filesystem-glob (`*`, `?`, `[...]`
   via `path/filepath.Match`, plus `**` globstar)** — NOT the MQTT `+`/`#`
   convention from the earlier draft. Confirmed with the user: filesystem
   users already know shell glob, and `ports.File`/`ports.Dir` is a
   filesystem abstraction, not a pub/sub topic abstraction.
2. **`*` may share a segment with literal text** (e.g. `"app-*.json"`) —
   confirmed with the user; this is the opposite of the rejected
   MQTT-mirrored "whole-segment purity" rule.
3. **`**` is globstar-style**: zero or more full segments, usable
   anywhere in the template, not restricted to the last segment —
   confirmed with the user.
4. **Phase 1 includes the full `path/filepath.Match` vocabulary** (`*`,
   `?`, `[...]`) plus `**`, not just `*`/`**` — confirmed with the user.
5. **`{varName}` and glob wildcards are mutually exclusive per segment**
   — a segment is either a named-var segment or a glob segment, never
   both — confirmed with the user, avoids ambiguous parsing.
6. **Glob wildcards produce NO captures** — `DirEntry.Vars` only ever
   contains named `{varName}` captures; glob matches are anonymous,
   exactly like a shell glob — confirmed with the user.
7. **`Dir.List` glob-discovery mode is kept**: a glob-enabled `Dir`
   template discovers every matching directory and aggregates entries;
   `vars` still filters/captures named segments as before, independent
   of the glob segments themselves — confirmed with the user.
8. **`BuildPath`/`MatchPath` reject glob-enabled templates** with new
   typed errors (`DirWildcardBuildError`/`FileWildcardBuildError`) —
   unchanged from the earlier draft, still applies under the new
   filesystem-glob semantics.
9. **`FilePathParam` gets the same glob syntax for `MatchPath` only** —
   no new `File.Glob` bulk-discovery method; `Dir.List` (discover) +
   `File.Read`/`Write`/`Delete` (per-entry) is the intended composition —
   unchanged from the earlier draft.
10. **At most one `**` per template — RESOLVED: cap adopted.** A second
    `**` in the same template panics at `NewDir`/`NewFile` declaration
    time. Confirmed with the user as a deliberate simplification: it
    turns `**` matching into deterministic prefix/suffix segment
    arithmetic instead of a general backtracking matcher, eliminating
    the highest-complexity/highest-bug-risk part of the whole feature,
    for negligible loss of real-world expressiveness.
11. **Wildcard-first-segment discovery — RESOLVED: allowed, risk
    accepted, mitigated via `WithBaseDir`.** A glob-enabled template MAY
    start with a wildcard segment (e.g. `"*/errors"`, `"**/secret.json"`).
    The user explicitly chose NOT to require a literal first segment,
    but then asked for an explicit way to bound the scan — resolved by
    adding a new declaration-time `WithBaseDir(path string) DirOpt` that
    sets the glob-discovery walk root (joined with the template's own
    literal prefix via `filepath.Join`), defaulting to `"."` when unset.
    This unifies the literal-prefix walk-root logic and the
    wildcard-first-segment case under one mechanism with zero behavior
    change for existing literal-prefixed templates.

No open design decisions remain — ready for implementation.
