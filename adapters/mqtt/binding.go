package mqtt

import (
	"context"
	"regexp"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/ports"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/stats"
	gstream "github.com/DaniDeer/go-codex/stream"
)

// templateVarRe matches {varName} placeholders in a topic template.
var templateVarRe = regexp.MustCompile(`\{[^}]+\}`)

// deriveWildcardFilter replaces each {varName} placeholder segment in topic
// with the MQTT single-level wildcard "+", producing a broker subscription
// filter usable directly when no explicit TopicFilter was configured (e.g.
// "sensors/{sensorID}/data" -> "sensors/+/data"). A topic with no placeholders
// is returned unchanged.
func deriveWildcardFilter(topic string) string {
	return templateVarRe.ReplaceAllString(topic, "+")
}

// ── SubscribeAdapter ──────────────────────────────────────────────────────────

// SubscribeAdapterOptions configures [SubscribeAdapter].
type SubscribeAdapterOptions struct {
	// TopicFilter is the MQTT broker subscription filter (e.g. "sensors/+/data").
	// When empty, derived automatically from [events.ChannelHandle.Topic] by
	// replacing each {varName} placeholder with the MQTT wildcard "+" (e.g.
	// "sensors/{sensorID}/data" -> "sensors/+/data") — the common case needs no
	// manual restatement. Set explicitly only for a filter that differs from
	// this derivation (e.g. a multi-level "#" wildcard).
	TopicFilter string
	// SecurityFunc enforces security requirements on each incoming message.
	SecurityFunc func(context.Context, pahomqtt.Message, []route.SecurityRequirement) error
	// Observer receives per-message lifecycle events. Resolved from ctx when nil.
	Observer stats.Observer
}

// SubscribeAdapter returns a [ports.SourceAdapter] backed by the MQTT v3/v3.1.1
// subscription machinery. Use with [ports.SourcePort.Bind]:
//
//	domain.SensorReadings.Bind(ctx, mqtt.SubscribeAdapter(
//	    client, sensorHandle, 0,
//	    format.JSON(ReadingCodec),
//	    mqtt.SubscribeAdapterOptions{TopicFilter: "sensors/+/data"},
//	))
//
// The full MQTT validation pipeline runs: format priority, topic var validation,
// security enforcement, observer calls. Errors are routed to Stream.Errors.
func SubscribeAdapter[T any](
	client pahomqtt.Client,
	handle *events.ChannelHandle[T],
	qos byte,
	fmt format.Format[T],
	opts SubscribeAdapterOptions,
) ports.SourceAdapter[T] {
	return &mqttSubscribeAdapter[T]{
		client: client,
		handle: handle,
		qos:    qos,
		fmt:    fmt,
		opts:   opts,
	}
}

type mqttSubscribeAdapter[T any] struct {
	client pahomqtt.Client
	handle *events.ChannelHandle[T]
	qos    byte
	fmt    format.Format[T]
	opts   SubscribeAdapterOptions
}

func (a *mqttSubscribeAdapter[T]) AdapterName() string { return "mqtt.SubscribeAdapter" }

func (a *mqttSubscribeAdapter[T]) Activate(ctx context.Context, dst chan<- T, errs chan<- error) {
	obs := a.opts.Observer
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}
	innerOpts := SubscribeOptions{
		Observer:     obs,
		SecurityFunc: a.opts.SecurityFunc,
		TopicFilter:  a.opts.TopicFilter,
		OnError: func(e SubscribeError) {
			select {
			case errs <- e:
			case <-ctx.Done():
			default:
			}
		},
	}
	handler := SubscribeHandler(ctx, a.handle,
		func(_ context.Context, v T) error {
			select {
			case dst <- v:
			case <-ctx.Done():
			default:
			}
			return nil
		}, innerOpts, a.fmt)

	filter := a.opts.TopicFilter
	if filter == "" {
		filter = deriveWildcardFilter(a.handle.Topic)
	}
	token := a.client.Subscribe(filter, a.qos, handler)
	token.Wait()
	if err := token.Error(); err != nil {
		select {
		case errs <- err:
		case <-ctx.Done():
		}
		return
	}
	<-ctx.Done()
}

// ── PublishAdapter ────────────────────────────────────────────────────────────

// MQTTDrainPublishOptions configures [PublishAdapter] publish behaviour.
type MQTTDrainPublishOptions struct {
	// QoS is the MQTT quality of service level (0, 1, or 2). Default 0.
	QoS byte
	// Retained, when true, publishes each item as a retained message.
	Retained bool
	// Vars, when non-nil, substitutes {varName} placeholders in the topic template.
	// The same map is used for every item (static topic vars only).
	// For per-item substitution, call [Publish] directly inside [gstream.Drain].
	Vars map[string]string
	// OnError, when non-nil, is called for encode failures ([PublishEncodeError])
	// or upstream stream errors.
	OnError func(error)
	// Observer receives per-publish lifecycle events.
	Observer stats.Observer
}

// PublishAdapter returns a [ports.SinkAdapter] that publishes each item via MQTT.
// Use with [ports.SinkPort.Bind]:
//
//	domain.OEEResults.Bind(ctx, mqtt.PublishAdapter(client, alertHandle, format.JSON(OEECodec),
//	    mqtt.MQTTDrainPublishOptions{}))
func PublishAdapter[T any](
	client pahomqtt.Client,
	handle *events.ChannelHandle[T],
	fmt format.Format[T],
	opts MQTTDrainPublishOptions,
) ports.SinkAdapter[T] {
	return &mqttPublishAdapter[T]{client: client, handle: handle, fmt: fmt, opts: opts}
}

type mqttPublishAdapter[T any] struct {
	client pahomqtt.Client
	handle *events.ChannelHandle[T]
	fmt    format.Format[T]
	opts   MQTTDrainPublishOptions
}

func (a *mqttPublishAdapter[T]) AdapterName() string { return "mqtt.PublishAdapter" }

func (a *mqttPublishAdapter[T]) Activate(ctx context.Context, src gstream.Stream[T]) {
	onErr := a.opts.OnError
	pubOpts := PublishOptions{Observer: a.opts.Observer}
	gstream.Drain(ctx, src,
		func(ctx context.Context, v T) error {
			if err := Publish(ctx, a.client, a.handle, a.opts.QoS, a.opts.Retained, v,
				a.opts.Vars, pubOpts, a.fmt); err != nil {
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
		gstream.DrainOptions{Observer: a.opts.Observer},
	)
}
