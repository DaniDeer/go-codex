package nethttp

import (
	"net/http"
	"testing"
)

// TestNewConsumer_WithBaseURL_ReturnsNewInstance is N1: NewConsumer +
// Consumer.WithBaseURL return a distinct *Consumer, never the same
// pointer — mirrors TestCaller_WithBaseURL_ReturnsNewInstance exactly.
// The fuller "does not mutate, reaches the new host, shares the client"
// proof lives alongside Consume's own tests in binding_test.go (mirrors
// TestCall_WithRebasedCaller_ReachesNewHost), since Consumer alone has no
// observable behavior without a Consume call to exercise it.
func TestNewConsumer_WithBaseURL_ReturnsNewInstance(t *testing.T) {
	base := NewConsumer(http.DefaultClient, "http://base.example")
	rebased := base.WithBaseURL("http://rebased.example")
	if rebased == base {
		t.Fatal("WithBaseURL must return a DISTINCT *Consumer, not the same pointer")
	}
}

func TestConsumer_WithBaseURL_ChainedReturnsDistinctInstance(t *testing.T) {
	base := NewConsumer(http.DefaultClient, "http://base.example")
	rebased := base.WithBaseURL("http://rebased.example")
	rerebased := rebased.WithBaseURL("http://another.example")
	if rerebased == nil || rerebased == rebased {
		t.Fatal("WithBaseURL chained off a rebased Consumer must return a distinct, non-nil *Consumer")
	}
}
