package file

import (
	"errors"
	"fmt"
	"log/slog"
)

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

// errorPatternMatcher is the type-erased bridge every declared
// ErrorPatterns[B] reduces to once attached to a role — lets roleCore
// store it without a type parameter of its own (B varies per
// attachment, just like roleBoundMW's In/Out).
type errorPatternMatcher interface {
	matchAny(err error) (any, bool)
}

// matchAny makes ErrorPatterns[B] satisfy [errorPatternMatcher] —
// type-erases B into `any` for roleCore's storage.
func (ps ErrorPatterns[B]) matchAny(err error) (any, bool) {
	b, ok := ps.ResponseFor(err)
	return b, ok
}

// ErrorPatternMatchedError wraps an original Backend-originated error
// when it matched a role's declared [ErrorPatterns] — the auto-
// invocation this round's D-0010 alignment adds (previously a
// documented gap: ErrorPattern existed but was never automatically
// consulted, unlike api/rest/api/events/api/reqreply's own dispatch
// cores). errors.As still reaches the ORIGINAL error via Unwrap — the
// match is additive, not a replacement.
//
// Payload is `any` (not a type parameter) deliberately: the caller
// already knows B (the type they declared the pattern with), so a
// plain type assertion (`pm.Payload.(MyResponseType)`) is no less
// ergonomic than a generic field would be, and avoids forcing
// ErrorPatternMatchedError itself to carry a type parameter that would
// otherwise propagate through every signature that could return it.
type ErrorPatternMatchedError struct {
	Payload any
	Err     error
}

func (e ErrorPatternMatchedError) Error() string {
	return fmt.Sprintf("api/file: error matched a declared ErrorPattern: %s", e.Err)
}

func (e ErrorPatternMatchedError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e ErrorPatternMatchedError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("payload_type", fmt.Sprintf("%T", e.Payload)),
		slog.Any("err", e.Err),
	)
}

// applyErrorPattern consults core's declared ErrorPatterns (if any)
// against err, wrapping it in [ErrorPatternMatchedError] on a match —
// a complete no-op (err returned unchanged) when core has no declared
// ErrorPatterns, or err is nil, or nothing matches. Called via defer by
// every xxxCore function in dispatch.go, so EVERY return path (not just
// one) gets uniform coverage without touching each individual return
// statement.
func applyErrorPattern[T any](core roleCore[T], err error) error {
	if err == nil || core.errorPatterns == nil {
		return err
	}
	if payload, ok := core.errorPatterns.matchAny(err); ok {
		return ErrorPatternMatchedError{Payload: payload, Err: err}
	}
	return err
}
