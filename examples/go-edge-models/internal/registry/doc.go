// Package internal holds the generic OCI Distribution Spec / Docker
// Registry HTTP API v2 wire-shape and auth-flow plumbing that BOTH sibling
// packages built on it are made of: manifest/manifest-list envelope
// types, the WWW-Authenticate challenge, the Docker auth-scope string,
// the "os/arch" platform selector, and their codecs.
//
// This is a true Go internal package — the compiler only allows it to be
// imported by code rooted at go-edge-models (go-edge-models itself, and
// any of its subpackages), never by a sibling module or an external
// package. It lives at go-edge-models/internal/registry — one level
// higher than either of its two importers — specifically so that BOTH
// can reach it despite being siblings, not a parent/child pair:
//
//   - models/docker/registry (the declarative contract: routes, domain
//     structs/codecs, MCP tool declarations) uses
//     ManifestEnvelope/ManifestEnvelopeCodec/DigestCodec directly in
//     GetManifestRoute's own declaration.
//   - app/registry (the concrete HTTP client + auth flow implementation)
//     uses Challenge/TokenResponse/DockerScopeCodec/BasicAuthCodec/
//     PlatformSelectorCodec/ManifestDescriptor for its auth flow and
//     manifest-list resolution logic.
//
// That is a deliberate, enforced boundary, not just a naming convention:
// models/docker/registry's and app/registry's own public files are the
// ONLY supported surface for a consumer of this library — this internal
// package is implementation plumbing for BOTH of them, never imported
// directly by anything else.
//
// IMPORTANT for future work: this package must stay PURELY GENERIC OCI/
// Docker Distribution Spec plumbing — it must never absorb a
// registry-specific quirk (e.g. a GHCR-only pagination scheme, an
// MCR-only catalog extension). Registry-specific integrations belong in
// their OWN future sibling packages under app/ (e.g. app/ghcr, app/mcr —
// mirroring the models/iotedge/modulepatch sibling-package precedent),
// composing models/docker/registry's PUBLIC contract instead of reaching
// into this internal package directly — even though such a package
// WOULD be compiler-permitted to import this one (anything rooted at
// go-edge-models is), doing so would defeat the point: registry-specific
// code has no business depending on the generic wire-envelope internals
// when models/docker/registry's declared types already cover the
// externally-facing contract.
package internal
