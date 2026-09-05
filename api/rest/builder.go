package rest

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/DaniDeer/go-codex/api/internal"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/render/openapi"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/schema"
)

// Info is an alias for [openapi.Info]. Using the alias avoids duplicating
// fields and keeps the two in sync automatically.
type Info = openapi.Info

// ServerEntry is an alias for [openapi.Server] — a single OpenAPI server URL
// entry (see [Server.AddServer]). Named ServerEntry, not Server, to avoid
// colliding with [Server] itself (the renamed api-level builder/registry
// type, mirroring [events.Client]'s own domain-level design).
type ServerEntry = openapi.Server

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
//
// V need not be string — see [codex.NewParam] for merging a path segment
// directly into an int/UUID/etc. via [codex.IntString]/[codex.TextCodec]/
// [codex.StringCodec].
func NewPathParam[T, V any](
	name string,
	codec codex.Codec[V],
	get func(T) V,
	set func(*T, V),
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
	// nil (default) inherits global security declared via Server.AddGlobalSecurity.
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
	// [Route.Use] (via [middleware.SecurityScheme]/[FromSecurityScheme]) —
	// the ONLY source of RouteHandle.SecuritySchemes; there is no
	// builder-level equivalent. Consumed identically by [Route.Register]
	// and [Route.ClientHandle].
	securitySchemes map[string]SecurityScheme

	// middlewares holds every [middleware.Middleware] attached via
	// [WithMiddleware], in attachment order. Security/RequestParams/
	// ResponseParams contributions are NOT applied here — they are applied
	// ONCE, order-independently, by [applyMiddlewareDeclarations] at
	// Register/ValidateRoute time (see "L1"/"L3" in
	// docs/roadmap/declarative-middleware.md).
	middlewares []middleware.Middleware

	// handlerFn holds the type-erased business handler attached via
	// [Route.WithHandler]/[SSERoute.WithHandler]. Resolved to the
	// concrete func(ctx, req) (Resp, error) (or the SSE handler shape) at
	// Register/Serve time.
	handlerFn any

	// handlerOpts holds the type-erased adapter Options value attached
	// via [Route.WithOptions]/[SSERoute.WithOptions]. Type-erased to
	// avoid api/rest importing any adapters/* package; the consuming
	// adapter (nethttp/chi) type-asserts it to its own Options type at
	// Serve time, returning OptionsShapeError on mismatch.
	handlerOpts any

	// impls holds every [middleware.ServerImplementation] attached via
	// [Route.HandleMW]/[SSERoute.HandleMW], in attachment order — built
	// internally by HandleMW from whatever mw/fn it receives.
	impls []middleware.ServerImplementation

	// clientImpls holds every [middleware.ClientImplementation] attached
	// via [Route.ClientMW], in attachment order — built internally by
	// ClientMW.
	clientImpls []middleware.ClientImplementation
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
	// Populated from the route's own [Route.Use] declarations (via
	// [middleware.SecurityScheme]/[FromSecurityScheme]) — this is the ONLY
	// way to declare a security scheme; there is no builder-level
	// equivalent. Both [Route.Register] and
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
	// Set via [Server.AddGlobalSecurity]. nil when no global security is declared.
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

	// Middlewares holds every [middleware.Middleware] attached via
	// [WithMiddleware]/[Route.Use], in attachment order — SERVER-side
	// declarations. Populated by both [Route.Register]/[Route.RegisterHandle]
	// and [Route.ClientHandle].
	Middlewares []middleware.Middleware

	// HandlerFn holds the type-erased business handler attached via
	// [Route.WithHandler]/[SSERoute.WithHandler] — nil if never called
	// (the route is spec-only; see [Serve]'s Part-1 gating rule). Resolved
	// to the concrete handler shape by the consuming adapter.
	HandlerFn any

	// HandlerOpts holds the type-erased adapter Options value attached via
	// [Route.WithOptions]/[SSERoute.WithOptions] — nil if never called
	// (the adapter uses its own zero-value Options).
	HandlerOpts any

	// Implementations holds every [middleware.ServerImplementation]
	// attached via [Route.HandleMW]/[SSERoute.HandleMW], in attachment
	// order — the runtime counterpart to Middlewares, built internally by
	// HandleMW.
	Implementations []middleware.ServerImplementation

	// ClientImplementations holds every [middleware.ClientImplementation]
	// attached via [Route.ClientMW], in attachment order — CLIENT-side
	// runtime fulfillment, built internally by ClientMW. Populated by
	// BOTH [Route.Register]/[Route.RegisterHandle] and
	// [Route.ClientHandle].
	ClientImplementations []middleware.ClientImplementation
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

// EncodeVars derives the path-var map from req using [RouteHandle.PathMergeFields]
// — the reflectable, MONOMORPHIZED-METHOD counterpart to calling
// [codex.EncodeVars] directly (which a reflection-based caller like
// [Client.Call]/[Client.Consume]'s adapter shim CANNOT do: Go forbids
// reflecting a generic FREE function for a runtime-only Req, but CAN call
// an exported METHOD already monomorphized for a concrete Req at compile
// time — see docs/design/d-0001-rest-middleware-workflow-simplification.md's Addendum 4). Mirrors
// [events.ChannelHandle.EncodeVars] exactly. A route with no path merge
// fields returns an empty, non-nil map.
func (h *RouteHandle[Req, Resp]) EncodeVars(req Req) (map[string]string, error) {
	return codex.EncodeVars(req, h.pathMergeFields...)
}

// EncodeQueryVars is [EncodeVars]'s query-param sibling, deriving from
// [RouteHandle.QueryMergeFields].
func (h *RouteHandle[Req, Resp]) EncodeQueryVars(req Req) (map[string]string, error) {
	return codex.EncodeVars(req, h.queryMergeFields...)
}

// EncodeHeaderVars is [EncodeVars]'s header-param sibling, deriving from
// [RouteHandle.HeaderMergeFields].
func (h *RouteHandle[Req, Resp]) EncodeHeaderVars(req Req) (map[string]string, error) {
	return codex.EncodeVars(req, h.headerMergeFields...)
}

// EncodeCookieVars is [EncodeVars]'s cookie-param sibling, deriving from
// [RouteHandle.CookieMergeFields].
func (h *RouteHandle[Req, Resp]) EncodeCookieVars(req Req) (map[string]string, error) {
	return codex.EncodeVars(req, h.cookieMergeFields...)
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
	if err := h.ApplyMergeFields(&req, pathVars, query, headers, cookies); err != nil {
		return req, err
	}
	return req, nil
}

// ApplyMergeFields merges pathVars/query/headers/cookies into an
// ALREADY-DECODED req value via [MergeFields] + [codex.DecodeVars] — the
// var-merge half of [DecodeMerged], split out so callers that decode the
// body via a negotiated [format.Format] (i.e. [WithRequestFormats], not
// plain [Decode]) can still apply merge-capable params afterward. Used by
// each adapter's internal serve dispatch (invoked via
// [nethttp.AttachMux]/[chi.AttachRouter]) for multi-format routes; a
// no-op when the route declares no merge-capable params.
func (h *RouteHandle[Req, Resp]) ApplyMergeFields(
	req *Req,
	pathVars, query, headers, cookies map[string]string,
) error {
	mergeFields := h.MergeFields()
	if len(mergeFields) == 0 {
		return nil
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
	return codex.DecodeVars(req, vars, mergeFields...)
}

// EncodeMerged encodes resp's body (via [RouteHandle.Encode]) AND derives
// its response header/cookie values (via [ResponseHeaderMergeFields]/
// [ResponseCookieMergeFields] + [codex.EncodeVars]) in ONE call — the
// SERVER-side, response-direction mirror of [DecodeMerged]. Used by
// each adapter's internal serve dispatch (invoked via
// [nethttp.AttachMux]/[chi.AttachRouter]) via a single reflect call,
// since Resp is erased at THAT call site — see
// docs/design/d-0001-rest-middleware-workflow-simplification.md's "Decision: Serve's
// generic dispatch mechanism") so the adapter never needs its own
// Resp-typed encode/merge logic. Behaves identically to a bare Encode
// when the route declares no response merge-capable params (both maps
// nil).
func (h *RouteHandle[Req, Resp]) EncodeMerged(resp Resp) (body []byte, headers, cookies map[string]string, err error) {
	body, err = h.Encode(resp)
	if err != nil {
		return nil, nil, nil, err
	}
	headers, cookies, err = h.EncodeResponseMergeFields(resp)
	if err != nil {
		return nil, nil, nil, err
	}
	return body, headers, cookies, nil
}

// EncodeResponseMergeFields derives resp's response header/cookie values
// (via [ResponseHeaderMergeFields]/[ResponseCookieMergeFields] +
// [codex.EncodeVars]) WITHOUT encoding the response body — the
// body-independent half of [EncodeMerged], split out so callers that
// encode the body via a negotiated [format.Format] (i.e. [WithFormats],
// not plain [Encode]) can still derive merge-capable response
// header/cookie values. Used by each adapter's internal serve dispatch
// (invoked via [nethttp.AttachMux]/[chi.AttachRouter]) for multi-format
// routes AND for a matched [ErrorPattern] payload whose concrete type
// equals Resp. Returns nil maps when the route declares no response
// merge-capable params.
func (h *RouteHandle[Req, Resp]) EncodeResponseMergeFields(resp Resp) (headers, cookies map[string]string, err error) {
	if fields := h.ResponseHeaderMergeFields(); len(fields) > 0 {
		if headers, err = codex.EncodeVars(resp, fields...); err != nil {
			return nil, nil, err
		}
	}
	if fields := h.ResponseCookieMergeFields(); len(fields) > 0 {
		if cookies, err = codex.EncodeVars(resp, fields...); err != nil {
			return nil, nil, err
		}
	}
	return headers, cookies, nil
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

// EncodeRequestWithFormats is the canonical "encode using whatever format
// THIS route declares" method — the single source of truth every
// client-side caller (escape-hatch [callWithVars] AND
// [ClientTransport]/Client.Attach) delegates to, instead of each
// duplicating its own format-resolution logic inline. Resolves, in
// priority order: formats (a call-time override, passed by escape-hatch
// callers that support one; empty for Client.Attach, which has no
// call-time-override concept) > h.RequestFormats[0] > plain
// [RouteHandle.EncodeRequest] (assumed "application/json"). Returns the
// matching Content-Type alongside the encoded body.
func (h *RouteHandle[Req, Resp]) EncodeRequestWithFormats(req Req, formats ...format.Format[Req]) (body []byte, contentType string, err error) {
	effectiveFmts := formats
	if len(effectiveFmts) == 0 {
		effectiveFmts = h.RequestFormats
	}
	if len(effectiveFmts) > 0 {
		body, err = effectiveFmts[0].Marshal(req)
		if err != nil {
			return nil, "", err
		}
		return body, effectiveFmts[0].ContentType(), nil
	}
	body, err = h.EncodeRequest(req)
	return body, "application/json", err
}

// ResponseFormat resolves the effective response format for this route,
// in priority order: formats (a call-time override) > h.Formats[0] >
// none (zero value, false — caller falls back to
// [RouteHandle.DecodeResponse]/"application/json"). The route's OWN
// declaration ([RouteHandle.WithFormats]) is the single source of truth;
// this is the resolution step a client-side caller needs BOTH before
// sending the request (to set the Accept header, via
// [format.Format.ContentType]) and again before decoding the response
// body (via [RouteHandle.DecodeResponseWithFormats]) — split out from
// the decode step itself since those two happen at different times.
func (h *RouteHandle[Req, Resp]) ResponseFormat(formats ...format.Format[Resp]) (format.Format[Resp], bool) {
	effectiveFmts := formats
	if len(effectiveFmts) == 0 {
		effectiveFmts = h.Formats
	}
	if len(effectiveFmts) > 0 {
		return effectiveFmts[0], true
	}
	var zero format.Format[Resp]
	return zero, false
}

// DecodeResponseWithFormats decodes body via [RouteHandle.ResponseFormat]'s
// resolution, falling back to plain [RouteHandle.DecodeResponse] when
// unresolved — the canonical "decode using whatever format THIS route
// declares" method every client-side caller (escape-hatch [callWithVars]
// AND [ClientTransport]/Client.Attach) delegates to. See
// [EncodeRequestWithFormats]'s doc comment for the shared rationale.
func (h *RouteHandle[Req, Resp]) DecodeResponseWithFormats(body []byte, formats ...format.Format[Resp]) (Resp, error) {
	if resolvedFmt, ok := h.ResponseFormat(formats...); ok {
		return resolvedFmt.Unmarshal(body)
	}
	return h.DecodeResponse(body)
}

// WithHandler is [Route.WithHandler]'s post-registration equivalent —
// attaches fn as h's business handler AFTER [Route.RegisterHandle] instead
// of before. Use this when the handler's dependencies (a database handle,
// an in-memory store, etc.) aren't available until runtime, but the route
// itself needs to be registered earlier (e.g. as a package-level var, for
// spec generation or ports wiring) — [Serve]/[ServeOne] read whichever
// value HandlerFn holds at the time they run, so calling WithHandler any
// time before [Serve] is equivalent to [Route.WithHandler] before
// [Route.Register]. A route with no handler attached by EITHER method
// remains spec-only (see [Serve]'s Part-1 gating rule).
func (h *RouteHandle[Req, Resp]) WithHandler(fn func(ctx context.Context, req Req) (Resp, error)) *RouteHandle[Req, Resp] {
	h.HandlerFn = fn
	return h
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
	// (from [Route.Use]) so [Server.OpenAPISpec] can aggregate
	// components.securitySchemes across all registered routes — there is no
	// builder-level security scheme store to read from instead.
	securitySchemes() map[string]SecurityScheme
	// hasHandler reports whether WithHandler was ever called — the Part-1
	// gating signal each adapter's internal serve/serveSSE dispatch (invoked via
	// [nethttp.AttachMux]/[chi.AttachRouter]) uses to skip
	// spec-only routes entirely (see "Decision: Serve's whole-builder
	// failure semantics").
	hasHandler() bool
}

// RouteEntry is a read-only, reflection-friendly view of one [Route]
// registered into a [Server] — returned by [Server.RouteEntries]. It
// exists so each adapter's internal serve dispatch (invoked via
// [nethttp.AttachMux]/[chi.AttachRouter]) can walk a HETEROGENEOUS
// collection of routes (each with a DIFFERENT Req/Resp pair) without
// api/rest itself needing net/http or reflect: Handle() returns the
// concrete *[RouteHandle][Req, Resp] type-erased to any; the consuming
// adapter recovers Req/Resp via reflect.Value.Call against the handle's
// ALREADY-concrete exported closures (Decode/Encode/HandlerFn/each
// [middleware.ServerImplementation.Fn]) — see
// docs/design/d-0001-rest-middleware-workflow-simplification.md's "Decision:
// Serve's generic dispatch mechanism" for the full rationale. The
// isRouteEntry marker method seals this interface to api/rest's own
// implementation.
type RouteEntry interface {
	// Method returns the route's HTTP method (GET, POST, etc.).
	Method() string
	// Path returns the route's path template (e.g. /users/{id}).
	Path() string
	// HasHandler reports whether [Route.WithHandler] was ever called.
	HasHandler() bool
	// Handle returns the underlying *RouteHandle[Req, Resp], type-erased.
	Handle() any
	isRouteEntry()
}

// SSERouteEntry is [RouteEntry]'s SSE counterpart, returned by
// [Server.SSEEntries]. Method always returns "GET" ([NewSSERoute]
// hardcodes it).
type SSERouteEntry interface {
	Method() string
	Path() string
	HasHandler() bool
	Handle() any // *SSERouteHandle[Req, Event], type-erased
	isSSERouteEntry()
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
func (e *typedRouteEntry[Req, Resp]) hasHandler() bool { return e.handle.HandlerFn != nil }

// RouteEntry implementation.
func (e *typedRouteEntry[Req, Resp]) Method() string   { return e.handle.Descriptor.Method }
func (e *typedRouteEntry[Req, Resp]) Path() string     { return e.handle.Descriptor.Path }
func (e *typedRouteEntry[Req, Resp]) HasHandler() bool { return e.hasHandler() }
func (e *typedRouteEntry[Req, Resp]) Handle() any      { return e.handle }
func (e *typedRouteEntry[Req, Resp]) isRouteEntry()    {}

// typedSSEEntry stores a pointer to the SSERouteHandle so that With* mutations
// are visible to the builder at OpenAPISpec() time.
type typedSSEEntry[Req, Event any] struct {
	handle *SSERouteHandle[Req, Event]
}

func (e *typedSSEEntry[Req, Event]) descriptor() route.Route { return e.handle.Descriptor }
func (e *typedSSEEntry[Req, Event]) securitySchemes() map[string]SecurityScheme {
	return e.handle.SecuritySchemes
}
func (e *typedSSEEntry[Req, Event]) hasHandler() bool { return e.handle.HandlerFn != nil }

// SSERouteEntry implementation.
func (e *typedSSEEntry[Req, Event]) Method() string   { return e.handle.Descriptor.Method }
func (e *typedSSEEntry[Req, Event]) Path() string     { return e.handle.Descriptor.Path }
func (e *typedSSEEntry[Req, Event]) HasHandler() bool { return e.hasHandler() }
func (e *typedSSEEntry[Req, Event]) Handle() any      { return e.handle }
func (e *typedSSEEntry[Req, Event]) isSSERouteEntry() {}

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
//
// V need not be string — see [codex.NewParam] for merging a query value
// directly into an int/UUID/etc.
func NewRequiredQueryParam[T, V any](
	name string,
	codec codex.Codec[V],
	get func(T) V,
	set func(*T, V),
) MergedQueryParam[T] {
	strCodec := codex.StringValidatorFrom(codec)
	return MergedQueryParam[T]{
		QueryParam: QueryParam{Name: name, Codec: &strCodec, Required: true},
		field:      codex.RequiredField(name, codec, get, set),
	}
}

// NewOptionalQueryParam declares an OPTIONAL query parameter that is BOTH
// validated against codec (when present) AND automatically merged into Req
// by [RouteHandle.DecodeMerged] (when present — absent values leave the
// field untouched, following [codex.OptionalField]'s semantics).
//
// V need not be string — see [codex.NewParam] for merging a query value
// directly into an int/UUID/etc.
func NewOptionalQueryParam[T, V any](
	name string,
	codec codex.Codec[V],
	get func(T) V,
	set func(*T, V),
) MergedQueryParam[T] {
	strCodec := codex.StringValidatorFrom(codec)
	return MergedQueryParam[T]{
		QueryParam: QueryParam{Name: name, Codec: &strCodec, Required: false},
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
//
// V need not be string — see [codex.NewParam] for merging a cookie value
// directly into an int/UUID/etc.
func NewRequiredCookieParam[T, V any](
	name string,
	codec codex.Codec[V],
	get func(T) V,
	set func(*T, V),
) MergedCookieParam[T] {
	strCodec := codex.StringValidatorFrom(codec)
	return MergedCookieParam[T]{
		CookieParam: CookieParam{Name: name, Codec: &strCodec, Required: true},
		field:       codex.RequiredField(name, codec, get, set),
	}
}

// NewOptionalCookieParam declares an OPTIONAL cookie parameter that is BOTH
// validated against codec (when present) AND automatically merged into Req
// (when present) by [RouteHandle.DecodeMerged].
//
// V need not be string — see [codex.NewParam] for merging a cookie value
// directly into an int/UUID/etc.
func NewOptionalCookieParam[T, V any](
	name string,
	codec codex.Codec[V],
	get func(T) V,
	set func(*T, V),
) MergedCookieParam[T] {
	strCodec := codex.StringValidatorFrom(codec)
	return MergedCookieParam[T]{
		CookieParam: CookieParam{Name: name, Codec: &strCodec, Required: false},
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
//
// V need not be string — see [codex.NewParam] for merging a header value
// directly into an int/UUID/etc.
func NewRequiredHeaderParam[T, V any](
	name string,
	codec codex.Codec[V],
	get func(T) V,
	set func(*T, V),
) MergedHeaderParam[T] {
	strCodec := codex.StringValidatorFrom(codec)
	return MergedHeaderParam[T]{
		HeaderParam: HeaderParam{Name: name, Codec: &strCodec, Required: true},
		field:       codex.RequiredField(name, codec, get, set),
	}
}

// NewOptionalHeaderParam declares an OPTIONAL header parameter that is BOTH
// validated against codec (when present) AND automatically merged into Req
// (when present) by [RouteHandle.DecodeMerged].
//
// V need not be string — see [codex.NewParam] for merging a header value
// directly into an int/UUID/etc.
func NewOptionalHeaderParam[T, V any](
	name string,
	codec codex.Codec[V],
	get func(T) V,
	set func(*T, V),
) MergedHeaderParam[T] {
	strCodec := codex.StringValidatorFrom(codec)
	return MergedHeaderParam[T]{
		HeaderParam: HeaderParam{Name: name, Codec: &strCodec, Required: false},
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
//
// V need not be string — see [codex.NewParam] for merging a response
// header value directly into an int/UUID/etc.
func NewRequiredResponseHeaderParam[Resp, V any](
	name string,
	codec codex.Codec[V],
	get func(Resp) V,
	set func(*Resp, V),
) MergedResponseHeaderParam[Resp] {
	strCodec := codex.StringValidatorFrom(codec)
	return MergedResponseHeaderParam[Resp]{
		ResponseHeaderParam: ResponseHeaderParam{Name: name, Codec: &strCodec, Required: true},
		field:               codex.RequiredField(name, codec, get, set),
	}
}

// NewOptionalResponseHeaderParam declares an OPTIONAL response header that is
// BOTH validated against codec (when present) AND automatically merged
// (when present), for both the server encode and client decode directions.
//
// V need not be string — see [codex.NewParam] for merging a response
// header value directly into an int/UUID/etc.
func NewOptionalResponseHeaderParam[Resp, V any](
	name string,
	codec codex.Codec[V],
	get func(Resp) V,
	set func(*Resp, V),
) MergedResponseHeaderParam[Resp] {
	strCodec := codex.StringValidatorFrom(codec)
	return MergedResponseHeaderParam[Resp]{
		ResponseHeaderParam: ResponseHeaderParam{Name: name, Codec: &strCodec, Required: false},
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
//
// V need not be string — see [codex.NewParam] for merging a response
// cookie value directly into an int/UUID/etc.
func NewRequiredResponseCookieParam[Resp, V any](
	name string,
	codec codex.Codec[V],
	get func(Resp) V,
	set func(*Resp, V),
) MergedResponseCookieParam[Resp] {
	strCodec := codex.StringValidatorFrom(codec)
	return MergedResponseCookieParam[Resp]{
		ResponseCookieParam: ResponseCookieParam{Name: name, Codec: &strCodec, Required: true},
		field:               codex.RequiredField(name, codec, get, set),
	}
}

// NewOptionalResponseCookieParam declares an OPTIONAL response cookie that is
// BOTH validated against codec (when present) AND automatically merged
// (when present), for both the server encode and client decode directions.
//
// V need not be string — see [codex.NewParam] for merging a response
// cookie value directly into an int/UUID/etc.
func NewOptionalResponseCookieParam[Resp, V any](
	name string,
	codec codex.Codec[V],
	get func(Resp) V,
	set func(*Resp, V),
) MergedResponseCookieParam[Resp] {
	strCodec := codex.StringValidatorFrom(codec)
	return MergedResponseCookieParam[Resp]{
		ResponseCookieParam: ResponseCookieParam{Name: name, Codec: &strCodec, Required: false},
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
// [middleware.SecurityScheme]/[FromSecurityScheme], attached via
// [Route.Use], is the ONLY way to declare one; there is no builder-level
// equivalent. The spec fields flow into the OpenAPI document (aggregated
// from all registered routes by [Server.OpenAPISpec]); Codec, when
// non-nil, is used by adapters to
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
//	var bearerAuth = rest.SecurityScheme{
//	    SecurityScheme: route.BearerScheme("JWT"),
//	}.WithCodec(codex.String().Refine(validate.BearerToken))
//
// Pass the result to [FromSecurityScheme] to build a [middleware.Middleware],
// attached via [Route.Use].
func (s SecurityScheme) WithCodec(c codex.Codec[string]) SecurityScheme {
	s.Codec = &c
	return s
}

// NOTE: WithSecurityScheme (the RouteOpt pairing RouteMeta.Security's
// manual, non-empty state with a scheme's spec metadata) was REMOVED —
// every route that wants an actual security requirement now goes through
// [middleware.SecurityScheme] (building a [middleware.Middleware] from
// scratch) or [FromSecurityScheme] (bridging an existing [SecurityScheme]
// value), attached via [Route.Use]. RouteMeta.Security's OTHER two states
// — nil (inherit global security) and []route.SecurityRequirement{}
// (explicit opt-out) — are UNRELATED to scheme declaration and remain
// unchanged. See docs/design/d-0001-rest-middleware-workflow-simplification.md's
// "Decision: eliminate manual per-route security declaration".

// FromSecurityScheme bridges an existing [SecurityScheme] value (e.g. a
// package-level var shared across several routes) into a real
// [middleware.Middleware], usable with [Route.Use]/[Route.HandleMW]
// exactly like one built via [middleware.SecurityScheme] directly.
//
// Lives in api/rest, NOT middleware — [SecurityScheme] (which bundles
// [route.SecurityScheme] + an optional Codec) is an api/rest-only type;
// middleware cannot import api/rest without a cycle (api/rest already
// imports middleware for [middleware.Middleware]/etc.).
//
//	var bearerAuth = rest.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")}.
//	    WithCodec(codex.String().Refine(validate.BearerToken))
//
//	var GetTagsRoute = rest.NewRoute[GetTagsReq, TagsList](
//	    "GET", "/v2/{name}/tags/list",
//	    c.Struct[GetTagsReq](), TagsListCodec,
//	    rest.RouteMeta{OperationID: "getTags"},
//	    rest.NewPathParam("name", ...),
//	).Use(rest.FromSecurityScheme("bearerAuth", bearerAuth, nil))
func FromSecurityScheme(schemeName string, scheme SecurityScheme, scopes []string) middleware.Middleware {
	return middleware.SecurityScheme(schemeName, scheme.SecurityScheme, scopes, scheme.Codec)
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

// Server accumulates route registrations, and produces OpenAPI 3.1
// specifications. Security schemes are declared per-route via
// [Route.Use] (there is no builder-level equivalent) — see
// [Server.OpenAPISpec] for how they're aggregated into the spec. It is safe
// to register routes from multiple goroutines as long as [Server.Build] is
// not called concurrently. Create one with [NewServer].
type Server struct {
	info           Info
	servers        []ServerEntry
	entries        []routeEntry
	schemas        map[string]schema.Schema
	pathCodec      *codex.Codec[string]
	globalSecurity []route.SecurityRequirement

	// mu guards entries/schemas, the only fields mutated after
	// construction (via Route/SSERoute.Register/RegisterHandle and
	// AddSchema) — making concurrent Register calls safe. Supports an app
	// fanning Register calls out across goroutines (one per feature
	// module, say) before a SINGLE later Serve call; it does NOT support
	// hot-adding routes to an already-Serve'd, already-running mux (see
	// docs/roadmap/dynamic-port-rebinding.md for that separate gap).
	mu sync.RWMutex
	// transport is the optional, adapter-provided [ServerTransport]
	// attached via [Server.Attach] (e.g. by nethttp.AttachMux/
	// chi.AttachRouter) — nil until Attach is called. See
	// [Server.Serve]'s doc comment and Decision 5 of
	// docs/design/d-0002-pubsub-workflow-simplification.md /
	// docs/design/d-0001-rest-middleware-workflow-simplification.md's
	// Addendum 5 for the full design (this is purely ADDITIVE — today's existing
	// nethttp.Serve(mux, builder)/chi.Serve(r, builder), wire-only,
	// caller owns their own http.Server, remain completely unchanged).
	transport ServerTransport
}

// ServerTransport is implemented by each adapter's internal, unexported
// binding attached to a [Server] via an adapter-specific Attach function
// (e.g. [nethttp.AttachMux], chi.AttachRouter) — see [Server.Attach].
// Mirrors [events.Transport] (docs/design/d-0002-pubsub-workflow-simplification.md's
// Decision 5) for the pub/sub side of this same unification — see
// docs/design/d-0001-rest-middleware-workflow-simplification.md's
// Addendum 5 for the full rationale.
type ServerTransport interface {
	// Serve wires every handler-bearing route (reusing today's existing
	// nethttp.Serve/chi.Serve logic internally, unchanged) and BLOCKS,
	// owning its own *http.Server, until ctx is cancelled (graceful
	// shutdown) or a fatal serve error occurs.
	Serve(ctx context.Context) error
}

// Attach binds t to b as b's server transport — the "attach the adapter to
// the builder" step behind [Server.Serve]. Each adapter provides its own
// entry point (e.g. nethttp.AttachMux(builder, mux, addr)) that builds an
// internal ServerTransport implementation and calls this method
// internally; application code calls the ADAPTER's Attach function, not
// this method directly, in the common case.
//
// Returns [ServerTransportAlreadyAttachedError] if b already has a
// transport attached — Attach is exclusive, mirrors
// [events.Client.Attach] exactly.
func (b *Server) Attach(t ServerTransport) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.transport != nil {
		return ServerTransportAlreadyAttachedError{}
	}
	b.transport = t
	return nil
}

// Serve wires every handler-bearing route and BLOCKS, via b's attached
// [ServerTransport], until ctx is cancelled or a fatal serve error occurs
// — the sole server-side workflow (mirrors [events.Client.ServeSubscribers]'s
// role on the pub/sub side). Returns [NoServerTransportAttachedError] if
// [Server.Attach] was never called.
//
// The older, non-blocking wire-only primitives this replaces
// (`nethttp.Serve(mux, builder)`/`chi.Serve(r, builder)`) were REMOVED
// (unexported) once `AttachMux`/`AttachRouter` + Serve shipped — see
// `docs/design/d-0002-pubsub-workflow-simplification.md`'s Decision 6.
// A caller needing full control over TLS/timeouts/etc. builds their own
// `*http.Server{Handler: mux}` after `AttachMux`/`AttachRouter` has wired
// mux, without ever calling `Serve` itself.
//
//	builder := rest.NewServer(rest.Info{...})
//	if err := createUserRoute.Register(builder); err != nil { ... }
//	mux := http.NewServeMux()
//	_ = nethttp.AttachMux(builder, mux, ":8080")
//	err := builder.Serve(ctx) // blocks, owns its own http.Server
func (b *Server) Serve(ctx context.Context) error {
	b.mu.RLock()
	t := b.transport
	b.mu.RUnlock()
	if t == nil {
		return NoServerTransportAttachedError{}
	}
	return t.Serve(ctx)
}

// ServerTransportAlreadyAttachedError is returned by [Server.Attach] when
// b already has a [ServerTransport] attached — Attach is exclusive, see
// its doc comment for the rationale.
type ServerTransportAlreadyAttachedError struct{}

func (e ServerTransportAlreadyAttachedError) Error() string {
	return "api/rest: Server already has a ServerTransport attached (Attach is exclusive; build a fresh Server for a different transport)"
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e ServerTransportAlreadyAttachedError) LogValue() slog.Value {
	return slog.GroupValue()
}

// NoServerTransportAttachedError is returned by [Server.Serve] when
// [Server.Attach] was never called.
type NoServerTransportAttachedError struct{}

func (e NoServerTransportAttachedError) Error() string {
	return "api/rest: Server has no ServerTransport attached (call an adapter's Attach function first, e.g. nethttp.AttachMux(builder, mux, addr))"
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e NoServerTransportAttachedError) LogValue() slog.Value {
	return slog.GroupValue()
}

// TransportTypeMismatchError is returned by [Client.Call] when the
// dynamic types of its `any`-typed arguments don't match each other as
// expected — e.g. req's concrete type doesn't match route's declared Req
// type. This is the explicit, narrowly-scoped runtime-type-safety cost of
// Client.Call's literal method call shape — mirrors
// [events.TransportTypeMismatchError] exactly (each API layer keeps its
// own parallel error vocabulary).
type TransportTypeMismatchError struct {
	// Path is the route path involved (empty when the mismatch is
	// detected before a route could be resolved at all, e.g. routeAny
	// itself isn't a recognized rest.Route[Req, Resp]).
	Path string
	// Want describes the expected type (e.g. "rest.Route[GetUserReq, GetUserResp]").
	Want string
	// Got describes the actual dynamic type provided.
	Got string
}

func (e TransportTypeMismatchError) Error() string {
	return fmt.Sprintf("api/rest: path %q: Transport type mismatch: want %s, got %s", e.Path, e.Want, e.Got)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e TransportTypeMismatchError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("path", e.Path),
		slog.String("want", e.Want),
		slog.String("got", e.Got),
	)
}

// ClientCallOptions configures a single [Client.Call] invocation —
// additive and optional (existing call sites passing no options are
// unaffected). Type-erased (`any` fields), consistent with
// [CallOptions.RequestFormats]/[ResponseFormats]'s existing idiom —
// [Client.Call]/[ClientTransport.Call] have no Req/Resp type parameter
// to constrain a generic options type. See
// docs/design/d-0001-rest-middleware-workflow-simplification.md's Addendum 4.
type ClientCallOptions struct {
	// RequestFormats, when non-nil, OVERRIDES the route's declared
	// request-body encode format for THIS call only
	// ([]format.Format[Req]) — mirrors [CallOptions.RequestFormats]
	// exactly, resolved generically by the attached [ClientTransport].
	RequestFormats any

	// ResponseFormats is [RequestFormats]'s response-direction sibling
	// ([]format.Format[Resp]) — mirrors [CallOptions.ResponseFormats].
	ResponseFormats any
}

// ClientConsumeOptions is [ClientCallOptions]'s SSE-consumption sibling,
// configuring a single [Client.Consume] invocation.
type ClientConsumeOptions struct {
	// Formats, when non-nil, OVERRIDES the route's declared event decode
	// format for THIS Consume call only ([]format.Format[Event]) —
	// mirrors [ConsumeOptions.Formats] exactly.
	Formats any
}

// ClientTransport is implemented by each adapter's internal, unexported
// binding attached to a [Client] via an adapter-specific Attach function
// (e.g. [nethttp.Attach]) — see [Client.Attach]. Mirrors
// [events.Transport] (docs/design/d-0002-pubsub-workflow-simplification.md's
// Decision 5) for the pub/sub side of this same unification — see
// docs/design/d-0001-rest-middleware-workflow-simplification.md's
// Addendum 5 for the full rationale. Bundles BOTH request/response AND SSE-stream consumption in
// ONE interface, mirroring [events.Transport]'s own
// Publish/Subscribe/ServeSubscribers bundling: an adapter is attachable
// to a [Client] ONLY if it implements the WHOLE interface (see
// docs/design/d-0001-rest-middleware-workflow-simplification.md's Addendum 4).
type ClientTransport interface {
	// Call performs a round trip against route (dynamic type
	// rest.Route[Req, Resp]) with req (dynamic type Req), returning the
	// decoded response as `any` (dynamic type Resp). opts is VARIADIC
	// (0 or 1 value; a 2nd+ is ignored) so existing 3-arg call sites stay
	// source-compatible — see [ClientCallOptions].
	Call(ctx context.Context, route any, req any, opts ...ClientCallOptions) (any, error)

	// Consume starts consuming the SSE route sseRoute (dynamic type
	// rest.SSERoute[Req, Event]) with req (dynamic type Req), calling fn
	// (dynamic type func(context.Context, Event) error) for each event.
	// Blocks until ctx is cancelled — mirrors [events.Transport.Subscribe]'s
	// identical blocking contract for the SAME reason (a long-lived
	// stream, not a one-shot call). opts is variadic for the SAME reason
	// as [Call]'s.
	Consume(ctx context.Context, sseRoute any, req any, fn any, opts ...ClientConsumeOptions) error
}

// Client is a client-side, api-level connection holder — the mirror of
// [events.Client] on REST's calling side, living in api/rest rather than
// an adapter package (unlike the earlier adapters/nethttp.Caller design
// this replaces). Client carries NO spec/registry (unlike [Server],
// which keeps all of the OpenAPI spec-accumulation responsibilities) —
// mirrors what adapters/nethttp.Caller already did (just a connection
// holder), relocated to the api/rest domain level.
//
// Construct via [NewClient], then bind it to a concrete transport via an
// adapter's own Attach function (e.g. [nethttp.Attach]) before calling
// [Client.Call].
type Client struct {
	mu        sync.RWMutex
	transport ClientTransport
}

// NewClient returns an unattached [Client]. Call an adapter's Attach
// function (e.g. [nethttp.Attach]) before using [Client.Call].
func NewClient() *Client {
	return &Client{}
}

// Attach binds t to c as c's transport — the "attach the adapter to the
// client" step behind [Client.Call]. Each adapter provides its own entry
// point (e.g. [nethttp.Attach](client, httpClient, baseURL)) that builds
// an internal ClientTransport implementation and calls this method
// internally; application code calls the ADAPTER's Attach function, not
// this method directly, in the common case.
//
// Returns [ClientTransportAlreadyAttachedError] if c already has a
// transport attached — Attach is exclusive, mirrors
// [events.Client.Attach]/[Server.Attach] exactly.
func (c *Client) Attach(t ClientTransport) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.transport != nil {
		return ClientTransportAlreadyAttachedError{}
	}
	c.transport = t
	return nil
}

// Call performs a round trip against route with req, via c's attached
// [ClientTransport] — see [ClientTransport]'s doc comment for the full
// design rationale (why this is `any`-typed/reflection-based) and
// [Client.Attach] for how a transport gets attached. Returns
// [NoClientTransportAttachedError] if [Client.Attach] was never called.
//
//	client := rest.NewClient()
//	_ = nethttp.Attach(client, httpClient, baseURL)
//	respAny, err := client.Call(ctx, getUserRoute, GetUserReq{ID: "f47ac10b"})
//	resp := respAny.(GetUserResp)
//
// opts is variadic (0 or 1 value) — additive, backward-compatible with
// every existing 3-arg call site.
func (c *Client) Call(ctx context.Context, route any, req any, opts ...ClientCallOptions) (any, error) {
	c.mu.RLock()
	t := c.transport
	c.mu.RUnlock()
	if t == nil {
		return nil, NoClientTransportAttachedError{}
	}
	return t.Call(ctx, route, req, opts...)
}

// Consume starts consuming sseRoute with req, via c's attached
// [ClientTransport] — the SSE-stream mirror of [Client.Call]: fn
// (dynamic type func(context.Context, Event) error) is called for each
// decoded event; Consume BLOCKS until ctx is cancelled or a fatal setup
// error occurs (mirrors [events.Client.Subscribe]'s identical blocking
// contract). Returns [NoClientTransportAttachedError] if [Client.Attach]
// was never called.
//
//	client := rest.NewClient()
//	_ = nethttp.Attach(client, httpClient, baseURL)
//	err := client.Consume(ctx, sensorStreamRoute, GetSensorReq{ID: "room-42"},
//	    func(ctx context.Context, e SensorReading) error { ...; return nil })
//
// opts is variadic (0 or 1 value) — additive.
func (c *Client) Consume(ctx context.Context, sseRoute any, req any, fn any, opts ...ClientConsumeOptions) error {
	c.mu.RLock()
	t := c.transport
	c.mu.RUnlock()
	if t == nil {
		return NoClientTransportAttachedError{}
	}
	return t.Consume(ctx, sseRoute, req, fn, opts...)
}

// ClientTransportAlreadyAttachedError is returned by [Client.Attach] when
// c already has a [ClientTransport] attached — Attach is exclusive, see
// its doc comment for the rationale.
type ClientTransportAlreadyAttachedError struct{}

func (e ClientTransportAlreadyAttachedError) Error() string {
	return "api/rest: Client already has a ClientTransport attached (Attach is exclusive; build a fresh Client for a different transport)"
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e ClientTransportAlreadyAttachedError) LogValue() slog.Value {
	return slog.GroupValue()
}

// NoClientTransportAttachedError is returned by [Client.Call]/
// [Client.Consume] when [Client.Attach] was never called.
type NoClientTransportAttachedError struct{}

func (e NoClientTransportAttachedError) Error() string {
	return "api/rest: Client has no ClientTransport attached (call an adapter's Attach function first, e.g. nethttp.Attach(client, httpClient, baseURL))"
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e NoClientTransportAttachedError) LogValue() slog.Value {
	return slog.GroupValue()
}

// ServerOption configures a [Server] at construction time.
type ServerOption func(*Server)

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
//	b := rest.NewServer(info, rest.WithPathConstraints(validate.HTTPPath))
func WithPathCodec(c codex.Codec[string]) ServerOption {
	return func(b *Server) { b.pathCodec = &c }
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
//	b := rest.NewServer(info, rest.WithPathConstraints(validate.HTTPPath, sensorPrefix))
func WithPathConstraints(cons ...codex.Constraint[string]) ServerOption {
	c := codex.String().Refine(cons...)
	return WithPathCodec(c)
}

// NewServer returns a Server initialised with the given API metadata.
func NewServer(info Info, opts ...ServerOption) *Server {
	b := &Server{
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
// [events.Client.AddServer]). Unlike the AsyncAPI builder, OpenAPI servers are
// an ordered array with no named keys — name is not stored beyond this point.
func (b *Server) AddServer(name string, s ServerEntry) *Server {
	if s.Description == "" {
		s.Description = name
	}
	b.mu.Lock()
	b.servers = append(b.servers, s)
	b.mu.Unlock()
	return b
}

// AddSchema registers a named schema in components/schemas.
// Use this to register reusable schemas (e.g. shared error types) that are
// referenced by SchemaName in route configs but not inlined in any codec.
func (b *Server) AddSchema(name string, s schema.Schema) *Server {
	b.mu.Lock()
	b.schemas[name] = s
	b.mu.Unlock()
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
func (b *Server) AddGlobalSecurity(reqs ...route.SecurityRequirement) *Server {
	b.mu.Lock()
	b.globalSecurity = append(b.globalSecurity, reqs...)
	b.mu.Unlock()
	return b
}

// Route is a declarative HTTP route spec: method, path, codecs, and options.
// It is a value type — define it once, store it, pass it around, and register
// it with one or more [Server] instances via [Route.Register].
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
//	handle, err := createUser.RegisterHandle(b)
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

// Register registers the route with b, binding its spec, business handler
// (attached via [Route.WithHandler]), and middleware implementations
// (attached via [Route.HandleMW]/[Route.ClientMW]) into b — for use by
// each adapter's internal serve/serveSSE/serveOne dispatch (invoked via
// [nethttp.AttachMux]/[nethttp.ServeOne]/[chi.AttachRouter]), which
// walk b's accumulated routes and wire each one. No [*RouteHandle] is
// returned — a caller wiring routes through Attach never needs one
// directly. Use [Route.RegisterHandle] instead when a direct handle is
// needed (e.g. ports-style direct adapter wiring that bypasses Serve
// entirely).
//
// If the builder was created with [WithPathCodec] or [WithPathConstraints], the
// path is validated immediately and an error is returned if it fails — no route
// is registered in that case.
//
// Any [PathParam] entry whose name does not appear as a {varName} placeholder
// in the path template causes Register to return an error immediately.
func (r Route[Req, Resp]) Register(b *Server) error {
	_, err := r.registerHandle(b)
	return err
}

// RegisterHandle registers the route with b — identical spec/handler/impl
// binding and validation as [Route.Register] — but ALSO returns the
// resulting [*RouteHandle], for direct-wiring callers that bypass
// each adapter's internal serve dispatch (invoked via
// [nethttp.AttachMux]/[chi.AttachRouter]) entirely (e.g. [ports]'
// pattern-building machinery, which owns its own adapter wiring and never
// mounts routes on a *http.ServeMux itself).
//
// Use [Route.WithRequestFormats]/[RouteHandle.WithFormats] on the returned
// handle to configure multi-format request/response handling.
func (r Route[Req, Resp]) RegisterHandle(b *Server) (*RouteHandle[Req, Resp], error) {
	return r.registerHandle(b)
}

func (r Route[Req, Resp]) registerHandle(b *Server) (*RouteHandle[Req, Resp], error) {
	if b.pathCodec != nil {
		if err := b.pathCodec.Validate(internal.StripTemplateVars(r.path)); err != nil {
			return nil, InvalidPathError{Path: r.path, Err: err}
		}
	}

	var rb routeBuilder
	for _, opt := range r.opts {
		opt.applyRoute(&rb)
	}

	if err := applyMiddlewareDeclarations(&rb, r.method+" "+r.path); err != nil {
		return nil, err
	}

	if err := checkImplementationsDeclared(r.method+" "+r.path, rb.middlewares, rb.impls, rb.clientImpls); err != nil {
		return nil, err
	}

	if err := codex.ValidateDeclaredParams(r.path, toCodexParams(rb.pathParams)); err != nil {
		return nil, err
	}

	frozen := buildDescriptor(r.method, r.path, r.reqCodec.Schema, r.respCodec.Schema, rb, nil)

	jsonReq := format.JSON(r.reqCodec)
	jsonResp := format.JSON(r.respCodec)

	h := &RouteHandle[Req, Resp]{
		Descriptor:            frozen,
		Decode:                func(body []byte) (Req, error) { return jsonReq.Unmarshal(body) },
		Encode:                func(resp Resp) ([]byte, error) { return jsonResp.Marshal(resp) },
		EncodeRequest:         func(req Req) ([]byte, error) { return jsonReq.Marshal(req) },
		DecodeResponse:        func(body []byte) (Resp, error) { return jsonResp.Unmarshal(body) },
		pathParams:            rb.pathParams,
		queryParams:           rb.queryParams,
		cookieParams:          rb.cookieParams,
		headerParams:          rb.headerParams,
		responseHeaderParams:  rb.respHeaders,
		responseCookieParams:  rb.respCookies,
		pathCodec:             b.pathCodec,
		SecuritySchemes:       rb.securitySchemes,
		GlobalSecurity:        slices.Clone(b.globalSecurity),
		errorStatusRules:      slices.Clone(rb.errorStatusRules),
		errorPatternRules:     slices.Clone(rb.errorPatternRules),
		Middlewares:           slices.Clone(rb.middlewares),
		HandlerFn:             rb.handlerFn,
		HandlerOpts:           rb.handlerOpts,
		Implementations:       slices.Clone(rb.impls),
		ClientImplementations: slices.Clone(rb.clientImpls),
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
	b.mu.Lock()
	b.entries = append(b.entries, entry)
	b.mu.Unlock()
	return h, nil
}

// ClientHandle returns a [RouteHandle] for client-side use without registering
// with a [Server]. No path codec validation and no spec registration occur.
//
// Use ClientHandle when only the client side needs codec and route definitions
// (no OpenAPI spec, no server), or when sharing a [Route] definition between
// server and client in the same binary.
//
// The returned handle has the same Decode / Encode / EncodeRequest / DecodeResponse
// codec helpers and the same parameter validation methods as a handle returned
// by [Route.Register] — including [RouteHandle.SecuritySchemes], populated from
// the route's own [Route.Use] security declarations (there is no [Server]
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
	// Merge any middleware-declared Security into rb.securitySchemes/
	// rb.meta.Security — the SAME mechanism Register uses (see
	// applyMiddlewareDeclarations), so a route declared via
	// rest.WithMiddleware gets IDENTICAL SecuritySchemes on BOTH the
	// server (Register) and client (ClientHandle) side, keeping
	// nethttp.Call's credential-format check working exactly like it
	// already does for the manual RouteMeta.Security escape hatch.
	// ClientHandle stays infallible: conflict detection and drift-closing
	// coverage checking (Register/ValidateRoute's job) do NOT run here.
	applyMiddlewareSecurityForClient(&rb)

	frozen := buildDescriptor(r.method, r.path, r.reqCodec.Schema, r.respCodec.Schema, rb, nil)

	jsonReq := format.JSON(r.reqCodec)
	jsonResp := format.JSON(r.respCodec)

	h := &RouteHandle[Req, Resp]{
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
		Middlewares:               slices.Clone(rb.middlewares),
		ClientImplementations:     slices.Clone(rb.clientImpls),
	}
	// Apply any inline Formats/RequestFormats RouteOpt declared on the
	// Route -- the SAME rb.requestFormats/rb.respFormats fields
	// registerHandle applies server-side. Without this, ClientHandle
	// silently ignored a declared wire format and nethttp.Call always
	// fell back to JSON regardless of what was declared (a confirmed
	// bug, not a design choice).
	if rb.requestFormats != nil {
		fmts, ok := rb.requestFormats.([]format.Format[Req])
		if !ok {
			panic(fmt.Sprintf("api/rest: ClientHandle: %s", FormatOptError{Direction: "request",
				Err: fmt.Errorf("want []format.Format[%T], got %T", *new(Req), rb.requestFormats)}.Error()))
		}
		h.WithRequestFormats(fmts...)
	}
	if rb.respFormats != nil {
		fmts, ok := rb.respFormats.([]format.Format[Resp])
		if !ok {
			panic(fmt.Sprintf("api/rest: ClientHandle: %s", FormatOptError{Direction: "response",
				Err: fmt.Errorf("want []format.Format[%T], got %T", *new(Resp), rb.respFormats)}.Error()))
		}
		h.WithFormats(fmts...)
	}
	return h
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

	// DecodeEvent deserialises and validates one SSE "data:" line's raw
	// bytes into an Event — the complement of EncodeEvent, used by
	// client-side consumption ([Client.Consume]/[nethttp.CallSSEAdapter])
	// as the fallback decoder when no Formats entry matches the
	// connection's negotiated Content-Type (or none are declared).
	DecodeEvent func(data []byte) (Event, error)

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
	// Populated from the route's own [Route.Use] security declarations —
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
	// Set via [Server.AddGlobalSecurity]. nil when no global security is declared.
	// Unlike SecuritySchemes, GlobalSecurity remains builder-only — it answers
	// "which routes require auth by default" (spec-wide), not "what does a
	// scheme look like" — and has no [Route.ClientHandle] equivalent (always
	// nil there, unchanged).
	GlobalSecurity []route.SecurityRequirement

	// Middlewares holds every [middleware.Middleware] attached via
	// [WithMiddleware]/[SSERoute.Use], in attachment order — mirrors
	// [RouteHandle.Middlewares].
	Middlewares []middleware.Middleware

	// HandlerFn holds the type-erased SSE handler attached via
	// [SSERoute.WithHandler] — nil if never called (spec-only; see
	// [ServeSSE]'s Part-1 gating rule).
	HandlerFn any

	// HandlerOpts holds the type-erased adapter Options value attached via
	// [SSERoute.WithOptions] — nil if never called.
	HandlerOpts any

	// Implementations holds every [middleware.ServerImplementation]
	// attached via [SSERoute.HandleMW], in attachment order — mirrors
	// [RouteHandle.Implementations].
	Implementations []middleware.ServerImplementation

	// responseHeaderParams holds per-header entries registered via ResponseHeaderParam options.
	responseHeaderParams []ResponseHeaderParam

	// responseCookieParams holds per-cookie entries registered via ResponseCookieParam options.
	responseCookieParams []ResponseCookieParam

	// mergeFields holds SSE event merge fields registered via
	// NewRequiredSSEEventParam/NewOptionalSSEEventParam.
	mergeFields []codex.FieldCodec[Event]

	// pathMergeFields/queryMergeFields/headerMergeFields/cookieMergeFields
	// hold the Req-side merge-capable fields registered via
	// NewRequiredPathParam[T]/NewRequiredQueryParam[T]/etc. — mirrors
	// RouteHandle's identically-named fields exactly. Populated by
	// SSERoute.registerHandle/ClientHandle from the SAME
	// rb.pathMergeFields/etc. routeBuilder fields Route.registerHandle
	// already reads — see [SSERouteHandle.PathMergeFields] and siblings.
	pathMergeFields   []codex.FieldCodec[Req]
	queryMergeFields  []codex.FieldCodec[Req]
	headerMergeFields []codex.FieldCodec[Req]
	cookieMergeFields []codex.FieldCodec[Req]

	// ClientImplementations holds every [middleware.ClientImplementation]
	// attached via [SSERoute.ClientMW], in attachment order — mirrors
	// [RouteHandle.ClientImplementations] exactly; consumed by
	// [Client.Consume]/[nethttp.CallSSEAdapter] the same way
	// [Client.Call] consumes RouteHandle's field.
	ClientImplementations []middleware.ClientImplementation
}

// PathMergeFields returns the Req-side merge-capable fields registered via
// [NewPathParam]/[NewRequiredPathParam] — role-scoped, safe for the
// ENCODE direction (client building a connection URL from Req). Mirrors
// [RouteHandle.PathMergeFields] exactly.
func (h *SSERouteHandle[Req, Event]) PathMergeFields() []codex.FieldCodec[Req] {
	return h.pathMergeFields
}

// QueryMergeFields returns the Req-side merge-capable fields registered
// via [NewRequiredQueryParam]/[NewOptionalQueryParam] — mirrors
// [RouteHandle.QueryMergeFields] exactly.
func (h *SSERouteHandle[Req, Event]) QueryMergeFields() []codex.FieldCodec[Req] {
	return h.queryMergeFields
}

// HeaderMergeFields returns the Req-side merge-capable fields registered
// via [NewRequiredHeaderParam]/[NewOptionalHeaderParam] — mirrors
// [RouteHandle.HeaderMergeFields] exactly.
func (h *SSERouteHandle[Req, Event]) HeaderMergeFields() []codex.FieldCodec[Req] {
	return h.headerMergeFields
}

// CookieMergeFields returns the Req-side merge-capable fields registered
// via [NewRequiredCookieParam]/[NewOptionalCookieParam] — mirrors
// [RouteHandle.CookieMergeFields] exactly.
func (h *SSERouteHandle[Req, Event]) CookieMergeFields() []codex.FieldCodec[Req] {
	return h.cookieMergeFields
}

// EncodeVars derives the path-var map from req using
// [SSERouteHandle.PathMergeFields] — mirrors [RouteHandle.EncodeVars]
// exactly; see its doc comment for why this exists as a reflectable
// METHOD rather than a direct [codex.EncodeVars] call.
func (h *SSERouteHandle[Req, Event]) EncodeVars(req Req) (map[string]string, error) {
	return codex.EncodeVars(req, h.pathMergeFields...)
}

// EncodeQueryVars is [EncodeVars]'s query-param sibling, deriving from
// [SSERouteHandle.QueryMergeFields].
func (h *SSERouteHandle[Req, Event]) EncodeQueryVars(req Req) (map[string]string, error) {
	return codex.EncodeVars(req, h.queryMergeFields...)
}

// EncodeHeaderVars is [EncodeVars]'s header-param sibling, deriving from
// [SSERouteHandle.HeaderMergeFields].
func (h *SSERouteHandle[Req, Event]) EncodeHeaderVars(req Req) (map[string]string, error) {
	return codex.EncodeVars(req, h.headerMergeFields...)
}

// EncodeCookieVars is [EncodeVars]'s cookie-param sibling, deriving from
// [SSERouteHandle.CookieMergeFields].
func (h *SSERouteHandle[Req, Event]) EncodeCookieVars(req Req) (map[string]string, error) {
	return codex.EncodeVars(req, h.cookieMergeFields...)
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

// WithHandler is [SSERoute.WithHandler]'s post-registration equivalent —
// see [RouteHandle.WithHandler] for the full rationale (runtime-only
// dependencies, package-level route vars, etc.); identical mechanics here.
func (h *SSERouteHandle[Req, Event]) WithHandler(fn func(ctx context.Context, req Req, send func(Event) error) error) *SSERouteHandle[Req, Event] {
	h.HandlerFn = fn
	return h
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

// EffectiveEventFormats resolves the CANDIDATE format list for
// decoding an incoming SSE event, in priority order: formats (a
// call-time override) > h.Formats — the single source of truth every
// client-side consumer (the escape-hatch `consumeSSE` primitive AND
// [Client.Consume]'s adapter shim) delegates to instead of duplicating
// this resolution inline. Mirrors [events.ChannelHandle.EffectiveSubscribeFormats]
// exactly. Returns the FULL candidate slice (not just the winning
// format) since [ResolveEventDecoder] needs every candidate to match
// against an Accept header.
func (h *SSERouteHandle[Req, Event]) EffectiveEventFormats(formats ...format.Format[Event]) []format.Format[Event] {
	if len(formats) > 0 {
		return formats
	}
	return h.Formats
}

// ResolveEventDecoder picks the ONE decode function to use for every
// event on a connection, given the Accept header value THIS client
// itself sent (a client-and-server-independently-agree algorithm — see
// the caller's own doc comment for why no round-trip is needed): empty/
// "*/*" Accept resolves to formats[0]; a specific Accept resolves to the
// matching declared format's ContentType; no match/no formats resolves
// to [SSERouteHandle.DecodeEvent] (the JSON default). Mirrors the
// server's own Accept-negotiation algorithm exactly (see
// negotiateFormatReflect in adapters/nethttp/serve_sse.go). Extracted as
// a method (rather than staying a free function in adapters/nethttp) so
// it is REFLECTABLE — a runtime-only Event type cannot reflect-call a
// generic free function, but CAN call an exported method already
// monomorphized for a concrete Event at compile time (see
// [RouteHandle.EncodeVars]'s doc comment for the same rationale).
func (h *SSERouteHandle[Req, Event]) ResolveEventDecoder(accept string, formats ...format.Format[Event]) func([]byte) (Event, error) {
	if len(formats) > 0 {
		if accept == "" || accept == "*/*" {
			f := formats[0]
			return func(data []byte) (Event, error) { return f.Unmarshal(data) }
		}
		for _, part := range strings.Split(accept, ",") {
			want := strings.TrimSpace(strings.SplitN(part, ";", 2)[0])
			if want == "*/*" {
				f := formats[0]
				return func(data []byte) (Event, error) { return f.Unmarshal(data) }
			}
			for _, f := range formats {
				ct, _, _ := strings.Cut(f.ContentType(), ";")
				if strings.TrimSpace(ct) == want {
					f := f
					return func(data []byte) (Event, error) { return f.Unmarshal(data) }
				}
			}
		}
	}
	return h.DecodeEvent
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
// text/event-stream — SSE's wire protocol is plain HTTP, so it reuses this
// package's whole toolchain rather than an AsyncAPI-shaped channel/message
// model; see docs/concepts/api-contracts.md's "Why SSE lives in api/rest,
// not api/events" for the full rationale.
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
//	handle, err := notifRoute.RegisterHandle(b)
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

// Register registers the SSE route with b, binding its spec, handler
// (attached via [SSERoute.WithHandler]), and middleware implementations
// (attached via [SSERoute.HandleMW]) into b — for use by
// each adapter's internal serveSSE dispatch (invoked via
// [nethttp.AttachMux]/[chi.AttachRouter]). No [*SSERouteHandle] is returned;
// use [SSERoute.RegisterHandle] when a direct handle is needed.
//
// Path validation follows the same rules as [Route.Register].
func (s SSERoute[Req, Event]) Register(b *Server) error {
	_, err := s.registerHandle(b)
	return err
}

// RegisterHandle registers the SSE route with b — identical binding and
// validation as [SSERoute.Register] — but ALSO returns the resulting
// [*SSERouteHandle], for direct-wiring callers (e.g. [ports]) that bypass
// each adapter's internal serveSSE dispatch (invoked via
// [nethttp.AttachMux]/[chi.AttachRouter]) entirely.
//
// Use [SSERouteHandle.WithFormats] on the returned handle to configure
// non-JSON event serialisation formats.
func (s SSERoute[Req, Event]) RegisterHandle(b *Server) (*SSERouteHandle[Req, Event], error) {
	return s.registerHandle(b)
}

// ClientHandle returns an [SSERouteHandle] for client-side use without
// registering with a [Server] — mirrors [Route.ClientHandle] exactly (no
// spec, no path codec validation): builds its OWN struct literal directly,
// the SAME separate construction path Route.ClientHandle uses (does NOT
// call [SSERoute.registerHandle]/[SSERoute.Register], which requires a
// real [*Server], runs the fallible [checkImplementationsDeclared]/
// coverage checks, and appends to the builder's entries — none of which
// ClientHandle wants). Merges middleware-declared Security via
// [applyMiddlewareSecurityForClient] — the SAME infallible,
// conflict-detection-free merge function Route.ClientHandle uses (NOT
// [applyMiddlewareDeclarations], which is the fallible Register-only
// path) — so a route declared via [WithMiddleware] gets IDENTICAL
// SecuritySchemes on both the server ([SSERoute.Register]) and client
// (ClientHandle) side.
//
// Use for client-only scenarios where no OpenAPI spec is needed, or when
// sharing an [SSERoute] definition between server and client in the same
// binary — see [Client.Consume]/[nethttp.CallSSEAdapter].
func (s SSERoute[Req, Event]) ClientHandle() *SSERouteHandle[Req, Event] {
	var rb routeBuilder
	for _, opt := range s.opts {
		opt.applyRoute(&rb)
	}
	applyMiddlewareSecurityForClient(&rb)

	frozen := buildDescriptor("GET", s.path, s.reqCodec.Schema, s.eventCodec.Schema, rb, []string{"text/event-stream"})

	jsonReq := format.JSON(s.reqCodec)
	jsonEvent := format.JSON(s.eventCodec)

	h := &SSERouteHandle[Req, Event]{
		Descriptor:            frozen,
		Decode:                func(body []byte) (Req, error) { return jsonReq.Unmarshal(body) },
		EncodeEvent:           func(e Event) ([]byte, error) { return jsonEvent.Marshal(e) },
		DecodeEvent:           func(data []byte) (Event, error) { return jsonEvent.Unmarshal(data) },
		ValidateEvent:         func(e Event) error { return jsonEvent.Validate(e) },
		pathParams:            rb.pathParams,
		queryParams:           rb.queryParams,
		cookieParams:          rb.cookieParams,
		headerParams:          rb.headerParams,
		SecuritySchemes:       rb.securitySchemes,
		Middlewares:           slices.Clone(rb.middlewares),
		ClientImplementations: slices.Clone(rb.clientImpls),
		pathMergeFields:       mustAssertMergeFields[Req]("SSERoute.ClientHandle", rb.pathMergeFields),
		queryMergeFields:      mustAssertMergeFields[Req]("SSERoute.ClientHandle", rb.queryMergeFields),
		headerMergeFields:     mustAssertMergeFields[Req]("SSERoute.ClientHandle", rb.headerMergeFields),
		cookieMergeFields:     mustAssertMergeFields[Req]("SSERoute.ClientHandle", rb.cookieMergeFields),
	}
	// Apply any inline Formats RouteOpt declared on the SSERoute -- the
	// SAME rb.respFormats field registerHandle applies server-side.
	// Without this, ClientHandle silently ignored a declared event
	// format and nethttp.Consume/CallSSEAdapter always fell back to
	// JSON regardless of what was declared (a confirmed bug).
	if rb.respFormats != nil {
		fmts, ok := rb.respFormats.([]format.Format[Event])
		if !ok {
			panic(fmt.Sprintf("api/rest: SSERoute.ClientHandle: %s", FormatOptError{Direction: "response",
				Err: fmt.Errorf("want []format.Format[%T], got %T", *new(Event), rb.respFormats)}.Error()))
		}
		h.WithFormats(fmts...)
	}
	return h
}

func (s SSERoute[Req, Event]) registerHandle(b *Server) (*SSERouteHandle[Req, Event], error) {
	if b.pathCodec != nil {
		if err := b.pathCodec.Validate(internal.StripTemplateVars(s.path)); err != nil {
			return nil, InvalidPathError{Path: s.path, Err: err}
		}
	}

	var rb routeBuilder
	for _, opt := range s.opts {
		opt.applyRoute(&rb)
	}

	if err := applyMiddlewareDeclarations(&rb, "GET "+s.path); err != nil {
		return nil, err
	}

	if err := checkImplementationsDeclared("GET "+s.path, rb.middlewares, rb.impls, rb.clientImpls); err != nil {
		return nil, err
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
		Descriptor:            frozen,
		Decode:                func(body []byte) (Req, error) { return jsonReq.Unmarshal(body) },
		EncodeEvent:           func(e Event) ([]byte, error) { return jsonEvent.Marshal(e) },
		DecodeEvent:           func(data []byte) (Event, error) { return jsonEvent.Unmarshal(data) },
		ValidateEvent:         func(e Event) error { return jsonEvent.Validate(e) },
		pathParams:            rb.pathParams,
		queryParams:           rb.queryParams,
		cookieParams:          rb.cookieParams,
		headerParams:          rb.headerParams,
		pathCodec:             b.pathCodec,
		SecuritySchemes:       rb.securitySchemes,
		GlobalSecurity:        slices.Clone(b.globalSecurity),
		Middlewares:           slices.Clone(rb.middlewares),
		HandlerFn:             rb.handlerFn,
		HandlerOpts:           rb.handlerOpts,
		Implementations:       slices.Clone(rb.impls),
		ClientImplementations: slices.Clone(rb.clientImpls),
		responseHeaderParams:  rb.respHeaders,
		responseCookieParams:  rb.respCookies,
		mergeFields:           eventMergeFields,
	}
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
	if rb.respFormats != nil {
		fmts, ok := rb.respFormats.([]format.Format[Event])
		if !ok {
			return nil, FormatOptError{Direction: "response",
				Err: fmt.Errorf("want []format.Format[%T], got %T", *new(Event), rb.respFormats)}
		}
		h.WithFormats(fmts...)
	}
	entry := &typedSSEEntry[Req, Event]{handle: h}
	b.mu.Lock()
	b.entries = append(b.entries, entry)
	b.mu.Unlock()
	return h, nil
}

// OpenAPISpec builds a complete OpenAPI 3.1 document from all registered routes.
// Returns an error if any non-empty SchemaName references a schema that will not
// be present in components/schemas (a dangling $ref).
//
// components.securitySchemes is aggregated from every registered route's own
// security declarations (via [Route.Use]/[middleware.SecurityScheme]/
// [FromSecurityScheme] — there is no builder-level security scheme
// store) — when two routes declare the same scheme name with different values,
// the LAST-registered route wins, with no error; define the scheme once as a
// shared package-level value (see [FromSecurityScheme]'s example) to avoid
// relying on this.
func (b *Server) OpenAPISpec() (openapi.Document, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
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

// RouteEntries returns every [Route] registered into b, as read-only
// [RouteEntry] views — for use by each adapter's internal serve dispatch
// (invoked via [nethttp.AttachMux]/[chi.AttachRouter]) to walk and
// wire the whole builder in one call. SSE entries are excluded; see
// [Server.SSEEntries].
func (b *Server) RouteEntries() []RouteEntry {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]RouteEntry, 0, len(b.entries))
	for _, e := range b.entries {
		if re, ok := e.(RouteEntry); ok {
			out = append(out, re)
		}
	}
	return out
}

// SSEEntries returns every [SSERoute] registered into b, as read-only
// [SSERouteEntry] views — for use by each adapter's internal serveSSE
// dispatch (invoked via [nethttp.AttachMux]/[chi.AttachRouter]).
// Regular Route entries are excluded; see [Server.RouteEntries].
func (b *Server) SSEEntries() []SSERouteEntry {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]SSERouteEntry, 0, len(b.entries))
	for _, e := range b.entries {
		if se, ok := e.(SSERouteEntry); ok {
			out = append(out, se)
		}
	}
	return out
}

// checkDanglingRefs verifies that every non-empty SchemaName used in routes
// resolves to a schema that will be registered in components/schemas.
// A name is resolvable when the accompanying Schema is non-nil, or when the
// name was explicitly registered via [Server.AddSchema].
func (b *Server) checkDanglingRefs() error {
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
