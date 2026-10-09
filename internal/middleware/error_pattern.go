package middleware

import "errors"

// This file consolidates the ErrorPatternAs/Case/HandleErrorPattern
// family out of api/rest and api/reqreply's own, previously independent,
// byte-for-byte-identical copies (docs/design/d-0009-internalize-shared-mechanics.md's
// Phase 4) — confirmed via direct diff to have zero logic divergence
// between the two. api/events correctly has NO equivalent: pub/sub's
// Publish is a ONE-WAY, fire-and-forget operation with no reply channel
// to decode a typed payload from, so there is nothing for an
// ErrorPatternAs/HandleErrorPattern-shaped helper to do on that side —
// a structural non-issue, not a gap.
//
// ErrorPatternValuer/ErrorPatternAs/Case/HandleErrorPattern are pure,
// protocol-agnostic MECHANICS touching only this interface — they belong
// here per the same "shared cross-pattern MECHANICS live in internal/"
// rule as the rest of this package (see doc.go and
// docs/design/d-0009-internalize-shared-mechanics.md). api/rest's and
// api/reqreply's own error_pattern_client.go files are thin, same-named
// public wrappers (type aliases + forwarding functions) around these.

// ErrorPatternValuer is implemented by any adapter-specific
// ErrorPatternResponse type (e.g. nethttp.ErrorPatternResponse,
// mqtt5.ErrorPatternResponse, zeromq.ErrorPatternResponse) carrying a
// matched ErrorPattern's typed payload — the SAME interface
// ErrorPatternOpt.Match already uses to populate it.
type ErrorPatternValuer interface {
	ErrorPatternValue() any
}

// ErrorPatternAs extracts a matched ErrorPattern's typed payload in one
// call, collapsing the errors.As + type-switch dance ErrorPatternValuer's
// own godoc documents into a single conditional.
//
//	if conflict, ok := middleware.ErrorPatternAs[domain.EmailConflictError](err); ok {
//	    return promptDifferentEmail(conflict.Email) // conflict is fully typed
//	}
//
// Returns false when err carries no ErrorPatternValuer value at all, OR
// when it does but the value isn't assignable to B. Works transparently
// against ANY adapter's own error-pattern response type since all of
// them implement ErrorPatternValuer — this function never needs to know
// which one.
func ErrorPatternAs[B any](err error) (B, bool) {
	var target ErrorPatternValuer
	if !errors.As(err, &target) {
		var zero B
		return zero, false
	}
	b, ok := target.ErrorPatternValue().(B)
	return b, ok
}

// ErrorCase is the type-erased interface each Case value implements —
// this is what makes a slice of heterogeneous typed cases possible in
// HandleErrorPattern without reflect. EXPORTED (unlike each pattern's
// former unexported errorCase) since Case[T]'s return type must be
// nameable for api/rest's/api/reqreply's own type-alias wrappers around
// it — a pure visibility change, zero behavior difference.
type ErrorCase interface {
	tryHandle(value any) bool
}

type typedCase[T any] struct {
	fn func(T)
}

// Case declares one typed handler for HandleErrorPattern — T is inferred
// from fn's own parameter type, so no explicit [T] instantiation is
// needed at the call site.
func Case[T any](fn func(T)) ErrorCase {
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

// HandleErrorPattern extracts a matched ErrorPattern's typed payload ONCE
// (a single errors.As call), then dispatches to the FIRST Case whose T
// matches the payload's concrete type — the closest visual parity to a
// switch/match expression, since each Case's type is inferred from its
// own closure parameter.
//
//	handled := middleware.HandleErrorPattern(err,
//	    middleware.Case(func(e domain.EmailConflictError) { promptDifferentEmail(e.Email) }),
//	    middleware.Case(func(e domain.ValidationError) { showValidationErrors(e) }),
//	)
//
// Returns false when err carries no ErrorPatternValuer value at all, OR
// when it does but no Case's T matches its value's concrete type. When a
// value could satisfy more than one registered Case's type (e.g. an
// interface T), the FIRST matching Case (in argument order) wins —
// mirrors this library's established first-declared-wins precedent
// elsewhere.
func HandleErrorPattern(err error, cases ...ErrorCase) bool {
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
