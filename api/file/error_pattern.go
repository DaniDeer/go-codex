package file

import "errors"

// ErrorPattern declares a typed, codec-backed error response for a
// Backend-specific error — mirrors
// [github.com/DaniDeer/go-codex/api/rest.ErrorPattern]/
// [github.com/DaniDeer/go-codex/api/events.ErrorChannel]/
// [github.com/DaniDeer/go-codex/api/reqreply.ErrorPattern]'s identical
// shape: `errors.As` match, first-declared-rule-wins precedence, direct
// (assignable) or mapped (mapFn) modes.
type ErrorPattern[E error, B any] struct {
	mapFn func(E) (B, error)
}

// NewErrorPattern declares a DIRECT-mode pattern: E is itself the
// response payload. Use [NewMappedErrorPattern] when E needs
// transforming into a different payload shape B.
func NewErrorPattern[E error]() ErrorPattern[E, E] {
	return ErrorPattern[E, E]{}
}

// NewMappedErrorPattern declares a MAPPED-mode pattern: mapFn converts a
// matched E into payload B.
func NewMappedErrorPattern[E error, B any](mapFn func(E) (B, error)) ErrorPattern[E, B] {
	return ErrorPattern[E, B]{mapFn: mapFn}
}

// Match reports whether err matches this pattern's E (via [errors.As]),
// and if so, resolves it into payload B — direct mode asserts E as B
// (always valid, since direct-mode E==B), mapped mode calls mapFn.
func (p ErrorPattern[E, B]) Match(err error) (B, bool) {
	var zero B
	var e E
	if !errors.As(err, &e) {
		return zero, false
	}
	if p.mapFn != nil {
		b, mapErr := p.mapFn(e)
		if mapErr != nil {
			return zero, false
		}
		return b, true
	}
	if b, ok := any(e).(B); ok {
		return b, true
	}
	return zero, false
}

// ErrorPatterns is an ordered list of patterns sharing one payload type
// B — first-declared-rule-wins, mirrors every other boundary's
// identical first-match convention.
type ErrorPatterns[B any] []func(error) (B, bool)

// Append registers p (any E) onto patterns, type-erasing its Match
// method into the shared matcher shape.
func Append[E error, B any](patterns ErrorPatterns[B], p ErrorPattern[E, B]) ErrorPatterns[B] {
	return append(patterns, func(err error) (B, bool) { return p.Match(err) })
}

// ResponseFor returns the first matching pattern's resolved payload, in
// declaration order.
func (ps ErrorPatterns[B]) ResponseFor(err error) (B, bool) {
	var zero B
	for _, match := range ps {
		if b, ok := match(err); ok {
			return b, true
		}
	}
	return zero, false
}
