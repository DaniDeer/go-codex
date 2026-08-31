package nethttp

import (
	"context"
	"net/http"
)

// Transform builds a general-purpose implementation Fn from fn — pins Raw
// to *http.Request; the ACTUAL logic lives in the returned closure, which
// wraps fn into the SAME shape a security-verifying Fn uses
// (func(context.Context, *http.Request, *Req) (map[string][]string, error)),
// returning nil grants always (fn contributes no scope grants — it exists
// to derive/enrich req from raw request data, e.g. a correlation ID header,
// not to authenticate).
//
// Returns `any` (the bare wrapped closure), NOT a
// middleware.ServerImplementation — [rest.Route.HandleMW] builds that
// internally. Transform never contributes to a route's spec, so pass nil
// as HandleMW's mw:
//
//	route = route.HandleMW(nil, nethttp.Transform(func(ctx context.Context, r *http.Request, req *Req) error {
//	    req.CorrelationID = r.Header.Get("X-Correlation-ID")
//	    return nil
//	}))
func Transform[Req any](fn func(ctx context.Context, r *http.Request, req *Req) error) any {
	return func(ctx context.Context, r *http.Request, req *Req) (map[string][]string, error) {
		return nil, fn(ctx, r, req)
	}
}
