// Package forge provides signed, governed, self-documenting KPI computation functions
// for the go-codex library.
//
// Forge adds a third composable layer on top of validated domain models (codex) and
// API builders (api/rest, api/events):
//
//	Layer 1: Validated domain models — codex.Codec[T] with Refine constraints.
//	Layer 2: API endpoints        — api/rest or api/events builders.
//	Layer 3: KPI pipelines        — forge.Function[In,Out] with governance + computation graph.
//
// The layers are independent. Use any subset; they compose naturally.
//
// # Measured[T]: boundary provenance
//
// Within a computation pipeline, values are naked validated types (e.g. Availability,
// Performance). When a value crosses a system boundary (REST request, MQTT message,
// database read), wrap it with Measured[T] to carry governance provenance:
//
//	codec := forge.MeasuredCodec(availabilityCodec)
//	m := forge.Measured[Availability]{
//	    Source: "sensor-ot-1", Version: "2.0", Author: "OT Team",
//	    Value:  av,
//	}
//	encoded, _ := codec.Encode(m)  // wire form includes source/version/author
//
// # Functions: signed, validated computations
//
// Function[In, Out] wraps a Go computation with a validated input and output, plus
// governance metadata (Author, ApprovedBy, ApprovedAt) and a tamper-evident hash
// of the computation contract (name + version + input/output schemas).
//
// For single-value inputs, In is a plain domain type. Port names for pipeline
// graph-edge inference come from codec.Schema.Title (set via .WithTitle):
//
//	var oeeCodec   = codex.Float64(zeroToOne()).WithTitle("oee")
//	var gradeCodec = codex.String(gradeEnum).WithTitle("grade")
//
//	gradeCalc := forge.NewFunction("gradeCalc", "1.0.0",
//	    oeeCodec, gradeCodec,
//	    func(oee OEE) (Grade, error) { ... },
//	)
//
// For multi-input computations, define a struct and build a codex.Struct codec.
// Struct field names are used as input port names automatically (Schema.Title on the
// struct codec is not needed for port naming). Cross-field constraints belong on the
// struct codec via codex.Refine; use WithRefinement for pipeline-level constraints:
//
//	type OEEIn struct {
//	    Availability Availability
//	    Performance  Performance
//	    Quality      Quality
//	}
//	oeeInCodec := codex.Struct[OEEIn](
//	    codex.RequiredField("availability", availabilityCodec, ...),
//	    codex.RequiredField("performance",  performanceCodec, ...),
//	    codex.RequiredField("quality",      qualityCodec, ...),
//	)
//	var oeeCodec = codex.Float64(zeroToOne()).WithTitle("oee")
//
//	oeeCalc := forge.NewFunction("oeeCalc", "1.0.0",
//	    oeeInCodec, oeeCodec,
//	    func(in OEEIn) (OEE, error) {
//	        return OEE(float64(in.Availability) * float64(in.Performance) * float64(in.Quality)), nil
//	    },
//	    forge.FunctionMeta{Description: "Computes OEE as availability × performance × quality.", Author: "OT Engineering", ApprovedBy: "Quality Manager", ApprovedAt: "2024-03-01"},
//	)
//	result, err := oeeCalc.Apply(OEEIn{Availability: av, Performance: pe, Quality: qu})
//
// # Registry + spec: machine-readable computation graph
//
// Register functions and call Spec() to produce a PipelineSpec that
// render/pipeline serialises as YAML — a machine-readable, git-committable
// audit trail of the entire KPI computation graph:
//
//	reg := forge.NewRegistry("OEE Pipeline", "1.0.0").
//	    WithDescription("Signed, governed OEE computation pipeline.")
//	oeeCalc.Register(reg)
//	yamlBytes, _ := pipeline.Render(reg.Spec())
package forge

import (
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/schema"
	"github.com/DaniDeer/go-codex/stats"
	"github.com/DaniDeer/go-codex/validate"
)

// Measured[T] wraps a validated value with its governance provenance.
//
// Use Measured[T] when a validated value must carry its data lineage across a
// system boundary (REST API, MQTT message, database row). Within a computation
// pipeline, pass the unwrapped Value field directly to Function*.Apply — no wrapper
// overhead at the computation layer.
type Measured[T any] struct {
	Source  string `json:"source"`
	Version string `json:"version"`
	Author  string `json:"author"`
	Value   T      `json:"value"`
}

// MeasuredCodec wraps an existing Codec[T] and produces a Codec[Measured[T]].
//
// The encoded form carries "source", "version", "author", and "value" fields.
// Source, Version, and Author are validated as non-empty strings on both encode
// and decode. The inner codec's constraints apply to Value on both encode and decode.
func MeasuredCodec[T any](inner codex.Codec[T]) codex.Codec[Measured[T]] {
	nonEmpty := codex.String().Refine(validate.NonEmptyString)
	return codex.Struct[Measured[T]](
		codex.RequiredField("source", nonEmpty,
			func(m Measured[T]) string { return m.Source },
			func(m *Measured[T], v string) { m.Source = v },
		),
		codex.RequiredField("version", nonEmpty,
			func(m Measured[T]) string { return m.Version },
			func(m *Measured[T], v string) { m.Version = v },
		),
		codex.RequiredField("author", nonEmpty,
			func(m Measured[T]) string { return m.Author },
			func(m *Measured[T], v string) { m.Author = v },
		),
		codex.RequiredField("value", inner,
			func(m Measured[T]) T { return m.Value },
			func(m *Measured[T], v T) { m.Value = v },
		),
	)
}

