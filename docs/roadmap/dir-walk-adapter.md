# Streaming Walk Adapter for Files & Directories — `adapters/file`

> **Status:** Idea only — not designed, no use case yet.
> [← Back to Roadmap](index.md)
>
> Follow-on to [Directory Listing Port](directory-listing-port.md) — split
> out as its own roadmap entry since it is a genuinely separate design
> question (a streamed, `ports.SourceAdapter`-shaped enumeration/watch
> capability), explicitly noted as OUT of that feature's Phase 1 scope
> ("A generic recursive WALK/streaming API ... a `dir.ScanAdapter`
> (streamed entries) is a natural Phase 2 if a use case appears").

## The idea

`ports.Dir` (planned, see `directory-listing-port.md`) returns one
`[]DirEntry` snapshot per `List` call — a single-shot enumeration, not a
stream. `adapters/file.WatchAdapter` (already shipped) DOES stream, but
only emits NEWLY-CREATED file paths in one non-recursive directory,
polling on an interval, and returns plain `string` paths (no `DirEntry`
shape, no distinction between files/dirs, no `EntryPattern`-style
filename parsing).

The gap this idea would close: a `ports.SourceAdapter`-shaped adapter
that WALKS a directory tree (like `filepath.WalkDir`, recursively, in one
pass) and STREAMS every discovered entry as a `ports.DirEntry` (reusing
whatever shape `ports.Dir`/`EntryPattern` ships with), for pipelines that
want to process "every config file under this tree" reactively/
incrementally rather than collecting a full slice up front — plus,
potentially, a genuinely CONTINUOUS watch mode that also detects
modifications and deletions (not just new files), which
`WatchAdapter`'s current polling-for-new-files-only design does not
attempt.

## Why this isn't scoped into any current Phase 1

- **Not requested by a concrete use case yet** — no consumer in
  `go-edge-models` needs streamed/incremental directory processing today;
  `ports.Dir.List`'s one-shot slice (once shipped) is sufficient for the
  iotedge use-case-discovery driver.
- **Depends on `ports.Dir`/`DirEntry` shipping first** — this idea reuses
  whatever `DirEntry`/`EntryPattern` shape `directory-listing-port.md`
  ships with (once implemented); designing the streaming variant before
  the one-shot `List` API is stable would risk having to redesign both
  together.
- **The biggest open question is genuinely a dependency/architecture
  fork**, not a small detail:
  - **Polling-based** (extends `WatchAdapter`'s existing approach — an
    interval timer + `os.ReadDir`/`filepath.WalkDir` re-scan, diffing
    against previously-seen entries): stays stdlib-only, matches
    `adapters/file`'s current "no external dependency" convention
    exactly, but is inherently latency-bound (only as responsive as the
    poll interval) and CPU-wasteful on large trees (full re-scan every
    tick).
  - **Event-based** (OS-native filesystem notifications — inotify/kqueue/
    ReadDirectoryChangesW): near-instant reaction to changes, no
    unnecessary re-scanning, but requires a NEW external dependency
    (there is no filesystem-event-notification API in the Go standard
    library) — most likely `fsnotify/fsnotify`, the same library nearly
    every other Go project reaches for. Would be the FIRST external
    dependency introduced into a currently-stdlib-only adapter package,
    a meaningfully bigger toolchain decision than anything else in this
    roadmap and one that needs its own dedicated evaluation (license,
    maintenance activity, platform support matrix, CGO-free requirement)
    before being decided either way.
- **Semantic scope questions**, independent of the above fork:
  - One-shot recursive WALK (enumerate the whole tree once via
    `filepath.WalkDir`, stream each `DirEntry` as it's discovered, then
    close — the streaming-shaped sibling of `ports.Dir.List`, no
    ongoing watch) vs. a genuinely CONTINUOUS watch (runs until `ctx` is
    cancelled, like `WatchAdapter` today) — these are two different
    capabilities and may both be worth having as separate adapters
    rather than one adapter trying to do both.
  - Should a continuous watch mode detect modifications and deletions
    (not just creations, unlike `WatchAdapter`'s current scope) — this
    changes the emitted item shape (needs an explicit "created/modified/
    deleted" event kind, not just a bare `DirEntry`).
  - Does `EntryPattern`-based filtering apply here too, mirroring
    `ports.Dir.List`'s silent-exclude behavior (resolved in
    `directory-listing-port.md`)?

## Relationship to what already exists (do not duplicate)

- `adapters/file.ScanAdapter` — reads ONE known file's lines/records; not
  a directory enumerator at all, orthogonal to this idea.
- `adapters/file.WatchAdapter` — already ships the "poll a directory,
  stream new file paths" capability in its narrowest form (non-recursive,
  new-files-only, plain `string` paths). Any polling-based design here
  would likely EXTEND or PARAMETERIZE `WatchAdapter` rather than
  duplicate it outright — but that's an implementation-time decision, not
  settled by this sketch.

## Next step (when a use case appears)

Write a proper roadmap doc (following the standard template) once (1)
`ports.Dir`/`DirEntry` has actually shipped (this idea builds directly on
its shape), and (2) there is a concrete driver that resolves at minimum:
polling vs. event-based (and, if event-based, which dependency and
platform support matrix), one-shot walk vs. continuous watch (or both, as
separate adapters), and whether continuous watch needs to track
modify/delete events beyond today's create-only scope.
