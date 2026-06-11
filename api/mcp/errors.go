package mcp

import "fmt"

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

// ToolOutputError is returned by [ToolHandle.Encode] when the handler's return
// value fails codec validation or cannot be marshalled to JSON.
// This indicates a server-side contract violation — the handler returned data
// that does not satisfy the declared output codec constraints.
type ToolOutputError struct {
	Name string
	Err  error
}

func (e ToolOutputError) Error() string {
	return fmt.Sprintf("tool %q: output error: %s", e.Name, e.Err)
}
func (e ToolOutputError) Unwrap() error { return e.Err }

// ResourceEncodeError is returned by [ResourceHandle.Encode] when the resource
// value fails codec validation or cannot be marshalled.
type ResourceEncodeError struct {
	// URI is the resource URI template.
	URI string
	Err error
}

func (e ResourceEncodeError) Error() string {
	return fmt.Sprintf("resource %q: encode error: %s", e.URI, e.Err)
}
func (e ResourceEncodeError) Unwrap() error { return e.Err }

// ResourceURIVarError is returned by [ResourceHandle.BuildURI] or
// [ResourceHandle.ValidateURIVars] when a URI variable fails its registered
// codec constraint.
type ResourceURIVarError struct {
	Name  string
	Value string
	Err   error
}

func (e ResourceURIVarError) Error() string {
	return fmt.Sprintf("resource URI var %q=%q: %s", e.Name, e.Value, e.Err)
}
func (e ResourceURIVarError) Unwrap() error { return e.Err }

// MissingResourceURIVarError is returned by [ResourceHandle.BuildURI] when a
// required URI template variable is absent from the provided vars map.
type MissingResourceURIVarError struct {
	Name string
}

func (e MissingResourceURIVarError) Error() string {
	return fmt.Sprintf("resource URI var %q: missing required variable", e.Name)
}

// PromptArgError is returned by [PromptHandle.ValidateArgs] when an argument
// fails its registered codec constraint.
type PromptArgError struct {
	// Name is the argument name.
	Name string
	Err  error
}

func (e PromptArgError) Error() string {
	return fmt.Sprintf("prompt arg %q: %s", e.Name, e.Err)
}
func (e PromptArgError) Unwrap() error { return e.Err }

// MissingPromptArgError is returned by [PromptHandle.ValidateArgs] when a
// required argument is absent from the provided args map.
type MissingPromptArgError struct {
	Name string
}

func (e MissingPromptArgError) Error() string {
	return fmt.Sprintf("prompt arg %q: required argument missing", e.Name)
}
