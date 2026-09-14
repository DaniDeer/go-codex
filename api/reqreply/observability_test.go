package reqreply_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/stats"
)

type obsSpy struct {
	stats.NoopObserver
	valErrors []stats.Diagnostic
}

func (s *obsSpy) RecordValidationError(location, constraintName, field string) {
	s.valErrors = append(s.valErrors, stats.Diagnostic{Location: location, ConstraintName: constraintName, Field: field})
}

func TestObservability_HappyPath(t *testing.T) {
	obs := &obsSpy{}
	var gotCtxObs stats.Observer
	next := func(ctx context.Context, req computeReq) (computeResp, error) {
		gotCtxObs = stats.ObserverFromContext(ctx)
		return computeResp{Sum: req.X + req.Y}, nil
	}
	wrapped := reqreply.Observability[computeReq, computeResp](obs)(next)

	resp, err := wrapped(context.Background(), computeReq{X: 3, Y: 4})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Sum != 7 {
		t.Errorf("resp.Sum = %d, want 7", resp.Sum)
	}
	if gotCtxObs != stats.Observer(obs) {
		t.Error("Observability must inject obs into ctx, visible to next via stats.ObserverFromContext")
	}
}

func TestObservability_ErrorPath(t *testing.T) {
	obs := &obsSpy{}
	wantErr := errors.New("handler failed")
	next := func(_ context.Context, _ computeReq) (computeResp, error) {
		return computeResp{}, wantErr
	}
	wrapped := reqreply.Observability[computeReq, computeResp](obs)(next)

	_, err := wrapped(context.Background(), computeReq{X: 1, Y: 2})
	if !errors.Is(err, wantErr) {
		t.Errorf("err = %v, want %v (Observability must pass through next's error unchanged)", err, wantErr)
	}
}

func TestObservability_DrainsDiagnostics(t *testing.T) {
	obs := &obsSpy{}
	next := func(ctx context.Context, _ computeReq) (computeResp, error) {
		stats.RecordDiagnostic(ctx, stats.Diagnostic{Location: "middleware:in", ConstraintName: "required", Field: "tenant_id"})
		return computeResp{}, nil
	}
	wrapped := reqreply.Observability[computeReq, computeResp](obs)(next)

	if _, err := wrapped(context.Background(), computeReq{}); err != nil {
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

func TestObservability_DoesNotCallRecordRequest(t *testing.T) {
	// Observability deliberately does NOT call RecordRequest itself —
	// every reqreply adapter transport already calls it unconditionally
	// on every dispatch path, so doing so again here would double-count.
	obs := &recordRequestSpy{}
	next := func(_ context.Context, _ computeReq) (computeResp, error) {
		return computeResp{}, nil
	}
	wrapped := reqreply.Observability[computeReq, computeResp](obs)(next)

	if _, err := wrapped(context.Background(), computeReq{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if obs.calls != 0 {
		t.Errorf("RecordRequest called %d times, want 0", obs.calls)
	}
}

type recordRequestSpy struct {
	stats.NoopObserver
	calls int
}

func (s *recordRequestSpy) RecordRequest(_, _ string, _ int, _ time.Duration) {
	s.calls++
}
