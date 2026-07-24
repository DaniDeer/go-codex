---
description: "Design instructions for go-codex: an autodocodec-inspired self-documenting codec library for Go"
applyTo: "**/*.go,**/go.mod"
---

# go-codex Development Instructions

go-codex is a Go port of the core ideas from Haskell's [autodocodec](https://hackage.haskell.org/package/autodocodec). A single `Codec[T]` value simultaneously describes how to encode, decode, and document a type. Write once; derive JSON, YAML, OpenAPI, and other representations from the same definition.

**Module:** `github.com/DaniDeer/go-codex`
**Go version:** 1.26.2

## Design Philosophy

- One `Codec[T]` is the single source of truth for encode, decode, and schema.
- Codecs compose: build complex codecs from primitive ones; never duplicate logic.
- Codecs are values, not magic; pass them, return them, store them.
- Errors carry context; decoding failures include field path and expected type.
- No reflection, no struct tags for codec logic; all wiring is explicit in Go code.

## Package Structure and Responsibilities

| Package                        | Responsibility                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                          | Imports allowed from                                                       |
| ------------------------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------- |
| `codex`                        | PUBLIC API: `Codec[T]`, primitives (`Int`, `Int32`, `Int64`, `Uint`, `Uint64`, `Float32`, `Float64`, `String`, `Bool`, `Bytes` (raw `[]byte`, schema format `"binary"`), `Base64` (base64 `[]byte`, schema format `"byte"`), `Time`, `Date`, `Duration`, `HexColor` (`Codec[Color]` — `Color{R,G,B,A uint8}` straight/non-premultiplied RGBA, deliberately NOT `image/color.RGBA`; CSS Color Level 4 hex notation `#RGB`/`#RRGGBB`/`#RGBA`/`#RRGGBBAA`, case-insensitive decode, canonical lowercase 6/8-digit encode; `InvalidColorError{Value}` on malformed hex, `TypeMismatchError` on non-string input; schema `{type:string,pattern:...}` — no standard JSON Schema format keyword for color), `Any`, `Pure`, `Eq`, `Empty`), `Nullable[T]`, `SliceOf[T]`, `StringMap[V]`, `Map[K, V]`, `EntrySlice[K,V,R]` (decodes JSON/YAML/TOML object → `[]R` by merging decoded key K + value V; key codec handles prefix/validation; `split func(R)(K,V)` for encode; reuses `KeyError`; works with JSON, YAML quoted keys, TOML quoted headers — TOML bare-dotted keys produce nested objects per spec), struct, `TaggedUnion`, `UntaggedUnion`, `Either[A,B]`, `Either2`, `MapCodecSafe`, `MapCodecValidated`, `Must`, `Constraint`, `Refine`, `RefineFunc`, `ValidationError`, `ValidationErrors`, `ConstraintError`, `TypeMismatchError`, `ElementError`, `KeyError`, `UnknownVariantError`, `VariantError`, `EitherError`, `ErrMissingField`                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                               | `schema`                                                                   |
| `schema`                       | Schema model (pure data, no codec logic)                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                | none                                                                       |
| `validate`                     | Reusable `Constraint` functions: numbers, strings, format (`Email`, `UUID`, `URL`, `URI`, `Hostname`, `IPv4`, `IPv6`, `IP`, `Date`, `Time`, `DateTime`, `SemVer`, `Slug`, `CIDR`, **`ContainerImage`** — OCI image ref, `MQTTTopic`, `MQTTPublishTopic`, `HTTPPath`, `BearerToken`, `JWT`, `EnvVarName`, `EnvVarPrefix`, `IntString` variants), bytes (`MaxBytes`, `MinBytes`, `HasPrefix(prefix []byte)` — magic-byte check); binary file format constraints: `PNG`, `JPEG`, `GIF`, `WebP`, `PDF`, `ZIP` — predefined `Constraint[[]byte]` values (follow `Email`/`UUID` pattern, no Schema annotation, produce `ConstraintError`); env var name constraints: `EnvVarName` (POSIX format `[A-Z_][A-Z0-9_]*`, Schema.Pattern set), `EnvVarPrefix(prefix string)` (namespace prefix check, compose with `EnvVarName`) — use when names arrive from external input, not code literals                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                     | `codex`, `schema`                                                          |
| `format`                       | Bridges `Codec[T]` to wire formats: JSON, YAML, TOML, Gob (binary), Binary (raw bytes), streaming; `NewStreamed` for chunked/SSE writes; `Gob[T](codec)` uses `marshalTyped`/`unmarshalTyped` path (bypasses `map[string]any`), ContentType `"application/gob"`, constraints enforced on both marshal and unmarshal — suitable for Go-to-Go binary communication, not REST content negotiation; **Binary**: `Binary(c codex.Codec[[]byte]) Format[[]byte]` — identity marshal/unmarshal (raw bytes passthrough), validates via `c.Validate` on both paths, ContentType `"application/octet-stream"` (override with `WithContentType`); contrast: Binary writes raw bytes (files stay openable by external tools), Gob adds framing (not readable by external tools); use `NewTyped` directly for `T≠[]byte` typed binary (Protobuf, `image.Image`, write-only formats); **File I/O has moved to `ports`** — see the `ports` row (`ports.File[T]`, `ports.NewFile`, `ports.PatchEncoded`, file error types) — `format` retains only `Format.IsPatchable()`/`Codec()`/`UnmarshalRaw()`/`MarshalRaw()`/`PatchInto()` and the package-level `DeepMerge()` as the low-level primitives `ports.File.Patch`/`ports.PatchEncoded` are built on; **env var loading has moved to `config`** — see the `config` row (`config.FromEnv`, `config.FromEnvVar`, `config.EnvVarError`); **embedded format codecs**: `EmbeddedJSON[T](inner Codec[T]) Codec[T]`, `EmbeddedYAML[T](inner Codec[T]) Codec[T]`, `EmbeddedTOML[T](inner Codec[T]) Codec[T]` — wire type is a serialised string; decode: parse string → map[string]any → inner.Decode; encode: inner.Encode → marshal to string; typed errors: `EmbeddedDecodeError{Format, Err}` (format parse failure), `EmbeddedEncodeError{Format, Err}` (marshal failure) — both `errors.As`-navigable + `slog.LogValuer`; inner codec validation errors propagate unchanged; YAML int→`int`, TOML int→`int64` both handled by codex primitives | `codex`, `schema`, external libs                                           |
| `config`                       | Loads typed values from OS environment variables using schema-driven coercion — standalone, no `Pattern`/adapter family (config loading happens once at startup, before any pipeline exists). `FromEnv[T](c Codec[T], prefix string) (T, error)` — struct loading, naming convention `strings.ToUpper(prefix + field_name)`, nested struct prefix expansion (`APP_DB_HOST`) or JSON object, slice comma-separated or JSON array, `Nullable[T]` (absent=nil), `TaggedUnion`/slices-of-objects silently skipped; `FromEnvVar[T](key string, c Codec[T]) (T, error)` — single var, returns zero value when not set (not an error); `EnvVarError{Key, Err}` — wraps `codex.ValidationErrors`, implements `Unwrap()` + `slog.LogValuer` | `codex`, `schema` (stdlib only)                                             |
| `route`                        | HTTP route descriptors: `Route`, `Param`, `Body`, `Response`; security: `SecurityScheme` (spec-only, no codec), `SecuritySchemeType` constants (`SecuritySchemeAPIKey`, `SecuritySchemeHTTP`, `SecuritySchemeOAuth2`, `SecuritySchemeOpenIDConnect`), `OAuthFlow`, `OAuthFlows`, `SecurityRequirement` (`map[string][]string`); named constructors: `BearerScheme(format)`, `BasicScheme()`, `APIKeyScheme(name, in)`, `OAuth2Scheme(flows)`, `OpenIDConnectScheme(url)`; `Require(scheme, scopes...)` helper; `Route.Security []SecurityRequirement` (nil=inherit global, empty=no auth)                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                               | `schema`                                                                   |
| `render/internal/schemarender` | Shared schema-to-map rendering logic used by both OpenAPI and AsyncAPI renderers                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                        | `schema`                                                                   |
| `render/openapi`               | Renders `schema.Schema` as OpenAPI 3.1 `components/schemas`; `DocumentBuilder` for full spec                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                            | `schema`, `route`, `render/internal/schemarender`, external libs           |
| `render/asyncapi/v2`           | Renders channels and schemas as a full AsyncAPI 2.6 document (frozen)                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                   | `schema`, `render/internal/schemarender`, external libs                    |
| `render/asyncapi/v3`           | Renders channels and schemas as a full AsyncAPI 3.0 document; separate `channels` + `operations` top-level keys; per-operation `security`; `Server.Security`; `ChannelItem.Address`; `AddSecurityScheme(name, route.SecurityScheme)`; **request-reply**: `OperationReply{Channel string}` + `Operation.Reply *OperationReply` — set on send operations to emit `reply: {channel: {$ref: ...}}` in the spec; `DocumentBuilder.AddReplyChannel(name, ChannelItem)` — registers a channel as reply-only (bypasses the "must have subscribe or publish" validation; reply channels are referenced by `OperationReply.Channel`)                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                              | `schema`, `route`, `render/internal/schemarender`, external libs           |
| `forge`                        | Governed, signed KPI computation: `Measured[T]` boundary wrapper with provenance; `MeasuredCodec[T]` struct codec; `Function[In,Out]` generic derivation function with SHA-256 contract hash — single-input or struct-input (use `codex.Struct[T]` for multi-input); `NewFunction[In,Out](name, version string, input codex.Codec[In], output codex.Codec[Out], fn, opts...)` constructor — infallible (panics on empty name/version); port names for pipeline graph-edge inference are inferred from `codec.Schema.Title` (set via `.WithTitle("name")`); struct codec properties auto-expand to individual `PortSpec` entries; scalar codecs default to `"input"` / `"output"` when title is empty; `Compose[A,B,Out]` for type-safe chaining — infallible; `Registry` fluent builder (`NewRegistry(title, version string).WithDescription().WithAuthor().WithApproval(approvedBy, approvedAt).WithObserver()`) + `PipelineSpec` for graph inference; `PipelineInfo{Title, Version, Description, Author, ApprovedBy, ApprovedAt}` — pipeline-level governance mirrors `FunctionMeta` at the pipeline envelope; `render/pipeline` emits `author`/`approvedBy`/`approvedAt` under `info:` when set; `FunctionOpt` interface — primary option is `FunctionMeta{Description, Author, ApprovedBy, ApprovedAt string}` struct literal (matches `RouteMeta` / `ChannelMeta` pattern); pipeline-level cross-input refinement via `WithRefinement[In](func(In)error)` — preferred: cross-field constraints via `codex.RefineFunc` on the input struct codec surface as `InputError`; `WithRefinement` surfaces as `RefinementError`; Apply sequence: input codec validation → optional cross-input refinement → compute → output codec validation; typed errors: `InputError`, `OutputError`, `ApplyError`, `RefinementError{Function,Err}`; `FunctionKind` typed constants: `FunctionKindScalar`(`""`), `FunctionKindMap`, `FunctionKindFilter`, `FunctionKindReduce`, `FunctionKindMapValues`; `render/pipeline` omits `kind:` key for `FunctionKindScalar` (stored as empty string — `NewFunction`/`Compose` never write `Kind`, so scalar functions have `Kind==""` by default); `FunctionSpec.Kind FunctionKind`, `FunctionSpec.Inputs []PortSpec`, `FunctionSpec.Output PortSpec`; collection ops: `Map` (lift fn over slice), `Filter` (predicate over slice, element port name inferred from `elemCodec.Schema.Title`), `Reduce` (fold slice, elem+acc port names from codec titles), `MapValues` (lift fn over `map[string]_`, no key validation), `MapValuesK[K]` (lift fn over `map[K]_` with key codec validation — validates all keys atomically before any value is processed; invalid key → `InputError → KeyError → ConstraintError`; `Kind=FunctionKindMapValues`, `Wraps=innerFn.Spec.Name` in pipeline YAML)                                                                                                                                                                | `codex`, `schema`, `stats`, stdlib (`crypto/sha256`, `encoding/json`)      |
| `render/pipeline`              | Renders a `forge.PipelineSpec` as a `pipelineSpec` YAML document (mirrors `render/openapi` / `render/asyncapi`)                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                         | `forge`, `schema`, `render/internal/schemarender`, external libs           |
| `render/stream`                | Renders a `stream.TopologySpec` as a `streamTopology` YAML document; `Render(spec stream.TopologySpec) ([]byte, error)` — analogous to `render/pipeline.Render` for forge; each step emits `kind`, `name`, `description`; apply steps also emit `function`, `version`, `hash` (from `forge.FunctionSpec.Hash`) | `stream`, external libs (`gopkg.in/yaml.v3`)                               |
| `api/internal`                 | Shared helpers for `api/rest`, `api/events`, and `api/mcp` (template variable parsing and substitution); not part of the public API                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                     | `codex`                                                                    |
| `api/rest`                     | Transport-agnostic REST API builder; typed Decode/Encode + OpenAPI spec; `NewSSERoute` for Server-Sent Events; `SSERouteHandle` with `BuildPath` and `WithFormats` (renamed from `WithFormats`; mirrors `ChannelHandle.WithFormats`); `RouteHandle.WithRequestFormats` / `WithFormats` for multi-format request/response bodies; **`RequestFormats[Req](fmts...) RouteOpt`** / **`Formats[Resp](fmts...) RouteOpt`** — the `RouteOpt` equivalents, declarable inline in `NewRoute`'s variadic opts (and therefore in `ports.RESTPattern.Opts` with zero ports changes); type-erased `any` storage in `routeBuilder`, resolved generically in `Route.Register` — a type mismatch returns `FormatOptError{Direction "request"/"response", Err}`; `rest.SecurityScheme{route.SecurityScheme + Codec}` for credential format validation; `WithCodec(c codex.Codec[string]) SecurityScheme` to set codec without pointer boilerplate; **all param types** (`PathParam`, `QueryParam`, `CookieParam`, `HeaderParam`, `ResponseHeaderParam`, `ResponseCookieParam`) have `.WithCodec(c codex.Codec[string])` value-receiver method — use instead of `&codec` pointer; `Builder.AddSecurityScheme(name, rest.SecurityScheme)` / `AddGlobalSecurity(reqs...)`; `RouteMeta.Security []route.SecurityRequirement` (nil=inherit global, empty=explicitly no auth); `RouteHandle.SecuritySchemes map[string]rest.SecurityScheme`; `RouteHandle.GlobalSecurity []route.SecurityRequirement` (populated from `AddGlobalSecurity`); `SecurityCredentialError{Scheme, Err}` / `SecurityError{Err}` structured errors; **client-side**: `RouteHandle.EncodeRequest func(Req)([]byte,error)` (complement of `Decode`) + `RouteHandle.DecodeResponse func([]byte)(Resp,error)` (complement of `Encode`) — used by `adapters/nethttp.Call` for client-side encoding/decoding; `Route.ClientHandle() *RouteHandle[Req,Resp]` — returns a handle without registering with a `Builder` (no spec, no path codec validation); use for client-only scenarios where no OpenAPI spec is needed                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                     | `codex`, `format`, `route`, `render/openapi`, `schema`, `api/internal`     |
| `api/events`                   | Transport-agnostic event channel builder; typed Decode/Encode + AsyncAPI 3.0 spec; `ChannelHandle.WithFormats` sets default payload format for both directions; `WithSubscribeFormats` / `WithPublishFormats` for asymmetric channels (different format per direction); **`Formats[T](fmts...) ChannelOpt`** / **`SubscribeFormats[T](fmts...) ChannelOpt`** / **`PublishFormats[T](fmts...) ChannelOpt`** — the `ChannelOpt` equivalents, declarable inline in `NewChannel`'s variadic opts (and therefore in `ports.EventPattern.Opts` with zero ports changes); mismatch → `FormatOptError{Direction "both"/"subscribe"/"publish", Err}`; all update `message.contentType` in the AsyncAPI descriptor; `TopicParam` has `.WithCodec(c codex.Codec[string])` value-receiver — use instead of `&codec` pointer; `Builder.AddServer` stores servers in insertion order (uses `[]namedServer` slice, not map); description fallback: if `Server.Description` is empty, `AddServer` uses the name; `events.SecurityScheme{route.SecurityScheme + Codec}` for credential validation; `WithCodec(c codex.Codec[string]) SecurityScheme` to set codec without pointer boilerplate; `Builder.AddSecurityScheme`; `Builder.AddGlobalSecurity(reqs...)` (runtime enforcement only — AsyncAPI 3.0 has no document-level global security); `Subscribe.Security` / `Publish.Security` for per-operation requirements (nil=inherit global, empty=no auth); `ChannelHandle.SecuritySchemes` / `ChannelHandle.GlobalSecurity` used by adapters; **`Builder.AppendTo(db *asyncapi.DocumentBuilder) error`** — writes all registered channels into a caller-supplied `*asyncapi.DocumentBuilder` (servers/schemas NOT copied); enables combining pub/sub + request-reply channels in one `asyncapi.DocumentBuilder.Build()` call                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                            ; **`Builder.AddChannelItem(topic string, item asyncapi.ChannelItem) *Builder`** — registers a pre-built ChannelItem for channels the single-codec Channel cannot express (duplex sockets with different In/Out payload types; used by `ports.RegisterSocket`); builder topic codec NOT applied (topic may be an HTTP upgrade path); SchemaName refs still participate in dangling-$ref validation | `codex`, `format`, `render/asyncapi/v3`, `route`, `schema`, `api/internal` |
| `api/reqreply`                   | **Transport-agnostic** AsyncAPI 3.0 spec builder for request-reply patterns (formerly `api/zeromq`; works for ZMQ, MQTT 5, AMQP, or any transport — protocol is a server string); mirrors `rest.Route`/`events.Channel` pattern for async transports; `NewRoute[Req,Resp](topic, reqCodec, respCodec, ...RouteOpt) Route[Req,Resp]` — infallible constructor (topic = address, no HTTP method); `Route.Register(b *Builder) (*RouteHandle[Req,Resp], error)` — registers with builder, gets spec + handle; `Route.ClientHandle() *RouteHandle[Req,Resp]` — no builder required, no spec registration; returns handle with same codec helpers + topic params (mirrors `rest.Route.ClientHandle`); `RouteHandle{Topic, Decode, Encode, EncodeRequest, DecodeResponse, RequestFormats, Formats}` + `WithRequestFormats`/`WithFormats` + **`RequestFormats[Req](fmts...) RouteOpt`**/**`Formats[Resp](fmts...) RouteOpt`** (inline `RouteOpt` equivalents, mismatch → `FormatOptError{Direction,Err}`, same shape as `api/rest`) + `BuildTopic(vars)(string,error)` + `ValidateTopicVars(vars)error` (mirrors `events.ChannelHandle`); `TopicParam{Name,Description,Codec *Codec[string]}` + `.WithCodec(c)` implements `RouteOpt` (parallel to `events.TopicParam`); `RouteParamError{Name,Value,Err}` + `MissingRouteParamError{Name}` — both `errors.As`-navigable + `slog.LogValuer` (parallel to `TopicParamError`/`MissingTopicVarError`); `DuplicateRouteError{Topic}` — `slog.LogValuer`; `RouteOpt` sealed interface; `RouteMeta{OperationID, Summary, Description, Tags, ReqSchemaName, RespSchemaName}` implements `RouteOpt`; `Builder.AsyncAPISpec() (asyncapi.Document, error)` — emits AsyncAPI 3.0 (protocol determined by AddServer); `Info = asyncapi.Info` alias; `Server = asyncapi.Server` alias; `Builder.AddServer(name, Server)` — chains; no `api/rest` import; `Info = asyncapi.Info` alias; `Server = asyncapi.Server` alias; `Builder.AddServer(name, Server)` — chains; **import rule**: `api/reqreply` does NOT import `adapters/zeromq` (API layer must not depend on adapter layer); **`Builder.AppendTo(db *asyncapi.DocumentBuilder) error`** — writes all request-reply channels into a caller-supplied `*asyncapi.DocumentBuilder`; enables combining with `api/events` channels in one spec via `asyncapi.DocumentBuilder.Build()`; `ErrorPattern[E,B](codec, mapFn...) ErrorPatternOpt[E,B]` implements `RouteOpt` — codec-first, runtime-wired error reply (mirrors `rest.ErrorPattern`/`events.ErrorChannel`; direct mode when no mapFn, mapped mode when provided; `errors.As` match, first-declared-wins precedence); `.WithCode`/`.WithDescription`/`.WithSchemaName`/`.WithChannelAddress`/`.WithOperationID` fluent setters customize the auto-generated AsyncAPI reply-error channel/operation (defaults: Code derived from `%T` type name, address `<topic>/reply/error/<code>`); `RouteHandle.ErrorResponseFor(err) (ErrorPatternResponse, bool, error)` is the lookup accessor `adapters/mqtt5.Serve` and `adapters/zeromq.Serve`/`ServeRouter` consult on handler/encode failure (NOT decode failure — no business error exists yet) before falling back to plain-text `err.Error()`; `ErrorPattern` ALSO pushes an equivalent `ErrorReplyMeta` entry so the AsyncAPI spec entry is produced automatically — `ErrorReplyMeta` itself remains spec-only/no-runtime-dispatch for documenting error replies produced by other means                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                      | `api/rest`, `render/asyncapi/v3`, `schema`                                 |
| `api/mcp`                      | Transport-agnostic MCP server builder; Tools, Resources, and Prompts follow the same declare → register → handle pattern as `api/rest` and `api/events`; `NewTool[In,Out](name, inputCodec, outputCodec, opts...)`, `NewResource[T](uriTemplate, codec, opts...)`, `NewPrompt(name, opts...)` — all return declarative value types; `Tool.Register(b)` → `ToolHandle[In,Out]` with `Decode(any)(In,error)` + `Encode(Out)([]byte,error)` function fields + `InputSchema`/`OutputSchema json.RawMessage`; `ResourceHandle[T]` has `Encode` (function field) + `BuildURI(vars)(string,error)` + `ValidateURIVars(vars)error` as proper methods (stores `uriParams []ResourceParam` internally; uses `api/internal.BuildFromTemplate`); `PromptHandle` has `ValidateArgs(args)(error)` as a method + `Args []PromptArg`; `ValidateArgs` behavior: absent key → `MissingPromptArgError` for required args; present-but-empty value → codec is called (not silently skipped); `Builder.MCPSpec()` returns `*MCPSpec{Name,Version,Tools,Resources,Prompts}` (static JSON doc analogous to OpenAPI/AsyncAPI); `ToolMeta{Description,Tags}`, `ResourceMeta{Name,Description,MimeType,Tags}`, `PromptMeta{Description,Tags}` implement opt interfaces (mirrors `RouteMeta`/`ChannelMeta`/`FunctionMeta`); `ResourceParam{Name,Description,Codec}.WithCodec(c)` and `PromptArg{Name,Description,Required,Codec}.WithCodec(c)` mirror `TopicParam.WithCodec`; typed errors: `ToolInputError{Name,Err}`, `ToolOutputError{Name,Err}`, `ResourceEncodeError{URI,Err}`, `ResourceParamError{Name,Value,Err}`, `MissingResourceVarError{Name}`, `InvalidResourceParamError{Name,URITemplate}`, `PromptArgError{Name,Err}`, `MissingPromptArgError{Name}` — all `errors.As`-navigable + `slog.LogValuer`; `ErrorPattern[E,B](codec, mapFn...) ErrorPatternOpt[E,B]` implements `ToolOpt` — codec-first typed error result for a matched HANDLER (business-logic) error only, mirroring `rest.ErrorPattern`/`events.ErrorChannel` (direct mode when no mapFn, mapped mode when provided; `errors.As` match, first-declared-wins precedence; no status/topic concept — MCP tool results aren't HTTP or pub/sub); does NOT apply to input-decode or output-encode errors (different concerns); `ToolHandle.ErrorResponseFor(err) (ErrorPatternResponse, bool, error)` is the lookup accessor                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                   | `codex`, `schema`, `render/jsonschema`, `api/internal`                     |
| `adapters/mcpgo`               | mark3labs/mcp-go adapter: `ToolHandler[In,Out](handle, fn, opts) (mcp.Tool, server.ToolHandlerFunc)` — builds mcp.Tool with `RawInputSchema` from codec + wraps fn; input validation errors → `mcp.NewToolResultError` (IsError=true, LLM sees error text); output encode error → protocol error (nil result, non-nil error — server contract violation); handler (business-logic) error consults `handle.ErrorResponseFor(err)` (a declared `apimcp.ErrorPattern`) first — matched → `mcp.NewToolResultStructured(json.RawMessage(body), string(body))` with `IsError: true` (structured typed content, still an error to the LLM); unmatched → falls back to plain-text `mcp.NewToolResultError` unchanged; `RegisterTool`, `RegisterResource`, `RegisterPrompt` call the respective Handler function and register with `*server.MCPServer`; `ResourceHandler[T]` detects URI template placeholders and uses `AddResourceTemplate` vs `AddResource` automatically; `PromptHandler` builds mcp.Prompt with `PromptArgument` list; `HandlerFunc[In,Out]`, `ResourceHandlerFunc[T]`, `PromptHandlerFunc`, `PromptMessage{Role,Content}`; `Options{Observer stats.Observer}` — uses `RecordRequest("tool"/"resource"/"prompt", name, statusCode, duration)` + `stats.ReportErrors(obs,"input",err)` for codec validation metrics; Observer also type-asserts to `stats.TraceObserver` for distributed tracing spans — **stream bridges** (stream.go): `ToolLatestHandler[In,Out](handle, src Stream[Out], opts) (mcp.Tool, server.ToolHandlerFunc)` — reactive cache: background goroutine stores latest value; tool call returns latest or `mcp.NewToolResultError("no value computed yet")`; `RegisterToolLatest` convenience wrapper; `ToolPipelineHandlerFunc[In,Out]` = `func(ctx, In) Stream[Out]`; `ToolPipelineHandler[In,Out](handle, fn, opts)` — reactive trigger: each tool call starts a fresh pipeline run (MCP equivalent of `nethttp.PipelineHandler`); errors → `mcp.NewToolResultError`; `RegisterToolPipeline` convenience wrapper — **port adapters** (binding.go): `ToolPipelineAdapter[In,Out](server *server.MCPServer, handle *apimcp.ToolHandle[In,Out], opts Options) ports.ToolAdapter[In,Out]` — wraps `RegisterToolPipeline`; each tool call runs a fresh pipeline; `LatestAdapter[Out](server, handle *apimcp.ToolHandle[struct{},Out], opts) ports.LatestAdapter[Out]` — serves a ports.LatestPort's cache cell as an MCP tool (successor to the removed ToolLatestAdapter; no ignored pipeline argument) | `api/mcp`, `stats`, `github.com/mark3labs/mcp-go`, `ports`, `stream`                |
| `render/jsonschema`            | Renders `schema.Schema` to plain JSON Schema `json.RawMessage`; `Schema(s schema.Schema) (json.RawMessage, error)` — returns nil for zero-value schema; used by `api/mcp` to convert codec schemas to MCP tool input/output schemas; wraps `render/internal/schemarender.SchemaObject` + `json.Marshal`                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                 | `schema`, `render/internal/schemarender`                                   |
| `adapters/nethttp`             | net/http adapter — **server**: `Handler`, `Register`, `SSEHandler`, `RegisterSSE`, `SSEHandlerFunc`, `RequestFromContext`, `WithResponseHeaders`, `ResponseHeadersFromContext`, `WithResponseCookies`, `ResponseCookiesFromContext`, `PendingCookie`, `SetCookie`, `CookieOptions`, `Options` (with `Observer stats.Observer`, `SecurityFunc func(ctx, *http.Request, []route.SecurityRequirement) error`); security enforcement: nil per-route Security falls back to `RouteHandle.GlobalSecurity`; `len(secReqs) > 0` gate (empty=no auth); credential extraction + codec validation + SecurityFunc; type-asserts `stats.SecurityObserver` for rejection metrics; request format negotiation via `RouteHandle.WithRequestFormats` — **client**: `Call[Req,Resp](ctx, *http.Client, baseURL, handle, req, vars map[string]string, CallOptions) (Resp, error)` — executes a typed HTTP request; validates all params via registered codecs before sending (path via `BuildPath`, query via `ValidateQuery`, cookies via `ValidateCookies`, headers via `ValidateHeaders`); `CallOptions{QueryParams, CookieParams, HeaderParams, ExtraHeaders, CredentialFunc func(ctx, []route.SecurityRequirement)(http.Header,error), Observer stats.Observer}`; `Observer.RecordRequest` called on every code path (status 0 = pre-flight validation failure, no HTTP call sent); typed client errors: `UnexpectedStatusError{Method,Path,StatusCode,Body}` (non-2xx), `RequestBuildError{Err}` (URL/context error), `RequestError{Method,Path,Err}` (network/transport failure), `ResponseBodyError{Err}` (body read failure) — all `errors.As`-navigable + `slog.LogValuer`; use `Route.ClientHandle()` (see `api/rest`) for client-only scenario without a `Builder` — **server stream handlers** (stream.go): `HandlerLatest[Req,Resp](handle, src Stream[Resp], opts) http.Handler` (reactive cache; 503+`NoLatestValueError` before first value); `RegisterLatest`; `PipelineHandlerFunc[Req,Resp]`; `PipelineHandler[Req,Resp](handle, fn, opts) http.Handler`; `RegisterPipeline`; `SSEFromHub[Req,Event](hub *stream.BroadcastHub[Event], SSEStreamOptions) SSEHandlerFunc[Req,Event]`; `SSEStreamOptions{Topic,OnError,Observer}`; bridge errors: `NoLatestValueError{Path}`, `PipelineFullError{Path,Capacity}`, `PipelineNoResponseError{Path}`, `SSEWriteError{Path,Err}` — all `slog.LogValuer`; **port adapters** (binding.go): `IngestAdapter[T](mux,handle *RouteHandle[T,struct{}],IngestAdapterOptions{Options,Buffer}) ports.SourceAdapter[T]`; `SSEAdapter[Event](mux,handle *SSERouteHandle[struct{},Event],SSEAdapterOptions) ports.SinkAdapter[Event]`; `CallAdapter[Req,Resp](client,baseURL,handle,CallStreamOptions{Vars,CallOpts,Buffer}) ports.IOAdapter[Req,Resp]`; `PollAdapter[Req,Resp](client,baseURL,handle,req,interval,PollStreamOptions{Vars,Observer,Buffer}) ports.SourceAdapter[Resp]`; `DrainCallAdapter[Req,Resp](client,baseURL,handle,DrainCallOptions{Vars,OnError,CallOpts}) ports.SinkAdapter[Req]`; `PipelineAdapter[Req,Resp](mux,handle,PipelineAdapterOptions{Options}) ports.ToolAdapter[Req,Resp]` — registers pipeline as HTTP endpoint; `LatestAdapter[Resp](mux,handle *RouteHandle[struct{},Resp],Options) ports.LatestAdapter[Resp]` — serves a ports.LatestPort's cache cell as a GET endpoint (503+`NoLatestValueError` before first value; port owns the cell, unlike HandlerLatest); NOTE: SSEHandler commits response headers on first `send` call, not at connection time | `api/rest`, `net/http`, `route`, `stats`, `format`, `ports`, `stream`               |
| `adapters/chi`                 | chi adapter: same API surface as `adapters/nethttp` plus `SSEHandler`, `RegisterSSE`, `SSEHandlerFunc`; path vars via `chi.URLParam`; `Handler`, `Register`, `RequestFromContext`, `WithResponseHeaders`, `WithResponseCookies`, `PendingCookie`, `SetCookie`, `CookieOptions`, `Options` (with `SecurityFunc`); global security fallback + empty=no-auth semantics same as nethttp; request format negotiation via `RouteHandle.WithRequestFormats` — **stream bridges** (stream.go): same `HandlerLatest`, `HandlerIngest`, `PipelineHandlerFunc`, `PipelineHandler`, `RegisterLatest`, `RegisterIngest`, `RegisterPipeline` as nethttp but return `http.HandlerFunc` (chi convention); `SSEFromStream[Req,Event]`, `SSEFromHub[Req,Event]`, `SSEStreamOptions` — same semantics as nethttp; **port adapters** (binding.go): `IngestAdapter`/`SSEAdapter`/`PipelineAdapter`/`LatestAdapter[Resp](r gochi.Router, handle *RouteHandle[struct{},Resp], Options) ports.LatestAdapter[Resp]` — mirror nethttp; NOTE chi's Mux is NOT safe for route registration concurrent with serving (no internal lock, unlike net/http.ServeMux), so chi port adapters register a `swapHandler` indirection at CONSTRUCTOR time (caller's goroutine, before the server starts) and atomically install the real handler from Activate/Serve — requests before installation get 503; bridge errors: `NoLatestValueError{Path}`, `PipelineFullError{Path,Capacity}`, `PipelineNoResponseError{Path}`, `SSEWriteError{Path,Err}` | `api/rest`, `net/http`, `route`, `stats`, `format`, chi lib, `stream`      |
| `adapters/mqtt`                | Paho MQTT adapter: `SubscribeHandler` (uses `handle.Formats` as default; call-time `formats ...format.Format[T]` variadic overrides), `Publish` (same priority chain), `TopicVarsFromMessage`, `TopicMismatchError`, `SubscribeError`, `ErrorKind` (KindDecode/KindHandler/KindSecurity), `PublishEncodeError{Topic,Err}` (returned by `Publish` on payload encode failure — `errors.As`-navigable + `slog.LogValuer`); `SubscribeError{Kind,Topic,Err}` and `TopicMismatchError{Template,Topic}` also implement `slog.LogValuer`; `SubscribeOptions` (with `Observer stats.Observer`, `SecurityFunc func(ctx, pahomqtt.Message, []route.SecurityRequirement) error`), `PublishOptions` (with `Observer stats.Observer`); nil per-channel Subscribe.Security falls back to `ChannelHandle.GlobalSecurity`; `len(secReqs) > 0` gate (empty=no auth) — **port adapters** (binding.go): `SubscribeAdapter[T](client, handle, qos, fmt, SubscribeAdapterOptions{TopicFilter,SecurityFunc,Observer}) ports.SourceAdapter[T]`; `PublishAdapter[T](client, handle, fmt, MQTTDrainPublishOptions) ports.SinkAdapter[T]`; `MQTTDrainPublishOptions{QoS,Retained,Vars,OnError,Observer}`; `SubscribeAdapterOptions{TopicFilter string, SecurityFunc, Observer}` — use with `ports.SourcePort.Bind` and `ports.SinkPort.Bind`; **error-path ergonomics**: `PublishAdapter.Activate` consults `handle.ErrorResponseFor(err)` (a declared `events.ErrorChannel`) for every upstream stream error before falling back to `MQTTDrainPublishOptions.OnError` — matched `respond` publishes the typed payload to the declared topic; matched `handle`/`log` or unmatched falls through to `OnError` unchanged (mirrors `adapters/mqtt5.PublishAdapter`) | `api/events`, `format`, `route`, `stats`, `ports`, Paho MQTT lib, `stream`          |
| `adapters/mqtt5`               | MQTT 5.0 adapter using `github.com/eclipse/paho.golang` (pure Go); **PUB/SUB** via `api/events`: `Subscribe[T](ctx, MQTTClient, MQTTRouter, *ChannelHandle[T], qos byte, fn, SubscribeOptions, ...Format[T]) error` (registers with broker + router), `Publish[T](ctx, MQTTClient, *ChannelHandle[T], qos, retain, msg, vars, PublishOptions, ...Format[T]) error`; **REQUEST-REPLY** via `api/reqreply`: `Serve[Req,Resp](ctx, MQTTClient, MQTTRouter, *reqreply.RouteHandle[Req,Resp], fn, ServeOptions) error` (subscribes + async handler; reads ResponseTopic+CorrelationData from msg.Properties; replies to ResponseTopic), `Call[Req,Resp](ctx, MQTTClient, MQTTRouter, *reqreply.RouteHandle[Req,Resp], req, CallOptions) (Resp, error)` (subscribes to per-call reply topic `prefix/<uuid>`, publishes with ResponseTopic+CorrelationData, waits for matching reply); **MQTT 5 features**: User Properties exposed in SecurityFunc (func(ctx, *paho.Publish, reqs)) + `UserPropertiesFromContext(ctx)` helper; ContentType auto-format-selection: Subscribe checks msg.Properties.ContentType against format.ContentType() in formats slice; Publish sets ContentType + UserProperties via PublishOptions; `MQTTClient` interface (`Publish`, `Subscribe`, `Unsubscribe`) satisfied by `*paho.Client`; `MQTTRouter` interface (`RegisterHandler`, `UnregisterHandler`) satisfied by `*paho.StandardRouter`; typed errors: `SubscribeError{Kind,Topic,Err}`, `PublishEncodeError{Topic,Err}`, `CallError{Kind,Err}`, `ServeError{Kind,Err}`, `UserPropertyError{Name,Value,Err}`, `MissingUserPropertyError{Name}`, `BrokerError{Op,Err}` (broker transport failures — Op: "subscribe", "publish") — all `errors.As`-navigable + `slog.LogValuer`; `ErrorKind`: `KindDecode`, `KindHandler`, `KindEncode`, `KindTimeout` (for Call no-reply within deadline); `RecordRequest("MQTT5-REP"/"MQTT5-REQ", path, status, dur)` for request-reply; trace ops: `"mqtt5.subscribe"`, `"mqtt5.publish"`, `"mqtt5.serve"`, `"mqtt5.request"`; `CallOptions.Vars map[string]string` — when non-nil, calls `handle.BuildTopic(vars)` before publishing; reports `RecordValidationError("topic_var", ...)` on validation failure; returns `CallError` wrapping `reqreply.RouteParamError` or `reqreply.MissingRouteParamError`; **reply topic customisation**: `type ReplyTopicBuilder func() (responseTopic, subscribeFilter string)` — in `CallOptions.ReplyTopicBuilder`; overrides `ReplyTopicPrefix` when non-nil; empty `responseTopic` → `CallError{KindEncode}`; empty `subscribeFilter` falls back to `responseTopic`; built-in constructors: `UUIDReplyTopic(prefix string) ReplyTopicBuilder` (default behaviour — equal topic+filter, empty prefix → "replies"), `SharedReplyTopic(prefix, group string) ReplyTopicBuilder` (filter = `"$share/<group>/<prefix>/<uuid>"`, responseTopic = `"<prefix>/<uuid>"`); `UserPropertyParam{Name, Description, Required, Codec *codex.Codec[string]}` + `.WithCodec(c)` (mirrors `rest.HeaderParam`); validated before SecurityFunc in Subscribe and Serve — missing required → `MissingUserPropertyError{Name}` wrapped in `SubscribeError{KindSecurity}`, codec fail → `UserPropertyError{Name,Value,Err}` wrapped in `SubscribeError{KindSecurity}`; `RecordValidationError("user_property", constraint, name)` called; for request-reply AsyncAPI spec use `reqreply.NewRoute(...).Register(builder)` with `Protocol: "mqtt5"` — **port adapters** (binding.go): `SubscribeAdapter[T](client,router,handle,qos,fmt,SubscribeAdapterOptions{TopicFilter,UserPropertyParams,SecurityFunc,Observer}) ports.SourceAdapter[T]`; `PublishAdapter[T](client,handle,fmt,MQTT5DrainPublishOptions) ports.SinkAdapter[T]`; `CallAdapter[Req,Resp](client,router,handle,CallOptions) ports.IOAdapter[Req,Resp]`; `MQTT5DrainPublishOptions{QoS,Retained,Vars,OnError,Observer}`; **stream handlers** (stream.go): `AsPipelineFunc[Req,Resp](fn) func(ctx,Req)(Resp,error)` — wraps a pipeline handler for use with `Serve`; `PipelineNoResponseError{Topic}`; **error-path ergonomics**: `Serve` consults `handle.ErrorResponseFor(err)` (a declared `reqreply.ErrorPattern`) on handler/encode failure (NOT decode failure) before falling back to plain-text `err.Error()` — matched pattern sends the typed codec-backed payload as the reply body instead | `api/events`, `api/rest`, `format`, `route`, `stats`, `ports`, `github.com/eclipse/paho.golang`, `stream` |
| `adapters/zeromq`              | ZeroMQ adapter — transport-agnostic via `FramedSocket` interface (no CGO in the adapter itself; wrap `pebbe/zmq4` in your application); **PUB/SUB + PUSH/PULL** via `api/events`: `Subscribe[T](ctx, FramedSocket, *ChannelHandle[T], fn, SubscribeOptions, ...Format[T]) error` (blocking loop, run in goroutine), `Publish[T](ctx, FramedSocket, *ChannelHandle[T], msg, vars, PublishOptions, ...Format[T]) error`; **REQ/REP** via `api/rest`: `Serve[Req,Resp](ctx, FramedSocket, *RouteHandle[Req,Resp], fn, ServeOptions) error` (blocking REP loop), `Call[Req,Resp](ctx, FramedSocket, *RouteHandle[Req,Resp], req, CallOptions) (Resp, error)`; **DEALER/ROUTER**: `ServeRouter[Req,Resp](ctx, FramedSocket, *RouteHandle[Req,Resp], fn, ServeOptions) error` (concurrent; spawns goroutine per request; waits for drain on ctx cancel), `CallDealer[Req,Resp](ctx, FramedSocket, *RouteHandle[Req,Resp], req, CallOptions) (Resp, error)` (concurrent-safe per call); DEALER/ROUTER frame layout: ROUTER recv `[identity,"",payload]`, send `[identity,"","ok"/"error",resp]`; DEALER send `["",payload]`, recv `["","ok"/"error",resp]`; `RecordRequest("ZMQ-ROUTER"/"ZMQ-DEALER", path, statusCode, duration)` for DEALER/ROUTER; message framing: PUB/SUB/PUSH/PULL `[topic_bytes, payload_bytes]`; REQ/REP request `[payload]`, reply `["ok","payload"]` or `["error","message"]`; typed errors: `SubscribeError{Kind, Topic, Err}`, `PublishEncodeError{Topic, Err}`, `ServeError{Kind, Err}`, `CallError{Err}`, `SocketError{Op, Err}` (socket infrastructure failures; Op values: `"set_subscription"`, `"set_recv_timeout"`, `"recv"`, `"send"`) — all `errors.As`-navigable + `slog.LogValuer`; `ErrorKind`: `KindDecode`, `KindHandler`, `KindEncode`; all options structs accept `Observer stats.Observer`; `CallOptions.Vars map[string]string` — when non-nil, resolves template topic vars via `handle.BuildTopic(vars)` before encoding; used for observer path accuracy and codec validation (ZMQ routing is socket-based, not topic-based); validation failure returns `CallError` wrapping `reqreply.RouteParamError`/`reqreply.MissingRouteParamError`; `TraceObserver` type-asserted for spans: `"zmq.subscribe"`, `"zmq.publish"`, `"zmq.serve"`, `"zmq.request"`; `ErrTimeout` sentinel for polling (100 ms poll interval); for REQ/REP AsyncAPI spec use `reqreply.NewRoute(...).Register(builder)` with `api/reqreply.Builder` — **port adapters** (binding.go): `SubscribeAdapter[T](sock,handle,fmt,SubscribeAdapterOptions{Buffer}) ports.SourceAdapter[T]`; `PublishAdapter[T](sock,handle,fmt,DrainPublishOptions{Vars,OnError,Observer}) ports.SinkAdapter[T]` — **error-path ergonomics**: `Activate` consults `handle.ErrorResponseFor(err)` (a declared `events.ErrorChannel`) for every upstream stream error before falling back to `OnError` — matched `respond` sends the typed payload as `[topic, payload]` frames to the declared topic; matched `handle`/`log` or unmatched falls through to `OnError` unchanged (mirrors `adapters/mqtt5.PublishAdapter`); `CallAdapter[Req,Resp](sock,handle,CallStreamOptions{Vars,Observer,Buffer}) ports.IOAdapter[Req,Resp]`; `ServeAdapter[Req,Resp](sock,handle,ServeOptions) ports.ToolAdapter[Req,Resp]` — starts Serve loop in background goroutine; **stream handlers** (stream.go): `AsPipelineFunc[Req,Resp](fn) func(ctx,Req)(Resp,error)` — wraps pipeline handler for use with `Serve`/`ServeRouter`; `ServeLatest[Req,Resp](ctx, sock, handle, src Stream[Resp], ServeLatestOptions) error` — reactive cache; `LatestAdapter[Resp](sock, handle *reqreply.RouteHandle[struct{},Resp], ServeLatestOptions) ports.LatestAdapter[Resp]` — serves a ports.LatestPort's cache cell over a blocking REP loop (port owns the cell); errors: `ServeLatestError{Op,Err}`, `NoLatestValueError{Topic}`, `CorrelationError{Seq,Err}`, `PipelineNoResponseError{Topic}`; **error-path ergonomics**: `Serve`/`ServeRouter` consult `handle.ErrorResponseFor(err)` (a declared `reqreply.ErrorPattern`) on handler/encode failure (NOT decode failure) before falling back to plain-text `err.Error()` — matched pattern sends the typed codec-backed payload instead (ROUTER variant preserves identity framing) | `api/events`, `api/rest`, `format`, `stats`, `ports`, `stream` (no CGO dependency)  |
| `adapters/sql`                 | SQL adapter combining goose migrations + codec validation; **`Validate[T](c, v, opts) (T, error)`** — encode→decode round trip applying all Refine/RefineFunc constraints; returns normalized T; wraps failures in `RowValidationError{Table,Op,Err}`; calls `stats.SQLObserver.RecordValidation` + `stats.Observer.RecordValidationError("sql_row", ...)` per field; **`DecorateInput[Req](fn func(ctx,Req)error, codec, ValidateOptions) func(ctx,Req)error`** / **`DecorateOutput[Req,Resp](fn func(ctx,Req)(Resp,error), codec, ValidateOptions) func(ctx,Req)(Resp,error)`** — wrap an sqlc-generated function ONCE, return a drop-in replacement with automatic codec validation (declare-once counterpart to calling `Validate` by hand around every sqlc call; reuses `ValidateOptions`, delegates to `Validate`; unlike bare `Validate`, the returned closures resolve `stats.ObserverFromContext(ctx)` when `Observer` is nil since they wrap ctx-taking functions); `DecorateInput` never calls fn on invalid input, `DecorateOutput` passes fn's own error through unchanged and only validates on success; **`Migrator`** — wraps `pressly/goose/v3`; `NewMigrator(db, migrations fs.FS, dir string, dialect string) (*Migrator, error)`; `Up(ctx, MigrateOptions) error`, `Down(ctx, MigrateOptions) error`, `Status(ctx) ([]MigrationStatus, error)`; calls `stats.SQLObserver.RecordMigration` per applied/rolled-back file; **`MigrationStatus{Version int64, Name string, AppliedAt time.Time}`**; **errors**: `RowValidationError{Table, Op, Err}` + `MigrationError{Op, Version, Err}` — both implement `Error()`, `Unwrap()`, `slog.LogValuer`; **`ValidateOptions{Table, Op string, Observer stats.Observer}`**; **`MigrateOptions{Observer stats.Observer}`**; does NOT generate SQL, scan rows, or build queries — those are sqlc's job; `Validate` is the only boundary between go-codex and the DB row — **stream bridges** (stream.go): `QueryStream[T](ctx, codec, queryFn, interval, QueryStreamOptions) Stream[T]` (polls queryFn at interval, validates each row with codec; database errors → `QueryStreamError{Table,Op,Err}`; validation errors → `RowValidationError`); `QueryEachStream[In,T](ctx, codec, src Stream[In], queryFn func(ctx,In)([]T,error), QueryEachStreamOptions) Stream[T]` (per-item parameterized lookup: calls queryFn for each stream item, validates each result row; `QueryEachStreamOptions{Table,Op,Observer,Buffer}`; same error types as QueryStream); `DrainInsert[T](ctx, codec, src, insertFn, DrainInsertOptions)` (validates each item before insert; validation → `RowValidationError`; insert errors → `InsertStreamError{Table,Op,Err}`) — new errors `QueryStreamError` + `InsertStreamError` both implement `slog.LogValuer` + `Unwrap()` | `codex`, `stats`, `database/sql`, `github.com/pressly/goose/v3`, `stream`  |
| `adapters/file`                | Stdlib-only file adapter — no external dependencies; **port adapters** (binding.go): **sources** `ScanAdapter[T](path, fmt, ScanAdapterOptions{Observer,Buffer}) ports.SourceAdapter[T]` (reads file line-by-line), `WatchAdapter(dir, interval, WatchAdapterOptions) ports.SourceAdapter[string]` (polls dir; emits new file paths); **intermediate** `ReadEachAdapter[In,T,Resp](f File[T], varsFor func(In)map[string]string, combine func(In,T)Resp, ReadEachAdapterOptions{Observer,FileOptions,Buffer}) ports.IOAdapter[In,Resp]` (per-item file read with path template vars); **sinks** `DrainWriteAdapter[T](w io.Writer, fmt, DrainWriteAdapterOptions{Path,Separator,Observer,OnError}) ports.SinkAdapter[T]` (encodes each item as a line; default separator `"\n"`; NDJSON), `DrainWriteFileAdapter[T](f File[T], varsFor func(T)map[string]string, DrainWriteFileAdapterOptions{Observer,FileOptions,OnError}) ports.SinkAdapter[T]` (whole-file overwrite per item), `DrainPatchAdapter[T](f File[T], varsFor func(map[string]any)map[string]string, DrainPatchAdapterOptions{Observer,FileOptions,OnError}) ports.SinkAdapter[map[string]any]` (untyped partial update per item via `ports.File.Patch` — JSON Merge Patch semantics; map-based formats only, `FilePatchNotSupportedError` passed through unwrapped for Gob/Binary/NewTyped/NewStreamed), `DrainPatchEncodedAdapter[T,P](f File[T], patchCodec codex.Codec[P], varsFor func(P)map[string]string, DrainPatchEncodedAdapterOptions{Observer,FileOptions,OnError}) ports.SinkAdapter[P]` (typed partial update per item via `ports.PatchEncoded` — patchCodec-only fields still persisted; both patch adapters stay handle-first, same as `ReadEachAdapter`, since the patch item type deliberately differs from f's own type T); **errors**: `ScanError{Path,Err}`, `WatchError{Dir,Err}`, `WriteError{Path,Err}`, `ReadError{Err}` — all `slog.LogValuer` + `Unwrap()` | `format`, `ports`, `stream`, stdlib only                                             |
| `adapters/redis`               | Typed cache adapter for any Redis-protocol server (Redis, Valkey, KeyDB, …) via `github.com/redis/go-redis/v9` behind a **narrow client interface**: `Commands{Get(ctx,key)([]byte,error) /*ErrCacheMiss on missing key*/, Set(ctx,key,value,ttl) error, Del(ctx,keys...) error}` — constructors accept `Commands`, never a concrete client; `NewCommands(goredis.UniversalClient) Commands` is the ONLY go-redis touchpoint (maps `redis.Nil` → `ErrCacheMiss`); unit tests + example use a hand-written in-memory fake (no miniredis, no live server); **standalone plain functions** (the non-pipeline entrypoint — no `ports.IOAdapter`, no `stream.Stream` — mirrors `sql.Validate`/`ports.File.Read`/`.Write`): `Get[T](ctx, client, cache ports.Cache[T], vars map[string]string, GetOptions{MissIsError,Observer}) (T, bool, error)` and `Set[T](ctx, client, cache ports.Cache[T], vars map[string]string, v T, SetOptions{TTL,Observer}) error`; **port adapters** (binding.go) DELEGATE to them per item: `GetAdapter[Req,Resp](client, cache ports.Cache[Resp], keyFn func(Req)map[string]string, GetAdapterOptions{MissIsError,Buffer,Observer}) ports.IOAdapter[Req,Resp]` (read-through: key from `cache.BuildKey(keyFn(req))`, hit → decoded+codec-validated Resp; miss → item SKIPPED by default (0..N contract) or `CacheError` wrapping `ErrCacheMiss` when `MissIsError`; decode failures → `CacheError` + per-field `stats.ReportErrors(obs, "payload", err)`), `SetAdapter[T](client, cache ports.Cache[T], keyFn, SetAdapterOptions{TTL /*overrides cache.TTL when non-zero*/,Buffer,OnError,Observer}) ports.IOAdapter[T,T]` (write-through transform: writes each item, item ALWAYS passes through unchanged — a cache write failure goes to Stream.Errors but never drops pipeline data), `DrainSetAdapter[T](client, cache, keyFn, SetAdapterOptions) ports.SinkAdapter[T]` (terminal write-through; errors → `OnError`, dropped when nil), `Seed[T](ctx, client, cache ports.Cache[T], SeedOptions{Observer}) (T, bool, error)` (thin wrapper around `Get` with nil vars; warm-restart read of a var-free key — `(zero,false,nil)` on miss, an empty cache is NOT an error; durable LatestPort = SetAdapter on the feeding stream + Seed-and-Merge after restart — `ports.LatestAdapter.Serve` is read-only so there is deliberately NO redis LatestAdapter); **errors**: `ErrCacheMiss` sentinel (survives wrapping — test with `errors.Is`) + `CacheError{Key /*expanded, e.g. "user:42"*/, Op /*"get"/"set"/"del"*/, Err}` — `Error()`, `Unwrap()`, `slog.LogValuer`; **observer**: `stats.CacheObserver` type-asserted (RecordCacheHit/RecordCacheMiss per lookup, RecordCacheWrite(key, op, success, dur) per write); nil Observer → `stats.ObserverFromContext(ctx)` | `ports`, `stats`, `stream`, `github.com/redis/go-redis/v9` |
| `stream`                       | Reactive stream pipeline over typed Go channels — bridges MQTT/ZeroMQ sources with `forge.Function[In,Out]`; **`Stream[T]{Values <-chan T, Errors <-chan error}`** — explicit error channels (idiomatic Go); **sources**: `From[T](ctx, <-chan T) Stream[T]`, `FromCodec[T](ctx, <-chan []byte, format.Format[T], SourceOptions) Stream[T]` (accepts any format — JSON, YAML, TOML, custom), `Single[T](ctx, v T) Stream[T]` (one-shot stream; emits v once, then closes; errors channel never written; used as per-request pipeline entry point in `PipelineHandlerFunc` and `AsPipelineFunc`); **`BroadcastHub[T]`** — N-subscriber fan-out; `NewBroadcastHub(ctx, src Stream[T], bufPerSubscriber int) *BroadcastHub[T]`; `Subscribe() Stream[T]` (returns buffered subscriber stream), `Unsubscribe(Stream[T])` (removes subscriber; channel closed when hub exits); hub goroutine fans out both values AND errors to all subscribers; non-blocking fan-out (slow subscriber drops items silently); used by `SSEFromHub` in nethttp/chi; **transforms**: `Apply[In,Out](ctx, Stream[In], *forge.Function[In,Out], ApplyOptions) Stream[Out]` (calls `forge.Function.ApplyContext` per item; fires `stats.StreamObserver.RecordStreamItem` if observer implements it; errors wrapped in `StreamApplyError`), `Filter[T]`, `Tap[T]` (domain event observation — calls `onValue(T)` without transforming; distinct from infrastructure metrics), `MapErr[T]` (recover/reclassify/silence errors), `Map[In,Out](ctx, src, fn func(In)(Out,error), MapOptions{Name,Observer,Buffer}) Stream[Out]` (typed 1→1 value transform WITH error path — errors wrapped in `StreamMapError{Name,Err}` (has Unwrap+LogValue) to Stream.Errors; use for plain transforms that don't need forge governance — for governed steps keep `Apply`), `Retry[T]` (alias for MapErr with descriptive name for retry pattern); **fan-in/out**: `Merge[T]`, `Tee[T]`; **routing**: `Switch[T](ctx, src, []Case[T], SwitchOptions{Buffer,Observer}) ([]Stream[T], rest Stream[T])` (FIRST matching named case wins; positional outputs `out[i]`↔`cases[i]`; non-matches AND src errors go ONLY to rest — single error ownership; PANICS on malformed cases: empty/duplicate `Name`, nil `When` — programming error by design), `Case[T]{Name string, When func(T) bool}`, `CaseConstraint[T](name, codex.Constraint[T]) Case[T]` (constraint's `Check` becomes the predicate — validation vocabulary doubles as routing vocabulary), `SwitchKey[T,K comparable](ctx, src, keys []K, keyOf func(T) K, SwitchOptions) ([]Stream[T], rest)` (keyed static variant; panics on duplicate keys; share a `TaggedUnion`'s named selectVariant fn so wire format + routing use one declaration), `GroupBy[T,K comparable](ctx, src, key func(T) K, onKey func(K, Stream[T]), GroupByOptions{Buffer,Observer})` (dynamic per-key sub-streams; BLOCKS until src closes — like `SinkPort.Feed`; `onKey` runs on the dispatch goroutine — start consumers, don't run inline; keys unbounded; errors fan out NON-BLOCKING to all active keys; sub-streams close with parent), `OfType[U,T](ctx, Stream[T]) Stream[U]` (typed filter over interface/sum-typed stream; non-matches dropped silently; errors forwarded; observer from ctx, location "oftype"), `SwitchType2[A,B,T]`/`SwitchType3[A,B,C,T]` (typed case streams + untyped rest; first match wins; errors → rest; direct dispatch — NOT composed from OfType), `SplitEither[A,B](ctx, Stream[codex.Either[A,B]], SwitchOptions) (Stream[A], Stream[B])` (TOTAL split, no rest — closed sum; errors fan out to both branches); routing adds NO new error types; observer locations: "groupby", case `Name`, "rest", "oftype", "switchtype.N"/"switchtype.rest", "either.left"/"either.right"; **time**: `Buffer[T]`, `Debounce[T]`, `Throttle[T]`; **sinks**: `Drain[T]` (drains both channels safely in one select loop), `Collect[T]`; **combine**: `CombineLatest2[A,B,Out]`; **errors**: `StreamDecodeError{Source,Err}` + `StreamApplyError{Function,Err}` — both `Error()`, `Unwrap()`, `slog.LogValuer`; **observer**: `stats.StreamObserver.RecordStreamItem` type-asserted from `ApplyOptions.Observer`; `nil` Observer → `stats.NoopObserver`; **topology**: `Topology` + `WithApply[In,Out](topo, fn)` free function (captures forge.FunctionSpec.Hash) + `Topology.WithSwitch(desc)`/`Topology.WithGroupBy(desc)` (`StepKindSwitch`="switch"/`StepKindGroupBy`="groupBy") + `WithPort(name, description)` (`StepKindPort` — an IO hop through a ports port, e.g. IOPort persistence/enrichment; carries the port name, no function spec) + `TopologySpec`; `render/stream.Render(TopologySpec)([]byte,error)` — stream topology YAML (analogous to render/pipeline); **TraceObserver**: `Apply` wraps each item in a child span when `opts.Observer` implements `stats.TraceObserver`; no external dependencies | `codex`, `format`, `forge`, `stats` |
| `adapters/websocket`           | Server-side WebSocket adapter — typed, codec-validated frame streams over persistent bidirectional connections (completes the HTTP story: IngestAdapter=in, SSEAdapter=out, WS=both). Wraps `github.com/gorilla/websocket` behind **narrow interfaces**: `Socket{ReadMessage()([]byte,error), WriteMessage([]byte) error, Close() error}` + `Upgrader{Upgrade(w,r)(Socket,error)}`; `NewUpgrader(UpgraderOptions{Subprotocols, PingInterval /*30s*/, ReadLimit /*1MiB*/, CheckOrigin}) Upgrader` is the ONLY gorilla touchpoint — keepalive (ping/pong deadlines) is shim-owned; tests use hand-written fakes + one gorilla loopback test; **`Hub`** — session registry constructed in main (`NewHub(buffer /*<=0 → 16*/)`), passed to adapters: `SessionInfo(ports.Session) (map[string]string, bool)` (upgrade-time path vars, e.g. which {room}), `Sessions() []ports.Session`; per-session buffered outbound queue — full queue DROPS the frame for that session only (`SocketError` wrapping `ErrFrameDropped` sentinel; slow client never blocks pipeline/other sessions); **port adapters** (binding.go): `IngestSocketAdapter[T](mux Mux, hub, upgrader, handle ports.Socket[T,struct{}], IngestSocketAdapterOptions{Observer}) ports.SourceAdapter[T]` (inbound-only: frames from ALL clients feed the port), `BroadcastSocketAdapter[T](mux, hub, upgrader, handle ports.Socket[struct{},T], BroadcastSocketAdapterOptions{OnError,ErrorFrames,Observer}) ports.SinkAdapter[T]` (WS sibling of SSE; inbound discarded; `ErrorFrames []ErrorFrameRule` — same declarative surface as `DuplexSocketAdapter`, matched against errors from the port's stream Errors channel only, NOT per-session write/encode failures which stay SocketError-wrapped via OnError), `DuplexSocketAdapter[In,Out](mux, hub, upgrader, handle ports.Socket[In,Out], DuplexSocketAdapterOptions{Observer, ErrorFrames}) ports.DuplexAdapter[In,Out]` (session-tagged inbound `Framed[In]` + targeted/broadcast outbound; unknown target session / dropped frame → `SocketError` on port Errors); `ErrorFrames []ErrorFrameRule` (plain slice, NOT type-erased — `ErrorFrameRule` is non-generic) set via `ErrorFrame[E,B](codec codex.Codec[B], mapFn...) ErrorFrameRule` — declares its OWN codec-backed payload type B independent of the socket's Out type (parity with `rest.ErrorPattern`/`events.ErrorChannel`/`reqreply.ErrorPattern`/`mcp.ErrorPattern`: B is validated via its declared codec — all Refine constraints run via `format.JSON(codec).Marshal` — before being broadcast); matches an upstream stream error (`errors.As`) and, on the default `events.ErrorRespond` action, broadcasts the pre-encoded payload to every connected session (the duplex analogue of events.ErrorChannel — no dedicated error topic, broadcast IS the notification); a mapper/encoding failure is forwarded to the port's Errors channel (never silently broadcast); `.WithAction(events.ErrorHandle).WithHandle(func(error))` runs the callback instead (no broadcast); `.WithAction(events.ErrorLog)` and unmatched errors fall through to the existing default (forwarded unchanged to port Errors); `Mux` iface = `Handle(pattern, http.Handler)` subset of http.ServeMux (Go 1.22 patterns, "GET /live/{room}"); handlers registered in Activate; upgrade validated once per connection (ALL template vars extracted for SessionInfo, declared PathParam codecs validated via handle.Route → 422 + RecordValidationError("path"); RecordRequest GET/101/4xx per upgrade attempt); frame decode failure → port Errors + "payload" reports, CONNECTION STAYS OPEN; ctx cancel → closeAll + wg.Wait (no sends after Activate returns); **errors**: `SocketError{Path, Session, Op /*upgrade/read/write/close*/, Err}` (`Error()`, `Unwrap()`, `slog.LogValuer`) + `ErrFrameDropped` sentinel; **observer**: NO new extension — `RecordRequest` per upgrade, `RecordSubscribe` per inbound frame, `RecordPublish` per outbound frame; nil → `stats.ObserverFromContext(ctx)`; **semantics**: at-most-once, no retained value — offline/reconnecting clients LOSE frames (pair with LatestPort for state-on-connect); NOT an MQTT broker — MQTT-over-WS is the MQTT client's transport option (ws:// broker URL to paho); **client-side dial adapters** (client.go): `Dialer{Dial(ctx, url) (Socket, error)}` + `NewDialer(DialerOptions{Subprotocols, RequestHeader, PingInterval, ReadLimit})` gorilla dial shim; `DialSourceAdapter[T](dialer, baseURL, vars, handle ports.Socket[T,struct{}], DialAdapterOptions{MaxBackoff /*30s cap*/, Observer}) ports.SourceAdapter[T]` (consume external feed), `DialSinkAdapter[T](… ports.Socket[struct{},T] …) ports.SinkAdapter[T]`, `DialDuplexAdapter[In,Out](… ports.Socket[In,Out] …) ports.DuplexAdapter[In,Out]` (ONE dialed connection; inbound tagged with session GENERATION "c1","c2",… — a generation change IS the reconnect marker; outbound Session ignored, one peer); URL = baseURL + handle.Route.BuildPath(vars) (PathParam codecs validate); **reconnect BY DESIGN — no silent loss**: auto-reconnect exp backoff 250ms→MaxBackoff (reset after a connection with traffic), EVERY failed dial (`Op:"dial"`) and EVERY drop (`Op:"read"`) → SocketError on port Errors; outbound frames while down (INCLUDING initial connection establishment) DROPPED with ErrFrameDropped — pump/buffer upstream if first frames matter; RecordRequest per dial attempt (101/503) | `api/rest`, `ports`, `stats`, `stream`, `github.com/gorilla/websocket` |
| `ports`                        | **Protocol-agnostic IO enforcement points** for inside-out pipeline wiring. Declare in domain/pipeline code (zero adapter imports); bind concrete transports in main.go. **Three-step declarative model, uniform across every port type**: (1) declare the port's structural shape (`NewSourcePort[T](name, codec, PortOptions)` etc. — no `Patterns` field on `PortOptions` anymore), (2) plug in a `Pattern` via a `PluginXxxPattern(pattern) (handle, error)` method — registers the Pattern AND returns its typed handle in ONE call, no separate lookup step, (3) bind a concrete adapter (with its own adapter-specific `Options`, e.g. QoS) to the port. **Six port types**: `SourcePort[T]` (external → pipeline, fan-in: multiple adapters merged), `SinkPort[T]` (pipeline → external, fan-out: broadcast to all adapters; ALSO supports the request-fed lifecycle `Start(ctx)`/`Push(ctx, v) error`/`Close() error` — port-owned channel + drain goroutine through the same broadcast path as `Feed`; `Push` blocks with backpressure, returns `PortNotStartedError{Port,Op}` before Start/after Close/on a Feed-driven port (the two lifecycles are mutually exclusive), `ctx.Err()` when cancelled while blocked; `Start` is a no-op if already owned, `Close` idempotent and waits for in-flight Push + adapter drain), `IOPort[Req,Resp]` (intermediate IO transform, exactly one adapter), `ToolPort[In,Out]` (server-side request/response — the complement of `IOPort`; `SetPipeline(fn)` once, `Bind` to multiple transports concurrently; `PortNoPipelineError{Port}` if `Bind` called before `SetPipeline`), `LatestPort[T]` (reactive cache: `Feed(ctx, src)` drains a stream into an atomic cell — src errors dropped, cache OUTLIVES the stream; `Bind(ctx, LatestAdapter[T]) error` fan-out serving — adapter contract `Serve(ctx, latest func() (T, bool)) error` MAY return after registration or block until ctx done, run in a supervised goroutine; `Latest() (T, bool)` programmatic read; empty-cache behavior is per-transport: HTTP 503+`NoLatestValueError`, zeromq error reply, MCP error result), `DuplexPort[In,Out]` (bidirectional session boundary — external peers send In frames, receive Out frames over persistent identified sessions; `Framed[T]{Session ports.Session, Payload T}` tags every frame — inbound carries the sender, outbound targets one session or broadcasts when Session is zero (""); `Bind(ctx, DuplexAdapter[In,Out]) error` EXACTLY ONE adapter (second → `PortBindError`, IOPort precedent — session identity across transports unresolved); `Inbound(ctx) Stream[Framed[In]]` (port-owned channels, call once, after Bind); `Feed(ctx, src Stream[Framed[Out]])` blocks until src ends, then closes outbound channels signalling the adapter; adapter contract `Activate(ctx, dst chan<- Framed[In], errs chan<- error, src Stream[Framed[Out]]) error` — must not close dst/errs, must drain src; session routing composes with stream.GroupBy by Framed.Session; only `SocketPattern` accepted — ANY other pattern kind fails Plugin). **`PipePort[T]`** — SCOPED TO COMPUTATION-ONLY (no IO-bridging overloads): a thin wrapper over `gstream`, declared flexibly then never mutated at runtime; `ChainStream[In,Out](ctx, from, transform func(gstream.Stream[In]) gstream.Stream[Out], to)` free function is the GENERAL stage connector, accepting either a `chainSource[T]` (`*PipePort[T]` or `*SourcePort[T]`, anything with `Name() string`+`Stream(ctx) gstream.Stream[T]`) as `from` and a `chainSink[T]` (`*PipePort[T]` or `*SinkPort[T]`, anything with `Name() string`+`Feed(ctx, gstream.Stream[T])`) as `to` — this is what makes `SourcePort -> Chain -> PipePort -> ChainStream -> SinkPort` a single, directly visible top-to-bottom declaration, boundary ports and internal pipes share the exact same call shape; `Chain[In,Out](ctx, from, fn func(In)(Out,error), to)` is `ChainStream`'s single-Map SPECIAL CASE — both call a shared internal `chainWire` engine, each first recording its own `ChainEdge` (see below, only recorded when `from` is actually a `*PipePort[In]`) so spec generation stays accurate per entry point; `fn`/`transform` need NOT be `forge.Function`-wrapped — pass a plain Go function directly unless the step needs `forge`'s contract-hash/signing governance (internal glue mappings usually don't); `Stream(ctx) gstream.Stream[T]` available directly for cases neither Chain nor ChainStream covers (e.g. fanning one stream into several independently-wired downstream pipes); `Feed(ctx, src gstream.Stream[T])` — BLOCKING, mirrors `SinkPort.Feed`'s contract exactly (drains src, closes internally on completion) — this is what lets a `*PipePort[T]` satisfy `chainSink[T]`; `Push(ctx, v) error` feeds items into the pipe at ANY time — before or after `Connect` (pushCh allocated eagerly in `NewPipePort`, items buffer until Connect's consumer drains them; returns `ctx.Err()` only on cancellation, no "not connected" error state); secondary IO use: `InputPort(name) *SourcePort[T]` for adapter fan-in, `OutputPort(name) *SinkPort[T]` for adapter fan-out (same name → same instance, `pipe/in/name` / `pipe/out/name` qualified, built with `PortOptions{Buffer,Params,Observer}` only — kept for legitimate side-observer taps, e.g. logging a copy of a stage's output; IO-BOUNDARY wiring should use a real `SourcePort`/`SinkPort`/`IOPort` declared and Chain/ChainStream'd directly, not this); `Connect(ctx)` starts the hub goroutine, idempotent-safe (second call is a no-op reported via observer, not a duplicate-read bug), fully instrumented — `RecordSubscribe` on the Push-consumer path, `RecordPublish` per fan-out destination (success and failure); `Done() <-chan struct{}` closes only after Connect's internal goroutines fully exit (teardown-complete signal, NOT closed if Connect was never called) — pairs with `app.App.Supervise(name, start func(ctx) <-chan struct{})` for lifecycle supervision of a fire-and-forget `PipePort` without racing `ctx.Done()` against real completion; ONE ordering rule: register InputPort/OutputPort/Stream/Chain/ChainStream before that pipe's Connect — Chain/ChainStream only need to precede the UPSTREAM pipe's Connect, downstream Connect may happen before or after; `Chain`/`ChainStream` wrap their edge-setup (not per-item) in a `"pipe.chain"` `TraceObserver` span; no Patterns on the pipe itself — schema comes from individual boundary SourcePort/SinkPort/IOPort/ToolPort ports at either end of the chain. **Pipeline spec generation — derived, not hand-typed** (`ports/pipeline_spec.go`): `PipeSpecSource` is now a MINIMAL interface (`Name() string` only), satisfied by `*PipePort[T]` AND by boundary ports (`*SourcePort[T]`, `*SinkPort[T]`) — `PipelineSpec(title, version string, sources ...PipeSpecSource) gstream.TopologySpec` accepts a heterogeneous, mixed pipe-and-boundary-port pipeline (different payload types per stage) in one call; richer per-source detail (`Buffer()`, `InputAdapters()`/`OutputAdapters() map[string][]string`, `OutEdges() []ChainEdge`, `BoundAdapters() []string`) is read via internal type assertions against optional extension interfaces (`pipeSpecBuffered`, `pipeSpecInputAdapters`, `pipeSpecOutputAdapters`, `pipeSpecEdges`, `pipeSpecBoundAdapters`) — a source only contributes the detail it actually implements, mirroring the `stats.Observer` extension-interface convention; `ChainEdge{Kind, To, Func}` recorded by `Chain`/`ChainStream` (only when `from` is a real `*PipePort[In]`), `Func` derived via `reflect.ValueOf(fn).Pointer()` + `runtime.FuncForPC` — real Go function identity (e.g. `"main.buildInsertParams"`), honestly closure-opaque for inline `ChainStream` transforms, never fabricated; `SourcePort.BoundAdapters()`/`SinkPort.BoundAdapters() []string` track every bound adapter's real `AdapterName()` at `Bind` time. **Constructors — all return `(*Port, error)`**: `NewSourcePort[T](name, codec, PortOptions)`, `NewSinkPort[T](name, codec, PortOptions)`, `NewIOPort[Req,Resp](name, reqCodec, respCodec, PortOptions)`, `NewToolPort[In,Out](name, inCodec, outCodec, PortOptions)`, `NewLatestPort[T](name, codec, PortOptions)`, `NewDuplexPort[In,Out](name, inCodec, outCodec, PortOptions)`, `NewPipePort[T](name, codec, PortOptions)` — wrap package-level declarations with `codex.Must(...)`. **Convenience constructors — pure sugar, always unwrappable into `NewIOPort`/`NewToolPort` + a Plugin call, never a parallel mechanism**: `NewRestPort[Req,Resp](name, reqCodec, respCodec, pattern RESTPattern, PortOptions) (*IOPort, *rest.RouteHandle[Req,Resp], error)`, `NewReqReplyPort[Req,Resp](..., pattern ReqReplyPattern, ...)`, `NewMCPPort[In,Out](..., pattern MCPPattern, ...)`, `NewSQLPort[Req,Resp](..., pattern SQLPattern, ...)` (all on `IOPort`); `NewRestToolPort[In,Out]`/`NewMCPToolPort[In,Out]` (on `ToolPort`) — each is literally `p, err := NewIOPort(...); h, err := p.PluginXxxPattern(pattern); return p, h, err`; use `codex.Must2(a, b, err)` to unwrap the `(A, B, error)` triple these return. **`Pattern` — declared standalone as a reusable value, then plugged in at wiring time (or immediately, via the convenience constructors above)**: `RESTPattern{Method, Path, Opts []rest.RouteOpt}` (HTTP — ROLE-AWARE: `IOPort`/`ToolPort` build `RouteHandle[Req,Resp]` from the codec pair; `LatestPort` builds `RouteHandle[struct{},T]`; `SourcePort[T]` builds the HTTP-ingest shape `RouteHandle[T,struct{}]` (request body = payload, empty response — pairs with nethttp/chi.IngestAdapter); `SinkPort[T]` builds the SSE shape `SSERouteHandle[struct{},T]` (always GET — a non-GET Method fails Plugin with `PatternRegisterError`; pairs with nethttp/chi.SSEAdapter); the outbound-client sink `DrainCallAdapter` needs an independent Resp codec and stays handle-first), `EventPattern{Topic, Opts []events.ChannelOpt}` (pub/sub — `SourcePort`/`SinkPort` only), `ReqReplyPattern{Topic, Opts []reqreply.RouteOpt}` (request/reply — `IOPort`/`ToolPort`/`LatestPort` only — a request/reply always carries a real payload on BOTH sides, so it never has a one-directional SourcePort/SinkPort form the way RESTPattern does), `MCPPattern{Name, Opts []apimcp.ToolOpt}` (MCP tool — `IOPort`/`ToolPort`/`LatestPort`), `FilePattern{Path, Format FileFormatKind, CustomFormat any, Opts []ports.FileOpt}` (typed file — `SourcePort`/`SinkPort`/`IOPort`/`ToolPort`/`LatestPort` all accept it; `FileFormatKind`: `FileFormatJSON` default/`FileFormatYAML`/`FileFormatTOML`; **`CustomFormat`** — general escape hatch: a pre-built `format.Format[T]` value (type-erased `any`), overrides `Format` when non-nil, type-asserted against T at Plugin time — mismatch returns `PatternRegisterError`; use for Gob/binary/PNG/PDF/protobuf/any format `format.New`-family constructors produce), `SQLPattern{Table, Op string}` (SQL — **metadata-only by design**: query text/placeholders are driver-specific typed closures, no handle, no spec; accepted on `SourcePort`/`SinkPort` (metadata only) plus `IOPort`/`ToolPort`/`LatestPort`; propagated to adapters via ctx — `WithSQLMeta(ctx, m)`/`SQLMetaFromContext(ctx) (SQLPattern, bool)` mirror `WithParams`, wrapped automatically by every port `Bind` and `IOPort.Connect`), `CachePattern{Key string /*template "user:{id}"*/, TTL time.Duration /*0 = no expiry*/, Format FileFormatKind, CustomFormat any, Opts []CacheOpt}` (key/value cache — accepted on `SinkPort` (payload codec), `IOPort` (cached value = RESPONSE codec), `LatestPort` (value codec, var-free key); REJECTED on `SourcePort`/`ToolPort` at Plugin time with `PatternRegisterError{Kind:"cache"}`; builds `Cache[T]{Key, TTL, Format format.Format[T]}` with `BuildKey(vars map[string]string) (string, error)` template expansion — missing var → `CacheKeyError{Key, Var}`; `Opts []CacheOpt` — sealed interface, `CacheKeyParam{Name, Description, Codec *codex.Codec[string]}.WithCodec(c)` validates the key var's VALUE, rejection returns `CacheKeyParamError{Key, Var, Value, Err}`; `NewCache[T](key, format.Format[T], opts ...CacheOpt) Cache[T]` is the standalone constructor, no port/pipeline involved), `SocketPattern{Path, Subprotocols []string, Format FileFormatKind, CustomFormat any, Opts []rest.RouteOpt}` (path-addressed duplex socket — accepted on `SourcePort` (builds `Socket[T,struct{}]`), `SinkPort` (`Socket[struct{},T]`), `DuplexPort` (`Socket[In,Out]`); REJECTED on `IOPort`/`LatestPort`/`ToolPort`). `Opts` carries the *exact* same option vocabulary as `rest.NewRoute`/`events.NewChannel`/`reqreply.NewRoute`/`apimcp.NewTool` — no new param types. A port plugs in one `Pattern` per protocol family it binds to (a `ToolPort` over HTTP+MQTT5+MCP plugs in three). **One construction path — always `Register`, never `ClientHandle`**: internally, each `Pattern` is turned into a handle via `Route`/`Channel`/`Tool.Register(builder)` — the *exact* call a hand-declared route makes — so a `Pattern`-derived handle is indistinguishable from one built by hand with the same builder; adapters cannot tell the difference. `PortOptions.RESTBuilder *rest.Builder` / `EventBuilder *events.Builder` / `ReqReplyBuilder *reqreply.Builder` / `MCPBuilder *apimcp.Builder` — supply your own (with `AddSecurityScheme`/`AddGlobalSecurity`/`rest.WithPathConstraints`/`events.WithTopicConstraints` already configured) to get full parity with a hand-registered route (security schemes, global security, path/topic format constraints, shared spec accumulation); when nil (default), `ports` registers against a private, single-use `Builder` with zero `Info` — same zero-ceremony default, identical `Register` code path. **`PluginXxxPattern` methods — the ONLY way to attach a Pattern (no construction-time `Patterns` field, no separate lookup function)**: `SourcePort.PluginEventPattern/PluginRESTPattern/PluginFilePattern/PluginSQLPattern/PluginSocketPattern`, `SinkPort.PluginEventPattern/PluginRESTPattern/PluginFilePattern/PluginCachePattern/PluginSQLPattern/PluginSocketPattern`, `IOPort.PluginRESTPattern/PluginReqReplyPattern/PluginMCPPattern/PluginFilePattern/PluginSQLPattern/PluginCachePattern`, `ToolPort.PluginRESTPattern/PluginReqReplyPattern/PluginMCPPattern/PluginFilePattern`, `LatestPort.PluginRESTPattern/PluginReqReplyPattern/PluginMCPPattern/PluginFilePattern/PluginCachePattern`, `DuplexPort.PluginSocketPattern` — each registers the Pattern against the port's stored Builder AND returns `(handle, error)` in one call; calling the SAME Plugin method twice on one port returns `PatternRegisterError` (duplicate-registration detection, reusing the same error type Pattern construction failures use). **Spec-from-binding**: `RegisterREST[Req,Resp](b *rest.Builder, port) error` (covers ingest as `RegisterREST[T, struct{}]`) / `RegisterSSE[Event](b, port) error` (SinkPort SSE routes) / `RegisterEvent[T](b, port) error` / `RegisterReqReply[Req,Resp](b, port) error` / `RegisterMCP[In,Out](b, port) error` / `RegisterSocket[In,Out](b *events.Builder, port) error` (renders SocketPattern as an AsyncAPI channel via `events.Builder.AddChannelItem`) — replay an already-plugged-in port's `Pattern` against a *different* real spec `Builder` after the fact; returns `MissingPatternError{Port,Kind}` if the port never plugged in a matching `Pattern`. **`IOParam`** — protocol-agnostic parameter declaration for **handle-less adapters only** (`file.ReadEachAdapter`, `file.DrainWriteFileAdapter` — no `Pattern`/builder of their own): `IOParam{Name, Description, Codec *codex.Codec[string], Required bool}.WithCodec(c)` mirrors `rest.PathParam.WithCodec`; propagated via `WithParams(ctx, params)`/`ParamsFromContext(ctx)`; enforced at runtime via `ValidateParams(params, vars) error` (returns `codex.ValidationErrors`). Handle-backed adapters (REST/events/reqreply/MCP) validate through their `Pattern`-derived handle instead and do not consult `Params`. **`PortOptions`** — full field list: `Params []IOParam`, `Buffer int` (only `SourcePort`/`SinkPort` honor it — `IOPort`/`ToolPort` have no internal channel), `Observer stats.Observer` (a `"port.bind"` `RecordRequest` call + `TraceObserver` span wraps every `Bind`/adapter-Activate call), `RESTBuilder`/`EventBuilder`/`ReqReplyBuilder`/`MCPBuilder` (stored at construction, consulted by every subsequent `PluginXxxPattern` call — NO `Patterns` field). **Adapter interfaces** (implemented by binding constructors in each adapter package): `SourceAdapter[T]` (`Activate(ctx, dst chan<-T, errs chan<-error)` + `AdapterName() string`), `SinkAdapter[T]` (`Activate(ctx, src Stream[T])` + `AdapterName()`), `IOAdapter[Req,Resp]` (`Transform(ctx, src Stream[Req]) Stream[Resp]` + `AdapterName()`), `ToolAdapter[In,Out]` (`Bind(ctx, fn func(ctx,In)Stream[Out]) error` + `AdapterName()`), `LatestAdapter[T]` (`Serve(ctx, latest func() (T, bool)) error` + `AdapterName()`), `DuplexAdapter[In,Out]` (`Activate(ctx, dst chan<- Framed[In], errs chan<- error, src Stream[Framed[Out]]) error` + `AdapterName()`). **Test helpers**: `ChanSourceAdapter[T](<-chan T)`, `ChanSinkAdapter[T](chan<-T)`, `FuncIOAdapter[Req,Resp](func(ctx,Req)(Resp,error))`. **Errors** (all `slog.LogValuer`; most `Unwrap()`): `PortBindError{Port,Adapter,Err}`, `PortNoAdapterError{Port}`, `PortNoPipelineError{Port}`, `PortNotStartedError{Port,Op}` (no Unwrap — no inner error), `MissingPatternError{Port,Kind}` (`Kind`: `"rest"`/`"event"`/`"reqreply"`/`"mcp"`), `PatternRegisterError{Port,Kind,Err}` (`Kind` also includes `"file"`/`"cache"`/`"socket"`; wraps the underlying `rest`/`events`/`reqreply`/`apimcp` error, a `CustomFormat` type-mismatch, a duplicate-Plugin-call, or a plain descriptive error for port-type rejections). **Each adapter package has a binding.go**: `mqtt5.SubscribeAdapter/PublishAdapter/CallAdapter/ServeAdapter`, `mqtt.SubscribeAdapter/PublishAdapter` (`SubscribeAdapterOptions.TopicFilter`, when empty, auto-derives an MQTT wildcard filter from `{var}` placeholders in the handle topic — `"sensors/{id}/data"` → `"sensors/+/data"`), `nethttp.IngestAdapter/SSEAdapter/CallAdapter/PollAdapter/DrainCallAdapter/PipelineAdapter/LatestAdapter`, `chi.IngestAdapter/SSEAdapter/PipelineAdapter/IngestSocketAdapter/BroadcastSocketAdapter/DuplexSocketAdapter` (socket variants delegate to adapters/websocket via a constructor-time swapHandler that satisfies websocket.Mux — chi-safe registration, 503 before Activate; naming shims override AdapterName to "chi.*"), `zeromq.SubscribeAdapter/PublishAdapter/CallAdapter/ServeAdapter/LatestAdapter`, `file.ScanAdapter/WatchAdapter/ReadAdapter/ReadEachAdapter/DrainWriteAdapter/DrainWriteFileAdapter/DrainPatchAdapter/DrainPatchEncodedAdapter` (`ReadAdapter[In,Resp]` is the 2-type per-item read pairing with `FilePattern` — file content IS the response; 3-type `ReadEachAdapter[In,T,Resp]` with `combine` stays handle-first for enrichment), `sql.QueryAdapter/QueryEachAdapter/DrainInsertAdapter` (all three default their options' `Table`/`Op` from `ports.SQLMetaFromContext(ctx)` when the explicit fields are empty — explicit values win), `websocket.IngestSocketAdapter/BroadcastSocketAdapter/DuplexSocketAdapter` + client-side `DialSourceAdapter/DialSinkAdapter/DialDuplexAdapter`, `redis.GetAdapter/SetAdapter/DrainSetAdapter` + plain `Get`/`Set` functions. **File I/O** (`ports.File[T]`, moved here from `format` — protocol-agnostic addressing descriptor bound via `FilePattern`, mirrors `Cache[T]`'s placement exactly; only `format.Format[T]` itself stays in `format`): declarative typed file descriptor mirroring `rest.Route`/`events.Channel`'s declare-once pattern; `ports.NewFile(template, format, ...FileOpt) File[T]`; `ports.FilePathParam{Name, Description}` with `.WithCodec(c) ports.FilePathParam` (no `Required` — all template vars must be present); `ports.FileOptions{Observer stats.Observer, Perm os.FileMode}` (type-asserts to `stats.TraceObserver` for distributed tracing spans); methods: `ports.File.Read(vars, opts) (T, error)`, `ports.File.ReadMerged(vars, opts) (T, error)` (decode-merge convenience — merges vars into the SAME decoded value via `codex.DecodeVars` using `File.MergeFields()`; identical to `Read` when no merge fields declared; `adapters/file.ReadEachAdapter`/`ReadAdapter` call this internally), `ports.File.Write(vars, T, opts) error` (use when you already have the decoded value — no re-read), `ports.WriteHandle[T](file, T, opts) error` (single-call convenience — derives vars from T automatically via `codex.EncodeVars(v, file.MergeFields()...)`, mirrors `mqtt5.PublishHandle`; `adapters/file.DrainWriteFileAdapter`'s `varsFor` may be `nil` to use this), `ports.File.Update(vars, func(T) T, opts) error` (re-reads file — use only when you need the latest state; no `UpdateHandle` convenience yet — deferred pending a concrete re-derivation-timing decision, no roadmap doc needed until a use case appears), `ports.File.Patch(vars, patch map[string]any, opts) error` (JSON Merge Patch RFC 7396 — only map-based formats; absent keys preserved), `ports.File.BuildPath(vars) (string, error)`, `ports.File.ValidatePathVars(vars) error`, `ports.File.PathParamSchemas() map[string]schema.Schema`; free function: `ports.PatchEncoded[T, P any](file ports.File[T], vars, patchCodec Codec[P], patch P, opts) error` — typed partial update via a separate patch struct+codec; P must be a struct codec, intermediate must be map[string]any; **field survival**: fields in file codec re-written, fields in BOTH codecs updated, fields in patchCodec only → written (validated by patchCodec, bypasses file codec re-encode), fields in neither codec → dropped; use `ports.File.Patch` for unknown-field-drop semantics; `format.Format.IsPatchable() bool` — true for JSON/YAML/TOML/New, false for Gob/Binary/NewTyped/NewStreamed; typed errors: `ports.FilePathParamError{Name, Value, Err}`, `ports.MissingFilePathVarError{Name}`, `ports.FileReadError{Path, Err}`, `ports.FileDecodeError{Path, Err}`, `ports.FileEncodeError{Path, Err}`, `ports.FileWriteError{Path, Err}`, `ports.FilePatchNotSupportedError{Path}` — all implement `Unwrap()` and `slog.LogValuer`. | `codex`, `stats`, `stream`, `format`, `api/rest`, `api/events`, `api/reqreply`, `api/mcp` |
| `app`                          | **Application lifecycle manager** — a shutdown-ordering helper, NOT a framework; ports/adapters know nothing about it (zero coupling — imports only `stats` + stdlib). `New(Options{Observer stats.Observer, Logger *slog.Logger, ShutdownTimeout time.Duration /*default 10s*/}) *App`. `Context() context.Context` — cancelable root with Observer pre-injected via `stats.WithObserver` (the SINGLE observer-injection point for a service); cancelled on shutdown. `Go(name, fn func(ctx) error)` — supervised goroutine; **fail-fast, errgroup-style**: first non-nil return cancels the app; all errors still collected. `OnShutdown(name, fn func(ctx) error)` — **LIFO** hooks (defer semantics); a failing hook never stops later hooks; each hook ctx bounded by ShutdownTimeout. `Run(parent) error` — blocks on SIGINT/SIGTERM (signal.NotifyContext installed inside Run ONLY — constructing App installs no handlers), parent cancellation, or first goroutine failure; then cancel → wait goroutines → hooks LIFO → `errors.Join` of everything (nil when clean). `Shutdown() error` — same teardown directly (demos/tests); idempotent + memoized, concurrent calls share one execution. `Supervise(name, start func(ctx context.Context) (done <-chan struct{}))` — supervises a non-blocking component (start is called once, returns immediately) by waiting on `done` for REAL completion; deliberately does NOT race `ctx.Done()` against `done` (that would report "finished" before the component actually drains — the exact bug this method exists to avoid); pairs with `ports.PipePort.Done()` (`start` returns the pipe's `Done()` channel). `Go`/`OnShutdown`/`Supervise` after shutdown = safe logged no-ops (start is never invoked). **Errors** (Error/Unwrap/LogValue): `GoroutineError{Name,Err}`, `HookError{Name,Err}` (wraps `context.DeadlineExceeded` on timeout). **Observer events**: `RecordRequest("app.go", name, 200\|500, dur)` per goroutine exit, `RecordRequest("app.shutdown", name, 200\|500, dur)` per hook (mirrors `"port.bind"`); nil Observer → NoopObserver. Out of scope: dependency graphs, health checks, restart policies. | `stats` |
| `adapters/templ`               | templ SSR format plug-in: `Format[Props](codec, component) format.Format[Props]`, `StreamingFormat[Props](codec, component) format.Format[Props]`, `DecodeNotSupportedError`; add to a route's `Formats` to serve `text/html` alongside JSON via the existing nethttp/chi adapters                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                      | `codex`, `format`, `github.com/a-h/templ`                                  |
| `stats`                        | Observer hooks: `ValidationObserver` (codec-level, 1 method); `Observer` (adapter-level, embeds `ValidationObserver` + transport hooks — `RecordRequest`, `RecordSubscribe`, `RecordPublish` are transport-agnostic: HTTP, ZeroMQ, MCP, MQTT all use them with different method strings); `PipelineObserver` (forge-level, `RecordApply(name, version string, success bool, duration time.Duration)`); `SecurityObserver` (optional interface, `RecordSecurityRejection(location, scheme string)` — type-asserted by adapters, never embedded in `Observer`); `NoopObserver` (satisfies all interfaces); `ReportErrors(obs, location, err)`; `ConstraintName(err)`; **FileObserver** (optional interface, type-asserted by `ports.File[T]`): `RecordFileRead(path string, success bool, d time.Duration)` + `RecordFileWrite(path string, success bool, d time.Duration)`; **SQLObserver** (optional interface, type-asserted by `adapters/sql`): `RecordValidation(table, op string, dur time.Duration, err error)` + `RecordMigration(op, name string, version int64, dur time.Duration, err error)`; **`LoggingObserver`**: `NewLoggingObserver(logger *slog.Logger) *LoggingObserver` — implements all interfaces including FileObserver and SQLObserver, logs every event via slog; **`TraceObserver`**: `StartSpan(ctx, operation, name string) context.Context` + `EndSpan(ctx, err error)` — optional interface, type-asserted by adapters; `NoopObserver` implements it; `LoggingObserver` does NOT; **`StreamObserver`** (optional interface, type-asserted by `stream.Apply`): `RecordStreamItem(function string, success bool, dur time.Duration)` — stream-level throughput metrics; **`CacheObserver`** (optional interface, type-asserted by `adapters/redis`): `RecordCacheHit(key string, dur time.Duration)` + `RecordCacheMiss(key string, dur time.Duration)` + `RecordCacheWrite(key, op string, success bool, dur time.Duration)` — cache lifecycle events; implemented by `NoopObserver`, `LoggingObserver`, and fanout; **`NewFanout`**: `NewFanout(observers ...Observer) Observer` — fans out to all provided observers, delegates optional interfaces (FileObserver/SecurityObserver/PipelineObserver/SQLObserver/StreamObserver/CacheObserver/TraceObserver) to implementing inner observers; **Context observer** (Phase 1 feature): `WithObserver(ctx context.Context, obs Observer) context.Context` — stores obs as default for the context; `ObserverFromContext(ctx context.Context) Observer` — retrieves stored observer or returns `NoopObserver{}`; adapters and stream packages consult `ObserverFromContext(ctx)` when `opts.Observer == nil` — explicit `opts.Observer` always wins; HTTP adapters (`Handler`, `SSEHandler`, `SSEFromStream`) resolve observer per-request/per-connection from `r.Context()` so per-request injection via middleware works; MCP adapters resolve per tool/resource/prompt call from the call `ctx`; `ports.File` resolves from `opts.Context` when set; `sql.Validate` has no ctx and falls back to `NoopObserver{}` (not context-aware); `forge.Registry` uses explicit `WithObserver` builder method (no context integration by design) | `codex`, `time`, `log/slog` (stdlib only)                                  |

- No circular imports.
- `schema` has zero dependencies inside this module.
- `route` imports only `schema` — no renderer or codec logic.
- `render/openapi` imports `schema` and `route` — no codec logic in the renderer layer.
- `render/asyncapi/v2` imports only `schema` — channels are independent of HTTP route concepts.
- `render/asyncapi/v3` imports `schema` and `route` — per-operation security requires route types.
- `render/jsonschema` imports `render/internal/schemarender` and `schema` — no API layer dependencies.
- `api/mcp` imports `render/jsonschema` and `api/internal` — no MCP SDK dependency (transport-agnostic).
- `adapters/mcpgo` imports `api/mcp`, `stats`, and `github.com/mark3labs/mcp-go` — SDK dependency isolated here.
- `examples/` must not be imported by any non-example package.

## Core Abstraction: `Codec[T]`

`Codec[T]` lives in the `codex` package. It bundles encode, decode, and schema in one value.

```go
// Codec encodes values of type T to an intermediate representation,
// decodes that representation back to T, and describes the schema.
type Codec[T any] struct {
    Schema  schema.Schema
    Encode  func(T) (any, error)
    Decode  func(any) (T, error)
}
```

- `Encode` transforms a Go value into an intermediate (e.g., `map[string]any` for JSON).
- `Decode` transforms the intermediate back into a Go value, returning an error on failure.
- `Schema` carries documentation: type name, description, examples, constraints.
- Keep `Codec[T]` fields exported so callers can inspect or wrap them.

### Annotating Codecs

Use fluent methods to attach human-readable metadata to the schema:

```go
// WithDescription returns a new Codec with Schema.Description set.
func (c Codec[T]) WithDescription(desc string) Codec[T]

