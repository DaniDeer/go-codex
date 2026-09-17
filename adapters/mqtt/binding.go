package mqtt

import (
	"context"
	"regexp"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/ports"
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
		Observer:    obs,
		TopicFilter: a.opts.TopicFilter,
		OnError: func(e SubscribeError) {
			select {
			case errs <- e:
			case <-ctx.Done():
			default:
			}
		},
	}
	handler := subscribeHandler(ctx, a.client, a.handle,
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
	// Does NOT apply to a matched events.ErrorChannel reply published for
	// an upstream pipeline error — those always use QoS 0/non-retained,
	// matching every other error-channel dispatch site (see
	// tryPublishErrorChannel).
	QoS byte
	// Retained, when true, publishes each item as a retained message. Does
	// NOT apply to error-channel replies — see QoS.
	Retained bool
	// Vars substitutes {varName} placeholders in the topic template.
	//
	// When nil, topic vars are derived PER-ITEM from each item's own
	// merge-field-declared struct fields (the same convenience
	// [publishHandle] provides) — every item may resolve to a different
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
	pubOpts := PublishOptions[T]{Observer: a.opts.Observer}
	// handleUpstreamError resolves declared events.ErrorChannel patterns on
	// a.handle before falling back to the adapter's existing OnError
	// callback, via the SAME tryPublishErrorChannel helper the subscribe
	// side and publish()'s own internal error paths already use — this
	// ALSO reports stats.ErrorPatternObserver match/miss + SpanTagger
	// observability (session review round-5 fix H2: this closure
	// previously hand-rolled its own dispatch via the bare
	// handle.ErrorResponseFor, silently skipping that observability).
	// handled=true: a matched ErrorRespond was published — done.
	// matched=true (handled=false): ErrorHandle/ErrorLog — fall through to
	// OnError. matched=false: genuine non-match — fall through to OnError
	// with the ORIGINAL error unchanged. Error-channel replies published
	// this way always use QoS 0 / non-retained (tryPublishErrorChannel's
	// fixed choice, matching every other error-channel dispatch site in
	// this package — a.opts.QoS/Retained no longer apply to THIS path
	// specifically, closing a previously-undocumented inconsistency).
	handleUpstreamError := func(e error) {
		handled, _ := tryPublishErrorChannel(ctx, a.client, a.handle, obs, e)
		if handled {
			return
		}
		if onErr != nil {
			onErr(e)
		}
	}
	gstream.Drain(ctx, src,
		func(ctx context.Context, v T) error {
			// Declared events.PublishAttributes (via Publisher.WithAttributes)
			// is the FALLBACK default when neither QoS nor Retained is
			// explicitly set on a.opts — an explicit opts.QoS/Retained
			// override still wins when set to a non-default value.
			qos, retained := a.opts.QoS, a.opts.Retained
			if qos == 0 && !retained {
				attrs := a.handle.ResolvePublishAttributes(v)
				qos, retained = byte(attrs.QoS), attrs.Retained
			}
			var err error
			if a.opts.Vars == nil {
				err = publishHandle(ctx, a.client, a.handle, qos, retained, v, pubOpts, a.fmt)
			} else {
				err = publish(ctx, a.client, a.handle, qos, retained, v,
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
