package forge

// FunctionOption configures optional governance metadata or cross-input validation on a forge function.
//
// Pass one or more FunctionOption values as trailing variadic arguments to
// New or Compose. Governance fields not supplied default to the zero string ("")
// and are omitted from the YAML spec output.
type FunctionOption func(*functionOptions)

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

func applyFunctionOptions(opts []FunctionOption) functionOptions {
	var cfg functionOptions
	for _, o := range opts {
		o(&cfg)
	}
	return cfg
}

// WithDescription sets the human-readable description for a forge function.
func WithDescription(desc string) FunctionOption {
	return func(o *functionOptions) { o.description = desc }
}

// WithAuthor sets the author governance field for a forge function.
func WithAuthor(author string) FunctionOption {
	return func(o *functionOptions) { o.author = author }
}

// WithApproval sets the approvedBy and approvedAt governance fields.
// approvedAt should be an ISO 8601 date string (e.g. "2024-03-01").
func WithApproval(approvedBy, approvedAt string) FunctionOption {
	return func(o *functionOptions) {
		o.approvedBy = approvedBy
		o.approvedAt = approvedAt
	}
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
// availCalc, _ := forge.New("availabilityCalc", "1.0.0",
//
//	"inputs", availInCodec,
//	"availability", availabilityCodec,
//	computeAvailability,
//	forge.WithRefinement(func(in AvailabilityIn) error {
//	    if float64(in.Downtime) > float64(in.PlannedTime) {
//	        return fmt.Errorf("downtime (%v h) must not exceed plannedTime (%v h)",
//	            in.Downtime, in.PlannedTime)
//	    }
//	    return nil
//	}),
//
// )
func WithRefinement[In any](fn func(In) error) FunctionOption {
	return func(o *functionOptions) {
		o.refinement = func(v any) error { return fn(v.(In)) }
	}
}
