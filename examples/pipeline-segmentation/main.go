// Package pipeline_segmentation demonstrates PipePort[T] as a computation
// pipeline stage boundary — the primary use case. Three named stages
// (raw → valid → enriched) are connected; side observers tap into any
// stage without touching the pipeline logic.
//
// Architecture:
//
//	SourcePort(sensors) ─→ PipePort("raw") ─Chain(validate)─→ PipePort("valid") ─sub-pipeline(3 stream.Map steps)─→ PipePort("enriched")
//	                             ↑ Tap(log)                        ↑ Tap(alerting) ↑ Tap(history)
//
// PipePort is the segmentation glue BETWEEN computation stages.
// The raw→valid transition uses ports.Chain (the one-function convenience).
// The valid→calibrated transition shows the general case: a multi-step
// sub-pipeline built directly with the stream module's OWN composition
// feature — chaining gstream.Map calls — then handed off into the next
// PipePort with the same Stream()/Push() primitives Chain itself wraps.
// Use this shape whenever a stage boundary needs more than one computation
// step. IO/adapter wiring (InputPort/OutputPort with transport adapters) is
// a supported convenience, not the primary use.
//
// The topology is modular at two levels, both plain ctx-scoped functions:
//
//   - buildCalibrationStage(ctx, in, out) is a STAGE builder — it wires one
//     multi-step sub-pipeline between two PipePorts, with the same
//     (ctx, from, to) shape as ports.Chain, so it slots into the pipeline
//     exactly like Chain does.
//   - BuildPipeline(ctx) is the PIPELINE builder — it wires the whole
//     topology by composing ports.Chain calls and stage builders like
//     buildCalibrationStage, adds observers, and starts every pipe.
//
// The entire topology — every PipePort, every stage transition, every side
// observer, every Connect — is declared once in [BuildPipeline] and never
// touched again. main is a caller: it applies the already-declared pipeline
// to one context, then feeds data in and reads results out. This mirrors
// the "declare once, apply anywhere" shape used throughout go-codex for
// REST routes, event channels, and forge functions — a pipeline topology,
// and any sub-pipeline within it, is exactly as declarative and composable
// as any other boundary.
//
// Build: go build .
// Run:   ./pipeline-segmentation
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/ports"
	gstream "github.com/DaniDeer/go-codex/stream"
)

// ── Domain types and codecs ──────────────────────────────────────────────────

// SensorReading arrives raw from hardware.
type SensorReading struct {
	SensorID string
	DegreesC float64
}

// ValidatedReading has passed basic sanity checks.
type ValidatedReading struct {
	SensorID string
	DegreesC float64
}

// CalibratedReading is the output of a 3-step stream sub-pipeline: calibrate
// (correct for sensor offset), classify (assign a severity Status), and
// annotate (produce a human-readable Message) — see main's Valid→Calibrated
// wiring below, built with three chained gstream.Map calls.
type CalibratedReading struct {
	SensorID    string
	DegreesC    float64 // original raw value
	CalibratedC float64 // corrected value
	Unit        string  // always "°C"
	Status      string  // "normal" | "warning" | "critical" — set by classifyReading
	Message     string  // human-readable summary — set by annotateReading
}

var nonEmpty = codex.Constraint[string]{
	Name:    "non_empty",
	Check:   func(s string) bool { return s != "" },
	Message: func(s string) string { return "expected non-empty string" },
}

var readingCodec = codex.Struct[SensorReading](
	codex.RequiredField("sensor_id", codex.String().Refine(nonEmpty),
		func(r SensorReading) string { return r.SensorID },
		func(r *SensorReading, v string) { r.SensorID = v },
	),
	codex.RequiredField("degrees_c", codex.Float64(),
		func(r SensorReading) float64 { return r.DegreesC },
		func(r *SensorReading, v float64) { r.DegreesC = v },
	),
)

var degreesCCodec = codex.Float64().Refine(codex.Constraint[float64]{
	Name:    "valid_range",
	Check:   func(d float64) bool { return d >= -50 && d <= 150 },
	Message: func(d float64) string { return fmt.Sprintf("%.1f°C out of range [-50, 150]", d) },
})

