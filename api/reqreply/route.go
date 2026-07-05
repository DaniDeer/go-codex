package reqreply

import (
	"fmt"
	"log/slog"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
)

// RouteOpt is the sealed interface for variadic [NewRoute] options.
//
// The following types implement RouteOpt:
//   - [RouteMeta] — operation metadata (OperationID, Summary, Description, Tags, schema names)
type RouteOpt interface{ applyRoute(*routeBuilder) }

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
	meta RouteMeta
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
