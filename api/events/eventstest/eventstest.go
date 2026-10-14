package eventstest

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/stats"
	"github.com/DaniDeer/go-codex/validate"
)

// SensorReading is the canonical message type [AssertPublishSubscribeRoundTrip]
// publishes and subscribes — deliberately the SAME shape
// (SensorID/Value) already used identically, independently, across
// adapters/mqtt5's, adapters/mqtt's, and adapters/zeromq's own test
// fixtures, so a conforming adapter's test author has nothing new to
// learn about the message shape itself.
type SensorReading struct {
	SensorID string
	Value    float64
}

var sensorReadingCodec = codex.Struct[SensorReading](
	codex.RequiredField("sensor_id",
		codex.String().Refine(validate.UUID),
		func(r SensorReading) string { return r.SensorID },
		func(r *SensorReading, v string) { r.SensorID = v },
	),
	codex.RequiredField("value",
		codex.Float64(),
		func(r SensorReading) float64 { return r.Value },
		func(r *SensorReading, v float64) { r.Value = v },
	),
)

// SensorTopic is the topic [AssertPublishSubscribeRoundTrip]'s canonical
// channel publishes/subscribes on — namespaced under "eventstest/" to
// avoid colliding with any adapter's own test-fixture topics (e.g.
// "sensors/readings").
const SensorTopic = "eventstest/sensor/reading"

// NewSensorChannel is the canonical, undecorated channel declaration
// every conforming events adapter must correctly publish and subscribe.
func NewSensorChannel() events.Channel[SensorReading] {
	return events.NewChannel[SensorReading](SensorTopic, sensorReadingCodec)
}

// AssertPublishSubscribeRoundTrip proves client (with a transport already
// [events.Client.Attach]ed) correctly delivers a message published via
// the real [events.Client.Publish] encode path to a subscriber registered
// via [events.Client.ServeSubscribers], with the decoded value intact.
//
// isReady and deliver are the ONLY adapter-specific pieces this function
// needs — every events adapter's own mock-broker test harness implements
// these two steps differently (e.g. adapters/mqtt5's
// mockRouter.hasHandler/dispatch vs. adapters/mqtt's
// subscribedHandlerSnapshot/direct field reads), and a shared package
// cannot unify them without assuming one adapter's internal mock shape:
//
//   - isReady reports whether the subscriber's handler is registered on
//     the adapter's mock broker yet (ServeSubscribers runs in a
//     background goroutine — registration is asynchronous). Polled with
//     a short sleep loop, mirroring the SAME idiom every adapter's own
//     test suite already uses internally.
//   - deliver is called once, immediately after Publish, and must move
//     the REAL message [events.Client.Publish] just encoded (read back
//     from the adapter's own mock client, e.g. `client.lastPublished()`)
//     into the adapter's own mock-dispatch mechanism (e.g.
//     `router.dispatch(topic, msg)`) — proving Publish's real encode
//     path reaches Subscribe's real decode path, not a hand-crafted
//     payload bypassing one half of the round trip.
func AssertPublishSubscribeRoundTrip(t *testing.T, client *events.Client, isReady func() bool, deliver func()) {
	t.Helper()

	var mu sync.Mutex
	var got SensorReading
	var handlerCalled bool
	sub := NewSensorChannel().WithSubscribe(events.Subscribe{}).WithHandler(func(_ context.Context, msg SensorReading) error {
		mu.Lock()
		got = msg
		handlerCalled = true
		mu.Unlock()
		return nil
	})
	if err := sub.Register(client); err != nil {
		t.Fatalf("Register: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- client.ServeSubscribers(ctx) }()

	deadline := time.Now().Add(time.Second)
	for !isReady() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the subscriber's handler to be registered (isReady never returned true)")
		}
		time.Sleep(5 * time.Millisecond)
	}

	want := SensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 42.5}
	pub := NewSensorChannel().WithPublish(events.Publish{})
	if err := client.Publish(ctx, pub, want); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	deliver()

	deadline = time.Now().Add(time.Second)
	for {
		mu.Lock()
		called := handlerCalled
		mu.Unlock()
		if called {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the subscriber's handler to run after deliver()")
		}
		time.Sleep(5 * time.Millisecond)
	}

	cancel()
	<-done

	mu.Lock()
	defer mu.Unlock()
	if got != want {
		t.Errorf("subscriber received %+v, want %+v (the exact value published)", got, want)
	}
}

// ── Scenario 2: topic-var merge field ───────────────────────────────────────

// TopicVarReading is the canonical message type
// [AssertTopicVarMergeRoundTrip] publishes and subscribes — deliberately
// a SEPARATE type from [SensorReading] (not reused) to avoid any
// ambiguity about whether a merge-supplied field must also appear in the
// payload codec: ID here is supplied SOLELY by the topic template, never
// by the payload, mirroring api/rest/resttest's own PathParamReq
// precedent (a struct field intentionally absent from its payload codec,
// filled only by the merge mechanism).
type TopicVarReading struct {
	ID    string
	Value float64
}

