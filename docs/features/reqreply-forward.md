# Request/Reply Forward/Bounce

> See also: [`api/reqreply` on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/api/reqreply) · [`adapters/mqtt5` on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/adapters/mqtt5) · [`adapters/zeromq` on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/adapters/zeromq)
>
> Runnable demo: [`examples/reqreply-api`'s `demo_forward.go`](https://github.com/DaniDeer/go-codex/blob/main/examples/reqreply-api/demo_forward.go) (`demoForwardWithProperties` demonstrates wire-level properties)
>
> Guide: [reqreply Forward/Bounce](../guides/reqreply-forward.md)

Pub/sub request/reply has no broker-level "redirect" the way HTTP does — a `reqreply` handler declares "actually, route X should handle this" by calling `reqreply.Forward` with the **target route value**, never a raw topic string. The SERVER signals "ask topic X instead" within the SAME correlation-ID round trip; `reqreply.Client.Call`/`Client.CallAsync` transparently republish and decode the *target* route's own response, as long as the target has been seen by that `*reqreply.Client`.

```go
import "github.com/DaniDeer/go-codex/api/reqreply"

var sumRoute = reqreply.NewRoute[SumReq, SumResp]("compute/sum", sumReqCodec, sumRespCodec)

var legacyAddRoute = reqreply.NewRoute[LegacyAddReq, LegacyAddResp]("compute/legacy-add",
    legacyAddReqCodec, legacyAddRespCodec).WithHandler(
    func(ctx context.Context, req LegacyAddReq) (LegacyAddResp, error) {
        var zero LegacyAddResp
        return zero, reqreply.Forward(sumRoute, SumReq{A: req.X, B: req.Y}, nil, nil)
    })
```

**`Forward`'s target `Req`/`Resp` are both FREE to differ from the originating route's** — unlike HTTP 307/308, reqreply's wire protocol has no method/body-preservation constraint to honor, so there is no method-mismatch check and no "from" parameter: the handler constructs and supplies a brand new request value explicitly. This is the single biggest divergence from `rest.Redirect`'s own shape.

`Forward`'s last 2 non-variadic parameters are both plain, nil-able `map[string]string` values: `vars` resolves the target's `{var}` topic template (above); `properties` carries wire-level properties/headers — see "Wire-level properties" below.

## Wire-level properties

A target route may require a property/header-delivered credential (e.g. an MQTT5 User Property) that has nothing to do with the request body. `Forward`'s `properties` parameter carries exactly that:

```go
func(ctx context.Context, req LegacyAddReq) (LegacyAddResp, error) {
    var zero LegacyAddResp
    return zero, reqreply.Forward(sumRoute, SumReq{A: req.X, B: req.Y}, nil,
        map[string]string{"X-Replay-Reason": "legacy-route-retired"})
}
```

`ForwardError.Properties` carries this map; `Client.followForward` threads it into every hop's `ClientTransport.CallRaw` call, and each adapter writes it onto the real wire reply too (`adapters/mqtt5`: a reserved, prefixed User Property per entry, `"x-forward-prop:" + key`, kept distinct from the forward-topic marker and any Case 3 middleware-declared response properties on the SAME reply; `adapters/zeromq`: a confirmed, tested no-op — zeromq's reqreply transport has no property/header wire mechanism of its own).

## Server side

A handler returns the `error` `reqreply.Forward` produces (a `reqreply.ForwardError`) instead of its declared response. `adapters/mqtt5` and `adapters/zeromq` both recognize this error *before* `ErrorPattern`/`DeadLetter` are ever consulted — a Forward is a deliberate instruction, not a failure, so neither mechanism fires for it. The reserved wire signal mirrors each adapter's own existing `ErrorPattern` discriminator exactly:

- `adapters/mqtt5` sets a reserved `ContentType` (`"application/mqtt5-forward"`) + a reserved User Property (`"x-forward-topic"`) carrying the target's already-resolved concrete topic.
- `adapters/zeromq` sends a 3rd status-frame value (`"forward"`, alongside the existing `"ok"`/`"error"`) + the target topic as the 2nd frame — the SAME `[status, code/topic, body]` 3-frame shape (4-frame for ROUTER/DEALER, carrying the leading delimiter) the matched-`ErrorPattern` reply already uses.

