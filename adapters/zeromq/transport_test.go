package zeromq

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/stats"
)

// ── Client.Attach/Publish/Subscribe (Decision 5) ────────────────────────────

// mergeSensorChannel declares sensorReading's SensorID as a merge-capable
// topic param, so Client.Publish's reflection shim can exercise the
// EncodeVars/BuildTopic path end-to-end ("one struct, one call").
func mergeSensorChannel(topic string) events.Channel[sensorReading] {
	return events.NewChannel[sensorReading](topic, sensorCodec,
		events.NewTopicParam("sensorID", codex.String(),
			func(r sensorReading) string { return r.SensorID },
			func(r *sensorReading, v string) { r.SensorID = v },
		),
	)
}

func TestAttach_ClientPublish_RoundTrip(t *testing.T) {
	sock := &mockSocket{}
	client := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	if err := client.Attach(NewTransport(TransportOptions{Socket: sock})); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	pub := mergeSensorChannel("sensors/{sensorID}/readings").WithPublish(events.Publish{})
	reading := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 22.5}
	if err := client.Publish(context.Background(), pub, reading); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	sock.mu.Lock()
	defer sock.mu.Unlock()
	if len(sock.sentFrames) != 1 {
		t.Fatalf("want 1 sent message, got %d", len(sock.sentFrames))
	}
	gotTopic := string(sock.sentFrames[0][0])
	wantTopic := "sensors/f47ac10b-58cc-4372-a567-0e02b2c3d479/readings"
	if gotTopic != wantTopic {
		t.Errorf("topic = %q, want %q (EncodeVars/BuildTopic derivation failed)", gotTopic, wantTopic)
	}
}

// TestAttach_ClientPublish_AcceptsPreBuiltHandle_WithRouter confirms
// recoverHandle's dual-mode acceptance (api/events.WithRouter's own
// closed gap, docs/design/d-0008-declarative-router-groups.md): a
// caller who composed a Router-Mounted topic THEMSELVES, via
// pub.Handle(nil, events.WithRouter(rt)), can pass the resulting
// *events.ChannelHandle[T] straight to Client.Publish.
func TestAttach_ClientPublish_AcceptsPreBuiltHandle_WithRouter(t *testing.T) {
	sock := &mockSocket{}
	client := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	if err := client.Attach(NewTransport(TransportOptions{Socket: sock})); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	pub := events.NewChannel[sensorReading]("readings", sensorCodec).WithPublish(events.Publish{})
	rt := events.NewRouter("sensors")
	handle, err := pub.Handle(nil, events.WithRouter(rt))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	reading := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 22.5}
	if err := client.Publish(context.Background(), handle, reading); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	sock.mu.Lock()
	defer sock.mu.Unlock()
	if len(sock.sentFrames) != 1 {
		t.Fatalf("want 1 sent message, got %d", len(sock.sentFrames))
	}
	gotTopic := string(sock.sentFrames[0][0])
	const wantTopic = "sensors/readings"
	if gotTopic != wantTopic {
		t.Errorf("topic = %q, want %q (WithRouter composition not honored)", gotTopic, wantTopic)
	}
}

// TestAttach_ClientPublish_HonorsDeclaredCapabilities confirms Phase 4c
// (docs/design/d-0006-protocol-native-capabilities.md): a Capabilities
// value declared via [events.Publisher.WithOptions] is resolved and
// applied by [events.Client.Publish]'s reflection shim — closing the
// gap where this shim never configured HWM/Conflate at all.
func TestAttach_ClientPublish_HonorsDeclaredCapabilities(t *testing.T) {
	sock := &capableSocket{mockSocket: &mockSocket{}}
	client := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	if err := client.Attach(NewTransport(TransportOptions{Socket: sock})); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	pub := mergeSensorChannel("sensors/{sensorID}/readings").WithPublish(events.Publish{}).
		WithOptions(PublishOptions[sensorReading]{Capabilities: []Capability{HWM(42), Conflate(true)}})
	reading := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 22.5}
	if err := client.Publish(context.Background(), pub, reading); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	if sock.hwm != 42 {
		t.Errorf("HWM = %d, want 42 from declared Capabilities", sock.hwm)
	}
	if !sock.conflate {
		t.Error("want Conflate true from declared Capabilities")
	}
}

