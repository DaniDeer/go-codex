# MCP Server

> See also: [`api/mcp` on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/api/mcp) · [`adapters/mcpgo` on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/adapters/mcpgo)
>
> Runnable demo: [`examples/adapters-mcp`](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-mcp)

`api/mcp` + `adapters/mcpgo` bring the same **declare → register → handle** workflow to [Model Context Protocol (MCP)](https://modelcontextprotocol.io/) servers. The codec drives the MCP tool's `inputSchema` automatically — no duplicate struct-tag definitions.

## Quick start

```go
import (
    "github.com/DaniDeer/go-codex/api/mcp"
    "github.com/DaniDeer/go-codex/adapters/mcpgo"
    mcpgoserver "github.com/mark3labs/mcp-go/server"
)

// Layer 1: codec
var calcInputCodec = codex.Struct[CalcInput](
    codex.RequiredField("a", codex.Float64().Refine(validate.PositiveFloat), ...),
    codex.RequiredField("op",
        codex.String().Refine(validate.OneOf("+", "-", "*", "/")), ...),
)

// Layer 2: declare as package-level values
var calcTool = mcp.NewTool[CalcInput, CalcOutput]("calculate",
    calcInputCodec, calcOutputCodec,
    mcp.ToolMeta{Description: "Arithmetic on two non-negative numbers."},
)

var itemResource = mcp.NewResource[Item]("items://{id}", itemCodec,
    mcp.ResourceMeta{Name: "Item", MimeType: "application/json"},
    mcp.ResourceParam{Name: "id"}.WithCodec(codex.String().Refine(validate.NonEmptyString)),
)

var summaryPrompt = mcp.NewPrompt("summarize",
    mcp.PromptMeta{Description: "Ask the LLM to summarize content."},
    mcp.PromptArg{Name: "content", Required: true},
    mcp.PromptArg{Name: "style"},
)

// Register with builder — obtain typed handles
b := mcp.NewBuilder(mcp.Info{Name: "My Server", Version: "1.0.0"})
toolHandle, _   := calcTool.Register(b)
resHandle, _    := itemResource.Register(b)
promptHandle, _ := summaryPrompt.Register(b)

// Static spec (analogous to OpenAPISpec / AsyncAPISpec)
spec, _ := b.MCPSpec()
data, _ := json.MarshalIndent(spec, "", "  ")

// Layer 3: wire to mcp-go server
s := mcpgoserver.NewMCPServer(b.Info().Name, b.Info().Version)

mcpgo.RegisterTool(s, toolHandle, func(ctx context.Context, in CalcInput) (CalcOutput, error) {
    return svc.Calculate(ctx, in)
}, mcpgo.Options{Observer: obs})

mcpgo.RegisterResource(s, resHandle, func(ctx context.Context, uri string) (Item, error) {
    return svc.GetItem(ctx, uri)
}, mcpgo.Options{})

mcpgo.RegisterPrompt(s, promptHandle, func(ctx context.Context, args map[string]string) ([]mcpgo.PromptMessage, error) {
    return []mcpgo.PromptMessage{{Role: "user", Content: "Summarize: " + args["content"]}}, nil
}, mcpgo.Options{})
```

## Transport options

```go
// Stdio (local clients, e.g. Claude Desktop):
server.ServeStdio(s)

// Streamable HTTP (MCP 2025-03-26+, recommended for remote):
mcpgoserver.NewStreamableHTTPServer(s).Start(":8080")

// SSE over HTTP (legacy transport, older clients):
mcpgoserver.NewSSEServer(s, mcpgoserver.WithBaseURL("http://localhost:8080")).Start(":8080")
```

`mcpgo.RegisterTool`/`RegisterResource`/`RegisterPrompt` are fully transport-agnostic — they only wire handlers onto a `*server.MCPServer`; the transport is entirely determined by which serving call above you make on that same `s`.

**Stdio requires stdout reserved for the protocol.** When using `server.ServeStdio(s)`, stdout carries ONLY the JSON-RPC message stream — a real client (e.g. Claude Desktop) reads every byte on stdout as protocol data. Any stray write to stdout (a `fmt.Println`, a `slog.Logger` pointed at `os.Stdout`, etc.) corrupts the message framing. Point every logger — including any `stats.Observer` that logs — at `os.Stderr` in a stdio-serving process; see `examples/adapters-mcp`'s `runServer`/`runDemo` split for the reference pattern (demo-mode output stays on stdout since no client reads it there; serve-mode logs exclusively to stderr).

## Key behaviours

- **Codec-driven `inputSchema`**: the codec's `schema.Schema` is rendered to `json.RawMessage` as the tool's `inputSchema` — no `jsonschema:""` struct tags needed. Clients see exactly the constraints declared in the codec.
- **Input validation → `IsError: true`**: codec constraint failures are returned to the LLM as tool errors (`IsError: true`). The LLM sees field-level detail and can retry with corrected arguments.
- **Output encode errors → protocol error**: if the output codec validation fails, the adapter returns a protocol-level Go error (not a tool error). Use `errors.As(err, &mcp.ToolOutputError{})` to inspect.

## URI and prompt validation

```go
// ResourceHandle.BuildURI — validates URI variables before assembling
uri, err := resHandle.BuildURI(map[string]string{"id": "item-123"})

// ResourceHandle.ValidateURIVars — validate without building
err = resHandle.ValidateURIVars(map[string]string{"id": ""})
// → mcp.ResourceParamError{Name: "id", Value: "", Err: ...}

// PromptHandle.ValidateArgs — validate arg presence and codecs
err = promptHandle.ValidateArgs(map[string]string{"style": "bullet"})
// → mcp.MissingPromptArgError{Name: "content"} (required arg absent)
```

### Automatic URI-var extraction — `ExtractURIVars` / `RegisterResourceWithVars`

`mcpgo.RegisterResource`'s handler receives only the raw, concrete URI
string — extracting `{varName}` values and validating them against each
registered `ResourceParam` codec is left entirely to the application. Use
`ResourceHandle.ExtractURIVars` (or the `mcpgo.RegisterResourceWithVars`
wiring below) to close that gap in one call:

```go
// ResourceHandle.ExtractURIVars is the inverse of BuildURI: matches a
// received URI against the template and returns the extracted vars,
// ALREADY validated via ValidateURIVars.
vars, err := resHandle.ExtractURIVars("items://item-123")
// vars["id"] == "item-123"
// err is mcp.ResourceURIMismatchError on a structural mismatch, or
// mcp.ResourceParamError/MissingResourceVarError on a codec failure.

// mcpgo.RegisterResourceWithVars wires ExtractURIVars automatically —
// the handler receives the extracted+validated vars map as a third
// argument, no manual parsing needed:
mcpgo.RegisterResourceWithVars(s, resHandle, func(ctx context.Context, uri string, vars map[string]string) (Item, error) {
    return svc.GetItem(ctx, vars["id"])
}, mcpgo.Options{})
```

`mcpgo.RegisterResource`/`ResourceHandlerFunc` remain available unchanged
— this is an ADDITIVE convenience (a new function/type pair), not a
breaking change. Extraction/validation failures are routed through the
same `RecordRequest(..., 500, ...)` observer path decode/encode errors
already use.

## Error-path ergonomics — `ErrorPattern`

MCP tool results have no HTTP status or reply topic — `mcp.ErrorPattern` is
the tool-call analogue of [`rest.ErrorPattern`](rest-api.md#error-path-ergonomics-errorstatus--errorpattern)
and [`events.ErrorChannel`](events.md#error-path-ergonomics-errorchannel):
declare a codec-backed typed error payload for a matched handler error type,
returned as a structured tool result instead of a bare error string.

```go
type NotFoundError struct{ ID string }
func (e NotFoundError) Error() string { return "not found: " + e.ID }

type ErrorPayload struct {
    Code    string
    Message string
}

tool := mcp.NewTool[SearchIn, SearchOut]("search", inCodec, outCodec,
    mcp.ErrorPattern[NotFoundError, ErrorPayload](errorPayloadCodec,
        func(e NotFoundError) (ErrorPayload, error) {
            return ErrorPayload{Code: "not_found", Message: e.Error()}, nil
        },
    ),
)
```

- **Direct mode** (no map function): `E` must itself be assignable to the
  declared payload type `B`.
- **Mapped mode** (map function provided): the map function converts `E`
  into `B`.
- **Matching**: type-only via `errors.As`; the first declared `ErrorPattern`
  (in `NewTool` option order) whose type matches wins — the same
  deterministic precedence used by REST/events/reqreply.
- **Scope**: `ErrorPattern` only applies to errors returned by the
  application handler function (business logic) — input-decode failures
  and output-encode failures are different concerns and unaffected.
- **`ToolHandle.ErrorResponseFor(err) (ErrorPatternResponse, bool, error)`**
  is the lookup accessor `adapters/mcpgo.ToolHandler` consults.

### Adapter wiring (`adapters/mcpgo`)

`mcpgo.ToolHandler`'s handler-error branch consults
`handle.ErrorResponseFor(err)` before falling back to
`mcp.NewToolResultError(err.Error())`:

- matched → returns `mcp.NewToolResultStructured(json.RawMessage(body), string(body))`
  with `IsError: true` — a structured typed result, still reported as an
  error to the LLM, but with parseable JSON content instead of a bare string;
- unmatched (or a mapper/encode failure within the matched pattern) → falls
  back to the existing plain-text `mcp.NewToolResultError(err.Error())`
  behavior unchanged (backward compatible).

## Structured errors

| Error type | Returned by | When |
|---|---|---|
| `mcp.ToolInputError{Name, Err}` | `ToolHandle.Decode` | input codec validation failure |
| `mcp.ToolOutputError{Name, Err}` | `ToolHandle.Encode` | output codec validation failure |
| `mcp.ResourceParamError{Name, Value, Err}` | `ResourceHandle.BuildURI` / `ValidateURIVars` / `ExtractURIVars` | URI var codec failure |
| `mcp.MissingResourceVarError{Name}` | `ResourceHandle.BuildURI` / `ValidateURIVars` / `ExtractURIVars` | required URI var absent |
| `mcp.ResourceURIMismatchError{Template, URI}` | `ResourceHandle.ExtractURIVars` | received URI doesn't match the template's structure |
| `mcp.ResourceEncodeError{URI, Err}` | `ResourceHandle.Encode` | resource encode failure |
| `mcp.PromptArgError{Name, Err}` | `PromptHandle.ValidateArgs` | arg codec failure |
| `mcp.MissingPromptArgError{Name}` | `PromptHandle.ValidateArgs` | required arg absent |

## Observer

```go
mcpgo.RegisterTool(s, toolHandle, handler, mcpgo.Options{Observer: obs})
// obs.RecordRequest("tool", "calculate", 200, duration) — per call
// obs.RecordValidationError("input", constraint, field) — per failing field
```

Observer location values: `"input"` for tool argument decode/validation; `"prompt.args"` for prompt argument codec failures.

## See also

- [examples/adapters-mcp](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-mcp) — full demo: Tools, Resources, Prompts, MCPSpec, observer
