package zeromq

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/validate"
)

// ── fixtures ─────────────────────────────────────────────────────────────

type tdIn struct{ Key string }

var tdInCodec = codex.Struct[tdIn](
	codex.RequiredField("key", codex.String().Refine(validate.NonEmptyString),
		func(in tdIn) string { return in.Key },
		func(in *tdIn, v string) { in.Key = v },
	),
)

type tdOut struct{ Value string }

var tdOutCodec = codex.Struct[tdOut](
	codex.RequiredField("value", codex.String().Refine(validate.NonEmptyString),
		func(out tdOut) string { return out.Value },
		func(out *tdOut, v string) { out.Value = v },
	),
)

func newTDDeclaration(name string) events.Middleware[tdIn, tdOut] {
	return events.NewMiddleware(middleware.NewDeclaration(name, tdInCodec, tdOutCodec))
}

// tdEmpty is an In/Out shape with NO required fields.
type tdEmpty struct{}

var tdEmptyCodec = codex.Struct[tdEmpty]()

func newTDEmptyDeclaration(name string) events.Middleware[tdEmpty, tdEmpty] {
	return events.NewMiddleware(middleware.NewDeclaration(name, tdEmptyCodec, tdEmptyCodec))
}

func newSubscriberHandle(subscriber events.Subscriber[sensorReading]) *events.ChannelHandle[sensorReading] {
	h, err := subscriber.Handle(nil)
	if err != nil {
		panic(err)
	}
	return h
}

func runSubscribeWithHandle(handle *events.ChannelHandle[sensorReading], sock *mockSocket, fn func(context.Context, sensorReading) error, opts SubscribeOptions[sensorReading]) error {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	return subscribeWithHandle(ctx, sock, handle, fn, opts)
}

// ── Transform: happy path, enrichment ─────────────────────────────────────

func TestSubscribe_Transform_HappyPath_EnrichesMsg(t *testing.T) {
	mw := newTDDeclaration("region-policy").
		WithSubscribeTopic(events.NewTopicParam("region", codex.String(),
			func(in tdIn) string { return in.Key },
			func(in *tdIn, v string) { in.Key = v },
		))
	subscriber := events.NewChannel[sensorReading]("sensors/{region}/readings", sensorCodec).
		WithSubscribe(events.Subscribe{Summary: "test"})
	subscriber = events.Transform(subscriber, mw, func(ctx context.Context, msg *sensorReading, in tdIn) error {
		msg.SensorID = in.Key + "-enriched"
		return nil
	})
	handle := newSubscriberHandle(subscriber)

	var received sensorReading
	sock := &mockSocket{
		inFrames: [][][]byte{
			{[]byte("sensors/us-east/readings"), []byte(validSensorJSON)},
		},
	}
	_ = runSubscribeWithHandle(handle, sock, func(_ context.Context, r sensorReading) error {
		received = r
		return nil
	}, SubscribeOptions[sensorReading]{})

	if received.SensorID != "us-east-enriched" {
		t.Errorf("want enriched SensorID %q, got %q", "us-east-enriched", received.SensorID)
	}
}

// ── Transform: In-decode failure short-circuits, handler never called ───

func TestSubscribe_Transform_InDecodeFailure_HandlerNotCalled(t *testing.T) {
	mw := newTDDeclaration("region-policy").
		WithSubscribeTopic(events.NewTopicParam("region", codex.String().Refine(validate.UUID),
			func(in tdIn) string { return in.Key },
			func(in *tdIn, v string) { in.Key = v },
		))
	subscriber := events.NewChannel[sensorReading]("sensors/{region}/readings", sensorCodec).
		WithSubscribe(events.Subscribe{Summary: "test"})
	handlerCalled := false
	subscriber = events.Transform(subscriber, mw, func(ctx context.Context, msg *sensorReading, in tdIn) error {
		handlerCalled = true
		return nil
	})
	handle := newSubscriberHandle(subscriber)

	var gotErr SubscribeError
	sock := &mockSocket{
		inFrames: [][][]byte{
			// "not-a-uuid" is a well-formed topic segment (matches the
			// template structurally) but fails mw's own InCodec (UUID).
			{[]byte("sensors/not-a-uuid/readings"), []byte(validSensorJSON)},
		},
	}
	_ = runSubscribeWithHandle(handle, sock, func(_ context.Context, r sensorReading) error { return nil },
		SubscribeOptions[sensorReading]{OnError: func(e SubscribeError) { gotErr = e }})

	if handlerCalled {
		t.Error("want handler NOT called when middleware In-decode fails")
	}
	var mie events.MiddlewareInputError
	if !errors.As(gotErr.Err, &mie) {
		t.Errorf("want MiddlewareInputError, got %v", gotErr.Err)
	}
}

