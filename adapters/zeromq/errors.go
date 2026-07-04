package zeromq

import (
	"fmt"
	"log/slog"
)

// ErrorKind classifies the origin of a [SubscribeError] or [ServeError].
type ErrorKind int

const (
	// KindDecode indicates the message payload could not be decoded or
	// failed codec validation.
	KindDecode ErrorKind = iota

	// KindHandler indicates the application handler returned an error after
	// successful decoding.
	KindHandler

	// KindEncode indicates the response could not be encoded (server side)
	// or the outgoing payload failed codec validation (publish side).
	KindEncode
)

func (k ErrorKind) String() string {
	switch k {
	case KindDecode:
		return "decode"
	case KindHandler:
		return "handler"
	case KindEncode:
		return "encode"
	default:
		return "unknown"
	}
}

// SubscribeError is delivered to [SubscribeOptions.OnError] with a typed [Kind]
// so callers can distinguish decode/validation failures from application errors
// without string matching.
//
// Use [errors.As] to extract the kind, topic, and underlying error:
//
//	var subErr zeromq.SubscribeError
//	if errors.As(err, &subErr) {
//	    slog.Warn("subscribe failed", "error", subErr) // LogValue emits structured fields
//	}
type SubscribeError struct {
	Kind  ErrorKind
	Topic string
	Err   error
}

func (e SubscribeError) Error() string {
	return fmt.Sprintf("zeromq subscribe %s %s: %v", e.Kind, e.Topic, e.Err)
}

// Unwrap allows [errors.Is] and [errors.As] to traverse the underlying error.
func (e SubscribeError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e SubscribeError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("kind", e.Kind.String()),
		slog.String("topic", e.Topic),
		slog.Any("err", e.Err),
	)
}

// PublishEncodeError is returned by [Publish] when encoding the outgoing
// message payload fails (codec validation or marshal error).
//
// Use [errors.As] to extract the topic and underlying error:
//
//	var encErr zeromq.PublishEncodeError
//	if errors.As(err, &encErr) {
//	    slog.Error("publish encode failed", "error", encErr)
//	}
type PublishEncodeError struct {
	// Topic is the resolved topic (after template substitution if vars were provided).
	Topic string
	// Err is the underlying codec validation or marshal error.
	Err error
}

func (e PublishEncodeError) Error() string {
	return fmt.Sprintf("zeromq encode %s: %v", e.Topic, e.Err)
}

// Unwrap allows [errors.Is] and [errors.As] to traverse the underlying error.
func (e PublishEncodeError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e PublishEncodeError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("topic", e.Topic),
		slog.Any("err", e.Err),
	)
}

// ServeError is delivered to [ServeOptions.OnError] on REP-side failures.
// The Kind field identifies whether the failure occurred during decode,
// handler execution, or response encoding.
//
//	var serveErr zeromq.ServeError
//	if errors.As(err, &serveErr) {
//	    slog.Warn("serve failed", "error", serveErr)
//	}
type ServeError struct {
	Kind ErrorKind
	Err  error
}

func (e ServeError) Error() string {
	return fmt.Sprintf("zeromq serve %s: %v", e.Kind, e.Err)
}

// Unwrap allows [errors.Is] and [errors.As] to traverse the underlying error.
func (e ServeError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e ServeError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("kind", e.Kind.String()),
		slog.Any("err", e.Err),
	)
}

// CallError wraps REQ-socket failures: encode, send, receive, or decode.
//
//	var callErr zeromq.CallError
//	if errors.As(err, &callErr) {
//	    slog.Error("zmq call failed", "error", callErr)
//	}
type CallError struct {
	Err error
}

func (e CallError) Error() string { return fmt.Sprintf("zeromq call: %v", e.Err) }

// Unwrap allows [errors.Is] and [errors.As] to traverse the underlying error.
func (e CallError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e CallError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.Any("err", e.Err),
	)
}

// SocketError wraps socket-level infrastructure failures: socket option
// configuration (SetSubscription, SetRecvTimeout) and transport I/O (recv, send).
// It is distinct from codec-level errors (SubscribeError, ServeError, CallError)
// which reflect application-layer decode/handler/encode failures.
//
// The Op field identifies which socket operation failed so callers can distinguish
// a recv failure from a configuration failure without string matching:
//
//	var sockErr zeromq.SocketError
//	if errors.As(err, &sockErr) {
//	    switch sockErr.Op {
//	    case "recv":               // socket connection died mid-loop
//	    case "send":               // socket send failed
//	    case "set_recv_timeout":   // could not configure socket option
//	    case "set_subscription":   // could not set SUB filter
//	    }
//	    slog.Error("socket failed", "error", sockErr)
//	}
type SocketError struct {
	// Op identifies the socket operation that failed.
	// Common values: "set_subscription", "set_recv_timeout", "recv", "send".
	Op string
	// Err is the underlying socket or OS error.
	Err error
}

func (e SocketError) Error() string {
	return fmt.Sprintf("zeromq socket %s: %v", e.Op, e.Err)
}

// Unwrap allows [errors.Is] and [errors.As] to traverse the underlying error.
func (e SocketError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e SocketError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("op", e.Op),
		slog.Any("err", e.Err),
	)
}