var topicVarUUIDCodec = codex.String().Refine(validate.UUID)

var topicVarReadingCodec = codex.Struct[TopicVarReading](
	codex.RequiredField("value",
		codex.Float64(),
		func(r TopicVarReading) float64 { return r.Value },
		func(r *TopicVarReading, v float64) { r.Value = v },
	),
)

// TopicVarTopicTemplate is [NewTopicVarChannel]'s declared topic
// template — the `{id}` segment is merged into [TopicVarReading.ID] via
// [events.NewTopicParam], never decoded from the payload.
const TopicVarTopicTemplate = "eventstest/topicvar/{id}/reading"

// TopicVarConcreteTopic is the concrete topic
// [AssertTopicVarMergeRoundTrip] actually publishes/delivers on — the
// `{id}` segment resolved to a fixed, valid UUID.
const TopicVarConcreteTopic = "eventstest/topicvar/f47ac10b-58cc-4372-a567-0e02b2c3d479/reading"

// NewTopicVarChannel is the canonical "topic-var merge field" channel
// declaration: a conforming adapter must merge the topic's `{id}`
// segment directly into the decoded request value — the handler itself
// must never need to re-extract it manually.
func NewTopicVarChannel() events.Channel[TopicVarReading] {
	return events.NewChannel[TopicVarReading](TopicVarTopicTemplate, topicVarReadingCodec,
		events.NewTopicParam("id", topicVarUUIDCodec,
			func(r TopicVarReading) string { return r.ID },
			func(r *TopicVarReading, v string) { r.ID = v },
		),
	)
}

