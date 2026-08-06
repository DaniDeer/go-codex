# MCP Tool ↔ REST Client Bridge — `adapters/mcprest`

> **Status:** Design complete — not yet implemented.
> [← Back to Roadmap](index.md)
>
> See also: [MCP feature docs](../features/mcp.md) · [Security & Authentication](../features/security.md) · [`examples/go-edge-models/docker/registry`](https://github.com/DaniDeer/go-codex/tree/main/examples/go-edge-models/docker/registry) — the driving use case

## Motivation

`examples/go-edge-models/docker/registry` declares a REST API purely as
`codex.Codec`s + `rest.Route` values, and consumes it client-side via
`adapters/nethttp.Call`/`CallHandle` — including security scheme
declarations and a `CredentialFunc` for the registry auth-challenge flow.
Wrapping the SAME client capability as an MCP tool (so an LLM agent can call
`get_tags`/`get_image_metadata` directly) today requires hand-writing the
glue between `apimcp.ToolHandle`'s `mcpgo.HandlerFunc[In, Out]` shape and
`nethttp.CallHandle`'s client-call shape — boilerplate that will recur every
time a REST API (already declared as `rest.Route`s, per go-codex's normal
workflow) needs to be exposed to an LLM as a tool.

This is a genuinely common shape: **any go-codex REST client (already built
from a declared `rest.Route`) can become an MCP tool with a single small
adapter function**, because `nethttp.CallHandle[Req, Resp](ctx, client,
baseURL, handle, req, opts) (Resp, error)` already has almost the exact
shape `mcpgo.HandlerFunc[Req, Resp] = func(ctx, in Req) (Resp, error)`
expects — only currying is needed (`client`, `baseURL`, `handle`, `opts`
fixed; `ctx`/`req` per call) for the case where the tool's input/output
shape IS the route's Req/Resp shape.

That said, an LLM-facing tool's ideal input/output shape often differs
from the wire request/response shape (fewer fields, flattened structure,
renamed for LLM readability, computed/derived values) — both shapes are
ALREADY codec-defined (the tool's via `apimcp.NewTool`'s `inputCodec`/
`outputCodec`, the route's via `rest.NewRoute`'s `reqCodec`/`respCodec`),
so the bridge should also support an optional MAPPING layer between them,
not just the identity case.

## Scope decisions (what's in Phase 1, what's deferred)

| In scope | Out of scope |
|---|---|
| `mcprest.MappedToolHandler[ToolIn, ToolOut, Req, Resp](client, baseURL, handle, opts, toReq, fromResp) mcpgo.HandlerFunc[ToolIn, ToolOut]` — the general bridge with optional mapping between the MCP tool's shape and the REST route's wire shape | Automatic/reflection-based field mapping between ToolIn/Req or Resp/ToolOut — go-codex never does implicit struct-to-struct mapping (see the explicit getter/setter convention throughout `codex.Field`); the mapping functions are always hand-written by the caller, the bridge just wires them in with proper error typing/ctx forwarding |
| `mcprest.ToolHandler[Req, Resp](client, baseURL, handle, opts) mcpgo.HandlerFunc[Req, Resp]` — the zero-boilerplate convenience for the common case where the tool's shape IS the route's Req/Resp shape (ToolIn=Req, ToolOut=Resp), implemented internally as `MappedToolHandler` with identity mappers — mirrors `Call`/`CallHandle`'s convenience-vs-general relationship | A THIRD, in-between "partial mapping" helper (e.g. map only the request, not the response) — not requested, and the general `MappedToolHandler` already covers this by passing an identity function for the side that doesn't need mapping |
| `mcprest.ToolRequestMapError{Method, Path, Err}` / `mcprest.ToolResponseMapError{Method, Path, Err}` — new structured errors (both `slog.LogValuer`) wrapping a failing `toReq`/`fromResp` mapper, kept DISTINCT from the underlying REST call's own typed errors (which continue to forward unchanged) | Retrying or auto-recovering from a mapping failure — a mapper failure is a programming error in the mapping function itself (e.g. a nil pointer, an out-of-range derived value), not a transient condition; it surfaces once, like any other input-decode error |
| `mcprest.DefaultErrorPatterns() []apimcp.ToolOpt` — auto-derived, OPT-IN `apimcp.ErrorPattern` declarations mapping the common `adapters/nethttp`/`api/rest` client error types (`UnexpectedStatusError`, `RequestError`, `RequestBuildError`, `ResponseBodyError`, `rest.SecurityCredentialError`) AND the two new mapper error types above into ONE structured `RESTClientErrorPayload`, so the calling LLM sees HTTP status/body/mapping context instead of a flat error string | A NEW generic error-response mechanism — this reuses the EXISTING `apimcp.ErrorPattern`/`errors.As`/first-declared-rule-wins convention unchanged, purely as a convenience set of pre-built rules |
| Bulk-generating a whole MCP toolset from an ENTIRE `rest.Builder` (many routes at once) | **Rejected for Phase 1 and likely indefinitely** — every declarative construct in go-codex (`rest.NewRoute`, `apimcp.NewTool`, `events.NewChannel`, `ports.NewToolPort`) is singular by design, composed by calling the constructor once per thing; there is no existing "bulk from a Builder" precedent anywhere to mirror. One route → one tool, called once per tool wanted, stays consistent |
| A FIXED `nethttp.CallOptions` (including `CredentialFunc`) configured ONCE when building the tool's handler — matches EVERY existing client-adapter binding precedent (`nethttp.CallAdapter`, `DrainCallAdapter`, `mqtt5.CallAdapter`, `zeromq.CallAdapter` all configure a single `CallOptions`/`CredentialFunc` at construction, reused for every call) | A generic, cross-adapter "per-session credential" mechanism. Evaluated and rejected: MCP's long-lived `ClientSession` is architecturally different from REST/MQTT/ZeroMQ's per-call `ctx` model, and no concrete need exists in more than one adapter today. The dynamic case remains fully achievable with ZERO new API — a `CredentialFunc` closure can already read `server.ClientSessionFromContext(ctx).SessionID()` (an existing `mcp-go` SDK helper) and look up a credential in an app-owned store, exactly the same ctx-introspection idiom `stats.ObserverFromContext` already establishes. Documented as a "recipe" in this package's doc comment, not new API |
| Observer integration reuses `nethttp.CallOptions.Observer` (fires `RecordRequest` for the underlying HTTP call) and `mcpgo.Options.Observer` (fires `RecordRequest("tool", ...)` for the MCP-level call) UNCHANGED — both already exist | A new `stats.Observer` extension. Not needed — the bridge introduces no new kind of lifecycle event; it is pure composition of two ALREADY-observed layers |

## Toolchain / dependency decisions

**New standalone package, `adapters/mcprest`** — NOT inside `adapters/mcpgo`
and NOT inside `adapters/nethttp`. Both existing adapters are documented to
import a narrow, fixed set of packages (`adapters/mcpgo` → `api/mcp`,
`stats`, `github.com/mark3labs/mcp-go`; `adapters/nethttp` → `api/rest`,
`net/http`, `format`, `stats`) with NO cross-adapter imports today. A new
package that imports BOTH `adapters/nethttp` and `adapters/mcpgo` (plus
`api/rest`, `api/mcp`) keeps each of those two adapters transport-pure and
independently importable, while still being a first-class go-codex package
(not just a documented example pattern) — justified because this bridge is
expected to recur across every future go-codex-based project that exposes
an existing REST client to an LLM.

## API surface

```go
// adapters/mcprest/bridge.go (new package)

// MappedToolHandler returns an mcpgo.HandlerFunc[ToolIn, ToolOut] that
// proxies each MCP tool call to an outbound REST request via
// nethttp.CallHandle, mapping between the tool's own In/Out shape and the
// REST route's Req/Resp wire shape via the supplied toReq/fromResp
// functions. Both mapper functions are fallible — return a non-nil error
// to abort the call before/after the underlying HTTP request; errors are
// wrapped as [ToolRequestMapError]/[ToolResponseMapError] respectively
// (kept distinct from the underlying REST call's own typed errors, which
// continue to forward unchanged via errors.As).
//
// handle's declared path/query/header/cookie merge fields and security
// schemes apply exactly as any other nethttp client call would. opts
// (including opts.CredentialFunc) is FIXED for every call made through the
// returned handler — see the package doc comment for the ctx/session
// recipe if a per-caller credential is ever needed.
//
// Use [ToolHandler] instead when the tool's In/Out IS the route's Req/Resp
// (the common case) — it is MappedToolHandler with identity mappers.
func MappedToolHandler[ToolIn, ToolOut, Req, Resp any](
    client *http.Client,
    baseURL string,
    handle *rest.RouteHandle[Req, Resp],
    opts nethttp.CallOptions,
    toReq func(ToolIn) (Req, error),
    fromResp func(Resp) (ToolOut, error),
) mcpgo.HandlerFunc[ToolIn, ToolOut]

// ToolHandler is the zero-boilerplate convenience for the common case
// where the MCP tool's In/Out IS the REST route's Req/Resp — no mapping
// needed. Implemented as [MappedToolHandler] with identity mapper
// functions.
//
// Pair with an apimcp.Tool[Req, Resp] built from the SAME Req/Resp codecs
// already used for the REST route (rest.NewRoute and apimcp.NewTool both
// just take a codex.Codec[Req]/[Resp] — reuse the identical package-level
// codec values, no re-derivation):
//
//	restHandle := registry.GetTagsRoute.ClientHandle()
//	toolHandle, _ := apimcp.NewTool[registry.GetTagsReq, registry.TagsList](
//	    "get_tags", reqCodec, respCodec,
//	    mcprest.DefaultErrorPatterns()...,
//	).Register(mcpBuilder)
//	tool, handlerFn := mcpgoAdapter.ToolHandler(toolHandle,
//	    mcprest.ToolHandler(httpClient, baseURL, restHandle, nethttp.CallOptions{
//	        CredentialFunc: myFixedCredentialFunc,
//	    }),
//	    mcpgo.Options{},
//	)
func ToolHandler[Req, Resp any](
    client *http.Client,
    baseURL string,
    handle *rest.RouteHandle[Req, Resp],
    opts nethttp.CallOptions,
) mcpgo.HandlerFunc[Req, Resp]
```

```go
// adapters/mcprest/errors.go (new package, same package as above)

// ToolRequestMapError wraps a failing toReq mapper function passed to
// [MappedToolHandler] — distinct from the underlying REST call's own
// typed errors, which continue to forward unchanged.
type ToolRequestMapError struct {
    Method string // handle.Descriptor.Method, for context
    Path   string // handle.Descriptor.Path, for context
    Err    error
}

func (e ToolRequestMapError) Error() string
func (e ToolRequestMapError) Unwrap() error
func (e ToolRequestMapError) LogValue() slog.Value

// ToolResponseMapError wraps a failing fromResp mapper function passed to
// [MappedToolHandler] — same shape as [ToolRequestMapError].
type ToolResponseMapError struct {
    Method string
    Path   string
    Err    error
}

func (e ToolResponseMapError) Error() string
func (e ToolResponseMapError) Unwrap() error
func (e ToolResponseMapError) LogValue() slog.Value

// RESTClientErrorPayload is the structured MCP tool-error payload used by
// [DefaultErrorPatterns] for REST-client-side failures (never business
// logic errors — those remain the caller's own [apimcp.ErrorPattern]
// declarations, matched first if declared before DefaultErrorPatterns'
// rules in NewTool's opts).
type RESTClientErrorPayload struct {
    // Kind identifies which client error type matched: "unexpected_status",
    // "request", "request_build", "response_body", "security_credential",
    // "request_map", or "response_map".
    Kind string
    // StatusCode is populated only when Kind == "unexpected_status".
    StatusCode int
    // Body is the raw response body, populated only when
    // Kind == "unexpected_status".
    Body string
    // Message is err.Error() — always populated.
    Message string
}

// DefaultErrorPatterns returns [apimcp.ToolOpt] values mapping every
// exported adapters/nethttp and api/rest CLIENT error type, PLUS
// [ToolRequestMapError]/[ToolResponseMapError], into
// [RESTClientErrorPayload] — pass alongside [apimcp.NewTool]'s other opts.
// Purely additive: declare your OWN [apimcp.ErrorPattern] BEFORE these in
// the opts list to override one of these mappings for a specific tool
// (first-declared-rule-wins, per [apimcp.ErrorPattern]'s existing
// precedence rule).
func DefaultErrorPatterns() []apimcp.ToolOpt
```

## Usage with `ports.ToolPort` (already works, no new code)

`mcprest.ToolHandler`/`MappedToolHandler` return exactly the
`func(context.Context, In) (Out, error)` shape [`ports.ToolPort.SetFunc`]
already accepts — confirmed identical to `mcpgo.HandlerFunc[In, Out]`. No
new `ports` plumbing is needed for this to compose:

```go
domainPort := ports.NewToolPort[registry.GetTagsReq, registry.TagsList](
    "get_tags", reqCodec, respCodec,
)
domainPort.SetFunc(mcprest.ToolHandler(client, baseURL, restHandle, callOpts))

toolHandle, _ := domainPort.PluginMCPPattern(ports.MCPPattern{Builder: mcpBuilder})
domainPort.Bind(ctx, mcpgo.ToolPipelineAdapter(mcpServer, toolHandle, mcpgo.Options{}))
```

Because the wrapped function is bound to a `ToolPort` — not directly to
`mcpgo.ToolHandler` — the SAME REST-backed logic can ALSO be exposed as a
REST endpoint (`PluginRESTPattern`) or a reqreply endpoint
(`PluginReqReplyPattern`) from the SAME port declaration, simultaneously,
with no duplicated business logic. This works for BOTH `ToolHandler` (the
identity case) and `MappedToolHandler` (the mapped case) — the ToolPort's
own `In`/`Out` generic params are just whatever the bridge itself returns.

## Structured errors (all implement `slog.LogValuer`)

Two NEW error types this round (added for the mapper-function support):
`ToolRequestMapError{Method, Path, Err}` and
`ToolResponseMapError{Method, Path, Err}` — both implement `Error()`,
`Unwrap()`, and `LogValue()` per the mandatory pattern (see
`codex/errors.go`'s reference shape). They wrap a failing `toReq`/
`fromResp` mapper function specifically, kept DISTINCT from the underlying
REST call's own typed errors.

`mcprest.ToolHandler`/`MappedToolHandler`'s returned closure forwards
whatever `nethttp.CallHandle` itself returns UNCHANGED for the actual HTTP
call — every one of those error types (`UnexpectedStatusError`,
`RequestError`, `RequestBuildError`, `ResponseBodyError`,
`rest.SecurityCredentialError`, `rest.PathParamError`/`QueryParamError`/
etc.) already implements `slog.LogValuer`. `RESTClientErrorPayload` is a
plain OUTPUT payload type (same category as any other MCP tool's `Out`/
error-pattern payload type) — not an internal go-codex error, so it does
not need `LogValue()`.

## Observer integration

No new `stats.Observer` extension. Reuses two ALREADY-shipped observer
call sites unchanged:
- `nethttp.CallOptions.Observer` (or `stats.ObserverFromContext(ctx)` when
  left nil) — fires `RecordRequest(method, path, statusCode, duration)`
  for the underlying outbound HTTP call.
- `mcpgo.Options.Observer` (or ctx default) — fires
  `RecordRequest("tool", name, statusCode, duration)` for the MCP-level
  call.

Both fire for a single MCP tool invocation that goes through this bridge —
document this composition (one tool call = two `RecordRequest` events, one
per layer) in `docs/features/mcp.md`'s new subsection, so a caller isn't
surprised in dashboards/logs. A mapper failure (`ToolRequestMapError`/
`ToolResponseMapError`) happens OUTSIDE the underlying HTTP call, so it
does NOT produce a `nethttp` `RecordRequest` event — only the
`mcpgo.Options.Observer`'s own `RecordRequest("tool", name, 500, ...)` for
the overall tool call fires, same as any other handler error.

## Unit test plan

| Test | Verifies |
|---|---|
| `TestToolHandler_HappyPath_ForwardsToCallHandle` | Returned handler calls `nethttp.CallHandle` with the exact client/baseURL/handle/opts and returns its result unchanged (identity mapping) |
| `TestToolHandler_ErrorPath_ForwardsUnderlyingErrorUnchanged` | A `nethttp.UnexpectedStatusError` from the underlying call surfaces via `errors.As` on the handler's returned error, unchanged |
| `TestToolHandler_FixedCallOptions_AppliedToEveryCall` | The same `CredentialFunc`/`ExtraHeaders` configured at construction apply identically across multiple handler invocations |
| `TestMappedToolHandler_HappyPath_MapsInputAndOutput` | `toReq`/`fromResp` are called with the right values and their results flow through correctly |
| `TestMappedToolHandler_ToReqError_WrapsAsToolRequestMapError` | A `toReq` error is wrapped as `ToolRequestMapError` with `Method`/`Path` populated from `handle.Descriptor`, `errors.As`-reachable, and the underlying REST call is NEVER made |
| `TestMappedToolHandler_FromRespError_WrapsAsToolResponseMapError` | A `fromResp` error (after a SUCCESSFUL REST call) is wrapped as `ToolResponseMapError`, `errors.As`-reachable |
| `TestMappedToolHandler_UnderlyingCallError_ForwardsUnchanged` | When `toReq` succeeds but the REST call itself fails, the original `nethttp` error type is returned UNCHANGED (not wrapped) |
| `TestToolRequestMapError_LogValue` / `TestToolResponseMapError_LogValue` | `LogValue()` returns `slog.KindGroup` with `method`/`path`/`err` keys — not just a non-empty Kind |
| `TestDefaultErrorPatterns_UnexpectedStatusError_MapsStatusAndBody` | `RESTClientErrorPayload{Kind:"unexpected_status", StatusCode, Body}` populated correctly |
| `TestDefaultErrorPatterns_RequestError_MapsMessage` | `RESTClientErrorPayload{Kind:"request", Message}` populated correctly |
| `TestDefaultErrorPatterns_SecurityCredentialError_Maps` | `RESTClientErrorPayload{Kind:"security_credential", Message}` populated correctly |
| `TestDefaultErrorPatterns_ToolRequestMapError_Maps` | `RESTClientErrorPayload{Kind:"request_map", Message}` populated correctly |
| `TestDefaultErrorPatterns_ToolResponseMapError_Maps` | `RESTClientErrorPayload{Kind:"response_map", Message}` populated correctly |
| `TestDefaultErrorPatterns_CustomPatternDeclaredFirst_Wins` | A caller's own `apimcp.ErrorPattern` for one of these types, declared BEFORE `DefaultErrorPatterns()` in `NewTool`'s opts, wins (first-declared-rule-wins) |
| `Example_toolHandler` (pkg.go.dev) | End-to-end wiring example: REST route → `ClientHandle` → `mcprest.ToolHandler` → `mcpgo.ToolHandler` → registered MCP tool |
| `Example_mappedToolHandler` (pkg.go.dev) | Same, but with a tool In/Out shape that differs from the route's Req/Resp, using `MappedToolHandler` |
| `TestToolHandler_ComposesWithToolPortSetFunc` | `ports.NewToolPort[...].SetFunc(mcprest.ToolHandler(...))` compiles and the port correctly invokes the bridge on `Bind`+dispatch — confirms the "Usage with `ports.ToolPort`" shape-match claim with a real test, not just a doc claim |

## Files to create

| File | Responsibility |
|---|---|
| `adapters/mcprest/bridge.go` | `MappedToolHandler[ToolIn, ToolOut, Req, Resp]` (general) + `ToolHandler[Req, Resp]` (identity convenience) |
| `adapters/mcprest/bridge_test.go` | Happy-path + error-path + fixed-options + mapper-error tests for both constructors |
| `adapters/mcprest/errors.go` | `ToolRequestMapError`, `ToolResponseMapError`, `RESTClientErrorPayload`, its codec, `DefaultErrorPatterns()` |
| `adapters/mcprest/errors_test.go` | Per-error-type mapping tests, `LogValue` shape tests, first-declared-rule-wins test |
| `adapters/mcprest/doc.go` | Package doc: the bridge, mapper-function rationale, FIXED-credential rationale, and the ctx/session recipe for dynamic per-caller credentials (using `server.ClientSessionFromContext`) |
| `examples/go-edge-models/main.go` (extend) | Wrap `docker/registry`'s `GetTagsRoute`/`GetManifestRoute` as MCP tools via `mcprest.ToolHandler`, PLUS at least one `MappedToolHandler` example showing a simplified/renamed LLM-facing tool shape, demonstrating `DefaultErrorPatterns()` against a simulated registry failure, PLUS one route bound via `ports.ToolPort.SetFunc` (in addition to the direct `mcpgo.ToolHandler` wiring) to demonstrate the "Usage with `ports.ToolPort`" composition |

## Out of scope (Phase 2+)

- Bulk-generating an MCP toolset from an entire `rest.Builder` — revisit
  only if a concrete multi-route use case appears; the one-at-a-time
  precedent (matching every other declarative construct in go-codex) is
  deliberately preserved for as long as possible.
- A generic, cross-adapter "per-session credential" mechanism — revisit
  only if a SECOND adapter (beyond MCP) develops a concrete, similar need;
  MCP's long-lived session model is the outlier today, not the norm.
- Auto-detecting a REST route's declared `rest.SecurityScheme` and
  resolving a matching `CredentialFunc` automatically (e.g. from an env var
  named after the scheme) — deferred; the caller supplies
  `nethttp.CallOptions.CredentialFunc` explicitly, same as any other
  `nethttp.Call`/`CallHandle` use.
- Automatic/reflection-based field mapping between ToolIn/Req or
  Resp/ToolOut — go-codex never does implicit struct mapping; mapper
  functions are always hand-written.

## Resolved design decisions

- **Package placement**: new standalone `adapters/mcprest` package, NOT
  inside `adapters/mcpgo` or `adapters/nethttp` — preserves the existing
  "no cross-adapter imports" convention for both.
- **Credential model**: FIXED `nethttp.CallOptions`/`CredentialFunc`
  configured once at construction — matches 100% of existing client-adapter
  binding precedent. Dynamic per-caller credentials remain achievable via
  the SAME ctx-introspection idiom `stats.ObserverFromContext` already
  establishes (documented as a recipe, not new API), using `mcp-go`'s own
  `server.ClientSessionFromContext(ctx).SessionID()`.
- **Scope granularity**: one `rest.RouteHandle` → one MCP tool's handler
  function, matching every other singular declarative construct in
  go-codex. No bulk-from-Builder generation.
- **Error translation**: `DefaultErrorPatterns()` opt-in helper, reusing
  the EXISTING `apimcp.ErrorPattern` mechanism unchanged — not a new
  error-response convention. Extended this round to also cover the two new
  mapper error types.
- **Mapper functions (NEW this round)**: `MappedToolHandler` is the general
  bridge (optional `toReq`/`fromResp`, both fallible); `ToolHandler` is the
  zero-boilerplate identity convenience built on top of it — mirrors
  `Call`/`CallHandle`'s existing convenience-vs-general relationship in
  `adapters/nethttp`. Mapper failures get their OWN structured error types
  (`ToolRequestMapError`/`ToolResponseMapError`), kept distinct from the
  underlying REST call's errors, which continue to forward unchanged.
- **Naming**: `adapters/mcprest` (short, mirrors `adapters/<transport>`
  naming, reads unambiguously as "MCP + REST").
- **Example**: extends `examples/go-edge-models` (the real driving use
  case: `docker/registry` exposed as MCP tools, including one mapped tool)
  rather than a new standalone example.
- **Ports composition**: `mcprest.ToolHandler`/`MappedToolHandler` already
  compose with `ports.ToolPort.SetFunc` today, with zero new plumbing
  (confirmed identical function shape to `mcpgo.HandlerFunc`) — moved out
  of "Out of scope," documented as a first-class usage pattern instead
  (see "Usage with `ports.ToolPort`" above).
