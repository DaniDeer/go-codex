package rest

import "github.com/DaniDeer/go-codex/internal/middleware"

// This file is api/rest's own, PUBLIC front door onto the shared,
// internal-only ErrorPatternAs/HandleErrorPattern/Case vocabulary
// (internal/middleware) — these 3 functions were ORIGINALLY placed in
// adapters/nethttp (Topic 6's initial decision), moved into api/rest
// directly (a confirmed, fixed design mistake — see
// docs/design/d-0005-error-handling.md's Topic 6 "Design guardrail"
// subsection), and have now been consolidated once more into
// internal/middleware alongside api/reqreply's byte-for-byte-identical
// copy (docs/design/d-0009-internalize-shared-mechanics.md's Phase 4) — part of
// go-codex's "shared cross-pattern MECHANICS live in internal/,
// pattern-specific access lives in the api/port layer" design rule (see
// docs/design/d-0009-internalize-shared-mechanics.md). A user of
// api/rest NEVER imports internal/middleware directly — everything
// below is a thin, same-named alias/forwarding constructor around it.

// ErrorPatternValuer is implemented by any adapter's own
// ErrorPatternResponse type (e.g. nethttp.ErrorPatternResponse,
// mqtt5.ErrorPatternResponse, zeromq.ErrorPatternResponse) carrying a
// matched ErrorPattern's typed payload — see
// [internal/middleware.ErrorPatternValuer] for the full doc comment.
type ErrorPatternValuer = middleware.ErrorPatternValuer

// ErrorPatternAs extracts a matched [ErrorPattern]'s typed payload in one
// call, collapsing the errors.As + type-switch dance
// [ErrorPatternValuer]'s own godoc documents into a single conditional.
//
//	if conflict, ok := rest.ErrorPatternAs[domain.EmailConflictError](err); ok {
//	    return promptDifferentEmail(conflict.Email) // conflict is fully typed
//	}
//
// Returns false when err carries no [ErrorPatternValuer] value at all,
// OR when it does but the value isn't assignable to B. Works
// transparently against ANY adapter's own error-pattern response type
// (e.g. `nethttp.ErrorPatternResponse`, `mqtt5.ErrorPatternResponse`,
// `zeromq.ErrorPatternResponse`) since all of them implement
// [ErrorPatternValuer] — this function never needs to know which one.
func ErrorPatternAs[B any](err error) (B, bool) {
	return middleware.ErrorPatternAs[B](err)
}

// ErrorCase is the type-erased interface each [Case] value implements —
// this is what makes a slice of heterogeneous typed cases possible in
// [HandleErrorPattern] without reflect. See
// [internal/middleware.ErrorCase] for the full doc comment.
type ErrorCase = middleware.ErrorCase

// Case declares one typed handler for [HandleErrorPattern] — T is
// inferred from fn's own parameter type, so no explicit [T] instantiation
// is needed at the call site.
func Case[T any](fn func(T)) ErrorCase {
	return middleware.Case(fn)
}

// HandleErrorPattern extracts a matched [ErrorPattern]'s typed payload
// ONCE (a single errors.As call), then dispatches to the FIRST [Case]
// whose T matches the payload's concrete type — the closest visual
// parity to a switch/match expression, since each Case's type is
// inferred from its own closure parameter.
//
//	handled := rest.HandleErrorPattern(err,
//	    rest.Case(func(e domain.EmailConflictError) { promptDifferentEmail(e.Email) }),
//	    rest.Case(func(e domain.ValidationError) { showValidationErrors(e) }),
//	)
//
// Returns false when err carries no [ErrorPatternValuer] value at all,
// OR when it does but no Case's T matches its value's concrete type.
// When a value could satisfy more than one registered Case's type (e.g.
// an interface T), the FIRST matching Case (in argument order) wins —
// mirrors this library's established first-declared-wins precedent
// elsewhere.
func HandleErrorPattern(err error, cases ...ErrorCase) bool {
	return middleware.HandleErrorPattern(err, cases...)
}
