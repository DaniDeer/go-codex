# Declarative Middleware as Partial Route/Channel Definitions — and Security Credentials as the Proving Case

> **Status:** Design draft — all open design decisions resolved (see
> "Open design decisions" below); not yet implemented.
> [← Back to Roadmap](index.md)

## Motivation

This doc has two layers, by explicit user direction:

1. **The general principle** — across `api/rest`, `api/events`, AND
   `api/reqreply`, a `Middleware[In,Out]` value is not a bolt-on
   decorator; it is **a partial route/channel definition**. It declares
   its OWN merge fields (mapping `In`/`Out` struct fields to wire
   locations — header/cookie/query for REST, topic/property for
   events/reqreply) using the IDENTICAL constructors
   (`NewRequiredHeaderParam`/`NewRequiredTopicParam`/etc.) a route's or
   channel's own Req/Resp already uses, and its own handler Fn
   (`WithReceive`/`WithSend`). **Stacking N such partial definitions on
   top of the route/channel's own declaration aggregates them into ONE
   final, complete declaration.** This is not a metaphor or aspiration —
   confirmed via code, it is EXACTLY what `applyParamDeclarations`
   (`api/rest/middleware.go`, mirrored in `api/events`/`api/reqreply`)
   already does today: it builds independent per-kind contribution maps,
   merging the route's/channel's OWN declared params with EVERY ATTACHED
   MIDDLEWARE's contributed params into one spec, with conflict detection
   (`ConflictingSecurityDeclarationError`/`DuplicateMiddlewareNameError`)
   across layers — exactly the behavior a genuine "stacked partial
   declarations aggregate into one final declaration" model requires.
2. **The proving case** — Security credentials are the one place this
   principle is NOT followed today, across ALL THREE packages. A
   Security-flagged `Middleware[struct{},struct{}]` is dispatched through
   an entirely separate, adapter-shaped Fn contract
   (`http.Header`/`*http.Request` for REST; `[]UserProperty`/`*T` for
   events+reqreply's MQTT adapters) instead of using its OWN
   `WithReceive`/`WithSend` + merge-field vocabulary like every other
   middleware already does. This doc proposes fixing Security
   SPECIFICALLY, as the concrete, motivating demonstration that the
   general principle holds uniformly — not a security-only fix bolted on
   sideways.

## Confirmed via code: the general mechanism already exists, symmetrically, in all three packages

| Package | Request/Subscribe-side (decode) | Response/Publish-side (encode) | Merge-field vocabulary |
|---|---|---|---|
| `api/rest` | `buildDecodeIn` (`transform.go`) — decodes incoming header/cookie/query into `In` before `.WithReceive` | `buildEncodeIn` — encodes `In` into OUTGOING header/cookie/query via `.WithSend` (client); `buildEncodeOut`/`buildDecodeOut` handle the Resp/Out direction | `WithRequestHeader`/`WithRequestCookie`/`WithRequestQuery` (In); `WithResponseHeader`/`WithResponseCookie` (Out) |
| `api/events` | `buildDecodeIn` (`transform.go`) — decodes incoming topic/property vars into `In` before `.WithReceive(fn func(ctx, In) error)` (Subscribe) | `buildEncodeOut` — encodes `Out` into outgoing topic/property vars via `.WithSend(fn func(ctx) (Out, error))` (Publish) | `WithSubscribeTopic`/`WithSubscribeProperty` (In); `WithPublishTopic`/`WithPublishProperty` (Out) |
| `api/reqreply` | `buildDecodeIn`/`buildDecodeOut` (`transform.go`) | `buildEncodeIn`/`buildEncodeOut` — FULLY symmetric, mirroring REST's duplex (request/response) shape exactly | `WithRequestTopic`/`WithRequestProperty` (In); `WithResponseTopic`/`WithResponseProperty` (Out) |

Each package's directionality matches its OWN communication pattern
(REST/reqreply are duplex request+response; events' Subscribe/Publish
are each one-directional) — but the UNDERLYING model is identical in
all three: a merge-field-declared `In`/`Out`, encoded/decoded generically
by the dispatch layer, with a handler Fn in between. Security, in ALL
THREE packages, bypasses this entirely today.

## Architectural consequence: a SHARED, explicit layering mechanism — not just a documented convention

User direction: this layering must be **reflected in the architecture
and the mechanism the code introduces**, not merely documented as a
shared mental model while three independent implementations continue to
coincidentally look alike. Checked the actual duplication this implies
fixing — it is real and already self-acknowledged in the code:
`api/reqreply/transform.go`'s own doc comment on `buildDecodeIn` says,
verbatim, **"mirrors rest's identical function, adapted to reqreply's
two axes (topic, property) instead of REST's three (header, cookie,
query)"** — the SAME structural logic (iterate N wire-location axes,
`codex.DecodeVars`/`EncodeVars` against each axis's declared fields,
validate the whole value via `InCodec`/`OutCodec`, wrap failures in
`MiddlewareInputError`/`MiddlewareOutputError`) is hand-duplicated THREE
times today (`api/rest/transform.go`, `api/events/transform.go`,
`api/reqreply/transform.go`), differing only in axis COUNT (2 or 3) and
axis NAMES (header/cookie/query vs. topic/property).

