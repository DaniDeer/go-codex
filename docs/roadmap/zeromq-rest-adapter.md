# `adapters/zeromqrest` — ZeroMQ REQ/REP Adapter for `api/rest`

> **Status:** Design draft — not yet implemented. **Now an INDEPENDENT
> future effort**, no longer gated on/gating
> `d-0006-protocol-native-capabilities.md`'s own Phase 3, whose
> CAPABILITY MECHANISM half has since SHIPPED without this adapter
> (an explicit scope decision at Implement time — see that doc's Phase
> 3 Learnings entry). Every design decision below (wire framing, API
> surface, Security/Middleware dispatch requirements, all 6 Open Design
> Decisions) stands unchanged and ready for whenever a future session
> picks this up — nothing here depends on timing.
> [← Back to Roadmap](index.md)

Spun out of [Composable Capability Requirements — Phase
3](../design/d-0006-protocol-native-capabilities.md#phase-3--apirest-a-new-synchronous-transport-stateless-adapter),
per this repo's own convention (a new adapter gets its own dedicated
Explore-mode roadmap doc, written BEFORE its Implement step — see the
`plan-a-new-codex-feature`/`add-a-new-adapter` skills). This doc covers
ONLY the adapter's own binding-level design (wire framing, socket
lifecycle, error types, `ports.IOAdapter` implementation). The
CAPABILITY mechanism this new adapter must eventually satisfy
(`rest.CapabilityRequirement`, `HeaderParam`/`CookieParam` becoming
genuinely-checked Tier 2 capabilities) is designed AND IMPLEMENTED
separately, already SHIPPED in the capability-composition doc's own
Phase 3 section — cross-reference it, don't re-derive it here. A future
Implement session for THIS adapter inherits that mechanism fully built
and tested: it need only implement `HeaderCapableTransport`/
`QueryCapableTransport` (not `CookieCapableTransport`) and a real `HWM`
`Capability` value (not `Conflate`), then wire
`rest.CheckParamKindCoverage`/`VerifyCapabilityCoverage` into its own
dispatch — mirroring exactly how `adapters/nethttp`/`adapters/chi` did.

## Motivation

`api/rest` is already transport-agnostic — its builder generates OpenAPI
3.1 regardless of which adapter serves it (`docs/features/rest-api.md`'s
own opening line). Today exactly one transport family exists (HTTP, via
`adapters/nethttp`/`adapters/chi`), so `HeaderParam`/`CookieParam`
coverage is "trivially always satisfied." Introducing a SECOND
REST-eligible transport makes that guarantee genuinely meaningful for
the first time.

Per the capability-composition doc's own settled **REST-eligible-
transport guardrail**: a transport is REST-eligible only if it is BOTH
(1) synchronous (request immediately expects its matching response, no
broker-mediated delivery gap) AND (2) transport-stateless (no
persistent, stateful broker connection). ZeroMQ REQ/REP satisfies both —
a REQ socket blocks for its REP, and the pattern requires no broker
session at all (point-to-point or via a stateless proxy). MQTT (v3 or 5)
satisfies NEITHER and is **permanently excluded** from ever getting an
`api/rest` adapter — that shape belongs to `api/reqreply`, by settled
design.

## Scope decisions

