# go-codex Consistency Checklist

Run each section during Phase 2 of the review. For every failing check, open a finding with the
format specified in SKILL.md.

---

## 1. Cross-Layer Naming Parity

| Check | Expected |
|-------|----------|
| Meta struct naming | `RouteMeta`, `ChannelMeta`, `FunctionMeta` — all three exist with same fields: `Title`, `Summary`, `Description`, `Tags []string` |
| MCP Meta structs | `ToolMeta{Description, Tags}`, `ResourceMeta{Name, Description, MimeType, Tags}`, `PromptMeta{Description, Tags}` — all three have `Tags []string` |
| Opt interface naming | `RouteOpt`, `ChannelOpt`, `FunctionOpt` — all three exist as interfaces |
| MCP Opt interfaces | `ToolOpt`, `ResourceOpt`, `PromptOpt` — all three exist as sealed interfaces |
| Info struct naming | `PipelineInfo{Title, Version, Description, Author, ApprovedBy, ApprovedAt}` — governance mirrors `FunctionMeta` governance fields |
| MCP Info | `mcp.Info{Name, Version}` — uses `Name` (MCP protocol) not `Title` (OpenAPI/AsyncAPI); correct by design |
| Builder naming | `rest.Builder`, `events.Builder`, `forge.Registry` — consistent fluent builder pattern |
| MCP Builder | `mcp.Builder` with `NewBuilder(info)`, `Info()`, `MCPSpec()` — analogous to `OpenAPISpec()`/`AsyncAPISpec()` |
| `AddServer` | Both `rest.Builder.AddServer(name, Server)` and `events.Builder.AddServer(name, Server)` exist; description fallback on both |
| Security scheme declaration | `rest.WithSecurityScheme(name, SecurityScheme)` (route-level `RouteOpt` — `rest.Builder` has NO `AddSecurityScheme`, removed) vs `events.Builder.AddSecurityScheme(name, SecurityScheme)` (builder-level) — INTENTIONAL divergence, do not flag; see `docs/features/security.md` for the design rationale (REST needed client+server symmetry via `Route.ClientHandle`, which has no `Builder` at all) |
| `AddGlobalSecurity` | Both builders have `AddGlobalSecurity(reqs...)` |
| Server description fallback | Both builders fall back `Server.Description = name` when empty |

---

## 2. Param Type Consistency

| Check | Expected |
|-------|----------|
| `PathParam` | No `Required` field (always required by OpenAPI spec); godoc explains why |
| `TopicParam` | No `Required` field (topic vars always required); godoc explains why |
| `ResourceParam` | No `Required` field (URI vars always required, same rationale as PathParam/TopicParam); godoc must explain |
| `FilePathParam` | No `Required` field (template vars always required, same rationale as PathParam/TopicParam); use `WithCodec(c)` value-receiver |
| `PromptArg` | Has `Required bool` — prompt args are optional by default; `Required: true` triggers `MissingPromptArgError` |
| `QueryParam`, `CookieParam`, `HeaderParam` | Have `Required bool` |
| `ResponseHeaderParam`, `ResponseCookieParam` | Have appropriate fields; no `Required` (response params are always present when set) |
| `.WithCodec(c codex.Codec[string])` | Present on all 7 REST/events param types: PathParam, QueryParam, CookieParam, HeaderParam, ResponseHeaderParam, ResponseCookieParam, TopicParam; also on `FilePathParam` |
| `.WithCodec(c codex.Codec[string])` on MCP | Present on `ResourceParam` and `PromptArg` — mirrors TopicParam pattern |
| Pointer-free codec setting | No usage of `Codec: &codec` in the library itself; examples must use `.WithCodec()` |

---

## 3. Builder Method Parity

### rest.Builder vs events.Builder

| Method | rest.Builder | events.Builder |
|--------|-------------|----------------|
| `AddServer` | ✓ | ✓ |
| `AddSecurityScheme` | ✗ (removed — see `rest.WithSecurityScheme`, route-level) | ✓ |
| `AddGlobalSecurity` | ✓ | ✓ |
| `Build()` | ✓ | ✓ |

### RouteHandle vs ChannelHandle

| Feature | RouteHandle | ChannelHandle |
|---------|-------------|---------------|
| Format setting | `WithFormats` (response) + `WithRequestFormats` | `WithFormats` (both) + `WithSubscribeFormats` + `WithPublishFormats` |
| Body codec | `Request codex.Codec[Req]`, `Response codex.Codec[Resp]` | `Subscribe.Codec`, `Publish.Codec` |
| Validate method | `ValidatePathParams`, `ValidateQueryParams`, `ValidateHeaders`, `ValidateCookies` | `ValidateTopicVars` |
| Security | `SecuritySchemes`, `GlobalSecurity` | `SecuritySchemes`, `GlobalSecurity` |
| Meta | Implements `RouteOpt` | Implements `ChannelOpt` |

If a method exists on `RouteHandle` and has a natural equivalent on `ChannelHandle`, both should exist.

### mcp.Builder parity

| Feature | rest/events Builder | mcp.Builder |
|---------|---------------------|-------------|
| Spec generation | `OpenAPISpec()` / `AsyncAPISpec()` | `MCPSpec()` → `*MCPSpec{Name, Version, Tools, Resources, Prompts}` |
| Server info | `NewBuilder(Info{Title, Version})` | `NewBuilder(Info{Name, Version})` — Name per MCP protocol |
| Security | `WithSecurityScheme` (route/channel-level), `AddGlobalSecurity` (builder-level) | n/a — MCP security outside builder |

### mcp Handle parity

| Feature | RouteHandle | ChannelHandle | ToolHandle / ResourceHandle / PromptHandle |
|---------|-------------|---------------|---------------------------------------------|
| Typed decode | `Decode([]byte)(Req,error)` (field) | `Decode([]byte)(T,error)` (field) | `ToolHandle.Decode(any)(In,error)` (field) |
| Typed encode | `Encode(Resp)([]byte,error)` (field) | `Encode(T)([]byte,error)` (field) | `ToolHandle.Encode(Out)([]byte,error)` (field) |
| Build path/topic/URI | `BuildPath(vars)(string,error)` (method) | `BuildTopic(vars)(string,error)` (method) | `ResourceHandle.BuildURI(vars)(string,error)` (method) |
| Validate params | `ValidatePathParams(vars)` (method) | `ValidateTopicVars(vars)` (method) | `ResourceHandle.ValidateURIVars(vars)` (method); `PromptHandle.ValidateArgs(args)` (method) |
| JSON Schema | n/a | n/a | `ToolHandle.InputSchema`/`OutputSchema json.RawMessage` |

