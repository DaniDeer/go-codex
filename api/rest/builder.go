package rest

import (
	"errors"
	"fmt"
	"log/slog"
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

// PathParam describes an HTTP path variable for a route (e.g. `{id}` in `/users/{id}`).
// It combines spec metadata with optional runtime validation via a codec.
//
// Entry names must correspond to {varName} placeholders in the path template;
// unknown names cause [Route.Register] to return an error immediately.
//
// PathParam is optional: the builder auto-generates a minimal parameter entry
// for every {varName} in the path. Only specify PathParam when you need a
// description or runtime validation for a specific variable.
//
// Note: path parameters are always required by the OpenAPI specification — there
// is no Required field. For optional key-value parameters use [QueryParam] with
// Required: true or false as appropriate.
//
// PathParam implements [RouteOpt]: pass it directly to [NewRoute] or [NewSSERoute].
// PathParam mirrors [codex.Param]'s shape field-for-field — the shared,
// VALIDATE-ONLY escape hatch (see codex/param.go's own doc comment for the
// cross-package rationale). Kept as a flat, non-embedded struct (rather
// than embedding codex.Param) so existing `rest.PathParam{Name: "id"}`
// struct literals keep compiling unchanged (Go requires keyed literal
// fields to be the struct's OWN fields, not promoted ones). A rest-owned
// type is required regardless (Go's method-locality rule: a type
// satisfying [RouteOpt] must be defined in this package), but PathParam
// keeps its own name/vocabulary rather than a generic shared one — rest's
// own domain concept ("a PATH variable"), distinct from
// api/events'/api/reqreply's "TopicParam".
type PathParam struct {
	Name        string
	Description string
	// Codec validates path parameter values at [RouteHandle.ValidatePathParams] and
	// [RouteHandle.BuildPath] time.
	// When non-nil, the codec's schema is also used in the OpenAPI spec.
	// Nil means no runtime validation; the spec schema will be empty.
	Codec *codex.Codec[string]
}

func (p PathParam) applyRoute(rb *routeBuilder) { rb.pathParams = append(rb.pathParams, p) }

// WithCodec sets the validation codec and returns the updated PathParam.
func (p PathParam) WithCodec(c codex.Codec[string]) PathParam { p.Codec = &c; return p }

// toParam converts p to the shared [codex.Param] shape.
func (p PathParam) toParam() codex.Param {
	return codex.Param{Name: p.Name, Description: p.Description, Codec: p.Codec}
}

// MergedPathParam is returned by [NewPathParam]. It wraps
// [codex.MergedParam][T] — the shared merge-capable counterpart to
// [PathParam] — so the same declaration serves both OpenAPI spec
// generation/validation AND automatic merge into Req via
// [RouteHandle.DecodeMerged] / [RouteHandle.MergeFields].
type MergedPathParam[T any] struct {
	codex.MergedParam[T]
}

// NewPathParam declares a path parameter that is BOTH validated against
// codec (exactly like plain [PathParam], unchanged spec/validation
// behavior) AND automatically merged into Req by [RouteHandle.DecodeMerged]
// — one declaration instead of a PathParam plus a separate codex.Field.
//
// NewPathParam is the PRIMARY, recommended way to declare a path parameter.
// The plain [PathParam] struct literal remains available as the low-level
// escape hatch for validate-only parameters with no merge need (avoids
// forcing a get/set pair on a parameter the handler never reads directly).
//
//	rest.NewRoute[GetUserReq, User]("GET", "/users/{id}", reqCodec, userCodec,
//	    rest.NewPathParam("id", codex.String().Refine(validate.UUID),
//	        func(r GetUserReq) string { return r.ID },
//	        func(r *GetUserReq, v string) { r.ID = v },
//	    ),
//	)
func NewPathParam[T any](
	name string,
	codec codex.Codec[string],
	get func(T) string,
	set func(*T, string),
) MergedPathParam[T] {
	return MergedPathParam[T]{MergedParam: codex.NewParam(name, codec, get, set)}
}

// WithDescription sets the PARAMETER-level description (rendered into the
// OpenAPI "parameter" object, distinct from the codec's schema-level
// description) and returns the updated value, mirroring PathParam.WithCodec's
// existing chain style.
func (p MergedPathParam[T]) WithDescription(desc string) MergedPathParam[T] {
	p.MergedParam = p.MergedParam.WithDescription(desc)
	return p
}

func (p MergedPathParam[T]) applyRoute(rb *routeBuilder) {
	rb.pathParams = append(rb.pathParams, PathParam{Name: p.Name, Description: p.Description, Codec: p.Codec}) // unchanged spec/validation path
	rb.pathMergeFields = append(rb.pathMergeFields, p.Field)
}

// toCodexParams converts pathParams to []codex.Param for [codex.BuildFromParams]/
// [codex.ValidateParams]/[codex.ValidateDeclaredParams].
func toCodexParams(pathParams []PathParam) []codex.Param {
	out := make([]codex.Param, len(pathParams))
	for i, p := range pathParams {
		out[i] = p.toParam()
	}
	return out
}

// requestFormatsOpt / formatsOpt are unexported RouteOpt implementations
// backing [RequestFormats] and [Formats] — see those constructors.
type requestFormatsOpt[Req any] struct{ fmts []format.Format[Req] }

func (o requestFormatsOpt[Req]) applyRoute(rb *routeBuilder) { rb.requestFormats = o.fmts }

// RequestFormats declares the formats a route accepts for request body
// decoding — the [RouteOpt] equivalent of calling
// [RouteHandle.WithRequestFormats] after [Route.Register]. Declarable inline
// in [NewRoute]'s variadic opts, which means it also works through
// ports.RESTPattern.Opts with zero changes to the ports package:
//
//	rest.NewRoute[UploadReq, ImageMeta]("PUT", "/images/{id}", reqCodec, respCodec,
//	    rest.RequestFormats(format.Binary(pngCodec).WithContentType("image/png")),
//	)
//
// A mismatched type (fmts holding format.Format[X] where the route's request
// type is not X) is only detectable once Req is concrete — [Route.Register]
// returns [FormatOptError] in that case.
func RequestFormats[Req any](fmts ...format.Format[Req]) RouteOpt {
	return requestFormatsOpt[Req]{fmts: fmts}
}

type formatsOpt[Resp any] struct{ fmts []format.Format[Resp] }

func (o formatsOpt[Resp]) applyRoute(rb *routeBuilder) { rb.respFormats = o.fmts }

// Formats declares the formats a route can produce for response content
// negotiation — the [RouteOpt] equivalent of calling [RouteHandle.WithFormats]
// after [Route.Register]. See [RequestFormats] for the inline-declaration
// rationale; [Route.Register] returns [FormatOptError] on a type mismatch.
func Formats[Resp any](fmts ...format.Format[Resp]) RouteOpt {
	return formatsOpt[Resp]{fmts: fmts}
}

// FormatOptError is returned by [Route.Register] when [RequestFormats] or
// [Formats] was declared with formats for a type that does not match the
// route's actual request/response type parameter.
type FormatOptError struct {
	// Direction is "request" (from [RequestFormats]) or "response" (from [Formats]).
	Direction string
	Err       error
}

func (e FormatOptError) Error() string {
	return fmt.Sprintf("api/rest: %s format option: %v", e.Direction, e.Err)
}

// Unwrap allows [errors.Is] and [errors.As] to reach the underlying error.
func (e FormatOptError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e FormatOptError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("direction", e.Direction),
		slog.Any("err", e.Err),
	)
}

// MergeFieldTypeError is returned by [Route.Register] when a merge field
// registered via [NewPathParam]/[NewRequiredQueryParam]/etc. does not match
// Req — a caller programming error (the get/set functions passed to the
// constructor referenced the wrong type), discovered at Register time where
// Req becomes concrete, mirroring [FormatOptError]'s existing handling of
// the same class of problem for request/response formats.
type MergeFieldTypeError struct {
	Err error
}

func (e MergeFieldTypeError) Error() string {
	return fmt.Sprintf("api/rest: merge field option: %v", e.Err)
}

// Unwrap allows [errors.Is] and [errors.As] to reach the underlying error.
func (e MergeFieldTypeError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e MergeFieldTypeError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.Any("err", e.Err),
	)
}

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

// errorStatusRule stores one per-route error-status mapping declared via
// [ErrorStatus].
type errorStatusRule struct {
	status int
	// typeName is used for doc/debug readability only.
	typeName string
	match    func(error) bool
}

