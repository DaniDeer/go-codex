package reqreply

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"unicode"

	"github.com/DaniDeer/go-codex/api/internal"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/schema"
)

// RouteOpt is the sealed interface for variadic [NewRoute] options.
//
// The following types implement RouteOpt:
//   - [RouteMeta] — operation metadata (OperationID, Summary, Description, Tags, schema names)
//   - [TopicParam] — topic template variable with optional codec and description
//   - [ErrorReplyMeta] — additional AsyncAPI reply error channel/message declarations (spec-only)
//   - [ErrorPattern] — codec-backed typed error reply (runtime dispatch + spec entry)
type RouteOpt interface{ applyRoute(*routeBuilder) }

// TopicParam describes a {varName} placeholder in a topic template.
// It is the [api/reqreply] analogue of [events.TopicParam].
//
// TopicParam is optional: [RouteHandle.BuildTopic] and [RouteHandle.ValidateTopicVars]
// use registered params to validate variable values. Use TopicParam when you want
// runtime codec validation on a specific variable.
//
// Note: all topic variables are always required — a template cannot be resolved
// without every {varName} placeholder present. There is no Required field.
//
// TopicParam implements [RouteOpt]: pass it directly to [NewRoute].
//
// Entry names must correspond to {varName} placeholders in the topic template.
//
//	var ComputeRoute = reqreply.NewRoute[ComputeReq, ComputeResp](
//	    "compute/{tenantID}/add",
//	    computeReqCodec, computeRespCodec,
//	    reqreply.RouteMeta{OperationID: "computeAdd"},
//	    reqreply.TopicParam{
//	        Name:        "tenantID",
//	        Description: "Tenant namespace for this computation.",
//	    }.WithCodec(codex.String().Refine(validate.NonEmptyString)),
//	)
//
// TopicParam mirrors [codex.Param]'s shape field-for-field — the shared,
// VALIDATE-ONLY escape hatch (see codex/param.go's own doc comment for the
// cross-package rationale). Kept as a flat, non-embedded struct (rather
// than embedding codex.Param) so existing `reqreply.TopicParam{Name: "id"}`
// struct literals keep compiling unchanged (Go requires keyed literal
// fields to be the struct's OWN fields, not promoted ones).
type TopicParam struct {
	// Name is the variable name (without braces) as it appears in the topic template.
	Name string
	// Description is shown in the AsyncAPI spec for this parameter.
	Description string
	// Codec validates topic parameter values at [RouteHandle.ValidateTopicVars] and
	// [RouteHandle.BuildTopic] time.
	// When non-nil, the codec's schema is also emitted in the AsyncAPI spec.
	// Nil means no runtime validation.
	Codec *codex.Codec[string]
}

func (p TopicParam) applyRoute(rb *routeBuilder) {
	rb.topicParams = append(rb.topicParams, p)
}

// toParam converts p to the shared [codex.Param] shape.
func (p TopicParam) toParam() codex.Param {
	return codex.Param{Name: p.Name, Description: p.Description, Codec: p.Codec}
}

// toCodexParams converts topicParams to []codex.Param for [codex.BuildFromParams]/
// [codex.ValidateParams]/[codex.ValidateDeclaredParams].
func toCodexParams(topicParams []TopicParam) []codex.Param {
	out := make([]codex.Param, len(topicParams))
	for i, p := range topicParams {
		out[i] = p.toParam()
	}
	return out
}

// MergedTopicParam is returned by [NewTopicParam]. It wraps
// [codex.MergedParam][Req] — the shared merge-capable counterpart to
// [TopicParam]. It is the reqreply mirror of
// [rest.MergedPathParam]/[events.MergedTopicParam]: the registered field's
// setter merges the extracted topic variable into the decoded Req via
// [RouteHandle.DecodeMerged]; the getter extracts the topic variable's
// value from Req for the client-side single-call convenience
// (adapter-specific `CallHandle`, e.g. `mqtt5.CallHandle`/
// `zeromq.CallHandle`). Request-side only — see [routeBuilder.mergeFields].
type MergedTopicParam[Req any] struct {
	codex.MergedParam[Req]
}

// NewTopicParam declares a topic variable that is BOTH validated against
// codec AND automatically merged into Req by [RouteHandle.DecodeMerged] —
// one declaration instead of a TopicParam plus a separate codex.Field. All
// topic variables are always required, matching plain [TopicParam]'s
// existing "no Required field" rationale.
//
//	reqreply.NewRoute[ComputeReq, ComputeResp]("compute/{tenantID}/add",
//	    computeReqCodec, computeRespCodec,
//	    reqreply.NewTopicParam("tenantID", codex.String().Refine(validate.NonEmptyString),
//	        func(r ComputeReq) string { return r.TenantID },
//	        func(r *ComputeReq, v string) { r.TenantID = v },
//	    ),
//	)
//
// V need not be string — see [codex.NewParam] for merging a topic segment
// directly into an int/UUID/etc.
func NewTopicParam[Req, V any](
	name string,
	codec codex.Codec[V],
	get func(Req) V,
	set func(*Req, V),
) MergedTopicParam[Req] {
	return MergedTopicParam[Req]{MergedParam: codex.NewParam(name, codec, get, set)}
}

// WithDescription sets the PARAMETER-level description and returns the
// updated value.
func (p MergedTopicParam[Req]) WithDescription(desc string) MergedTopicParam[Req] {
	p.MergedParam = p.MergedParam.WithDescription(desc)
	return p
}

func (p MergedTopicParam[Req]) applyRoute(rb *routeBuilder) {
	rb.topicParams = append(rb.topicParams, TopicParam{Name: p.Name, Description: p.Description, Codec: p.Codec})
	rb.mergeFields = append(rb.mergeFields, p.Field)
}

// requestFormatsOpt / formatsOpt are unexported RouteOpt implementations
// backing [RequestFormats] and [Formats] — see those constructors.
type requestFormatsOpt[Req any] struct{ fmts []format.Format[Req] }

