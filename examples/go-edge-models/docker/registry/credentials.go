package registry

import (
	c "github.com/DaniDeer/go-codex/codex"
	v "github.com/DaniDeer/go-codex/validate"
)

// ghcrRegistryHost is GitHub Container Registry's host.
const ghcrRegistryHost = "ghcr.io"

// mcrRegistryHost is Microsoft Container Registry's host.
const mcrRegistryHost = "mcr.microsoft.com"

// knownRegistryHosts lists every registry host WithCredentialsByRegistry/
// RegistryCredentialsCodec accepts as a map key — exactly the registries
// this package has been proven against end-to-end (see
// registry_integration_test.go). This is the SOLE source of truth
// RegistryCredentialsCodec's key constraint is built from (via
// validate.OneOf(knownRegistryHosts...)) — add a new registry here (and
// prove it end-to-end) to expand what WithCredentialsByRegistry accepts;
// there is no separate list to keep in sync. dockerHubRegistryHost is
// declared in imageref.go (same package, referenced directly here).
var knownRegistryHosts = []string{dockerHubRegistryHost, ghcrRegistryHost, mcrRegistryHost}

// ── Credentials / RegistryCredentials ─────────────────────────────────────────

// Credentials supplies Basic-auth credentials for the auth-token exchange
// step (getTokenRoute) — needed for private repositories on registries
// that require Basic auth to mint a ****** e.g. a private GHCR
// package authenticated with a GitHub username + a PAT with
// read:packages scope. Anonymous/public pulls need no Credentials at
// all — passing WithCredentials is purely additive; GetTags/
// GetImageMetadata behave exactly as before when it is omitted.
type Credentials struct {
	Username string
	Password string
}

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

// CredentialsCodec validates a Credentials value — Password must be
// non-empty; Username is OPTIONAL: some registries (e.g. GHCR with a
// personal access token) authenticate correctly with an empty/arbitrary
// username and the actual token carried entirely in Password, so
// Username is deliberately not constrained here. Lets a caller decode
// Credentials/RegistryCredentials from an external config file (JSON/
// YAML/TOML) via format.<Format>(CredentialsCodec)/(RegistryCredentialsCodec).
var CredentialsCodec = c.Struct[Credentials](
	c.OptionalField("username", c.String(),
		func(cr Credentials) string { return cr.Username },
		func(cr *Credentials, val string) { cr.Username = val },
	),
	c.RequiredField("password", c.String().Refine(v.NonEmptyString),
		func(cr Credentials) string { return cr.Password },
		func(cr *Credentials, val string) { cr.Password = val },
	),
)

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
