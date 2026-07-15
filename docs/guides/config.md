# Guide: Config, CLI & Protobuf

For the full API reference and all code examples, see the feature page.

**Feature:** [Config, CLI & Protobuf](../features/config.md)

## examples/cli-config

Demonstrates TOML config file loading with env var overlay:

1. Decode the TOML file via `format.TOML(configCodec).Unmarshal(data)`
2. Apply `os.Getenv(...)` overrides to specific struct fields
3. Call `configCodec.Validate(cfg)` to validate the merged config

→ [examples/cli-config](https://github.com/DaniDeer/go-codex/tree/main/examples/cli-config)

## examples/env-config

Full `format.FromEnv` demo including:
- `DefaultField` with default value visible in schema
- Nested struct expansion (`APP_DB_HOST`)
- Slice from comma-separated values (`APP_TAGS=web,api`)
- Complex fields as JSON strings (`APP_DB='{"host":"localhost",...}'`)
- JSON Schema output for VS Code settings autocomplete

→ [examples/env-config](https://github.com/DaniDeer/go-codex/tree/main/examples/env-config)

## Declarative file I/O (format.File)

`format.File[T]` reads and writes typed, validated config files using any wire format (JSON, YAML, TOML). Declare the file descriptor once and reuse it across the application:

```go
var configFile = format.NewFile("config.toml", format.TOML(configCodec))

// Read + validate in one call
cfg, err := configFile.Read(nil, format.FileOptions{Observer: obs})

// Write after mutation — use Write directly when you already have the decoded value
cfg.LogLevel = "debug"
err = configFile.Write(nil, cfg, format.FileOptions{Perm: 0600})

// Atomic update (read → transform → write) — use when you need the latest file state
err = configFile.Update(nil, func(c Config) Config {
    c.Port = 9090
    return c
}, format.FileOptions{})
```

For partial updates (`Patch`, `PatchEncoded`) and the field survival rules, see [Formats & Serialization — Choosing the right write operation](../features/formats.md#choosing-the-right-write-operation).

→ [examples/file-io](https://github.com/DaniDeer/go-codex/tree/main/examples/file-io) — full demo: static config + template paths + Patch + PatchEncoded + error handling + `CountingObserver`

## Single env var (format.FromEnvVar)

`format.FromEnvVar[T]` replaces manual `os.LookupEnv` + `strconv` for individual settings:

```go
// Returns zero value when not set — no error
port, err := format.FromEnvVar("APP_PORT",
    codex.Int().Refine(validate.RangeInt(1, 65535)))
if err != nil {
    var envErr format.EnvVarError
    errors.As(err, &envErr)
    slog.Error("env var invalid", "key", envErr.Key, "err", envErr.Err)
}
```

See [Config, CLI & Protobuf — Single env var](../features/config.md#single-env-var-fromenvvar) for full details.

## Passing env config into pipeline functions

Pipeline functions (`forge.NewFunction` apply funcs, `gstream.Filter`/`FlatMapSlice`
predicates, …) are deliberately pure — no `ctx`, no side channels. The clean way to
parameterize them from env vars is the **validated-config factory pattern**: load and
validate config once in `main()`, then pass the typed struct into a factory that
closes over it.

```go
// domain layer — TYPED, already-validated config; zero env access here.
type AlertConfig struct{ Threshold float64 }

var alertConfigCodec = codex.Struct[AlertConfig](
    codex.DefaultField("threshold",
        codex.Float64().Refine(validate.MinFloat(0)).WithDescription("Alert threshold."),
        50.0, // used when APP_ALERT_THRESHOLD is unset — also visible in the schema
        func(c AlertConfig) float64 { return c.Threshold },
        func(c *AlertConfig, v float64) { c.Threshold = v }),
)

// Factory closes over the config — the returned function stays pure and testable
// (tests pass any AlertConfig directly; no env manipulation needed).
func newShouldAlert(cfg AlertConfig) func(db.Reading) bool {
    return func(r db.Reading) bool { return r.Value > cfg.Threshold }
}

// main() — load + validate ONCE, at the same place ports and adapters are wired.
alertCfg, err := format.FromEnv(alertConfigCodec, "APP_ALERT_") // APP_ALERT_THRESHOLD
must(err, "load alert config from env")
shouldAlert := newShouldAlert(alertCfg)

aboveThreshold := gstream.Filter(ctx, readings, shouldAlert)
```

The codec **is** the env contract: variable names, type coercion, constraints,
defaults (`codex.DefaultField`), and documentation all live in one declaration —
the same declare-once philosophy `ports.Pattern` applies to wire contracts.

Why this and not the alternatives:

| Alternative | Verdict |
|---|---|
| `os.Getenv` inside the pipeline function | **Anti-pattern** — unvalidated string, hidden dependency, untestable, re-read per call |
| Context value (like `stats.WithObserver`) | Wrong tool — context is for *per-request* values (observer, trace span), not static config; loses compile-time typing |
| An env `ports.SourceAdapter` | Synthetic fit — env vars are a construction-time concern, not a runtime stream a pipeline continuously reads |
| Validated-config factory (above) | **Recommended** — validated once, typed everywhere, testable |

→ [examples/sensor-service](https://github.com/DaniDeer/go-codex/tree/main/examples/sensor-service) — live demonstration: `APP_ALERT_THRESHOLD=90 go run ./examples/sensor-service` changes the alert filter and the printed stream topology; unset, the `DefaultField` value 50.0 applies.