func (o requestFormatsOpt[Req]) applyRoute(rb *routeBuilder) { rb.requestFormats = o.fmts }

// RequestFormats declares the formats a request-reply route accepts for
// request decoding — the [RouteOpt] equivalent of calling
// [RouteHandle.WithRequestFormats] after [Route.Register]. Declarable
// inline in [NewRoute]'s variadic opts, which means it also works through
// ports.ReqReplyPattern.Opts with zero changes to the ports package.
//
// A mismatched type is only detectable once Req is concrete —
// [Route.Register] returns [FormatOptError] in that case.
func RequestFormats[Req any](fmts ...format.Format[Req]) RouteOpt {
	return requestFormatsOpt[Req]{fmts: fmts}
}

type formatsOpt[Resp any] struct{ fmts []format.Format[Resp] }

func (o formatsOpt[Resp]) applyRoute(rb *routeBuilder) { rb.formats = o.fmts }

// Formats declares the formats a request-reply route can produce for
// response encoding — the [RouteOpt] equivalent of calling
// [RouteHandle.WithFormats] after [Route.Register]. See [RequestFormats].
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
	return fmt.Sprintf("api/reqreply: %s format option: %v", e.Direction, e.Err)
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
// registered via [NewTopicParam] has the wrong type parameter for the
// route's Req type — mirrors [rest.MergeFieldTypeError] exactly.
type MergeFieldTypeError struct {
	Err error
}

func (e MergeFieldTypeError) Error() string {
	return fmt.Sprintf("api/reqreply: merge field: %v", e.Err)
}

// Unwrap allows [errors.Is] and [errors.As] to reach the underlying error.
func (e MergeFieldTypeError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e MergeFieldTypeError) LogValue() slog.Value {
	return slog.GroupValue(slog.Any("err", e.Err))
}

// assertMergeFields type-asserts each element of raw (declared as []any on
// routeBuilder to keep the builder non-generic) against
// codex.FieldCodec[Req]. Returns MergeFieldTypeError on the first mismatch.
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
// (ClientHandle has no error return) — panics on a type mismatch.
func mustAssertMergeFields[Req any](caller string, raw []any) []codex.FieldCodec[Req] {
	fields, err := assertMergeFields[Req](raw)
	if err != nil {
		panic(fmt.Sprintf("api/reqreply: %s: %s", caller, err.Error()))
	}
	return fields
}

// WithCodec sets the validation codec and returns the updated TopicParam.
func (p TopicParam) WithCodec(c codex.Codec[string]) TopicParam { p.Codec = &c; return p }

// RouteMeta holds metadata for a [Route] registration. It controls the generated
// AsyncAPI operation IDs, summary, description, and schema refs.
//
// RouteMeta implements [RouteOpt]: pass it directly to [NewRoute].
type RouteMeta struct {
	// OperationID is the base name for the two generated operations.
	// The send operation is named "send<OperationID>" and the receive operation
	// is named "receive<OperationID>Reply". When empty, the topic is used
	// (e.g. "compute/add" → "sendComputeAdd" / "receiveComputeAddReply").
	OperationID string

	// Summary is a short human-readable summary of the route.
	Summary string

	// Description is a longer human-readable description for the send operation.
	Description string

	// Tags attach arbitrary labels to the operations in the AsyncAPI spec.
	Tags []string

	// ReqSchemaName, when non-empty, registers the request payload schema in
	// components/schemas and emits a $ref. Use to share schemas across routes.
	ReqSchemaName string

	// RespSchemaName, when non-empty, registers the response payload schema in
	// components/schemas and emits a $ref.
	RespSchemaName string

	// Security, when non-nil, overrides global security for this route.
	// Pass an empty slice to declare "no auth required" for this route.
	// nil (default) inherits global security declared via [Builder.AddGlobalSecurity].
	Security []route.SecurityRequirement
}

func (m RouteMeta) applyRoute(rb *routeBuilder) { rb.meta = m }

// SecurityScheme combines [route.SecurityScheme] spec metadata with optional
// runtime credential validation for request-reply adapters.
//
// [WithSecurityScheme] declares a SecurityScheme on a route — the ONLY way to
// declare one; there is no builder-level equivalent (mirrors
// [rest.WithSecurityScheme] and [events.WithSecurityScheme] exactly). The
// spec fields flow into the AsyncAPI document (aggregated from all
// registered routes by [Builder.AsyncAPISpec]); Codec, when non-nil, is used
// by MQTT5 adapters ([adapters/mqtt5/reqreply.Serve]/[adapters/mqtt5/reqreply.Call])
// to validate the raw credential string extracted from a message's User
// Properties before ServeOptions.SecurityFunc is called (server) or before
// the request is published (client, CallOptions.CredentialFunc).
//
// ZeroMQ's reqreply adapters ([adapters/zeromq]) have no per-message
// metadata channel (raw multipart frames only) — Codec-level extraction only
// applies to MQTT5; ZeroMQ reqreply security is a documented future gap.
//
// Use [SecurityScheme.WithCodec] to set the Codec field inline without a
// temporary variable:
// reqreply.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")}.WithCodec(c)
type SecurityScheme struct {
	route.SecurityScheme
	// Codec, when non-nil, validates the extracted raw credential string.
	// Nil means no format validation; SecurityFunc receives the message as-is.
	Codec *codex.Codec[string]
}

