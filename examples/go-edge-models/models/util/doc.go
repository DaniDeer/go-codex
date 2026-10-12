// Package util holds generic OCI Distribution Spec / Docker Registry
// HTTP API v2 wire-shape plumbing that is NOT specifically about
// authentication: the manifest/manifest-list envelope types and the
// "os/arch" platform selector, plus their codecs.
//
// This package is PUBLIC (no `internal/` path segment) — a deliberate
// choice, migrated from what used to be a true Go internal package
// (examples/go-edge-models/internal/registry) specifically so these wire
// shapes become part of the officially reusable, declared contract:
// any consumer of this module (not just code rooted at go-edge-models)
// can decode/encode a real registry manifest or a platform selector
// string without reaching into go-edge-models' own HTTP client plumbing.
//
// Both models/docker/registry (the declarative REST contract:
// GetManifestRoute's own Resp type is util.ManifestEnvelope, merging the
// Docker-Content-Digest response header into util.ManifestEnvelope.Digest
// via rest.NewRequiredResponseHeaderParam) and app/registry (the concrete
// HTTP client + auth flow implementation, including its self-contained
// per-registry siblings — dockerhub/ghcr/mcr — and the generic fallback
// client) depend on this package directly.
//
// Files are organized ONE PER CONCEPT — the same convention every other
// package in this module follows (models/docker, models/iotedge,
// models/docker/registry, models/versioning):
//
//   - manifest.go — ManifestDescriptor/PlatformDescriptor/
//     SingleManifestWire/ManifestListWire/ManifestEnvelope,
//     DigestConstraint, and their codecs (DigestCodec,
//     ManifestDescriptorCodec, SingleManifestWireCodec,
//     ManifestListWireCodec, ManifestEnvelopeCodec).
//   - platform.go — PlatformSelector, SupportedPlatforms,
//     PlatformConstraint (an ENUM over SupportedPlatforms, via
//     validate.OneOf — not an open regex), PlatformSelectorCodec
//     ("os/arch" selector strings), plus DefaultPlatform/
//     DefaultPlatformSelector/DefaultPlatformCodec — a reusable,
//     codex.Either2-based "empty string means the default platform,
//     else a valid enum-constrained selector" codec every self-contained
//     registry client's GetImageMetadata method uses.
//
// The auth-specific half of what used to live in internal/registry
// (Challenge/WWW-Authenticate parsing, the Docker auth-scope string,
// TokenResponse, Bearer/Basic header codecs) moved to the sibling
// models/docker/registry/auth package instead — see that package's own
// doc comment. This package must stay PURELY GENERIC OCI/Docker
// Distribution Spec wire plumbing — it must never absorb a
// registry-specific quirk (e.g. a GHCR-only pagination scheme, an
// MCR-only catalog extension); registry-specific integrations belong in
// models/docker/registry/{dockerhub,ghcr,mcr} and
// app/registry/{dockerhub,ghcr,mcr} instead.
package util
