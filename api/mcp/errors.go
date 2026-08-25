package mcp

import (
	"fmt"
	"log/slog"
)

// ToolInputError is returned by [ToolHandle.Decode] when the incoming arguments
// fail codec validation or cannot be decoded into the input type.
// Use [errors.As] to extract the name and underlying error:
//
//	var tie mcp.ToolInputError
//	if errors.As(err, &tie) {
//	    log.Printf("tool %s: bad input: %v", tie.Name, tie.Err)
//	}
type ToolInputError struct {
	// Name is the tool name as registered with the Builder.
	Name string
	// Err is the underlying codec validation or decode error.
	Err error
}

func (e ToolInputError) Error() string {
	return fmt.Sprintf("tool %q: input error: %s", e.Name, e.Err)
}
func (e ToolInputError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e ToolInputError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("name", e.Name),
		slog.Any("cause", e.Err),
	)
}

// ToolOutputError is returned by [ToolHandle.Encode] when the handler's return
// value fails codec validation or cannot be marshalled to JSON.
// This indicates a server-side contract violation — the handler returned data
// that does not satisfy the declared output codec constraints.
//
// Use [errors.As] to extract the tool name and underlying error:
//
//	var toe mcp.ToolOutputError
//	if errors.As(err, &toe) {
//	    log.Printf("tool %s: bad output: %v", toe.Name, toe.Err)
//	}
type ToolOutputError struct {
	// Name is the tool name as registered with the Builder.
	Name string
	// Err is the underlying codec validation or marshal error.
	Err error
}

func (e ToolOutputError) Error() string {
	return fmt.Sprintf("tool %q: output error: %s", e.Name, e.Err)
}
func (e ToolOutputError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e ToolOutputError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("name", e.Name),
		slog.Any("cause", e.Err),
	)
}

// ResourceEncodeError is returned by [ResourceHandle.Encode] when the resource
// value fails codec validation or cannot be marshalled.
//
// Use [errors.As] to extract the URI template and underlying error:
//
//	var ree mcp.ResourceEncodeError
//	if errors.As(err, &ree) {
//	    log.Printf("resource %s: encode failed: %v", ree.URI, ree.Err)
//	}
type ResourceEncodeError struct {
	// URI is the resource URI template.
	URI string
	// Err is the underlying codec validation or marshal error.
	Err error
}

func (e ResourceEncodeError) Error() string {
	return fmt.Sprintf("resource %q: encode error: %s", e.URI, e.Err)
}
func (e ResourceEncodeError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e ResourceEncodeError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("uri", e.URI),
		slog.Any("cause", e.Err),
	)
}

// PromptArgError is returned by [PromptHandle.ValidateArgs] when an argument
// fails its registered codec constraint.
//
// Use [errors.As] to extract the argument name and underlying error:
//
//	var pe mcp.PromptArgError
//	if errors.As(err, &pe) {
//	    log.Printf("prompt arg %q failed: %v", pe.Name, pe.Err)
//	}
type PromptArgError struct {
	// Name is the argument name.
	Name string
	// Err is the underlying codec constraint error.
	Err error
}

func (e PromptArgError) Error() string {
	return fmt.Sprintf("prompt arg %q: %s", e.Name, e.Err)
}
func (e PromptArgError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e PromptArgError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("name", e.Name),
		slog.Any("cause", e.Err),
	)
}

// MissingPromptArgError is returned by [PromptHandle.ValidateArgs] when a
// required argument is absent from the provided args map.
//
// Use [errors.As] to extract the missing argument name:
//
//	var me mcp.MissingPromptArgError
//	if errors.As(err, &me) {
//	    log.Printf("required prompt arg %q was not provided", me.Name)
//	}
type MissingPromptArgError struct {
	Name string
}

func (e MissingPromptArgError) Error() string {
	return fmt.Sprintf("prompt arg %q: required argument missing", e.Name)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e MissingPromptArgError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("name", e.Name),
	)
}
