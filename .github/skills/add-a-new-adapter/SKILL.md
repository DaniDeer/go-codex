---
name: add-a-new-adapter
description: 'Workflow for adding a new transport/store adapter to go-codex (adapters/<name>): boundary classification to port types, dependency decisions, package layout, ports Pattern integration, stream bridges, and full doc/test sync. Use when asked to "add a new adapter", "plan a new adapter", "wrap <client library> for go-codex", "new transport adapter", "redis/amqp/tcp/kafka/nats adapter", or when creating any new package under adapters/.'
---

# Adding a New Adapter to go-codex

Every adapter is a **Layer-2 boundary package** under `adapters/<name>` that
wires an external transport or store into the codec (Layer 1) and ports/stream
(Layer 3) machinery. This skill encodes the conventions extracted from the
existing adapters (`mqtt5`, `mqtt`, `nethttp`, `chi`, `zeromq`, `sql`, `file`,
`mcpgo`) so every new adapter lands with the same shape.

**Planning first:** a new adapter ALWAYS starts as a roadmap doc via the
`plan-a-new-codex-feature` skill (Explore mode). This skill supplies the
adapter-specific content for that roadmap doc and the implementation
conventions once approved. Existing roadmap examples:
`docs/roadmap/amqp-adapter.md`, `docs/roadmap/tcp-adapter.md`.

---

## Step 1 — Classify the boundary → port types

Decide which of the six port types the adapter serves. This drives the whole
API surface — each supported port type means one `ports.XxxAdapter` interface
implementation exposed as a constructor function.

| Port type | Adapter interface (in `ports/`) | Direction | Implement when the boundary… |
|---|---|---|---|
| `SourcePort[T]` | `SourceAdapter[T]` — `Activate(ctx, dst chan<- T, errs chan<- error)` | external → pipeline | pushes/produces items (subscribe, watch, poll, scan, HTTP ingest) |
| `SinkPort[T]` | `SinkAdapter[T]` — `Activate(ctx, src stream.Stream[T])` | pipeline → external | consumes items (publish, insert, write, SSE out) |
| `IOPort[Req,Resp]` | `IOAdapter[Req,Resp]` — `Transform(ctx, src Stream[Req]) Stream[Resp]` | pipeline ↔ external | per-item request/response or 1→N lookup (HTTP call, SQL query, cache get) |
| `LatestPort[T]` | `LatestAdapter[T]` — `Serve(ctx, latest func() (T, bool)) error` | pipeline → query | serves the current cached value to request/response clients |
| `ToolPort[In,Out]` | `ToolAdapter[In,Out]` — `Bind(ctx, fn func(ctx, In) stream.Stream[Out]) error` | external request → pipeline → response | exposes the same pipeline function over request/response boundaries (REST, req/reply, MCP) |
| `DuplexPort[In,Out]` | `DuplexAdapter[In,Out]` — `Activate(ctx, dst chan<- Framed[In], errs chan<- error, src Stream[Framed[Out]]) error` | external ↔ pipeline (sessions) | persistent bidirectional session boundary (WebSocket, framed TCP) |

Rules:
- **Adapter interface contracts are strict**: `SourceAdapter.Activate` must
  NOT close `dst`/`errs` (the port owns channel lifecycle); `SinkAdapter`
  must DRAIN `src` (ignoring items blocks the pipeline); `LatestAdapter.Serve`
  may return after registration OR block — `Bind` supervises either way.
- Each constructor returns the interface type
  (e.g. `func GetAdapter[...](...) ports.IOAdapter[Req, Resp]`) backed by an
  unexported struct (`redisGetAdapter[T]`), mirroring `mqtt5.SubscribeAdapter`.
- `AdapterName() string` returns `"<pkg>.<Constructor>"` (e.g.
  `"redis.GetAdapter"`) — used in `PortBindError` and observability.
- When the constructor's docs mention Bind, show the one-line usage:
  `domain.Port.Bind(ctx, pkg.XxxAdapter(...))`.
