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

// Write after mutation
cfg.LogLevel = "debug"
err = configFile.Write(nil, cfg, format.FileOptions{Perm: 0600})

// Atomic update (read → transform → write)
err = configFile.Update(nil, func(c Config) Config {
    c.Port = 9090
    return c
}, format.FileOptions{})
```

See [Formats & Serialization — File I/O](../features/formats.md#file-io--declarative-typed-file-access) for the full API including path templates and typed errors.

→ [examples/file-io](https://github.com/DaniDeer/go-codex/tree/main/examples/file-io) — full demo: static config + template paths + error handling + `CountingObserver`

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