| In scope | Out of scope |
|---|---|
| A new `adapters/zeromqrest` package, `ports.IOAdapter`-based, mirroring `adapters/nethttp`'s binding shape | Retrofitting `adapters/zeromq` itself — REST and reqreply stay PERMANENTLY SEPARATE APIs (settled in the capability-composition doc), even though both touch ZeroMQ, via genuinely different socket usage |
| Plain REQ/REP (point-to-point) | ROUTER/DEALER REST multiplexing (deferred — reqreply's ROUTER/DEALER precedent could transplant later if real demand appears, not designed here) |
| Literal, non-templated `Path`s (one socket per registered {Method, Path}) | `PathParam`/`{var}` templated paths server-side — `t.sockets[path]`'s literal map lookup (inherited from `adapters/zeromq`'s own reqreply precedent) has no wildcard-matching equivalent; `AttachServer` fails fast with `PathTemplateUnsupportedError` rather than silently mismatching — see "Path template variables" below |
| `HeaderParam` support (multi-frame, mirrors `mqtt5.UserPropertyParam`'s wire shape) | `CookieParam` support — see Open Design Decision #1 (resolved: correctly, permanently omitted) |
| `HWM` support (bounds socket queue depth, applies to any socket type) | `Conflate` support — see Open Design Decision #2 (resolved: correctly, permanently omitted — has no meaning for a pattern with at most one outstanding request) |
| `Method`+`Path` carried for OpenAPI spec-fidelity | `Method` driving actual protocol-level dispatch differences (ZeroMQ REQ/REP has no verb concept) — see Open Design Decision #3 |
| Reusing `adapters/zeromq`'s existing `FramedSocket` interface as-is (zero new socket abstraction) | A new ZMQ Go binding dependency — `adapters/zeromq` has ZERO hard dependency on any ZMQ library today (documents `pebbe/zmq4` as the recommended `FramedSocket` implementation, but the interface lets callers bring their own); this adapter follows the identical "bring your own socket" pattern |

## Toolchain / dependency decisions

**No new dependency.** `adapters/zeromq` itself has zero `go.mod`
dependency on any ZMQ Go binding — `FramedSocket` (`SendFrames`/
`RecvFrames`, multi-frame `[][]byte`) is the abstraction boundary, and
`docs/guides/zeromq.md` documents `github.com/pebbe/zmq4` as the
recommended concrete implementation callers wire in themselves. This new
adapter reuses `FramedSocket` UNCHANGED (import it directly from
`adapters/zeromq`, or duplicate the tiny interface locally — an Open
Design Decision below) rather than inventing a second socket
abstraction.

## Wire framing proposal

Mirrors `adapters/zeromq`'s existing reqreply REQ/REP framing convention
exactly (`adapters/zeromq/reqreply_transport.go`), extended with a
header frame:

- **Request:** `[method, path, params, body]` — 4 frames. `method`/
  `path` carried for spec-fidelity and observability (`RecordRequest`
  method/path fields), not dispatch (one socket per registered route,
  same as reqreply's "one socket per topic" model — routing is entirely
  socket-based, a `path` frame is redundant for dispatch but keeps the
  wire self-describing, matching HTTP's own request-line redundancy with
  its own routing). `params` is a SINGLE frame carrying BOTH declared
  `HeaderParam` AND `QueryParam` name/value pairs together (no wire-level
  reason to separate them once you're not literally HTTP — both are
  flat string k/v pairs; this is why the transport type implements BOTH
  `rest.HeaderCapableTransport` and `rest.QueryCapableTransport`, backed
  by the SAME frame) — format TBD, Open Design Decision #4: reuse MQTT5
  User-Properties-style repeated-pair encoding, or a small
  length-prefixed scheme, or a JSON object frame for simplicity at the
  cost of a few extra bytes. NO `cookies` frame exists — see Open Design
  Decision #1, now resolved.
- **Success reply:** `[status, params, body]` — 3 frames, mirrors
  reqreply's `[status, payload]` with a NEW params frame for
  `ResponseHeaderParam` support (REST's response-header concept has no
  reqreply equivalent; `ResponseCookieParam` is UNSUPPORTED for the same
  reason as the request side).
- **Error reply:** `[status, code, body]` — 3 frames when a declared
  `rest.ErrorPattern` matches (byte-identical shape to reqreply's own
  matched-error convention); `[status, body]` — 2 frames, plain-text
  fallback, when no pattern matches (mirrors reqreply's unmatched
  fallback shape exactly, minus the `code` frame).

## Proposed API surface

```go
package zeromqrest // adapters/zeromqrest

// ServeOptions configures [Serve]/[AttachServer].
type ServeOptions struct {
    OnError      func(ServeError)
    Observer     stats.Observer
    Capabilities []Capability // sealed, adapters/zeromqrest's OWN type — NOT the same as adapters/zeromq.Capability, even though both wrap ZMQ HWM/Conflate; see the capability-composition doc's Phase 3 section for why a shared type across APIs is rejected
}

// CallOptions configures [Call]/[AttachClient].
type CallOptions struct {
    Observer        stats.Observer
    Timeout         time.Duration
    RequestFormats  any
    ResponseFormats any
    Capabilities    []Capability
}

// Serve is the thin, single-route escape hatch — mirrors
// [zeromq.Serve]'s reqreply precedent exactly.
func Serve[Req, Resp any](
    ctx context.Context,
    sock FramedSocket,
    handle *rest.RouteHandle[Req, Resp],
    fn func(context.Context, Req) (Resp, error),
    opts ServeOptions,
) error

// Call is the thin, single-call escape hatch.
func Call[Req, Resp any](
    ctx context.Context,
    sock FramedSocket,
    handle *rest.RouteHandle[Req, Resp],
    req Req,
    opts CallOptions,
) (Resp, error)

// NewServerTransport/NewClientTransport build a configured, attachable
// transport binding an entire *rest.Server's/*rest.Client's registered
// routes at once (one FramedSocket per registered route's Path) —
// mirrors [zeromq.NewServerTransport]/[zeromq.NewClientTransport]'s
// reflection-based bulk-dispatch mechanism exactly. Per docs/design/
// d-0006-protocol-native-capabilities.md's Phase 4d (Attach factory
// redesign, already shipped for events/rest/reqreply's OTHER adapters
// by the time this adapter is built): attaching is EXCLUSIVELY
// `server.Attach(...)`/`client.Attach(...)` — no adapter-namespaced
// Attach* convenience function should be added here.
func NewServerTransport(opts ServerTransportOptions) rest.ServerTransport
func NewClientTransport(opts ClientTransportOptions) rest.ClientTransport

// ServerTransportOptions/ClientTransportOptions bundle sockets+opts into
// a SINGLE Options struct parameter (docs/design/
// d-0006-protocol-native-capabilities.md's Phase 4d convention — no
// positional params, even for required fields).
type ServerTransportOptions struct {
    Sockets map[string]FramedSocket
    Serve   ServeOptions
}
type ClientTransportOptions struct {
    Sockets map[string]FramedSocket
    Call    CallOptions
}

// binding.go: ports.IOAdapter implementation for ports.Pattern support
// (PluginRESTPattern etc.) — mirrors adapters/nethttp/binding.go's
// reference pattern exactly.
```

## Security/Middleware dispatch (existing REST machinery, not new design)

**Found missing from an earlier draft of this sketch — not a new
mechanism, just an omission.** `AttachServer`/`Serve`'s real dispatch
(in `transport.go`) MUST reflect the same fields off `*rest.RouteHandle`
that `adapters/nethttp/serve.go`'s `buildRouteHandler` already does,
byte-for-byte:

```go
elem := reflect.ValueOf(handle).Elem()
secSchemes, _ := elem.FieldByName("SecuritySchemes").Interface().(map[string]rest.SecurityScheme)
globalSecurity, _ := elem.FieldByName("GlobalSecurity").Interface().([]route.SecurityRequirement)
impls, _ := elem.FieldByName("Implementations").Interface().([]middleware.ServerImplementation)
middlewareHandlers, _ := elem.FieldByName("MiddlewareHandlers").Interface().([]rest.MiddlewareHandler)
```

then `validateImplementationShapesReflect` (paired-security-Fn shape
check), the security coverage check (every scheme in `descriptor.
Security`/`GlobalSecurity` must have a matching `impls[i].Satisfies`
entry), and dispatch wrapped by the SAME general-purpose (unpaired)
`HandleMW` composition + D-0003 `Transform`/`MiddlewareHandlers`
dispatch nethttp already runs. `AttachClient`'s `clientTransport`
mirrors `adapters/nethttp/clienttransport.go`'s identical
`ClientImplementations` reflection + `mergeCredentialHeaders`-equivalent
step. Without this, a route's `.HandleMW`/`.Use(mw)`/`Transform`
declarations would silently never execute against this adapter — this
is NOT optional, it's the same baseline every other REST/reqreply
adapter already provides.

**Cross-reference, sequencing-sensitive (added during a later design
review)**: [`docs/design/d-0003-codec-declared-middlewares.md's Addendum 7`](../design/d-0003-codec-declared-middlewares.md)
REMOVES the legacy/raw-adapter Security-pairing mode this section's
"paired-security-Fn shape check" wording refers to —
`validateImplementationShapesReflect`'s job (checking a Security-paired
`impls[i]`'s Fn shape) becomes vestigial once that mode is gone, since
`impls`/`Implementations` can then only ever carry general-purpose
(`mw == nil`) decorator entries. The underlying FIELDS this section
reflects on (`MiddlewareHandlers`, `Implementations`) stay stable either
way — `BoundMiddleware`-based dispatch populates the SAME
`rb.middlewareHandlers`/`rb.middlewareSpecContributions` fields the
current bound-shape-detection mechanism does today — so the reflection
APPROACH sketched above remains structurally valid. Only the
DESCRIPTION above (and whatever `adapters/zeromqrest/transport.go` is
eventually written against) needs to match whichever mechanism is
CURRENT at implementation time.

**Resolution (the sequencing question above is now settled)**: the
bound-middleware-split redesign SHIPPED first (across `api/rest`,
`api/events`, `api/reqreply`) — `adapters/zeromqrest` has NOT been
implemented yet. This section's "paired-security-Fn shape check"
wording is therefore ALREADY STALE and must be updated, as part of this
adapter's own implementation, to describe dispatching against
`BoundMiddleware`-populated `MiddlewareHandlers`/`Implementations`
fields instead (see D-0003's Addendum 7 for the current mechanism) —
implement `adapters/zeromqrest/transport.go` directly against this
current mechanism; do NOT implement a mirror of the removed
reflection-based Security-pairing path this section originally
described.

**Tier 2 coverage-check presence test, using the capability doc's newly
added accessors** (`rv.MethodByName("CookieParamNames").Call(nil)`,
etc. — see the capability-composition doc's Phase 3 section for why
these were added and why the older `HeaderMergeFields()`-style methods
are insufficient): `AttachServer` calls
`HeaderParamNames()`/`CookieParamNames()`/`QueryParamNames()` (plus
scans `SecuritySchemes` for an `In` field match) via this SAME
reflection pass, BEFORE dispatch begins, to decide whether
`rest.UnsupportedParamKindError` fires.

## Path template variables — literal paths only (accepted Phase 3 scope limitation)

**A genuine, inherited constraint, not silently new to REST.** Confirmed
via `adapters/zeromq/reqreply_transport.go`: `t.sockets[path]` is a
LITERAL string map lookup — no wildcard/pattern matching exists for
ZeroMQ REQ/REP sockets (each socket binds to ONE fixed address; there is
no router layer the way HTTP frameworks provide). Reqreply tolerates
this because topic templates are typically resolved against a small,
known set of concrete values; REST's `PathParam`/`{var}` templates
(e.g. `/users/{id}`) are used far more heavily and expect ANY `{id}` to
dispatch to the same handler — something this adapter structurally
cannot do without a routing layer this phase does not build.

**Resolution:** Phase 3 supports ONLY literal, non-templated `Path`s
server-side — "one socket per registered {Method, Path} pair" (Open
Design Decision #3) means exactly that: a LITERAL path string, one
socket each. `AttachServer` returns a NEW typed error,
`rest.PathTemplateUnsupportedError{Path string, Adapter string}`
(or the equivalent `zeromqrest`-local type), when a registered route's
`Path` contains an unresolved `{var}` token — FAILING FAST at attach
time, not silently mismatching or requiring the deployer to
pre-register one socket per possible ID value (impractical for
open-ended identifiers). ROUTER/DEALER's own future multiplexing
(Scope decision — deferred) is the natural place a real routing layer
could eventually live, if genuine demand for templated REST paths over
ZeroMQ appears; not attempted in this phase.

## Content negotiation (RequestFormats/ResponseFormats)

HTTP negotiates via real `Accept`/`Content-Type` headers. Since the
`params` frame already carries arbitrary header-shaped key/value pairs
(see the wire framing proposal above), negotiation rides on the SAME
frame via the conventional `Content-Type`/`Accept` KEYS — no new frame,
no new mechanism. When absent, falls back to `Formats[0]`/first-declared
(mirrors the escape hatch's existing no-negotiation default elsewhere
in this codebase) rather than requiring negotiation headers on every
call.

## Structured errors (all implement `slog.LogValuer`)

Mirrors `adapters/zeromq`'s existing reqreply error taxonomy —
`ServeError`/`CallError` with `Kind` (`KindDecode`/`KindEncode`/
`KindHandler`/`KindSecurity`/`KindTimeout`/`KindMiddleware`), plus a new
`MissingSocketError{Path string}` (renamed field from reqreply's
`Topic`) and `TransportTypeMismatchError`. No new taxonomy shape needed
— the reqreply error types are already transport-family-agnostic in
spirit; this adapter's own types are a separate, sealed copy (own
package, per the "adapters don't share exported types across API
boundaries" convention), not literally reused from `adapters/zeromq`.
PLUS one genuinely NEW type this adapter needs that no sibling adapter
does: `PathTemplateUnsupportedError{Path, Adapter string}` (see "Path
template variables" above) — returned by `AttachServer` at attach time,
never per-request.

## Observer integration

`stats.Observer.RecordRequest` with method `"ZMQREST-REP"`/`"ZMQREST-
REQ"` (mirrors `"ZMQ-REP"`/`"ZMQ-REQ"`'s naming convention), `path`
carried from the route's declared `Path`. `stats.CapabilityObserver`
wired via the SAME `events.ResolveCapabilityValue`/
`RecordCapabilityApplied` generic helpers reqreply's Phase 2 already
reuses (fully generic, zero events-specific types) — first REST adapter
ever to need this, since REST's capability mechanism doesn't exist until
this phase. Only `HWM` fires `RecordCapabilityApplied` here (`Conflate`
is not ported — see Open Design Decision #2).

**`rest.UnsupportedParamKindError` (Header/Cookie/Query coverage
mismatch) is a plain Go error returned at `AttachServer`/`AttachClient`
setup, NEVER an observer event** — mirrors `VerifyCapabilityCoverage`'s
own established precedent exactly (see the capability-composition
doc's Phase 3 section for the full reasoning, traced across every
existing coverage-check call site). Attach-time structural failures
happen before any request stream exists to report into; only
successful capability application and per-request rejections
(`RecordSecurityRejection`) go through the Observer.

## Unit test plan

Mirrors reqreply's own test shape closely:

| Test | Verifies |
|---|---|
| `TestAttachServer_AttachClient_RoundTrip` | Full REQ/REP round trip via `AttachServer`/`AttachClient` |
| `TestServe_HeaderParam_ValidatedAndMerged` | Declared `HeaderParam` validated against the incoming params frame |
| `TestServe_QueryParam_ValidatedAndMerged` | Declared `QueryParam` validated against the SAME incoming params frame |
| `TestCall_ResponseHeaderParam_Decoded` | A declared `ResponseHeaderParam` decodes from the reply's params frame |
| `TestAttachServer_CookieParam_ReturnsUnsupportedParamKindError` | A route declaring `CookieParam` (or `route.APIKeyScheme(name, "cookie")`) fails FAST at `AttachServer`/`AttachClient` setup with `rest.UnsupportedParamKindError`, not a per-request failure |
| `TestServe_ErrorPattern_MatchedReply` | 3-frame `[status, code, body]` on a matched `rest.ErrorPattern` |
| `TestServe_ErrorPattern_NoMatch_FallsBackToPlainText` | 2-frame `[status, body]` fallback |
| `TestAttachServer_MissingSocketError` | Unregistered path → typed error |
| `TestAttachServer_PathTemplate_ReturnsPathTemplateUnsupportedError` | A route declaring a `{var}` templated `Path` fails FAST at `AttachServer` setup, not a per-request mismatch |
| `TestAttachServer_HandleMW_PairedSecurityFn_Verifies` | Paired security `Fn` dispatch parity with `adapters/nethttp`'s own test — closes Gap 1 |
| `TestAttachServer_CheckCoverage_MissingSecurityMiddlewareError` | A declared security scheme with no paired `HandleMW` implementation is rejected at `AttachServer` time |
| `TestAttachServer_HandleMW_GeneralPurpose_AlwaysRuns` | An unpaired, general-purpose `HandleMW` (e.g. observability) always runs regardless of security |
| `TestAttachClient_ClientMW_PairedCredentialFn_WritesReq` | Client-side paired credential `Fn` writes into the outgoing params frame |
| `TestBinding_PluginRESTPattern` | `ports.IOAdapter` binding parity with `adapters/nethttp`'s own test |

## Files to create

| File | Responsibility |
|---|---|
| `adapters/zeromqrest/adapter.go` | `ServeOptions`/`CallOptions`, `Serve`/`Call` escape hatches |
| `adapters/zeromqrest/transport.go` | `serverTransport`/`clientTransport` — the real reflection-based dispatch `AttachServer`/`AttachClient` build on, INCLUDING `Implementations`/`MiddlewareHandlers`/`SecuritySchemes`/`GlobalSecurity` reflection, security coverage check, and general/paired middleware dispatch (mirrors `adapters/nethttp/serve.go`'s `buildRouteHandler`/`clienttransport.go` exactly — see "Security/Middleware dispatch" above); also rejects templated `Path`s at `AttachServer` time (see "Path template variables" above) |
| `adapters/zeromqrest/errors.go` | `ServeError`/`CallError`/`MissingSocketError`/`TransportTypeMismatchError`/`PathTemplateUnsupportedError` |
| `adapters/zeromqrest/capability.go` | Sealed `Capability` — `HWM` ONLY (mirrors `adapters/zeromq`'s own `HWM`; `Conflate` deliberately NOT ported — see Open Design Decision #2, resolved); transport type implements `rest.HeaderCapableTransport`/`rest.QueryCapableTransport` — deliberately NOT `rest.CookieCapableTransport` (Open Design Decision #1, resolved) |
| `adapters/zeromqrest/binding.go` | `ports.IOAdapter` implementation (`PluginRESTPattern` support) |
| `adapters/zeromqrest/doc.go` | Package godoc, usage example |
| `examples/rest-api` (or a new `examples/rest-zeromq` example) | Demonstrates the adapter alongside existing HTTP ones |

## Open design decisions (to resolve before/during Design's finalization)

1. **Cookie support — RESOLVED** (per the capability-composition doc's
   own Phase 3 section, which formalizes the actual mechanism): this
   adapter's transport type implements `rest.HeaderCapableTransport`/
   `rest.QueryCapableTransport` (both fold into the ONE params frame
   below) but DELIBERATELY DOES NOT implement
   `rest.CookieCapableTransport` — a correct, permanent,
   compiler-visible omission, exactly like AMQP's exchange/queue
   capability being MQTT5-unsatisfiable. A route declaring `CookieParam`
   (or a `route.APIKeyScheme(name, "cookie")`) and attached to this
   adapter fails FAST at `AttachServer`/`AttachClient` setup with
   `rest.UnsupportedParamKindError{Kind: "Cookie", Adapter:
   "zeromqrest"}`, not a confusing per-request validation failure. No
   cookie-jar semantics are ever implemented here.
2. **`HWM`/`Conflate` Tier 3a support — RESOLVED, `HWM` only** (per the
   capability-composition doc's own Phase 3 section): `HWM`
   (`ZMQ_SNDHWM`/`ZMQ_RCVHWM`) is a genuine per-socket-type ZeroMQ option
   that bounds internal queue depth regardless of pattern — transfers
   cleanly to a synchronous REQ/REP socket (guards against unbounded
   memory growth if a peer stalls), so it's ported unchanged.
   `Conflate` (`ZMQ_CONFLATE`, "keep only the latest message **per
   topic**") has NO protocol-level meaning for REQ/REP — the pattern has
   at most ONE outstanding request/reply in flight at a time by
   definition, so there is no backlog of unread messages for "keep only
   latest" to ever apply to. This is a genuine, PERMANENT, protocol-
   driven omission (mirrors the Cookie omission above exactly) — not a
   "surveyed but not implemented, awaiting demand" placeholder the way
   MQTT5's Message Expiry/Shared Subscriptions are (tracked separately
   in the capability-composition doc's Phase 4).
3. **Method's role** — carried purely for spec-fidelity/observability
   (current proposal), or should it participate in dispatch somehow
   (e.g. multiple Methods sharing one Path, dispatched by a `method`
   frame lookup, mirroring HTTP's own method-based routing)? The
   simplest, most REQ/REP-native design is "one socket per registered
   {Method, Path} pair" (mirrors reqreply's "one socket per topic"
   exactly) — leaning toward this, deferring the routing-by-frame
   alternative unless a real multi-method-per-path use case appears.
4. **Params frame encoding** — MQTT5-User-Properties-style repeated
   key/value pairs (byte-compatible with existing patterns in this
   codebase) vs. a small custom length-prefixed scheme vs. a JSON object
   (simplest to implement, most bytes on the wire). Leaning: mirror the
   MQTT5 convention for consistency across the codebase's own adapters,
   not introduce a third encoding style.
5. **`FramedSocket` reuse** — import `adapters/zeromq.FramedSocket`
   directly (creates a NEW cross-adapter-package dependency, currently
   unprecedented in this codebase — every other adapter pair, e.g.
   `mqtt`/`mqtt5`, stays fully independent) vs. duplicate the tiny
   4-method interface locally in `adapters/zeromqrest` (keeps the
   "adapters never depend on each other" invariant intact, at the cost
   of one duplicated interface definition). Leaning: duplicate — the
   interface is tiny (4 methods) and the "adapters stay independent"
   invariant is worth more than avoiding one small duplication.
6. **Package naming** — `adapters/zeromqrest` (chosen here, mirrors
   `adapters/websocket`'s single-word-compound convention) vs.
   `adapters/zmqrest` (shorter, but inconsistent with `adapters/zeromq`'s
   own spelled-out naming) vs. a sub-package `adapters/zeromq/rest`
   (rejected — contradicts Scope decision #1, REST and reqreply must
   stay independent, a sub-package implies a shared parent dependency
   that doesn't otherwise exist).

## See also

- [Composable Capability Requirements — Phase
  3](../design/d-0006-protocol-native-capabilities.md#phase-3--apirest-a-new-synchronous-transport-stateless-adapter)
  — the capability mechanism this adapter must satisfy once built
- [`docs/roadmap/amqp-adapter.md`](amqp-adapter.md) — the template this
  doc's structure follows
- [`add-a-new-adapter` skill](../../.github/skills/add-a-new-adapter/SKILL.md)
  — the full new-adapter checklist to apply at Implement time
- [`adapters/zeromq/reqreply_transport.go`](../../adapters/zeromq/reqreply_transport.go)
  — the wire-framing and dispatch precedent this doc's proposals mirror
- [`adapters/nethttp/serve.go`](../../adapters/nethttp/serve.go)/
  [`adapters/nethttp/clienttransport.go`](../../adapters/nethttp/clienttransport.go)
  — the Security/Middleware reflection-dispatch precedent this doc's
  "Security/Middleware dispatch" section mirrors exactly
