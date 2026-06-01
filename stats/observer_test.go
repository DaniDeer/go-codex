package stats_test

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/stats"
	"github.com/DaniDeer/go-codex/validate"
)

// capturingObserver records all RecordValidationError calls for assertion.
type capturingObserver struct {
	calls []validationCall
}

type validationCall struct {
	location   string
	constraint string
	field      string
}

func (o *capturingObserver) RecordValidationError(location, constraint, field string) {
	o.calls = append(o.calls, validationCall{location, constraint, field})
}

func assertCalls(t *testing.T, obs *capturingObserver, want []validationCall) {
	t.Helper()
	if len(obs.calls) != len(want) {
		t.Fatalf("RecordValidationError called %d times, want %d; got %v", len(obs.calls), len(want), obs.calls)
	}
	for i, got := range obs.calls {
		w := want[i]
		if got.location != w.location || got.constraint != w.constraint || got.field != w.field {
			t.Errorf("call[%d]: got {%q, %q, %q}, want {%q, %q, %q}",
				i, got.location, got.constraint, got.field,
				w.location, w.constraint, w.field)
		}
	}
}

func TestReportErrors_ValidationErrors(t *testing.T) {
	c := codex.Struct[struct{ Name string }](
		codex.RequiredField("name", codex.String().Refine(validate.NonEmptyString),
			func(v struct{ Name string }) string { return v.Name },
			func(v *struct{ Name string }, s string) { v.Name = s },
		),
	)
	_, err := c.Decode(map[string]any{"name": ""})
	if err == nil {
		t.Fatal("expected error")
	}
	obs := &capturingObserver{}
	stats.ReportErrors(obs, "body", err)
	assertCalls(t, obs, []validationCall{
		{"body", "non-empty", "name"},
	})
}

func TestReportErrors_KeyError(t *testing.T) {
	pattern := regexp.MustCompile(`^[a-z]+-\d+$`)
	keyCodec := codex.String().Refine(validate.Pattern(pattern))
	c := codex.Map[string, int](keyCodec, codex.Int())

	_, err := c.Encode(map[string]int{"INVALID_KEY": 1})
	if err == nil {
		t.Fatal("expected error")
	}

	obs := &capturingObserver{}
	stats.ReportErrors(obs, "payload", err)
	if len(obs.calls) == 0 {
		t.Fatal("RecordValidationError not called for KeyError")
	}
	got := obs.calls[0]
	if got.field != "INVALID_KEY" {
		t.Errorf("want field=INVALID_KEY, got %q", got.field)
	}
	if got.location != "payload" {
		t.Errorf("want location=payload, got %q", got.location)
	}
	if got.constraint == "" {
		t.Error("want non-empty constraint name")
	}
}

func TestReportErrors_KeyError_Decode(t *testing.T) {
	pattern := regexp.MustCompile(`^[a-z]+-\d+$`)
	keyCodec := codex.String().Refine(validate.Pattern(pattern))
	c := codex.Map[string, int](keyCodec, codex.Int())

	_, err := c.Decode(map[string]any{"BAD_KEY": 5})
	if err == nil {
		t.Fatal("expected error")
	}

	obs := &capturingObserver{}
	stats.ReportErrors(obs, "payload", err)
	if len(obs.calls) == 0 {
		t.Fatal("RecordValidationError not called for KeyError on Decode")
	}
	if obs.calls[0].field != "BAD_KEY" {
		t.Errorf("want field=BAD_KEY, got %q", obs.calls[0].field)
	}
}

func TestReportErrors_ElementError(t *testing.T) {
	c := codex.SliceOf(codex.Int().Refine(validate.PositiveInt))

	_, err := c.Decode([]any{1, -5, 3})
	if err == nil {
		t.Fatal("expected error")
	}

	obs := &capturingObserver{}
	stats.ReportErrors(obs, "items", err)
	if len(obs.calls) == 0 {
		t.Fatal("RecordValidationError not called for ElementError")
	}
	got := obs.calls[0]
	if got.field != "[1]" {
		t.Errorf("want field=[1], got %q", got.field)
	}
	if got.location != "items" {
		t.Errorf("want location=items, got %q", got.location)
	}
}

func TestReportErrors_Nil(t *testing.T) {
	obs := &capturingObserver{}
	stats.ReportErrors(obs, "loc", nil)
	if len(obs.calls) != 0 {
		t.Errorf("expected no calls for nil error, got %v", obs.calls)
	}
}

func TestReportErrors_UnknownError(t *testing.T) {
	obs := &capturingObserver{}
	stats.ReportErrors(obs, "loc", fmt.Errorf("some opaque error"))
	if len(obs.calls) != 0 {
		t.Errorf("expected no calls for opaque error, got %v", obs.calls)
	}
}

func TestReportErrors_NestedKeyError_ThroughUnwrap(t *testing.T) {
	// Simulate forge.InputError wrapping a KeyError (the forge-collection Scenario 5 path).
	pattern := regexp.MustCompile(`^[a-z]+-\d+$`)
	keyCodec := codex.String().Refine(validate.Pattern(pattern))
	mc := codex.Map[string, int](keyCodec, codex.Int())
	_, innerErr := mc.Encode(map[string]int{"BAD": 1})
	if innerErr == nil {
		t.Fatal("expected inner error")
	}
	// Wrap like forge.InputError would.
	wrapped := fmt.Errorf("outer wrapper: %w", innerErr)
	obs := &capturingObserver{}
	stats.ReportErrors(obs, "sensors", wrapped)
	if len(obs.calls) == 0 {
		t.Fatal("RecordValidationError not called for nested KeyError through Unwrap")
	}
	if obs.calls[0].field != "BAD" {
		t.Errorf("want field=BAD, got %q", obs.calls[0].field)
	}
}

func TestConstraintName_KeyError(t *testing.T) {
	pattern := regexp.MustCompile(`^[a-z]+-\d+$`)
	keyCodec := codex.String().Refine(validate.Pattern(pattern))
	mc := codex.Map[string, int](keyCodec, codex.Int())
	_, err := mc.Encode(map[string]int{"BAD": 1})
	if err == nil {
		t.Fatal("expected error")
	}
	var ke codex.KeyError
	if !isKeyError(err, &ke) {
		t.Fatalf("expected KeyError, got %T", err)
	}
	name := stats.ConstraintName(ke.Err)
	if name == "" {
		t.Error("ConstraintName should return non-empty for ConstraintError inside KeyError")
	}
}

// isKeyError uses errors.As to check for KeyError.
func isKeyError(err error, target *codex.KeyError) bool {
	if ke, ok := err.(codex.KeyError); ok {
		*target = ke
		return true
	}
	return false
}
