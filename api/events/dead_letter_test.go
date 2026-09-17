package events_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/stats"
)

func TestDeadLetterFor_UndeclaredHandle_ReturnsFalse(t *testing.T) {
	b := events.NewClient(events.WithInfo(testInfo))
	h, err := events.NewChannel[userEvent]("sensors/no-dlq/data", userEventCodec).
		WithSubscribe(events.Subscribe{}).Handle(b)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	topic, body, ok := h.DeadLetterFor(stats.NoopObserver{}, "sensors/no-dlq/data", []byte("raw"), errors.New("boom"))
	if ok {
		t.Fatalf("want ok=false when no DeadLetter declared, got topic=%q body=%s", topic, body)
	}
}

func TestDeadLetterFor_ReportsObserver_AndReturnsEncodedEnvelope(t *testing.T) {
	b := events.NewClient(events.WithInfo(testInfo))
	h, err := events.NewChannel[userEvent]("sensors/dlq/data", userEventCodec,
		events.DeadLetter("sensors/dlq/dead-letter"),
	).WithSubscribe(events.Subscribe{}).Handle(b)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	topic, body, ok := h.DeadLetterFor(stats.NoopObserver{}, "sensors/dlq/data", []byte(`{"bad":true}`), errors.New("decode failed"))
	if !ok {
		t.Fatal("want ok=true when DeadLetter is declared")
	}
	if topic != "sensors/dlq/dead-letter" {
		t.Errorf("want topic=sensors/dlq/dead-letter, got %q", topic)
	}
	if !strings.Contains(string(body), "decode failed") {
		t.Errorf("want envelope body to contain the error message, got: %s", body)
	}
	if !strings.Contains(string(body), "sensors/dlq/data") {
		t.Errorf("want envelope body to contain the source topic, got: %s", body)
	}
}

func TestDeadLetter_GlobalDefault_Inherited(t *testing.T) {
	b := events.NewClient(events.WithInfo(testInfo)).AddGlobalDeadLetter("global/dead-letter")
	h, err := events.NewChannel[userEvent]("sensors/inherits-global/data", userEventCodec).
		WithSubscribe(events.Subscribe{}).Handle(b)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	topic, _, ok := h.DeadLetterFor(stats.NoopObserver{}, "sensors/inherits-global/data", nil, errors.New("boom"))
	if !ok || topic != "global/dead-letter" {
		t.Fatalf("want inherited global default topic=global/dead-letter ok=true, got topic=%q ok=%v", topic, ok)
	}
}

func TestDeadLetter_OverriddenPerChannel(t *testing.T) {
	b := events.NewClient(events.WithInfo(testInfo)).AddGlobalDeadLetter("global/dead-letter")
	h, err := events.NewChannel[userEvent]("sensors/overrides-global/data", userEventCodec,
		events.DeadLetter("sensors/overrides-global/own-dead-letter"),
	).WithSubscribe(events.Subscribe{}).Handle(b)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	topic, _, ok := h.DeadLetterFor(stats.NoopObserver{}, "sensors/overrides-global/data", nil, errors.New("boom"))
	if !ok || topic != "sensors/overrides-global/own-dead-letter" {
		t.Fatalf("want per-channel override to win, got topic=%q ok=%v", topic, ok)
	}
}

func TestDeadLetter_OptedOut(t *testing.T) {
	b := events.NewClient(events.WithInfo(testInfo)).AddGlobalDeadLetter("global/dead-letter")
	h, err := events.NewChannel[userEvent]("sensors/opts-out/data", userEventCodec,
		events.DeadLetter(""), // explicit opt-out
	).WithSubscribe(events.Subscribe{}).Handle(b)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	_, _, ok := h.DeadLetterFor(stats.NoopObserver{}, "sensors/opts-out/data", nil, errors.New("boom"))
	if ok {
		t.Fatal("want ok=false when explicitly opted out of the global default")
	}
}

// This section tests F1's fix (session review finding): DeadLetter now
// generates its own AsyncAPI channel entry, per the roadmap's Topic 4
// "DECIDED this round" section, previously unimplemented.

func mustAsyncAPISpec(t *testing.T, b *events.Client) string {
	t.Helper()
	doc, err := b.AsyncAPISpec()
	if err != nil {
		t.Fatalf("AsyncAPISpec: %v", err)
	}
	out, err := doc.MarshalYAML()
	if err != nil {
		t.Fatalf("MarshalYAML: %v", err)
	}
	return string(out)
}

func TestDeadLetter_RegistersAsyncAPIChannel(t *testing.T) {
	b := events.NewClient(events.WithInfo(testInfo))
	_, err := events.NewChannel[userEvent]("sensors/dlq-spec/data", userEventCodec,
		events.DeadLetter("sensors/dlq-spec/dead-letter").
			WithDescription("Undeliverable sensor readings.").
			WithSchemaName("SensorDeadLetter"),
	).WithSubscribe(events.Subscribe{}).Handle(b)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	out := mustAsyncAPISpec(t, b)
	if !strings.Contains(out, "sensors/dlq-spec/dead-letter:") {
		t.Errorf("want a dead-letter channel key in spec:\n%s", out)
	}
	if !strings.Contains(out, "address: sensors/dlq-spec/dead-letter") {
		t.Errorf("want the dead-letter channel address in spec:\n%s", out)
	}
	if !strings.Contains(out, "SensorDeadLetter:") {
		t.Errorf("want the dead-letter schema registered under its SchemaName in components:\n%s", out)
	}
	if !strings.Contains(out, "Undeliverable sensor readings.") {
		t.Errorf("want the dead-letter description in spec:\n%s", out)
	}
}

func TestDeadLetter_GlobalDefault_RegistersOneSharedAsyncAPIChannel(t *testing.T) {
	b := events.NewClient(events.WithInfo(testInfo)).AddGlobalDeadLetter("global/dlq-spec")
	_, err := events.NewChannel[userEvent]("sensors/dlq-spec-a/data", userEventCodec).
		WithSubscribe(events.Subscribe{}).Handle(b)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	_, err = events.NewChannel[userEvent]("sensors/dlq-spec-b/data", userEventCodec).
		WithSubscribe(events.Subscribe{}).Handle(b)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	out := mustAsyncAPISpec(t, b)
	if strings.Count(out, "global/dlq-spec:") != 1 {
		t.Errorf("want the shared global dead-letter channel registered EXACTLY once, got spec:\n%s", out)
	}
}

func TestDeadLetter_NoDeclaration_NoAsyncAPIChannel(t *testing.T) {
	b := events.NewClient(events.WithInfo(testInfo))
	_, err := events.NewChannel[userEvent]("sensors/no-dlq-spec/data", userEventCodec).
		WithSubscribe(events.Subscribe{}).Handle(b)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	out := mustAsyncAPISpec(t, b)
	if strings.Contains(out, "dead-letter") || strings.Contains(out, "DeadLetter") {
		t.Errorf("want NO dead-letter channel in spec when none is declared:\n%s", out)
	}
}