func (r errorStatusRule) applyRoute(rb *routeBuilder) {
	rb.errorStatusRules = append(rb.errorStatusRules, r)
}

// ErrorAction selects how a matched [ErrorPattern] is realized by the
// adapter. A matched pattern executes exactly ONE of these, never an
// implicit chain — mirrors [events.ErrorAction]/[websocket.ErrorFrame]'s
// action model, adapted to REST's request/response boundary.
type ErrorAction string

const (
	// ErrorRespond writes the declared typed error body (+status) directly.
	// This is the default action when [ErrorPattern] is declared without an
	// explicit action — REST always has a caller to respond to.
	ErrorRespond ErrorAction = "respond"
	// ErrorHandle skips the automatic typed body write; the adapter falls
	// through to [Options.ErrorHandler] instead (REST's existing envelope
	// escape hatch — the same behavior as an unmatched error, but the
	// resolved status from this pattern's declared status still applies).
	ErrorHandle ErrorAction = "handle"
	// ErrorLog skips the automatic typed body write; behaves identically to
	// [ErrorHandle] for REST (both fall through to [Options.ErrorHandler]) —
	// kept as a distinct value for vocabulary parity with
	// [events.ErrorAction]/[websocket.ErrorFrame] across boundaries.
	ErrorLog ErrorAction = "log"
)

// ErrorPatternResponse is the adapter-ready payload produced by
// [RouteHandle.ErrorResponseFor] when a declared [ErrorPattern] matches.
type ErrorPatternResponse struct {
	Status int
	Body   []byte
	Value  any
	// Action is the resolved action for the matched pattern. Adapters
	// auto-write Body only when Action is [ErrorRespond] (the default);
	// [ErrorHandle]/[ErrorLog] fall through to [Options.ErrorHandler].
	Action ErrorAction
}

type errorPatternRule struct {
	status int
	action ErrorAction
	match  func(error) (ErrorPatternResponse, bool, error)
	// decode is the client-side counterpart of match: given the raw wire
	// body for a response whose status equals status, decode it via the
	// pattern's declared codec. Only populated for [ErrorPattern] rules
	// (never nil after applyRoute).
	decode func([]byte) (ErrorPatternResponse, error)
}

func (r errorPatternRule) applyRoute(rb *routeBuilder) {
	rb.errorPatternRules = append(rb.errorPatternRules, r)
}

// ErrorPatternOpt is the [RouteOpt] value returned by [ErrorPattern].
type ErrorPatternOpt[E error, B any] struct {
	status int
	codec  codex.Codec[B]
	mapper func(E) (B, error)
	action ErrorAction
}

// WithAction returns a copy of o with Action set to action, overriding the
// default [ErrorRespond]. A matched pattern executes exactly one action —
// never an implicit respond-then-handle chain.
func (o ErrorPatternOpt[E, B]) WithAction(action ErrorAction) ErrorPatternOpt[E, B] {
	o.action = action
	return o
}

func (o ErrorPatternOpt[E, B]) applyRoute(rb *routeBuilder) {
	action := o.action
	if action == "" {
		action = ErrorRespond
	}
	jsonCodec := format.JSON(o.codec)
	mapper := o.mapper
	status := o.status
	schemaCopy := o.codec.Schema

	rule := errorPatternRule{
		status: status,
		action: action,
		decode: func(body []byte) (ErrorPatternResponse, error) {
			payload, err := jsonCodec.Unmarshal(body)
			if err != nil {
				return ErrorPatternResponse{}, err
			}
			return ErrorPatternResponse{Status: status, Body: body, Value: payload, Action: action}, nil
		},
		match: func(err error) (ErrorPatternResponse, bool, error) {
			var target E
			if !errors.As(err, &target) {
				return ErrorPatternResponse{}, false, nil
			}

			var (
				payload B
				ok      bool
			)
			if mapper != nil {
				mapped, mapErr := mapper(target)
				if mapErr != nil {
					return ErrorPatternResponse{}, true, mapErr
				}
				payload = mapped
			} else {
				payload, ok = any(target).(B)
				if !ok {
					return ErrorPatternResponse{}, true,
						fmt.Errorf("api/rest: ErrorPattern direct mode: %T not assignable to payload", target)
				}
			}

			body, encErr := jsonCodec.Marshal(payload)
			if encErr != nil {
				return ErrorPatternResponse{}, true, encErr
			}
			return ErrorPatternResponse{Status: status, Body: body, Value: payload, Action: action}, true, nil
		},
	}

	rule.applyRoute(rb)
	errorStatusRule{
		status:   status,
		typeName: fmt.Sprintf("%T", *new(E)),
		match: func(err error) bool {
			var target E
			return errors.As(err, &target)
		},
	}.applyRoute(rb)
	ResponseMeta{
		Status: fmt.Sprintf("%d", status),
		Schema: &schemaCopy,
	}.applyRoute(rb)
}

// ErrorStatus declares a per-route mapping from an error type to the HTTP
// status code the adapter should pass to Options.ErrorHandler.
//
// This mapping is consumed by both plain and pipeline handlers.
//
// Matching is type-only: the first declared mapping whose type matches via
// [errors.As] wins.
//
//	rest.NewRoute[Req, Resp]("POST", "/jobs", reqCodec, respCodec,
//	    rest.ErrorStatus[domain.InvalidTransitionError](http.StatusConflict),
//	)
func ErrorStatus[E error](status int) RouteOpt {
	return errorStatusRule{
		status:   status,
		typeName: fmt.Sprintf("%T", *new(E)),
		match: func(err error) bool {
			var target E
			return errors.As(err, &target)
		},
	}
}

