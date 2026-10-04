package mqtt

import (
	"errors"
	"log/slog"
	"testing"
	"time"
)

// docs/design/d-0007-declarative-middleware-layering.md's Rollout Phase B
// follow-up: regression tests for ConnectError's new ReturnCode field and
// ConnectOptions.Observer's RecordSecurityRejection integration — mirrors
// adapters/mqtt5/connect_reasoncode_test.go's structure. No live broker
// involved (mirrors this package's established "no real network
// dependency in tests" precedent, see connect_test.go) — these exercise
// the new logic directly.

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

func TestConnectError_Error_IncludesReturnCodeWhenKnown(t *testing.T) {
	err := ConnectError{Op: "connect", Err: errBoom, ReturnCode: 4}
	got := err.Error()
	want := "mqtt connect connect: boom (return code 4: Bad username or password)"
	if got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestConnectError_Error_OmitsReturnCodeWhenZero(t *testing.T) {
	err := ConnectError{Op: "connect", Err: errBoom}
	got := err.Error()
	want := "mqtt connect connect: boom"
	if got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestConnectError_Unwrap(t *testing.T) {
	err := ConnectError{Op: "connect", Err: errBoom}
	if !errors.Is(err, errBoom) {
		t.Errorf("errors.Is(err, errBoom) = false, want true")
	}
}

func TestConnectError_LogValue_IncludesReturnCodeWhenNonZero(t *testing.T) {
	err := ConnectError{Op: "connect", Err: errBoom, ReturnCode: 5}
	m := attrMap(t, err.LogValue())
	if m["op"] != "connect" {
		t.Errorf("op = %v, want connect", m["op"])
	}
	if m["return_code"] != int64(5) {
		t.Errorf("return_code = %v, want 5", m["return_code"])
	}
}

func TestConnectError_LogValue_OmitsReturnCodeWhenZero(t *testing.T) {
	err := ConnectError{Op: "connect", Err: errBoom}
	m := attrMap(t, err.LogValue())
	if _, ok := m["return_code"]; ok {
		t.Errorf("return_code present, want absent for zero value")
	}
}

func TestRecordConnectSecurityRejection_CalledOnAuthRejectionCodes(t *testing.T) {
	for _, code := range []byte{4, 5} {
		obs := &recordingSecurityObserver{}
		recordConnectSecurityRejection(ConnectOptions{Observer: obs}, code)
		if obs.calls != 1 {
			t.Errorf("code %d: RecordSecurityRejection called %d times, want 1", code, obs.calls)
		}
	}
}

func TestRecordConnectSecurityRejection_NotCalledOnOtherCodes(t *testing.T) {
	for _, code := range []byte{0, 1, 2, 3} {
		obs := &recordingSecurityObserver{}
		recordConnectSecurityRejection(ConnectOptions{Observer: obs}, code)
		if obs.calls != 0 {
			t.Errorf("code %d: RecordSecurityRejection called %d times, want 0", code, obs.calls)
		}
	}
}

func TestRecordConnectSecurityRejection_NilObserver_NoPanic(t *testing.T) {
	recordConnectSecurityRejection(ConnectOptions{}, 4)
}

func TestRecordConnectSecurityRejection_PlainObserver_GracefulNoop(t *testing.T) {
	recordConnectSecurityRejection(ConnectOptions{Observer: plainObserver{}}, 4)
}

// recordingSecurityObserver implements [stats.SecurityObserver] (plus the
// base [stats.Observer] methods) and counts RecordSecurityRejection calls.
type recordingSecurityObserver struct {
	calls int
}

func (o *recordingSecurityObserver) RecordRequest(string, string, int, time.Duration) {}
func (o *recordingSecurityObserver) RecordSubscribe(string, bool, time.Duration)      {}
func (o *recordingSecurityObserver) RecordPublish(string, bool, time.Duration)        {}
func (o *recordingSecurityObserver) RecordValidationError(string, string, string)     {}
func (o *recordingSecurityObserver) RecordSecurityRejection(boundary, reason string) {
	o.calls++
}
