# ReqReply Codec-Declared Middleware — bringing `api/reqreply` up to D-0003 parity

> **Status:** Design draft — not yet implemented. Refined across 2
> follow-up rounds. Round 1, all 3 originally-open design decisions
> RESOLVED (see "Open design decisions"): (1) `ClientTransform`'s shape
> confirmed against `api/rest/transform.go`'s real signatures; (2)
> naming (`reqreply.Middleware[In,Out]` alongside `middleware.
> Middleware`) confirmed safe, zero collision; (3) a NEW protocol-neutral
> **"property" vocabulary axis** (`WithRequestProperty`/
> `WithResponseProperty`) added after confirming MQTT5 User Properties
> and AMQP message headers are the SAME cross-transport concept — a
> genuine companion change for `api/events` too (see "Companion change"
> below). **Round 2 — re-verified against REST's ACTUAL implementation
> code (not just its public signatures) and found/fixed 4 issues**: (a)
> a design ERROR — `DecodeIn`/`EncodeOut`/etc. do NOT combine topic +
> property vars into one map, they take/return SEPARATE maps per axis,
> mirroring REST's real multi-map signatures exactly (see "The
> 'property' vocabulary axis"); (b) dispatch ORDER now stated explicitly
> — `Transform`'s declared middleware runs AFTER the paired security Fn,
> confirmed via REST's real dispatch code; (c) a missing AsyncAPI
> spec-rendering integration, now added (see "AsyncAPI spec rendering");
> (d) merge-field name conflict detection now resolved — follows REST's
> stricter `ConflictingParamContributionError` precedent, a deliberate,
> accepted divergence from Phase 1b's laxer silent-dedupe behavior (see
> "AsyncAPI spec rendering" and "Open design decisions"). **Round 3 —
> deep-compared the new `PropertyParam`/`MergedPropertyParam[T]`/
> `NewPropertyParam[T,V]` sketch against reqreply's OWN real, shipped
> `TopicParam`/`MergedTopicParam[Req]`/`NewTopicParam`
> (`api/reqreply/route.go`), which it claims to mirror exactly, and
> found/fixed 4 parity gaps**: (a) a struct-shape BUG —
> `MergedPropertyParam[T]` now correctly wraps `codex.MergedParam[T]`
> directly (single embed, matching `MergedTopicParam[Req]`'s real
> shape) instead of a disconnected, never-populated separate `Field`
> field; (b) added the missing `WithDescription` method (the only way
> to set one, since `NewPropertyParam` takes no description param,
> mirroring `MergedTopicParam.WithDescription`); (c) added the missing
> `WithCodec` escape-hatch method on plain `PropertyParam` (mirroring
> `TopicParam.WithCodec`); (d) added the missing internal
> `toParam`/`applyRoute` route-builder wiring pair (routes into a NEW
> `rb.propertyParams []PropertyParam` slice, parallel to the existing
> `topicParams` slice). **Round 4 — full end-to-end re-read (including
> Observer integration, previously under-reviewed) found/fixed 2 bugs
> and resolved 1 new open decision**: (a) a self-contradiction leftover
> from BEFORE the Round 2 fix — a paragraph near the
> `RouteHandle`/`MiddlewareHandler` sketch still said `DecodeIn` uses
> "COMBINED" topic+property vars, now corrected to state two SEPARATE
> map parameters; (b) matching ambiguous "COMBINED" wording in the unit
> test plan, reworded for clarity; (c) a genuine gap — the Observer
> integration section never specified which adapter `ErrorKind` wraps
> the new mechanism's own errors; RESOLVED (decision #6): `KindDecode`/
> `KindEncode` for merge-side failures (natural extension of existing
> Phase 1b precedent), and a NEW `KindMiddleware` value added to both
> `mqtt5.ErrorKind` and `zeromq.ErrorKind` for `MiddlewareError` (D2's
> fn-business-error fallback), distinguishing a declared-middleware
> failure from a real handler failure. One decision remains genuinely
> open — decision #4 (shared vs. duplicated `PropertyParam` location),
> deferred until the `api/events` companion is scoped.
> [← Back to Roadmap](index.md)

## Motivation

[D-0003 — Codec-Declared Middlewares](../design/d-0003-codec-declared-middlewares.md)
gave `api/rest` and `api/events` a SECOND, additive middleware mechanism —
`middleware.Declaration[In,Out]` + a per-pattern `Middleware[In,Out]` type
(`rest.Middleware[In,Out]`/`events.Middleware[In,Out]`) — on top of the
older, still-fully-functional flat `middleware.Middleware` (`.Use()`/
`HandleMW`/`ClientMW`, security-only) mechanism. The new mechanism lets a
middleware value carry its OWN codec-backed Input/Output shape, exactly
like a route/channel declares its own `Req`/`Resp` — enabling declarative
param merge (REST: header/cookie/query, both directions; events: topic
vars) for concerns that are NOT security (enrichment, derived data,
response-attribute policy), attached via `Transform`/`ClientTransform`
(route/channel-BOUND, `fn` gets `req`/`msg` access) or plain `.Use(mw)`
with a bundled `WithReceive`/`WithSend` Fn (route/channel-AGNOSTIC,
reusable verbatim across many routes/channels).

`api/reqreply` never got this second mechanism — not because of a
deliberate exclusion, but PURELY CHRONOLOGICAL: D-0003 was designed and
shipped BEFORE [D-0004 — ReqReply Workflow Simplification](../design/d-0004-reqreply-workflow-simplification.md)
(which gave reqreply its `Server`/`Client`/`Attach` rework) and BEFORE
[ReqReply Middleware](reqreply-middleware.md)'s Phase 1/1b (which gave
reqreply its OWN flat `.Use()`/`HandleMW`/`ClientMW` split, mqtt5 AND
zeromq). D-0003 could only build on REST/events' EXISTING baselines at
the time it was written — reqreply had no baseline yet to extend. Today,
reqreply's flat mechanism has reached the SAME maturity point REST's
flat mechanism was at immediately BEFORE D-0003 shipped: a working
security declare/implement split, but no codec-backed, non-security
enrichment/merge mechanism alongside it.

