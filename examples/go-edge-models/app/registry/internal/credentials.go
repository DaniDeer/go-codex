package internal

// RegistryBaseURL returns the HTTPS base URL to dial for a registry host
// — a small named helper (not a codec: deriving a dial address from an
// already-validated host string has no wire decode direction to model)
// shared by every self-contained registry client's own New(...)
// constructor, replacing separate inline "https://"+host concatenations.
func RegistryBaseURL(host string) string {
	return "https://" + host
}

// The shared auth-flow mechanics in this package (NewAuthenticator/
// Authenticator.CredentialFor/BearerAuthClientMW) operate directly on
// auth.Credentials (models/docker/registry/auth) — the SAME base type
// every per-registry client's own named Credentials type
// (dockerhub.Credentials, ghcr.Credentials, mcr.Credentials, or the
// generic fallback's own models/docker/registry/generic.Credentials
// alias) is built from. The caller converts its own typed Credentials
// into auth.Credentials once, at construction time, before handing it to
// NewAuthenticator/BearerAuthClientMW. A nil *auth.Credentials means
// anonymous access.
