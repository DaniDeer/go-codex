// Package forge-collection demonstrates forge collection operations applied to
// a batch of MQTT-style sensor temperature readings.
//
// Use case: temperature sensors publish periodic readings. Our service receives
// batches of raw readings per sensor, filters out warm-up readings, maps each
// reading to a validated Celsius domain value, then reduces the batch to a
// summary (count, min, max, average). A final MapValues step processes all
// sensors at once.
//
// forge collection operations used:
//
//	forge.Filter   — discard readings from the sensor warm-up phase
//	forge.Map      — convert each RawReading to a validated Celsius value
//	forge.Reduce   — fold []Celsius → BatchSummary{Count, Min, Max, Avg}
//	forge.MapValues — apply the scalar pipeline per-key over map[string][]RawReading
//
// All four return *forge.Function, making them composable, registerable in a
// Registry, and fully observable. The pipeline YAML spec shows kind/wraps fields.
//
// Run with: go run ./examples/forge-collection
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"regexp"
	"sort"
	"time"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/forge"
	"github.com/DaniDeer/go-codex/render/pipeline"
	"github.com/DaniDeer/go-codex/stats"
	"github.com/DaniDeer/go-codex/validate"
)

// sensorIDPattern matches sensor map keys of the form <sensor>-<digits>, e.g. "sensor-01".
var sensorIDPattern = regexp.MustCompile(`^[a-z]+-\d+$`)

// ─────────────────────────────────────────────────────────────────────────────
// Layer 1 — Domain types
// ─────────────────────────────────────────────────────────────────────────────

// RawReading is the wire-level temperature measurement from a sensor.
type RawReading struct {
	RawCelsius float64 // raw ADC-to-Celsius conversion, may include warm-up noise
	WarmUp     bool    // true during the first seconds after sensor power-on
}

// Celsius is a validated domain temperature. Accepted range: -50 … 150 °C.
type Celsius float64

// BatchSummary holds aggregated statistics for one sensor's reading batch.
type BatchSummary struct {
	Count int
	Min   float64
	Max   float64
	Avg   float64
}

// ─────────────────────────────────────────────────────────────────────────────
// Layer 1 — Codecs
// ─────────────────────────────────────────────────────────────────────────────

var rawReadingCodec = codex.Struct[RawReading](
	codex.RequiredField("rawCelsius", codex.Float64(),
		func(r RawReading) float64 { return r.RawCelsius },
		func(r *RawReading, v float64) { r.RawCelsius = v },
	),
	codex.RequiredField("warmUp", codex.Bool(),
		func(r RawReading) bool { return r.WarmUp },
		func(r *RawReading, v bool) { r.WarmUp = v },
	),
)

var celsiusCodec = codex.Float64().
	Refine(validate.RangeFloat(-50, 150)).
	WithTitle("Celsius").
	WithDescription("Validated temperature in Celsius [-50, 150].")

var batchSummaryCodec = codex.Struct[BatchSummary](
	codex.RequiredField("count", codex.Int(),
		func(b BatchSummary) int { return b.Count },
		func(b *BatchSummary, v int) { b.Count = v },
	),
	codex.RequiredField("min", codex.Float64(),
		func(b BatchSummary) float64 { return b.Min },
		func(b *BatchSummary, v float64) { b.Min = v },
	),
	codex.RequiredField("max", codex.Float64(),
		func(b BatchSummary) float64 { return b.Max },
		func(b *BatchSummary, v float64) { b.Max = v },
	),
	codex.RequiredField("avg", codex.Float64(),
		func(b BatchSummary) float64 { return b.Avg },
		func(b *BatchSummary, v float64) { b.Avg = v },
	),
)

// sensorIDCodec validates sensor ID keys of the form <sensor>-<id>, e.g. "sensor-01".
// Used by forge.MapValuesK in buildPerSensorSummary to enforce the key format.
var sensorIDCodec = codex.String().
	Refine(validate.Pattern(sensorIDPattern)).
	WithTitle("SensorID").
	WithDescription("Sensor map key of the form <sensor>-<digits>, e.g. sensor-01.")