This doc designs that mechanism for `api/reqreply` — `reqreply.
Middleware[In,Out]` + `Transform`/`ClientTransform` — entirely ADDITIVE
to Phase 1/1b's existing flat mechanism (which stays completely
unchanged, exactly as `middleware.Middleware`/`SecurityScheme` remained
unchanged when D-0003 shipped for REST/events).

## Scope decisions (what's in this doc, what's deferred)

| In scope | Out of scope |
|---|---|
| `reqreply.Middleware[In,Out]` embedding `middleware.Declaration[In,Out]` | Retrofitting/changing the existing flat `middleware.Middleware`/`Route.Use`/`HandleMW`/`ClientMW` security mechanism — stays exactly as shipped |
| Topic-var merge vocabulary (`WithRequestTopic`/`WithResponseTopic`, reusing the EXISTING `NewTopicParam`/`MergedTopicParam[Req]` constructor — no new param type) | — |
| **Property merge vocabulary** (`WithRequestProperty`/`WithResponseProperty`, via a NEW `PropertyParam`/`MergedPropertyParam[T]`/`NewPropertyParam[T,V]` triple — a protocol-neutral "named metadata separate from payload" concept realized differently per adapter: MQTT5 User Properties, future AMQP native message headers) | Building the ADAPTER-side wire mechanism itself (e.g. actually reading/writing MQTT5 User Properties for this NEW axis) — that's `adapters/mqtt5`'s own implementation work, sequenced AFTER this doc's core-type design ships |
| `Transform`/`ClientTransform` — route-BOUND attachment, `fn` gets `req *Req`/`req Req` access, mirroring REST's EXACT request/response-symmetric shape (confirmed against `api/rest/transform.go`'s real signatures — see "API surface") | A `zeromq`-specific extension — this doc's mechanism is fully transport-agnostic (topic vars AND properties are both just named `map[string]string` merges under the hood), so it should work identically for mqtt5 AND zeromq with ZERO core-type adapter-specific work; a route declaring a REQUIRED property on an adapter with no property mechanism (zeromq, mqtt v3) naturally surfaces a validation error there, no special-casing needed |
| Channel-AGNOSTIC attachment via bundled `WithReceive`/`WithSend` + plain `.Use(mw)` | `mqtt`(v3) — N/A, no reqreply support exists there at all (protocol limitation, unchanged) |
| D6(b)/D7-equivalent checks (duplicate middleware name, ambiguous dual-attachment) | A `ports.Middleware[In,Out]` extension — D-0003's own "Feasibility" section already covers that as a SEPARATE future decision, unrelated to reqreply |
| Noting `api/events`' IDENTICAL gap as a companion follow-up (see "Companion change" below) | Actually IMPLEMENTING the `api/events` companion change in this same round — smaller, mostly mechanical once this doc's design is locked, but a separate PR/session |

## Toolchain / dependency decisions

None — this is a pure `api/reqreply` + adapter (`adapters/mqtt5`,
`adapters/zeromq`) addition, reusing `middleware.Declaration[In,Out]`
(already shipped, package `middleware`) and `codex.FieldCodec[T]`/
`codex.DecodeVars`/`codex.EncodeVars` (already shipped, package `codex`)
exactly as REST/events already do. No new external dependency.

## API surface

Mirrors `rest.Middleware[In,Out]`/`Transform`/`ClientTransform` in SHAPE
(single `Route` carries BOTH request and reply directions, unlike
events' split Subscriber/Publisher) — **confirmed against
`api/rest/transform.go`'s ACTUAL signatures this round, resolving what
would otherwise have been an open design decision**: REST's `Transform`
fn is `func(ctx, req *Req, in In) (Out, error)` (decodes `In` from
REQUEST-side vars, fn enriches `*Req` AND produces `Out`, which encodes
into RESPONSE-side vars) — REST's OWN `Transform` already spans BOTH the
request and response directions in one call, because a single HTTP
round-trip has both. reqreply's request/reply round-trip is the
STRUCTURALLY IDENTICAL shape (one `Serve` invocation sees both the
decoded request AND produces the encoded reply) — so `reqreply.Transform`
mirrors `rest.Transform` byte-for-byte, no new shape needed. Likewise
`rest.ClientTransform`'s fn is `func(ctx, req Req) (In, error)` — PRODUCES
`In` to encode into the OUTGOING request; `Out` is then decoded
MECHANICALLY (no Fn) from the actual reply's vars, via a `DecodeOut`
closure on `ClientMiddlewareHandler`, exactly mirroring `rest.
ClientMiddlewareHandler.DecodeOut`'s identical "no Fn, no reply-inspection
Fn needed" design. reqreply's VOCABULARY (which var buckets exist to
merge into/from) mirrors events' topic vars for topic-derived data, PLUS
a NEW, protocol-neutral **property** axis (resolved this round — see
below) — NOT REST's richer header/cookie/query surface, since reqreply's
wire boundary is fundamentally "one topic template plus optional
protocol-native metadata," not HTTP's multi-part request shape.

### The "property" vocabulary axis — resolved this round

MQTT5 User Properties and (per [AMQP 0.9.1 Adapter](amqp-adapter.md)'s
own roadmap) AMQP's native message headers are THE SAME cross-transport
CONCEPT: named metadata carried separately from the payload, wire-
realized differently per protocol. This is NOT an MQTT5-specific idea —
confirmed via code that `codex.Param`/`MergedParam[T]`/`NewParam[T,V]`
(`codex/param.go`) are the SHARED primitives `TopicParam`/`HeaderParam`/
`CookieParam`/`QueryParam` ALL already wrap — a NEW `PropertyParam`/
`MergedPropertyParam[T]`/`NewPropertyParam[T,V]` triple can mirror
`TopicParam` exactly (same wrapper shape), MINUS the "must appear in the
topic template" validation topic vars require (a property name is
looked up in an adapter-supplied map, not parsed from the topic string).
`codex.DecodeVars`/`EncodeVars` already operate on ANY generic
`map[string]string` regardless of provenance — no core merge-mechanism
change needed, only a NEW named merge-field slice + constructor,
mirroring topic vars' existing shape.

