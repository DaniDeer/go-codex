package registry

import (
	"github.com/DaniDeer/go-codex/api/rest"
	c "github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/internal/registry"
	"github.com/DaniDeer/go-codex/validate"
)

// BearerAuthSchemeName/BearerAuthScheme declare GetTagsRoute/
// GetManifestRoute's "bearerAuth" scheme metadata. Neither route ever
// implements a go-codex SERVER — the real server is an external OCI
// Distribution Spec registry (Docker Hub, GHCR, etc.) this package only
// ever calls AS A CLIENT. The scheme is still declared "from the server's
// perspective" — it documents what THAT external system requires — via
// [rest.SecurityMiddleware] below, attached with [rest.Route.Use]
// exactly like a real server route would — this mirrors HandleMW/ClientMW's
// "server declares, client fulfills" split
// (docs/design/d-0001-rest-middleware-workflow-simplification.md).
// [rest.SecurityMiddleware] deliberately has NO
// Fn: nothing in THIS codebase verifies the credential — that is the
// external registry's job. app/registry's own newAuthCredentialFunc supplies
// the credential CLIENT-side, attached via [rest.Route.ClientMW]
// (app/registry's gettags.go/getimagemetadata.go) — see its own doc
// comment for the full flow.
//
// basicAuthSecurity/basicAuthScheme (used only by app/registry's own
// getTokenRoute, the token-exchange endpoint) stay in app/registry
// instead — getTokenRoute has no legitimate standalone caller outside
// that package's own authenticate() function, so it is auth-flow
// plumbing, not part of this package's externally-facing contract.
const BearerAuthSchemeName = "bearerAuth"

// BearerAuthScheme declares the "bearerAuth" scheme's spec metadata and a
// non-empty-string format Codec. app/registry's newAuthCredentialFunc's
// credential-supplying Fn gets a genuine extra safety net for free from
// this Codec: nethttp.Call validates its returned Authorization header's
// bare token against it before sending, on top of (not instead of) the
// fact that BearerCredential's own merge-field codec
// (internal.BearerTokenCodec) already constructs that header value — this
// catches an empty token specifically, which the encode-side codec alone
// does not.
var BearerAuthScheme = rest.BearerScheme("").WithCodec(c.String().Refine(validate.NonEmptyString))

// BearerCredential is the codec-declared merge-field carrier for
// GetTagsRoute/GetManifestRoute's bearerAuth credential — docs/roadmap/
// declarative-middleware-layering.md's Rollout Phase A: the "bearer token
// -> Authorization header" transform, and the anonymous-access
// (empty-token -> no header at all) case, are now BOTH declared via a
// codec-backed [MergedHeaderParam] rather than app/registry's
// newAuthCredentialFunc hand-building an http.Header value directly. Token
// is the BARE token (no "Bearer " prefix) — internal.BearerTokenCodec
// handles the wire-format transform directly (BearerAuthDeclaration's own
// merge field below, replacing app/registry's prior formatBearerToken
// helper).
type BearerCredential struct {
	Token string
}

// BearerAuthDeclaration is the codec-backed middleware GetTagsRoute/
// GetManifestRoute attach via [rest.Route.Use] (spec-only there — no Fn
// bundled) — see BearerAuthSchemeName's own doc comment for why this
// codebase declares (but never enforces) this requirement. app/registry's
// newAuthCredentialFunc supplies the credential CLIENT-side, attached via
// [rest.Route.ClientMW] — its Fn's shape (func(ctx, Req) (BearerCredential,
// error)) is recognized by ClientMW's bound-path shape detection
// automatically, dispatching through the SAME merge-field mechanism this
// declaration's WithRequestHeader registers below: an empty Token omits
// the Authorization header entirely (the anonymous-access case,
// [rest.NewOmitEmptyHeaderParam]'s own documented motivation), a non-empty
// Token encodes it via internal.BearerTokenCodec.
var BearerAuthDeclaration = rest.SecurityMiddleware[BearerCredential, struct{}](BearerAuthSchemeName, BearerAuthScheme, nil).
	WithRequestHeader(rest.NewOmitEmptyHeaderParam("Authorization", internal.BearerTokenCodec,
		func(cred BearerCredential) string { return cred.Token },
		func(cred *BearerCredential, v string) { cred.Token = v },
	))
