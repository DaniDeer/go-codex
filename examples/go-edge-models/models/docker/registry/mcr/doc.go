// Package mcr declares Microsoft-Container-Registry-SPECIFIC models —
// RegistryHost (the actual dialable registry API host) and a dedicated
// Credentials type (`type Credentials auth.Credentials` — the shared
// base shape/validation lives ONCE in models/docker/registry/auth; this
// package only adds the DISTINCT Go type, never redeclaring the struct)
// — for the sibling models/docker/registry package's shared,
// registry-agnostic routes (PingRoute/GetTagsRoute/GetManifestRoute,
// which this package does NOT redeclare).
//
// The concrete, self-contained MCR HTTP client built on top of these
// models (and models/docker/registry's shared routes, plus
// models/docker/registry/auth's auth-flow codecs) lives in the sibling
// examples/go-edge-models/app/registry/mcr package instead — this
// package is pure data, no *http.Client, no I/O.
//
// See models/docker/registry's own doc comment for the full "what's
// shared here vs. what's registry-specific" rationale.
package mcr