// WithTitle returns a new Codec with Schema.Title set.
func (c Codec[T]) WithTitle(title string) Codec[T]

// WithExample returns a new Codec with Schema.Example set (any value).
func (c Codec[T]) WithExample(v any) Codec[T]

// WithDeprecated returns a new Codec with Schema.Deprecated = true.
func (c Codec[T]) WithDeprecated() Codec[T]
```

These are typically chained after `Refine`:

```go
var AgeCodec = codex.Int().
    Refine(validate.RangeInt(0, 150)).
    WithTitle("Age").
    WithDescription("Age in years.").
    WithExample(25)

var LegacyIPCodec = codex.String().
    Refine(validate.IPv4).
    WithDescription("IPv4 of last login. Deprecated: use hostname.").
    WithDeprecated()
```

### `Validate`, `New`, and `Must`: Construction-Time Validation

**`Codec.Validate(v T) error`** checks a Go value by encoding it (which runs Refine constraints) and then decoding it back. Returns only the error; the value is discarded.

**`Codec.New(v T) (T, error)`** validates and returns the value. Use as a smart constructor — validate at the point of construction, get a typed result back:

```go
// Validate is declared on Codec[T]:
func (c Codec[T]) New(v T) (T, error)

// Example:
email, err := emailCodec.New(Email("user@example.com"))
if err != nil {
    return err
}
// email is valid here
```

**`Must[T any](v T, err error) T`** is a generic panic-on-error helper. Use it for package-level validated constants and test data — not for user-facing code:

```go
// Package-level constant validated at init time:
var guestUser = codex.Must(usernameCodec.New(Username("guest")))