// ErrorPattern declares a codec-backed typed error response for a matched
// error type. The first matching declared pattern wins.
//
// Two modes:
//   - Direct: no mapFn provided, E value must be assignable to B.
//   - Mapped: mapFn provided, mapFn(E) produces B.
//
// On a match, adapters emit status+body directly (the default [ErrorRespond]
// action) without delegating to Options.ErrorHandler. Use
// [ErrorPatternOpt.WithAction] to select [ErrorHandle]/[ErrorLog] instead —
// the adapter then falls through to Options.ErrorHandler (still using this
// pattern's declared status).
func ErrorPattern[E error, B any](
	status int,
	codec codex.Codec[B],
	mapFn ...func(E) (B, error),
) ErrorPatternOpt[E, B] {
	var mapper func(E) (B, error)
	if len(mapFn) > 0 {
		mapper = mapFn[0]
	}
	return ErrorPatternOpt[E, B]{status: status, codec: codec, mapper: mapper}
}

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
//   - [ErrorStatus] — per-route error type → HTTP status mapping
//   - [ErrorPattern] — per-route typed error response (status + codec-backed body + [ErrorAction])
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
	// requestFormats/respFormats hold []format.Format[Req]/[]format.Format[Resp]
	// type-erased (any) — set by [RequestFormats]/[Formats], resolved generically
	// in [Route.Register] where Req/Resp are concrete. See [FormatOptError].
	requestFormats any
	respFormats    any
	// pathMergeFields/queryMergeFields/headerMergeFields/cookieMergeFields
	// hold type-erased codex.FieldCodec[Req] values registered via
	// NewPathParam/NewRequiredQueryParam/etc., kept SEPARATE per role.
	// Role separation matters for the ENCODE direction (client-side
	// nethttp.Call): CallOptions.QueryParams/HeaderParams/CookieParams each
	// add EVERY map entry to their respective HTTP location with no name
	// filtering, so a flat merge-field list would leak a path value into
	// the query string (etc.) if reused across roles. The DECODE direction
	// (DecodeMerged) is safe with a flat union since the four SOURCE maps
	// are already correctly scoped before merging — see
	// [RouteHandle.MergeFields] (aggregate, decode-side) vs
	// [RouteHandle.PathMergeFields]/[QueryMergeFields]/[HeaderMergeFields]/
	// [CookieMergeFields] (role-scoped, encode-side).
	// Resolved to []codex.FieldCodec[Req] in [Route.Register]. See
	// [MergeFieldTypeError].
	pathMergeFields   []any
	queryMergeFields  []any
	headerMergeFields []any
	cookieMergeFields []any

	// responseHeaderMergeFields/responseCookieMergeFields hold type-erased
	// codex.FieldCodec[Resp] values registered via
	// NewRequiredResponseHeaderParam/etc. — the RESPONSE-direction mirror
	// of pathMergeFields/etc. above. Resolved to []codex.FieldCodec[Resp]
	// in [Route.Register]. See [MergeFieldTypeError].
	responseHeaderMergeFields []any
	responseCookieMergeFields []any
	// sseEventMergeFields holds type-erased codex.FieldCodec[Event] values
	// registered via NewRequiredSSEEventParam/NewOptionalSSEEventParam.
	// Resolved to []codex.FieldCodec[Event] in [SSERoute.Register].
	sseEventMergeFields []any
	// errorStatusRules hold per-route error type -> HTTP status mappings declared
	// via [ErrorStatus].
	errorStatusRules []errorStatusRule
	// errorPatternRules hold per-route typed error response declarations from
	// [ErrorPattern]. Adapters may emit these directly before ErrorHandler.
	errorPatternRules []errorPatternRule
	// securitySchemes holds per-route security scheme declarations from
	// [WithSecurityScheme] — the ONLY source of RouteHandle.SecuritySchemes;
	// there is no builder-level equivalent. Consumed identically by
	// [Route.Register] and [Route.ClientHandle].
	securitySchemes map[string]SecurityScheme
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

	// EncodeRequest serialises Req to JSON bytes for use as an outgoing HTTP
	// request body. It is the complement of Decode and is used by client-side
	// adapters (e.g. nethttp.Call) to encode the typed request before sending.
	EncodeRequest func(req Req) ([]byte, error)

	// DecodeResponse deserialises and validates a JSON response body into Resp.
	// It is the complement of Encode and is used by client-side adapters to
	// decode the server response into a typed value.
	DecodeResponse func(body []byte) (Resp, error)

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
	// Populated from the route's own [WithSecurityScheme] declarations —
	// this is the ONLY way to declare a security scheme; there is no
	// builder-level equivalent. Both [Route.Register] and
	// [Route.ClientHandle] populate this field identically, so the SAME
	// route value builds a server-side handle and a client-side handle
	// with IDENTICAL credential-format enforcement on both sides: the
	// server adapter's Handler validates an INCOMING credential against
	// Codec before calling SecurityFunc; [nethttp.Call] validates an
	// OUTGOING credential (the header CredentialFunc returned) against the
	// SAME Codec before sending.
	SecuritySchemes map[string]SecurityScheme

	// GlobalSecurity holds the builder-level security requirements that apply
	// when Descriptor.Security is nil (i.e. the route inherits global security).
	// Adapters resolve the effective requirements as:
	//   reqs := handle.Descriptor.Security
	//   if reqs == nil { reqs = handle.GlobalSecurity }
	// Set via [Builder.AddGlobalSecurity]. nil when no global security is declared.
	// Unlike SecuritySchemes, GlobalSecurity remains builder-only — it answers
	// "which routes require auth by default" (spec-wide), not "what does a
	// scheme look like" — and has no [Route.ClientHandle] equivalent (always
	// nil there, unchanged).
	GlobalSecurity []route.SecurityRequirement

	// pathMergeFields/queryMergeFields/headerMergeFields/cookieMergeFields
	// hold the merge-capable fields registered via NewPathParam/
	// NewRequiredQueryParam/etc., kept SEPARATE per role — see
	// [PathMergeFields]/[QueryMergeFields]/[HeaderMergeFields]/
	// [CookieMergeFields] (role-scoped, for the ENCODE/client direction)
	// and [MergeFields] (aggregate, for the DECODE/server direction via
	// [DecodeMerged]).
	pathMergeFields   []codex.FieldCodec[Req]
	queryMergeFields  []codex.FieldCodec[Req]
	headerMergeFields []codex.FieldCodec[Req]
	cookieMergeFields []codex.FieldCodec[Req]

	// responseHeaderMergeFields/responseCookieMergeFields hold the
	// merge-capable fields registered via
	// NewRequiredResponseHeaderParam/etc. — the RESPONSE-direction mirror
	// of the four fields above. Unlike the request side, no role split is
	// needed here: headers and cookies are always written to two DISTINCT
	// destinations ([ResponseHeaderMergeFields] → HTTP headers,
	// [ResponseCookieMergeFields] → Set-Cookie), so there is no
	// leak-across-roles hazard analogous to nethttp.CallOptions'
	// QueryParams/HeaderParams/CookieParams maps.
	responseHeaderMergeFields []codex.FieldCodec[Resp]
	responseCookieMergeFields []codex.FieldCodec[Resp]
	// errorStatusRules are per-route mappings declared via [ErrorStatus].
	errorStatusRules []errorStatusRule
	// errorPatternRules are per-route typed error response declarations from
	// [ErrorPattern].
	errorPatternRules []errorPatternRule
}

// ErrorStatusFor returns the first declared per-route mapping status for err
// (matching via [errors.As]), or (0, false) when none match.
func (h *RouteHandle[Req, Resp]) ErrorStatusFor(err error) (int, bool) {
	if err == nil {
		return 0, false
	}
	for _, rule := range h.errorStatusRules {
		if rule.match != nil && rule.match(err) {
			return rule.status, true
		}
	}
	return 0, false
}

// ErrorResponseFor returns the first matching route-declared [ErrorPattern]
// response, or ok=false when no pattern matches.
func (h *RouteHandle[Req, Resp]) ErrorResponseFor(err error) (resp ErrorPatternResponse, ok bool, applyErr error) {
	if err == nil {
		return ErrorPatternResponse{}, false, nil
	}
	for _, rule := range h.errorPatternRules {
		if rule.match == nil {
			continue
		}
		matchedResp, matched, matchErr := rule.match(err)
		if matched {
			return matchedResp, true, matchErr
		}
	}
	return ErrorPatternResponse{}, false, nil
}

// DecodeErrorFor is the client-side counterpart of [RouteHandle.ErrorResponseFor]:
// given a non-2xx response status and its raw body, it looks up the first
// declared [ErrorPattern] (in declaration order) whose status matches AND
// whose action is the default [ErrorRespond] — patterns tagged
// [ErrorPatternOpt.WithAction] with [ErrorHandle] or [ErrorLog] are skipped,
// since the server does not guarantee those patterns' typed body reached
// the wire (it falls through to Options.ErrorHandler instead, which may
// write anything).
//
// Matching is status-only: the client has no Go error value to match via
// [errors.As], only the wire status code.
//
// On a match, body is decoded via that pattern's declared codec. A decode
// failure (e.g. schema drift between client/server versions) is returned as
// applyErr with ok=true — callers should treat any non-nil applyErr the same
// as ok=false (fall back to an untyped error).
func (h *RouteHandle[Req, Resp]) DecodeErrorFor(status int, body []byte) (resp ErrorPatternResponse, ok bool, applyErr error) {
	for _, rule := range h.errorPatternRules {
		if rule.status != status || rule.decode == nil || rule.action != ErrorRespond {
			continue
		}
		decoded, err := rule.decode(body)
		return decoded, true, err
	}
	return ErrorPatternResponse{}, false, nil
}

// MergeFields returns ALL merge-capable fields registered via
// [NewPathParam]/[NewRequiredQueryParam]/[NewOptionalQueryParam]/etc.,
// aggregated across path/query/header/cookie. Safe for the DECODE
// direction ([codex.DecodeVars], [RouteHandle.DecodeMerged]) because the
// source vars map is already correctly scoped (built from four separate,
// already-role-correct maps) before the merge runs.
//
// Do NOT use this aggregate for the ENCODE direction (building an outgoing
// client request) — [CallOptions.QueryParams]/[HeaderParams]/[CookieParams]
// each add EVERY map entry to their respective HTTP location with no name
// filtering, so passing one flat map built from ALL merge fields would leak
// a path value into the query string (etc.). Use [PathMergeFields]/
// [QueryMergeFields]/[HeaderMergeFields]/[CookieMergeFields] instead —
// each returns only that role's fields, safe to pass to
// [codex.EncodeVars] independently and route to the matching [nethttp.Call]
// parameter/option.
func (h *RouteHandle[Req, Resp]) MergeFields() []codex.FieldCodec[Req] {
	all := make([]codex.FieldCodec[Req], 0,
		len(h.pathMergeFields)+len(h.queryMergeFields)+len(h.headerMergeFields)+len(h.cookieMergeFields))
	all = append(all, h.pathMergeFields...)
	all = append(all, h.queryMergeFields...)
	all = append(all, h.headerMergeFields...)
	all = append(all, h.cookieMergeFields...)
	return all
}

