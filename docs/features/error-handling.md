# Error Handling

> See also: [`codex` package on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/codex)
>
> Runnable demos: [`examples/error-types`](https://github.com/DaniDeer/go-codex/tree/main/examples/error-types) · [`examples/decode-errors`](https://github.com/DaniDeer/go-codex/tree/main/examples/decode-errors)

All decode failures are structured types. Use `errors.As` to inspect them precisely, or pass them directly to `log/slog` — every type implements `slog.LogValuer`.

## Error types

| Type | Returned by | Key fields |
|---|---|---|
| `ValidationErrors` | `Struct` decode | `[]ValidationError`; also implements `Unwrap() []error` |
| `ValidationError` | each field in `Struct` decode | `Field string`, `Err error` |
| `ConstraintError` | `Refine` on any codec (Encode and Decode) | `Name string`, `Message string` |
| `TypeMismatchError` | any codec receiving wrong Go type | `Expected string`, `Got string` |
| `ElementError` | `SliceOf` decode | `Index int`, `Err error` |
| `KeyError` | `StringMap` / `Map` decode | `Key string`, `Err error` |
| `UnknownVariantError` | `TaggedUnion` when tag value has no matching codec | `Tag string`, `Variant string` |
| `VariantError` | `TaggedUnion` when a known variant fails to decode/encode | `Tag string`, `Variant string`, `Err error` |
| `ErrMissingField` | required `Field` when key absent | sentinel; use `errors.Is` |

## Inspecting errors with errors.As

```go
var ve codex.ValidationErrors
if errors.As(err, &ve) {
    for _, fieldErr := range ve {
        var ce codex.ConstraintError
        if errors.As(fieldErr.Err, &ce) {
            // ce.Name    — constraint identifier, e.g. "email", "minLen(3)"
            // ce.Message — human-readable description of the failure
            fmt.Printf("field %q: constraint %q failed: %s\n",
                fieldErr.Field, ce.Name, ce.Message)
        }
        if errors.Is(fieldErr.Err, codex.ErrMissingField) {
            fmt.Printf("field %q is required but absent\n", fieldErr.Field)
        }
    }
}
```

## Structured logging with log/slog

All error types implement `slog.LogValuer`. Pass them as slog attributes to get structured key-value output:

```go
logger := slog.Default().With("transport", "http")

var ve codex.ValidationErrors
if errors.As(err, &ve) {
    // Emits each field name and its error as separate slog attributes.
    logger.Error("request validation failed", slog.Any("validation_errors", ve))

    for _, fieldErr := range ve {
        var ce codex.ConstraintError
        if errors.As(fieldErr.Err, &ce) {
            // Emits field.field, field.error, constraint.constraint, constraint.message.
            logger.Warn("field constraint failed",
                slog.Any("field", fieldErr),
                slog.Any("constraint", ce),
            )
        }
    }
}
```

## HTTP adapter errors (api/rest + adapters/nethttp)

| Error type | When returned | `errors.As` target |
|---|---|---|
| `rest.PathParamError` | path variable fails its codec | `PathParamError{Name, Value, Err}` |
| `rest.MissingPathVarError` | template variable absent from vars map | `MissingPathVarError{Name}` |
| `rest.QueryParamError` | query parameter value fails its codec | `QueryParamError{Name, Value, Err}` |
| `rest.CookieParamError` | cookie value fails its codec | `CookieParamError{Name, Value, Err}` |
| `rest.HeaderParamError` | request header value fails its codec | `HeaderParamError{Name, Value, Err}` |
| `rest.ResponseHeaderParamError` | response header fails codec (adapter returns 500) | `ResponseHeaderParamError{Name, Value, Err}` |
| `rest.ResponseCookieParamError` | response cookie fails codec (adapter returns 500) | `ResponseCookieParamError{Name, Value, Err}` |
| `rest.UnsupportedMediaTypeError` | wrong `Content-Type` on POST/PUT/PATCH | `UnsupportedMediaTypeError{Got, Supported}` |
| `rest.NotAcceptableError` | `Accept` header has no match | `NotAcceptableError{Accept, Supported}` |
| `rest.BodyTooLargeError` | body exceeds `Options.MaxBodyBytes` | `BodyTooLargeError{Limit}` |
| `rest.SecurityCredentialError` | credential codec validation failure | `SecurityCredentialError{Scheme, Err}` |
| `rest.SecurityError` | `SecurityFunc` rejected the request | `SecurityError{Err}` |

## HTTP client errors (adapters/nethttp.Call)

| Error type | When returned |
|---|---|
| `nethttp.UnexpectedStatusError{Method, Path, StatusCode, Body}` | non-2xx response |
| `nethttp.RequestBuildError{Err}` | `http.NewRequestWithContext` failure |
| `nethttp.RequestError{Method, Path, Err}` | transport failure (network, DNS, TLS) |
| `nethttp.ResponseBodyError{Err}` | `io.ReadAll` failure on response body |

```go
var statusErr nethttp.UnexpectedStatusError
if errors.As(err, &statusErr) {
    slog.Error("api call failed",
        "method", statusErr.Method,
        "path",   statusErr.Path,
        "status", statusErr.StatusCode,
        "body",   string(statusErr.Body),
    )
}
```

## MQTT 3.1.1 adapter errors (adapters/mqtt)

All MQTT adapter error types implement `slog.LogValuer`.

| Error type | When returned |
|---|---|
| `mqtt.SubscribeError{Kind, Topic, Err}` | decode, handler, or security failure in `SubscribeHandler` |
| `mqtt.PublishEncodeError{Topic, Err}` | payload encode failure in `Publish` |
| `mqtt.TopicMismatchError{Template, Topic}` | concrete topic doesn't match template structure |
| `events.TopicParamError{Name, Value, Err}` | topic variable fails its codec |
| `events.MissingTopicVarError{Name}` | topic variable absent from vars map |

```go
mqtt.SubscribeHandler(ctx, channel, handler, mqtt.SubscribeOptions{
    OnError: func(e mqtt.SubscribeError) {
        switch e.Kind {
        case mqtt.KindDecode:
            var validationErrs codex.ValidationErrors
            if errors.As(e.Err, &validationErrs) {
                logger.Warn("decode validation error",
                    "topic", e.Topic,
                    "errors", validationErrs, // triggers ValidationErrors.LogValue()
                )
            }
        case mqtt.KindHandler:
            logger.Error("handler error", "error", e) // slog.LogValuer: emits kind, topic, err
        }
    },
})
```

## MQTT 5.0 adapter errors (adapters/mqtt5)

All MQTT 5.0 adapter error types implement `slog.LogValuer`.

| Error type | When returned |
|---|---|
| `mqtt5.SubscribeError{Kind, Topic, Err}` | decode, handler, or security failure in `Subscribe` |
| `mqtt5.PublishEncodeError{Topic, Err}` | payload encode failure in `Publish` |
| `mqtt5.ServeError{Kind, Err}` | decode, handler, or encode failure in `Serve` (responder side) |
| `mqtt5.CallError{Kind, Err}` | encode, timeout, server-error, or decode failure in `Call` (caller side) |
| `mqtt5.BrokerError{Op, Err}` | broker-level failure in `Subscribe`/`Publish` (Op: "subscribe", "publish") |
| `mqtt5.UserPropertyError{Name, Value, Err}` | User Property codec validation failure |
| `mqtt5.MissingUserPropertyError{Name}` | required User Property absent |

```go
// Call (requester side) — returned directly.
resp, err := mqtt5adapter.Call(ctx, client, router, handle, req, mqtt5adapter.CallOptions{})
var callErr mqtt5.CallError
if errors.As(err, &callErr) {
    switch callErr.Kind {
    case mqtt5.KindTimeout:  // no reply within deadline
    case mqtt5.KindHandler:  // server returned an error reply
    case mqtt5.KindDecode:   // reply payload could not be decoded
    }
    slog.Error("call failed", "error", callErr) // emits kind, err
}

// Serve (responder side) — delivered to OnError callback.
mqtt5adapter.Serve(ctx, client, router, handle, fn, mqtt5adapter.ServeOptions{
    OnError: func(e mqtt5.ServeError) {
        slog.Warn("serve error", "error", e) // emits kind, err
    },
})
```

## Request-reply route errors (api/reqreply)

| Error type | When returned |
|---|---|
| `reqreply.RouteParamError{Name, Value, Err}` | topic variable fails its codec in `BuildTopic` or `ValidateTopicVars` |
| `reqreply.MissingRouteParamError{Name}` | topic variable absent from vars map |
| `reqreply.DuplicateRouteError{Topic}` | same topic registered twice with one `Builder` |

These are returned by `RouteHandle.BuildTopic` and wrapped by adapter call errors (e.g. `mqtt5.CallError`, `zeromq.CallError`) when `CallOptions.Vars` resolves a template topic.

## Forge pipeline errors (forge)

| Error type | When |
|---|---|
| `forge.InputError{Err}` | input codec validation failed |
| `forge.RefinementError{Function, Err}` | `RefineFunc` or `WithRefinement` constraint failed |
| `forge.ApplyError{Function, Err}` | compute function returned an error |
| `forge.OutputError{Err}` | output codec validation failed |
| `forge.CollectionElementError{Index, Function, Err}` | slice collection op failed at element |
| `forge.CollectionKeyError{Key, Function, Err}` | map collection op failed at key |

## MCP errors (api/mcp)

All MCP error types implement `slog.LogValuer`.

| Error type | When |
|---|---|
| `mcp.ToolInputError{Name, Err}` | `ToolHandle.Decode` — input codec failure |
| `mcp.ToolOutputError{Name, Err}` | `ToolHandle.Encode` — output codec failure |
| `mcp.ResourceEncodeError{URI, Err}` | `ResourceHandle.Encode` — resource encode failure |
| `codex.ValidationErrors` | `ResourceHandle.BuildURI`/`ExtractURIVars` — URI variable fails its codec or is absent (via `Template[V]`'s own `Codec()`) |
| `codex.TemplateMismatchError{Template, Concrete}` | `ResourceHandle.ExtractURIVars` — received URI doesn't match the template's structure |
| `mcp.PromptArgError{Name, Err}` | prompt argument codec failure |
| `mcp.MissingPromptArgError{Name}` | required prompt argument absent |

## See also

- [examples/error-types](https://github.com/DaniDeer/go-codex/tree/main/examples/error-types) — every error type with `errors.As` and slog
- [examples/decode-errors](https://github.com/DaniDeer/go-codex/tree/main/examples/decode-errors) — struct validation + HTTP 400 patterns
