package stream_test

import (
	"testing"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/forge"
	gstream "github.com/DaniDeer/go-codex/stream"
)

var topoFn = forge.NewFunction("oeeCalc", "1.0.0",
	codex.Float64().WithTitle("oee"),
	codex.Float64().WithTitle("grade"),
	func(v float64) (float64, error) { return v * 100, nil },
)

func TestTopology_Steps(t *testing.T) {
	topo := gstream.NewTopology("Pipeline", "1.0.0").
		WithSource("mqtt/sensors", "Sensor source").
		WithFilter("value > 0").
		WithTap("dashboard event").
		WithBuffer("batch 10 / 500ms").
		WithDebounce("30s silence").
		WithThrottle("1 per second").
		WithSink("mqtt/out", "Output sink")

	spec := topo.Spec()
	if len(spec.Steps) != 7 {
		t.Fatalf("want 7 steps, got %d", len(spec.Steps))
	}
	kinds := []gstream.StepKind{
		gstream.StepKindSource, gstream.StepKindFilter, gstream.StepKindTap,
		gstream.StepKindBuffer, gstream.StepKindDebounce, gstream.StepKindThrottle,
		gstream.StepKindSink,
	}
	for i, want := range kinds {
		if spec.Steps[i].Kind != want {
			t.Errorf("step %d: want kind %q, got %q", i, want, spec.Steps[i].Kind)
		}
	}
}

func TestTopology_WithApply_CapturesFunctionSpec(t *testing.T) {
	topo := gstream.NewTopology("P", "1.0.0")
	gstream.WithApply(topo, topoFn)
	spec := topo.Spec()
	if len(spec.Steps) != 1 {
		t.Fatalf("want 1 step, got %d", len(spec.Steps))
	}
	s := spec.Steps[0]
	if s.Kind != gstream.StepKindApply {
		t.Errorf("kind: want apply, got %q", s.Kind)
	}
	if s.Name != "oeeCalc" {
		t.Errorf("name: want %q, got %q", "oeeCalc", s.Name)
	}
	if s.Function == nil {
		t.Fatal("Function field must not be nil for apply steps")
	}
	if s.Function.Hash == "" {
		t.Error("Function.Hash must be captured from forge.FunctionSpec")
	}
}

func TestTopology_Info(t *testing.T) {
	spec := gstream.NewTopology("My Pipeline", "2.1.0").
		WithDescription("Real-time OEE pipeline.").
		Spec()
	if spec.Info.Title != "My Pipeline" {
		t.Errorf("Title: want %q, got %q", "My Pipeline", spec.Info.Title)
	}
	if spec.Info.Version != "2.1.0" {
		t.Errorf("Version: want %q, got %q", "2.1.0", spec.Info.Version)
	}
	if spec.Info.Description == "" {
		t.Error("Description should be set")
	}
}
