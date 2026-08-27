# Declarative Middleware — a shared `middleware` package for `route`/`api/*`/`ports`

> **Status:** Design draft — Phase 1 (REST) fully speced with THREE
> worked capabilities: security (`RequireScopes`, now SPEC-DERIVING —
> see "Two attachment points" below), observability
> (`ObservabilityMiddleware`, see "Relationship to Observer" below), and
> general header/cookie/query param spec contribution (`RequestParams`/
> `ResponseParams`, not just security schemes). **EVERY other boundary
> go-codex ships is now COVERED BY DESIGN** (see "Coverage across every
> API/port boundary" below) — events/reqreply get FULL parity with REST
> (Phase 2, same mechanisms, not vague); MCP gets observability-only via
> a decorator shape (Security stays permanently N/A, unchanged); ports
> (`File`/`Cache`/`SQL`/`Dir`) all get the SAME decorator shape `File`
> already proved. Only REST is Phase 1 for IMPLEMENTATION — every other
> boundary remains Phase 2+, but the DESIGN is now concrete and
> consistent everywhere, not left as a hand-wave.
> [← Back to Roadmap](index.md)
>
> Breaking changes are explicitly ACCEPTED for this feature (single
> internal consumer today) — `adapters/nethttp`/`chi`'s `Options.SecurityFunc`/
> `CallOptions.CredentialFunc`/`Options.Observer` fields are ALL REMOVED,
> not deprecated alongside. See "Migration" below.
>
> **Self-review status**: L1-L14 are ALL RESOLVED — three full critical
> review passes, all findings closed or properly tracked. L14
> (forge/Registry, Layer 3, was never checked against this design) was
> resolved by spinning it out into
> [`docs/roadmap/forge-pipeline-middleware.md`](forge-pipeline-middleware.md)
> for a future dedicated design pass, rather than deciding inline. Read
> "Known limitations" before implementing.
>
> **Final pre-implementation consistency pass**: a full end-to-end
> re-read (not a new design review — no new L-numbered findings) caught
> 4 staleness bugs left over from earlier editing rounds, all now fixed:
> `RequireAPIKey`'s example still had the pre-L4 `Fn` signature; the
> Files-to-create table didn't mention wiring
> `middleware.EnsureContextFields` into `adapters/nethttp`'s entry point
> (required by L13); the Observability section's "Class A" list
> conflated `PipelineObserver` (forge, explicitly out of scope per L14)
> with the ports observers this doc actually wires; and
> `nethttp.ObservabilityMiddleware`'s sketch called `to.EndSpan(ctx)`
> with one argument instead of `stats.TraceObserver.EndSpan`'s actual
> two (`ctx, err`). This doc is now considered ready for Phase 1
> implementation.
>
> **Revised this round**: middleware now attaches at TWO points, not
> one — spec-relevant middleware (`Security`/`RequestParams`/
> `ResponseParams`) at `NewRoute(...).Register(builder)` time via a new
> `rest.WithMiddleware` `RouteOpt` (early enough to feed the OpenAPI
> spec); non-spec-relevant/shared middleware (observability, generic
> logging) still at `nethttp.Register(...)`'s variadic parameter, exactly
> as originally designed. This fixes a real sequencing bug in the
> original single-attachment-point design — see "Two attachment points"
> below.

## Core thesis

Routes, channels, and ports should be reducible to what they
fundamentally ARE: a typed **input/request → output/response** contract,
nothing more. Cross-cutting concerns — security, rate-limiting, request
enrichment — do NOT belong baked into a route/channel/port's own
construction or `Options` struct as ad hoc, boundary-specific fields
(`SecurityFunc`, `CredentialFunc`, and any future one-off equivalent).
They belong attached SEPARATELY, via ONE shared, composable mechanism, to
WHICHEVER boundary needs them — REST route, event channel, or
`ports.File`/`Cache`/`SQL` alike.

This is what makes it easy to add authorization to `ports.File` — which
has NO security hook of ANY kind today (unlike REST, which at least has
a `SecurityFunc` field to fix) — with the SAME vocabulary already used
for a REST route, instead of inventing a bespoke mechanism per boundary
type as the need arises. See "Ports get the same treatment" below for a
concrete, structurally-proven sketch — the direct test of whether this
design is genuinely general, not a REST-specific rename of `SecurityFunc`.

`Options.Observer`/`stats.Observer` is Phase 1's SECOND worked use case
proving the same point — see "Observability worked example" below,
which absorbs `Options.Observer` into this SAME mechanism entirely
(REMOVED, not kept as a separate parallel hook).

## Cross-cutting concerns and one-struct-one-call

Reviewed explicitly: does attaching security/observability via
`middleware.Middleware` threaten the "one-struct-one-call" principle
(`docs/concepts/api-contracts.md`) — a caller does the ENTIRE
encode-or-decode direction with one struct value in/out, one call?
**No.** `Register`/`Call`/`ports.File.Read`/`.Write` all remain
single-struct-in/out calls — every `middleware.Middleware` value is an
ADDITIONAL, purely opt-in, variadic parameter alongside the one
struct, never a replacement for it or a second struct a caller must
also assemble.

The one place this design actively IMPROVES on the status quo, rather
than staying merely neutral: today's `SecurityFunc`/`CredentialFunc`
(and any hand-rolled equivalent) can ONLY see the raw wire value (an
`*http.Request`, a raw `map[string]string` of vars), even on boundaries
where a merge-field declaration (`rest.NewRequiredHeaderParam`,
`events.NewTopicParam`, `ports.NewFilePathParam`) has ALREADY produced a
fully-decoded struct field by the time security/authorization runs. A
caller who wants an authorization decision based on that SAME field
previously had to declare it TWICE — once as a merge field (for business
logic) and again as a raw header/var read inside their security
closure. The Req-generic security `Fn` shape (see "API surface" and
"Security worked example" above, and the `ports.File`/`Write` decorator
below) closes this gap: ONE declaration, read by BOTH business logic and
the cross-cutting security check — exactly the "declare once" promise
one-struct-one-call already makes for every other concern in this
library, now extended to security as well.

## Motivation

Two separate but related problems drove this design:

1. **A real, currently-shipping drift bug.** `nethttp.Handler`/`chi.Handler`
   only invoke `opts.SecurityFunc` when it is non-nil:

   ```go
   if len(secReqs) > 0 {
       if credErr := validateSecurityCredentials(r, secReqs, handle.SecuritySchemes); credErr != nil { ... }
       if opts.SecurityFunc != nil {           // ← silent no-op if forgotten
           if err := opts.SecurityFunc(ctx, r, secReqs); err != nil { ... }
       }
   }
   ```

   A route can declare `Security: []route.SecurityRequirement{...}` (the
   OpenAPI spec says "protected") while the caller simply forgets to set
   `Options.SecurityFunc` at `Register` time — the request-format check
   still runs, but NO application-level verification happens at all, and
   ANY correctly-formatted credential is accepted. The spec and the
   runtime behavior can silently drift apart because the two are declared
   in **two different places, at two different times, by two different
   people** (route declaration lives in a shared `domain`/`contract`
   package; `Options.SecurityFunc` is wired later, in `main.go`, per
   adapter, per process).

2. **A desire for a general, reusable "middleware" concept.** `SecurityFunc`
   is one instance of a broader need: reusable, composable
   transform/cleanse/enrich/authorize units, attachable to a route (and,
   eventually, a channel/tool/port), that (a) contribute their OWN
   codec-validated declarative surface (a header/path/query/cookie param
   they read or produce) FORWARD into the attaching route's spec, and (b)
   provide the actual runtime behavior — generalizing net/http's
   `func(http.Handler) http.Handler` composition idiom (already fully
   usable today, since `nethttp.Handler`/`chi.Handler` already return a
   plain `http.Handler`/`http.HandlerFunc`) into something DECLARATIVE
   enough to also feed spec generation, not just wrap a black-box handler.

Also motivating this: real deployments increasingly delegate AUTHENTICATION
(token validation, identity) to infrastructure OUTSIDE the application —
an OAuth2 Proxy sidecar, Keycloak, Envoy's JWT filter — leaving the
application responsible only for AUTHORIZATION (does this caller's
already-validated identity carry the scopes this ROUTE declares it
needs?). Today's `SecurityFunc` already supports this split structurally
(it receives `reqs []route.SecurityRequirement`, the route's OWN
declaration, and can read pre-validated claims from `context`/headers a
proxy already set) — but every caller hand-rolls the "does `reqs` match
what I have" loop themselves (see `examples/adapters-nethttp-security`'s
`flatScopes`/`hasScope`). This is exactly the kind of mechanical,
reusable check a shared helper should provide once.

## Scope decisions

**Phase 1 (this doc, fully speced):** the `middleware` package's core
type, REST's (`api/rest` + `adapters/nethttp`/`chi`) full integration,
and THREE worked capabilities proving the mechanism is genuinely
general, not single-purpose:

