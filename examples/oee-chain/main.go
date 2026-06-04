// Package oee-chain demonstrates the three-layer go-codex architecture end-to-end:
//
//	Layer 1: codex — validated domain types + codex.MapCodecSafe for wire-type bridging
//	Layer 2: api/events — channel contracts, AsyncAPI spec generation
//	Layer 3: forge — named, governed KPI computation with pipeline spec generation
//
// Use case: OT sensors publish equipment measurements as raw JSON over MQTT.
// Our service consumes those messages, validates the payload, maps float64 wire
// values to strongly typed domain values, and computes OEE (Overall Equipment
// Efficiency) KPIs through a governed forge pipeline.
//
// Pipeline overview:
//
//	Sensor JSON  ──decode──▶  SensorReading (codex.MapCodecSafe maps float64 → domain types)
//	                                 │
//	                      ┌──────────┼──────────┐
//	                      ▼          ▼           ▼
//	             availabilityCalc  performanceCalc  qualityCalc
//	             (forge.Function)  (forge.Function) (forge.Function)
//	                      │          │           │
//	                      └──────────┴──────────┘
//	                                 │
//	                            oeeCalc.Apply
//	                            (forge.Function)
//	                                 │
//	                           KPIResult ──publish──▶ broker
//
// Why codex.MapCodecSafe for wire bridging, not forge.Function?
//
//	codex.MapCodecSafe answers: "How do I represent float64 as PlannedTime?"
//	  ✓ Bidirectional (encode + decode)   ✓ Structural/representational concern
//	  ✗ No name/version/governance        ✗ Not tracked in pipeline spec
//
//	forge.Function answers: "What governed computation derives Availability from AvailabilityIn?"
//	  ✓ Named + versioned + SHA-256 hash  ✓ Registry → pipeline YAML spec
//	  ✓ PipelineObserver telemetry         ✓ Governance metadata (author, approvals)
//	  ✗ Unidirectional only               ✗ Not for structural type mapping
//
// Run with: go run ./examples/oee-chain
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/forge"
	"github.com/DaniDeer/go-codex/render/pipeline"
	"github.com/DaniDeer/go-codex/stats"
	"github.com/DaniDeer/go-codex/validate"
)

// ─────────────────────────────────────────────────────────────────────────────
// Layer 1 — Domain types
//
// Strongly typed aliases prevent accidental substitution (e.g. passing a
// PlannedTime where a PlannedCycles is expected). Each type has its own codec
// with domain-specific constraints.
// ─────────────────────────────────────────────────────────────────────────────

type PlannedTime float64   // planned production time in hours; > 0
type Downtime float64      // unplanned downtime in hours; ≥ 0
type PlannedCycles float64 // ideal cycle count for the shift; > 0
type ActualCycles float64  // actual cycle count produced; ≥ 0
type GoodUnits float64     // units passing quality check; ≥ 0
type TotalUnits float64    // total units produced; ≥ 0

type Availability float64 // uptime fraction: [0, 1]
type Performance float64  // speed fraction: [0, 1]
type Quality float64      // quality fraction: [0, 1]
type OEE float64          // overall equipment efficiency: [0, 1]

// ─────────────────────────────────────────────────────────────────────────────
// Layer 1 — Wire bridging: codex.MapCodecSafe
//
// MQTT sensors send raw float64 values. codex.MapCodecSafe maps float64 → T
// bidirectionally. This is a structural/representational concern — not a named,
// governed computation. The mapping lives in the codec layer, not in forge.
// ─────────────────────────────────────────────────────────────────────────────

func positiveFloat64() codex.Codec[float64] {
	return codex.Float64().Refine(codex.Constraint[float64]{
		Name:    "positive",
		Check:   func(v float64) bool { return v > 0 },
		Message: func(v float64) string { return "must be > 0" },
	})
}

func nonNegativeFloat64() codex.Codec[float64] {
	return codex.Float64().Refine(codex.Constraint[float64]{
		Name:    "non_negative",
		Check:   func(v float64) bool { return v >= 0 },
		Message: func(v float64) string { return "must be ≥ 0" },
	})
}

func zeroToOne() codex.Codec[float64] {
	return codex.Float64().Refine(validate.RangeFloat(0, 1))
}

