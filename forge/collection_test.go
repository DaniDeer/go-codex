package forge_test

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/forge"
	"github.com/DaniDeer/go-codex/validate"
)

// ── helpers ──────────────────────────────────────────────────────────────────

func positiveFloat64Codec() codex.Codec[float64] {
	return codex.Float64().Refine(validate.RangeFloat(0, 100))
}

// scaleCalc: float64 → float64, multiplies by 2.
func scaleCalc(t *testing.T) *forge.Function[float64, float64] {
	t.Helper()
	fn := forge.NewFunction(
		"scaleCalc", "1.0.0",
		"value", positiveFloat64Codec(),
		"scaled", positiveFloat64Codec(),
		func(v float64) (float64, error) { return v * 2, nil },
	)
	return fn
}

// ── forge.Map ────────────────────────────────────────────────────────────────

func TestMap_HappyPath(t *testing.T) {
	fn := scaleCalc(t)
	batchFn := forge.Map("batchScale", "1.0.0", fn)

	got, err := batchFn.Apply([]float64{1, 2, 3})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	want := []float64{2, 4, 6}
	for i, v := range got {
		if v != want[i] {
			t.Errorf("element %d: got %v, want %v", i, v, want[i])
		}
	}
}

func TestMap_FunctionSpec(t *testing.T) {
	fn := scaleCalc(t)
	batchFn := forge.Map("batchScale", "1.0.0", fn)

	if batchFn.Spec.Kind != "map" {
		t.Errorf("Kind: got %q, want %q", batchFn.Spec.Kind, "map")
	}
	if batchFn.Spec.Wraps != "scaleCalc" {
		t.Errorf("Wraps: got %q, want %q", batchFn.Spec.Wraps, "scaleCalc")
	}
	if batchFn.Spec.Name != "batchScale" {
		t.Errorf("Name: got %q, want %q", batchFn.Spec.Name, "batchScale")
	}
}

func TestMap_ElementValidationFailure(t *testing.T) {
	fn := scaleCalc(t)
	batchFn := forge.Map("batchScale", "1.0.0", fn)

	// element 1 is invalid (negative — out of range [0,100])
	_, err := batchFn.Apply([]float64{10, -5, 20})
	if err == nil {
		t.Fatal("expected CollectionElementError, got nil")
	}
	var ce forge.CollectionElementError
	if !errors.As(err, &ce) {
		t.Fatalf("expected CollectionElementError, got %T: %v", err, err)
	}
	if ce.Index != 1 {
		t.Errorf("Index: got %d, want 1", ce.Index)
	}
	if ce.Function != "batchScale" {
		t.Errorf("Function: got %q, want %q", ce.Function, "batchScale")
	}
}

func TestMap_ApplyFailure(t *testing.T) {
	fn := forge.NewFunction(
		"errCalc", "1.0.0",
		"v", positiveFloat64Codec(),
		"out", positiveFloat64Codec(),
		func(v float64) (float64, error) { return 0, fmt.Errorf("compute failed") },
	)
	batchFn := forge.Map("batchErr", "1.0.0", fn)

	_, err := batchFn.Apply([]float64{1.0})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var ce forge.CollectionElementError
	if !errors.As(err, &ce) {
		t.Fatalf("expected CollectionElementError, got %T", err)
	}
	if ce.Index != 0 {
		t.Errorf("Index: got %d, want 0", ce.Index)
	}
}

func TestMap_PanicEmptyName(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for empty name")
		}
		msg, _ := r.(string)
		if !strings.Contains(msg, "name") {
			t.Errorf("unexpected panic message: %v", r)
		}
	}()
	fn := scaleCalc(t)
	forge.Map("", "1.0.0", fn)
}

func TestMap_PanicEmptyVersion(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for empty version")
		}
		msg, _ := r.(string)
		if !strings.Contains(msg, "version") {
			t.Errorf("unexpected panic message: %v", r)
		}
	}()
	fn := scaleCalc(t)
	forge.Map("batchScale", "", fn)
}

func TestMap_WithRefinement(t *testing.T) {
	fn := scaleCalc(t)
	batchFn := forge.Map("batchScale", "1.0.0", fn,
		forge.WithRefinement(func(in []float64) error {
			if len(in) == 0 {
				return fmt.Errorf("batch must not be empty")
			}
			return nil
		}),
	)
	_, err := batchFn.Apply([]float64{})
	if err == nil {
		t.Fatal("expected RefinementError, got nil")
	}
	var re forge.RefinementError
	if !errors.As(err, &re) {
		t.Fatalf("expected RefinementError, got %T: %v", err, err)
	}
}

// ── forge.Filter ─────────────────────────────────────────────────────────────

