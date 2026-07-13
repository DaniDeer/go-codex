// Package stream-oee demonstrates how to govern OEE computation with forge and
// bridge it to a reactive machine event stream using the stream package.
//
// # The design pattern
//
// forge governs the WHAT — each OEE component is a separately signed forge.Function
// with a SHA-256 contract hash, Author, and ApprovedBy. Changing a formula invalidates
// its hash, requiring re-approval. The Registry produces a governance YAML.
//
// stream governs the WHEN — machine events arrive as a push-based stream.
// stream.Window collects events over a time window; when the window fires, the
// governed forge functions run on the batch.
//
// # Key design decision: sequential wrapper vs Tee+CombineLatest3
//
// When all three OEE components derive from the SAME event window, run them
// sequentially inside one wrapper forge function rather than using Tee+CombineLatest3.
// Tee+CombineLatest3 is correct when inputs arrive on INDEPENDENT streams (separate
// MQTT topics with different frequencies). For a single window feeding three functions,
// sequential application is cleaner, avoids sync issues, and the pipeline Registry
// still shows all six functions with their individual hashes.
//
// # Tapping into partial calculations
//
// To observe intermediate values (Availability, Performance, Quality), the wrapper
// forge function returns an [OEEResult] struct carrying all components alongside the
// final OEE. This is the idiomatic pattern for exposing intermediate values in a
// stream pipeline — returning a rich result type rather than splitting via Tee.
//
// Downstream [stream.Tap] observers can then react independently to any field:
//
//	results = stream.Tap(ctx, results, func(r OEEResult) {
//	    if float64(r.Availability) < 0.80 { maintenanceTeam.Notify() }
//	    if float64(r.Quality)      < 0.90 { qualityTeam.Notify()     }
//	})
//
// # Architecture
//
//	MachineEvent stream (goroutine / MQTT)
//	    → stream.Window(windowDuration)      → Stream[[]MachineEvent]
//	    → stream.Apply(computeOEEFromWindow) → Stream[OEEResult]
//	         (OEEResult{Availability, Performance, Quality, OEE})
//	    → stream.Tap: Availability < 80% → maintenance pre-alert
//	    → stream.Tap: Quality      < 90% → quality team notification
//	    → stream.Tee ───────────────────────────────────────
//	         ↓ alert branch                ↓ metrics branch
//	      Filter(oee < target)         FlatMapSlice → []ComponentMetric
//	      Drain(alert)                 Drain(metrics bus)
//
// # Running
//
// go run ./examples/stream-oee
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/forge"
	"github.com/DaniDeer/go-codex/format"
	pipelinerender "github.com/DaniDeer/go-codex/render/pipeline"
	streamrender "github.com/DaniDeer/go-codex/render/stream"
	"github.com/DaniDeer/go-codex/stats"
	"github.com/DaniDeer/go-codex/stream"
	"github.com/DaniDeer/go-codex/validate"
)

// ── Domain types ──────────────────────────────────────────────────────────────

type PlannedHours float64  // planned production time in hours; must be > 0
type DowntimeHours float64 // unplanned downtime in hours; must be ≥ 0
type PlannedCycles float64 // ideal cycle count; must be > 0
type ActualCycles float64  // actual cycles produced; must be ≥ 0
type Quality float64       // fraction of good units: [0, 1]
type Availability float64  // uptime fraction: [0, 1]
type Performance float64   // speed fraction: [0, 1]
type OEE float64           // overall equipment efficiency: [0, 1]

// AvailabilityIn is the multi-input struct for availabilityCalc.
type AvailabilityIn struct {
	PlannedHours  PlannedHours
	DowntimeHours DowntimeHours
}

// PerformanceIn is the multi-input struct for performanceCalc.
type PerformanceIn struct {
	PlannedCycles PlannedCycles
	ActualCycles  ActualCycles
}

// OEEIn assembles the three validated KPI outputs (sum-type composition).
type OEEIn struct {
	Availability Availability
	Performance  Performance
	Quality      Quality
}

