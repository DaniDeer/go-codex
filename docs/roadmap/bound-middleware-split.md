# Bound Middleware Split — `Middleware[In,Out]` (reusable) vs `BoundMiddleware[Req,In,Out]` (route-bound)

> **Status:** Design complete — not yet implemented.
> [← Back to Roadmap](index.md)

## Motivation

Today, ONE Go type — `Middleware[In, Out]` (in `api/rest`, `api/events`,
`api/reqreply`, see `docs/design/d-0003-codec-declared-middlewares.md`/
`docs/design/d-0007-declarative-middleware-layering.md`) — silently
serves THREE structurally different roles, distinguished only at RUNTIME
via reflection on the paired Fn's signature:

1. **Reusable, declarative-only** — `Declaration[In,Out]` + merge-field
   declarations (REST: header/cookie/query; events/reqreply: topic/
   property) + `.WithReceive(fn)`/`.WithSend(fn)`, a `Req`/`T`-FREE Fn
   attached via plain `.Use(mw)`. Fully reusable across ANY route/channel,
   since the Fn never needs route-specific typing.
2. **Route/channel-bound** — the SAME type, but the Fn is supplied
   separately at `HandleMW(mw, fn)`/`ClientMW(mw, fn)`/`SubscribeMW(mw,
   fn)`/`PublishMW(mw, fn)` call time, with an ADDITIONAL `*Req`/`*T`
   parameter — detected via `isBoundHandleMWShape[Req](fn)`-style
   reflection on `fn`'s signature.
3. **Legacy/raw-adapter Security pairing** — `mw` (legacy
   `middleware.Middleware{Name,Security}` OR a codec-backed
   `Middleware[In,Out]` used purely as a scheme carrier, e.g.
   `SecurityMiddleware[struct{},struct{}]`) paired with a raw-adapter-
   shaped Fn (`func(ctx, *http.Request, *Req) (map[string][]string,
   error)`), falling through to `buildServerImplementation` whenever `fn`
   does NOT match the bound shape.

