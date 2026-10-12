// Package generic holds the any-OCI-compliant-registry credential
// concept — used ONLY by app/registry's generic FALLBACK client
// (NewGenericClient), for any registry host OTHER than the 3 known ones
// this module has dedicated, self-contained clients + typed Credentials
// packages for (models/docker/registry/dockerhub/.../ghcr/.../mcr).
//
// Credentials is a plain ALIAS of models/docker/registry/auth.Credentials
// (not a distinct named type like dockerhub.Credentials/ghcr.Credentials/
// mcr.Credentials) — the whole point of the generic fallback path is "I
// don't know/care which registry in advance," so there is no "wrong
// registry" mistake category to compile-time-guard against the way the 3
// known registries' own packages do.
//
// This package is a sibling of dockerhub/ghcr/mcr under
// models/docker/registry — the layout is fully symmetric:
// generic/dockerhub/ghcr/mcr, all built on the shared
// models/docker/registry/auth.Credentials base.
//
// Files are organized ONE PER CONCEPT:
//
//   - credentials.go — Credentials (alias), CredentialsCodec,
//     NewCredentials, RegistryCredentials, RegistryCredentialsCodec,
//     knownRegistryHosts (sourced directly from dockerhub/ghcr/mcr's own
//     RegistryHost consts — this package has no need to depend on
//     models/docker/registry's config.go re-exports).
package generic
