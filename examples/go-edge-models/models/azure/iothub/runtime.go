package iothub

import (
	c "github.com/DaniDeer/go-codex/codex"
	v "github.com/DaniDeer/go-codex/validate"
)

// ── RegistryCredential / RegistryCredentials ──────────────────────────────────
//
// Wire: {"ghcrBoschCcMfd": {"address": "ghcr.io", "username": "user", "password": "${GHCR_IO_READ_PACKAGES_PAT}"}, ...}
//
// A DIFFERENT shape than
// [github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker/registry.RegistryCredentials]
// (that type is keyed DIRECTLY by registry host, for client-side
// registry-API auth) — this one is keyed by an ARBITRARY credential-set
// ALIAS (e.g. "ghcrBoschCcMfd"), and each entry carries its OWN "address"
// field naming which registry host it authenticates against. Values are
// frequently CI/CD placeholder strings (e.g. "${GHCR_IO_READ_PACKAGES_PAT}")
// substituted by a deployment pipeline before the manifest ever reaches
// IoT Hub — these need no special type, exactly like
// EnvVar's own string values already accept "${...}"
// placeholders unconstrained.

// RegistryCredential is ONE named credential-set entry under
// runtime.settings.registryCredentials.
type RegistryCredential struct {
	// Address is the registry host this credential-set authenticates
	// against, e.g. "ghcr.io".
	Address string
	// Username authenticates alongside Password. Some registries (e.g.
	// GHCR with a PAT) work with an arbitrary/empty username, so this is
	// deliberately unconstrained — mirrors
	// docker/registry.Credentials.Username's own rationale.
	Username string
	// Password is frequently a CI/CD placeholder string (e.g.
	// "${GHCR_IO_READ_PACKAGES_PAT}"), substituted before deployment —
	// validated as non-empty only (the ACTUAL secret is never this
	// package's concern).
	Password string
}

// RegistryCredentialCodec validates/encodes a RegistryCredential —
// Address/Password are required; Username is optional (see its own
// doc comment above).
var RegistryCredentialCodec = c.Struct[RegistryCredential](
	c.RequiredField("address", c.String().Refine(v.NonEmptyString),
		func(rc RegistryCredential) string { return rc.Address },
		func(rc *RegistryCredential, val string) { rc.Address = val },
	),
	c.OptionalField("username", c.String(),
		func(rc RegistryCredential) string { return rc.Username },
		func(rc *RegistryCredential, val string) { rc.Username = val },
	),
	c.RequiredField("password", c.String().Refine(v.NonEmptyString),
		func(rc RegistryCredential) string { return rc.Password },
		func(rc *RegistryCredential, val string) { rc.Password = val },
	),
)

// RegistryCredentials maps an arbitrary credential-set alias (e.g.
// "ghcrBoschCcMfd") to its RegistryCredential — the alias is a caller-
// chosen label, not a registry host, so no name constraint applies
// beyond being a non-empty string.
type RegistryCredentials map[string]RegistryCredential

var registryCredentialsAliasCodec = c.String().Refine(v.NonEmptyString)

// RegistryCredentialsCodec decodes/encodes the "registryCredentials"
// object into RegistryCredentials via codex.Map.
var RegistryCredentialsCodec = c.Map[string, RegistryCredential](
	registryCredentialsAliasCodec, RegistryCredentialCodec,
)

// ── RuntimeSettings / Runtime ──────────────────────────────────────────────────

// RuntimeSettings is $edgeAgent's "runtime.settings" document.
type RuntimeSettings struct {
	// MinDockerVersion is the minimum Docker Engine version required,
	// e.g. "v1.25".
	MinDockerVersion string
	// RegistryCredentials is OPTIONAL — a use case with only public
	// images needs none.
	RegistryCredentials RegistryCredentials
}

var RuntimeSettingsCodec = c.Struct[RuntimeSettings](
	c.RequiredField("minDockerVersion", c.String().Refine(v.NonEmptyString),
		func(rs RuntimeSettings) string { return rs.MinDockerVersion },
		func(rs *RuntimeSettings, val string) { rs.MinDockerVersion = val },
	),
	c.OptionalField("registryCredentials", RegistryCredentialsCodec,
		func(rs RuntimeSettings) map[string]RegistryCredential { return rs.RegistryCredentials },
		func(rs *RuntimeSettings, val map[string]RegistryCredential) {
			rs.RegistryCredentials = RegistryCredentials(val)
		},
	),
)

// Runtime is $edgeAgent's "runtime" document — its own settings plus a
// fixed "type" ("docker"), mirroring ModuleConfig's own
// "type" field convention.
type Runtime struct {
	Settings RuntimeSettings
	Type     Type
}

var RuntimeCodec = c.Struct[Runtime](
	c.RequiredField("settings", RuntimeSettingsCodec,
		func(r Runtime) RuntimeSettings { return r.Settings },
		func(r *Runtime, val RuntimeSettings) { r.Settings = val },
	),
	c.RequiredField("type", TypeCodec,
		func(r Runtime) Type { return r.Type },
		func(r *Runtime, val Type) { r.Type = val },
	),
)