// ─────────────────────────────────────────────────────────────────────────────
// Layer 2 — MQTT channel contract (api/events)
//
// The events builder documents the wire contract and generates an AsyncAPI spec.
// No real broker is needed for this example — the channel is simulated in-memory.
// ─────────────────────────────────────────────────────────────────────────────

func buildEventChannel() *events.ChannelHandle[RawReading] {
	b := events.NewBuilder(
		events.Info{
			Title:       "SensorBatchPipeline",
			Version:     "1.0.0",
			Description: "Temperature sensor measurement pipeline.",
		},
		events.WithTopicConstraints(validate.MQTTPublishTopic),
	)
	ch, err := events.NewChannel[RawReading]("sensors/{sensorId}/readings",
		rawReadingCodec,
		events.ChannelMeta{Description: "Raw temperature readings from field sensors."},
		events.Subscribe{Summary: "Receive raw temperature readings"},
	).Register(b)
	if err != nil {
		fmt.Fprintf(os.Stderr, "events.AddChannel: %v\n", err)
		os.Exit(1)
	}
	return ch
}

// ─────────────────────────────────────────────────────────────────────────────
// Layer 3 — forge pipeline
//
// Four collection operations, each returning a *forge.Function registerable
// in a Registry and emitting Kind/Wraps fields in the pipeline YAML spec.
// ─────────────────────────────────────────────────────────────────────────────

// rawToCelsius converts one RawReading to a validated Celsius value.
// This is the scalar Function lifted by forge.Map below.
func buildRawToCelsius() *forge.Function[RawReading, Celsius] {
	fn := forge.NewFunction(
		"rawToCelsius", "1.0.0",
		rawReadingCodec,
		codex.MapCodecSafe(celsiusCodec,
			func(c float64) Celsius { return Celsius(c) },
			func(c Celsius) (float64, error) { return float64(c), nil },
		),
		func(r RawReading) (Celsius, error) {
			return Celsius(r.RawCelsius), nil
		},
		forge.FunctionMeta{Description: "Maps a raw sensor reading to a validated Celsius value.", Author: "Sensor Engineering"},
	)
	return fn
}

func buildFilterWarmUp(rawCodec codex.Codec[RawReading]) *forge.Function[[]RawReading, []RawReading] {
	return forge.Filter(
		"filterWarmUp", "1.0.0",
		rawCodec,
		func(r RawReading) bool { return !r.WarmUp },
		forge.FunctionMeta{Description: "Discards readings taken during sensor warm-up phase.", Author: "Sensor Engineering"},
	)
}

func buildMapToCelsius(rawToCelsius *forge.Function[RawReading, Celsius]) *forge.Function[[]RawReading, []Celsius] {
	return forge.Map(
		"mapToCelsius", "1.0.0",
		rawToCelsius,
		forge.FunctionMeta{Description: "Applies rawToCelsius over a batch of readings."},
		forge.WithRefinement(func(readings []RawReading) error {
			if len(readings) == 0 {
				return fmt.Errorf("batch must contain at least one reading after warm-up filter")
			}
			return nil
		}),
	)
}

func buildReduceSummary() *forge.Function[[]Celsius, BatchSummary] {
	return forge.Reduce(
		"reduceSummary", "1.0.0",
		codex.MapCodecSafe(celsiusCodec,
			func(c float64) Celsius { return Celsius(c) },
			func(c Celsius) (float64, error) { return float64(c), nil },
		),
		batchSummaryCodec,
		BatchSummary{Min: math.MaxFloat64, Max: -math.MaxFloat64},
		func(acc BatchSummary, c Celsius) BatchSummary {
			v := float64(c)
			acc.Count++
			if v < acc.Min {
				acc.Min = v
			}
			if v > acc.Max {
				acc.Max = v
			}
			acc.Avg += (v - acc.Avg) / float64(acc.Count) // incremental mean
			return acc
		},
		forge.FunctionMeta{Description: "Reduces a []Celsius batch to a BatchSummary (count, min, max, avg).", Author: "Analytics Team", ApprovedBy: "Data Engineering Lead", ApprovedAt: "2024-06-01"},
	)
}

