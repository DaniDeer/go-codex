// Package main demonstrates the forge package for signed, governed KPI computation.
//
// This example shows the three composable layers of go-codex:
//
//   - Layer 1: Validated domain models — codex.Codec[T] with Refine constraints.
//   - Layer 2 (not shown here): API endpoints — api/rest or api/events builders.
//   - Layer 3: KPI pipelines — forge.Function[In,Out] with governance + computation graph.
//
// The domain is Overall Equipment Efficiency (OEE), a standard manufacturing KPI:
//
// OEE = Availability × Performance × Quality
//
// Availability measures uptime: how much of the planned production time was running.
// Performance measures speed: how close to the ideal cycle time the equipment ran.
// Quality measures correctness: the fraction of units produced without defects.
//
// The example demonstrates:
//  1. Domain codecs with validate.RangeFloat constraints.
//  2. forge.MeasuredCodec for attaching provenance to values crossing system boundaries.
//  3. Minimal pipeline definition — only name + version + codecs + compute fn.
//  4. Governed pipeline definition — same but with FunctionOpt governance metadata.
//  5. Single-input functions: gradeCalc, availabilityOnlyOEE.
//  6. Multi-input functions via struct codecs (codex.Struct): availabilityCalc, performanceCalc.
//  7. Sum-type composition: OEEIn assembles validated Availability+Performance+Quality outputs.
//  8. Cross-input validation: codec-level RefineFunc (preferred) and WithRefinement (pipeline-level).
//  9. forge.Compose: chaining Function[A,B] + Function[B,Out] → Function[A,Out].
//
// 10. Structured errors (forge.InputError, RefinementError, ApplyError) via errors.As.
// 11. stats.PipelineObserver injected via Registry.WithObserver for Apply telemetry.
// 12. forge.Registry + render/pipeline YAML spec output.
package main

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/forge"
	"github.com/DaniDeer/go-codex/render/pipeline"
	"github.com/DaniDeer/go-codex/validate"
)

// --- Domain types -----------------------------------------------------------

type PlannedTime float64    // planned production time in hours; must be > 0
type Downtime float64       // unplanned downtime in hours; must be ≥ 0
type PlannedCycles float64  // ideal cycle count for the shift; must be > 0
type ActualCycles float64   // actual cycle count produced; must be ≥ 0
type Quality float64        // fraction of good units: [0, 1]
type Availability float64   // uptime fraction: [0, 1]
type Performance float64    // speed fraction: [0, 1]
type OEE float64            // overall equipment efficiency: [0, 1]
type EfficiencyGrade string // "excellent" | "acceptable" | "poor"

// --- Multi-input structs for forge functions ---------------------------------
//
// Each multi-input function groups its inputs in a dedicated struct.
// The struct codec validates each field individually; cross-field constraints
// are added via codex.RefineFunc on the struct codec.
// This is the "sum type" pattern — validated output types from upstream functions
// compose as fields of a downstream function's input struct.

// AvailabilityIn groups the inputs for availabilityCalc.
// Cross-field constraint: Downtime must not exceed PlannedTime.
type AvailabilityIn struct {
	PlannedTime PlannedTime
	Downtime    Downtime
}

// PerformanceIn groups the inputs for performanceCalc.
type PerformanceIn struct {
	PlannedCycles PlannedCycles
	ActualCycles  ActualCycles
}

// OEEIn assembles the three validated KPI component outputs as a single input
// to oeeCalc. This is the sum type composition pattern: each upstream function
// produces a strongly typed, validated value; OEEIn collects them as a struct.
type OEEIn struct {
	Availability Availability
	Performance  Performance
	Quality      Quality
}

// --- Domain codecs ----------------------------------------------------------

func zeroToOne() codex.Codec[float64] {
	return codex.Float64().Refine(validate.RangeFloat(0, 1))
}

func mapFloat64[T ~float64](c codex.Codec[float64]) codex.Codec[T] {
	return codex.MapCodecSafe(c,
		func(f float64) T { return T(f) },
		func(t T) (float64, error) { return float64(t), nil },
	)
}

