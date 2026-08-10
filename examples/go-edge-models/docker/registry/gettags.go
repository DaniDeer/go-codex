package registry

import (
	"context"
	"net/http"

	mcpgo "github.com/DaniDeer/go-codex/adapters/mcpgo"
	mcprest "github.com/DaniDeer/go-codex/adapters/mcprest"
	nethttp "github.com/DaniDeer/go-codex/adapters/nethttp"
	mcp "github.com/DaniDeer/go-codex/api/mcp"
	"github.com/DaniDeer/go-codex/api/rest"
	c "github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/docker"
	v "github.com/DaniDeer/go-codex/validate"
)

// This file holds everything specific to the GetTags operation: the
// route (GetTagsRoute), its request/response types+codecs (GetTagsReq,
// TagsList/TagsListCodec), the batteries-included client function
// (GetTags), and its ready-made MCP tool (GetTagsTool/
// NewGetTagsToolHandler) — declare, implement, and expose one operation
// together, so nothing about "GetTags" is scattered across the package.

// ── GetTagsReq / GetTagsRoute ──────────────────────────────────────────────────

// GetTagsReq is GetTagsRoute's request — Name merges automatically into
// the {name} path variable via nethttp.CallHandle.
type GetTagsReq struct {
	Name string
}

// GetTagsRoute is GET /v2/{name}/tags/list — lists every tag for a
// repository. {name} is the full repository path (may itself contain "/",
// e.g. "prometheus/prometheus") — substituted as-is, no escaping
// needed (see BuildPath's plain string-replace semantics). Req is
// GetTagsReq, whose Name field merges into {name} automatically via
// nethttp.CallHandle — no manual vars map needed.
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

// ── TagsList ──────────────────────────────────────────────────────────────────

// TagsList is the decoded response body of GET /v2/<name>/tags/list.
type TagsList struct {
	Name string
	// Tags reuses docker.Tag directly — a registry's tags/list response
	// is a list of the SAME concept docker.Image.Tag validates, and the
	// registry itself only ever lists already-valid tags (see
	// docker.Tag's own doc comment).
	Tags []docker.Tag
}

// tagsFieldCodec wraps a []string wire value into []docker.Tag — a
// registry's tags/list response only ever lists already-valid tags (see
// TagsList.Tags's own doc comment), so this is a plain infallible cast in
// both directions, not a validated MapCodecValidated.
var tagsFieldCodec = c.MapCodecSafe(
	c.SliceOf(c.String()),
	func(ss []string) []docker.Tag {
		out := make([]docker.Tag, len(ss))
		for i, s := range ss {
			out[i] = docker.Tag(s)
		}
		return out
	},
	func(ts []docker.Tag) ([]string, error) {
		out := make([]string, len(ts))
		for i, t := range ts {
			out[i] = string(t)
		}
		return out, nil
	},
)

// TagsListCodec is the canonical codec for the GET /v2/<name>/tags/list
// response body.
var TagsListCodec = c.Struct[TagsList](
	c.RequiredField("name", c.String(),
		func(t TagsList) string { return t.Name },
		func(t *TagsList, v string) { t.Name = v },
	),
	// OptionalField: some registries omit "tags" entirely for a repository
	// with zero tags, rather than returning an empty array.
	c.OptionalField("tags", tagsFieldCodec,
		func(t TagsList) []docker.Tag { return t.Tags },
		func(t *TagsList, v []docker.Tag) { t.Tags = v },
	),
)

// ── registryBaseURL ───────────────────────────────────────────────────────────

// registryBaseURL returns the HTTPS base URL to dial for a registry host
// — a small named helper (not a codec: deriving a dial address from an
// already-validated ImageRef.Registry value has no wire decode direction
// to model) shared by GetTags (below) and getimagemetadata.go's
// GetImageMetadata, replacing three separate inline "https://"+host
// concatenations.
func registryBaseURL(host string) string {
	return "https://" + host
}

// ── GetTags ───────────────────────────────────────────────────────────────────

