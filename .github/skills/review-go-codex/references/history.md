# go-codex Review History (R1–R74)

Do not re-report any of these findings. They have been implemented and tested.

---

## Round 74 (`examples/adapters-mcp` stdio stdout-pollution bug)

Discovered while evaluating "can go-codex build a stdio MCP server instead of HTTP" —
architecturally YES (`mcpgo.RegisterTool`/`RegisterResource`/`RegisterPrompt` only wire onto a
`*server.MCPServer`, fully transport-agnostic; `server.ServeStdio(s)` from the underlying
`mark3labs/mcp-go` library works directly), but the SHIPPED example had a real bug making it
unsafe to actually point a real client at with `SERVE=1`:

- **G5 — `examples/adapters-mcp` logged to stdout even in stdio-serving mode**: the demo logger
  (`slog.NewTextHandler(os.Stdout, ...)`) and its `stats.NewLoggingObserver` were constructed once
  in `main()` and reused for BOTH the in-process demo simulation AND the real `RegisterTool`/
  `RegisterResource`/`RegisterPrompt` calls feeding `ServeStdio(s)`. Stdio transport requires stdout
  to carry ONLY the JSON-RPC protocol stream — any stray log line (or the unconditional demo output:
  MCPSpec dump, simulated calls, observer summary, all printed BEFORE the SERVE=1 check) corrupts
  message framing for a real connected client. Fixed by splitting `main()` into `runDemo` (unchanged
  behavior, stdout logger — safe, no client attached in demo mode) and `runServer` (stdio path,
  logger + observer pointed at `os.Stderr` exclusively), with the `SERVE=1` branch checked BEFORE any
  demo output runs. Verified end-to-end: piped a real `initialize` + `tools/call` JSON-RPC sequence
  into `SERVE=1 go run ./examples/adapters-mcp` and confirmed stdout contained ONLY the two JSON-RPC
  response frames, with all logging (including the "calculate succeeded"/`RecordRequest` observer
  lines from the tool call) landing on stderr.
- Docs updated: `docs/features/mcp.md`'s Transport options section gained an explicit "stdio
  requires stdout reserved for the protocol" warning, cross-referencing the example's `runServer`/
  `runDemo` split as the reference pattern.

---

## Round 73 (`api/rest` merge-field constructor test gap — codebase-memory graph analysis)

Discovered via a code-knowledge-graph audit (the `codebase-memory` MCP server, newly available
this session) cross-checking every documented layering/Pattern-acceptance rule against the actual
import graph — all held with zero violations EXCEPT one isolated test-coverage gap:

- **G4 — `NewRequiredQueryParam`/`NewRequiredCookieParam` had zero test coverage**: of the twelve
  declare-once merge-field constructors (`NewRequiredXxxParam`/`NewOptionalXxxParam` pairs for
  Path/Query/Cookie/Header/SSEEvent/ResponseHeader/ResponseCookie), only these two "Required"
  variants had no direct test exercising their merge-field-registration behavior (sibling
  `NewOptionalQueryParam`/`NewOptionalCookieParam` were already used in multiple existing tests;
  `NewRequiredHeaderParam`/`NewRequiredSSEEventParam`/`NewRequiredResponseHeaderParam`/
  `NewRequiredResponseCookieParam` all had coverage too). Added 4 tests to `api/rest/builder_test.go`:
  `TestNewRequiredQueryParam_RegistersSpecAndMergeField`,
  `TestDecodeMerged_RequiredQueryParam_MissingReturnsError`,
  `TestNewRequiredCookieParam_RegistersSpecAndMergeField`,
  `TestDecodeMerged_RequiredCookieParam_MissingReturnsError` — mirroring the existing
  `TestNewPathParam_RegistersSpecAndMergeField`/`TestDecodeMerged_MergeFailure` pattern (spec
  `Required:true` + `MergeFields()` count, then `DecodeMerged` missing-var → `codex.ValidationErrors`).
