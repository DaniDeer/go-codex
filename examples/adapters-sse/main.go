// Package adapters-sse demonstrates Server-Sent Events (SSE) using the
// go-codex adapters/nethttp and adapters/chi adapters.
//
// # What this example shows
//
//   - [rest.AddSSERoute] — registering a typed SSE route with request and event codecs
//   - [nethttp.SSEHandler] / [nethttp.RegisterSSE] — wiring SSE onto net/http
//   - [chiadapter.SSEHandler] / [chiadapter.RegisterSSE] — wiring SSE onto chi
//   - Path parameter codec validation on {id} via [rest.PathParam.Codec]
//   - [rest.SSERouteHandle.BuildPath] for validated URL assembly
//   - Codec validation on each event before it is written to the client
//   - Stats observer counting validation errors from rejected events
//   - [rest.ResponseHeaderParam] on SSE routes — custom response header committed on first send
//   - OpenAPI 3.1 spec generation including SSE routes
package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"time"

	gochi "github.com/go-chi/chi/v5"

	chiadapter "github.com/DaniDeer/go-codex/adapters/chi"
	nethttp "github.com/DaniDeer/go-codex/adapters/nethttp"
	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/stats"
	"github.com/DaniDeer/go-codex/validate"
)

// ── Domain types ──────────────────────────────────────────────────────────────

type counterEvent struct {
	Count int
}

type sensorReading struct {
	SensorID    string
	Temperature float64
	Unit        string
}

type readingMeta struct {
	SensorID string
}

type readingPayload struct {
	Temperature float64
	Unit        string
}

type readingEvent struct {
	Meta    readingMeta
	Payload readingPayload
}

// ── Codecs ────────────────────────────────────────────────────────────────────

var counterEventCodec = codex.Struct[counterEvent](
	codex.RequiredField("count", codex.Int(), func(e counterEvent) int { return e.Count }, func(e *counterEvent, v int) { e.Count = v }),
)

var temperatureConstraint = codex.Constraint[float64]{
	Name:    "valid-temperature",
	Check:   func(v float64) bool { return v >= -273.15 },
	Message: func(v float64) string { return fmt.Sprintf("temperature %v below absolute zero", v) },
}

var sensorReadingCodec = codex.Struct[sensorReading](
	codex.RequiredField("sensor_id", codex.String().Refine(validate.NonEmptyString), func(r sensorReading) string { return r.SensorID }, func(r *sensorReading, v string) { r.SensorID = v }),
	codex.RequiredField("temperature", codex.Float64().Refine(temperatureConstraint), func(r sensorReading) float64 { return r.Temperature }, func(r *sensorReading, v float64) { r.Temperature = v }),
	codex.RequiredField("unit", codex.String().Refine(validate.NonEmptyString), func(r sensorReading) string { return r.Unit }, func(r *sensorReading, v string) { r.Unit = v }),
)

var readingEventCodec = codex.Struct[readingEvent](
	codex.RequiredField("sensor_id", codex.String().Refine(validate.NonEmptyString), func(r readingEvent) string { return r.Meta.SensorID }, func(r *readingEvent, v string) { r.Meta.SensorID = v }),
	codex.RequiredField("temperature", codex.Float64().Refine(temperatureConstraint), func(r readingEvent) float64 { return r.Payload.Temperature }, func(r *readingEvent, v float64) { r.Payload.Temperature = v }),
	codex.RequiredField("unit", codex.String().Refine(validate.NonEmptyString), func(r readingEvent) string { return r.Payload.Unit }, func(r *readingEvent, v string) { r.Payload.Unit = v }),
)

// sensorIDCodec validates that a sensor ID is in the "<word>-<word>" format.
var sensorIDCodec = codex.String().Refine(
	validate.NonEmptyString,
	codex.Constraint[string]{
		Name:    "sensor-id",
		Check:   func(v string) bool { return len(strings.Split(v, "-")) >= 2 },
		Message: func(v string) string { return fmt.Sprintf("sensor ID %q must be in format '<word>-<word>'", v) },
	},
)

// ── Stats observer ────────────────────────────────────────────────────────────

type statsObserver struct {
	requests         int
	validationErrors int
}

func (o *statsObserver) RecordRequest(method, path string, statusCode int, d time.Duration) {
	o.requests++
}