**Key**: `BuildURI`, `ValidateURIVars`, and `ValidateArgs` MUST be methods (not function fields) — consistent with REST/events. If they become function fields, that is a `small` finding.

### Registry (forge) parity with Builders

| Feature | rest/events Builder | forge Registry |
|---------|---------------------|----------------|
| Description | `Server.Description` | `Registry.WithDescription(s string)` |
| Author | n/a (per-route) | `Registry.WithAuthor(s string)` |
| Approval | n/a (per-route) | `Registry.WithApproval(by, at string)` |
| Observer | n/a | `Registry.WithObserver(stats.PipelineObserver)` |

---

## 4. Codec Field Godoc

All `Codec *codex.Codec[string]` fields on param types must use consistent wording:

> `Codec validates X parameter values at [Handle.ValidateY] time.`

Check: `HeaderParam.Codec`, `ResponseHeaderParam.Codec`, `ResponseCookieParam.Codec`,
`QueryParam.Codec`, `CookieParam.Codec`, `PathParam.Codec`, `TopicParam.Codec`,
`ResourceParam.Codec`, `PromptArg.Codec`.

`ResourceParam.Codec` should say: "Codec validates URI parameter values at [ResourceHandle.ValidateURIVars] and [ResourceHandle.BuildURI] time."
`PromptArg.Codec` should say: "Codec validates the argument value at [PromptHandle.ValidateArgs] time."

Deviation from this pattern = trivial finding.

---

## 5. Format API Parity

| Check | Expected |
|-------|----------|
| `RouteHandle.WithFormats` | Sets response encode formats |
| `RouteHandle.WithRequestFormats` | Sets request decode formats |
| `ChannelHandle.WithFormats` | Sets both subscribe + publish formats |
| `ChannelHandle.WithSubscribeFormats` | Sets subscribe-only formats |
| `ChannelHandle.WithPublishFormats` | Sets publish-only formats |
| Adapter priority | Adapters check `SubscribeFormats`/`PublishFormats` before falling back to `Formats` |
| `SSERouteHandle.WithFormats` | Sets SSE stream formats (mirrors ChannelHandle pattern) |
| `format.Binary(c codex.Codec[[]byte]) Format[[]byte]` | Raw bytes identity format — validates via Refine constraints; distinct from Gob (Gob adds framing; Binary writes raw bytes) |
| `codex.Bytes()` | Raw `[]byte` codec, schema `{type:"string", format:"binary"}` — for binary file I/O and HTTP binary bodies |
| `codex.Base64()` | Base64 `[]byte` codec, schema `{type:"string", format:"byte"}` — for binary fields embedded in JSON |
| `Format` struct godoc | Must list `Binary` alongside JSON, YAML, TOML, Gob: "Use JSON, YAML, TOML, Gob, or Binary to construct one" |
| Binary file format constraints | `validate.PNG`, `validate.JPEG`, `validate.GIF`, `validate.WebP`, `validate.PDF`, `validate.ZIP` — predefined `Constraint[[]byte]` values, no Schema annotation, produce `ConstraintError` |
| `validate.HasPrefix(prefix []byte)` | General magic-byte check; prefer built-in constants for known formats; use HasPrefix for custom/proprietary formats |
| `ports.FilePattern`/`CachePattern`/`SocketPattern` `CustomFormat any` | Pre-built `format.Format[T]` escape hatch for binary/custom formats — overrides `Format` enum when non-nil; type-asserted at build time via `resolveFormat`; mismatch → `PatternRegisterError` |
| `rest.RequestFormats[Req]`/`Formats[Resp]`, `events.Formats[T]`/`SubscribeFormats[T]`/`PublishFormats[T]`, `reqreply.RequestFormats[Req]`/`Formats[Resp]` | Inline `RouteOpt`/`ChannelOpt` constructors — the `RESTPattern`/`EventPattern`/`ReqReplyPattern` equivalent of `CustomFormat` (these 3 patterns need no struct field since their handles already support format negotiation); mismatch → package-local `FormatOptError` |

---

## 6. Forge Consistency

| Check | Expected |
|-------|----------|
| `FunctionKindScalar` | Value is `""` (empty string); not `"scalar"` |
| Scalar functions | `Kind == ""` by default; `NewFunction`/`Compose` never write `Kind` |
| `render/pipeline` scalar omission | `kind:` key omitted from YAML for scalar functions |
| `FunctionOpt` options | Only `FunctionMeta{...}` struct literal; no `WithDescription` FunctionOpt function |
| `PipelineInfo` governance | Has `Author`, `ApprovedBy`, `ApprovedAt` fields |
| `Registry.WithAuthor` / `Registry.WithApproval` | Both exist as fluent methods |
| `render/pipeline buildInfo()` | Emits `author`/`approvedBy`/`approvedAt` under `info:` when set; omits when empty |
| Port name inference | From `codec.Schema.Title`; struct codec properties expand to individual `PortSpec` entries |
| Collection ops | `Map`, `Filter`, `Reduce`, `MapValues`, `MapValuesK` — all set correct `FunctionKind` constant |

---

## 7. Error Sentinel Consistency (Structured Errors)

All error returns must be typed — not bare `fmt.Errorf` strings without a typed wrapper.

### rest package

| Error type | When to use |
|------------|-------------|
| `rest.PathParamError{Name, Err}` | codec validation failure on path param |
| `rest.MissingPathVarError{Name}` | path var missing from request context |
| `rest.QueryParamError{Name, Err}` | codec validation failure on query param |
| `rest.HeaderParamError{Name, Err}` | codec validation failure on header |
| `rest.CookieParamError{Name, Err}` | codec validation failure on cookie |
| `rest.SecurityCredentialError{Scheme, Err}` | credential codec validation failure |
| `rest.SecurityError{Err}` | `SecurityFunc` returned an error |

### events package

| Error type | When to use |
|------------|-------------|
| `events.TopicParamError{Name, Value, Err}` | codec validation failure on topic var |
| `events.MissingTopicVarError{Name}` | topic var missing from `vars` map |

### adapters/mqtt (`Publish`)

| Error type | When to use |
|------------|-------------|
| `events.TopicParamError`, `events.MissingTopicVarError` | topic var codec failure or missing var (from `BuildTopic`) |
| `mqtt.PublishEncodeError{Topic, Err}` | payload encode/marshal failure — `Topic` is the concrete topic after template substitution; `Unwrap()` exposes the underlying codec error |