**Round 3 correction**: an earlier revision of this sketch gave
`MergedPropertyParam[T]` its OWN separate `Field codex.FieldCodec[T]`
field alongside embedding `PropertyParam`. That doesn't actually mirror
`MergedTopicParam[Req]` — confirmed via `api/reqreply/route.go`'s real
definition, `type MergedTopicParam[Req any] struct { codex.MergedParam
[Req] }`, a SINGLE embed. `codex.MergedParam[T]` already carries BOTH
`Param` and `Field FieldCodec[T]` internally (`codex/param.go`), so a
separate `Field` on `MergedPropertyParam[T]` would be disconnected from
`codex.NewParam`'s real merge machinery — dead weight, never populated.
Corrected below to the real one-embed shape, and the sketch now also
includes the methods `TopicParam`/`MergedTopicParam[Req]` actually have
that were missing before: `WithCodec` (plain `PropertyParam`'s
escape-hatch, since `PropertyParam{Name: ..., Description: ...}` struct
literals need a way to attach a codec afterward), `WithDescription`
(`MergedPropertyParam[T]`'s only way to add a description, since
`NewPropertyParam` takes none), and the internal `toParam`/`applyRoute`
route-builder wiring pair mirroring `TopicParam`/`MergedTopicParam[Req]`
exactly (routed into a NEW `rb.propertyParams []PropertyParam` slice on
`routeBuilder`, parallel to its existing `topicParams` slice):

```go
// PropertyParam describes a named piece of protocol-native metadata
// (MQTT5 User Property, future AMQP message header, ...) — the
// validate-only escape hatch, mirrors [TopicParam] exactly but with NO
// "must appear in the topic template" check (there is no template to
// check against). Wraps [codex.Param] directly — same shared primitive
// TopicParam/HeaderParam/CookieParam/QueryParam already use.
type PropertyParam struct {
    codex.Param
}

// WithCodec attaches a codec to p and returns the updated value —
// mirrors [TopicParam.WithCodec] exactly; the only way to add runtime
// validation to a PropertyParam built via a bare struct literal.
func (p PropertyParam) WithCodec(c codex.Codec[string]) PropertyParam { p.Codec = &c; return p }

// applyRoute wires p into rb's property-param list — mirrors
// [TopicParam.applyRoute]'s routeBuilder-option pattern.
func (p PropertyParam) applyRoute(rb *routeBuilder) {
    rb.propertyParams = append(rb.propertyParams, p)
}

// toParam converts p to the shared codex.Param used by the generic
// decode/encode/spec-rendering machinery — mirrors [TopicParam.toParam].
func (p PropertyParam) toParam() codex.Param { return p.Param }

// MergedPropertyParam[T] additionally merges this property's value into
// T — mirrors [MergedTopicParam][T] exactly: a SINGLE embed of
// [codex.MergedParam][T], which already carries both Param and the
// merge-capable Field internally (no separate Field on this type).
type MergedPropertyParam[T any] struct {
    codex.MergedParam[T]
}

// WithDescription sets the PARAMETER-level description and returns the
// updated value — mirrors [MergedTopicParam.WithDescription] exactly;
// the only way to add one, since NewPropertyParam takes no description
// parameter.
func (p MergedPropertyParam[T]) WithDescription(desc string) MergedPropertyParam[T] {
    p.MergedParam = p.MergedParam.WithDescription(desc)
    return p
}

// applyRoute wires p into rb's property-param list — mirrors
// [MergedTopicParam.applyRoute].
func (p MergedPropertyParam[T]) applyRoute(rb *routeBuilder) {
    rb.propertyParams = append(rb.propertyParams, PropertyParam{Param: p.Param})
}

// NewPropertyParam declares a property that is BOTH validated AND
// merge-capable into T — mirrors [NewTopicParam][T,V] exactly, wraps
// [codex.NewParam][T,V] directly.
func NewPropertyParam[T, V any](
    name string,
    codec codex.Codec[V],
    get func(T) V,
    set func(*T, V),
) MergedPropertyParam[T] {
    return MergedPropertyParam[T]{MergedParam: codex.NewParam(name, codec, get, set)}
}
```

`Middleware[In,Out]` gains a SECOND pair of merge-field slices, kept
SEPARATE from `topicMergeFieldsIn`/`topicMergeFieldsOut` (different
validation rules — property names are never checked against a topic
template):

```go
type Middleware[In, Out any] struct {
    middleware.Declaration[In, Out]

    topicMergeFieldsIn     []codex.FieldCodec[In]
    topicMergeFieldsOut    []codex.FieldCodec[Out]
    propertyMergeFieldsIn  []codex.FieldCodec[In]
    propertyMergeFieldsOut []codex.FieldCodec[Out]

    receiveFn func(ctx context.Context, in In) (Out, error)
    sendFn    func(ctx context.Context) (In, error)
}

// WithRequestProperty registers one REQUEST-side property merge field
// into mw's own In vocabulary — mirrors WithRequestTopic exactly, using
// NewPropertyParam instead of NewTopicParam.
func (m Middleware[In, Out]) WithRequestProperty(p MergedPropertyParam[In]) Middleware[In, Out]

// WithResponseProperty is WithRequestProperty's REPLY-side sibling.
func (m Middleware[In, Out]) WithResponseProperty(p MergedPropertyParam[Out]) Middleware[In, Out]
```

**Dispatch-side consequence — CORRECTED this round against REST's
ACTUAL implementation, not just its public signatures**: an earlier
revision of this doc claimed topic vars and property vars get COMBINED
into one map before decoding. That is WRONG — confirmed via
`api/rest/transform.go`'s real `buildDecodeIn`: REST's `MiddlewareHandler.
DecodeIn` takes THREE SEPARATE map parameters
(`func(headerVars, cookieVars, queryVars map[string]string) (any, error)`),
and its body calls `codex.DecodeVars` ONCE PER AXIS, SEQUENTIALLY,
against the SAME target value — never merges the maps themselves. Each
call only touches the fields THAT AXIS declared (via that axis's own
`FieldCodec[In]` slice), so calling `DecodeVars` multiple times against
one `&in` is safe and side-effect-free across axes. reqreply mirrors
this EXACTLY, with its own two axes:

