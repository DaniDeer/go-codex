package reqreply

import (
	"fmt"
	"log/slog"

	"github.com/DaniDeer/go-codex/api/internal"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
)

// RouteOpt is the sealed interface for variadic [NewRoute] options.
//
// The following types implement RouteOpt:
//   - [RouteMeta] — operation metadata (OperationID, Summary, Description, Tags, schema names)
//   - [TopicParam] — topic template variable with optional codec and description
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

// routeBuilder accumulates RouteOpt values before building the route.
type routeBuilder struct {
	meta        RouteMeta
	topicParams []TopicParam
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

	b.registerRoute(r.topic, r.reqCodec.Schema, r.respCodec.Schema, rb.meta)
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
