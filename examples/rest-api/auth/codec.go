// Package auth is this example's SELF-CONTAINED auth module — codecs,
// middleware declarations, verifier/handler IMPLEMENTATIONS, and
// auth-flow demo ROUTES (LoginRoute, ComputeGSRoute) all live together
// here, modeling how a real service would factor out a reusable auth
// library consumed by multiple services (mirrors examples/reqreply-api's
// own auth/ package — same reasoning, same per-file split). Plain
// BUSINESS routes that merely ATTACH this package's middleware (e.g.
// CreateUserRoute/ProfileRoute) stay in routes/, importing auth.X — a
// one-way dependency: auth/ MAY import routes/ (e.g. for CreateUserReq's
// Req type parameter in BoundScopeServerMW's instantiations elsewhere),
// routes/ must NEVER import auth/ (would create a cycle).
package auth

import (
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/validate"
)

// BearerCodec validates a raw bearer token string's FORMAT (non-empty,
// well-formed) — shared by every security declaration below so the check
// is defined once.
var BearerCodec = codex.String().Refine(validate.BearerToken)
