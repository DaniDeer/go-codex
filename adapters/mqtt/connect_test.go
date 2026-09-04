package mqtt

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestConnect_UnreachableBroker_ReturnsConnectError exercises [Connect]
// against a safe, CI-runnable "definitely unreachable" address
// ("tcp://127.0.0.1:1" — port 1 is a reserved/privileged port nothing
// listens on in any CI sandbox) rather than requiring a real MQTT broker,
// following this package's existing precedent of avoiding real-network
// dependencies in tests (see adapter_test.go/caller_test.go's mockClient
// fixtures — no test in this package dials a real broker), mirroring
// [mqtt5]'s own TestConnect_UnreachableBroker_ReturnsConnectError.
func TestConnect_UnreachableBroker_ReturnsConnectError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := Connect(ctx, "tcp://127.0.0.1:1", ConnectOptions{ClientID: "test-client"})
	if err == nil {
		t.Fatal("want error connecting to unreachable broker, got nil")
	}
	if client != nil {
		t.Fatalf("want nil client on error, got %v", client)
	}

	var connErr ConnectError
	if !errors.As(err, &connErr) {
		t.Fatalf("want ConnectError, got %T: %v", err, err)
	}
	if connErr.Op != "connect" {
		t.Errorf("Op = %q, want %q", connErr.Op, "connect")
	}
}
