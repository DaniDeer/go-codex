package mqtt

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/stats"
)

// eventsPkgPath is api/events' import path — used to distinguish a
// genuine events.Publisher[T]/events.Subscriber[T] value (for ANY T)
// from an unrelated/wrong-package value passed by caller mistake to
// [Client.Publish]/[Client.Subscribe].
const eventsPkgPath = "github.com/DaniDeer/go-codex/api/events"

// defaultQoS is the fallback QoS/Retain-false zero value the reflection
// shim's Publish/Subscribe start from before resolving a declared
// [Publisher.WithOptions]/[Subscriber.WithOptions] Capabilities value
// (docs/roadmap/capability-requirement-composition.md's Phase 4c) —
// matches MQTT's own protocol-level default (at-most-once). This
// package has not yet migrated to the Apply-interface shape mqtt5 has
// (Phase 5's job), so Capabilities are resolved the OLD way, via
// [events.ResolveCapabilityValue] — mirroring [publish]'s own identical
// resolution block.
const defaultQoS byte = 0

// resolveHandlerOptsCapabilities extracts the []Capability slice from a
// type-erased HandlerOpts value (a concrete mqtt.SubscribeOptions or
// mqtt.PublishOptions[T] boxed as any) via reflection against the fixed
// "Capabilities" field name — works regardless of T, since that field's
// type ([]Capability) doesn't depend on T. Returns nil (not an error)
// when handlerOpts is nil/wrong-shape/has no such field — a declared
// channel with no Capabilities is the common, valid case. Mirrors
// [adapters/mqtt5]'s identical helper.
func resolveHandlerOptsCapabilities(handlerOptsField reflect.Value) []Capability {
	if !handlerOptsField.IsValid() || handlerOptsField.IsNil() {
		return nil
	}
	v := reflect.ValueOf(handlerOptsField.Interface())
	if v.Kind() != reflect.Struct {
		return nil
	}
	capsField := v.FieldByName("Capabilities")
	if !capsField.IsValid() {
		return nil
	}
	caps, _ := capsField.Interface().([]Capability)
	return caps
}

// transport implements [events.Transport] AND [events.ClientAwareTransport]
// (via [transport.BindClient]), wrapping an internal [*caller] — built by
// [NewTransport]. See docs/design/d-0002-pubsub-workflow-simplification.md's
// Decision 5 for the full design and the reflection technique this type
// relies on (Go forbids generic methods, so Publish/Subscribe/
// ServeSubscribers recover the concrete payload type at runtime via
// reflection against the ALREADY-CONCRETE closures on the type-erased
// events.ChannelHandle — never via reflecting a generic FUNCTION, which
// Go does not support).
type transport struct {
	caller *caller
}

// TransportOptions configures [NewTransport] — the SOLE configuration
// surface for an mqtt (v3) [events.Transport] (docs/roadmap/
// capability-requirement-composition.md's Phase 4d: a single Options
// struct, no positional params, even for this one REQUIRED field — a
// deliberate, uniform, declarative shape across every adapter's
// `New*Transport` factory).
type TransportOptions struct {
	// Client is the MQTT v3.1.1 broker connection. Required.
	Client pahomqtt.Client
}

