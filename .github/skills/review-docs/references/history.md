# go-codex Documentation Review History (DR1–)

Do not re-report any findings listed here. They have been implemented.

---

## Round DR13 (post-review-go-codex-R147 verification sweep — nav/README/instructions sync + cross-link correctness)

Triggered immediately after `review-go-codex` Round 147 fixed a large, repo-wide
`nethttp.CallWithHandle` documentation-staleness bug (71 mentions across 17 files). This round
verified that fix's completeness from the documentation-sync angle (nav, README, instructions.md,
cross-link correctness) rather than re-auditing API accuracy (already covered by R147).

- **Nav completeness (checklist §1) — CONFIRMED CLEAN, no finding.** `zensical.toml`'s 93 `*.md`
  nav entries vs. `find docs/ -name "*.md"`'s 93 actual files: exact 1:1 match, zero dangling
  entries, zero orphaned files (confirms the earlier middleware-consolidation.md nav-entry removal
  this session was done correctly).
- **README/instructions.md/reference-index sync (checklist §9-10) — CONFIRMED CLEAN, no finding.**
  `middleware` package's rows in `README.md`, `.github/instructions/go-codex.instructions.md`, and
  `docs/reference/index.md` are all already accurate post-middleware-consolidation (Security-only
  shrink correctly reflected everywhere); no stale `CallWithHandle` mentions in any of README.md,
  `docs/get-started.md`, `docs/index.md`, or `docs/reference/project-structure.md`.
