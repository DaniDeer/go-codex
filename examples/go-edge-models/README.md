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
| [`models/iotedge/`](models/iotedge) | Azure IoT-Edge deployment manifest: `ModuleConfig`, `ModuleSettings`, `EnvVars` (string/int/float union), and `ModuleName`/`Modules`/`DeploymentManifest` — dotted-key extraction (`"properties.desired.modules.<name>"`) via `codex.Map`. `FlattenEnvVars(vars EnvVars) docker.Env` is a ONE-DIRECTION mapper (iotedge → docker only) that formats each typed string/int/float `EnvVarValue` as a flat "KEY=VALUE" `docker.EnvVar` — no reverse mapper (would require guessing the original value's type from a flat string). Also `NewConfigFile(path) ports.File[DeploymentManifest]` — a declared file-port CONSTRUCTOR (not a static value: a manifest's path is fully caller-supplied, and a single `{var}` path template cannot span the "/" segments of an arbitrary file path — see its own doc comment) — and `ModuleSummary`/`NewModuleSummary` (a reduced, read-only module view: image, host-mapped `PortBindings`, `Binds`, status, restart policy) plus the declared `ReadModuleSummaryTool`/`UpdateModuleImageTool` MCP contracts (the latter's input is a plain `ImageURL` string, parsed server-side, and its output is the module's UPDATED `ModuleSummary`). Pure declarative package — no I/O (the file port's own construction is free; only calling `.Read`/`ports.PatchEncoded` on it touches disk, done from `app/iotedge`). | `models/docker` (for `ModuleSettings.CreateOptions`/`ModuleSummary` and `FlattenEnvVars`'s return type) |
| [`models/iotedge/modulepatch/`](models/iotedge/modulepatch) | `ModuleFieldsPatch` — a GENERAL multi-field patch for one module (any subset of `ModuleName`/`Image`/`CreateOptions`/`Env`/`Type`/`Status`/`RestartPolicy`/`Version`) that encodes directly into the manifest's full nested shape (`modulesContent` → `$edgeAgent` → `<key>` → ...), reusing `iotedge`'s own exported field codecs. `ModuleFieldsPatchCodec` is a HAND-ROLLED `codex.Codec[T]` (not `codex.Struct`, whose `Encode` always writes every declared field — see its own doc comment) so only the fields actually set are included, leaving everything else — on that module and elsewhere in the manifest — untouched. `EmptyPatchError` (structured, `slog.LogValuer`) signals a caller built a patch with nothing set. `NewUpdateModuleImagePatch(moduleName, image)` is a named smart constructor for the single most common patch operation, validated via `ModuleFieldsPatchCodec.New`. Pure declarative package — no I/O. | `models/iotedge` |
| [`models/docker/`](models/docker) | The shared "working-with-Docker-containers" DOMAIN package: `Image` (parsed `Name`/`Tag`/`Digest`, with `ImageCodec` for the wire string ↔ struct round trip and a `Stringer` for ergonomic printing), `Tag`/`Digest` named domain types (validated by a package-local `tagConstraint` and by core `validate.Digest` respectively — reused directly by `models/docker/registry`'s `TagsList.Tags`/`ManifestMetadata.Image.Digest`), `EnvVar`/`Env` (Docker's real create-options `"Env":["KEY=VALUE",...]` field, with `EnvCodec` for the wire ↔ struct round trip), plus generic Docker Engine API create-options modeling: `Port`, `Bind`, `Ulimit`, `Healthcheck`, `HostConfig`, `CreateOptions`. `TagRank`/`FilterTags`/`WithSort`/`WithLimit`/etc. are thin, `Tag`-typed re-exports of `models/versioning` (see below) — the actual classification/sort logic is domain-agnostic and lives there. Zero dependency on `iotedge` — reusable standalone for Docker Compose or any other Docker create-options tooling. | `models/versioning` (tag sort/filter delegation) |
| [`models/versioning/`](models/versioning) | Domain-agnostic classification/ordering of version-like strings — generalized out of `models/docker`'s tag-sorting once it became clear the same "strict semver, semver-like, or opaque" problem applies to any versioned identifier (Helm charts, git tags, npm packages, ...). `Version`/`VersionCodec` (a `codex.UntaggedUnion` over `validate.SemVer`/`validate.SemVerLike`), `Parse[T ~string]`, `Compare`, `Filter[T ~string]` (pure, no I/O — works directly on `docker.Tag` or any other named string type, no conversion needed). See its own README.md for the full design, usage examples, and the "version-order, not real chronological order" caveat. Self-contained enough to plausibly become its own standalone library one day. | *(none)* |
| [`models/docker/registry/`](models/docker/registry) | The DECLARED, REUSABLE contract for the Docker Registry HTTP API v2 / OCI Distribution Spec: `PingRoute`/`GetTagsRoute`/`GetManifestRoute` (plain `rest.Route` values), domain structs/codecs (`ImageRef`, `TagsList`, `GetTagsReq`, `GetManifestReq`, `GetImageMetadataReq`, `ManifestMetadata`, `Credentials`), and `GetTagsTool`/`GetImageMetadataTool` (declared, unregistered `api/mcp` tool contracts). Pure data — no `*http.Client`, no auth flow; safe to import standalone to build a different client/server. `ImageRef.ToImage()`/`ImageRefFromImage()` map to/from `docker.Image` — `ImageRef`'s registry-host-split shape is genuinely different (needed to build a specific registry's HTTP base URL), so it keeps its own codec rather than reusing `docker.ImageCodec` directly. `TagsList.Tags []docker.Tag` reuses the shared domain type directly (same concept, identical shape — no mapper needed). `GetImageMetadataReq`/`ManifestMetadata` (unlike `GetTagsReq`/`GetManifestReq`) are NOT one route's Req/Resp types — `GetImageMetadata` (its concrete implementation lives in `app/registry`) is a multi-call client-side orchestration, so its own shape is declared via a plain `codex.Struct`-based codec pair instead of a `rest.Route`. Files are organized ONE PER OPERATION (`ping.go`, `gettags.go`, `getimagemetadata.go`, plus shared `imageref.go`/`credentials.go`/`security.go`) — no `client.go`/`routes.go`/`mcptools.go` aggregator file. | `models/docker` (Image/Tag/Digest reuse plus the Image mapper) |
| [`app/registry/`](app/registry) | The CONCRETE IMPLEMENTATION built on `models/docker/registry`'s contract: `GetTags`/`GetImageMetadata` (the batteries-included client functions — transparent multi-arch manifest-list resolution, registry-agnostic per-call registry resolution), the entire Bearer/Basic auth-challenge flow (token exchange, credential injection) as package-private plumbing in `auth.go` a caller never constructs themselves, and `NewGetTagsToolHandler`/`NewGetImageMetadataToolHandler` (closures binding `models/docker/registry`'s declared MCP tools to these client functions — registry-agnostic, NOT an `adapters/mcprest` route bridge). Has its OWN internal models where useful for the implementation only (e.g. manifest-list resolution helpers, `NestedManifestListError`/`PlatformNotFoundError`) — these are deliberately NOT exported as part of any reusable contract. | `models/docker/registry`, `models/docker` |
| [`internal/registry/`](internal/registry) | Shared, generic OCI Distribution Spec / Docker Registry HTTP API v2 wire-format plumbing (manifest/manifest-list envelope types, WWW-Authenticate challenge, Docker auth-scope string, "os/arch" platform selector, and their codecs) — a true Go internal package, importable only by code rooted at `go-edge-models`. Lives one level above `models/docker/registry` and `app/registry` (its two importers, which are siblings, not a parent/child pair) specifically so BOTH can reach it. | *(none — pure wire-format codecs)* |
| [`app/iotedge/`](app/iotedge) | The CONCRETE IMPLEMENTATION built on `models/iotedge`'s contract: `ReadConfig` (reads a deployment manifest file via `models/iotedge.NewConfigFile`), `PatchModule` (applies a `modulepatch.ModuleFieldsPatch` — any subset of one module's fields), `UpdateModuleImage` (a thin convenience over `PatchModule` for the single most common case, validated via `modulepatch.NewUpdateModuleImagePatch`), `NewReadModuleSummaryToolHandler`/`NewUpdateModuleImageToolHandler` (bind `models/iotedge`'s declared `ReadModuleSummaryTool`/`UpdateModuleImageTool` — sharing an unexported `readModuleSummary` helper for "read manifest, find module, summarize" — returning a `ModuleNotFoundError`, structured `slog.LogValuer`, when the named module is absent; the update handler checks the module exists BEFORE patching, so a nonexistent module fails cleanly instead of merging an incomplete entry). A caller always supplies the concrete manifest file path per call; this package never assumes or hardcodes where a manifest lives. | `models/iotedge`, `models/iotedge/modulepatch` |

Each declarative package follows the same internal convention: ONE FILE
PER CONCEPT — a concept's plain struct(s), any `validate.Constraint`
values it needs, and its `codex.Codec[T]` values (built via
`RequiredField`/`OptionalField`) all live together in that one file, so
understanding or changing one concept never requires jumping across
files. `models/docker`/`models/iotedge` split by DOMAIN CONCEPT (e.g.
`models/docker/image.go`, `models/iotedge/envvars.go`);
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
`registry.NewCredentials`/`NewImageRef`, `iotedge.NewModuleSettings`,
`iotedge.NewEnvVarValueString`/`NewEnvVarValueInt`/`NewEnvVarValueFloat`,
`modulepatch.NewUpdateModuleImagePatch`. See [`docs/concepts/codec.md`](../../docs/concepts/codec.md)'s
`HasCodec[T]` section for the general pattern; types without a real
constraint (e.g. `docker.CreateOptions`, `iotedge.ModuleConfig`) are left
as plain struct literals + `Validate` — no ceremony added for its own
sake.

## Quick usage

**Decode a real deployment manifest:**

```go
manifest, err := format.JSON(iotedge.DeploymentManifestCodec).Unmarshal(manifestJSON)
dashboard := manifest.ModulesContent.EdgeAgent["factory-dashboard"]
fmt.Println(dashboard.Settings.Image, dashboard.Status)
// dashboard.Settings.Image is a docker.Image (Name/Tag/Digest) — its
// Stringer prints it back as the same plain wire string ("ghcr.io/org/edge-web:2.0.0").
```

**Patch one module's image in-place on disk:**

```go
import (
    iotedgeapp "github.com/DaniDeer/go-codex/examples/go-edge-models/app/iotedge"
)

image := docker.Image{Name: "ghcr.io/org/edge-web", Tag: "2.0.0"}
err := iotedgeapp.UpdateModuleImage(path, "factory-dashboard", image, ports.FileOptions{})
```

**Patch multiple fields at once (any subset of a module's fields):**

```go
status := iotedge.Status("stopped")
restartPolicy := iotedge.RestartPolicy("on-failure")
err := iotedgeapp.PatchModule(path, modulepatch.ModuleFieldsPatch{
    ModuleName:    "factory-dashboard",
    Status:        &status,
    RestartPolicy: &restartPolicy,
}, ports.FileOptions{})
// Only status and restartPolicy change — image, env, and every other
// module are left untouched.
```

**Fetch tags and lean manifest metadata from any OCI-compliant registry —
same functions, same routes, only the image URL differs:**

```go
import (
    registry "github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker/registry"
    registryapp "github.com/DaniDeer/go-codex/examples/go-edge-models/app/registry"
)

tags, err := registryapp.GetTags(ctx, http.DefaultClient, "nodered/node-red")       // Docker Hub
tags, err  = registryapp.GetTags(ctx, http.DefaultClient, "ghcr.io/org/image")      // GHCR
tags, err  = registryapp.GetTags(ctx, http.DefaultClient, "mcr.microsoft.com/dotnet/runtime") // MCR

meta, err := registryapp.GetImageMetadata(ctx, http.DefaultClient,
    registry.GetImageMetadataReq{ImageURL: "alpine:latest"}) // Platform defaults to "linux/amd64"
fmt.Println(meta.Image, meta.TotalSizeBytes)

// Private repository requiring Basic auth at the token-exchange step
// (e.g. a private GHCR package — GitHub username + a read:packages PAT):
tags, err = registryapp.GetTags(ctx, http.DefaultClient, "ghcr.io/org/private-image",
    registryapp.WithCredentials(registry.Credentials{Username: os.Getenv("GHCR_USER"), Password: os.Getenv("GHCR_PAT")}))

// A single call site working against MULTIPLE registries: declare ALL
// your registries' credentials once, GetTags/GetImageMetadata pick the
// right entry automatically based on each image URL's resolved registry
// host — the SAME options value is reused unchanged below.
byRegistry := registry.RegistryCredentials{
    "registry-1.docker.io": {Username: "docker-user", Password: os.Getenv("DOCKERHUB_TOKEN")},
    "ghcr.io":               {Username: os.Getenv("GHCR_USER"), Password: os.Getenv("GHCR_PAT")},
    "mcr.microsoft.com":     {},                              // MCR needs no auth — omit or leave zero.
}
opt := registryapp.WithCredentialsByRegistry(byRegistry)
tags, err = registryapp.GetTags(ctx, http.DefaultClient, "nodered/node-red", opt)
tags, err  = registryapp.GetTags(ctx, http.DefaultClient, "ghcr.io/org/private-image", opt)
```

**Get the most recent N tags** — the plain OCI `tags/list` response is
unordered and carries no timestamps, so `docker.FilterTags` orders tags by
highest-version-first (strict semver, then "semver-like" tags such as
`3.1-debian`/`18.04`, then opaque tags like `latest` last), NOT real
chronological recency — see `docker.SortByVersionDesc`'s doc comment:

```go
tags, err := registryapp.GetTagsFiltered(ctx, http.DefaultClient, "nodered/node-red",
    []docker.FilterTagsOpt{docker.WithLimit(5)}) // 5 highest-version tags
// tags.Tags is already sorted/limited — no further client-side work needed.
```

The `get_tags` MCP tool exposes the same mechanism to an LLM via two
optional input fields: `limit` (int) and `sort`
(`"version_desc"`|`"alphabetical"`|`"none"`).

**Expose GetTags/GetImageMetadata as MCP tools** — the declared contract
(`models/docker/registry`) and the concrete binding (`app/registry`) stay
separate, so registering the tool never requires touching HTTP-client
code:

```go
tool, err := registry.GetTagsTool.Register(mcpBuilder)
_, handlerFn := mcpgo.ToolHandler(tool,
    registryapp.NewGetTagsToolHandler(http.DefaultClient, registryapp.WithObserver(obs)),
    mcpgo.Options{})
```

**Observer integration.** `app/registry` builds on `nethttp.Call`/
`CallHandle` internally, so it inherits [go-codex's context-based Observer
default](../../docs/features/observer.md#per-layer-behavior) for free — no
option needed:

```go
// The FREE way: attach an observer to ctx once; every HTTP call this
// package makes (auth-realm Ping/token exchange included) is observed
// automatically.
ctx = stats.WithObserver(ctx, stats.NewLoggingObserver(slog.Default()))
tags, err := registryapp.GetTags(ctx, http.DefaultClient, "nodered/node-red")

// The EXPLICIT way: registryapp.WithObserver overrides ctx for just this
// one call (same "explicit wins over context" precedence as
// nethttp.CallOptions.Observer).
tags, err = registryapp.GetTags(ctx, http.DefaultClient, "nodered/node-red",
    registryapp.WithObserver(stats.NewLoggingObserver(slog.Default())))
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
`app/registry` is registry-agnostic by design — verified end-to-end
against real Docker Hub, GHCR, and MCR (see Testing below); there is no
per-registry subpackage, and none is planned — any future registry-specific
wire-shape difference gets modeled as an additional field/variant in the
existing generic codecs, never as registry-name branching.

## Running the example

```sh
go run ./examples/go-edge-models
```

Decodes the embedded `examples/usecase1.json` reference manifest, patches
one module's image, and demonstrates `app/registry` against two local
`httptest` mock servers (a registry host + a separate auth-realm host).

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
