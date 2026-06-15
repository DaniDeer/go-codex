# Formats & Serialization

> See also: [`format` package on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/format)
>
> Runnable demo: [`examples/formats`](https://github.com/DaniDeer/go-codex/tree/main/examples/formats) · [`examples/multiformat`](https://github.com/DaniDeer/go-codex/tree/main/examples/multiformat) · [`examples/file-io`](https://github.com/DaniDeer/go-codex/tree/main/examples/file-io)

A `Codec[T]` works with an intermediate representation (`map[string]any`) that is format-agnostic. The `format` package bridges that intermediate to concrete wire formats — the **same codec** reads and writes multiple formats without any changes.

## Built-in formats

```go
json := format.JSON(userCodec)   // Content-Type: application/json
yaml := format.YAML(userCodec)   // Content-Type: application/yaml
toml := format.TOML(userCodec)   // Content-Type: application/toml
gob  := format.Gob(userCodec)    // Content-Type: application/gob (binary)
```

All four share the same codec — constraints, schema, and field names are identical.

```go
// Encode to JSON
data, _ := json.Marshal(User{Name: "Alice", Email: "alice@example.com"})
// {"email":"alice@example.com","name":"Alice"}

// Round-trip through YAML — same codec, same validation
user, _ := yaml.Unmarshal([]byte("name: Alice\nemail: alice@example.com\n"))
```

## Gob — binary Go-to-Go encoding

`format.Gob` uses `encoding/gob` under the hood, bypassing the `map[string]any` intermediate and operating on the typed value directly. Constraints run on both marshal and unmarshal — the same rules apply regardless of format.

```go
var GobFormat = format.Gob(OrderCodec)

// Producer: marshal to binary bytes
data, _ := GobFormat.Marshal(Order{ID: "...", Quantity: 2})

// Consumer: unmarshal — constraints re-validated
order, err := GobFormat.Unmarshal(data)
```

Gob is ideal for internal Go-to-Go communication where binary efficiency matters. Use JSON/YAML/TOML for external-facing APIs or when OpenAPI/AsyncAPI spec generation is needed.

**Gob in OpenAPI/AsyncAPI specs:** the spec renderer emits `"application/gob"` as the content type alongside the JSON Schema body. The schema correctly documents the logical data shape for human readers, but tooling (Swagger UI, API gateways, code generators) cannot interpret binary gob payloads. Keep `"application/gob"` out of external-facing specs; use it only for internal Go-to-Go channels where the Go library itself is the authoritative contract.

## File I/O — declarative typed file access

`format.File[T]` is a declarative typed file descriptor: declare a path template, wire format, and optional per-variable codecs once — then read, write, and update files with full codec validation.

It mirrors the declare-once pattern of `rest.Route` and `events.Channel`:

```go
import "github.com/DaniDeer/go-codex/format"

// Declare once — no side effects
var measurementFile = format.NewFile(
    "data/{date}/{sensor}.json",
    format.JSON(measurementCodec),
    format.FilePathParam{Name: "date", Description: "ISO date (YYYY-MM-DD)"}.
        WithCodec(codex.String().Refine(validate.Date)),
    format.FilePathParam{Name: "sensor", Description: "Sensor ID"},
)

// Read a file (validates path vars + decodes + runs constraints)
vars := map[string]string{"date": "2024-01-15", "sensor": "sensor-42"}
m, err := measurementFile.Read(vars, format.FileOptions{Observer: obs})

// Write a file (validates path vars + encodes + writes)
err = measurementFile.Write(vars, measurement, format.FileOptions{Perm: 0644})

// Update a file (read → transform → write in one call)
err = measurementFile.Update(vars, func(m Measurement) Measurement {
    m.Value += 1.0
    return m
}, format.FileOptions{Observer: obs})
```

### Static paths

For static paths (no template variables), pass `nil` for vars:

```go
var configFile = format.NewFile("config.toml", format.TOML(configCodec))

cfg, err := configFile.Read(nil, format.FileOptions{})
err = configFile.Write(nil, cfg, format.FileOptions{Perm: 0600})
```

### Pre-flight path validation

`BuildPath` substitutes template variables and validates without any I/O — useful for early error detection:

```go
path, err := measurementFile.BuildPath(vars)
// Returns FilePathParamError when a variable fails its codec
// Returns MissingFilePathVarError when a variable is absent
```

### Typed file errors

| Error type | Returned by |
|---|---|
| `FilePathParamError` | path variable fails its codec constraint |
| `MissingFilePathVarError` | path variable not in provided map |
| `FileReadError` | `os.ReadFile` fails |
| `FileDecodeError` | codec decode or constraint validation fails |
| `FileEncodeError` | codec encode fails |
| `FileWriteError` | `os.WriteFile` fails |

All errors implement `Unwrap()` for `errors.As`/`errors.Is` traversal and `slog.LogValuer` for structured logging.

### FileObserver

`FileOptions.Observer` accepts any `stats.Observer`. When the observer also implements `stats.FileObserver`, it receives per-operation lifecycle events:

```go
type FileObserver interface {
    RecordFileRead(path string, success bool, d time.Duration)
    RecordFileWrite(path string, success bool, d time.Duration)
}
```

See [Metrics Observer](observer.md) for the full observer interface table.

## Loading from environment variables

`format.FromEnv` loads a typed struct from environment variables using the codec's schema:

```go
// Env var names: strings.ToUpper(prefix + field_name)
// "port"      + "APP_" → APP_PORT
// "log_level" + "APP_" → APP_LOG_LEVEL
cfg, err := format.FromEnv(configCodec, "APP_")
```

Nested structs expand the prefix (`db.host` → `APP_DB_HOST`). Slices use comma separation. Complex fields accept JSON:

```sh
APP_DB='{"host":"localhost","port":5432,"name":"mydb"}'
APP_TAGS='["web","api","v2"]'
```

## Custom formats

The `format` package provides three constructors for custom wire formats:

| Constructor | Intermediate | Use cases |
|---|---|---|
| `format.New[T](codec, marshal, unmarshal)` | `map[string]any` | CBOR, MessagePack, XML |
| `format.NewTyped[T](codec, marshal, unmarshal, ct)` | typed `T` directly | templ HTML, Protobuf, CSV |
| `format.NewStreamed[T](codec, marshalTo, unmarshal, ct)` | writes to `io.Writer` | SSR streaming, chunked responses |

### `format.New` — map-based formats

Use for any format that has a map-based intermediate (CBOR, MessagePack, XML, etc.):

```go
msgpackFmt := format.New(userCodec,
    func(v any) ([]byte, error) { return msgpack.Marshal(v) },
    func(b []byte) (any, error) { var m any; return m, msgpack.Unmarshal(b, &m) },
).WithContentType("application/msgpack")
```

### `format.NewTyped` — typed direct formats

Use when the format renderer takes the typed value directly (templ components, Protobuf, CSV):

```go
csvFmt := format.NewTyped(userCodec,
    func(u User) ([]byte, error) {
        return []byte(fmt.Sprintf("%s,%s\n", u.Name, u.Email)), nil
    },
    func([]byte) (User, error) { return User{}, errors.New("csv: decode not supported") },
    "text/csv",
)
```

`format.NewTyped` runs the codec's `Refine` constraints on the value **before** calling `marshal`, so validation always fires regardless of output format.

**Binary formats** — `NewTyped` bypasses the `map[string]any` intermediate entirely, so the wire representation stays binary. The unmarshal function must call `codec.Validate` explicitly (unlike the JSON/YAML path which runs through `Decode`):

```go
// Raw PNG request body with magic-byte validation.
pngMagic := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
pngCodec := codex.Codec[[]byte]{
    Schema: schema.Schema{Type: "string", Format: "binary"},
    Encode: func(v []byte) (any, error) { return v, nil },
    Decode: func(v any) ([]byte, error) { return v.([]byte), nil },
}.
    Refine(validate.MaxBytes(5 * 1024 * 1024)).  // reject before reading content
    Refine(codex.Constraint[[]byte]{
        Name:    "png-header",
        Check:   func(v []byte) bool { return len(v) >= 8 && bytes.Equal(v[:8], pngMagic) },
        Message: func([]byte) string { return "expected PNG magic bytes" },
    })

pngFormat := format.NewTyped(
    pngCodec,
    func(v []byte) ([]byte, error) { return v, nil },
    func(data []byte) ([]byte, error) {
        if err := pngCodec.Validate(data); err != nil { return nil, err }
        return data, nil
    },
    "image/png",
)
```

See [`examples/png-upload`](https://github.com/DaniDeer/go-codex/tree/main/examples/png-upload) for the full route definition with `PathParam` and `CookieParam` codec validation alongside the PNG request format.

### `format.NewStreamed` — streaming formats

Use for SSR streaming and chunked responses — validates first, then writes to `io.Writer` without buffering:

```go
streamFmt := format.NewStreamed(propsCodec,
    func(props PageProps, w io.Writer) error {
        return pageComponent(props).Render(context.Background(), w)
    },
    func([]byte) (PageProps, error) { return PageProps{}, errors.New("decode not supported") },
    "text/html",
)
```

Call `IsStreamable()` to detect streaming formats. The adapter commits headers only after validation passes — a failed validation never writes partial output.

## Multi-format content negotiation

Register multiple response formats on a route. The adapter picks the format matching the client's `Accept` header:

```go
articleRoute = articleRoute.WithFormats(
    adapttempl.Format(propsCodec, ArticleCard), // Accept: text/html
    format.JSON(propsCodec),                     // Accept: application/json
    format.YAML(propsCodec),                     // Accept: application/yaml
)
// */* → picks the first format (text/html)
// Unknown Accept → 406 NotAcceptableError
```

Multi-format request bodies (Content-Type negotiation):

```go
createUserRoute = createUserRoute.WithRequestFormats(
    format.JSON(createUserReqCodec),
    format.YAML(createUserReqCodec),
)
// Wrong Content-Type → 415 UnsupportedMediaTypeError
```

## See also

- [Guide: HTTP Server](../guides/http-server.md) — multi-format in the HTTP adapter
- [Feature: SSE & Streaming](sse-streaming.md) — streaming formats in practice
- [examples/multiformat](https://github.com/DaniDeer/go-codex/tree/main/examples/multiformat) — JSON/YAML/TOML from one codec
- [examples/gob-contract](https://github.com/DaniDeer/go-codex/tree/main/examples/gob-contract) — binary Gob contract pattern