// WithCodec returns a copy of s with Codec set to c. It avoids the
// temporary-variable + address-of pattern required when setting Codec inline:
//
//	reqreply.WithSecurityScheme("bearerAuth", reqreply.SecurityScheme{
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
// THIS route. It is the ONLY way to declare a security scheme — there is no
// builder-level equivalent. Both [Route.Register] and [Route.ClientHandle]
// populate [RouteHandle.SecuritySchemes] from this declaration, so the SAME
// route value — including its security scheme — builds a server-side handle
// (Register) and a client-side handle (ClientHandle) with IDENTICAL
// credential-format enforcement on both sides. Mirrors [rest.WithSecurityScheme]
// and [events.WithSecurityScheme] exactly.
//
// Define a scheme once as a package-level value and reuse it across every
// route that shares it:
//
//	var bearerAuth = reqreply.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")}.
//	    WithCodec(codex.String().Refine(validate.BearerToken))
//
//	var ComputeRoute = reqreply.NewRoute[ComputeReq, ComputeResp](
//	    "compute/add", computeReqCodec, computeRespCodec,
//	    reqreply.RouteMeta{Security: []route.SecurityRequirement{route.Require("bearerAuth")}},
//	    reqreply.WithSecurityScheme("bearerAuth", bearerAuth),
//	)
//
// When multiple routes declare the SAME scheme name with DIFFERENT values,
// [Builder.AsyncAPISpec] resolves the conflict last-registered-wins (no
// error) — define the scheme once as a shared value (as above) to avoid
// this entirely.
func WithSecurityScheme(name string, scheme SecurityScheme) RouteOpt {
	return securitySchemeOpt{name: name, scheme: scheme}
}

// SecurityCredentialError is returned when credential format validation via
// SecurityScheme.Codec fails (MQTT5 only). It is distinct from [SecurityError],
// which wraps rejections from ServeOptions.SecurityFunc.
//
// Use [errors.As] to extract the scheme name and underlying constraint error:
//
//	var credErr reqreply.SecurityCredentialError
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

// LogValue implements [slog.LogValuer] for structured logging.
func (e SecurityCredentialError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("scheme", e.Scheme),
		slog.Any("err", e.Err),
	)
}

// SecurityError is returned when ServeOptions.SecurityFunc rejects a request.
// It is distinct from [SecurityCredentialError], which covers codec format failures.
//
// Use [errors.As] to extract the underlying error from SecurityFunc:
//
//	var secErr reqreply.SecurityError
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

// LogValue implements [slog.LogValuer] for structured logging.
func (e SecurityError) LogValue() slog.Value {
	return slog.GroupValue(slog.Any("err", e.Err))
}

// ErrorReplyMeta declares one additional reply error message for AsyncAPI rendering.
//
// It adds a dedicated reply-error channel+operation to the generated spec.
// Runtime adapter behavior is unchanged: this option is documentation/contract
// metadata only (same role as [RouteMeta]).
//
// Schema is required; when SchemaName is non-empty, the schema is emitted via
// $ref in components/schemas.
type ErrorReplyMeta struct {
	// Code identifies the error variant (e.g. "conflict", "validation").
	// It is used to derive channel/operation IDs when explicit IDs are not set.
	Code string
	// Description describes the error reply operation.
	Description string
	// Schema is the payload schema for this error reply message.
	Schema schema.Schema
	// SchemaName, when non-empty, emits a $ref and registers Schema in
	// components/schemas.
	SchemaName string
	// OperationID, when non-empty, overrides the generated receive operation ID.
	OperationID string
	// ChannelAddress, when non-empty, overrides the generated reply-error
	// channel address. Default: "<topic>/reply/error[/<code>]".
	ChannelAddress string
}

func (m ErrorReplyMeta) applyRoute(rb *routeBuilder) {
	rb.errorReplies = append(rb.errorReplies, m)
}

// ErrorPatternResponse is the adapter-ready payload produced by
// [RouteHandle.ErrorResponseFor] when a declared [ErrorPattern] matches.
type ErrorPatternResponse struct {
	// Body is the JSON-encoded typed error payload.
	Body []byte
	// Value is the typed payload before encoding — useful for adapters that
	// want to re-encode with a non-JSON format.
	Value any
}

// errorPatternRule is the type-erased runtime form of a declared
// [ErrorPattern], stored on [routeBuilder]/[RouteHandle].
type errorPatternRule struct {
	match func(error) (ErrorPatternResponse, bool, error)
}

// ErrorPatternOpt is the [RouteOpt] value returned by [ErrorPattern].
type ErrorPatternOpt[E error, B any] struct {
	codec          codex.Codec[B]
	mapper         func(E) (B, error)
	code           string
	description    string
	schemaName     string
	channelAddress string
	operationID    string
}

// ErrorPattern declares a codec-backed typed error reply for a matched error
// type — the request-reply analogue of [rest.ErrorPattern] and
// [events.ErrorChannel]. Unlike REST, reqreply has no HTTP status; the
// declaration is simply "when a handler error matches E, reply with this
// codec-backed payload" instead of a plain-text error string.
//
// Two modes, mirroring [rest.ErrorPattern]:
//   - Direct: no mapFn provided, E must be assignable to B.
//   - Mapped: mapFn(E) produces B.
//
// Matching is type-only via [errors.As]; the first declared ErrorPattern (in
// [NewRoute] option order) whose type matches wins — the same deterministic
// precedence used by REST/events.
//
// ErrorPattern ALSO drives the AsyncAPI reply-error channel/operation that
// [ErrorReplyMeta] previously had to be declared separately for — one
// declaration now produces both the runtime dispatch AND the spec entry.
// Use [ErrorPatternOpt.WithCode]/[ErrorPatternOpt.WithDescription]/
// [ErrorPatternOpt.WithSchemaName]/[ErrorPatternOpt.WithChannelAddress]/
// [ErrorPatternOpt.WithOperationID] to customize the generated spec entry
// (defaults mirror [ErrorReplyMeta]'s defaults). [ErrorReplyMeta] remains
// available unchanged for spec-only declarations that need no runtime
// dispatch (e.g. documenting an error reply produced by a different
// mechanism entirely).
//
//	reqreply.NewRoute[ComputeReq, ComputeResp]("compute/add", reqCodec, respCodec,
//	    reqreply.ErrorPattern[domain.ConflictError, ErrorPayload](errorPayloadCodec,
//	        func(e domain.ConflictError) (ErrorPayload, error) {
//	            return ErrorPayload{Code: "conflict", Message: e.Error()}, nil
//	        },
//	    ),
//	)
func ErrorPattern[E error, B any](
	codec codex.Codec[B],
	mapFn ...func(E) (B, error),
) ErrorPatternOpt[E, B] {
	var mapper func(E) (B, error)
	if len(mapFn) > 0 {
		mapper = mapFn[0]
	}
	return ErrorPatternOpt[E, B]{codec: codec, mapper: mapper}
}