var validCodec = codex.Struct[ValidatedReading](
	codex.RequiredField("sensor_id", codex.String().Refine(nonEmpty),
		func(v ValidatedReading) string { return v.SensorID },
		func(v *ValidatedReading, s string) { v.SensorID = s },
	),
	codex.RequiredField("degrees_c", degreesCCodec,
		func(v ValidatedReading) float64 { return v.DegreesC },
		func(v *ValidatedReading, d float64) { v.DegreesC = d },
	),
)

var statusCodec = codex.String().Refine(codex.Constraint[string]{
	Name:    "known_status",
	Check:   func(s string) bool { return s == "normal" || s == "warning" || s == "critical" },
	Message: func(s string) string { return fmt.Sprintf("unknown status %q", s) },
})

var calibratedCodec = codex.Struct[CalibratedReading](
	codex.RequiredField("sensor_id", codex.String().Refine(nonEmpty),
		func(c CalibratedReading) string { return c.SensorID },
		func(c *CalibratedReading, v string) { c.SensorID = v },
	),
	codex.RequiredField("degrees_c", codex.Float64(),
		func(c CalibratedReading) float64 { return c.DegreesC },
		func(c *CalibratedReading, v float64) { c.DegreesC = v },
	),
	codex.RequiredField("calibrated_c", codex.Float64(),
		func(c CalibratedReading) float64 { return c.CalibratedC },
		func(c *CalibratedReading, v float64) { c.CalibratedC = v },
	),
	codex.RequiredField("unit", codex.String().Refine(nonEmpty),
		func(c CalibratedReading) string { return c.Unit },
		func(c *CalibratedReading, v string) { c.Unit = v },
	),
	codex.RequiredField("status", statusCodec,
		func(c CalibratedReading) string { return c.Status },
		func(c *CalibratedReading, v string) { c.Status = v },
	),
	codex.RequiredField("message", codex.String().Refine(nonEmpty),
		func(c CalibratedReading) string { return c.Message },
		func(c *CalibratedReading, v string) { c.Message = v },
	),
)

// ── Pipeline stages (PipePort waypoints) ─────────────────────────────────────
//
// Declared as package vars because constructing a PipePort has no side
// effects — same as any codec or Route declaration elsewhere in go-codex.
// Wiring stages together (Chain, Connect) is different: it starts
// goroutines and needs a ctx, so it stays a function ([BuildPipeline]),
// called once from main — the same split examples/sensor-service uses
// between its port/codec declarations and its ctx-parameterized
// pipeline.Build(ctx, ...) wiring function.

var (
	// Stage 1: raw readings arrive here.
	Raw = must(ports.NewPipePort[SensorReading]("raw", readingCodec, ports.PortOptions{Buffer: 8}))

	// Stage 2: validated readings (range check passed).
	Valid = must(ports.NewPipePort[ValidatedReading]("valid", validCodec, ports.PortOptions{Buffer: 8}))

	// Stage 3: calibrated readings (corrected with offset).
	Calibrated = must(ports.NewPipePort[CalibratedReading]("calibrated", calibratedCodec, ports.PortOptions{Buffer: 8}))
)

// ── Computation stages (between PipePorts) ───────────────────────────────────

func validateReading(r SensorReading) (ValidatedReading, error) {
	return ValidatedReading(r), nil
}

// The valid→calibrated stage transition is a 3-step sub-pipeline: each
// function below does ONE thing, is independently unit-testable, and is
// wired as a separate gstream.Map stage in main (not composed by hand into
// one function) — see the "Valid → Calibrated" section in main for how the
// stream module's own Map chaining builds the sub-pipeline.

// Step 1: calibrateReading corrects the raw value by a known sensor offset.
func calibrateReading(v ValidatedReading) (CalibratedReading, error) {
	const offset = 1.2 // known per-sensor offset; production code would look it up
	return CalibratedReading{
		SensorID:    v.SensorID,
		DegreesC:    v.DegreesC,
		CalibratedC: v.DegreesC + offset,
		Unit:        "°C",
	}, nil
}

// Step 2: classifyReading assigns a severity Status from the calibrated value.
func classifyReading(c CalibratedReading) (CalibratedReading, error) {
	switch {
	case c.CalibratedC >= 30:
		c.Status = "critical"
	case c.CalibratedC >= 25:
		c.Status = "warning"
	default:
		c.Status = "normal"
	}
	return c, nil
}

