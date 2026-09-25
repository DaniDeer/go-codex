// Package httpsecurity is the shared, adapters-internal core for
// dispatching a route's [middleware.ServerImplementation] security
// implementations against an incoming *http.Request — the ONE piece of
// F4 ("Thin Adapters Audit") that genuinely cannot drop *http.Request,
// since it dispatches to a USER-PROVIDED Fn whose documented contract
// (middleware/middleware.go) is explicitly
// func(ctx, raw *http.Request, req *Req) (map[string][]string, error).
//
// Scoped under adapters/internal/ (not the repo-root internal/, unlike
// internal/templatematch) since this package will NEVER be needed
// outside the net/http-family adapter tree (adapters/nethttp,
// adapters/chi, and any future adapter built on net/http) — see
// docs/roadmap/thin-adapters-audit.md's "Forward-looking guardrail"
// section and its F4b "Detailed design" subsection for the full
// rationale.
package httpsecurity

import (
	"context"
	"net/http"
	"reflect"

	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/route"
)

// RunSecurityMiddlewareReflect is [runSecurityMiddleware]'s reflect-based
// equivalent — reqPtr is an addressable *Req reflect.Value (Req erased).
func RunSecurityMiddlewareReflect(ctx context.Context, r *http.Request, reqPtr reflect.Value, impls []middleware.ServerImplementation, secReqs []route.SecurityRequirement) error {
	granted := make(map[string][]string)
	for _, impl := range impls {
		fnVal := reflect.ValueOf(impl.Fn)
		if !fnVal.IsValid() || fnVal.Kind() != reflect.Func || fnVal.Type().NumIn() != 3 {
			continue // not the security shape (general-purpose or nil)
		}
		if len(impl.Satisfies) > 0 && len(secReqs) == 0 {
			continue
		}
		results := fnVal.Call([]reflect.Value{reflect.ValueOf(ctx), reflect.ValueOf(r), reqPtr})
		if err, _ := results[1].Interface().(error); err != nil {
			return err
		}
		g, _ := results[0].Interface().(map[string][]string)
		for k, v := range g {
			granted[k] = v
		}
	}
	return middleware.CheckScopes(secReqs, granted)
}
