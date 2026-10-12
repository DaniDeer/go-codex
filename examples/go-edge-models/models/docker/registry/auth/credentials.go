package auth

import (
	"encoding/base64"
	"strings"

	c "github.com/DaniDeer/go-codex/codex"
	v "github.com/DaniDeer/go-codex/validate"
)

// This file holds the shared (username, password) credential concept —
// the struct, its base codec, and the Bearer/Basic auth-header encoding
// transforms built on top of it.

// Credentials is the shared (username, password) pair EVERY registry
// credential type in this module is ultimately built from — both the
// DOMAIN value a caller supplies directly (the generic fallback's own
// models/docker/registry/generic.Credentials is a plain alias of this
// type) or via a per-registry package's own DISTINCT named type
// (dockerhub.Credentials/ghcr.Credentials/mcr.Credentials, each
// convertible to/from this shape), and the WIRE-codec input
// BasicAuthCodec (below) encodes into a Basic Authorization header.
// Username is OPTIONAL — some registries authenticate correctly with an
// empty/arbitrary username and the actual token carried entirely in
// Password (e.g. a Docker Hub personal access token); Password is
// REQUIRED. A per-registry package needing a STRICTER constraint (e.g.
// GHCR's "Username always required") layers its own CredentialsCodec on
// top of CredentialsCodec via .RefineFunc(...) — see ghcr.CredentialsCodec
// — never redeclaring this struct or its base field codecs.
type Credentials struct {
	Username string
	Password string
}

// CredentialsCodec validates a Credentials value — Password must be
// non-empty; Username is left unconstrained (see Credentials' own doc
// comment). Lets a caller decode Credentials from an external config
// file (JSON/YAML/TOML) via format.<Format>(CredentialsCodec).
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

// ── Bearer token header ─────────────────────────────────────────────────

// BearerTokenCodec converts a bare token string to/from its Bearer-scheme
// Authorization header value ("Bearer <token>"). The decode direction
// (stripping the prefix) is provided for symmetry/reuse even though this
// package currently only ever encodes (constructs outgoing Authorization
// headers); a server-side or verification caller could reuse the same
// codec to extract a bare token from an incoming header.
var BearerTokenCodec = c.MapCodecSafe(
	c.String(),
	func(s string) string { return strings.TrimPrefix(s, "Bearer ") },
	func(token string) (string, error) { return "Bearer " + token, nil },
)

// ── Basic auth header ────────────────────────────────────────────────────

// BasicAuthCodec converts a Credentials value to/from an HTTP Basic
// "Authorization" header value ("Basic <base64(username:password)>"). The
// decode direction is a best-effort inverse (base64-decode, split on the
// first ":") — provided for symmetry, same rationale as BearerTokenCodec;
// this package currently only ever encodes (constructs outgoing
// Authorization headers for the auth-token exchange).
var BasicAuthCodec = c.MapCodecSafe(
	c.String(),
	func(s string) Credentials {
		encoded := strings.TrimPrefix(s, "Basic ")
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return Credentials{}
		}
		user, pass, ok := strings.Cut(string(decoded), ":")
		if !ok {
			return Credentials{}
		}
		return Credentials{Username: user, Password: pass}
	},
	func(creds Credentials) (string, error) {
		encoded := base64.StdEncoding.EncodeToString([]byte(creds.Username + ":" + creds.Password))
		return "Basic " + encoded, nil
	},
)
