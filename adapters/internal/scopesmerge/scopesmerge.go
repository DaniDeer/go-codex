// Package scopesmerge is the shared, adapters-internal helper for merging
// a bound `MiddlewareHandler`'s (`events.MiddlewareHandler`/
// `reqreply.MiddlewareHandler`) decoded `Out.GrantedScopes` into the SAME
// `granted` map a route/channel's legacy
// [middleware.ServerImplementation] credential Fns already populate,
// before a SINGLE [middleware.CheckScopes] call — docs/roadmap/
// declarative-middleware-layering.md's "Prerequisite for Phase 2
// (api/events)"/Rollout Phase C: found missing during a Phase C review
// (ZERO `GrantedScopes` references existed in any events adapter),
// mirrors adapters/internal/httpsecurity.MergeMiddlewareHandlerGrants's
// identical, already-proven logic for REST — but kept as a SEPARATE
// package (not a cross-import of httpsecurity) since that package's own
// doc comment explicitly scopes it to the net/http-family adapter tree
// only.
//
// Scoped under adapters/internal/ (not the repo-root internal/, unlike
// internal/templatematch) since this package is needed ONLY by the
// events/reqreply adapter trees (adapters/mqtt5, adapters/mqtt,
// adapters/zeromq) — RECEIVING-side dispatch (events' Subscribe,
// reqreply's Serve) only; the SENDING side (Publish/Call) never needs
// this (confirmed via REST's own `ClientMW` dispatch never merging
// grants either — see the roadmap doc's "Learnings" for the full
// reasoning). The merge logic itself is entirely generic (`map[string]
// []string`, `[][]string`, `[]any` — no events- or reqreply-specific
// type anywhere), so one package safely serves both consumers.
package scopesmerge

import "reflect"

// HasSatisfyingHandler reports whether ANY entry in satisfiesPerHandler
// is non-empty — i.e. at least one attached handler is Security-shaped
// (declares a scheme it satisfies), as opposed to every handler being
// general-purpose. Callers use this to decide WHETHER to invoke
// [middleware.CheckScopes] at all: a transport whose LEGACY security
// mechanism is pure binary accept/reject (e.g. zeromq's — confirmed via
// code, it has no grants concept and never populates `granted`) must NOT
// have [middleware.CheckScopes] invoked when the ONLY Satisfies-bearing
// attachment is that legacy mechanism itself (its own Fn success already
// fully satisfies the requirement, by its own pre-existing contract) —
// calling CheckScopes unconditionally would retroactively require a
// grants entry that mechanism can never produce, a confirmed regression
// found via a real test failure during implementation. Gate the NEW,
// unified CheckScopes call on `len(secReqs) > 0 &&
// HasSatisfyingHandler(satisfiesPerHandler)` instead of `len(secReqs) >
// 0` alone.
func HasSatisfyingHandler(satisfiesPerHandler [][]string) bool {
	for _, s := range satisfiesPerHandler {
		if len(s) > 0 {
			return true
		}
	}
	return false
}

// MergeHandlerGrants merges `GrantedScopes` from every handler in
// satisfiesPerHandler/outs whose OWN Satisfies is non-empty into granted
// (mutated in place) — the bound-dispatch counterpart to a channel's
// legacy-Fn-populated grants. outs[i] is handler i's decoded Out (boxed
// `any`, as returned by [events.DispatchSubscribeMiddlewareHandlers]/
// [events.DispatchPublishMiddlewareHandlers]); satisfiesPerHandler[i] is
// that SAME handler's own Satisfies field. A handler with empty Satisfies
// (no Security declared), a nil outs[i] (Subscribe's original 1-return
// shape, HasOut false), or whose Out carries no field named
// "GrantedScopes" at all, contributes nothing — all three are safe
// no-ops, implementing docs/design/d-0007-declarative-middleware-layering.md's
// RESOLVED "conventional field" design (Option 3): a Security Out type is
// simply EXPECTED to carry `GrantedScopes map[string][]string`, read via
// reflection — the SAME technique already used pervasively in this
// codebase, applied to a new, conventionalized field name.
func MergeHandlerGrants(granted map[string][]string, satisfiesPerHandler [][]string, outs []any) {
	for i, satisfies := range satisfiesPerHandler {
		if len(satisfies) == 0 || i >= len(outs) || outs[i] == nil {
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
