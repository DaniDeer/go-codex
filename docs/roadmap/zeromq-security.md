# ZeroMQ Security Mechanism — `adapters/zeromq`

> **Status:** SHIPPED. Items 1 (optional frame-based mechanism) and 2
> (connection-level CURVE) are CLOSED — researched real-world ZeroMQ
> CURVE/ZAP practice, cross-checked `pebbe/zmq4`'s actual API surface;
> both stay permanently out of scope for core `adapters/zeromq` — see
> "Connection-level authentication — CLOSED" below for the full
> rationale. **Reqreply's Fn-shape (the one remaining actionable item)
> is now SHIPPED** — `.Use()`/`HandleMW`/`ClientMW` wired into all 4
> zeromq reqreply transports (`serverTransport`/`clientTransport`/
> `routerServerTransport`/`dealerClientTransport`), exactly per this
> doc's own finalized design (paired Fn shape, general-purpose decorator
> shape, the `reflect.New`-based value↔pointer mechanic, error/observer/
> coverage wiring) — verified via `gofmt`/`go build`/`go vet`/`go test
> -race`/`just check`/all examples, all clean, plus all 12 planned tests
> (see "Unit test plan" below). `examples/reqreply-api`'s Demo 9
> (`demo_cross_api_oauth2_sharing.go`) demonstrates it end-to-end,
> additionally showing the SAME `middleware.SecurityScheme` declaration
> shared across a zeromq reqreply route AND a locally-declared REST
> route (see `docs/features/security.md`'s "Sharing a security scheme
> declaration across REST/events/reqreply" section).
>
> Spun out of [Pub/Sub Workflow Simplification](../design/d-0002-pubsub-workflow-simplification.md)'s
> escape-hatch simplification review (Escape Hatch #5), which originally
> assumed closing this gap required inventing an entirely new wire-level
> credential convention before ANY progress was possible. That
> assumption turned out to be wrong: resolving a related finding
> (Escape Hatch #6, `mqtt` v3's publish-side "no credential mechanism"
> gap) surfaced an IN-PAYLOAD message-level security mechanism —
> `PublishMW`/`SubscribeMW`'s `fn` gaining write/read access to `*T`
> (the decoded payload itself) — that needs NO wire-format change at
> all and applies to zeromq identically. This doc's remaining scope is
> now much narrower: whether zeromq ALSO wants an optional, additional
> out-of-band frame-based mechanism, plus a separate connection-level/
> CURVE question. No sequencing dependency on the pub/sub workflow
> doc's own Decision 1/2/3 — those already specify the in-payload
> mechanism directly; this doc only covers the OPTIONAL extras.
>
> **Scope extended beyond pub/sub**: this doc's in-payload finding ALSO
> resolves the analogous open question
> [ReqReply Middleware](reqreply-middleware.md) raised for zeromq's
> `reqreply` security Fn-shape (see "Implication for
> `reqreply-middleware.md`" section below) — same underlying reasoning,
> same decision, a different `api/*` pattern.
> [← Back to Roadmap](index.md)

## Confirmed current state

> **Updated (re-verified against real code during a later review
> round)**: `adapters/zeromq`'s PUB/SUB side (`SubscribeOptions[T]`/
> `PublishOptions[T]`) has SINCE GAINED `SecurityFunc`/`CredentialFunc`
> fields — this is exactly the in-payload mechanism this doc's own intro
> describes as "surfaced" via D-0002 (see `adapters/zeromq/adapter.go`).
> The paragraph below, describing "ZERO security mechanism... zero hits
> for `SecurityFunc`/`CredentialFunc`", predates that and is now STALE
> for pub/sub specifically — kept below as the doc's original framing
> (per this doc's own "keep in place, mark resolved" discipline), but
> readers should treat it as historical, PRE-D-0002 context, not the
> current state. **Still accurate for REQREPLY**: `adapters/zeromq`'s
> reqreply `ServeOptions`/`CallOptions` (`Serve`/`Call`/`ServeRouter`/
> `CallDealer`) have NEVER had `SecurityFunc`/`CredentialFunc` fields —
> zeromq reqreply genuinely has zero security mechanism today, which is
> what this doc's remaining Fn-shape work item (below) is actually
> closing.

`adapters/zeromq` has ZERO security mechanism of any kind today —
confirmed via exhaustive grep: zero hits for
`SecurityFunc`/`CredentialFunc`/`middleware.` anywhere in
`adapters/zeromq/*.go`. `Subscribe`/`Publish` only ever see
`[topic, payload]` frames — there is no header/property slot to extract
an OUT-OF-BAND credential FROM, unlike MQTT5's User Properties or even
MQTT 3.1.1's CONNECT-time username/password.

## What's now tractable, without any wire-format change

[Pub/Sub Workflow Simplification](../design/d-0002-pubsub-workflow-simplification.md)'s
Decision 3 gives `SubscribeMW`/`PublishMW`'s `fn` read/write access to
`*T` — the DECODED payload itself, not any transport-specific envelope.
A credential embedded as an ordinary field in the payload (validated,
merged, and encoded via the SAME codec as the rest of the message) works
identically regardless of transport, since `T` is entirely
transport-agnostic. Concretely, for zeromq:

```go
// Both directions follow the SAME shape — no raw-message-equivalent
// parameter exists (or is needed) since zeromq's [topic, payload]
// frames carry nothing beyond what's already decoded into T, unlike
// mqtt/mqtt5's subscribe-side which also gets the raw *pahomqtt.Message/
// *pahomqtt5.Publish for reading protocol-native User Properties.
func(ctx context.Context, msg *T, reqs []route.SecurityRequirement) error
```

This closes the PRACTICAL message-level security gap for zeromq without
any of the wire-level invention originally assumed necessary — a
`SubscribeMW`/`PublishMW` `fn` following this shape can already verify/
attach a credential today, using the SAME declarative attachment point
(`Subscriber[T].SubscribeMW`/`Publisher[T].PublishMW`) every other
transport uses. **This piece does not need its own roadmap item — it
is part of [Pub/Sub Workflow Simplification](../design/d-0002-pubsub-workflow-simplification.md)'s
Decision 3 directly**, once implemented there.

## What remains genuinely open (this doc's actual scope) — ALL RESOLVED

1. **CLOSED — not needed.** An OPTIONAL, additional out-of-band
   frame-based mechanism (mirroring MQTT5's User Properties via an EXTRA
   ZeroMQ frame, e.g. `[topic, credential, payload]` instead of `[topic,
   payload]`), kept SEPARATE from the payload itself. **Resolved this
   round**: no concrete driver/use case has ever surfaced, and zeromq's
   pub/sub in-payload mechanism (`SubscribeOptions.SecurityFunc`/
   `PublishOptions.CredentialFunc`, ALREADY SHIPPED) has proven
   sufficient in practice — adding wire-format complexity for a
   hypothetical benefit contradicts this codebase's own "don't invent
   unrequested API" discipline. Revisit ONLY if a genuine driver
   surfaces (e.g. a real need to verify a credential BEFORE the full
   payload decode runs) — not planned.
2. **CLOSED — connection-level CURVE stays permanently out of scope
   for core `adapters/zeromq`.** See "Connection-level authentication —
   CLOSED" below for the full research-backed rationale (real-world
   CURVE/ZAP practice, `pebbe/zmq4`'s actual API, and why
   `adapters/zeromq`'s own library-agnostic design makes this a
   structurally different situation from mqtt5/mqtt's `SecuredClient`).
3. **Answered by 1/2's closure**: "use the in-payload mechanism for
   message-level (pub/sub: shipped; reqreply: designed below, not yet
   implemented), CURVE + your own authorization layer entirely outside
   go-codex for connection-level" IS the sufficient, final answer —
   mirrors `api/mcp`'s deliberate "security handled entirely outside
   go-codex" precedent exactly. No further investigation needed on
   either axis.

## Connection-level authentication — CLOSED

Researched real-world ZeroMQ CURVE/ZAP practice and `pebbe/zmq4`'s
actual Go API (the library `adapters/zeromq`'s own doc comments already
reference as the illustrative example binding) to resolve item 2 above
with evidence, not just architectural intuition:

- **CURVE is configured on the CONCRETE socket object, before Bind/
  Connect** — `sock.SetCurveServer(1)`, `sock.SetCurvePublickey(pub)`,
  `sock.SetCurveSecretkey(sec)` (server), `sock.SetCurveServerkey(...)`
  (client) are all methods on `*zmq.Socket` itself, called during
  connection setup. Authentication is typically delegated to a ZAP
  (ZeroMQ Authentication Protocol, RFC 27) handler — a separate
  long-running process validating each connecting client's public key
  against a whitelist, entirely independent of any per-message dispatch.
  This is architecturally IDENTICAL to how TLS works for mqtt5/mqtt
  (`tls.Config` supplied to `tls.Dialer`/`SetTLSConfig` before the
  client is ever constructed) — a connection-ESTABLISHMENT-time
  concern, not a per-message one, confirming the "same layer as TLS"
  intuition `docs/features/security.md` already stated.
- **The crucial structural difference from mqtt5/mqtt**: those adapters
  already depend DIRECTLY on a concrete client library
  (`github.com/eclipse/paho.golang/paho`) — so an optional `Connect()`+
  `ConnectOptions.TLS` convenience wrapper is a natural, zero-new-
  dependency extension built on top of an already-required import.
  `adapters/zeromq` is DELIBERATELY library-agnostic — confirmed via
  `go.mod`: **zero dependency on any concrete ZeroMQ library** at all.
  It exposes only a `FramedSocket` interface (`SendFrames`/`RecvFrames`/
  `SetSubscription`/...) that the CALLER implements from whatever
  binding they choose (`pebbe/zmq4`, `go-zeromq/zmq4`, etc. — shown only
  as an illustrative example in a doc comment, never imported).
- **Why this rules out a `zeromq.Connect()`+CURVE convenience**: adding
  one would require EITHER (a) growing `FramedSocket` with CURVE-
  specific setter methods — breaking every existing implementer and
  permanently coupling the interface to one specific security
  mechanism's vocabulary, or (b) picking ONE concrete library as an
  actual dependency of `adapters/zeromq` — a structural discontinuity
  from the "bring your own binding" design this package has always had,
  unlike mqtt5/mqtt's situation where that dependency already exists.
- **Decision**: core `adapters/zeromq` exposes NO CURVE/TLS
  configuration surface, permanently — CURVE key setup, ZAP handler
  wiring, and IP allow-listing are 100% the caller's own concern,
  configured on their own concrete socket before it's ever wrapped as a
  `FramedSocket`. If a convenience wrapper is EVER wanted, it belongs in
  a SEPARATE, explicitly-optional sub-package that opts into ONE
  concrete library dependency by choice (mirroring how `adapters/mqtt5`/
  `adapters/mqtt` accepted their own `paho.golang` dependency) — not in
  core `adapters/zeromq`, and only if a real driver emerges. Not planned
  now.

## Implication for `reqreply-middleware.md` — SAME decision as pub/sub

[ReqReply Middleware](reqreply-middleware.md) — the design for
`api/reqreply`'s own `.Use()`/`HandleMW`/`ClientMW` declare/implement
split — currently defers zeromq's Fn-shape entirely, stating (carried
forward from `docs/design/d-0004-reqreply-workflow-simplification.md`
without re-checking against THIS doc's own, already-resolved finding)
that it "needs a NEW wire-level credential convention before ANY Fn
shape can be finalized." **This is stale/overly pessimistic** — found
while cross-checking the two docs against each other:

- zeromq's REQ/REP frames (`[payload]` request / `["ok"/"error",
  payload]` reply, confirmed via `adapters/zeromq/reqreply_transport.go`)
  have the EXACT SAME "no separate credential slot" shape as pub/sub's
  `[topic, payload]` frames — the SAME in-payload mechanism this doc
  already established for pub/sub (confirmed ALREADY SHIPPED:
  `adapters/zeromq/adapter.go:82,110` — `SecurityFunc func(ctx, msg *T,
  reqs) error` / `CredentialFunc func(ctx, msg *T, reqs) error`) applies
  to reqreply identically — NO new wire-level frame is needed there
  either.
- **Decision (same as the pub/sub case, confirmed)**: reqreply's zeromq
  paired security Fn shapes read/write the decoded `*Req` directly,
  mirroring the pub/sub shapes above EXACTLY (same parameter shape, same
  plain-`error` return — deliberately NOT REST's scope-grant model
  `reqreply-middleware.md` has mqtt5 adopt for ITS OWN paired Fn shape;
  each transport adapter mirrors its OWN precedent, an intentional,
  already-established per-adapter difference in that doc, not an
  inconsistency introduced here):

  ```go
  // Server-side (paired) — reads the decoded *Req before dispatch,
  // mirrors zeromq pub/sub's Subscribe security shape (SecurityFunc)
  // exactly:
  func(ctx context.Context, req *Req, reqs []route.SecurityRequirement) error

  // Client-side (paired) — writes a credential field INTO *Req before
  // publish, mirrors zeromq pub/sub's Publish security shape
  // (CredentialFunc) exactly:
  func(ctx context.Context, req *Req, reqs []route.SecurityRequirement) error
  ```

- **General-purpose (UNPAIRED) Fn shape — DECIDED this round, not yet
  implemented.** Unlike mqtt5's server-side general-purpose decorator
  (which wraps the RAW, pre-decode `*pahomqtt5.Publish` — a type zeromq
  has no equivalent of), zeromq reqreply has no raw-message-equivalent
  type at all: `[payload]` frames are decoded immediately, before any
  middleware could run against a "raw" form. Both server AND client
  general-purpose decorators therefore wrap the FULL decoded
  request/response handler directly — one shape, both sides, mirroring
  mqtt5's CLIENT-side decorator shape exactly (which already has no raw
  form to wrap either, for the identical reason — a request is encoded/
  decoded, not intercepted mid-flight):

  ```go
  // Server AND client general-purpose (UNPAIRED) — identical shape,
  // wraps the full req/resp dispatch (no raw pre-decode form exists for
  // zeromq to wrap instead, unlike mqtt5's server-side raw *Publish
  // wrap):
  func(next func(ctx context.Context, req Req) (Resp, error)) func(ctx context.Context, req Req) (Resp, error)
  ```

- **Implementation surface — larger than mqtt5's Phase 1, sized
  explicitly**: zeromq reqreply has FOUR transports needing this
  dispatch wiring (`serverTransport`, `clientTransport` — REQ/REP —
  PLUS `routerServerTransport`, `dealerClientTransport` — ROUTER/DEALER),
  vs. mqtt5's Phase 1 TWO (`serverTransport`/`clientTransport` only,
  mqtt5 has no ROUTER/DEALER-equivalent socket family). Each needs its
  own `validateServerImplementationShapes`/`applyGeneralServerMiddleware`
  /`runServerSecurityMiddleware` (server pair) and
  `validateClientImplementationShapes`/a credential-writing equivalent
  (client pair — simpler than mqtt5's `mergeCredentialUserProperties`,
  since the paired Fn writes directly into `*Req` rather than returning
  a separate property list to merge) — mirroring `adapters/mqtt5/
  reqreply_transport.go`'s existing functions structurally, but
  duplicated ×2 for the ROUTER/DEALER variant (consistent with how
  Phase 0/0b's capability-parity work was ALSO duplicated ×2 there, not
  shared — confirmed via `adapters/zeromq/reqreply_transport.go`'s
  existing structure, which already has 4 independent transport types
  with no shared dispatch helpers between the REQ/REP pair and the
  ROUTER/DEALER pair).
- **`api/reqreply` itself needs ZERO changes** — `Route.Use`/`HandleMW`/
  `ClientMW`, `RouteHandle.Implementations`/`ClientImplementations`,
  `CheckCoverage`/`checkImplementationsDeclared` are ALL already
  adapter-agnostic (shipped, mqtt5-proven) — only `adapters/zeromq`'s 4
  transports need new Fn-shape validation/dispatch code, consuming
  those same fields.

### A NEW reflection technique this package hasn't needed before — flagged explicitly

**Confirmed via code, this round**: mqtt5's paired security Fn operates
on the RAW, already-concrete `*pahomqtt5.Publish` (`adapters/mqtt5/
reqreply_transport.go`'s `serverSecurityFnType`/`clientCredentialFnType`)
— it never needed to convert an ERASED `Req` into a `*Req`, since the
raw message is a concrete type known at compile time, unrelated to the
generic `Req`. zeromq's design is different BY NECESSITY (no raw
message exists to operate on instead) — the paired Fn takes `*Req`
directly, where `Req` is only known at Serve/AttachClient RUNTIME (via
`reflect.TypeOf`), inside a reflection-only dispatcher
(`serverTransport.Serve(ctx, routeAny any, fnAny any)` etc.). This
requires a genuinely NEW reflection mechanic for THIS package — flagged
here explicitly (not "just mirror mqtt5") per this codebase's own
"Delivery risk & staging" discipline (`reqreply-middleware.md`'s own
section of the same name, and `d-0001`'s "Lessons Learned" — a
reflection dispatcher's "equivalent to X" claim is a hypothesis until
verified, not a given):

**Server side** (post-decode, pre-handler-dispatch insertion point,
confirmed via `serverTransport.Serve`/`routerServerTransport.Serve`'s
real code — `decodeResults := decodeWithFormatsMethod.CallSlice(...)`
followed immediately by `fnVal.Call([]reflect.Value{spanCtx, reqVal})`):

```go
// reqVal is decodeResults[0] — a reflect.Value holding the DECODED Req
// by VALUE (not a pointer) — confirmed via the real dispatch code.
// Build a fresh, addressable copy, call the paired Fn against ITS
// pointer, then read back whatever it may have mutated:
reqPtr := reflect.New(reqType)   // *Req, zero-valued
reqPtr.Elem().Set(reqVal)         // copy the decoded value in
secResults := securityFnVal.Call([]reflect.Value{
    reflect.ValueOf(ctx), reqPtr, reflect.ValueOf(secReqs),
})
if errI, _ := secResults[0].Interface().(error); errI != nil {
    // reject — mirrors mqtt5's reqreply.SecurityError wrapping (see below)
}
reqVal = reqPtr.Elem() // possibly mutated — this is what fnVal.Call sees next
```

**Client side** (pre-encode insertion point, confirmed via
`clientTransport.call`/`dealerClientTransport.call`'s real code —
`reqVal` passed directly into `EncodeRequestWithFormats` — the SAME
"copy to a fresh pointer, call, read back" mechanic, mirrored, but for
WRITING a credential in rather than just reading):

```go
reqPtr := reflect.New(reqType)
reqPtr.Elem().Set(reqVal)
credResults := credentialFnVal.Call([]reflect.Value{
    reflect.ValueOf(ctx), reqPtr, reflect.ValueOf(secReqs),
})
if errI, _ := credResults[0].Interface().(error); errI != nil {
    // reject client-side, BEFORE anything is sent — mirrors mqtt5's
    // reqreply.SecurityCredentialError wrapping (see below)
}
reqVal = reqPtr.Elem() // now carries the written credential field
// ... proceed to EncodeRequestWithFormats using this reqVal, not the original
```

Both directions are STRAIGHTFORWARD reflection (no generics-in-reflect
trickery beyond `reflect.New`/`.Elem()`/`.Set()`, all standard library
primitives already used elsewhere in this same file for topic-var
merging) — flagged as a new technique for THIS package, not a blocked
or risky one. Verify with a dedicated test exercising BOTH the
read-only case (server Fn returns an error without mutating) AND a
mutating case (server Fn writes an enrichment field, client Fn writes a
credential field) before considering this shipped — a test that only
covers the read-only path would leave the WRITE half of "read/write
access to `*Req`" (this doc's own headline claim, matching pub/sub's
`SecurityFunc`/`CredentialFunc`) unverified.

### Reused error types — NO new zeromq-specific security errors needed

Confirmed via code: `reqreply.SecurityError{Err error}` and
`reqreply.SecurityCredentialError{Scheme string, Err error}`
(`api/reqreply/route.go`) are ALREADY transport-agnostic — mqtt5's
Phase 1 uses them directly (`adapters/mqtt5/reqreply_transport.go:399,
419,792`: `reqreply.SecurityCredentialError{Scheme: name, Err: credErr}`
server-rejection-side, `reqreply.SecurityError{Err: err}`
client-rejection-side). zeromq's implementation reuses BOTH unchanged —
wrap the paired Fn's returned error the SAME way, surfaced via the
existing `ServeError`/`CallError{Kind: KindSecurity}` wrapping
(`KindSecurity` already exists in `adapters/zeromq/errors.go` — 
confirmed, no new `ErrorKind` value needed either).

### Observer + coverage-check wiring — mirrors mqtt5/zeromq pub/sub exactly

- **`stats.SecurityObserver.RecordSecurityRejection`**: zeromq PUB/SUB
  already wires this on rejection (`adapters/zeromq/adapter.go:410`,
  `secObs.RecordSecurityRejection(topic, firstSchemeName(secReqs))`,
  guarded via type-assertion, never embedded — per this codebase's
  Observer Pattern Guardrail). reqreply's 4 transports need the
  IDENTICAL call on EVERY paired-Fn rejection (both server AND client
  side), mirroring `adapters/mqtt5/reqreply_transport.go`'s own
  identical wiring for its Phase 1.
- **`reqreply.CheckCoverage`**: called ONCE, at Serve/AttachServer
  CONSTRUCTION time (not per-message) — the SAME timing mqtt5 uses
  (`adapters/mqtt5/reqreply_transport.go`'s `serverTransport.Serve`,
  right after `validateServerImplementationShapes`). ALL FOUR zeromq
  transports need this: `serverTransport.Serve` and
  `routerServerTransport.Serve` each call it independently (mirrors how
  Phase 0's format-override/`ErrorPattern` work was ALSO duplicated ×2,
  not shared, across these same 4 transports) — a route declaring a
  security scheme with no attached implementation fails loudly at
  Serve/AttachServer/AttachRouterServer time with
  `reqreply.MissingSecurityMiddlewareError`, never silently.
- **Implication for callers**: the APPLICATION's own domain `Req`
  struct must carry a credential field itself (e.g. `Req{..., Token
  string}`) for this to work — mirrors pub/sub's IDENTICAL implication,
  already accepted there; zeromq security remains "in-payload only," a
  documented, accepted transport limitation, not a new one introduced by
  reqreply.

### Unit test plan — SHIPPED, all 12 tests pass

| Test | Verifies |
|---|---|
| `TestAttachServer_HandleMW_PairedSecurityFn_Verifies` (REQ/REP) | paired server Fn runs, can reject with `reqreply.SecurityError` |
| `TestAttachServer_HandleMW_PairedSecurityFn_MutatesReq` | paired server Fn WRITES an enrichment field into `*Req`, visible to the handler afterward — the write-half of the reflection mechanic above, not just read |
| `TestAttachRouterServer_HandleMW_PairedSecurityFn_Verifies` (ROUTER/DEALER) | same, for `routerServerTransport` — confirms the ×2 duplication is correct, not just copy-pasted incorrectly |
| `TestAttachServer_CheckCoverage_MissingSecurityMiddlewareError` (REQ/REP) | a declared scheme with no attached implementation fails at Serve construction |
| `TestAttachRouterServer_CheckCoverage_MissingSecurityMiddlewareError` (ROUTER/DEALER) | same, for `routerServerTransport` |
| `TestAttachClient_ClientMW_PairedCredentialFn_WritesReq` (REQ/REP) | paired client Fn writes a credential field into `*Req` BEFORE encode, visible in the transmitted payload |
| `TestAttachClient_ClientMW_PairedCredentialFn_RejectsBeforeSend` | a rejecting client Fn returns `reqreply.SecurityCredentialError` BEFORE anything is sent over the socket (mirrors mqtt5's identical client-side-reject-before-publish guarantee) |
| `TestAttachDealerClient_ClientMW_PairedCredentialFn_WritesReq` (ROUTER/DEALER) | same, for `dealerClientTransport` |
| `TestAttachServer_HandleMW_GeneralPurpose_AlwaysRuns` | unpaired Fn runs regardless of declared Security (both REQ/REP and ROUTER/DEALER) |
| `TestAttachClient_MultipleGeneralPurposeClientMW_ComposeOutermostIn` | composition order for 2+ general-purpose decorators, direct port of mqtt5's own regression test (caught a REAL bug there — same risk applies here) |
| `TestAttachClient_ClientMW_AppliesToCallAsyncToo` | general-purpose decorator applies through `CallAsync` too (confirmed low-risk: `CallAsync` just calls `.call()` in a goroutine, but still needs a test, not just an inference) |
| `TestAttachServer_HandleMW_SecurityRejection_CallsSecurityObserver` | `stats.SecurityObserver.RecordSecurityRejection` fires on rejection (required adding `RecordSecurityRejection` to the shared `testObserver` fixture in `adapter_test.go` — it didn't implement `stats.SecurityObserver` before this) |

## Files to create/modify — ALL SHIPPED

| File | Change |
|---|---|
| `adapters/zeromq/reqreply_transport.go` (edit, SHIPPED) | Fn-shape validation/dispatch added to all 4 transports: `buildPairedSecurityFnType`/`buildGeneralDecoratorFnType`/`validateServerImplementationShapes`/`validateClientImplementationShapes`/`runPairedServerSecurity`/`runPairedClientCredential`/`applyGeneralServerMiddleware`/`effectiveSecurity` helpers; `reqreply.CheckCoverage` wired into `serverTransport.Serve`/`routerServerTransport.Serve`; `clientTransport.call`/`dealerClientTransport.call` restructured around a `reflect.MakeFunc`-built `innerCall` closure (mirrors mqtt5's Phase 1 technique) so general-purpose `ClientMW` decorators can wrap the full credential→encode→send→recv→decode sequence; the `reflect.New`-based value↔pointer mechanic implemented in both directions |
| `adapters/zeromq/reqreply_transport_test.go` (edit, SHIPPED) | 12 new tests, all passing (see table above) |
| `adapters/zeromq/adapter_test.go` (edit, SHIPPED) | Added `RecordSecurityRejection` to the shared `testObserver` fixture (needed for the `SecurityObserver` test) |
| `examples/reqreply-api/routes/routes.go`, `routes/middleware.go` (edit, SHIPPED) | New `OAuthComputeReq`/`OAuthComputeResp`/`OAuthComputeRoute` + `OAuthMw` (OAuth2 scheme, via `route.OAuth2Scheme`) — resolves the OPEN QUESTION below: extended the example with a NEW demo (Demo 9) rather than Demo 4, since Demo 4's own purpose (concurrent multi-route dispatch) is unrelated to security |
| `examples/reqreply-api/zeromqserver/server.go` (edit, SHIPPED) | Registers `OAuthComputeRoute` via `.Use(OAuthMw).HandleMW(&OAuthMw, handlers.VerifyOAuthComputeZeroMQ)`, new socket pair, new `Built.OAuthHandle` field |
| `examples/reqreply-api/handlers/oauth.go` (NEW, SHIPPED) | `VerifyOAuth2Scopes` (shared, transport-agnostic verification logic) + `VerifyOAuthComputeZeroMQ` (thin zeromq-shaped wrapper) — demonstrates `docs/features/security.md`'s "Sharing a security scheme declaration across REST/events/reqreply" pattern |
| `examples/reqreply-api/demo_cross_api_oauth2_sharing.go` (NEW, SHIPPED, Demo 9) | Calls the zeromq route (reject + accept cases) AND registers the SAME `OAuthMw` value on a throwaway `rest.NewServer`, printing both specs' `securitySchemes.oauth2Compute` entries side by side — proves byte-identical scheme sharing across REST + zeromq reqreply |
| `.github/instructions/go-codex.instructions.md` (edit, SHIPPED) | `adapters/zeromq` row updated with the full reqreply security mechanism description |
| `docs/features/security.md` (edit, SHIPPED) | Reqreply section updated (no longer "MQTT5 only"); new zeromq code sketch; new "Sharing a security scheme declaration across REST/events/reqreply" section |
| `examples/reqreply-api/handlers/handlers.go` (edit, SHIPPED) | New `AddOAuth` handler backing `OAuthComputeRoute` — omitted from an earlier draft of this table, added here for completeness |
| `examples/reqreply-api/main.go` (edit, SHIPPED) | Wires the `demoCrossAPIOAuth2Sharing` call into the demo sequence |
| `examples/reqreply-api/zeromqrouterserver/server.go` (edit, SHIPPED) | Comment-only fix (unrelated demo renumbering from a prior round) |
| `docs/roadmap/reqreply-middleware.md`, `docs/design/d-0004-reqreply-workflow-simplification.md`, `docs/roadmap/index.md` (edit, SHIPPED) | Cross-references updated to reflect zeromq reqreply security shipping |

**Bug found and fixed in a LATER review pass (this doc's own
implementation, PLUS the mqtt5 Phase 1 precedent it mirrored)**: both
`clientTransport.call` and `dealerClientTransport.call`'s
`reflect.MakeFunc`-built `innerCall` closures ignored `args[0]` (the
`ctx` actually passed in by whichever general-purpose `ClientMW`
decorator calls `next(ctx, req)`) and used the STALE, pre-decorator
`ctx` captured from the enclosing `call` function instead — silently
discarding any context mutation (injected value, deadline, span) a
decorator made before dispatch. Confirmed via a background review agent
or grep (`args[0]` had zero hits in either closure) and independently
verified by temporarily reverting each fix and confirming a new
regression test (`TestAttachClient_ClientMW_ContextMutationPropagatesIntoInnerCall`,
one per adapter) fails without it and passes with it. **The SAME bug
existed in `adapters/mqtt5`'s own Phase 1 `innerCall`** (shipped
earlier, unrelated to this round) — inherited faithfully when this
work "mirrored" mqtt5's technique; fixed in BOTH adapters together, not
just zeromq's new code.

**Status of this finding: SHIPPED.** `reqreply-middleware.md`'s own
Phase 1 stays mqtt5-only (unchanged) — THIS doc's own Fn-shape design
(paired + general-purpose, both directions, sized across all 4
transports, PLUS the reflection mechanic, error/observer/coverage
wiring) is now fully implemented and verified here, independently. Full
verification battery (`gofmt`/`go build`/`go vet`/`go test -race`/
`just check`/all examples) — all clean.

## REMINDER for zeromq's own future Fn-shape phase: also remove `SecurityFunc`/`CredentialFunc` then

`reqreply-middleware.md`'s Phase 1 (mqtt5-only, SHIPPED) makes a
BREAKING change mirroring REST's own D-0001 precedent (confirmed via
code: `adapters/nethttp/adapter.go`'s own `"BREAKING: Observer and
SecurityFunc are REMOVED"` doc comment) — REMOVES
`mqtt5.ServeOptions.SecurityFunc`/`mqtt5.CallOptions.CredentialFunc`
ENTIRELY once `.Use`/`HandleMW`/`ClientMW`'s declared
`Implementations`/`ClientImplementations` become the sole credential
mechanism, for BOTH the escape hatch (`Serve`/`Call`/`CallHandle`) and
`Attach`-based dispatch. This reverses `reqreply-middleware.md`'s
Decision #4, which previously followed EVENTS pub/sub's precedent
(keep both mechanisms permanently) instead of REST's.

**This is NOT actioned for zeromq in mqtt5's Phase 1** — zeromq has NO
`.Use`/`HandleMW`/`ClientMW` mechanism yet (that's the Fn-shape work
THIS doc tracks as a follow-up, still not started). **Correction
(confirmed via code, this pass)**: only zeromq's pub/sub side actually
has `events.SubscribeOptions.SecurityFunc`/`PublishOptions.
CredentialFunc` to remove later — zeromq's reqreply `ServeOptions`/
`CallOptions` (used by `Serve`/`Call`/`ServeRouter`/`CallDealer`) NEVER
had `SecurityFunc`/`CredentialFunc` fields to begin with, so there is
nothing to "leave unchanged" there; zeromq reqreply already has ZERO
security mechanism today, matching this doc's own "Confirmed current
state" section above — not a pure regression risk, since removal isn't
what's pending on the reqreply side, ADDING the new Fn-shape mechanism
is.

**But when zeromq's own Fn-shape phase (this doc's own tracked
follow-up) eventually ships** `.Use`/`HandleMW`/`ClientMW` for zeromq's
PUB/SUB side, it should ALSO remove zeromq's pub/sub `SecurityFunc`/
`CredentialFunc` fields at THAT point — mirroring mqtt5's Phase 1
exactly, for the identical reason (REST's precedent: one declarative
mechanism only, no permanent parallel imperative escape hatch living
alongside it). zeromq's REQREPLY side has no equivalent old mechanism
to remove — adding `.Use`/`HandleMW`/`ClientMW` there is a pure
addition, not a removal-plus-addition. Do not repeat events pub/sub's
"keep both forever" choice for zeromq's OWN pub/sub mechanism without
an explicit, reasoned decision to do so — default to REMOVAL, matching
REST/mqtt5's now-established precedent, unless a genuine new reason to
keep both emerges at that time.
