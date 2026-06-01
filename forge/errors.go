package forge

import (
	"fmt"
	"log/slog"
)

// InputError is returned by Function*.Apply when an input value fails its codec validation.
// Function is the function's name, Input is the input parameter's name, Err is the underlying error.
type InputError struct {
	Function string
	Input    string
	Err      error
}

func (e InputError) Error() string {
	return fmt.Sprintf("function %q: input %q: %s", e.Function, e.Input, e.Err)
}

func (e InputError) Unwrap() error { return e.Err }

// LogValue implements slog.LogValuer for structured logging.
func (e InputError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("function", e.Function),
		slog.String("input", e.Input),
		slog.Any("cause", e.Err),
	)
}

// OutputError is returned by Function*.Apply when the computed output fails its codec validation.
// Function is the function's name, Output is the output parameter's name, Err is the underlying error.
type OutputError struct {
	Function string
	Output   string
	Err      error
}

func (e OutputError) Error() string {
	return fmt.Sprintf("function %q: output %q: %s", e.Function, e.Output, e.Err)
}

func (e OutputError) Unwrap() error { return e.Err }

// LogValue implements slog.LogValuer for structured logging.
func (e OutputError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("function", e.Function),
		slog.String("output", e.Output),
		slog.Any("cause", e.Err),
	)
}

// ApplyError is returned by Function*.Apply when the compute function itself returns an error.
// Function is the function's name, Err is the error returned by the compute function.
type ApplyError struct {
	Function string
	Err      error
}

func (e ApplyError) Error() string {
	return fmt.Sprintf("function %q: %s", e.Function, e.Err)
}

func (e ApplyError) Unwrap() error { return e.Err }

// LogValue implements slog.LogValuer for structured logging.
func (e ApplyError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("function", e.Function),
		slog.Any("cause", e.Err),
	)
}

// RefinementError is returned by Function*.Apply when a cross-input constraint fails.
// It is produced after all individual input validations pass but before the compute
// function runs. Function is the function's name, Err is the constraint error returned
// by the refinement function.
type RefinementError struct {
	Function string
	Err      error
}

func (e RefinementError) Error() string {
	return fmt.Sprintf("function %q: refinement: %s", e.Function, e.Err)
}

func (e RefinementError) Unwrap() error { return e.Err }

// LogValue implements slog.LogValuer for structured logging.
func (e RefinementError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("function", e.Function),
		slog.Any("cause", e.Err),
	)
}

// constructor argument is missing. Func identifies the constructor (e.g. "forge.New"),
// Field is "name" or "version".
type ConfigError struct {
	Func  string
	Field string
}

func (e ConfigError) Error() string {
	return fmt.Sprintf("%s: %s must not be empty", e.Func, e.Field)
}

// LogValue implements slog.LogValuer for structured logging.
func (e ConfigError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("constructor", e.Func),
		slog.String("field", e.Field),
	)
}
