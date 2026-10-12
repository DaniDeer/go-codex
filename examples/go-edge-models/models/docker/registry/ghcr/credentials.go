package ghcr

import (
	"errors"

	c "github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker/registry/auth"
)

// RegistryHost is GitHub Container Registry's host — the SOURCE OF
// TRUTH models/docker/registry/generic's own knownRegistryHosts reads
// directly (no models/docker/registry re-export, unlike
// dockerhub.RegistryHost — see that constant's own doc comment for why
// Docker Hub's host is re-exported there and this one isn't: only
// imageref.go's Docker-Hub-specific domain-alias parsing needs a
// registry-root-level constant).
const RegistryHost = "ghcr.io"

// Credentials supplies Basic-auth credentials for GHCR's auth-token
// exchange step. UNLIKE Docker Hub/MCR, GHCR REQUIRES a real GitHub
// username — not just a token: GHCR's token-exchange step is standard
// HTTP Basic auth ("username:PAT"), and there is no bearer-only/
// username-less mode, even with a GITHUB_TOKEN or a GitHub App
// installation token (neither is accepted for GHCR push/pull outside a
// GitHub Actions workflow). A dedicated bot/machine GitHub account is
// the recommended way to rotate only the PAT (the actual expiring
// secret) in an unattended service without also touching the username
// each time. A DISTINCT Go type from dockerhub.Credentials/
// mcr.Credentials (even though the shape is identical today) — see
// dockerhub.Credentials' own doc comment for the full rationale. The
// underlying shape is written ONCE in auth.Credentials — this type is
// directly convertible to/from that shared base via a plain Go type
// conversion; CredentialsCodec (below) layers GHCR's EXTRA "Username
// required" constraint on top of auth.CredentialsCodec's base validation
// instead of redeclaring the struct's field codecs.
type Credentials auth.Credentials

// CredentialsCodec validates a Credentials value — BOTH Username and
// Password are required (unlike the generic/dockerhub/mcr codecs, which
// leave Username optional): GHCR's Basic-auth token exchange always
// needs a real username (see Credentials' own doc comment), so an empty
// one is a caller mistake worth catching here rather than at request
// time against the real registry. Built by layering a RefineFunc
// constraint on top of auth.CredentialsCodec's base validation (Password
// required, Username unconstrained) — the EXTRA "Username required" rule
// is the ONLY thing this package adds; the field codecs themselves are
// never redeclared. Lets a caller decode Credentials from an external
// config file (JSON/YAML/TOML) via format.<Format>(CredentialsCodec).
var CredentialsCodec = c.MapCodecSafe(
	auth.CredentialsCodec.RefineFunc(func(a auth.Credentials) error {
		if a.Username == "" {
			return errors.New("ghcr: username is required (GHCR's Basic-auth token exchange always needs a real GitHub username)")
		}
		return nil
	}),
	func(a auth.Credentials) Credentials { return Credentials(a) },
	func(cr Credentials) (auth.Credentials, error) { return auth.Credentials(cr), nil },
)

// Codec implements [codex.HasCodec][Credentials], returning
// [CredentialsCodec].
func (Credentials) Codec() c.Codec[Credentials] { return CredentialsCodec }

// NewCredentials is a named per-field smart constructor: validates
// both username and password (both non-empty, see Credentials' own doc
// comment) via CredentialsCodec.New and returns the constructed
// Credentials, or the zero value and the failing constraint's error.
func NewCredentials(username, password string) (Credentials, error) {
	return CredentialsCodec.New(Credentials{Username: username, Password: password})
}
