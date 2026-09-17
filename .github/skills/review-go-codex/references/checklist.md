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
| Builder naming | `rest.Server`, `events.Client`, `reqreply.Server`, `forge.Registry` — consistent fluent builder pattern (`events.Builder` was renamed to `events.Client` by Decision 1 of `docs/design/d-0002-pubsub-workflow-simplification.md`; `reqreply.Server` is the NEWEST addition, unifying what a separate, now-deprecated `reqreply.Builder` did with dispatch/transport — mirrors `rest.Server`'s asymmetric role shape, not `events.Client`'s symmetric one, since reqreply's server/client roles are genuinely asymmetric like REST's, unlike pub/sub's) |
| MCP Builder | `mcp.Builder` with `NewBuilder(info)`, `Info()`, `MCPSpec()` — analogous to `OpenAPISpec()`/`AsyncAPISpec()` |
| `AddServer` | `rest.Server.AddServer(name, Server)`, `events.Client.AddServer(name, Server)`, AND `reqreply.Server.AddServer(name, ServerEntry)` all exist; description fallback on all three (`reqreply.ServerEntry` is the renamed AsyncAPI-entry alias — see `docs/design/d-0004-reqreply-workflow-simplification.md` — do not confuse with the dispatch-owning `reqreply.Server` type itself) |
| Security scheme declaration | REST and events CONVERGED on the same route/channel-level pattern (no longer a divergence): REST: `middleware.SecurityScheme(schemeName, scheme, scopes, codec) middleware.Middleware` / `rest.FromSecurityScheme(schemeName, rest.SecurityScheme, scopes) middleware.Middleware`, attached via `Route.Use(mw)` (`rest.WithSecurityScheme` was REMOVED by `docs/design/d-0001-rest-middleware-workflow-simplification.md` — no metadata-only registration exists anymore); events: `events.FromSecurityScheme(schemeName, events.SecurityScheme, scopes) middleware.Middleware`, attached via `Subscriber.Use(mw)`/`Publisher.Use(mw)` (`events.WithSecurityScheme` is deprecated-but-kept for backward-compat regression coverage, mirrors REST's OLD mechanism before its own Revision 2 removal — see `events.WithSecurityScheme`'s own doc comment). Neither package has a BUILDER-level security-scheme declaration anymore — do not look for `events.Client.AddSecurityScheme`, it does not exist |
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
| `PropertyParam`/`MergedPropertyParam[T]` (`api/reqreply`, `api/events`) | The "property" vocabulary axis (see `docs/design/d-0003-codec-declared-middlewares.md`'s Addendum) — protocol-neutral named metadata (MQTT5 User Properties) carried SEPARATELY from topic vars (independent namespace, never conflict-checked against `TopicParam`). Has `Required bool` (unlike `TopicParam`) — properties are conceptually optional metadata. `NewPropertyParam[T,V]` (required, hardcodes `RequiredField`) / `NewOptionalPropertyParam[T,V]` (optional, `OptionalField` — absence is not an error) mirror `NewTopicParam[T,V]`'s shape exactly, with the SAME field-for-field structure duplicated per-package (reqreply/events each own their copy, same precedent as `TopicParam` itself). `.WithCodec()` present. Attached to `Middleware[In,Out]` via `WithRequestProperty`/`WithResponseProperty` (reqreply) or `WithSubscribeProperty`/`WithPublishProperty` (events) — mirrors each package's own topic-axis naming convention (Request/Response vs. Subscribe/Publish) |

---

## 3. Builder Method Parity

### rest.Server vs events.Client vs reqreply.Server

| Method | rest.Server | events.Client | reqreply.Server |
|--------|-------------|----------------|------------------|
| `AddServer` | ✓ | ✓ | ✓ (`AddServer(name, ServerEntry)`) |
| `AddSecurityScheme` | ✗ (removed — see `middleware.SecurityScheme`/`rest.FromSecurityScheme` + `Route.Use`, route-level) | ✗ (removed on this side too — see `events.FromSecurityScheme` + `Subscriber.Use`/`Publisher.Use`, channel-level; both packages CONVERGED on the same declaration-level pattern, no longer a divergence) | ✗ (route-level only — `reqreply.WithSecurityScheme` is the ONLY declaration mechanism, mirroring reqreply's own never-had-a-builder-level-scheme history, not a regression) |
| `AddGlobalSecurity` | ✓ | ✓ | ✓ |
| `Attach`/`Serve` (dispatch) | n/a — `rest.Server` is spec-accumulation only, dispatch lives in `adapters/nethttp`/`chi`'s `AttachMux`/`AttachRouter` | n/a — `events.Client.Attach` + `Publish`/`Subscribe`/`ServeSubscribers` (dispatch IS on the same value) | ✓ `Server.Attach(ServerTransport)` + `Server.Serve(ctx)` — dispatch IS on the same value, mirrors `events.Client`'s unification more than `rest.Server`'s split (see `docs/design/d-0004-reqreply-workflow-simplification.md`) |
| `RegisteredTopics()`/`Topical` | n/a | n/a | ✓ reqreply-specific — used by `zeromq.AttachServer`/`AttachRouterServer` to validate topic/socket coverage upfront, before `Serve` |

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
| Security | REST: `middleware.SecurityScheme`/`rest.FromSecurityScheme` + `Route.Use` (route-level); events: `events.FromSecurityScheme` + `Subscriber.Use`/`Publisher.Use` (channel-level); both + `AddGlobalSecurity` (builder-level) | n/a — MCP security outside builder |

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
| `Route.ClientHandle()`/`SSERoute.ClientHandle()`/`Channel.ClientHandle()`/`reqreply.Route.ClientHandle()` | ALL apply the SAME declared `Formats`/`RequestFormats`/`SubscribeFormats`/`PublishFormats` `registerHandle`/`Register` applies server-side — type mismatch PANICS with the same message `FormatOptError` would return (infallible construction path, mirrors `mustAssertMergeFields`'s panic-on-misuse precedent) |
| `nethttp.CallOptions.RequestFormats`/`ResponseFormats any`, `nethttp.ConsumeOptions.Formats any` | PER-CALL type-erased format override (resolved generically inside `Call`/`CallWithHandle`/`consumeSSE`) — wins over route-declared `handle.RequestFormats`/`handle.Formats` for THIS call only, falling back to JSON when neither is set; mismatch → `nethttp.CallFormatOptError{Direction,Err}` |
| `mqtt5.CallOptions.RequestFormats`/`ResponseFormats any` (reqreply `Call`/`CallHandle`), `zeromq.CallOptions.RequestFormats`/`ResponseFormats any` (reqreply `Call`/`CallHandle`/`CallDealer`) | SAME per-call override mechanism as `nethttp.CallOptions`, mirrored for reqreply's bidirectional `Call` — mismatch wrapped in the package's own `CallError` (mqtt5: `Kind: KindEncode`/`KindDecode`; zeromq: plain `CallError{Err}`, no `Kind` field) rather than a dedicated `FormatOptError`-shaped type, matching each package's PRE-EXISTING error-wrapping convention for all other `Call`-time failures |

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
| `codex.ValidationErrors` / `codex.TemplateMismatchError` | `ResourceHandle.BuildURI`/`ExtractURIVars` — URI var codec/structural failure, surfaced DIRECTLY (no `api/mcp`-local wrapper type — `Resource[V,T]` is built on `codex.Template[V]`) |
| `mcp.PromptArgError{Name, Err}` | `PromptHandle.ValidateArgs` — arg codec failure |
| `mcp.MissingPromptArgError{Name}` | `PromptHandle.ValidateArgs` — required arg absent |

### reqreply package (`api/reqreply` `Server`/`Client`/`Attach` — see `docs/design/d-0004-reqreply-workflow-simplification.md`)

| Error type | When to use |
|------------|-------------|
| `reqreply.NoServerTransportAttachedError` | `Server.Serve` called before `Server.Attach` |
| `reqreply.NoClientTransportAttachedError` | `Client.Call`/`CallAsync` called before `Client.Attach` |
| `reqreply.ServerTransportAlreadyAttachedError` | `Server.Attach` called a second time |
| `reqreply.ClientTransportAlreadyAttachedError` | `Client.Attach` called a second time |
| `reqreply.TransportTypeMismatchError{Topic,Want,Got}` | `route`/`fn` value passed to a `ServerTransport`/`ClientTransport` method has the wrong dynamic type (reflection shim's type-safety check) |
| `reqreply.FutureTimeoutError` | `Future.Wait(ctx)` — ctx cancelled/deadline exceeded before the async reply resolved |
| `zeromq.MissingSocketError{Topic}` | `zeromq.AttachServer`/`AttachClient`/`AttachRouterServer`/`AttachDealerClient` — a registered route's topic has no corresponding entry in the `topic → socket` map (checked upfront at Attach time for server-side, lazily at Call time for client-side) |
| `reqreply.MissingSecurityMiddlewareError{Route,Scheme}` | `CheckCoverage` (called by the attached `ServerTransport` at Serve time) — a declared security scheme has no attached `HandleMW` implementation satisfying it |
| `reqreply.UnknownMiddlewareImplementationError{Route,Scheme}` | `Route.Register`/`ClientHandle` — a `HandleMW`/`ClientMW` call is PAIRED against a scheme name never `.Use()`'d on the same route (reverse-direction sibling to `CheckCoverage`) |
| `reqreply.DuplicateMiddlewareNameError{Route,Name}` | `Route.Register`/`ClientHandle` — two `Middleware[In,Out]` attachments on the same route share a `Declaration.Name` (D6(b), mirrors `rest.DuplicateMiddlewareNameError`) |
| `reqreply.AmbiguousMiddlewareAttachmentError{Name}` | `Route.Register`/`ClientHandle` — a bundled `Middleware[In,Out]` (carries a `WithReceive`/`WithSend` Fn) is ALSO attached via `Transform`/`ClientTransform` on the same route (D7) |
| `reqreply.ConflictingParamContributionError{Route,ParamName,FirstSource,SecondSource}` | `Route.Register`/`ClientHandle` — two contributions (Phase 1b's flat header-param mechanism AND/OR the codec-backed property/topic axis) declare the SAME topic-var/property name with differing `Required`/codec — the uniform, Round-18 conflict-detection algorithm (see `docs/design/d-0003-codec-declared-middlewares.md`'s Addendum); topic vars and properties are independent namespaces, never cross-checked |

All must be `errors.As`-navigable and implement `slog.LogValuer` (none wrap an inner error, so none need `Unwrap`, except `MissingSocketError` which is terminal too). Bare `fmt.Errorf` in `api/reqreply/{builder,client,future,middleware,middleware_declaration}.go` or the two `reqreply_transport.go` adapter files without a typed wrapper is a finding.

### adapters/nethttp client (`rest.Client.Call` via `nethttp.Attach`, and `nethttp.CallWithHandle`)

The public generic `nethttp.Call[Req,Resp]` free function was unexported (now internal `call`)
by Decision 6 of `docs/design/d-0002-pubsub-workflow-simplification.md` — the modern public surface
is `rest.Client.Call(ctx, route, req) (any, error)` (via `rest.NewClient()` + `nethttp.Attach`,
the preferred workflow) and `nethttp.CallWithHandle[Req,Resp]` (the handle-based escape hatch for
callers needing per-call `CallOptions`). Both delegate through the same internal error-producing
logic, so the table below applies identically to either.

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
| `nethttp.CallFormatOptError{Direction,Err}` | `CallOptions.RequestFormats`/`ResponseFormats` (or `ConsumeOptions.Formats`) set with formats for the wrong `Req`/`Resp`/`Event` type parameter — per-call analogue of `rest.FormatOptError` |

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

### General-purpose `Observability` decorator family (declare-time attachment, distinct from direct `Options.Observer` wiring)

A SEPARATE mechanism from the direct-field wiring above: a ready-made
`func(next Fn) Fn`-shaped closure attached via each boundary's OWN
general-purpose (unpaired) declaration surface —
`route.HandleMW(nil, fn)`/`route.ClientMW(nil, fn)` (REST, reqreply) or
`sub.SubscribeMW(nil, fn)`/`pub.PublishMW(nil, fn)` (events pub/sub) —
composable alongside a PAIRED security implementation on the same
route/channel.

| Package | Symbol | Shape | Notes |
|---|---|---|---|
| `adapters/nethttp` | `Observability(obs) func(http.Handler) http.Handler` | wraps the WHOLE call | calls `RecordRequest`+`TraceObserver` span itself (only place doing so for the wrapped call); `adapters/chi` reuses it UNCHANGED — no `chi.Observability` exists |
| `api/events` (CORE package, not an adapter) | `Observability[T](obs) func(func(ctx,T)error) func(ctx,T)error` | pub/sub | ctx-inject + Diagnostics drain ONLY — does NOT call `RecordSubscribe`/`RecordPublish` itself (done by the adapter wrappers below, or directly by an adapter's own subscribe/publish dispatch) |
| `adapters/mqtt5`/`adapters/mqtt` | `Observability[T](topic, obs) func(func(ctx,T)error) func(ctx,T)error` | pub/sub | THINNED wrappers delegating to `events.Observability[T]` for the shared ctx-inject/Diagnostics-drain part, then ADDITIONALLY calling `RecordSubscribe`/`RecordPublish` + a `TraceObserver` span, using `MessageFromContext` for adapter-specific direction detection — the genuinely adapter-specific remainder that cannot move into `api/events` |
| `adapters/zeromq` | — (REMOVED) | pub/sub | had ZERO adapter-specific code (ctx-inject + Diagnostics drain only) — consolidated into `api/events.Observability[T]` directly, no zeromq-owned wrapper remains; callers use `events.Observability[T]` directly |
| `api/reqreply` (CORE package, not an adapter) | `Observability[Req, Resp](obs) func(func(ctx,Req)(Resp,error)) func(ctx,Req)(Resp,error)` | reqreply | lives in the CORE package because zeromq's server+client AND mqtt5's client general decorators share this EXACT shape — ONE implementation, zero adapter duplication; deliberately does NOT call `RecordRequest`/start a `TraceObserver` span (every reqreply adapter transport already does both on every dispatch path — avoids double-counting); mqtt5's SERVER side has no equivalent (its raw pre-decode handler has no ctx to inject into — see `docs/features/observer.md`'s reqreply section) |

**Check**: do not flag `events.Observability[T]`'s lack of
`RecordSubscribe`/`RecordPublish`, or `reqreply.Observability`'s lack of
`RecordRequest`/`TraceObserver`, as missing coverage — both are
deliberate non-duplication decisions, documented in each symbol's own
godoc. This consolidation (breaking removal of the old
`zeromq.Observability[T]`, `mqtt5`/`mqtt` wrappers thinned to delegate to
`events.Observability[T]`) is ALREADY SHIPPED — see
`docs/design/d-0002-pubsub-workflow-simplification.md`'s own Addendum
("Observability Core Consolidation, `SecurityFunc` Retirement, and
`examples/events-api`") for the full design record. Do not propose
re-splitting `api/events.Observability[T]` back into a per-adapter
implementation.

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
- Structured error handling (`errors.As` on `codex.TemplateMismatchError`, `MissingPromptArgError`)
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
| `api/rest` (REST) | `NewPathParam[T]`/`NewRequiredQueryParam[T]`/etc. + `NewRequiredResponseHeaderParam[Resp]`/etc. | `PathParam`/`QueryParam`/etc. struct literals still work | `DecodeMerged` (decode) + `PathMergeFields()`/`QueryMergeFields()`/etc. (encode) | server (`AttachMux`/`AttachRouter` + `Serve`, or the escape-hatch `ServeOne`) + client (`rest.Client.Call` via `nethttp.Attach`) both covered | `rest.Client.Call` (route-based, SOLE public client entry point) / `nethttp.CallWithHandle` (lower-level handle-based escape hatch `Call` wraps) + `AttachMux`/`AttachRouter`/`ServeOne` auto-merge (server) — see `docs/design/d-0002-pubsub-workflow-simplification.md`'s Decision 6 (the old public generic `nethttp.Call[Req,Resp]`/`Handler`/`RegisterSSE` were REMOVED, not kept as "legacy"; `nethttp.CallHandle` was renamed to `CallWithHandle` by `docs/design/d-0001-rest-middleware-workflow-simplification.md`) | ✅ `examples/rest-nested-binary` + `TestNestedStructMergeFields_GetSetReachIntoSubstruct`/`TestGobBodyFormat_ComposesWithNestedMergeFields` — nested `Meta`/`Payload` sub-structs, Gob body via `format.NewTyped` projection | ✅ Reference implementation for the CORE API — see the port-binding-layer caveat below |
| `api/events` (pub/sub) | `NewTopicParam[T]` | `TopicParam` struct literals still work | `ChannelHandle.DecodeMerged` (decode) + `MergeFields()` (encode — single flat slice, no role-split needed, only ONE var destination) | subscriber (`Subscribe`/`SubscribeHandler` auto-merge) + publisher (`PublishHandle`) both covered, per transport | `mqtt5.PublishHandle`/`zeromq.PublishHandle`/`mqtt.PublishHandle` | ✅ `examples/events-nested-binary` — nested `Meta`/`Value` payload, Gob body via `format.NewTyped` projection | ✅ SHIPPED across `adapters/mqtt5`, `adapters/zeromq` (own pub/sub, G2), and `adapters/mqtt` v3 (G3). The property vocabulary axis (`PropertyParam`/`MergedPropertyParam[T]`, see §2) has its OWN parallel, now-symmetric merge mechanism when attached DIRECTLY to `NewChannel` — `ChannelHandle.PropertyMergeFields()`/`MergePropertyVars`/`EncodePropertyVars` (Round 138 fixed a bug where direct attachment registered spec metadata only, silently dropping the merge field — now fixed, mirrors this row's topic-var mechanism exactly, independent namespace) |
| `api/reqreply` (req/reply) — escape hatch (`Serve`/`Call`/`CallHandle`) AND PREFERRED `Server`/`Client`+`Attach` (see `docs/design/d-0004-reqreply-workflow-simplification.md`) | `NewTopicParam[T]` (Req-side only) — same declarations serve BOTH workflows, one route either way | `TopicParam` struct literals still work | `RouteHandle.DecodeMergedWithFormats` (decode) + `MergeFields()` (encode) | server (`mqtt5.Serve`/`AttachServer` auto-merge) + client (`mqtt5.CallHandle`/`zeromq.CallHandle`/`Client.Call`+`AttachClient`) both covered | `mqtt5.CallHandle`/`zeromq.CallHandle` (escape hatch) + `Client.Call`/`CallAsync` via `ClientCallOptions` (preferred workflow, per-call `RequestFormats`/`Formats` override) | ✅ nested-Req + Gob round trip test (`TestServeCallHandle_NestedReq_RoundTrip`); `TestAttachServer_AttachClient_MergeFields_RoundTrip` proves the SAME merge-field mechanism through `Attach` | ✅ SHIPPED for BOTH workflows — `Serve`/`Call`/`CallHandle` (escape hatch) and `AttachServer`/`AttachClient` (mqtt5), `AttachServer`/`AttachClient`/`AttachRouterServer`/`AttachDealerClient` (zeromq) ALL delegate to the SAME `serverTransport`/`clientTransport` internally (Phase 0b's delegation fix, `docs/design/d-0004-reqreply-workflow-simplification.md`'s own Addendum), so `DecodeMergedWithFormats`/`EncodeWithFormats`/`ErrorResponseFor`/per-call format overrides all now work identically through EITHER entry point — the "Attach can't merge/override/ErrorPattern" gap this row used to document is CLOSED, not merely narrowed. One documented exception carries over unchanged: `zeromq`'s SERVER side (`Serve`/`AttachServer`/`AttachRouterServer`) reads raw socket frames with no per-message topic string, architecturally cannot decode-merge server-side, regardless of workflow — `zeromq`'s CLIENT side (`CallHandle`/`Client.Call`+`AttachClient`/`AttachDealerClient`) is unaffected and merges normally. The property vocabulary axis has its OWN parallel, now-symmetric merge mechanism when attached DIRECTLY to `NewRoute` — `RouteHandle.PropertyMergeFields()`/`MergePropertyVars`/`EncodePropertyVars` (Round 138 fixed the same direct-attachment bug as the events row above; `adapters/mqtt5`'s Serve/Call got the matching dispatch-gate fix) |
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
| `rest.Client.Call`/`nethttp.CallWithHandle` (client-side) | **No new declaration** — reuses the SAME `rest.ErrorPattern` rules already on `RouteHandle` via `RouteHandle.DecodeErrorFor(status, body) (ErrorPatternResponse, bool, error)`, status-only match (no Go error to match via `errors.As` client-side) | HTTP status (client reads it off the wire) | Only `ErrorRespond`-tagged rules are eligible — `ErrorHandle`/`ErrorLog`-tagged rules are skipped (server doesn't guarantee those wrote the typed body); both `Call`/`CallWithHandle` return `nethttp.ErrorPatternResponse` on match, unchanged `UnexpectedStatusError` on no-match or decode failure |

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
- **Design guardrail: adapters implement wire protocols only — client-side ergonomics belong in
  `api/*`** (session review round-140, `docs/roadmap/error-handling-rest-events-reqreply.md`'s
  Topic 6 "Design guardrail" subsection). Any user-facing convenience helper that touches ONLY core
  `api/*` types — codecs, handles, declared patterns, or a core-layer interface like
  `ErrorPatternValuer` — belongs in `api/*`, never in `adapters/*`, EVEN WHEN only one adapter
  happens to implement that boundary today. `rest.ErrorPatternAs`/`HandleErrorPattern`/`Case` and
  `reqreply.ErrorPatternAs`/`HandleErrorPattern`/`Case` now live in `api/rest`/`api/reqreply`
  respectively (moved from `adapters/nethttp` and `adapters/mqtt5`+`adapters/zeromq` — the latter
  pair was byte-for-byte duplicated code, confirming the misplacement). When reviewing a NEW
  adapter-owned helper, ask: "could this be written using ONLY `errors.As`/a core-layer interface,
  with zero reference to any adapter-specific type?" If yes, it's misplaced — file at least a
  `small` finding (or `bug` if duplicated verbatim across 2+ adapters, as reqreply's was). Do NOT
  accept "only one adapter exists today" as a justification for adapter placement — the test is
  whether the logic is transport-INDEPENDENT, not how many adapters currently implement it.
  `api/events` (pub/sub), `mcp.ErrorPattern`, and `websocket.ErrorFrame` were all audited and
  confirmed to have NO equivalent gap (structurally different — no synchronous caller receives a
  matched error back to hand to a helper in those 3 cases) — do not propose adding one there.
- **Ports parity is REQUIRED and already proven** — `TestRESTPattern_ErrorStatus_ParityWithDirectRouteDeclaration`
  (`ports/port_test.go`) and `TestEventPattern_ErrorChannel_ParityWithDirectChannelDeclaration` lock
  that a `Pattern`-declared error rule (via `PluginRESTPattern`/`PluginEventPattern`) behaves
  identically to one declared directly via `rest.NewRoute`/`events.NewChannel` — no ports-specific
  wiring needed since `Pattern.Opts` is a thin `RouteOpt`/`ChannelOpt` pass-through. A NEW pattern
  type that fails this parity (e.g. silently drops error-pattern opts) is a `bug`.
- **Examples exist and must stay in sync**: `examples/rest-api`, `examples/events-api`, and
  `examples/reqreply-api` each have ONE consolidated `demo_error_pattern.go` (replaced several
  earlier, more narrowly-scoped demo files of the same session) covering that API's FULL mechanism
  end to end — every declaration mode (REST: `ErrorStatus`/Direct/Mapped; events/reqreply:
  Direct/Mapped), every `ErrorAction` (REST/events: Respond/Handle/Log; reqreply has none), all 3
  client-side recovery mechanisms (`ErrorPatternAs[B]`/`HandleErrorPattern`+`Case`/the declaration
  value's own `.Match`), a security-middleware combo, the `DeadLetter` two-tier fallback
  (events/reqreply), and the port/stream-adapter binding proof. `examples/websocket-duplex`
  (websocket.ErrorFrame broadcast) and `examples/redis-cache` (SQL/Cache/File composition pattern)
  remain the reference examples for their respective boundaries. If you touch any of these files
  for an unrelated reason, verify the error-path demo section still builds/runs (`go build` + `go
  run` clean exit).
- **`declare → PluginXxxPattern → Bind` is consumption-style-agnostic — do not propose parallel
  plain-Go-only port constructors.** `SourcePort`/`SinkPort`/`LatestPort`/`DuplexPort` already
  satisfy the "plain idiomatic Go, no forge/gstream" consumption style via existing methods:
  `Stream(ctx)` + `stream.Drain` callback, `Start`/`Push`/`Close`, `Latest()`, `Inbound`/`Feed`.
  `ToolPort.SetFunc(func(ctx, In) (Out, error))` and `IOPort.Call(ctx, req) (Resp, error)` are the
  plain-Go equivalents of `SetPipeline`/`Connect` — same bound adapter, same `Pattern`-built handle,
  mutually exclusive with their stream-composed sibling (later call wins for `SetFunc`/`SetPipeline`).
  `IOPort.Call` returns `PortNoResponseError{Port}` if the adapter's stream emits zero items. Do not
  flag the absence of a separate non-generic "simple port" API — this IS it.

---

## 14. Godoc & Documentation-Site Reference Integrity

Not caught by `go build`/`go vet`/`go test`/`staticcheck` — only by explicit grep sweeps. Run
this section whenever a review round follows (or itself performs) the removal/rename of any
exported symbol.

| Check | Expected |
|-------|----------|
| Godoc bracket-links | No `[Symbol]` comment reference anywhere in `*.go` points to a deleted/renamed exported (or same-package unexported) symbol. A dangling `[Foo]` silently renders as plain unlinked text on pkg.go.dev — it does not fail any build step, so it must be found by `grep -rn '\[OldSymbolName\]' --include='*.go' .` |
| `docs/**/*.md` code snippets | No fenced code block in `docs/features/`, `docs/guides/`, `docs/concepts/` calls a deleted/renamed function. These pages are never compiled or type-checked; staleness here is invisible to every other check in this checklist |
| `examples/**/*.go` comments | No example's explanatory comment describes itself as "still using the older X" for an X that has since been deleted, or references a symbol that no longer exists — a common artifact of migrating an example's CODE without revisiting its OWN prose comments |
| `.github/**/*.md` skill/instruction files | This skill's own `SKILL.md`/`checklist.md`/`history.md`, `go-codex.instructions.md`, and any other skill file (`add-a-new-adapter`, `plan-a-new-codex-feature`) don't reference the deleted/renamed symbol as if current |
| README tables | Any `examples/*/README.md` API-surface table lists the CURRENT entry point, not a deleted one |

### Rules

- **A green `go build ./...` proves nothing about documentation accuracy.** Godoc comments and
  Markdown code fences are not parsed by the Go toolchain; treat them as a SEPARATE verification
  surface with its own explicit sweep step, not an assumed side effect of "the code compiles."
- **Sweep the WHOLE repo, not just the package being changed.** A symbol removed from
  `adapters/nethttp` can be referenced from `middleware/`, `adapters/mqtt5/`, `api/rest/`, any
  `docs/*.md` page, any example, or this skill's own files — cross-package/cross-surface
  references are the norm for anything documented as a "reference implementation," not the
  exception. See `docs/design/d-0001-rest-middleware-workflow-simplification.md`'s "Lessons Learned" for a
  real case: deleting 4 functions left 31 dangling godoc links across 7 Go files PLUS stale,
  non-compiling snippets across 8 separate `docs/*.md` pages, found only via two full-repo sweep
  rounds after the code-level work was already declared complete.
- **A `MissingSecurityMiddlewareError`/`CheckCoverage`-style regression can hide behind a clean
  build too.** When a removed function bundled a validation/coverage check alongside its primary
  responsibility (see checklist item under "Removing an old API" in
  `.github/skills/plan-a-new-codex-feature/SKILL.md`), verify the replacement calls that check
  explicitly — write a reproduction test BEFORE trusting that "the new entry point already does
  this," the same way `docs/design/d-0001-rest-middleware-workflow-simplification.md`'s Lessons Learned
  documents `Serve`/`ServeSSE` silently losing `rest.CheckCoverage` when `Register`/`RegisterSSE`
  were deleted.
