# go-edge-models

Models an Azure IoT-Edge deployment manifest end-to-end with go-codex
codecs, alongside two standalone, reusable packages: a generic Docker
Engine API (create-options) modeling package, and a Docker Registry HTTP
API v2 / OCI Distribution Spec client/tool set. This tree is structured to
become its own importable library — every package below is designed to
be used independently, not just from `main.go`'s demo.

## Two top-level trees: `models/` vs `app/`

This tree is split into two top-level directories, matching a single
rule: **is this a declared, reusable CONTRACT, or a concrete
IMPLEMENTATION?**

- **[`models/`](models)** — declarative and REUSABLE by any application:
  domain structs, their `codex.Codec[T]` values, `api/rest.Route`
  declarations, and `api/mcp` tool declarations. Pure data — zero
  `*http.Client`, zero I/O, zero auth flow. Safe to import standalone to
  build a *different* HTTP client, generate an OpenAPI/AsyncAPI spec, or
  expose these routes through a different MCP server.
- **[`app/`](app)** — the concrete IMPLEMENTATIONS/applications built on
  top of `models/`'s declared contracts: HTTP client functions, the
  registry's Bearer-token auth flow, and MCP tool handler bindings. It is
  entirely OK for an `app/` package to have its OWN internal models/codecs
  that are useful only to that implementation (e.g. `app/registry`'s
  manifest-list resolution helpers) — those stay unexported/package-local
  precisely because they are NOT part of the reusable contract.

The dependency direction is ALWAYS `app/` → `models/`, never the reverse —
`models/` packages never import anything from `app/`. This is the same
"declare, then adapt/implement" split go-codex itself uses everywhere
(`api/rest` declares, `adapters/nethttp` implements) — applied locally to
this example tree.

## Package map

