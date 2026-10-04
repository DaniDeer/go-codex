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
// docs/concepts/ports-and-adapters.md's "Guardrail: adapters as pure
// protocol shims" section for the general principle this package
// follows.
package httpsecurity

import (
	"context"
	"net/http"
	"reflect"

	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/route"
)

// RunSecurityMiddlewareReflect is adapters/nethttp's generic
// runSecurityMiddleware's reflect-based equivalent — reqPtr is an
// addressable *Req reflect.Value (Req erased). Kept for callers with NO
// [middleware.ContextField]-free MiddlewareHandler-based Security to
// merge (e.g. adapters/nethttp's own handlerFunc path, which has no
// MiddlewareHandlers available at all) — a thin wrapper over
// [CollectGrantsReflect] + [middleware.CheckScopes], behavior UNCHANGED.
// A caller that ALSO has MiddlewareHandler-dispatched Security to merge
// (docs/design/d-0007-declarative-middleware-layering.md's Rollout Phase A)
// uses [CollectGrantsReflect]/[MergeMiddlewareHandlerGrants] directly
// instead, calling [middleware.CheckScopes] itself ONCE after BOTH
// sources are known.
func RunSecurityMiddlewareReflect(ctx context.Context, r *http.Request, reqPtr reflect.Value, impls []middleware.ServerImplementation, secReqs []route.SecurityRequirement) error {
	granted, err := CollectGrantsReflect(ctx, r, reqPtr, impls, secReqs)
	if err != nil {
		return err
	}
	return middleware.CheckScopes(secReqs, granted)
}

// CollectGrantsReflect runs every attached security-shaped
// [middleware.ServerImplementation] Fn (the legacy func(ctx, *http.Request,
// *Req) (map[string][]string, error) shape), merging their returned
// grants into ONE map — WITHOUT calling [middleware.CheckScopes] itself,
// so a caller that ALSO has MiddlewareHandler-dispatched Security to merge
// (its GrantedScopes become available only AFTER route dispatch — see
// [MergeMiddlewareHandlerGrants]) can call CheckScopes ONCE, combining
// both sources. Fails fast on the first Fn error, mirroring
// [RunSecurityMiddlewareReflect]'s own established behavior exactly.
func CollectGrantsReflect(ctx context.Context, r *http.Request, reqPtr reflect.Value, impls []middleware.ServerImplementation, secReqs []route.SecurityRequirement) (map[string][]string, error) {
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
			return nil, err
		}
		g, _ := results[0].Interface().(map[string][]string)
		for k, v := range g {
			granted[k] = v
		}
	}
	return granted, nil
}

// MergeMiddlewareHandlerGrants merges GrantedScopes contributed by every
// Security-Satisfying rest.MiddlewareHandler-dispatched middleware into
// granted (mutated in place) — the MiddlewareHandler-dispatched
// counterpart to [CollectGrantsReflect]'s legacy-impls source. outs[i] is
// handler i's decoded Out (boxed `any`, as returned by
// rest.DispatchMiddlewareHandlers); satisfiesPerHandler[i] is that SAME
// handler's own Satisfies field. A handler with empty
// Satisfies (no Security declared), or whose Out carries no field named
// "GrantedScopes" at all, contributes nothing — both are safe no-ops,
// implementing docs/design/d-0007-declarative-middleware-layering.md's RESOLVED
// "conventional field" design (Option 3): a Security Out type is simply
// EXPECTED to carry `GrantedScopes map[string][]string`, read via
// reflection — the SAME technique already used pervasively in this
// codebase, applied to a new, conventionalized field name.
func MergeMiddlewareHandlerGrants(granted map[string][]string, satisfiesPerHandler [][]string, outs []any) {
	for i, satisfies := range satisfiesPerHandler {
		if len(satisfies) == 0 || i >= len(outs) {
			continue
		}
		outVal := reflect.ValueOf(outs[i])
		if outVal.Kind() != reflect.Struct {
			continue
		}
		field := outVal.FieldByName("GrantedScopes")
		if !field.IsValid() {
			continue
		}
		g, _ := field.Interface().(map[string][]string)
		for k, v := range g {
			granted[k] = v
		}
	}
}
