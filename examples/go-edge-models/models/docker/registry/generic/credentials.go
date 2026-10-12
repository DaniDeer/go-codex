package generic

import (
	c "github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker/registry/auth"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker/registry/dockerhub"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker/registry/ghcr"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker/registry/mcr"
	v "github.com/DaniDeer/go-codex/validate"
)

// This file holds the GENERIC, any-OCI-compliant-registry credential
// concept — used ONLY by app/registry's generic FALLBACK client
// (NewGenericClient), for any registry host OTHER than the 3 known ones
// (dockerhub/ghcr/mcr already have their own dedicated, self-contained
// clients + typed Credentials — see their own packages). Unlike those 3,
// there is no "wrong registry" mistake category to compile-time-guard
// against here (the whole point of this path is "I don't know/care which
// registry in advance"), so Credentials is a plain ALIAS of
// auth.Credentials, not a distinct named type.

// Credentials supplies Basic-auth credentials for the generic fallback
// client's auth-token exchange step — needed for private repositories on
// registries that require Basic auth to mint an access token. Anonymous/
// public pulls need no Credentials at all — passing WithCredentials is
// purely additive.
type Credentials = auth.Credentials

// CredentialsCodec validates a Credentials value — a direct re-export of
// auth.CredentialsCodec (Password required, Username unconstrained).
var CredentialsCodec = auth.CredentialsCodec

// NewCredentials is a named per-field smart constructor — a direct
// re-export of auth.NewCredentials.
func NewCredentials(username, password string) (Credentials, error) {
	return auth.NewCredentials(username, password)
}

// knownRegistryHosts lists every registry host WithCredentialsByRegistry/
// RegistryCredentialsCodec accepts as a map key — exactly the registries
// this module has been proven against end-to-end (see
// registry_integration_test.go). Sourced DIRECTLY from dockerhub/ghcr/
// mcr's own RegistryHost consts (not via models/docker/registry's
// config.go re-export) — this package has no need to depend on that
// package at all. This is the SOLE source of truth
// RegistryCredentialsCodec's key constraint is built from — add a new
// registry here (and prove it end-to-end) to expand what
// WithCredentialsByRegistry accepts; there is no separate list to keep in
// sync.
var knownRegistryHosts = []string{dockerhub.RegistryHost, ghcr.RegistryHost, mcr.RegistryHost}

// RegistryCredentials maps a registry host to the Credentials to use for
// that registry's token-exchange step. Supply ALL your registries'
// credentials via WithCredentialsByRegistry — GetTags/GetImageMetadata
// look up the right entry automatically based on the image URL's resolved
// registry host, making the call site itself registry-agnostic: the SAME
// options work unchanged no matter which registry a given image URL
// resolves to. A registry host with no matching entry falls back to
// anonymous/public access — same as omitting credentials entirely.
//
// Keys are restricted to the registries this package has been proven
// against end-to-end (see registry_integration_test.go) — Docker Hub,
// GHCR, MCR — via RegistryCredentialsCodec's key constraint. For any OTHER
// registry, use the single-value WithCredentials(Credentials{...}) option
// instead (unrestricted — works with any OCI-compliant registry).
type RegistryCredentials map[string]Credentials

// RegistryCredentialsCodec validates a RegistryCredentials map. Keys are
// restricted via validate.OneOf(knownRegistryHosts...) to the registries
// this package is proven against end-to-end — registry-1.docker.io
// (Docker Hub's actual API host), ghcr.io, mcr.microsoft.com. See
// RegistryCredentials' doc comment for the WithCredentials escape hatch
// when a different registry is needed.
var RegistryCredentialsCodec = c.Map(
	c.String().Refine(v.OneOf(knownRegistryHosts...)),
	CredentialsCodec,
)
