// Package registry is the CONCRETE IMPLEMENTATION built on top of the
// sibling models/docker/registry package's declared contract (routes,
// domain structs/codecs, MCP tool declarations). This package holds the
// Client interface, NewGenericClient (a real, persistent,
// per-registry-host-bound *rest.Client — the GENERIC FALLBACK for any
// OCI-compliant registry not specifically modeled by one of the 3
// self-contained sibling packages), and Dispatcher (this package's own
// unifying, registry-agnostic entry point, folding in what used to be a
// separate app/registry/dispatch package — see dispatcher.go's own
// comment for why that fold is now possible).
//
// If you only need to declare a request/response shape, generate a spec,
// or build your OWN HTTP client/MCP server against this registry API's
// wire format, import models/docker/registry instead — it has zero
// dependency on this package (the dependency only ever goes this
// direction) and pulls in no concrete HTTP client machinery beyond what
// api/rest itself needs.
//
// This package does NOT import app/registry/dockerhub/.../ghcr/.../mcr's
// own per-registry MODELS packages for anything beyond their
// RegistryHost constants (dispatcher.go's per-host dispatch table) — it
// never touches their typed Credentials directly; Dispatcher's own
// WithDockerHubOptions/WithGHCROptions/WithMCROptions pass each
// sibling's OWN options straight through instead.
//
// File layout:
//
//   - client.go: the Client interface, genericClient/NewGenericClient (the
//     generic-fallback implementation — a THIN wrapper around ONE
//     app/registry/internal.RegistryClient, the SAME shared construction/
//     operation mechanics app/registry/dockerhub/.../ghcr/.../mcr's own
//     Client wrappers delegate to — see
//     app/registry/internal/registryclient.go's own doc comment), and
//     this package's public
//     error-type aliases (NestedManifestListError/PlatformNotFoundError/
//     RegistryAuthChallengeError/RegistryAuthError — see
//     app/registry/internal's own doc comment for why every consuming
//     package re-exports these the same way).
//   - options.go: Option/WithCredentials/WithCredentialsByRegistry/
//     WithObserver — NewGenericClient's OWN options, built around
//     models/docker/registry/generic's Credentials/RegistryCredentials
//     (a plain alias of models/docker/registry/auth.Credentials — the
//     generic fallback has no single known registry to compile-time-
//     guard against, unlike dockerhub/ghcr/mcr's own DISTINCT Credentials
//     types).
//   - filteropts.go: FilterOptsFor (the GetTagsToolReq ->
//     docker.FilterTagsOpt mapping Dispatcher's own
//     NewGetTagsToolHandler reuses).
//   - dispatcher.go: Dispatcher/NewDispatcher/DispatcherOption (this
//     package's OWN registry-agnostic, image-URL-resolving entry point —
//     see its own doc comment for the full rationale).
//
// REGISTRY-AGNOSTIC BY DESIGN at the Dispatcher layer — verified against
// real registries, not just Docker Hub: Dispatcher.GetTags/
// GetImageMetadata work IDENTICALLY regardless of which OCI-compliant
// registry the image URL points at. Confirmed end-to-end (see
// registry_integration_test.go) against Docker Hub (registry-1.docker.io),
// GHCR (ghcr.io), and MCR (mcr.microsoft.com).
package registry