// Test helper:
got := codex.Must(emailCodec.Decode("user@example.com"))
```

**Named per-field constructors for struct codecs**: `Codec[T].New` is the only smart-constructor primitive — there is no separate reflection-based or codegen "derive a `NewUser(field1, field2, ...)` from the codec" feature, and there will not be one (go-codex has no reflection, no struct tags for codec logic — see the design rule above — and Go generics cannot express a variadic-arity constructor that works for any `codex.Struct[T]`). The idiomatic pattern is a 3-line hand-written wrapper that takes positional field values and delegates to `Codec.New`:

```go
var UserCodec = codex.Struct[User](
    codex.RequiredField("name", nameCodec, func(u User) string { return u.Name }, func(u *User, v string) { u.Name = v }),
    codex.RequiredField("age", ageCodec, func(u User) int { return u.Age }, func(u *User, v int) { u.Age = v }),
)

// NewUser is a named constructor: positional args in, validated User or error out.
// It is a thin wrapper — all validation still runs through UserCodec.New/Validate.
func NewUser(name string, age int) (User, error) {
    return UserCodec.New(User{Name: name, Age: age})
}
```

See `examples/construction/main.go` for a runnable example.

**When to use each:**

|               | `Validate`              | `New`             | `Must`           |
| ------------- | ----------------------- | ----------------- | ---------------- |
| Returns value | no                      | yes               | yes              |
| Returns error | yes                     | yes               | panics           |
| Typical use   | check before store/send | smart constructor | constants, tests |

## `HasCodec` Interface

Types that have a canonical codec implement `HasCodec[T]`:

```go
// HasCodec is implemented by types that declare their canonical Codec.
type HasCodec[T any] interface {
    Codec() codex.Codec[T]
}
```

- Prefer defining `Codec()` as a package-level function `func Codec() codex.Codec[MyType]` when the type is a value type.
- Use a method receiver only when the codec depends on instance state.

## `MapCodecSafe`: Bidirectional Codec Transformation

`MapCodecSafe[A, B any]` transforms `Codec[A]` into `Codec[B]`. Equivalent to autodocodec's `BimapCodec`.

```go
// MapCodecSafe creates a new Codec[B] from Codec[A] using two mapping functions.
// to is the decode direction and must always succeed (total).
// from is the encode direction and may return an error.
func MapCodecSafe[A, B any](c codex.Codec[A], to func(A) B, from func(B) (A, error)) codex.Codec[B]
```

- Use when a type wraps a primitive: e.g., `type Email string` over `primitive.String()`.
- `to` is the decode direction: transforms the decoded `A` into `B`. Must be total.
- `from` is the encode direction: transforms a `B` back to `A` for encoding. May fail.
- Schema is inherited from `Codec[A]`.
- For validation on decode, use `Refine` instead of `MapCodecSafe`.

```go
// Good example — Email newtype codec
type Email string

