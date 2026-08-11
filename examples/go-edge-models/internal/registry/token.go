package internal

import (
	c "github.com/DaniDeer/go-codex/codex"
)

// This file holds the registry token endpoint response concept — the
// struct and its codec.

// TokenResponse is the decoded response body of the registry token
// endpoint. Registries vary between "token" and "access_token" as the key
// name for the same value — both are modeled and callers should use
// whichever is non-empty.
type TokenResponse struct {
	Token       string
	AccessToken string
	ExpiresIn   int
}

// TokenResponseCodec decodes/encodes the registry token endpoint's JSON
// body. Both Token and AccessToken are OptionalField — registries vary
// between the two key names for the same value (the parent package's
// auth.go picks whichever is non-empty).
var TokenResponseCodec = c.Struct[TokenResponse](
	c.OptionalField("token", c.String(),
		func(t TokenResponse) string { return t.Token },
		func(t *TokenResponse, v string) { t.Token = v },
	),
	c.OptionalField("access_token", c.String(),
		func(t TokenResponse) string { return t.AccessToken },
		func(t *TokenResponse, v string) { t.AccessToken = v },
	),
	c.OptionalField("expires_in", c.Int(),
		func(t TokenResponse) int { return t.ExpiresIn },
		func(t *TokenResponse, v int) { t.ExpiresIn = v },
	),
)