var (
	plannedTimeCodec  = mapFloat64[PlannedTime](codex.Float64().Refine(validate.PositiveFloat))
	downtimeCodec     = mapFloat64[Downtime](codex.Float64().Refine(validate.MinFloat(0)))
	plannedCycleCodec = mapFloat64[PlannedCycles](codex.Float64().Refine(validate.PositiveFloat))
	actualCycleCodec  = mapFloat64[ActualCycles](codex.Float64().Refine(validate.MinFloat(0)))

	qualityCodec      = mapFloat64[Quality](zeroToOne())
	availabilityCodec = mapFloat64[Availability](zeroToOne()).WithTitle("availability")
	performanceCodec  = mapFloat64[Performance](zeroToOne()).WithTitle("performance")
	oeeCodec          = mapFloat64[OEE](zeroToOne()).WithTitle("oee")

	gradeCodec = codex.MapCodecSafe(
		codex.String().Refine(validate.OneOf("excellent", "acceptable", "poor")),
		func(s string) EfficiencyGrade { return EfficiencyGrade(s) },
		func(g EfficiencyGrade) (string, error) { return string(g), nil },
	).WithTitle("grade")

	// availabilityInCodec validates each field and enforces the cross-field constraint
	// that Downtime ≤ PlannedTime directly on the codec — the preferred place for
	// constraints that are properties of the domain rather than the pipeline.
	availabilityInCodec = codex.Struct[AvailabilityIn](
		codex.RequiredField("plannedTime", plannedTimeCodec,
			func(v AvailabilityIn) PlannedTime { return v.PlannedTime },
			func(v *AvailabilityIn, f PlannedTime) { v.PlannedTime = f },
		),
		codex.RequiredField("downtime", downtimeCodec,
			func(v AvailabilityIn) Downtime { return v.Downtime },
			func(v *AvailabilityIn, f Downtime) { v.Downtime = f },
		),
	).RefineFunc(func(in AvailabilityIn) error {
		if float64(in.Downtime) > float64(in.PlannedTime) {
			return fmt.Errorf("downtime (%v h) must not exceed plannedTime (%v h)",
				in.Downtime, in.PlannedTime)
		}
		return nil
	})

	performanceInCodec = codex.Struct[PerformanceIn](
		codex.RequiredField("plannedCycles", plannedCycleCodec,
			func(v PerformanceIn) PlannedCycles { return v.PlannedCycles },
			func(v *PerformanceIn, f PlannedCycles) { v.PlannedCycles = f },
		),
		codex.RequiredField("actualCycles", actualCycleCodec,
			func(v PerformanceIn) ActualCycles { return v.ActualCycles },
			func(v *PerformanceIn, f ActualCycles) { v.ActualCycles = f },
		),
	)

	// oeeInCodec validates the three KPI component values assembled from upstream outputs.
	// This is the sum type composition: each field was produced and validated by its own
	// forge function; oeeInCodec re-validates them as a unit.
	oeeInCodec = codex.Struct[OEEIn](
		codex.RequiredField("availability", availabilityCodec,
			func(v OEEIn) Availability { return v.Availability },
			func(v *OEEIn, f Availability) { v.Availability = f },
		),
		codex.RequiredField("performance", performanceCodec,
			func(v OEEIn) Performance { return v.Performance },
			func(v *OEEIn, f Performance) { v.Performance = f },
		),
		codex.RequiredField("quality", qualityCodec,
			func(v OEEIn) Quality { return v.Quality },
			func(v *OEEIn, f Quality) { v.Quality = f },
		),
	)
)