var EmailCodec = codex.MapCodecSafe(
    codex.String(),
    func(s string) Email { return Email(s) },
    func(e Email) (string, error) { return string(e), nil },
)

// Validation belongs in Refine, not MapCodecSafe:
var ValidEmailCodec = EmailCodec.Refine(codex.Constraint[Email]{
    Name:    "email",
    Check:   func(e Email) bool { return strings.Contains(string(e), "@") },
    Message: func(e Email) string { return fmt.Sprintf("invalid email: %q", e) },
})
```

## `MapCodecValidated`: Fallible Mapping with Post-Decode Validation

`MapCodecValidated[A, B any]` transforms `Codec[A]` into `Codec[B]` where both mapping directions may fail, and the mapped `B` value is validated using a provided `Codec[B]`.

```go
// MapCodecValidated creates a Codec[B] from Codec[A] and Codec[B] using two fallible mapping functions.
// After mapping to B in the decode direction, cb.Validate enforces all Refine constraints on cb.
// The resulting codec carries cb's schema.
func MapCodecValidated[A, B any](ca codex.Codec[A], cb codex.Codec[B], to func(A) (B, error), from func(B) (A, error)) codex.Codec[B]
```

- `to` is the decode direction: fallible — returns `(B, error)`.
- `from` is the encode direction: fallible — returns `(A, error)`.
- After `to(a)` succeeds, `cb.Validate(b)` runs all `Refine` constraints defined on `cb`.
- On encode, `cb.Validate(b)` is called before `from(b)` to prevent encoding invalid values.
- Schema comes from `cb` (the domain type with its constraints).
- Use when the mapping itself can fail **and** the target type `B` carries its own validation rules.

```go
type Celsius float64