// OEEResult carries all intermediate KPI components alongside the final OEE.
// Returning all components enables downstream Tap observers to react to
// individual KPI failures (e.g. quality alert before OEE drops below threshold).
type OEEResult struct {
	Availability Availability
	Performance  Performance
	Quality      Quality
	OEE          OEE
}

// ComponentMetric is a single named metric emitted by the metrics bus (FlatMapSlice).
type ComponentMetric struct {
	Name  string
	Value float64
}

// ── Domain types (machine events) ────────────────────────────────────────────

// EventType classifies each machine event from the control system.
type EventType string

const (
	EventRunning  EventType = "running"   // machine running; Duration = time in minutes
	EventDowntime EventType = "downtime"  // unplanned stop; Duration = time in minutes
	EventGoodPart EventType = "good_part" // one conforming unit produced
	EventDefect   EventType = "defect"    // one non-conforming unit produced
)

// MachineEvent is a raw event from the machine control system.
type MachineEvent struct {
	Type     EventType     `json:"type"`
	Duration time.Duration `json:"duration_ns"` // nanoseconds, meaningful for Running/Downtime
}

// ── Layer 1: Codecs ───────────────────────────────────────────────────────────

func mapFloat64[T ~float64](c codex.Codec[float64]) codex.Codec[T] {
	return codex.MapCodecSafe(c,
		func(f float64) T { return T(f) },
		func(t T) (float64, error) { return float64(t), nil },
	)
}

var (
	zeroToOne   = codex.Float64().Refine(validate.RangeFloat(0, 1))
	posFloat    = codex.Float64().Refine(validate.PositiveFloat)
	nonNegFloat = codex.Float64().Refine(validate.MinFloat(0))

	availabilityCodec = mapFloat64[Availability](zeroToOne).WithTitle("availability")
	performanceCodec  = mapFloat64[Performance](zeroToOne).WithTitle("performance")
	qualityCodec      = mapFloat64[Quality](zeroToOne).WithTitle("quality")
	oeeCodec          = mapFloat64[OEE](zeroToOne).WithTitle("oee")

	availabilityInCodec = codex.Struct[AvailabilityIn](
		codex.RequiredField("plannedHours",
			mapFloat64[PlannedHours](posFloat),
			func(v AvailabilityIn) PlannedHours { return v.PlannedHours },
			func(v *AvailabilityIn, f PlannedHours) { v.PlannedHours = f }),
		codex.RequiredField("downtimeHours",
			mapFloat64[DowntimeHours](nonNegFloat),
			func(v AvailabilityIn) DowntimeHours { return v.DowntimeHours },
			func(v *AvailabilityIn, f DowntimeHours) { v.DowntimeHours = f }),
	).RefineFunc(func(in AvailabilityIn) error {
		if float64(in.DowntimeHours) > float64(in.PlannedHours) {
			return fmt.Errorf("downtime (%.2fh) > plannedHours (%.2fh)", in.DowntimeHours, in.PlannedHours)
		}
		return nil
	})

	performanceInCodec = codex.Struct[PerformanceIn](
		codex.RequiredField("plannedCycles",
			mapFloat64[PlannedCycles](posFloat),
			func(v PerformanceIn) PlannedCycles { return v.PlannedCycles },
			func(v *PerformanceIn, f PlannedCycles) { v.PlannedCycles = f }),
		codex.RequiredField("actualCycles",
			mapFloat64[ActualCycles](nonNegFloat),
			func(v PerformanceIn) ActualCycles { return v.ActualCycles },
			func(v *PerformanceIn, f ActualCycles) { v.ActualCycles = f }),
	)

	oeeResultCodec = codex.Struct[OEEResult](
		codex.RequiredField("availability", availabilityCodec,
			func(v OEEResult) Availability { return v.Availability },
			func(v *OEEResult, f Availability) { v.Availability = f }),
		codex.RequiredField("performance", performanceCodec,
			func(v OEEResult) Performance { return v.Performance },
			func(v *OEEResult, f Performance) { v.Performance = f }),
		codex.RequiredField("quality", qualityCodec,
			func(v OEEResult) Quality { return v.Quality },
			func(v *OEEResult, f Quality) { v.Quality = f }),
		codex.RequiredField("oee", oeeCodec,
			func(v OEEResult) OEE { return v.OEE },
			func(v *OEEResult, f OEE) { v.OEE = f }),
	).WithTitle("oee_result")

	oeeInCodec = codex.Struct[OEEIn](
		codex.RequiredField("availability", availabilityCodec,
			func(v OEEIn) Availability { return v.Availability },
			func(v *OEEIn, f Availability) { v.Availability = f }),
		codex.RequiredField("performance", performanceCodec,
			func(v OEEIn) Performance { return v.Performance },
			func(v *OEEIn, f Performance) { v.Performance = f }),
		codex.RequiredField("quality", qualityCodec,
			func(v OEEIn) Quality { return v.Quality },
			func(v *OEEIn, f Quality) { v.Quality = f }),
	)

	machineEventCodec = codex.Struct[MachineEvent](
		codex.RequiredField("type",
			codex.String().Refine(validate.OneOf("running", "downtime", "good_part", "defect")),
			func(e MachineEvent) string { return string(e.Type) },
			func(e *MachineEvent, v string) { e.Type = EventType(v) }),
		codex.RequiredField("duration_ns",
			codex.Int64().WithTitle("duration_ns"),
			func(e MachineEvent) int64 { return int64(e.Duration) },
			func(e *MachineEvent, v int64) { e.Duration = time.Duration(v) }),
	)

	windowCodec = codex.SliceOf(machineEventCodec).WithTitle("machine_events_window")
)