// buildPerSensorSummary composes Filter → Map → Reduce into a Compose chain,
// then lifts it with MapValuesK to process all sensors at once with validated keys.
//
// forge.MapValuesK attaches sensorIDCodec to the map input — every key must match
// <sensor>-<digits> before any value is processed. Invalid keys surface as
// InputError → KeyError without processing any values (fail-fast).
func buildPerSensorSummary(
	mapFn *forge.Function[[]RawReading, []Celsius],
	reduceFn *forge.Function[[]Celsius, BatchSummary],
) *forge.Function[map[string][]RawReading, map[string]BatchSummary] {
	// Compose mapToCelsius → reduceSummary: []RawReading → BatchSummary
	singleSensorPipeline := forge.Compose(
		"singleSensorPipeline", "1.0.0",
		mapFn, reduceFn,
		forge.FunctionMeta{Description: "Full pipeline for one sensor: map readings to Celsius, then summarise."},
	)

	// MapValuesK: apply singleSensorPipeline over map[string][]RawReading,
	// validating every key against sensorIDCodec (<sensor>-<digits> pattern).
	return forge.MapValuesK(
		"perSensorSummary", "1.0.0",
		sensorIDCodec,
		singleSensorPipeline,
		forge.FunctionMeta{Description: "Applies the single-sensor pipeline to every validated sensor key.", Author: "Analytics Team"},
	)
}

// ─────────────────────────────────────────────────────────────────────────────
// Observer — collects stats across codec and forge layers
// ─────────────────────────────────────────────────────────────────────────────

type ChainObserver struct {
	codecErrors  map[string]int    // constraint name → count
	applyResults map[string][2]int // fn name → [ok, fail]
	applyDurs    map[string][]time.Duration
}

func newChainObserver() *ChainObserver {
	return &ChainObserver{
		codecErrors:  make(map[string]int),
		applyResults: make(map[string][2]int),
		applyDurs:    make(map[string][]time.Duration),
	}
}

func (o *ChainObserver) RecordValidationError(_, constraint, _ string) {
	if constraint == "" {
		constraint = "unknown"
	}
	o.codecErrors[constraint]++
}

func (o *ChainObserver) RecordApply(name, _ string, success bool, dur time.Duration) {
	counts := o.applyResults[name]
	if success {
		counts[0]++
	} else {
		counts[1]++
	}
	o.applyResults[name] = counts
	o.applyDurs[name] = append(o.applyDurs[name], dur)
}

func (o *ChainObserver) Summary() {
	fmt.Println("\n══════════════════════════════════════════════")
	fmt.Println("  Chain Observer Summary")
	fmt.Println("══════════════════════════════════════════════")
	printCodecErrors(o.codecErrors)
	printApplyResults(o.applyResults, o.applyDurs)
	fmt.Println("══════════════════════════════════════════════")
}

// observerSnapshot captures a point-in-time copy of observer counters.
type observerSnapshot struct {
	codecErrors  map[string]int
	applyResults map[string][2]int
	applyDurs    map[string][]time.Duration
}

// snapshot returns a deep copy of the current observer state for delta reporting.
func (o *ChainObserver) snapshot() observerSnapshot {
	ce := make(map[string]int, len(o.codecErrors))
	for k, v := range o.codecErrors {
		ce[k] = v
	}
	ar := make(map[string][2]int, len(o.applyResults))
	for k, v := range o.applyResults {
		ar[k] = v
	}
	ad := make(map[string][]time.Duration, len(o.applyDurs))
	for k, v := range o.applyDurs {
		cp := make([]time.Duration, len(v))
		copy(cp, v)
		ad[k] = cp
	}
	return observerSnapshot{codecErrors: ce, applyResults: ar, applyDurs: ad}
}