- No production code changed — all other checked claims (core-layer purity, `ports`→`adapters`
  isolation, `render`/`forge` purity, the one documented `chi→websocket` adapter coupling, the
  Pattern-acceptance matrix per port type, `app`'s "stats only" dependency) were independently
  confirmed correct by the graph with zero deviations from documentation.

---

## Round 72 (`adapters/openai` error-name disambiguation + observer location fix)

Focused follow-up audit of the newly-shipped LLM Integration feature (Round 71) against the
full review-go-codex checklist — three findings, all implemented:

- **G1 — `adapters/openai` error types missing `Name`**: `RequestBuildError`, `RequestError`,
  `UnexpectedStatusError`, `ResponseBodyError`, `NoChoicesError`, `RetriesExhaustedError` only
  carried `Model` (or nothing) — an app with two `llm.Call`s sharing the same Model couldn't tell
  which one failed from the error text or structured log. Added `Name string` (populated from
  `handle.Name`) to all 6 types; updated `Error()`/`LogValue()`; mirrors `llm.ResponseDecodeError`
  (which already carried `Name`) and `nethttp.RequestError.Path`'s route-disambiguation role.
- **G2 — Wrong observer location string for incoming-response decode failure**:
  `adapters/openai/client.go`'s `complete[Req,Resp]` used `stats.ReportErrors(obs, "response", ...)`
  for a `DecodeResponse` failure — `"response"` is already used elsewhere in the codebase for an
  unrelated scenario (server-side SSE outgoing-event merge in `adapters/nethttp`/`chi`). Changed to
  `"body"`, matching `nethttp.Call`'s established client-side incoming-response-decode convention.
- **G3 — Fragile index-based `LogValue` tests**: 8 tests (2 in `api/llm/call_test.go`, 6 in
  `adapters/openai/client_test.go`) asserted `attrs[0].Key`/bare `len(v.Group())` instead of the
  reference pattern (`adapters/sql/validate_test.go`'s `TestValidate_LogValue`: assert
  `Kind()==slog.KindGroup`, check ALL expected keys via a map). Rewrote all 8 using a shared
  `logValueKeys(t, v)` test helper in each package.

Docs updated: `docs/features/llm-integration.md`'s Structured errors table (all 6 `Name` fields)
and Observer section (`"body"` location); `.github/instructions/go-codex.instructions.md`'s
`adapters/openai` row.

---

## Round 71 (LLM Integration — `api/llm`, `ports.LLMPattern`, `adapters/openai`, `render/openaitools`)

Full 5-phase implementation of the LLM-integration roadmap (declarative system-prompt +
input/output codec contract for go-codex CALLING an LLM — the other direction from the
already-shipped MCP server). All phases shipped in one autopilot session, sequentially:

- **`api/llm`** (foundation, zero deps on `ports`/`adapters/openai`): `Call[Req,Resp]`,
  `CallHandle[Req,Resp]`, `Builder`, `LLMSpec`/`CallSpec`, `SystemPromptFileError`/
  `ResponseDecodeError`. 17 tests.
- **`ports.LLMPattern`**: new `patternKindLLM` case in `buildDualCodecPatternHandles`
  (2 new trailing params `llmBuilder *llm.Builder, llmAllowed bool` — mirrors
  `CachePattern`'s `cacheAllowed bool` convention exactly); `IOPort`-only via
  `IOPort.PluginLLMPattern`/`PortOptions.LLMBuilder` — `SourcePort`/`SinkPort` have no
  method for it at all (type-system enforced, not just a runtime check); `ToolPort`/
  `LatestPort`/`DuplexPort` call sites pass `nil, false` (reject). New `RegisterLLM` in
  `ports/spec.go` for parity with `RegisterREST`/`RegisterEvent`/etc. Fixed a genuinely
  stale doc comment in `ports/pattern_errors.go` found while touching the file
  (`MissingPatternError` still referenced long-removed free functions
  `RESTHandle`/`EventHandle`/etc. from a much earlier Plugin-model refactor round that
  missed this one file). 7 tests.
- **`adapters/openai`**: stdlib-only (`net/http`, no SDK) Chat Completions
  `ports.IOAdapter[Req,Resp]` — strict `response_format:json_schema`, bounded
  retry-on-invalid-completion loop. Design nuance: `MaxRetries:0` (default) returns the
  raw `llm.ResponseDecodeError` unwrapped, NOT wrapped in `RetriesExhaustedError` — that
  type is reserved specifically for "retries were attempted and exhausted"
  (`MaxRetries > 0` case). 17 tests.
- **`render/openaitools`**: pure renderer, `FromMCPSpec`/`FromLLMSpec`/`Render` — converts
  existing `mcp.ToolSpec`/new `llm.CallSpec` into the OpenAI `tools` JSON array. 5 tests.
- Runnable example `examples/adapters-openai` (httptest fake, no real API key needed,
  demonstrates the retry loop firing once). Docs: `docs/features/llm-integration.md`,
  `docs/guides/llm-integration.md`. Roadmap doc `docs/roadmap/llm-integration.md` retired
  (deleted) once shipped — moved to Features/Guides nav, removed from Roadmap index/nav.
- **Gotcha for future reviews**: a bash-tool output-display artifact showed "******" in
  place of legitimate credential-adjacent text (e.g. "Bearer auth") when the doc was first
  read this round — verified via `od -c` byte-level inspection that the actual file bytes
  were always correct ("Bearer auth", "Authorization: Bearer <APIKey>", etc.). This was
  purely a display-layer quirk of the tool, not real file corruption — don't re-flag
  similar-looking text without a byte-level check first.

---

## Round 70 (map size constraints, whole-struct cross-field Refine, `StrictStruct`)

Broader follow-up to Round 69's array-length audit: "are there other missing features in
codex's slice/object/map(key) codecs?" — three findings, all shipped.

- **Map/StringMap size constraints (real gap, mirrors R69)**: `schema.Schema` gained
  `MinProperties *int`/`MaxProperties *int` (JSON Schema object-size keywords); `IsZero()`
  updated. `render/internal/schemarender/schemarender.go` renders `minProperties`/
  `maxProperties`. New `validate/map.go`: `MinProperties[K,V](n)`, `MaxProperties[K,V](n)`,
  `NonEmptyMap[K,V]()` — all `codex.Constraint[map[K]V]`, mirroring `validate/slice.go`'s
  naming one level up (entries instead of elements). Map KEY constraints (pattern/format/enum
  via `.Refine()` on the `keyCodec` argument) already fully worked — NOT a gap, confirmed via
  existing tests. Documented caveat: `EntrySlice[K,V,R]` returns `Codec[[]R]` (slice-shaped),
  so its entry-count constraint is the SLICE constructors (`MinItems[R]`/etc.), not the new
  map ones, despite its object-shaped schema — a harmless keyword/type mismatch if rendered.
  `examples/order`'s `tags` field now uses `MaxProperties[string,string](5)` with a new
  too-many-tags validation-error demo scene.
- **Whole-struct (cross-field) `Refine` (already worked, zero code changes — test/doc gap
  only, same pattern as R68's nested-struct round)**: `codex.Struct[T](...)` returns a plain
  `Codec[T]`, so `.Refine(...)` already validates invariants spanning multiple fields (e.g.
  "start before end"), running after all per-field checks succeed, symmetric on Encode. Zero
  prior test/doc coverage — now has 4 new tests in `codex/object_test.go`, a new "Whole-struct
  (cross-field) constraints" doc subsection, and a real cross-field constraint on
  `examples/order`'s `orderCodec` (`deliveryDate` must not be before `createdAt`) with a new
  validation-error demo scene.
- **`codex.StrictStruct[T]` (genuine new feature — reject unknown/typo'd keys)**: new sibling
  constructor to `Struct[T]` (identical signature) that sets `Schema.AdditionalProperties =
  false` and wraps Decode to also reject undeclared input keys. New `codex.ErrUnknownField`
  sentinel mirrors `ErrMissingField`'s existing style exactly (no per-field struct — the field
  name is carried by the wrapping `ValidationError.Field`). Unknown-key errors are merged with
  normal per-field errors (missing required, constraint failures) in one `ValidationErrors`
  pass, sorted for deterministic ordering. NOT viral/recursive across nesting — a plain
  `Struct`-declared nested field stays non-strict under a `StrictStruct` outer struct; opt in
  per level, matching how Required/Optional/Default are already independent per nesting level.
  `Encode` unchanged from `Struct`. 8 new tests in `codex/object_test.go` (including the
  merge-with-missing-required and non-viral-nesting edge cases), new "Rejecting unknown keys —
  StrictStruct" doc subsection.
- Docs: `docs/concepts/codec.md` (new "Maps — size constraints" section, new "Whole-struct
  (cross-field) constraints" and "Rejecting unknown keys — StrictStruct" subsections);
  `.github/instructions/go-codex.instructions.md`'s `codex`/`schema`/`validate` bullets
  updated with all new exported symbols.

---

## Round 69 (array/slice length constraints — `MinItems`/`MaxItems`/`NonEmptySlice`/`UniqueItems`)

Closed a real feature gap found while evaluating "can arrays be required/optional, and can I
set min/max length or non-empty?" (required/optional on `[]T` fields already worked via the
generic `Field[T,F]` mechanism — no gap there — but array LENGTH/uniqueness constraints had no
schema representation and no `validate` constructors at all):

- **`schema.Schema`**: added `MinItems *int`, `MaxItems *int`, `UniqueItems bool` (JSON Schema
  array-keyword names, mirroring `MinLength`/`MaxLength`'s int-pointer style); `IsZero()`
  updated to check all three.
- **`render/internal/schemarender/schemarender.go`**: renders `minItems`/`maxItems`/
  `uniqueItems` in the "Array items" section — the single translation point shared by
  `render/jsonschema` and `render/openapi`, so both pick this up with zero additional wiring.
- **New `validate/slice.go`**: `MinItems[T](n)`, `MaxItems[T](n)`, `NonEmptySlice[T]()`
  (function, not `var` — Go has no generic package-level vars), and
  `UniqueItems[T comparable]()` (narrower `comparable` bound, `map[T]struct{}` O(n) dedup) —
  all `codex.Constraint[[]T]`, composing via the existing `.Refine()` mechanism with zero
  `codex`/`SliceOf` changes needed.
- **`examples/order`**: `items` field now uses
  `codex.SliceOf(lineItemCodec).Refine(validate.NonEmptySlice[LineItem](), validate.MaxItems[LineItem](20))`
  — a new demo scenario shows the empty-items validation error; schema output shows
  `"MinItems": 1, "MaxItems": 20` on the `items` property.
- Tests: `schema/schema_test.go` (3 new `IsZero` cases),
  `render/internal/schemarender/schemarender_test.go` (2 new render tests + emptyFieldsOmitted
  list update), `validate/slice_test.go` (12 new tests covering Check/Message/Schema-annotation/
  Decode-composition for all four constructors).
- Docs: `docs/concepts/codec.md` new "Slices — array-level constraints" section;
  `.github/instructions/go-codex.instructions.md`'s `schema`/`validate` bullets updated (new
  exported symbols).

---

## Round 68 (stale Plugin-model doc examples — `PortOptions.Patterns`/free-function `XxxHandle`)

Focused audit of Round 67's changes plus a repo-wide sweep it surfaced: several doc comments
(godoc + README + a draft roadmap) still showed the PRE-Plugin-model-refactor API —
`ports.PortOptions{Patterns: []ports.Pattern{...}}` (the field was removed) and free-function
getters `ports.EventHandle[T](port)`/`ports.RESTHandle[T](port)`/`ports.ReqReplyHandle[T](port)`/
`ports.MCPHandle[T](port)` (none of these functions exist — the current API is
`port.PluginXxxPattern(pattern)` called directly on the port instance). None of the fixes are
compiled code (doc comments only), but they presented non-compiling snippets as canonical usage
on pkg.go.dev/README.

- **G1 [bug] — `ports/doc.go`'s top package-doc example**: rewrote the "Inside-out development"
  `SourcePort` example to declare a separate `Pattern` var and call `PluginEventPattern`, matching
  the pattern the adjacent "Two consumption styles" section already used correctly.
- **G2 [bug] — `README.md`**: same rewrite for its `SourcePort`/MQTT5 quickstart snippet.
- **G3 [bug] — `adapters/nethttp/binding.go`'s `LatestAdapter` godoc**: replaced
  `ports.RESTHandle[struct{}, db.Reading](domain.Latest)` with
  `domain.Latest.PluginRESTPattern(domain.LatestPattern)`.
- **G4 [bug] — `adapters/chi/binding.go`'s `LatestAdapter` godoc**: same fix as G3 (chi mirrors
  nethttp).
- **G5 [bug] — `adapters/zeromq/binding.go`'s `LatestAdapter` godoc**: replaced
  `ports.ReqReplyHandle[struct{}, OEE](domain.Latest)` with
  `domain.Latest.PluginReqReplyPattern(domain.LatestPattern)`.
- **G6 [bug] — `adapters/mcpgo/binding.go`'s `LatestAdapter` godoc**: replaced
  `ports.MCPHandle[struct{}, OEE](domain.Latest)` with
  `domain.Latest.PluginMCPPattern(domain.LatestPattern)`.
- **G7 [trivial] — `docs/roadmap/redis-pubsub.md`** (draft, unshipped): same
  `PortOptions{Patterns:...}` rewrite for consistency with the current API even in draft form.
- Also fixed during this round's verification pass (introduced by Round 67, not a new finding):
  a `just check` gosec G104 (unhandled `resp.Body.Close()` error inside a loop) in
  `examples/ports-plain-go/main.go` — extracted a `doConvertRequest` helper using the
  `defer resp.Body.Close()` idiom already used throughout `examples/adapters-nethttp`/
  `examples/adapters-chi`.

---

## Round 67 (ports plain-Go convenience — `ToolPort.SetFunc` + `IOPort.Call`)

Closed the last consumption-style gap in `ports`: `SourcePort`/`SinkPort`/`LatestPort`/`DuplexPort`
already had non-stream escape hatches (`Stream`+`Drain`, `Start`/`Push`/`Close`, `Latest()`,
`Inbound`/`Feed`), but `ToolPort`/`IOPort` only accepted `gstream`-composed functions
(`SetPipeline`, `Connect`), forcing plain idiomatic-Go users to write a trivial
`stream.Single`/`stream.Collect` wrapper by hand.

- **`ToolPort.SetFunc(func(ctx, In) (Out, error))`** (`ports/tool_port.go`) — plain-Go alternative
  to `SetPipeline`; internally wraps the fn to satisfy the same pipeline-function field. Mutually
  exclusive with `SetPipeline` — the later call wins. Same `PluginXxxPattern`/`Bind` calls serve
  either style.
- **`IOPort.Call(ctx, req) (Resp, error)`** (`ports/io_port.go`) — plain-Go alternative to
  `Connect`; drains the bound adapter's stream for one request via `stream.Single`/`stream.Collect`.
  Returns the new **`PortNoResponseError{Port}`** (`ports/port_errors.go`, `slog.LogValuer`) if the
  adapter emits zero items.
- **Documented the consumption-style-agnostic invariant** in `ports/doc.go` (new "Two consumption
  styles, one declaration mechanism" section), `docs/features/ports.md` and `docs/guides/ports.md`
  (new sections + inline `IOPort`/`ToolPort` callouts), and
  `.github/instructions/go-codex.instructions.md`'s `ports` bullet.
- Tests: `TestToolPort_SetFunc_*` (3), `TestIOPort_Call_*` (4),
  `TestPortNoResponseError_ErrorAndLogValue` (1) in `ports/port_test.go`.
- **New `docs/concepts/ports-and-adapters.md`** — dedicated concept page for the
  port/Pattern/plugin/adapter mechanism, framing plain-Go and forge-pipeline
  consumption as equal first-class styles; added to `zensical.toml` nav.
- **New `examples/ports-plain-go`** — two `ToolPort` endpoints
  (`/convert/{unit}` via `SetFunc`, `/convert-pipeline/{unit}` via
  `SetPipeline`) share one `convertPattern()` (path param `unit` +
  optional header `X-Trace-Id`, both via `rest.NewPathParam`/
  `rest.NewOptionalHeaderParam` merge-field constructors, backed by
  once-declared `unitCodec`/`traceIDCodec` reused across the struct field,
  the merge-field constructor, AND — for `unit` — the request body field)
  to prove the merge-field codec layer behaves identically for both
  consumption styles. Also shows `rest.WithPathConstraints` +
  `ports.PortOptions.RESTBuilder` enforcing the route TEMPLATE's shape
  (`exactUnitPathConstraint`: exact prefix, exactly one placeholder,
  nothing after) as a distinct concern from the placeholder's runtime
  VALUE codec — and demonstrates `SourcePort`+`stream.Drain` and
  `SinkPort` `Start`/`Push`/`Close`.

---

## Round 66 (client-side error decode parity — `nethttp.Call` + `RouteHandle.DecodeErrorFor`)

Closed the last remaining error-path-ergonomics gap: `rest.Route.ClientHandle()`/`.Register()`
already populated `errorPatternRules` on `RouteHandle` since Phase 1A, but `adapters/nethttp.Call`
never consulted them — non-2xx responses always returned the untyped `UnexpectedStatusError`, even
when the server had declared a typed `rest.ErrorPattern` for that exact status.

- **New `RouteHandle.DecodeErrorFor(status int, body []byte) (ErrorPatternResponse, bool, error)`**
  (`api/rest/builder.go`) — the client-side counterpart of `ErrorResponseFor`. Matching is
  status-only (the client has no Go error to match via `errors.As`, only the wire status code).
  Only rules whose action is the default `ErrorRespond` are eligible — rules tagged
  `.WithAction(ErrorHandle)`/`.WithAction(ErrorLog)` are skipped, since the server does not
  guarantee those wrote the typed body to the wire (falls through to `Options.ErrorHandler`
  instead). Reuses the existing `errorPatternRule` struct with a new `decode func([]byte)
  (ErrorPatternResponse, error)` field, populated alongside the existing `match` closure in
  `ErrorPatternOpt.applyRoute` — same declared codec drives both directions.
- **New `nethttp.ErrorPatternResponse{StatusCode int, Value any, Body []byte}`** (`adapters/nethttp/client.go`)
  — `Error()`/`LogValue()` implemented per the structured-errors guardrail. `Call`'s non-2xx branch
  now tries `handle.DecodeErrorFor(statusCode, respBody)` first; on match (`matched && decErr ==
  nil`) returns `ErrorPatternResponse` instead of `UnexpectedStatusError`; falls back to the
  unchanged `UnexpectedStatusError` on no-match or decode failure (schema drift) — zero behavior
  change for callers with no declared `ErrorPattern`.
- Tests: CDP1–CDP8 across `api/rest/builder_test.go` (`TestDecodeErrorFor_*` — matched, no-match,
  action-skip ×2, decode-failure, first-match precedence, no-patterns-declared) and
  `adapters/nethttp/client_test.go` (`TestCall_ErrorPatternResponse_*` integration tests via a real
  `httptest.Server`, `TestErrorPatternResponse_LogValue`).
- `examples/adapters-nethttp-client`: new section "1b" demonstrates the full round trip —
  `contract.CreateUser` declares `rest.ErrorPattern[EmailConflictError, EmailConflictError](409,
  EmailConflictCodec)`; a duplicate-email create call returns a decoded
  `nethttp.ErrorPatternResponse` whose `Value` is already a typed `contract.EmailConflictError`.
- Docs: `docs/features/rest-api.md` new "Client-side decode" subsection under "Error-path
  ergonomics"; `docs/features/http-client.md` "Error handling" section reordered to check
  `ErrorPatternResponse` before the `UnexpectedStatusError` fallback; `docs/guides/http-client.md`
  new "Handling the response: happy path vs error path" section (full `errors.As` chain walkthrough
  with a "what to do next" rule of thumb per error type).

---

## Round 65 (error-path ergonomics follow-up — `websocket.ErrorFrame` codec parity + `OnError`/declarative-handle consistency audit)

Two follow-up reviews after Round 64 shipped error-path ergonomics.

- **G1 — `websocket.ErrorFrame[E,Out]` had NO codec, reusing the socket's `Out` codec**: unlike
  `rest.ErrorPattern`/`events.ErrorChannel`/`reqreply.ErrorPattern`/`mcp.ErrorPattern`, which all
  declare an independent `codex.Codec[B]` for their error payload and validate it (Refine
  constraints run via `Encode`), `ErrorFrame` had no codec parameter and encoded via
  `a.handle.OutFormat.Marshal(frame)` — forcing the error struct to be the socket's happy-path
  `Out` type. Fixed: `ErrorFrame[E,B](codec codex.Codec[B], mapFn...) ErrorFrameRule` now declares
  its own codec; encoding happens inside the rule's `match` closure via `format.JSON(codec).Marshal`,
  producing a pre-encoded `ErrorFrameResponse{Body, Value, Action}`. Side effect: `ErrorFrameRule`
  is no longer generic (parameterized by `Out`), so `ErrorFrames` became a plain
  `[]ErrorFrameRule` on both socket adapters — the old type-erasure (`any` field) and runtime
  type-assertion (`ErrorFrameOptError`, now removed as unreachable) are both gone; a declaration
  mismatch is now a compile error. `examples/websocket-duplex` updated to declare a dedicated
  `ErrorPayload` struct+codec instead of reusing `Update`. New tests:
  `TestDuplex_ErrorFrame_IndependentCodec_ValidatesAndBroadcasts`,
  `TestErrorFrame_MapperProducesInvalidPayload_ReturnsValidationError`.
- **G2 — `BroadcastSocketAdapter` lacked the `ErrorFrames` declarative option `DuplexSocketAdapter`
  has**: a full audit of every `OnError func(error)` site in the repo (typed non-port
  Subscribe/Serve callbacks, untyped port-adapter sink/drain callbacks, SQL/Cache/File composition
  pattern, SSE helpers, `ServeLatest`) found this the ONE inconsistency — `BroadcastSocketAdapter`
  is structurally the closest sibling to `DuplexSocketAdapter` (both own a `ports.Socket` handle,
  both loop over `hub.Sessions()` broadcasting, both drain via the same `gstream.Drain(...,
  onErr, ...)` shape) yet had no way to declare a typed broadcast-on-error payload. Fixed:
  `BroadcastSocketAdapterOptions.ErrorFrames []ErrorFrameRule` added, wired identically to
  `DuplexSocketAdapter` via a `handleUpstreamError` closure that matches ONLY errors from the
  port's stream `Errors` channel (per-session write/encode failures remain `SocketError`-wrapped
  via `OnError` directly, unchanged). New tests:
  `TestBroadcast_ErrorFrame_Match_BroadcastsToAllSessions`,
  `TestBroadcast_ErrorFrame_NoMatch_FallsBackToOnError`,
  `TestBroadcast_ErrorFrame_HandleAction_NoBroadcast`.
- Confirmed NOT gaps (audited, no changes needed): SQL/Cache/File Drain adapters' composition
  pattern (by design); `nethttp`/`chi` SSE helpers lacking a declarative option (no bound handle
  exists at their call site — architectural, not a gap); `zeromq.ServeAdapter`/`mqtt5.ServeAdapter`
  and `zeromq.ServeLatest`/`LatestAdapter` — all delegate straight to `Serve`, which already
  consults `reqreply.ErrorPattern`, so no additional wiring was needed.

---

## Round 64 (error-path ergonomics — codec-first declarative error handling across all boundaries)

Not a review round in the usual sense — a full feature roadmap (Phases 1A–1D + Phase 2) implemented
across every `api/*` layer plus `adapters/websocket`. Recorded here so future review rounds don't
re-flag any of this as a gap. See checklist.md §13 and SKILL.md's "Error-path ergonomics" gotcha
entry for the full rule set. The design roadmap doc that originally tracked this
(`docs/roadmap/error-path-ergonomics.md`) has since been REMOVED — all phases (including the Round
65 follow-up fixes below) shipped; this history entry plus checklist.md §13 are now the durable
design-decision record.

- **G1 (Phase 1A) — REST had no codec-first error declaration**: added `rest.ErrorStatus[E](status)`
  (status-only) and `rest.ErrorPattern[E,B](status, codec, mapFn...)` (status + codec-backed body,
  direct/mapped modes, `errors.As` match, first-match precedence). Wired into `adapters/nethttp`/`chi`
  `Handler` (and `PipelineHandler`, which wraps `Handler`). Error responses support the same
  header/cookie merge-field parity as the happy path.
- **G2 (Phase 1B) — Events/WebSocket had no error-output declaration**: added
  `events.ErrorChannel[E,B](topic, codec, mapFn...)` with a full three-way action model
  (`events.ErrorAction`: `ErrorRespond`/`ErrorHandle`/`ErrorLog` via `.WithAction`). Wired into
  `adapters/mqtt5.PublishAdapter` as the reference implementation. Added
  `websocket.ErrorFrame[E,B](codec, mapFn...) ErrorFrameRule` on
  `DuplexSocketAdapterOptions.ErrorFrames []ErrorFrameRule` — matched errors broadcast a typed,
  independently codec-validated frame to every connected session (no dedicated error topic exists
  on a socket). *(Amended after a later parity review: the original shipped signature was
  `ErrorFrame[E,Out]`, reusing the socket's `Out` codec instead of declaring its own — fixed so
  `ErrorFrame` has the same "declare your own error struct+codec" guarantee as
  `rest.ErrorPattern`/`events.ErrorChannel`/`reqreply.ErrorPattern`/`mcp.ErrorPattern`; `ErrorFrameRule`
  is no longer generic, so `ErrorFrames` is a plain slice with no type erasure or runtime type
  assertion — `ErrorFrameOptError` was removed as unreachable.)*
- **G3 (Phase 1C) — SQL/Cache/File had no error-path story**: determined NO new adapter API was
  needed — every sink-side adapter's existing `OnError func(error)` already realizes the `handle`
  action (nil = `log`); a `respond`-equivalent is achieved by composing `OnError` with a declared
  `events.ErrorChannel.ErrorResponseFor` lookup inline. Locked with composition tests in
  `adapters/sql`/`adapters/redis`/`adapters/file`.
- **G4 (Phase 1D) — no ports parity proof, no cross-adapter docs matrix**: added
  `TestRESTPattern_ErrorStatus_ParityWithDirectRouteDeclaration`/
  `TestEventPattern_ErrorChannel_ParityWithDirectChannelDeclaration` (`ports/port_test.go`) proving
  `Pattern`-declared error rules behave identically to direct declarations. Added the capability
  matrix now in checklist.md §13.
- **G5 (Phase 2.1) — `reqreply.ErrorReplyMeta` was spec-only, no runtime dispatch**: added
  `reqreply.ErrorPattern[E,B](codec, mapFn...)` which drives BOTH the AsyncAPI reply-error
  channel/operation AND runtime dispatch in one declaration (`.WithCode`/`.WithDescription`/
  `.WithSchemaName`/`.WithChannelAddress`/`.WithOperationID` customize the generated spec entry;
  default `Code` derived from `%T` type name). Wired into `mqtt5.Serve` and `zeromq.Serve`/
  `ServeRouter` (ROUTER variant preserves identity framing) on handler/encode failure only.
  `ErrorReplyMeta` remains available unchanged for spec-only use.
- **G6 (Phase 2.2) — MCP tool errors were always plain text**: added `mcp.ErrorPattern[E,B](codec,
  mapFn...)` as a `ToolOpt` on `NewTool` (Tool handler errors only — Resources/Prompts are
  protocol-level, out of scope). Wired into `adapters/mcpgo.ToolHandler`'s handler-error branch:
  matched → `mcp.NewToolResultStructured(...)` with `IsError: true` set manually; unmatched → falls
  back to `mcp.NewToolResultError(err.Error())` unchanged. Added `docs/features/mcp.md`'s
  error-path-ergonomics section (new — none existed before).
- **G7 (Phase 2.3) — only `mqtt5.PublishAdapter` consulted `ErrorResponseFor`**: applied the exact
  same wiring to `adapters/mqtt.PublishAdapter` and `adapters/zeromq.PublishAdapter` — no new
  options struct fields needed (the declaration lives entirely on the `events.ChannelHandle` every
  publish adapter already receives).
- **G8 (Phase 2.4) — no runnable examples for any error-path feature except REST**: extended
  `examples/adapters-mqtt5` (events.ErrorChannel via `ports.SinkPort`+`PublishAdapter`),
  `examples/websocket-duplex` (websocket.ErrorFrame broadcast, `NegativeValueError` business rule
  re-emitted through `stream.MapErr` instead of silenced), and `examples/redis-cache` (SQL/Cache/File
  `OnError`+`ErrorChannel` composition as a runnable program). All three build and smoke-test cleanly.
- **G9 (Phase 2.5) — REST had no `handle`/`log` action selector, only `respond`**: `rest.ErrorPattern`
  now returns a chainable `ErrorPatternOpt[E,B]` (still satisfies `RouteOpt`, zero breaking changes)
  with `.WithAction(rest.ErrorAction)`. `rest.ErrorAction` is a NEW standalone type (NOT shared with
  `events.ErrorAction` — see gotcha above). `ErrorHandle`/`ErrorLog` both skip the auto-write and
  fall through to `Options.ErrorHandler` — behaviorally identical for REST (only one hook exists),
  kept distinct for cross-boundary vocabulary parity. This closes three-way action-model parity
  across REST/Events/WebSocket.
- Deferred (not implemented, no concrete use case): client-side error decode parity for
  `nethttp.Call` (`UnexpectedStatusError.Body` already works as an escape hatch).

---

## Round 63 (extending "one struct, one call" to `ports.File`/`ports.Cache` — full adapter/port audit)

- **G1 — `ports.File`/`adapters/file` had the declare-once constructor (`NewFilePathParam`) and `MergeFields()` accessor but ZERO single-call convenience**: added `File.ReadMerged` (decode-merge, mirrors `events.ChannelHandle.DecodeMerged`) and `ports.WriteHandle` (encode-side single-call convenience, mirrors `mqtt5.PublishHandle`). Wired `adapters/file`'s `ReadEachAdapter`/`ReadAdapter` to read via `ReadMerged` automatically (merges vars already known from `varsFor(In)` into the decoded file content); `DrainWriteFileAdapter`'s `varsFor` may now be `nil` when the file declares merge fields, deriving vars per-item automatically instead of requiring a mandatory hand-written closure.
- **G2 — `ports.Cache`/`adapters/redis` had the same gap, with a doc comment explicitly (and incorrectly) claiming no bundling convenience was needed**: added `redis.GetMerged` (decode-merge) and `redis.SetHandle` (encode-side convenience). Wired `GetAdapter` to look up via `GetMerged` automatically; `SetAdapter`/`DrainSetAdapter`'s `keyFn` may now be `nil` when the cache declares merge fields, deriving key vars per-item automatically via a new shared `keyVarsFor` helper.
- **G2 (bonus, found while implementing)** — a real, pre-existing bug: BOTH `CachePattern` build paths in `ports/handle.go` (`buildEventPatternHandles` for `SinkPort`/`LatestPort`, `buildDualCodecPatternHandles` for `IOPort`) reconstructed `Cache[T]`/`Cache[Resp]` field-by-field and silently dropped `NewCacheKeyParam`-registered merge fields (only `cb.params`, the plain validate-only params, were copied) — every `CachePattern`-built cache had an EMPTY `MergeFields()` regardless of `NewCacheKeyParam` usage. Fixed by delegating to `NewCache` (mirrors `FilePattern`'s existing delegation to `NewFile`, which never had this bug). New regression tests: `TestCachePattern_NewCacheKeyParam_WiredThroughIOPort`/`_WiredThroughSinkPort`.
- **G3 (deferred, tracked not fixed)** — `adapters/websocket`'s upgrade path uses validate-only `rest.PathParam` for connection-level vars; same open "per-connection vs. per-message merge" question already deferred for SSE. No use case, not actioned.
- **G4 — checklist §12 table had no `ports.File`/`ports.Cache` rows**: added both, reflecting the G1/G2 shipped status.
- Full design was in `docs/roadmap/file-cache-merge-field-gaps.md` (deleted after G1/G2/G4 shipped; its G3 connection-merge item later shipped via the SSE/WebSocket merge round).

---

## Round 62 (checklist.md §11 rewrite — port-adapter architecture sync, docs-only, zero behavior change)

- **G1 — checklist §11 "Stream Bridge Consistency" described deleted APIs**: the section still instructed reviewers to check `mqtt.SubscribeStream`, `mqtt5.SubscribeStream`, `zeromq.SubscribeStream`, `sql.QueryStream`, `nethttp.HandlerIngest`, and `DrainPublish`-as-bridge — all removed in Round 45 and replaced by port adapters (`SubscribeAdapter`, `QueryAdapter`, `IngestAdapter`, etc.), already covered correctly by SKILL.md's own "Port Adapter Guardrail" (B1–B3). Rewrote the section (renamed "Port Adapter Consistency") with live function names for every rule (B1 validation-pipeline delegation, B2 error routing to `errs`/`Stream.Errors`, B3 static-`Vars` godoc, B4 `AsPipelineFunc` shape, adapter error-type completeness, `IngestAdapter` param-value gap, HTTP codec-coverage docs) and added the missing `ReadError{Err}` to the `adapters/file` error-type row (already correct in SKILL.md's parallel table). Cross-references SKILL.md explicitly as the authoritative source if the two ever diverge again.

---

## Round 61 (custom-format consistency audit across all 8 `ports.Pattern` types — docs/comments only, zero behavior change)

- **G1 — `Pattern` interface accessor list stale**: `ports/pattern.go`'s top-level doc was missing `[CacheHandle]`/`[SocketHandle]` from its accessor list (already correct in `ports/doc.go`, missed when those two were added). Fixed; also added a new "Custom wire formats" section explaining the two mechanisms (`CustomFormat` field vs. inline `RouteOpt`/`ChannelOpt`) and which patterns use which.
- **G2/G3 — stale "infallible" claims for `FilePattern` building**: `buildEventPatternHandles` and `buildDualCodecPatternHandles` doc comments in `ports/handle.go` both still called `FilePattern`'s `format.File` construction "infallible" — false since `CustomFormat` (R59) can fail a type assertion. Both updated to "infallible on the enum-only path; a CustomFormat type mismatch returns PatternRegisterError."
- **G4 — `PatternRegisterError` doc incomplete**: the `Kind` field godoc enumerated only `"rest"/"event"/"reqreply"/"mcp"`, missing `"file"/"cache"/"socket"` (added by R59/R60); the type-level "wraps rest/events/reqreply/mcp" sentence also didn't mention `CustomFormat` mismatches or port-type rejection errors. Both updated. Instructions file's parallel sentence synced too.
- **G5 — `MCPPattern`/`SQLPattern` silent on format**: of the 8 patterns, only these two said nothing about wire-format customization, with no explanation why. Added one clarifying sentence each (MCP: protocol-structured, no wire-format layer; SQL: driver-native rows, never encoded through `format.Format[T]`).
- **G6 — no unifying format-mechanism note**: added a paragraph to the `Pattern` interface doc (see G1) cross-referencing all three format stories (CustomFormat / inline RouteOpt-ChannelOpt / no format at all) in one place.
- **G7 — `SocketPattern.Opts` misuse trap undocumented**: `rest.RequestFormats`/`rest.Formats` silently fail their type assertion if placed in `SocketPattern.Opts` (the upgrade route's Req/Resp are always `struct{}` internally) — added a warning pointing to `Format`/`CustomFormat` instead.
- **G8 — review-skill checklist doc drift**: `references/checklist.md` §5 "Format API Parity" predated R59 (`CustomFormat`) and R60 (format `RouteOpt`/`ChannelOpt` constructors) — added two rows.

---

## Round 60 (`api/rest`/`api/events`/`api/reqreply` — inline format `RouteOpt`/`ChannelOpt` constructors: `RequestFormats`, `Formats`, `SubscribeFormats`, `PublishFormats`)

- **This is API SYMMETRY with the `CustomFormat` escape hatch (R59), NOT a duplicate mechanism** — `RESTPattern`/`EventPattern`/`ReqReplyPattern` never needed a `CustomFormat` field: their built handles already accepted any `format.Format[T]` (with real multi-format negotiation) via `WithRequestFormats`/`WithFormats`/`WithSubscribeFormats`/`WithPublishFormats`; the gap was ergonomics — declaring the format required a POST-Register handle mutation, not a `Pattern.Opts` entry. Do not propose a `CustomFormat` field on these three patterns.
- **Zero `ports` package changes** — `RequestFormats`/`Formats`/etc. just implement the EXISTING `rest.RouteOpt`/`events.ChannelOpt`/`reqreply.RouteOpt` interfaces; `RESTPattern.Opts`/`EventPattern.Opts`/`ReqReplyPattern.Opts` already accept them. Confirmed via `ports/format_opt_test.go` (3 zero-ports-change regression tests). Do not suggest touching `ports/pattern.go`/`handle.go` for this feature.
- **Type-erased `any` storage on `routeBuilder`/`channelBuilder`, resolved generically in `Register`** — same pattern as `CustomFormat`'s `resolveFormat`; a caller declaring formats for the wrong type only fails at `Register` time (`FormatOptError`), not at the `RequestFormats[Req](...)` call site (Go generics can't link a `RouteOpt` value back to the route's type params at compile time). This is intentional, not a missed compile-time check.
- **New `FormatOptError{Direction, Err}` type ADDED WITH `LogValue`** to `api/rest`, `api/events`, `api/reqreply` — even though sibling pre-existing error types in the SAME files (`PathParamError`, `TopicParamError`, etc.) lack `LogValue` in `api/rest`/`api/events`. This is a deliberate improvement (mandatory 5-requirements rule), not an inconsistency to "fix" by removing LogValue — do not flag, and do not retrofit LogValue onto the older sibling errors as part of unrelated work (separate concern).
- **`events.Formats`/`SubscribeFormats`/`PublishFormats` naming mirrors the handle setter names exactly** (`WithFormats`→`Formats`, `WithSubscribeFormats`→`SubscribeFormats`, etc.) — same convention in `api/reqreply` (`WithFormats`→`Formats`, `WithRequestFormats`→`RequestFormats`). Do not suggest renaming for "clarity" — the 1:1 mapping to the handle method IS the clarity.

## Round 59 (`ports.Pattern` `CustomFormat` escape hatch — `FilePattern`/`CachePattern`/`SocketPattern`)

- **No dedicated `FileFormatGob` enum value BY DESIGN** — J/Y/T share one construction shape (`map[string]any` intermediate via codec Encode/Decode); Gob is `NewTyped`-style (direct typed value, no intermediate) and architecturally does not belong in that closed enum. `CustomFormat` is the one path for Gob and every future binary/custom format (protobuf, msgpack, CBOR) — do not propose growing `FileFormatKind` for new binary formats.
- **`CustomFormat` stores a pre-built `format.Format[T]` value, NOT a factory closure.** The caller already has the concrete codec at `Pattern`-declaration time (same codec passed to the port constructor moments earlier) — nothing to defer. Do not suggest `func(codex.Codec[T]) format.Format[T]`.
- **`fileFormatFor` intentionally became fallible** (new `resolveFormat` wrapper returns `(format.Format[T], error)`) — a `CustomFormat` type mismatch returns `PatternRegisterError`; the enum-only path remains infallible. This is correct, not a regression of the "construction is infallible" claim (that claim now only covers the enum-only path).
- **`SocketPattern.CustomFormat`'s unused `struct{}` side is EXEMPT from the type assertion** — a one-directional port (`SourcePort`→`Socket[T,struct{}]`, `SinkPort`→`Socket[struct{},T]`) builds BOTH `InFormat` and `OutFormat` internally; asserting a real-type `CustomFormat` against the unused `struct{}` side would wrongly fail. `resolveFormat` checks `any(*new(T)).(struct{})` and skips the assertion when T is `struct{}`, silently defaulting to JSON (never used functionally). Do not flag this as inconsistent — it's the fix for a real bug caught during test-writing.
- **Precedence: `CustomFormat` wins when non-nil, `Format` is silently ignored** — no error when both are set (documented, since `Format`'s zero value is `FileFormatJSON` and would almost always be "set" incidentally alongside `CustomFormat`).
- Asymmetric `SocketPattern` formats (different `CustomFormat` for In vs Out on a `DuplexPort`) remain DEFERRED (no `CustomInFormat`/`CustomOutFormat` split) — no use case yet, recorded in the roadmap doc.

## Round 58 (websocket Phase 2 — client-side dial adapters, chi socket variants, `ports.RegisterSocket` AsyncAPI)

- **Dial adapters auto-reconnect with gap SocketErrors BY DESIGN** — exponential backoff 250ms→MaxBackoff (default 30s), reset after a connection that carried traffic; EVERY failed dial (`Op:"dial"`) and EVERY drop (`Op:"read"`) is emitted to the port's Errors channel. Do not suggest silent reconnect or fail-fast.
- **Session GENERATIONS (`c1`,`c2`,…) mark reconnects** on dial adapters — a generation change in inbound `Framed` values is the visible gap marker. Not a bug that the "session" changes.
- **Outbound frames while the dialed connection is down are DROPPED with `ErrFrameDropped` — INCLUDING during initial connection establishment.** Consumers that need the first frames must pump or buffer upstream (tests/examples pump on a ticker until the echo arrives). Consistent with the server slow-client policy; do not propose queueing.
- **chi socket adapters DELEGATE to adapters/websocket** via a constructor-time `swapHandler` satisfying `websocket.Mux` (`Handle` = atomic install) — zero duplicated frame/upgrade logic; tiny naming shims override `AdapterName` to `"chi.*"`. Do not flag the delegation as indirection; do not suggest chi-side reimplementation.
- **`events.Builder.AddChannelItem` intentionally skips the builder topic codec** — the topic may be an HTTP upgrade path (`"/live/{room}"`), not an MQTT topic. SchemaName refs still hit dangling-$ref validation.
- **`RegisterSocket` direction mapping follows the renderer's struct comments**: Subscribe = frames the application RECEIVES (In), Publish = frames it SENDS (Out); one-directional ports skip the `struct{}` side by a type assertion on the zero value.
- **`DialSinkAdapter` gaps surface only via `RecordPublish(success=false)`** — SinkAdapter has no error channel; documented, not an oversight.
- ConnectionObserver and dynamic subprotocol negotiation remain DEFERRED (websocket-deferred roadmap) — do not propose implementing them without a use case.

## Round 57 (WebSocket adapter — `adapters/websocket`, sixth port type `ports.DuplexPort`, `ports.SocketPattern`)

- **`DuplexPort[In,Out]` binds exactly ONE adapter BY DESIGN** (IOPort precedent) — session identity across multiple transports is unresolved; do not propose multi-adapter fan-in/out.
- **`DuplexAdapter.Activate` takes the outbound stream as a direct `src` parameter** (not an `outbound func()` closure as an early sketch had) — the port owns all four channels; `Feed` closes the outbound pair to signal completion.
- **Slow-client policy: DROP the frame for that session only** (`SocketError` wrapping `ErrFrameDropped`; per-session queue default 16, BroadcastHub precedent). Not silent data loss — reported per drop. Do not suggest blocking or disconnecting instead.
- **Frame decode failure keeps the connection OPEN** — one bad frame ≠ disconnect; error goes to the port's Errors channel with "payload" reports.
- **`SocketPattern` rejected on IOPort/LatestPort/ToolPort** (`PatternRegisterError{Kind:"socket"}`) — per-message req/reply over a socket is an RPC discipline (ReqReplyPattern territory). `DuplexPort` accepts ONLY SocketPattern — any other kind fails construction.
- **Upgrade validation extracts ALL `{var}` template vars** (regex on the path template) for `Hub.SessionInfo`, then validates only DECLARED PathParam codecs via the handle's `rest.RouteHandle[struct{},struct{}]` — `PathParamNames()` alone would miss undeclared template vars (real bug found in test iteration).
- **Keepalive is shim-owned** (`NewUpgrader`: ping 30s, pong wait 2×, read limit 1 MiB; gorilla is imported ONLY in socket.go); `Hub` is an explicit main-constructed collaborator so `SessionInfo` is reachable without widening the adapter interfaces.
- **NO `ConnectionObserver` extension** — transport hooks suffice (`RecordRequest` per upgrade, `RecordSubscribe`/`RecordPublish` per frame); connect/disconnect metrics wait for a use case (recorded in websocket-deferred roadmap).
- **NOT an MQTT broker** — MQTT-over-WS is the MQTT client's transport option (ws:// broker URL to paho); permanently out of scope.
- The "universal StreamPattern" idea was evaluated and REJECTED — WebSocket (path-addressed, at-most-once) and Redis Streams (key-addressed, at-least-once, XACK) need separate declarations.

## Round 56 (Redis cache adapter — `adapters/redis`, `ports.CachePattern`, `stats.CacheObserver`)

- **`Commands` narrow interface BY DESIGN** — constructors accept the three-method `Commands`, never `*redis.Client`; `NewCommands` (commands.go) is the ONLY go-redis import; unit tests + example use hand-written fakes. Do not flag the shim as unnecessary indirection.
- **`GetAdapter` miss SKIPS the item by default** — the IOAdapter 0..N contract; `MissIsError` opts into `CacheError` wrapping `ErrCacheMiss`. Not a silent-data-loss bug.
- **`SetAdapter` passes the item through even when the cache write FAILS** — a cache failure must never drop pipeline data; the error still goes to Stream.Errors. Intentional.
- **There is deliberately NO `redis.LatestAdapter`** — `ports.LatestAdapter.Serve(ctx, latest)` is read-only (serves, cannot inject). Durable LatestPort = `SetAdapter` tee on the feeding stream + `Seed` (warm-restart read, `(zero,false,nil)` on miss) merged as first item. Do not propose a LatestAdapter.
- **`CachePattern` rejected on `SourcePort`/`ToolPort`** with `PatternRegisterError{Kind:"cache"}` at construction — first pattern with explicit port-type rejection (others are silently ignored where not applicable); intentional strictness for a pattern with no meaningful fallback.
- **`ports.Cache[T].BuildKey` treats an unbalanced `{` as literal** — not an error; only a missing var for a well-formed placeholder errors (`CacheKeyError`, no Unwrap — no inner error).
- **`CacheObserver` is a new stats extension** (hit/miss/write is a genuinely new lifecycle event) — type-asserted like SQLObserver; Noop/Logging/fanout implement it.
- Cache key vars are plain strings (no per-var codecs) in Phase 1 — mirror of `varsFor` in file adapters, revisit only with a use case. Redis pub/sub deferred (fire-and-forget, closer to ZeroMQ than MQTT).

## Round 55 (stream routing operators — `stream/route.go`: GroupBy, Switch, SwitchKey, OfType, SwitchType2/3, SplitEither)

- **`Switch` sends non-matches AND src errors ONLY to the rest stream BY DESIGN** — single error ownership; case streams carry values only. Do not flag missing per-case error channels.
- **`Switch`/`SwitchKey` PANIC on malformed cases (empty/duplicate `Name`, nil `When`, duplicate keys) BY DESIGN** — programming errors caught at wiring time; keeps the two-value return signature. Not a missing-error-return bug.
- **`GroupBy` blocks until src closes (like `SinkPort.Feed`); `onKey` runs on the dispatch goroutine** — "start, don't run" contract. Keys are unbounded by design (documented); errors fan out NON-BLOCKING to all active keys (`select`/`default` drop is intentional).
- **`OfType` drops non-matching types silently and takes NO Options struct** — observer resolved from ctx (`stats.ObserverFromContext`), location `"oftype"`. Intentional minimal signature.
- **`SwitchType3` is direct dispatch, NOT composed from `SwitchType2`+`OfType`** — composition would put two concurrent readers on one channel and steal items. Do not suggest the composition "simplification".
- **`SplitEither` has no rest stream** — `codex.Either[A,B]` is a closed sum; errors fan out to BOTH branches non-blocking.
- **Routing adds NO new error types** — routing introduces no failure modes; `Stream.Errors` passthrough only. Observer locations: `"groupby"`, case `Name`, `"rest"`, `"oftype"`, `"switchtype.N"`/`"switchtype.rest"`, `"either.left"`/`"either.right"`.
- Topology gained `StepKindSwitch`/`StepKindGroupBy` + `Topology.WithSwitch`/`WithGroupBy`.

## Round 54 (post-ship review of the gaps phases — doc.go sync, ports Examples, chi.LatestAdapter, two latent race fixes)

- **`ports/doc.go` rewritten** (was severely stale: "Three port types", error-less constructors, no Pattern) — now Pattern-first with `codex.Must`, five port types, Push lifecycle, accessors. `stream/doc.go` transform list gained `Map`.
- **`ports` package gained Example functions** (`ExampleNewSourcePort` Pattern-first + ChanSourceAdapter, `ExampleSinkPort_Push`, `ExampleNewLatestPort`) — deterministic, test adapters only.
- **`chi.LatestAdapter` added** (G1 had skipped chi despite the "same API surface as nethttp" contract; chi already had `HandlerLatest`/`RegisterLatest`).
- **chi port adapters use a `swapHandler` constructor-time registration — do not flag the indirection.** chi's Mux is NOT safe for route registration concurrent with serving (no internal lock, unlike `net/http.ServeMux`), and port `Bind` runs adapters in background goroutines. All three chi port adapters (`IngestAdapter`/`SSEAdapter`/`LatestAdapter`) register a `swapHandler` at CONSTRUCTOR time (caller's goroutine, before the server starts) and atomically install the real handler from `Activate`/`Serve`; requests before installation get 503. This fixed a pre-existing data race exposed by the first `-race` run against chi's binding tests.
- **`IngestAdapter.Activate` (chi AND nethttp) now waits for its forwarding goroutine** via a done channel before returning — previously a send to `dst` could race the port's channel close after ctx cancellation (latent crash, caught by `-race`).
- **sensor-service README** documents the `app` lifecycle wiring (main.go row + run-section note).

## Round 53 (ports post-Phase-6 gaps — Phase D: `app` lifecycle package)

- **NEW top-level package `app`** (NOT `forge.App` — the original backlog name was inertia; forge is Layer-2 computation governance, App is process lifecycle; one top-level package per concern is the repo convention; imports only `stats` + stdlib). `app.New(app.Options{Observer, Logger, ShutdownTimeout /*default 10s*/}) *app.App`.
- **`Context()`** — cancelable root with Observer pre-injected via `stats.WithObserver`: the SINGLE observer-injection point for a service (replaces the hand-written `ctx = stats.WithObserver(ctx, obs)` line in main).
- **`Go(name, fn)` is fail-fast, errgroup-style BY DESIGN** — first non-nil return cancels the app; all goroutine + hook errors still collected via `errors.Join`. Do not flag fail-fast as fragile: adapters that should survive errors handle them internally (per-adapter `OnError`) and return nil.
- **`OnShutdown(name, fn)` runs LIFO** (defer semantics); a failing hook never stops later hooks; each hook ctx bounded by ShutdownTimeout (`HookError` wraps `context.DeadlineExceeded`). **`Run(parent)`** installs signal handlers inside Run ONLY (constructing App installs none — test-friendly); **`Shutdown()`** is the direct/idempotent/memoized teardown for demos/tests — both share one path.
- **Zero coupling to `ports`/`forge`** — teardown registration is explicit `OnShutdown`, never inferred from ctx identity (ctx-sniffing rejected in the design pass). Errors: `GoroutineError{Name,Err}`/`HookError{Name,Err}` (Error/Unwrap/LogValue). Observer events `"app.go"`/`"app.shutdown"` via plain `RecordRequest` (no TraceObserver spans — lifecycle is not request-scoped).
- **`examples/sensor-service`** adopted: `app.New` owns the root ctx (MQTT pipeline runs on a cancelable CHILD ctx — the demo cancels it mid-run while HTTP ports keep serving); exports-port `Close` + httptest-server close are `OnShutdown` hooks; demo ends with `a.Shutdown()` (LIFO: http-server → exports-port).

## Round 52 (ports post-Phase-6 gaps — Phase C: role-aware RESTPattern for HTTP ingest + SSE)

- **`RESTPattern` on single-codec ports is role-aware**: `buildEventPatternHandles` gained an unexported `portRole` param (`roleSource`/`roleSink`, passed by `NewSourcePort`/`NewSinkPort`) and a `RESTPattern` case. `SourcePort[T]` → ingest `rest.NewRoute[T, struct{}](Method, Path, codec, codex.Struct[struct{}](), Opts…)` — handle via the EXISTING `RESTHandle[T, struct{}]` accessor (type params express the shape; no new accessor). `SinkPort[T]` → SSE `rest.NewSSERoute[struct{}, T](Path, struct{} codec, codec, Opts…)` — always GET; non-GET `Method` fails construction with `PatternRegisterError`; NEW accessor `SSEHandle[Event](port) (*rest.SSERouteHandle[struct{}, Event], bool)` (distinct handle type — `RESTHandle`'s assertion can never match it) + NEW replay `RegisterSSE[Event](b, port) error`.
- **Zero adapter-side changes** — `nethttp/chi.IngestAdapter` and `SSEAdapter` already accept exactly these handle shapes; pattern-derived handles slot straight in. Ingest response semantics unchanged (200 empty body / 503 `PipelineFullError`).
- **`nethttp.DrainCallAdapter` stays handle-first BY DESIGN** — needs an independent response codec the single-codec port can't supply; do not flag as missing pattern support.
- **Test gotcha recorded**: `SSEHandler` commits response headers on the FIRST event, so an SSE e2e test must pump events in the background BEFORE `http.Client.Do` returns — a client that connects first and feeds later deadlocks the test.

## Round 51 (ports post-Phase-6 gaps — Phases A+B: LatestPort, SinkPort.Push, topology port step, stream.Map)

- **`ports.LatestPort[T]` — the FIFTH port type** (reactive cache): `NewLatestPort(name, codec, PortOptions) (*LatestPort[T], error)`; `Feed(ctx, src)` drains a stream into a port-owned `atomic.Pointer[T]` cell (src errors dropped; the cache OUTLIVES the stream — adapters keep serving after src terminates); `Bind(ctx, LatestAdapter[T]) error` fan-out (many transports, one cell), runs `Serve` in a supervised goroutine via `bindWithObserver` (`"port.bind"`); `Latest() (T, bool)` programmatic read. `LatestAdapter[T]` contract: `Serve(ctx, latest func() (T, bool)) error` — MAY return immediately after registration (nethttp, mcpgo) or block until ctx done (zeromq REP loop); both shapes correct by contract. Patterns build with request codec `codex.Struct[struct{}]()`: `RESTPattern`/`ReqReplyPattern`/`MCPPattern`. No new error types (reuses `PatternRegisterError`; empty-cache stays per-adapter `NoLatestValueError`/error result).
- **`mcpgo.ToolLatestAdapter` REMOVED** (breaking change, user-approved) — `mcpgo.LatestAdapter[Out](server, handle *apimcp.ToolHandle[struct{},Out], opts)` replaces it with no ignored pipeline argument. `ToolLatestHandler`/`RegisterToolLatest` (non-port functions) remain, as do `nethttp.HandlerLatest`/`RegisterLatest` and `zeromq.ServeLatest`. New serving constructors: `nethttp.LatestAdapter[Resp](mux, handle *rest.RouteHandle[struct{},Resp], Options)`, `zeromq.LatestAdapter[Resp](sock, handle *reqreply.RouteHandle[struct{},Resp], ServeLatestOptions)`.
- **`SinkPort` request-fed lifecycle**: `Start(ctx)` (port-owned channel + drain goroutine through the SAME broadcast path as `Feed`), `Push(ctx, v) error` (blocking with backpressure; `ctx.Err()` when cancelled), `Close() error` (waits for in-flight Push + adapter drain; idempotent). Mutually exclusive with `Feed` — `PortNotStartedError{Port, Op}` (Error+LogValue, NO Unwrap — no inner error) on violations. Internally a `feedMode` enum guarded by an RWMutex; Push holds RLock during the send so Close (write lock) waits for in-flight pushes — no send-on-closed-channel race.
- **`stream.StepKindPort` + `Topology.WithPort(name, description)`** — honest topology step for IO-port hops (sensor-service's persist step was previously mislabeled `[tap]`).
- **`stream.Map[In,Out](ctx, src, fn func(In)(Out,error), MapOptions{Name,Observer,Buffer})`** — typed 1→1 transform WITH error path; errors wrapped in `StreamMapError{Name, Err}` (Unwrap + LogValue) to Stream.Errors; `RecordStreamItem` per item. Positioned as the non-governed alternative to `forge.Function` + `Apply` — do not flag the overlap; Apply stays for governed steps.
- **Fixed a pre-existing data race in zeromq tests**: `mockSocket` was unsynchronized while background Serve goroutines write `sentFrames` and tests poll it — added mutex + `sentSnapshot()`; three poll sites converted. The race predated this round (exposed once `-race` ran against the new background-Serve tests).
- **`examples/sensor-service`**: `ioports.Latest` is now a LatestPort (RESTPattern; replaced `LatestRoute`/`LatestHandle` + `RegisterLatest`); export flow uses `Start`/`Push`/`Close` (deleted the hand-rolled exportCh + goroutine + done-channel); `pipeline.Topology` uses `WithPort` for the persist hop.

## Round 50 (inside-out pipeline wiring — Phase 6: `FilePattern` + `SQLPattern`)

- **`ports.FilePattern{Path, Format FileFormatKind, Opts []format.FileOpt}`** — declares a typed file on the port. `FileFormatKind` (`FileFormatJSON` default/`FileFormatYAML`/`FileFormatTOML`) is applied to the port's own codec inside the build fns — a generic `format.Format[T]` cannot sit in the non-generic `Pattern` struct; custom formats stay handle-first. On `SinkPort[T]` the handle is `format.File[T]` (payload codec); on `IOPort[Req,Resp]` it is `format.File[Resp]` (**response** codec — the file content IS the port's response). Accessor: `ports.FileHandle[T](port) (format.File[T], bool)`. Construction is infallible (`format.NewFile` returns a value) — no constructor signature changes. No `RegisterFile` — files have no spec document concept. `ports` now imports `format` (no cycle).
- **`ports.SQLPattern{Table, Op string}` is metadata-only BY DESIGN — do not flag the asymmetry with the handle-building patterns.** SQL query text/placeholders are driver-specific typed closures owned by the adapter constructor; there is no template to parse, no handle, no spec. Propagation: `WithSQLMeta(ctx, m)`/`SQLMetaFromContext(ctx) (SQLPattern, bool)` mirror `WithParams`; the unexported `adapterContext` helper (ports/sql_meta.go) wraps ctx in `SourcePort.Bind`/`SinkPort.Bind`/`ToolPort.Bind`/`IOPort.Connect` (IOPort's adapter sees ctx at `Transform`, not `Bind`). Accessor: `ports.SQLMeta(port)`.
- **All three sql adapters default `Table`/`Op` from context** via `resolveTableOp(ctx, table, op)` — explicit option values always win; resolved once per `Activate`/`Transform`, not per item.
- **`file.ReadAdapter[In,Resp]`** — new 2-type per-item read pairing with `FilePattern` (file content = response). Thin wrapper delegating to `fileReadEachAdapter[In,Resp,Resp]` with identity `combine`; own `AdapterName() == "file.ReadAdapter"`. The 3-type `ReadEachAdapter[In,T,Resp]` stays handle-first for enrichment — both existing is intentional, not duplication.
- **Out of scope, intentional**: `FilePattern` on `SourcePort` (`ScanAdapter` is line-oriented with a plain path, `WatchAdapter` emits paths — nothing to declare) and on `ToolPort` (file/SQL are storage, not serving transports).
- **`examples/sensor-service`** demonstrates both: `SQLPattern` on the polling `rowPort` (empty `QueryStreamOptions`), `FilePattern` calibration-lookup `IOPort` with `file.ReadAdapter`.

## Round 49 (inside-out pipeline wiring — Phase 5: full `api` module parity in `Pattern`, one construction path)

- **`ports` always calls `Register`, never `ClientHandle`, internally.** `Route`/`Channel`/`Tool.Register(builder)` is a strict superset of `ClientHandle()` — same decode/encode/param wiring, plus unknown-param-name checks, path/topic codec validation, security scheme/global security population, and (for `reqreply`/`mcp` only) duplicate-name detection. When no `Builder` is supplied, `ports` creates a private, single-use one with zero `Info` for that one `Register` call — same zero-ceremony default, identical code path. This makes a `Pattern`-derived handle indistinguishable from one hand-built via `Register` — adapters cannot tell the difference.
- **`PortOptions.RESTBuilder`/`EventBuilder`/`ReqReplyBuilder`/`MCPBuilder`** (`*rest.Builder`/`*events.Builder`/`*reqreply.Builder`/`*apimcp.Builder`) — supply your own (with `AddSecurityScheme`/`AddGlobalSecurity`/`rest.WithPathConstraints`/`events.WithTopicConstraints` already configured) to get full parity with a hand-registered route; the port's route/channel/tool accumulates directly into that builder's spec.
- **`NewSourcePort`/`NewSinkPort` now return `(*Port, error)`** (breaking, joining `NewIOPort`/`NewToolPort` from Phase 4) — `Register` is fallible in ways the old builder-free construction wasn't (unknown param names, path/topic constraint failures, duplicate names on `reqreply`/`mcp`). ~27 call sites updated across `ports/port_test.go`, `examples/sensor-service/main.go`, and 5 adapter `binding_test.go` files.
- **Fixed a real correctness bug found during the review**: `Pattern`-derived handles previously always had an empty `SecuritySchemes` map and `nil` `GlobalSecurity` (never populated by `ClientHandle`), meaning any `RouteMeta.Security`/`Subscribe.Security`/`Publish.Security` requirement on a `Pattern`-based port was silently unenforced (`validateSecurityCredentials` skips unknown scheme names rather than rejecting). Fixed by always registering against a real or private `Builder`.
- **`mqtt5`/`mqtt` `SubscribeAdapterOptions.TopicFilter`** now auto-derives an MQTT wildcard filter (`{var}` → `+`, e.g. `"sensors/{id}/data"` → `"sensors/+/data"`) from the handle's topic when empty, instead of subscribing with the raw, brace-containing topic string — the one confirmed adapter-option redundancy found during a full audit of every adapter's `XxxAdapterOptions` against what the `Pattern`-derived handle already carries (all other option fields — `SecurityFunc`, `Observer`, poll intervals, buffer sizes — are genuine protocol-specific glue, not redundant). `adapters/mqtt` gained its first `binding_test.go` in the process (previously zero coverage for `SubscribeAdapter`).
- **Correction discovered during implementation**: `rest.Route.Register` and `events.Channel.Register` do **not** detect duplicate routes/topics (only `reqreply.Route.Register` and `apimcp.Tool.Register` do, via `DuplicateRouteError`/an "already registered" error). Calling `ports.RegisterREST`/`RegisterEvent` with the same builder a `Pattern` already registered against does not error — it just adds a duplicate spec entry. Only `RegisterReqReply`/`RegisterMCP` reject the redundant call.
- **`examples/sensor-service`** updated: `sensorsPort`/`alertsPort` now share one `events.Builder` (via `PortOptions.EventBuilder`) configured with `events.WithTopicConstraints(validate.MQTTPublishTopic, sensorTopicConstraint)`, mirroring `examples/adapters-mqtt`'s builder-level constraint style but enforced through the port's `Pattern`; the example also prints the AsyncAPI spec built directly from the two ports' bindings.

## Round 48 (inside-out pipeline wiring — Phase 4: `Pattern` — ports as the primary declaration surface)

- **`ports.Pattern` sealed interface** + `RESTPattern{Method,Path,Opts []rest.RouteOpt}`, `EventPattern{Topic,Opts []events.ChannelOpt}`, `ReqReplyPattern{Topic,Opts []reqreply.RouteOpt}`, `MCPPattern{Name,Opts []apimcp.ToolOpt}` — thin wrappers reusing the *exact* `rest`/`events`/`reqreply`/`apimcp` option vocabulary (no new param types). `PortOptions.Patterns []Pattern` — one entry per protocol family a port binds to.
- **`RESTHandle[Req,Resp]`/`EventHandle[T]`/`ReqReplyHandle[Req,Resp]`/`MCPHandle[In,Out]`** accessor functions — return `(handle, false)` (not an error/panic) when the port declared no matching `Pattern`.
- **`RegisterREST`/`RegisterEvent`/`RegisterReqReply`/`RegisterMCP`** — replay a port's stored `Pattern` against a real spec `Builder`, building the OpenAPI/AsyncAPI/MCP doc *from* the binding.
- **`events.Channel[T].ClientHandle()`** and **`apimcp.Tool[In,Out].ClientHandle()`** added — mirror `rest.Route.ClientHandle()`/`reqreply.Route.ClientHandle()` (builder-free handle construction, no spec side effects).
- **`NewIOPort`/`NewToolPort` changed to `(*Port, error)`** — fail-fast `PatternRegisterError` on malformed `Pattern` (unknown param name, empty MCP tool name). `NewSourcePort`/`NewSinkPort` stayed infallible at this point (revisited/changed in Phase 5 above).
- **Scope note**: `RESTPattern` for `SourcePort` (HTTP ingest) and `SinkPort` (SSE) is not implemented — both need an asymmetric `Req`/`Resp` shape a single-codec port can't express with `RESTPattern{Method,Path,Opts}`.
- **`examples/sensor-service`** migrated: `sensorsPort`/`alertsPort` declare `ports.EventPattern` directly instead of building `events.Channel` + `Builder.Register` separately.

## Round 47 (inside-out pipeline wiring — Phase 3: gap analysis and fixes)

- **`ports.ValidateParams` + `ports.WithParams`/`ParamsFromContext`** added — real `IOParam` enforcement wired into `file.ReadEachAdapter`/`file.DrainWriteFileAdapter` (the only handle-less adapters). Handle-backed adapters (REST/events/MQTT5) already fully validate via their own handle mechanism — `IOParam`/`Params` is decorative there (this fact directly motivated Phase 4/5's `Pattern` design above).
- **`ports.bindWithObserver` helper** — real `RecordRequest("port.bind", "<port>/<adapter>", 200|500, duration)` + `TraceObserver` spans now fire from all 4 port `Bind` methods (previously dead `_ = obs` code in `SourcePort.Stream`/`IOPort.Connect`/`ToolPort.Bind` — only `SinkPort.Feed` actually used the observer before this fix).
- **`PortOptions.Buffer` on `IOPort`/`ToolPort`** confirmed as intentional, not a bug — neither has an internal channel to buffer (`Connect`/`Bind` delegate directly to the adapter's `Transform`/`Bind` call).
- **Test coverage added**: `chi.PipelineAdapter`, `zeromq.ServeAdapter`, `mqtt5.ServeAdapter` (zero tests before), `mcpgo.ToolLatestAdapter` strengthened (previous test only asserted `Bind` didn't error, never verified the cached value actually flows through a tool call).

---

## Round 46 (inside-out pipeline wiring — Phase 2: ToolPort, chi bindings, mcpgo bindings, server-side ToolAdapters)

- **`ports.ToolPort[In,Out]`** — new server-side request/response port; `NewToolPort`, `SetPipeline(fn)`, `Bind(ctx, ToolAdapter) error`; multiple Bind calls expose the same pipeline on multiple transports; `PortNoPipelineError{Port}` returned when Bind called before SetPipeline; 5 tests.
- **`ports.ToolAdapter[In,Out]` interface** — `Bind(ctx, fn func(ctx,In)Stream[Out]) error` + `AdapterName() string`; complement of `SourceAdapter`/`SinkAdapter`/`IOAdapter` for server-side request/response.
- **`adapters/chi/binding.go`** — `IngestAdapter[T]`, `SSEAdapter[Event]`, `PipelineAdapter[Req,Resp]` using chi router; `binding_test.go` added.
- **`adapters/mcpgo/binding.go`** — `ToolPipelineAdapter[In,Out]` (wraps `RegisterToolPipeline`) and `ToolLatestAdapter[In,Out]` (wraps `RegisterToolLatest`); `binding_test.go` added.
- **Server-side ToolAdapters added** to existing binding.go files: `nethttp.PipelineAdapter[Req,Resp]` (wraps `PipelineHandler`), `chi.PipelineAdapter[Req,Resp]`, `zeromq.ServeAdapter[Req,Resp]` (wraps `Serve` in goroutine), `mqtt5.ServeAdapter[Req,Resp]` (wraps `Serve` in goroutine).

---

## Round 45 (remove deprecated stream bridge helpers; update plan-a-new-codex-feature skill)

- **Deleted all deprecated stream bridge functions** — `SubscribeStream`, `DrainPublish`, `CallStream`, `HandlerIngest`, `RegisterIngest`, `SSEFromStream`, `PollStream`, `DrainCall`, `SSEClientStream`, `ScanStream`, `WatchStream`, `DrainWrite`, `ReadEachStream`, `TapWriteFile`, `DrainWriteFile`, `QueryStream`, `DrainInsert`, `QueryEachStream` removed from all adapter packages. Option types exclusively used by deleted functions also removed (e.g. `SSEClientOptions`). Shared option types reused by binding.go (`CallStreamOptions`, `DrainPublishOptions`, etc.) moved into binding.go.
- **Binding.go files updated to inline implementations** — each `ports.XxxAdapter.Activate`/`Transform` method now directly contains the implementation (was delegating to the now-deleted bridge functions); logic is unchanged.
- **Test files converted to adapter pattern** — `mqtt5/stream_test.go`, `zeromq/stream_test.go`, `nethttp/stream_test.go`, `nethttp/stream_sse_test.go`, `chi/stream_test.go`, `file/stream_test.go`, `sql/stream_test.go` rewritten to test via port adapters; `mqtt/stream_test.go` deleted (only tested removed functions).
- **sensor-service example updated** — stale bridge doc comments replaced with ports language; `QueryStream` → `QueryAdapter`, remaining comment cleanup.
- **`plan-a-new-codex-feature` skill updated** — added binding.go file pattern to research table, Files to create template, and Gotchas; new adapters must implement port interfaces not write stream bridge functions.
- **`review-go-codex` SKILL.md + checklist updated** — Stream Bridge Guardrail → Port Adapter Guardrail; B1 check for port interface; Gotchas updated.
- **docs/guides/stream-bridges.md rewritten** — new guide describes port adapter pattern, three port types, available adapters, IOParam, test adapters.
- **`go-codex.instructions.md` updated** — all adapter rows updated to describe binding.go constructors instead of deprecated stream bridges.

---

## Round 44 (inside-out pipeline wiring — `ports` package + adapter bindings)

- **`ports` package** — new `github.com/DaniDeer/go-codex/ports` package providing protocol-agnostic IO enforcement points: `SourcePort[T]` (inbound, fan-in), `SinkPort[T]` (outbound, fan-out), `IOPort[Req,Resp]` (intermediate 1:N transform); `IOParam{Name,Description,Codec,Required}.WithCodec(c)` for protocol-agnostic param declarations; `PortOptions{Params, Buffer, Observer}`; `SourceAdapter[T]`, `SinkAdapter[T]`, `IOAdapter[Req,Resp]` interfaces; `ChanSourceAdapter`, `ChanSinkAdapter`, `FuncIOAdapter` test helpers; `PortBindError{Port,Adapter,Err}` + `PortNoAdapterError{Port}` — both `slog.LogValuer`; 17 tests covering fan-in, fan-out, IOPort, error types.
- **Adapter binding constructors** — `binding.go` added to every adapter package wrapping existing stream bridge machinery as `SourceAdapter`/`SinkAdapter`/`IOAdapter` implementations: `mqtt5.SubscribeAdapter/PublishAdapter/CallAdapter`, `mqtt.SubscribeAdapter/PublishAdapter`, `nethttp.IngestAdapter/SSEAdapter/CallAdapter/PollAdapter/DrainCallAdapter`, `zeromq.SubscribeAdapter/PublishAdapter/CallAdapter`, `file.ScanAdapter/WatchAdapter/ReadEachAdapter/DrainWriteAdapter/DrainWriteFileAdapter`, `sql.QueryAdapter/QueryEachAdapter/DrainInsertAdapter`.
- **Stream bridge helpers deprecated** — all `SubscribeStream`, `DrainPublish`, `CallStream`, `HandlerIngest`, `ScanStream`, `WatchStream`, `QueryStream`, etc. marked with `//Deprecated:` godoc; non-stream functions (`Subscribe`, `Publish`, `Call`, `Serve`, `Handler`) kept as-is.
- **`examples/sensor-service`** updated — replaced 3 deprecated bridge calls with `ports.SourcePort.Bind(mqtt.SubscribeAdapter(...))`, `ports.SinkPort.Bind(mqtt.PublishAdapter(...))`, `ports.SourcePort.Bind(sql.QueryAdapter(...))`.

---

## Round 43 (stream bridge completeness — MQTT/MQTT5 SubscribeStream ergonomic fix, file.ReadEachStream)

- **`mqtt.SubscribeStream` ergonomic fix** — breaking change: old signature returned `(Stream[T], pahomqtt.MessageHandler)`, forcing caller to call `client.Subscribe(filter, qos, handler)` manually; new signature takes `client pahomqtt.Client` + `qos byte`, subscribes internally, returns `Stream[T]` only; added `TopicFilter string` to `SubscribeOptions` (MQTT wildcard filter, e.g. `"sensors/+/data"`; falls back to `handle.Topic` when empty); updated `examples/sensor-service/main.go`; test refactored to `deliverableClient` mock that captures the Subscribe handler internally.
- **`mqtt5.SubscribeStream` ergonomic fix** — same breaking change: old signature returned `(Stream[T], func(*paho.Publish))`, forcing caller to register handler with router; new signature takes `client MQTTClient` + `router MQTTRouter` + `qos byte`, calls `router.RegisterHandler` + `client.Subscribe` internally, returns `Stream[T]` only; added `TopicFilter string` to `mqtt5.SubscribeOptions`; tests updated to use `mockBroker` + `mockRouter` (already in package).
- **`file.ReadEachStream`** — new enrichment bridge: `ReadEachStream[In,T,Out](ctx, format.File[T], src Stream[In], varsFor func(In)map[string]string, combine func(In,T)Out, ReadEachStreamOptions) Stream[Out]`; reads a complete typed file for each upstream item; `varsFor` maps item → path template vars; `combine` pairs original item with file content; read errors → `ReadError{Err}` in `Stream.Errors` + `OnError`; upstream errors forwarded; when `src.Values` closes, remaining items in `src.Errors` are drained before the output stream closes; `ReadError{Err}` added to `adapters/file/errors.go` (implements `Unwrap()` + `slog.LogValuer`); 3 tests.

---

## Round 42 (stream bridge completeness — nethttp.CallStream, file write bridges, sql.QueryEachStream)

- **`nethttp.CallStream`** — HTTP was the only transport missing a `CallStream` intermediate I/O operator; added `CallStream[Req,Resp](ctx, client, baseURL, handle, src, opts)` + `CallStreamOptions{Vars, CallOpts, Buffer}` mirroring `zeromq.CallStream`/`mqtt5.CallStream`; full codec validation per item; errors go to `Stream.Errors` as typed `UnexpectedStatusError`/`RequestError` etc.; 3 tests.
- **`file.TapWriteFile`** — declarative whole-file write as a stream tap (stream continues); `TapWriteFileOptions{OnError, Observer, FileOptions}`; observer resolved from ctx when nil; 2 tests.
- **`file.DrainWriteFile`** — declarative whole-file write as a terminal drain sink; `DrainWriteFileOptions{OnError, Observer, FileOptions}`; 1 test.
- **`sql.QueryEachStream`** — per-item parameterized SQL lookup; `QueryEachStream[In,T](ctx, codec, src, queryFn, QueryEachStreamOptions)`; calls queryFn for each stream item, validates each row via codec; database errors → `QueryStreamError`; validation errors → `RowValidationError`; 4 tests.

---

## Round 41 (DrainWriteOptions.Observer test coverage)

- **G1 [trivial] — `DrainWriteOptions.Observer` field had no test**: The `Observer` field added to `DrainWriteOptions` in Phase 0 had no test verifying that `stats.ReportErrors` fires on codec-rejected items; added `TestDrainWrite_ObserverReceivesEncodeError` (explicit observer receives `RecordValidationError` on encode failure) and `TestDrainWrite_ContextObserver` (observer resolved from ctx via `stats.WithObserver` when `Options.Observer` is nil).

---

## Round 40 (stream bridge — Vars gap in HTTP client bridges, chi SSE tests)

- **G1 [small] — `PollStreamOptions` missing `Vars map[string]string`**: `PollStream` passed `nil` for the `vars` parameter to `Call`, making routes with path params (e.g. `/metrics/{sensorID}`) silently fail with `MissingPathVarError` on every poll; added `Vars map[string]string` to `PollStreamOptions` and passed `opts.Vars` to `Call`, matching the pattern used by `mqtt`, `mqtt5`, and `zeromq` `DrainPublish` options.
- **G2 [small] — `DrainCallOptions` missing `Vars map[string]string`**: Same issue — `DrainCall` passed `nil` for `vars`, leaving callers with no way to specify path vars for parameterised sink routes; added `Vars map[string]string` to `DrainCallOptions` (static map per item, documented limitation identical to `DrainPublish`).
- **G3 [small] — `SSEClientStream` URL built from raw path template without var substitution**: `url := baseURL + handle.Descriptor.Path` would produce a malformed URL for SSE routes with path vars (e.g. `/events/{machineID}`); added `Vars map[string]string` to `SSEClientOptions` and replaced the concatenation with `handle.BuildPath(opts.Vars)` — a `BuildPath` failure emits `SSEConnectError` and terminates the stream.
- **G4 [trivial] — `mqtt5.SubscribeStream` comment incorrectly stated `handle.SubscribeFormats`/`handle.Formats` are consulted**: `effectiveFmts = [fmt]` is always non-empty so neither field is ever read; updated comment to accurately state the provided `fmt` is always used exclusively.
- **G5 [trivial] — `chi.SSEFromStream` and `chi.SSEFromHub` had no tests**: Added `TestChiSSEFromStream_EmitsStreamItems`, `TestChiSSEFromStream_StreamErrorCallsOnError`, and `TestChiSSEFromHub_BroadcastsToAllClients` to `adapters/chi/stream_test.go`, mirroring the nethttp SSE bridge tests.

---

## Round 39 (gap implementation — BroadcastHub, SSE bridges, tests)

- **G1 [bug] — `SSEClientStream` signature used `*rest.SSERouteHandle[struct{}, Event]` — too restrictive**: The `struct{}` Req constraint prevented using any typed request handle with `SSEClientStream`; changed the signature to `SSEClientStream[Req, Event any](..., *rest.SSERouteHandle[Req, Event], ...)` to accept any Req type.
- **G2 [bug] — `TestSSEFromHub_BroadcastsToAllClients` deadlocked due to sequential `Do()` calls**: `SSEHandler` commits `WriteHeader(200)` on first `send` (not on connection); sequential `makeClient()` calls blocked waiting for headers — first client could never unblock until events were sent, which required both clients to be connected first; rewrote test to connect both clients via goroutines so the first event emission unblocks both `Do()` calls.
- **G3 [small] — `TestPollStream_EmitsResponsePerTick` used route with `{id}` path var but `getReq` is empty struct**: `Call` pre-flight validation for path variables found `{id}` unresolvable and returned `MissingPathVarError`; changed test route to `/users/latest` (no path params) so polling works.
- **G4 [trivial] — `stream/broadcast_test.go` unnecessary blank identifier assignment**: `_ = <-done1` / `_ = <-done2` triggered staticcheck S1005; changed to `<-done1` / `<-done2`.
- **G5 [small] — `adapters/nethttp/stream.go` unhandled `resp.Body.Close()` errors flagged by gosec G104**: Two `resp.Body.Close()` calls on error paths in `SSEClientStream` had unhandled errors; changed to `_ = resp.Body.Close()` (no useful error recovery on connection teardown).

---

## Round 38 (mcpgo bridge layout and SKILL maintenance)

- **G1 [trivial] — `SKILL.md` missing `ToolPipelineHandler` in Phase 1 table and Rule B1**: Phase 1 file description listed only `ToolLatestHandler`; Rule B1 table had no row for `ToolPipelineHandler`; updated both to include `ToolPipelineHandler` and added Gotcha explaining the distinction between the two patterns.
- **G2 [trivial] — `RegisterToolPipeline` and `RegisterToolLatest` had no tests**: Added `TestRegisterToolPipeline_AddsTool` and `TestRegisterToolLatest_AddsTool` verifying both convenience wrappers register without panic.
- **G3 [trivial] — `mcpgo/stream.go` layout: `RegisterToolLatest` separated from `ToolLatestHandler`; stale section comment**: A stale `// ── ToolLatestHandler` section comment appeared before `RegisterToolLatest` instead of before `ToolLatestHandler`; `errNoResult` was declared at the top with `errNoLatestValue` instead of adjacent to `ToolPipelineHandler`; restructured by adding `// ── ToolLatestHandler` before `ToolLatestHandler`, moving `errNoResult` adjacent to `ToolPipelineHandler`, and removing the stale comment before `RegisterToolLatest`.

---

## Round 37 (stream bridge review — bugs, errors, test coverage)

- **G1 [small] — `zeromq.ServeLatest` double-calls `opts.OnError` for no-value case**: When the latest pointer was nil, the fn body called `onErr(NoLatestValueError{...})` directly AND `Serve` then called `serveOpts.OnError(ServeError{KindHandler, ...})`, so `opts.OnError` fired twice; fixed by removing the direct call from fn and detecting `NoLatestValueError` via `errors.As` in the `serveOpts.OnError` wrapper, delivering the typed error without double-firing.
- **G2 [trivial] — `mqtt5` bridge functions had no tests**: `mqtt5.SubscribeStream`, `mqtt5.DrainPublish`, `mqtt5.AsPipelineFunc`, and `mqtt5.CallStream` had no dedicated tests; added `adapters/mqtt5/stream_test.go` covering happy path, decode errors routed to `Stream.Errors`, error precedence, `PipelineNoResponseError`, and upstream error forwarding.
- **G3 [trivial] — `chi` bridge helpers had no behavioral tests**: `chi.HandlerLatest`, `chi.HandlerIngest`, and `chi.PipelineHandler` had only error-type tests; added `adapters/chi/stream_test.go` covering latest value return, 503 before first value, ingest push + full channel 503, pipeline value + error + Tap observation + no-value `PipelineNoResponseError`.
- **G4 [trivial] — `mcpgo.ToolLatestHandler` had no tests**: Added `adapters/mcpgo/stream_test.go` covering: latest value returned (success), no-value case `IsError=true` with "no value computed yet" message, input validation still runs (constrainedInputCodec rejects negative input), observer receives `RecordRequest(200)` on success.
- **G5 [trivial] — `AsPipelineFunc` used hardcoded transport name in `PipelineNoResponseError.Topic`**: `mqtt5.AsPipelineFunc` returned `PipelineNoResponseError{Topic: "mqtt5"}` and `zeromq.AsPipelineFunc` returned `{Topic: "zeromq"}` — misleading since the actual topic is unknown; changed both to `Topic: ""` with a godoc comment explaining the empty value.
- **G6 [trivial] — `mcpgo.ToolLatestHandler` used wrong error type for no-value state**: Returned `apimcp.ToolInputError{Name: ..., Err: errNoLatestValue}` when no value was available, producing `"tool getOEE input: no value computed yet"` — the "input:" prefix is semantically wrong (not an input problem); changed to return `errNoLatestValue` directly so `ToolHandler` produces `mcp.NewToolResultError("no value computed yet")`.

---

## Round 36 (HTTP bridge codec-layer review — documentation)

- **G1 — `HandlerIngest` missing "Codec coverage" godoc**: All 9 HTTP codec layers run (body, query, cookie, header, path, security, response body, response headers, response cookies) but the godoc didn't document this; added "Codec coverage" section noting that only body `Req` is pushed to the channel and that path/query/cookie/header param VALUES (though validated) are not included; added `Handler`-direct workaround with `RequestFromContext(ctx)` example.
- **G2 — `PipelineHandler` missing param-access and response-header documentation**: No godoc explained how to access path/query/cookie/header param values inside the pipeline via `RequestFromContext(ctx)`, or how `WithResponseHeaders(ctx, ...)` works in sequential pipelines; added "Codec coverage — all HTTP layers" and usage examples for both.
- **G3 — `HandlerLatest` missing "Codec coverage" godoc**: All request codec layers validate even though `Req` is discarded (intentional — ensures well-formed requests receive cached responses); documented with a note in godoc.
- **G4 — `stream-bridges.md` missing codec coverage table**: The guides chapter lacked a table showing all 9 HTTP codec layers and how each bridge exposes param values; added comprehensive "Codec coverage" table and per-pattern param-access documentation.
- **G5 — `review-go-codex` skill missing stream bridge checks**: The skill's Phase 1 file list, checklist (Section 11), and guardrails did not cover stream bridges; added bridge files to Phase 1 read list, added `Stream Bridge Consistency` as checklist Section 11, added `Stream Bridge Guardrail` with B1–B4 rules, and added bridge-specific Gotchas.

---

## Round 35 (stream bridge codec bypass fixes)

- **G1 [bug] — `mqtt.SubscribeStream` bypassed `SubscribeHandler`**: The bridge used a hand-rolled handler that pushed raw `msg.Payload()` bytes to a channel, skipping security enforcement, format priority chain (`SubscribeFormats`/`Formats`), topic-var error reporting, and proper observer calls; replaced with `SubscribeHandler(ctx, handle, fn, innerOpts, fmt)` + typed channel, routing all adapter errors to `Stream.Errors` as `mqtt.SubscribeError`.
- **G2 [bug] — `mqtt5.SubscribeStream` bypassed `makeSubscribeMessageHandler`**: Same root cause as G1 — raw handler skipped ContentType negotiation, `UserPropertyParams` validation, security enforcement, and observer calls; extracted `makeSubscribeMessageHandler` from `Subscribe` (removing code duplication) and used it in `SubscribeStream` with `innerOpts.OnError` overriding to route errors to `Stream.Errors`.
- **G3 [small] — `zeromq.CallStream` missing `Vars` in options**: `CallStreamOptions` had no `Vars` field even though the underlying `Call` function supports topic variable codec validation via `Vars map[string]string`; added `Vars map[string]string` to `CallStreamOptions` and passed it to each `Call` invocation.
- **G4 [small] — `mqtt.DrainPublish` / `mqtt5.DrainPublish` / `zeromq.DrainPublish` static-Vars limitation undocumented**: The `Vars` field applies the same map to every item — per-item topic var substitution is impossible; added godoc note explaining the limitation and directing users to `stream.Drain` + `Publish` for per-item vars.

---

## Round 34 (stream doc and test correctness)

- **G1 — `stream/doc.go` stale `FromCodec` example**: Package-level pipeline example passed raw `sensorCodec` (a `codex.Codec[T]`) directly to `FromCodec` but the signature requires `format.Format[T]`; corrected to `format.JSON(sensorCodec)`.
- **G2 — `stream/topology.go` Topology godoc stale `WithApply` chain**: Example showed `.WithApply(oeeCalcFn)` as a chained method call but `WithApply` is a free function; restructured example to show `stream.WithApply(topo, oeeCalcFn)` as a separate statement with an explanatory comment.
- **G3 — `render/stream/render.go` package doc stale `WithApply` chain**: Same `.WithApply(oeeCalcFn)` method-call bug in the render package doc; fixed with same restructure.
- **G4 — `stream/topology_test.go` inconsistent import alias**: All other files in the `stream_test` package use `stream` as the alias for `github.com/DaniDeer/go-codex/stream`; `topology_test.go` used `gstream`; renamed alias to `stream` for consistency.
- **G5 — `render/stream/render_test.go` missing coverage for R33 step kinds**: `TestRender_WithSteps` only covered source/filter/sink; added `TestRender_WithPhase3StepKinds` exercising all seven step kinds added in R33 (merge, tee, window, slidingWindow, combineLatest, zip, flatMapSlice).
- **G6 — `stream/sink_test.go` `DrainOptions.Observer` path untested**: No test verified that `stats.ReportErrors` fires `RecordValidationError` when `onValue` returns a `codex.ValidationErrors`; added `TestDrain_ObserverCalledOnValueError`.

---

## Round 33 (stream topology parity)

- **G1 — `StepKindMerge`/`StepKindTee` constants had no builder methods**: `topology.go` exported `StepKindMerge` and `StepKindTee` constants but `Topology` had no `WithMerge(desc)` or `WithTee(desc)` methods; added both methods matching the `With*` builder pattern.
- **G2 — Phase 3 operators missing from Topology**: `Window`, `SlidingWindow`, `FlatMapSlice`, `CombineLatest`, and `Zip` operators all had no `StepKind*` constants or `With*` builder methods; added `StepKindWindow`, `StepKindSlidingWindow`, `StepKindCombineLatest`, `StepKindZip`, `StepKindFlatMapSlice` constants and corresponding `WithWindow`, `WithSlidingWindow`, `WithCombineLatest`, `WithZip`, `WithFlatMapSlice` methods.
- **G3 — `topology_test.go` missing coverage for new step kinds**: `TestTopology_Steps` only exercised 7 step kinds; extended to exercise all 14 `With*` builder methods and added `TestTopology_AllStepKindConstants` to verify every exported constant maps to its expected string value.
- **G4 — No `ExampleNewTopology()` or `ExampleRender()` functions**: pkg.go.dev showed no runnable examples for the topology builder or YAML renderer; added `ExampleNewTopology()` in `stream/topology_test.go` and `ExampleRender()` in `render/stream/render_test.go`.

---

## Round 32 (adapters/sql test quality)

- **G1 — `TestMigrationError_LogValue` weak assertion + dead code**: Contained a no-op `import_slog_for_test` closure that was immediately discarded, and only checked `Kind().String() != ""`; replaced with `KindGroup` assertion + field-key presence checks (`op`, `version`, `err`) to match the parallel `TestValidate_LogValue` pattern.
- **G2 — `TestValidate_ObserverCalledOnFailure` missing error type assertion**: Checked `spy.validations[0].err != nil` but never verified the error passed to `RecordValidation` is the context-enriched `RowValidationError` (with `Table` and `Op` fields set); added `errors.As` check and field assertions to confirm the adapter passes the wrapper, not the raw codec error.
- **G3 — `MigrationError.Unwrap()` had no errors.As chain test**: `RowValidationError` had `TestValidate_ErrorsAs_ValidationErrors` verifying traversal to the inner codec error; added `TestMigrationError_Unwrap` verifying `errors.Is` and `errors.Unwrap` reach the inner goose error through `MigrationError`.

---

## Round 31 (stats observer godoc + SQLObserver fanout test)

- **G1 — `NoopObserver` godoc stale**: Comment listed only four interfaces ("satisfies Observer, ValidationObserver, PipelineObserver, and SecurityObserver") but `NoopObserver` also implements `FileObserver`, `SQLObserver`, and `TraceObserver` added in later rounds; updated to "all observer interfaces" with full list.
- **G2 — `LoggingObserver` godoc incorrect**: Comment said "implements all observer interfaces" but `LoggingObserver` intentionally does not implement `TraceObserver` (slog has no distributed tracing concept); changed to "all observer interfaces except `[TraceObserver]`" with explanation.
- **G3 — `NewFanout` godoc stale**: Comment listed only `FileObserver`, `SecurityObserver`, `PipelineObserver` as optional interfaces implemented by `fanout`; added `SQLObserver` and `TraceObserver` which were added in subsequent rounds.
- **G4 — Missing `fanout` SQLObserver delegation test**: `TestFanout_TraceObserver_OnlyToImplementors` existed for `TraceObserver` delegation but no equivalent test verified `SQLObserver` delegation; added `TestFanout_SQLObserver_OnlyToImplementors` and `TestFanout_SQLObserver_SkipsNonImplementors` following the same pattern.

---

## Round 30 (mcpgo ToolOutputError wrapping + stale test names)

- **G1 — `adapters/mcpgo` ToolOutputError discarded in fmt.Errorf wrap**: `fmt.Errorf("...: %w", toe.Err)` at adapter.go:118 wrapped the inner codec error and discarded the `ToolOutputError` outer wrapper, making `errors.As(err, &mcp.ToolOutputError{})` fail; changed `toe.Err` → `err` so the typed sentinel stays in the error chain.
- **G2 — `adapters/mqtt5/reqreply_test.go` stale test function names**: 21 test functions were still named `TestServeRequestReply_*` and `TestRequest_*` after the R29 rename of the API to `Serve`/`Call`; renamed all affected test functions and their string literals to match.

---

## Round 28 (slog.LogValuer parity)

- **G1 — `adapters/mqtt` error types missing `LogValue()`**: `SubscribeError` and `PublishEncodeError` lacked `LogValue() slog.Value` while their `adapters/mqtt5` and `adapters/zeromq` equivalents had it; added `LogValue()` to both types and `"log/slog"` import.
- **G2 — `adapters/mqtt.TopicMismatchError` missing `LogValue()`**: `TopicMismatchError` lacked `LogValue() slog.Value`; added implementation emitting `template` and `topic` fields.
- **G3 — `adapters/nethttp` client error types missing `LogValue()`**: `UnexpectedStatusError`, `RequestBuildError`, `RequestError`, `ResponseBodyError` (added R19) all lacked `LogValue() slog.Value`; added implementations to all four types.
- **G4 — `api/mcp` error types missing `LogValue()`**: All 8 MCP error types (`ToolInputError`, `ToolOutputError`, `ResourceEncodeError`, `ResourceParamError`, `MissingResourceVarError`, `PromptArgError`, `MissingPromptArgError`, `InvalidResourceParamError`) lacked `LogValue()` while `api/reqreply` errors (same layer) had it; added `LogValue()` to all 8.
- **G5 — `examples/adapters-mqtt5` observer mixed concerns**: `exampleObserver` called `o.logger.Warn/Info` directly inside `RecordValidationError`, `RecordRequest`, `RecordSubscribe`, `RecordPublish` method bodies; replaced with `stats.NewFanout(eventCounter, stats.NewLoggingObserver(logger))` separating metric counting from logging.

---

## Round 27 (mqtt5 BrokerError)

- **G1 — `adapters/mqtt5.Subscribe` bare `fmt.Errorf` on broker subscribe failure**: `client.Subscribe()` failure returned bare `fmt.Errorf("mqtt5: subscribe: %w", err)`; replaced with typed `BrokerError{Op: "subscribe", Err: err}` — callers can now `errors.As`-distinguish broker failures from codec errors.
- **G2 — `adapters/mqtt5.Publish` bare `fmt.Errorf` on broker publish failure**: `client.Publish()` failure returned bare `fmt.Errorf("mqtt5: publish: %w", err)`; replaced with `BrokerError{Op: "publish", Err: err}`.
- **G3 — `adapters/mqtt5.ServeRequestReply` bare `fmt.Errorf` on broker subscribe failure**: same pattern; replaced with `BrokerError{Op: "subscribe", Err: err}`; added `TestBrokerError_LogValue`, `TestBrokerError_ErrorsAs`, `TestBrokerError_ErrorString`, `TestSubscribe_BrokerError_OnSubscribeFail`, `TestPublish_BrokerError_OnPublishFail`; updated `go-codex.instructions.md`.

---

## Round 26 (zeromq SocketError + dead code)

- **G1 — `adapters/zeromq` bare `fmt.Errorf` for socket infrastructure failures**: Eight bare `fmt.Errorf` returns in `Subscribe`, `Publish`, `Serve`, and `ServeRouter` (SetSubscription failure, SetRecvTimeout failure, recv failure, send failure) replaced with typed `SocketError{Op string, Err error}` that implements `Unwrap()` and `slog.LogValuer`; callers can now `errors.As`-distinguish socket setup from I/O failures; added `TestSocketError_LogValue`, `TestSocketError_ErrorsAs`, `TestSocketError_ErrorString`, `TestSubscribe_SocketError_OnSetSubscriptionFail`; updated `go-codex.instructions.md`.
- **G2 — `api/zeromq/builder.go` dead `tagsToSlice` function**: `tagsToSlice([]string) []string` was a no-op identity function used at one call site; removed and replaced with direct `meta.Tags` reference.

---

## Round 25 (PublishEncodeError + checklist housekeeping)

- **G1 — checklist stale `FilePathParamError{Param,Err}` / `MissingFilePathVarError{Param}` field names**: Updated checklist §7 to reflect actual struct layouts `{Name,Value,Err}` / `{Name}`; added `FilePatchNotSupportedError{Path}` to the table; added note that all 7 file error types implement both `Unwrap()` and `slog.LogValuer`.
- **G2 — checklist missing `slog.LogValuer` note for file errors**: All 7 file error types gained `LogValue()` during the Patch work; checklist now documents this.
- **G3 — `adapters/mqtt.Publish` bare `fmt.Errorf` on encode failure**: Replaced with typed `PublishEncodeError{Topic,Err}` (parallel to `SubscribeError`); added `errors.As`-navigable godoc; added `TestPublish_EncodeError_returnsPublishEncodeError` and `TestPublishEncodeError_ErrorAndUnwrap` in `adapters/mqtt/adapter_test.go`; added new checklist §7 `adapters/mqtt (Publish)` table; updated `go-codex.instructions.md` package table and detail section.

---

## Round 23 (Stale codex.Field struct literals in test files)

- **G1 — `format/format_test.go:19` stale `codex.Field[T,V]{}`**: Single `codex.Field[struct{N int}, int]{Required:true}` replaced with `codex.RequiredField(...)`.
- **G2 — `format/env_test.go` stale `codex.Field[T,V]{}`**: 10 occurrences across `flatCodec`, `nestedCodec`, `sliceCodec`, `nullableCodec` replaced with `codex.RequiredField` / `codex.OptionalField` constructors.
- **G3 — `codex/object_test.go:19-32` stale `codex.Field[T,V]{}`**: `pointCodec()` helper — two fields replaced with `codex.RequiredField` / `codex.OptionalField`.
- **G4 — `codex/codec_test.go:62-75` stale `codex.Field[T,V]{}`**: `TestCodecValidate_StructAllFields` inline codec — two required fields replaced with `codex.RequiredField`.
- **G5 — `codex/union_test.go:23-39,163-169` stale `codex.Field[T,V]{}`**: `vehicleCodec()` and `TestTaggedUnion_SchemaMutation_Regression` — four fields replaced with `codex.RequiredField`.

---

## Round 22 (File I/O API completeness + example stale pattern)

- **G3 — `File.PathParamSchemas()` missing implementation**: `FilePathParam.Codec` godoc and `go-codex.instructions.md` both referenced `File.PathParamSchemas() map[string]schema.Schema` but the method was never implemented; added the method to `format/file.go` (requires `schema` import) with three new tests in `format/file_test.go`.
- **G2 — `File.Update` signature stale in instructions.md**: `func(T)(T,error)` corrected to `func(T) T` — the transform function has no error return.
- **G4 — `FilePathParamError` / `MissingFilePathVarError` field names stale in instructions.md**: `{Param, Err}` → `{Name, Value, Err}` and `{Param}` → `{Name}` to match actual struct declarations.
- **G5 — instructions.md incorrectly claimed file errors implement `slog.LogValuer`**: removed `+ slog.LogValuer` — file error types only implement `Unwrap()`.
- **G1 — `examples/png-upload/main.go` download route used stale `Codec: &` pattern**: `PathParam{Codec: &uuidCodec}` and `CookieParam{Codec: &sessionTokenCodec}` replaced with `.WithCodec(uuidCodec)` / `.WithCodec(sessionTokenCodec)`.

---

## Round 21 (Binary codec, validators, and format.Binary)

- **`codex.Bytes()` renamed → `codex.Base64()`**: Old `codex.Bytes()` encoded/decoded via base64 — renamed to `Base64()` to match its actual behaviour (schema `{type:"string",format:"byte"}`). All callers updated.
- **New `codex.Bytes()` — raw bytes**: New `Bytes()` codec with identity Encode/Decode, schema `{type:"string",format:"binary"}`. `TypeMismatchError` on non-`[]byte` Decode. For binary file I/O and HTTP binary bodies.
- **`validate.HasPrefix(prefix []byte)`**: New general magic-byte constraint; produces `ConstraintError`. Prefer built-in format constants for known formats.
- **`validate.PNG/JPEG/GIF/WebP/PDF/ZIP`**: Predefined `Constraint[[]byte]` values for common binary file formats. Follow `validate.Email`/`validate.UUID` pattern. No Schema annotation. Produce `ConstraintError`.
- **`format.Binary(c codex.Codec[[]byte]) Format[[]byte]`**: New format constructor — identity marshal/unmarshal, validates via `c.Validate`, default CT `"application/octet-stream"`. Unlike Gob, Binary writes raw bytes (no framing). Works with MQTT, HTTP, and `File[T]` adapters.
- **`format/format.go` `Format` struct godoc stale**: "Use JSON, YAML, TOML, or Gob" updated to include `Binary`.

---

## Round 20 (Test file codec syntax + transport error tests)

- **G1 — Stale `codex.Field[T,V]{...}` in test helpers**: Four test files (`api/rest/builder_test.go`, `api/events/builder_test.go`, `adapters/nethttp/adapter_test.go`, `adapters/mqtt/adapter_test.go`) used verbose struct literal syntax for test codecs; replaced all 14 occurrences with `codex.RequiredField` / `codex.OptionalField` constructors, matching the pattern enforced in examples since R8.
- **G2 — Missing tests for R19 transport error types**: `RequestBuildError`, `RequestError`, and `ResponseBodyError` introduced in R19 had no unit tests; added `TestRequestBuildError_ErrorAndUnwrap`, `TestRequestError_ErrorAndUnwrap`, `TestResponseBodyError_ErrorAndUnwrap` to `adapters/nethttp/client_test.go` covering `Error()` string format and `errors.Is`/`errors.As` chain traversal.

---

## Round 19 (Client-side adapter structured errors + test coverage)

- **G1 — Bare `fmt.Errorf` in `adapters/nethttp/client.go`**: Three transport error paths returned bare wrapped errors; replaced with typed `RequestBuildError{Err}`, `RequestError{Method,Path,Err}`, and `ResponseBodyError{Err}` so callers can `errors.As`-inspect all failure modes.
- **G2 — `strings.NewReader(string(bodyBytes))` inefficiency**: Redundant `[]byte→string` copy in request body encoding; replaced with `bytes.NewReader(bodyBytes)`.
- **G3 — Missing `EncodeRequest`/`DecodeResponse` tests**: Added `TestRouteHandle_EncodeRequest_roundTrip` and `TestRouteHandle_DecodeResponse_roundTrip` to `api/rest/builder_test.go`.
- **G4 — Missing `Route.ClientHandle()` tests**: Added `TestRoute_ClientHandle_returnsHandle`, `_notRegisteredWithBuilder`, and `_encodeDecodeRoundTrip` to `api/rest/builder_test.go`.
- **G5 — `CallOptions.Observer` godoc missing status-0 semantics**: Updated godoc to document that status 0 is passed to `RecordRequest` when a pre-flight validation failure prevents any HTTP call from being sent.

Skill updates:
- `SKILL.md`: added `adapters/nethttp/client.go` to Phase 1 file list; added client-side typed error table and observer status-0 rule to Structured Errors / Observer guardrails.
- `references/checklist.md`: added `adapters/nethttp` client error table (section 7), client observer rules (section 8), and `nethttp/client_test.go` coverage row (section 9).

---

## Round 18 (MCP test coverage + godoc parity)

- **G1 — `ResourceParam.WithCodec` / `PromptArg.WithCodec` missing tests**: added `TestResourceParam_WithCodec_setsCodecWithoutAddressOf`, `_returnsDistinctCopy`, `TestPromptArg_WithCodec_setsCodecWithoutAddressOf`, `_returnsDistinctCopy` — mirrors the pattern established in R12/R13 for all `WithCodec` methods.
- **G2 — Tags propagation tests absent**: `ToolMeta.Tags`, `ResourceMeta.Tags`, `PromptMeta.Tags` added in R16 had no tests verifying they flow to handles and `MCPSpec`; added `TestToolMeta_Tags_flowToHandleAndSpec`, `TestResourceMeta_Tags_flowToHandleAndSpec`, `TestPromptMeta_Tags_flowToHandleAndSpec`.
- **G3 — `PromptArgError` / `MissingPromptArgError` missing `errors.As` examples**: added usage examples matching the style of every other error type in `api/mcp/errors.go`.

---

## Round 17 (MCP API consistency — errors, methods, ValidateArgs fix, README)

- **G1 — `Resource.Register` bare `fmt.Errorf` for unknown URI param**: replaced with typed `InvalidResourceParamError{Name, URITemplate}` so callers can `errors.As` the registration failure (mirrors `InvalidPathParamError` / `InvalidTopicParamError`).
- **G2 — `ValidateArgs` empty-string bug**: changed `!ok || val == ""` to `!ok` only — a present-but-empty arg is now passed to the codec rather than silently skipping validation; codec decides whether `""` is acceptable.
- **G3 — error name inconsistency**: renamed `ResourceURIVarError` → `ResourceParamError` and `MissingResourceURIVarError` → `MissingResourceVarError` to match cross-layer `PathParamError`/`TopicParamError` and `MissingPathVarError`/`MissingTopicVarError` pattern.
- **G4 — function fields converted to methods**: `BuildURI`, `ValidateURIVars` (on `ResourceHandle`) and `ValidateArgs` (on `PromptHandle`) converted from function fields to proper methods, matching how `BuildTopic`/`ValidateTopicVars` work on `ChannelHandle`. `ResourceHandle` now stores `uriParams []ResourceParam` internally.
- **G5 — godoc parity**: added `errors.As` usage examples to `ToolOutputError` and `ResourceEncodeError` (matching `ToolInputError` style).
- **G6 — README MCP section**: added dedicated `### MCP Server Adapter` section (after templ section) with full code example, key behaviour bullets, structured errors table, observer location values, and link to `examples/adapters-mcp`.

---

## Round 16 (MCP adapter test coverage + Tags parity)

- **G1 — Missing `ResourceHandler`/`PromptHandler` tests**: `adapters/mcpgo/adapter_test.go` only covered `ToolHandler`; added 10 new tests for `ResourceHandler` (happy path, handler error, encode error, template vs literal URI detection, observer) and `PromptHandler` (happy path, missing required arg, handler error, descriptor name, observer).
- **G2 — `ResourceMeta`/`PromptMeta` missing `Tags`**: `ToolMeta` had `Tags []string` but `ResourceMeta` and `PromptMeta` did not, creating within-layer inconsistency; added `Tags []string` to both Meta structs, `ResourceHandle`, `PromptHandle`, `ResourceSpec`, and `PromptSpec`.
- **G3 — `history.md` not updated for R15**: R15 (Format struct godoc Gob fix) was applied to `format/format.go` but `history.md` was never updated; added R15 section and updated header.

---

## Round 15 (Format struct godoc — Gob omission)

- **G1 — `Format` struct godoc missing Gob**: `Format` struct godoc listed "JSON, YAML, or TOML" as the construction options, omitting the newly added `Gob` constructor; updated to "JSON, YAML, TOML, or Gob".

---

## Round 1–3 (Declarative Route/Channel API)

- **Declarative constructors**: `NewRoute`, `NewSSERoute`, `NewChannel` added — replaces `AddRoute`/`AddChannel` imperative pattern.
- **`RouteMeta` / `ChannelMeta` structs**: unified metadata (title, summary, description, tags) as struct literals replacing per-field builder calls.
- **`WithFormats` on RouteHandle**: replaces manual response format setting.
- **`WithRequestFormats` on RouteHandle**: separate decode-format control distinct from encode.

---

## Round 4 (API Consistency Audit)

- **`FunctionKindScalar = ""`**: constant value corrected to empty string — scalar functions have `Kind==""` by design; `NewFunction`/`Compose` never write `Kind`. Contradicting godoc removed.
- **Stale `forge/options.go` godoc**: removed bullet mentioning non-existent `WithDescription`, `WithAuthor`, `WithApproval` as `FunctionOpt` functions. These do not exist at function level; use `FunctionMeta{...}` struct literal.
- **`events.Builder.servers` map→slice**: switched from `map[string]Server` to `[]namedServer` for deterministic AsyncAPI server insertion order. Same fix applied to `render/asyncapi/v2/document.go` and `render/asyncapi/v3/document.go`.
- **`events.Builder.AddServer` description fallback**: if `Server.Description` is empty, `AddServer` now falls back to using `name` as description (mirrors `rest.Builder.AddServer`).
- **`SSERouteHandle.Decode` godoc**: removed cross-package reference to `adapters/nethttp.RequestFromContext` — replaced with transport-agnostic wording.
- **README Field literals**: replaced verbose `Field[User,string]{...}` struct literals with `codex.RequiredField(...)` / `codex.OptionalField(...)` constructors.

---

## Round 5 (SSE Parity)

- **`WithCodec` on all 7 param types**: added `.WithCodec(c codex.Codec[string])` value-receiver to `PathParam`, `QueryParam`, `CookieParam`, `HeaderParam`, `ResponseHeaderParam`, `ResponseCookieParam` (rest) and `TopicParam` (events). Users no longer need `Codec: &myCodec`.
- **`ChannelHandle.WithSubscribeFormats` / `WithPublishFormats`**: asymmetric channels can now set different formats per direction. `SubscribeFormats`/`PublishFormats` exported fields on `ChannelHandle`. Adapters check these before falling back to `Formats`.

---

## Round 6 (SSE Header/Cookie/Path Support)

- Full header, cookie, and path-parameter support on SSE requests and responses via `SSERouteHandle`.
- `ResponseHeaderParam` / `ResponseCookieParam` on SSE routes validated correctly.
- Adapter `WithResponseHeaders` / `WithResponseCookies` context helpers work for SSE handlers.

---

## Round 7 (Empty Request Body / nil Codec)

- **Nil codec on RouteHandle**: `nethttp` and `chi` adapters handle `RouteHandle.Request == nil` without panicking — GET routes and other body-less requests no longer require a dummy empty codec.
- Examples updated to remove empty-body codec boilerplate.

---

## Round 8 (Example Correctness Pass)

- All 31 `examples/*/main.go` updated for current API (no stale `Codec: &`, no old builder calls).
- SSE examples (`examples/adapters-sse/main.go`) use `.WithCodec()` on `PathParam` and `ResponseHeaderParam`.
- PNG upload example (`examples/png-upload/main.go`) uses `.WithCodec()` on `PathParam` and `CookieParam`.

---

## Round 9 (Cross-layer Consistency)

All findings listed in the active plan.md under "Round 9" are implemented:

- F1: `FunctionKindScalar = ""` (constant + godoc) — done
- F2: Stale `forge/options.go` bullets removed — done
- F3+F9: `events.Builder.servers` map→slice, both AsyncAPI renderers — done
- F4: `events.Builder.AddServer` description fallback — done
- F5: `.WithCodec()` on all 7 param types — done
- F6: `SSERouteHandle.Decode` godoc cross-package reference removed — done
- F7: README Field literals → constructor style — done
- F8: `WithSubscribeFormats` / `WithPublishFormats` on `ChannelHandle` — done

---

## Round 10 (Governance + ValidateTopicVars Bug)

- **G1 — `ValidateTopicVars` missing `ok`-check**: missing key returned `TopicParamError{Value:""}` instead of `MissingTopicVarError`. Fixed with `val, ok := vars[p.Name]; if !ok { return MissingTopicVarError{Name: p.Name} }`.
- **G2 — `PathParam` / `TopicParam` godoc**: added sentence explaining why `Required` is absent (OpenAPI mandates path params always required; topic vars must always be present).
- **G3+G4 — `ChannelMeta` godoc**: condensed duplicate paragraph; `ChannelOpt` list now mentions all 4 `ChannelMeta` fields.
- **G5 — Codec field godoc**: normalized wording on `HeaderParam`, `ResponseHeaderParam`, `ResponseCookieParam` to consistent pattern.
- **G6 — `PipelineInfo` governance fields**: added `Author`, `ApprovedBy`, `ApprovedAt` to `PipelineInfo`; added `Registry.WithAuthor(string)` and `Registry.WithApproval(approvedBy, approvedAt string)` fluent methods; `render/pipeline/pipeline.go` `buildInfo()` emits them when set.
- **G7 — `rest.Builder.AddServer` godoc**: clarified that `name` is not stored beyond the description fallback (OpenAPI servers are a keyless ordered array).

---

## Round 11 (Path Param Observer, MQTT Format Priority, CookieOptions Ergonomics)

- **H1 — `reportPathErrors()` helper** (nethttp + chi): path param name was passed as `""` to `obs.RecordValidationError("path", ...)`. Added `reportPathErrors()` that `errors.As`-unpacks `rest.PathParamError` and passes `pe.Name`. Fixed 4 sites (Handler + SSEHandler in each adapter).
- **H2 — MQTT `SubscribeFormats`/`PublishFormats` priority**: `SubscribeHandler` and `Publish` in `adapters/mqtt/adapter.go` used only `handle.Formats`, skipping the R9-added `SubscribeFormats`/`PublishFormats` fields. Priority chain now: call-time → `SubscribeFormats`/`PublishFormats` → `Formats`.
- **H3 — `CookieOptions.WithCodec()`** (nethttp + chi): added `.WithCodec(c codex.Codec[string]) CookieOptions` value-receiver to both adapter packages, mirroring the `rest.*Param.WithCodec` pattern. Updated `examples/adapters-nethttp` and `examples/adapters-chi` to use `.WithCodec()`. Godoc updated to show fluent style.

---

## Round 12 (Godoc + Test Coverage for CookieOptions.WithCodec)

- **G1 — Stale `Codec: &` in `api/rest/builder.go` package godoc**: Package-level example used `PathParam{Name: "id", Codec: &uuidCodec}`; updated to `PathParam{Name: "id"}.WithCodec(uuidCodec)`.
- **G2 — `nethttp/cookie_test.go` used stale `Codec: &` pattern**: `TestSetCookie_Codec_valid/invalid` updated to use `.WithCodec()`; added `TestCookieOptions_WithCodec_setsCodec` and `TestCookieOptions_WithCodec_returnsDistinctCopy`.
- **G3 — No chi cookie tests**: Created `adapters/chi/cookie_test.go` with `TestChiSetCookie_defaults/Codec_valid/Codec_invalid` and `TestChiCookieOptions_WithCodec_setsCodec/returnsDistinctCopy`.

---

## Round 13 (CookieParam receiver rename + PathParam godoc)

- **G1 — `CookieParam.WithCodec` receiver inconsistency**: Renamed receiver from `c` to `cp` in both `applyRoute` and `WithCodec`; renamed codec arg from `cc` to `c` — now consistent with all other 9 `WithCodec` methods.
- **G2 — `PathParam.Codec` godoc incomplete**: Updated godoc to mention both `ValidatePathParams` and `BuildPath` — previously only mentioned `BuildPath`, hiding the adapter-side validation use.

---

## Round 14 (TopicParam.Codec godoc + PathParam duplicate godoc)

- **G1 — `TopicParam.Codec` godoc incomplete**: Updated godoc to mention both `ValidateTopicVars` and `BuildTopic` — previously only mentioned `BuildTopic`, hiding adapter-side validation use (parallel to R13 G2 fix for `PathParam.Codec`).
- **G2 — `PathParam` type-level godoc duplicated**: Removed the first of two overlapping introductory paragraphs — "PathParam describes…" appeared twice and `PathParam implements [RouteOpt]` appeared twice; kept the more specific version with the `{id}` example and `Required` note.
