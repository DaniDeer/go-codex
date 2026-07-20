package codex_test

import (
	"errors"
	"fmt"
	"log/slog"
	"testing"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/validate"
)

// ── Domain type shared across tests ─────────────────────────────────────────

type varTestReq struct {
	ID     string
	Amount int
	Note   string
}

var varIDField = codex.RequiredField("id", codex.String().Refine(validate.NonEmptyString),
	func(r varTestReq) string { return r.ID },
	func(r *varTestReq, v string) { r.ID = v })

var varNoteField = codex.OptionalField("note", codex.String(),
	func(r varTestReq) string { return r.Note },
	func(r *varTestReq, v string) { r.Note = v })

var varAmountField = codex.RequiredField("amount",
	codex.MapCodecSafe(codex.String().Refine(validate.PositiveIntString),
		func(s string) int { var n int; fmt.Sscanf(s, "%d", &n); return n },
		func(n int) (string, error) { return fmt.Sprintf("%d", n), nil }),
	func(r varTestReq) int { return r.Amount },
	func(r *varTestReq, v int) { r.Amount = v })

// ── DecodeVars ────────────────────────────────────────────────────────────────

func TestDecodeVars_HappyPath(t *testing.T) {
	var req varTestReq
	err := codex.DecodeVars(&req, map[string]string{"id": "abc-123"}, varIDField)
	if err != nil {
		t.Fatalf("DecodeVars: %v", err)
	}
	if req.ID != "abc-123" {
		t.Errorf("req.ID: want %q, got %q", "abc-123", req.ID)
	}
}

func TestDecodeVars_PartialMerge(t *testing.T) {
	req := varTestReq{Amount: 42, Note: "keep me"}
	err := codex.DecodeVars(&req, map[string]string{"id": "abc-123"}, varIDField)
	if err != nil {
		t.Fatalf("DecodeVars: %v", err)
	}
	if req.ID != "abc-123" {
		t.Errorf("req.ID: want %q, got %q", "abc-123", req.ID)
	}
	if req.Amount != 42 {
		t.Errorf("req.Amount should be untouched: want 42, got %d", req.Amount)
	}
	if req.Note != "keep me" {
		t.Errorf("req.Note should be untouched: want %q, got %q", "keep me", req.Note)
	}
}

func TestDecodeVars_MissingRequired(t *testing.T) {
	var req varTestReq
	err := codex.DecodeVars(&req, map[string]string{}, varIDField)
	if err == nil {
		t.Fatal("expected error for missing required var")
	}
	var ve codex.ValidationErrors
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationErrors, got %T: %v", err, err)
	}
	if len(ve) != 1 || ve[0].Field != "id" {
		t.Fatalf("unexpected ValidationErrors: %+v", ve)
	}
	if !errors.Is(ve[0].Err, codex.ErrMissingField) {
		t.Errorf("expected ErrMissingField, got %v", ve[0].Err)
	}
}

func TestDecodeVars_CodecValidationFailure(t *testing.T) {
	var req varTestReq
	err := codex.DecodeVars(&req, map[string]string{"id": ""}, varIDField)
	if err == nil {
		t.Fatal("expected error for empty string failing NonEmptyString constraint")
	}
	var ve codex.ValidationErrors
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationErrors, got %T: %v", err, err)
	}
}

func TestDecodeVars_OptionalFieldAbsent(t *testing.T) {
	var req varTestReq
	err := codex.DecodeVars(&req, map[string]string{"id": "abc"}, varIDField, varNoteField)
	if err != nil {
		t.Fatalf("DecodeVars: %v", err)
	}
	if req.Note != "" {
		t.Errorf("req.Note should stay zero value when absent: got %q", req.Note)
	}
}

func TestDecodeVars_MultipleFailuresCollected(t *testing.T) {
	var req varTestReq
	err := codex.DecodeVars(&req, map[string]string{"id": ""}, varIDField, varAmountField)
	if err == nil {
		t.Fatal("expected error")
	}
	var ve codex.ValidationErrors
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationErrors, got %T: %v", err, err)
	}
	if len(ve) != 2 {
		t.Fatalf("expected 2 collected failures (id + amount), got %d: %+v", len(ve), ve)
	}
}