// WithCode returns a copy of o with Code set — used to derive the generated
// reply-error channel/operation IDs and address (mirrors [ErrorReplyMeta.Code]).
// Defaults to a sanitized form of E's type name when not set.
func (o ErrorPatternOpt[E, B]) WithCode(code string) ErrorPatternOpt[E, B] {
	o.code = code
	return o
}

// WithDescription returns a copy of o with Description set (mirrors
// [ErrorReplyMeta.Description]).
func (o ErrorPatternOpt[E, B]) WithDescription(desc string) ErrorPatternOpt[E, B] {
	o.description = desc
	return o
}

// WithSchemaName returns a copy of o with SchemaName set — emits a $ref for
// the payload schema in components/schemas (mirrors [ErrorReplyMeta.SchemaName]).
func (o ErrorPatternOpt[E, B]) WithSchemaName(name string) ErrorPatternOpt[E, B] {
	o.schemaName = name
	return o
}

// WithChannelAddress returns a copy of o with ChannelAddress set, overriding
// the generated reply-error channel address (mirrors [ErrorReplyMeta.ChannelAddress]).
func (o ErrorPatternOpt[E, B]) WithChannelAddress(addr string) ErrorPatternOpt[E, B] {
	o.channelAddress = addr
	return o
}

// WithOperationID returns a copy of o with OperationID set, overriding the
// generated receive operation ID (mirrors [ErrorReplyMeta.OperationID]).
func (o ErrorPatternOpt[E, B]) WithOperationID(id string) ErrorPatternOpt[E, B] {
	o.operationID = id
	return o
}

func (o ErrorPatternOpt[E, B]) applyRoute(rb *routeBuilder) {
	code := o.code
	if code == "" {
		code = sanitizeTypeNameForCode(fmt.Sprintf("%T", *new(E)))
	}
	jsonCodec := format.JSON(o.codec)
	mapper := o.mapper
	schemaCopy := o.codec.Schema

	rule := errorPatternRule{
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
						fmt.Errorf("api/reqreply: ErrorPattern direct mode: %T not assignable to payload", target)
				}
			}

			body, encErr := jsonCodec.Marshal(payload)
			if encErr != nil {
				return ErrorPatternResponse{}, true, encErr
			}
			return ErrorPatternResponse{Body: body, Value: payload}, true, nil
		},
	}
	rb.errorPatternRules = append(rb.errorPatternRules, rule)
	rb.errorReplies = append(rb.errorReplies, ErrorReplyMeta{
		Code:           code,
		Description:    o.description,
		Schema:         schemaCopy,
		SchemaName:     o.schemaName,
		OperationID:    o.operationID,
		ChannelAddress: o.channelAddress,
	})
}

// sanitizeTypeNameForCode converts a %T-formatted type name (e.g.
// "domain.ConflictError") to a topic/ID-safe code segment (e.g.
// "conflictError") for default reply-error channel/operation derivation.
func sanitizeTypeNameForCode(typeName string) string {
	name := typeName
	if idx := strings.LastIndex(name, "."); idx >= 0 {
		name = name[idx+1:]
	}
	name = strings.TrimPrefix(name, "*")
	if name == "" {
		return "error"
	}
	runes := []rune(name)
	runes[0] = unicode.ToLower(runes[0])
	return string(runes)
}

// routeBuilder accumulates RouteOpt values before building the route.
type routeBuilder struct {
	meta         RouteMeta
	topicParams  []TopicParam
	errorReplies []ErrorReplyMeta
	// errorPatternRules holds per-route typed error reply declarations from
	// [ErrorPattern] — see [RouteHandle.ErrorResponseFor].
	errorPatternRules []errorPatternRule
	// requestFormats/formats hold []format.Format[Req]/[]format.Format[Resp]
	// type-erased (any) — set by [RequestFormats]/[Formats], resolved
	// generically in [Route.Register] where Req/Resp are concrete. See
	// [FormatOptError].
	requestFormats any
	formats        any
	// mergeFields holds type-erased codex.FieldCodec[Req] values registered
	// via [NewTopicParam] — resolved to []codex.FieldCodec[Req] in
	// [Route.Register]/[Route.ClientHandle]. Request-side only: reqreply
	// uses ONE shared topic template for both request and reply, and the
	// reply is correlated by the underlying transport (not by re-encoding
	// topic vars into Resp) — resolved design decision.
	mergeFields []any
	// securitySchemes holds this route's own [WithSecurityScheme]
	// declarations — the ONLY source of [RouteHandle.SecuritySchemes]
	// (there is no builder-level equivalent; mirrors rest's/events'
	// routeBuilder/channelBuilder.securitySchemes).
	securitySchemes map[string]SecurityScheme
}

