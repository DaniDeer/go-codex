// Package mqtt adapts [api/events] channel handles to [Paho MQTT] callbacks.
//
// [SubscribeHandler] turns a [events.ChannelHandle] into an [mqtt.MessageHandler]
// that decodes and validates incoming payloads before calling the application
// handler. [Publish] encodes a value and publishes it to the broker.
//
// Typical usage:
//
//	b := events.NewBuilder(events.Info{Title: "My Events", Version: "1.0.0"})
//	userCreated, _ := events.NewChannel[UserCreated]("user/created", codec,
//	    events.ChannelMeta{}, events.Subscribe{}).Register(b)
//
//	// Wire to Paho on connect (JSON, the default):
//	client.Subscribe(userCreated.Topic, 1,
//	    mqtt.SubscribeHandler(ctx, userCreated, func(ctx context.Context, e UserCreated) error {
//	        return svc.HandleUserCreated(ctx, e)
//	    }, mqtt.SubscribeOptions{
//	        OnError: func(e mqtt.SubscribeError) { log.Println("event error:", e) },
//	    }),
//	)
//
//	// Subscribe with a custom format (e.g. YAML):
//	client.Subscribe(userCreated.Topic, 1,
//	    mqtt.SubscribeHandler(ctx, userCreated, handler, opts, format.YAML(codec)))
//
//	// Publish an event (JSON, the default):
//	notification := NotificationCommand{Recipient: "alice@example.com", ...}
//	mqtt.Publish(ctx, client, notifChannel, 1, false, notification, nil, opts)
//
//	// Publish with a custom format (e.g. YAML):
//	mqtt.Publish(ctx, client, notifChannel, 1, false, notification, nil, opts, format.YAML(codec))
package mqtt

import (
	"context"
	"errors"
	"fmt"
	"time"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/stats"
)

// ErrorKind classifies the origin of a [SubscribeError].
type ErrorKind int

const (
	// KindDecode indicates the message payload could not be decoded or
	// failed codec validation.
	KindDecode ErrorKind = iota

	// KindHandler indicates the application handler returned an error after
	// successful decoding.
	KindHandler

	// KindSecurity indicates the SecurityFunc rejected the message.
	KindSecurity
)

func (k ErrorKind) String() string {
	switch k {
	case KindDecode:
		return "decode"
	case KindHandler:
		return "handler"
	case KindSecurity:
		return "security"
	default:
		return "unknown"
	}
}

// SubscribeError is returned to the onErr callback with a typed Kind so callers
// can distinguish decode/validation failures from application handler errors
// without string matching.
type SubscribeError struct {
	Kind  ErrorKind
	Topic string
	Err   error
}

func (e SubscribeError) Error() string {
	return fmt.Sprintf("mqtt %s %s: %v", e.Kind, e.Topic, e.Err)
}

func (e SubscribeError) Unwrap() error { return e.Err }

// contextKey is the unexported type for values stored in context by this package.
type contextKey struct{}