// Step 3: annotateReading produces a human-readable summary Message.
func annotateReading(c CalibratedReading) (CalibratedReading, error) {
	c.Message = fmt.Sprintf("%s: %.1f°C [%s]", c.SensorID, c.CalibratedC, c.Status)
	return c, nil
}

// ── Pipeline (declared once, applied by main) ────────────────────────────────
//
// Two levels of builder function, both taking ctx + the PipePorts they
// connect and returning nothing but their wiring effect:
//
//   - buildCalibrationStage(ctx, in, out) wires ONE stage transition — a
//     3-step sub-pipeline. It has the exact same shape as [ports.Chain]
//     (ctx, from, ..., to) but is hand-written because the transition needs
//     more than one Map. Nothing about it is BuildPipeline-specific: it
//     could live in its own file, package, or be unit-tested in isolation
//     by wiring it between two throwaway PipePorts.
//   - BuildPipeline(ctx) wires the WHOLE topology by calling ports.Chain for
//     simple transitions and buildCalibrationStage for the complex one,
//     then adds observers and starts every pipe. It composes stage
//     builders the same way stage builders compose plain functions.
//
// This mirrors examples/sensor-service's own layering: small, ctx-scoped
// builder functions assembled by one top-level Build/BuildPipeline function,
// never one monolithic wiring block.

// buildCalibrationStage wires the Valid → Calibrated sub-pipeline: three
// sequential gstream.Map steps (calibrate → classify → annotate), then
// hands the result to out via gstream.Drain + Push — the same primitives
// [ports.Chain] uses internally, assembled by hand because Chain only
// wraps a single Map. Each step function stays independently unit-testable;
// this function only owns the WIRING between them.
//
// Call before in's Connect (it registers a Stream consumer); out's Connect
// may happen before or after (Push buffers either way) — the same ordering
// rule ports.Chain follows.
func buildCalibrationStage(ctx context.Context, in *ports.PipePort[ValidatedReading], out *ports.PipePort[CalibratedReading]) {
	calibrationStage := gstream.Map(ctx, in.Stream(ctx), calibrateReading, gstream.MapOptions{})
	classifiedStage := gstream.Map(ctx, calibrationStage, classifyReading, gstream.MapOptions{})
	annotatedStage := gstream.Map(ctx, classifiedStage, annotateReading, gstream.MapOptions{})
	go gstream.Drain(ctx, annotatedStage, func(_ context.Context, v CalibratedReading) error {
		return out.Push(ctx, v)
	}, nil, gstream.DrainOptions{})
}

// PipelineIO is everything a caller needs to drive an already-wired
// pipeline: one channel to feed input, and one channel per side observer to
// read results from. BuildPipeline is the only place that knows how many
// stages exist, how they are chained, or which observers are attached —
// main (or any other caller) only ever sees this shape.
type PipelineIO struct {
	Feed     chan<- SensorReading     // write end: submit raw readings here
	Log      <-chan SensorReading     // side observer: raw readings, unmodified
	Alerting <-chan CalibratedReading // side observer #1 on the final stage
	History  <-chan CalibratedReading // side observer #2 on the final stage
}

