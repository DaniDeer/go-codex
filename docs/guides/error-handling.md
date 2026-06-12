# Guide: Error Handling

For the full reference of all error types, `errors.As` patterns, and slog integration, see the feature page.

**Feature:** [Error Handling](../features/error-handling.md)

## Key pattern: errors.As + named slog.Logger

```go
logger := slog.Default().With("transport", "http-client")

var pathErr rest.PathParamError
if errors.As(err, &pathErr) {
    logger.Warn("param rejected (no request sent)",
        "param", pathErr.Name,
        "value", pathErr.Value,
        "cause", pathErr.Err,
    )
}
```

Every error type implements `slog.LogValuer` — pass them directly to `slog.Any(...)` for structured key-value output.

## Examples

- [examples/error-types](https://github.com/DaniDeer/go-codex/tree/main/examples/error-types) — every error type demonstrated with `errors.As` and slog
- [examples/decode-errors](https://github.com/DaniDeer/go-codex/tree/main/examples/decode-errors) — multi-field `ValidationErrors` with HTTP 400 response patterns
