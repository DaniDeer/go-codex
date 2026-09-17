package reqreply

import "testing"

// This file proves ErrorPatternAs/HandleErrorPattern/Case are GENUINELY
// transport-independent — they touch ONLY the ErrorPatternValuer
// interface, never any concrete adapter type. localErrorPatternValue is
// a minimal, package-local test type (NOT mqtt5.ErrorPatternResponse or
// zeromq.ErrorPatternResponse) proving these functions work against ANY
// value implementing ErrorPatternValuer, with zero adapter dependency at
// all — see adapters/mqtt5/error_pattern_client_test.go and
// adapters/zeromq/error_pattern_client_test.go for the complementary
// real-round-trip regression coverage proving each transport's own
// ErrorPatternResponse is still recognized correctly after the move.

type errPayload struct {
	Code string
}

type localErrorPatternValue struct {
	value any
}

func (e localErrorPatternValue) Error() string          { return "local error pattern value" }
func (e localErrorPatternValue) ErrorPatternValue() any { return e.value }

func TestErrorPatternAs_MatchedValue_ExtractsTypedPayload(t *testing.T) {
	err := localErrorPatternValue{value: errPayload{Code: "conflict"}}
	payload, ok := ErrorPatternAs[errPayload](err)
	if !ok {
		t.Fatalf("want ErrorPatternAs to match, got ok=false for err=%v", err)
	}
	if payload.Code != "conflict" {
		t.Errorf("code = %q, want conflict", payload.Code)
	}
}

func TestErrorPatternAs_WrongType_ReturnsFalse(t *testing.T) {
	err := localErrorPatternValue{value: errPayload{Code: "conflict"}}
	type unrelatedType struct{ X int }
	_, ok := ErrorPatternAs[unrelatedType](err)
	if ok {
		t.Error("want ErrorPatternAs to return false for a type mismatch")
	}
}

func TestErrorPatternAs_NilError_ReturnsFalse(t *testing.T) {
	_, ok := ErrorPatternAs[errPayload](nil)
	if ok {
		t.Error("want ErrorPatternAs to return false for a nil error")
	}
}

func TestHandleErrorPattern_DispatchesFirstMatchingCase(t *testing.T) {
	err := localErrorPatternValue{value: errPayload{Code: "conflict"}}
	var gotCode string
	handled := HandleErrorPattern(err,
		Case(func(p errPayload) { gotCode = p.Code }),
	)
	if !handled {
		t.Fatal("want HandleErrorPattern to report handled=true")
	}
	if gotCode != "conflict" {
		t.Errorf("gotCode = %q, want conflict", gotCode)
	}
}

func TestHandleErrorPattern_FirstMatchWins(t *testing.T) {
	err := localErrorPatternValue{value: errPayload{Code: "conflict"}}
	var called []string
	handled := HandleErrorPattern(err,
		Case(func(p errPayload) { called = append(called, "first") }),
		Case(func(p errPayload) { called = append(called, "second") }),
	)
	if !handled {
		t.Fatal("want handled=true")
	}
	if len(called) != 1 || called[0] != "first" {
		t.Errorf("want only the first matching Case to run, got %v", called)
	}
}

func TestHandleErrorPattern_NoCaseMatches_ReturnsFalse(t *testing.T) {
	err := localErrorPatternValue{value: errPayload{Code: "conflict"}}
	type unrelatedType struct{ X int }
	handled := HandleErrorPattern(err,
		Case(func(p unrelatedType) { t.Fatal("must not be called") }),
	)
	if handled {
		t.Error("want handled=false when no Case matches")
	}
}

func TestHandleErrorPattern_NilError_ReturnsFalse(t *testing.T) {
	handled := HandleErrorPattern(nil, Case(func(p errPayload) {}))
	if handled {
		t.Error("want handled=false for a nil error")
	}
}
