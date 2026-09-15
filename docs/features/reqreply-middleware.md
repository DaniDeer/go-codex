# ReqReply Codec-Declared Middleware — property vocabulary axis

> See also: [`api/reqreply` package on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/api/reqreply)
>
> Runnable demo: [`examples/reqreply-api`](https://github.com/DaniDeer/go-codex/tree/main/examples/reqreply-api) (Demo 10 — property vocabulary axis)
>
> Design record: [D-0003 — Codec-Declared Middlewares](../design/d-0003-codec-declared-middlewares.md)'s
> own "Addendum: `api/reqreply` and `api/events`' property vocabulary
> axis" section (the original roadmap doc, `reqreply-codec-declared-middleware.md`,
> has since shipped and been deleted per its own graduation policy)

`api/reqreply.Middleware[In, Out]` brings `api/reqreply` up to the SAME
codec-declared middleware parity `api/rest`/`api/events` already have (see
[D-0003 — Codec-Declared Middlewares](../design/d-0003-codec-declared-middlewares.md))
— a SECOND, additive mechanism entirely alongside the older, still-fully-
functional flat `middleware.Middleware` (`.Use()`/`HandleMW`/`ClientMW`,
security-only). `api/events.Middleware[In, Out]` gained the IDENTICAL new
axis (`WithSubscribeProperty`/`WithPublishProperty`) in the same round —
see [Feature: Event Channels](events.md#codec-backed-middleware-transformclienttransform)
for the events-side counterpart to everything below.

## The "property" vocabulary axis

Unlike REST (header/cookie/query) or events (topic vars only), reqreply's
wire boundary is "one topic template plus optional protocol-native
metadata" — MQTT5 User Properties today, a future AMQP adapter's native
message headers tomorrow. `reqreply.Middleware[In, Out]` reuses the
EXISTING `WithRequestTopic`/`WithResponseTopic` (built on the SAME
`NewTopicParam` a route's own `Req`/`Resp` already use) and adds a NEW,
protocol-neutral **property** axis:

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

route = reqreply.Transform(route, tenantPolicy,
    func(ctx context.Context, req *ComputeReq, in TenantIn) (TenantAck, error) {
        return TenantAck{Ack: "processed-for-" + in.TenantID}, nil
    })
```

`NewPropertyParam[T, V]` declares a REQUIRED property (absent from the
adapter-supplied property map → `reqreply.MiddlewareInputError`, mirroring
a missing topic var). `NewOptionalPropertyParam[T, V]` declares one that's
simply left at its zero value when absent — properties, unlike topic vars,
are conceptually optional metadata a message may legitimately omit (a
topic var has no optional variant: it either appears in the template,
unconditionally required, or doesn't exist at all).

Both `PropertyParam`/`MergedPropertyParam[T]` mirror `TopicParam`'s exact
wrapper pattern (`WithCodec`, `WithDescription`) over the SAME shared
`codex.Param`/`MergedParam[T]`/`NewParam[T, V]` primitives every other
API's own param types already use — see [Feature: Schema Metadata](schema-metadata.md).

## Two attachment styles

Identical to REST's/events' own split:

- **Route-BOUND**, via `reqreply.Transform`/`reqreply.ClientTransform` —
  `fn` additionally receives the route's own already-decoded `req *Req`
  (server) / `req Req` (client), shown above. Multiple `Transform`/
  `ClientTransform` calls accumulate on the SAME route in registration
  order — two attached middlewares both writing the same `*Req` field is
  attachment-order, last-applied-wins, not an error.
- **Route-AGNOSTIC**, via `Middleware.WithReceive`/`Middleware.WithSend` +
  plain `.Use(mw)` — the LITERAL SAME bundled value reused verbatim across
  many routes with entirely different `Req`/`Resp` types.

A middleware `fn`'s own business error is `ErrorPattern`-eligible (matched
the same way a handler error is) before falling back to
`reqreply.MiddlewareError{Name, Err}`. Attaching two `Middleware[In,Out]`
values with the same `Declaration.Name` to one route returns
`reqreply.DuplicateMiddlewareNameError`; combining both attachment styles
on one value returns `reqreply.AmbiguousMiddlewareAttachmentError`.

## Conflict detection — a deliberate breaking change

`Route.Register` checks EVERY declared topic-var/property contribution —
Phase 1b's OLDER flat `.Use(mqtt5.FromUserPropertyParam(...))` mechanism
AND this NEW `Middleware[In,Out]` axis alike — for a UNIFORM
`reqreply.ConflictingParamContributionError`: two contributions for the
SAME name conflict if `Required` differs, or their codec schemas differ.
This is a deliberate, accepted BREAKING CHANGE relative to Phase 1b's own
older, laxer silent-first-seen-wins dedupe for mismatched declarations —
chosen for ONE simple algorithm over tracking which mechanism declared
which contribution. Topic-vars and properties are INDEPENDENT namespaces
(a topic var and a property sharing the same name never conflict with
each other — they come from genuinely different wire locations).

## Value precedence

When the route's own topic/property merge (from `Req`/`Resp`) and a
`Middleware`'s own merge target the SAME var name, the middleware-derived
value ALWAYS wins — mirrors REST's/events' own "explicit override >
middleware-derived > route/channel-own-derived" precedence rule (reqreply
has no explicit-override tier today, so it's a 2-tier rule: middleware
always beats route-own).

## Write-side wiring (mqtt5)

`WithRequestProperty`'s produced value merges into the SAME MQTT5 User
Properties mechanism a request's security credentials already use
(`t.opts.UserProperties`). `WithResponseProperty`'s produced value is
written onto the SERVER's actual outgoing reply — both the success-reply
AND error-reply paths — as real MQTT5 User Properties, using the SAME
extraction/validation machinery Phase 1b's `UserPropertyParam` already
uses for the request side. `adapters/zeromq` has no property mechanism at
all — a route declaring a REQUIRED property there fails naturally with
the SAME `MiddlewareInputError` a missing topic var would, no
special-casing needed anywhere in the core dispatch logic.

## AsyncAPI spec rendering

The property axis's declared contributions render into the SAME
request/reply message `headers` schema Phase 1b's own
`FromUserPropertyParam`/`FromResponseUserPropertyParam` bridge already
populates — `Required` correctly determines which property names appear
in the schema's `required` array (an optional property does not appear
there, even though it's still validated/merged when present).

## See also

- [Feature: Event Channels](events.md#codec-backed-middleware-transformclienttransform) — the identical mechanism for `api/events`
- [Feature: REST API & HTTP Adapters](rest-api.md#codec-backed-middleware-transformclienttransform) — the reference implementation this mechanism mirrors
- [Feature: Security & Auth](security.md) — Phase 1b's OLDER flat `UserPropertyParam`/`FromUserPropertyParam` mechanism, unchanged, still fully functional alongside this NEW one
- [D-0003 — Codec-Declared Middlewares](../design/d-0003-codec-declared-middlewares.md) — the shared design this mechanism implements
- [examples/reqreply-api](https://github.com/DaniDeer/go-codex/tree/main/examples/reqreply-api) — Demo 10 exercises the property axis end-to-end, including the write-side wiring, over mqtt5
- [Feature: Observer Pattern](observer.md#apireqreplyobservability--a-shipped-declarative-observer-wrapper) — the DIFFERENT, general-purpose (unpaired) `.HandleMW(nil, fn)`/`.ClientMW(nil, fn)` mechanism this SAME package exposes via the shipped `reqreply.Observability[Req, Resp]` helper, used for observability rather than codec-backed enrichment — see Demo 11 (`demo_observer_middleware.go`)