// ── Layer 1: Governed forge functions ─────────────────────────────────────────
//
// Each OEE component function is separately signed and approved.
// Changing any formula invalidates its SHA-256 hash → re-approval required.
// The ideal cycle rate (cycles per hour of running time) is part of the governed
// formula — changing the rate changes the hash and triggers a governance event.

const idealCyclesPerHour = 10.0 // governed: any change requires re-approval
// 10 cycles/h means a 7-hour run plans 70 cycles; demo produces 65 → Performance ≈ 93%

// eventsToAvailIn derives AvailabilityIn from a batch of machine events.
var eventsToAvailIn = forge.NewFunction(
	"eventsToAvailIn", "1.0.0",
	windowCodec, availabilityInCodec,
	func(events []MachineEvent) (AvailabilityIn, error) {
		var planned, downtime time.Duration
		for _, e := range events {
			switch e.Type {
			case EventRunning:
				planned += e.Duration
			case EventDowntime:
				planned += e.Duration
				downtime += e.Duration
			}
		}
		return AvailabilityIn{
			PlannedHours:  PlannedHours(planned.Hours()),
			DowntimeHours: DowntimeHours(downtime.Hours()),
		}, nil
	},
	forge.FunctionMeta{
		Description: "Derive AvailabilityIn from machine event window: planned hours and downtime hours.",
		Author:      "OT Engineering",
		ApprovedBy:  "Quality Manager",
		ApprovedAt:  "2024-06-01",
	},
)

// eventsToPerfIn derives PerformanceIn from a batch of machine events.
// PlannedCycles = idealCyclesPerHour × running hours (governed constant).
// ActualCycles = count of GoodPart + Defect events.
var eventsToPerfIn = forge.NewFunction(
	"eventsToPerfIn", "1.0.0",
	windowCodec, performanceInCodec,
	func(events []MachineEvent) (PerformanceIn, error) {
		var running time.Duration
		var actual int
		for _, e := range events {
			switch e.Type {
			case EventRunning:
				running += e.Duration
			case EventGoodPart, EventDefect:
				actual++
			}
		}
		return PerformanceIn{
			PlannedCycles: PlannedCycles(running.Hours() * idealCyclesPerHour),
			ActualCycles:  ActualCycles(actual),
		}, nil
	},
	forge.FunctionMeta{
		Description: fmt.Sprintf("Derive PerformanceIn: planned = %.0f cycles/h × running hours; actual = part events.", idealCyclesPerHour),
		Author:      "OT Engineering",
		ApprovedBy:  "Quality Manager",
		ApprovedAt:  "2024-06-01",
	},
)