// mapFloat64 uses codex.MapCodecSafe to bridge a validated float64 codec to a
// domain type T. This is the correct tool for wire-type bridging because it is
// bidirectional and structural — it answers "how to represent float64 as T".
func mapFloat64[T ~float64](base codex.Codec[float64]) codex.Codec[T] {
	return codex.MapCodecSafe(base,
		func(f float64) T { return T(f) },
		func(t T) (float64, error) { return float64(t), nil },
	)
}

var (
	plannedTimeCodec   = mapFloat64[PlannedTime](positiveFloat64())
	downtimeCodec      = mapFloat64[Downtime](nonNegativeFloat64())
	plannedCyclesCodec = mapFloat64[PlannedCycles](positiveFloat64())
	actualCyclesCodec  = mapFloat64[ActualCycles](nonNegativeFloat64())
	goodUnitsCodec     = mapFloat64[GoodUnits](nonNegativeFloat64())
	totalUnitsCodec    = mapFloat64[TotalUnits](nonNegativeFloat64())

	availabilityCodec = mapFloat64[Availability](zeroToOne())
	performanceCodec  = mapFloat64[Performance](zeroToOne())
	qualityCodec      = mapFloat64[Quality](zeroToOne())
	oeeCodec          = mapFloat64[OEE](zeroToOne())
)

// ─────────────────────────────────────────────────────────────────────────────
// Layer 2 — Event channel contracts (api/events)
//
// SensorReading is the MQTT payload: raw float64 fields, validated as a codex
// struct codec. SensorReading uses the same domain codecs for its fields —
// channel decoding already produces validated, typed values.
//
// KPIResult is published back to the broker after the forge pipeline runs.
// ─────────────────────────────────────────────────────────────────────────────

type SensorReading struct {
	PlannedTime   PlannedTime
	Downtime      Downtime
	PlannedCycles PlannedCycles
	ActualCycles  ActualCycles
	GoodUnits     GoodUnits
	TotalUnits    TotalUnits
}

type KPIResult struct {
	Availability Availability
	Performance  Performance
	Quality      Quality
	OEE          OEE
}

var sensorReadingCodec = codex.Struct[SensorReading](
	codex.RequiredField[SensorReading, PlannedTime]("plannedTime", plannedTimeCodec,
		func(s SensorReading) PlannedTime { return s.PlannedTime },
		func(s *SensorReading, v PlannedTime) { s.PlannedTime = v },
	),
	codex.RequiredField[SensorReading, Downtime]("downtime", downtimeCodec,
		func(s SensorReading) Downtime { return s.Downtime },
		func(s *SensorReading, v Downtime) { s.Downtime = v },
	),
	codex.RequiredField[SensorReading, PlannedCycles]("plannedCycles", plannedCyclesCodec,
		func(s SensorReading) PlannedCycles { return s.PlannedCycles },
		func(s *SensorReading, v PlannedCycles) { s.PlannedCycles = v },
	),
	codex.RequiredField[SensorReading, ActualCycles]("actualCycles", actualCyclesCodec,
		func(s SensorReading) ActualCycles { return s.ActualCycles },
		func(s *SensorReading, v ActualCycles) { s.ActualCycles = v },
	),
	codex.RequiredField[SensorReading, GoodUnits]("goodUnits", goodUnitsCodec,
		func(s SensorReading) GoodUnits { return s.GoodUnits },
		func(s *SensorReading, v GoodUnits) { s.GoodUnits = v },
	),
	codex.RequiredField[SensorReading, TotalUnits]("totalUnits", totalUnitsCodec,
		func(s SensorReading) TotalUnits { return s.TotalUnits },
		func(s *SensorReading, v TotalUnits) { s.TotalUnits = v },
	),
)

var kpiResultCodec = codex.Struct[KPIResult](
	codex.RequiredField[KPIResult, Availability]("availability", availabilityCodec,
		func(k KPIResult) Availability { return k.Availability },
		func(k *KPIResult, v Availability) { k.Availability = v },
	),
	codex.RequiredField[KPIResult, Performance]("performance", performanceCodec,
		func(k KPIResult) Performance { return k.Performance },
		func(k *KPIResult, v Performance) { k.Performance = v },
	),
	codex.RequiredField[KPIResult, Quality]("quality", qualityCodec,
		func(k KPIResult) Quality { return k.Quality },
		func(k *KPIResult, v Quality) { k.Quality = v },
	),
	codex.RequiredField[KPIResult, OEE]("oee", oeeCodec,
		func(k KPIResult) OEE { return k.OEE },
		func(k *KPIResult, v OEE) { k.OEE = v },
	),
)

