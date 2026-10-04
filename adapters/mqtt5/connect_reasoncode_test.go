package mqtt5

import (
	"errors"
	"log/slog"
	"testing"
	"time"
)

// docs/design/d-0007-declarative-middleware-layering.md's Rollout Phase B:
// regression tests for ConnectError's new ReasonCode/ReasonString fields
// and ConnectOptions.Observer's RecordSecurityRejection integration. No
// live broker involved (mirrors this package's established "no real
// network dependency in tests" precedent, see connect_test.go) — these
// exercise the new logic directly.

var errBoom = errors.New("boom")

// plainObserver implements only [stats.Observer]'s base methods, NOT
// [stats.SecurityObserver] — used to confirm a graceful no-op.
type plainObserver struct{}

func (plainObserver) RecordRequest(string, string, int, time.Duration) {}
func (plainObserver) RecordSubscribe(string, bool, time.Duration)      {}
func (plainObserver) RecordPublish(string, bool, time.Duration)        {}
func (plainObserver) RecordValidationError(string, string, string)     {}

// attrMap flattens a [slog.Value] of Kind Group into a map, keyed by attr
// key, for easy assertion — fails the test if v is not a Group.
func attrMap(t *testing.T, v slog.Value) map[string]any {
	t.Helper()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("LogValue() kind = %v, want %v", v.Kind(), slog.KindGroup)
	}
	m := make(map[string]any)
	for _, a := range v.Group() {
		m[a.Key] = a.Value.Any()
	}
	return m
}

func TestConnectError_Error_IncludesReasonStringWhenSet(t *testing.T) {
	err := ConnectError{Op: "connect", Err: errBoom, ReasonCode: 0x86, ReasonString: "Bad username or password"}
	got := err.Error()
	want := "mqtt5 connect connect: boom (reason code 0x86: Bad username or password)"
	if got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestConnectError_Error_OmitsReasonWhenUnset(t *testing.T) {
	err := ConnectError{Op: "dial", Err: errBoom}
	got := err.Error()
	want := "mqtt5 connect dial: boom"
	if got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestConnectError_LogValue_IncludesReasonFieldsWhenSet(t *testing.T) {
	err := ConnectError{Op: "connect", Err: errBoom, ReasonCode: 0x87, ReasonString: "Not authorized"}
	v := err.LogValue()
	m := attrMap(t, v)
	if m["reason_code"] != int64(0x87) {
		t.Errorf("reason_code = %v, want %d", m["reason_code"], 0x87)
	}
	if m["reason_string"] != "Not authorized" {
		t.Errorf("reason_string = %v, want %q", m["reason_string"], "Not authorized")
	}
}

func TestConnectError_LogValue_OmitsReasonFieldsWhenZero(t *testing.T) {
	err := ConnectError{Op: "dial", Err: errBoom}
	m := attrMap(t, err.LogValue())
	if _, ok := m["reason_code"]; ok {
		t.Error("want no reason_code attr when ReasonCode is zero")
	}
	if _, ok := m["reason_string"]; ok {
		t.Error("want no reason_string attr when ReasonString is empty")
	}
}

func TestRecordConnectSecurityRejection_CallsObserverOnAuthRejectionCode(t *testing.T) {
	obs := &testObserver{}
	recordConnectSecurityRejection(ConnectOptions{Observer: obs}, 0x86)
	if len(obs.secRejections) != 1 || obs.secRejections[0] != "connect" {
		t.Errorf("want one RecordSecurityRejection(\"connect\", ...) call, got %v", obs.secRejections)
	}
}

func TestRecordConnectSecurityRejection_SkipsBelowThreshold(t *testing.T) {
	obs := &testObserver{}
	recordConnectSecurityRejection(ConnectOptions{Observer: obs}, 0x00) // success code, should never reach here in practice
	if len(obs.secRejections) != 0 {
		t.Errorf("want no RecordSecurityRejection call for a non-failure reason code, got %v", obs.secRejections)
	}
}

func TestRecordConnectSecurityRejection_NilObserver_NoPanic(t *testing.T) {
	recordConnectSecurityRejection(ConnectOptions{}, 0x86) // Observer is nil — must not panic
}

func TestRecordConnectSecurityRejection_PlainObserver_NoPanic(t *testing.T) {
	// A plain stats.Observer NOT implementing SecurityObserver must be a
	// graceful no-op, never a panic.
	recordConnectSecurityRejection(ConnectOptions{Observer: plainObserver{}}, 0x86)
}
