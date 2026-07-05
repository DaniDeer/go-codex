package mqtt5

import (
	"fmt"
	"log/slog"
)

// ErrorKind classifies the origin of a [SubscribeError], [RequestError], or
// [ServeRequestReplyError].
type ErrorKind int

const (
	// KindDecode indicates the message payload could not be decoded or failed
	// codec validation.
	KindDecode ErrorKind = iota

	// KindHandler indicates the application handler returned an error after
	// successful decoding.
	KindHandler

	// KindEncode indicates the response or outgoing payload failed codec
	// validation or encoding.
	KindEncode

	// KindTimeout indicates the [Request] call did not receive a reply within
	// the configured deadline.
	KindTimeout

	// KindSecurity indicates the SecurityFunc rejected the message.
	KindSecurity
)

func (k ErrorKind) String() string {
	switch k {
	case KindDecode:
		return "decode"
	case KindHandler:
		return "handler"
	case KindEncode:
		return "encode"
	case KindTimeout:
		return "timeout"
	case KindSecurity:
		return "security"
	default:
		return "unknown"
	}
}

// SubscribeError is delivered to [SubscribeOptions.OnError] with a typed [Kind]
// so callers can distinguish decode/validation failures from application handler
// or security errors without string matching.
//
//	var subErr mqtt5.SubscribeError
//	if errors.As(err, &subErr) {
//	    slog.Warn("subscribe failed", "error", subErr)
//	}
type SubscribeError struct {
	Kind  ErrorKind
	Topic string
	Err   error
}

func (e SubscribeError) Error() string {
	return fmt.Sprintf("mqtt5 subscribe %s %s: %v", e.Kind, e.Topic, e.Err)
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

// PublishEncodeError is returned by [Publish] when encoding the outgoing message
// payload fails (codec validation or marshal error).
//
//	var encErr mqtt5.PublishEncodeError
//	if errors.As(err, &encErr) {
//	    slog.Error("mqtt5 publish encode failed", "error", encErr)
//	}
type PublishEncodeError struct {
	// Topic is the resolved publish topic (after template substitution).
	Topic string
	// Err is the underlying codec validation or marshal error.
	Err error
}

func (e PublishEncodeError) Error() string {
	return fmt.Sprintf("mqtt5 encode %s: %v", e.Topic, e.Err)
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

// RequestError wraps requester-side failures in [Request]: encode, send,
// timeout, or reply decode failures.
//
//	var reqErr mqtt5.RequestError
//	if errors.As(err, &reqErr) {
//	    if reqErr.Kind == mqtt5.KindTimeout {
//	        // retry or report timeout
//	    }
//	    slog.Error("mqtt5 request failed", "error", reqErr)
//	}
type RequestError struct {
	Kind ErrorKind
	Err  error
}

func (e RequestError) Error() string {
	return fmt.Sprintf("mqtt5 request %s: %v", e.Kind, e.Err)
}

// Unwrap allows [errors.Is] and [errors.As] to traverse the underlying error.
func (e RequestError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e RequestError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("kind", e.Kind.String()),
		slog.Any("err", e.Err),
	)
}

// ServeRequestReplyError is delivered to [ServeOptions.OnError] on
// responder-side failures: decode, handler, or reply-encode failures.
//
//	var serveErr mqtt5.ServeRequestReplyError
//	if errors.As(err, &serveErr) {
//	    slog.Warn("mqtt5 serve failed", "error", serveErr)
//	}
type ServeRequestReplyError struct {
	Kind ErrorKind
	Err  error
}

func (e ServeRequestReplyError) Error() string {
	return fmt.Sprintf("mqtt5 serve %s: %v", e.Kind, e.Err)
}

// Unwrap allows [errors.Is] and [errors.As] to traverse the underlying error.
func (e ServeRequestReplyError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e ServeRequestReplyError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("kind", e.Kind.String()),
		slog.Any("err", e.Err),
	)
}

// UserPropertyError is returned when a [UserPropertyParam] codec validation
// fails on an incoming MQTT 5 message. It mirrors [rest.HeaderParamError] for
// HTTP headers.
//
//	var propErr mqtt5.UserPropertyError
//	if errors.As(err, &propErr) {
//	    slog.Warn("user property invalid", "error", propErr)
//	}
type UserPropertyError struct {
	// Name is the User Property key that failed validation.
	Name string
	// Value is the raw string value that was rejected.
	Value string
	// Err is the underlying codec constraint error.
	Err error
}

func (e UserPropertyError) Error() string {
	return fmt.Sprintf("mqtt5 user property %q: invalid value %q: %v", e.Name, e.Value, e.Err)
}

// Unwrap allows [errors.Is] and [errors.As] to traverse the underlying error.
func (e UserPropertyError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e UserPropertyError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("name", e.Name),
		slog.String("value", e.Value),
		slog.Any("err", e.Err),
	)
}

// MissingUserPropertyError is returned when a required [UserPropertyParam] is
// absent from an incoming MQTT 5 message. It mirrors [events.MissingTopicVarError].
//
//	var missing mqtt5.MissingUserPropertyError
//	if errors.As(err, &missing) {
//	    slog.Warn("required user property missing", "name", missing.Name)
//	}
type MissingUserPropertyError struct {
	// Name is the User Property key that was required but not present.
	Name string
}

func (e MissingUserPropertyError) Error() string {
	return fmt.Sprintf("mqtt5 user property %q: required but not present", e.Name)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e MissingUserPropertyError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("name", e.Name),
	)
}