**Proposed: extract this into ONE shared, generic mechanism in the
`middleware` package**, so "a layer is a partial declaration with
declared wire axes + a handler, aggregated with its siblings" becomes an
actual shared TYPE/FUNCTION every package's dispatch calls into — not
three independently-maintained look-alikes kept in sync by convention
and code-review vigilance alone.

```go
// middleware — NEW, shared across api/rest/api/events/api/reqreply.

// Axis describes ONE wire-location axis (e.g. "header", "topic") a
// Declaration[In,Out] value can merge a struct field against — the
// axis-generic form of what REST's reqHeaderParams/reqCookieParams/
// reqQueryParams (and events/reqreply's topic/property equivalents)
// already are per-package today, just not yet a SHARED type.
type Axis[T any] struct {
    Name   string // diagnostic only — "header", "cookie", "query", "topic", "property"
    Fields []codex.FieldCodec[T]
}

// DecodeLayer decodes a T from N axes' wire-value maps (each axis's OWN
// map — e.g. headerVars, cookieVars, queryVars — passed positionally,
// matched to axes by index), validates the assembled T via codec, and
// wraps any failure in a caller-supplied error constructor — the ONE
// shared implementation [rest.buildDecodeIn]/[events.buildDecodeIn]/
// [reqreply.buildDecodeIn] all become thin callers of.
func DecodeLayer[T any](
    name string,
    codec codex.Codec[T],
    newInputErr func(name string, err error) error,
    axesAndVars ...AxisVars[T],
) (T, error)

// EncodeLayer is DecodeLayer's encode-side mirror — validates T first,
// then derives each axis's wire-value map via codex.EncodeVars.
func EncodeLayer[T any](
    name string,
    codec codex.Codec[T],
    newOutputErr func(name string, err error) error,
    value T,
    axes ...Axis[T],
) ([]map[string]string, error) // one map per axis, same order as axes
```