// SubscribeOptions configures [SubscribeHandler].
type SubscribeOptions struct {
	// OnError, when non-nil, is called with a typed [SubscribeError] on decode
	// or application handler failure. If nil, errors are silently discarded.
	OnError func(SubscribeError)

	// Observer, when non-nil, receives per-message lifecycle events: success/
	// failure counts and processing duration per topic. Per-field payload validation
	// errors are reported via [stats.Observer.RecordValidationError] with location
	// "payload". Topic variable errors from [TopicVarsFromMessage] propagated through
	// the handler are reported with location "topic_var" (per-variable codec failures)
	// or "topic" (topic-level codec or structural mismatch).
	// Defaults to [stats.NoopObserver] when nil.
	Observer stats.Observer

	// SecurityFunc, when non-nil, is called for channels whose subscribe operation
	// has non-empty security requirements (per-channel Security or global security
	// declared via [Builder.AddGlobalSecurity]), before fn is invoked.
	// Return a non-nil error to reject the message; [Options.OnError] is called with
	// [KindSecurity] and the returned error.
	//
	// reqs contains the effective security requirements for the channel.
	//
	// Three patterns for obtaining credentials:
	//
	// Pattern 1 — Closure: capture a shared secret or token at CONNECT time
	// and reference it in the closure. No message access needed.
	//
	// Pattern 2 — Direct msg access: msg is passed directly to SecurityFunc.
	// For MQTT 5.0 libraries that expose User Properties, extract via
	// msg.Properties().User.Get("key"). For MQTT 3.1.1 (Paho), msg does not
	// carry per-message credentials; use Pattern 1 or 3 instead.
	//
	// Pattern 3 — Handler access: use [MessageFromContext] inside fn (after
	// SecurityFunc returns nil) to inspect QoS, retained flag, or full topic.
	// SecurityFunc itself always receives the message directly via the msg parameter.
	//
	// Example (Pattern 2 with MQTT 5.0 User Properties):
	//
	//	opts.SecurityFunc = func(ctx context.Context, msg pahomqtt.Message, reqs []route.SecurityRequirement) error {
	//	    token := msg.Properties().User.Get("Authorization")
	//	    return verifyJWT(token, reqs)
	//	}
	SecurityFunc func(ctx context.Context, msg pahomqtt.Message, reqs []route.SecurityRequirement) error
}

// MessageFromContext retrieves the [pahomqtt.Message] stored in ctx by [SubscribeHandler].
// Returns false if the context was not created by this package.
func MessageFromContext(ctx context.Context) (pahomqtt.Message, bool) {
	msg, ok := ctx.Value(contextKey{}).(pahomqtt.Message)
	return msg, ok
}

// SubscribeHandler returns a [pahomqtt.MessageHandler] that decodes the message
// payload using handle's codec, validates it, and calls fn.
//
// ctx is threaded through to fn for cancellation and deadline propagation.
// Pass [SubscribeOptions] to handle errors and observe lifecycle events. Pass a
// zero-value [SubscribeOptions]{} for no-op behaviour.
//
// The optional formats parameter specifies the payload format to use for
// decoding. When provided, the first format is used; when omitted, the default
// JSON codec of the channel handle is used. MQTT 3.1.1 carries no content-type
// metadata, so the format must be agreed out-of-band per subscription.
//
// The Topic field of [SubscribeError] reflects the concrete topic of the incoming
// message (from msg.Topic()), which is useful when the channel was registered with
// a template topic.
func SubscribeHandler[T any](
	ctx context.Context,
	handle *events.ChannelHandle[T],
	fn func(context.Context, T) error,
	opts SubscribeOptions,
	formats ...format.Format[T],
) pahomqtt.MessageHandler {
	obs := opts.Observer
	if obs == nil {
		obs = stats.NoopObserver{}
	}
	// Priority: call-time formats > handle formats > JSON fallback (handle.Decode).
	effectiveFmts := formats
	if len(effectiveFmts) == 0 {
		effectiveFmts = handle.Formats
	}
	return func(_ pahomqtt.Client, msg pahomqtt.Message) {
		start := time.Now()
		ctx := context.WithValue(ctx, contextKey{}, msg)
		var value T
		var err error
		if len(effectiveFmts) > 0 {
			value, err = effectiveFmts[0].Unmarshal(msg.Payload())
		} else {
			value, err = handle.Decode(msg.Payload())
		}
		if err != nil {
			reportPayloadErrors(err, obs)
			obs.RecordSubscribe(msg.Topic(), false, time.Since(start))
			if opts.OnError != nil {
				opts.OnError(SubscribeError{Kind: KindDecode, Topic: msg.Topic(), Err: err})
			}
			return
		}
		// Enforce security: per-operation requirements take precedence; nil falls
		// back to global security declared via Builder.AddGlobalSecurity.
		var secReqs []route.SecurityRequirement
		if handle.Descriptor.Subscribe != nil {
			secReqs = handle.Descriptor.Subscribe.Security
		}
		if secReqs == nil {
			secReqs = handle.GlobalSecurity
		}
		if len(secReqs) > 0 {
			if credErr := validateSecurityCredentials(msg, secReqs, handle.SecuritySchemes); credErr != nil {
				if secObs, ok := obs.(stats.SecurityObserver); ok {
					secObs.RecordSecurityRejection(msg.Topic(), firstScheme(secReqs))
				}
				obs.RecordSubscribe(msg.Topic(), false, time.Since(start))
				if opts.OnError != nil {
					opts.OnError(SubscribeError{Kind: KindSecurity, Topic: msg.Topic(), Err: credErr})
				}
				return
			}
			if opts.SecurityFunc != nil {
				if err := opts.SecurityFunc(ctx, msg, secReqs); err != nil {
					if secObs, ok := obs.(stats.SecurityObserver); ok {
						secObs.RecordSecurityRejection(msg.Topic(), firstScheme(secReqs))
					}
					obs.RecordSubscribe(msg.Topic(), false, time.Since(start))
					if opts.OnError != nil {
						opts.OnError(SubscribeError{Kind: KindSecurity, Topic: msg.Topic(), Err: err})
					}
					return
				}
			}
		}

		if err := fn(ctx, value); err != nil {
			reportTopicParamErrors(err, obs)
			reportTopicMismatchErrors(err, obs)
			reportInvalidTopicErrors(err, obs)
			reportMissingTopicVarErrors(err, obs)
			obs.RecordSubscribe(msg.Topic(), false, time.Since(start))
			if opts.OnError != nil {
				opts.OnError(SubscribeError{Kind: KindHandler, Topic: msg.Topic(), Err: err})
			}
			return
		}
		obs.RecordSubscribe(msg.Topic(), true, time.Since(start))
	}
}

