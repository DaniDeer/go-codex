package middleware

import (
	"context"
	"testing"
)

func TestDisposition_SetAndGet_RoundTrip(t *testing.T) {
	ctx := EnsureDispositionBox(context.Background())
	if got := DispositionFromContext(ctx); got != DispositionDefault {
		t.Errorf("want DispositionDefault before any Set, got %v", got)
	}
	SetDisposition(ctx, DispositionNackDiscard)
	if got := DispositionFromContext(ctx); got != DispositionNackDiscard {
		t.Errorf("want DispositionNackDiscard, got %v", got)
	}
}

func TestSetDisposition_NoopWhenBoxNotPrepared(t *testing.T) {
	ctx := context.Background()
	// Must not panic.
	SetDisposition(ctx, DispositionAck)
	if got := DispositionFromContext(ctx); got != DispositionDefault {
		t.Errorf("want DispositionDefault when box was never prepared, got %v", got)
	}
}

func TestEnsureDispositionBox_Idempotent(t *testing.T) {
	ctx := EnsureDispositionBox(context.Background())
	SetDisposition(ctx, DispositionNackRequeue)
	ctx2 := EnsureDispositionBox(ctx) // should be a no-op, same box
	if got := DispositionFromContext(ctx2); got != DispositionNackRequeue {
		t.Errorf("want the existing box preserved (DispositionNackRequeue), got %v", got)
	}
}

func TestResolveDisposition(t *testing.T) {
	tests := []struct {
		name       string
		signal     Disposition // DispositionDefault means "never call SetDisposition"
		handlerErr error
		want       Disposition
	}{
		{"explicit NackRequeue overrides nil error", DispositionNackRequeue, nil, DispositionNackRequeue},
		{"explicit NackDiscard overrides non-nil error", DispositionNackDiscard, context.Canceled, DispositionNackDiscard},
		{"default with nil error resolves to Ack", DispositionDefault, nil, DispositionAck},
		{"default with non-nil error resolves to NackRequeue", DispositionDefault, context.Canceled, DispositionNackRequeue},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := EnsureDispositionBox(context.Background())
			if tt.signal != DispositionDefault {
				SetDisposition(ctx, tt.signal)
			}
			if got := ResolveDisposition(ctx, tt.handlerErr); got != tt.want {
				t.Errorf("ResolveDisposition() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestResolveDisposition_UnpreparedContext confirms ResolveDisposition
// still resolves sensibly (via the default fallback) even when
// EnsureDispositionBox was never called — an adapter with no
// acknowledgement concept never prepares the box, but shared business
// logic might still call ResolveDisposition defensively.
func TestResolveDisposition_UnpreparedContext(t *testing.T) {
	ctx := context.Background()
	if got := ResolveDisposition(ctx, nil); got != DispositionAck {
		t.Errorf("want DispositionAck, got %v", got)
	}
	if got := ResolveDisposition(ctx, context.Canceled); got != DispositionNackRequeue {
		t.Errorf("want DispositionNackRequeue, got %v", got)
	}
}