All must be `errors.As`-navigable. Bare `fmt.Errorf` in `adapter.go` Publish without a typed sentinel is a finding.

### mcp package

| Error type | When to use |
|------------|-------------|
| `mcp.ToolInputError{Name, Err}` | `ToolHandle.Decode` — input codec validation failure |
| `mcp.ToolOutputError{Name, Err}` | `ToolHandle.Encode` — output codec validation failure |
| `mcp.ResourceEncodeError{URI, Err}` | `ResourceHandle.Encode` — resource encode failure |
| `mcp.ResourceParamError{Name, Value, Err}` | `ResourceHandle.BuildURI`/`ValidateURIVars` — URI var codec failure |
| `mcp.MissingResourceVarError{Name}` | `ResourceHandle.BuildURI`/`ValidateURIVars` — required URI var absent |
| `mcp.InvalidResourceParamError{Name, URITemplate}` | `Resource.Register` — `ResourceParam` not in URI template |
| `mcp.PromptArgError{Name, Err}` | `PromptHandle.ValidateArgs` — arg codec failure |
| `mcp.MissingPromptArgError{Name}` | `PromptHandle.ValidateArgs` — required arg absent |

### adapters/nethttp client (`nethttp.Call`)

| Error type | When to use |
|------------|-------------|
| `rest.PathParamError`, `rest.MissingPathVarError` | path var codec failure or missing var (pre-flight, no HTTP call) |
| `rest.QueryParamError` | query param codec failure (pre-flight) |
| `rest.CookieParamError` | cookie codec failure (pre-flight) |
| `rest.HeaderParamError` | header codec failure (pre-flight) |
| `nethttp.ErrorPatternResponse{StatusCode,Value,Body}` | non-2xx response matching a route-declared `rest.ErrorPattern` (default `ErrorRespond` action), decoded via `RouteHandle.DecodeErrorFor` — returned INSTEAD OF `UnexpectedStatusError` on match; check this BEFORE `UnexpectedStatusError` in an `errors.As` chain |
| `nethttp.UnexpectedStatusError{Method,Path,StatusCode,Body}` | non-2xx HTTP response with no matching `ErrorPattern` (or its body failed to decode) — universal fallback |
| `nethttp.RequestBuildError{Err}` | `http.NewRequestWithContext` failure (bad URL, cancelled ctx) |
| `nethttp.RequestError{Method,Path,Err}` | `http.Client.Do` transport failure (network, DNS, TLS, timeout) |
| `nethttp.ResponseBodyError{Err}` | `io.ReadAll` failure on response body |

All must be `errors.As`-navigable. Bare `fmt.Errorf` in `client.go` without a typed sentinel is a finding.

### adapters/mcpgo adapter behavior (distinct from REST/events)

| Situation | How mcpgo handles it |
|-----------|----------------------|
| Input decode/validation failure | Returns `mcp.NewToolResultError(err.Error())` — `IsError: true` result, **not** a Go error |
| Handler error, no matching `mcp.ErrorPattern` | Returns `mcp.NewToolResultError(err.Error())` — `IsError: true` result |
| Handler error, matching `mcp.ErrorPattern` | Returns `mcp.NewToolResultStructured(json.RawMessage(body), string(body))` with `IsError: true` set manually — structured typed content, still an error to the LLM (see §13) |
| Output encode failure | Returns `(nil, err)` — protocol-level Go error (server contract violation); NEVER consults `ErrorPattern` (different concern) |

### format package (File I/O)

| Error type | When to use |
|------------|-------------|
| `ports.FilePathParamError{Name, Value, Err}` | path variable fails its codec constraint in `BuildPath`/`Read`/`Write`/`Update` |
| `ports.MissingFilePathVarError{Name}` | path variable absent from the `vars` map |
| `ports.FileReadError{Path, Err}` | `os.ReadFile` fails |
| `ports.FileDecodeError{Path, Err}` | codec decode or constraint validation fails on read |
| `ports.FileEncodeError{Path, Err}` | codec encode fails on write |
| `ports.FileWriteError{Path, Err}` | `os.WriteFile` fails |
| `ports.FilePatchNotSupportedError{Path}` | `Patch`/`PatchEncoded` called on a Gob or Binary format |

All 7 file error types implement `Unwrap()` **and** `slog.LogValuer`. Callers can pass any file error directly to `slog.Any(...)` for nested structured attributes.

Also: `config.EnvVarError{Key, Err}` — returned by `config.FromEnvVar[T]` when coercion or constraint fails. `Unwrap()` exposes `codex.ValidationErrors`; also implements `slog.LogValuer`.

### forge package

| Error type | When to use |
|------------|-------------|
| `forge.InputError{Err}` | input codec decode failure |
| `forge.OutputError{Err}` | output codec validate failure |
| `forge.ApplyError{Function, Err}` | user `fn` returned an error |
| `forge.RefinementError{Function, Err}` | `WithRefinement` predicate failed |

**Check**: grep for `fmt.Errorf` in adapters and api packages. Any bare `fmt.Errorf("...")` that
doesn't wrap a typed sentinel is a finding.

---

## 8. Observer Pattern

### stats.Observer interfaces

| Interface | Methods | Used by |
|-----------|---------|---------|
| `ValidationObserver` | `RecordValidation(location string, err error)` | codecs (internal) |
| `Observer` | embeds `ValidationObserver` + transport hooks | adapters (nethttp, chi, mqtt, mcpgo) |
| `PipelineObserver` | `RecordApply(name, version string, success bool, duration time.Duration)` | forge Registry |
| `SecurityObserver` | `RecordSecurityRejection(location, scheme string)` | adapters (type-asserted, not mcpgo) |
| `FileObserver` | `RecordFileRead(path string, success bool, d time.Duration)` · `RecordFileWrite(path string, success bool, d time.Duration)` | `ports.File[T]` (type-asserted, never embedded) |
| `TraceObserver` | `StartSpan(ctx, operation, name string) context.Context` · `EndSpan(ctx, err error)` | all adapters (type-asserted, never embedded) |

### Rules

