package mqtt

import (
	"context"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/stats"
	gstream "github.com/DaniDeer/go-codex/stream"
)

// ── SubscribeStream ───────────────────────────────────────────────────────────

// SubscribeStream creates a bridge from an MQTT subscription to a typed stream.
// It returns both the stream and the [pahomqtt.MessageHandler] to register with
// the MQTT client. The caller must register the handler before messages can flow:
//
//	s, handler := mqtt.SubscribeStream(ctx, sensorHandle, format.JSON(sensorCodec),
//	    gstream.SourceOptions{Name: "mqtt/sensors/+", Observer: obs},
//	    mqtt.SubscribeOptions{Observer: obs})
//	client.Subscribe(sensorHandle.Topic, 1, handler)
//	oeeStream := gstream.Apply(ctx, s, oeeCalcFn, gstream.ApplyOptions{Observer: obs})
//
// Decode or validation failures are sent to [gstream.Stream.Errors] as
// [gstream.StreamDecodeError]. The stream terminates when ctx is cancelled.
// The caller owns the MQTT client subscription lifecycle — SubscribeStream
// never calls client.Unsubscribe.
//
// Observer calls from subOpts (RecordSubscribe, RecordValidationError) fire for
// every incoming message, independent from the per-item stream observer in srcOpts.
func SubscribeStream[T any](
	ctx context.Context,
	handle *events.ChannelHandle[T],
	fmt format.Format[T],
	srcOpts gstream.SourceOptions,
	subOpts SubscribeOptions,
) (gstream.Stream[T], pahomqtt.MessageHandler) {
	rawCh := make(chan []byte, srcOpts.Buffer)
	if srcOpts.Name == "" {
		srcOpts.Name = handle.Topic
	}

	// The MQTT handler writes raw payloads to rawCh; security and observer calls
	// from subOpts fire here. The application fn is a no-op — stream operators
	// take over once FromCodec emits decoded items.
	obs := subOpts.Observer
	if obs == nil {
		obs = stats.NoopObserver{}
	}
	handler := func(_ pahomqtt.Client, msg pahomqtt.Message) {
		payload := msg.Payload()
		topic := msg.Topic()
		_ = topic // used for observer reporting
		select {
		case rawCh <- payload:
			obs.RecordSubscribe(topic, true, 0)
		case <-ctx.Done():
		default: // drop when buffer is full
		}
	}

	src := gstream.FromCodec(ctx, rawCh, fmt, srcOpts)
	return src, pahomqtt.MessageHandler(handler)
}

// ── DrainPublish ──────────────────────────────────────────────────────────────

// MQTTDrainPublishOptions configures [DrainPublish].
type MQTTDrainPublishOptions struct {
	// QoS is the MQTT quality of service level (0, 1, or 2). Default 0.
	QoS byte
	// Retained, when true, publishes each item as a retained message.
	Retained bool
	// Vars, when non-nil, substitutes {varName} placeholders in the topic template.
	Vars map[string]string
	// OnError, when non-nil, is called for encode failures ([PublishEncodeError])
	// or upstream stream errors.
	OnError func(error)
	// Observer receives per-publish lifecycle events via [stats.Observer.RecordPublish].
	Observer stats.Observer
}

// DrainPublish publishes each value item from src to the MQTT broker using handle.
// Encode failures are delivered to opts.OnError as [PublishEncodeError].
// Upstream stream errors are forwarded to opts.OnError unchanged.
// Blocks until src terminates or ctx is cancelled.
func DrainPublish[T any](
	ctx context.Context,
	client pahomqtt.Client,
	handle *events.ChannelHandle[T],
	src gstream.Stream[T],
	fmt format.Format[T],
	opts MQTTDrainPublishOptions,
) {
	onErr := opts.OnError
	pubOpts := PublishOptions{Observer: opts.Observer}

	gstream.Drain(ctx, src,
		func(ctx context.Context, v T) error {
			if err := Publish(ctx, client, handle, opts.QoS, opts.Retained, v,
				opts.Vars, pubOpts, fmt); err != nil {
				if onErr != nil {
					onErr(err)
				}
			}
			return nil
		},
		func(e error) {
			if onErr != nil {
				onErr(e)
			}
		},
		gstream.DrainOptions{},
	)
}