// NewTransport returns an [events.Transport] configured per opts — the
// adapter's ONLY job in the attach workflow (docs/roadmap/
// capability-requirement-composition.md's Phase 4d): construct a
// fully-configured, attachable value. Attaching it is EXCLUSIVELY
// [events.Client.Attach]'s job — there is no adapter-namespaced Attach
// function anymore (REMOVED, breaking, per that phase's explicit
// "zero backdoor between the api layer and the adapters" directive).
// The returned value also implements [events.ClientAwareTransport] —
// [events.Client.Attach] supplies the [*events.Client] reference this
// shim needs (spec-registration + [ServeSubscribers]'s registry walk)
// via [transport.BindClient], immediately after storing it; NewTransport
// itself never needs a [*events.Client] parameter.
//
// NOTE — v1 scope, NARROWED by Phase 4c (docs/roadmap/
// capability-requirement-composition.md): the reflection shim's
// Publish/Subscribe now honor a declared [Publisher.WithOptions]/
// [Subscriber.WithOptions]([PublishOptions][T]/[SubscribeOptions]{
// Capabilities: ...}) value, resolved via [events.ResolveCapabilityValue]
// — closing the ONE gap that forced a caller needing Capabilities to
// bypass [events.Client.Publish]/[Client.Subscribe] entirely. Per-call
// [format.Format] overrides and declare-time SubscribeMW/PublishMW — of
// EITHER shape, credential-paired OR general-purpose wrapping — are
// STILL NOT exercised by this shim (Phase 4e's scope, not yet shipped;
// mirrors [adapters/nethttp/clienttransport.go]'s now-closed "no
// security/credential handling" limitation, which this package has not
// closed yet): a channel declaring Security plus a correctly-paired
// credential SubscribeMW/PublishMW STILL gets ZERO runtime enforcement
// through the attached transport — no credential is fetched or
// injected, silently, with no error. A caller needing ANY declared
// SubscribeMW/PublishMW (credential or general-purpose) enforced, or a
// per-call format override, should use [subscribe]/[publish] directly
// until Phase 4e ships.
// [stats.Observer] (RecordPublish/RecordSubscribe, TraceObserver) IS
// fully wired, resolved from ctx same as [subscribe]/[publish]; a
// subscribe handler's returned error also consults a declared
// [events.ErrorChannel] — see
// docs/design/d-0002-pubsub-workflow-simplification.md's Decision 8 for the
// fix history.
//
// [Client.Subscribe] BLOCKS until ctx is cancelled — a deliberate uniform
// contract across every [events.Transport] implementation, mirroring
// adapters/zeromq's and adapters/mqtt5's own shims.
//
//	transport := mqtt.NewTransport(mqtt.TransportOptions{Client: mqttClient})
//	if err := evClient.Attach(transport); err != nil { ... }
func NewTransport(opts TransportOptions) events.Transport {
	return &transport{caller: newCaller(opts.Client, nil)}
}

// BindClient implements [events.ClientAwareTransport] — called by
// [events.Client.Attach] immediately after storing t, supplying the
// [*events.Client] reference [recoverHandle] (spec-registration) and
// [ServeSubscribers] (registry walk) need.
func (t *transport) BindClient(c *events.Client) error {
	t.caller.events = c
	return nil
}

// recoverHandle calls anyAny's Handle(client) method via reflection —
// mirrors [adapters/zeromq]'s/[adapters/mqtt5]'s identical helper.
func recoverHandle(kind string, anyAny any, client *events.Client) (reflect.Value, reflect.Value, error) {
	v := reflect.ValueOf(anyAny)
	if !v.IsValid() || v.Type().PkgPath() != eventsPkgPath || !strings.HasPrefix(v.Type().Name(), kind+"[") {
		return reflect.Value{}, reflect.Value{}, events.TransportTypeMismatchError{
			Want: fmt.Sprintf("events.%s[T]", kind), Got: fmt.Sprintf("%T", anyAny),
		}
	}
	handleMethod := v.MethodByName("Handle")
	results := handleMethod.Call([]reflect.Value{reflect.ValueOf(client)})
	if errI, _ := results[1].Interface().(error); errI != nil {
		return reflect.Value{}, reflect.Value{}, errI
	}
	handleVal := results[0]
	return handleVal, handleVal.Elem(), nil
}