// Topic is a reusable topic template + [TopicParam] shape, for the rare case
// where the SAME topic template and variable declarations are shared by two
// or more [Route] declarations of DIFFERENT Req/Resp type pairs (e.g. one
// topic family used for several distinct command types). Mirrors
// [events.Topic]/[rest.Path]/[ports.FilePathTemplate] exactly.
//
// The plain-string form passed directly to [NewRoute] is the default and
// stays that way — reach for Topic + [NewRouteFromTopic] only when reuse is
// the actual goal:
//
//	var deviceCmdTopic = reqreply.NewTopic("device/{deviceID}/cmd",
//	    reqreply.TopicParam{Name: "deviceID", Codec: deviceIDCodec},
//	)
//	var RebootRoute = reqreply.NewRouteFromTopic[RebootReq, RebootResp](
//	    deviceCmdTopic, rebootReqCodec, rebootRespCodec,
//	)
//	var UpdateRoute = reqreply.NewRouteFromTopic[UpdateReq, UpdateResp](
//	    deviceCmdTopic, updateReqCodec, updateRespCodec,
//	)
//
// A route declared via [NewRouteFromTopic] is byte-for-byte identical to one
// declared via [NewRoute] with the same template and [TopicParam] values
// passed inline — nothing downstream (adapters, Register, spec generation)
// can tell the difference. Topic captures ONLY the template+params shape;
// every other [RouteOpt] ([RouteMeta], [ErrorReplyMeta], [ErrorPattern],
// [WithSecurityScheme], …) is passed to [NewRouteFromTopic] exactly as it
// would be to [NewRoute].
type Topic struct {
	// Template is the topic template, e.g. "device/{deviceID}/cmd".
	Template string
	// Params holds the topic template's variable declarations.
	Params []TopicParam
}

// NewTopic declares a Topic from a template and its TopicParam variables.
func NewTopic(template string, params ...TopicParam) Topic {
	return Topic{Template: template, Params: params}
}

// BuildTopic substitutes {varName} placeholders in t.Template with the
// values in vars, validating each against its registered [TopicParam.Codec]
// (if any). Mirrors [RouteHandle.BuildTopic] exactly (same underlying
// engine, same error types), MINUS any builder-level topic codec — that
// only applies once a Topic-based route is registered via
// [NewRouteFromTopic] + [Route.Register], where it is enforced exactly as
// it would be for a plain-string route.
func (t Topic) BuildTopic(vars map[string]string) (string, error) {
	return codex.BuildFromParams(t.Template, toCodexParams(t.Params), vars)
}

// ValidateTopicVars validates extracted topic variable values against t's
// registered [TopicParam] codecs. Mirrors [RouteHandle.ValidateTopicVars]
// exactly (same error types); variables without a registered codec are
// skipped.
func (t Topic) ValidateTopicVars(vars map[string]string) error {
	return codex.ValidateParams(toCodexParams(t.Params), vars)
}

// Route[Req,Resp] is a typed request-reply route for async transports (ZeroMQ,
// MQTT 5, AMQP, etc.). It is the [api/reqreply] analogue of [rest.Route], which
// is for HTTP. The key difference is that a Route has a topic/address instead of
// an HTTP method and path.
//
// NewRoute is infallible — it only captures the spec. Validation runs at
// [Route.Register] time.
//
// Typical usage:
//
//	var ComputeRoute = reqreply.NewRoute[ComputeReq, ComputeResp](
//	    "compute/add",
//	    computeReqCodec, computeRespCodec,
//	    reqreply.RouteMeta{OperationID: "computeAdd", Summary: "Add two integers."},
//	)
//
//	// Register with a builder to get an AsyncAPI spec + a RouteHandle:
//	builder := reqreply.NewBuilder(reqreply.Info{Title: "API", Version: "1.0.0"})
//	builder.AddServer("zmq", reqreply.Server{URL: "tcp://...", Protocol: "zmq"})
//	handle, err := ComputeRoute.Register(builder)
//
//	// Adapters accept *reqreply.RouteHandle:
//	zmqadapter.Serve(ctx, sock, handle, fn, zmqadapter.ServeOptions{Observer: obs})
//	mqtt5adapter.ServeRequestReply(ctx, client, router, handle, fn, mqtt5.ServeOptions{Observer: obs})
type Route[Req, Resp any] struct {
	topic     string
	reqCodec  codex.Codec[Req]
	respCodec codex.Codec[Resp]
	opts      []RouteOpt
}

// NewRoute creates a [Route] spec from a topic, codecs, and variadic opts.
// NewRoute is infallible — validation runs at [Route.Register] time.
//
// NewRoute is a free function (not a method) because Go requires type parameters
// on free functions, not on method receivers.
func NewRoute[Req, Resp any](
	topic string,
	reqCodec codex.Codec[Req],
	respCodec codex.Codec[Resp],
	opts ...RouteOpt,
) Route[Req, Resp] {
	return Route[Req, Resp]{
		topic:     topic,
		reqCodec:  reqCodec,
		respCodec: respCodec,
		opts:      opts,
	}
}

// NewRouteFromTopic declares a Route using a pre-built [Topic] instead of a
// raw topic-template string — see [Topic]'s doc comment for when to reach
// for this. Produces the IDENTICAL [Route] [NewRoute] would produce from
// topic.Template plus topic.Params passed inline, since [TopicParam] already
// implements [RouteOpt].
func NewRouteFromTopic[Req, Resp any](
	topic Topic,
	reqCodec codex.Codec[Req],
	respCodec codex.Codec[Resp],
	opts ...RouteOpt,
) Route[Req, Resp] {
	allOpts := make([]RouteOpt, 0, len(topic.Params)+len(opts))
	for _, p := range topic.Params {
		allOpts = append(allOpts, p)
	}
	allOpts = append(allOpts, opts...)
	return NewRoute(topic.Template, reqCodec, respCodec, allOpts...)
}

