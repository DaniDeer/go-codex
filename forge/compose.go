package forge

// Compose chains two Functions: the output of f1 becomes the input of f2.
//
// The resulting Function validates in through f1's input codec, runs f1 (including any
// refinement on f1), then feeds the result into f2 (including any refinement on f2).
// The composed FunctionSpec records its own contract hash from name, version, and the
// outer input/output shapes.
//
// Pass WithRefinement to add a pipeline-level cross-input constraint on the composed
// function's input (type A). It runs after f1's input codec validation and before f1.
//
// Compose panics if name or version is empty — these are programming errors.
//
//	celsius2kelvin := forge.Compose("c2k", "1.0.0", celsius2centi, centi2kelvin)
func Compose[A, B, Out any](
	name, version string,
	f1 *Function[A, B],
	f2 *Function[B, Out],
	opts ...FunctionOption,
) *Function[A, Out] {
	if name == "" {
		panic("forge.Compose: name must not be empty")
	}
	if version == "" {
		panic("forge.Compose: version must not be empty")
	}
	cfg := applyFunctionOptions(opts)
	inputs := f1.Spec.Inputs
	out := f2.Spec.Output
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
	composed := func(a A) (Out, error) {
		mid, err := f1.Apply(a)
		if err != nil {
			return *new(Out), err
		}
		return f2.Apply(mid)
	}
	var refinement func(A) error
	if cfg.refinement != nil {
		r := cfg.refinement
		refinement = func(a A) error { return r(a) }
	}
	return &Function[A, Out]{
		Spec:       spec,
		inputCodec: f1.inputCodec,
		output:     f2.output,
		apply:      composed,
		refinement: refinement,
	}
}
