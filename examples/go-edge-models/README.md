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
| [`iotedge/`](iotedge) | Azure IoT-Edge deployment manifest: `ModuleConfig`, `ModuleSettings`, `EnvVars` (string/int/float union), and `ModuleName`/`Modules`/`DeploymentManifest` — dotted-key extraction (`"properties.desired.modules.<name>"`) via `codex.Map`. | `docker/` (for `ModuleSettings.CreateOptions`) |
| [`iotedge/modulepatch/`](iotedge/modulepatch) | A derived, composed codec demonstrating the "compose new wire codecs from reusable codecs" story: `ModulePatch{ModuleName, ImageURL}` encodes directly into the manifest's full nested shape (`modulesContent` → `$edgeAgent` → `<key>` → `settings` → `image`), reusing `iotedge`'s own exported field codecs. | `iotedge/` |
| [`docker/`](docker) | Generic Docker Engine API create-options modeling: `Port`, `Bind`, `Ulimit`, `Healthcheck`, `HostConfig`, `CreateOptions`. Zero dependency on `iotedge` — reusable standalone for Docker Compose or any other Docker create-options tooling. | *(none)* |
| [`docker/registry/`](docker/registry) | Docker Registry HTTP API v2 / OCI Distribution Spec client with a deliberately RADICALLY REDUCED public surface — exactly three things: (1) `GetTags`/`GetImageMetadata`, the primary batteries-included entry points; (2) `PingRoute`/`GetTagsRoute`/`GetManifestRoute`, plain `rest.Route` values for advanced/low-level direct use; (3) domain structs/codecs (`ImageRef`, `TagsList`, `GetTagsReq`, `GetManifestReq`, `GetImageMetadataReq`, `ManifestMetadata`, `Credentials`). The entire Bearer/Basic auth-challenge flow (token exchange, credential injection, `rest.WithSecurityScheme` declarations) is package-private plumbing in `auth.go` — a caller never constructs it themselves. Transparent multi-arch manifest-list resolution. `docker/registry/internal/` holds the generic wire-format plumbing behind a real, compiler-enforced package boundary — never imported from outside `docker/registry`. | *(none — a separate concern from the create-options `docker/` package)* |

Each package follows the same internal convention: `types.go` (plain
structs, no codec logic), `constraints.go` (`validate.Constraint` values),
`codecs.go` (`codex.Codec[T]` values + `RequiredField`/`OptionalField`
wiring) — so every field-level codec is a standalone, reusable value, not
buried inline inside a larger struct's codec.

## Quick usage

**Decode a real deployment manifest:**

```go
manifest, err := format.JSON(iotedge.DeploymentManifestCodec).Unmarshal(manifestJSON)
web := manifest.ModulesContent.EdgeAgent["cv-writer-web"]
fmt.Println(web.Settings.Image, web.Status)
```

**Patch one module's image in-place on disk:**

```go
manifestFile := ports.NewFile(path, format.JSON(iotedge.DeploymentManifestCodec))
patch := modulepatch.ModulePatch{ModuleName: "cv-writer-web", ImageURL: "ghcr.io/org/edge-web:2.0.0"}
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
fmt.Println(meta.Digest, meta.TotalSizeBytes)

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
