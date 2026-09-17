package reqreply

import "errors"

// This file's 3 functions were ORIGINALLY placed identically (byte for
// byte) in BOTH adapters/mqtt5 and adapters/zeromq (Topic 6's initial
// decision) under the reasoning "near-identical copies, one per client
// package, mirroring the existing precedent that each adapter keeps its
// own ErrorPatternResponse type." That reasoning conflated two different
// things: it's correct that the CONCRETE response type
// (mqtt5.ErrorPatternResponse, zeromq.ErrorPatternResponse) legitimately
// differs per adapter (protocol-specific fields like Code/Body) — but
// these 3 helper functions touch ONLY the shared, already-core-layer
// [ErrorPatternValuer] interface (the SAME interface
// [ErrorPatternOpt.Match] already uses) and have ZERO protocol-specific
// logic. Per this library's "thin adapter" design guardrail
// (docs/roadmap/error-handling-rest-events-reqreply.md's Topic 6 "Design
// guardrail" subsection): a user-facing convenience helper that only
// touches core api/* types belongs in api/*, never in adapters/*, even
// when multiple adapters happen to implement that boundary today. Moved
// here (from BOTH adapters/mqtt5 and adapters/zeromq, closing a genuine
// byte-for-byte code duplication in the process) for that reason — a
// confirmed, fixed design mistake, not a reinterpretation of new
// requirements.

// ErrorPatternAs extracts a matched [ErrorPattern]'s typed payload in one
// call, collapsing the errors.As + type-switch dance
// [ErrorPatternValuer]'s own godoc documents into a single conditional.
//
//	if conflict, ok := reqreply.ErrorPatternAs[domain.ConflictError](err); ok {
//	    // conflict is fully typed
//	}
//
// Returns false when err carries no [ErrorPatternValuer] value at all,
// OR when it does but the value isn't assignable to B. Works
// transparently against ANY transport's own error-pattern response type
// (e.g. `mqtt5.ErrorPatternResponse`, `zeromq.ErrorPatternResponse`)
// since both implement [ErrorPatternValuer] — this function never needs
// to know which one.
func ErrorPatternAs[B any](err error) (B, bool) {
	var target ErrorPatternValuer
	if !errors.As(err, &target) {
		var zero B
		return zero, false
	}
	b, ok := target.ErrorPatternValue().(B)
	return b, ok
}

// errorCase is the internal, type-erased interface each [Case] value
// implements — this is what makes a slice of heterogeneous typed cases
// possible in [HandleErrorPattern] without reflect.
type errorCase interface {
	tryHandle(value any) bool
}

type typedCase[T any] struct {
	fn func(T)
}

// Case declares one typed handler for [HandleErrorPattern] — T is
// inferred from fn's own parameter type, so no explicit [T] instantiation
// is needed at the call site.
func Case[T any](fn func(T)) errorCase {
	return typedCase[T]{fn: fn}
}

func (c typedCase[T]) tryHandle(value any) bool {
	v, ok := value.(T)
	if !ok {
		return false
	}
	c.fn(v)
	return true
}

// HandleErrorPattern extracts a matched [ErrorPattern]'s typed payload
// ONCE (a single errors.As call), then dispatches to the FIRST [Case]
// whose T matches the payload's concrete type — the closest visual
// parity to a switch/match expression, since each Case's type is
// inferred from its own closure parameter.
//
//	handled := reqreply.HandleErrorPattern(err,
//	    reqreply.Case(func(e domain.ConflictError) { ... }),
//	    reqreply.Case(func(e domain.ValidationError) { ... }),
//	)
//
// Returns false when err carries no [ErrorPatternValuer] value at all,
// OR when it does but no Case's T matches its value's concrete type.
// When a value could satisfy more than one registered Case's type (e.g.
// an interface T), the FIRST matching Case (in argument order) wins —
// mirrors this library's established first-declared-wins precedent
// elsewhere.
func HandleErrorPattern(err error, cases ...errorCase) bool {
	var target ErrorPatternValuer
	if !errors.As(err, &target) {
		return false
	}
	for _, c := range cases {
		if c.tryHandle(target.ErrorPatternValue()) {
			return true
		}
	}
	return false
}