// TestTransport_Publish_WrongPubType/WrongMsgType_ReturnsTransportTypeMismatchError
// call [NewTransport]'s [events.Transport] interface directly rather than
// going through [events.Client.Publish] — since Go 1.27's generic-method
// retrofit (go127-generic-methods), Client.Publish's pub/msg params are
// statically typed ([publishArg][T]/T), so passing a wrong-shaped pub or a
// wrong-typed msg to Client.Publish itself is now a COMPILE error, not a
// runtime [events.TransportTypeMismatchError] — that mismatch can only still
// occur at the one layer that remains genuinely `any`-typed: the adapter's
// own [events.Transport] implementation, exercised here directly to keep
// this adapter's own reflection dispatch covered.
func TestTransport_Publish_WrongPubType_ReturnsTransportTypeMismatchError(t *testing.T) {
	sock := &mockSocket{}
	transport := NewTransport(TransportOptions{Socket: sock})
	err := transport.Publish(context.Background(), "not-a-publisher", sensorReading{})
	var mismatchErr events.TransportTypeMismatchError
	if !errors.As(err, &mismatchErr) {
		t.Fatalf("want TransportTypeMismatchError, got %v (%T)", err, err)
	}
}

func TestTransport_Publish_WrongMsgType_ReturnsTransportTypeMismatchError(t *testing.T) {
	sock := &mockSocket{}
	transport := NewTransport(TransportOptions{Socket: sock})
	pub := sensorChannel("sensors/readings").WithPublish(events.Publish{})
	err := transport.Publish(context.Background(), pub, "wrong-type")
	var mismatchErr events.TransportTypeMismatchError
	if !errors.As(err, &mismatchErr) {
		t.Fatalf("want TransportTypeMismatchError, got %v (%T)", err, err)
	}
}