// AssertTopicVarMergeRoundTrip proves a conforming adapter merges
// [NewTopicVarChannel]'s `{id}` topic segment into the decoded value
// automatically. Takes the SAME isReady/deliver shape as
// [AssertPublishSubscribeRoundTrip] — see that function's doc comment
// for the full rationale.
func AssertTopicVarMergeRoundTrip(t *testing.T, client *events.Client, isReady func() bool, deliver func()) {
	t.Helper()

	var mu sync.Mutex
	var got TopicVarReading
	var handlerCalled bool
	sub := NewTopicVarChannel().WithSubscribe(events.Subscribe{}).WithHandler(func(_ context.Context, msg TopicVarReading) error {
		mu.Lock()
		got = msg
		handlerCalled = true
		mu.Unlock()
		return nil
	})
	if err := sub.Register(client); err != nil {
		t.Fatalf("Register: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- client.ServeSubscribers(ctx) }()

	deadline := time.Now().Add(time.Second)
	for !isReady() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the subscriber's handler to be registered (isReady never returned true)")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// want.ID is set so Publish's own topic-building step (which merges
	// {id} from the SAME getter NewTopicVarChannel declares) resolves the
	// concrete topic correctly — it is still NEVER encoded into the
	// payload itself (topicVarReadingCodec declares no "id" field at
	// all); on the SUBSCRIBE side it only reaches the handler via
	// topic-var merge, proving the merge mechanism, not a payload round
	// trip.
	want := TopicVarReading{ID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 7.25}
	pub := NewTopicVarChannel().WithPublish(events.Publish{})
	if err := client.Publish(ctx, pub, want); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	deliver()

	deadline = time.Now().Add(time.Second)
	for {
		mu.Lock()
		called := handlerCalled
		mu.Unlock()
		if called {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the subscriber's handler to run after deliver()")
		}
		time.Sleep(5 * time.Millisecond)
	}

	cancel()
	<-done

	mu.Lock()
	defer mu.Unlock()
	if got != want {
		t.Errorf("subscriber received %+v, want %+v (ID merged from the topic, Value from the payload)", got, want)
	}
}

// ── Scenario 3: capability-requirement rejection ────────────────────────────

// CapabilityRejectionReading is the canonical message type
// [NewCapabilityRejectionChannel] declares — never actually delivered,
// since the channel is expected to fail at [events.Client.ServeSubscribers]
// time, before any message could be received.
type CapabilityRejectionReading struct{ Value float64 }

var capabilityRejectionReadingCodec = codex.Struct[CapabilityRejectionReading](
	codex.RequiredField("value",
		codex.Float64(),
		func(r CapabilityRejectionReading) float64 { return r.Value },
		func(r *CapabilityRejectionReading, v float64) { r.Value = v },
	),
)

// CapabilityRejectionTopic is [NewCapabilityRejectionChannel]'s topic.
const CapabilityRejectionTopic = "eventstest/capability-rejection/reading"

// NewCapabilityRejectionChannel is the canonical "unsatisfiable
// capability requirement" channel: it declares [events.RequireHWM],
// whose own doc comment confirms it is "satisfiable only by
// adapters/zeromq.HWM — no mqtt/mqtt5 equivalent exists" — a conforming
// MQTT-family adapter's [events.Client.ServeSubscribers] MUST reject a
// subscriber registered against this channel with a non-nil error,
// before entering its blocking per-entry subscribe loop.
func NewCapabilityRejectionChannel() events.Channel[CapabilityRejectionReading] {
	return events.NewChannel[CapabilityRejectionReading](CapabilityRejectionTopic, capabilityRejectionReadingCodec,
		events.RequireHWM(1),
	)
}

// AssertCapabilityRejected proves err (the error returned by
// [events.Client.ServeSubscribers] after registering a subscriber for
// [NewCapabilityRejectionChannel]) is non-nil.
func AssertCapabilityRejected(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("want a non-nil error from ServeSubscribers for an unsatisfiable capability requirement, got nil")
	}
}

// ── Scenario 4: security-gate rejection (REDUCED RIGOR — see doc.go) ───────

// SecurityRejectionReading is the canonical message type
// [NewSecurityRejectionChannel] declares.
type SecurityRejectionReading struct{ Value float64 }

var securityRejectionReadingCodec = codex.Struct[SecurityRejectionReading](
	codex.RequiredField("value",
		codex.Float64(),
		func(r SecurityRejectionReading) float64 { return r.Value },
		func(r *SecurityRejectionReading, v float64) { r.Value = v },
	),
)

// SecurityRejectionTopic is [NewSecurityRejectionChannel]'s topic.
const SecurityRejectionTopic = "eventstest/security-rejection/reading"

// SecurityRejectionScheme is the security scheme name
// [NewSecurityRejectionChannel] declares and [AssertSecurityRejectionRoundTrip]
// asserts [stats.SecurityObserver.RecordSecurityRejection] is reported
// against.
const SecurityRejectionScheme = "apiKeyAuth"

// securityRejectionIn is the Bound-mechanism In shape
// [NewSecurityRejectionChannel]'s middleware declares — deliberately
// EMPTY, mirroring adapters/mqtt's own existing security-rejection test
// convention exactly (see AssertSecurityRejectionRoundTrip's doc comment
// for why no real credential is extracted here).
type securityRejectionIn struct{}

// securityRejectionOut is the Bound-mechanism Out shape
// [NewSecurityRejectionChannel]'s middleware declares.
type securityRejectionOut struct{ GrantedScopes map[string][]string }

// NewSecurityRejectionChannel is the canonical "security-gate rejection"
// channel declaration (undecorated — call `.WithSubscribe(...)
// .SubscribeBoundMW(...)` to attach the always-rejecting middleware, as
// [AssertSecurityRejectionRoundTrip] does).
func NewSecurityRejectionChannel() events.Channel[SecurityRejectionReading] {
	return events.NewChannel[SecurityRejectionReading](SecurityRejectionTopic, securityRejectionReadingCodec)
}

// SecurityRejectionRequiredScope is the scope
// [NewSecurityRejectionChannel]'s declared [events.SecurityRequirement]
// demands — [newSecurityRejectionBoundMW]'s Fn deliberately NEVER grants
// it (see that function's doc comment for why), so
// [events.CheckScopes] — not the middleware Fn itself — is what
// actually rejects.
const SecurityRejectionRequiredScope = "read:eventstest"

// newSecurityRejectionBoundMW returns the
// [events.BoundSecuritySubscribeMiddleware] [AssertSecurityRejectionRoundTrip]
// attaches via `.SubscribeBoundMW(...)`.
//
// Its Fn ALWAYS SUCCEEDS (returns a nil error) but grants NO scopes at
// all — confirmed directly against adapters/mqtt5's own dispatch-core
// classification logic: a middleware Fn that itself RETURNS AN ERROR is
// classified as a generic handler failure (KindHandler), NOT a security
// rejection, and does NOT call
// [stats.SecurityObserver.RecordSecurityRejection] — only a Fn that
// SUCCEEDS with insufficient granted scopes, subsequently rejected by
// [events.CheckScopes], is classified as KindSecurity and triggers
// RecordSecurityRejection. This Fn is deliberately shaped to hit that
// exact path, not the Fn-error path, so the scenario proves what it
// claims to prove.
func newSecurityRejectionBoundMW() events.BoundSubscribeMiddleware[SecurityRejectionReading, securityRejectionIn, securityRejectionOut] {
	return events.BoundSecuritySubscribeMiddleware[SecurityRejectionReading, securityRejectionIn, securityRejectionOut](
		SecurityRejectionScheme, events.APIKeyScheme("X-Eventstest-Api-Key", "header"), []string{SecurityRejectionRequiredScope},
		func(_ context.Context, _ *SecurityRejectionReading, _ securityRejectionIn) (securityRejectionOut, error) {
			return securityRejectionOut{GrantedScopes: map[string][]string{SecurityRejectionScheme: nil}}, nil
		},
	)
}

// securityRejectionSpyObserver is a minimal [stats.SecurityObserver] spy
// recording every RecordSecurityRejection call it receives — mirrors
// api/rest/resttest's own SpyObserver precedent, scoped to this one
// interface since that's all this scenario needs.
type securityRejectionSpyObserver struct {
	stats.NoopObserver
	calls []securityRejectionCall
}

type securityRejectionCall struct {
	location string
	scheme   string
}

func (s *securityRejectionSpyObserver) RecordSecurityRejection(location, scheme string) {
	s.calls = append(s.calls, securityRejectionCall{location: location, scheme: scheme})
}

// AssertSecurityRejectionRoundTrip proves a conforming adapter's security
// gate rejects a message whose granted scopes never satisfy the declared
// requirement: the handler is NEVER called, and
// [stats.SecurityObserver.RecordSecurityRejection] fires exactly once
// with [SecurityRejectionTopic]/[SecurityRejectionScheme].
//
// REDUCED RIGOR, DOCUMENTED, NOT A GAP: this scenario proves the
// security-GATE WIRING only (an insufficiently-scoped grant correctly
// blocks the handler and is correctly observed) — it does NOT prove real
// credential round-trip validation (missing/wrong/valid key), because
// MQTT v3 has NO out-of-band credential channel (no User Properties) to
// extract a real credential from at all — confirmed directly from
// adapters/mqtt's own existing security-rejection test convention
// (TestServeSubscribers_SecurityRejection_CallsSecurityObserver), which
// uses the SAME "Fn declares no real credential extraction" shape (there,
// via an always-erroring Fn instead — see [newSecurityRejectionBoundMW]'s
// own doc comment for why THIS scenario deliberately uses a
// succeeds-but-under-scoped Fn instead, the only shape that actually
// reaches [stats.SecurityObserver.RecordSecurityRejection] per
// adapters/mqtt5's own dispatch-core classification). Full
// credential-validation rigor remains adapters/mqtt5-only (see that
// package's own GrantedScopes/ContextField test) and is a PERMANENT,
// protocol-level asymmetry, not a temporary gap to close in a future
// round.
func AssertSecurityRejectionRoundTrip(t *testing.T, client *events.Client, isReady func() bool, deliver func()) {
	t.Helper()

	var mu sync.Mutex
	var handlerCalled bool
	sub := NewSecurityRejectionChannel().
		WithSubscribe(events.Subscribe{Security: []events.SecurityRequirement{events.Require(SecurityRejectionScheme, SecurityRejectionRequiredScope)}}).
		SubscribeBoundMW(newSecurityRejectionBoundMW()).
		WithHandler(func(_ context.Context, _ SecurityRejectionReading) error {
			mu.Lock()
			handlerCalled = true
			mu.Unlock()
			return nil
		})
	if err := sub.Register(client); err != nil {
		t.Fatalf("Register: %v", err)
	}

	spy := &securityRejectionSpyObserver{}
	ctx, cancel := context.WithTimeout(stats.WithObserver(context.Background(), spy), 2*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- client.ServeSubscribers(ctx) }()

	deadline := time.Now().Add(time.Second)
	for !isReady() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the subscriber's handler to be registered (isReady never returned true)")
		}
		time.Sleep(5 * time.Millisecond)
	}

	pub := NewSecurityRejectionChannel().WithPublish(events.Publish{})
	if err := client.Publish(ctx, pub, SecurityRejectionReading{Value: 1}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	deliver()

	deadline = time.Now().Add(time.Second)
	for {
		mu.Lock()
		spyCalls := len(spy.calls)
		mu.Unlock()
		if spyCalls > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for RecordSecurityRejection to be called after deliver()")
		}
		time.Sleep(5 * time.Millisecond)
	}

	cancel()
	<-done

	mu.Lock()
	defer mu.Unlock()
	if handlerCalled {
		t.Error("handler must NOT be called when the security middleware rejects")
	}
	if len(spy.calls) != 1 {
		t.Fatalf("want exactly 1 RecordSecurityRejection call, got %d: %+v", len(spy.calls), spy.calls)
	}
	got := spy.calls[0]
	if got.location != SecurityRejectionTopic || got.scheme != SecurityRejectionScheme {
		t.Errorf("RecordSecurityRejection call = %+v, want {location:%s scheme:%s}", got, SecurityRejectionTopic, SecurityRejectionScheme)
	}
}
