# Events SecurityFunc/CredentialFunc removal — align `api/events` with REST D-0001 and reqreply Phase 1

> **Status:** DRAFT — investigation only, no code changes yet. Written after
> `docs/roadmap/reqreply-middleware.md`'s Phase 1 shipped (mqtt5), which
> removed reqreply's `ServeOptions.SecurityFunc`/`CallOptions.CredentialFunc`
> as a breaking change, following `adapters/nethttp`'s own D-0001 precedent
> (`docs/design/d-0001-rest-middleware-workflow-simplification.md`) of
> removing `SecurityFunc` entirely once the declarative `.Use()`/`HandleMW`/
> `ClientMW` mechanism could fully replace it. Events pub/sub is the LAST
> place in go-codex where an old imperative security escape hatch still
> coexists permanently with the newer declarative mechanism
> (`Subscriber.SubscribeMW`/`Publisher.PublishMW`, shipped D-0002/D-0003).
> This doc investigates whether — and how — to close that gap too.
>
> **Decisions locked in (this round):** all 3 adapters (mqtt5, zeromq,
> mqtt v3) together, one combined round; `examples/adapters-mqtt-security`
> is DELETED and replaced by a new `examples/events-api` mini-project
> (mirrors `rest-api`/`reqreply-api`'s multi-package layout), demonstrating
> `SubscribeMW`/`PublishMW` security end-to-end across all 3 adapters in
> one coherent example instead of one disconnected adapter-focused demo.

## Motivation

The same principle REST (D-0001) and reqreply (Phase 1) already
established: **one declarative security mechanism, not two.** A permanent
imperative escape hatch sitting alongside a declarative one is confusing —
worse, `examples/adapters-mqtt-security/main.go` (see "Evidence" below)
shows this confusion is not hypothetical: it already has a
`SubscribeMW`-attached Fn that is a **deliberate no-op**, present only to
satisfy `CheckCoverage`'s bookkeeping, while the REAL enforcement logic
lives in the OLD `SubscribeOptions.SecurityFunc`. That is the exact
"two mechanisms, one real, one decorative" anti-pattern this doc exists to
eliminate.

Blast radius is larger than reqreply's Phase 1 (mqtt5-only): events
pub/sub's imperative mechanism exists across **three adapters**
(`adapters/mqtt5`, `adapters/zeromq`, `adapters/mqtt` v3), on **both roles**
(Subscribe AND Publish).

## Evidence gathered (code-verified this round)

| Adapter | `SubscribeOptions.SecurityFunc` | `PublishOptions.CredentialFunc` | `binding.go` (port adapter) also has it | Real (non-test) usage found |
|---|---|---|---|---|
| `adapters/mqtt5` | yes | yes | yes (`SubscribeAdapterOptions.SecurityFunc`) | `examples/adapters-mqtt-security/main.go` |
| `adapters/zeromq` | yes | yes | no | **none** — zero examples, zero non-test callers |
| `adapters/mqtt` (v3) | yes | yes | yes (`SubscribeAdapterOptions.SecurityFunc`) | none found (mqtt v3 has no events example of its own) |

**`events.Channel.Handle()` already calls `CheckCoverage` UNCONDITIONALLY**
for the subscribe role, at Handle-construction time
(`api/events/builder.go`, inside `Channel.Handle`, right after
`checkImplementationsDeclared`) — this is DIFFERENT from reqreply's
pre-Phase-1 state, where no coverage check existed at all before this
session's work. Events pub/sub's declarative half (SubscribeMW/PublishMW +
Implementations/ClientImplementations + mandatory CheckCoverage) is
**already fully shipped and enforced** — unlike reqreply, this doc does
NOT need a "build the coverage check" step. The ONLY remaining gap is the
old imperative escape hatch still existing IN PARALLEL.

**The paired security Fn shape for SubscribeMW is strictly MORE capable
than SecurityFunc, not less** — confirmed via `adapters/mqtt/caller.go`:
the SubscribeMW-recognized security shape is
`func(context.Context, pahomqtt.Message, *T) (map[string][]string, error)`
— it already receives the SAME raw `pahomqtt.Message` SecurityFunc gets,
PLUS the decoded typed value `*T`, PLUS scope-grant return semantics
SecurityFunc lacks entirely (`func(ctx, msg, reqs) error`, no grants, no
`*T`). Same finding holds for mqtt5's paired shape and PublishMW's
credential-supply shape (`adapters/mqtt/adapter.go`'s
`validatePublishImplementationShapes`/`runPublishSecurityImpls`, mirroring
mqtt5's `CredentialFunc func(ctx, msg *T, reqs) ([]UserProperty, error)`
against its own already-existing PublishMW credential-supply shape).
**This means removal is not blocked by any missing capability** — every
real thing SecurityFunc/CredentialFunc can currently do, SubscribeMW/
PublishMW can already do too, with a strictly richer signature.

`examples/adapters-mqtt-security/main.go`'s own doc comment (lines 1-24)
argues MQTT 3.1.1's `pahomqtt.Message` interface doesn't expose User
Properties, so "SecurityFunc is the enforcement point" — but this
rationale is now OUTDATED: SubscribeMW's paired Fn receives the identical
`pahomqtt.Message` value, so it can do the exact same raw-message
inspection SecurityFunc does today. The doc comment needs correcting as
part of any migration, not just the code.

## Investigation questions

1. **Does removal break any existing example/test with no SubscribeMW/
   PublishMW equivalent wired?** Yes, confirmed: `examples/adapters-mqtt-
   security` is the only real-world caller, and its `SubscribeMW` Fn is
   currently a no-op placeholder — migrating it means moving the REAL
   `SecurityFunc` logic INTO that Fn, not just deleting a field. Estimated
   blast radius: **one example, three named test/demo patterns inside it**
   (closure / msg-extraction / MessageFromContext), plus whatever
   `adapters/mqtt5`, `adapters/zeromq`, `adapters/mqtt` unit tests
   currently exercise `SecurityFunc`/`CredentialFunc` directly (not yet
   enumerated — next investigation step).
2. **Per-adapter protocol-specific nuances?** zeromq's own Fn-shape design
   (in-payload `*Req`-mutation, per `docs/roadmap/zeromq-security.md`)
   already differs from mqtt5's User-Property-based shape and mqtt v3's
   raw-message shape — removal must preserve each adapter's OWN existing
   paired shape (already shipped), not force convergence. Since zeromq has
   ZERO real callers of its own `SecurityFunc`/`CredentialFunc` today, its
   removal is close to a pure deletion (lowest risk of the three).
3. **Does `CheckCoverage` need adding to any adapter's dispatch path
   first?** No — confirmed already unconditional at `Channel.Handle()`
   construction time for ALL adapters (it lives in `api/events` itself,
   not per-adapter dispatch code — architecturally different from
   reqreply, where `CheckCoverage` runs inside the adapter's own
   `serverTransport.Serve`). This is a pre-existing, already-correct
   design; no new wiring needed before removal is safe.
4. **Port-adapter layer (`binding.go`) impact?** `adapters/mqtt5/
   binding.go`'s `SubscribeAdapterOptions.SecurityFunc` and
   `adapters/mqtt/binding.go`'s equivalent both thread straight through to
   the same `SubscribeOptions.SecurityFunc` field — these must be removed
   in lockstep with the underlying adapter option, not left dangling.
   zeromq's `binding.go` has no such field to begin with.
5. **Sequencing: mqtt5 first (mirroring reqreply Phase 1), or all three
   together?** Given zeromq has zero real callers (near-zero risk) and
   mqtt v3 has none either (only mqtt5 has a real example), a single
   combined round across all three may be lower overhead than three
   separate rounds — but the actual migration work (rewriting the
   `adapters-mqtt-security` example) is mqtt5/mqtt-v3-specific. TO BE
   DECIDED with the user before implementation.

## Scope decisions

| Item | In scope | Out of scope |
|---|---|---|
| Remove `SubscribeOptions.SecurityFunc`/`PublishOptions.CredentialFunc` — `adapters/mqtt5` (events) | ✅ | |
| Remove `SubscribeOptions.SecurityFunc`/`PublishOptions.CredentialFunc` — `adapters/zeromq` (events) | ✅ | |
| Remove `SubscribeOptions.SecurityFunc`/`PublishOptions.CredentialFunc` — `adapters/mqtt` (v3, events) | ✅ | |
| Remove matching `binding.go` `SecurityFunc` passthrough fields (mqtt5, mqtt v3) | ✅ | |
| Delete `examples/adapters-mqtt-security`, create `examples/events-api` mini-project demonstrating `SubscribeMW`/`PublishMW` security across all 3 adapters | ✅ | |
| Migrate/rewrite affected unit tests in all 3 adapters | ✅ | |
| `adapters/mqtt` (v3) reqreply — N/A, mqtt v3 has no reqreply support at all | | ✅ (unrelated, unaffected) |
| Any NEW security capability beyond what SecurityFunc/CredentialFunc already provide | | ✅ (parity migration only, not a feature add) |
| Changing `CheckCoverage`'s existing `api/events`-side wiring/timing | | ✅ (already correct, not touched) |

## Open design decisions — RESOLVED

- **Sequencing**: **all 3 adapters together, one round.** (zeromq/mqtt v3
  have zero real callers, so bundling them with mqtt5's real migration
  adds negligible risk.)
- **PR/round granularity**: **single combined round** — one PR covering
  all 3 adapters + the new example + doc/instructions updates.
- **`examples/adapters-mqtt-security` scope**: **DROP the adapter-focused
  single-file example entirely.** Replace it with a proper
  `examples/events-api` mini-project, mirroring `examples/rest-api`'s and
  `examples/reqreply-api`'s established multi-package layout (`routes/`,
  `handlers/`, one server-assembly package per adapter, `client/`,
  `demo_*.go` files, narrative `main.go`) — the same pattern this session
  already used for `reqreply-api`. This retires the "one giant single-file
  adapter demo" style in favor of the project's own established mini-
  project convention, AND demonstrates the declarative `SubscribeMW`/
  `PublishMW` security mechanism end-to-end (replacing the old
  `SecurityFunc`/`CredentialFunc`-based patterns 1-3 in the retired
  example) across `adapters/mqtt5`, `adapters/zeromq`, and `adapters/mqtt`
  (v3) subscribe-side, in one coherent example — not three disconnected
  ones.

