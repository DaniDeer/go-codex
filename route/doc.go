// Package route holds spec descriptors shared by the OpenAPI and AsyncAPI
// renderers: HTTP operation shapes and the security-scheme vocabulary.
//
// A Route is a transport-agnostic descriptor for a single HTTP operation:
// method, path, parameters, request body, and responses. Codecs supply the
// schemas; renderers (such as render/openapi) consume routes to emit specs.
// Route/Param/Body/Response are HTTP-only and used solely by [api/rest] and
// [render/openapi].
//
// SecurityScheme, SecurityRequirement, and OAuthFlows are transport-agnostic:
// the same bearer/apiKey/oauth2/openIdConnect vocabulary applies to both
// OpenAPI (REST, via [api/rest]) and AsyncAPI (pub/sub channels, via
// [api/events]) security schemes, so it lives here rather than being
// duplicated per renderer. See [render/openapi] and [render/asyncapi/v3] for
// the consumers.
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