// GetTags lists every tag for imageURL's repository. imageURL's own
// Reference segment (if any) is ignored — only Registry and Repository
// are used. Pass WithCredentials(...) for a private repository that
// requires Basic auth at the token-exchange step; anonymous pulls need
// no options at all. Pass WithObserver(...) to receive
// stats.Observer.RecordRequest metrics for every HTTP call this makes
// (including the auth-realm Ping/token exchange, when required).
func GetTags(ctx context.Context, httpClient *http.Client, imageURL string, opts ...Option) (TagsList, error) {
	ref, err := ParseImageRef(imageURL)
	if err != nil {
		return TagsList{}, err
	}

	o := resolveOptions(opts)
	handle := GetTagsRoute.ClientHandle()
	baseURL := registryBaseURL(ref.Registry)
	credFn := newAuthCredentialFunc(httpClient, ref.Registry, ref.Repository, opts...)
	callOpts := nethttp.CallOptions{CredentialFunc: credFn, Observer: o.observer}
	return nethttp.CallHandle(ctx, httpClient, baseURL, handle, GetTagsReq{Name: ref.Repository}, callOpts)
}

// ── GetTags as an MCP tool ──────────────────────────────────────────────────────
//
// GetTagsTool/NewGetTagsToolHandler wrap the batteries-included GetTags
// FUNCTION above (registry-agnostic — it resolves the target registry
// per call from ImageURL), NOT the raw GetTagsRoute — there's no single
// fixed REST endpoint to bridge (mcprest.ToolHandler needs one fixed
// baseURL), so this is a plain closure, not an mcprest bridge. Contrast
// with examples/go-edge-models/main.go's OWN separate demo, which
// deliberately wraps GetTagsRoute directly via mcprest.ToolHandler to
// illustrate that lower-level, single-registry route-bridging pattern.

// GetTagsToolReq is GetTagsTool's input — unlike GetTagsReq (the ROUTE's
// request, which only carries an already-resolved repository path, no
// registry host), GetTagsToolReq carries the full raw image URL, exactly
// like GetTags's own imageURL parameter — an MCP tool call has no
// separate "already resolved which registry" step, so the full URL must
// travel through the tool's own input codec.
type GetTagsToolReq struct {
	ImageURL string
}

// GetTagsToolReqCodec validates a GetTagsToolReq value — ImageURL is
// required (GetTags itself further validates the full image-reference
// shape via ParseImageRef at call time).
var GetTagsToolReqCodec = c.Struct[GetTagsToolReq](
	c.RequiredField("imageURL",
		c.String().Refine(v.NonEmptyString).WithDescription(
			"The full container image reference to list tags for, e.g. "+
				`"alpine" or "ghcr.io/org/repo:1.2.3" — any registry host `+
				"embedded in the reference is resolved automatically; an "+
				"absent registry host defaults to Docker Hub.",
		),
		func(r GetTagsToolReq) string { return r.ImageURL },
		func(r *GetTagsToolReq, val string) { r.ImageURL = val },
	),
)

// GetTagsTool is the declared, UNREGISTERED MCP tool contract for GetTags
// — the SAME "declare once, register anywhere" pattern GetTagsRoute
// itself follows: a caller registers it against their own mcp.Builder
// (GetTagsTool.Register(builder)) and pairs the resulting handle with
// NewGetTagsToolHandler via mcpgo.ToolHandler. ToolMeta.Description tells
// an LLM client WHEN to call this tool; GetTagsToolReqCodec's own
// per-field .WithDescription (above) tells it WHAT to pass — both flow
// into the JSON Schema an MCP client shows the LLM, so a caller only
// needs to supply imageURL, with no extra guesswork about the shape.
var GetTagsTool = mcp.NewTool[GetTagsToolReq, TagsList](
	"get_tags", GetTagsToolReqCodec, TagsListCodec,
	append([]mcp.ToolOpt{
		mcp.ToolMeta{
			Description: "List every tag for a container image's repository, " +
				"from any OCI-compliant registry (Docker Hub, GHCR, MCR, or " +
				"any other registry implementing the OCI Distribution Spec).",
		},
	}, mcprest.DefaultErrorPatterns()...)...,
)

// NewGetTagsToolHandler returns an mcpgo.HandlerFunc that calls GetTags
// against httpClient/opts for every tool invocation — registry-agnostic,
// exactly like GetTags itself (works against whichever registry
// req.ImageURL resolves to, per call).
//
// Usage:
//
//	tool, _ := registry.GetTagsTool.Register(mcpBuilder)
//	_, handlerFn := mcpgo.ToolHandler(tool,
//	    registry.NewGetTagsToolHandler(httpClient, registry.WithObserver(obs)),
//	    mcpgo.Options{})
func NewGetTagsToolHandler(httpClient *http.Client, opts ...Option) mcpgo.HandlerFunc[GetTagsToolReq, TagsList] {
	return func(ctx context.Context, req GetTagsToolReq) (TagsList, error) {
		return GetTags(ctx, httpClient, req.ImageURL, opts...)
	}
}
