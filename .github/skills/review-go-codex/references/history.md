# go-codex Review History (R1–R33)

Do not re-report any of these findings. They have been implemented and tested.

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
