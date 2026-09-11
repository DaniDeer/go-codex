package routes

import (
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/validate"
)

// BearerCodec validates a raw bearer token string's FORMAT (non-empty) —
// shared by every security declaration below, mirroring
// examples/rest-api/routes/middleware.go's identical BearerCodec.
var BearerCodec = codex.String().Refine(validate.NonEmptyString)

// BearerAuthMw declares the "bearerAuth" scheme — attached via
// .Use(BearerAuthMw) on every route that requires it (Phase 1 of
// docs/roadmap/reqreply-middleware.md), REPLACING the older
// reqreply.WithSecurityScheme + manual RouteMeta.Security declaration
// pattern still shown on routes.SecuredComputeRoute's own (deprecated)
// path — mirrors routes.ProfileScopeMw/AdminScopeMw's identical role in
// examples/rest-api.
var BearerAuthMw = middleware.SecurityScheme("bearerAuth", route.BearerScheme("JWT"), nil, &BearerCodec)
