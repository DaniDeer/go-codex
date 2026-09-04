package mqtt

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"

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
// A caller needing a different QoS uses [subscribe] directly
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

// Attach binds client (via an internal [*caller]) as client's
// [events.Transport] — the "attach the adapter to the client" step
// behind [events.Client.Publish]/[events.Client.Subscribe]/
// [events.Client.ServeSubscribers]. Returns
// [events.TransportAlreadyAttachedError] if the events client already
// has a transport attached.
//
// NOTE — v1 scope: the reflection shim's Publish/Subscribe cover the
// CORE common case (JSON default format, automatic topic-var derivation,
// observer resolved from ctx, QoS 0). Per-call [format.Format] overrides,
// non-zero QoS, and declare-time general-purpose SubscribeMW/PublishMW
// wrapping are NOT exercised by this shim — a caller needing those should
// use [subscribe]/[Publish] directly, which remain
// fully featured and completely unaffected by this addition.
//
// [Client.Subscribe] BLOCKS until ctx is cancelled — a deliberate uniform
// contract across every [events.Transport] implementation, mirroring
// adapters/zeromq's and adapters/mqtt5's own shims.
func Attach(eventsClient *events.Client, mqttClient pahomqtt.Client) error {
	return eventsClient.Attach(&transport{caller: newCaller(mqttClient, eventsClient)})
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
		return fmt.Errorf("mqtt: encode: %w", errI)
	}
	payload, _ := encodeResults[0].Interface().([]byte)

	token := t.caller.client.Publish(finalTopic, defaultQoS, false, payload)
	token.Wait()
	if tokErr := token.Error(); tokErr != nil {
		return tokErr
	}
	return nil
}

// Subscribe implements [events.Transport]. Registers a reflection-built
// [pahomqtt.MessageHandler] directly via client.Subscribe (v3 has no
// router — the Subscribe call itself both registers AND performs the
// broker SUBSCRIBE, unlike mqtt5's separate router-registration step),
// then BLOCKS until ctx is cancelled, unsubscribing on exit — see
// [Attach]'s doc comment for why this blocks even though v3's dispatch
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

	handler := func(_ pahomqtt.Client, msg pahomqtt.Message) {
		vars, matchErr := matchTopicTemplate(topic, msg.Topic())
		if matchErr != nil {
			return // broader wildcard subscription received a non-matching topic — expected, not an error
		}
		decodeResults := decodeMergedMethod.Call([]reflect.Value{reflect.ValueOf(msg.Payload()), reflect.ValueOf(vars)})
		if errI, _ := decodeResults[1].Interface().(error); errI != nil {
			return
		}
		_ = fnVal.Call([]reflect.Value{ctxVal, decodeResults[0]})
	}

	subToken := t.caller.client.Subscribe(filter, defaultQoS, handler)
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