- After implementing, **add the constructor names to the adapter lists in the
  `ports/*.go` interface godocs** ("Implemented by transport binding
  constructors: …").

## Step 2 — Dependency decision

- Wrapping an external client lib is normal (paho for MQTT, chi for routing).
  Evaluate: actively maintained? preserves type safety (no `interface{}`
  boxing at the API surface)? Record accepted AND rejected candidates with
  rationale in the roadmap doc's "Toolchain decisions" section.
- **Narrow-interface rule**: when the client library's concrete type is not
  interface-shaped (go-redis, database/sql wrappers…), define a small
  unexported-friendly interface in the adapter covering ONLY the commands the
  adapter uses, accept that interface in constructors, and unit-test against
  a hand-written fake. No test-only dependencies (no miniredis, no brokers in
  CI — same rule as MQTT).
- stdlib-only is preferred when the protocol is simple enough
  (see `docs/roadmap/tcp-adapter.md` for a worked example of that decision).
- `go.mod` changes happen at implementation time, never during planning.

## Step 3 — Package layout

```
adapters/<name>/
├── doc.go            # package overview: paradigm, port mapping, observer story
├── errors.go         # structured error types (Error/Unwrap/LogValue)
├── binding.go        # ports adapter constructors (+ options structs)
├── <op>.go           # operation-specific files (client.go, reqreply.go, stream.go…)
└── *_test.go         # one test file per source file
```

- Mirror `adapters/mqtt5` for transports, `adapters/sql` for stores.
- Options structs per constructor (`XxxAdapterOptions`), always with an
  `Observer stats.Observer` field documented as "Resolved from ctx when nil".
- Stream bridges (`SubscribeStream`, `DrainPublish` analogues) go in
  `stream.go` — only when the transport has a natural continuous mode.

## Step 4 — Ports Pattern integration

Decision criteria for how the adapter participates in `ports.Pattern`:

| Choice | When | Precedent |
|---|---|---|
| Reuse existing Pattern | boundary is HTTP-shaped, topic-shaped, or file-shaped | `RESTPattern`, `EventPattern`, `FilePattern` |
| Metadata-only Pattern | per-call text/closures are driver-specific; nothing templatable | `SQLPattern` (Table/Op via context propagation) |
| New Pattern type | boundary has a declarable addressing template (key/queue/frame shape) + options that belong on the port | `CachePattern` (redis), AMQP exchange/queue topology |

A new Pattern type requires, in `ports/`:
- struct + `isPortPattern()` in `pattern.go`
- build logic in `handle.go` (`buildPatterns` switch) — handle-building or
  metadata-stored, per the table above
- plugin/access path in `ports/*_port.go` (`PluginXxxPattern` + `patternSpec`
  backing) + `MissingPatternError` path for `Register*` replay helpers
- pattern-kind validation per port type (which port types accept it; reject
  others at `PluginXxxPattern` time with `PatternRegisterError`)
- spec rendering consideration (`ports/spec.go`) — does the pattern appear in
  the generated spec document?

## Step 5 — Mandatory requirements (do not duplicate here)

Follow the **six mandatory requirements** in the
`plan-a-new-codex-feature` skill (structured errors with `slog.LogValuer`,
observer integration incl. the `ObserverFromContext` nil-guard rules, unit
test matrix, three-surface documentation, runnable example). Adapter-specific
additions:

- **New observer extension** only when the adapter has a genuinely new
  lifecycle event (e.g. cache hit/miss). Follow the `SQLObserver` pattern:
  interface in `stats/observer.go`, implemented by `NoopObserver`,
  `LoggingObserver`, fanout; compile-time assertion in `stats/observer_test.go`;
  ALWAYS type-assertion guarded.
- **Location strings** are shared vocabulary — reuse `"payload"`, `"body"`,
  `"topic_var"`, `"sql_row"`, `"file"` before inventing new ones.
- Docs surfaces: `docs/features/<name>.md` + `docs/guides/` entry when there
  is a workflow, `zensical.toml` nav, `docs/reference/project-structure.md`
  (new directory!), instructions row in
  `.github/instructions/go-codex.instructions.md` (adapter table AND the
  ports "Implemented by" lists), review-skill history + known-facts.

## Step 5b — Merge-field convenience (MANDATORY when the boundary is Req/Resp- or payload-shaped)

**The user promise, stated first**: for any boundary with a request/response
shape or a duplex role pair (publisher/subscriber, requestor/replier,
client/server), a caller on EITHER side must be able to do the ENTIRE
encode-or-decode direction with **one struct value in (or out), one call**
— no manual map-building, no manual header/cookie/query/topic stitching, in
the common case. Everything below exists to make that promise safe, not to
replace it.

REST (`api/rest` + `adapters/nethttp`/`chi`) is the reference
implementation — mirror it exactly when the new adapter's boundary has this
shape:

```go
// Client: ONE struct in, ONE struct out.
resp, err := nethttp.CallWithHandle(ctx, client, baseURL, handle, req, nethttp.CallOptions{})

// Server: ONE struct in, ONE struct out.
route := route.WithHandler(func(ctx context.Context, req Req) (Resp, error) {
    return resp, nil // adapter auto-decoded req, will auto-encode resp
})
route.Register(b)
nethttp.Serve(mux, b)
```

**Checklist — implement all five, both directions, both roles:**

1. **Declare-once constructors** — `NewXxxParam[T]` that register BOTH the
   boundary's spec Param (unchanged, still drives OpenAPI/AsyncAPI/MCP spec
   generation) AND a `codex.FieldCodec[T]` merge field in one call. Mirror
   `rest.NewPathParam[T]`/`NewRequiredQueryParam[T]`/`NewOptionalQueryParam[T]`
   (+ Header/Cookie) for the request/inbound side, and
   `rest.NewRequiredResponseHeaderParam[Resp]`/`NewOptionalResponseHeaderParam[Resp]`
   (+ Cookie) for the response/outbound side when the boundary has one.