// ─────────────────────────────────────────────────────────────────────────────
// Layer 3 — forge: multi-input structs for governed KPI functions
//
// Each forge.Function with multiple inputs declares a struct type grouping those
// inputs. The struct codec validates each field individually; cross-field
// constraints use codex.RefineFunc on the struct codec.
//
// forge.Function is the right tool here because:
//   - The computation is named, versioned, and SHA-256 hashed for governance.
//   - It must appear in a pipeline YAML spec (Registry.Spec).
//   - PipelineObserver telemetry is needed per Apply call.
//   - It is a business computation, not a structural representation.
// ─────────────────────────────────────────────────────────────────────────────

type AvailabilityIn struct {
	PlannedTime PlannedTime
	Downtime    Downtime
}

type PerformanceIn struct {
	PlannedCycles PlannedCycles
	ActualCycles  ActualCycles
}

type QualityIn struct {
	GoodUnits  GoodUnits
	TotalUnits TotalUnits
}

type OEEIn struct {
	Availability Availability
	Performance  Performance
	Quality      Quality
}

// Cross-field constraint: downtime cannot exceed planned time.
var availabilityInCodec = codex.Struct[AvailabilityIn](
	codex.RequiredField[AvailabilityIn, PlannedTime]("plannedTime", plannedTimeCodec,
		func(a AvailabilityIn) PlannedTime { return a.PlannedTime },
		func(a *AvailabilityIn, v PlannedTime) { a.PlannedTime = v },
	),
	codex.RequiredField[AvailabilityIn, Downtime]("downtime", downtimeCodec,
		func(a AvailabilityIn) Downtime { return a.Downtime },
		func(a *AvailabilityIn, v Downtime) { a.Downtime = v },
	),
).RefineFunc(func(a AvailabilityIn) error {
	if float64(a.Downtime) > float64(a.PlannedTime) {
		return fmt.Errorf("downtime (%v) exceeds plannedTime (%v)", a.Downtime, a.PlannedTime)
	}
	return nil
})

var performanceInCodec = codex.Struct[PerformanceIn](
	codex.RequiredField[PerformanceIn, PlannedCycles]("plannedCycles", plannedCyclesCodec,
		func(p PerformanceIn) PlannedCycles { return p.PlannedCycles },
		func(p *PerformanceIn, v PlannedCycles) { p.PlannedCycles = v },
	),
	codex.RequiredField[PerformanceIn, ActualCycles]("actualCycles", actualCyclesCodec,
		func(p PerformanceIn) ActualCycles { return p.ActualCycles },
		func(p *PerformanceIn, v ActualCycles) { p.ActualCycles = v },
	),
)

var qualityInCodec = codex.Struct[QualityIn](
	codex.RequiredField[QualityIn, GoodUnits]("goodUnits", goodUnitsCodec,
		func(q QualityIn) GoodUnits { return q.GoodUnits },
		func(q *QualityIn, v GoodUnits) { q.GoodUnits = v },
	),
	codex.RequiredField[QualityIn, TotalUnits]("totalUnits", totalUnitsCodec,
		func(q QualityIn) TotalUnits { return q.TotalUnits },
		func(q *QualityIn, v TotalUnits) { q.TotalUnits = v },
	),
)

var oeeInCodec = codex.Struct[OEEIn](
	codex.RequiredField[OEEIn, Availability]("availability", availabilityCodec,
		func(o OEEIn) Availability { return o.Availability },
		func(o *OEEIn, v Availability) { o.Availability = v },
	),
	codex.RequiredField[OEEIn, Performance]("performance", performanceCodec,
		func(o OEEIn) Performance { return o.Performance },
		func(o *OEEIn, v Performance) { o.Performance = v },
	),
	codex.RequiredField[OEEIn, Quality]("quality", qualityCodec,
		func(o OEEIn) Quality { return o.Quality },
		func(o *OEEIn, v Quality) { o.Quality = v },
	),
)