// eventsToQual derives Quality (good parts / total parts) from a batch.
var eventsToQual = forge.NewFunction(
	"eventsToQual", "1.0.0",
	windowCodec, qualityCodec.WithTitle("quality"),
	func(events []MachineEvent) (Quality, error) {
		var good, total int
		for _, e := range events {
			switch e.Type {
			case EventGoodPart:
				good++
				total++
			case EventDefect:
				total++
			}
		}
		if total == 0 {
			return Quality(1.0), nil
		}
		return Quality(float64(good) / float64(total)), nil
	},
	forge.FunctionMeta{
		Description: "Derive Quality = good_part events / (good_part + defect) events.",
		Author:      "OT Engineering",
		ApprovedBy:  "Quality Manager",
		ApprovedAt:  "2024-06-01",
	},
)

// availabilityCalc computes Availability = (PlannedHours - DowntimeHours) / PlannedHours.
var availabilityCalc = forge.NewFunction(
	"availabilityCalc", "1.0.0",
	availabilityInCodec, availabilityCodec,
	func(in AvailabilityIn) (Availability, error) {
		return Availability((float64(in.PlannedHours) - float64(in.DowntimeHours)) / float64(in.PlannedHours)), nil
	},
	forge.FunctionMeta{
		Description: "Availability = (PlannedHours - DowntimeHours) / PlannedHours.",
		Author:      "OT Engineering",
		ApprovedBy:  "Quality Manager",
		ApprovedAt:  "2024-06-01",
	},
)

// performanceCalc computes Performance = ActualCycles / PlannedCycles.
var performanceCalc = forge.NewFunction(
	"performanceCalc", "1.0.0",
	performanceInCodec, performanceCodec,
	func(in PerformanceIn) (Performance, error) {
		p := float64(in.ActualCycles) / float64(in.PlannedCycles)
		if p > 1.0 {
			p = 1.0 // cap at 100% (over-performance is not OEE > 1)
		}
		return Performance(p), nil
	},
	forge.FunctionMeta{
		Description: "Performance = min(ActualCycles / PlannedCycles, 1.0).",
		Author:      "OT Engineering",
		ApprovedBy:  "Quality Manager",
		ApprovedAt:  "2024-06-01",
	},
)

// oeeCalc is the governing OEE formula: OEE = Availability × Performance × Quality.
var oeeCalc = forge.NewFunction(
	"oeeCalc", "1.0.0",
	oeeInCodec, oeeCodec,
	func(in OEEIn) (OEE, error) {
		return OEE(float64(in.Availability) * float64(in.Performance) * float64(in.Quality)), nil
	},
	forge.FunctionMeta{
		Description: "OEE = Availability × Performance × Quality. SHA-256 hash proves the approved formula.",
		Author:      "OT Engineering",
		ApprovedBy:  "Quality Manager",
		ApprovedAt:  "2024-06-01",
	},
)