// reportPayloadErrors extracts per-field validation errors from a payload decode
// error and reports them to obs with location "payload".
func reportPayloadErrors(err error, obs stats.Observer) {
	stats.ReportErrors(obs, "payload", err)
}

// reportTopicParamErrors extracts the failing topic variable from a [events.TopicParamError]
// and reports it to obs with location "topic_var".
func reportTopicParamErrors(err error, obs stats.Observer) {
	var pe events.TopicParamError
	if !errors.As(err, &pe) {
		return
	}
	obs.RecordValidationError("topic_var", stats.ConstraintName(pe.Err), pe.Name)
}

// reportMissingTopicVarErrors extracts the missing variable name from a [events.MissingTopicVarError]
// and reports it to obs with location "topic_var" and constraint "required".
func reportMissingTopicVarErrors(err error, obs stats.Observer) {
	var me events.MissingTopicVarError
	if !errors.As(err, &me) {
		return
	}
	obs.RecordValidationError("topic_var", "required", me.Name)
}

// reportInvalidTopicErrors extracts the constraint from an [events.InvalidTopicError]
// and reports it to obs with location "topic".
func reportInvalidTopicErrors(err error, obs stats.Observer) {
	var ie events.InvalidTopicError
	if !errors.As(err, &ie) {
		return
	}
	obs.RecordValidationError("topic", stats.ConstraintName(ie.Err), "")
}

// reportTopicMismatchErrors reports a [TopicMismatchError] to obs with location "topic"
// and constraint name "topic-mismatch".
func reportTopicMismatchErrors(err error, obs stats.Observer) {
	var mm TopicMismatchError
	if !errors.As(err, &mm) {
		return
	}
	obs.RecordValidationError("topic", "topic-mismatch", "")
}

type PublishOptions struct {
	// Observer, when non-nil, receives per-publish lifecycle events:
	// [stats.Observer.RecordPublish] is called with success=true on broker
	// acknowledgement and success=false on encode failure, broker error, or
	// context cancellation. Per-field payload encode errors are reported via
	// [stats.Observer.RecordValidationError] with location "payload".
	// Topic variable errors from [events.ChannelHandle.BuildTopic] are reported
	// with location "topic_var" (per-variable codec failures) or "topic"
	// (topic-level codec failures).
	// Defaults to [stats.NoopObserver] when nil.
	Observer stats.Observer
}

