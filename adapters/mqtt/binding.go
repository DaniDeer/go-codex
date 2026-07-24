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
	// Vars substitutes {varName} placeholders in the topic template.
	//
	// When nil, topic vars are derived PER-ITEM from each item's own
	// merge-field-declared struct fields (the same convenience
	// [PublishHandle] provides) — every item may resolve to a different
	// concrete topic. When set to a non-nil map (including an explicitly
	// empty one), that map is used as-is for every item (static topic vars
	// only) — the escape hatch, unchanged from prior behavior.
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
	obs := a.opts.Observer
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}
	pubOpts := PublishOptions{Observer: a.opts.Observer}
	// handleUpstreamError resolves declared events.ErrorChannel patterns on
	// a.handle before falling back to the adapter's existing OnError
	// callback. A matched ErrorRespond pattern publishes the typed error
	// payload to its declared error-output topic; ErrorHandle runs OnError
	// (unchanged existing behaviour); ErrorLog and unmatched errors also
	// fall through to OnError — see [events.ErrorChannel] and mirrors
	// [adapters/mqtt5.mqtt5PublishAdapter.Activate].
	handleUpstreamError := func(e error) {
		resp, matched, matchErr := a.handle.ErrorResponseFor(e)
		if matched && matchErr == nil && resp.Action == events.ErrorRespond {
			token := a.client.Publish(resp.Topic, a.opts.QoS, a.opts.Retained, resp.Body)
			select {
			case <-ctx.Done():
			case <-token.Done():
				if pubErr := token.Error(); pubErr != nil {
					stats.ReportErrors(obs, "error_channel", pubErr)
					if onErr != nil {
						onErr(pubErr)
					}
				}
			}
			return
		}
		if matched && matchErr != nil {
			stats.ReportErrors(obs, "error_channel", matchErr)
		}
		if onErr != nil {
			onErr(e)
		}
	}
	gstream.Drain(ctx, src,
		func(ctx context.Context, v T) error {
			var err error
			if a.opts.Vars == nil {
				err = PublishHandle(ctx, a.client, a.handle, a.opts.QoS, a.opts.Retained, v, pubOpts, a.fmt)
			} else {
				err = Publish(ctx, a.client, a.handle, a.opts.QoS, a.opts.Retained, v,
					a.opts.Vars, pubOpts, a.fmt)
			}
			if err != nil {
				if onErr != nil {
					onErr(err)
				}
			}
			return nil
		},
		handleUpstreamError,
		gstream.DrainOptions{Observer: a.opts.Observer},
	)
}