// SummaryDelta prints only the observer activity that occurred after snap was taken.
func (o *ChainObserver) SummaryDelta(label string, snap observerSnapshot) {
	deltaErrors := make(map[string]int)
	for k, v := range o.codecErrors {
		if d := v - snap.codecErrors[k]; d > 0 {
			deltaErrors[k] = d
		}
	}
	deltaApply := make(map[string][2]int)
	deltaApplyDurs := make(map[string][]time.Duration)
	for k, v := range o.applyResults {
		prev := snap.applyResults[k]
		d := [2]int{v[0] - prev[0], v[1] - prev[1]}
		if d[0]+d[1] > 0 {
			deltaApply[k] = d
			prevLen := len(snap.applyDurs[k])
			deltaApplyDurs[k] = o.applyDurs[k][prevLen:]
		}
	}

	fmt.Printf("\n  ── Observer delta: %s ──\n", label)
	printCodecErrors(deltaErrors)
	printApplyResults(deltaApply, deltaApplyDurs)
}

func printCodecErrors(m map[string]int) {
	if len(m) == 0 {
		fmt.Println("  No codec validation errors.")
		return
	}
	fmt.Println("  Codec validation errors:")
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Printf("    %-30s %d\n", k, m[k])
	}
}

func printApplyResults(results map[string][2]int, durs map[string][]time.Duration) {
	if len(results) == 0 {
		return
	}
	fmt.Println("  Forge Apply calls:")
	names := make([]string, 0, len(results))
	for n := range results {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		counts := results[n]
		var avgDur time.Duration
		if ds := durs[n]; len(ds) > 0 {
			var total time.Duration
			for _, d := range ds {
				total += d
			}
			avgDur = total / time.Duration(len(ds))
		}
		fmt.Printf("    %-32s ok=%-3d fail=%-3d avg=%s\n",
			n, counts[0], counts[1], avgDur.Round(time.Microsecond))
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Main
// ─────────────────────────────────────────────────────────────────────────────

func main() {
	obs := newChainObserver()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// ── Build pipeline ──────────────────────────────────────────────────────
	rawToCelsius := buildRawToCelsius()
	filterWarmUp := buildFilterWarmUp(rawReadingCodec)
	mapToCelsius := buildMapToCelsius(rawToCelsius)
	reduceSummary := buildReduceSummary()
	perSensor := buildPerSensorSummary(mapToCelsius, reduceSummary)

	// ── Register all functions with an observer-wired registry ──────────────
	reg := forge.NewRegistry("SensorBatchPipeline", "1.0.0").
		WithDescription("Collection-based temperature sensor batch processing pipeline.").
		WithObserver(obs)
	rawToCelsius.Register(reg)
	filterWarmUp.Register(reg)
	mapToCelsius.Register(reg)
	reduceSummary.Register(reg)
	perSensor.Register(reg)

	// ── Layer 2: events channel (AsyncAPI contract) ─────────────────────────
	ch := buildEventChannel()
	_ = ch // In production: use adaptermqtt.SubscribeHandler with ch

	// ─────────────────────────────────────────────────────────────────────
	// Scenario 1: single-sensor happy path
	// Filter → Map → Reduce on one sensor's readings
	// ─────────────────────────────────────────────────────────────────────
	fmt.Println("── Scenario 1: single sensor (filter → map → reduce) ──")

	sensorReadings := []RawReading{
		{RawCelsius: 22.5, WarmUp: false},
		{RawCelsius: 23.1, WarmUp: false},
		{RawCelsius: 19.8, WarmUp: false},
		{RawCelsius: 25.0, WarmUp: true}, // warm-up — filtered out
		{RawCelsius: 21.3, WarmUp: false},
	}

	filtered, err := filterWarmUp.Apply(sensorReadings)
	if err != nil {
		fmt.Fprintf(os.Stderr, "filterWarmUp: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("  After filter: %d/%d readings kept\n", len(filtered), len(sensorReadings))

	mapped, err := mapToCelsius.Apply(filtered)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mapToCelsius: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("  After map: %d Celsius values\n", len(mapped))

	summary, err := reduceSummary.Apply(mapped)
	if err != nil {
		fmt.Fprintf(os.Stderr, "reduceSummary: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("  Summary: count=%d  min=%.1f°C  max=%.1f°C  avg=%.2f°C\n",
		summary.Count, summary.Min, summary.Max, summary.Avg)

	// ─────────────────────────────────────────────────────────────────────
	// Scenario 2: MapValues over multiple sensors (with validated keys)
	// ─────────────────────────────────────────────────────────────────────
	fmt.Println("\n── Scenario 2: multi-sensor MapValues (validated keys) ──")

	allSensors := map[string][]RawReading{
		"sensor-01": {
			{RawCelsius: 20.0, WarmUp: false},
			{RawCelsius: 21.5, WarmUp: false},
		},
		"sensor-02": {
			{RawCelsius: 35.0, WarmUp: true}, // warm-up, filtered
			{RawCelsius: 36.2, WarmUp: false},
			{RawCelsius: 37.8, WarmUp: false},
		},
		"sensor-03": {
			{RawCelsius: 100.0, WarmUp: false},
			{RawCelsius: 101.2, WarmUp: false},
		},
	}

	allSummaries, err := perSensor.Apply(allSensors)
	if err != nil {
		fmt.Fprintf(os.Stderr, "perSensor: %v\n", err)
		os.Exit(1)
	}

	sensorIDs := make([]string, 0, len(allSummaries))
	for id := range allSummaries {
		sensorIDs = append(sensorIDs, id)
	}
	sort.Strings(sensorIDs)
	for _, id := range sensorIDs {
		s := allSummaries[id]
		fmt.Printf("  %-12s count=%d  min=%.1f°C  max=%.1f°C  avg=%.2f°C\n",
			id, s.Count, s.Min, s.Max, s.Avg)
	}

	// ─────────────────────────────────────────────────────────────────────
	// Scenario 3: CollectionElementError — invalid element in batch
	// ─────────────────────────────────────────────────────────────────────
	fmt.Println("\n── Scenario 3: invalid element → CollectionElementError ──")

	invalidBatch := []RawReading{
		{RawCelsius: 22.5, WarmUp: false},
		{RawCelsius: 999.9, WarmUp: false}, // out of range [-50, 150]
	}
	// filterWarmUp passes (no warm-up flag set), so the invalid raw value
	// reaches mapToCelsius which calls rawToCelsius.Apply per element.
	filteredInvalid, _ := filterWarmUp.Apply(invalidBatch)
	_, err = mapToCelsius.Apply(filteredInvalid)
	if err != nil {
		var ce forge.CollectionElementError
		if errors.As(err, &ce) {
			fmt.Printf("  CollectionElementError: function=%q  index=%d\n", ce.Function, ce.Index)
			fmt.Printf("  Cause: %v\n", ce.Err)
		}
		// forge.CollectionElementError implements slog.LogValuer — logs function, index, and cause.
		// stats.ReportErrors walks the error tree; CollectionElementError wraps an OutputError
		// whose cause carries the ConstraintError — RecordValidationError fires for the element.
		logger.Error("element validation failed", slog.Any("error", err))
		stats.ReportErrors(obs, "batch", err)
	}

	// ─────────────────────────────────────────────────────────────────────
	// Scenario 4: WithRefinement on the Map — empty batch after filter
	// ─────────────────────────────────────────────────────────────────────
	fmt.Println("\n── Scenario 4: WithRefinement on Map — empty batch ──")

	allWarmUp := []RawReading{
		{RawCelsius: 25.0, WarmUp: true},
		{RawCelsius: 26.0, WarmUp: true},
	}
	filteredEmpty, _ := filterWarmUp.Apply(allWarmUp)
	_, err = mapToCelsius.Apply(filteredEmpty)
	if err != nil {
		var re forge.RefinementError
		if errors.As(err, &re) {
			fmt.Printf("  RefinementError: function=%q  cause=%v\n", re.Function, re.Err)
		}
	}

	// ─────────────────────────────────────────────────────────────────────
	// Scenario 5: key validation via forge.MapValuesK
	//
	// perSensorSummary was built with forge.MapValuesK + sensorIDCodec.
	// The codec enforces <sensor>-<digits> on every map key atomically —
	// one bad key rejects all input; no partial results are produced.
	//
	// Case A: only bad keys
	// Case B: one bad key mixed with valid keys — same fail-fast behaviour
	// ─────────────────────────────────────────────────────────────────────
	fmt.Println("\n── Scenario 5: MapValuesK key validation ──")

	handleKeyErr := func(label string, err error) {
		var inputErr forge.InputError
		var keyErr codex.KeyError
		if errors.As(err, &inputErr) && errors.As(inputErr.Err, &keyErr) {
			fmt.Printf("  [%s] Fail-fast: key %q rejected before pipeline ran\n", label, keyErr.Key)
			fmt.Printf("  [%s] Cause: %v\n", label, keyErr.Err)
		}
		logger.Error("sensor map key validation failed",
			slog.String("case", label),
			slog.Any("error", err),
		)
		stats.ReportErrors(obs, "badKey", err)
	}

	// Case A: only bad keys
	badKeySensors := map[string][]RawReading{
		"SENSOR_01": { // uppercase + underscore — violates sensorIDPattern
			{RawCelsius: 22.5, WarmUp: false},
		},
	}
	_, err = perSensor.Apply(badKeySensors)
	if err != nil {
		handleKeyErr("only bad keys", err)
	}

	// Case B: one bad key mixed with valid keys — still fail-fast, no partial output
	mixedSensors := map[string][]RawReading{
		"sensor-01": {
			{RawCelsius: 20.0, WarmUp: false},
		},
		"SENSOR_BAD": { // uppercase + underscore — fails sensorIDPattern
			{RawCelsius: 30.0, WarmUp: false},
		},
	}
	_, err = perSensor.Apply(mixedSensors)
	if err != nil {
		handleKeyErr("mixed keys", err)
	}

	// ─────────────────────────────────────────────────────────────────────
	// Scenario 6: codex.Map — standalone codec validation
	//
	// codex.Map[K, V] is a pure Codec[map[K]V] that validates both keys
	// (via keyCodec) and values (via valueCodec) during Decode/Encode.
	// It is not a pipeline Function — it does not compute; it only validates.
	//
	// Contrast with forge.MapValuesK:
	//   codex.Map       → Codec[map[K]V]  — validate/encode/decode a map value
	//   forge.MapValuesK → *Function[map[K]In, map[K]Out] — lift a pipeline fn
	//                       over map values; key validation is integrated
	//
	// Use codex.Map when:
	//   - you need a typed codec for a map field inside a struct or API body
	//   - you want Encode/Decode without any computation (no pipeline)
	//   - you need the JSON Schema (propertyNames + additionalProperties) for
	//     an OpenAPI or AsyncAPI spec
	// ─────────────────────────────────────────────────────────────────────
	fmt.Println("\n── Scenario 6: codex.Map — standalone codec validation ──")

	snap6 := obs.snapshot() // capture state before this scenario for delta reporting

	// allSensorsCodec validates map[string][]RawReading where every key must
	// match the sensorIDCodec pattern (<sensor>-<digits>).
	allSensorsCodec := codex.Map[string, []RawReading](
		sensorIDCodec,
		codex.SliceOf(rawReadingCodec),
	)

	// ── 6a: Happy path — Decode wire intermediate → typed map ───────────
	// Wire intermediate: as if unmarshalled from JSON.
	// codex.Map.Decode expects map[string]any; values are []any of map[string]any.
	wireValid := map[string]any{
		"sensor-01": []any{
			map[string]any{"rawCelsius": 20.0, "warmUp": false},
			map[string]any{"rawCelsius": 21.5, "warmUp": false},
		},
		"sensor-02": []any{
			map[string]any{"rawCelsius": 35.1, "warmUp": false},
		},
	}
	decoded, err := allSensorsCodec.Decode(wireValid)
	if err != nil {
		fmt.Fprintf(os.Stderr, "allSensorsCodec.Decode: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("  6a decoded: %d sensors\n", len(decoded))
	for _, id := range []string{"sensor-01", "sensor-02"} {
		fmt.Printf("      %-12s %d readings\n", id, len(decoded[id]))
	}

	// Re-Encode the typed map back to wire intermediate and pretty-print.
	encoded, err := allSensorsCodec.Encode(decoded)
	if err != nil {
		fmt.Fprintf(os.Stderr, "allSensorsCodec.Encode: %v\n", err)
		os.Exit(1)
	}
	reenc, _ := json.MarshalIndent(encoded, "  ", "  ")
	fmt.Printf("  6a re-encoded:\n  %s\n", reenc)

	// ── 6b: Bad key — key fails sensorIDPattern ──────────────────────────
	// codex.Map.Decode validates keys via keyCodec.Decode.
	// One bad key returns KeyError immediately (fail-fast, like MapValuesK).
	wireBadKey := map[string]any{
		"SENSOR_BAD": []any{ // uppercase + underscore — violates sensorIDPattern
			map[string]any{"rawCelsius": 22.5, "warmUp": false},
		},
	}
	_, err = allSensorsCodec.Decode(wireBadKey)
	if err != nil {
		var ke codex.KeyError
		if errors.As(err, &ke) {
			fmt.Printf("  6b bad key: key=%q  cause=%v\n", ke.Key, ke.Err)
		}
		stats.ReportErrors(obs, "allSensorsCodec", err)
	}

	// ── 6c: Bad value — a reading has a wrong-type field ─────────────────
	// The key is valid; the value fails rawReadingCodec (type mismatch).
	// The error is still surfaced as KeyError{Key, ...inner...}.
	wireBadValue := map[string]any{
		"sensor-01": []any{
			map[string]any{"rawCelsius": "not-a-number", "warmUp": false}, // string instead of float64
		},
	}
	_, err = allSensorsCodec.Decode(wireBadValue)
	if err != nil {
		var ke codex.KeyError
		if errors.As(err, &ke) {
			fmt.Printf("  6c bad value: key=%q  cause=%v\n", ke.Key, ke.Err)
		}
		stats.ReportErrors(obs, "allSensorsCodec", err)
	}

	obs.SummaryDelta("Scenario 6 (codex.Map)", snap6)

	// ── 6d: Schema — JSON Schema generated from codex.Map ────────────────
	// propertyNames enforces the key constraint; additionalProperties the value schema.
	schemaJSON, _ := json.MarshalIndent(allSensorsCodec.Schema, "  ", "  ")
	fmt.Printf("  6d schema:\n  %s\n", schemaJSON)

	// ─────────────────────────────────────────────────────────────────────
	// Pipeline YAML spec — shows kind/wraps for collection functions
	// ─────────────────────────────────────────────────────────────────────
	fmt.Println("\n── Pipeline YAML spec ──")
	spec, err := pipeline.Render(reg.Spec())
	if err != nil {
		fmt.Fprintf(os.Stderr, "pipeline.Render: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(string(spec))

	obs.Summary()
}
