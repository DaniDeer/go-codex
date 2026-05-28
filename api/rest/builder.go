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
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/DaniDeer/go-codex/api/internal"
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

	// PathParamCodecs maps path variable names (without braces) to codecs that
	// validate substituted values at runtime via [RouteHandle.BuildPath].
	//
	// Example — require {id} to be a valid UUID:
	//
	//	PathParamCodecs: map[string]codex.Codec[string]{
	//	    "id": codex.String().Refine(validate.UUID),
	//	}
	//
	// A key that does not appear as a {varName} in the route path is a
	// programming error: [AddRoute] returns an error immediately.
	// Nil or empty means no runtime param validation.
	PathParamCodecs map[string]codex.Codec[string]

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

	// paramCodecs holds per-variable codecs registered via PathParamCodecs.
	paramCodecs map[string]codex.Codec[string]

	// pathCodec is the builder-level path codec (may be nil).
	// Used to re-validate the final assembled path in BuildPath.
	pathCodec *codex.Codec[string]
}

// BuildPath substitutes {varName} placeholders in the route's path template
// with the values provided in vars, validating each against its registered
// codec (if any).
//
// All template variables must be present in vars; missing variables return an
// error. Values are validated before substitution; codec failures return a
// [PathParamError] that identifies the variable name and the failing value.
// Keys in vars that do not appear in the template are silently ignored.
//
// If the builder was created with [WithPathCodec] or [WithPathConstraints],
// the final assembled path is also validated against that codec. A failure
// returns an [InvalidPathError] with the concrete path (not the template).
//
// Example:
//
//	path, err := getUserRoute.BuildPath(map[string]string{"id": "f47ac10b-..."})
//	// path = "/users/f47ac10b-..."
func (h *RouteHandle[Req, Resp]) BuildPath(vars map[string]string) (string, error) {
	result, err := internal.BuildFromTemplate(h.Descriptor.Path, vars, h.paramCodecs,
		func(name string) error { return MissingPathVarError{Name: name} },
		func(name, value string, err error) error {
			return PathParamError{Name: name, Value: value, Err: err}
		},
	)
	if err != nil {
		return "", err
	}
	if h.pathCodec != nil {
		if err := h.pathCodec.Validate(result); err != nil {
			return "", InvalidPathError{Path: result, Err: err}
		}
	}
	return result, nil
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

// InvalidPathError is returned by [AddRoute] when the path fails builder-level
// path codec validation.
//
// Use errors.As to extract it and inspect the failing path or the underlying
// constraint error:
//
//	var pathErr rest.InvalidPathError
//	if errors.As(err, &pathErr) {
//	    log.Printf("bad path %q: %v", pathErr.Path, pathErr.Err)
//	}
type InvalidPathError struct {
	Path string // the path that failed validation
	Err  error  // the underlying constraint or codec error
}

func (e InvalidPathError) Error() string {
	return fmt.Sprintf("invalid path %q: %s", e.Path, e.Err.Error())
}

// Unwrap allows errors.As and errors.Is to traverse the underlying constraint error.
func (e InvalidPathError) Unwrap() error { return e.Err }

// PathParamError is returned by [RouteHandle.BuildPath] when a path variable
// value fails codec validation.
//
// Use errors.As to extract the failing variable name and value:
//
//	var paramErr rest.PathParamError
//	if errors.As(err, &paramErr) {
//	    log.Printf("bad value for {%s}: %q — %v", paramErr.Name, paramErr.Value, paramErr.Err)
//	}
type PathParamError struct {
	Name  string // variable name without braces, e.g. "id"
	Value string // the value that failed validation
	Err   error  // the underlying constraint or codec error
}

func (e PathParamError) Error() string {
	return fmt.Sprintf("path variable {%s}: invalid value %q: %s", e.Name, e.Value, e.Err.Error())
}

// Unwrap allows errors.As and errors.Is to traverse the underlying constraint error.
func (e PathParamError) Unwrap() error { return e.Err }

// MissingPathVarError is returned by [RouteHandle.BuildPath] when a {varName}
// placeholder in the path template has no corresponding entry in the vars map.
//
// Use errors.As to extract the missing variable name:
//
//	var missingErr rest.MissingPathVarError
//	if errors.As(err, &missingErr) {
//	    log.Printf("caller forgot to supply path variable {%s}", missingErr.Name)
//	}
type MissingPathVarError struct {
	Name string // the variable name (without braces) that had no value
}

func (e MissingPathVarError) Error() string {
	return fmt.Sprintf("missing value for path variable {%s}", e.Name)
}

// Builder accumulates route registrations and produces OpenAPI specs.
// Create one with [NewBuilder].
type Builder struct {
	info      Info
	servers   []Server
	entries   []routeEntry
	schemas   map[string]schema.Schema
	pathCodec *codex.Codec[string]
}

// BuilderOption configures a [Builder] at construction time.
type BuilderOption func(*Builder)

// WithPathCodec sets a codec used to validate every path passed to [AddRoute].
// If the path is invalid, [AddRoute] returns an error immediately.
//
// Use [WithPathConstraints] for the common case of stacking one or more
// [codex.Constraint] values; use WithPathCodec when you need a fully-custom
// [codex.Codec].
//
// Example — enforce HTTP path rules:
//
//	import "github.com/DaniDeer/go-codex/validate"
//
//	b := rest.NewBuilder(info, rest.WithPathConstraints(validate.HTTPPath))
func WithPathCodec(c codex.Codec[string]) BuilderOption {
	return func(b *Builder) { b.pathCodec = &c }
}

// WithPathConstraints is a convenience wrapper around [WithPathCodec] that
// builds a codec from [codex.String] refined with the given constraints.
// Multiple constraints are applied in order; all must pass.
//
// Users can mix built-in constraints from the validate package with their own:
//
//	sensorPrefix := codex.Constraint[string]{
//	    Name:    "sensor-prefix",
//	    Check:   func(v string) bool { return strings.HasPrefix(v, "/sensors/") },
//	    Message: func(v string) string { return fmt.Sprintf("path must start with /sensors/, got %q", v) },
//	}
//	b := rest.NewBuilder(info, rest.WithPathConstraints(validate.HTTPPath, sensorPrefix))
func WithPathConstraints(cons ...codex.Constraint[string]) BuilderOption {
	c := codex.Refine(codex.String(), cons...)
	return WithPathCodec(c)
}

// NewBuilder returns a Builder initialised with the given API metadata.
func NewBuilder(info Info, opts ...BuilderOption) *Builder {
	b := &Builder{info: info, schemas: make(map[string]schema.Schema)}
	for _, opt := range opts {
		opt(b)
	}
	return b
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

// AddSchema registers a named schema in components/schemas.
// Use this to register reusable schemas (e.g. shared error types) that are
// referenced by SchemaName in route configs but not inlined in any codec.
func (b *Builder) AddSchema(name string, s schema.Schema) *Builder {
	b.schemas[name] = s
	return b
}

// AddRoute registers a route with the builder and returns a [RouteHandle].
//
// reqCodec is used to decode and validate the JSON request body.
// respCodec is used to encode the JSON response.
//
// If the builder was created with [WithPathCodec] or [WithPathConstraints], the
// path is validated immediately. An error is returned if validation fails —
// no route is registered in that case.
//
// If config.PathParamCodecs is non-empty, each key is verified to be a
// {varName} present in the path template. A key that does not appear in the
// path is a programming error and causes AddRoute to return an error.
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
) (*RouteHandle[Req, Resp], error) {
	if b.pathCodec != nil {
		if err := b.pathCodec.Validate(internal.StripTemplateVars(path)); err != nil {
			return nil, InvalidPathError{Path: path, Err: err}
		}
	}

	templateVars := internal.ParseTemplateVars(path)
	for name := range config.PathParamCodecs {
		if !templateVars[name] {
			return nil, fmt.Errorf("api/rest: PathParamCodecs key %q not found in path template %q", name, path)
		}
	}

	frozen := buildDescriptor(method, path, reqCodec.Schema, respCodec.Schema, config)

	entry := &typedRouteEntry[Req, Resp]{frozen: frozen}
	b.entries = append(b.entries, entry)

	jsonReq := format.JSON(reqCodec)
	jsonResp := format.JSON(respCodec)

	return &RouteHandle[Req, Resp]{
		Descriptor:  frozen,
		Decode:      func(body []byte) (Req, error) { return jsonReq.Unmarshal(body) },
		Encode:      func(resp Resp) ([]byte, error) { return jsonResp.Marshal(resp) },
		paramCodecs: config.PathParamCodecs,
		pathCodec:   b.pathCodec,
	}, nil
}

// OpenAPISpec builds a complete OpenAPI 3.1 document from all registered routes.
// Returns an error if any non-empty SchemaName references a schema that will not
// be present in components/schemas (a dangling $ref).
func (b *Builder) OpenAPISpec() (openapi.Document, error) {
	if err := b.checkDanglingRefs(); err != nil {
		return openapi.Document{}, err
	}
	ob := openapi.NewDocumentBuilder(b.info)
	for _, s := range b.servers {
		ob.AddServer(s)
	}
	for name, s := range b.schemas {
		ob.AddSchema(name, s)
	}
	for _, e := range b.entries {
		ob.AddRoute(e.descriptor())
	}
	return ob.Build()
}

// checkDanglingRefs verifies that every non-empty SchemaName used in routes
// resolves to a schema that will be registered in components/schemas.
// A name is resolvable when the accompanying Schema is non-nil, or when the
// name was explicitly registered via [Builder.AddSchema].
func (b *Builder) checkDanglingRefs() error {
	// Build the set of resolvable names.
	resolvable := make(map[string]bool, len(b.schemas))
	for name := range b.schemas {
		resolvable[name] = true
	}
	for _, e := range b.entries {
		r := e.descriptor()
		if r.RequestBody != nil && r.RequestBody.SchemaName != "" {
			resolvable[r.RequestBody.SchemaName] = true
		}
		for _, resp := range r.Responses {
			if resp.SchemaName != "" && resp.Schema != nil {
				resolvable[resp.SchemaName] = true
			}
		}
	}

	// Collect any referenced names that are not resolvable.
	seen := make(map[string]bool)
	var unresolved []string
	for _, e := range b.entries {
		r := e.descriptor()
		for _, resp := range r.Responses {
			if resp.SchemaName != "" && resp.Schema == nil && !resolvable[resp.SchemaName] {
				if !seen[resp.SchemaName] {
					seen[resp.SchemaName] = true
					unresolved = append(unresolved, resp.SchemaName)
				}
			}
		}
	}
	if len(unresolved) > 0 {
		sort.Strings(unresolved)
		return fmt.Errorf("unregistered schema names (dangling $ref): %s", strings.Join(unresolved, ", "))
	}
	return nil
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