- `SecurityObserver` must be guarded: `if so, ok := obs.(stats.SecurityObserver); ok { ... }` — **never** embedded in `Observer`
- `FileObserver` must be guarded: `if fo, ok := obs.(stats.FileObserver); ok { ... }` — **never** embedded in `Observer`; `path` is concrete path after template substitution, never the template
- `TraceObserver` must be guarded: `if to, ok := obs.(stats.TraceObserver); ok { ... }` — **never** embedded in `Observer`; `LoggingObserver` does NOT implement `TraceObserver`
- `PipelineObserver.RecordApply` must be called for every function in a pipeline, including `Map`/`Filter`/etc.
- Adapters must call `Observer` on every code path — including early-exit error paths — not just the happy path
- `NoopObserver` satisfies all five interfaces; returned by `ObserverFromContext` when no context observer is set
- **Nil-guard pattern (direct-ctx functions)** — `ObserverFromContext(ctx)` not `NoopObserver{}`:
  ```go
  obs := opts.Observer
  if obs == nil {
      obs = stats.ObserverFromContext(ctx)  // ← correct since default-observer feature
  }
  ```
  `NoopObserver{}` in a nil-guard is a finding unless the function has no `ctx` parameter.
- **HTTP/MCP closure exception**: `nethttp.Handler`, `chi.Handler`, `mcpgo.ToolHandler` etc. are constructors that return closures. obs is resolved inside the closure from `r.Context()` / call ctx — NOT at construction time. This is correct.
- **`sql.Validate` exception**: no `ctx` parameter → `NoopObserver{}` is correct. Do not flag.
- **`forge.Registry`**: explicit `.WithObserver(obs)` builder. No context integration by design.
- **`ports.File` two-step guard**: uses `opts.Context` (optional) → `ObserverFromContext(opts.Context)` then `NoopObserver{}` fallback. This is correct.
- **`adapters/nethttp` client (`Call`) observer rules**:
  - `RecordRequest(method, routePathTemplate, statusCode, duration)` — called on **every** code path; status 0 = pre-flight failure (no HTTP call reached the network)
  - `stats.ReportErrors(obs, location, err)` called before `RecordRequest` for param validation failures (location: `"path"`, `"query"`, `"cookie"`, `"header"`, `"body"`)
  - Path template (e.g. `/users/{id}`), not concrete URL — allows grouping metrics by route
- **`adapters/mcpgo` observer locations**:
  - `"input"` — tool argument decode/validation failure (`stats.ReportErrors(obs, "input", err)`)
  - `"prompt.args"` — prompt argument codec failure (`stats.ReportErrors(obs, "prompt.args", err)`)
  - `RecordRequest("tool"|"resource"|"prompt", name, 200|400|500, d)` — one call per tool/resource/prompt invocation

**Check**: in each adapter (`adapter.go`), verify Observer is called in both success and error branches.

### Separation of concerns — metrics vs logging

Observer implementations must **not mix** metric counting with slog logging. The canonical pattern uses the library-provided types:

```go
// CORRECT: separate concerns via stats.NewFanout
obs := stats.NewFanout(
    metricsObserver,                                       // pure counters
    stats.NewLoggingObserver(slog.Default().With(...)),    // pure slog
)
```

**Check**: no `fmt.Printf` or `slog.*` calls inside `RecordValidationError`, `RecordRequest`, `RecordSubscribe`, `RecordPublish`, `RecordFileRead`, or `RecordFileWrite` method bodies in **any** observer implementation in the codebase or examples. These calls indicate mixed concerns. File a `trivial` finding for each occurrence.

**Check**: all example `CountingObserver`/`telemetryObserver` implementations use `stats.NewFanout` + `stats.NewLoggingObserver` for logging rather than embedding a logger or calling slog directly.

---

## 9. Unit Test Coverage

### Per-package expectations

| Package | Test file | Expected coverage |
|---------|-----------|-------------------|
| `api/rest` | `builder_test.go` | Each param type: WithCodec, Validate* happy+error path; `RouteHandle.EncodeRequest`/`DecodeResponse` round-trip; `Route.ClientHandle` returns handle, BuildPath works, encode/decode round-trip |
| `adapters/nethttp` | `client_test.go` | `Call` happy path (POST+GET); non-2xx → `UnexpectedStatusError`; path/query/cookie/header param validation errors; `CredentialFunc` invoked + error; `Observer.RecordRequest` called on validation failure (status 0); `ClientHandle` (no builder); query params in URL; extra headers sent |
| `api/events` | `builder_test.go` | TopicParam WithCodec; ValidateTopicVars missing key → MissingTopicVarError; WithSubscribeFormats/WithPublishFormats |
| `api/mcp` | `builder_test.go` | Tool/Resource/Prompt Register happy+error; ToolHandle.Decode/Encode; ResourceHandle.BuildURI/ValidateURIVars; PromptHandle.ValidateArgs; ResourceParam.WithCodec; PromptArg.WithCodec; Tags flow to handles + MCPSpec; all typed errors via errors.As |
| `adapters/mcpgo` | `adapter_test.go` | ToolHandler success; input error → IsError=true; handler error → IsError=true; output error → protocol error; observer RecordRequest 200/400/500; ResourceHandler happy+error+encodeError+template detection; PromptHandler happy+missingArg+handlerError |
| `render/jsonschema` | `jsonschema_test.go` | zero schema → nil; string type; object with properties; enum; numeric constraints |
| `forge` | `forge_test.go` | FunctionKindScalar; PipelineInfo.WithAuthor/WithApproval; collection ops |
| `render/pipeline` | `pipeline_test.go` | governance fields emitted when set; omitted when empty |
| `render/asyncapi/v2` | any `*_test.go` | server insertion order deterministic |
| `render/asyncapi/v3` | any `*_test.go` | server insertion order deterministic |
| `validate` | `bytes_test.go` | `HasPrefix`: match, no-match, empty prefix, too-short value, `ConstraintError` type assertion; `MaxBytes`/`MinBytes` integration via `codex.Base64()` |
| `validate` | `binary_test.go` | Each format constraint (`PNG`, `JPEG`, `GIF`, `WebP`, `PDF`, `ZIP`): valid magic passes, wrong magic fails, too-short fails, `Name` non-empty, `ConstraintError` via `errors.As` |
| `format` | `format_test.go` | `Binary`: roundtrip, constraint fail on write (`ConstraintError`), constraint fail on read (`ConstraintError`), default CT `"application/octet-stream"`, `WithContentType` override, not streamable |
| `codex` | `primitives_test.go` | `Bytes`: roundtrip (identity encode), schema `{type:"string",format:"binary"}`, `TypeMismatchError` on non-`[]byte` Decode; `Base64`: roundtrip (base64 encode), schema `{type:"string",format:"byte"}`, invalid base64 Decode error |

