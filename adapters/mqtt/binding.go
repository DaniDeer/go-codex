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
	// Capabilities supplies sealed, compile-time-checked protocol-native
	// declarations (currently [QoS]) for this subscription — the SOLE
	// mechanism (docs/design/d-0006-protocol-native-capabilities.md's
	// Phase 5: the former raw `qos byte` constructor parameter was
	// REMOVED entirely — it bypassed [Capability]/[Apply] completely).
	// Mirrors adapters/mqtt5's identical, already-shipped shape exactly.
	Capabilities []Capability
}

// SubscribeAdapter returns a [ports.SourceAdapter] backed by the MQTT v3/v3.1.1
// subscription machinery. Use with [ports.SourcePort.Bind]:
//
//	domain.SensorReadings.Bind(ctx, mqtt.SubscribeAdapter(
//	    client, sensorHandle,
//	    format.JSON(ReadingCodec),
//	    mqtt.SubscribeAdapterOptions{
//	        TopicFilter:  "sensors/+/data",
//	        Capabilities: []mqtt.Capability{mqtt.QoSAtLeastOnce},
//	    },
//	))
//
// The full MQTT validation pipeline runs: format priority, topic var validation,
// security enforcement, observer calls. Errors are routed to Stream.Errors.
func SubscribeAdapter[T any](
	client pahomqtt.Client,
	handle *events.ChannelHandle[T],
	fmt format.Format[T],
	opts SubscribeAdapterOptions,
) ports.SourceAdapter[T] {
	return &mqttSubscribeAdapter[T]{
		client: client,
		handle: handle,
		fmt:    fmt,
		opts:   opts,
	}
}

type mqttSubscribeAdapter[T any] struct {
	client pahomqtt.Client
	handle *events.ChannelHandle[T]
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
	// Tier 1 coverage check — a channel's own declared [events.RequireQoS]/
	// [events.RequireRetained] (a.handle.Requirements) must be verified
	// here too — this ports.SourceAdapter binding is a SEPARATE dispatch
	// path from subscribeHandle/ServeSubscribers and had the SAME
	// confirmed-missing gap.
	if len(a.handle.Requirements) > 0 {
		if covErr := events.VerifyCapabilityCoverage(a.handle.Topic, a.handle.Requirements, a.opts.Capabilities); covErr != nil {
			select {
			case errs <- covErr:
			case <-ctx.Done():
			}
			return
		}
	}
	// docs/design/d-0006-protocol-native-capabilities.md's Phase 5:
	// Capabilities is the SOLE mechanism — events.ApplyCapabilities is
	// the API-LAYER-OWNED dispatch loop; this adapter contributes only
	// Capability.Apply. Mirrors adapters/mqtt5's identical, already-
	// shipped shape exactly.
	var wire WireAttributes
	events.ApplyCapabilities(a.opts.Capabilities, &wire, obs, filter)
	token := a.client.Subscribe(filter, wire.QoS, handler)
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
	// Capabilities supplies sealed, compile-time-checked protocol-native
	// declarations (currently [QoS]/[Retained]) for every published item
	// — the SOLE mechanism (docs/design/d-0006-protocol-native-capabilities.md's
	// Phase 5: the former raw QoS byte/Retained bool fields were REMOVED
	// entirely). Does NOT apply to a matched events.ErrorChannel reply
	// published for an upstream pipeline error — those always use QoS
	// 0/non-retained, matching every other error-channel dispatch site
	// (see tryPublishErrorChannel). Mirrors adapters/mqtt5's identical,
	// already-shipped shape exactly.
	Capabilities []Capability
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
	// this package — a.opts.Capabilities no longer applies to THIS path
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
			itemOpts := pubOpts
			itemOpts.Capabilities = a.opts.Capabilities
			var err error
			if a.opts.Vars == nil {
				err = publishHandle(ctx, a.client, a.handle, v, itemOpts, a.fmt)
			} else {
				err = publish(ctx, a.client, a.handle, v, a.opts.Vars, itemOpts, a.fmt)
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