```go
// buildDecodeIn — mirrors rest's identical function, adapted to
// reqreply's two axes (topic, property) instead of REST's three
// (header, cookie, query).
func buildDecodeIn[In, Out any](mw Middleware[In, Out]) func(topicVars, propertyVars map[string]string) (any, error) {
    return func(topicVars, propertyVars map[string]string) (any, error) {
        var in In
        if len(mw.topicMergeFieldsIn) > 0 {
            if err := codex.DecodeVars(&in, topicVars, mw.topicMergeFieldsIn...); err != nil {
                return nil, MiddlewareInputError{Name: mw.Name, Err: err}
            }
        }
        if len(mw.propertyMergeFieldsIn) > 0 {
            if err := codex.DecodeVars(&in, propertyVars, mw.propertyMergeFieldsIn...); err != nil {
                return nil, MiddlewareInputError{Name: mw.Name, Err: err}
            }
        }
        if err := mw.InCodec.Validate(in); err != nil {
            return nil, MiddlewareInputError{Name: mw.Name, Err: err}
        }
        return in, nil
    }
}
```

`ClientMiddlewareHandler.EncodeIn`/`DecodeOut` mirror this same
per-axis-separate-map shape, confirmed against REST's real signatures:
`EncodeIn func(in any) (topicVars, propertyVars map[string]string, err error)`,
`DecodeOut func(topicVars, propertyVars map[string]string) (any, error)`.

`codex.DecodeVars`/`EncodeVars` themselves need NO change (already
provenance-agnostic about where a `map[string]string` comes from). The
ADAPTER is responsible for supplying the property-value map however it
can: `adapters/mqtt5` builds it from the real message's User Properties
(the SAME extraction `UserPropertyParam`/`FromUserPropertyParam` already
do for the flat mechanism); a future `adapters/amqp` would build it from
the message's native headers property; `adapters/zeromq`/`mqtt`(v3)
simply pass an empty map — a route declaring a REQUIRED property on one
of those adapters then naturally fails with the SAME
`MiddlewareInputError` a missing topic var would produce, no
adapter-specific error type or special-casing needed anywhere in the
core dispatch logic. `topicVars` is unaffected by this correction —
supplied exactly as it already is for the existing `WithRequestTopic`/
`WithResponseTopic` axis.

```go
package reqreply

// Middleware is a codec-backed, reqreply-specific middleware declaration
// — the per-pattern counterpart to [middleware.Declaration], adding
// reqreply's own topic-var AND property merge vocabularies (see "The
// 'property' vocabulary axis" above for the full Middleware[In,Out]
// struct sketch — repeated here is only the method surface). Unlike
// events (whose Subscribe/Publish roles are asymmetric — only one of
// In/Out is ever used per role), reqreply's SINGLE Route sees BOTH
// directions in one round-trip (mirrors REST's Route exactly) — so BOTH
// the In-side AND Out-side merge fields (topic AND property) are used
// together, on the SAME Middleware value, exactly as REST's request/
// response pair already works.

// NewMiddleware builds a Middleware from a middleware.Declaration.
func NewMiddleware[In, Out any](decl middleware.Declaration[In, Out]) Middleware[In, Out]

// WithRequestTopic registers one REQUEST-side topic-var merge field into
// mw's own In vocabulary — reuses the EXISTING NewTopicParam[In]
// constructor directly (no new reqreply-side param type). Only
// meaningful for a topic var the route's OWN template declares but that
// the route's OWN Req does NOT already merge via its own NewTopicParam.
func (m Middleware[In, Out]) WithRequestTopic(p MergedTopicParam[In]) Middleware[In, Out]

// WithResponseTopic is WithRequestTopic's REPLY-side sibling — registers
// one topic-var merge field into mw's own Out vocabulary, encoded into
// the reply's topic vars once Transform's fn (or a bundled WithReceive)
// produces an Out value, OR decoded from the reply's topic vars on the
// client side (mechanical, no Fn — mirrors rest.ClientMiddlewareHandler.
// DecodeOut exactly).
func (m Middleware[In, Out]) WithResponseTopic(p MergedTopicParam[Out]) Middleware[In, Out]

// WithRequestProperty/WithResponseProperty are WithRequestTopic/
// WithResponseTopic's property-axis siblings — see "The 'property'
// vocabulary axis" above for the full rationale and signatures.

// WithReceive/WithSend bundle a route-AGNOSTIC Fn directly onto mw,
// enabling plain .Use(mw) attachment (reusable verbatim across routes) —
// mirrors rest.Middleware's identical SERVER/CLIENT split (WithReceive:
// server-side, produces Out from In; WithSend: client-side, produces In
// to encode into the outgoing request — mirrors [Transform]/
// [ClientTransform]'s own fn shapes with the *Req/Req parameter dropped).
func (m Middleware[In, Out]) WithReceive(fn func(ctx context.Context, in In) (Out, error)) Middleware[In, Out]
func (m Middleware[In, Out]) WithSend(fn func(ctx context.Context) (In, error)) Middleware[In, Out]

func (Middleware[In, Out]) RouteMiddlewareMarker() {}

// Transform attaches mw's declaration AND its runtime fn to r in ONE
// call — the route-BOUND, SERVER-side attachment point, mirrors
// rest.Transform's EXACT shape. fn receives ctx, the route's OWN
// already-decoded *Req (POINTER — fn may read AND enrich it with derived
// data the wire request never carried), and mw's own decoded+validated
// In (declaratively extracted from REQUEST-side topic vars the route's
// Req does NOT model) — fn PRODUCES mw's own Out value, which mw's OWN
// reply-topic merge fields then encode into the REPLY's topic vars.
// Dispatches AFTER the paired security Fn (if any), both still
// pre-handler — confirmed via adapters/nethttp/serve.go's real dispatch
// order: security runs FIRST (runSecurityMiddlewareReflect), THEN
// declared MiddlewareHandlers run SECOND (runMiddlewareHandlersReflect)
// — mirrors D1's REST/events precedent exactly, same explicit order for
// reqreply's own adapters.
func Transform[Req, Resp, In, Out any](
    r Route[Req, Resp],
    mw Middleware[In, Out],
    fn func(ctx context.Context, req *Req, in In) (Out, error),
) Route[Req, Resp]

// ClientTransform is Transform's route-BOUND, CLIENT-side counterpart —
// mirrors rest.ClientTransform's EXACT shape. fn PRODUCES mw's own In
// value from req (the caller's OWN already-built value, VALUE not
// pointer — the caller already owns and can mutate its own Req directly
// before calling Call at all), encoded into the OUTGOING request's topic
// vars via mw's own request-topic merge fields. AFTER the reply arrives,
// mw's own reply-topic merge fields MECHANICALLY decode Out from the
// reply's actual topic vars — no Fn needed for this half, mirrors
// rest.ClientMiddlewareHandler.DecodeOut exactly.
func ClientTransform[Req, Resp, In, Out any](
    r Route[Req, Resp],
    mw Middleware[In, Out],
    fn func(ctx context.Context, req Req) (In, error),
) Route[Req, Resp]
```

