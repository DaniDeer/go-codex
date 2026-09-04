package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/DaniDeer/go-codex/examples/sensor-service/adapters"
	"github.com/DaniDeer/go-codex/examples/sensor-service/db"
	"github.com/DaniDeer/go-codex/examples/sensor-service/domain"
	"github.com/DaniDeer/go-codex/examples/sensor-service/ioports"
	"github.com/DaniDeer/go-codex/examples/sensor-service/observability"
	"github.com/DaniDeer/go-codex/examples/sensor-service/pipeline"
	"github.com/DaniDeer/go-codex/ports"
	streamrender "github.com/DaniDeer/go-codex/render/stream"
)

// demoEnv carries everything the demo scenario needs from the wired service.
type demoEnv struct {
	ctx            context.Context
	cancelPipeline context.CancelFunc
	pipelineDone   <-chan struct{}
	mqttClient     *adapters.MockMQTTClient
	srvURL         string
	cfg            domain.AlertConfig
	counting       *observability.CountingObserver
}

// runDemo drives the wired service through ONE coherent story:
//
//  1. Sensors publish readings over MQTT → persisted to the DB through the
//     Readings port; a threshold breach (env-configured) publishes an alert.
//  2. A reading is created and fetched over REST; the reactive cache serves
//     the latest reading without a DB query.
//  3. The time series of one sensor is requested over REST — queried from
//     the DB through the History port.
//  4. An export is triggered over REST — queried through the ExportQuery
//     port and written to a typed JSON file through the Exports file port.
//
// Then it prints the observer summary, the stream topology, and the
// AsyncAPI + OpenAPI specs built from the port declarations.
func runDemo(env demoEnv) {
	shouldAlert := domain.NewShouldAlert(env.cfg)

	// ── 1. MQTT sensor messages ────────────────────────────────────────────
	fmt.Println("── 1. MQTT sensor events → DB → threshold alert ─────────")

	sensorA := "550e8400-e29b-41d4-a716-446655440001"
	sensorB := "550e8400-e29b-41d4-a716-446655440002"

	publish := func(sensorID string, value float64) {
		payload, _ := json.Marshal(map[string]any{
			"sensor_id": sensorID,
			"value":     value,
			"unit":      "C",
		})
		env.mqttClient.Deliver("sensors/"+sensorID+"/data", payload)
	}

	// Two readings from sensorA (its time series in scene 3) — below threshold.
	publish(sensorA, 23.5)
	fmt.Printf("  → sensor %s: 23.5 C  (below threshold, no alert)\n", sensorA[:8])
	time.Sleep(20 * time.Millisecond) // distinct recorded_at ordering
	publish(sensorA, 24.1)
	fmt.Printf("  → sensor %s: 24.1 C  (below threshold, no alert)\n", sensorA[:8])

	// High reading from sensorB (above threshold → alert published).
	publish(sensorB, 87.3)
	if shouldAlert(db.Reading{Value: 87.3}) {
		fmt.Printf("  → sensor %s: 87.3 C  (above %.0f° threshold — alert published)\n\n",
			sensorB[:8], env.cfg.Threshold)
	} else {
		fmt.Printf("  → sensor %s: 87.3 C  (below %.0f° threshold, no alert)\n\n",
			sensorB[:8], env.cfg.Threshold)
	}

	// Give the async goroutines time to process the MQTT messages, then
	// cancel the pipeline context to drain and shut down the MQTT stream
	// operators. (The HTTP ports below run on per-request contexts and are
	// unaffected.)
	time.Sleep(100 * time.Millisecond)
	env.cancelPipeline()
	<-env.pipelineDone // wait for PublishAdapter goroutine to finish

	// ── 2. REST create/read + reactive cache ──────────────────────────────
	fmt.Println("── 2. REST requests + reactive cache ────────────────────")

	// POST /readings — valid.
	postBody, _ := json.Marshal(map[string]any{
		"sensor_id": sensorA,
		"value":     22.8,
		"unit":      "C",
	})
	resp, err := http.Post(env.srvURL+"/readings", "application/json", bytes.NewReader(postBody))
	must(err, "POST /readings")
	var created db.Reading
	_ = json.NewDecoder(resp.Body).Decode(&created)
	_ = resp.Body.Close()
	fmt.Printf("  POST /readings            → %d  id=%s\n", resp.StatusCode, created.ID)

	// POST /readings — invalid unit (codec rejects before DB).
	badBody, _ := json.Marshal(map[string]any{
		"sensor_id": sensorA,
		"value":     22.0,
		"unit":      "kelvin",
	})
	resp2, err := http.Post(env.srvURL+"/readings", "application/json", bytes.NewReader(badBody))
	must(err, "POST /readings (bad)")
	_ = resp2.Body.Close()
	fmt.Printf("  POST /readings (bad unit) → %d  (codec rejected before DB)\n", resp2.StatusCode)

	// GET /readings/latest — served from the stream's atomic pointer, no DB
	// query. Still holds the last MQTT reading (87.3 C from sensorB).
	resp3, err := http.Get(env.srvURL + "/readings/latest")
	must(err, "GET /readings/latest")
	var latestMap map[string]any
	_ = json.NewDecoder(resp3.Body).Decode(&latestMap)
	_ = resp3.Body.Close()
	fmt.Printf("  GET /readings/latest      → %d  value=%v %v  (from stream cache, zero DB queries)\n\n",
		resp3.StatusCode, latestMap["value"], latestMap["unit"])

	// ── 3. REST time series — queried from the DB through the History port ─
	//
	// REST → HistoryTool (ToolPort, RESTPattern) → pipeline →
	// History (IOPort, SQLPattern) → sqlc query. Every hop is a port.
	fmt.Println("── 3. GET /sensors/{sensorID}/readings (History port) ───")
	resp4, err := http.Get(env.srvURL + "/sensors/" + sensorA + "/readings")
	must(err, "GET time series")
	var series map[string]any
	_ = json.NewDecoder(resp4.Body).Decode(&series)
	_ = resp4.Body.Close()
	readings, _ := series["readings"].([]any)
	fmt.Printf("  GET /sensors/%s/readings → %d  (%d readings)\n", sensorA[:8], resp4.StatusCode, len(readings))
	for _, item := range readings {
		m, _ := item.(map[string]any)
		fmt.Printf("    %v  value=%v %v\n", m["recorded_at"], m["value"], m["unit"])
	}
	fmt.Println()

	// ── 4. REST-triggered export — written to file through the Exports port ─
	//
	// REST → ExportTool (ToolPort, RESTPattern) → pipeline →
	// ExportQuery (IOPort, SQLPattern) → Exports (SinkPort, FilePattern) →
	// typed JSON file. The response path comes from the SAME FilePattern
	// declaration that writes the file (FileHandle.BuildPath).
	fmt.Println("── 4. POST /export (ExportQuery + Exports file port) ────")

	// The X-Api-Key header codec (domain.APIKeyCodec) was declared on the
	// port's RESTPattern — the adapter validates it BEFORE the pipeline runs.
	postExport := func(apiKey string) *http.Response {
		req, err := http.NewRequest(http.MethodPost, env.srvURL+"/export", bytes.NewReader([]byte(`{}`)))
		must(err, "build POST /export")
		req.Header.Set("Content-Type", "application/json")
		if apiKey != "" {
			req.Header.Set("X-Api-Key", apiKey)
		}
		resp, err := http.DefaultClient.Do(req)
		must(err, "POST /export")
		return resp
	}

	// No API key → 400: required header missing, pipeline never runs.
	respNoKey := postExport("")
	_ = respNoKey.Body.Close()
	fmt.Printf("  POST /export (no key)     → %d  (required X-Api-Key missing)\n", respNoKey.StatusCode)

	// Malformed key → 400: header codec constraint failed, pipeline never runs.
	respBadKey := postExport("wrong-key")
	_ = respBadKey.Body.Close()
	fmt.Printf("  POST /export (bad key)    → %d  (X-Api-Key failed api-key-format constraint)\n", respBadKey.StatusCode)

	// Valid key → 201: export runs.
	resp5 := postExport("sk-demo-key-123")
	var exported domain.ExportResult
	_ = json.NewDecoder(resp5.Body).Decode(&exported)
	_ = resp5.Body.Close()
	fmt.Printf("  POST /export (valid key)  → %d  count=%d\n", resp5.StatusCode, exported.Count)
	fmt.Printf("  file: …/%s\n", filepathBase(exported.File))

	// The file write happens asynchronously through the sink port — wait
	// briefly for the drain, then read the snapshot back through the codec
	// path (os.ReadFile + json) to prove it landed.
	var snapshot map[string]any
	for i := 0; i < 100; i++ {
		raw, err := os.ReadFile(exported.File)
		if err == nil && json.Unmarshal(raw, &snapshot) == nil && snapshot["export_id"] != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	exportedReadings, _ := snapshot["readings"].([]any)
	fmt.Printf("  read back: export_id=%.8v…  %d readings in file\n\n",
		snapshot["export_id"], len(exportedReadings))

	// ── Observer summary ───────────────────────────────────────────────────
	env.counting.Print()

	// ── Pipeline spec documentation — derived, not hand-typed ───────────────
	//
	// ports.PipelineSpec reads pipe names, buffer sizes, bound adapter
	// identities, and every Chain/ChainStream edge (including each
	// transform's real Go function identity via reflection) directly from
	// the four PipePorts pipeline.Build wired — no separate,
	// hand-maintained pipeline.Topology function to keep in sync (Gap 4 of
	// docs/roadmap/pipe-port-composition-hardening.md). This package
	// imports both ioports (for Sensors/Alerts) and pipeline (for
	// Params/Saved), so it — not pipeline itself — is where the call
	// belongs (pipeline never imports ioports).
	pipelineSpec := ports.PipelineSpec("Sensor Service MQTT Pipeline", "1.0.0",
		ioports.Sensors, pipeline.Params, pipeline.Saved, ioports.Alerts)
	specYAML, err := streamrender.Render(pipelineSpec)
	must(err, "render pipeline spec")

	fmt.Println("\n── Pipeline spec (ports.PipelineSpec → render/stream.Render, fully derived) ──")
	fmt.Println(string(specYAML))

	// ── Specs built FROM the port declarations ─────────────────────────────
	//
	// ioports.Sensors/Alerts registered their EventPatterns against
	// ioports.EventsClient, and ioports.HistoryTool/ExportTool their
	// RESTPatterns against ioports.RESTBuilder — the specs below reflect the
	// ports without any separate Register call: the port declaration IS the
	// channel/route declaration.
	fmt.Println("\n── AsyncAPI spec (from the ports.EventPattern bindings) ──")
	asyncDoc, err := ioports.EventsClient.AsyncAPISpec()
	must(err, "build AsyncAPI spec")
	asyncYAML, err := asyncDoc.MarshalYAML()
	must(err, "marshal AsyncAPI spec")
	fmt.Println(string(asyncYAML))

	fmt.Println("── OpenAPI spec (from the route + RESTPattern bindings) ──")
	openapiDoc, err := ioports.RESTBuilder.OpenAPISpec()
	must(err, "build OpenAPI spec")
	openapiYAML, err := openapiDoc.MarshalYAML()
	must(err, "marshal OpenAPI spec")
	fmt.Println(string(openapiYAML))
}

func filepathBase(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[i+1:]
		}
	}
	return p
}
