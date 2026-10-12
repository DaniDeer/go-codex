package internal

import (
	"context"

	"github.com/DaniDeer/go-codex/api/rest"
	c "github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker/registry/auth"
)

// BearerAuthClientMW builds the GetTagsRoute/GetManifestRoute-shared
// Bearer-auth BoundClientMiddleware, generic over Req since
// rest.BoundSecurityClientMiddleware itself is — one instantiation per
// route (GetTagsReq/GetManifestReq), both delegating to the SAME a
// (Authenticator) + creds (this Client's own, construction-time-fixed
// credential) pair. The bound class (Fn receives the per-call Req) is
// REQUIRED here, not just convenient: the owning Client's *rest.Client
// attaches this middleware ONCE, at construction — the only way the SAME
// attached Fn can still learn which repository THIS call is for (a
// Bearer token is scoped to exactly one repository) is by reading it off
// repoOf(req).
//
// Reuses auth.BearerAuthSchemeName/BearerAuthScheme (the scheme
// metadata auth.BearerAuthDeclaration, the unattached PLAIN
// declaration, still exposes for a caller who wants spec-only metadata)
// and auth.BearerTokenCodec for the SAME Authorization header merge-field
// transform (anonymous access -> omitted header, non-empty token ->
// encoded header) BearerAuthDeclaration's own WithRequestHeader already
// declares — duplicated here (not shared via method call) only because
// WithRequestHeader is a method on two DIFFERENT builder types
// (Middleware vs BoundClientMiddleware), not a standalone composable
// value.
func BearerAuthClientMW[Req any](a *Authenticator, creds *auth.Credentials, repoOf func(Req) string) rest.BoundClientMiddleware[Req, auth.BearerCredential, struct{}] {
	return rest.BoundSecurityClientMiddleware[Req, auth.BearerCredential, struct{}](
		auth.BearerAuthSchemeName, auth.BearerAuthScheme, nil,
		func(ctx context.Context, req Req) (auth.BearerCredential, error) {
			token, err := a.CredentialFor(ctx, repoOf(req), creds)
			if err != nil {
				return auth.BearerCredential{}, err
			}
			return auth.BearerCredential{Token: token}, nil
		},
	).WithRequestHeader(rest.NewOmitEmptyHeaderParam("Authorization", auth.BearerTokenCodec,
		func(cred auth.BearerCredential) string { return cred.Token },
		func(cred *auth.BearerCredential, v string) { cred.Token = v },
	))
}

// basicAuthClientMW builds getTokenRoute's "basicAuth"-scheme BOUND
// client middleware — a fresh instance per authenticate() call
// (authenticator.go), closing over creds (nil for an anonymous exchange,
// non-nil for a private-repo token exchange). The BOUND class is used
// for consistency with BearerAuthClientMW above even though getTokenReq
// is deliberately ignored here — getTokenRoute's own call is a one-off
// per authenticate() invocation (never a persistent-client-attached
// route — the dynamic auth realm rules that out, see authenticator.go),
// so creds is simply closure-captured instead of read off the per-call
// Req. The Fn runs UNCONDITIONALLY — nil creds simply produces an empty
// auth.BasicAuthCredential, and NewOmitEmptyHeaderParam (below) omits the
// Authorization header entirely in that case (the anonymous-access
// behavior).
//
// Reuses auth.BasicAuthSchemeName/BasicAuthScheme (the scheme metadata
// auth.BasicAuthDeclaration, the unattached PLAIN declaration, mirrors
// BearerAuthDeclaration for a caller who wants spec-only metadata) —
// duplicated here (not shared via method call) for the same reason
// BearerAuthClientMW's own WithRequestHeader call is: it is a method on
// two DIFFERENT builder types (Middleware vs BoundClientMiddleware), not
// a standalone composable value.
func basicAuthClientMW(creds *auth.Credentials) rest.BoundClientMiddleware[getTokenReq, auth.BasicAuthCredential, struct{}] {
	return rest.BoundSecurityClientMiddleware[getTokenReq, auth.BasicAuthCredential, struct{}](
		auth.BasicAuthSchemeName, auth.BasicAuthScheme, nil,
		func(_ context.Context, _ getTokenReq) (auth.BasicAuthCredential, error) {
			if creds == nil {
				return auth.BasicAuthCredential{}, nil
			}
			basicAuth, err := formatBasicAuth(creds.Username, creds.Password)
			if err != nil {
				return auth.BasicAuthCredential{}, err
			}
			return auth.BasicAuthCredential{Authorization: basicAuth}, nil
		},
	).WithRequestHeader(rest.NewOmitEmptyHeaderParam("Authorization", c.String(),
		func(in auth.BasicAuthCredential) string { return in.Authorization },
		func(in *auth.BasicAuthCredential, v string) { in.Authorization = v },
	))
}
