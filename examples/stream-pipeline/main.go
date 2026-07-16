// Package stream-pipeline demonstrates the go-codex stream package across
// ten sections, each showcasing a different group of operators.
//
// # Domain
//
// Environmental monitoring station: temperature and humidity arrive on
// independent goroutine channels. No external dependencies — all sources
// are goroutines.
//
// Domain types: TempReading, HumidityReading, HeatIndexInput, HeatIndex.
//
// # Operators demonstrated
//
//   - Sources:      From, FromCodec
//   - Transforms:   Apply (forge.Function), Filter, Tap, MapErr, FlatMapSlice
//   - Fan-in/out:   Merge, Tee, CombineLatest2
//   - Routing:      Switch (named cases + rest), GroupBy (per-key sub-streams)
//   - Time:         Buffer, Window, Debounce, Throttle
//   - Sinks:        Drain, Collect
//   - Observer:     stats.StreamObserver (infrastructure metrics) + Tap (domain events) + stats.NewFanout
//   - Topology:     stream.NewTopology, stream.WithApply, render/stream.Render
//
// # Running
//
// go run ./examples/stream-pipeline
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/forge"
	"github.com/DaniDeer/go-codex/format"
	streamrender "github.com/DaniDeer/go-codex/render/stream"
	"github.com/DaniDeer/go-codex/stats"
	"github.com/DaniDeer/go-codex/stream"
	"github.com/DaniDeer/go-codex/validate"
)

// ── Domain types ──────────────────────────────────────────────────────────────

// TempReading is a temperature measurement from a sensor.
type TempReading struct {
	SensorID string
	Celsius  float64
}

// HumidityReading is a relative humidity measurement.
type HumidityReading struct {
	SensorID string
	Percent  float64
}

// HeatIndexInput combines latest temperature and humidity for the heat index formula.
type HeatIndexInput struct {
	Temp     float64 // °C
	Humidity float64 // %
}

// HeatIndex is the computed heat index with a human-readable level.
type HeatIndex struct {
	Score float64
	Level string // "comfortable", "caution", "danger"
}

// ── Layer 1: Codecs ───────────────────────────────────────────────────────────

var tempCodec = codex.Struct(
	codex.RequiredField("sensor_id",
		codex.String().Refine(validate.NonEmptyString),
		func(r TempReading) string { return r.SensorID },
		func(r *TempReading, v string) { r.SensorID = v }),
	codex.RequiredField("celsius",
		codex.Float64().Refine(validate.RangeFloat(-60, 60)),
		func(r TempReading) float64 { return r.Celsius },
		func(r *TempReading, v float64) { r.Celsius = v }),
)

var heatIndexInputCodec = codex.Struct(
	codex.RequiredField("temp",
		codex.Float64().WithTitle("temperature_celsius"),
		func(h HeatIndexInput) float64 { return h.Temp },
		func(h *HeatIndexInput, v float64) { h.Temp = v }),
	codex.RequiredField("humidity",
		codex.Float64().WithTitle("humidity_percent"),
		func(h HeatIndexInput) float64 { return h.Humidity },
		func(h *HeatIndexInput, v float64) { h.Humidity = v }),
)

var heatIndexCodec = codex.Struct(
	codex.RequiredField("score",
		codex.Float64().WithTitle("heat_index_score"),
		func(h HeatIndex) float64 { return h.Score },
		func(h *HeatIndex, v float64) { h.Score = v }),
	codex.RequiredField("level",
		codex.String().Refine(validate.OneOf("comfortable", "caution", "danger")),
		func(h HeatIndex) string { return h.Level },
		func(h *HeatIndex, v string) { h.Level = v }),
)

// ── Layer 1: Forge functions ──────────────────────────────────────────────────

// celsiusToFahrenheit converts a TempReading °C to °F — a simple, governed transformation.
var celsiusToFahrenheit = forge.NewFunction(
	"celsiusToFahrenheit", "1.0.0",
	tempCodec.WithTitle("temp_reading"),
	codex.Float64().WithTitle("fahrenheit"),
	func(r TempReading) (float64, error) { return r.Celsius*9/5 + 32, nil },
	forge.FunctionMeta{Description: "Convert TempReading Celsius to Fahrenheit.", Author: "environmental-team"},
)

