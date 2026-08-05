# Route-Only Security Scheme Declaration + Client-Side Credential Validation — `api/rest`

> **Status:** SHIPPED (Phase 1). Kept as design history — see
> [Security & Authentication](../features/security.md) for current usage docs.
> [← Back to Roadmap](index.md)

## Motivation

`api/rest.SecurityScheme` already lets a route declare a security scheme's
spec metadata PLUS an optional `Codec *codex.Codec[string]` that validates
the raw credential's FORMAT (e.g. "is this actually a well-formed JWT
string?") — but only on the SERVER side, and only via a `Builder`.
`Builder.AddSecurityScheme` + `Route.Register(b)` populate
`RouteHandle.SecuritySchemes`, and `adapters/nethttp`/`chi`'s `Handler`
consults it (`validateSecurityCredentials`) to reject a malformed credential
with `rest.SecurityCredentialError` (401) BEFORE calling `SecurityFunc`.

The CLIENT side has no equivalent. `nethttp.Call`'s `CredentialFunc` returns
an `http.Header` that gets merged into the outgoing request completely
unvalidated — a bug in a hand-written `CredentialFunc` (wrong prefix, empty
string, wrong scheme entirely) is invisible until the SERVER rejects the
request, turning what could be an immediate, local, typed error into a round
trip and a generic `UnexpectedStatusError{StatusCode: 401}` with no
indication of WHY. Worse, there was previously no way to attach a
`SecurityScheme`+`Codec` to a route at all unless the caller used
`Route.Register(builder)` — `Route.ClientHandle()` (the documented,
zero-ceremony path for pure-client packages with no `Builder`/spec, e.g.
`examples/go-edge-models/docker/registry`) never populated
`RouteHandle.SecuritySchemes`, so a client-only consumer had no way to
declare "this route's Bearer credential must look like a JWT" even if it
wanted to.

This feature closes both gaps with ONE fully route-level mechanism:
`rest.WithSecurityScheme` — a `RouteOpt` that is the SOLE way to declare a
security scheme (there is no builder-level equivalent after this change).
The SAME `rest.NewRoute(...)` value — including its declared security
scheme — builds BOTH a server-side handle (`Route.Register`) and a
client-side handle (`Route.ClientHandle`) with IDENTICAL credential-format
enforcement on both sides. A symmetric client-side codec check is added to
`nethttp.Call`, reusing the EXISTING server-side extraction/validation logic
verbatim (`validateSecurityCredentials`/`extractCredential` already operate
on a generic `*http.Request` — they work unchanged against the just-built
outgoing request).

Prompted by (and validated against) `examples/go-edge-models/docker/registry`:
that package builds every route via `.ClientHandle()` with no `Builder` at
all, and its `NewAuthCredentialFunc` builds the `Authorization` header via
`internal.BearerTokenCodec.Encode` — the format IS already codec-guaranteed
by construction there specifically, so this feature is not needed to FIX a
bug in that package today, but the discussion surfaced a genuine, general
asymmetry in `api/rest`/`adapters/nethttp` worth closing for the general
case (hand-written `CredentialFunc`s that do NOT go through a codec on the
way in).

**Design history note:** an earlier draft of this doc kept the existing
builder-level `Builder.AddSecurityScheme` alongside a new route-level
`WithSecurityScheme`, merged with route-level winning by name. The
maintainer (sole consumer of go-codex) rejected that as unnecessary
complexity: "the goal is to specify everything in the route for client and
server side... we can make breaking changes if necessary." This doc now
reflects the simplified, route-only, breaking design.

## Scope decisions (what's in Phase 1, what's deferred)