// PortSpec describes one named input or output port of a function.
// Schema is the codec's schema for this value.
type PortSpec struct {
	Name   string        `json:"name"`
	Schema schema.Schema `json:"schema"`
}

// FunctionKind identifies how a Function was constructed.
type FunctionKind string

const (
	// FunctionKindScalar is the default: a scalar function created by NewFunction or Compose.
	FunctionKindScalar FunctionKind = "scalar"
	// FunctionKindMap is a function created by Map (lifts a Function over a slice).
	FunctionKindMap FunctionKind = "map"
	// FunctionKindFilter is a function created by Filter (keeps elements satisfying a predicate).
	FunctionKindFilter FunctionKind = "filter"
	// FunctionKindReduce is a function created by Reduce (folds a slice to an accumulator).
	FunctionKindReduce FunctionKind = "reduce"
	// FunctionKindMapValues is a function created by MapValues or MapValuesK.
	FunctionKindMapValues FunctionKind = "mapValues"
)

// FunctionSpec is the type-erased, schema-level descriptor of a function.
//
// All fields are set automatically by NewFunction, Compose, and the collection constructors
// (Map, Filter, Reduce, MapValues).
// Hash is computed over (Name, Version, Inputs[].Schema, Output.Schema); governance
// fields (Author, ApprovedBy, ApprovedAt) are excluded from the hash — changing
// who approved a function does not alter what it computes.
type FunctionSpec struct {
	// Core identity — always set.
	Name    string
	Version string
	// Hash is "sha256:<hex>" over the canonical JSON of the computation contract.
	Hash string
	// Kind identifies the constructor that produced this function.
	// FunctionKindScalar ("") for scalar functions created by NewFunction or Compose.
	// FunctionKindMap/Filter/Reduce/MapValues for collection functions.
	Kind FunctionKind
	// Wraps is the Name of the scalar Function lifted by Map or MapValues.
	// Empty for scalar functions and for Filter/Reduce (which take raw predicates).
	Wraps string
	// Governance metadata — set via [FunctionMeta].
	Description string
	Author      string
	ApprovedBy  string
	ApprovedAt  string // ISO 8601 date, e.g. "2024-01-15"
	// Input/output shapes.
	Inputs []PortSpec
	Output PortSpec
}

// GraphEdge links a consuming function's named input to the function that produces it.
// ProducedBy is empty when the input is a direct measurement with no registered producer.
type GraphEdge struct {
	Function   string // name of the consuming function
	Input      string // input name within that function
	ProducedBy string // name of the producing function, or "" for direct measurements
}

// PipelineInfo is pipeline-level metadata.
type PipelineInfo struct {
	Title       string
	Version     string
	Description string
}

// PipelineSpec is the full machine-readable computation graph spec.
// Use render/pipeline.Render to serialise it as YAML.
type PipelineSpec struct {
	Info      PipelineInfo
	Functions []FunctionSpec
	Graph     []GraphEdge
}

// Registry holds registered functions and produces a PipelineSpec.
//
// Graph edges are inferred automatically: when function A's output name matches
// function B's input name, the registry records that B depends on A.
//
// Build a registry with NewRegistry and chain optional configuration via
// WithDescription and WithObserver before registering any functions:
//
//	reg := forge.NewRegistry("OEE Pipeline", "1.0.0").
//	    WithDescription("Signed, governed OEE computation pipeline.").
//	    WithObserver(myObs)
//	availabilityCalc.Register(reg)
//	oeeCalc.Register(reg)
type Registry struct {
	info      PipelineInfo
	functions []FunctionSpec
	observer  stats.PipelineObserver
}

// NewRegistry returns a new Registry with the given pipeline title and version.
// Chain WithDescription and WithObserver to add optional configuration.
func NewRegistry(title, version string) *Registry {
	return &Registry{
		info:     PipelineInfo{Title: title, Version: version},
		observer: stats.NoopObserver{},
	}
}

// WithDescription sets the pipeline-level description and returns r for chaining.
func (r *Registry) WithDescription(desc string) *Registry {
	r.info.Description = desc
	return r
}

// WithObserver sets the PipelineObserver injected into every function that
// registers itself with this registry. Returns r for chaining.
func (r *Registry) WithObserver(obs stats.PipelineObserver) *Registry {
	if obs != nil {
		r.observer = obs
	}
	return r
}

// add registers a FunctionSpec. Called by Function*.Register; not part of the
// public Registry API so that only typed function values can register themselves.
func (r *Registry) add(spec FunctionSpec) {
	r.functions = append(r.functions, spec)
}

// Spec builds and returns the PipelineSpec, including inferred graph edges.
//
// Graph edges are inferred by matching each function's output name against the
// input names of all other functions. If multiple registered functions share the
// same output name, all matches are recorded as separate edges.
func (r *Registry) Spec() PipelineSpec {
	var edges []GraphEdge
	for _, consumer := range r.functions {
		for _, input := range consumer.Inputs {
			for _, producer := range r.functions {
				if producer.Name == consumer.Name {
					continue
				}
				if producer.Output.Name == input.Name {
					edges = append(edges, GraphEdge{
						Function:   consumer.Name,
						Input:      input.Name,
						ProducedBy: producer.Name,
					})
				}
			}
		}
	}
	return PipelineSpec{
		Info:      r.info,
		Functions: r.functions,
		Graph:     edges,
	}
}
