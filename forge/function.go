package forge

import (
	"context"
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

// NewFunction creates a [Function] and computes its contract hash.
// Panics if name or version is empty — these are programming errors detected at
// startup, not runtime conditions.
//
// NewFunction is a free function (not a method) because Go requires type parameters
// to appear on free functions, not on method receivers.
//
// Port names (used for pipeline graph-edge inference) are inferred from the codec's
// Schema.Title (set via [codex.Codec.WithTitle]). For struct codecs the struct field
// names are always used regardless of Title. Scalar codecs default to "input"/"output"
// when no title is set.
//
// For single-value inputs pass any scalar codec directly:
//
//	var oeeCodec   = codex.Float64(zeroToOne()).WithTitle("oee")
//	var gradeCodec = codex.String(gradeEnum).WithTitle("grade")
//
//	gradeCalc := forge.NewFunction("gradeCalc", "1.0.0",
//	    oeeCodec, gradeCodec,
//	    func(oee OEE) (Grade, error) { ... },
//	)
//
// For multi-input computations, define an input struct and a codex.Struct codec.
// Cross-field constraints belong on the struct codec via codex.Refine; use
// WithRefinement for pipeline-level constraints:
//
//	type AvailabilityIn struct {
//	    PlannedTime PlannedTime
//	    Downtime    Downtime
//	}
//	availInCodec := codex.Struct[AvailabilityIn](
//	    codex.RequiredField("plannedTime", ptCodec, ...),
//	    codex.RequiredField("downtime",    dtCodec, ...),
//	)
//	availabilityCodec := codex.Float64(zeroToOne()).WithTitle("availability")
//
//	availCalc := forge.NewFunction("availabilityCalc", "1.0.0",
//	    availInCodec, availabilityCodec,
//	    func(in AvailabilityIn) (Availability, error) { ... },
//	    forge.WithRefinement(func(in AvailabilityIn) error { ... }),
//	    forge.FunctionMeta{Description: "Computes availability as (plannedTime - downtime) / plannedTime."},
//	)
func NewFunction[In, Out any](
	name, version string,
	input codex.Codec[In],
	output codex.Codec[Out],
	apply func(In) (Out, error),
	opts ...FunctionOpt,
) *Function[In, Out] {
	if name == "" {
		panic("forge.NewFunction: name must not be empty")
	}
	if version == "" {
		panic("forge.NewFunction: version must not be empty")
	}
	cfg := applyFunctionOptions(opts)
	inName := input.Schema.Title
	if inName == "" {
		inName = "input"
	}
	inputs := inputSpecs(inName, input.Schema)
	outName := output.Schema.Title
	if outName == "" {
		outName = "output"
	}
	out := PortSpec{Name: outName, Schema: output.Schema}
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
	}
}

// inputSpecs builds the Inputs slice for a FunctionSpec.
// When the codec schema is an object with declared properties (i.e. the input is a
// codex struct codec), each property becomes a separate PortSpec so that the Registry
// can infer graph edges by matching individual field names to producer output names.
// For scalar codecs a single PortSpec is returned using name as the port name.
func inputSpecs(name string, s schema.Schema) []PortSpec {
	if len(s.Properties) > 0 {
		specs := make([]PortSpec, 0, len(s.Properties))
		for _, p := range s.Properties {
			specs = append(specs, PortSpec{Name: p.Name, Schema: p.Schema})
		}
		return specs
	}
	return []PortSpec{{Name: name, Schema: s}}
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
	var err error

	if to, ok := f.observer.(stats.TraceObserver); ok {
		ctx := to.StartSpan(context.Background(), "forge.apply", f.Spec.Name)
		defer func() { to.EndSpan(ctx, err) }()
	}

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
