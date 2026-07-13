# Default Observer — `stats`

> **Status:** Phase 0 (pre-flight fixes) ready to implement. Phase 1 (context observer) design complete.
> [← Back to Roadmap](index.md)

---

## Motivation

Every adapter function, stream bridge, and forge registry currently requires the caller
to pass `Observer: obs` in its `Options` struct. A typical service setup looks like:

```go
obs := stats.NewLoggingObserver(slog.Default())

nethttp.Handler(handle, fn, nethttp.Options{Observer: obs})
nethttp.RegisterLatest(mux, oeeHandle, oeeStream, nethttp.Options{Observer: obs})
mqtt.Subscribe(ctx, client, handle, 1, fn, mqtt.SubscribeOptions{Observer: obs})
mqtt.DrainPublish(ctx, client, handle, src, fmt, mqtt.MQTTDrainPublishOptions{Observer: obs})
stream.Apply(ctx, s, fn, stream.ApplyOptions{Observer: obs})
stream.FromCodec(ctx, ch, fmt, stream.SourceOptions{Observer: obs})
file.Read(vars, format.FileOptions{Observer: obs})
registry.WithObserver(obs)
```

This is **verbose and error-prone**: if any call-site omits `Observer: obs`, that
component silently falls back to `NoopObserver{}` and produces no telemetry. In a
large service, it is easy to wire up 90% of components correctly and miss the
remaining 10% — with no compile-time check.

The feature request: **set an observer once per context (or globally) and have every
adapter automatically use it** when `Observer` is nil in the options struct.

---

## Phase 0 — Pre-flight observer consistency fixes

A review of the existing observer wiring across all adapters revealed three
consistency gaps that must be fixed **before** the context-observer feature is
added. Fixing them first ensures the context-observer feature propagates correctly
everywhere, rather than silently not reaching broken call sites.

---

### F1 — `adapters/file.DrainWriteOptions` missing `Observer` field

**Affected:** `adapters/file.DrainWrite`

Every other sink bridge options struct carries `Observer stats.Observer`:
- `mqtt.MQTTDrainPublishOptions` ✅
- `mqtt5.MQTT5DrainPublishOptions` ✅
- `zeromq.DrainPublishOptions` ✅
- `sql.DrainInsertOptions` ✅
- `nethttp.DrainCallOptions` ✅
- `adapters/file.DrainWriteOptions` ❌ **missing**

Without an `Observer` field, encode errors and write failures inside `DrainWrite`
cannot emit `RecordValidationError` events. The user has no way to plug in
observability for file write streams.

**Fix:**
```go
// adapters/file/stream.go — add Observer to DrainWriteOptions
type DrainWriteOptions struct {
    OnError   func(error)
    Path      string
    Separator string
    // Observer receives per-item encode/write lifecycle events.
    // [stats.Observer.RecordValidationError] fires for encode failures.
    // Defaults to [stats.NoopObserver] when nil.
    Observer  stats.Observer
}
```

Use `opts.Observer` (after nil-guard) for the `gstream.Drain` call and to call
`stats.ReportErrors` on encode errors.

---

### F2 — Sink bridges pass empty `DrainOptions{}` — observer not threaded to `stream.Drain`

**Affected:** `mqtt.DrainPublish`, `mqtt5.DrainPublish`, `zeromq.DrainPublish`,
`nethttp.DrainCall`, `adapters/file.DrainWrite`

Every sink bridge delegates to `gstream.Drain(ctx, src, fn, errFn, gstream.DrainOptions{})`.
`stream.Drain` has a `DrainOptions.Observer` field that calls
`stats.ReportErrors(obs, "stream", err)` when `onValue` returns an error.

Only `sql.DrainInsert` correctly passes the observer:
```go
gstream.DrainOptions{Observer: opts.Observer}  // ✅ sql.DrainInsert
gstream.DrainOptions{}                          // ❌ all other sink bridges
```

When `onValue` (e.g., `Publish`) returns an error inside these bridges, `Drain`
calls `stats.ReportErrors` with a noop observer — per-field validation errors from
encode failures silently bypass the observer.

**Fix:** every sink bridge that calls `gstream.Drain` must pass its resolved observer:

```go
// mqtt.DrainPublish — before
gstream.Drain(ctx, src, fn, errFn, gstream.DrainOptions{})

// after
gstream.Drain(ctx, src, fn, errFn, gstream.DrainOptions{Observer: opts.Observer})
```

Same pattern for `mqtt5.DrainPublish`, `zeromq.DrainPublish`, `nethttp.DrainCall`,
and `adapters/file.DrainWrite` (after F1 adds the `Observer` field).

---

### F3 — `stats.Observer` interface godoc mentions only HTTP / MQTT

**Affected:** `stats/observer.go`

`stats.Observer` documents `RecordRequest` as "called after every HTTP request"
and `RecordSubscribe` as "called after every MQTT message". But ZeroMQ and MCP
adapters legitimately reuse these methods with non-HTTP semantics:

| Adapter | `RecordRequest` method string |
|---------|------------------------------|
| `adapters/nethttp` | `"GET"`, `"POST"`, … |
| `adapters/chi` | same |
| `adapters/zeromq` | `"ZMQ-REP"`, `"ZMQ-REQ"`, `"ZMQ-ROUTER"`, `"ZMQ-DEALER"` |
| `adapters/mcpgo` | `"tool"`, `"resource"`, `"prompt"` |
| `adapters/nethttp` SSE bridges | `"GET"` (for SSEClientStream) |
| `adapters/mqtt`/`mqtt5` | `"MQTT5-REQ"` (for Call) |

`RecordSubscribe` is used both for MQTT subscribe events and for SSE stream
events (per-item sends in `SSEFromStream`).

Users implementing `stats.Observer` for ZeroMQ or MCP services see "HTTP" in the
godoc and are confused about whether the method applies. The interface is correct;
only the documentation is misleading.

**Fix:** update `stats.Observer` godoc to describe method semantics
transport-agnostically, listing all known method strings per adapter:

```go
// RecordRequest is called after every request/response cycle completes.
// method describes the transport and operation — values vary by adapter:
//   - HTTP adapters: uppercase HTTP method ("GET", "POST", …)
//   - ZeroMQ adapters: "ZMQ-REP", "ZMQ-REQ", "ZMQ-ROUTER", "ZMQ-DEALER"
//   - MCP adapter: "tool", "resource", "prompt"
//   - MQTT5 reqreply: "MQTT5-REQ", "MQTT5-REP"
// path is the route pattern or topic template, not the concrete value.
// statusCode follows HTTP conventions: 200 success, 400 client error,
// 500 server error; 0 means no request reached the transport (pre-flight failure).
```

```go
// RecordSubscribe is called after every inbound message or event is processed.
// topic is the concrete value (not a template). success is false when
// decode or the application handler failed. Used by MQTT adapters (per-message)
// and SSE stream bridges (per-emitted event).
```

---

### Phase 0 implementation order

| Step | Change | File(s) |
|------|--------|---------|
| P0-1 | Add `Observer` field to `DrainWriteOptions` + nil-guard + thread to `Drain` | `adapters/file/stream.go` |
| P0-2 | Thread observer to `gstream.Drain` in `mqtt.DrainPublish` | `adapters/mqtt/stream.go` |
| P0-3 | Thread observer to `gstream.Drain` in `mqtt5.DrainPublish` | `adapters/mqtt5/stream.go` |
| P0-4 | Thread observer to `gstream.Drain` in `zeromq.DrainPublish` | `adapters/zeromq/stream.go` |
| P0-5 | Thread observer to `gstream.Drain` in `nethttp.DrainCall` | `adapters/nethttp/stream.go` |
| P0-6 | Update `stats.Observer` godoc for `RecordRequest` and `RecordSubscribe` | `stats/observer.go` |

Each step: apply change → `go fmt` → `go build ./...` → `go test ./...` → `just check`.

---

## Layer 1 codecs — why they are out of scope

`codex.Codec[T]` and `format.Format[T]` (Marshal, Unmarshal, Validate) are **pure
value-transformation functions** — they carry no observer. This is a correct and
intentional design:

