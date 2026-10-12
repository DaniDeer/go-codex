# Typed HTTP Redirects

> See also: [`api/rest` on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/api/rest) · [`adapters/nethttp` on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/adapters/nethttp)
>
> Runnable demos: [`examples/rest-redirect`](https://github.com/DaniDeer/go-codex/tree/main/examples/rest-redirect) (standalone, minimal — auto-populate-via-`Call`) · [`examples/rest-api`'s `demo_redirect.go`](https://github.com/DaniDeer/go-codex/blob/main/examples/rest-api/demo_redirect.go) (richer, dual-adapter, Router-grouped target — explicit construction-time `RegisterRoute`)
>
> Guide: [REST redirects](../guides/rest-redirects.md)

A handler declares "redirect to THIS route" by calling `rest.Redirect`/`rest.RedirectToSSE` with the **target route value**, never a raw URL string — the path is built from the target's own declared template + vars, the same way `BuildPath` always works. `rest.Client.Call`/`rest.Client.Consume` transparently follow the redirect and decode the *target* route's own response type, as long as the target has been seen by that `*rest.Client`.

```go
import "github.com/DaniDeer/go-codex/api/rest"

var getOrderRoute = rest.NewRoute[GetOrderReq, Order]("GET", "/orders/{id}",
    getOrderReqCodec, orderCodec,
    rest.NewPathParam("id", codex.String(),
        func(r GetOrderReq) string { return r.ID },
        func(r *GetOrderReq, v string) { r.ID = v }))

var createOrderRoute rest.Route[CreateOrderReq, Order]
createOrderRoute = rest.NewRoute[CreateOrderReq, Order]("POST", "/orders",
    createOrderReqCodec, orderCodec).WithHandler(
    func(ctx context.Context, req CreateOrderReq) (Order, error) {
        id := createOrder(req)
        var zero Order
        return zero, rest.Redirect(http.StatusSeeOther, createOrderRoute, getOrderRoute,
            map[string]string{"id": id})
    })
```

## Router-grouped targets

`rest.Redirect`/`rest.RedirectToSSE` accept trailing variadic `opts ...rest.ClientHandleOpt`, passed straight through to the target's own `ClientHandle(opts...)`. **Pass `rest.WithRouter(rt)` whenever the target route is (or will be) grouped under a [`Router`](router-groups.md)** — a Router-grouped route declares a RELATIVE path template (e.g. `"/{id}"`, composed to `"/users/{id}"` only via its Router's prefix); omitting `WithRouter` resolves the Location against the bare, un-prefixed template instead — the WRONG target.

```go
var createOrderRoute rest.Route[CreateOrderReq, Order]
createOrderRoute = rest.NewRoute[CreateOrderReq, Order]("POST", "", createOrderReqCodec, orderCodec).
    WithHandler(func(ctx context.Context, req CreateOrderReq) (Order, error) {
        id := createOrder(req)
        var zero Order
        return zero, rest.Redirect(http.StatusSeeOther, createOrderRoute, getOrderRoute,
            map[string]string{"id": id}, rest.WithRouter(ordersRouter)) // getOrderRoute is Mounted under ordersRouter
    })
```

`WithRouter` composes BOTH the Router's prefix AND any reusable middleware attached via `Router.Use` — the SAME composition `Router.Route(target)` + `Router.Register(b)` would have produced for the target's server side, so the redirect's computed Location always matches what the server actually serves.

