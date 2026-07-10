package stream_test

import (
	"strings"
	"testing"

	streamrender "github.com/DaniDeer/go-codex/render/stream"
	gstream "github.com/DaniDeer/go-codex/stream"
)

func TestRender_MinimalTopology(t *testing.T) {
	topo := gstream.NewTopology("Test Pipeline", "1.0.0")
	out, err := streamrender.Render(topo.Spec())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	s := string(out)
	for _, want := range []string{"streamTopology:", "Test Pipeline", "1.0.0"} {
		if !strings.Contains(s, want) {
			t.Errorf("output missing %q\nfull:\n%s", want, s)
		}
	}
}

func TestRender_WithSteps(t *testing.T) {
	topo := gstream.NewTopology("Sensor Pipeline", "2.0.0").
		WithSource("mqtt/sensors/+", "Raw MQTT readings").
		WithFilter("value > 0").
		WithSink("mqtt/alerts", "Alert sink")

	out, err := streamrender.Render(topo.Spec())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	s := string(out)
	for _, want := range []string{"source", "filter", "sink", "mqtt/sensors", "mqtt/alerts"} {
		if !strings.Contains(s, want) {
			t.Errorf("output missing %q\nfull:\n%s", want, s)
		}
	}
}

func TestRender_WithDescription(t *testing.T) {
	topo := gstream.NewTopology("My Pipeline", "1.0.0").
		WithDescription("Processes sensor readings in real time.")

	out, err := streamrender.Render(topo.Spec())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(string(out), "Processes sensor") {
		t.Errorf("description not in output:\n%s", out)
	}
}

func ExampleRender() {
	topo := gstream.NewTopology("Sensor OEE Pipeline", "1.0.0").
		WithDescription("Real-time OEE from MQTT sensor readings.").
		WithSource("mqtt/sensors/+/data", "Raw sensor readings").
		WithFilter("value > 0").
		WithWindow("1-minute tumbling window").
		WithSink("mqtt/alerts/oee", "OEE alert publisher")

	yaml, err := streamrender.Render(topo.Spec())
	if err != nil {
		panic(err)
	}
	_ = yaml // yaml contains the stream topology as a YAML document
	// Output:
}

func TestRender_WithPhase3StepKinds(t *testing.T) {
	topo := gstream.NewTopology("Full Pipeline", "1.0.0").
		WithMerge("merge sensor A + B").
		WithTee("tee to archive + alerts").
		WithWindow("1-minute tumbling window").
		WithSlidingWindow("size=10 step=5").
		WithCombineLatest("merge availability + performance").
		WithZip("pair requests with responses").
		WithFlatMapSlice("each reading → multiple metrics")

	out, err := streamrender.Render(topo.Spec())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	s := string(out)
	for _, want := range []string{
		"merge", "tee", "window", "slidingWindow",
		"combineLatest", "zip", "flatMapSlice",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("output missing step kind %q\nfull:\n%s", want, s)
		}
	}
}

func TestTopology_Spec_IsImmutable(t *testing.T) {
	topo := gstream.NewTopology("P", "1.0.0").WithSource("ch", "")
	spec := topo.Spec()
	topo.WithSink("out", "") // add after Spec() call
	if len(spec.Steps) != 1 {
		t.Errorf("Spec() should return a snapshot; got %d steps", len(spec.Steps))
	}
}
