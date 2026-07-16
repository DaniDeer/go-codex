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
	// StepKindWindow collects items into fixed-interval time windows.
	StepKindWindow StepKind = "window"
	// StepKindSlidingWindow collects items into overlapping count-based windows.
	StepKindSlidingWindow StepKind = "slidingWindow"
	// StepKindCombineLatest merges the latest values from multiple sources.
	StepKindCombineLatest StepKind = "combineLatest"
	// StepKindZip pairs items from two streams by position.
	StepKindZip StepKind = "zip"
	// StepKindFlatMapSlice expands each item into multiple output items.
	StepKindFlatMapSlice StepKind = "flatMapSlice"
	// StepKindPort is an IO hop through a ports port (e.g. persistence or
	// enrichment via an IOPort, submission to a SinkPort).
	StepKindPort StepKind = "port"
	// StepKindSwitch routes items into static named cases ([Switch]/[SwitchKey]).
	StepKindSwitch StepKind = "switch"
	// StepKindGroupBy splits the stream into dynamic per-key sub-streams ([GroupBy]).
	StepKindGroupBy StepKind = "groupBy"
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
//	    WithFilter("oee < 0.65").
//	    WithSink("mqtt/alerts/oee", "Low-OEE alerts")
//	stream.WithApply(topo, oeeCalcFn) // free function — Go generics cannot add type params to methods
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

// WithMerge records a merge (fan-in) step combining multiple source streams.
func (t *Topology) WithMerge(description string) *Topology {
	t.steps = append(t.steps, TopologyStep{Kind: StepKindMerge, Description: description})
	return t
}

// WithTee records a tee (fan-out) step splitting one stream into two copies.
func (t *Topology) WithTee(description string) *Topology {
	t.steps = append(t.steps, TopologyStep{Kind: StepKindTee, Description: description})
	return t
}

// WithWindow records a window step (fixed-interval tumbling time windows).
func (t *Topology) WithWindow(description string) *Topology {
	t.steps = append(t.steps, TopologyStep{Kind: StepKindWindow, Description: description})
	return t
}

// WithSlidingWindow records a sliding window step (overlapping count-based windows).
func (t *Topology) WithSlidingWindow(description string) *Topology {
	t.steps = append(t.steps, TopologyStep{Kind: StepKindSlidingWindow, Description: description})
	return t
}

// WithCombineLatest records a CombineLatest step (merges latest values from multiple sources).
func (t *Topology) WithCombineLatest(description string) *Topology {
	t.steps = append(t.steps, TopologyStep{Kind: StepKindCombineLatest, Description: description})
	return t
}

// WithZip records a Zip step (pairs items from two streams by position).
func (t *Topology) WithZip(description string) *Topology {
	t.steps = append(t.steps, TopologyStep{Kind: StepKindZip, Description: description})
	return t
}

// WithFlatMapSlice records a FlatMapSlice step (expands each item into multiple items).
func (t *Topology) WithFlatMapSlice(description string) *Topology {
	t.steps = append(t.steps, TopologyStep{Kind: StepKindFlatMapSlice, Description: description})
	return t
}

// WithPort records an IO-port step: an IO hop through a ports port inside
// the pipeline (persistence or enrichment via an IOPort, submission to a
// SinkPort). Name is the port name (e.g. "sql/readings/save"); description
// explains the hop.
func (t *Topology) WithPort(name, description string) *Topology {
	t.steps = append(t.steps, TopologyStep{Kind: StepKindPort, Name: name, Description: description})
	return t
}

// WithSwitch records a static case-routing step ([Switch]/[SwitchKey]) with a
// human-readable description of the cases (e.g. "alert | warning | archive").
func (t *Topology) WithSwitch(description string) *Topology {
	t.steps = append(t.steps, TopologyStep{Kind: StepKindSwitch, Description: description})
	return t
}

// WithGroupBy records a dynamic per-key split step ([GroupBy]) with a
// human-readable description of the key (e.g. "by sensorID").
func (t *Topology) WithGroupBy(description string) *Topology {
	t.steps = append(t.steps, TopologyStep{Kind: StepKindGroupBy, Description: description})
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
