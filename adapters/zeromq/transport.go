package zeromq

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/DaniDeer/go-codex/api/events"
)

// eventsPkgPath is api/events' import path — used to distinguish a
// genuine events.Publisher[T]/events.Subscriber[T] value (for ANY T)
// from an unrelated/wrong-package value passed by caller mistake to
// [Client.Publish]/[Client.Subscribe].
const eventsPkgPath = "github.com/DaniDeer/go-codex/api/events"

// transport implements [events.Transport], wrapping an internal [*Caller]
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

// Attach binds sock+client (via an internal [*Caller]) as client's
// [events.Transport] — the "attach the adapter to the client" step
// behind [events.Client.Publish]/[events.Client.Subscribe]/
// [events.Client.ServeSubscribers]. Returns
// [events.TransportAlreadyAttachedError] if client already has a
// transport attached.
//
//	client := events.NewClient(events.WithInfo(events.Info{Title: "Sensor Network", Version: "1.0.0"}))
//	if err := zeromq.Attach(client, sock); err != nil { ... }
//	sub := ReadingsChannel.WithSubscribe(events.Subscribe{})
//	pub := ReadingsChannel.WithPublish(events.Publish{})
//	err := client.Subscribe(ctx, sub, func(ctx context.Context, r SensorReading) error { ... })
//	err = client.Publish(ctx, pub, reading)
//
// NOTE — v1 scope: the reflection shim's Publish/Subscribe cover the
// CORE common case (JSON default format, automatic topic-var
// derivation via [events.ChannelHandle.EncodeVars]/[events.ChannelHandle.BuildTopic],
// observer resolved from ctx). Per-call [format.Format] overrides and
// declare-time general-purpose SubscribeMW/PublishMW wrapping are NOT
// exercised by this shim — a caller needing those should use
// [SubscribeWithHandle]/[Publish] directly, which remain fully
// featured and completely unaffected by this addition.
func Attach(client *events.Client, sock FramedSocket) error {
	return client.Attach(&transport{caller: newCaller(sock, client)})
}

// recoverHandle calls anyAny's Handle(client) method via reflection —
// anyAny's dynamic type must be events.Subscriber[T]/events.Publisher[T]
// for some concrete T (both expose an identically-shaped
// Handle(*events.Client) (*events.ChannelHandle[T], error) method).
// Returns the resulting *events.ChannelHandle[T] as a reflect.Value
// (dynamic type still concrete, since T was already fixed by the
// caller's own compile-time instantiation of anyAny) plus its Elem()
// reflect.Value, or a TransportTypeMismatchError if anyAny isn't a
// recognized events.Subscriber[T]/events.Publisher[T] value.
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

	// Derive topic vars from msg's merge-capable NewTopicParam fields
	// (empty map when the channel declares none) via the reflection-
	// friendly ChannelHandle.EncodeVars method.
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
		return PublishEncodeError{Topic: finalTopic, Err: errI}
	}
	payload, _ := encodeResults[0].Interface().([]byte)

	if sendErr := t.caller.sock.SendFrames([][]byte{[]byte(finalTopic), payload}); sendErr != nil {
		return SocketError{Op: "send", Err: sendErr}
	}
	return nil
}

// Subscribe implements [events.Transport]. Unlike a scratch-client
// approach (which would register sub's spec into a THROWAWAY client,
// never the one the caller attached), this registers into the REAL
// attached client (via sub.Handle(t.caller.events) — spec-only, does NOT
// touch any subscriber registry, so it never conflicts with OTHER
// subscriptions on the same client) so [events.Client.AsyncAPISpec]
// correctly includes this operation, then runs a DEDICATED receive loop
// scoped to just this one channel — mirroring [SubscribeWithHandle]'s
// core steps (SetSubscription, decode+topic-var-merge via
// [events.ChannelHandle.DecodeMerged], dispatch to fn) via reflection,
// since SubscribeWithHandle itself cannot be called with a runtime-only
// T. matchTopicTemplate/deriveTopicPrefix are plain, non-generic,
// same-package functions, called directly (no reflection needed). See
// [Attach]'s doc comment for v1 scope notes (no per-call format
// override, no Implementations/security-impl enforcement, no
// general-purpose SubscribeMW wrapping — a caller needing those uses
// [subscribe]/[SubscribeWithHandle] directly).
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

	filter := deriveTopicPrefix(topic)
	if err := t.caller.sock.SetSubscription(filter); err != nil {
		return SocketError{Op: "set_subscription", Err: err}
	}
	if err := t.caller.sock.SetRecvTimeout(recvPollInterval); err != nil {
		return SocketError{Op: "set_recv_timeout", Err: err}
	}

	ctxVal := reflect.ValueOf(ctx)
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		frames, recvErr := t.caller.sock.RecvFrames()
		if errors.Is(recvErr, ErrTimeout) {
			continue
		}
		if recvErr != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				return SocketError{Op: "recv", Err: recvErr}
			}
		}
		if len(frames) < 2 {
			continue // malformed: expect [topic, payload]
		}
		gotTopic := string(frames[0])
		payload := frames[1]

		vars, matchErr := matchTopicTemplate(topic, gotTopic)
		if matchErr != nil {
			continue // topic prefix subscription received a non-matching topic — expected, not an error
		}

		decodeResults := decodeMergedMethod.Call([]reflect.Value{reflect.ValueOf(payload), reflect.ValueOf(vars)})
		if errI, _ := decodeResults[1].Interface().(error); errI != nil {
			continue
		}
		fnResults := fnVal.Call([]reflect.Value{ctxVal, decodeResults[0]})
		if errI, _ := fnResults[0].Interface().(error); errI != nil {
			continue
		}
	}
}

// ServeSubscribers implements [events.Transport] — delegates directly to
// the wrapped [*Caller]'s own ServeSubscribers (non-generic, no
// reflection needed), which walks every [events.Subscriber] registered
// against t.caller.events via [events.Subscriber.Register].
func (t *transport) ServeSubscribers(ctx context.Context) error {
	return t.caller.ServeSubscribers(ctx)
}

var _ events.Transport = (*transport)(nil)
