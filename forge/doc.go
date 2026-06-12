// Package forge provides governed, self-documenting KPI computation functions.
//
// A [Function][In, Out] carries its input and output codecs, a SHA-256 contract
// hash, version string, and optional governance metadata (author, approver,
// approval date) — all in one value. The contract hash changes whenever the
// codec or the compute function changes, making silent drift impossible.
//
// # Defining a function
//
//	var oeeFunction = forge.NewFunction[OEEInput, OEEResult](
//	    "oee", "1.0.0",
//	    oeeInputCodec, oeeResultCodec,
//	    func(ctx context.Context, in OEEInput) (OEEResult, error) {
//	        return OEEResult{OEE: in.Availability * in.Performance * in.Quality}, nil
//	    },
//	    forge.FunctionMeta{
//	        Description: "Overall Equipment Effectiveness (ISO 22400-2).",
//	        Author:      "engineering@example.com",
//	        ApprovedBy:  "quality-board",
//	        ApprovedAt:  "2024-01-15",
//	    },
//	)
//
// # Validation sequence
//
// When [Function.Apply] is called:
//
//  1. Input codec decodes and validates → [InputError] on failure
//  2. Optional cross-input refinement runs → [RefinementError] on failure
//  3. User function executes → [ApplyError] on failure
//  4. Output codec validates → [OutputError] on failure
//
// # Composing functions
//
// [Compose] chains two functions type-safely:
//
//	var pipeline = forge.Compose(rawDataFn, oeeFn)
//	// type-checked: Out of rawDataFn must match In of oeeFn
//
// # Registering in a pipeline
//
//	registry := forge.NewRegistry("OEE Pipeline", "1.0.0").
//	    WithAuthor("engineering@example.com").
//	    WithApproval("quality-board", "2024-01-15").
//	    WithObserver(obs)
//
//	registry.Register(oeeFunction)
//	spec := registry.PipelineSpec()
//	// render with render/pipeline to produce a YAML governance document
//
// # Collection operations
//
// Lift a function over a slice or map:
//
//	batchOEE  := forge.Map(oeeFunction)    // []OEEInput → []OEEResult
//	alerts    := forge.Filter(alertFn)     // []Reading → []Reading
//	total     := forge.Reduce(sumFn)       // ([]Reading, float64) → float64
//
// # Further reading
//
//   - [render/pipeline] — renders a [PipelineSpec] to YAML
//   - [codex] — codec primitives used for input/output contracts
package forge
