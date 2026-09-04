package nethttp

import (
	"net/http"
)

// caller is a client-side convenience holder removing repeated
// client/baseURL boilerplate across many calls to the same API — NOT a
// spec-accumulating [rest.Server] equivalent (the server's Builder
// exists because OpenAPI needs ONE document; the client has no
// equivalent accumulation need). Unexported: the sole public client-side
// workflow is [Attach] + [rest.Client.Call] (see
// docs/roadmap/pubsub-workflow-simplification.md's Decision 6); caller
// is still used internally by [clientTransport] (built by [Attach]) and
// by [call]/[callWithHandle].
//
// Construct once per API (or per base-URL) via [newCaller], then pass it
// to [call] exactly like the underlying *http.Client, just without
// repeating client/baseURL at every call site. Client-side credential
// fulfillment is declared PER-ROUTE via [rest.Route.ClientMW] (paired
// against the SAME [middleware.Middleware] the route's security
// requirement was declared with) — there is no caller-level default
// slot anymore; a route needing a shared credential across many calls
// declares it ONCE via ClientMW, not per-caller.
type caller struct {
	client  *http.Client
	baseURL string
}

// newCaller builds a [caller] bound to client and baseURL.
func newCaller(client *http.Client, baseURL string) *caller {
	return &caller{client: client, baseURL: baseURL}
}

// WithBaseURL returns a NEW [caller] sharing c's *http.Client but bound to a
// different baseURL. This is a convenience over calling [newCaller] directly
// when you already hold a base caller and want to rebase it for one call
// without re-passing the underlying *http.Client — e.g. a registry client
// resolving Docker Hub vs. GHCR vs. MCR per image reference. Does NOT mutate
// c: c remains usable as a template/default, safe to share across goroutines
// and rebase concurrently — each call returns an independent *caller.
//
// baseURL is not validated here (mirrors [newCaller]'s own lack of
// validation); an empty or malformed baseURL surfaces naturally as
// [RequestBuildError] the next time [call] is invoked with the result.
func (c *caller) WithBaseURL(baseURL string) *caller {
	return &caller{client: c.client, baseURL: baseURL}
}
