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
//   - OpenAPI 3.1 spec generation including SSE routes
package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
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
	"github.com/DaniDeer/go-codex/stats"
	"github.com/DaniDeer/go-codex/validate"
)

// ── Domain types ──────────────────────────────────────────────────────────────

type emptyReq struct{}

type counterEvent struct {
	Count int
}

type sensorReading struct {
	SensorID    string
	Temperature float64
	Unit        string
}

// ── Codecs ────────────────────────────────────────────────────────────────────

var emptyReqCodec = codex.Struct[emptyReq]()

var counterEventCodec = codex.Struct[counterEvent](
	codex.Field[counterEvent, int]{
		Name:     "count",
		Codec:    codex.Int(),
		Required: true,
		Get:      func(e counterEvent) int { return e.Count },
		Set:      func(e *counterEvent, v int) { e.Count = v },
	},
)

var temperatureConstraint = codex.Constraint[float64]{
	Name:    "valid-temperature",
	Check:   func(v float64) bool { return v >= -273.15 },
	Message: func(v float64) string { return fmt.Sprintf("temperature %v below absolute zero", v) },
}

var sensorReadingCodec = codex.Struct[sensorReading](
	codex.Field[sensorReading, string]{
		Name:     "sensor_id",
		Codec:    codex.String().Refine(validate.NonEmptyString),
		Required: true,
		Get:      func(r sensorReading) string { return r.SensorID },
		Set:      func(r *sensorReading, v string) { r.SensorID = v },
	},
	codex.Field[sensorReading, float64]{
		Name:     "temperature",
		Codec:    codex.Float64().Refine(temperatureConstraint),
		Required: true,
		Get:      func(r sensorReading) float64 { return r.Temperature },
		Set:      func(r *sensorReading, v float64) { r.Temperature = v },
	},
	codex.Field[sensorReading, string]{
		Name:     "unit",
		Codec:    codex.String().Refine(validate.NonEmptyString),
		Required: true,
		Get:      func(r sensorReading) string { return r.Unit },
		Set:      func(r *sensorReading, v string) { r.Unit = v },
	},
)

// sensorIDCodec validates that a sensor ID is in the "<word>-<word>" format.
var sensorIDCodec = codex.Refine(codex.String(),
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
	fmt.Printf("  [stats] %s %s → %d (%s)\n", method, path, statusCode, d.Round(time.Millisecond))
}

func (o *statsObserver) RecordValidationError(location, constraint, fieldName string) {
	o.validationErrors++
	fmt.Printf("  [stats] validation error: location=%s constraint=%s field=%s\n", location, constraint, fieldName)
}

func (o *statsObserver) Print() {
	fmt.Printf("  requests: %d  validation errors: %d\n", o.requests, o.validationErrors)
}

func (o *statsObserver) RecordPublish(_ string, _ bool, _ time.Duration)   {}
func (o *statsObserver) RecordSubscribe(_ string, _ bool, _ time.Duration) {}

var _ stats.Observer = (*statsObserver)(nil)

// ── Handlers ──────────────────────────────────────────────────────────────────

func handleCounter(_ context.Context, _ emptyReq, send func(counterEvent) error) error {
	for i := 1; i <= 3; i++ {
		if err := send(counterEvent{Count: i}); err != nil {
			return fmt.Errorf("counter send: %w", err)
		}
	}
	return nil
}

// handleSensor uses chi URL params to extract {id}.
func handleSensor(ctx context.Context, _ emptyReq, send func(sensorReading) error) error {
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

// handleInvalid sends one valid event, then an invalid one (empty SensorID),
// then another valid event — demonstrating per-event validation.
func handleInvalid(_ context.Context, _ emptyReq, send func(sensorReading) error) error {
	_ = send(sensorReading{SensorID: "s1", Temperature: 21.0, Unit: "°C"})
	if err := send(sensorReading{SensorID: "", Temperature: 99.0, Unit: "°C"}); err != nil {
		fmt.Printf("  rejected invalid event (expected): %v\n", err)
	}
	_ = send(sensorReading{SensorID: "s1", Temperature: 22.0, Unit: "°C"})
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

func main() {
	obs := &statsObserver{}
	opts := nethttp.Options{Observer: obs}
	chiOpts := chiadapter.Options{Observer: obs}

	b := rest.NewBuilder(rest.Info{Title: "SSE Demo API", Version: "1.0.0"})

	counterRoute, err := rest.AddSSERoute[emptyReq, counterEvent](
		b, "/sse/counter",
		emptyReqCodec, counterEventCodec,
		rest.RouteConfig{OperationID: "streamCounter", Summary: "Stream a counter"},
	)
	if err != nil {
		log.Fatalf("AddSSERoute counter: %v", err)
	}

	sensorRoute, err := rest.AddSSERoute[emptyReq, sensorReading](
		b, "/sse/sensor/{id}",
		emptyReqCodec, sensorReadingCodec,
		rest.RouteConfig{
			OperationID: "streamSensor",
			Summary:     "Stream sensor readings",
			PathParams: []rest.PathParam{
				{Name: "id", Description: "Sensor ID (<word>-<word>)", Codec: &sensorIDCodec},
			},
		},
	)
	if err != nil {
		log.Fatalf("AddSSERoute sensor: %v", err)
	}

	invalidRoute, err := rest.AddSSERoute[emptyReq, sensorReading](
		b, "/sse/invalid",
		emptyReqCodec, sensorReadingCodec,
		rest.RouteConfig{OperationID: "streamInvalid", Summary: "Codec rejection demo"},
	)
	if err != nil {
		log.Fatalf("AddSSERoute invalid: %v", err)
	}

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
	nethttp.RegisterSSE(mux, counterRoute, handleCounter, opts)
	nethttp.RegisterSSE(mux, invalidRoute, handleInvalid, opts)

	r := gochi.NewRouter()
	chiadapter.RegisterSSE(r, sensorRoute, handleSensor, chiOpts)
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

	// ── Stats summary ──────────────────────────────────────────────────────
	fmt.Println("=== Observer summary ===")
	obs.Print()
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
