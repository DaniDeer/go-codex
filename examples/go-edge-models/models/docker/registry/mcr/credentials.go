package mcr

import (
	c "github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker/registry/auth"
)

// RegistryHost is Microsoft Container Registry's host — see
// ghcr.RegistryHost's own doc comment for why this one has no
// models/docker/registry root-level re-export (unlike
// dockerhub.RegistryHost).
const RegistryHost = "mcr.microsoft.com"

// Credentials supplies Basic-auth credentials for MCR's auth-token
// exchange step. MCR's own public images need NO credentials at all —
// this type exists for completeness/future-proofing (an enterprise MCR
// deployment could require auth) and for a consistent per-registry API
// shape alongside dockerhub.Credentials/ghcr.Credentials, not because any
// image this module is proven against needs it today. A DISTINCT Go type
// from dockerhub.Credentials/ghcr.Credentials (even though the shape is
// identical today) — see dockerhub.Credentials' own doc comment for the
// full rationale. The underlying shape and base validation (Password
// required, Username optional) are written ONCE in
// auth.Credentials/auth.CredentialsCodec — this type is directly
// convertible to/from that shared base via a plain Go type conversion.
type Credentials auth.Credentials

// CredentialsCodec validates a Credentials value — a thin wrapper
// renaming auth.CredentialsCodec's validation (Password required,
// Username optional — mirroring dockerhub.CredentialsCodec, not
// ghcr.CredentialsCodec's stricter both-required shape: MCR has no
// GHCR-style "always needs a real username" constraint) onto this
// package's own named Credentials type. Lets a caller decode Credentials
// from an external config file (JSON/YAML/TOML) via
// format.<Format>(CredentialsCodec).
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
