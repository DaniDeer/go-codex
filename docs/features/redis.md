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

### Standalone use — building without the pipeline features

If your application doesn't use `ports`/`stream` pipelines at all, `Get` and
`Set` are the cache's equivalent of `format.File.Read`/`.Write` or
`adapters/sql.Validate` — plain functions, full codec validation (key vars
AND value), **no `ports.IOAdapter`, no `stream.Stream`, no port anywhere in
the call path**:

```go
userCache := ports.NewCache("user:{id}", format.JSON(userCodec),
    ports.CacheKeyParam{Name: "id"}.WithCodec(codex.String().Refine(validate.UUID)))
userCache.TTL = 15 * time.Minute

v, ok, err := redis.Get(ctx, client, userCache, map[string]string{"id": userID}, redis.GetOptions{})
// v is fully decoded AND codec-validated; (zero, false, nil) on a miss

err = redis.Set(ctx, client, userCache, map[string]string{"id": user.ID}, user, redis.SetOptions{})
// key-var codec rejection (CacheKeyParamError) happens before any network call
```

`ports.Cache[T]` (via `NewCache`) is the declarative descriptor — the same
role `RouteHandle`/`ChannelHandle` play via `route.ClientHandle()`/
`channel.ClientHandle()`. `Get`/`Set` are the concrete redis implementation
against it, the same role `nethttp.Call`/`mqtt.Publish` play against a
route/channel handle. This mirrors every other non-pipeline building block
in go-codex — see [Design pattern: declarative descriptor + plain
function](ports.md#design-pattern-declarative-descriptor--plain-function)
for the full comparison table across `file`/`cache`/`rest`/`events`/`sql`.

`Seed` is a thin wrapper around `Get` with nil vars — the one case that
must run before any stream exists (a `LatestPort`'s warm restart, before the
feeding stream is even constructed), so it stays a dedicated zero-vars
function rather than asking every caller to pass an empty map. It only
applies to a **var-free** key (e.g. `"latest-user"`, not `"user:{id}"`):

```go
latestCache := ports.NewCache("latest-user", format.JSON(userCodec))
v, ok, err := redis.Seed(ctx, client, latestCache, redis.SeedOptions{})
```

#### Pipeline use — same functions, delegated to

`GetAdapter`/`SetAdapter`/`DrainSetAdapter` (the `ports.IOAdapter`/
`SinkAdapter` implementations bound via `port.Bind`) delegate to `Get`/`Set`
internally per item — calling `Get`/`Set` directly and driving a
fully-bound port produce **identical behavior** (same codec validation,
same errors, same observer events). Use the adapters when you have an
actual pipeline (`ports.NewIOPort`/`SinkPort` + `Bind`); use `Get`/`Set`
directly everywhere else.

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
| `Get[T](ctx, client, cache, vars, opts) (T, bool, error)` | — | Plain-function standalone read; `GetAdapter` delegates to it per item |
| `Set[T](ctx, client, cache, vars, v, opts) error` | — | Plain-function standalone write; `SetAdapter`/`DrainSetAdapter` delegate to it per item |
| `GetAdapter[Req,Resp](client, cache, keyFn, opts) ports.IOAdapter[Req,Resp]` | `IOPort` | Read-through: key from `keyFn(Req)`, hit → decoded+validated Resp downstream. Miss → item skipped (default) or `CacheError` wrapping `ErrCacheMiss` when `MissIsError` |
| `SetAdapter[T](client, cache, keyFn, opts) ports.IOAdapter[T,T]` | `IOPort` | Write-through transform: writes each item, passes it through UNCHANGED — a cache write failure goes to `Stream.Errors` but never drops pipeline data |
| `DrainSetAdapter[T](client, cache, keyFn, opts) ports.SinkAdapter[T]` | `SinkPort` | Terminal write-through; errors → `SetAdapterOptions.OnError` |
| `Seed[T](ctx, client, cache, opts) (T, bool, error)` | — | Warm-restart read of a var-free key; a thin wrapper around `Get` with nil vars; `(zero, false, nil)` on miss — an empty cache is not an error |

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
