# go-codex Documentation Review History (DR1–)

Do not re-report any findings listed here. They have been implemented.

---

## Round DR5 (docs sync after R28–R29: slog.LogValuer parity + reqreply rename)

- **D1 — `docs/reference/index.md` mqtt5 row names `ServeRequestReply`+`Request`**: Updated to `Serve`+`Call` to match R29 rename.
- **D2 — `docs/concepts/observable-layers.md` stale `(ServeRequestReply)` annotation**: Changed to `(Serve)` and `(Call)`.
- **D3 — `docs/guides/mqtt5.md` section heading `### Requester (Request)`**: Renamed to `### Caller (Call)`; also fixed inline `// Request — returned directly` comment.
- **D4 — `docs/features/error-handling.md` missing MQTT 5.0 adapter errors section**: Added `## MQTT 5.0 adapter errors` section covering `CallError`, `ServeError`, `BrokerError`, `UserPropertyError`, `MissingUserPropertyError`; updated MQTT 3.1.1 section to include `SubscribeError`, `PublishEncodeError`, `TopicMismatchError`.
- **D5 — `docs/features/error-handling.md` missing `slog.LogValuer` notes for mqtt/mcp error tables**: Added explicit note that all MQTT 3.1.1, MQTT 5.0, and MCP error types implement `slog.LogValuer`; added `ResourceEncodeError` and `InvalidResourceParamError` to MCP table.
- **D6 — `docs/features/error-handling.md` missing reqreply route param errors**: Added `## Request-reply route errors` section covering `RouteParamError`, `MissingRouteParamError`, `DuplicateRouteError` and their relationship to `CallOptions.Vars`.

---

## Round DR4 (README + reference sync after binary additions + R22)

- **D1 — README "Multi-format" bullet missing Binary**: Added "Binary (raw bytes)" to the feature bullet listing JSON, YAML, TOML, Gob.
- **D2 — README "Builtin constraints" bullet missing binary file format validators**: Added "binary file formats (png, jpeg, pdf, zip, …)" to the constraints bullet.
- **D3 — README primitives.go comment missing Base64**: Added `Base64` alongside `Bytes` in the Project Structure tree inline comment for `primitives.go`.
- **D4 — README format/ description missing Binary**: Updated format/ tree entry description and `format.go` inline comment to include `Binary()`.
- **D5 — docs/reference/index.md validate/format descriptions stale**: Updated `validate` row to mention binary file format constants; updated `format` row to mention Binary and File I/O.
- **D6 — docs/features/formats.md missing PathParamSchemas/ValidatePathVars**: Added pre-flight introspection section documenting `ValidatePathVars` and `PathParamSchemas()` (added in R22).

---

## Round DR3 (Binary codec, validators, and format.Binary docs)

- **`docs/features/formats.md` Binary section added**: New "Binary — raw binary file I/O and HTTP bodies" section with Gob vs Binary comparison table, `format.Binary` wiring example, built-in format constraint table (`validate.PNG/JPEG/GIF/WebP/PDF/ZIP`), and `codex.Bytes` vs `codex.Base64` table.
- **`docs/guides/mqtt.md` binary payloads section**: New "Binary payloads" section explaining how `format.Binary` + `WithFormats` works with MQTT, covering size limits, no content-type in MQTT 3.1.1, and error handling.
- **`docs/guides/http-server.md` binary payloads section**: New "Binary payloads" section covering incoming binary request bodies (`WithRequestFormats`), outgoing binary responses (`WithFormats`), and the `MaxBodyBytes` ↔ `validate.MaxBytes` ordering subtlety.
- **`docs/guides/http-client.md` binary section**: New "Binary requests and responses" section covering client-side binary request encoding and binary response decoding via `nethttp.Call`.
- **`validate/doc.go` updated**: Added "Binary byte constraints" and "Binary file format constraints" sections; new "When to use which" and "Composition and ordering" sections.
- **`codex/doc.go` updated**: Added "Binary codecs — Bytes vs Base64" section with use-case table and code examples.
- **`format/doc.go` updated**: Added `Binary`; explains Binary vs Gob vs `NewTyped` relationship.
- **`go-codex.instructions.md` updated**: Primitives list includes `Base64` and raw `Bytes`; validate entry lists `HasPrefix` and binary format constants; format entry includes `Binary`.

---

## Round DR2 (File I/O + FromEnvVar + FileObserver docs gaps)

- **D6 — `docs/features/formats.md` missing File[T] section**: Added "File I/O — declarative typed file access" section covering `NewFile`, `FilePathParam.WithCodec`, `FileOptions`, `Read`/`Write`/`Update`/`BuildPath`, static paths, typed file errors table, and `FileObserver` hook.
- **D7 — `docs/features/config.md` missing FromEnvVar**: Added "Single env var (FromEnvVar)" section with typed example, `EnvVarError` handling, and distinction from `FromEnv`.
- **D8 — `docs/features/observer.md` missing FileObserver**: Added `FileObserver` row to interface table, new "FileObserver (format.File)" section with full implementation example, and added `"file"` to the observer location table; updated `guides/observer.md` location table.
- **D9 — `go-codex.instructions.md` format+stats entries stale**: Updated `format` row to include `File[T]`, `NewFile`, `FilePathParam`, `FileOptions`, all file error types, `FromEnvVar`, and `EnvVarError`; updated `stats` row to include `FileObserver` as 5th optional interface.
- **D10 — `docs/reference/index.md` stats row incomplete**: Added `SecurityObserver` and `FileObserver` to the stats package description.
- **D11 — no `TestFromEnvVar` in `format/env_test.go`**: Added 5 tests covering happy path (int, string), unset-returns-zero, invalid value returns `EnvVarError`, and `Unwrap()` exposes inner error.
- **D12 — `docs/guides/config.md` missing File[T] and FromEnvVar**: Added "Declarative file I/O" and "Single env var" sections with code examples and feature-page links.
- **D13 — `review-go-codex` checklist missing file symbols**: Added `FilePathParam` to param types table; added `FileObserver` to observer interface table with guard rule; added `format` package error table (`FilePathParamError`, `MissingFilePathVarError`, `FileReadError`, `FileDecodeError`, `FileEncodeError`, `FileWriteError`, `EnvVarError`).

---

## Round DR1 (doc.go quality + concept cross-links)

- **D1 — `api/internal/doc.go` thin (3 lines)**: Expanded to 13 lines documenting `ParseTemplateVars`, `StripTemplateVars`, and `BuildFromTemplate` with their roles in template-transparent validation.
- **D2 — `render/jsonschema/doc.go` thin (8 lines)**: Expanded to 18 lines documenting the `Schema()` function, its use by `api/mcp`, and its relationship to `render/internal/schemarender`.
- **D3 — `render/internal/schemarender/doc.go` thin (6 lines)**: Expanded to 18 lines documenting `SchemaObject`, the single-change design rationale, and the `AdditionalPropertiesSchema` field precedence rule.
- **D4 — `docs/concepts/api-contracts.md` See also links stale**: Four `guides/` links replaced with correct `features/` links (REST API, HTTP Client, Events, MCP, API Builders).
- **D5 — `docs/concepts/codec.md` See also links stale**: `guides/error-handling.md` replaced with `features/error-handling.md`.

---

<!-- New rounds go here, above the previous round. -->
