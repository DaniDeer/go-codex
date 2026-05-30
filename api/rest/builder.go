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
// just to specify query parameters.
type Param = route.Param

// PathParam describes a {varName} placeholder in a route path template.
// It combines spec metadata with optional runtime validation via a codec.
type PathParam struct {
	Name        string
	Description string
	// Codec validates substituted values at [RouteHandle.BuildPath] time.
	// When non-nil, the codec's schema is also used in the OpenAPI spec.
	// Nil means no runtime validation; the spec schema will be empty.
	Codec *codex.Codec[string]
}

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

	// PathParams describes {varName} placeholder variables in the path template.
	// Each entry can add a description and/or a codec for runtime validation.
	// The codec schema is also used in the OpenAPI path parameter spec.
	//
	// PathParams is optional: the builder auto-generates a minimal parameter
	// entry for every {varName} in the path. Only specify PathParams when you
	// need a description or runtime validation for a specific variable.
	//
	// Entry names must correspond to {varName} placeholders in the path template;
	// unknown names cause [AddRoute] to return an error immediately.
	PathParams []PathParam

	// QueryParams describes query parameters for the route.
	// Each entry can add a description, required flag, and/or codec for runtime validation.
	// The codec schema flows into the OpenAPI query parameter spec automatically.
	QueryParams []QueryParam

	// CookieParams describes HTTP cookie parameters for the route.
	// Each entry can add a description, required flag, and/or codec for runtime validation.
	// The codec schema flows into the OpenAPI cookie parameter spec automatically.
	CookieParams []CookieParam

	// HeaderParams describes HTTP header parameters for the route.
	// Each entry can add a description, required flag, and/or codec for runtime validation.
	// The codec schema flows into the OpenAPI header parameter spec automatically.
	//
	// Note: OpenAPI reserves Accept, Content-Type, and Authorization as standard
	// headers — do not declare those here; they are handled via request body and
	// security scheme definitions.
	HeaderParams []HeaderParam

	// ResponseHeaderParams describes HTTP headers returned in the primary success
	// response. Each entry can add a description, required flag, and/or codec for
	// runtime validation. The adapter validates these headers after the handler
	// returns and before writing the response; a codec violation returns 500 (the
	// server violated its own contract). The codec schema flows into the OpenAPI
	// response header spec automatically.
	ResponseHeaderParams []ResponseHeaderParam

	// ResponseCookieParams describes Set-Cookie headers returned in the primary
	// success response. Each entry can add a description, required flag, and/or
	// codec for runtime validation of the cookie value. The adapter validates
	// these cookies after the handler returns and before writing the response;
	// a codec violation returns 500. The codec schema flows into the OpenAPI
	// response header spec as a "Set-Cookie" entry (OpenAPI 3.1 has no
	// first-class response cookie object).
	ResponseCookieParams []ResponseCookieParam

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

	// ResponseFormats, when non-empty, lists the formats the route can produce.
	// The adapter uses this slice for content negotiation: it picks the format
	// matching the client's Accept header and encodes the response with it.
	// When empty, the adapter falls back to JSON (via Encode).
	ResponseFormats []format.Format[Resp]

	// pathParams holds per-variable params registered via PathParams.
	pathParams []PathParam

	// queryParams holds per-parameter entries registered via QueryParams.
	queryParams []QueryParam

	// cookieParams holds per-parameter entries registered via CookieParams.
	cookieParams []CookieParam

	// headerParams holds per-parameter entries registered via HeaderParams.
	headerParams []HeaderParam

	// responseHeaderParams holds per-header entries registered via ResponseHeaderParams.
	responseHeaderParams []ResponseHeaderParam

	// responseCookieParams holds per-cookie entries registered via ResponseCookieParams.
	responseCookieParams []ResponseCookieParam

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

// ValidateQuery validates query parameter values against their registered codecs.
//
// For each [QueryParam] that has a non-nil Codec, the corresponding value in
// params is validated. Returns a [QueryParamError] on the first failure.
// Parameters not present in params are silently skipped (use Required on the
// [QueryParam] entry to document required params in the spec; enforcement is
// the caller's responsibility). Extra keys in params are silently ignored.
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
// Returns a [QueryParamError] on the first failure. Parameters not present in
// params are silently skipped; extra keys are silently ignored.
func (h *RouteHandle[Req, Resp]) ValidateQueryMulti(params map[string][]string) error {
	for i := range h.queryParams {
		qp := &h.queryParams[i]
		if qp.Codec == nil {
			continue
		}
		values, ok := params[qp.Name]
		if !ok || len(values) == 0 {
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
// Parameters not present in params are silently skipped. Extra keys are ignored.
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
// Parameters not present in params are silently skipped. Extra keys are ignored.
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
// Parameters not present in headers are silently skipped.
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
			continue
		}
		if err := rp.Codec.Validate(value); err != nil {
			return ResponseHeaderParamError{Name: rp.Name, Value: value, Err: err}
		}
	}
	return nil
}