### Missing test finding rule

If an exported symbol (type, method, function) has no corresponding `func Test…` function anywhere
in the package's `*_test.go` files, file a `trivial` finding. Priority upgrades to `small` if the
symbol is on an error path.

---

## 10. Example Correctness

Scan all `examples/*/main.go` files.

### API pattern checks

| Pattern to find | Expected state | Finding if |
|-----------------|---------------|-----------|
| `Codec: &` | Should not exist | File a `trivial` finding |
| `AddRoute(` | Should not exist (replaced by `NewRoute`) | File a `small` finding |
| `AddChannel(` | Should not exist (replaced by `NewChannel`) | File a `small` finding |
| `codex.Field[` | Should not exist (replaced by `RequiredField`/`OptionalField`) | File a `trivial` finding |
| `validate.HasPrefix(` in examples | Prefer built-in constants (`validate.PNG` etc.) for known formats; `HasPrefix` only for custom formats | File a `trivial` finding if a known format (PNG, JPEG, PDF…) is checked via `HasPrefix` instead of the built-in constant |
| `codex.Bytes()` used for base64 JSON fields | Should be `codex.Base64()` — `codex.Bytes()` is now the raw binary codec | File a `small` finding |
| MCP: `s.AddTool(` direct on `MCPServer` | Should use `mcpgo.RegisterTool(s, handle, fn, opts)` unless using `ToolHandler` directly | File a `trivial` finding |
| MCP: `mcp.NewTool(name, mcp.WithDescription(...))` directly | Should use `mcp.NewTool[In,Out](name, inputCodec, outputCodec, mcp.ToolMeta{...})` | File a `small` finding |

### Runtime check

```bash
for d in examples/*/; do
  echo "=== $d ==="
  (cd "$d" && timeout 5 go run . 2>&1) || echo "FAILED: $d"
done
```

Any non-zero exit (excluding timeout) is a `small` finding if the example is broken, or a `trivial`
finding if the example just runs forever (server) and timeout is expected.

### Example-feature alignment

Each example directory name should match what it demonstrates. If an example named `adapters-sse`
does not exercise SSE, file a `trivial` finding.

Verify `examples/adapters-mcp/main.go` demonstrates:
- All three MCP primitives: Tool, Resource, Prompt
- `mcp.NewBuilder` + `MCPSpec()` output
- `mcpgo.RegisterTool/Resource/Prompt` with `Options{Observer: obs}`
- Observer (CountingObserver) wired and printed at end
- Structured error handling (`errors.As` on `ResourceParamError`, `MissingPromptArgError`)
- Transport options comment block (stdio / streamable HTTP / SSE)

---

## 11. Port Adapter Consistency

Port adapters (`adapters/*/binding.go`) connect transports to `ports.SourcePort`/`SinkPort`/
`IOPort`/`ToolPort`/`LatestPort`/`DuplexPort`. The legacy "stream bridge" functions this section
used to describe (`SubscribeStream`, `QueryStream`, `DrainPublish`-as-bridge, `HandlerIngest`,
etc.) were fully removed in Round 45 — see SKILL.md's own "Port Adapter Guardrail" (rules B1–B3)
for the authoritative, up-to-date version of these checks. This section restates them with live
function names so the two stay in sync; if they ever diverge, SKILL.md wins.

Apply these checks after any addition or change to `adapters/*/binding.go` or `ports/*.go`.

### Source/IO adapter validation pipeline (Rule B1)

Every adapter must delegate to the underlying non-stream adapter function rather than rolling its
own validation:

| Adapter | Check | Bug if |
|---------|-------|--------|
| `mqtt.SubscribeAdapter` | Calls `SubscribeHandler(ctx, handle, fn, innerOpts, fmt)` | Raw handler pushes `msg.Payload()` without calling `SubscribeHandler` |
| `mqtt5.SubscribeAdapter` | Calls `makeSubscribeMessageHandler(ctx, handle, fmts, fn, obs, opts)` | Raw handler pushes `msg.Payload` without going through it |
| `zeromq.SubscribeAdapter` | Calls `sock.SetSubscription`, `sock.RecvFrames`, `gstream.FromCodec` | SetSubscription not called, or raw payload pushed without decode |
| `nethttp.IngestAdapter` / `chi.IngestAdapter` | Calls `Handler(handle, fn, opts)` internally | Implements its own `http.HandlerFunc` that skips any codec layer |
| `nethttp.PipelineAdapter` / `chi.PipelineAdapter` | Calls `Handler(handle, fn, opts)` | Same |
| `nethttp.LatestAdapter` / `chi.LatestAdapter` | Calls `Handler(handle, fn, opts)` | Same |
| `sql.QueryAdapter` / `QueryEachAdapter` | Calls `Validate(codec, row, opts)` per row | Rows pushed without `Validate` call |
| `mcpgo.ToolPipelineHandler` / `ToolLatestHandler` | Calls `ToolHandler(handle, fn, opts)` | Direct `server.ToolHandlerFunc` that skips `handle.Decode`/`handle.Encode` |

### Error routing to `Stream.Errors` / `errs` channel (Rule B2)

Check each source adapter: errors must reach the `errs chan<- error` passed to `Activate`, not be
silently discarded.

| Adapter | How errors reach the port | Error type |
|---------|---------------------------|------------|
| `mqtt.SubscribeAdapter` | `innerOpts.OnError → errs channel` | `mqtt.SubscribeError` (wrapped as `ports.PortBindError` via `BrokerError`) |
| `mqtt5.SubscribeAdapter` | Same `innerOpts.OnError` override pattern | `mqtt5.SubscribeError` |
| `sql.QueryAdapter` | `QueryStreamError` on `queryFn` failure; `RowValidationError` on codec failure | `sql.QueryStreamError`, `sql.RowValidationError` |
| `zeromq.SubscribeAdapter` | Socket errors terminate the goroutine → channels close | — |
| `file.ScanAdapter` | `ScanError` → `errs` channel | `file.ScanError` |

Missing error routing (errors dropped to `default` only, never reaching `errs`) = `bug` finding.

### Sink adapter static `Vars` documentation (Rule B3)

Check `DrainPublishOptions`/`MQTT5DrainPublishOptions`/`DrainPublishOptions` (`mqtt`, `mqtt5`,
`zeromq`) and `CallStreamOptions`-style `Vars` fields on `CallAdapter`. Godoc for the `Vars` field
must say (in substance):
> "The same map is used for every item (static topic vars only). For per-item substitution, use
> [gstream.Drain]/[stream.Drain] with [Publish]/[Call] directly."