`RouteHandle` gains two new fields, mirroring `ChannelHandle.
MiddlewareHandlers`/`ClientMiddlewareHandlers` exactly:

```go
type RouteHandle[Req, Resp any] struct {
    // ... existing fields unchanged ...
    MiddlewareHandlers       []MiddlewareHandler
    ClientMiddlewareHandlers []ClientMiddlewareHandler
}
```

`MiddlewareHandler`/`ClientMiddlewareHandler` mirror `rest`'s identical
type-erased runtime dispatch units — `MiddlewareHandler{Name, DecodeIn,
Fn any, Agnostic, dualAttached}` (server-side: `DecodeIn` takes request
topic vars AND adapter-supplied property vars as TWO SEPARATE map
parameters — never combined into one map, per the correction above —
`Fn` produces `Out` for the reply); `ClientMiddlewareHandler{Name, Fn any,
EncodeIn, DecodeOut, Agnostic, dualAttached}` (client-side: `Fn` produces
`In`, `EncodeIn` merges it into the outgoing request's topic AND
property vars (as SEPARATE map return values, mirroring REST's own
`EncodeIn func(in any) (headers, cookies, query map[string]string, err
error)` shape exactly), `DecodeOut` mechanically decodes `Out` from the
reply's topic AND property vars (again as SEPARATE map PARAMETERS, never
one combined map)) — see `api/rest/transform.go` for the exact shape
this doc's implementation ports (closer structural match than events',
since reqreply's `RouteHandle` — like REST's — carries both directions
on one type). See "The 'property' vocabulary axis" above (specifically
its `buildDecodeIn` sketch) for exactly how the adapter supplies the
property-value map, kept SEPARATE from the topic-var map throughout.

## AsyncAPI spec rendering

**Confirmed real gap in an earlier revision of this doc — now
addressed.** REST's D-0003 mechanism doesn't just drive runtime dispatch
— `Transform`/`ClientTransform` ALSO layer their declared merge-field
params into the OpenAPI spec, via `middlewareSpecContribution`/
`boundSpecContributionOf` (`api/rest/transform.go`), fed into the SAME
`applyParamDeclarations` conflict-detection/layering pass the OLDER flat
mechanism's params already use (D4 — see `docs/design/
d-0003-codec-declared-middlewares.md`). reqreply needs the SAME
integration, for TWO reasons: (1) parity with REST's own precedent, and
(2) parity with reqreply's OWN Phase 1b, which ALREADY renders
header-as-middleware declarations (the flat mechanism's `.Use(mqtt5.
FromUserPropertyParam(...))`) into the request/reply message's AsyncAPI
`headers` schema (`api/reqreply/middleware.go`'s existing
`applyParamDeclarations`/`headerParamProperty`). Leaving THIS NEW
mechanism's property axis un-rendered would be a real, visible
regression relative to what Phase 1b already ships.

Concretely: `Transform`/`ClientTransform` need to feed a
`reqreplyMiddlewareSpecContribution` (mirrors REST's
`middlewareSpecContribution` exactly — `Name`, the declared
`PropertyParam`/`TopicParam` values in spec-only form, `dualAttached`
for D7) into a NEW step inside `Route.Register`, unified with Phase
1b's EXISTING `applyParamDeclarations`/`headerParamProperty` code path
— NOT a separate, second AsyncAPI-rendering mechanism living alongside
it. Concretely, the property axis's contributions should be
DEDUPED/MERGED into the SAME `reqHeaders`/`respHeaders` `schema.Schema`
values Phase 1b's `applyParamDeclarations` already builds and passes to
`Builder.registerRoute` — topic-var contributions render via the
EXISTING topic-param spec path (`buildTopicParameters`,
`Builder.registerRoute`'s own `Parameters` map), unchanged, since
`WithRequestTopic`/`WithResponseTopic` don't introduce any NEW spec
surface beyond what `NewTopicParam` already renders. Only the property
axis needs this new unification work.

**Conflict detection (decision #4, RESOLVED this round)**: when TWO
different `Middleware[In,Out]` values (or a `Middleware[In,Out]`
contribution AND Phase 1b's flat `.Use(mqtt5.FromUserPropertyParam(...))`
contribution) declare the SAME property name with DIFFERENT attributes
(e.g. one says `Required: true`, the other `Required: false`, or
different codecs), this should ERROR — mirroring REST's own
`checkParamConflicts` behavior, NOT reqreply's OWN Phase 1b precedent
(which silently dedupes first-seen-wins, no mismatch check at all).
This is a DELIBERATE, ACCEPTED divergence: REST is this codebase's
established reference implementation for D-0003 (see the
`review-go-codex` skill's own "REST is the reference" guidance), so the
NEW mechanism follows REST's stricter precedent going forward — Phase
1b's existing, laxer flat-mechanism behavior is NOT retroactively
changed to match (a separate, much larger, out-of-scope change that
would risk breaking existing callers relying on silent dedup today).
Two contributions agreeing on the SAME name/attributes still dedupe
into ONE spec entry (not an error) — only a genuine MISMATCH errors.

## Companion change: the same axis for `api/events`

**Confirmed, NOT implemented here — tracked as an explicit, smaller
follow-up.** `api/events`' own `Middleware[In,Out]` (shipped by D-0003)
has the IDENTICAL gap this doc closes for reqreply: only a topic-var
vocabulary exists (`WithSubscribeTopic`/`WithPublishTopic`) — no
declarative property/header bridge at all, not even a flat-mechanism
equivalent of reqreply's own Phase 1b `FromUserPropertyParam`/
`FromResponseUserPropertyParam` (confirmed via grep: those functions only
exist in `adapters/mqtt5/reqreply_transport.go`, never for events'
`SubscribeMW`/`PublishMW`).

Once THIS doc's `PropertyParam`/`MergedPropertyParam[T]`/
`NewPropertyParam[T,V]` triple ships, extending `events.Middleware[In,Out]`
is a SMALL, mostly mechanical mirror — NOT a new design:

```go
// Mirrors events' EXISTING WithSubscribeTopic/WithPublishTopic naming
// convention (role-based, not request/response-based — events'
// Subscribe/Publish roles are asymmetric, unlike reqreply's single
// Route).
func (m Middleware[In, Out]) WithSubscribeProperty(p MergedPropertyParam[In]) Middleware[In, Out]
func (m Middleware[In, Out]) WithPublishProperty(p MergedPropertyParam[Out]) Middleware[In, Out]
```

**One small open question for that follow-up, not resolved here**:
should `PropertyParam`/`MergedPropertyParam[T]`/`NewPropertyParam[T,V]`
live in ONE shared location both `api/reqreply` and `api/events`
reference (a new home, since neither package may import the other, and
`codex`/`middleware` are the only shared ancestors both already import),
or be duplicated per-package like `TopicParam`/`HeaderParam`/etc.
already are (this codebase's OWN established "each API layer keeps its
own copy" precedent, confirmed via `codex/param.go`'s own doc comment
explaining why `rest.PathParam`/`events.TopicParam`/`reqreply.TopicParam`
are three independent thin wrappers over the SAME shared `codex.Param`/
`MergedParam`/`NewParam`, not one cross-package type)? The established
precedent favors duplication — `PropertyParam`/`MergedPropertyParam[T]`/
`NewPropertyParam[T,V]` would most likely become TWO thin wrapper
copies (`reqreply.PropertyParam` and `events.PropertyParam`), both over
the SAME shared `codex.Param`/`MergedParam`/`NewParam`, mirroring
`TopicParam`'s own precedent exactly — but this is worth confirming
explicitly when that follow-up is scoped, not assumed here.

## Structured errors (all implement `slog.LogValuer`)

Reuses the SAME error TYPES rest/events already ship for this mechanism
(package-local copies, per this codebase's own established "each API
layer keeps its own copy" precedent — NOT shared cross-package types):

```go
// MiddlewareInputError — mw's In value failed to decode/validate from
// request-side topic vars.
type MiddlewareInputError struct {
    Name string
    Err  error
}

