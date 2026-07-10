package stream

import "github.com/DaniDeer/go-codex/forge"

// StepKind identifies the type of a pipeline step.
type StepKind string

const (
	// StepKindSource is a data source (MQTT channel, Go channel, file, etc.).
	StepKindSource StepKind = "source"
	// StepKindApply is a forge.Function applied per-item.
	StepKindApply StepKind = "apply"
	// StepKindFilter retains only items matching a predicate.
	StepKindFilter StepKind = "filter"
	// StepKindTap observes items without transforming them.
	StepKindTap StepKind = "tap"
	// StepKindBuffer batches items by count or time window.
	StepKindBuffer StepKind = "buffer"
	// StepKindDebounce emits only after a silence window.
	StepKindDebounce StepKind = "debounce"
	// StepKindThrottle rate-limits item emission.
	StepKindThrottle StepKind = "throttle"
	// StepKindMerge fan-in from multiple sources.
	StepKindMerge StepKind = "merge"
	// StepKindTee splits a stream into two copies.
	StepKindTee StepKind = "tee"
	// StepKindSink consumes items and errors.
	StepKindSink StepKind = "sink"
)

// TopologyStep describes one step in a stream pipeline.
type TopologyStep struct {
	// Kind identifies the operator type.
	Kind StepKind

	// Name is a human-readable label for this step.
	// For source/sink steps this is typically the channel address or topic.
	// For apply steps it is the forge function name.
	Name string

	// Description is an optional longer human-readable description.
	Description string

	// Function carries governance metadata when Kind == StepKindApply.
	// Nil for all other step kinds.
	Function *forge.FunctionSpec
}

// TopologyInfo is pipeline-level metadata for a stream topology.
type TopologyInfo struct {
	Title       string
	Version     string
	Description string
}

// TopologySpec is the full machine-readable description of a stream pipeline.
// Use [render/stream.Render] to serialise it as YAML.
type TopologySpec struct {
	Info  TopologyInfo
	Steps []TopologyStep
}

// Topology is a declarative builder for a [TopologySpec].
// Chain builder methods to describe the pipeline, then call [Topology.Spec]
// to produce the machine-readable spec.
//
// Usage mirrors [forge.Registry]:
//
//	topo := stream.NewTopology("Sensor OEE Pipeline", "1.0.0").
//	    WithDescription("Real-time OEE from MQTT sensor readings.").
//	    WithSource("mqtt/sensors/+/data", "Decoded sensor readings").
//	    WithApply(oeeCalcFn).
//	    WithFilter("oee < 0.65").
//	    WithSink("mqtt/alerts/oee", "Low-OEE alerts")
//
//	yaml, _ := streamrender.Render(topo.Spec())
type Topology struct {
	info  TopologyInfo
	steps []TopologyStep
}

// NewTopology returns a new Topology with the given title and version.
func NewTopology(title, version string) *Topology {
	return &Topology{info: TopologyInfo{Title: title, Version: version}}
}

// WithDescription sets the pipeline-level description and returns t for chaining.
func (t *Topology) WithDescription(desc string) *Topology {
	t.info.Description = desc
	return t
}

// WithSource records a source step (e.g. an MQTT topic, a file path, a typed channel).
func (t *Topology) WithSource(name, description string) *Topology {
	t.steps = append(t.steps, TopologyStep{Kind: StepKindSource, Name: name, Description: description})
	return t
}

// WithApply records an apply step from a [forge.Function].
// The function's name, version, hash, and description are captured from its [forge.FunctionSpec].
func WithApply[In, Out any](t *Topology, fn *forge.Function[In, Out]) *Topology {
	spec := fn.Spec
	t.steps = append(t.steps, TopologyStep{
		Kind:        StepKindApply,
		Name:        spec.Name,
		Description: spec.Description,
		Function:    &spec,
	})
	return t
}

// WithFilter records a filter step with a human-readable description of the predicate.
func (t *Topology) WithFilter(description string) *Topology {
	t.steps = append(t.steps, TopologyStep{Kind: StepKindFilter, Description: description})
	return t
}

// WithTap records a tap (domain event observer) step.
func (t *Topology) WithTap(description string) *Topology {
	t.steps = append(t.steps, TopologyStep{Kind: StepKindTap, Description: description})
	return t
}

// WithBuffer records a buffer (windowing) step.
func (t *Topology) WithBuffer(description string) *Topology {
	t.steps = append(t.steps, TopologyStep{Kind: StepKindBuffer, Description: description})
	return t
}

// WithDebounce records a debounce step.
func (t *Topology) WithDebounce(description string) *Topology {
	t.steps = append(t.steps, TopologyStep{Kind: StepKindDebounce, Description: description})
	return t
}

// WithThrottle records a throttle step.
func (t *Topology) WithThrottle(description string) *Topology {
	t.steps = append(t.steps, TopologyStep{Kind: StepKindThrottle, Description: description})
	return t
}

// WithSink records a sink step.
func (t *Topology) WithSink(name, description string) *Topology {
	t.steps = append(t.steps, TopologyStep{Kind: StepKindSink, Name: name, Description: description})
	return t
}

// Spec returns the accumulated TopologySpec.
func (t *Topology) Spec() TopologySpec {
	steps := make([]TopologyStep, len(t.steps))
	copy(steps, t.steps)
	return TopologySpec{Info: t.info, Steps: steps}
}
