# `adapters/redis` — Redis Cache Adapter

> **Status:** Design complete — not yet implemented.
> [← Back to Roadmap](index.md)

## Motivation

Pipelines routinely need a cache boundary: read-through lookups
("do we already have the enrichment for this ID?"), write-through persistence
of computed values, and durable backing for a `LatestPort` so the "current
state" survives a process restart. Today that means hand-written Redis code
outside the codec machinery — untyped `[]byte` in and out, no validation, no
observer events, no declaration in the port's spec. This adapter makes the
cache a **typed, codec-validated, declared** boundary like every other
go-codex adapter: values are encoded/decoded through the port's
`codex.Codec[T]`, keys are declared as templates on the port, and every hit,
miss, and write fires an observer event.

## Scope decisions (Phase 1)

| In scope | Out of scope (deferred) |
|---|---|
| Typed cache ops: Get / Set (with TTL) / Del | Redis pub/sub → **Phase 2** (SourcePort/SinkPort bridges, like MQTT) |
| `IOPort` adapters: `GetAdapter` (read-through), `SetAdapter` (write-through transform) | Redis Streams (XADD/XREAD) |
| `SinkPort` adapter: `DrainSetAdapter` (terminal write-through) | Cluster topology management (the client handles it; no adapter API) |
| `LatestPort` adapter: `LatestAdapter` (persist latest value → warm restarts) | Lua scripting / transactions / pipelining API |
| New `ports.CachePattern` (key template + TTL + format kind) | Server-assisted client-side caching (see toolchain — rueidis rejected) |
| Narrow `Commands` interface + hand-written fake for tests | Distributed locking, rate limiting (separate features, not cache ops) |

## Toolchain / dependency decisions

**Chosen: `github.com/redis/go-redis/v9`** — the official client. Stable,
universally deployed, no CGO, supports standalone/sentinel/cluster through
one `UniversalClient` construction.

Rejected alternatives (recorded for posterity):

| Candidate | Why rejected |
|---|---|
| `rueidis` | Excellent performance (auto-pipelining, RESP3, server-assisted client caching that would pair well with `LatestPort`) — but a larger, less familiar API surface for marginal benefit at go-codex's abstraction level. Client-side caching can be revisited in Phase 2 if a use case appears. |
| In-repo minimal RESP client (stdlib-only) | Viable for the cache subset (tcp-adapter precedent), but reconnect/cluster/sentinel handling is real operational surface that go-redis already solves. Not worth owning. |
| `alicebob/miniredis` (test dep) | Not needed — the narrow-interface rule (below) makes unit tests fake-based with zero extra dependencies. |

**Narrow-interface rule** (per the `add-a-new-adapter` skill): go-redis's
`*redis.Client` is a concrete type, not an interface. The adapter defines its
own minimal command interface and accepts THAT in constructors — go-redis is
referenced only where the caller constructs the client:

```go
// Commands is the narrow Redis command surface the adapter uses.
// *redis.Client, *redis.ClusterClient, and redis.UniversalClient all satisfy
// it via a thin shim (go-redis returns *redis.StatusCmd etc., so the shim
// adapts to plain Go values). Unit tests provide a hand-written fake.
type Commands interface {
    Get(ctx context.Context, key string) ([]byte, error)  // ErrCacheMiss on missing key
    Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
    Del(ctx context.Context, keys ...string) error
}

// NewCommands wraps a go-redis UniversalClient as Commands.
func NewCommands(c redis.UniversalClient) Commands
```

The shim (`NewCommands`) is the ONLY file importing go-redis; everything else
(adapters, tests) depends on `Commands`. `go.mod` gains the dependency at
implementation time.

## Ports integration — new `CachePattern`

Cache keys are shaped like topics and paths — `"user:{id}"` is as declarable
as `"sensors/{sensorID}/data"` — so the cache gets a first-class Pattern
(decision: NOT SQLPattern-style metadata-only).

```go
// CachePattern declares a cache-shaped pattern for a port bound to a cache
// adapter (adapters/redis). Key is a template with {var} placeholders,
// validated/expanded with the same machinery as topic templates.
//
//	ports.CachePattern{Key: "user:{id}", TTL: 15 * time.Minute}
type CachePattern struct {
    // Key is the key template, e.g. "user:{id}". Placeholders are expanded
    // per item from declared key vars (codec-validated, like topic vars).
    Key string
    // TTL is the default time-to-live applied on writes. Zero = no expiry.
    TTL time.Duration
    // Format selects the value wire format applied to the port's codec.
    // Reuses FileFormatKind (JSON default / YAML / TOML) — same enum, same
    // reasoning: a generic format.Format[T] cannot live in the non-generic
    // Pattern struct.
    Format FileFormatKind
    // Vars declares key-template variables with optional per-var codecs
    // (exact type TBD at implementation — mirror EventPattern's topic vars).
    Vars []CacheVar
}

func (CachePattern) isPortPattern() {}
```

