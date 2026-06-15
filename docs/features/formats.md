# Formats & Serialization

> See also: [`format` package on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/format)
>
> Runnable demo: [`examples/formats`](https://github.com/DaniDeer/go-codex/tree/main/examples/formats) · [`examples/multiformat`](https://github.com/DaniDeer/go-codex/tree/main/examples/multiformat) · [`examples/file-io`](https://github.com/DaniDeer/go-codex/tree/main/examples/file-io)

A `Codec[T]` works with an intermediate representation (`map[string]any`) that is format-agnostic. The `format` package bridges that intermediate to concrete wire formats — the **same codec** reads and writes multiple formats without any changes.

## Built-in formats

```go
json   := format.JSON(userCodec)   // Content-Type: application/json
yaml   := format.YAML(userCodec)   // Content-Type: application/yaml
toml   := format.TOML(userCodec)   // Content-Type: application/toml
gob    := format.Gob(userCodec)    // Content-Type: application/gob (Go-to-Go binary)
binary := format.Binary(bytesCodec) // Content-Type: application/octet-stream (raw binary)
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

## Binary — raw binary file I/O and HTTP bodies

`format.Binary` writes and reads `[]byte` exactly as-is — no encoding framing is added. Any tool that understands the underlying format (an image viewer, a PDF reader, a media player) can open the resulting file directly.

**Gob vs Binary:**

| | `format.Gob` | `format.Binary` |
|---|---|---|
| Wire form | Gob-framed bytes (adds header) | Raw bytes — byte-identical to original |
| Opens in external tools | No | Yes |
| Use case | Go-to-Go internal caching | PNG, JPEG, PDF, WAV file I/O; binary HTTP bodies |

```go
// Declare once — the file descriptor validates format on every read and write.
var pngFile = format.NewFile(
    "images/{name}.png",
    format.Binary(
        codex.Bytes().
            Refine(validate.MaxBytes(5*1024*1024)).
            Refine(validate.PNG),
    ).WithContentType("image/png"),
    format.FilePathParam{Name: "name"},
)

// Write — validate.PNG runs before the file is written.
err := pngFile.Write(
    map[string]string{"name": "chart"},
    pngBytes,
    format.FileOptions{Observer: obs},
)

// Read — validate.PNG runs after the file is read.
data, err := pngFile.Read(
    map[string]string{"name": "chart"},
    format.FileOptions{Observer: obs},
)
```

Constraint failures surface as `format.FileEncodeError` (write) or `format.FileDecodeError` (read), both implementing `Unwrap()` and navigable via `errors.As`. The `FileObserver` callbacks fire on every path — success and failure alike.

### Built-in binary format constraints

The `validate` package provides predefined constraints for common binary file formats. Each checks the file's magic bytes — no external dependency required:

| Constraint | Magic bytes | Covers |
|-----------|------------|--------|
| `validate.PNG` | `\x89PNG\r\n\x1a\n` | PNG images |
| `validate.JPEG` | `\xFF\xD8\xFF` | JPEG images (JFIF, Exif, all subtypes) |
| `validate.GIF` | `GIF87a` / `GIF89a` | GIF images (both versions) |
| `validate.WebP` | `RIFF....WEBP` | WebP images |
| `validate.PDF` | `%PDF-` | PDF documents |
| `validate.ZIP` | `PK\x03\x04` | ZIP archives; also DOCX, XLSX, APK, JAR |

```go
// Image upload — accept JPEG or PNG, max 5 MiB each
jpegCodec := codex.Bytes().Refine(validate.MaxBytes(5*1024*1024)).Refine(validate.JPEG)
pngCodec  := codex.Bytes().Refine(validate.MaxBytes(5*1024*1024)).Refine(validate.PNG)

// Document upload — PDF only
pdfCodec := codex.Bytes().Refine(validate.MaxBytes(20*1024*1024)).Refine(validate.PDF)

// Archive upload
zipCodec := codex.Bytes().Refine(validate.MaxBytes(50*1024*1024)).Refine(validate.ZIP)
```

### codex.Bytes vs codex.Base64

Both work with `[]byte` in Go, but the wire representation differs:

| | `codex.Bytes()` | `codex.Base64()` |
|---|---|---|
| OpenAPI schema | `type: string, format: binary` | `type: string, format: byte` |
| Wire representation | Raw bytes (not encoded) | Base64 string |
| Use case | Binary file I/O, HTTP binary body | Binary field embedded in a JSON document |

```go
// Bytes: raw binary in a file or HTTP body
pngCodec := codex.Bytes().Refine(validate.PNG)

// Base64: binary field inside a JSON document
avatarField := codex.Base64().Refine(validate.MaxBytes(65536)).
    WithDescription("Profile image (base64, max 64 KiB).")
```

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

### Pre-flight path validation and introspection

`BuildPath` substitutes template variables and validates without any I/O — useful for early error detection:

```go
path, err := measurementFile.BuildPath(vars)
// Returns FilePathParamError when a variable fails its codec
// Returns MissingFilePathVarError when a variable is absent
```

`ValidatePathVars` runs only the codec constraints without building the full path (useful when you have the vars map and want to check them before calling `BuildPath`):

```go
err := measurementFile.ValidatePathVars(vars)
```

`PathParamSchemas` returns a map of variable name → codec schema for each param that has a codec registered — useful for documentation generation and spec tooling:

```go
schemas := measurementFile.PathParamSchemas()
// map["date"] = schema.Schema{Type:"string", Format:"date"}
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

**Raw binary `[]byte` formats** — for raw binary data where `T = []byte`, use `format.Binary` instead of wiring `NewTyped` manually:

```go
// format.Binary is the recommended shorthand for raw []byte I/O.
// validate.PNG checks the PNG magic bytes — no manual byte slice needed.
pngFormat := format.Binary(
    codex.Bytes().
        Refine(validate.MaxBytes(5 * 1024 * 1024)).
        Refine(validate.PNG),
).WithContentType("image/png")
```

`format.Binary` is equivalent to `NewTyped[[]byte]` with identity marshal/unmarshal — `NewTyped` is not needed for the `[]byte` raw passthrough case.

**Typed binary formats** — use `NewTyped` directly when `T ≠ []byte` or when real encoding/decoding logic is required (e.g. `image.Image`, Protobuf structs, write-only formats):

```go
// Decode PNG bytes into image.Image — NewTyped is the right tool here.
imgFormat := format.NewTyped(
    imageCodec,
    func(img image.Image) ([]byte, error) {
        var buf bytes.Buffer
        return buf.Bytes(), png.Encode(&buf, img)
    },
    func(data []byte) (image.Image, error) {
        return png.Decode(bytes.NewReader(data))
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