// Publish implements [events.Transport]. See [Attach]'s doc comment for
// v1 scope notes. Resolves [stats.Observer] from ctx (this shim has no
// per-call Options struct to carry an explicit override) and calls
// RecordPublish on EVERY exit path, mirroring [publish]'s own convention.
func (t *transport) Publish(ctx context.Context, pubAny, msgAny any) (err error) {
	obs := stats.ObserverFromContext(ctx)
	start := time.Now()

	handleVal, elem, err := recoverHandle("Publisher", pubAny, t.caller.events)
	if err != nil {
		return err
	}
	topic := elem.FieldByName("Topic").String()

	if to, ok := obs.(stats.TraceObserver); ok {
		ctx = to.StartSpan(ctx, "mqtt.publish", topic)
		defer func() { to.EndSpan(ctx, err) }()
	}

	encodeWithFormatsMethod := handleVal.MethodByName("EncodeWithFormats") // func(T, ...format.Format[T]) ([]byte, error)
	msgVal := reflect.ValueOf(msgAny)
	if !msgVal.IsValid() || msgVal.Type() != encodeWithFormatsMethod.Type().In(0) {
		obs.RecordPublish(topic, false, time.Since(start))
		err = events.TransportTypeMismatchError{
			Topic: topic, Want: encodeWithFormatsMethod.Type().In(0).String(), Got: fmt.Sprintf("%T", msgAny),
		}
		return err
	}

	varsResults := handleVal.MethodByName("EncodeVars").Call([]reflect.Value{msgVal})
	if errI, _ := varsResults[1].Interface().(error); errI != nil {
		obs.RecordPublish(topic, false, time.Since(start))
		err = errI
		return err
	}
	vars, _ := varsResults[0].Interface().(map[string]string)

	finalTopic := topic
	if len(vars) > 0 {
		topicResults := handleVal.MethodByName("BuildTopic").Call([]reflect.Value{reflect.ValueOf(vars)})
		if errI, _ := topicResults[1].Interface().(error); errI != nil {
			obs.RecordPublish(topic, false, time.Since(start))
			err = errI
			return err
		}
		finalTopic, _ = topicResults[0].Interface().(string)
	}

	// The channel's OWN declaration (WithFormats/WithPublishFormats) is
	// the single source of truth for which format applies —
	// EncodeWithFormats resolves it; Client.Attach never duplicates that
	// resolution logic itself (no call-time override to pass, matching
	// this shim's documented v1 scope).
	encodeResults := encodeWithFormatsMethod.Call([]reflect.Value{msgVal})
	if errI, _ := encodeResults[1].Interface().(error); errI != nil {
		obs.RecordPublish(topic, false, time.Since(start))
		err = fmt.Errorf("mqtt: encode: %w", errI)
		return err
	}
	payload, _ := encodeResults[0].Interface().([]byte)

	// docs/roadmap/capability-requirement-composition.md's Phase 4c: a
	// declared Capabilities value now resolves via
	// [events.ResolveCapabilityValue] (this package's still-legacy
	// mechanism — see [defaultQoS]'s doc comment), closing the gap
	// where this shim could only ever publish at QoS 0/non-retained.
	caps := resolveHandlerOptsCapabilities(elem.FieldByName("HandlerOpts"))
	qos, retained := defaultQoS, false
	if capQoS, qosSet := events.ResolveCapabilityValue[Capability, QoS](caps); qosSet {
		qos = byte(capQoS)
		events.RecordCapabilityApplied(obs, finalTopic, capQoS)
	}
	if capRetained, retainedSet := events.ResolveCapabilityValue[Capability, Retained](caps); retainedSet {
		retained = bool(capRetained)
		events.RecordCapabilityApplied(obs, finalTopic, capRetained)
	}

	token := t.caller.client.Publish(finalTopic, qos, retained, payload)
	token.Wait()
	if tokErr := token.Error(); tokErr != nil {
		obs.RecordPublish(finalTopic, false, time.Since(start))
		err = tokErr
		return err
	}
	obs.RecordPublish(finalTopic, true, time.Since(start))
	return nil
}