Port-type acceptance (validated at `New*Port` time, wrong combinations
rejected with a `PatternError`):

| Port type | CachePattern meaning |
|---|---|
| `IOPort[Req,Resp]` | Read-through/write-through step — key vars extracted from Req |
| `SinkPort[T]` | Write-through terminal — key vars extracted from T |
| `LatestPort[T]` | Persistence key for the latest value (no vars — a single key) |
| `SourcePort[T]`, `ToolPort` | **Rejected** — a cache does not produce a stream (Phase 2 pub/sub is `EventPattern` territory) |

Handle story: `CachePattern` builds a `CacheHandle[T]` (key expander + bound
`format.Format[T]` + TTL), retrievable via `ports.CacheHandle[T](port)`;
missing pattern → `MissingPatternError` (same accessor convention as
`FileHandle`). Spec rendering: the pattern appears in the port spec document
as `cache: {key: "user:{id}", ttl: "15m", format: json}`.

## API surface (`adapters/redis`)

```go
// ── IOPort: read-through lookup ───────────────────────────────────────────

// GetAdapterOptions configures [GetAdapter].
type GetAdapterOptions struct {
    // OnMiss selects miss behaviour: emit nothing (default — 0 Resp for the
    // item, IOAdapter's 0..N contract) or route ErrCacheMiss to Stream.Errors.
    MissIsError bool
    // Observer receives cache lifecycle events. Resolved from ctx when nil.
    Observer stats.Observer
}

// GetAdapter returns a [ports.IOAdapter] that looks each Req up in the cache.
// The key is built from the port's CachePattern template + key vars from Req.
// Hit → decoded, codec-validated Resp. Miss → skip or error per options.
func GetAdapter[Req, Resp any](
    client Commands,
    keyFn func(Req) map[string]string, // key-var extraction from Req
    opts GetAdapterOptions,
) ports.IOAdapter[Req, Resp]

// ── IOPort: write-through transform ──────────────────────────────────────

// SetAdapter returns a [ports.IOAdapter] that writes each item to the cache
// and passes it through unchanged (Req == Resp == T). Encode failures and
// write failures go to Stream.Errors; the item is still passed through.
func SetAdapter[T any](
    client Commands,
    keyFn func(T) map[string]string,
    opts SetAdapterOptions,
) ports.IOAdapter[T, T]

// ── SinkPort: terminal write-through ──────────────────────────────────────

// DrainSetAdapter returns a [ports.SinkAdapter] that writes every item to the
// cache. The terminal variant of SetAdapter for pipeline ends.
func DrainSetAdapter[T any](
    client Commands,
    keyFn func(T) map[string]string,
    opts SetAdapterOptions,
) ports.SinkAdapter[T]

// ── LatestPort: durable latest value ──────────────────────────────────────

// LatestAdapter returns a [ports.LatestAdapter] that persists each new latest
// value under the pattern's (var-free) key, and — on Serve start — seeds the
// in-memory latest value from Redis if present (warm restart).
//
// NOTE: requires a LatestPort write hook or a Feed-tee; exact seam is an open
// design decision (see below).
func LatestAdapter[T any](client Commands, opts LatestAdapterOptions) ports.LatestAdapter[T]
```

All constructors accept `Commands`, never `*redis.Client`. Key building, TTL,
and value format come from the port's `CachePattern` via the built
`CacheHandle[T]` — adapters read it at `Bind` time (mirrors how sql adapters
read `SQLMeta` from context).

## Structured errors (all implement `slog.LogValuer`)

```go
// ErrCacheMiss is the sentinel for a missing key. Callers use
// errors.Is(err, ErrCacheMiss). Chosen over a (T, bool, error) return shape:
// the ports adapter interfaces fix the signatures, so the miss must travel
// as an error value when MissIsError is set — and a sentinel composes with
// CacheError via Unwrap.
var ErrCacheMiss = errors.New("redis: cache miss")

// CacheError wraps any cache operation failure.
type CacheError struct {
    Key string // the expanded key, e.g. "user:42"
    Op  string // "get", "set", "del"
    Err error
}

func (e CacheError) Error() string  // "redis: get user:42: <err>"
func (e CacheError) Unwrap() error  // e.Err — errors.Is reaches ErrCacheMiss
func (e CacheError) LogValue() slog.Value
// slog.GroupValue(slog.String("key", …), slog.String("op", …), slog.Any("err", …))
```

Decode failures reuse the existing codec error chain (`ValidationErrors`)
wrapped in `CacheError{Op: "get"}` — no new decode error type.

## Observer integration

