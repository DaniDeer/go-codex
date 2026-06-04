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

// --- helpers ----------------------------------------------------------------

func float64Codec(min, max float64) codex.Codec[float64] {
	return codex.Float64().Refine(validate.RangeFloat(min, max))
}

func identity(v float64) (float64, error) { return v, nil }

// --- MeasuredCodec ----------------------------------------------------------

func TestMeasuredCodec_RoundTrip(t *testing.T) {
	inner := float64Codec(0, 1)
	mc := forge.MeasuredCodec(inner)

	m := forge.Measured[float64]{Source: "s", Version: "1.0", Author: "a", Value: 0.5}
	enc, err := mc.Encode(m)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := mc.Decode(enc)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got.Source != "s" || got.Version != "1.0" || got.Author != "a" || got.Value != 0.5 {
		t.Errorf("round-trip mismatch: %+v", got)
	}
}

func TestMeasuredCodec_EmptySourceError(t *testing.T) {
	mc := forge.MeasuredCodec(float64Codec(0, 1))
	_, err := mc.Decode(map[string]any{
		"source": "", "version": "1.0", "author": "a", "value": 0.5,
	})
	if err == nil {
		t.Fatal("expected error for empty source, got nil")
	}
}

func TestMeasuredCodec_ValueConstraintError(t *testing.T) {
	mc := forge.MeasuredCodec(float64Codec(0, 1))
	_, err := mc.Decode(map[string]any{
		"source": "s", "version": "1.0", "author": "a", "value": 2.0,
	})
	if err == nil {
		t.Fatal("expected error for out-of-range value, got nil")
	}
}

// --- Registry ---------------------------------------------------------------

func TestRegistry_FluentBuilder(t *testing.T) {
	reg := forge.NewRegistry("Test Pipeline", "1.0.0").
		WithDescription("desc")
	spec := reg.Spec()
	if spec.Info.Title != "Test Pipeline" {
		t.Errorf("title: got %q", spec.Info.Title)
	}
	if spec.Info.Version != "1.0.0" {
		t.Errorf("version: got %q", spec.Info.Version)
	}
	if spec.Info.Description != "desc" {
		t.Errorf("description: got %q", spec.Info.Description)
	}
}

func TestRegistry_GraphEdgeInference(t *testing.T) {
	c := float64Codec(0, 100)
	cIn := c.WithTitle("in")
	cMid := c.WithTitle("mid")
	cOut := c.WithTitle("out")
	f1 := forge.NewFunction("step1", "1.0.0", cIn, cMid, identity)
	f2 := forge.NewFunction("step2", "1.0.0", cMid, cOut, identity)

	reg := forge.NewRegistry("P", "1")
	f1.Register(reg)
	f2.Register(reg)

	spec := reg.Spec()
	if len(spec.Graph) == 0 {
		t.Fatal("expected at least one graph edge")
	}
	var found bool
	for _, e := range spec.Graph {
		if e.Function == "step2" && e.Input == "mid" && e.ProducedBy == "step1" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected edge step2.mid←step1, got %+v", spec.Graph)
	}
}

func TestRegistry_WithObserver(t *testing.T) {
	obs := &recordingObserver{}
	c := float64Codec(0, 1)
	f := forge.NewFunction("fn", "1.0.0", c, c, identity)

	reg := forge.NewRegistry("P", "1").WithObserver(obs)
	f.Register(reg)
	f.Apply(0.5)

	if len(obs.calls) != 1 {
		t.Fatalf("expected 1 RecordApply call, got %d", len(obs.calls))
	}
	if !obs.calls[0].success {
		t.Error("expected success=true")
	}
}

// --- FunctionOption ---------------------------------------------------------

func TestFunctionOption_FieldsApplied(t *testing.T) {
	c := float64Codec(0, 1)
	f := forge.NewFunction("fn", "1.0.0",
		c, c, identity,
		forge.WithDescription("desc"),
		forge.WithAuthor("author"),
		forge.WithApproval("approver", "2024-01-01"),
	)
	if f.Spec.Description != "desc" {
		t.Errorf("Description: got %q", f.Spec.Description)
	}
	if f.Spec.Author != "author" {
		t.Errorf("Author: got %q", f.Spec.Author)
	}
	if f.Spec.ApprovedBy != "approver" {
		t.Errorf("ApprovedBy: got %q", f.Spec.ApprovedBy)
	}
	if f.Spec.ApprovedAt != "2024-01-01" {
		t.Errorf("ApprovedAt: got %q", f.Spec.ApprovedAt)
	}
}

func TestFunctionOption_GovernanceExcludedFromHash(t *testing.T) {
	c := float64Codec(0, 1)
	noGov := forge.NewFunction("fn", "1.0.0", c, c, identity)
	withGov := forge.NewFunction("fn", "1.0.0", c, c, identity,
		forge.WithAuthor("x"), forge.WithApproval("y", "2024-01-01"),
	)
	if noGov.Spec.Hash != withGov.Spec.Hash {
		t.Errorf("governance opts changed hash: %q vs %q", noGov.Spec.Hash, withGov.Spec.Hash)
	}
}

// --- New panics on invalid config -------------------------------------------

func TestNew_ReturnsValue(t *testing.T) {
	c := float64Codec(0, 1)
	f := forge.NewFunction("fn", "1.0.0", c, c, identity)
	if f.Spec.Name != "fn" {
		t.Errorf("name: got %q", f.Spec.Name)
	}
}

func TestNew_PanicsOnEmptyName(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic, got none")
		}
	}()
	c := float64Codec(0, 1)
	forge.NewFunction("", "1.0.0", c, c, identity)
}

// --- recordingObserver ------------------------------------------------------

type applyCall struct {
	name, version string
	success       bool
	duration      time.Duration
}

type recordingObserver struct {
	calls []applyCall
}

func (r *recordingObserver) RecordApply(name, version string, success bool, dur time.Duration) {
	r.calls = append(r.calls, applyCall{name, version, success, dur})
}

var _ stats.PipelineObserver = (*recordingObserver)(nil)

func TestNoopObserver_SatisfiesAllInterfaces(t *testing.T) {
	var _ stats.ValidationObserver = stats.NoopObserver{}
	var _ stats.Observer = stats.NoopObserver{}
	var _ stats.PipelineObserver = stats.NoopObserver{}
	// just a compile-time assertion — if it compiles, the test passes
	_ = errors.New("compile-time check only")
}
