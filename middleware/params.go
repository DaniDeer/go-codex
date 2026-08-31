package middleware

import "github.com/DaniDeer/go-codex/codex"

// HeaderParamSpec, CookieParamSpec, QueryParamSpec, ResponseHeaderParamSpec,
// and ResponseCookieParamSpec are middleware-package-local typed mirrors of
// [rest.HeaderParam]/[rest.CookieParam]/[rest.QueryParam]/
// [rest.ResponseHeaderParam]/[rest.ResponseCookieParam]'s field shape —
// living here, not in api/rest, because api/rest already imports middleware
// (for [Middleware]/[ServerImplementation]/etc.), so middleware importing
// api/rest back would create a cycle. This exactly mirrors why
// [SecurityDeclaration] carries its own [Codec] field instead of embedding
// an api/rest-specific type.
//
// [Middleware.RequestHeaderParams]/[Middleware.RequestCookieParams]/
// [Middleware.RequestQueryParams]/[Middleware.ResponseHeaderParams]/
// [Middleware.ResponseCookieParams] use these types directly — a
// middleware author builds one of these structs, not a [rest.HeaderParam]
// value, when contributing a param spec entry. Bridge functions in
// api/rest (rest.FromHeaderParam/FromCookieParam/FromQueryParam/
// FromResponseHeaderParam/FromResponseCookieParam) wrap an EXISTING
// rest.XParam value into the matching spec type, for callers that already
// have one (mirrors [SecurityScheme]/FromSecurityScheme's own "build from
// scratch" vs. "bridge an existing value" split).
type HeaderParamSpec struct {
	Name, Description string
	Required          bool
	// Codec validates the header value. Nil means no runtime validation;
	// the spec schema will be empty.
	Codec *codex.Codec[string]
}

// CookieParamSpec is [HeaderParamSpec]'s cookie-request-param sibling.
type CookieParamSpec struct {
	Name, Description string
	Required          bool
	Codec             *codex.Codec[string]
}

// QueryParamSpec is [HeaderParamSpec]'s query-param sibling.
type QueryParamSpec struct {
	Name, Description string
	Required          bool
	Codec             *codex.Codec[string]
}

// ResponseHeaderParamSpec is [HeaderParamSpec]'s response-header sibling.
type ResponseHeaderParamSpec struct {
	Name, Description string
	Required          bool
	Codec             *codex.Codec[string]
}

// ResponseCookieParamSpec is [HeaderParamSpec]'s response-cookie sibling.
type ResponseCookieParamSpec struct {
	Name, Description string
	Required          bool
	Codec             *codex.Codec[string]
}