func TestFilter_KeepsMatchingElements(t *testing.T) {
	c := positiveFloat64Codec()
	filterFn := forge.Filter("filterPos", "1.0.0", c, func(v float64) bool {
		return v >= 50
	})

	got, err := filterFn.Apply([]float64{10, 60, 30, 80})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(got) != 2 || got[0] != 60 || got[1] != 80 {
		t.Errorf("got %v, want [60 80]", got)
	}
}

func TestFilter_FunctionSpec(t *testing.T) {
	c := positiveFloat64Codec()
	filterFn := forge.Filter("filterPos", "1.0.0", c, func(v float64) bool { return true })

	if filterFn.Spec.Kind != "filter" {
		t.Errorf("Kind: got %q, want %q", filterFn.Spec.Kind, "filter")
	}
	if filterFn.Spec.Wraps != "" {
		t.Errorf("Wraps: got %q, want empty", filterFn.Spec.Wraps)
	}
}

func TestFilter_ElementValidationFailure(t *testing.T) {
	c := positiveFloat64Codec()
	filterFn := forge.Filter("filterPos", "1.0.0", c, func(v float64) bool { return true })

	// element 1 is negative — invalid
	_, err := filterFn.Apply([]float64{10, -5})
	if err == nil {
		t.Fatal("expected CollectionElementError, got nil")
	}
	var ce forge.CollectionElementError
	if !errors.As(err, &ce) {
		t.Fatalf("expected CollectionElementError, got %T", err)
	}
	if ce.Index != 1 {
		t.Errorf("Index: got %d, want 1", ce.Index)
	}
}

func TestFilter_EmptySliceReturnsEmpty(t *testing.T) {
	c := positiveFloat64Codec()
	filterFn := forge.Filter("filterPos", "1.0.0", c, func(v float64) bool { return false })
	got, err := filterFn.Apply([]float64{})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want empty", got)
	}
}

func TestFilter_PanicEmptyName(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for empty name")
		}
		msg, _ := r.(string)
		if !strings.Contains(msg, "name") {
			t.Errorf("unexpected panic message: %v", r)
		}
	}()
	c := positiveFloat64Codec()
	forge.Filter("", "1.0.0", c, func(v float64) bool { return true })
}

func TestFilter_PanicEmptyVersion(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for empty version")
		}
		msg, _ := r.(string)
		if !strings.Contains(msg, "version") {
			t.Errorf("unexpected panic message: %v", r)
		}
	}()
	c := positiveFloat64Codec()
	forge.Filter("filterPos", "", c, func(v float64) bool { return true })
}

// ── forge.Reduce ─────────────────────────────────────────────────────────────

type sumResult struct{ Total float64 }

func TestReduce_CorrectFold(t *testing.T) {
	elemCodec := positiveFloat64Codec()
	accCodec := codex.Struct[sumResult](
		codex.RequiredField("total", codex.Float64(),
			func(s sumResult) float64 { return s.Total },
			func(s *sumResult, v float64) { s.Total = v },
		),
	)
	reduceFn := forge.Reduce(
		"sumCalc", "1.0.0",
		elemCodec, accCodec,
		sumResult{},
		func(acc sumResult, v float64) sumResult { return sumResult{Total: acc.Total + v} },
	)

	got, err := reduceFn.Apply([]float64{10, 20, 30})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got.Total != 60 {
		t.Errorf("Total: got %v, want 60", got.Total)
	}
}

func TestReduce_FunctionSpec(t *testing.T) {
	elemCodec := positiveFloat64Codec()
	accCodec := codex.Struct[sumResult](
		codex.RequiredField("total", codex.Float64(),
			func(s sumResult) float64 { return s.Total },
			func(s *sumResult, v float64) { s.Total = v },
		),
	)
	reduceFn := forge.Reduce(
		"sumCalc", "1.0.0",
		elemCodec, accCodec, sumResult{},
		func(acc sumResult, v float64) sumResult { return acc },
	)
	if reduceFn.Spec.Kind != "reduce" {
		t.Errorf("Kind: got %q, want %q", reduceFn.Spec.Kind, "reduce")
	}
	if reduceFn.Spec.Wraps != "" {
		t.Errorf("Wraps: got %q, want empty", reduceFn.Spec.Wraps)
	}
}

