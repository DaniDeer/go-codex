# Schema Metadata

> See also: [`codex` package on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/codex)
>
> Runnable demos: [`examples/formats`](https://github.com/DaniDeer/go-codex/tree/main/examples/formats) · [`examples/env-config`](https://github.com/DaniDeer/go-codex/tree/main/examples/env-config)

Codecs carry schema metadata that flows automatically into generated OpenAPI and AsyncAPI documents. These methods annotate the schema without any effect on encode/decode behaviour.

## WithDescription

Adds a `description` to the schema — visible in OpenAPI/AsyncAPI docs and on pkg.go.dev.

```go
var nameCodec = codex.String().
    Refine(validate.NonEmptyString).
    WithDescription("The user's full display name.")
```

## WithTitle

Sets the `title` in the schema. Also used by `forge` as the port name for pipeline graph inference.

```go
var availabilityCodec = codex.Float64().
    Refine(validate.RangeFloat(0, 1)).
    WithTitle("Availability")
// In forge: this becomes the port name "Availability" in the pipeline YAML.
```

## WithExample

Sets the `example` field in the schema — shown in Swagger UI and other OpenAPI renderers.

```go
var emailCodec = codex.String().
    Refine(validate.Email).
    WithDescription("Primary contact email.").
    WithExample("alice@example.com")
```

Output in OpenAPI:
```yaml
email:
  type: string
  format: email
  description: Primary contact email.
  example: alice@example.com
```

## WithDeprecated

Marks the codec (and its schema) as deprecated. Rendered as `deprecated: true` in OpenAPI.

```go
var legacyIPCodec = codex.String().
    Refine(validate.IPv4).
    WithDescription("IPv4 of last login. Deprecated: use hostname instead.").
    WithDeprecated()
```

## DefaultField — optional field with a declared default

`codex.DefaultField` creates an optional field that uses a default value when the key is absent from the input. The default value is reflected into the schema's `default` property.

```go
var configCodec = codex.Struct[Config](
    codex.RequiredField("port",
        codex.Int().Refine(validate.RangeInt(1, 65535)),
        func(c Config) int { return c.Port },
        func(c *Config, v int) { c.Port = v },
    ),
    // Absent key → default "info" is used; default visible in schema
    codex.DefaultField("log_level",
        codex.String().Refine(validate.OneOf("debug", "info", "warn", "error")),
        "info",
        func(c Config) string { return c.LogLevel },
        func(c *Config, v string) { c.LogLevel = v },
    ),
)
```

Output in OpenAPI:
```yaml
log_level:
  type: string
  enum: [debug, info, warn, error]
  default: info
```

`DefaultField` sets `Required: false` and propagates the default into `schema.Schema.Default`. Zero-value defaults are supported — the `Default *F` field uses a pointer to distinguish "no default" from `""` or `0`.

## Comparison: RequiredField / OptionalField / DefaultField

| Constructor | `Required` | Default value | Use when |
|---|---|---|---|
| `RequiredField(name, codec, get, set)` | `true` | — | Field must always be present |
| `OptionalField(name, codec, get, set)` | `false` | zero value of `F` | Field is optional, zero value is acceptable |
| `DefaultField(name, codec, default, get, set)` | `false` | `default` | Field is optional but has a meaningful non-zero default |

## Custom constraint schema annotation

Constraints can annotate the schema by setting `Constraint.Schema`:

```go
func MaxLen(n int) codex.Constraint[string] {
    return codex.Constraint[string]{
        Name:  fmt.Sprintf("maxLen(%d)", n),
        Check: func(v string) bool { return len(v) <= n },
        Message: func(v string) string {
            return fmt.Sprintf("expected at most %d characters, got %d", n, len(v))
        },
        Schema: func(s schema.Schema) schema.Schema {
            s.MaxLength = &n    // reflected into OpenAPI output automatically
            return s
        },
    }
}
```

All built-in `validate.*` constraints use this mechanism — `MinLen`, `MaxLen`, `RangeInt`, `Email`, `UUID`, etc. all annotate the schema automatically.

## See also

- [Concept: Codec](../concepts/codec.md) — full codec reference including all schema annotation methods
- [Guide: OpenAPI Spec](../guides/openapi.md) — how schema metadata flows into OpenAPI output
- [examples/formats](https://github.com/DaniDeer/go-codex/tree/main/examples/formats) — `WithExample` and `WithDeprecated` in context
- [examples/env-config](https://github.com/DaniDeer/go-codex/tree/main/examples/env-config) — `DefaultField` with `config.FromEnv`
