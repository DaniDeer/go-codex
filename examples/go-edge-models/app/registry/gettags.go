package registry

import (
	"context"
	"net/http"

	mcpgo "github.com/DaniDeer/go-codex/adapters/mcpgo"
	nethttp "github.com/DaniDeer/go-codex/adapters/nethttp"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker"
	regmodels "github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker/registry"
)

// This file holds the CONCRETE IMPLEMENTATION of the GetTags operation:
// the batteries-included client function (GetTags), its registryBaseURL
// helper, and NewGetTagsToolHandler (which BINDS
// regmodels.GetTagsTool — the declared MCP tool contract — to this
// implementation). The declarative contract itself (GetTagsRoute,
// GetTagsReq, TagsList/TagsListCodec, GetTagsToolReq, GetTagsTool) lives
// in the sibling models/docker/registry package instead — see this
// package's doc.go for the full models/ vs app/ rationale.

// ── registryBaseURL ───────────────────────────────────────────────────────────

// registryBaseURL returns the HTTPS base URL to dial for a registry host
// — a small named helper (not a codec: deriving a dial address from an
// already-validated ImageRef.Registry value has no wire decode direction
// to model) shared by GetTags (below), getimagemetadata.go's
// GetImageMetadata, and auth.go's authenticate, replacing three separate
// inline "https://"+host concatenations.
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
func GetTags(ctx context.Context, httpClient *http.Client, imageURL string, opts ...Option) (regmodels.TagsList, error) {
	ref, err := regmodels.ParseImageRef(imageURL)
	if err != nil {
		return regmodels.TagsList{}, err
	}

	o := resolveOptions(opts)
	// Declare (GetTagsRoute, including its regmodels.BearerAuthDeclaration
	// server-side Security declaration) → chain (UseClient) → build
	// (ClientHandle) — authMw supplies the credential; see
	// newAuthMiddleware's own doc comment (auth.go). CallHandle picks up
	// authMw automatically from handle.ClientMiddlewares — no need to
	// ALSO pass it here.
	authMw := newAuthMiddleware(httpClient, ref.Registry, ref.Repository, opts...)
	handle := regmodels.GetTagsRoute.UseClient(authMw).ClientHandle()
	baseURL := registryBaseURL(ref.Registry)
	callOpts := nethttp.CallOptions{Observer: o.observer}
	return nethttp.CallHandle(ctx, httpClient, baseURL, handle, regmodels.GetTagsReq{Name: ref.Repository}, callOpts)
}

// GetTagsFiltered calls GetTags, then sorts/limits the result's Tags via
// docker.FilterTags(list.Tags, filterOpts...) — a thin, client-side
// convenience: no new HTTP call, no new wire type. filterOpts default to
// docker.FilterTags's own defaults (docker.SortByVersionDesc, no limit)
// when empty. See docker.SortByVersionDesc's doc comment for the
// important "version-order, not chronological order" caveat that applies
// here too (the registry's tags/list response carries no timestamps).
func GetTagsFiltered(ctx context.Context, httpClient *http.Client, imageURL string, filterOpts []docker.FilterTagsOpt, opts ...Option) (regmodels.TagsList, error) {
	list, err := GetTags(ctx, httpClient, imageURL, opts...)
	if err != nil {
		return regmodels.TagsList{}, err
	}
	list.Tags = docker.FilterTags(list.Tags, filterOpts...)
	return list, nil
}

// ── GetTags as an MCP tool ──────────────────────────────────────────────────────
//
// NewGetTagsToolHandler wraps the batteries-included GetTags FUNCTION
// above (registry-agnostic — it resolves the target registry per call
// from ImageURL), NOT the raw GetTagsRoute — there's no single fixed REST
// endpoint to bridge (mcprest.ToolHandler needs one fixed baseURL), so
// this is a plain closure, not an mcprest bridge. Contrast with
// examples/go-edge-models/main.go's OWN separate demo, which deliberately
// wraps regmodels.GetTagsRoute directly via mcprest.ToolHandler to
// illustrate that lower-level, single-registry route-bridging pattern.

// NewGetTagsToolHandler returns an mcpgo.HandlerFunc that calls GetTags
// against httpClient/opts for every tool invocation — registry-agnostic,
// exactly like GetTags itself (works against whichever registry
// req.ImageURL resolves to, per call).
//
// Usage:
//
//	tool, _ := regmodels.GetTagsTool.Register(mcpBuilder)
//	_, handlerFn := mcpgo.ToolHandler(tool,
//	    registryapp.NewGetTagsToolHandler(httpClient, registryapp.WithObserver(obs)),
//	    mcpgo.Options{})
func NewGetTagsToolHandler(httpClient *http.Client, opts ...Option) mcpgo.HandlerFunc[regmodels.GetTagsToolReq, regmodels.TagsList] {
	return func(ctx context.Context, req regmodels.GetTagsToolReq) (regmodels.TagsList, error) {
		return GetTagsFiltered(ctx, httpClient, req.ImageURL, filterOptsFor(req), opts...)
	}
}

// filterOptsFor maps a GetTagsToolReq's wire-friendly Limit/Sort fields to
// docker.FilterTagsOpt values. An absent or unrecognized Sort falls back
// to docker.FilterTags's own default (docker.SortByVersionDesc) rather
// than erroring — an LLM omitting the field is the expected common case,
// and GetTagsToolReqCodec's own sortModeConstraint already guarantees Sort
// is either "" or one of regmodels' allowed values by the time a handler
// ever sees it.
func filterOptsFor(req regmodels.GetTagsToolReq) []docker.FilterTagsOpt {
	opts := []docker.FilterTagsOpt{docker.WithLimit(req.Limit)}
	switch req.Sort {
	case "alphabetical":
		opts = append(opts, docker.WithSort(docker.SortAlphabetical))
	case "none":
		opts = append(opts, docker.WithSort(docker.SortNone))
	default:
		opts = append(opts, docker.WithSort(docker.SortByVersionDesc))
	}
	return opts
}