2. **Escape hatch preserved** — the plain, validate-only Param struct
   literals must remain fully supported, unchanged, for the rare param a
   handler never reads/writes directly. A boundary must be able to freely
   mix both styles on the same route/channel/tool.
3. **Encode/decode symmetry** — decode (the side receiving several
   pre-scoped string-keyed sources) is a safe flat union merge; encode (the
   side decomposing one struct into several destinations) REQUIRES
   role-aware accessors (e.g. `PathMergeFields()`/`QueryMergeFields()`/
   `HeaderMergeFields()`/`CookieMergeFields()`, never one flat list) to
   avoid leaking a value into the wrong destination. Both directions must
   exist — do not ship decode-only.
4. **Role symmetry** — implement for BOTH roles of the boundary: server AND
   client for request/response transports; publisher AND subscriber for
   pub/sub; requestor AND replier for req/reply. The one-struct promise
   must hold on both sides of the wire, not just one.
5. **Single-call convenience wrapper** — an encode-side wrapper
   (`CallHandle`-equivalent) that derives every destination map from one
   struct automatically, with explicit per-call overrides still winning on
   key collision; the decode-side equivalent is the adapter's `Handler`/
   `Register`-style entry point auto-merging before invoking the caller's
   function. This wrapper IS the one-struct-one-call promise made concrete
   — don't stop at the accessors/constructors, ship the wrapper too.

**`api/rest`/`api/events`/`api/reqreply` all have the core API shipped, AND
so does the `ports.Pattern` binding layer** —
`DrainCallAdapter`/`PublishAdapter`/`CallAdapter` across
`nethttp`/`mqtt5`/`zeromq`/`mqtt` all delegate to their transport's
`CallHandle`/`PublishHandle` and derive vars PER-ITEM whenever their `Vars`
option is left `nil`. Use any of these as precedent for a NEW
boundary's declare-once constructors/`DecodeMerged`/single-call
wrapper/binding-layer delegation — a new adapter's `SinkAdapter`/`IOAdapter`
constructors should follow the SAME `Vars == nil` → delegate-to-Handle
pattern from day one, not the old static-`Vars`-only shape that predated
this fix.

