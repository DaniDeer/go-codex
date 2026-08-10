# go-edge-models

Models an Azure IoT-Edge deployment manifest end-to-end with go-codex
codecs, alongside two standalone, reusable packages: a generic Docker
Engine API (create-options) modeling package, and a Docker Registry HTTP
API v2 / OCI Distribution Spec client. This tree is structured to become
its own importable library — every package below is designed to be used
independently, not just from `main.go`'s demo.

## Package map

| Package | What it models | Depends on |
|---|---|---|
| [`iotedge/`](iotedge) | Azure IoT-Edge deployment manifest: `ModuleConfig`, `ModuleSettings`, `EnvVars` (string/int/float union), and `ModuleName`/`Modules`/`DeploymentManifest` — dotted-key extraction (`"properties.desired.modules.<name>"`) via `codex.Map`. `FlattenEnvVars(vars EnvVars) docker.Env` is a ONE-DIRECTION mapper (iotedge → docker only) that formats each typed string/int/float `EnvVarValue` as a flat "KEY=VALUE" `docker.EnvVar` — no reverse mapper (would require guessing the original value's type from a flat string). | `docker/` (for `ModuleSettings.CreateOptions` and `FlattenEnvVars`'s return type) |
| [`iotedge/modulepatch/`](iotedge/modulepatch) | A derived, composed codec demonstrating the "compose new wire codecs from reusable codecs" story: `ModulePatch{ModuleName, ImageURL}` encodes directly into the manifest's full nested shape (`modulesContent` → `$edgeAgent` → `<key>` → `settings` → `image`), reusing `iotedge`'s own exported field codecs. | `iotedge/` |
| [`docker/`](docker) | The shared "working-with-Docker-containers" DOMAIN package: `Image` (parsed `Name`/`Tag`/`Digest`, with `ImageCodec` for the wire string ↔ struct round trip and a `Stringer` for ergonomic printing), `Tag`/`Digest` named domain types (validated by a package-local `tagConstraint` and by core `validate.Digest` respectively — reused directly by `docker/registry`'s `TagsList.Tags`/`ManifestMetadata.Image.Digest`), `EnvVar`/`Env` (Docker's real create-options `"Env":["KEY=VALUE",...]` field, with `EnvCodec` for the wire ↔ struct round trip), plus generic Docker Engine API create-options modeling: `Port`, `Bind`, `Ulimit`, `Healthcheck`, `HostConfig`, `CreateOptions`. Zero dependency on `iotedge` — reusable standalone for Docker Compose or any other Docker create-options tooling. | *(none)* |
| [`docker/registry/`](docker/registry) | Docker Registry HTTP API v2 / OCI Distribution Spec client with a deliberately RADICALLY REDUCED public surface — exactly FOUR things: (1) `GetTags`/`GetImageMetadata`, the primary batteries-included entry points; (2) `PingRoute`/`GetTagsRoute`/`GetManifestRoute`, plain `rest.Route` values for advanced/low-level direct use; (3) domain structs/codecs (`ImageRef`, `TagsList`, `GetTagsReq`, `GetManifestReq`, `GetImageMetadataReq`, `ManifestMetadata`, `Credentials`); (4) `GetTagsTool`/`GetImageMetadataTool` + `NewGetTagsToolHandler`/`NewGetImageMetadataToolHandler` — ready-made MCP tool contracts wrapping (1) directly (registry-agnostic closures, not an `adapters/mcprest` route bridge — see below). The entire Bearer/Basic auth-challenge flow (token exchange, credential injection, `rest.WithSecurityScheme` declarations) is package-private plumbing in `auth.go` — a caller never constructs it themselves. Transparent multi-arch manifest-list resolution. `docker/registry/internal/` holds the generic wire-format plumbing behind a real, compiler-enforced package boundary — never imported from outside `docker/registry` (its own `DigestConstraint` is a thin re-export of core `validate.Digest`, deduped against `docker.Digest`'s constraint). `ImageRef.ToImage()`/`ImageRefFromImage()` map to/from `docker.Image` — `ImageRef`'s registry-host-split shape is genuinely different (needed to build a specific registry's HTTP base URL), so it keeps its own codec rather than reusing `docker.ImageCodec` directly. `TagsList.Tags []docker.Tag` reuses the shared domain type directly (same concept, identical shape — no mapper needed); `ManifestMetadata.Image docker.Image` is built by `GetImageMetadata` reusing `ImageRef.ToImage()` and overriding `.Digest` with the registry-resolved content digest. `GetImageMetadataReq`/`ManifestMetadata` (unlike `GetTagsReq`/`GetManifestReq`) are NOT one route's Req/Resp types — `GetImageMetadata` is a multi-call client-side orchestration (parses the image URL, calls `GetManifestRoute` up to twice, computes a size summary), so its own shape is declared via a plain `codex.Struct`-based codec pair (`GetImageMetadataReqCodec`/`ManifestMetadataCodec`) instead of a `rest.Route`. Files are organized ONE PER OPERATION (`ping.go`, `gettags.go`, `getimagemetadata.go`, plus shared `imageref.go`/`credentials.go`/`auth.go`) — each operation's route, req/resp types+codecs, client function, and MCP tool live together; there is deliberately no `client.go`/`routes.go`/`mcptools.go` aggregator file. | `docker` (Image/Tag/Digest reuse plus the Image mapper — the HTTP client/auth/routes remain independent) |

Each package follows the same internal convention: ONE FILE PER CONCEPT —
a concept's plain struct(s), any `validate.Constraint` values it needs,
and its `codex.Codec[T]` values (built via `RequiredField`/
`OptionalField`) all live together in that one file, so understanding or
changing one concept never requires jumping across files. `docker`/
`iotedge` split by DOMAIN CONCEPT (e.g. `docker/image.go`,
`iotedge/envvars.go`); `docker/registry` (a REST client, not a pure codec
package) splits by OPERATION instead (e.g. `gettags.go`,
`getimagemetadata.go`) — each operation's route, request/response
types+codecs, batteries-included client function, and MCP tool all live
together, with no separate `client.go`/`routes.go`/`mcptools.go`
aggregator file. Every field-level codec is still its own standalone,
reusable value, not buried inline inside a larger struct's codec — see
each package's `doc.go` for its exact file map.

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
manifestFile := ports.NewFile(path, format.JSON(iotedge.DeploymentManifestCodec))
patch := modulepatch.ModulePatch{ModuleName: "factory-dashboard", ImageURL: "ghcr.io/org/edge-web:2.0.0"}
err := ports.PatchEncoded(manifestFile, nil, modulepatch.ModulePatchCodec, patch, ports.FileOptions{})
```

**Fetch tags and lean manifest metadata from any OCI-compliant registry —
same functions, same routes, only the image URL differs:**

```go
tags, err := registry.GetTags(ctx, http.DefaultClient, "nodered/node-red")       // Docker Hub
tags, err  = registry.GetTags(ctx, http.DefaultClient, "ghcr.io/org/image")      // GHCR
tags, err  = registry.GetTags(ctx, http.DefaultClient, "mcr.microsoft.com/dotnet/runtime") // MCR

meta, err := registry.GetImageMetadata(ctx, http.DefaultClient,
    registry.GetImageMetadataReq{ImageURL: "alpine:latest"}) // Platform defaults to "linux/amd64"
fmt.Println(meta.Image, meta.TotalSizeBytes)

// Private repository requiring Basic auth at the token-exchange step
// (e.g. a private GHCR package — GitHub username + a read:packages PAT):
tags, err = registry.GetTags(ctx, http.DefaultClient, "ghcr.io/org/private-image",
    registry.WithCredentials(registry.Credentials{Username: os.Getenv("GHCR_USER"), Password: os.Getenv("GHCR_PAT")}))

// A single call site working against MULTIPLE registries: declare ALL
// your registries' credentials once, GetTags/GetImageMetadata pick the
// right entry automatically based on each image URL's resolved registry
// host — the SAME options value is reused unchanged below.
byRegistry := registry.RegistryCredentials{
    "registry-1.docker.io": {Username: "docker-user", Password: os.Getenv("DOCKERHUB_TOKEN")},
    "ghcr.io":               {Username: os.Getenv("GHCR_USER"), Password: os.Getenv("GHCR_PAT")},
    "mcr.microsoft.com":     {},                              // MCR needs no auth — omit or leave zero.
}
opt := registry.WithCredentialsByRegistry(byRegistry)
tags, err = registry.GetTags(ctx, http.DefaultClient, "nodered/node-red", opt)
tags, err  = registry.GetTags(ctx, http.DefaultClient, "ghcr.io/org/private-image", opt)
```

**Observer integration.** This package builds on `nethttp.Call`/`CallHandle`
internally, so it inherits [go-codex's context-based Observer
default](../../docs/features/observer.md#per-layer-behavior) for free — no
option needed:

```go
// The FREE way: attach an observer to ctx once; every HTTP call this
// package makes (auth-realm Ping/token exchange included) is observed
// automatically.
ctx = stats.WithObserver(ctx, stats.NewLoggingObserver(slog.Default()))
tags, err := registry.GetTags(ctx, http.DefaultClient, "nodered/node-red")

// The EXPLICIT way: registry.WithObserver overrides ctx for just this one
// call (same "explicit wins over context" precedence as nethttp.CallOptions.Observer).
tags, err = registry.GetTags(ctx, http.DefaultClient, "nodered/node-red",
    registry.WithObserver(stats.NewLoggingObserver(slog.Default())))
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
`docker/registry` is registry-agnostic by design — verified end-to-end
against real Docker Hub, GHCR, and MCR (see Testing below); there is no
per-registry subpackage, and none is planned — any future registry-specific
wire-shape difference gets modeled as an additional field/variant in the
existing generic codecs, never as registry-name branching.

## Running the example

```sh
go run ./examples/go-edge-models
```

Decodes the embedded `examples/usecase1.json` reference manifest, patches
one module's image, and demonstrates `docker/registry` against two local
`httptest` mock servers (a registry host + a separate auth-realm host).

## Testing

```sh
# Unit tests — offline, deterministic, run by `just check` / CI.
go test ./examples/go-edge-models/...

# Integration test — hits the REAL Docker Hub registry, opt-in only.
go test -tags=integration ./examples/go-edge-models/docker/registry/...
```

The integration test (`docker/registry/registry_integration_test.go`) is
gated behind the `integration` build tag specifically so it never runs as
part of a normal build/test/CI pass — it requires network access and talks
to `registry-1.docker.io`/`auth.docker.io`, `ghcr.io`, and
`mcr.microsoft.com` directly (proving `docker/registry`'s registry-agnostic
design against all three real registries in one run).