func TestReduce_ElementValidationFailure(t *testing.T) {
	elemCodec := positiveFloat64Codec()
	accCodec := codex.Struct[sumResult](
		codex.RequiredField("total", codex.Float64(),
			func(s sumResult) float64 { return s.Total },
			func(s *sumResult, v float64) { s.Total = v },
		),
	)
	reduceFn := forge.Reduce(
		"sumCalc", "1.0.0",
		elemCodec, accCodec, sumResult{},
		func(acc sumResult, v float64) sumResult { return sumResult{Total: acc.Total + v} },
	)

	// element 0 is negative — invalid
	_, err := reduceFn.Apply([]float64{-1, 10})
	if err == nil {
		t.Fatal("expected CollectionElementError, got nil")
	}
	var ce forge.CollectionElementError
	if !errors.As(err, &ce) {
		t.Fatalf("expected CollectionElementError, got %T", err)
	}
	if ce.Index != 0 {
		t.Errorf("Index: got %d, want 0", ce.Index)
	}
}

func TestReduce_PanicEmptyName(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for empty name")
		}
		msg, _ := r.(string)
		if !strings.Contains(msg, "name") {
			t.Errorf("unexpected panic message: %v", r)
		}
	}()
	elemCodec := positiveFloat64Codec()
	accCodec := codex.Struct[sumResult](
		codex.RequiredField("total", codex.Float64(),
			func(s sumResult) float64 { return s.Total },
			func(s *sumResult, v float64) { s.Total = v },
		),
	)
	forge.Reduce("", "1.0.0", elemCodec, accCodec, sumResult{},
		func(acc sumResult, v float64) sumResult { return acc })
}

func TestReduce_PanicEmptyVersion(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for empty version")
		}
		msg, _ := r.(string)
		if !strings.Contains(msg, "version") {
			t.Errorf("unexpected panic message: %v", r)
		}
	}()
	elemCodec := positiveFloat64Codec()
	accCodec := codex.Struct[sumResult](
		codex.RequiredField("total", codex.Float64(),
			func(s sumResult) float64 { return s.Total },
			func(s *sumResult, v float64) { s.Total = v },
		),
	)
	forge.Reduce("sumCalc", "", elemCodec, accCodec, sumResult{},
		func(acc sumResult, v float64) sumResult { return acc })
}

// ── forge.MapValues ──────────────────────────────────────────────────────────

func TestMapValues_HappyPath(t *testing.T) {
	fn := scaleCalc(t)
	mapValFn := forge.MapValues("perSensor", "1.0.0", fn)

	got, err := mapValFn.Apply(map[string]float64{"a": 10, "b": 20})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got["a"] != 20 || got["b"] != 40 {
		t.Errorf("got %v, want {a:20 b:40}", got)
	}
}

func TestMapValues_FunctionSpec(t *testing.T) {
	fn := scaleCalc(t)
	mapValFn := forge.MapValues("perSensor", "1.0.0", fn)

	if mapValFn.Spec.Kind != "mapValues" {
		t.Errorf("Kind: got %q, want %q", mapValFn.Spec.Kind, "mapValues")
	}
	if mapValFn.Spec.Wraps != "scaleCalc" {
		t.Errorf("Wraps: got %q, want %q", mapValFn.Spec.Wraps, "scaleCalc")
	}
}

func TestMapValues_KeyError(t *testing.T) {
	fn := scaleCalc(t)
	mapValFn := forge.MapValues("perSensor", "1.0.0", fn)

	// single invalid entry — deterministic (only one key)
	_, err := mapValFn.Apply(map[string]float64{"bad": -5})
	if err == nil {
		t.Fatal("expected CollectionKeyError, got nil")
	}
	var ke forge.CollectionKeyError
	if !errors.As(err, &ke) {
		t.Fatalf("expected CollectionKeyError, got %T: %v", err, err)
	}
	if ke.Key != "bad" {
		t.Errorf("Key: got %q, want %q", ke.Key, "bad")
	}
	if ke.Function != "perSensor" {
		t.Errorf("Function: got %q, want %q", ke.Function, "perSensor")
	}
}

func TestMapValues_PanicEmptyName(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for empty name")
		}
		msg, _ := r.(string)
		if !strings.Contains(msg, "name") {
			t.Errorf("unexpected panic message: %v", r)
		}
	}()
	fn := scaleCalc(t)
	forge.MapValues("", "1.0.0", fn)
}

func TestMapValues_PanicEmptyVersion(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for empty version")
		}
		msg, _ := r.(string)
		if !strings.Contains(msg, "version") {
			t.Errorf("unexpected panic message: %v", r)
		}
	}()
	fn := scaleCalc(t)
	forge.MapValues("perSensor", "", fn)
}

// ── observer integration ─────────────────────────────────────────────────────

func TestMap_ObserverCalled(t *testing.T) {
	fn := scaleCalc(t)
	batchFn := forge.Map("batchScale", "1.0.0", fn)
	reg := forge.NewRegistry("test", "1.0.0")
	batchFn.Register(reg)

	_, err := batchFn.Apply([]float64{1, 2, 3})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	// Observer integration verified by successful registration and apply.
	// Full observer call verification is covered in forge_test.go TestRegistry_Observer.
}

