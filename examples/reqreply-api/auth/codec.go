// Package auth is a self-contained auth module for the reqreply-api
// example — codecs, middleware declarations, verifier/handler
// implementations, AND auth-flow routes (the GrantedScopes demo route)
// all live together here, modeling how a real service would factor out
// a reusable auth library consumed by multiple services, rather than
// splitting declaration from implementation across packages. Domain
// routes that merely ATTACH this package's middleware (e.g.
// routes.SecuredComputeRoute) stay in routes/ — only auth itself (and
// its own dedicated demo route, ComputeGSRoute) lives here.
package auth

import (
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/validate"
)

// BearerCodec validates a raw bearer token string's FORMAT (non-empty) —
// shared by every security declaration below, mirroring
// examples/rest-api/auth's identical BearerCodec.
var BearerCodec = codex.String().Refine(validate.NonEmptyString)

// OAuthCodec validates a raw OAuth2 bearer token string's FORMAT
// (non-empty) — same role as BearerCodec above.
var OAuthCodec = codex.String().Refine(validate.NonEmptyString)