Each package's OWN `buildDecodeIn`/`buildEncodeIn`/`buildEncodeOut`/
`buildDecodeOut` becomes a THIN wrapper: supply ITS OWN axis definitions
(REST: header/cookie/query; events/reqreply: topic/property) and ITS OWN
error constructor (`rest.MiddlewareInputError`/`events.
MiddlewareInputError`/`reqreply.MiddlewareInputError` — these stay
PACKAGE-SPECIFIC, since `errors.As` callers need to distinguish which
package's middleware failed), then delegate the actual decode/encode/
validate/wrap logic to the shared `middleware.DecodeLayer`/`EncodeLayer`
— eliminating the 3-way hand-duplication, not just documenting it away.

**This is a pure internal refactor with ZERO behavior change** — fully
verifiable by running EVERY existing test in all 3 packages' `transform_test.go`-equivalents unchanged before/after; the shared
mechanism must produce byte-identical results to today's 3 separate
implementations for every currently-passing test case. This is Phase 1,
and a hard PREREQUISITE for Phase 2 (Security's generalization) — once
`DecodeLayer`/`EncodeLayer` exist and all 3 packages delegate to them,
extending Security to use the SAME mechanism becomes a natural
consequence of the architecture, not a 4th hand-written variant to keep
in sync.

### Phase 1 sub-item: thread `codex`'s existing sparse-field capability through `EncodeVars`

Checked (via a "think outside the box" question about reusing
`codex.PartialField`/`PartialStruct`'s layering — the mechanism
`examples/go-edge-models/iotedge`'s manifest patches use) whether a
general codex-level layering primitive could ALSO serve this doc's
merge-field axes. Conclusion: `PartialStruct`/`PartialField` itself
solves a DIFFERENT, body-shaped problem (patch semantics within ONE JSON
object — confirmed a deliberately separate, parallel interface to
`FieldCodec[T]` per its own doc comment) and is not directly reusable
here. But the research surfaced a smaller, genuinely reusable, ALREADY
BUILT primitive:

- `codex/omitempty.go`'s `sparseFieldCodec[T]` is an ADDITIVE, OPTIONAL
  companion capability any `FieldCodec[T]` may implement (NOT a parallel
  type system) — `Struct`'s own `Encode` loop already checks for it
  (`codex/object.go:150`: `if sf, ok := f.(sparseFieldCodec[T]); ok {
  ...encodeSparse... }`), falling back to the normal `encode` otherwise.
  `OmitEmptyField`/`OmitEmptyFieldFunc`/`OmitDefaultField`/`MaybeField`
  all implement it today.
- **Confirmed gap**: `codex.EncodeVars` — the function underlying EVERY
  merge-field axis in all 3 packages (and this doc's own proposed
  `middleware.EncodeLayer`) — calls `f.encode(v)` unconditionally, with
  NO equivalent check (`codex/varfields.go`). No merge-field axis
  anywhere (header/cookie/query/topic/property) can express "omit this
  wire value entirely if unset" today — e.g. an optional
  `X-Idempotency-Key` header that should simply not be sent when unset.

**Folded into Phase 1's scope**: extend `codex.EncodeVars` to also check
for `sparseFieldCodec[T]`, mirroring `Struct`'s existing pattern exactly
— zero new codec concept, reusing `OmitEmptyField`/`MaybeField` as-is.
Once `middleware.EncodeLayer` is built on top of the now-extended
`EncodeVars` (Phase 1's own main item), every merge-field axis in all 3
packages inherits "omit if unset" support for free, including Security's
own credential merge fields (Phase 2) — e.g. an optional credential
sub-field that should be omitted from the wire entirely when the
`.WithSend` Fn didn't set it.

### Phase 1 sub-item: fail-fast vs. accumulate-all errors across stacked layers

Checked (via a question about modeling middleware as a `forge`-style
pipeline with an Either(Result,Error)-shaped error channel) whether
go-codex's existing control flow already matches this shape. Confirmed
it does, just via idiomatic Go `(T, error)` rather than an explicit
Either wrapper:

- `forge.Compose` chains functions sequentially, short-circuiting on the
  first error (`mid, err := f1.Apply(a); if err != nil { return
  ...err }`) — a classic railway/Either-shaped composition, already
  shipped.
- `api/rest.DispatchMiddlewareHandlers` (`api/rest/transform_dispatch.go`)
  already does exactly what "partial results flow into the resulting
  output" describes for middleware: it iterates N stacked layers
  sequentially, collecting EACH layer's own Out value independently into
  `outs[i]` (later merged into the response) — but SHORT-CIRCUITS the
  instant ANY layer's `DecodeIn` or Fn errors.

**The one genuine, previously-undecided asymmetry this surfaced**:
layers ACROSS a stack are fail-fast (stop at the first failing layer),
while fields WITHIN one layer's own axis decode already ACCUMULATE every
field's error into one `ValidationErrors` (via `codex.DecodeVars`/
`EncodeVars`, never stopping early). Literally introducing an
`Either[Result,Error]` wrapper type is NOT the recommended fix (go-codex
already prefers typed errors + `errors.As`, per its own established
"Six mandatory requirements" convention — a formal Either/Result
algebraic type would be a foundational, un-idiomatic departure for
little real benefit, since `(T, error)` already IS go-codex's Either).
**Folded into Phase 1's scope as an open design decision** (below):
should `middleware.DecodeLayer`'s dispatch across STACKED layers also
accumulate every layer's error (extending `ValidationErrors`' existing
precedent upward from "fields within an axis" to "layers within a
stack"), or is today's fail-fast-across-layers behavior deliberate and
should stay? Not pre-decided — a real behavior change either way,
needing its own confirmation before Phase 1 ships.

Note: literally reusing `forge.Function[In,Out]` as the middleware step
type itself was explicitly considered and rejected — it carries a
SHA-256 contract hash and governance metadata (author/approver/approval
date) built for KPI-computation governance, irrelevant ceremony for an
ordinary middleware layer. The two mechanisms share PHILOSOPHY (named,
typed, composed, short-circuiting steps), not code worth merging.

## The pivot, concretely — all three packages

**Today (REST):**
```go
var BearerAuthDeclaration = rest.SecurityMiddleware("bearerAuth", route.BearerScheme("JWT"), nil)
route.ClientMW(&BearerAuthDeclaration, func(ctx context.Context, reqs []route.SecurityRequirement) (http.Header, error) {
    h := make(http.Header)
    h.Set("Authorization", "Bearer "+token) // hand-built, adapter-aware
    return h, nil
})
```

**Proposed (REST) — Security IS just a stacked partial declaration:**
```go
type BearerCredential struct{ Token string }

var BearerAuthDeclaration = rest.SecurityMiddleware[BearerCredential, struct{}](
    "bearerAuth", route.BearerScheme("JWT"), nil,
).WithRequestHeader(rest.NewRequiredHeaderParam("Authorization", bearerHeaderCodec,
    func(c BearerCredential) string { return c.Token },
    func(c *BearerCredential, v string) { c.Token = v },
))

route.Use(BearerAuthDeclaration.WithSend(func(ctx context.Context) (BearerCredential, error) {
    return BearerCredential{Token: token}, nil
}))
```
`BearerAuthDeclaration` contributes an `Authorization` header merge field
+ a credential-producing Fn — **exactly** the way any other
`.Use(someOtherMw)` middleware contributes an `X-Trace-Id` header merge
field + its own Fn today. No special case.

**Today (events/reqreply, mqtt5):**
```go
var BearerAuthDeclaration = events.SecurityMiddleware("bearerAuth", route.BearerScheme("JWT"), nil)
channel.PublishMW(&BearerAuthDeclaration, func(ctx context.Context, msg *T, reqs []route.SecurityRequirement) ([]mqtt5.UserProperty, error) {
    return []mqtt5.UserProperty{{Key: "Authorization", Value: "Bearer " + token}}, nil
})
```

**Proposed (events/reqreply) — the SAME pivot, mqtt5's User Property carrier instead of HTTP headers:**
```go
type BearerCredential struct{ Token string }

var BearerAuthDeclaration = events.SecurityMiddleware[struct{}, BearerCredential](
    "bearerAuth", route.BearerScheme("JWT"), nil,
).WithPublishProperty(events.NewRequiredPropertyParam("Authorization", bearerPropertyCodec,
    func(c BearerCredential) string { return c.Token },
    func(c *BearerCredential, v string) { c.Token = v },
))

channel.Use(BearerAuthDeclaration.WithSend(func(ctx context.Context) (BearerCredential, error) {
    return BearerCredential{Token: token}, nil
}))
```
(`api/reqreply` mirrors this identically via `WithRequestProperty`/
`.WithSend(fn func(ctx) (In, error))`, matching its REST-like duplex
shape rather than events' publish-only shape.)

## Confirmed via code: scope-checking is ALREADY separable, zero change needed there — in all three packages

`runSecurityMiddleware`-equivalents already treat scope-checking as a
GENERIC post-Fn step, independent of credential shape and independent of
package: every attached security Fn returns a `granted
map[string][]string`, merged across all attached Fns, THEN
`middleware.CheckScopes(secReqs, granted)` runs ONCE. Confirmed identical
in `adapters/nethttp/adapter.go` (REST), `adapters/mqtt5/{caller.go,
adapter.go,reqreply_transport.go}` (events + reqreply). This pivot only
needs to change HOW the Fn receives its credential input / produces its
wire output — the granted-scopes return value and `CheckScopes` call are
UNCHANGED everywhere.

## Phasing

This doc now has TWO concrete implementation phases, not one:

- **Phase 1 — shared mechanism (prerequisite, zero behavior change).**
  Extract `middleware.DecodeLayer`/`EncodeLayer` (or equivalently-named
  equivalents, see Open design decisions); migrate all 3 packages'
  `buildDecodeIn`/`buildEncodeIn`/`buildEncodeOut`/`buildDecodeOut` onto
  it, representative-sample-then-full-sweep, fully verified by every
  EXISTING test continuing to pass unchanged (this refactor must be
  invisible to any current caller).
- **Phase 2 — Security as the proving consumer.** Generalize
  `SecurityMiddleware[In,Out]` in all 3 packages and route its dispatch
  through the NOW-SHARED Phase 1 mechanism — by construction uniform
  across packages, not a 4th hand-written variant.

Phase 1 is independently valuable (removes a confirmed, self-acknowledged
3-way duplication) even if Phase 2 were deferred — but Phase 2 is what
actually PROVES the shared mechanism is correctly general, not
REST-shaped-with-mqtt5-bolted-on.

## Scope decisions

| In scope | Out of scope |
|---|---|
| **Phase 1**: a shared `middleware.DecodeLayer`/`EncodeLayer` mechanism, with all 3 packages' existing `buildDecodeIn`/`buildEncodeIn`/`buildEncodeOut`/`buildDecodeOut` migrated onto it as thin wrappers | Changing `route.SecurityScheme`'s own shape — unchanged, in all 3 packages |
| **Phase 2**: generalizing `SecurityMiddleware`'s signature in ALL THREE packages — `rest.SecurityMiddleware[In, Out any](...)`, `events.SecurityMiddleware[In, Out any](...)`, `reqreply.SecurityMiddleware[In, Out any](...)` — away from the hardcoded `struct{}, struct{}`, dispatched through Phase 1's shared mechanism | Inventing any new merge-field/codec type — explicitly rejected; the EXISTING per-package merge-field constructors are reused verbatim |
| Preserving each package's OWN error types (`rest.MiddlewareInputError` vs `events.MiddlewareInputError` vs `reqreply.MiddlewareInputError`) — Phase 1's shared mechanism takes an error CONSTRUCTOR callback, it does not unify the error TYPES themselves (`errors.As` callers must still distinguish which package failed) | Unifying `MiddlewareInputError`/`MiddlewareOutputError` into one cross-package type — explicitly rejected, would break existing `errors.As` call sites for no benefit |
| Preserving the granted-scopes return value + `CheckScopes` call exactly as today, in all 3 packages (confirmed separable) | Changing `CheckScopes`'s own logic or signature |
| `adapters/nethttp`/`chi` (HTTP: header/cookie/query) AND `adapters/mqtt5`/`zeromq`/`mqtt` (user-property) — Phase 2 only, once Phase 1's shared mechanism exists to dispatch Security through | A brand-new wire-location kind beyond header/cookie/query/property |
| A breaking replacement of today's fixed-shape `ClientImplementation.Fn`/`ServerImplementation.Fn` signatures for Security specifically, in all 3 packages (Phase 2) | Changing the fixed-shape Fn contract for NON-Security general-purpose middleware — `WithReceive`/`WithSend`'s own existing contract is unchanged; Security adopts it, it doesn't change it |

## Current state (confirmed via code, for contrast — all three packages)

- **REST client-side** (`adapters/nethttp/clienttransport.go`'s
  `mergeCredentialHeaders`): `ClientImplementation.Fn` type-asserted to
  the FIXED shape `func(context.Context, []route.SecurityRequirement)
  (http.Header, error)` — bypasses `ClientMiddlewareHandler`/`EncodeIn`
  entirely.
- **REST server-side** (`adapters/nethttp/adapter.go`'s
  `runSecurityMiddleware`): `ServerImplementation.Fn` type-asserted to
  `func(context.Context, *http.Request, *Req) (map[string][]string,
  error)` — same bypass, raw `*http.Request` handed directly to the Fn.
- **events/reqreply publish-side** (`adapters/mqtt5/transport_dispatch.go`,
  `adapters/mqtt5/reqreply_transport.go`): `func(context.Context, *T,
  []route.SecurityRequirement) ([]UserProperty, error)` — also bypasses
  the agnostic `EncodeOut`/`WithSend` dispatch; ALSO note the Fn receives
  `*T` (the outgoing payload pointer) for in-payload credential
  embedding, a capability this redesign must explicitly decide to keep
  or drop (see Open design decisions).
- **Post-Fn credential validation** (`api/rest/security_dispatch.go`'s
  `ValidateSecurityCredentials`/`extractCredential`; mirrored per package):
  re-extracts the credential, strips the scheme's own prefix, validates
  against `SecurityScheme.Codec *codex.Codec[string]` — SEPARATE from the
  Fn's own return. Under the pivot, this role is naturally absorbed by
  `mw.InCodec.Validate(in)` (already running generically for ANY agnostic
  middleware, in all 3 packages) — `SecurityScheme.Codec` likely becomes
  redundant, see Open design decisions.

## API surface

```go
// api/rest
func SecurityMiddleware[In, Out any](schemeName string, scheme SecurityScheme, scopes []string) Middleware[In, Out]

// api/events
func SecurityMiddleware[In, Out any](schemeName string, scheme SecurityScheme, scopes []string) Middleware[In, Out]

// api/reqreply
func SecurityMiddleware[In, Out any](schemeName string, scheme SecurityScheme, scopes []string) Middleware[In, Out]
```

No NEW types are introduced beyond this signature change in each
package — `.WithSend`/`.WithReceive`/the merge-field methods are ALL
pre-existing `Middleware[In,Out]` methods, reused verbatim, per package.

**Granted-scopes convention (RESOLVED — Option 3, "conventional field"):**
`Out` keeps the EXACT SAME uniform signature every other agnostic
middleware uses (`func(ctx, In) (Out, error)` — no 3-tuple return, no
Security-specific Fn arity). A Security `Out` type is simply EXPECTED to
carry a field named `GrantedScopes map[string][]string`:

```go
type BearerAuthOut struct {
    GrantedScopes map[string][]string
    // ... any OTHER genuine response merge fields a caller wants, e.g.:
    // RefreshedSession string `header:"Set-Cookie"` (illustrative — actual
    // declaration via .WithResponseHeader/.WithResponseCookie as usual)
}
```

The adapter's dispatch reads `GrantedScopes` via
`elem.FieldByName("GrantedScopes")` — the SAME reflection technique
ALREADY used pervasively in this codebase (e.g.
`clienttransport.go`'s `elem.FieldByName("Descriptor")`) — not a new
technique, just applied to a new, conventionalized field name. `Out`
remains free to carry additional genuine response data on OTHER fields,
declared via the EXISTING `WithResponseHeader`/`WithResponseCookie`
methods, unaffected by the `GrantedScopes` convention.

```go
// adapters/nethttp (chi mirrors) + adapters/mqtt5 (zeromq/mqtt mirror) —
// Security's client/server dispatch changes from a type-asserted FIXED
// Fn shape to invoking the mw's OWN ClientMiddlewareHandler/
// MiddlewareHandler (EncodeIn/DecodeIn/EncodeOut/DecodeOut +
// WithSend/WithReceive's Fn) — the SAME dispatch already built for
// non-Security agnostic middleware in each package.
//
// Adapter-side plumbing (RESOLVED): adapters/mqtt5/adapter.go's publish
// dispatch ALREADY calls BOTH the old ClientImplementations-based
// Security path (runPublishSecurityImpls, line 997) AND the new
// ClientMiddlewareHandlers-based agnostic path (events.
// DispatchPublishMiddlewareHandlers, line 925) in the SAME function
// today. Phase 2 simply STOPS populating/reading ClientImplementations
// for Security-shaped attachments specifically — the dispatch call
// already exists and already runs. wrapPublishGeneral's SEPARATE
// general-wrap-shaped ClientImplementations usage is UNRELATED and
// explicitly untouched (out of scope). Identical dual-path structure
// confirmed in adapters/mqtt5/reqreply_transport.go.
```

## Structured errors (all implement `slog.LogValuer`)

No NEW error type needed in any package for the credential-encoding
axis — a merge field's codec rejection already surfaces as the EXISTING
`MiddlewareInputError`/`MiddlewareOutputError` (REST),
`events.MiddlewareInputError`/`MiddlewareOutputError`,
`reqreply.MiddlewareInputError`/`MiddlewareOutputError` — used by every
other agnostic middleware today; Security's credential merge fields
reuse the SAME error types per package, not new ones.
**`SecurityScheme.Codec` redundancy (RESOLVED):** confirmed via code —
`rest.SecurityScheme.Codec *codex.Codec[string]` has EXACTLY ONE
consumer, `ValidateSecurityCredentials` (`api/rest/security_dispatch.go`),
which re-validates the credential AFTER the Fn's return is already
merged into the wire request — a post-hoc re-check of content
`mw.InCodec.Validate(in)` ALREADY validates, earlier and more directly,
once a scheme migrates to the new mechanism. Confirmed zero
OpenAPI/AsyncAPI spec-rendering dependency on `Codec` (`render/openapi`/
`render/asyncapi` consume only the base `route.SecurityScheme`, which has
no `Codec` field). Plan: deprecate `SecurityScheme.Codec` once Phase 2's
full sweep deletes the old dispatch path that was its only caller;
`SecurityCredentialError` retires alongside it.

## Observer integration

No new observer hooks in any package — reuses `stats.SecurityObserver
.RecordSecurityRejection` exactly as today; a rejected credential merge
field already flows through the SAME `stats.ReportErrors`/
`"middleware:in"` location string every other agnostic middleware's
input decode failure already uses, in all 3 packages.

## Unit test plan (sketch — per package, mirrored)

**Phase 1 (shared mechanism — zero behavior change, verified not assumed):**

| Test | Verifies |
|---|---|
| `TestDecodeLayer_MultiAxis_MatchesExistingRestBehavior` | `middleware.DecodeLayer` given REST's 3 axes (header/cookie/query) produces IDENTICAL results to today's `rest.buildDecodeIn` for every existing REST middleware test case |
| `TestDecodeLayer_MultiAxis_MatchesExistingEventsBehavior` | Same, for events' 2 axes (topic/property) |
| `TestDecodeLayer_MultiAxis_MatchesExistingReqreplyBehavior` | Same, for reqreply's 2 axes |
| `TestEncodeLayer_MatchesExistingBehavior` (×3 packages) | `middleware.EncodeLayer`'s encode-side mirror, same cross-check |
| **Full regression**: every EXISTING `*_test.go` test in `api/rest/transform_test.go`/`api/events/transform_test.go`/`api/reqreply/transform_test.go`-equivalents must continue passing UNCHANGED after the Phase 1 migration — this is the actual proof of "zero behavior change," not a design-review claim (per this repo's own established "verify by migration, not by review" lesson) |

**Phase 2 (Security as the proving consumer):**

| Test | Verifies |
|---|---|
| `TestSecurityMiddleware_WithSend_MergeField_EncodesCredential` (×3 packages) | A `.WithSend` Fn returning `Cred{Token: "x"}` + a merge-field declaration results in the correctly-formatted wire value — reusing each package's NOW-SHARED `DecodeLayer`/`EncodeLayer`, not a new mechanism |
| `TestSecurityMiddleware_WithReceive_DecodesCredentialBeforeFn` (×3) | An incoming message's declared field is decoded into `Cred` BEFORE the `.WithReceive` Fn runs |
| `TestSecurityMiddleware_GrantedScopes_ReadViaReflectedField` (×3) | The RESOLVED `GrantedScopes` convention: dispatch reads `Out.GrantedScopes` via `elem.FieldByName("GrantedScopes")` and feeds it into an UNCHANGED `middleware.CheckScopes` call |
| `TestSecurityMiddleware_Out_CarriesAdditionalResponseFields` (×3) | A Security `Out` type with BOTH `GrantedScopes` AND a genuine response merge field (e.g. a declared response header) — confirms the convention doesn't foreclose real response data |
| `TestSecurityMiddleware_CredentialCodecRejects_MiddlewareInputError` (×3) | A credential field's codec validation failure surfaces as the EXISTING, package-specific `MiddlewareInputError`, not a new or unified type |
| `TestSecurityMiddleware_RawRouteVsRouteHandle_Unaffected` (REST only) | This redesign does not reintroduce or interact with the separate `GlobalSecurity` dual-mode dispatch gap (D-0001 Addendum 6) — independent concerns |
| `TestDispatchMiddlewareHandlers_FailFast_StopsAtFirstFailure` (×3, Phase 1) | Confirms the RESOLVED fail-fast decision is preserved by the Phase 1 migration — a 2nd/3rd stacked layer's Fn must NOT run after an earlier layer already failed |
| `TestSecurityMiddleware_InPayloadMutation_NotSupported` (mqtt5 only) | Confirms the RESOLVED "drop" decision — migrating a Security middleware onto the new mechanism has no way to mutate the outgoing payload; documents the limitation via a compile-shape/doc-level test, not a runtime capability test |

## Files to create/modify

| File | Responsibility | Phase |
|---|---|---|
| `middleware/layer.go` (new) | `Axis[T]`/`AxisVars[T]`, `DecodeLayer`/`EncodeLayer` — the new shared mechanism | 1 |
| `api/rest/transform.go` | Migrate `buildDecodeIn`/`buildEncodeIn`/`buildEncodeOut`/`buildDecodeOut` onto `middleware.DecodeLayer`/`EncodeLayer` | 1 |
| `api/events/transform.go` | Same migration, 2-axis (topic/property) | 1 |
| `api/reqreply/transform.go` | Same migration, 2-axis (topic/property), fully duplex like REST | 1 |
| `api/rest/middleware_declaration.go` | `SecurityMiddleware`'s generalized signature | 2 |
| `api/rest/middleware.go`/`client.go`/`adapter.go` (nethttp/chi) | Replace fixed-shape Fn type-assertions with dispatch through Phase 1's shared mechanism | 2 |
| `api/events/middleware_declaration.go`, `api/events/builder.go` | Same generalization for events | 2 |
| `api/reqreply/middleware_declaration.go`, `api/reqreply/route.go` | Same generalization for reqreply | 2 |
| `adapters/mqtt5/{transport.go,transport_dispatch.go,reqreply_transport.go}`, `adapters/zeromq`, `adapters/mqtt` | Replace fixed-shape Fn type-assertions with dispatch through the mw's own handler | 2 |
| `examples/go-edge-models/app/registry/auth.go` | Migrate `newAuthCredentialFunc`/`BearerAuthDeclaration` onto the new pattern — the motivating real case (REST) | 2 |
| `docs/features/security.md` | Full rewrite of the credential-Fn sections to show the new declarative pattern, for all 3 packages | 2 |
| `docs/design/d-0003-codec-declared-middlewares.md` | Add a new Addendum documenting `middleware.DecodeLayer`/`EncodeLayer` as the architectural embodiment of "middleware is a partial route/channel definition, stacked" — the mechanism itself is now the documentation, not just prose describing a convention | 1 |

## Out of scope (Phase 2)

- OAuth2 token-endpoint response body credentials (a fundamentally
  different shape).
- Automatic prefix/format derivation from `scheme.Scheme` — Phase 1 keeps
  formatting explicit in the closure, matching the existing merge-field
  convention exactly.
- `api/mcp`/`ports` — confirmed (per `mcp-ports-declarative-middleware.md`,
  a sibling roadmap doc) to have a DIFFERENT, single-phase attachment
  model with no spec/two-phase declare-dispatch split; Security is
  already permanently N/A for MCP and not revisited here.
- **mqtt5's in-payload credential embedding via `*T` mutation on
  publish (RESOLVED: dropped)** — confirmed via code this is the ONLY
  place in the entire codebase (including the mechanism being replaced)
  where a middleware Fn gets automatic mutation access to an OUTGOING
  payload; every other send-side mechanism in all 3 packages
  (`ClientTransform`, the agnostic `WithSend` path) deliberately passes
  the value by VALUE, not pointer, on the theory the caller already owns
  and can mutate it before calling. Confirmed zero real test/example in
  this repo exercises the capability today (every test using this exact
  Fn signature leaves the `*T` parameter unused). A caller needing
  in-payload embedding does it themselves, directly, inside their own
  handler — not via the generic middleware mechanism.

## Open design decisions — ALL RESOLVED

All 8 items below were open as of this doc's prior draft. Each is now
resolved, with the decision and rationale recorded directly (not just a
pointer to a separate discussion) so this doc stays self-contained.

1. **Fail-fast vs. accumulate-all errors across stacked layers —
   RESOLVED: fail-fast (keep today's behavior).**
   `middleware.DecodeLayer`'s dispatch across stacked layers stops at
   the FIRST failing layer, exactly matching
   `DispatchMiddlewareHandlers`'s current, confirmed behavior — NOT
   changed to accumulate-all. Rationale: avoids a later layer's side
   effects (e.g. a credential Fn making a network call) running
   unconditionally after an earlier layer has already failed. This was
   surfaced via a tangential question about modeling middleware as an
   Either(Result,Error)-style pipeline — confirmed go-codex's existing
   `(T, error)` dispatch already IS this "railway" shape; no new
   mechanism (e.g. a formal `Either[Result,Error]` wrapper type) is
   introduced.
2. **`codex.EncodeVars`/`sparseFieldCodec[T]` extension — RESOLVED:
   yes, in scope for Phase 1.** Extend `EncodeVars` to check for the
   existing `sparseFieldCodec[T]` companion capability, mirroring
   `Struct`'s own `Encode` loop (`codex/object.go:150`) exactly — zero
   new codec concept, reuses `OmitEmptyField`/`MaybeField` as-is. Small,
   additive, not a hard prerequisite for `DecodeLayer`/`EncodeLayer`'s
   main work but bundled into the same phase.
3. **`Axis[T]`/`AxisVars[T]` shape: positional vs. named — RESOLVED:
   positional.** Every current call site has a FIXED, compile-time-known
   axis count (REST: 3; events/reqreply: 2), declared once per package —
   no dynamic/variable-axis scenario exists anywhere to justify a named
   `map[string]map[string]string` shape. Positional also matches today's
   existing calling convention exactly (`buildDecodeIn(headerVars,
   cookieVars, queryVars map[string]string)` is already positional). A
   throwaway spike against REST's 3-axis case still happens as the FIRST
   concrete Phase 1 implementation step — this decision gives that spike
   a target shape to build against, rather than resolving the shape
   question blind.
4. **Risk of subtle behavior regression during Phase 1 extraction —
   not a decision, a carried-forward discipline.** No choice to make
   here; the test plan's "every existing test must pass unchanged" bar
   IS the mitigation. Flagged as a real risk to watch during migration,
   not assumed away — but nothing to resolve in the design itself.
5. **`adapters/mqtt5`/`zeromq`/`mqtt` adapter-side plumbing — RESOLVED:
   small, well-scoped changes needed, confirmed via code.** Traced
   `adapters/mqtt5/adapter.go`'s publish dispatch directly: it ALREADY
   calls BOTH the old `ClientImplementations`-based Security path
   (`runPublishSecurityImpls`, line 997) AND the new
   `ClientMiddlewareHandlers`-based agnostic path (`events.
   DispatchPublishMiddlewareHandlers`, line 925) in the SAME function
   today. Phase 2 simply stops populating/reading `ClientImplementations`
   for Security-shaped attachments — the new path's dispatch call already
   exists and already runs; `wrapPublishGeneral`'s separate general-wrap
   use of `ClientImplementations` is untouched. Identical dual-path
   structure confirmed in `adapters/mqtt5/reqreply_transport.go`.
6. **`SecurityScheme.Codec` vs. `Middleware[In,Out].InCodec` redundancy
   — RESOLVED: confirmed redundant, plan to deprecate.** `SecurityScheme
   .Codec` has exactly one consumer (`ValidateSecurityCredentials`, a
   post-hoc re-check after the Fn's return is already merged into the
   wire request) and zero OpenAPI/AsyncAPI spec-rendering dependency.
   `mw.InCodec.Validate(in)` already validates the same semantic content,
   earlier and more directly, once a scheme migrates. Deprecate
   `SecurityScheme.Codec`/`SecurityCredentialError` once Phase 2's full
   sweep deletes the old dispatch path that was `Codec`'s only caller.
7. **Granted-scopes shape — RESOLVED: Option 3, a conventional
   `GrantedScopes` field on `Out`.** `Out` keeps the EXACT uniform
   `func(ctx, In) (Out, error)` signature every other agnostic middleware
   uses — no 3-tuple return, no Security-specific Fn arity. A Security
   `Out` type is expected to carry a `GrantedScopes map[string][]string`
   field, read by dispatch via `elem.FieldByName("GrantedScopes")` (the
   same reflection technique already used pervasively in this codebase,
   e.g. `clienttransport.go`'s `elem.FieldByName("Descriptor")`). `Out`
   stays free to carry additional genuine response merge fields on other
   fields of the same struct. Considered and rejected: `Out` being the
   map directly (forecloses genuine response data); a separate 3-tuple
   return channel (reintroduces Security-specific Fn arity).
8. **mqtt5's in-payload credential embedding (`*T` on publish) —
   RESOLVED: dropped.** Confirmed via code this is the ONLY place in the
   entire codebase — including the mechanism being replaced — where a
   middleware Fn gets automatic mutation access to an OUTGOING payload.
   Every other send-side mechanism in all 3 packages (`ClientTransform`,
   the agnostic `WithSend` path) deliberately passes the value by VALUE,
   not pointer (confirmed: REST's/events'/reqreply's `ClientTransform`
   doc comments all say the caller already owns and can mutate its own
   value before calling). Confirmed zero real test/example in this repo
   exercises the capability today (every test using this exact Fn
   signature leaves the `*T` parameter unused). Accepted as a documented
   Phase 2 limitation (see "Out of scope" above) — a caller needing
   in-payload embedding does it themselves, inside their own handler.
9. **Backward-compatibility migration mechanics for Phase 2 — RESOLVED:
   mechanical, representative-sample-then-full-sweep migration, NO
   transitional dual-dispatch window.** Follows directly from this doc's
   own already-confirmed scope decision (a full breaking replacement,
   not an additive/dual-dispatch mechanism) — the SAME migration
   discipline already used for this session's other breaking changes
   (e.g. middleware-consolidation's Phase D). `adapters/nethttp/
   *_test.go` alone has 15+ existing credential-Fn call sites; mqtt5/
   zeromq/mqtt add more — migrated mechanically, not via a transitional
   window. (Phase 1 itself has NO backward-compatibility concern — it is
   purely internal.)