**New optional extension `stats.CacheObserver`** — hit/miss/write is a
genuinely new lifecycle event (per the skill's rule), not expressible as
`RecordRequest`:

```go
// CacheObserver is an optional extension to Observer for cache adapters.
// Always type-assert before calling.
type CacheObserver interface {
    // RecordCacheHit / RecordCacheMiss fire per lookup.
    RecordCacheHit(key string, duration time.Duration)
    RecordCacheMiss(key string, duration time.Duration)
    // RecordCacheWrite fires per Set/Del, success or failure.
    RecordCacheWrite(key string, op string, success bool, duration time.Duration)
}
```

- Implemented by `NoopObserver`, `LoggingObserver`, fanout; compile-time
  assertion in `stats/observer_test.go`.
- Nil-guard: `if obs == nil { obs = stats.ObserverFromContext(ctx) }` inside
  `Activate`/`Transform`/`Serve` (all have ctx).
- Validation failures on decoded values → `stats.ReportErrors(obs, "payload", err)`
  (reuses the existing `"payload"` location — cached values are payloads).
- Observer fires on EVERY path: hit, miss, write success, write failure,
  decode failure.

## Unit test plan (fake `Commands`, no live Redis)

| ID | Test | Verifies |
|---|---|---|
| C1 | Get hit → typed value | happy path: fake returns encoded bytes, adapter decodes + validates |
| C2 | Get miss, default | 0 Resp emitted, no error, RecordCacheMiss fired |
| C3 | Get miss, MissIsError | `CacheError{Op:"get"}` on Errors; `errors.Is(err, ErrCacheMiss)` true |
| C4 | Get decode failure | ValidationErrors wrapped in CacheError; ReportErrors per field |
| C5 | Set writes + passes through | fake captures key/value/TTL; item continues downstream |
| C6 | Set write failure | error to Stream.Errors, item still passed through |
| C7 | TTL from CachePattern | fake receives the pattern's TTL |
| C8 | Key template expansion | `"user:{id}"` + vars → `"user:42"`; missing var → typed error |
| C9 | DrainSetAdapter drains fully | all items written; src fully consumed |
| C10 | LatestAdapter warm start | fake pre-seeded → latest() true before first Feed item |
| C11 | CacheError.LogValue | `slog.KindGroup` + keys `key`, `op`, `err` |
| C12 | errors.As chain | outer CacheError → inner ErrCacheMiss / ValidationErrors |
| C13 | Observer hit/miss/write | counting fake observer, all paths |
| C14 | nil Observer | no panic; plain Observer (no CacheObserver) → graceful skip |
| C15 | CachePattern port acceptance | SourcePort+CachePattern rejected with PatternError |
| — | `Example...()` | ExampleGetAdapter (deterministic, fake-backed) |

## Files to create

| File | Responsibility |
|---|---|
| `adapters/redis/doc.go` | Package overview: typed cache boundary, port mapping, observer story |
| `adapters/redis/errors.go` | `ErrCacheMiss`, `CacheError` |
| `adapters/redis/commands.go` | `Commands` interface + `NewCommands` go-redis shim (ONLY file importing go-redis) |
| `adapters/redis/binding.go` | `GetAdapter`, `SetAdapter`, `DrainSetAdapter`, `LatestAdapter` + options |
| `adapters/redis/*_test.go` | fake `Commands` + full test plan above |
| `ports/pattern.go` | `CachePattern` + `CacheVar` |
| `ports/handle.go` | build + `CacheHandle[T]` accessor + acceptance validation |
| `stats/observer.go` | `CacheObserver` extension (+ Noop/Logging/fanout impls) |
| `docs/features/redis.md` | Feature page |
| `examples/redis-cache/main.go` | Fake-backed runnable example (no live Redis in CI) |

## Out of scope (Phase 2)

- **Redis pub/sub** — `SubscribeAdapter` (SourcePort) / `PublishAdapter`
  (SinkPort) via `EventPattern`, mirroring MQTT. Deferred: cache ops are the
  validated use case; pub/sub needs its own channel-semantics review
  (Redis pub/sub is fire-and-forget, no persistence — closer to ZeroMQ than
  MQTT).
- Redis Streams, cluster APIs, Lua, client-side caching (rueidis territory).

## Open design decisions (resolve before/during implementation)

1. **`LatestAdapter` write seam** — `ports.LatestAdapter.Serve(ctx, latest)`
   is read-only (it serves the value, it doesn't see updates). Persisting on
   each update needs either (a) a `LatestPort` write hook (new ports API), or
   (b) a `Feed`-tee where the caller routes the stream through a
   `SetAdapter` before `Feed`. Leaning (b) — zero ports changes; the warm
   restart seed still fits `Serve`.
2. **`keyFn` vs struct tags** — key-var extraction as an explicit
   `func(T) map[string]string` (leaning: explicit, mirrors topic-var
   handling) vs reflective struct tags (rejected so far: go-codex avoids
   reflection at boundaries).
3. **`CacheVar` codec validation depth** — full per-var codecs like topic
   vars, or plain string vars for Phase 1 (leaning: plain strings first,
   codecs when a use case demands).
4. **Del exposure** — `Commands.Del` exists for completeness; is a
   `DelAdapter` needed, or is Del an implementation detail of Set-with-nil?
   Leaning: no DelAdapter in Phase 1.
