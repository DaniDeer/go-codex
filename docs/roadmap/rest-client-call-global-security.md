# REST `Client.Consume` (SSE) — dual-mode dispatch for `GlobalSecurity` visibility

> **Status:** Partially resolved. `Client.Call`'s half of this gap was
> **already fixed** as an unplanned side effect of Phase 5a (confirmed via
> direct code trace — `clientTransport.Call` already dispatches through
> `recoverClientRouteHandleValue`, which accepts either a raw `Route` or an
> already-registered `*RouteHandle`) — but this fix has **zero test
> coverage** of the GlobalSecurity-only scenario today. `Client.Consume`
> (SSE) still has the **identical, unfixed** gap this doc originally
> described for `Call` — confirmed via code, `clientTransport.Consume` has
> no dual-mode branch at all. Scope is now REST-only; `api/events` and
> `api/reqreply` were checked and confirmed to have no analogous gap (see
> below).
> [← Back to Roadmap](index.md)

## Motivation

Confirmed while reviewing REST's real `Client.Call` code as the reference
model for [D-0004 — ReqReply Workflow Simplification](../design/d-0004-reqreply-workflow-simplification.md):
REST's client-side dispatch has a real, load-bearing limitation that
reqreply's `Client.Call` does not — a client can **never** see
`GlobalSecurity`, only per-route `Security`, unless it's handed an
already-registered handle.

- `Route.ClientHandle()` (`api/rest/builder.go`) explicitly "sources no
  Builder" (its own doc comment) — the handle it returns always has
  `GlobalSecurity: nil`, regardless of whether the same route was also
  registered against a `*Server` with `AddGlobalSecurity(...)` declared.
- `resolveClientSecurity` (`adapters/nethttp/clienttransport.go`) already
  contains the correct fallback —
  `if secReqs == nil { secReqs, _ = elem.FieldByName("GlobalSecurity")... }`
  — but this fallback only fires when the handle it inspects genuinely
  carries a populated `GlobalSecurity` field, i.e. when the dispatcher
  was handed an already-registered `*RouteHandle` rather than a fresh
  `ClientHandle()` built from a raw `Route`.

## `Client.Call` — already fixed (confirmed via code trace)

Traced the real call chain in `adapters/nethttp`: `clientTransport.Call`
dispatches through `recoverClientRouteHandleValue`, which already
implements the exact dual-mode type-switch this doc originally proposed:

- **Raw, unregistered `Route[Req,Resp]`** — derives `.ClientHandle()`
  fresh; `GlobalSecurity` stays invisible, the same accepted limitation
  as always (zero `Server` needed, lowest-ceremony path).
- **Already-registered `*RouteHandle[Req,Resp]`** (obtained via
  `Route.RegisterHandle(server)` — **not** `Route.Register(server)`,
  which returns only an `error` and discards the handle) — used as-is;
  `GlobalSecurity` is populated (`slices.Clone(b.globalSecurity)` at
  registration time) and fully visible to `resolveClientSecurity`'s
  existing fallback.

This helper was built during the earlier, unrelated Phase 5a work (to
support bare-handle callers like `adapters/mcprest`) and `clientTransport
.Call` was apparently unified onto it without anyone connecting the fix
back to this roadmap doc. **The mechanism works today** — but:

1. **Zero test coverage exists for this exact scenario.** Every existing
   `AddGlobalSecurity`-involving test in `adapters/nethttp/{adapter,
   client,clienttransport}_test.go` also declares per-route `Security` on
   the same route, so the `GlobalSecurity` fallback path has never
   actually been exercised by any test, despite apparently working.
2. **The capability is undocumented.** Neither `Client.Call`'s own godoc
   example nor the `ClientTransport.Call` interface doc comment mention
   that passing a `*RouteHandle` (vs. a raw `Route`) makes
   `GlobalSecurity` visible — a user reading either comment today has no
   way to discover this.

## `Client.Consume` (SSE) — still broken, confirmed via code

`clientTransport.Consume` has **no dual-mode branch at all** — it
type-checks only for a raw `SSERoute[...]` (via
`strings.HasPrefix(rv.Type().Name(), "SSERoute[")`) and unconditionally
calls `.ClientHandle()` fresh, every time. `SSERouteHandle.GlobalSecurity`
exists and is populated at registration time exactly like `RouteHandle`'s
— but it is never visible via `Consume` today. This is the one piece of
real, scoped implementation work this doc still needs: apply the
identical dual-mode resolution `Call` already has, to `Consume`.

## Confirmed N/A for `api/events` and `api/reqreply`