// BuildPipeline declares and starts the ENTIRE pipeline topology: every
// PipePort, every stage transition, every side observer, and every Connect
// call. Nothing about the topology is decided by the caller — main only
// supplies ctx and receives back the channels in PipelineIO. This is the
// same "declare once, apply anywhere" shape as a REST route or
// forge.Function: the wiring is written once, here, and reused by calling
// BuildPipeline(ctx) — never by copying the wiring code itself.
func BuildPipeline(ctx context.Context) PipelineIO {
	// ── Raw → Valid: single-function stage transition ─────────────────────
	//
	// ports.Chain wraps Stream+Map+Drain+Push — the boilerplate of wiring
	// one PipePort's output through a transform into the next PipePort's
	// input. Must be called before the UPSTREAM pipe's Connect (registers a
	// Stream consumer); can be called before or after the DOWNSTREAM pipe's
	// Connect (Push buffers either way).
	ports.Chain(ctx, Raw, validateReading, Valid)

	// ── Valid → Calibrated: multi-step sub-pipeline ───────────────────────
	//
	// This transition needs three sequential computations, so its wiring is
	// factored into its own builder function, buildCalibrationStage, called
	// here with the SAME (ctx, from, to) shape as ports.Chain above — a
	// sub-pipeline slots into BuildPipeline exactly like a single-function
	// stage does.
	buildCalibrationStage(ctx, Valid, Calibrated)

	// ── Side observers: tap into any stage ────────────────────────────────
	//
	// These are INDEPENDENT of the stage wiring above. Add or remove
	// observers without touching either builder function.

	rawObserved := make(chan SensorReading, 8)
	Raw.OutputPort("log").Bind(ctx, ports.ChanSinkAdapter(rawObserved))

	calibratedCh1 := make(chan CalibratedReading, 8)
	calibratedCh2 := make(chan CalibratedReading, 8)
	Calibrated.OutputPort("alerting").Bind(ctx, ports.ChanSinkAdapter(calibratedCh1))
	Calibrated.OutputPort("history").Bind(ctx, ports.ChanSinkAdapter(calibratedCh2))

	// ── Bind source adapter to first stage ────────────────────────────────
	srcCh := make(chan SensorReading, 8)
	Raw.InputPort("in").Bind(ctx, ports.ChanSourceAdapter(srcCh))

	// ── Connect all pipes (starts hub goroutines; order does not matter) ──
	Raw.Connect(ctx)
	Valid.Connect(ctx)
	Calibrated.Connect(ctx)

	return PipelineIO{
		Feed:     srcCh,
		Log:      rawObserved,
		Alerting: calibratedCh1,
		History:  calibratedCh2,
	}
}

// ── Main: apply the pipeline ─────────────────────────────────────────────────
//
// main knows nothing about stages, Chain, gstream.Map, or observers — it
// only calls BuildPipeline once, then feeds input and reads results through
// the returned PipelineIO. This is deliberately the entire "how do I use
// this pipeline" surface.

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pipe := BuildPipeline(ctx)

	pipe.Feed <- SensorReading{SensorID: "sensor-1", DegreesC: 22.5}
	pipe.Feed <- SensorReading{SensorID: "sensor-2", DegreesC: 23.0}
	close(pipe.Feed)

	nRaw := 0
	for range pipe.Log {
		nRaw++
		if nRaw == 2 {
			break
		}
	}

	c1a := <-pipe.Alerting
	c1b := <-pipe.Alerting
	c2a := <-pipe.History
	c2b := <-pipe.History

	fmt.Printf("alerting:  %s [%s] %s\n", c1a.SensorID, c1a.Status, c1a.Message)
	fmt.Printf("alerting:  %s [%s] %s\n", c1b.SensorID, c1b.Status, c1b.Message)
	fmt.Printf("history:   %s [%s] %s\n", c2a.SensorID, c2a.Status, c2a.Message)
	fmt.Printf("history:   %s [%s] %s\n", c2b.SensorID, c2b.Status, c2b.Message)

	// sensor-1: 22.5 + 1.2 = 23.7°C → normal
	if c1a.CalibratedC != c1a.DegreesC+1.2 || c1a.Status != "normal" {
		fmt.Printf("FAIL: expected calibrated=%.1f status=normal, got calibrated=%.1f status=%s\n",
			c1a.DegreesC+1.2, c1a.CalibratedC, c1a.Status)
		os.Exit(1)
	}
	// sensor-2: 23.0 + 1.2 = 24.2°C → normal
	if c2a.CalibratedC != c2a.DegreesC+1.2 || c2a.Status != "normal" {
		fmt.Printf("FAIL: expected calibrated=%.1f status=normal, got calibrated=%.1f status=%s\n",
			c2a.DegreesC+1.2, c2a.CalibratedC, c2a.Status)
		os.Exit(1)
	}
	if c1a.Message == "" || c2a.Message == "" {
		fmt.Println("FAIL: expected non-empty Message from annotateReading")
		os.Exit(1)
	}

	fmt.Println("OK: 3-stage computation pipeline segmentation (with a 3-step stream.Map sub-pipeline) completed")
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
