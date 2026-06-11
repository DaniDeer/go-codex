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

// ResourceParamError is returned by [ResourceHandle.BuildURI] or
// [ResourceHandle.ValidateURIVars] when a URI variable fails its registered
// codec constraint.
//
// Use [errors.As] to extract the failing variable name and value:
//
//	var paramErr mcp.ResourceParamError
//	if errors.As(err, &paramErr) {
//	    log.Printf("bad value %q for {%s}: %v", paramErr.Value, paramErr.Name, paramErr.Err)
//	}
type ResourceParamError struct {
	Name  string // variable name without braces
	Value string // the value that failed validation
	Err   error  // the underlying constraint or codec error
}

func (e ResourceParamError) Error() string {
	return fmt.Sprintf("resource URI var {%s}: invalid value %q: %s", e.Name, e.Value, e.Err)
}
func (e ResourceParamError) Unwrap() error { return e.Err }

// MissingResourceVarError is returned by [ResourceHandle.BuildURI] when a
// {varName} placeholder in the URI template has no corresponding entry in the
// vars map.
//
// Use [errors.As] to extract the missing variable name:
//
//	var missingErr mcp.MissingResourceVarError
//	if errors.As(err, &missingErr) {
//	    log.Printf("caller forgot to supply URI variable {%s}", missingErr.Name)
//	}
type MissingResourceVarError struct {
	Name string // the variable name (without braces) that had no value
}

func (e MissingResourceVarError) Error() string {
	return fmt.Sprintf("missing value for resource URI variable {%s}", e.Name)
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

// InvalidResourceParamError is returned by [Resource.Register] when a
// [ResourceParam] entry names a variable that does not appear in the URI template.
//
// Use [errors.As] to extract the offending name and the URI template:
//
//	var paramErr mcp.InvalidResourceParamError
//	if errors.As(err, &paramErr) {
//	    log.Printf("ResourceParam %q not in URI template %q", paramErr.Name, paramErr.URITemplate)
//	}
type InvalidResourceParamError struct {
	// Name is the ResourceParam variable name not found in the template.
	Name string
	// URITemplate is the URI template that was checked.
	URITemplate string
}

func (e InvalidResourceParamError) Error() string {
	return fmt.Sprintf("api/mcp: ResourceParam %q not found in URI template %q", e.Name, e.URITemplate)
}