var celsiusBaseCodec = codex.MapCodecSafe(
    codex.Float64().
        Refine(validate.MinFloat(-273.15)).
        Refine(validate.MaxFloat(1_000_000)),
    func(f float64) Celsius { return Celsius(f) },
    func(c Celsius) (float64, error) { return float64(c), nil },
)

var celsiusCodec = codex.MapCodecValidated(
    codex.Float64(),    // ca: wire codec
    celsiusBaseCodec,   // cb: domain codec with range constraints
    func(f float64) (Celsius, error) {
        if f != f { return 0, errors.New("NaN is not a valid temperature") }
        return Celsius(f), nil
    },
    func(c Celsius) (float64, error) { return float64(c), nil },
)
```

**When to choose `MapCodecSafe` vs `MapCodecValidated`:**

|                        | `MapCodecSafe`         | `MapCodecValidated`           |
| ---------------------- | ---------------------- | ----------------------------- |
| `to` direction         | infallible `func(A) B` | fallible `func(A) (B, error)` |
| Post-decode validation | none                   | `cb.Validate(b)`              |
| Pre-encode validation  | none                   | `cb.Validate(b)`              |
| Schema source          | `ca`                   | `cb`                          |
| Typical use            | newtype wrappers       | domain types with constraints |

## `Downcast`: Type Assertion Helper

`Downcast[A, B any]` attempts to cast a value of type `B` to type `A` using a type assertion.

```go
// Downcast attempts to cast a value of type B to type A.
// Useful for tagged unions where variants share a common interface.
func Downcast[A any, B any](v B) (A, error)
```

- Use with `TaggedUnion` when variant types share a common interface and you need to convert to a concrete type.

## `Refine` and `Constraint`

`Refine[T]` wraps an existing `Codec[T]` with one or more `Constraint[T]` predicates. All constraints run on **both Encode and Decode** — a value that fails a constraint cannot be serialised OR deserialised. This ensures the codec is the single source of truth for validity.

```go
// Constraint is a named validation predicate.
// The optional Schema field annotates the codec's schema when the constraint
// is applied via Refine. Set it to propagate constraint metadata (e.g. bounds,
// patterns) into the schema for renderers such as render/openapi.
type Constraint[T any] struct {
    Name    string
    Check   func(T) bool
    Message func(T) string
    Schema  func(schema.Schema) schema.Schema // optional: mutates schema when Refine is applied
}

// Refine adds one or more constraints to a codec. Constraints run on
// both Encode and Decode, in order; first failure stops evaluation.
// If Constraint.Schema is non-nil, it is applied to the codec's schema.
// Calling Refine with no arguments returns the codec unchanged.
func (c Codec[T]) Refine(cons ...Constraint[T]) Codec[T]
```

- `Constraint.Name` identifies the constraint in error messages.
- `Constraint.Message` produces the human-readable failure description.
- `Constraint.Schema` is optional. Set it to annotate the codec's schema (e.g. `MinLength`, `Minimum`). Nil = no-op; all existing constraints are unaffected.
- Reusable constraints live in `validate/`; domain-specific ones live next to the type.

For cross-field validation without defining a named `Constraint[T]`, use `RefineFunc`:

```go
// RefineFunc wraps a func(T) error as a constraint applied on both Encode and Decode.
// On failure, returns ConstraintError{Name:"refine", Message: err.Error()}.
func (c Codec[T]) RefineFunc(fn func(T) error) Codec[T]
```

```go
// Good example — cross-field constraint
var rangeCodec = codex.Struct[DateRange](...).
    RefineFunc(func(r DateRange) error {
        if !r.End.After(r.Start) {
            return errors.New("end must be after start")
        }
        return nil
    })
```

```go
// Good example — constrained integer
var PositiveIntCodec = codex.Int().Refine(validate.PositiveInt)

// Good example — custom constraint with schema annotation
var ShortStringCodec = codex.String().Refine(codex.Constraint[string]{
    Name:    "maxLen(50)",
    Check:   func(v string) bool { return len(v) <= 50 },
    Message: func(v string) string { return "string too long" },
    Schema: func(s schema.Schema) schema.Schema {
        n := 50
        s.MaxLength = &n
        return s
    },
})
```

`codex.Empty` is a ready-made `Codec[struct{}]` for body-less routes:

```go
// Use codex.Empty as reqCodec for GET / DELETE / SSE routes — no per-file empty struct needed.
// Adapters skip body decoding when Descriptor.RequestBody == nil (GET, HEAD, DELETE, etc.).
handle, err := rest.NewRoute[struct{}, User]("GET", "/users/{id}",
    codex.Empty, userCodec, rest.RouteMeta{OperationID: "getUser"},
).Register(b)

sseRoute, err := rest.NewSSERoute[struct{}, Event]("/stream",
    codex.Empty, eventCodec, rest.RouteMeta{OperationID: "streamEvents"},
).Register(b)
```

`codex.Pure` and `codex.Eq` are related fixed-value combinators:

```go
// Pure: always decodes to value, always encodes value. Schema: {enum:[value]}.
// Use for protocol version fields, derived fields set automatically.
func Pure[T any](value T) Codec[T]

// Eq: wraps base with an equality constraint. Decode: base decodes, then checks == value.
// Schema: inherits base schema with Enum set to [value].
// Use a typed base codec so wire-type coercion is handled: Eq(Int(), 42) accepts JSON float64(42).
func Eq[T comparable](base Codec[T], value T) Codec[T]
```

```go
// Good example — CloudEvents spec version (always "1.0")
var specVersionCodec = codex.Pure("1.0")

// Good example — only accept one specific event type
var orderEventTypeCodec = codex.Eq(codex.String(), "com.example.order.placed")
```

## Object Codec: Struct Composition

`codex.Struct` builds a codec for a struct by composing field codecs. Modelled after autodocodec's `ObjectCodec` with `RequiredKey` / `OptionalKey`.

```go
// Field describes a single struct field and its codec.
type Field[S, F any] struct {
    Name     string
    Codec    codex.Codec[F]
    Get      func(S) F          // for encoding
    Set      func(*S, F)        // for decoding
    Required bool
}
```

- `Field.Name` is the explicit key string used in the encoded representation.
- Compose fields into a struct codec using `codex.Struct`.
- Use `codex.RequiredField` / `codex.OptionalField` / `codex.DefaultField` instead of `Field{..., Required: true/false}` for clearer intent:

```go
// Preferred — intent explicit from constructor name
var PointCodec = codex.Struct[Point](
    codex.RequiredField("x", codex.Float64(),
        func(p Point) float64 { return p.X },
        func(p *Point, v float64) { p.X = v },
    ),
    codex.OptionalField("y", codex.Float64(),
        func(p Point) float64 { return p.Y },
        func(p *Point, v float64) { p.Y = v },
    ),
    // DefaultField: absent key uses "info"; default is also reflected in schema
    codex.DefaultField("log_level", codex.String(), "info",
        func(c Config) string { return c.LogLevel },
        func(c *Config, v string) { c.LogLevel = v },
    ),
)
```

`DefaultField` sets `Required: false` and stores the default as `*F` (pointer, to distinguish zero-value defaults from "no default"). The default is reflected in `Schema.Default` and rendered as `default` in OpenAPI/AsyncAPI.

## Union Codec: Tagged and Untagged Unions

`codex.TaggedUnion` handles discriminated unions via a string tag field.

```go
// TaggedUnion builds a Codec[T] for a sum type discriminated by a tag field.
func TaggedUnion[T any](
    tag string,
    variants map[string]codex.Codec[T],
    selectVariant func(T) (string, error),
) codex.Codec[T]
```

- `tag` is the JSON key used to identify the variant (e.g., `"type"`).
- `variants` maps tag strings to codecs that handle each case.
- `selectVariant` picks the tag for a given value during encoding.
- Return an error during decode when no variant matches the tag.
- `TaggedUnion` automatically sets `Schema.Discriminator = &schema.DiscriminatorSchema{PropertyName: tag}` on the returned codec's schema. This is reflected in OpenAPI/AsyncAPI specs via the shared `render/internal/schemarender` package.

```go
// Good example — Shape union
var ShapeCodec = codex.TaggedUnion[Shape]("type",
    map[string]codex.Codec[Shape]{
        "circle":    CircleCodec,
        "rectangle": RectangleCodec,
    },
    func(s Shape) (string, error) { return s.Kind(), nil },
)
```

`codex.UntaggedUnion` is the complement for cases where no discriminator field is present in the encoded form.

```go
// UntaggedVariant[T] pairs a documentation name with a Codec[T].
type UntaggedVariant[T any] struct {
    Name  string
    Codec Codec[T]
}

// UntaggedUnion tries each variant in order during decode (first match wins).
// `which` selects the encode branch by 0-based variant index.
func UntaggedUnion[T any](which func(T) int, variants ...UntaggedVariant[T]) Codec[T]
```

- Schema: `{oneOf: [...variant schemas...]}` — no `discriminator` block.
- Decode failure (all variants fail): returns `EitherError{Errors: [...]}`

`codex.Either2` produces a `Codec[Either[A,B]]` that tries codec A first, then B.

```go
type Either[A, B any] struct {
    Left  *A  // non-nil if decoded as A
    Right *B  // non-nil if decoded as B
}

func Either2[A, B any](ca Codec[A], cb Codec[B]) Codec[Either[A, B]]
```

- Decode: try `ca`; if it fails, try `cb`; if both fail, return `EitherError{Errors: []error{errA, errB}}`.
- Encode: if `Left != nil`, use `ca`; else use `cb`.
- Schema: `{oneOf: [schemaA, schemaB]}`.
- Left branch wins on ambiguity (order-dependent, documented).

## Schema Model

The `schema` package defines pure data structures that describe a codec. No codec logic lives here.

- `schema.Schema` is the root type; it carries `Type`, `Title`, `Description`, `Format`, `Example`, `Properties` (ordered `[]schema.Property`), `Required`, `Enum`, `OneOf`, `Items`, and numeric/string constraint fields (`Minimum`, `Maximum`, `ExclusiveMinimum`, `ExclusiveMaximum`, `MinLength`, `MaxLength`, `Pattern`).
- `schema.Property` is `{Name string; Schema Schema}` — using a slice instead of a map preserves registration order for deterministic YAML/JSON output.
- Use `s.Prop(name)` to look up a property by name (returns `(Schema, bool)`).
- Additional fields on `schema.Schema`:
  - `Nullable bool` — marks the value as accepting null; renders as `nullable: true` in OpenAPI/AsyncAPI.
  - `AdditionalProperties *bool` — nil = unset (spec default), `false` = no extra properties, `true` = any allowed.
  - `Discriminator *schema.DiscriminatorSchema` — describes the polymorphism tag for `TaggedUnion` schemas. Set automatically by `TaggedUnion`.
  - `Deprecated bool` — renders as `deprecated: true` in OpenAPI/AsyncAPI. Set by `Codec.WithDeprecated()`.
  - `Default any` — the declared default value for a field. Set by `DefaultField` and rendered as `default` in generated schemas.
- `schema.DiscriminatorSchema` holds `PropertyName string` and optional `Mapping map[string]string`.
- Codec constructors populate `Schema` when building a `Codec[T]`.
- Downstream renderers (JSON Schema, OpenAPI) read `schema.Schema` without touching codec logic.

## Naming Conventions

| Concept             | Convention                                        | Example                                           |
| ------------------- | ------------------------------------------------- | ------------------------------------------------- |
| Codec variable      | `<Type>Codec` (exported) or `codec` (unexported)  | `EmailCodec`, `PointCodec`                        |
| Constraint variable | descriptive noun/adjective                        | `validate.PositiveInt`, `validate.NonEmptyString` |
| Field key string    | camelCase matching external representation        | `"firstName"`, `"createdAt"`                      |
| Tag key string      | `"type"` by default unless domain differs         | `"type"`, `"kind"`                                |
| Package function    | `func Codec() codex.Codec[T]` for canonical codec | `func Codec() codex.Codec[Email]`                 |

## Error Handling in Codecs

All decode failures are concrete structured types. Every type implements `error`, `slog.LogValuer`, and (where applicable) `Unwrap`.

| Type                  | Returned by                                               | Key fields                                                                                     |
| --------------------- | --------------------------------------------------------- | ---------------------------------------------------------------------------------------------- |
| `ValidationErrors`    | `Struct` decode                                           | `[]ValidationError`; `Unwrap() []error` for `errors.Is`/`As` traversal                         |
| `ValidationError`     | each failing field in `Struct` decode                     | `Field string`, `Err error`; `Unwrap()` returns `Err`                                          |
| `ConstraintError`     | `Refine`/`RefineFunc` on any codec when constraint fails  | `Name string`, `Message string`                                                                |
| `TypeMismatchError`   | any codec receiving wrong Go type                         | `Expected string`, `Got string`                                                                |
| `ElementError`        | `SliceOf` decode/encode                                   | `Index int`, `Err error`; `Unwrap()` returns `Err`                                             |
| `KeyError`            | `StringMap` decode/encode                                 | `Key string`, `Err error`; `Unwrap()` returns `Err`                                            |
| `UnknownVariantError` | `TaggedUnion` when tag value has no matching codec        | `Tag string`, `Variant string`; no `Unwrap`                                                    |
| `VariantError`        | `TaggedUnion` when a known variant fails to decode/encode | `Tag string`, `Variant string`, `Err error`; `Err` is always non-nil; `Unwrap()` returns `Err` |
| `EitherError`         | `Either2`/`UntaggedUnion` when all branches fail          | `Errors []error`; `Unwrap() []error` for `errors.Is`/`As` traversal                            |
| `ErrMissingField`     | required `Field` when key absent                          | exported sentinel; use `errors.Is`                                                             |

- Struct decode collects **all** field errors before returning — the error is always `ValidationErrors`, never a partial slice.
- Use `errors.As(err, &ve)` to extract `ValidationErrors`. Then inspect each `ValidationError.Err` for the underlying cause.
- `ValidationErrors.Unwrap() []error` enables `errors.Is`/`errors.As` to traverse the full list directly.
- Encode errors are exceptional; prefer designs where encoding is total.

```go
// Struct decode: collect all field errors, inspect constraint name.
_, err := MyCodec.Decode(input)
var ve codex.ValidationErrors
if errors.As(err, &ve) {
    for _, fe := range ve {
        var ce codex.ConstraintError
        if errors.As(fe.Err, &ce) {
            fmt.Printf("field %q: constraint %q failed: %s\n", fe.Field, ce.Name, ce.Message)
        }
        if errors.Is(fe.Err, codex.ErrMissingField) {
            fmt.Printf("field %q: required but absent\n", fe.Field)
        }
    }
}

// slog: all structured error types implement slog.LogValuer; wrapping types
// use slog.Any("cause", e.Err) so nested LogValue() is preserved.
logger.Error("decode failed", slog.Any("validation_errors", ve))
// → validation_errors.name.constraint=non-empty validation_errors.name.message="..."
```

See `examples/error-types/` for a runnable demo of every error type with `errors.As` and slog.
See `examples/decode-errors/` for struct validation errors and HTTP 400 response patterns.

## Common Patterns

### Wrapping a Primitive Type

```go
type UserID string

var UserIDCodec = codex.MapCodecSafe(
    codex.String(),
    func(s string) UserID { return UserID(s) },
    func(id UserID) (string, error) { return string(id), nil },
)
```

### Slice Codec

```go
var EmailListCodec = codex.SliceOf(EmailCodec)
```

### Time and Date Codecs

```go
// Codec[time.Time] — RFC 3339 strings; schema {type:string, format:date-time}
var CreatedAtCodec = codex.Time()

// Codec[time.Time] — date-only strings (2006-01-02); schema {type:string, format:date}
var BirthDateCodec = codex.Date()
```

### Hex Color Codec

`codex.HexColor() Codec[Color]` follows the same "builtin codec type" shape as
`Time()`/`Date()`/`Duration()`: a real Go struct (`Color{R, G, B, A uint8}`),
not a plain string. `Color` is deliberately NOT `image/color.RGBA` — that
stdlib type's channels are alpha-premultiplied, a different semantic than hex
notation's straight channels, and reusing it would silently produce wrong
values for partially-transparent colors.

```go
// Codec[Color] — CSS Color Level 4 hex notation: "#RGB", "#RRGGBB", "#RGBA",
// "#RRGGBBAA" (case-insensitive on decode). Encode always emits a canonical
// lowercase form: "#rrggbb" when A is 255 (fully opaque), "#rrggbbaa"
// otherwise — never the 3/4-digit shorthand, regardless of decode input.
var BrandColorCodec = codex.HexColor()

c, err := BrandColorCodec.Decode("#1e90ff") // Color{R:0x1E, G:0x90, B:0xFF, A:0xFF}
```

Decode failures return `codex.InvalidColorError{Value string}` (no `Unwrap` —
no wrapped cause) for malformed hex strings; non-string input returns the
existing `codex.TypeMismatchError`, matching `Time()`/`Date()`/`Duration()`.
Schema is `{type: "string", pattern: ...}` — there is no standard JSON Schema
`format` keyword for color, so `Pattern` documents the accepted shape instead.

### MANDATORY design contract: one struct, one call (per boundary)

For any API-contract boundary with a request/response shape or a duplex
role pair (publisher/subscriber, requestor/replier, client/server), a
caller on EITHER side must be able to do the ENTIRE encode-or-decode
direction with exactly **one struct value in (or out) and one call** — no
manual map-building, no manual header/cookie/query/topic stitching, in the
common case. This is the headline user promise; the mechanics below
(declare-once constructors, escape hatch, encode/decode symmetry, role
symmetry) exist to make that promise safe, not to replace it.

REST is the reference implementation and the model every boundary follows:

```go
// Client: ONE struct in, ONE struct out — no manual maps.
handle := getUserActivity.ClientHandle()
req := GetUserActivityReq{ID: userID, Filter: "logins"} // literal, or the caller's own New... factory
resp, err := nethttp.CallHandle(ctx, client, baseURL, handle, req, nethttp.CallOptions{})
// resp is fully decoded AND merged: body + response header/cookie fields, e.g. resp.RequestID.