- **D1 [bug] — 3 dangling same-directory-assumed relative links in `docs/design/
  d-0006-protocol-native-capabilities.md`**: `[...](zeromq-rest-adapter.md)` (×2),
  `[...](declarative-workflow-tutorials.md)`, `[...](design-doc-compaction.md)` — all 3 targets
  actually live in `docs/roadmap/`, not `docs/design/` (d-0006's own directory), so the links
  resolved to nonexistent `docs/design/<name>.md`. Fixed all 3 to `../roadmap/<name>.md`.
- **D2 [bug] — `docs/guides/ports.md:383`**: `[app.App.Supervise](app.md)` — missing the
  `../features/` prefix present on the SAME page's 2 other correct links to the same file. Fixed.
- **D3 [small] — `docs/roadmap/mcp-ports-declarative-middleware.md`**: its own status header links
  `[Declarative Middleware](declarative-middleware.md)` while the SAME sentence correctly says
  "(now DELETED...)" — a dead link pointing at a file the prose itself says doesn't exist. Changed
  to a plain, non-linked filename mention (matches the established convention for citing deleted
  docs elsewhere in the repo).
- **D4 [bug] — `docs/roadmap/declarative-workflow-tutorials.md:285`**: `[...](../../instructions/
  agent-skills.instructions.md)` — resolves to `<repo-root>/instructions/...` (missing the
  `.github/` path segment the real file lives under: `.github/instructions/
  agent-skills.instructions.md`). Fixed to `../../.github/instructions/agent-skills.instructions.md`.
- **Accuracy guardrail spot-check (checklist §2-3) — CONFIRMED CLEAN, no finding.** Checked all
  `codex.Field[T,V]{...}` / `Codec: &c`-shaped mentions found via grep across `docs/`: the
  `codex.Field[T,V]` ones are return-type signatures (not struct-literal construction — correct);
  the `Codec: &c` ones (`ports.EntryParam` — a type alias for `codex.Param`, `events.TopicParam`
  shown as a from-scratch struct literal) are legitimate alternate constructions for types that
  are plain data structs, not violations of the `.WithCodec(c)` idiom guardrail.

`zensical build` is not available in this environment (CLI not installed) — substituted a custom
Python nav-sync check (93/93 exact match) and a full-repo markdown-link resolver (confirmed only 1
false positive remaining, inside a Go code-block comment the regex misparsed — `docs/features/
events.md`'s `capabilities.md` link is real and resolves correctly). `go build ./...`/`go test
./...` (56 packages, zero failures)/`just check` (0 issues/501 files) all clean.

---

## Round DR12 (cross-cutting surfaces — README, project-structure.md, zensical.toml nav, go-codex.instructions.md, docs/index.md, reference/index.md)

Scoped pass over the 5 shared/cross-cutting surfaces (Phase 8 item 2.4 of
`docs/design/d-0006-protocol-native-capabilities.md`), the final review-docs
sub-item — runs last so it can verify nav/cross-link consistency after
2.1-2.3's per-API fixes.

- **D1 — `README.md`'s "Layer 2" code sample called `createUser.Register(builder)`
  as `handle, _ := ...`** [bug]: `rest.Route.Register` returns ONLY `error`
  (confirmed via `api/rest/builder.go`); the two-value form is a literal
  compile error. Fixed to `RegisterHandle` (the `(*RouteHandle, error)`-returning
  method).
- **D2 — `zensical.toml` nav pointed at 4 nonexistent files** [bug]: `"features/
  reqreply-middleware.md"` (renamed to `features/codec-declared-middleware.md`,
  confirmed via the file's own title "Codec-Declared Middleware — REST, Events
  & ReqReply" — an orphaned file with no nav entry at all) and 3 stale roadmap
  entries (`roadmap/events-pubsub-consolidation.md`, `roadmap/
  mqtt5-user-property-merge.md`, `roadmap/thin-adapters-audit.md` — none exist,
  none referenced anywhere else, confirmed fully superseded/removed). Fixed the
  first by repointing nav to the real file; removed the 3 dead entries. Also
  found `docs/roadmap/idea-codec-defined-hateoas.md` — a real, substantial
  (1000-line) file with NO nav entry and NO `roadmap/index.md` table row;
  added both.
- **D3 — `docs/reference/index.md` repeated `api/reqreply`'s "Round DR11"
  stale-API bugs** [bug]: `Route.Register(b) *RouteHandle` (wrong — same
  Register/RegisterHandle confusion as D1), `adapters/mqtt5`'s "`Serve` +
  `Call` (request-reply)" and `adapters/zeromq`'s "`Serve`/`Call` (REQ/REP) +
  `ServeRouter`/`CallDealer`" (none of these standalone functions exist
  anymore — confirmed removed in DR11). Fixed to `NewServerTransport`/
  `NewClientTransport`/`NewRouterServerTransport`/`NewDealerClientTransport`
  consumed via `Server.Attach`/`Client.Attach`.
- **D4 — coverage gaps across README/reference/index.md** [small]: `middleware`
  (a real top-level package, confirmed documented in
  `go-codex.instructions.md`'s Package Structure table but entirely absent
  from README's directory summary + import table, `docs/reference/
  index.md`'s Core table + quick-import table, AND `docs/reference/
  project-structure.md`'s full tree) and `adapters/{mcprest,openai,file,
  redis,websocket}` (all real, all missing from one or more of README's
  directory summary/import table and `docs/reference/index.md`'s Adapters
  table/quick-import table) — added throughout all 4 locations.
- **D5 — `middleware` package has NO `doc.go`** [small]: violates the
  established "every package under the module root has one" guardrail
  (confirmed via `ls middleware/` — genuinely missing, not just thin).
  Created `middleware/doc.go` covering all 4 sub-concerns (Middleware/
  Declaration, SecurityScheme, Disposition, ContextField).
- **D6 — `adapters/file`'s package doc comment lived in `binding.go`, not
  `doc.go`** [trivial]: same guardrail, softer violation (content existed,
  wrong file). Moved the existing comment into a new `adapters/file/doc.go`
  (mechanical split, zero content change).
- **D7 — `.github/instructions/go-codex.instructions.md`'s `api/reqreply`
  Package Structure table row repeated the SAME stale
  `AttachServer`/`AttachClient`/`AttachRouterServer`/`AttachDealerClient`/
  standalone-`Serve`/`Call`/`ServeRouter`/`CallDealer` naming DR11 fixed
  everywhere else** [bug]: this is a very large (~29KB), deeply historical
  single table cell narrating the reqreply workflow-simplification's
  multi-phase migration — most of the historical "X was later REMOVED/
  DELETED" framing is already correctly past-tense and accurate as written.
  Fixed only the clearly-wrong PRESENT-TENSE claims describing removed
  symbols as currently callable (the REQ/REP "escape hatch" paragraph, the
  per-call-format-override paragraph, 3 dead-letter/coverage-check
  sentences) via targeted, high-confidence mechanical replacement — left
  the surrounding historical narrative prose untouched. **A full rewrite of
  this file's verbose historical content is explicitly the separate,
  already-accepted `docs/roadmap/design-doc-compaction.md` roadmap's job,
  not this pass's** — scoped deliberately narrow here to avoid duplicating
  that effort.
- **Verified clean (no action needed)**: `docs/get-started.md` (already
  accurate, includes `api/reqreply`, `Format.Unmarshal` signature matches);
  `docs/reference/project-structure.md`'s adapters/ subtree (already fully
  synced with actual directory layout, confirmed via diff).
- **Also fixed**: `docs/index.md`'s "API contract" layer row and "What you
  get" bullets omitted `api/reqreply` entirely (a fully shipped Layer-2
  feature) — added both a table mention and a dedicated bullet.

---

## Round DR11 (api/reqreply — stale Attach/AttachServer/AttachClient naming + dead escape-hatch examples)

Scoped pass over `api/reqreply` + `adapters/mqtt5`/`zeromq` reqreply-side
docs/godoc/examples (Phase 8 item 2.3 of
`docs/design/d-0006-protocol-native-capabilities.md`). Same root-cause
class as DR9/DR10: an `AttachServer`/`AttachClient`/`AttachRouterServer`/
`AttachDealerClient`/standalone-`Serve`/`Call`/`ServeRouter`/`CallDealer`
convenience-function generation was fully REMOVED (zero backdoor
directive) in favor of `NewServerTransport`/`NewClientTransport`/
`NewRouterServerTransport`/`NewDealerClientTransport` constructors
consumed via `Server.Attach`/`Client.Attach` — but docs/godoc across the
whole reqreply surface still taught the removed names/functions.

- **D1 — stale `mqtt5.Attach`/`zeromq.Attach`/`AttachServer`/`AttachClient`/
  `AttachRouterServer`/`AttachDealerClient` naming** [bug]: fixed across
  `api/reqreply/{client,builder,route,doc}.go`, `api/events/builder.go`
  (one reqreply cross-reference), `docs/guides/{mqtt5,zeromq,
  error-handling}.md`, `docs/features/{security,codec-declared-middleware,
  capabilities,observer,ports}.md`, `docs/concepts/api-contracts.md`,
  `examples/reqreply-api/{mqtt5server,zeromqserver}/server.go`,
  `examples/reqreply-api/{demo_error_pattern,
  demo_user_property_param_middleware,
  demo_zeromq_dealer_router_variant}.go`,
  `examples/reqreply-api/routes/routes.go`,
  `api/reqreply/example_server_client_test.go`.
- **D2 — dead escape-hatch examples calling removed standalone
  `Serve`/`Call`/`ServeRouter`/`CallDealer` functions** [bug]: an entire
  guide section in `docs/guides/mqtt5.md` ("Escape hatch: Serve/Call
  directly") and `docs/guides/zeromq.md` (two sections) taught calling
  functions that no longer exist at all (confirmed via repo-wide grep —
  zero `func Serve(`/`func Call(`/`func ServeRouter(`/`func CallDealer(`
  remain in either adapter). Rewrote both as "per-route/per-call
  customization via `ServerTransportOptions.Serve`/
  `ClientTransportOptions.Call` at attach time" — the actual current
  mechanism. Also fixed a real invented-API bug introduced then caught
  mid-round: `reqreply.ClientCallOptions` has NO `Vars` field (only
  `RequestFormats`/`ResponseFormats`) — per-call template topic vars are
  derived automatically from the request struct via
  `reqreply.NewTopicParam` merge fields, mirroring `events.NewTopicParam`;
  corrected the guide's template-topic example to use a merge field
  instead of a nonexistent `ClientCallOptions.Vars`.
- **D3 — stale "v1 scope" claim in `adapters/zeromq/reqreply_transport.go`**
  [bug]: `serverTransport`/`clientTransport`/`routerServerTransport`'s own
  godoc claimed `RequestFormats`/`Formats`/`ErrorPattern` were "NOT
  honored" and recommended "use `[Serve]` directly" as an escape hatch —
  verified FALSE by reading `Serve`'s actual body (it calls
  `DecodeWithFormats`/`EncodeWithFormats`/`ObserveErrorResponseFor`/
  `DeadLetterFor` throughout) AND confirming the recommended escape hatch
  function doesn't exist. Rewrote to describe the actual shipped
  capability parity, mirroring `adapters/mqtt5`'s own already-correct
  "Capability parity ... SHIPPED" wording.
- **D4 — misc stale/wrong references caught while sweeping** [bug/small]:
  `docs/features/observer.md`'s `mqtt5.Server.Serve(...)` (wrong
  qualifier — `Server` is a `reqreply` type, not `mqtt5`'s); a stale
  `CredentialFunc` field mention in `adapters/mqtt5/reqreply_transport.go`'s
  `ClientTransportOptions` doc (removed, replaced by `Capabilities`); bare
  `[Serve]`/`[Call]` godoc bracket links on `ServeOptions`/`CallOptions`
  type comments in `adapters/mqtt5/reqreply.go` and
  `adapters/zeromq/adapter.go`; `docs/concepts/api-contracts.md`'s stale
  `mqtt5.CallHandle`/`zeromq.CallHandle` references (never existed as
  standalone functions).
- **Deferred (below the bug/small bar given round size)**: ~150
  `TestAttachServer_*`/`TestAttachClient_*`/`TestAttachRouterServer_*`/
  `TestAttachDealerClient_*`-named test functions and matching
  `t.Fatalf("AttachServer: %v", err)`-style internal error labels across
  `adapters/zeromq/{reqreply_transport,dead_letter_reqreply,
  capability_reqreply}_test.go` — internal test identifiers only, no
  pkg.go.dev/docs-site visibility, purely cosmetic if renamed; explicitly
  deferred rather than doing a ~150-occurrence mechanical rename for zero
  user-facing value.

---

## Round DR10 (api/events — stale Attach naming + broken positional qos/retained examples)

Scoped pass over `api/events` + `adapters/mqtt`/`mqtt5`/`zeromq` docs/godoc/
examples (Phase 8 item 2.2 of `docs/design/d-0006-protocol-native-capabilities.md`).
Two root causes, both more severe than the REST pass's D1: (1) the same
`Attach`-suffixed-helper → `NewTransport(...)` + `Client.Attach(...)` rename
DR9 fixed for REST, never swept for events; (2) Phase 5's "zero backdoor"
redesign REMOVED positional `qos byte`/`retained bool` call-time parameters
from `NewSubscribeTransport`/`NewPublishTransport` entirely (replaced by a
`Capabilities []Capability` field), leaving many docs with literally
non-compiling example code — including a `Channel[T].Register(client)` call
that has never existed on `Channel[T]` (only `Subscriber[T]`/`Publisher[T]`,
reached via `.WithSubscribe(...)`/`.WithPublish(...)`, have `Register`/`Handle`).

- **D1 — stale `mqtt.Attach`/`mqtt5.Attach`/`zeromq.Attach` naming** [bug]:
  fixed across `api/events/{doc.go,builder.go}`, `adapters/{mqtt,mqtt5,
  zeromq}/doc.go`, `adapters/mqtt/connect_security.go`,
  `docs/concepts/ports-and-adapters.md` (events-scoped portion). Current
  pattern is `client.Attach(<adapter>.NewTransport(...))`.
- **D2 — impossible `Channel[T].Register(client)`/stale channel-declaration
  examples** [bug]: rewrote `docs/features/events.md`'s "Declaring channels"
  section and every other `NewChannel(...).Register(...)` call site found
  repo-wide (`docs/features/{asyncapi,ports}.md`, `docs/guides/error-handling.md`,
  `docs/concepts/pipelines.md`, `docs/what-is-go-codex.md`) to the correct
  `NewChannel(...)` → `.WithSubscribe(...)`/`.WithPublish(...)` →
  `.Handle(client)` (spec-only) or `.WithHandler(fn).Register(client)`
  (handler + registration) pattern.
- **D3 — stale positional `qos`/`retained`/`router` transport-constructor
  calls** [bug]: removed stale positional args and, where the original
  example specified a non-default value, added the replacement
  `Capabilities: []<pkg>.Capability{<pkg>.QoS(n)}`/`Retained(true)` field
  across `docs/features/{events,error-handling,observer,security}.md`,
  `docs/guides/{mqtt,mqtt5,observer,stream}.md`, `docs/concepts/{codec-as-contract,
  observable-layers}.md`, and each adapter's own godoc (`adapters/mqtt/
  {adapter.go,doc.go,topicvars.go,connect_security.go}`) — including a
  self-contradicting bug where `adapters/mqtt/adapter.go`'s own
  `PublishOptions.Capabilities` field doc (correctly describing the
  removal) sat next to a stale example a few lines below still using the
  removed signature. `adapters/mqtt5`'s `router` positional argument
  (unrelated to qos/retained, still required) was preserved everywhere.
- **D4 — false "v1-scoped"/removed-field security claims** [bug]: corrected
  `docs/features/security.md`'s claim that `mqtt5.Attach + Client.Subscribe`
  is "v1-scoped and does NOT enforce SubscribeMW" — verified via
  `adapters/mqtt5/transport.go`'s own doc comment that `Client.Attach` +
  `Client.Subscribe`/`Publish` are FULL-FEATURED (Phase 4e closed that gap).
  Also fixed stale `SubscribeOptions.SecurityFunc`/`PublishOptions.CredentialFunc`
  field references (removed, replaced by security-shaped `SubscribeMW`/
  `PublishMW`-attached Fns) in `docs/guides/mqtt5.md` and
  `adapters/mqtt/{doc.go,connect_security.go}`.
- **Also fixed while sweeping**: `docs/features/redis.md` and
  `docs/features/ports.md`'s stale `channel.ClientHandle()` references
  (that method never existed on `Channel[T]` — corrected to
  `sub.Handle(nil)`/`pub.Handle(nil)`), and `examples/gob-contract/main.go`'s
  own stale `adapters/mqtt.Attach`/`nethttp.Attach` comment references.

---

## Round DR9 (api/rest — stale AttachMux/AttachRouter/nethttp.Attach sweep)

Scoped pass over `api/rest`-owned docs/godoc/examples (Phase 8 item 2.1 of
`docs/design/d-0006-protocol-native-capabilities.md`). Root cause: an
earlier Phase 4d rename (`nethttp.AttachMux(builder, mux, addr)` →
`nethttp.NewServerTransport(...)` + `builder.Attach(...)`; similarly for
`chi.AttachRouter`/client-side `nethttp.Attach`) was applied correctly in
code everywhere, but documentation and even some exported godoc comments
were never fully swept.

- **D1 — pervasive stale `AttachMux`/`AttachRouter`/`nethttp.Attach`
  references** [bug]: fixed across `docs/concepts/{observable-layers,
  codec-as-domain-boundary,api-contracts,codec-as-contract,
  ports-and-adapters}.md` (REST-scoped portions only), `docs/guides/
  {http-server,http-client}.md`, `docs/features/{http-client,security,
  rest-api,sse-streaming}.md`, `docs/guides/openapi.md`,
  `examples/rest-api/*` stale code comments (server/client/routes/util/
  demo files — code itself was already correct), and — most notably —
  **`api/rest/builder.go`'s own exported godoc** (`NewClient`,
  `Client.Attach`, `Client.Call`/`Client.Consume` examples,
  `ServerTransport`/`ClientTransport` interface docs, `Server.transport`
  field doc, `Server.Serve`'s embedded code example) plus
  `api/rest/middleware.go` and `api/rest/builder_test.go` godoc bracket-
  links, `adapters/nethttp/{doc.go,stream.go,client_test.go}`,
  `adapters/chi/{doc.go,adapter_test.go}`. ASCII-box diagrams in
  `observable-layers.md`/`codec-as-domain-boundary.md` required exact
  width recalculation to preserve alignment after text-length changes.
- **D2 — `docs/guides/http-server.md` fictional `ErrorResponse[...]`
  roadmap block** [bug]: replaced with an accurate description of the
  actually-shipped `rest.ErrorPattern`/`ErrorStatus`/`ErrorAction`
  mechanism.
- **D3 — false "v1-scoped" claims about `rest.Client.Call`** [small]:
  corrected in `docs/guides/http-client.md`, `docs/features/http-client.md`,
  and `docs/features/security.md` — `Client.Call`/`Client.Consume` are
  full-featured (path/query/header/cookie params, security/credential
  `ClientMW`, per-call format override, error-pattern decoding); the real
  distinction from `CallWithHandle` is handle-vs-route-value ergonomics,
  not a feature gap.
- **D4 — `docs/guides/openapi.md` stale example description** [small]:
  corrected "low-level `DocumentBuilder`" to `Server.OpenAPISpec()`
  (verified against `examples/rest-api/main.go`).
- D5 (missing named `adapters/nethttp.ExampleCall()`) — reviewed and
  DEFERRED: `Example()` in `client_test.go` already covers client-side
  path-param validation; adding a fully-scaffolded `ExampleClient_Call`
  mirroring `ExampleClient_Consume` is worthwhile future work but was
  judged out of this round's bug/small bar given the round's already
  large D1 scope.

Verification: `gofmt`/`go build`/`go vet`/`go test ./...`/`just check` all
clean (one flaky, timing-based `TestChiSSEAdapter_ServesItemsToClients`
failure reproduced as pre-existing and unrelated — passed on retry).

---

## Round DR8 (D-0006 capabilities reference doc + nav sync)

Docs gap discovered while answering a user question about D-0006's scope: the
graduated `Capability` mechanism had no dedicated feature page, and
`zensical.toml` nav was out of sync with D-0006's graduation from roadmap to
design doc.

- **D1 — no `docs/features/capabilities.md`**: added. New feature page
  explaining the sealed, per-adapter `Capability` mechanism, a per-adapter
  capability reference table (`adapters/mqtt`/`adapters/mqtt5`: QoS,
  Retained; `adapters/zeromq`: HWM, Conflate), not-yet-migrated call-time
  options (`ContentType`, `UserProperty`), surveyed-but-unshipped items,
  and a "why not REST/Security/reqreply" section explaining the two-part
  test that keeps those on different, already-documented mechanisms.
- **D2 — `zensical.toml` `[nav."Design Documents"]` missing D-0006**: added
  `"— D-0006: Protocol-Native Capabilities" = "design/d-0006-protocol-native-capabilities.md"`.
- **D3 — `zensical.toml` `[nav.Roadmap]` dead link**: removed
  `"— Protocol-Native Feature Declarations" = "roadmap/protocol-native-features.md"`
  — the doc graduated to `docs/design/d-0006-protocol-native-capabilities.md`
  and the roadmap file no longer exists.
- **D4 — `zensical.toml` `[nav.Features]` missing capabilities entry**:
  added `"Protocol-Native Capabilities" = "features/capabilities.md"`.
- Cross-linked the new page from `docs/features/events.md`,
  `docs/guides/mqtt.md`, `docs/guides/mqtt5.md`, `docs/guides/zeromq.md`
  (`See also`/`Feature:` lines).

Pre-existing, unrelated broken nav links noted but left untouched (out of
scope for this round): `features/reqreply-middleware.md`,
`roadmap/events-pubsub-consolidation.md`,
`roadmap/mqtt5-user-property-merge.md`,
`roadmap/observer-param-error-consolidation.md`,
`roadmap/thin-adapters-audit.md` — all referenced in nav but absent from
`docs/`.

---

## Round DR7 (error-path ergonomics — docs sync across all boundaries)

Docs work accompanying the error-path-ergonomics feature (Phases 1A–1D + Phase 2). Not a
standalone review round — recorded so future doc reviews don't re-flag these as gaps. The design
roadmap doc that originally tracked this work (`docs/roadmap/error-path-ergonomics.md`) has since
been REMOVED — every phase shipped, and the feature-doc sections below plus
`.github/skills/review-go-codex/references/history.md` (Rounds 64–65) are now the durable record.

- **D1 — `docs/features/rest-api.md` had no error-path-ergonomics section**: added, covering
  `rest.ErrorStatus`/`rest.ErrorPattern` (direct/mapped modes), the `.WithAction` action selector
  table, and header/cookie parity with the happy path.
- **D2 — `docs/features/events.md` had no error-path-ergonomics section**: added, covering
  `events.ErrorChannel`, the three-way action model, and adapter wiring notes for
  `mqtt5`/`mqtt`/`zeromq` `PublishAdapter`.
- **D3 — `docs/features/websocket.md` had no error-path-ergonomics section**: added, covering
  `websocket.ErrorFrame`, broadcast semantics, and the action model.
- **D4 — `docs/features/mcp.md` had NO error-path-ergonomics section at all (feature didn't exist
  yet)**: added, covering `mcp.ErrorPattern` and the `adapters/mcpgo.ToolHandler` wiring.
- **D5 — `docs/guides/error-handling.md` had no store/IO boundary guidance**: added "Store/IO
  boundaries (SQL, Cache, File)" section documenting the `OnError` + `events.ErrorChannel`
  composition pattern (no new adapter API — see checklist.md §13 in `review-go-codex`).
- **D6 — `docs/guides/asyncapi.md` and `docs/concepts/api-contracts.md` described `ErrorReplyMeta`
  as the only req/reply error declaration**: updated both to document `reqreply.ErrorPattern` as
  the recommended runtime-wired declaration, with `ErrorReplyMeta` demoted to "spec-only, no runtime
  dispatch" alternative.
- **D7 — no runnable example demonstrated any error-path feature except REST**: extended
  `examples/adapters-mqtt5`, `examples/websocket-duplex`, and `examples/redis-cache` — see
  `review-go-codex` skill's history.md Round 64 (G8) for details.
- **D8 — `.github/instructions/go-codex.instructions.md` bullets for `api/rest`, `api/events`,
  `api/reqreply`, `api/mcp`, `adapters/nethttp`/`chi`/`mqtt5`/`mqtt`/`zeromq`/`mcpgo`/`websocket`
  didn't mention any of the above**: all updated with concise error-path-ergonomics summaries.

---

## Round DR6 (format/file docs sync after EntrySlice additions)

- **D1 — `docs/features/formats.md` flat-key-patch link described only "four patterns"**: Updated to enumerate all 11 current sections including EntrySlice single-segment key, multi-field key extraction, and static key injection added in this session.
- **D2 — `docs/features/formats.md` missing `codex.EntrySlice` mention**: Added "Merging JSON object keys into decoded values" paragraph with a cross-link to the EntrySlice section in `docs/concepts/codec.md`.
- **D3 — `format/file_test.go` missing `ExampleNewFile()`**: Added `ExampleNewFile()` with `// Output:` demonstrating static path, `Write`, and `Read` round-trip.

---

## Round DR5 (docs sync after R28–R29: slog.LogValuer parity + reqreply rename)

- **D1 — `docs/reference/index.md` mqtt5 row names `ServeRequestReply`+`Request`**: Updated to `Serve`+`Call` to match R29 rename.
- **D2 — `docs/concepts/observable-layers.md` stale `(ServeRequestReply)` annotation**: Changed to `(Serve)` and `(Call)`.
- **D3 — `docs/guides/mqtt5.md` section heading `### Requester (Request)`**: Renamed to `### Caller (Call)`; also fixed inline `// Request — returned directly` comment.
- **D4 — `docs/features/error-handling.md` missing MQTT 5.0 adapter errors section**: Added `## MQTT 5.0 adapter errors` section covering `CallError`, `ServeError`, `BrokerError`, `UserPropertyError`, `MissingUserPropertyError`; updated MQTT 3.1.1 section to include `SubscribeError`, `PublishEncodeError`, `TopicMismatchError`.
- **D5 — `docs/features/error-handling.md` missing `slog.LogValuer` notes for mqtt/mcp error tables**: Added explicit note that all MQTT 3.1.1, MQTT 5.0, and MCP error types implement `slog.LogValuer`; added `ResourceEncodeError` and `InvalidResourceParamError` to MCP table.
- **D6 — `docs/features/error-handling.md` missing reqreply route param errors**: Added `## Request-reply route errors` section covering `RouteParamError`, `MissingRouteParamError`, `DuplicateRouteError` and their relationship to `CallOptions.Vars`.

---

## Round DR4 (README + reference sync after binary additions + R22)

- **D1 — README "Multi-format" bullet missing Binary**: Added "Binary (raw bytes)" to the feature bullet listing JSON, YAML, TOML, Gob.
- **D2 — README "Builtin constraints" bullet missing binary file format validators**: Added "binary file formats (png, jpeg, pdf, zip, …)" to the constraints bullet.
- **D3 — README primitives.go comment missing Base64**: Added `Base64` alongside `Bytes` in the Project Structure tree inline comment for `primitives.go`.
- **D4 — README format/ description missing Binary**: Updated format/ tree entry description and `format.go` inline comment to include `Binary()`.
- **D5 — docs/reference/index.md validate/format descriptions stale**: Updated `validate` row to mention binary file format constants; updated `format` row to mention Binary and File I/O.
- **D6 — docs/features/formats.md missing PathParamSchemas/ValidatePathVars**: Added pre-flight introspection section documenting `ValidatePathVars` and `PathParamSchemas()` (added in R22).

---

## Round DR3 (Binary codec, validators, and format.Binary docs)

- **`docs/features/formats.md` Binary section added**: New "Binary — raw binary file I/O and HTTP bodies" section with Gob vs Binary comparison table, `format.Binary` wiring example, built-in format constraint table (`validate.PNG/JPEG/GIF/WebP/PDF/ZIP`), and `codex.Bytes` vs `codex.Base64` table.
- **`docs/guides/mqtt.md` binary payloads section**: New "Binary payloads" section explaining how `format.Binary` + `WithFormats` works with MQTT, covering size limits, no content-type in MQTT 3.1.1, and error handling.
- **`docs/guides/http-server.md` binary payloads section**: New "Binary payloads" section covering incoming binary request bodies (`WithRequestFormats`), outgoing binary responses (`WithFormats`), and the `MaxBodyBytes` ↔ `validate.MaxBytes` ordering subtlety.
- **`docs/guides/http-client.md` binary section**: New "Binary requests and responses" section covering client-side binary request encoding and binary response decoding via `nethttp.Call`.
- **`validate/doc.go` updated**: Added "Binary byte constraints" and "Binary file format constraints" sections; new "When to use which" and "Composition and ordering" sections.
- **`codex/doc.go` updated**: Added "Binary codecs — Bytes vs Base64" section with use-case table and code examples.
- **`format/doc.go` updated**: Added `Binary`; explains Binary vs Gob vs `NewTyped` relationship.
- **`go-codex.instructions.md` updated**: Primitives list includes `Base64` and raw `Bytes`; validate entry lists `HasPrefix` and binary format constants; format entry includes `Binary`.

---

## Round DR2 (File I/O + FromEnvVar + FileObserver docs gaps)

- **D6 — `docs/features/formats.md` missing File[T] section**: Added "File I/O — declarative typed file access" section covering `NewFile`, `FilePathParam.WithCodec`, `FileOptions`, `Read`/`Write`/`Update`/`BuildPath`, static paths, typed file errors table, and `FileObserver` hook.
- **D7 — `docs/features/config.md` missing FromEnvVar**: Added "Single env var (FromEnvVar)" section with typed example, `EnvVarError` handling, and distinction from `FromEnv`.
- **D8 — `docs/features/observer.md` missing FileObserver**: Added `FileObserver` row to interface table, new "FileObserver (ports.File)" section with full implementation example, and added `"file"` to the observer location table; updated `guides/observer.md` location table.
- **D9 — `go-codex.instructions.md` format+stats entries stale**: Updated `format` row to include `File[T]`, `NewFile`, `FilePathParam`, `FileOptions`, all file error types, `FromEnvVar`, and `EnvVarError`; updated `stats` row to include `FileObserver` as 5th optional interface.
- **D10 — `docs/reference/index.md` stats row incomplete**: Added `SecurityObserver` and `FileObserver` to the stats package description.
- **D11 — no `TestFromEnvVar` in `format/env_test.go`**: Added 5 tests covering happy path (int, string), unset-returns-zero, invalid value returns `EnvVarError`, and `Unwrap()` exposes inner error.
- **D12 — `docs/guides/config.md` missing File[T] and FromEnvVar**: Added "Declarative file I/O" and "Single env var" sections with code examples and feature-page links.
- **D13 — `review-go-codex` checklist missing file symbols**: Added `FilePathParam` to param types table; added `FileObserver` to observer interface table with guard rule; added `format` package error table (`FilePathParamError`, `MissingFilePathVarError`, `FileReadError`, `FileDecodeError`, `FileEncodeError`, `FileWriteError`, `EnvVarError`).

---

## Round DR1 (doc.go quality + concept cross-links)

- **D1 — `api/internal/doc.go` thin (3 lines)**: Expanded to 13 lines documenting `ParseTemplateVars`, `StripTemplateVars`, and `BuildFromTemplate` with their roles in template-transparent validation.
- **D2 — `render/jsonschema/doc.go` thin (8 lines)**: Expanded to 18 lines documenting the `Schema()` function, its use by `api/mcp`, and its relationship to `render/internal/schemarender`.
- **D3 — `render/internal/schemarender/doc.go` thin (6 lines)**: Expanded to 18 lines documenting `SchemaObject`, the single-change design rationale, and the `AdditionalPropertiesSchema` field precedence rule.
- **D4 — `docs/concepts/api-contracts.md` See also links stale**: Four `guides/` links replaced with correct `features/` links (REST API, HTTP Client, Events, MCP, API Builders).
- **D5 — `docs/concepts/codec.md` See also links stale**: `guides/error-handling.md` replaced with `features/error-handling.md`.

---

<!-- New rounds go here, above the previous round. -->
