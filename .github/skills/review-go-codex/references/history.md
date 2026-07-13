# go-codex Review History (R1–R41)

Do not re-report any of these findings. They have been implemented and tested.

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
