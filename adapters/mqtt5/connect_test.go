package mqtt5

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestConnect_UnreachableBroker_ReturnsConnectError exercises [Connect]
// against a safe, CI-runnable "definitely unreachable" address ("127.0.0.1:1"
// — port 1 is a reserved/privileged port nothing listens on in any CI
// sandbox) rather than requiring a real MQTT broker, following this
// package's existing precedent of avoiding real-network dependencies in
// tests (see adapter_test.go/caller_test.go's mockClient/mockRouter
// fixtures — no test in this package dials a real broker).
func TestConnect_UnreachableBroker_ReturnsConnectError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client, router, err := Connect(ctx, "127.0.0.1:1", ConnectOptions{ClientID: "test-client"})
	if err == nil {
		t.Fatal("want error connecting to unreachable broker, got nil")
	}
	if client != nil || router != nil {
		t.Fatalf("want nil client/router on error, got client=%v router=%v", client, router)
	}

	var connErr ConnectError
	if !errors.As(err, &connErr) {
		t.Fatalf("want ConnectError, got %T: %v", err, err)
	}
	if connErr.Op != "dial" {
		t.Errorf("Op = %q, want %q", connErr.Op, "dial")
	}
}
