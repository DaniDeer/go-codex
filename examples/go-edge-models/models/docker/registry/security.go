package registry

import (
	"github.com/DaniDeer/go-codex/api/rest"
	c "github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/validate"
)

// BearerAuthSchemeName/BearerAuthScheme declare GetTagsRoute/
// GetManifestRoute's "bearerAuth" scheme metadata. Neither route ever
// implements a go-codex SERVER — the real server is an external OCI
// Distribution Spec registry (Docker Hub, GHCR, etc.) this package only
// ever calls AS A CLIENT. The scheme is still declared "from the server's
// perspective" — it documents what THAT external system requires — via
// [middleware.DeclareSecurity] below, attached with [rest.Route.Use]
// exactly like a real server route would (see
// docs/roadmap/declarative-middleware.md's "server declares, client
// fulfills" principle). [middleware.DeclareSecurity] deliberately has NO
// Fn: nothing in THIS codebase verifies the credential — that is the
// external registry's job. app/registry's own newAuthMiddleware supplies
// the credential CLIENT-side, attached via [rest.Route.UseClient]
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
// non-empty-string format Codec. app/registry's newAuthMiddleware's
// credential-supplying Fn gets a genuine extra safety net for free from
// this Codec: nethttp.Call validates its returned Authorization header's
// bare token against it before sending, on top of (not instead of) the
// fact that formatBearerToken/internal.BearerTokenCodec.Encode already
// construct that header from a codec — this catches an empty token
// specifically, which the encode-side codec alone does not.
var BearerAuthScheme = rest.SecurityScheme{
	SecurityScheme: route.BearerScheme(""),
}.WithCodec(c.String().Refine(validate.NonEmptyString))

// BearerAuthDeclaration is the spec-only [middleware.Middleware] GetTagsRoute/
// GetManifestRoute attach via [rest.Route.Use] — see BearerAuthSchemeName's
// own doc comment for why this codebase declares (but never enforces) this
// requirement.
var BearerAuthDeclaration = middleware.DeclareSecurity(BearerAuthSchemeName, BearerAuthScheme.SecurityScheme, nil, BearerAuthScheme.Codec)
