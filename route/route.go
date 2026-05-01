// Package route describes HTTP operations for use with API spec renderers.
//
// A Route is a transport-agnostic descriptor for a single HTTP operation:
// method, path, parameters, request body, and responses. Codecs supply the
// schemas; renderers (such as render/openapi) consume routes to emit specs.
//
// Typical usage:
//
//	routes := []route.Route{
//	    {
//	        Method:      "POST",
//	        Path:        "/users",
//	        OperationID: "createUser",
//	        Summary:     "Create a user",
//	        RequestBody: &route.Body{
//	            Required:   true,
//	            Schema:     CreateUserCodec.Schema,
//	            SchemaName: "CreateUserRequest",
//	        },
//	        Responses: []route.Response{
//	            {Status: "201", Description: "Created", Schema: &UserCodec.Schema, SchemaName: "User"},
//	        },
//	    },
//	}
package route

import "github.com/DaniDeer/go-codex/schema"

// Route describes a single HTTP operation.
type Route struct {
	Method      string // GET, POST, PUT, PATCH, DELETE
	Path        string // e.g. /users/{id}
	OperationID string
	Summary     string
	Description string
	Tags        []string
	PathParams  []Param
	QueryParams []Param
	RequestBody *Body
	Responses   []Response
}

// Param describes a path or query parameter.
type Param struct {
	Name        string
	Description string
	Required    bool
	Schema      schema.Schema
}

// Body describes an HTTP request body.
//
// When SchemaName is non-empty, the renderer emits a $ref to
// components/schemas and registers Schema under that name automatically.
// When SchemaName is empty, Schema is inlined in the operation.
type Body struct {
	Description string
	Required    bool
	// Schema is the payload schema. Required when SchemaName is non-empty.
	Schema schema.Schema
	// SchemaName, when non-empty, emits a $ref and registers Schema in components/schemas.
	SchemaName  string
	ContentType string // defaults to "application/json"
}

// Response describes one HTTP response for an operation.
//
// Status is the HTTP status code as a string: "200", "201", "default", "2XX", etc.
// When SchemaName is non-empty, the renderer emits a $ref to components/schemas.
// A nil Schema with empty SchemaName produces a description-only response (e.g. 204).
type Response struct {
	Status      string // "200", "201", "default", "2XX", etc.
	Description string
	// Schema is the response body schema. Nil means no response body.
	Schema *schema.Schema
	// SchemaName, when non-empty, emits a $ref and registers Schema in components/schemas.
	SchemaName  string
	ContentType string // defaults to "application/json"
}
