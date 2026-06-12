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
