package reqreply

import (
	"fmt"
	"log/slog"

	"github.com/DaniDeer/go-codex/api/internal"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/schema"
)

// RouteOpt is the sealed interface for variadic [NewRoute] options.
//
// The following types implement RouteOpt:
//   - [RouteMeta] — operation metadata (OperationID, Summary, Description, Tags, schema names)
//   - [TopicParam] — topic template variable with optional codec and description
//   - [ErrorReplyMeta] — additional AsyncAPI reply error channel/message declarations
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

// MergedTopicParam is returned by [NewTopicParam]. It is the reqreply
// mirror of [rest.MergedPathParam]/[events.MergedTopicParam]: the
// registered field's setter merges the extracted topic variable into the
// decoded Req via [RouteHandle.DecodeMerged]; the getter extracts the
// topic variable's value from Req for the client-side single-call
// convenience (adapter-specific `CallHandle`, e.g. `mqtt5.CallHandle`/
// `zeromq.CallHandle`). Request-side only — see [routeBuilder.mergeFields].
type MergedTopicParam[Req any] struct {
	TopicParam
	field codex.FieldCodec[Req]
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
func NewTopicParam[Req any](
	name string,
	codec codex.Codec[string],
	get func(Req) string,
	set func(*Req, string),
) MergedTopicParam[Req] {
	return MergedTopicParam[Req]{
		TopicParam: TopicParam{Name: name, Codec: &codec},
		field:      codex.RequiredField(name, codec, get, set),
	}
}

// WithDescription sets the PARAMETER-level description and returns the
// updated value.
func (p MergedTopicParam[Req]) WithDescription(desc string) MergedTopicParam[Req] {
	p.Description = desc
	return p
}

func (p MergedTopicParam[Req]) applyRoute(rb *routeBuilder) {
	rb.topicParams = append(rb.topicParams, p.TopicParam)
	rb.mergeFields = append(rb.mergeFields, p.field)
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
}

func (m RouteMeta) applyRoute(rb *routeBuilder) { rb.meta = m }

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

// routeBuilder accumulates RouteOpt values before building the route.
type routeBuilder struct {
	meta         RouteMeta
	topicParams  []TopicParam
	errorReplies []ErrorReplyMeta
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

	return &RouteHandle[Req, Resp]{
		Topic:          r.topic,
		Decode:         func(p []byte) (Req, error) { return jsonReq.Unmarshal(p) },
		Encode:         func(v Resp) ([]byte, error) { return jsonResp.Marshal(v) },
		EncodeRequest:  func(v Req) ([]byte, error) { return jsonReq.Marshal(v) },
		DecodeResponse: func(p []byte) (Resp, error) { return jsonResp.Unmarshal(p) },
		topicParams:    rb.topicParams,
		mergeFields:    mustAssertMergeFields[Req]("ClientHandle", rb.mergeFields),
	}
}

// Register registers the route with b and returns a [RouteHandle].
//
// Returns [DuplicateRouteError] if a route with the same topic has already been
// registered with b.
//
// Use [RouteHandle.WithRequestFormats] and [RouteHandle.WithFormats] after
// Register to configure multi-format request/response handling.
func (r Route[Req, Resp]) Register(b *Builder) (*RouteHandle[Req, Resp], error) {
	if _, exists := b.topics[r.topic]; exists {
		return nil, DuplicateRouteError{Topic: r.topic}
	}

	var rb routeBuilder
	for _, opt := range r.opts {
		opt.applyRoute(&rb)
	}

	jsonReq := format.JSON(r.reqCodec)
	jsonResp := format.JSON(r.respCodec)

	h := &RouteHandle[Req, Resp]{
		Topic:          r.topic,
		Decode:         func(p []byte) (Req, error) { return jsonReq.Unmarshal(p) },
		Encode:         func(v Resp) ([]byte, error) { return jsonResp.Marshal(v) },
		EncodeRequest:  func(v Req) ([]byte, error) { return jsonReq.Marshal(v) },
		DecodeResponse: func(p []byte) (Resp, error) { return jsonResp.Unmarshal(p) },
		topicParams:    rb.topicParams,
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

	b.registerRoute(r.topic, r.reqCodec.Schema, r.respCodec.Schema, rb.meta, rb.errorReplies)
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

	// mergeFields holds the merge-capable fields registered via
	// [NewTopicParam] — see [MergeFields] and [DecodeMerged]. Request-side
	// only (see [routeBuilder.mergeFields] for the rationale).
	mergeFields []codex.FieldCodec[Req]
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
// Mirrors [events.ChannelHandle.BuildTopic].
//
//	topic, err := computeRoute.BuildTopic(map[string]string{"tenantID": "acme"})
//	// topic = "compute/acme/add"
func (h *RouteHandle[Req, Resp]) BuildTopic(vars map[string]string) (string, error) {
	codecMap := make(map[string]*codex.Codec[string], len(h.topicParams))
	for i := range h.topicParams {
		if h.topicParams[i].Codec != nil {
			codecMap[h.topicParams[i].Name] = h.topicParams[i].Codec
		}
	}
	return internal.BuildFromTemplate(h.Topic, vars, codecMap,
		func(name string) error { return MissingRouteParamError{Name: name} },
		func(name, value string, err error) error {
			return RouteParamError{Name: name, Value: value, Err: err}
		},
	)
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
	for i := range h.topicParams {
		p := &h.topicParams[i]
		if p.Codec == nil {
			continue
		}
		val, ok := vars[p.Name]
		if !ok {
			return MissingRouteParamError{Name: p.Name}
		}
		if err := p.Codec.Validate(val); err != nil {
			return RouteParamError{Name: p.Name, Value: val, Err: err}
		}
	}
	return nil
}

// RouteParamError is returned by [RouteHandle.BuildTopic] and
// [RouteHandle.ValidateTopicVars] when a topic variable fails its registered
// codec check. It mirrors [events.TopicParamError].
//
//	var paramErr reqreply.RouteParamError
//	if errors.As(err, &paramErr) {
//	    slog.Warn("bad topic var", "error", paramErr)
//	}
type RouteParamError struct {
	Name  string // the {varName} that failed
	Value string // the value that was rejected
	Err   error  // the underlying codec error
}

func (e RouteParamError) Error() string {
	return fmt.Sprintf("invalid value %q for topic variable {%s}: %v", e.Value, e.Name, e.Err)
}

// Unwrap allows [errors.Is] and [errors.As] to traverse the underlying error.
func (e RouteParamError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e RouteParamError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("name", e.Name),
		slog.String("value", e.Value),
		slog.Any("err", e.Err),
	)
}

// MissingRouteParamError is returned by [RouteHandle.BuildTopic] and
// [RouteHandle.ValidateTopicVars] when a required topic variable is absent from
// the vars map. It mirrors [events.MissingTopicVarError].
//
//	var missing reqreply.MissingRouteParamError
//	if errors.As(err, &missing) {
//	    slog.Warn("missing topic var", "name", missing.Name)
//	}
type MissingRouteParamError struct {
	// Name is the {varName} placeholder that was missing from vars.
	Name string
}

func (e MissingRouteParamError) Error() string {
	return fmt.Sprintf("missing topic variable {%s}", e.Name)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e MissingRouteParamError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("name", e.Name),
	)
}

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