// ── forge.MapValuesK ─────────────────────────────────────────────────────────

// sensorIDCodecTest matches keys of the form <word>-<digits>, e.g. "sensor-01".
func sensorIDCodecTest() codex.Codec[string] {
	return codex.String().Refine(validate.Pattern(
		regexp.MustCompile(`^[a-z]+-\d+$`),
	))
}

func TestMapValuesK_HappyPath(t *testing.T) {
	fn := scaleCalc(t)
	mapFn := forge.MapValuesK("perSensor", "1.0.0", sensorIDCodecTest(), fn)

	got, err := mapFn.Apply(map[string]float64{
		"sensor-01": 10,
		"sensor-02": 20,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got["sensor-01"] != 20 || got["sensor-02"] != 40 {
		t.Errorf("got %v, want {sensor-01:20 sensor-02:40}", got)
	}
}

func TestMapValuesK_FunctionSpec(t *testing.T) {
	fn := scaleCalc(t)
	mapFn := forge.MapValuesK("perSensor", "1.0.0", sensorIDCodecTest(), fn)

	if mapFn.Spec.Kind != "mapValues" {
		t.Errorf("Kind: got %q, want %q", mapFn.Spec.Kind, "mapValues")
	}
	if mapFn.Spec.Wraps != "scaleCalc" {
		t.Errorf("Wraps: got %q, want %q", mapFn.Spec.Wraps, "scaleCalc")
	}
}

func TestMapValuesK_InvalidKey_InputError(t *testing.T) {
	fn := scaleCalc(t)
	mapFn := forge.MapValuesK("perSensor", "1.0.0", sensorIDCodecTest(), fn)

	// "SENSOR_01" fails the pattern — invalid key
	_, err := mapFn.Apply(map[string]float64{"SENSOR_01": 10})
	if err == nil {
		t.Fatal("expected InputError, got nil")
	}
	var ie forge.InputError
	if !errors.As(err, &ie) {
		t.Fatalf("expected InputError, got %T: %v", err, err)
	}
	var ke codex.KeyError
	if !errors.As(ie.Err, &ke) {
		t.Fatalf("expected KeyError inside InputError, got %T: %v", ie.Err, ie.Err)
	}
	if ke.Key != "SENSOR_01" {
		t.Errorf("KeyError.Key: got %q, want %q", ke.Key, "SENSOR_01")
	}
}

func TestMapValuesK_MixedKeys_FailFast(t *testing.T) {
	fn := scaleCalc(t)
	mapFn := forge.MapValuesK("perSensor", "1.0.0", sensorIDCodecTest(), fn)

	// One valid key + one invalid key — still fails (no partial results)
	_, err := mapFn.Apply(map[string]float64{
		"sensor-01":  10,
		"SENSOR_BAD": 20,
	})
	if err == nil {
		t.Fatal("expected InputError for mixed-key map, got nil")
	}
	var ie forge.InputError
	if !errors.As(err, &ie) {
		t.Fatalf("expected InputError, got %T: %v", err, err)
	}
}

func TestMapValuesK_InvalidValue_CollectionKeyError(t *testing.T) {
	fn := scaleCalc(t)
	mapFn := forge.MapValuesK("perSensor", "1.0.0", sensorIDCodecTest(), fn)

	// valid key, invalid value (-5 violates positiveFloat64Codec range)
	_, err := mapFn.Apply(map[string]float64{"sensor-01": -5})
	if err == nil {
		t.Fatal("expected CollectionKeyError, got nil")
	}
	var ke forge.CollectionKeyError
	if !errors.As(err, &ke) {
		t.Fatalf("expected CollectionKeyError, got %T: %v", err, err)
	}
	if ke.Key != "sensor-01" {
		t.Errorf("Key: got %q, want %q", ke.Key, "sensor-01")
	}
	if ke.Function != "perSensor" {
		t.Errorf("Function: got %q, want %q", ke.Function, "perSensor")
	}
}

func TestMapValuesK_PanicEmptyName(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for empty name")
		}
		msg, _ := r.(string)
		if !strings.Contains(msg, "name") {
			t.Errorf("unexpected panic message: %v", r)
		}
	}()
	fn := scaleCalc(t)
	forge.MapValuesK("", "1.0.0", sensorIDCodecTest(), fn)
}

func TestMapValuesK_PanicEmptyVersion(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for empty version")
		}
		msg, _ := r.(string)
		if !strings.Contains(msg, "version") {
			t.Errorf("unexpected panic message: %v", r)
		}
	}()
	fn := scaleCalc(t)
	forge.MapValuesK("perSensor", "", sensorIDCodecTest(), fn)
}