Missing note = `trivial` finding.

### `AsPipelineFunc` pattern (Rule B4)

`AsPipelineFunc` in `mqtt5` and `zeromq` must:
1. Return `func(context.Context, Req) (Resp, error)` — the fn signature for `Serve`/`ServeRouter`
2. Use `stream.Single(ctx, req)` to build the per-request source
3. Call `stream.Collect(ctx, pipeline)` to extract the result
4. Return `PipelineNoResponseError{Topic}` when `vals` is empty
5. Return `errs[0]` (not `vals[0]`) when both `errs` and `vals` are non-empty
6. Not add a new `Serve` variant — it wraps the `fn` argument only

Deviation from any of these = `small` finding.

### Adapter error type completeness

Verify every error type listed in the Structured Errors Guardrail / SKILL.md's "Error types in
port adapters" table exists and implements `slog.LogValuer`:

| Package | Error types that must exist and implement `slog.LogValuer` |
|---------|----------------------------------------------------------|
| `adapters/nethttp` | `NoLatestValueError{Path}`, `PipelineFullError{Path,Capacity}`, `PipelineNoResponseError{Path}`, `SSEWriteError{Path,Err}` |
| `adapters/chi` | Same as nethttp |
| `adapters/zeromq` | `ServeLatestError{Op,Err}`, `NoLatestValueError{Topic}`, `CorrelationError{Seq,Err}`, `PipelineNoResponseError{Topic}` |
| `adapters/mqtt5` | `PipelineNoResponseError{Topic}`, `BrokerError{Op,Err}` |
| `adapters/sql` | `QueryStreamError{Table,Op,Err}`, `InsertStreamError{Table,Op,Err}`, `RowValidationError{Table,Op,Err}` |
| `adapters/file` | `ScanError{Path,Err}`, `WatchError{Dir,Err}`, `WriteError{Path,Err}`, `ReadError{Err}` |
| `ports` | `PortBindError{Port,Adapter,Err}`, `PortNoAdapterError{Port}` |

Errors with inner `Err` field must implement `Unwrap()`. Terminal errors (no inner cause) must NOT implement `Unwrap()`. Violation = `small` finding.

### `nethttp.IngestAdapter` / `chi.IngestAdapter` param value gap (documented design)

`IngestAdapter` pushes only the body-decoded `Req` to the port — path/query/cookie/header param
values are validated but discarded. **Do not flag this as a bug** — it is documented design. Check
only that the godoc note is present explaining this and providing the `Handler`-direct workaround.

Missing godoc note = `trivial` finding.

### HTTP adapter codec coverage documentation

`LatestAdapter`, `IngestAdapter`, and `PipelineAdapter` godoc should include a "Codec coverage"
section listing all HTTP codec layers. If it is missing:
- `LatestAdapter`: must note that Req is validated even though discarded
- `IngestAdapter`: must note body-only channel push + param value gap + workaround
- `PipelineAdapter`: must document `RequestFromContext(ctx)` for param access + response header/cookie pattern

Missing section = `trivial` finding per adapter.

---

## 12. Merge-field / Boundary Symmetry — one struct, one call

See SKILL.md's "Boundary Symmetry Guardrail" for the full rationale — this section restates it as
concrete per-boundary check rows. The headline check for any `api/*` builder-backed boundary with a
request/response shape or a duplex role pair: **can a caller on either side do the entire
encode-or-decode direction with one struct value in (or out), one call?**

