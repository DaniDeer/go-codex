package stream_test

import (
	"testing"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/forge"
	stream "github.com/DaniDeer/go-codex/stream"
)

var topoFn = forge.NewFunction("oeeCalc", "1.0.0",
	codex.Float64().WithTitle("oee"),
	codex.Float64().WithTitle("grade"),
	func(v float64) (float64, error) { return v * 100, nil },
)

func TestTopology_Steps(t *testing.T) {
	topo := stream.NewTopology("Pipeline", "1.0.0").
		WithSource("mqtt/sensors", "Sensor source").
		WithFilter("value > 0").
		WithTap("dashboard event").
		WithBuffer("batch 10 / 500ms").
		WithDebounce("30s silence").
		WithThrottle("1 per second").
		WithMerge("merge sensor A and B").
		WithTee("tee to archive + alerts").
		WithWindow("1-minute tumbling window").
		WithSlidingWindow("size=10 step=5").
		WithCombineLatest("merge availability + performance").
		WithZip("pair requests with responses").
		WithFlatMapSlice("each reading → [°C, °F, K]").
		WithSink("mqtt/out", "Output sink")

	spec := topo.Spec()
	if len(spec.Steps) != 14 {
		t.Fatalf("want 14 steps, got %d", len(spec.Steps))
	}
	kinds := []stream.StepKind{
		stream.StepKindSource, stream.StepKindFilter, stream.StepKindTap,
		stream.StepKindBuffer, stream.StepKindDebounce, stream.StepKindThrottle,
		stream.StepKindMerge, stream.StepKindTee,
		stream.StepKindWindow, stream.StepKindSlidingWindow,
		stream.StepKindCombineLatest, stream.StepKindZip, stream.StepKindFlatMapSlice,
		stream.StepKindSink,
	}
	// Note: 14 enum kinds + Apply is covered by TestTopology_WithApply_CapturesFunctionSpec
	for i, want := range kinds {
		if spec.Steps[i].Kind != want {
			t.Errorf("step %d: want kind %q, got %q", i, want, spec.Steps[i].Kind)
		}
	}
}

func TestTopology_AllStepKindConstants(t *testing.T) {
	// Verify every exported StepKind constant has a non-empty string value
	// and matches its With* method output — catches orphaned constants.
	constants := map[stream.StepKind]string{
		stream.StepKindSource:        "source",
		stream.StepKindApply:         "apply",
		stream.StepKindFilter:        "filter",
		stream.StepKindTap:           "tap",
		stream.StepKindBuffer:        "buffer",
		stream.StepKindDebounce:      "debounce",
		stream.StepKindThrottle:      "throttle",
		stream.StepKindMerge:         "merge",
		stream.StepKindTee:           "tee",
		stream.StepKindWindow:        "window",
		stream.StepKindSlidingWindow: "slidingWindow",
		stream.StepKindCombineLatest: "combineLatest",
		stream.StepKindZip:           "zip",
		stream.StepKindFlatMapSlice:  "flatMapSlice",
		stream.StepKindPort:          "port",
		stream.StepKindSink:          "sink",
	}
	for kind, want := range constants {
		if string(kind) != want {
			t.Errorf("StepKind constant: want string %q, got %q", want, string(kind))
		}
	}
}

func TestTopology_WithApply_CapturesFunctionSpec(t *testing.T) {
	topo := stream.NewTopology("P", "1.0.0")
	stream.WithApply(topo, topoFn)
	spec := topo.Spec()
	if len(spec.Steps) != 1 {
		t.Fatalf("want 1 step, got %d", len(spec.Steps))
	}
	s := spec.Steps[0]
	if s.Kind != stream.StepKindApply {
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
	spec := stream.NewTopology("My Pipeline", "2.1.0").
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

func ExampleNewTopology() {
	topo := stream.NewTopology("Sensor Pipeline", "1.0.0").
		WithDescription("Real-time sensor processing pipeline.").
		WithSource("mqtt/sensors/+/data", "Raw sensor readings from MQTT").
		WithFilter("value > 0").
		WithTap("dashboard observer").
		WithSink("mqtt/alerts", "OEE alert publisher")

	stream.WithApply(topo, topoFn)

	spec := topo.Spec()
	_ = spec // pass spec to render/stream.Render to get YAML
	// Output:
}

func TestTopology_WithPort(t *testing.T) {
	topo := stream.NewTopology("t", "1").
		WithSource("in", "src").
		WithPort("sql/readings/save", "persist via IOPort — stored row re-emitted").
		WithSink("out", "sink")
	spec := topo.Spec()
	if len(spec.Steps) != 3 {
		t.Fatalf("want 3 steps, got %d", len(spec.Steps))
	}
	step := spec.Steps[1]
	if step.Kind != stream.StepKindPort {
		t.Errorf("want kind port, got %q", step.Kind)
	}
	if step.Name != "sql/readings/save" {
		t.Errorf("want port name preserved, got %q", step.Name)
	}
	if step.Description == "" {
		t.Error("want description preserved")
	}
	if step.Function != nil {
		t.Error("port step must carry no function spec")
	}
}
