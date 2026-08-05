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
//   - routes.go: the plain api/rest.Route contract for every registry
//     endpoint (Ping, GetTags, GetManifest, GetToken).
//   - client.go: general client wiring — image-reference parsing,
//     manifest-list-to-single-platform resolution, and the two public
//     entry points (GetTags/GetImageMetadata).
//   - auth.go: everything related to authentication — the Bearer-token
//     challenge/token exchange flow and the optional Basic-auth
//     Credentials escape hatch for private repositories.
//   - constants.go: package-level constants (Docker Hub defaults, the
//     manifest Accept header value).
//   - errors.go: every exported error type this package returns, both
//     client-wiring and auth errors.
//   - types.go: all public request/response/domain types.
//
// Consumption layers, in the order a caller encounters them:
//
//  1. routes.go's exported rest.Route values (PingRoute, GetTagsRoute,
//     GetManifestRoute, GetTokenRoute) are the PRIMARY contract — call
//     .ClientHandle() on any of them and drive adapters/nethttp.Call
//     directly, with your own *http.Client, retry policy, or observer.
//     auth.go's NewAuthCredentialFunc is the matching reusable auth
//     building block for this layer: pass its result as
//     nethttp.CallOptions.CredentialFunc and the same Ping + challenge +
//     token-exchange flow GetTags/GetImageMetadata use internally applies
//     to your own direct route calls too — e.g. wrapping GetTagsRoute/
//     GetManifestRoute as an MCP tool without going through (2) at all.
//  2. client.go's GetTags/GetImageMetadata are a convenience layer built ON
//     TOP of (1) — they compose the routes with auth.go's auth flow (via
//     NewAuthCredentialFunc) and manifest-list resolution so a caller
//     doesn't have to reimplement that orchestration.
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
