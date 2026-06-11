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
| `AddSecurityScheme` | Both `rest.Builder` and `events.Builder` have `AddSecurityScheme(name, SecurityScheme)` |
| `AddGlobalSecurity` | Both builders have `AddGlobalSecurity(reqs...)` |
| Server description fallback | Both builders fall back `Server.Description = name` when empty |

---

## 2. Param Type Consistency

| Check | Expected |
|-------|----------|
| `PathParam` | No `Required` field (always required by OpenAPI spec); godoc explains why |
| `TopicParam` | No `Required` field (topic vars always required); godoc explains why |
| `ResourceParam` | No `Required` field (URI vars always required, same rationale as PathParam/TopicParam); godoc must explain |
| `PromptArg` | Has `Required bool` — prompt args are optional by default; `Required: true` triggers `MissingPromptArgError` |
| `QueryParam`, `CookieParam`, `HeaderParam` | Have `Required bool` |
| `ResponseHeaderParam`, `ResponseCookieParam` | Have appropriate fields; no `Required` (response params are always present when set) |
| `.WithCodec(c codex.Codec[string])` | Present on all 7 REST/events param types: PathParam, QueryParam, CookieParam, HeaderParam, ResponseHeaderParam, ResponseCookieParam, TopicParam |
| `.WithCodec(c codex.Codec[string])` on MCP | Present on `ResourceParam` and `PromptArg` — mirrors TopicParam pattern |
| Pointer-free codec setting | No usage of `Codec: &codec` in the library itself; examples must use `.WithCodec()` |

---

## 3. Builder Method Parity

### rest.Builder vs events.Builder

| Method | rest.Builder | events.Builder |
|--------|-------------|----------------|
| `AddServer` | ✓ | ✓ |
| `AddSecurityScheme` | ✓ | ✓ |
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
| Security | `AddSecurityScheme`, `AddGlobalSecurity` | n/a — MCP security outside builder |

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

### adapters/mcpgo adapter behavior (distinct from REST/events)

| Situation | How mcpgo handles it |
|-----------|----------------------|
| Input decode/validation failure | Returns `mcp.NewToolResultError(err.Error())` — `IsError: true` result, **not** a Go error |
| Handler error | Returns `mcp.NewToolResultError(err.Error())` — `IsError: true` result |
| Output encode failure | Returns `(nil, err)` — protocol-level Go error (server contract violation) |

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

### Rules

- `SecurityObserver` must be guarded: `if so, ok := obs.(stats.SecurityObserver); ok { ... }` — **never** embedded in `Observer`
- `PipelineObserver.RecordApply` must be called for every function in a pipeline, including `Map`/`Filter`/etc.
- Adapters must call `Observer` on every code path — including early-exit error paths — not just the happy path
- `NoopObserver` satisfies all four interfaces; use as default when no observer provided
- **`adapters/mcpgo` observer locations**:
  - `"input"` — tool argument decode/validation failure (`stats.ReportErrors(obs, "input", err)`)
  - `"prompt.args"` — prompt argument codec failure (`stats.ReportErrors(obs, "prompt.args", err)`)
  - `RecordRequest("tool"|"resource"|"prompt", name, 200|400|500, d)` — one call per tool/resource/prompt invocation

**Check**: in each adapter (`adapter.go`), verify Observer is called in both success and error branches.

---

## 9. Unit Test Coverage

### Per-package expectations

| Package | Test file | Expected coverage |
|---------|-----------|-------------------|
| `api/rest` | `builder_test.go` | Each param type: WithCodec, Validate* happy+error path |
| `api/events` | `builder_test.go` | TopicParam WithCodec; ValidateTopicVars missing key → MissingTopicVarError; WithSubscribeFormats/WithPublishFormats |
| `api/mcp` | `builder_test.go` | Tool/Resource/Prompt Register happy+error; ToolHandle.Decode/Encode; ResourceHandle.BuildURI/ValidateURIVars; PromptHandle.ValidateArgs; ResourceParam.WithCodec; PromptArg.WithCodec; Tags flow to handles + MCPSpec; all typed errors via errors.As |
| `adapters/mcpgo` | `adapter_test.go` | ToolHandler success; input error → IsError=true; handler error → IsError=true; output error → protocol error; observer RecordRequest 200/400/500; ResourceHandler happy+error+encodeError+template detection; PromptHandler happy+missingArg+handlerError |
| `render/jsonschema` | `jsonschema_test.go` | zero schema → nil; string type; object with properties; enum; numeric constraints |
| `forge` | `forge_test.go` | FunctionKindScalar; PipelineInfo.WithAuthor/WithApproval; collection ops |
| `render/pipeline` | `pipeline_test.go` | governance fields emitted when set; omitted when empty |
| `render/asyncapi/v2` | any `*_test.go` | server insertion order deterministic |
| `render/asyncapi/v3` | any `*_test.go` | server insertion order deterministic |

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
