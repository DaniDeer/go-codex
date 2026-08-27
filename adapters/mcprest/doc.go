// Package mcprest bridges [github.com/DaniDeer/go-codex/api/rest] client
// calls (via [github.com/DaniDeer/go-codex/adapters/nethttp]) to MCP tool
// handlers (via [github.com/DaniDeer/go-codex/adapters/mcpgo]) — any
// already-declared [rest.Route] can become an MCP tool with a single
// function, because [nethttp.CallHandle]'s shape already almost matches
// [mcpgo.HandlerFunc]'s shape.
//
// This package deliberately imports BOTH adapters/nethttp and
// adapters/mcpgo — neither of those two adapters imports the other or
// this package, keeping both transport-pure and independently importable.
//
// # Two constructors
//
// [ToolHandler] is the zero-boilerplate convenience for the common case
// where the MCP tool's input/output IS the REST route's request/response
// shape:
//
//	restHandle := registry.GetTagsRoute.ClientHandle()
//	toolHandle, _ := apimcp.NewTool[registry.GetTagsReq, registry.TagsList](
//	    "get_tags", reqCodec, respCodec,
//	    mcprest.DefaultErrorPatterns()...,
//	).Register(mcpBuilder)
//	tool, handlerFn := mcpgoAdapter.ToolHandler(toolHandle,
//	    mcprest.ToolHandler(httpClient, baseURL, restHandle, nethttp.CallOptions{},
//	        middleware.Middleware{Fn: myFixedCredentialFunc}),
//	    mcpgo.Options{},
//	)
//
// [MappedToolHandler] is the general form: an LLM-facing tool's ideal
// input/output shape often differs from the wire request/response shape
// (fewer fields, flattened structure, renamed for LLM readability). Both
// shapes are already codec-defined — the tool's via [apimcp.NewTool]'s
// codecs, the route's via [rest.NewRoute]'s codecs — so supply toReq/
// fromResp mapper functions to bridge between them:
//
//	handlerFn := mcprest.MappedToolHandler(httpClient, baseURL, restHandle,
//	    nethttp.CallOptions{},
//	    func(in SimpleSearchInput) (registry.GetTagsReq, error) {
//	        return registry.GetTagsReq{Name: in.Image}, nil
//	    },
//	    func(resp registry.TagsList) (SimpleSearchOutput, error) {
//	        return SimpleSearchOutput{Tags: resp.Tags}, nil
//	    },
//	    middleware.Middleware{Fn: myFixedCredentialFunc},
//	)
//
// A failing mapper returns [ToolRequestMapError]/[ToolResponseMapError] —
// kept distinct from the underlying REST call's own typed errors
// ([nethttp.UnexpectedStatusError], [rest.SecurityCredentialError], etc.),
// which continue to forward unchanged.
//
// # Credentials are FIXED per tool
//
// opts and mws (the credential-providing [middleware.Middleware], if any)
// are configured ONCE, when the tool's handler is built, and reused for
// every call made through it — matching every other client-adapter
// binding in go-codex ([nethttp.CallAdapter], [nethttp.DrainCallAdapter],
// the mqtt5/zeromq equivalents). There is no per-call credential
// override.
//
// If a per-CALLER credential is ever needed (e.g. different MCP clients/
// sessions should authenticate to the downstream REST API differently),
// it is already achievable with ZERO new API: a middleware.Middleware's Fn
// receives ctx on every invocation and MCP tool calls carry a
// per-connection session identity accessible via
// [github.com/mark3labs/mcp-go/server.ClientSessionFromContext](ctx).SessionID() —
// look up a credential in an application-owned store keyed by that
// session ID, inside the Fn closure passed as mws:
//
//	credMw := middleware.Middleware{
//	    Fn: func(ctx context.Context, reqs []route.SecurityRequirement) (http.Header, error) {
//	        sessionID := server.ClientSessionFromContext(ctx).SessionID()
//	        cred, ok := myCredentialStore.Lookup(sessionID)
//	        if !ok {
//	            return nil, fmt.Errorf("no credential registered for session %s", sessionID)
//	        }
//	        h := make(http.Header)
//	        h.Set("Authorization", "Bearer "+cred.Token)
//	        return h, nil
//	    },
//	}
//
// This mirrors the same ctx-introspection idiom [stats.ObserverFromContext]
// already establishes elsewhere in go-codex — no new mcprest API is needed
// for it.
//
// # Composing with ports.ToolPort
//
// Both [ToolHandler] and [MappedToolHandler] return exactly the
// func(context.Context, In) (Out, error) shape [ports.ToolPort.SetFunc]
// already accepts:
//
//	domainPort := ports.NewToolPort[registry.GetTagsReq, registry.TagsList](
//	    "get_tags", reqCodec, respCodec,
//	)
//	domainPort.SetFunc(mcprest.ToolHandler(client, baseURL, restHandle, callOpts))
//
// Because the wrapped function is bound to a [ports.ToolPort] — not
// directly to [mcpgo.ToolHandler] — the SAME REST-backed logic can ALSO be
// exposed as a REST endpoint or a reqreply endpoint from the SAME port
// declaration, simultaneously, with no duplicated business logic.
package mcprest
