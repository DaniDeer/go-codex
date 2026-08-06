package registry

import (
	"github.com/DaniDeer/go-codex/api/rest"
	c "github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/docker/registry/internal"
)

// This file is the PRIMARY consumable contract of this package AND
// contains ONLY route declarations — no security scheme/requirement
// values are defined here. Every route below (PingRoute, GetTagsRoute,
// GetManifestRoute) is a plain [rest.Route] value a downstream consumer
// can genuinely call .ClientHandle() on directly — each is independently
// useful with its own *http.Client, retry policy, observer, or security
// wiring, entirely independent of client.go's convenience orchestration
// (auth flow, manifest-list resolution). See client.go for the
// batteries-included GetTags/GetImageMetadata built on top of these same
// routes.
//
// bearerAuthSecurity/bearerAuthScheme (referenced by GetTagsRoute/
// GetManifestRoute below via RouteMeta.Security/rest.WithSecurityScheme)
// are declared in auth.go, NOT here — auth.go is the single home for
// every security scheme/requirement value this package declares
// (bearerAuthSecurity/bearerAuthScheme AND basicAuthSecurity/
// basicAuthScheme), keeping this file's own job purely "declare the
// routes," independent of the auth flow's own internal wiring.
//
// getTokenRoute (the auth-realm token-exchange endpoint) deliberately does
// NOT live here even though it is also a [rest.Route] value — it lives in
// auth.go instead, alongside its exclusive caller (authenticate()) and its
// own basicAuthScheme/basicAuthSecurity declarations. Unlike the three
// routes above, getTokenRoute has no legitimate standalone caller: it
// needs a realm URL, service, and scope that only come from parsing a
// WWW-Authenticate challenge (authenticate's own job), so it is auth-flow
// plumbing, not part of this package's externally-facing contract.

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
// e.g. "prometheus/prometheus") — substituted as-is, no escaping
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
	rest.WithSecurityScheme("bearerAuth", bearerAuthScheme),
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
	rest.WithSecurityScheme("bearerAuth", bearerAuthScheme),
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
