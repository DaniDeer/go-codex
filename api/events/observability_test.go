package events_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/stats"
)

type eventsObsSpy struct {
	stats.NoopObserver
	valErrors []stats.Diagnostic
}

func (s *eventsObsSpy) RecordValidationError(location, constraintName, field string) {
	s.valErrors = append(s.valErrors, stats.Diagnostic{Location: location, ConstraintName: constraintName, Field: field})
}

func TestObservability_HappyPath(t *testing.T) {
	obs := &eventsObsSpy{}
	var gotCtxObs stats.Observer
	next := func(ctx context.Context, msg userEvent) error {
		gotCtxObs = stats.ObserverFromContext(ctx)
		return nil
	}
	wrapped := events.Observability[userEvent](obs)(next)

	if err := wrapped(context.Background(), userEvent{ID: "1", Name: "alice"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotCtxObs != stats.Observer(obs) {
		t.Error("Observability must inject obs into ctx, visible to next via stats.ObserverFromContext")
	}
}

func TestObservability_ErrorPath(t *testing.T) {
	obs := &eventsObsSpy{}
	wantErr := errors.New("handler failed")
	next := func(_ context.Context, _ userEvent) error {
		return wantErr
	}
	wrapped := events.Observability[userEvent](obs)(next)

	err := wrapped(context.Background(), userEvent{ID: "1", Name: "alice"})
	if !errors.Is(err, wantErr) {
		t.Errorf("err = %v, want %v (Observability must pass through next's error unchanged)", err, wantErr)
	}
}

func TestObservability_DrainsDiagnostics(t *testing.T) {
	obs := &eventsObsSpy{}
	next := func(ctx context.Context, _ userEvent) error {
		stats.RecordDiagnostic(ctx, stats.Diagnostic{Location: "middleware:in", ConstraintName: "required", Field: "tenant_id"})
		return nil
	}
	wrapped := events.Observability[userEvent](obs)(next)

	if err := wrapped(context.Background(), userEvent{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(obs.valErrors) != 1 {
		t.Fatalf("want 1 RecordValidationError call, got %d", len(obs.valErrors))
	}
	got := obs.valErrors[0]
	if got.Location != "middleware:in" || got.ConstraintName != "required" || got.Field != "tenant_id" {
		t.Errorf("unexpected diagnostic forwarded: %+v", got)
	}
}

func TestObservability_DoesNotCallRecordSubscribeOrPublish(t *testing.T) {
	// Observability deliberately does NOT call RecordSubscribe/RecordPublish
	// itself — those lifecycle events are recorded elsewhere (adapter
	// dispatch directly, or an adapter-specific wrapper delegating to this
	// function), so doing so again here would double-count.
	obs := &eventsRecordSpy{}
	next := func(_ context.Context, _ userEvent) error {
		return nil
	}
	wrapped := events.Observability[userEvent](obs)(next)

	if err := wrapped(context.Background(), userEvent{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if obs.subscribeCalls != 0 {
		t.Errorf("RecordSubscribe called %d times, want 0", obs.subscribeCalls)
	}
	if obs.publishCalls != 0 {
		t.Errorf("RecordPublish called %d times, want 0", obs.publishCalls)
	}
}

type eventsRecordSpy struct {
	stats.NoopObserver
	subscribeCalls int
	publishCalls   int
}

func (s *eventsRecordSpy) RecordSubscribe(_ string, _ bool, _ time.Duration) {
	s.subscribeCalls++
}

func (s *eventsRecordSpy) RecordPublish(_ string, _ bool, _ time.Duration) {
	s.publishCalls++
}