// Publish encodes msg using handle's codec and publishes it to the broker.
//
// vars controls the topic used for publishing:
//   - nil: publish to handle.Topic directly (use for static topics).
//   - non-nil: call handle.BuildTopic(vars) to build a concrete topic from the
//     template, validating each variable against its registered codec. An error
//     is returned if any variable is missing or fails validation.
//
// Pass [PublishOptions] to observe publish lifecycle events via a [stats.Observer].
// Pass a zero-value [PublishOptions]{} for no-op behaviour.
//
// The optional formats parameter specifies the payload format to use for
// encoding. When provided, the first format is used; when omitted, the default
// JSON codec of the channel handle is used.
//
// Example — static topic (JSON, the default):
//
//	err := adaptermqtt.Publish(ctx, client, notifChannel, 1, false, notification, nil,
//	    adaptermqtt.PublishOptions{})
//
// Example — static topic with YAML encoding:
//
//	err := adaptermqtt.Publish(ctx, client, notifChannel, 1, false, notification, nil,
//	    adaptermqtt.PublishOptions{}, format.YAML(notifCodec))
//
// Example — template topic (sensors/{sensorID}/alerts):
//
//	err := adaptermqtt.Publish(ctx, client, alertChannel, 1, false, alert,
//	    map[string]string{"sensorID": id}, adaptermqtt.PublishOptions{Observer: obs})
//
// Publish waits for broker acknowledgement, respecting ctx cancellation. If the
// context is cancelled before the broker responds, ctx.Err() is returned.
func Publish[T any](ctx context.Context, client pahomqtt.Client, handle *events.ChannelHandle[T], qos byte, retained bool, msg T, vars map[string]string, opts PublishOptions, formats ...format.Format[T]) error {
	obs := opts.Observer
	if obs == nil {
		obs = stats.NoopObserver{}
	}
	start := time.Now()

	topic := handle.Topic
	if vars != nil {
		var err error
		topic, err = handle.BuildTopic(vars)
		if err != nil {
			reportTopicParamErrors(err, obs)
			reportMissingTopicVarErrors(err, obs)
			reportInvalidTopicErrors(err, obs)
			obs.RecordPublish(handle.Topic, false, time.Since(start))
			return err
		}
	}
	// Priority: call-time formats > handle formats > JSON fallback (handle.Encode).
	effectiveFmts := formats
	if len(effectiveFmts) == 0 {
		effectiveFmts = handle.Formats
	}
	var payload []byte
	var err error
	if len(effectiveFmts) > 0 {
		payload, err = effectiveFmts[0].Marshal(msg)
	} else {
		payload, err = handle.Encode(msg)
	}
	if err != nil {
		reportPayloadErrors(err, obs)
		obs.RecordPublish(topic, false, time.Since(start))
		return fmt.Errorf("mqtt encode %s: %w", topic, err)
	}
	token := client.Publish(topic, qos, retained, payload)
	select {
	case <-ctx.Done():
		obs.RecordPublish(topic, false, time.Since(start))
		return ctx.Err()
	case <-token.Done():
		if token.Error() != nil {
			obs.RecordPublish(topic, false, time.Since(start))
			return token.Error()
		}
		obs.RecordPublish(topic, true, time.Since(start))
		return nil
	}
}

// validateSecurityCredentials checks registered SecurityScheme codecs against
// credentials extracted from the MQTT message.
//
// Because the paho.mqtt.golang library does not expose MQTT 5.0 User Properties
// through the pahomqtt.Message interface, credential extraction is always a
// no-op and Codec validation is deliberately skipped. Use SecurityFunc with
// MessageFromContext for runtime credential inspection instead.
func validateSecurityCredentials(_ pahomqtt.Message, _ []route.SecurityRequirement, _ map[string]events.SecurityScheme) error {
	// Codec validation skipped: pahomqtt.Message (MQTT 3.1.1) does not expose
	// per-message credentials. Use SecurityFunc for runtime enforcement instead.
	return nil
}

// firstScheme returns the first scheme name from the security requirements.
func firstScheme(reqs []route.SecurityRequirement) string {
	for _, req := range reqs {
		for name := range req {
			return name
		}
	}
	return ""
}
