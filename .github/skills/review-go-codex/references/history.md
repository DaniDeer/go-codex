# go-codex Review History (R1–R113)

Do not re-report any of these findings. They have been implemented and tested.

---

## Round 113 (`examples/go-edge-models` azure/iothub extraction — stale "sibling" wording + corrupted filename literals)

Focused audit of this session's `models/azure/iothub` extraction (moving `manifesttemplate`/`baseline`
out of `models/iotedge` into a new top-level `models/azure/iothub` package) — no core go-codex
library files changed this session (confirmed via `git status`), so this round scoped entirely to the
example tree per the established "review what changed" pattern (same as Round 111). Found the same
class of bug as Round 111 (stale sibling/parent/child relationship wording after a package move) plus
one new class: corrupted filename literals from a blanket `s/baseline\./iothub./g` rename.

- **G1–G12 [trivial] — stale "sibling" claims across `models/iotedge/*` and `models/azure/iothub`**:
  after moving `manifesttemplate`/`baseline` from `models/iotedge/{manifesttemplate,baseline}` into the
  new `models/azure/iothub` (a different parent directory, `models/azure` vs `models/iotedge`), several
  doc comments and READMEs still called the two packages "siblings" of each other (and of `models/iotedge`
  itself) — no longer true, since a sibling relationship requires the same immediate parent directory.
  Fixed in `deviceconfig.go`, `modulepatch/doc.go`, `modulepatch/fieldspatch.go`,
  `modulesummary/modulesummary.go`, `usecase/config.go`, `usecase/usecase.go`, `usecase/device.go`
  (verified correct, no change needed), `usecase/doc.go`, `azure/iothub/basedeployment.go`,
  `azure/iothub/doc.go` (×3 sites), `azure/iothub/keys.go`, `azure/iothub/layereddeployment.go`,
  `models/iotedge/doc.go`, `models/iotedge/README.md`, `models/azure/iothub/README.md`,
  `models/iotedge/usecase/README.md`, `main.go` (×2 sites), and `app/iotedge/usecase.go` — by dropping
  the inaccurate "sibling" qualifier (or moving it to the genuinely-sibling half of a mixed sentence).
  True siblings (both packages sharing the same immediate parent, e.g. `deviceconfig`↔`finaldeviceconfig`,
  both under `models/iotedge`) were left unchanged — verified each site's actual directory tree before
  editing.
- **G13 [small] — corrupted `"baseline.json"` filename literal became `"iothub.json"`**: a blanket
  `sed 's/\biothub\.json\b/.../'`-style rename during the mechanical package-move accidentally rewrote
  the literal on-disk filename string `"baseline.json"` to `"iothub.json"` in doc comments and one test
  assertion message across `usecase/config.go`, `usecase/doc.go`, `usecase/usecase.go`,
  `usecase/usecase_test.go`, and `usecase/device_test.go` — the actual `baselineFileName` CONSTANT was
  correct (fixed earlier in the same session), but the doc comments/test-failure message still claimed
  the wrong filename, which would mislead anyone reading the docs about what file to look for on disk.
  Fixed all 7 occurrences to read `"baseline.json"` consistently with the real constant.
- **G14 [small] — `usecase/doc.go` listed the SAME `models/azure/iothub` package link twice** as if it
  were two separate wire-format packages ("the global base deployment" and "deployment manifest") — a
  leftover artifact from when `baseline` and `manifesttemplate` were two distinct sibling packages before
  this session's merge into one `azure/iothub` package. Collapsed into one link covering both document
  shapes (`BaseDeployment` and `LayeredDeployment`).
- **Also fixed 4 stale `manifesttemplate.ModuleNameCodec`/`ModuleKeyPrefix` references** in
  `.github/instructions/go-codex.instructions.md` (missed during the session's mechanical rename pass
  since this file isn't Go source and wasn't touched by any `go build`/`go test` run) — updated to
  `iothub.ModuleNameCodec`/`iothub.ModuleKeyPrefix`.

Full verification: `gofmt -l .` clean, `go build ./...`, `go test ./...` (repo-wide, all packages pass,
including all `go-edge-models` subpackages), `just check` (staticcheck + gosec, 0 issues across 336
files/78,530 lines), `go run ./examples/go-edge-models` exits 0 unchanged (913-line demo, full
baseline+template+device-config layering). No exported API changed (doc-comment/godoc wording and
comment-only filename-literal fixes) — `.github/instructions/go-codex.instructions.md` updated for the
4 stale references found there.

---

## Round 112 (dotted-key/patch primitives — missing Schema on new Layer 1 codecs)

Audit of this session's new core-library surface: `codex.ApplyPatch`/`ApplyDottedPatch`/
`ApplyDottedPatchTo`/`IsEmptyPatch`/`NonEmptyPatch`/`EmptyPatchError`, `codex.DottedKeyCodec`/
`DottedPatchMapCodec`/`KeyVarConstraint`/`DottedKeyError`, `forge.Patch`/`PatchInput`, and the
`internal/templatematch` MQTT-to-dotted-key generalization (`matchWildcard`/`MatchDottedWildcard`).
All Layer 2/3 bundle constructors added this session (`events.Topic`/`rest.Path`/
`ports.FilePathTemplate`/`ports.DirPathTemplate`) were re-verified and found consistent — no
findings there.

- **G1 [bug] — `codex.DottedKeyCodec`/`codex.DottedPatchMapCodec` returned a `Codec` with a
  zero-value `Schema`**: every other codec constructor in the library sets `Schema` (e.g.
  `codex.String()` → `{Type:"string"}`, `PartialStruct` → `{Type:"object", ...}`), and
  `codex.Map[K,V]` propagates `keyCodec.Schema` directly into `PropertyNames` — so any
  `Map`/`Codec` built on these two new constructors (including
  `examples/go-edge-models`'s `deviceconfig.edgeAgentPatchCodec`) silently rendered an empty
  `propertyNames`/schema in generated JSON Schema/OpenAPI/AsyncAPI docs, with no error. Fixed
  by setting `Schema: schema.Schema{Type: "string"}` on `DottedKeyCodec` and
  `Schema: schema.Schema{Type: "object"}` on `DottedPatchMapCodec`, matching the `codex.String()`/
  `PartialStruct` conventions. Added `TestDottedKeyCodec_Schema_IsString`,
  `TestDottedPatchMapCodec_Schema_IsObject`, and a `Schema.PropertyNames` assertion in
  `TestDottedKeyCodec_ComposesWithMapForTypedValues` to close the coverage gap and prevent
  regression.

---

## Round 111 (`examples/go-edge-models` wire/derived split + directory nesting — stale relationship wording)

Focused audit of this session's `models/iotedge` refactor (extracting
`manifesttemplate`/`deviceconfig` wire packages, then nesting both under
`models/iotedge`) — no core go-codex library files changed since Round
110, so this round scoped to the example tree per the established
"review what changed this session" pattern. Found 4 stale
parent/child/sibling relationship claims in doc comments, no
functional bugs.

- **G1 [small] — `manifesttemplate/doc.go` called `models/iotedge` a
  "sibling"**: after nesting `manifest-template` under `models/iotedge`
  this session, `iotedge` became `manifesttemplate`'s PARENT, not a
  sibling. Fixed the wording.
- **G2 [small] — `manifesttemplate/doc.go` called `docker` a
  "sibling"**: `models/docker` (top-level) and `manifesttemplate`
  (nested two levels under `models/iotedge`) are no longer siblings
  post-nesting. Dropped the inaccurate "sibling" qualifier.
- **G3 [small] — `deviceconfig/doc.go` called `models/iotedge` a
  "sibling"**: same bug as G1 — `iotedge` is `deviceconfig`'s parent
  now, not a sibling. Fixed the wording.
- **G4 [trivial] — `models/iotedge/doc.go` called its own child
  `modulepatch` package a "sibling"**: `modulepatch` is nested at
  `models/iotedge/modulepatch` (a child of `iotedge`), not a sibling of
  it. Reworded to "child package".

Full verification: `gofmt -l .` clean, `go build ./...`, `go test ./...`
(repo-wide, all packages pass), `just check` (staticcheck + gosec, 0
issues), `go run ./examples/go-edge-models` exits 0 unchanged. No
exported API changed (doc-comment wording only) — no
`.github/instructions/go-codex.instructions.md` update needed.

---

## Round 110 (`ports.Dir`/`ports.File` glob path template segments — error-wrap bug + doc gaps)

Focused audit of the new filesystem-glob path template feature
(`*`/`?`/`[...]`/`**` via `internal/templatematch.MatchGlob`, `Dir.List`'s
glob-discovery mode, `ports.WithBaseDir`) added this session — found one
real bug and three doc/test-coverage gaps, no observer/example issues.

- **G1 [bug] — glob-discovery double-wrapped `DirPathParamError`**:
  `Dir.listGlobDiscovery`'s `filepath.WalkDir` error handling wrapped a
  `DirPathParamError` (from a captured named var failing its codec)
  inside `DirReadError` instead of returning it directly — `errors.As`
  still worked via `Unwrap()`, but the top-level type/message was wrong
  and contradicted `List`'s own godoc. `listRecursive` (same file)
  already special-cases this exact situation for `DirEntryParamError`;
  `listGlobDiscovery` didn't mirror it. Fixed by adding the same
  type-assertion unwrap, plus a regression test
  (`TestDir_List_Glob_NamedVarCodecFailure_ReturnsDirPathParamErrorDirectly`).
- **G2 [small] — `Dir.List`'s godoc omitted glob-mode opt behavior**:
  `DirOptions.CreateIfMissing`/`DryRun`/`Strict` are silently ignored
  when the template is glob-enabled (the glob branch returns before
  those opts are consulted), but this wasn't documented anywhere. Added
  a paragraph to `List`'s godoc.
- **G3 [trivial] — `Dir.Delete`'s glob rejection was undocumented and
  untested**: `Delete` on a glob-enabled template correctly returns
  `DirWildcardBuildError` (via its internal `BuildPath` call), but
  `Delete`'s own godoc didn't mention it and no test covered it. Added a
  paragraph to `Delete`'s godoc plus
  `TestDir_Delete_GlobTemplate_ReturnsDirWildcardBuildError`.
- **G4 [trivial] — dangling roadmap reference**: `listGlobDiscovery`'s
  doc comment referenced "the roadmap's accepted wildcard-first-segment
  risk," pointing at `docs/roadmap/path-template-wildcards.md`, deleted
  once shipped (same pattern Round 109's G1 already fixed once for a
  different doc). Rephrased to state the rationale inline.

Full verification: `gofmt -l .` clean, `go build ./...`, `go test ./...`
(repo-wide, all packages pass), `just check` (staticcheck + gosec, 0
issues), full example sweep (all exit 0 after clearing a disk-space
constraint — `go clean -cache` + removing stale `/tmp/go-build*` dirs).
No exported API signatures changed (godoc + internal error-handling
fix only) — no `.github/instructions/go-codex.instructions.md` update
needed.

---

## Round 109 (`ports.Dir`/`File.Delete`/`Dir.Delete` — stale doc comments after rapid iteration)

Focused audit of the new `ports.Dir`, `File.Delete`/`Dir.Delete`, and
symmetric `DryRun`/`Strict` surface added this session (nothing since
Round 108 covered it) — found 4 small doc-consistency gaps, no
functional bugs, no test-coverage gaps (30+ tests already existed and
passed), no observer/error-type/example issues.

- **G1 [small] — `ports/dir.go`'s top-of-file comment had a dead link**:
  it pointed at `docs/roadmap/directory-listing-port.md`, deleted earlier
  this session once shipped, and its "list a directory's entries"
  description didn't mention the now-present `Delete` capability. Fixed
  by pointing at `docs/features/ports.md`'s "Dir" subsection instead and
  broadening the description to cover both listing and deletion.
- **G2 [small] — `FileOptions`/`DirOptions` top doc comments were
  stale**: `FileOptions`'s comment listed only `Read`/`Write`/`Update`,
  omitting `Delete`/`Patch`/`PatchEncoded`/`WriteHandle`/`ReadMerged` (all
  of which take `FileOptions`); `DirOptions`'s comment listed only `List`,
  omitting `Delete`. Fixed by enumerating every method each Options
  struct configures, noting that not every field applies to every method.
- **G3 [small] — `Dir.List`'s own `Errors:` godoc list was incomplete**:
  `List` can return `DirAlreadyExistsError` (via `CreateIfMissing`+
  `Strict` on an already-existing directory) but its doc comment didn't
  mention it, making the error undiscoverable via godoc alone. Added it
  to the list.
- **G4 [small] — `stats.FileObserver`'s doc comment/example were
  stale**: the comment claimed "implementing this interface is purely
  additive — existing Observer implementations need not change," but
  `RecordFileDelete` was later added directly to this SAME interface as
  an intentional, acknowledged breaking change; the code example only
  showed `RecordFileRead`/`RecordFileWrite`; the comment mentioned only
  `[ports.File]`, omitting `[ports.Dir]` (which also type-asserts
  `FileObserver`). Fixed by documenting the one exception, recommending
  embedding `NoopObserver` for forward-compatibility, adding
  `RecordFileDelete` to the example, and mentioning `ports.Dir` alongside
  `ports.File`.

Full verification: `gofmt -l .` clean, `go build ./...`, `go test ./...`
(repo-wide, 48 packages, all pass), `just check` (staticcheck + gosec, 0
issues), full example sweep (all exit 0). No exported API changed
(doc-only fixes) — no `.github/instructions/go-codex.instructions.md`
update needed.

---

## Round 108 (`codex.PartialField`/`PartialStruct` — test coverage + gotcha docs)

Focused audit of the newly-shipped `codex.PartialField`/`codex.PartialStruct`
primitive and its flagship consumer (`examples/go-edge-models/models/iotedge/modulepatch`).
The core implementation itself was already correct (sealed interface,
`//lint:ignore U1000` markers, `ValidationErrors`/`TypeMismatchError` reuse
all matching `Struct`/`FieldCodec`'s established pattern) — found three
completeness gaps, all documentation/test-coverage, no functional bugs.

- **G1 [small] — `PartialStruct` missing Decode error-path tests**:
  `codex/partial_test.go` had no equivalent of `object_test.go`'s
  `TestStruct_DecodeNonObject`/`TestStruct_DecodeFieldWrongType`/
  `TestStruct_DecodeMultipleErrors`. Added
  `TestPartialStruct_Decode_NonObject_ReturnsTypeMismatchError`,
  `TestPartialStruct_Decode_FieldCodecError`,
  `TestPartialStruct_Decode_MultipleErrors`.
- **G2 [small] — nested-empty-patch footgun undocumented outside a test
  comment**: a non-nil-but-empty `&ModuleSettingsPatch{}` still encodes as
  present (`"settings": {}`) since presence is `!= nil`, not "has anything
  set inside" — this was only noted in a test comment, not in either doc
  surface or `ModuleSettingsPatch`'s own godoc. Added a "Nesting gotcha"
  note to `.github/instructions/go-codex.instructions.md` and
  `docs/concepts/codec.md`'s `PartialField`/`PartialStruct` sections, plus
  a godoc comment on `ModuleSettingsPatch` itself.
- **G3 [small] — Go generics named-type inference gotcha undocumented**:
  `codex.Map[K,V]`/`codex.SliceOf[T]` return a `Codec` over the plain
  underlying type, not any named type built on it, so `PartialField[T,F]`
  fails to infer `F` against a `*NamedType` field without an explicit
  `MapCodecSafe` identity-retype wrapper (discovered via
  `modulepatch.envVarsCodec` during the flagship refactor, but only
  documented as a code comment in that one file). Added a matching gotcha
  bullet to both doc surfaces referencing `modulepatch.envVarsCodec` as
  the reference pattern.

Full verification: `gofmt -l .` clean, `go build ./...`, `go test ./...`
(repo-wide, 48 packages, all pass), `just check` (staticcheck + gosec, 0
issues), full example sweep (all exit 0).

---

## Round 107 (`codex.HasCodec`'s own godoc gave impossible construction advice)

Focused audit of the only new core-library surface since Round 106 —
`codex.HasCodec[T]` (added earlier this session, plus its 8 adoptions in
`examples/go-edge-models`). Found one doc-comment defect; everything else
(interface design, 5 generic helpers, zero-value-call contract, test
coverage, `doc.go`/`docs/concepts/codec.md` sync) was already correct.

- **G1 [small] — `codex/hascodec.go`'s exported doc comment recommended
  an impossible pattern**: it said "Prefer defining Codec() as a
  package-level function (func Codec() codex.Codec[MyType])" — but
  `HasCodec[T]`'s method set requires `Codec()` to be a METHOD on `T`, so
  a bare package-level function can never satisfy it. This contradicted
  the interface declared 15 lines below in the same file, all 9 real
  implementations in the repo (`examples/construction` + all 8
  `go-edge-models` types, all value-receiver methods), and
  `docs/concepts/codec.md`'s own correct example. Fixed by rewording to
  "Prefer a value-receiver method returning a package-level codec
  variable... when the type has no per-instance state — the common case.
  A method is always required...; only the RECEIVER needs to genuinely
  close over instance state." Applied the identical wording fix to
  `.github/instructions/go-codex.instructions.md`'s matching bullet (which
  had a parenthetical that technically saved it from being wrong, but was
  confusingly worded the same way).

