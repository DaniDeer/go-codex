# Declarative Var Extraction & Merge — `codex.DecodeVars`/`EncodeVars`

> **Status:** ✅ FEATURE COMPLETE. Rounds 1, 3, 4 SHIPPED (REST — request+
> response merge, role-aware client encode, single-call convenience, nested
> structs, binary formats). **Phase 2 SHIPPED** (events/reqreply/cache).
> [← Back to Roadmap](index.md)

## Shipped history (Rounds 1, 3, 4)

This feature turns `codex.Field[T,F]` (already used for `codex.Struct[T]`'s
JSON object fields) into a general-purpose primitive for ANY string-keyed
source — HTTP path/query/header/cookie params, MQTT/event topic vars, file
path segments, cache key segments, env vars — via
`codex.DecodeVars`/`EncodeVars`, which decode/encode a
`map[string]string` using the SAME `RequiredField`/`OptionalField`/
`DefaultField` declarations already in every codebase. The full original
motivation, rejected alternatives (making the 7 existing Param types
generic), and detailed API design are preserved in git history for this
file (see `git log -- docs/roadmap/vars-codec-merge.md`) — this doc no
longer re-derives shipped design decisions; the code and the three doc
surfaces below are the source of truth going forward.

**Round 1 (2026-07) — foundation + REST + File:**
- `codex.FieldCodec[T]` export, `codex.DecodeVars`/`EncodeVars`,
  `codex.VarEncodeTypeError`.
- `ports.File.MatchPath` (inverse of `BuildPath`, mirrors
  `mqtt.TopicVarsFromMessage`), `ports.NewFilePathParam[T]`,
  `ports.FilePathMismatchError`.
- REST: `rest.NewPathParam[T]`, `NewRequiredQueryParam[T]`/
  `NewOptionalQueryParam[T]` (+ Header/Cookie), `RouteHandle.MergeFields()`/
  `DecodeMerged`, `nethttp`/`chi` adapter auto-merge wiring.
- **Role-aware split** (same cycle): `RouteHandle.MergeFields()` was a flat,
  role-erased list — safe for decode, unsafe for the client ENCODE
  direction (`nethttp.CallOptions.QueryParams`/`HeaderParams`/`CookieParams`
  add every map entry with no name filtering). Fixed with four role-scoped
  accessors: `PathMergeFields()`/`QueryMergeFields()`/`HeaderMergeFields()`/
  `CookieMergeFields()`.