**This composition must independently match on the client side too.** The server computes the Location via `Redirect(..., target, vars, rest.WithRouter(serverSideRouter))`; the client separately resolves the auto-follow target by consulting its own registry, populated via `Client.RegisterRoute(target.ClientHandle(rest.WithRouter(clientSideRouter)))` (or an equivalent already-composed `*RouteHandle`/credential-bound variant — never the bare, un-composed route value). Both Router values must compose to the IDENTICAL absolute path, or the client fails with the already-typed `rest.UnrecognizedRedirectError` rather than a silent wrong-route match. See [`examples/rest-api`](https://github.com/DaniDeer/go-codex/tree/main/examples/rest-api)'s `demo_redirect.go` for a worked, Router-grouped example.

**Known limitation**: `WithRouter(rt)` applies only `rt`'s OWN prefix/middleware — never an ancestor `Router.Mount`'s contribution (a pre-existing limitation of `WithRouter` itself, not specific to redirects — see `Route.ClientHandle`'s own doc comment). For a multi-level nested `Router.Mount` hierarchy, keep the Router FLAT (one `NewRouter("prefix")` call) when a redirect target under it is also needed, rather than nesting via `Mount`.

**Auto-follow never dispatches the target's own `ClientMW`.** Decoding an auto-followed response uses only the target handle's `DecodeMergedResponse` (codec-declared response header/cookie merge fields) — it does NOT invoke the target's own attached general-purpose `ClientMW`/`ClientBoundMW` (e.g. a timing observer, or any Out-side side effect). This is intentional and mirrors the credential-reuse rule above: auto-follow consults the registered target ONLY for its decode logic, never for behavior the ORIGINATING call didn't itself opt into.

**Credential reuse covers BOTH credential delivery mechanisms.** The originating call's fully-merged header vars are replayed on the follow-up request — this includes a credential delivered via the LEGACY credential-header mechanism AND one delivered via a codec-backed `BoundClientMiddleware`/`ClientMW`'s own `EncodeIn` (e.g. `ClientBoundMW`-attached security middleware declaring a header merge field). A security-scheme MISMATCH across the hop (the target requires a DIFFERENT or ADDITIONAL scheme than the originating route) is never silently bypassed or fabricated — it surfaces as the target's own ordinary rejection (401/400, whichever its own declared requirement produces), exactly as if that request had been made directly.

## Server side

A handler returns the `error` `rest.Redirect`/`rest.RedirectToSSE` produces (a `rest.RedirectError`) instead of its declared response. `adapters/nethttp` and `adapters/chi` both recognize this error *before* `ErrorPattern`/`ErrorStatus` matching and render a bare status + `Location` header, with no body — for BOTH plain REST routes and SSE routes (gated on whether the SSE stream has already committed to `200 text/event-stream`; a redirect returned after the first `send` is treated as an ordinary late error, not honored).

`rest.Redirect`/`rest.RedirectToSSE` validate at construction time:
- **307/308** preserve the originating method and body — the target route's declared method must match the originating route's, or `rest.RedirectMethodMismatchError` is returned instead.
- **301/302/303** always redirect to GET (RFC 9110) — no method constraint.
- Missing/invalid path vars for the target's template produce `rest.RedirectTargetVarError`.

## Client side (`adapters/nethttp`)

`rest.Client` keeps two parallel **redirect-target registries**: one for `Call`-decodable `Route`s, one for `Consume`-streamable `SSERoute`s. A route is indexed automatically the first time it's seen via `Client.Call`/`Client.Consume` — a route that's *only ever reached as a redirect target* needs explicit warming via `Client.RegisterRoute`:

```go
client := rest.NewClient()
client.Attach(nethttp.NewClientTransport(nethttp.ClientTransportOptions{HTTPClient: hc, BaseURL: baseURL}))
client.RegisterRoute(getOrderRoute) // warm the registry — getOrderRoute is never called directly

respAny, err := client.Call(ctx, createOrderRoute, CreateOrderReq{Item: "Widget"})
order := respAny.(Order) // the TARGET route's response type, decoded transparently
```

On a 3xx:
- `Client.Call` resolves against the `routes` registry. A match in `sseRoutes` only returns `rest.RedirectToStreamUnsupportedError` (a stream target is fundamentally incompatible with `Call`'s single-decode contract). No match: `rest.UnrecognizedRedirectError`.
- `Client.Consume` resolves against `sseRoutes` first (and re-checks on **every reconnect attempt**, not just the first connection). A match in `routes` only returns `rest.RedirectTargetNotStreamableError`. The target SSE route's event type must be identical to the originating route's event type (the caller's `fn` is fixed to it) — a mismatch is treated as `rest.UnrecognizedRedirectError`.
- Both cap the chain depth (`rest.RedirectChainTooDeepError`) at `ClientCallOptions.MaxRedirects`/`ClientConsumeOptions.MaxRedirects` (0 = default 10, mirroring `net/http`'s own precedent) — covers both long chains and cycles.
- Auto-follow reuses the **originating call's own credentials/headers/cookies** — never re-derived from the registered target's own `ClientMW`. The target is consulted only for its decode logic/event type.

**`rest.CallWithTransport`/`nethttp.NewClientTransport` used directly (no `*rest.Client`) stay registry-free** — they return the typed `rest.RedirectError` directly for the caller to inspect or resolve manually, matching the escape-hatch contract the rest of `api/rest` already follows. This includes `adapters/nethttp`'s `ports.Pattern` bindings that take a raw `*http.Client` directly rather than a `*rest.Client` — `CallAdapter`/`DrainCallAdapter`/`PollAdapter` (when `Vars` is set, or always for `PollAdapter`) and `CallSSEAdapter`: all of these are ALSO registry-free (no `*rest.Client` to auto-follow against), and overriding `CheckRedirect` on their own caller-supplied client the same way `NewClientTransport` does is what makes a 3xx surface as `rest.RedirectError` there too, instead of `net/http`'s own silent default auto-follow attempting to decode the WRONG (redirected-to) body against the ORIGINATING route's codec.

`NewClientTransport` overrides `CheckRedirect` on a shallow copy of the caller's own `*http.Client` (so a caller's own client is never mutated) to stop `net/http`'s silent built-in auto-follow — `rest.Client`'s own registry-aware auto-follow replaces it.

**Every hop is observed.** Each follow-up hop (`Call`'s `followRedirectHop`/`doFollowUpRequest` chain and `Consume`'s `followSSERedirect`/`doFollowSSERequest` chain) calls `obs.RecordRequest` exactly as if it were its own `Call`/connection attempt — using the SAME `Observer` the originating call resolved (`ClientCallOptions.Observer` override, or `stats.ObserverFromContext(ctx)`), status 0 before a response is ever received (pre-flight/build/network failures, or a terminal error like `RedirectChainTooDeepError`), the real status code once received. This was a confirmed, found-and-fixed gap (`history.md`'s Round 202) — earlier redirect rounds verified the dispatch/decode/error-taxonomy correctness of auto-follow but missed that every follow-up hop was entirely invisible to observability until that round.

## `ResponseMeta.Headers`

A redirect's `Location` response header (or any other declared response header) can be documented in the OpenAPI spec via `rest.ResponseMeta.Headers`:

```go
rest.ResponseMeta{
    Status: "303",
    Headers: map[string]rest.ResponseMetaHeader{
        "Location": {Description: "The created order's URL.", Required: true},
    },
}
```