Full verification: `gofmt -l .` clean, `go build ./...`, `go test
./codex/...` (all 7 `HasCodec` tests pass unchanged — doc-only fix), `just
check` (staticcheck + gosec, 0 issues).

---

## Round 106 (`adapters/mcprest.DefaultErrorPatterns` missing pre-flight validation errors)

Focused audit of Round 105's new `adapters/mcprest` package (everything
else was already covered through Round 105) — found one completeness gap.

- **G1 [small] — `DefaultErrorPatterns()` didn't cover the pre-flight
  validation error family**: its own doc comment (and
  `.github/instructions/go-codex.instructions.md`/`docs/features/mcp.md`)
  claimed to map "every exported `adapters/nethttp`/`api/rest` CLIENT
  error type" into `RESTClientErrorPayload`, but only 5 of the 11 client
  error types `nethttp.Call`/`CallHandle` can return were actually
  covered — missing `rest.PathParamError`, `rest.MissingPathVarError`,
  `rest.QueryParamError`, `rest.CookieParamError`, `rest.HeaderParamError`
  (all pre-flight validation failures returned BEFORE any HTTP request is
  sent, per this skill's own "Structured Errors Guardrail"). Fixed by
  adding 5 more `apimcp.ErrorPattern` rules (`Kind` values `"path_param"`,
  `"missing_path_var"`, `"query_param"`, `"cookie_param"`,
  `"header_param"`), mirroring the existing 6 rules' style exactly, plus a
  matching unit test per new rule. `rest.InvalidPathParamError`
  (Register-time only, never returned by `Call`) and
  `nethttp.ErrorPatternResponse` (the route's own already-typed declared
  `rest.ErrorPattern` match — wrapping it generically would discard its
  decoded `Value`) were confirmed correctly excluded, not additional gaps.
  No doc changes were needed — the "every... client error type" claim in
  `go-codex.instructions.md`/`docs/features/mcp.md` is now accurate.

Two trivial, unrelated working-tree hygiene items were also found
(an empty untracked stub `examples/go-edge-models/docker/registry/mcptools.go`
and an unused, uncommitted `docker.Image` struct + commented-out codec in
`examples/go-edge-models/docker/{types,codecs}.go`) — left untouched since
they are uncommitted and could be the user's own in-progress work; only a
`gofmt -w` (whitespace-only, zero content change) was applied to the stub
file to unblock `just check`.

Full verification: `gofmt -l .` clean, `go build ./...`, `go test ./...`
(repo-wide, including the 5 new `adapters/mcprest` tests), all examples
exit 0, `just check` (staticcheck + gosec, 0 issues).

---

## Round 105 (New `adapters/mcprest` package — MCP Tool ↔ REST Client Bridge)

Implemented `docs/roadmap/mcp-rest-tool-bridge.md`'s design end-to-end — a
new standalone package bridging `adapters/nethttp`'s REST client to
`adapters/mcpgo`'s MCP tool handler shape, driven by the concrete
`examples/go-edge-models/docker/registry` use case.

- **`adapters/mcprest/bridge.go`**: `ToolHandler[Req, Resp](client,
  baseURL, handle, opts) mcpgo.HandlerFunc[Req, Resp]` — zero-boilerplate
  identity case, implemented as `MappedToolHandler` with identity mapper
  functions (mirrors `Call`/`CallHandle`'s convenience-vs-general
  relationship). `MappedToolHandler[ToolIn, ToolOut, Req, Resp](client,
  baseURL, handle, opts, toReq, fromResp) mcpgo.HandlerFunc[ToolIn,
  ToolOut]` — the general form, letting an LLM-facing tool's input/output
  shape differ from the REST route's wire request/response shape via
  fallible mapper functions.
- **`adapters/mcprest/errors.go`**: two new structured errors,
  `ToolRequestMapError{Method, Path, Err}` / `ToolResponseMapError{Method,
  Path, Err}` (both `slog.LogValuer`), wrapping a failing `toReq`/
  `fromResp` specifically — kept distinct from the underlying REST call's
  own typed errors, which continue to forward unchanged.
  `DefaultErrorPatterns() []apimcp.ToolOpt` — an opt-in helper reusing the
  EXISTING `apimcp.ErrorPattern` mechanism, mapping every `adapters/nethttp`/
  `api/rest` client error type plus the two new mapper errors into one
  structured `RESTClientErrorPayload{Kind, StatusCode, Body, Message}`.
- **Package placement**: new standalone package (not inside
  `adapters/mcpgo` or `adapters/nethttp`) — both those adapters are
  documented to import a narrow, fixed set of packages with no
  cross-adapter imports; `adapters/mcprest` is the only package importing
  both, keeping each transport-pure.
- **Credential model**: `nethttp.CallOptions`/`CredentialFunc` is FIXED
  once at construction — matches every existing client-adapter binding
  (`nethttp.CallAdapter`, `DrainCallAdapter`, mqtt5/zeromq equivalents).
  Evaluated and rejected a generic cross-adapter "per-session credential"
  mechanism (MCP's long-lived session is architecturally different from
  REST/MQTT/ZeroMQ's per-call model); documented a zero-new-API ctx/session
  recipe instead, using `mcp-go`'s own `server.ClientSessionFromContext`.
- **No new `stats.Observer` extension** — reuses `nethttp.CallOptions.Observer`
  and `mcpgo.Options.Observer` unchanged; the bridge is pure composition of
  two already-observed layers.
- **Composes with `ports.ToolPort.SetFunc` with zero extra plumbing** —
  confirmed identical function shape to `mcpgo.HandlerFunc`; proven with a
  dedicated test (`TestToolHandler_ComposesWithToolPortSetFunc`), not just a
  doc claim.
- **Tests**: 18 tests/examples in `adapters/mcprest` covering happy/error
  paths for both constructors, fixed-options application, mapper
  happy/error paths (with underlying-call errors still forwarding
  unchanged), `LogValue()` shape for both new error types,
  `DefaultErrorPatterns()` per-error-type mapping plus
  first-declared-rule-wins override, ports composition, and two runnable
  `Example_` functions.
- **Docs**: `.github/instructions/go-codex.instructions.md` (new
  `adapters/mcprest` row in both the package table and dependency table),
  `docs/features/mcp.md` (new "Bridging an existing REST client" section +
  2 new structured-error table rows), `docs/reference/project-structure.md`
  (new package listing).
- **Example**: `examples/go-edge-models/main.go` gained `runMCPBridgeDemo`
  — wraps `docker/registry`'s `GetTagsRoute` as an MCP tool both ways
  (`ToolHandler` and `MappedToolHandler` with a simplified
  `search_tags(image) -> {tags}` shape), demonstrates `DefaultErrorPatterns()`
  against a real simulated 404 (an explicit wildcard mux handler was added
  so an unknown image produces a genuine `UnexpectedStatusError` instead of
  falling through to the auth-ping catch-all), and binds the same handler
  function via `ports.ToolPort.SetFunc` using a small local fake
  `ports.ToolAdapter` to invoke it in-process.
- **Roadmap doc retired**: `docs/roadmap/mcp-rest-tool-bridge.md` deleted
  along with its `docs/roadmap/index.md` row and `zensical.toml` nav entry,
  per the "remove once shipped" convention.

Full verification: `gofmt -l .` clean, `go build ./...`, `go test ./...`
(repo-wide, including the new `adapters/mcprest` package), all examples
exit 0, `just check` (staticcheck + gosec, 0 issues).

---

## Round 104 (Async/refreshable credential caching for `adapters/nethttp`)

Implemented the `docs/roadmap/credential-caching.md` feature end-to-end
after finalizing three open design decisions with the user (hand-rolled
single-flight instead of `x/sync/singleflight`; a `CredentialFunc` type
ALIAS for zero breaking change; a notification-only
`CallOptions.OnCredentialRejected` hook instead of a hidden retry loop
inside `Call`).

- **`adapters/nethttp/client.go`**: added `CredentialFunc` type alias
  (`= func(ctx, []route.SecurityRequirement) (http.Header, error)`) and
  `CallOptions.OnCredentialRejected func()`, which fires when `Call`
  observes a 401 response AND a `CredentialFunc` was configured for that
  call — purely notificational, `Call` never retries automatically.
- **`adapters/nethttp/credential_cache.go`** (new): `NewCachingCredentialFunc(inner CredentialFunc, CachingCredentialFuncOptions{TTL, Observer}) (fn CredentialFunc, invalidate func())`
  — TTL-based caching wrapper; concurrent callers during a cache miss share
  the same in-flight call via a hand-rolled channel-based single-flight
  join (no thundering herd, no external dependency); `invalidate` purges
  the cached entry immediately, meant to be wired to
  `CallOptions.OnCredentialRejected`.
- **`stats.CredentialCacheObserver`** (new optional `Observer` extension):
  `RecordCredentialCacheHit(location string, duration time.Duration)` +
  `RecordCredentialCacheRefresh(location string, success bool, duration time.Duration)`
  — both include `duration` even on a hit, matching the established
  `CacheObserver` convention (a real inconsistency in the original roadmap
  draft, fixed during finalization). Implemented by `NoopObserver`,
  `LoggingObserver`, and `fanout` (type-assertion delegation, same pattern
  as `CacheObserver`); both doc-comment interface-enumeration lists
  (`LoggingObserver`, `NewFanout`) updated.
- **Tests**: 3 new tests in `adapters/nethttp/client_test.go`
  (`TestCall_OnCredentialRejected_FiresOn401`/`NotCalledWhenCredentialFuncNil`/`NotCalledOnNon401Status`);
  7 new tests in `adapters/nethttp/credential_cache_test.go` covering
  TTL caching, TTL expiry, concurrent-miss single-flight correctness,
  error-not-cached, invalidate-forces-refresh, observer hit/refresh
  events, and nil-Observer safety; compile-time `CredentialCacheObserver`
  assertions + fanout hit/refresh tests added to `stats/observer_test.go`.
- **Docs**: `.github/instructions/go-codex.instructions.md` (`adapters/nethttp`
  and `stats` rows), `docs/features/security.md` (new "Caching a
  CredentialFunc" subsection with the explicit retry-once caller pattern),
  `docs/reference/project-structure.md` (added `credential_cache.go`
  listing).
- **Example**: `examples/adapters-nethttp-client/main.go` gained a new
  "4b. Security: caching a CredentialFunc" section demonstrating
  cache-hit reuse across calls and the `OnCredentialRejected` +
  explicit-retry-once pattern against a simulated 401.
- **Roadmap**: `docs/roadmap/credential-caching.md` updated in place with
  the finalized design (status: implemented) rather than retired, per the
  precedent of keeping shipped design docs unless the user asks to delete
  them.

Full verification: `gofmt -l .` clean, `go build ./...`, `go test ./...`
(repo-wide, including `-count=1` and `-race` on `adapters/nethttp`), all
examples exit 0, `just check` (staticcheck + gosec, 0 issues).

## Round 103 (Consistency audit — `adapters/mqtt.PublishOptions` security godoc gap)

Ran the `review-go-codex` skill's full checklist, focused on the most
recently touched areas (Rounds 92–102: REST/events/reqreply security
parity + the connect-level credential feature). Checked cross-layer
naming/API parity, structured errors, observer pattern guardrails, test
coverage, example correctness, and `Codec` field godoc wording — all
clean, no issues found in any of those categories.

- **G1 [small] — `adapters/mqtt.PublishOptions` had no security
  documentation at all**: `SubscribeOptions.SecurityFunc` has an extensive
  doc comment explaining the (permanent, documented no-op) message-level
  credential check for MQTT 3.1.1; `PublishOptions` had NOTHING — no
  field, no comment, nothing explaining why a channel declaring
  `Publish.Security` is silently ignored. Fixed by adding a type-level doc
  comment to `PublishOptions` explaining MQTT 3.1.1 has no per-message
  metadata channel at all (unlike MQTT 5's User Properties), so
  message-level security has no viable mechanism for Publish, and pointing
  callers at `mqtt.NewSecuredClient` (the connect-level mechanism shipped
  in Round 101) as the intended alternative. Documentation-only — no
  functional change.

Full verification: `gofmt -l .` clean, `go build ./...`, `go test ./...`
(repo-wide), all examples exit 0, `just check` (staticcheck + gosec, 0
issues).

---

## Round 102 (Retired `docs/roadmap/mqtt-connect-credential-scheme.md` — fully shipped)

Reviewed the roadmap doc against Round 101's implementation, item by item:

- **API surface**: `ConnectSecurityScheme`/`WithCodec`, `SecuredClient`,
  `NewSecuredClient(client, scheme, username, password string, opts...)`,
  `SecuredClientOption`/`WithObserver`, `ConnectSecurityCredentialError` —
  confirmed identical in both `adapters/mqtt5/connect_security.go` and
  `adapters/mqtt/connect_security.go` to the roadmap doc's final code
  block.
- **Unit test plan**: all 10 planned tests present verbatim (by name) in
  both `connect_security_test.go` files — 20/20 shipped, all passing.
- **Example update**: confirmed `examples/adapters-mqtt5`'s "Demo 3b"
  wired into `main()`.
- **Out of scope items correctly NOT implemented**: no `sync.Map`/
  `sync.Once` (Design 1's rejected global memoization did not leak in);
  `Server.Security` remains godoc-only cross-reference, no code linkage.
- **Resolved design decisions section**: matches the shipped code exactly
  (username/password combined into `"username:password"`; `Server.Security`
  godoc-only; naming confirmed as `SecuredClient`/`NewSecuredClient`).

Since every item was fully shipped and verified, deleted
`docs/roadmap/mqtt-connect-credential-scheme.md` (same retirement
precedent as Rounds 97/99). Fixed the resulting dangling cross-references
in `docs/features/security.md` (removed the "see roadmap doc for full
design" pointer — the feature is now documented as shipped, not planned),
`docs/roadmap/index.md` (removed the table row), and `zensical.toml`
(removed the nav entry). Verified `gofmt -l .`/`go build ./...` unaffected
(docs-only change) and the 20 connect-security tests still pass.

---

## Round 101 (Implemented Connect-Level Credential Codec — `adapters/mqtt`/`adapters/mqtt5`)

Implemented the `mqtt-connect-credential-scheme.md` roadmap doc drafted in
Round 100, resolving its 3 open design decisions first:

- **Credential shape**: `NewSecuredClient(client, scheme, username, password string, opts ...SecuredClientOption)`
  — two separate parameters (matching the MQTT CONNECT packet's actual two
  wire fields), combined internally into a single `"username:password"`
  string before calling `scheme.Codec.Validate(...)` — mirrors the existing
  `examples/go-edge-models/docker/registry`'s `internal.BasicAuthCodec`
  convention rather than inventing a new one. This combined string is a
  VALIDATION-TIME-ONLY representation — never transmitted.
- **`Server.Security` cross-reference**: godoc-only recommendation (no code
  linkage) — `NewSecuredClient`'s doc comment recommends also declaring the
  same requirement via `Server.Security` for spec/runtime documentation
  parity; `Server.Security` itself remains untouched.
- **Naming**: `SecuredClient`/`NewSecuredClient` confirmed as-is.

Implementation (identical shape in both adapters, protocol-symmetric):

- **`adapters/mqtt5/connect_security.go`** (new file): `ConnectSecurityScheme{route.SecurityScheme + Codec}`
  + `WithCodec`; `SecuredClient{MQTTClient}` (struct-embeds `MQTTClient` —
  every method promoted automatically, zero manual delegation);
  `SecuredClientOption`/`WithObserver`; `NewSecuredClient` (validates ONCE,
  synchronously, at construction — no `sync.Once`, no lazy first-call
  race, no global state); `ConnectSecurityCredentialError{Scheme,Err}` —
  `Error()`/`Unwrap()`/`LogValue()` (`slog.LogValuer`). Compile-time
  assertion `var _ MQTTClient = (*SecuredClient)(nil)`.
- **`adapters/mqtt/connect_security.go`** (new file): identical shape,
  wrapping `pahomqtt.Client` (MQTT 3.1.1) instead — confirms the design's
  protocol symmetry (CONNECT username/password exists in both MQTT
  versions, unlike per-message User Properties which are MQTT5-only).
- **10 tests per adapter** (20 total, `connect_security_test.go` in each
  package): valid credential → wrapper returned; malformed credential →
  `ConnectSecurityCredentialError`, wrapper nil; nil `Codec` → no-op;
  malformed credential → underlying client NEVER touched (zero
  Publish/Subscribe calls on the mock); `*SecuredClient` satisfies
  `MQTTClient`/`pahomqtt.Client` (compile-time assertion);
  `TestSecuredClient_TransparentDelegation` — re-runs a representative
  Publish+Subscribe pair through the wrapper, proving identical behavior
  to the raw client; Observer records `RecordSecurityRejection("connect", scheme.Type)`
  on failure; nil Observer doesn't panic; `LogValue()`/`errors.As` on the
  new error type. Reused each package's EXISTING `mockClient` test helper
  (both packages already had one satisfying the full client interface) —
  no new mock infrastructure needed.
- **`examples/adapters-mqtt5`**: added "Demo 3b: Connect-level security" —
  wraps the mock broker with `NewSecuredClient`, contrasting the SAME
  concept against Demo 3's message-level security in one runnable place;
  happy path + malformed-credential-rejected-at-construction path. Found
  and fixed a demo bug: the initial malformed-credential example used
  `validate.NonEmptyString` on the combined `"username:password"` string,
  but `""+ ":" +""` is still non-empty (1 char) — switched to
  `validate.MinLen(3)` for a realistic rejection.
- **Docs (3 surfaces)**: `.github/instructions/go-codex.instructions.md`
  (both `adapters/mqtt`/`adapters/mqtt5` table rows); `docs/features/security.md`'s
  "Connection-level vs message-level security" section (written in Round
  100) updated with the concrete, now-shipped `NewSecuredClient` code
  example; `docs/reference/project-structure.md` file listings for both
  adapters' new `connect_security.go`.
- **Roadmap doc updated in place** (`docs/roadmap/mqtt-connect-credential-scheme.md`):
  Status → "Design complete — ready for implementation" before coding
  started; "Open design decisions" section replaced with "Resolved design
  decisions" recording the 3 choices above (not deleted — decision
  history preserved per skill convention). NOT yet retired this round —
  left in place as implementation reference; retirement (moving lasting
  rationale into `docs/features/security.md` and deleting the roadmap
  doc, per the Round 97/99 precedent) deferred to a future round once the
  feature has been in use for a while, rather than immediately after
  shipping.
- Full verification: `gofmt -l .` clean, `go build ./...`, `go test ./...`
  (repo-wide), `go run` on every example (all exit 0), `just check`
  (staticcheck + gosec, 0 issues).

---

## Round 100 (New roadmap doc: Connect-Level Credential Codec — `adapters/mqtt`/`adapters/mqtt5`)

Follow-up investigation after Round 99's message-level security parity
work — reviewed remaining open items and designed a NEW, not-yet-implemented
feature (Explore mode, docs only, no code this round):

- **Corrected another assumption**: `adapters/zeromq` ALSO implements
  reqreply (`Serve`/`Call`/`ServeRouter`/`CallDealer` in `adapter.go`, not a
  separate `reqreply.go` file) — confirmed it has ZERO security fields at
  all (not even a manual `SecurityFunc`-style hook), a real gap distinct
  from the MQTT5-only scoping decision. Left as a documented gap per the
  user's explicit choice — already captured in `docs/features/security.md`
  from Round 99.
- **Clarified `adapters/mqtt` (3.1.1) Subscribe/Publish asymmetry**: found
  `adapters/mqtt` already had a pre-existing (permanent no-op)
  `validateSecurityCredentials` scaffold on Subscribe (from before this
  session), but `Publish` has ZERO security wiring at all — not resolved
  this round (user redirected toward the connect-level design instead).
- **Confirmed `credential-caching.md` is NOT a connection-lifecycle
  concern** — it only caches `CredentialFunc`'s returned VALUE across
  stateless `nethttp.Call` invocations, never touches any transport
  connection. No principle needed weakening for the new feature either.
- **Designed a new feature**: connect-level (CONNECT username/password)
  credential FORMAT validation — protocol-symmetric across MQTT 3.1.1 +
  MQTT5 (confirmed via `go doc` that both `paho.mqtt.golang` and
  `paho.golang/paho` carry Username/Password on CONNECT), unlike the
  message-level model (MQTT5-only, since it depends on User Properties).
  Surfaced and resolved a real design tension: `WithSecurityScheme`'s
  "declare once → automatically applied" guarantee works because
  `Subscribe`/`Publish`/`Serve`/`Call` are go-codex-owned entry points that
  read the handle every call — connect-level validation has NO such hook
  (go-codex never calls `Connect()`). Considered two designs:
  - **Design 1 (rejected)**: global per-client-pointer memoization
    (`sync.Map`), lazily triggered on first `Subscribe`/`Publish`/`Serve`/
    `Call` — rejected for hidden global state, cache-eviction complexity,
    and requiring a new options field on 8 structs (4 entry points × 2
    adapters).
  - **Design 2 (chosen)**: an explicit `SecuredClient` wrapper —
    `NewSecuredClient(client, scheme, credential) (*SecuredClient, error)`
    validates ONCE, synchronously, at construction; the wrapper uses Go
    struct embedding to get every `MQTTClient`/`pahomqtt.Client` method
    promoted for free (zero manual delegation), so every existing
    `Subscribe`/`Publish`/`Serve`/`Call` call site works completely
    unchanged afterward. No global state, no lazy-race, no eviction
    questions — mirrors `docker/registry`'s already-proven memoized-closure
    pattern, just evaluated once per CONNECTION instead of once per
    top-level call.
- Wrote `docs/roadmap/mqtt-connect-credential-scheme.md` (Status: Design
  draft) with full API surface, structured error
  (`ConnectSecurityCredentialError`), Observer integration sketch, unit
  test plan, files-to-create list, and explicit open design decisions
  (opaque credential string vs. username+password; `Server.Security`
  cross-reference; naming). Added to `docs/roadmap/index.md`/`zensical.toml`.
- Added a new "Connection-level vs message-level security" section to
  `docs/features/security.md` explaining the two independent layers and
  forward-referencing the new roadmap doc.
- No code changes this round — verified `gofmt -l .` clean and
  `go build ./...` unaffected (docs-only).

---

## Round 99 (Security Scheme Parity Phases 1–3 — `api/events` + `api/reqreply`, MQTT5)

Implemented Phases 1–3 of the (now-retired) `events-reqreply-mcp-security-scheme.md`
roadmap doc, finalizing several design decisions during planning:

- **Corrected a stale roadmap claim**: "reqreply is mqtt5-only today" was
  wrong — `adapters/zeromq/adapter.go` also implements `Call`/`Serve`/
  `ServeRouter`/`CallDealer` against `reqreply.RouteHandle`. Scoped Phase 3
  to MQTT5 only anyway (ZeroMQ uses raw multipart frames with no header/
  property concept — a genuinely separate envelope-frame design problem),
  documented as an explicit future gap in both code comments and
  `docs/features/security.md`.
- **New parity gap found and closed**: REST's server side
  (`nethttp.Handler`) has always done a BUILT-IN codec-based credential
  extract+validate step (`validateSecurityCredentials`) BEFORE the
  optional custom `SecurityFunc`. MQTT5's `Subscribe` had NO such
  built-in step — `SecurityFunc` was entirely manual. Added the built-in
  check to BOTH events (Subscribe/Publish) and reqreply (Serve/Call) MQTT5
  paths, via a new shared `adapters/mqtt5/security.go`
  (`extractUserPropertyCredential`/`validateSecurityCredentials`, mirroring
  `adapters/nethttp`'s helpers exactly): HTTP/oauth2/oidc schemes read a
  fixed `"Authorization"` MQTT5 User Property (stripping `Bearer `/`Basic `
  prefixes, case-insensitive); apiKey schemes read the User Property named
  `scheme.Name`.
- **Phase 1** (`api/events`): migrated `Builder.AddSecurityScheme`
  (builder-level) to channel-level `events.WithSecurityScheme` — exact
  mirror of REST's Round-92 migration. Added `channelEntry.securitySchemes()`
  to the entry interface so `AsyncAPISpec()` aggregates schemes from every
  registered channel (last-registered-wins), instead of a removed
  builder-level map. `Channel.ClientHandle()` now ALSO populates
  `SecuritySchemes` (previously left empty) — client/server parity.
  Migrated 6 call sites (`api/events/builder_test.go` ×2, `ports/port_test.go`,
  `examples/api-events`, `examples/adapters-mqtt-security`,
  `adapters/mqtt/adapter_test.go` ×2).
- **Phase 2** (`adapters/mqtt5` events, MQTT5-only — same header/property
  constraint as reqreply applies to `adapters/mqtt`/ZeroMQ pub/sub too,
  confirmed with the user): added `PublishOptions.CredentialFunc func(ctx, reqs) ([]UserProperty, error)`.
  `Publish` resolves `secReqs` from `Descriptor.Publish.Security`
  (fallback `GlobalSecurity`), calls `CredentialFunc`, merges the returned
  properties, then validates their format BEFORE the actual
  `client.Publish` call — gated on `credProps != nil`, NOT
  `len(secReqs) > 0` alone (the exact Round-93 REST regression class,
  covered from day one here). New `events.SecurityCredentialError`/
  `events.SecurityError` types (`slog.LogValuer`, `errors.As`-navigable).
- **Phase 3** (`api/reqreply` + `adapters/mqtt5/reqreply`, MQTT5-only):
  full net-new security feature mirroring REST end-to-end —
  `reqreply.SecurityScheme`+`WithCodec`, `reqreply.WithSecurityScheme`
  (route-level, the ONLY declaration mechanism — no builder-level
  equivalent from the start), `RouteMeta.Security`, `RouteHandle.SecuritySchemes`/
  `GlobalSecurity` (populated by both `Route.Register` and
  `Route.ClientHandle`), `Builder.AddGlobalSecurity`. Since reqreply's
  `Builder` has no per-route entry list (unlike rest/events), schemes are
  accumulated directly into `Builder.securitySchemes` at `Route.Register`
  time and pushed into the AsyncAPI doc builder inside `AsyncAPISpec()` —
  a simpler storage approach than rest/events', appropriate given reqreply's
  existing `Builder` shape. `mqtt5.reqreply.CallOptions.CredentialFunc` /
  `ServeOptions.SecurityFunc` reuse the SAME `adapters/mqtt5/security.go`
  helpers as Phase 2. New `reqreply.SecurityCredentialError`/
  `reqreply.SecurityError` types. `binding.go`'s `CallAdapter`/`ServeAdapter`
  needed NO changes — they pass `CallOptions`/`ServeOptions` straight
  through, so the new fields flow automatically to `ports.Pattern` usage.
- Added 6 new tests to `api/events/builder_test.go` (Phase 1), 6 to
  `adapters/mqtt5/adapter_test.go` (Phase 2), 9 to
  `adapters/mqtt5/reqreply_test.go` + 4 to `api/reqreply/route_test.go`
  (Phase 3) — full REST-mirrored matrix (happy path, malformed credential,
  nil-CredentialFunc-not-an-error, `SecurityFunc` two-step ordering, spec
  aggregation, collision policy).
- Added a new "Demo 3: Security" section to `examples/adapters-mqtt5`
  demonstrating `reqreply.WithSecurityScheme` + `CallOptions.CredentialFunc`
  (happy path + client-side-rejected malformed credential) end-to-end.
  Updated `docs/features/security.md` (events section rewritten for
  `WithSecurityScheme`+`CredentialFunc`; new "Security for request-reply
  routes (reqreply)" section; explicit MCP-permanently-out-of-scope note)
  and `.github/instructions/go-codex.instructions.md`.
- **Retired `docs/roadmap/events-reqreply-mcp-security-scheme.md`**
  (Phases 1–3 shipped; Phase 4/MCP is a PERMANENT non-goal, not a deferred
  item — same retirement precedent as Round 97's
  `security-scheme-symmetry.md`). Removed from `docs/roadmap/index.md`/
  `zensical.toml`; fixed the dangling cross-reference in
  `docs/roadmap/credential-caching.md`; fixed a stale
  `Builder.AddSecurityScheme` mention in
  `.github/skills/review-go-codex/references/checklist.md`'s mcp.Builder
  parity table.
- Hit a `/tmp` disk-space exhaustion mid-round (`go build` failures across
  many `examples/*` packages) — resolved via `go clean -cache` (freed
  ~50GB of stale build cache); unrelated to the code changes themselves.
- Full verification: `gofmt -l .`, `go build ./...`, `go test ./...`
  (repo-wide), `go run` on every example (all exit 0), `just check`
  (staticcheck + gosec, 0 issues). `go vet ./...` surfaced one PRE-EXISTING,
  unrelated finding in `adapters/chi/adapter_test.go` (not touched this
  round).

---

## Round 98 (`docker/registry`: `RegistryCredentials` map — multi-registry credentials in one call)

Implemented the `RegistryCredentials` feature designed in the prior
planning round (session-only `plan.md`, no roadmap doc — this was a small,
additive feature scoped directly with the user, not a new subsystem):

- **`constants.go`**: added `ghcrRegistryHost`/`mcrRegistryHost` constants
  and a single `knownRegistryHosts []string` package-level slice built FROM
  the three named host constants (`dockerHubRegistryHost`/`ghcrRegistryHost`/
  `mcrRegistryHost`) — the SOLE source of truth `RegistryCredentialsCodec`'s
  key constraint is built from, per the user's explicit follow-up question
  ("can I combine the OneOf with the constants?") — no separately hand-typed
  string list exists anywhere to drift out of sync.
- **`types.go`**: added `RegistryCredentials map[string]Credentials`, doc
  comment explains the lookup-by-resolved-registry-host behavior and the
  `WithCredentials` escape hatch for registries outside the known set.
- **`codecs.go`**: added `CredentialsCodec` (struct codec) and
  `RegistryCredentialsCodec` (`codex.Map` keyed by
  `validate.OneOf(knownRegistryHosts...)`, valued by `CredentialsCodec`).
  **Mid-implementation correction** (user-reported real-world GHCR
  behavior): GHCR frequently authenticates with an empty/arbitrary username
  and the PAT carried entirely in `Password`. Confirmed with the user, then
  relaxed `CredentialsCodec.Username` from `RequiredField(NonEmptyString)`
  to `OptionalField` (unconstrained) — only `Password` remains required
  non-empty.
- **`auth.go`**: added `options.credentialsByRegistry RegistryCredentials`
  field, `WithCredentialsByRegistry(RegistryCredentials) Option`, and
  lookup logic inside `newAuthCredentialFunc` — `registryHost` was already
  a parameter (passed from `ParseImageRef`'s resolved `ref.Registry` by
  `GetTags`/`GetImageMetadata`), so the lookup needed no new plumbing.
  Precedence: a single `WithCredentials` value wins over
  `WithCredentialsByRegistry` when both are supplied to the same call (more
  specific override).
- **`auth_test.go`**: added 6 new tests —
  `TestWithCredentialsByRegistry_PicksCorrectEntryPerRegistry` (two mock
  registries, two map entries, no cross-contamination),
  `TestWithCredentialsByRegistry_NoMatchingEntry_FallsBackToAnonymous`,
  `TestWithCredentials_WinsOverWithCredentialsByRegistry`,
  `TestRegistryCredentialsCodec_RejectsUnknownRegistryHost`,
  `TestRegistryCredentialsCodec_RoundTrip` (all 3 known hosts, including an
  empty-Username GHCR-style entry), and
  `TestCredentialsCodec_RejectsEmptyPasswordButAllowsEmptyUsername`.
- Updated `doc.go`'s public-surface description (types.go/codecs.go bullet
  now lists `RegistryCredentials`) and added a paragraph describing the
  multi-registry option; updated `examples/go-edge-models/README.md`'s
  Quick Usage section with a `WithCredentialsByRegistry` example (including
  the GHCR empty-Username convention and MCR's no-auth zero value).
- Full verification: `gofmt -l .` clean, `go build ./...`, `go test
  ./examples/go-edge-models/docker/registry/...` (untagged, all new +
  existing tests pass), `go test -tags=integration ./examples/go-edge-models/docker/registry/...`
  (real Docker Hub/GHCR/MCR, unaffected — those tests only exercise
  `WithCredentials`), full repo `go test ./...`, `go run
  ./examples/go-edge-models` (demo output unchanged), `just check`
  (staticcheck + gosec, 0 issues).

---

## Round 97 (retired `docs/roadmap/security-scheme-symmetry.md` — all follow-ups fully accounted for)

Audited every outstanding item in the shipped `security-scheme-symmetry.md`
roadmap doc's "Out of scope (Phase 2)" and "Open design decisions" sections
and confirmed each is now resolved:

- **`api/events`/`api/reqreply`/`api/mcp` equivalents** → covered by the new
  `docs/roadmap/events-reqreply-mcp-security-scheme.md` (Round 94).
- **Async/refreshable credential caching** → covered by the new
  `docs/roadmap/credential-caching.md` (Round 94).
- **Enforcing "Security implies CredentialFunc required"** → permanently
  rejected during design (not deferred) — confirmed still documented in
  `docker/registry`'s own code comments and `docs/features/security.md`.
- **Migrating `docker/registry` to `WithSecurityScheme`** → actually
  IMPLEMENTED (Rounds 94–96), not just planned — confirmed via
  `grep`/`go doc` that `auth.go` uses `rest.WithSecurityScheme` for both
  Bearer and Basic auth.
- **Collision policy (last-registered-wins) for `OpenAPISpec()`** →
  resolved and shipped in code (`api/rest/builder.go`'s own godoc states
  it).
- **Whether `WithSecurityScheme` should imply `RouteMeta.Security`** →
  resolved (kept separate) via the "Why two mechanisms" design Q&A,
  documented in `docs/features/security.md`'s "Security requirement
  shapes" section.

With every follow-up item accounted for elsewhere, deleted
`docs/roadmap/security-scheme-symmetry.md` (previously kept as design
history) rather than leaving a redundant, now-fully-superseded doc around.
Fixed the resulting cross-references in `.github/skills/review-go-codex/references/checklist.md`,
`docs/features/security.md`, `docs/roadmap/events-reqreply-mcp-security-scheme.md`,
and `docs/roadmap/credential-caching.md` to point at `docs/features/security.md`
(the durable, current usage doc) instead of the deleted file — verified no
dead markdown links remain and `go build ./...` is unaffected (doc-only
change).

---

## Round 96 (`docker/registry`: radical public-API-surface reduction — auth flow fully internal)

Follow-up to Round 95's `GetTokenRoute` move: the user asked to reduce the
package's ENTIRE public surface to exactly three things — routes, the two
client functions, and domain structs/codecs — removing every other
exported symbol that exists only to support "drive the auth flow yourself"
use cases nobody actually needs yet.

- **Unexported** (moved from exported to lowercase, all within the SAME
  package so no behavior change): `NewAuthCredentialFunc` →
  `newAuthCredentialFunc`, the `CredentialFunc` type alias →
  `credentialFunc`, `FormatChallenge`/`FormatDockerScope`/
  `FormatBearerToken`/`FormatBasicAuth` → `formatChallenge`/
  `formatDockerScope`/`formatBearerToken`/`formatBasicAuth`. A regex-based
  rename initially, and dangerously, also matched the UNRELATED
  `nethttp.CallOptions.CredentialFunc` field name (a different symbol in
  go-codex core) in both comments and actual code
  (`nethttp.CallOptions{credentialFunc: ...}` — would not have compiled);
  caught immediately via `go build` and fixed by restoring every
  `nethttp.CallOptions.CredentialFunc`/`.CredentialFunc` reference to its
  correct capitalization.
- **Kept exported** (deliberately, with reasoning re-verified): `Option`/
  `WithCredentials`/`Credentials` (needed to call `GetTags`/
  `GetImageMetadata` with private-repo credentials — part of the client
  functions' own public API, not auth-internal plumbing);
  `RegistryAuthChallengeError`/`RegistryAuthError` (can legitimately
  surface FROM `GetTags`/`GetImageMetadata`'s own error returns, so a
  caller doing `errors.As` needs to name them); `ParseImageRef`/
  `FormatImageRef`/`FormatPlatformSelector` (domain-struct helpers —
  `ImageRef`/platform-selector ↔ string — squarely "structs and codecs for
  domain models," not auth plumbing).
- **Updated `examples/go-edge-models/main.go`'s demo**, which used the
  now-unexported `registry.FormatChallenge`/`FormatDockerScope`/
  `FormatBearerToken` to build its mock registry+auth server — replaced
  with plain string formatting (`fmt.Sprintf`), since that mock plays the
  role of a REAL third-party registry SERVER (which `docker/registry`
  itself never implements — it's a client-only package), not part of the
  reusable client logic being demonstrated.
- Rewrote `auth.go`'s and `doc.go`'s file-level doc comments to state the
  reduced public surface as an explicit, permanent design invariant (not
  just describe what currently happens to be exported) — including
  guidance that a future need (e.g. an MCP tool wrapper) should add a NEW
  exported function to `client.go`, not re-expose the auth-flow internals.
  Fixed several now-stale cross-references left over from the
  `GetTokenRoute`/`bearerAuthScheme` moves (Round 95) that still pointed
  at `routes.go` for things now living in `auth.go`.
- Verified public surface via `go doc .`: exactly `PingRoute`/
  `GetTagsRoute`/`GetManifestRoute` (routes), `GetTags`/`GetImageMetadata`
  (client functions), `Option`/`WithCredentials` (the one functional
  option), domain structs/codecs, and the 5 error types — zero auth-flow
  internals visible.
- Verified: `gofmt`, `go build ./...`, `go test ./...` (all clean),
  `go test -tags=integration` against real Docker Hub/GHCR/MCR, `go run
  ./examples/go-edge-models` (demo output unchanged), `just check`
  (0 issues).

---

## Round 95 (`docker/registry`: move `GetTokenRoute` out of routes.go's externally-facing contract; documented `route.SecurityRequirement` AND/OR shapes)

Two follow-ups from design Q&A about the security-scheme-symmetry feature:

- **Documented `route.SecurityRequirement`'s combination shapes**: added a
  new "Security requirement shapes" section to `docs/features/security.md`
  covering single-scheme, OR (multiple slice elements), AND (one map,
  multiple keys), mixed OR-of-ANDs, explicit opt-out (empty slice), inherit
  global (nil), and scheme-reuse-with-different-scopes — with a closing
  explanation of why `WithSecurityScheme` (1:1 "what does scheme X look
  like") and `RouteMeta.Security` (N:M "which combination does this
  operation need") stay separate mechanisms rather than being folded into
  one (a `RequireSecurityScheme` convenience sugar was considered and
  explicitly rejected — it would only cover the single-scheme case, still
  needing the two-call pattern for AND-combinations, not worth the added
  decision-fork).
- **Fixed a real inconsistency in `examples/go-edge-models/docker/registry`**:
  `GetTokenRoute` (the auth-realm token-exchange endpoint, used exclusively
  by `auth.go`'s `authenticate()`) was declared in `routes.go` alongside
  `PingRoute`/`GetTagsRoute`/`GetManifestRoute`, and `doc.go` explicitly
  listed it as part of "the PRIMARY contract" a downstream caller would
  call `.ClientHandle()` on directly. This was misleading — `GetTokenRoute`
  needs a realm URL/service/scope that only come from parsing a
  WWW-Authenticate challenge (`authenticate()`'s own job), so it has no
  legitimate standalone caller, unlike the three routes it was grouped
  with. Moved `GetTokenRoute` (plus its exclusively-related
  `basicAuthScheme`/`basicAuthSecurity` declarations) from `routes.go` into
  `auth.go`, alongside its only caller. Updated `routes.go`'s and
  `doc.go`'s file-level doc comments to accurately describe
  `routes.go` as containing only genuinely externally-facing routes.
  Zero behavior change — pure code-organization fix, all existing tests
  pass unchanged (including the private-repo Basic-auth credential tests
  and the full integration suite against real Docker Hub/GHCR/MCR).
- Verified: `gofmt`, `go build ./...`, `go test ./...` (all clean),
  `go test -tags=integration` against real registries, `go run
  ./examples/go-edge-models` (demo unchanged), `just check` (0 issues).

---

## Round 94 (`docker/registry`: unify Basic-auth with Bearer's `WithSecurityScheme` pattern; two new roadmap docs)

Three separate follow-ups requested after Round 93's Phase 2 review:

- **`examples/go-edge-models/docker/registry` — full migration to
  `WithSecurityScheme` (implemented):** `GetTokenRoute` (the token-exchange
  call, used only when private-repo `Credentials` are supplied) previously
  injected Basic-auth via `nethttp.CallOptions.ExtraHeaders`, entirely
  bypassing the `SecurityScheme`/`CredentialFunc` mechanism Bearer
  credentials already used. Added `routes.go`'s `basicAuthScheme`
  (mirrors `bearerAuthScheme`: `route.BasicScheme()` + a non-empty-string
  format `Codec`) and `basicAuthSecurity`, attached to `GetTokenRoute` via
  `rest.WithSecurityScheme` + `RouteMeta.Security` (declared
  unconditionally — safe, since a nil/no-op `CredentialFunc` on a secured
  route stays a non-error per the Round 92/93 contract, so anonymous
  token-exchange calls are completely unaffected). `auth.go`'s
  `authenticate()` now builds a `tokenOpts.CredentialFunc` via
  `FormatBasicAuth` instead of setting `ExtraHeaders` directly — both
  Bearer and Basic credentials now flow through the IDENTICAL declarative
  mechanism, eliminating the package's one remaining manual header
  injection site. Deliberately did NOT remove
  `internal.BearerTokenCodec`/`BasicAuthCodec`/`Format*` helpers — those
  construct header VALUES (a different job from `SecurityScheme.Codec`,
  which only validates an already-constructed value) and removing them
  would contradict the package's established "everything flows through a
  codec" philosophy; the actual "custom code to reduce" was the
  `ExtraHeaders` bypass, not the codecs themselves. All existing tests
  (including `TestAuthenticate_WithCredentials_SendsBasicAuthOnTokenExchange`/
  `TestAuthenticate_NoCredentials_SendsNoAuthorizationOnTokenExchange`) pass
  UNCHANGED — same external behavior, different internal wiring. Verified
  against real Docker Hub/GHCR/MCR via `-tags=integration`.
- **New roadmap doc: `docs/roadmap/events-reqreply-mcp-security-scheme.md`**
  (Explore mode, no code changes). Investigated current state per layer:
  `api/events` has builder-level `AddSecurityScheme`+`Codec` (server/
  subscribe-side only, mirrors REST's OLD asymmetric design) but its
  pub/sub `PublishOptions` (mqtt/mqtt5/zeromq) has NO
  `CredentialFunc`-equivalent field at all — no per-publish credential
  injection point exists today. `api/reqreply` has ZERO security concepts
  (no `Security` field, no `SecurityScheme` type, no `CredentialFunc`/
  `SecurityFunc` anywhere) — a full net-new feature, not a migration.
  `api/mcp` has ZERO, and is EXPLICITLY documented as intentional ("MCP
  security is handled separately and not part of `api/mcp`"). Doc phases
  the work: Phase 1 (events channel-level `WithSecurityScheme`, small) →
  Phase 2 (net-new publish-side `CredentialFunc` for mqtt/mqtt5/zeromq,
  flags an open design question — MQTT 3.1.1/ZeroMQ have no per-message
  header concept to inject a credential into, unlike MQTT 5's User
  Properties) → Phase 3 (reqreply full net-new security feature) → Phase 4
  (mcp, explicitly flagged BLOCKED pending maintainer sign-off, since it
  reverses a deliberate documented design decision, not a routine parity
  gap).
- **New roadmap doc: `docs/roadmap/credential-caching.md`** (Explore mode,
  no code changes). Sketches `nethttp.NewCachingCredentialFunc(inner,
  opts)` — a generic, protocol-agnostic wrapper adding TTL-based caching,
  single-flight concurrency safety, and an optional retry-on-401 policy to
  any `CredentialFunc`. Flags the trickiest open design question:
  `CredentialFunc` runs BEFORE the network call and has no visibility into
  the response, so "retry on 401" needs either a bigger wrapper API
  (wrapping the whole `Call` invocation) or a new `nethttp.Call`-level hook
  (`OnCredentialRejected`) — deferred to implementation time. Also sketches
  a new `stats.CredentialCacheObserver` extension
  (`RecordCredentialCacheHit`/`RecordCredentialCacheRefresh`). Explicitly
  notes `docker/registry`'s own `sync.Once`-based memoization is CORRECT
  for its actual usage pattern and does NOT need migrating to this wrapper.
- Both new roadmap docs added to `docs/roadmap/index.md`'s table and
  `zensical.toml`'s roadmap nav.
- Verified: `gofmt`, `go build ./...`, `go test ./...` (all clean),
  `go test -tags=integration ./examples/go-edge-models/docker/registry/...`
  against real Docker Hub/GHCR/MCR, `go run ./examples/go-edge-models`
  (unchanged demo output), and `just check` (0 issues).

---

## Round 93 (Phase 2 review of Round 92: real bug found and fixed in the client-side credential check + `docker/registry` migration)

Reviewed `docs/roadmap/security-scheme-symmetry.md`'s "Out of scope (Phase 2)"
list after Round 92 shipped. Investigated all four items concretely:

- **`api/events`/`api/reqreply`/`api/mcp` equivalents**: confirmed NOT
  actionable — checked `adapters/mqtt`/`mqtt5` (`SecurityFunc` is
  subscribe-side/server-only, no publish-side credential hook exists at
  all), `adapters/mqtt5/reqreply.Call` (its `CallOptions` has no
  `CredentialFunc`-equivalent field), and `api/mcp`/`adapters/mcpgo` (no
  client dial-out concept). None of these transports have a client-side
  credential hook to make symmetric with — left deferred, no action.
- **Async/refreshable credential caching (retry-on-401)**: confirmed
  genuinely a separate, larger feature (cache invalidation, retry
  semantics) — would need its own roadmap doc if ever wanted. Left
  deferred.
- **Enforcing "Security ⇒ CredentialFunc required"**: stays rejected, not
  phase 2 material — this was a deliberate design decision from Round 92,
  not a deferred one.
- **Migrating `docker/registry` to adopt `WithSecurityScheme`**: implemented.
  Added `bearerAuthScheme` (a non-empty-string format `Codec`) to
  `docker/registry/routes.go`, attached via `rest.WithSecurityScheme` to
  `GetTagsRoute`/`GetManifestRoute` — no `Builder` needed (works through
  `.ClientHandle()` alone). This surfaced a REAL, previously-undetected bug
  in Round 92's own client-side check.

**Bug found and fixed (`adapters/nethttp/client.go`):** the client-side
credential-format check introduced in Round 92 was gated on
`len(secReqs) > 0` alone. Running the `docker/registry` integration suite
against MCR (a registry that requires NO authentication — a 200 direct on
the base ping) failed: `NewAuthCredentialFunc` correctly detects "no auth
needed" and returns `(nil, nil)` from its `CredentialFunc`, but the
resulting ABSENT `Authorization` header still extracted as `""`, which the
newly-attached `NonEmptyString` `Codec` rejected — wrongly treating "no
credential needed" the same as "malformed credential" and breaking a
previously-passing, real-registry integration test. Root cause: the check
didn't distinguish "CredentialFunc deliberately supplied nothing" from "a
credential was supplied but is malformed." Fixed by gating the check on
`len(secReqs) > 0 && credHeaders != nil` instead — the codec check now
only fires when the credential mechanism actually returned something,
preserving the pre-existing "nil/no-op `CredentialFunc` on a secured route
is not an error" contract exactly. Added
`TestCall_CredentialFunc_ReturnsNilHeader_SkipsValidation` as a permanent
regression guard.

Also updated `examples/adapters-nethttp-client/main.go`'s security demo,
which Round 92 had (incorrectly, as it turned out) reframed around the
buggy behavior: restored the "no CredentialFunc → server 401" case to its
original, now-correct-again form, and added a NEW, separate case
demonstrating the client-side codec rejection with an EXPLICITLY malformed
credential (`CredentialFunc` returns `"Bearer "` — empty after prefix
strip) — the demo now correctly shows all four distinct security
behaviors: happy path, no-`CredentialFunc` (server 401), malformed
credential (local rejection), and `CredentialFunc` error (local abort).

Verified: `gofmt`, `go build ./...`, `go test ./...` (all packages, plus
`go test -tags=integration ./examples/go-edge-models/docker/registry/...`
against real Docker Hub/GHCR/MCR — MCR specifically now passes again),
every example run (all exit 0 except the known long-running
`sensor-service`), and `just check` (0 issues).

---

## Round 92 (`api/rest`: route-only security scheme declaration + symmetric client-side credential validation)

Implemented `docs/roadmap/security-scheme-symmetry.md` Phase 1 — a
BREAKING change (sole consumer of go-codex, confirmed acceptable):

- **Removed `Builder.AddSecurityScheme`/`Builder.securitySchemes` entirely**
  from `api/rest`. New route-level `rest.WithSecurityScheme(name, scheme) RouteOpt`
  is now the ONLY way to declare a security scheme+codec — stored in a new
  `routeBuilder.securitySchemes` field, consumed identically by
  `Route.Register` (server) and `Route.ClientHandle` (client, previously
  never populated `SecuritySchemes` at all).
- `Builder.OpenAPISpec()` now aggregates `components.securitySchemes` from
  every registered route's own declaration (new `securitySchemes()` method
  on the `routeEntry` interface, implemented by `typedRouteEntry`/
  `typedSSEEntry`) instead of a builder-level map — last-registered-wins
  on a name collision, documented.
- `adapters/nethttp.Call`/`CallHandle`: new symmetric client-side check —
  after all headers are merged into the outgoing request, reuses the
  EXISTING `validateSecurityCredentials`/`extractCredential`/`firstScheme`
  helpers (already generic over `*http.Request`, zero duplication) to
  validate a `CredentialFunc`'s returned credential format before sending.
  Reuses `rest.SecurityCredentialError` and
  `stats.SecurityObserver.RecordSecurityRejection` verbatim — no new error
  or observer types. Deliberately does NOT require `CredentialFunc` to be
  non-nil on a secured route (preserves the existing, documented,
  demonstrated "unauthenticated request to a secured route → server 401"
  convention, symmetric with server-side `SecurityFunc`).
- Migrated every in-repo `Builder.AddSecurityScheme` call site (breaking
  change) to route-level `WithSecurityScheme`: `api/rest/builder_test.go`
  (3 tests), `adapters/nethttp/adapter_test.go` + `client_test.go` (6
  tests), `adapters/chi/adapter_test.go` (5 tests),
  `examples/adapters-nethttp-security`, `examples/adapters-chi-security`,
  `examples/adapters-nethttp-client` (scheme moved into the shared
  `contract` package so client+server both get it from one declaration).
  `api/events`' OWN independent `AddSecurityScheme` (different package,
  different mechanism) is UNCHANGED — untouched by this round.
- Fixed `examples/adapters-nethttp-client`'s "no CredentialFunc → server
  401" demo, which the new client-side check legitimately changed the
  mechanics of (the route's Codec now catches the missing credential
  LOCALLY before any request, since `docker/registry`-style codec
  guarantees didn't apply there) — updated the demo's comments/error
  handling to `errors.As(err, &rest.SecurityCredentialError{})` and
  reframed it as demonstrating the NEW symmetric behavior, rather than
  silently leaving a dead `errors.As(&nethttp.UnexpectedStatusError{})`
  branch that would never match again.
- Added 8 new tests total across `api/rest/builder_test.go` (3:
  `TestWithSecurityScheme_ClientHandle_PopulatesSecuritySchemes`,
  `TestWithSecurityScheme_Register_PopulatesSecuritySchemes`,
  `TestOpenAPISpec_AggregatesSecuritySchemesFromRoutes`) and
  `adapters/nethttp/client_test.go` (5:
  `TestCall_CredentialFunc_ValidFormat_Passes`,
  `TestCall_CredentialFunc_MalformedFormat_ReturnsSecurityCredentialError`,
  `TestCall_CredentialFunc_MalformedFormat_RecordsSecurityRejection`,
  `TestCall_NoSecurityScheme_NoValidation`,
  `TestCall_NoCredentialFunc_SecuredRoute_StillNotAnError`).
- Updated `.github/instructions/go-codex.instructions.md`, `docs/features/security.md`,
  `docs/features/openapi.md`, `docs/features/ports.md`, `docs/guides/ports.md`,
  `docs/guides/http-server.md`, `docs/reference/project-structure.md`, and this
  skill's own `checklist.md` (corrected the now-stale
  `rest.Builder`/`events.Builder` `AddSecurityScheme` parity row — this is
  now an INTENTIONAL divergence, do not re-flag it in a future review).
- `docs/roadmap/security-scheme-symmetry.md` marked SHIPPED (kept as design
  history); removed from `docs/roadmap/index.md`'s active table and
  `zensical.toml`'s roadmap nav.
- Verified: `gofmt`, `go build ./...`, `go test ./...` (one pre-existing,
  unrelated flaky timing test in `stream` confirmed to pass in isolation
  and on re-run — not caused by this change), every example run (all exit
  0 except `sensor-service`, a long-running server example that always
  times out under a bounded `timeout` wrapper — unrelated, no
  `AddSecurityScheme`/`WithSecurityScheme` usage there), and `just check`
  (staticcheck + gosec, 0 issues).

---

## Round 91 (`examples/go-edge-models/docker/registry`: rewrite `authenticate` as an exported, memoizing `NewAuthCredentialFunc`)

Rewrote the registry auth flow so it runs through
`nethttp.CallOptions.CredentialFunc` — the mechanism `GetTagsRoute`/
`GetManifestRoute` already declared via `RouteMeta.Security`
(`bearerAuthSecurity`) — instead of being pre-fetched manually before
every secured call. This is groundwork for a later phase that wraps this
package's routes as MCP tools without going through `GetTags`/
`GetImageMetadata` at all.

- **`auth.go`**: added `type CredentialFunc = func(ctx context.Context,
  reqs []route.SecurityRequirement) (http.Header, error)` (named alias for
  readability) and exported `NewAuthCredentialFunc(httpClient,
  registryHost, repository, opts ...Option) CredentialFunc`. It calls the
  existing (unchanged) `authenticate()` LAZILY, on first invocation by
  `nethttp.Call`/`CallHandle`, and memoizes the token/error via
  `sync.Once` for the closure's lifetime — so reusing the SAME
  `CredentialFunc` value across multiple secured calls (e.g.
  `GetImageMetadata`'s two `GetManifestRoute` fetches while resolving a
  manifest list) performs the Ping + WWW-Authenticate-challenge +
  token-exchange dance only ONCE. Removed `bearerCredentialFunc` (its
  header-formatting logic is now inlined in the new closure).
- **`client.go`**: `GetTags`/`GetImageMetadata` no longer call
  `authenticate()` up front — they build one `NewAuthCredentialFunc(...)`
  value and pass it straight through as `CallOptions.CredentialFunc`.
  `fetchManifest`'s trailing `token string` parameter became
  `credFn CredentialFunc`; `GetImageMetadata` builds ONE `credFn` and
  reuses it across both `fetchManifest` calls, which is what makes the
  memoization take effect for the manifest-list-resolution path.
- **`auth_test.go`**: added `TestNewAuthCredentialFunc_MemoizesAcrossMultipleCalls`
  (asserts the mock registry's Ping/token endpoints are each hit exactly
  once across two `CredentialFunc` invocations), `TestNewAuthCredentialFunc_NoAuthNeeded_ReturnsNilHeader`,
  and `TestNewAuthCredentialFunc_PropagatesAuthError` (asserts
  `RegistryAuthError` surfaces via `errors.As`, and stays consistent on a
  second invocation). Existing `TestAuthenticate_*` tests (calling
  `authenticate()` directly) are untouched and still pass.
- **Design decision, not implemented**: memoization stays in this example
  package (`docker/registry`), not in `adapters/nethttp` — the core
  adapter has no natural cache key or token-expiry/invalidation story at
  the `CredentialFunc` boundary; baking caching into `CallOptions` would
  be scope creep against go-codex's no-hidden-state design. A `sync.Once`
  closure scoped to `(registryHost, repository, credentials)` is the
  correct, precise place for it.
- **Explicitly out of scope**: wrapping `docker/registry`'s routes as MCP
  tools (planned as a separate, later round); registering an actual
  `rest.SecurityScheme` via a `rest.Builder` (this package has no
  spec-building `Builder` at all — `RouteMeta.Security` is already
  sufficient to trigger client-side `CredentialFunc` invocation).
- Verified: `gofmt`, `go build`, `go vet`, `go test` (untagged: 8 tests
  pass) and `go test -tags=integration` (all integration + unit tests
  pass against real Docker Hub/GHCR/MCR registries, including the
  manifest-list double-fetch path for `alpine`/`nodered/node-red`), full
  repo `go test ./...`, `go run ./examples/go-edge-models` (demo output
  unchanged), and `just check` (staticcheck+gosec, 0 issues) — all clean.

---

## Round 90 (`examples/go-edge-models/docker/registry`: split `client.go`/`auth.go`/`constants.go`/`errors.go`; consolidated tests into `auth_test.go`)

Pure code-organization refactor of `docker/registry` — no behavior
changes, no public API changes. Split the previously monolithic
`client.go` into four single-purpose files so a reader can find any
symbol by its category, and consolidated the auth-related tests
previously scattered across `registry_test.go` and `credentials_test.go`
into one `auth_test.go`:

- **`auth.go`** (new): all authentication logic — `parseChallenge`,
  `authenticate`, `bearerCredentialFunc`, the `FormatChallenge`/
  `FormatDockerScope`/`FormatBearerToken`/`FormatBasicAuth` helpers, and
  the `Option`/`WithCredentials` functional-option pair for private-repo
  Basic auth.
- **`client.go`** (trimmed): general client wiring only — image-reference
  parsing (`ParseImageRef`/`FormatImageRef`/`FormatPlatformSelector`/
  `splitDockerDomain`), manifest-list-to-single-platform resolution
  (`fetchManifest`/`platformMatches`), and the two public entry points
  (`GetTags`/`GetImageMetadata`).
- **`constants.go`** (new): the Docker Hub default constants and
  `acceptManifestTypes`, previously inlined at the top of `client.go`.
- **`errors.go`** (new): every exported error type this package returns —
  both the client-wiring errors (`ImageRefParseError`,
  `NestedManifestListError`, `PlatformNotFoundError`) and the auth errors
  (`RegistryAuthChallengeError`, `RegistryAuthError`) — consolidated in
  one file so error shapes and their `slog.LogValuer` implementations are
  easy to compare side-by-side.
- **`auth_test.go`** (new): `TestParseChallenge` (moved from
  `registry_test.go`) plus `TestAuthenticate_WithCredentials_SendsBasicAuthOnTokenExchange`
  and `TestAuthenticate_NoCredentials_SendsNoAuthorizationOnTokenExchange`
  (moved from the now-deleted `credentials_test.go`), along with their
  httptest helpers.
- **`credentials_test.go`** deleted — fully absorbed into `auth_test.go`.
- **`registry_test.go`** trimmed to `TestParseImageRef` only (the sole
  remaining non-auth pure-function test); its file doc comment now
  cross-references `auth_test.go` for auth-specific tests.
- File-level doc comments on `client.go`, `auth.go`, and the package
  `doc.go` updated to describe the new file layout.
- Verified: `gofmt`, `go build`, `go vet`, `go test` (untagged: 4 tests
  pass) and `go test -tags=integration` (all integration + unit tests
  pass against real Docker Hub/GHCR/MCR registries), full repo
  `go test ./...`, `go run ./examples/go-edge-models` (demo output
  unchanged), and `just check` (staticcheck+gosec, 0 issues) — all clean.

---

## Round 89 (`docker/registry` proven registry-agnostic against GHCR + MCR; a real bug found and fixed; private-repo `Credentials` support added)

Extended `docker/registry` to be demonstrably registry-agnostic — same
`GetTags`/`GetImageMetadata` functions, same routes, working identically
against Docker Hub, GHCR (`ghcr.io`), and MCR (`mcr.microsoft.com`) — per
explicit design goal that the user "will not use [a different client]...
feels using the same client/routes despite the image URL."

**Phase 1 (prove it) found and fixed a real, generic bug.** Added GHCR
(`ghcr.io/nginxinc/nginx-unprivileged`) and MCR
(`mcr.microsoft.com/dotnet/runtime`) cases to the existing integration
test table. MCR passed immediately (anonymous, no auth — already handled
by `authenticate`'s 200-ping-means-no-auth path). GHCR initially FAILED
with a 403 on the token-exchange step. Root cause: `authenticate`
previously preferred the CHALLENGE's own `Scope` value when present,
falling back to a self-built `"repository:<repo>:pull"` scope only when
the challenge's scope was empty — this happened to work for Docker Hub
only because Docker Hub's base (repository-agnostic) `/v2/` ping never
includes a `Scope` in its challenge at all; GHCR's base ping, however,
DOES include a non-empty but placeholder/example scope
(`"repository:user/image:pull"`), which is architecturally impossible to
be correct (the base ping cannot know which repository the caller wants).
Fixed generically (no registry-name branching): `authenticate` now ALWAYS
self-builds the pull scope from the repository it is actually calling
for, unconditionally ignoring `challenge.Scope` — a pure bug fix in
`docker/registry`'s own trust assumption, applicable to every registry
identically. Confirmed fix via a scratch reproduction, then via the full
integration suite: all 4 images (Docker Hub ×2, GHCR, MCR) now pass with
zero registry-specific code anywhere.

**Phase 2 (the one real remaining gap): private-repository Basic auth.**
Added `Credentials{Username,Password}` (public type, `types.go`),
`Option`/`WithCredentials(Credentials) Option` (functional-option
pattern), and `FormatBasicAuth(username, password string) (string,
error)` (thin wrapper, mirrors `FormatBearerToken`) built on a new
`internal.BasicAuthCodec`/`internal.BasicCredentials` (same
`MapCodecSafe` pattern as `BearerTokenCodec`). `GetTags`/`GetImageMetadata`
gained a variadic `...Option` parameter — 100% backward compatible,
existing call sites unaffected; `authenticate` sends the resolved
`Credentials` as a Basic `Authorization` header ONLY on the token-exchange
request (`GetTokenRoute`), never on the subsequent Bearer-authenticated
calls. Verified via two new httptest-based unit tests
(`credentials_test.go` — the one deliberate, narrowly-scoped exception to
`registry_test.go`'s Round-88 IO-free design, since observing an HTTP
header requires a real request and no private-registry secret is
available/appropriate in this environment) confirming Basic auth is sent
when `Credentials` are supplied and absent when they are not.

**Explicit non-goals held**: no per-registry subpackages, no
registry-name branching anywhere, no speculative tags-list pagination
support (checked — MCR ignored a `?n=` pagination hint and returned the
full list; not needed for any image tested).

Updated `docker/registry/doc.go` and `examples/go-edge-models/README.md`
to state the registry-agnostic guarantee explicitly (which registries are
verified) and document the `Credentials`/`WithCredentials` escape hatch.

Verified via `go build ./...`, `go vet`, `go test
./examples/go-edge-models/docker/registry/...` (4 pure/mock unit tests,
no tags), `go test -tags=integration ./...` (9 tests: 5 original + GHCR/MCR
cases + the 2 new pure tests, all passing against the 3 live registries),
full `go test ./...` (no regressions), `gofmt -l` (clean), `go run
./examples/go-edge-models` (demo output unchanged), and `just check`
(staticcheck+gosec, 0 issues).

---

## Round 88 (`docker/registry` — real Docker Hub integration test + README; `registry_test.go` made fully IO-free)

Live-verified `docker/registry` against the real `registry-1.docker.io`/
`auth.docker.io` for `nodered/node-red` and `alpine` (no repo changes for
the initial ad-hoc pass), then committed that coverage as a proper,
opt-in test: `docker/registry/registry_integration_test.go`, gated behind
`//go:build integration` — never compiled/run by `go build`/`go vet`/
`go test ./...`/`just check`; run explicitly via `go test
-tags=integration ./examples/go-edge-models/docker/registry/...`. Adds a
runtime network-reachability guard (`t.Skip` on failure) so a
tagged-but-offline run skips cleanly. Covers: `GetTags`/`GetImageMetadata`
for both images (table-driven), a platform-override check confirming
`linux/amd64` vs `linux/arm64` resolve to genuinely different digests
against real multi-arch data, a `PlatformNotFoundError` check, and a small
`ParseImageRef` real-convention sanity check. All 5 integration tests pass
against the live registry.

Per follow-up review request, simplified `registry_test.go` to be
**totally IO-free**: removed the ~150-line httptest-mock-server
infrastructure (`newTestRegistry`, `imageURLFor`, `httpClientFor`,
`roundTripFunc`, `asPlatformNotFoundError`) and the three tests built on
it (`TestGetTags`, `TestGetImageMetadata_ResolvesManifestList`,
`TestGetImageMetadata_PlatformNotFound`) — their behavioral coverage is
now provided more realistically by the new integration test against the
REAL registry. `registry_test.go` now contains only `TestParseImageRef`
and `TestParseChallenge`, both pure-function tests with zero network/IO.
**Explicit tradeoff, noted for transparency**: the default (non-integration-
tagged) `go test ./...`/`just check` path no longer exercises
`GetTags`/`GetImageMetadata`'s full orchestration (auth flow, manifest-list
resolution, header merging) at all — that coverage now requires the
`integration` tag + network access. This was a deliberate choice: the
mock-server infrastructure was substantial to maintain and the real
integration test is strictly more realistic; the two test files' doc
comments cross-reference each other so the coverage split is discoverable.

Added `examples/go-edge-models/README.md` — package map (`iotedge`,
`iotedge/modulepatch`, `docker`, `docker/registry` + its `internal`
boundary), quick usage snippets (manifest decode, `ModulePatch` +
`ports.PatchEncoded`, `GetTags`/`GetImageMetadata`), running the example,
and testing instructions (unit vs. `-tags=integration`) — every code
snippet's signatures were cross-checked against the actual source before
being written.

Verified via `go build ./...` (repo-wide), `go vet`, `go test
./examples/go-edge-models/docker/registry/...` (no tags — confirms only
the 2 pure-function tests run), `go test -tags=integration
./examples/go-edge-models/docker/registry/...` (confirms all 5 integration
tests + the 2 unit tests pass together against the live registry), full
`go test ./...` (no regressions), `gofmt -l` (clean), `go run
./examples/go-edge-models` (demo output unchanged), and `just check`
(staticcheck+gosec, 0 issues; confirms the integration file is correctly
excluded from gosec's scan by the build tag too).

---

## Round 87 (`docker/registry/internal` — a real, compiler-enforced public/private API boundary)

Split `docker/registry` into a public surface and a genuine Go `internal/`
package, per user request to make the future library's API surface
unambiguous. Unlike prior rounds' `types.go`/`constraints.go`/`codecs.go`
file-naming CONVENTION (which only signaled intent via capitalization),
`docker/registry/internal` is a COMPILER-ENFORCED boundary — Go only allows
importing an `internal/` package from code rooted at its parent directory;
verified directly by attempting (and having the compiler correctly reject)
an import of `docker/registry/internal` from the sibling `iotedge` package.

**Stayed public** (`docker/registry`): `ImageRef`/`ImageRefCodec`,
`TagsList`/`TagsListCodec`, `ManifestMetadata`, the four `Get*Req` types
(needed since they're the exported `Get*Route` values' type parameters),
`PingRoute`/`GetTagsRoute`/`GetManifestRoute`/`GetTokenRoute`,
`GetTags`/`GetImageMetadata`, `ParseImageRef`, all 5 error types, and the
`Format*` helper family.

**Moved to `docker/registry/internal`** (organized as `types.go`/
`constraints.go`/`codecs.go`/`helpers.go`, mirroring the parent package's
own file convention): the 9 wire-shape/auth-flow types
(`ManifestDescriptor`, `PlatformDescriptor`, `SingleManifestWire`,
`ManifestListWire`, `ManifestEnvelope`, `TokenResponse`, `Challenge`,
`PlatformSelector`, `DockerScope`), their constraints, their codecs
(`ManifestEnvelopeCodec`, `TokenResponseCodec`, `ChallengeCodec`,
`WWWAuthenticateCodec`, `PlatformSelectorCodec`, `DockerScopeCodec`,
`BearerTokenCodec`, `DigestCodec`, `PlatformCodec` — all now exported
WITHIN `internal`, since the parent package's `routes.go`/`client.go` need
to reference them), and the `ParseChallengeString`/`FormatChallengeString`/
`ParseDockerScopeString`/`FormatDockerScopeString` parse/format helper
functions — `internal/helpers.go`'s file doc comment explicitly frames
these as "the helpers wrapping codec decode/encode," per the user's own
phrasing.

**Ergonomic improvement as a byproduct**: the public `FormatChallenge`/
`FormatDockerScope`/`FormatPlatformSelector` signatures changed from
taking the (now-internal) struct type directly to plain fields
(`FormatChallenge(realm, service, scope string)`, etc.) — a consumer of
this library never needs to import or name an internal type at all, only
pass plain values; each function builds the internal struct itself before
calling into `internal.XxxCodec.Encode`.

**`routes.go`** now imports `internal` and types `GetManifestRoute`/
`GetTokenRoute`'s `Resp` as `internal.ManifestEnvelope`/
`internal.TokenResponse` — documented explicitly in the route's doc
comment: a consumer calling `GetManifestRoute.ClientHandle()` directly
(bypassing `GetImageMetadata`) receives a value of this unimportable type;
Go still permits reading its EXPORTED fields via ordinary type inference,
but the type cannot be named outside `docker/registry` — a deliberate
signal that the raw envelope is internal plumbing, and `GetImageMetadata`
is the supported, fully-resolved public result.

**Forward-looking rationale (per user)**: `internal/` is documented (its
own `doc.go`) as staying PURELY generic OCI Distribution Spec / Docker
Registry HTTP API v2 plumbing — future GHCR/MCR-specific integrations
(both already OCI-compliant, so the CURRENT generic implementation likely
already works against them unmodified) will live in their own sibling
packages (e.g. `docker/registry/ghcr`), composing `docker/registry`'s
PUBLIC contract, with NO access to this internal package by construction —
exactly the boundary that keeps registry-specific quirks from ever leaking
into the shared generic plumbing.

Verified via `go build ./...` (repo-wide), `go vet`, `go test
./examples/go-edge-models/docker/registry/...` (all 5 pre-existing tests
pass, only their `Format*` call sites simplified for the new plain-field
signatures — zero behavioral changes), full `go test ./...` (no
regressions), `gofmt -l` (clean), `go run ./examples/go-edge-models` (demo
output byte-identical), a direct compiler-rejection check confirming the
`internal/` boundary is genuinely enforced, and `just check`
(staticcheck+gosec, 0 issues).

---

## Round 86 (`docker/registry` — remaining string concatenations become codecs + a `Format*`/`Parse*` helper family)

Audited every remaining `+`-based string concatenation in `docker/registry`
and its consumers (`main.go`, `registry_test.go`) per user request, and closed
every genuine gap. Added `DockerScope{ResourceType, Name, Actions []string}` +
`DockerScopeCodec` (`MapCodecValidated`, mirrors `ImageRefCodec`'s pattern —
`"type:name:action1,action2"` ↔ struct) replacing the hand-concatenated
`"repository:" + repository + ":pull"` scope-fallback string in
`authenticate`. Added `BearerTokenCodec` (`MapCodecSafe`, `"Bearer <token>"`
↔ bare token string) replacing `"Bearer " + token` in `bearerCredentialFunc`.
Added a new `Format*` thin-wrapper family (`FormatImageRef`, `FormatChallenge`,
`FormatDockerScope`, `FormatPlatformSelector`, `FormatBearerToken`) mirroring
the existing `ParseImageRef` convention — each hides its codec's
`Encode(...) (any, error)` unboxing behind an ergonomic typed function, and
all are EXPORTED so consumers (tests, mocks, demos) can construct valid wire
values without hand-rolling string formats themselves. Reused the EXISTING
`PlatformSelectorCodec` (no new codec) to replace the manual
`OS+"/"+Architecture` concatenation when building the "available platforms"
list in `GetImageMetadata`'s error path. Added one deliberate NON-codec
helper, `registryBaseURL(host string) string`, replacing three repeated
`"https://" + host` concatenations — explicitly NOT modeled as a codec since
deriving a dial address from already-validated config data has no wire-decode
direction to represent; forcing a codec there would misrepresent what a codec
is for. Updated `main.go`'s registry demo mock and `registry_test.go`'s
`newTestRegistry`/`imageURLFor` to build their WWW-Authenticate header,
synthetic image URL, and Bearer-auth-check values via the new
`registry.Format*` helpers instead of manual string concatenation —
demonstrating the SAME codecs used to decode real registry responses also
correctly encode the values a mock server needs to send. Deliberately left
`TestParseChallenge`'s literal challenge-string fixtures untouched — building
them via the codec's own Encode would make that test circular, no longer an
independent check of the wire format. Verified via `go build ./...`,
`go vet`, `go test ./examples/go-edge-models/docker/registry/...` (all 5
pre-existing tests pass, zero test-assertion changes needed beyond the mock
construction mechanism), full `go test ./...` (no regressions), `gofmt -l`
(clean), `go run ./examples/go-edge-models` (demo output byte-identical),
and `just check` (staticcheck+gosec, 0 issues).

---

## Round 85 (`WWWAuthenticateCodec` — the last plain `Header.Get(...)` in `docker/registry` becomes a codec)

Closed the final non-codec-based step in `docker/registry`: `authenticate`'s
extraction of the "WWW-Authenticate" value out of `nethttp.UnexpectedStatusError.
Header` (Round 84) was still a plain `Header.Get("WWW-Authenticate")` call
followed by a separate `ChallengeCodec.Decode(string)`. Added `headerCodec` (a
trivial passthrough `codex.Codec[http.Header]` — Decode type-asserts the input,
Encode is the identity — existing purely so the next codec can compose via the
SAME `MapCodecValidated` pattern every other codec in this package uses) and
`WWWAuthenticateCodec` (`http.Header -> Challenge` via `MapCodecValidated`,
reusing the EXACT same `parseChallengeString`/`formatChallengeString`/
`challengeStructCodec` building blocks `ChallengeCodec` itself uses — zero
duplicated parsing logic, just composed one layer higher, over the header set
instead of an already-extracted string). `parseChallenge`'s signature changed
from `string` to `http.Header` — now a thin wrapper around
`WWWAuthenticateCodec.Decode`, so the header-extraction step is itself part of
a codec Decode call, not a preceding plain-Go step. `authenticate` now calls
`parseChallenge(statusErr.Header)` directly. `ChallengeCodec` (string ↔
Challenge) is unchanged and remains available for any caller that already has
the raw header string. Fixed a real correctness bug surfaced while updating the
test: constructing `http.Header` via a map literal (`http.Header{"WWW-
Authenticate": [...]}`) does NOT canonicalize the key the way `http.Header.Set`
does, so a literal `"WWW-Authenticate"` key is invisible to `.Get("WWW-
Authenticate")` (which canonicalizes its lookup key to `"Www-Authenticate"`) —
fixed both `WWWAuthenticateCodec`'s Encode direction and the new
`TestParseChallenge` test to build headers via `.Set(...)`, not map literals.
Verified via `go build ./...`, `go vet`, `go test ./examples/go-edge-models/docker/registry/...`
(all 5 tests pass, including the updated `TestParseChallenge`), full
`go test ./...` (no regressions), `gofmt -l` (clean), `go run
./examples/go-edge-models` (demo output byte-identical), and `just check`
(staticcheck+gosec, 0 issues). Entirely example-local — no further
`adapters/nethttp` changes were needed; Round 84's `UnexpectedStatusError.
Header` already supplied the input this codec consumes.

---

## Round 84 (`adapters/nethttp.UnexpectedStatusError.Header` — closing the last declarative gap in `docker/registry`'s `authenticate`)

Closed the one remaining manual-HTTP exception in `docker/registry/client.go`'s
`authenticate` function (the Ping/401-detection step), per explicit user
request to have EVERY request in that function be a declared route with
codecs, no manual plumbing at all. Investigation confirmed this genuinely
required a small, additive change to the SHARED library, not just an
example-local workaround: `adapters/nethttp.Call`'s response header/cookie
merge (`rest.NewRequiredResponseHeaderParam`) only applies on the successful
(2xx) path; `UnexpectedStatusError` (returned on non-2xx) had no `Header`
field at all; and `rest.ErrorPattern`'s client-side decode only ever receives
the response BODY, never headers — so there was no existing declarative path
to reach `WWW-Authenticate` on a 401 response. Added `Header http.Header` to
`adapters/nethttp.UnexpectedStatusError` (purely additive — populated in
`Call`'s non-2xx fallback branch; confirmed via existing tests that nothing
relies on the struct's exact field set, since all assertions use `errors.As`
+ individual field checks) — the same category of information `Body []byte`
already exposes on this struct, now extended to cover the header set too.
Added `TestCall_UnexpectedStatus_HeaderPopulated` in
`adapters/nethttp/client_test.go` confirming the new field. Updated
`.github/instructions/go-codex.instructions.md`'s `UnexpectedStatusError`
field-set documentation to include `Header` and its rationale.
`authenticate`'s Ping step is now a plain `nethttp.CallHandle(PingRoute...)`
call like every other call in the package — on error, `errors.As(err,
&statusErr)` extracts `statusErr.Header.Get("WWW-Authenticate")`, decoded via
the already-existing `ChallengeCodec` (Round 83). Zero manual
`http.NewRequestWithContext`/`httpClient.Do`/`resp.Body` handling remains
anywhere in `docker/registry/client.go` — every request/response in the
package is now route+codec driven. Searched the repo for other similar
"manual plumbing to work around a header-access gap" helpers per the user's
request: `adapters/openai/client.go` also calls `http.Client.Do` directly,
but it is an intentionally separate, already-reviewed `ports`-pattern
adapter with its own bespoke wire format (no `rest.Route` involved at all,
so there is no declarative mechanism it could be bypassing) — not the same
anti-pattern, correctly out of scope. No other offenders found. Verified via
`go build ./...` (repo-wide, since this touches shared library code),
`go vet` scoped to the changed packages (a pre-existing, unrelated `go vet`
finding in `adapters/chi/adapter_test.go` was confirmed present before this
session and left untouched, per the "don't fix pre-existing unrelated
issues" rule), `go test ./adapters/nethttp/...` (existing tests + new
header-population test all pass), `go test ./examples/go-edge-models/docker/registry/...`
(all 5 pre-existing tests pass UNCHANGED — zero test modifications needed,
confirming the refactor preserved exact external behavior), full
`go test ./...` (repo-wide, no regressions), `go run
./examples/go-edge-models` (demo output byte-identical), and `just check`
(staticcheck+gosec, 0 issues).

---

## Round 83 (`docker/registry` — full-codec + one-struct-one-call refactor)

Converted every remaining manual map-building, manual HTTP, and ad-hoc string
parsing in `docker/registry` into declarative codecs and `nethttp.CallHandle`
one-struct-one-call convenience, per explicit feedback that "codecs for most
of the implementation" wasn't good enough — go FULL codec. Two categories of
change:

**HTTP-call boundary → merge-field codecs + CredentialFunc.** New request
structs `GetTagsReq{Name}`, `GetManifestReq{Name, Reference}`,
`GetTokenReq{Service, Scope}` replace `struct{}` + hand-built `vars`/query
maps; `routes.go` declares `rest.NewPathParam`/`NewOptionalQueryParam` merge
fields for each, consumed automatically by `nethttp.CallHandle`. Discovered
(by reading `adapters/nethttp/client.go`) that `nethttp.Call`/`CallHandle`
ALREADY merges declared response headers into `Resp` on every successful
(2xx) response — added `manifestEnvelope.Digest` (a peer field of
`Single`/`List`, since the digest belongs to the response envelope, not the
JSON body) merged via `rest.NewRequiredResponseHeaderParam("Docker-Content-Digest", ...)`,
which ELIMINATED the manual HTTP call `fetchManifest` previously needed just
to read that header — it is now a single `nethttp.CallHandle` call.
Bearer-token auth switched from manual `CallOptions.ExtraHeaders` to
`RouteMeta.Security` (`bearerAuthSecurity`) + a new `bearerCredentialFunc`
helper supplying `CallOptions.CredentialFunc` — the same declarative
security mechanism `examples/adapters-nethttp-client` demonstrates.

**Every manual parsing routine → a real `codex.Codec[T]`.** `ImageRefCodec`
(`codex.MapCodecValidated`, mirrors `docker.BindCodec`'s parse/format
pattern) replaces `ParseImageRef`'s inline registry/repository/reference
extraction — `ParseImageRef` is now a thin wrapper around
`ImageRefCodec.Decode`, with a real `formatImageRefString` Encode direction
(round-trips "registry/repo:tag" vs "registry/repo@digest" faithfully,
detecting which via whether `Reference` contains ":"). `ChallengeCodec`
replaces `parseChallenge`'s ad-hoc regex (WWW-Authenticate → new `Challenge{Realm,
Service, Scope}` type), also with a faithful Encode direction.
`PlatformSelectorCodec` replaces the ad-hoc `strings.SplitN` inside
`platformMatches` ("os/arch" → new `PlatformSelector{OS, Architecture}`
type) — `platformMatches` is now a plain struct-field comparison over
already-decoded values, no string splitting left anywhere in business
logic. New `DigestCodec` (format constraint: `algorithm:hex`) applied to
every digest value in the package — `manifestDescriptor.Digest` (wire body)
AND `manifestEnvelope.Digest` (response-header merge field) — closing the
loop so a malformed digest is now a structured `ConstraintError`, not a
silently-accepted bare string.

**Two explicitly scoped exceptions, justified as non-parsing:**
`"Bearer " + token` header-value construction stays plain Go (one-way
formatting of an OUTGOING value with no corresponding decode direction — a
`Codec[T]` models a two-way transform, and there is nothing to decode
here); `TotalSizeBytes` summation stays plain Go (arithmetic aggregation
over already-decoded structured values, not a wire-format encode/decode).
The Ping/401-detection manual `http.Client.Do` call in `authenticate`
remains the SOLE I/O exception (`nethttp.Call`'s `UnexpectedStatusError`
still doesn't expose response headers, and `WWW-Authenticate` is only
present on the error response) — but the header value it retrieves is now
decoded via `ChallengeCodec`, not ad-hoc regex.

Verified via `go build ./...`, `go vet`, `go test ./examples/go-edge-models/docker/registry/...`
(all 5 pre-existing tests kept passing after updating test fixtures to use
valid 64-hex-char digests — `DigestCodec`'s new constraint correctly
rejected the old placeholder digest strings like `"sha256:layer1"`, a
genuine improvement in strictness, not a regression), `go test ./...`
(repo-wide, no regressions), `gofmt -l` (clean), `go run
./examples/go-edge-models` (demo output unchanged in shape, digest values
updated to valid hex), and `just check` (staticcheck+gosec, 0 issues).

---

## Round 82 (`docker/registry` — Docker Registry HTTP API v2 client: routes + codecs + auth/resolution orchestration)

Added a new sibling sub-package `examples/go-edge-models/docker/registry`, nested
under `docker` (zero dependency on `iotedge` or `docker`'s own CreateOptions types —
an entirely separate Docker HTTP API), modeling the Docker Registry HTTP API v2 /
OCI Distribution Spec surface needed to fetch an image's tags and lean top-level
manifest metadata (`SchemaVersion`, `MediaType`, `Digest`, `TotalSizeBytes` — no
per-layer detail). First package in the `examples/go-edge-models` tree to go beyond
pure codecs: `routes.go` declares `rest.Route` values (`PingRoute`, `GetTagsRoute`,
`GetManifestRoute`, `GetTokenRoute`, all `ClientHandle()`-based, no server/Builder)
as the PRIMARY reusable contract — a consumer can call `.ClientHandle()` themselves
and drive `adapters/nethttp.Call` directly with their own client/observer/security
wiring, independent of this package's own orchestration. `client.go` is a convenience
layer built on top: `ParseImageRef` (replicates Docker Hub's default-registry +
`library/`-prefix + `latest`-default convention via a from-scratch domain/tag/digest
splitter, validated against the library's existing `validate.ContainerImage`),
`authenticate` (WWW-Authenticate challenge parsing → realm-scoped Bearer token fetch
via `GetTokenRoute`, whose `rest.Route` path is deliberately EMPTY since the auth
realm can be a completely different host than the registry — Docker Hub's own
`auth.docker.io` vs `registry-1.docker.io` topology), `GetTags`, and
`GetImageMetadata` (auto-detects a multi-arch manifest list / OCI image index via
`manifestEnvelopeCodec`'s `UntaggedUnion` single-vs-list dispatch — same
try-in-order pattern as `iotedge.EnvVarValueCodec` — resolves ONE platform
transparently, defaulting to `linux/amd64`, overridable per-request; a nested list
after resolution is a clear `NestedManifestListError`). Documented and applied a
deliberate, isolated exception: `nethttp.Call`'s `UnexpectedStatusError` doesn't
expose response headers, so the Ping (401 challenge detection) and manifest-fetch
(needs the `Docker-Content-Digest` header, which is NOT part of the manifest body)
steps use a manual `http.Client.Do` call — `GetTagsRoute`/`GetTokenRoute` still go
through `nethttp.Call`/`ClientHandle()` normally since they need no header access.
Added typed, `slog.LogValuer` errors (`ImageRefParseError`,
`RegistryAuthChallengeError`, `RegistryAuthError`, `NestedManifestListError`,
`PlatformNotFoundError`) per the repo's structured-error convention. Added
`registry_test.go` (this package, unlike its pure-codec siblings, has real
executable orchestration logic worth testing directly): `ParseImageRef` table tests
(Docker Hub default, explicit registry+port, digest reference, invalid shape) and an
`httptest`-based end-to-end test (two servers — registry + a SEPARATE auth-realm
host, proving the realm-on-a-different-host case) covering `GetTags`, manifest-list
resolution, and the `PlatformNotFoundError` path. Demonstrated in a new section
appended to `examples/go-edge-models/main.go` — PURE WIRING per the layering
contract (spins up the same two-`httptest`-server topology, calls
`registry.GetTags`/`registry.GetImageMetadata` against a synthetic multi-arch
`cv-writer-web` image, prints results) — no business logic duplicated in `main.go`.
Verified via `go build ./...`, `go vet`, `go test ./...` (new tests pass), `gofmt -l`
(clean), `go run ./examples/go-edge-models` (new section's output correct, all
prior output unchanged), and `just check` (staticcheck+gosec, 0 issues).

---

## Round 81 (`iotedge/modulepatch` — derived-representation package composed from `iotedge`'s exported codecs + `ports.PatchEncoded` demo)

Proved out Round 80's "compose new wire codecs from reusable field-level codecs" goal
with a real use case: `modulepatch.ModulePatch{ModuleName, ImageURL}` — a flat struct
whose `ModulePatchCodec` encodes directly into the manifest's full real nested wire
shape (`modulesContent` → `$edgeAgent` → `<dotted module key>` → `settings` →
`image`), reusing `iotedge.ModuleNameCodec` and `iotedge.ImageCodec` directly (zero new
constraints). Initially implemented as a `derived_types.go`/`derived_codecs.go` file
pair inside `iotedge` itself, then — per user feedback — promoted to its OWN sibling
sub-package, `iotedge/modulepatch` (types.go/codecs.go/doc.go), mirroring the
`docker`/`iotedge` reuse-boundary precedent from Round 80: `iotedge` stays focused on
the base wire schema and never accumulates one file per derived representation, and a
consumer who only needs the image-patch shape doesn't pull in any other derived
representation added later. Any FUTURE derived representation (e.g. a status-only
patch) should get its own sibling sub-package under `iotedge/` the same way — NOT a
new file inside `iotedge` or inside `modulepatch`. Built via minimal per-level
intermediate wire-shape types local to `modulepatch` (`imageSettingsPatch`,
`moduleConfigPatch`, `modulesContentPatch`, `manifestImagePatch` — each mirroring
exactly one field of the corresponding real manifest type, `ModuleSettings`/
`ModuleConfig`/`ModulesContent`/`DeploymentManifest`, with only "image" populated)
composed via nested `codex.Struct`/`codex.Map`, then one `MapCodecSafe` wrapper
converting the flat `ModulePatch` Go value ↔ the fully-nested document shape — the same
building-block pattern already used throughout `iotedge/codecs.go` for scalar
wrapping, here applied to a whole nested structure. Demonstrated end-to-end in
`main.go` via `ports.NewFile` + `ports.PatchEncoded` (mirrors
`examples/flat-key-patch`'s pattern): writes the real `usecase1.json` to a temp file,
patches `cv-writer-web`'s image, reads back and confirms via a full-manifest scan that
the OTHER 11 modules and every other field of the patched module (env, createOptions,
etc.) are byte-for-byte unaffected — deep-merge only touched `settings.image`.
Verified via `go build ./...`, `go vet`, `go test ./...`, `gofmt -l` (clean),
`go run ./examples/go-edge-models` (before/after output confirms exact, isolated field
change), and `just check` (staticcheck+gosec, 0 issues).

---

## Round 80 (`examples/go-edge-models` restructured into an importable library: `docker` + `iotedge` sibling packages)

Migrated the flat `package main` codec/struct files into two importable, non-`main`
packages so downstream projects can compose new codecs reusing these types/codecs
directly (e.g. a patch codec keyed by module name touching only the image field).
`docker/` (Port, Bind, Ulimit, Healthcheck, HostConfig, CreateOptions — the generic
Docker Engine API create-options modeling) is a SIBLING of `iotedge/` (ModuleConfig,
ModuleSettings, EnvVars, ModuleName/Modules/DeploymentManifest), not nested under it —
`iotedge` imports `docker` for `ModuleSettings.CreateOptions`, but `docker` has zero
dependency on `iotedge`, so it stays reusable standalone for non-IoT-Edge tooling
(Docker Compose codecs, plain `docker create`/`run` wrappers, etc.). Within EACH
package, further split into `types.go` (plain structs/named types only),
`constraints.go` (`validate.Constraint` values only), and `codecs.go` (all
`codex.Codec[T]` values + `RequiredField`/`OptionalField` wiring) — every field's codec
is now a standalone named value (previously several were inline `.Refine(...)` chains
buried inside a struct's codec definition), so a caller assembling a NEW wire codec can
reuse the exact same field-level codec. Newly extracted/named: `docker.UlimitNameCodec`
(was inline on `Ulimit`'s "Name" field), `docker.bindPathCodec` (unexported, was
duplicated inline on `Bind`'s "hostPath"/"containerPath" fields), `iotedge.ImageCodec`
(was inline on `ModuleSettings`'s "image" field). Exported previously-unexported
cross-package-boundary symbols: `docker.isZeroCreateOptions` → `docker.IsZeroCreateOptions`
(needed by `iotedge.CreateOptionsFieldCodec`), `moduleKeyPrefix` →
`iotedge.ModuleKeyPrefix` (reusable by a consumer's own dotted-key patch codec, mirroring
`examples/flat-key-patch`'s `containerKeyPrefix`). Added `doc.go` package overviews to
both new packages (mirrors `examples/sensor-service/domain/doc.go` convention).
Compacted in-line documentation: removed all `usecase1.json`-specific narrative
call-outs ("confirmed against the real reference file...") from every codec file,
replacing them with general, fixture-independent design rationale — `main.go`'s own
`usecase1.json` embed/read/print logic is UNCHANGED (it legitimately embeds and decodes
that file). Verified via `go build ./...`, `go vet`, `go test ./...`, `gofmt -l` (clean),
re-running the example against the real `usecase1.json` (output content identical to
pre-restructure baseline — the only diffs were pre-existing Go map-iteration-order
nondeterminism in ExposedPorts/env-var printing, confirmed present before this round
too, not a regression), a scratch-module proof that a brand-new `ImagePatch` codec
composes `iotedge.ModuleNameCodec` + `iotedge.ImageCodec` directly, and `just check`
(staticcheck+gosec, 0 issues).

---

## Round 79 (`ModuleCreateOptions.go` — Docker `CreateOptions`/`HostConfig` field extension: Cmd/Entrypoint/Hostname/Domainname/Memory/MemorySwap/Ulimits/Healthcheck)

Extended `CreateOptions` (top-level: `Cmd []string`, `Entrypoint []string`, `Hostname
string`, `Domainname string`, `Healthcheck Healthcheck`) and `HostConfig` (`Memory int64`,
`MemorySwap int64`, `Ulimits []Ulimit`) — scope narrowed via 6 rounds of user-confirmed
leading questions against a research table of ~20 candidate Docker Engine API fields
(deliberately deferring `Privileged`/`Devices`/`CapAdd`/`CapDrop`/`SecurityOpt`,
`NetworkMode`/`ExtraHosts`/`DNS`, and `User`/`WorkingDir`/top-level `Env` — the last
flagged as a real naming-collision risk vs. IoT-Edge's own module-level `env` map if ever
added). New `Ulimit{Name,Soft,Hard}` type with `Name` validated via a local `OneOf`
constraint against Docker's full documented `--ulimit` name list (`as, core, cpu, data,
fsize, locks, memlock, msgqueue, nice, nofile, nproc, rss, rtprio, rttime, sigpending,
stack`). New `Healthcheck{Test,Interval,Timeout,StartPeriod,StartInterval,Retries}` type
(top-level `createOptions` field, sibling of `HostConfig`, not nested in it) backed by a
new `dockerNanosDurationCodec` (`MapCodecSafe` wrapping `codex.Int64()` ↔
`time.Duration`) — required because Docker's wire format for these fields is a raw
nanosecond integer (e.g. `30000000000`), NOT a duration string, so `codex.Duration()`
(which expects `"30s"` via `time.ParseDuration`) does not fit. All new fields are
`OptionalField` (matches the existing convention: real IoT-Edge modules vary widely in
which create-options fields they set). Updated `isZeroCreateOptions`/added
`isZeroHealthcheck` to check all new fields — required for `CreateOptionsFieldCodec`'s
(`ModuleSettings.go`) empty-string round-trip tolerance to stay correct (an unfixed
`isZeroCreateOptions` would have wrongly treated a `CreateOptions` with only e.g. `Cmd`
set as "zero" and lost the data on re-encode). Verified via an isolated scratch module
(synthetic JSON exercising all new fields, invalid ulimit name rejection, nanosecond↔
`time.Duration` round-trip correctness) plus a full re-run against the real
`examples/go-edge-models/examples/usecase1.json` (all 12 modules unaffected — none use
the new fields yet, confirming no regression). `go build ./...`, `go test ./...`, all
`examples/*` runs, and `just check` (staticcheck+gosec) all clean.

---

## Round 78 (`DeploymentManifestTemplate.go` — `codex.Map[K,V]` module-name extraction; real-data robustness fixes)

Built `ModuleNameCodec`/`ModulesCodec` to extract the container/module name from a dotted
key (`"properties.desired.modules.cv-writer-kvrocks"` → `ModuleName("cv-writer-kvrocks")`)
using `codex.Map[K,V]` — mirroring `examples/flat-key-patch`'s `containerKeyCodec` two-layer
`MapCodecValidated` pattern (wire-level full-key constraint + domain-level name constraint),
but targeting a NAMED `ModuleName` type so it composes with `codex.Map`
(→ `map[ModuleName]ModuleConfig`) instead of `flat-key-patch`'s `codex.EntrySlice`
(→ a merged `[]Container` slice). Reused `validate.Slug` (already in the library) for the
name-segment check — verified it matches all 12 real module names in the user's
`examples/go-edge-models/examples/usecase1.json` reference file, so no new constraint
needed. Added `ModulesContent`/`DeploymentManifest` wrapper types matching the manifest's
real 2-level nesting (`modulesContent` → `$edgeAgent` → module map).

Per the user's explicit choice to decode the REAL reference file end-to-end (not a
synthetic snippet), inspecting every one of its 12 modules surfaced — and required fixing —
several real data-shape issues:

- **G7 — `TypeCodec`/`StatusCodec`/`RestartPolicyCodec`/`VersionCodec` bug** (flagged but
  left unfixed as out-of-scope in Rounds 76-77; now genuinely blocking): these wrapped
  `Type`/`Status`/`RestartPolicy`/`Version` as single-field STRUCTS expecting
  `{"type":"docker"}`, but the real wire shape is a bare string `"docker"` — would reject
  every real module. Fixed: replaced with `codex.MapCodecSafe`-based plain-string wrappers.
- **`ModuleConfig.Env` made `OptionalField`**: `cv-writer-kvrocks` has no `"env"` key at all
  in the real file; 11/12 other modules do.
- **`EnvVarsCodec` fixed to use `codex.Map[EnvVarName, EnvVar]`** (was still `StringMap`,
  broken after the user's own independent edit introduced a named `EnvVarName` key type).
  Deliberately did NOT apply `validate.EnvVarName`'s POSIX format constraint to it — real
  module env var names in the reference file include `https_proxy`, `no_proxy`,
  `UploadTarget`, `ResourceID`, `LogAnalyticsWorkspaceId`,
  `experimentalFeatures__AddIdentifyingTags` (from the Azure Monitor metrics-collector
  module) — none match `[A-Z_][A-Z0-9_]*`; Docker itself places no format restriction on
  env var names, so applying that constraint would incorrectly reject legitimate data.
- **`CreateOptions.ExposedPorts`/`HostConfig` and `HostConfig.Binds`/`PortBindings` all made
  `OptionalField`**: real modules vary widely — only 1 of 12 declares all four; most
  declare only `ExposedPorts`; one declares neither (just an unmodeled `Cmd` field, silently
  ignored by the non-strict `Struct` codec — an intentional, already-documented "subset"
  modeling limitation, not a new gap).
- **New `CreateOptionsFieldCodec`** (in `ModuleSettings.go`, not `format.EmbeddedJSON`
  directly): tolerates an empty `createOptions:""` string (the Azure Monitor
  metrics-collector module ships this) as equivalent to `"{}"`/zero-value `CreateOptions{}`
  — `format.EmbeddedJSON` alone would fail this with `EmbeddedDecodeError` since `""` isn't
  valid JSON, and since `codex.Map`'s `Decode` aborts the ENTIRE map on any single entry's
  error, this one module would otherwise have blocked decoding all 12. Symmetric on encode:
  a zero-value `CreateOptions` re-encodes back to `""`, not `"{}"`.
- Verified via an isolated scratch-module module-by-module round-trip diff against the
  REAL reference file: all 12 modules decode correctly (names, images, ports, binds,
  env values including the int/string union); 11/12 modules show only EXPECTED, DOCUMENTED
  round-trip asymmetries on re-encode (`codex.Struct.Encode` always writes every declared
  field regardless of `Required`/`Optional`, so an absent `"env"` key becomes `"env":{}` on
  re-encode; unmodeled `Cmd`/`Memory` fields are silently dropped, matching `CreateOptions`'
  documented "subset" scope; map key ordering differs, which is not semantically
  significant) — none of these are bugs.
- Side effect: the pre-existing, previously out-of-scope unused `moduleKeyPrefix` constant
  finding (flagged in Rounds 76-77) is now resolved — the constant is genuinely used by
  `ModuleNameCodec`. `just check` is now fully clean across the ENTIRE repo.

---

## Round 77 (Docker `CreateOptions` codec — `format.EmbeddedJSON`, `validate.Port`/`DockerPort`, `codex.EntrySlice`)

Designed and implemented a typed codec for `ModuleSettings.CreateOptions` (previously a raw
`string` holding a JSON-escaped Docker create-options document: `ExposedPorts`,
`HostConfig.Binds`, `HostConfig.PortBindings`). All 6 design decisions confirmed with the
user before implementation:

- **String-escaping**: `format.EmbeddedJSON(CreateOptionsCodec)` (already existed in the
  library, documented in `docs/concepts/codec.md`) — zero new mechanism needed;
  `ModuleSettings.CreateOptions` is now the fully-typed `CreateOptions` struct, not a raw
  string.
- **New reusable Constraints** — `validate.Port` (bare port-number string, 1-65535 range
  check since a regex alone can't express the numeric bound) and `validate.DockerPort`
  (full `"<port>/tcp"`/`"<port>/udp"` spec string), both in `validate/format.go` next to
  `ContainerImage`/`CIDR`, same style/doc conventions, with table-driven tests in
  `validate/format_test.go`.
- **`ExposedPorts` → `[]Port`** (not `map[Port]struct{}`) via `codex.EntrySlice` — merges
  each key with its (discarded) always-empty `{}` value; `codex.Struct[struct{}]()` (zero
  fields) is the reusable "empty object" codec, no new library code needed for that either.
- **`PortBindings` → `[]PortBinding{Port, Bindings []PortBindingEntry}`** via
  `codex.EntrySlice` + `codex.SliceOf` — kept `PortBindingEntry{HostPort string}` as its
  own named struct (not flattened to `[]string`) so adding `HostIp` later is additive.
- **`HostPort` dual representation** — `PortNumberCodec` (in the example, not the library:
  domain-specific) built on `codex.StringOrInt()` (shipped 2 rounds ago) + `validate.Port`:
  accepts EITHER a JSON string or number on decode, always canonicalizes to a validated
  string on encode.
- **`HostConfig.Binds` → parsed `[]Bind{HostPath, ContainerPath, Mode}`** via
  `codex.MapCodecValidated` (wire codec `codex.String()`, target codec
  `codex.Struct[Bind](...)` used for its own per-field `Refine` via `Validate`) with a
  documented segment-count-based parser limitation (doesn't handle paths containing
  literal `:`).
- New file `examples/go-edge-models/ModuleCreateOptions.go` holds all of the above; a
  stale placeholder `examples/go-edge-models/CreateOptions.go` (pre-existing, minimal,
  superseded) was removed to resolve a duplicate-declaration conflict.
- Verified via an isolated scratch-module round trip of the user's EXACT example JSON:
  full decode → every field checked → re-encode → `reflect.DeepEqual` confirms semantic
  equivalence to the original (map key order differs, values don't); plus `HostPort`-as-
  bare-number decode, invalid protocol/out-of-range-port/malformed-bind-spec error paths,
  and a `:mode`-suffixed bind spec — all correct.
- Docs updated: `.github/instructions/go-codex.instructions.md`'s `validate` row.
- Verified: `gofmt`/`go build`/`go test ./...` clean repo-wide; all examples exit 0;
  `staticcheck`/`gosec` clean on `validate`/`examples/go-edge-models` except the SAME
  pre-existing, out-of-scope unused `moduleKeyPrefix` constant flagged in Rounds 75-76
  (still not fixed, still not part of this task).

---

## Round 75 (`codex.StringOrXxx` family — "string or number" convenience)

Evaluated whether the "value is a string OR a number" wire pattern (Docker/IoT-Edge module
env vars `"5"` vs `5`, Kubernetes `apimachinery.IntOrString`, Terraform/HCL, Helm
`values.yaml`) should be built into go-codex. Findings: it is common enough to warrant a
named convenience, AND it was already fully possible today with zero new code
(`codex.Either2(codex.String(), codex.Int64())`) — verified format-agnostic since
`encoding/json`/`yaml.v3`/`BurntSushi/toml` each decode numbers into different native Go
types (`float64` / `int`+`float64` / `int64`+`float64` respectively), all of which every
existing numeric primitive's `Decode` already type-switches over. User chose to both
document the pattern AND add named constructors:

- Added `codex/stringor.go`: 7 one-line constructors (`StringOrInt`, `StringOrInt32`,
  `StringOrInt64`, `StringOrUint`, `StringOrUint64`, `StringOrFloat32`, `StringOrFloat64`),
  each `Either2(String(), Xxx())` — pure sugar, zero new error types/schema shape/observer
  changes (inherits `Either2`'s existing `{oneOf:[...]}` schema and `EitherError` exactly).
- Added `codex/stringor_test.go`: one test per constructor (decode string/native
  number/invalid type, encode both branches, schema shape) plus a dedicated test
  confirming `StringOrInt64` decodes both YAML's native `int` and TOML's native `int64`
  correctly (not just JSON's `float64`).
- Docs updated: `codex/doc.go`'s composing-constructors list (also added the
  previously-undocumented `Either2` itself, which had been missing from that list),
  `docs/concepts/codec.md` (both overview tables plus a new "StringOrInt64 and family"
  prose subsection explaining the format-agnostic rationale), `docs/guides/enum-union-sum.md`
  (extended item 5), `.github/instructions/go-codex.instructions.md`'s `codex` row.
- Extended the officially-showcased `examples/enum-union-sum/main.go` with a "5b. Bonus"
  section demonstrating `codex.StringOrInt64()` end-to-end (JSON string → Left, JSON
  number → Right), keeping the example in sync with the new library feature.
- Rewrote `examples/go-edge-models/ModuleEnvVars.go` (from the previous round) to use
  `codex.StringOrInt64()` directly instead of a hand-rolled `MapCodecSafe`+`UntaggedUnion`
  implementation — dropped the custom `EnvVarValue` struct entirely (`EnvVar.Value` is now
  plain `codex.Either[string, int64]`), reducing the file from ~113 lines to 36 while
  preserving identical decode/encode behavior (verified via an isolated scratch-module
  round trip: string/number/empty-string/invalid-type decode, both-branch encode, and
  `EnvVarsCodec`'s `StringMap` composition all behave identically to before).
- Verified: `gofmt`/`go build`/`go test ./...` clean repo-wide; `staticcheck`/`gosec` clean
  on every touched package (`codex`, `examples/enum-union-sum`); all examples exit 0.

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