// ─────────────────────────────────────────────────────────────────────────────
// Observer — unified implementation for all three layers
//
// ChainObserver implements both stats.ValidationObserver (codec layer) and
// stats.PipelineObserver (forge layer). Wire it at every level so one struct
// captures telemetry across the full pipeline:
//
//   - Layer 1 (codec): call stats.ReportErrors(obs, "payload", err) after
//     every sensorCh.Decode — one RecordValidationError call per failing field.
//   - Layer 3 (forge): pass obs to forge.Registry.WithObserver — one
//     RecordApply call per Function.Apply.
//
// In production replace the in-memory slices with Prometheus counters or an
// OpenTelemetry meter; the interface signatures stay the same.
// ─────────────────────────────────────────────────────────────────────────────

type validationEvent struct {
	location   string
	constraint string
	field      string
}

type applyEvent struct {
	function string
	version  string
	success  bool
	duration time.Duration
}

// ChainObserver collects codec validation errors and forge Apply events.
// It satisfies both [stats.ValidationObserver] and [stats.PipelineObserver].
type ChainObserver struct {
	validations []validationEvent
	applies     []applyEvent
}

// RecordValidationError implements [stats.ValidationObserver].
// Called by stats.ReportErrors after each codec Decode that returns errors.
func (o *ChainObserver) RecordValidationError(location, constraint, field string) {
	o.validations = append(o.validations, validationEvent{location, constraint, field})
	fmt.Printf("  [codec]  validation error — location=%-10s constraint=%-20s field=%s\n",
		location, constraint, field)
}

// RecordApply implements [stats.PipelineObserver].
// Called automatically by forge.Function.Apply via the Registry observer.
func (o *ChainObserver) RecordApply(name, version string, success bool, d time.Duration) {
	o.applies = append(o.applies, applyEvent{name, version, success, d})
	status := "ok"
	if !success {
		status = "FAIL"
	}
	fmt.Printf("  [forge]  apply         — function=%-20s version=%s status=%s duration=%v\n",
		name, version, status, d.Round(time.Microsecond))
}

// Summary prints aggregated stats collected across all levels.
func (o *ChainObserver) Summary() {
	fmt.Println("\n=== Observer summary ===")

	// ── Codec layer ───────────────────────────────────────────────────────────
	fmt.Printf("  codec validation errors : %d\n", len(o.validations))
	byConstraint := map[string]int{}
	for _, v := range o.validations {
		byConstraint[v.constraint]++
	}
	for c, n := range byConstraint {
		fmt.Printf("    constraint %-24s × %d\n", c, n)
	}

	// ── Forge layer ───────────────────────────────────────────────────────────
	var applyOK, applyFail int
	var totalDur time.Duration
	for _, a := range o.applies {
		totalDur += a.duration
		if a.success {
			applyOK++
		} else {
			applyFail++
		}
	}
	fmt.Printf("  forge Apply calls       : %d  (ok=%d  fail=%d)\n",
		len(o.applies), applyOK, applyFail)
	if len(o.applies) > 0 {
		fmt.Printf("  forge total duration    : %v  (avg %v)\n",
			totalDur.Round(time.Microsecond),
			(totalDur / time.Duration(len(o.applies))).Round(time.Microsecond))
	}
}