// Server: ONE struct in, ONE struct out — no manual r.PathValue()/w.Header().Set().
nethttp.Register(mux, handle, func(ctx context.Context, req GetUserActivityReq) (User, error) {
    u := lookup(req.ID)     // req arrives fully merged: path+query+header+cookie+body
    u.RequestID = traceID() // just set the field
    return u, nil           // adapter auto-encodes body AND response merge fields
}, nethttp.Options{})
```

**Now true everywhere, both the core API and the `ports.Pattern` binding
layer**: `api/rest`, `api/events` (pub/sub), and `api/reqreply` (req/reply)
satisfy this at the CORE-API level (declare-once constructors,
`DecodeMerged`, single-call convenience), AND the `ports.Pattern` binding
layer's `SinkAdapter`/`IOAdapter` constructors
(`nethttp.DrainCallAdapter`/`CallAdapter`, `mqtt5.PublishAdapter`/
`CallAdapter`, `zeromq.PublishAdapter`/`CallAdapter`,
`mqtt.PublishAdapter`) delegate to `CallHandle`/`PublishHandle` and derive
vars PER-ITEM whenever their `Vars` option is left `nil` — a single static
`Vars` map remains available as the escape hatch when explicitly set.
`adapters/zeromq`'s own pub/sub `Subscribe`/`Publish` and `adapters/mqtt`
(v3) events also got the same merge-field wiring `adapters/mqtt5` already
had. `ports.File`/`adapters/file` and `ports.Cache`/`adapters/redis` got
the SAME convenience too: `File.ReadMerged`/`ports.WriteHandle` and
`redis.GetMerged`/`redis.SetHandle` mirror `DecodeMerged`/`CallHandle`
exactly (`ReadEachAdapter`/`ReadAdapter`/`GetAdapter` call the Merged
variant internally; `DrainWriteFileAdapter`/`SetAdapter`/`DrainSetAdapter`'s
`varsFor`/`keyFn` closure argument may be left `nil` to derive vars
per-item automatically). Fixing this also caught a real, pre-existing bug:
BOTH `ports.CachePattern` build paths silently dropped
`NewCacheKeyParam`-registered merge fields entirely (fixed by delegating to
`NewCache`, mirroring `FilePattern`'s existing delegation to `NewFile`).
A shared, module-internal `internal/templatematch` package now backs the
topic/path-matching core for `mqtt`/`mqtt5`/`zeromq`/`ports.File`. SSE and
WebSocket now ship the same convenience for long-lived connections:
`rest.NewRequiredSSEEventParam`/`NewOptionalSSEEventParam` +
`SSERouteHandle.MergeEvent` (adapter auto-wired), and
`ports.NewRequiredSocketInParam`/`NewOptionalSocketInParam` +
`ports.NewRequiredSocketOutParam`/`NewOptionalSocketOutParam`
(`SocketPattern.InOpts`/`OutOpts`, adapter auto-wired).

**Every new or newly-touched Req/Resp-or-payload-shaped boundary must reach
REST's bar** — see the `add-a-new-adapter` skill's "Step 5b" for the
concrete checklist (declare-once constructors, escape hatch, encode/decode
symmetry via role-aware accessors, role symmetry, a single-call wrapper).

**Not JSON-specific, not flat-struct-specific** — do not assume either
narrowing when implementing or reviewing this pattern:
- Body decode/encode is completely orthogonal to var-merge
  (`codex.DecodeVars`/`EncodeVars` only ever touch a `map[string]string`,
  never body bytes), so ANY `format.Format[T]` (JSON, YAML, TOML,
  `format.Gob`, `format.Binary`, a custom `format.NewTyped`) composes with
  merge fields unchanged.
- Merge-field `get`/`set` are plain Go closures, not reflection over a
  struct's direct fields, so nested composition
  (`func(r Req) string { return r.Meta.X }`) works with zero framework
  changes.
- One subtlety: whole-value binary formats (`format.Gob`, protobuf, custom
  binary) serialise the typed value directly via reflection, bypassing the
  codec's `Encode`/`Decode` for the wire bytes — `format.Gob(reqCodec)` on
  a nested `Req` gob-encodes EVERY exported field, not just a nested
  `Payload` sub-field (harmless — `DecodeMerged` always merges AFTER body
  decode, so the authoritative HTTP values win — but wasteful). Use
  `format.NewTyped` with a custom marshal/unmarshal projecting onto/from
  the sub-field when the wire bytes should represent ONLY that sub-field.
  See `examples/rest-nested-binary` for the full runnable version.

### Declarative Var Extraction & Merge: `DecodeVars`/`EncodeVars` (mechanics)

`codex.FieldCodec[T]` is the exported name for the interface `Struct[T]`
composes internally (renamed from the previously-unexported `fieldCodec[T]`
so other packages can name it in their own signatures) — `RequiredField`/
`OptionalField`/`DefaultField` already produce values satisfying it.

`codex.DecodeVars[T](target *T, vars map[string]string, fields ...FieldCodec[T]) error`
decodes named vars (path/topic/header/query/cookie/filename values — always
string-keyed) into a SUBSET of an existing struct's fields, using the SAME
`Field`/`RequiredField`/`OptionalField`/`DefaultField` declarations already
used for `Struct[T]` — a PARTIAL merge (unlike `Struct.Decode`, which builds
an entirely new T from one JSON object). Reuses `codex.ValidationErrors`
verbatim — no new error type for the decode direction.

`codex.EncodeVars[T](v T, fields ...FieldCodec[T]) (map[string]string, error)`
is the inverse — extracts field values into a `map[string]string`,
replacing hand-written `varsFor func(T) map[string]string` closures used by
every adapter's `SinkAdapter`/`IOAdapter`/`SourceAdapter` constructor.
Returns `codex.VarEncodeTypeError{Field, Got}` if a field's codec doesn't
produce a string (an unsuitable codec attached to a var field — caller
error, not a runtime data error).

```go
var req GetUserReq
err := codex.DecodeVars(&req, map[string]string{"id": r.PathValue("id")},
    codex.RequiredField("id", codex.String().Refine(validate.UUID),
        func(r GetUserReq) string { return r.ID },
        func(r *GetUserReq, v string) { r.ID = v }))
```

**Per-boundary "declare once" sugar** — `rest.NewPathParam[T]`,
`rest.NewRequiredQueryParam[T]`/`NewOptionalQueryParam[T]` (+ Header/Cookie
equivalents), `ports.NewFilePathParam[T]` declare BOTH the existing spec
Param (`PathParam`/`QueryParam`/etc. — unchanged, still validate-only when
used directly) AND a merge field from ONE call, `T` inferred from `get`/
`set` exactly like `RequiredField` infers `T`/`F`. Each `Merged*Param[T]`
wrapper gets a `.WithDescription(string)` chain method (sets the
PARAMETER-level description — rendered into the OpenAPI "parameter"
object, distinct from the codec's own `Schema.Description`, rendered into
the nested "schema" object). **Primary but not sole**: these constructors
are the documented, recommended path; the plain `PathParam`/`QueryParam`/
`FilePathParam` struct literals remain the low-level escape hatch for
validate-only params with no merge need (avoids forcing a `get`/`set` pair
on a param a handler never reads directly) — a route/file can freely mix
both styles.

`RouteHandle.MergeFields() []codex.FieldCodec[Req]` /
`ports.File.MergeFields() []codex.FieldCodec[T]` collect the registered
merge fields for direct use with `codex.DecodeVars`/`EncodeVars`.
`RouteHandle.DecodeMerged(body, pathVars, query, headers, cookies) (Req, error)`
closes the loop for REST — decodes the body (if any) AND merges every
`MergeFields()`-registered value into the SAME `Req`, in one call;
`adapters/nethttp`/`adapters/chi`'s `Handler` call it automatically whenever
`handle.MergeFields()` is non-empty (byte-for-byte identical behavior
otherwise). `ports.File.MatchPath(path string) (map[string]string, error)`
is the missing inverse of `BuildPath` — matches a discovered file path
against the template, extracting vars (mirrors `mqtt.TopicVarsFromMessage`'s
existing pattern for MQTT topics); a `{var}` placeholder may share a
segment with literal text (e.g. `{date}.json`).

**Role-aware split (client encode direction)**: `RouteHandle.MergeFields()`
is an AGGREGATE across all four roles — safe for DECODE (`DecodeMerged`,
since the source vars map is already correctly scoped before merging) but
NOT safe for ENCODE, since `nethttp.CallOptions.QueryParams`/`HeaderParams`/
`CookieParams` each add every map entry to their HTTP location with no name
filtering. Use `RouteHandle.PathMergeFields()`/`QueryMergeFields()`/
`HeaderMergeFields()`/`CookieMergeFields()` instead — each returns ONLY
that role's fields, safe to feed `codex.EncodeVars` independently per
`nethttp.Call` parameter.

**Response merge fields** (server encode / client decode, mirrors the
request side): `rest.NewRequiredResponseHeaderParam[Resp]`/
`NewOptionalResponseHeaderParam[Resp]` (+ Cookie equivalents) declare BOTH
the spec `ResponseHeaderParam`/`ResponseCookieParam` AND a
`codex.FieldCodec[Resp]` merge field, keyed on `Resp` instead of `Req`.
`RouteHandle.ResponseHeaderMergeFields()`/`ResponseCookieMergeFields()`
collect them; `RouteHandle.DecodeMergedResponse(body, headers, cookies) (Resp, error)`
is the response-direction mirror of `DecodeMerged`. `adapters/nethttp`/
`adapters/chi`'s `Handler` encode them into the actual HTTP response
headers/cookies automatically after the handler returns (no
`WithResponseHeaders`/`WithResponseCookies` call needed for struct-modeled
fields — those calls remain the escape hatch for anything else);
`nethttp.Call` decodes them back into the client's `Resp` automatically.

**`nethttp.CallHandle[Req, Resp]`** — single-call client convenience:
derives `vars`/`QueryParams`/`HeaderParams`/`CookieParams` from `req`
automatically via the role-aware accessors + `codex.EncodeVars`, then
delegates to `Call`. Explicit `opts.QueryParams`/`HeaderParams`/
`CookieParams` entries win over the derived value for the same key. `Call`
remains the lower-level escape hatch.

New error types: `codex.VarEncodeTypeError{Field, Got}`,
`ports.FilePathMismatchError{Template, Path}` (mirrors
`mqtt.TopicMismatchError`), `rest.MergeFieldTypeError{Err}` (mirrors
`FormatOptError`, returned by `Route.Register` on a merge-field/`Req`/`Resp`
type mismatch — reused for both request- and response-side merge fields).

**Phase 2 SHIPPED**: `events.NewTopicParam[T]`/`ChannelHandle.MergeFields()`/
`DecodeMerged(payload, topicVars) (T, error)` — events has exactly ONE var
destination (topic), so a single flat merge-field slice is always safe for
both directions (no role-aware split needed, unlike REST).
`adapters/mqtt5.TopicVarsFromMessage[T]` (new prerequisite — mirrors
`adapters/mqtt`'s existing v3 version) + `Subscribe` auto-merge wiring +
`mqtt5.PublishHandle[T]` (single-call convenience, mirrors
`nethttp.CallHandle`). `reqreply.NewTopicParam[T]` (Req-side only —
reqreply shares ONE topic template for both directions; the reply is
correlated by the transport, not by re-encoding topic vars into `Resp`) +
`RouteHandle.MergeFields()`/`DecodeMerged` + `adapters/mqtt5.Serve` merge
wiring + new `mqtt5.CallHandle[Req,Resp]`/`zeromq.CallHandle[Req,Resp]`.
`ports.NewCacheKeyParam[T]`/`Cache.MergeFields()` — simplest boundary, no
role symmetry, no `DecodeMerged`/single-call wrapper needed (`Cache`
adapters already take value+vars together at each call site).

**`adapters/zeromq` limitation (by design, not a gap)**: ZMQ REQ/REP
routing is socket-based — `Serve`'s incoming messages carry NO per-message
topic string to extract vars FROM (unlike MQTT's broker-routed topics), so
zeromq has NO server-side decode-merge equivalent. `zeromq.CallHandle` is
client-side only; the resolved topic is used solely for codec validation
and observer reporting (matches `CallOptions.Vars`'s existing documented
behavior).

New error types: `events.MergeFieldTypeError{Err}`/`reqreply.MergeFieldTypeError{Err}`
(both mirror `rest.MergeFieldTypeError` exactly).

### Nullable Codec

Wraps any codec to handle pointer fields (`*T`). `nil` encodes as JSON null.
The generated schema inherits the inner schema and sets `nullable: true`.

```go
// Codec[*string] — accepts nil (null) or a string value
var NoteCodec = codex.Nullable(codex.String())
```

### Bytes Codec

Encodes `[]byte` as a base64 standard-encoded string.
Schema: `{type:string, format:byte}`.

```go
var AvatarCodec = codex.Bytes()
```

### StringMap Codec

Encodes `map[string]V` where all values share the same codec.
Schema: `{type:object, additionalProperties:{...valueSchema}}`.

```go
var TagsCodec = codex.StringMap(codex.String())         // map[string]string
var CountsCodec = codex.StringMap(codex.Int())          // map[string]int
```

### Map[K, V] Codec

Encodes `map[K]V` where **key codec** validates and transforms map keys, and **value codec** handles values. Key codec must encode `K` to a `string` (JSON/YAML require string keys). Key validation errors surface as `KeyError{Key, Err}`. Schema: `{type:object, propertyNames:{...keySchema}, additionalProperties:{...valueSchema}}`.

```go
var sensorIDCodec = codex.String().
    Refine(validate.Pattern(regexp.MustCompile(`^[a-z]+-\d+$`))).
    WithTitle("SensorID")
var sensorsCodec = codex.Map[string, float64](sensorIDCodec, codex.Float64())
// Encode: validates each key against sensorIDCodec; returns KeyError for invalid keys.
// Schema: {type:object, propertyNames:{type:string,title:SensorID,pattern:...}, additionalProperties:{type:number}}
```

`StringMap[V]` stays as the zero-overhead variant when no key validation is needed.

### Optional Field in Object

Set `Required: false` on the field. The field is omitted from the encoded object when missing during decode; no error is returned.

## Validation

- `validate/` contains reusable `Constraint[T]` factory functions.
- `int` constraints: `PositiveInt`, `NegativeInt`, `NonZeroInt`, `MinInt(n)`, `MaxInt(n)`, `RangeInt(min, max)`.
- `int32` constraints: `PositiveInt32`, `NegativeInt32`, `MinInt32(n)`, `MaxInt32(n)`, `RangeInt32(min, max)`.
- `int64` constraints: `PositiveInt64`, `NegativeInt64`, `MinInt64(n)`, `MaxInt64(n)`, `RangeInt64(min, max)`.
- `uint` constraints: `PositiveUint`, `MinUint(n)`, `MaxUint(n)`, `RangeUint(min, max)`. No `NegativeUint` — unsigned type.
- `uint64` constraints: `PositiveUint64`, `MinUint64(n)`, `MaxUint64(n)`, `RangeUint64(min, max)`.
- Float constraints: `PositiveFloat`, `NegativeFloat`, `NonZeroFloat`, `MinFloat(n)`, `MaxFloat(n)`, `RangeFloat(min, max)`.
- `time.Duration` constraints: `PositiveDuration`, `NonNegativeDuration`, `MinDuration(d)`, `MaxDuration(d)`. No schema annotation (no JSON Schema standard for duration bounds).
- String constraints: `NonEmptyString`, `MinLen(n)`, `MaxLen(n)`, `Pattern(re)`, `OneOf(values...)`.
- Numeric string constraints (for path/topic variables): `IntString` (valid signed integer), `PositiveIntString` (> 0), `NonNegativeIntString` (≥ 0), `IntStringInRange(min, max)` (bounded). No schema annotation. Designed for use in `PathParamCodecs`/`TopicParamCodecs`.
- Protocol path/topic constraints: `MQTTTopic` (non-empty, no null byte, max 65535 UTF-8 bytes), `MQTTPublishTopic` (same + no `+`/`#` wildcards), `HTTPPath` (must start with `/`, no spaces or null bytes, OpenAPI-style `{param}` allowed). None carry schema annotations (no JSON Schema standard keywords for these rules).
- Format constraints: `Email`, `UUID`, `URL`, `URLWithSchemes(schemes...)`, `URI`, `Hostname`, `IPv4`, `IPv6`, `IP`, `Date`, `Time`, `DateTime`, `SemVer`, `Slug`, `CIDR`.
- Byte-size constraints: `MaxBytes(n)`, `MinBytes(n)` — validate decoded `[]byte` length; no schema annotation (JSON Schema has no standard keyword for decoded-byte-count limits).
- Constraints in `validate/` must not depend on any specific codec; they depend only on `codex.Constraint[T]` and `schema.Schema`.
- All built-in `validate/` constraints carry a `Schema` transformer that annotates the codec's schema automatically when applied via `Refine`, **except** `MaxBytes`/`MinBytes` and Duration constraints (runtime-only).

## OpenAPI Schema Rendering

The `render/openapi` package converts `schema.Schema` into OpenAPI 3.x schema objects. It delegates to the shared `render/internal/schemarender` package — no codec logic, no wire format.

The shared `render/internal/schemarender.SchemaObject(s schema.Schema) map[string]any` function handles all schema fields including `Nullable`, `AdditionalProperties`, `AdditionalPropertiesSchema`, `Discriminator`, `OneOf`, numeric bounds, string constraints, and enum. Both `render/openapi` and `render/asyncapi` use it; adding a new `schema.Schema` field requires updating only `schemarender`.

When `AdditionalPropertiesSchema` is set on a `schema.Schema`, it renders as a schema object (`additionalProperties: {type: ...}`). This takes precedence over the boolean `AdditionalProperties` field. Used by `StringMap[V]` and `Map[K, V]` codecs. When `PropertyNames` is set, it renders as `propertyNames: {...}` — used by `Map[K, V]` to express key format constraints.

```go
// SchemaObject converts s to an OpenAPI 3.x schema object (map[string]any).
func SchemaObject(s schema.Schema) map[string]any

// ComponentsSchemas produces the map for components.schemas in an OpenAPI doc.
func ComponentsSchemas(named map[string]schema.Schema) map[string]any

// MarshalJSON renders named schemas as JSON bytes.
func MarshalJSON(named map[string]schema.Schema) ([]byte, error)

// MarshalYAML renders named schemas as YAML bytes.
func MarshalYAML(named map[string]schema.Schema) ([]byte, error)
```

```go
// Good example — render OpenAPI schemas from codecs
yamlBytes, err := openapi.MarshalYAML(map[string]schema.Schema{
    "User": UserCodec.Schema,
    "Order": OrderCodec.Schema,
})
```

- The renderer is a pure function over `schema.Schema` — it never touches `Codec[T]` or any codec logic.
- Constraint annotations (`MinLength`, `Minimum`, `Pattern`, `Enum`, etc.) flow from `Refine` automatically when using `validate.*` constraints.
- Set `Constraint.Schema` on custom constraints to opt into schema annotation.

## HTTP Route Descriptors (`route/`)

The `route` package describes HTTP operations without any renderer or codec logic. It imports only `schema`.

```go
// Route describes a single HTTP operation.
type Route struct {
    Method, Path, OperationID, Summary, Description string
    Tags         []string
    PathParams   []Param
    QueryParams  []Param
    CookieParams []Param
    HeaderParams []Param
    RequestBody  *Body
    Responses    []Response
}

// Body describes a request body.
// SchemaName non-empty → renderer emits $ref and registers Schema in components/schemas.
type Body struct {
    Description string
    Required    bool
    Schema      schema.Schema
    SchemaName  string
    ContentType string // defaults to "application/json"
}

// Response describes one HTTP response.
// Status is a string: "200", "201", "default", "2XX", etc.
// Schema nil → description-only response (e.g. 204, 404 without body).
type Response struct {
    Status      string
    Description string
    Schema      *schema.Schema
    SchemaName  string
    ContentType string // defaults to "application/json"
}
```

- `route` is purely a data descriptor — no HTTP server logic, no encoding.
- Use codec schemas (`UserCodec.Schema`) as `Body.Schema` / `Response.Schema`.

## Full OpenAPI 3.1 Document (`render/openapi`)

In addition to `SchemaObject`/`ComponentsSchemas`/`MarshalYAML`, `render/openapi` provides `DocumentBuilder` for emitting a full 3.1 spec.

```go
// NewDocumentBuilder returns a builder for a full OpenAPI 3.1 document.
func NewDocumentBuilder(info Info) *DocumentBuilder

// Build validates routes and produces a Document. Returns error on:
// - duplicate (method, path) pair
// - PathParam name not matching a {placeholder} in the path (or vice versa)
func (b *DocumentBuilder) Build() (Document, error)

func (d Document) MarshalJSON() ([]byte, error)
func (d Document) MarshalYAML() ([]byte, error)
```

Key rules:

- `render/openapi` imports `route` and `schema`. No codec logic.
- Path parameters are always `required: true` in the output (OpenAPI 3.1 requirement).
- `Body.SchemaName != ""` → `$ref` emitted + schema auto-registered in `components/schemas`.
- `Response.Schema == nil` → no `content` block (correct for 204, no-body errors).
- Existing `SchemaObject`, `ComponentsSchemas`, `MarshalJSON`, `MarshalYAML` remain unchanged.

## AsyncAPI 2.6 Document (`render/asyncapi/v2`)

`render/asyncapi/v2` produces a full AsyncAPI 2.6 document. It imports only `schema`.

```go
// NewDocumentBuilder returns a builder for a full AsyncAPI 2.6 document.
func NewDocumentBuilder(info Info) *DocumentBuilder

// Build validates channels (each must have at least one operation) and produces a Document.
func (b *DocumentBuilder) Build() (Document, error)

func (d Document) MarshalJSON() ([]byte, error)
func (d Document) MarshalYAML() ([]byte, error)
```

Key types:

```go
type ChannelItem struct {
    Description string
    Parameters  map[string]Parameter // {varName} → Parameter; auto-populated by api/events builder
    Subscribe   *Operation           // app receives
    Publish     *Operation           // app sends
}

type Parameter struct {
    Description string
    Schema      schema.Schema // zero-value → default {type: string} in spec output
}

type Operation struct {
    Summary, Description string
    Tags    []string
    Message Message
}

type Message struct {
    Name        string
    Schema      schema.Schema
    SchemaName  string // non-empty → $ref in payload + auto-registered in components/schemas
    ContentType string
}
```

Key rules:

- `render/asyncapi/v2` imports only `schema` — channels are independent of HTTP route concepts.
- `Message.SchemaName != ""` → `$ref` in `message.payload` + schema auto-registered.
- `Message.Schema` zero-value with empty `SchemaName` → empty payload `{}` inline.
- `ChannelItem.Parameters` non-empty → `parameters:` block emitted in spec. Schema zero-value → `{type: string}`.
- Each channel must have at least one of `Subscribe` or `Publish`; `Build()` rejects channels with neither.
- AsyncAPI 3.0 upgrade path: isolate version-specific serialisation so a v3 variant can be added as `render/asyncapi/v3` without breaking 2.6.

## REST API Builder (`api/rest`)

`api/rest` is a transport-agnostic REST API builder layered on top of `render/openapi`. It imports **no HTTP library**. Users receive typed `Decode`/`Encode` helpers per route; they wire those into any HTTP framework.

```go
// NewBuilder returns a Builder for REST route registration.
// opts are applied in order; use WithPathCodec or WithPathConstraints to validate paths.
func NewBuilder(info Info, opts ...BuilderOption) *Builder

// WithPathCodec sets a codec used to validate every path registered via Register.
// If validation fails, Register returns an InvalidPathError immediately.
func WithPathCodec(c codex.Codec[string]) BuilderOption

// WithPathConstraints is a convenience wrapper: builds codex.String() refined with cons
// and delegates to WithPathCodec.
func WithPathConstraints(cons ...codex.Constraint[string]) BuilderOption

// NewRoute constructs a Route value (not yet registered). Call .Register(b) to register.
// Route[Req, Resp] is a value type — construct, pass around, register.
// Register returns (RouteHandle, error); returns InvalidPathError immediately if path codec validation fails.
func NewRoute[Req, Resp any](
    method, path string,
    reqCodec codex.Codec[Req],
    respCodec codex.Codec[Resp],
    opts ...RouteOpt,
) Route[Req, Resp]

func (r Route[Req, Resp]) Register(b *Builder) (*RouteHandle[Req, Resp], error)

// InvalidPathError is returned by Register when path codec validation fails.
// Use errors.As to extract it and inspect Path or the underlying constraint Err.
type InvalidPathError struct {
    Path string
    Err  error
}

// OpenAPISpec builds a full OpenAPI 3.1 document from all registered routes.
// Returns an error if there are dangling $refs.
func (b *Builder) OpenAPISpec() (openapi.Document, error)
```

Example — enforce HTTP path format:

```go
import "github.com/DaniDeer/go-codex/validate"

b := rest.NewBuilder(info, rest.WithPathConstraints(validate.HTTPPath))

createUser, err := rest.NewRoute[CreateUserReq, User]("POST", "/users", reqCodec, respCodec,
    rest.RouteMeta{OperationID: "createUser"},
).Register(b)
if err != nil {
    // err is an InvalidPathError — path failed validation immediately
    var pathErr rest.InvalidPathError
    errors.As(err, &pathErr) // pathErr.Path, pathErr.Err available
    return err
}
```

`RouteHandle[Req, Resp]`:

- `Descriptor route.Route` — live descriptor; updated by `WithRequestFormats` / `WithFormats`; use for framework routing and spec generation
- `Decode(body []byte) (Req, error)` — JSON decode + Refine validation
- `Encode(resp Resp) ([]byte, error)` — JSON encode
- `BuildPath(vars map[string]string) (string, error)` — substitutes `{varName}` placeholders in the path template, validating each against its `PathParam.Codec`. After substitution, if a builder-level `pathCodec` is set, the final assembled path is re-validated against it (no template stripping — this is the real path). Returns `MissingPathVarError` for missing variables, `PathParamError` for per-variable codec failures, `InvalidPathError` if the final path fails the builder codec. Extra keys in `vars` are silently ignored.

`PathParamError` is returned by `BuildPath` when a path variable fails its codec:

```go
type PathParamError struct {
    Name  string // the {varName} that failed
    Value string // the value that was rejected
    Err   error  // the underlying codec error
}
```

`MissingPathVarError` is returned by `BuildPath` when a template variable has no entry in `vars`:

```go
type MissingPathVarError struct {
    Name string // the variable name (without braces) that had no value
}
```

`InvalidPathParamError` is returned by `Register` when a `PathParams` entry names a variable not in the path template:

```go
type InvalidPathParamError struct {
    Name string // the variable name (without braces) not found in the template
    Path string // the path template that was validated against
}
```

`RouteOpt` values are passed as variadic trailing args to `NewRoute` and `NewSSERoute`. Available options:

