# Guide: AsyncAPI Spec

For the full API reference and all code examples, see the feature page.

**Feature:** [Event Channels — MQTT & AsyncAPI](../features/events.md) — AsyncAPI spec generation section

## Examples

- [examples/api-events](https://github.com/DaniDeer/go-codex/tree/main/examples/api-events) — event channel builder + `AsyncAPISpec()` output
- [examples/event-driven](https://github.com/DaniDeer/go-codex/tree/main/examples/event-driven) — full AsyncAPI 2.6 document via the low-level `DocumentBuilder`

---

## Combining pub/sub and request-reply in one AsyncAPI spec

By default, `api/events.Client` (PUB/SUB channels) and `api/reqreply.Server`
(request-reply channels) each produce their own `AsyncAPISpec()`. To publish a
**single combined AsyncAPI 3.0 document** covering both patterns, use
`AppendTo(*asyncapi.DocumentBuilder)` on each builder:

```go
import asyncapi "github.com/DaniDeer/go-codex/render/asyncapi/v3"

// 1. Create a shared underlying document builder.
doc := asyncapi.NewDocumentBuilder(asyncapi.Info{
    Title:   "Sensor Service API",
    Version: "1.0.0",
})
doc.AddServer("mqtt5", asyncapi.Server{
    URL:      "mqtts://broker.example.com:8883",
    Protocol: "mqtt5",
})

// 2. Register pub/sub channels and append them.
eventsClient := events.NewClient(events.WithInfo(events.Info{Title: "Sensor Service API", Version: "1.0.0"}))
sensorHandle, _ := sensorChannel.WithSubscribe(events.Subscribe{}).Handle(eventsB)
if err := eventsB.AppendTo(doc); err != nil {
    log.Fatal(err)
}

// 3. Register request-reply routes and append them.
reqreplyServer := reqreply.NewServer(reqreply.Info{Title: "Sensor Service API", Version: "1.0.0"})
computeHandle, _ := computeRoute.Register(reqreplyServer)
if err := reqreplyServer.AppendTo(doc); err != nil {
    log.Fatal(err)
}

// 4. Build once — one document covers pub/sub + request-reply.
spec, err := doc.Build()
if err != nil {
    log.Fatal(err)
}
yaml, _ := spec.MarshalYAML()
fmt.Println(string(yaml))
```

The combined YAML will contain both channel types:

```yaml
asyncapi: 3.0.0
info:
  title: Sensor Service API
  version: 1.0.0
channels:
  sensor/reading:            # ← pub/sub channel from events.Client
    address: sensor/reading
    ...
  computeAdd:                # ← request channel from reqreply.Server
    address: compute/add
    ...
  computeAddReply:           # ← auto-generated reply channel
    address: compute/add/reply
    ...
```

### Declaring dedicated req/reply error channels

For request-reply contracts, declare explicit error-path reply channels on the
route with `reqreply.ErrorPattern` — this is the codec-first, runtime-wired
declaration (recommended for new code) that drives BOTH the AsyncAPI spec
entry AND the actual `mqtt5`/`zeromq` `Serve` reply behavior in one
declaration:

```go
computeRoute := reqreply.NewRoute[ComputeReq, ComputeResp](
    "compute/add", computeReqCodec, computeRespCodec,
    reqreply.RouteMeta{OperationID: "computeAdd"},
    reqreply.ErrorPattern[domain.ConflictError, ErrorPayload](errorPayloadCodec,
        func(e domain.ConflictError) (ErrorPayload, error) {
            return ErrorPayload{Code: "conflict", Message: e.Error()}, nil
        },
    ).WithCode("conflict").WithDescription("Business conflict reply.").WithSchemaName("ConflictError"),
)
```

At runtime, `mqtt5.Serve`/`zeromq.Serve`/`zeromq.ServeRouter` consult
`handle.ErrorResponseFor(err)` on handler and encode failures — a matched
pattern sends the encoded typed payload instead of a plain-text error
string. Unmatched errors keep the existing plain-text fallback unchanged.

Generated AsyncAPI adds an additional NAMED MESSAGE (for example
`ErrorConflict`) to the route's SINGLE reply channel's `messages` map,
alongside the normal `Success` message — AsyncAPI 3.0's native
channel-level multi-message mechanism, the direct analogue of OpenAPI's
per-status `responses` object. Earlier versions of go-codex generated a
SEPARATE reply-error channel/operation per declared pattern (e.g.
`computeAddReplyErrorConflict` at `compute/add/reply/error/conflict`) —
this was migrated to the single-channel, multi-message shape (see
`docs/design/d-0005-error-handling.md`'s Topic 3): existing
`ErrorPattern`/`ErrorReplyMeta` declarations need NO changes, only the
RENDERED spec's shape changed. `ErrorReplyMeta.OperationID`/
`ChannelAddress` are now ignored (there is no longer a separate
channel/operation for them to override).

`reqreply.ErrorReplyMeta` remains available unchanged for spec-only
declarations that document an error reply produced by some other mechanism
(no runtime dispatch — pure documentation/contract metadata, same role as
`RouteMeta`):

```go
computeRoute := reqreply.NewRoute[ComputeReq, ComputeResp](
    "compute/add", computeReqCodec, computeRespCodec,
    reqreply.RouteMeta{OperationID: "computeAdd"},
    reqreply.ErrorReplyMeta{
        Code:        "conflict",
        Description: "Business conflict reply.",
        Schema:      codex.String().Schema,
        SchemaName:  "ConflictError",
    },
)
```

### Client-side decode — `RouteHandle.DecodeErrorFor` (mqtt5 + zeromq)

`reqreply.ErrorPattern` round-trips all the way to the CLIENT, mirroring
REST's `nethttp.CallWithHandle`/`ErrorPatternResponse` workflow — one
shared `Route` declaration, zero client-side boilerplate:

```go
_, err := client.Call(ctx, computeRoute, ComputeReq{X: 1, Y: 2})
if err != nil {
    var epr mqtt5.ErrorPatternResponse // or zeromq.ErrorPatternResponse
    if errors.As(err, &epr) {
        conflict := epr.Value.(domain.ConflictError) // decoded automatically
        // ... handle the typed conflict ...
    }
}
```

**Convenient matching** — the SAME 3 alternatives REST's client offers
are also available for reqreply, collapsing the `errors.As` +
type-assertion dance above into a single conditional (Topic 6 of
`docs/design/d-0005-error-handling.md`). All 3 live in
`api/reqreply` (transport-independent — they work identically whether
`err` came from `mqtt5.Call`/`AttachClient` or `zeromq.Call`/
`AttachClient`, since both adapters' `ErrorPatternResponse` types
implement the same core `reqreply.ErrorPatternValuer` interface):

```go
// reqreply.ErrorPatternAs[B] — generic one-liner.
if conflict, ok := reqreply.ErrorPatternAs[domain.ConflictError](err); ok { /* ... */ }

// reqreply.ErrorPatternOpt.Match — the SAME value declares (server) AND matches (client).
var conflictPattern = reqreply.ErrorPattern[domain.ConflictError, domain.ConflictError](conflictCodec).WithCode("conflict")
if conflict, ok := conflictPattern.Match(err); ok { /* ... */ }

// reqreply.HandleErrorPattern/Case — closest parity to a switch expression.
handled := reqreply.HandleErrorPattern(err,
    reqreply.Case(func(e domain.ConflictError) { /* ... */ }),
)
```

All 3 return `false`/`ok=false` when `err` carries no matched
`ErrorPatternResponse` at all, or when the payload's concrete type
doesn't match. As with REST, **a matched `ErrorPatternResponse` is a
business decision the server made deliberately — never safe to blindly
retry**, unlike a transport-level `mqtt5.CallError{Kind: KindTimeout}`/
`zeromq.CallError`, which usually IS safe to retry.

Unlike REST (which has a free HTTP status discriminator), reqreply has no
status code on the wire — so each `ErrorPattern`'s `Code` (the SAME value
used to derive the reply-error channel's operation ID, either explicit via
`.WithCode(...)` or the sanitized-type-name default) is ALSO transmitted:
mqtt5 as a dedicated MQTT5 User Property, zeromq as an extra frame — only
on the matched-pattern path; the plain-text fallback is completely
unchanged. `RouteHandle.DecodeErrorFor(code, body)` is the client-side
lookup accessor `Call` uses (mirrors `ErrorResponseFor`, but matches by
`Code` instead of `errors.As`, since the client has no Go error value —
only the wire-transmitted code string).

**Unlike REST's status code, give each `ErrorPattern` a UNIQUE `Code`**
— `Register` rejects two patterns sharing one `Code` with
`reqreply.DuplicateErrorPatternCodeError` (a hard error, not a documented
caveat like REST's same-status precedence — reqreply is a newer
mechanism with no existing behavior to preserve, so this ambiguity is
prevented at declaration time instead of left for the client to
discover).

Wrapped inside `mqtt5.CallError`/`zeromq.CallError` (not returned bare,
unlike REST) — preserves `errors.As` ergonomics via `Unwrap()`, staying
consistent with each package's own established error-wrapping convention.
`adapters/mqtt` (v3) has no reqreply support at all, so this applies to
`adapters/mqtt5` and `adapters/zeromq` only (both REQ/REP and
ROUTER/DEALER for zeromq).

### What `AppendTo` does and does NOT copy

| Copied by `AppendTo` | Not copied (caller owns) |
|---|---|
| All registered channels | Servers |
| Reply channels (request-reply pattern) | Schemas registered via `AddSchema` |
| | Security schemes |

Servers, schemas, and security schemes must be added directly to the shared
`*asyncapi.DocumentBuilder` before calling `Build()`. This gives you full
control over the combined document without any hidden merging surprises.