// computeOEEFromWindow is the stream-level wrapper that calls all six functions
// sequentially on one event window. Each component function still runs with its
// own governance (hash, author, approval) recorded in the forge.Registry.
//
// This is the correct pattern when all three OEE components derive from the
// SAME event window. Use CombineLatest3 instead when the three components
// arrive from INDEPENDENT streams (separate MQTT topics at different rates).
// computeOEEFromWindow runs all six governed forge functions sequentially on one
// event window and returns OEEResult{Availability, Performance, Quality, OEE}.
//
// Returning all intermediate values (not just the final OEE) enables downstream
// stream.Tap observers to react to individual component thresholds independently.
var computeOEEFromWindow = forge.NewFunction(
	"computeOEEFromWindow", "1.0.0",
	windowCodec, oeeResultCodec,
	func(events []MachineEvent) (OEEResult, error) {
		availIn, err := eventsToAvailIn.Apply(events)
		if err != nil {
			return OEEResult{}, fmt.Errorf("eventsToAvailIn: %w", err)
		}
		avail, err := availabilityCalc.Apply(availIn)
		if err != nil {
			return OEEResult{}, fmt.Errorf("availabilityCalc: %w", err)
		}
		perfIn, err := eventsToPerfIn.Apply(events)
		if err != nil {
			return OEEResult{}, fmt.Errorf("eventsToPerfIn: %w", err)
		}
		perf, err := performanceCalc.Apply(perfIn)
		if err != nil {
			return OEEResult{}, fmt.Errorf("performanceCalc: %w", err)
		}
		qual, err := eventsToQual.Apply(events)
		if err != nil {
			return OEEResult{}, fmt.Errorf("eventsToQual: %w", err)
		}
		oee, err := oeeCalc.Apply(OEEIn{Availability: avail, Performance: perf, Quality: qual})
		if err != nil {
			return OEEResult{}, fmt.Errorf("oeeCalc: %w", err)
		}
		return OEEResult{
			Availability: avail,
			Performance:  perf,
			Quality:      qual,
			OEE:          oee,
		}, nil
	},
	forge.FunctionMeta{
		Description: "Compute OEEResult{Availability, Performance, Quality, OEE} from a machine event window.",
		Author:      "OT Engineering",
		ApprovedBy:  "Quality Manager",
		ApprovedAt:  "2024-06-01",
	},
)

// ── Layer 3: Machine event source ────────────────────────────────────────────

// generateMachineEvents pushes two simulated production windows into rawCh
// then closes it. In production this is an MQTT SubscribeHandler.
//
// Windows represent 8-hour production shifts with:
//   - Window 1 (good shift): 7h running, 1h downtime, 70 good parts, 5 defects
//   - Window 2 (poor shift): 4h running, 4h downtime, 20 good parts, 20 defects
func generateMachineEvents(rawCh chan<- []byte) {
	defer close(rawCh)

	push := func(events []MachineEvent) {
		for _, e := range events {
			raw, _ := json.Marshal(map[string]any{
				"type":        string(e.Type),
				"duration_ns": int64(e.Duration),
			})
			rawCh <- raw
		}
		time.Sleep(15 * time.Millisecond) // gap between windows
	}

	// Window 1: good shift — Availability=87.5%, Performance=75%, Quality=93.3% → OEE≈61.4%
	push(buildShift(7*time.Hour, 1*time.Hour, 60, 5))

	// Window 2: poor shift — Availability=50%, Performance=37.5%, Quality=50% → OEE=9.4%
	push(buildShift(4*time.Hour, 4*time.Hour, 15, 15))
}

// buildShift constructs a realistic event slice for a production shift.
func buildShift(runTime, downTime time.Duration, goodParts, defects int) []MachineEvent {
	events := []MachineEvent{
		{EventRunning, runTime},
		{EventDowntime, downTime},
	}
	for i := 0; i < goodParts; i++ {
		events = append(events, MachineEvent{EventGoodPart, 0})
	}
	for i := 0; i < defects; i++ {
		events = append(events, MachineEvent{EventDefect, 0})
	}
	return events
}

