package forge

// FunctionOpt configures optional governance metadata or cross-input validation on a forge function.
//
// Pass one or more FunctionOpt values as trailing variadic arguments to
// NewFunction or Compose. The primary ways to supply options are:
//
//   - [FunctionMeta] struct literal — for governance metadata (description, author, approval)
//   - [WithRefinement] — for a typed pipeline-level cross-input constraint
//   - [WithDescription], [WithAuthor], [WithApproval] — convenience wrappers (prefer FunctionMeta)
//
// Governance fields not supplied default to the zero string ("") and are omitted from
// the YAML spec output.
type FunctionOpt interface {
	applyFunctionOption(*functionOptions)
}

// funcOpt is the function-based implementation of FunctionOpt.
type funcOpt func(*functionOptions)

func (f funcOpt) applyFunctionOption(o *functionOptions) { f(o) }

// FunctionMeta holds optional governance metadata for a forge function.
// It implements [FunctionOpt] and can be passed directly to [NewFunction] or [Compose].
//
//	forge.NewFunction("calc", "1.0.0", inCodec, outCodec, fn,
//	    forge.FunctionMeta{
//	        Description: "compute efficiency grade",
//	        Author:      "OT Engineering",
//	        ApprovedBy:  "Quality Manager",
//	        ApprovedAt:  "2024-03-01",
//	    },
//	)
type FunctionMeta struct {
	// Description is a short human-readable explanation of what the function computes.
	Description string
	// Author is the team or person responsible for this function definition.
	Author string
	// ApprovedBy names the approver or approval authority.
	ApprovedBy string
	// ApprovedAt is the ISO 8601 date of approval (e.g. "2024-03-01").
	ApprovedAt string
}

func (m FunctionMeta) applyFunctionOption(o *functionOptions) {
	if m.Description != "" {
		o.description = m.Description
	}
	if m.Author != "" {
		o.author = m.Author
	}
	if m.ApprovedBy != "" {
		o.approvedBy = m.ApprovedBy
	}
	if m.ApprovedAt != "" {
		o.approvedAt = m.ApprovedAt
	}
}

type functionOptions struct {
	description string
	author      string
	approvedBy  string
	approvedAt  string
	// refinement holds a pipeline-level cross-input constraint.
	// WithRefinement wraps the typed func(In) error as func(any) error via type assertion.
	// New extracts and re-wraps it into the typed func(In) error field on Function.
	refinement func(any) error
}

func applyFunctionOptions(opts []FunctionOpt) functionOptions {
	var cfg functionOptions
	for _, o := range opts {
		o.applyFunctionOption(&cfg)
	}
	return cfg
}

// WithDescription sets the human-readable description for a forge function.
// Prefer [FunctionMeta] when setting multiple governance fields at once.
func WithDescription(desc string) FunctionOpt {
	return funcOpt(func(o *functionOptions) { o.description = desc })
}

// WithAuthor sets the author governance field for a forge function.
// Prefer [FunctionMeta] when setting multiple governance fields at once.
func WithAuthor(author string) FunctionOpt {
	return funcOpt(func(o *functionOptions) { o.author = author })
}

// WithApproval sets the approvedBy and approvedAt governance fields.
// approvedAt should be an ISO 8601 date string (e.g. "2024-03-01").
// Prefer [FunctionMeta] when setting multiple governance fields at once.
func WithApproval(approvedBy, approvedAt string) FunctionOpt {
	return funcOpt(func(o *functionOptions) {
		o.approvedBy = approvedBy
		o.approvedAt = approvedAt
	})
}

// WithRefinement adds a pipeline-level cross-input constraint to a function.
//
// The constraint runs after input codec validation and before the compute function.
// Return a non-nil error to reject the input. On failure, Apply returns a RefinementError.
//
// For multi-input functions the constraint receives the assembled input struct, giving
// access to all fields. Cross-field rules can also be expressed directly on the input
// codec via codex.Refine — prefer that approach when the constraint is a property of
// the domain type itself rather than the pipeline definition.
//
//	availCalc := forge.NewFunction("availabilityCalc", "1.0.0",
//	    availInCodec, availabilityCodec,
//	    computeAvailability,
//	    forge.WithRefinement(func(in AvailabilityIn) error {
//	        if float64(in.Downtime) > float64(in.PlannedTime) {
//	            return fmt.Errorf("downtime (%v h) must not exceed plannedTime (%v h)",
//	                in.Downtime, in.PlannedTime)
//	        }
//	        return nil
//	    }),
//	)
func WithRefinement[In any](fn func(In) error) FunctionOpt {
	return funcOpt(func(o *functionOptions) {
		o.refinement = func(v any) error { return fn(v.(In)) }
	})
}
