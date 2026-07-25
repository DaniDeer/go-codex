package codex

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
)

// ErrMissingField is returned when a required struct field is absent from the input.
// Use errors.Is to check for this sentinel.
var ErrMissingField = errors.New("missing required field")

// ErrUnknownField is returned by [StrictStruct]-built codecs when the input
// contains a key not declared by any field — the "additionalProperties: false"
// case. Use errors.Is to check for this sentinel. Like [ErrMissingField], the
// offending field name is carried by the wrapping [ValidationError.Field],
// not by this sentinel itself.
var ErrUnknownField = errors.New("unknown field")

// ValidationError is a single field-level validation failure returned from struct Decode.
type ValidationError struct {
	Field string // name of the field that failed
	Err   error  // underlying constraint or missing-field error
}

func (e ValidationError) Error() string {
	return "field " + e.Field + ": " + e.Err.Error()
}

func (e ValidationError) Unwrap() error { return e.Err }

// LogValue implements slog.LogValuer for structured logging.
func (e ValidationError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("field", e.Field),
		slog.Any("cause", e.Err),
	)
}

// ValidationErrors is a collection of field-level validation errors.
// It implements the error interface; callers can use errors.As to extract it.
// Unwrap returns the individual errors as a []error slice for errors.Is/As traversal.
type ValidationErrors []ValidationError

func (ve ValidationErrors) Error() string {
	msgs := make([]string, len(ve))
	for i, e := range ve {
		msgs[i] = e.Error()
	}
	return strings.Join(msgs, "; ")
}

// Unwrap returns the individual ValidationError values as a []error slice,
// enabling errors.Is and errors.As to traverse the full list.
func (ve ValidationErrors) Unwrap() []error {
	errs := make([]error, len(ve))
	for i, e := range ve {
		errs[i] = e
	}
	return errs
}

// LogValue implements slog.LogValuer for structured logging.
// Each field name is the slog key; its value is the underlying error (which invokes
// LogValue on types like ConstraintError, preserving nested structure).
func (ve ValidationErrors) LogValue() slog.Value {
	attrs := make([]slog.Attr, len(ve))
	for i, e := range ve {
		attrs[i] = slog.Any(e.Field, e.Err)
	}
	return slog.GroupValue(attrs...)
}

// ConstraintError is returned when a Refine constraint check fails during Decode.
// Name identifies the constraint (e.g. "minLen(3)"); Message describes the failure.
type ConstraintError struct {
	Name    string // constraint identifier
	Message string // human-readable failure description
}

func (e ConstraintError) Error() string {
	return fmt.Sprintf("constraint failed (%s): %s", e.Name, e.Message)
}

// LogValue implements slog.LogValuer for structured logging.
func (e ConstraintError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("constraint", e.Name),
		slog.String("message", e.Message),
	)
}

// TypeMismatchError is returned when a codec receives a value of an unexpected type.
// Expected names the required type; Got names the actual type received.
type TypeMismatchError struct {
	Expected string // e.g. "object", "array", "string"
	Got      string // e.g. "int", "bool"
}

func (e TypeMismatchError) Error() string {
	return fmt.Sprintf("expected %s, got %s", e.Expected, e.Got)
}

// LogValue implements slog.LogValuer for structured logging.
func (e TypeMismatchError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("expected", e.Expected),
		slog.String("got", e.Got),
	)
}

// ElementError wraps a decode error at a specific slice index.
type ElementError struct {
	Index int
	Err   error
}

func (e ElementError) Error() string {
	return fmt.Sprintf("element %d: %s", e.Index, e.Err.Error())
}

func (e ElementError) Unwrap() error { return e.Err }

// LogValue implements slog.LogValuer for structured logging.
func (e ElementError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.Int("index", e.Index),
		slog.Any("cause", e.Err),
	)
}

// KeyError wraps a decode error at a specific map key.
type KeyError struct {
	Key string
	Err error
}

func (e KeyError) Error() string {
	return fmt.Sprintf("key %q: %s", e.Key, e.Err.Error())
}

func (e KeyError) Unwrap() error { return e.Err }

// LogValue implements slog.LogValuer for structured logging.
func (e KeyError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("key", e.Key),
		slog.Any("cause", e.Err),
	)
}

// UnknownVariantError is returned when a tagged union receives a tag value that
// does not match any registered variant. Tag is the discriminator field name;
// Variant is the unrecognised tag value.
type UnknownVariantError struct {
	Tag     string // discriminator field name
	Variant string // unrecognised tag value
}

func (e UnknownVariantError) Error() string {
	return fmt.Sprintf("field %s: unknown variant %q", e.Tag, e.Variant)
}

// LogValue implements slog.LogValuer for structured logging.
func (e UnknownVariantError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("tag", e.Tag),
		slog.String("variant", e.Variant),
	)
}

// VariantError is returned when a known tagged-union variant fails to encode or decode.
// Tag is the discriminator field name; Variant is the matched variant value.
// Err is always non-nil; use UnknownVariantError for unrecognised tag values.
type VariantError struct {
	Tag     string // discriminator field name
	Variant string // matched variant value
	Err     error  // underlying encode or decode failure
}

func (e VariantError) Error() string {
	return fmt.Sprintf("variant [%s=%q]: %s", e.Tag, e.Variant, e.Err.Error())
}

func (e VariantError) Unwrap() error { return e.Err }

// LogValue implements slog.LogValuer for structured logging.
func (e VariantError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("tag", e.Tag),
		slog.String("variant", e.Variant),
		slog.Any("cause", e.Err),
	)
}

// EitherError is returned when all branches of an Either2 or UntaggedUnion codec
// fail to decode. Errors contains one error per branch in order.
type EitherError struct {
	Errors []error
}

func (e EitherError) Error() string {
	msgs := make([]string, len(e.Errors))
	for i, err := range e.Errors {
		msgs[i] = fmt.Sprintf("branch %d: %s", i+1, err.Error())
	}
	return "all branches failed: " + strings.Join(msgs, "; ")
}

// Unwrap returns all branch errors for errors.Is/As traversal.
func (e EitherError) Unwrap() []error { return e.Errors }

// LogValue implements slog.LogValuer for structured logging.
func (e EitherError) LogValue() slog.Value {
	attrs := make([]slog.Attr, len(e.Errors))
	for i, err := range e.Errors {
		attrs[i] = slog.Any(fmt.Sprintf("branch%d", i+1), err)
	}
	return slog.GroupValue(attrs...)
}