- `RouteMeta{OperationID, Summary, Description, Tags, ReqSchemaName, RespStatus, RespDescription, RespSchemaName}` — route-level metadata.
- `PathParam{Name, Description, Codec *codex.Codec[string]}` — pass directly.
- `QueryParam{Name, Description string, Required bool, Codec *codex.Codec[string]}` — pass directly.
- `CookieParam{Name, Description string, Required bool, Codec *codex.Codec[string]}` — pass directly.
- `HeaderParam{Name, Description string, Required bool, Codec *codex.Codec[string]}` — pass directly.
- `ResponseHeaderParam{Name, Description string, Required bool, Codec *codex.Codec[string]}` — pass directly.
- `ResponseCookieParam{Name, Description string, Required bool, Codec *codex.Codec[string]}` — pass directly.
- `ResponseMeta{Status, Description string}` — extra responses beyond the primary success response.

`PathParam{Name, Description, Codec *codex.Codec[string]}` — optional per-variable metadata. `Name` must correspond to a `{varName}` placeholder in the path template. `Codec` (pointer, `nil` = no validation) provides runtime validation and auto-flows its schema into the OpenAPI spec. An unknown `Name` causes `Register` to return `InvalidPathParamError` immediately.

`QueryParam{Name, Description string, Required bool, Codec *codex.Codec[string]}` — optional query parameter metadata. `Name` is the query key (no template syntax). `Codec` (pointer, `nil` = no validation) provides runtime validation via `RouteHandle.ValidateQuery` and auto-flows its schema into the OpenAPI spec. Unlike `PathParam`, query params are not auto-generated for template placeholders — only entries explicitly listed in `QueryParams` appear in the spec.

```go
type QueryParam struct {
    Name        string
    Description string
    Required    bool
    Codec       *codex.Codec[string] // nil = no validation
}
```

`RouteHandle.ValidateQuery(params map[string]string) error` — validates each `QueryParam` with a non-nil `Codec` against the provided map. When `Required: true` and the key is absent, returns `QueryParamError{Err: ErrRequiredParam}`. Optional missing keys are silently skipped. Returns `QueryParamError` on first failure. `SSERouteHandle` mirrors the same method.

`RouteHandle.ValidateQueryMulti(params map[string][]string) error` — same as `ValidateQuery` but accepts the multi-value map returned by `r.URL.Query()`. Validates the first value per key. Use when handling repeated query keys (`?tags=a&tags=b`). Called by the adapter when `Options.MultiValueQueryParams` is true.

```go
type QueryParamError struct {
    Name  string
    Value string
    Err   error // wrapped codec validation error
}
```

`CookieParam{Name, Description string, Required bool, Codec *codex.Codec[string]}` — optional cookie parameter metadata. Follows the same pattern as `QueryParam`. `Codec` provides runtime validation via `RouteHandle.ValidateCookies` and auto-flows its schema into the OpenAPI spec (`in: cookie`).

```go
type CookieParam struct {
    Name        string
    Description string
    Required    bool
    Codec       *codex.Codec[string] // nil = no validation
}

type CookieParamError struct {
    Name  string
    Value string
    Err   error
}
```

`HeaderParam{Name, Description string, Required bool, Codec *codex.Codec[string]}` — optional HTTP header parameter metadata. Follows the same pattern as `QueryParam`. `Codec` provides runtime validation via `RouteHandle.ValidateHeaders` and auto-flows its schema into the OpenAPI spec (`in: header`). Do **not** declare `Accept`, `Content-Type`, or `Authorization` as `HeaderParam` entries — OpenAPI reserves these for `requestBody` and security schemes respectively.

```go
type HeaderParam struct {
    Name        string
    Description string
    Required    bool
    Codec       *codex.Codec[string] // nil = no validation
}

type HeaderParamError struct {
    Name  string
    Value string
    Err   error
}

// UnsupportedMediaTypeError — returned by the adapter when Content-Type does not match expected.
type UnsupportedMediaTypeError struct {
    Got      string // actual Content-Type (without parameters)
    Expected string // configured expected type (default "application/json")
}

// BodyTooLargeError — returned by the adapter when body exceeds Options.MaxBodyBytes.
type BodyTooLargeError struct {
    Limit int64 // configured byte limit
}

// NotAcceptableError — returned by the adapter when client's Accept header has no match.
type NotAcceptableError struct {
    Accept    string   // client's Accept header value
    Supported []string // content types the route can produce
}
```

`ResponseHeaderParam{Name, Description string, Required bool, Codec *codex.Codec[string]}` — declares an outgoing response header. Symmetric to `HeaderParam` but for the server side. Codec is validated by the adapter **after** the handler returns; a violation returns `ResponseHeaderParamError` and the adapter responds with 500 (server contract violation). Schema auto-flows into `responses[status].headers` in the OpenAPI spec. Pass directly as a `RouteOpt` to `NewRoute`.

`ResponseCookieParam{Name, Description string, Required bool, Codec *codex.Codec[string]}` — declares a `Set-Cookie` header returned in the primary success response. Same flow as `ResponseHeaderParam` but for cookies. The adapter validates cookie values via `ValidateResponseCookies` after the handler returns. The handler deposits cookies via `WithResponseCookies(ctx, ...PendingCookie)`. A codec violation returns `ResponseCookieParamError` and adapter responds with 500. Schema flows into `responses[status].headers["Set-Cookie"]` in spec (OpenAPI 3.1 has no first-class response cookie object). Pass directly as a `RouteOpt` to `NewRoute`.

```go
type ResponseHeaderParam struct {
    Name        string
    Description string
    Required    bool
    Codec       *codex.Codec[string] // nil = no validation
}

type ResponseHeaderParamError struct {
    Name  string
    Value string
    Err   error
}

type ResponseCookieParam struct {
    Name        string
    Description string
    Required    bool
    Codec       *codex.Codec[string] // nil = no validation
}

type ResponseCookieParamError struct {
    Name  string
    Value string
    Err   error
}
```

**Codec schema → spec**: `PathParam.Codec` schema automatically flows into the OpenAPI path parameter spec. `QueryParam.Codec`, `CookieParam.Codec`, and `HeaderParam.Codec` schemas automatically flow into their respective OpenAPI parameter specs (`in: query`, `in: cookie`, `in: header`). `ResponseHeaderParam.Codec` schema flows into `responses[status].headers`. `ResponseCookieParam.Codec` flows into `responses[status].headers["Set-Cookie"]`. When `Codec` is nil, the parameter is still declared (minimal entry with no schema).

Key rules:

