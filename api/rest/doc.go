// Package rest provides a transport-agnostic REST API builder for go-codex.
//
// Define routes declaratively with codec-backed request and response types;
// register them with a [Server] to obtain a [RouteHandle] with typed Decode
// and Encode helpers. Pass those helpers to any HTTP framework (net/http, Gin,
// Chi, Echo) — this package does not import net/http or any framework.
//
// Spec generation is also available: [Server.OpenAPISpec] derives a complete
// OpenAPI 3.1 document from the registered routes.
//
// Typical usage:
//
//	b := rest.NewServer(rest.Info{Title: "User API", Version: "1.0.0"})
//	b.AddServer("production", rest.ServerEntry{URL: "https://api.example.com"})
//
//	// Declare the route as a value — define once, pass around, register later.
//	var createUser = rest.NewRoute[CreateUserReq, User]("POST", "/users/{id}",
//	    createUserCodec, userCodec,
//	    rest.RouteMeta{OperationID: "createUser", Summary: "Create a user",
//	        ReqSchemaName: "CreateUserRequest", RespSchemaName: "User"},
//	    rest.PathParam{Name: "id"}.WithCodec(uuidCodec),
//	)
//
//	handle, err := createUser.Register(b)
//	handle.
//	    WithRequestFormats(format.JSON(createUserCodec), format.YAML(createUserCodec)).
//	    WithFormats(format.JSON(userCodec))
//
//	// In your HTTP handler (any framework):
//	req, err := handle.Decode(body)      // JSON → CreateUserReq, validates
//	user, err := myService.CreateUser(req)
//	out, err  := handle.Encode(user)     // User → JSON
//
//	// OpenAPI 3.1 spec:
//	doc, err := b.OpenAPISpec()
//	yaml, _  := doc.MarshalYAML()
//
// Encoding is JSON only by default. Use [RouteHandle.WithRequestFormats] and
// [RouteHandle.WithFormats] to enable additional formats such as YAML,
// TOML, or templ HTML.
package rest
