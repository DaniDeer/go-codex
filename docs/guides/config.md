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
