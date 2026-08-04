// Package internal holds the generic OCI Distribution Spec / Docker
// Registry HTTP API v2 wire-shape and auth-flow plumbing that the sibling
// docker/registry package's public API is built on: manifest/manifest-list
// envelope types, the WWW-Authenticate challenge, the Docker auth-scope
// string, the "os/arch" platform selector, and their codecs.
//
// This is a true Go internal package — the compiler only allows it to be
// imported by code rooted at docker/registry (docker/registry itself, and
// any of docker/registry's OWN subpackages), never by a sibling package or
// an external module. That is a deliberate, enforced boundary, not just a
// naming convention: docker/registry's public files (types.go, codecs.go,
// routes.go, client.go) are the ONLY supported surface for a consumer of
// this library.
//
// IMPORTANT for future work: this package must stay PURELY GENERIC OCI/
// Docker Distribution Spec plumbing — it must never absorb a
// registry-specific quirk (e.g. a GHCR-only pagination scheme, an
// MCR-only catalog extension). Registry-specific integrations belong in
// their OWN future sibling packages under docker/registry/ (e.g.
// docker/registry/ghcr, docker/registry/mcr — mirroring the
// iotedge/iotedge-modulepatch sibling-package precedent), composing
// docker/registry's PUBLIC contract. Such packages have NO access to this
// internal package (Go's import rule enforces it), which is the correct
// boundary: registry-specific code has no business depending on the
// generic wire-envelope internals.
package internal