func (o *statsObserver) RecordValidationError(location, constraint, fieldName string) {
	o.validationErrors++
}

func (o *statsObserver) Print() {
	fmt.Printf("  requests: %d  validation errors: %d\n", o.requests, o.validationErrors)
}

func (o *statsObserver) RecordPublish(_ string, _ bool, _ time.Duration)   {}
func (o *statsObserver) RecordSubscribe(_ string, _ bool, _ time.Duration) {}

var _ stats.Observer = (*statsObserver)(nil)

// ── Handlers ──────────────────────────────────────────────────────────────────

func handleCounter(_ context.Context, _ struct{}, send func(counterEvent) error) error {
	for i := 1; i <= 3; i++ {
		if err := send(counterEvent{Count: i}); err != nil {
			return fmt.Errorf("counter send: %w", err)
		}
	}
	return nil
}

// handleSensor uses chi URL params to extract {id}.
func handleSensor(ctx context.Context, _ struct{}, send func(sensorReading) error) error {
	r, _ := chiadapter.RequestFromContext(ctx)
	sensorID := "unknown"
	if r != nil {
		if id := gochi.URLParam(r, "id"); id != "" {
			sensorID = id
		}
	}

	readings := []float64{20.0, 20.5, 21.0}
	for _, temp := range readings {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		if err := send(sensorReading{SensorID: sensorID, Temperature: temp, Unit: "°C"}); err != nil {
			return err
		}
	}
	return nil
}

// handleWithHeaders stages a custom response header before sending the first event.
// The adapter commits the header to the wire on the first send() call.
func handleWithHeaders(ctx context.Context, _ struct{}, send func(counterEvent) error) error {
	extra := make(http.Header)
	extra.Set("X-Trace-Id", "trace-abc-123")
	nethttp.WithResponseHeaders(ctx, extra)
	return send(counterEvent{Count: 1})
}

// handleInvalid sends one valid event, then an invalid one (empty SensorID),
// then another valid event — demonstrating per-event validation.
func handleInvalid(_ context.Context, _ struct{}, send func(sensorReading) error) error {
	_ = send(sensorReading{SensorID: "s1", Temperature: 21.0, Unit: "°C"})
	if err := send(sensorReading{SensorID: "", Temperature: 99.0, Unit: "°C"}); err != nil {
		fmt.Printf("  rejected invalid event (expected): %v\n", err)
	}
	_ = send(sensorReading{SensorID: "s1", Temperature: 22.0, Unit: "°C"})
	return nil
}

// handleSensorMergedConvenience sends events without setting SensorID.
// NewRequiredSSEEventParam merges {id} into Meta.SensorID automatically.
func handleSensorMergedConvenience(_ context.Context, _ struct{}, send func(readingEvent) error) error {
	for _, temp := range []float64{21.0, 21.5} {
		if err := send(readingEvent{Payload: readingPayload{Temperature: temp, Unit: "C"}}); err != nil {
			return err
		}
	}
	return nil
}

// handleSensorManualEscape demonstrates the escape hatch:
// manually reading {id} and setting Meta.SensorID before send.
func handleSensorManualEscape(ctx context.Context, _ struct{}, send func(readingEvent) error) error {
	r, _ := nethttp.RequestFromContext(ctx)
	sensorID := "unknown"
	if r != nil {
		sensorID = r.PathValue("id")
	}
	for _, temp := range []float64{21.0, 21.5} {
		if err := send(readingEvent{
			Meta:    readingMeta{SensorID: sensorID},
			Payload: readingPayload{Temperature: temp, Unit: "C"},
		}); err != nil {
			return err
		}
	}
	return nil
}

// ── Helper: read all SSE data lines from a response ──────────────────────────