### `examples/events-api` — planned mini-project layout (mirrors `reqreply-api`)

```
examples/events-api/
  routes/              — domain models, codecs, channel declarations
                          (plain channel, channel-level security via
                          .Use()+SubscribeMW, global-security-only channel)
  handlers/             — SERVER-side business logic (subscribe handlers),
                          adapter-agnostic
  mqtt5server/          — assembles routes/+handlers/ onto adapters/mqtt5
                          (in-process mock broker)
  zeromqserver/         — assembles onto adapters/zeromq PUB/SUB
  mqttserver/           — assembles onto adapters/mqtt (v3, Paho) —
                          demonstrates the raw pahomqtt.Message access
                          pattern directly through SubscribeMW's paired Fn
                          (the capability the retired example's "msg
                          extraction"/"MessageFromContext" patterns showed
                          via SecurityFunc)
  client/               — Publisher constructors for each adapter-
                          attachment mode above
  demo_*.go             — one file per concern
  main.go               — builds every server+client, runs every demo
```

This example becomes the ONE place demonstrating events pub/sub security
end-to-end post-removal, across all 3 adapters — superseding
`adapters-mqtt-security`'s narrower, mqtt-v3-only, SecurityFunc-based
scope.

## Files to create/investigate (next round)

| File | Action |
|---|---|
| `adapters/mqtt5/adapter.go` | remove `SubscribeOptions.SecurityFunc`/`PublishOptions.CredentialFunc` fields |
| `adapters/mqtt5/binding.go` | remove `SubscribeAdapterOptions.SecurityFunc` passthrough |
| `adapters/zeromq/adapter.go` | remove `SubscribeOptions.SecurityFunc`/`PublishOptions.CredentialFunc` fields |
| `adapters/mqtt/adapter.go` | remove `SubscribeOptions.SecurityFunc`/`PublishOptions.CredentialFunc` fields |
| `adapters/mqtt/binding.go` | remove `SubscribeAdapterOptions.SecurityFunc` passthrough |
| `examples/adapters-mqtt-security/` | **delete** — superseded by `examples/events-api` |
| `examples/events-api/` (new) | create mini-project — `routes/`, `handlers/`, `mqtt5server/`, `zeromqserver/`, `mqttserver/`, `client/`, `demo_*.go`, `main.go` — see layout above |
| `adapters/mqtt5/*_test.go`, `adapters/zeromq/*_test.go`, `adapters/mqtt/*_test.go` | enumerate + migrate tests exercising `SecurityFunc`/`CredentialFunc` directly (not yet enumerated) |
| `docs/roadmap/zeromq-security.md` | update/close its existing reminder section once zeromq's removal ships |
| `.github/instructions/go-codex.instructions.md` | update `api/events`/adapter rows once shipped |

## Out of scope entirely

- `adapters/mqtt` (v3) reqreply support — does not exist; this doc is
  events-pub/sub-only and does not touch reqreply.
- Any change to `api/events`'s own `CheckCoverage`/`Channel.Handle()`
  logic — already correct, confirmed this round, not touched.
- Any new declarative capability beyond what `SecurityFunc`/
  `CredentialFunc` already provide today — this is a mechanism
  consolidation, not a feature addition.
