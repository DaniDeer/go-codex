package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/DaniDeer/go-codex/examples/reqreply-api/routes"
	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/route"
)

// VerifyBearer is BearerAuthMw's reusable-class WithReceive Fn —
// attached via auth.BearerAuthMw.WithReceive(auth.VerifyBearer), itself
// attached to a route via plain .Use(...). The credential's FORMAT
// (non-empty) is already validated by the built-in codec-based check
// (via reqreply.SecurityScheme.Codec) BEFORE this Fn ever runs; the raw
// "Authorization" MQTT5 User Property value is merge-field-decoded into
// in.Token DECLARATIVELY (BearerAuthMw's own WithRequestProperty) rather
// than read off *pahomqtt5.Publish by hand. Returns an unconditional
// grant — this example has only ONE scope-less scheme, so there is no
// scope-matching logic to demonstrate; a real implementation would look
// the extracted token up against an identity provider/database, mirroring
// examples/rest-api/auth's ExtractScopes.
func VerifyBearer(_ context.Context, in BearerAuthIn) (BearerAuthOut, error) {
	token := strings.TrimPrefix(in.Token, "Bearer ")
	_ = token // format already validated upstream; nothing further to check for this demo
	return BearerAuthOut{GrantedScopes: map[string][]string{"bearerAuth": nil}}, nil
}

// AlwaysRejectSecurityImpl is a reusable-class WithReceive Fn that ALWAYS
// rejects — used by demo_error_pattern.go's security-middleware +
// ErrorPattern combination demo to prove a declared
// reqreply.ErrorPattern[reqreply.SecurityError, ...] intercepts a
// SECURITY-MIDDLEWARE Fn failure (not just a handler failure). Attached
// via .Use(auth.BearerAuthMw.WithReceive(auth.AlwaysRejectSecurityImpl)),
// not auth.BearerAuthMw directly — Middleware is immutable, so this
// derived value shares the "bearerAuth" scheme name without ever
// mutating the shared base.
func AlwaysRejectSecurityImpl(_ context.Context, _ BearerAuthIn) (BearerAuthOut, error) {
	return BearerAuthOut{}, errors.New("access denied for demo")
}

// VerifyOAuth2Scopes is the SHARED, transport-agnostic OAuth2 verification
// logic — simulates a token-introspection call (mirrors
// examples/rest-api/auth's ExtractScopes simulation style, which does the
// same for REST's own bearer/scope model). Returns the granted scopes
// for a valid token, or an error for an invalid one.
//
// This function is deliberately the ONLY place OAuth2 verification logic
// lives — go-codex's declarative security mechanism lets the SAME
// scheme config (oauthComputeScheme/OAuthCodec) be declared via EITHER
// API's own vocabulary (reqreply.BoundSecurityMiddleware/
// rest.SecurityMiddleware) — but the PAIRED implementation Fn attached
// via HandleBoundMW/ClientMW cannot be shared literally, since each
// transport's Fn shape differs. The RECOMMENDED pattern demonstrated
// here: factor the actual verification logic into ONE shared helper like
// this one, then write a THIN, transport-specific wrapper Fn that only
// differs in HOW it extracts the raw token from its own transport (see
// demo_cross_api_oauth2_sharing.go's zeromqOAuthSecurityFn for the
// zeromq-side wrapper).
func VerifyOAuth2Scopes(token string) (map[string][]string, error) {
	switch token {
	case "valid-compute-write-token":
		return map[string][]string{"oauth2Compute": {OAuthComputeWriteScope}}, nil
	case "":
		return nil, fmt.Errorf("oauth2: missing token")
	default:
		return nil, fmt.Errorf("oauth2: token %q not recognized", token)
	}
}

// VerifyOAuthComputeZeroMQ is the THIN, zeromq-shaped BOUND security Fn —
// attached via reqreply.Route.HandleBoundMW(auth.NewOAuthMwReqreply(auth.VerifyOAuthComputeZeroMQ)).
// It ONLY extracts the raw token from zeromq's own transport shape (the
// decoded *routes.OAuthComputeReq's Token field — zeromq has no raw-
// message side channel, unlike mqtt5's User Properties) and delegates
// the REAL verification work to the SHARED VerifyOAuth2Scopes helper
// above — mirrors VerifyBearer (mqtt5-shaped) in spirit, but demonstrates
// the shared-helper factoring VerifyBearer's own (deliberately simple,
// unconditional-grant) demo didn't need. Returns a GrantedScopes-carrying
// OAuthOut, merged into the SAME middleware.CheckScopes call every
// Security attachment uses (see reqreply.BoundSecurityMiddleware's own
// godoc for the convention).
func VerifyOAuthComputeZeroMQ(_ context.Context, req *routes.OAuthComputeReq, _ struct{}) (OAuthOut, error) {
	scopes, err := VerifyOAuth2Scopes(req.Token)
	if err != nil {
		return OAuthOut{}, err
	}
	if err := middleware.CheckScopes([]route.SecurityRequirement{{"oauth2Compute": {OAuthComputeWriteScope}}}, scopes); err != nil {
		return OAuthOut{}, err
	}
	return OAuthOut{GrantedScopes: scopes}, nil
}

// gsTokenScopes is a mock credential store for the GrantedScopes demo —
// deliberately separate from any other scheme's own credential store (a
// different scheme, "bearerAuthGS", with its own scope vocabulary).
var gsTokenScopes = map[string][]string{
	"valid-compute-token":  {"compute:write"},
	"valid-readonly-token": {"profile"}, // lacks "compute:write" — proves rejection
}

// VerifyBearerGS is NewGrantedScopesComputeMw's embedded Fn — func(ctx,
// *Req, In) (Out, error), attached via
// auth.ComputeGSRoute.HandleBoundMW(auth.NewGrantedScopesComputeMw(auth.VerifyBearerGS)).
// Unlike the generic merge-field case, zeromq has no property/header
// side channel for In to decode FROM (GrantedScopesAuthIn is
// deliberately empty — see its own doc comment), so this Fn reads the
// credential directly off its OWN *Req parameter instead — the SAME
// established pattern VerifyOAuthComputeZeroMQ uses. Returns a
// GrantedScopes-carrying GrantedScopesAuthOut, PLUS Subject (the
// authenticated identity, propagated to the real handler via
// SetContextFieldFromOut).
func VerifyBearerGS(_ context.Context, req *ComputeGSReq, _ GrantedScopesAuthIn) (GrantedScopesAuthOut, error) {
	scopes, ok := gsTokenScopes[req.Token]
	if !ok {
		return GrantedScopesAuthOut{}, fmt.Errorf("unknown or expired token %q", req.Token)
	}
	return GrantedScopesAuthOut{
		GrantedScopes: map[string][]string{"bearerAuthGS": scopes},
		Subject:       req.Token,
	}, nil
}

// MakeComputeGSHandler is the real business handler — it never touches
// req.Token itself. The authenticated token reaches it PURELY via
// GrantedScopesUserIDField.Get(ctx), published by VerifyBearerGS above
// through SetContextFieldFromIn — the concrete "zero manual re-decoding"
// promise docs/design/d-0007-declarative-middleware-layering.md's Phase 3
// makes.
func MakeComputeGSHandler() func(ctx context.Context, req ComputeGSReq) (routes.ComputeResp, error) {
	return func(ctx context.Context, req ComputeGSReq) (routes.ComputeResp, error) {
		token, ok := GrantedScopesUserIDField.Get(ctx)
		fmt.Printf("  [handler] authenticated token (via ContextField, ok=%v): %q\n", ok, token)
		return routes.ComputeResp{Sum: req.X + req.Y}, nil
	}
}