// PathMergeFields returns the merge-capable fields registered via
// [NewPathParam] — role-scoped, safe for both directions:
//
//	// Client (encode): build the vars map for nethttp.Call from req.
//	vars, _ := codex.EncodeVars(req, handle.PathMergeFields()...)
//	resp, err := nethttp.Call(ctx, client, baseURL, handle, req, vars, nethttp.CallOptions{})
func (h *RouteHandle[Req, Resp]) PathMergeFields() []codex.FieldCodec[Req] {
	return h.pathMergeFields
}

// QueryMergeFields returns the merge-capable fields registered via
// [NewRequiredQueryParam]/[NewOptionalQueryParam] — role-scoped, safe for
// both directions:
//
//	// Client (encode): build CallOptions.QueryParams from req.
//	query, _ := codex.EncodeVars(req, handle.QueryMergeFields()...)
//	resp, err := nethttp.Call(ctx, client, baseURL, handle, req, vars,
//	    nethttp.CallOptions{QueryParams: query})
func (h *RouteHandle[Req, Resp]) QueryMergeFields() []codex.FieldCodec[Req] {
	return h.queryMergeFields
}

// HeaderMergeFields returns the merge-capable fields registered via
// [NewRequiredHeaderParam]/[NewOptionalHeaderParam] — role-scoped, safe for
// both directions (see [PathMergeFields] for the usage pattern; substitute
// [CallOptions.HeaderParams]).
func (h *RouteHandle[Req, Resp]) HeaderMergeFields() []codex.FieldCodec[Req] {
	return h.headerMergeFields
}

// CookieMergeFields returns the merge-capable fields registered via
// [NewRequiredCookieParam]/[NewOptionalCookieParam] — role-scoped, safe for
// both directions (see [PathMergeFields] for the usage pattern; substitute
// [CallOptions.CookieParams]).
func (h *RouteHandle[Req, Resp]) CookieMergeFields() []codex.FieldCodec[Req] {
	return h.cookieMergeFields
}

// DecodeMerged decodes body (if the route has a request body — pass nil
// for body-less routes) AND merges every [MergeFields]-registered path/
// query/header/cookie value into the SAME Req value, using
// [codex.DecodeVars] internally. Additive — [RouteHandle.Decode] is
// unchanged and keeps working exactly as today; DecodeMerged behaves
// identically to a bare Decode when the route declares no merge-capable
// params (MergeFields() is empty).
//
// On failure, the body decode error (if the route has a body) is returned
// FIRST — matching Decode's existing "stop at first structural failure"
// behavior — before the var-merge step runs. The var-merge step itself
// collects every field's failure via [codex.DecodeVars] (never stops at
// the first one).
func (h *RouteHandle[Req, Resp]) DecodeMerged(
	body []byte,
	pathVars, query, headers, cookies map[string]string,
) (Req, error) {
	var req Req
	var err error
	if h.Descriptor.RequestBody != nil && len(body) > 0 {
		req, err = h.Decode(body)
		if err != nil {
			return req, err
		}
	}
	mergeFields := h.MergeFields()
	if len(mergeFields) == 0 {
		return req, nil
	}
	vars := make(map[string]string, len(pathVars)+len(query)+len(headers)+len(cookies))
	for k, v := range pathVars {
		vars[k] = v
	}
	for k, v := range query {
		vars[k] = v
	}
	for k, v := range headers {
		vars[k] = v
	}
	for k, v := range cookies {
		vars[k] = v
	}
	if err := codex.DecodeVars(&req, vars, mergeFields...); err != nil {
		return req, err
	}
	return req, nil
}

// ResponseHeaderMergeFields returns the merge-capable fields registered via
// [NewRequiredResponseHeaderParam]/[NewOptionalResponseHeaderParam] — the
// RESPONSE-direction mirror of [PathMergeFields]/etc. On the server, the
// adapter encodes these from the handler's returned Resp via
// [codex.EncodeVars] and sets them as actual HTTP response headers
// automatically. On the client, [DecodeMergedResponse] merges the HTTP
// response's headers back into the decoded Resp.
func (h *RouteHandle[Req, Resp]) ResponseHeaderMergeFields() []codex.FieldCodec[Resp] {
	return h.responseHeaderMergeFields
}

// ResponseCookieMergeFields returns the merge-capable fields registered via
// [NewRequiredResponseCookieParam]/[NewOptionalResponseCookieParam] — same
// as [ResponseHeaderMergeFields], for Set-Cookie instead of headers.
func (h *RouteHandle[Req, Resp]) ResponseCookieMergeFields() []codex.FieldCodec[Resp] {
	return h.responseCookieMergeFields
}

// DecodeMergedResponse decodes the response body (via [RouteHandle.DecodeResponse],
// when body is non-empty) AND merges every registered response header/cookie
// value into the SAME Resp value, using [codex.DecodeVars] internally. This
// is the RESPONSE-direction mirror of [DecodeMerged] — used internally by
// [nethttp.Call], and directly usable by any hand-rolled client. Behaves
// identically to a bare DecodeResponse when the route declares no response
// merge-capable params.
//
// On failure, the body decode error is returned FIRST (matching
// [DecodeMerged]'s precedent) before the header/cookie merge step runs; the
// merge step itself collects every field's failure via [codex.DecodeVars].
func (h *RouteHandle[Req, Resp]) DecodeMergedResponse(
	body []byte,
	headers, cookies map[string]string,
) (Resp, error) {
	var resp Resp
	var err error
	if len(body) > 0 {
		resp, err = h.DecodeResponse(body)
		if err != nil {
			return resp, err
		}
	}
	mergeFields := make([]codex.FieldCodec[Resp], 0, len(h.responseHeaderMergeFields)+len(h.responseCookieMergeFields))
	mergeFields = append(mergeFields, h.responseHeaderMergeFields...)
	mergeFields = append(mergeFields, h.responseCookieMergeFields...)
	if len(mergeFields) == 0 {
		return resp, nil
	}
	vars := make(map[string]string, len(headers)+len(cookies))
	for k, v := range headers {
		vars[k] = v
	}
	for k, v := range cookies {
		vars[k] = v
	}
	if err := codex.DecodeVars(&resp, vars, mergeFields...); err != nil {
		return resp, err
	}
	return resp, nil
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
	result, err := codex.BuildFromParams(h.Descriptor.Path, toCodexParams(h.pathParams), vars)
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
	return codex.ValidateParams(toCodexParams(h.pathParams), vars)
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
	// securitySchemes returns the route's own security scheme declarations
	// (from [WithSecurityScheme]) so [Builder.OpenAPISpec] can aggregate
	// components.securitySchemes across all registered routes — there is no
	// builder-level security scheme store to read from instead.
	securitySchemes() map[string]SecurityScheme
}

// typedRouteEntry stores a pointer to the RouteHandle so that With* mutations
// are visible to the builder at OpenAPISpec() time.
type typedRouteEntry[Req, Resp any] struct {
	handle *RouteHandle[Req, Resp]
}

func (e *typedRouteEntry[Req, Resp]) descriptor() route.Route { return e.handle.Descriptor }
func (e *typedRouteEntry[Req, Resp]) securitySchemes() map[string]SecurityScheme {
	return e.handle.SecuritySchemes
}

// typedSSEEntry stores a pointer to the SSERouteHandle so that With* mutations
// are visible to the builder at OpenAPISpec() time.
type typedSSEEntry[Req, Event any] struct {
	handle *SSERouteHandle[Req, Event]
}

func (e *typedSSEEntry[Req, Event]) descriptor() route.Route { return e.handle.Descriptor }
func (e *typedSSEEntry[Req, Event]) securitySchemes() map[string]SecurityScheme {
	return e.handle.SecuritySchemes
}

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
// value fails codec validation. A type ALIAS for [codex.ParamError] — the
// SAME underlying type, so existing errors.As(&rest.PathParamError{}) calls
// and generic instantiations (e.g. apimcp.ErrorPattern[rest.PathParamError, ...])
// keep working unchanged; see codex/param.go for the canonical definition.
//
// Use errors.As to extract the failing variable name and value:
//
//	var paramErr rest.PathParamError
//	if errors.As(err, &paramErr) {
//	    log.Printf("bad value for {%s}: %q — %v", paramErr.Name, paramErr.Value, paramErr.Err)
//	}
type PathParamError = codex.ParamError

// MissingPathVarError is returned by [RouteHandle.BuildPath] when a {varName}
// placeholder in the path template has no corresponding entry in the vars map.
// A type ALIAS for [codex.MissingParamError] — see [PathParamError]'s own
// doc comment for the rationale.
//
// Use errors.As to extract the missing variable name:
//
//	var missingErr rest.MissingPathVarError
//	if errors.As(err, &missingErr) {
//	    log.Printf("caller forgot to supply path variable {%s}", missingErr.Name)
//	}
type MissingPathVarError = codex.MissingParamError

