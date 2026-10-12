package dockerhub

import (
	c "github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker/registry/auth"
)

// RegistryHost is the ACTUAL host app/registry/dockerhub's Client calls
// — Docker Hub's reference domain ("docker.io") is NOT itself a
// reachable registry API host; "registry-1.docker.io" is. This is the
// SOURCE OF TRUTH models/docker/registry's own config.go re-exports as
// DockerHubRegistryHost (imageref.go's own parsing logic needs it
// alongside that file's Docker-Hub-only domain aliases; this constant's
// canonical home is here).
const RegistryHost = "registry-1.docker.io"

// Credentials supplies Basic-auth credentials for Docker Hub's
// auth-token exchange step — needed for private repositories; anonymous/
// public pulls need no Credentials at all. A DISTINCT Go type from
// ghcr.Credentials/mcr.Credentials (even though the shape is identical
// today) — passing one registry's credentials to another's Client is a
// compile error, not a silent runtime misconfiguration, and either
// registry's own auth requirements can diverge independently in the
// future without affecting the others. The underlying shape and base
// validation (Password required, Username optional) are written ONCE in
// auth.Credentials/auth.CredentialsCodec — this type is directly
// convertible to/from that shared base via a plain Go type conversion
// (Credentials(someAuthCredentials)/auth.Credentials(someCredentials)).
type Credentials auth.Credentials

// CredentialsCodec validates a Credentials value — a thin wrapper
// renaming auth.CredentialsCodec's validation (Password required,
// Username optional) onto this package's own named Credentials type. Lets
// a caller decode Credentials from an external config file (JSON/YAML/
// TOML) via format.<Format>(CredentialsCodec).
var CredentialsCodec = c.MapCodecSafe(auth.CredentialsCodec,
	func(a auth.Credentials) Credentials { return Credentials(a) },
	func(cr Credentials) (auth.Credentials, error) { return auth.Credentials(cr), nil },
)

// Codec implements [codex.HasCodec][Credentials], returning
// [CredentialsCodec].
func (Credentials) Codec() c.Codec[Credentials] { return CredentialsCodec }

// NewCredentials is a named per-field smart constructor: validates
// password (non-empty; username is deliberately unconstrained, see
// Credentials' own doc comment) via CredentialsCodec.New and returns the
// constructed Credentials, or the zero value and the failing constraint's
// error.
func NewCredentials(username, password string) (Credentials, error) {
	return CredentialsCodec.New(Credentials{Username: username, Password: password})
}
