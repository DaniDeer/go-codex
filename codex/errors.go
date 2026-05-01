package codex

import "strings"

// ValidationError is a single field-level validation failure returned from struct Decode.
type ValidationError struct {
	Field string // name of the field that failed
	Err   error  // underlying constraint or missing-field error
}

func (e ValidationError) Error() string {
	return "field " + e.Field + ": " + e.Err.Error()
}

func (e ValidationError) Unwrap() error { return e.Err }

// ValidationErrors is a collection of field-level validation errors.
// It implements the error interface; callers can use errors.As to extract it.
type ValidationErrors []ValidationError

func (ve ValidationErrors) Error() string {
	msgs := make([]string, len(ve))
	for i, e := range ve {
		msgs[i] = e.Error()
	}
	return strings.Join(msgs, "; ")
}