// ClientHandle returns a [RouteHandle] for client-side use without registering
// with a [Builder]. No spec registration occurs.
//
// Use ClientHandle when only the client side needs codec and route definitions
// (no AsyncAPI spec, no server), or when sharing a [Route] definition between
// server and client in the same binary without a second builder registration.
//
// The returned handle has the same Decode / Encode / EncodeRequest / DecodeResponse
// codec helpers and BuildTopic / ValidateTopicVars methods as a handle returned
// by [Route.Register].
//
// Example — client-only usage (no builder required):
//
//	var ComputeRoute = reqreply.NewRoute[ComputeReq, ComputeResp](
//	    "compute/add", computeReqCodec, computeRespCodec,
//	)
//
//	// Client side — no builder needed.
//	handle := ComputeRoute.ClientHandle()
//	resp, err := mqtt5adapter.Call(ctx, client, router, handle, req, mqtt5adapter.CallOptions{})
//
// Mirrors [rest.Route.ClientHandle].
func (r Route[Req, Resp]) ClientHandle() *RouteHandle[Req, Resp] {
	var rb routeBuilder
	for _, opt := range r.opts {
		opt.applyRoute(&rb)
	}

	jsonReq := format.JSON(r.reqCodec)
	jsonResp := format.JSON(r.respCodec)

	schemes := make(map[string]SecurityScheme, len(rb.securitySchemes))
	for k, v := range rb.securitySchemes {
		schemes[k] = v
	}

	return &RouteHandle[Req, Resp]{
		Topic:             r.topic,
		Decode:            func(p []byte) (Req, error) { return jsonReq.Unmarshal(p) },
		Encode:            func(v Resp) ([]byte, error) { return jsonResp.Marshal(v) },
		EncodeRequest:     func(v Req) ([]byte, error) { return jsonReq.Marshal(v) },
		DecodeResponse:    func(p []byte) (Resp, error) { return jsonResp.Unmarshal(p) },
		topicParams:       rb.topicParams,
		mergeFields:       mustAssertMergeFields[Req]("ClientHandle", rb.mergeFields),
		errorPatternRules: rb.errorPatternRules,
		Security:          rb.meta.Security,
		SecuritySchemes:   schemes,
	}
}

// Register registers the route with b and returns a [RouteHandle].
//
// Returns [DuplicateRouteError] if a route with the same topic has already been
// registered with b.
//
// If b was created with [WithTopicCodec] or [WithTopicConstraints], the
// topic is validated immediately and an [InvalidTopicError] is returned if
// it fails — no route is registered in that case.
//
// Use [RouteHandle.WithRequestFormats] and [RouteHandle.WithFormats] after
// Register to configure multi-format request/response handling.
func (r Route[Req, Resp]) Register(b *Builder) (*RouteHandle[Req, Resp], error) {
	if _, exists := b.topics[r.topic]; exists {
		return nil, DuplicateRouteError{Topic: r.topic}
	}

	if b.topicCodec != nil {
		if err := b.topicCodec.Validate(internal.StripTemplateVars(r.topic)); err != nil {
			return nil, InvalidTopicError{Topic: r.topic, Err: err}
		}
	}

	var rb routeBuilder
	for _, opt := range r.opts {
		opt.applyRoute(&rb)
	}

	if err := codex.ValidateDeclaredParams(r.topic, toCodexParams(rb.topicParams)); err != nil {
		return nil, err
	}

	jsonReq := format.JSON(r.reqCodec)
	jsonResp := format.JSON(r.respCodec)

	schemes := make(map[string]SecurityScheme, len(rb.securitySchemes))
	for k, v := range rb.securitySchemes {
		schemes[k] = v
	}

	h := &RouteHandle[Req, Resp]{
		Topic:             r.topic,
		Decode:            func(p []byte) (Req, error) { return jsonReq.Unmarshal(p) },
		Encode:            func(v Resp) ([]byte, error) { return jsonResp.Marshal(v) },
		EncodeRequest:     func(v Req) ([]byte, error) { return jsonReq.Marshal(v) },
		DecodeResponse:    func(p []byte) (Resp, error) { return jsonResp.Unmarshal(p) },
		topicParams:       rb.topicParams,
		topicCodec:        b.topicCodec,
		errorPatternRules: rb.errorPatternRules,
		Security:          rb.meta.Security,
		SecuritySchemes:   schemes,
		GlobalSecurity:    append([]route.SecurityRequirement(nil), b.globalSecurity...),
	}

	if rb.requestFormats != nil {
		fmts, ok := rb.requestFormats.([]format.Format[Req])
		if !ok {
			return nil, FormatOptError{Direction: "request",
				Err: fmt.Errorf("want []format.Format[%T], got %T", *new(Req), rb.requestFormats)}
		}
		h.WithRequestFormats(fmts...)
	}
	if rb.formats != nil {
		fmts, ok := rb.formats.([]format.Format[Resp])
		if !ok {
			return nil, FormatOptError{Direction: "response",
				Err: fmt.Errorf("want []format.Format[%T], got %T", *new(Resp), rb.formats)}
		}
		h.WithFormats(fmts...)
	}
	var mergeErr error
	h.mergeFields, mergeErr = assertMergeFields[Req](rb.mergeFields)
	if mergeErr != nil {
		return nil, mergeErr
	}

	b.registerRoute(r.topic, r.reqCodec.Schema, r.respCodec.Schema, rb.meta, rb.errorReplies, rb.topicParams)
	// Merge this route's own WithSecurityScheme declarations into the
	// builder's aggregate — last-registered-wins on name collision,
	// matching rest's/events' documented policy. There is no per-route
	// entry list to iterate at AsyncAPISpec() time (unlike rest/events),
	// so schemes are accumulated directly here instead.
	for name, s := range schemes {
		b.securitySchemes[name] = s
	}
	return h, nil
}

