package mqtt5

import (
	"context"
	"reflect"
	"testing"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/stats"
	"github.com/DaniDeer/go-codex/validate"
	pahomqtt5 "github.com/eclipse/paho.golang/paho"
)

// TestSubscribeDispatchCore_DirectCall proves [subscribeDispatchCore]
// itself — independent of which entry point (transport.go's
// (*transport).Subscribe or caller.go's (*caller).serveOneEntry) calls
// it — correctly decodes a payload, merges a topic-template var into the
// decoded value, and invokes the supplied handler. This is the Phase 4
// events/reqreply family, Workstream A conformance seed the roadmap's
// design calls for: future regressions in the shared core are now
// caught here directly, not only through one entry point's own tests.
func TestSubscribeDispatchCore_DirectCall(t *testing.T) {
	uuidCodec := codex.String().Refine(validate.UUID)
	ch := events.NewChannel[sensorReading](
		"sensors/{sensorID}/readings",
		sensorCodec,
		events.NewTopicParam("sensorID", uuidCodec,
			func(r sensorReading) string { return r.SensorID },
			func(r *sensorReading, v string) { r.SensorID = v }),
	).WithSubscribe(events.Subscribe{})

	handle, err := ch.Handle(nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	handleVal := reflect.ValueOf(handle)
	info := buildErasedSubscriberHandleFields(handleVal, handleVal.Elem())

	var received sensorReading
	var callCount int
	fnVal := reflect.ValueOf(func(_ context.Context, r sensorReading) error {
		callCount++
		received = r
		return nil
	})
	formatsZero := reflect.Zero(info.decodeMergedMethod.Type().In(2))

	client := &mockClient{}
	handler := subscribeDispatchCore(context.Background(), client, stats.NoopObserver{}, info, fnVal, formatsZero, nil, nil)

	handler(&pahomqtt5.Publish{
		Topic:   "sensors/f47ac10b-58cc-4372-a567-0e02b2c3d479/readings",
		Payload: []byte(`{"sensor_id":"00000000-0000-0000-0000-000000000000","value":22.5}`),
	})

	if callCount != 1 {
		t.Fatalf("handler call count: want 1, got %d", callCount)
	}
	// The topic-derived sensorID must OVERWRITE the payload's own
	// (deliberately different) sensor_id — proves topic-var merge runs
	// inside the shared core, not just through one specific entry point.
	if received.SensorID != "f47ac10b-58cc-4372-a567-0e02b2c3d479" {
		t.Errorf("SensorID: want merged from topic, got %q", received.SensorID)
	}
	if received.Value != 22.5 {
		t.Errorf("Value: want 22.5, got %v", received.Value)
	}
}

// TestSubscribeDispatchCore_DecodeFailure_InvokesOnError proves a decode
// failure reaches onErrorFn (the dispatchFailure fallback's final tier,
// since no ErrorChannel/DeadLetter is declared on this fixture) and that
// the handler itself is never invoked — exercised directly against
// [subscribeDispatchCore], independent of which entry point built it.
func TestSubscribeDispatchCore_DecodeFailure_InvokesOnError(t *testing.T) {
	uuidCodec := codex.String().Refine(validate.UUID)
	ch := events.NewChannel[sensorReading](
		"sensors/{sensorID}/readings",
		sensorCodec,
		events.NewTopicParam("sensorID", uuidCodec,
			func(r sensorReading) string { return r.SensorID },
			func(r *sensorReading, v string) { r.SensorID = v }),
	).WithSubscribe(events.Subscribe{})

	handle, err := ch.Handle(nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	handleVal := reflect.ValueOf(handle)
	info := buildErasedSubscriberHandleFields(handleVal, handleVal.Elem())

	var handlerCalled bool
	fnVal := reflect.ValueOf(func(_ context.Context, _ sensorReading) error {
		handlerCalled = true
		return nil
	})
	formatsZero := reflect.Zero(info.decodeMergedMethod.Type().In(2))

	var gotErr SubscribeError
	onError := func(e SubscribeError) { gotErr = e }

	client := &mockClient{}
	handler := subscribeDispatchCore(context.Background(), client, stats.NoopObserver{}, info, fnVal, formatsZero, nil, onError)

	handler(&pahomqtt5.Publish{
		Topic:   "sensors/f47ac10b-58cc-4372-a567-0e02b2c3d479/readings",
		Payload: []byte(`not valid json`),
	})

	if handlerCalled {
		t.Error("handler must not be called on a decode failure")
	}
	if gotErr.Kind != KindDecode {
		t.Errorf("onError Kind: want KindDecode, got %v", gotErr.Kind)
	}
}