func main() {
	// -----------------------------------------------------------------------
	// Layer 1 demo: MeasuredCodec
	// -----------------------------------------------------------------------
	fmt.Println("=== MeasuredCodec: boundary provenance ===")
	fmt.Println()

	measuredQuality := forge.MeasuredCodec(qualityCodec)
	mq := forge.Measured[Quality]{
		Source:  "quality-control-system",
		Version: "3.1.0",
		Author:  "Quality Engineering",
		Value:   Quality(0.98),
	}
	encoded, err := measuredQuality.Encode(mq)
	must(err, "measuredQuality.Encode")
	fmt.Printf("Encoded Measured[Quality]: %v\n", encoded)

	decoded, err := measuredQuality.Decode(encoded)
	must(err, "measuredQuality.Decode")
	fmt.Printf("Decoded → source=%q version=%q value=%.2f\n\n", decoded.Source, decoded.Version, decoded.Value)

	_, err = measuredQuality.Decode(map[string]any{
		"source": "", "version": "1.0", "author": "OT", "value": float64(0.9),
	})
	fmt.Printf("Empty source → error: %v\n\n", err)

	// -----------------------------------------------------------------------
	// Layer 3 demo: Minimal pipeline definition
	//
	// No governance metadata — name + version + codec + compute fn.
	// WithRefinement adds a pipeline-level cross-input constraint.
	// -----------------------------------------------------------------------
	fmt.Println("=== Minimal pipeline: struct input, WithRefinement ===")
	fmt.Println()

	simpleAvailInCodec := codex.Struct[AvailabilityIn](
		codex.RequiredField("plannedTime", plannedTimeCodec,
			func(v AvailabilityIn) PlannedTime { return v.PlannedTime },
			func(v *AvailabilityIn, f PlannedTime) { v.PlannedTime = f },
		),
		codex.RequiredField("downtime", downtimeCodec,
			func(v AvailabilityIn) Downtime { return v.Downtime },
			func(v *AvailabilityIn, f Downtime) { v.Downtime = f },
		),
	)
	simpleAvailCalc := forge.NewFunction("availabilityCalcSimple", "1.0.0",
		simpleAvailInCodec,
		availabilityCodec,
		func(in AvailabilityIn) (Availability, error) {
			return Availability((float64(in.PlannedTime) - float64(in.Downtime)) / float64(in.PlannedTime)), nil
		},
		// Pipeline-level cross-input constraint via WithRefinement.
		// Use this when the constraint is specific to this pipeline's policy rather
		// than to the domain type itself. For domain constraints, prefer RefineFunc on the codec.
		forge.WithRefinement(func(in AvailabilityIn) error {
			if float64(in.Downtime) > float64(in.PlannedTime) {
				return fmt.Errorf("downtime (%v h) exceeds planned time (%v h)",
					in.Downtime, in.PlannedTime)
			}
			return nil
		}),
	)

	av0, err := simpleAvailCalc.Apply(AvailabilityIn{PlannedTime: 8, Downtime: 1})
	must(err, "simpleAvailCalc.Apply")
	fmt.Printf("simpleAvailCalc(8h planned, 1h down) → availability=%.4f  hash=%s\n",
		av0, simpleAvailCalc.Spec.Hash[:14]+"…")

	_, refErr := simpleAvailCalc.Apply(AvailabilityIn{PlannedTime: 8, Downtime: 9})
	var re forge.RefinementError
	if errors.As(refErr, &re) {
		fmt.Printf("WithRefinement violation           → RefinementError: %v\n", refErr)
	}
	fmt.Println()

	// -----------------------------------------------------------------------
	// Governed pipeline definition — opt-in governance metadata
	//
	// Cross-field validation is now on the availabilityInCodec (RefineFunc) — the
	// preferred place when the constraint is a property of the domain type.
	// Governance fields are excluded from the contract hash — changing approver
	// does not change what the function computes.
	// -----------------------------------------------------------------------
	fmt.Println("=== Governed pipeline: codec RefineFunc + governance metadata ===")
	fmt.Println()

	availabilityCalc := forge.NewFunction("availabilityCalc", "1.0.0",
		availabilityInCodec, // RefineFunc is on the codec
		availabilityCodec,
		func(in AvailabilityIn) (Availability, error) {
			return Availability((float64(in.PlannedTime) - float64(in.Downtime)) / float64(in.PlannedTime)), nil
		},
		forge.FunctionMeta{
			Description: "Computes availability as (plannedTime - downtime) / plannedTime.",
			Author:      "OT Engineering",
			ApprovedBy:  "Plant Manager",
			ApprovedAt:  "2024-03-01",
		},
	)

	av, err := availabilityCalc.Apply(AvailabilityIn{PlannedTime: 8, Downtime: 1})
	must(err, "availabilityCalc.Apply")
	fmt.Printf("availabilityCalc(8h, 1h) → availability=%.4f  author=%q approvedBy=%q\n",
		av, availabilityCalc.Spec.Author, availabilityCalc.Spec.ApprovedBy)

	_, inputErr := availabilityCalc.Apply(AvailabilityIn{PlannedTime: -1, Downtime: 0})
	fmt.Printf("Apply(plannedTime=-1)    → error: %v\n", inputErr)

	_, crossErr := availabilityCalc.Apply(AvailabilityIn{PlannedTime: 8, Downtime: 9})
	fmt.Printf("Apply(9h down > 8h plan) → error: %v\n", crossErr)
	fmt.Println()

	// -----------------------------------------------------------------------
	// performanceCalc: Function[PerformanceIn, Performance]
	// -----------------------------------------------------------------------
	performanceCalc := forge.NewFunction("performanceCalc", "1.0.0",
		performanceInCodec,
		performanceCodec,
		func(in PerformanceIn) (Performance, error) {
			ratio := float64(in.ActualCycles) / float64(in.PlannedCycles)
			if ratio > 1 {
				ratio = 1
			}
			return Performance(ratio), nil
		},
		forge.FunctionMeta{
			Description: "Computes performance as min(1, actualCycles / plannedCycles).",
			Author:      "OT Engineering",
			ApprovedBy:  "Plant Manager",
			ApprovedAt:  "2024-03-01",
		},
	)

	pe, err := performanceCalc.Apply(PerformanceIn{PlannedCycles: 400, ActualCycles: 360})
	must(err, "performanceCalc.Apply")
	fmt.Printf("performanceCalc(400 planned, 360 actual) → performance=%.4f\n\n", pe)

	// -----------------------------------------------------------------------
	// oeeCalc: Function[OEEIn, OEE]
	//
	// OEEIn is the sum type: it assembles the validated outputs of availabilityCalc,
	// performanceCalc, and a quality measurement into a single struct input.
	// Each field is re-validated by the oeeInCodec before computing the product.
	// -----------------------------------------------------------------------
	oeeCalc := forge.NewFunction("oeeCalc", "1.0.0",
		oeeInCodec,
		oeeCodec,
		func(in OEEIn) (OEE, error) {
			return OEE(float64(in.Availability) * float64(in.Performance) * float64(in.Quality)), nil
		},
		forge.FunctionMeta{
			Description: "Computes OEE as the product of availability, performance, and quality.",
			Author:      "OT Engineering",
			ApprovedBy:  "Quality Manager",
			ApprovedAt:  "2024-03-01",
		},
	)

	fmt.Println("=== Sum type composition: OEEIn assembles upstream KPI outputs ===")
	fmt.Println()

	// Assemble the OEEIn struct from individually validated KPI outputs.
	oeeVal, err := oeeCalc.Apply(OEEIn{
		Availability: av,
		Performance:  pe,
		Quality:      decoded.Value,
	})
	must(err, "oeeCalc.Apply")
	fmt.Printf("Apply(av=%.4f, pe=%.4f, qu=%.4f) → OEE=%.4f\n", av, pe, decoded.Value, oeeVal)

	_, badQu := oeeCalc.Apply(OEEIn{Availability: 1.0, Performance: 1.0, Quality: 1.5})
	fmt.Printf("Apply(quality=1.5)              → error: %v\n\n", badQu)

	// -----------------------------------------------------------------------
	// Structured errors — errors.As for typed inspection
	// -----------------------------------------------------------------------
	fmt.Println("=== Structured errors ===")
	fmt.Println()

	var ie forge.InputError
	if errors.As(inputErr, &ie) {
		fmt.Printf("InputError       — function=%q input=%q cause=%v\n", ie.Function, ie.Input, ie.Err)
	}

	var re2 forge.RefinementError
	if errors.As(refErr, &re2) {
		fmt.Printf("RefinementError  — function=%q cause=%v\n", re2.Function, re2.Err)
	}

	var oe forge.InputError
	if errors.As(badQu, &oe) {
		fmt.Printf("InputError       — function=%q input=%q cause=%v\n", oe.Function, oe.Input, oe.Err)
	}
	fmt.Println()

	// -----------------------------------------------------------------------
	// PipelineObserver — per-function Apply telemetry
	// -----------------------------------------------------------------------
	fmt.Println("=== PipelineObserver: Apply telemetry ===")
	fmt.Println()

	obs := &applyLogger{}
	obsReg := forge.NewRegistry("OEE Observer Demo", "1.0.0").WithObserver(obs)
	availabilityCalc.Register(obsReg)
	oeeCalc.Register(obsReg)

	_, _ = availabilityCalc.Apply(AvailabilityIn{PlannedTime: 8, Downtime: 1})  // success — observed via PipelineObserver
	_, _ = availabilityCalc.Apply(AvailabilityIn{PlannedTime: -1, Downtime: 0}) // failure — observed via PipelineObserver
	fmt.Println()

	// -----------------------------------------------------------------------
	// forge.Compose: chaining two single-input functions
	//
	// gradeCalc: OEE → EfficiencyGrade
	// availabilityOnlyOEE: Availability → OEE (perfect perf + quality assumed)
	// Compose → Function[Availability, EfficiencyGrade]
	// -----------------------------------------------------------------------
	gradeCalc := forge.NewFunction("gradeCalc", "1.0.0",
		oeeCodec,
		gradeCodec,
		func(o OEE) (EfficiencyGrade, error) {
			switch {
			case float64(o) >= 0.85:
				return "excellent", nil
			case float64(o) >= 0.65:
				return "acceptable", nil
			default:
				return "poor", nil
			}
		},
		forge.FunctionMeta{
			Description: "Converts an OEE value to an efficiency grade.",
			Author:      "OT Engineering",
			ApprovedBy:  "Quality Manager",
			ApprovedAt:  "2024-03-01",
		},
	)

	availabilityOnlyOEE := forge.NewFunction("availabilityOnlyOEE", "1.0.0",
		availabilityCodec,
		oeeCodec,
		func(a Availability) (OEE, error) { return OEE(float64(a)), nil },
		forge.FunctionMeta{
			Description: "Simplified OEE assuming perfect performance and quality.",
			Author:      "OT Engineering",
		},
	)

	shiftGrade := forge.Compose("shiftGradeFromAvailability", "1.0.0",
		availabilityOnlyOEE, gradeCalc,
		forge.FunctionMeta{
			Description: "Rates a shift by availability alone (perf=1, quality=1).",
			Author:      "OT Engineering",
		},
	)

	fmt.Println("=== Compose: shiftGradeFromAvailability ===")
	fmt.Println()
	fmt.Printf("Composed hash: %s\n", shiftGrade.Spec.Hash[:14]+"…")

	for _, a := range []Availability{0.90, 0.70, 0.50} {
		grade, err := shiftGrade.Apply(a)
		must(err, "shiftGrade.Apply")
		fmt.Printf("Apply(availability=%.2f) → grade=%q\n", a, grade)
	}
	fmt.Println()

	// -----------------------------------------------------------------------
	// Registry + render/pipeline: machine-readable computation graph
	// -----------------------------------------------------------------------
	reg := forge.NewRegistry("OEE Calculation Pipeline", "1.0.0").
		WithDescription("Signed, governed functions for computing Overall Equipment Efficiency.")
	availabilityCalc.Register(reg)
	performanceCalc.Register(reg)
	oeeCalc.Register(reg)
	gradeCalc.Register(reg)
	shiftGrade.Register(reg)

	fmt.Println("=== render/pipeline: YAML spec ===")
	fmt.Println()

	spec := reg.Spec()
	yamlBytes, err := pipeline.Render(spec)
	must(err, "pipeline.Render")
	fmt.Println(string(yamlBytes))
}

func must(err error, ctx string) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: unexpected error: %v\n", ctx, err)
		os.Exit(1)
	}
}

// applyLogger is a stats.PipelineObserver that prints each Apply event to stdout.
type applyLogger struct{}

func (a *applyLogger) RecordApply(name, version string, success bool, dur time.Duration) {
	status := "ok"
	if !success {
		status = "err"
	}
	fmt.Printf("[observer] RecordApply name=%-28s version=%s status=%s dur=%v\n",
		name, version, status, dur.Round(time.Microsecond))
}