**Verify with a non-default format AND a nested struct, not just the
flat/JSON happy path.** Body decode/encode is orthogonal to var-merge, so
ANY `format.Format[T]` (Gob, Binary, custom `format.NewTyped`) must compose
with merge fields, not just JSON/YAML/TOML — and merge-field `get`/`set`
are plain closures, so nested sub-struct access
(`func(r Req) string { return r.Meta.X }`) must work, not just top-level
fields. When the wire bytes for a whole-value binary format (Gob,
protobuf) should represent ONLY a nested sub-field, use `format.NewTyped`
with a custom marshal/unmarshal projecting onto/from that field — do NOT
assume `format.Gob(reqCodec)` alone achieves this (it serialises the WHOLE
typed value via reflection, bypassing the codec's Encode/Decode). See
`api/rest/builder_test.go`'s `TestGobBodyFormat_ComposesWithNestedMergeFields`
and `examples/rest-nested-binary` for the reference pattern to mirror.

## Step 6 — Use the checklist

Work through [references/checklist.md](references/checklist.md) — a
copy-paste per-adapter checklist covering files, tests, docs, nav, and the
verification ritual. Track progress with todos, one per checklist block.

---

## Gotchas

- **Never let a `SourceAdapter` close its `dst`/`errs` channels** — the port
  owns channel lifecycle; closing causes a panic on the port's own close.
- **`IngestAdapter`-style forwarding goroutines must be waited for in
  `Activate`** (done channel) — otherwise a send can race the port's channel
  close after ctx cancellation (this was a real `-race` finding; see
  review-skill history R54).
- **HTTP-mux-style registration is NOT always concurrency-safe** — chi's Mux
  cannot register routes while serving; register a swap-handler at
  CONSTRUCTOR time and install the real handler atomically in `Activate`/
  `Serve` (R54 pattern). Check your transport's registration semantics.
- **Go generics methods cannot introduce new type params** — adapter
  constructors needing a second type parameter must be free functions.
- **Errors channel ownership**: stream bridges route errors to
  `Stream.Errors`, never a separate callback (`subOpts.OnError` is overridden
  internally — documented behaviour, keep it consistent).
- **New pub/sub sink adapters must wire the error-path ergonomics convention**:
  if your adapter binds an `events.ChannelHandle[T]` (pub/sub sink), consult
  `handle.ErrorResponseFor(err)` for every upstream stream error BEFORE
  falling back to your own `OnError` option — mirror
  `adapters/mqtt5.PublishAdapter`'s `Activate` exactly (also implemented in
  `adapters/mqtt`/`adapters/zeromq`). A matched `events.ErrorRespond` pattern
  publishes the typed payload to its declared error topic using your
  adapter's own low-level send call; `ErrorHandle`/`ErrorLog` or unmatched
  falls through to `OnError` unchanged. No new options struct fields needed
  — the declaration lives entirely on the `ChannelHandle`. If your adapter
  is instead a request/reply Serve loop (binds `reqreply.RouteHandle`),
  consult `handle.ErrorResponseFor(err)` on handler/encode failure ONLY
  (never decode failure) before sending a plain-text error reply — mirror
  `adapters/mqtt5.Serve`/`adapters/zeromq.Serve`. See `review-go-codex`
  skill's checklist.md §13 for the full per-boundary rule set.
- **No `//nolint`/`//gosec` suppressions** — `just check` must stay at 0
  issues without new suppressions.
- **Unit tests must not require a live broker/server** — hand-written fakes
  against the narrow interface; loopback servers (`httptest`, in-proc ZeroMQ)
  are fine when the ecosystem provides them.

## References

- [references/checklist.md](references/checklist.md) — per-adapter checklist
- `plan-a-new-codex-feature` skill — roadmap template + five mandatory requirements + the "boundary symmetry" 6th requirement
- `review-go-codex` skill — consistency checklist + history (do not re-introduce fixed issues); checklist category 12 covers merge-field/boundary symmetry
- `adapters/mqtt5/` — reference transport adapter (binding.go conventions)
- `adapters/sql/` — reference store adapter (metadata Pattern, validate/observer patterns)
- `api/rest/builder.go` + `adapters/nethttp/{adapter,client}.go` — reference implementation of Step 5b's one-struct-one-call pattern (`NewPathParam`/etc., `DecodeMerged`, role-aware `PathMergeFields`/etc., `NewRequiredResponseHeaderParam`/etc., `DecodeMergedResponse`, `CallHandle`)
- `docs/concepts/api-contracts.md` — "one struct, one call" design principle, user-facing framing
- `ports/pattern.go`, `ports/handle.go` — Pattern declaration + build machinery