- Reference: `.github/instructions/go-codex.instructions.md` ("Declarative
  Var Extraction & Merge" section), `docs/features/rest-api.md`,
  `docs/features/ports.md`, `docs/concepts/codec.md`, `examples/adapters-nethttp`,
  `examples/adapters-chi`.

**Round 3 (2026-07) — REST response merge + single-call client
convenience:** closed the remaining REST gap — response headers/cookies
were validate-only (no merge), and the client still assembled 3-4 maps by
hand.
- `rest.NewRequiredResponseHeaderParam[Resp]`/`NewOptionalResponseHeaderParam[Resp]`
  (+ Cookie), `RouteHandle.ResponseHeaderMergeFields()`/
  `ResponseCookieMergeFields()`, `RouteHandle.DecodeMergedResponse`.
- `nethttp`/`chi` `Handler` auto-encodes response merge fields after the
  handler returns; `nethttp.Call` auto-decodes them back into `Resp`.
- `nethttp.CallHandle[Req, Resp]` — single-call client convenience,
  derives every request-side map from `req` automatically; explicit
  `CallOptions` entries win on key collision.
- Reference: `docs/features/rest-api.md` ("Response merge fields", "One-line
  client calls — CallHandle"), `examples/adapters-nethttp-client` (2b/2c).

**Round 4 (2026-07) — nested structs & binary formats:** proved (not just
theorized) that the pattern is neither JSON-specific nor
flat-struct-specific.
- Body decode/encode is orthogonal to var-merge — any `format.Format[T]`
  (Gob, Binary, custom) composes with merge fields unchanged.
- Merge-field `get`/`set` are plain closures — nested sub-struct access
  (`func(r Req) string { return r.Meta.X }`) needs zero framework changes.
- **Correction found during implementation**: `format.Gob(codec)`
  serialises the WHOLE typed value via `encoding/gob`'s own reflection,
  bypassing the codec's `Encode`/`Decode` for wire bytes entirely — it does
  NOT let you project onto just a nested sub-field the way
  `codex.MapCodecSafe` does for JSON/YAML/TOML. The correct primitive for
  "wire bytes represent ONLY a nested sub-field" with Gob/protobuf/custom
  binary is `format.NewTyped` with a custom marshal/unmarshal.
- Reference: `docs/features/rest-api.md` ("Nested structs & binary body
  formats"), `examples/rest-nested-binary`,
  `api/rest/builder_test.go`'s `TestNestedStructMergeFields_GetSetReachIntoSubstruct`/
  `TestGobBodyFormat_ComposesWithNestedMergeFields`. Mandated policy-wide
  in `.github/instructions/go-codex.instructions.md`, `add-a-new-adapter`'s
  Step 5b, `plan-a-new-codex-feature`'s item 6, `review-go-codex`'s
  Boundary Symmetry Guardrail + checklist category 12.

**Verification ritual for all three rounds** (repeat for Phase 2): gofmt,
`go build ./...`, `go test ./...`, `-race` on touched packages, `just
check`, all 50+ examples exit 0.

## Phase 2 — events, reqreply, cache — ✅ SHIPPED (2026-07)

> **Policy context:** the "one struct, one call" pattern Rounds 1/3/4
> established for REST is a MANDATORY design contract for every `api/*`
> builder-backed boundary with a request/response or duplex role shape —
> see `.github/instructions/go-codex.instructions.md`'s "MANDATORY design
> contract: one struct, one call" section and the `add-a-new-adapter`
> skill's "Step 5b". Phase 2 closed that mandate for
> `api/events`/`api/reqreply`/`ports.Cache`, against that checklist
> (declare-once constructors, escape hatch, encode/decode symmetry, role
> symmetry, single-call wrapper) AND the Round 4 mandate (non-JSON payload
> formats, nested struct composition, from day one — not retrofitted
> afterward).

**Shipped:**
- `api/events`: `NewTopicParam[T]`, `ChannelHandle.MergeFields()`/
  `DecodeMerged` (single flat slice — events has only ONE var destination,
  no role-split needed).
- `adapters/mqtt5`: new `TopicVarsFromMessage[T]` prerequisite (mirrors
  `adapters/mqtt`'s v3 version), `Subscribe` auto-merge wiring,
  `PublishHandle[T]` single-call convenience.
- `api/reqreply`: `NewTopicParam[T]` (Req-side only — resolved: the reply
  is correlated by the transport, not by re-encoding topic vars into
  `Resp`), `RouteHandle.MergeFields()`/`DecodeMerged`.
- `adapters/mqtt5`: `Serve` auto-merge wiring, new `CallHandle[Req,Resp]`.
- `adapters/zeromq`: new `CallHandle[Req,Resp]` — **client-side only**, a
  genuine finding during implementation: `Serve` reads raw socket frames
  with NO per-message topic string at all (routing is socket-based, the
  topic is a static observer-reporting label only) — zeromq CANNOT support
  server-side decode-merge for topic vars, documented as an intentional
  transport limitation, not a gap.
- `ports.Cache`: `NewCacheKeyParam[T]`, `Cache.MergeFields()` — simplest
  boundary, no role symmetry, no single-call wrapper needed.
- Tests: EV1–EV7 (events + mqtt5), RR1–RR7 (reqreply + mqtt5/zeromq), C1–C2
  (cache) — all passing, including nested-struct + Gob-payload round trips
  for both events and reqreply (mirrors Round 4's rigor for REST).
- New example `examples/events-nested-binary` — transport-agnostic
  demonstration of nested payload + topic merge + Gob body (via
  `format.NewTyped` projection), mirroring `examples/rest-nested-binary`.
- Docs: `docs/features/events.md` ("Topic vars with automatic merge" +
  "Nested structs & non-JSON payloads"), `docs/features/ports.md` (new
  Cache section), `.github/instructions/go-codex.instructions.md`.

**Verification:** gofmt clean, `go build ./...` clean, full
`go test ./...` clean, `-race` on `api/events`/`api/reqreply`/
`adapters/mqtt5`/`adapters/zeromq`/`ports` clean, `just check` 0 issues,
all 52 examples (including the new one) exit 0.

### Design notes (as implemented)

The sections below capture the pre-implementation design (motivation,
scope, API surface, test plan) — kept for historical reference; all open
design decisions listed at the end were resolved as noted in "Shipped"
above.

### Motivation

Round 1 deliberately deferred events/reqreply/cache — REST was the
explicitly-requested, highest-value surface. Two sessions later, auditing
`api/events/builder.go`, `api/reqreply/route.go`, `ports/cache.go`, and
`adapters/mqtt5/adapter.go` found these boundaries have made ZERO progress
since Round 1 (no merge-field constructors exist at all) — meanwhile REST's
shape has evolved twice (role-aware split, response merge, `CallHandle`).
This section replaces the original "mechanical repeat" framing (which
undersold real design differences between REST's 4-role HTTP shape and
events/reqreply's single-topic shape) with a fully worked design.

### Scope decisions

| Boundary | Shape | In scope | Key difference from REST |
|---|---|---|---|
| `api/events` (pub/sub) | single type `T`, ONE var destination (topic) | `NewTopicParam[T]`, `ChannelHandle.DecodeMerged`, `mqtt5.PublishHandle` (single-call) | No role-aware split needed — only one destination, no cross-role leak risk possible |
| `api/reqreply` (req/reply) | `Req`/`Resp`, ONE shared topic template for both directions | `NewTopicParam[T]` (Req-side), `RouteHandle.DecodeMerged`, single-call client wrapper (adapter-specific: `mqtt5`/`zeromq`) | Open question: does Resp need its own topic-merge role? (see Open design decisions) |
| `ports.Cache` | key/value, no request/response, key built FROM known values | `NewCacheKeyParam[T]`, `Cache.MergeFields()` | Simplest — no `MatchPath`-equivalent, no role symmetry, no single-call wrapper needed (`Cache.Get`/`Set` already take vars directly) |

Out of scope for Phase 2 (unless a concrete need surfaces): reqreply
response-side topic merge (see Open design decisions), a `Cache`
`MatchKey` inverse (cache keys are never reverse-matched from a discovered
key, unlike file paths).

### A genuine prerequisite gap found this session

`adapters/mqtt5` has **no** `TopicVarsFromMessage` equivalent — the older
`adapters/mqtt` (v3) package already has one
(`mqtt.TopicVarsFromMessage[T](handle, msg) (map[string]string, error)`,
the exact "MatchPath for topics" primitive), but `mqtt5`'s
`makeSubscribeMessageHandler` never extracts vars from the received
concrete topic at all — it only decodes the payload. **This must be built
first** (mirroring the existing `mqtt` v3 version) before
`ChannelHandle.DecodeMerged` can be wired into `mqtt5.Subscribe`
automatically.

### API surface

**`api/events`** (`api/events/builder.go` — additive, `TopicParam`
unchanged):

```go
func NewTopicParam[T any](name string, codec codex.Codec[string],
    get func(T) string, set func(*T, string)) MergedTopicParam[T]

type MergedTopicParam[T any] struct {
    TopicParam
    field codex.FieldCodec[T]
}
func (p MergedTopicParam[T]) WithDescription(desc string) MergedTopicParam[T]

func (h *ChannelHandle[T]) MergeFields() []codex.FieldCodec[T]

// DecodeMerged decodes payload (via handle's format) AND merges topic vars
// into the SAME T value — mirrors RouteHandle.DecodeMerged. A single map is
// always safe here (unlike REST's 4-destination encode direction) since
// topic is the ONLY var destination — no role split needed.
func (h *ChannelHandle[T]) DecodeMerged(payload []byte, topicVars map[string]string) (T, error)
```

**`adapters/mqtt5`** (`adapters/mqtt5/adapter.go` — new prerequisite +
wiring):

```go
// TopicVarsFromMessage is the mqtt5 equivalent of adapters/mqtt's existing
// TopicVarsFromMessage — the missing prerequisite found this session.
func TopicVarsFromMessage[T any](handle *events.ChannelHandle[T], msg *pahomqtt5.Publish) (map[string]string, error)

// PublishHandle is the single-call convenience — mirrors nethttp.CallHandle:
// derives vars from msg automatically via codex.EncodeVars(msg,
// handle.MergeFields()...), then delegates to the existing Publish.
// Publish remains the escape hatch for callers building vars themselves.
func PublishHandle[T any](ctx context.Context, client MQTTClient,
    handle *events.ChannelHandle[T], qos byte, retained bool, msg T,
    opts PublishOptions, formats ...format.Format[T]) error
```

`Subscribe`'s `makeSubscribeMessageHandler` calls `TopicVarsFromMessage` +
`handle.DecodeMerged` instead of bare `handle.Decode`/format `Unmarshal`
when `len(handle.MergeFields()) > 0` — identical behavior otherwise
(regression guard, same pattern as REST's P5/P6).

**`api/reqreply`** (`api/reqreply/route.go` — additive):

```go
func NewTopicParam[T any](name string, codec codex.Codec[string],
    get func(T) string, set func(*T, string)) MergedTopicParam[T]
// T = Req (request-side only — see Open design decisions).

func (h *RouteHandle[Req, Resp]) MergeFields() []codex.FieldCodec[Req]
func (h *RouteHandle[Req, Resp]) DecodeMerged(payload []byte, topicVars map[string]string) (Req, error)
```

Single-call client convenience: `adapters/mqtt5`/`adapters/zeromq`'s
existing `Call[Req, Resp](ctx, client/sock, handle, req, opts)` functions
ALREADY take vars via `opts.Vars` (not a separate positional parameter,
unlike REST's pre-`CallHandle` shape) — the convenience here is narrower
than REST's: auto-populate `opts.Vars` from `req` via
`codex.EncodeVars(req, handle.MergeFields()...)` when `opts.Vars` is nil,
with an explicit `opts.Vars` still winning when provided. Confirm the
exact wiring point (modify `Call` directly vs. add a distinct
`CallHandle`-equivalent per adapter) during implementation.

**`ports.Cache`** (`ports/cache.go` — additive, mirrors
`ports.NewFilePathParam` exactly):

```go
func NewCacheKeyParam[T any](name string, codec codex.Codec[string],
    get func(T) string, set func(*T, string)) MergedCacheKeyParam[T]
func (c Cache[T]) MergeFields() []codex.FieldCodec[T]
```

No `DecodeMerged`/single-call wrapper — `Cache.Get`/`Set` already take vars
directly; this is purely the "declare once" constructor.

### Structured errors / Observer integration

No new error types — reuses `codex.ValidationErrors`,
`codex.VarEncodeTypeError` verbatim (same as REST). No new observer
methods — `events`/`reqreply`/`mqtt5` reuse their EXISTING
`stats.Observer.RecordPublish`/`RecordSubscribe`/`RecordRequest` calls,
matching the "location strings are shared vocabulary" rule
(`"topic_var"`/`"payload"` already exist).

### Unit test plan

| ID | Test |
|---|---|
| EV1 | `events.NewTopicParam` registers spec `TopicParam` + merge field |
| EV2 | `ChannelHandle.DecodeMerged` happy path — payload + topic vars merged, NESTED struct (Round 4 mandate) |
| EV3 | `ChannelHandle.DecodeMerged` with zero merge fields — regression guard, identical to bare decode |
| EV4 | `mqtt5.TopicVarsFromMessage` happy path + mismatch (mirrors `mqtt.TopicVarsFromMessage`'s existing test) |
| EV5 | `mqtt5.Subscribe` auto-merge wiring — WITH/WITHOUT merge fields (mirrors REST's P5/P6) |
| EV6 | `mqtt5.PublishHandle` single-call, one struct in — no manual vars map |
| EV7 | Non-JSON payload (Gob via `format.NewTyped` projection) + nested struct, full publish→subscribe round trip (Round 4 mandate) |
| RR1–RR7 | Same shape as EV1–EV7, applied to `api/reqreply` + its adapter-specific `Call` wiring |
| C1 | `ports.NewCacheKeyParam` registers spec `CacheKeyParam` + merge field |
| C2 | `Cache.MergeFields()` + `codex.EncodeVars`/`DecodeVars` round trip |

### Files to create/change

| File | Responsibility |
|---|---|
| `api/events/builder.go` + `_test.go` | `NewTopicParam[T]`, `MergedTopicParam[T]`, `ChannelHandle.MergeFields()`/`DecodeMerged` |
| `api/reqreply/route.go` + `_test.go` | Same shape for `Req` |
| `ports/cache.go` + `_test.go` | `NewCacheKeyParam[T]`, `MergedCacheKeyParam[T]`, `Cache.MergeFields()` |
| `adapters/mqtt5/adapter.go` + `_test.go` | new `TopicVarsFromMessage`, `PublishHandle`, `Subscribe` auto-merge wiring |
| `adapters/mqtt5/reqreply.go` + `_test.go` | `Call` auto-populates `opts.Vars` from `req` when nil |
| `adapters/zeromq/adapter.go` + `_test.go` | same `Call` wiring as mqtt5's reqreply |
| `adapters/mqtt/topicvars.go` | optional: refactor `matchTopicTemplate` to delegate to `api/internal.MatchTemplate` (see Open design decisions) |
| new `examples/` | events pub/sub + reqreply demo with a nested payload struct and a non-JSON format (Gob), mirroring `examples/rest-nested-binary`'s rigor |
| `docs/features/events.md`, `docs/features/asyncapi.md` | new `NewTopicParam` sections |
| `docs/features/ports.md` | new Cache section, parallel to the existing File section |
| this doc | mark Phase 2 SHIPPED once complete, mirroring Round 3/4's write-up style |

### Open design decisions

1. **Does the reqreply REPLY direction need its own topic-merge role**
   (mirroring REST's response-header merge), or is topic-var merge a
   request-side-only concern (the reply is correlated by the underlying
   transport, not by re-encoding topic vars into `Resp`)? **Leaning:
   request-side only** — resolve definitively during implementation; if a
   concrete use case for reply-side topic merge appears, design it as an
   ADDITIVE follow-up, not a Phase 2 blocker.
2. **Where does the reqreply single-call convenience live** — modify
   `Call` directly to auto-populate `opts.Vars`, or add a distinct
   `CallHandle`-style function per adapter (mqtt5, zeromq)? Modifying
   `Call` in place is simpler (no new exported name) since `opts.Vars`
   already exists as the vars-carrying field, but changes `Call`'s
   behavior for existing callers with `opts.Vars == nil` who currently get
   `handle.Topic` unmodified — **must confirm this is safe** (a route with
   no merge fields declared has `MergeFields()` empty, so
   `EncodeVars(req, nil...)` returns an empty map and `opts.Vars` stays
   effectively nil-equivalent — likely safe, verify with a regression test
   mirroring REST's P4/P6 pattern).
3. **`adapters/mqtt/topicvars.go` → `api/internal.MatchTemplate` refactor**
   — still deferred from Round 1 (Open design decision, never resolved).
   Do opportunistically once `mqtt5.TopicVarsFromMessage` is built — a
   SECOND consumer proves the shared helper's shape is right.
4. **Does `adapters/zeromq`'s reqreply binding need identical wiring to
   mqtt5's**, or does binding the SAME `reqreply.RouteHandle` type mean
   fixing `RouteHandle.DecodeMerged`/`MergeFields()` once is sufficient,
   with only the per-adapter `Call`/`opts.Vars` convenience needing
   duplication? Likely the latter — confirm during implementation.

## Quick wins (independent of Phase 2 — cheap, still open, do first or in parallel)

These were deferred during Round 1 as low-priority polish, unrelated to
the events/reqreply/cache design above, and remain undone:

- `examples/sensor-service/main.go`, `examples/api-rest/main.go`: still
  use raw `r.PathValue(...)` — update to `rest.NewPathParam` + automatic
  merge, matching `examples/adapters-nethttp`'s existing pattern.
- `docs/features/config.md`: "Config file + env var overrides" section
  still hand-rolls `os.Getenv`+`strconv.Atoi`+manual assignment — replace
  with `codex.DecodeVars` (needs no new code, `DecodeVars` already works
  for env vars since they're string-keyed like everything else this
  feature covers).
- No dedicated `examples/` walkthrough for `ports.File.MatchPath` exists
  yet — the ORIGINAL motivating scenario (filename-encoded metadata) is
  only covered by `ports/file_test.go`'s `ExampleFile_MatchPath` + unit
  tests, not a runnable `examples/` directory demo.