// RouteHandle is returned by [Route.Register]. It holds the codec-backed
// Decode/Encode helpers and is passed directly to request-reply adapters
// ([adapters/zeromq], [adapters/mqtt5]).
//
// RouteHandle mirrors [rest.RouteHandle] and [events.ChannelHandle]: it is a
// value that callers pass around and store. No magic, no global state.
type RouteHandle[Req, Resp any] struct {
	// Topic is the request address (e.g. "compute/add").
	Topic string

	// Decode deserialises and validates a JSON request payload into Req.
	// All Refine constraints on the request codec run automatically.
	Decode func(payload []byte) (Req, error)

	// Encode serialises Resp to JSON bytes.
	Encode func(resp Resp) ([]byte, error)

	// EncodeRequest serialises Req to JSON bytes for use as an outgoing request
	// payload. It is the client-side complement of Decode.
	EncodeRequest func(req Req) ([]byte, error)

	// DecodeResponse deserialises and validates a JSON reply payload into Resp.
	// It is the client-side complement of Encode.
	DecodeResponse func(payload []byte) (Resp, error)

	// RequestFormats, when non-empty, overrides the default JSON format for
	// decoding incoming request payloads. The adapter uses RequestFormats[0]
	// instead of Decode when present.
	// Configure via [RouteHandle.WithRequestFormats].
	RequestFormats []format.Format[Req]

	// Formats, when non-empty, overrides the default JSON format for encoding
	// reply payloads. The adapter uses Formats[0] instead of Encode when present.
	// Configure via [RouteHandle.WithFormats].
	Formats []format.Format[Resp]

	// topicParams holds per-variable params registered via [TopicParam] options.
	topicParams []TopicParam

	// topicCodec is the builder-level topic codec (may be nil), set via
	// [WithTopicCodec]/[WithTopicConstraints] on the [Builder] this handle
	// was registered with. nil when the handle came from [Route.ClientHandle]
	// (no Builder to source it from).
	topicCodec *codex.Codec[string]

	// mergeFields holds the merge-capable fields registered via
	// [NewTopicParam] — see [MergeFields] and [DecodeMerged]. Request-side
	// only (see [routeBuilder.mergeFields] for the rationale).
	mergeFields []codex.FieldCodec[Req]

	// errorPatternRules holds per-route typed error reply declarations from
	// [ErrorPattern] — see [ErrorResponseFor].
	errorPatternRules []errorPatternRule

	// Security holds this route's own effective security requirements — nil
	// means "inherit GlobalSecurity", an empty (non-nil) slice means
	// "explicitly no auth required". Set from [RouteMeta.Security].
	// Adapters resolve the effective requirements as:
	//   reqs := handle.Security
	//   if reqs == nil { reqs = handle.GlobalSecurity }
	Security []route.SecurityRequirement

	// SecuritySchemes maps scheme name to SecurityScheme (with runtime Codec).
	// Populated from the route's own [WithSecurityScheme] declarations — the
	// ONLY source (there is no builder-level equivalent). Adapters use this
	// map to extract and validate credentials per scheme.
	SecuritySchemes map[string]SecurityScheme

	// GlobalSecurity holds the builder-level security requirements that apply
	// when Security is nil (i.e. the route inherits global security). Set
	// via [Builder.AddGlobalSecurity]. nil when no global security is
	// declared, or when the handle came from [Route.ClientHandle] (no
	// Builder to source it from).
	GlobalSecurity []route.SecurityRequirement
}

// ErrorResponseFor returns the first declared [ErrorPattern] match for err
// (matching via [errors.As], in declaration order), or
// (ErrorPatternResponse{}, false, nil) when none match.
//
// A non-nil third return value indicates the matched pattern's mapping or
// encoding failed — callers should treat this as a terminal error for that
// pattern (do not fall through to other patterns).
func (h *RouteHandle[Req, Resp]) ErrorResponseFor(err error) (ErrorPatternResponse, bool, error) {
	if err == nil {
		return ErrorPatternResponse{}, false, nil
	}
	for _, rule := range h.errorPatternRules {
		if rule.match == nil {
			continue
		}
		resp, matched, matchErr := rule.match(err)
		if !matched {
			continue
		}
		return resp, true, matchErr
	}
	return ErrorPatternResponse{}, false, nil
}

// MergeFields returns the merge-capable fields registered via
// [NewTopicParam] — feed them directly into [codex.DecodeVars]/
// [codex.EncodeVars], or use [RouteHandle.DecodeMerged] for the
// closed-loop convenience method.
func (h *RouteHandle[Req, Resp]) MergeFields() []codex.FieldCodec[Req] {
	return h.mergeFields
}

// DecodeMerged decodes the request payload (via the route's registered
// format) AND merges every [NewTopicParam]-registered topic variable into
// the SAME Req value, using [codex.DecodeVars] internally — the reqreply
// mirror of [rest.RouteHandle.DecodeMerged]/[events.ChannelHandle.DecodeMerged].
// Additive — [RouteHandle.Decode] is unchanged; DecodeMerged behaves
// identically to a bare Decode when the route declares no merge-capable
// topic params (MergeFields() is empty).
//
// The payload decode error (if any) is returned FIRST, before the
// topic-var merge step runs — matching the REST/events precedent. The
// merge step itself collects every field's failure via [codex.DecodeVars]
// (never stops at the first one).
func (h *RouteHandle[Req, Resp]) DecodeMerged(payload []byte, topicVars map[string]string) (Req, error) {
	var req Req
	var err error
	if len(payload) > 0 {
		req, err = h.Decode(payload)
		if err != nil {
			return req, err
		}
	}
	if len(h.mergeFields) == 0 {
		return req, nil
	}
	if err := codex.DecodeVars(&req, topicVars, h.mergeFields...); err != nil {
		return req, err
	}
	return req, nil
}

// WithRequestFormats sets the formats the route accepts for request body decoding
// and returns the updated handle. Adapters use RequestFormats[0] for decoding
// when non-empty, falling back to [RouteHandle.Decode] (JSON) otherwise.
//
// Mirrors [rest.RouteHandle.WithRequestFormats].
func (h *RouteHandle[Req, Resp]) WithRequestFormats(fmts ...format.Format[Req]) *RouteHandle[Req, Resp] {
	h.RequestFormats = append(h.RequestFormats[:0:0], fmts...)
	return h
}

// WithFormats sets the formats used for encoding reply payloads and returns
// the updated handle. Adapters use Formats[0] for encoding when non-empty,
// falling back to [RouteHandle.Encode] (JSON) otherwise.
//
// Mirrors [rest.RouteHandle.WithFormats].
func (h *RouteHandle[Req, Resp]) WithFormats(fmts ...format.Format[Resp]) *RouteHandle[Req, Resp] {
	h.Formats = append(h.Formats[:0:0], fmts...)
	return h
}