// ── Transform: fn error surfaces as events.MiddlewareError ──────────────

func TestSubscribe_Transform_FnError_WrapsAsMiddlewareError(t *testing.T) {
	mw := newTDEmptyDeclaration("region-policy")
	subscriber := events.NewChannel[sensorReading]("sensors/readings", sensorCodec).
		WithSubscribe(events.Subscribe{Summary: "test"})
	handlerCalled := false
	subscriber = events.Transform(subscriber, mw, func(ctx context.Context, msg *sensorReading, in tdEmpty) error {
		return errors.New("boom")
	})
	handle := newSubscriberHandle(subscriber)

	var gotErr SubscribeError
	sock := &mockSocket{
		inFrames: [][][]byte{
			{[]byte("sensors/readings"), []byte(validSensorJSON)},
		},
	}
	_ = runSubscribeWithHandle(handle, sock, func(_ context.Context, r sensorReading) error { handlerCalled = true; return nil },
		SubscribeOptions[sensorReading]{OnError: func(e SubscribeError) { gotErr = e }})

	if handlerCalled {
		t.Error("want handler NOT called when middleware fn returns an error")
	}
	var mwErr events.MiddlewareError
	if !errors.As(gotErr.Err, &mwErr) {
		t.Fatalf("want events.MiddlewareError, got %v", gotErr.Err)
	}
	if mwErr.Name != "region-policy" {
		t.Errorf("want Name %q, got %q", "region-policy", mwErr.Name)
	}
}

// ── Channel-agnostic .Use(mw) dispatch ───────────────────────────────────

func TestSubscribe_Use_AgnosticMiddleware_Dispatches(t *testing.T) {
	callCount := 0
	mw := newTDEmptyDeclaration("agnostic-policy").
		WithReceive(func(ctx context.Context, in tdEmpty) error {
			callCount++
			return nil
		})
	subscriber := events.NewChannel[sensorReading]("sensors/readings", sensorCodec).
		WithSubscribe(events.Subscribe{Summary: "test"}).Use(mw)
	handle := newSubscriberHandle(subscriber)

	sock := &mockSocket{
		inFrames: [][][]byte{
			{[]byte("sensors/readings"), []byte(validSensorJSON)},
		},
	}
	_ = runSubscribeWithHandle(handle, sock, func(_ context.Context, r sensorReading) error { return nil }, SubscribeOptions[sensorReading]{})

	if callCount != 1 {
		t.Errorf("want bundled receiveFn called exactly once, got %d", callCount)
	}
}

// ── ClientTransform (publish side): happy path, encodes Out into topic vars ──

