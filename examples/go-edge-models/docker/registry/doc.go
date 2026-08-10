// Package registry models the Docker Registry HTTP API v2 (a.k.a. the OCI
// Distribution Spec) surface needed to retrieve, for a given container
// image reference: its available tags, and lean top-level manifest
// metadata (schema version, media type, content digest, total size) —
// deliberately excluding per-layer detail.
//
// Unlike the sibling docker package (pure codec modeling, zero I/O), this
// package is a REST CLIENT: it declares api/rest.Route values for every
// registry endpoint AND provides a thin orchestration layer implementing
// the registry's Bearer-token challenge flow (auth.go) and automatic
// multi-arch manifest-list resolution (getimagemetadata.go) — both of
// which require issuing HTTP calls, so they cannot live inside a pure
// codex.Codec.
//
// File layout of this package — ONE FILE PER OPERATION, not per layer:
// each operation's route declaration, request/response types+codecs,
// batteries-included client function, its own error types/constants, and
// (where applicable) its ready-made MCP tool ALL live together, so
// understanding or changing one operation never requires jumping across
// files. There is deliberately no client.go/routes.go/mcptools.go
// aggregator file — Go doesn't need one for "a clear import" (the
// package itself, `import ".../docker/registry"`, already is that), and
// this doc comment is the one index a reader needs.
//
//   - ping.go: PingRoute — no client function or MCP tool of its own (see
//     its own doc comment for why).
//   - gettags.go: GetTagsReq/GetTagsRoute, TagsList/TagsListCodec,
//     GetTags (the batteries-included client function),
//     GetTagsToolReq/GetTagsTool/NewGetTagsToolHandler (its MCP tool),
//     and registryBaseURL (the one helper shared with
//     getimagemetadata.go).
//   - getimagemetadata.go: GetManifestReq/GetManifestRoute (the
//     underlying single-manifest route this operation composes, up to
//     twice, for manifest-list resolution), GetImageMetadataReq/
//     ManifestMetadata and their codecs (GetImageMetadataReqCodec/
//     ManifestMetadataCodec — declared as plain codex.Struct codecs, NOT
//     a rest.Route, since GetImageMetadata is a multi-call
//     client-side orchestration with no single dialable Method+Path —
//     see this file's own doc comment for the full rationale),
//     GetImageMetadata (the batteries-included client function),
//     fetchManifest/platformMatches (its manifest-list resolution
//     logic), and GetImageMetadataTool/NewGetImageMetadataToolHandler
//     (its MCP tool — reuses GetImageMetadataReq/ManifestMetadata
//     directly, since they're already a single registry-agnostic shape).
//     ManifestMetadata.Image is a docker.Image built by reusing
//     imageref.go's ImageRef.ToImage() and overriding Digest with the
//     registry-resolved content digest.
//   - imageref.go: ImageRef (registry/repository/reference), its codec,
//     the ToImage/ImageRefFromImage mapper to/from docker.Image,
//     ParseImageRef/FormatImageRef, splitDockerDomain, ImageRefParseError,
//     and the Docker-Hub-default constants only this file's own logic
//     needs.
//   - credentials.go: Credentials/RegistryCredentials and their codecs,
//     plus the registry-host constants (ghcrRegistryHost/
//     mcrRegistryHost/knownRegistryHosts) only RegistryCredentialsCodec's
//     key constraint needs (dockerHubRegistryHost itself lives in
//     imageref.go and is referenced from here directly — same package,
//     no import needed).
//   - auth.go: everything related to authentication — the Bearer-token
//     challenge/token exchange flow, the optional Basic-auth Credentials
//     escape hatch for private repositories, the getTokenRoute endpoint
//     itself (also an api/rest.Route value, but auth-flow plumbing rather
//     than part of the externally-facing contract above — it needs a
//     realm URL/service/scope that only come from parsing a
//     WWW-Authenticate challenge, so it has no legitimate standalone
//     caller outside authenticate()), every security scheme/requirement
//     value this package declares (bearerAuthScheme/bearerAuthSecurity,
//     basicAuthScheme/basicAuthSecurity — gettags.go/getimagemetadata.go
//     only REFERENCE these by name via RouteMeta.Security/
//     rest.WithSecurityScheme, never define them), and this package's
//     auth-specific error types (RegistryAuthChallengeError/
//     RegistryAuthError).
//
// This package's PUBLIC SURFACE is deliberately reduced to exactly FOUR
// things — nothing about the auth flow itself (challenge parsing, token
// exchange, credential injection) is exported:
//
//  1. GetTags/GetImageMetadata (gettags.go/getimagemetadata.go) — the
//     PRIMARY, batteries-included entry points. They compose the routes
//     below with the auth flow and manifest-list resolution internally;
//     a caller never touches auth machinery directly. This is how most
//     callers use this package.
//  2. The exported rest.Route values (PingRoute, GetTagsRoute,
//     GetManifestRoute) — for advanced/low-level use: call .ClientHandle()
//     on any of them and drive adapters/nethttp.Call directly, with your
//     own *http.Client, retry policy, or observer. GetTagsRoute/
//     GetManifestRoute both declare Security, so driving them directly
//     without going through (1) means supplying your own
//     nethttp.CallOptions.CredentialFunc — this package has no exported
//     helper for that; write one against the target registry's actual auth
//     requirements, or use (1) instead.
//  3. The domain structs and codecs (ImageRef in imageref.go, TagsList in
//     gettags.go, GetTagsReq in gettags.go, GetManifestReq/
//     GetImageMetadataReq/ManifestMetadata in getimagemetadata.go,
//     Credentials/RegistryCredentials in credentials.go, and their
//     codecs) — needed to call (1) or (2) and read their results.
//  4. GetTagsTool/GetImageMetadataTool + NewGetTagsToolHandler/
//     NewGetImageMetadataToolHandler (gettags.go/getimagemetadata.go) —
//     ready-made MCP tool contracts wrapping (1) directly (registry-agnostic,
//     closure-based — NOT an mcprest route bridge, since GetTags/
//     GetImageMetadata resolve their target registry per call, not from
//     one fixed baseURL). A caller registers the Tool against their own
//     mcp.Builder and pairs it with the handler via mcpgo.ToolHandler;
//     see either constructor's own doc comment for the full usage
//     snippet. (examples/go-edge-models/main.go's OWN separate demo
//     additionally shows the LOWER-level pattern of wrapping GetTagsRoute
//     directly via adapters/mcprest.ToolHandler, fixed to one registry —
//     a distinct, more advanced use case from wrapping GetTagsRoute
//     yourself, kept for illustration.)
//
// This package has NO dependency on the sibling iotedge or docker
// packages — it models an entirely separate Docker HTTP API (the registry
// API), not the create-options wire format.
//
// REGISTRY-AGNOSTIC BY DESIGN — verified against real registries, not just
// Docker Hub: GetTags/GetImageMetadata (and the routes above) work
// IDENTICALLY regardless of which OCI-compliant registry the image URL
// points at. Confirmed end-to-end (see registry_integration_test.go) against
// Docker Hub (registry-1.docker.io), GHCR (ghcr.io), and MCR
// (mcr.microsoft.com) — same functions, same routes, only the image URL
// string differs ("ghcr.io/org/repo:tag" vs a bare "alpine" defaulting to
// Docker Hub via ParseImageRef). There is deliberately no per-registry
// subpackage (no docker/registry/ghcr, no docker/registry/mcr) — that would
// work AGAINST this goal. Any registry-specific wire-shape difference
// discovered in the future belongs in the existing generic codecs
// (docker/registry/internal) as an additional optional field/variant, never
// as registry-name branching.
//
// Private repositories on registries that require Basic auth to mint a
// Bearer token (e.g. a private GHCR package, authenticated with a GitHub
// username + a PAT with read:packages scope) are supported via
// WithCredentials(Credentials{...}) — an additive functional option on
// GetTags/GetImageMetadata; anonymous/public pulls are completely unaffected
// when it is omitted.
//
// A single call site working against MULTIPLE registries (e.g. some
// images on Docker Hub, some on GHCR, some on MCR) can instead declare
// ALL of its registries' credentials once via
// WithCredentialsByRegistry(RegistryCredentials{...}) — GetTags/
// GetImageMetadata then pick the right entry automatically based on the
// image URL's resolved registry host, so the SAME options value is reused
// unchanged no matter which registry a given image URL resolves to.
// RegistryCredentials' keys are restricted to the registries this package
// is proven against end-to-end (Docker Hub, GHCR, MCR — see
// knownRegistryHosts in credentials.go); for any other registry, use the
// unrestricted single-value WithCredentials instead. If both options are
// supplied to the same call, WithCredentials wins.
package registry