// BuildTopic substitutes {varName} placeholders in the route's topic template
// with the values provided in vars, validating each against its registered
// [TopicParam] codec (if any).
//
// All template variables must be present in vars; missing variables return a
// [MissingRouteParamError]. Values are validated before substitution; codec
// failures return a [RouteParamError] identifying the variable name and value.
// Keys in vars that do not appear in the template are silently ignored.
//
// If the builder was created with [WithTopicCodec] or [WithTopicConstraints],
// the final assembled topic is also validated against that codec. A failure
// returns an [InvalidTopicError] with the concrete topic (not the template).
//
// Mirrors [events.ChannelHandle.BuildTopic].
//
//	topic, err := computeRoute.BuildTopic(map[string]string{"tenantID": "acme"})
//	// topic = "compute/acme/add"
func (h *RouteHandle[Req, Resp]) BuildTopic(vars map[string]string) (string, error) {
	result, err := codex.BuildFromParams(h.Topic, toCodexParams(h.topicParams), vars)
	if err != nil {
		return "", err
	}
	if h.topicCodec != nil {
		if err := h.topicCodec.Validate(result); err != nil {
			return "", InvalidTopicError{Topic: result, Err: err}
		}
	}
	return result, nil
}

// ValidateTopic validates a received concrete topic string against the
// builder-level topic codec (set via [WithTopicCodec] or
// [WithTopicConstraints]).
//
// Call this after a wildcard subscription/reply delivers a message to
// verify the concrete topic satisfies the same constraints applied at route
// registration time. Returns [InvalidTopicError] on failure; returns nil if
// no topic codec is registered.
//
// Mirrors [events.ChannelHandle.ValidateTopic].
func (h *RouteHandle[Req, Resp]) ValidateTopic(topic string) error {
	if h.topicCodec == nil {
		return nil
	}
	if err := h.topicCodec.Validate(topic); err != nil {
		return InvalidTopicError{Topic: topic, Err: err}
	}
	return nil
}

// ValidateTopicVars validates extracted topic variable values against the
// registered [TopicParam] codecs. Call this after extracting vars from an
// incoming request topic to ensure each variable satisfies its codec constraints.
//
// Returns [RouteParamError] for the first variable that fails its codec.
// Variables without a registered codec are skipped.
// Missing required variables return [MissingRouteParamError].
//
// Mirrors [events.ChannelHandle.ValidateTopicVars].
func (h *RouteHandle[Req, Resp]) ValidateTopicVars(vars map[string]string) error {
	return codex.ValidateParams(toCodexParams(h.topicParams), vars)
}

// RouteParamError is returned by [RouteHandle.BuildTopic] and
// [RouteHandle.ValidateTopicVars] when a topic variable fails its registered
// codec check. A type ALIAS for [codex.ParamError] — the SAME underlying
// type, so existing errors.As(&reqreply.RouteParamError{}) calls keep
// working unchanged; see codex/param.go for the canonical definition.
//
//	var paramErr reqreply.RouteParamError
//	if errors.As(err, &paramErr) {
//	    slog.Warn("bad topic var", "error", paramErr)
//	}
type RouteParamError = codex.ParamError

// MissingRouteParamError is returned by [RouteHandle.BuildTopic] and
// [RouteHandle.ValidateTopicVars] when a required topic variable is absent from
// the vars map. A type ALIAS for [codex.MissingParamError] — see
// [RouteParamError]'s own doc comment for the rationale.
//
//	var missing reqreply.MissingRouteParamError
//	if errors.As(err, &missing) {
//	    slog.Warn("missing topic var", "name", missing.Name)
//	}
type MissingRouteParamError = codex.MissingParamError

// InvalidRouteParamError is returned by [Route.Register] when a [TopicParam]
// entry names a variable that does not appear in the topic template — a
// NEW check reqreply gains for free from the shared codex.Param foundation
// (rest/events already had the equivalent InvalidPathParamError/
// InvalidTopicParamError checks; reqreply never did until now). A type
// ALIAS for [codex.InvalidParamError].
//
//	var paramErr reqreply.InvalidRouteParamError
//	if errors.As(err, &paramErr) {
//	    slog.Warn("TopicParam not in template", "name", paramErr.Name, "template", paramErr.Template)
//	}
type InvalidRouteParamError = codex.InvalidParamError

// DuplicateRouteError is returned by [Route.Register] when a route with the
// same topic has already been registered with the [Builder].
//
//	var dup reqreply.DuplicateRouteError
//	if errors.As(err, &dup) {
//	    slog.Error("duplicate route", "topic", dup.Topic)
//	}
type DuplicateRouteError struct {
	// Topic is the topic that was registered more than once.
	Topic string
}

func (e DuplicateRouteError) Error() string {
	return fmt.Sprintf("reqreply: route %q already registered", e.Topic)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e DuplicateRouteError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("topic", e.Topic),
	)
}

// InvalidTopicError is returned by [Route.Register], [RouteHandle.BuildTopic],
// and [RouteHandle.ValidateTopic] when a topic fails builder-level topic
// codec validation (set via [WithTopicCodec]/[WithTopicConstraints]).
//
// Use errors.As to extract it and inspect the failing topic or the
// underlying constraint error:
//
//	var topicErr reqreply.InvalidTopicError
//	if errors.As(err, &topicErr) {
//	    slog.Error("bad topic", "topic", topicErr.Topic, "err", topicErr.Err)
//	}
//
// Mirrors [events.InvalidTopicError].
type InvalidTopicError struct {
	Topic string // the topic that failed validation
	Err   error  // the underlying constraint or codec error
}

func (e InvalidTopicError) Error() string {
	return fmt.Sprintf("reqreply: invalid topic %q: %s", e.Topic, e.Err.Error())
}

// Unwrap allows errors.As and errors.Is to traverse the underlying constraint error.
func (e InvalidTopicError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e InvalidTopicError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("topic", e.Topic),
		slog.Any("err", e.Err),
	)
}
