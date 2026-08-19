package forge_test

import (
	"errors"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/forge"
	"github.com/DaniDeer/go-codex/stats"
	"github.com/DaniDeer/go-codex/validate"
)

type forgePatchBase struct {
	Name  string
	Value int
}

var forgePatchBaseCodec = codex.Struct[forgePatchBase](
	codex.RequiredField("name", codex.String().Refine(validate.NonEmptyString),
		func(b forgePatchBase) string { return b.Name },
		func(b *forgePatchBase, v string) { b.Name = v }),
	codex.RequiredField("value", codex.Int(),
		func(b forgePatchBase) int { return b.Value },
		func(b *forgePatchBase, v int) { b.Value = v }),
)

type forgePatchDelta struct {
	Value *int
}

var forgePatchDeltaCodec = codex.PartialStruct[forgePatchDelta](
	codex.PartialField("value", codex.Int(),
		func(p forgePatchDelta) *int { return p.Value },
		func(p *forgePatchDelta, v *int) { p.Value = v }),
)

func TestPatch_AppliesPatchOntoBase(t *testing.T) {
	fn := forge.Patch("applyDelta", "1.0.0", forgePatchBaseCodec, forgePatchDeltaCodec)
	v := 99
	got, err := fn.Apply(forge.PatchInput[forgePatchBase, forgePatchDelta]{
		Base:  forgePatchBase{Name: "widget", Value: 1},
		Patch: forgePatchDelta{Value: &v},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got.Value != 99 {
		t.Errorf("Value = %d, want 99", got.Value)
	}
	if got.Name != "widget" {
		t.Errorf("Name = %q, want unchanged widget", got.Name)
	}
}

func TestPatch_KindIsPatch(t *testing.T) {
	fn := forge.Patch("applyDelta", "1.0.0", forgePatchBaseCodec, forgePatchDeltaCodec)
	if fn.Spec.Kind != forge.FunctionKindPatch {
		t.Errorf("Kind = %q, want %q", fn.Spec.Kind, forge.FunctionKindPatch)
	}
}

func TestPatch_TwoNamedInputPorts(t *testing.T) {
	fn := forge.Patch("applyDelta", "1.0.0", forgePatchBaseCodec, forgePatchDeltaCodec)
	names := map[string]bool{}
	for _, in := range fn.Spec.Inputs {
		names[in.Name] = true
	}
	if !names["base"] || !names["patch"] {
		t.Errorf("Inputs = %+v, want exactly base/patch ports present", fn.Spec.Inputs)
	}
}

func TestPatch_InputValidationError_Base(t *testing.T) {
	fn := forge.Patch("applyDelta", "1.0.0", forgePatchBaseCodec, forgePatchDeltaCodec)
	v := 1
	_, err := fn.Apply(forge.PatchInput[forgePatchBase, forgePatchDelta]{
		Base:  forgePatchBase{Name: "", Value: 1}, // invalid: empty name
		Patch: forgePatchDelta{Value: &v},
	})
	var ie forge.InputError
	if !errors.As(err, &ie) {
		t.Fatalf("expected InputError, got %v", err)
	}
}

func TestPatch_InputValidationError_Patch(t *testing.T) {
	// Use a patch codec that rejects the zero value to exercise the patch side.
	rejectingDeltaCodec := codex.PartialStruct[forgePatchDelta](
		codex.PartialField("value", codex.Int().Refine(validate.RangeInt(0, 10)),
			func(p forgePatchDelta) *int { return p.Value },
			func(p *forgePatchDelta, v *int) { p.Value = v }),
	)
	fn := forge.Patch("applyDelta", "1.0.0", forgePatchBaseCodec, rejectingDeltaCodec)
	bad := 999
	_, err := fn.Apply(forge.PatchInput[forgePatchBase, forgePatchDelta]{
		Base:  forgePatchBase{Name: "widget", Value: 1},
		Patch: forgePatchDelta{Value: &bad},
	})
	var ie forge.InputError
	if !errors.As(err, &ie) {
		t.Fatalf("expected InputError, got %v", err)
	}
}

type patchApplyCall struct {
	name    string
	success bool
}

type patchRecordingObserver struct {
	stats.NoopObserver
	calls []patchApplyCall
}

func (r *patchRecordingObserver) RecordApply(name, version string, success bool, dur time.Duration) {
	r.calls = append(r.calls, patchApplyCall{name, success})
}

var _ stats.PipelineObserver = (*patchRecordingObserver)(nil)

func TestPatch_ParticipatesInPipelineObserver(t *testing.T) {
	obs := &patchRecordingObserver{}
	fn := forge.Patch("applyDelta", "1.0.0", forgePatchBaseCodec, forgePatchDeltaCodec)
	reg := forge.NewRegistry("P", "1").WithObserver(obs)
	fn.Register(reg)

	v := 5
	_, err := fn.Apply(forge.PatchInput[forgePatchBase, forgePatchDelta]{
		Base:  forgePatchBase{Name: "widget", Value: 1},
		Patch: forgePatchDelta{Value: &v},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(obs.calls) != 1 {
		t.Fatalf("expected 1 RecordApply call, got %d", len(obs.calls))
	}
	if !obs.calls[0].success {
		t.Error("expected success=true")
	}
}

func ExamplePatch() {
	fn := forge.Patch("applyDelta", "1.0.0", forgePatchBaseCodec, forgePatchDeltaCodec)
	v := 42
	got, err := fn.Apply(forge.PatchInput[forgePatchBase, forgePatchDelta]{
		Base:  forgePatchBase{Name: "widget", Value: 1},
		Patch: forgePatchDelta{Value: &v},
	})
	if err != nil {
		panic(err)
	}
	_ = got
}
