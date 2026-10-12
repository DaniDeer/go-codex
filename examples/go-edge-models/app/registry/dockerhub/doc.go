// Package dockerhub is Docker Hub's FULLY SELF-CONTAINED registry
// client — one of app/registry's per-registry sibling packages (alongside
// app/registry/ghcr and app/registry/mcr). Mirrors
// models/iotedge/modulepatch's sibling-package precedent: a consumer who
// only needs the Docker Hub client doesn't pull in anything GHCR/MCR-
// specific, and vice versa.
//
// Client is a THIN wrapper around ONE *internal.RegistryClient — it does
// NOT embed or wrap a shared app/registry.Client the way an earlier round
// of this codebase did (per the user's explicit "fully self-contained
// clients" design); it also no longer builds its own persistent
// *rest.Client/*Authenticator inline the way an even earlier round of
// THIS package did — both the construction/wiring (rest.NewClient/
// Authenticator/bound-routes/RegisterRoute/Attach) AND the operation
// logic (GetTags/GetTagsFiltered/GetImageMetadata/ctxWithObserver) now
// live in app/registry/internal's own RegistryClient, confirmed via
// `diff` to have been BYTE-IDENTICAL across this package and its ghcr/mcr
// siblings before that extraction — see
// app/registry/internal/registryclient.go's own doc comment for the full
// before/after story. What stays genuinely PACKAGE-SPECIFIC here: this
// package's OWN distinct Credentials type (hostmodels.Credentials,
// compile-time-distinct from ghcr.Credentials/mcr.Credentials) and its
// OWN Option/WithCredentials/WithObserver built around it. This package
// depends on models/docker/registry (shared routes), models/docker/
// registry/dockerhub (this registry's OWN RegistryHost/Credentials
// models), and models/docker/registry/auth (auth-flow codecs) — NOT on
// app/registry itself, specifically so app/registry can safely import
// THIS package for its own unifying Client without an import cycle.
//
// This package has NO dependency on app/registry/ghcr or app/registry/mcr
// (true siblings, not interdependent).
//
// Most callers never construct this directly — app/registry's own
// unifying Client holds one of these (alongside ghcr.Client/mcr.Client)
// under the hood and picks the right one per call from an image URL. Use
// this package directly only when you specifically know you're always
// talking to Docker Hub and want that narrower, more specific type (and
// its own typed Credentials, distinct from ghcr.Credentials/
// mcr.Credentials).
package dockerhub
