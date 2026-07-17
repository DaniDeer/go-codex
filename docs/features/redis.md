# Redis Cache Adapter — `adapters/redis`

> See also: [`adapters/redis` on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/adapters/redis) · [Ports — Protocol-Agnostic Wiring](ports.md) · [Metrics Observer](observer.md) · [Error Handling](error-handling.md)
>
> Runnable demo: [`examples/redis-cache`](https://github.com/DaniDeer/go-codex/tree/main/examples/redis-cache) — CachePattern declaration, write-through `SetAdapter`, read-through `GetAdapter`, `Seed` warm restart, and a `stats.CacheObserver` — all against an in-memory fake, no Redis server needed.

`adapters/redis` makes the cache a **typed, codec-validated, declared**
boundary. Values enter and leave through the port's `codex.Codec[T]` — every
read decodes AND validates, every write validates AND encodes. Keys, TTL, and
wire format are declared once on the port; every hit, miss, and write fires an
observer event.

Works with any server speaking the Redis protocol (Redis, Valkey, KeyDB,
DragonflyDB, …).

---

## Declare the cache on the port — `ports.CachePattern`

Cache keys are shaped like topics and paths — `"user:{id}"` is as declarable
as `"sensors/{sensorID}/data"`:

```go
var UserCache = codex.Must(ports.NewIOPort[UserQuery, User]("user-cache",
    queryCodec, userCodec, ports.PortOptions{
        Patterns: []ports.Pattern{
            ports.CachePattern{Key: "user:{id}", TTL: 15 * time.Minute},
        },
    }))

cache, _ := ports.CacheHandle[User](UserCache) // ports.Cache[User]
```

| `CachePattern` field | Meaning |
|---|---|
| `Key` | Key template with `{var}` placeholders, expanded per item via `Cache.BuildKey` (missing var → `ports.CacheKeyError`) |
| `TTL` | Default time-to-live on writes; zero = no expiry; overridable per adapter via `SetAdapterOptions.TTL` |
| `Format` | Value wire format applied to the port's codec: JSON (default), YAML, TOML — same enum as `FilePattern` |
| `CustomFormat` | Escape hatch for binary/custom formats (Gob, protobuf, …) — a pre-built `format.Format[T]`, overrides `Format` when non-nil. See [`ports.FilePattern.CustomFormat`](ports.md#filepattern--typed-files-as-sink-or-intermediate-io) |
| `Opts` | `[]ports.CacheOpt` — currently only `ports.CacheKeyParam{Name, Description, Codec}.WithCodec(c)`, validating a key var's **value** (not just its presence) before every key is built. Fails fast — a codec-validated key var is rejected before the Redis round-trip, not after |

### Per-key-variable codecs — `ports.CacheKeyParam`

By default, key vars are plain strings — `Cache.BuildKey` only checks that
every `{var}` has an entry in `vars` (`CacheKeyError` on a missing var).
Declare a `CacheKeyParam` to also validate the *value*, the same way
`rest.PathParam`/`events.TopicParam`/`format.FilePathParam` validate their
templated variables:

```go
var UserCache = codex.Must(ports.NewIOPort[UserQuery, User]("user-cache",
    queryCodec, userCodec, ports.PortOptions{
        Patterns: []ports.Pattern{
            ports.CachePattern{
                Key: "user:{id}", TTL: 15 * time.Minute,
                Opts: []ports.CacheOpt{
                    ports.CacheKeyParam{Name: "id"}.WithCodec(codex.String().Refine(validate.UUID)),
                },
            },
        },
    }))
```

A non-UUID `id` now returns `ports.CacheKeyParamError{Key, Var, Value, Err}`
from `Cache.BuildKey` (and from `redis.GetAdapter`/`SetAdapter`, wrapped in
their existing `CacheError`) — before any network call. A var with no
matching `CacheKeyParam` (or one with a nil `Codec`) substitutes unvalidated,
exactly as before this feature. `Cache.ValidateKeyVars(vars)` runs the same
validation without building the key string; `Cache.KeySchemas()` returns each
codec-bearing var's `schema.Schema` (forward-compatible plumbing for future
spec tooling).

### Standalone use — zero port/pipeline involved

`ports.Cache[T]` is a plain declarative descriptor, the same way
`format.File[T]` is — every `adapters/redis` constructor
(`GetAdapter`/`SetAdapter`/`DrainSetAdapter`/`Seed`) takes a `ports.Cache[T]`
value directly, never a port. Building the cache with `ports.NewCache`
instead of a `CachePattern` gets you the exact same codec-validated
behavior — value encode/decode AND key-variable validation — with **no
`ports.NewIOPort`/`SinkPort`/`Pattern`/`Bind` anywhere in the call path**:

```go
userCache := ports.NewCache("user:{id}", format.JSON(userCodec),
    ports.CacheKeyParam{Name: "id"}.WithCodec(codex.String().Refine(validate.UUID)))
userCache.TTL = 15 * time.Minute
```

#### Single-value read — `Seed`

`Seed` is the purest form: a plain function call, no `stream.Stream`
involved at all. Use it whenever you just need "the current value for a
var-free key" (config, session, last-known-state):

```go
v, ok, err := redis.Seed(ctx, client, userCache, redis.SeedOptions{})
// v is fully decoded AND codec-validated; (zero, false, nil) on a miss
```

#### Single-value get/set — `GetAdapter`/`SetAdapter` via `stream.Single`

`GetAdapter` and `SetAdapter` are shaped as `ports.IOAdapter[Req,Resp]` (a
`Transform(ctx, stream.Stream[Req]) stream.Stream[Resp]` method) because
that is the one interface every port-bound and standalone caller shares —
but calling `.Transform` directly needs nothing but a `stream.Stream[T]`
value, and `stream.Single` builds one from a single value with zero
pipeline ceremony (no channel, no goroutine wiring, no port):

```go
// Read-through get for one query — same codec validation as the pipeline path.
out := redis.GetAdapter[UserQuery, User](client, userCache,
    func(q UserQuery) map[string]string { return map[string]string{"id": q.ID} },
    redis.GetAdapterOptions{},
).Transform(ctx, stream.Single(ctx, UserQuery{ID: "f47ac10b-58cc-4372-a567-0e02b2c3d479"}))

users, errs := stream.Collect(ctx, out) // 0 or 1 value: hit or miss

// Write-through set for one value — same codec validation, same key-var
// rejection (CacheKeyParamError) before any network call.
out = redis.SetAdapter[User](client, userCache,
    func(u User) map[string]string { return map[string]string{"id": u.ID} },
    redis.SetAdapterOptions{},
).Transform(ctx, stream.Single(ctx, User{ID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Name: "Ada"}))

written, errs := stream.Collect(ctx, out) // the item, passed through unchanged
```

`stream.Single`/`stream.Collect` are from the `stream` package (not `ports`)
— they exist independently of any port declaration; this is the same
"declare a handle by hand, drive it with the lowest-ceremony helper"
pattern `format.File` uses for standalone reads/writes.

#### Port-type acceptance (when you DO use a port)

Port-type acceptance for `CachePattern`-declared caches (wrong combinations
fail at construction with `PatternRegisterError`):

| Port type | Meaning |
|---|---|
| `IOPort[Req,Resp]` | Read-through / write-through step — cached value is the RESPONSE type |
| `SinkPort[T]` | Terminal write-through |
| `LatestPort[T]` | Durable current state (var-free key) |
| `SourcePort`, `ToolPort` | **Rejected** — a cache does not produce a stream and is not a tool surface |

---

## The narrow client interface — `Commands`

Constructors accept `Commands` — a three-method subset — never a concrete
client:

```go
type Commands interface {
    Get(ctx context.Context, key string) ([]byte, error) // ErrCacheMiss on missing key
    Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
    Del(ctx context.Context, keys ...string) error
}
```

Wrap a go-redis client once (`NewCommands` is the only place go-redis is
touched — standalone, cluster, and sentinel clients all satisfy
`redis.UniversalClient`):

```go
client := redis.NewCommands(goredis.NewUniversalClient(
    &goredis.UniversalOptions{Addrs: []string{"localhost:6379"}}))
```

Unit tests and the example use a hand-written in-memory fake — no test
dependencies, no broker in CI.

---

## Adapters

| Constructor | Port | What it does |
|---|---|---|
| `GetAdapter[Req,Resp](client, cache, keyFn, opts) ports.IOAdapter[Req,Resp]` | `IOPort` | Read-through: key from `keyFn(Req)`, hit → decoded+validated Resp downstream. Miss → item skipped (default) or `CacheError` wrapping `ErrCacheMiss` when `MissIsError` |
| `SetAdapter[T](client, cache, keyFn, opts) ports.IOAdapter[T,T]` | `IOPort` | Write-through transform: writes each item, passes it through UNCHANGED — a cache write failure goes to `Stream.Errors` but never drops pipeline data |
| `DrainSetAdapter[T](client, cache, keyFn, opts) ports.SinkAdapter[T]` | `SinkPort` | Terminal write-through; errors → `SetAdapterOptions.OnError` |
| `Seed[T](ctx, client, cache, opts) (T, bool, error)` | — | Warm-restart read of a var-free key; `(zero, false, nil)` on miss — an empty cache is not an error |

### Durable LatestPort — the Feed-tee pattern

`ports.LatestAdapter.Serve` is read-only, so persistence composes from
existing pieces instead of a new adapter: route the feeding stream through
`SetAdapter` (persist every update), and `Seed` the first item after a
restart:

```go
latest, _ := ports.NewLatestPort[OEE]("oee-latest", oeeCodec, ports.PortOptions{
    Patterns: []ports.Pattern{ports.CachePattern{Key: "latest-oee"}},
})
cache, _ := ports.CacheHandle[OEE](latest)

// Persist every update on the way to Feed:
persisted := redis.SetAdapter[OEE](client, cache,
    func(OEE) map[string]string { return nil }, // var-free key
    redis.SetAdapterOptions{}).Transform(ctx, oeeStream)

// Warm restart: seed the stream with the last known value.
if v, ok, _ := redis.Seed(ctx, client, cache, redis.SeedOptions{}); ok {
    persisted = stream.Merge(ctx, stream.Single(ctx, v), persisted)
}
latest.Feed(ctx, persisted)
```

---

## Errors

```go
var ErrCacheMiss = errors.New("redis: cache miss") // sentinel, survives wrapping

type CacheError struct {
    Key string // expanded key, e.g. "user:42"
    Op  string // "get", "set", "del"
    Err error
}
```

`CacheError` implements `Error()`, `Unwrap()`, and `slog.LogValuer` — 
`errors.Is(err, redis.ErrCacheMiss)` and `errors.As` into
`codex.ValidationErrors` both work through the chain. Key-template failures
surface as `ports.CacheKeyError{Key, Var}`.

---

## Observer — `stats.CacheObserver`

A new optional extension (type-asserted, like `SQLObserver` — existing
Observer implementations need not change):

```go
type CacheObserver interface {
    RecordCacheHit(key string, duration time.Duration)
    RecordCacheMiss(key string, duration time.Duration)
    RecordCacheWrite(key, op string, success bool, duration time.Duration)
}
```

- Implemented by `NoopObserver`, `LoggingObserver`, and `NewFanout`.
- Nil Observer resolves from ctx (`stats.ObserverFromContext`).
- Decode validation failures additionally report per-field via
  `stats.ReportErrors` with location `"payload"`.

---

## Scope

Phase 1 is cache ops only. Redis pub/sub (SourcePort/SinkPort bridges à la
MQTT) is designed in the
[Phase 2 roadmap](../roadmap/redis-pubsub.md) — fire-and-forget without
persistence (closer to ZeroMQ than MQTT), reusing `EventPattern` and the
existing transport observer hooks. Redis Streams, Lua scripting, and
client-side caching remain out of scope.
