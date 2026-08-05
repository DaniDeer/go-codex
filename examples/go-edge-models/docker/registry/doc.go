// Package registry models the Docker Registry HTTP API v2 (a.k.a. the OCI
// Distribution Spec) surface needed to retrieve, for a given container
// image reference: its available tags, and lean top-level manifest
// metadata (schema version, media type, content digest, total size) —
// deliberately excluding per-layer detail.
//
// Unlike the sibling docker package (pure codec modeling, zero I/O), this
// package is a REST CLIENT: it declares api/rest.Route values for every
// registry endpoint (routes.go) AND provides a thin orchestration layer
// implementing the registry's Bearer-token challenge flow (auth.go) and
// automatic multi-arch manifest-list resolution (client.go) — both of
// which require issuing HTTP calls, so they cannot live inside a pure
// codex.Codec.
//
// File layout of this package:
//
//   - routes.go: the externally-facing api/rest.Route contract — every
//     endpoint a downstream caller has a legitimate standalone reason to
//     call directly (Ping, GetTags, GetManifest).
//   - client.go: general client wiring — image-reference parsing,
//     manifest-list-to-single-platform resolution, and the two public
//     entry points (GetTags/GetImageMetadata).
//   - auth.go: everything related to authentication — the Bearer-token
//     challenge/token exchange flow, the optional Basic-auth Credentials
//     escape hatch for private repositories, the getTokenRoute endpoint
//     itself (also an api/rest.Route value, but auth-flow plumbing rather
//     than part of the externally-facing contract above — it needs a
//     realm URL/service/scope that only come from parsing a
//     WWW-Authenticate challenge, so it has no legitimate standalone
//     caller outside authenticate()), AND every security scheme/
//     requirement value this package declares (bearerAuthScheme/
//     bearerAuthSecurity, basicAuthScheme/basicAuthSecurity) — routes.go
//     only REFERENCES these by name (RouteMeta.Security/
//     rest.WithSecurityScheme), it never defines any of them itself.
//   - constants.go: package-level constants (Docker Hub defaults, the
//     manifest Accept header value).
//   - errors.go: every exported error type this package returns, both
//     client-wiring and auth errors.
//   - types.go: all public request/response/domain types.
//
// This package's PUBLIC SURFACE is deliberately reduced to exactly three
// things — nothing about the auth flow itself (challenge parsing, token
// exchange, credential injection) is exported:
//
//  1. client.go's GetTags/GetImageMetadata — the PRIMARY, batteries-included
//     entry points. They compose routes.go's routes with the auth flow and
//     manifest-list resolution internally; a caller never touches auth
//     machinery directly. This is how most callers use this package.
//  2. routes.go's exported rest.Route values (PingRoute, GetTagsRoute,
//     GetManifestRoute) — for advanced/low-level use: call .ClientHandle()
//     on any of them and drive adapters/nethttp.Call directly, with your
//     own *http.Client, retry policy, or observer. GetTagsRoute/
//     GetManifestRoute both declare Security, so driving them directly
//     without going through (1) means supplying your own
//     nethttp.CallOptions.CredentialFunc — this package has no exported
//     helper for that; write one against the target registry's actual auth
//     requirements, or use (1) instead.
//  3. types.go/codecs.go's domain structs and codecs (ImageRef, TagsList,
//     GetTagsReq, GetManifestReq, GetImageMetadataReq, ManifestMetadata,
//     Credentials, and their codecs) — needed to call (1) or (2) and read
//     their results.
//
// If a future need calls for a new capability (e.g. wrapping GetTagsRoute/
// GetManifestRoute as an MCP tool with this package's own auth flow baked
// in), that belongs as a NEW exported function alongside GetTags/
// GetImageMetadata in client.go — not as newly-exported internal auth
// plumbing for external callers to wire together themselves.
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
package registry
