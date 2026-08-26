# Declarative Middleware — a shared `middleware` package for `route`/`api/*`/`ports`

> **Status:** Design draft — Phase 1 (REST + security) fully speced;
> `ports.File`'s decorator shape sketched and structurally proven (see
> "Ports get the same treatment" below), implementation still Phase 2;
> events/reqreply/MCP mirror staged, not yet speced in detail.
> [← Back to Roadmap](index.md)
>
> Breaking changes are explicitly ACCEPTED for this feature (single
> internal consumer today) — `adapters/nethttp`/`chi`'s `Options.SecurityFunc`/
> `CallOptions.CredentialFunc` fields are REMOVED, not deprecated
> alongside. See "Migration" below.

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

(`Options.Observer`/`stats.Observer` is a SEPARATE, already-clean
cross-cutting mechanism and is NOT part of this thesis — see
"Relationship to Observer" below for why it stays as-is.)

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
and security as the worked first use case — including the shared
scope-matching predicate and the drift-closing validation.

**Phase 2+ (staged for IMPLEMENTATION, `ports.File` DESIGN already
sketched and proven):** `api/events`/`api/reqreply` adopting the SAME
`middleware.Middleware` type for their own `SecurityFunc`-equivalents
(`mqtt`/`mqtt5` adapters); a "value-transform"/decorator flavor of
middleware for `ports` (`ports.File`/`Cache`/`SQL`) operating on the
ALREADY-DECODED typed value rather than the wire boundary. Unlike the
first version of this doc, `ports.File`'s shape is NOT left vague — see
"Ports get the same treatment" below for a concrete, structurally-proven
sketch (the direct test of the Core thesis) — only the actual
IMPLEMENTATION (changing `File[T]`'s real method signatures,
`ports.Cache`/`SQL`'s mechanical extensions) remains Phase 2.

**Explicitly NOT in scope, by design, at any phase:** deriving a route's
declared `Security`/scopes FROM which middleware happens to be attached
(inspection-based inference). `RouteMeta.Security`/`rest.WithSecurityScheme`
remain UNCHANGED — the single, explicit, always-present source of truth
for spec generation. Middleware only changes HOW enforcement is wired at
Register/Call time, never WHAT the spec declares. This was an explicit,
deliberate fork resolved during design — see "Why not infer from
middleware" below.

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
//     security requirements (correct for logging, request-ID injection,
//     rate limiting — concerns that don't need route awareness).
//   - Security-specific: func(ctx context.Context, r *http.Request, reqs
//     []route.SecurityRequirement) error — mirrors today's SecurityFunc
//     EXACTLY, applied INSIDE Handler at the same point SecurityFunc used
//     to run (needs the route's OWN declared reqs, only known there).
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
// middleware.Middleware parameter — general-purpose ones wrap the
// http.Handler outermost-in, in the order given; security-specific ones
// are invoked from inside Handler, at the same point SecurityFunc used to
// run, and are cross-checked against handle.Descriptor.Security (see
// "Drift-closing validation").
func Register[Req, Resp any](
    mux *http.ServeMux,
    handle *rest.RouteHandle[Req, Resp],
    fn HandlerFunc[Req, Resp],
    opts Options, // SecurityFunc field REMOVED
    mws ...middleware.Middleware,
) error // NEW return — was previously void

// BREAKING: CallOptions loses CredentialFunc. Call gains the same
// variadic middleware.Middleware parameter; the concrete Fn shape for a
// client-side credential provider is:
//   func(ctx context.Context, reqs []route.SecurityRequirement) (http.Header, error)
// — IDENTICAL to today's CredentialFunc type, just carried inside a
// Middleware value instead of a bare Options field.
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

### Drift-closing validation

At the top of `Register`/`Call`, before building the handler:

```go
secReqs := handle.Descriptor.Security
if secReqs == nil {
    secReqs = handle.GlobalSecurity
}
for _, req := range secReqs {
    for scheme := range req {
        if !satisfiedByAny(mws, scheme) {
            return MissingSecurityMiddlewareError{Route: handle.Descriptor.Path, Scheme: scheme}
        }
    }
}
```

`MissingSecurityMiddlewareError{Route, Scheme}` (new, `slog.LogValuer`) —
returned immediately, before any request is ever served. This is the
concrete mechanism that makes "declared secure but silently unenforced"
a **construction-time error**, not a runtime footgun.

### Security worked example — `nethttp.RequireScopes`

```go
// adapters/nethttp
//
// RequireScopes builds a security Middleware that checks the caller's
// GRANTED scopes (however obtained — read from context set by an
// upstream net/http middleware translating an OAuth2 Proxy/Keycloak/Envoy
// JWT filter's headers, a locally-verified JWT, anything) against the
// route's OWN declared Security via route.Satisfied — decoupling
// AUTHENTICATION (extract, delegated entirely to the caller-supplied
// extract func) from AUTHORIZATION (the mechanical scope-match, done
// once, here, for every caller instead of every app hand-rolling it).
func RequireScopes(schemeName string, extract func(ctx context.Context, r *http.Request) (map[string][]string, error)) middleware.Middleware {
    return middleware.Middleware{
        Name:      "require-scopes:" + schemeName,
        Satisfies: []string{schemeName},
        Fn: func(ctx context.Context, r *http.Request, reqs []route.SecurityRequirement) error {
            granted, err := extract(ctx, r)
            if err != nil {
                return err
            }
            if !route.Satisfied(reqs, granted) {
                return UnsatisfiedScopesError{Requirements: reqs, Granted: granted}
            }
            return nil
        },
    }
}
```

Usage — the OAuth2-Proxy-in-front topology this design was motivated by:

```go
// A plain net/http middleware — standard idiom, zero go-codex API —
// translating a proxy-injected header into context, BEFORE go-codex ever
// sees the request. This is where AuthN (delegated) lives.
func withProxyGroups(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        groups := strings.Split(r.Header.Get("X-Auth-Request-Groups"), ",")
        ctx := context.WithValue(r.Context(), groupsKey{}, groups)
        next.ServeHTTP(w, r.WithContext(ctx))
    })
}

scopesFromProxy := nethttp.RequireScopes("oauth2", func(ctx context.Context, _ *http.Request) (map[string][]string, error) {
    groups, _ := ctx.Value(groupsKey{}).([]string)
    return map[string][]string{"oauth2": groups}, nil
})

mux := http.NewServeMux()
if err := nethttp.Register(mux, profileHandle, profileFn, nethttp.Options{}, scopesFromProxy); err != nil {
    log.Fatal(err)
}
handler := withProxyGroups(mux) // general net/http wrapping, unchanged idiom
```

`withProxyGroups` needs NO go-codex-specific type at all (plain
`func(http.Handler) http.Handler`) — it is wrapped around the WHOLE mux,
exactly as any net/http middleware always has been; `RequireScopes`
handles the route-aware, declared-scopes half, inside `Register`.

### New structured errors

- `MissingSecurityMiddlewareError{Route, Scheme string}` — `Register`/
  `Call` refuse to wire a route/call whose declared scheme has no
  attached middleware satisfying it.
- `UnsatisfiedScopesError{Requirements []route.SecurityRequirement, Granted map[string][]string}` —
  returned by `RequireScopes`'s `Fn` (and any similar helper) when the
  extracted grants don't satisfy the route's declared requirements; wraps
  into `rest.SecurityError` exactly like any other `SecurityFunc`-shaped
  error does today.
- `MiddlewareShapeError{Name, Expected, Got string}` — a `Middleware.Fn`
  whose concrete type doesn't match what the consuming adapter/role
  expects (e.g. a general-purpose `func(http.Handler) http.Handler` value
  passed where a security-specific closure was required, or vice versa).

Both new error types implement `slog.LogValuer`, matching every other
structured error in this codebase.

## Observer integration

Unchanged: `stats.SecurityObserver.RecordSecurityRejection` still fires
on any middleware-returned error, exactly as it does today for
`SecurityFunc` rejections — `middleware.Middleware`'s `Fn` closure is
invoked from the SAME call site inside `Handler`, just resolved via the
new mechanism instead of a bare `Options.SecurityFunc` field.

## Spec generation impact

None — `RouteMeta.Security`/`rest.WithSecurityScheme`/`SecuritySchemes`
are completely UNCHANGED by this design (see "Why not infer" above).
OpenAPI generation continues to read `Descriptor.Security`/
`SecuritySchemes` exactly as it does today.

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
boundary:

```go
// ports — mirrors adapters/nethttp.RequireScopes exactly, except the
// wrap shape is a decorator (func() (T, error)) instead of http.Handler.
func RequireScopes[T any](schemeName string, reqs []route.SecurityRequirement, extract func(ctx context.Context, vars map[string]string) (map[string][]string, error)) middleware.Middleware {
    return middleware.Middleware{
        Name:      "require-scopes:" + schemeName,
        Satisfies: []string{schemeName},
        Fn: func(ctx context.Context, vars map[string]string, next func() (T, error)) (T, error) {
            var zero T
            granted, err := extract(ctx, vars)
            if err != nil {
                return zero, err
            }
            if !route.Satisfied(reqs, granted) {
                return zero, UnsatisfiedScopesError{Requirements: reqs, Granted: granted}
            }
            return next()
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
`docs/concepts/declaring-apis-and-ports.md`'s "non-spec" workflow).

**This sketch is proof, not a commitment to ship in Phase 1** — the
actual `Read`/`Write` signature change, `MiddlewareShapeError` reuse, and
`ports.Cache`/`ports.SQL` equivalents remain Phase 2 for IMPLEMENTATION.

## Phase 2+ sketch (not fully speced)

- **`api/events` + `adapters/mqtt`/`mqtt5`**: same `middleware.Middleware`
  type, same `Satisfies`/drift-closing-validation shape; `Fn`'s concrete
  type becomes `func(ctx, *pahomqtt5.Publish, reqs) error` (mirrors
  today's `mqtt5.SecurityFunc` exactly) — `Subscribe`/`Publish`/`Serve`/
  `Call` all gain the same variadic parameter and `error` return.
- **`api/reqreply`**: same, for `mqtt5`'s `ServeOptions`/`CallOptions`.
- **`api/mcp`**: N/A by existing, permanent design — MCP has no
  `SecurityFunc`/security methods at all (host-application-managed auth).
- **`ports.Cache`/`ports.SQL`**: same decorator shape as `ports.File`
  above, adapted to each port's own operation signatures (`Get`/`Set` for
  Cache; `Query`/`Insert` for SQL) — not sketched in full here since the
  `ports.File` sketch already establishes the pattern; a straightforward
  mechanical extension once `ports.File` ships.

## Relationship to Observer

`Options.Observer`/`stats.Observer` is a SEPARATE, deliberately unchanged
mechanism — it remains the right hook for route/channel/port-SPECIFIC
implementation observability (validation errors, request lifecycle,
pipeline apply events) and is NOT folded into `middleware.Middleware` by
this design. However, the SAME `middleware.Middleware` mechanism COULD
ALSO express a logging/metrics/tracing middleware — e.g. a
general-purpose `func(http.Handler) http.Handler` that logs every
request, or a `ports.File` decorator that records read/write latency —
as a COMPLEMENT to or, for some use cases, an ALTERNATIVE to parts of the
`Observer` pattern. This is noted here as a possibility this design makes
available, not a commitment to migrate `Observer` — Phase 1 (and this
doc) stays scoped to security-shaped cross-cutting concerns only.

## Files to create/modify (Phase 1)

| File | Change |
|---|---|
| `middleware/middleware.go` (new package) | `Middleware` struct, doc comments |
| `middleware/middleware_test.go` (new) | Construction/shape tests |
| `route/security.go` (or `route.go`) | `Satisfied` function + tests |
| `adapters/nethttp/adapter.go` | `Options.SecurityFunc` removed; `Register`/`Handler` accept `...middleware.Middleware`, apply drift-closing validation, apply general-purpose wrapping |
| `adapters/nethttp/client.go` | `CallOptions.CredentialFunc` removed; `Call` accepts `...middleware.Middleware` |
| `adapters/nethttp/errors.go` (or inline) | `MissingSecurityMiddlewareError`, `UnsatisfiedScopesError`, `MiddlewareShapeError` |
| `adapters/nethttp/scopes.go` (new) | `RequireScopes` |
| `adapters/chi/adapter.go` | Mirror nethttp's changes exactly |
| `examples/adapters-nethttp-security`, `adapters-chi-security`, `adapters-nethttp-client`, `mutable-security-keys` | Rewritten call sites |
| `docs/features/security.md` | Rewritten `SecurityFunc`/`CredentialFunc` sections to the new `middleware.Middleware`-based API; new "Delegating authentication to an external proxy" section using `RequireScopes` |
| `docs/concepts/middleware.md` (new) | Design rationale — the Core-thesis pure-I/O framing, the type-erasure + adapter-side-assertion pattern, why security schemes are NOT inferred from attached middleware, the `ports.File` proof-of-generality sketch. Mirrors `docs/concepts/observable-layers.md`'s role for the `Observer` pattern. |
| `docs/features/middleware.md` (new) | Practical usage guide — `RequireScopes`, the OAuth2-Proxy-in-front pattern, migration from `Options.SecurityFunc`/`CallOptions.CredentialFunc`, a `ports.File` worked example. Mirrors `docs/features/observer.md`'s role. |
| `docs/concepts/declaring-apis-and-ports.md` | Add a one-line cross-link (its own "See also" list) to the new `docs/concepts/middleware.md` — no duplicated content. |
| `.github/instructions/go-codex.instructions.md` | New `middleware` package row; updated `route`/`adapters/nethttp`/`adapters/chi` rows |
