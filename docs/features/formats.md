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
var pngFile = ports.NewFile(
    "images/{name}.png",
    format.Binary(
        codex.Bytes().
            Refine(validate.MaxBytes(5*1024*1024)).
            Refine(validate.PNG),
    ).WithContentType("image/png"),
    ports.FilePathParam{Name: "name"},
)

// Write — validate.PNG runs before the file is written.
err := pngFile.Write(
    map[string]string{"name": "chart"},
    pngBytes,
    ports.FileOptions{Observer: obs},
)

// Read — validate.PNG runs after the file is read.
data, err := pngFile.Read(
    map[string]string{"name": "chart"},
    ports.FileOptions{Observer: obs},
)
```

Constraint failures surface as `ports.FileEncodeError` (write) or `ports.FileDecodeError` (read), both implementing `Unwrap()` and navigable via `errors.As`. The `FileObserver` callbacks fire on every path — success and failure alike.

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

`ports.File[T]` is a declarative typed file descriptor: declare a path template, wire format, and optional per-variable codecs once — then read, write, and update files with full codec validation. It lives in the `ports` package (alongside `ports.Cache[T]`) because it's a protocol-agnostic addressing descriptor, not a wire format — but using it needs **no pipeline machinery**: no `ports.NewIOPort`/`SinkPort`, no `Bind`, no `stream.Stream`. `Read`/`Write`/`Update`/`Patch` are plain methods you can call in an application that never declares a port at all.

It mirrors the declare-once pattern of `rest.Route` and `events.Channel` — and is the original blueprint `ports.Cache[T]`/`ports.CacheKeyParam` were designed to mirror for `adapters/redis`. See [Design pattern: declarative descriptor + plain function](ports.md#design-pattern-declarative-descriptor--plain-function) for the full comparison across `file`/`cache`/`rest`/`events`/`sql`.

```go
import (
    "github.com/DaniDeer/go-codex/format"
    "github.com/DaniDeer/go-codex/ports"
)

// Declare once — no side effects
var measurementFile = ports.NewFile(
    "data/{date}/{sensor}.json",
    format.JSON(measurementCodec),
    ports.FilePathParam{Name: "date", Description: "ISO date (YYYY-MM-DD)"}.
        WithCodec(codex.String().Refine(validate.Date)),
    ports.FilePathParam{Name: "sensor", Description: "Sensor ID"},
)

// Read a file (validates path vars + decodes + runs constraints)
vars := map[string]string{"date": "2024-01-15", "sensor": "sensor-42"}
m, err := measurementFile.Read(vars, ports.FileOptions{Observer: obs})

// Write a file (validates path vars + encodes + writes)
err = measurementFile.Write(vars, measurement, ports.FileOptions{Perm: 0644})

// Update a file (read → transform → write in one call)
err = measurementFile.Update(vars, func(m Measurement) Measurement {
    m.Value += 1.0
    return m
}, ports.FileOptions{Observer: obs})
```

### Choosing the right write operation

| Operation | Input | When to use |
|-----------|-------|------------|
| `Write(vars, T, opts)` | Full value `T` | Full overwrite — no re-read. **Use this when you already have the decoded value.** |
| `Update(vars, func(T)T, opts)` | Transform function | Need the latest file state before deciding what to write (e.g. increment a counter). |
| `Patch(vars, map[string]any, opts)` | Explicit field map | Partial update: only the keys in the map change; unknown fields dropped. |
| `ports.PatchEncoded(file, vars, Codec[P], P, opts)` | Patch struct + codec | Typed partial update; fields declared in patchCodec but not in file codec are also written. |

> **If you already have a decoded struct in memory, use `Write` directly.** `Update` performs an unnecessary re-read when you already hold the current value.

### Field survival rules

Every write operation filters the output through its codec. Understanding which fields survive is important for multi-schema files:

| Field in file | Field in patch | `Patch` | `PatchEncoded` |
|---------------|---------------|---------|----------------|
| ✓ file codec | — | Preserved (re-written) | Preserved (re-written) |
| ✓ file codec | ✓ patch map / patchCodec | **Updated** | **Updated** |
| ✗ file codec only | ✓ patch map | **Dropped** (no codec validates it) | **Dropped** |
| ✗ file codec only | ✓ patchCodec | N/A | **Written** (validated by patchCodec) |
| ✗ neither codec | — | **Dropped** | **Dropped** |

Key rule: **a field is written only if at least one codec knows about it**. `PatchEncoded` is the correct tool for intentionally adding new fields to a file — declare them in the patch codec.

#### `Patch` — explicit map (RFC 7396)

```go
// Only port and log_level change — max_workers not in the map, preserved unchanged
err = configFile.Patch(nil, map[string]any{
    "port":      9090,
    "log_level": "debug",
}, ports.FileOptions{Observer: obs})
```

#### `ports.PatchEncoded` — typed patch via a separate codec

`PatchEncoded` is a free function (not a method) because Go methods cannot introduce new type parameters. Declare a dedicated patch struct and codec containing only the fields you want to be patchable.

**Updating known fields** — patch type is a subset of the file type:

```go
// Full file type and codec
type AppConfig struct { Port int; LogLevel string; MaxWorkers int }
var configFile = ports.NewFile("config.json", format.JSON(appConfigCodec))

