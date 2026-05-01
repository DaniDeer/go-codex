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
//	    }, func(err error) { log.Println("event error:", err) }),
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

// SubscribeHandler returns a [pahomqtt.MessageHandler] that decodes the message
// payload using handle's codec, validates it, and calls fn.
//
// ctx is threaded through to fn for cancellation and deadline propagation.
// If onErr is non-nil it is called with any error from decoding, validation, or
// fn. If onErr is nil errors are silently discarded.
func SubscribeHandler[T any](
	ctx context.Context,
	handle *events.ChannelHandle[T],
	fn func(context.Context, T) error,
	onErr func(error),
) pahomqtt.MessageHandler {
	return func(_ pahomqtt.Client, msg pahomqtt.Message) {
		value, err := handle.Decode(msg.Payload())
		if err != nil {
			if onErr != nil {
				onErr(fmt.Errorf("mqtt decode %s: %w", handle.Topic, err))
			}
			return
		}
		if err := fn(ctx, value); err != nil {
			if onErr != nil {
				onErr(fmt.Errorf("mqtt handler %s: %w", handle.Topic, err))
			}
		}
	}
}

// Publish encodes msg using handle's codec and publishes it to handle.Topic.
// It waits for broker acknowledgement, respecting ctx cancellation. If the
// context is cancelled before the broker responds, ctx.Err() is returned.
func Publish[T any](ctx context.Context, client pahomqtt.Client, handle *events.ChannelHandle[T], qos byte, retained bool, msg T) error {
	payload, err := handle.Encode(msg)
	if err != nil {
		return fmt.Errorf("mqtt encode %s: %w", handle.Topic, err)
	}
	token := client.Publish(handle.Topic, qos, retained, payload)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-token.Done():
		return token.Error()
	}
}