Before finalizing this doc's scope, `api/events` and `api/reqreply` were
checked for the same bug class. Neither has it:

- **`api/events` is architecturally immune by design.**
  `events.Client.AddGlobalSecurity` lives on `Client` itself (events
  unifies builder+dispatcher into one type, unlike REST's Server/Client
  split). `Client.Subscribe`/`Publish` resolve handles via a shared
  `recoverHandle(kind, anyAny, client *events.Client)` helper
  (mirrored across `adapters/mqtt5`/`zeromq`/`mqtt`), which calls
  `sub.Handle(client)` — passing the **live** `*events.Client` reference
  on every single dispatch, never a cached/disconnected handle. There is
  no "stale handle built without a client reference" failure mode here at
  all; `GlobalSecurity` is always current. No fix is possible or needed.
- **`api/reqreply` already has this fix as its reference
  implementation.** `reqreply.Client` has only three methods (`Attach`/
  `Call`/`CallAsync` — no SSE-like second entry point). `CallAsync`'s own
  doc comment confirms it shares `Call`'s dual-mode dispatch. At the
  adapter level, `adapters/mqtt5/reqreply_transport.go`'s
  `recoverRouteHandleValue` is in fact the **original** mechanism
  `nethttp`'s `recoverClientRouteHandleValue` was modeled after —
  reqreply is the source, not a follower, here. Zero gap, zero action
  needed.

The remaining scope of this doc is therefore **REST-only**, and narrower
than the original title suggested: only `Consume` needs an actual code
change; `Call` needs a regression test and documentation.

## Scope decisions

| In scope | Out of scope |
|---|---|
| A regression test proving `Client.Call`'s already-shipped dual-mode dispatch actually resolves `GlobalSecurity` end-to-end | Re-implementing `Call` itself — nothing to build there, it already works |
| `rest.Client.Consume`/`nethttp`'s SSE client transport gaining the identical dual-mode dispatch `Call` already has | Any other REST client transport, since none currently exists — the pattern should be documented here for whoever adds the next one |
| Documenting the `*RouteHandle`/`*SSERouteHandle`-acceptance contract on `Client.Call`/`Client.Consume` and the `ClientTransport` interface | `api/events`/`api/reqreply` — confirmed above to have no analogous gap, zero work needed |

## API surface

`Client.Call`'s signature is unchanged and needs no further
implementation — `adapters/nethttp/clienttransport.go`'s
`recoverClientRouteHandleValue` already accepts either shape:

```go
// api/rest — unchanged; documented here for clarity only.
func (c *Client) Call(ctx context.Context, route any, req any, opts ...ClientCallOptions) (any, error)
```

```go
// adapters/nethttp/clienttransport.go — ALREADY SHIPPED, confirmed via
// code trace (not proposed; real signature, not simplified). Call
// dispatches through this helper today. It returns BOTH the handle value
// (for method calls, e.g. EncodeVars) and its .Elem() struct value (for
// field access, e.g. Descriptor/GlobalSecurity) — downstream code needs
// both, so there is no narrower 2-value form to simplify this to.
func recoverClientRouteHandleValue(routeAny any) (reflect.Value, reflect.Value, error) {
	rv := reflect.ValueOf(routeAny)
	if !rv.IsValid() {
		return reflect.Value{}, reflect.Value{}, rest.TransportTypeMismatchError{
			Want: "rest.Route[Req, Resp] or *rest.RouteHandle[Req, Resp]", Got: fmt.Sprintf("%T", routeAny),
		}
	}
	t := rv.Type()
	switch {
	case t.PkgPath() == restPkgPath && strings.HasPrefix(t.Name(), "Route["):
		// RAW, unregistered Route — derives ClientHandle() fresh;
		// GlobalSecurity stays invisible (accepted limitation).
		handleVal := rv.MethodByName("ClientHandle").Call(nil)[0]
		return handleVal, handleVal.Elem(), nil
	case t.Kind() == reflect.Pointer && t.Elem().PkgPath() == restPkgPath && strings.HasPrefix(t.Elem().Name(), "RouteHandle["):
		// Already-registered *RouteHandle (via route.RegisterHandle(server)) —
		// GlobalSecurity IS populated and visible to resolveClientSecurity.
		return rv, rv.Elem(), nil
	default:
		return reflect.Value{}, reflect.Value{}, rest.TransportTypeMismatchError{
			Want: "rest.Route[Req, Resp] or *rest.RouteHandle[Req, Resp]", Got: fmt.Sprintf("%T", routeAny),
		}
	}
}
```