// Patch type — only patchable fields; MaxWorkers cannot be patched via this codec
type AppConfigPatch struct { Port int; LogLevel string }
var appConfigPatchCodec = codex.Struct[AppConfigPatch](
    codex.RequiredField("port",
        codex.Int().Refine(validate.RangeInt(1, 65535)),
        func(p AppConfigPatch) int { return p.Port },
        func(p *AppConfigPatch, v int) { p.Port = v },
    ),
    codex.RequiredField("log_level",
        codex.String().Refine(validate.OneOf("debug", "info", "warn", "error")),
        func(p AppConfigPatch) string { return p.LogLevel },
        func(p *AppConfigPatch, v string) { p.LogLevel = v },
    ),
)

// MaxWorkers preserved — it is not in the patch type
err = ports.PatchEncoded(configFile, nil, appConfigPatchCodec,
    AppConfigPatch{Port: 9090, LogLevel: "debug"},
    ports.FileOptions{Observer: obs},
)
```

**Adding new fields** — patch type contains fields NOT in the file type:

Fields declared in the patch codec but absent from the file codec are **written to the file and preserved**. This is the intended mechanism for intentionally extending a file with new fields:

```go
// Existing file type — does not include FeatureFlags
type AppConfig struct { Port int; LogLevel string }
var configFile = ports.NewFile("config.json", format.JSON(appConfigCodec))

// Patch type adds FeatureFlags — a new field the file codec doesn't know about
type AppConfigPatch struct {
    Port         int
    FeatureFlags map[string]bool `json:"feature_flags"`
}
var extendedPatchCodec = codex.Struct[AppConfigPatch](
    codex.RequiredField("port",
        codex.Int().Refine(validate.RangeInt(1, 65535)),
        func(p AppConfigPatch) int { return p.Port },
        func(p *AppConfigPatch, v int) { p.Port = v },
    ),
    codex.RequiredField("feature_flags",
        codex.StringMap(codex.Bool()),
        func(p AppConfigPatch) map[string]bool { return p.FeatureFlags },
        func(p *AppConfigPatch, v map[string]bool) { p.FeatureFlags = v },
    ),
)

// After this call, config.json contains both AppConfig fields AND feature_flags
err = ports.PatchEncoded(configFile, nil, extendedPatchCodec,
    AppConfigPatch{Port: 9090, FeatureFlags: map[string]bool{"dark_mode": true}},
    ports.FileOptions{Observer: obs},
)
// config.json: {"port":9090,"log_level":"info","feature_flags":{"dark_mode":true}}
```

The file codec (`appConfigCodec`) validates `port` and `log_level`; the patch codec (`extendedPatchCodec`) validates `feature_flags`. Both sets of constraints run before the file is written.

See [`examples/flat-key-patch`](https://github.com/DaniDeer/go-codex/tree/main/examples/flat-key-patch) for a complete demo using flat dotted-key JSON (IoT Edge device twin style, e.g. Azure IoT Edge device twins). Sections covered:

1. Fixed dotted key — `codex.Struct` with a dotted field name literal
2. Dynamic key via `File.Patch` — string concatenation at call time
3. Typed value validation via `PatchEncoded` + `codex.StringMap`
4. Key format + value validation via `PatchEncoded` + `codex.Map`
5. Adding new keys not in the file codec — field survival via patchCodec
6. Schema rendering — OpenAPI YAML from twinCodec / moduleCodec
7. Error cases + structured logging
8. Observer summary (FileMetricsObserver)
9. Key+value merge into `[]Container` via `codex.EntrySlice` (single-segment key)
10. Multi-field key extraction — two segments (tenant + name) into a struct key
11. Static key — constant name injected via `codex.MapCodecSafe`

### Merging JSON object keys into decoded values (`codex.EntrySlice`)

When the JSON object key carries domain meaning (e.g. a container name in `"properties.desired.modules.cv-writer-kvrocks"`), use `codex.EntrySlice` to decode the entire object directly into `[]R` — no post-processing loop needed. The key codec handles prefix stripping and per-segment validation; the value codec decodes the object value; `merge` combines them into a single element.

`EntrySlice` works with any `ports.File`-produced intermediate — JSON, YAML, and TOML (with quoted headers) all produce `map[string]any`, which `EntrySlice` iterates directly.

→ See [Codec — EntrySlice](../concepts/codec.md#merging-key-and-value-into-one-type-entryslice) for the full API, encode path walkthrough, multi-field key extraction, static key pattern, and schema generation details.

### Static paths

For static paths (no template variables), pass `nil` for vars:

```go
var configFile = ports.NewFile("config.toml", format.TOML(configCodec))

cfg, err := configFile.Read(nil, ports.FileOptions{})
err = configFile.Write(nil, cfg, ports.FileOptions{Perm: 0600})
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
| `FilePatchNotSupportedError` | `Patch` or `PatchEncoded` called on Gob/Binary/streaming format |

All errors implement `Unwrap()` for `errors.As`/`errors.Is` traversal and `slog.LogValuer` for structured logging:

```go
var encErr ports.FileEncodeError
if errors.As(err, &encErr) {
    slog.Warn("encode failed", "error", encErr) // → error.path=..., error.cause=...
}
```

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

Environment-variable config loading lives in the standalone [`config`](config.md#environment-variables-12-factor-containers)
package (`config.FromEnv`/`config.FromEnvVar`), not `format` — it never used
`format.Format[T]` internally (no marshal/unmarshal, no wire bytes; just a
codec + schema-driven `os.LookupEnv` coercion), so it doesn't belong here.
See [Config, CLI & Protobuf — Environment variables](config.md#environment-variables-12-factor-containers)
for the full guide.

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
