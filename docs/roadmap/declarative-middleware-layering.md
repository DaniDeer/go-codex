# Declarative Middleware as Partial Route/Channel Definitions — and Security Credentials as the Proving Case

> **Status:** Rollout Phase A (`api/rest`) AND Rollout Phase B
> (`api/events`) IMPLEMENTED and fully verified (`go build`/`go vet`/
> `go test`/`just check`/every `examples/*/` clean; see "Learnings from
> Rollout Phase A"/"Learnings from Rollout Phase B" below). Phase C
> (`api/reqreply`) remains NOT yet implemented — all other design
> decisions below (including Phase 2-4's cross-API design) stay resolved
> and current.
>
> **A Phase C review found a confirmed gap in Phase B's own shipped
> code — tracked as a PREREQUISITE patch, scheduled BEFORE Phase C, not
> folded into it**: `events.SecurityMiddleware[In,Out]`'s
> `GrantedScopes`-on-`Out` convention (Open design decision 7) was never
> actually wired into any adapter's dispatch (confirmed zero
> `GrantedScopes` references in `adapters/mqtt5`/`mqtt`/`zeromq`), and
> events' Subscribe-side bound Fn shape structurally cannot carry `Out`
> at all. See Open design decisions 11-12 below for the full finding and
> resolution (an additive, non-breaking Subscribe shape widening +
> merge-and-enforce wiring, both directions, one events-only patch).
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

## Middleware mental model — In/Out/ContextField/Error, confirmed via code

A reference section, added so future phases (and future readers) don't
need to re-derive this — the full answer to "what role does each part of
`Middleware[In,Out]` actually play," confirmed against the shipped code
in `api/rest` (the other two packages reuse the identical model — see
the closing note below).

### 1. `In` is ALWAYS the request vocabulary, `Out` is ALWAYS the response vocabulary — only the encode/decode ROLE flips by direction

Confirmed precisely against `MiddlewareHandler`/`ClientMiddlewareHandler`'s
own doc comments (`api/rest/transform.go`) — **`In`/`Out` do NOT swap
meaning by attachment side.** `In` is unconditionally the REQUEST-side
wire vocabulary; `Out` is unconditionally the RESPONSE-side wire
vocabulary. What genuinely differs between server and client is only
whether each is being DECODED (read, by whichever role RECEIVES that
message) or ENCODED (written, by whichever role SENDS it):

| Direction | Attachment | Fn shape (bound) | `In` (request vocabulary) | `Out` (response vocabulary) |
|---|---|---|---|---|
| **Server** (RECEIVES the request, SENDS the response) | `HandleMW` (or `.Use()` + `WithReceive`, agnostic) | `func(ctx, *Req, In) (Out, error)` | **Decoded** from the incoming request's headers/cookies/query (`DecodeIn`), using the middleware's OWN merge-field declarations | **Encoded** into the outgoing response's headers/cookies (`EncodeOut`), from whatever Fn returns |
| **Client** (SENDS the request, RECEIVES the response) | `ClientMW` (or `.Use()` + `WithSend`, agnostic) | `func(ctx, Req) (In, error)` | **Encoded** into the outgoing request's headers/cookies/query (`EncodeIn`), from whatever Fn returns | **Decoded** from the incoming response's headers/cookies (`DecodeOut`) — mechanical, no Fn involved |

**Revised this round — see "Architecture revision" below**:
`Transform`/`ClientTransform` no longer exist as separate call sites;
`HandleMW`/`ClientMW` themselves now provide the `*Req`-accessing, bound
Fn shape shown in this table, via reflection.

The pattern: whoever RECEIVES a message DECODES its vocabulary;
whoever SENDS a message ENCODES it. A server always receives requests
and sends responses, so it always decodes `In`/encodes `Out`. A client
always sends requests and receives responses, so it always encodes
`In`/decodes `Out`. Neither role ever decodes/encodes the "wrong"
struct — `In` is never read from a response, `Out` is never written
into a request.

This is also why a single `Middleware[In,Out]` value, attached on BOTH
client and server for the same concern, is fully bidirectional without
any duplication: the server's `Out` (encoded into its response) and the
client's `Out` (decoded from that SAME response) share one declared
vocabulary — produced on one side, consumed on the other.

### 2. `ContextField` is separate and orthogonal to In/Out

`middleware.ContextField[V]` (`middleware/context_field.go`) is NOT part
of the In/Out merge-field vocabulary at all — it is a codec-typed
context slot: `Set` by one middleware layer, `Get` by a LATER layer or
by the route/channel handler, all sharing ONE mutable box pre-allocated
once per request (`EnsureContextFields`). It deliberately does **not**
feed spec rendering (confirmed via its own doc comment — "use this
INSTEAD OF adding security-specific fields to every route's own Req
type") — it exists for a DERIVED value with no natural wire location of
its own (e.g. a `UserID` parsed out of a validated token), not for
anything that should appear in an OpenAPI/AsyncAPI document.

Confirmed scope today: wired server-side only
(`adapters/nethttp`/`chi`'s dispatch calls `EnsureContextFields`) — never
on REST's client side, never in `api/events`/`api/reqreply` at all. This
doc's Phase 3 (folded into the per-API rollout phases below) extends
`ContextField` to those missing surfaces via two new declarative link
methods, `SetContextFieldFromIn`/`SetContextFieldFromOut`.

### 3. Current implementation status: Security is the ONE confirmed exception to this model — RESOLVED by the Architecture revision below, not a future Phase 2

Sections 1–2 above describe the GENERAL mechanism — confirmed via
passing tests to be fully shipped today for ordinary, non-Security
middleware. **Security specifically does NOT use this mechanism yet —
confirmed via code, not assumed:**

- **Dispatch**: `adapters/nethttp/adapter.go`'s `runSecurityMiddleware`
  (server-side) dispatches the OLD `middleware.ServerImplementation.Fn`
  — a FIXED shape, `func(ctx, *http.Request, *Req)
  (map[string][]string, error)` — NOT `MiddlewareHandler`'s `In`/`Out`
  shape from section 1 at all. Client-side,
  `adapters/nethttp/binding.go`'s `mergeCredentialHeaders` dispatches
  `middleware.ClientImplementation`s, NOT `ClientMiddlewareHandler`s.
  This is this entire roadmap doc's starting premise (see
  "Motivation" above) — restated here so section 1's table isn't
  mistaken for something already true of Security today.
- **Error fallback**: confirmed via code, Security's own Fn error today
  wraps as `rest.SecurityError` (a DISTINCT type from the general
  `rest.MiddlewareError` section 4 below describes) with a default
  fallback status of **401** (`http.StatusUnauthorized`) — not the
  generic 500 ordinary middleware falls back to. It IS
  `ErrorPattern`-eligible today (confirmed via an existing code comment
  citing an already-shipped fix) — but these fallback specifics are
  Security-only, not the general case.

**Closing this gap — making Security use the IDENTICAL mechanism as
ordinary middleware, sections 1–2's table included — is resolved by the
"Architecture revision: dropping `Transform`/`ClientTransform`" section
below, as PART OF folding `HandleMW`/`ClientMW` onto a single,
reflection-based dispatch — NOT a separate, later Phase 2.** Until that
revision ships, read sections 1–2 as "how general-purpose middleware
already works, and how Security will work once this revision ships" —
not as Security's current behavior.

### 4. Error mapping is a 2-tier fallback, plus a separate Fn-error classification

**Tier 1 — declarative, checked first.** A route declares an
`ErrorPattern` (an `errors.As`-matched response shape); server-side
dispatch calls `RouteHandle.DispatchErrorResponse`, which checks this
FIRST — on a match, it encodes the matched value's own response merge
fields, validates them, and writes the declared status/body/headers onto
the wire; the caller returns immediately (`handled == true`).

**Tier 2 — generic fallback.** Unmatched (or a non-`Respond` action)
falls through to the adapter's own plain, pre-existing error handling —
confirmed via code: `errFn(w, r, http.StatusInternalServerError, err)` —
an opaque, always-500, caller-configurable `ErrorHandler`. This is the
"somehow mapped" catch-all: every error that isn't an explicitly declared
pattern becomes a generic 500 by default.

**A separate, independent classification governs a middleware's OWN Fn
errors specifically** (confirmed via `transform_dispatch.go`'s
`middlewareDispatchError`): a `DecodeIn` failure (the middleware's own
merge-field decode/validation failing) is NEVER `ErrorPattern`-eligible —
no business error exists yet at that point. A Fn's own RETURNED business
error IS `ErrorPattern`-eligible, falling back to the package's own
`MiddlewareError` type (e.g. `rest.MiddlewareError`) when no pattern
matches it.

**Client-side**, a returned error may wrap an `ErrorPatternValuer` value
(reconstructed from whatever the server actually wrote for a matched
pattern) — extracted in one call via `rest.ErrorPatternAs[T](err)` or
dispatched via `rest.HandleErrorPattern(err, rest.Case(...), ...)`; an
error with no such wrapped value is an ordinary, unstructured Go error
(a network failure, a non-pattern-matched status, etc.).

### 5. Client/server is a real, confirmed, structural distinction

`HandleMW` (server-side implementation attachment) and `ClientMW`
(client-side implementation attachment) are genuinely separate
attachment points, each with its own Fn shape (table above) — not two
names for the same mechanism. A single `Middleware[In,Out]` value (e.g.
Security) can be attached via EITHER or BOTH on the same route, which is
exactly how a service that is both a REST client (calling an upstream)
and a REST server (serving its own callers) reuses one declared scheme
for both roles.

### `api/events`/`api/reqreply` reuse this identical model

Confirmed: both packages follow the SAME 5-part model above, with their
own package-specific wire vocabulary (topic/property merge fields
instead of header/cookie/query) substituted in — including the same
confirmed Security exception (section 3): events/reqreply's Security
dispatch ALSO uses an old, fixed-shape Fn contract today
(`[]UserProperty`/`*T`), not the `In`/`Out` shape, for the identical
reason. The one confirmed, genuine asymmetry — `api/events`' Subscribe (`WithReceive`) produces NO
`Out` at all, unlike REST/reqreply's fully symmetric shape — is covered
in depth in the Phase 3 section below ("Package-by-package verdict").

**A second, separate clarification (Phase B model-review round): section
1's closing "fully bidirectional" paragraph is REST/reqreply-specific
framing that does NOT carry over literally to events.** That paragraph
describes a TRUE request/response duplex — one conversation, two ends,
where the server's `Out` and the client's `Out` are the SAME wire
payload, produced by one side and consumed by the other. **Events has no
such duplex on a single channel** — Subscribe and Publish are
independent message streams (even on the same topic), not a paired
request+response, so there is no analogous "produced on one side,
consumed on the other" relationship between a channel's Subscribe `In`
and its Publish `Out`. Reusing ONE declared `events.Middleware[In,Out]`
value across a `SubscribeMW` attachment and a `PublishMW` attachment
(same or different channel) remains FULLY supported — via the
ALREADY-SHIPPED `.Use()`-style cross-attachment reuse every agnostic
middleware already gets — just without REST's "shared wire payload"
relationship, since there is no single conversation to share in the
first place.

### 6. Summary: what goes where, what's codec-declared, what reaches spec

A consolidated wrap-up of sections 1–5 above, for quick reference.

| | `In` | `Out` | `ContextField` | Errors |
|---|---|---|---|---|
| **Role** | Request-side wire vocabulary — always, never flips | Response-side wire vocabulary — always, never flips | Orthogonal ctx-local slot, not wire vocabulary at all | 2-tier: declarative pattern + generic fallback |
| **Wire location** | header/cookie/query (REST) · topic/property (events/reqreply) | header/cookie (REST) · publish topic/property (events) | **None** — purely in-process, never transmitted | `ErrorPattern`'s declared status/body/headers, or opaque adapter fallback |
| **Server side** | **Decoded** from the incoming request (`DecodeIn`) | **Encoded** into the outgoing response (`EncodeOut`), from Fn's return | `Set` by an earlier layer, `Get` by a later layer/the handler | `DispatchErrorResponse`: pattern match → write; else generic fallback |
| **Client side** | **Encoded** into the outgoing request (`EncodeIn`), from Fn's return | **Decoded** from the incoming response (`DecodeOut`) — mechanical, no Fn | Extended here by Phase 3 (not yet shipped) | `ErrorPatternAs`/`HandleErrorPattern` extract a typed payload |
| **Codec-declared?** | Yes — `WithRequestHeader`/`Cookie`/`Query` (or topic/property equivalents), `FieldCodec[In]`-backed | Yes — `WithResponseHeader`/`Cookie` (or publish equivalents) | Partially — codec-typed for type safety, but deliberately opts OUT of spec rendering | The `ErrorPattern` payload type is codec/struct-backed (spec-rendered); the generic fallback is not |
| **Goes to spec?** | Yes — request parameters | Yes — response parameters | No, by design (its own doc comment: "instead of adding security-specific fields to every route's Req") | Only the declared `ErrorPattern` shape; the generic 500/401 fallback is opaque |
| **Body access?** | Raw `*Req` pointer only (via `HandleMW`/`ClientMW` directly, per the Architecture revision above) — manual Go mutation, ZERO codec, ZERO spec contribution (reuses the route's own already-rendered body schema) | None at all — Fn never receives `*Resp`; response body is EXCLUSIVELY the route handler's own job | N/A | N/A |

**The short version:**

- **`In`/`Out` are the codec-declared, spec-contributing halves** —
  strictly header/cookie/query/topic/property, never body. This is
  where the "declarative middleware" story lives: merge fields,
  validated by a `FieldCodec`, rendered into OpenAPI/AsyncAPI
  automatically.
- **Full request body is reachable too — but as a raw pointer, not a
  codec.** `Fn` gets `*Req` (already decoded by the route's own body
  format) and can read/mutate it freely; this contributes nothing to
  spec, since the route's own body schema already owns that once.
  Response body has no equivalent at all — asymmetric, by design.
- **`ContextField` is the one deliberately non-spec channel** — a
  typed, in-process relay for values with no wire shape of their own
  (a parsed `UserID`, not a new header).
- **Errors split cleanly**: a DECLARED shape (`ErrorPattern`,
  codec-backed, spec-visible) checked first, falling back to an OPAQUE
  default (generic 500, or 401 for Security specifically) when nothing
  matches.

## Architecture revision: dropping `Transform`/`ClientTransform` — folded into `HandleMW`/`ClientMW` via reflection, Security unified in the SAME step

A design decision reached this round, revising earlier sections of this
doc (which still describe `Transform`/`ClientTransform` as a separate
mechanism from `HandleMW`/`ClientMW` — those sections remain useful
historical/research record, but this section is now authoritative on
the attachment mechanism itself).

### The decision

**`Transform`/`ClientTransform` (and their SSE counterparts,
`TransformSSE`/`ClientTransformSSE`) are REMOVED as separate free
functions.** `HandleMW`/`ClientMW` become the ONLY server/client
attachment methods — and they gain `Transform`'s `*Req`-access
capability directly, dispatched via RUNTIME REFLECTION rather than a
Go type parameter.

**Why this is mechanically possible now, when it wasn't before**:
`Transform` was ORIGINALLY forced to be a free function
(`Transform[Req,Resp,In,Out](...)`, not a method) because Go disallows a
method from introducing NEW type parameters beyond its receiver's own —
`Middleware[In,Out]` only has 2 type parameters; attaching it to a
concrete `Route[Req,Resp]` with `*Req` access needs 2 MORE (`Req`,
`Resp`), which only a free function can supply. Reflection sidesteps
this limitation entirely — confirmed via code, Security's EXISTING
`ServerImplementation.Fn`/`ClientImplementation.Fn` ALREADY dispatch an
untyped `any` Fn via `reflect.ValueOf(fn).Call(...)`, giving it
effective `*Req` access with ZERO type parameters on the attaching
method (`HandleMW`/`ClientMW` are already plain methods on
`Route[Req,Resp]`, which DOES know its own concrete `Req`/`Resp` at the
call site — reflection just needs to resolve the erased `any` back to
the CONCRETE `*Req` the method's own receiver already carries).
**Applying this SAME technique to `MiddlewareHandler`/
`ClientMiddlewareHandler`'s dispatch (today split across `Transform` and
`HandleMW`) unifies both into ONE method, ONE dispatch mechanism.**

### What is preserved, unchanged

- **The channel/route-AGNOSTIC, reusable style** (`WithReceive`/
  `WithSend` + plain `.Use(mw)`, no `*Req` access, one middleware value
  usable verbatim across many different `Req`-typed routes) is KEPT
  exactly as it is today. `HandleMW`/`ClientMW` detect bound-vs-agnostic
  via reflection, REUSING the EXISTING `Agnostic` bool-detection logic
  `MiddlewareHandler`/`ClientMiddlewareHandler` already implement
  (confirmed via `api/rest/transform.go`) — not a new mechanism, just a
  single entry point instead of one split across 2 call sites.
- **`DecodeLayer`/`EncodeLayer`** (Phase 1's shared merge-field codec
  extraction) is UNAFFECTED — it governs HOW a middleware's own
  header/cookie/query merge fields decode/encode, entirely independent
  of WHICH method attaches the middleware.
- **The granted-scopes/`CheckScopes` call**, and the **DecodeIn-vs-Fn
  error classification** (section 3/4 of the Middleware mental model),
  are UNAFFECTED — both operate on the ALREADY-DECODED `In`/the Fn's
  OWN returned error, independent of the attachment mechanism.

### The Security-unification consequence

Because `HandleMW`'s/`ClientMW`'s Fn dispatch is now reflection-based
for EVERY middleware (not uniquely for Security), Security's EXISTING Fn
shapes — server: `func(ctx, *http.Request, *Req) (map[string][]string,
error)`; client: `func(ctx, []route.SecurityRequirement) (http.Header,
error)` — become recognizable as JUST ONE MORE shape the NOW-UNIFIED
dispatch already handles. `adapters/nethttp/adapter.go`'s
`runSecurityMiddleware` and `adapters/nethttp/binding.go`'s
`mergeCredentialHeaders` — today's SEPARATE code paths, confirmed in the
Middleware mental model's section 3 — are RETIRED, folded into the SAME
unified dispatch `HandleMW`/`ClientMW` now use for every other
middleware. **This directly resolves section 3's "Security is the ONE
confirmed exception" finding as PART OF this architecture revision — not
as a separate, later Phase 2.**

### Confirmed mechanical details (found during Phase A's design review, before implementation)

A dedicated review pass (per this doc's own per-phase workflow: design
→ review → plan → implement), run before Phase A's implementation
began, traced the Architecture revision down to the exact Go
mechanics. Found 4 concrete, confirmed, all-mechanically-resolvable
details that were previously unstated — documented here so
implementation doesn't have to re-derive them.

1. **Migration scale, precisely confirmed**: ~45 real test call sites
   across `adapters/nethttp/*_test.go`/`adapters/chi/*_test.go`
   (mirrored) PLUS `examples/error-types/main.go` (a real example, 3
   call sites) use `rest.Transform`/`TransformSSE`/`ClientTransformSSE`
   directly. The migration itself is MECHANICAL — a pure rename
   (`rest.Transform(route, mw, fn)` → `route.HandleMW(mw, fn)`,
   confirmed identical Fn signature, confirmed `SSERoute` already has
   its own `HandleMW`/`ClientMW` methods for the SSE variants too) —
   consistent with the already-resolved "mechanical,
   representative-sample-then-full-sweep" Open design decision. See
   "Files to create/modify" below for the now-explicit scope.
2. **The type-erasure bridge mechanism — reuses an ALREADY-EXISTING
   pattern, invents nothing new.** `HandleMW`'s `mw
   middleware.RouteMiddleware` parameter is an INTERFACE — Go generics
   cannot let it recover `Middleware[In,Out]`'s concrete `In`/`Out` to
   build a `MiddlewareHandler` (the SAME "no new type params on a
   method" limitation that originally forced `Transform` to be a free
   function). Confirmed this EXACT problem is ALREADY solved, today, for
   the AGNOSTIC case: `routeMiddlewareContributor`
   (`api/rest/middleware.go`) — an unexported interface with
   `applyAgnosticRoute(rb *routeBuilder)`, implemented by
   `Middleware[In,Out]` using its OWN bound type parameters (zero NEW
   type params needed on the method — Go's own rule is satisfied simply
   because the METHOD's receiver already carries `In`/`Out`). `HandleMW`
   needs the SAME pattern extended with a NEW, analogous bound-case
   method (e.g. alongside `applyAgnosticRoute`) that builds a
   `MiddlewareHandler` carrying `*Req`-accessing dispatch instead of the
   agnostic shape. The legacy, non-codec `middleware.Middleware` (a bare
   `{Name, Security}` struct, confirmed NO merge-field vocabulary at
   all) needs NO such bridge at all — it stays on its existing, simpler
   `ServerImplementation` path, unchanged — a separate branch, not a
   conflict, since `HandleMW` can type-switch on `mw` itself before
   deciding which path applies.

   **Confirmed this round, with full precision**: `buildMiddlewareHandler[Req,
   Resp, In, Out any](mw Middleware[In,Out], fn func(ctx, *Req, In) (Out,
   error)) MiddlewareHandler` (Transform's own internal builder) —
   traced its FULL body: `Req`/`Resp` are STRUCTURALLY VESTIGIAL. They
   exist ONLY to type-check `fn`'s signature at Transform's OWN call
   site; the function BODY never references `Req`/`Resp` again (it
   stores `fn` directly into `MiddlewareHandler.Fn` as `any`, and builds
   `DecodeIn`/`EncodeOut` purely from `mw`'s In/Out). This means the new
   bound-case contributor method can be written with the EXACT same
   signature shape as `applyAgnosticRoute` plus one parameter —
   `applyBoundRoute(rb *routeBuilder, fn any)` — taking `fn` as `any`
   (matching `HandleMW`'s OWN already-erased `fn any` parameter
   precisely, zero adaptation needed at the call site) and ZERO new type
   parameters, confirmed mechanically trivial, not just plausible.

   **Also confirmed this round**: `HandleMW`'s CURRENT body
   (`api/rest/middleware.go`) calls `buildServerImplementation(mw, fn)`
   UNCONDITIONALLY — ZERO type-switch on `mw`'s concrete type exists
   today. The fix needs `HandleMW` to gain a type-switch mirroring
   `routeMiddlewareOpt.applyRoute`'s OWN EXISTING pattern exactly
   (`switch v := mw.(type) { case middleware.Middleware: ...;  case
   <new bound-contributor interface>: v.applyBoundRoute(&rb, fn); ...
   }`) — reusing, again, an established dispatch technique already
   proven elsewhere in the SAME file, not inventing a new one.

   **`applyBoundRoute`'s full responsibility, now completely specified**:
   confirmed via `checkMiddlewareNameUniquenessAndAttachment`'s own doc
   comment — the D6(b)/D7 checks (duplicate-name, ambiguous-dual-
   attachment) explicitly "cover BOTH attachment paths — plain `.Use()`
   ... and Transform/ClientTransform — since both feed
   `rb.middlewareSpecContributions`." Confirmed `boundSpecContributionOf[In,
   Out any](mw Middleware[In,Out])` (the function Transform ALSO calls
   today, separately from `buildMiddlewareHandler`) is EQUALLY Req/Resp-
   agnostic — only needs In/Out. So `applyBoundRoute` must do BOTH of
   Transform's 2 separate appends in one method: populate
   `rb.middlewareHandlers` (the bound `MiddlewareHandler`, this item's
   main subject) AND `rb.middlewareSpecContributions` (via
   `boundSpecContributionOf(m)`, UNCHANGED) — otherwise D6(b)/D7's
   existing, already-correct enforcement would silently stop covering
   `HandleMW`-attached middleware once `Transform` is removed. Confirmed
   mechanically complete — no remaining unknowns in this bridge.
   **Same fix applies identically to `SSERoute.HandleMW`/`ClientMW`**
   (confirmed via code: both call the SAME `buildServerImplementation`/
   inline `ClientImplementation`-building logic as `Route`'s own
   methods, zero SSE-specific divergence) — not a separate design, just
   2 more call sites for the SAME fix.
3. **Security's gating semantics need a new field to survive the
   fold-in — confirmed to affect the ALREADY-SHIPPED agnostic path too,
   not just the new bound case.** `MiddlewareHandler` (`Transform`'s
   dispatch unit) has NO `Satisfies []string` field at all — confirmed
   via grep — because it dispatches UNCONDITIONALLY (correct for its
   original, non-Security, always-run purpose). Security's pairing
   (matching `Satisfies` against the route's declared requirements)
   lives on `ServerImplementation.Satisfies` today. **Confirmed via
   code**: the EXISTING, ALREADY-SHIPPED `buildAgnosticMiddlewareHandler`/
   `buildAgnosticClientMiddlewareHandler` (`api/rest/transform.go`) —
   which "The pivot, concretely"'s OWN worked examples rely on, via
   `.Use(BearerAuthDeclaration.WithSend(...))` — populate NO `Satisfies`
   either, meaning THOSE examples, as currently written, would run their
   credential Fn UNCONDITIONALLY on every call through the attached
   route, regardless of whether that route actually declares the scheme
   as required. Folding Security onto the unified mechanism needs a NEW
   `Satisfies []string` field added to `MiddlewareHandler`/
   `ClientMiddlewareHandler`, populated the SAME way
   `buildServerImplementation` already populates it from `mw`'s Security
   declaration (`securityDeclarationOf(mw)`) — in BOTH the existing
   agnostic builders AND the new bound-case builder (item 2 above), not
   just the latter.
4. **`CheckCoverage` is hardcoded to `[]middleware.ServerImplementation`
   — confirmed, needs updating. Resolution refined this round: a small
   SIGNATURE change, NOT a storage merge.** Called from the ADAPTER
   layer (`nethttp.Serve`/chi) at Serve time, not builder time (per its
   own doc comment — this check can only run once BOTH the declaration
   AND the implementation are known). Confirmed via code
   (`api/rest/builder.go`): `RouteHandle` ALREADY carries
   `Implementations []middleware.ServerImplementation` AND
   `MiddlewareHandlers []MiddlewareHandler` as TWO SEPARATE, already-
   coexisting exported fields — no merge needed, that would be an
   unnecessarily invasive, public-API-breaking change. The precise fix:
   extend `CheckCoverage`'s signature to accept BOTH lists (e.g. a 2nd
   `handlers []MiddlewareHandler` parameter), checking `Satisfies`
   across BOTH — so a route whose Security migrated onto the new
   `MiddlewareHandler`-based path (item 2 above) is still correctly
   recognized as covered, avoiding a false-positive
   `MissingSecurityMiddlewareError`. Same fix needed in `events`'/
   `reqreply`'s OWN separately-defined `CheckCoverage` functions
   (confirmed near-identical duplicates, not shared code, per
   `api/events/builder.go`/`api/reqreply/middleware.go`) — relevant to
   Phase A for REST now, and to Phase B/C's own review passes later for
   events/reqreply.

### Worked example — a Security-shaped middleware needing `*Req` access, attached via `HandleMW` directly

```go
// No separate Transform call — HandleMW itself now gives fn *Req
// access, exactly like Transform used to, via the SAME reflection
// mechanism Security's OWN Fn already relied on internally.
route = route.HandleMW(apiKeyAuth, func(ctx context.Context, req *Req, in APIKeyCredential) (struct{}, error) {
    // fn can read/validate against req's ALREADY-DECODED fields here —
    // e.g. cross-checking the credential against a tenant ID the
    // route's own body already decoded — the SAME capability Transform
    // provided, now via HandleMW directly.
    return struct{}{}, validateAgainstTenant(req.TenantID, in)
})
```

### This applies to all 3 packages, not just REST — same principle, different method names

Written above in REST's terms (`HandleMW`/`ClientMW`/`Transform`/
`ClientTransform`) for concreteness, but confirmed via code: `api/events`
and `api/reqreply` have the IDENTICAL `Transform`/`ClientTransform` free
functions (`api/events/transform.go`, `api/reqreply/transform.go`), for
the EXACT SAME Go-generics reason. The SAME fold-via-reflection applies
analogously, just onto each package's OWN existing attachment methods:
`api/events.Subscriber.SubscribeMW`/`Publisher.PublishMW` (confirmed via
`api/events/builder.go` — events uses this naming, not `HandleMW`), and
`api/reqreply.Route.HandleMW`/`ClientMW` (confirmed identical naming to
REST). Carried out in EACH package's OWN Rollout Phase (B for events, C
for reqreply) — see "Implementation rollout" below, updated accordingly
— mirroring Rollout Phase A's approach, not a REST-only change.

### Scope confirmation

This is a DESIGN-LEVEL decision this round — no code has been changed.
See "Confirmed mechanical details" above for the exact Go-level bridging
mechanism (a dedicated design-review pass found and resolved 4 concrete
gaps before implementation began), "Implementation rollout"'s Rollout
Phase A/B/C bullets (updated below) for where this lands in the per-API
rollout sequencing, and "Files to create/modify" (updated below) for the
concrete file-level consequences.


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
// then derives each axis's wire-value map via codex.EncodeMergeVars (NOT
// codex.EncodeVars — see "Phase 1 sub-item" above for why these are
// deliberately separate functions), so every merge-field axis inherits
// "omit if unset" support automatically.
func EncodeLayer[T any](
    name string,
    codec codex.Codec[T],
    newOutputErr func(name string, err error) error,
    value T,
    axes ...Axis[T],
) ([]map[string]string, error) // one map per axis, same order as axes
```

> **Implementation note (Phase B model-review round) — the ABOVE sketch
> is illustrative only; the ACTUAL shipped `middleware/layer.go`
> (Rollout Phase A) is simpler and SIGNATURE-DIFFERENT, confirmed via
> code. Phase B must call the REAL functions, not this sketch:**
> ```go
> type Axis[T any] struct { Fields []codex.FieldCodec[T]; Vars map[string]string }
> func DecodeLayer[T any](axes []Axis[T], wrapErr func(err error) error) (T, error)
> func EncodeLayer[T any](v T, axisFields [][]codex.FieldCodec[T], wrapErr func(err error) error) ([]map[string]string, error)
> ```
> No `name`/`codec` parameters exist on either function — codec
> validation (`mw.InCodec.Validate(in)`/`mw.OutCodec.Validate(out)`)
> happens OUTSIDE `DecodeLayer`/`EncodeLayer`, inside each package's OWN
> `buildDecodeIn`/`buildEncodeOut` (confirmed via
> `api/rest/transform.go`) — `DecodeLayer`/`EncodeLayer` themselves are
> PURELY the axis-iteration mechanism, nothing else. `Axis`/`AxisVars`
> were also merged into ONE struct (`Axis[T]{Fields, Vars}`), not kept
> as two separate types as sketched above.

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

### Phase 1 sub-item: a NEW, separate `codex.EncodeMergeVars` function — NOT an extension of `EncodeVars` (corrected this round)

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

**CORRECTED this round — "extend `EncodeVars`" was WRONG, confirmed by
actually attempting it during Phase A's implementation.** Extending
`EncodeVars` itself to check `sparseFieldCodec[T]` breaks 2 EXISTING,
deliberate tests (`codex/maybe_test.go`'s
`TestMaybeField_EncodeVarsIgnoresSparseRule`,
`codex/omitempty_test.go`'s
`TestOmitEmptyField_EncodeVarsIgnoresSparseRule`) and contradicts
`docs/concepts/codec.md`'s own documented rationale ("Interaction with
`Template`/`DottedKeyCodec`/`DecodeVars`/`EncodeVars`" section):
`EncodeVars` is ALSO used for PATH/TOPIC/DOTTED-KEY building (every
`SinkAdapter`/`IOAdapter`/`SourceAdapter` constructor across
`adapters/file`/`redis`/`mqtt`/`mqtt5`/`zeromq`, plus
`DottedKeyCodec`/`Template`) — where a silently-omitted field would
CORRUPT the built URI/topic/key structure. This is a deliberate,
pre-existing safety guarantee this doc's original resolution would have
broken.

**Corrected resolution**: add a NEW, separate, exported function —
`codex.EncodeMergeVars[T](v T, fields ...FieldCodec[T]) (map[string]string, error)`
— identical to `EncodeVars` EXCEPT it honors `sparseFieldCodec[T]`,
mirroring `Struct`'s own Encode loop exactly. `EncodeVars` itself stays
COMPLETELY UNCHANGED, preserving its path/topic/dotted-key safety
guarantee. `EncodeMergeVars` MUST live inside package `codex` (not
`middleware`) since `sparseFieldCodec[T]` is unexported — confirmed via
grep, no cross-package access possible. Once `middleware.EncodeLayer`
is built on top of `EncodeMergeVars` (Phase 1's own main item), every
merge-field axis in all 3 packages inherits "omit if unset" support for
free, including Security's own credential merge fields (Phase 2) — e.g.
an optional credential sub-field that should be omitted from the wire
entirely when the `.WithSend` Fn didn't set it. Path/topic template
building (an entirely separate, pre-existing mechanism) continues to
use `EncodeVars`, untouched.

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
**RESOLVED (Phase B model-review round): fail-fast-across-layers,
confirmed as what Phase A actually shipped — not left undecided.**
`rest.DispatchMiddlewareHandlers` returns immediately on the first
STACKED layer's `DecodeIn`/Fn error, exactly as described above. **One
additional confirmed level this round, not previously stated**:
`middleware.DecodeLayer`'s real implementation (`middleware/layer.go`)
is ALSO fail-fast ACROSS AXES WITHIN one layer (header fails → cookie/
query never attempted) — a third, finer-grained level the original
framing above didn't address (it only contrasted "layers across a
stack" vs. "fields within one axis's own `DecodeVars` call," leaving
"axes within one layer" unstated). All 3 levels are now confirmed:
fields-within-an-axis ACCUMULATE (`ValidationErrors`, unchanged);
axes-within-a-layer and layers-within-a-stack are BOTH fail-fast. Not a
remaining design choice — **directly actionable for Phase B**:
`events.DispatchSubscribeMiddlewareHandlers`/
`DispatchPublishMiddlewareHandlers` must match this SAME confirmed
fail-fast behavior at every level for consistency, not re-litigate the
choice during implementation.

Note: literally reusing `forge.Function[In,Out]` as the middleware step
type itself was explicitly considered and rejected — it carries a
SHA-256 contract hash and governance metadata (author/approver/approval
date) built for KPI-computation governance, irrelevant ceremony for an
ordinary middleware layer. The two mechanisms share PHILOSOPHY (named,
typed, composed, short-circuiting steps), not code worth merging.

## The pivot, concretely — all three packages

**Note — unaffected by the "Architecture revision" section above**: the
examples below all use the channel/route-AGNOSTIC style (`.Use(mw.WithSend(...))`),
which the Architecture revision explicitly preserves unchanged. These
examples remain valid as written. For a Security-shaped middleware that
instead needs `*Req` access (e.g. cross-checking a credential against an
already-decoded body field), see the Architecture revision section's own
worked example — attached via `HandleMW` directly, no separate
`Transform` call.

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

### Worked example: migrating the REAL motivating case (`newAuthCredentialFunc`)

The illustrative example above is a simplified sketch. This doc's own
"Files to create/modify" table has always named the ACTUAL motivating
real case: `examples/go-edge-models/app/registry/auth.go`'s
`newAuthCredentialFunc` — re-evaluated here, end to end, against the
current design (confirmed fully migratable, modulo the ONE new
constructor above).

**Today:**
```go
return func(ctx context.Context, _ []route.SecurityRequirement) (http.Header, error) {
    once.Do(func() {
        token, authErr = authenticate(ctx, httpClient, registryHost, repository, creds, o.observer)
    })
    if authErr != nil {
        return nil, authErr
    }
    if token == "" {
        return nil, nil // registry requires no auth for this request.
    }
    h := make(http.Header, 1)
    h.Set("Authorization", formatBearerToken(token)) // hand-built
    return h, nil
}
```

**Proposed — identical runtime behavior, declared instead of hand-built:**
```go
type BearerCredential struct{ Token string }

// internal.BearerTokenCodec (ALREADY EXISTS, confirmed —
// examples/go-edge-models/internal/registry/auth.go) already formats a
// plain token string into "Bearer <token>" — reused UNCHANGED as the
// header's own value-codec, no new formatting logic needed.
var BearerAuthDeclaration = rest.SecurityMiddleware[BearerCredential, struct{}](
    "bearerAuth", route.BearerScheme("JWT"), nil,
).WithRequestHeader(rest.NewOmitEmptyHeaderParam("Authorization", internal.BearerTokenCodec,
    func(c BearerCredential) string { return c.Token },
    func(c *BearerCredential, v string) { c.Token = v },
    func(token string) bool { return token == "" }, // omit entirely when empty — matches today's `return nil, nil`
))

func newAuthCredentialFunc(httpClient *http.Client, registryHost, repository string, opts ...Option) /* ... */ {
    // ... o := resolveOptions(opts); creds := ...; identical setup ...
    var (
        once    sync.Once
        token   string
        authErr error
    )
    route = route.Use(BearerAuthDeclaration.WithSend(func(ctx context.Context) (BearerCredential, error) {
        once.Do(func() {
            token, authErr = authenticate(ctx, httpClient, registryHost, repository, creds, o.observer)
        })
        if authErr != nil {
            return BearerCredential{}, authErr
        }
        return BearerCredential{Token: token}, nil // "" → header omitted entirely, via NewOmitEmptyHeaderParam
    }))
}
```

**Confirmed unchanged**: `sync.Once` memoization, the closed-over
`token`/`authErr` state, and the lazy-on-first-call semantics are
ORDINARY Go closure internals — entirely orthogonal to the attachment
mechanism, migrated verbatim. The attachment itself moves from
`.ClientMW(&BearerAuthDeclaration, authFn)` to
`.Use(BearerAuthDeclaration.WithSend(authFn))` — the AGNOSTIC style,
confirmed compatible since this Fn never needed `*Req` access (it only
closes over `httpClient`/`registryHost`/`repository`/`creds`/`o.observer`,
exactly like today). The ONLY genuinely new piece is
`NewOmitEmptyHeaderParam` (declared above) — without it, an empty token
would encode as a present-but-empty `Authorization:` header instead of
omitting it entirely, a real behavior regression from today's anonymous-
access path.

## Cross-protocol credential composition — a confirmed, already-supported capability

A natural question this design raises: can a middleware declared for an
`api/events` (or future `api/mcp`/`ports`) boundary use a COMPLETELY
DIFFERENT protocol internally to authenticate — e.g. an MQTT channel's
Security Fn performing an OAuth2 client-credentials flow over REST/HTTP
before publishing? **Yes, confirmed — and not a new capability this doc
introduces, but a direct, structural consequence of a design choice
already in place today.**

**Already-shipped proof, not a hypothesis**:
`examples/go-edge-models/app/registry/auth.go`'s `authenticate(ctx,
httpClient, registryHost, repository, creds, obs)` ALREADY makes a real
REST/HTTP call (`rest.CallWithTransport(ctx, pingTransport, pingHandle,
struct{}{}, ...)` against a `PingRoute`) INSIDE a function called from
`newAuthCredentialFunc` — the Security credential Fn attached to
`GetTagsRoute`'s REST route via `.ClientMW(...)`. This is a
same-protocol (REST authenticating REST) instance of the SAME principle
that generalizes to cross-protocol use directly.

**Why this generalizes with ZERO new go-codex mechanism needed**: the
Security Fn — under BOTH the current mechanism AND this doc's own
redesign — is, and always has been, an ORDINARY GO CLOSURE
(`func(ctx context.Context) (Cred, error)` under `.WithSend`). go-codex's
middleware mechanism ONLY governs the DECLARATIVE SURFACE (scheme name,
scopes, and which wire location the resulting credential maps to via
merge fields) — it places ZERO constraints on the Fn's OWN internal
implementation. The Fn body can call any Go code, including
`rest.CallWithTransport` against a completely unrelated REST OAuth2
token endpoint, even though the CHANNEL being secured is MQTT/ZeroMQ.
This isn't a special case or an escape hatch — it is the direct,
structural consequence of the Fn being "just a closure," true in every
version of this mechanism.

**Illustrative sketch** (events channel authenticating via a REST OAuth2
token endpoint — NOT shipped code, a pattern sketch):

```go
// A REST route for an OAuth2 client-credentials token endpoint —
// completely unrelated to the events channel this will secure.
var tokenTransport = nethttp.NewClientTransport(nethttp.ClientTransportOptions{
    HTTPClient: httpClient, BaseURL: oauthProviderBaseURL,
})

// The events channel's credential Fn — an ordinary closure embedding a
// REST call, mirroring auth.go's authenticate() pattern exactly,
// including its sync.Once-based token caching (avoiding re-authenticating
// on every publish).
var once sync.Once
var cachedToken string
var tokenErr error

credFn := func(ctx context.Context) (BearerCredential, error) {
    once.Do(func() {
        resp, err := rest.CallWithTransport(ctx, tokenTransport, tokenHandle,
            TokenReq{GrantType: "client_credentials", ClientID: id, ClientSecret: secret})
        if err != nil {
            tokenErr = err
            return
        }
        cachedToken = resp.(TokenResp).AccessToken
    })
    if tokenErr != nil {
        return BearerCredential{}, tokenErr
    }
    return BearerCredential{Token: cachedToken}, nil
}

channel.Use(events.SecurityMiddleware[BearerCredential, struct{}](
    "oauth2", route.OAuth2Scheme(flows), scopes,
).WithPublishProperty(events.NewRequiredPropertyParam("Authorization", bearerPropertyCodec,
    func(c BearerCredential) string { return c.Token },
    func(c *BearerCredential, v string) { c.Token = v },
)).WithSend(credFn))
```

**MCP/ports — confirmed the same principle applies**:
`docs/roadmap/mcp-ports-declarative-middleware.md` already designs
`ports.RequireScopes[T]` to reuse the ALREADY-SHARED
`middleware.SecurityScheme`/`CheckScopes` directly — the SAME
Fn-is-a-plain-closure principle applies there: a `ports.File`
middleware's credential Fn could embed a REST/HTTP OAuth2 call before
permitting file access, for the identical structural reason. No special
plumbing is needed in that roadmap doc's design either, for the same
reason documented here.

## `api/events` middleware deep-dive: Observer + connection-level auth + message-level (cross-protocol) auth + context propagation

The user asked for all of this tied together specifically for
`api/events`: an Observer middleware, a pub/sub client's authentication
(both broker-native connection-level AND protocol-independent
message-level), and the context-propagation mechanism from the Phase 3
section above. Confirmed via code — these are not 2 or 3 competing
options, they are 3 DIFFERENT LAYERS of "secured, observed channel," and
(with one confirmed gap, below) they compose.

### Three layers, confirmed via code — and a real asymmetry between them

| Layer | Mechanism | Spec-rendered? | Protocol-independent? |
|---|---|---|---|
| **Observer** | `events.Observability[T]` — a general-purpose `func(next) func(ctx,T) error` closure, attached unpaired via `sub.SubscribeMW(nil, events.Observability[T](obs))`/`pub.PublishMW(nil, ...)`. No `In`/`Out` at all — confirmed, matches this doc's own earlier "Observer pattern ... has no IN/OUT structs, a special case" framing. | No — purely a runtime hook. | Yes — identical shape regardless of adapter. |
| **Connection-level auth** | `mqtt5.Connect(ctx, brokerURL, ConnectOptions{Username, Password})` — performs the REAL broker CONNECT handshake (confirmed via `adapters/mqtt5/connect.go`); `mqtt5.ConnectSecurityScheme`/`NewSecuredClient` is a SEPARATE, OPTIONAL format-only pre-check layered on top of an already-connected client, not the auth mechanism itself. | **Partially, and NOT linked to the runtime check** — see the confirmed gap below (closed by Phase 4). | **No — inherently protocol-native by definition** (CONNECT-time credentials are tied to the wire protocol's own handshake; MQTT's CONNECT packet fields have no cross-protocol equivalent to embed an unrelated OAuth2 call INTO). |
| **Message-level auth** | `Middleware[In,Out]` + `SecurityDeclaration()` + a credential `Fn` — the subject of this doc's Phase 2. | Yes, fully — one declaration drives both the AsyncAPI `security` list AND the runtime Fn. | **Yes, confirmed** — the Fn is an ordinary Go closure; see "Cross-protocol credential composition" above (the `auth.go` proof). |

**Correcting this doc's own earlier framing**: the user is right that
connection-level auth IS a legitimate middleware/capability concern under
this project's own design guardrails ("a capability is declared in the
API layer; an adapter satisfies it at attach") — it was wrong to treat it
as purely out-of-scope/adapter-only. Confirmed via code exactly where it
currently falls short of that guardrail:

### Phase 4: closing the connection-level auth gap — a real design, for `api/events` AND `api/reqreply`

Message-level Security (`Middleware[In,Out]` + `SecurityDeclaration()`)
already unifies spec and runtime behind ONE declaration — attach the
value once, both the AsyncAPI `security` requirement AND the runtime Fn
dispatch come from the SAME source. Connection-level auth did NOT have
this property — confirmed via code, and sharper than first thought:

- The SPEC side is `asyncapi/v3.Server.Security []route.SecurityRequirement`
  — a real, rendered field on the AsyncAPI Server object (confirmed:
  `render/asyncapi/v3/document.go`'s `Server` struct), populated via
  `events.Client.AddServer(name, events.Server{..., Security: [...]})` /
  `reqreply.Builder.AddServer(name, reqreply.ServerEntry{...})` at the API
  layer.
- The RUNTIME side is `mqtt5.Connect` (the function that actually dials
  and performs the CONNECT handshake — see "Reframed explicitly as a
  D-0006-pattern capability" below) and/or `mqtt5.ConnectSecurityScheme`/
  `NewSecuredClient` (a separate, optional, format-only pre-check layered
  on an already-connected client) — entirely separate code, living in
  the adapter package, invoked by the caller before passing the result
  to Subscribe/Publish/Serve/Call.
- **Sharper root cause, confirmed via code**: `events.Client.AsyncAPISpec()`'s
  own comment states *"there is no builder-level registry"* for security
  schemes — `components/securitySchemes` is populated ONLY by
  aggregating every CHANNEL's own `WithSecurityScheme` declarations
  (`reqreply.Builder.AsyncAPISpec()` has the IDENTICAL limitation, same
  wording, aggregating from ROUTES instead). **A connection-only scheme —
  referenced solely via `Server.Security`, used by NO individual
  channel/route — cannot be spec-registered at all today.** This is
  worse than "unlinked" — it's "spec-unrepresentable without an
  unrelated channel/route declaring the same scheme as a workaround."

**Confirmed this applies equally to `api/reqreply`, not just
`api/events`** (the motivating observation for this round): an
mqtt5-backed reqreply `Call`/`Serve` requires the identical
connect-then-authenticate sequence as events' Subscribe/Publish.
Confirmed via code: `adapters/mqtt5/reqreply_transport.go`'s `Call`/`Serve`
take a plain `MQTTClient` parameter — the EXACT SAME interface
`adapters/mqtt5`'s Subscribe/Publish already take — meaning the plain
`MQTTClient` `mqtt5.Connect` returns is ALREADY a drop-in for reqreply
too, with **zero adapter code change needed** (and so is
`*mqtt5.SecuredClient`, the OPTIONAL secondary pre-check wrapper, since
it promotes every `MQTTClient` method transparently via struct
embedding). Confirmed via `adapters/zeromq`: no connection-level
security construct exists there at all — this remains an mqtt5-only
concern, consistent with CONNECT-time credentials being inherently
protocol-native (D-0006's own conclusion, cross-referenced above).

#### The fix — one new, small, additive method per package; zero adapter changes

```go
// package events
//
// AddConnectSecurityScheme registers name/scheme directly into
// components/securitySchemes, independent of any channel's own
// WithSecurityScheme declaration — for a scheme used ONLY via a
// Server's connection-level Security list (see AddServer), never
// referenced by any individual channel's own Subscribe/Publish
// requirements. Collision policy matches every other registration on
// Client: last-registered-wins (no error returned), consistent with
// [Client.AddSchema]/[Client.AddServer].
//
// Reuse the SAME scheme's NAME, unchanged, when later supplying real
// credentials via the adapter's own attach-time mechanism — e.g.
// mqtt5.Connect(ctx, brokerURL, mqtt5.ConnectOptions{Username, Password})
// — closing the spec/runtime link via one reused declared value, not a
// new shared mechanism. See "Reframed explicitly as a D-0006-pattern
// capability" below for why this split (decoupled declare + sealed
// attach-time supply, no handler stage in between) is the right shape.
func (c *Client) AddConnectSecurityScheme(name string, scheme route.SecurityScheme) *Client
```

```go
// package reqreply — byte-identical shape and doc comment, same
// collision policy as [Builder.AddSchema]/[Builder.AddServer].
func (b *Builder) AddConnectSecurityScheme(name string, scheme route.SecurityScheme) *Builder
```

Both merge into the SAME aggregation each package's `AsyncAPISpec()`
already performs for channel/route-registered schemes — registered
BEFORE the channel/route loop runs, so a channel/route re-registering
the identical name still wins on collision (unchanged last-registered-
wins policy, now simply has a 3rd contributor instead of 2).

**No new error type** — mirrors `AddSchema`/`AddServer`'s existing
no-error-return, silently-overwrite-on-collision convention; introducing
one here would be inconsistent with every sibling registration method on
the same type.

#### Reframed explicitly as a D-0006-pattern capability — declare/attach split, no handler stage

Re-reading [D-0006](../design/d-0006-protocol-native-capabilities.md) §3's
own, already-RESOLVED 4-stage lifecycle model — (1) **declare**
(route/channel/port, adapter-agnostic), (2) **capability-declare**
(spec-contributing, still adapter-agnostic), (3) **handler-attach**
(business logic), (4) **adapter-attach** (concrete adapter supplied,
checked against stage 2) — and its chosen "Candidate 3" design (a
DECOUPLED, spec-only stage-2 sibling value + the ALREADY-PROVEN, sealed,
compile-time-checked stage-4 `Attach`-time supply, linked only by an
OPTIONAL, opt-in drift-check, never a compiler guarantee) — connection-
level auth is confirmed to fit this EXACT pattern, one granularity level
up from QoS/User Properties (which operate at the per-channel-operation
level via `SubscribeOptions`/`PublishOptions.Capabilities`):

- **Stage 2 (declare, adapter-agnostic)** = `AddConnectSecurityScheme`
  (unchanged from above) — declared near "new builder/new broker"
  construction, exactly where a connection-level concern belongs
  conceptually, with NO link yet to any concrete adapter.
- **Stage 4 (adapter-attach)** = `mqtt5.Connect`'s `ConnectOptions{
  Username, Password, ...}` — confirmed ALREADY EXISTS
  (`adapters/mqtt5/connect.go`) and ALREADY IS exactly "hand over
  credentials as attach-time options for the adapter." `ConnectOptions`
  is sealed to package `mqtt5` by ordinary Go function-signature
  scoping — there is no shared `[]Capability`-style slot here for a
  wrong adapter's value to be mistakenly accepted into, so (unlike
  QoS/Retained, which share ONE `SubscribeOptions.Capabilities []Capability`
  slot across potentially many capability kinds) a sealed marker-method
  interface would add ceremony with no corresponding safety gain. The
  safety D-0006's `Capability` interface buys via `isMQTT5Capability()`
  is achieved here for free, simply because `mqtt5.Connect` is a
  concrete, non-generic function living in one package.
- **There is NO stage 3 (handler-attach) for this capability — confirmed,
  not a gap.** This directly answers "how would the handler of a
  connection-auth middleware work before any adapter is attached": it
  wouldn't, because there is nothing for it to do. `Capability.Apply`
  already establishes the precedent that some capabilities have NO
  separate Fn/handler stage — the adapter-level action itself (`Apply`
  for QoS/Retained; `Connect` here) **is** the realization. A
  `Middleware[In,Out]`-shaped "handler" cannot exist for connection-level
  auth, for the same reason stated in the prior round (no per-message
  `In`/`Out`, no cross-protocol angle) — now given a principled citation
  instead of an ad-hoc observation: stages 3 and 4 simply collapse into
  one for any capability whose entire realization IS an adapter-level
  action with nothing upstream of it to enrich or validate.

#### Worked example — the full 2-stage flow, in order, both packages

```go
// STAGE 2 — declare, near client/builder construction. Adapter-agnostic;
// no concrete adapter exists yet at this point.
var brokerAuth = route.SecurityScheme{Type: route.SecuritySchemeHTTP, Scheme: "basic"}
eventsClient.AddConnectSecurityScheme("brokerAuth", brokerAuth)
reqreplyBuilder.AddConnectSecurityScheme("brokerAuth", brokerAuth)

eventsClient.AddServer("mqtt5", events.Server{
    URL: "mqtts://broker:8883", Protocol: "mqtt5",
    Security: []route.SecurityRequirement{route.Require("brokerAuth")},
})
reqreplyBuilder.AddServer("mqtt5", reqreply.ServerEntry{
    URL: "mqtts://broker:8883", Protocol: "mqtt5",
    Security: []route.SecurityRequirement{route.Require("brokerAuth")},
})

// ... later, at ATTACH time — STAGE 4, entirely adapter-owned. This is
// where real credentials are handed to the adapter, and where a real
// broker-rejection error surfaces, BEFORE Client.Attach is ever reached.
client, router, err := mqtt5.Connect(ctx, "broker:8883", mqtt5.ConnectOptions{
    ClientID: "svc-1", Username: username, Password: password,
})
if err != nil {
    var connErr mqtt5.ConnectError
    if errors.As(err, &connErr) {
        // handle dial failure vs. broker-rejected credentials here
    }
    return err
}
transport := mqtt5.NewTransport(mqtt5.TransportOptions{Client: client, Router: router})

// Client.Attach itself stays entirely protocol-agnostic — it never sees
// Username/Password, only the already-connected Transport value.
if err := eventsClient.Attach(transport); err != nil { /* handle */ }
```

**Optional, opt-in drift-check** (NOT designed further here, flagged by
analogy only): mirroring `CheckCapabilityCoverage`'s own precedented,
non-mandatory shape, a future helper COULD confirm the scheme NAME
declared via `AddConnectSecurityScheme` ("brokerAuth" above) matches the
name a caller intends when supplying `mqtt5.Connect`'s credentials — but
this is explicitly an OPT-IN safety net a caller could still forget to
call, not a compiler-enforced link, exactly the same honest trade-off
D-0006 itself accepts for `Capability`/`CheckCapabilityCoverage`. The
reused-Go-value convention shown above (the SAME `"brokerAuth"` string
literal at both call sites) is the PRIMARY safety mechanism today; the
drift-check would only catch a caller who let the two literals drift
apart, a secondary concern.

#### Two confirmed, real gaps in `mqtt5.Connect`'s error propagation — found via code, not assumed

1. **CONNACK reason code is silently discarded today.** Confirmed via
   `paho.golang`'s own `Client.Connect`: on an auth rejection (CONNACK
   reason code ≥ 0x80, e.g. `0x86` "Bad username or password", `0x87`
   "Not authorized"), paho returns BOTH a non-nil `*paho.Connack`
   (carrying `.ReasonCode`/`.Properties.ReasonString`) AND a generic
   `fmt.Errorf("failed to connect to server: %s", reason)`.
   `mqtt5.Connect`'s current body discards the `*Connack` return entirely
   (`if _, err := client.Connect(...); err != nil`) — the STRUCTURED
   reason is lost, only a generic string survives inside `ConnectError.Err`.
   **Proposed fix**: extend `ConnectError` with optional `ReasonCode byte`/
   `ReasonString string` fields, populated from the `*Connack` paho
   already returns (today discarded) whenever `Op == "connect"` and a
   non-nil `Connack` was received — zero-value for a "dial"-stage
   failure, where no CONNACK was ever received. `errors.As` callers can
   then branch on `ReasonCode` directly, without parsing the generic
   error string — this is the concrete mechanism that makes "propagating
   back connection errors" actionable, not just visible.
2. **No Observer integration.** `ConnectOptions` has no `Observer` field
   at all — unlike `NewSecuredClient`'s existing `WithObserver`/
   `stats.SecurityObserver.RecordSecurityRejection` pattern. **Proposed
   fix**: add an optional `Observer` field to `ConnectOptions`; on an
   auth rejection (reason code in the "not authorized"/"bad username or
   password" range), call `stats.SecurityObserver.RecordSecurityRejection(
   "connect", ...)` — mirroring `NewSecuredClient`'s already-shipped
   pattern, satisfying this project's own observer-integration
   requirement for any new/extended mechanism.

**Confirmed reqreply parity, unchanged**: `mqtt5.Connect` is adapter-level
(package `mqtt5`, not events- or reqreply-specific) — already directly
reusable by `api/reqreply` with zero code change, since it returns a
plain `MQTTClient`/`MQTTRouter` pair, consumed identically by either
package's own `Attach`/`NewTransport` constructor (confirmed via the
worked example above, which attaches to both).

**Housekeeping note (not a new design question)**: `connect_security.go`'s
doc comment "go-codex NEVER calls Connect() itself" is misleading at the
PACKAGE level — true only for `NewSecuredClient`'s own mechanism (which
wraps an ALREADY-connected client for format-only pre-validation), false
for its sibling `mqtt5.Connect` (which performs the real handshake). Flag
for correction when Phase 4 is actually implemented.


### Combining Phase 2 (message-level OAuth2/scopes) and Phase 4 (connection-level auth) — a full worked AsyncAPI rendering, and what the broker does NOT know about

A natural follow-up question: declaring BOTH a connection-auth scheme
(Phase 4) AND a message-layer OAuth2-with-scopes authorization middleware
(Phase 2) on the SAME channel/server — does this render correctly into
AsyncAPI, is it even a sound design, and what exactly is being
compensated for, given plain MQTT brokers have no native
scopes/authorization concept at all? **Confirmed via code: yes, yes, and
precisely this — the broker enforces CONNECT-time credentials only; ALL
scope/authorization enforcement happens in go-codex's own process, never
touching the broker.**

#### Worked example — one of each, same channel

```go
// Phase 4 — connection-level: the broker's OWN CONNECT-time credential.
// A plain MQTT broker understands THIS (username/password), nothing more.
var brokerAuth = route.SecurityScheme{Type: route.SecuritySchemeHTTP, Scheme: "basic"}
eventsClient.AddConnectSecurityScheme("brokerAuth", brokerAuth)
eventsClient.AddServer("mqtt5", events.Server{
    URL: "mqtts://broker:8883", Protocol: "mqtt5",
    Security: []route.SecurityRequirement{route.Require("brokerAuth")},
})
client, router, _ := mqtt5.Connect(ctx, "broker:8883", mqtt5.ConnectOptions{
    Username: username, Password: password,
})
transport := mqtt5.NewTransport(mqtt5.TransportOptions{Client: client, Router: router})
eventsClient.Attach(transport)

// Phase 2 — message-level: an OAuth2 scheme the BROKER has never heard
// of. Declared via the SAME route.OAuth2Scheme(...) constructor REST/
// reqreply already use — nothing events-specific about the scheme type.
var oauth2 = route.OAuth2Scheme(route.OAuthFlows{
    ClientCredentials: &route.OAuthFlow{
        TokenURL: "https://auth.example.com/token",
        Scopes:   map[string]string{"subscribe:sensors": "read sensor data"},
    },
})
bearerAuth := events.NewMiddleware(middleware.Declaration[OAuthCred, struct{}]{
    Name: "oauth2",
    Security: &middleware.SecurityDeclaration{Scheme: oauth2},
}).WithReceive(validateTokenAndCheckScopes)

sub := sensorChannel.WithSubscribe(events.Subscribe{
    Security: []route.SecurityRequirement{route.Require("oauth2", "subscribe:sensors")},
})
sub.Use(bearerAuth)
```

#### Resulting AsyncAPI document (sketch) — both schemes coexist, zero collision

```yaml
components:
  securitySchemes:
    brokerAuth: { type: http, scheme: basic }
    oauth2:
      type: oauth2
      flows:
        clientCredentials:
          tokenUrl: https://auth.example.com/token
          scopes: { "subscribe:sensors": "read sensor data" }
servers:
  mqtt5:
    url: mqtts://broker:8883
    protocol: mqtt5
    security: [{ brokerAuth: [] }]          # connection-level, Phase 4
channels:
  sensors/{sensorID}/data:
    subscribe:
      security: [{ oauth2: ["subscribe:sensors"] }]   # message-level, Phase 2
```

Confirmed via code: `components/securitySchemes` is just a `name →
scheme` map (`buildSecuritySchemes`) — `brokerAuth` and `oauth2` are two
unrelated entries, registered by two unrelated call sites
(`AddConnectSecurityScheme` vs. the channel's own aggregated
`WithSecurityScheme`/`SecurityDeclaration`), with zero shared code path.
`Server.Security` (rendered by `document.go`'s server-building loop) and
`channels.<x>.subscribe.security` (rendered by the SAME file's
operation-building loop) are two INDEPENDENT nesting points of the same
document — standard AsyncAPI v3 modeling: connection-level and
operation-level security are separate, both-apply constraints, not an
either/or choice a renderer needs to reconcile.

#### Who enforces what, and where — the actual compensation

| | Phase 4 (`brokerAuth`) | Phase 2 (`oauth2` + scopes) |
|---|---|---|
| What the SPEC says | A client must authenticate to connect to this server | A client must hold a token granting `subscribe:sensors` to use this operation |
| Who ENFORCES it | **The MQTT broker itself** — via its own ACL/auth config, checking the CONNECT packet's username/password. go-codex's `NewSecuredClient` only pre-validates the FORMAT before sending; the broker makes the real accept/reject decision. | **go-codex's own dispatch, in the subscribing/publishing process** — `Middleware[In,Out]`'s Fn validates the token, `CheckScopes` compares granted vs. required scopes. The broker is not involved at all — it has already delivered (or would deliver) the message regardless of scope outcome; go-codex's own handler simply never runs the business logic if `CheckScopes` fails. |
| Does the broker understand this constraint? | Yes — this is exactly what MQTT's CONNECT packet models. | **No — confirmed, by protocol design.** MQTT (v3 and v5) has no scope/authorization concept at the wire level at all. A "basic" MQTT broker that merely implements the protocol spec literally cannot represent, let alone enforce, "`subscribe:sensors` scope required" — it only ever sees "this client connected with these credentials, then subscribed to this topic string." |

**This is the precise compensation the user described**: Phase 2 exists
*because* the broker can't do this — go-codex's `Middleware[In,Out]` +
`CheckScopes` mechanism is an APPLICATION-layer authorization layer
bolted on top of a protocol that has none, enforced entirely in the
subscribing/publishing process, never delegated to (or verifiable by)
the broker. The AsyncAPI document's `channels.*.subscribe.security`
entry is therefore **descriptive of go-codex's own enforcement**, not a
broker capability being documented — an important distinction for a
spec reader: `Server.Security` describes a BROKER-ENFORCED guarantee;
`Operation.Security` (for an MQTT-backed channel) describes an
APPLICATION-ENFORCED one. Both are equally real, equally worth
documenting, and AsyncAPI already has the vocabulary for both — but a
reader auditing "is this actually secure" needs to know WHICH enforcement
point applies to which entry, since only one of them is the broker's
problem.

**No new mechanism needed to combine them** — declaring one of each is
sufficient; Phase 2 and Phase 4 were already confirmed structurally
independent (prior rounds), and this round confirms their SPEC OUTPUTS
compose cleanly too, with no renderer-level conflict.


### Correcting the context-propagation tie-in: `SetContextFieldFromIn` on subscribe has NOTHING to do with publish

A separate point of confusion in this round's earlier draft: describing
`SetContextFieldFromIn` (née `PublishFieldIn` — see the renamed "Open
design decisions for Phase 3" item above) as somehow involving the
publish side on a SUBSCRIBE channel. **This was wrong, and the
`PublishField*` naming itself was the direct cause** — renamed in this
same round to `SetContextFieldFromIn`/`SetContextFieldFromOut` precisely
because the old name's "Publish" prefix reads, in an `api/events`
context, as "the publish side of this channel" — it never meant that.

To be fully explicit, for the SUBSCRIBE path specifically:

- A Security `Middleware[In,Out]` attached to a channel's SUBSCRIBE side
  decodes `In` from the incoming message's topic/property vars (e.g. a
  bearer token) — confirmed, `WithReceive`'s shape is `func(ctx, In)
  error`, no `Out` at all (see the signature table earlier in this doc).
- `SetContextFieldFromIn(field, get func(In) any)` dispatches
  IMMEDIATELY after `DecodeIn` succeeds, BEFORE the Fn even runs — it
  reads a value OUT OF `In` (e.g. a `UserID` the credential's OWN field
  codec already derived from the raw token, ordinary
  `codex.Struct`/`Refine` composition, ADDRESSED in the Phase 3 section
  above) and writes it into the `ContextField`.
- The subscribe HANDLER then reads it back via `field.Get(ctx)` — fully
  typed, available by the time the handler runs.
- **The PUBLISH side of this (or any other) channel is not involved at
  any point in this flow.** `SetContextFieldFromOut` is the SEPARATE
  method for the produced-value case (events' PUBLISH side, where
  `WithSend` DOES produce an `Out`) — it simply does not apply to
  subscribe at all, by the structural asymmetry already documented in
  the Phase 3 section (events' `WithReceive` has no `Out` to source a
  value from).

### Worked, corrected sketch — all 3 layers on one subscribe channel, zero publish-side involvement

```go
// Layer 1 — connection-level (broker) auth: connect once, before any
// Subscribe call, using the SAME scheme name registered via
// AddConnectSecurityScheme (see Phase 4 above for the full declare/
// attach-time design) — closing what was, in an earlier round of this
// doc, an unlinked, by-hand-only convention.
client, router, err := mqtt5.Connect(ctx, brokerURL, mqtt5.ConnectOptions{
    Username: username, Password: password,
})
transport := mqtt5.NewTransport(mqtt5.TransportOptions{Client: client, Router: router})

// Layer 2 — Observer: attached unpaired, wraps every dispatch.
sub := sensorChannel.WithSubscribe(events.Subscribe{})
sub.SubscribeMW(nil, events.Observability[SensorReading](obs))

// Layer 3 — message-level, protocol-independent auth (Phase 2 shape):
// the credential Fn embeds an OAuth2 token validation over REST,
// independent of this channel's own mqtt5 transport (see "Cross-protocol
// credential composition" above) — and its In carries a codec-derived
// UserID alongside the raw token.
userIDField := middleware.NewContextField[string]("events.userID", userIDCodec)

bearerAuth := events.NewMiddleware(middleware.Declaration[BearerCred, struct{}]{
    Name: "bearerAuth", Security: &middleware.SecurityDeclaration{...},
}).WithReceive(func(ctx context.Context, in BearerCred) error {
    // in.UserID already derived at the CODEC level from in.Token —
    // ordinary Refine composition, no new mechanism.
    return nil // credential already validated by DecodeIn; nothing left to do
}).SetContextFieldFromIn(userIDField, func(in BearerCred) any { return in.UserID })

sub.Use(bearerAuth)

// The subscribe HANDLER — reads the derived UserID, fully typed, with
// ZERO manual *T mutation and ZERO involvement of this channel's
// (or any channel's) publish side.
events.SubscribeHandle(ctx, sub, transport, func(ctx context.Context, msg SensorReading) error {
    userID, _ := userIDField.Get(ctx)
    return handle(userID, msg)
})
```

### Remaining small open item: recommended Observer/Security dispatch ordering

Confirmed via code: `SubscribeMW`'s dispatch order is registration order,
caller-controlled — go-codex does not enforce Observer-before-Security or
vice versa. **Recommendation (documentation guidance only, not a new
mechanism)**: register the Observer FIRST (as in the sketch above) so a
Security rejection is still observed/logged — mirrors the equivalent
recommendation already given for REST in
`docs/features/security.md`. No code change proposed; a doc note only,
added when Phase 2/3 land.


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

## Relationship to the Observer pattern and ErrorPattern — confirmed orthogonal/compatible, neither threatened by this doc

User direction: explicitly check this doc's "middleware is a partial
route/channel definition" model against the Observer pattern and the
ErrorPattern mechanism, since Observer in particular is "a non-spec
adding middleware, that has no IN/OUT structs and is a special case."
Confirmed via code — both are genuinely different in kind from the
`Middleware[In,Out]` layering this doc is about, and neither needs any
change as a result of Phase 1 or Phase 2.

### Observer — confirmed fully orthogonal, not a layer at all

Traced `DispatchMiddlewareHandlers` (`api/rest/transform_dispatch.go`,
the exact function Phase 1 wraps `DecodeLayer`/`EncodeLayer` inside of):
Observer integration is `stats.ReportErrors(DiagnosticObserver{Ctx: ctx},
"middleware:in"/"middleware:fn", err)` — resolved PURELY from `ctx` at
dispatch time (`stats.ObserverFromContext`-style resolution), with:

- **No `Middleware[In,Out]` involvement at all** — Observer is not
  attached via `.Use(mw)`/`Transform`/`ClientTransform`; it is ambient,
  threaded through `ctx` the same way for EVERY route/channel, with or
  without any middleware attached.
- **Zero spec contribution** — Observer never appears in the OpenAPI/
  AsyncAPI spec, has no `Name`, no `Security` field, no merge-field
  declarations of its own. It genuinely has no `In`/`Out` because it
  isn't decoding or encoding anything — it's an OBSERVATION of what
  already happened, not a declared data transformation.
- **Confirms the user's framing exactly**: Observer is correctly
  understood as a special case OUTSIDE this doc's layering model, not an
  example this doc's principle should be generalized to cover. This doc
  does not propose (and should not be read as implying) any change to
  how Observer works — Phase 1's `DecodeLayer`/`EncodeLayer` extraction
  is a pure internal refactor of the axis decode/encode logic INSIDE
  `DispatchMiddlewareHandlers`; the `stats.ReportErrors` calls that wrap
  it are untouched, at the same call sites, unaffected by where the
  decode/encode logic itself lives.

### ErrorPattern — confirmed a genuine, but different-in-kind, interaction point; Phase 2 must preserve an already-shipped fix

Unlike Observer, `ErrorPattern` is NOT fully orthogonal — it genuinely
interacts with middleware dispatch outcomes, but it is declared on the
ROUTE/channel itself (via `route.ErrorPattern[E,B](...)`), never on
`Middleware[In,Out]` — confirmed via grep: zero `ErrorPattern`-related
fields or methods exist on `Middleware[In,Out]` anywhere. The
interaction is at the ERROR-CLASSIFICATION level, confirmed via
`middlewareDispatchError`'s own fields (`api/rest/transform_dispatch.go`):

- A middleware's `DecodeIn` failure (a merge-field/axis decode or
  `InCodec.Validate` failure) is classified `isFnError: false` — NOT
  ErrorPattern-eligible, treated as a plain param-validation-style
  failure (no business error exists yet to match against a pattern).
- A middleware's own Fn failure (the user's business logic) IS
  classified `isFnError: true` — ErrorPattern-eligible, falling back to
  `MiddlewareError` when no pattern matches.

**Security's CURRENT error handling already received this exact fix** —
confirmed via `adapters/nethttp/adapter.go`'s own code comment:
"Security middleware Fn error IS ErrorPattern-eligible now (session-review
finding H1...) — previously bypassed ErrorResponseFor entirely, always
producing SecurityError." This is an ALREADY-SHIPPED behavior, not a new
design question — Phase 2 MUST preserve it: once Security's credential
Fn dispatches through the SAME `DispatchMiddlewareHandlers` mechanism
every other middleware uses, its Fn error naturally gets the SAME
`isFnError: true`/ErrorPattern-eligible classification for free, with NO
special-casing needed (confirming, yet again, that routing Security
through the general mechanism is strictly as good as or better than its
current bespoke path, never a regression).

**One asymmetry confirmed consistent, not a regression**: today,
`ValidateSecurityCredentials`'s failure (the `SecurityScheme.Codec`
path already resolved as redundant/to-be-deprecated — see "Open design
decisions" item 6) bypasses ErrorPattern entirely (`errFn(sw, r,
http.StatusUnauthorized, credErr)`, called directly, no pattern
consultation) — DIFFERENT from the Fn-error path's ErrorPattern
eligibility. Under Phase 2, credential validation moves to
`mw.InCodec.Validate(in)`, which runs INSIDE `DecodeIn`/`DecodeLayer` —
classified `isFnError: false`, i.e. also NOT ErrorPattern-eligible. **This
is the SAME classification as today, reached via a different code path**
— confirmed consistent, not a behavior change to flag as a risk.

**See also "Implementation rollout" below** — this section describes
the 4 CONCERNS (Phase 1–4); the rollout section maps them onto the
actual ORDER of implementation work, one API package at a time.

## Implementation rollout: one API per phase, learnings carried forward

The 4 phases above are organized by CONCERN (shared mechanism, Security,
context propagation, connection-level auth) — each concern spans
multiple API packages. Actual implementation instead proceeds API BY
API, so that each package's rollout can be fully designed, reviewed,
planned, implemented, and learned from BEFORE the next package starts —
rather than attempting all 3 packages' version of one concern
simultaneously.

**Per-phase workflow (identical for A/B/C)**: design → review (a
dedicated gaps/open-decisions pass, like the ones already run against
this doc) → plan the implementation → implement → **document learnings
relevant to the next phase** (a new subsection, added to this doc AFTER
that phase ships, capturing anything the rollout revealed that wasn't
visible from the prior phase(s) alone — so the next phase doesn't
silently re-discover it).

- **Rollout Phase A — `api/rest`.** Scope: Phase 1's shared
  `DecodeLayer`/`EncodeLayer` mechanism is EXTRACTED AND PROVEN here
  first — generalized in SHAPE, but only exercised against REST's own
  `buildDecodeIn`/`buildEncodeIn`/`buildEncodeOut`/`buildDecodeOut` this
  phase (events/reqreply's migration onto it is Phase B/C's job, not
  this phase's). **Also in scope, as ONE COMBINED deliverable (not 2
  sequential steps)**: the "Architecture revision" above — dropping
  `Transform`/`ClientTransform` in favor of reflection-based `HandleMW`/
  `ClientMW` — AND Security's generalization, unified in the SAME step
  (Security's existing Fn shapes become just one more case the
  now-unified dispatch recognizes, retiring `runSecurityMiddleware`/
  `mergeCredentialHeaders` directly, rather than migrating Security onto
  a still-separate Transform-based mechanism first). Also in scope:
  Phase 3's context-propagation work, REST-only
  (`SetContextFieldFromIn`/`SetContextFieldFromOut` + extending
  `EnsureContextFields` to REST's client side); the REST-only
  `Client.Call`/`Client.Consume` `ClientMiddlewareHandlers`-dispatch
  prerequisite; SSE coverage. **Phase 4 does not apply to this phase** —
  HTTP has no connection-level, broker-style auth concept for
  `AddConnectSecurityScheme`/`mqtt5.Connect` to address.
- **Rollout Phase B — `api/events`.** Scope: migrate events' own
  `buildDecodeIn`/`buildEncodeIn`/`buildEncodeOut`/`buildDecodeOut` onto
  Phase A's NOW-PROVEN shared mechanism — this is the mechanism's real
  generality test (confirms it isn't "REST-shaped with mqtt5 bolted on"),
  not a rubber-stamp. **Also, as ONE COMBINED deliverable (mirroring
  Phase A)**: the "Architecture revision" applied to events' OWN
  `Transform`/`ClientTransform` (`api/events/transform.go`) — folded
  into `Subscriber.SubscribeMW`/`Publisher.PublishMW` via reflection
  (events' own attachment-method names, not `HandleMW`/`ClientMW`) — AND
  Security's generalization for events, unified in the SAME step. Also:
  Phase 3's context-propagation for events (greenfield `ContextField`
  integration, plus the confirmed `SetContextFieldFromIn`-only-on-Subscribe
  asymmetry — `SetContextFieldFromOut` structurally cannot apply
  there); ALL of Phase 4 (connection-level auth:
  `Client.AddConnectSecurityScheme`, `mqtt5.Connect`'s
  `ConnectError`/Observer extension) — Phase 4 was events-motivated, and
  the "api/events middleware deep-dive" section (combining message-level
  OAuth2/scopes with connection-level auth in one AsyncAPI rendering)
  already lives here.
- **Rollout Phase C — `api/reqreply`.** Scope: migrate reqreply's own
  `buildDecodeIn`/etc. onto the shared mechanism (the 2nd confirming
  consumer, after events). **Also, as ONE COMBINED deliverable
  (mirroring Phase A)**: the "Architecture revision" applied to
  reqreply's OWN `Transform`/`ClientTransform` (`api/reqreply/transform.go`)
  — folded into `Route.HandleMW`/`ClientMW` via reflection (reqreply
  uses the SAME method names as REST, confirmed via
  `api/reqreply/middleware.go`) — AND Security's generalization for
  reqreply, unified in the SAME step. Also: Phase 3's
  context-propagation for reqreply (greenfield, confirmed structurally
  IDENTICAL to REST's shape — fully symmetric, no events-style
  asymmetry); Phase 4's connection-level auth APPLIED to reqreply
  (`Builder.AddConnectSecurityScheme`, confirmed
  `mqtt5.Connect`/`*mqtt5.SecuredClient` reuse with ZERO adapter code
  change — the design already covers this from Phase B's work; Phase C
  is where it actually ships for reqreply callers).

### Learnings from Rollout Phase A (for Phase B/C)

**Shipped.** `api/rest`'s `Transform`/`ClientTransform`/`TransformSSE`/
`ClientTransformSSE` were removed and folded into `HandleMW`/`ClientMW`;
Security now dispatches through the SAME unified mechanism via a new
`GrantedScopes` conventional field. Full verification (`go build ./...`,
`go vet ./...`, `go test ./...`, `just check`, every `examples/*/`) is
clean. Concrete findings worth carrying into Phase B (`api/events`) and
Phase C (`api/reqreply`):

1. **Shape-detect on `fn`'s reflected signature, never on `mw`'s dynamic
   type.** `rest.SecurityMiddleware[In,Out]` can produce a
   `Middleware[In,Out]` value used PURELY as a legacy credential-shape
   carrier (its `In`/`Out` never touched by `fn`) — only inspecting
   `fn`'s own parameter types (`isBoundHandleMWShape`/
   `isBoundClientMWShape` in `api/rest/middleware.go`) correctly
   disambiguates the bound-dispatch path from the legacy path. This was
   caught by an ACTUAL test misclassification, not design review — plan
   for an equivalent representative-migration step in Phase B/C before
   trusting the analogous `events`/`reqreply` dispatchers.
2. **A generalized `SecurityMiddleware[In,Out]` needs a real default
   codec, not a zero value.** Leaving `InCodec`/`OutCodec` at Go
   zero-value panics (nil Encode/Decode funcs) the first time a
   non-`struct{}` `In`/`Out` is actually validated. `codex.Struct[T]()`
   with zero declared fields is a safe, verified no-op default
   (`Encode` returns `map[string]any{}`; `Decode`/`Validate` succeed
   trivially) — apply the same default when `events`/
   `reqreply.SecurityMiddleware` are generalized in Phase B/C.
3. **`EncodeMergeVars` must be a NEW sibling function, never an
   extension of `EncodeVars`.** Extending `EncodeVars` directly to
   support omission broke 2 existing tests and would have silently
   violated the documented path/topic/dotted-key "a declared var is
   always present" guarantee. The omit-aware sparse-check belongs in a
   separate function consulted only by the NEW omit-empty constructor
   family, never by the existing required/optional ones.
4. **Removing an old multi-purpose function needs its FULL
   responsibility list enumerated before deletion, not just its
   headline purpose.** `runSecurityMiddleware`/`mergeCredentialHeaders`
   bundled wiring + grant-merging + dispatch-ordering in one call;
   retiring them required the `GrantedScopes`-conventional-field +
   `CollectGrantsReflect`/`MergeMiddlewareHandlerGrants` replacement to
   be independently verified (via real test migration, not review) to
   reproduce every one of those responsibilities before the old
   functions were deleted.
5. **Migrate the real motivating example early, not last.** The
   `examples/go-edge-models` `newAuthCredentialFunc` migration surfaced
   the `SecurityMiddleware` zero-value-codec panic (finding 2 above) —
   a case review alone would not have caught, since it only manifests
   when a REAL non-`struct{}` credential type flows through dispatch.
   Schedule the equivalent real-example migration for Phase B/C before
   declaring either phase done.
6. **Design-doc addenda, not rewritten body text, for a "frozen but
   evolving" design record.** `docs/design/d-0003-codec-declared-middlewares.md`'s
   own established convention (append "Addendum N", flag prior code
   samples as historical) scaled cleanly to a 4th addendum — reuse this
   pattern rather than rewriting the doc's original worked examples when
   Phase B/C ship their own architectural changes.
7. **A post-implementation REVIEW pass (comparing shipped code line-by-line
   against this doc's own "Confirmed mechanical details," not just
   re-reading the doc) found and closed a real gap: `adapters/nethttp`'s/
   `adapters/chi`'s `ports`-facing single-route binding adapters
   (`IngestAdapter`, `SSEAdapter`, `LatestAdapter`, `stream.go`'s
   handlers — all built on `handlerFunc`/`sseHandlerFunc`) never
   dispatched `RouteHandle.MiddlewareHandlers` at all, silently dropping
   Security/ordinary codec-backed middleware for any route served via a
   `ports.SourceAdapter`/`SinkAdapter`/`LatestAdapter` instead of
   `Server.Attach`.** Pre-existing (predated this phase — the SAME gap
   applied to the already-shipped agnostic `.Use(mw)`-attached middleware
   before Rollout Phase A even began), not a regression introduced by
   this phase, but directly adjacent to its own "Security now fully
   unified through HandleMW" claim. Fixed by threading the SAME
   `CollectGrantsReflect`/`DispatchMiddlewareHandlers`/
   `MergeMiddlewareHandlerGrants`/`CheckScopes` sequence `serve.go`/
   `serve_sse.go` already used into `handlerFunc`/`sseHandlerFunc`
   directly, and wiring real `Implementations`/`MiddlewareHandlers` into
   all 2×6 ports-adapter call sites (both nethttp and chi). A second,
   smaller drift was found and fixed in the SAME pass: chi's
   `handlerFunc` still inlined `codex.EncodeVars` (no omit-empty support)
   for response header/cookie merge fields where nethttp's had already
   been updated to `codex.EncodeMergeVars` — confirming that a
   mechanical, cross-adapter change (nethttp/chi are supposed to mirror
   each other exactly) can silently drift out of sync even within the
   SAME phase unless a dedicated line-by-line review pass checks for it.
   **Actionable for Phase B/C**: check whether `adapters/mqtt`/`mqtt5`/
   `zeromq`'s own single-item/ports-facing binding adapters have an
   analogous gap once events/reqreply gain their own `HandleMW`-fold-in
   equivalent — do not assume parity with the reflect-based `Attach`/
   `Serve` path without checking each ports adapter individually.
8. **Confirmed, events-specific: `adapters/mqtt5/adapter.go`'s
   Subscribe/Publish dispatch is the ONE AND ONLY per-message pipeline
   for events — no Gap-1-style duplicate-dispatch-path risk exists.**
   Unlike REST's `Server.Attach`'s reflect-based `serve.go` vs. the
   SEPARATE `handlerFunc`/`sseHandlerFunc` used by `ports` adapters (2
   independent implementations of the same pipeline that silently
   drifted apart), `adapters/mqtt5/adapter.go` already dispatches
   `MiddlewareHandlers`/`ClientMiddlewareHandlers` generically via the
   existing `Agnostic bool` field, reused identically regardless of
   consumer. Phase B's bound-dispatch handlers will be picked up
   automatically by this SAME call site — confirmed, no second
   ports-adapter-specific wiring pass needed for events.
9. **Confirmed, events-specific: shape-detection (fn signature, not
   `mw`'s dynamic type) must apply to events' fold-in from the start,
   not be rediscovered via a failing test.** `events.SubscribeMW`/
   `PublishMW` already share REST's PRE-Phase-A pattern exactly —
   unconditional `buildServerImplementation(mw, fn)`, legacy
   `middleware.Middleware`+`ServerImplementation.Fn` shape — meaning
   once `events.SecurityMiddleware` is generalized to `[In,Out]`, it can
   ALSO be used purely as a legacy credential-shape carrier, the EXACT
   same ambiguity Phase A found via `examples/go-edge-models`'s real
   `basicAuthMw`. Events' fold-in must use the SAME `fn`-signature
   detection technique (`isBoundHandleMWShape`'s events equivalent) from
   day one.
10. **Confirmed, events-specific: spec metadata is ALREADY bundled
    directly on `MiddlewareHandler` — simpler than REST, not a gap.**
    REST's `applyAgnosticRoute`/`applyBoundRoute` append to TWO separate
    builder fields (`rb.middlewareHandlers` AND
    `rb.middlewareSpecContributions`). Events' `MiddlewareHandler` struct
    (`api/events/transform.go`) already carries its OWN
    `propertyParams []PropertyParam` field directly — no parallel
    spec-contribution slice exists or is needed. Phase B's new
    `applyBoundSubscriber`/`applyBoundPublisher` methods (events' mirror
    of REST's `applyBoundRoute`/`applyBoundClientRoute`) only need to
    append ONE handler value each, `propertyParams` populated inline.
11. **Confirmed, events-specific: `checkEventsMiddlewareNameUniquenessAndAttachment`
    (D6(b)/D7) is ALREADY generic over both attachment styles — no
    change needed, unlike `CheckCoverage`.** It already takes
    `[]MiddlewareHandler`/`[]ClientMiddlewareHandler` directly and
    doesn't care how a handler was built — confirmed it will keep
    working unchanged once `SubscribeMW`/`PublishMW` populate these via
    the new bound path. Only `events.CheckCoverage` (a DIFFERENT
    function — the Security-coverage check) needs the SAME signature
    extension `rest.CheckCoverage` got in Phase A (a `handlers
    []MiddlewareHandler` parameter, not a storage merge).
12. **Confirmed, events-specific: the cross-cutting `EncodeMergeVars`/
    omit-empty-constructor work is ALREADY DONE for events — not a Phase
    B deliverable.** `api/events/builder.go`/`property_param.go` already
    use `codex.EncodeMergeVars` for property merge fields and already
    have `NewOmitEmptyPropertyParam` (shipped as part of Phase A's
    cross-package Level-1 step, since that primitive was never
    REST-specific). Topic vars correctly still use plain
    `codex.EncodeVars` (no omit-empty — topic vars are never optional,
    mirrors REST's path vars). Phase B's own remaining mechanism work is
    narrower than it might first appear: migrating
    `buildDecodeIn`/`buildEncodeIn`/`buildEncodeOut`/`buildDecodeOut`'s
    INTERNAL implementation onto `middleware.DecodeLayer`/`EncodeLayer`
    (confirmed still using hand-written sequential
    `codex.DecodeVars`/`EncodeVars`/`EncodeMergeVars` calls per axis
    today) — the EXTERNAL `EncodeMergeVars`/omit-empty surface is
    already fully shared across all 3 packages.
13. **Real migration targets identified for events — migrate early, per
    learning #5 above.** `examples/events-api/routes/routes.go`'s
    `APIKeyAuthMW` and `examples/api-events/main.go`'s `bearerAuthMW` are
    REAL, shipping uses of `events.SecurityMiddleware` attached via
    `.Use(mw).SubscribeMW(&mw, implFn)` — the exact legacy-shape pattern
    that will need migrating onto the new bound path (or confirmed to
    keep working via the legacy branch) once the fold-in ships. Schedule
    migrating these EARLY in Phase B's implementation, mirroring
    `examples/go-edge-models`'s role for Phase A, rather than discovering
    a real-world incompatibility late.

### Learnings from Rollout Phase B (for Phase C)

**Shipped.** `api/events`'s `Transform`/`ClientTransform` were removed
and folded into `SubscribeMW`/`PublishMW`; Security generalized
(`SecurityMiddleware[In,Out]`) with the zero-value-codec fix applied
FROM THE START (not rediscovered); `events.CheckCoverage` extended;
`SetContextFieldFromIn`/`SetContextFieldFromOut` added (Publish-only for
the latter); `Client.AddConnectSecurityScheme` ships Phase 4 for events;
`adapters/mqtt5.ConnectError`/`ConnectOptions` extended. Full
verification clean. Concrete findings for Phase C (`api/reqreply`):

1. **Reqreply's legacy-vs-bound shape ambiguity needs checking
   per-adapter, not assumed identical to REST or events.** Phase B found
   a GENUINE divergence from REST's own shape-detection technique:
   events' `adapters/mqtt`/`zeromq` legacy subscribe-security Fn shares
   BOTH the bound shape's arity AND its 2nd-param type (`*T`), forcing a
   3rd-param-type check instead of REST's simpler 2nd-param check —
   confirmed only by writing the actual test, not by analogy to REST.
   Before implementing reqreply's fold-in, write out EVERY adapter's
   (`mqtt5`, `zeromq`) actual legacy Fn shapes FIRST and compare arities/
   param types against the bound shape directly — do not assume "REST's
   technique generalizes unchanged" a second time.
2. **The "DRY builder consolidation" (one `buildXAny` function shared by
   agnostic AND bound paths) has a real trap: don't let a field meant
   ONLY for the bound path (like D7's `dualAttached`) leak into the
   shared builder.** Confirmed via an ACTUAL test failure during Phase
   B's own implementation (not caught by design review): computing
   `dualAttached: mw.isBundled()` inside the shared builder broke the
   legitimate pure-agnostic case, since `isBundled()` is naturally true
   there too. Fix: the shared builder must NEVER set attachment-style-
   specific fields; only the BOUND call site sets them, after calling
   the shared builder. Apply this discipline from the start for reqreply
   rather than rediscovering it.
3. **A signature change to a dispatch-closure field type (adding `ctx
   context.Context` to `DecodeIn`/`EncodeOut` for Phase 3's context
   propagation) ripples into EVERY direct struct-literal test fixture
   constructing that type, not just production call sites.** Confirmed
   via compile errors in `transform_dispatch_test.go`'s own hand-built
   `MiddlewareHandler{DecodeIn: func(...){...}}` literals — grep for
   struct-literal fixtures of the affected type BEFORE assuming a
   signature change is contained to production code and its direct
   callers.
4. **Reqreply's own `buildDecodeIn`/`buildEncodeIn`/`buildEncodeOut`/
   `buildDecodeOut` migration onto `middleware.DecodeLayer`/`EncodeLayer`
   is the 3RD (not 2nd) consumer — by this point the mechanism is
   thoroughly proven; expect this step to be routine, not risky.**
   Confirmed zero behavior change required for events' identical
   migration (every pre-existing test passed unchanged) — the SAME
   should hold for reqreply, which is fully symmetric with REST (unlike
   events' asymmetric Subscribe/Publish shape), making it if anything an
   EASIER migration than events' was.
5. **Reqreply's context-propagation is confirmed fully symmetric with
   REST (no events-style asymmetry)** — per this doc's own
   "Package-by-package verdict," both `SetContextFieldFromIn` AND
   `SetContextFieldFromOut` apply on BOTH reqreply's request and response
   sides, unlike events' Subscribe-has-no-Out asymmetry. This should
   make reqreply's Phase 3 work a more direct port of REST's exact
   pattern than events' was.
6. **Reqreply's `AddConnectSecurityScheme`/`mqtt5.Connect` reuse needs
   ZERO adapter code change** — confirmed already, by design, before
   Phase B even shipped (`adapters/mqtt5/reqreply_transport.go`'s `Call`/
   `Serve` already take the same plain `MQTTClient`/`MQTTRouter` pair
   `mqtt5.Connect` returns). Phase C's version of this step should be a
   pure `reqreply.Builder.AddConnectSecurityScheme` addition (mirroring
   `events.Client`'s identical method byte-for-byte) plus wiring into
   `reqreply.Builder.AsyncAPISpec()`'s own securitySchemes aggregation —
   no `adapters/mqtt5` changes anticipated at all.
7. **A connection-level enhancement scoped to "the mqtt protocol family"
   in design discussion must be checked against EVERY adapter in that
   family, not just the one named in the roadmap text.** Phase B's
   Level 0 (steps 2-3) extended `adapters/mqtt5.ConnectError`/
   `ConnectOptions` with `ReasonCode`/`ReasonString`/`Observer`, but the
   roadmap doc only ever said "mqtt5.Connect" by name — `adapters/mqtt`
   (v3)'s own, separately-implemented `Connect`/`ConnectError`/
   `ConnectOptions` was overlooked entirely until a post-ship review
   caught it. Confirmed fixable: `paho.mqtt.golang`'s
   `*pahomqtt.ConnectToken.ReturnCode()` exposes v3's CONNACK return
   code, analogous to v5's `Connack.ReasonCode` — now added as
   `ConnectError.ReturnCode byte` + `ConnectOptions.Observer`, gated on
   MQTT 3.1.1's own auth-specific codes (4, 5) rather than MQTT5's
   `>= 0x80` range (the two protocol versions' CONNACK semantics are
   NOT interchangeable — no `ReasonString` equivalent exists for v3).
   Before closing out reqreply's Phase 4 (step 6, above), explicitly
   re-check whether `adapters/mqtt` needs the identical reqreply-side
   treatment too (reqreply's `Call`/`Serve` plumbing is shared with
   events' Connect helpers, so this may already be covered — verify,
   don't assume).

### Learnings from the Phase C review (for Rollout Phase C itself)

**Phase C's own implementation has not started yet** — these are
findings from REVIEWING the roadmap against Phase B's actual shipped
code, found before Phase C begins (see Open design decisions 11-12 and
this doc's status header for the concrete gap/resolution):

1. **A doc comment's claim that something is "confirmed via" existing
   code must be re-verified against that code directly, every time —
   never trusted because it reads convincingly.** `events
   .SecurityMiddleware[In,Out]`'s own doc comment asserted the
   `GrantedScopes`-on-`Out` convention was "confirmed via adapters'
   existing CheckScopes integration" — a specific, falsifiable, and
   FALSE claim (that integration is the OLD legacy-Fn-only path, with
   zero awareness of the new bound dispatch's `Out` values). This
   wasn't caught during Phase B's own implementation or its review
   rounds — only surfaced when Phase C's review traced the claim
   through actual adapter code via grep. Apply the SAME "verify by
   tracing, not by reading" discipline to every doc-comment claim
   inherited from a prior phase before building on top of it.
2. **A "RESOLVED" design decision can still hide a direction-specific
   structural gap that isn't caught until a REAL, non-`struct{}`,
   end-to-end case is exercised — not just a "doesn't panic" unit
   test.** Open design decision 7 (Granted-scopes convention, Option
   3) was written generically ("`Out`'s `GrantedScopes` field, read via
   reflection") without being checked against events' OWN,
   already-established asymmetry between Subscribe (no `Out` in its
   bound Fn shape at all) and Publish (has `Out`). The one new test
   written for this
   (`TestSecurityMiddleware_RealInOutType_DoesNotPanicOnDispatch`)
   exercised the builder-layer closures directly and confirmed no
   panic — but never reached an adapter's real dispatch/`CheckScopes`
   code, so it could not have caught the missing wiring OR the
   structural Subscribe-side gap. This is the SAME "verify by
   migration, not by review" lesson from
   `docs/design/d-0001-rest-middleware-workflow-simplification.md`'s
   own Lessons Learned, now confirmed a 2nd time for a DIFFERENT
   mechanism (scopes enforcement, not dispatch removal) — a design
   review or a shape-level unit test is not equivalent to exercising
   the full, real, adapter-level path.
3. **A roadmap's own "Files to create/modify" table can silently
   conflate a DONE phase with a NOT-DONE one when both touch the same
   adapter directory, and can list an adapter that doesn't even
   support the feature at all.** Confirmed: two rows bundled events-
   side work (shipped in Phase B, all 3 adapters) together with
   reqreply-side work (not yet done, only 2 of 3 adapters even apply)
   under one combined row, and both wrongly included `adapters/mqtt`
   (v3) — which has ZERO reqreply support (no `reqreply.go`/
   `reqreply_transport.go` exists for it at all). Before generating
   Phase C's own implementation todos from this doc's table, re-verify
   each row's adapter list against `find adapters -iname "*reqreply*"`
   (or equivalent) rather than assuming a prior phase's row is still
   accurate.

## Phasing

**Phase 4** (connection-level auth, `api/events` + `api/reqreply`) is independent of Phases 1–3 — a small, additive builder-level method (`AddConnectSecurityScheme`) with no dependency on `DecodeLayer`/`EncodeLayer`/`SetContextFieldFromIn`/`Out`. Can ship before, after, or alongside Phases 1–3.

This doc now has FOUR concrete implementation phases:

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
- **Phase 3 — propagate middleware-derived values to the handler/caller
  (new scope, not a Phase 1/2 dependency).** Extend `middleware.
  ContextField[V]` with a declarative `SetContextFieldFromIn`/`SetContextFieldFromOut`
  link on `Middleware[In,Out]`, so a middleware's decoded `In` (or
  produced `Out`) becomes automatically retrievable by the route/channel
  handler — see "Propagating middleware-derived values to the handler/
  caller" below for the full design. Independently useful, genuinely
  separate from Phase 1/2's own scope (which only extract/generalize the
  EXISTING wire-encoding mechanism, never touch handler-visibility).
- **Phase 4 — connection-level auth, `api/events` + `api/reqreply`**
  (new scope, independent of Phases 1–3). Add
  `Client.AddConnectSecurityScheme`/`Builder.AddConnectSecurityScheme`
  (declare-time, spec-only, D-0006-pattern stage 2) and extend
  `mqtt5.Connect`'s error/Observer fidelity (`ConnectError.ReasonCode`/
  `ReasonString`, `ConnectOptions.Observer` — D-0006-pattern stage 4,
  already-sealed by package boundary). See "api/events middleware
  deep-dive" above for the full design.

Phase 1 is independently valuable (removes a confirmed, self-acknowledged
3-way duplication) even if Phase 2 were deferred — but Phase 2 is what
actually PROVES the shared mechanism is correctly general, not
REST-shaped-with-mqtt5-bolted-on. Phase 3 is independent of BOTH —
useful for ANY middleware (Security or otherwise) in ANY package, not
specific to Security's own generalization. **Phase 4 is independent of
Phase 3 too, confirmed, not just unstated**: Phase 4's `mqtt5.Connect`
runs at Attach-time/Transport-construction, strictly BEFORE any
`Middleware[In,Out]` dispatch exists for a given channel/route — there is
no per-message `In`/`Out` at that point for Phase 3's `ContextField`
mechanism to hook into, so the two phases never interact, by
construction, not merely by omission.

## Prerequisite for Phase 2 (REST only): `Client.Call`/`Client.Consume` must dispatch `ClientMiddlewareHandlers`

**Found during a pre-implementation review — a real, previously-missed
blocking dependency, not a hypothetical risk.** Traced
`adapters/nethttp/clienttransport.go`'s `Call` AND `Consume` (the
functions behind `rest.Client.Call`/`Client.Consume`, the MAIN documented
entry points this whole doc is about) — confirmed via direct grep: ZERO
occurrences of `ClientMiddlewareHandlers` anywhere in this file. This is
not a new discovery of a bug — it's an ALREADY-DOCUMENTED, previously
ACCEPTED limitation, confirmed via `adapters/nethttp/client.go`'s own
code comment (right after the deleted `CallWithHandle`'s doc comment):

> "One pre-existing, KNOWN, unchanged limitation carries over unchanged:
> neither this deleted function's replacement NOR `rest.Client.Call`
> dispatch declared `rest.Route.ClientTransform`/bundled `.Use()`
> codec-backed middleware today (confirmed via code —
> `clientTransport.Call` never called
> dispatchClientMiddlewareIn/Out)... this is a known, accepted gap, not
> a regression affecting any migrated caller."

**Why this was safe to accept before, but is NOT safe to leave as-is for
this doc's Phase 2**: at the time (D-0006 Phase 5a), no real caller used
`ClientTransform`/bundled `.Use()` agnostic middleware through
`Client.Call`, so the gap was harmless. **Phase 2 changes that
directly**: it proposes migrating Security's credential Fn from today's
`ClientImplementations`-based dispatch (which `Call`/`Consume` DO
correctly read today, via `resolveClientSecurity`/`mergeCredentialHeaders`)
onto the `.Use(mw.WithSend(...))` agnostic mechanism — which populates
`ClientMiddlewareHandlers`, a COMPLETELY DIFFERENT field `Call`/`Consume`
never read. **Left unfixed, Phase 2's own worked code example in this doc
(`route.Use(BearerAuthDeclaration.WithSend(...))`) would silently stop
dispatching the credential Fn when called via `client.Call(...)` — a real
functional regression for Security specifically**, not a hypothetical
risk, and not acceptable to ship.

**Confirmed REST/nethttp-specific, NOT shared across packages** — events
and reqreply have NO equivalent gap:

- `adapters/mqtt5/adapter.go`'s publish dispatch ALREADY calls
  `events.DispatchPublishMiddlewareHandlers(ctx, msg,
  handle.ClientMiddlewareHandlers)` (line 925).
- `adapters/mqtt5/reqreply_transport.go` ALREADY calls
  `reqreply.DispatchClientMiddlewareIn`/`DispatchClientMiddlewareOut`
  (lines 966/1230).
- `adapters/chi` has no client at all — not applicable.

**Required fix, scoped precisely**: add `ClientMiddlewareHandlers`
dispatch to `adapters/nethttp/clienttransport.go`'s `Call` AND `Consume`
— mirroring the EXACT pattern `adapters/nethttp/binding.go`/`client.go`
already use (`dispatchClientMiddlewareIn`/`dispatchClientMiddlewareOut`,
confirmed existing, tested functions — nothing new to invent, just a
new call site in 2 more places). This is a BLOCKING PREREQUISITE of
Phase 2 specifically (not Phase 1 — Phase 1 never touches dispatch call
sites, only the axis decode/encode logic inside functions already being
called) — tracked here as its own concrete migration step, not folded
silently into Phase 2's description.

## Prerequisite for Phase 2 (`api/events`): `GrantedScopes` merge-and-enforce wiring + Subscribe `Out`-widening

**Found during a Phase C review — the SAME KIND of previously-missed,
confirmed, blocking gap as the REST prerequisite above, not a
hypothetical risk.** Traced `events.SecurityMiddleware[In,Out]`'s own
doc comment, which claims the `GrantedScopes`-on-`Out` convention
(Open design decision 7) is "confirmed via adapters/mqtt5/mqtt/
zeromq's existing CheckScopes integration" — a specific, falsifiable
claim. Confirmed via direct grep: ZERO occurrences of `GrantedScopes`
anywhere in `adapters/mqtt5`/`mqtt`/`zeromq`. The "CheckScopes
integration" that DOES exist
(`runSubscribeSecurityImpls`/`runPublishSecurityImpls`) is the OLD,
legacy-`ServerImplementation`/`ClientImplementation`-Fn-only path — it
builds its OWN, separate `granted` map and calls `CheckScopes` BEFORE
the new bound `MiddlewareHandler`/`ClientMiddlewareHandler` dispatch
even runs, and never merges with it.

**Why this was never caught**: the one new Phase B test touching this
(`TestSecurityMiddleware_RealInOutType_DoesNotPanicOnDispatch`) only
calls `DecodeIn`/`EncodeOut` directly (the builder-layer closures) — it
never reaches an adapter's real dispatch/`CheckScopes` code. The real
examples (`examples/api-events/main.go`,
`examples/events-api/routes/routes.go`) exclusively use the OLD legacy
Fn shape (`func(ctx, T) (map[string][]string, error)`, `In=Out=
struct{}`), which `isBoundSubscribeMWShape`/`isBoundPublishMWShape`
correctly detect as NOT the bound shape (arity differs) — so nobody
has ever attached a REAL (non-`struct{}`) bound Security middleware
carrying actual `GrantedScopes` end-to-end, through either direction.

**A SECOND, structural wrinkle, found while tracing the first**:
events' SUBSCRIBE bound Fn shape is `func(ctx, *T, In) error` — ONE
return value, no `Out` slot at all (confirmed via
`isBoundSubscribeMWShape`'s `t.NumOut() != 1` check), mirroring
Subscribe's own real business-handler shape (`func(ctx, T) error` —
pub/sub has no reply to a subscribe, so no `Out` exists there, by
design). This means `GrantedScopes` is not merely unwired on
Subscribe — it is STRUCTURALLY IMPOSSIBLE to express through the
bound mechanism at all, until the shape itself changes.

**Confirmed events-specific, NOT shared with REST or reqreply**:

- REST already has this fully wired (`adapters/internal/httpsecurity`'s
  `CollectGrantsReflect`/`MergeMiddlewareHandlerGrants`, confirmed via
  code + its own adapter-level tests).
- `api/reqreply` doesn't have a generalized `SecurityMiddleware
  [In,Out]` yet (Phase C hasn't started) — N/A today, not a gap to
  fix, just new work. Confirmed reqreply does NOT inherit Subscribe's
  structural limitation: its receiving/Serve-side bound `Fn` already
  returns `(Out, error)` (reqreply is always request/REPLY, never
  fire-and-forget).

**Required fix, scoped precisely — ONE pre-Phase-C events patch**
(resolved via Open design decisions 11/12):

1. Add a NEW, additively-detected `func(ctx, *T, In) (Out, error)`
   bound Subscribe shape, alongside the EXISTING `func(ctx, *T, In)
   error` shape (which keeps working completely unchanged for
   general-purpose Subscribe middleware — logging, rate-limiting,
   enrichment). Detected by return-arity (2 vs 1).
2. `DispatchSubscribeMiddlewareHandlers`: when the 2-return shape is
   used, call `mw.OutCodec.Validate(out)` (mirrors `EncodeOut`'s
   existing validation step for Publish), then make `out` available to
   step 3 below. NO wire-encoding — Subscribe's `Out` exists ONLY to
   carry metadata like `GrantedScopes` through to enforcement.
3. A NEW merge-and-enforce helper mirroring REST's
   `CollectGrantsReflect`/`MergeMiddlewareHandlerGrants`: merges
   `GrantedScopes` from BOTH the legacy `ServerImplementation`/
   `ClientImplementation` grants AND any bound `MiddlewareHandler`/
   `ClientMiddlewareHandler`'s decoded `Out`, into ONE `granted` map,
   before the SINGLE existing `CheckScopes` call — wired into all 3
   adapters (`adapters/mqtt5`/`mqtt`/`zeromq`), for BOTH Subscribe and
   Publish dispatch.
4. Regression tests proving: a bound Subscribe Security middleware's
   `GrantedScopes` IS now consulted by `CheckScopes`; a bound Publish
   Security middleware's `GrantedScopes` IS now consulted; existing
   general-purpose (1-return-shape) Subscribe middleware is COMPLETELY
   unaffected (no signature change required, same behavior as before).

This is a BLOCKING PREREQUISITE tracked as its OWN schedulable unit of
work, BEFORE Phase C begins (not folded into it) — Phase C's own
Phase 2 step (reqreply) builds its OWN wiring afterward, reusing
whatever merge-helper shape this patch establishes where it fits
reqreply's own (already-symmetric, both-directions-have-`Out`)
structure.

## SSE (`api/rest`) — partially covered already; the gap above closes the rest

SSE was not explicitly considered in earlier drafts of this doc. Traced
the actual dispatch mechanism directly:

- **Server-side (SSE's receive direction)**: confirmed
  `adapters/nethttp/serve_sse.go` ALREADY calls the EXACT SAME
  `rest.DispatchMiddlewareHandlers` function Route's own server dispatch
  uses (line 220) — meaning Phase 1's `DecodeLayer`/`EncodeLayer`
  extraction (which only touches the axis decode/encode logic INSIDE
  this already-shared function) automatically, silently benefits SSE's
  server-side dispatch too, with ZERO additional Phase 1 work.
- **Client-side (`Consume`)**: shares the EXACT gap described above —
  `clienttransport.go` hosts both `Call` and `Consume` in the same file,
  and the "zero `ClientMiddlewareHandlers` dispatch" finding covers both
  equally. The SAME fix (adding dispatch to `clienttransport.go`) closes
  BOTH gaps in one pass.
- `SSERoute.Use()`/`.HandleMW()`/`.ClientMW()` are confirmed to exist and
  mirror `Route`'s own signatures exactly (`api/rest/middleware.go`) —
  SSE already has full attachment-point parity with `Route`; only the
  CLIENT-side dispatch (shared with the gap above) was missing, not the
  declaration surface.

## Phase 3: propagating middleware-derived values to the route/channel handler/caller

Raised directly: since middleware uses the SAME codec/merge-field
mechanism a route's own Req/Resp does, and the library's own "one
struct, one call" paradigm composes everything into one struct, a
middleware's derived values should be AVAILABLE to the handler — not
just silently merged onto the wire. Confirmed, via code, this is only
PARTIALLY true today, and the gap splits into two genuinely distinct
aspects.

### Two aspects, confirmed distinct

1. **Spec-adding structs (headers/cookies/queries/topics/properties)** —
   a middleware's `Out` CAN already declare these via the EXISTING
   merge-field methods (`WithResponseHeader`/`WithPublishProperty`/etc.),
   and they DO already appear in the OpenAPI/AsyncAPI spec. The gap here
   is RETRIEVAL: once encoded onto the wire, is the Out value ALSO
   retrievable by the handler/caller as a typed value, not just sent?
2. **Derived, non-wire values** (e.g. a `UserID` parsed out of a JWT —
   not itself a new wire location, just something the HANDLER needs
   access to) — these have NO natural spec representation at all (they
   don't correspond to any single wire field); the gap here is
   PROPAGATION: does the handler get this value at all, typed, without
   manual `*Req`/`*T` mutation?

### What already works, confirmed (manual, opt-in, not automatic)

`Transform` (receive-side, all 3 packages) gives the middleware Fn
POINTER access to the route/channel's own `Req`/`T` — confirmed
identical signatures: REST `func(ctx, req *Req, in In) (Out, error)`,
events `func(ctx, msg *T, in In) error`, reqreply `func(ctx, req *Req,
in In) (Out, error)`. A Fn CAN manually copy a derived value from `In`
into `*Req`/`*T`, making it visible to the handler — but this is a
manual, per-field mutation the Fn author must choose to do, not
automatic. **Confirmed TODAY's Security Fn signature already has this
access too** — REST server-side: `func(ctx, *http.Request, *Req)
(map[string][]string, error)`; events subscribe-side: `func(ctx, *T,
[]route.SecurityRequirement) ([]UserProperty, error)` — meaning
Phase 2's CURRENTLY-SKETCHED examples (using the AGNOSTIC
`.Use(mw.WithSend(...))` style, which has NO Req/T access at all) would
actually REGRESS this existing capability unless Phase 3 (below) closes
the gap a different way.

### How nethttp/chi solve this — confirmed standard, no compile-time safety, no spec concept

Both use plain `context.WithValue(ctx, key, value)` — a middleware
writes an untyped `any`, a handler reads it back via a manual type
assertion, zero compile-time safety (a wrong assertion is caught only at
runtime). Neither library has ANY concept of spec rendering (they are
raw HTTP libraries with no OpenAPI/AsyncAPI awareness). There is no
prior art to directly adopt — go-codex's own codec-driven approach
already goes further than the ecosystem baseline.

### go-codex already has a close, codec-typed answer: `middleware.ContextField[V]`

Confirmed, already shipped (`middleware/context_field.go`):
`ContextField[V]{key, codec}`, declared once at package level, shared by
every producer (`Set`) and consumer (`Get`) — Go generics give
compile-time type safety for THIS field (the SAME value is used on both
sides). `Set(ctx, raw any) error` takes a RAW, undecoded value and runs
it through the field's OWN `codec.Decode` — the field's codec does the
ACTUAL parsing/validation (e.g. turning a raw JWT string into a
structured `UserID`), cleanly separating "extract the raw wire value"
from "derive/validate the typed result." Implemented via a shared
mutable box pre-allocated once per request/call
(`EnsureContextFields`), so values set by an earlier middleware are
visible to a later middleware or the handler. **Confirmed via its own
doc comment it deliberately does NOT feed spec rendering** — framed as
"use this INSTEAD OF adding security-specific fields to every route's
own Req type" — matching Aspect 2 (derived, non-wire values) precisely.

**Confirmed scope gaps, via code**: `EnsureContextFields` is called ONLY
by `adapters/nethttp`/`adapters/chi`'s SERVER-side dispatch (`serve.go`,
`serve_sse.go`, `adapter.go`) — ZERO calls anywhere in `adapters/mqtt5`/
`zeromq`/`mqtt` or `api/reqreply`'s adapters, and ZERO calls on REST's
OWN CLIENT side (`clienttransport.go`). `ContextField` is
REST-SERVER-ONLY today, and entirely MANUALLY invoked — nothing links it
declaratively to a `Middleware[In,Out]`'s own `In`/`Out` fields.

### Proposed design — extend `ContextField`, don't replace it; link it declaratively

Confirmed all 6 `WithReceive`/`WithSend` signatures precisely before
designing this, since `events` turned out to be genuinely asymmetric
relative to REST/reqreply:

| Package | Receive direction | Send direction |
|---|---|---|
| `api/rest` | `func(ctx, In) (Out, error)` | `func(ctx) (In, error)` |
| `api/reqreply` | `func(ctx, In) (Out, error)` | `func(ctx) (In, error)` |
| `api/events` | `func(ctx, In) error` — **no Out at all** | `func(ctx) (Out, error)` — produces Out, not In |

REST and reqreply are fully symmetric with each other. **events is
asymmetric on BOTH axes** — its Subscribe (`WithReceive`) produces
NOTHING but an error (no Out to extract a field from at all), and its
Publish (`WithSend`) produces `Out` (not `In` — analogous in ROLE to
REST/reqreply's `WithSend` producing `In`, just named oppositely by
that package's own convention). **A single `SetContextField(field, get
func(Out) any)` method would silently be unusable for events'
Subscribe** — there being no `Out` there at all — so the design splits
into two methods instead of one, reflecting this honestly:

**CRITICAL FIX this round — the sketch below's ORIGINAL signatures
(`field middleware.ContextField[V]`) DO NOT COMPILE, confirmed by
actually compiling the shape**: `V` appears nowhere else in either
method's signature (`Middleware[In,Out]`'s receiver only binds `In`/
`Out`) — Go requires every type parameter referenced in a method to be
either the receiver's own or impossible to introduce new, exactly the
SAME rule driving this whole doc's Architecture revision. The error is
literal: `undefined: V`. **Fix, also confirmed by compiling**: `V` never
actually appears in `ContextField[V].Set`'s OWN signature either
(`Set(ctx context.Context, raw any) error` — `raw` is `any`, not `V`;
decoding happens INSIDE `Set` via the field's own codec) — meaning
EVERY `ContextField[V]`, regardless of `V`, ALREADY satisfies a tiny,
V-free interface. Both methods take THAT interface instead of the
generic struct directly:

```go
// middleware — a new, exported, V-free interface every ContextField[V]
// already satisfies today, with zero changes to ContextField itself.
type ContextFieldSetter interface {
    Set(ctx context.Context, raw any) error
}

// api/rest (events/reqreply mirror) — two new declarative methods on
// Middleware[In,Out]. field is middleware.ContextFieldSetter (NOT
// middleware.ContextField[V] directly) — confirmed necessary, not
// stylistic: see the CRITICAL FIX note above.

// SetContextFieldFromIn declares that, after DecodeIn succeeds (BEFORE the Fn
// runs), dispatch automatically calls field.Set(ctx, get(in)) — no
// manual Fn-body code needed. Works UNIVERSALLY: every package, every
// attachment style (bound Transform, agnostic WithReceive/WithSend, AND
// events' Subscribe, which has no Out at all) — In always exists
// post-decode, regardless of direction or package. The derivation
// itself (e.g. JWT parsing into a structured UserID) happens at the
// credential field's OWN codec level (ordinary codex.Struct/Refine
// composition, nothing new) — so by the time SetContextFieldFromIn runs, the
// derived value is ALREADY part of In.
func (m Middleware[In, Out]) SetContextFieldFromIn(field middleware.ContextFieldSetter, get func(In) any) Middleware[In, Out]

// SetContextFieldFromOut is SetContextFieldFromIn's sibling for the PRODUCED value —
// Out for REST/reqreply's receive direction AND events' send
// direction; In for REST/reqreply's send direction. NOT usable for
// events' Subscribe (WithReceive) — there is no Out parameter to
// reference there; Go's own type system means the method simply
// doesn't type-check against that shape, not a runtime restriction.
func (m Middleware[In, Out]) SetContextFieldFromOut(field middleware.ContextFieldSetter, get func(Out) any) Middleware[In, Out]
```

A caller still passes a concrete `ContextField[string]{...}`/
`ContextField[UserID]{...}` value directly at the call site — Go's
ordinary implicit interface satisfaction means NOTHING changes about
how `SetContextFieldFromIn`/`Out` are actually CALLED, only how they're
DECLARED; this fix is invisible to every caller, confirmed via a
compiling round-trip test (declare `ContextField[string]{}`, pass it to
a `ContextFieldSetter`-typed parameter, build clean).

Dispatch calls every declared link automatically — `SetContextFieldFromIn`
right after `DecodeIn` succeeds (before the Fn runs), `SetContextFieldFromOut`
right after the Fn produces its value (alongside the EXISTING
`EncodeOut`/`EncodeIn` calls) — mirroring exactly how wire merge fields
are already dispatched automatically today, just targeting
`ContextField.Set` instead of a wire location. **Does NOT feed spec
rendering** — by design, matching `ContextField`'s own existing
framing; a caller wanting the SAME value ALSO wire-rendered declares a
SEPARATE, ordinary merge field on the same struct — the two are
complementary, not exclusive.

### Package-by-package verdict

- **`api/rest`**: BOTH methods fully supported, both directions.
  Confirmed prerequisites: extend `EnsureContextFields` to the CLIENT
  side (`clienttransport.go`, currently server-only); new dispatch call
  sites alongside the existing `EncodeOut`/`EncodeIn` calls.
- **`api/reqreply`**: IDENTICAL shape to REST (fully symmetric, per the
  table above) — same 2 prerequisites, scoped to
  `adapters/mqtt5/reqreply_transport.go`/`adapters/zeromq/
  reqreply_transport.go`. PLUS a confirmed, NEW prerequisite:
  `ContextField`/`EnsureContextFields` have NEVER been wired into
  reqreply's dispatch at all (confirmed zero existing usage) — this is
  GREENFIELD integration for this package, not an extension.
- **`api/events`**: `SetContextFieldFromOut` works on PUBLISH (send) only —
  analogous to REST/reqreply's send-side `In`. `SetContextFieldFromIn` is the
  ONLY option — and the thing that actually closes the user's original
  UserID-in-handler use case — on SUBSCRIBE (receive), PROVIDED the
  derivation happens at the credential field's OWN codec level
  (confirmed a real, if more constrained, requirement specific to
  events — REST/reqreply could ALSO derive post-Fn via `Out`; events
  cannot). Same greenfield `ContextField` integration prerequisite as
  reqreply (confirmed zero existing usage in `adapters/mqtt5`/`zeromq`/
  `mqtt`'s events dispatch either).

**This design also fully retires the Phase 2 Transform-vs-agnostic
regression concern** raised earlier — once `SetContextFieldFromIn` exists,
Security's Fn no longer needs `*Req`/`*T` access to expose a derived
value to the handler, so the agnostic `.Use(mw.WithSend(...))` style
(Phase 2's original worked examples) is fine again, PROVIDED Phase 3
ships — if Phase 3 is deferred, Phase 2 should note this as an interim
trade-off (see new Open design decision below).

### Design decisions for Phase 3 — all 4 RESOLVED (were "NOT yet resolved — genuinely new, unlike Phase 1/2's")

- **Sequencing: does Phase 2 need `*Req`/`*T` enrichment access for
  Security if Phase 3 is deferred or ships later? — RESOLVED by the
  "Architecture revision" above, not left open.** The original concern:
  if Phase 3 (context propagation) is NOT implemented alongside Phase 2,
  Security's agnostic-style migration would regress today's `*Req`/`*T`
  enrichment capability until Phase 3 lands. Now moot — the Architecture
  revision makes `HandleMW`/`ClientMW` themselves provide `*Req` access
  directly (the SAME capability `Transform` used to be the only way to
  get), with ZERO dependency on Phase 3 shipping first. A Security
  middleware needing `*Req`/`*T` enrichment simply attaches via
  `HandleMW`/`ClientMW` (not the agnostic `.Use(mw.WithSend(...))`
  style) — no interim measure or accepted regression needed.
- **Naming — RESOLVED: `SetContextFieldFromIn`/`SetContextFieldFromOut`,
  not `PublishFieldIn`/`PublishFieldOut`.** The original sketch used a
  `PublishField*` prefix, chosen to read as "publish this value into the
  ContextField" — but this COLLIDES with `api/events`' own
  Publish/Subscribe vocabulary: on events' SUBSCRIBE path specifically, a
  name containing "Publish" wrongly suggests the mechanism is tied to the
  PUBLISH side, when it is not — `SetContextFieldFromIn` works on
  SUBSCRIBE precisely BECAUSE it is sourced from `In` (always available,
  regardless of direction), with ZERO relationship to publish. Renamed to
  name the mechanism by its SOURCE (`In`/`Out`), never by a verb that
  could be misread as a pub/sub role — closes a real, user-caught naming
  defect (see "api/events middleware deep-dive" section's own call-out).
  Other alternatives considered and still rejected: a single method with
  an enum/flag distinguishing direction (would compile-check against the
  wrong direction for events, defeating the whole point of catching the
  events-Subscribe case at compile time); a single method overloaded via
  Go's type system (not possible — Go has no function overloading).
- **RESOLVED (Phase B design-closure round): `ContextField`'s existing
  shared-mutable-box implementation needs ZERO changes to support
  events'/reqreply's dispatch models — only a new `EnsureContextFields`
  call site per adapter, reusing the box as-is.** Confirmed by direct
  analogy to Phase A's OWN shipped proof, not left as a speculative
  "needs a close read": `ContextFieldSetter.Set(ctx context.Context, raw
  any) error` was deliberately designed to be BOTH `V`-free AND
  direction-free (see the "CRITICAL FIX" note above) — it has no
  awareness of In vs. Out, Subscribe vs. Publish, or which package calls
  it; it is a pure `(ctx, raw) error` sink. `EnsureContextFields` itself
  (pre-allocates the shared box once per ctx, idempotent) is ALSO
  already package-agnostic — confirmed via code, it takes only a
  `context.Context`, nothing REST-specific. The ONLY per-package work is
  therefore: (1) call `EnsureContextFields` at the right dispatch point
  in `adapters/mqtt5`/`mqtt`/`zeromq`'s Subscribe/Publish path (events)
  and `adapters/mqtt5`/`zeromq`'s reqreply transport (mirroring
  `adapters/nethttp`/`chi`'s existing call sites exactly), and (2) add
  `SetContextFieldFromIn`/`SetContextFieldFromOut` methods to
  `events.Middleware[In,Out]`/`reqreply.Middleware[In,Out]`, dispatched
  from their OWN `buildDecodeIn`/`buildEncodeOut` — immediately after
  `InCodec.Validate`/`OutCodec.Validate` respectively, the EXACT two
  insertion points REST's `api/rest/transform.go` already uses. No
  design spike needed; this is now a mechanical port.
- **RESOLVED-AS-DEFERRED (Phase B design-closure round): events does
  NOT gain its own post-Fn "Out-equivalent" return channel for Subscribe
  in Phase B.** Re-confirmed, no new information changes the prior
  conclusion — `SetContextFieldFromIn`'s derivation-at-the-codec-level
  constraint (the UserID-from-token derivation happening in the
  credential field's OWN `codex.Struct`/`Refine` composition, not in a
  post-Fn return value) is accepted as Phase B's documented design
  constraint, not a gap to close. A breaking `WithReceive` signature
  change remains explicitly OUT of scope for Phase B — revisit only if
  real Phase B usage surfaces a concrete case the codec-level-derivation
  constraint cannot express (none identified so far).

## Cross-call client-side state (session/cookie-jar pattern) — confirmed already achievable, no new mechanism

A natural follow-up question this doc's Phase 3 raises: Phase 3 covers
WITHIN-call propagation (middleware → handler, same request/response
lifecycle). What about CROSS-call persistence — the client-side
equivalent of a browser's cookie jar, where a session value the server
sent back on call N should automatically be available again on call
N+1, without the caller manually threading it through? Server-side,
this is straightforward (a Security/general Fn calls Redis or any store
directly — ordinary Go code, zero go-codex involvement). Client-side,
there's no browser-style automatic cookie jar — so is this a gap?

**Confirmed: no — already fully achievable today, via the EXISTING
`WithClientMiddlewareOut`/`ClientMiddlewareOutFromContext` mechanism**
(`adapters/nethttp/client_middleware.go`). A call's decoded `Out` is
already exposed, keyed by the middleware's own `Declaration.Name` —
reusing the SAME ctx (decorated ONCE via `WithClientMiddlewareOut`)
across multiple subsequent calls lets a caller read back a PRIOR call's
decoded value and feed it into whatever state their OWN credential `Fn`
reads from.

### The settled conclusion: state storage stays 100% the implementer's job

Exactly symmetric with the server side, not an exception to it: go-codex's
role stops at decoding/encoding the DECLARED merge fields — what happens
to that decoded value afterward (an in-memory variable, a local cache,
Redis, memcached, whatever fits the deployment) is entirely up to the
implementer, same as a server-side Fn's own already-unconstrained design
(confirmed throughout this doc — e.g. "Cross-protocol credential
composition"'s `auth.go` citation, which ALREADY makes a real REST call
and caches the result via `sync.Once`, with zero go-codex-provided
caching mechanism involved).

### Worked example — using the EXISTING mechanism for session-token persistence

```go
// Decorate ctx ONCE, reuse it across every subsequent call — the sink
// persists for as long as this ctx value does.
ctx = rest.WithClientMiddlewareOut(ctx)

// Call 1 — server issues a rotating session token via a declared
// response merge field (mw's own Out).
_, err := rest.CallWithTransport(ctx, transport, handle1, req1, opts)
if out, ok := rest.ClientMiddlewareOutFromContext(ctx)["sessionAuth"].(SessionOut); ok {
    // Entirely ordinary Go code — the SAME sync.Once/mutex pattern
    // newAuthCredentialFunc already uses today. Could equally be a
    // Redis SET call here instead of an in-memory variable — go-codex
    // has no opinion either way.
    sessionState.mu.Lock()
    sessionState.token = out.Token
    sessionState.mu.Unlock()
}

// Call 2 — the SAME middleware's WithSend Fn reads sessionState (its
// own closed-over variable, or a Redis GET, or whatever the
// implementer chose) to produce the next request's credential,
// automatically resent via the ALREADY-declared merge field.
_, err = rest.CallWithTransport(ctx, transport, handle2, req2, opts)
```

### Decision record: `OnReceive`/`LatestValue[V]` considered and REJECTED this round

An earlier draft of this section proposed 2 new mechanisms — a
`Middleware[In,Out].OnReceive(fn func(ctx, Out) error)` hook (automatic
dispatch right after `DecodeOut`) and `middleware.LatestValue[V]` (a
built-in, mutex-protected "remember the latest value" box). **Both
REJECTED, with rationale recorded** so a future reader doesn't
re-propose them without seeing why:

- **`LatestValue[V]` crossed into caching-POLICY territory — the wrong
  layer for go-codex.** It would only solve the single-process,
  in-memory case — exactly the case that's often NOT the right answer
  for real session state (multiple processes/pods needing to SHARE
  state need Redis/memcached anyway, called directly inside the
  implementer's own `Fn`, no go-codex abstraction warranted).
- **`OnReceive` would only have removed boilerplate for a case the
  EXISTING mechanism already unblocks** — not closed an actual
  capability gap. The existing `ClientMiddlewareOutFromContext` pattern
  requires more per-call-site code than other middleware hooks (which
  all dispatch automatically), but the use case is NOT blocked today,
  and introducing new dispatch plumbing purely to save a few lines of
  caller code was judged not worth the added surface area.

## Relationship to sibling "declarative middleware" roadmap docs

Two sibling roadmap docs explore closely related "does boundary X have a
declarative middleware mechanism" territory — cross-referenced here for
discoverability, mirroring this session's established convention (e.g.
`dynamic-port-rebinding.md` ↔ `mcp-ports-declarative-middleware.md`):

- [WebSocket — should it gain a general-purpose declarative middleware
  mechanism?](websocket-declarative-middleware.md) — confirmed OUT OF
  SCOPE here: WebSocket is built on `ports.DuplexPort`/`SocketPattern`,
  not `api/events.Channel`, so there is no `Middleware[In,Out]`
  attachment point to generalize in the first place (confirmed via that
  doc's own research — zero `.HandleMW`/`.ClientMW`/`.Use(` matches
  anywhere under `adapters/websocket`). This doc's `DecodeLayer`/
  `EncodeLayer` mechanism is specific to `api/rest`/`api/events`/
  `api/reqreply`'s shared `Middleware[In,Out]` shape, which WebSocket
  does not have.
- [MCP and Ports Declarative Middleware](mcp-ports-declarative-middleware.md)
  — confirmed OUT OF SCOPE here: MCP/`ports` use a DIFFERENT,
  single-phase attachment model with no spec/two-phase declare-dispatch
  split (see this doc's own "Out of scope" section) — the two doc's
  designs are independent, not competing or overlapping.

## Relationship to D-0006 (protocol-native capabilities) — two distinct, intentionally-separate mechanisms, preserved unchanged

A natural question this redesign raises: does generalizing `Middleware`
dispatch via a shared `DecodeLayer`/`EncodeLayer` mechanism interact with,
weaken, or risk being conflated with
[D-0006's](../design/d-0006-protocol-native-capabilities.md) sealed,
per-adapter `Capability` interface (`mqtt5.QoS`/`mqtt5.Retained`, etc.)?
**Confirmed via code: no — these are two distinct mechanisms, already
cleanly separated today, and this doc's Phase 1–3 redesign preserves that
separation unchanged.** Worth stating explicitly, since both this doc's
prose and D-0006's own prose use the word "capability" loosely and
non-exclusively — a reader skimming either doc could otherwise wrongly
assume they're the same thing or that one subsumes the other.

### Side by side

| | D-0006's sealed `Capability` | This doc's `Middleware[In,Out]` |
|---|---|---|
| Where declared | Adapter-owned (`mqtt5.QoS`, `zeromq.Conflate`, ...) | Core, protocol-agnostic (`middleware.Declaration[In,Out]`) |
| Sealing mechanism | Unexported marker method per adapter (`isMQTT5Capability()`) — Go-compiler-enforced, zero cross-adapter mixing possible | Not sealed — `Middleware[In,Out]` is a plain generic value, usable with any adapter that dispatches `Middleware`'s shared mechanism |
| Supplied at | DECLARE time, via `SubscribeOptions.Capabilities`/`PublishOptions.Capabilities` | DECLARE time too, but via `.Use(mw)`/`HandleMW`/`ClientMW` on a channel/route — a different declare-time surface |
| Dispatch mechanism | Direct method call — `Capability.Apply(wire *WireAttributes) (bool, error)`, NO reflection | Reflection-based — `reflect.ValueOf(h.Fn).Call(...)`, needed because `Fn`'s exact signature varies by attachment style (bound vs. agnostic, `In`/`Out` generic) |
| Coverage/requirement check | `api/events.CapabilityRequirement` (Tier 3 — Explicit) + `CheckCapabilityCoverage`, adapter-agnostic declare-time hook resolved against whichever adapter's sealed value is actually supplied | `rest.CheckCoverage`-equivalent (Security-specific, pre-existing, unchanged by this doc) |
| Spec rendering | Generic `"x-capabilities"` AsyncAPI vendor extension (one entry per `CapabilityRequirement`, adapter-agnostic by design) | The route/channel's own existing merge-field-driven spec rendering (headers/cookies/queries/topic-vars/properties) |

### Why Security stays on this doc's mechanism, not D-0006's sealed `Capability`

D-0006 §5.5 already settled this, and this doc's redesign is consistent
with that resolution rather than reopening it: Security is the ONE
surveyed case that clears BOTH of D-0006's bars for folding into the
sealed-`Capability`-at-Attach-time mechanism — **uniform shape** (scheme +
scopes + credential, the SAME declaration shape in all 3 packages) AND
**uniform-enough support** (every adapter can enforce or at least document
a security requirement) — unlike QoS/User Properties/AMQP addressing,
each of which fails at least one bar (D-0006 §5.1/§5.2's worked
analysis) and so correctly stays adapter-owned and sealed. Because
Security clears both bars, it belongs in the protocol-agnostic
`middleware`/`api/*` core — exactly where `Middleware[In,Out]` already
lives — with zero adapter import required at declare time. This doc's
Phase 2 (generalizing `SecurityMiddleware`'s signature) is a refinement
of HOW Security's existing core-layer mechanism dispatches, not a
proposal to move Security onto D-0006's sealed mechanism or vice versa.

### Explicit non-goal

Phase 1–3 of this doc do NOT touch, generalize, fold into, or otherwise
alter D-0006's sealed `Capability` mechanism. Confirmed: this doc's
"Files to create/modify" table contains zero entries under
`adapters/*/capability.go` or `api/events/capability*.go` — every file
D-0006's mechanism owns is untouched by this redesign. QoS, Retained, and
any future sealed capability keep their own Attach-time,
non-reflection, `Apply(wire)`-based path, completely unchanged by
anything in this doc.

### One confirmed-compatible future point of convergence — not proposed or scheduled here

`adapters/mqtt5/capability.go`'s own comment already notes that
`UserPropertyParam` (today a separate, pre-existing, non-`Capability`
mechanism) could, in a FUTURE round, be folded into a sealed
`UserProperty[In]` Capability type that EMBEDS
`middleware.Declaration[In, struct{}]` for its merge-field vocabulary —
exactly the shape D-0006 §2.1 originally sketched. If that future round
happens, Phase 1's shared `DecodeLayer`/`EncodeLayer` mechanism would be
a natural fit for that Capability's INNER codec/merge-field dispatch —
while the OUTER sealed `isMQTT5Capability()`/`Apply` contract remains
exactly as sealed and adapter-owned as it is today. This is a
confirmed-COMPATIBLE future shape, not a dependency, proposal, or
scheduled phase of this doc — noted here only so a future reader doesn't
need to re-derive that these two mechanisms CAN layer cleanly, should
that future round ever happen.


## Scope decisions

| In scope | Out of scope |
|---|---|
| **Phase 1**: a shared `middleware.DecodeLayer`/`EncodeLayer` mechanism, with all 3 packages' existing `buildDecodeIn`/`buildEncodeIn`/`buildEncodeOut`/`buildDecodeOut` migrated onto it as thin wrappers | Changing `route.SecurityScheme`'s own shape — unchanged, in all 3 packages |
| **Phase 2**: generalizing `SecurityMiddleware`'s signature in ALL THREE packages — `rest.SecurityMiddleware[In, Out any](...)`, `events.SecurityMiddleware[In, Out any](...)`, `reqreply.SecurityMiddleware[In, Out any](...)` — away from the hardcoded `struct{}, struct{}`, dispatched through Phase 1's shared mechanism | Inventing any new merge-field/codec type — explicitly rejected; the EXISTING per-package merge-field constructors are reused verbatim |
| **Architecture revision**: dropping `Transform`/`ClientTransform`(+SSE) as separate free functions, folding their `*Req`-access capability directly into `HandleMW`/`ClientMW` via runtime reflection (the SAME technique Security's existing Fn dispatch already uses) — Security unified onto this SAME mechanism in the SAME step, not a later phase | Changing the Agnostic/reusable style's own existing contract (`WithReceive`/`WithSend` + plain `.Use(mw)`) — unaffected, preserved exactly as it is today |
| Preserving each package's OWN error types (`rest.MiddlewareInputError` vs `events.MiddlewareInputError` vs `reqreply.MiddlewareInputError`) — Phase 1's shared mechanism takes an error CONSTRUCTOR callback, it does not unify the error TYPES themselves (`errors.As` callers must still distinguish which package failed) | Unifying `MiddlewareInputError`/`MiddlewareOutputError` into one cross-package type — explicitly rejected, would break existing `errors.As` call sites for no benefit |
| Preserving `CheckScopes`'s own logic/signature exactly as today, in all 3 packages (confirmed separable) — the granted-scopes VALUE's SOURCE differs per package's CURRENT state, see below | Changing `CheckScopes`'s own logic or signature |
| **Corrected this round (Phase C review) — the granted-scopes-into-`CheckScopes` WIRING is NOT uniformly "already there" across packages, confirmed via code.** REST: genuinely already wired end-to-end for the bound path (`adapters/internal/httpsecurity`'s `CollectGrantsReflect`/`MergeMiddlewareHandlerGrants`). Events: confirmed NOT wired for the bound `MiddlewareHandler`/`ClientMiddlewareHandler` path on EITHER direction (zero `GrantedScopes` references in any of `adapters/mqtt5`/`mqtt`/`zeromq` — only the OLD legacy-Fn path feeds `CheckScopes` today) — tracked as a dedicated pre-Phase-C patch, see "Prerequisite for Phase 2 (api/events)" below and Open design decisions 11/12. Reqreply: N/A today (package doesn't have a generalized `SecurityMiddleware[In,Out]` yet) — built FRESH as part of Phase C's own Phase 2 step, reusing whatever merge-helper shape the events patch establishes | N/A — this row corrects Phase B's own now-superseded scope-decision claim, not a new in/out-of-scope split |
| `adapters/nethttp`/`chi` (HTTP: header/cookie/query) AND `adapters/mqtt5`/`zeromq`/`mqtt` (user-property) — Phase 2 only, once Phase 1's shared mechanism exists to dispatch Security through | A brand-new wire-location kind beyond header/cookie/query/property |
| A breaking replacement of today's fixed-shape `ClientImplementation.Fn`/`ServerImplementation.Fn` signatures for Security specifically, in all 3 packages (Phase 2) | Changing the fixed-shape Fn contract for NON-Security general-purpose middleware — `WithReceive`/`WithSend`'s own existing contract is unchanged; Security adopts it, it doesn't change it |
| N/A (pure cross-reference, no code change) | Any change to `adapters/*/capability.go`'s sealed `Capability` interface or `api/events/capability*.go`'s declare/Attach-time coverage-check mechanism (D-0006) — confirmed fully independent, zero files touched |
| **Phase 4**: `events.Client.AddConnectSecurityScheme`/`reqreply.Builder.AddConnectSecurityScheme` (standalone connection-level security-scheme spec registration, both packages) PLUS `mqtt5.Connect`'s error/Observer fidelity (`ConnectError.ReasonCode`/`ReasonString`, `ConnectOptions.Observer`) | A new shared/core-layer connection-security type unifying spec+runtime — explicitly rejected; `route.SecurityScheme` (already shared) plus a documented reuse convention is sufficient, no new type needed. Also out of scope: a sealed, marker-method `Capability`-style interface for connection auth — explicitly rejected, no corresponding safety gain (see "Reframed explicitly as a D-0006-pattern capability") |

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

**A genuinely NEW constructor FAMILY, confirmed needed** — found while
re-evaluating `examples/go-edge-models/app/registry/auth.go`'s
`newAuthCredentialFunc` (this doc's own motivating real case, see the
worked example below) against Phase 1's own sub-item (the
`codex.EncodeMergeVars` "omit on encode" function, corrected above):
once `EncodeMergeVars` can check `sparseFieldCodec[T]`, a constructor is
needed to actually DECLARE a merge field backed by it — confirmed via
grep, zero `OmitEmptyField`/`MaybeField` usage exists anywhere in
`api/rest`/`api/events`/`api/reqreply` today, and every
`Merged*Param.field` is unexported (buildable only via each location's
existing `NewRequired*Param`/`NewOptional*Param` pair, neither
omit-capable).

**Corrected this round — NOT header-only.** Every merge-field location
ALREADY comes in Required/Optional PAIRS, confirmed via code:
`NewRequiredQueryParam`/`NewOptionalQueryParam`,
`NewRequiredCookieParam`/`NewOptionalCookieParam`,
`NewRequiredHeaderParam`/`NewOptionalHeaderParam`,
`NewRequiredResponseHeaderParam`/`NewOptionalResponseHeaderParam`,
`NewRequiredResponseCookieParam`/`NewOptionalResponseCookieParam`
(REST); `NewPropertyParam`/`NewOptionalPropertyParam` (events/reqreply,
identical pair shape). An omit-empty tier must match this EXISTING
symmetry, not single out headers — the FULL family:

```go
// api/rest — one per EXISTING Required/Optional pair, all built on
// codex.EncodeMergeVars (NOT codex.EncodeVars), all backed by
// codex.OmitEmptyFieldFunc instead of plain Field. Declares a merge
// field that is OMITTED FROM THE WIRE ENTIRELY (not sent as an empty
// value) whenever isEmpty(get(in)) is true.
func NewOmitEmptyQueryParam[T, V any](name string, codec codex.Codec[V], get func(T) V, set func(*T, V), isEmpty func(V) bool) MergedQueryParam[T]
func NewOmitEmptyCookieParam[T, V any](name string, codec codex.Codec[V], get func(T) V, set func(*T, V), isEmpty func(V) bool) MergedCookieParam[T]
func NewOmitEmptyHeaderParam[T, V any](name string, codec codex.Codec[V], get func(T) V, set func(*T, V), isEmpty func(V) bool) MergedHeaderParam[T]
func NewOmitEmptyResponseHeaderParam[Resp, V any](name string, codec codex.Codec[V], get func(Resp) V, set func(*Resp, V), isEmpty func(V) bool) MergedResponseHeaderParam[Resp]
func NewOmitEmptyResponseCookieParam[Resp, V any](name string, codec codex.Codec[V], get func(Resp) V, set func(*Resp, V), isEmpty func(V) bool) MergedResponseCookieParam[Resp]

// api/events, api/reqreply — one each, mirroring NewOptionalPropertyParam's
// shape exactly. NO topic-param equivalent: confirmed via code,
// NewTopicParam has no Required/Optional split at all (a topic template's
// vars are positional/structural, not an optional-presence concept), so
// "omit if unset" doesn't apply there.
func NewOmitEmptyPropertyParam[T, V any](name string, codec codex.Codec[V], get func(T) V, set func(*T, V), isEmpty func(V) bool) MergedPropertyParam[T]
```

**Open question, not assumed**: whether `NewRequiredSSEEventParam`/
`NewOptionalSSEEventParam` also need an omit-empty variant — less
obviously motivated, since an SSE event is typically a full payload,
not a scalar presence/absence case. Not designed further here; revisit
during implementation if a real use case surfaces.

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

**IMPORTANT — found missing during the Phase C review, NOT yet built
for events or reqreply (see Open design decisions 11/12 and
"Prerequisite for Phase 2 (api/events)" below for the full finding):**
the paragraph above describes the dispatch CALL existing and running —
it does NOT mean `GrantedScopes` is actually read from the dispatched
`Out` and merged into `CheckScopes` anywhere. Confirmed via code: ZERO
`GrantedScopes` references exist in `adapters/mqtt5`/`mqtt`/`zeromq`
today. This is REST-only, currently (`adapters/internal/httpsecurity`'s
`CollectGrantsReflect`/`MergeMiddlewareHandlerGrants`). Additionally,
this "uniform `func(ctx, In) (Out, error)`" signature shown above is
events' PUBLISH shape only — events' SUBSCRIBE bound shape is
`func(ctx, *T, In) error` (confirmed via `isBoundSubscribeMWShape`,
NO `Out` at all, by design — Subscribe has no reply to encode one
into). A scope-granular Security check on Subscribe needs a NEW,
ADDITIVELY-added shape:

```go
// api/events — NEW, additive bound Subscribe shape (alongside the
// EXISTING func(ctx, *T, In) error shape, which keeps working
// unchanged for general-purpose Subscribe middleware). Detected by
// return-arity (2 vs 1), mirroring this codebase's existing
// shape-detection idiom. Out is NEVER wire-encoded for Subscribe
// (nothing to encode it into) — it exists ONLY so a Security-carrying
// middleware can return GrantedScopes, read the SAME way Publish's Out
// already is.
func(ctx context.Context, msg *T, in In) (Out, error)
```

Both the merge-into-`CheckScopes` wiring AND this new Subscribe shape
are scoped as ONE pre-Phase-C events patch (not part of Phase C
itself) — see "Prerequisite for Phase 2 (api/events)" below.

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
input decode failure already uses, in all 3 packages. See "Relationship
to the Observer pattern and ErrorPattern" above for the full confirmation
that Observer is fully orthogonal to this doc's layering model (ambient,
ctx-resolved, zero spec/`In`/`Out` involvement) and that Security's
Fn-error ErrorPattern-eligibility (already shipped) is preserved, not
regressed, by Phase 2.

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
| `TestSecurityMiddleware_GrantedScopes_ReadViaReflectedField` (REST only — ALREADY EXISTS, confirmed via `adapters/internal/httpsecurity`'s own tests) | The RESOLVED `GrantedScopes` convention: dispatch reads `Out.GrantedScopes` via `elem.FieldByName("GrantedScopes")` and feeds it into an UNCHANGED `middleware.CheckScopes` call — **corrected this round: this is NOT "(×3)" — events/reqreply need the NEW tests below instead, not a trivial port of this one** |
| `TestSubscribeMW_BoundShapeWithOut_Detected` (events, pre-Phase-C patch) | The NEW additive `func(ctx, *T, In) (Out, error)` Subscribe shape is correctly detected (return-arity 2), distinct from the EXISTING 1-return shape |
| `TestSubscribeMW_LegacyOneReturnShape_StillDispatchesUnchanged` (events, pre-Phase-C patch) | A general-purpose (non-Security, no `Out` needed) bound Subscribe middleware using the EXISTING 1-return shape is COMPLETELY unaffected by the new shape's addition — no signature change forced on it |
| `TestSubscribeMW_GrantedScopes_MergedIntoCheckScopes` (events, pre-Phase-C patch, all 3 adapters) | A bound Subscribe Security middleware using the NEW 2-return shape, returning real `GrantedScopes`, has them ACTUALLY merged into the `CheckScopes` call — the core fix, end-to-end, not just a "doesn't panic" check |
| `TestPublishMW_GrantedScopes_MergedIntoCheckScopes` (events, pre-Phase-C patch, all 3 adapters) | Same, for Publish's EXISTING `Out` shape — confirms the merge helper covers BOTH directions |
| `TestMergeGrants_LegacyOnly_BoundOnly_Both_Neither` (events, pre-Phase-C patch) | The new merge-and-enforce helper's own unit tests: legacy-only grants, bound-only grants, both merged, neither present — mirrors REST's `MergeMiddlewareHandlerGrants` test matrix |
| `TestSecurityMiddleware_GrantedScopes_MergedIntoCheckScopes` (reqreply, Phase C's own Phase 2) | Reqreply's OWN merge-and-enforce wiring, built fresh (not inherited from events) — both directions, since reqreply's receiving/Serve-side already has `Out` symmetrically with Publish |
| `TestSecurityMiddleware_Out_CarriesAdditionalResponseFields` (×3) | A Security `Out` type with BOTH `GrantedScopes` AND a genuine response merge field (e.g. a declared response header) — confirms the convention doesn't foreclose real response data |
| `TestSecurityMiddleware_CredentialCodecRejects_MiddlewareInputError` (×3) | A credential field's codec validation failure surfaces as the EXISTING, package-specific `MiddlewareInputError`, not a new or unified type |
| `TestSecurityMiddleware_RawRouteVsRouteHandle_Unaffected` (REST only) | This redesign does not reintroduce or interact with the separate `GlobalSecurity` dual-mode dispatch gap (D-0001 Addendum 6) — independent concerns |
| `TestSecurityMiddleware_FnError_StillErrorPatternEligible` (×3 packages) | Confirms Security's already-shipped ErrorPattern-eligibility for Fn errors (session-review finding H1) is PRESERVED once dispatched through the shared mechanism — a declared `ErrorPattern` still matches a Security Fn's business error after migration |
| `TestSecurityMiddleware_CredentialDecodeFailure_NotErrorPatternEligible` (×3 packages) | Confirms a credential merge-field/`InCodec` decode failure stays classified as `isFnError: false` (NOT ErrorPattern-eligible) — consistent with today's `ValidateSecurityCredentials` bypass behavior, reached via a different code path |
| `TestDispatchMiddlewareHandlers_FailFast_StopsAtFirstFailure` (×3, Phase 1) | Confirms the RESOLVED fail-fast decision is preserved by the Phase 1 migration — a 2nd/3rd stacked layer's Fn must NOT run after an earlier layer already failed |
| `TestSecurityMiddleware_InPayloadMutation_NotSupported` (mqtt5 only) | Confirms the RESOLVED "drop" decision — migrating a Security middleware onto the new mechanism has no way to mutate the outgoing payload; documents the limitation via a compile-shape/doc-level test, not a runtime capability test |

**Phase 3 (middleware-derived value propagation):**

| Test | Verifies |
|---|---|
| `TestSetContextFieldFromIn_HandlerRetrievesDerivedValue` (×3 packages, incl. events Subscribe) | A `SetContextFieldFromIn`-linked `ContextField` is retrievable, fully typed, inside the route/channel handler — the user's original "UserID in the handler" use case, proven end-to-end |
| `TestSetContextFieldFromOut_CallerRetrievesDerivedValue` (REST + reqreply; events Publish) | Same, for the produced-value direction |
| `TestSetContextFieldFromIn_EventsSubscribe_NoOutNeeded` (events only) | Confirms `SetContextFieldFromIn` works on events' Subscribe despite its `WithReceive` having no `Out` at all — the asymmetry-aware part of the design |
| `TestSetContextFieldFromOut_EventsSubscribe_DoesNotCompile` (events only, compile-time check) | Confirms (via a `//go:build` compile-fail test or similar) that `SetContextFieldFromOut` genuinely cannot be declared against events' `WithReceive` shape — Go's own type system catches it, not a runtime guard |
| `TestContextField_SharedAcrossStackedLayers` (×3) | A value `Set` by an EARLIER middleware layer is retrievable by a LATER layer or the handler — the "propagate between middleware handlers and the route/channel handler" requirement |

**Phase 4 (connection-level auth spec registration):**

| Test | Verifies |
|---|---|
| `TestClient_AddConnectSecurityScheme_StandaloneRegistration` (events) | A scheme registered via `AddConnectSecurityScheme`, referenced by ZERO channels, still appears in `components/securitySchemes` |
| `TestBuilder_AddConnectSecurityScheme_StandaloneRegistration` (reqreply) | Same, for `reqreply.Builder` |
| `TestClient_AddConnectSecurityScheme_LastRegisteredWins` (events + reqreply) | A channel/route re-registering the SAME scheme name still wins on collision — unchanged aggregation precedence, now with a 3rd contributor |
| `TestServer_Security_ReferencesConnectOnlyScheme_NoDanglingRef` (events + reqreply) | `AddServer`'s `Server.Security` referencing a connect-only scheme name resolves with the scheme present in `components/securitySchemes` — no dangling reference |
| `TestConnect_DropInMQTTClientForReqreply` (mqtt5, reqreply) | Confirms the plain `MQTTClient` returned by `mqtt5.Connect` works unmodified as the `MQTTClient` passed to reqreply's `Call`/`Serve` — zero adapter change needed (the PRIMARY mechanism) |
| `TestSecuredClient_DropInForReqreply` (mqtt5, reqreply) | Same, for `*mqtt5.SecuredClient` (the OPTIONAL secondary pre-check wrapper) — confirms it promotes transparently too |
| `TestConnectError_CarriesReasonCodeOnAuthRejection` (mqtt5) | A CONNACK reason code ≥ 0x80 (e.g. `0x86`/`0x87`) is captured into `ConnectError.ReasonCode`/`ReasonString`, not discarded — `errors.As` callers can branch on it directly |
| `TestConnectError_ZeroReasonCodeOnDialFailure` (mqtt5) | A "dial"-stage failure (no CONNACK ever received) leaves `ReasonCode`/`ReasonString` at their zero value — confirms the two failure modes stay distinguishable |
| `TestConnect_ObserverReportsSecurityRejectionOnAuthFailure` (mqtt5) | `ConnectOptions.Observer`, when set and implementing `stats.SecurityObserver`, receives `RecordSecurityRejection("connect", ...)` on an auth-rejection CONNACK — mirrors `NewSecuredClient`'s already-shipped pattern |

## Files to create/modify

| File | Responsibility | Phase |
|---|---|---|
| `adapters/nethttp/clienttransport.go` | **Prerequisite for Phase 2 (REST only)**: add `ClientMiddlewareHandlers` dispatch to `Call` AND `Consume` — mirroring `binding.go`/`client.go`'s existing `dispatchClientMiddlewareIn`/`Out` calls. Closes the confirmed, pre-existing gap blocking Security's migration AND SSE `Consume`'s client-side agnostic middleware support in one pass | Prereq |
| `middleware/layer.go` (new) | `Axis[T]`/`AxisVars[T]`, `DecodeLayer`/`EncodeLayer` — the new shared mechanism | 1 |
| `api/rest/transform.go` | Migrate `buildDecodeIn`/`buildEncodeIn`/`buildEncodeOut`/`buildDecodeOut` onto `middleware.DecodeLayer`/`EncodeLayer`; **REMOVE** `Transform`/`ClientTransform`/`TransformSSE`/`ClientTransformSSE` as separate free functions (Architecture revision) — ONLY after the test/example migration row below lands | 1 + Arch. revision |
| `adapters/nethttp/*_test.go`, `adapters/chi/*_test.go` (mirrored), `examples/error-types/main.go` | **Confirmed via code review**: ~45 real test call sites + 1 real example use `rest.Transform`/`TransformSSE`/`ClientTransformSSE` directly — mechanical rename sweep (`rest.Transform(route, mw, fn)` → `route.HandleMW(mw, fn)`, confirmed identical Fn signature) BEFORE the functions themselves are removed | Arch. revision |
| `api/events/transform.go` | Same migration, 2-axis (topic/property) | 1 |
| `api/reqreply/transform.go` | Same migration, 2-axis (topic/property), fully duplex like REST | 1 |
| `api/rest/middleware_declaration.go` | `SecurityMiddleware`'s generalized signature | 2 |
| `api/rest/middleware.go` | `HandleMW`/`ClientMW` become the ONLY attachment methods. **Confirmed mechanism** (see "Confirmed mechanical details" above): extend `routeMiddlewareContributor`/`applyAgnosticRoute` (today agnostic-only) with a new bound-case method for `*Req`-accessing dispatch; add `Satisfies []string` to `MiddlewareHandler`/`ClientMiddlewareHandler`; update `CheckCoverage` (and likely merge `rb.impls` with the `MiddlewareHandler`-populated field) to see the unified type's `Satisfies` | 2 + Arch. revision |
| `adapters/nethttp/adapter.go` | **RETIRE** `runSecurityMiddleware`'s separate code path — folded into `HandleMW`'s now-unified reflection dispatch | Arch. revision |
| `adapters/nethttp/binding.go`/`client.go` | **RETIRE** `mergeCredentialHeaders`'s separate code path — folded into `ClientMW`'s now-unified reflection dispatch | Arch. revision |
| `api/events/middleware_declaration.go`, `api/events/builder.go` | Same generalization for events | 2 — **SHIPPED (Rollout Phase B)** |
| `adapters/mqtt5/{transport.go,transport_dispatch.go}`, `adapters/zeromq`, `adapters/mqtt` | Replace fixed-shape Fn type-assertions with dispatch through the mw's own handler — EVENTS side only | 2 — **SHIPPED (Rollout Phase B)**, PLUS a confirmed follow-up gap found during Phase C review: the `GrantedScopes`-on-`Out` convention (Open design decision 7) was never actually wired into any of these 3 adapters' dispatch (zero `GrantedScopes` references in any of them) — no adapter merges a bound Security `MiddlewareHandler`'s `Out` into the `granted` map before `CheckScopes`. Needs a REST-style merge helper (mirrors `adapters/internal/httpsecurity.CollectGrantsReflect`/`MergeMiddlewareHandlerGrants`), planned for Phase C alongside reqreply's OWN build of the same mechanism (see below) |
| `api/reqreply/middleware_declaration.go`, `api/reqreply/route.go` | Same generalization for reqreply | 2 — NOT YET DONE |
| `adapters/mqtt5/reqreply_transport.go`, `adapters/zeromq` (reqreply-side files) | Replace fixed-shape Fn type-assertions with dispatch through the mw's own handler — REQREPLY side only; build the `GrantedScopes` merge-and-enforce wiring FROM SCRATCH here too (same gap as events' row above — do both in the SAME pass, do not repeat the omission a 3rd time). **`adapters/mqtt` (v3) has NO reqreply support at all — confirmed via code, no `reqreply.go`/`reqreply_transport.go` exists for it — do not add a row for it here** | 2 — NOT YET DONE |
| `codex/varfields.go` | Add `EncodeMergeVars[T]` (new, separate function — see "Phase 1 sub-item" above) — `EncodeVars` itself stays UNCHANGED | 1 |
| `api/rest/builder.go` | Add the FULL omit-empty constructor family (`NewOmitEmptyQueryParam`/`NewOmitEmptyCookieParam`/`NewOmitEmptyHeaderParam`/`NewOmitEmptyResponseHeaderParam`/`NewOmitEmptyResponseCookieParam` — see "API surface") alongside each location's existing `NewRequired*Param`/`NewOptional*Param` pair, all built on `codex.EncodeMergeVars` | 1 |
| `api/events/property_param.go`, `api/reqreply/property_param.go` | Add `NewOmitEmptyPropertyParam[T,V]` (mirrors `NewOptionalPropertyParam`'s shape) — no topic-param equivalent (topics have no Required/Optional split to begin with) | 1 |
| `examples/go-edge-models/app/registry/auth.go` | Migrate `newAuthCredentialFunc`/`BearerAuthDeclaration` onto the new pattern — the motivating real case (REST), confirmed fully migratable via the agnostic `.Use(mw.WithSend(...))` style; DEPENDS on `NewOmitEmptyHeaderParam` (above) for the anonymous-access (empty-token) case — see the worked example in "The pivot, concretely" | 2 |
| `docs/features/security.md` | Full rewrite of the credential-Fn sections to show the new declarative pattern, for all 3 packages | 2 |
| `docs/design/d-0003-codec-declared-middlewares.md` | Add a new Addendum documenting `middleware.DecodeLayer`/`EncodeLayer` as the architectural embodiment of "middleware is a partial route/channel definition, stacked" — the mechanism itself is now the documentation, not just prose describing a convention | 1 |
| `middleware/context_field.go` | Add the NEW `ContextFieldSetter` interface (confirmed REQUIRED, not optional — the original `ContextField[V]`-typed sketch does not compile, see "Proposed design" above); add `SetContextFieldFromIn`/`SetContextFieldFromOut` declarative link methods to `Middleware[In,Out]`, typed against `ContextFieldSetter`; extend `EnsureContextFields` call sites | 3 |
| `adapters/nethttp/clienttransport.go` | Add `EnsureContextFields` + `SetContextFieldFromIn`/`Out` dispatch to the CLIENT side (currently server-only) | 3 |
| `adapters/mqtt5/transport.go`, `adapters/zeromq`, `adapters/mqtt` | Greenfield `ContextField`/`SetContextFieldFromIn`/`Out` integration — EVENTS side only | 3 — **SHIPPED (Rollout Phase B)** |
| `adapters/mqtt5/reqreply_transport.go`, `adapters/zeromq` (reqreply-side files) | Greenfield `ContextField`/`SetContextFieldFromIn`/`Out` integration — REQREPLY side only; confirmed zero existing usage in reqreply today. **`adapters/mqtt` (v3) has no reqreply support — no row here**, same as the Phase 2 row above | 3 — NOT YET DONE |
| `api/events/builder.go` | Add `Client.AddConnectSecurityScheme` | 4 |
| `api/reqreply/builder.go` | Add `Builder.AddConnectSecurityScheme` | 4 |
| `adapters/mqtt5/errors.go` | Extend `ConnectError` with `ReasonCode byte`/`ReasonString string`, populated from the `*Connack` paho already returns on a "connect"-stage failure (today discarded) | 4 |
| `adapters/mqtt5/connect.go` | Add an optional `Observer` field to `ConnectOptions`; report `stats.SecurityObserver.RecordSecurityRejection("connect", ...)` on an auth-rejection CONNACK | 4 |

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

All 10 items below were open across this doc's various drafts (items
1–9 from earlier rounds, item 10 added this round). Each is now
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
2. **`sparseFieldCodec[T]` support for merge fields — RESOLVED: a NEW,
   separate `codex.EncodeMergeVars` function, NOT an extension of
   `EncodeVars` (corrected during Phase A's design review — the
   original "extend `EncodeVars`" resolution was factually wrong,
   confirmed by actually attempting it: breaks 2 existing tests and
   contradicts `docs/concepts/codec.md`'s documented path/topic/
   dotted-key safety rationale).** `EncodeMergeVars` checks for the
   existing `sparseFieldCodec[T]` companion capability, mirroring
   `Struct`'s own `Encode` loop (`codex/object.go:150`) exactly — zero
   new codec concept, reuses `OmitEmptyField`/`MaybeField` as-is.
   `EncodeVars` itself is explicitly UNCHANGED and UNTOUCHED. Small,
   additive, not a hard prerequisite for `DecodeLayer`/`EncodeLayer`'s
   main work but bundled into the same phase. See "Phase 1 sub-item"
   above for the full corrected resolution.
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
10. **Exact reflection-based signature-detection mechanics for the
    now-unified `HandleMW`/`ClientMW` (Architecture revision) —
    RESOLVED: mostly REUSE, not novel.** The bound-vs-agnostic detection
    `HandleMW`/`ClientMW` need is the SAME `Agnostic` bool-detection
    logic `MiddlewareHandler`/`ClientMiddlewareHandler` already
    implement today (confirmed via `api/rest/transform.go`) — this
    revision gives it ONE entry point instead of splitting it across
    `Transform` vs `HandleMW`, it does not require inventing a new
    detection mechanism. Flagged as low-risk, not a genuinely open
    question, but worth stating explicitly for the implementation
    round (Rollout Phase A).
11. **`GrantedScopes`-on-`Out` enforcement wiring for events — found
    missing during a Phase C review, RESOLVED: a SEPARATE, standalone
    pre-Phase-C bugfix patch, not folded into Phase C.** Confirmed via
    code: `adapters/internal/httpsecurity`'s `CollectGrantsReflect`/
    `MergeMiddlewareHandlerGrants` (REST's fully-wired reference
    implementation — merges a bound Security `MiddlewareHandler`'s
    decoded `Out.GrantedScopes` into the SAME `granted` map the legacy
    credential-Fn path populates, then ONE `CheckScopes` call) has NO
    events equivalent — zero `GrantedScopes` references anywhere in
    `adapters/mqtt5`/`mqtt`/`zeromq` today, confirmed via grep.
    `events.SecurityMiddleware[In,Out]`'s own doc comment incorrectly
    claims this is "confirmed via adapters' existing CheckScopes
    integration" — that integration
    (`runSubscribeSecurityImpls`/`runPublishSecurityImpls`) is the OLD,
    legacy-Fn-only path; it never merges with the NEW bound dispatch's
    `Out` values. This went undetected because the only new Phase B
    test touching it
    (`TestSecurityMiddleware_RealInOutType_DoesNotPanicOnDispatch`)
    only calls `DecodeIn`/`EncodeOut` directly — it never reaches an
    adapter's actual dispatch/`CheckScopes` code — and the real
    examples/tests exclusively use the OLD legacy Fn shape (which
    IS correctly wired), never the new bound shape with a real
    `GrantedScopes`-carrying `Out`. Tracked as its own schedulable unit
    of work, BEFORE Phase C, so it is not lost (see this doc's status
    header).
12. **Events' Subscribe-side `Out`/scope-granularity limitation — found
    during the SAME Phase C review, RESOLVED: widen Subscribe's bound
    Fn shape, ADDITIVELY (non-breaking), bundled into item 11's SAME
    pre-Phase-C patch.** Confirmed via code: events' Subscribe bound Fn
    shape is `func(ctx, *T, In) error` (1 return value, no `Out` slot
    at all — confirmed via `isBoundSubscribeMWShape`'s `t.NumOut() != 1`
    check and `MiddlewareHandler.Fn`'s own doc comment), mirroring
    Subscribe's real business-handler shape (`func(ctx, T) error` —
    pub/sub has no reply to a subscribe, so no `Out` exists there by
    design, not a bug). This means `GrantedScopes` is not merely
    unwired on Subscribe (item 11's gap) — it is STRUCTURALLY
    IMPOSSIBLE to express through the bound mechanism at all, for
    EITHER direction, until the shape itself changes. **Resolution**:
    add a NEW, separately-detected `func(ctx, *T, In) (Out, error)`
    (2-return) bound shape for Subscribe, ALONGSIDE the EXISTING
    1-return shape (which keeps working completely unchanged for
    general-purpose Subscribe middleware — logging, rate-limiting,
    enrichment — that has nothing meaningful to return as `Out`).
    Detected by return-arity, mirroring this codebase's existing
    shape-detection idiom. When the 2-return shape is used,
    `DispatchSubscribeMiddlewareHandlers` calls `mw.OutCodec.Validate
    (out)` (mirrors `EncodeOut`'s existing validation step for
    Publish) then makes `out` available to item 11's merge step — NO
    wire-encoding is needed for Subscribe's `Out` (there is no
    outgoing message to encode into; `Out` exists ONLY to carry
    metadata like `GrantedScopes` through to the merge-and-enforce
    step). Bundled into item 11's SAME patch because both are the
    SAME underlying mechanism, now covering both directions — not a
    3rd separate unit of work. `api/reqreply` does NOT inherit this
    limitation (confirmed via code: its receiving/Serve-side bound
    `Fn` already returns `(Out, error)`, since reqreply is always
    request/REPLY, never fire-and-forget) — Phase C's own reqreply
    work builds its OWN merge-and-enforce wiring afterward, reusing
    whatever helper shape item 11's patch establishes where it fits.