- Codecs return typed errors (`codex.ValidationErrors`, `codex.TypeMismatchError`)
  without emitting telemetry. They have no `ctx` or `Observer` parameter.
- Adapters — which already have `ctx` and `obs` — call `stats.ReportErrors(obs, location, err)`
  on the errors returned by codecs. This is where observer telemetry is emitted.
- `format.FromEnv` and `format.FromEnvVar` similarly return `EnvVarError` wrapping
  `codex.ValidationErrors`; the caller calls `stats.ReportErrors` if they want telemetry.

**Consequence for this feature:** Layer 1 codec calls do not benefit from the
context observer and require no changes. The context observer propagates through
adapters (Layer 2), stream bridges, and forge (Layer 3), which all receive `ctx`.

---

## Scope decisions

| In scope (Phase 1) | Out of scope / deferred |
|---|---|
| `stats.WithObserver(ctx, obs) context.Context` — attach observer to context | Modifying all adapter Options structs to carry `ctx` internally |
| `stats.ObserverFromContext(ctx) stats.Observer` — retrieve from context | Per-adapter "default" setters (`SetDefaultObserver` etc.) |
| All adapters / stream bridges / `format.File` that accept `ctx` read from it when `Observer` field is nil | Changing the `Observer` field type |
| Zero-allocation fast path: no context lookup when `Observer` is explicitly set | Package-level global (mutable global state — against go-codex design) |
| Backward-compatible: existing code with explicit `Observer: obs` is unaffected | `net/http` middleware injecting observer (trivial user code, not needed in library) |
| **`format.File`** — uses `opts.Context` already; extend nil-guard to use `ObserverFromContext(opts.Context)` | **`codex.Codec[T]`** — no observer, by design; returns typed errors; caller calls `stats.ReportErrors` |
| | **`format.Format.Marshal/Unmarshal`** — no observer, by design; pure value transformation |
| | **`format.FromEnv`/`FromEnvVar`** — no observer parameter; caller calls `stats.ReportErrors` on returned error |
| | **`forge.Registry.WithObserver`** — already has explicit builder API; context would be awkward for long-lived setup |

---

## Design

### Mechanism: context key in `stats`

```go
// stats/context.go (new file)

type observerKey struct{}

// WithObserver returns a new context carrying obs as the default observer.
// Any adapter, stream bridge, or forge registry that receives this context
// and has a nil Observer in its options will use obs automatically.
func WithObserver(ctx context.Context, obs Observer) context.Context

// ObserverFromContext retrieves the observer stored by [WithObserver].
// Returns [NoopObserver]{} if no observer has been set.
func ObserverFromContext(ctx context.Context) Observer
```

### Adapter nil-guard update

Every adapter currently has this guard at the top of each function:

```go
obs := opts.Observer
if obs == nil {
    obs = stats.NoopObserver{}
}
```

It changes to:

```go
obs := opts.Observer
if obs == nil {
    obs = stats.ObserverFromContext(ctx)
}
```

`ObserverFromContext` returns `NoopObserver{}` when no context observer is set, so
the behaviour is **identical** when neither `opts.Observer` is set nor a context
observer exists — fully backward compatible.

### Precedence rules (explicit → context → noop)

```
opts.Observer != nil   →  use opts.Observer           (explicit — highest priority)
opts.Observer == nil   →  use ObserverFromContext(ctx) (may be NoopObserver{})
```

No third level — a package-level global would be mutable shared state and break
concurrent test isolation.

### Functions without `ctx` in options

A small number of call sites receive an observer through `opts` without a `ctx`
parameter (e.g. `format.FileOptions` where `Context` is optional). These are handled
by using `opts.Context` when present, or leaving the nil-guard unchanged (no context
to pull from). The roadmap for these is:

- `format.FileOptions` — already has an optional `Context context.Context` field;
  use `ObserverFromContext(opts.Context)` as the fallback.
- `forge.Registry.WithObserver` — `Registry` is set up before serving; it already
  has an explicit API. No change needed.

### Usage — "set once, forget everywhere"