func readSSELines(resp *http.Response) []string {
	defer resp.Body.Close()
	var lines []string
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "data:") {
			lines = append(lines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	return lines
}

// ── Main ──────────────────────────────────────────────────────────────────────

// mustRegister exits the program if RegisterSSE returns an error — e.g. a
// malformed middleware Fn shape, caught eagerly at wiring time.
func mustRegister(err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "RegisterSSE failed: %v\n", err)
		os.Exit(1)
	}
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	slog.SetDefault(logger)

	metrics := &statsObserver{}
	obs := stats.NewFanout(metrics, stats.NewLoggingObserver(logger.With("component", "sse")))
	// ObservabilityMiddleware is the only nethttp/chi call site that
	// touches stats.Observer now — chi reuses nethttp's directly (same
	// general-purpose Fn shape both packages recognize).
	obsMw := nethttp.ObservabilityMiddleware(obs)
	opts := nethttp.Options{}
	chiOpts := chiadapter.Options{}

	b := rest.NewBuilder(rest.Info{Title: "SSE Demo API", Version: "1.0.0"})

	counterRoute, err := rest.NewSSERoute[struct{}, counterEvent]("/sse/counter",
		codex.Empty, counterEventCodec,
		rest.RouteMeta{OperationID: "streamCounter", Summary: "Stream a counter"},
	).Register(b)
	if err != nil {
		log.Fatalf("AddSSERoute counter: %v", err)
	}

	sensorRoute, err := rest.NewSSERoute[struct{}, sensorReading]("/sse/sensor/{id}",
		codex.Empty, sensorReadingCodec,
		rest.RouteMeta{
			OperationID: "streamSensor",
			Summary:     "Stream sensor readings",
		},
		rest.PathParam{Name: "id", Description: "Sensor ID (<word>-<word>)"}.WithCodec(sensorIDCodec),
	).Register(b)
	if err != nil {
		log.Fatalf("AddSSERoute sensor: %v", err)
	}

	invalidRoute, err := rest.NewSSERoute[struct{}, sensorReading]("/sse/invalid",
		codex.Empty, sensorReadingCodec,
		rest.RouteMeta{OperationID: "streamInvalid", Summary: "Codec rejection demo"},
	).Register(b)
	if err != nil {
		log.Fatalf("AddSSERoute invalid: %v", err)
	}

	traceCodec := codex.String().Refine(validate.NonEmptyString)
	withHeadersRoute, err := rest.NewSSERoute[struct{}, counterEvent]("/sse/with-headers",
		codex.Empty, counterEventCodec,
		rest.RouteMeta{OperationID: "streamWithHeaders", Summary: "Stream with custom response header"},
		rest.ResponseHeaderParam{Name: "X-Trace-Id", Description: "Distributed trace ID"}.WithCodec(traceCodec),
	).Register(b)
	if err != nil {
		log.Fatalf("AddSSERoute with-headers: %v", err)
	}

	mergedRoute, err := rest.NewSSERoute[struct{}, readingEvent]("/sse/merge/{id}",
		codex.Empty, readingEventCodec,
		rest.RouteMeta{OperationID: "streamMerged", Summary: "One-struct SSE merge convenience"},
		rest.PathParam{Name: "id", Description: "Sensor ID (<word>-<word>)"}.WithCodec(sensorIDCodec),
		rest.NewRequiredSSEEventParam("id", codex.String(), func(e readingEvent) string { return e.Meta.SensorID }, func(e *readingEvent, v string) { e.Meta.SensorID = v }),
	).Register(b)
	if err != nil {
		log.Fatalf("AddSSERoute merge: %v", err)
	}
	mergedRoute = mergedRoute.WithFormats(format.YAML(readingEventCodec))

	manualRoute, err := rest.NewSSERoute[struct{}, readingEvent]("/sse/manual/{id}",
		codex.Empty, readingEventCodec,
		rest.RouteMeta{OperationID: "streamManual", Summary: "SSE merge escape hatch"},
		rest.PathParam{Name: "id", Description: "Sensor ID (<word>-<word>)"}.WithCodec(sensorIDCodec),
	).Register(b)
	if err != nil {
		log.Fatalf("AddSSERoute manual: %v", err)
	}
	manualRoute = manualRoute.WithFormats(format.YAML(readingEventCodec))

	// ── BuildPath codec validation ─────────────────────────────────────────
	fmt.Println("=== BuildPath with sensorIDCodec ===")
	if path, err := sensorRoute.BuildPath(map[string]string{"id": "room-42"}); err != nil {
		log.Fatalf("BuildPath room-42: %v", err)
	} else {
		fmt.Printf("  valid:   %s\n", path)
	}
	if _, err := sensorRoute.BuildPath(map[string]string{"id": "invalid"}); err != nil {
		fmt.Printf("  invalid: BuildPath rejected (expected): %v\n\n", err)
	}

	// ── Wiring ────────────────────────────────────────────────────────────
	mux := http.NewServeMux()
	mustRegister(nethttp.RegisterSSE(mux, counterRoute, handleCounter, opts, obsMw))
	mustRegister(nethttp.RegisterSSE(mux, invalidRoute, handleInvalid, opts, obsMw))
	mustRegister(nethttp.RegisterSSE(mux, withHeadersRoute, handleWithHeaders, opts, obsMw))
	mustRegister(nethttp.RegisterSSE(mux, mergedRoute, handleSensorMergedConvenience, opts, obsMw))
	mustRegister(nethttp.RegisterSSE(mux, manualRoute, handleSensorManualEscape, opts, obsMw))

	r := gochi.NewRouter()
	mustRegister(chiadapter.RegisterSSE(r, sensorRoute, handleSensor, chiOpts, obsMw))
	mux.Handle("/sse/sensor/", r)

	srv := httptest.NewServer(mux)
	defer srv.Close()

	// ── Counter stream ─────────────────────────────────────────────────────
	fmt.Println("=== GET /sse/counter (3 events) ===")
	resp1, err := http.Get(srv.URL + "/sse/counter") //nolint:noctx
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("  Content-Type: %s\n", resp1.Header.Get("Content-Type"))
	for _, line := range readSSELines(resp1) {
		fmt.Printf("  event: %s\n", line)
	}
	fmt.Println()

	// ── Sensor stream with path param codec ───────────────────────────────
	fmt.Println("=== GET /sse/sensor/room-42 (chi, {id} codec-validated) ===")
	resp2, err := http.Get(srv.URL + "/sse/sensor/room-42") //nolint:noctx
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("  Content-Type: %s\n", resp2.Header.Get("Content-Type"))
	for _, line := range readSSELines(resp2) {
		fmt.Printf("  event: %s\n", line)
	}
	fmt.Println()

	// ── Invalid event demo ─────────────────────────────────────────────────
	fmt.Println("=== GET /sse/invalid (middle event rejected by codec) ===")
	resp3, err := http.Get(srv.URL + "/sse/invalid") //nolint:noctx
	if err != nil {
		log.Fatal(err)
	}
	lines := readSSELines(resp3)
	fmt.Printf("  received %d events (1 rejected, 2 sent)\n", len(lines))
	for _, line := range lines {
		fmt.Printf("  event: %s\n", line)
	}
	fmt.Println()

	// ── ResponseHeaderParam: custom header committed on first send ─────────
	fmt.Println("=== GET /sse/with-headers (X-Trace-Id committed on first send) ===")
	resp4, err := http.Get(srv.URL + "/sse/with-headers") //nolint:noctx
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("  X-Trace-Id: %s\n", resp4.Header.Get("X-Trace-Id"))
	for _, line := range readSSELines(resp4) {
		fmt.Printf("  event: %s\n", line)
	}
	fmt.Println()

	fmt.Println("=== Side-by-side: one-struct merge vs manual escape hatch (YAML) ===")
	resp5, err := http.Get(srv.URL + "/sse/merge/room-42") //nolint:noctx
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("  merge route Content-Type: %s\n", resp5.Header.Get("Content-Type"))
	for _, line := range readSSELines(resp5) {
		fmt.Printf("  merge event:  %s\n", line)
	}
	resp6, err := http.Get(srv.URL + "/sse/manual/room-42") //nolint:noctx
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("  manual route Content-Type: %s\n", resp6.Header.Get("Content-Type"))
	for _, line := range readSSELines(resp6) {
		fmt.Printf("  manual event: %s\n", line)
	}
	fmt.Println("  both routes deliver same payload; merge route removes manual path-value stitching")
	fmt.Println()

	// ── Stats summary ──────────────────────────────────────────────────────
	fmt.Println("=== Observer summary ===")
	metrics.Print()
	fmt.Println()

	// ── OpenAPI spec ───────────────────────────────────────────────────────
	fmt.Println("=== OpenAPI 3.1 spec (SSE routes included) ===")
	doc, err := b.OpenAPISpec()
	if err != nil {
		fmt.Fprintf(os.Stderr, "OpenAPISpec error: %v\n", err)
		os.Exit(1)
	}
	yaml, err := doc.MarshalYAML()
	if err != nil {
		fmt.Fprintf(os.Stderr, "MarshalYAML error: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(string(yaml))
}