// MiddlewareError — a Transform/ClientTransform (or bundled WithReceive/
// WithSend) fn returned its own business error not matched by any
// declared ErrorPattern — D2's fallback, mirrors rest.MiddlewareError/
// events.MiddlewareError exactly.
type MiddlewareError struct {
    Name string
    Err  error
}

// DuplicateMiddlewareNameError — two Middleware values with the SAME
// Declaration.Name attached to one route (D6(b)).
type DuplicateMiddlewareNameError struct {
    Route string
    Name  string
}

// AmbiguousMiddlewareAttachmentError — a SINGLE Middleware value carries
// a bundled WithReceive/WithSend Fn AND is ALSO passed to Transform/
// ClientTransform on the SAME route (D7).
type AmbiguousMiddlewareAttachmentError struct {
    Name string
}

// ConflictingParamContributionError — two DIFFERENT Middleware
// contributions (or a Middleware contribution vs. Phase 1b's flat
// mechanism) declare the SAME property/topic name with DIFFERENT
// attributes (kind or required-ness) — mirrors rest.
// ConflictingParamContributionError exactly (confirmed via
// api/rest/middleware.go's real checkParamConflicts). RESOLVED decision
// (see "AsyncAPI spec rendering" above): the NEW mechanism follows
// REST's stricter precedent, NOT Phase 1b's laxer silent-dedupe one.
type ConflictingParamContributionError struct {
    Route        string
    ParamName    string
    FirstSource  string
    SecondSource string
}
```

Each implements `Error() string`/`Unwrap() error` (where applicable)/
`LogValue() slog.Value` — direct ports of `rest`'s/`events`' identical
error types, field-for-field.

## Observer integration

No NEW observer interface needed — reuses `stats.Observer.RecordRequest`/
`stats.ReportErrors` exactly as the existing flat mechanism already does
(a `Transform`-attached `fn`'s error surfaces through the SAME
adapter-scoped `ServeError`/`CallError` types the security Fn's error
already uses today — `mqtt5.ServeError`/`mqtt5.CallError`,
`zeromq.ServeError`/`zeromq.CallError` — NOT a core `api/reqreply` type;
`stats.ReportErrors(obs, "topic_var", err)` for `MiddlewareInputError` —
mirrors existing topic-var error reporting). **Which `ErrorKind` value
wraps the NEW mechanism's own errors is an open question — see "Open
design decisions" #6 below**, found and not yet resolved in this
round's review.

## Unit test plan

Mirrors `api/rest`'s own `Transform`/`ClientTransform` test structure
(the closest structural match — single Route, both directions):

| Test | Verifies |
|---|---|
| `TestMiddleware_WithRequestTopic_MergesIn` | `Transform`'s `fn` receives correctly-decoded `In` from request topic vars |
| `TestMiddleware_WithResponseTopic_EncodesOutIntoReply` (server) | `Transform`'s returned `Out` correctly encodes into the reply's topic vars |
| `TestMiddleware_WithResponseTopic_DecodesOutFromReply` (client) | `ClientTransform`'s `DecodeOut` mechanically decodes `Out` from the actual reply's topic vars, no Fn involved |
| `TestTransform_EnrichesReqPointer` | `Transform`'s `fn` can both READ and WRITE `*Req`, visible to the route's own handler afterward |
| `TestClientTransform_ProducesInFromReq` | `ClientTransform`'s `fn` receives the caller's own `Req` value (read-only) and produces `In` |
| `TestRoute_Use_BundledWithReceive_AgnosticAttachment` | a `Middleware` with `WithReceive` set, attached via plain `.Use()`, dispatches without needing `Transform` |
| `TestRoute_Register_DuplicateMiddlewareNameError` | D6(b): two `Middleware` values sharing a `Declaration.Name` on one route |
| `TestRoute_Register_AmbiguousMiddlewareAttachmentError` | D7: one value both bundled AND passed to `Transform` |
| `TestRoute_Register_ConflictingParamContributionError` | two `Middleware` values (or a `Middleware` + Phase 1b's flat mechanism) declare the SAME property/topic name with DIFFERENT attributes — fails with `ConflictingParamContributionError` (decision #4's resolved REST-style behavior) |
| `TestRoute_Register_AgreeingParamContributions_DedupeWithoutError` | two `Middleware` values declaring the SAME name with the SAME attributes dedupe into ONE spec entry, no error |
| `TestTransform_MiddlewareError_FallsBackWhenNoErrorPatternMatch` | D2: `fn`'s business error wraps as `MiddlewareError` when no `ErrorPattern` matches |
| `TestAttachServer_Transform_RunsAfterPairedSecurity` (mqtt5) | D1: `Transform`'s declared middleware runs AFTER the paired security Fn (both pre-handler) — confirms reqreply's dispatch order matches REST's real, established order (security first, then declared middleware), not just "the same point" |
| `TestAttachServer_Transform_RunsAfterPairedSecurity` (zeromq) | same, zeromq — confirms the mechanism is genuinely transport-agnostic with zero adapter-specific work |
| `TestMiddleware_WithRequestProperty_MergesIn` | `Transform`'s `fn` receives correctly-decoded `In`, decoded from its OWN separate property-value map, alongside (never combined with) any topic vars decoded on the same route |
| `TestMiddleware_WithRequestProperty_RequiredButAdapterSuppliesNoPropertyMap` | a route declaring a REQUIRED property on an adapter with no property mechanism (simulated empty map) fails with the SAME `MiddlewareInputError` a missing topic var would — confirms no special-casing needed |
| `TestAttachServer_MiddlewareError_WrapsAsKindMiddleware` (mqtt5 and zeromq) | decision #6: a `Transform`-attached `fn`'s own business error (falling back to `MiddlewareError` per D2) surfaces through `ServeError{Kind: KindMiddleware}` (a NEW `ErrorKind` value in each adapter), NOT `KindHandler` — distinguishing a declared-middleware failure from a real handler failure |

## Files to create

| File | Responsibility |
|---|---|
| `api/reqreply/middleware_declaration.go` (NEW) | `Middleware[In,Out]`, `NewMiddleware`, `WithRequestTopic`/`WithResponseTopic`/`WithRequestProperty`/`WithResponseProperty`/`WithReceive`/`WithSend`, `RouteMiddlewareMarker`, error types — direct port of `api/rest/middleware_declaration.go`'s structure (closer shape match than events') |
| `api/reqreply/property_param.go` (NEW) | `PropertyParam`/`MergedPropertyParam[T]`/`NewPropertyParam[T,V]` — thin wrapper over `codex.Param`/`MergedParam`/`NewParam`, mirrors `TopicParam`'s exact wrapper pattern minus template-presence validation |
| `api/reqreply/transform.go` (NEW) | `Transform`/`ClientTransform`, `MiddlewareHandler`/`ClientMiddlewareHandler`, `buildDecodeIn`/`buildEncodeOut`/`buildEncodeIn`/`buildDecodeOut`, `buildMiddlewareHandler`/`buildClientMiddlewareHandler` — direct port of `api/rest/transform.go`'s structure; `DecodeIn`/`EncodeOut`/`EncodeIn`/`DecodeOut` take/return topic vars and property vars as SEPARATE map parameters (never combined), mirroring REST's real multi-map signatures exactly |
| `api/reqreply/route.go` (edit) | `RouteHandle.MiddlewareHandlers`/`ClientMiddlewareHandlers` fields; `Route.Register`/`ClientHandle` call the D6(b)/D7 check (mirrors `checkMiddlewareNameUniquenessAndAttachment`) AND the NEW `checkParamConflicts`-equivalent (`ConflictingParamContributionError`, unified with Phase 1b's existing `applyParamDeclarations`) |
| `api/reqreply/builder.go` (edit) | `registerRoute`/`AsyncAPISpec`-adjacent code unifies `Transform`/`ClientTransform`'s property-axis spec contributions into the SAME `reqHeaders`/`respHeaders` `schema.Schema` values Phase 1b's `applyParamDeclarations` already builds — see "AsyncAPI spec rendering" above |
| `adapters/mqtt5/reqreply_transport.go` (edit), `adapters/mqtt5/errors.go` (edit) | `serverTransport.Serve`/`clientTransport.call` consult `MiddlewareHandlers`/`ClientMiddlewareHandlers`, dispatching AFTER the paired security Fn (both pre-handler/pre-encode, confirmed order via REST's real dispatch code); supplies the property-value map from the real message's User Properties (reuses the SAME extraction `UserPropertyParam` already does); `errors.go` gains a NEW `KindMiddleware` `ErrorKind` value (decision #6) with a matching `String()` case |
| `adapters/zeromq/reqreply_transport.go` (edit), `adapters/zeromq/errors.go` (edit) | Same for topic-var dispatch, all 4 transports — confirms transport-agnostic parity; supplies an empty property-value map (zeromq has no property mechanism) — a route declaring a REQUIRED property fails naturally, no special-casing; `errors.go` gains the SAME NEW `KindMiddleware` value, mirroring mqtt5's |
| `api/reqreply/middleware_declaration_test.go`, `transform_test.go`, `property_param_test.go` (NEW) | See "Unit test plan" |
| `docs/features/security.md` or a NEW `docs/features/reqreply-middleware.md` | Document the new mechanism alongside the existing security one |
| `.github/instructions/go-codex.instructions.md` | `api/reqreply` row updated |
| **(Companion follow-up, NOT this round — see "Companion change" above)** `api/events/middleware_declaration.go` (edit), a shared or duplicated `PropertyParam` triple for events | `WithSubscribeProperty`/`WithPublishProperty` mirroring reqreply's new axis |

## Out of scope (deferred)

- Retiring/changing Phase 1b's flat `FromUserPropertyParam`/
  `FromResponseUserPropertyParam` mechanism — stays exactly as shipped;
  this doc's NEW property axis is fully additive, a SECOND way to
  declare a property-backed concern (codec-backed, `Declaration[In,Out]`-
  typed) alongside the flat mechanism's existing, simpler one (raw
  string params on `middleware.Middleware`) — mirrors how D-0003 itself
  never touched REST's/events' own flat mechanisms either.
- Actually IMPLEMENTING `api/events`' companion property axis
  (`WithSubscribeProperty`/`WithPublishProperty`) — tracked explicitly
  as a follow-up (see "Companion change" above), not part of this round.
- `ports.ReqReplyPattern` changes — likely none needed, mirroring
  `reqreply-middleware.md`'s own identical conclusion for Phase 1
  (`PluginReqReplyPattern` already delegates to `Route.Register`, so new
  `RouteHandle` fields populate for free).
- Extending to `mqtt`(v3) — N/A, no reqreply support exists there.

## Open design decisions (to resolve before implementation)

1. ~~`ClientTransform`'s `Out` shape — request-enrichment only, or reply-
   inspection too?~~ **RESOLVED this round** — confirmed against
   `api/rest/transform.go`'s ACTUAL signatures: `rest.ClientTransform`'s
   fn produces `In` (encoded into the OUTGOING request), and `Out` is
   decoded MECHANICALLY (no Fn) from the actual response, via
   `ClientMiddlewareHandler.DecodeOut`. reqreply mirrors this exactly —
   no new shape needed, no genuinely new problem to solve; REST already
   solved "a boundary with both a request AND a response/reply direction"
   and reqreply's shape is structurally identical.
2. ~~Should `Transform`/`ClientTransform` be able to run a general
   codec-backed enrichment concern using MQTT5 User Properties, not just
   topic vars?~~ **RESOLVED this round, scope EXPANDED** — MQTT5 User
   Properties and (future) AMQP message headers are the SAME
   cross-transport concept (named metadata separate from payload,
   wire-realized differently per protocol), NOT MQTT5-specific.
   Decision: added as a SECOND vocabulary axis directly on core
   `reqreply.Middleware[In,Out]` (`WithRequestProperty`/
   `WithResponseProperty`, protocol-neutral naming — "Property," not
   "Header," avoiding HTTP-specific casing/multi-value implications
   that don't apply to MQTT5/AMQP) — see "The 'property' vocabulary
   axis" above for the full design. Confirmed this is ALSO a genuine gap
   in `api/events`' own `Middleware[In,Out]` — tracked as an explicit,
   smaller companion follow-up (see "Companion change" above), not
   implemented in this round.
3. ~~**Naming**: is `reqreply.Middleware[In,Out]` the right name, given
   `middleware.Middleware` (the FLAT type) already exists and is
   imported by the SAME package?~~ **RESOLVED this round** — confirmed
   via grep: `api/reqreply` has ZERO existing unqualified `Middleware`
   identifier today, so `reqreply.Middleware[In,Out]` (package-qualified
   from outside, unqualified `Middleware[In,Out]` inside the package
   itself) coexists safely with the imported `middleware.Middleware`
   package-qualified reference — the EXACT SAME pattern `api/rest`/
   `api/events` already use with zero reported friction. No rename
   needed.
4. **Where should `PropertyParam`/`MergedPropertyParam[T]`/
   `NewPropertyParam[T,V]` live?** Given the "Companion change" section
   above establishes this triple is needed by BOTH `api/reqreply` and
   `api/events`, and this codebase's own established precedent
   (`codex/param.go`'s own doc comment, re: `TopicParam`/`HeaderParam`/
   etc.) is "each API layer keeps its own thin wrapper over the SAME
   shared `codex` primitive, never one cross-package type" — leaning
   toward DUPLICATION (`reqreply.PropertyParam` and `events.
   PropertyParam`, both thin wrappers over `codex.Param`/`MergedParam`/
   `NewParam`), not a new shared package. Not fully locked in — confirm
   explicitly when the events companion follow-up is scoped.
5. ~~Merge-field name conflict detection — REST-style ERROR on
   mismatch, or Phase 1b-style silent DEDUPE?~~ **RESOLVED this round
   (Round 2)** — user's decision: follow REST's stricter
   `ConflictingParamContributionError` behavior (errors when two
   contributions disagree on kind/required-ness for the SAME name;
   agreeing contributions still dedupe into one spec entry, no error).
   This is a DELIBERATE, ACCEPTED divergence from reqreply's OWN Phase
   1b precedent (`applyParamDeclarations`'s existing silent first-seen-
   wins dedupe, no mismatch check) — rationale: REST is this codebase's
   established reference implementation for D-0003 (per the
   `review-go-codex` skill's own "REST is the reference" guidance), so
   the NEW mechanism follows REST's precedent going forward. Phase 1b's
   existing, laxer flat-mechanism behavior is NOT retroactively changed
   to match — a separate, larger, explicitly out-of-scope change that
   would risk breaking existing callers relying on silent dedup today.
   See "AsyncAPI spec rendering" above for the full mechanism and the
   new `ConflictingParamContributionError` type.
6. ~~NEW this round (Round 4) — which adapter `ErrorKind` wraps the NEW
   mechanism's own errors?~~ **RESOLVED this round.** Neither
   `mqtt5.ErrorKind` nor `zeromq.ErrorKind` has a value earmarked for a
   declared-middleware failure today. Existing Phase 1b precedent
   already uses `KindDecode` for topic-var decode failures (confirmed in
   `adapters/mqtt5/reqreply_transport.go`), which naturally extends to
   this mechanism's own merge-side errors: `KindDecode` for a
   `MiddlewareInputError` (request-side merge failure, whether topic or
   property), `KindEncode` for an output-merge failure (reply-side).
   `MiddlewareError` (D2's fn-business-error fallback) needed a decision
   — user's choice: add a NEW `KindMiddleware` value to BOTH
   `mqtt5.ErrorKind` and `zeromq.ErrorKind` (option (b), over reusing
   `KindHandler`), giving an `OnError`/Observer consumer a clear signal
   that the failure happened in DECLARED MIDDLEWARE, before the real
   handler ran, distinguishable from a genuine handler failure. This
   adds one new enum value to each adapter's existing `ErrorKind` type
   (alongside `KindDecode`/`KindHandler`/`KindEncode`/`KindTimeout`/
   `KindSecurity`), with a `String()` case added to match.