// computeHeatIndex computes a simplified heat index from temperature and humidity.
var computeHeatIndex = forge.NewFunction(
	"computeHeatIndex", "1.0.0",
	heatIndexInputCodec.WithTitle("heat_index_input"),
	heatIndexCodec.WithTitle("heat_index"),
	func(in HeatIndexInput) (HeatIndex, error) {
		// Simplified apparent temperature formula
		score := in.Temp + 0.33*in.Humidity/100*6.105 - 4
		var level string
		switch {
		case score < 27:
			level = "comfortable"
		case score < 35:
			level = "caution"
		default:
			level = "danger"
		}
		return HeatIndex{Score: score, Level: level}, nil
	},
	forge.FunctionMeta{
		Description: "Compute heat index from temperature and humidity.",
		Author:      "environmental-team",
		ApprovedBy:  "Safety Officer",
		ApprovedAt:  "2024-06-01",
	},
)

// ── Layer 2: API declarations ─────────────────────────────────────────────────

// Stream topology — declared at package level as a living design document.
var envMonitorTopo = stream.NewTopology("Environmental Monitor", "1.0.0").
	WithDescription("Real-time temperature and humidity pipeline with heat index computation.").
	WithSource("temp-goroutine", "Temperature sensor (°C readings every 10ms)").
	WithSource("humidity-goroutine", "Humidity sensor (% readings every 10ms)")

// ── Helpers ───────────────────────────────────────────────────────────────────

// pumpTemps sends n temperature readings into ch then closes it.
func pumpTemps(ch chan<- TempReading, temps []float64) {
	for _, c := range temps {
		ch <- TempReading{SensorID: "env-1", Celsius: c}
	}
	close(ch)
}

// pumpHumidity sends n humidity readings into ch then closes it.
func pumpHumidity(ch chan<- HumidityReading, percents []float64) {
	for _, p := range percents {
		ch <- HumidityReading{SensorID: "env-1", Percent: p}
	}
	close(ch)
}

// ── main ──────────────────────────────────────────────────────────────────────

