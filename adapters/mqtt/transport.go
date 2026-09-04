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
// [stats.Observer] (RecordPublish/RecordSubscribe, TraceObserver) IS
// fully wired, resolved from ctx same as [subscribe]/[Publish]; a
// subscribe handler's returned error also consults a declared
// [events.ErrorChannel] — see
// docs/roadmap/pubsub-workflow-simplification.md's Decision 8 for the
// fix history.
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

	token := t.caller.client.Publish(finalTopic, defaultQoS, false, payload)
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
