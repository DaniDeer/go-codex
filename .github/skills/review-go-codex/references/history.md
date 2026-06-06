# go-codex Review History (R1–R10)

Do not re-report any of these findings. They have been implemented and tested.

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