func main() {
	ctx := context.Background()

	fmt.Println("═══════════════════════════════════════════════════════════")
	fmt.Println("  stream-pipeline — go-codex stream operator showcase")
	fmt.Println("═══════════════════════════════════════════════════════════")

	// ── Section 1: Basic pipeline ─────────────────────────────────────────
	//
	// From → Apply(forge.Function) → Tap → Filter → Drain
	// Shows: the core reactive pipeline replacing a manual goroutine loop.
	fmt.Println("\n─── Section 1: Basic pipeline (From → Apply → Tap → Filter → Drain)")

	temps1 := make(chan TempReading, 6)
	go pumpTemps(temps1, []float64{20, 25, 30, 35, 40, 45})

	hotAlerts := 0
	stream.Drain(ctx,
		stream.Filter(ctx,
			stream.Tap(ctx,
				stream.Apply(ctx,
					stream.From(ctx, temps1),
					celsiusToFahrenheit,
					stream.ApplyOptions{},
				),
				func(f float64) {
					// Tap: domain event observation without transforming
					fmt.Printf("  tap: %.1f °F\n", f)
				},
			),
			func(f float64) bool { return f > 95 }, // >35°C
		),
		func(_ context.Context, f float64) error {
			hotAlerts++
			fmt.Printf("  🔴 alert: %.1f °F exceeds threshold\n", f)
			return nil
		},
		func(err error) { fmt.Println("  error:", err) },
		stream.DrainOptions{},
	)
	fmt.Printf("  → %d alert(s) fired\n", hotAlerts)

	// ── Section 2: Multi-source (CombineLatest2) ──────────────────────────
	//
	// CombineLatest2 merges the latest value from two independent streams
	// into a combined struct fed to a forge function.
	fmt.Println("\n─── Section 2: Multi-source (CombineLatest2 → Apply heat index)")

	tempCh2 := make(chan TempReading, 4)
	humCh2 := make(chan HumidityReading, 4)
	go pumpTemps(tempCh2, []float64{28, 32, 38, 42})
	go pumpHumidity(humCh2, []float64{40, 55, 70, 85})

	heatInputs := stream.CombineLatest2(ctx,
		stream.From(ctx, tempCh2),
		stream.From(ctx, humCh2),
		func(t TempReading, h HumidityReading) HeatIndexInput {
			return HeatIndexInput{Temp: t.Celsius, Humidity: h.Percent}
		},
	)
	stream.WithApply(envMonitorTopo, computeHeatIndex) // record in topology
	heatStream := stream.Apply(ctx, heatInputs, computeHeatIndex, stream.ApplyOptions{})
	heatVals, _ := stream.Collect(ctx, heatStream)
	for _, h := range heatVals {
		fmt.Printf("  heat index: %.1f → %s\n", h.Score, h.Level)
	}

	// ── Section 3: Fan-out (Tee) ──────────────────────────────────────────
	//
	// Tee splits one stream into two independent copies.
	// Both copies receive all items — backpressure on either blocks the other.
	fmt.Println("\n─── Section 3: Fan-out (Tee → archive + alert branches)")

	teeIn := make(chan TempReading, 5)
	go pumpTemps(teeIn, []float64{22, 28, 35, 41, 25})
	fahrenheitStream := stream.Apply(ctx, stream.From(ctx, teeIn), celsiusToFahrenheit, stream.ApplyOptions{})

	archiveBranch, alertBranch := stream.Tee(ctx, fahrenheitStream)
	archiveDone := make(chan []float64, 1)
	go func() {
		vals, _ := stream.Collect(ctx, archiveBranch)
		archiveDone <- vals
	}()
	alertVals, _ := stream.Collect(ctx, alertBranch)
	archive := <-archiveDone
	fmt.Printf("  archive received %d readings; alert branch: %d readings\n",
		len(archive), len(alertVals))

	// ── Section 4: Fan-in (Merge) ─────────────────────────────────────────
	//
	// Merge combines two streams of the same type into one.
	fmt.Println("\n─── Section 4: Fan-in (Merge two sensor sources)")

	sensorA := make(chan float64, 3)
	sensorB := make(chan float64, 3)
	sensorA <- 10
	sensorA <- 20
	sensorA <- 30
	close(sensorA)
	sensorB <- 100
	sensorB <- 200
	sensorB <- 300
	close(sensorB)

	merged := stream.Merge(ctx,
		stream.From(ctx, sensorA),
		stream.From(ctx, sensorB),
	)
	mergedVals, _ := stream.Collect(ctx, merged)
	fmt.Printf("  merged %d items from 2 sources (3+3)\n", len(mergedVals))

	// ── Section 5: One-to-many (FlatMapSlice) ─────────────────────────────
	//
	// FlatMapSlice expands each item into multiple output items.
	fmt.Println("\n─── Section 5: One-to-many (FlatMapSlice: °C → [°C, °F, K])")

	flatIn := make(chan TempReading, 3)
	go pumpTemps(flatIn, []float64{0, 100, 37})

	expanded := stream.FlatMapSlice(ctx, stream.From(ctx, flatIn),
		func(r TempReading) []string {
			c := r.Celsius
			return []string{
				fmt.Sprintf("%.1f°C", c),
				fmt.Sprintf("%.1f°F", c*9/5+32),
				fmt.Sprintf("%.1fK", c+273.15),
			}
		},
	)
	expVals, _ := stream.Collect(ctx, expanded)
	fmt.Printf("  3 readings → %d derived values: %v\n", len(expVals), expVals[:4])

	// ── Section 6: Time operators ─────────────────────────────────────────
	//
	// All four time operators compared side-by-side.
	fmt.Println("\n─── Section 6: Time operators (Debounce / Throttle / Buffer / Window)")

	// Source: 6 items arriving quickly
	makeQuickSource := func() stream.Stream[int] {
		ch := make(chan int, 6)
		for i := 1; i <= 6; i++ {
			ch <- i
		}
		close(ch)
		errCh := make(chan error)
		close(errCh)
		return stream.Stream[int]{Values: ch, Errors: errCh}
	}

	// Debounce: emits only the last value after a silence window
	debounced, _ := stream.Collect(ctx, stream.Debounce(ctx, makeQuickSource(), 5*time.Millisecond))
	fmt.Printf("  debounce(5ms): %d item(s) — last of burst: %v\n", len(debounced), debounced)

	// Throttle: at most one per interval
	throttled, _ := stream.Collect(ctx, stream.Throttle(ctx, makeQuickSource(), 1*time.Millisecond))
	fmt.Printf("  throttle(1ms): %d item(s) from burst of 6\n", len(throttled))

	// Buffer: collect up to 3 items OR 100ms silence → emit as batch
	buffered, _ := stream.Collect(ctx, stream.Buffer(ctx, makeQuickSource(), 3, 100*time.Millisecond))
	total := 0
	for _, b := range buffered {
		total += len(b)
	}
	fmt.Printf("  buffer(n=3): %d batch(es), %d total items\n", len(buffered), total)

	// Window: fixed-interval ticker (collect during each slot)
	windowCtx, windowCancel := context.WithTimeout(ctx, 30*time.Millisecond)
	defer windowCancel()
	windowed, _ := stream.Collect(windowCtx,
		stream.Window(windowCtx, makeQuickSource(), 10*time.Millisecond))
	windowTotal := 0
	for _, w := range windowed {
		windowTotal += len(w)
	}
	fmt.Printf("  window(10ms): %d slot(s), %d total items\n", len(windowed), windowTotal)

	// ── Section 7: Error handling (MapErr) ───────────────────────────────
	//
	// MapErr transforms errors: recover, reclassify, or silence them.
	fmt.Println("\n─── Section 7: Error handling (FromCodec bad payload → MapErr recovery)")

	rawCh := make(chan []byte, 3)
	rawCh <- mustMarshal(map[string]any{"sensor_id": "env-1", "celsius": 22.5})
	rawCh <- []byte(`not json`) // bad payload → StreamDecodeError
	rawCh <- mustMarshal(map[string]any{"sensor_id": "env-1", "celsius": 30.0})
	close(rawCh)

	decoded := stream.FromCodec(ctx, rawCh, format.JSON(tempCodec),
		stream.SourceOptions{Name: "sensor-feed"})

	// MapErr: silence decode errors, let value items through
	recovered := stream.MapErr(ctx, decoded, func(err error) (TempReading, bool, error) {
		var sde stream.StreamDecodeError
		if errors.As(err, &sde) {
			fmt.Printf("  silenced: %v\n", sde)
			return TempReading{}, false, nil // silence
		}
		return TempReading{}, false, err // re-emit unknown errors
	})
	goodVals, _ := stream.Collect(ctx, recovered)
	fmt.Printf("  %d valid readings after silencing 1 bad payload\n", len(goodVals))

	// ── Section 8: Observer pattern ──────────────────────────────────────
	//
	// Two observer kinds for stream pipelines:
	//
	// A. Infrastructure metrics — stats.StreamObserver.RecordStreamItem
	//    Fires inside stream.Apply for every item (success or failure).
	//    Carries: function name, success bool, duration.
	//    Pass via ApplyOptions.Observer.
	//
	// B. Domain event observation — stream.Tap
	//    Called with the typed value; no metrics, no boxing.
	//    Use for business logic reactions (dashboards, audit logs).
	//
	// Compose both via stats.NewFanout — one fanout value wired to all operators.
	fmt.Println("\n─── Section 8: Observer pattern (StreamObserver metrics + Tap domain events + NewFanout)")

	metrics := &streamMetrics{}
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	// stats.NewFanout composes metrics counting + slog logging into one observer.
	// Pass this single value to every operator that accepts an Observer.
	obs := stats.NewFanout(metrics, stats.NewLoggingObserver(logger))

	temps9 := make(chan TempReading, 4)
	go pumpTemps(temps9, []float64{18, 25, 37, 42})

	domainEvents := 0

	s9 := stream.Apply(ctx,
		// A. Tap: domain event observation — typed business values
		stream.Tap(ctx,
			stream.From(ctx, temps9),
			func(r TempReading) {
				domainEvents++
				// In production: update a real-time dashboard, publish to an audit log, etc.
				_ = fmt.Sprintf("domain event: sensor %s = %.1f°C", r.SensorID, r.Celsius)
			},
		),
		celsiusToFahrenheit,
		// B. ApplyOptions.Observer: infrastructure metrics
		// RecordStreamItem fires for every item; stats.LoggingObserver also logs via slog.
		stream.ApplyOptions{Observer: obs},
	)
	obsVals, _ := stream.Collect(ctx, s9)
	fmt.Printf("  items processed by Apply   : %d (via StreamObserver.RecordStreamItem)\n", metrics.items)
	fmt.Printf("  domain events from Tap     : %d (typed TempReading — no boxing)\n", domainEvents)
	fmt.Printf("  avg Apply latency          : %v\n", metrics.avgLatency())
	fmt.Printf("  results                    : %v\n", obsVals)
	fmt.Println("  → stats.NewFanout wires metrics + logging into one observer value")
	fmt.Println("  → Tap and StreamObserver are orthogonal: use both simultaneously")

	// ── Section 9: Stream topology YAML ──────────────────────────────────
	//
	// stream.Topology documents the pipeline; render/stream serialises as YAML.
	fmt.Println("\n─── Section 9: Stream topology YAML (render/stream.Render)")

	envMonitorTopo.
		WithFilter("heat index level == danger").
		WithDebounce("30s — rate-limit alerts").
		WithSink("stdout-alerts", "Print dangerous heat index events")

	spec := envMonitorTopo.Spec()
	yamlBytes, err := streamrender.Render(spec)
	if err != nil {
		fmt.Println("render error:", err)
		return
	}
	fmt.Println(string(yamlBytes))

	// ── Section 10: Routing (Switch + GroupBy) ────────────────────────────
	//
	// Switch routes each item to the FIRST matching named case; non-matches
	// and stream errors land on the rest stream. GroupBy splits the stream
	// into per-key sub-streams, one per distinct key.
	fmt.Println("\n─── Section 10: Routing (Switch by severity + GroupBy per sensor)")

	routeIn := make(chan TempReading, 6)
	for _, r := range []TempReading{
		{SensorID: "roof", Celsius: 52},
		{SensorID: "lab", Celsius: 41},
		{SensorID: "roof", Celsius: 23},
		{SensorID: "lab", Celsius: 55},
		{SensorID: "yard", Celsius: 19},
	} {
		routeIn <- r
	}
	close(routeIn)

	outs, rest := stream.Switch(ctx, stream.From(ctx, routeIn),
		[]stream.Case[TempReading]{
			{Name: "alert", When: func(r TempReading) bool { return r.Celsius >= 50 }},
			{Name: "warning", When: func(r TempReading) bool { return r.Celsius >= 40 }},
		},
		stream.SwitchOptions{Buffer: 6})

	alerts, _ := stream.Collect(ctx, outs[0])
	warnings, _ := stream.Collect(ctx, outs[1])
	archived, _ := stream.Collect(ctx, rest)
	fmt.Printf("  switch: %d alerts (≥50°C), %d warnings (≥40°C), %d archived\n",
		len(alerts), len(warnings), len(archived))

	groupIn := make(chan TempReading, 4)
	for _, r := range []TempReading{
		{SensorID: "roof", Celsius: 52},
		{SensorID: "lab", Celsius: 41},
		{SensorID: "roof", Celsius: 23},
		{SensorID: "lab", Celsius: 55},
	} {
		groupIn <- r
	}
	close(groupIn)

	type keyCount struct {
		key string
		n   int
	}
	counts := make(chan keyCount, 4)
	var consumers sync.WaitGroup
	stream.GroupBy(ctx, stream.From(ctx, groupIn),
		func(r TempReading) string { return r.SensorID },
		func(sensorID string, s stream.Stream[TempReading]) {
			consumers.Add(1)
			go func() {
				defer consumers.Done()
				n := 0
				for range s.Values {
					n++
				}
				for range s.Errors {
				}
				counts <- keyCount{sensorID, n}
			}()
		},
		stream.GroupByOptions{Buffer: 4})
	consumers.Wait() // GroupBy returned → sub-streams closed; wait for consumers
	close(counts)

	got := map[string]int{}
	for kc := range counts {
		got[kc.key] = kc.n
	}
	fmt.Printf("  groupby: roof=%d readings, lab=%d readings\n", got["roof"], got["lab"])
}

func mustMarshal(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// ── Observer types for Section 9 ─────────────────────────────────────────────

// streamMetrics is a pure counters implementation of [stats.Observer] +
// [stats.StreamObserver]. In production replace with Prometheus CounterVecs
// or OpenTelemetry instruments — the interface is identical.
type streamMetrics struct {
	stats.NoopObserver     // satisfies all Observer interfaces not explicitly implemented
	items              int // stream.Apply items processed
	latencies          []time.Duration
}

// RecordStreamItem implements [stats.StreamObserver].
func (m *streamMetrics) RecordStreamItem(_ string, success bool, dur time.Duration) {
	if success {
		m.items++
		m.latencies = append(m.latencies, dur)
	}
}

func (m *streamMetrics) avgLatency() time.Duration {
	if len(m.latencies) == 0 {
		return 0
	}
	var total time.Duration
	for _, d := range m.latencies {
		total += d
	}
	return total / time.Duration(len(m.latencies))
}
