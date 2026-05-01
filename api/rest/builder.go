// Package rest provides a transport-agnostic REST API builder for go-codex.
//
// Define routes with codec-backed request and response types; the builder
// returns a [RouteHandle] with typed Decode and Encode helpers. Pass those
// helpers to any HTTP framework (net/http, Gin, Chi, Echo) — this package
// does not import net/http or any framework.
//
// Spec generation is also available: [Builder.OpenAPISpec] derives a complete
// OpenAPI 3.1 document from the registered routes.
//
// Typical usage:
//
//	b := rest.NewBuilder(rest.Info{Title: "User API", Version: "1.0.0"})
//	b.AddServer("production", rest.Server{URL: "https://api.example.com"})
//
//	createUser := rest.AddRoute[CreateUserReq, User](b, "POST", "/users",
//	    createUserCodec, userCodec, rest.RouteConfig{
//	        OperationID:    "createUser",
//	        Summary:        "Create a user",
//	        ReqSchemaName:  "CreateUserRequest",
//	        RespSchemaName: "User",
//	    })
//
//	// In your HTTP handler (any framework):
//	req, err := createUser.Decode(body)      // JSON → CreateUserReq, validates
//	user, err := myService.CreateUser(req)
//	out, err  := createUser.Encode(user)     // User → JSON
//
//	// OpenAPI 3.1 spec:
//	doc, err := b.OpenAPISpec()
//	yaml, _  := doc.MarshalYAML()
//
// Encoding is JSON only. AddRoute uses [format.JSON] internally; for other
// formats construct a [format.Format] directly and call its Unmarshal/Marshal.
package rest

import (
	"slices"
	"strings"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/render/openapi"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/schema"
)

// Info is an alias for [openapi.Info]. Using the alias avoids duplicating
// fields and keeps the two in sync automatically.
type Info = openapi.Info

// Server is an alias for [openapi.Server].
type Server = openapi.Server

// Param is an alias for [route.Param] so callers do not need to import route
// just to specify path or query parameters.
type Param = route.Param

// ResponseMeta describes one additional response entry for a route (errors,
// redirects, etc.). The primary success response is derived from the response
// codec and RespStatus/RespDescription/RespSchemaName in RouteConfig.
type ResponseMeta struct {
	Status      string // e.g. "400", "404", "default"
	Description string
	Schema      *schema.Schema // nil for description-only responses (e.g. 404)
	SchemaName  string         // non-empty → $ref in spec
}

// RouteConfig holds metadata for a route registration. It controls both spec
// output and default behaviour of the returned [RouteHandle].
type RouteConfig struct {
	OperationID string
	Summary     string
	Description string
	Tags        []string

	// PathParams and QueryParams are included in the OpenAPI spec as path/query
	// parameters. Codec-based path/query decoding is a future extension.
	PathParams  []route.Param
	QueryParams []route.Param

	// ReqSchemaName, when non-empty, emits a $ref for the request body schema
	// in the spec and registers the schema under that name in components/schemas.
	ReqSchemaName string

	// RespStatus is the HTTP status code for the primary success response.
	// Defaults to "201" for POST, "200" for all other methods.
	RespStatus string

	// RespDescription is the description for the primary success response.
	RespDescription string

	// RespSchemaName, when non-empty, emits a $ref for the response schema.
	RespSchemaName string

	// Responses are additional response entries (error codes, etc.) appended
	// after the primary success response in the spec.
	Responses []ResponseMeta
}

// RouteHandle is returned by [AddRoute]. It holds the frozen spec descriptor
// and codec-backed Decode/Encode helpers.
//
// Decode and Encode use JSON encoding. For body-less methods (GET, HEAD,
// DELETE), Decode can still be called if the request carries a body, but
// typical REST usage will not call it.
type RouteHandle[Req, Resp any] struct {
	// Descriptor is the frozen route.Route built at registration time.
	// Use it to inspect method, path, parameters, and spec metadata.
	Descriptor route.Route

	// Decode deserialises and validates a JSON request body into Req.
	// All Refine constraints on the request codec run automatically.
	Decode func(body []byte) (Req, error)

	// Encode serialises Resp to JSON bytes.
	Encode func(resp Resp) ([]byte, error)
}

// routeEntry is the type-erased interface stored inside Builder.
type routeEntry interface {
	descriptor() route.Route
}

// typedRouteEntry stores the frozen descriptor for a single route.
type typedRouteEntry[Req, Resp any] struct {
	frozen route.Route
}

