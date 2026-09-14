# D-0003 — Codec-Declared Middlewares — a general pattern for reusable middleware/policy values

> **Status:** Implemented — architectural foundation. All 7 design decisions
> (D1-D7, see "Resolved design decisions") are shipped and verified across
> `api/rest` (`Route` AND `SSERoute`), `api/events`, and every pub/sub
> adapter (`adapters/mqtt5`, `adapters/mqtt`, `adapters/zeromq`). **This
> remains the accurate, current description of shipped code.**
>
> **Forward-looking note (not a status change):**
> [Feature](../roadmap/protocol-native-features.md) (a broader
> roadmap-stage redesign, idea only, no code written — formerly titled
> "Feature/Provider") has RESOLVED its relationship to this doc's
> `Declaration[In,Out]`/`Middleware[In,Out]` mechanism (that doc's §3, via
> a confirmed 4-stage lifecycle model, tested with a throwaway Go
> prototype): both mechanisms occupy the SAME declare-time,
> spec-contributing lifecycle stage as that doc's sealed, per-adapter
> `Capability` mechanism, WITHOUT merging into one Go type — each keeps
> its own runtime-enforcement path (`Middleware[In,Out]`: reflection-based
> `Transform`/`.Use(mw)`, unchanged; `Capability`: sealed, compile-time
> `Attach`-time supply). This is NOT the earlier sub-round's "yes,
> subsume" conclusion (reached against a since-superseded, open
> string-ID-based primitive) — that conclusion no longer applies; the
> current resolution keeps the two mechanisms distinct. Nothing in THIS
> doc or in shipped code changes as a result of that roadmap doc alone;
> any actual migration remains explicitly deferred to a separate,
> dedicated implementation-planning round. Until that round ships, this
> doc's
> design remains final and unchanged.
>
> Codec-backed, per-pattern middleware declaration (`middleware.Declaration[In,Out]`
> + `rest.Middleware[In,Out]`/`events.Middleware[In,Out]`) for `api/rest`
> (`Route` AND `SSERoute`) and `api/events`, fully additive — no breaking
> changes to `middleware.Middleware`/`SecurityScheme`/existing `.Use(...)`
> call sites. Two attachment styles, one shared vocabulary across ALL
> THREE surfaces: **`Transform`/`ClientTransform`** (route/channel-BOUND,
> `fn` gets `req *Req`/`msg *T` access for reading/enriching, plus `In` for
> vars the route/channel doesn't model) and plain **`.Use(mw)`**
> (route/channel-AGNOSTIC, via `WithReceive`/`WithSend`-bundled Fns, reused
> verbatim across many routes/channels). SSE gets its own
> `TransformSSE`/`ClientTransformSSE`; events adopts REST's naming
> exactly. Deliberately NO raw `*http.Request` anywhere — everything
> stays codec-defined, a stricter position than net/http's/chi's own
> middleware convention. A feasibility analysis confirms extending this to
> `ports` (File/Cache/Dir) later is realistic, not aspirational (see
> "Feasibility" below); `ports.SQL` is a confirmed, separate exception.
>
> Headline decisions from the D1-D7 resolution pass: **D2** makes a
> middleware `fn` error `ErrorPattern`-eligible, falling back to a NEW
> `rest.MiddlewareError` (not `SecurityError`); **D6(b)** ENFORCES
> `Declaration.Name` uniqueness per route/channel (NEW
> `rest.DuplicateMiddlewareNameError`), deliberately diverging from the
> legacy mechanism's looser precedent; **D7** REJECTS combining both
> attachment styles on one `Middleware[In,Out]` value as ambiguous (NEW
> `rest.AmbiguousMiddlewareAttachmentError`).
>
> **Supersedes** [Common-Base + Per-Pattern-Derived Middleware Types](../roadmap/common-middleware-architecture.md)
> — see "Relationship to other roadmap docs" below. [← Back to Design Documents](index.md)

## Lessons learned (implementation, Phase 1)

- **`RouteMiddleware`'s marker method must be EXPORTED, not unexported.**
  Every code snippet in this doc originally showed `isRouteMiddleware()` (unexported),
  mirroring `ports.Pattern`'s own technique. This compiles right up until an ACTUAL
  cross-package value (`rest.Middleware[In,Out]`) is passed to `.Use(...)` — Go's
  unexported-method interface satisfaction is scoped PER PACKAGE (confirmed with a
  minimal reproduction), so a method literally named `isRouteMiddleware` declared in
  package `api/rest` is a DIFFERENT identifier from `middleware.RouteMiddleware`'s own
  unexported `isRouteMiddleware`, and never satisfies it. `ports.Pattern` avoids this
  because EVERY `Pattern` implementation (`RESTPattern`, `EventPattern`, etc.) is
  declared INSIDE the `ports` package itself — never cross-package. Fixed by renaming
  the marker method to the EXPORTED `RouteMiddlewareMarker()` everywhere (package
  `middleware`'s interface + `Middleware`'s implementation, and `rest.Middleware[In,Out]`'s
  own). All code snippets in this doc below reflect the corrected, exported name.

- **`ResponseDepositor` (`examples/rest-api`) is NOT a candidate for a `Transform`-based
  refactor — confirmed during Phase 4, not a gap in the implementation.** The original
  Motivation section's premise (`POST /users`'s `session` cookie needing declarative
  `MaxAge`/`Insecure`) is real, but `ResponseDepositor.SetHeader`/`SetCookie` are called
  from INSIDE `MakeCreateUserHandler`, AFTER `store.Save(record)` runs — i.e. the
  Location header and session cookie VALUE are derived from data the HANDLER ITSELF
  computes (a newly created user's ID), not from the route's own `Req`. `Transform`'s
  dispatch is deliberately PRE-HANDLER-ONLY (D1, confirmed and not revisited: "no second,
  post-handler attachment point... a concern that genuinely does already have a
  mechanism: the EXISTING wrapping-shaped Fn") — a `Transform`-attached `fn` runs BEFORE
  the handler and has no access to whatever the handler will go on to compute. The
  §7 worked example (`sessionCookiePolicy`) that motivated this whole design works ONLY
  because it hardcodes `newSessionToken()` independent of any handler-computed value —
  it was never actually a fit for `examples/rest-api`'s OWN `CreateUserRoute`, whose
  session value depends on `user.ID`. **Conclusion: `ResponseDepositor`/
  `chiResponseDepositor`/`nethttpResponseDepositor` remain the CORRECT, intentional
  escape hatch for this specific case** (post-handler-computed response header/cookie
  values) — not a gap this design was ever meant to close, and no code change is made
  here. A genuinely `Transform`-suited case is one where the response header/cookie
  value derives ONLY from the route's own `Req`/topic vars/a middleware's own `In` —
  e.g. this doc's §7 API-key/session-attribute sketches — never from the handler's own
  return value.

## Motivation

`examples/rest-api`'s `POST /users` handler needs a `session` response cookie with
custom attributes (`MaxAge: 3600, Insecure: true`). Investigating this surfaced a real
gap: `rest.NewRequiredResponseCookieParam` already merges a cookie's NAME/VALUE
declaratively, but merge-derived cookies always get DEFAULT `PendingCookie.Opts`
(confirmed in code: both `adapters/nethttp/serve.go` and `adapters/chi/serve.go` build
`PendingCookie{Name: k, Value: v}` with a zero-value `Opts` for every merge-derived
cookie) — there is no way today to declare `MaxAge`/`Secure`/`SameSite`/`Path`/`Domain`
declaratively. A caller who needs those must drop to the adapter-specific
`WithResponseCookies`/`PendingCookie.Opts` escape hatch (exactly what
`examples/rest-api`'s `ResponseDepositor`/`chiResponseDepositor`/
`nethttpResponseDepositor` does today).

Investigating this surfaced a SECOND, independently-known gap, already tracked (see
"Relationship to other roadmap docs" below): `middleware.Middleware` is a single flat
struct with 5 REST-only param-contribution fields, imported directly by `api/rest`,
`api/reqreply`, AND `api/events` — even though only REST uses those fields. `api/events`
already carries an INTERIM fix for this (`checkUnsupportedMiddlewareParams` in
`api/events/builder.go`, confirmed via code — eagerly rejects any `.Use()`-attached
middleware whose REST-only fields are non-empty).

Both gaps are resolved by ONE mechanism: giving middleware itself an Input/Output
codec pair, exactly like a route/channel declares its Req/Resp (or Item) shape via a
codec — and making that mechanism PER-PATTERN (REST's own merge vocabulary vs.
events' own), not a single shared shape.

## Guiding principle: middleware IS a spec-layering mechanism (non-spec is the degenerate case, not a separate mechanism)

Before designing anything new, it's worth stating explicitly what every EXISTING
middleware kind already does, since this design must fit the SAME model, not invent a
parallel one. **Middleware, as a whole, is fundamentally a spec-layering
mechanism**: a declare-time value, attached via `.Use(...)`, resolved ONCE (at
Register/ValidateRoute time for anything affecting the spec; at dispatch time for
anything affecting runtime behavior). Some attachments layer something into the final
OpenAPI/AsyncAPI spec; some have dispatch-time runtime behavior; some do both. A
middleware contributing NOTHING to the spec — an Observer, a retry wrapper, a raw
enrichment step — is not a DIFFERENT mechanism from spec-layering; it is simply the
case where the spec column of the SAME mechanism is empty. Every existing and
newly-designed middleware kind already fits this ONE model, confirmed via code:

| Middleware kind | Spec effect (layers into the final spec) | Runtime effect (dispatch-time behavior) |
|---|---|---|
| `middleware.SecurityScheme`, attached alone (no `HandleMW` ever paired) | Layers a Security requirement + scheme | None — documents an externally-enforced requirement |
| `middleware.SecurityScheme` + paired `HandleMW`/`ClientMW` | Layers a Security requirement + scheme | Verifies (server) / supplies (client) the credential |
| `FromHeaderParam`/`FromCookieParam`/`FromQueryParam`/response siblings | Layers a header/cookie/query param | None (spec-only, unless separately paired with a general-purpose Fn reading the same value) |
| Security-shaped Fn, attached UNPAIRED (`mw` nil or `Security` nil) | **None** — the degenerate case | Direct write access into `*Req`/`*T` — `Transform`'s enrichment use case, confirmed "free" per d-0002 |
| Wrapping-shaped Fn (`func(http.Handler) http.Handler`, pub/sub's `func(next) next`) | **None** — the degenerate case | Wraps the entire dispatch/round-trip — Observer, retries, tracing |
| `rest.Middleware[In,Out]` (this doc's design) + `Transform`/`ClientTransform` (route-BOUND) or `.Use(mw)` (route-AGNOSTIC) | Layers header/cookie/query/response params (§3) — the SAME conflict-detection the legacy path already uses | `Transform`/`ClientTransform`: reads/enriches the route's own `req *Req` (or `msg *T`) PLUS decodes `In` from vars it doesn't model; `.Use(mw)`: decodes `In` only, NO `req`/`msg` access, reusable verbatim across routes (§4) |
| `rest.PathPrefix`/`events.TopicPathPrefix` (sketch, §"Extending the model") | Layers a path/topic-string transform | **None** — the mirror-image degenerate case |
| `ports.FilePathPrefix`/`DirPathPrefix` (sketch, §"Extending the model") | **None** — ports has no spec to layer into | **None** — resolved once into internal config at declare time, not per-call dispatch (see the refinement discussed where this row is sketched) |

This table is the reason `Middleware[In,Out]` retaining the FULL declared param
(spec metadata AND merge field — see §3) isn't a bespoke fix bolted onto a
narrower design — it is simply this middleware kind populating BOTH columns at
once, exactly like paired Security already does. It is also why the existing
non-spec Fn shapes (§9) need no special justification: they were ALWAYS spec-layering
middleware with an empty spec column, whether or not that was stated explicitly
before.

**This is the guiding design principle for REST and events middleware today, and
should guide any FUTURE `ports` middleware mechanism too.** No `ports.Middleware`
concept exists yet (`ports.Pattern` is a different thing — binding metadata, not
cross-cutting-concern attachment); `docs/roadmap/declarative-middleware.md` already
sketches/proves `ports.File`'s own decorator shapes as Phase 2 prior art.

### Feasibility of a full `.Use()`-based `ports.Middleware[In,Out]` — a grounded analysis, not just a placeholder

The long-term goal is for `ports` to adopt the SAME `.Use()`+codec-backed model this
doc designs for REST/events, not a permanently separate mechanism. Investigated
whether the building blocks this design depends on ALREADY exist in `ports` —
they do, more concretely than expected.

**The prerequisite this whole design leans on is already present in `ports`.** Every
`Middleware[In,Out]` variant in this doc reuses ALREADY-GENERIC param constructors
(`rest.NewRequiredHeaderParam[T,V]`, `events.NewTopicParam[T,V]`) instead of inventing
new ones — retargeting `T` from a Route's/Channel's own type to a Middleware's `In`/
`Out`. Confirmed via code: `ports` already has the IDENTICAL shape of reusable,
generic constructor — `ports.NewCacheKeyParam[T, V any](...) MergedCacheKeyParam[T]`
and `ports.NewFilePathParam[T, V any](...)` are BOTH already generic over `T`,
mirroring `rest.NewRequiredHeaderParam[T,V]` exactly. A `ports.Middleware[In,Out]`
reusing `WithCacheKey(p MergedCacheKeyParam[In])`/`WithFilePathVar(p
MergedFilePathParam[In])` is the SAME architectural move already proven twice (REST,
events), not a speculative extension.

**Two port shapes need two different treatments, both achievable:**

- **Pattern-bound ports** (`SourcePort`/`SinkPort`/`IOPort`/`ToolPort` with
  `RESTPattern`/`EventPattern`) already build a real `rest.Route`/`events.Channel`
  internally — confirmed `RESTPattern.Opts []rest.RouteOpt`/`EventPattern.Opts
  []events.ChannelOpt` already pass straight through to it, meaning `.Use(securityScheme)`
  ALREADY reaches these ports today via the Pattern's `Opts` field. A future
  `.Use()`-on-the-port-itself is mostly SUGAR here: accumulate attached
  `middleware.RouteMiddleware` values and feed them into the Pattern's `Opts` before
  the port constructs its handle — no new middleware TYPE needed, since the port
  already inherits REST's/events' real spec unchanged.
- **Pattern-less ports** (`File`/`Cache`/`Dir`, confirmed to have NO spec at all) —
  a `ports.Middleware[In,Out]` here occupies ONLY the runtime column, exactly as the
  guiding-principle table predicts for a spec-less concern. `NewFile(...)`/`NewCache(...)`
  ARE already declare-once values (mirrors `NewRoute`/`NewChannel`'s own two-phase
  shape: declare once, dispatch per-operation via `Read`/`Write`/`Get`/`Set`) —
  contrary to `declarative-middleware.md`'s OLDER decorator sketch, which attaches
  `mws ...middleware.Middleware` as a CALL-TIME variadic parameter and concluded
  "there is only ONE attachment point for ports" — that conclusion describes ITS OWN
  specific design choice, not an inherent limitation of `ports.File`'s structure.
  Under a `.Use()`-based redesign, `File[T].Use(mws ...middleware.RouteMiddleware)
  File[T]` at declare time + dispatch inside `Read`/`Write` mirrors REST/events'
  declare/dispatch split cleanly.

**The "security via an HTTP auth endpoint" example maps directly onto this, with
`In = struct{}`**: mirrors this doc's OWN REST session-cookie worked example (§7),
which also uses `struct{}` for a concern with no var-boundary to decode. `fn(ctx,
struct{}{})` reads a credential from `ctx` (via `middleware.ContextField`, already
confirmed to compose for free) or an explicit param, calls the HTTP endpoint itself
(arbitrary I/O — exactly what the older decorator sketch already assumed a `Fn`'s
body could do), and returns `Out` (e.g. granted scopes) or an error gating the real
`Read`/`Write`. **Observer middleware for ports** maps onto the SAME wrapping-shaped,
runtime-only category §9 already covers for REST/events — no new mechanism category
needed, though `stats.WithObserver(ctx, obs)`/`ObserverFromContext` already solve this
independently today via ctx injection, so a `.Use()`-attached observer middleware
would be an ADDITIONAL declare-once attachment style, not a required replacement.

**Concrete, confirmed implementation costs (not just abstract feasibility):**

- `ports.File[T].Read(vars, opts)`/`.Write(vars, v, opts)` have **NO `ctx
  context.Context` parameter today** — confirmed via code, only an OPTIONAL
  `opts.Context` field used solely for observer lookup. Any middleware `fn` needing
  real `ctx` (the HTTP auth call's timeout/cancellation, or `ContextField` access)
  requires `Read`/`Write` to gain a proper `ctx` parameter — a genuine signature
  change (additive variants, or breaking the existing ones) to plan for explicitly,
  not free.
- `ports.SQL` is confirmed **metadata-only by design** — no handle, no `Route`/
  `Channel`-equivalent value exists to attach `.Use()` to at all (SQL query text/
  placeholders are driver-specific closures owned by the adapter constructor
  directly). This is a REAL LIMIT CASE, not just an edge case: unlike File/Cache/Dir,
  SQL has no natural "declare once" value under the CURRENT architecture — it would
  need its own separate design (a different attachment point, e.g. at the adapter
  constructor call) or an accepted, permanent exception, mirroring how `SQLPattern`
  is already a deliberately different, metadata-only shape vs.
  `RESTPattern`/`EventPattern`/`FilePattern`.

**Verdict: realistic, not merely aspirational** — the core reusable-constructor
prerequisite already exists for File/Cache/Dir; the two-phase declare/dispatch shape
already exists structurally (just not wired to `.Use()` yet); the "arbitrary I/O in a
middleware's `fn` body" pattern this doc already designs for REST (`struct{}`-typed
`In`) directly covers the HTTP-auth-endpoint case with no new mechanism. What remains
is real but bounded: wiring `.Use()`/dispatch onto File/Cache/Dir, threading a proper
`ctx` through `Read`/`Write`, and either solving or explicitly accepting SQL's
metadata-only exception. Not designed in full here — recorded as the grounded
starting point for that future work, not a placeholder. See also
`ports.FilePathPrefix`/`DirPathPrefix` in "Extending the model" below — a concrete,
confirmed-needed driver (neither `File` nor `Dir` has a general workspace-root-prefix
mechanism today) for exactly this same "Pattern-less ports get `.Use()` at declare
time" mechanism, applied to a template/config transform rather than a runtime `In`/
`Out` dispatch.

## The design

### 1. `middleware.RouteMiddleware` — additive marker interface, zero breaking changes

```go
// package middleware
// RouteMiddleware is a marker interface any attach-time middleware value
// can implement to become passable to a route/channel's .Use(...) method.
// The marker method is EXPORTED (RouteMiddlewareMarker, not an unexported
// isRouteMiddleware) — Go's unexported-method interface satisfaction is
// scoped PER PACKAGE, so a type declared in api/rest/api/events could
// NEVER satisfy an interface whose unexported method lives in package
// middleware, regardless of name (confirmed the hard way during
// implementation — see "Lessons Learned"). ports.Pattern's own
// unexported-method sealing technique works ONLY because every Pattern
// implementation is declared IN the ports package itself.
type RouteMiddleware interface{ RouteMiddlewareMarker() }

// Middleware gains ONE new, trivial method — every EXISTING
// .Use(someSecurityScheme)-style call site keeps compiling unchanged
// (Go's structural interface satisfaction).
func (Middleware) RouteMiddlewareMarker() {}
```

`Route.Use`/`SSERoute.Use` (confirmed at `api/rest/middleware.go:130,137`) and
`Subscriber.Use`/`Publisher.Use` (confirmed at `api/events/builder.go:1713,1793`)
widen their parameter type from `...middleware.Middleware` to
`...middleware.RouteMiddleware`. No existing call site anywhere in the repo needs to
change.

### 2. `middleware.Declaration[In, Out]` — new, minimal, pattern-agnostic shared core

```go
// package middleware
type Declaration[In, Out any] struct {
	Name     string
	InCodec  codex.Codec[In]
	OutCodec codex.Codec[Out]
}

func NewDeclaration[In, Out any](name string, inCodec codex.Codec[In], outCodec codex.Codec[Out]) Declaration[In, Out] {
	return Declaration[In, Out]{Name: name, InCodec: inCodec, OutCodec: outCodec}
}
```

`SecurityDeclaration` (today's security-specific shape: raw credential string +
`route.SecurityScheme` + scopes) is intentionally **NOT** retrofitted onto
`Declaration` — it is protocol-driven, already shipped and stable, and stays its own
special-cased mechanism. `middleware.Common`'s original proposal (see
`common-middleware-architecture.md`) — `{Name, Security}` — and `Declaration[In,Out]`
are separate, independent, COMPOSABLE mechanisms: a route can `.Use(securityScheme)`
for security and separately attach a `Declaration`-backed middleware for a
non-security concern, together.

### 3. `rest.Middleware[In, Out]` — REST's own per-pattern derived type, populating BOTH the spec and runtime columns

Per the guiding principle above, `Middleware[In,Out]` is designed to occupy BOTH
table columns at once: its declared params layer into the route's spec (exactly like
`middleware.Middleware`'s existing `RequestHeaderParams`/etc already do), AND its
merge fields drive its runtime dispatch (§4). This requires retaining the FULL
declared param — spec metadata (`Name`/`Description`/`Required`/`Codec`) AND the
merge-capable `FieldCodec` — not just the merge field alone (an earlier draft of this
design discarded the spec half, which is what originally caused §9's "declare twice,
no conflict check" gap; retaining both halves resolves it as a natural consequence of
fitting this table, not a bespoke fix).

**Route/channel-AGNOSTIC vs. route/channel-BOUND — a real design gap, resolved by
splitting the ATTACHMENT point, not by splitting `Middleware[In,Out]` itself (§4
covers this in full; summarized here since it changes what `Middleware[In,Out]`
itself needs to carry).** A `Middleware[In,Out]` whose runtime `fn` needs `req
*Req`/`msg *T` access (the enrichment case) is inherently BOUND to one route/channel
— Go's type system forces `Req`/`T` into `fn`'s own type, so the literal same `fn`
value cannot be reused across routes with different `Req` types (confirmed: the
ALREADY-SHIPPED security-shaped Fn has this SAME constraint). But a `Middleware[In,Out]`
whose `fn` genuinely does NOT need `Req`/`T` access (pure `In`→`Out` logic — API-key
verification with no enrichment, a rate-limit check, a cookie policy) can be
ATTACHED, verbatim, to arbitrarily many routes/channels — mirroring how
`middleware.SecurityScheme`/Observer/`PathPrefix` already achieve this today, simply
because THEIR Fn types never mention `Req`/`T` either. `Middleware[In,Out]` therefore
gains an OPTIONAL way to carry such a `Req`/`T`-free `fn` DIRECTLY as a field — see
the `receiveFn`/`sendFn` fields and `WithReceive`/`WithSend` methods in the struct
below, after the following clarification of `In`'s own scope.

**`In`'s scope, stated precisely (a critical review round clarified this): `In` is
NOT a general-purpose replacement for the route's own `Req`.** It exists SPECIFICALLY
to give a middleware declarative, codec-validated access to header/cookie/query
values the route's OWN `Req` does not model at all — e.g. an API key the route never
declares as part of its own request shape. Whatever the route's `Req` ALREADY models
(including any header/cookie/query the ROUTE ITSELF declared as a merge field) is
read/enriched DIRECTLY via `req *Req`, passed alongside `In` to `Transform`'s
`fn` (§4) — mirroring the ALREADY-ESTABLISHED, ALREADY-PROVEN security-shaped Fn
precedent (`func(ctx, *http.Request, *Req) (...)`, confirmed via code to already
have POINTER/write access, and confirmed to already cover `nethttp.Transform`'s
ENRICHMENT use case "for free" — see §9). A middleware can therefore both READ
whatever the route's `Req` already carries AND WRITE new, derived data into it (a
user ID resolved from an auth token, say) that the wire request never carried at
all — the actual handler then sees the enriched `Req` transparently. `In`/`req *Req`
are deliberately NOT alternatives to choose between — a middleware needing BOTH
kinds of data (something the route already models, AND something declaratively new)
uses both parameters together (§4).

**No raw `*http.Request` anywhere in this design, by deliberate choice.** An earlier
draft of this design considered exposing the raw `*http.Request` directly to a
middleware's `fn`, mirroring net/http's/chi's own middleware convention (`func(http.Handler)
http.Handler`, confirmed via `adapters/nethttp/adapter.go`'s `applyGeneralMiddleware`
to expose ONLY the raw request/response, zero codec knowledge) — REJECTED for THIS
mechanism specifically: everything a `Middleware[In,Out]`-based `fn` touches must be
codec-defined (`Req` via the route's own codec, `In`/`Out` via the middleware's own
`Declaration[In,Out]` codecs), with zero untyped, unvalidated escape hatches — the
SAME "declare once, validated, schema'd" trade-off go-codex already makes everywhere
else (routes/channels never expose raw bytes to a handler either). This is a
STRICTER position than net/http's/chi's own convention, adopted deliberately — the
codebase's EXISTING wrapping-shaped Fn (`func(http.Handler) http.Handler`, §9) still
covers the "I genuinely need raw, untyped request/response access" case for callers
who want it; `Middleware[In,Out]` is a DIFFERENT, more declarative option for callers
who don't.

```go
// package api/rest
type Middleware[In, Out any] struct {
	middleware.Declaration[In, Out]

	reqHeaderParams  []MergedHeaderParam[In]           // spec metadata + merge field, together
	reqCookieParams  []MergedCookieParam[In]
	reqQueryParams   []MergedQueryParam[In]
	respHeaderParams []MergedResponseHeaderParam[Out]
	respCookieParams []MergedResponseCookieParam[Out]

	// receiveFn/sendFn, when set (via WithReceive/WithSend below), carry a
	// Req/Resp-FREE runtime Fn directly on the value itself — enabling
	// route/channel-AGNOSTIC attachment via plain .Use(mw) (see the new
	// subsection in §4). Left nil for the route/channel-BOUND case, where
	// Transform/ClientTransform supply an Req/T-accessing fn separately
	// instead (never both — see §4's D7: combining both is rejected as
	// ambiguous via rest.AmbiguousMiddlewareAttachmentError).
	receiveFn func(ctx context.Context, in In) (Out, error)
	sendFn    func(ctx context.Context) (In, error)
}

func NewMiddleware[In, Out any](decl middleware.Declaration[In, Out]) Middleware[In, Out]

// WithRequestHeader/WithRequestCookie/WithRequestQuery populate the In side;
// WithResponseHeader/WithResponseCookie populate the Out side. Each takes
// the SAME MergedHeaderParam[In]/MergedCookieParam[In]/MergedQueryParam[In]/
// MergedResponseHeaderParam[Out]/MergedResponseCookieParam[Out] values
// already returned by rest.NewRequiredHeaderParam[In,V]/
// NewRequiredCookieParam[In,V]/NewRequiredQueryParam[In,V]/
// NewRequiredResponseHeaderParam[Out,V]/NewRequiredResponseCookieParam[Out,V]
// (and their Optional siblings) — confirmed these constructors are ALREADY
// GENERIC over any T, not hardcoded to a route's own Req/Resp. NO new
// REST-side param constructors are introduced by this design.
func (m Middleware[In, Out]) WithRequestHeader(p MergedHeaderParam[In]) Middleware[In, Out]
func (m Middleware[In, Out]) WithRequestCookie(p MergedCookieParam[In]) Middleware[In, Out]
func (m Middleware[In, Out]) WithRequestQuery(p MergedQueryParam[In]) Middleware[In, Out]
func (m Middleware[In, Out]) WithResponseHeader(p MergedResponseHeaderParam[Out]) Middleware[In, Out]
func (m Middleware[In, Out]) WithResponseCookie(p MergedResponseCookieParam[Out]) Middleware[In, Out]

// WithReceive/WithSend attach a route/channel-AGNOSTIC runtime Fn directly to
// mw — NEITHER signature mentions Req/Resp, so the returned Middleware[In,Out]
// value (fn included) can be passed to .Use(...) verbatim, on as many
// different routes as needed (see §4's new subsection). Use Transform/
// ClientTransform instead (§4) when fn genuinely needs req/msg access.
func (m Middleware[In, Out]) WithReceive(fn func(ctx context.Context, in In) (Out, error)) Middleware[In, Out]
func (m Middleware[In, Out]) WithSend(fn func(ctx context.Context) (In, error)) Middleware[In, Out]

// RouteMiddlewareMarker makes Middleware[In,Out] satisfy middleware.RouteMiddleware
// — NEW in this round (an earlier draft left this unimplemented, since
// Middleware[In,Out] was ONLY ever attached via Transform/ClientTransform
// directly, never via .Use()). Required now so .Use(mw) can recognize and
// dispatch a route/channel-agnostic mw carrying a WithReceive/WithSend fn.
// EXPORTED, not isRouteMiddleware — see §1's RouteMiddleware note on why an
// unexported marker method can never be satisfied from another package.
func (Middleware[In, Out]) RouteMiddlewareMarker() {}
```

**How the spec half layers in — reusing the EXISTING conflict-detection pass, not a
parallel one.** Whichever attachment point is used — plain `.Use(mw)` (route/channel-
agnostic, this section) or `Transform`/`ClientTransform` (route/channel-bound, §4) —
feeds `mw`'s retained `MergedHeaderParam[In].HeaderParam`/etc (the embedded spec
structs) into the SAME per-name contribution map `applyParamDeclarations`/
`checkParamConflicts` already build from `middleware.Middleware.RequestHeaderParams`/
etc and the route's own manual params — collected from ALL sources, conflict-checked
ONCE, then layered into `rb.headerParams`/etc exactly as today. A manual route-level
declaration with the same param name still wins (unchanged precedence); the
middleware's OWN merge field keeps decoding the raw header/cookie/query value
regardless of which spec entry "won," since merge decode operates on raw vars,
independent of spec bookkeeping. `middleware.Middleware` (the legacy type) is
unaffected — it keeps flowing through its own existing, unchanged code path; this is
purely a SECOND (and now THIRD) source feeding the SAME resolution pass, mirroring
how the pass already tolerates an arbitrary number of contributing
`middleware.Middleware` values today. Exact plumbing (e.g. whether `routeBuilder`
gains one new accumulator field, or `Middleware[In,Out]`'s spec-contribution is
captured directly inside the `RouteOpt` each attachment point returns) is a small
implementation detail, not resolved further here — the CONTRACT (one declaration,
both effects, one shared conflict-check, REGARDLESS of attachment style) is what
this section commits to.

### 4. `rest.Transform` (route/channel-BOUND) + `.Use(mw)` (route/channel-AGNOSTIC) — two attachment points for one `Middleware[In,Out]` type

Both are free functions, not methods (Go disallows new type params on a method —
the SAME constraint `nethttp.Transform[Req]` itself is already subject to).

```go
// package api/rest — SERVER attachment (RECEIVING role)
func Transform[Req, Resp, In, Out any](
	r Route[Req, Resp],
	mw Middleware[In, Out],
	fn func(ctx context.Context, req *Req, in In) (Out, error),
) Route[Req, Resp]

// package api/rest — CLIENT attachment (SENDING role) — NEW
func ClientTransform[Req, Resp, In, Out any](
	r Route[Req, Resp],
	mw Middleware[In, Out],
	fn func(ctx context.Context, req Req) (In, error),
) Route[Req, Resp]
```

**Naming, adopted deliberately, not invented**: `adapters/nethttp.Transform[Req
any](fn func(ctx, r *http.Request, req *Req) error) any` ALREADY EXISTS in this
codebase — confirmed via code — as exactly today's route-BOUND, `Req`-enriching
mechanism (generic over `Req`, instantiated per route, consumed via
`route.HandleMW(nil, nethttp.Transform(...))`). `rest.Transform`/`rest.ClientTransform`
adopt this ALREADY-ESTABLISHED name for the SAME concept — "a `Req`-bound enrichment
Fn, one instantiation per route" — rather than inventing new vocabulary, living in
`api/rest` (transport-agnostic) rather than the `nethttp` adapter package (which
targets `*http.Request` specifically) since this mechanism has no adapter dependency.

**`fn`'s `req` parameter (§3's clarification, made concrete here)**: `Transform`'s
`req *Req` is the route's OWN already-decoded value — POINTER, so `fn` can both READ
whatever the route's `Req` already models AND WRITE derived/enriched data into it
(e.g. a user ID resolved from an auth token) before the actual handler runs, exactly
mirroring the security-shaped Fn's existing `*Req` access. `ClientTransform`'s `req
Req` (value, not pointer — the caller already owns and can mutate its own `Req`
directly before calling `Call` at all, so write access through the middleware isn't
the only path to enrichment on this side) is available for `fn` to INSPECT when
deciding what `In` to produce (e.g. deriving an outgoing trace header FROM a field
already present in `Req`). Neither `req` parameter is a substitute for `In` — a
middleware needing data the route's `Req` does NOT model at all still declares it via
`Middleware[In,Out]`'s own merge fields (§3), decoded/encoded independently.

`Transform` registers `(mw, fn)` as a type-erased `MiddlewareHandler` entry on
`routeBuilder` (mirrors exactly how today's `HandleMW`/`ServerImplementation` already
erase `Req` — same erasure technique, one more entry). Unlike `HandleMW` (whose Fn
shape is checked via runtime reflection), both `Transform`'s and `ClientTransform`'s
Fn shapes are checked AT COMPILE TIME — `In`/`Out` are inferred from `mw`/`fn`
directly. Both are FREE FUNCTIONS, not methods — Go disallows type parameters on a
method beyond its receiver's own, the SAME constraint `nethttp.Transform[Req]` itself
is already subject to.

**Why `ClientTransform` exists — confirmed via code, not designed from scratch**:
`CallWithHandle`/`CallOptions` already auto-derive `QueryParams`/`HeaderParams`/
`CookieParams` from a route's OWN Req merge fields (ENCODE direction, via
`codex.EncodeVars`), and `DecodeMergedResponse` decodes a route's OWN Resp merge
fields back from the HTTP response (DECODE direction) — this is the EXISTING
client-side "one struct, one call" symmetry for a route's own Req/Resp. Security has
the SAME split: `Route.HandleMW` (server: verify) + `Route.ClientMW` (client: supply —
`mergeCredentialHeaders` runs a `func(ctx, secReqs) (http.Header, error)` Fn,
producing headers attached to the outgoing request). The original version of this doc
designed ONLY the server half — an asymmetry inconsistent with BOTH of these existing
precedents. `ClientTransform` closes it: `fn` PRODUCES an `In` value (mirrors the
credential Fn producing a value to send, rather than decoding one that arrived); the
middleware's OWN request header/cookie/query merge fields — the SAME fields
`WithRequestHeader`/etc. declare — are reused for `EncodeIn`, merged into
`CallOptions` alongside the route's own derived values. After the response,
`DecodeOut` runs mechanically (no Fn — mirrors `DecodeMergedResponse`) against the
middleware's OWN response header/cookie merge fields, returning `Out` to the caller
alongside `Resp`.

This closes the loop: the SAME `rest.Middleware[In,Out]` declaration now has BOTH a
server attachment (`Transform`) and a client attachment (`ClientTransform`), exactly
mirroring `HandleMW`/`ClientMW`'s existing split for security. A middleware can be
attached via EITHER, BOTH, or neither, per route — e.g. an API-key middleware
declared once, `ClientTransform`-attached on the calling application's `Route` value
(to send the key) and `Transform`-attached on the serving application's OWN copy of
that same `Route` value (to verify it) — mirroring exactly how a shared
`middleware.SecurityScheme` value is attached via `HandleMW` server-side and `ClientMW`
client-side today.

**Attaching BOTH on the SAME `Route` value, chained (a critical review round flagged
this scenario as unaddressed) — confirmed ALLOWED, not designed against.** Since
`Transform`/`ClientTransform` both take a `Route[Req,Resp]` and return a
`Route[Req,Resp]`, `ClientTransform(Transform(r, mw, fnServer), mw, fnClient)` is
ordinary, legal Go — e.g. a single process acting as BOTH server and proxying client
for the same route. This is not the doc's typical worked-example shape (server/
client attachment on two SEPARATE `Route` values, in different applications), but
nothing in this design prevents or specially handles it either. §3's spec-layering
conflict-detection (feeding `mw`'s param structs into `checkParamConflicts`) already
tolerates this correctly, mirroring how it already tolerates an arbitrary number of
contributing `middleware.Middleware` values today — N4's already-added-names guard
applies uniformly regardless of WHICH free function (`Transform` or
`ClientTransform`) contributed a given name.

### Route/channel-AGNOSTIC attachment — plain `.Use(mw)`, for `Fn`s that never need `req`/`msg` access

**The problem this solves, stated precisely.** `Transform`/`ClientTransform`'s `fn`
signature FORCES the concrete `Req` type into `fn`'s own Go type — the literal SAME
`fn` value therefore CANNOT be passed to two different routes with different `Req`
types (Go's type system won't allow it). Confirmed this is NOT a new limitation this
design introduces — the ALREADY-SHIPPED security-shaped Fn has the IDENTICAL
constraint (`impl.Fn.(func(context.Context, *http.Request, *Req) (map[string][]string,
error))` is type-asserted PER ROUTE in `runSecurityMiddlewareReflect`). But NOT every
middleware needs `Req` access — confirmed every EXISTING route/channel-AGNOSTIC
middleware kind achieves reuse specifically by making sure its Fn's type NEVER
MENTIONS `Req`/`Resp`/`T` at all: the wrapping-shaped Fn (`func(http.Handler)
http.Handler`, no Req/Resp anywhere — the literal same `nethttp.Observability(obs)`
value is reusable across every route verbatim), the client-side credential Fn
(`func(ctx, []route.SecurityRequirement) (http.Header, error)`, no Req either —
confirmed `adapters/nethttp.NewCachingCredentialFunc` builds exactly ONE such value,
reused across every route needing that credential), and `PathPrefix` (no Fn at all,
just a value).

**The fix: `Middleware[In,Out]`'s `WithReceive`/`WithSend` (§3) bundle a `Req`/`T`-free
Fn DIRECTLY onto the value itself, attached via the EXISTING, UNWIDENED `.Use(mw)`
call** — no new attachment method to learn beyond `.Use()`, which callers already
know from `SecurityScheme`/`PathPrefix`. Since `Middleware[In,Out]` now implements
`middleware.RouteMiddleware` (§3), `route.Use(mw)` recognizes it directly: it layers
`mw`'s declared params into the spec (§3, IDENTICAL mechanism regardless of
attachment style) AND, when `mw` carries a `WithReceive`/`WithSend` Fn, registers the
runtime dispatch automatically — in the SAME call, for as many DIFFERENT routes as
`mw` is `.Use()`'d on, with the literal SAME `mw` value (Fn included) reused
verbatim:

```go
rateLimitPolicy := rest.NewMiddleware(
	middleware.NewDeclaration[struct{}, RateLimitOut]("rate-limit", codex.Struct[struct{}](), rateLimitOutCodec),
).WithReceive(func(ctx context.Context, _ struct{}) (RateLimitOut, error) {
	return checkRateLimit(ctx)
})

// Attached verbatim to N different routes — the SAME rateLimitPolicy value,
// NO per-route Req-typed closure needed, unlike Transform:
routeA = routeA.Use(rateLimitPolicy)
routeB = routeB.Use(rateLimitPolicy)
routeC = routeC.Use(rateLimitPolicy)
```

**This is a MATERIAL WIDENING of `.Use()`'s existing contract, stated explicitly
rather than silently changed.** Today, `.Use()` is declare-time-ONLY — confirmed via
`WithMiddleware`'s own doc comment: "the actual Security/RequestParams/ResponseParams
application... happens ONCE, at Register/ValidateRoute time" — it never runs
anything at request-dispatch time. Bundling a runtime Fn onto a `.Use()`-attached
`Middleware[In,Out]` value means `.Use()` can now ALSO cause runtime dispatch, for
this ONE new attaching type — `middleware.Middleware` (legacy) is UNAFFECTED, since
it never carries a Fn at all (Security's runtime Fn is ALWAYS supplied separately,
via `HandleMW`/`ClientMW`, matched by scheme name). Worth flagging as a genuine
precedent change to `.Use()`'s documented behavior, not merely an additive feature.

**D7 — combining both attachment styles on the SAME `Middleware[In,Out]` value:
RESOLVED, REJECTED as ambiguous.** A `mw` carrying a bundled `WithReceive`/
`WithSend` Fn, if ALSO passed to `Transform`/`ClientTransform` (which supplies its
OWN, separate `fn`), leaves it genuinely unclear WHICH Fn should run — unlike
header/cookie merge fields (where multiple contributing sources simply compose),
there is only ONE dispatch point per role, so two candidate Fns for the same role
cannot both apply. **Decision: reject this combination at Register/Handle time**
with a new, typed error — **`rest.AmbiguousMiddlewareAttachmentError{Name string}`**
(events mirror: `events.AmbiguousMiddlewareAttachmentError{Name string}`) — checked
in the SAME resolution pass as D6(b)'s uniqueness check. A `Middleware[In,Out]`
value is therefore either BUNDLED-Fn (agnostic, `.Use()`-only) or BOUND-Fn (supplied
separately via `Transform`/`ClientTransform`) for a given role — never both on the
SAME value. Nothing prevents declaring TWO SEPARATE `Middleware[In,Out]` values
(even representing "the same logical concern") where one is bundled and the other
is bound — this restriction applies only to combining both styles on ONE value.

### 5. Adapter dispatch wiring — server (`adapters/nethttp/serve.go`, `adapters/chi/serve.go`) and client (`adapters/nethttp/client.go`)

**Server side — confirmed dispatch-ordering finding that makes this safe**: the
existing security-middleware dispatch (`runSecurityMiddlewareReflect`) already runs
AFTER Req is fully decoded+merged, receiving BOTH the raw `*http.Request` and the
merged `*Req` — at that exact point, `headerVars`/`cookieVars`/`queryVars` (the raw
string maps used to build Req) are ALREADY in scope. A `Transform`-registered
entry:

1. Derives `In` from those SAME raw var maps via its OWN independent
   `codex.DecodeVars` call (using `mw`'s own `reqHeaderMergeFields`/etc.) plus a full
   `InCodec.Validate` pass — completely independent of Req's own decode, no ordering
   conflict, no shared-state hazard.
2. Calls `fn(ctx, reqPtr, in)` → `(out, err)`, passing the SAME already-decoded
   `*Req` pointer the handler will ALSO receive (confirmed via code: `serve.go`
   already holds this exact `reqPtr` in scope at this dispatch point, used
   identically by `runSecurityMiddlewareReflect` today) — any enrichment `fn` writes
   into `*req` is visible to the actual handler afterward, transparently. An error
   short-circuits the request (same error-response handling
   `runSecurityMiddlewareReflect` already does).
3. On success, derives response header/cookie values from `out` via
   `codex.EncodeVars` (using `mw`'s own `respHeaderMergeFields`/`respCookieMergeFields`),
   merging them into the SAME pending-header/cookie collection the route's OWN
   `EncodeMerged`/`EncodeResponseMergeFields` already populates — middleware-produced
   and route-produced response header/cookie values compose into ONE final HTTP
   response. Conflict precedence (same header/cookie name from both sources):
   registration-order, last-applied-wins — consistent with how chained `Opt`s already
   behave elsewhere in this codebase.

**Client side — confirmed via `adapters/nethttp/client.go`'s `CallWithHandle`/`call`**:
a `ClientTransform`-registered entry runs at the SAME point `mergeCredentialHeaders`
already does (before the HTTP request is built):

1. Calls `fn(ctx, req)` → `(in, err)`, passing the caller's OWN `Req` value (already
   fully built at this point, before `CallWithHandle`'s own derivation runs) for `fn`
   to inspect if useful. An error aborts the call before any network activity
   (status 0 per the existing client observer convention — see `stats.Observer`'s
   "status 0 is the sentinel for 'call attempted but no HTTP request reached the
   network'" rule).
2. On success, derives header/cookie/query values from `in` via `codex.EncodeVars`
   (using `mw`'s own `reqHeaderMergeFields`/etc.), merged into `CallOptions` alongside
   the route's own req-derived values (same `overrideDerived`-style precedence
   `CallWithHandle` already uses for the route's own merge fields — caller-supplied
   `CallOptions` entries still win over BOTH the route's own derived values and a
   middleware's).
3. After the response is received, derives `Out` via `codex.DecodeVars` (using `mw`'s
   own `respHeaderMergeFields`/`respCookieMergeFields`) against the response's actual
   headers/cookies — mechanical, no Fn — returned to the caller ALONGSIDE the decoded
   `Resp` (exact return-shape mechanism — e.g. an additional out-param on `Call`, vs. a
   separate `CallTransform`-scoped accessor — is a small signature detail to finalize
   during implementation, not a design blocker).

**Route/channel-AGNOSTIC dispatch (`.Use(mw)`, §4's new subsection) reuses the
IDENTICAL dispatch steps above — just without a `req`/`msg` parameter to pass.** A
`.Use()`-attached `Middleware[In,Out]` carrying a `WithReceive` Fn dispatches at the
SAME server-side point as `Transform` (step 1-3 above, with `fn(ctx, in)` instead of
`fn(ctx, reqPtr, in)`); a `WithSend` Fn dispatches at the SAME client-side point as
`ClientTransform` (`fn(ctx)` instead of `fn(ctx, req)`). No new dispatch MECHANISM is
introduced — only a narrower `fn` signature at the SAME two call sites.

### SSE — migrated onto the SAME model, via `TransformSSE`/`ClientTransformSSE` (UN-DEFERRED — a critical review round confirmed neither blocker is fundamental)

An earlier draft of this doc scoped SSE OUT entirely ("plain `Route`/`Client.Call`
only"), reasoning that `SSERouteHandle` lacks response merge fields and
`consumeSSE`'s client implementations recognize only the credential shape. **A
further review confirmed BOTH gaps are small, concrete, sketchable additions — not
fundamental blockers** — and un-deferring them is REQUIRED, not optional, for this
design to deliver a genuinely "simple, declarative, consistent workflow": a caller
attaching the SAME cookie-policy/rate-limit middleware to a plain `Route` and an
`SSERoute` should use the SAME two verbs, not learn a second vocabulary for SSE.

**In-side — CONFIRMED wireable with ZERO new prerequisite** (upgraded from the
earlier draft's "likely"): `adapters/nethttp/serve_sse.go` ALREADY builds
`headerVars`/`cookieVars`/`queryVars` and calls `runSecurityMiddlewareReflect(ctx, r,
reqPtr, impls, secReqs)` at the EXACT SAME dispatch timing plain `Route` already
uses (confirmed via code, lines ~143-191) — `TransformSSE`'s `DecodeIn`→`fn` step
slots in at this SAME point, unchanged from `Transform`'s own mechanism.

**Out-side — CONFIRMED solvable via a small, concrete addition, not a redesign**:
confirmed via code an EXACT gap exists in `serve_sse.go` between
`runSecurityMiddlewareReflect` returning and SSE's own headers being committed
(`w.Header().Set("Content-Type", "text/event-stream")`, marked "must be set before
WriteHeader" in the code's own comment) — this is PRECISELY where `Out`'s
response-header/cookie dispatch slots in: derive header/cookie values from `Out` via
`codex.EncodeVars`, call `w.Header().Set(...)`/`SetCookie(...)`, THEN commit SSE's
own headers. The only missing piece: `SSERouteHandle[Req,Event]` needs its own
`responseHeaderMergeFields`/`responseCookieMergeFields` fields + an
`EncodeResponseMergeFields`-equivalent method, mirroring `RouteHandle`'s existing
ones — a small, well-understood addition.

**Client-consume — CONFIRMED solvable via one new recognized Fn shape, same
category of change `Call` already has TWICE**: `consumeSSE`'s
`validateClientImplementationShapes` recognizes ONLY the credential shape today, but
`Call`'s OWN `validateCallImplementationShapes` ALREADY recognizes TWO shapes
(credential + general-purpose wrapping) — proving "recognize more than one Fn shape"
is an established mechanism, just not yet extended to SSE consume. Adding a THIRD
recognized shape (`func(ctx, req Req) (In, error)`, `ClientTransformSSE`'s shape) is
the SAME category of change.

**One genuinely NEW decision this surfaces (no existing precedent to mirror):**
unlike `Call` (one request → one response, `Out` decodes once from THAT response),
SSE's consume delivers MANY events over ONE long-lived connection — `Out` should
decode ONCE, from the INITIAL SSE connection's response headers/cookies at
connection-open time, BEFORE the first event arrives, analogous to a single
`Response`'s headers being set once per connection rather than per event. Sketched
in principle here; the exact mechanical hook (e.g. whether `consumeSSE` needs a
new "connection opened" callback distinct from its existing per-event delivery) is
not designed in full mechanical detail in this round.

**New free functions, mirroring `Transform`/`ClientTransform` but for `SSERoute`**:

```go
// api/rest — SSE server attachment (RECEIVING role)
func TransformSSE[Req, Event, In, Out any](
	s SSERoute[Req, Event],
	mw Middleware[In, Out],
	fn func(ctx context.Context, req *Req, in In) (Out, error),
) SSERoute[Req, Event]

// api/rest — SSE client attachment (SENDING role, consume-side)
func ClientTransformSSE[Req, Event, In, Out any](
	s SSERoute[Req, Event],
	mw Middleware[In, Out],
	fn func(ctx context.Context, req Req) (In, error),
) SSERoute[Req, Event]
```

**Why TWO SEPARATE pairs of names, not one shared pair** — confirmed via code:
`HandleMW`/`ClientMW` are METHODS today, defined separately on BOTH `Route` and
`SSERoute` (identical logic, different receiver — no naming collision, since Go
allows the same method name on different types). `Transform`/`ClientTransform`
(and their SSE counterparts) MUST be FREE FUNCTIONS (Go disallows type parameters
on a method beyond its receiver's own) — free functions CANNOT be "overloaded" by
receiver type the way methods can, so `Route` and `SSERoute` genuinely need
DISTINCT function names. `Out` on `SSERoute` targets response headers/cookies only
(no body) — the SAME scope `Out` already has on plain `Route` (it never touches
`Resp`'s body codec there either), so this is not a new asymmetry, just consistently
applied to a second route kind.

**Route/channel-AGNOSTIC attachment needs NO new naming for SSE at all** —
`SSERoute.Use` ALREADY EXISTS (confirmed via code) and needs only the SAME
`middleware.RouteMiddleware`-widening `Route.Use` already gets (§1) to recognize a
`Middleware[In,Out]` carrying a bundled `WithReceive`/`WithSend` Fn — the identical
mechanism, the identical call (`sseRoute.Use(mw)`), no SSE-specific verb needed.

### 6. Structured errors

- **`rest.MiddlewareInputError{Name string, Err error}`** (mirrors `PathParamError`-
  style wrapping, implements `slog.LogValuer`) — returned when a middleware's `In`
  fails to decode/validate. Events mirror: `events.MiddlewareInputError{Name string,
  Err error}`, returned from `events.Transform`'s topic-var `In` decode failure.
- **`rest.MiddlewareError{Name string, Err error}`** (NEW, per D2) — the fallback
  when a `Transform`/`ClientTransform` `fn`'s own returned business error does NOT
  match any declared `ErrorPattern` (checked FIRST, via `handle.ErrorResponseFor`,
  mirroring how a handler error is already treated). Default status 400. Implements
  `slog.LogValuer`. Events mirror: `events.MiddlewareError{Name string, Err error}`
  (D2's events extension), the fallback when `events.Transform`'s `fn` error
  doesn't match a declared `ErrorChannel` pattern.
- **`rest.DuplicateMiddlewareNameError{Route string, Name string}`** (NEW, per
  D6(b)) — returned at Register/Handle time when two `Middleware[In,Out]` values
  attached to the SAME route (via ANY combination of `.Use()`/`Transform`/
  `ClientTransform`) share the same `Declaration.Name`. Implements `slog.LogValuer`.
  Events mirror: `events.DuplicateMiddlewareNameError{Topic string, Name string}`.
- **`rest.AmbiguousMiddlewareAttachmentError{Name string}`** (NEW, per D7) —
  returned at Register/Handle time when a SINGLE `Middleware[In,Out]` value carries
  a bundled `WithReceive`/`WithSend` Fn AND is ALSO passed to `Transform`/
  `ClientTransform` (which supplies its own, separate `fn`) — ambiguous, since only
  one Fn can run per role. Implements `slog.LogValuer`. Events mirror:
  `events.AmbiguousMiddlewareAttachmentError{Name string}`.

All four are distinct from `rest.SecurityError` (reserved for the EXISTING
`middleware.SecurityScheme` mechanism, unchanged).

### 7. Worked example — cookie attributes, the original motivating gap

```go
type CookieAttrs struct {
	Value    string
	MaxAge   int
	SameSite string // "default"|"lax"|"strict"|"none" — validate.OneOf-constrained
	Insecure bool
}

var cookieAttrsCodec = codex.Struct[CookieAttrs](
	codex.RequiredField("value", codex.String(), ...),
	codex.OptionalField("maxAge", codex.Int(), ...),
	codex.OptionalField("sameSite", codex.String().Refine(validate.OneOf("default", "lax", "strict", "none")), ...),
	codex.OptionalField("insecure", codex.Bool(), ...),
)

sessionCookiePolicy := rest.NewMiddleware(
	middleware.NewDeclaration[struct{}, CookieAttrs]("session-cookie-policy", codex.Struct[struct{}](), cookieAttrsCodec),
).WithResponseCookie(rest.NewRequiredResponseCookieParam("session", codex.String(),
	func(a CookieAttrs) string { return a.Value },
	func(a *CookieAttrs, v string) { a.Value = v },
))

// SERVER side — sets the cookie. This example's In is struct{} (no header/cookie/
// query the route doesn't already model is needed here), but req *LoginReq is
// still passed — unused in this particular example, present because Transform's
// signature always offers it (see §4). Since this fn never reads req, it could
// ALSO have been declared via .Use(sessionCookiePolicy.WithReceive(...)) instead
// (§4's route/channel-agnostic path) — Transform is used here only to show the
// full signature; either mechanism works for THIS particular example.
route = rest.Transform(route, sessionCookiePolicy,
	func(ctx context.Context, req *LoginReq, _ struct{}) (CookieAttrs, error) {
		return CookieAttrs{Value: newSessionToken(), MaxAge: 3600, Insecure: true}, nil
	})
```

This closes the ORIGINAL gap (declarative `MaxAge`/`Insecure` for a response cookie)
with ZERO new REST-side param constructors — only `rest.NewRequiredResponseCookieParam`,
already existing, reused against `Out = CookieAttrs` instead of a route's own `Resp`.
Note only the cookie's VALUE is merge-derived here (a `Path`/`Domain`/`SameSite`
extension follows the identical pattern via additional `WithResponseCookie`-style
merge fields once a concrete need for those specific attributes as SEPARATE Set-Cookie
attributes — vs. bundled inside `Out`, as sketched above — is confirmed; not designed
further here since it is a mechanical extension, not a new mechanism).

**Client side — a DIFFERENT middleware attaches the SENDING role** (an API-key
example is clearer than resending a session cookie, which is more naturally an
application-level cookie-jar concern outside a single stateless `Call`):

```go
type APIKeyIn struct{ Key string }

var apiKeyInCodec = codex.Struct[APIKeyIn](
	codex.RequiredField("key", codex.String().Refine(validate.NonEmptyString), ...),
)

apiKeyPolicy := rest.NewMiddleware(
	middleware.NewDeclaration[APIKeyIn, struct{}]("api-key-policy", apiKeyInCodec, codex.Struct[struct{}]()),
).WithRequestHeader(rest.NewRequiredHeaderParam("X-API-Key", codex.String(),
	func(in APIKeyIn) string { return in.Key },
	func(in *APIKeyIn, v string) { in.Key = v },
))

// SERVER side — verifies the key AND enriches the route's own Req with a
// resolved user ID the wire request never carried, demonstrating req *Req
// (enrichment) and in In (a header the route itself doesn't model) used
// TOGETHER — the actual handler afterward sees req.UserID already populated,
// with zero extra plumbing:
route = rest.Transform(route, apiKeyPolicy,
	func(ctx context.Context, req *GetProfileReq, in APIKeyIn) (struct{}, error) {
		userID, err := lookupUserIDForKey(in.Key)
		if err != nil {
			return struct{}{}, ErrInvalidAPIKey
		}
		req.UserID = userID // enrichment — GetProfileReq's own codec never decodes this
		return struct{}{}, nil
	})

// CLIENT side — supplies the key on every call, no manual CallOptions.HeaderParams.
// req is available to inspect (e.g. deriving the key from a field already on
// Req) but unused in this example — the key comes from the environment instead:
route = rest.ClientTransform(route, apiKeyPolicy,
	func(ctx context.Context, req GetProfileReq) (APIKeyIn, error) {
		return APIKeyIn{Key: os.Getenv("MY_API_KEY")}, nil
	})
```

The SAME `apiKeyPolicy` declaration (built once, e.g. as a shared package-level var)
is attached via `Transform` on the SERVING application's `Route` value and via
`ClientTransform` on the CALLING application's `Route` value — exactly mirroring how
a shared `middleware.SecurityScheme` value is used today. `GetProfileReq.UserID`
would be an ordinary field the route's OWN codec never decodes from the wire (no
`RequiredField`/`OptionalField` for it) — populated ONLY via this middleware's
enrichment, exactly like `nethttp.Transform`'s existing enrichment pattern (§9)
already proves works for the security-shaped Fn precedent this design's `req *Req`
parameter mirrors.

### 8. `events.Middleware[In, Out]` — the events-side derived type, adopting the SAME naming and BOTH attachment styles as REST

**Naming, RESOLVED this round (previously left open)**: an earlier draft left
events using the PRE-rename names (`HandleMiddleware`/`ClientMiddleware`), flagging
whether it should follow REST's `Transform`/`ClientTransform` rename as an open
question. Resolved in favor of ONE SHARED vocabulary across both patterns — the
explicit goal is a "simple, declarative, consistent workflow" for the user across
REST and events, which a per-pattern-different verb would directly undermine.
Events therefore renames `HandleMiddleware`/`ClientMiddleware` to `events.Transform`/
`events.ClientTransform`, mirroring REST's naming exactly (this is a rename WITHIN
an as-yet-unimplemented design, not a breaking change to shipped code — neither name
was ever implemented). The route/channel-agnostic vs. bound tension applies
identically to events: a subscribe/publish-side concern whose `fn` doesn't need
`msg *T` access is just as reusable across multiple channels as REST's `.Use(mw)`
case. `Subscriber[T].Use`/`Publisher[T].Use` ALREADY EXIST (confirmed via code) and
need only the SAME `middleware.RouteMiddleware`-widening + bundled-Fn dispatch
REST's `.Use()` gets — no new events-specific verb needed for the agnostic case,
exactly mirroring `SSERoute.Use`'s situation (see "SSE" above).

Confirmed via code: `api/events`' ONLY merge vocabulary today is topic-var merge
(`NewTopicParam`/`MergedTopicParam[T]`, `ChannelHandle.EncodeVars`) — there is NO
header/cookie concept on pub/sub's wire at all. Events' security uses a completely
different mechanism (Decision 3's in-payload `*T`-write access), not merge fields.

**Corrected role mapping (an earlier version of this doc got this backwards)** —
confirmed via `adapters/mqtt5/adapter.go`:
- **Subscribe** (`runSubscribeSecurityImpls`) runs AFTER topic vars are merged into
  the decoded value, receiving both the raw message and the merged value — exactly
  mirrors REST's SERVER dispatch. This is the RECEIVING role: derive `In` from
  incoming topic vars → call `fn(ctx, msg, in)` → error only. Pub/sub has NO
  response/reply channel, so there is nothing to encode an `Out` INTO on subscribe —
  unlike REST's server side, subscribe's `Out` is unused. Mirrors REST's `req *Req`
  addition exactly: `msg *T` is the channel's OWN already-decoded value (confirmed
  via `runSubscribeSecurityImpls`'s existing `*T`-write-access precedent — already
  proven for enrichment), letting `fn` read/enrich it, alongside `in` for anything
  the channel's topic template doesn't model.
- **Publish** (`runPublishSecurityImpls`, a `ClientImplementation`) produces a value
  (`[]UserProperty` in mqtt5's case) attached to the OUTGOING message — exactly
  mirrors REST's CLIENT dispatch (`mergeCredentialHeaders` producing headers to
  attach). This is the SENDING role: `fn(ctx, msg) → (out, error)` → encode `Out`
  into ADDITIONAL topic vars merged into the outgoing publish, with `msg T` (the
  caller's own already-built value, mirrors REST's client-side `req Req`) available
  for `fn` to inspect. `In` is unused on publish (mirrors REST client:
  `events.ClientTransform` doesn't decode anything either, it only produces/encodes).

```go
// package api/events
type Middleware[In, Out any] struct {
	middleware.Declaration[In, Out]

	topicMergeFieldsIn  []codex.FieldCodec[In]  // used by Transform (subscribe)
	topicMergeFieldsOut []codex.FieldCodec[Out] // used by ClientTransform (publish)

	// receiveFn/sendFn — the SAME route/channel-AGNOSTIC bundling mechanism
	// as rest.Middleware[In,Out] (§3): a Req/T-free Fn attached directly to
	// the value, dispatched automatically via plain .Use(mw) on a
	// Subscriber[T]/Publisher[T], reusable verbatim across many channels.
	receiveFn func(ctx context.Context, in In) error
	sendFn    func(ctx context.Context) (In, error)
}

func NewMiddleware[In, Out any](decl middleware.Declaration[In, Out]) Middleware[In, Out]

// WithSubscribeTopic/WithPublishTopic populate the In/Out topic-var merge
// vocabulary respectively, reusing the SAME events.NewTopicParam[T,V]
// constructor already used for a channel's own Item — no new events-side
// param constructor either.
func (m Middleware[In, Out]) WithSubscribeTopic(p MergedTopicParam[In]) Middleware[In, Out]
func (m Middleware[In, Out]) WithPublishTopic(p MergedTopicParam[Out]) Middleware[In, Out]

// WithReceive/WithSend mirror rest.Middleware[In,Out]'s own methods (§3) —
// bundling a route/channel-agnostic Fn for attachment via plain .Use(mw).
func (m Middleware[In, Out]) WithReceive(fn func(ctx context.Context, in In) error) Middleware[In, Out]
func (m Middleware[In, Out]) WithSend(fn func(ctx context.Context) (In, error)) Middleware[In, Out]

// RouteMiddlewareMarker makes events.Middleware[In,Out] satisfy
// middleware.RouteMiddleware — mirrors rest.Middleware[In,Out]'s own (§3).
// EXPORTED, not isRouteMiddleware — see §1.
func (Middleware[In, Out]) RouteMiddlewareMarker() {}

// Transform (subscribe, RECEIVING role, route/channel-BOUND) — mirrors
// rest.Transform, minus the Out/response half (no reply channel to encode
// into). msg *T is the channel's OWN already-decoded value (enrichable,
// mirrors req *Req exactly). Renamed from HandleMiddleware (RESOLVED naming
// question — see this section's header note).
func Transform[T, In, Out any](
	s Subscriber[T],
	mw Middleware[In, Out],
	fn func(ctx context.Context, msg *T, in In) error,
) Subscriber[T]

// ClientTransform (publish, SENDING role, route/channel-BOUND) — mirrors
// rest.ClientTransform exactly. msg T is the caller's OWN already-built
// value, available to inspect. Renamed from ClientMiddleware.
func ClientTransform[T, In, Out any](
	p Publisher[T],
	mw Middleware[In, Out],
	fn func(ctx context.Context, msg T) (Out, error),
) Publisher[T]
```

**`In`/`Out`'s scope here, stated as precisely as REST's §3 clarification (a critical
review round found this was missing).** Unlike a REST header/cookie/query — a
free-standing name a middleware can introduce independently of the route — a pub/sub
topic var is constrained by the CHANNEL's OWN topic TEMPLATE: a middleware cannot
invent a NEW topic var out of thin air, since a var only exists if the topic string
declares a matching `{varName}` placeholder in the first place. `WithSubscribeTopic`/
`WithPublishTopic` therefore only make sense for a topic var the channel's template
DOES declare, but that the channel's own `Item` (`T`) does NOT already merge via its
OWN `NewTopicParam` — e.g. a topic `"sensors/{sensorID}/{region}/data"` where the
channel's own `T` merges only `sensorID`, leaving `region` unclaimed for a
region-based authorization middleware to merge into its OWN `In` instead. This is the
DIRECT events-side analogue of REST's "`In` is for vars the route's `Req` doesn't
model" scope (§3) — restated here explicitly since events' constraint is sourced
differently (a shared topic template vs. REST's free-standing header namespace).

Both dispatch at whatever point `api/events`' own subscribe/publish handling already
resolves topic vars today (same "derive/encode from vars already in scope" principle
as REST's dispatch wiring) — `Transform`'s `DecodeIn` runs at the SAME point
`runSubscribeSecurityImpls` already runs (after topic vars are merged into the
decoded value); `ClientTransform`'s `EncodeOut` runs at the SAME point
`runPublishSecurityImpls` already runs (before the message is sent, merged into the
SAME additional-vars mechanism that mqtt5's `UserProperty` attachment already uses as
precedent — exact per-adapter wiring, e.g. whether `zeromq`/`mqtt`(v3) have an
equivalent attachment point, TBD during implementation).

This directly resolves `common-middleware-architecture.md`'s original finding for
events specifically: `events.Middleware[In,Out]` carries ONLY topic-var merge
vocabulary — no unused header/cookie/query fields — making
`checkUnsupportedMiddlewareParams`'s INTERIM eager-rejection check obsolete once this
ships (a legacy `middleware.Middleware` with REST-only fields attached to an events
`.Use()` call is STILL correctly rejected the same way; the NEW `events.Middleware[In,Out]`
path simply has no such fields to reject in the first place).

### Extending the model — `rest.PathPrefix`/`events.TopicPathPrefix`/`ports.FilePathPrefix`/`DirPathPrefix` (sketch, illustrates the model generalizes beyond REST/events)

The guiding-principle table's `PathPrefix` row is a SKETCH, included to demonstrate
that the model generalizes beyond params — not a fully-resolved design this round.
The idea: a declare-time-only value, attached via `.Use(...)` exactly like
`SecurityScheme`/`Middleware[In,Out]`, whose effect is a TEMPLATE/CONFIG TRANSFORM
resolved exactly ONCE — prefixing a route's/channel's final path or topic string
before the descriptor is built (e.g. mounting the SAME declared route/channel set
under `/api/v1` vs `/api/v2`, or a different topic namespace per deployment, without
redeclaring every route/channel):

```go
// api/rest — sketch, not finalized
func PathPrefix(prefix string) middleware.RouteMiddleware
```

```go
// api/events — sketch, not finalized
func TopicPathPrefix(prefix string) middleware.RouteMiddleware
```

**A concrete third driver confirms this generalizes past REST/events, to Pattern-less
ports too**: mounting a workspace-folder ROOT prefix for `ports.File`/`Dir` (e.g.
per-tenant or per-environment base directories) is the SAME shape, reusing the
"Pattern-less ports get `.Use()` at declare time" mechanism from the ports
feasibility analysis above:

```go
// ports — sketch, not finalized
func FilePathPrefix(prefix string) middleware.RouteMiddleware
func DirPathPrefix(prefix string) middleware.RouteMiddleware
```

Confirmed via code this closes a REAL, currently-unfilled gap: `ports.Dir`'s existing
`WithBaseDir(path string) DirOpt` is narrower than it sounds — its own doc comment
states it affects ONLY `Dir.List`'s GLOB-DISCOVERY scan anchor, explicitly having "no
effect on `Dir.BuildPath`/`MatchPath`, which already resolve relative to cwd exactly
as today." `ports.File` has no base-path concept at all. Neither port type has a
GENERAL root-prefix mechanism today, independent of this whole middleware design.

**A refinement the guiding-principle table doesn't fully capture, surfaced by this
third driver.** The table's two columns (spec effect / runtime-dispatch effect)
happen to coincide for REST/events specifically because a route's/channel's
path/topic string IS both the spec's displayed value AND the literal runtime routing
key, computed once at Register/descriptor-build time — mutating it pre-freeze
determines runtime routing with no separate dispatch step, which is why "spec-only,
no runtime Fn" accurately described `PathPrefix`/`TopicPathPrefix` there.
`ports.File`/`Dir` have **no spec at all** (confirmed, intentional) — so
`FilePathPrefix`/`DirPathPrefix` cannot be "spec-only" in that same sense; there is no
spec column to populate. But the SAME mechanical shape still applies: attached via
`.Use()` at `NewFile(...)`/`NewDir(...)`-construction time, the prefix is baked into
the `File[T]`/`Dir` value's own internal template/`baseDir` field EXACTLY ONCE —
`BuildPath`/`MatchPath`/`List`/`Delete` never need a separate per-call dispatch step,
mirroring REST's `PathPrefix` needing none. The TRUE common shape across all four
sketches is **"declare-time-only, resolved-once template/config transform"** — REST/
events render this as a spec effect because path/topic IS the spec; ports render the
IDENTICAL mechanical shape as a resolved-once CONFIG transform with NO spec effect at
all (both landing in the guiding-principle table's "spec: None" cell — but for a
different reason than Observer/wrapping-shaped Fns land there: not because it's
per-call runtime-dispatched, but because ports has no spec to populate, full stop).
Worth stating this distinction explicitly rather than letting "mirror image of
Observer" imply a resemblance that isn't really there.

**Why a `.Use()`-attached, reusable prefix value earns its keep over a plain
constructor option** (not previously stated): the value is REUSE ACROSS MULTIPLE,
ALREADY-DECLARED values, toggled together as a unit — e.g. a SET of `File[T]`/`Dir`
declarations (a config file, a log dir, a data dir) built ONCE with relative
templates, then mounted under DIFFERENT tenant/environment workspace roots by
attaching a DIFFERENT `FilePathPrefix` value per deployment, without redeclaring each
file/dir. This is the same justification REST's `/api/v1`-vs-`/api/v2` example
gestures at but never states plainly: a reusable prefix middleware is worth a
dedicated `.Use()`-attached type specifically when SEVERAL declarations need to move
together, not for a single one-off path (which a plain constructor argument already
handles more simply).

**Left open, not resolved this round**: composition semantics for MULTIPLE attached
`PathPrefix`/`TopicPathPrefix`/`FilePathPrefix`/`DirPathPrefix` values on the same
route/channel/file/dir (error on conflict, concatenate in attachment order, or
last-attached-wins) — a separate, smaller question from the guiding principle itself,
deferred to a future Refine pass once a concrete need for this specific middleware
surfaces (following this repo's own "don't design past a second concrete driver"
precedent, same as elsewhere in this doc). The ports-side sketch adds a CONCRETE new
wrinkle to this same open question: if a `.Use()`-attached `FilePathPrefix` AND an
inline `WithBaseDir`-equivalent constructor option are BOTH present on the same
`File[T]`/`Dir` value, which wins, or do they compose (e.g.
`filepath.Join(prefix, baseDir, template)`)? Not resolved — named explicitly here so
it isn't rediscovered as a surprise later. Whether this generalized attachment
mechanism should ever be EXPORTED for third-party custom spec-layering types, or stay
closed to api/rest's/api/events'/ports' own shipped types, is likewise left open —
leaning closed/sealed for now, consistent with `RouteMiddleware`'s/`ports.Pattern`'s
existing sealed-marker-interface precedent elsewhere in this codebase.

### 9. How the existing non-spec-adding Fn shapes fit the guiding principle's runtime column

The guiding principle's table already places the two EXISTING non-spec-adding Fn
shapes in the runtime-only column. This section grounds that placement against real
code — `docs/design/d-0002-pubsub-workflow-simplification.md` already asked and
answered this exact question for pub/sub's OWN middleware concept — a dedicated
review confirmed BOTH REST and events, on BOTH roles (server/subscribe,
client/publish), recognize TWO such shapes each, validated eagerly via reflection:

1. **Security-shaped, attached UNPAIRED** (`mw` nil or `mw.Security == nil`) — direct
   WRITE ACCESS to the route's/channel's own shared, already-merged `*Req`/`*T`
   (`func(ctx, *http.Request, *Req) (map[string][]string, error)` for REST server;
   the credential shape for REST client; the mirrored security shape for events).
   **Confirmed (d-0002): this shape already covers `nethttp.Transform`'s enrichment
   use case "for free"** — an unpaired security-shaped Fn can decode/derive a value
   and write it DIRECTLY into `*Req`/`*T` before the handler runs, no new mechanism
   needed.
2. **Wrapping-shaped** (most general — wraps the ENTIRE dispatch/round-trip):
   `func(http.Handler) http.Handler` (REST server), `func(next func(ctx,Req)(Resp,error))
   func(ctx,Req)(Resp,error)` (REST client, shipped per d-0001 Addendum 3),
   `func(next func(ctx,T) error) func(ctx,T) error` (events, both roles). Used for
   observability, retries, tracing, body manipulation.

**`Middleware[In,Out]` is a NEW way to populate BOTH columns together (per §3), not a
fourth, unrelated Fn-shape category competing with these two.** Both existing shapes
were ALWAYS spec-layering middleware with an empty spec column (per the guiding
principle above) — this design doesn't replace them, it adds a mechanism suited to a
different sub-case: typed extraction from vars, optionally typed production of
outgoing vars, WITH an optional spec contribution, rather than arbitrary Req-mutation
or full-dispatch wrapping with none.

Comparison against the two existing shapes (updated: `Middleware[In,Out]`'s `fn` ALSO
gets `req *Req`/`msg *T` access per §3/§4 — the SAME enrichment capability
security-shaped-unpaired already has; that row is no longer a differentiator between
the two shapes, so the table below reflects this honestly rather than the earlier,
now-corrected claim that `Middleware[In,Out]` never touches Req/T):

| | Security-shaped-unpaired | Wrapping-shaped | `Middleware[In,Out]` (this design) |
|---|---|---|---|
| Touches route's own Req/Resp (or T) | Directly, by mutation | Indirectly (wraps the whole handler) | Yes — read AND enrich, via `req`/`msg` (§3/§4), SAME access as security-shaped-unpaired |
| ALSO gets typed access to vars `Req`/`T` doesn't model | No (raw string extraction only, into Req) | No | Yes — `In`, codec-validated (§3) |
| Spec contribution | None | None | Optional — layers into the route's spec (§3) |
| Response-side declarative output | No (write-only into Req) | No (must hand-roll) | Yes, for REST only (Out → response merge fields) |
| Shape safety | Runtime reflection check | Runtime reflection check | Compile-time (generic free functions) |

The REAL remaining differentiators, now that Req/T access is shared, are: (1) `In`'s
codec-validated access to vars the route doesn't model at all (security-shaped-
unpaired only ever extracts raw strings, with no schema/validation); (2) the optional
spec contribution; (3) compile-time Fn-shape safety instead of runtime reflection.
Best suited when the concern's natural shape is "typed extraction from vars,
optionally typed production of outgoing vars, ALONGSIDE enrichment of the route's own
Req/T" rather than arbitrary Req-mutation with no schema, or full-dispatch wrapping —
an ADDITIONAL option, not a consolidation of the other two.

**`ContextField` composes with this design FOR FREE — confirmed via dispatch-order
code, mirroring d-0002's own `nethttp.Transform` finding exactly** (a naming
collision worth flagging explicitly now that this design's OWN mechanism is ALSO
named `Transform`, per §4 — the TWO are related in spirit but distinct symbols in
DIFFERENT packages: `nethttp.Transform[Req]` is the pre-existing adapter-level
helper wrapping the security-shaped Fn; `rest.Transform`/`rest.ClientTransform` are
this doc's NEW, `Middleware[In,Out]`-based mechanism — a future doc reader must not
conflate the two despite the shared name). `middleware.EnsureContextFields(ctx)` is
called at the very TOP of `adapters/nethttp/serve.go`'s (and chi's) dispatch —
BEFORE Req decode, BEFORE `runSecurityMiddlewareReflect`, and therefore BEFORE the
point this design places `Transform`'s dispatch. An implementer's `fn` passed to
`rest.Transform` can ALREADY call `someContextField.Set(ctx, value)` today with ZERO
new wiring — the actual business handler retrieves it via `someContextField.Get(ctx)`,
exactly like any OTHER Fn shape already can.

Two caveats, not design blockers:
- `ContextField.Set(ctx, raw any)` decodes `raw` via its OWN codec — whether an
  ALREADY-DECODED `Out`/`In` Go value can be passed directly (vs. needing `Out`'s own
  codec reused as the `ContextField`'s codec) is codec-dependent (many `Struct` codecs
  expect a `map[string]any`-shaped `raw`) — a small implementation detail to confirm,
  not resolved here.
- **events has NO `ContextField` wiring at all today** (confirmed: its own doc
  comment states pub/sub's security-shaped Fns get direct `*T` write access instead,
  serving the same need via a different mechanism). If `events.Transform`'s
  `fn` ever needs to publish a value for the actual subscribe handler to read
  (mirroring REST's `ContextField` use), `EnsureContextFields` would need to be added
  to pub/sub's own dispatch first — a new, small prerequisite, not previously called
  out anywhere in this doc.

**RESOLVED (previously an open gap in an earlier draft of this doc): no more
"declare twice."** An earlier version of `Middleware[In,Out]` discarded a declared
param's spec metadata, keeping only its merge field — meaning a caller wanting
"X-API-Key" BOTH documented in the OpenAPI spec AND merge-decoded into a typed `In`
had to declare it twice, with no conflict check between the two declarations. Per §3's
fix, `Middleware[In,Out]` now retains BOTH halves of every declared param, and its
spec half feeds the SAME `checkParamConflicts` pass the legacy `middleware.Middleware`
path already uses — one declaration, both effects, one shared conflict-check. This
isn't a bespoke patch; it falls directly out of the guiding principle above: a
middleware occupying both table columns needs a single source of truth for its spec
contribution, exactly like paired Security already has one.

## Resolved design decisions (previously N1-N6, now closed)

A dedicated review traced this design's proposed dispatch points against the REAL
code they would hook into (`adapters/nethttp/serve.go`, `adapters/nethttp/client.go`,
`adapters/mqtt5/adapter.go`) and found six open questions beyond SSE/PathPrefix/
third-party-extensibility above. ALL SIX are now resolved — reconsidered once more,
explicitly, under the freedom to make breaking changes where it improves
maintainability (uncomplicated here: this is all still-unimplemented design, so
"breaking" only means picking the best shape, not migrating real callers).

**D1 (was N1/N7) — pre-handler-only timing: RESOLVED, stays pre-handler-only.**
Confirmed: `Transform`'s ENTIRE dispatch (`DecodeIn` → call `fn` → `EncodeOut`) is
placed at the SAME point `runSecurityMiddlewareReflect`/`runSubscribeSecurityImpls`
already run — BEFORE the actual handler is invoked, unconditionally, mirroring the
already-proven security-shaped Fn's own timing exactly. **Decision: no second,
post-handler attachment point** — no worked example in this doc needs post-handler
`Resp` access, and a concern that genuinely does already has a mechanism: the
EXISTING wrapping-shaped Fn (`func(http.Handler) http.Handler`) wraps the ENTIRE
dispatch, handler included. Adding a second attachment point now would be designing
past a driver that doesn't exist — this repo's own established discipline. Applies
identically to REST and events (events' subscribe side has the identical
constraint; publish has no handler to depend on, so it's unaffected).

**D2 (was N2) — `ErrorPattern` integration: RESOLVED, `fn` errors ARE eligible.**
Confirmed: `runSecurityMiddlewareReflect`'s own Fn errors always produce a FIXED
`http.StatusUnauthorized` + `rest.SecurityError{Err: err}`, never routed through the
route's own declared `ErrorPattern`/`ErrorStatus` rules. **Decision: a `Transform`/
`ClientTransform` `fn` error is now run through `handle.ErrorResponseFor(err)` FIRST**
(confirmed generic enough to reuse — the SAME mechanism a handler error already
uses) — a middleware's own business error (e.g. "malformed API key") can be matched
against a declared `ErrorPattern` exactly like a handler error can. Only when NO
pattern matches does it fall back to a NEW, dedicated error type —
**`rest.MiddlewareError{Name string, Err error}`**, default status 400 — replacing
the previous "reuse `SecurityError`" approach, which was semantically wrong for a
non-security middleware (a cookie-policy middleware's business error mislabeled as
a "security" failure). `rest.SecurityError` remains reserved for the EXISTING
`middleware.SecurityScheme` mechanism, unchanged. **Events mirror, confirmed
extendable (not REST-only)**: `api/events.ChannelHandle.ErrorResponseFor` ALREADY
EXISTS (`api/events/error_pattern.go`), mirroring REST's `ErrorResponseFor` exactly
— `events.Transform`'s (subscribe) `fn` error is matched against a declared
`ErrorChannel` pattern FIRST, the SAME way, falling back to a NEW
`events.MiddlewareError{Name string, Err error}` (mirrors `rest.MiddlewareError`)
when unmatched. `events.ClientTransform` (publish) errors get the same treatment if
and when events' publish path gains its own error-response concept — not a blocker
to stating the RECEIVING (subscribe) side's resolution now.

**D3 (was N3) — client-side precedence: RESOLVED, three tiers.** Confirmed:
`adapters/nethttp/client.go`'s `overrideDerived` establishes a clean 2-tier
precedence today — explicit `CallOptions` always wins over the route's own
Req-derived merge values. **Decision: explicit `CallOptions` > middleware-derived
(`ClientTransform`'s `In`) > route-own-derived** — middleware is the MORE SPECIFIC,
LATER-ATTACHED concern (`ClientTransform` chains onto an already-built `Route`
value), mirroring this doc's OWN already-established server-side rule for response
header/cookie conflicts ("registration-order, last-applied-wins," §5) applied
symmetrically to the client side. Fills the previously-unstated middle tier.
**Events mirror**: the SAME 3-tier PRINCIPLE (explicit > middleware-derived >
channel-derived) applies to `events.ClientTransform`'s publish side — confirmed
events' publish adapters already have their OWN explicit-override mechanism (e.g.
`adapters/mqtt5/binding.go`'s `Vars map[string]string`) to sit at the TOP tier,
mirroring `CallOptions`'s role. Exact per-adapter wiring (whether `mqtt`(v3)/
`zeromq` expose the same override shape) is an implementation detail, same category
as REST's own "exact `Out`-return-shape" detail (§5) — not a design blocker.

**D4 (was N4) — mandatory implementation requirement, confirmed, not a decision
with two answers.** A separate review round found and fixed a real, shipped bug:
`applyParamDeclarations`'s layering loop could double-append a param name agreed
upon by 2+ LEGACY `middleware.Middleware` values, absent a guard tracking
already-added names (not just manually-declared ones). §3's "layering" mechanism
(`Middleware[In,Out]` retaining full param structs, feeding the SAME conflict-
detection pass) MUST extend the SAME already-added-names guard to cover
contributions from MULTIPLE `Middleware[In,Out]` instances (or a `Middleware[In,Out]`
alongside a legacy `middleware.Middleware`) agreeing on the same param name — stated
here as a MANDATORY requirement for whoever implements §3, not an open question.

**D5 (was N5) — Observer/stats integration: RESOLVED, the new mechanism improves on
the legacy silence.** Confirmed: `runSecurityMiddlewareReflect` itself calls no
observer at all for its OWN Fn errors today (a pre-existing gap in the legacy
mechanism, not introduced by this design). **Decision: `Transform`/`ClientTransform`
call `stats.ReportErrors(obs, "middleware:in", err)` on an `In`-decode failure
(`MiddlewareInputError`) and `stats.ReportErrors(obs, "middleware:fn", err)` on the
`fn`'s own returned business error** — distinct location strings for the two
failure kinds, mirroring this codebase's existing `stats.ReportErrors(obs, "path",
err)`-style call sites elsewhere. No backward-compat cost to doing this properly,
since this is entirely new code — deliberately NOT mirroring the legacy mechanism's
silence. **Events mirror, confirmed NOT REST-specific**: `stats.Observer`/
`stats.ReportErrors` are ALREADY pattern-agnostic (used identically across REST and
events elsewhere in this codebase) — `events.Transform`/`events.ClientTransform`
get the SAME `stats.ReportErrors(obs, "middleware:in"/"middleware:fn", err)` calls,
no adaptation needed beyond calling the same function from events' own dispatch
points.

**D6 (was N6) — multiple `Middleware[In,Out]` attached to ONE route/channel:
RESOLVED, three sub-cases.**

- **(a) Two different `Middleware[In,Out]` values both reading the SAME header name
  into their OWN, independent `In` types — VALID, no conflict.** Each decodes its
  OWN typed copy from the same underlying raw value; no shared mutable state, no
  ambiguity. This is a DIFFERENT case from §3's spec-layering conflict-detection
  (which is about DECLARED SPEC METADATA agreement, not independent runtime reads)
  — no new check needed.
- **(b) `Declaration.Name` uniqueness — ENFORCED, per attaching route/channel.**
  Reconsidered explicitly under the freedom to diverge from the legacy mechanism's
  looseness (`middleware.ServerImplementation.Name`/`ClientImplementation.Name` are
  NEVER uniqueness-checked today) — for THIS brand-new mechanism, where `Name` now
  matters more (D5's Observer attribution; D2's `MiddlewareError.Name`), enforcing
  uniqueness is a genuine maintainability win: it catches a real copy-paste mistake
  (accidentally attaching the same `Declaration` twice, or two different
  declarations sharing a `Name`) at Register/Handle time instead of silently
  producing ambiguous observability/error data. New error:
  **`rest.DuplicateMiddlewareNameError{Route string, Name string}`** (events mirror:
  `events.DuplicateMiddlewareNameError{Topic string, Name string}`), checked in the
  SAME resolution pass that already collects `mw` contributions for spec-layering
  (§3) — no new pass, just an added check in the existing one. The LEGACY
  `middleware.ServerImplementation.Name`/`ClientImplementation.Name` remain exactly
  as they are — this enforcement applies ONLY to the NEW `Middleware[In,Out]`/
  `Declaration` mechanism, not retroactively to the old one.
- **(c) Two middlewares both WRITE to the SAME `*Req`/`*T` field via enrichment —
  attachment-order, last-applied-wins, NOT flagged as a conflict/error.** Mirrors §5's
  already-established response header/cookie precedent, applied consistently here
  too. Not a compat question either way — a fundamental Go-language limitation
  (arbitrary runtime field writes inside a closure can't be statically tracked
  without disproportionate reflection machinery) independent of what's safe to
  break. A legitimate use case (two independent enrichment steps, one a fallback for
  the other) isn't necessarily wrong either — no error, just a defined order.

**Explicitly out of scope for this "ready to implement" pass**: M2/M3, from an
EARLIER, separate critical review of the ALREADY-SHIPPED legacy mechanism (Fn
fail-fast ordering; events' `CheckCoverage` eager-vs-deferred timing) — a different
topic (existing, shipped code behavior), tracked as its own separate SQL todos, not
bundled into this design's readiness.

### Test plan (once implementation begins)

- `middleware.Declaration`/`NewDeclaration` — construction + codec validation.
- `rest.Middleware[In,Out]`/`events.Middleware[In,Out]` — merge-field registration
  tests mirroring `RouteHandle`'s/`ChannelHandle`'s own existing merge-field tests.
- `rest.Transform`/`events.Transform` (RECEIVING role, both packages) —
  registration + dispatch: happy path, In-decode failure (`MiddlewareInputError`), fn
  error (short-circuits before handler/delivery), Out response-merge composing
  correctly alongside the route's own merge fields (REST only — events' Out is
  unused on subscribe, see §8).
- `rest.ClientTransform`/`events.ClientTransform` (SENDING role, both packages) —
  registration + dispatch: happy path (fn's produced value correctly encoded into
  outgoing headers/cookies/query or topic vars, merged alongside the route/channel's
  own derived values with correct precedence), fn error (aborts before any network/
  publish activity), Out decode from the response composing correctly (REST only —
  events' publish has no response to decode from).
- Backward-compatibility regression: an EXISTING `.Use(securityScheme)` call site
  continues to pass unchanged after `Use`'s parameter type widens to
  `middleware.RouteMiddleware`.
- A test attaching the SAME `Middleware[In,Out]` value via BOTH `Transform` and
  `ClientTransform` on two different `Route`/`Subscriber`/`Publisher` values (the
  API-key worked example) — confirming the "one declaration, two attachment points"
  contract end-to-end.
- Route/channel-AGNOSTIC (`.Use(mw)`) coverage: a `Middleware[In,Out]` carrying a
  `WithReceive`/`WithSend` Fn, attached verbatim (the LITERAL same `mw` value) to
  TWO OR MORE routes with DIFFERENT `Req` types, confirming both routes dispatch
  correctly and independently — the test that actually proves route-agnostic reuse,
  not just that the mechanism compiles. Repeat for `SSERoute.Use`/
  `Subscriber[T].Use`/`Publisher[T].Use` to confirm the SAME reuse across all
  attachment surfaces (the point of this round's SSE/events migration).
- `rest.TransformSSE`/`rest.ClientTransformSSE` — the SAME test shapes as
  `Transform`/`ClientTransform` above, PLUS: `Out` response-merge composing
  correctly alongside SSE's own headers (`Content-Type: text/event-stream` etc.,
  confirmed set AFTER middleware dispatch in `serve_sse.go`); `ClientTransformSSE`'s
  `Out` decoding ONCE at connection-open time, confirmed NOT re-decoded per event.
- A test confirming `req *Req`/`msg *T` ENRICHMENT — a value written by `fn` into
  `*req`/`*msg` is visible to the actual handler afterward, unchanged from whatever
  `fn` set it to (the core new capability this design round added; not previously
  named as its own test case).
- D6(c): two attached middlewares both writing the SAME `*Req`/`*T` field —
  confirms the RESOLVED rule (attachment-order, last-applied-wins, no error).
- D2: a middleware `fn` error matching a declared `ErrorPattern` produces the
  PATTERN's structured response (not a generic status); a `fn` error matching NO
  pattern falls back to `rest.MiddlewareError` with status 400. Repeat for
  `events.Transform`/`ErrorChannel`/`events.MiddlewareError`.
- D3: explicit `CallOptions` wins over `ClientTransform`-derived values, which in
  turn win over the route's own req-derived values, for the SAME header/cookie/
  query name — all three tiers exercised in one test. Repeat for
  `events.ClientTransform`'s publish-side precedence (explicit adapter Vars >
  middleware-derived > channel-derived).
- D5: `stats.ReportErrors(obs, "middleware:in", err)` called on `In`-decode failure;
  `stats.ReportErrors(obs, "middleware:fn", err)` called on the `fn`'s own error.
  Repeat for `events.Transform`/`ClientTransform`.
- D6(b): attaching two `Middleware[In,Out]` values with the SAME `Declaration.Name`
  to one route/channel (via any combination of `.Use()`/`Transform`/
  `ClientTransform`) returns `rest.DuplicateMiddlewareNameError`
  (`events.DuplicateMiddlewareNameError` for events) at Register/Handle time.
- D7: a `Middleware[In,Out]` carrying a bundled `WithReceive`/`WithSend` Fn, ALSO
  passed to `Transform`/`ClientTransform`, returns
  `rest.AmbiguousMiddlewareAttachmentError` at Register/Handle time; a `mw` used
  ONLY via `.Use()` (bundled) or ONLY via `Transform`/`ClientTransform` (bound)
  succeeds normally, confirming the rejection is specific to COMBINING both styles
  on one value, not either style alone.

### Deferred, not part of this design

- Refactoring `examples/rest-api`'s `ResponseDepositor`/`chiResponseDepositor`/
  `nethttpResponseDepositor` to use this mechanism — a separate, later round, once
  this ships.
- A full `ports.Middleware[In,Out]`/`.Use()` implementation for `ports` — see
  "Feasibility of a full `.Use()`-based `ports.Middleware[In,Out]`" above for the
  grounded analysis (confirmed reusable building blocks, the Pattern-bound vs.
  Pattern-less split, the `ctx`-parameter cost, and SQL's metadata-only exception).
  Out of scope for THIS implementation round, but no longer just a placeholder.
- SSE is NO LONGER deferred (see "SSE" above) — `SSERouteHandle`'s new response
  merge fields, `TransformSSE`/`ClientTransformSSE`'s implementation, and
  `consumeSSE`'s new recognized Fn shape are all IN SCOPE, sketched at the
  confirmed dispatch gap-points. What remains genuinely deferred: the EXACT
  mechanical hook for `ClientTransformSSE`'s Out-decode-once-at-connection-open
  timing (e.g. whether `consumeSSE` needs restructuring to expose a distinct
  "connection opened" callback) — sketched in principle only, not in full
  mechanical detail.
- Per-adapter wiring details for `events.ClientTransform`'s "additional topic vars"
  mechanism beyond mqtt5's confirmed `UserProperty`-attachment precedent (whether
  `zeromq`/`mqtt`(v3) have an equivalent attachment point for OUT-side data) — a
  mechanical per-adapter detail, not a design blocker, to resolve during
  implementation.
- `PathPrefix`/`TopicPathPrefix`'s own composition semantics for multiple attachments
  (error on conflict, concatenate, last-wins) — sketched under "Extending the model,"
  explicitly not resolved, deferred until a concrete driver surfaces.
- Whether the generalized spec-layering attachment mechanism should ever be EXPORTED
  for third-party custom middleware types, or stay closed/sealed to api/rest's/
  api/events' own shipped types ("Extending the model") — leaning sealed, not decided.
- Adding `middleware.EnsureContextFields`-equivalent wiring to pub/sub's own dispatch
  — needed only if `events.Transform`'s `fn` wants to publish a value for the
  actual subscribe handler to read, mirroring REST's already-free `ContextField`
  composition (§9). Not needed for anything else this design ships.

## Relationship to other roadmap docs

- **Supersedes** [Common-Base + Per-Pattern-Derived Middleware Types](../roadmap/common-middleware-architecture.md):
  that doc's core finding (a single shared `middleware.Middleware` struct carrying
  REST-only fields unused by `api/events`/`api/reqreply`) is what this design fixes —
  but via an ADDITIVE marker interface + NEW per-pattern generic types
  (`rest.Middleware[In,Out]`/`events.Middleware[In,Out]`), not that doc's original
  proposal to retrofit/split `middleware.Middleware` itself (which would have been
  breaking). `middleware.Middleware`/`SecurityScheme` remain completely unchanged and
  continue to work exactly as before, side by side with this new mechanism.
- **Update — relationship RESOLVED, via a confirmed 4-stage lifecycle
  model.** An earlier version of this section described
  [Feature](../roadmap/protocol-native-features.md) (then titled
  "Protocol-Native Feature Declarations," later "Feature/Provider") as a
  separate, complementary axis — opaque protocol capability flags (MQTT5
  Shared Subscriptions, Message Expiry, ZeroMQ Conflate/HWM) evaluated via
  adapter type-switching, distinct from THIS doc's structured,
  codec-backed Input/Output data. That framing went through TWO further
  rounds — a "subsume this doc's mechanism" conclusion (reached against an
  open, string-ID-based primitive later REJECTED for compile-time-safety
  reasons), then reopened — before landing on its CURRENT, confirmed
  resolution: `Middleware[In,Out]` and the sealed, per-adapter `Capability`
  mechanism both occupy the SAME declare-time, spec-contributing lifecycle
  stage, WITHOUT merging into one Go type — each keeps its own distinct
  runtime-enforcement path. See that doc's §3 for the full resolution and
  its supporting prototype evidence. THIS doc remains the accurate,
  unchanged description of SHIPPED code; the roadmap doc's chosen
  direction is idea-only, not yet implemented, and any actual migration
  is deferred to a separate implementation-planning round.
- Distinct from the ALREADY-SHIPPED REST/events `HandleMW`/`ClientMW`/
  `SubscribeMW`/`PublishMW` security+observer+general-purpose split
  (`docs/design/d-0001-rest-middleware-workflow-simplification.md`/
  `docs/design/d-0002-pubsub-workflow-simplification.md`) — a different,
  already-resolved topic, untouched by this design.
  `docs/roadmap/declarative-middleware.md` (the doc that ORIGINALLY
  proposed that split, before d-0001/d-0002 shipped it) has since been
  trimmed to its own remaining MCP/ports scope, which THIS design's own
  "Feasibility" section above builds directly on.

## Next steps

> **Historical note**: items 1-4 below describe the ORIGINAL rollout plan
> (REST → SSE → events) — all shipped, in this order, confirmed via the
> "Addendum: `api/reqreply` and `api/events`' property axis..." section
> below, which covers the LAST major follow-on round (bringing
> `api/reqreply` up to this same parity, plus 2 bug fixes discovered in
> events' already-shipped mechanism). Item 5 (`ports` middleware) remains
> future work, unaffected by any of this.

1. Implement in the order: `middleware.Declaration`/`RouteMiddleware` (package
   `middleware`) → `rest.Middleware[In,Out]` + BOTH `Transform` (server, bound) AND
   `ClientTransform` (client, bound) together, PLUS `WithReceive`/`WithSend` +
   `.Use(mw)`'s widened dispatch (route/channel-agnostic) (package `api/rest`, with
   adapter wiring in `adapters/nethttp`/`adapters/chi` server dispatch AND
   `adapters/nethttp/client.go` client dispatch, covering BOTH attachment styles)
   → `rest.TransformSSE`/`ClientTransformSSE` + `SSERoute.Use`'s widened dispatch
   (SSE, no longer deferred — see "SSE" above), including `SSERouteHandle`'s new
   response merge fields and `consumeSSE`'s new recognized Fn shape → RESOLVED
   naming: `events.Middleware[In,Out]` + BOTH `events.Transform` (subscribe) AND
   `events.ClientTransform` (publish) together, PLUS `Subscriber[T].Use`/
   `Publisher[T].Use`'s widened dispatch (package `api/events`, with adapter wiring
   in whichever pub/sub adapters need it, starting with `mqtt5` which already has
   the confirmed `UserProperty`-style precedent). **Sequencing, explicit**: REST
   plain `Route` FIRST (it has the concrete cookie-attrs AND API-key drivers) →
   REST `SSERoute` SECOND → events THIRD. SSE precedes events (not merely listed
   first by convention) for a concrete, code-grounded reason: `TransformSSE`/
   `ClientTransformSSE` live in the SAME `api/rest` package as `Transform`/
   `ClientTransform`, and SSE's adapter wiring lives in the SAME adapter files
   (`adapters/nethttp/serve_sse.go`, alongside `serve.go`) — reusing infrastructure
   just built for plain `Route`, zero new package. Events requires an ENTIRELY
   SEPARATE package (`api/events`) built from scratch, PLUS separate adapter
   packages (`mqtt5`/`mqtt`/`zeromq`) — a genuinely bigger increment, best
   attempted once `Route`'s (and SSE's) pattern is proven out, not interchangeably
   with SSE. Ship each pattern's receiving AND sending role TOGETHER, not
   staggered, AND ship BOTH attachment styles (`Transform`-family and `.Use()`)
   together per surface — shipping only one half is exactly the asymmetry earlier
   review rounds found and corrected, twice now (roles, then attachment styles).
2. Full verification each step: `gofmt`/`go build`/`go vet`/`go test`/`just check`/
   `just examples`.
3. Update `.github/instructions/go-codex.instructions.md` and fold into
   `docs/design/d-0001-rest-middleware-workflow-simplification.md` (REST, including
   SSE) and `docs/design/d-0002-pubsub-workflow-simplification.md` (events) as new
   Addenda.
4. Revisit `examples/rest-api`'s `ResponseDepositor` once REST's half ships.
5. `ports` (File/Cache/Dir/SQL) middleware remains its own, separate, later
   implementation effort — see "Feasibility of a full `.Use()`-based
   `ports.Middleware[In,Out]`" above; UNAFFECTED by this round's REST/SSE/events
   naming and attachment-style resolution, since it was already scoped as future
   work, not blocked on any decision made this round.

## Addendum: `api/reqreply` and `api/events`' "property" vocabulary axis, bringing both up to full parity with this design

Added after `docs/roadmap/reqreply-codec-declared-middleware.md` shipped
(19 review rounds, 8 numbered design decisions, implemented in a later
session as a 13-phase, two-track parallel effort) — recorded here so the
lineage is discoverable from this document, since the newer feature's own
roadmap doc did not itself narrate where its core mechanism came from. That
roadmap doc has SINCE BEEN DELETED (per its own 3-way delete/keep/promote
graduation policy — a single-feature roadmap doc, fully shipped, with a
confirmed zero-gap review, and no lasting cross-cutting design value of its
own beyond what is captured here); this addendum is the durable record of
the design lineage that remains after that deletion.

### What shipped

Before this round, `Middleware[In,Out]` (REST/events, per D1-D7 above) only
had a "topic"/"path" vocabulary axis (`WithRequestTopic`/`WithResponseTopic`
for reqreply, channel-var equivalents for events) — no way to declare a
merge field against MQTT5 User Properties/AMQP-style headers, "named
metadata separate from payload." This round added a SECOND, orthogonal
vocabulary axis, applied identically to BOTH `api/reqreply` (new package,
brought up to D-0003 parity for the first time in this round) and
`api/events` (already D-0003-compliant for the topic axis; this round added
property-axis parity):

- `PropertyParam`/`MergedPropertyParam[T]` (per-package, duplicating
  `TopicParam`'s existing shape exactly — same reasoning as `TopicParam`
  itself: two independently-defined thin wrappers over the SAME shared
  `codex.Param`/`MergedParam[T]`/`NewParam[T,V]` primitives, not one
  cross-package type).
- `NewPropertyParam[T,V]` (required) / `NewOptionalPropertyParam[T,V]`
  (optional — a NEW capability added to the shared `codex` primitives'
  surface, via `codex.OptionalField`, which already existed internally but
  was never exposed through `NewParam`'s API before this round).
  `Required bool` lives on `PropertyParam`/`MergedPropertyParam[T]`
  themselves (mirrors `rest.HeaderParam.Required` — NOT added to shared
  `codex.Param`, keeping `TopicParam`'s own no-Required-field design
  untouched).
- `WithRequestProperty`/`WithResponseProperty` (reqreply),
  `WithSubscribeProperty`/`WithPublishProperty` (events) — new
  `Middleware[In,Out]` attachment methods, alongside the existing topic-axis
  ones.
- `buildDecodeIn`/`buildEncodeOut` extended to take TWO SEPARATE map
  parameters (topic vars, property vars) — never combined into one map,
  mirroring REST's own real `buildDecodeIn(headerVars, cookieVars,
  queryVars map[string]string)` multi-axis pattern exactly (confirmed
  against REST's real code, not assumed).
- AsyncAPI spec rendering: the property axis's contributions unify into
  Phase 1b's existing `applyParamDeclarations` mechanism for reqreply
  (events renders its own standalone contribution, since it had no
  pre-existing flat mechanism to unify with); `Required` correctly
  propagates into the rendered schema's `required` array, not just runtime
  validation.
- Conflict detection: a NEW, uniform algorithm applies to ALL contributions
  (reqreply's flat Phase-1b mechanism AND the new axis; a brand-new
  `events.ConflictingParamContributionError` + `checkEventsParamConflicts`
  for events, which had zero pre-existing conflict machinery at all).
  Topic-vars and properties are tracked in INDEPENDENT namespaces (never
  cross-checked against each other — a stronger boundary than REST's own
  looser header/cookie/query shared-namespace precedent, justified by the
  two axes coming from genuinely different wire locations). Contributions
  additionally compare `Schema` (via `reflect.DeepEqual`, nil-vs-non-nil
  treated as a mismatch) — a deliberate DIVERGENCE beyond REST's own real
  `checkParamConflicts`, which never compares codecs at all.

### Accepted breaking change (deliberate, not silently introduced)

Unifying conflict-detection onto ONE uniform algorithm retired reqreply's
Phase 1b's OLD silent-first-seen-wins dedupe behavior for MISMATCHED
declarations sharing a name — two contributions disagreeing on
`Required`/codec now ALWAYS error, regardless of origin (Phase 1b's flat
mechanism or the new axis). Scope of the break is narrow: only routes with
two PRE-EXISTING Phase-1b-only declarations for the same property name with
differing `Required`/codec (previously silently tolerated) would newly fail
to `Register`. Chosen deliberately over preserving the old laxer behavior —
"one algorithm, one mental model" was judged simpler and more maintainable
than tracking contribution origin through the comparison, even at the cost
of a breaking change to already-shipped behavior.

### Write-side wiring — the most significant implementation finding

Tracing property values all the way to the wire (not just the type-level
API) surfaced a REAL, pre-existing capability gap: `adapters/mqtt5`'s
reqreply server-reply path had ZERO mechanism to write ANY outgoing User
Property onto its own reply message (`ServeOptions.UserPropertyParams` was
validate-only, checked against the INCOMING request only). This round added
the missing capability — both success-reply and error-reply `.Publish(...)`
call sites in `adapters/mqtt5/reqreply_transport.go` now write
`propertyVars` from `WithResponseProperty` into the outgoing
`PublishProperties.User`. The client-request side and events' publish side
both had existing write-targets already (`t.opts.UserProperties`/
`PublishOptions.UserProperties`) and only needed wiring, not new capability.

### 2 bugs found and fixed in events' ALREADY-SHIPPED D-0003 mechanism (pre-existing, not introduced by this round)

Discovered while scoping full reqreply/events parity, both confirmed real
via code tracing, fixed in the same round since it already touched the
exact code paths:

1. **Value-precedence bug** — events' publish-side
   (`adapters/mqtt5/adapter.go`/`adapters/zeromq/adapter.go`, identical
   pattern in both) had channel-own-derived vars winning over
   middleware-derived vars — the OPPOSITE of this doc's own D3 (explicit >
   middleware-derived > route/channel-own-derived, confirmed via REST's
   real `overrideDerived` chain, and via this doc's own "Test plan" wording
   verbatim). Fixed via a minimal `isExplicitVars bool` flag through
   `publish()`'s call chain (simpler than an earlier 2-map-parameter sketch
   — the explicit-vs-channel-own cases are already mutually exclusive at
   the call site).
2. **Missing Observer integration** — events' `dispatchSubscribeMiddlewareHandlers`/
   `dispatchPublishMiddlewareHandlers` (`adapters/mqtt5/transform_dispatch.go`)
   had ZERO `stats.ReportErrors` calls for middleware decode/business-error
   failures, contradicting this doc's own D5 (which explicitly states
   "Events mirror, confirmed NOT REST-specific"). Fixed: both now call
   `stats.ReportErrors(obs, "middleware:in"/"middleware:fn", err)`,
   matching REST's real call sites exactly.

### Validation

A dedicated post-implementation gap review (comparing the roadmap doc's
"Files to create" table and all 54 planned unit tests against the real,
shipped codebase) found and fixed 4 residual gaps (a missing unit test, a
missing `docs/features/reqreply-middleware.md` page, 2 stale
cross-references in companion roadmap docs) — confirmed CLOSED, zero
functional gaps remained in shipped code. Full verification (`go build
./...`, `go test -count=1 ./...` repo-wide, `just check`, `gofmt -l .`) —
all clean.