`reqreply.Forward` validates at construction time: missing/invalid topic vars for the target's template, or a request-encode failure, produce `reqreply.ForwardTargetVarError`.

## Client side

`reqreply.Client` keeps ONE topic-keyed registry (shared with `RegisterRoute`/auto-populate — see `docs/design/d-0004-reqreply-workflow-simplification.md`'s own "Addendum: Client Registry, Forward/Bounce, and Dead-Letter Replay" for the full registry design). A route is indexed automatically the first time it's seen via `Client.Call`/`Client.CallAsync` — a route that's *only ever reached as a Forward target* needs explicit warming via `Client.RegisterRoute`:

```go
client := reqreply.NewClient()
client.Attach(mqtt5.NewClientTransport(mqtt5.ClientTransportOptions{Client: mqttClient, Router: router}))

// sumRoute is never called directly in this app — warm the registry
// explicitly so Call(legacyAddRoute, ...) can resolve it.
client.RegisterRoute(sumRoute)

sum, err := client.Call(ctx, legacyAddRoute, LegacyAddReq{X: 3, Y: 4})
// sum is already sumRoute's own response type (SumResp), decoded
// transparently — Client.Call is a Go 1.27 generic method, no manual
// assertion needed.
```

On a recognized Forward reply, `Client.Call`'s CENTRALIZED chain-following loop (not the adapter):

1. Resolves the target topic against the registry — `reqreply.UnrecognizedForwardError` if no route was ever registered for it, `reqreply.DuplicateTopicTypeMismatchError` if the registry entry is poisoned (two genuinely different route types registered against the same topic).
2. Compares the originating and target routes' declared security SCHEME NAME(S) — a mismatch fails FAST, client-side, BEFORE the network round-trip, with `reqreply.ForwardSecuritySchemeMismatchError`. This is a GENUINE DIVERGENCE from REST's own more permissive behavior (REST discovers a mismatch only after sending, over the wire) — since a Forward's target can be an entirely different route in an entirely different trust domain, a cross-domain scheme mismatch is more likely here than for REST's own redirect targets.
3. Caps the chain depth at `ClientCallOptions.MaxForwards` (<=0 = default 10, mirroring `net/http`'s own precedent via `rest.ClientCallOptions.MaxRedirects`) — `reqreply.ForwardChainTooDeepError` once exhausted, covering both unreasonably long chains and cycles with one mechanism.
4. Republishes via the single-hop `ClientTransport.CallRaw` primitive and classifies the reply: an ordinary successful reply decodes immediately via the resolved target route's own response codec; a reply itself tagged as a Forward loops back to step 1; a reply matching a declared `ErrorPattern` is decoded via the resolved target route's `DecodeErrorFor`, producing the SAME typed `ErrorPatternResponse` an ordinary (non-forwarded) `Call` would.

Both `Client.Call` and `Client.CallAsync` exercise this IDENTICAL loop — `CallAsync` is a thin goroutine wrapper reusing `Call` directly, not a second, adapter-level copy of the chain-following mechanism.

**`reqreply.CallWithTransport` used directly (no `*reqreply.Client`) stays registry-free** — it returns the typed `reqreply.ForwardError` directly for the caller to inspect or resolve manually, matching the escape-hatch contract the rest of `api/reqreply` already follows. This includes `adapters/mqtt5`/`adapters/zeromq`'s `ports.Pattern` bindings (`CallAdapter`) that take a raw transport directly rather than a `*reqreply.Client` — these are ALSO registry-free (no `*reqreply.Client` to auto-follow against).

**Every hop is observed, for free.** `ClientCallOptions` has no `Observer` override field (unlike REST's own) — `obs` is always resolved via the attached `ClientTransport`'s own construction-time `Observer`, falling back to `ctx`. Since the chain-following loop calls `CallRaw` on the EXACT SAME attached `ClientTransport` instance the originating `Call` used, every hop automatically resolves the identical `Observer` — and `CallRaw` already calls `obs.RecordRequest` on its own. No additional wiring needed.