func TestPublish_ClientTransform_HappyPath_EncodesOutIntoTopicVars(t *testing.T) {
	mw := newTDDeclaration("region-policy").
		WithPublishTopic(events.NewTopicParam("region", codex.String(),
			func(out tdOut) string { return out.Value },
			func(out *tdOut, v string) { out.Value = v },
		))
	publisher := events.NewChannel[sensorReading]("sensors/{region}/readings", sensorCodec).
		WithPublish(events.Publish{Summary: "test"})
	publisher = events.ClientTransform(publisher, mw, func(ctx context.Context, msg sensorReading) (tdOut, error) {
		return tdOut{Value: "us-west"}, nil
	})
	handle, err := publisher.Handle(nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	sock := &mockSocket{}
	reading := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 22.5}
	if err := publish(context.Background(), sock, handle, reading, nil, false, PublishOptions[sensorReading]{}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if len(sock.sentFrames) != 1 {
		t.Fatalf("want 1 send, got %d", len(sock.sentFrames))
	}
	if got := string(sock.sentFrames[0][0]); got != "sensors/us-west/readings" {
		t.Errorf("want topic %q, got %q", "sensors/us-west/readings", got)
	}
}

// ── ClientTransform: fn error aborts before publish ──────────────────────

func TestPublish_ClientTransform_FnError_AbortsBeforePublish(t *testing.T) {
	mw := newTDEmptyDeclaration("region-policy")
	publisher := events.NewChannel[sensorReading]("sensors/readings", sensorCodec).
		WithPublish(events.Publish{Summary: "test"})
	publisher = events.ClientTransform(publisher, mw, func(ctx context.Context, msg sensorReading) (tdEmpty, error) {
		return tdEmpty{}, errors.New("boom")
	})
	handle, err := publisher.Handle(nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	sock := &mockSocket{}
	reading := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 22.5}
	pubErr := publish(context.Background(), sock, handle, reading, nil, false, PublishOptions[sensorReading]{})
	if pubErr == nil {
		t.Fatal("want error from ClientTransform fn")
	}
	if len(sock.sentFrames) != 0 {
		t.Error("want no message published when middleware fn errors")
	}
}

// ── Phase 10: events property vocabulary axis + Bug 1/Bug 2 side-track ──

// D1: codec-backed middleware dispatch (Transform) runs AFTER the paired
// security Fn, both pre-handler — confirms zeromq's dispatch order matches
// mqtt5's/REST's established order, confirming the mechanism is genuinely
// transport-agnostic.
func TestSubscribe_MiddlewareDispatch_RunsAfterPairedSecurity(t *testing.T) {
	var order []string
	mw := newTDEmptyDeclaration("order-policy")

	subscriber := events.NewChannel[sensorReading]("sensors/readings", sensorCodec).
		WithSubscribe(events.Subscribe{Summary: "test"}).
		SubscribeMW(nil, func(_ context.Context, _ *sensorReading, _ []route.SecurityRequirement) error {
			order = append(order, "security")
			return nil
		})
	subscriber = events.Transform(subscriber, mw, func(ctx context.Context, msg *sensorReading, in tdEmpty) error {
		order = append(order, "middleware")
		return nil
	})
	handle := newSubscriberHandle(subscriber)

	sock := &mockSocket{
		inFrames: [][][]byte{{[]byte("sensors/readings"), []byte(validSensorJSON)}},
	}
	_ = runSubscribeWithHandle(handle, sock, func(_ context.Context, _ sensorReading) error { return nil }, SubscribeOptions[sensorReading]{})

	if len(order) != 2 || order[0] != "security" || order[1] != "middleware" {
		t.Errorf("want dispatch order [security, middleware], got %v", order)
	}
}

// Bug 1 fix verification: with ALL THREE tiers present on the SAME var
// name (explicit PublishOptions.Vars, a Middleware's WithPublishTopic, AND
// the channel's own NewTopicParam-derived value), the final resolved
// value follows explicit > middleware-derived > channel-own-derived — the
// CORRECTED order (previously backwards: channel-own beat middleware).
// Mirrors mqtt5's identical test, confirming the fix is transport-agnostic.
func TestPublish_MiddlewareDispatch_ValuePrecedence_ExplicitBeatsMiddlewareBeatsChannelOwn(t *testing.T) {
	uuidCodec := codex.String().Refine(validate.UUID)
	mw := newTDDeclaration("sensor-override-policy").
		WithPublishTopic(events.NewTopicParam("sensorID", uuidCodec,
			func(o tdOut) string { return o.Value },
			func(o *tdOut, v string) { o.Value = v }))

	pub := events.NewChannel[sensorReading](
		"sensors/{sensorID}/readings", sensorCodec,
		events.NewTopicParam("sensorID", uuidCodec,
			func(r sensorReading) string { return r.SensorID },
			func(r *sensorReading, v string) { r.SensorID = v }),
	).WithPublish(events.Publish{})
	pub = events.ClientTransform(pub, mw, func(ctx context.Context, msg sensorReading) (tdOut, error) {
		return tdOut{Value: "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"}, nil
	})
	handle, err := pub.Handle(nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	reading := sensorReading{SensorID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", Value: 1}
	ctx := context.Background()

	// (a) explicit ALWAYS wins over middleware-derived.
	sock := &mockSocket{}
	if err := publish(ctx, sock, handle, reading,
		map[string]string{"sensorID": "cccccccc-cccc-cccc-cccc-cccccccccccc"}, true, PublishOptions[sensorReading]{}); err != nil {
		t.Fatalf("publish (explicit): %v", err)
	}
	if !strings.Contains(string(sock.sentFrames[0][0]), "cccccccc-cccc-cccc-cccc-cccccccccccc") {
		t.Errorf("want explicit value to win, got topic %q", sock.sentFrames[0][0])
	}

	// (b) middleware-derived wins over channel-own-derived (the Bug 1 fix).
	sock2 := &mockSocket{}
	if err := publishHandle(ctx, sock2, handle, reading, PublishOptions[sensorReading]{}); err != nil {
		t.Fatalf("publishHandle: %v", err)
	}
	if !strings.Contains(string(sock2.sentFrames[0][0]), "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb") {
		t.Errorf("want middleware-derived value to beat channel-own, got topic %q", sock2.sentFrames[0][0])
	}
}

// Bug 2 fix verification: a DecodeIn failure calls
// stats.ReportErrors(obs, "middleware:in", err), and the fn's own business
// error calls stats.ReportErrors(obs, "middleware:fn", err).
func TestSubscribe_Observer_ReportsMiddlewareInAndFnLocations(t *testing.T) {
	// "middleware:in": a REQUIRED In field fails to decode/validate. zeromq
	// has no property mechanism, so this uses the topic-var axis instead
	// (property axis on zeromq is covered by
	// TestMiddleware_WithSubscribeProperty_RequiredButAdapterSuppliesNoPropertyMap
	// at the api/events level) — either axis exercises the SAME DecodeIn
	// failure path this test targets. Mirrors
	// TestSubscribe_Transform_InDecodeFailure_HandlerNotCalled's own
	// "well-formed segment, fails mw's stricter codec" technique.
	inMW := newTDDeclaration("region-required-policy").
		WithSubscribeTopic(events.NewTopicParam("region", codex.String().Refine(validate.UUID),
			func(in tdIn) string { return in.Key },
			func(in *tdIn, v string) { in.Key = v }))
	subIn := events.NewChannel[sensorReading]("sensors/{region}/readings", sensorCodec).WithSubscribe(events.Subscribe{Summary: "test"})
	subIn = events.Transform(subIn, inMW, func(ctx context.Context, msg *sensorReading, in tdIn) error { return nil })
	handleIn := newSubscriberHandle(subIn)

	obsIn := &testObserver{}
	sockIn := &mockSocket{
		inFrames: [][][]byte{{[]byte("sensors/not-a-uuid/readings"), []byte(validSensorJSON)}},
	}
	_ = runSubscribeWithHandle(handleIn, sockIn, func(_ context.Context, _ sensorReading) error { return nil },
		SubscribeOptions[sensorReading]{Observer: obsIn})

	foundIn := false
	for _, loc := range obsIn.validationLocations {
		if loc == "middleware:in" {
			foundIn = true
		}
	}
	if !foundIn {
		t.Errorf("want a RecordValidationError call with location %q, got %v", "middleware:in", obsIn.validationLocations)
	}

	// "middleware:fn": the fn's own business error, shaped as
	// codex.ValidationErrors so it flows through stats.ReportErrors'
	// walkErrors and is observable via RecordValidationError.
	fnMW := newTDEmptyDeclaration("fn-error-policy")
	subFn := events.NewChannel[sensorReading]("sensors/readings", sensorCodec).WithSubscribe(events.Subscribe{Summary: "test"})
	subFn = events.Transform(subFn, fnMW, func(ctx context.Context, msg *sensorReading, in tdEmpty) error {
		return codex.ValidationErrors{{Field: "region", Err: errors.New("boom")}}
	})
	handleFn := newSubscriberHandle(subFn)

	obsFn := &testObserver{}
	sockFn := &mockSocket{
		inFrames: [][][]byte{{[]byte("sensors/readings"), []byte(validSensorJSON)}},
	}
	_ = runSubscribeWithHandle(handleFn, sockFn, func(_ context.Context, _ sensorReading) error { return nil },
		SubscribeOptions[sensorReading]{Observer: obsFn})

	foundFn := false
	for _, loc := range obsFn.validationLocations {
		if loc == "middleware:fn" {
			foundFn = true
		}
	}
	if !foundFn {
		t.Errorf("want a RecordValidationError call with location %q, got %v", "middleware:fn", obsFn.validationLocations)
	}
}

// Bug 2 fix verification, publish side — mirrors mqtt5's identical test
// and asymmetry note (publish-side dispatch only ever returns a fn
// business error; no separate DecodeIn-equivalent step exists there).
func TestPublish_Observer_ReportsMiddlewareInAndFnLocations(t *testing.T) {
	mw := newTDEmptyDeclaration("fn-error-policy")
	pub := events.NewChannel[sensorReading]("sensors/readings", sensorCodec).WithPublish(events.Publish{})
	pub = events.ClientTransform(pub, mw, func(ctx context.Context, msg sensorReading) (tdEmpty, error) {
		return tdEmpty{}, codex.ValidationErrors{{Field: "region", Err: errors.New("boom")}}
	})
	handle, err := pub.Handle(nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	obs := &testObserver{}
	sock := &mockSocket{}
	reading := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 1}
	_ = publishHandle(context.Background(), sock, handle, reading, PublishOptions[sensorReading]{Observer: obs})

	found := false
	for _, loc := range obs.validationLocations {
		if loc == "middleware:fn" {
			found = true
		}
	}
	if !found {
		t.Errorf("want a RecordValidationError call with location %q, got %v", "middleware:fn", obs.validationLocations)
	}
}

// Rest-middleware-conflict-detection-improvements' adapter-dispatch review
// found this EncodeOut failure was previously mislabeled as
// "middleware:fn" (indistinguishable from a real fn business error) —
// now reported as its own "middleware:out" location, symmetric with
// REST's own "middleware:out".
func TestPublish_Observer_ReportsMiddlewareOutLocation(t *testing.T) {
	mw := newTDDeclaration("tenant-required-policy")
	pub := events.NewChannel[sensorReading]("sensors/readings", sensorCodec).WithPublish(events.Publish{})
	pub = events.ClientTransform(pub, mw, func(ctx context.Context, msg sensorReading) (tdOut, error) {
		// Empty Value fails tdOutCodec's NonEmptyString refinement at
		// EncodeOut/OutCodec.Validate time, NOT the fn itself.
		return tdOut{Value: ""}, nil
	})
	handle, err := pub.Handle(nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	obs := &testObserver{}
	sock := &mockSocket{}
	reading := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 1}
	_ = publishHandle(context.Background(), sock, handle, reading, PublishOptions[sensorReading]{Observer: obs})

	found := false
	for _, loc := range obs.validationLocations {
		if loc == "middleware:out" {
			found = true
		}
	}
	if !found {
		t.Errorf("want a RecordValidationError call with location %q, got %v", "middleware:out", obs.validationLocations)
	}
}