// ── EncodeVars ────────────────────────────────────────────────────────────────

func TestEncodeVars_HappyPath(t *testing.T) {
	req := varTestReq{ID: "abc-123", Amount: 7}
	vars, err := codex.EncodeVars(req, varIDField, varAmountField)
	if err != nil {
		t.Fatalf("EncodeVars: %v", err)
	}
	if vars["id"] != "abc-123" {
		t.Errorf("vars[id]: want %q, got %q", "abc-123", vars["id"])
	}
	if vars["amount"] != "7" {
		t.Errorf("vars[amount]: want %q, got %q", "7", vars["amount"])
	}
}

// nonStringField deliberately attaches a non-string-wire codec (codex.Int())
// directly to a var field, to test EncodeVars's type-mismatch error path.
var nonStringField = codex.RequiredField("amount", codex.Int(),
	func(r varTestReq) int { return r.Amount },
	func(r *varTestReq, v int) { r.Amount = v })

func TestEncodeVars_NonStringCodec(t *testing.T) {
	req := varTestReq{Amount: 7}
	_, err := codex.EncodeVars(req, nonStringField)
	if err == nil {
		t.Fatal("expected VarEncodeTypeError")
	}
	var vete codex.VarEncodeTypeError
	if !errors.As(err, &vete) {
		t.Fatalf("expected VarEncodeTypeError, got %T: %v", err, err)
	}
	if vete.Field != "amount" {
		t.Errorf("VarEncodeTypeError.Field: want %q, got %q", "amount", vete.Field)
	}
}

func TestEncodeVars_DecodeVars_RoundTrip(t *testing.T) {
	original := varTestReq{ID: "xyz", Amount: 99}
	vars, err := codex.EncodeVars(original, varIDField, varAmountField)
	if err != nil {
		t.Fatalf("EncodeVars: %v", err)
	}

	var got varTestReq
	if err := codex.DecodeVars(&got, vars, varIDField, varAmountField); err != nil {
		t.Fatalf("DecodeVars: %v", err)
	}
	if got.ID != original.ID || got.Amount != original.Amount {
		t.Errorf("round-trip mismatch: want %+v, got %+v", original, got)
	}
}

func TestVarEncodeTypeError_LogValue(t *testing.T) {
	err := codex.VarEncodeTypeError{Field: "amount", Got: "int"}
	lv := err.LogValue()
	if lv.Kind() != slog.KindGroup {
		t.Fatalf("LogValue: want KindGroup, got %v", lv.Kind())
	}
	attrs := lv.Group()
	keys := make(map[string]bool, len(attrs))
	for _, a := range attrs {
		keys[a.Key] = true
	}
	for _, want := range []string{"field", "got"} {
		if !keys[want] {
			t.Errorf("LogValue missing attribute %q", want)
		}
	}
}

// ── Examples ──────────────────────────────────────────────────────────────────

func ExampleDecodeVars() {
	type GetUserReq struct{ ID string }

	idField := codex.RequiredField("id", codex.String().Refine(validate.UUID),
		func(r GetUserReq) string { return r.ID },
		func(r *GetUserReq, v string) { r.ID = v })

	var req GetUserReq
	vars := map[string]string{"id": "f47ac10b-58cc-4372-a567-0e02b2c3d479"}
	if err := codex.DecodeVars(&req, vars, idField); err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println(req.ID)
	// Output:
	// f47ac10b-58cc-4372-a567-0e02b2c3d479
}

func ExampleEncodeVars() {
	type SensorReading struct{ SensorID string }

	sensorIDField := codex.RequiredField("sensorID", codex.String().Refine(validate.NonEmptyString),
		func(r SensorReading) string { return r.SensorID },
		func(r *SensorReading, v string) { r.SensorID = v })

	reading := SensorReading{SensorID: "sensor-42"}
	vars, err := codex.EncodeVars(reading, sensorIDField)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println(vars["sensorID"])
	// Output:
	// sensor-42
}
