package internal

import (
	"encoding/base64"
	"strings"

	c "github.com/DaniDeer/go-codex/codex"
)

// This file holds the Bearer/Basic auth-header encoding concept — small
// header-value transforms with no shared struct between the two (Bearer
// is a bare string transform; Basic's structured side is
// BasicCredentials, below).

// BasicCredentials is a username/password pair encoded/decoded to/from an
// HTTP Basic "Authorization" header value via BasicAuthCodec (above).
// Needed for the auth-token exchange on registries/repositories that
// require Basic auth to mint a Bearer token (e.g. a private GHCR package,
// authenticated with a GitHub username + a PAT with read:packages scope).
type BasicCredentials struct {
	Username string
	Password string
}

// ── Bearer token ──────────────────────────────────────────────────────────────

// BearerTokenCodec converts a bare token string to/from the
// "Bearer <token>" Authorization header value. The decode direction
// (stripping the prefix) is provided for symmetry/reuse even though this
// package currently only ever encodes (constructs outgoing Authorization
// headers); a server-side or verification caller could reuse the same
// codec to extract a bare token from an incoming header.
var BearerTokenCodec = c.MapCodecSafe(
	c.String(),
	func(s string) string { return strings.TrimPrefix(s, "Bearer ") },
	func(token string) (string, error) { return "Bearer " + token, nil },
)

// ── Basic auth ────────────────────────────────────────────────────────────────

// BasicAuthCodec converts a username/password pair to/from an HTTP Basic
// "Authorization" header value ("Basic <base64(username:password)>"). The
// decode direction is a best-effort inverse (base64-decode, split on the
// first ":") — provided for symmetry, same rationale as BearerTokenCodec;
// this package currently only ever encodes (constructs outgoing
// Authorization headers for the auth-token exchange).
var BasicAuthCodec = c.MapCodecSafe(
	c.String(),
	func(s string) BasicCredentials {
		encoded := strings.TrimPrefix(s, "Basic ")
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return BasicCredentials{}
		}
		user, pass, ok := strings.Cut(string(decoded), ":")
		if !ok {
			return BasicCredentials{}
		}
		return BasicCredentials{Username: user, Password: pass}
	},
	func(creds BasicCredentials) (string, error) {
		encoded := base64.StdEncoding.EncodeToString([]byte(creds.Username + ":" + creds.Password))
		return "Basic " + encoded, nil
	},
)
