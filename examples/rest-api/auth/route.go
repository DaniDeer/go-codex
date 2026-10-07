package auth

import (
	"net/http"

	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/validate"
)

// ── Auth boundary: login ─────────────────────────────────────────────────────

// LoginReq is what an HTTP client sends to authenticate.
type LoginReq struct {
	Username string
	Password string
}

// LoginReqCodec enforces non-empty username/password.
var LoginReqCodec = codex.Struct[LoginReq](
	codex.RequiredField("username",
		codex.String().Refine(validate.NonEmptyString).WithDescription("Username."),
		func(r LoginReq) string { return r.Username },
		func(r *LoginReq, v string) { r.Username = v },
	),
	codex.RequiredField("password",
		codex.String().Refine(validate.NonEmptyString).WithDescription("Password."),
		func(r LoginReq) string { return r.Password },
		func(r *LoginReq, v string) { r.Password = v },
	),
)

// TokenResp carries the issued bearer token.
type TokenResp struct {
	Token string
}

// TokenRespCodec describes the login response.
var TokenRespCodec = codex.Struct[TokenResp](
	codex.RequiredField("token",
		codex.String().WithDescription("****** for subsequent requests."),
		func(r TokenResp) string { return r.Token },
		func(r *TokenResp, v string) { r.Token = v },
	),
)

// LoginErrorPayload is the typed response body for LoginRoute's invalid-
// credentials case — replaces a FORMER hand-rolled errors.As dispatch
// inside chiserver/nethttpserver's shared adapter ErrorHandler.
type LoginErrorPayload struct {
	Message string
}

// LoginErrorPayloadCodec describes LoginErrorPayload.
var LoginErrorPayloadCodec = codex.Struct[LoginErrorPayload](
	codex.RequiredField("message",
		codex.String().WithDescription("Why the login attempt was rejected."),
		func(p LoginErrorPayload) string { return p.Message },
		func(p *LoginErrorPayload, v string) { p.Message = v },
	),
)

// InvalidCredentialsError is returned by MakeLoginHandler when the
// username or password is wrong — matched by LoginRoute's declared
// rest.ErrorPattern below.
type InvalidCredentialsError struct{ Err error }

func (e InvalidCredentialsError) Error() string { return e.Err.Error() }
func (e InvalidCredentialsError) Unwrap() error { return e.Err }

// LoginRoute — POST /login — PUBLIC, no security declaration. Issues a
// mock bearer token for a valid username/password pair. The declared
// rest.ErrorPattern below replaces a FORMER hand-rolled errors.As
// dispatch that used to live inside chiserver/nethttpserver's shared
// adapter ErrorHandler — invalid credentials now get a typed,
// OpenAPI-documented 401 body instead of the generic {"error": "..."}
// envelope, with zero imperative dispatch code in the adapter layer. See
// demo_login.go's negative-path call, which recovers the payload
// client-side via rest.ErrorPatternAs.
var LoginRoute = rest.NewRoute[LoginReq, TokenResp]("POST", "/login",
	LoginReqCodec, TokenRespCodec,
	rest.RouteMeta{OperationID: "login", Summary: "Authenticate and receive a bearer token", Tags: []string{"auth"}},
	rest.ErrorPattern[InvalidCredentialsError, LoginErrorPayload](http.StatusUnauthorized, LoginErrorPayloadCodec,
		func(e InvalidCredentialsError) (LoginErrorPayload, error) {
			return LoginErrorPayload{Message: e.Error()}, nil
		},
	),
)

// ── GrantedScopes + ContextField demo route ──────────────────────────────────

// ComputeGSReq/ComputeGSResp are deliberately trivial — this demo's
// entire point is the security/ContextField mechanism, not the business
// payload.
type ComputeGSReq struct{ X, Y int }
type ComputeGSResp struct{ Sum int }

var computeGSReqCodec = codex.Struct[ComputeGSReq](
	codex.RequiredField("x", codex.Int(), func(r ComputeGSReq) int { return r.X }, func(r *ComputeGSReq, v int) { r.X = v }),
	codex.RequiredField("y", codex.Int(), func(r ComputeGSReq) int { return r.Y }, func(r *ComputeGSReq, v int) { r.Y = v }),
)
var computeGSRespCodec = codex.Struct[ComputeGSResp](
	codex.RequiredField("sum", codex.Int(), func(r ComputeGSResp) int { return r.Sum }, func(r *ComputeGSResp, v int) { r.Sum = v }),
)

// ComputeGSRoute declares NO security requirement itself — HandleBoundMW/
// ClientBoundMW (via GrantedScopesComputeServerMW/
// GrantedScopesComputeClientMW, attached in chiserver/server.go/
// client/client.go) populate rb.meta.Security/rb.securitySchemes
// automatically on whichever concrete route value they attach to
// (docs/design/d-0003-codec-declared-middlewares.md's Addendum 7 —
// BoundMiddleware.applyBoundRoute contributes to rb.middlewares, which
// both Register's applySecurityDeclarations AND ClientHandle's
// applyMiddlewareSecurityForClient already read).
var ComputeGSRoute = rest.NewRoute[ComputeGSReq, ComputeGSResp]("POST", "/compute-gs",
	computeGSReqCodec, computeGSRespCodec,
	rest.RouteMeta{
		OperationID: "computeGS",
		Summary:     "Compute (GrantedScopes + ContextField demo)",
		Tags:        []string{"granted-scopes"},
	},
)
