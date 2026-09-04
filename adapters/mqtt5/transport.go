package mqtt5

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	pahomqtt5 "github.com/eclipse/paho.golang/paho"

	"github.com/DaniDeer/go-codex/api/events"
)

// eventsPkgPath is api/events' import path — used to distinguish a
// genuine events.Publisher[T]/events.Subscriber[T] value (for ANY T)
// from an unrelated/wrong-package value passed by caller mistake to
// [Client.Publish]/[Client.Subscribe].
const eventsPkgPath = "github.com/DaniDeer/go-codex/api/events"

// defaultQoS is used by the reflection shim's Subscribe/Publish since
// [events.Transport]'s call shape has no room for a per-call QoS
// parameter — matches MQTT's own protocol-level default (at-most-once).
// A caller needing a different QoS uses [subscribe]/[Publish]
// directly, unaffected by this addition.
const defaultQoS byte = 0

// transport implements [events.Transport], wrapping an internal [*caller]
// — built by [Attach]. See docs/roadmap/pubsub-workflow-simplification.md's
// Decision 5 for the full design and the reflection technique this type
// relies on (Go forbids generic methods, so Publish/Subscribe/
// ServeSubscribers recover the concrete payload type at runtime via
// reflection against the ALREADY-CONCRETE closures on the type-erased
// events.ChannelHandle — never via reflecting a generic FUNCTION, which
// Go does not support).
type transport struct {
	caller *caller
}

// Attach binds client+router+eventsClient (via an internal [*caller]) as
// client's [events.Transport] — the "attach the adapter to the client"
// step behind [events.Client.Publish]/[events.Client.Subscribe]/
// [events.Client.ServeSubscribers]. Returns
// [events.TransportAlreadyAttachedError] if client already has a
// transport attached.
//
// NOTE — v1 scope: the reflection shim's Publish/Subscribe cover the
// CORE common case (JSON default format, automatic topic-var derivation,
// observer resolved from ctx, QoS 0). Per-call [format.Format] overrides,
// non-zero QoS, and declare-time general-purpose SubscribeMW/PublishMW
// wrapping are NOT exercised by this shim — a caller needing those should
// use [subscribe]/[Publish] directly, which remain
// fully featured and completely unaffected by this addition.
//
// Unlike [SubscribeWithHandle] (non-blocking — registers with the router
// and returns immediately, dispatch happens via the router's OWN
// callback mechanism), [Client.Subscribe] BLOCKS until ctx is cancelled —
// a deliberate uniform contract across every [events.Transport]
// implementation (zeromq's own Subscribe genuinely blocks on its receive
// loop; mqtt5's shim adds the SAME guarantee on top of its naturally
// non-blocking primitive, for a consistent Client.Subscribe caller
// experience regardless of which adapter is attached).
func Attach(client *events.Client, mqttClient MQTTClient, router MQTTRouter) error {
	return client.Attach(&transport{caller: newCaller(mqttClient, router, client)})
}

// recoverHandle calls anyAny's Handle(client) method via reflection —
// mirrors [adapters/zeromq]'s identical helper.
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
// v1 scope notes.
func (t *transport) Publish(ctx context.Context, pubAny, msgAny any) error {
	handleVal, elem, err := recoverHandle("Publisher", pubAny, t.caller.events)
	if err != nil {
		return err
	}
	topic := elem.FieldByName("Topic").String()

	encodeField := elem.FieldByName("Encode") // func(T) ([]byte, error)
	msgVal := reflect.ValueOf(msgAny)
	if !msgVal.IsValid() || msgVal.Type() != encodeField.Type().In(0) {
		return events.TransportTypeMismatchError{
			Topic: topic, Want: encodeField.Type().In(0).String(), Got: fmt.Sprintf("%T", msgAny),
		}
	}

	varsResults := handleVal.MethodByName("EncodeVars").Call([]reflect.Value{msgVal})
	if errI, _ := varsResults[1].Interface().(error); errI != nil {
		return errI
	}
	vars, _ := varsResults[0].Interface().(map[string]string)

	finalTopic := topic
	if len(vars) > 0 {
		topicResults := handleVal.MethodByName("BuildTopic").Call([]reflect.Value{reflect.ValueOf(vars)})
		if errI, _ := topicResults[1].Interface().(error); errI != nil {
			return errI
		}
		finalTopic, _ = topicResults[0].Interface().(string)
	}

	encodeResults := encodeField.Call([]reflect.Value{msgVal})
	if errI, _ := encodeResults[1].Interface().(error); errI != nil {
		return fmt.Errorf("mqtt5: encode: %w", errI)
	}
	payload, _ := encodeResults[0].Interface().([]byte)

	_, pubErr := t.caller.client.Publish(ctx, &pahomqtt5.Publish{
		Topic:   finalTopic,
		QoS:     defaultQoS,
		Payload: payload,
	})
	if pubErr != nil {
		return BrokerError{Op: "publish", Err: pubErr}
	}
	return nil
}

// Subscribe implements [events.Transport]. Registers a reflection-built
// [pahomqtt5.MessageHandler] with the router (mirrors
// [SubscribeWithHandle]'s registration step, since that function itself
// cannot be called with a runtime-only T) and issues the broker SUBSCRIBE,
// then BLOCKS until ctx is cancelled, unregistering the handler on exit —
// see [Attach]'s doc comment for why this blocks (a deliberate, uniform
// Client.Subscribe contract) even though the underlying mqtt5 dispatch
// mechanism itself is callback-driven, not loop-driven.
func (t *transport) Subscribe(ctx context.Context, subAny, fnAny any) error {
	handleVal, elem, err := recoverHandle("Subscriber", subAny, t.caller.events)
	if err != nil {
		return err
	}
	topic := elem.FieldByName("Topic").String()

	decodeMergedMethod := handleVal.MethodByName("DecodeMerged") // (payload []byte, vars map[string]string) (T, error)
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

	handler := func(msg *pahomqtt5.Publish) {
		vars, matchErr := matchTopicTemplate(topic, msg.Topic)
		if matchErr != nil {
			return // broader wildcard subscription received a non-matching topic — expected, not an error
		}
		decodeResults := decodeMergedMethod.Call([]reflect.Value{reflect.ValueOf(msg.Payload), reflect.ValueOf(vars)})
		if errI, _ := decodeResults[1].Interface().(error); errI != nil {
			return
		}
		_ = fnVal.Call([]reflect.Value{ctxVal, decodeResults[0]})
	}

	t.caller.router.RegisterHandler(filter, handler)
	if _, err := t.caller.client.Subscribe(ctx, &pahomqtt5.Subscribe{
		Subscriptions: []pahomqtt5.SubscribeOptions{{Topic: filter, QoS: defaultQoS}},
	}); err != nil {
		t.caller.router.UnregisterHandler(filter)
		return BrokerError{Op: "subscribe", Err: err}
	}

	<-ctx.Done()
	t.caller.router.UnregisterHandler(filter)
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