func TestAttach_ClientSubscribe_RoundTrip(t *testing.T) {
	sock := &mockSocket{
		inFrames: [][][]byte{{[]byte("sensors/readings"), []byte(validSensorJSON)}},
	}
	client := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	if err := client.Attach(NewTransport(TransportOptions{Socket: sock})); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	sub := sensorChannel("sensors/readings").WithSubscribe(events.Subscribe{})
	received := make(chan sensorReading, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	// Subscribe blocks until ctx is cancelled, mirroring
	// ServeOneSubscriber's own documented blocking semantics.
	if err := client.Subscribe(ctx, sub, func(_ context.Context, r sensorReading) error {
		received <- r
		return nil
	}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	select {
	case r := <-received:
		if r.SensorID == "" {
			t.Fatal("expected non-empty SensorID")
		}
	default:
		t.Fatal("expected message to be delivered via Client.Subscribe")
	}
}

// TestAttach_ClientSubscribe_AcceptsPreBuiltHandle_WithRouter is
// TestAttach_ClientPublish_AcceptsPreBuiltHandle_WithRouter's Subscribe
// counterpart — a gap found via deep-dive-bug-hunt's go127-generic-methods
// focused round on api/events: Publish's dual-accept was tested with the
// handle form in all 3 adapters, but Subscribe's identical dual-accept
// mechanism had NEVER been exercised with the handle form anywhere.
func TestAttach_ClientSubscribe_AcceptsPreBuiltHandle_WithRouter(t *testing.T) {
	sock := &mockSocket{
		inFrames: [][][]byte{{[]byte("sensors/readings"), []byte(validSensorJSON)}},
	}
	client := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	if err := client.Attach(NewTransport(TransportOptions{Socket: sock})); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	sub := events.NewChannel[sensorReading]("readings", sensorCodec).WithSubscribe(events.Subscribe{})
	rt := events.NewRouter("sensors")
	handle, err := sub.Handle(nil, events.WithRouter(rt))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	received := make(chan sensorReading, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if err := client.Subscribe(ctx, handle, func(_ context.Context, r sensorReading) error {
		received <- r
		return nil
	}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	select {
	case r := <-received:
		if r.SensorID == "" {
			t.Fatal("expected non-empty SensorID")
		}
	default:
		t.Fatal("expected message to be delivered via Client.Subscribe (WithRouter composition not honored)")
	}
}

// TestAttach_ClientSubscribe_HonorsDeclaredCapabilities confirms Phase
// 4c (docs/design/d-0006-protocol-native-capabilities.md): a
// Capabilities value declared via [events.Subscriber.WithOptions] is
// resolved and applied by [events.Client.Subscribe]'s reflection shim —
// closing the gap where this shim never configured HWM/Conflate at all.
func TestAttach_ClientSubscribe_HonorsDeclaredCapabilities(t *testing.T) {
	sock := &capableSocket{mockSocket: &mockSocket{}}
	client := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	if err := client.Attach(NewTransport(TransportOptions{Socket: sock})); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	sub := sensorChannel("sensors/readings").WithSubscribe(events.Subscribe{}).
		WithOptions(SubscribeOptions[sensorReading]{Capabilities: []Capability{HWM(7), Conflate(true)}})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := client.Subscribe(ctx, sub, func(_ context.Context, _ sensorReading) error { return nil }); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	if sock.hwm != 7 {
		t.Errorf("HWM = %d, want 7 from declared Capabilities", sock.hwm)
	}
	if !sock.conflate {
		t.Error("want Conflate true from declared Capabilities")
	}
}

// TestAttach_ClientSubscribe_RegistersSpecIntoRealClient is a regression
// test proving Client.Subscribe registers sub's spec into the SAME
// client the caller attached (not a throwaway scratch client) — an
// earlier draft of transport.Subscribe used a scratch *events.Client
// (mirroring ServeOneSubscriber), which would silently leave the
// subscribe operation out of client.AsyncAPISpec().
func TestAttach_ClientSubscribe_RegistersSpecIntoRealClient(t *testing.T) {
	sock := &mockSocket{
		inFrames: [][][]byte{{[]byte("sensors/readings"), []byte(validSensorJSON)}},
	}
	client := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	if err := client.Attach(NewTransport(TransportOptions{Socket: sock})); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	sub := sensorChannel("sensors/readings").WithSubscribe(events.Subscribe{OperationID: "receiveReading"})
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := client.Subscribe(ctx, sub, func(context.Context, sensorReading) error { return nil }); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	doc, err := client.AsyncAPISpec()
	if err != nil {
		t.Fatalf("AsyncAPISpec: %v", err)
	}
	yamlBytes, err := doc.MarshalYAML()
	if err != nil {
		t.Fatalf("MarshalYAML: %v", err)
	}
	out := string(yamlBytes)
	for _, want := range []string{"sensors/readings:", "receiveReading", "action: receive"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected client.AsyncAPISpec() to include the subscribe channel/operation; missing %q\nfull output:\n%s", want, out)
		}
	}
}

// TestTransport_Subscribe_WrongSubType_ReturnsTransportTypeMismatchError calls
// [NewTransport]'s [events.Transport] interface directly — see
// TestTransport_Publish_WrongPubType_ReturnsTransportTypeMismatchError's doc
// comment for why Client.Subscribe itself can no longer exercise this path.
func TestTransport_Subscribe_WrongSubType_ReturnsTransportTypeMismatchError(t *testing.T) {
	sock := &mockSocket{}
	transport := NewTransport(TransportOptions{Socket: sock})
	err := transport.Subscribe(context.Background(), "not-a-subscriber", func() {})
	var mismatchErr events.TransportTypeMismatchError
	if !errors.As(err, &mismatchErr) {
		t.Fatalf("want TransportTypeMismatchError, got %v (%T)", err, err)
	}
}

// ── Decision 7: NewPublishTransport/NewSubscribeTransport (generic, no *events.Client needed) ──

func TestNewPublishTransport_PublishHandle_RoundTrip(t *testing.T) {
	sock := &mockSocket{}
	transport := NewPublishTransport[sensorReading](sock, PublishOptions[sensorReading]{})

	pub := mergeSensorChannel("sensors/{sensorID}/readings").WithPublish(events.Publish{})
	reading := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 22.5}
	if err := events.PublishHandle(context.Background(), pub, transport, reading); err != nil {
		t.Fatalf("PublishHandle: %v", err)
	}

	sock.mu.Lock()
	defer sock.mu.Unlock()
	if len(sock.sentFrames) != 1 {
		t.Fatalf("want 1 sent message, got %d", len(sock.sentFrames))
	}
	gotTopic := string(sock.sentFrames[0][0])
	wantTopic := "sensors/f47ac10b-58cc-4372-a567-0e02b2c3d479/readings"
	if gotTopic != wantTopic {
		t.Errorf("topic = %q, want %q", gotTopic, wantTopic)
	}
}

func TestNewSubscribeTransport_SubscribeHandle_RoundTrip(t *testing.T) {
	sock := &mockSocket{
		inFrames: [][][]byte{{[]byte("sensors/readings"), []byte(validSensorJSON)}},
	}
	subTransport := NewSubscribeTransport[sensorReading](sock, SubscribeOptions[sensorReading]{})

	sub := sensorChannel("sensors/readings").WithSubscribe(events.Subscribe{})
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	received := make(chan sensorReading, 1)
	// SubscribeHandle blocks until ctx is cancelled, mirroring
	// Client.Subscribe's own documented blocking semantics.
	if err := events.SubscribeHandle(ctx, sub, subTransport, func(_ context.Context, r sensorReading) error {
		received <- r
		return nil
	}); err != nil {
		t.Fatalf("SubscribeHandle: %v", err)
	}

	select {
	case r := <-received:
		if r.SensorID == "" {
			t.Fatal("expected non-empty SensorID")
		}
	default:
		t.Fatal("expected a received message before ctx cancellation")
	}
}

// ── Observer + ErrorChannel parity for Client.Attach (Decision 8) ──────────
//
// Confirmed gap (see docs/design/d-0002-pubsub-workflow-simplification.md's
// Decision 8): transport.Publish/Subscribe used to call neither
// stats.Observer NOR consult a declared events.ErrorChannel on a
// subscribe handler's returned error — these tests lock in the fix.

func TestAttach_ClientPublish_RecordsObserver(t *testing.T) {
	sock := &mockSocket{}
	client := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	if err := client.Attach(NewTransport(TransportOptions{Socket: sock})); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	obs := &testObserver{}
	ctx := stats.WithObserver(context.Background(), obs)
	pub := mergeSensorChannel("sensors/{sensorID}/readings").WithPublish(events.Publish{})
	reading := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 22.5}
	if err := client.Publish(ctx, pub, reading); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	if len(obs.publishes) != 1 || !obs.publishes[0] {
		t.Fatalf("RecordPublish calls = %v, want exactly one successful call", obs.publishes)
	}
	if len(obs.startSpanOps) != 1 || obs.startSpanOps[0] != "zmq.publish" {
		t.Errorf("TraceObserver.StartSpan calls = %v, want [zmq.publish]", obs.startSpanOps)
	}
}

func TestAttach_ClientSubscribe_RecordsObserver(t *testing.T) {
	sock := &mockSocket{
		inFrames: [][][]byte{{[]byte("sensors/readings"), []byte(validSensorJSON)}},
	}
	client := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	if err := client.Attach(NewTransport(TransportOptions{Socket: sock})); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	obs := &testObserver{}
	ctx, cancel := context.WithTimeout(stats.WithObserver(context.Background(), obs), 300*time.Millisecond)
	defer cancel()

	sub := sensorChannel("sensors/readings").WithSubscribe(events.Subscribe{})
	if err := client.Subscribe(ctx, sub, func(_ context.Context, _ sensorReading) error { return nil }); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	if len(obs.subscribes) != 1 || !obs.subscribes[0] {
		t.Fatalf("RecordSubscribe calls = %v, want exactly one successful call", obs.subscribes)
	}
}

// TestAttach_ClientSubscribe_HandlerError_MatchedErrorChannel_PublishesTypedPayload
// confirms a subscribe handler's returned domain error is redirected to a
// declared events.ErrorChannel's error-output topic through Client.Attach's
// Subscribe path.
func TestAttach_ClientSubscribe_HandlerError_MatchedErrorChannel_PublishesTypedPayload(t *testing.T) {
	sock := &mockSocket{
		inFrames: [][][]byte{{[]byte("sensors/readings"), []byte(validSensorJSON)}},
	}
	client := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	if err := client.Attach(NewTransport(TransportOptions{Socket: sock})); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	ch := events.NewChannel[sensorReading]("sensors/readings", sensorCodec,
		events.ErrorChannel[sensorZmqValidationErr, sensorZmqErrPayload](
			"sensors/readings/errors", sensorZmqErrPayloadCodec,
			func(e sensorZmqValidationErr) (sensorZmqErrPayload, error) {
				return sensorZmqErrPayload{Code: "out_of_range", Message: e.msg}, nil
			},
		),
	)
	sub := ch.WithSubscribe(events.Subscribe{})

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if err := client.Subscribe(ctx, sub, func(_ context.Context, _ sensorReading) error {
		return sensorZmqValidationErr{msg: "value too high"}
	}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	sock.mu.Lock()
	defer sock.mu.Unlock()
	found := false
	for _, frames := range sock.sentFrames {
		if len(frames) >= 2 && string(frames[0]) == "sensors/readings/errors" {
			found = true
			if !strings.Contains(string(frames[1]), "out_of_range") {
				t.Errorf("error payload = %s, want it to contain out_of_range", frames[1])
			}
		}
	}
	if !found {
		t.Fatal("expected a message published to the declared error-output topic")
	}
}

// ── Client.Attach honors the channel's declared format (d-0002-pubsub-workflow-simplification.md Decision 9) ──
//
// Confirmed gap (see docs/design/d-0002-pubsub-workflow-simplification.md's Decision 9): Client.Attach's
// Publish/Subscribe used to ALWAYS assume JSON, silently ignoring a
// channel's declared WithFormats/WithPublishFormats/WithSubscribeFormats
// — this test locks in the fix (round-trips YAML through Client.Attach).
func TestAttach_ClientPublishSubscribe_HonorsDeclaredYAMLFormat(t *testing.T) {
	pubSock := &mockSocket{}
	pubClient := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	if err := pubClient.Attach(NewTransport(TransportOptions{Socket: pubSock})); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	ch := events.NewChannel[sensorReading]("sensors/readings", sensorCodec,
		events.Formats(format.YAML(sensorCodec)),
	)
	pub := ch.WithPublish(events.Publish{})
	reading := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 22.5}
	if err := pubClient.Publish(context.Background(), pub, reading); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	pubSock.mu.Lock()
	if len(pubSock.sentFrames) != 1 {
		pubSock.mu.Unlock()
		t.Fatalf("want 1 sent message, got %d", len(pubSock.sentFrames))
	}
	gotPayload := pubSock.sentFrames[0][1]
	pubSock.mu.Unlock()

	// Confirm the WIRE bytes are actually YAML, not JSON — proves
	// EncodeWithFormats (not plain Encode) was used.
	if strings.HasPrefix(strings.TrimSpace(string(gotPayload)), "{") {
		t.Errorf("expected YAML wire payload, got JSON-shaped: %s", gotPayload)
	}
	if !strings.Contains(string(gotPayload), "value: 22.5") {
		t.Errorf("expected YAML payload containing 'value: 22.5', got: %s", gotPayload)
	}

	subSock := &mockSocket{
		inFrames: [][][]byte{{[]byte("sensors/readings"), gotPayload}},
	}
	subClient := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	if err := subClient.Attach(NewTransport(TransportOptions{Socket: subSock})); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	sub := ch.WithSubscribe(events.Subscribe{})
	received := make(chan sensorReading, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if err := subClient.Subscribe(ctx, sub, func(_ context.Context, r sensorReading) error {
		received <- r
		return nil
	}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	select {
	case r := <-received:
		if r.SensorID != reading.SensorID || r.Value != reading.Value {
			t.Errorf("round-tripped reading = %+v, want %+v", r, reading)
		}
	default:
		t.Fatal("expected message to be delivered via Client.Subscribe")
	}
}
