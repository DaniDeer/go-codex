// Package mqtt adapts [api/events] channel handles to [Paho MQTT] callbacks.
//
// [SubscribeHandler] turns a [events.ChannelHandle] into an [mqtt.MessageHandler]
// that decodes and validates incoming payloads before calling the application
// handler. [Publish] encodes a value and publishes it to the broker.
//
// Typical usage:
//
//	b := events.NewBuilder(events.Info{Title: "My Events", Version: "1.0.0"})
//	userCreated := events.AddChannel[UserCreated](b, "user/created", codec,
//	    events.ChannelConfig{Subscribe: &events.OperationConfig{...}})
//
//	// Wire to Paho on connect:
//	client.Subscribe(userCreated.Topic, 1,
//	    mqtt.SubscribeHandler(ctx, userCreated, func(ctx context.Context, e UserCreated) error {
//	        return svc.HandleUserCreated(ctx, e)
//	    }, func(e mqtt.SubscribeError) { log.Println("event error:", e) }),
//	)
//
//	// Publish an event:
//	notification := NotificationCommand{Recipient: "alice@example.com", ...}
//	mqtt.Publish(ctx, client, notifChannel, 1, false, notification)
package mqtt

import (
	"context"
	"fmt"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"

	"github.com/DaniDeer/go-codex/api/events"
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
)

func (k ErrorKind) String() string {
	switch k {
	case KindDecode:
		return "decode"
	case KindHandler:
		return "handler"
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
// If onErr is non-nil it is called with a typed [SubscribeError] containing the
// error kind, topic, and underlying error. If onErr is nil errors are silently
// discarded.
//
// The Topic field of [SubscribeError] reflects the concrete topic of the incoming
// message (from msg.Topic()), which is useful when the channel was registered with
// a template topic.
func SubscribeHandler[T any](
	ctx context.Context,
	handle *events.ChannelHandle[T],
	fn func(context.Context, T) error,
	onErr func(SubscribeError),
) pahomqtt.MessageHandler {
	return func(_ pahomqtt.Client, msg pahomqtt.Message) {
		ctx := context.WithValue(ctx, contextKey{}, msg)
		value, err := handle.Decode(msg.Payload())
		if err != nil {
			if onErr != nil {
				onErr(SubscribeError{Kind: KindDecode, Topic: msg.Topic(), Err: err})
			}
			return
		}
		if err := fn(ctx, value); err != nil {
			if onErr != nil {
				onErr(SubscribeError{Kind: KindHandler, Topic: msg.Topic(), Err: err})
			}
		}
	}
}

// Publish encodes msg using handle's codec and publishes it to the broker.
//
// vars controls the topic used for publishing:
//   - nil: publish to handle.Topic directly (use for static topics).
//   - non-nil: call handle.BuildTopic(vars) to build a concrete topic from the
//     template, validating each variable against its registered codec. An error
//     is returned if any variable is missing or fails validation.
//
// Example — static topic:
//
//	err := adaptermqtt.Publish(ctx, client, notifChannel, 1, false, notification, nil)
//
// Example — template topic (sensors/{sensorID}/alerts):
//
//	err := adaptermqtt.Publish(ctx, client, alertChannel, 1, false, alert,
//	    map[string]string{"sensorID": id})
//
// Publish waits for broker acknowledgement, respecting ctx cancellation. If the
// context is cancelled before the broker responds, ctx.Err() is returned.
func Publish[T any](ctx context.Context, client pahomqtt.Client, handle *events.ChannelHandle[T], qos byte, retained bool, msg T, vars map[string]string) error {
	topic := handle.Topic
	if vars != nil {
		var err error
		topic, err = handle.BuildTopic(vars)
		if err != nil {
			return err
		}
	}
	payload, err := handle.Encode(msg)
	if err != nil {
		return fmt.Errorf("mqtt encode %s: %w", topic, err)
	}
	token := client.Publish(topic, qos, retained, payload)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-token.Done():
		return token.Error()
	}
}
