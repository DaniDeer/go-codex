# `ports.CachePattern` — Per-Var Key Codecs

> **Status:** Design complete — not yet implemented.
> [← Back to Roadmap](index.md)
>
> See also: [Ports feature](../features/ports.md) · [Redis Cache Adapter](../features/redis.md)

## Motivation

`ports.CachePattern.Key` is a `{var}` template (e.g. `"user:{id}"`), expanded
per item by `Cache[T].BuildKey(vars map[string]string)`. Today that expansion
is **plain string substitution** — the only check is that every placeholder
has an entry in `vars` (`CacheKeyError` on a missing var). There is no way to
validate the *value* of a key var (reject a non-UUID `id`, enforce a date
format, lowercase-only tenant slugs, …) declaratively.

Every other templated pattern in `ports` already supports this:

| Pattern | Per-var codec mechanism |
|---|---|
| `RESTPattern` | `rest.PathParam{Name}.WithCodec(c)` |
| `EventPattern` | `events.TopicParam{Name}.WithCodec(c)` |
| `FilePattern` | `format.FilePathParam{Name}.WithCodec(c)` |
| `SocketPattern` | `rest.PathParam` (via the upgrade route's `Opts`) |
| `CachePattern` | **none** — no `Opts` field at all |

This was a known, documented Phase-1 simplification when `CachePattern` and
`adapters/redis` shipped (Round 56) — recorded in `docs/roadmap/index.md`'s
Redis Phase 2 row as still-deferred: *"per-var key codecs, CachePattern spec
rendering."* This doc plans closing that gap, following the exact
`FilePathParam`/`format.File` precedent (`ports` is to `Cache[T]` what
`format` is to `File[T]`).

## Scope decisions

| In scope | Out of scope |
|---|---|
| `CachePattern.Opts []CacheOpt` field (mirrors `FilePattern.Opts`) | `CachePattern` spec/document rendering (still no `RegisterCache` — cache is metadata for adapters, not a spec-emitting surface; tracked separately in the Redis Phase 2 roadmap row) |
| `ports.CacheKeyParam{Name, Description, Codec}.WithCodec(c)` — sealed `CacheOpt` | Composite/multi-field key validation (e.g. cross-var constraints) — single-var `codex.Codec[string]` only, same ceiling as `PathParam`/`TopicParam`/`FilePathParam` |
| `Cache[T].BuildKey` validates declared vars via their codec before substitution | Changing `CacheKeyError`'s existing shape (`{Key, Var}`, no `Err`, no `Unwrap`) — stays exactly as-is for the missing-var case |
| New `Cache[T].ValidateKeyVars(vars) error` — pre-flight validation without building the key (mirrors `File.ValidatePathVars`) | Any behavior change for keys with **no** declared `CacheKeyParam` — `BuildKey` on an untouched `CachePattern.Opts` (nil) behaves byte-for-byte identically to today |
| New `Cache[T].KeySchemas() map[string]schema.Schema` — mirrors `File.PathParamSchemas()`, for future spec/doc tooling | Auto-generating any AsyncAPI/OpenAPI parameter object from `KeySchemas()` — no renderer consumes it yet; this is forward-compatible plumbing only |

## API surface

### `ports.CacheOpt` and `ports.CacheKeyParam`

```go
// CacheOpt is the sealed option interface for CachePattern.Opts — currently
// only CacheKeyParam implements it (mirrors format.FileOpt / FilePathParam).
type CacheOpt interface{ applyCache(*cacheBuilder) }

// CacheKeyParam describes a {varName} placeholder in a CachePattern.Key
// template. It mirrors format.FilePathParam — no Required field because
// every template variable must always be present.
//
// CacheKeyParam implements the CacheOpt interface: pass it directly in
// CachePattern.Opts.
type CacheKeyParam struct {
    // Name is the placeholder name (without braces) in the key template.
    // e.g. for template "user:{id}", Name is "id".
    Name string

    // Description enriches documentation for this key variable.
    Description string

    // Codec validates the variable value at Cache.BuildKey and
    // Cache.ValidateKeyVars time. When non-nil, the codec's schema is
    // available via Cache.KeySchemas. Nil means no runtime validation
    // (identical to today's behavior).
    Codec *codex.Codec[string]
}

// WithCodec sets the validation codec and returns the updated CacheKeyParam.
//
//	ports.CacheKeyParam{Name: "id"}.WithCodec(codex.String().Refine(validate.UUID))
func (p CacheKeyParam) WithCodec(c codex.Codec[string]) CacheKeyParam {
    p.Codec = &c
    return p
}
```

### `ports.CachePattern` — new `Opts` field

```go
type CachePattern struct {
    Key          string
    TTL          time.Duration
    Format       FileFormatKind
    CustomFormat any

    // Opts carries CacheKeyParam values declaring per-var codecs for Key's
    // {var} placeholders — mirrors format.NewFile's variadic FileOpt args.
    // A var with no matching CacheKeyParam (or a CacheKeyParam with a nil
    // Codec) is substituted without validation, exactly as today.
    Opts []CacheOpt
}
```

### `ports.Cache[T]` — new methods, `BuildKey` behavior change

```go
type Cache[T any] struct {
    Key    string
    TTL    time.Duration
    Format format.Format[T]

    params []CacheKeyParam // unexported — populated from CachePattern.Opts at build time
}

// BuildKey expands the key template's {var} placeholders from vars, validating
// each declared CacheKeyParam's value through its Codec (if set) before
// substitution.
//
// Errors:
//   - CacheKeyError{Key, Var} — a placeholder has no entry in vars (unchanged)
//   - CacheKeyParamError{Key, Var, Value, Err} — a declared codec rejects the value (new)
func (c Cache[T]) BuildKey(vars map[string]string) (string, error)

// ValidateKeyVars validates vars against declared CacheKeyParam codecs
// without building the concrete key. Mirrors format.File.ValidatePathVars.
func (c Cache[T]) ValidateKeyVars(vars map[string]string) error

// KeySchemas returns a map from key template variable name to the codec's
// schema.Schema, for each CacheKeyParam with a non-nil Codec. Mirrors
// format.File.PathParamSchemas. Forward-compatible plumbing for future spec
// tooling — no renderer consumes it yet.
func (c Cache[T]) KeySchemas() map[string]schema.Schema
```

### `ports/handle.go` — wiring

The `CachePattern` case in both `buildDualCodecPatternHandles` (SinkPort/IOPort
role) and the `LatestPort` build path builds a `cacheBuilder` from `pat.Opts`
(same shape as `fileBuilder` in `format`), then constructs
`Cache[T]{Key: pat.Key, TTL: pat.TTL, Format: cFmt, params: cb.params}`. No new
rejection rules — `Opts` is orthogonal to the existing SourcePort/ToolPort
rejection logic.

## Structured errors

### `CacheKeyParamError` (new)

```go
// CacheKeyParamError is returned by [Cache.BuildKey] and [Cache.ValidateKeyVars]
// when a declared CacheKeyParam's Codec rejects the substituted value.
//
// Use errors.As to extract the offending variable:
//
//	var paramErr ports.CacheKeyParamError
//	if errors.As(err, &paramErr) {
//	    slog.Warn("cache key variable rejected",
//	        "key", paramErr.Key, "var", paramErr.Var, "value", paramErr.Value, "cause", paramErr.Err)
//	}
type CacheKeyParamError struct {
    Key   string // the declared key template (e.g. "user:{id}")
    Var   string // placeholder name (without braces)
    Value string // the value that failed validation
    Err   error  // underlying constraint or codec error
}

func (e CacheKeyParamError) Error() string {
    return fmt.Sprintf("cache key %q variable %q: invalid value %q: %s", e.Key, e.Var, e.Value, e.Err)
}

func (e CacheKeyParamError) Unwrap() error { return e.Err }

func (e CacheKeyParamError) LogValue() slog.Value {
    return slog.GroupValue(
        slog.String("key", e.Key),
        slog.String("var", e.Var),
        slog.String("value", e.Value),
        slog.Any("cause", e.Err),
    )
}
```

`CacheKeyError` (existing, missing-var case) is untouched — no `Err` field, no
`Unwrap`, exactly as documented today.

## Observer integration

None — `BuildKey`/`ValidateKeyVars` are pure functions with no `ctx` parameter,
same as today (mirrors `File.BuildPath`/`ValidatePathVars`, which are also
observer-free — path/key validation is a precondition check, not an I/O
operation the `FileObserver`/`CacheObserver` lifecycle covers). `redis.GetAdapter`/
`SetAdapter` already call `stats.ReportErrors(obs, "payload", ...)` on other
failures; a `CacheKeyParamError` from `BuildKey` flows into their existing
`CacheError{Key, Op, Err}` wrapping unchanged (`BuildKey` failures already
produce a `CacheError` in both adapters today — the inner `Err` is just a more
specific typed error now instead of always being `CacheKeyError`).

## Unit test plan

| ID | Name | Verifies |
|---|---|---|
| T1 | `TestCacheKeyParam_WithCodec` | Returns updated value; original unmodified (value semantics) |
| T2 | `TestCache_BuildKey_NoParams_Unchanged` | Nil/empty `Opts` — byte-identical behavior to pre-feature `BuildKey` (regression guard) |
| T3 | `TestCache_BuildKey_ValidatesDeclaredCodec_HappyPath` | Valid value passes codec, substitutes correctly |
| T4 | `TestCache_BuildKey_CodecRejectsValue` | Invalid value → `CacheKeyParamError`, `errors.As`, `LogValue` keys `key`/`var`/`value`/`cause` |
| T5 | `TestCache_BuildKey_MissingVar_StillCacheKeyError` | A declared `CacheKeyParam` for a var absent from `vars` still returns `CacheKeyError` (not `CacheKeyParamError` — can't codec-validate an absent value) |
| T6 | `TestCache_ValidateKeyVars_HappyAndError` | Mirrors `BuildKey` validation without building the key string |
| T7 | `TestCache_KeySchemas_OmitsParamsWithoutCodec` | Only codec-bearing params appear; empty map when none declared |
| T8 | `TestCachePattern_Opts_WiredThroughIOPort` | `ports.NewIOPort` with `CachePattern{Opts: [...]}` → `ports.CacheHandle[T]` has working per-var validation end-to-end |
| T9 | `TestCachePattern_Opts_WiredThroughSinkPort` | Same, `SinkPort` role |
| T10 | `TestCachePattern_Opts_WiredThroughLatestPort` | Same, `LatestPort` role (var-free key — confirms `Opts` on a var-free key is a no-op, not an error) |
| T11 | `Example` for `CacheKeyParam`/`CachePattern.Opts` | pkg.go.dev example showing UUID-validated cache key |

## Files to change

| File | Responsibility |
|---|---|
| `ports/pattern.go` | `CacheOpt` interface, `CacheKeyParam` struct + `WithCodec`, `CachePattern.Opts` field, `Cache[T].params` field + `BuildKey`/`ValidateKeyVars`/`KeySchemas` methods, `cacheBuilder` unexported type |
| `ports/pattern_errors.go` | New `CacheKeyParamError{Key, Var, Value, Err}` |
| `ports/handle.go` | Wire `pat.Opts` into `cacheBuilder` at both `CachePattern` build sites (SinkPort/IOPort role, LatestPort role) |
| `ports/pattern_test.go` / `ports/handle_test.go` | T1–T11 |
| `.github/instructions/go-codex.instructions.md` | Sync `CachePattern`/`Cache[T]` description |
| `docs/features/ports.md` | `CachePattern` section — document `Opts`/`CacheKeyParam` alongside the existing key-template paragraph |
| `docs/guides/ports.md` | Same, in the step-by-step cache walkthrough |
| `docs/features/redis.md` | Note that key vars can now be codec-validated before the Redis round-trip (fails fast, no wasted network call) |
| `docs/roadmap/index.md` | Remove "per-var key codecs" from the Redis Phase 2 deferred list |
| `examples/redis-cache/main.go` | Add a `CacheKeyParam` demonstrating a rejected malformed key var (extend existing example rather than a new one — same scenario, one more assertion) |

## Open design decisions

1. **Should `CacheKeyParamError`'s `Value` be redacted for sensitive keys** (e.g. a tenant secret embedded in a key var)? Existing `FilePathParamError`/`rest.PathParamError` all include the raw value — following that precedent for consistency unless the user flags a specific security concern.
2. **Does `LatestPort`'s var-free key case need any special handling?** A var-free `Key` (e.g. `"session:current"`) with `Opts` declared is inert (no `{var}` to match) — `BuildKey`/`ValidateKeyVars` simply never look up an unused `CacheKeyParam.Name`. Confirmed as a no-op, not an error, consistent with `FilePathParam` behavior for a static (var-free) `File` path.