// InvalidPathParamError is returned by [Route.Register] when a [PathParam] entry
// names a variable that does not appear in the path template. A type ALIAS
// for [codex.InvalidParamError] — see [PathParamError]'s own doc comment for
// the rationale. NOTE: the field is named Template (not Path) on the shared
// type.
//
// Use errors.As to extract the offending name and the path template:
//
//	var paramErr rest.InvalidPathParamError
//	if errors.As(err, &paramErr) {
//	    log.Printf("PathParam %q not in path %q", paramErr.Name, paramErr.Template)
//	}
type InvalidPathParamError = codex.InvalidParamError

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

// MergedQueryParam is returned by [NewRequiredQueryParam]/[NewOptionalQueryParam].
// It embeds the unchanged [QueryParam] plus a merge field, so one
// declaration serves both spec/validation and automatic merge — see
// [MergedPathParam] for the full rationale.
type MergedQueryParam[T any] struct {
	QueryParam
	field codex.FieldCodec[T]
}

// NewRequiredQueryParam declares a REQUIRED query parameter that is BOTH
// validated against codec AND automatically merged into Req by
// [RouteHandle.DecodeMerged]. See [NewPathParam] for the full rationale;
// this is the query-parameter equivalent, following [codex.RequiredField]'s
// naming convention.
func NewRequiredQueryParam[T any](
	name string,
	codec codex.Codec[string],
	get func(T) string,
	set func(*T, string),
) MergedQueryParam[T] {
	return MergedQueryParam[T]{
		QueryParam: QueryParam{Name: name, Codec: &codec, Required: true},
		field:      codex.RequiredField(name, codec, get, set),
	}
}

// NewOptionalQueryParam declares an OPTIONAL query parameter that is BOTH
// validated against codec (when present) AND automatically merged into Req
// by [RouteHandle.DecodeMerged] (when present — absent values leave the
// field untouched, following [codex.OptionalField]'s semantics).
func NewOptionalQueryParam[T any](
	name string,
	codec codex.Codec[string],
	get func(T) string,
	set func(*T, string),
) MergedQueryParam[T] {
	return MergedQueryParam[T]{
		QueryParam: QueryParam{Name: name, Codec: &codec, Required: false},
		field:      codex.OptionalField(name, codec, get, set),
	}
}

// WithDescription sets the PARAMETER-level description and returns the
// updated value, mirroring QueryParam.WithCodec's existing chain style.
func (q MergedQueryParam[T]) WithDescription(desc string) MergedQueryParam[T] {
	q.Description = desc
	return q
}