| In scope | Out of scope |
|---|---|
| New `rest.WithSecurityScheme(name string, scheme SecurityScheme) RouteOpt` — the ONLY way to declare a route's security scheme+codec, consumed identically by `Route.Register` and `Route.ClientHandle` | `api/events`/`api/reqreply`/`api/mcp`'s parallel `SecurityScheme`/`AddSecurityScheme` mechanisms — each API layer keeps its own independent vocabulary (established convention, e.g. `RouteMeta`/`ChannelMeta`/`ErrorAction`); untouched this round |
| **Remove** `Builder.AddSecurityScheme`/`Builder.securitySchemes` entirely (breaking change — no deprecation period, no migration shim; sole consumer confirmed this is acceptable) | Deprecating-then-removing — rejected; straight removal, migrate the ~6 in-repo call sites in the same change |
| `nethttp.Call`/`CallHandle` symmetric client-side check: after merging all headers into the outgoing `*http.Request` (declared header params, `ExtraHeaders`, `CredentialFunc`'s `credHeaders`), reuse the existing `validateSecurityCredentials`/`extractCredential` functions (already generic over `*http.Request`, zero duplication) to validate the credential FORMAT before `client.Do` | A NEW error type — reuses `rest.SecurityCredentialError{Scheme, Err}` verbatim (already the exact right shape, already imported by `adapters/nethttp`) |
| `stats.SecurityObserver.RecordSecurityRejection(routePath, schemeName)` fired on a client-side codec rejection, mirroring the server-side call site exactly | A new Observer interface — `SecurityObserver` already exists and already has the right shape (`location, scheme string`) |
| `Builder.OpenAPISpec()` aggregates `components.securitySchemes` from registered routes' own `SecuritySchemes` (via a new `securitySchemes()` method on the existing unexported `routeEntry` interface) instead of a builder-level map | Configurable dedup precedence when two routes declare the same scheme name differently — last-registered-wins, documented; not configurable |
| `adapters/chi` — no client-side change needed (chi has no `Call`/`CallHandle`; it's server-only, and the server path already works via `Register`-populated `SecuritySchemes`, now route-sourced instead of builder-sourced — transparent to chi) | New chi-specific work |

## Toolchain / dependency decisions

**Why route-level only, no builder-level fallback at all:** the initial
draft kept `Builder.AddSecurityScheme` for the common case of many routes
sharing one named scheme (defined once at the builder, referenced by name
everywhere), with route-level `WithSecurityScheme` as an override. The
maintainer rejected this: since Go already supports "define once, reference
everywhere" via an ordinary package-level variable —

```go
var bearerAuth = rest.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")}.
    WithCodec(codex.String().Refine(validate.BearerToken))

var GetTagsRoute = rest.NewRoute[GetTagsReq, TagsList](..., rest.WithSecurityScheme("bearerAuth", bearerAuth), ...)
var GetManifestRoute = rest.NewRoute[GetManifestReq, ManifestEnvelope](..., rest.WithSecurityScheme("bearerAuth", bearerAuth), ...)
```

— there is no loss of "declare once" ergonomics; the builder-level map was
solving a problem Go's own language already solves, at the cost of a SECOND
place credential shape/format could live (and, critically, a place
`ClientHandle()` could never see, since it never takes a `Builder`). Removing
it entirely collapses "where is this scheme defined" to one unambiguous
answer: the route itself.

**Why not an optional `*Builder` parameter on `ClientHandle()` instead:**
considered and rejected in an earlier round of this same discussion —
it would reintroduce a `Builder` dependency into the exact code path
(`ClientHandle`) whose entire purpose is to NOT require one; a pure client
package like `docker/registry` would need to construct and hold a
`*rest.Builder` just to carry a scheme declaration, for no spec benefit at
all.

## API surface

```go
// api/rest/builder.go

// WithSecurityScheme declares scheme's spec metadata and optional Codec for
// THIS route. It is the ONLY way to declare a security scheme in go-codex —
// there is no builder-level equivalent (Builder.AddSecurityScheme has been
// removed). Both Route.Register and Route.ClientHandle populate
// RouteHandle.SecuritySchemes from this declaration, so the SAME
// rest.NewRoute(...) value — including its security scheme — builds a
// server-side handle (Register) and a client-side handle (ClientHandle)
// with IDENTICAL credential-format enforcement on both sides: the server's
// Handler validates an INCOMING credential against Codec before calling
// SecurityFunc; nethttp.Call validates an OUTGOING credential (the header
// CredentialFunc returned) against the SAME Codec before sending.
//
//	var bearerAuth = rest.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")}.
//	    WithCodec(codex.String().Refine(validate.BearerToken))
//
//	var GetTagsRoute = rest.NewRoute[GetTagsReq, TagsList](
//	    "GET", "/v2/{name}/tags/list",
//	    c.Struct[GetTagsReq](), TagsListCodec,
//	    rest.RouteMeta{Security: bearerAuthSecurity},
//	    rest.WithSecurityScheme("bearerAuth", bearerAuth),
//	    rest.NewPathParam("name", ...),
//	)
func WithSecurityScheme(name string, scheme SecurityScheme) RouteOpt

// routeBuilder gains one new field:
//   securitySchemes map[string]SecurityScheme
// populated by the WithSecurityScheme RouteOpt's applyRoute method.
```

```go
// Builder — REMOVED entirely:
//   securitySchemes map[string]SecurityScheme   (struct field)
//   func (b *Builder) AddSecurityScheme(name string, s SecurityScheme) *Builder
// NewBuilder no longer initializes a securitySchemes map.
// Builder.AddGlobalSecurity/RouteMeta.Security are UNCHANGED — this
// feature only changes WHAT a scheme looks like, not WHICH routes require it.

// Route.Register(b) — SIMPLIFIED (was: schemes := clone(b.securitySchemes)):
//   h.SecuritySchemes = rb.securitySchemes   // route-level only, no merge

// SSERoute.Register(b) — same simplification (SSE has no ClientHandle
// equivalent; server-only, but shares the same routeBuilder shape).

// Route.ClientHandle() — MODIFIED (was: no SecuritySchemes field set at all):
//   h.SecuritySchemes = rb.securitySchemes   // route-level only; no Builder exists here
```

```go
// routeEntry interface — one new method:
type routeEntry interface {
    descriptor() route.Route
    securitySchemes() map[string]SecurityScheme  // NEW
}
// typedRouteEntry / typedSSEEntry implement it by returning e.handle.SecuritySchemes.

// Builder.OpenAPISpec() — MODIFIED aggregation (was: for name, s := range b.securitySchemes):
//   schemes := make(map[string]SecurityScheme)
//   for _, e := range b.entries {
//       for name, s := range e.securitySchemes() {
//           schemes[name] = s  // last-registered-wins on name collision (documented)
//       }
//   }
//   for name, s := range schemes {
//       ob.AddSecurityScheme(name, s.SecurityScheme)
//   }
//   // (rest of OpenAPISpec unchanged: servers, schemas, globalSecurity, routes)
```

```go
// adapters/nethttp/client.go — Call/CallHandle, new step inserted AFTER all
// headers are merged into httpReq (declared HeaderParams, ExtraHeaders,
// CredentialFunc's credHeaders) and BEFORE client.Do:
//
//   // 8b. Validate the outgoing credential FORMAT — client-side mirror of
//   // the server-side check in Handler. Reuses validateSecurityCredentials/
//   // extractCredential UNCHANGED (both already operate on a generic
//   // *http.Request; httpReq — with every header already merged, including
//   // Cookies via httpReq.AddCookie and the query string already appended
//   // to httpReq.URL — is a valid input with zero adaptation needed).
//   if len(secReqs) > 0 {
//       if credErr := validateSecurityCredentials(httpReq, secReqs, handle.SecuritySchemes); credErr != nil {
//           if secObs, ok := obs.(stats.SecurityObserver); ok {
//               secObs.RecordSecurityRejection(routePath, firstScheme(secReqs))
//           }
//           obs.RecordRequest(method, routePath, 0, time.Since(start))
//           return zero, credErr  // rest.SecurityCredentialError — errors.As-navigable, unchanged shape
//       }
//   }
```

No new exported symbols in `adapters/nethttp` beyond what already exists —
`validateSecurityCredentials`/`extractCredential`/`firstScheme` are
unexported, same-package helpers already shared between the server
`Handler` path (adapter.go) and now the client `Call` path (client.go); zero
duplication, zero new functions to write beyond the call site itself.

## Structured errors (all implement `slog.LogValuer`)

No new error types. Reuses `rest.SecurityCredentialError{Scheme, Err}`
(already implements `Error()`, `Unwrap()`, and is `errors.As`-navigable) —
verified this is the identical shape needed for the client-side case; the
only difference is WHERE it's constructed (client `Call` vs server
`Handler`), not what it carries.

## Observer integration

Reuses `stats.SecurityObserver.RecordSecurityRejection(location, scheme string)`
— already exists, already has the exact right shape. Client-side call site
mirrors the server-side one exactly:

```go
if secObs, ok := obs.(stats.SecurityObserver); ok {
    secObs.RecordSecurityRejection(routePath, firstScheme(secReqs))
}
```

`location` is the route path template (same convention `RecordRequest`
already uses client-side — not the concrete URL). No new Observer
interface, no new location-string convention.

`obs.RecordRequest(method, routePath, 0, time.Since(start))` — status `0`,
consistent with every other pre-flight, no-network-call-sent rejection
(`PathParamError`, `QueryParamError`, `CredentialFunc` error, etc.).

## Unit test plan

| Test | Verifies |
|---|---|
| `TestWithSecurityScheme_ClientHandle_PopulatesSecuritySchemes` | `ClientHandle()` on a route with `WithSecurityScheme(...)` carries the scheme+codec in `RouteHandle.SecuritySchemes`, with NO `Builder` involved at all |
| `TestWithSecurityScheme_Register_PopulatesSecuritySchemes` | `Register(b)` on a route with `WithSecurityScheme(...)` carries the scheme+codec (replaces the old `TestBuilder_AddSecurityScheme_propagatesToRouteHandle`) |
| `TestOpenAPISpec_AggregatesSecuritySchemesFromRoutes` | `OpenAPISpec()` emits `components.securitySchemes` correctly sourced from route-level declarations across multiple registered routes (dedup by name; last-registered-wins on collision) |
| `TestCall_CredentialFunc_ValidFormat_Passes` | A `CredentialFunc` returning a well-formed credential (passes the route's `Codec`) results in a normal request — no new rejection |
| `TestCall_CredentialFunc_MalformedFormat_ReturnsSecurityCredentialError` | A `CredentialFunc` returning a credential that fails the route's `Codec` returns `rest.SecurityCredentialError` via `errors.As`, and the request is NEVER sent (assert via a `RoundTripper` spy that counts zero calls) |
| `TestCall_CredentialFunc_MalformedFormat_RecordsSecurityRejection` | `stats.SecurityObserver.RecordSecurityRejection` is called with the route path template and scheme name on a codec rejection |
| `TestCall_NoSecurityScheme_NoValidation` (regression) | A route with `Security` declared but NO `WithSecurityScheme` entry (e.g. `examples/adapters-nethttp-client`'s `GetSecuredData` after migration, when exercising the "no CredentialFunc" demo path) behaves EXACTLY as before — `CredentialFunc` nil or present, no codec check ever fires, `UnexpectedStatusError{StatusCode:401}` still surfaces from the SERVER when credentials are missing (not a new client-side rejection) |
| `TestCall_NoCredentialFunc_SecuredRoute_StillNotAnError` (regression) | Confirms the existing "nil CredentialFunc on a secured route is not a client-side error" contract is UNCHANGED — this feature only validates a credential that WAS returned, it never requires one |

## Files to create / modify

| File | Responsibility |
|---|---|
| `api/rest/builder.go` | Remove `Builder.securitySchemes`/`AddSecurityScheme`/its `NewBuilder` init; add `routeBuilder.securitySchemes` field + `WithSecurityScheme(name string, scheme SecurityScheme) RouteOpt` + `applyRoute` method; update `Route.Register`/`SSERoute.Register`/`Route.ClientHandle` to source `SecuritySchemes` from `rb.securitySchemes` directly; add `securitySchemes()` to the `routeEntry` interface + both `typedRouteEntry`/`typedSSEEntry` implementations; update `OpenAPISpec()`'s aggregation to iterate `b.entries` instead of the deleted builder map |
| `api/rest/builder_test.go` | Migrate the 3 existing `AddSecurityScheme`-based tests to `WithSecurityScheme`; add the new tests from the Unit test plan (rows 1–3) |
| `adapters/nethttp/client.go` | Insert the new client-side validation step (reusing `validateSecurityCredentials`/`extractCredential`/`firstScheme` from adapter.go, same package) between header-merge and `client.Do` |
| `adapters/nethttp/client_test.go` | New tests per the Unit test plan table (rows 4–8) |
| `examples/adapters-nethttp-security/main.go` | Migrate `b.AddSecurityScheme(...)` off the builder onto the route via `rest.WithSecurityScheme(...)` |
| `examples/adapters-chi-security/main.go` | Same migration |
| `examples/adapters-nethttp-client/main.go` | Same migration — verify the deliberate "no CredentialFunc → server 401" demo still passes unchanged after migration (it should — see Unit test plan row 7) |
| `.github/instructions/go-codex.instructions.md` | Document `WithSecurityScheme` as the sole scheme-declaration mechanism; remove any `Builder.AddSecurityScheme` references; document the new symmetric client-side validation |
| `docs/features/rest-api.md` (or wherever `api/rest` security is currently documented) | User-facing doc update: how to declare a route-level scheme+codec once and get both server and client enforcement, and the breaking-change note for anyone previously using `Builder.AddSecurityScheme` |

## Out of scope (Phase 2)

- **`api/events`/`api/reqreply`/`api/mcp` equivalents** — those packages
  have their OWN parallel `SecurityScheme`/`AddSecurityScheme` mechanisms;
  not touched this round. Revisit only if a concrete need arises (e.g. an
  MQTT client-side credential scheme).
- **Async/refreshable credential caching** (e.g. auto-retry once on a
  server-side 401 by calling `CredentialFunc` again) — a separate, larger
  feature; this Phase 1 only adds FORMAT validation of whatever
  `CredentialFunc` already returned, not a retry/refresh loop.
- **Enforcing "Security declared implies CredentialFunc must be non-nil"**
  — explicitly rejected as a general default during this feature's design
  discussion: it would break the legitimate, already-documented,
  already-demonstrated (`examples/adapters-nethttp-client`) pattern of
  intentionally sending an unauthenticated request to a secured route to
  exercise the server's 401 rejection path. `route.Security`/
  `rest.SecurityScheme` remain spec-only metadata unless an explicit
  enforcement mechanism (`SecurityFunc` server-side, this feature's codec
  check client-side) is wired in — symmetric with the pre-existing
  documented convention ("no adapter enforces a SecurityScheme unless
  SecurityFunc does so explicitly").
- **Migrating `docker/registry` to adopt `WithSecurityScheme`** — not
  required for correctness (`NewAuthCredentialFunc`'s header is already
  codec-guaranteed by construction via `FormatBearerToken`/
  `internal.BearerTokenCodec.Encode`); optional future polish once this
  feature ships, purely for OpenAPI-spec documentation value if that
  package ever grows a `Builder`.

## Open design decisions (to resolve before/during implementation)

- **Collision policy when two routes declare the same scheme name
  differently** (e.g. one sets a `Codec`, another doesn't, for the same
  `"bearerAuth"` name) in `OpenAPISpec()`'s aggregation: current lean is
  last-registered-wins (simple, matches Go map semantics, documented in the
  godoc) — no validation/error on mismatch. Revisit if this proves
  surprising in practice; a stricter option would be to return a new error
  from `OpenAPISpec()` on a detected mismatch, but that adds complexity for
  a scenario that's arguably a caller bug best caught by convention (define
  the scheme once as a package-level var, as shown in the API surface
  example) rather than runtime validation.
- **Should `WithSecurityScheme` imply the scheme is REQUIRED on that
  route** (i.e. auto-populate `RouteMeta.Security`), or must a caller still
  separately declare `RouteMeta.Security: []route.SecurityRequirement{route.Require("bearerAuth")}`?
  Current lean: keep them SEPARATE — `RouteMeta.Security` answers "is auth
  required for this operation" (drives BOTH server enforcement gating and
  client validation gating via `len(secReqs) > 0`), while
  `WithSecurityScheme` answers "what does this named scheme look like."
  Conflating them would make `WithSecurityScheme` implicitly force every
  route using scheme name X to require it, which breaks the documented
  `route.SecurityRequirement` `nil`-inherits/empty-means-none semantics.