| Package | What it models | Depends on |
|---|---|---|
| [`models/azure/iothub/`](models/azure/iothub) | The PURE WIRE FORMAT of the GENERIC Azure IoT Hub device-twin spec (https://learn.microsoft.com/en-us/azure/iot-edge/module-edgeagent-edgehub) — Go package `iothub`, nested under `models/azure` (zero knowledge of, and zero dependency on, `models/iotedge`'s own layering strategy — mirrors `models/docker`'s "pure wire spec" convention). Models BOTH of Azure's own document shapes: `BaseDeployment` (the full, nested priority-0 base deployment) and `LayeredDeployment` (the flat-dotted-key layered overlay, `"properties.desired.modules.<name>"` via `codex.Map`) — plus `ModuleConfig`, `ModuleSettings`, `EnvVars` (string/int/float union), `SystemModuleConfig`/`SystemModules`, `Route`/`RouteTarget`, `Runtime`/`RegistryCredential`, `StoreAndForwardConfiguration`, `SchemaVersion`. `FlattenEnvVars(vars EnvVars) docker.Env` is a ONE-DIRECTION mapper (iothub → docker only) that formats each typed string/int/float `EnvVarValue` as a flat "KEY=VALUE" `docker.EnvVar` — no reverse mapper (would require guessing the original value's type from a flat string). Plain structs + `codex.Codec[T]` ONLY — no file I/O, no `ports.File`/`ports.Dir`, no derived/reduced views; see `models/iotedge` for everything OUR OWN layering builds on top of this spec. | `models/docker` (for `ModuleSettings.CreateOptions` and `FlattenEnvVars`'s return type) |
| [`models/iotedge/deviceconfig/`](models/iotedge/deviceconfig) | The PURE WIRE FORMAT of one device's device-specific config file — Go package `deviceconfig`, nested under `models/iotedge`. A real device config file IS a **patch** over its use case's own layered deployment: `Patch{EdgeAgent map[string]any, EdgeHub map[iothub.RouteName]iothub.Route}` — `EdgeAgent` keys are bare dotted paths reaching to ARBITRARY depth inside a module (whole-module, one env var, one settings field, ...); `EdgeHub` keys are whole-route add/override. `PatchCodec` is HAND-ROLLED (dynamic keys); `EmptyPatchError` signals a no-op patch. Deliberately has NO merge logic (see `finaldeviceconfig`). | `models/azure/iothub` (a few field-level codecs only) |
| [`models/iotedge/finaldeviceconfig/`](models/iotedge/finaldeviceconfig) | The DERIVED operation that layers a use case's own `iothub.LayeredDeployment` and one device's `deviceconfig.Patch` onto the GLOBAL `iothub.BaseDeployment`: `Merge(base, template, patch) (iothub.BaseDeployment, error)` — deep-merges the patch (creating new modules/routes as needed, overwrite/add only, no deletion), then re-validates the result through `iothub.BaseDeploymentCodec`. Kept as its own package since it depends on BOTH `azure/iothub` and `deviceconfig` at once — a dependency shape neither may take on itself. | `models/azure/iothub`, `models/iotedge/deviceconfig` |
| [`models/iotedge/usecase/`](models/iotedge/usecase) | Everything DERIVED/CONSTRUCTED for a "use case" AND the devices nested under it — never wire formats themselves. `Name`/`DeviceID`/`BasePath` (named string types, validated via `NameCodec`/`DeviceIDCodec`/`BasePathCodec`, constructed via `NewName`/`NewDeviceID`/`NewBasePath`) are this package's typed vocabulary, used throughout instead of bare `string` — `BasePath` additionally canonicalizes via `filepath.Clean` on decode and carries its OWN field description (single source of truth, reused as-is by `modulesummary.BasePathField` as an MCP tool field's LLM-facing description). The full use-case model/composition — `NewBaselineFile(basePath BasePath) ports.File[iothub.BaseDeployment]` (the SINGLE GLOBAL `"{basePath}/baseline/baseline.json"` file port, no template variables), `NewFile(basePath BasePath) ports.File[iothub.LayeredDeployment]` (a declared, TEMPLATED file port over `"{basePath}/usecases/{usecase_name}.json"`), `NewDir`/`ListNames` (returns `[]Name`), and the domain composition `UseCase`/`Read`/`Write` (which also assembles every nested `DeviceConfig`) — is consolidated in `usecase.go`; the device-level analogue (`NewDeviceFile` wrapping `deviceconfig.Patch`, `NewDeviceDir`/`ListDeviceIDs` (returns `[]DeviceID`), `DeviceConfig`/`ReadDeviceConfig`/`WriteDeviceConfig`, `DeviceConfig.Merge(base, template)` — the "one call" convenience delegating to `finaldeviceconfig.Merge` — and `ReadEffective(basePath, useCaseName, deviceID, opts)`, combining `NewBaselineFile.Read`+`NewFile.Read`+`ReadDeviceConfig`+`DeviceConfig.Merge` into ONE call, tolerant of a device with no config file yet — that device's effective config is simply baseline+template) lives in `device.go`. Pure declarative package — no I/O (the file port's own construction is free; only calling `.Read`/`ports.PatchEncoded` on it touches disk, done from `app/iotedge`). | `models/azure/iothub`, `models/iotedge/deviceconfig`, `models/iotedge/finaldeviceconfig` |
| [`models/iotedge/modulesummary/`](models/iotedge/modulesummary) | `Summary`/`NewSummary` (a reduced, read-only module view built from an `iothub.ModuleConfig`: image, host-mapped `PortBindings`, `Binds`, status, restart policy) plus the declared `ReadReq`/`ReadTool` MCP contract for reading it. `ReadReq` fields are typed `usecase.BasePath`/`usecase.Name`/`usecase.DeviceID` (via shared `targetfields.go` builders — `DeviceIDField` uses its own encode-permissive codec so an unset `DeviceID` round-trips through `codex.Struct`'s always-encode-every-field behavior); `DeviceID` is OPTIONAL — set, returns that device's ACTUAL configured summary (baseline + template + device config, merged); empty, returns the use case template's own summary. Pure declarative package — no I/O. | `models/azure/iothub`, `models/docker`, `models/iotedge/usecase` |
| [`models/iotedge/updatemoduleimage/`](models/iotedge/updatemoduleimage) | `Req`/`Tool` — the declared MCP contract for updating one module's container image (input is a plain `ImageURL` string, parsed server-side; output is the module's UPDATED `modulesummary.Summary`). `Req`'s `BasePath`/`UseCaseName`/`DeviceID` fields reuse `modulesummary`'s shared field builders, typed `usecase.BasePath`/`usecase.Name`/`usecase.DeviceID`; `DeviceID` is OPTIONAL — set, the update is written to ONLY that device's own config; empty, it patches the use case template. Kept separate from `modulesummary`, mirroring `modulepatch`'s existing separateness from `azure/iothub`. Pure declarative package — no I/O. | `models/azure/iothub`, `models/iotedge/modulesummary`, `models/iotedge/usecase` |
| [`models/iotedge/modulepatch/`](models/iotedge/modulepatch) | `FieldsPatch` — a GENERAL multi-field patch for one module (any subset of `ModuleName`/`Image`/`CreateOptions`/`Env`/`Type`/`Status`/`RestartPolicy`/`Version`) that encodes directly into the manifest's full nested shape (`modulesContent` → `$edgeAgent` → `<key>` → ...), reusing `azure/iothub`'s own exported field codecs. `FieldsPatchCodec` is a HAND-ROLLED `codex.Codec[T]` (not `codex.Struct`, whose `Encode` always writes every declared field — see its own doc comment) so only the fields actually set are included, leaving everything else — on that module and elsewhere in the manifest — untouched. `FieldsBodyCodec` (`FieldsPatch`'s own fields, WITHOUT the outer module-name wrapping) is EXPORTED specifically to bridge this package's TEMPLATE-level typed patches to `models/iotedge/deviceconfig`'s DEVICE-level raw patches (see `app/iotedge.PatchDeviceModule`) — reusing the SAME validation at both altitudes. `NonEmptyFieldsPatch` is the standalone "at least one field is set" guard for direct `FieldsBodyCodec` callers. `EmptyPatchError` (structured, `slog.LogValuer`, deliberately NOT stutter-stripped) signals a caller built a patch with nothing set. `NewUpdateModuleImage(moduleName, image)` is a named smart constructor for the single most common patch operation, validated via `FieldsPatchCodec.New`. Pure declarative package — no I/O. Depends ONLY on `models/azure/iothub` (a sibling package tree), never on the parent `models/iotedge` directory itself. | `models/azure/iothub` |
| [`models/docker/`](models/docker) | The shared "working-with-Docker-containers" DOMAIN package: `Image` (parsed `Name`/`Tag`/`Digest`, with `ImageCodec` for the wire string ↔ struct round trip and a `Stringer` for ergonomic printing), `Tag`/`Digest` named domain types (validated by a package-local `tagConstraint` and by core `validate.Digest` respectively — reused directly by `models/docker/registry`'s `TagsList.Tags`/`ManifestMetadata.Image.Digest`), `EnvVar`/`Env` (Docker's real create-options `"Env":["KEY=VALUE",...]` field, with `EnvCodec` for the wire ↔ struct round trip), plus generic Docker Engine API create-options modeling: `Port`, `Bind`, `Ulimit`, `Healthcheck`, `HostConfig`, `CreateOptions`. Also models Docker's own CLI-flag conventions used by MULTIPLE consumers (not just Compose): `MemBytesCodec`/`ParseMemBytes`/`FormatMemBytes` (`docker run --memory 512m`-style byte-size strings), `CLIDurationCodec`/`HealthcheckCLICodec` (`docker run --health-interval=30s`-style duration strings — an alternate wire form of the SAME `Healthcheck` struct), and `ParsePortMapping` (`docker run -p 8080:80/udp`-style short-syntax parsing). `TagRank`/`FilterTags`/`WithSort`/`WithLimit`/etc. are thin, `Tag`-typed re-exports of `models/versioning` (see below) — the actual classification/sort logic is domain-agnostic and lives there. Zero dependency on `iotedge` or Compose — reusable standalone for any Docker create-options tooling. | `models/versioning` (tag sort/filter delegation) |
| [`models/docker/dockercompose/`](models/docker/dockercompose) | The Docker Compose FILE FORMAT itself — `Project`/`ServiceName`/`ProjectCodec` (a project's named services map) and `Service`/`ServiceCodec` (image, `build:` presence, `ports:` decoded DIRECTLY into `[]docker.PortMapping` via `docker.PortMappingCodec`, `volumes:` reusing `docker.Bind` directly, `environment:` list-form reusing `docker.Env` directly, `command`/`entrypoint`, `restart`, `healthcheck` decoded DIRECTLY into `docker.Healthcheck` via `HealthcheckFromComposeCodec`, `mem_limit`/`mem_reservation` reusing `docker.MemBytesCodec`, and `ulimits` decoded DIRECTLY into `[]docker.Ulimit` via `UlimitsCodec`). **`CreateOptionsFromServiceCodec`** is a `Codec[docker.CreateOptions]` backed directly by `ServiceCodec`'s own wire shape (built via `codex.MapCodecValidated`) — the genuine "Docker ↔ docker-compose" mapping; `Service.CreateOptionsFor()`/`ServiceFromCreateOptions()` are thin wrappers around it — zero orchestrator (IoT Edge, Kubernetes, ...) vocabulary whatsoever. A malformed `ports:` entry now fails `ServiceCodec.Decode` itself, matching every other field's already-strict behavior. `codex.Struct`'s forward-compatible decode means unmodeled Compose keys (`networks:`, `depends_on:`, `deploy:`, ...) are silently ignored, not errors. | `models/docker` |
| [`models/versioning/`](models/versioning) | Domain-agnostic classification/ordering of version-like strings — generalized out of `models/docker`'s tag-sorting once it became clear the same "strict semver, semver-like, or opaque" problem applies to any versioned identifier (Helm charts, git tags, npm packages, ...). `Version`/`VersionCodec` (a `codex.UntaggedUnion` over `validate.SemVer`/`validate.SemVerLike`), `Parse[T ~string]`, `Compare`, `Filter[T ~string]` (pure, no I/O — works directly on `docker.Tag` or any other named string type, no conversion needed). See its own README.md for the full design, usage examples, and the "version-order, not real chronological order" caveat. Self-contained enough to plausibly become its own standalone library one day. | *(none)* |
| [`models/iotedge/fromcompose/`](models/iotedge/fromcompose) | A BIDIRECTIONAL converter between a `dockercompose.Project` and a SCAFFOLD `iothub.LayeredDeployment` use-case template — the ONLY package depending on BOTH `dockercompose` AND `azure/iothub` at once (mirrors `finaldeviceconfig`'s own "depends on both wire formats" shape). The actual VALUE transformation is ONE declaration, `ModuleConfigFromServiceCodec = codex.MapCodecValidated(dockercompose.ServiceCodec, iothub.ModuleConfigCodec, ...)` — since `MapCodecValidated` always builds BOTH Encode and Decode, this single `codex.Codec[iothub.ModuleConfig]` works directly with `format.YAML`/`format.JSON` in EITHER direction, with `iothub.ModuleConfigCodec`'s own `Refine` constraints enforced automatically. `ServicesToModulesCodec` extends the same idea to the whole project via `codex.Map[ModuleName, ModuleConfig]`. `ConvertService`/`ConvertProject` (forward) and `ConvertModuleConfig`/`ConvertDeployment` (reverse) route through these codecs; name sanitization, placeholder-image policy, and restart-policy mapping stay hand-written (genuinely IoT-Edge-specific concerns), with Warnings computed OUTSIDE the pure codec by re-deriving the same facts. `$edgeHub` routes are ALWAYS EMPTY going forward and have nothing to reverse onto going back. Conversion ALWAYS SUCCEEDS in either direction for a syntactically valid input — every lossy/approximated decision is a `Warning`, never an error. | `models/docker/dockercompose`, `models/azure/iothub` |
| [`models/docker/registry/`](models/docker/registry) | The DECLARED, REUSABLE contract for the Docker Registry HTTP API v2 / OCI Distribution Spec: `PingRoute`/`GetTagsRoute`/`GetManifestRoute` (plain `rest.Route` values, SHARED across every registry — the wire format is genuinely identical) and domain structs/codecs (`ImageRef`, `TagsList`, `GetTagsReq`, `GetManifestReq`, `GetImageMetadataReq`, `ManifestMetadata`), plus `GetTagsTool`/`GetImageMetadataTool` (declared, unregistered `api/mcp` tool contracts). Pure data — no `*http.Client`, no auth flow; safe to import standalone to build a different client/server. ALL auth-related concerns (Credentials, Bearer/Basic header codecs, the "bearerAuth" scheme) live in the sibling `auth` subpackage instead, not here. `ImageRef.ToImage()`/`ImageRefFromImage()` map to/from `docker.Image`. `GetImageMetadataReq`/`ManifestMetadata` (unlike `GetTagsReq`/`GetManifestReq`) are NOT one route's Req/Resp types — `GetImageMetadata` is a multi-call client-side orchestration, so its own shape is declared via a plain `codex.Struct`-based codec pair instead of a `rest.Route`. | `models/docker`, `models/util` (manifest/platform wire types), `models/docker/registry/auth` (Bearer header codec) |
| [`models/docker/registry/auth/`](models/docker/registry/auth) | PUBLIC, SINGLE HOME for every auth-related concern: `Credentials` (the shared Username/Password base type EVERY other Credentials type in this module — `generic.Credentials`, `dockerhub.Credentials`, `ghcr.Credentials`, `mcr.Credentials` — is built from, directly or via a distinct named type over it) + `CredentialsCodec`/`NewCredentials`, the Bearer/Basic Authorization-header codecs (`BearerTokenCodec`/`BasicAuthCodec` — the latter's input/output type IS `Credentials` itself), the token-USE scheme declaration (`BearerAuthScheme`/`BearerCredential`/`BearerAuthDeclaration` — migrated here from `models/docker/registry`'s own `security.go`), the WWW-Authenticate `Challenge`/`ChallengeCodec`, the Docker auth-scope `DockerScope`/`DockerScopeCodec`, and the registry token-endpoint `TokenResponse`. Migrated from what used to be a true Go internal package specifically so this auth-flow plumbing is officially reusable — a caller building their OWN client against a registry this module doesn't model concretely can reuse it directly. | *(none — pure wire-format codecs)* |
| [`models/docker/registry/generic/`](models/docker/registry/generic) | The any-OCI-registry FALLBACK's own `Credentials`/`RegistryCredentials` — `Credentials` is a plain ALIAS of `auth.Credentials` (no extra constraint: the generic fallback has no single known registry to compile-time-guard against), used ONLY by `app/registry`'s `NewGenericClient`/`WithCredentials`/`WithCredentialsByRegistry`, for any registry host OTHER than the 3 known ones below. A sibling of `dockerhub`/`ghcr`/`mcr` — the layout is fully symmetric. | `models/docker/registry/auth`, `models/docker/registry/{dockerhub,ghcr,mcr}` (RegistryHost consts only) |
| [`models/docker/registry/dockerhub/`](models/docker/registry/dockerhub), [`.../ghcr/`](models/docker/registry/ghcr), [`.../mcr/`](models/docker/registry/mcr) | Registry-SPECIFIC models — each declares its own `RegistryHost` constant and its own DISTINCT named `Credentials` type (`type Credentials auth.Credentials` — passing GHCR credentials to a Docker Hub client is a compile error, not a silent misconfiguration) + `CredentialsCodec` + `NewCredentials` smart constructor. The underlying shape/base validation is written ONCE in `auth.Credentials`/`auth.CredentialsCodec` — each package's own `CredentialsCodec` is a thin wrapper composed on top of it (`c.MapCodecSafe(auth.CredentialsCodec, ...)`), never a redeclared struct. `ghcr.CredentialsCodec` layers an EXTRA "Username required" constraint via `.RefineFunc(...)` (GHCR's token exchange always needs a real GitHub username); `dockerhub`/`mcr` leave Username optional, matching `auth.CredentialsCodec`'s own base shape. | `models/docker/registry/auth` |
| [`models/util/`](models/util) | Generic OCI Distribution Spec wire plumbing that is NOT auth-specific — the manifest/manifest-list envelope (`ManifestEnvelope`/`ManifestDescriptor`/`SingleManifestWire`/`ManifestListWire`, `DigestCodec`) and the "os/arch" platform selector (`PlatformSelector`/`PlatformSelectorCodec`). A top-level, PUBLIC package (sibling to `models/docker`/`models/azure`/`models/iotedge`/`models/versioning`) — migrated from what used to be a true Go internal package. | *(none — pure wire-format codecs)* |
| [`app/registry/`](app/registry) | The CONCRETE IMPLEMENTATION built on `models/docker/registry`'s contract: the `Client` interface, `NewGenericClient` (a real, persistent, per-registry-host-bound `*rest.Client` — the FALLBACK for any OCI-compliant registry not specifically modeled by the 3 self-contained sibling packages), and `Dispatcher`/`NewDispatcher` — THE recommended, registry-agnostic entry point, bundling `GetTags`/`GetTagsFiltered`/`GetImageMetadata` plus ready-made MCP tool-handler constructors into ONE constructed object that picks the right CONCRETE per-registry client per call from an image URL (falling back to `NewGenericClient` for any other registry). `Dispatcher` safely imports `dockerhub`/`ghcr`/`mcr` with NO import cycle, since those are now FULLY SELF-CONTAINED (see below) rather than embedding a shared base. | `models/docker/registry`, `models/docker`, `app/registry/internal`, `app/registry/dockerhub`, `app/registry/ghcr`, `app/registry/mcr` |
| [`app/registry/dockerhub/`](app/registry/dockerhub), [`.../ghcr/`](app/registry/ghcr), [`.../mcr/`](app/registry/mcr) | FULLY SELF-CONTAINED registry clients — one per KNOWN registry (Docker Hub/GHCR/MCR). Each builds its OWN persistent `*rest.Client` + its OWN `*internal.Authenticator` directly against its own `models/docker/registry/<name>` models (RegistryHost + typed Credentials) — NOT embedding/wrapping a shared `app/registry.Client` (an earlier round of this codebase did; this round deliberately reversed that). Each exposes its own `New(httpClient, opts...) *Client` + `WithCredentials`/`WithObserver` (per-registry TYPED options — a `dockerhub.Option` is a DIFFERENT Go type from `ghcr.Option`). Depends on `app/registry/internal` for shared MECHANICS, NOT on `app/registry` itself — specifically so `app/registry` can safely import these 3 back for `Dispatcher` without a cycle. | `app/registry/internal`, `models/docker/registry`, `models/docker/registry/<name>`, `models/docker/registry/auth`, `models/util` |
| [`app/registry/internal/`](app/registry/internal) | The MECHANICS every self-contained client (and `app/registry`'s own generic fallback) is BUILT ON — the Bearer-token challenge/token-exchange auth flow, a per-repository token cache (`Authenticator`), the shared `BoundClientMiddleware` constructor (`BearerAuthClientMW`), and manifest-list resolution logic (`PlatformMatches`/`FormatPlatformSelector` -- each client calls its own `*rest.Client.Call(ctx, manifestRoute, ...)` directly for the actual fetch, a single line not worth a shared wrapper). A true Go internal package — importable only from code rooted at `app/registry` (itself and its subpackages) — so "fully self-contained client" means "owns its own construction," not "duplicates ~150 lines of auth-flow/manifest-resolution logic" 3 times over. `RegistryAuthError`/`RegistryAuthChallengeError`/`NestedManifestListError`/`PlatformNotFoundError` are defined HERE; every consuming package re-exports each as a PUBLIC type alias, so a caller can `errors.As` on the SAME underlying type regardless of which concrete Client's error they caught. | `models/docker/registry`, `models/docker/registry/auth`, `models/util` |
| [`app/iotedge/`](app/iotedge) | The CONCRETE IMPLEMENTATION built on the `models/iotedge` subpackages' contracts: `ReadUseCase(basePath usecase.BasePath, useCaseName usecase.Name, opts)` (reads useCaseName's deployment manifest under basePath via `usecase.NewFile` — named distinctly from `usecase.Read`, which additionally assembles every nested device), `PatchUseCaseModule`/`UpdateUseCaseModuleImage` (patch the use case's shared TEMPLATE), and their DEVICE-scoped analogues `PatchDeviceModule`/`UpdateDeviceModuleImage` (patch ONE device's OWN config file only, taking a `usecase.DeviceID` too — isolated and reversible, template and every other device untouched; bridges `modulepatch.FieldsBodyCodec`'s typed validation into `deviceconfig.Patch.EdgeAgent`, handling both "device already has a config file" via `ports.PatchEncoded`'s deep-merge and "device's first-ever override" via a direct write). `NewReadModuleSummaryToolHandler`/`NewUpdateModuleImageToolHandler` bind `modulesummary`'s/`updatemoduleimage`'s declared `ReadTool`/`Tool` — selecting template vs. device scope via the request's OPTIONAL `DeviceID` (delegating to `usecase.ReadEffective` for device-scoped reads) — sharing an unexported `readModuleSummary`/`effectiveManifest` helper, returning a `ModuleNotFoundError`, structured `slog.LogValuer`, when the named module is absent; the update handler checks the module exists BEFORE patching, so a nonexistent module fails cleanly instead of merging an incomplete entry. `ImportDockerComposeAsUseCase(basePath usecase.BasePath, useCaseName usecase.Name, composeFilePath, opts) ([]fromcompose.Warning, error)` reads a `docker-compose.yaml` from disk, converts it via `fromcompose.ConvertProject`, and writes the result as a new use-case template file via `usecase.NewFile`; its reverse, `ExportUseCaseAsDockerCompose(basePath usecase.BasePath, useCaseName usecase.Name, composeFilePath, opts) ([]fromcompose.Warning, error)`, reads a use case back via `ReadUseCase` and writes it out as Compose YAML via `fromcompose.ConvertDeployment` — the concrete file-I/O wrappers over `fromcompose`'s pure, in-memory bidirectional conversion. A caller always supplies a `usecase.BasePath` + `usecase.Name` per call; this package never assumes or hardcodes where a use case's manifest lives. `usecase.BasePath`/`usecase.Name`/`usecase.DeviceID` all flow end-to-end from an MCP tool request's wire strings straight through to every function here (the now-dead internal `usecase.NewName`/`usecase.NewDeviceID` conversions this package used to need have been removed) — exactly ONE string→typed conversion per identifier, at the outermost boundary. | `models/iotedge/usecase`, `models/iotedge/modulesummary`, `models/iotedge/updatemoduleimage`, `models/iotedge/modulepatch`, `models/iotedge/deviceconfig`, `models/iotedge/fromcompose`, `models/docker/dockercompose` |

Each declarative package follows the same internal convention: ONE FILE
PER CONCEPT — a concept's plain struct(s), any `validate.Constraint`
values it needs, and its `codex.Codec[T]` values (built via
`RequiredField`/`OptionalField`) all live together in that one file, so
understanding or changing one concept never requires jumping across
files. `models/docker`/`models/azure/iothub` split by DOMAIN
CONCEPT (e.g. `models/docker/image.go`,
`models/azure/iothub/envvars.go`);
`models/docker/registry` (routes + MCP tools, not just codecs) splits by
OPERATION instead (e.g. `gettags.go`, `getimagemetadata.go`) — each
operation's route, request/response types+codecs, and MCP tool
declaration all live together. `app/registry` mirrors that same
per-operation split for its implementation half. Every field-level codec
is still its own standalone, reusable value, not buried inline inside a
larger struct's codec — see each package's `doc.go` for its exact file
map.

**Construction:** types whose codec enforces a genuine constraint (not
just a bare literal) implement `codex.HasCodec[T]` (a one-line `Codec()`
method) and expose a hand-written `NewXxx(...)` smart constructor
wrapping `Codec.New` — e.g. `docker.NewImage`/`NewBind`/`NewUlimit`,
`registry.NewCredentials`/`NewImageRef`, `iothub.NewModuleSettings`,
`iothub.NewEnvVarValueString`/`NewEnvVarValueInt`/`NewEnvVarValueFloat`,
`modulepatch.NewUpdateModuleImage`. See [`docs/concepts/codec.md`](../../docs/concepts/codec.md)'s
`HasCodec[T]` section for the general pattern; types without a real
constraint (e.g. `docker.CreateOptions`, `iothub.ModuleConfig`) are
left as plain struct literals + `Validate` — no ceremony added for its own
sake.

## Quick usage

**Decode a real deployment manifest:**

```go
manifest, err := format.JSON(iothub.LayeredDeploymentCodec).Unmarshal(manifestJSON)
dashboard := manifest.ModulesContent.EdgeAgent["factory-dashboard"]
fmt.Println(dashboard.Settings.Image, dashboard.Status)
// dashboard.Settings.Image is a docker.Image (Name/Tag/Digest) — its
// Stringer prints it back as the same plain wire string ("ghcr.io/org/edge-web:2.0.0").
```

**Patch one module's image in-place on disk:**

```go
import (
    iotedgeapp "github.com/DaniDeer/go-codex/examples/go-edge-models/app/iotedge"
    "github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/usecase"
)

basePath, err := usecase.NewBasePath("/srv/edge-configs") // the ONE string->BasePath conversion
image := docker.Image{Name: "ghcr.io/org/edge-web", Tag: "2.0.0"}
err = iotedgeapp.UpdateUseCaseModuleImage(basePath, "usecase1", "factory-dashboard", image, ports.FileOptions{})
```

**Patch multiple fields at once (any subset of a module's fields):**

```go
status := iothub.Status("stopped")
restartPolicy := iothub.RestartPolicy("on-failure")
err := iotedgeapp.PatchUseCaseModule(basePath, "usecase1", modulepatch.FieldsPatch{
    ModuleName:    "factory-dashboard",
    Status:        &status,
    RestartPolicy: &restartPolicy,
}, ports.FileOptions{})
// Only status and restartPolicy change — image, env, and every other
// module are left untouched.
```

**Fetch tags and lean manifest metadata from any OCI-compliant registry —
construct ONE `Dispatcher`, same methods, only the image URL differs:**

```go
import (
    registry "github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker/registry"
    registryapp "github.com/DaniDeer/go-codex/examples/go-edge-models/app/registry"
    "github.com/DaniDeer/go-codex/examples/go-edge-models/app/registry/dockerhub"
    "github.com/DaniDeer/go-codex/examples/go-edge-models/app/registry/ghcr"
)

client := registryapp.NewDispatcher(http.DefaultClient)

tags, err := client.GetTags(ctx, "nodered/node-red")       // Docker Hub
tags, err  = client.GetTags(ctx, "ghcr.io/org/image")      // GHCR
tags, err  = client.GetTags(ctx, "mcr.microsoft.com/dotnet/runtime") // MCR

meta, err := client.GetImageMetadata(ctx,
    registry.GetImageMetadataReq{ImageURL: "alpine:latest"}) // Platform defaults to "linux/amd64"
fmt.Println(meta.Image, meta.TotalSizeBytes)

// Private repository requiring Basic auth at the token-exchange step
// (e.g. a private GHCR package — GitHub username + a read:packages PAT) —
// pass credentials at CONSTRUCTION time, via the TYPED option the target
// registry's OWN package exposes (a dockerhub.Option is a DIFFERENT Go
// type from a ghcr.Option — Dispatcher wraps each per-registry so it
// still reads as one constructor call): a Client represents one identity
// for its whole lifetime.
privateClient := registryapp.NewDispatcher(http.DefaultClient,
    registryapp.WithGHCROptions(ghcr.WithCredentials(ghcr.Credentials{
        Username: os.Getenv("GHCR_USER"), Password: os.Getenv("GHCR_PAT"),
    })))
tags, err = privateClient.GetTags(ctx, "ghcr.io/org/private-image")

// A Dispatcher working against MULTIPLE known registries: configure each
// one's own typed credentials once — GetTags/GetImageMetadata pick the
// right concrete client automatically based on each image URL's resolved
// registry host, falling back to the generic client (and its own
// generic.RegistryCredentials map) for any OTHER OCI-compliant registry.
multiClient := registryapp.NewDispatcher(http.DefaultClient,
    registryapp.WithDockerHubOptions(dockerhub.WithCredentials(dockerhub.Credentials{
        Username: "docker-user", Password: os.Getenv("DOCKERHUB_TOKEN"),
    })),
    registryapp.WithGHCROptions(ghcr.WithCredentials(ghcr.Credentials{
        Username: os.Getenv("GHCR_USER"), Password: os.Getenv("GHCR_PAT"),
    })),
    // MCR needs no auth for public images — omit its option entirely.
)
tags, err = multiClient.GetTags(ctx, "nodered/node-red")
tags, err  = multiClient.GetTags(ctx, "ghcr.io/org/private-image")
```

A caller who already knows the target registry (no image-URL resolution
needed) can instead construct one of the FULLY SELF-CONTAINED clients
directly — `dockerhub.New(...)`/`ghcr.New(...)`/`mcr.New(...)` — or, for
any OTHER OCI-compliant registry, `app/registry.NewGenericClient`
directly. `Dispatcher` is built on top of exactly these 4, bundling them
behind one constructed object.

**Get the most recent N tags** — the plain OCI `tags/list` response is
unordered and carries no timestamps, so `docker.FilterTags` orders tags by
highest-version-first (strict semver, then "semver-like" tags such as
`3.1-debian`/`18.04`, then opaque tags like `latest` last), NOT real
chronological recency — see `docker.SortByVersionDesc`'s doc comment:

```go
tags, err := client.GetTagsFiltered(ctx, "nodered/node-red",
    []docker.FilterTagsOpt{docker.WithLimit(5)}) // 5 highest-version tags
// tags.Tags is already sorted/limited — no further client-side work needed.
```

The `get_tags` MCP tool exposes the same mechanism to an LLM via two
optional input fields: `limit` (int) and `sort`
(`"version_desc"`|`"alphabetical"`|`"none"`).

**Expose GetTags/GetImageMetadata as MCP tools** — the declared contract
(`models/docker/registry`) and the concrete binding (`Dispatcher`) stay
separate, so registering the tool never requires touching HTTP-client
code:

```go
tool, err := registry.GetTagsTool.Register(mcpBuilder)
_, handlerFn := mcpgo.ToolHandler(tool,
    client.NewGetTagsToolHandler(),
    mcpgo.Options{})
```

**Observer integration.** `app/registry` builds on `rest.Client.Call`
internally, so it inherits [go-codex's context-based Observer
default](../../docs/features/observer.md#per-layer-behavior) for free — no
option needed:

```go
// The FREE way: attach an observer to ctx once; every HTTP call this
// package makes (auth-realm Ping/token exchange included) is observed
// automatically.
ctx = stats.WithObserver(ctx, stats.NewLoggingObserver(slog.Default()))
tags, err := client.GetTags(ctx, "nodered/node-red")

// The EXPLICIT way: registryapp.WithGenericOptions(registryapp.WithObserver(...))
// becomes the generic-fallback client's fixed Observer for its whole
// lifetime (construction-time, like credentials above) — each
// self-contained sibling takes its OWN WithObserver via its own
// WithDockerHubOptions/WithGHCROptions/WithMCROptions wrapper the same way.
observedClient := registryapp.NewDispatcher(http.DefaultClient,
    registryapp.WithGenericOptions(registryapp.WithObserver(stats.NewLoggingObserver(slog.Default()))))
tags, err = observedClient.GetTags(ctx, "nodered/node-red")
```

**GHCR requires a real username, not just a token.** GHCR's token-exchange
step is standard HTTP Basic auth (`username:PAT`) — unlike some registries,
there is no bearer-only/username-less mode, even with a `GITHUB_TOKEN` or
a GitHub App installation token (neither is accepted for GHCR push/pull
outside a GitHub Actions workflow). If you want to rotate *only* the
token in an unattended service (e.g. an MCP server) without also touching
a username each time, use a dedicated **bot/machine GitHub account**: its
username is a fixed value you set once via `GHCR_USER` (rarely changes),
and only its PAT (`GHCR_PAT`) — the actual expiring secret — needs
rotating. Give the bot account's classic PAT `read:packages` (+ `repo` for
private repositories) scope.

`GetImageMetadata` transparently resolves a multi-arch manifest list to a
single platform — no list/index shape ever reaches the caller.
`app/registry` (the generic fallback + `Dispatcher`) is registry-agnostic
by design — verified end-to-end against real Docker Hub, GHCR, and MCR
(see Testing below). `app/registry/dockerhub`/`.../ghcr`/`.../mcr` ARE
per-registry, FULLY SELF-CONTAINED clients — the seam for registry-
specific behavior (e.g. `ghcr.Credentials` requiring a non-empty Username,
unlike the other two) — but any future WIRE-SHAPE difference still gets
modeled as an additional field/variant in the existing shared codecs
(`models/docker/registry`, `models/util`, `models/docker/registry/auth`),
never as registry-name branching inside the declarative contract itself.

## Running the example

```sh
go run ./examples/go-edge-models
```

Decodes the embedded `examples/usecases/usecase1.json` reference manifest
and `examples/devices/usecase1/sensor-1.json` device manifest (mirroring
the real `<basePath>/usecases/<usecase_name>.json` +
`<basePath>/devices/<usecase_name>/<device_id>.json` filesystem layout),
patches one module's image, lists/reads the device via
`usecase.ListDeviceIDs`/`ReadDeviceConfig`/`Read`, and demonstrates
`app/registry` against two local `httptest` mock servers (a registry host
+ a separate auth-realm host).

## Testing

```sh
# Unit tests — offline, deterministic, run by `just check` / CI.
go test ./examples/go-edge-models/...

# Integration test — hits the REAL Docker Hub registry, opt-in only.
go test -tags=integration ./examples/go-edge-models/app/registry/...
```

The integration test (`app/registry/registry_integration_test.go`) is
gated behind the `integration` build tag specifically so it never runs as
part of a normal build/test/CI pass — it requires network access and talks
to `registry-1.docker.io`/`auth.docker.io`, `ghcr.io`, and
`mcr.microsoft.com` directly (proving `app/registry`'s registry-agnostic
design against all three real registries in one run).