1. **Security** — `RequireScopes`, the shared scope-matching predicate,
   and the drift-closing validation (see "API surface" below). NOW
   SPEC-DERIVING: `RequireScopes` builds a complete `Security`
   declaration (scheme, scopes, credential codec) that
   `rest.WithMiddleware` feeds directly into the OpenAPI spec at
   `.Register(builder)` time — a route using this path needs NO separate
   `WithSecurityScheme`/`RouteMeta.Security` call at all (see "Two
   attachment points" and "Security worked example" below). The OLD
   manual-declaration path remains fully available as an escape hatch.
2. **General request/response param spec contribution** — `RequestParams`/
   `ResponseParams` generalize the SAME mechanism beyond security: ANY
   middleware can contribute header/cookie/query param spec entries it
   itself needs (e.g. an API-key middleware documenting "X-API-Key"),
   with zero route-level declaration (see "Header/cookie param
   auto-contribution" below).
3. **Observability** — `ObservabilityMiddleware`, absorbing BOTH classes
   of `stats.Observer` events (including the decode-intrinsic ones, via
   a ctx-based diagnostics ferry) and REMOVING `Options.Observer`
   entirely (see "Relationship to Observer" below). Purely non-spec —
   attaches at the OTHER attachment point (`nethttp.Register`'s variadic
   parameter), never via `rest.WithMiddleware`.

**Phase 2+ (staged for IMPLEMENTATION, DESIGN already complete and
consistent for EVERY boundary — see "Coverage across every API/port
boundary" below):** `api/events`/`api/reqreply` (`mqtt`/`mqtt5`/`zeromq`
adapters) get FULL parity with REST's Phase 1 design (message-shaped
variant); `api/mcp`/`mcpgo` get observability ONLY, via the
decorator-shaped variant (Security stays permanently N/A, unchanged
design); `ports.File`/`Cache`/`SQL`/`Dir` all get the SAME
decorator-shaped variant `File`'s sketch already proves, mechanically
extended. Unlike the first version of this doc, NONE of these are left
vague — only the actual IMPLEMENTATION (real method/function signature
changes per package) remains Phase 2+.

**Explicitly NOT in scope, by design, at any phase:** deriving a route's
security/params from the mere PRESENCE of some attached middleware
(inspection-based inference — "a middleware is attached, therefore GUESS
it means scheme X is required"). This was rejected and remains rejected
— see "Why not infer" below. **What IS in scope, resolved this round**:
a middleware VALUE can carry a COMPLETE, EXPLICIT declaration (a
`Security`/`RequestParams`/`ResponseParams` field, populated by
CONSTRUCTOR ARGUMENTS the caller supplies) that `rest.WithMiddleware`
feeds into the spec DIRECTLY — attaching the middleware IS the
declaration, not an inference from its presence. `RouteMeta.Security`/
`rest.WithSecurityScheme` remain fully available UNCHANGED as a manual
escape hatch (cross-checked for conflicts against any middleware-derived
declaration — see "Drift-closing validation" below); they are no longer
the ONLY way to reach the spec. See "Two attachment points" and "Why not
infer" below for the full resolution and the precise distinction between
"explicit declaration carried by value" (in scope) and "inference from
presence" (rejected).

## Why not infer security schemes from attached middleware

Two failure modes make inference strictly worse than the drift bug it
would replace:

- **Attach middleware, forget the spec declaration** → the app enforces
  security correctly, but the OpenAPI spec says the route is public —
  consumers/API gateways/generated clients get a wrong, unauthenticated
  contract for a route that will actually reject them.
- **Declare `Security`, forget to attach the inference-triggering
  middleware** → EXACTLY today's bug, just moved to a different missing
  piece.

Both are silent, hard-to-notice failures at construction time — the same
class of problem this design sets out to fix. An EXPLICIT declaration
(`RouteMeta.Security`) cross-checked against an EXPLICIT, mandatory
enforcement attachment (`middleware.Middleware`, see "Drift-closing
validation" below) closes the loop with a **loud, construction-time
error** instead of either silent failure mode.

**This is NOT reversed by "Two attachment points" below — it is
sharpened.** The rejection above is specifically about INFERRING facts
(which scheme, which scopes) from the mere FACT that some middleware
object is attached, with no explicit statement of what it declares. What
"Two attachment points" adds is different in kind: `middleware.Middleware`
itself carries EXPLICIT fields (`Security`, `RequestParams`,
`ResponseParams`) whose CONTENTS are supplied by the caller as ordinary
constructor arguments (`RequireScopes(scheme, scopeName, scopes, ...)`) —
there is no guessing involved. Attaching the middleware and declaring the
scheme are now the SAME act, at the SAME call site, not two acts a caller
could perform independently and let drift apart. The two REJECTED failure
modes above cannot occur under this model: there is no longer a separate
"forget the spec declaration" step to skip (the declaration IS the
attachment), and the old manual path (still fully supported) remains
cross-checked exactly as designed.

## Two attachment points — resolving a sequencing bug in the original design

The single-attachment-point design (attach ALL middleware at
`nethttp.Register(mux, handle, fn, opts, mws...)`) has a structural bug:
OpenAPI spec generation happens EARLIER, when a route calls
`rest.NewRoute(...).Register(builder)` — this is where
`RouteHandle.SecuritySchemes`/`Security` are frozen into an immutable
snapshot (see `.github/instructions/go-codex.instructions.md`'s note on
`Builder` types taking a snapshot at `Register()` time). By the time
`nethttp.Register` runs, in a typical program, the spec has ALREADY been
generated (often printed/served) — middleware attached there is
STRUCTURALLY TOO LATE to influence it. Any attempt to make
`nethttp.Register`-attached middleware "spec-deriving" would not work.

**Resolution: split attachment by concern, matching WHEN each kind of
information is needed:**

1. **Spec-relevant middleware** (carries a `Security` and/or
   `RequestParams`/`ResponseParams` declaration) attaches via a NEW
   `rest.WithMiddleware(mw)` `RouteOpt`, passed to `NewRoute(...)` — this
   runs INSIDE `.Register(builder)`, early enough to feed the SAME
   snapshot-taking logic `WithSecurityScheme`/`NewRequiredHeaderParam`
   already use. The resulting `*RouteHandle` gains a NEW
   `Middlewares []middleware.Middleware` field, carrying the middleware
   FORWARD so `nethttp.Register(mux, handle, fn, opts)` can retrieve and
   apply it automatically — nothing to re-declare at the transport layer.
2. **Non-spec-relevant / shared middleware** (observability, generic
   logging, rate-limiting — nothing to contribute to the spec) stays
   attachable at `nethttp.Register(mux, handle, fn, opts, extraMws...)`,
   UNCHANGED from the original design — this is ALSO the natural way to
   wire ONE shared middleware (e.g. `ObservabilityMiddleware`) across MANY
   routes without repeating a `rest.WithMiddleware(...)` call on every
   single `NewRoute`.
3. `nethttp.Register` combines BOTH sources
   (`handle.Middlewares` followed by the variadic `extraMws`, in that
   order) before running drift-closing validation and building the
   handler — a middleware attached EITHER way behaves identically at
   runtime; only the ATTACHMENT POINT differs, driven purely by whether
   it has anything to declare into the spec.

See "Security worked example" below for the full annotated
`NewRoute`/`Register` call showing both attachment points side by side.

## API surface

### `middleware` package (new, shared — sibling to `route`)

```go
package middleware // github.com/DaniDeer/go-codex/middleware

// Middleware is a named, composable enrichment/enforcement unit,
// attached at Register (server) or Call (client) time — REPLACING
// today's adapter-specific Options.SecurityFunc/CallOptions.CredentialFunc
// fields with one shared, explicit, harder-to-forget mechanism.
//
// Fn is deliberately untyped (any) — resolved by the SPECIFIC adapter
// function that consumes it, mirroring the SAME type-erasure +
// call-site-assertion idiom already used by [ports.Pattern]'s CustomFormat
// and [codex.Mutable]'s Observer field: one shared struct shape, a
// concrete function signature chosen by whichever adapter/role consumes
// it. A Middleware built for the wrong adapter/role fails LOUDLY with a
// typed [MiddlewareShapeError] at Register/Call time — never silently.
//
// Two concrete Fn shapes exist for adapters/nethttp+chi (Phase 1):
//   - General-purpose: func(http.Handler) http.Handler — the exact
//     net/http/chi middleware idiom, applied OUTSIDE codec
//     decode/validation, with NO visibility into the route's declared
//     security requirements OR the decoded/merged request value (correct
//     for logging, request-ID injection, rate limiting — concerns that
//     don't need route or request-shape awareness at all). Attaches ONLY
//     at nethttp.Register's variadic parameter — never spec-relevant, so
//     never carries Security/RequestParams/ResponseParams below.
//   - Security-specific: func(ctx context.Context, r *http.Request, req
//     *Req) (map[string][]string, error) — GENERIC over Req, and req is
//     a POINTER (unlike today's SecurityFunc, which only ever saw the
//     raw *http.Request, read-only). Invoked INSIDE Handler, AFTER
//     path/query/header/cookie merge into req has already happened — so
//     a security Fn reads an already-decoded, already-merged struct
//     field directly (e.g. req.TenantID, merged from an X-Tenant-ID
//     header) instead of re-parsing r.Header itself, AND can WRITE a
//     new/changed field back (e.g. req.UserID, derived from verified
//     claims) using the SAME codex.DecodeVars/RequiredField/
//     OptionalField vocabulary the route itself uses for merge fields —
//     see "Dedicated codecs inside middleware" below.
//
//     IMPORTANT — this Fn's ONLY job is AUTHENTICATION (extract this
//     credential, return the scopes it grants); it does NOT decide
//     pass/fail against the route's declared requirements itself (no
//     []route.SecurityRequirement parameter at all). Handler runs EVERY
//     attached security Fn, merges their returned grants, and performs
//     ONE final route.Satisfied AUTHORIZATION check afterward — see "L4"
//     in "Known limitations" below for why this split is necessary
//     (multiple Fns each independently checking route.Satisfied against
//     only their OWN grants is an actual bug for AND-combined
//     multi-scheme routes, not just a style choice). A caller free to
//     ignore req entirely (never read, never write) gets today's EXACT
//     SecurityFunc extraction behavior as a strict subset — this is
//     additive, not a forced migration.
type Middleware struct {
    // Name identifies this middleware in errors and observability.
    Name string

    // Fn is the adapter/role-specific closure — see the two Phase 1
    // shapes above. Never called directly by this package.
    Fn any

    // Satisfies lists the security scheme names (matching WithSecurityScheme's
    // declared name) this middleware ENFORCES. Empty for non-security
    // middleware (logging, rate limiting, etc.) — such middleware is
    // never consulted by the drift-closing validation below.
    Satisfies []string

    // Security, when non-nil, is a COMPLETE security scheme +
    // requirement declaration — NOT inferred from anything (see "Why
    // not infer" above). Only meaningful when this Middleware is
    // attached via [rest.WithMiddleware] (spec-relevant attachment
    // point) — ignored (with no effect) if attached at
    // nethttp.Register's variadic parameter instead, since the spec is
    // already frozen by then. See "Two attachment points" above.
    Security *SecurityDeclaration

    // RequestParams contributes additional header/cookie/query param
    // spec entries this middleware itself needs represented — e.g. an
    // API-key middleware documenting "X-API-Key". Type-erased (any),
    // resolved by the consuming adapter/API package (rest.HeaderParam,
    // rest.CookieParam, rest.QueryParam, ...), same idiom as Fn. Only
    // meaningful via [rest.WithMiddleware], same caveat as Security.
    RequestParams []any

    // ResponseParams mirrors RequestParams for response-side spec
    // contributions (rest.ResponseHeaderParam/ResponseCookieParam-shaped)
    // — e.g. documenting a rate-limit response header this middleware's
    // Fn writes via [nethttp.WithResponseHeaders] at runtime (see
    // "Response-path forwarding" below). Only meaningful via
    // [rest.WithMiddleware].
    ResponseParams []any
}

// SecurityDeclaration is a COMPLETE, explicit security scheme +
// requirement declaration, carried BY a Middleware value — the thing
// that makes attaching a security Middleware also BE the spec
// declaration (see "Two attachment points" above). Every field here is
// supplied by the CALLER as ordinary constructor arguments (e.g. to
// [nethttp.RequireScopes]) — nothing is inferred from the Middleware's
// mere presence.
type SecurityDeclaration struct {
    // SchemeName is the scheme's name in the OpenAPI/AsyncAPI
    // components.securitySchemes map — matches what WithSecurityScheme
    // would have used manually.
    SchemeName string

    // Scheme is the scheme's spec metadata — e.g. route.BearerScheme("JWT").
    Scheme route.SecurityScheme

    // Scopes are the scopes this declaration requires for the attached
    // route — becomes one route.SecurityRequirement entry.
    Scopes []string

    // Codec, when non-nil, format-validates the raw credential before
    // any Fn runs — identical role to [rest.SecurityScheme.Codec] today.
    Codec *codex.Codec[string]
}
```

### `route.Satisfied` — the shared scope-matching predicate

```go
package route

// Satisfied reports whether granted — a scheme name → granted-scopes map
// — satisfies AT LEAST ONE requirement in reqs (OR across requirements,
// AND within one requirement's scheme+scopes — the same semantics
// []SecurityRequirement already has in the OpenAPI/AsyncAPI spec itself).
// A scheme present in granted with a nil/empty scope slice is treated as
// "authenticated, no scope restriction" — satisfies any requirement for
// that scheme with an empty scopes list (e.g. plain apiKey/bearer schemes
// that don't use OAuth2 scopes at all).
func Satisfied(reqs []SecurityRequirement, granted map[string][]string) bool
```

Pure, transport- and error-agnostic — lives in `route` alongside
`SecurityRequirement`/`Require` themselves, not in `middleware` (which
stays adapter/role-facing) or in any single `api/*` package (which would
duplicate it four times, same rationale that already keeps
`SecurityRequirement` itself in `route`).

### `adapters/nethttp` — Register/Call signature changes

```go
// BREAKING: Options loses SecurityFunc. Register/Call gain a variadic
// middleware.Middleware parameter for NON-SPEC/SHARED middleware (see
// "Two attachment points" above) — general-purpose ones wrap the
// http.Handler outermost-in, in the order given; security-specific ones
// (if attached HERE rather than via rest.WithMiddleware — still legal,
// just with no spec effect) are invoked from inside Handler, AFTER merge
// (req is fully decoded and merged by then), at the same point
// SecurityFunc used to run.
//
// Register combines TWO sources before validating/applying:
// handle.Middlewares (populated by rest.WithMiddleware at declaration
// time — see below) FOLLOWED BY the extraMws variadic parameter here —
// a middleware attached either way behaves identically at runtime.
// Register knows Req's concrete type, so it can safely type-assert a
// security Fn's Req-generic (now pointer) signature against it.
//
// Handler runs EVERY attached security-specific Fn IN ATTACHMENT ORDER
// (fail-fast on the FIRST one whose OWN credential extraction errors),
// MERGES their returned grants into ONE map, then performs a SINGLE
// route.Satisfied check via middleware.CheckScopes — see "L4" in "Known
// limitations" below for why each Fn does NOT independently decide
// pass/fail against the route's full requirement set.
func Register[Req, Resp any](
    mux *http.ServeMux,
    handle *rest.RouteHandle[Req, Resp],
    fn HandlerFunc[Req, Resp],
    opts Options, // SecurityFunc field REMOVED
    extraMws ...middleware.Middleware,
) error // NEW return — was previously void

// BREAKING: CallOptions loses CredentialFunc. Call gains the same
// variadic middleware.Middleware parameter (client-side middleware has
// no spec to feed, so there is only ONE attachment point for Call — the
// two-attachment-point split is a SERVER-side concern); the concrete Fn
// shape for a client-side credential provider is:
//   func(ctx context.Context, reqs []route.SecurityRequirement) (http.Header, error)
// — IDENTICAL to today's CredentialFunc type, just carried inside a
// Middleware value instead of a bare Options field.
//
// Multiple credential-providing middlewares (see L9): Call runs ALL of
// them, in attachment order, and MERGES their returned http.Header
// values into one combined header set on the outgoing request — this
// is a MERGE, not an authorization check (the client never judges its
// own authorization, only the server does). Two middlewares setting the
// SAME header key to DIFFERENT values is a ConflictingCredentialHeaderError;
// identical values are allowed silently; any Fn's own error aborts the
// call immediately (fail-fast).
func Call[Req, Resp any](
    ctx context.Context,
    client *http.Client,
    baseURL string,
    handle *rest.RouteHandle[Req, Resp],
    req Req,
    vars map[string]string,
    opts CallOptions, // CredentialFunc field REMOVED
    mws ...middleware.Middleware,
) (Resp, error)
```

`Register` returning an `error` is itself a breaking change (every
existing call site adds `if err := ...; err != nil`) — necessary because
"the route declares a scheme with no attached middleware satisfying it"
must fail LOUDLY at wiring time, not be silently swallowed or deferred to
first request. `chi.Register` mirrors this exactly (chi's `Router`
already natively supports `.With(stdMiddlewareFuncs...)` for the
general-purpose case — chi callers can keep using that OR pass
general-purpose `middleware.Middleware` values through the same variadic
parameter for consistency with nethttp; both are equivalent, caller's
choice).

### `rest.WithMiddleware` — the spec-relevant attachment point

```go
// WithMiddleware attaches mw to the route being declared. If mw.Security
// is non-nil, applyRoute populates rb.securitySchemes[mw.Security.SchemeName]
// AND appends a route.SecurityRequirement to rb.meta.Security — the SAME
// internal fields WithSecurityScheme/RouteMeta.Security populate manually
// — so the OpenAPI spec reflects it with ZERO separate declaration. If
// mw.RequestParams/ResponseParams are non-empty, each entry is type-
// asserted to a known rest param type (HeaderParam, CookieParam,
// QueryParam, ResponseHeaderParam, ResponseCookieParam) and applied to rb
// exactly as if the route had declared it directly (same applyRoute
// mechanism every existing Param type already uses) — a mismatched type
// fails with a new ParamContributionShapeError at Register time.
// Regardless of Security/RequestParams/ResponseParams, mw is ALSO
// appended to rb — surfacing later as handle.Middlewares, so
// nethttp.Register can retrieve and apply Fn automatically.
//
// Every declaration (manual RouteOpt OR middleware-contributed) is
// tracked by its CONTRIBUTOR ("manual" or a middleware's Name) in rb's
// internal registry (securityContributedBy/requestParamContributedBy/
// responseParamContributedBy) — so ANY two sources naming the SAME
// scheme/param are checked SYMMETRICALLY, not just "manual vs. one
// middleware slot". Differing declarations for the same name return
// ConflictingSecurityDeclarationError/ConflictingParamContributionError;
// IDENTICAL redundant declarations are allowed silently (see "L3" in
// "Known limitations" below for the full comparison rules) — safety
// preserved, matching the "loud at construction time" philosophy "Why
// not infer" establishes.
func WithMiddleware(mw middleware.Middleware) RouteOpt
```

Usage — attaching `RequireScopes` (built below) needs NOTHING else on the
route:

```go
handle, err := rest.NewRoute[GetProfileReq, ProfileResp]("GET", "/profile",
    reqCodec, respCodec,
    rest.RouteMeta{OperationID: "getProfile"}, // NO Security field set
    rest.WithMiddleware(scopesFromProxy),      // ← spec AND enforcement, one line
).Register(builder)
```

`events.WithMiddleware`/`reqreply.WithMiddleware` mirror this exactly for
Phase 2+ — same mechanism, different `ChannelOpt`/`RouteOpt` interface.

### Drift-closing validation

Two validation points now exist, matching the two attachment points:

1. **At `.Register(builder)` time** (spec-relevant path): trivially
   satisfied whenever `rest.WithMiddleware` is used, because the SAME
   `Middleware` value that declares `Security` also carries the `Fn` that
   enforces it — there is no way to attach one without the other, since
   they are the SAME value. No separate check is needed for THIS path;
   the mechanism design itself closes the gap.
2. **At `nethttp.Register` time** (the escape-hatch/legacy path — a route
   with a MANUALLY declared `RouteMeta.Security`/`WithSecurityScheme`,
   with no `rest.WithMiddleware` used for that scheme at all):

```go
secReqs := handle.Descriptor.Security
if secReqs == nil {
    secReqs = handle.GlobalSecurity
}
allMws := append(append([]middleware.Middleware{}, handle.Middlewares...), extraMws...)
for _, req := range secReqs {
    for scheme := range req {
        if !satisfiedByAny(allMws, scheme) {
            return MissingSecurityMiddlewareError{Route: handle.Descriptor.Path, Scheme: scheme}
        }
    }
}
```

`MissingSecurityMiddlewareError{Route, Scheme}` (new, `slog.LogValuer`) —
returned immediately, before any request is ever served. This closes the
gap for the MANUAL-declaration path exactly as originally designed;
`rest.WithMiddleware`'s spec-deriving path never reaches this check with
a gap to find, by construction.

### Security worked example — `nethttp.RequireScopes`

`nethttp.RequireScopes`/`mqtt5.RequireScopes`/`mqtt.RequireScopes` are
ALL one-line wrappers around the SHARED `middleware.RequireScopes[Raw,
Req]` generic core (see "L2" in "Known limitations" below for the full
rationale and code) — the extract→`route.Satisfied`→wrap-error logic
lives EXACTLY ONCE, in `middleware`, not reimplemented per adapter:

```go
// adapters/nethttp
//
// RequireScopes builds a security Middleware that is BOTH the spec
// declaration (via Security, consumed by rest.WithMiddleware) AND the
// runtime scope check (via Fn, consumed by nethttp.Register) — ONE call
// produces both, so a route using this never declares
// WithSecurityScheme/RouteMeta.Security separately at all. Pins Raw to
// *http.Request; the ACTUAL logic lives in middleware.RequireScopes.
//
// extract returns the caller's GRANTED scopes (however obtained — read
// from context set by an upstream net/http middleware translating an
// OAuth2 Proxy/Keycloak/Envoy JWT filter's headers, a locally-verified
// JWT, anything) — PURE AUTHENTICATION, nothing more. AUTHORIZATION (the
// mechanical scope-match against the route's declared requirements) is
// NOT done here — it is done ONCE by the adapter, AFTER merging every
// attached security Fn's grants, via middleware.CheckScopes. See "L4" in
// "Known limitations" below for why this split is necessary.
//
// Generic over Req: extract receives the ALREADY-DECODED, ALREADY-MERGED
// request value (as *Req — see "Dedicated codecs inside middleware"
// below for using req's WRITE access) alongside the raw *http.Request. A
// caller that only needs r/ctx (the common case) simply ignores req.
func RequireScopes[Req any](
    schemeName string,
    scheme route.SecurityScheme,
    scopes []string,
    credentialCodec *codex.Codec[string],
    extract func(ctx context.Context, r *http.Request, req *Req) (map[string][]string, error),
) middleware.Middleware {
    return middleware.RequireScopes[*http.Request, Req](schemeName, scheme, scopes, credentialCodec, extract)
}
```

`chi` reuses this EXACT function directly (identical `Raw` type,
`*http.Request`) — it does not declare its own `RequireScopes` at all.
`mqtt5.RequireScopes`/`mqtt.RequireScopes` are the SAME one-line pattern,
with `Raw = *pahomqtt5.Publish`/`pahomqtt.Message` respectively; `zeromq`
reuses THOSE directly (see "L2" below for the full picture).

### How declaring a route with middleware looks — fully annotated

```go
// ── Declaration (contract package — shared by server and client) ──────
//
// scopesFromProxy is built ONCE, reused across every route needing
// "oauth2" — the scheme/scopes/codec live HERE, not repeated per route.
var scopesFromProxy = nethttp.RequireScopes[GetProfileReq](
    "oauth2",                                    // ← spec: components.securitySchemes["oauth2"].name
    route.OAuth2Scheme(route.OAuthFlows{ /* ... */ }), // ← spec: scheme type/flows
    []string{"profile:read"},                    // ← spec: operation's security[].oauth2 scopes
    nil,                                          // no format codec needed for this scheme
    func(ctx context.Context, _ *http.Request, req *GetProfileReq) (map[string][]string, error) {
        groups, _ := ctx.Value(groupsKey{}).([]string) // set by withProxyGroups, below
        return map[string][]string{"oauth2": groups}, nil
    },
)

handle, err := rest.NewRoute[GetProfileReq, ProfileResp]("GET", "/profile",
    reqCodec, respCodec,
    rest.RouteMeta{
        OperationID: "getProfile", // ← spec: operationId (ordinary, unrelated to security)
        // NOTE: NO Security field set here — rest.WithMiddleware supplies
        // it below. Setting BOTH for the SAME scheme with different
        // scopes/details is a construction-time error (see "Drift-closing
        // validation").
    },
    rest.WithMiddleware(scopesFromProxy), // ← spec: security scheme+requirement, AND enforcement — ONE line
).Register(builder)
if err != nil {
    log.Fatal(err) // e.g. ConflictingSecurityDeclarationError if misdeclared elsewhere
}

// ── Wiring (main.go / application layer) ───────────────────────────────
//
// A plain net/http middleware — standard idiom, zero go-codex API —
// translating a proxy-injected header into context, BEFORE go-codex ever
// sees the request. This is where AuthN (delegated) lives; it needs NO
// go-codex-specific type at all (plain func(http.Handler) http.Handler).
func withProxyGroups(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        groups := strings.Split(r.Header.Get("X-Auth-Request-Groups"), ",")
        ctx := context.WithValue(r.Context(), groupsKey{}, groups)
        next.ServeHTTP(w, r.WithContext(ctx))
    })
}

mux := http.NewServeMux()
// handle.Middlewares (populated by rest.WithMiddleware above) is applied
// AUTOMATICALLY here — no extraMws argument needed for scopesFromProxy;
// it was already attached at declaration time.
if err := nethttp.Register(mux, handle, profileFn, nethttp.Options{}); err != nil {
    log.Fatal(err)
}
handler := withProxyGroups(mux) // general net/http wrapping, unchanged idiom
```

`builder.OpenAPISpec()` now renders `components.securitySchemes.oauth2`
and `paths./profile.get.security` — EXACTLY as if `WithSecurityScheme`/
`RouteMeta.Security` had been declared by hand — even though NEITHER was
called on this route. Nothing else changes: `getProfile`'s
`operationId`/response schema render identically to any other route.

A more realistic `extract` reads a MERGED field directly — e.g. a route
declaring `rest.NewRequiredHeaderParam[GetProfileReq, string]("X-Tenant-ID", ...)`
merged onto `req.TenantID`: `extract` becomes `func(ctx, r, req
*GetProfileReq) (map[string][]string, error) { return
tenantScopes(req.TenantID), nil }` — no `r.Header.Get` call at all, the
declared merge field IS the access path (see "Cross-cutting concerns and
one-struct-one-call" above).

### New structured errors

- `MissingSecurityMiddlewareError{Route, Scheme string}` — `Register`/
  `Call` refuse to wire a route/call whose declared scheme has no
  attached middleware satisfying it.
- `UnsatisfiedScopesError{Requirements []route.SecurityRequirement, Granted map[string][]string}` —
  returned by `middleware.CheckScopes`, called ONCE by the ADAPTER (not
  by any individual security `Fn` — see "L4" in "Known limitations"
  below) after merging every attached `Fn`'s extracted grants, when the
  COMBINED result doesn't satisfy the route's declared requirements;
  wraps into `rest.SecurityError` exactly like any other
  `SecurityFunc`-shaped error does today.
- `MiddlewareShapeError{Name, Expected, Got string}` — a `Middleware.Fn`
  whose concrete type doesn't match what the consuming adapter/role
  expects (e.g. a general-purpose `func(http.Handler) http.Handler` value
  passed where a security-specific closure was required, or vice versa).
- `ConflictingSecurityDeclarationError{Route, Scheme string, FirstSource,
  SecondSource string, FirstScopes, SecondScopes []string}` —
  `rest.WithMiddleware`/`.Register(builder)` refuse to build a route
  where TWO declarations for the SAME scheme (each EITHER a manual
  `RouteMeta.Security`/`WithSecurityScheme` call OR an attached
  middleware's `Security` field — `FirstSource`/`SecondSource` name
  which, `"manual"` or a middleware's `Name`) DIFFER in scheme type or
  scopes — returned instead of silently picking one. Generalized (see
  "L3" below) to cover EVERY pair of sources, not just manual-vs-one-
  middleware.
- `ParamContributionShapeError{Name, Expected, Got string}` — a
  `Middleware.RequestParams`/`ResponseParams` entry whose concrete type
  isn't a recognized `rest` param type (`HeaderParam`/`CookieParam`/
  `QueryParam`/`ResponseHeaderParam`/`ResponseCookieParam`) — same
  type-erasure-mismatch pattern as `MiddlewareShapeError`, for the param-
  contribution mechanism instead of `Fn`.
- `ConflictingParamContributionError{Route, ParamName, FirstSource,
  SecondSource string}` — the SAME symmetric conflict shape as
  `ConflictingSecurityDeclarationError`, for two DIFFERING
  `RequestParams`/`ResponseParams` declarations (manual OR
  middleware-contributed) naming the SAME param (see "L3" below).
- `ConflictingCredentialHeaderError{Header, FirstSource, SecondSource string}` —
  `Call` refuses to send a request where TWO attached credential-
  providing middlewares (`FirstSource`/`SecondSource` name each by
  `Middleware.Name`) return DIFFERENT values for the SAME outgoing
  header key; identical values are merged silently (see "L9" in "Known
  limitations" below — the client-side mirror of
  `ConflictingSecurityDeclarationError`'s "allow identical, reject
  differing" rule).
- `ContextFieldNotPreparedError{}` — `ContextField[V].Set` called
  before the owning adapter pre-allocated the shared box via
  `middleware.EnsureContextFields` (see "L13" below) — an
  adapter-wiring mistake, surfaced loudly rather than silently
  degrading to a no-op write.

All new error types implement `slog.LogValuer`, matching every other
structured error in this codebase.

## Observer integration (security use case)

`stats.SecurityObserver.RecordSecurityRejection` still fires on any
middleware-returned error, exactly as it does today for `SecurityFunc`
rejections — `middleware.Middleware`'s `Fn` closure is invoked from the
SAME call site inside `Handler`, just resolved via the new mechanism
instead of a bare `Options.SecurityFunc` field; `RequireScopes`'s own
`Fn` resolves its `stats.Observer` via `stats.ObserverFromContext(ctx)`
and calls `RecordSecurityRejection` itself. This is narrower than it
first appears — see "Observability worked example" below for the FULL
picture: `Options.Observer` itself is removed entirely, not just this
one event kept alive.

## Spec generation impact

Revised this round: `RouteMeta.Security`/`rest.WithSecurityScheme`/
`SecuritySchemes` remain UNCHANGED AS FIELDS, and OpenAPI generation
continues to READ `Descriptor.Security`/`SecuritySchemes`/the route's
header/cookie/query params EXACTLY as it does today — the renderer
(`render/openapi`) needs ZERO changes. What's new is HOW those fields get
POPULATED: `rest.WithMiddleware` (see "Two attachment points"/"API
surface" above) can populate them from an attached middleware's
`Security`/`RequestParams`/`ResponseParams`, in ADDITION to the existing
manual `WithSecurityScheme`/`NewRequiredHeaderParam`-style population —
both paths write into the SAME underlying `routeBuilder` fields, so the
spec is IDENTICAL regardless of which path populated it. This is spec
POPULATION generalization, not a spec FORMAT change; "Why not infer"
above still holds — the population always comes from an EXPLICIT
declaration (either call style), never inferred from presence alone.

## Migration (breaking changes accepted)

- `nethttp.Options.SecurityFunc`, `nethttp.CallOptions.CredentialFunc`,
  and the `chi` equivalents are REMOVED, not deprecated-alongside.
- `nethttp.Register`/`chi.Register` gain an `error` return value.
- Every existing example/test using `Options.SecurityFunc`/
  `CallOptions.CredentialFunc` (`examples/adapters-nethttp-security`,
  `examples/adapters-chi-security`, `examples/adapters-nethttp-client`,
  `examples/mutable-security-keys`) needs rewriting to the
  `middleware.Middleware`-based call sites.
- `nethttp.NewCachingCredentialFunc` keeps its existing
  `CredentialFunc`-shaped signature (`func(ctx, reqs) (http.Header, error)`)
  UNCHANGED internally — it now gets wrapped in a plain
  `middleware.Middleware{Fn: credFn}` at the `Call` call site instead of
  assigned to `CallOptions.CredentialFunc`.
- `nethttp.Options.Observer`/the `chi` equivalent are ALSO REMOVED (not
  deprecated-alongside) — `Handler` never holds or resolves a
  `stats.Observer` reference anywhere; observability is entirely opt-in
  via `nethttp.ObservabilityMiddleware(obs)`, wired the same way as
  `RequireScopes`. Every existing example/test passing `Options.Observer`
  (`examples/adapters-nethttp-security`, `examples/stats-observer`, and
  any other example constructing `nethttp.Options{Observer: ...}`) needs
  rewriting to attach `nethttp.ObservabilityMiddleware` instead.
- `adapters/nethttp`'s internal `report*Errors` helpers
  (`reportQueryErrors`/`reportCookieErrors`/`reportHeaderErrors`/
  `reportPathErrors`/body-decode error reporting) are rewired from
  `obs.RecordValidationError(...)` to `stats.RecordDiagnostic(ctx,
  stats.Diagnostic{...})` — same call sites, same data, different target.
- `rest.RouteHandle[Req, Resp]` gains a NEW `Middlewares
  []middleware.Middleware` field, populated by `rest.WithMiddleware` at
  `.Register(builder)` time — additive, not itself breaking (existing
  handles simply have a nil/empty slice).

## Dedicated codecs inside middleware

Confirmed via code inspection: `codex.DecodeVars(target *T, vars
map[string]string, fields ...FieldCodec[T])` is ALREADY public, and
`codex.RequiredField`/`OptionalField` let ANYONE declare a field with its
OWN dedicated codec. Combined with the security `Fn`'s new `req *Req`
(pointer, see "API surface" above), a middleware can decode+verify a raw
wire value via a codec that is COMPLETELY INDEPENDENT of Req's own
declared fields, then write the result onto Req — **zero new `codex` API
needed**:

```go
// Inside a security Middleware's Fn (req is *Req, from the pointer
// signature above):
var claims Claims
err := codex.DecodeVars(req,
    map[string]string{"bearer": r.Header.Get("Authorization")},
    codex.RequiredField("bearer", claimsCodec, // claimsCodec: codex.Codec[Claims], Decode does REAL JWT verification
        func(r Req) Claims { return r.Claims },
        func(r *Req, c Claims) { r.Claims = c },
    ),
)
```

**Guardrail — do NOT use a verifying codec as a ROUTE-DECLARED merge
field.** `rest.NewRequiredHeaderParam[Req, Claims]("Authorization",
claimsCodec, ...)` looks tempting (same codec, same field) but is WRONG:
`codex.StringValidatorFrom` (which `NewRequiredHeaderParam` uses
internally) renders the header's OpenAPI schema from `claimsCodec.Schema`
DIRECTLY — the spec would document `Authorization`'s schema as CLAIMS'
shape (an object with `sub`/`exp`/etc.), not "a bearer token string",
which is simply wrong documentation. Merge-field `V` must represent the
WIRE shape (string → int/UUID/time.Time — same semantic value, just
typed); a verifying/transforming codec whose output is UNRELATED to the
wire shape belongs INSIDE a middleware's `Fn` (as shown above), called
directly — never declared as a route merge field. This is why
`RequireScopes` above builds `Claims`/similar values inside `Fn`, not via
a `RouteOpt`.

Whether the decoded value should THEN live on `req` (as above — for
fields genuinely part of Req's OWN business contract) or on `ctx` (via
`ContextField[V]`, below — for TRULY cross-cutting data that shouldn't
force every Req to carry it) is exactly the choice "`middleware.ContextField[V]`"
resolves next.

## `middleware.ContextField[V]` — the codec-typed, ctx-carried value bus

For data that should NOT force every route's `Req` to carry
security-specific fields just because SOME deployment attaches an auth
middleware — mirrors the EXISTING `nethttp.WithResponseHeaders`/
`stats.WithObserver`/`ObserverFromContext` ctx-value pattern already
established in this codebase, generalized and made codec-typed:

**Redesigned around a pre-allocated mutable box (see "L13" in "Known
limitations" below)** — mirrors `nethttp.WithResponseHeaders`' EXACT
pre-allocation pattern: the owning adapter allocates a SHARED, mutable
box on `ctx` ONCE, before any `Fn` runs; `Set` mutates the box IN
PLACE instead of returning a new `ctx` — this is what makes it usable
from EVERY `Fn` shape uniformly, including the security `Fn` whose
finalized signature (see "L4") has no `context.Context` in its return
tuple at all:

```go
package middleware

// contextFieldBox is the shared, mutable container every ContextField
// reads/writes through — pre-allocated ONCE per request/call by the
// owning adapter. Because it is a POINTER stored as a ctx value, every
// descendant context derived from the SAME ancestor (any Fn's ctx,
// the handler's internal ctx, a general-purpose http.Handler
// middleware's ORIGINAL ctx even AFTER next.ServeHTTP returns) sees
// the SAME box — ctx values propagate to children, and MUTATING a
// referenced object (rather than replacing an immutable ctx value) is
// what makes the data visible upward too. Same trick
// WithResponseHeaders already uses; ContextField generalizes it to
// arbitrary codec-typed values.
type contextFieldBox struct {
    mu     sync.Mutex
    values map[any]any // key (one per declared ContextField) -> decoded value
}

type ctxFieldBoxKey struct{}

// EnsureContextFields pre-allocates the shared box on ctx if not
// already present — called ONCE by each adapter's entry point (e.g.
// nethttp.Register's outermost wrap, ports.File.Read/Write,
// mcpgo.ToolHandler, events/reqreply adapters), BEFORE any attached Fn
// runs. Idempotent — safe to call more than once on the same ctx chain.
func EnsureContextFields(ctx context.Context) context.Context {
    if _, ok := ctx.Value(ctxFieldBoxKey{}).(*contextFieldBox); ok {
        return ctx
    }
    return context.WithValue(ctx, ctxFieldBoxKey{}, &contextFieldBox{values: map[any]any{}})
}

// ContextField is a codec-typed, ctx-carried value slot — a middleware
// decodes+verifies via ITS OWN dedicated codec and publishes the typed
// result via Set; the handler (or ANY other attached middleware,
// regardless of shape or attachment order) retrieves it fully-typed
// via Get, with zero `any` type-assertion for the consumer. Declared
// ONCE, at package level, shared by every producer/consumer that needs
// this SAME piece of cross-cutting data.
type ContextField[V any] struct {
    key   any // unique per field (e.g. new(int)), same idiom as nethttp's contextKey
    codec codex.Codec[V]
}

// NewContextField declares a ContextField backed by codec — the
// "dedicated codec" for this piece of cross-cutting data.
func NewContextField[V any](codec codex.Codec[V]) ContextField[V]

// Set validates raw via f's codec and writes the decoded value INTO
// the box already present on ctx — mutates in place, returns only an
// error (NOT a new ctx). Callable from ANY Fn shape (general-purpose,
// security-specific, credential-providing, decorator, session-shaped)
// since none of them need to hand a modified ctx back to anyone — the
// box IS the shared channel. Returns ContextFieldNotPreparedError if
// the owning adapter never called EnsureContextFields.
func (f ContextField[V]) Set(ctx context.Context, raw any) error

// Get retrieves the value published by Set, from the SAME shared box.
// ok is false if never Set, OR if the box was never pre-allocated —
// mirrors DiagnosticsFromContext/ObserverFromContext's no-op-when-absent
// safety (Get never panics or errors).
func (f ContextField[V]) Get(ctx context.Context) (V, bool)
```

Usage — the SAME JWT-claims example, published to `ctx` instead of `req`:

```go
var ClaimsField = middleware.NewContextField[Claims](claimsCodec) // package-level, shared

// Inside the security Middleware's Fn — note Set no longer returns a
// new ctx; it mutates the box already on ctx in place:
if err := ClaimsField.Set(ctx, r.Header.Get("Authorization")); err != nil {
    return nil, err // still fails the request the same way as before
}
// ... later, inside the ACTUAL business handler (not the middleware),
// OR inside a general-purpose http.Handler middleware reading it back
// AFTER next.ServeHTTP(w, r) returns — BOTH now work identically ...
claims, ok := ClaimsField.Get(ctx) // fully typed, zero re-parsing, req stays security-agnostic
```

This works IDENTICALLY across every boundary this doc covers — REST,
`ports.File` (Phase 2), and the Phase 2+ events/reqreply mirror — since
all of them already carry `ctx` in their `Fn` signatures, AND (as of
this redesign) each adapter pre-allocates the SAME shared box at its
own entry point; unlike `*Req` mutation, this mechanism is not
boundary-shape-specific at all. Bidirectional as a bonus: because the
box is a shared mutable object rather than a sequence of immutable ctx
replacements, a general-purpose middleware can now `Get` a value `Set`
by an INNER security `Fn` — something the original design could never
do, since `context.Context` values never propagate upward through a
call stack on their own.

## Response-path forwarding

Confirmed via code inspection: `nethttp.WithResponseHeaders(ctx,
h)`/`WithResponseCookies(ctx, ...)` ALREADY exist as a ctx-based
mechanism (pre-allocated by `Handler` before the handler function runs,
read back afterward to write real HTTP response headers/cookies) — a
security/enrichment middleware's `Fn` ALREADY receives `ctx`, so it can
call these DIRECTLY, TODAY's exact mechanism, with **zero new runtime
mechanism needed** for forwarding a header/cookie value to the response:

```go
// Inside a security Middleware's Fn — e.g. echoing a rate-limit budget
// derived from the SAME credential check back to the client:
h := make(http.Header)
h.Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
nethttp.WithResponseHeaders(ctx, h)
```

What IS new: `Middleware.ResponseParams` (see "API surface" above) lets
such a middleware ALSO contribute the SPEC-level declaration — so
OpenAPI documents that `X-RateLimit-Remaining` is part of this route's
response — closing the loop between "runtime write" (existing
`WithResponseHeaders`, unchanged) and "spec declaration" (new
`ResponseParams`, attached via `rest.WithMiddleware` exactly like
`Security`/`RequestParams`).

**Explicitly out of scope**: middleware mutating the TYPED `Resp` struct
itself (as opposed to response headers/cookies, which ARE covered above)
remains unresolved — there is no clean hook point between "handler
produced `Resp`" and "`Resp` gets encoded" in the current pipeline. Left
for a future round if a concrete driver appears.

## Header/cookie param auto-contribution — `RequestParams`/`ResponseParams`

Generalizes the SAME `rest.WithMiddleware` mechanism beyond security:
ANY middleware can contribute header/cookie/query param spec entries it
itself needs, with ZERO route-level declaration — e.g. an API-key
middleware that needs "X-API-Key" present, without the route ALSO
declaring `rest.NewRequiredHeaderParam` for it:

```go
func RequireAPIKey[Req any](headerName string, verify func(ctx context.Context, key string) error) middleware.Middleware {
    return middleware.Middleware{
        Name: "require-api-key",
        RequestParams: []any{
            rest.HeaderParam{Name: headerName, Required: true}.WithCodec(codex.String().Refine(validate.NonEmptyString)),
        },
        Fn: func(ctx context.Context, r *http.Request, req *Req) (map[string][]string, error) {
            return nil, verify(ctx, r.Header.Get(headerName)) // no grants contributed — pure presence/format check
        },
    }
}
```

`rest.WithMiddleware` applies each `RequestParams`/`ResponseParams` entry
to the SAME `routeBuilder` snapshot `rest.HeaderParam`'s own `applyRoute`
method already targets — this is not a parallel mechanism, it is the
SAME one, just reached via a middleware value instead of a direct
`RouteOpt` argument to `NewRoute`. `NewRoute(..., rest.WithMiddleware(
apiKeyMw)).Register(builder)` renders `X-API-Key` in the OpenAPI spec's
`parameters` list, identically to a route declaring
`rest.HeaderParam{Name: "X-API-Key", Required: true}` directly.

## Ports get the same treatment — `ports.File[T]` (sketched, Phase 2 for implementation)

`ports.File[T].Read`/`.Write` are ALREADY pure I/O — `Read(vars,
opts) (T, error)`, `Write(vars, v, opts) (createdDirs, error)` — with
**zero** existing security/authorization hook, unlike REST (which at
least has `SecurityFunc` to fix). This is the direct test of the Core
thesis: does the SAME `middleware.Middleware` shape genuinely generalize,
or is it secretly REST/HTTP-specific?

`File[T]`'s operations are plain method calls, not `http.Handler`-wrapped
— so the concrete `Fn` shape here is a DECORATOR, not a handler-wrapper,
but the STRUCTURE (name, type-erased `Fn`, optional `Satisfies`) is
IDENTICAL to Phase 1's `middleware.Middleware`:

```go
// ports.File[T].Read/.Write gain a variadic middleware.Middleware
// parameter, exactly mirroring nethttp.Register/Call's Phase 1 shape.
func (fh File[T]) Read(ctx context.Context, vars map[string]string, opts FileOptions, mws ...middleware.Middleware) (T, error) {
    next := func() (T, error) { return fh.readRaw(ctx, vars, opts) } // today's existing logic, unchanged
    for i := len(mws) - 1; i >= 0; i-- {
        fn, ok := mws[i].Fn.(func(context.Context, map[string]string, func() (T, error)) (T, error))
        if !ok {
            var zero T
            return zero, MiddlewareShapeError{Name: mws[i].Name, Expected: "file decorator", Got: fmt.Sprintf("%T", mws[i].Fn)}
        }
        prevNext, mw := next, fn
        next = func() (T, error) { return mw(ctx, vars, prevNext) }
    }
    return next()
}
```

A `RequireScopes`-shaped constructor for `ports.File`, reusing the EXACT
SAME `route.Satisfied` predicate REST's `RequireScopes` uses — proving
the scope-matching logic is 100% shared, only the wrap-shape differs per
boundary. Mirroring REST's "L4" resolution (see "Known limitations"
below): `Fn` here is EXTRACTION-ONLY (`func(ctx, vars, req) (map[string][]string,
error)`, no pass/fail decision) — a SEPARATE Fn shape from the
general-purpose decorator (`func(ctx, vars, next) (T, error)`) used for
observability. `Read`/`Write` collect grants from every attached
security-shaped `Fn` in a FIRST pass (none of them call `next` — they
only extract), merge them, run ONE `middleware.CheckScopes` check, and
only THEN invoke the real operation (wrapped by any general-purpose
decorators, in the usual nested fashion):

```go
// ports — mirrors adapters/nethttp.RequireScopes exactly (extraction
// only; NO route.Satisfied check here — see "L4" below for why).
func RequireScopes[T any](schemeName string, scheme route.SecurityScheme, scopes []string, codec *codex.Codec[string], extract func(ctx context.Context, vars map[string]string) (map[string][]string, error)) middleware.Middleware {
    return middleware.Middleware{
        Name:      "require-scopes:" + schemeName,
        Satisfies: []string{schemeName},
        Security:  &middleware.SecurityDeclaration{SchemeName: schemeName, Scheme: scheme, Scopes: scopes, Codec: codec},
        Fn: func(ctx context.Context, vars map[string]string) (map[string][]string, error) {
            return extract(ctx, vars)
        },
    }
}
```

```go
// Usage — a caller reading a config file that should only be readable by
// callers with the "config:read" scope (however AuthN happened upstream —
// the SAME OAuth2-Proxy-in-front topology REST's example uses).
configFile.Read(ctx, vars, ports.FileOptions{},
    ports.RequireScopes[Config]("apiKey", []route.SecurityRequirement{route.Require("apiKey", "config:read")}, extractGrantedScopes),
)
```

Since `ports.File` has NO existing `Security`/`SecuritySchemes` SPEC
concept at all (unlike a REST route, `ports.File` has no OpenAPI/AsyncAPI
document to declare against) there is no drift-closing-validation
equivalent to design here — `Satisfies` is carried for CONSISTENCY with
Phase 1's shape and future spec integration, but nothing currently reads
it for `ports.File`. This asymmetry is expected, not a gap: `ports.File`
was never spec-backed in the first place (see
`docs/concepts/declaring-apis-and-ports.md`'s "non-spec" workflow). For
the SAME reason, `ports.File`'s `Middleware` values NEVER carry
`Security`/`RequestParams`/`ResponseParams` (there is no `rest.WithMiddleware`-
equivalent RouteOpt here, and no spec for it to feed) — `ports.File`
stays PURE runtime-decorator-only, attached directly at the `Read`/`Write`
call site (there is only ONE attachment point for ports, unlike REST's
two, precisely because there is no earlier "declaration time" separate
from the call itself).

### Role symmetry — `Write` needs its OWN decorator shape

`Read`'s decorator shape (`next func() (T, error)`) does NOT fit
`Write` — `Read`'s `T` is an OUTPUT (produced by `next`), but `Write`'s
`T` is an INPUT (the caller already has `v` before calling `Write` at
all). Matching the SAME "both roles of the boundary" requirement that
already governs REST/events/reqreply's publisher/subscriber and
requestor/replier symmetry (see the review checklist's Boundary Symmetry
Guardrail), `Write` gets its OWN decorator shape, threading `v` THROUGH
each middleware instead of producing it:

```go
// ports.File[T].Write — v is an INPUT, so next takes T and can be
// inspected/transformed before the real write happens (e.g. a
// middleware that rejects an oversized v before touching the
// filesystem, or a security check that reads v's OWN fields to decide).
func (fh File[T]) Write(ctx context.Context, vars map[string]string, v T, opts FileOptions, mws ...middleware.Middleware) (createdDirs []string, err error) {
    next := func(v T) ([]string, error) { return fh.writeRaw(ctx, vars, v, opts) } // today's existing logic, unchanged
    for i := len(mws) - 1; i >= 0; i-- {
        fn, ok := mws[i].Fn.(func(context.Context, map[string]string, T, func(T) ([]string, error)) ([]string, error))
        if !ok {
            return nil, MiddlewareShapeError{Name: mws[i].Name, Expected: "file write decorator", Got: fmt.Sprintf("%T", mws[i].Fn)}
        }
        prevNext, mw := next, fn
        next = func(v T) ([]string, error) { return mw(ctx, vars, v, prevNext) }
    }
    return next(v)
}
```

A `RequireScopes`-for-`Write` sketch mirrors the `Read` version exactly
— EXTRACTION-ONLY, same "L4" split (no pass/fail decision inside `Fn`;
`Write` collects grants from every attached security-shaped `Fn` in a
first pass, merges, runs ONE `middleware.CheckScopes`, and only then
calls the real write):

```go
func RequireScopesWrite[T any](schemeName string, scheme route.SecurityScheme, scopes []string, codec *codex.Codec[string], extract func(ctx context.Context, vars map[string]string, v T) (map[string][]string, error)) middleware.Middleware {
    return middleware.Middleware{
        Name:      "require-scopes:" + schemeName,
        Satisfies: []string{schemeName},
        Security:  &middleware.SecurityDeclaration{SchemeName: schemeName, Scheme: scheme, Scopes: scopes, Codec: codec},
        Fn: func(ctx context.Context, vars map[string]string, v T) (map[string][]string, error) {
            return extract(ctx, vars, v)
        },
    }
}
```

This IS the SAME convenience Finding A identified for REST — `extract`
here can inspect `v`'s OWN fields directly (e.g. `v.OwnerID`) instead of
needing a separate raw-wire re-derivation, since `Write`'s `v` is ALREADY
the fully-typed value, never a partially-decoded wire form.

**This sketch is proof, not a commitment to ship in Phase 1** — the
actual `Read`/`Write` signature changes, `MiddlewareShapeError` reuse,
and `ports.Cache`/`ports.SQL` equivalents remain Phase 2 for
IMPLEMENTATION.

## Coverage across every API/port boundary

Reviewed explicitly, per the user's request, against EVERY Layer 2
(request/response or per-call-invoked) boundary go-codex ships today —
REST, events, reqreply, MCP, and ports — not just REST. Resolved to
exactly THREE attachment-shape variants; every boundary maps onto one
of them, with NO fourth shape needed anywhere. **This table does NOT
cover Layer 3 (`forge.Registry`/pipelines)** — see "L14" in "Known
limitations" below; forge/pipeline middleware integration is tracked
separately in
[`docs/roadmap/forge-pipeline-middleware.md`](forge-pipeline-middleware.md).

| Boundary | Spec? | Attachment shape | `Security` available? | `Observability` available? |
|---|---|---|---|---|
| REST (`api/rest` + `nethttp`/`chi`) | OpenAPI | HTTP-shaped | Yes — Phase 1, fully speced above | Yes — Phase 1, fully speced above |
| Events (`api/events` + `mqtt`/`mqtt5`/`zeromq`) | AsyncAPI | Message-shaped | Yes — Phase 2, full parity below | Yes — Phase 2, full parity below |
| ReqReply (`api/reqreply` + `mqtt5`/`zeromq`) | AsyncAPI | Message-shaped | Yes — Phase 2, full parity below | Yes — Phase 2, full parity below |
| MCP (`api/mcp` + `mcpgo`) | MCP manifest/JSON schema | Decorator-shaped | **No — N/A, permanent design** (unchanged) | Yes — Phase 2, below |
| `ports.File`/`Cache`/`SQL`/`Dir` | **None — not spec-backed** | Decorator-shaped | Yes — `File` sketched above, mechanical extension below | Yes — Phase 2, below |

**The three shapes, confirmed as exhaustive:**

1. **HTTP-shaped** (REST) — general-purpose `func(http.Handler)
   http.Handler` + security-specific `func(ctx, *http.Request, *Req,
   reqs) error`. Phase 1, fully speced above.
2. **Message-shaped** (events/reqreply) — security-specific `func(ctx,
   *rawMessage, *T, reqs) error`; `rawMessage`'s concrete type varies per
   transport (`*pahomqtt5.Publish` for MQTT5, `pahomqtt.Message` for MQTT
   3.1.1) but the SHAPE is otherwise identical to REST's — same
   Req-generic-pointer rationale, same reason (merge already happened by
   the time security runs).
3. **Decorator-shaped** (MCP tools, ports) — NO raw wire-request object
   exists as a distinct thing worth exposing (MCP arguments are already
   `map[string]any`; ports vars are already a plain
   `map[string]string`) — `func(ctx, args/vars, *T, next func(...) (T,
   error)) (T, error)`-style, the SAME shape `ports.File`'s sketch
   already uses above.

### Events + ReqReply (message-shaped, full parity with REST)

Confirmed via code: `events.ChannelHandle[T]`/`reqreply.RouteHandle[Req,
Resp]` ALREADY have the IDENTICAL `SecuritySchemes map[string]SecurityScheme`/
`GlobalSecurity []route.SecurityRequirement` fields REST's `RouteHandle`
has — the `WithMiddleware`/`Middlewares`-field mechanism generalizes
MECHANICALLY, not merely "by analogy":

- `events.WithMiddleware`/`reqreply.WithMiddleware` — same mechanics as
  `rest.WithMiddleware`: applies an attached middleware's `Security`/
  `RequestParams`/`ResponseParams` to the SAME builder snapshot
  `WithSecurityScheme`/topic-var params already populate, and stores the
  middleware on the resulting handle's NEW `Middlewares` field.
- `mqtt5.RequireScopes[T]`/`mqtt.RequireScopes[T]` — mirrors
  `nethttp.RequireScopes` exactly, `Fn`: `func(ctx, *pahomqtt5.Publish,
  value *T, reqs) error` / `func(ctx, pahomqtt.Message, value *T, reqs)
  error` — invoked AFTER topic-var merge, same convenience as REST.
- `mqtt5.ObservabilityMiddleware`/`mqtt.ObservabilityMiddleware` — Class
  A events (`RecordSubscribe`/`RecordPublish`, already boundary-shaped —
  see "Relationship to Observer" above) are absorbed directly, exactly
  like REST's `RecordRequest`. Class B (topic-var/payload decode errors)
  reuses the SAME `stats.Diagnostic`/`WithDiagnostics`/`RecordDiagnostic`/
  `DiagnosticsFromContext` ferry REST's `ObservabilityMiddleware` uses —
  `stats.Diagnostic` is NOT REST-specific, so this is REUSE, not new
  design; `adapters/mqtt5`'s internal merge/decode error-reporting call
  sites redirect to `stats.RecordDiagnostic(ctx, ...)` the SAME way
  `adapters/nethttp`'s do.
- `Subscribe`/`Publish`/`Serve`/`Call` combine `handle.Middlewares` +
  variadic `extraMws` before drift-closing validation, identically to
  `nethttp.Register`.
- `adapters/zeromq`'s OWN pub/sub/req-rep bind to the SAME
  `api/events`/`api/reqreply` declarations (different transport, same
  `ChannelHandle`/`RouteHandle`) — no separate design needed; it inherits
  this coverage automatically once `events`/`reqreply`'s OWN middleware
  support ships.
- `adapters/websocket` ALSO binds to `events.Channel`/`ChannelHandle`
  (`RegisterSocket` builds atop it) — it inherits `events.WithMiddleware`
  automatically, PLUS a FOURTH, session-shaped `Fn` variant
  (`func(ctx, sess ports.Session, info map[string]string) error`) for
  its long-lived-connection-specific re-authorization/revocation needs —
  see "L6" in "Known limitations" below for the full design (a
  genuinely different concern from one-shot/per-message boundaries,
  resolved as an EXTENSION of this SAME declare-once mechanism, not a
  bespoke one). SSE's equivalent concern is covered by the ALREADY
  public `stream.BroadcastHub.Unsubscribe` — no new design needed there.

(A related but INDEPENDENT gap surfaced while reviewing this doc:
`adapters/mqtt5.UserPropertyParam` has no merge-into-struct sibling —
see [MQTT5 User Property Merge](mqtt5-user-property-merge.md), a
separate roadmap item with no sequencing dependency on this one.)

### MCP (decorator-shaped, observability ONLY — no Security)

Covers ALL THREE MCP surfaces uniformly — `ToolHandler`,
`ResourceHandler`, AND `PromptHandler` — confirmed via code to already
share the IDENTICAL observability pattern verbatim (resolve `obs`, start
a `TraceObserver` span, call `RecordRequest("<kind>", name, status,
duration)` on every path, differing only in the `<kind>`
`"tool"`/`"resource"`/`"prompt"` string and the identifying name). See
"L5" in "Known limitations" below for the full resolution.

- `mcpgo.ObservabilityMiddleware(obs) middleware.Middleware{Fn: obs}` —
  carries the RAW `stats.Observer` value directly; NO decorator/wrapping
  logic is needed at all, because `ToolHandler`/`ResourceHandler`/
  `PromptHandler` ALREADY contain the full observability logic inline —
  only the SOURCE of `obs` changes (a middleware slot instead of
  `Options.Observer`). **NO ctx-ferry needed either** — all three are
  SINGLE-STAGE decodes (unlike REST's 5-stage pipeline), so the terminal
  outcome is already visible without smuggling intermediate events out
  via `ctx`.
- `ToolHandler`/`ResourceHandler`/`PromptHandler` each gain a variadic
  `mws ...middleware.Middleware` parameter (replacing `Options.Observer`);
  `RegisterTool`/`RegisterResource`/`RegisterPrompt` forward it through
  unchanged.
- **Explicitly confirmed, NOT changed by this design**: NO
  `mcpgo.RequireScopes`/`Security` field use for MCP tools/resources/
  prompts — `api/mcp` has no security methods at all, by PERMANENT
  design (host-application-managed auth; see the skill's own
  Gotchas list). The middleware mechanism doesn't reopen this
  decision — it simply gives MCP its fair share of the OTHER capability
  (observability) via the SAME shared `middleware.Middleware` type,
  using the decorator shape (variant 3), not the HTTP shape (variant 1).
- **Attachment point**: ONE, not two, for ALL THREE surfaces — MCP has
  no separate "declaration time" builder snapshot comparable to REST's
  `.Register(builder)` spec-freezing (there is no security scheme
  concept for it to feed anyway), so middleware attaches directly at
  `mcpgo.ToolHandler`/`ResourceHandler`/`PromptHandler`/`RegisterTool`/
  `RegisterResource`/`RegisterPrompt` call time, exactly like
  `ports.File` below.

### Ports — `File`/`Cache`/`SQL`/`Dir` (decorator-shaped, `File`'s shape extends mechanically)

`ports.File`'s Read/Write decorator shape (sketched above) is the
TEMPLATE for every other port — confirmed via code inspection:
`ports.File`/`adapters/sql` already call `RecordValidationError` from a
SINGLE decode/validate call (not REST's multi-stage pipeline), so NO
`stats.Diagnostic` ferry is needed for ANY port — a plain decorator
wrapping the whole operation already sees the terminal `(T, error)`.

- `ports.Cache[T]` (`Get`/`Set`/`Del`), a SQL-port equivalent
  (`Query`/`Insert`), and `ports.Dir` (`List`) all get the MECHANICALLY
  IDENTICAL decorator shape as `File`'s `Read`/`Write` sketch above,
  parameterized by their own operation signature — no new design
  question per port, a straightforward mechanical extension.
- `ports.ObservabilityMiddleware[T]` — a decorator calling
  `RecordFileRead`/`RecordFileWrite` (or the `CacheObserver`/`SQLObserver`
  equivalent per port) directly around `next()` — no ctx-ferry, matching
  the SAME single-stage reasoning as MCP above.
- `ports.RequireScopes[T]` is ALREADY sketched for `File` above; it
  generalizes to `Cache`/`SQL`/`Dir` the same mechanical way.
- Same as MCP: ONE attachment point (no spec to feed) — middleware
  attaches directly at the `Read`/`Write`/`Get`/`Set`/`Query`/`List` call
  site.

## Observability worked example — `nethttp.ObservabilityMiddleware`

Phase 1's SECOND worked use case (alongside security), proving
`middleware.Middleware` generalizes beyond auth: `stats.Observer`'s
sub-interfaces split into two structurally different classes, and this
design's `middleware.Middleware` mechanism fully absorbs BOTH —
`Options.Observer` is NOT kept as a permanent parallel mechanism; it is
REMOVED, matching `Options.SecurityFunc`'s migration exactly. The
Observability middleware sketched below becomes the ONLY caller of
`stats.Observer` anywhere in `adapters/nethttp`/`chi`.

**Confirmed: unlike the security use case, observability stays
request-value-UNAWARE by design, on every boundary.** `RecordRequest`/
`TraceObserver`/`FileObserver`/etc. are all boundary- or
ctx-ferry-shaped (path/name + success + duration, or a `Diagnostic`
list) — none of them need a merged struct FIELD the way a scope check
might. The general-purpose `func(Handler) Handler` shape (no `Req`
parameter at all) is therefore the CORRECT, sufficient shape for
observability on every boundary this doc covers — Finding A's
Req-generic refinement applies ONLY to the security use case, not
observability. This is not an oversight; it is confirmed by Class A/B's
own shapes above.

**Class A — boundary-shaped (moves to middleware with zero loss):**
`RecordRequest(method, path, status, duration)`, `SecurityObserver.
RecordSecurityRejection`, `TraceObserver.StartSpan`/`EndSpan`, and —
notably — `FileObserver`/`CacheObserver`/`SQLObserver`/`StreamObserver`/
`PipelineObserver` (all `(path/name, success bool, duration)` shaped, NO
field-level granularity). **This list classifies the underlying
`stats.Observer` sub-interfaces' SHAPE only** — it is NOT a claim that
every one of them has designed middleware wiring in THIS doc.
`FileObserver`/`CacheObserver`/`SQLObserver` DO (see "Ports — `File`/
`Cache`/`SQL`/`Dir`" above); `PipelineObserver` belongs to
`forge.Registry` (Layer 3), which this doc explicitly does NOT cover —
see "L14" in "Known limitations" below and
[`forge-pipeline-middleware.md`](forge-pipeline-middleware.md) for its
own, separately-tracked integration question. All of the ones THIS doc
DOES wire need only the OUTER call boundary — exactly what a
general-purpose `func(Handler) Handler` middleware (or the `ports.File`
decorator shape already sketched above) naturally wraps.
`RecordSecurityRejection` specifically is already
planned to move into the security `Middleware`'s own `Fn` (it has `ctx`,
resolves its own `stats.Observer` via `stats.ObserverFromContext`).
`RecordRequest`/`TraceObserver` belong to the NEW general-purpose
Observability middleware below, which wraps the whole call, timing start
to finish and capturing the final status code via its own
`ResponseWriter` wrapper (the same well-known net/http logging-middleware
idiom) — it needs no cooperation from `Handler` at all. `ports.File`'s
`RecordFileRead`/`Write`/`Delete` fit the SAME shape — its decorator
middleware (sketched below) calls them directly around `next()`.

**Class B — decode-intrinsic (needs a ferry, not a rewrite):**
`RecordValidationError(location, constraintName, field)` fires from
MULTIPLE sequential points INSIDE the codec decode/validate pipeline
(query → cookie → header → path → body, fail-fast on first error) — a
point a wrapping-only middleware structurally cannot observe, since
standard middleware sees only what's written to the `ResponseWriter`,
not Handler's internal typed errors.

**Resolution — reuse the EXISTING ctx-mutable-sink pattern.**
`nethttp.Handler` ALREADY pre-allocates a mutable value in `ctx` before
calling the user's handler function and reads it back afterward —
`WithResponseHeaders`/`WithResponseCookies` do exactly this for outgoing
headers/cookies. The SAME pattern, generalized, ferries Class B events
out to the Observability middleware instead of requiring `Handler` to
call an `Observer` directly:

```go
package stats

// Diagnostic is one decode/validate-time observability event, ferried
// out of the codec pipeline via ctx instead of a direct Observer call —
// the Class B counterpart to Class A's boundary-shaped events, which
// need no ferry at all.
type Diagnostic struct {
    Location       string // "query", "cookie", "header", "path", "body", ...
    ConstraintName string
    Field          string
}

// WithDiagnostics pre-allocates a ctx-carried Diagnostic sink — call
// once, at the top of Handler, mirroring [nethttp.WithResponseHeaders]'s
// pre-allocation exactly.
func WithDiagnostics(ctx context.Context) context.Context

// RecordDiagnostic appends d to the sink allocated by WithDiagnostics.
// A no-op if ctx was never so decorated (mirrors WithResponseHeaders'
// same no-op-when-absent safety).
func RecordDiagnostic(ctx context.Context, d Diagnostic)

// DiagnosticsFromContext returns every Diagnostic recorded so far —
// called by the Observability middleware AFTER next.ServeHTTP returns.
func DiagnosticsFromContext(ctx context.Context) []Diagnostic
```

`adapters/nethttp`'s internal `report*Errors` helpers (`reportQueryErrors`,
`reportCookieErrors`, `reportHeaderErrors`, `reportPathErrors`, body
decode errors) change from `obs.RecordValidationError(...)` to
`stats.RecordDiagnostic(ctx, stats.Diagnostic{...})` — the SAME call
site, the SAME data, just redirected into `ctx` instead of a direct
`Observer` call. `Handler` itself never holds or resolves an `Observer`
reference anywhere.

### `nethttp.ObservabilityMiddleware` — the worked example

```go
// adapters/nethttp
//
// ObservabilityMiddleware builds a general-purpose Middleware that wraps
// the ENTIRE call: records RecordRequest (method/path/status/duration),
// drives TraceObserver.StartSpan/EndSpan if obs implements it, and — after
// next.ServeHTTP returns — drains stats.DiagnosticsFromContext(ctx) and
// forwards each to obs.RecordValidationError. This is the ONLY place in
// adapters/nethttp that ever calls into stats.Observer.
func ObservabilityMiddleware(obs stats.Observer) middleware.Middleware {
    return middleware.Middleware{
        Name: "observability",
        Fn: func(next http.Handler) http.Handler {
            return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
                start := time.Now()
                ctx := stats.WithDiagnostics(r.Context())
                if to, ok := obs.(stats.TraceObserver); ok {
                    ctx = to.StartSpan(ctx, "http.request", r.URL.Path)
                }
                sw := &statusWriter{ResponseWriter: w, code: http.StatusOK}
                next.ServeHTTP(sw, r.WithContext(ctx))
                for _, d := range stats.DiagnosticsFromContext(ctx) {
                    obs.RecordValidationError(d.Location, d.ConstraintName, d.Field)
                }
                obs.RecordRequest(r.Method, r.URL.Path, sw.code, time.Since(start))
                if to, ok := obs.(stats.TraceObserver); ok {
                    var spanErr error // http.Handler has no direct Go error — derive from status,
                    if sw.code >= 400 { // matching TraceObserver's "err is the eventual operation
                        spanErr = fmt.Errorf("http %d", sw.code) // error, nil on success" convention
                    }
                    to.EndSpan(ctx, spanErr)
                }
            })
        },
    }
}
```

Usage — `Options.Observer` no longer exists; observability is entirely
opt-in via this one middleware, wired the SAME way as `RequireScopes`:

```go
if err := nethttp.Register(mux, profileHandle, profileFn, nethttp.Options{},
    nethttp.ObservabilityMiddleware(myObserver),
    scopesFromProxy,
); err != nil {
    log.Fatal(err)
}
```

A caller who wants ZERO observability simply omits this middleware —
matching this design's whole thesis: cross-cutting concerns are entirely
opt-in attachments, never implicit `Options` fields.

### `ports.File`'s equivalent — no ferry needed

Because `FileObserver` is already Class A (boundary-shaped, no
field-level detail), `ports.File`'s own observability middleware needs
NO diagnostics ferry — it is a plain decorator, structurally identical to
the `RequireScopes[T]` sketch above, just calling `RecordFileRead`/
`RecordFileWrite` directly around `next()`:

```go
// ports — no ctx-ferry needed; FileObserver has no field-level events.
func ObservabilityMiddleware[T any](obs stats.Observer) middleware.Middleware {
    return middleware.Middleware{
        Name: "observability",
        Fn: func(ctx context.Context, vars map[string]string, next func() (T, error)) (T, error) {
            start := time.Now()
            v, err := next()
            if fo, ok := obs.(stats.FileObserver); ok {
                fo.RecordFileRead(vars["path"], err == nil, time.Since(start))
            }
            return v, err
        },
    }
}
```

## Known limitations and open risks

This is an ACTIVE PUNCH LIST, not a closing critique — each item below
is planned to be tackled (eliminated or mitigated) in a dedicated future
round, one (or a few) at a time. Read this before implementing anything
in this doc; it exists to prevent the design's otherwise-affirmative
tone from hiding real, confirmed trade-offs.

### L1 — Type erasure and compile-time safety

**Status:** RESOLVED (this round) — reframed via a corrected
understanding, plus one genuinely new, optional helper. Not a regression
that needs eliminating; a structural trade-off this codebase already
makes, now confirmed sufficient (with one small addition).

**Corrected understanding (confirmed via code):** `routeBuilder`'s OWN
merge-field fields — `pathMergeFields`/`queryMergeFields`/
`headerMergeFields`/`cookieMergeFields`/`requestFormats`/`respFormats` —
are ALREADY `[]any`/`any`, type-erased EXACTLY like `Middleware.Fn any`,
resolved to `codex.FieldCodec[Req]` inside `Route[Req,Resp].Register()`
(which knows Req), with `MergeFieldTypeError` as the EXACT same kind of
runtime safety net `MiddlewareShapeError` is. This is NOT a regression
middleware introduced — it is the established idiom this entire
codebase already uses for every declarative `RouteOpt`.

**Why this is structurally unavoidable, not a design gap:** `Builder`
accumulates MANY routes with DIFFERENT `Req`/`Resp` types into ONE spec
document. Go generics cannot express "a slice of different
instantiations of a generic type" without type erasure — the
alternative (one `Builder` per `Req`/`Resp` pair) would defeat the
entire purpose of an ACCUMULATING builder. Explicitly considered and
REJECTED as not worth the cost.

**Why `.Register(builder)` already delivers "right at declaration
time" validation for the common case:** `.Register(builder)` is not a
rarely-exercised runtime code path — it is PROGRAM STARTUP, unconditional,
deterministic, hit on the FIRST run of any program (or test) that
actually builds its routes. A middleware shape mismatch surfaces
IMMEDIATELY there — closer to a fail-fast startup check than a silent
runtime bug. The ORIGINAL framing of this finding ("an under-exercised
route could ship broken to production") overstated the risk by treating
`.Register(builder)` like a rarely-hit handler branch, which it is not
— every route a program actually serves calls it, unconditionally, at
startup.

**Tightening applied:** middleware attached via `rest.WithMiddleware`
gets its FULL validation (`Security` merge, `RequestParams`/
`ResponseParams` merge, AND `Fn`-shape assertion) done INSIDE
`.Register(builder)` itself — NOT deferred to `nethttp.Register` — since
`.Register` already knows `Req`, and the `Fn` shape contract
(`*http.Request`-based) doesn't vary between `nethttp`/`chi`. Middleware
attached via `nethttp.Register`'s own variadic `extraMws` (the
non-spec/shared path) is still checked there, at the earliest point ITS
`Req` is known — still declaration-adjacent (called at program startup,
immediately after `.Register(builder)`), same risk profile.

**The one genuine remaining gap — and its resolution:** a route
declared in a SEPARATE domain/contract package (this doc's own
Motivation section describes exactly this pattern: "route declaration
lives in a shared `domain`/`contract` package; `Options.SecurityFunc` is
wired later, in `main.go`") that NEVER calls `.Register(builder)` itself
(deferred to the consuming application) has NO builder to validate
against at ITS OWN test time. **Resolution — a new, OPTIONAL helper**:

```go
// rest.ValidateRoute type-checks an ENTIRE route declaration — EVERY
// RouteOpt a caller would pass to NewRoute (WithSecurityScheme,
// NewRequiredHeaderParam, WithMiddleware, ANY RouteOpt), not just
// middleware.Middleware values — WITHOUT requiring a live *Builder or a
// call to .Register. Builds a scratch, throwaway routeBuilder internally
// (never touching a real shared *Builder) and applies every opt via the
// SAME applyRoute mechanism .Register(builder) uses — for domain/contract
// packages that declare routes independently of their eventual runtime
// wiring (see Motivation's domain-package pattern). Returns the SAME
// MiddlewareShapeError/ParamContributionShapeError/
// ConflictingSecurityDeclarationError/ConflictingParamContributionError a
// real .Register(builder) call would surface, just callable standalone —
// e.g. from the declaring package's own _test.go file. NOTE: renamed
// from an earlier, narrower ValidateMiddleware[Req](mws
// ...middleware.Middleware) sketch that only saw middleware values, not
// the route's OTHER RouteOpts — see "L8" in "Known limitations" below
// for why that was incomplete and how this widened version closes the
// gap.
func ValidateRoute[Req, Resp any](meta RouteMeta, opts ...RouteOpt) error
```

NOT a mandatory step for the common "declare and register in the same
place" case — there, `.Register(builder)` already does this for free.
Purely additive, for the specific split-declaration-from-wiring
architecture. Mirrored as `events.ValidateChannel[T]`/
`reqreply.ValidateRoute[Req]` for the SAME reason, on the SAME
`.Register(builder)`-based boundaries. **Does NOT apply to MCP/ports** —
they have exactly ONE attachment point (`mcpgo.ToolHandler`/
`ports.File.Read`/`.Write` IS both declaration and wiring
simultaneously), so there is no "declare separately from wiring" split
possible there at all; adding `ValidateRoute` to those packages would be
meaningless.

### L2 — Combinatorial `RequireScopes` maintenance burden

**Status:** RESOLVED (this round) — a shared generic core removes the
duplication, and TWO of the originally-assumed 6+ implementations turn
out to be unnecessary entirely.

**Original problem:** `RequireScopes` seemed to need a SEPARATE
implementation per adapter — `nethttp`, `chi`, `mqtt`, `mqtt5`, and
`ports` (×4 operation shapes) — 6+ near-duplicate implementations of the
SAME `route.Satisfied`-wrapping boilerplate, a plausible drift risk (this
exact class of bug already happened once in this session's own history,
at a smaller scale — Round 118's `NewFanout` doc-comment gap).

**Resolution — a shared generic core, `middleware.RequireScopes[Raw, Req]`:**
the ACTUAL extract→`route.Satisfied`→wrap-error logic, written EXACTLY
ONCE, generic over BOTH the adapter's raw wire-message type (`Raw`) and
the route's decoded type (`Req`):

```go
package middleware

// RequireScopes is EXTRACTION-ONLY — it does NOT decide pass/fail
// against the route's declared requirements itself (no
// []route.SecurityRequirement parameter at all). See "L4" in "Known
// limitations" below for why: the ADAPTER runs every attached security
// Fn, merges their returned grants, and calls CheckScopes ONCE — a Fn
// that independently checked route.Satisfied against only its OWN
// grants would be WRONG for routes requiring MULTIPLE schemes ANDed
// together (confirmed as an actual bug in an earlier version of this
// sketch, fixed via this split).
func RequireScopes[Raw, Req any](
    schemeName string,
    scheme route.SecurityScheme,
    scopes []string,
    codec *codex.Codec[string],
    extract func(ctx context.Context, raw Raw, req *Req) (map[string][]string, error),
) Middleware {
    return Middleware{
        Name:      "require-scopes:" + schemeName,
        Satisfies: []string{schemeName},
        Security:  &SecurityDeclaration{SchemeName: schemeName, Scheme: scheme, Scopes: scopes, Codec: codec},
        Fn: func(ctx context.Context, raw Raw, req *Req) (map[string][]string, error) {
            return extract(ctx, raw, req)
        },
    }
}

// CheckScopes is the shared route.Satisfied check + typed error wrap —
// called ONCE by the ADAPTER (never by an individual Fn — see "L4"
// below) after merging every attached security Fn's grants into one map.
func CheckScopes(reqs []route.SecurityRequirement, granted map[string][]string) error {
    if !route.Satisfied(reqs, granted) {
        return UnsatisfiedScopesError{Requirements: reqs, Granted: granted}
    }
    return nil
}
```

Each ADAPTER's own `RequireScopes` becomes a ONE-LINE wrapper, existing
ONLY to pin `Raw`'s concrete type and for call-site readability
(`nethttp.RequireScopes` reads better than spelling out
`middleware.RequireScopes[*http.Request, Req]` everywhere):

```go
// adapters/nethttp
func RequireScopes[Req any](schemeName string, scheme route.SecurityScheme, scopes []string, codec *codex.Codec[string], extract func(ctx context.Context, r *http.Request, req *Req) (map[string][]string, error)) middleware.Middleware {
    return middleware.RequireScopes[*http.Request, Req](schemeName, scheme, scopes, codec, extract)
}
// adapters/mqtt5 / adapters/mqtt — SAME one-line pattern, Raw =
// *pahomqtt5.Publish / pahomqtt.Message respectively.
```

**Two of the originally-assumed 6+ implementations turn out to be
UNNECESSARY, confirmed via code:**

- **`chi` needs NO `RequireScopes` of its own at all** — its `Fn` shape
  uses the IDENTICAL `Raw` type (`*http.Request`) as `nethttp`'s.
  `chi.Register` accepts a `nethttp.RequireScopes(...)`-built
  `middleware.Middleware` DIRECTLY. This eliminates one implementation
  entirely, not merely shrinks it.
- **`zeromq` needs none either**, for the SAME reason — confirmed via
  code (`adapters/zeromq/adapter.go` has NO `SecurityFunc`/security
  concept of its own today) — it binds to the SAME
  `events.ChannelHandle`/`reqreply.RouteHandle` `mqtt`/`mqtt5` use (see
  "Coverage across every API/port boundary" above), so
  `mqtt5.RequireScopes`/`mqtt.RequireScopes`-built middleware attaches
  directly wherever `zeromq` binds to those SAME declarations.

`ports` (decorator-shaped — `next` continuation instead of a bare error
return) genuinely CAN'T share the exact same generic core, but its own
`RequireScopes[T]`/`RequireScopesWrite[T]` (see "Ports get the same
treatment" above) now call the SHARED `middleware.CheckScopes`
internally instead of inlining `route.Satisfied` + error construction
themselves — removing the one remaining duplicated slice of logic there
too.

**Net result:** "6+ near-duplicate implementations" becomes 1 shared
generic core (`middleware.RequireScopes[Raw,Req]` + `CheckScopes`) + 3
one-line named wrappers (`nethttp`/`mqtt5`/`mqtt`) + 2 ports-specific
Read/Write variants reusing `CheckScopes`. `chi`/`zeromq` need ZERO of
their own. The actual extract→check→wrap LOGIC now exists in exactly ONE
place, eliminating the adapter-to-adapter drift risk this finding
originally identified.

### L3 — Conflict detection, generalized to every declaration pair

**Status:** RESOLVED (this round) — generalized into ONE symmetric
mechanism covering EVERY pair of declarations for the same name,
regardless of origin, not a special case for "manual vs. middleware"
alone.

**Original problem:** `ConflictingSecurityDeclarationError` only covered
"manual `RouteMeta.Security` declaration vs. middleware-derived
`Security`." TWO DIFFERENT middlewares attached to the SAME route, both
contributing a `RequestParams` entry for the SAME header name, or both
declaring `Security` for the SAME scheme with DIFFERENT scopes, had NO
detection at all.

**Resolution — one shared declaration registry per KIND, inside
`routeBuilder`**, tracking WHO contributed each name (a manual
`RouteOpt` call, or a specific middleware's `Name`), checked
SYMMETRICALLY regardless of source — unifying what were two
conceptually separate checks into one:

```go
// routeBuilder (internal) — tracks the CONTRIBUTOR of each declaration,
// not just the declaration itself, so any TWO sources for the same name
// can be compared, not only "manual vs. the one middleware-derived slot".
securityContributedBy    map[string]string // scheme name → "manual" or a middleware's Name
requestParamContributedBy map[string]string // param name → contributor label
responseParamContributedBy map[string]string
```

**Conflict rule — confirmed via discussion**: only DIFFERING
declarations for the SAME name are conflicts; IDENTICAL redundant
declarations are ALLOWED silently — covers the legitimate "shared base +
an override that happens to redeclare the same thing" composition
pattern without erroring.

**Comparison rules** (kept intentionally pragmatic — no deep-equality of
codec function values, which aren't meaningfully comparable in Go
anyway):

- **Security**: conflict if the scheme TYPE (`Scheme.Type`/`Scheme.Scheme`
  — e.g. `"http"`/`"bearer"` vs. `"apiKey"`) differs, OR the SCOPES set
  differs (order-independent set comparison). A codec-presence mismatch
  (one nil, one non-nil) also counts as a conflict; two non-nil codecs
  are assumed compatible.
- **`RequestParams`/`ResponseParams`**: conflict if the SAME name is
  declared with a DIFFERENT concrete param KIND (e.g. `HeaderParam` vs.
  `CookieParam` for the same name) OR a DIFFERENT `Required` value. Same
  pragmatic codec-presence rule as security.

**Extended/new error types:**

- `ConflictingSecurityDeclarationError{Route, Scheme string, FirstSource,
  SecondSource string, FirstScopes, SecondScopes []string}` — EXTENDED
  from its original manual-only shape to record BOTH contributors
  symmetrically — works identically for manual-vs-middleware AND
  middleware-vs-middleware.
- NEW `ConflictingParamContributionError{Route, ParamName, FirstSource,
  SecondSource string}` — the SAME symmetric shape, for
  `RequestParams`/`ResponseParams` conflicts.

Both fire INSIDE `.Register(builder)` (via `rest.WithMiddleware`'s
`applyRoute`) — the SAME construction-time-error philosophy as
everything else in this doc, consistent with L1's resolution
(declaration time IS registration time IS validation time — see "L1"
above).

#### Conflict example — L3's mechanism actually firing (see "L10" below)

Two middlewares, both mistakenly declaring `Security` for the SAME
scheme name (`"oauth2"`) but with DIFFERENT scopes — e.g. a proxy-groups
middleware and a second, redundantly-added audit middleware someone
copy-pasted and half-edited:

```go
var scopesFromProxy = nethttp.RequireScopes[GetProfileReq](
    "oauth2",
    route.OAuth2Scheme(route.OAuthFlows{ /* ... */ }),
    []string{"profile:read"},
    nil,
    func(ctx context.Context, _ *http.Request, req *GetProfileReq) (map[string][]string, error) {
        groups, _ := ctx.Value(groupsKey{}).([]string)
        return map[string][]string{"oauth2": groups}, nil
    },
)

// Copy-pasted from scopesFromProxy above, then "fixed" to also grant
// profile:admin — but the ORIGINAL scopesFromProxy above was never
// removed, so BOTH are now attached to the same route.
var scopesFromAudit = nethttp.RequireScopes[GetProfileReq](
    "oauth2", // ← SAME scheme name as scopesFromProxy
    route.OAuth2Scheme(route.OAuthFlows{ /* ... */ }),
    []string{"profile:read", "profile:admin"}, // ← DIFFERENT scopes
    nil,
    func(ctx context.Context, _ *http.Request, req *GetProfileReq) (map[string][]string, error) {
        groups, _ := ctx.Value(groupsKey{}).([]string)
        return map[string][]string{"oauth2": groups}, nil
    },
)

handle, err := rest.NewRoute[GetProfileReq, ProfileResp]("GET", "/profile",
    reqCodec, respCodec,
    rest.RouteMeta{OperationID: "getProfile"},
    rest.WithMiddleware(scopesFromProxy),
    rest.WithMiddleware(scopesFromAudit), // ← conflicts with scopesFromProxy above
).Register(builder)
if err != nil {
    log.Fatal(err)
    // err is exactly:
    // ConflictingSecurityDeclarationError{
    //     Route:        "GET /profile",
    //     Scheme:       "oauth2",
    //     FirstSource:  "scopesFromProxy",  // Middleware.Name of the FIRST attached
    //     SecondSource: "scopesFromAudit",  // Middleware.Name of the SECOND attached
    //     FirstScopes:  []string{"profile:read"},
    //     SecondScopes: []string{"profile:read", "profile:admin"},
    // }
    // .Error() renders as:
    // `route "GET /profile": conflicting security declaration for scheme
    // "oauth2": "scopesFromProxy" declares scopes [profile:read], but
    // "scopesFromAudit" declares scopes [profile:read profile:admin]`
}
```

Contrast with the ALLOWED case — the SAME middleware value (or two
independently-constructed ones with IDENTICAL scheme/scopes) attached
twice, e.g. via a shared base builder plus a per-route override that
happens to redeclare the same thing:

```go
rest.WithMiddleware(scopesFromProxy),
rest.WithMiddleware(scopesFromProxy), // redundant, but IDENTICAL — no error
```

`.Register(builder)` returns `nil` here — `securityContributedBy["oauth2"]`
already records `"scopesFromProxy"` as the contributor; the second
attachment compares scheme type and scopes, finds them IDENTICAL to the
first, and is a silent no-op (L3's "allow identical, reject differing"
rule). The EXACT same firing/allowed contrast applies to
`ConflictingParamContributionError` for `RequestParams`/`ResponseParams`
— same registry mechanism, same rule, just keyed by param name instead
of scheme name.

### L4 — Multiple security `Fn`s: a real bug, not just an under-specified order

**Status:** RESOLVED (this round) — this turned out to be an ACTUAL BUG
in the design, confirmed by tracing it precisely, not merely an
ambiguity. Fixed by separating AUTHENTICATION (per-`Fn`, extraction) from
AUTHORIZATION (once, adapter-side, the final judgment).

**The bug, confirmed via tracing:** `route.SecurityRequirement` =
`map[string][]string` (AND within one map); `[]SecurityRequirement` = OR
across elements (confirmed via `route/route.go`'s own doc comment). The
ORIGINAL `middleware.RequireScopes[Raw,Req]` design had EACH attached
security `Fn` independently call `CheckScopes(effectiveReqs,
ownGrantsOnly)` — using ONLY that ONE `Fn`'s OWN extracted grants. If a
route requires `{bearerAuth AND apiKey}` (one AND-requirement) and TWO
SEPARATE `RequireScopes`-built `Fn`s are attached (one per scheme),
NEITHER `Fn`'s own grants alone satisfy the AND-both requirement — BOTH
would INCORRECTLY fail even when the caller correctly satisfies both
schemes COMBINED. Confirmed real, not hypothetical — this is exactly the
kind of thing "review it critically" was meant to catch before
implementation.

**Resolution — separate the two concerns the doc's own Motivation
section already NAMES but didn't fully carry through the mechanism
until now:**

1. **Security `Fn` signature changes**: `func(ctx, raw Raw, req *Req)
   (map[string][]string, error)` — DROPS the `effectiveReqs
   []route.SecurityRequirement` parameter entirely. Each `Fn`'s ONLY job
   is "extract and verify THIS credential, return what it grants" — a
   PURE authentication step. A `Fn`'s OWN extraction failure (e.g. an
   invalid JWT signature) still fails FAST, immediately, before any
   merging — identical behavior to today's single-scheme case.
2. **`middleware.RequireScopes[Raw,Req]` simplifies to a trivial
   forward**:
   ```go
   func RequireScopes[Raw, Req any](
       schemeName string, scheme route.SecurityScheme, scopes []string, codec *codex.Codec[string],
       extract func(ctx context.Context, raw Raw, req *Req) (map[string][]string, error),
   ) Middleware {
       return Middleware{
           Name: "require-scopes:" + schemeName, Satisfies: []string{schemeName},
           Security: &SecurityDeclaration{SchemeName: schemeName, Scheme: scheme, Scopes: scopes, Codec: codec},
           Fn: func(ctx context.Context, raw Raw, req *Req) (map[string][]string, error) {
               return extract(ctx, raw, req) // no CheckScopes here anymore — see the adapter, below
           },
       }
   }
   ```
   `middleware.CheckScopes` is NO LONGER called PER-`Fn` at all.
3. **The adapter (not each `Fn`) does ONE final combined check**: after
   running EVERY attached security `Fn` — IN ATTACHMENT ORDER, fail-fast
   on the FIRST `Fn` whose OWN extraction errors — the adapter MERGES
   every successful `Fn`'s returned grants into ONE combined
   `map[string][]string`, then calls
   `middleware.CheckScopes(effectiveReqs, combinedGranted)` EXACTLY ONCE,
   using the SAME `route.Satisfied` OR/AND semantics uniformly across
   however many schemes/`Fn`s are attached. `UnsatisfiedScopesError` is
   now raised by the ADAPTER at this point (see "New structured errors"
   below), not inside any individual `Fn`.
4. **Ports (decorator-shaped) mirror the SAME split** — each attached
   security decorator also returns `(map[string][]string, error)`
   instead of deciding pass/fail itself; grants accumulate through the
   SAME nested-`next` chain (an explicit accumulator threaded through
   `next`, mechanically analogous to REST's adapter-side merge — not
   re-designed line-by-line here); the FINAL check happens once, at the
   point closest to the real `Read`/`Write` call.
5. **Execution order, now fully specified**: attached security `Fn`s run
   in ATTACHMENT ORDER; the FIRST whose OWN extraction fails
   short-circuits immediately (an invalid/malformed credential is an
   unconditional hard failure, regardless of OR/AND elsewhere
   satisfiable); if ALL succeed, their grants merge (a later `Fn`'s
   entry for the SAME scheme name overwrites an earlier one — an edge
   case only relevant if two `Fn`s both claim the SAME scheme, itself an
   unusual/likely-misconfigured setup) and the ONE final
   `route.Satisfied` check runs.

### L5 — MCP coverage: Resources and Prompts

**Status:** RESOLVED (this round) — simpler than expected: MCP needs NO
decorator/wrapping mechanism at all, just a change to WHERE the
`Observer` value is resolved from.

**Original problem:** confirmed via code (`adapters/mcpgo/adapter.go`)
— the "Coverage across every API/port boundary" section only addressed
`mcpgo.ToolHandler`. `api/mcp`/`adapters/mcpgo` ALSO has
`ResourceHandler`/`PromptHandler`, each with their OWN
`RecordRequest("resource"/"prompt", ...)` Observer call site — a real,
concrete omission, not a hypothetical one; the doc's coverage claim was
OVERSTATED.

**Confirmed via code: all three already share the IDENTICAL pattern,
verbatim** — `ToolHandler`, `ResourceHandler`, `PromptHandler` each do:

```go
obs := opts.Observer
if obs == nil {
    obs = stats.ObserverFromContext(ctx)
}
start := time.Now()
if to, ok := obs.(stats.TraceObserver); ok {
    ctx = to.StartSpan(ctx, "mcp.<kind>", name)
    defer func() { to.EndSpan(ctx, err) }()
}
// ... call fn, encode, on EVERY path:
obs.RecordRequest("<kind>", name, statusCode, time.Since(start))
```

— only the `<kind>` string (`"tool"`/`"resource"`/`"prompt"`) and the
identifying `name` differ. MCP's observability logic is ALREADY fully
written, uniform, and correct across all three surfaces — nothing was
functionally missing.

**Resolution — no decorator needed, just a resolution-source swap:**
since the observability LOGIC already lives inline in each of
`ToolHandler`/`ResourceHandler`/`PromptHandler` (unlike REST, which
needed a NEW `stats.Diagnostic` ctx-ferry for decode-intrinsic events
with no prior home), MCP needs ONLY a change to WHERE `obs` comes from —
not a new wrapping mechanism:

```go
// mcpgo.ObservabilityMiddleware carries a raw stats.Observer value
// INSIDE a middleware.Middleware wrapper — Fn IS the Observer itself,
// not a closure around it. No decorator/wrapping logic is needed,
// because ToolHandler/ResourceHandler/PromptHandler already CONTAIN the
// full observability logic inline; only the SOURCE of obs changes.
func ObservabilityMiddleware(obs stats.Observer) middleware.Middleware {
    return middleware.Middleware{Name: "observability", Fn: obs}
}
```

`ToolHandler`/`ResourceHandler`/`PromptHandler` each gain a variadic
`mws ...middleware.Middleware` parameter (replacing `Options.Observer`,
consistent with its removal everywhere else in this doc); internally,
the existing resolution becomes:

```go
var obs stats.Observer
for _, mw := range mws {
    if o, ok := mw.Fn.(stats.Observer); ok {
        obs = o
        break
    }
}
if obs == nil {
    obs = stats.ObserverFromContext(ctx)
}
```

EVERYTHING ELSE (the `TraceObserver` span, the `RecordRequest` calls, the
`<kind>`/`name` strings) is COMPLETELY UNCHANGED — a one-line resolution
swap applied IDENTICALLY in all three functions, not a redesign.
`RegisterTool`/`RegisterResource`/`RegisterPrompt` forward `mws` through
unchanged. The coverage claim now GENUINELY covers all three MCP
surfaces, not just tools.

### L6 — Long-lived SSE/WebSocket connections

**Status:** RESOLVED (this round) — narrower gap than assumed: SSE was
already fully covered by an EXISTING mechanism; WebSocket needed one
export change plus a FOURTH `Fn` shape, declared the SAME way as every
other boundary in this doc (route/channel declares, adapter invokes).

**Original problem:** SSE/WebSocket's INITIAL upgrade request goes
through the SAME `nethttp.Handler` pipeline as any REST route, so the
HTTP-shaped middleware variant correctly covers the HANDSHAKE. But
NEITHER the design NOR this doc addressed continuous/re-authorization
concerns for the LONG-LIVED session AFTER upgrade (revoking a session
mid-connection, re-checking authorization per outgoing frame).

**Confirmed via code — the gap is much narrower than assumed:**

- **SSE's revocation concern is ALREADY FULLY SOLVED**:
  `stream.BroadcastHub[T].Unsubscribe(s Stream[T])` (`stream/broadcast.go`)
  is ALREADY public — an SSE subscriber can be revoked mid-connection
  TODAY, zero new design needed.
- **WebSocket's `Hub` has the IDENTICAL mechanism, just unexported**:
  `websocket.Hub.unregister(sess)` (`adapters/websocket/hub.go`) already
  force-closes a session's socket/writer queue — the `*Hub` value is
  ALREADY held by the application (constructed via `NewHub`, passed to
  the adapter constructor), just with no public method to call.
- **A natural per-session hook point ALREADY EXISTS**:
  `wsBroadcastAdapter.Activate`'s send loop already iterates every
  session and fetches `hub.SessionInfo(s)` (the upgrade-time vars)
  RIGHT BEFORE `MergeOutbound`/encode/send — exactly where a re-check
  belongs, with the data it needs already in hand.

**Resolution — reuses the SAME "route/channel declares, adapter
invokes" split this whole doc uses everywhere else** (raised explicitly
during design review): WebSocket sockets are ALREADY declared via
`events.Channel`/`ChannelHandle` (`RegisterSocket` builds atop it,
"Subscribe=In, Publish=Out" — see "Coverage across every API/port
boundary" above) — so they get NO separate declaration mechanism of
their own; they reuse `events.WithMiddleware` (already designed for
Phase 2+ events/reqreply) exactly as-is:

1. **Export `Hub.Revoke`** — a thin public wrapper around the existing
   `unregister`, the underlying mechanism a middleware's `Fn` (or any
   app code) calls to force-disconnect:
   ```go
   func (h *Hub) Revoke(sess ports.Session) bool { return h.unregister(sess) }
   ```
2. **A FOURTH `Fn` shape — "session-shaped"** — joining the three
   already established (HTTP/message/decorator):
   `func(ctx context.Context, sess ports.Session, info map[string]string) error`
   — declared via `events.WithMiddleware(mw)` on the SAME
   `events.Channel`/`ChannelHandle` a WebSocket route is already built
   from, carried forward on `ChannelHandle.Middlewares` EXACTLY like
   every other boundary — no new declaration mechanism, no bespoke
   `Options` field on the adapter for this at all.
3. **The ADAPTER (not `Options`) retrieves and invokes it**:
   `websocket.BroadcastSocketAdapter`/`DuplexSocketAdapter` read
   `handle.Middlewares`, filter for session-shaped `Fn`s, and invoke
   them INSIDE their OWN existing per-session loop — the EXACT point
   `SessionInfo` is already fetched for `MergeOutbound`. A non-nil error
   SKIPS that session for THIS frame (reported the SAME way an existing
   encode/merge failure already is — `SocketError`, `OnError`,
   `RecordPublish` success=false); combine with `hub.Revoke` INSIDE the
   `Fn` for a permanent removal instead of a per-frame skip. Mirrors
   EXACTLY how `nethttp.Register` retrieves `handle.Middlewares` and
   invokes security `Fn`s at the right point in ITS OWN pipeline — same
   pattern, fourth shape.
4. **Initial upgrade handshake stays covered by the HTTP-shaped
   variant, unchanged** — a WebSocket route can combine BOTH shapes on
   the SAME declaration: HTTP-shaped for the upgrade handshake,
   session-shaped for ongoing re-authorization, both attached via their
   respective `WithMiddleware` calls, both carried on the SAME handle.

### L7 — Go-generics ergonomics, EMPIRICALLY VALIDATED

**Status:** RESOLVED (this round) — via an ACTUAL throwaway Go
prototype (built, compiled, run, and deleted this round — not just
reasoned about), settling every open question with real compiler
behavior instead of assumption.

**Original problem:** six rounds of prose-only design had never been
checked against `go build`/`go vet`. Open question: can
`nethttp.RequireScopes[Req](...)` actually infer `Req` from a
concretely-typed `extract` closure argument, or does every caller need
the explicit type parameter shown in this doc's examples?

**Confirmed via a real, throwaway prototype** (a standalone Go module in
`/tmp`, implementing `middleware.RequireScopes[Raw,Req]`, `middleware.CheckScopes`,
a one-line `nethttp.RequireScopes[Req]` wrapper, and a minimal
type-asserting `Register[Req]` stand-in — mirroring this doc's design
exactly — then deleted after recording results):

1. **Type inference WORKS with ZERO explicit type parameters.**
   `nethttp.RequireScopes("oauth2", scheme, scopes, nil, func(ctx
   context.Context, r *http.Request, req *ProfileReq) (...) {...})`
   compiled and ran successfully WITHOUT `[ProfileReq]` — Go correctly
   inferred `Req=ProfileReq` from the closure argument's own type, both
   INSIDE a function body AND at PACKAGE-LEVEL `var` scope (the exact
   pattern this doc's own worked examples use). **This doc's examples
   showing an explicit type parameter were MORE VERBOSE than strictly
   necessary** — `[ProfileReq]` remains valid (confirmed working too,
   as an OPTIONAL clarity aid a caller can still choose), but is NOT
   mandatory. Real call sites will typically be terser than every
   example in this doc shows.
2. **The `MiddlewareShapeError`/type-assertion safety net (L1) WORKS
   CORRECTLY at runtime** for a GENERIC function type stored behind
   `Fn any` — confirmed by deliberately attaching a middleware built for
   the WRONG `Req` type (`AdminReq` instead of `ProfileReq`) to
   `Register[ProfileReq]`: the type assertion correctly failed and
   produced the expected error, with ZERO false positives/negatives
   across 5 distinct test cases (correct-type success, explicit-type-
   param success, wrong-type rejection, and `CheckScopes`'s AND-semantics
   check).
3. **No surprising friction found** — no generic-type-inference
   limitation, no issue with `*Req` pointer types specifically, no
   problem with storing/reassembling a generic function type through an
   `any` field. The design's core mechanics compile and behave exactly
   as every prior round assumed.

**Correction applied**: this doc's worked examples (e.g. "Security
worked example", "How declaring a route with middleware looks") remain
VALID as written (explicit type parameters are not WRONG), but a note is
warranted that they show the MORE VERBOSE, not the ONLY, calling
convention — real callers can typically omit the type parameter
entirely and let inference handle it.

### L8 — `ValidateRoute` (formerly `ValidateMiddleware`): widened to close the completeness gap

**Status:** RESOLVED (this round) — widened and renamed to genuinely
match `.Register(builder)`'s validation, not a subset of it.

**Original problem:** `ValidateMiddleware[Req any](mws
...middleware.Middleware) error` (L1's original resolution) took ONLY
`mws` — it had NO visibility into a route's MANUALLY-declared
`RouteMeta.Security`/params, declared via OTHER `RouteOpt`s passed to
`NewRoute` entirely. It could catch middleware-vs-middleware conflicts
(L3's second half) and `Fn`-shape mismatches (L1) — but NOT the
manual-vs-middleware conflict case L3 introduced first. The doc's claim
that it "returns the SAME errors a real `.Register(builder)` call
would surface" was OVERSTATED — a strict subset.

**Resolution — widen the scope to the FULL `opts` list, rename for
accuracy:**

```go
// rest.ValidateRoute type-checks an ENTIRE route declaration —
// EVERY RouteOpt a caller would pass to NewRoute, not just
// middleware.Middleware values — WITHOUT requiring a live *Builder or a
// call to .Register. Builds a SCRATCH, throwaway routeBuilder (never
// touching a real shared *Builder), applies EVERY opt via the SAME
// applyRoute mechanism .Register(builder) already uses (covers
// WithSecurityScheme, NewRequiredHeaderParam, WithMiddleware, and any
// other RouteOpt uniformly), then runs the IDENTICAL validation logic
// .Register(builder) runs — genuinely the SAME checks, not a subset,
// because it is literally the SAME code path minus the final "append
// to the real Builder" step.
//
// For domain/contract packages that declare routes independently of
// their eventual runtime wiring (see Motivation's domain-package
// pattern) — callers pass the EXACT SAME opts list they'd give
// NewRoute, with ZERO separate declaration to keep in sync.
func ValidateRoute[Req, Resp any](meta RouteMeta, opts ...RouteOpt) error
```

Usage — catches EVERYTHING `.Register(builder)` would, including the
manual-vs-middleware conflict L8 originally found missing:

```go
if err := rest.ValidateRoute[GetProfileReq, ProfileResp](
    rest.RouteMeta{OperationID: "getProfile"},
    rest.WithMiddleware(scopesFromProxy),
); err != nil {
    // catches MiddlewareShapeError, ParamContributionShapeError,
    // ConflictingSecurityDeclarationError, ConflictingParamContributionError
    // — the FULL set, since meta.Security participates in the SAME
    // scratch-builder validation as any middleware-derived declaration.
}
```

Mirrored as `events.ValidateChannel[T]`/`reqreply.ValidateRoute[Req]`
for the SAME reason, on the SAME `.Register(builder)`-based boundaries
— the SAME rename/widen applies uniformly. Still does NOT apply to
MCP/ports (single attachment point, no separate declare-vs-wire split
possible there at all, per L1's original resolution — unchanged).

### L9 — L4's authentication/authorization split was never checked against the client side

**Status:** RESOLVED.

**Problem:** L4's fix (extraction-only `Fn`, adapter merges grants and
checks once) was designed and verified ENTIRELY in terms of the
SERVER-side verification `Fn` (`Register`/`Handler`). `Call`'s `Fn`
shape (the former `CredentialFunc`) has a FUNDAMENTALLY DIFFERENT
contract — it PRODUCES an outgoing credential/header, it does not
EXTRACT grants from an incoming request. L4's analysis never revisited
this side at all, and the doc's `Call` description never said what
happens when MULTIPLE credential-providing middlewares are attached.

**Why it matters:** if MULTIPLE credential-providing middlewares are
attached to ONE `Call` (e.g. one supplying a bearer token, another
supplying an API key for a DIFFERENT declared scheme), it is UNCLEAR
whether their outgoing headers merge safely (additive, like the
security `Fn`'s grants do) or could COLLIDE (two middlewares both
trying to set the SAME header, e.g. `Authorization`) — a distinct
question from anything L4 resolved, with no answer in this doc today.

**Resolution — same principle as L4, adapted to the client's different
contract:** the key difference from L4 is that the CLIENT never judges
its own authorization — it just attaches whatever credentials it has
for whatever schemes the route declares, and the SERVER judges (via the
SAME `route.Satisfied`/`CheckScopes` mechanism L4 already covers
server-side). So there is no final "check" step to add on the client
side, only a MERGE step: `Call` runs EVERY attached credential-providing
`Fn` (in attachment order), and merges their returned `http.Header`
values into ONE combined header set attached to the outgoing request.

Collision handling mirrors L3's precedent exactly: identical redundant
values are allowed silently; only DIFFERING values for the SAME header
key are a conflict.

```go
// adapters/nethttp — Call's credential-middleware handling. Runs EVERY
// attached credential-providing Fn in attachment order, merging their
// returned headers into ONE combined set attached to the outgoing
// request. No final authorization check is needed here (unlike L4's
// server-side CheckScopes) — the client never judges its own
// authorization, only the server does.
combined := make(http.Header)
setBy := make(map[string]string) // header key -> middleware Name that set it
for _, mw := range mws {
    fn, ok := mw.Fn.(func(context.Context, []route.SecurityRequirement) (http.Header, error))
    if !ok {
        continue // not a credential-providing Fn (e.g. an observability middleware) — skip
    }
    h, err := fn(ctx, effectiveReqs)
    if err != nil {
        return zero, err // fail-fast — a credential provider's own failure aborts the call
    }
    for key, vals := range h {
        if prior, exists := setBy[key]; exists && prior != mw.Name && !equalValues(combined[key], vals) {
            return zero, ConflictingCredentialHeaderError{
                Header: key, FirstSource: prior, SecondSource: mw.Name,
            }
        }
        combined[key] = vals
        setBy[key] = mw.Name
    }
}
for key, vals := range combined {
    httpReq.Header[key] = vals
}
```

New structured error: `ConflictingCredentialHeaderError{Header,
FirstSource, SecondSource string}` — mirrors
`ConflictingSecurityDeclarationError`/`ConflictingParamContributionError`'s
exact shape (L3's precedent). Two DIFFERENT credential-providing
middlewares producing DIFFERENT values for the SAME header key is a
caller misconfiguration, surfaced loudly rather than silently
overwritten (last-write-wins was explicitly rejected — a silently
dropped credential is exactly the kind of bug this whole design exists
to prevent). IDENTICAL values from two middlewares for the SAME key are
allowed silently — same "allow identical, reject differing" rule L3
established for security/param declarations.

### L10 — No worked example shows a conflict actually firing

**Status:** RESOLVED.

**Problem:** L3's `ConflictingSecurityDeclarationError`/
`ConflictingParamContributionError` mechanism is fully designed (the
registry, the comparison rules, the error shapes) but this doc never
shows it FAILING in a concrete, worked example — every example shown
demonstrates the HAPPY path only.

**Why it matters:** a reader implementing this design has no concrete
reference to verify their own mental model of WHEN exactly a conflict
fires against — increasing the risk of a subtly-wrong implementation
that passes the (also hypothetical, never-written) test suite for the
happy path but gets the conflict-detection edge cases wrong.

**Resolution:** added a "Conflict example — L3's mechanism actually
firing" subsection directly under L3's resolution above, reusing the
SAME `scopesFromProxy`/`GetProfileReq` declaration style already
established in "How declaring a route with middleware looks." It shows:
(1) two middlewares declaring `Security` for the SAME scheme
(`"oauth2"`) with DIFFERENT scopes, and the EXACT
`ConflictingSecurityDeclarationError` value (and rendered `.Error()`
string) `.Register(builder)` returns; (2) the contrasting ALLOWED case —
the same scheme/scopes declared twice (e.g. via a shared base + a
redundant per-route override) — returning `nil`, concretely
demonstrating L3's "allow identical, reject differing" rule instead of
only asserting it in prose. No new mechanism was needed — this closes
the gap purely by making the existing, already-correct mechanism
verifiable against a worked reference.

### L11 — Unreconciled overlap with `dynamic-port-rebinding.md`

**Status:** RESOLVED.

**Problem:** confirmed via code — `docs/roadmap/dynamic-port-rebinding.md`
explicitly motivates itself with "credential rollover... without
process restart" for `ports` — DIRECTLY overlapping with what security
middleware in THIS doc is designed to solve. The two roadmap docs have
NEVER cross-referenced each other despite this motivating overlap.

**Why it matters:** since `ports`' middleware attaches PER-CALL (not
baked into an immutable handle — see "Ports get the same treatment"
above), it can ALREADY be swapped freely between calls with ZERO new
mechanism — `dynamic-port-rebinding.md`'s hot-swap concept may be
PARTIALLY REDUNDANT for the "rotate a port's security middleware"
use case specifically (though still needed for swapping the underlying
TRANSPORT adapter itself, a different concern). Meanwhile REST/events/
reqreply bake middleware into an IMMUTABLE `RouteHandle` at
`.Register(builder)` time — credential rollover for THOSE boundaries
has NO hot-swap story at all today, a genuine gap neither doc currently
owns.

**Resolution — lightweight cross-reference, no new mechanism (confirmed
with the user):**

- `ports`' credential-rollover story is ALREADY solved, today, by this
  doc's per-call middleware attachment (see "Ports get the same
  treatment" above) — a `ports.RequireScopes[T]`/credential-providing
  middleware value can be swapped between calls with ZERO new
  mechanism, since it is never baked into an immutable handle the way
  `RouteHandle` is. `dynamic-port-rebinding.md` remains necessary ONLY
  for swapping the underlying TRANSPORT ADAPTER itself (broker
  failover, endpoint rotation, phased migration) — a genuinely
  different concern from credential rotation, and `dynamic-port-
  rebinding.md` has been updated to say so explicitly (see its
  Motivation section).
- REST/events/reqreply's IMMUTABLE `RouteHandle.Middlewares` (frozen at
  `.Register(builder)` time) has NO hot-swap story today — this is an
  explicitly acknowledged, OUT-OF-SCOPE gap for BOTH docs, left for a
  future round if ever prioritized (not designed now — no user need for
  it currently, since REST/events/reqreply middleware rotation, if
  ever needed, would look like `dynamic-port-rebinding.md`'s own
  `Rebind`-style mechanism applied to a route/channel's handle, not a
  `ports`-specific concern).

No new mechanism was designed or implemented — this closes the gap
purely via cross-referencing and explicit scope acknowledgment between
the two roadmap docs.

### L12 — MCP's observability resolution silently drops all but the first matching middleware

**Status:** RESOLVED.

**Problem:** L5's resolution has `ToolHandler`/`ResourceHandler`/
`PromptHandler` scan `mws` for a `stats.Observer`-typed `Fn` and take
the FIRST match via `break`:

```go
var obs stats.Observer
for _, mw := range mws {
    if o, ok := mw.Fn.(stats.Observer); ok {
        obs = o
        break // ← the SECOND stats.Observer-typed middleware is silently ignored
    }
}
```

If a caller attaches TWO `mcpgo.ObservabilityMiddleware` values (e.g.
one wrapping a metrics collector, one wrapping a tracer, a very
plausible real setup mirroring how `chi`/`nethttp` freely stack
multiple general-purpose middlewares today), only the FIRST is ever
consulted — the second is silently dropped, with no error, no warning,
no merge.

**Why it matters:** this is the EXACT class of bug this entire design
exists to eliminate — `Options.SecurityFunc`'s original "declare
`Security` in the spec, forget to also set the enforcing function, no
error" silent-no-op bug that motivated this whole doc in the first
place (see "Motivation"). Every OTHER boundary in this doc composes
multiple attached middlewares of the same kind correctly: HTTP
general-purpose middlewares nest (each wraps the next, none are
dropped); `ports.File`'s decorator shape nests the same way; security
`Fn`s MERGE their grants (L4); client-side credential `Fn`s MERGE their
headers (L9). MCP's shortcut ("no decorator needed, just swap the
resolution source") is the ONE place in the entire design where
attaching two of the same kind of middleware silently loses one of
them. Confirmed via `grep` that `stats` has no `MultiObserver`/fan-out
helper today that could paper over this by pre-composing observers
before attaching a single one.

**Resolution — new `stats.MultiObserver` fan-out composite (confirmed
with the user):** rather than making every observer call site
(`RecordRequest`, `StartSpan`/`EndSpan`, `RecordValidationError`, …)
loop over N observers individually, `stats.MultiObserver` mechanically
combines N `stats.Observer` values into ONE `stats.Observer` value —
mirrors `io.MultiWriter`. The EXISTING "scan `mws`, take the first
`stats.Observer`-typed `Fn`" resolution logic in
`ToolHandler`/`ResourceHandler`/`PromptHandler` stays structurally
UNCHANGED; it just now collects EVERY matching `Fn` and builds a
`MultiObserver` when there's more than one, instead of `break`-ing on
the first:

```go
// stats — NEW, sketched here for a future implementation round.
//
// MultiObserver fans out every Observer-family call to N inner
// observers, mechanically combining them into ONE stats.Observer value
// — mirrors io.MultiWriter. All OPTIONAL sub-interfaces
// (SecurityObserver, TraceObserver, FileObserver, SQLObserver,
// CacheObserver, CredentialCacheObserver) are fanned out via a per-call
// type-assertion guard — identical in spirit to how every adapter
// already consults SecurityObserver today
// (`if so, ok := obs.(stats.SecurityObserver); ok { ... }`) — no inner
// observer is required to implement every optional interface.
type MultiObserver struct {
    Observers []Observer // order = fan-out order; also determines nested span order
}

func (m MultiObserver) RecordValidationError(location, constraintName, field string) {
    for _, o := range m.Observers {
        o.RecordValidationError(location, constraintName, field)
    }
}

func (m MultiObserver) RecordRequest(method, path string, statusCode int, duration time.Duration) {
    for _, o := range m.Observers {
        o.RecordRequest(method, path, statusCode, duration)
    }
}

func (m MultiObserver) RecordSecurityRejection(location, scheme string) {
    for _, o := range m.Observers {
        if so, ok := o.(SecurityObserver); ok {
            so.RecordSecurityRejection(location, scheme)
        }
    }
}
```

`TraceObserver` needs special handling — `StartSpan` returns a NEW
`context.Context` that `EndSpan` later needs, so a naive fan-out would
only see ONE span's context. `MultiObserver.StartSpan` calls EVERY
inner `TraceObserver` in order, threading `ctx` through each (each
tracer's span nests as a child of the previous), and stores the
per-tracer `(TraceObserver, context.Context)` pairs in a private
ctx-carried slice so `EndSpan` can unwind them in REVERSE order,
calling each tracer's OWN `EndSpan` with ITS OWN chained ctx — the
standard nested-span composition pattern:

```go
type multiSpanKey struct{}

func (m MultiObserver) StartSpan(ctx context.Context, operation, name string) context.Context {
    type span struct {
        tracer TraceObserver
        ctx    context.Context
    }
    var spans []span
    for _, o := range m.Observers {
        if to, ok := o.(TraceObserver); ok {
            ctx = to.StartSpan(ctx, operation, name) // nests: child of the previous tracer's span
            spans = append(spans, span{tracer: to, ctx: ctx})
        }
    }
    return context.WithValue(ctx, multiSpanKey{}, spans)
}

func (m MultiObserver) EndSpan(ctx context.Context, err error) {
    spans, _ := ctx.Value(multiSpanKey{}).([]struct {
        tracer TraceObserver
        ctx    context.Context
    })
    for i := len(spans) - 1; i >= 0; i-- { // reverse — innermost span ends first
        spans[i].tracer.EndSpan(spans[i].ctx, err)
    }
}
```

`MultiObserver` itself always satisfies `stats.Observer`, and is always
SAFE to type-assert against `stats.SecurityObserver`/`stats.TraceObserver`
even when `Observers` is empty or none of them implement those optional
interfaces — the fan-out degrades to a no-op loop, never a panic or a
missing-method compile error.

**MCP's resolution logic, updated to build a `MultiObserver` when
N > 1** — this is L12's actual fix, and it is a strict superset of L5's
original one-line swap (the zero- and single-observer cases behave
IDENTICALLY to before; only the N>1 case, previously silently dropped,
is now handled):

```go
// mcpgo — collects EVERY stats.Observer-typed Fn (not just the
// first), combining them into ONE stats.MultiObserver when more than
// one is found. Everything else in ToolHandler/ResourceHandler/
// PromptHandler is UNCHANGED — obs is still a single stats.Observer
// value by the time the existing TraceObserver/RecordRequest logic runs.
var observers []stats.Observer
for _, mw := range mws {
    if o, ok := mw.Fn.(stats.Observer); ok {
        observers = append(observers, o)
    }
}
var obs stats.Observer
switch len(observers) {
case 0:
    obs = stats.ObserverFromContext(ctx)
case 1:
    obs = observers[0]
default:
    obs = stats.MultiObserver{Observers: observers}
}
```

A caller can also pre-build a `stats.MultiObserver` themselves and pass
it as the SOLE observability middleware, whenever they want explicit
control over fan-out order — both paths converge on the same result.

### L13 — `middleware.ContextField[V]`'s contract is incompatible with the L4-final security `Fn` signature

**Status:** RESOLVED.

**Problem:** `ContextField[V].Set(ctx, raw) (context.Context, error)`
returns a NEW, immutable `context.Context` — its own usage example
(see "`middleware.ContextField[V]`" above) shows this called explicitly
"inside the security Middleware's `Fn`" so that "the handler (or a
LATER middleware)" can retrieve it via `Get`. But L4 (written/resolved
LATER in this doc's own timeline, in a different round) finalized the
security `Fn` signature as:

```go
Fn: func(ctx context.Context, raw Raw, req *Req) (map[string][]string, error)
```

— there is NO `context.Context` in the return tuple at all. A `Fn` that
calls `ctx, err := ClaimsField.Set(ctx, ...)` produces a brand-new ctx
value that has **no channel to escape `Fn`'s own stack frame**: it
cannot reach the actual business handler, any LATER-attached security
`Fn` (the adapter's loop re-passes its OWN original `ctx` to each `Fn`,
not a threaded/accumulated one — confirmed via L4's resolution code,
which never mentions ctx threading between `Fn`s), or a general-purpose
`http.Handler`-shaped middleware attached alongside it (Go's
`context.Context` values never propagate UPWARD through a call stack —
only `nethttp.WithResponseHeaders`' pre-allocated-mutable-map trick
escapes this limitation, by mutating a REFERENCED object rather than
replacing an immutable value; `ContextField` as documented does NOT use
that trick — `Set` explicitly returns a new value).

**Why it matters:** this is a genuine, previously-unreconciled
contradiction between two sections of the SAME doc, written at
different points in the design's evolution and never cross-checked
against each other. `ContextField[V]`'s entire stated purpose — "for
data that should NOT force every route's `Req` to carry security-
specific fields" — is precisely the security-`Fn` use case, and that
exact use case is the one its own contract cannot actually satisfy
today. A reader implementing this design from the doc as written would
discover their published `ContextField` value is unreachable from
anywhere outside the `Fn` that set it.

**Resolution — redesigned around a pre-allocated mutable box
(confirmed with the user), same trick `WithResponseHeaders` already
uses:** see the fully rewritten "`middleware.ContextField[V]`" section
above for the complete API. Summary: `EnsureContextFields(ctx)`
pre-allocates a SHARED, mutable box on `ctx` once, at each adapter's
entry point, BEFORE any `Fn` runs; `Set(ctx, raw) error` mutates that
box IN PLACE instead of returning a new ctx; `Get(ctx) (V, bool)` reads
from the SAME box. Because the box is a pointer stored as a ctx value
(not an immutable ctx replacement), EVERY context descended from the
SAME pre-allocation point — any `Fn`'s ctx, the handler's internal ctx,
a general-purpose `http.Handler` middleware's ORIGINAL ctx even AFTER
`next.ServeHTTP` returns — sees the SAME box, so mutations are visible
regardless of WHICH `Fn` shape performed them or in which direction
(downward OR upward) the read happens.

This does NOT reopen L4: the security `Fn` signature (`func(ctx, raw,
req) (map[string][]string, error)`) is completely unchanged — `Set`
simply no longer NEEDS a `context.Context` in its own return value, so
a `Fn` calling `ClaimsField.Set(ctx, raw)` (checking only the single
`error` result, same as any other fallible call inside `Fn`) works
regardless of `Fn`'s own return shape. This also makes `ContextField`
usable UNIFORMLY from every `Fn` shape this doc defines (general-
purpose, security-specific, credential-providing, decorator,
session-shaped) with ZERO shape-specific variation — a genuine
improvement over the original design, which only happened to work (by
accident of Go's downward-only ctx propagation) for the general-
purpose shape.

**New structured error:** `ContextFieldNotPreparedError{}` — `Set`
called before the owning adapter pre-allocated the box (a missing
`EnsureContextFields` call is an adapter-wiring mistake, surfaced
loudly rather than silently degrading to a no-op write).

**Adapter wiring (Phase 2 for implementation, sketched here):** each
adapter's entry point calls `ctx = middleware.EnsureContextFields(ctx)`
ONCE, as the OUTERMOST-most step before composing anything else —
`nethttp.Register` wraps its FULLY composed handler chain (general-
purpose middlewares AND the security-`Fn`-containing `Handler`) in one
more, outermost layer that pre-allocates the box first;
`ports.File.Read`/`Write` call it at the top of the method, before
building the `next` decorator chain; `mcpgo.ToolHandler`/
`ResourceHandler`/`PromptHandler` call it at the top of their closures;
events/reqreply adapters call it identically. Uniform across every
boundary, matching `ContextField`'s own original "not boundary-shape-
specific" claim — now actually TRUE, not just asserted.

### L14 — forge/Registry (Layer 3 pipelines) was never checked against this design at all

**Status:** RESOLVED — spun out into a dedicated roadmap doc.

**Problem:** the doc's "Coverage across every API/port boundary"
section states "Full coverage confirmed for every boundary go-codex
ships" — but `forge.Registry`/pipeline functions (Layer 3 —
`forge.NewFunction`, `Compose`, `Registry.Apply`) are never mentioned
ANYWHERE in this doc. Confirmed via `grep`: zero occurrences of
`forge`/`Registry` outside of unrelated prose (e.g. "registry" used
generically). This is a genuine gap in a claim, not a hypothetical.

**Why it matters:** forge already has its OWN parallel cross-cutting
mechanism — `stats.PipelineObserver.RecordApply`, wired via the
EXPLICIT builder method `Registry.WithObserver` (deliberately WITHOUT
context integration, "explicit builder API by design" per existing
go-codex conventions — pipelines are long-lived registries configured
at startup, not per-call constructs the way routes/channels/tools/ports
are). It is UNCLEAR whether this design's `middleware.Middleware`
mechanism was deliberately judged a poor fit for forge (a defensible
position — `Registry.WithObserver` already solves observability there,
and forge has no analogous "security" concept at all, since a pipeline
function doesn't correspond to an inbound request needing
authentication) or was simply never considered during either of the
first two review passes. Either way, the doc's blanket "every boundary"
claim is not accurate until this is addressed explicitly one way or the
other.

**Resolution — spun out into
[`docs/roadmap/forge-pipeline-middleware.md`](forge-pipeline-middleware.md)
(per the user's explicit choice), rather than decided inline:** the
three candidate directions surfaced during discovery — (a) document
`Registry.WithObserver` as already adequate, closing the gap via
documentation only; (b) actually extend `middleware.Middleware` to
forge via a decorator-shaped `Fn` wrapping `Registry.Apply` (mirrors
`ports.File`); (c) narrow this doc's "every boundary" claim to
explicitly mean Layer 2 only — are ALL carried forward, unresolved,
into the new dedicated doc for a future design pass. This doc's own
"Coverage across every API/port boundary" section has been updated to
state its scope is Layer 2 only, closing the immediate accuracy gap in
the claim without pre-committing to (a), (b), or (c).

## Files to create/modify (Phase 1)

| File | Change |
|---|---|
| `middleware/middleware.go` (new package) | `Middleware` struct (incl. `Security`/`RequestParams`/`ResponseParams`), `SecurityDeclaration`, `ContextField[V]`/`NewContextField`/`Set`/`Get` backed by a pre-allocated mutable box (`EnsureContextFields`, `ContextFieldNotPreparedError` — see "L13" below), `RequireScopes[Raw, Req]` (shared generic core), `CheckScopes` (shared inner check), doc comments |
| `middleware/middleware_test.go` (new) | Construction/shape tests, `ContextField` set/get/no-op-when-absent tests |
| `route/security.go` (or `route.go`) | `Satisfied` function + tests |
| `stats/diagnostics.go` (new) | `Diagnostic`, `WithDiagnostics`, `RecordDiagnostic`, `DiagnosticsFromContext` — the Class B ctx-ferry mechanism |
| `stats/diagnostics_test.go` (new) | Pre-allocate/append/read-back tests, no-op-when-absent tests |
| `stats/multi.go` (new) | `MultiObserver` fan-out composite — `RecordRequest`/`RecordValidationError` unconditional, `SecurityObserver`/`TraceObserver`/etc. via per-call type-assertion guard, `StartSpan`/`EndSpan` nested-span chaining (see "L12" below) |
| `stats/multi_test.go` (new) | Fan-out order tests, nested-span `StartSpan`/`EndSpan` unwind tests, zero/one/N-observer cases |
| `adapters/mcpgo/adapter.go` | `ToolHandler`/`ResourceHandler`/`PromptHandler`'s observer resolution collects EVERY `stats.Observer`-typed `Fn` (not just the first) and builds a `stats.MultiObserver` when more than one is attached (see "L12" below) |
| `api/rest/builder.go` | NEW `WithMiddleware` `RouteOpt` (applies `Security`/`RequestParams`/`ResponseParams` to the `routeBuilder` snapshot, appends to `rb.middlewares`, validates the FULL shape INSIDE `.Register(builder)` — see "L1" below); `routeBuilder` gains `securityContributedBy`/`requestParamContributedBy`/`responseParamContributedBy` registries (see "L3" below); `RouteHandle` gains `Middlewares []middleware.Middleware`; NEW `ConflictingSecurityDeclarationError`/`ConflictingParamContributionError`/`ParamContributionShapeError`; NEW `ValidateRoute[Req, Resp]` standalone helper — validates the FULL `opts` list via a scratch `routeBuilder`, not just middleware (see "L8" below) |
| `api/events/builder.go`, `api/reqreply/route.go` | Mirrored `ValidateChannel[T]`/`ValidateRoute[Req]` standalone helpers, same rationale as `rest`'s |
| `adapters/nethttp/adapter.go` | `Options.SecurityFunc`/`Options.Observer` removed; `Register`/`Handler` combine `handle.Middlewares` + variadic `extraMws`, apply drift-closing validation (manual-declaration path only), apply general-purpose wrapping; `report*Errors` helpers rewired to `stats.RecordDiagnostic`; pre-allocates the `ContextField` box via `middleware.EnsureContextFields` as the OUTERMOST step, before any attached `Fn` runs (see "L13" below) |
| `adapters/nethttp/client.go` | `CallOptions.CredentialFunc` removed; `Call` accepts `...middleware.Middleware`; runs every attached credential-providing `Fn` and merges their headers, failing fast on any `Fn` error and on same-key-differing-value collisions; NEW `ConflictingCredentialHeaderError` (see "L9" below) |
| `adapters/nethttp/errors.go` (or inline) | `MissingSecurityMiddlewareError`, `UnsatisfiedScopesError`, `MiddlewareShapeError` |
| `adapters/nethttp/scopes.go` (new) | `RequireScopes` — ONE-LINE wrapper around `middleware.RequireScopes[*http.Request, Req]` (see "L2" below); `RequireAPIKey` (param-contribution worked example) |
| `adapters/nethttp/observability.go` (new) | `ObservabilityMiddleware` — the ONLY `stats.Observer` call site left in the package |
| `adapters/chi/adapter.go` | Mirror nethttp's changes exactly, including `Options.Observer` removal + `report*Errors` rewiring + `handle.Middlewares` combination. **NO `chi`-specific `RequireScopes`** — reuses `nethttp.RequireScopes` directly (identical `*http.Request` `Raw` type). |
| `examples/adapters-nethttp-security`, `adapters-chi-security`, `adapters-nethttp-client`, `mutable-security-keys`, `stats-observer` | Rewritten call sites — security examples adopt `RequireScopes`; `stats-observer` adopts `ObservabilityMiddleware` in place of `Options.Observer` |
| `docs/features/security.md` | Rewritten `SecurityFunc`/`CredentialFunc` sections to the new `middleware.Middleware`-based API; new "Delegating authentication to an external proxy" section using `RequireScopes` |
| `docs/concepts/middleware.md` (new) | Design rationale — the Core-thesis pure-I/O framing, the type-erasure + adapter-side-assertion pattern, why security schemes are NOT inferred from attached middleware, the `ports.File` proof-of-generality sketch. Mirrors `docs/concepts/observable-layers.md`'s role for the `Observer` pattern. |
| `docs/features/middleware.md` (new) | Practical usage guide — `RequireScopes`, the OAuth2-Proxy-in-front pattern, migration from `Options.SecurityFunc`/`CallOptions.CredentialFunc`, a `ports.File` worked example. Mirrors `docs/features/observer.md`'s role. |
| `docs/concepts/declaring-apis-and-ports.md` | Add a one-line cross-link (its own "See also" list) to the new `docs/concepts/middleware.md` — no duplicated content. |
| `.github/instructions/go-codex.instructions.md` | New `middleware` package row; updated `route`/`adapters/nethttp`/`adapters/chi` rows |
