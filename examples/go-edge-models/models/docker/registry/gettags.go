package registry

import (
	mcprest "github.com/DaniDeer/go-codex/adapters/mcprest"
	mcp "github.com/DaniDeer/go-codex/api/mcp"
	"github.com/DaniDeer/go-codex/api/rest"
	c "github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker"
	v "github.com/DaniDeer/go-codex/validate"
)

// This file holds the DECLARATIVE contract for the GetTags operation:
// the route (GetTagsRoute), its request/response types+codecs (GetTagsReq,
// TagsList/TagsListCodec), and its MCP tool declaration (GetTagsToolReq/
// GetTagsTool). Pure data — no I/O, no *http.Client, nothing that
// actually calls a registry. The concrete implementation (the
// batteries-included GetTags client function and NewGetTagsToolHandler,
// which BINDS GetTagsTool to that implementation) lives in the sibling
// app/registry package, built on top of this file's declarations — see
// this package's doc.go for the full models/ vs app/ rationale.

// ── GetTagsReq / GetTagsRoute ──────────────────────────────────────────────────

// GetTagsReq is GetTagsRoute's request — Name merges automatically into
// the {name} path variable via nethttp.Call/CallWithHandle.
type GetTagsReq struct {
	Name string
}

// GetTagsRoute is GET /v2/{name}/tags/list — lists every tag for a
// repository. {name} is the full repository path (may itself contain "/",
// e.g. "prometheus/prometheus") — substituted as-is, no escaping
// needed (see BuildPath's plain string-replace semantics). Req is
// GetTagsReq, whose Name field merges into {name} automatically via
// nethttp.Call/CallWithHandle — no manual vars map needed.
//
// GetTagsRoute declares its "bearerAuth" requirement via .Use(BearerAuthDeclaration)
// below — a spec-only declaration (see BearerAuthSchemeName's doc comment):
// this codebase documents the requirement but never enforces it, since the
// real server is an external registry. A caller MUST separately chain a
// credential-SUPPLYING middleware.ClientImplementation via .ClientMW(...)
// before calling .ClientHandle() to actually authenticate outgoing calls;
// see app/registry's GetTags for the batteries-included client (auth flow
// via app/registry's own newAuthCredentialFunc, image-URL parsing) built
// this way on top of this route.
var GetTagsRoute = rest.NewRoute[GetTagsReq, TagsList](
	"GET", "/v2/{name}/tags/list",
	c.Struct[GetTagsReq](), TagsListCodec,
	rest.RouteMeta{
		OperationID:    "getTags",
		Summary:        "List every tag for a repository",
		RespSchemaName: "TagsList",
	},
	rest.NewPathParam("name",
		c.String(),
		func(r GetTagsReq) string { return r.Name },
		func(r *GetTagsReq, v string) { r.Name = v },
	).WithDescription("Repository path"),
).Use(BearerAuthDeclaration)

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

// ── GetTags as an MCP tool ──────────────────────────────────────────────────────
//
// GetTagsToolReq/GetTagsTool declare the MCP contract for GetTags —
// unlike GetTagsReq (the ROUTE's request, which only carries an
// already-resolved repository path, no registry host), GetTagsToolReq
// carries the full raw image URL: an MCP tool call has no separate
// "already resolved which registry" step, so the full URL must travel
// through the tool's own input codec. app/registry's NewGetTagsToolHandler
// binds this declared tool to the concrete, registry-agnostic GetTags
// client function (resolves the target registry per call from
// ImageURL) — NOT GetTagsRoute directly, since there's no single fixed
// REST endpoint/baseURL to bridge via adapters/mcprest.

// GetTagsToolReq is GetTagsTool's input.
type GetTagsToolReq struct {
	ImageURL string
	// Limit caps the number of tags returned, keeping the first Limit
	// after sorting (see Sort). 0 or absent means "no limit" (every tag
	// returned) — mirrors docker.WithLimit's own "n <= 0 means no limit"
	// semantics.
	Limit int
	// Sort selects the tag ordering — one of "version_desc" (default),
	// "alphabetical", or "none". An absent or unrecognized value falls
	// back to "version_desc" (app/registry's NewGetTagsToolHandler
	// tolerates this rather than erroring, since an LLM omitting the
	// field is the expected common case).
	Sort string
}

// sortModeAllowedValues lists GetTagsToolReq.Sort's allowed wire values —
// the string names for docker.SortMode's constants, since MCP tool
// arguments are plain JSON-ish values, not Go enums. Exported as a plain
// slice (not a type) so app/registry's NewGetTagsToolHandler can reuse
// the same three literal strings when mapping Sort back to
// docker.SortMode, without either package hand-duplicating them.
var sortModeAllowedValues = []string{"version_desc", "alphabetical", "none"}

// sortModeConstraint accepts "" (the zero value for an absent Sort field)
// in addition to sortModeAllowedValues — an OptionalField's codec is still
// called unconditionally on Encode even when the field was never set (see
// docker.Image's tagConstraint for the same "empty string must stay
// valid for Encode-time re-validation" reasoning), so plain
// validate.OneOf(sortModeAllowedValues...) alone would incorrectly reject
// every GetTagsToolReq whose Sort was left at its zero value.
var sortModeConstraint = c.Constraint[string]{
	Name: "get-tags-sort-mode",
	Check: func(s string) bool {
		if s == "" {
			return true
		}
		return v.OneOf(sortModeAllowedValues...).Check(s)
	},
	Message: func(s string) string {
		return v.OneOf(sortModeAllowedValues...).Message(s)
	},
	Schema: v.OneOf(sortModeAllowedValues...).Schema,
}

// GetTagsToolReqCodec validates a GetTagsToolReq value — ImageURL is
// required (app/registry's GetTags further validates the full
// image-reference shape via ParseImageRef at call time); Limit and Sort
// are both optional.
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
	c.OptionalField("limit",
		c.Int().WithDescription(
			"Maximum number of tags to return, keeping the highest-ranked "+
				"ones per sort (see sort). Omit or pass 0 for every tag.",
		),
		func(r GetTagsToolReq) int { return r.Limit },
		func(r *GetTagsToolReq, val int) { r.Limit = val },
	),
	c.OptionalField("sort",
		c.String().Refine(sortModeConstraint).WithDescription(
			`How to order the returned tags: "version_desc" (default — `+
				"highest version first, inferred from each tag's own text; "+
				"NOT real chronological recency, since registries do not "+
				`expose tag timestamps), "alphabetical" (plain string sort, `+
				`ignoring version structure), or "none" (registry's own `+
				"order). Omit for the default.",
		),
		func(r GetTagsToolReq) string { return r.Sort },
		func(r *GetTagsToolReq, val string) { r.Sort = val },
	),
)

// GetTagsTool is the declared, UNREGISTERED MCP tool contract for GetTags
// — the SAME "declare once, register anywhere" pattern GetTagsRoute
// itself follows: a caller registers it against their own mcp.Builder
// (GetTagsTool.Register(builder)) and pairs the resulting handle with
// app/registry's NewGetTagsToolHandler via mcpgo.ToolHandler.
// ToolMeta.Description tells an LLM client WHEN to call this tool;
// GetTagsToolReqCodec's own per-field .WithDescription (above) tells it
// WHAT to pass — both flow into the JSON Schema an MCP client shows the
// LLM, so a caller only needs to supply imageURL, with no extra
// guesswork about the shape.
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
