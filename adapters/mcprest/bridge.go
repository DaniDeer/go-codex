package mcprest

import (
	"context"
	"net/http"

	mcpgo "github.com/DaniDeer/go-codex/adapters/mcpgo"
	nethttp "github.com/DaniDeer/go-codex/adapters/nethttp"
	"github.com/DaniDeer/go-codex/api/rest"
)

// MappedToolHandler returns an [mcpgo.HandlerFunc][ToolIn, ToolOut] that
// proxies each MCP tool call to an outbound REST request via
// [nethttp.CallWithHandle], mapping between the tool's own In/Out shape and
// the REST route's Req/Resp wire shape via the supplied toReq/fromResp
// functions. Both mapper functions are fallible — return a non-nil error
// to abort the call before/after the underlying HTTP request; errors are
// wrapped as [ToolRequestMapError]/[ToolResponseMapError] respectively
// (kept distinct from the underlying REST call's own typed errors, which
// continue to forward unchanged via errors.As).
//
// handle's declared path/query/header/cookie merge fields, security
// schemes, and any [rest.Route.ClientMW]-attached credential
// implementations apply exactly as any other nethttp client call would.
// opts is FIXED for every call made through the returned handler — see
// the package doc comment for the ctx/session recipe if a per-caller
// credential is ever needed (declare it via ClientMW on the route BEFORE
// calling [rest.Route.ClientHandle] to build handle).
//
// Use [ToolHandler] instead when the tool's In/Out IS the route's Req/Resp
// (the common case) — it is MappedToolHandler with identity mappers.
//
// The returned function's shape (func(context.Context, ToolIn) (ToolOut,
// error)) is identical to [ports.ToolPort.SetFunc]'s parameter — pass it
// there directly to expose the SAME REST-backed logic as a REST endpoint
// or reqreply endpoint too, from the same port declaration. No new `ports`
// plumbing is needed for this to compose.
func MappedToolHandler[ToolIn, ToolOut, Req, Resp any](
	client *http.Client,
	baseURL string,
	handle *rest.RouteHandle[Req, Resp],
	opts nethttp.CallOptions,
	toReq func(ToolIn) (Req, error),
	fromResp func(Resp) (ToolOut, error),
) mcpgo.HandlerFunc[ToolIn, ToolOut] {
	return func(ctx context.Context, in ToolIn) (ToolOut, error) {
		var zero ToolOut

		req, err := toReq(in)
		if err != nil {
			return zero, ToolRequestMapError{
				Method: handle.Descriptor.Method,
				Path:   handle.Descriptor.Path,
				Err:    err,
			}
		}

		resp, err := nethttp.CallWithHandle(ctx, client, baseURL, handle, req, opts)
		if err != nil {
			return zero, err
		}

		out, err := fromResp(resp)
		if err != nil {
			return zero, ToolResponseMapError{
				Method: handle.Descriptor.Method,
				Path:   handle.Descriptor.Path,
				Err:    err,
			}
		}
		return out, nil
	}
}

// ToolHandler is the zero-boilerplate convenience for the common case
// where the MCP tool's In/Out IS the REST route's Req/Resp — no mapping
// needed. Implemented as [MappedToolHandler] with identity mapper
// functions.
//
// Pair with an apimcp.Tool[Req, Resp] built from the SAME Req/Resp codecs
// already used for the REST route (rest.NewRoute and apimcp.NewTool both
// just take a codex.Codec[Req]/[Resp] — reuse the identical package-level
// codec values, no re-derivation):
//
//	restHandle := registry.GetTagsRoute.ClientMW(&credMw, myFixedCredentialFunc).ClientHandle()
//	toolHandle, _ := apimcp.NewTool[registry.GetTagsReq, registry.TagsList](
//	    "get_tags", reqCodec, respCodec,
//	    mcprest.DefaultErrorPatterns()...,
//	).Register(mcpBuilder)
//	tool, handlerFn := mcpgoAdapter.ToolHandler(toolHandle,
//	    mcprest.ToolHandler(httpClient, baseURL, restHandle, nethttp.CallOptions{}),
//	    mcpgo.Options{},
//	)
func ToolHandler[Req, Resp any](
	client *http.Client,
	baseURL string,
	handle *rest.RouteHandle[Req, Resp],
	opts nethttp.CallOptions,
) mcpgo.HandlerFunc[Req, Resp] {
	return MappedToolHandler(client, baseURL, handle, opts,
		func(req Req) (Req, error) { return req, nil },
		func(resp Resp) (Resp, error) { return resp, nil },
	)
}
