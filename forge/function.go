package forge

import (
	"errors"
	"time"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/schema"
	"github.com/DaniDeer/go-codex/stats"
)

// passOrWrapApply returns err unchanged when it is already a typed forge error
// (InputError, OutputError, ApplyError, or RefinementError), preventing double-wrapping
// when a composed closure returns an error from an inner function's Apply.
// For plain errors (e.g. from user-supplied compute functions), wraps in ApplyError.
func passOrWrapApply(fnName string, err error) error {
	var ie InputError
	var oe OutputError
	var ae ApplyError
	var re RefinementError
	if errors.As(err, &ie) || errors.As(err, &oe) || errors.As(err, &ae) || errors.As(err, &re) {
		return err
	}
	return ApplyError{Function: fnName, Err: err}
}

// Must unwraps a (T, error) pair, panicking if err is non-nil.
//
// Use at program startup where a construction error indicates a programming mistake:
//
// availCalc := forge.Must(forge.New("availabilityCalc", "1.0.0", ...))
func Must[T any](v T, err error) T {
	if err != nil {
		panic("forge: " + err.Error())
	}
	return v
}

// Function is a validated, signed derivation function with a single generic input In
// and output Out.
//
// For single-value inputs (e.g. computing a grade from an OEE value), In is a plain
// domain type. For multi-input computations, define a struct that groups the inputs and
// build its codec with codex.Struct — each field is validated individually by the struct
// codec, and cross-field constraints can be added via codex.Refine on the struct codec
// or via WithRefinement at the forge.New call site.
//
// Apply runs the following sequence:
//
//  1. Validate In with the input codec           → InputError on failure
//  2. Run optional WithRefinement constraint     → RefinementError on failure
//  3. Run the compute function                   → ApplyError on failure
//  4. Validate Out with the output codec         → OutputError on failure
//  5. Notify the observer
type Function[In, Out any] struct {
	// Spec is the schema-level descriptor: governance metadata + contract hash.
	Spec       FunctionSpec
	inputCodec codex.Codec[In]
	output     codex.Codec[Out]
	apply      func(In) (Out, error)
	refinement func(In) error
	observer   stats.PipelineObserver
}

// New creates a Function and computes its contract hash.
// Returns a ConfigError when name or version is empty.
//
// For single-value inputs pass any scalar codec directly:
//
// gradeCalc, _ := forge.New("gradeCalc", "1.0.0",
//
//	"oee", oeeCodec,
//	"grade", gradeCodec,
//	func(oee OEE) (Grade, error) { ... },
//
// )
//
// For multi-input computations, define an input struct and a codex.Struct codec.
// Cross-field constraints belong on the struct codec via codex.Refine; use
// WithRefinement for pipeline-level constraints:
//
//	type AvailabilityIn struct {
//	   PlannedTime PlannedTime
//	   Downtime    Downtime
//	}
//
// availInCodec := codex.Struct[AvailabilityIn](
//
//	codex.RequiredField("plannedTime", ptCodec, ...),
//	codex.RequiredField("downtime",    dtCodec, ...),
//
// )
// availCalc, _ := forge.New("availabilityCalc", "1.0.0",
//
//	"inputs", availInCodec,
//	"availability", availabilityCodec,
//	func(in AvailabilityIn) (Availability, error) { ... },
//	forge.WithRefinement(func(in AvailabilityIn) error { ... }),
//	forge.WithDescription("Computes availability as (plannedTime - downtime) / plannedTime."),
//
// )
func New[In, Out any](
	name, version string,
	inputName string, input codex.Codec[In],
	outputName string, output codex.Codec[Out],
	apply func(In) (Out, error),
	opts ...FunctionOption,
) (*Function[In, Out], error) {
	if name == "" {
		return nil, ConfigError{Func: "forge.New", Field: "name"}
	}
	if version == "" {
		return nil, ConfigError{Func: "forge.New", Field: "version"}
	}
	cfg := applyFunctionOptions(opts)
	inputs := inputSpecs(inputName, input.Schema)
	out := InputSpec{Name: outputName, Schema: output.Schema}
	spec := FunctionSpec{
		Name:        name,
		Version:     version,
		Hash:        computeHash(name, version, inputs, out),
		Description: cfg.description,
		Author:      cfg.author,
		ApprovedBy:  cfg.approvedBy,
		ApprovedAt:  cfg.approvedAt,
		Inputs:      inputs,
		Output:      out,
	}
	var refinement func(In) error
	if cfg.refinement != nil {
		r := cfg.refinement
		refinement = func(in In) error { return r(in) }
	}
	return &Function[In, Out]{
		Spec:       spec,
		inputCodec: input,
		output:     output,
		apply:      apply,
		refinement: refinement,
	}, nil
}

