# File & Cache Merge-Field Gaps — extending "one struct, one call" to `ports.File`/`ports.Cache`

> **Status:** G1, G2, G4 SHIPPED. G3 remains deferred — no concrete use
> case has appeared, same as SSE's already-deferred equivalent question.
> [← Back to Roadmap](index.md)
>
> Produced by a `review-go-codex` skill audit of every adapter and every
> port against the merge-field / "one struct, one call" convenience pattern
> established by `api/rest`'s `NewPathParam`/`CallHandle` and already
> extended to `api/events`, `api/reqreply`, and the `ports.Pattern`
> binding layer (see `.github/instructions/go-codex.instructions.md`'s
> "Declarative Var Extraction & Merge" section and
> `docs/roadmap/merge-field-remaining-gaps.md`). This doc captures TWO
> previously-unassessed gaps found in that audit — `ports.File`/
> `adapters/file` and `ports.Cache`/`adapters/redis` — plus two smaller,
> related observations.

## Motivation

Every adapter (`nethttp`, `chi`, `mqtt`, `mqtt5`, `zeromq`, `sql`, `file`,
`redis`, `websocket`, `mcpgo`) and every port (`SourcePort`, `SinkPort`,
`IOPort`, `ToolPort`, `LatestPort`, `DuplexPort`, `File`, `Cache`,
`SQLPattern`) was reviewed against the five-point "one struct, one call"
checklist (`review-go-codex`'s Boundary Symmetry Guardrail):

1. Declare-once constructors for every var-boundary
2. Escape hatch preserved (plain validate-only params still work)
3. Encode/decode symmetry (role-aware accessors both directions)
4. Role symmetry (both sides of the boundary get the convenience)
5. Single-call convenience wrapper on encode + automatic wiring on decode

**`api/rest`, `api/events`, `api/reqreply`, and the `ports.Pattern`
binding layer (`nethttp`/`mqtt5`/`zeromq`/`mqtt`) all satisfy all five —
confirmed, not re-reported.** `api/mcp` Resources/Prompts were already
assessed in a prior round (`docs/roadmap/merge-field-remaining-gaps.md`'s
G4) — G4a (Resources' automatic URI-var extraction) shipped; G4b (full
merge-field parity for Resources/Prompts) was explicitly deferred pending
a concrete use case. **Not re-reported here.**

Two ports were found to have the DECLARE-ONCE CONSTRUCTOR (point 1) and
the MERGE-FIELDS ACCESSOR, but ZERO of points 3-5 — the "one struct, one
call" promise was never extended to them at all, despite having the same
building blocks REST/events/reqreply started from:

1. **`ports.File`** has `NewFilePathParam[T]` (declare-once, merge-capable)
   and `File[T].MergeFields()`, but `File.Read`/`File.Write` never call
   `codex.EncodeVars`/`codex.DecodeVars` with them — no `ReadMerged`
   (decode-merge) and no `WriteHandle`-equivalent (encode-side
   single-call convenience). `adapters/file`'s `ReadAdapter`/
   `ReadEachAdapter`/`DrainWriteFileAdapter` all require a **hand-written**
   `varsFor func(T) map[string]string` closure — even when `T` already
   declares its path vars via `NewFilePathParam`.
2. **`ports.Cache`** has `NewCacheKeyParam[T]` (declare-once, merge-capable)
   and `Cache[T].MergeFields()`, with a doc comment EXPLICITLY
   acknowledging "no bundling convenience method exists for Cache" — but
   the reasoning given ("`Get`/`Set` already take vars directly — no
   'body vs. vars' split to coordinate") does not hold up: `Set`'s value
   argument `v` IS the same type declaring the key vars, exactly like
   `mqtt5.PublishHandle`'s `msg` argument. `adapters/redis`'s
   `SetAdapter`/`DrainSetAdapter` require a **mandatory** hand-written
   `keyFn func(T) map[string]string` — there is no "leave nil, derive
   automatically" escape at all, unlike every `Vars`-carrying port-binding
   adapter fixed in the prior round.

Both gaps were found by reading `ports/file.go`, `ports/cache.go`,
`adapters/file/binding.go`, and `adapters/redis/binding.go` directly — not
speculation. Neither port/adapter pair appears in
`review-go-codex/references/checklist.md`'s section-12 boundary-symmetry
table at all, which is itself a process gap (the table only lists
`api/rest`/`api/events`/`api/reqreply`/the port-binding layer) — worth
fixing alongside the code (see G4).

## Scope decisions

| Gap | Boundary | Severity | In scope this round | Status |
|---|---|---|---|---|
| G1 | `ports.File`/`adapters/file` never got single-call convenience (`ReadMerged`/`WriteHandle`-equivalent) despite having `NewFilePathParam`+`MergeFields()` | `ports/file.go`, `adapters/file/binding.go` | **bug** — a shipped, declare-once constructor with no convenience wrapper is an incomplete implementation of the documented pattern | Yes | ✅ SHIPPED |
| G2 | `ports.Cache`/`adapters/redis` never got single-call convenience (`GetMerged`/`SetHandle`-equivalent) despite having `NewCacheKeyParam`+`MergeFields()`; existing doc comment's rationale for skipping it doesn't hold | `ports/cache.go`, `adapters/redis/binding.go` | **bug** — same category as G1 | Yes | ✅ SHIPPED — plus a real, pre-existing bug found and fixed while implementing (see "Implementation notes") |
| G3 | `adapters/websocket`'s upgrade path uses `rest.PathParam`/`ValidatePathParams` (validate-only) for the CONNECTION's path vars — same "per-connection vs. per-message merge" open question already deferred for SSE (`merge-field-remaining-gaps.md`'s G1) | `adapters/websocket` | Low — same open question as SSE, no concrete use case | Not actioned — track alongside SSE's G1, revisit together if a use case appears for either | Deferred |
| G4 | `review-go-codex/references/checklist.md`'s section-12 boundary-symmetry table has no `ports.File`/`ports.Cache` rows — the audit process itself doesn't check these two ports | `.github/skills/review-go-codex/references/checklist.md` | Low — process/doc gap, not a code bug | Yes — trivial, add two rows once G1/G2 land (or immediately with "❌ not yet" status, updated when shipped) | ✅ SHIPPED |

## API surface

**G1 — `ports.File` single-call convenience** (mirrors `mqtt5.PublishHandle`/
`nethttp.CallHandle` exactly):

```go
// ports/file.go

// ReadMerged is the decode-merge convenience: reads and decodes like Read,
// then merges the vars used to build the path into the SAME returned
// value via codex.DecodeVars — mirrors events.ChannelHandle.DecodeMerged.
// Additive: Read is unchanged; ReadMerged behaves identically to Read when
// MergeFields() is empty.
func (fh File[T]) ReadMerged(vars map[string]string, opts FileOptions) (T, error)

// WriteHandle is the single-call convenience wrapper around Write: it
// derives vars from v automatically via codex.EncodeVars(v,
// fh.MergeFields()...) — one struct in, no manual vars map — mirroring
// mqtt5.PublishHandle/nethttp.CallHandle's convenience. Write remains the
// lower-level escape hatch for callers that build vars themselves (e.g.
// no merge fields declared, or vars come from a non-struct source).
func WriteHandle[T any](fh File[T], v T, opts FileOptions) error

// UpdateHandle mirrors Update, deriving vars from the CURRENT value
// (read first via ReadMerged, then vars re-derived from the updated value
// for the write) — needs a design decision (see Open design decisions).
func UpdateHandle[T any](fh File[T], fn func(T) T, opts FileOptions) error
```

```go
// adapters/file/binding.go — ReadAdapter/ReadEachAdapter/DrainWriteFileAdapter
// gain a "derive automatically" path: when varsFor is nil AND the file's
// MergeFields() is non-empty (only sensible for the SAME-type case —
// DrainWriteFileAdapter[T] and ReadAdapter[T,T]-shaped uses; ReadEachAdapter's
// independent In/Resp enrichment case keeps varsFor mandatory, unchanged),
// derive vars via codex.EncodeVars automatically instead of requiring a
// hand-written closure.
```

**G2 — `ports.Cache` single-call convenience** (mirrors `mqtt5.PublishHandle`/
`zeromq.PublishHandle` exactly):

```go
// ports/cache.go

// GetMerged mirrors ReadMerged: looks up like Get, then merges the key
// vars into the returned value via codex.DecodeVars.
func (c Cache[T]) GetMerged(vars map[string]string) (T, bool, error)
// (Get is currently a free function taking client+cache — GetMerged's
// exact signature/placement needs to match Get's existing shape; see
// Open design decisions.)

// SetHandle is the single-call convenience wrapper around Set: derives
// vars from v automatically via codex.EncodeVars(v, cache.MergeFields()...).
func SetHandle[T any](ctx context.Context, client Commands, cache Cache[T], v T, opts SetOptions) error
```

```go
// adapters/redis/binding.go — SetAdapter/DrainSetAdapter gain a
// "keyFn nil -> derive automatically" path, mirroring G1's DrainWriteFileAdapter
// fix — GetAdapter's independent Req/Resp enrichment shape keeps keyFn
// mandatory (same rationale as ReadEachAdapter).
```

## Structured errors / Observer integration

No new error types for G1/G2 — `ReadMerged`/`GetMerged` reuse
`codex.ValidationErrors`/existing `FilePathParamError`/`CacheKeyParamError`
exactly as `Read`/`Get` already do (the merge step uses the SAME
`codex.DecodeVars` error shape `nethttp.Handler`'s decode-merge already
produces). `WriteHandle`/`SetHandle` reuse `codex.VarEncodeTypeError`
exactly as `PublishHandle`/`CallHandle` already do. No new observer
methods — existing `recordFileRead`/`recordFileWrite`/`RecordCacheHit`/
`RecordCacheWrite` calls are unchanged; only the VALUE SOURCE for vars
changes (derived vs. caller-supplied).

## Unit test plan

All implemented — see `ports/file_test.go`, `adapters/file/binding_test.go`,
`adapters/redis/binding_test.go`, `ports/cache_test.go`.

| ID | Test | Status |
|---|---|---|
| G1-1 | `File.ReadMerged` merges path vars into the decoded value when the file declares merge fields (regression guard: identical to `Read` when none declared) | ✅ |
| G1-2 | `WriteHandle` derives vars from v's own merge-field-declared struct fields — two values with different path-var fields write to two different concrete paths | ✅ |
| G1-3 | `file.DrainWriteFileAdapter`/`ReadEachAdapter` per-item derivation when `varsFor` is nil, plus decode-merge wiring | ✅ |
| G2-1 | `redis.GetMerged` merges key vars into the returned value (regression guard: identical to `Get` when no merge fields declared; miss behaves like a miss) | ✅ |
| G2-2 | `redis.SetHandle` derives key vars from v's own merge-field-declared struct fields | ✅ |
| G2-3 | `redis.SetAdapter`/`DrainSetAdapter` per-item derivation when `keyFn` is nil; `GetAdapter` decode-merge wiring; explicit-`keyFn`-still-wins regression | ✅ |
| G2-bonus | `CachePattern.Opts`' `NewCacheKeyParam` merge fields wired through `IOPort`/`SinkPort` (regression guard for the found-and-fixed bug) | ✅ |
| G4 | (no test — doc-only checklist correction) | ✅ |

## Files to create/change

| File | Responsibility |
|---|---|
| `ports/file.go` + `_test.go` | ✅ G1: `File.ReadMerged`, `ports.WriteHandle` (`UpdateHandle` deferred) |
| `adapters/file/binding.go` + `_test.go` | ✅ G1: `ReadEachAdapter`/`ReadAdapter` wired to `ReadMerged`; `DrainWriteFileAdapter`'s `varsFor`-nil derivation |
| `adapters/redis/binding.go` + `_test.go` | ✅ G2: `GetMerged`, `SetHandle`, `keyVarsFor` helper; `GetAdapter` wired to `GetMerged`; `SetAdapter`/`DrainSetAdapter`'s `keyFn`-nil derivation |
| `ports/handle.go` + `ports/cache_test.go` | ✅ G2 (bonus fix): both `CachePattern` build paths now delegate to `NewCache`, fixing the dropped-merge-fields bug; new regression tests |
| `.github/skills/review-go-codex/references/checklist.md` | ✅ G4: added `ports.File`/`ports.Cache` rows to the section-12 table |
| `.github/skills/review-go-codex/references/history.md` | ✅ Round 63 entry appended |
| this doc | marked SHIPPED for G1/G2/G4; G3 remains deferred |

## Open design decisions (resolved during implementation)

1. **G1 — `ReadMerged` naming and placement**: RESOLVED as leaned — both
   `File[T].ReadMerged` and `ports.WriteHandle` implemented; `ReadMerged`
   as a method (no new type param, matches `Read`/`Write`'s shape),
   `WriteHandle` as a free function (matches `PublishHandle`'s naming
   precedent, though it needs no new type param here either — kept as a
   free function for naming/discoverability symmetry with the encode-side
   convenience across every other boundary).
2. **G1 — is `UpdateHandle` in scope?** RESOLVED as deferred — NOT
   implemented this round. `Update`'s "read current, apply fn, write back"
   shape raises a genuine re-derivation question (vars from the pre- or
   post-`fn` value) that has no existing precedent to lean on; left as a
   future addition if a concrete need appears. `Update` itself is
   unchanged and remains the escape hatch.
3. **G2 — where does `GetMerged` live?** RESOLVED as leaned —
   `redis.GetMerged`/`redis.SetHandle` live in `adapters/redis` (alongside
   `Get`/`Set`), not `ports/cache.go` — mirrors `mqtt5.PublishHandle`'s
   placement in the adapter package, not `api/events`.
4. **G1/G2 — same-type-only derivation for port-binding adapters?**
   RESOLVED as leaned — `DrainWriteFileAdapter[T]`/`SetAdapter[T]`/
   `DrainSetAdapter[T]` gained `varsFor`/`keyFn`-nil automatic derivation;
   `ReadEachAdapter[In,T,Resp]`/`ReadAdapter[In,Resp]`/`GetAdapter[Req,Resp]`
   keep their closures mandatory (enrichment shape, independent types) —
   BUT both `ReadEachAdapter`/`ReadAdapter` and `GetAdapter` were ADDITIONALLY
   wired to call `ReadMerged`/`GetMerged` internally, so the DECODE-side
   convenience (merging the already-known vars into the returned value)
   applies uniformly even where the ENCODE-side automatic derivation
   doesn't — a refinement found and implemented beyond the original sketch.
5. **G3 — resolve together with SSE?** RESOLVED as leaned — tracked
   together, both deferred, no concrete use case for either.

## Implementation notes (found during implementation, not anticipated in the design above)

- **A real, pre-existing bug in `ports/handle.go`'s `CachePattern` handling**
  was found while wiring G2's tests: BOTH build paths
  (`buildEventPatternHandles` for `SinkPort`/`LatestPort`,
  `buildDualCodecPatternHandles` for `IOPort`) reconstructed `Cache[T]`
  field-by-field (`Cache[T]{Key: ..., TTL: ..., Format: ..., params:
  cb.params}`) instead of delegating to `NewCache` — silently dropping
  `cb.mergeFields` entirely. This meant `NewCacheKeyParam` registered via
  `CachePattern.Opts` was COMPLETELY INERT for every Pattern-built cache
  (only hand-built `ports.NewCache(...)` calls ever got working merge
  fields) — a significant, previously-unnoticed regression relative to
  `FilePattern`, which correctly delegates to `NewFile` and never had this
  bug. Fixed by replacing both field-by-field reconstructions with
  `NewCache[T](pat.Key, cFmt, pat.Opts...)` + `c.TTL = pat.TTL`. New
  regression tests (`TestCachePattern_NewCacheKeyParam_WiredThroughIOPort`/
  `_WiredThroughSinkPort`) lock this in.
- **`ReadEachAdapter`/`ReadAdapter`/`GetAdapter` get the decode-merge
  convenience even without encode-side automatic derivation** — since the
  vars used to look up the file/cache entry are already known (from
  `varsFor(In)`/`keyFn(Req)`) by the time the value is decoded, merging
  those SAME vars back into the returned value via `ReadMerged`/`GetMerged`
  is valid regardless of whether `In`/`Req` matches the file/cache's own
  type. This closes a decode-side gap uniformly across ALL file/cache read
  paths, not just the same-type write paths the original sketch focused on.

## Verification

Same ritual as every prior round: gofmt, `go build ./...`, `go test
./...`, `-race` on touched packages, `just check`, all examples exit 0.