```go
// At application startup — set once:
obs := stats.NewFanout(metricsObserver, stats.NewLoggingObserver(slog.Default()))
ctx := stats.WithObserver(context.Background(), obs)

// All adapters pick it up automatically from ctx when Observer is nil:
nethttp.Handler(handle, fn, nethttp.Options{})         // uses ctx observer
mqtt.Subscribe(ctx, client, handle, 1, fn, mqtt.SubscribeOptions{})  // uses ctx observer
stream.Apply(ctx, s, fn, stream.ApplyOptions{})        // uses ctx observer
file.Read(vars, format.FileOptions{Context: ctx})      // uses ctx observer
```

Per-component override still works — explicit beats context:

```go
nethttp.Handler(handle, fn, nethttp.Options{Observer: auditObserver}) // explicit, no lookup
```

### HTTP middleware integration (out of scope, but designed to work)

A net/http middleware can inject the observer per-request:

```go
func ObserverMiddleware(obs stats.Observer) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            ctx := stats.WithObserver(r.Context(), obs)
            next.ServeHTTP(w, r.WithContext(ctx))
        })
    }
}
```

This is out of scope for this package (users write it themselves) but the design
enables it. Per-request observer injection is a natural extension.

---

## API surface

### `stats/context.go` (new file)

```go
package stats

import "context"

type observerKey struct{}

// WithObserver returns a copy of ctx carrying obs as the default observer.
// Adapters, stream bridges, and forge registries that receive this context
// will use obs automatically when no explicit Observer is set in their options.
//
// Use WithObserver at application startup to avoid repeating Observer: obs on
// every call site:
//
//	obs := stats.NewFanout(metricsObserver, stats.NewLoggingObserver(slog.Default()))
//	ctx := stats.WithObserver(context.Background(), obs)
//	// All adapters now use obs unless they explicitly set a different Observer.
//
// The context-provided observer has lower priority than an explicitly set
// opts.Observer — explicit always wins.
func WithObserver(ctx context.Context, obs Observer) context.Context {
    return context.WithValue(ctx, observerKey{}, obs)
}

// ObserverFromContext retrieves the observer attached by [WithObserver].
// Returns [NoopObserver]{} when no observer has been stored in ctx.
func ObserverFromContext(ctx context.Context) Observer {
    if obs, ok := ctx.Value(observerKey{}).(Observer); ok {
        return obs
    }
    return NoopObserver{}
}
```

### Adapter nil-guard change (all adapters, ~36 call sites)

**Before:**
```go
obs := opts.Observer
if obs == nil {
    obs = stats.NoopObserver{}
}
```

**After:**
```go
obs := opts.Observer
if obs == nil {
    obs = stats.ObserverFromContext(ctx)
}
```

This is a **mechanical, non-breaking change** — `ObserverFromContext` returns
`NoopObserver{}` when no context observer exists, preserving existing behaviour.

### `format.FileOptions` change

```go
// Before (in format/file.go):
obs := opts.Observer
if obs == nil {
    obs = stats.NoopObserver{}
}

// After:
obs := opts.Observer
if obs == nil && opts.Context != nil {
    obs = stats.ObserverFromContext(opts.Context)
}
if obs == nil {
    obs = stats.NoopObserver{}
}
```

`format.FileOptions.Context` is already an optional field; this adds the lookup
without changing the public API.

---

## Structured errors

No new error types. This feature adds no new failure modes — `ObserverFromContext`
is infallible (returns `NoopObserver{}` on miss).

---

## Observer integration

No new observer interfaces. The feature makes existing interfaces easier to wire —
it does not add new methods or hooks.

---

## Unit test plan

| ID | Test name | What it verifies |
|----|-----------|-----------------|
| T1 | `TestWithObserver_StoresAndRetrieves` | `WithObserver` + `ObserverFromContext` round-trip |
| T2 | `TestObserverFromContext_NilContext` | `context.Background()` → returns `NoopObserver{}` |
| T3 | `TestObserverFromContext_WrongType` | Non-Observer value stored → returns `NoopObserver{}` |
| T4 | `TestWithObserver_Overrides` | Inner ctx observer overrides outer when nested |
| T5 | `TestAdapter_UsesContextObserver_WhenOptsNil` | `nethttp.Handler` with empty Options picks up context observer |
| T6 | `TestAdapter_ExplicitObserverBeatsContext` | Explicit `opts.Observer` takes precedence over context observer |
| T7 | `TestAdapter_NoContextObserver_NoObserverSet_IsNoop` | Nil opts + no context observer → no panic, noop behaviour |
| T8 | `TestFileOptions_UsesContextObserver` | `format.File.Read` with `FileOptions{Context: ctx}` picks up context observer |
| T9 | `TestStreamApply_UsesContextObserver` | `stream.Apply` with nil Observer opts picks up context observer |
| T10 | `ExampleWithObserver` | pkg.go.dev example showing startup wiring pattern |

