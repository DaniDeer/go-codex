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
//	client.Subscribe("sensors/+/data", 1, handler) // use MQTT wildcard, not API template
//	oeeStream := gstream.Apply(ctx, s, oeeCalcFn, gstream.ApplyOptions{Observer: obs})
//
// The returned handler is built by [SubscribeHandler] and applies the full adapter
// validation pipeline: security enforcement, payload decode with format priority
// (call-time > SubscribeFormats > Formats > handle.Decode), topic var error reporting,
// and all observer calls. Decode, security, and handler errors are sent to
// [gstream.Stream.Errors] as [SubscribeError] — callers can use [gstream.MapErr]
// to recover or reclassify them.
//
// The stream terminates when ctx is cancelled. The caller owns the MQTT client
// subscription lifecycle — SubscribeStream never calls client.Unsubscribe.
func SubscribeStream[T any](
	ctx context.Context,
	handle *events.ChannelHandle[T],
	fmt format.Format[T],
	srcOpts gstream.SourceOptions,
	subOpts SubscribeOptions,
) (gstream.Stream[T], pahomqtt.MessageHandler) {
	typedCh := make(chan T, srcOpts.Buffer)
	errCh := make(chan error, srcOpts.Buffer)

	// Override OnError: route adapter errors (decode, security, topic vars)
	// to Stream.Errors so callers can handle them with stream operators.
	innerOpts := subOpts
	innerOpts.OnError = func(e SubscribeError) {
		select {
		case errCh <- e:
		case <-ctx.Done():
		default: // drop on full buffer
		}
	}

	// SubscribeHandler applies format priority chain, security enforcement,
	// topic var error reporting, and all observer calls — identical to direct usage.
	handler := SubscribeHandler(ctx, handle,
		func(_ context.Context, v T) error {
			select {
			case typedCh <- v:
			case <-ctx.Done():
			default: // drop on full buffer
			}
			return nil
		}, innerOpts, fmt)

	go func() {
		<-ctx.Done()
		close(typedCh)
		close(errCh)
	}()

	return gstream.Stream[T]{Values: typedCh, Errors: errCh}, handler
}

// ── DrainPublish ──────────────────────────────────────────────────────────────

// MQTTDrainPublishOptions configures [DrainPublish].
type MQTTDrainPublishOptions struct {
	// QoS is the MQTT quality of service level (0, 1, or 2). Default 0.
	QoS byte
	// Retained, when true, publishes each item as a retained message.
	Retained bool
	// Vars, when non-nil, substitutes {varName} placeholders in the topic template.
	// The same map is used for every item in the stream (static topic vars only).
	// For per-item topic var substitution (e.g. {sensorID} from each payload),
	// use [gstream.Drain] with [Publish] directly and build the vars map per item.
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
		gstream.DrainOptions{Observer: opts.Observer},
	)
}