| Boundary | Declare-once constructors | Escape hatch | Encode/decode symmetry | Role symmetry | Single-call wrapper | Nested + non-JSON coverage | Status |
|---|---|---|---|---|---|---|---|
| `api/rest` (REST) | `NewPathParam[T]`/`NewRequiredQueryParam[T]`/etc. + `NewRequiredResponseHeaderParam[Resp]`/etc. | `PathParam`/`QueryParam`/etc. struct literals still work | `DecodeMerged` (decode) + `PathMergeFields()`/`QueryMergeFields()`/etc. (encode) | server (`Handler`/`Register`) + client (`Call`) both covered | `nethttp.CallHandle` (client) + `Handler` auto-merge (server) | ✅ `examples/rest-nested-binary` + `TestNestedStructMergeFields_GetSetReachIntoSubstruct`/`TestGobBodyFormat_ComposesWithNestedMergeFields` — nested `Meta`/`Payload` sub-structs, Gob body via `format.NewTyped` projection | ✅ Reference implementation for the CORE API — see the port-binding-layer caveat below |
| `api/events` (pub/sub) | `NewTopicParam[T]` | `TopicParam` struct literals still work | `ChannelHandle.DecodeMerged` (decode) + `MergeFields()` (encode — single flat slice, no role-split needed, only ONE var destination) | subscriber (`Subscribe`/`SubscribeHandler` auto-merge) + publisher (`PublishHandle`) both covered, per transport | `mqtt5.PublishHandle`/`zeromq.PublishHandle`/`mqtt.PublishHandle` | ✅ `examples/events-nested-binary` — nested `Meta`/`Value` payload, Gob body via `format.NewTyped` projection | ✅ SHIPPED across `adapters/mqtt5`, `adapters/zeromq` (own pub/sub, G2), and `adapters/mqtt` v3 (G3) |
| `api/reqreply` (req/reply) | `NewTopicParam[T]` (Req-side only) | `TopicParam` struct literals still work | `RouteHandle.DecodeMerged` (decode) + `MergeFields()` (encode) | server (`mqtt5.Serve` auto-merge) + client (`mqtt5.CallHandle`/`zeromq.CallHandle`) both covered | `mqtt5.CallHandle`/`zeromq.CallHandle` | ✅ nested-Req + Gob round trip test (`TestServeCallHandle_NestedReq_RoundTrip`) | ✅ SHIPPED, with a documented exception: `zeromq.CallHandle` is client-side ONLY — `zeromq.Serve` reads raw socket frames with no per-message topic string, architecturally cannot decode-merge server-side |
| **Port-binding layer** (`ports.Pattern` + `adapters/*/binding.go`) | n/a | n/a | Decode side (`SubscribeAdapter`) inherits the underlying `Subscribe`/`Handler` auto-merge for free; `zeromq.SubscribeAdapter` now delegates to `Subscribe` itself (previously hand-rolled, bypassing merge wiring entirely) | n/a | `DrainCallAdapter`/`PublishAdapter`/`CallAdapter` delegate to `CallHandle`/`PublishHandle` and derive vars PER-ITEM whenever `Vars` is left `nil`, across `nethttp`/`mqtt5`/`zeromq`/`mqtt` — a non-nil `Vars` map remains the static-vars escape hatch | n/a | ✅ SHIPPED. Flag as a finding if a NEW port-binding adapter reproduces the OLD static-`Vars`-only pattern instead of delegating to its own Handle-suffixed convenience. SSE/WebSocket connection-level merge and hardening are also shipped (see docs/features pages). |
| MCP `api/mcp` (Resources/Prompts) | `ResourceParam`/`PromptArg` exist (validate-only) | n/a | Resources: ✅ SHIPPED — `ResourceHandle.ExtractURIVars` extracts+validates in one call; `mcpgo.RegisterResourceWithVars`/`ResourceHandlerWithVars` (additive, `RegisterResource`/`ResourceHandlerFunc` unchanged) wire it automatically. Prompts: `ValidateArgs` auto-called in `PromptHandler`, but the app still gets a raw `map[string]string`, not a merged struct (deferred, no use case) | n/a | n/a | ✅ Resources SHIPPED. Full merge-field parity for either Resources or Prompts permanently declined without a concrete use case (see `docs/concepts/api-contracts.md`'s "one struct, one call" reference table) — not tracked in a roadmap doc since it's a closed decision, not pending work |
| `ports.File` | `NewFilePathParam[T]` | `FilePathParam` struct literals still work | `File.ReadMerged` (decode) + `File.MergeFields()` (encode — single flat slice, only ONE var destination: the path) | `adapters/file`'s `ReadEachAdapter`/`ReadAdapter` (read) + `DrainWriteFileAdapter` (write) both covered | `ports.WriteHandle` (encode) + `ReadEachAdapter`/`ReadAdapter` auto-merge via `ReadMerged` (decode) | ✅ `TestReadMerged_MergesPathVarsIntoDecodedValue`/`TestWriteHandle_DerivesVarsFromValue` (`ports/file_test.go`); adapter wiring in `adapters/file/binding_test.go` | ✅ SHIPPED (Round 63 — see `references/history.md`). Fixed alongside: `ReadEachAdapter`/`ReadAdapter`'s independent `In`/`Resp` shape keeps `varsFor` mandatory (enrichment, not same-type) — only `DrainWriteFileAdapter`'s `varsFor`-nil path derives automatically |
| `ports.Cache` | `NewCacheKeyParam[T]` | `CacheKeyParam` struct literals still work | `redis.GetMerged` (decode) + `Cache.MergeFields()` (encode — single flat slice, only ONE var destination: the key) | `adapters/redis`'s `GetAdapter` (read) + `SetAdapter`/`DrainSetAdapter` (write) both covered | `redis.SetHandle` (encode) + `GetAdapter` auto-merge via `GetMerged` (decode) | ✅ `TestGetMerged_MergesKeyVarsIntoDecodedValue`/`TestSetHandle_DerivesVarsFromValue` + adapter wiring (`adapters/redis/binding_test.go`) | ✅ SHIPPED (Round 63 — see `references/history.md`). Also fixed a real, pre-existing bug found while implementing: BOTH `CachePattern` build paths (`buildEventPatternHandles`/`buildDualCodecPatternHandles` in `ports/handle.go`) reconstructed `Cache[T]` field-by-field and silently dropped `NewCacheKeyParam`-registered merge fields — fixed by delegating to `NewCache` (mirrors `FilePattern`'s existing delegation to `NewFile`) |

If a boundary marked ❌/⚠️ above is touched by the change under review, re-verify it against all five
checks in SKILL.md's "Boundary Symmetry Guardrail" AND the "Nested + non-JSON coverage" column and
file findings for anything missing — at that point the "known gap" exemption no longer applies to
the boundary being worked on. A boundary that passes the first five columns but only ever demonstrates JSON body + flat
top-level fields is INCOMPLETE — file at least a `small` finding (see SKILL.md's "Boundary Symmetry
Guardrail" for the rationale: body format is orthogonal to var-merge, and merge-field `get`/`set` are
plain closures that must support nested access).

---

## 13. Error-Path Ergonomics (`ErrorPattern`/`ErrorChannel`/`ErrorFrame`/`ErrorAction`)

Codec-first, declarative error-path declarations exist across every `api/*` layer plus
`adapters/websocket`, unifying error handling with the same "declare → register → handle" workflow
already used for the happy path. The design roadmap (`docs/roadmap/error-path-ergonomics.md`) has
been REMOVED — all phases shipped, and this checklist section plus `references/history.md`
(Rounds 64–65) are now the durable design-decision record. See `docs/guides/error-handling.md` for
the cross-boundary usage guide and each `docs/features/*.md`'s own "Error-path ergonomics" section
for user-facing docs.

### Declaration surface per boundary

