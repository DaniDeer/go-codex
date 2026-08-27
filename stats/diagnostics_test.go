package stats_test

import (
	"context"
	"testing"

	"github.com/DaniDeer/go-codex/stats"
)

func TestDiagnostics_PreallocateAppendReadBack(t *testing.T) {
	ctx := stats.WithDiagnostics(context.Background())

	stats.RecordDiagnostic(ctx, stats.Diagnostic{Location: "query", ConstraintName: "required", Field: "id"})
	stats.RecordDiagnostic(ctx, stats.Diagnostic{Location: "body", ConstraintName: "email", Field: "email"})

	got := stats.DiagnosticsFromContext(ctx)
	if len(got) != 2 {
		t.Fatalf("want 2 diagnostics, got %d", len(got))
	}
	if got[0].Location != "query" || got[0].Field != "id" {
		t.Errorf("unexpected first diagnostic: %+v", got[0])
	}
	if got[1].Location != "body" || got[1].ConstraintName != "email" {
		t.Errorf("unexpected second diagnostic: %+v", got[1])
	}
}

func TestDiagnostics_VisibleFromDerivedContext(t *testing.T) {
	ctx := stats.WithDiagnostics(context.Background())

	// Simulate a nested Fn deriving its own child context, still recording
	// into the SAME underlying sink (the mutable-box trick).
	type unrelatedKey struct{}
	child := context.WithValue(ctx, unrelatedKey{}, "unrelated")
	stats.RecordDiagnostic(child, stats.Diagnostic{Location: "header", ConstraintName: "type-mismatch", Field: "X-Foo"})

	got := stats.DiagnosticsFromContext(ctx)
	if len(got) != 1 {
		t.Fatalf("want 1 diagnostic visible from the parent ctx, got %d", len(got))
	}
	if got[0].Field != "X-Foo" {
		t.Errorf("unexpected diagnostic: %+v", got[0])
	}
}

func TestDiagnostics_NoOpWhenAbsent(t *testing.T) {
	ctx := context.Background()

	// RecordDiagnostic on an undecorated ctx must not panic.
	stats.RecordDiagnostic(ctx, stats.Diagnostic{Location: "query"})

	got := stats.DiagnosticsFromContext(ctx)
	if got != nil {
		t.Errorf("want nil diagnostics when never decorated, got %v", got)
	}
}