func (q MergedQueryParam[T]) applyRoute(rb *routeBuilder) {
	rb.queryParams = append(rb.queryParams, q.QueryParam)
	rb.queryMergeFields = append(rb.queryMergeFields, q.field)
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

func (cp CookieParam) applyRoute(rb *routeBuilder) { rb.cookieParams = append(rb.cookieParams, cp) }

// WithCodec sets the validation codec and returns the updated CookieParam.
func (cp CookieParam) WithCodec(c codex.Codec[string]) CookieParam { cp.Codec = &c; return cp }

// MergedCookieParam is returned by [NewRequiredCookieParam]/[NewOptionalCookieParam].
// See [MergedPathParam] for the full rationale.
type MergedCookieParam[T any] struct {
	CookieParam
	field codex.FieldCodec[T]
}

// NewRequiredCookieParam declares a REQUIRED cookie parameter that is BOTH
// validated against codec AND automatically merged into Req by
// [RouteHandle.DecodeMerged]. See [NewPathParam] for the full rationale.
func NewRequiredCookieParam[T any](
	name string,
	codec codex.Codec[string],
	get func(T) string,
	set func(*T, string),
) MergedCookieParam[T] {
	return MergedCookieParam[T]{
		CookieParam: CookieParam{Name: name, Codec: &codec, Required: true},
		field:       codex.RequiredField(name, codec, get, set),
	}
}

// NewOptionalCookieParam declares an OPTIONAL cookie parameter that is BOTH
// validated against codec (when present) AND automatically merged into Req
// (when present) by [RouteHandle.DecodeMerged].
func NewOptionalCookieParam[T any](
	name string,
	codec codex.Codec[string],
	get func(T) string,
	set func(*T, string),
) MergedCookieParam[T] {
	return MergedCookieParam[T]{
		CookieParam: CookieParam{Name: name, Codec: &codec, Required: false},
		field:       codex.OptionalField(name, codec, get, set),
	}
}

// WithDescription sets the PARAMETER-level description and returns the
// updated value.
func (cp MergedCookieParam[T]) WithDescription(desc string) MergedCookieParam[T] {
	cp.Description = desc
	return cp
}

func (cp MergedCookieParam[T]) applyRoute(rb *routeBuilder) {
	rb.cookieParams = append(rb.cookieParams, cp.CookieParam)
	rb.cookieMergeFields = append(rb.cookieMergeFields, cp.field)
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

// MergedHeaderParam is returned by [NewRequiredHeaderParam]/[NewOptionalHeaderParam].
// See [MergedPathParam] for the full rationale.
type MergedHeaderParam[T any] struct {
	HeaderParam
	field codex.FieldCodec[T]
}

// NewRequiredHeaderParam declares a REQUIRED header parameter that is BOTH
// validated against codec AND automatically merged into Req by
// [RouteHandle.DecodeMerged]. See [NewPathParam] for the full rationale.
func NewRequiredHeaderParam[T any](
	name string,
	codec codex.Codec[string],
	get func(T) string,
	set func(*T, string),
) MergedHeaderParam[T] {
	return MergedHeaderParam[T]{
		HeaderParam: HeaderParam{Name: name, Codec: &codec, Required: true},
		field:       codex.RequiredField(name, codec, get, set),
	}
}

// NewOptionalHeaderParam declares an OPTIONAL header parameter that is BOTH
// validated against codec (when present) AND automatically merged into Req
// (when present) by [RouteHandle.DecodeMerged].
func NewOptionalHeaderParam[T any](
	name string,
	codec codex.Codec[string],
	get func(T) string,
	set func(*T, string),
) MergedHeaderParam[T] {
	return MergedHeaderParam[T]{
		HeaderParam: HeaderParam{Name: name, Codec: &codec, Required: false},
		field:       codex.OptionalField(name, codec, get, set),
	}
}

// WithDescription sets the PARAMETER-level description and returns the
// updated value.
func (h MergedHeaderParam[T]) WithDescription(desc string) MergedHeaderParam[T] {
	h.Description = desc
	return h
}

func (h MergedHeaderParam[T]) applyRoute(rb *routeBuilder) {
	rb.headerParams = append(rb.headerParams, h.HeaderParam)
	rb.headerMergeFields = append(rb.headerMergeFields, h.field)
}

// MergedSSEEventParam is returned by [NewRequiredSSEEventParam]/
// [NewOptionalSSEEventParam]. It declares a connection-level variable merge
// for SSE events: each sent Event can be stamped with request-derived vars
// (path/query/header/cookie) automatically.
//
// Unlike NewPathParam/NewRequiredQueryParam/etc., this option does NOT
// register request parameters in the OpenAPI route spec; it only registers a
// merge field for the pushed Event payload.
type MergedSSEEventParam[T any] struct {
	Name        string
	Description string
	Required    bool
	Codec       *codex.Codec[string]
	field       codex.FieldCodec[T]
}

// NewRequiredSSEEventParam declares a REQUIRED connection variable that is
// merged into each pushed SSE event.
func NewRequiredSSEEventParam[T any](
	name string,
	codec codex.Codec[string],
	get func(T) string,
	set func(*T, string),
) MergedSSEEventParam[T] {
	return MergedSSEEventParam[T]{
		Name:     name,
		Required: true,
		Codec:    &codec,
		field:    codex.RequiredField(name, codec, get, set),
	}
}

// NewOptionalSSEEventParam declares an OPTIONAL connection variable that is
// merged into each pushed SSE event when present.
func NewOptionalSSEEventParam[T any](
	name string,
	codec codex.Codec[string],
	get func(T) string,
	set func(*T, string),
) MergedSSEEventParam[T] {
	return MergedSSEEventParam[T]{
		Name:     name,
		Required: false,
		Codec:    &codec,
		field:    codex.OptionalField(name, codec, get, set),
	}
}

// WithDescription sets the merge-field description and returns the updated
// value.
func (p MergedSSEEventParam[T]) WithDescription(desc string) MergedSSEEventParam[T] {
	p.Description = desc
	return p
}

func (p MergedSSEEventParam[T]) applyRoute(rb *routeBuilder) {
	rb.sseEventMergeFields = append(rb.sseEventMergeFields, p.field)
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

// MergedResponseHeaderParam is returned by
// [NewRequiredResponseHeaderParam]/[NewOptionalResponseHeaderParam]. It is
// the RESPONSE-direction mirror of [MergedHeaderParam]: on the server, the
// registered field's getter extracts the header value from the handler's
// returned Resp and the adapter sets it on the HTTP response automatically
// (no manual [ResponseHeadersFromContext] call needed); on the client, the
// field's setter merges the HTTP response header back into the decoded Resp
// via [RouteHandle.DecodeMergedResponse]. See [MergedPathParam] for the
// full request-direction rationale — this type applies the identical
// pattern to the response side.
type MergedResponseHeaderParam[Resp any] struct {
	ResponseHeaderParam
	field codex.FieldCodec[Resp]
}

// NewRequiredResponseHeaderParam declares a REQUIRED response header that is
// BOTH validated against codec AND automatically merged: encoded from Resp
// on the server (via the adapter, using get), and merged into Resp on the
// client (via [RouteHandle.DecodeMergedResponse], using set). "Required"
// governs the DECODE direction only — the client treats a missing header as
// a merge failure; the server always encodes it (get is always called).
func NewRequiredResponseHeaderParam[Resp any](
	name string,
	codec codex.Codec[string],
	get func(Resp) string,
	set func(*Resp, string),
) MergedResponseHeaderParam[Resp] {
	return MergedResponseHeaderParam[Resp]{
		ResponseHeaderParam: ResponseHeaderParam{Name: name, Codec: &codec, Required: true},
		field:               codex.RequiredField(name, codec, get, set),
	}
}

// NewOptionalResponseHeaderParam declares an OPTIONAL response header that is
// BOTH validated against codec (when present) AND automatically merged
// (when present), for both the server encode and client decode directions.
func NewOptionalResponseHeaderParam[Resp any](
	name string,
	codec codex.Codec[string],
	get func(Resp) string,
	set func(*Resp, string),
) MergedResponseHeaderParam[Resp] {
	return MergedResponseHeaderParam[Resp]{
		ResponseHeaderParam: ResponseHeaderParam{Name: name, Codec: &codec, Required: false},
		field:               codex.OptionalField(name, codec, get, set),
	}
}

// WithDescription sets the PARAMETER-level description and returns the
// updated value.
func (p MergedResponseHeaderParam[Resp]) WithDescription(desc string) MergedResponseHeaderParam[Resp] {
	p.Description = desc
	return p
}

func (p MergedResponseHeaderParam[Resp]) applyRoute(rb *routeBuilder) {
	rb.respHeaders = append(rb.respHeaders, p.ResponseHeaderParam)
	rb.responseHeaderMergeFields = append(rb.responseHeaderMergeFields, p.field)
}

// MergedResponseCookieParam is returned by
// [NewRequiredResponseCookieParam]/[NewOptionalResponseCookieParam]. See
// [MergedResponseHeaderParam] for the full rationale — this is the
// Set-Cookie equivalent.
type MergedResponseCookieParam[Resp any] struct {
	ResponseCookieParam
	field codex.FieldCodec[Resp]
}

// NewRequiredResponseCookieParam declares a REQUIRED response cookie that is
// BOTH validated against codec AND automatically merged: encoded from Resp
// on the server, merged into Resp on the client via
// [RouteHandle.DecodeMergedResponse]. Merge-derived cookies get default
// [PendingCookie.Opts] (no Path/Secure/SameSite override) — use
// [ResponseHeadersFromContext]'s cookie helper directly for custom cookie
// attributes.
func NewRequiredResponseCookieParam[Resp any](
	name string,
	codec codex.Codec[string],
	get func(Resp) string,
	set func(*Resp, string),
) MergedResponseCookieParam[Resp] {
	return MergedResponseCookieParam[Resp]{
		ResponseCookieParam: ResponseCookieParam{Name: name, Codec: &codec, Required: true},
		field:               codex.RequiredField(name, codec, get, set),
	}
}

// NewOptionalResponseCookieParam declares an OPTIONAL response cookie that is
// BOTH validated against codec (when present) AND automatically merged
// (when present), for both the server encode and client decode directions.
func NewOptionalResponseCookieParam[Resp any](
	name string,
	codec codex.Codec[string],
	get func(Resp) string,
	set func(*Resp, string),
) MergedResponseCookieParam[Resp] {
	return MergedResponseCookieParam[Resp]{
		ResponseCookieParam: ResponseCookieParam{Name: name, Codec: &codec, Required: false},
		field:               codex.OptionalField(name, codec, get, set),
	}
}

// WithDescription sets the PARAMETER-level description and returns the
// updated value.
func (p MergedResponseCookieParam[Resp]) WithDescription(desc string) MergedResponseCookieParam[Resp] {
	p.Description = desc
	return p
}

func (p MergedResponseCookieParam[Resp]) applyRoute(rb *routeBuilder) {
	rb.respCookies = append(rb.respCookies, p.ResponseCookieParam)
	rb.responseCookieMergeFields = append(rb.responseCookieMergeFields, p.field)
}

// SecurityScheme combines [route.SecurityScheme] spec metadata with optional
// runtime credential extraction and format validation.
//
// [WithSecurityScheme] declares a SecurityScheme on a route — the ONLY way to
// declare one; there is no builder-level equivalent. The spec fields flow
// into the OpenAPI document (aggregated from all registered routes by
// [Builder.OpenAPISpec]); Codec, when non-nil, is used by adapters to
// validate the raw credential string before SecurityFunc is called
// (server-side) or before the request is sent (client-side, [nethttp.Call]).
//
// The adapter extracts the raw credential from the request based on the scheme
// Type and location fields:
//   - http bearer / openIdConnect / oauth2: strips "Bearer " from the Authorization header
//   - http basic: strips "Basic " from the Authorization header
//   - apiKey: reads from the header / query / cookie named Name according to In
//
// A codec validation failure causes the adapter to return a [SecurityCredentialError]
// with HTTP 401 (server), or the same error type client-side before any
// network call is sent, without invoking SecurityFunc.
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
//	rest.WithSecurityScheme("bearer", rest.SecurityScheme{
//	    SecurityScheme: route.BearerScheme("JWT"),
//	}.WithCodec(codex.String().Refine(validate.BearerToken)))
func (s SecurityScheme) WithCodec(c codex.Codec[string]) SecurityScheme {
	s.Codec = &c
	return s
}

// securitySchemeOpt is the [RouteOpt] returned by [WithSecurityScheme].
type securitySchemeOpt struct {
	name   string
	scheme SecurityScheme
}

func (o securitySchemeOpt) applyRoute(rb *routeBuilder) {
	if rb.securitySchemes == nil {
		rb.securitySchemes = make(map[string]SecurityScheme, 1)
	}
	rb.securitySchemes[o.name] = o.scheme
}

// WithSecurityScheme declares scheme's spec metadata and optional Codec for
// THIS route. It is the ONLY way to declare a security scheme in go-codex —
// there is no builder-level equivalent. Both [Route.Register] and
// [Route.ClientHandle] populate [RouteHandle.SecuritySchemes] from this
// declaration, so the SAME route value — including its security scheme —
// builds a server-side handle (Register) and a client-side handle
// (ClientHandle) with IDENTICAL credential-format enforcement on both sides.
//
// Define a scheme once as a package-level value and reuse it across every
// route that shares it — Go's ordinary "declare once, reference everywhere"
// idiom, no builder-level registry needed:
//
//	var bearerAuth = rest.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")}.
//	    WithCodec(codex.String().Refine(validate.BearerToken))
//
//	var GetTagsRoute = rest.NewRoute[GetTagsReq, TagsList](
//	    "GET", "/v2/{name}/tags/list",
//	    c.Struct[GetTagsReq](), TagsListCodec,
//	    rest.RouteMeta{Security: bearerAuthSecurity},
//	    rest.WithSecurityScheme("bearerAuth", bearerAuth),
//	    rest.NewPathParam("name", ...),
//	)
//
// When multiple routes declare the SAME scheme name with DIFFERENT values,
// [Builder.OpenAPISpec] resolves the conflict last-registered-wins (no
// error) — define the scheme once as a shared value (as above) to avoid
// this entirely.
func WithSecurityScheme(name string, scheme SecurityScheme) RouteOpt {
	return securitySchemeOpt{name: name, scheme: scheme}
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

// Builder accumulates route registrations, and produces OpenAPI 3.1
// specifications. Security schemes are declared per-route via
// [WithSecurityScheme] (there is no builder-level equivalent) — see
// [Builder.OpenAPISpec] for how they're aggregated into the spec. It is safe
// to register routes from multiple goroutines as long as [Builder.Build] is
// not called concurrently. Create one with [NewBuilder].
type Builder struct {
	info           Info
	servers        []Server
	entries        []routeEntry
	schemas        map[string]schema.Schema
	pathCodec      *codex.Codec[string]
	globalSecurity []route.SecurityRequirement
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
		info:    info,
		schemas: make(map[string]schema.Schema),
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
// Path bundles a path template with its declared [PathParam] variables —
// the Req/Resp-independent "shape" of a route's path (the SAME state
// [RouteHandle.BuildPath]/[RouteHandle.ValidatePathParams] already use
// internally, extracted into its own value). Mirrors
// [github.com/DaniDeer/go-codex/api/events.Topic] exactly, one boundary over.
//
// The plain-string form remains the default and primary way to declare a
// route — pass a path template string directly to [NewRoute], exactly as
// always. Reach for Path ONLY when you find yourself declaring the SAME
// template+params shape for two or more routes (of different Req/Resp
// types — e.g. GET and DELETE on the same resource path) and want that
// shape to have exactly one source of truth, or when you need to
// build/validate a path standalone, with no request/response codec
// involved at all.
//
// A route declared via [NewRouteFromPath] is byte-for-byte identical to one
// declared via [NewRoute] with the same template and [PathParam] values
// passed inline — nothing downstream can tell the difference. Path
// captures ONLY the template+params shape; every other [RouteOpt] is
// passed to [NewRouteFromPath] exactly as it would be to [NewRoute].
type Path struct {
	// Template is the path template, e.g. "/users/{id}".
	Template string
	// Params holds the path template's variable declarations.
	Params []PathParam
}

// NewPath declares a Path from a template and its PathParam variables.
func NewPath(template string, params ...PathParam) Path {
	return Path{Template: template, Params: params}
}

// BuildPath substitutes {varName} placeholders in p.Template with the
// values in vars, validating each against its registered [PathParam.Codec]
// (if any). Mirrors [RouteHandle.BuildPath] exactly (same underlying
// engine, same error types), MINUS any builder-level path codec — that
// only applies once a Path-based route is registered via
// [NewRouteFromPath] + [Route.Register], where it is enforced exactly as
// it would be for a plain-string route.
func (p Path) BuildPath(vars map[string]string) (string, error) {
	return codex.BuildFromParams(p.Template, toCodexParams(p.Params), vars)
}

// ValidatePathParams validates path variable values against p's registered
// [PathParam] codecs. Mirrors [RouteHandle.ValidatePathParams] exactly
// (same error types); variables without a registered codec are skipped.
func (p Path) ValidatePathParams(vars map[string]string) error {
	return codex.ValidateParams(toCodexParams(p.Params), vars)
}

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

// NewRouteFromPath declares a Route using a pre-built [Path] instead of a
// raw path-template string — see [Path]'s doc comment for when to reach
// for this. Produces the IDENTICAL [Route] [NewRoute] would produce from
// path.Template plus path.Params passed inline, since [PathParam] already
// implements [RouteOpt].
func NewRouteFromPath[Req, Resp any](
	method string,
	path Path,
	reqCodec codex.Codec[Req],
	respCodec codex.Codec[Resp],
	opts ...RouteOpt,
) Route[Req, Resp] {
	allOpts := make([]RouteOpt, 0, len(path.Params)+len(opts))
	for _, p := range path.Params {
		allOpts = append(allOpts, p)
	}
	allOpts = append(allOpts, opts...)
	return NewRoute(method, path.Template, reqCodec, respCodec, allOpts...)
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

	if err := codex.ValidateDeclaredParams(r.path, toCodexParams(rb.pathParams)); err != nil {
		return nil, err
	}

	frozen := buildDescriptor(r.method, r.path, r.reqCodec.Schema, r.respCodec.Schema, rb, nil)

	jsonReq := format.JSON(r.reqCodec)
	jsonResp := format.JSON(r.respCodec)

	h := &RouteHandle[Req, Resp]{
		Descriptor:           frozen,
		Decode:               func(body []byte) (Req, error) { return jsonReq.Unmarshal(body) },
		Encode:               func(resp Resp) ([]byte, error) { return jsonResp.Marshal(resp) },
		EncodeRequest:        func(req Req) ([]byte, error) { return jsonReq.Marshal(req) },
		DecodeResponse:       func(body []byte) (Resp, error) { return jsonResp.Unmarshal(body) },
		pathParams:           rb.pathParams,
		queryParams:          rb.queryParams,
		cookieParams:         rb.cookieParams,
		headerParams:         rb.headerParams,
		responseHeaderParams: rb.respHeaders,
		responseCookieParams: rb.respCookies,
		pathCodec:            b.pathCodec,
		SecuritySchemes:      rb.securitySchemes,
		GlobalSecurity:       slices.Clone(b.globalSecurity),
		errorStatusRules:     slices.Clone(rb.errorStatusRules),
		errorPatternRules:    slices.Clone(rb.errorPatternRules),
	}
	if rb.requestFormats != nil {
		fmts, ok := rb.requestFormats.([]format.Format[Req])
		if !ok {
			return nil, FormatOptError{Direction: "request",
				Err: fmt.Errorf("want []format.Format[%T], got %T", *new(Req), rb.requestFormats)}
		}
		h.WithRequestFormats(fmts...)
	}
	if rb.respFormats != nil {
		fmts, ok := rb.respFormats.([]format.Format[Resp])
		if !ok {
			return nil, FormatOptError{Direction: "response",
				Err: fmt.Errorf("want []format.Format[%T], got %T", *new(Resp), rb.respFormats)}
		}
		h.WithFormats(fmts...)
	}
	var err error
	h.pathMergeFields, err = assertMergeFields[Req](rb.pathMergeFields)
	if err != nil {
		return nil, err
	}
	h.queryMergeFields, err = assertMergeFields[Req](rb.queryMergeFields)
	if err != nil {
		return nil, err
	}
	h.headerMergeFields, err = assertMergeFields[Req](rb.headerMergeFields)
	if err != nil {
		return nil, err
	}
	h.cookieMergeFields, err = assertMergeFields[Req](rb.cookieMergeFields)
	if err != nil {
		return nil, err
	}
	h.responseHeaderMergeFields, err = assertMergeFields[Resp](rb.responseHeaderMergeFields)
	if err != nil {
		return nil, err
	}
	h.responseCookieMergeFields, err = assertMergeFields[Resp](rb.responseCookieMergeFields)
	if err != nil {
		return nil, err
	}

	entry := &typedRouteEntry[Req, Resp]{handle: h}
	b.entries = append(b.entries, entry)
	return h, nil
}

// ClientHandle returns a [RouteHandle] for client-side use without registering
// with a [Builder]. No path codec validation and no spec registration occur.
//
// Use ClientHandle when only the client side needs codec and route definitions
// (no OpenAPI spec, no server), or when sharing a [Route] definition between
// server and client in the same binary.
//
// The returned handle has the same Decode / Encode / EncodeRequest / DecodeResponse
// codec helpers and the same parameter validation methods as a handle returned
// by [Route.Register] — including [RouteHandle.SecuritySchemes], populated from
// the route's own [WithSecurityScheme] declarations (there is no [Builder]
// involved in this path, so builder-level state never applies here — route-level
// declarations are the only source). [adapters/nethttp.Call] uses it to validate
// a [nethttp.CallOptions.CredentialFunc]'s returned credential format before
// sending, symmetric with the server-side check [Route.Register] enables.
//
// Example — client-only usage:
//
//	var getUser = rest.NewRoute[GetUserReq, User]("GET", "/users/{id}",
//	    getUserReqCodec, userCodec,
//	    rest.PathParam{Name: "id"}.WithCodec(uuidCodec),
//	)
//
//	handle := getUser.ClientHandle()
//	user, err := nethttp.Call(ctx, http.DefaultClient, "https://api.example.com",
//	    handle, GetUserReq{}, map[string]string{"id": userID}, nethttp.CallOptions{})
func (r Route[Req, Resp]) ClientHandle() *RouteHandle[Req, Resp] {
	var rb routeBuilder
	for _, opt := range r.opts {
		opt.applyRoute(&rb)
	}

	frozen := buildDescriptor(r.method, r.path, r.reqCodec.Schema, r.respCodec.Schema, rb, nil)

	jsonReq := format.JSON(r.reqCodec)
	jsonResp := format.JSON(r.respCodec)

	return &RouteHandle[Req, Resp]{
		Descriptor:                frozen,
		Decode:                    func(body []byte) (Req, error) { return jsonReq.Unmarshal(body) },
		Encode:                    func(resp Resp) ([]byte, error) { return jsonResp.Marshal(resp) },
		EncodeRequest:             func(req Req) ([]byte, error) { return jsonReq.Marshal(req) },
		DecodeResponse:            func(body []byte) (Resp, error) { return jsonResp.Unmarshal(body) },
		pathParams:                rb.pathParams,
		queryParams:               rb.queryParams,
		cookieParams:              rb.cookieParams,
		headerParams:              rb.headerParams,
		errorStatusRules:          slices.Clone(rb.errorStatusRules),
		errorPatternRules:         slices.Clone(rb.errorPatternRules),
		pathMergeFields:           mustAssertMergeFields[Req]("ClientHandle", rb.pathMergeFields),
		queryMergeFields:          mustAssertMergeFields[Req]("ClientHandle", rb.queryMergeFields),
		headerMergeFields:         mustAssertMergeFields[Req]("ClientHandle", rb.headerMergeFields),
		cookieMergeFields:         mustAssertMergeFields[Req]("ClientHandle", rb.cookieMergeFields),
		responseHeaderMergeFields: mustAssertMergeFields[Resp]("ClientHandle", rb.responseHeaderMergeFields),
		responseCookieMergeFields: mustAssertMergeFields[Resp]("ClientHandle", rb.responseCookieMergeFields),
		SecuritySchemes:           rb.securitySchemes,
	}
}

// assertMergeFields type-asserts each element of raw (declared as []any on
// routeBuilder to keep the builder non-generic) against
// codex.FieldCodec[Req]. Returns MergeFieldTypeError on the first mismatch —
// a caller programming error (mixing a merge field built for one Req type
// into a Route declared with another).
func assertMergeFields[Req any](raw []any) ([]codex.FieldCodec[Req], error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make([]codex.FieldCodec[Req], len(raw))
	for i, mf := range raw {
		fc, ok := mf.(codex.FieldCodec[Req])
		if !ok {
			return nil, MergeFieldTypeError{
				Err: fmt.Errorf("want codex.FieldCodec[%T], got %T", *new(Req), mf)}
		}
		out[i] = fc
	}
	return out, nil
}

// mustAssertMergeFields is assertMergeFields for infallible callers
// (ClientHandle has no error return) — panics on a type mismatch, same
// class as ports.NewFile's panic-on-misuse precedent.
func mustAssertMergeFields[Req any](caller string, raw []any) []codex.FieldCodec[Req] {
	fields, err := assertMergeFields[Req](raw)
	if err != nil {
		panic(fmt.Sprintf("api/rest: %s: %s", caller, err.Error()))
	}
	return fields
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
	// Populated from the route's own [WithSecurityScheme] declarations —
	// this is the ONLY way to declare a security scheme; there is no
	// builder-level equivalent. Both [Route.Register] and
	// [Route.ClientHandle] populate this field identically, so the SAME
	// route value builds a server-side handle and a client-side handle
	// with IDENTICAL credential-format enforcement on both sides: the
	// server adapter's Handler validates an INCOMING credential against
	// Codec before calling SecurityFunc; [nethttp.Call] validates an
	// OUTGOING credential (the header CredentialFunc returned) against the
	// SAME Codec before sending.
	SecuritySchemes map[string]SecurityScheme

	// GlobalSecurity holds the builder-level security requirements that apply
	// when Descriptor.Security is nil (i.e. the route inherits global security).
	// Adapters resolve the effective requirements as:
	//   reqs := handle.Descriptor.Security
	//   if reqs == nil { reqs = handle.GlobalSecurity }
	// Set via [Builder.AddGlobalSecurity]. nil when no global security is declared.
	// Unlike SecuritySchemes, GlobalSecurity remains builder-only — it answers
	// "which routes require auth by default" (spec-wide), not "what does a
	// scheme look like" — and has no [Route.ClientHandle] equivalent (always
	// nil there, unchanged).
	GlobalSecurity []route.SecurityRequirement

	// responseHeaderParams holds per-header entries registered via ResponseHeaderParam options.
	responseHeaderParams []ResponseHeaderParam

	// responseCookieParams holds per-cookie entries registered via ResponseCookieParam options.
	responseCookieParams []ResponseCookieParam

	// mergeFields holds SSE event merge fields registered via
	// NewRequiredSSEEventParam/NewOptionalSSEEventParam.
	mergeFields []codex.FieldCodec[Event]
}

// BuildPath substitutes {varName} placeholders in the route's path template
// with the values provided in vars, validating each against its registered
// codec (if any). Follows the same contract as [RouteHandle.BuildPath].
func (h *SSERouteHandle[Req, Event]) BuildPath(vars map[string]string) (string, error) {
	result, err := codex.BuildFromParams(h.Descriptor.Path, toCodexParams(h.pathParams), vars)
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

// MergeFields returns the merge-capable fields registered via
// NewRequiredSSEEventParam/NewOptionalSSEEventParam.
func (h *SSERouteHandle[Req, Event]) MergeFields() []codex.FieldCodec[Event] {
	return h.mergeFields
}

// MergeEvent merges request-derived vars into one SSE event value using the
// fields registered by NewRequiredSSEEventParam/NewOptionalSSEEventParam.
// pathVars/query/headers/cookies are merged in that order; later maps override
// earlier keys with the same name.
func (h *SSERouteHandle[Req, Event]) MergeEvent(
	ev Event,
	pathVars map[string]string,
	query map[string]string,
	headers map[string]string,
	cookies map[string]string,
) (Event, error) {
	if len(h.mergeFields) == 0 {
		return ev, nil
	}
	vars := make(map[string]string, len(pathVars)+len(query)+len(headers)+len(cookies))
	for k, v := range pathVars {
		vars[k] = v
	}
	for k, v := range query {
		vars[k] = v
	}
	for k, v := range headers {
		vars[k] = v
	}
	for k, v := range cookies {
		vars[k] = v
	}
	if err := codex.DecodeVars(&ev, vars, h.mergeFields...); err != nil {
		return ev, err
	}
	return ev, nil
}

// ValidatePathParams validates path variable values against their registered codecs.
// Mirrors [RouteHandle.ValidatePathParams] for SSE routes.
func (h *SSERouteHandle[Req, Event]) ValidatePathParams(vars map[string]string) error {
	return codex.ValidateParams(toCodexParams(h.pathParams), vars)
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
	eventMergeFields, err := assertMergeFields[Event](rb.sseEventMergeFields)
	if err != nil {
		return nil, err
	}

	if err := codex.ValidateDeclaredParams(s.path, toCodexParams(rb.pathParams)); err != nil {
		return nil, err
	}

	frozen := buildDescriptor("GET", s.path, s.reqCodec.Schema, s.eventCodec.Schema, rb, []string{"text/event-stream"})

	jsonReq := format.JSON(s.reqCodec)
	jsonEvent := format.JSON(s.eventCodec)

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
		SecuritySchemes:      rb.securitySchemes,
		GlobalSecurity:       slices.Clone(b.globalSecurity),
		responseHeaderParams: rb.respHeaders,
		responseCookieParams: rb.respCookies,
		mergeFields:          eventMergeFields,
	}
	entry := &typedSSEEntry[Req, Event]{handle: h}
	b.entries = append(b.entries, entry)
	return h, nil
}

// OpenAPISpec builds a complete OpenAPI 3.1 document from all registered routes.
// Returns an error if any non-empty SchemaName references a schema that will not
// be present in components/schemas (a dangling $ref).
//
// components.securitySchemes is aggregated from every registered route's own
// [WithSecurityScheme] declarations (there is no builder-level security scheme
// store) — when two routes declare the same scheme name with different values,
// the LAST-registered route wins, with no error; define the scheme once as a
// shared package-level value (see [WithSecurityScheme]'s example) to avoid
// relying on this.
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
	schemes := make(map[string]SecurityScheme)
	for _, e := range b.entries {
		for name, s := range e.securitySchemes() {
			schemes[name] = s
		}
	}
	for name, s := range schemes {
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
