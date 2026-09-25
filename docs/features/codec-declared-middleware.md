# Codec-Declared Middleware — REST, Events & ReqReply

> See also: [D-0003 — Codec-Declared Middlewares](../design/d-0003-codec-declared-middlewares.md)
> (the shared design this feature implements) · [Guide: Error Handling](../guides/error-handling.md#middleware-error-paths--rest-events-reqreply-side-by-side)
> (the full 3-error-type reference table, side-by-side across all 3 APIs) ·
> [Feature: REST API](rest-api.md) · [Feature: Event Channels](events.md)
>
> Runnable demos: [`examples/error-types`](https://github.com/DaniDeer/go-codex/tree/main/examples/error-types) ·
> [`examples/rest-api`](https://github.com/DaniDeer/go-codex/tree/main/examples/rest-api) ·
> [`examples/events-api`](https://github.com/DaniDeer/go-codex/tree/main/examples/events-api) ·
> [`examples/reqreply-api`](https://github.com/DaniDeer/go-codex/tree/main/examples/reqreply-api) (Demo 10 — property vocabulary axis)

`api/rest`, `api/events`, and `api/reqreply` share ONE `middleware.Declaration[In,Out]`-based
mechanism for declaring a REUSABLE, codec-backed enrichment/enforcement concern — independent of
any one route's/channel's own `Req`/`Resp`/payload type. A middleware's own `In`/`Out` values
validate through their own codecs, and its own header/cookie/query/topic/property merge fields
reuse the SAME constructors a route's/channel's own `Req`/`Item` already use — no new param
vocabulary to learn per boundary. This page describes the mechanism ONCE, then the per-API
specifics each boundary's own wire shape requires.

## The shared model

Every boundary attaches a `Middleware[In, Out]` value in one of two styles:

- **Route/channel-BOUND**, via `Transform`/`ClientTransform` (REST additionally has SSE-route
  counterparts `TransformSSE`/`ClientTransformSSE`) — `fn` additionally receives the route's/
  channel's own already-decoded value (`req *Req` for REST/reqreply server-side, `msg *T` for
  events subscribe) for concerns that need to read or enrich it.
- **Route/channel-AGNOSTIC**, via `Middleware.WithReceive`/`Middleware.WithSend` bundling a
  `req`/`msg`-free `fn` directly onto the value, attached via plain `.Use(mw)` — the LITERAL SAME
  value reused verbatim across many routes/channels with entirely different `Req`/payload types.

A middleware `fn`'s own business error is `ErrorPattern`/`ErrorChannel`-eligible (matched the SAME
way a handler error is) BEFORE falling back to a package-local `MiddlewareError{Name, Err}`. See
the [Error Handling guide's "Middleware error paths" section](../guides/error-handling.md#middleware-error-paths--rest-events-reqreply-side-by-side)
for the full 3-error-type shape (`MiddlewareInputError`/`MiddlewareError`/`MiddlewareOutputError`),
side-by-side across all 3 APIs, plus the exact observer location strings
(`"middleware:in"`/`"middleware:fn"`/`"middleware:out"`) each dispatch point reports.

Two structural rules hold identically across all 3 packages:

- Attaching two `Middleware[In,Out]` values with the same `Declaration.Name` to one route/channel
  returns `DuplicateMiddlewareNameError{Route/Channel, Name}`.
- Combining BOTH attachment styles on one `Middleware` value (bundled `WithReceive`/`WithSend` AND
  attached via `Transform`/`ClientTransform`) returns `AmbiguousMiddlewareAttachmentError{Name}`.

**Value precedence.** When a route's/channel's own merge (derived from `Req`/payload) and a
middleware's own merge target the SAME var/header/property name, the middleware-derived value
ALWAYS wins over the route/channel's own — REST/events additionally have a THIRD, outermost tier: an
explicit per-call override (e.g. `CallOptions.Vars`) beats even the middleware-derived value.
reqreply has no explicit-override tier today, so it's a 2-tier rule (middleware always beats
route-own).

## REST (`api/rest`)

```go
apiKeyPolicy := rest.NewMiddleware(
    middleware.NewDeclaration("api-key-policy", apiKeyInCodec, apiKeyOutCodec),
).WithRequestHeader(rest.NewRequiredHeaderParam("X-API-Key", codex.String(),
    func(in APIKeyIn) string { return in.Key },
    func(in *APIKeyIn, v string) { in.Key = v },
))

route = rest.Transform(route, apiKeyPolicy,
    func(ctx context.Context, req *GetProfileReq, in APIKeyIn) (APIKeyOut, error) {
        return APIKeyOut{Validated: true}, nil
    })
```

Merge fields reuse the SAME constructors a route's own `Req`/`Resp` already use:
`WithRequestHeader`/`WithRequestCookie`/`WithRequestQuery` (decoding `In`) and
`WithResponseHeader`/`WithResponseCookie` (encoding `Out`). SSE routes attach the identical
mechanism via `TransformSSE`/`ClientTransformSSE`. See
[Feature: REST API — Codec-backed middleware](rest-api.md#codec-backed-middleware-transformclienttransform)
for the full walkthrough, route-agnostic reuse example, and the REST-local error-type table.

## Events (`api/events`)

```go
regionPolicy := events.NewMiddleware(
    middleware.NewDeclaration("region-policy", regionInCodec, regionOutCodec),
).WithSubscribeTopic(events.NewTopicParam("region", codex.String(),
    func(in RegionIn) string { return in.Region },
    func(in *RegionIn, v string) { in.Region = v },
))

subscriber = events.Transform(subscriber, regionPolicy,
    func(ctx context.Context, msg *SensorReading, in RegionIn) error {
        msg.Region = in.Region
        return nil
    })
```

Pub/sub's asymmetric shape means subscribe is the RECEIVING role (`In` decoded from incoming topic
vars, `Out` unused — no reply channel to encode into) while publish is the SENDING role (`Out`
encoded into outgoing topic vars via `WithPublishTopic`, `In` unused). Reuses the SAME
`events.NewTopicParam[T,V]` constructor a channel's own `Item` already uses. See
[Feature: Event Channels — Codec-backed middleware](events.md#codec-backed-middleware-transformclienttransform)
for the full walkthrough and the events-local error-type table.

## ReqReply (`api/reqreply`)

```go
tenantPolicy := reqreply.NewMiddleware(
    middleware.NewDeclaration("tenant-policy", tenantInCodec, tenantAckCodec),
).WithRequestTopic(reqreply.NewTopicParam("tenant", codex.String(),
    func(in TenantIn) string { return in.TenantID },
    func(in *TenantIn, v string) { in.TenantID = v },
))

route = reqreply.Transform(route, tenantPolicy,
    func(ctx context.Context, req *ComputeReq, in TenantIn) (TenantAck, error) {
        return TenantAck{Ack: "processed-for-" + in.TenantID}, nil
    })
```

`WithRequestTopic`/`WithResponseTopic` reuse the SAME `NewTopicParam` a route's own `Req`/`Resp`
already use — the merge target is `Req` only (topic vars never merge into `Resp`, matching
`reqreply.NewTopicParam[Req]`'s own restriction). Since `api/reqreply` has no dedicated feature page
of its own today, this page remains its primary reference for both the middleware mechanism and the
property vocabulary axis below.

## The property vocabulary axis (events + reqreply only)

Unlike REST (header/cookie/query) or events' topic-vars-only shape, `api/reqreply`'s and
`api/events`' wire boundary can ALSO carry protocol-native metadata alongside a topic — MQTT5 User
Properties today, a future AMQP adapter's native message headers tomorrow. Both packages add a
SECOND, protocol-neutral **property** axis alongside the existing topic-var axis:

```go
tenantPolicy := reqreply.NewMiddleware(
    middleware.NewDeclaration("tenant-policy", tenantInCodec, tenantAckCodec),
).WithRequestProperty(reqreply.NewPropertyParam("X-Tenant-Id", codex.String(),
    func(in TenantIn) string { return in.TenantID },
    func(in *TenantIn, v string) { in.TenantID = v },
)).WithResponseProperty(reqreply.NewPropertyParam("X-Ack", codex.String(),
    func(out TenantAck) string { return out.Ack },
    func(out *TenantAck, v string) { out.Ack = v },
))
```

`NewPropertyParam[T, V]` declares a REQUIRED property (absent from the adapter-supplied property map
→ `MiddlewareInputError`, mirroring a missing topic var). `NewOptionalPropertyParam[T, V]` declares
one that's simply left at its zero value when absent — properties, unlike topic vars, are
conceptually optional metadata a message may legitimately omit. Both `PropertyParam`/
`MergedPropertyParam[T]` mirror `TopicParam`'s exact wrapper pattern (`WithCodec`, `WithDescription`)
over the SAME shared `codex.Param`/`MergedParam[T]`/`NewParam[T, V]` primitives every other API's own
param types already use — see [Feature: Schema Metadata](schema-metadata.md).
`adapters/zeromq` has no property mechanism at all — a route/channel declaring a REQUIRED property
there fails naturally with the SAME `MiddlewareInputError` a missing topic var would, no
special-casing needed anywhere in the core dispatch logic.

### Direct (Middleware-free) attachment — the simpler default

For the common case — merge a property value straight into a field on `Req`/`Item`, no extra logic
needed — attach a `MergedPropertyParam[T]` DIRECTLY to `NewRoute`/`NewChannel`, mirroring
`NewTopicParam`'s own framing exactly. No `Middleware[In,Out]` wrapper required:

```go
route := reqreply.NewRoute[ComputeReq, ComputeResp](
    "compute/add", computeReqCodec, computeRespCodec,
    reqreply.NewPropertyParam("X-Tenant-Id", codex.String(),
        func(r ComputeReq) string { return r.TenantID },
        func(r *ComputeReq, v string) { r.TenantID = v }),
)
```

This merges the real incoming MQTT5 User Property directly into `ComputeReq` on the server
(`RouteHandle.PropertyMergeFields()`/`MergePropertyVars`), and derives the outgoing User Property
from `Req` on the client (`RouteHandle.EncodePropertyVars`) — the SAME declare-once, zero-ceremony
convenience `NewTopicParam` already provides for topic vars (`events.NewChannel` gains the identical
convenience via `ChannelHandle.PropertyMergeFields()`/`MergePropertyVars`/`EncodePropertyVars`).
Reach for the `Middleware[In,Out]`-based `WithRequestProperty`/`WithResponseProperty` (or events'
`WithSubscribeProperty`/`WithPublishProperty`) only when you ALSO need custom `fn` logic (e.g. a
policy lookup keyed by the property value) or route/channel-agnostic reuse across many
routes/channels.

### Write-side wiring (mqtt5)

`WithRequestProperty`'s produced value merges into the SAME MQTT5 User Properties mechanism a
request's security credentials already use. `WithResponseProperty`'s produced value is written onto
the SERVER's actual outgoing reply — both the success-reply AND error-reply paths — as real MQTT5
User Properties, using the SAME extraction/validation machinery the older, still-fully-functional
flat `UserPropertyParam` mechanism already uses for the request side (see
[Feature: Security & Auth](security.md)).

### AsyncAPI spec rendering

The property axis's declared contributions render into the SAME request/reply message `headers`
schema the older `FromUserPropertyParam`/`FromResponseUserPropertyParam` bridge already populates —
`Required` correctly determines which property names appear in the schema's `required` array (an
optional property does not appear there, even though it's still validated/merged when present).

## Conflict detection — a uniform algorithm across all 3 APIs

`Route.Register`/`Channel.Register` check EVERY declared var/property contribution — the OLDER flat
`.Use(mw)`-based mechanisms AND the `Middleware[In,Out]` axis alike — for a UNIFORM conflict rule:
two contributions for the SAME name conflict if `Required` differs, OR their codec schemas differ.
Two DIFFERENT-kind params sharing a name (e.g. a REST header "X" and a query "X") are INDEPENDENT
namespaces and never conflict; topic vars and properties are likewise INDEPENDENT namespaces (a
topic var and a property sharing the same name never conflict with each other — they come from
genuinely different wire locations). The error is `rest.ConflictingParamContributionError`/
`events.ConflictingParamContributionError`/`reqreply.ConflictingParamContributionError`
(`{Route/Channel, ParamName, FirstSource, SecondSource}`), a deliberate, accepted BREAKING CHANGE
relative to earlier, laxer first-seen-wins dedupe — chosen for ONE simple algorithm over tracking
which mechanism declared which contribution. See
[D-0003's Addendum 2](../design/d-0003-codec-declared-middlewares.md#addendum-2-rest-conflict-detection-alignment--middlewareout-cross-adapter-parity)
for the full history of aligning REST onto the SAME rule events/reqreply already used.

## Dead-letter fallback (`api/reqreply`)

`reqreply.DeadLetter(topic, opts...)` declares an OPTIONAL, last-resort sink attempted immediately
after `ErrorPattern` fails to match (or none is declared) — the SAME fixed
`DeadLetterEnvelope{SourceTopic, Payload, Error, Timestamp}` shape `events.DeadLetter` uses (see
[Feature: Event Channels — Dead-letter fallback](events.md#dead-letter-fallback--deadletter) for the
identical mechanism on the pub/sub side, and the
[Error Handling guide](../guides/error-handling.md#dead-letter-fallback-when-nothing-else-claimed-the-failure)
for the cross-API rationale and adapter-reachability matrix):

```go
route := reqreply.NewRoute[Req, Resp]("compute/add", reqCodec, respCodec,
    reqreply.DeadLetter("compute/add/dlq").
        WithDescription("Undeliverable compute requests.").
        WithSchemaName("ComputeDeadLetter"),
)
```

`Server.AddGlobalDeadLetter(topic, opts...)` sets an application-wide default; a route with no
`DeadLetter` declared inherits it, and an explicit `DeadLetter("")` opts out — mirrors
`AddGlobalSecurity`'s nil-inherit/empty-override convention. `RouteHandle.DeadLetterFor(obs,
sourceTopic, rawPayload, err) (topic, body, ok)` is the single call site adapters consult, at the
SAME dispatch points as `ErrorPattern` (request decode, topic/property-var merge, security
middleware, `Middleware` `DecodeIn`/`Fn`/`EncodeOut`, handler error, and the response `EncodeOut`
failure) as well as a failed reply publish. `DeadLetter` DOES generate its own AsyncAPI channel
entry (a receive-only channel carrying the fixed `DeadLetterEnvelope` schema) —
`WithDescription`/`WithSchemaName`/`WithChannelAddress`/`WithOperationID` all affect the rendered
spec, mirroring `ErrorPatternOpt`'s equivalents; routes sharing ONE dead-letter destination (e.g.
via `Server.AddGlobalDeadLetter`) register that topic as a SINGLE spec channel entry, not once per
route.

**`adapters/zeromq`'s REQ/REP transport needs its own DLQ socket.** Unlike `adapters/mqtt5` (one
shared client reaches any topic), REQ/REP is point-to-point — the declared dead-letter topic must
have its OWN entry in the `sockets` map passed to `zeromq.AttachServer`/`AttachRouterServer`.
Without one, the dead-letter is silently skipped rather than sent back over the route's own
REP/ROUTER socket (which would violate REQ/REP's one-reply-per-request invariant).

## See also

- [D-0003 — Codec-Declared Middlewares](../design/d-0003-codec-declared-middlewares.md) — the shared
  design this mechanism implements, including its
  [property vocabulary axis Addendum](../design/d-0003-codec-declared-middlewares.md#addendum-apireqreply-and-apievents-property-vocabulary-axis-bringing-both-up-to-full-parity-with-this-design)
- [Guide: Error Handling — Middleware error paths](../guides/error-handling.md#middleware-error-paths--rest-events-reqreply-side-by-side) —
  the full 3-error-type reference table, side-by-side across all 3 APIs
- [Feature: REST API — Codec-backed middleware](rest-api.md#codec-backed-middleware-transformclienttransform)
- [Feature: Event Channels — Codec-backed middleware](events.md#codec-backed-middleware-transformclienttransform) ·
  [Feature: Event Channels — Dead-letter fallback](events.md#dead-letter-fallback--deadletter)
- [Feature: Security & Auth](security.md) — the older flat `UserPropertyParam`/
  `FromUserPropertyParam` mechanism, unchanged, still fully functional alongside this one
- [Feature: Observer Pattern](observer.md#apireqreplyobservability--a-shipped-declarative-observer-wrapper) —
  the DIFFERENT, general-purpose (unpaired) `.HandleMW(nil, fn)`/`.ClientMW(nil, fn)` mechanism this
  SAME package exposes via `reqreply.Observability[Req, Resp]`, used for observability rather than
  codec-backed enrichment
- [Concepts: Declaring APIs and Ports](../concepts/declaring-apis-and-ports.md) — where this
  mechanism sits relative to each boundary's own declaration story
