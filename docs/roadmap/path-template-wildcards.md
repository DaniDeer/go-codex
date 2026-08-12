# Wildcard/Glob Path Template Segments — `ports`

> **Status:** Idea only — not designed, no use case yet.
> [← Back to Roadmap](index.md)
>
> Follow-on to [Directory Listing Port](directory-listing-port.md) — split
> out as its own roadmap entry since it is a genuinely separate design
> question that would affect BOTH `ports.File`'s existing `FilePathParam`
> and the planned `ports.Dir`'s `DirPathParam`, not something scoped into
> either feature's Phase 1.

## The idea

Today both `FilePathParam` (`ports.File`, shipped) and `DirPathParam`
(`ports.Dir`, planned — see `directory-listing-port.md`) support ONLY
named `{varName}` placeholders in a path template — every segment is
either literal text or a named, individually-codec-validated variable.
Neither supports an actual wildcard/glob segment (e.g. `*` matching "any
single path segment, value discarded" or `**` matching "any number of
segments"), the way `filepath.Glob`/shell globbing does.

The question a future roadmap round would need to answer: **should
go-codex's path-template vocabulary grow a wildcard/glob segment type**,
symmetrically added to both `FilePathParam` and `DirPathParam` at once
(so a caller who learns the pattern for one template type gets the same
capability on the other), for cases like "match any file under
`logs/*/`" or "list every subdirectory regardless of name" without
needing to declare (and validate) a named variable for a segment whose
value the caller doesn't actually care about.

## Why this isn't scoped into any current Phase 1

- **Not requested by a concrete use case yet** — the iotedge consumer
  driving `directory-listing-port.md` always cares about the extracted
  variable value (e.g. `{useCase}` from a filename); it never needs "any
  segment, don't care what."
- **`BuildPath` has no meaningful semantics for a wildcard segment** — you
  cannot construct a concrete path from a template containing a bare `*`
  without SOME value for that segment, so a wildcarded template would
  only make sense for the MATCHING direction (`MatchPath`/`List`/
  `filepath.Glob`-style discovery), not the building direction
  (`BuildPath`) both `FilePathParam` and `DirPathParam` currently support
  symmetrically. Introducing an asymmetric capability (works for
  match, not for build) is exactly the kind of inconsistency this
  library's own review discipline (`review-go-codex` skill) would flag —
  needs a real design pass, not a quick addition.
- **Semantic questions are genuinely open**, e.g.:
  - Single-segment wildcard (`*`, like `FilePathParam`'s existing `{var}`
    capture-until-next-`/` semantics, but the value is discarded/unnamed)
    vs. multi-segment/recursive wildcard (`**`, like `filepath.Glob`'s
    doublestar or `**` in gitignore-style patterns) — are both needed, or
    just one?
  - Can a wildcard segment coexist with named `{var}` segments in the
    SAME template (e.g. `"logs/*/{date}.json"`)? If so, what governs
    ordering/precedence when both a wildcard library (stdlib
    `filepath.Match`) and the existing `internal/templatematch` core are
    involved — do they need to be merged into one matching engine, or
    can wildcard matching be layered as an independent pre/post pass?
  - Does a wildcard segment get its own named type (e.g. `WildcardParam`)
    to keep `FilePathParam`/`DirPathParam` themselves free of dual
    semantics, or does the SAME param type grow a "this is a wildcard,
    not a validated variable" mode (a `Codec == nil` "any value, no
    capture" sentinel — the kind of implicit dual-meaning the project's
    own design conventions try to avoid; a dedicated type is likely
    cleaner)?

## Next step (when a use case appears)

Write a proper roadmap doc (following the standard template) once there
is a concrete driver, resolving at minimum: (1) single- vs. multi-segment
wildcard scope, (2) whether it lands on `FilePathParam`/`DirPathParam`
directly or a new dedicated param type, and (3) whether `BuildPath`
should reject a wildcarded template outright (compile-time-ish panic at
`NewFile`/`NewDir` declaration) or simply be unavailable/undefined for
templates containing one.