func (e *typedRouteEntry[Req, Resp]) descriptor() route.Route { return e.frozen }

// Builder accumulates route registrations and produces OpenAPI specs.
// Create one with [NewBuilder].
type Builder struct {
	info    Info
	servers []Server
	entries []routeEntry
}

// NewBuilder returns a Builder initialised with the given API metadata.
func NewBuilder(info Info) *Builder {
	return &Builder{info: info}
}

// AddServer appends a named server entry to the spec. name is used as the
// server's Description if s.Description is empty, making it consistent with
// [events.Builder.AddServer].
func (b *Builder) AddServer(name string, s Server) *Builder {
	if s.Description == "" {
		s.Description = name
	}
	b.servers = append(b.servers, s)
	return b
}

// AddRoute registers a route with the builder and returns a [RouteHandle].
//
// reqCodec is used to decode and validate the JSON request body.
// respCodec is used to encode the JSON response.
//
// AddRoute is a free function (not a method) because Go requires type
// parameters to appear on free functions, not on method receivers.
//
// The descriptor is built and frozen at call time; later mutations to config
// do not affect the registered route or the returned handle.
func AddRoute[Req, Resp any](
	b *Builder,
	method, path string,
	reqCodec codex.Codec[Req],
	respCodec codex.Codec[Resp],
	config RouteConfig,
) *RouteHandle[Req, Resp] {
	frozen := buildDescriptor(method, path, reqCodec.Schema, respCodec.Schema, config)

	entry := &typedRouteEntry[Req, Resp]{frozen: frozen}
	b.entries = append(b.entries, entry)

	jsonReq := format.JSON(reqCodec)
	jsonResp := format.JSON(respCodec)

	return &RouteHandle[Req, Resp]{
		Descriptor: frozen,
		Decode:     func(body []byte) (Req, error) { return jsonReq.Unmarshal(body) },
		Encode:     func(resp Resp) ([]byte, error) { return jsonResp.Marshal(resp) },
	}
}

// OpenAPISpec builds a complete OpenAPI 3.1 document from all registered routes.
func (b *Builder) OpenAPISpec() (openapi.Document, error) {
	ob := openapi.NewDocumentBuilder(b.info)
	for _, s := range b.servers {
		ob.AddServer(s)
	}
	for _, e := range b.entries {
		ob.AddRoute(e.descriptor())
	}
	return ob.Build()
}

// buildDescriptor constructs a frozen route.Route from method, path, schemas,
// and config. Deep-copies all slices to prevent later mutation from affecting
// the registered route.
func buildDescriptor(method, path string, reqSchema, respSchema schema.Schema, config RouteConfig) route.Route {
	status := config.RespStatus
	if status == "" {
		if strings.ToUpper(method) == "POST" {
			status = "201"
		} else {
			status = "200"
		}
	}

	r := route.Route{
		Method:      method,
		Path:        path,
		OperationID: config.OperationID,
		Summary:     config.Summary,
		Description: config.Description,
		Tags:        slices.Clone(config.Tags),
		PathParams:  slices.Clone(config.PathParams),
		QueryParams: slices.Clone(config.QueryParams),
	}

	if isBodyMethod(method) {
		r.RequestBody = &route.Body{
			Required:   true,
			Schema:     reqSchema,
			SchemaName: config.ReqSchemaName,
		}
	}

	respSchemaCopy := respSchema
	primary := route.Response{
		Status:      status,
		Description: config.RespDescription,
		Schema:      &respSchemaCopy,
		SchemaName:  config.RespSchemaName,
	}
	r.Responses = append([]route.Response{primary}, buildExtraResponses(config.Responses)...)

	return r
}

func buildExtraResponses(metas []ResponseMeta) []route.Response {
	out := make([]route.Response, len(metas))
	for i, m := range metas {
		out[i] = route.Response{
			Status:      m.Status,
			Description: m.Description,
			Schema:      m.Schema,
			SchemaName:  m.SchemaName,
		}
	}
	return out
}

// isBodyMethod reports whether the HTTP method conventionally carries a
// request body. Only POST, PUT, and PATCH are treated as body-bearing;
// all others (GET, HEAD, DELETE, OPTIONS) omit RequestBody from the spec.
func isBodyMethod(method string) bool {
	switch strings.ToUpper(method) {
	case "POST", "PUT", "PATCH":
		return true
	}
	return false
}
