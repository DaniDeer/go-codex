// Package ghcr is GitHub Container Registry's FULLY SELF-CONTAINED
// registry client — one of app/registry's per-registry sibling packages
// (alongside app/registry/dockerhub and app/registry/mcr).
//
// Client is a THIN wrapper around ONE *internal.RegistryClient — it does
// NOT embed or wrap a shared app/registry.Client, and no longer builds
// its own persistent *rest.Client/*Authenticator inline either: both the
// construction/wiring AND the operation logic (GetTags/GetTagsFiltered/
// GetImageMetadata/ctxWithObserver) now live in app/registry/internal's
// own RegistryClient — see that package's own doc comment
// (app/registry/internal/registryclient.go), and app/registry/dockerhub's
// own doc comment for the full "why self-contained, and why now a thin
// wrapper" rationale (identical here). What stays genuinely
// PACKAGE-SPECIFIC: this package's OWN distinct Credentials type and its
// OWN Option/WithCredentials/WithObserver. This package depends on
// models/docker/registry (shared routes), models/docker/registry/ghcr
// (this registry's OWN RegistryHost/Credentials models — NOTE: GHCR's
// own Credentials requires BOTH Username and Password, unlike Docker
// Hub/MCR's optional Username — see that package's own doc comment), and
// models/docker/registry/auth (auth-flow codecs) — NOT on app/registry
// itself, specifically so app/registry can safely import THIS package
// for its own unifying Client without an import cycle.
//
// This package has NO dependency on app/registry/dockerhub or
// app/registry/mcr (true siblings, not interdependent).
//
// Most callers never construct this directly — app/registry's own
// unifying Client holds one of these (alongside dockerhub.Client/
// mcr.Client) under the hood and picks the right one per call from an
// image URL. Use this package directly only when you specifically know
// you're always talking to GHCR and want that narrower, more specific
// type.
package ghcr
