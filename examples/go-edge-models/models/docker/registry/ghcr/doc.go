// Package ghcr declares GitHub-Container-Registry-SPECIFIC models —
// RegistryHost (the actual dialable registry API host) and a dedicated
// Credentials type (`type Credentials auth.Credentials`, BOTH Username
// and Password required — see its own doc comment for why GHCR
// genuinely differs here; the extra constraint is layered via
// .RefineFunc(...) on top of models/docker/registry/auth's shared base
// CredentialsCodec, never a redeclared struct) — for the sibling
// models/docker/registry package's shared, registry-agnostic routes
// (PingRoute/GetTagsRoute/GetManifestRoute, which this package does NOT
// redeclare).
//
// The concrete, self-contained GHCR HTTP client built on top of these
// models (and models/docker/registry's shared routes, plus
// models/docker/registry/auth's auth-flow codecs) lives in the sibling
// examples/go-edge-models/app/registry/ghcr package instead — this
// package is pure data, no *http.Client, no I/O.
//
// See models/docker/registry's own doc comment for the full "what's
// shared here vs. what's registry-specific" rationale.
package ghcr