This entanglement is the direct, confirmed cause of several bugs found
and fixed/documented across the sessions implementing
`docs/design/d-0007-declarative-middleware-layering.md`:
- `DuplicateMiddlewareNameError` when `.Use(mw)` + a bound `HandleMW(&mw,
  fn)` combine for the same Security-only `mw` (documented, never fixed
  — both attachment styles add a spec contribution under the same name,
  with no structural reason they couldn't collide).
- The exact-`Req`-type-match requirement silently defeats generic,
  multi-`Req`-type reuse for the bound shape (forces one Fn per `Req`
  type, documented in `docs/features/security.md`'s "Choosing
  `[struct{},struct{}]` vs a real `[In,Out]`" sections).
- The `Client.Attach`/`ServeSubscribers` regression (bound `SubscribeMW`
  silently never dispatched at all through the primary workflow, across
  all 3 pub/sub adapters) — a direct consequence of THREE separate,
  independently-reflection-implemented dispatch paths needing to
  individually remember to handle the bound case correctly.

This doc proposes collapsing the three roles above into exactly TWO
explicit, compile-time-distinct Go types — removing reflection-based
Fn-shape guessing entirely for the bound case, and removing the legacy/
raw-adapter Security-pairing mode outright (evaluated below and found
unnecessary for every current real use case).

## Investigation: can the legacy/raw-adapter mode be dropped entirely?

Inspected the ACTUAL FN BODY (not just the declared shape) of every
current legacy Security Fn in the repository:

| Fn | Touches `*Req`/`*T` meaningfully? | Migrates to |
|---|---|---|
| REST `handlers.ExtractScopes` (`ScopesImpl[Req any]`, 6 routes: `CreateUserReq`/`GetUserReq`/`UpdateUserReq`/`ListUsersReq`/`ProfileReq`/`AdminActionReq`) | NO — `_ *routes.XxxReq` discarded every time | Reusable (`SecurityMiddleware[AuthIn,AuthOut].WithReceive(fn)`, header-decoded `AuthIn`) |
| `examples/mutable-security-keys` | NO — `_ *struct{}` discarded | Reusable |
| `examples/adapters-nethttp-client` | NO — `_ *struct{}` discarded | Reusable |
| `examples/adapters-sse` | NO — `_ *struct{}` discarded | Reusable |
| events `MQTT5SecurityImpl` | Reads `msg.Properties` directly, but ONLY to extract one property value — directly replaceable by a property-merge-field-decoded `In` | Reusable (property-decoded `AuthIn`) |
| events `ZeromqSecurityImpl` | NO — `_ *routes.SensorReading` discarded (its own doc comment admits this is a placeholder shape demo) | Reusable |
| events `MQTTSecurityImpl` (mqtt v3) | NO — credential from a CONNECT-time closure, message discarded | Reusable |
| reqreply `VerifyBearer` (mqtt5) | Reads `msg.Properties.User` but discards the extracted value entirely (`_ = token // nothing further to check`) | Reusable (property-decoded `In`) |
| reqreply `VerifyOAuthComputeZeroMQ` + `validOAuthCredFn`/`invalidOAuthCredFn` (zeromq) | **YES — genuinely.** Reads `req.Token` (server) / WRITES `req.Token = "..."` (client) — zeromq has NO property/header side channel at all, so the credential can ONLY live on the decoded `Req` struct itself | **Bound** — the ONE genuine, confirmed current need |

**Conclusion: the legacy/raw-adapter mode is PERMANENTLY CLOSED, not
dropped-for-now.** Every current usage except ONE (zeromq's in-payload
credential pattern) migrates to the reusable class — which turns out to
be MORE reusable than previously framed: a reusable middleware works
across ANY `Req`/`T` type unconditionally (it never inspects `Req`/`T`
at all), not merely "the same type reused." `BoundMiddleware[Req,In,Out]`/
`BoundSubscribeMiddleware[T,In,Out]`/`BoundPublishMiddleware[T,In,Out]`
is **not** a narrow escape hatch kept around apologetically — it is THE
dedicated, declarative mechanism for any middleware need (Security or
general) that requires reading/writing the full CODEC-DECODED `Req`/`T`
struct. It fully and permanently replaces the old raw-adapter-Fn pairing
mode: `isBoundHandleMWShape`/`isBoundClientMWShape`-style reflection
detection is deleted outright, and `HandleMW`/`ClientMW` will NEVER
again accept a raw-transport-shaped Fn (`func(ctx, *http.Request, *Req)
(...)`) for Security pairing, under any circumstance. This is a clean,
closed design, not a placeholder for a mode that might return.

**Where the boundary sits, explicitly**: `BoundMiddleware`'s `*Req`/`*T`
parameter is always the CODEC-DECODED struct — the SAME type the
route/channel itself declares, after validation. It is never a raw
transport object (`*http.Request`, a raw MQTT packet, a ZeroMQ frame).
Raw TRANSPORT-level access (TLS client certificate, remote IP, iterating
headers beyond what's declared as merge fields) is a structurally
DIFFERENT, lower layer than anything `Middleware`-paired Fns have ever
served — confirmed zero current usage needs it. That layer stays out of
scope on its own terms, as a different architectural concern entirely
(adapter/transport-level, not codec/API-level) — not as "maybe later for
this mechanism." If a genuine transport-level need ever arises, it would
require its own, separately-designed mechanism (e.g. an adapter-level
context value), never a reopening of this closed Fn-pairing mode.

**What stays, confirmed structurally orthogonal:**
- **General-purpose (non-Security) decorator pairing** — `HandleMW(nil,
  fn)`/`ClientMW(nil, fn)` where `fn` is a pure decorator shape
  (`func(http.Handler) http.Handler` for REST; a generic
  `func(next func(ctx,Req)(Resp,error)) func(ctx,Req)(Resp,error)` wrap
  for the client side) — a structurally different concern (cross-cutting
  behavior wrapping, e.g. `Observability`/timing middleware), with no
  Security declaration and no need for `Req` access. Confirmed via code:
  `buildServerImplementation`'s `mw == nil` branch is entirely separate
  from the Security-pairing branch. Stays completely untouched.
- **Spec-only `.Use(mw)` declarations with no attached implementation at
  all** (`examples/go-edge-models`'s Docker-registry-client pattern,
  documenting an external requirement this codebase never enforces) —
  stays legitimate; this was never Fn-pairing at all.
- **Cross-pattern scheme-config sharing** (`route.SecurityScheme`/
  scopes/codec reused across 3 pattern-specific constructor calls, per
  `docs/features/security.md`'s "Sharing a security SCHEME across
  REST/events/reqreply") — stays, unaffected; confirmed this was NEVER
  about sharing one Declaration/Fn value, only the underlying config.

## Scope decisions

| In scope | Out of scope |
|---|---|
| `api/rest` (`Route`, `SSERoute`) | `api/mcp` (no `Middleware[In,Out]` mechanism at all) |
| `api/events` (`Subscriber`, `Publisher`) | `ports` binding layer (continues working via whichever dispatch its `Pattern` plumbing already uses — a non-goal to touch here) |
| `api/reqreply` (`Route`) | General-purpose (non-Security) decorator pairing — confirmed orthogonal, unchanged |
| Dropping the legacy/raw-adapter Security-pairing dispatch mode entirely | The bare `middleware.Middleware{Name,Security}` type itself (stays, for spec-only declarations with no implementation) |
| Migrating every current example/test consumer of the dropped mode or the old bound-shape-detection mechanism | `rest.FromSecurityScheme`/`events.FromSecurityScheme` bridges (unaffected — they build spec-only declarations, no Fn pairing) |

## API surface

### `api/rest` (reqreply mirrors this almost exactly — see below)

```go
// Middleware[In, Out] — UNCHANGED surface, INTERNALLY simplified: no
// longer satisfies whatever interface the bound-attach point requires.
// .Use(mw) remains its ONLY attachment path.
type Middleware[In, Out any] struct {
    middleware.Declaration[In, Out]
    // ... same merge-field fields as today ...
    receiveFn func(ctx context.Context, in In) (Out, error)
    sendFn    func(ctx context.Context) (In, error)
    // applyBoundRoute/applyBoundClientRoute methods REMOVED entirely.
}

// BoundMiddleware[Req, In, Out] — NEW type. Req is the discriminator:
// a BoundMiddleware[Req,...] value attaches ONLY to routes whose own
// Req type parameter matches, enforced via a Go generic interface
// assertion (see "Attachment mechanics" below), never Fn-shape
// reflection.
//
// INTERNAL LAYOUT — a NAMED field, not an embedded one (resolves a
// gap found during design review): `mw` holds a `Middleware[In, Out]`
// value, giving BoundMiddleware the EXACT SAME merge-field vocabulary
// for free and letting its own applyBoundRoute call the 9 existing
// `transform.go` helpers (buildMiddlewareHandlerAny, satisfiesOf,
// specContributionOf, etc.) UNCHANGED, passing `bm.mw` — zero
// duplication of that logic. Using a NAMED field (`mw Middleware[In,
// Out]`), NOT an anonymous/embedded one, is DELIBERATE: Go promotes
// ALL methods of an embedded field, which would silently promote
// Middleware[In,Out]'s OWN `applyAgnosticRoute` method onto
// BoundMiddleware too — making it accidentally satisfy
// routeMiddlewareContributor and attachable via plain `.Use()`,
// reintroducing the exact bound/reusable ambiguity (D6(b)/D7) this
// entire redesign exists to eliminate. A named field has NO method
// promotion — BoundMiddleware's ONLY attachment path is its own
// purpose-built `applyBoundRoute`, satisfying `boundContributor[Req]`.
type BoundMiddleware[Req, In, Out any] struct {
    mw Middleware[In, Out] // NAMED, not embedded — see comment above
    fn func(ctx context.Context, req *Req, in In) (Out, error) // baked in at construction, NEVER supplied separately again
}

// NewBoundMiddleware is the server-side (HandleBoundMW) constructor —
// fn's shape is checked by the ORDINARY GO COMPILER at this call, zero
// reflection needed to verify arity/types.
func NewBoundMiddleware[Req, In, Out any](
    decl middleware.Declaration[In, Out],
    fn func(ctx context.Context, req *Req, in In) (Out, error),
) BoundMiddleware[Req, In, Out]

// BoundSecurityMiddleware mirrors SecurityMiddleware[In,Out]'s role for
// the bound class — a Security-carrying bound middleware, fn embedded.
func BoundSecurityMiddleware[Req, In, Out any](
    schemeName string, scheme SecurityScheme, scopes []string,
    fn func(ctx context.Context, req *Req, in In) (Out, error),
) BoundMiddleware[Req, In, Out]

// Mirrors Middleware[In,Out]'s own 10 merge-field + 2 ctxField builder
// methods — SAME names, now on BoundMiddleware. Each is a ONE-LINE
// forwarder onto the named `mw` field's own existing method, e.g.:
//   func (m BoundMiddleware[Req, In, Out]) WithRequestHeader(p MergedHeaderParam[In]) BoundMiddleware[Req, In, Out] {
//       m.mw = m.mw.WithRequestHeader(p)
//       return m
//   }
// — repeated for every method below; zero new merge-field logic.
func (m BoundMiddleware[Req, In, Out]) WithRequestHeader(p MergedHeaderParam[In]) BoundMiddleware[Req, In, Out]
func (m BoundMiddleware[Req, In, Out]) WithRequestCookie(p MergedCookieParam[In]) BoundMiddleware[Req, In, Out]
func (m BoundMiddleware[Req, In, Out]) WithRequestQuery(p MergedQueryParam[In]) BoundMiddleware[Req, In, Out]
func (m BoundMiddleware[Req, In, Out]) WithResponseHeader(p MergedResponseHeaderParam[Out]) BoundMiddleware[Req, In, Out]
func (m BoundMiddleware[Req, In, Out]) WithResponseCookie(p MergedResponseCookieParam[Out]) BoundMiddleware[Req, In, Out]
func (m BoundMiddleware[Req, In, Out]) WithRequestHeaderSpec(p HeaderParam) BoundMiddleware[Req, In, Out]
func (m BoundMiddleware[Req, In, Out]) WithRequestCookieSpec(p CookieParam) BoundMiddleware[Req, In, Out]
func (m BoundMiddleware[Req, In, Out]) WithRequestQuerySpec(p QueryParam) BoundMiddleware[Req, In, Out]
func (m BoundMiddleware[Req, In, Out]) WithResponseHeaderSpec(p ResponseHeaderParam) BoundMiddleware[Req, In, Out]
func (m BoundMiddleware[Req, In, Out]) WithResponseCookieSpec(p ResponseCookieParam) BoundMiddleware[Req, In, Out]
func (m BoundMiddleware[Req, In, Out]) SetContextFieldFromIn(field middleware.ContextFieldSetter, get func(In) any) BoundMiddleware[Req, In, Out]
func (m BoundMiddleware[Req, In, Out]) SetContextFieldFromOut(field middleware.ContextFieldSetter, get func(Out) any) BoundMiddleware[Req, In, Out]

// applyBoundRoute satisfies boundContributor[Req] — the ONLY attach
// path BoundMiddleware has. Calls the EXISTING, UNCHANGED
// buildMiddlewareHandlerAny/boundSpecContributionOf helpers with the
// named `mw` field, exactly as Middleware[In,Out]'s OWN
// applyBoundRoute does today for the (now-removed) bound-shape-
// detected case.
func (m BoundMiddleware[Req, In, Out]) applyBoundRoute(rb *routeBuilder) {
    rb.middlewareHandlers = append(rb.middlewareHandlers, buildMiddlewareHandlerAny(m.mw, m.fn))
    rb.middlewareSpecContributions = append(rb.middlewareSpecContributions, boundSpecContributionOf(m.mw))
}

// BoundClientMiddleware[Req, In, Out] — ClientMW's sending-side sibling.
// Fn shape differs (Req BY VALUE, matching ClientMW's existing bound-
// shape convention exactly): func(ctx, req Req) (In, error). SAME named-
// field layout as BoundMiddleware (mw Middleware[In,Out], named not
// embedded) and the same rationale.
type BoundClientMiddleware[Req, In, Out any] struct {
    mw Middleware[In, Out] // NAMED, not embedded — same rationale as BoundMiddleware
    fn func(ctx context.Context, req Req) (In, error)
}

func NewBoundClientMiddleware[Req, In, Out any](
    decl middleware.Declaration[In, Out],
    fn func(ctx context.Context, req Req) (In, error),
) BoundClientMiddleware[Req, In, Out]

func BoundSecurityClientMiddleware[Req, In, Out any](
    schemeName string, scheme SecurityScheme, scopes []string,
    fn func(ctx context.Context, req Req) (In, error),
) BoundClientMiddleware[Req, In, Out]
```

### Attachment mechanics (resolves Open Decision 1 below)

```go
// boundContributor[Req] is Req-parameterized — BoundMiddleware[Req,...]
// satisfies it FOR ITS OWN Req only. Go's own generic interface
// satisfaction does the matching; no Fn-shape reflection anywhere.
type boundContributor[Req any] interface {
    applyBoundRoute(rb *routeBuilder)
}

func (r Route[Req, Resp]) HandleBoundMW(bm any) Route[Req, Resp] {
    if v, ok := bm.(boundContributor[Req]); ok {
        r.opts = append(slices.Clone(r.opts), boundHandleMWOpt{mw: v})
        return r
    }
    // bm constructed with the WRONG Req (deliberate misuse only — e.g.
    // passing a BoundMiddleware[Foo,...] to a Route[Bar,...]) — a NEW,
    // clear BoundMiddlewareReqMismatchError, not silent mis-dispatch.
    r.opts = append(slices.Clone(r.opts), boundMismatchOpt{got: bm})
    return r
}

func (r Route[Req, Resp]) ClientBoundMW(bm any) Route[Req, Resp] { /* mirrors HandleBoundMW, boundClientContributor[Req] */ }
```

**How `boundMismatchOpt`'s error actually surfaces (gap found during
design review — no existing mechanism did this)**: every `RouteOpt`'s
`applyRoute(rb *routeBuilder)` returns NOTHING today — opts mutate `rb`
directly, and misconfigurations are caught by a SEPARATE, LATER
validation pass over the already-populated `rb` (e.g.
`checkMiddlewareNameUniquenessAndAttachment(rb, routeLabel)`, called
explicitly after the opts loop). No existing `rb` field stashes a
pending build-time error for a later check to pick up — this is a NEW
mechanism, not a reuse of one:

```go
// routeBuilder gains ONE new field:
type routeBuilder struct {
    // ... existing fields ...
    buildErr error // set by boundMismatchOpt.applyRoute; checked early in registerHandle
}

func (o boundMismatchOpt) applyRoute(rb *routeBuilder) {
    if rb.buildErr == nil { // first error wins, matching this being a configuration mistake, not an accumulation scenario
        rb.buildErr = BoundMiddlewareReqMismatchError{Route: /* ... */, Got: o.got}
    }
}

// registerHandle checks rb.buildErr early, mirroring InvalidPathError's
// own existing early-return pattern at the top of the same function:
func (r Route[Req, Resp]) registerHandle(b *Server) (*RouteHandle[Req, Resp], error) {
    // ... existing pathCodec.Validate early-return ...
    var rb routeBuilder
    for _, opt := range r.opts {
        opt.applyRoute(&rb)
    }
    if rb.buildErr != nil {
        return nil, rb.buildErr
    }
    // ... existing checkMiddlewareNameUniquenessAndAttachment call, etc. ...
}
```

`api/events`'/`api/reqreply`'s own builder structs (whatever each
package's equivalent of `routeBuilder` is called) need the SAME new
field + early-check, mirrored exactly.

`HandleMW(mw middleware.RouteMiddleware, fn any)`/`ClientMW(...)` KEEP
their exact current signatures — simplified internally to ONLY dispatch
the general-purpose (`mw == nil`) decorator path now (the Security-
pairing fallback branch is removed along with the legacy mode).

### `api/events` — mirrors the above with `T` instead of `Req`, Subscribe/Publish split

**Same named-field layout as REST, NOT re-derived below**: both
`BoundSubscribeMiddleware[T,In,Out]` and `BoundPublishMiddleware[T,In,Out]`
carry a NAMED (never anonymously embedded) `mw events.Middleware[In,Out]`
field, for the EXACT same reason REST's `BoundMiddleware` does (see
REST's section above, in full, for the rationale — not repeated here).
Their `applyBoundSubscriber`/`applyBoundPublisher` methods call
`api/events/transform.go`'s existing helpers with that named field,
exactly mirroring REST's `applyBoundRoute`.

```go
type BoundSubscribeMiddleware[T, In, Out any] struct { /* topic/property merge fields */ }
func NewBoundSubscribeMiddleware[T, In, Out any](decl middleware.Declaration[In, Out], fn func(ctx context.Context, msg *T, in In) (Out, error)) BoundSubscribeMiddleware[T, In, Out]
func BoundSecuritySubscribeMiddleware[T, In, Out any](schemeName string, scheme SecurityScheme, scopes []string, fn func(ctx context.Context, msg *T, in In) (Out, error)) BoundSubscribeMiddleware[T, In, Out]

type BoundPublishMiddleware[T, In, Out any] struct { /* topic/property merge fields */ }
func NewBoundPublishMiddleware[T, In, Out any](decl middleware.Declaration[In, Out], fn func(ctx context.Context, msg T) (Out, error)) BoundPublishMiddleware[T, In, Out]
func BoundSecurityPublishMiddleware[T, In, Out any](schemeName string, scheme SecurityScheme, scopes []string, fn func(ctx context.Context, msg T) (Out, error)) BoundPublishMiddleware[T, In, Out]

func (s Subscriber[T]) SubscribeBoundMW(bm any) Subscriber[T]
func (p Publisher[T]) PublishBoundMW(bm any) Publisher[T]
```

**Simplification found during this investigation**: events' CURRENT bound
Subscribe mechanism supports TWO Fn arities — the original 1-return
`func(ctx, *T, In) error` (no `Out`, general-purpose bound enrichment)
and the newer 2-return `func(ctx, *T, In) (Out, error)` (GrantedScopes-
capable). Grepped every current example/test: **zero current usages of
the 1-return shape exist outside unit tests exercising the shape itself**
— every real Subscribe bound usage either uses the legacy raw-adapter
shape (now dropped) or the 2-return shape. `BoundSubscribeMiddleware`
therefore ALWAYS returns `(Out, error)` — uniform with REST/reqreply's
bound shape — simplifying the mental model (a pure-enrichment-with-no-
reply-value case just declares `Out = struct{}`).

### `api/reqreply` — identical to REST's shape (fully duplex, `Req` naming)

**Same named-field layout as REST, NOT re-derived below**: reqreply's
own `BoundMiddleware[Req,In,Out]` carries a NAMED (never anonymously
embedded) `mw reqreply.Middleware[In,Out]` field, for the EXACT same
reason REST's does — see REST's section above for the full rationale.
Its `applyBoundRoute`/`applyBoundClientRoute` methods call
`api/reqreply/transform.go`'s existing helpers with that named field.

```go
type BoundMiddleware[Req, In, Out any] struct { /* topic/property merge fields */ }
func NewBoundMiddleware[Req, In, Out any](decl middleware.Declaration[In, Out], fn func(ctx context.Context, req *Req, in In) (Out, error)) BoundMiddleware[Req, In, Out]
func BoundSecurityMiddleware[Req, In, Out any](schemeName string, scheme SecurityScheme, scopes []string, fn func(ctx context.Context, req *Req, in In) (Out, error)) BoundMiddleware[Req, In, Out]

func (r Route[Req, Resp]) HandleBoundMW(bm any) Route[Req, Resp]
func (r Route[Req, Resp]) ClientBoundMW(bm any) Route[Req, Resp]
```

## Open design decisions — ALL RESOLVED (recorded with rationale)

1. **How does `Route[Req,Resp]` accept a `BoundMiddleware[Req,In,Out]`
   value, given Go forbids a method from introducing NEW type parameters
   beyond its receiver's own? — RESOLVED: method + internal
   `boundContributor[Req]` type-assertion** (not a free function).
   Rejected alternative: a free function (`rest.HandleBoundMW[Req,Resp,
   In,Out](route, bm) Route[Req,Resp]`) gives maximal compile-time safety
   (a `Req` mismatch is a compile error, not even a runtime possibility)
   but breaks the fluent dot-chaining style every existing example/doc
   already uses. The method+assertion approach's "mismatch" case can
   only occur via DELIBERATE misuse (constructing `BoundMiddleware[Foo,
   ...]` then attaching it to `Route[Bar,...]`) — caught immediately,
   loudly, via a new `BoundMiddlewareReqMismatchError`, not silently
   mis-dispatched. Preserves ergonomics at a negligible, well-contained
   safety cost.
2. **Does the new bound-attach point get a NEW method name or does
   `HandleMW` itself get overloaded? — RESOLVED: new, distinct method
   name** (`HandleBoundMW`/`ClientBoundMW` for REST/reqreply;
   `SubscribeBoundMW`/`PublishBoundMW` for events). A reader sees
   immediately, at the call site, which class is in play — no variadic-
   signature ambiguity, no need to inspect `mw`'s type to know what's
   happening. `HandleMW`/`ClientMW`/`SubscribeMW`/`PublishMW` keep their
   EXACT current signatures, simplified internally.
3. **Exact naming — RESOLVED**: `BoundMiddleware[Req,In,Out]` (REST/
   reqreply), `BoundSubscribeMiddleware[T,In,Out]`/
   `BoundPublishMiddleware[T,In,Out]` (events) — "Bound" prefix on the
   type, "Bound" suffix on the attach method, consistently.

## Structured errors (all implement `slog.LogValuer`)

- `BoundMiddlewareReqMismatchError{Route string, Got any}` (NEW, all 3
  packages, fields aligned with the "Attachment mechanics" code sketch
  — a prior draft of this field list, `{Route, MiddlewareName, Err}`,
  didn't match the sketch and didn't fit the error's own shape:
  `MiddlewareName` isn't always extractable — `bm` may not even be the
  right CLASS, let alone the wrong `Req` — and `Err` implies wrapping an
  inner error, but a `Req` mismatch is a leaf condition with nothing to
  wrap) — `HandleBoundMW`/`ClientBoundMW`/`SubscribeBoundMW`/
  `PublishBoundMW` return this when `bm`'s concrete `Req`/`T` doesn't
  match the route/channel's own — see Open Decision 1. `Got` holds the
  raw mismatched `bm any` value itself (matching the sketch below) —
  `Error()`/`LogValue()` format it via `%T`/`reflect.TypeOf(Got)` to show
  its concrete type, never the value's contents.
  For a friendlier message when `bm` IS the right class (just the wrong
  `Req`), `HandleBoundMW` may ADDITIONALLY try a narrower, `Req`-free
  `interface { MiddlewareName() string }` type-assertion (which
  `BoundMiddleware[Req,In,Out]` satisfies by forwarding to its named
  `mw` field's own `MiddlewareName()`) purely to enrich the error
  message — this is an optional ergonomics detail, not a new field on
  the error type itself.
- `MissingSecurityMiddlewareError`/`UnknownMiddlewareImplementationError`
  — **narrow in scope, not removed**: once every `BoundMiddleware`/
  `BoundSubscribeMiddleware`/`BoundPublishMiddleware` value is
  constructed with its Fn ALREADY EMBEDDED, it becomes STRUCTURALLY
  IMPOSSIBLE to "declare a security scheme with no attached
  implementation" for the bound class — the scheme declaration and its
  Fn are inseparable, by construction. These errors remain needed ONLY
  for the plain `RouteMeta.Security`/`Subscribe.Security`-declared-
  directly case (a security REQUIREMENT with no attached `BoundMiddleware`
  contributing grants at all).
- `AmbiguousMiddlewareAttachmentError` (D7) — **DELETED, now unreachable**:
  structurally impossible once `Middleware[In,Out]` can never be bound
  — a value can never combine `.WithReceive`/`.WithSend` (reusable) with
  bound attachment, since bound attachment requires the DIFFERENT
  `BoundMiddleware[Req,In,Out]` type entirely.
- `DuplicateMiddlewareNameError` (D6(b)) — stays, still needed across
  multiple DIFFERENT attached `Middleware`/`BoundMiddleware` values —
  but the SPECIFIC confirmed bug (`.Use(mw)` + bound `HandleMW(&mw,fn)`
  colliding for the SAME value) becomes structurally impossible as a
  side effect: `.Use(mw)` always contributes via `applyAgnosticRoute`
  (one `middlewareSpecContribution`); `HandleBoundMW(bm)` contributes via
  an entirely separate code path, on an entirely separate TYPE — the two
  can never be the same Go value.

## Observer integration

No changes to REQUEST-DISPATCH-time Observer calls —
`stats.SecurityObserver.RecordSecurityRejection`,
`stats.ReportErrors(obs, "middleware:in"/"middleware:fn"/"middleware:out",
...)` all fire identically regardless of which class dispatched; the
dispatch POINT (pre-handler, post-decode, inside each
`adapters/{nethttp,chi,mqtt5,mqtt,zeromq}` package) is unchanged — these
helpers operate on `rb.middlewareHandlers`/`rb.middlewareSpecContributions`
generically, with zero awareness of which Go type (`Middleware[In,Out]`
vs. `BoundMiddleware[Req,In,Out]`) produced a given entry.

**The new `BoundMiddlewareReqMismatchError` is explicitly NOT
Observer-reported, by design, consistent with existing precedent**:
confirmed via code that today's `DuplicateMiddlewareNameError` and
`AmbiguousMiddlewareAttachmentError` (D6(b)/D7) — the closest existing
analogues, both builder/`Register()`-time configuration mistakes — are
PLAIN RETURNED ERRORS from `Route.Register`/`RegisterHandle`, never
routed through `stats.Observer` (confirmed via grep: every
`stats.ReportErrors`/`RecordSecurityRejection` call site lives inside
`adapters/*`'s REQUEST-serving code, never inside `api/rest`/`api/events`/
`api/reqreply`'s builder code). `stats.Observer` exists to report
per-REQUEST runtime outcomes (a decode failure, a security rejection on
an incoming call) — a misconfigured `BoundMiddleware` attached to the
wrong `Req` type is a PROGRAMMING mistake caught once, at startup/build
time, before any request is ever served. `BoundMiddlewareReqMismatchError`
follows the SAME category and stays outside Observer's scope for the
same reason.

## Relationship to D-0006 (protocol-native capabilities) and `ports.Pattern` — confirmed orthogonal, neither affected

Mirroring `docs/design/d-0007-declarative-middleware-layering.md`'s own
"Relationship to D-0006" section (which this doc should NOT have to
re-derive from scratch, but is worth confirming explicitly here too,
since this doc touches the SAME dispatch code paths D-0007 did):

- **D-0006's Capability mechanism (QoS/Retained/HWM/Conflate) is fully
  orthogonal, confirmed via code.** Capability verification
  (`events.VerifyCapabilityCoverage`/`ResolveCapabilityValue`/
  `RecordCapabilityApplied`) is called from entirely separate adapter
  call sites (`adapters/{mqtt5,zeromq,mqtt}/{caller,reqreply_transport,
  serve_subscribers}.go`) than anything Middleware-related — these two
  mechanisms never share a code path, a struct field, or a dispatch
  point. Nothing about splitting `Middleware[In,Out]` into reusable vs.
  bound classes touches Capability verification at all.
- **`ports.Pattern` (the `RESTPattern`/`EventPattern`/`ReqReplyPattern`/
  `MCPPattern` binding layer) has ZERO coupling to `Middleware`/
  `HandleMW`/`SubscribeMW`/the bound-shape-detection mechanism —
  confirmed via grep of the entire `ports/` package.** `ports` builds
  handles by calling `Route`/`Channel`/`Tool.Register(builder)`, which
  internally dispatches through whichever Middleware mechanism a given
  route/channel declared — but `ports` itself never references
  `Middleware`, `BoundMiddleware`, `HandleMW`, or any of this doc's
  renamed/removed symbols directly. The "Scope decisions" table's claim
  that `ports` is a non-goal/unaffected by this split is independently
  confirmed accurate.

## Unit test plan (sketch)

| Test | Verifies |
|---|---|
| `TestBoundMiddleware_ReqMatch_AttachesSuccessfully` (×3 packages) | `BoundMiddleware[Req,...]` attaches cleanly to a matching `Route[Req,...]` |
| `TestBoundMiddleware_ReqMismatch_ReturnsTypedError` (×3) | Attaching `BoundMiddleware[Foo,...]` to `Route[Bar,...]` returns `BoundMiddlewareReqMismatchError`, not a silent no-op or panic |
| `TestMiddleware_NoLongerBindable_RuntimeRejected` (×3) | A plain `Middleware[In,Out]` (no bound capability) passed to `HandleBoundMW` is rejected the same way as a type mismatch — RUNTIME-rejected (via the `boundContributor[Req]` assertion failing), matching Open Decision 1's resolution (method+assertion, not the rejected compile-time-safe free-function option) |
| `TestBoundMiddleware_GrantedScopes_MergedIntoCheckScopes` (×3, reusing last session's regression tests, migrated) | End-to-end GrantedScopes enforcement still works identically through the NEW type |
| `TestBoundMiddleware_SharedAcrossMultipleRoutes_SameReqType` (REST/reqreply) | ONE `BoundMiddleware[ComputeReq,...]` value attaches to multiple routes sharing that `Req` type — confirms the "same-Req-type reuse" capability is preserved |
| `TestAmbiguousMiddlewareAttachmentError_Removed` (×3) | Confirms D7's error type/check no longer exists in the codebase (a documentation/completeness test, or simply its absence confirmed by the full suite passing without it) |
| `TestCheckCoverage_BoundMiddleware_AlwaysSatisfied` (×3) | A route/channel with ONLY a `BoundMiddleware`-contributed scheme never trips `MissingSecurityMiddlewareError` (structurally impossible) |
| `TestCheckCoverage_PlainRouteMetaSecurity_StillChecked` (×3) | A route declaring `RouteMeta.Security` directly, with NO `BoundMiddleware` attached, still trips `MissingSecurityMiddlewareError` as before |

## Files to create/modify

| File | Responsibility |
|---|---|
| `api/rest/bound_middleware.go` (new) | `BoundMiddleware[Req,In,Out]`/`BoundClientMiddleware[Req,In,Out]` (each with a NAMED, non-embedded `mw Middleware[In,Out]` field — see "API surface" for why embedding would be wrong), `NewBoundMiddleware`/`NewBoundClientMiddleware`/`BoundSecurityMiddleware`/`BoundSecurityClientMiddleware`, merge-field builder methods (one-line forwarders onto the named field) |
| `api/rest/middleware_declaration.go` | Remove `applyBoundRoute`/`applyBoundClientRoute` from `Middleware[In,Out]` |
| `api/rest/middleware.go` | Remove `isBoundHandleMWShape`/`isBoundClientMWShape` reflection detection from `HandleMW`/`ClientMW`; add `HandleBoundMW`/`ClientBoundMW` + `boundContributor[Req]`/`boundClientContributor[Req]` interfaces **for BOTH `Route[Req,Resp]` and `SSERoute[Req,Event]`** (SSERoute has its own, separate `HandleMW`/`ClientMW` definitions in this same file — 4 new methods total, not 2); delete D7 (`AmbiguousMiddlewareAttachmentError`) |
| `api/rest/transform.go` | NO signature changes to the 9 existing helpers (`buildMiddlewareHandlerAny`, `buildAgnosticMiddlewareHandler`, `buildClientMiddlewareHandlerAny`, `buildAgnosticClientMiddlewareHandler`, `buildEncodeIn`, `buildDecodeOut`, `satisfiesOf`, `specContributionOf`, `boundSpecContributionOf`) — `BoundMiddleware`'s `applyBoundRoute` calls them with its named `mw` field, exactly as `Middleware[In,Out]`'s own `applyBoundRoute` does today |
| `api/rest/builder.go` | Add `buildErr error` field to `routeBuilder`; check it early in `registerHandle` (mirrors `InvalidPathError`'s existing early-return pattern) — see "Attachment mechanics" |
| `api/events/bound_middleware.go` (new) | `BoundSubscribeMiddleware[T,In,Out]`/`BoundPublishMiddleware[T,In,Out]` + constructors — same named-field layout as REST's `BoundMiddleware` |
| `api/events/middleware_declaration.go`, `api/events/builder.go` | Same removal/addition pattern as REST, including the new `buildErr`-equivalent field on events' own builder struct |
| `api/events/transform.go` | Same "no signature changes" treatment as `api/rest/transform.go` |
| `api/reqreply/bound_middleware.go` (new) | `BoundMiddleware[Req,In,Out]` (reqreply's own copy) + constructors — same named-field layout |
| `api/reqreply/middleware_declaration.go`, `api/reqreply/middleware.go` | Same removal/addition pattern as REST |
| `api/reqreply/transform.go` | Same "no signature changes" treatment |
| `api/reqreply/route.go` | Same `buildErr`-equivalent field addition as REST — reqreply's `routeBuilder` struct lives in `route.go`, NOT `builder.go` (confirmed via code; differs from REST's/events' package layout, where the equivalent struct IS in `builder.go`) |
| `adapters/mqtt5/{adapter,caller,transport,reqreply_transport}.go`, `adapters/mqtt/{adapter,caller,transport}.go`, `adapters/zeromq/{adapter,transport,serve_subscribers,reqreply_transport}.go` | Replace `isBoundHandleMWShape`/`isBoundSubscribeMWShape`-style reflection detection with direct dispatch against the new types (no shape-guessing — the type IS the signal) |
| `examples/rest-api/{routes,handlers,chiserver,nethttpserver}/*`, `examples/events-api/{routes,handlers,mqtt5broker,mqttbroker,zeromqbroker}/*`, `examples/reqreply-api/{routes,handlers,mqtt5server,zeromqserver}/*` | Migrate every legacy-Security-pairing usage to the reusable class (`SecurityMiddleware[AuthIn,AuthOut].WithReceive(fn)`), except reqreply's zeromq OAuth case → `BoundSecurityMiddleware`/`BoundSecurityClientMiddleware` |
| `examples/go-edge-models/app/registry/auth.go`, `examples/adapters-nethttp-client/main.go`, `examples/adapters-sse/main.go`, `examples/mutable-security-keys/main.go`, `examples/api-events/main.go` | Same migration to the reusable class |
| `docs/features/security.md` | Rewrite the "Codec-backed Security"/"Choosing `[struct{},struct{}]` vs a real `[In,Out]`" sections entirely — there is no longer a `[struct{},struct{}]` choice to make for Security, only reusable-vs-bound |
| `.github/instructions/go-codex.instructions.md` | Update `middleware`/`api/rest`/`api/events`/`api/reqreply` rows |
| `docs/design/d-0003-codec-declared-middlewares.md` | New `Addendum 7` documenting the split (see Final Phase below) |
| `docs/design/d-0007-declarative-middleware-layering.md` | Reworked (not just addended) mechanism sections — see Final Phase below |
| `examples/{rest,events,reqreply}-api/demo_bound_middleware_split.go` (new) | Side-by-side reusable-vs-bound demos — see Final Phase below |
| `docs/roadmap/bound-middleware-split.md` (this doc) | Deleted once its content is fully folded into `d-0007`/`d-0003` — see Final Phase below |

## Final phase — demos, design-doc rework, and this doc's own retirement

This section resolves the "3-way disposition decision deferred to ship
time" the original "See also" section below used to defer. Decision
(confirmed with the user): **no new `docs/design/` doc is created.**
Instead, this roadmap doc's content is folded into the TWO existing
design docs it already refines, and this doc is deleted once that's
done.

### New demos (one per package — reusable alone, bound alone, AND stacked together)

Beyond migrating existing legacy-Security-pairing examples to the
reusable class (covered in the "Files to create/modify" table above),
add ONE new, dedicated demo file per package that shows **three**
things, not just two:

1. The reusable class alone.
2. The bound class alone.
3. **Stacked/layered** — BOTH classes attached to the SAME route/
   channel at once, demonstrating real composition.

| File | Reusable alone | Bound alone | Stacked together |
|---|---|---|---|
| `examples/rest-api/demo_bound_middleware_split.go` | `SecurityMiddleware[AuthIn,AuthOut].WithReceive(fn)` (existing GrantedScopes pattern) | `BoundSecurityMiddleware[Req_i, AuthIn, AuthOut]` — migrated `ProfileScopeMw`/`AdminScopeMw`, reused across 2+ DIFFERENT route `Req` types sharing one `verifyScopes` helper (today's genuine cross-`Req`-type case) | ONE route (reusing `ProfileReq`/`AdminActionReq`) with `.Use(reusable).HandleBoundMW(bound)` — generic bearer-token presence/validity check layered with a route-specific scope check reading that route's own `Req` |
| `examples/events-api/demo_bound_middleware_split.go` | Existing GrantedScopes `Middleware[In,Out]` pattern | `BoundSubscribeMiddleware[T,In,Out]` reused across 2+ subscribers sharing one `T` — **doc comment must note this is a structural/API-parity demo, not bug-motivated**: no current events usage genuinely needs Class 2 (confirmed during the original investigation) | ONE subscriber with `.Use(reusable).SubscribeBoundMW(bound)` |
| `examples/reqreply-api/demo_bound_middleware_split.go` | mqtt5 bearer-token pattern (property-decoded, `*Req` discarded) | `BoundMiddleware[OAuthComputeReq,In,Out]`/`BoundClientMiddleware[OAuthComputeReq,In,Out]` — migrated zeromq in-payload OAuth credential pattern (`VerifyOAuthComputeZeroMQ`/`validOAuthCredFn`/`invalidOAuthCredFn`), **the one genuine Class-2-necessary case found in this repo today** | ONE route (the zeromq OAuth route) with `.Use(reusable).HandleBoundMW(bound)` — a generic cross-cutting concern (e.g. observability/rate-limit) layered with the in-payload credential check |

**Layering composes today, structurally — no new mechanism required**:
`Route`'s (and `Subscriber`'s/`Publisher`'s) `opts` slice accumulates
EVERY attach call (`.Use()`, `.HandleMW()`, the new `.HandleBoundMW()`/
`.SubscribeBoundMW()`) the same way, dispatched in **declaration order**
at `Register` time. A route/channel can already carry both a reusable
contribution and a bound contribution simultaneously; the ORDER they're
declared in the fluent chain is the dispatch order, so the "stacked"
demo cases above deliberately choose `.Use(reusable).HandleBoundMW(bound)`
(generic concern runs first) to make the layering visible and
order-aware, not merely "both present."

Each demo wires into its package's `main.go`, following the exact
convention `demo_granted_scopes_context_field.go` already established.

### Design-doc rework (not a new doc)

- **`docs/design/d-0007-declarative-middleware-layering.md`** — this is
  the doc whose bound-shape-detection mechanism (`isBoundHandleMWShape`-
  style reflection) this split directly replaces. Rework its mechanism-
  describing sections (API surface, "Current state", any section
  documenting the reflection-based bound-shape detectors) to describe
  the new explicit `BoundMiddleware[Req,In,Out]`/
  `BoundSubscribeMiddleware[T,In,Out]`/`BoundPublishMiddleware[T,In,Out]`
  types instead. The historical Phase A/B/C rollout narrative (Rollout
  Phase A/B/C sections, Learnings subsections) stays intact as a
  historical record — only the still-current-tense mechanism
  descriptions get reworked, clearly marked as superseded where the
  narrative references the old reflection mechanism directly.
- **`docs/design/d-0003-codec-declared-middlewares.md`** — append a new
  `Addendum 7` documenting the bound/reusable split, mirroring Addenda
  1-6's own established format ("What changed" / "Validation"
  subsections), cross-referencing `d-0007`'s reworked sections rather
  than duplicating them.

### This doc's own retirement

Once the above is done, **delete `docs/roadmap/bound-middleware-split.md`
entirely** (not promote it to `docs/design/`) — remove its row from
`docs/roadmap/index.md` and its nav entry from `zensical.toml`'s
`[nav.Roadmap]`. `.github/instructions/go-codex.instructions.md` is
synced last, pointing at the new types and the reworked `d-0007`/`d-0003`
Addendum 7 sections instead of this (by-then-deleted) roadmap doc.

## Out of scope

- `api/mcp` — no `Middleware[In,Out]` mechanism exists there at all.
- Raw TRANSPORT-level access beyond what codec-decoded `Req`/`T` and
  merge fields already express (e.g. TLS client cert, remote IP,
  unstructured header iteration) — confirmed unused by any current
  example. This is **permanently** out of scope for this mechanism, not
  deferred: it is a structurally different, lower (adapter/transport)
  layer than `BoundMiddleware`'s codec-decoded `Req`/`T` access, and the
  raw-adapter-Fn pairing mode that used to offer a loose approximation of
  it is closed for good by this doc (see Motivation, above). A genuine
  future need at that layer would require its own, separately-designed,
  adapter-level mechanism — never a reopening of `HandleMW`/`ClientMW`'s
  Fn-pairing to raw-transport shapes.
- Changing `middleware.ServerImplementation`/`ClientImplementation`'s own
  shape — these remain the general-purpose decorator's underlying
  carrier type, untouched.

## Cross-roadmap considerations — other PENDING (not-yet-implemented) roadmap docs this design touches

Checked every entry in `docs/roadmap/index.md` for coupling with this
doc's mechanism (reflection-based dispatch, `Middleware[In,Out]`'s
current three-role entanglement). Four docs have a confirmed
relationship — each got a cross-reference note added pointing back here:

- **[`zeromq-rest-adapter.md`](zeromq-rest-adapter.md)** — CONCRETE
  collision: its "Security/Middleware dispatch" section explicitly plans
  to mirror TODAY's reflection-based mechanism
  (`validateImplementationShapesReflect`'s "paired-security-Fn shape
  check") byte-for-byte. Sequencing matters: whichever of the two docs
  ships first, the other needs a follow-up pass (either update that
  doc's wording to match the new `BoundMiddleware`-based dispatch, or add
  `adapters/zeromqrest/transport.go` to THIS doc's own migration list).
  The underlying `RouteHandle.Implementations`/`MiddlewareHandlers`
  FIELDS stay stable either way — only the description of what populates
  them changes.
- **[`amqp-adapter.md`](amqp-adapter.md)** — no current Security/
  Middleware content (purely transport/topology-scoped so far), but its
  eventual adapter-dispatch wiring will need to target whichever
  mechanism (this doc's reusable or bound classes) is current at THAT
  implementation time. AMQP's `BasicProperties.Headers` property
  side-channel means it likely only needs the reusable class, mirroring
  `adapters/mqtt5`, not the narrow zeromq in-payload bound case.
- **[`websocket-declarative-middleware.md`](websocket-declarative-middleware.md)**
  — confirmed NO current `Middleware[In,Out]` mechanism exists for
  WebSocket at all (no collision today). IF that doc's open "should
  WebSocket gain one?" question is ever answered yes, the design should
  start from THIS doc's resolved two-class split from day one, rather
  than re-growing into the single-entangled-type-with-reflection mistake
  a 4th time.
- **[`mcp-ports-declarative-middleware.md`](mcp-ports-declarative-middleware.md)**
  — already independently designed its `ToolMiddleware`/`FileMiddleware`/
  `CacheMiddleware`/`DirMiddleware[In,Out]` types as agnostic-only (no
  bound variant at all), so no collision today. If a bound variant is
  ever added there, it should reuse THIS doc's resolved named-field
  (never anonymous-embedded) internal layout to avoid the identical
  accidental-interface-satisfaction trap Finding A (above) found and
  fixed here.

Confirmed NO coupling (checked via grep, no action taken): `redis-pubsub.md`,
`mqtt5-capability-extensions.md`, `tcp-adapter.md`, `webhook-adapter.md`,
`dynamic-port-rebinding.md` — `webhook-adapter.md`'s `Middleware
func(http.Handler) http.Handler` field is the general-purpose decorator
shape, already confirmed orthogonal to this split (see Motivation,
above).

## See also

- `docs/design/d-0003-codec-declared-middlewares.md` — the original
  codec-backed middleware design this doc refines; gains `Addendum 7`
  once this ships (see "Final phase" above).
- `docs/design/d-0007-declarative-middleware-layering.md` — the Rollout
  Phase A/B/C history this doc's "legacy/bound" entanglement originates
  from; its mechanism sections are REWORKED (not just addended) once
  this ships (see "Final phase" above). This roadmap doc is deleted once
  both are updated — no new `docs/design/` doc is created for this split.
- `docs/features/security.md` — the user-facing guide this doc's
  shipped outcome will significantly rewrite.