// ValidateResponseCookies validates the cookie values collected by the handler
// against their registered [ResponseCookieParam] codecs. Only entries whose name
// is present in cookies is validated. Returns a [ResponseCookieParamError] on the
// first failure.
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

// typedRouteEntry stores the frozen descriptor for a single route.
type typedRouteEntry[Req, Resp any] struct {
	frozen route.Route
}

func (e *typedRouteEntry[Req, Resp]) descriptor() route.Route { return e.frozen }

// typedSSEEntry stores the frozen descriptor for a single SSE route.
type typedSSEEntry struct {
	frozen route.Route
}

func (e *typedSSEEntry) descriptor() route.Route { return e.frozen }

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

// InvalidPathParamError is returned by [AddRoute] when a [PathParam] entry
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
type QueryParam struct {
	Name        string
	Description string
	Required    bool
	// Codec validates query parameter values at [RouteHandle.ValidateQuery] time.
	// When non-nil, the codec's schema is also used in the OpenAPI spec.
	// Nil means no runtime validation; the spec schema will be empty.
	Codec *codex.Codec[string]
}

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
type CookieParam struct {
	Name        string
	Description string
	Required    bool
	// Codec validates cookie parameter values at [RouteHandle.ValidateCookies] time.
	// When non-nil, the codec's schema is also used in the OpenAPI spec.
	// Nil means no runtime validation; the spec schema will be empty.
	Codec *codex.Codec[string]
}

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
type HeaderParam struct {
	Name        string
	Description string
	Required    bool
	// Codec validates header values at [RouteHandle.ValidateHeaders] time.
	// When non-nil, the codec's schema is also used in the OpenAPI spec.
	// Nil means no runtime validation; the spec schema will be empty.
	Codec *codex.Codec[string]
}

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
type ResponseHeaderParam struct {
	Name        string
	Description string
	Required    bool
	// Codec validates response header values at [RouteHandle.ValidateResponseHeaders] time.
	// When non-nil, the codec's schema is also used in the OpenAPI spec.
	// Nil means no runtime validation; the spec schema will be empty.
	Codec *codex.Codec[string]
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
type ResponseCookieParam struct {
	Name        string
	Description string
	Required    bool
	// Codec validates the cookie value at [RouteHandle.ValidateResponseCookies] time.
	// When non-nil, the codec's schema is also used in the OpenAPI spec.
	// Nil means no runtime validation; the spec schema will be empty.
	Codec *codex.Codec[string]
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

// UnsupportedMediaTypeError is returned by the net/http adapter when the
// request Content-Type does not match the expected media type.
// Use [errors.As] to inspect the Got and Expected fields.
//
//	var ctErr rest.UnsupportedMediaTypeError
//	if errors.As(err, &ctErr) {
//	    log.Printf("got %q, want %q", ctErr.Got, ctErr.Expected)
//	}
type UnsupportedMediaTypeError struct {
	// Got is the actual Content-Type value sent by the client (without parameters).
	Got string
	// Expected is the media type the adapter was configured to accept.
	Expected string
}

func (e UnsupportedMediaTypeError) Error() string {
	return fmt.Sprintf("unsupported media type %q: expected %q", e.Got, e.Expected)
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
// responseFormats optionally lists the formats the route can produce for content
// negotiation. When non-empty the adapter picks the format matching the client's
// Accept header and encodes the response accordingly; a mismatch returns 406.
// When empty, the adapter defaults to JSON. The first format in the list is used
// when the client sends Accept: */*, or when no formats are registered.
//
// If the builder was created with [WithPathCodec] or [WithPathConstraints], the
// path is validated immediately. An error is returned if validation fails —
// no route is registered in that case.
//
// If config.PathParams is non-empty, each entry name is verified to be a
// {varName} present in the path template. An unknown name is a programming
// error and causes AddRoute to return an error.
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
	responseFormats ...format.Format[Resp],
) (*RouteHandle[Req, Resp], error) {
	if b.pathCodec != nil {
		if err := b.pathCodec.Validate(internal.StripTemplateVars(path)); err != nil {
			return nil, InvalidPathError{Path: path, Err: err}
		}
	}

	templateVars := internal.ParseTemplateVars(path)
	for _, p := range config.PathParams {
		if !templateVars[p.Name] {
			return nil, InvalidPathParamError{Name: p.Name, Path: path}
		}
	}

	// Collect content types from registered response formats for spec generation.
	var respContentTypes []string
	for _, f := range responseFormats {
		if ct := f.ContentType(); ct != "" {
			respContentTypes = append(respContentTypes, ct)
		}
	}

	frozen := buildDescriptor(method, path, reqCodec.Schema, respCodec.Schema, config, respContentTypes)

	entry := &typedRouteEntry[Req, Resp]{frozen: frozen}
	b.entries = append(b.entries, entry)

	jsonReq := format.JSON(reqCodec)
	jsonResp := format.JSON(respCodec)

	return &RouteHandle[Req, Resp]{
		Descriptor:           frozen,
		Decode:               func(body []byte) (Req, error) { return jsonReq.Unmarshal(body) },
		Encode:               func(resp Resp) ([]byte, error) { return jsonResp.Marshal(resp) },
		ResponseFormats:      slices.Clone(responseFormats),
		pathParams:           config.PathParams,
		queryParams:          config.QueryParams,
		cookieParams:         config.CookieParams,
		headerParams:         config.HeaderParams,
		responseHeaderParams: config.ResponseHeaderParams,
		responseCookieParams: config.ResponseCookieParams,
		pathCodec:            b.pathCodec,
	}, nil
}

// SSERouteHandle is returned by [AddSSERoute]. It holds the route descriptor
// and typed helpers for decoding requests and encoding SSE events.
//
// The adapter uses EncodeEvent to serialise each event to JSON and ValidateEvent
// to reject invalid values before they are written to the client.
// When EventFormats is non-empty the adapter may use an explicit format for
// event data serialisation (e.g. JSON or YAML inside the data field).
type SSERouteHandle[Req, Event any] struct {
	// Descriptor is the frozen route.Route built at registration time.
	Descriptor route.Route

	// Decode deserialises and validates a JSON request body into Req.
	// For SSE (GET) routes, this is rarely called — use [RequestFromContext]
	// to read path and query parameters instead.
	Decode func(body []byte) (Req, error)

	// EncodeEvent serialises one event value to JSON bytes.
	// Used as the fallback encoder when EventFormats is empty.
	EncodeEvent func(e Event) ([]byte, error)

	// ValidateEvent runs the event codec constraints on e without serialising.
	// The adapter calls this inside the send func before encoding.
	ValidateEvent func(e Event) error

	// EventFormats, when non-empty, lists the formats available for encoding
	// event data. The adapter picks the first format (or the JSON fallback).
	EventFormats []format.Format[Event]

	// pathParams holds per-variable params registered via PathParams in config.
	pathParams []PathParam

	// pathCodec is the builder-level path codec (may be nil).
	pathCodec *codex.Codec[string]
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

// AddSSERoute registers a Server-Sent Events route (always GET) with the
// builder and returns an [SSERouteHandle].
//
// reqCodec decodes the (usually empty) request body; for query/path parameter
// extraction use [RequestFromContext] inside the handler.
// eventCodec validates and encodes each outbound event as JSON.
// eventFormats optionally lists formats for event data serialisation; when
// empty the adapter defaults to JSON. The first format is the default encoder.
//
// The route appears in the OpenAPI spec with Content-Type text/event-stream.
// Path validation follows the same rules as [AddRoute].
func AddSSERoute[Req, Event any](
	b *Builder,
	path string,
	reqCodec codex.Codec[Req],
	eventCodec codex.Codec[Event],
	config RouteConfig,
	eventFormats ...format.Format[Event],
) (*SSERouteHandle[Req, Event], error) {
	if b.pathCodec != nil {
		if err := b.pathCodec.Validate(internal.StripTemplateVars(path)); err != nil {
			return nil, InvalidPathError{Path: path, Err: err}
		}
	}

	templateVars := internal.ParseTemplateVars(path)
	for _, p := range config.PathParams {
		if !templateVars[p.Name] {
			return nil, InvalidPathParamError{Name: p.Name, Path: path}
		}
	}

	frozen := buildDescriptor("GET", path, reqCodec.Schema, eventCodec.Schema, config, []string{"text/event-stream"})
	entry := &typedSSEEntry{frozen: frozen}
	b.entries = append(b.entries, entry)

	jsonReq := format.JSON(reqCodec)
	jsonEvent := format.JSON(eventCodec)

	return &SSERouteHandle[Req, Event]{
		Descriptor:    frozen,
		Decode:        func(body []byte) (Req, error) { return jsonReq.Unmarshal(body) },
		EncodeEvent:   func(e Event) ([]byte, error) { return jsonEvent.Marshal(e) },
		ValidateEvent: func(e Event) error { return jsonEvent.Validate(e) },
		EventFormats:  slices.Clone(eventFormats),
		pathParams:    config.PathParams,
		pathCodec:     b.pathCodec,
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
//
// Path params are converted from PathParams ([]PathParam) to route.Param entries
// for OpenAPI spec output. A minimal entry is auto-added for any {varName}
// placeholder in the path that has no explicit PathParams declaration.
func buildDescriptor(method, path string, reqSchema, respSchema schema.Schema, config RouteConfig, respContentTypes []string) route.Route {
	status := config.RespStatus
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
		OperationID:  config.OperationID,
		Summary:      config.Summary,
		Description:  config.Description,
		Tags:         slices.Clone(config.Tags),
		PathParams:   buildRouteParams(config.PathParams, path),
		QueryParams:  buildQueryParams(config.QueryParams),
		CookieParams: buildCookieParams(config.CookieParams),
		HeaderParams: buildHeaderParams(config.HeaderParams),
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
		Status:       status,
		Description:  config.RespDescription,
		Schema:       &respSchemaCopy,
		SchemaName:   config.RespSchemaName,
		ContentTypes: slices.Clone(respContentTypes),
		Headers:      append(buildResponseHeaderParams(config.ResponseHeaderParams), buildResponseCookieParams(config.ResponseCookieParams)...),
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