// Subscribe implements [events.Transport]. Registers a reflection-built
// [pahomqtt.MessageHandler] directly via client.Subscribe (v3 has no
// router — the Subscribe call itself both registers AND performs the
// broker SUBSCRIBE, unlike mqtt5's separate router-registration step),
// then BLOCKS until ctx is cancelled, unsubscribing on exit — see
// [Attach]'s doc comment for why this blocks even though v3's dispatch
// mechanism itself is callback-driven, not loop-driven.
//
// [stats.Observer] is resolved from ctx ONCE and RecordSubscribe is
// called PER INCOMING MESSAGE (mirrors [subscribeHandler]'s own
// per-message convention, not a single call for the whole blocking
// Subscribe). When fn (the caller's handler) returns a non-nil error, a
// declared [events.ErrorChannel] is consulted via handle.ErrorResponseFor
// — on an [events.ErrorRespond] match, the typed payload is published to
// the declared error-output topic.
func (t *transport) Subscribe(ctx context.Context, subAny, fnAny any) error {
	obs := stats.ObserverFromContext(ctx)

	handleVal, elem, err := recoverHandle("Subscriber", subAny, t.caller.events)
	if err != nil {
		return err
	}
	topic := elem.FieldByName("Topic").String()

	// The channel's OWN declaration (WithFormats/WithSubscribeFormats) is
	// the single source of truth for which format applies —
	// DecodeMergedWithFormats resolves it; Client.Attach never duplicates
	// that resolution logic itself (no call-time override to pass,
	// matching this shim's documented v1 scope).
	decodeMergedMethod := handleVal.MethodByName("DecodeMergedWithFormats") // (payload []byte, vars map[string]string, formats ...format.Format[T]) (T, error)
	errorResponseForMethod := handleVal.MethodByName("ErrorResponseFor")
	fnVal := reflect.ValueOf(fnAny)
	wantFnType := reflect.FuncOf(
		[]reflect.Type{reflect.TypeOf((*context.Context)(nil)).Elem(), decodeMergedMethod.Type().Out(0)},
		[]reflect.Type{reflect.TypeOf((*error)(nil)).Elem()},
		false,
	)
	if !fnVal.IsValid() || fnVal.Type() != wantFnType {
		return events.TransportTypeMismatchError{Topic: topic, Want: wantFnType.String(), Got: fmt.Sprintf("%T", fnAny)}
	}

	filter := deriveWildcardFilter(topic)
	ctxVal := reflect.ValueOf(ctx)

	handler := func(client pahomqtt.Client, msg pahomqtt.Message) {
		start := time.Now()
		vars, matchErr := matchTopicTemplate(topic, msg.Topic())
		if matchErr != nil {
			return // broader wildcard subscription received a non-matching topic — expected, not an error
		}
		decodeResults := decodeMergedMethod.Call([]reflect.Value{reflect.ValueOf(msg.Payload()), reflect.ValueOf(vars)})
		if errI, _ := decodeResults[1].Interface().(error); errI != nil {
			obs.RecordSubscribe(msg.Topic(), false, time.Since(start))
			return
		}
		fnResults := fnVal.Call([]reflect.Value{ctxVal, decodeResults[0]})
		handlerErr, _ := fnResults[0].Interface().(error)
		if handlerErr == nil {
			obs.RecordSubscribe(msg.Topic(), true, time.Since(start))
			return
		}
		obs.RecordSubscribe(msg.Topic(), false, time.Since(start))
		errResults := errorResponseForMethod.Call([]reflect.Value{reflect.ValueOf(&handlerErr).Elem()})
		resp, _ := errResults[0].Interface().(events.ErrorChannelResponse)
		matched, _ := errResults[1].Interface().(bool)
		matchErrI, _ := errResults[2].Interface().(error)
		if matched && matchErrI == nil && resp.Action == events.ErrorRespond {
			token := client.Publish(resp.Topic, defaultQoS, false, resp.Body)
			token.Wait()
		}
	}

	// docs/roadmap/capability-requirement-composition.md's Phase 4c: a
	// declared Capabilities value now resolves via
	// [events.ResolveCapabilityValue], closing the gap where this shim
	// could only ever subscribe at QoS 0.
	subQoS := defaultQoS
	if caps := resolveHandlerOptsCapabilities(elem.FieldByName("HandlerOpts")); caps != nil {
		if capQoS, qosSet := events.ResolveCapabilityValue[Capability, QoS](caps); qosSet {
			subQoS = byte(capQoS)
			events.RecordCapabilityApplied(obs, topic, capQoS)
		}
	}

	subToken := t.caller.client.Subscribe(filter, subQoS, handler)
	subToken.Wait()
	if err := subToken.Error(); err != nil {
		return err
	}

	<-ctx.Done()
	unsubToken := t.caller.client.Unsubscribe(filter)
	unsubToken.Wait()
	return nil
}

// ServeSubscribers implements [events.Transport] — delegates directly to
// the wrapped [*caller]'s own ServeSubscribers (non-generic, no
// reflection needed), which walks every [events.Subscriber] registered
// against t.caller.events via [events.Subscriber.Register].
func (t *transport) ServeSubscribers(ctx context.Context) error {
	return t.caller.ServeSubscribers(ctx)
}

var _ events.Transport = (*transport)(nil)
