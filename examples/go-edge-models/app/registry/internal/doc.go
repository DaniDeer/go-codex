// Package internal holds the MECHANICS every self-contained per-registry
// client (app/registry/dockerhub, .../ghcr, .../mcr) and app/registry's
// own generic-fallback Client are BUILT ON: the Bearer-token
// challenge/token-exchange auth flow and per-repository token cache
// (Authenticator), the shared BoundClientMiddleware constructor
// (BearerAuthClientMW), manifest-list resolution logic
// (PlatformMatches/FormatPlatformSelector plus
// NestedManifestListError/PlatformNotFoundError) — AND, since
// registryclient.go, the FULL shared client construction/operation
// surface itself (RegistryClient/NewRegistryClient): rest.NewClient/
// Authenticator/bound-routes/RegisterRoute/Attach wiring, plus
// GetTags/GetTagsFiltered/GetImageMetadata/ctxWithObserver. An earlier
// round shared only the low-level helpers above while each outer Client
// duplicated the construction/operation logic itself, verbatim, across
// all 4 packages — registryclient.go's own doc comment has the full
// before/after story.
//
// This is a true Go internal package — the compiler only allows it to be
// imported by code rooted at app/registry (app/registry itself, and any
// of its subpackages: dockerhub, ghcr, mcr), never by a sibling package
// outside that tree or an external module. It exists SPECIFICALLY so
// "fully self-contained client" (each of dockerhub/ghcr/mcr builds its
// OWN persistent *rest.Client and auth flow directly, rather than
// embedding a shared app/registry.Client the way an earlier round of
// this codebase did) doesn't mean "quadruplicated ~150 lines of
// construction/auth-flow/manifest-resolution logic" — every
// self-contained client's own New(...) now builds exactly ONE
// *internal.RegistryClient and forwards every method to it, instead of
// reimplementing them.
//
// RegistryAuthChallengeError/RegistryAuthError/NestedManifestListError/
// PlatformNotFoundError are defined HERE (the single, true underlying
// type for each) — every consuming package (app/registry,
// .../dockerhub, .../ghcr, .../mcr) re-exports each as a PUBLIC type
// alias (e.g. `type RegistryAuthError = internal.RegistryAuthError`) in
// its own vocabulary, so a caller can `errors.As` on the SAME underlying
// type regardless of which concrete Client's error they caught — this
// package itself is never importable from outside app/registry's own
// tree, so without that alias the error type would be permanently
// unreachable via errors.As for any real external caller.
package internal
