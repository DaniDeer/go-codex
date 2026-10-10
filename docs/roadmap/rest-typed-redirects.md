# Typed HTTP Redirects — `api/rest`

> **Status:** Design draft — no code written yet. Spun out of a direct
> user question ("how do I send HTTP 3xx responses, especially a 303 See
> Other redirect?") that exposed a genuine gap, not a documentation gap.
> [← Back to Roadmap](index.md)

## Why this exists

`api/rest` has no first-class concept of an HTTP redirect today. A user
asked specifically how to send a 303 See Other (and 3xx in general), and
proposed a concrete design: let a declared route point directly at
ANOTHER declared route as its redirect target — server-side AND
client-side — so neither end has to know the destination as a bare
string URL.

## Confirmed current state (via code inspection, not assumption)

- **`RouteMeta.RespStatus`** is the only existing status-code
  customization point, and it is a STATIC per-route string (default
  `"201"` for POST, `"200"` otherwise) — not a runtime/per-request
  decision. A route could be declared with `RespStatus: "303"`, but every
  response from that route would then always be a 303, which is not
  what a redirect means.
- A `Location` response header CAN already be declared today, as a
  workaround, via the existing generic
  `NewRequiredResponseHeaderParam`/`ResponseHeaderParam` mechanism (it
  accepts any header name) — but this is a manual, unenforced convention,
  not a typed concept the framework understands.
- **The adapter always encodes and writes the handler's `Resp` as the
  response body** (`adapters/nethttp/adapter.go`, `adapters/chi/adapter.go`)
  — there is no way for a handler to suppress the body and return "just a
  redirect" today.
- **Confirmed client-side risk**: `adapters/nethttp` never overrides
  `http.Client.CheckRedirect` anywhere in the codebase (grepped
  exhaustively) — Go's default `net/http` behavior silently auto-follows
  3xx responses, up to 10 hops, with no caller visibility. If the
  `RespStatus="303"` + manual `Location` header workaround above were used
  today, a go-codex client would transparently follow it and decode the
  redirect TARGET's body as if it were the ORIGINAL route's `Resp` type —
  a silent type mismatch, not an error.
- **`rest.Client.Call`** already returns `(any, error)` (type-erased) —
  auto-following a redirect and decoding a DIFFERENT route's `Resp` is
  structurally natural here; no new type-system problem.
- **`rest.CallWithTransport[Req, Resp any]`** is fully generic — `Resp` is
  fixed at the call site. Auto-following to a redirect target with a
  DIFFERENT `Resp` type is a genuine Go-generics problem; it cannot
  transparently return a different type from the same call. See Open
  Design Decision 2.
- **Spec generation only ever renders `Responses[0]`**
  (`primaryStatusFor` in `adapters/nethttp/adapter.go`) — `Responses` is a
  slice in name only today. See Open Design Decision 5, the single
  biggest scope-creep risk this doc flags.
- **Related but distinct**: `docs/roadmap/idea-codec-defined-hateoas.md`
  covers typed links embedded INSIDE a response body for client
  navigation. This feature is about an actual protocol-level 3xx status +
  `Location` header — not a body convention. Cross-referenced, not
  merged.

## Scope decisions (confirmed with the user)

1. **Status codes**: the general redirect family — 301, 302, 303, 307,
   308 — not just 303.
2. **Server-side shape**: a route's redirect points DIRECTLY at another
   registered `rest.Route`/`rest.SSERoute` (a typed target, not a bare
   string), with the `Location` resolved from the target's OWN path
   template plus caller-supplied vars — reusing the existing
   `PathParam`/merge-field machinery rather than inventing a second one.
3. **Client-side shape**: `rest.Client.Call` (already type-erased) SHOULD
   auto-follow a recognized redirect and decode the target route's typed
   `Resp`. `rest.CallWithTransport` (fully generic) does NOT auto-follow
   — its exact behavior is Open Design Decision 2, not yet resolved.
4. **Disable `http.Client`'s automatic redirect-following** by default
   for go-codex-managed client transports, so every redirect becomes an
   explicit, typed, caller-visible concept instead of `net/http`'s silent
   default.

## Proposed API surface (tentative — sketch only, not final)

```go
// RedirectError signals that a handler wants the caller redirected to
// another registered route, instead of returning a normal response body.
// Implements error and slog.LogValuer per the structured-errors convention.
type RedirectError struct {
    Status int // one of 301, 302, 303, 307, 308
    // unexported: resolved target path + the originating route, for
    // the adapter to render the Location header and for the client
    // to recognize and decode against.
}

func (e RedirectError) Error() string
func (e RedirectError) LogValue() slog.Value

// Redirect resolves target's path template against vars and returns a
// RedirectError a handler can return as its error value. Fails with a
// typed RedirectTargetVarError if a var is missing or fails the
// target route's own codec constraint.
func Redirect[TReq, TResp any](status int, target Route[TReq, TResp], vars map[string]string) (RedirectError, error)

// RedirectToSSE mirrors Redirect for an SSERoute target.
func RedirectToSSE[TReq, TEvent any](status int, target SSERoute[TReq, TEvent], vars map[string]string) (RedirectError, error)
```

- Adapter dispatch (`nethttp`, `chi`) recognizes `RedirectError` via
  `errors.As`, BEFORE `ErrorPattern`/`ErrorStatus` matching — this
  mirrors the already-established sentinel-error convention elsewhere in
  the codebase, not a new dispatch shape.
- Client side: `UnrecognizedRedirectError` is returned by `Client.Call`
  if the received `Location` doesn't match any route registered on that
  SAME `Client` — an unrecognized redirect is a hard error, never
  silently followed blind.
- `adapters/nethttp`'s client transport construction overrides
  `http.Client.CheckRedirect` to `http.ErrUseLastResponse` (or
  equivalent) by default, so `net/http`'s own auto-follow never fires
  underneath go-codex's explicit handling.

## Structured errors

Both new error types (`RedirectError`, `RedirectTargetVarError`,
`UnrecognizedRedirectError`) MUST implement `slog.LogValuer` per the
repo's standing structured-errors rule — no exception for this feature.
Per the also-standing internal-package-error-aliasing rule
(`.github/instructions/go-codex.instructions.md`), if any of these are
built on a shared `internal/*` mechanism, the SAME-named public alias
must exist in `api/rest`'s own vocabulary from day one — not retrofitted
later.

## Observer integration

A redirect taken (server-side) and a redirect followed (client-side) are
both candidate `stats.Observer`/`stats.SecurityObserver`-style events —
exact hook names/shapes not yet decided; must be designed alongside
whichever Open Design Decision 1 resolution is chosen, since that
decision affects where in the dispatch path the observer call would sit.

## Unit test plan (sketch, to refine once Open Design Decisions resolve)

| Area | Test |
|---|---|
| Server | A route returning `RedirectError` renders the correct status + `Location` header, no body |
| Server | `Redirect` with a missing/invalid var returns `RedirectTargetVarError`, not a panic |
| Server | 307/308 vs 301/302/303 — confirm method/body semantics aren't silently violated (see ODD 4) |
| Client | `Client.Call` auto-follows a recognized redirect and decodes the TARGET route's `Resp` |
| Client | `Client.Call` returns `UnrecognizedRedirectError` for a `Location` not matching any registered route |
| Client | `http.Client.CheckRedirect` is confirmed disabled by default (no silent double-follow) |
| Spec | Generated OpenAPI output for a redirect-capable route (depends on ODD 5's resolution) |
| chi/nethttp parity | Both adapters behave identically for every case above |

## Files to create (once approved for implementation)

| File | Purpose |
|---|---|
| `api/rest/redirect.go` + `redirect_test.go` | `RedirectError`, `Redirect`, `RedirectToSSE`, target-var resolution |
| `adapters/nethttp/redirect.go` (or inline in `adapter.go`/`client.go`) + test | Server dispatch recognition + `Location` rendering; client `CheckRedirect` override + auto-follow/decode |
| `adapters/chi/redirect.go` (or inline) + test | Same server-side behavior for `chi` |
| `examples/rest-redirect/main.go` | Worked end-to-end example |
| `docs/features/rest-redirects.md`, `docs/guides/rest-redirects.md` | User-facing docs once shipped |
| `docs/roadmap/index.md` row, `zensical.toml` nav entry | Already added by this doc's own creation |

## Out of scope (for now)

- Cross-`Client`/cross-`Server` redirect targets (redirecting to a route
  registered on a DIFFERENT `Client`/`Server` instance) — see ODD 3.
- Redirecting to a bare, unregistered URL (e.g. an external site) — the
  user's own design explicitly wants a TYPED target route, not a string;
  an escape hatch for external URLs is not precluded by this design but
  is not scoped here either.
- `docs/roadmap/idea-codec-defined-hateoas.md`'s in-body typed links —
  related, intentionally not merged (see "Why this exists" above).

## Open design decisions (flagged, NOT resolved — genuinely hard)

1. **How does a handler signal "redirect" given Go's static `(Resp,
   error)` return shape?** The error-return `RedirectError` approach
   above is one candidate. An alternative is a context-based signal
   (mirroring `nethttp.WithResponseHeaders`'s existing pattern) — more
   conceptually honest (a redirect isn't really an "error"), but costs a
   new per-success-path check on every dispatch, not just the error path.
   Not yet decided.
2. **`CallWithTransport`'s exact redirect-received return shape.** Since
   `Resp` is statically fixed at the call site, it cannot transparently
   return a different type. Candidate: return a typed `RedirectError` (or
   similar) as the error value, requiring the caller to make a SEPARATE,
   explicit follow-up call themselves with the right `Resp` type. Needs a
   fully worked example before this is considered resolved, not just a
   one-line sketch.
3. **Cross-`Client`/`Server` redirect targets** — likely deferred
   entirely (different `Server`/`Client` instances may not even share a
   process), but not formally ruled out yet.
4. **307/308 method+body preservation** (per RFC 9110, unlike 303/302
   which become GET) — does `Redirect` need to validate that the TARGET
   route's method matches the ORIGINATING route's method specifically
   for 307/308, and reject the declaration (or the call) if not?
5. **Biggest scope-creep risk**: today's spec generation only ever
   renders `Responses[0]`. A route that sometimes redirects needs its
   generated OpenAPI spec to document BOTH its normal response and the
   possible 3xx. Does this require turning `Responses` into a genuine
   multi-entry mechanism (a real design change to `RouteMeta`/spec
   rendering, not just this feature's own code)? Flagged explicitly — not
   assumed trivial, not assumed out of scope either.