- `api/rest` encodes responses as JSON by default. To enable content negotiation, call `handle.WithFormats(formats...)` after `Register`; the adapter picks the format matching the client's `Accept` header (406 on mismatch via `rest.NotAcceptableError`).
- `route.Response.ContentTypes []string` — when non-empty, the OpenAPI renderer emits the schema under all listed content types in `responses[N].content`. Set automatically from `WithFormats` content types.
- Request body (`RequestBody`) is only added to the spec for `POST`, `PUT`, `PATCH`.
- The descriptor is updated live via `WithRequestFormats` and `WithFormats`; all mutations must happen before `OpenAPISpec()` is called.
- Path validation is **immediate**: if a `pathCodec` is set, `Register` returns `InvalidPathError` at call time. The route is not registered on failure.
- **Template-transparent validation**: before running the path codec, `{varName}` placeholders are replaced with the literal `x` (e.g. `"/users/{id}"` → `"/users/x"`). Constraints run on the structural shape of the path, not the template syntax. This means any path constraint — including ones that do not mention braces — works correctly on parameterised routes. The stored `Descriptor.Path` is always the original template.
- **Final path re-validation**: `BuildPath` re-validates the fully assembled path (e.g. `"/users/hello world"`) against the builder-level `pathCodec` after substitution. This catches values that pass their `PathParam.Codec` but violate the global path constraint (e.g. a space introduced by a loose param codec). Returns `InvalidPathError{Path: finalPath, Err: ...}`.
- `Info = openapi.Info` and `Server = openapi.Server` are type aliases to avoid drift.
- `api/rest` may import `codex`, `format`, `route`, `render/openapi`, `schema`. No `net/http`.
- `adapters/nethttp` wraps `RouteHandle` for `net/http`. It imports `api/rest`, `net/http`, `format`, and `stats`.
  - `Handler[Req,Resp](handle, fn, opts Options) http.Handler` — decodes body (POST/PUT/PATCH), calls fn, encodes response; instruments via `opts.Observer`.
  - `Options.ErrorHandler func(w, r, status, err)` — custom error response writer; default is JSON `{"error":"..."}`.
  - `Options.Observer stats.Observer` — receives `RecordRequest` and `RecordValidationError` events; defaults to `stats.NoopObserver`. Observer also type-asserts to `stats.TraceObserver` for distributed tracing spans.
  - `Options.MaxBodyBytes int64` — max request body size for POST/PUT/PATCH; 0 = default (1 MiB). Requests exceeding the limit are rejected with 413 Request Entity Too Large and a `rest.BodyTooLargeError`.
  - `Options.ContentType string` — expected `Content-Type` for body-bearing methods; default `"application/json"`. Wrong type → 415 Unsupported Media Type with a `rest.UnsupportedMediaTypeError`. Parameters (e.g. `; charset=utf-8`) are stripped before comparison.
  - `Options.MultiValueQueryParams bool` — when true, passes `r.URL.Query()` (`map[string][]string`) to `ValidateQueryMulti` instead of the flat single-value map. Use for repeated keys like `?tags=a&tags=b`.
  - `Register[Req,Resp](mux, handle, fn, opts Options)` — registers on `*http.ServeMux` via Go 1.22+ `"METHOD /path"` pattern.
  - `RequestFromContext(ctx) (*http.Request, bool)` — retrieves the underlying `*http.Request` for path params, headers, etc. Use `r.PathValue("id")` for Go 1.22+ path segments.
  - Non-body methods (GET/HEAD/DELETE): fn called with zero value of Req; body reader not touched.
  - **Content-Type check**: for POST/PUT/PATCH, `Content-Type` is checked before reading body; 415 on mismatch.
  - **Content negotiation**: when `handle.Formats` is non-empty, the adapter reads `Accept`, picks the matching format, encodes with it. No match → 406 `rest.NotAcceptableError`. `*/*` picks first format. The chosen format's `ContentType()` is set as the response `Content-Type` header.
  - **Query validation**: `ValidateQuery` is called automatically before the handler function. Codec-backed `QueryParam` entries are validated from `r.URL.Query()`; 400 is returned on failure. Use `ValidateQueryMulti` (via `Options.MultiValueQueryParams`) for repeated keys. When `Required: true` and the key is absent, returns a `QueryParamError` wrapping `ErrRequiredParam`.
  - **Cookie validation**: `ValidateCookies` is called automatically before the handler function. Codec-backed `CookieParam` entries are validated from `r.Cookies()`; 400 is returned on failure. When `Required: true` and the cookie is absent, returns a `CookieParamError` wrapping `ErrRequiredParam`.
  - **Header validation**: `ValidateHeaders` is called automatically before the handler function. Codec-backed `HeaderParam` entries are validated from `r.Header`; 400 is returned on failure. When `Required: true` and the header is absent, returns a `HeaderParamError` wrapping `ErrRequiredParam`. Observer reports with `location="cookie"` or `location="header"` respectively.
  - **Path param validation**: `ValidatePathParams` is called automatically before the handler function. The adapter extracts path variable values using the router's path extraction (`r.PathValue(name)` for net/http, `chi.URLParam(r, name)` for chi) for each name in `handle.PathParamNames()`, then validates each codec; 400 on failure. Returns `PathParamError` on codec violation. `PathParamNames() []string` returns the names of all registered `PathParam` entries.
  - **`ErrRequiredParam`**: sentinel error wrapped inside any param error when a required param (`Required: true`) is absent. Check with `errors.Is(err, rest.ErrRequiredParam)`. Applies to: `QueryParam`, `CookieParam`, `HeaderParam`, `ResponseHeaderParam`, `ResponseCookieParam` on both `RouteHandle` and `SSERouteHandle`.
  - **Response header validation**: `ValidateResponseHeaders` is called automatically after the handler function succeeds. Codec-backed `ResponseHeaderParam` entries are validated against collected response headers; 500 is returned on failure (server contract violation). Observer reports with `location="response_header"`.
  - **`WithResponseHeaders(ctx, h http.Header)`** — mutates the pre-allocated header map in `ctx` in-place; call from inside `HandlerFunc` to attach extra response headers. Returns nothing (void); maps are reference types. `ResponseHeadersFromContext(ctx) (http.Header, bool)` retrieves the collected headers (useful for testing/middleware).
  - **Response cookie validation**: `ValidateResponseCookies` is called automatically after the handler function succeeds. Codec-backed `ResponseCookieParam` entries are validated against collected cookie values; 500 is returned on failure. Observer reports with `location="response_cookie"`.
  - **`WithResponseCookies(ctx, cookies ...PendingCookie)`** — deposits `PendingCookie` values into `ctx`; call from inside `HandlerFunc` to queue `Set-Cookie` headers. The adapter validates values, then writes `Set-Cookie` headers on success. `ResponseCookiesFromContext(ctx) ([]PendingCookie, bool)` retrieves queued cookies (useful for testing/middleware).
  - **`PendingCookie{Name, Value string, Opts CookieOptions}`** — a cookie queued for response writing. `Opts` controls `Secure`, `HttpOnly`, `SameSite`, `MaxAge`, `Path`, `Domain`. `Opts.Codec` is cleared by the adapter before writing (route-level validation already ran via `ValidateResponseCookies`).
  - **`SetCookie(w, name, value, opts CookieOptions) error`** — writes a `Set-Cookie` header with secure defaults (`Secure=true`, `HttpOnly=true`, `SameSite=Strict`, `Path="/"`). If `opts.Codec` is non-nil, value is validated before writing; on failure returns `rest.CookieParamError` without writing the header. Use the same `*codex.Codec[string]` as the read-side `CookieParam` for symmetric validation.
    - `CookieOptions.Insecure bool` — omit `Secure` (for non-TLS, e.g. localhost dev)
    - `CookieOptions.AllowJS bool` — omit `HttpOnly` (for JS-readable cookies, e.g. CSRF tokens)
    - `CookieOptions.SameSite http.SameSite` — override; defaults to `SameSiteStrictMode`
    - `CookieOptions.MaxAge int` — 0 = session; negative = delete immediately
    - `CookieOptions.Path string` — defaults to `"/"`
    - `CookieOptions.Domain string` — defaults to current host
    - `CookieOptions.Codec *codex.Codec[string]` — optional write-side validator; returns `rest.CookieParamError` on failure without writing header
    - `CookieOptions.WithCodec(c codex.Codec[string]) CookieOptions` — fluent setter; avoids `&codec` boilerplate; mirrors `rest.*Param.WithCodec`
  - **`SSEHandler[Req,Event](handle *rest.SSERouteHandle[Req,Event], fn SSEHandlerFunc[Req,Event], opts Options) http.Handler`** — SSE streaming handler. Sets `Content-Type: text/event-stream`, `Cache-Control: no-cache`, `Connection: keep-alive`. Calls `fn`; the `send` func validates each event via the codec before writing `data: <json>\n\n` and flushing. **First-send commit**: on the first `send()` call, staged response headers and cookies (set via `SetResponseHeader`/`WithResponseCookies` in the handler) are validated and written to the wire before any data frame — after this point headers cannot change. **Accept negotiation**: when `handle.Formats` is non-empty, the adapter reads the `Accept` header and calls `negotiateFormat`; `text/event-stream` and `*/*` fall back to `Formats[0]`. Any other Accept value that doesn't match a registered format returns 406. **Path param validation** + **Required enforcement** + **query/cookie/header validation** match the regular `Handler` — see the adapter validation bullets above.
  - **Pipeline error-channel status mapping**: `PipelineHandler`/`RegisterPipeline` honor route-declared `rest.ErrorStatus[E](status)` mappings (first match wins via `errors.As`). Unmatched pipeline errors remain 500. `PipelineNoResponseError` defaults to 503 unless explicitly remapped by a route rule. `rest.ErrorPattern[E,B](status, codec, ...mapFn) ErrorPatternOpt[E,B]` additionally declares a codec-backed typed error response body (direct or mapped payload) that adapters emit directly on match, ahead of `Options.ErrorHandler`. `ErrorPatternOpt[E,B].WithAction(rest.ErrorAction)` selects the realized action — `rest.ErrorRespond` (default: auto-write typed body) or `rest.ErrorHandle`/`rest.ErrorLog` (skip auto-write, fall through to `Options.ErrorHandler` using the pattern's declared status; both behave identically for REST — kept as distinct values for vocabulary parity with `events.ErrorAction`/`websocket.ErrorFrame`). `ErrorPatternResponse.Action` carries the resolved action; `adapters/nethttp` and `adapters/chi` both check `patternResp.Action == "" || patternResp.Action == rest.ErrorRespond` before auto-writing.
  - **No-pipeline status ergonomics**: plain `Handler`/`Register` has no route-level typed error-status map; map domain errors in `Options.ErrorHandler` (or keep default adapter statuses). Keep `ErrorHandler` focused on body/envelope shaping for consistency across both no-pipeline and pipeline routes.
  - **`RegisterSSE[Req,Event](mux, handle, fn, opts Options)`** — registers the SSE handler on `*http.ServeMux` under `GET <path>`.
  - **`SSEHandlerFunc[Req,Event]`** = `func(ctx context.Context, req Req, send func(Event) error) error` — typed handler signature for SSE. `send` returns error if codec rejects the event; no bytes are written. Honour `ctx.Done()` for clean disconnect handling.

- `adapters/chi` wraps `RouteHandle` for `github.com/go-chi/chi/v5`. It has the same API surface as `adapters/nethttp` with one key difference: path variables are extracted via `chi.URLParam(r, "name")` instead of `r.PathValue("name")`. Chi uses the same `{param}` placeholder syntax as go-codex path templates.
  - `Handler[Req,Resp](handle, fn, opts Options) http.HandlerFunc` — same pipeline as nethttp.
  - `Register[Req,Resp](r gochi.Router, handle, fn, opts Options)` — calls `r.Method(method, path, handler)`.
  - `SSEHandler[Req,Event](handle, fn, opts Options) http.HandlerFunc` — SSE streaming handler; same contract as `nethttp.SSEHandler`.
  - `RegisterSSE[Req,Event](r gochi.Router, handle, fn, opts Options)` — calls `r.Get(path, handler)`.
  - `RequestFromContext(ctx) (*http.Request, bool)` — retrieve request for `chi.URLParam(r, "id")`.
  - All validation, response header/cookie, content negotiation, SSE features are identical to `adapters/nethttp`.
  - Observer also type-asserts to `stats.TraceObserver` for distributed tracing spans (same as nethttp).
  - `SetCookie`, `CookieOptions`, `PendingCookie`, `WithResponseCookies`, `WithResponseHeaders` all present with identical signatures.

- `adapters/templ` is a plug-in for the templ SSR library (`github.com/a-h/templ`). It does **not** implement an HTTP adapter — it produces a `format.Format[Props]` value that participates in the existing content negotiation pipeline of `adapters/nethttp` and `adapters/chi`.
  - `Format[Props](c codex.Codec[Props], component func(Props) atempl.Component) format.Format[Props]` — wraps a templ component as a `format.Format` with `ContentType: "text/html; charset=utf-8"`. Add it to a route's `Formats`.
  - `StreamingFormat[Props](c codex.Codec[Props], component func(Props) atempl.Component) format.Format[Props]` — streaming variant built with `format.NewStreamed`; renders directly to `ResponseWriter` without buffering.
  - `DecodeNotSupportedError{ContentType string}` — returned by the format's `Unmarshal`; HTML cannot be decoded back to a typed value. Use `errors.As` to detect it.
  - Props are validated via the codec's `Refine` constraints before the component renders. Validation failure → HTTP 500 via the hosting adapter; the component is never called with invalid data.
  - The component receives `context.Background()` during rendering; pass all data the component needs through the Props struct.
  - Works with both `adapters/nethttp` and `adapters/chi` — no chi-specific variant needed.

  **Composability with SSE (HTMX HTML-over-the-wire):** `adapttempl.Format` can be passed as an `EventFormat` to `rest.NewSSERoute`. Each SSE `data:` line contains a rendered HTML fragment — events with invalid props are rejected before the component renders. `adapttempl.StreamingFormat` can be used as a `ResponseFormat` on any regular route for chunked HTML delivery. See `examples/adapters-streaming-sse-templ` for both patterns together.

  ```go
  import (
      adapttempl "github.com/DaniDeer/go-codex/adapters/templ"
      nethttp    "github.com/DaniDeer/go-codex/adapters/nethttp"
      atempl     "github.com/a-h/templ"
      "github.com/DaniDeer/go-codex/format"
  )

  // Define a templ component (or use a real templ-generated one):
  func ArticleCard(p ArticleProps) atempl.Component {
      return atempl.ComponentFunc(func(ctx context.Context, w io.Writer) error {
          _, err := fmt.Fprintf(w, "<h2>%s</h2>", html.EscapeString(p.Title))
          return err
      })
  }

  // Register both formats on one route:
  articleRoute, _ := rest.NewRoute[ArticleReq, ArticleProps]("GET", "/article",
      articleReqCodec, articlePropsCodec,
      rest.RouteMeta{},
  ).Register(b)
  articleRoute = articleRoute.WithFormats(
      adapttempl.Format(articlePropsCodec, ArticleCard), // Accept: text/html
      format.JSON(articlePropsCodec),                     // Accept: application/json
  )

  // One handler, one registration — adapter handles content negotiation:
  nethttp.Register(mux, articleRoute, func(ctx context.Context, req Req) (ArticleProps, error) {
      return svc.GetArticle(ctx, req.ID)
  }, nethttp.Options{})
  ```

- `StreamingFormat[Props](c codex.Codec[Props], component func(Props) atempl.Component) format.Format[Props]` — streaming variant that renders directly to `ResponseWriter` via `format.NewStreamed`, bypassing the intermediate `bytes.Buffer`. Use when you want true chunked delivery of large HTML responses.

### `adapters/nethttp` + `adapters/chi` — SSE (Server-Sent Events)

Both adapters expose `SSEHandler` and `RegisterSSE` for streaming Server-Sent Events from a `rest.SSERouteHandle`.

```go
import (
    nethttp "github.com/DaniDeer/go-codex/adapters/nethttp"
    "github.com/DaniDeer/go-codex/api/rest"
)

// Register an SSE route — always GET.
sensorRoute, err := rest.NewSSERoute[struct{}, sensorReading](
    "/sensors/{id}/readings",
    codex.Empty, sensorReadingCodec,
    rest.RouteMeta{OperationID: "streamSensor"},
    rest.PathParam{Name: "id", Description: "Sensor ID", Codec: &sensorIDCodec},
).Register(b)

// Wire onto net/http.
nethttp.RegisterSSE(mux, sensorRoute,
    func(ctx context.Context, _ struct{}, send func(sensorReading) error) error {
        r, _ := nethttp.RequestFromContext(ctx)
        id := r.PathValue("id")
        for {
            select {
            case <-ctx.Done():
                return nil // client disconnected
            default:
            }
            if err := send(svc.Read(id)); err != nil {
                return err // codec rejected value — no bytes written
            }
            time.Sleep(time.Second)
        }
    }, nethttp.Options{Observer: obs})
```

Key contract rules:

- `send(event)` validates via the event codec → encodes to JSON → writes `data: <json>\n\n` → flushes. If validation fails, `send` returns an error without writing anything; the stream remains clean.
- `ctx.Done()` signals client disconnects; always respect it to avoid goroutine leaks.
- `SSERouteHandle.BuildPath(vars)` validates path variables via per-param codecs and the builder-level path codec — same contract as `RouteHandle.BuildPath`.
- `SSERouteHandle` accepts `ResponseHeaderParam` and `ResponseCookieParam` opts (same as `RouteHandle`). `ValidateResponseHeaders(map[string]string) error` / `ValidateResponseCookies(map[string]string) error` mirror the regular-route methods. Both adapters commit staged response headers/cookies on the **first** `send()` call (validate → write to wire) before any `data:` frame is emitted.
- `rest.NewRequiredSSEEventParam` / `rest.NewOptionalSSEEventParam` declare connection-var merge fields for the pushed event type; adapters call `SSERouteHandle.MergeEvent` automatically on every `send`, merging path/query/header/cookie values into the event struct before encode.
- `rest.NewSSERoute` accepts `...RouteOpt` as variadic trailing args (same as `NewRoute`). Configure event formats via `handle.WithFormats(fmts...)` after registration; the adapter uses the first format for event data serialisation (defaults to JSON when empty).
- The route appears in the OpenAPI spec as a GET operation with `Content-Type: text/event-stream`.
- Stats observer receives `RecordValidationError("response", constraint, "event")` for each rejected event.
- For chi: use `chiadapter.SSEHandler` / `chiadapter.RegisterSSE`; path vars via `chi.URLParam(r, "id")`.

`api/events` is a transport-agnostic event channel builder layered on top of `render/asyncapi`. It imports **no messaging library**. Users receive typed `Decode`/`Encode` helpers per channel; they wire those into any message broker.

```go
// NewBuilder returns a Builder for event channel registration.
// opts are applied in order; use WithTopicCodec or WithTopicConstraints to validate topics.
func NewBuilder(info Info, opts ...BuilderOption) *Builder

// WithTopicCodec sets a codec used to validate every topic registered via Register.
// If validation fails, Register returns an InvalidTopicError immediately.
func WithTopicCodec(c codex.Codec[string]) BuilderOption

// WithTopicConstraints is a convenience wrapper: builds codex.String() refined with cons
// and delegates to WithTopicCodec.
func WithTopicConstraints(cons ...codex.Constraint[string]) BuilderOption

// NewChannel constructs a Channel value (not yet registered). Call .Register(b) to register.
// Channel[T] is a value type — construct, pass around, register.
// Register returns (ChannelHandle, error); returns InvalidTopicError immediately if topic codec validation fails.
func NewChannel[T any](
    topic string,
    codec codex.Codec[T],
    opts ...ChannelOpt,
) Channel[T]

func (c Channel[T]) Register(b *Builder) (*ChannelHandle[T], error)

// InvalidTopicError is returned by Register when topic codec validation fails.
// Use errors.As to extract it and inspect Topic or the underlying constraint Err.
type InvalidTopicError struct {
    Topic string
    Err   error
}

// AsyncAPISpec builds a full AsyncAPI 2.6 document from all registered channels.
// Returns an error if there are dangling $refs.
func (b *Builder) AsyncAPISpec() (asyncapi.Document, error)
```

Example — enforce MQTT publish topic rules:

```go
import "github.com/DaniDeer/go-codex/validate"

b := events.NewBuilder(info, events.WithTopicConstraints(validate.MQTTPublishTopic))
// Use validate.MQTTTopic (without the publish restriction) for subscribe-only builders.

ch, err := events.NewChannel[MeasurementEvent]("sensors/+/data", codec,
    events.Subscribe{Summary: "Sensor data"},
).Register(b)
if err != nil {
    // err is an InvalidTopicError — topic failed validation immediately
    var topicErr events.InvalidTopicError
    errors.As(err, &topicErr) // topicErr.Topic, topicErr.Err available
    return err
}
```

`ChannelHandle[T]`:

- `Topic string`
- `Descriptor asyncapi.ChannelItem` — live descriptor; updated by `WithFormats`; use for spec generation
- `Decode(payload []byte) (T, error)` — JSON decode + Refine validation
- `Encode(msg T) ([]byte, error)` — JSON encode
- `BuildTopic(vars map[string]string) (string, error)` — substitutes `{varName}` placeholders in the topic template, validating each against its `TopicParam.Codec`. After substitution, if a builder-level `topicCodec` is set, the final assembled topic is re-validated against it (no template stripping). Returns `MissingTopicVarError` for missing variables, `TopicParamError` for per-variable codec failures, `InvalidTopicError` if the final topic fails the builder codec. Extra keys in `vars` are silently ignored.
- `WithFormats(fmts ...format.Format[T]) *ChannelHandle[T]` — sets default payload format for adapter use (subscribe decode + publish encode) **and** updates `Descriptor.Subscribe.Message.ContentType` / `Descriptor.Publish.Message.ContentType` to `fmts[0].ContentType()`. Calling with no arguments clears both. Changes are visible to `AsyncAPISpec()` since the builder holds a `*ChannelHandle` (live pointer). Call after `Register`.
- `WithSubscribeFormats(fmts ...format.Format[T]) *ChannelHandle[T]` — sets the subscribe (receive / decode) direction format only. Updates `SubscribeFormats` field and `Descriptor.Subscribe.Message.ContentType`. Does not affect publish direction. Use for asymmetric channels (e.g. YAML in, JSON out).
- `WithPublishFormats(fmts ...format.Format[T]) *ChannelHandle[T]` — sets the publish (send / encode) direction format only. Updates `PublishFormats` field and `Descriptor.Publish.Message.ContentType`. Does not affect subscribe direction.
- `ValidateTopic(topic string) error` — validates a received concrete topic string against the builder-level topic codec. Returns `InvalidTopicError` on failure; nil if no topic codec is registered. Call after a wildcard subscription delivers a message.
- `ValidateTopicVars(vars map[string]string) error` — validates extracted topic variable values against registered `TopicParam` codecs. Returns `MissingTopicVarError` if a key is absent from `vars` (topic vars are always required — unlike path vars where the adapter guarantees presence). Returns `TopicParamError` for the first variable that fails its codec. Call after `TopicVarsFromMessage` or directly on a vars map.

`TopicParamError` is returned by `BuildTopic` and `ValidateTopicVars` when a topic variable fails its codec:

```go
type TopicParamError struct {
    Name  string // the {varName} that failed
    Value string // the value that was rejected
    Err   error  // the underlying codec error
}
```

`MissingTopicVarError` is returned by `BuildTopic` when a template variable has no entry in `vars`:

```go
type MissingTopicVarError struct {
    Name string // the variable name (without braces) that had no value
}
```

`InvalidTopicParamError` is returned by `Register` when a `TopicParam` option names a variable not in the topic template:

```go
type InvalidTopicParamError struct {
    Name  string // the variable name (without braces) not found in the template
    Topic string // the topic template that was validated against
}
```

`ChannelOpt` values are passed directly to `NewChannel`. Available options:

- `ChannelMeta{Title, Summary, Description, Tags []string}` — channel-level metadata. All fields are optional and flow directly into the AsyncAPI spec `ChannelItem` (both v2 and v3).
- `Subscribe{OperationID, Summary, Description, Tags, SchemaName}` — subscribe operation metadata. `OperationID` is emitted as `operationId` in the AsyncAPI spec (used by codegen tools).
- `Publish{OperationID, Summary, Description, Tags, SchemaName}` — publish operation metadata. `OperationID` is emitted as `operationId` in the AsyncAPI spec (used by codegen tools).
- `TopicParam{Name, Description, Codec *codex.Codec[string]}` — optional per-variable metadata. `Name` must correspond to a `{varName}` placeholder in the topic template. `Codec` (pointer, `nil` = no validation) provides runtime validation and auto-flows its schema into the AsyncAPI `parameters:` block. An unknown `Name` causes `Register` to return `InvalidTopicParamError` immediately. Use `.WithCodec(c codex.Codec[string])` value-receiver to set the codec without pointer boilerplate: `TopicParam{Name: "id"}.WithCodec(uuidCodec)`.
- `ErrorChannel[E error, B any](topic string, codec codex.Codec[B], mapFn ...func(E) (B, error)) ErrorChannelOpt[E, B]` — declares a per-channel typed error pattern, the pub/sub analogue of `rest.ErrorPattern`: matches an upstream error via `errors.As`, then publishes a codec-backed payload (direct or mapped) to `topic`. Default action `events.ErrorRespond`; override with `.WithAction(events.ErrorHandle)` (adapter's existing `OnError`-style callback runs, no publish) or `.WithAction(events.ErrorLog)` (unchanged fallback, no publish). `ChannelHandle.ErrorResponseFor(err) (ErrorChannelResponse, bool, error)` is the lookup accessor; first declared matching rule wins. `adapters/mqtt5.PublishAdapter` calls this before its own `OnError` fallback. See `docs/roadmap/error-path-ergonomics.md`.
- **Store/IO boundaries (`adapters/sql`, `adapters/redis`, `adapters/file`) default to `handle`/`log` — NO new adapter API was added for Phase 1C.** These are internal boundaries with no caller to respond to. Every sink-side adapter's existing `OnError func(error)` (`sql.DrainInsertOptions.OnError`, `redis.SetAdapterOptions.OnError`, `file.DrainWriteAdapterOptions.OnError`/`DrainWriteFileAdapterOptions.OnError`) already realizes the `handle` action; leaving it nil is the `log` default (observer-only). A `respond`-equivalent (typed error payload publish) is achieved by COMPOSING that `OnError` callback with a declared `events.ErrorChannel` — call `errHandle.ErrorResponseFor(err)` inside `OnError` and publish `resp.Body` to `resp.Topic` when `resp.Action == events.ErrorRespond`. This is intentional reuse, not a missing feature — do not flag the absence of a `sql.ErrorChannel`/`redis.ErrorChannel`/`file.ErrorChannel` type as a gap. See `docs/guides/error-handling.md` "Store/IO boundaries" section and the composition tests in each adapter's `binding_test.go` (`TestDrainInsertAdapter_OnError_ComposesWithEventsErrorChannel`, `TestDrainSetAdapter_OnError_ComposesWithEventsErrorChannel`, `TestDrainWriteAdapter_OnError_ComposesWithEventsErrorChannel`).

**Codec schema → spec**: `TopicParam.Codec` schema automatically flows into the AsyncAPI channel `parameters:` block. For each `{varName}` in the topic template, a parameter entry is always emitted — using the codec schema when a `TopicParam.Codec` is set, or `{type: string}` as default. `TopicParam` is only needed to add a description or runtime validation.

Key rules:

- `api/events` uses `format.JSON(codec)` internally — explicitly JSON-only.
- The descriptor is built and frozen at `Register` call time.
- Topic validation is **immediate**: if a `topicCodec` is set, `Register` returns `InvalidTopicError` at call time. The channel is not registered on failure.
- **Template-transparent validation**: before running the topic codec, `{varName}` placeholders are replaced with the literal `x` (e.g. `"sensors/{sensorID}/data"` → `"sensors/x/data"`). Constraints run on the structural shape of the topic, not the template syntax. The stored `ChannelHandle.Topic` is always the original template.
- **Final topic re-validation**: `BuildTopic` re-validates the fully assembled topic against the builder-level `topicCodec` after substitution. Catches values that pass their `TopicParam.Codec` but violate the global topic constraint. Returns `InvalidTopicError{Topic: finalTopic, Err: ...}`.
- `Info = asyncapi.Info` and `Server = asyncapi.Server` are type aliases (where `asyncapi` now refers to `render/asyncapi/v3`).
- `api/events` may import `codex`, `format`, `route`, `render/asyncapi/v3`, `schema`. No messaging library.
- `adapters/mqtt` wraps `ChannelHandle` for Paho MQTT. It imports `api/events`, `stats`, and `github.com/eclipse/paho.mqtt.golang`.
  - `SubscribeHandler[T](ctx, handle, fn, opts SubscribeOptions, formats ...format.Format[T]) mqtt.MessageHandler` — decodes payload, calls fn, routes typed errors to `opts.OnError`; instruments via `opts.Observer` (`RecordSubscribe` + `RecordValidationError` for payload and topic errors). Format priority: call-time `formats` → `handle.Formats` → JSON fallback. `SubscribeError.Topic` reflects the concrete incoming message topic (`msg.Topic()`).
  - `SubscribeOptions{OnError func(SubscribeError), Observer stats.Observer}` — zero value is safe (nil `OnError` discards errors, nil `Observer` defaults to `NoopObserver`).
  - `MessageFromContext(ctx) (pahomqtt.Message, bool)` — retrieves the raw `pahomqtt.Message` stored in context by `SubscribeHandler`. Analogous to `nethttp.RequestFromContext`. Gives access to `Qos()`, `Retained()`, `MessageID()`, `Duplicate()` without breaking the typed handler signature. Returns false on a plain context.
  - `SubscribeError{Kind ErrorKind, Topic string, Err error}` — typed error; `Kind` is `KindDecode`, `KindHandler`, or `KindSecurity`.
  - `PublishEncodeError{Topic string, Err error}` — returned by `Publish` when encoding the outgoing payload fails (codec validation or marshal error). `Topic` is the concrete topic after template substitution. `Unwrap()` exposes the underlying error. Use `errors.As` to distinguish encode failures from topic-build errors or broker errors.
  - `Publish[T](ctx, client, handle, qos, retained, msg, vars map[string]string, opts PublishOptions, formats ...format.Format[T]) error` — unified publish: `nil` vars → use `handle.Topic` (static topics); non-nil vars → call `handle.BuildTopic(vars)`. Format priority: call-time `formats` → `handle.Formats` → JSON fallback. Instruments via `opts.Observer`: calls `RecordPublish(topic, success, duration)` on all exit paths; calls `RecordValidationError("payload", ...)` on encode errors; calls `RecordValidationError("topic_var", ...)` or `RecordValidationError("topic", ...)` on `BuildTopic` failures. Returns `TopicParamError` or `MissingTopicVarError` if `BuildTopic` fails; returns `PublishEncodeError` if payload encoding fails. Context-aware token wait.
  - `PublishOptions{Observer stats.Observer}` — zero value is safe (nil `Observer` defaults to `NoopObserver`). Observer also type-asserts to `stats.TraceObserver` for distributed tracing spans.
  - `TopicVarsFromMessage[T](handle, msg) (map[string]string, error)` — inverse of `BuildTopic`. Matches the concrete MQTT topic (`msg.Topic()`) against the channel's topic template, extracting `{varName}` values into the returned map. Template rules: `{varName}` captures one level; `+` matches one level (anonymous, not captured); `#` as last segment captures all remaining levels under key `"#"`. Applies full validation chain (symmetric with `BuildTopic`): (1) structural match → `TopicMismatchError{Template, Topic}`; (2) builder-level topic codec → `InvalidTopicError{Topic, Err}`; (3) per-param `TopicParam.Codec` validation → `TopicParamError{Name, Value, Err}`.
  - `TopicMismatchError{Template, Topic string}` — returned by `TopicVarsFromMessage` when the received topic does not match the template structure.

- `stats` — dependency-free observability package.
  - `ValidationObserver` — codec-level interface: `RecordValidationError(location, constraintName, field string)`. Implement this when using codecs directly (no adapter). The `location` is a user-chosen label (e.g. `"config"`, `"input"`).
  - `Observer` — adapter-level interface: embeds `ValidationObserver` + `RecordRequest`, `RecordSubscribe`, `RecordPublish`. Use this with `adapters/nethttp` and `adapters/mqtt`.
  - `TraceObserver` — optional additive interface (6th): `StartSpan(ctx, operation, name string) context.Context` starts a new trace span, returns context with span for child propagation; `EndSpan(ctx, err error)` ends the span, recording err when non-nil. Type-asserted by adapters (never embedded). No external OTel dependency.
  - `NoopObserver{}` — satisfies both interfaces, zero-cost default.
  - `ReportErrors(obs ValidationObserver, location string, err error)` — iterates `codex.ValidationErrors` from a decode error, calls `obs.RecordValidationError` per field. Codec-only users call this after `codec.Decode`.
  - `ConstraintName(err error) string` — extracts stable constraint label: `ConstraintError.Name`, `"type-mismatch"`, `"required"`, or `""`.
  - `location` values by adapter: `"body"` (nethttp/chi body decode/encode), `"query"` (nethttp/chi query), `"cookie"` (nethttp/chi request cookie), `"header"` (nethttp/chi request header), `"response_header"` (nethttp/chi response header), `"response_cookie"` (nethttp/chi response cookie), `"payload"` (mqtt payload), `"topic_var"` (mqtt per-variable codec failure), `"topic"` (mqtt topic-level codec or structural mismatch), user-defined string (codec-only).
  - `Registry.WithObserver` accepts `PipelineObserver`; the concrete observer is also type-asserted to `TraceObserver` for forge apply spans.

### Package import table (updated)

| Package              | Imports allowed from                                                  |
| -------------------- | --------------------------------------------------------------------- |
| `api/rest`           | `codex`, `format`, `route`, `render/openapi`, `schema`                |
| `api/events`         | `codex`, `format`, `route`, `render/asyncapi/v3`, `schema`            |
| `api/reqreply`       | `codex`, `format`, `render/asyncapi/v3`, `schema`                     |
| `api/mcp`            | `codex`, `render/jsonschema`                                          |
| `ports`              | `codex`, `stats`, `stream`, `format`, `api/rest`, `api/events`, `api/reqreply`, `api/mcp` — deliberately at top level (not under `forge`) to avoid an import cycle (`stream` imports `forge`) |
| `app`                | `stats` only (+ stdlib) — top-level lifecycle package; zero coupling to `ports`/`forge` (teardown registration is explicit `OnShutdown`, never inferred from ctx identity) |
| `adapters/nethttp`   | `api/rest`, `net/http` (stdlib), `route`, `stats`, `format`, `ports`, `stream` |
| `adapters/chi`       | `api/rest`, `net/http` (stdlib), `route`, `stats`, `format`, `ports`, `stream`, chi lib |
| `adapters/mqtt`      | `api/events`, `route`, `stats`, `ports`, `stream`, `github.com/eclipse/paho.mqtt.golang` |
| `adapters/mqtt5`     | `codex`, `api/events`, `api/reqreply`, `route`, `stats`, `ports`, `stream`, `github.com/eclipse/paho.golang` |
| `adapters/zeromq`    | `api/events`, `api/reqreply`, `format`, `stats`, `ports`, `stream` (no CGO — wrap a ZMQ lib in your app) |
| `adapters/mcpgo`     | `api/mcp`, `stats`, `ports`, `stream`, `github.com/mark3labs/mcp-go`  |
| `adapters/file`      | `format`, `ports`, `stats`, `stream` (stdlib only otherwise)          |
| `adapters/sql`       | `codex`, `stats`, `ports`, `stream`                                   |
| `adapters/redis`     | `ports`, `stats`, `stream`, `github.com/redis/go-redis/v9`            |
| `adapters/websocket` | `api/rest`, `ports`, `stats`, `stream`, `github.com/gorilla/websocket` |
| `adapters/templ`     | `codex`, `format`, `github.com/a-h/templ`                             |
| `stats`              | `codex`, `errors`, `time` (stdlib only)                               |
| `render/asyncapi/v3` | `schema`, `route`, `render/internal/schemarender`, external libs      |

## Multi-Format Output

`Codec[T]` is format-agnostic: `Encode`/`Decode` operate on `any` (typically `map[string]any`).
The `format` package adds a thin bridge to wire formats.

```go
// One codec — three formats.
jsonFmt := format.JSON(configCodec)
yamlFmt := format.YAML(configCodec)
tomlFmt := format.TOML(configCodec)

cfg, err := jsonFmt.Unmarshal(jsonBytes)
cfg, err  = yamlFmt.Unmarshal(yamlBytes)
cfg, err  = tomlFmt.Unmarshal(tomlBytes)

out, err := tomlFmt.Marshal(cfg)
```

`Format[T]` has four methods: `Marshal(T) ([]byte, error)`, `Unmarshal([]byte) (T, error)`, `Validate(T) error`, `Schema() schema.Schema`.

`format.New[T]` accepts custom marshal/unmarshal functions for formats not built-in.

**Important**: primitive codecs handle the numeric types each format produces:

- JSON produces `float64` for all numbers
- YAML produces `int` for integers, `float64` for floats
- TOML produces `int64` for integers, `float64` for floats

`Int()` handles `int`, `int64`, and integral `float64`. Add new numeric types to this list when extending.

## Environment Variable Loading (`config.FromEnv`)

`config.FromEnv[T]` loads a struct from environment variables using the codec's schema for schema-driven string coercion. It lives in its own `config` package (not `format` — it never uses `format.Format[T]` internally, and not `ports` — no `Pattern`/adapter family binds to it; config loading is standalone by nature, happening once at startup before any pipeline exists). It is a standalone function, not a `Format[T]` (env vars are read-only; no Marshal direction).

```go
// Naming: strings.ToUpper(prefix + field_name)
// "port"         + "APP_" → APP_PORT
// "log_level"    + "APP_" → APP_LOG_LEVEL
// nested "db.host"        → APP_DB_HOST
cfg, err := config.FromEnv(configCodec, "APP_")
// err is codex.ValidationErrors — parse errors + missing required + constraints.
```

**Supported types** (determined from codec schema):

| Schema type                                                                           | Coercion                                                                      |
| ------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------- |
| `"integer"`                                                                           | `strconv.Atoi` → `int`                                                        |
| `"number"`                                                                            | `strconv.ParseFloat(64)` → `float64`                                          |
| `"boolean"`                                                                           | `strconv.ParseBool` → `bool`                                                  |
| `"string"` (any other)                                                                | pass as-is                                                                    |
| nested struct (`Type="object"`, `Properties!=nil`, `AdditionalPropertiesSchema==nil`) | prefix expansion (`APP_DB_HOST`) OR JSON object (`APP_DB='{"host":"..."}'`)   |
| slice (`Type="array"`, `Items!=nil`)                                                  | comma-separated (`APP_TAGS=a,b,c`) OR JSON array (`APP_TAGS='["a","b","c"]'`) |
| StringMap (`AdditionalPropertiesSchema!=nil`)                                         | JSON object only (`APP_LABELS='{"k":"v"}'`)                                   |
| `Nullable[T]`                                                                         | absent = nil; present = coerce inner type                                     |

**JSON detection**: when the env var value starts with `{` (for object fields) or `[` (for array fields), it is parsed as JSON. JSON takes precedence over prefix expansion and comma-split. Malformed JSON returns a `ValidationError` for that field.

**Silently skipped**: `TaggedUnion`, slices of objects.

**Error shape**: `codex.ValidationErrors` — parse errors are collected before Decode runs; decode errors (missing required, constraint violations) follow in the same type.

## Explicit Validation (bidirectional)

By design, `Refine` constraints run only in the **decode direction** — they guard external input you don't control.
`Encode` is trusted: you constructed the value yourself.

When bidirectional validation is needed, call `Validate` explicitly:

```go
// Codec.Validate — no format required.
if err := userCodec.Validate(u); err != nil { ... }

// Format.Validate — delegates to the codec, format-independent.
if err := jsonFmt.Validate(u); err != nil { ... }
```

`Validate` reuses the exact same `Refine` constraints — builtin (`validate.*`) and self-defined — with no duplication. It encodes `v` to the intermediate and decodes it back, running all constraints in the decode path.

**Never change `Refine` to also wrap `Encode`.** The encode direction must remain unconstrained to preserve the trusted-code design principle.

## Testing

Tests use the standard `testing` package. No test framework dependency.

### File Placement

- `_test.go` files co-located with the package under test.
- Default: external test package (`package codex_test`) for black-box discipline.
- White-box (`package codex`) only when unexported internals must be accessed.

### Table-Driven Pattern

Use `t.Run` subtests with a slice of `{name, input, want, wantErr}` structs:

```go
cases := []struct {
    name    string
    input   any
    want    int
    wantErr bool
}{
    {"from int", 42, 42, false},
    {"wrong type", "x", 0, true},
}
for _, tc := range cases {
    t.Run(tc.name, func(t *testing.T) {
        got, err := codec.Decode(tc.input)
        if (err != nil) != tc.wantErr { ... }
    })
}
```

### What to Test for Every Codec

| Aspect         | Test                                            |
| -------------- | ----------------------------------------------- |
| Happy path     | Valid input decodes/encodes correctly           |
| Round-trip     | `decode(encode(v)) == v`                        |
| Error paths    | Wrong type, missing field, constraint violation |
| Schema         | `Schema.Type` and sub-fields correct            |
| Error messages | Relevant field names / values included          |

### What NOT to Test

- `Codec` struct function fields directly — test through behavior (`Encode`, `Decode`).
- `examples/` — run via `go run`, not `go test`.

## Tooling

This project uses [`just`](https://just.systems/) as the task runner. All common development tasks have a `just` recipe. Run `just` with no arguments to list available recipes.

| Recipe              | Tool                        | Purpose                                     |
| ------------------- | --------------------------- | ------------------------------------------- |
| `just build`        | `go build`                  | Compile all packages                        |
| `just test`         | `go test`                   | Run tests                                   |
| `just test-verbose` | `go test -v`                | Run tests with verbose output               |
| `just cover`        | `go test` + `go tool cover` | Generate HTML coverage report               |
| `just fmt`          | `gofmt`                     | List files with formatting issues           |
| `just staticcheck`  | `staticcheck`               | Static analysis (supersedes `go vet`)       |
| `just gosec`        | `gosec`                     | Security scan (config: `gosec.config.json`) |
| `just check`        | fmt + staticcheck + gosec   | All quality gates                           |
| `just tidy`         | `go mod tidy`               | Clean up module dependencies                |

**Note:** `staticcheck` supersedes `go vet` in this project. Do not run `go vet` directly; use `just staticcheck` or `just check`.

## Verification

```sh
just build    # compile
just check    # fmt + staticcheck + gosec
just test     # run tests
```