func main() {
	// Create the shared observer. It is wired at every layer below so that a
	// single Summary() call at the end reports across codec + forge.
	obs := &ChainObserver{}

	// ── Layer 2: event channels ───────────────────────────────────────────────

	// WithTopicConstraints validates every topic registered via AddChannel at
	// builder time. validate.MQTTPublishTopic rejects empty topics and wildcard
	// characters (+ or #), making it safe for both subscribe and publish channels.
	// Use validate.MQTTTopic if you only need the general MQTT topic rules.
	b := events.NewBuilder(
		events.Info{
			Title:       "OEE Sensor Events",
			Version:     "1.0.0",
			Description: "Channels for receiving OT sensor readings and publishing KPI results.",
		},
		events.WithTopicConstraints(validate.MQTTPublishTopic),
	)

	sensorCh, err := events.NewChannel[SensorReading]("sensors/oee/reading", sensorReadingCodec,
		events.ChannelMeta{Description: "Raw equipment measurements from OT sensors."},
		events.Subscribe{
			Summary:    "Receive sensor reading",
			Tags:       []string{"sensor", "oee"},
			SchemaName: "SensorReading",
		},
	).Register(b)
	if err != nil {
		fmt.Fprintf(os.Stderr, "AddChannel sensor: %v\n", err)
		os.Exit(1)
	}

	kpiCh, err := events.NewChannel[KPIResult]("kpi/oee/result", kpiResultCodec,
		events.ChannelMeta{Description: "Computed OEE KPI results published after each sensor reading."},
		events.Publish{
			Summary:    "Publish KPI result",
			Tags:       []string{"kpi", "oee"},
			SchemaName: "KPIResult",
		},
	).Register(b)
	if err != nil {
		fmt.Fprintf(os.Stderr, "AddChannel kpi: %v\n", err)
		os.Exit(1)
	}

	// ── Layer 3: forge pipeline ───────────────────────────────────────────────
	//
	// forge.NewFunction panics on misconfigured function definitions (empty name,
	// empty version). These are programmer errors — they are caught at startup,
	// just like a misconfigured HTTP router.

	availabilityCalc := forge.NewFunction(
		"availabilityCalc", "1.0.0",
		availabilityInCodec,
		availabilityCodec,
		func(in AvailabilityIn) (Availability, error) {
			return Availability((float64(in.PlannedTime) - float64(in.Downtime)) / float64(in.PlannedTime)), nil
		},
		forge.WithDescription("Computes availability as (plannedTime - downtime) / plannedTime."),
		forge.WithAuthor("oee-team"),
	)

	performanceCalc := forge.NewFunction(
		"performanceCalc", "1.0.0",
		performanceInCodec,
		performanceCodec,
		func(in PerformanceIn) (Performance, error) {
			return Performance(float64(in.ActualCycles) / float64(in.PlannedCycles)), nil
		},
		forge.WithDescription("Computes performance as actualCycles / plannedCycles."),
		forge.WithAuthor("oee-team"),
	)

	qualityCalc := forge.NewFunction(
		"qualityCalc", "1.0.0",
		qualityInCodec,
		qualityCodec,
		func(in QualityIn) (Quality, error) {
			if float64(in.TotalUnits) == 0 {
				return 0, nil
			}
			return Quality(float64(in.GoodUnits) / float64(in.TotalUnits)), nil
		},
		forge.WithDescription("Computes quality as goodUnits / totalUnits."),
		forge.WithAuthor("oee-team"),
	)

	oeeCalc := forge.NewFunction(
		"oeeCalc", "1.0.0",
		oeeInCodec,
		oeeCodec,
		func(in OEEIn) (OEE, error) {
			return OEE(float64(in.Availability) * float64(in.Performance) * float64(in.Quality)), nil
		},
		forge.WithDescription("Computes OEE = availability × performance × quality."),
		forge.WithAuthor("oee-team"),
	)

	// ── Registry: graph inference + pipeline spec ─────────────────────────────
	// Wire obs as the PipelineObserver — every Apply call reports to obs.RecordApply.

	reg := forge.NewRegistry("OEE Pipeline", "1.0.0").WithObserver(obs)
	reg = availabilityCalc.Register(reg)
	reg = performanceCalc.Register(reg)
	reg = qualityCalc.Register(reg)
	reg = oeeCalc.Register(reg)

	// ── Simulate sensor message handling ─────────────────────────────────────
	//
	// In production: broker callback → ch.Decode(rawPayload) → pipeline.
	// Here: construct payloads directly (no real MQTT needed).
	//
	// stats.ReportErrors wires obs as the codec-level ValidationObserver:
	// it extracts codex.ValidationErrors from the decode error and calls
	// obs.RecordValidationError once per failing field.

	fmt.Println("=== Reading 1: valid sensor data ===")

	rawPayload := map[string]any{
		"plannedTime":   8.0,
		"downtime":      1.0,
		"plannedCycles": 1000.0,
		"actualCycles":  850.0,
		"goodUnits":     820.0,
		"totalUnits":    850.0,
	}
	rawJSON, _ := json.Marshal(rawPayload)

	reading, decodeErr := sensorCh.Decode(rawJSON)
	stats.ReportErrors(obs, "payload", decodeErr) // codec layer — ValidationObserver
	if decodeErr != nil {
		fmt.Fprintf(os.Stderr, "decode sensor reading: %v\n", decodeErr)
		os.Exit(1)
	}
	fmt.Printf("  Decoded: plannedTime=%.1f  downtime=%.1f  plannedCycles=%.0f  actualCycles=%.0f  goodUnits=%.0f  totalUnits=%.0f\n",
		reading.PlannedTime, reading.Downtime, reading.PlannedCycles, reading.ActualCycles, reading.GoodUnits, reading.TotalUnits)

	// Run forge pipeline — each Apply call reports to obs.RecordApply.
	avail, err := availabilityCalc.Apply(AvailabilityIn{PlannedTime: reading.PlannedTime, Downtime: reading.Downtime})
	if err != nil {
		fmt.Fprintf(os.Stderr, "availabilityCalc: %v\n", err)
		os.Exit(1)
	}
	perf, err := performanceCalc.Apply(PerformanceIn{PlannedCycles: reading.PlannedCycles, ActualCycles: reading.ActualCycles})
	if err != nil {
		fmt.Fprintf(os.Stderr, "performanceCalc: %v\n", err)
		os.Exit(1)
	}
	qual, err := qualityCalc.Apply(QualityIn{GoodUnits: reading.GoodUnits, TotalUnits: reading.TotalUnits})
	if err != nil {
		fmt.Fprintf(os.Stderr, "qualityCalc: %v\n", err)
		os.Exit(1)
	}
	oee, err := oeeCalc.Apply(OEEIn{Availability: avail, Performance: perf, Quality: qual})
	if err != nil {
		fmt.Fprintf(os.Stderr, "oeeCalc: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("  KPIs: availability=%.4f  performance=%.4f  quality=%.4f  OEE=%.4f\n",
		avail, perf, qual, oee)

	kpiResult := KPIResult{Availability: avail, Performance: perf, Quality: qual, OEE: oee}
	encoded, err := kpiCh.Encode(kpiResult)
	if err != nil {
		fmt.Fprintf(os.Stderr, "encode KPI result: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("  Published: %s\n", encoded)

	// ── Reading 2: invalid payload — codec observer fires ─────────────────────
	//
	// plannedTime=0 fails the "positive" constraint. stats.ReportErrors extracts
	// the codex.ValidationErrors and calls obs.RecordValidationError for the
	// failing field. The forge pipeline is skipped entirely.

	fmt.Println("\n=== Reading 2: invalid payload (plannedTime=0) ===")

	badPayload := map[string]any{
		"plannedTime":   0.0, // fails "positive" constraint
		"downtime":      1.0,
		"plannedCycles": 1000.0,
		"actualCycles":  850.0,
		"goodUnits":     820.0,
		"totalUnits":    850.0,
	}
	badJSON, _ := json.Marshal(badPayload)

	_, decodeErr = sensorCh.Decode(badJSON)
	stats.ReportErrors(obs, "payload", decodeErr) // codec layer — fires RecordValidationError
	if decodeErr != nil {
		fmt.Printf("  decode rejected (expected): %v\n", decodeErr)
	}

	// ── Reading 3: valid payload, cross-field constraint violation ────────────
	//
	// Downtime > PlannedTime is caught by the codec-level RefineFunc on
	// AvailabilityIn. forge.Function.Apply returns a RefinementError and calls
	// obs.RecordApply(success=false).

	fmt.Println("\n=== Reading 3: cross-field constraint (downtime > plannedTime) ===")

	_, err = availabilityCalc.Apply(AvailabilityIn{PlannedTime: 2.0, Downtime: 5.0})
	var re forge.RefinementError
	if errors.As(err, &re) {
		fmt.Printf("  refinement error (expected): function=%q  cause=%v\n", re.Function, re.Err)
	}

	// ── Spec output ───────────────────────────────────────────────────────────

	fmt.Println("\n=== AsyncAPI spec (events layer) ===")
	asyncDoc, err := b.AsyncAPISpec()
	if err != nil {
		fmt.Fprintf(os.Stderr, "AsyncAPISpec: %v\n", err)
		os.Exit(1)
	}
	asyncYAML, err := asyncDoc.MarshalYAML()
	if err != nil {
		fmt.Fprintf(os.Stderr, "MarshalYAML: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(string(asyncYAML))

	fmt.Println("=== Pipeline spec (forge layer) ===")
	pipelineSpec, err := pipeline.Render(reg.Spec())
	if err != nil {
		fmt.Fprintf(os.Stderr, "pipeline.Render: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(string(pipelineSpec))

	// ── Stats summary — collected across all levels ───────────────────────────
	obs.Summary()
}
