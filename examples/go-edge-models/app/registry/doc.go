// Package registry is the CONCRETE IMPLEMENTATION built on top of the
// sibling models/docker/registry package's declared contract (routes,
// domain structs/codecs, MCP tool declarations). This package holds
// everything that actually issues HTTP calls or needs a *http.Client:
// the batteries-included GetTags/GetImageMetadata client functions, the
// Bearer-token challenge/token-exchange auth flow, and the MCP tool
// handler bindings (NewGetTagsToolHandler/NewGetImageMetadataToolHandler)
// that wire models/docker/registry's declared Tool values to these
// concrete functions.
//
// If you only need to declare a request/response shape, generate a spec,
// or build your OWN HTTP client/MCP server against this registry API's
// wire format, import models/docker/registry instead — it has zero
// dependency on this package (the dependency only ever goes this
// direction: app/registry imports models/docker/registry, never the
// reverse) and pulls in no concrete HTTP client machinery beyond what
// api/rest itself needs.
//
// File layout — mirrors models/docker/registry's per-operation split,
// but holding only the IMPLEMENTATION half of each operation:
//
//   - auth.go: everything related to authentication — the Bearer-token
//     challenge/token exchange flow (parseChallenge, authenticate,
//     newAuthCredentialFunc), whose returned credentialFunc GetTags/
//     GetImageMetadata chain onto GetTagsRoute/GetManifestRoute via
//     .ClientMW(&regmodels.BearerAuthDeclaration, authFn) — pairing the
//     runtime credential flow against the "bearerAuth" Security
//     declaration built from models/docker/registry's shared
//     BearerAuthScheme/BearerAuthSchemeName), the optional Basic-auth
//     Credentials escape hatch for
//     private repositories (Option/WithCredentials/
//     WithCredentialsByRegistry/WithObserver), the getTokenRoute endpoint
//     (auth-flow plumbing — it has no legitimate standalone caller
//     outside this file's own authenticate()), basicAuthMw
//     (getTokenRoute's OWN middleware.Middleware security declaration,
//     unlike GetTagsRoute/GetManifestRoute's shared,
//     models/docker/registry-declared one, since getTokenRoute has a
//     single caller and no external contract to share), and this file's
//     own error types (RegistryAuthChallengeError/RegistryAuthError).
//   - gettags.go: GetTags (the batteries-included client function),
//     registryBaseURL (shared with getimagemetadata.go/auth.go), and
//     NewGetTagsToolHandler (binds models/docker/registry's GetTagsTool
//     to GetTags).
//   - getimagemetadata.go: fetchManifest/platformMatches/
//     FormatPlatformSelector (manifest-list resolution logic),
//     NestedManifestListError/PlatformNotFoundError,
//     acceptManifestTypes/defaultPlatform constants, GetImageMetadata
//     (the batteries-included client function), and
//     NewGetImageMetadataToolHandler (binds models/docker/registry's
//     GetImageMetadataTool to GetImageMetadata).
//
// This package's PUBLIC SURFACE is deliberately reduced to exactly TWO
// things — nothing about the auth flow itself (challenge parsing, token
// exchange, credential injection) is exported:
//
//  1. GetTags/GetImageMetadata (gettags.go/getimagemetadata.go) — the
//     PRIMARY, batteries-included entry points. They compose
//     models/docker/registry's routes with the auth flow and
//     manifest-list resolution internally; a caller never touches auth
//     machinery directly. This is how most callers use this package.
//  2. NewGetTagsToolHandler/NewGetImageMetadataToolHandler
//     (gettags.go/getimagemetadata.go) — ready-made MCP tool handler
//     bindings wrapping (1) directly (registry-agnostic, closure-based —
//     NOT an adapters/mcprest route bridge, since GetTags/GetImageMetadata
//     resolve their target registry per call, not from one fixed
//     baseURL). Pair the returned handler with
//     models/docker/registry's GetTagsTool/GetImageMetadataTool
//     (Tool.Register(mcpBuilder)) via mcpgo.ToolHandler; see either
//     constructor's own doc comment for the full usage snippet.
//     (examples/go-edge-models/main.go's OWN separate demo additionally
//     shows the LOWER-level pattern of wrapping GetTagsRoute directly via
//     adapters/mcprest.ToolHandler, fixed to one registry — a distinct,
//     more advanced use case, kept for illustration.)
//
// REGISTRY-AGNOSTIC BY DESIGN — verified against real registries, not
// just Docker Hub: GetTags/GetImageMetadata work IDENTICALLY regardless
// of which OCI-compliant registry the image URL points at. Confirmed
// end-to-end (see registry_integration_test.go) against Docker Hub
// (registry-1.docker.io), GHCR (ghcr.io), and MCR (mcr.microsoft.com) —
// same functions, only the image URL string differs ("ghcr.io/org/repo:tag"
// vs a bare "alpine" defaulting to Docker Hub via
// models/docker/registry.ParseImageRef).
//
// Private repositories on registries that require Basic auth to mint a
// ****** (e.g. a private GHCR package, authenticated with a GitHub
// username + a PAT with read:packages scope) are supported via
// WithCredentials(regmodels.Credentials{...}) — an additive functional
// option on GetTags/GetImageMetadata; anonymous/public pulls are
// completely unaffected when it is omitted.
//
// A single call site working against MULTIPLE registries (e.g. some
// images on Docker Hub, some on GHCR, some on MCR) can instead declare
// ALL of its registries' credentials once via
// WithCredentialsByRegistry(regmodels.RegistryCredentials{...}) — GetTags/
// GetImageMetadata then pick the right entry automatically based on the
// image URL's resolved registry host, so the SAME options value is reused
// unchanged no matter which registry a given image URL resolves to.
// RegistryCredentials' keys are restricted to the registries this package
// is proven against end-to-end (Docker Hub, GHCR, MCR — see
// models/docker/registry's knownRegistryHosts in credentials.go); for any
// other registry, use the unrestricted single-value WithCredentials
// instead. If both options are supplied to the same call, WithCredentials
// wins.
package registry
