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

// Deprecated: Use [SubscribeAdapter] with [ports.SourcePort] instead.
//
// SubscribeStream creates a bridge from an MQTT subscription to a typed stream.
// It subscribes to the broker internally and returns only the stream — no
// handler registration is needed by the caller:
//
//	s := mqtt.SubscribeStream(ctx, client, sensorHandle, 1,
//	    format.JSON(sensorCodec),
//	    gstream.SourceOptions{Name: "mqtt/sensors/+"},
//	    mqtt.SubscribeOptions{TopicFilter: "sensors/+/data"}) // MQTT wildcard
//	oeeStream := gstream.Apply(ctx, s, oeeCalcFn, gstream.ApplyOptions{})
//
// The subscription filter passed to [pahomqtt.Client.Subscribe] is
// [SubscribeOptions.TopicFilter] when set, otherwise [events.ChannelHandle.Topic].
// Use TopicFilter when the handle stores an API template topic
// (e.g. "sensors/{sensorID}/data") but the broker requires MQTT wildcard syntax
// (e.g. "sensors/+/data").
//
// The full adapter validation pipeline runs: security enforcement, payload decode
// with format priority (call-time > SubscribeFormats > Formats > handle.Decode),
// topic var error reporting, and all observer calls. Decode, security, and handler
// errors are sent to [gstream.Stream.Errors] as [SubscribeError] — callers can
// use [gstream.MapErr] to recover or reclassify them.
//
// The stream terminates when ctx is cancelled. SubscribeStream calls
// [pahomqtt.Client.Subscribe] but never calls [pahomqtt.Client.Unsubscribe] —
// the caller owns the MQTT client lifecycle.
func SubscribeStream[T any](
	ctx context.Context,
	client pahomqtt.Client,
	handle *events.ChannelHandle[T],
	qos byte,
	fmt format.Format[T],
	srcOpts gstream.SourceOptions,
	subOpts SubscribeOptions,
) gstream.Stream[T] {
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

	// Subscribe internally — use TopicFilter when set (MQTT wildcard syntax
	// may differ from the API template topic stored in handle.Topic).
	filter := subOpts.TopicFilter
	if filter == "" {
		filter = handle.Topic
	}
	client.Subscribe(filter, qos, handler)

	go func() {
		<-ctx.Done()
		close(typedCh)
		close(errCh)
	}()

	return gstream.Stream[T]{Values: typedCh, Errors: errCh}
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

// Deprecated: Use [PublishAdapter] with [ports.SinkPort] instead.
//
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
