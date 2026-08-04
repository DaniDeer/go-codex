package registry

import (
	"github.com/DaniDeer/go-codex/api/rest"
	c "github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/docker/registry/internal"
	"github.com/DaniDeer/go-codex/route"
)

// bearerAuthSecurity declares that a route requires Bearer-token
// credentials — set as RouteMeta.Security below so
// [nethttp.CallOptions.CredentialFunc] is invoked automatically by
// [nethttp.Call]/[nethttp.CallHandle], instead of the caller having to set
// the Authorization header by hand via CallOptions.ExtraHeaders. This
// package never registers an actual rest.SecurityScheme (there is no
// Builder/spec here — see routes.go's file doc comment) — the requirement
// alone is enough to trigger client-side CredentialFunc invocation.
var bearerAuthSecurity = []route.SecurityRequirement{{"bearerAuth": nil}}

// This file is the PRIMARY consumable contract of this package. Every
// route below is a plain [rest.Route] value — a downstream consumer can
// call .ClientHandle() on any of them directly and drive
// adapters/nethttp.Call with their own *http.Client, retry policy,
// observer, or security wiring, entirely independent of client.go's
// convenience orchestration (auth flow, manifest-list resolution). See
// client.go for the batteries-included GetTags/GetImageMetadata built on
// top of these same routes.

// PingRoute is the Docker Registry HTTP API v2 base check, GET /v2/. A 200
// response means the registry is reachable and does not require auth for
// this request; a 401 response carries a WWW-Authenticate challenge header
// (see client.go's authenticate). Response body is always empty.
var PingRoute = rest.NewRoute[struct{}, struct{}](
	"GET", "/v2/",
	c.Empty, c.Empty,
	rest.RouteMeta{
		OperationID: "ping",
		Summary:     "Docker Registry HTTP API v2 base check",
	},
)

// GetTagsRoute is GET /v2/{name}/tags/list — lists every tag for a
// repository. {name} is the full repository path (may itself contain "/",
// e.g. "bosch-cc-mfd/edge-gateway") — substituted as-is, no escaping
// needed (see BuildPath's plain string-replace semantics). Req is
// GetTagsReq, whose Name field merges into {name} automatically via
// nethttp.CallHandle (see client.go) — no manual vars map needed.
var GetTagsRoute = rest.NewRoute[GetTagsReq, TagsList](
	"GET", "/v2/{name}/tags/list",
	c.Struct[GetTagsReq](), TagsListCodec,
	rest.RouteMeta{
		OperationID:    "getTags",
		Summary:        "List every tag for a repository",
		RespSchemaName: "TagsList",
		Security:       bearerAuthSecurity,
	},
	rest.NewPathParam("name",
		c.String(),
		func(r GetTagsReq) string { return r.Name },
		func(r *GetTagsReq, v string) { r.Name = v },
	).WithDescription("Repository path"),
)

// GetManifestRoute is GET /v2/{name}/manifests/{reference} — fetches a
// manifest (single-platform) or a manifest list / OCI image index,
// dispatched automatically by internal.ManifestEnvelopeCodec based on the
// response shape. {reference} is a tag or a digest. Req is GetManifestReq,
// whose Name/Reference fields merge into {name}/{reference} automatically
// via nethttp.CallHandle. Resp additionally merges the
// Docker-Content-Digest RESPONSE HEADER directly into
// internal.ManifestEnvelope.Digest via rest.NewRequiredResponseHeaderParam
// — nethttp.Call/CallHandle applies this merge automatically on every
// successful (2xx) response, so client.go no longer needs a manual HTTP
// call just to read that header.
//
// Resp is internal.ManifestEnvelope — a consumer calling
// GetManifestRoute.ClientHandle() directly (bypassing the GetImageMetadata
// convenience function) receives a value of this type. Since
// docker/registry/internal is a true Go internal package, that type
// cannot be NAMED from outside docker/registry — but its EXPORTED fields
// (Digest, Single, List) remain readable via ordinary Go type inference
// (e.g. `env, _ := nethttp.Call(...); env.Digest`). This is intentional:
// GetManifestRoute stays usable standalone for advanced/low-level cases,
// while the internal package boundary makes it unambiguous that the raw
// envelope shape is plumbing — GetImageMetadata is the supported, fully
// resolved public result.
//
// The registry's response media type is negotiated via the Accept request
// header — client.go sends all four supported media types (Docker Schema
// 2 manifest, OCI manifest, Docker manifest list, OCI image index) so the
// registry can return whichever shape is appropriate for {reference}.
// This route does not declare Accept as a rest.HeaderParam because its
// value is a fixed protocol-negotiation constant, not a caller-supplied
// value — see client.go's acceptManifestTypes.
var GetManifestRoute = rest.NewRoute[GetManifestReq, internal.ManifestEnvelope](
	"GET", "/v2/{name}/manifests/{reference}",
	c.Struct[GetManifestReq](), internal.ManifestEnvelopeCodec,
	rest.RouteMeta{
		OperationID:    "getManifest",
		Summary:        "Fetch a manifest or manifest list for a repository reference",
		RespSchemaName: "ManifestEnvelope",
		Security:       bearerAuthSecurity,
	},
	rest.NewPathParam("name",
		c.String(),
		func(r GetManifestReq) string { return r.Name },
		func(r *GetManifestReq, v string) { r.Name = v },
	).WithDescription("Repository path"),
	rest.NewPathParam("reference",
		c.String(),
		func(r GetManifestReq) string { return r.Reference },
		func(r *GetManifestReq, v string) { r.Reference = v },
	).WithDescription("Tag or digest"),
	rest.NewRequiredResponseHeaderParam("Docker-Content-Digest",
		internal.DigestCodec,
		func(e internal.ManifestEnvelope) string { return e.Digest },
		func(e *internal.ManifestEnvelope, v string) { e.Digest = v },
	).WithDescription("The manifest's own content digest"),
)

// GetTokenRoute is the registry auth-token endpoint. Its path is
// deliberately EMPTY: the auth realm is an arbitrary full URL that may be
// on a COMPLETELY DIFFERENT HOST than the registry itself (e.g. Docker
// Hub's registry is registry-1.docker.io but its auth realm is
// auth.docker.io/token) — client.go passes the realm URL (parsed from the
// WWW-Authenticate challenge header) as the baseURL for this route's
// nethttp.Call, so the route's own path template must contribute nothing
// beyond that. Req is GetTokenReq, whose Service/Scope fields merge into
// the service/scope query params automatically via nethttp.CallHandle —
// both OptionalField since real registries vary in which of the two they
// actually populate in a challenge.
var GetTokenRoute = rest.NewRoute[GetTokenReq, internal.TokenResponse](
	"GET", "",
	c.Struct[GetTokenReq](), internal.TokenResponseCodec,
	rest.RouteMeta{
		OperationID:    "getToken",
		Summary:        "Fetch a Bearer token from the registry's auth realm",
		RespSchemaName: "TokenResponse",
	},
	rest.NewOptionalQueryParam("service",
		c.String(),
		func(r GetTokenReq) string { return r.Service },
		func(r *GetTokenReq, v string) { r.Service = v },
	),
	rest.NewOptionalQueryParam("scope",
		c.String(),
		func(r GetTokenReq) string { return r.Scope },
		func(r *GetTokenReq, v string) { r.Scope = v },
	),
)
