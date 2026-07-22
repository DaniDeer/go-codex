# App — Application Lifecycle

> See also: [`app` package on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/app) · [Ports feature](ports.md) · [Wiring Guide](../guides/ports.md)
>
> Runnable demo: [`examples/sensor-service`](https://github.com/DaniDeer/go-codex/tree/main/examples/sensor-service) — `app.New` owns the root context and the ordered teardown of the export sink and HTTP server.

`app` is a **minimal application lifecycle manager**: one root context with
the observer pre-injected, supervised goroutines with fail-fast semantics, and
ordered (LIFO) shutdown hooks. It is a shutdown-ordering helper, not a
framework — ports and adapters know nothing about it.

## Motivation

Wiring a multi-port service by hand means owning context trees, shutdown
ordering, and done-channel choreography in `main()`. `app` replaces that
boilerplate with three declarations:

```go
a := app.New(app.Options{Observer: obs, Logger: logger})
ctx := a.Context() // observer pre-injected; cancelled on shutdown

// Bind ports with the app context; declare teardown where the lifecycle starts.
exports.Bind(ctx, file.DrainWriteFileAdapter(exportFile, varsFor, opts))
exports.Start(ctx)
a.OnShutdown("exports", func(context.Context) error { return exports.Close() })

// Long-lived work runs supervised.
a.Go("alerts-feed", func(ctx context.Context) error {
    alerts.Feed(ctx, alertPayloads) // returns on ctx cancel
    return nil
})

// Services: block until SIGINT/SIGTERM, then ordered teardown.
if err := a.Run(context.Background()); err != nil {
    slog.Error("shutdown finished with errors", "error", err)
}
```

Demos and tests that are not signal-driven call `a.Shutdown()` directly —
both share the same teardown path.

## API

| Symbol | Purpose |
|--------|---------|
| `app.New(app.Options{Observer, Logger, ShutdownTimeout}) *app.App` | Construct; the root context is live immediately; **no signal handlers installed** (that happens inside `Run` only) |
| `App.Context() context.Context` | Cancelable root context with `Options.Observer` pre-injected via `stats.WithObserver` — use for every `Bind`/`Feed`/`Start` call |
| `App.Go(name, fn func(ctx) error)` | Supervised goroutine. **Fail-fast, errgroup-style**: the first non-nil return cancels the app; all errors are collected |
| `App.Supervise(name, start func(ctx) (done <-chan struct{}))` | Supervises a **non-blocking** component: `start` is called once and returns immediately; "finished" is reported only when the returned `done` channel closes — deliberately does **not** race `ctx.Done()` against `done` (that would report completion before the component actually drains). Pairs with [`ports.PipePort.Done()`](ports.md#pipeportt) |
| `App.OnShutdown(name, fn func(ctx) error)` | Shutdown hook, run **LIFO** (defer semantics: close what you opened last, first). A failing hook never stops later hooks. Each hook's ctx is bounded by `ShutdownTimeout` (default 10 s) |
| `App.Run(parent) error` | Blocks until SIGINT/SIGTERM, parent cancellation, or the first goroutine failure — then cancels, waits for goroutines, runs hooks, returns `errors.Join` of everything (nil when clean) |
| `App.Shutdown() error` | The same ordered teardown, directly — idempotent; concurrent calls share one execution |

## Structured errors

Both implement `Error()`, `Unwrap()`, and `slog.LogValuer`:

| Error | When |
|-------|------|
| `GoroutineError{Name, Err}` | A supervised goroutine returned non-nil (triggers fail-fast) |
| `HookError{Name, Err}` | A shutdown hook returned non-nil — including `context.DeadlineExceeded` when it exceeded `ShutdownTimeout` |

Reach individual failures in the joined result with `errors.As`.

## Observer integration

- `Options.Observer` is stored in `App.Context()` — the single place the
  whole service's observer is injected; every port and adapter bound with
  that context resolves it automatically.
- App emits two event families via `Observer.RecordRequest` (plain
  `stats.Observer`, no type assertion): `("app.go", name, 200|500, duration)`
  when a supervised goroutine exits and `("app.shutdown", name, 200|500,
  duration)` per hook — mirroring the `"port.bind"` convention.

## Design notes

- **Fail-fast by design** — a long-running service losing one supervised
  boundary should shut down in an orderly way, not limp. Adapters that should
  survive errors handle them internally (per-adapter `OnError`) and return nil.
- **Zero coupling** — `app` imports only `stats` + stdlib; `ports` and
  `forge` know nothing about it. Teardown registration is always explicit
  (`OnShutdown`), never inferred from context identity.
- **`Supervise` exists to avoid a specific bug** — a naive
  `a.Go(name, func(ctx) error { start(ctx); return nil })` for a
  non-blocking component would report "finished" as soon as `start`
  returns, not when the component actually stops. `Supervise` waits on the
  component's own completion signal (e.g. `ports.PipePort.Done()`) instead.
- `Go`/`OnShutdown`/`Supervise` calls made after shutdown has begun are
  safe, logged no-ops — the goroutine/hook/`start` function is never
  invoked.
- **Out of scope** — dependency graphs between ports, health checks, restart
  policies.