---

## Files to create / modify

| File | Change |
|---|---|
| `stats/context.go` | **New** — `WithObserver`, `ObserverFromContext`, `observerKey` |
| `stats/context_test.go` | **New** — T1–T4, T10 |
| `adapters/nethttp/adapter.go` | Modify 2 nil-guards |
| `adapters/nethttp/client.go` | Modify 1 nil-guard |
| `adapters/nethttp/stream.go` | Modify 3 nil-guards (SSEFromStream, PollStream, SSEClientStream) |
| `adapters/chi/adapter.go` | Modify 2 nil-guards |
| `adapters/chi/stream.go` | Modify 1 nil-guard (SSEFromStream) |
| `adapters/mqtt/adapter.go` | Modify 2 nil-guards |
| `adapters/mqtt5/adapter.go` | Modify 2 nil-guards |
| `adapters/mqtt5/reqreply.go` | Modify 1 nil-guard |
| `adapters/mqtt5/stream.go` | Modify 1 nil-guard |
| `adapters/zeromq/adapter.go` | Modify 6 nil-guards |
| `adapters/zeromq/stream.go` | Modify 3 nil-guards |
| `adapters/mcpgo/adapter.go` | Modify 3 nil-guards |
| `adapters/sql/validate.go` | Modify 1 nil-guard |
| `adapters/sql/stream.go` | Modify 2 nil-guards |
| `stream/transform.go` | Modify 1 nil-guard (`Apply`) |
| `stream/source.go` | Modify 1 nil-guard (`FromCodec`) |
| `stream/sink.go` | Modify 1 nil-guard (`Drain`) |
| `format/file.go` | Modify 3 nil-guards (Read, Write, Update) using `opts.Context` |
| `.github/instructions/go-codex.instructions.md` | Document new `stats.WithObserver` / `ObserverFromContext` API |
| `docs/features/observability.md` (or relevant page) | Add "default observer" section |

---

## Out of scope (Phase 2)

- **Package-level global default** — `stats.SetDefaultObserver(obs)` — mutable global
  state breaks concurrent test isolation and conflicts with go-codex's "no global state"
  design principle. Explicitly not planned.
- **HTTP middleware helper** — `stats.ObserverMiddleware(obs) http.Handler` — a one-liner
  wrapper using `WithObserver`. Easy to write inline; no need to ship in the library.
- **`forge.Registry` context integration** — `Registry` is long-lived and configured at
  startup with `WithObserver`; the explicit builder API is intentional and sufficient.
- **Automatic observer propagation across goroutines** — Go contexts propagate naturally
  when passed to goroutines; no special handling needed.

---

## Open design decisions

| Decision | Options | Current preference |
|----------|---------|-------------------|
| **Stream sink: `Drain` nil-guard** | `Drain` takes `ctx` but `DrainOptions.Observer` is the hook; change nil-guard to context lookup | ✅ Change the nil-guard — consistent with all other adapters |
| **`forge.Registry` nil-guard** | `WithObserver` is a builder method on `Registry`; no `ctx` is passed at setup time. Change? | ❌ Leave unchanged — registry is set up once at startup; explicit `WithObserver` is the correct API there |
| **`stream.Apply` context lookup** | `Apply` receives `ctx` from the caller's pipeline context — use it | ✅ Yes — `ApplyOptions.Observer` nil → `ObserverFromContext(ctx)` |
| **Test isolation** | Context observer is per-context, so tests that pass `context.Background()` without a context observer are unaffected | ✅ No test isolation risk — context-scoped not global |
