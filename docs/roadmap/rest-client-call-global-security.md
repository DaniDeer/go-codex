# REST `Client.Call` — dual-mode dispatch for `GlobalSecurity` visibility

> **Status:** Design draft — spun out of the [D-0004 — ReqReply Workflow
> Simplification](../design/d-0004-reqreply-workflow-simplification.md)
> ergonomics-parity review; no code written, no spike run yet (the gap this
> doc describes is already fully confirmed via direct code citations, not
> a hypothesis).
> [← Back to Roadmap](index.md)

## Motivation

Confirmed while reviewing REST's real `Client.Call` code as the reference
model for [D-0004 — ReqReply Workflow Simplification](../design/d-0004-reqreply-workflow-simplification.md):
REST's own `Client.Call` has a real, load-bearing limitation that reqreply's
newly-designed `Client.Call` no longer has — REST's client can **never**
see `GlobalSecurity`, only per-route `Security`.

- `rest.Client.Call` (`adapters/nethttp/clienttransport.go`'s `Call`)
  requires `routeAny` to be a raw `Route[Req,Resp]` value — confirmed via
  its own type check (`rv.Type().Name()` must have prefix `"Route["`,
  otherwise it returns `TransportTypeMismatchError`). It then calls
  `.ClientHandle()` on that raw route internally, FRESH, on every call.
- `Route.ClientHandle()` (`api/rest/builder.go:3167`) explicitly "sources
  no Builder" (its own doc comment) — the handle it returns always has
  `GlobalSecurity: nil`, regardless of whether the same route was also
  registered against a `*Server` with `AddGlobalSecurity(...)` declared.
- `resolveClientSecurity` (`adapters/nethttp/clienttransport.go:108`)
  already contains the CORRECT fallback —
  `if secReqs == nil { secReqs, _ = elem.FieldByName("GlobalSecurity")... }`
  — but this fallback is dead code in practice for `Client.Call`, because
  the handle it inspects is always a fresh `ClientHandle()` result whose
  `GlobalSecurity` field is always nil. The fallback only ever fires today
  via the manual escape hatch (constructing a `RouteHandle` directly with
  `GlobalSecurity` set by hand), not via the declared,
  `Server.AddGlobalSecurity`-driven mechanism most callers actually use.

This is not a hypothetical concern — `Server.AddGlobalSecurity` exists
precisely so route authors don't have to repeat the same `Security`
requirement on every route. Today, that convenience is honored
SERVER-side (`Route.Register(server)` reads it, `RouteHandle.GlobalSecurity`
is populated, `nethttp`'s server-side dispatch enforces it) but silently
NOT honored CLIENT-side — a caller using `Client.Call` against a route
protected only by `GlobalSecurity` gets no `CredentialFunc`
invocation/credential-header injection at all, with no error or warning.

reqreply's own `Client.Call` (this session's confirmed design, see
[D-0004 — ReqReply Workflow Simplification](../design/d-0004-reqreply-workflow-simplification.md)'s
Decision 1) already resolves the IDENTICAL problem via a dual-mode
type-switch: accept EITHER a raw, unregistered `Route` (today's REST-style
behavior, `GlobalSecurity` invisible — an accepted, documented limitation)
OR an already-registered `*RouteHandle` (obtained via `route.Register
(server)`, `GlobalSecurity` visible and enforced). This draft proposes
backporting that exact mechanism to REST's own `Client.Call`, closing the
gap reqreply's design surfaced by comparison — REST would then offer its
own client callers the same choice reqreply now does, rather than being
the one boundary still missing it.

## Scope decisions

| In scope | Out of scope |
|---|---|
| `rest.Client.Call`/`nethttp.Call` accepting EITHER a raw `Route[Req,Resp]` (unchanged, current behavior) OR an already-registered `*RouteHandle[Req,Resp]` (new, `GlobalSecurity`-visible) | Changing `Route.ClientHandle()`'s own signature (e.g. accepting a `*Server` parameter to source `GlobalSecurity` without a dual-mode `Call`) — considered and explicitly deferred; the dual-mode `Call` mirrors reqreply's confirmed mechanism directly, no need to invent a second approach |
| The `nethttp` client transport specifically (REST's only client transport today) | Any other REST client transport, since none currently exists — the pattern should be documented here for whoever adds the next one, not implemented speculatively |
| `rest.Client.Call`'s dispatch only | `rest.Client.Consume` (SSE) — not reviewed in this draft; may have the same gap, flagged as an open question below, not assumed |

## API surface

```go
// api/rest — Client.Call's signature is unchanged; only what routeAny
// may legally BE changes (documented, not a breaking change — a raw
// Route already works today and keeps working identically).
func (c *Client) Call(ctx context.Context, route any, req any, opts ...ClientCallOptions) (any, error)
```

```go
// adapters/nethttp — proposed dual-mode dispatch inside clientTransport.Call,
// mirroring reqreply's confirmed ClientTransport.Call type-switch exactly.
func (t *clientTransport) Call(ctx context.Context, routeAny, reqAny any, opts ...rest.ClientCallOptions) (any, error) {
	var handleVal reflect.Value
	switch {
	case isRouteType(routeAny):
		// RAW, unregistered Route — TODAY'S sole behavior, unchanged.
		// Derives ClientHandle() fresh; GlobalSecurity stays invisible,
		// same accepted limitation as today (zero Server needed).
		handleVal = reflect.ValueOf(routeAny).MethodByName("ClientHandle").Call(nil)[0]
	case isRouteHandleType(routeAny):
		// Already-registered *RouteHandle (via route.Register(server)) —
		// GlobalSecurity IS populated and visible to resolveClientSecurity's
		// existing (currently dead-in-practice) fallback.
		handleVal = reflect.ValueOf(routeAny)
	default:
		return nil, rest.TransportTypeMismatchError{
			Want: "rest.Route[Req, Resp] or *rest.RouteHandle[Req, Resp]",
			Got:  fmt.Sprintf("%T", routeAny),
		}
	}
	// ... rest of Call proceeds identically against handleVal, using the
	// SAME resolveClientSecurity/mergeCredentialHeaders path that already
	// exists today — no change needed there, since its GlobalSecurity
	// fallback is already correct, just previously unreachable.
}
```

The type-switch's two helper predicates (`isRouteType`/`isRouteHandleType`)
mirror the existing single check (`rv.Type().PkgPath() == restPkgPath &&
strings.HasPrefix(rv.Type().Name(), "Route[")`), adding a second branch
for `strings.HasPrefix(rv.Type().Name(), "*RouteHandle[")` (or the
equivalent reflect-based pointer/struct-name check `nethttp` already uses
elsewhere for `*RouteHandle`-shaped values, e.g. in `callWithVars`).

**No change needed to `resolveClientSecurity`, `mergeCredentialHeaders`,
or any subsequent step of `Call`** — every step downstream of obtaining
`handleVal` already operates generically against `*RouteHandle`'s fields
via reflection, regardless of which branch produced `handleVal`. This is
the same "already correct, previously unreachable" shape reqreply's own
review found in `resolveClientSecurity`'s fallback line, confirming this
is a narrow, surgical fix — not a rearchitecture of `Call`.

## Open design decisions

- **Exact type-check mechanism for the `*RouteHandle` branch.** `Call`
  today identifies a raw `Route` via `rv.Type().PkgPath()` +
  `strings.HasPrefix(...,"Route[")`. The equivalent `*RouteHandle` check
  needs the same care (confirm it can't accidentally match unrelated
  types, confirm it works for both `RouteHandle` and any future
  REST-adjacent handle type like `SSERouteHandle`) — not yet spiked.
- **Should `Client.Consume` (SSE) get the identical treatment?** Not
  reviewed in this draft. `SSERouteHandle` has its own `GlobalSecurity`
  field (confirmed `api/rest/builder.go:3332`) and its own
  `ClientHandle()`-equivalent (`SSERoute.ClientHandle()`,
  `api/rest/builder.go:3920`) — the same gap likely exists there too, but
  needs its own confirmation pass before folding into this draft's scope.
- **Backward compatibility / migration.** Since a raw `Route` keeps
  working identically (this is purely additive — a new accepted input
  shape, not a removed one), this should require zero migration for
  existing callers. Worth a throwaway spike to confirm no existing test
  or example accidentally relies on `Call` REJECTING a `*RouteHandle`
  (unlikely, but unverified).
- **Whether this should be spiked before implementation**, per this
  skill's usual discipline (throwaway Go prototype, `/tmp/gospike`,
  confirm-then-delete) — recommended before writing real code, even
  though the gap itself needs no further confirmation; the FIX's own
  mechanics (the two type-check predicates, confirming
  `resolveClientSecurity`'s fallback actually fires end-to-end against a
  `*RouteHandle` obtained via `Route.Register(server)`) still deserve a
  quick compiling proof, not just a doc-level description.