Note there are no separately-named `isRouteType`/`isRouteHandleType`
predicates today — both checks are inlined directly in the `switch`
above. The `Consume` fix either mirrors this same inline style (one new
function, same shape, for `SSERoute`/`SSERouteHandle`) or extracts named
predicates as part of the same change — see the open design decision
below.

`Consume`'s real signature is a blocking reconnect loop, not a
channel-returning function — the proposed fix is a **small, localized
change** to the ~6 lines near the top of the existing function that
derive `handleVal`/`elem`/`descriptor` once (reused across every
reconnect attempt inside `consumeOnce`), not a rewrite of `Consume`
itself:

```go
// adapters/nethttp/clienttransport.go — real signature; PROPOSED fix
// confined to the handle-derivation lines only (shown inline, replacing
// today's single unconditional "always derive ClientHandle() fresh"
// check), mirroring recoverClientRouteHandleValue's exact two branches.
func (t *clientTransport) Consume(ctx context.Context, sseRouteAny, reqAny, fnAny any, optsVariadic ...rest.ClientConsumeOptions) error {
	// ... opts/obs setup unchanged ...

	// BEFORE (today, the bug): always derives ClientHandle() fresh,
	// regardless of whether sseRouteAny is already a *SSERouteHandle.
	//   rv := reflect.ValueOf(sseRouteAny)
	//   if !rv.IsValid() || rv.Type().PkgPath() != restPkgPath || !strings.HasPrefix(rv.Type().Name(), "SSERoute[") {
	//       return rest.TransportTypeMismatchError{...}
	//   }
	//   handleVal := rv.MethodByName("ClientHandle").Call(nil)[0]

	// AFTER (proposed fix): dual-mode, mirroring recoverClientRouteHandleValue.
	handleVal, elem, err := recoverClientSSERouteHandleValue(sseRouteAny)
	if err != nil {
		return err
	}
	descriptor := elem.FieldByName("Descriptor")
	// ... rest of Consume proceeds completely unchanged from here —
	// EncodeVars/ResolveEventDecoder/resolveClientSecurity(elem, descriptor)
	// inside consumeOnce already operate generically against whichever
	// handleVal/elem was produced.
}
```

**No change needed to `resolveClientSecurity`, `mergeCredentialHeaders`,
or any subsequent dispatch step for either method** — both already
operate generically against the resolved handle's fields via reflection,
regardless of which branch produced it.

## Remaining work

1. **Add a regression test** proving `Call`'s already-shipped mechanism:
   a route declared with no per-route `Security`, a `Server` with
   `AddGlobalSecurity(...)`, registered via `RegisterHandle`, called via
   `client.Call(ctx, handle, req)` — assert the credential-providing
   `ClientMW` Fn is invoked and succeeds. Also assert the accepted
   raw-`Route` limitation still holds (same scenario via the raw
   `Route` — credential Fn is *not* invoked).
2. **Implement the dual-mode fix for `Consume`** — add an
   `*SSERouteHandle[...]` branch alongside the existing raw-`SSERoute[...]`
   check, mirroring `recoverClientRouteHandleValue` exactly.
3. **Document the capability** on `Client.Call`'s and `Client.Consume`'s
   own godoc (showing the `RegisterHandle`-then-call pattern as the
   `GlobalSecurity`-visible alternative) and on the `ClientTransport`
   interface's `Call`/`Consume` doc comments, so future transport
   implementers know dual-mode acceptance is part of the contract, not
   an `nethttp`-specific accident.

## Open design decisions

- **Exact type-check mechanism for the `*SSERouteHandle` branch.**
  Should mirror `recoverClientRouteHandleValue`'s existing inline
  reflect-based checks closely, but needs its own confirmation pass (not
  yet spiked) to ensure it can't accidentally match an unrelated type.
- **Extract a named `recoverClientSSERouteHandleValue` helper, or keep
  the check inline in `Consume`?** `recoverClientRouteHandleValue` was
  extracted as its own function because TWO callers need it (`Call` and
  `CallWithTransport`). `Consume` currently has only one call site, so
  there's no forced-reuse reason to extract a helper yet — but doing so
  now would keep the Call/Consume fixes symmetric and make any future
  second SSE entry point consistent for free. Lean toward extracting it
  (matches the existing Call-side convention, cheap either way), but not
  pre-decided.
- **Whether this should be spiked before implementation**, per this
  skill's usual discipline (throwaway Go prototype, confirm-then-delete)
  — recommended before writing the `Consume` fix for real, even though
  the gap itself needs no further confirmation.
