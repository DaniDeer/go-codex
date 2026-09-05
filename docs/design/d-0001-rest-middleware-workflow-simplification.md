# Middleware Workflow — Current-State Audit & Simplification Proposal

> **Status:** FULLY IMPLEMENTED — `middleware`, `api/rest`,
> `adapters/nethttp`, `adapters/chi` all ship the unified `HandleMW`/
> `ClientMW`/`Serve`/`ServeSSE`/`ServeOne`/`Call`/`CallWithHandle` design
> described below, PLUS the typed `RequestParams`/`ResponseParams` fields
> (`HeaderParamSpec`/etc. in `middleware`, `FromHeaderParam`/etc. bridges
> in `api/rest`) and the generalized spec-declaring middleware
> constructors these same bridge functions also satisfy; every
> non-example package plus all ~60 examples build, `go vet`, and
> `go test` clean, and every example runs to completion. **Old-door
> removal — DONE.** The OLD, pre-redesign `Handler`/`Register`/
> `SSEHandler`/`RegisterSSE` per-route functions have been DELETED from
> both `adapters/nethttp` and `adapters/chi`; `Serve`/`ServeOne`/
> `ServeSSE` are now the SOLE public server-side entry points, with zero
> capability lost — see "Decision: `Serve` is the only public
> server-side entry point" below, updated to reflect the final state.
> Closing this required fixing two genuine gaps discovered mid-migration
> in `Serve`/`ServeSSE`'s reflect-based dispatch (request/response
> `Formats` content negotiation, including streaming formats; full
> `ErrorPattern` response building via `ErrorResponseFor` on the
> handler-error path) — both are now fully implemented, so no
> capability regression exists relative to the old `Handler`/`Register`
> doors. `RouteHandle.WithHandler`/`SSERouteHandle.WithHandler` were
> added as `Route.WithHandler`'s post-registration equivalents, for
> routes whose handler depends on runtime state unavailable at
> package-level var-init time (see `examples/sensor-service`).
> **Also explicitly OUT of scope** (separate roadmap docs, not
> implementation gaps of THIS doc): Events/ReqReply/Ports-beyond-
> pattern-building and SSE CLIENT consumption — see the sibling docs
> cross-referenced at the bottom of this summary. Started as a factual
> audit of the SHIPPED workflow (post [Declarative
> Middleware](../roadmap/declarative-middleware.md) "Revision 2 — the declare/
> implement split"); all 6 open questions raised during that audit have
> since been worked through collaboratively and resolved into 5 concrete
> decisions (a 6th confirmed the status quo as correct, no change).
> **Scope: REST (`api/rest`/`adapters/nethttp`/`adapters/chi`) only** —
> `api/events`'s own equivalent shipped separately (see
> [Pub/Sub Workflow Simplification](d-0002-pubsub-workflow-simplification.md));
> `api/reqreply`'s equivalent is designed in
> [ReqReply Workflow Simplification](../roadmap/reqreply-workflow-simplification.md),
> deliberately deferred until THIS doc's implementation ships.
> The "Step 1–4"/"escape hatches" sections below describe the OLD, currently-
> shipped workflow being replaced; the "## Decision: ..." sections
> describe the NEW, agreed design. [← Back to Design Documents](index.md)
>
> **Summary of the 6 questions' decisions:** (1) whole-API declarative
> wiring — `Route.HandleMW`/`.WithHandler()` chain into
> `Route.Register(builder) error` (no more `*RouteHandle` returned), a
> new `nethttp.Serve(mux, builder)`/`ServeSSE` wire the whole API in one
> call; (2) closed as a side effect of (1) — `HandleMW(mw, fn)` takes the
> actual declared `mw` value, closing a typo-risk gap; (3) symmetric
> client-side wiring — `Route.ClientMW(mw, fn)` + a single
> `nethttp.Call(ctx, caller, route, req, opts)` replace
> `ClientMiddleware`/`UseClient`/`Call`/`CallHandle`/`CallVia`/
> `CallHandleVia` entirely; (4) `Serve`/`ServeSSE` become the ONLY public
> server-side entry points — `Handler`/`Register`/`SSEHandler`/
> `RegisterSSE` are removed; (5) closed as a side effect of (3) — the four
> client entry-point functions collapse into one; (6) confirmed the
> `any`-typed `Fn`/`fn` design is the correct tradeoff, not a gap — no
> change.
>
> **Then a second "compromises" evaluation pass** re-checked every one of
> the 12 escape hatches against decisions 1–4, surfacing two
> underestimated compromises and three further elimination
> opportunities, ALL now resolved: `FromSecurityScheme` bridges manual
> security declarations into the new mechanism, AND `rest.WithSecurityScheme`
> is removed entirely (redundant once the bridge exists) — the ONLY
> escape hatch actually eliminated outright, not just replaced; an
> unexported `callWithHandle` primitive keeps `ports`' high-throughput
> call pattern regression-free with zero new public API; `Middleware`'s
> `RequestParams`/`ResponseParams []any` become typed fields, eliminating
> `ParamContributionShapeError` at compile time; and a new `.Implement()`
> chainable method + `Wrap`/`Transform` constructors make general-purpose
> (empty-`Satisfies`) implementations explicit and give them a simpler
> attachment path than security's `.Use()`+`.HandleMW()` pairing (this
> also fixed a genuine bug found later in an earlier draft of this same
> idea — see "Decision: `.Implement()`..." below).
>
> **Every follow-on design question raised across all decisions has since
> been resolved** (`Serve`'s whole-builder failure semantics — strict
> fail-fast, routes without `.WithHandler()` skipped silently;
> `Options` → per-route `.WithOptions()`; `.ClientMW()` validation timing
> — inside `Call`, matching `Serve`; `chi`'s client-side story — none,
> permanently; single-route test ergonomics — new `ServeOne` convenience
> wrapper; `ValidateRoute` — kept unchanged, a clean two-tier split with
> `ServeOne`). Zero open design questions remain from that pass.
>
> **A further review round** ("Review round: PathParams/HeaderParams/
> CookieParams and middleware chaining") confirmed route-level merge
> fields, middleware-contributed validate-only params, and the existing
> `*Req`-pointer chaining/`codex.DecodeVars` mechanism are ALL fully
> preserved and unaffected by Decisions 1–8 — "one struct, one call"
> holds unchanged at the handler boundary. One genuine gap was found (no
> conflict detection for two chained `Fn`s writing the same `Req` field)
> and deliberately left undetected, for principled reasons (would
> reintroduce the reflective/type-erased machinery just eliminated
> elsewhere) — closed via a documented naming/`ContextField` convention
> instead of enforcement.
>
> **A final review round** across the three middleware use-case
> categories (security / observer / general custom middleware) found a
> genuine bug in the ORIGINAL `middleware.Wrap` decision (returned the
> wrong type — `Middleware` instead of `ServerImplementation`) and used
> it as the occasion to generalize: a NEW `.Implement(impl)` chainable
> method gives declare-time-content-free implementations (observability,
> `Req`-cleansing/transformation via the new `Transform` constructor) a
> simpler attachment path than security's `.Use()`+`.HandleMW()` pairing,
> since there's nothing for them to "match." Also generalized
> `nethttp.HeaderParam` (previously framed as part of the API-key story)
> into the core `middleware` package, alongside its missing
> `CookieParam`/`QueryParam`/`ResponseHeaderParam`/`ResponseCookieParam`
> siblings — confirmed these need NO coverage check, a fundamentally
> different risk profile than security (worst case: a validated-but-
> unused declaration, not a security hole).
>
> **A final critical review pass — immediately before implementation —**
> found and resolved 8 concrete issues (3 Critical, 2 High, 2 Medium, 1
> Low): `ServeOne`'s stray `Options` parameter (contradicted the
> `.WithOptions()` decision, dropped); `Serve`'s missing duplicate
> Method+Path detection (a REAL `mux.Handle` panic risk, closed via a new
> `DuplicateRouteError` + defensive `recover()`); `Transform`'s Fn shape
> clarified to WRAP into the existing security shape rather than
> introduce a third one; a `HandleMW`-to-`.Use()` pairing check folded
> into `Register`'s existing merge pass (order-independent, zero
> ergonomics cost); the ctx pre-allocation checklist broadened from
> `ContextField` alone to all FOUR ctx keys `Handler`'s current body sets
> up; `SSERoute`'s full chainable method set spelled out explicitly
> (spinning SSE CLIENT consumption — confirmed to not exist anywhere in
> go-codex today — into its own new SSE Client Consumption roadmap doc,
> since shipped and removed — see this doc's own addendum below);
> `Server` gains
> an internal mutex for concurrent `Register` calls (an explicit first
> step toward, not a solution to, the separately-tracked [Dynamic Port
> Rebinding](dynamic-port-rebinding.md) gap); and `MultiRouteError`'s
> exact shape finalized (`Unwrap() []error`, Go 1.20+ multi-error
> support). Zero open items remain from this pass either.
>
> **A second, fresh full read-through — checking specifically for
> inconsistencies introduced by the pass above — found 3 more items**:
> the `HandleMW`-pairing fix was SIMPLIFIED from per-`mw` bookkeeping to
> a reverse-Satisfies check (a sibling to `CheckCoverage`, needing zero
> new bookkeeping and correctly covering `.Implement()`-attached misuse
> too); `SSERoute.Register`'s merge-pass parity with `Route.Register`'s
> was made explicit (it shares every check, including the reverse-
> Satisfies one); and the SAME `mw`-mismatch ambiguity was found on
> `ClientMW` too — resolved by giving client-side implementations their
> OWN `Satisfies` concept via a NEW, EXPLICIT
> `middleware.ClientImplementation{Name, Satisfies, Fn}` type (the TRUE
> successor to the removed `ClientMiddleware`, not just "removed with
> nothing replacing it"), gating WHICH implementations `Call` runs
> (mirroring server-side gating exactly) — a correctness improvement
> that PREVENTS the mismatch rather than merely detecting it. Zero open
> items remain from this pass either — except one EXPLICITLY-DEFERRED
> question (does the client side also need its own `.Implement()`-
> equivalent for truly declare-time-content-free implementations,
> flagged but NOT decided as a side effect of this pass).
>
> **A FOURTH pass found and closed the deferred question above, via a
> bigger unification than first planned.** Investigating `HandleMW`/
> `ClientMW` for a genuine, previously-undocumented nil-pointer-panic
> risk (`mw.Security.SchemeName` accessed with no nil guard) led to a
> better fix than a nil check alone: `mw` becomes a NILABLE
> `*middleware.Middleware` on BOTH methods, and `.Implement(impl)` is
> REMOVED ENTIRELY — `HandleMW(nil, fn)`/`ClientMW(nil, fn)` now cover
> everything it used to, with nil sanctioned as "nothing to pair
> against" rather than a misuse case needing detection. `Wrap` is ALSO
> removed — a caller passes a raw `func(http.Handler) http.Handler`
> closure directly to `HandleMW(nil, fn)`. `Transform` and
> `nethttp.Observability` both simplify to return the bare wrapped
> closure instead of a `ServerImplementation`, since `HandleMW` now
> builds that internally. This closes the previous round's deferred
> question as a direct side effect — `ClientMW(nil, fn)` IS the
> client-side `.Implement()`-equivalent that was asked about. Zero open
> items remain from this pass either.
>
> **A FIFTH pass — verifying the FOURTH pass's unification actually
> propagated everywhere — found 2 more items, both now fixed.** Both of
> the doc's own fully-worked example code blocks ("The full workflow,"
> "The full symmetric workflow") still passed `mw` BY VALUE
> (`middleware.Middleware`) to `HandleMW`/`ClientMW`, which now require a
> POINTER (`*middleware.Middleware`) per the fourth pass's own
> unification — a genuine compile error in the doc's own worked
> examples, fixed by taking `&mw` at all three call sites. Separately,
> the "`Serve`'s whole-builder failure semantics" section's Part 1
> wording was ambiguous about whether a route with `.HandleMW()`
> attached but NO `.WithHandler()` is still skipped as spec-only — the
> heading already said `.WithHandler()`'s presence ALONE is the gating
> signal, but the body's phrasing read as if BOTH had to be absent;
> reworded for clarity (it IS skipped, exactly like a `.Use()`-only
> route — there's no handler to wire either way). Zero open items remain
> from this pass either.
>
> **Implementation planning found and resolved 3 more pre-implementation
> gaps, all now written in below.** (1) `Register`'s "no more
> `*RouteHandle` returned... nothing downstream consumes one directly"
> claim was CODE-VERIFIED FALSE — `ports/handle.go` directly calls
> `route.Register(b)`/`SSERoute.Register(b)` and consumes the returned
> handle for both REST-ingest and SSE pattern wiring — resolved with a
> NEW, dedicated `RegisterHandle` method on both `Route` and `SSERoute`
> (identical validation, ALSO returns the handle) for `ports`-style
> direct wiring that bypasses `Serve`/`ServeSSE` entirely; `Register(b)
> error` stays the pure `Serve`-consumed path. (2) `middleware.Scopes`/
> `nethttp.Scopes`/`nethttp.APIKey` are now REDUNDANT under the fourth
> pass's `HandleMW` unification (their only job — wrapping an extract/
> verify closure into a `ServerImplementation`— now happens INSIDE
> `HandleMW` itself) — all three REMOVED. (3) `.WithOptions(opts
> Options)`'s `Options` type was left ambiguous — `api/rest` cannot
> import `adapters/nethttp.Options` without an import cycle — resolved
> as type-erased `WithOptions(opts any)`, asserted at `Serve`/`chi.Serve`
> time with a new `OptionsShapeError` on mismatch, mirroring the
> existing `FormatOptError` pattern. Zero open items remain from this
> pass either.
>
> **A further gap surfaced mid-implementation: `Serve`'s generic dispatch
> is not achievable with plain Go generics.** `Serve(mux, b) error` walks
> a HETEROGENEOUS collection of routes (each with a DIFFERENT `Req`/`Resp`
> pair), but building each one's `http.Handler` needs Req/Resp-typed
> code — Go cannot instantiate a generic function with type parameters
> known only at runtime. **Resolved: `reflect.Value.Call`, used ENTIRELY
> inside `nethttp`/`chi`'s own `Serve`/`ServeSSE`** — Go cannot
> reflectively INSTANTIATE a generic function, but CAN reflectively CALL
> an already-concrete function value by its dynamic type, and every
> closure `Serve` needs (`Decode`/`Encode`/`HandlerFn`/each
> `ServerImplementation.Fn`) is already such a value, stored in an
> exported field. Zero logic moves out of `Handler`/`chi.Handler` (they
> stay untouched); `api/rest` gains only 2 small, reflection-free
> accessors (`Server.RouteEntries`/`SSEEntries`) and stays exactly as
> `net/http`/`reflect`-free as every other decision in this doc requires.
> Zero open items remain from this pass either — ready for
> implementation.
>
> See each "## Decision: ..." / "## Review round: ..." section below for
> full API surface, rationale, and resolution history.

---

## The three core types (`middleware` package)

```go
// Declare-time-only. The ONLY type that can contribute to a route's spec.
// Attached via rest.WithMiddleware(...) / Route.Use(...).
type Middleware struct {
    Name           string
    Security       *SecurityDeclaration   // nil = no spec contribution
    RequestParams  []any                  // e.g. rest.HeaderParam{...}
    ResponseParams []any
}

type SecurityDeclaration struct {
    SchemeName string
    Scheme     route.SecurityScheme       // e.g. route.BearerScheme("JWT")
    Scopes     []string
    Codec      *codex.Codec[string]       // credential format validator
}

// Register-time-only, SERVER-side. Attached via an adapter's
// Register/Handler/SSEHandler/RegisterSSE's own variadic `impls` param.
// NEVER via Route.Use.
type ServerImplementation struct {
    Name      string
    Satisfies []string   // scheme names this Fn verifies; empty = general-purpose (always runs)
    Fn        any        // func(http.Handler) http.Handler  OR
                          // func(ctx, *http.Request, *Req) (map[string][]string, error)
}

// Register/build-time, CLIENT-side. Attached via Route.UseClient(...) or
// directly to Call/CallHandle/CallVia/CallHandleVia's own variadic.
type ClientMiddleware struct {
    Name string
    Fn   any   // func(ctx, []route.SecurityRequirement) (http.Header, error)
}
```

### Constructors

```go
// middleware package — declare-time, adapter-agnostic:
func SecurityScheme(schemeName string, scheme route.SecurityScheme,
    scopes []string, codec *codex.Codec[string]) Middleware

// middleware package — register-time, adapter-agnostic core (Raw is the
// adapter's raw wire-request type, e.g. *http.Request):
func Scopes[Raw, Req any](schemeName string,
    extract func(ctx context.Context, raw Raw, req *Req) (map[string][]string, error),
) ServerImplementation

// nethttp package — register-time, PINS Raw = *http.Request:
func Scopes[Req any](schemeName string,
    extract func(ctx context.Context, r *http.Request, req *Req) (map[string][]string, error),
) middleware.ServerImplementation

// nethttp package — declare-time half of an API-key style header check:
func HeaderParam(headerName string) middleware.Middleware

// nethttp package — register-time half (general-purpose, empty Satisfies):
func APIKey[Req any](headerName string,
    verify func(ctx context.Context, key string) error,
) middleware.ServerImplementation

// nethttp package — register-time, general-purpose (logging/metrics/tracing):
func Observability(obs stats.Observer) middleware.ServerImplementation
```

### Authorization helper (called ONCE by the adapter, never per-Fn)

```go
func CheckScopes(reqs []route.SecurityRequirement, granted map[string][]string) error
// -> UnsatisfiedScopesError{Requirements, Granted} on mismatch
```

---

## Step 1 — Declare the route + security (server-authoring side)

```go
declMw := middleware.SecurityScheme("bearerAuth", route.BearerScheme("JWT"),
    []string{"profile"}, &bearerCodec)   // codec validates credential FORMAT

route := rest.NewRoute[GetProfileReq, ProfileResp]("GET", "/profile",
    reqCodec, respCodec,
    rest.RouteMeta{OperationID: "getProfile"},
).Use(declMw)   // == rest.WithMiddleware(declMw) passed as a NewRoute opt
```

`Route.Use` / `SSERoute.Use`:

```go
func (r Route[Req, Resp]) Use(mws ...middleware.Middleware) Route[Req, Resp]
func (s SSERoute[Req, Event]) Use(mws ...middleware.Middleware) SSERoute[Req, Event]
```

- Returns a NEW `Route` (immutable, chainable: `.Use(a).Use(b)` ≡ `.Use(a,b)`).
- `rest.WithMiddleware(mws...) RouteOpt` is the underlying primitive; `.Use`
  is chi-style sugar for it, usable after `NewRoute` instead of only inline.

At `Use`/`WithMiddleware` attachment time: NOTHING happens yet except
appending to an internal slice (`rb.middlewares`). All merging/validation
is deferred to ONE later pass.

---

## Step 2 — Freeze the route: `Register` (server) or `ClientHandle` (client)

```go
handle, err := route.Register(builder)   // *rest.RouteHandle[Req, Resp], error
// OR, client-only, no spec needed:
handle := route.ClientHandle()           // *rest.RouteHandle[Req, Resp]
```

Both run the same merge pass (`Register` calls
`applyMiddlewareDeclarations` directly; `ClientHandle` calls
`applyMiddlewareSecurityForClient`, a narrower variant — see the
divergence table below), which:

1. Detects conflicting Security declarations for the SAME scheme name
   (manual `RouteMeta.Security`/`WithSecurityScheme` vs. middleware, AND
   middleware vs. middleware) → `ConflictingSecurityDeclarationError`.
2. Merges every middleware's `Security` into ONE combined (AND)
   `route.SecurityRequirement` + registers its scheme into
   `SecuritySchemes`.
3. Detects conflicting `RequestParams`/`ResponseParams` contributions →
   `ConflictingParamContributionError` / `ParamContributionShapeError`.
4. Applies every middleware-contributed param entry not already manually
   declared.

**Does NOT check** that every declared scheme has an enforcing
implementation — that check is DEFERRED (see Step 3). A route that only
ever calls `.Use(middleware.SecurityScheme(...))`, with NO
`ServerImplementation` supplied ANYWHERE, legitimately PASSES
`Register`/`ClientHandle` — this is the by-design "declared but not yet
implemented" intermediate state (used for routes describing an EXTERNAL
system's API this codebase never serves itself, e.g.
`examples/go-edge-models`'s Docker registry client).

### `RouteHandle[Req, Resp]` — the fields relevant here

```go
type RouteHandle[Req, Resp any] struct {
    Descriptor        route.Route                        // .Security lives here
    SecuritySchemes   map[string]SecurityScheme           // populated by BOTH Register & ClientHandle
    GlobalSecurity    []route.SecurityRequirement          // builder-level fallback; Register only, always nil on ClientHandle
    Middlewares       []middleware.Middleware             // Register ONLY; nil on ClientHandle
    ClientMiddlewares []middleware.ClientMiddleware        // populated by BOTH
    // ... Decode/Encode/EncodeRequest/DecodeResponse, params, merge fields, etc.
}
```

### Register vs. ClientHandle divergence (exact)

| Field | `Register(builder)` | `ClientHandle()` |
|---|---|---|
| `Descriptor.Security` | set from merged middleware + manual `RouteMeta.Security` | SAME (via `applyMiddlewareSecurityForClient`) |
| `SecuritySchemes` | populated | populated (same mechanism) |
| `GlobalSecurity` | `builder.globalSecurity` clone | always `nil` (no builder) |
| `Middlewares` | `[]middleware.Middleware` attached, cloned | **always `nil`** — a client handle never carries server declarations, only used for the coverage-check server-side |
| `ClientMiddlewares` | populated | populated |
| conflict detection | full (`ConflictingSecurityDeclarationError` etc.) | **NONE** — `ClientHandle` stays infallible (no error return); only the merge runs, not validation |

`ValidateRoute[Req, Resp](meta RouteMeta, opts ...RouteOpt) error` — a
dry-run of the IDENTICAL validation `Register` runs, without needing a
live `*Server` — same conflict errors, same "coverage not checked here"
rule.

---

## Step 3 — Server: supply the implementation at `Register`/`Handler` time

```go
implMw := nethttp.Scopes[GetProfileReq]("bearerAuth",
    func(ctx context.Context, r *http.Request, req *GetProfileReq) (map[string][]string, error) {
        // pure authentication: return granted scopes, however obtained
        return map[string][]string{"bearerAuth": nil}, nil
    })

err = nethttp.Register(mux, handle, getProfileFn, nethttp.Options{}, implMw)
// OR, for a bare http.Handler without eager validation:
h := nethttp.Handler(handle, getProfileFn, nethttp.Options{}, implMw)
```

### Exact signatures (`adapters/nethttp`, `adapters/chi` mirrors identically)

```go
func Handler[Req, Resp any](handle *rest.RouteHandle[Req, Resp],
    fn HandlerFunc[Req, Resp], opts Options,
    impls ...middleware.ServerImplementation) http.Handler

func Register[Req, Resp any](mux *http.ServeMux, handle *rest.RouteHandle[Req, Resp],
    fn HandlerFunc[Req, Resp], opts Options,
    impls ...middleware.ServerImplementation) error

func SSEHandler[Req, Event any](handle *rest.SSERouteHandle[Req, Event],
    fn SSEHandlerFunc[Req, Event], opts Options,
    impls ...middleware.ServerImplementation) http.HandlerFunc

func RegisterSSE[Req, Event any](mux *http.ServeMux, handle *rest.SSERouteHandle[Req, Event],
    fn SSEHandlerFunc[Req, Event], opts Options,
    impls ...middleware.ServerImplementation) error
```

### `Register`/`RegisterSSE` do THREE things `Handler`/`SSEHandler` do NOT

1. `validateImplementationShapes[Req](impls)` — EAGERLY type-asserts every
   `impl.Fn` against the two recognized shapes; `MiddlewareShapeError` if
   neither matches.
2. Resolve `secReqs := handle.Descriptor.Security; if nil { secReqs =
   handle.GlobalSecurity }`.
3. `rest.CheckCoverage(routeLabel, secReqs, impls)` — every scheme
   NAMED in `secReqs` must have some `impls[i]` whose `Satisfies` contains
   it → `MissingSecurityMiddlewareError` if not. **This is the coverage
   check deferred from Step 2.**

`Handler`/`SSEHandler` (called directly, bypassing `Register`) do NEITHER
of these — a malformed `Fn` is silently skipped per-request instead of
failing at wiring time; a missing implementation is silently a no-op
security check (i.e. request passes through unauthenticated) rather than
a construction-time error. This asymmetry is PRE-EXISTING (shape
validation was Register-only even before Revision 2) and intentional — not
something Revision 2 changed.

### Runtime dispatch inside `Handler`'s returned closure (per-request)

```go
func applyGeneralMiddleware(h http.Handler, impls []middleware.ServerImplementation) http.Handler
// wraps h with every impl.Fn matching func(http.Handler) http.Handler,
// OUTERMOST-in, in attachment order (impls[0] = outermost, runs first).

func runSecurityMiddleware[Req any](ctx, r *http.Request, req *Req,
    impls []middleware.ServerImplementation, secReqs []route.SecurityRequirement) error
// runs every impl.Fn matching func(ctx, *http.Request, *Req) (map[string][]string, error)
// in ATTACHMENT order (fail-fast on the FIRST error), merges ALL grants
// into ONE map, then calls middleware.CheckScopes ONCE (never per-Fn).
```

**Gating rule** (the one genuinely tricky bit of the current design): an
implementation with `Satisfies` EMPTY runs UNCONDITIONALLY, regardless of
whether the route declares any Security at all — this is how
`nethttp.APIKey`/`nethttp.Observability` (general-purpose,
logging/rate-limiting/presence-checks) get expressed under the SAME type
as security-verifying implementations. An implementation with `Satisfies`
NON-EMPTY only runs when the route actually has a non-empty `secReqs` —
an unsecured route never invokes a scope-granting Fn.

---

## Step 4 — Client side: fulfilling the same declared requirement

### Path A — ambient default, declared once

```go
credMw := middleware.ClientMiddleware{
    Fn: func(ctx context.Context, reqs []route.SecurityRequirement) (http.Header, error) {
        h := make(http.Header)
        h.Set("Authorization", "Bearer "+token)
        return h, nil
    },
}
handle := route.Use(declMw).UseClient(credMw).ClientHandle()
```

```go
func (r Route[Req, Resp]) UseClient(mws ...middleware.ClientMiddleware) Route[Req, Resp]
// rest.WithClientMiddleware(mws...) RouteOpt is the underlying primitive.
```

- `UseClient`-attached `ClientMiddleware`s land in `RouteHandle.ClientMiddlewares`
  — populated by BOTH `Register` and `ClientHandle`.
- NEVER affects the spec (`ClientMiddleware` has no Security/Param fields
  at all — structurally cannot).

### Path B — one-off, per-call override (the escape hatch)

```go
caller := nethttp.NewCaller(httpClient, baseURL)   // built once
resp, err := nethttp.CallVia(ctx, caller, handle, req, vars, nethttp.CallOptions{}, credMw)
```

### Exact client signatures

```go
type CredentialFunc = func(ctx context.Context, reqs []route.SecurityRequirement) (http.Header, error)
// a type ALIAS naming the one Fn shape Call/CallHandle/CallVia/CallHandleVia recognize.

func Call[Req, Resp any](ctx context.Context, client *http.Client, baseURL string,
    handle *rest.RouteHandle[Req, Resp], req Req, vars map[string]string,
    opts CallOptions, mws ...middleware.ClientMiddleware) (Resp, error)

func CallHandle[Req, Resp any](ctx context.Context, client *http.Client, baseURL string,
    handle *rest.RouteHandle[Req, Resp], req Req,
    opts CallOptions, mws ...middleware.ClientMiddleware) (Resp, error)
// derives vars/QueryParams/HeaderParams/CookieParams from req automatically
// via merge fields; Call is the lower-level primitive with no merge fields.

type Caller struct { /* unexported client, baseURL, defaultMws */ }
func NewCaller(client *http.Client, baseURL string, defaultMws ...middleware.ClientMiddleware) *Caller

func CallVia[Req, Resp any](ctx context.Context, c *Caller,
    handle *rest.RouteHandle[Req, Resp], req Req, vars map[string]string,
    opts CallOptions, extraMws ...middleware.ClientMiddleware) (Resp, error)

func CallHandleVia[Req, Resp any](ctx context.Context, c *Caller,
    handle *rest.RouteHandle[Req, Resp], req Req,
    opts CallOptions, extraMws ...middleware.ClientMiddleware) (Resp, error)
```

### `Call`'s exact internal step order (the numbered comments in the code)

```
0. allMws := handle.ClientMiddlewares + mws (variadic)   — validateClientMiddlewareShapes(allMws) EAGERLY
1. BuildPath(vars)                    → rest.PathParamError / MissingPathVarError
2. ValidateQuery(opts.QueryParams)    → rest.QueryParamError
3. ValidateCookies(opts.CookieParams) → rest.CookieParamError
4. ValidateHeaders(opts.HeaderParams) → rest.HeaderParamError
5. build full URL (+ query string)
6. secReqs := handle.Descriptor.Security ?? handle.GlobalSecurity
   if len(secReqs) > 0: mergeCredentialHeaders(ctx, secReqs, allMws)
     — runs EVERY attached credential Fn, merges returned http.Header
     — conflicting header values from two Fns → ConflictingCredentialHeaderError
7. encode request body (POST/PUT/PATCH only)
8. build *http.Request, set Content-Type/Accept, merge header params,
   extra headers, credential headers, cookies
8b. validate outgoing credential FORMAT against handle.SecuritySchemes[name].Codec
     (gated on len(credHeaders) > 0, not on secReqs alone)
     → rest.SecurityCredentialError, locally, before any network call
9. client.Do(...)                     → RequestError (transport failure)
10. decode response body               → typed ErrorPatternResponse / UnexpectedStatusError / ResponseBodyError
```

`CallVia`/`CallHandleVia` do NOTHING but combine `c.defaultMws` +
`extraMws` (defaultMws first) then call `Call`/`CallHandle` with the
combined slice — literally `mws := append(slices.Clone(c.defaultMws),
extraMws...)`. No new behavior, no new validation.

---

## Every escape hatch that exists today (exhaustive)

1. **Manual security declaration** — `rest.WithSecurityScheme(name,
   scheme)` + `rest.RouteMeta{Security: ...}` set directly, bypassing
   `middleware.SecurityScheme` entirely. Still fully supported,
   cross-checked for conflicts against any ALSO-attached middleware
   declaration (`ConflictingSecurityDeclarationError` if they disagree).
2. **`middleware.SecurityScheme` with no matching `ServerImplementation`
   ever supplied** — a legitimate way to document an EXTERNAL system's
   security requirement (client-only usage) without server-side
   enforcement machinery. Register/ClientHandle allow it; only
   `nethttp.Register`/`RegisterSSE` (not `Handler`/`SSEHandler`) reject it
   via `MissingSecurityMiddlewareError`.
3. **`Handler`/`SSEHandler` called directly instead of
   `Register`/`RegisterSSE`** — skips BOTH eager shape validation AND the
   coverage check. A malformed `Fn` is silently ignored per-request; a
   missing implementation silently authenticates nothing. Pre-existing
   asymmetry, not new.
4. **`ServerImplementation.Fn` as `any`** — type-erased, resolved via a
   runtime type-switch (`validateImplementationShapes`). A wrong-shaped
   `Fn` fails with `MiddlewareShapeError`, but only at `Register`/
   `RegisterSSE` time (see #3).
5. **`ClientMiddleware.Fn` as `any`** — same type-erasure/escape-hatch
   pattern client-side (`validateClientMiddlewareShapes`), checked eagerly
   inside `Call`/`CallHandle` itself (both entry points, unlike the
   server-side Handler/Register split — no separate "Call vs. eager-Call"
   distinction exists client-side).
6. **Per-call `ClientMiddleware` override** — passed directly to
   `Call`/`CallHandle`/`CallVia`/`CallHandleVia`'s trailing variadic
   instead of (or in addition to) `Route.UseClient`'s ambient default;
   used to test a different credential for one specific call.
7. **`nethttp.Call`/`CallHandle` used directly instead of
   `Caller`/`CallVia`/`CallHandleVia`** — needed by `ports.Pattern`'s REST
   binding machinery (owns its own client/baseURL via `PortOptions`, no
   `Caller`), and for genuinely one-off calls or calls against a
   different client/baseURL than a persistent `Caller`.
8. **`RequestParams`/`ResponseParams` as `[]any`** — type-erased spec
   contributions, resolved via `requestParamInfo`/`responseParamInfo`
   (only `HeaderParam`/`CookieParam`/`QueryParam` recognized) →
   `ParamContributionShapeError` for anything else.
9. **Conflicting param contributions** (two middlewares declaring the
   same header/cookie/query name with different `kind`/`required`) →
   `ConflictingParamContributionError`; IDENTICAL redundant declarations
   are silently allowed (deduped).
10. **General-purpose `ServerImplementation` (empty `Satisfies`)** as the
    unified mechanism for logging/observability/rate-limiting/API-key
    presence checks — NOT security-verifying, always runs. No separate
    "non-security middleware" type exists; it's the SAME
    `ServerImplementation` struct with an empty slice field as the only
    distinguishing signal.
11. **`ValidateRoute`** — a dry-run validator usable with no live
    `*Server`, for pre-flight checking a route declaration's
    conflicts/shape before ever calling `Register`.
12. **`middleware.ContextField[V]`** (not detailed above, exists
    separately) — a codec-typed ctx value bus for cross-middleware data
    sharing, an escape hatch for passing derived data between a security
    Fn and the business handler without a dedicated Req field.

---

## Decision: whole-API declarative wiring (resolves Question 1, closes Question 2)

**Status: IMPLEMENTED.** This supersedes Question 1's narrow "combine 2
constructors" framing with a bigger, generalized redesign: the ROUTE
itself (still the SAME `Route[Req, Resp]` type throughout — no new type
introduced) accumulates its own business handler AND every attached
middleware's implementation as part of the SAME declarative chain that
already builds `.Use(mw)`; `Register` becomes the single moment
everything (spec + handler + middleware implementations) is bound into
the `Server`; and a NEW builder-level adapter call performs the actual
transport wiring (`mux.Handle(...)`) for every accumulated route in one
shot.

### The full workflow

```go
mw := middleware.SecurityScheme("bearerAuth", scheme, scopes, codec)

route := rest.NewRoute[Req, Resp]("GET", "/profile", reqCodec, respCodec, meta).
    Use(mw).                                                          // 1. declare middleware on route (spec only, no IO)
    HandleMW(&mw, func(ctx context.Context, r *http.Request, req *Req) (map[string][]string, error) {
        return extractScopes(ctx, r)                                  // 2. attach an implementation to THAT middleware — &mw: HandleMW takes *middleware.Middleware (nilable)
    }).
    WithHandler(func(ctx context.Context, req Req) (Resp, error) {    // 3. attach the route's own business handler
        return getProfile(ctx, req)
    })

err := route.Register(builder)   // 4. binds spec + handler + middleware impls into builder — error only, no *RouteHandle returned
// ... repeat declare→Use→HandleMW→WithHandler→Register for every route in the API ...

mux := http.NewServeMux()
err = nethttp.Serve(mux, builder)   // 5. walks EVERY accumulated route, performs mux.Handle(...) for each, in one call
http.ListenAndServe(":8080", mux)   // 6. run
```

### API surface (confirmed names/signatures)

```go
// api/rest — Route stays the SAME generic type; two NEW chainable
// methods alongside the existing .Use()/.UseClient() — pure
// accumulation into the route's own opts list, no IO until Register.

// WithHandler attaches THIS route's own business handler.
func (r Route[Req, Resp]) WithHandler(fn func(ctx context.Context, req Req) (Resp, error)) Route[Req, Resp]

// HandleMW — the ONLY server-side implementation-attachment method
// (UNIFIED during a fourth critical review pass — see "Decision:
// HandleMW/ClientMW unification" below for the full rationale). mw is
// NILABLE:
//   - non-nil AND mw.Security != nil: PAIRED — fn is matched against a
//     PREVIOUSLY-.Use()'d security declaration, mw being the SAME
//     middleware.Middleware value (not a re-typed string) — matched
//     internally, closing the "typo risk" from Question 2 entirely:
//     there is no independent re-declaration of the scheme name at the
//     implement call site, mw IS the declaration.
//   - nil (or mw.Security == nil): UNPAIRED, general-purpose — fn runs
//     unconditionally, nothing to satisfy.
func (r Route[Req, Resp]) HandleMW(mw *middleware.Middleware, fn any) Route[Req, Resp]

// Register's signature changes — no more *RouteHandle returned to the
// caller. Its job is now to bind everything (spec, handler, middleware
// implementations) into the builder; a caller wiring routes through
// Serve doesn't need a per-route handle anymore — the NEXT step
// operates on the whole builder.
func (r Route[Req, Resp]) Register(b *Server) error

// adapters/nethttp (adapters/chi mirrors identically) — NEW,
// builder-level. Walks every route the builder has accumulated (each
// already carrying its own handler + middleware implementations, bound
// at Register time above) and performs the actual mux.Handle(...) call
// for each — the ONLY place literal transport wiring happens.
func Serve(mux *http.ServeMux, b *rest.Server) error
```

### RESOLVED — a dedicated `RegisterHandle` for direct-wiring callers (`ports`), found during implementation planning

**Status: IMPLEMENTED.** Planning this doc's
actual implementation surfaced a REAL, code-verified conflict: `Register`'s
"no more `*RouteHandle` returned... nothing downstream consumes one
directly" claim above is FALSE. `ports/handle.go` directly calls
`route.Register(b)` (both its `roleSource`/REST-ingest and
`roleSink`/SSE branches) and stores the RETURNED handle for adapter
binding (`nethttp.IngestAdapter`/`SubscribeAdapter`-equivalent machinery
consumes it directly, bypassing `Serve` entirely — `ports` never mounts
routes on a `*http.ServeMux` itself). This is a real, current caller, not
a hypothetical — the claim above was simply wrong.

Considered and rejected: (a) reverting `Register` to keep returning a
handle — reintroduces the exact ambiguity Decision 4 removed ("does the
caller need to also call `Serve`, or is the handle enough on its own?");
(b) having `ports` route through `Serve` somehow — `ports` doesn't own a
`*http.ServeMux`/router at all, this doesn't fit its shape.

**Resolved: `Route.Register` is for building the API (the `Serve` path)
— `ports`-style direct wiring gets its OWN, separate method that shares
`Register`'s validation but ALSO returns the handle:**

```go
// api/rest — ports-facing (and any other direct-wiring caller that
// bypasses Serve/ServeSSE entirely). Does IDENTICAL spec/handler/impl
// binding + validation as Register — shares the same internal
// implementation — but ALSO returns the handle, for callers that need
// to wire an adapter directly instead of going through Serve.
func (r Route[Req, Resp]) RegisterHandle(b *Server) (*RouteHandle[Req, Resp], error)
func (s SSERoute[Req, Event]) RegisterHandle(b *Server) (*SSERouteHandle[Req, Event], error)
```

`ports/handle.go`'s two call sites (`roleSource`/`roleSink`) switch from
`route.Register(b)` to `route.RegisterHandle(b)` — a one-line rename
each, zero other changes needed; every other `ports` behavior (spec
accumulation, security scheme population, path/topic codec validation)
is unaffected since `RegisterHandle` performs the SAME work `Register`
always did, just also handing back the handle.

### Mechanics

- `.Use()`/`.HandleMW()`/`.WithHandler()` are pure accumulation — same
  discipline as today's `.Use()`: append to an internal opts slice,
  return a new `Route` value, no IO performed yet.
- `HandleMW`'s `fn any` mirrors `ServerImplementation.Fn`'s existing
  type-erasure discipline — no generics needed at the call site; the
  closure literal's own concrete type is inferred by Go directly. The
  adapter still validates the shape at `Register`/`Serve` time via the
  same type-switch mechanism (`MiddlewareShapeError` on mismatch).
- `Route.Register(b *Server)`'s wiring (binding handler+impls to the
  route) happens **immediately, per-route, at the `Register` call** —
  not deferred to `Serve`. `Serve` only performs the literal
  `mux.Handle(...)` registration, walking what `Register` already bound.
- The Server becomes both the OpenAPI-spec accumulator (unchanged,
  existing role) AND the handler/implementation registry — extending the
  SAME accumulation discipline `Server` already has for security
  schemes/global security to also cover handlers and middleware `Fn`s,
  keyed per route.
- **`Server` gains an internal `sync.RWMutex`, making concurrent
  `Register` calls safe — closed during a final critical review pass.**
  Confirmed via code: `Server`'s entire mutation surface is THREE call
  sites (`b.entries = append(...)` ×2, one per `Route.Register`/
  `SSERoute.Register`; `b.schemas[name] = s`, schema registration) —
  everything else (`OpenAPISpec`, and the new `Serve`/`ServeSSE`/
  `ServeOne`) only READS `b.entries`/`b.schemas` by ranging over them.
  The fix is small and mechanical: one `mu sync.RWMutex` field; the 3
  write sites wrapped in `.Lock()`/`.Unlock()`; read-only iteration
  methods wrapped in `.RLock()`/`.RUnlock()`. No recursive/nested
  `Server`-method calls exist anywhere `Register` doesn't already
  return before the next one starts — zero deadlock risk to reason
  about.

  **Explicit boundary — what this DOES and does NOT buy:** this
  supports an app that fans `Register` calls out across GOROUTINES (one
  per feature-module, say) before a SINGLE later `Serve` call — multiple
  goroutines concurrently building up the SAME route set safely. It does
  NOT support hot-adding routes to an ALREADY-`Serve`'d, ALREADY-running
  mux — that is a fundamentally different feature (dynamic route
  rebinding on a live server), already tracked separately and explicitly
  flagged as an acknowledged, not-yet-designed gap in
  [Dynamic Port Rebinding](dynamic-port-rebinding.md) ("REST/events/
  reqreply's immutable `RouteHandle`/`ChannelHandle` middleware hot-swap
  remains an acknowledged gap in both docs, not yet designed"). This
  synchronized `Server` is a deliberate FIRST STEP toward that direction
  — concurrent-safe accumulation — not a claim that hot-reload itself is
  solved; a future round extending toward live rebinding would build on
  this foundation rather than starting from an unsynchronized `Server`.
- Scope: designed generically so `api/events`/`api/reqreply`/`api/mcp`
  could adopt the identical pattern later (`Channel.WithHandler`/
  `Channel.HandleMW`/`Channel.Register(builder) error` +
  `mqtt5.Serve(client, builder)`/`mcpgo.Serve(server, builder)`, etc.) —
  only REST is actually being redesigned now; other boundaries remain
  Phase 2+, unchanged from the existing declarative-middleware roadmap.

### `HandleMW`-to-`.Use()` pairing validation — RESOLVED, refined across THREE successive critical review passes

**Pass 1 — the original finding.** `HandleMW(mw, fn)` reads
`mw.Security.SchemeName` directly off whatever `mw` VALUE is passed to
IT — nothing inherently stops a caller from passing a `mw` that was
never `.Use()`'d on the SAME route at all (e.g. a copy-paste mistake,
reusing a DIFFERENT route's `mw` by accident). Such a mismatched
implementation would still RUN (the general-purpose/security gating
rule only checks `secReqs` is non-empty overall, not per-scheme),
silently contributing an irrelevant grant, while `CheckCoverage` would
separately (and correctly, but confusingly) still flag the ACTUAL
declared scheme as uncovered.

Considered generalizing chain-method validation broadly (a "sticky
error" pattern — an internal `err error` field on `Route`, every chain
method no-ops once set, surfaced at `.Register()`) and REJECTED for this
specific check: an immediate, at-call-time check using only what's
accumulated SO FAR would introduce a NEW call-ORDER dependency
(`.HandleMW(mw, fn)` before a LATER `.Use(mw)` would be incorrectly
flagged, even though the fully-assembled route is valid) — contradicting
`WithMiddleware`'s existing, explicitly-documented ORDER-INDEPENDENT
merge semantics (Register's single merge pass already processes the
COMPLETE, final opts list regardless of attachment order).

**Pass 2 — simplified into a reverse-Satisfies check.** Resolved by
extending `Register`'s EXISTING merge pass — the same pass that already
detects `ConflictingSecurityDeclarationError`/param conflicts — with a
SIMPLER, more robust mechanism than originally drafted: rather than
tracking WHICH `mw` value was passed to WHICH specific `.HandleMW()`
call (bookkeeping that would need a new, per-route data structure), the
check filters the route's FINAL, fully-accumulated `impls` list for
entries with NON-EMPTY `Satisfies`, and verifies EACH ONE corresponds to
an actually-`.Use()`'d Security scheme name ON THE SAME ROUTE — a
REVERSE-direction sibling to the EXISTING `CheckCoverage` (which checks
"every DECLARED scheme has a covering implementation"; this checks
"every IMPLEMENTED scheme was actually declared"). Needs ZERO new
per-route bookkeeping — it cross-checks two lists (`.Use()`'d Security
declarations, accumulated `impls`) that already exist in their FINAL,
fully-merged form by the time `Register`'s pass runs, order-independent
exactly like every other check in this pass.

A NEW error (`UnknownMiddlewareImplementationError` or similar — exact
name TBD at implementation time) is returned by `Register(b *Server)
error` itself — surfacing PER-ROUTE, at `Register` time, which is far
EARLIER than `Serve` (batched, potentially much later/farther away in
`main.go`). Zero chain-method signature changes, zero order-dependency
introduced, zero ergonomics cost — this reuses `Register`'s pre-existing
role as "the one place a route's own internal consistency is checked,"
exactly like every other cross-check in this design already does. This
check — and every other check `Register`'s merge pass already performs
— applies IDENTICALLY to `SSERoute.Register` (see "Decision: `SSERoute`'s
full chainable method set" below).

**Pass 3 — `mw` becomes nilable, and this check STAYS unaffected, cleanly.**
Investigating this check's exact mechanics for implementation-readiness
surfaced a related, previously-undocumented risk: NEITHER `HandleMW` NOR
`ClientMW` guarded against `mw.Security` itself being `nil` (e.g. a
general-purpose `Middleware` built via `middleware.HeaderParam(...)`
mistakenly passed to `HandleMW`) — a genuine nil-pointer-dereference
PANIC waiting to happen, not a graceful error. Investigating the fix led
to a bigger, better redesign — see "Decision: `HandleMW`/`ClientMW`
unification" below — where `mw` becomes a NILABLE `*middleware.Middleware`,
and passing `nil` (or a non-nil `mw` with `Security == nil`) becomes the
SANCTIONED signal for "general-purpose, nothing to pair against,"
replacing `.Implement()`/`Wrap` entirely rather than needing a new
misuse-detecting error at all.

**This reverse-Satisfies check (Pass 2) needs NO changes under Pass 3's
unification** — it only ever inspects entries with NON-EMPTY `Satisfies`,
which by construction can ONLY arise from a `HandleMW(mw, fn)` call where
`mw` is non-nil AND `mw.Security != nil` (the paired case). Every
general-purpose implementation (attached via `HandleMW(nil, fn)` — a
raw `func(http.Handler) http.Handler` closure, `Transform`'s output, or
`Observability`'s output all pass through this SAME `nil`-`mw` path)
always carries EMPTY `Satisfies` and is correctly, automatically exempt
from this check — nil `mw` was never the misuse case this check exists
to catch; a non-nil `mw` whose scheme was never actually `.Use()`'d is.

### Follow-on questions this raises

- ~~What happens to the EXISTING per-route `nethttp.Register`/
  `Handler`?~~ **RESOLVED — see "Decision: `Serve` is the only public
  server-side entry point" below.** Both are removed entirely, no
  escape hatch retained.
- **RESOLVED — `Options` becomes per-route, via a new `.WithOptions(opts
  any) Route[Req, Resp]` chainable method.** Confirmed against real
  usage: `examples/adapters-nethttp/main.go` already uses DIFFERENT
  `ErrorHandler`s for different routes on the SAME server — per-route
  customization is a real, currently-used pattern, not hypothetical.
  `.WithOptions()` fits the same "declare everything on the route before
  `Register`" pattern as `.WithHandler()`/`.HandleMW()`/`.ClientMW()` —
  defaults to zero-value `Options` if never called, stored in the
  `Server` alongside the handler/impls. `Serve(mux, builder) error`
  takes NO `Options` parameter at all — it uses whatever each route
  declared.
  **`opts` is type-erased (`any`), not `nethttp.Options` directly —
  found during implementation planning.** `api/rest` cannot import
  `adapters/nethttp` (nethttp already imports `api/rest` — that would be
  a cycle). `WithOptions` stores `opts` type-erased, exactly like
  `RequestFormats`/`HandleMW`'s `fn`/`RequestParams` already are;
  `nethttp.Serve`/`chi.Serve` type-assert it to their own `Options` type
  at wiring time, returning a new `OptionsShapeError` (mirroring the
  existing `FormatOptError` pattern in `builder.go`) on mismatch. Every
  earlier pseudocode signature in this doc showing `WithOptions(opts
  Options)` means this type-erased `any` form, not a literal
  cross-package `Options` reference.
- ~~Does `SSERoute` get `.WithHandler()`/`.HandleMW()`, and does `Serve`
  handle both `Route` and `SSERoute`?~~ **RESOLVED — see "Decision:
  `Serve` is the only public server-side entry point" below.** SSE gets
  its OWN `ServeSSE(mux, builder) error`, not folded into `Serve`.
- `ClientHandle()` is UNCHANGED by `WithHandler`/`HandleMW`/`Register`/
  `Serve` (a client-only route never touches those four) — see "Decision:
  symmetric client-side declarative wiring" below for what a client-only
  route DOES now attach (`.ClientMW()`) and how `ClientHandle()`'s role
  shifts from "the user's own explicit step" to "an internal building
  block the new `Call` calls for you."
- **RESOLVED — `Server.OpenAPISpec()` stays UNAFFECTED.** It reads only
  spec state (`Descriptor`, `SecuritySchemes`, `GlobalSecurity`, etc.)
  that existed before this redesign and is populated by `Register`
  exactly as today; the new handler/impl bindings `Register` ALSO now
  stores are simply never read by `OpenAPISpec()` — no construction
  changes anything about spec generation.
- **RESOLVED — `chi`'s mirror is `chi.Serve(router gochi.Router, b
  *rest.Server) error`** (and `chi.ServeSSE`), identical shape to
  `nethttp.Serve`/`ServeSSE` — consistent with every other decision in
  this doc, where `chi` mirrors `nethttp`'s redesigned surface exactly
  (`HandleMW`/`WithHandler`/`WithOptions`/`ClientMW` are all declared
  once on `rest.Route`, adapter-agnostic; only the terminal `Serve`/
  `ServeSSE`/`ServeOne` functions are adapter-specific, one pair per
  adapter).

---

## Decision: symmetric client-side declarative wiring (resolves Question 3, closes Question 5)

**Status: IMPLEMENTED.** This mirrors "Decision: whole-API declarative wiring"
above onto the CLIENT side, closing the discoverability gap Question 3
raised. It turned out to also fully resolve Question 5 (four client-side
entry-point functions) as a side effect — the whole `Call`/`CallHandle`/
`CallVia`/`CallHandleVia` family collapses into ONE function.

### The three-layer problem this replaces

Before this decision, client-side credential middleware could be attached
at THREE separate, independently-combinable layers, with no single
obvious default for a new user to reach for:

1. `route.UseClient(mw)` — per-route ambient default
2. `nethttp.NewCaller(client, baseURL, defaultMws...)` — per-Caller
   ambient default, applied to every route called through it
3. `CallVia(..., extraMws...)` — per-call override

All three combined at `Call`'s own merge step (`handle.ClientMiddlewares`
then `Caller.defaultMws` then `CallVia`'s own variadic), and nothing
prevented attaching the same or CONFLICTING credentials at more than one
layer simultaneously (caught only at runtime, via
`ConflictingCredentialHeaderError`).

### The full symmetric workflow

```go
// Shared declaration — identical either way
mw := middleware.SecurityScheme("bearerAuth", scheme, scopes, codec)
route := rest.NewRoute[Req, Resp]("GET", "/profile", reqCodec, respCodec, meta).Use(mw)

// SERVER branch (see "Decision: whole-API declarative wiring" above):
err := route.HandleMW(&mw, extractFn).WithHandler(businessFn).Register(builder)
err = nethttp.Serve(mux, builder)

// CLIENT branch (this decision):
clientRoute := route.ClientMW(&mw, credentialFn)   // client-side fulfillment — DISTINCT method from HandleMW; &mw: ClientMW takes *middleware.Middleware (nilable)

caller := nethttp.NewCaller(httpClient, baseURL)              // now JUST a client+baseURL holder
resp, err := nethttp.Call(ctx, caller, clientRoute, req, opts) // ONE function, takes Route directly
```

### API surface (confirmed names/signatures)

```go
// api/rest — a NEW chainable method, DISTINCT from HandleMW (deliberately
// NOT reused — a reader should be able to tell server-verification from
// client-fulfillment apart at the call site without inspecting fn's
// closure signature). Pure accumulation, same discipline as .Use()/
// .HandleMW()/.WithHandler() — no IO until Call is actually invoked.
//
// fn is `any` — the ONE shape nethttp.Call recognizes is
// func(ctx context.Context, reqs []route.SecurityRequirement) (http.Header, error)
// (the former CredentialFunc/ClientMiddleware.Fn shape), validated
// EAGERLY inside Call, same MiddlewareShapeError discipline as today.
// Internally builds a middleware.ClientImplementation (see "Client-side
// Satisfies-gated implementations" below) — the TRUE, EXPLICIT successor
// to the removed middleware.ClientMiddleware, mirroring
// ServerImplementation's shape. mw is NILABLE — mirrors HandleMW's own
// unification (see "Decision: HandleMW/ClientMW unification" below):
// non-nil + Security != nil PAIRS against a declared scheme; nil (or
// Security == nil) is a general-purpose client implementation, ALWAYS
// runs, nothing to pair against.
func (r Route[Req, Resp]) ClientMW(mw *middleware.Middleware, fn any) Route[Req, Resp]

// adapters/nethttp — Caller is now JUST a client/baseURL holder, no
// defaultMws field at all (credential fulfillment moved entirely onto
// the route via ClientMW).
type Caller struct { /* unexported client, baseURL */ }
func NewCaller(client *http.Client, baseURL string) *Caller

// THE single client-side entry point — REPLACES Call, CallHandle,
// CallVia, AND CallHandleVia entirely. Takes the ROUTE directly (not a
// pre-built *RouteHandle) — builds the handle internally via
// route.ClientHandle(), invisibly; the user never calls ClientHandle()
// themselves anymore. ALWAYS auto-derives path/query/header/cookie
// values from the route's declared merge fields (folding in what
// CallHandle used to do exclusively) — there is no manual-vars variant
// anymore.
func Call[Req, Resp any](ctx context.Context, c *Caller,
    route rest.Route[Req, Resp], req Req, opts CallOptions) (Resp, error)
```

### What disappears entirely

- `middleware.ClientMiddleware` (the standalone type — REPLACED by
  `middleware.ClientImplementation{Name, Satisfies, Fn}`, see "Client-side
  `Satisfies`-gated implementations" below; not removed with nothing in
  its place)
- `Route.UseClient` / `rest.WithClientMiddleware`
- The old `Call(ctx, client, baseURL, handle, req, vars, opts, mws...)`
- `CallHandle(ctx, client, baseURL, handle, req, opts, mws...)`
- `CallVia(ctx, c, handle, req, vars, opts, extraMws...)`
- `CallHandleVia(ctx, c, handle, req, opts, extraMws...)`
- `Caller.defaultMws` (Caller keeps only client + baseURL)
- The per-call credential override escape hatch (see "confirmed tradeoffs" below — deliberately NOT kept)

### What stays, with a narrowed role

- `Route.ClientHandle() *RouteHandle[Req, Resp]` — stays EXPORTED (used
  internally by the new `Call`, and by other consumers like `ports` that
  need a `*RouteHandle` directly without dialing HTTP) — but a typical
  user calling `nethttp.Call` never invokes it themselves anymore.

### Confirmed tradeoffs (deliberately accepted, not oversights)

- **No more per-call credential override.** Since `ClientMW` declares
  fulfillment on the route itself (mirroring the server side's
  declare-then-register discipline exactly), there is no remaining slot
  for "use a different credential just for this one call" — `Call` takes
  no `mws`/`extraMws` variadic at all. A caller needing a genuinely
  different credential for one specific call must build a DIFFERENT
  `Route` value (via a fresh `.ClientMW(mw, differentFn)`) rather than
  override at the call site.
- **No manual-vars fallback.** `Call` always auto-derives path/query/
  header/cookie values from the route's declared merge fields — a route
  with a path template variable but NO corresponding `rest.NewPathParam`
  merge field declared will fail with a path-build error at call time.
  Every route intended for client use must declare merge fields for
  every path/query/header/cookie value it needs — there is no raw
  `vars map[string]string` escape hatch anymore.

### Client-side `Satisfies`-gated implementations — RESOLVED, closed during a second final critical review pass

`ClientMW(mw, fn)` had the EXACT same `mw`-mismatch ambiguity as
`HandleMW(mw, fn)` (see "`HandleMW`-to-`.Use()` pairing validation"
above) — a copy-paste mistake could attach a DIFFERENT route's `mw` to
this route's `ClientMW`. Unlike the server side, this had NO direct fix
available: `Call` runs EVERY attached credential Fn UNCONDITIONALLY
whenever `secReqs` is non-empty at all (`mergeCredentialHeaders` has no
per-scheme filtering) — there was no `Satisfies` concept on client-side
implementations to check against in the first place, since the OLD
`middleware.ClientMiddleware{Name, Fn}` (and the informal representation
`ClientMW` produced in its place) never carried one.

**Resolved by giving client-side implementations a `Satisfies` concept
too, for the SAME reason server-side has one: user-facing consistency.**

```go
// middleware package — the TRUE, EXPLICIT successor to the removed
// middleware.ClientMiddleware, mirroring ServerImplementation's shape
// field-for-field.
type ClientImplementation struct {
    Name      string
    Satisfies []string   // scheme name(s) this Fn supplies a credential for; empty = general-purpose, ALWAYS runs
    Fn        any        // func(ctx, []route.SecurityRequirement) (http.Header, error)
}
```

`ClientMW(mw, fn)` derives `Satisfies` from `mw.Security.SchemeName`
internally (identical mechanism to `HandleMW`) WHEN `mw` is non-nil AND
`mw.Security != nil`; producing a `ClientImplementation`, accumulated
into the route exactly like `HandleMW`'s output is. Since `mw` is
NILABLE (see "Decision: `HandleMW`/`ClientMW` unification" below), `nil`
(or a non-nil `mw` with `Security == nil`) is the SANCTIONED signal for
"general-purpose, nothing to pair against" — `Satisfies` is simply
empty in that case, not an error.

**This is a genuine, deliberate BEHAVIOR CHANGE from today's
`mergeCredentialHeaders`, not just a validation-only addition** — `Call`
now GATES which attached implementations run, mirroring
`runSecurityMiddleware`'s exact gating rule: an implementation with
EMPTY `Satisfies` (e.g. a general-purpose tracing-header injector,
unrelated to any credential) ALWAYS runs; one with NON-EMPTY `Satisfies`
only runs if AT LEAST ONE of its schemes appears in the route's declared
`secReqs`. This is a CORRECTNESS improvement, not just consistency for
its own sake: it directly, structurally PREVENTS Finding C's original
concern rather than merely detecting it after the fact — a
mismatched/mis-attached `ClientMW` for an irrelevant scheme simply never
fires at all, contributing nothing, wasting no credential fetch, risking
no spurious `ConflictingCredentialHeaderError`. Confirmed harmless for
every legitimate use case: an AND-combined requirement (`{bearerAuth AND
apiKey}`) still needs and runs BOTH; an OR-combined requirement
(`[{bearerAuth}, {apiKey}]`) already tolerates either or both credentials
being sent unconditionally today, so gating changes nothing there
either — it ONLY changes behavior for the misuse case this exists to
catch.

**Reverse-check parity, mirroring Finding A, checked INSIDE `Call`
itself** (there is no client-side `Register`-equivalent batched step to
defer to — consistent with the ALREADY-established "`Call` is where
client-side validation happens, mirroring `Serve`'s role" resolution):
every attached `ClientImplementation` with NON-EMPTY `Satisfies` must
have at least one scheme appearing in the route's declared `secReqs`,
or `Call` returns an error — the reverse-direction sibling to the
gating rule above, same relationship `CheckCoverage`/Finding A's check
have server-side.

**Deliberately NOT extended to a coverage-style requirement** ("every
declared scheme must have SOME attached `ClientMW`") — this would
contradict the ALREADY-established, deliberate "a nil/absent credential
on a secured route is not an error" precedent (a route can legitimately
be called with no credential attached at all, e.g. to exercise the 401
path, or because auth is optional in some deployment). This asymmetry
from the server side (which DOES require coverage) is intentional, not
an oversight — the client never enforces its own authorization, only
the server does.

**RESOLVED (was previously flagged as an open question in an earlier
round): does the client side need its own `.Implement()`-equivalent for
a TRULY declare-time-content-free client implementation** (e.g. a
tracing-header injector with no `mw`/Security pairing at all)? YES —
and the `HandleMW`/`ClientMW` unification (below) answers it directly:
`ClientMW(nil, fn)` IS that equivalent. No separate method needed; this
was the SAME underlying gap `.Implement()`'s removal closes server-side,
just not yet connected to this question when it was first raised.

### Follow-on notes (all design questions resolved; one implementation heads-up remains)

- ~~Does `ports` need to be rewritten against the new single `Call`?~~
  **RESOLVED — see "Decision: unexported handle-based primitive"
  below.** `ports`' `binding.go` adapters keep their EXACT current
  construction pattern (build/accept a `*rest.RouteHandle` once, reuse
  across many calls) and call an unexported `callWithHandle` directly —
  zero rewrite needed beyond the call-site rename, since `binding.go` and
  `client.go` are confirmed to be the same Go package.
- **RESOLVED — `adapters/chi` gets NO client-side story at all, now
  confirmed as a permanent design decision, not an accident.**
  Confirmed via repo-wide search: `chi` has ZERO client-side functions
  today, and the only mentions of `chi.Call`/`chi.Caller` anywhere are in
  this doc's own hypothetical question. `chi` wraps `go-chi/chi/v5`
  purely as a SERVER-side router (chosen for its own performance/routing
  features over plain `net/http`) — an HTTP client call has nothing to
  do with which router served the request on the other end.
  `adapters/nethttp`'s `Call`/`Caller` remain the ONLY client-side entry
  point, regardless of whether the target server was built with `chi`,
  plain `net/http`, or anything else.
- **RESOLVED — `.ClientMW()` stays PURE ACCUMULATION, no validation at
  attachment time; `middleware.MiddlewareShapeError` validation for
  `ClientMW`'s `fn` happens INSIDE `Call` itself.** Consistent with the
  server side: `.HandleMW()`/`.WithHandler()` are also pure accumulation,
  with all validation deferred to `Serve` — the one place with full
  visibility. The client side has no equivalent whole-API gathering
  step (each `Call` is a standalone, one-route operation), so `Call`
  itself is the only place validation CAN happen, mirroring `Serve`'s
  role exactly. This also preserves the fluent chaining style — every
  declare-time method (`.Use()`, `.HandleMW()`, `.WithHandler()`,
  `.WithOptions()`, `.ClientMW()`) returns just `Route[Req, Resp]`, never
  `(Route[Req, Resp], error)` — adding eager validation to `.ClientMW()`
  alone would have been the one inconsistent exception.
- `examples/adapters-nethttp-client` (the example most affected — see
  its own extensive `Call`/`CallHandle`/`Caller` demo sections) will need
  a full rewrite once this ships; not a design question, just a heads-up
  for the implementation phase.

---

## Decision: `Serve` is the only public server-side entry point (resolves Question 4)

**Status: FULLY IMPLEMENTED.** `Serve(mux, b) error`/`ServeSSE(mux,
b) error` are the SOLE public server-side entry points in both
`adapters/nethttp` and `adapters/chi` — the OLD `Handler`/`Register`/
`SSEHandler`/`RegisterSSE` per-route functions have been DELETED
entirely (their internal implementations survive as unexported
`handlerFunc`/`sseHandlerFunc`, reused by `Serve`/`ServeSSE`'s reflect
dispatch AND by `HandlerLatest`/`PipelineHandler`/`SSEAdapter` in
`stream.go`/`binding.go`). This closes Question 4 by removing the
asymmetric choice entirely — mirroring exactly how "Decision: symmetric
client-side declarative wiring" made `Call` the only client-side door.

Closing this required first fixing two genuine gaps discovered mid-migration
in `Serve`/`ServeSSE`'s reflect-based dispatch (documented in "Decision:
`Serve`'s generic dispatch mechanism" below): request/response `Formats`
content negotiation (including streaming formats) via
`WithRequestFormats`/`WithFormats`, and full `ErrorPattern` response
building (typed payload + response header/cookie merge) via
`ErrorResponseFor` on the handler-error path. Both are now fully
implemented in `buildRouteHandler`'s reflect dispatch (identically in
`adapters/nethttp/serve.go` and `adapters/chi/serve.go`) — so removing
the old doors causes ZERO capability regression.

A related structural case was also resolved: `examples/sensor-service`
declares routes as package-level vars via `RegisterHandle` BEFORE any
handler exists (the real handler needs a runtime-constructed `store`).
`RouteHandle.WithHandler`/`SSERouteHandle.WithHandler` were added as
`Route.WithHandler`'s post-registration equivalents (mirroring the
existing `WithFormats`/`WithRequestFormats` post-registration pattern) —
attach the handler once the runtime dependency exists, any time before
`Serve`/`ServeSSE` runs.

### The asymmetry this replaced

`Handler`/`SSEHandler` (bare `http.Handler`, no error) skipped BOTH the
eager shape-validation (`validateImplementationShapes`) AND the coverage
check (`rest.CheckCoverage`) that `Register`/`RegisterSSE` (return
`error`) performed. Reaching for "just give me an `http.Handler`" was a
silent opt-out of both safety checks — a sharp edge for anyone who didn't
realize the two pairs behaved differently. `Serve`/`ServeOne`/`ServeSSE`
run BOTH checks unconditionally — there is no lower-safety door anymore.

### Confirmed: no capability actually lost for the two concerns raised

1. **"Compose with a non-go-codex middleware before mounting"** — NOT
   lost. `HandleMW`'s general-purpose shape (`func(http.Handler)
   http.Handler`, empty `Satisfies` — the same shape `nethttp.Observability`
   already uses) lets ANY external middleware (otel, custom logging,
   whatever) wrap a route's handler; it flows through `Serve`
   automatically as just another attached implementation.
2. **"A route not part of any `Server`"** (ad hoc handler, mounted on a
   DIFFERENT router such as gorilla/mux) — CONFIRMED DROPPED. Every route
   intended to actually serve traffic must go through `Register(builder)`
   + `Serve(mux, builder)` (or `ServeOne` for a single route). There is
   no more standalone "give me a bare handler for a route with no
   Server" path.

### SSE stays a separate function

`Serve(mux, builder) error` handles ONLY `Route` entries. SSE gets its
own `ServeSSE(mux, builder) error` (not collapsed into one dispatching
function) — SSE's distinct streaming lifecycle (long-lived connection,
`send func(Event) error` callback shape, no request body) stays cleanly
separated from the regular request/response `Route` path.

### What was removed

- `nethttp.Handler[Req, Resp any](handle, fn, opts, impls...) http.Handler`
- `nethttp.Register[Req, Resp any](mux, handle, fn, opts, impls...) error`
- `nethttp.SSEHandler[Req, Event any](handle, fn, opts, impls...) http.Handler`
- `nethttp.RegisterSSE[Req, Event any](mux, handle, fn, opts, impls...) error`
- (all four had a `chi` mirror — removed identically there)
- All ~128 existing test call sites across both packages were migrated
  onto `ServeOne`/`Serve`/`ServeSSE`; every example in `examples/` that
  used the old doors was migrated to the same shape (7 examples plus
  `sensor-service`'s runtime-attached-handler case).

### What replaces them

```go
func Serve(mux *http.ServeMux, b *rest.Server) error      // Route entries
func ServeOne[Req, Resp any](r rest.Route[Req, Resp]) (http.Handler, error) // single-route sugar
func ServeSSE(mux *http.ServeMux, b *rest.Server) error   // SSERoute entries
```
- **RESOLVED — a thin convenience wrapper, `ServeOne`, not a new
  mechanism.** Confirmed the scale of this need: `adapters/nethttp`'s
  OWN test suite (`adapter_test.go`) has 66 direct calls to
  `nethttp.Handler(...)` for isolated single-route testing — this is
  foundational, not hypothetical.

  ```go
  // adapters/nethttp — pure sugar. Implemented as LITERALLY "build a
  // scratch, single-route Server, register route into it, call Serve,
  // return the resulting mux" — reuses Serve's exact validation path,
  // zero bypass, zero new mechanism. Still consistent with "Serve is the
  // only door" (Decision 4) — this is a convenience wrapper AROUND that
  // one door, not a second door.
  //
  // Takes NO Options parameter — fully consistent with "Options becomes
  // per-route via .WithOptions()" (see this decision's own follow-on
  // notes above): a caller wanting custom Options for a single-route
  // test calls route.WithOptions(opts) FIRST, then ServeOne(route). An
  // EARLIER draft of this signature also took an `opts Options`
  // parameter directly — a leftover inconsistency, caught during a
  // final critical review pass and corrected here: `Serve` takes no
  // Options, so `ServeOne` ("literally Serve on a scratch single-route
  // builder") cannot either, without a special case this doc's own
  // "fewer doors" discipline argues against.
  func ServeOne[Req, Resp any](route rest.Route[Req, Resp]) (http.Handler, error)
  ```

  A test (or any caller) wanting a bare `http.Handler` for exactly one
  route calls `ServeOne` instead of manually spinning up a scratch
  `Server`/mux/`Serve` sequence by hand — but the underlying mechanism
  is IDENTICAL either way; `ServeOne` is not a bypass.
- **RESOLVED — `Server` does NOT need a route-lookup-by-label
  mechanism.** `ServeOne`'s scratch builder contains EXACTLY the one
  route passed to it — it never searches an existing, larger `Server`
  at all, so there is no need for `Server` to support looking up one
  route among many by label/operation-ID. `Server` stays a write-only
  accumulator, walked in full only by `Serve`/`ServeSSE`/`ServeOne`.

## Decision: `Serve`'s generic dispatch mechanism — `reflect`, isolated to `nethttp`/`chi`, found and resolved during implementation

**Status: IMPLEMENTED.** Implementation planning
found a genuine Go-generics feasibility gap in the decision above:
`Serve(mux, b) error` is non-generic and walks a HETEROGENEOUS
collection (`b`'s accumulated routes, each with a DIFFERENT `Req`/`Resp`
pair) — but building an `http.Handler` for any one of them requires
calling code that is Req/Resp-typed (decode body into `Req`, call the
business handler, encode `Resp`). Go has no mechanism to instantiate a
generic function (`Handler[Req, Resp]`) with type parameters known only
at runtime — every alternative considered (erased closures captured at
register time, callback interfaces, registered-builder patterns) still
requires knowing `Req`/`Resp` at a point that doesn't have them.

**Resolved: `reflect.Value.Call`, used ENTIRELY inside `nethttp`/`chi`'s
own `Serve`/`ServeSSE`, never in `api/rest`.** The key fact that makes
this tractable: Go cannot reflectively INSTANTIATE a generic function,
but it CAN reflectively CALL an already-concrete function VALUE
(`reflect.Value.Call`), matching arguments/returns by their DYNAMIC
types — regardless of whether the caller's own code knows those types
statically. Every closure `Serve` needs (`RouteHandle.Decode`, `.Encode`,
`.HandlerFn`, each `middleware.ServerImplementation.Fn`) is ALREADY a
concrete function value stored in an EXPORTED field — `reflect.Value.Call`
invokes each one directly, with ZERO logic moved out of
`nethttp.Handler`/`chi.Handler` (which stay exactly where they are,
unchanged) and ZERO new `net/http`/`reflect` dependency in `api/rest`.

```go
// api/rest — sealed exported interfaces (unexported marker method
// prevents any OTHER package from implementing them, while still
// letting nethttp/chi range over them) distinguishing Route entries
// from SSERoute entries cleanly in Server's single internal list.
type RouteEntry interface {
    Method() string
    Path() string
    HasHandler() bool
    Handle() any // *RouteHandle[Req, Resp], Req/Resp erased
    isRouteEntry()
}
type SSERouteEntry interface {
    Method() string // always "GET"
    Path() string
    HasHandler() bool
    Handle() any // *SSERouteHandle[Req, Event], erased
    isSSERouteEntry()
}
func (b *Server) RouteEntries() []RouteEntry      // read-only, RLock-guarded
func (b *Server) SSEEntries() []SSERouteEntry     // read-only, RLock-guarded
```

`nethttp.Serve`/`ServeSSE` (per handler-bearing entry): dereference
`reflect.ValueOf(e.Handle()).Elem()`, then reflect-call `Decode` (body →
`Req`), the ALREADY non-generic `ValidatePathParams`/`ValidateQuery`/etc.
methods NORMALLY (no reflect needed — their signatures never named
Req/Resp), reflect-call the Req-typed merge step, dispatch general-purpose
implementations via an ORDINARY type assertion (`func(http.Handler)
http.Handler` has nothing Req-specific in it), dispatch security-paired
implementations via `reflect.Value.Call` (matches `impl.Fn`'s concrete
`func(context.Context, *http.Request, *Req) (...)` shape by dynamic
type), reflect-call `HandlerFn`, reflect-call `Encode`, write the
response — the SAME pipeline `Handler[Req,Resp]` already runs today,
just invoked via `reflect.Value.Call` instead of static generic calls.
`ServeSSE` mirrors this for `*SSERouteHandle[Req,Event]`'s
`EncodeEvent`/`ValidateEvent`/`send func(Event) error` shape.

**Confirmed acceptable tradeoff.** Real per-request `reflect.Value.Call`
overhead, and a mismatched `Fn` shape becomes a runtime panic (caught by
Part 3's existing `recover()` safety net) rather than a compile error —
but `validateImplementationShapesReflect` ALREADY eagerly type-switch-checks
every `Fn` at `Serve`-call time (before any request arrives), so this is
caught at wiring time, not silently at first-request time. Isolated
entirely to `nethttp`/`chi`'s private `Serve`/`ServeSSE` — zero public
API surface change, and `api/rest` itself remains exactly as
`net/http`/`reflect`-free as every other decision in this doc requires
(the 2 new `RouteEntry`/`SSERouteEntry` accessors above are ordinary,
reflection-free Go).

### Two gaps found and closed during old-door removal (not part of the original plan)

Migrating the existing test suites and examples off `Handler`/`Register`
onto `Serve`/`ServeOne` surfaced two features the FIRST version of
`buildRouteHandler`'s reflect dispatch never implemented — silently
regressing relative to `handlerFunc` for any route using them:

1. **No request/response `Formats` content negotiation.** The first
   version always called `DecodeMerged`/`EncodeMerged` (the route's
   default JSON codec), never consulting `RequestFormats`/`Formats` at
   all — confirmed via 6+ failing tests when migrated blindly. **Fixed**
   by reflecting over the `[]format.Format[Req]`/`[]format.Format[Resp]`
   slice fields directly: each `format.Format[T]` value's EXPORTED
   methods (`ContentType`, `Unmarshal`, `Marshal`, `Validate`,
   `IsStreamable`, `MarshalTo`) are reflect-callable regardless of `T`
   (Go generics are monomorphized per concrete type at compile time, so
   a `reflect.Value` wrapping an already-concrete `format.Format[T]`
   value has a full, callable method set) — no new codex/format API
   needed. `RouteHandle.ApplyMergeFields`/`EncodeResponseMergeFields`
   were added (splitting `DecodeMerged`/`EncodeMerged`'s var-merge half
   from their body-codec half) so the merge-field step still runs when
   the body is decoded/encoded via a negotiated format instead of plain
   `Decode`/`Encode`.
2. **No full `ErrorPattern` response on the handler-error path.** The
   first version only called `ErrorStatusFor` (status-code-only
   mapping), never `ErrorResponseFor` (typed payload + response
   header/cookie merge) — confirmed via 2 failing tests. **Fixed** by
   reflect-calling `ErrorResponseFor` (already non-generic in its
   return shape — `ErrorPatternResponse{Status, Body, Value any,
   Action}` — so no reflect needed for the pattern match itself) and
   adding `RouteHandle.EncodeResponseMergeFields` (used both by the
   handler-success path AND the matched-`ErrorPattern`-payload path,
   type-checking `pattern.Value`'s concrete type against `Resp` via
   `reflect.TypeOf` before applying merge fields — the SAME parity rule
   `handlerFunc`'s `writeErrorPatternResponse` already enforced).

Both fixes are isolated to `serve.go`'s reflect dispatch (identically in
`nethttp`/`chi`) plus 3 small new non-generic-signature `RouteHandle`
methods (`ApplyMergeFields`, `EncodeResponseMergeFields`,
`WithHandler`) — zero change to `handlerFunc`/`sseHandlerFunc` or any
other public API. A third, SEPARATE parity bug was found and fixed in
the same pass: `negotiateFormatReflect` (SSE's own Accept-header
negotiation, shared with `Serve`'s Formats negotiation) compared the raw
Accept token against a format's FULL `ContentType()` string without
stripping `;`-parameters, unlike the non-reflect `negotiateFormat` —
causing false 406s for content types with parameters (e.g. `text/html;
charset=utf-8`). Fixed to strip parameters from both sides before
comparing, matching `negotiateFormat` exactly.

---

## Decision: `SSERoute`'s full chainable method set (server-side only — closed during a final critical review pass)

**Status: IMPLEMENTED.** A final critical
review pass found that, while `Serve`/`ServeSSE`'s split was already
resolved, `SSERoute`'s OWN new chainable methods were never given
explicit signatures anywhere in this doc — only narratively implied
("SSE gets its OWN `ServeSSE`... treatment"). This closes that gap,
SERVER-side only.

`SSERoute` mirrors `Route`'s full chain, one-for-one, with exactly ONE
signature difference (`WithHandler`, below):

```go
// api/rest — SSERoute[Req, Event]'s new chainable methods. HandleMW's
// mw is NILABLE, mirroring Route's own unified HandleMW exactly (see
// "Decision: HandleMW/ClientMW unification" below) — .Implement() never
// existed as a SEPARATE SSERoute method; this same HandleMW(nil, fn)
// covers it.
func (s SSERoute[Req, Event]) HandleMW(mw *middleware.Middleware, fn any) SSERoute[Req, Event]
func (s SSERoute[Req, Event]) WithOptions(opts any) SSERoute[Req, Event]

// WithHandler DIFFERS from Route's — takes SSEHandlerFunc, not
// func(ctx, req) (Resp, error), since an SSE route streams MULTIPLE
// events per request instead of returning ONE Resp.
func (s SSERoute[Req, Event]) WithHandler(fn SSEHandlerFunc[Req, Event]) SSERoute[Req, Event]

// Register — same signature SHAPE as Route's (error only, no
// *SSERouteHandle returned), binding spec+handler+impls into the SAME
// Server Route.Register(b) already accumulates into.
func (s SSERoute[Req, Event]) Register(b *Server) error

// RegisterHandle — same ports-facing addition as Route's own (see
// "RESOLVED — a dedicated RegisterHandle..." above); ports' roleSink/SSE
// branch uses this instead of Register.
func (s SSERoute[Req, Event]) RegisterHandle(b *Server) (*SSERouteHandle[Req, Event], error)
```

**`SSERoute.Register`'s merge pass is IDENTICAL to `Route.Register`'s —
made explicit here since it wasn't stated anywhere else.** Every check
`Route.Register` performs (`ConflictingSecurityDeclarationError`, param
conflicts, and the reverse-Satisfies `HandleMW`-pairing check from
"`HandleMW`-to-`.Use()` pairing validation" above) applies UNCHANGED to
`SSERoute.Register`, since `SSERoute.HandleMW` shares the EXACT same
nilable-`mw` signature shape as `Route`'s (shown above) — there is
nothing SSE-specific about any of these checks; only `WithHandler`'s
signature differs, and that difference doesn't affect what gets
validated.

`.Use()` is UNCHANGED (already existed, already correctly signatured —
see "Step 1" above). No `.ClientMW()`/client-side `Call` equivalent is
defined for `SSERoute` here — see "Explicitly deferred" below.

### Explicitly deferred: SSE client-side consumption

While designing the above, confirmed there is NO first-class,
declarative SSE CLIENT-consumption mechanism anywhere in go-codex
today — `examples/adapters-sse/main.go` reads its own SSE responses via
a hand-rolled `readSSELines(resp *http.Response) []string` helper (raw
line parsing), and `adapters/nethttp/stream_errors.go` still carries
`SSEConnectError`/`SSEParseError` types whose doc comments reference a
function called `SSEClientStream` that does NOT exist anywhere in the
current codebase — an apparent leftover from an earlier, since-removed
stream-bridge helper that was never given a declarative replacement.

This is NOT a mirror of an existing mechanism (unlike security/client-
credentials, which HAD an old design to redesign) — it would be a
GENUINELY NEW capability. Deliberately NOT designed as part of this
review pass; captured instead as its own roadmap doc, SSE Client
Consumption — to be picked up AFTER this doc's implementation ships,
same deferral pattern already established for
[ReqReply Workflow Simplification](../roadmap/reqreply-workflow-simplification.md).
(`api/events`'s own equivalent has since shipped — see
[Pub/Sub Workflow Simplification](d-0002-pubsub-workflow-simplification.md)
— and this doc's own addendum below.)

---

## Decision: `any`-typed `Fn`/`fn` parameters are the correct tradeoff, not a gap (resolves Question 6)

**Status: confirmed — NOT a design change, a closed investigation.**
Unlike the other five decisions above, this one concludes NO API change
is warranted — the current `any`-typed shape is the only one that
satisfies two constraints simultaneously, and no alternative design can
satisfy both at once.

### Why a compile-time-typed alternative isn't reachable

1. **Go does not allow generic methods.** A method cannot introduce type
   parameters beyond its receiver's own. `Route[Req, Resp]` is already
   generic over `Req`/`Resp`; a hypothetical `func (r Route[Req, Resp])
   HandleMW[Raw any](mw Middleware, fn func(ctx context.Context, raw Raw,
   req *Req) (map[string][]string, error)) Route[Req, Resp]` — which
   would let `Raw` (e.g. `*http.Request`) be compile-time-checked — is
   simply not valid Go. This is the SAME constraint that already forced
   `Caller.Call`/`CallHandle` to be free functions (`CallVia`/
   `CallHandleVia`) rather than methods, documented elsewhere in this
   codebase. Making `HandleMW`/`ClientMW`'s `Raw`-typed shape
   compile-time-safe would require abandoning the fluent
   `.Use().HandleMW().WithHandler()` chain (Decisions 1–3) in favor of a
   free-function alternative — reintroducing exactly the two-shapes-for-
   one-concept friction those decisions eliminated.
2. **`api/rest` and `middleware` deliberately never import `net/http` or
   any `adapters/*` package** (confirmed via repo-wide grep — zero hits
   outside test files) — this layering is what lets the SAME
   `middleware.Middleware`/`ServerImplementation` types generalize to
   future non-HTTP adapters (events/reqreply/MCP), per Decisions 1–3's
   explicit scope. The general-purpose shape
   (`func(http.Handler) http.Handler`) is inherently HTTP-specific;
   encoding it as a concrete (non-`any`) parameter type on
   `Route.HandleMW`/`ClientMW` would force `api/rest` to import
   `net/http`, breaking that layering for every future adapter, not just
   nethttp/chi.

Since compile-time safety for the general-purpose shape requires breaking
constraint 2, and compile-time safety for the security-verifying shape
requires breaking constraint 1 (Go's generic-method limitation), there is
no design that recovers static typing for `Fn`/`fn` here without
sacrificing either the adapter-agnostic core or the fluent chaining API
established in Decisions 1–3. **The current design — `any` + eager
runtime shape validation via `MiddlewareShapeError`, checked at `Serve`
time (per Decision 4, the only remaining entry point) — is confirmed as
the correct tradeoff for this constraint set, not an oversight.** No
follow-on questions; this closes Question 6 with no open thread.

---

## Compromises evaluation — every escape hatch, re-checked against Decisions 1–4

After Decisions 1–4 were agreed, every one of the 12 escape hatches
inventoried earlier in this doc was re-evaluated twice: once for
"does the new design still support this," and once more for "can the
new, simpler workflow actually ELIMINATE this, not just tolerate it."
Two NEW, previously-underestimated compromises surfaced in the first
pass (EH1 and EH7) — both more significant than the audit's original
framing suggested. Three more elimination candidates surfaced in the
second pass (all three accepted). All five are resolved below.

Severity scale used throughout: **Critical** (breaks a common pattern
silently, no workaround) / **High** (breaks a real pattern, workaround
exists but the loss is structural) / **Medium** (narrow audience, real
but bounded impact) / **Low** (implementation risk, not a design flaw,
cheap to prevent).

| # | Escape hatch | Purpose | New-design implication | Severity | Verdict |
|---|---|---|---|---|---|
| 1 | Manual `WithSecurityScheme`+`RouteMeta.Security` | Declare security spec without touching `middleware` at all | **Confirmed in code**: today's coverage check (`rest.CheckCoverage`) matches PURELY by scheme-name string, with zero dependency on any `Middleware` value — a fully-manual declaration and a `Scopes("bearerAuth", fn)` implementation satisfy each other today with no connection between them. `HandleMW(mw, fn)` requires an actual `mw` value; a manual declaration produces none — so under Decisions 1–2, a manually-declared scheme has **no way left to attach a server-side implementation at all**. | **Critical** (until resolved below) | **Resolved — see "Decision: eliminate manual per-route security declaration" below** (goes further than a bridge: the escape hatch is removed, not patched) |
| 2 | `SecurityScheme` declared, never implemented (external-API docs) | Document a scheme with zero enforcement, on purpose | Still valid in isolation; converges with EH1 under the new design — both become "declared, unimplemented" cases. | **High** (was, until resolved) | **Resolved — see "RESOLVED: `Serve`'s whole-builder failure semantics" below.** A route with no `.WithHandler()` attached is unambiguously spec-only and is SKIPPED entirely by `Serve` (never validated, never an error) — this is exactly EH2's use case, now a first-class, zero-friction outcome rather than an unresolved tension |
| 3 | `Handler`/`SSEHandler` bypass | Cheap unchecked handler, or standalone (no-`Server`) usage | Already eliminated (Decision 4); mitigations already confirmed sufficient | — | Already decided: **stays dropped**, no change from this pass |
| 4 | `ServerImplementation.Fn` as `any` | Adapter-agnostic type erasure | Unaffected; Decision 6 re-confirmed it's mandatory | — | **Keep** — foundational, not a "hatch" in the droppable sense |
| 5 | Client `fn` as `any` (now on `.ClientMW()`) | Same, client side | Unaffected, relocated | — | **Keep** — same reasoning as #4 |
| 6 | Per-call credential override | One-off/test credential swap | Already eliminated (Decision 3) — workaround is building a new `Route` value with a different `.ClientMW()` | **Low–Medium** | **Confirmed fine as-is** — considered and rejected a lightweight test-only override helper; building a fresh `Route` is acceptable ceremony for this narrow case |
| 7 | `ports`' direct `Call`/`CallHandle` | Build a `*rest.RouteHandle` once, call it per-item at high throughput | **Confirmed in code**: `adapters/nethttp/binding.go`'s port adapters (`CallAdapter`, `DrainCallAdapter`, etc.) already accept a pre-built `*rest.RouteHandle` at construction time, reused across unboundedly many calls (e.g. a ticker loop). The new unified `Call(ctx, caller, route, req, opts)` would rebuild the handle via `route.ClientHandle()` on EVERY call if `ports` were forced through it — a real, measurable regression for this narrow, high-throughput audience. | **Medium** | **Resolved — see "Decision: unexported handle-based primitive" below** |
| 8 | `RequestParams`/`ResponseParams` as `[]any` | Type-erased spec contribution | Unaffected by Decisions 1–4, BUT re-examined in the second pass: unlike `Fn`, these entries are already concrete, non-generic `api/rest` types — no obstacle to making them compile-time-typed | **Low** (as a compromise); genuine simplification opportunity | **ELIMINATED — see "Decision: typed `RequestParams`/`ResponseParams` fields" below** |
| 9 | Conflicting param contribution detection | Safety net for typo/clash | Unaffected — still runs inside `Register`, now operating on typed inputs instead of type-switched `any` entries (per #8's resolution) | — | **Keep** — the safety net itself is still needed, just simplified |
| 10 | General-purpose empty-`Satisfies` implementation | Unify logging/observability/rate-limiting under ONE mechanism | Unaffected — and is the EXPLICIT, already-confirmed mitigation for "compose external middleware" now that `Handler` is gone (Decision 4). Re-examined in the second pass: the "empty `Satisfies` = general-purpose" signal is implicit, worth making explicit given how load-bearing it now is. Re-examined AGAIN in a later review round: forcing this case through `.Use()`+`.HandleMW()` (mirroring security's pairing) was itself the bug — general-purpose implementations declare nothing, so there's nothing to match. Re-examined a THIRD time, in a fourth review round: even the interim `.Implement(impl)` fix was superseded — `HandleMW`'s `mw` became NILABLE, unifying the general-purpose case directly into `HandleMW`/`ClientMW` themselves, no separate method needed at all. | — | **Keep the mechanism, now UNIFIED directly into `HandleMW`/`ClientMW` — see "Decision: `HandleMW`/`ClientMW` unification" below** (supersedes the interim `.Implement()`/`Wrap` fix, which itself superseded and fixed an earlier, buggy `middleware.Wrap` draft) |
| 11 | `ValidateRoute` dry-run | Pre-flight check, no live `Server` needed | **RESOLVED — kept completely unchanged, no code change needed.** Confirmed `applyMiddlewareDeclarations` (what `ValidateRoute` calls) only ever reads `rb.middlewares`/security/param registries — the new opt kinds (`.HandleMW()`, `.WithHandler()`, `.WithOptions()`, `.ClientMW()`) populate OTHER fields it never touches, so it naturally, structurally ignores them. Considered extending it to also simulate `Serve`'s shape/coverage check, and explicitly rejected: `ValidateRoute` (declaration-only, no handler needed — useful for a "contract-first" workflow validating a shared route/middleware declaration in CI before any handler exists) and `ServeOne` (full check, requires `.WithHandler()`) now form a clean, non-overlapping two-tier validation story. A third "full check without building a handler" tier would be marginal, extra surface for a rare need — cuts against the "fewer doors" discipline established throughout this doc. | **Low** | **Keep as-is, unchanged — no extension** |
| 12 | `middleware.ContextField[V]` | Cross-middleware/handler data sharing, no `Req` pollution | Depends on `EnsureContextFields(ctx)` being called as the OUTERMOST step per route — currently done by `Register`/`Handler` (both removed by Decision 4). **Broadened during a final critical review pass**: confirmed via direct code inspection that `Handler`'s CURRENT body actually sets up FOUR ctx keys before any Fn runs, not just this one — `middleware.EnsureContextFields(ctx)`, raw `*http.Request` access (`contextKey{}`), the response-headers box (`responseHeadersKey{}`), and the response-cookies box (`responseCookiesKey{}`, backing `nethttp.WithResponseHeaders`/`WithResponseCookies`). Real risk of silent loss for ALL FOUR, not just `ContextField`, if whatever replaces `Handler`'s body doesn't carry every one of them forward. | **Low** | **Keep** — tracked as an explicit implementation-checklist item (ALL FOUR ctx pre-allocation steps, not just `ContextField`/`EnsureContextFields`), not a design question |

## Decision: eliminate manual per-route security declaration (resolves EH1's critical finding)

**Status: IMPLEMENTED.** Rather than merely
bridging the manual escape hatch into the new mechanism, this goes
further: the manual "declare a NEW per-route security requirement without
a `Middleware` value" pattern is removed entirely, since it structurally
cannot produce anything `HandleMW` can attach an implementation to.

`RouteMeta.Security`'s OTHER two states are UNRELATED to scheme
declaration and are kept unchanged:
- `nil` — inherit global security (via `Server.AddGlobalSecurity`)
- `[]route.SecurityRequirement{}` (empty slice) — explicit opt-out, "no
  auth required" for this route

Only the THIRD state — a non-empty `RouteMeta.Security` manually
declaring a NEW requirement, paired with `rest.WithSecurityScheme` to
register the scheme's metadata — is eliminated. Every route that wants an
actual security requirement now goes through `middleware.SecurityScheme`
(building a `Middleware` from scratch) or `rest.FromSecurityScheme`
(bridging an existing `rest.SecurityScheme` value — see below), attached
via `.Use()`.

```go
// api/rest package — NOT middleware (found during implementation
// planning: rest.SecurityScheme is an api/rest-only type bundling
// route.SecurityScheme + an optional Codec; middleware cannot import
// api/rest without a cycle, since api/rest already imports middleware
// for Middleware/ServerImplementation/etc. rest.FromSecurityScheme lives
// alongside that type and bridges it into a real middleware.Middleware
// with a one-line internal call to middleware.SecurityScheme, passing
// scheme.SecurityScheme and scheme.Codec through) — bridges an existing
// rest.SecurityScheme value (e.g. a package-level var shared across
// several routes) into a real Middleware, usable with .Use()/.HandleMW()
// exactly like one built via middleware.SecurityScheme(...) directly.
func FromSecurityScheme(schemeName string, scheme SecurityScheme, scopes []string) middleware.Middleware
```

**Confirmed consequence: `rest.WithSecurityScheme` is REMOVED entirely.**
There is no `Server`-level scheme registration independent of routes
(confirmed — no `Server.AddSecurityScheme` exists), so `WithSecurityScheme`'s
only remaining purpose (registering a scheme's spec metadata) is fully
subsumed by `FromSecurityScheme`/`SecurityScheme` — nothing is lost,
there is no longer a second, parallel path to the same result.

**Confirmed consequence: the manual-vs-middleware conflict-detection
machinery is dead code, removed.** `ConflictingSecurityDeclarationError`'s
"manual" source case (`securityContribution{source: "manual"}` and its
cross-checks against middleware-contributed declarations) becomes
unreachable once manual declaration itself is gone — this is a genuine
simplification of `api/rest/middleware.go`, not just an API surface
reduction. `ConflictingSecurityDeclarationError` itself may still be
needed for middleware-vs-middleware conflicts (two DIFFERENT
`SecurityScheme`/`FromSecurityScheme` values disagreeing on the SAME
scheme name) — only the "manual" source variant is dead.

### RESOLVED: `Serve`'s whole-builder failure semantics (escape hatch #2)

**Status: IMPLEMENTED.**

**Part 1 — `.WithHandler()`'s presence ALONE is the exact gating signal.**
Whether a route ever had `.WithHandler()` called on it is precisely
"was this route ever meant to be served" — regardless of how many
`.Use()`/`.HandleMW()`/`.ClientMW()` calls it also accumulated. A route
missing `.WithHandler()` is unambiguously spec-only (escape hatch #2's
external-API-documentation case) and is SKIPPED by `Serve` (and
`ServeSSE`) entirely — no `mux.Handle` call, no shape/coverage
validation, no error — EVEN IF it also has one or more `.HandleMW(nil, ...)`
general-purpose implementations attached (e.g. an observability wrapper
declared on a route with no business handler yet): there is still no
handler to wire, so the SAME skip applies, exactly as if only `.Use()`
had been called. The shape/coverage check ONLY runs for routes where
`.WithHandler()` WAS called — that is the only case where "declared
security with no implementation" is an actual bug rather than
intentional documentation. This cleanly separates EH2's legitimate use
case (never validated, never wired, never an error) from a genuine
misconfiguration (a route the caller DOES want served, missing a
required implementation).

**Part 2 — strict fail-fast, all-or-nothing, for routes that DO have a
handler attached.** `Serve`/`ServeSSE` validate EVERY handler-bearing
route in the builder FIRST (shape + coverage checks), collecting ALL
failures into ONE aggregate error (a new `MultiRouteError` or similar,
carrying every individual `MissingSecurityMiddlewareError`/
`MiddlewareShapeError` found, not just the first) — and wire NOTHING
(the `*http.ServeMux` receives zero `mux.Handle` calls) if even ONE
handler-bearing route fails. This mirrors how every other check in this
design already behaves (`MissingSecurityMiddlewareError`,
`ConflictingSecurityDeclarationError`, etc. all fail loudly and
immediately, never degrade silently) — "the whole server either starts
correctly or doesn't start at all" is a far easier production mental
model than "some unknown subset of my API silently isn't there,"
discovered only via unexplained 404s later. A caller wanting partial
degradation (e.g. one team's broken route shouldn't block another
team's working ones) can still achieve it explicitly — by building
SEPARATE `Server`s per independently-deployable group of routes and
calling `Serve` once per `Server`/mux — rather than `Serve` silently
doing this partitioning on their behalf.

**Part 3 — duplicate Method+Path detection, closing a gap found during a
final critical review pass.** `*http.ServeMux.Handle` PANICS on a
conflicting pattern — a failure mode Part 2's "validate shape/coverage
first, wire only if all pass" design does not cover on its own, since
duplicate-path collision isn't a shape or coverage problem. Left
unaddressed, this would have TWO consequences: (1) nothing proactively
catches a duplicate Method+Path before wiring begins (`rest.Route.Register`
itself doesn't detect duplicates either — confirmed only
`reqreply.Route.Register`/`apimcp.Tool.Register` do, an existing,
unrelated asymmetry); and (2) a panic occurring PARTWAY through the
`mux.Handle(...)` wiring loop would leave the mux PARTIALLY wired
(earlier routes in the loop already registered, since `*http.ServeMux`
has no rollback) — directly violating Part 2's all-or-nothing guarantee.

Resolved with BOTH a proactive check and a defensive safety net,
layered:

1. **Proactive duplicate detection, folded into Part 2's SAME
   pre-validation pass.** Before any `mux.Handle` call, `Serve`/`ServeSSE`
   additionally walk every handler-bearing route checking for
   Method+Path collisions among them, contributing a new
   `DuplicateRouteError{Method, Path string}` entry into the SAME
   aggregate `MultiRouteError` Part 2 already builds — a duplicate is
   reported with exactly the same loud, `errors.As`-navigable,
   all-or-nothing treatment as a missing-coverage or shape error, not a
   raw panic message.
2. **A defensive `recover()` around the wiring loop, as a last-resort
   safety net — not the primary defense.** Even with proactive detection
   for the KNOWN panic cause (duplicates), `*http.ServeMux.Handle` can
   panic for OTHER reasons this design doesn't enumerate (e.g. a
   malformed pattern string) — `Serve`/`ServeSSE` wrap the wiring loop in
   a `recover()`, converting any such panic into a returned error instead
   of crashing the calling process. Since proactive detection (1) already
   prevents the wiring loop from ever starting when duplicates exist,
   this recover only needs to handle genuinely unanticipated panics — it
   is a safety net, not a substitute for (1).

Together, these close the gap Part 2 alone left open: EVERY known and
unknown `mux.Handle` failure mode is now either caught before wiring
starts (1) or converted to a graceful error if it somehow still occurs
during wiring (2) — "the whole server either starts correctly or doesn't
start at all" now holds unconditionally, not just for shape/coverage
errors.

**Part 4 — `MultiRouteError`'s exact shape, finalized during a final
critical review pass.** Parts 2 and 3 above both feed into the SAME
aggregate error — previously left as "a new `MultiRouteError` or
similar," now a concrete design:

```go
// adapters/nethttp — aggregate error returned by Serve/ServeSSE when
// one or more handler-bearing routes fail validation. Carries EVERY
// individual failure found during the pre-wiring validation pass — not
// just the first — so a caller sees the COMPLETE list of what's wrong
// in one error, not one-at-a-time via repeated fix-rebuild-fail cycles.
type MultiRouteError struct {
    // Errors is one entry per failing route — each individually
    // errors.As-navigable to MissingSecurityMiddlewareError,
    // MiddlewareShapeError, or DuplicateRouteError.
    Errors []RouteError
}

// RouteError pairs a route's identity with what went wrong on it.
type RouteError struct {
    Method string
    Path   string
    Err    error
}

func (e MultiRouteError) Error() string   // e.g. "3 routes failed validation: ..."
func (e MultiRouteError) Unwrap() []error // Go 1.20+ multi-unwrap — lets errors.As/Is reach into ANY individual route's error
func (e MultiRouteError) LogValue() slog.Value
```

`Unwrap() []error` (Go 1.20+'s multi-error unwrap support) rather than a
custom-only `Errors` field means a caller can `errors.As(err,
&missingErr)` DIRECTLY against the top-level `MultiRouteError` and it'll
find a match in ANY of the per-route errors — no manual loop required,
standard-library-idiomatic, consistent with every other typed error in
this design being `errors.As`-navigable.

## Decision: unexported handle-based primitive (resolves EH7's confirmed regression)

**Status: IMPLEMENTED.** `adapters/nethttp/
binding.go` (where every `ports` adapter lives) is in the SAME Go package
as `client.go` (where `Call` lives) — confirmed via `head -1` on both
files. This means the fix needs ZERO new public API surface: an
UNEXPORTED, handle-based call primitive, shared internally by BOTH the
new public `Call` and `ports`' adapters.

```go
// adapters/nethttp — UNEXPORTED. The actual call logic, unchanged from
// today's Call in every respect except its name/visibility. Takes an
// ALREADY-BUILT *rest.RouteHandle — no route.ClientHandle() call inside
// it at all.
func callWithHandle[Req, Resp any](ctx context.Context, c *Caller,
    handle *rest.RouteHandle[Req, Resp], req Req, opts CallOptions) (Resp, error)

// The new PUBLIC Call — calls route.ClientHandle() ONCE, then delegates.
// This is the ONLY new cost versus today, and only for callers using the
// public Call directly (a human calling occasionally) — not for ports,
// which never goes through this path at all.
func Call[Req, Resp any](ctx context.Context, c *Caller,
    route rest.Route[Req, Resp], req Req, opts CallOptions) (Resp, error) {
    return callWithHandle(ctx, c, route.ClientHandle(), req, opts)
}
```

`ports`' `binding.go` adapters are UNCHANGED in their own construction
pattern (still accept/build a `*rest.RouteHandle` once, at construction
time) — they simply call `callWithHandle` directly instead of the old
public `Call`, entirely inside the SAME package, invisible to any
external consumer. No performance regression, no new public surface, no
compromise left for this escape hatch.

## Decision: typed `RequestParams`/`ResponseParams` fields (eliminates EH8)

**Status: IMPLEMENTED — with one design correction found during
implementation.** Unlike `ServerImplementation.Fn` (which genuinely
cannot be made compile-time-typed — see Decision 6's Go-generics/layering
argument), `HeaderParam`/`CookieParam`/`QueryParam` are already concrete,
non-generic `api/rest` types. There is no obstacle to replacing the
type-erased `[]any` fields with typed ones — but this section's ORIGINAL
sketch (directly below, struck through) had an import-cycle bug: the
`middleware` package cannot import `api/rest` (`api/rest` already imports
`middleware`), so `[]rest.HeaderParam` cannot compile as a `middleware`
package field type.

```go
// ORIGINAL SKETCH — DOES NOT COMPILE, kept for history. middleware cannot
// import api/rest (api/rest already imports middleware for Middleware/
// ServerImplementation/etc.) — the exact same constraint that already
// keeps SecurityScheme/FromSecurityScheme split across the two packages.
type Middleware struct {
    Name           string
    Security       *SecurityDeclaration
    RequestHeaderParams  []rest.HeaderParam  // ← does not compile
    RequestCookieParams  []rest.CookieParam  // ← does not compile
    RequestQueryParams   []rest.QueryParam   // ← does not compile
    ResponseHeaderParams []rest.ResponseHeaderParam  // ← does not compile
    ResponseCookieParams []rest.ResponseCookieParam  // ← does not compile
}
```

**Shipped design: middleware-package-local typed spec structs +
api/rest bridge functions, mirroring `SecurityDeclaration`/
`FromSecurityScheme`'s existing split exactly.**

```go
// middleware package (middleware/params.go) — typed mirrors of
// rest.HeaderParam/CookieParam/QueryParam/ResponseHeaderParam/
// ResponseCookieParam's field shape, using only codex (which middleware
// already imports) — no rest import needed.
type HeaderParamSpec struct {
    Name, Description string
    Required           bool
    Codec              *codex.Codec[string]
}
// CookieParamSpec/QueryParamSpec/ResponseHeaderParamSpec/
// ResponseCookieParamSpec — identical shape, one type per kind.

// middleware package — Middleware's typed fields, using the spec types above.
type Middleware struct {
    Name                 string
    Security             *SecurityDeclaration
    RequestHeaderParams  []HeaderParamSpec
    RequestCookieParams  []CookieParamSpec
    RequestQueryParams   []QueryParamSpec
    ResponseHeaderParams []ResponseHeaderParamSpec
    ResponseCookieParams []ResponseCookieParamSpec
}

// api/rest package — bridge functions wrapping an EXISTING rest.XParam
// value into a Middleware, mirroring FromSecurityScheme's "wrap what you
// already have" pattern (also closes "Decision: generalized spec-
// declaring middleware constructors" below in the SAME change).
func FromHeaderParam(p HeaderParam) middleware.Middleware
func FromCookieParam(p CookieParam) middleware.Middleware
func FromQueryParam(p QueryParam) middleware.Middleware
func FromResponseHeaderParam(p ResponseHeaderParam) middleware.Middleware
func FromResponseCookieParam(p ResponseCookieParam) middleware.Middleware
```

**Eliminates `ParamContributionShapeError` entirely** — a wrong-type
entry becomes a Go compile error, not a runtime `errors.As`-navigable
one. `requestParamInfo`/`responseParamInfo`'s type-switch-over-`any`
logic is removed along with it.
`ConflictingParamContributionError`/its detection logic (escape hatch
#9) stays — the safety net for two DIFFERENT middlewares declaring the
SAME param name is still needed, it now simply operates over typed
fields instead of type-switched `any` entries.

## Decision: `HandleMW`/`ClientMW` unification (supersedes `.Implement()`, `Wrap`, and the original buggy `middleware.Wrap` draft)

**Status: IMPLEMENTED.** This is the THIRD
generation of this exact idea, each one superseding the last as a
deeper issue was found underneath the previous fix:

1. **Original `middleware.Wrap` draft** — buggy: `func Wrap(fn
   func(http.Handler) http.Handler) middleware.Middleware` — but
   `Middleware` is the PURE declare-time type (no `Fn` field at all, per
   the whole declare/implement split); it cannot carry `fn`.
2. **`.Implement(impl)` fix** — introduced a SEPARATE chainable method
   for declare-time-content-free implementations, since `.HandleMW(mw,
   fn)` was designed around "attach an implementation that SATISFIES a
   previously-`.Use()`'d declaration" and general-purpose implementations
   (`nethttp.Observability`, logging, rate-limiting) declare NOTHING —
   there was nothing for them to "match," so forcing them through
   `.Use(marker) → .HandleMW(marker, fn)` was ceremony with no payoff.
3. **THIS unification** — found while investigating `HandleMW`/`ClientMW`
   for a DIFFERENT, unrelated bug (neither guarded against `mw.Security`
   being `nil`, a genuine nil-pointer-panic risk — see "`HandleMW`-to-
   `.Use()` pairing validation"'s "Pass 3" above). Discussing the fix
   led to a simpler, better design: rather than adding a nil-guard THEN
   keeping `.Implement()` as a separate method, make `mw` itself NILABLE
   on `HandleMW`/`ClientMW` and FOLD `.Implement()`'s entire role into
   them — nil becomes the SANCTIONED signal for "nothing to pair
   against," not a misuse case. `.Implement()` is REMOVED, not
   deprecated-alongside.

**Confirmed there are actually TWO distinct general-purpose (empty
`Satisfies`) Fn shapes, both now unified under `HandleMW(nil, fn)`:**

| Shape | Runs | Can access `*Req`? | Constructor |
|---|---|---|---|
| `func(http.Handler) http.Handler` | Wraps the WHOLE handler, before body/param decode | NO — Req doesn't exist yet | None needed — pass the closure DIRECTLY to `HandleMW(nil, fn)` |
| `func(ctx, raw Raw, req *Req) (map[string][]string, error)`, empty `Satisfies`, returns `(nil, nil)` | After decode/merge, before the business handler | YES | `Transform` (see below) — returns the bare wrapped closure, not a `ServerImplementation` |

### The resolution

```go
// api/rest — HandleMW is the ONLY server-side implementation-attachment
// method (Implement is REMOVED). mw is NILABLE:
//   - non-nil AND mw.Security != nil: PAIRED, security case, unchanged
//     mechanics (Satisfies derived from mw.Security.SchemeName).
//   - nil (or mw.Security == nil): UNPAIRED, general-purpose — Satisfies
//     empty, fn runs unconditionally. Replaces EVERYTHING .Implement()
//     used to cover.
func (r Route[Req, Resp]) HandleMW(mw *middleware.Middleware, fn any) Route[Req, Resp]

// ClientMW — identical unification, client-side (see "Client-side
// Satisfies-gated implementations" above).
func (r Route[Req, Resp]) ClientMW(mw *middleware.Middleware, fn any) Route[Req, Resp]
```

**`Wrap` is REMOVED entirely** — a caller passes their `func(http.Handler)
http.Handler` closure DIRECTLY to `HandleMW(nil, myWrapFn)`; there is
nothing left for a separate `Wrap` constructor to add.

**`middleware.Scopes[Raw, Req]`, `nethttp.Scopes[Req]`, and
`nethttp.APIKey[Req]` are ALSO REMOVED entirely — found during
implementation planning.** All three exist ONLY to wrap a raw
extract/verify closure into a `middleware.ServerImplementation{Satisfies,
Fn}` — that exact wrapping now happens INSIDE `HandleMW` itself
(deriving `Satisfies` from `mw.Security.SchemeName` when `mw.Security !=
nil`, empty otherwise). A caller now passes the raw extract/verify
closure straight to `HandleMW`/`ClientMW`, e.g.
`route.HandleMW(&mw, extractScopesFn)` instead of
`route.Implement(nethttp.Scopes[Req]("bearerAuth", extractScopesFn))` —
one fewer indirection, and one fewer constructor per Raw/Req pinning to
maintain.

**`Transform` SURVIVES, but simplifies** — instead of returning a full
`ServerImplementation`, it now returns the BARE wrapped closure directly,
since `HandleMW` itself builds the `ServerImplementation` internally now:

```go
// middleware package — adapter-agnostic core. Returns `any` (the bare
// wrapped closure), NOT a ServerImplementation — HandleMW/ClientMW build
// that internally from whatever `fn` (and `mw`) they receive.
func Transform[Raw, Req any](fn func(ctx context.Context, raw Raw, req *Req) error) any {
    return func(ctx context.Context, raw Raw, req *Req) (map[string][]string, error) {
        return nil, fn(ctx, raw, req)
    }
}

// nethttp/chi — pins Raw = *http.Request:
func Transform[Req any](fn func(ctx context.Context, r *http.Request, req *Req) error) any

// usage:
route = route.HandleMW(nil, nethttp.Transform(func(ctx context.Context, r *http.Request, req *Req) error {
    req.CorrelationID = r.Header.Get("X-Correlation-ID")
    return nil
}))
```

**`nethttp.Observability` gets the SAME simplification** — instead of
returning a `ServerImplementation`, it now returns the bare
`func(http.Handler) http.Handler` closure directly:

```go
// nethttp — simplifies alongside Wrap's elimination.
func Observability(obs stats.Observer) func(http.Handler) http.Handler

// usage:
route = route.HandleMW(nil, nethttp.Observability(obs))
```

(The "three core types" section's mention of `Observability(obs)
middleware.ServerImplementation` near the TOP of this doc describes
TODAY's ACTUALLY-SHIPPED signature — historical, unaffected by this
decision, not a stale cross-reference.)

**`Transform`'s stored `Fn` shape still WRAPS into the EXISTING security
shape, does NOT introduce a third one** — unaffected by this
unification, still true for exactly the same three reasons as before:

1. **Zero dispatch-loop changes.** `runSecurityMiddleware` already
   handles "empty `Satisfies`, returns `(nil, nil)` on success" correctly
   today — that IS the general-purpose gating rule from Step 3. A
   genuine third stored shape would require updating
   `validateImplementationShapes`'s type-switch AND adding a new,
   separate dispatch pass — neither is needed.
2. **Correct interleaving with security Fns, for free.** Since
   `Transform`-built and `HandleMW`-built (security) implementations
   share the IDENTICAL stored shape and land in the SAME `impls` slice,
   they naturally interleave in ATTACHMENT ORDER through the ONE existing
   dispatch loop — a `Transform` deriving `req.TenantID` can run before
   or after a security Fn reading it, purely by `HandleMW` call order on
   the route. A genuine third shape, needing its own separate pass,
   would break this ordering guarantee entirely.
3. **`Transform`'s public signature stays simpler than the stored
   shape.** A caller writing a transform never needs to know or care
   about the `map[string][]string` grants machinery — `Transform`
   absorbs that entirely; only `middleware`-internal code (and
   `nethttp`/`chi`'s Raw-pinned wrapper) ever sees the wrapped form.

### Bonus: this fully answers a question left open in an earlier round

"Does the client side need its own `.Implement()`-equivalent?" (flagged
as explicitly deferred in "Client-side `Satisfies`-gated implementations"
above) — YES, and `ClientMW(nil, fn)` IS that equivalent. No separate
method needed; the same unification closes both sides at once.

## Decision: generalized spec-declaring middleware constructors (the third sub-case of "general custom middleware")

**Status: IMPLEMENTED — with the SAME import-cycle correction as the
previous section, and the constructors relocated to `api/rest` instead of
`middleware`.** Reviewing the "may or may not add to headers/cookies"
sub-case of general custom middleware found that the (now-removed)
`nethttp.HeaderParam` (declare-time half of the API-key story)
generalizes cleanly beyond that framing — and was missing several
obvious siblings. The original sketch below proposed relocating it INTO
`middleware` as `middleware.HeaderParam(p rest.HeaderParam) Middleware`
— this has the exact same import-cycle bug as the previous section's
original sketch (`middleware` cannot import `api/rest`).

```go
// ORIGINAL SKETCH — DOES NOT COMPILE, kept for history.
func HeaderParam(p rest.HeaderParam) Middleware  // ← does not compile in package middleware
func CookieParam(p rest.CookieParam) Middleware  // ← does not compile
func QueryParam(p rest.QueryParam) Middleware    // ← does not compile
func ResponseHeaderParam(p rest.ResponseHeaderParam) Middleware  // ← does not compile
func ResponseCookieParam(p rest.ResponseCookieParam) Middleware  // ← does not compile
```

**Shipped as `api/rest.FromHeaderParam`/etc. instead** — these are the
EXACT SAME functions introduced in "Decision: typed `RequestParams`/
`ResponseParams` fields" above; this section's "generalize beyond
HTTP-specific framing" goal and that section's "typed fields" goal turned
out to be the SAME change, closed together:

```go
// api/rest package (api/rest/middleware.go) — wraps an EXISTING
// rest.XParam value directly, mirroring FromSecurityScheme's "wrap what
// you already have" pattern. Lives in api/rest, not middleware, for the
// SAME import-cycle reason FromSecurityScheme does.
func FromHeaderParam(p HeaderParam) middleware.Middleware
func FromCookieParam(p CookieParam) middleware.Middleware
func FromQueryParam(p QueryParam) middleware.Middleware
func FromResponseHeaderParam(p ResponseHeaderParam) middleware.Middleware
func FromResponseCookieParam(p ResponseCookieParam) middleware.Middleware
```

`adapters/nethttp/scopes.go`'s old `HeaderParam(headerName string)`
(which only took a bare NAME, not a full `rest.HeaderParam` value, and had
zero call sites anywhere in the repo) was REMOVED entirely in favor of
`rest.FromHeaderParam(rest.HeaderParam{Name: headerName, Required: true})`.

Naming note: `rest.FromHeaderParam` (function, returns `middleware.Middleware`)
intentionally sits alongside `rest.HeaderParam` (struct type) in the SAME
package — the SAME naming pattern already used for `rest.FromSecurityScheme`/
`rest.SecurityScheme`.

### The full workflow for this case — declare and implement are DECOUPLED, no matching needed

```go
// DECLARE — pure spec, wraps an existing rest.HeaderParam value directly.
corrIDMw := rest.FromHeaderParam(rest.HeaderParam{
    Name: "X-Correlation-ID", Required: true, Codec: &uuidCodec,
})
route := rest.NewRoute[Req, Resp](...).Use(corrIDMw)

// IMPLEMENT — completely OPTIONAL, and has NO reference back to corrIDMw
// at all — unlike security's HandleMW(mw, fn) pairing, there is nothing
// here to "satisfy." Uses HandleMW's UNIFIED, nil-mw form (see
// "Decision: HandleMW/ClientMW unification" below) — .Implement() no
// longer exists as a separate method.
route = route.HandleMW(nil, nethttp.Transform(func(ctx context.Context, r *http.Request, req *Req) error {
    req.CorrelationID = r.Header.Get("X-Correlation-ID")
    return nil
}))
```

**Confirmed: no coverage check is needed for this case, and this is a
DIFFERENT risk profile than security, not an oversight.** `rest.CheckCoverage`
operates PURELY over `secReqs []route.SecurityRequirement` — it has ZERO
awareness of general param declarations, today or in anything decided
this session. Declaring a header for OpenAPI documentation purposes with
NO custom runtime behavior is completely legitimate: the framework's own
pipeline auto-validates format via `ValidateHeaders` regardless of
whether any custom `Fn` exists for it. This is unlike security, where
"declared but unenforced" is a genuine hole (an unauthenticated caller
gets through) — here, the worst case is a validated-but-otherwise-unused
declaration, not a security incident. `.Use(mw)` and `.HandleMW(nil,
...)` for this case correctly need NO shared reference between them.

---

## Review round: PathParams/HeaderParams/CookieParams and middleware chaining

A dedicated review of how `PathParam`/`HeaderParam`/`CookieParam` merge
fields, and middleware-to-middleware "chaining," interact with the
redesigned workflow (Decisions 1–4) and the "one struct, one call"
principle — since none of the decisions above explicitly addressed this,
it's covered here as its own review pass.

### Baseline: route-level merge fields are fully unaffected

The 9-layer request pipeline (body → query → cookie → header → path →
security) runs BEFORE any `HandleMW`-attached `Fn` executes — by the time
the FIRST Fn runs, `Req` is ALREADY fully merged from every route-declared
`rest.NewPathParam`/`NewRequiredHeaderParam`/etc. merge field. None of
Decisions 1–8 touch this pipeline ordering or the merge-field mechanism
itself. "One struct, one call" at the HANDLER boundary — `func(ctx, req
Req) (Resp, error)`, everything the handler needs already merged into
`req` — is completely untouched by this whole redesign.

### Middleware-contributed `HeaderParam`/`CookieParam` stay validate-only — confirmed deliberate, not a gap

`Middleware.RequestParams`/`ResponseParams` (typed, per "Decision: typed
`RequestParams`/`ResponseParams` fields" above) can only ever produce
spec-level, format-validated entries — never merge fields — because
`Middleware` isn't generic over `Req` (it's reused across routes with
different `Req` types; a merge field needs a concrete getter/setter
pair). This was initially suspected to be a fixable gap, but the
ORIGINAL `declarative-middleware.md` design doc already explicitly
rejected exactly that fix, for a correctness reason, not an oversight:
wiring a VERIFYING/transforming codec (e.g. one that decodes a bearer
token into a `Claims` struct) through a route-declared merge field
renders the WRONG OpenAPI schema — the spec would document the header's
schema as the DECODED shape (`Claims`, an object with `sub`/`exp`/etc.),
not "a bearer token string." **Confirmed still correct, unaffected by
anything decided this session — no change needed.**

### The actual chaining mechanism — already designed, fully preserved by `HandleMW`

`HandleMW`'s `fn` — matching the shape `func(ctx context.Context, raw
Raw, req *Req) (map[string][]string, error)` — is UNCHANGED from
today's security-verifying `Fn` shape, and this IS the "chain middlewares
accessing/setting header or cookie data" mechanism:

1. A Fn reads the raw value directly off `Raw` (e.g. `r.Header.Get(...)`,
   `r.Cookie(...)`).
2. It can decode+verify via ITS OWN dedicated codec, using the
   already-public `codex.DecodeVars`/`RequiredField`/`OptionalField` —
   zero new API needed (e.g. real JWT verification producing a `Claims`
   value from the raw `Authorization` header).
3. It writes the result directly onto `*req` — visible to the business
   handler, and to any LATER-attached Fn on the SAME route, since every
   `HandleMW`-attached Fn shares the SAME `*Req` pointer and runs in
   ATTACHMENT ORDER. This is genuine chaining: Fn2 (attached after Fn1)
   can read whatever Fn1 already wrote.

None of this changes under `HandleMW` — the Fn signature, the pointer
sharing, and the attachment-order execution are IDENTICAL to today's
mechanism, just reached via a different attachment API (`.HandleMW()`
chained on `Route` instead of a raw `impls` variadic passed to
`nethttp.Register`).

### Response-side asymmetry — necessary, not a flaw

`Resp` doesn't exist yet when any `HandleMW`-attached Fn runs (Fns
execute BEFORE the business handler) — so response-side contributions
cannot use a `*Resp` pointer-write the way request-side Fns use `*Req`.
They go through the EXISTING ctx-based `WithResponseHeaders`/
`WithResponseCookies` mechanism instead. This asymmetry is REQUIRED by
Resp's lifecycle, not a design gap, and is unaffected by this session's
decisions.

### `PathParam` specifically

Middleware CANNOT contribute new `PathParam` declarations (only
`HeaderParam`/`CookieParam`/`QueryParam` are recognized by
`requestParamInfo`/the typed fields from the EH8 decision) — a route's
path template is fixed once, at `NewRoute(method, path, ...)` time, and
this is unaffected. A Fn CAN read an already-merged path-derived `Req`
field (e.g. checking a JWT's subject claim against the path's `:id` —
a common authorization pattern) since path merge fields are applied
before any Fn runs, same as header/cookie merge fields.

### The one genuine gap: no conflict detection for RUNTIME `*Req` writes — RESOLVED as an accepted limitation, documented via convention

Every SPEC-level disagreement in this design is a loud, immediate error
(`ConflictingSecurityDeclarationError`, `ConflictingParamContributionError`).
If TWO chained Fns both write to the SAME `Req` field with different
values, there is no equivalent check — plain Go mutation, silent
last-write-wins. Considered adding field-ownership tracking to close
this, and REJECTED, for principled reasons, not just convenience:

1. **Fundamentally different in kind from every other conflict check.**
   Every existing check operates on DECLARATIVE metadata — a list of
   typed values the framework can iterate and compare BEFORE any code
   runs. A runtime `*Req` write is an arbitrary line inside a
   user-supplied Go closure; there is no declarative surface to check
   against, and Go gives no cheap way to know "which fields will this
   closure write to" without executing or instrumenting it.
2. **Any real fix would reintroduce exactly what "Decision: typed
   `RequestParams`/`ResponseParams` fields" just eliminated.** Detecting
   this would require `*Req` direct-field-access to become a
   tracked/audited write path (e.g. `req.Set("TenantID", value)` instead
   of `req.TenantID = value`) — the same reflective/type-erased
   machinery this session moved AWAY from elsewhere, reintroduced here
   to catch a narrower problem.
3. **Lower real-world risk than the spec-level case.** Two middleware
   VALUES accidentally sharing a scheme-name STRING (the spec-level
   case) can genuinely happen across independent authors/packages that
   have never seen each other's code. Two Fns writing the SAME `Req`
   field requires ONE person to have deliberately attached both to a
   route whose struct they also defined — closer to "a bug in your own
   wiring code" than "two strangers' declarations coincidentally
   clashed."
4. **Cost/benefit does not favor building this.** Reflection-based
   diffing of `Req` after every attached Fn, on every request, is real
   per-request overhead for a scenario that is rare and self-inflicted.

**Resolution: leave undetected, close the gap via documented convention
instead of enforcement.** When implemented, `docs/concepts/middleware.md`
(and this doc, for now) should recommend: give each Fn a DISTINCTLY-NAMED
`Req` field when contributing derived data (e.g. `TenantIDFromJWT` vs.
`TenantIDFromHeader`, never a shared generic name two unrelated Fns might
both reach for), and reach for `middleware.ContextField[V]` instead of a
shared `Req` field when multiple Fns genuinely need to communicate a
value — `ContextField` at least makes the sharing EXPLICIT rather than
an implicit same-field collision.

### Overall verdict

"One struct, one call" is FULLY PRESERVED at the handler boundary: by
the time the business handler runs, `req` carries every route-declared
merge field's value AND whatever any attached Fn additionally derived
and wrote, all in ONE struct, via ONE handler call signature — no ctx
lookup required for anything modeled as a `Req` field. The mechanism
achieving this (multiple Fns mutating shared state before the handler
runs) is an internal implementation detail invisible to the handler
author. Nothing in Decisions 1–8 changes this guarantee, weakens it, or
strengthens it — it was already correctly designed, and remains so.

---

## Open questions for a simplification pass

These are raw observations framed as QUESTIONS — nothing here is decided.
Bring your own priorities; this is the starting point for a discussion,
not a recommendation.

1. **RESOLVED — see "Decision: whole-API declarative wiring" above.**
   ~~Is three separate constructors for one concept worth its cost?~~
   `SecurityScheme` + `Scopes` + a route needing BOTH `.Use()` and the
   adapter's variadic is more ceremony than the OLD bundled
   `RequireScopes` for the common case (a route this codebase both
   declares AND enforces). The split only pays off for the
   external-API-description case (escape hatch #2).

2. **RESOLVED — see "Decision: whole-API declarative wiring" above**
   (`HandleMW(mw, fn)` takes the ACTUAL declared `mw` value, not a
   re-typed string — the typo risk described below is structurally
   closed). ~~Should the scheme-name string have a compile-time link
   between declare and implement?~~ `"bearerAuth"` appears in
   `SecurityScheme(...)`, `Scopes(...)`, and implicitly in `Satisfies` —
   no compile-time connection; a typo silently produces "declared but
   never covered," caught only at `Register`/`RegisterSSE` time.

3. **RESOLVED — see "Decision: symmetric client-side declarative wiring"
   above.** ~~Is the ambient-vs-override client wiring choice discoverable
   enough?~~ `Route.UseClient` (ambient default) vs. passing directly to
   `CallVia` (one-off override) had no single obvious default — there
   were actually THREE stacking layers, not two (route-level, Caller-level,
   per-call) — all three now collapse into ONE (`.ClientMW()`, declared on
   the route, no per-call override).

4. **RESOLVED — see "Decision: `Serve` is the only public server-side
   entry point" above.** ~~Is the `Handler`/`Register` validation
   asymmetry a trap?~~ Reaching for "just give me an `http.Handler`"
   (`Handler`/`SSEHandler`) silently opted out of BOTH the shape check
   and the coverage check — this asymmetry is now CLOSED entirely:
   `Handler`/`Register`/`SSEHandler`/`RegisterSSE` have been DELETED from
   both `adapters/nethttp` and `adapters/chi`; `Serve`/`ServeOne`/
   `ServeSSE` (always fully checked, including the coverage check — see
   "Lessons Learned" below for a regression this closure itself caused
   and fixed) are the ONLY remaining public server-side entry points.

5. **RESOLVED — see "Decision: symmetric client-side declarative wiring"
   above (closed as a side effect of resolving Question 3).** ~~Four
   client-side entry-point functions — too many?~~ `Call`/`CallHandle`/
   `CallVia`/`CallHandleVia` collapse into ONE `Call(ctx, caller, route,
   req, opts)` function that takes the `Route` directly and always
   auto-derives merge fields.

6. **RESOLVED — see "Decision: `any`-typed `Fn`/`fn` parameters are the
   correct tradeoff, not a gap" above.** ~~Are `any`-typed `fn`/`Fn`
   parameters the right tradeoff?~~ `ServerImplementation.Fn`, and
   `HandleMW`'s/`ClientMW`'s `fn any` parameters, all sacrifice
   compile-time safety for adapter-agnosticism — CONFIRMED as the only
   design that satisfies both Go's no-generic-methods constraint and
   `api/rest`/`middleware`'s adapter-agnostic layering simultaneously; no
   API change follows from this question.

---

## Lessons Learned (post-implementation retrospective)

This section is added AFTER the design above was fully implemented and the old
doors (`Handler`/`Register`/`SSEHandler`/`RegisterSSE`) were deleted — it
records what actually went wrong during execution that the design and its
review passes did NOT anticipate, for future maintainers planning a
similarly-shaped removal (an old, multi-responsibility public API replaced by
a new one). None of this changes the shipped design; it changes how the NEXT
one should be planned and reviewed.

### 1. "Equivalent" claims about generated/reflective code need runtime proof, not review sign-off

The roadmap doc's own "Decision: `Serve`'s generic dispatch mechanism" section
stated plainly that `buildRouteHandler` "runs the SAME pipeline `Handler`
runs, invoked via `reflect.Value.Call` instead of static generic calls." This
was written in good faith, reviewed multiple times, and was **false** — the
reflect dispatch never implemented request/response `Formats` content
negotiation, and never called `ErrorResponseFor` on the handler-error path.
Both gaps were silent: `Serve`/`ServeOne` built successfully, wired
successfully, and served ordinary requests successfully. They only surfaced
when the OLD test suite (128+ tests exercising `Handler`/`Register` directly)
was migrated wholesale onto the new entry points and specific tests started
failing. A smaller, incremental migration (a handful of tests at a time,
declared "good enough" after each batch passed) would very plausibly have
shipped with these gaps permanently baked in and undetected.

**Takeaway:** when a design claims a new mechanism is a drop-in equivalent for
an old one, that claim is a hypothesis, not a fact, until every existing
caller of the old mechanism has been ported and re-verified against it. Plan
for "migrate everything, then see what breaks" as the actual verification
step — not code review of the new mechanism in isolation.

### 2. Deleting an old function can silently delete a responsibility nobody tracked as separate

`Register`/`RegisterSSE` did two things: wire the handler onto the
mux/router, AND call `rest.CheckCoverage` to reject a route whose declared
security scheme had no matching implementation. The roadmap's planning and
every review pass treated "delete `Register`/`RegisterSSE`" as pure code
removal of the wiring responsibility — nobody re-derived the full list of
side effects the old function had, so the coverage check's removal was
invisible until a dedicated regression test was written specifically to
probe it (see the `review-go-codex` Round 125 finding G4). Until that test
existed, `Serve`/`ServeSSE` would silently wire a misconfigured security
route with zero error, deferring the failure to every individual runtime
request instead of failing loudly at wiring time.

**Takeaway:** before deleting a function, enumerate ALL of its responsibilities
— not just the one implied by its name or its most obvious call site — and
verify each one has an explicit new home. A multi-responsibility function is
a bundle; deleting the bundle without unbundling it first drops whatever
wasn't the primary focus of attention.

### 3. Documentation is not verified by any build step — it rots silently and at scale

Deleting 4 exported functions left 31 dangling godoc `[Symbol]` bracket-links
across 7 Go files, plus stale, non-compiling code snippets in 8 separate
`docs/*.md` pages, 2 examples' leftover explanatory comments, a README table,
and 2 of this repo's own skill files — none of which `go build`, `go vet`,
`go test`, or `staticcheck` ever flagged, because none of them parse Markdown
or resolve godoc comment syntax. This required TWO separate full-repo grep
sweeps (one that caught the `adapters/` package files, a second deeper pass
that caught everything else) after the code-level work was already declared
"done and verified."

**Takeaway:** "the build is green" is necessary but not sufficient evidence
that a symbol removal is complete. Any plan that removes an exported symbol
must include an explicit, scheduled step to grep the ENTIRE repository — not
just the package being changed — for the symbol's name, across `*.go` doc
comments, `docs/**/*.md`, `examples/**/*.go`, and `.github/**/*.md`.

### 4. The removal's true blast radius was far larger than what was planned for

The original roadmap doc's status banner tracked the removal's cost as "128+
combined test call sites" (83 in `adapters/nethttp/adapter_test.go` + 1 in
`stream_test.go` + 44 in `adapters/chi/adapter_test.go`) — a code-only,
test-only count. The actual cleanup touched: those 128 test call sites, 7
examples using the old doors directly, 1 example (`sensor-service`) with a
structurally different blocker entirely, 3 Go source files' doc comments
outside `adapters/`, 8 `docs/*.md` pages, 2 examples' stale explanatory
comments, a README, and 2 skill files — a genuinely larger surface than the
plan's own cost estimate, discovered only by working through it rather than
by the original scoping pass.

**Takeaway:** when scoping the cost of an API removal, the code-call-site
count is a floor, not a ceiling. Documentation, examples, and tooling
configuration that reference the symbol are real, uncounted cost — budget for
a discovery pass, not just the known call sites, before declaring a removal
"small" or "large."

### 5. Curated test suites don't surface every real-world integration shape

Every existing unit test exercising `Handler`/`Register` assumed the ordinary
shape: build a route, attach a handler, register it. `examples/sensor-service`
did something none of the tests did: declare routes as PACKAGE-LEVEL VARS via
`RegisterHandle` at Go `var`-init time, then attach the real handler LATER in
`main()` once a runtime dependency (a database `store`) existed — an ordering
the new `Serve`/`ServeOne` design had implicitly assumed away ("`WithHandler`
before `Register`"). This wasn't found by reading the roadmap doc, the
tests, or any other example — it was found only by attempting to actually
migrate that ONE real, "in the wild" example. It required a genuinely new,
small piece of API (`RouteHandle.WithHandler`/`SSERouteHandle.WithHandler` as
`Route.WithHandler`'s post-registration equivalent) that the original design
never anticipated needing.

**Takeaway:** a representative-looking test suite is not a substitute for
migrating every REAL consumer of an API being changed, including the ones
that don't look like the tests. Structural assumptions ("handler always
exists before registration") should be treated as unverified until every
existing example — not just every existing test — has been checked against
them.

### 6. Independent, parallel exploration finds bugs a targeted review doesn't

While migrating `examples/adapters-templ` (one of several migrations run as
parallel background agents, each independently exercising a different
example's code paths), one agent found that `negotiateFormatReflect` (SSE's
Accept-header format negotiation, shared with `Serve`'s own dispatch) failed
to strip `;`-parameters from a content-type before comparing it against the
`Accept` header — a bug unrelated to either of the 2 gaps this round's review
was explicitly looking for. It was caught only because that agent's specific
example exercised `Accept: text/html` against a format whose `ContentType()`
included `; charset=utf-8`, a case the review's own test-writing hadn't
targeted.

**Takeaway:** running independent migrations/explorations in parallel, each
against a genuinely different real consumer, finds classes of bugs a single
reviewer working through a fixed checklist will not — because the checklist
can only test for what someone already thought to ask about.

### 7. Environment reliability assumptions should be made explicit and have a fallback

`ask_user` calls made throughout this implementation frequently returned
"user not available" — including at moments intended to confirm significant,
hard-to-reverse decisions (e.g., whether to accept feature loss vs. fix the 2
Serve gaps before deleting the old doors). The practical resolution was an
explicit fallback policy: when `ask_user` is unavailable, choose the more
conservative option, state the assumption clearly in the response, and keep
working rather than blocking. This was not planned for at the start of the
implementation and had to be improvised mid-session.

**Takeaway:** any implementation plan spanning a long session should assume
interactive confirmation may be unavailable at the moment it's needed, and
should pre-decide a conservative default for its highest-stakes open
questions rather than relying on being able to ask in the moment.

---

## Addendum: this design as the foundation for SSE client consumption

Added after the SSE Client Consumption roadmap doc (`docs/roadmap/
sse-client-consumption.md`) shipped its Phase 1 (a separate feature, planned
and implemented in a later session) — recorded here so the lineage is
discoverable from this document, since the newer feature's own roadmap doc
did not itself narrate where its core mechanism came from. That roadmap doc
has SINCE BEEN DELETED (per its own 3-way delete/keep/promote graduation
policy — a single-feature roadmap doc, fully shipped, with a confirmed
zero-gap review, and no lasting cross-cutting design value of its own beyond
what is captured here); this addendum is the durable record of the design
lineage that remains after that deletion.

`SSERoute.ClientHandle()`/`ClientMW()` — the pair that lets a single
`rest.SSERoute` declaration serve BOTH the server side (`ServeSSE`) and the
client side (`Client.Consume`/`CallSSEAdapter`, per Addendum 4 below —
originally `nethttp.Consume`, since retired) with no separate
declaration — is not a new design. It is a direct, mechanical application of
two decisions already made and shipped by THIS document:

- **"Decision: symmetric client-side declarative wiring"** (above) is what
  established `Route.ClientHandle()`/`ClientMW()` as the client-side mirror
  of `Register`/`HandleMW` in the first place, for the plain request/response
  `Route` case. `SSERoute.ClientHandle()`/`ClientMW()` apply the identical
  pattern to the stream case — same struct-literal-construction shape, same
  infallible-with-panic-on-type-mismatch contract, same
  `applyMiddlewareSecurityForClient` reuse for security scheme parity with
  the server side.
- **"Decision: unexported handle-based primitive"** (above) is what
  established `callWithVars`/`CallWithHandle` as the layering `Call` is
  built on. `adapters/nethttp/binding.go`'s `consumeSSE`/`consumeSSEOnce`
  primitive that both `Consume` (route-based convenience) and
  `CallSSEAdapter` (handle-based, port-adapter-facing) delegate to is the
  same layering shape, one level up (a long-lived reconnecting loop instead
  of a single call), for exactly the same reason: one primitive, two public
  entry points at different levels of convenience.

A dedicated `review-go-codex` code-review pass (Round 127, prompted by the
user's explicit request to check `sse-client-consumption.md` against its
implementation for open gaps) confirmed **zero gaps** between that roadmap
doc's committed scope and the shipped code, tests, and documentation — every
API surface item, unit-test-plan item, and "in scope"/"out of scope" line
was verified present and correct, and the "out of scope" `Last-Event-ID`/
pluggable-retry-policy items were confirmed correctly carried forward into
their own follow-on roadmap doc (`docs/roadmap/sse-resume-and-retry-policy.md`)
rather than silently dropped. This is presented as evidence that the two
Decisions above generalize cleanly to a second, structurally different
boundary (a long-lived stream instead of a single request/response) without
needing new design work — the strongest practical validation this document's
core patterns could receive short of a third independent adapter adopting
them.

## Addendum: `Client.Attach`'s Observer + `ErrorPattern` parity fix

Added after a dedicated review of error handling and `stats.Observer`
integration across REST and events under the "thin adapter" `Client.Attach`
workflow (Decision 5, this doc's own `nethttp.Attach`/`AttachMux` design),
triggered by a direct user request. Companion events-side fix documented in
`docs/design/d-0002-pubsub-workflow-simplification.md`'s Decision 8.

**Confirmed via code inspection: server-side `Attach` had NO gap.**
`nethttp.AttachMux`/`chi.AttachRouter` delegate straight to the existing
`serve`/`serveSSE` functions — the SAME functions the pre-`Attach` API
surface already used — so `ErrorPattern` and `stats.Observer` wiring were
never at risk on the server side; `serverTransport.Serve` (see this doc's
own Revision 2 "Decision: unexported handle-based primitive" section) is a
pure wiring wrapper, not a reimplementation.

**Confirmed bug: `nethttp.Attach`'s CLIENT-side `clientTransport.Call`
called `stats.Observer` NOWHERE AT ALL, and did NOT consult a declared
`rest.ErrorPattern` on a non-2xx response** — it reimplemented the HTTP
call from scratch (a hand-rolled `http.NewRequestWithContext`/`client.Do`/
`io.ReadAll` sequence) rather than delegating to the existing, already-
correct `callWithVars` (this doc's own Decision: unexported handle-based
primitive). This meant every call made through the "preferred" `Client.
Attach` workflow silently dropped ALL metrics/logging/tracing, AND lost the
declarative typed-error-decode `ErrorPattern` promises entirely — falling
back to a bare `UnexpectedStatusError` even when a route declared a
matching `rest.ErrorPattern`.

**Fix**: `clientTransport.Call` now resolves `obs :=
stats.ObserverFromContext(ctx)` (ctx-only — this reflection shim has no
per-call `CallOptions`-equivalent struct to carry an explicit override,
matching its own documented v1 scope) and calls `obs.RecordRequest(method,
path, statusCode, duration)` on EVERY exit path — status 0 before a
response is ever received (pre-flight/build/network failures), mirroring
`callWithVars`'s own convention exactly. `TraceObserver` span start/end is
wired identically (`"http.request"` span name, matching `callWithVars`'s
own). On a non-2xx response, `clientTransport.Call` now calls
`handle.DecodeErrorFor(statusCode, respBody)` (the client-side counterpart
of `RouteHandle.ErrorResponseFor` — see this doc's own error-pattern
sections) via the SAME reflection technique already used for `BuildPath`/
`EncodeRequest`/`DecodeResponse`, returning the typed `ErrorPatternResponse`
on a match, falling back to `UnexpectedStatusError` unchanged otherwise —
mirroring `callWithVars`'s own step 11 exactly.

**Why this was missed originally**: `clientTransport.Call` was written as
a from-scratch reflection shim (necessarily so, since `rest.Client.Call`'s
`any`-typed signature requires runtime type recovery this doc's Decision 5
already establishes) rather than delegating to `callWithVars` — a
reasonable-looking choice at the time, since the CORE request/response
mechanics (encode, build URL, decode) had to be reflection-driven anyway.
But this meant the Observer/ErrorPattern wiring — which does NOT need
reflection, since `obs` and the error-decode call are both resolved via
already-concrete values (`stats.ObserverFromContext(ctx)` returns a
concrete `stats.Observer`; `handle.DecodeErrorFor` is called via reflection
only because `handle`'s own concrete type is runtime-only, not because the
call itself needs anything special) — was simply never added, since there
was no existing "add this one wiring block" pattern to notice was missing.
**Lesson, consistent with this doc's own "Lessons Learned" section**: a
reflection-based shim built to solve ONE structural problem (recovering a
generic type at runtime) can silently regress OTHER, unrelated concerns
(observability, declarative error handling) that have nothing to do with
the reflection problem itself, if the shim is written as a fresh
implementation instead of a thin wrapper delegating to the already-correct
non-reflection-blocked pieces.

**Verification**: new tests in `adapters/nethttp/clienttransport_test.go`
cover `RecordRequest` on success (with the real status code) and network
failure (status 0), plus a matched `ErrorPattern` returning
`ErrorPatternResponse` and an unmatched non-2xx status falling back to
`UnexpectedStatusError` unchanged. `gofmt`/`go build`/`go vet`/`go test`
all green; zero regressions in the existing `TestAttach_*`/`TestCall_*`
suites.

## Addendum 2: `Client.Call`'s declared-format gap, fixed by centralizing resolution on `RouteHandle`

A second, structurally identical gap surfaced while proving the Observer/
`ErrorPattern` fix above via example rework. The identical fix also
landed on the events side in the same round — see
`docs/design/d-0002-pubsub-workflow-simplification.md`'s Decision 9 for the
events-specific detail (`ChannelHandle[T]`'s mirror-image methods).

**Confirmed bug**: `clientTransport.Call`'s request-encode/response-decode
steps ALSO always called `RouteHandle[Req,Resp]`'s plain
`EncodeRequest`/`DecodeResponse`, which are hardcoded to JSON codec
encode/decode by original design (their own doc comments say so
explicitly), silently ignoring a route's declared
`WithFormats`/`WithRequestFormats` (YAML, TOML, Gob, custom binary) —
while `callWithVars` (the existing, already-correct escape-hatch
primitive) resolved this correctly by duplicating the resolution logic
inline at its own call site ("call-time override > declared
RequestFormats/Formats > plain EncodeRequest/DecodeResponse"). A route
declared with a non-JSON format would silently break the moment a caller
switched from the escape hatch to `Client.Attach`. This is **not** about
per-call format overrides (which stay a legitimate client-side
`CallOptions` concern, untouched) — it is about the route's own
**declared** format, the single most basic contract a handle makes about
its own wire shape.

**Fix**: rather than teach `Call` to duplicate `callWithVars`'s resolution
logic a second time, the logic moved **onto `RouteHandle[Req,Resp]`
itself** — the single source of truth for a route's own declared
configuration. REST's format model has only two levels (no three-level
Subscribe/Publish/Formats chain like events) — override > declared, no
further fallback chain needed. Three new canonical methods
(`api/rest/builder.go`):

```go
// EncodeRequestWithFormats resolves formats (call-time override) >
// declared RequestFormats > plain EncodeRequest, returning the matching
// Content-Type alongside the bytes (a pre-flight concern, needed before
// sending the request).
func (h *RouteHandle[Req, Resp]) EncodeRequestWithFormats(req Req, formats ...format.Format[Req]) (body []byte, contentType string, err error)

// ResponseFormat resolves the SAME priority for the response direction,
// returning only the resolved format's ContentType — used for the Accept
// header, a pre-flight concern separate from decode (a post-flight
// concern), hence its own method.
func (h *RouteHandle[Req, Resp]) ResponseFormat(formats ...format.Format[Resp]) string

// DecodeResponseWithFormats resolves formats > declared Formats > plain
// DecodeResponse.
func (h *RouteHandle[Req, Resp]) DecodeResponseWithFormats(body []byte, formats ...format.Format[Resp]) (resp Resp, acceptContentType string, err error)
```

BOTH `callWithVars` and `Call` now call these methods, deleting
`callWithVars`'s own inline duplicated copy in the process:
`adapters/nethttp/client.go`'s `callWithVars` request-encode/response-
decode steps now call `handle.EncodeRequestWithFormats`/
`handle.DecodeResponseWithFormats`, passing the existing
`resolveCallFormat`-derived override through unchanged (still a
client-side `CallOptions` concern); `adapters/nethttp/clienttransport.go`'s
`Call` calls the SAME canonical methods via
`reflect.Value.MethodByName(...)` with zero-length variadic (no
call-time override — `Client.Attach` still has no per-call override
concept, unchanged from Decision 5's documented v1 scope). Net effect:
`callWithVars` and `Call` both shrink; the only logic left in
`clienttransport.go`'s `Call` is the reflection dispatch itself, exactly
the "thin adapter, pure IO" principle this doc's own Addendum 1 already
established for Observer/`ErrorPattern`.

**Tests**: canonical method unit tests in `api/rest/builder_test.go`
(declared-format-wins, call-time-override-wins-over-declared,
empty-falls-back-to-plain-EncodeRequest/DecodeResponse); existing
escape-hatch `callWithVars` tests pass unmodified, confirming the
refactor preserves 100% existing behavior; a new round-trip test proving
`Client.Attach`'s `Call` now honors a declared YAML format end-to-end —
`TestAttach_ClientCall_HonorsDeclaredYAMLFormat` in
`adapters/nethttp/clienttransport_test.go` (a real `httptest.Server` +
`rest.Route` declared with `RequestFormats(format.YAML(...))`/
`Formats(format.YAML(...))`, called via `rest.NewClient()` +
`nethttp.Attach` + `Client.Call`).

**Verified**: `gofmt`/`go build`/`go vet`/`go test` all green repo-wide;
`just check` (staticcheck + gosec) with no new suppressions; zero
regressions in existing test suites.

## Addendum 3: client-side general-purpose `ClientMW` hook (closes the last known REST/events middleware asymmetry)

Folded in from `docs/roadmap/rest-client-general-purpose-middleware.md`,
now deleted (its content lives here). Spun out of the events-side review
(`docs/design/d-0002-pubsub-workflow-simplification.md`'s Decision 11),
which flagged this exact asymmetry as the one remaining known gap between
REST and events middleware: `Route.HandleMW` (server-side) recognizes
TWO Fn shapes — the security-Fn shape and a general-purpose
`func(http.Handler) http.Handler` — and pub/sub's `PublishMW`/
`SubscribeMW` (`adapters/mqtt5/adapter.go`, mirrored in `mqtt`/`zeromq`)
also recognize two shapes each. `Route.ClientMW` (client-side, via
`adapters/nethttp.Call`/`CallWithHandle`) recognized only ONE shape — the
credential shape — hard-erroring on anything else. There was no way to
attach general-purpose, non-credential behavior (custom request/response
transformation, retry logic, bespoke tracing) to `ClientMW` at all.

**Fix, mirroring the shipped pub/sub precedent exactly**
(`adapters/mqtt5/adapter.go`'s `wrapPublishGeneral`/
`validatePublishImplementationShapes`/`publish`): a new Fn shape,

```go
func(next func(context.Context, Req) (Resp, error)) func(context.Context, Req) (Resp, error)
```

recognized alongside the credential shape by a new
`validateCallImplementationShapes[Req, Resp]` (used ONLY by
`callWithVars`/`CallWithHandle`), and dispatched by a new
`wrapCallGeneral[Req, Resp]` (`adapters/nethttp/client.go`), which wraps
fn OUTERMOST-in, in attachment order — identical contract to
`wrapPublishGeneral`.

**Wrap boundary** — confirmed by mirroring `publish`'s shipped structure:
topic derivation and security/credential resolution happen OUTSIDE
`wrapPublishGeneral`'s wrap, which applies ONLY to an "encode and
transmit" closure. REST's direct equivalent: the wrap applies ONLY to
`callWithVars`'s network-round-trip step (encode request body → build+
send `httpReq` → decode response → response header/cookie merge-fields),
NOT to path/query/header/cookie param derivation+validation, format
resolution, URL building, or credential resolution — those stay outside
the wrap and are closed over by the wrapped closure. `CallOptions.Observer`
is unaffected — it stays a permanent per-call field, exactly like
`PublishOptions.Observer` stays independent of `PublishMW`'s own general
hook; every existing `obs.RecordRequest(...)` call site was relocated
verbatim into the new closure's lexical scope, not altered.

**Variable-scoping verification** (the one non-mechanical risk in this
refactor): `callWithVars`'s `err` is a single, function-body-scoped
variable, first declared at `concretePath, err := handle.BuildPath(vars)`
and reused via `:=`/`=` throughout (Go's `:=` reuses an existing
same-scope variable whenever at least one new variable also appears on
the LHS); every place that needs the variable NOT to propagate uses an
`if err := ...; err != nil { return ... }` block-scoped shadow that
returns immediately. Moving the network-round-trip step into a nested
closure introduces a genuinely new function scope, but the closure's
return value is combined back into the SAME outer `err` at the call site
(`result, err := next(ctx, req)`, executed at the pre-existing outer
scope, not inside the closure) — so the deferred
`to.EndSpan(ctx, err)` (`stats.TraceObserver`) continues to observe the
correct final error exactly as before the refactor. Locked in by
`TestCall_GeneralShape_TraceObserver_SeesFinalError`.

**SSE deliberately NOT touched**: `consumeSSE` (SSE's `Consume`/
`CallSSEAdapter`) shares `validateClientImplementationShapes` (the
original, credential-only validator) with `callWithVars` today. Rather
than widen that shared function — which would let a general-purpose Fn
attached to an SSE route silently pass validation while never being
invoked (SSE's per-event dispatch shape, `func(context.Context, Event)
error` called repeatedly, doesn't match the single-call wrap shape) — a
SEPARATE validator (`validateCallImplementationShapes`) was introduced,
used only by `callWithVars`/`CallWithHandle`. `consumeSSE` keeps calling
the original, completely untouched `validateClientImplementationShapes`.
Mirrors mqtt5/mqtt/zeromq's existing precedent of separate
`validateSubscribeImplementationShapes` vs
`validatePublishImplementationShapes` rather than one universal
function. Locked in by `TestConsume_GeneralShapeFn_StillRejected`
(`adapters/nethttp/binding_test.go`) — a regression guard confirming SSE
was NOT widened by this change.

**`rest.Route.ClientMW`/`rest.SSERoute.ClientMW` needed ZERO code
changes** — they already accept untyped `fn any` with no shape
validation of their own (all shape validation and dispatch logic lives
in the consuming adapter). The entire fix is scoped to
`adapters/nethttp/client.go`.

**Explicitly out of scope** (both confirmed during this doc's own
finalization, not new information):
- `adapters/nethttp/clienttransport.go`'s `Client.Attach`-backed `Call`
  (used by `rest.Client.Call`) — its own, already-documented, much
  narrower "v1 scope" (no path/query/header/cookie params, no security/
  credential handling AT ALL, per Decision 5 above) is a separate,
  larger, pre-existing gap, not conflated with this feature.
- SSE's own general-purpose hook — a structurally different, future
  design problem (SSE's per-event dispatch shape doesn't match the
  single-call wrap shape), analogous to why pub/sub needed separate
  `Subscribe`/`Publish` general shapes rather than one universal one.
- `adapters/chi` — confirmed to have ZERO client-side code (a
  server-side-only router library); the original roadmap doc's title
  incorrectly listed it in scope and was corrected before this fold-in.

**Structured errors / observer**: no new types. `middleware.MiddlewareShapeError`
(already `slog.LogValuer`-compliant) is reused as-is for the new
shape-mismatch case, mirroring `validatePublishImplementationShapes`'s
reuse of the same type. No observer changes beyond the verbatim
relocation described above.

**Tests** (`adapters/nethttp/client_test.go` unless noted): `TestCall_GeneralShape_WrapsRequest`
(happy path, one general-purpose Fn wraps `next`), `TestCall_GeneralShape_MultipleFns_OuterToInner_AttachmentOrder`
(two Fns compose outermost-in, in attachment order), `TestCall_GeneralAndCredential_Coexist`
(a credential-shaped Fn and a general-purpose Fn attached together, both
run correctly), `TestCall_GeneralShape_TraceObserver_SeesFinalError`
(regression guard for the variable-scoping verification above),
`TestConsume_GeneralShapeFn_StillRejected` (`adapters/nethttp/binding_test.go`,
regression guard confirming SSE stays untouched); the pre-existing
`TestCall_WrongShapeMiddleware_ReturnsMiddlewareShapeError` (server-side
`func(http.Handler) http.Handler` shape attached to `ClientMW`) continues
to correctly fail, confirmed unaffected by the second recognized shape.

**Verified**: `gofmt`/`go build`/`go vet`/`go test` all green repo-wide;
`just check` (staticcheck + gosec, 0 issues); `just examples` (all pass);
zero regressions in existing test suites.

## Addendum 4: `Client.Call`/`Client.Consume` full `ClientTransport` feature parity, and retiring `nethttp.Consumer`/`Consume`

Folded in from `docs/roadmap/rest-client-transport-full-parity.md`, now
deleted (its content lives here). Triggered by a direct user request to
add `rest.Client.Consume` — a declarative, `ClientTransport`-based SSE
counterpart to `Client.Call` mirroring events' `Client.Subscribe` — which
surfaced that `Client.Call`'s existing reflection shim
(`clientTransport.Call`) had a documented "v1 scope" excluding path/query/
header/cookie params, security/credential `ClientMW`, per-call format
overrides, and general-purpose `ClientMW` wrapping. A naive
`Client.Consume` mirroring that same scope would have been unusable for
SSE's common path-templated routes, so this addendum closes ALL FOUR
gaps for BOTH `Client.Call` and the new `Client.Consume` together, then
retires the now-fully-redundant `nethttp.Consumer`/`Consume`.

**Part 1 — `ClientTransport` interface gains `Consume`.** Mirrors BOTH
`ClientTransport.Call` (REST's own precedent) and `events.Transport`'s
bundled `Publish`/`Subscribe`/`ServeSubscribers` shape (one interface per
boundary role, ALL operations bundled, ONE adapter type implementing all
of them):

```go
type ClientTransport interface {
    Call(ctx context.Context, route any, req any, opts ...ClientCallOptions) (any, error)
    Consume(ctx context.Context, sseRoute any, req any, fn any, opts ...ClientConsumeOptions) error
}
```

`nethttp.Attach`'s existing `clientTransport` struct implements BOTH
methods. `Client.Consume` is a thin dispatcher, exactly mirroring
`Client.Call`. `opts` is variadic (0 or 1 value) on both methods —
additive, backward-compatible with every existing 3-arg `Call` call site.

**Part 2 — new `EncodeVars`-family methods close the param-derivation
wall.** `RouteHandle[Req,Resp]`/`SSERouteHandle[Req,Event]` gained
`EncodeVars`/`EncodeQueryVars`/`EncodeHeaderVars`/`EncodeCookieVars(req
Req) (map[string]string, error)` — each a 1-line wrapper around
`codex.EncodeVars(req, h.xMergeFields()...)`, mirroring
`events.ChannelHandle[T].EncodeVars` exactly. These exist as
monomorphized METHODS (not direct `codex.EncodeVars` calls) specifically
because a reflection-based caller with a runtime-only `Req` (like
`clientTransport.Call`/`.Consume`) cannot reflect a generic FREE function
— Go forbids it — but CAN call an exported method already monomorphized
for a concrete `Req` at compile time. This same wall previously forced
`clientTransport.Call` to pass a `nil` vars map to `BuildPath`, meaning
ANY path-templated route (or one with query/header/cookie params) could
never be called through `Client.Call` at all before this fix.

**Part 3 — `SSERouteHandle` gained `EffectiveEventFormats`/
`ResolveEventDecoder`.** Extracted from `adapters/nethttp/binding.go`'s
previously free-function `resolveSSEDecodeFormat` (which now delegates to
these), mirroring `events.ChannelHandle.EffectiveSubscribeFormats`
exactly: `EffectiveEventFormats(formats ...format.Format[Event])
[]format.Format[Event]` resolves override > declared `h.Formats`;
`ResolveEventDecoder(accept string, formats ...format.Format[Event])
func([]byte) (Event, error)` picks the one decoder via the SAME
Accept-header-matching algorithm the server's own Accept-negotiation
uses. Behavior-neutral refactor — confirmed via the full existing SSE
test suite staying green unmodified.

**Part 4 — security/credential `ClientMW` and format overrides.**
`clientTransport.Call`/`.Consume` now call the EXISTING, non-generic
`mergeCredentialHeaders`/`validateSecurityCredentials` directly (no
reflection needed for the call itself, only to reach the plain-typed
`Descriptor.Security`/`GlobalSecurity`/`ClientImplementations`/
`SecuritySchemes` fields on the type-erased handle). New
`ClientCallOptions{RequestFormats, ResponseFormats any}`/
`ClientConsumeOptions{Formats any}` — plain, type-erased structs
(consistent with the codebase's existing `CallOptions`/`ConsumeOptions`
idiom, not a new functional-options pattern) — provide per-call format
overrides, resolved via `reflect.Value` type-comparison against the
reflected handle method's expected parameter type (the reflection
sibling of `resolveCallFormat`).

**Part 5 — general-purpose `ClientMW` wrapping, via `reflect.MakeFunc`.**
For `Call`, the network round-trip (encode → send → decode) is built as
a `reflect.MakeFunc`-constructed closure of type `func(context.Context,
Req) (Resp, error)`, then wrapped by every attached general-purpose
`ClientImplementation.Fn` whose reflected type matches `func(next
<that type>) <that type>`, iterated OUTERMOST-in in attachment order —
mirrors `wrapCallGeneral`'s static-generics contract exactly, just via
reflection instead of a type assertion. For `Consume`, the wrap applies
to the PER-EVENT dispatch (`func(context.Context, Event) error`) instead
— mirrors `wrapSubscribeGeneral`'s identical per-message (not
per-connection) wrap boundary, the natural SSE analogue since Consume has
no single "Resp" the way Call does.

**Part 6 — `nethttp.Consumer`/`Consume`/`NewConsumer` retired.**
Confirmed via code that `Consume[Req,Event](ctx, consumer, sseRoute, req,
fn, opts)` was a 2-line delegate (`sseRoute.ClientHandle()` +
`consumeSSE(...)`) — structurally identical in role to the ALREADY-REMOVED
`Call[Req,Resp]`/`Caller` (Decision 6: demoted to unexported
`call`/`caller`, `CallWithHandle` remaining the sole escape hatch).
Deleted `Consume` and `consumer.go`'s `Consumer`/`NewConsumer`/
`WithBaseURL` entirely — `CallSSEAdapter` (handle-based, plain
`client,baseURL` params) remains the SOLE public escape hatch, mirroring
`CallWithHandle` exactly, delegating to the unchanged `consumeSSE`/
`consumeSSEOnce` primitive.

**Migration completed**: `examples/adapters-sse/main.go` — every
`Consume`/`Consumer` call site migrated to `rest.Client.Consume` (via
`nethttp.Attach`); the one demo needing `ConsumeOptions.OnError` (the
codec-rejection demo, and the negative unauthenticated-request demo)
migrated to `nethttp.CallSSEAdapter` + `ports.SourcePort` instead, since
`Client.Consume` deliberately has no `OnError`/`OnCredentialRejected`/
`MaxBackoff`/`ExtraHeaders` hooks. `adapters/nethttp/binding_test.go`'s 23
`TestConsume_*` tests migrated to call the unexported `consumeSSE`
primitive directly (same package, handle built via `route.ClientHandle()`)
— a mechanical rename, zero behavior change, since `Consume`'s body was
always just a 2-line delegate to this exact primitive. One test
(`ExampleConsume`) rewritten as `ExampleClient_Consume`, demonstrating the
new public API instead of the removed one. Swept dangling `[Consume]`/
`[nethttp.Consume]`/`[Consumer]` godoc bracket-links across
`adapters/nethttp/{binding,stream_errors,clienttransport,serve_sse}.go`
and `api/rest/{builder,middleware}.go`, plus prose references in
`docs/features/sse-streaming.md`, `docs/concepts/api-contracts.md`, and
this document's own earlier SSE-consumption addendum.

**Structured errors / observer**: no new error types. `CallFormatOptError`
(pre-existing) is reused for the new format-override type-mismatch case.
No observer changes — every existing `RecordRequest`/`TraceObserver`/
`SecurityObserver` call site was preserved exactly, only reached via a
new code path (the extended `clientTransport.Call`) or a new sibling
(`clientTransport.Consume`).

**Tests**: `TestAttach_ClientCall_DerivesPathVars`,
`TestAttach_ClientCall_CredentialClientMW_Invoked`,
`TestAttach_ClientCall_GeneralPurposeClientMW_Wraps`,
`TestAttach_ClientCall_WithClientRequestResponseFormats_Overrides`,
`TestAttach_ClientCall_BackwardCompatible_NoOptsStillWorks`,
`TestAttach_ClientConsume_RoundTrip`,
`TestAttach_ClientConsume_DerivesPathVars`,
`TestAttach_ClientConsume_CredentialClientMW_Invoked`,
`TestAttach_ClientConsume_GeneralPurposeClientMW_Wraps`,
`TestAttach_ClientConsume_WithFormats_Overrides`,
`TestAttach_ClientConsume_NoTransportAttached_ReturnsNoClientTransportAttachedError`,
`TestAttach_ClientConsume_WrongRouteType_ReturnsTransportTypeMismatchError`
(`adapters/nethttp/clienttransport_test.go`); 8 new
`RouteHandle`/`SSERouteHandle.EncodeVars`-family tests
(`api/rest/builder_test.go`).

**Verified**: `gofmt`/`go build`/`go vet`/`go test` all green repo-wide
(zero regressions across the full existing test suite, including the 23
migrated `TestConsume_*` tests); `just check` (staticcheck + gosec, 0
issues — one new `G124` false-positive exclusion added to
`gosec.config.json` for `adapters/nethttp/clienttransport.go`, mirroring
the SAME pre-existing exclusion already applied to
`client.go`/`binding.go`/`cookie.go` for the identical outgoing-cookie
pattern); `just examples` (all pass, including the migrated
`adapters-sse` example, verified console output for both the
credential-supplied and unauthenticated-rejection demo paths).
