package nethttp

import (
	"net/http"
)

// Caller is a client-side convenience holder removing repeated
// client/baseURL boilerplate across many calls to the same API — NOT a
// spec-accumulating [rest.Builder] equivalent (the server's Builder
// exists because OpenAPI needs ONE document; the client has no
// equivalent accumulation need).
//
// Construct once per API (or per base-URL) via [NewCaller], then pass it
// to [Call] exactly like the underlying *http.Client, just without
// repeating client/baseURL at every call site. Client-side credential
// fulfillment is declared PER-ROUTE via [rest.Route.ClientMW] (paired
// against the SAME [middleware.Middleware] the route's security
// requirement was declared with) — there is no Caller-level default
// slot anymore; a route needing a shared credential across many calls
// declares it ONCE via ClientMW, not per-Caller.
//
// For a client whose target server varies per call (the SAME declared
// route, called against a DIFFERENT host each time — e.g. a registry
// client resolving Docker Hub vs. GHCR vs. MCR per image reference), a
// fresh [Caller] can always be built directly via [NewCaller] — Caller
// is a trivial, cheap-to-copy value (just client+baseURL), so no
// dedicated "rebase" mechanism is structurally required (this is exactly
// how examples/go-edge-models's docker/registry client does it: a fresh
// NewCaller per call, since each call already resolves its own baseURL
// before constructing one). [Caller.WithBaseURL] exists purely as
// ergonomic sugar for callers who already hold a base Caller value and
// want to rebase off it without re-passing the *http.Client separately.
type Caller struct {
	client  *http.Client
	baseURL string
}

// NewCaller builds a [Caller] bound to client and baseURL.
func NewCaller(client *http.Client, baseURL string) *Caller {
	return &Caller{client: client, baseURL: baseURL}
}

// WithBaseURL returns a NEW [Caller] sharing c's *http.Client but bound to a
// different baseURL. This is a convenience over calling [NewCaller] directly
// when you already hold a base Caller and want to rebase it for one call
// without re-passing the underlying *http.Client — e.g. a registry client
// resolving Docker Hub vs. GHCR vs. MCR per image reference. Does NOT mutate
// c: c remains usable as a template/default, safe to share across goroutines
// and rebase concurrently — each call returns an independent *Caller.
//
// baseURL is not validated here (mirrors [NewCaller]'s own lack of
// validation); an empty or malformed baseURL surfaces naturally as
// [RequestBuildError] the next time [Call] is invoked with the result.
//
//	base := nethttp.NewCaller(httpClient, "")
//	caller := base.WithBaseURL(registryBaseURL(ref.Registry)) // per-call rebase
//	resp, err := nethttp.Call(ctx, caller, route, req, nethttp.CallOptions{})
func (c *Caller) WithBaseURL(baseURL string) *Caller {
	return &Caller{client: c.client, baseURL: baseURL}
}
