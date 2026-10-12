package auth

import (
	"github.com/DaniDeer/go-codex/api/rest"
	c "github.com/DaniDeer/go-codex/codex"
	v "github.com/DaniDeer/go-codex/validate"
)

// This file holds BOTH of this module's security-scheme declarations —
// the token-USE concern (BearerAuth*, attaching an already-obtained
// Bearer token to the ACTUAL registry API calls, GetTagsRoute/
// GetManifestRoute) and the token-EXCHANGE concern (BasicAuth*, the
// Basic-auth credentials used to MINT that token in the first place, via
// app/registry/internal's own getTokenRoute). Both follow the SAME
// declaration/handler split: the scheme/codec/credential-carrier
// metadata — the SPEC-ONLY [rest.SecurityMiddleware] declaration, no Fn
// bundled — lives here, in models; the actual credential-supplying Fn
// (the HANDLER) lives in app/registry/internal/middleware.go
// (BearerAuthClientMW/basicAuthClientMW), attached via
// [rest.Route.ClientMW]/[rest.BoundSecurityClientMiddleware].

// BearerAuthSchemeName/BearerAuthScheme declare models/docker/registry's
// GetTagsRoute/GetManifestRoute's "bearerAuth" scheme metadata. Neither
// route ever implements a go-codex SERVER — the real server is an
// external OCI Distribution Spec registry (Docker Hub, GHCR, etc.) that
// package only ever calls AS A CLIENT. The scheme is still declared
// "from the server's perspective" — it documents what THAT external
// system requires — via [rest.SecurityMiddleware] below, attached with
// [rest.Route.Use] exactly like a real server route would — this mirrors
// HandleMW/ClientMW's "server declares, client fulfills" split
// (docs/design/d-0001-rest-middleware-workflow-simplification.md).
// [rest.SecurityMiddleware] deliberately has NO
// Fn: nothing in THIS codebase verifies the credential — that is the
// external registry's job. app/registry/internal's own
// BearerAuthClientMW supplies the credential CLIENT-side, attached via
// [rest.Route.ClientMW] — see its own doc comment for the full flow.
const BearerAuthSchemeName = "bearerAuth"

// BearerAuthScheme declares the "bearerAuth" scheme's spec metadata and a
// non-empty-string format Codec. app/registry/internal's
// BearerAuthClientMW's credential-supplying Fn gets a genuine extra
// safety net for free from
// this Codec: nethttp.Call validates its returned Authorization header's
// bare token against it before sending, on top of (not instead of) the
// fact that BearerCredential's own merge-field codec (BearerTokenCodec,
// credentials.go) already constructs that header value — this catches an
// empty token specifically, which the encode-side codec alone does not.
var BearerAuthScheme = rest.BearerScheme("").WithCodec(c.String().Refine(v.NonEmptyString))

// BearerCredential is the codec-declared merge-field carrier for
// GetTagsRoute/GetManifestRoute's bearerAuth credential — docs/roadmap/
// declarative-middleware-layering.md's Rollout Phase A: the "bearer token
// -> Authorization header" transform, and the anonymous-access
// (empty-token -> no header at all) case, are now BOTH declared via a
// codec-backed [MergedHeaderParam] rather than app/registry/internal's
// BearerAuthClientMW hand-building an http.Header value directly. Token
// is the BARE token (no "Bearer " prefix) — BearerTokenCodec
// (credentials.go) handles the wire-format transform directly
// (BearerAuthDeclaration's own merge field below).
type BearerCredential struct {
	Token string
}

// BearerAuthDeclaration is the codec-backed middleware GetTagsRoute/
// GetManifestRoute attach via [rest.Route.Use] (spec-only there — no Fn
// bundled) — see BearerAuthSchemeName's own doc comment for why this
// codebase declares (but never enforces) this requirement.
// app/registry/internal's BearerAuthClientMW supplies the credential
// CLIENT-side, attached via [rest.Route.ClientMW] — its Fn's shape
// (func(ctx, Req) (BearerCredential, error)) is recognized by ClientMW's
// bound-path shape detection automatically, dispatching through the SAME
// merge-field mechanism this declaration's WithRequestHeader registers
// below: an empty Token omits the Authorization header entirely (the
// anonymous-access case, [rest.NewOmitEmptyHeaderParam]'s own documented
// motivation), a non-empty Token encodes it via BearerTokenCodec.
var BearerAuthDeclaration = rest.SecurityMiddleware[BearerCredential, struct{}](BearerAuthSchemeName, BearerAuthScheme, nil).
	WithRequestHeader(rest.NewOmitEmptyHeaderParam("Authorization", BearerTokenCodec,
		func(cred BearerCredential) string { return cred.Token },
		func(cred *BearerCredential, v string) { cred.Token = v },
	))

// ── Basic auth (token-exchange) ─────────────────────────────────────────

// BasicAuthSchemeName/BasicAuthScheme declare app/registry/internal's own
// getTokenRoute (the auth-token EXCHANGE endpoint — minting the Bearer
// token BearerAuthDeclaration above then attaches, not part of this
// package's externally-facing GetTags/GetManifest contract) "basicAuth"
// scheme metadata — mirrors BearerAuthSchemeName/BearerAuthScheme's own
// shape exactly, for the same reason: the SCHEME/CODEC/CREDENTIAL-CARRIER
// metadata is pure spec plumbing, reused by exactly one real handler
// (app/registry/internal/middleware.go's basicAuthClientMW), so it
// belongs here rather than being declared inline, ad hoc, at that one
// call site.
const BasicAuthSchemeName = "basicAuth"

// BasicAuthScheme declares the "basicAuth" scheme's spec metadata and a
// non-empty-string format Codec for the already-encoded
// "Basic <base64(user:pass)>" header value (BasicAuthCodec,
// credentials.go, builds that value; this Codec only guards against an
// empty result reaching the wire).
var BasicAuthScheme = rest.BasicScheme().WithCodec(c.String().Refine(v.NonEmptyString))

// BasicAuthCredential is the codec-declared merge-field carrier for
// getTokenRoute's basicAuth credential — Authorization already holds the
// fully-formatted "Basic <base64>" header value (BasicAuthCodec,
// credentials.go); an empty value means anonymous access (no credentials
// supplied), omitting the header entirely via
// [rest.NewOmitEmptyHeaderParam] below, exactly like BearerCredential's
// own empty-Token case.
type BasicAuthCredential struct {
	Authorization string
}

// BasicAuthDeclaration is the codec-backed middleware getTokenRoute
// attaches via [rest.Route.Use] (spec-only there — no Fn bundled) —
// mirrors BearerAuthDeclaration's own shape exactly.
// app/registry/internal's basicAuthClientMW supplies the credential
// CLIENT-side, attached via [rest.BoundSecurityClientMiddleware].
var BasicAuthDeclaration = rest.SecurityMiddleware[BasicAuthCredential, struct{}](BasicAuthSchemeName, BasicAuthScheme, nil).
	WithRequestHeader(rest.NewOmitEmptyHeaderParam("Authorization", c.String(),
		func(cred BasicAuthCredential) string { return cred.Authorization },
		func(cred *BasicAuthCredential, v string) { cred.Authorization = v },
	))
