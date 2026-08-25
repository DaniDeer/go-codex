# Config, CLI, and Protobuf

> Runnable demos: [`examples/cli-config`](https://github.com/DaniDeer/go-codex/tree/main/examples/cli-config) · [`examples/env-config`](https://github.com/DaniDeer/go-codex/tree/main/examples/env-config)

## CLI config files (YAML, TOML, JSON)

Define a codec once and get type-safe parsing, structured validation errors, and auto-generated JSON Schema for free.

```go
type Config struct {
    Port     int
    LogLevel string
}

var configCodec = codex.Struct[Config](
    codex.RequiredField("port",
        codex.Int().Refine(validate.RangeInt(1, 65535)),
        func(c Config) int { return c.Port },
        func(c *Config, v int) { c.Port = v },
    ),
    codex.DefaultField("log_level",
        codex.String().Refine(validate.OneOf("debug", "info", "warn", "error")),
        "info",  // default value — visible in generated schema
        func(c Config) string { return c.LogLevel },
        func(c *Config, v string) { c.LogLevel = v },
    ),
)

// In main() or cobra's PersistentPreRunE:
data, _ := os.ReadFile("config.toml")
cfg, err := format.TOML(configCodec).Unmarshal(data)
if err != nil {
    // err is codex.ValidationErrors — all field errors collected at once
    log.Fatal(err)
}
```

The same codec works with JSON, YAML, and TOML — swap `format.TOML` for `format.YAML` or `format.JSON` without touching the codec.

## Environment variables (12-factor / containers)

`config.FromEnv` loads config from environment variables. The codec's schema drives string-to-type coercion — no per-field `strconv` code. It returns the loaded config wrapped in a freshly-constructed, already-`Set` `*codex.Immutable[T]` — not a bare struct — since config loading happens exactly once, at startup: the returned value is frozen for the rest of the process's lifetime as a type-level guarantee (a second `Set` on the SAME instance fails), not just a documented convention:

```go
import "github.com/DaniDeer/go-codex/config"

// Env var names: strings.ToUpper(prefix + field_name)
// "port"      + "APP_" → APP_PORT
// "log_level" + "APP_" → APP_LOG_LEVEL
appConfig, err := config.FromEnv(configCodec, "APP_")
if err != nil {
    // err is codex.ValidationErrors
    log.Fatal(err)
}
// ... elsewhere, for the rest of the process's lifetime:
cfg := appConfig.Get()
```

See [Concept: Codec](../concepts/codec.md)'s `Getter`/`Setter` subsection for `Immutable[T]`'s full contract (`Get()` panics before `Set`, `TryGet()` for safe access, second `Set` always fails).

Nested structs expand the prefix (`db.host` → `APP_DB_HOST`). Slices use comma separation. Complex fields also accept JSON:

```sh
# Nested struct as JSON object
APP_DB='{"host":"localhost","port":5432,"name":"mydb"}'

# Slice as JSON array
APP_TAGS='["web","api","v2"]'

# StringMap as JSON object
APP_LABELS='{"env":"prod","team":"platform"}'
```

JSON takes precedence when the value starts with `{` or `[`.

## Single env var (FromEnvVar)

`config.FromEnvVar[T]` loads a single typed value from one environment variable. The codec's schema drives coercion; all `Refine` constraints run after:

```go
import "github.com/DaniDeer/go-codex/config"

// Load a typed port number — returns zero value when APP_PORT is not set
port, err := config.FromEnvVar("APP_PORT",
    codex.Int().Refine(validate.RangeInt(1, 65535)))
if err != nil {
    var envErr config.EnvVarError
    if errors.As(err, &envErr) {
        slog.Warn("env var invalid", "key", envErr.Key, "cause", envErr.Err)
        stats.ReportErrors(obs, "env", envErr.Err)
    }
    log.Fatal(err)
}

// Load a typed log level — falls back to default when not set
level, err := config.FromEnvVar("LOG_LEVEL",
    codex.String().Refine(validate.OneOf("debug", "info", "warn", "error")))
if level == "" {
    level = "info" // caller decides the default
}
```

`FromEnvVar` returns `config.EnvVarError{Key, Err}` when the variable is present but fails coercion or constraint validation. It returns the zero value of T (no error) when the variable is not set — the caller decides whether the variable is required.

Use `FromEnvVar` for ad-hoc overrides of individual settings. Use `config.FromEnv` when loading an entire config struct from environment variables.

## Config file + env var overrides

Env vars are just another string-keyed source — the SAME
`codex.DecodeVars` primitive used for HTTP path/query/header/cookie params
and MQTT topic vars (see
[Concept: Codec — Reusing Field declarations](../concepts/codec.md#reusing-field-declarations-for-pathtopicheaderquery-vars))
applies here too, replacing the hand-rolled `os.Getenv`+`strconv.Atoi`+
manual-assignment recipe:

```go
// portField wraps codex.Int() through a string codec via MapCodecValidated
// — DecodeVars requires a field's codec to accept a string (env vars, like
// every other var source, are string-valued at the wire level), and the
// string→int parse (strconv.Atoi) can fail on non-numeric input, so the
// fallible-both-directions variant is needed (not MapCodecSafe, whose
// decode direction is infallible).
var portField = codex.OptionalField("port",
    codex.MapCodecValidated(codex.String(), codex.Int(),
        strconv.Atoi,
        func(n int) (string, error) { return strconv.Itoa(n), nil }),
    func(c Config) int { return c.Port },
    func(c *Config, v int) { c.Port = v },
)

// Decode the config file
data, _ := os.ReadFile("config.toml")
cfg, _ := format.TOML(configCodec).Unmarshal(data)

// Apply env var overrides — only include vars that are actually set;
// OptionalField skips KEYS ABSENT from the map (an empty string VALUE for
// a present key is not the same as "absent" — DecodeVars only checks
// whether the map contains the key at all).
vars := map[string]string{}
if v := os.Getenv("APP_PORT"); v != "" {
    vars["port"] = v
}
if err := codex.DecodeVars(&cfg, vars, portField); err != nil {
    log.Fatal(err)
}

// Validate the merged config
if err := configCodec.Validate(cfg); err != nil {
    log.Fatal(err)
}
```

This scales cleanly to multiple override vars — declare one `FieldCodec`
per overridable field and pass them all to the same `DecodeVars` call; an
empty string value for an `OptionalField` is treated as absent (skipped),
so unset env vars never overwrite the file-loaded default.

## JSON Schema for editor autocomplete

```go
import "github.com/DaniDeer/go-codex/render/openapi"

yamlBytes, _ := openapi.MarshalYAML(map[string]schema.Schema{
    "Config": configCodec.Schema,
})
// Paste into openapi.yaml or use as JSON Schema for VS Code settings.json
```

## Scope of go-codex in CLI tools

| Concern | Owner |
|---|---|
| Config file parsing, validation, schema | go-codex codec |
| CLI flags (`--flag value`), subcommands, `--help` | [cobra](https://github.com/spf13/cobra), [pflag](https://github.com/spf13/pflag), or stdlib `flag` |

go-codex handles config; cobra handles CLI UX. They compose cleanly.

## Protobuf integration

go-codex and Protobuf solve different problems:

| Concern | Owner |
|---|---|
| Wire format, field numbers, binary encoding | `.proto` + `protoc-gen-go` |
| Validation rules, richer documentation, JSON/YAML/TOML decode | `Codec[T]` |

Write a codec on top of the protoc-generated struct to add what proto cannot express:

```go
// Generated by protoc-gen-go — do not edit.
type CreateUserRequest struct {
    Name  string
    Email string
    Age   int32
}

// Defined by you — the codec adds validation + documentation.
var CreateUserRequestCodec = codex.Struct[CreateUserRequest](
    codex.RequiredField("name",
        codex.String().Refine(validate.NonEmptyString).WithDescription("Display name."),
        func(r CreateUserRequest) string { return r.Name },
        func(r *CreateUserRequest, v string) { r.Name = v },
    ),
    // ...
)
```

**What this gives you:**
- gRPC handles binary transport; the codec handles REST/JSON/YAML config validation.
- `render/openapi` renders the codec's schema as OpenAPI documentation — no separate YAML file.
- Validation rules (`Refine`) live in Go, next to the type, not scattered across proto options.

**What this is not:** go-codex does not generate `.proto` files from codecs, and does not read `.proto` files. The proto file is the wire-format source of truth; the codec is the validation-and-documentation source of truth.

## See also

- [examples/cli-config](https://github.com/DaniDeer/go-codex/tree/main/examples/cli-config) — TOML file + env var overlay
- [examples/env-config](https://github.com/DaniDeer/go-codex/tree/main/examples/env-config) — full env var loading with DefaultField and nested structs