// ── main ──────────────────────────────────────────────────────────────────────

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	obs := stats.NewFanout(stats.NoopObserver{}, stats.NewLoggingObserver(logger))
	// Store obs in the context once — stream.Apply, stream.FromCodec, and stream.Drain
	// all resolve it automatically when Options.Observer is nil.
	ctx := stats.WithObserver(context.Background(), obs)

	fmt.Println("═══════════════════════════════════════════════════════════════")
	fmt.Println("  stream-oee — governed OEE from reactive machine event stream")
	fmt.Println("═══════════════════════════════════════════════════════════════")

	// ── Governance registry ────────────────────────────────────────────────
	//
	// Register all six computation functions. Each function's SHA-256 hash
	// proves the approved formula. The Registry YAML is the governance record.
	reg := forge.NewRegistry("OEE Streaming Pipeline", "1.0.0").
		WithDescription("Governed OEE from machine event stream; window-based computation.").
		WithAuthor("OT Engineering").
		WithApproval("Quality Manager", "2024-06-01").
		WithObserver(obs) // forge.Registry uses explicit WithObserver — no context integration
	eventsToAvailIn.Register(reg)
	eventsToPerfIn.Register(reg)
	eventsToQual.Register(reg)
	availabilityCalc.Register(reg)
	performanceCalc.Register(reg)
	oeeCalc.Register(reg)
	computeOEEFromWindow.Register(reg)

	govYAML, err := pipelinerender.Render(reg.Spec())
	must(err, "pipelinerender.Render")
	fmt.Println("\n─── Governance spec (render/pipeline YAML — hashes prove approved formulas) ───")
	fmt.Println(string(govYAML))

	// ── Reactive stream pipeline ──────────────────────────────────────────
	//
	// Machine events arrive on rawCh (from MQTT in production, from goroutine here).
	// stream.Window collects them into time-bounded batches.
	// computeOEEFromWindow applies all six governed functions sequentially to each batch.
	const windowDuration = 10 * time.Millisecond // 10ms per window → simulates 1 production shift

	rawCh := make(chan []byte, 128)
	go generateMachineEvents(rawCh)

	// Source: decode raw MQTT bytes → typed MachineEvent stream
	events := stream.FromCodec(ctx, rawCh, format.JSON(machineEventCodec),
		stream.SourceOptions{Name: "machine-control-system"}) // observer from ctx

	// Window: collect events per time slot; emit []MachineEvent batches
	// Empty windows (between shifts) are filtered below.
	windows := stream.Window(ctx, events, windowDuration)

	// Filter: skip empty windows (no events → no OEE to compute)
	nonEmptyWindows := stream.Filter(ctx, windows, func(w []MachineEvent) bool {
		return len(w) > 0
	})

	// Apply: run all six governed forge functions on each window batch.
	// Returns Stream[OEEResult] — all intermediate values are now observable.
	results := stream.Apply(ctx, nonEmptyWindows, computeOEEFromWindow,
		stream.ApplyOptions{}) // observer from ctx

	// ── Tap into PARTIAL calculations ─────────────────────────────────────
	//
	// Tap is called for every OEEResult item WITHOUT transforming the stream.
	// Each Tap is independent — react to Availability, Performance, or Quality
	// thresholds before the final OEE alert threshold is evaluated.

	const (
		oeeTarget          = 0.65 // overall OEE alert threshold
		availabilityTarget = 0.80 // maintenance pre-alert (machine running time)
		qualityTarget      = 0.90 // quality team notification (defect rate)
	)

	fmt.Println("\n─── Reactive pipeline output — partial calculation Taps ─────────────────────")

	// Tap 1: Availability — observe uptime fraction per window.
	// Fires a maintenance pre-alert when availability drops below 80%,
	// even if the overall OEE hasn't crossed the 65% threshold yet.
	windowCount := 0
	results = stream.Tap(ctx, results, func(r OEEResult) {
		windowCount++
		fmt.Printf("  Window %d  Availability=%.1f%%  Performance=%.1f%%  Quality=%.1f%%  OEE=%.1f%%\n",
			windowCount,
			float64(r.Availability)*100,
			float64(r.Performance)*100,
			float64(r.Quality)*100,
			float64(r.OEE)*100,
		)
		if float64(r.Availability) < availabilityTarget {
			fmt.Printf("            → ⚠️  Availability %.1f%% < %.0f%% — maintenance pre-alert\n",
				float64(r.Availability)*100, availabilityTarget*100)
		}
	})

	// Tap 2: Quality — observe defect rate per window independently.
	// Quality issues may require immediate action (scrapping, rework) even when
	// OEE is still above the overall threshold.
	results = stream.Tap(ctx, results, func(r OEEResult) {
		if float64(r.Quality) < qualityTarget {
			fmt.Printf("            → ⚠️  Quality %.1f%% < %.0f%% — quality team notification\n",
				float64(r.Quality)*100, qualityTarget*100)
		}
	})

	// ── Tee: split into alert stream + metrics bus ─────────────────────────
	//
	// One copy goes to the OEE alert filter.
	// The other copy fans out via FlatMapSlice into individual component metrics
	// (the "metrics bus" pattern — one result → N named measurements).
	alertStream, metricsStream := stream.Tee(ctx, results)

	// ── Alert branch: filter below-target OEE → alert sink ────────────────
	belowTarget := stream.Filter(ctx, alertStream, func(r OEEResult) bool {
		return float64(r.OEE) < oeeTarget
	})
	alertsFired := 0
	alertDone := make(chan struct{})
	go func() {
		defer close(alertDone)
		stream.Drain(ctx, belowTarget,
			func(_ context.Context, r OEEResult) error {
				alertsFired++
				fmt.Printf("  🚨 OEE Alert: %.1f%% — dispatching maintenance notification\n",
					float64(r.OEE)*100)
				return nil
			},
			func(err error) { fmt.Println("  alert stream error:", err) },
			stream.DrainOptions{}, // observer from ctx
		)
	}()

	// ── Metrics branch: FlatMapSlice → individual component metrics ─────────
	//
	// FlatMapSlice expands one OEEResult into four named ComponentMetric values.
	// In production these would be published to a metrics bus (Prometheus, MQTT, MES).
	componentMetrics := stream.FlatMapSlice(ctx, metricsStream,
		func(r OEEResult) []ComponentMetric {
			return []ComponentMetric{
				{"availability", float64(r.Availability)},
				{"performance", float64(r.Performance)},
				{"quality", float64(r.Quality)},
				{"oee", float64(r.OEE)},
			}
		},
	)
	metricCount := 0
	metricDone := make(chan struct{})
	go func() {
		defer close(metricDone)
		stream.Drain(ctx, componentMetrics,
			func(_ context.Context, m ComponentMetric) error {
				metricCount++
				fmt.Printf("  📊 metric: %-14s = %.1f%%\n", m.Name, m.Value*100)
				return nil
			},
			func(err error) { fmt.Println("  metrics stream error:", err) },
			stream.DrainOptions{},
		)
	}()

	<-alertDone
	<-metricDone
	fmt.Printf("\n  %d alert(s) fired, %d component metrics published across %d windows\n",
		alertsFired, metricCount, windowCount)
	fmt.Printf("  %d alert(s) fired across %d production windows\n", alertsFired, windowCount)

	// ── Stream topology YAML ──────────────────────────────────────────────
	//
	// Topology documents the streaming wiring. Governance detail (hashes, authors)
	// is in the pipeline YAML above.
	topo := stream.NewTopology("OEE Machine Event Stream", "1.0.0").
		WithDescription("Governed OEE from machine event stream; window-based computation with partial Tap observers.").
		WithSource("machine-control-system", "Machine events (MQTT / ERP / PLC)").
		WithBuffer("tumbling window — non-empty batches only")
	stream.WithApply(topo, computeOEEFromWindow) // → OEEResult{Avail, Perf, Qual, OEE}
	topo.
		WithTap(fmt.Sprintf("Availability < %.0f%% → maintenance pre-alert", availabilityTarget*100)).
		WithTap(fmt.Sprintf("Quality < %.0f%% → quality team notification", qualityTarget*100)).
		WithFilter(fmt.Sprintf("oee < %.0f%% (OEE alert threshold)", oeeTarget*100)).
		WithSink("maintenance-alert", "Maintenance notification (MQTT / webhook / MES)").
		WithSink("metrics-bus", "Per-component metrics (Prometheus / MQTT / MES)")

	topoYAML, err := streamrender.Render(topo.Spec())
	must(err, "streamrender.Render")
	fmt.Println("\n─── Stream topology YAML (render/stream — architecture view) ────────────────")
	fmt.Println(string(topoYAML))
}

func must(err error, msg string) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL %s: %v\n", msg, err)
		os.Exit(1)
	}
}
