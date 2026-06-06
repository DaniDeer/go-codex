// Package rest provides a transport-agnostic REST API builder for go-codex.
//
// Define routes declaratively with codec-backed request and response types;
// register them with a [Builder] to obtain a [RouteHandle] with typed Decode
// and Encode helpers. Pass those helpers to any HTTP framework (net/http, Gin,
// Chi, Echo) — this package does not import net/http or any framework.
//
// Spec generation is also available: [Builder.OpenAPISpec] derives a complete
// OpenAPI 3.1 document from the registered routes.
//
// Typical usage:
//
//	b := rest.NewBuilder(rest.Info{Title: "User API", Version: "1.0.0"})
//	b.AddServer("production", rest.Server{URL: "https://api.example.com"})
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

import (
	"errors"
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

// PathParam describes a {varName} placeholder in a route path template.
// It combines spec metadata with optional runtime validation via a codec.
//
// PathParam implements [RouteOpt]: pass it directly to [NewRoute] or [NewSSERoute].
//
// Entry names must correspond to {varName} placeholders in the path template;
// unknown names cause [Route.Register] to return an error immediately.
//
// PathParam is optional: the builder auto-generates a minimal parameter entry
// for every {varName} in the path. Only specify PathParam when you need a
// description or runtime validation for a specific variable.
// PathParam describes an HTTP path variable for a route (e.g. `{id}` in `/users/{id}`).
// It combines spec metadata with optional runtime validation via a codec.
//
// Note: path parameters are always required by the OpenAPI specification — there
// is no Required field. For optional key-value parameters use [QueryParam] with
// Required: true or false as appropriate.
//
// PathParam implements [RouteOpt]: pass it directly to [NewRoute] or [NewSSERoute].
type PathParam struct {
	Name        string
	Description string
	// Codec validates substituted values at [RouteHandle.BuildPath] time.
	// When non-nil, the codec's schema is also used in the OpenAPI spec.
	// Nil means no runtime validation; the spec schema will be empty.
	Codec *codex.Codec[string]
}

func (p PathParam) applyRoute(rb *routeBuilder) { rb.pathParams = append(rb.pathParams, p) }

// WithCodec sets the validation codec and returns the updated PathParam.
func (p PathParam) WithCodec(c codex.Codec[string]) PathParam { p.Codec = &c; return p }

// ResponseMeta describes one additional response entry for a route (errors,
// redirects, etc.). The primary success response is derived from the response
// codec and RespStatus/RespDescription/RespSchemaName in RouteMeta.
//
// ResponseMeta implements [RouteOpt]: pass it directly to [NewRoute].
type ResponseMeta struct {
	Status      string // e.g. "400", "404", "default"
	Description string
	Schema      *schema.Schema // nil for description-only responses (e.g. 404)
	SchemaName  string         // non-empty → $ref in spec
}

func (m ResponseMeta) applyRoute(rb *routeBuilder) { rb.extraResps = append(rb.extraResps, m) }

// RouteMeta holds documentation and response metadata for a route registration.
// It controls spec output (OpenAPI operation fields and the primary success
// response). Pass it as a variadic option to [NewRoute] or [NewSSERoute].
//
// RouteMeta implements [RouteOpt].
type RouteMeta struct {
	OperationID string
	Summary     string
	Description string
	Tags        []string

	// ReqSchemaName, when non-empty, emits a $ref for the request body schema
	// in the spec and registers the schema under that name in components/schemas.
	// Has no effect on SSE routes (GET — no request body).
	ReqSchemaName string

	// RespStatus is the HTTP status code for the primary success response.
	// Defaults to "201" for POST, "200" for all other methods.
	RespStatus string

	// RespDescription is the description for the primary success response.
	RespDescription string

	// RespSchemaName, when non-empty, emits a $ref for the response schema.
	RespSchemaName string

	// Security, when non-nil, overrides global security for this operation.
	// Pass an empty slice to declare "no auth required" for this route.
	// nil (default) inherits global security declared via Builder.AddGlobalSecurity.
	Security []route.SecurityRequirement
}

func (m RouteMeta) applyRoute(rb *routeBuilder) { rb.meta = m }

// RouteOpt is the sealed interface for variadic [NewRoute] and [NewSSERoute] options.
//
// The following types implement RouteOpt:
//   - [RouteMeta] — operation metadata (ID, summary, description, schema names, response status)
//   - [PathParam] — path template variable with optional codec and description
//   - [QueryParam] — query parameter with optional codec, description, and required flag
//   - [CookieParam] — cookie parameter with optional codec, description, and required flag
//   - [HeaderParam] — request header with optional codec, description, and required flag
//   - [ResponseHeaderParam] — response header with optional codec for server-side validation
//   - [ResponseCookieParam] — response Set-Cookie with optional codec for server-side validation
//   - [ResponseMeta] — additional response entries (error codes, redirects, etc.)
type RouteOpt interface{ applyRoute(*routeBuilder) }

// routeBuilder accumulates RouteOpt values before building the route descriptor.
type routeBuilder struct {
	meta         RouteMeta
	pathParams   []PathParam
	queryParams  []QueryParam
	cookieParams []CookieParam
	headerParams []HeaderParam
	respHeaders  []ResponseHeaderParam
	respCookies  []ResponseCookieParam
	extraResps   []ResponseMeta
}

// RouteHandle is returned by [Route.Register]. It holds the spec descriptor
// and codec-backed Decode/Encode helpers.
//
// Decode and Encode use JSON encoding. For body-less methods (GET, HEAD,
// DELETE), Decode can still be called if the request carries a body, but
// typical REST usage will not call it.
type RouteHandle[Req, Resp any] struct {
	// Descriptor is the live route.Route descriptor. It is updated in place
	// by [WithRequestFormats] and [WithFormats] so that spec generation
	// always reflects the latest configuration.
	Descriptor route.Route

	// Decode deserialises and validates a JSON request body into Req.
	// All Refine constraints on the request codec run automatically.
	Decode func(body []byte) (Req, error)

	// Encode serialises Resp to JSON bytes.
	Encode func(resp Resp) ([]byte, error)

	// RequestFormats, when non-empty, lists the formats the route accepts for
	// request body decoding. The adapter uses this slice for content negotiation:
	// it picks the format matching the client's Content-Type header and decodes
	// the request body with it. When empty, the adapter falls back to JSON (via
	// Decode) and enforces opts.ContentType.
	RequestFormats []format.Format[Req]

	// Formats, when non-empty, lists the formats the route can produce.
	// The adapter uses this slice for content negotiation: it picks the format
	// matching the client's Accept header and encodes the response with it.
	// When empty, the adapter falls back to JSON (via Encode).
	Formats []format.Format[Resp]

	// pathParams holds per-variable params registered via PathParam options.
	pathParams []PathParam

	// queryParams holds per-parameter entries registered via QueryParam options.
	queryParams []QueryParam

	// cookieParams holds per-parameter entries registered via CookieParam options.
	cookieParams []CookieParam

	// headerParams holds per-parameter entries registered via HeaderParam options.
	headerParams []HeaderParam

	// responseHeaderParams holds per-header entries registered via ResponseHeaderParam options.
	responseHeaderParams []ResponseHeaderParam

	// responseCookieParams holds per-cookie entries registered via ResponseCookieParam options.
	responseCookieParams []ResponseCookieParam

	// pathCodec is the builder-level path codec (may be nil).
	// Used to re-validate the final assembled path in BuildPath.
	pathCodec *codex.Codec[string]

	// SecuritySchemes maps scheme name to SecurityScheme (with runtime Codec).
	// Populated from Builder.AddSecurityScheme when Register is called.
	// Adapters use this map to extract and validate credentials per scheme.
	SecuritySchemes map[string]SecurityScheme

	// GlobalSecurity holds the builder-level security requirements that apply
	// when Descriptor.Security is nil (i.e. the route inherits global security).
	// Adapters resolve the effective requirements as:
	//   reqs := handle.Descriptor.Security
	//   if reqs == nil { reqs = handle.GlobalSecurity }
	// Set via [Builder.AddGlobalSecurity]. nil when no global security is declared.
	GlobalSecurity []route.SecurityRequirement
}

// WithRequestFormats registers the formats the route accepts for request body
// decoding. The adapter performs content negotiation using the client's
// Content-Type header; a mismatch returns HTTP 415 [UnsupportedMediaTypeError].
//
// Calling WithRequestFormats also updates the OpenAPI spec: the request body
// will list all accepted content types.
//
// Example:
//
//	route, err := rest.NewRoute[CreateItemReq, Item]("POST", "/items",
//	    reqCodec, respCodec,
//	    rest.RouteMeta{OperationID: "createItem"}).Register(b)
//	route.WithRequestFormats(
//	    format.JSON(reqCodec),  // Content-Type: application/json
//	    format.YAML(reqCodec),  // Content-Type: application/yaml
//	)
func (h *RouteHandle[Req, Resp]) WithRequestFormats(fmts ...format.Format[Req]) *RouteHandle[Req, Resp] {
	h.RequestFormats = slices.Clone(fmts)
	if h.Descriptor.RequestBody != nil {
		var cts []string
		for _, f := range fmts {
			if ct := f.ContentType(); ct != "" {
				cts = append(cts, ct)
			}
		}
		h.Descriptor.RequestBody.ContentTypes = cts
	}
	return h
}

// WithFormats registers the formats the route can produce for content
// negotiation. The adapter picks the format matching the client's Accept header;
// a mismatch returns HTTP 406 [NotAcceptableError].
//
// When empty, the adapter defaults to JSON (via Encode).
// The first format is used when the client sends Accept: */*.
//
// Calling WithFormats also updates the OpenAPI spec: the primary
// response will list all registered content types.
//
// Example:
//
//	route.WithFormats(
//	    adapttempl.Format(propsCodec, pageComponent),  // Accept: text/html
//	    format.JSON(respCodec),                         // Accept: application/json
//	    format.YAML(respCodec),                         // Accept: application/yaml
//	)
func (h *RouteHandle[Req, Resp]) WithFormats(fmts ...format.Format[Resp]) *RouteHandle[Req, Resp] {
	h.Formats = slices.Clone(fmts)
	if len(h.Descriptor.Responses) > 0 {
		var cts []string
		for _, f := range fmts {
			if ct := f.ContentType(); ct != "" {
				cts = append(cts, ct)
			}
		}
		h.Descriptor.Responses[0].ContentTypes = cts
	}
	return h
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
	// Build codec lookup map from pathParams.
	codecMap := make(map[string]*codex.Codec[string], len(h.pathParams))
	for i := range h.pathParams {
		if h.pathParams[i].Codec != nil {
			codecMap[h.pathParams[i].Name] = h.pathParams[i].Codec
		}
	}
	result, err := internal.BuildFromTemplate(h.Descriptor.Path, vars, codecMap,
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

// ErrRequiredParam is the sentinel wrapped inside a param error when a required
// parameter is absent from the request. Check with [errors.Is]:
//
//	if errors.Is(err, rest.ErrRequiredParam) {
//	    http.Error(w, "missing required parameter", http.StatusBadRequest)
//	}
var ErrRequiredParam = errors.New("required parameter missing")

// ValidatePathParams validates path variable values against their registered
// codecs. For each [PathParam] that has a non-nil Codec, the corresponding
// value in vars is validated. Returns a [PathParamError] on the first failure.
// Extra keys in vars are silently ignored.
//
// Adapters build the vars map using [RouteHandle.PathParamNames] and the
// path-variable extraction provided by their router (e.g. r.PathValue).
func (h *RouteHandle[Req, Resp]) ValidatePathParams(vars map[string]string) error {
	for i := range h.pathParams {
		pp := &h.pathParams[i]
		if pp.Codec == nil {
			continue
		}
		value, ok := vars[pp.Name]
		if !ok {
			continue
		}
		if err := pp.Codec.Validate(value); err != nil {
			return PathParamError{Name: pp.Name, Value: value, Err: err}
		}
	}
	return nil
}

// PathParamNames returns the names of all registered path parameters.
// Adapters use this to build the map required by [RouteHandle.ValidatePathParams].
func (h *RouteHandle[Req, Resp]) PathParamNames() []string {
	names := make([]string, len(h.pathParams))
	for i := range h.pathParams {
		names[i] = h.pathParams[i].Name
	}
	return names
}

// ValidateQuery validates query parameter values against their registered codecs.
//
// For each [QueryParam] that has a non-nil Codec, the corresponding value in
// params is validated. Returns a [QueryParamError] on the first failure.
// When [QueryParam.Required] is true and the key is absent, ValidateQuery
// returns a [QueryParamError] wrapping [ErrRequiredParam]. Extra keys are ignored.
//
// Example:
//
//	errs := listRoute.ValidateQuery(map[string]string{
//	    "page": r.URL.Query().Get("page"),
//	    "limit": r.URL.Query().Get("limit"),
//	})
func (h *RouteHandle[Req, Resp]) ValidateQuery(params map[string]string) error {
	for i := range h.queryParams {
		qp := &h.queryParams[i]
		if qp.Codec == nil {
			continue
		}
		value, ok := params[qp.Name]
		if !ok {
			if qp.Required {
				return QueryParamError{Name: qp.Name, Err: ErrRequiredParam}
			}
			continue
		}
		if err := qp.Codec.Validate(value); err != nil {
			return QueryParamError{Name: qp.Name, Value: value, Err: err}
		}
	}
	return nil
}

// ValidateQueryMulti validates query parameter values against their registered codecs
// using a multi-value map (such as [url.Values] returned by r.URL.Query()).
//
// For each [QueryParam] that has a non-nil Codec, the first value in the slice
// for that key is validated. This mirrors the behaviour of [ValidateQuery] but
// accepts the raw map[string][]string directly — useful when handling repeated
// query keys such as "?tags=a&tags=b".
//
// Returns a [QueryParamError] on the first failure. When [QueryParam.Required] is
// true and the key is absent or empty, ValidateQueryMulti returns a [QueryParamError]
// wrapping [ErrRequiredParam]. Extra keys are silently ignored.
func (h *RouteHandle[Req, Resp]) ValidateQueryMulti(params map[string][]string) error {
	for i := range h.queryParams {
		qp := &h.queryParams[i]
		if qp.Codec == nil {
			continue
		}
		values, ok := params[qp.Name]
		if !ok || len(values) == 0 {
			if qp.Required {
				return QueryParamError{Name: qp.Name, Err: ErrRequiredParam}
			}
			continue
		}
		value := values[0]
		if err := qp.Codec.Validate(value); err != nil {
			return QueryParamError{Name: qp.Name, Value: value, Err: err}
		}
	}
	return nil
}

// ValidateCookies validates cookie parameter values against their registered codecs.
//
// For each [CookieParam] that has a non-nil Codec, the corresponding value in
// params is validated. Returns a [CookieParamError] on the first failure.
// When [CookieParam.Required] is true and the key is absent, ValidateCookies
// returns a [CookieParamError] wrapping [ErrRequiredParam]. Extra keys are ignored.
//
// Example:
//
//	cookies := map[string]string{"session_token": r.Cookie("session_token").Value}
//	if err := myRoute.ValidateCookies(cookies); err != nil {
//	    http.Error(w, err.Error(), http.StatusBadRequest)
//	}
func (h *RouteHandle[Req, Resp]) ValidateCookies(params map[string]string) error {
	for i := range h.cookieParams {
		cp := &h.cookieParams[i]
		if cp.Codec == nil {
			continue
		}
		value, ok := params[cp.Name]
		if !ok {
			if cp.Required {
				return CookieParamError{Name: cp.Name, Err: ErrRequiredParam}
			}
			continue
		}
		if err := cp.Codec.Validate(value); err != nil {
			return CookieParamError{Name: cp.Name, Value: value, Err: err}
		}
	}
	return nil
}

// ValidateHeaders validates HTTP header values against their registered codecs.
//
// For each [HeaderParam] that has a non-nil Codec, the corresponding value in
// params is validated. Returns a [HeaderParamError] on the first failure.
// When [HeaderParam.Required] is true and the key is absent, ValidateHeaders
// returns a [HeaderParamError] wrapping [ErrRequiredParam]. Extra keys are ignored.
//
// Example:
//
//	headers := map[string]string{"X-Request-ID": r.Header.Get("X-Request-ID")}
//	if err := myRoute.ValidateHeaders(headers); err != nil {
//	    http.Error(w, err.Error(), http.StatusBadRequest)
//	}
func (h *RouteHandle[Req, Resp]) ValidateHeaders(params map[string]string) error {
	for i := range h.headerParams {
		hp := &h.headerParams[i]
		if hp.Codec == nil {
			continue
		}
		value, ok := params[hp.Name]
		if !ok {
			if hp.Required {
				return HeaderParamError{Name: hp.Name, Err: ErrRequiredParam}
			}
			continue
		}
		if err := hp.Codec.Validate(value); err != nil {
			return HeaderParamError{Name: hp.Name, Value: value, Err: err}
		}
	}
	return nil
}

// ValidateResponseHeaders validates HTTP response header values against their registered codecs.
//
// For each [ResponseHeaderParam] that has a non-nil Codec, the corresponding value
// in headers is validated. Returns a [ResponseHeaderParamError] on the first failure.
// When [ResponseHeaderParam.Required] is true and the key is absent, ValidateResponseHeaders
// returns a [ResponseHeaderParamError] wrapping [ErrRequiredParam].
//
// The net/http adapter calls this automatically after the handler returns and
// before writing the response. A failure indicates a server-side contract
// violation and results in a 500 response.
func (rh *RouteHandle[Req, Resp]) ValidateResponseHeaders(headers map[string]string) error {
	for i := range rh.responseHeaderParams {
		rp := &rh.responseHeaderParams[i]
		if rp.Codec == nil {
			continue
		}
		value, ok := headers[rp.Name]
		if !ok {
			if rp.Required {
				return ResponseHeaderParamError{Name: rp.Name, Err: ErrRequiredParam}
			}
			continue
		}
		if err := rp.Codec.Validate(value); err != nil {
			return ResponseHeaderParamError{Name: rp.Name, Value: value, Err: err}
		}
	}
	return nil
}

// ValidateResponseCookies validates the cookie values collected by the handler
// against their registered [ResponseCookieParam] codecs. Returns a [ResponseCookieParamError]
// on the first failure. When [ResponseCookieParam.Required] is true and the key is absent,
// ValidateResponseCookies returns a [ResponseCookieParamError] wrapping [ErrRequiredParam].
//
// The net/http adapter calls this automatically after the handler returns and
// before writing the response. A failure indicates a server-side contract
// violation and results in a 500 response.
func (rh *RouteHandle[Req, Resp]) ValidateResponseCookies(cookies map[string]string) error {
	for i := range rh.responseCookieParams {
		cp := &rh.responseCookieParams[i]
		if cp.Codec == nil {
			continue
		}
		value, ok := cookies[cp.Name]
		if !ok {
			if cp.Required {
				return ResponseCookieParamError{Name: cp.Name, Err: ErrRequiredParam}
			}
			continue
		}
		if err := cp.Codec.Validate(value); err != nil {
			return ResponseCookieParamError{Name: cp.Name, Value: value, Err: err}
		}
	}
	return nil
}

type routeEntry interface {
	descriptor() route.Route
}

// typedRouteEntry stores a pointer to the RouteHandle so that With* mutations
// are visible to the builder at OpenAPISpec() time.
type typedRouteEntry[Req, Resp any] struct {
	handle *RouteHandle[Req, Resp]
}

func (e *typedRouteEntry[Req, Resp]) descriptor() route.Route { return e.handle.Descriptor }

// typedSSEEntry stores a pointer to the SSERouteHandle so that With* mutations
// are visible to the builder at OpenAPISpec() time.
type typedSSEEntry[Req, Event any] struct {
	handle *SSERouteHandle[Req, Event]
}

func (e *typedSSEEntry[Req, Event]) descriptor() route.Route { return e.handle.Descriptor }

// InvalidPathError is returned by [Route.Register] when the path fails builder-level
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

// InvalidPathParamError is returned by [Route.Register] when a [PathParam] entry
// names a variable that does not appear in the path template.
//
// Use errors.As to extract the offending name and the path template:
//
//	var paramErr rest.InvalidPathParamError
//	if errors.As(err, &paramErr) {
//	    log.Printf("PathParam %q not in path %q", paramErr.Name, paramErr.Path)
//	}
type InvalidPathParamError struct {
	Name string // the variable name (without braces) that is not in the template
	Path string // the path template that was validated against
}

func (e InvalidPathParamError) Error() string {
	return fmt.Sprintf("api/rest: PathParams entry %q not found in path template %q", e.Name, e.Path)
}

// QueryParam describes a query parameter for a route.
// It combines spec metadata with optional runtime validation via a codec.
//
// QueryParam implements [RouteOpt]: pass it directly to [NewRoute].
type QueryParam struct {
	Name        string
	Description string
	Required    bool
	// Codec validates query parameter values at [RouteHandle.ValidateQuery] time.
	// When non-nil, the codec's schema is also used in the OpenAPI spec.
	// Nil means no runtime validation; the spec schema will be empty.
	Codec *codex.Codec[string]
}

func (q QueryParam) applyRoute(rb *routeBuilder) { rb.queryParams = append(rb.queryParams, q) }

// WithCodec sets the validation codec and returns the updated QueryParam.
func (q QueryParam) WithCodec(c codex.Codec[string]) QueryParam { q.Codec = &c; return q }

// QueryParamError is returned by [RouteHandle.ValidateQuery] when a query
// parameter value fails codec validation.
//
// Use errors.As to extract the failing parameter name and value:
//
//	var paramErr rest.QueryParamError
//	if errors.As(err, &paramErr) {
//	    log.Printf("bad value for query param %q: %q — %v", paramErr.Name, paramErr.Value, paramErr.Err)
//	}
type QueryParamError struct {
	Name  string // query parameter name
	Value string // the value that failed validation
	Err   error  // the underlying constraint or codec error
}

func (e QueryParamError) Error() string {
	return fmt.Sprintf("query parameter %q: invalid value %q: %s", e.Name, e.Value, e.Err.Error())
}

// Unwrap allows errors.As and errors.Is to traverse the underlying constraint error.
func (e QueryParamError) Unwrap() error { return e.Err }

// CookieParam describes an HTTP cookie parameter for a route.
// It combines spec metadata with optional runtime validation via a codec.
//
// CookieParam implements [RouteOpt]: pass it directly to [NewRoute].
type CookieParam struct {
	Name        string
	Description string
	Required    bool
	// Codec validates cookie parameter values at [RouteHandle.ValidateCookies] time.
	// When non-nil, the codec's schema is also used in the OpenAPI spec.
	// Nil means no runtime validation; the spec schema will be empty.
	Codec *codex.Codec[string]
}

func (c CookieParam) applyRoute(rb *routeBuilder) { rb.cookieParams = append(rb.cookieParams, c) }

// WithCodec sets the validation codec and returns the updated CookieParam.
func (c CookieParam) WithCodec(cc codex.Codec[string]) CookieParam { c.Codec = &cc; return c }

// CookieParamError is returned by [RouteHandle.ValidateCookies] when a cookie
// parameter value fails codec validation.
//
// Use errors.As to extract the failing parameter name and value:
//
//	var cookieErr rest.CookieParamError
//	if errors.As(err, &cookieErr) {
//	    log.Printf("bad value for cookie %q: %q — %v", cookieErr.Name, cookieErr.Value, cookieErr.Err)
//	}
type CookieParamError struct {
	Name  string // cookie parameter name
	Value string // the value that failed validation
	Err   error  // the underlying constraint or codec error
}

func (e CookieParamError) Error() string {
	return fmt.Sprintf("cookie parameter %q: invalid value %q: %s", e.Name, e.Value, e.Err.Error())
}

// Unwrap allows errors.As and errors.Is to traverse the underlying constraint error.
func (e CookieParamError) Unwrap() error { return e.Err }

// HeaderParam describes an HTTP header parameter for a route.
// It combines spec metadata with optional runtime validation via a codec.
//
// Note: OpenAPI reserves Accept, Content-Type, and Authorization — do not
// declare those as HeaderParams; they belong to request body and security scheme
// definitions respectively.
//
// HeaderParam implements [RouteOpt]: pass it directly to [NewRoute].
type HeaderParam struct {
	Name        string
	Description string
	Required    bool
	// Codec validates request header parameter values at [RouteHandle.ValidateHeaders] time.
	// When non-nil, the codec's schema is also used in the OpenAPI spec.
	// Nil means no runtime validation; the spec schema will be empty.
	Codec *codex.Codec[string]
}

func (h HeaderParam) applyRoute(rb *routeBuilder) { rb.headerParams = append(rb.headerParams, h) }

// WithCodec sets the validation codec and returns the updated HeaderParam.
func (h HeaderParam) WithCodec(c codex.Codec[string]) HeaderParam { h.Codec = &c; return h }

// HeaderParamError is returned by [RouteHandle.ValidateHeaders] when a header
// value fails codec validation.
//
// Use errors.As to extract the failing header name and value:
//
//	var headerErr rest.HeaderParamError
//	if errors.As(err, &headerErr) {
//	    log.Printf("bad value for header %q: %q — %v", headerErr.Name, headerErr.Value, headerErr.Err)
//	}
type HeaderParamError struct {
	Name  string // header name
	Value string // the value that failed validation
	Err   error  // the underlying constraint or codec error
}

func (e HeaderParamError) Error() string {
	return fmt.Sprintf("header %q: invalid value %q: %s", e.Name, e.Value, e.Err.Error())
}

// Unwrap allows errors.As and errors.Is to traverse the underlying constraint error.
func (e HeaderParamError) Unwrap() error { return e.Err }

// ResponseHeaderParam describes an HTTP header returned in the primary success
// response. It combines spec metadata with optional runtime validation via a codec.
//
// The adapter validates response headers after the handler returns and before
// writing the response. A codec violation results in a 500 (server contract
// violation). The codec schema flows into the OpenAPI response header spec automatically.
//
// ResponseHeaderParam implements [RouteOpt]: pass it directly to [NewRoute].
type ResponseHeaderParam struct {
	Name        string
	Description string
	Required    bool
	// Codec validates response header parameter values at [RouteHandle.ValidateResponseHeaders] time.
	// When non-nil, the codec's schema is also used in the OpenAPI spec.
	// Nil means no runtime validation; the spec schema will be empty.
	Codec *codex.Codec[string]
}

func (p ResponseHeaderParam) applyRoute(rb *routeBuilder) {
	rb.respHeaders = append(rb.respHeaders, p)
}

// WithCodec sets the validation codec and returns the updated ResponseHeaderParam.
func (p ResponseHeaderParam) WithCodec(c codex.Codec[string]) ResponseHeaderParam {
	p.Codec = &c
	return p
}

// ResponseHeaderParamError is returned by [RouteHandle.ValidateResponseHeaders] when
// a response header value fails codec validation.
//
// Use errors.As to extract the failing header name and value:
//
//	var rhErr rest.ResponseHeaderParamError
//	if errors.As(err, &rhErr) {
//	    log.Printf("bad response header %q: %q — %v", rhErr.Name, rhErr.Value, rhErr.Err)
//	}
type ResponseHeaderParamError struct {
	Name  string // header name
	Value string // the value that failed validation
	Err   error  // the underlying constraint or codec error
}

func (e ResponseHeaderParamError) Error() string {
	return fmt.Sprintf("response header %q: invalid value %q: %s", e.Name, e.Value, e.Err.Error())
}

// Unwrap allows errors.As and errors.Is to traverse the underlying constraint error.
func (e ResponseHeaderParamError) Unwrap() error { return e.Err }

// ResponseCookieParam describes a Set-Cookie header returned in the primary
// success response. It combines spec metadata with optional runtime validation
// via a codec that validates the cookie value string.
//
// The adapter validates response cookie values after the handler returns and
// before writing the response. A codec violation results in a 500 (server
// contract violation). The codec schema flows into the OpenAPI response header
// spec as a "Set-Cookie" string header (OpenAPI 3.1 has no first-class
// response cookie object).
//
// ResponseCookieParam implements [RouteOpt]: pass it directly to [NewRoute].
type ResponseCookieParam struct {
	Name        string
	Description string
	Required    bool
	// Codec validates response cookie parameter values at [RouteHandle.ValidateResponseCookies] time.
	// When non-nil, the codec's schema is also used in the OpenAPI spec.
	// Nil means no runtime validation; the spec schema will be empty.
	Codec *codex.Codec[string]
}

func (p ResponseCookieParam) applyRoute(rb *routeBuilder) {
	rb.respCookies = append(rb.respCookies, p)
}

// WithCodec sets the validation codec and returns the updated ResponseCookieParam.
func (p ResponseCookieParam) WithCodec(c codex.Codec[string]) ResponseCookieParam {
	p.Codec = &c
	return p
}

// ResponseCookieParamError is returned by [RouteHandle.ValidateResponseCookies]
// when a response cookie value fails codec validation.
//
// Use errors.As to extract the failing cookie name and value:
//
//	var rcErr rest.ResponseCookieParamError
//	if errors.As(err, &rcErr) {
//	    log.Printf("bad response cookie %q: %q — %v", rcErr.Name, rcErr.Value, rcErr.Err)
//	}
type ResponseCookieParamError struct {
	Name  string // cookie name
	Value string // the value that failed validation
	Err   error  // the underlying constraint or codec error
}

func (e ResponseCookieParamError) Error() string {
	return fmt.Sprintf("response cookie %q: invalid value %q: %s", e.Name, e.Value, e.Err.Error())
}

// Unwrap allows errors.As and errors.Is to traverse the underlying constraint error.
func (e ResponseCookieParamError) Unwrap() error { return e.Err }

// SecurityScheme combines [route.SecurityScheme] spec metadata with optional
// runtime credential extraction and format validation.
//
// AddSecurityScheme registers a SecurityScheme with the builder. The spec fields
// flow into the OpenAPI document; Codec, when non-nil, is used by adapters to
// validate the raw credential string before SecurityFunc is called.
//
// The adapter extracts the raw credential from the request based on the scheme
// Type and location fields:
//   - http bearer / openIdConnect / oauth2: strips "Bearer " from the Authorization header
//   - http basic: strips "Basic " from the Authorization header
//   - apiKey: reads from the header / query / cookie named Name according to In
//
// A codec validation failure causes the adapter to return a [SecurityCredentialError]
// with HTTP 401, without invoking SecurityFunc.
type SecurityScheme struct {
	route.SecurityScheme
	// Codec, when non-nil, validates the extracted raw credential string.
	// Nil means no format validation; SecurityFunc receives the request as-is.
	//
	// Use [SecurityScheme.WithCodec] to set this field inline without a temporary
	// variable: rest.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")}.WithCodec(c)
	Codec *codex.Codec[string]
}

// WithCodec returns a copy of s with Codec set to c. It avoids the
// temporary-variable + address-of pattern required when setting Codec inline:
//
//	b.AddSecurityScheme("bearer", rest.SecurityScheme{
//	    SecurityScheme: route.BearerScheme("JWT"),
//	}.WithCodec(codex.String().Refine(validate.BearerToken)))
func (s SecurityScheme) WithCodec(c codex.Codec[string]) SecurityScheme {
	s.Codec = &c
	return s
}

// SecurityCredentialError is returned when credential format validation via
// SecurityScheme.Codec fails. It is distinct from [SecurityError], which wraps
// rejections from SecurityFunc.
//
// Use [errors.As] to extract the scheme name and underlying constraint error:
//
//	var credErr rest.SecurityCredentialError
//	if errors.As(err, &credErr) {
//	    log.Printf("security scheme %q: invalid credential: %v", credErr.Scheme, credErr.Err)
//	}
type SecurityCredentialError struct {
	Scheme string // security scheme name
	Err    error  // codec constraint error
}

func (e SecurityCredentialError) Error() string {
	return fmt.Sprintf("security scheme %q: invalid credential: %s", e.Scheme, e.Err)
}

// Unwrap allows errors.As and errors.Is to traverse the underlying constraint error.
func (e SecurityCredentialError) Unwrap() error { return e.Err }

// SecurityError is returned when SecurityFunc rejects a request.
// It is distinct from [SecurityCredentialError], which covers codec format failures.
//
// Use [errors.As] to extract the underlying error from SecurityFunc:
//
//	var secErr rest.SecurityError
//	if errors.As(err, &secErr) {
//	    log.Printf("security check failed: %v", secErr.Err)
//	}
type SecurityError struct {
	Err error
}

func (e SecurityError) Error() string {
	return fmt.Sprintf("security check failed: %s", e.Err)
}

// Unwrap allows errors.As and errors.Is to traverse the underlying SecurityFunc error.
func (e SecurityError) Unwrap() error { return e.Err }

// UnsupportedMediaTypeError is returned by the net/http adapter when the
// request Content-Type does not match any accepted media type.
// Use [errors.As] to inspect the Got and Supported fields.
//
//	var ctErr rest.UnsupportedMediaTypeError
//	if errors.As(err, &ctErr) {
//	    log.Printf("got %q, supported: %v", ctErr.Got, ctErr.Supported)
//	}
type UnsupportedMediaTypeError struct {
	// Got is the actual Content-Type value sent by the client (without parameters).
	Got string
	// Supported lists the media types the adapter accepts. Contains one entry
	// for routes with a fixed content type; multiple entries when
	// [RouteHandle.WithRequestFormats] is used.
	Supported []string
}

func (e UnsupportedMediaTypeError) Error() string {
	return fmt.Sprintf("unsupported media type %q: supported: %s", e.Got, strings.Join(e.Supported, ", "))
}

// BodyTooLargeError is returned by the net/http adapter when the request body
// exceeds [Options.MaxBodyBytes]. Use [errors.As] to inspect the Limit field.
//
//	var sizeErr rest.BodyTooLargeError
//	if errors.As(err, &sizeErr) {
//	    log.Printf("body exceeded %d byte limit", sizeErr.Limit)
//	}
type BodyTooLargeError struct {
	// Limit is the maximum allowed body size in bytes (from Options.MaxBodyBytes,
	// or the default 1 MiB when not configured).
	Limit int64
}

func (e BodyTooLargeError) Error() string {
	return fmt.Sprintf("request body exceeds limit of %d bytes", e.Limit)
}

// NotAcceptableError is returned by the net/http adapter when the client's
// Accept header does not match any of the route's registered response formats.
// Use [errors.As] to inspect the Accept and Supported fields.
//
//	var naErr rest.NotAcceptableError
//	if errors.As(err, &naErr) {
//	    log.Printf("client wants %q; route supports %v", naErr.Accept, naErr.Supported)
//	}
type NotAcceptableError struct {
	// Accept is the value of the client's Accept header.
	Accept string
	// Supported lists the content types the route can produce.
	Supported []string
}

func (e NotAcceptableError) Error() string {
	return fmt.Sprintf("not acceptable: Accept %q; supported: %s", e.Accept, strings.Join(e.Supported, ", "))
}

// Builder accumulates route registrations and security schemes, and produces
// OpenAPI 3.1 specifications. It is safe to register routes from multiple
// goroutines as long as [Builder.Build] is not called concurrently.
// Create one with [NewBuilder].
type Builder struct {
	info            Info
	servers         []Server
	entries         []routeEntry
	schemas         map[string]schema.Schema
	pathCodec       *codex.Codec[string]
	securitySchemes map[string]SecurityScheme
	globalSecurity  []route.SecurityRequirement
}

// BuilderOption configures a [Builder] at construction time.
type BuilderOption func(*Builder)

// WithPathCodec sets a codec used to validate every path passed to [Route.Register].
// If the path is invalid, [Route.Register] returns an error immediately.
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
	c := codex.String().Refine(cons...)
	return WithPathCodec(c)
}

// NewBuilder returns a Builder initialised with the given API metadata.
func NewBuilder(info Info, opts ...BuilderOption) *Builder {
	b := &Builder{
		info:            info,
		schemas:         make(map[string]schema.Schema),
		securitySchemes: make(map[string]SecurityScheme),
	}
	for _, opt := range opts {
		opt(b)
	}
	return b
}

// AddServer appends a server entry to the OpenAPI spec in registration order.
// If s.Description is empty, name is used as the description (consistent with
// [events.Builder.AddServer]). Unlike the AsyncAPI builder, OpenAPI servers are
// an ordered array with no named keys — name is not stored beyond this point.
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

// AddSecurityScheme registers a named security scheme with the builder.
// The spec fields flow into the OpenAPI document via OpenAPISpec; Codec, when
// non-nil, is used by adapters to validate extracted credentials before
// SecurityFunc is called.
//
// The name must match those used in route.Require calls and AddGlobalSecurity.
func (b *Builder) AddSecurityScheme(name string, s SecurityScheme) *Builder {
	b.securitySchemes[name] = s
	return b
}

// AddGlobalSecurity appends security requirements that apply to all operations
// by default. The requirements flow into both:
//   - The OpenAPI spec (top-level security field).
//   - Runtime enforcement: routes with nil RouteMeta.Security inherit these
//     requirements at the adapter layer via [RouteHandle.GlobalSecurity].
//
// To mark a specific route as explicitly unsecured (exempt from global security),
// set Security to an empty slice in [RouteMeta]: Security: []route.SecurityRequirement{}.
func (b *Builder) AddGlobalSecurity(reqs ...route.SecurityRequirement) *Builder {
	b.globalSecurity = append(b.globalSecurity, reqs...)
	return b
}

// Route is a declarative HTTP route spec: method, path, codecs, and options.
// It is a value type — define it once, store it, pass it around, and register
// it with one or more [Builder] instances via [Route.Register].
//
// Create a Route with [NewRoute].
type Route[Req, Resp any] struct {
	method    string
	path      string
	reqCodec  codex.Codec[Req]
	respCodec codex.Codec[Resp]
	opts      []RouteOpt
}

// NewRoute creates a [Route] spec from method, path, codecs, and variadic opts.
// NewRoute is infallible — it only captures the spec. Validation (path codec,
// PathParam template consistency) runs at [Route.Register] time.
//
// Pass any combination of [RouteMeta], [PathParam], [QueryParam], [CookieParam],
// [HeaderParam], [ResponseHeaderParam], [ResponseCookieParam], and [ResponseMeta]
// as opts. All opts are optional.
//
// NewRoute is a free function (not a method) because Go requires type parameters
// to appear on free functions, not on method receivers.
//
// Typical usage:
//
//	var createUser = rest.NewRoute[CreateUserReq, User]("POST", "/users",
//	    createUserCodec, userCodec,
//	    rest.RouteMeta{OperationID: "createUser", Summary: "Create a user"},
//	)
//
//	// Later, register with a builder:
//	handle, err := createUser.Register(b)
func NewRoute[Req, Resp any](
	method, path string,
	reqCodec codex.Codec[Req],
	respCodec codex.Codec[Resp],
	opts ...RouteOpt,
) Route[Req, Resp] {
	return Route[Req, Resp]{
		method:    method,
		path:      path,
		reqCodec:  reqCodec,
		respCodec: respCodec,
		opts:      opts,
	}
}

// Register registers the route with b and returns a [RouteHandle].
//
// If the builder was created with [WithPathCodec] or [WithPathConstraints], the
// path is validated immediately and an error is returned if it fails — no route
// is registered in that case.
//
// Any [PathParam] entry whose name does not appear as a {varName} placeholder
// in the path template causes Register to return an error immediately.
//
// Use [RouteHandle.WithRequestFormats] and [RouteHandle.WithFormats]
// after Register to configure multi-format request/response handling.
func (r Route[Req, Resp]) Register(b *Builder) (*RouteHandle[Req, Resp], error) {
	if b.pathCodec != nil {
		if err := b.pathCodec.Validate(internal.StripTemplateVars(r.path)); err != nil {
			return nil, InvalidPathError{Path: r.path, Err: err}
		}
	}

	var rb routeBuilder
	for _, opt := range r.opts {
		opt.applyRoute(&rb)
	}

	templateVars := internal.ParseTemplateVars(r.path)
	for _, p := range rb.pathParams {
		if !templateVars[p.Name] {
			return nil, InvalidPathParamError{Name: p.Name, Path: r.path}
		}
	}

	frozen := buildDescriptor(r.method, r.path, r.reqCodec.Schema, r.respCodec.Schema, rb, nil)

	jsonReq := format.JSON(r.reqCodec)
	jsonResp := format.JSON(r.respCodec)

	schemes := make(map[string]SecurityScheme, len(b.securitySchemes))
	for k, v := range b.securitySchemes {
		schemes[k] = v
	}
	h := &RouteHandle[Req, Resp]{
		Descriptor:           frozen,
		Decode:               func(body []byte) (Req, error) { return jsonReq.Unmarshal(body) },
		Encode:               func(resp Resp) ([]byte, error) { return jsonResp.Marshal(resp) },
		pathParams:           rb.pathParams,
		queryParams:          rb.queryParams,
		cookieParams:         rb.cookieParams,
		headerParams:         rb.headerParams,
		responseHeaderParams: rb.respHeaders,
		responseCookieParams: rb.respCookies,
		pathCodec:            b.pathCodec,
		SecuritySchemes:      schemes,
		GlobalSecurity:       slices.Clone(b.globalSecurity),
	}
	entry := &typedRouteEntry[Req, Resp]{handle: h}
	b.entries = append(b.entries, entry)
	return h, nil
}

// SSERouteHandle is returned by [SSERoute.Register]. It holds the route descriptor
// and typed helpers for decoding requests and encoding SSE events.
//
// The adapter uses EncodeEvent to serialise each event to JSON and ValidateEvent
// to reject invalid values before they are written to the client.
// When Formats is non-empty the adapter may use an explicit format for
// event data serialisation (e.g. JSON or YAML inside the data field).
type SSERouteHandle[Req, Event any] struct {
	// Descriptor is the live route.Route descriptor.
	Descriptor route.Route

	// Decode deserialises and validates a JSON request body into Req.
	// For SSE (GET) routes, this is rarely called — read path and query
	// parameter values from your HTTP framework's request context instead.
	Decode func(body []byte) (Req, error)

	// EncodeEvent serialises one event value to JSON bytes.
	// Used as the fallback encoder when Formats is empty.
	EncodeEvent func(e Event) ([]byte, error)

	// ValidateEvent runs the event codec constraints on e without serialising.
	// The adapter calls this inside the send func before encoding.
	ValidateEvent func(e Event) error

	// Formats, when non-empty, lists the formats available for encoding
	// event data. The adapter picks the first format (or the JSON fallback).
	Formats []format.Format[Event]

	// pathParams holds per-variable params registered via PathParam options.
	pathParams []PathParam

	// queryParams holds per-parameter entries registered via QueryParam options.
	queryParams []QueryParam

	// cookieParams holds per-parameter entries registered via CookieParam options.
	cookieParams []CookieParam

	// headerParams holds per-parameter entries registered via HeaderParam options.
	headerParams []HeaderParam

	// pathCodec is the builder-level path codec (may be nil).
	pathCodec *codex.Codec[string]

	// SecuritySchemes maps scheme name to SecurityScheme (with runtime Codec).
	// Populated from Builder.AddSecurityScheme when Register is called.
	// Adapters use this map to extract and validate credentials per scheme.
	SecuritySchemes map[string]SecurityScheme

	// GlobalSecurity holds the builder-level security requirements that apply
	// when Descriptor.Security is nil (i.e. the route inherits global security).
	// Adapters resolve the effective requirements as:
	//   reqs := handle.Descriptor.Security
	//   if reqs == nil { reqs = handle.GlobalSecurity }
	// Set via [Builder.AddGlobalSecurity]. nil when no global security is declared.
	GlobalSecurity []route.SecurityRequirement

	// responseHeaderParams holds per-header entries registered via ResponseHeaderParam options.
	responseHeaderParams []ResponseHeaderParam

	// responseCookieParams holds per-cookie entries registered via ResponseCookieParam options.
	responseCookieParams []ResponseCookieParam
}

// BuildPath substitutes {varName} placeholders in the route's path template
// with the values provided in vars, validating each against its registered
// codec (if any). Follows the same contract as [RouteHandle.BuildPath].
func (h *SSERouteHandle[Req, Event]) BuildPath(vars map[string]string) (string, error) {
	codecMap := make(map[string]*codex.Codec[string], len(h.pathParams))
	for i := range h.pathParams {
		if h.pathParams[i].Codec != nil {
			codecMap[h.pathParams[i].Name] = h.pathParams[i].Codec
		}
	}
	result, err := internal.BuildFromTemplate(h.Descriptor.Path, vars, codecMap,
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

// WithFormats registers the formats available for encoding SSE event data.
// The adapter uses the first format; when empty, events are encoded as JSON.
//
// This mirrors [RouteHandle.WithFormats] for SSE routes and [ChannelHandle.WithFormats]
// for event channels. Call it after [NewSSERoute] to configure non-JSON event serialisation:
//
//	notifRoute = notifRoute.WithFormats(
//	    adapttempl.Format(notifCodec, notifFragment), // HTML fragments over SSE
//	)
func (h *SSERouteHandle[Req, Event]) WithFormats(fmts ...format.Format[Event]) *SSERouteHandle[Req, Event] {
	h.Formats = slices.Clone(fmts)
	if len(h.Descriptor.Responses) > 0 {
		var cts []string
		for _, f := range fmts {
			if ct := f.ContentType(); ct != "" {
				cts = append(cts, ct)
			}
		}
		h.Descriptor.Responses[0].ContentTypes = cts
	}
	return h
}

// ValidatePathParams validates path variable values against their registered codecs.
// Mirrors [RouteHandle.ValidatePathParams] for SSE routes.
func (h *SSERouteHandle[Req, Event]) ValidatePathParams(vars map[string]string) error {
	for i := range h.pathParams {
		pp := &h.pathParams[i]
		if pp.Codec == nil {
			continue
		}
		value, ok := vars[pp.Name]
		if !ok {
			continue
		}
		if err := pp.Codec.Validate(value); err != nil {
			return PathParamError{Name: pp.Name, Value: value, Err: err}
		}
	}
	return nil
}

// PathParamNames returns the names of all registered path parameters.
// Adapters use this to build the map required by [SSERouteHandle.ValidatePathParams].
func (h *SSERouteHandle[Req, Event]) PathParamNames() []string {
	names := make([]string, len(h.pathParams))
	for i := range h.pathParams {
		names[i] = h.pathParams[i].Name
	}
	return names
}

// ValidateQuery validates query parameter values against their registered codecs.
// Mirrors [RouteHandle.ValidateQuery] for SSE routes.
func (h *SSERouteHandle[Req, Event]) ValidateQuery(params map[string]string) error {
	for i := range h.queryParams {
		qp := &h.queryParams[i]
		if qp.Codec == nil {
			continue
		}
		value, ok := params[qp.Name]
		if !ok {
			if qp.Required {
				return QueryParamError{Name: qp.Name, Err: ErrRequiredParam}
			}
			continue
		}
		if err := qp.Codec.Validate(value); err != nil {
			return QueryParamError{Name: qp.Name, Value: value, Err: err}
		}
	}
	return nil
}

// ValidateQueryMulti validates query parameter values using a multi-value map.
// Mirrors [RouteHandle.ValidateQueryMulti] for SSE routes.
func (h *SSERouteHandle[Req, Event]) ValidateQueryMulti(params map[string][]string) error {
	for i := range h.queryParams {
		qp := &h.queryParams[i]
		if qp.Codec == nil {
			continue
		}
		values, ok := params[qp.Name]
		if !ok || len(values) == 0 {
			if qp.Required {
				return QueryParamError{Name: qp.Name, Err: ErrRequiredParam}
			}
			continue
		}
		value := values[0]
		if err := qp.Codec.Validate(value); err != nil {
			return QueryParamError{Name: qp.Name, Value: value, Err: err}
		}
	}
	return nil
}

// ValidateCookies validates cookie parameter values against their registered codecs.
// Mirrors [RouteHandle.ValidateCookies] for SSE routes.
func (h *SSERouteHandle[Req, Event]) ValidateCookies(params map[string]string) error {
	for i := range h.cookieParams {
		cp := &h.cookieParams[i]
		if cp.Codec == nil {
			continue
		}
		value, ok := params[cp.Name]
		if !ok {
			if cp.Required {
				return CookieParamError{Name: cp.Name, Err: ErrRequiredParam}
			}
			continue
		}
		if err := cp.Codec.Validate(value); err != nil {
			return CookieParamError{Name: cp.Name, Value: value, Err: err}
		}
	}
	return nil
}

// ValidateHeaders validates HTTP header values against their registered codecs.
// Mirrors [RouteHandle.ValidateHeaders] for SSE routes.
func (h *SSERouteHandle[Req, Event]) ValidateHeaders(params map[string]string) error {
	for i := range h.headerParams {
		hp := &h.headerParams[i]
		if hp.Codec == nil {
			continue
		}
		value, ok := params[hp.Name]
		if !ok {
			if hp.Required {
				return HeaderParamError{Name: hp.Name, Err: ErrRequiredParam}
			}
			continue
		}
		if err := hp.Codec.Validate(value); err != nil {
			return HeaderParamError{Name: hp.Name, Value: value, Err: err}
		}
	}
	return nil
}

// ValidateResponseHeaders validates HTTP response header values against their
// registered codecs. Only entries with a non-nil Codec are validated.
// Returns a [ResponseHeaderParamError] on the first invalid value.
// When [ResponseHeaderParam.Required] is true and the key is absent, returns a
// [ResponseHeaderParamError] wrapping [ErrRequiredParam].
func (h *SSERouteHandle[Req, Event]) ValidateResponseHeaders(headers map[string]string) error {
	for i := range h.responseHeaderParams {
		rp := &h.responseHeaderParams[i]
		if rp.Codec == nil {
			continue
		}
		value, ok := headers[rp.Name]
		if !ok {
			if rp.Required {
				return ResponseHeaderParamError{Name: rp.Name, Err: ErrRequiredParam}
			}
			continue
		}
		if err := rp.Codec.Validate(value); err != nil {
			return ResponseHeaderParamError{Name: rp.Name, Value: value, Err: err}
		}
	}
	return nil
}

// ValidateResponseCookies validates response cookie values against their
// registered codecs. Only entries with a non-nil Codec are validated.
// Returns a [ResponseCookieParamError] on the first invalid value.
// When [ResponseCookieParam.Required] is true and the key is absent, returns a
// [ResponseCookieParamError] wrapping [ErrRequiredParam].
func (h *SSERouteHandle[Req, Event]) ValidateResponseCookies(cookies map[string]string) error {
	for i := range h.responseCookieParams {
		cp := &h.responseCookieParams[i]
		if cp.Codec == nil {
			continue
		}
		value, ok := cookies[cp.Name]
		if !ok {
			if cp.Required {
				return ResponseCookieParamError{Name: cp.Name, Err: ErrRequiredParam}
			}
			continue
		}
		if err := cp.Codec.Validate(value); err != nil {
			return ResponseCookieParamError{Name: cp.Name, Value: value, Err: err}
		}
	}
	return nil
}

// SSERoute is a declarative Server-Sent Events route spec: path, codecs, and
// options. It is a value type — define it once and register it with [SSERoute.Register].
//
// Create an SSERoute with [NewSSERoute].
type SSERoute[Req, Event any] struct {
	path       string
	reqCodec   codex.Codec[Req]
	eventCodec codex.Codec[Event]
	opts       []RouteOpt
}

// NewSSERoute creates an [SSERoute] spec from path, codecs, and variadic opts.
// NewSSERoute is infallible — it only captures the spec. Validation runs at
// [SSERoute.Register] time.
//
// The route is always GET and appears in the OpenAPI spec with Content-Type
// text/event-stream.
//
// NewSSERoute is a free function (not a method) because Go requires type
// parameters to appear on free functions, not on method receivers.
//
// Typical usage:
//
//	var notifRoute = rest.NewSSERoute[struct{}, Notification]("/notifications",
//	    emptyCodec, notifCodec,
//	    rest.RouteMeta{OperationID: "streamNotifications"},
//	)
//
//	handle, err := notifRoute.Register(b)
func NewSSERoute[Req, Event any](
	path string,
	reqCodec codex.Codec[Req],
	eventCodec codex.Codec[Event],
	opts ...RouteOpt,
) SSERoute[Req, Event] {
	return SSERoute[Req, Event]{
		path:       path,
		reqCodec:   reqCodec,
		eventCodec: eventCodec,
		opts:       opts,
	}
}

// Register registers the SSE route with b and returns an [SSERouteHandle].
//
// Path validation follows the same rules as [Route.Register].
// Use [SSERouteHandle.WithFormats] after Register to configure non-JSON
// event serialisation formats.
func (s SSERoute[Req, Event]) Register(b *Builder) (*SSERouteHandle[Req, Event], error) {
	if b.pathCodec != nil {
		if err := b.pathCodec.Validate(internal.StripTemplateVars(s.path)); err != nil {
			return nil, InvalidPathError{Path: s.path, Err: err}
		}
	}

	var rb routeBuilder
	for _, opt := range s.opts {
		opt.applyRoute(&rb)
	}

	templateVars := internal.ParseTemplateVars(s.path)
	for _, p := range rb.pathParams {
		if !templateVars[p.Name] {
			return nil, InvalidPathParamError{Name: p.Name, Path: s.path}
		}
	}

	frozen := buildDescriptor("GET", s.path, s.reqCodec.Schema, s.eventCodec.Schema, rb, []string{"text/event-stream"})

	jsonReq := format.JSON(s.reqCodec)
	jsonEvent := format.JSON(s.eventCodec)

	schemes := make(map[string]SecurityScheme, len(b.securitySchemes))
	for k, v := range b.securitySchemes {
		schemes[k] = v
	}

	h := &SSERouteHandle[Req, Event]{
		Descriptor:           frozen,
		Decode:               func(body []byte) (Req, error) { return jsonReq.Unmarshal(body) },
		EncodeEvent:          func(e Event) ([]byte, error) { return jsonEvent.Marshal(e) },
		ValidateEvent:        func(e Event) error { return jsonEvent.Validate(e) },
		pathParams:           rb.pathParams,
		queryParams:          rb.queryParams,
		cookieParams:         rb.cookieParams,
		headerParams:         rb.headerParams,
		pathCodec:            b.pathCodec,
		SecuritySchemes:      schemes,
		GlobalSecurity:       slices.Clone(b.globalSecurity),
		responseHeaderParams: rb.respHeaders,
		responseCookieParams: rb.respCookies,
	}
	entry := &typedSSEEntry[Req, Event]{handle: h}
	b.entries = append(b.entries, entry)
	return h, nil
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
	for name, s := range b.securitySchemes {
		ob.AddSecurityScheme(name, s.SecurityScheme)
	}
	for _, req := range b.globalSecurity {
		ob.AddGlobalSecurity(req)
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

// buildDescriptor constructs a route.Route from method, path, schemas, and the
// accumulated routeBuilder options. respContentTypes overrides the content types
// for the primary response (used by SSE routes to force text/event-stream).
//
// Path params are converted from []PathParam to []route.Param entries for
// OpenAPI spec output. A minimal entry is auto-added for any {varName}
// placeholder in the path that has no explicit PathParam declaration.
func buildDescriptor(method, path string, reqSchema, respSchema schema.Schema, rb routeBuilder, respContentTypes []string) route.Route {
	status := rb.meta.RespStatus
	if status == "" {
		if strings.ToUpper(method) == "POST" {
			status = "201"
		} else {
			status = "200"
		}
	}

	r := route.Route{
		Method:       method,
		Path:         path,
		OperationID:  rb.meta.OperationID,
		Summary:      rb.meta.Summary,
		Description:  rb.meta.Description,
		Tags:         slices.Clone(rb.meta.Tags),
		PathParams:   buildRouteParams(rb.pathParams, path),
		QueryParams:  buildQueryParams(rb.queryParams),
		CookieParams: buildCookieParams(rb.cookieParams),
		HeaderParams: buildHeaderParams(rb.headerParams),
		Security:     slices.Clone(rb.meta.Security),
	}

	if isBodyMethod(method) {
		r.RequestBody = &route.Body{
			Required:   true,
			Schema:     reqSchema,
			SchemaName: rb.meta.ReqSchemaName,
		}
	}

	respSchemaCopy := respSchema
	primary := route.Response{
		Status:       status,
		Description:  rb.meta.RespDescription,
		Schema:       &respSchemaCopy,
		SchemaName:   rb.meta.RespSchemaName,
		ContentTypes: slices.Clone(respContentTypes),
		Headers:      append(buildResponseHeaderParams(rb.respHeaders), buildResponseCookieParams(rb.respCookies)...),
	}
	r.Responses = append([]route.Response{primary}, buildExtraResponses(rb.extraResps)...)

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

// buildRouteParams converts []PathParam to []route.Param for OpenAPI spec output.
//
// For each PathParam entry the codec's schema (if non-nil) populates the
// route.Param.Schema field. A minimal route.Param{Name: name} is also
// auto-added for any {varName} placeholder in the path that has no explicit
// PathParam declaration, so the OpenAPI spec always has complete path parameter
// coverage without requiring users to declare every variable.
func buildRouteParams(pathParams []PathParam, path string) []route.Param {
	result := make([]route.Param, 0, len(pathParams))
	declared := make(map[string]bool, len(pathParams))
	for _, p := range pathParams {
		rp := route.Param{Name: p.Name, Description: p.Description}
		if p.Codec != nil {
			rp.Schema = p.Codec.Schema
		}
		result = append(result, rp)
		declared[p.Name] = true
	}
	// Auto-add minimal entries for template vars with no explicit PathParam.
	for _, m := range internal.TemplateVarRe.FindAllStringSubmatch(path, -1) {
		name := m[1]
		if !declared[name] {
			result = append(result, route.Param{Name: name})
		}
	}
	return result
}

// buildQueryParams converts []QueryParam to []route.Param for OpenAPI spec output.
// Codec schema (when non-nil) flows into the route.Param.Schema field.
func buildQueryParams(queryParams []QueryParam) []route.Param {
	result := make([]route.Param, len(queryParams))
	for i, q := range queryParams {
		rp := route.Param{Name: q.Name, Description: q.Description, Required: q.Required}
		if q.Codec != nil {
			rp.Schema = q.Codec.Schema
		}
		result[i] = rp
	}
	return result
}

// buildCookieParams converts []CookieParam to []route.Param for OpenAPI spec output.
// Codec schema (when non-nil) flows into the route.Param.Schema field.
func buildCookieParams(cookieParams []CookieParam) []route.Param {
	result := make([]route.Param, len(cookieParams))
	for i, c := range cookieParams {
		rp := route.Param{Name: c.Name, Description: c.Description, Required: c.Required}
		if c.Codec != nil {
			rp.Schema = c.Codec.Schema
		}
		result[i] = rp
	}
	return result
}

// buildHeaderParams converts []HeaderParam to []route.Param for OpenAPI spec output.
// Codec schema (when non-nil) flows into the route.Param.Schema field.
func buildHeaderParams(headerParams []HeaderParam) []route.Param {
	result := make([]route.Param, len(headerParams))
	for i, h := range headerParams {
		rp := route.Param{Name: h.Name, Description: h.Description, Required: h.Required}
		if h.Codec != nil {
			rp.Schema = h.Codec.Schema
		}
		result[i] = rp
	}
	return result
}

// buildResponseHeaderParams converts []ResponseHeaderParam to []route.Param for OpenAPI spec output.
// Codec schema (when non-nil) flows into the route.Param.Schema field.
func buildResponseHeaderParams(params []ResponseHeaderParam) []route.Param {
	result := make([]route.Param, len(params))
	for i, p := range params {
		rp := route.Param{Name: p.Name, Description: p.Description, Required: p.Required}
		if p.Codec != nil {
			rp.Schema = p.Codec.Schema
		}
		result[i] = rp
	}
	return result
}

// buildResponseCookieParams converts []ResponseCookieParam to []route.Param for OpenAPI spec
// output. Because OpenAPI 3.1 has no first-class response cookie object, each entry is
// emitted as a "Set-Cookie" string header under responses[N].headers. The Codec schema
// (when non-nil) flows into the route.Param.Schema field.
func buildResponseCookieParams(params []ResponseCookieParam) []route.Param {
	result := make([]route.Param, len(params))
	for i, p := range params {
		rp := route.Param{
			Name:        "Set-Cookie",
			Description: p.Description,
			Required:    p.Required,
		}
		if p.Codec != nil {
			rp.Schema = p.Codec.Schema
		}
		result[i] = rp
	}
	return result
}