// inputSpecs builds the Inputs slice for a FunctionSpec.
// When the codec schema is an object with declared properties (i.e. the input is a
// codex struct codec), each property becomes a separate InputSpec so that the Registry
// can infer graph edges by matching individual field names to producer output names.
// For scalar codecs a single InputSpec is returned.
func inputSpecs(name string, s schema.Schema) []InputSpec {
	if len(s.Properties) > 0 {
		specs := make([]InputSpec, 0, len(s.Properties))
		for _, p := range s.Properties {
			specs = append(specs, InputSpec{Name: p.Name, Schema: p.Schema})
		}
		return specs
	}
	return []InputSpec{{Name: name, Schema: s}}
}

func (f *Function[In, Out]) obs() stats.PipelineObserver {
	if f.observer == nil {
		return stats.NoopObserver{}
	}
	return f.observer
}

// Apply validates in, runs the optional cross-input refinement, runs the computation,
// then validates and returns the result.
func (f *Function[In, Out]) Apply(in In) (Out, error) {
	start := time.Now()
	var zero Out
	if err := f.inputCodec.Validate(in); err != nil {
		f.obs().RecordApply(f.Spec.Name, f.Spec.Version, false, time.Since(start))
		return zero, InputError{Function: f.Spec.Name, Input: inputNameFromErr(f.Spec, err), Err: err}
	}
	if f.refinement != nil {
		if err := f.refinement(in); err != nil {
			f.obs().RecordApply(f.Spec.Name, f.Spec.Version, false, time.Since(start))
			return zero, RefinementError{Function: f.Spec.Name, Err: err}
		}
	}
	result, err := f.apply(in)
	if err != nil {
		f.obs().RecordApply(f.Spec.Name, f.Spec.Version, false, time.Since(start))
		return zero, passOrWrapApply(f.Spec.Name, err)
	}
	if err := f.output.Validate(result); err != nil {
		f.obs().RecordApply(f.Spec.Name, f.Spec.Version, false, time.Since(start))
		return zero, OutputError{Function: f.Spec.Name, Output: f.Spec.Output.Name, Err: err}
	}
	f.obs().RecordApply(f.Spec.Name, f.Spec.Version, true, time.Since(start))
	return result, nil
}

// inputNameFromErr determines the best Input label for an InputError.
// For single-input functions, returns the input spec name.
// For struct-input functions, inspects the validation error: if it is a
// codex.ValidationErrors, returns the first failing field name for a more
// precise error message; otherwise falls back to the function name.
func inputNameFromErr(spec FunctionSpec, err error) string {
	if len(spec.Inputs) == 1 {
		return spec.Inputs[0].Name
	}
	var ve codex.ValidationErrors
	if errors.As(err, &ve) && len(ve) > 0 {
		return ve[0].Field
	}
	return spec.Name
}

// Register adds this function to r, injects r's observer, and returns r for chaining.
func (f *Function[In, Out]) Register(r *Registry) *Registry {
	f.observer = r.observer
	r.add(f.Spec)
	return r
}