| Boundary | Declaration | Status/topic concept | Action model |
|---|---|---|---|
| `api/rest` | `rest.ErrorStatus[E](status)` (status-only) / `rest.ErrorPattern[E,B](status, codec, mapFn...)` (status + codec-backed body) | HTTP status | Full: `rest.ErrorAction` (`ErrorRespond`/`ErrorHandle`/`ErrorLog`) via `.WithAction(...)` — REST-local type, NOT shared with `events.ErrorAction` (each API layer keeps its own parallel vocabulary, same as `RouteMeta`/`ChannelMeta`) |
| `api/events` | `events.ErrorChannel[E,B](topic, codec, mapFn...)` | declared error-output topic | Full: `events.ErrorAction` via `.WithAction(...)` |
| `api/reqreply` | `reqreply.ErrorPattern[E,B](codec, mapFn...)` — auto-generates the `ErrorReplyMeta`-equivalent AsyncAPI reply-error channel/operation in the SAME declaration (`.WithCode`/`.WithDescription`/`.WithSchemaName`/`.WithChannelAddress`/`.WithOperationID` customize it) | reply-error channel (no HTTP status) | n/a — reqreply has no separate OnError-style hook; matched pattern always sends the typed payload as the reply, unmatched falls back to plain-text `err.Error()` |
| `api/mcp` | `mcp.ErrorPattern[E,B](codec, mapFn...)` on `NewTool` (Tool only — Resources/Prompts out of scope, protocol-level not business errors) | n/a (tool result, not HTTP/topic) | n/a — matched → structured `IsError:true` result; unmatched → plain-text `IsError:true` result |
| `adapters/websocket` | `websocket.ErrorFrame[E,B](codec, mapFn...) ErrorFrameRule` on BOTH `DuplexSocketAdapterOptions.ErrorFrames []ErrorFrameRule` AND `BroadcastSocketAdapterOptions.ErrorFrames []ErrorFrameRule` (plain slice, non-generic rule type — declares its OWN codec, independent of the socket's `Out` type; matched against upstream stream `Errors` only, never per-session write/encode failures) | broadcast to all sessions (no dedicated topic — broadcast IS the notification path) | Full: shares `events.ErrorAction` (`.WithAction(events.ErrorHandle)` + `.WithHandle(func(error))`) |
| SQL/Cache/File (`adapters/sql`/`adapters/redis`/`adapters/file`) | **NO dedicated declaration type** — compose the existing `OnError func(error)` hook with a declared `events.ErrorChannel.ErrorResponseFor(err)` lookup inline | n/a (internal boundary, no caller) | `OnError` IS the `handle` action; nil `OnError` is `log`; `respond`-equivalent achieved by composition, not new API |
| `adapters/nethttp.Call` (client-side) | **No new declaration** — reuses the SAME `rest.ErrorPattern` rules already on `RouteHandle` via `RouteHandle.DecodeErrorFor(status, body) (ErrorPatternResponse, bool, error)`, status-only match (no Go error to match via `errors.As` client-side) | HTTP status (client reads it off the wire) | Only `ErrorRespond`-tagged rules are eligible — `ErrorHandle`/`ErrorLog`-tagged rules are skipped (server doesn't guarantee those wrote the typed body); `Call` returns `nethttp.ErrorPatternResponse` on match, unchanged `UnexpectedStatusError` on no-match or decode failure |

### Rules

- **Matching is always type-only via `errors.As`, first-declared-rule-wins precedence** — identical
  across all five declaration types above. A new boundary that invents different matching semantics
  (e.g. string comparison, error codes) is a `bug`-severity finding.
- **Two modes on every `ErrorPattern`/`ErrorChannel`/`ErrorFrame` constructor**: direct (no `mapFn`,
  `E` must be assignable to `B`/`Out`) and mapped (`mapFn(E) (B, error)` provided). Missing either
  mode on a new declaration type is a `small` finding.
- **One matched pattern executes exactly ONE action** — never an implicit chain (e.g. never
  handle-then-respond). Verify test coverage proves this (a `.WithAction(ErrorHandle)` test asserting
  NO auto-write/auto-publish/auto-broadcast happened).
- **Do NOT propose new dedicated declaration types for SQL/Cache/File** — this was a deliberate
  design decision (see `references/history.md` Round 64, item G3): these are internal
  boundaries with no channel/topic concept of their own, and the existing `OnError` hook already
  generically covers "handle" — composing it with `events.ErrorChannel.ErrorResponseFor` achieves
  the "respond"-equivalent with zero new API surface. Flagging the absence of
  `sql.ErrorChannel`/`redis.ErrorChannel`/`file.ErrorChannel` as a gap is INCORRECT.
- **`reqreply.ErrorPattern` reconciles with the older spec-only `reqreply.ErrorReplyMeta`** —
  `ErrorReplyMeta` remains available UNCHANGED for spec-only declarations with no runtime dispatch
  (pure documentation/contract metadata, same role as `RouteMeta`). Do not flag `ErrorReplyMeta` as
  dead code or propose removing it.
- **Adapter wiring only touches HANDLER/ENCODE failure branches, never DECODE failure** — decode
  failures happen before the application handler runs, so there is no business error to match yet;
  they keep their existing typed-error (`rest.PathParamError`, `mqtt5.SubscribeError{KindDecode}`,
  etc.) behavior unchanged. A new adapter wiring that tries to match `ErrorPattern` against a decode
  failure is a `bug`-severity finding (wrong boundary).
- **`mqtt5.PublishAdapter` is the reference implementation for events pub/sub adapter wiring** —
  `mqtt.PublishAdapter` and `zeromq.PublishAdapter` both mirror it exactly (consult
  `handle.ErrorResponseFor(err)` before falling back to `OnError`). A new pub/sub adapter that skips
  this wiring (only has `OnError`, never consults `ErrorResponseFor`) reproduces a known-fixed gap —
  file at least a `small` finding.
- **Ports parity is REQUIRED and already proven** — `TestRESTPattern_ErrorStatus_ParityWithDirectRouteDeclaration`
  (`ports/port_test.go`) and `TestEventPattern_ErrorChannel_ParityWithDirectChannelDeclaration` lock
  that a `Pattern`-declared error rule (via `PluginRESTPattern`/`PluginEventPattern`) behaves
  identically to one declared directly via `rest.NewRoute`/`events.NewChannel` — no ports-specific
  wiring needed since `Pattern.Opts` is a thin `RouteOpt`/`ChannelOpt` pass-through. A NEW pattern
  type that fails this parity (e.g. silently drops error-pattern opts) is a `bug`.
- **Examples exist and must stay in sync**: `examples/adapters-mqtt5` (events.ErrorChannel via
  `ports.SinkPort`+`PublishAdapter`), `examples/websocket-duplex` (websocket.ErrorFrame broadcast),
  `examples/redis-cache` (SQL/Cache/File composition pattern). If you touch any of these files for an
  unrelated reason, verify the error-path demo section still builds/runs (`go build` + `go run`
  clean exit).
- **`declare → PluginXxxPattern → Bind` is consumption-style-agnostic — do not propose parallel
  plain-Go-only port constructors.** `SourcePort`/`SinkPort`/`LatestPort`/`DuplexPort` already
  satisfy the "plain idiomatic Go, no forge/gstream" consumption style via existing methods:
  `Stream(ctx)` + `stream.Drain` callback, `Start`/`Push`/`Close`, `Latest()`, `Inbound`/`Feed`.
  `ToolPort.SetFunc(func(ctx, In) (Out, error))` and `IOPort.Call(ctx, req) (Resp, error)` are the
  plain-Go equivalents of `SetPipeline`/`Connect` — same bound adapter, same `Pattern`-built handle,
  mutually exclusive with their stream-composed sibling (later call wins for `SetFunc`/`SetPipeline`).
  `IOPort.Call` returns `PortNoResponseError{Port}` if the adapter's stream emits zero items. Do not
  flag the absence of a separate non-generic "simple port" API — this IS it.
