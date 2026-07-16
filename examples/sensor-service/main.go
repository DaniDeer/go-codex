// Command sensor-service is the go-codex flagship example: a small but
// complete sensor-readings service structured as a real project, with each
// concern in its own package:
//
//	domain/         — Layer 1+2: models, codecs, field factories, constraints,
//	                  pure business rules (validated-config factories included)
//	pipeline/       — business logic: forge functions + stream topology,
//	                  parameterized by the consumer-defined Store interface
//	ioports/        — the service's complete IO surface: protocol-agnostic
//	                  ports (EventPattern/SQLPattern/FilePattern) + REST routes
//	observability/  — cross-cutting CountingObserver (fanned out with a
//	                  LoggingObserver, stored once in the context)
//	adapters/       — infrastructure edge: mock MQTT client, SQL ReadingStore,
//	                  HTTP handler factories
//	db/             — sqlc-generated queries + goose migrations
//	main.go         — wiring ONLY: config, DB, observer, adapter binds, server
//	demo.go         — the runnable demo scenario
//
// Import direction is strictly acyclic: main → {ioports, pipeline, adapters,
// observability, domain}; pipeline → domain; ioports → domain;
// adapters → domain; domain → nothing internal but db.
//
// # What it demonstrates
//
//   - [ports.SourcePort] + [adaptermqtt.SubscribeAdapter] — MQTT ingestion
//     wired to a protocol-agnostic SourcePort; pipeline code has no MQTT import.
//     The topic + params are declared once via [ports.EventPattern] on the port
//     itself (ioports.Sensors); [ports.EventHandle] derives the *ChannelHandle
//     for the adapter — no separate events.NewChannel/Register step needed.
//   - [ports.SinkPort] + [adaptermqtt.PublishAdapter] — MQTT alert publishing
//     wired to a SinkPort (ioports.Alerts); supports fan-out to additional sinks.
//   - [ports.IOPort] + [sqladapter.QueryEachAdapter] — persistence as an
//     explicit intermediate IO step (ioports.Readings): the pipeline's forge
//     function stays PURE (payload → insert params); the save happens through
//     the port, whose adapter is chosen here. Table/Op metadata declared once
//     via [ports.SQLPattern].
//   - [ports.ToolPort] + [nethttp.PipelineAdapter] — GET /sensors/{sensorID}/readings
//     (ioports.HistoryTool, [ports.RESTPattern]): the tool pipeline Connects
//     through ioports.History (IOPort, SQLPattern) — REST layer and database
//     never meet directly.
//   - [ports.SinkPort] + [fileadapter.DrainWriteFileAdapter] — POST /export
//     (ioports.ExportTool): query through ioports.ExportQuery (SQLPattern),
//     write the snapshot through ioports.NewExportsPort's [ports.FilePattern]
//     ({exportID}.json); the response path comes from the SAME declaration
//     via [ports.FileHandle].BuildPath.
//   - [nethttp.HandlerLatest] — reactive cache endpoint; GET /readings/latest
//     returns the most recently saved reading without querying the DB.
//   - Validated-config factory pattern — main() loads domain.AlertConfig once
//     via format.FromEnv (APP_ALERT_THRESHOLD, default 50.0); the pipeline
//     functions close over the typed, validated config (see domain.NewShouldAlert).
//   - One [stats.NewFanout] observer across HTTP, MQTT, SQL, file, and stream.
//
// Run:
//
//	go run ./examples/sensor-service
//	APP_ALERT_THRESHOLD=90 go run ./examples/sensor-service
package main

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"

	_ "modernc.org/sqlite"

	fileadapter "github.com/DaniDeer/go-codex/adapters/file"
	adaptermqtt "github.com/DaniDeer/go-codex/adapters/mqtt"
	nethttp "github.com/DaniDeer/go-codex/adapters/nethttp"
	sqladapter "github.com/DaniDeer/go-codex/adapters/sql"
	"github.com/DaniDeer/go-codex/examples/sensor-service/adapters"
	"github.com/DaniDeer/go-codex/examples/sensor-service/db"
	"github.com/DaniDeer/go-codex/examples/sensor-service/domain"
	"github.com/DaniDeer/go-codex/examples/sensor-service/ioports"
	"github.com/DaniDeer/go-codex/examples/sensor-service/observability"
	"github.com/DaniDeer/go-codex/examples/sensor-service/pipeline"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/ports"
	"github.com/DaniDeer/go-codex/stats"
	gstream "github.com/DaniDeer/go-codex/stream"
)

// migrations are embedded here — embed paths are package-relative and the
// migrations directory sits at the example root, next to main.go.
//
//go:embed migrations/*.sql
var migrationsFS embed.FS

func main() {
	ctx, cancelPipeline := context.WithCancel(context.Background())

	// ── Observability (cross-cutting) ─────────────────────────────────────
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	counting := observability.NewCountingObserver()
	obs := stats.NewFanout(counting, stats.NewLoggingObserver(logger))
	// Store obs in the context once — MQTT, stream, sql, file, and HTTP
	// adapters all resolve it automatically when Options.Observer is nil.
	ctx = stats.WithObserver(ctx, obs)

	// ── Env config (validated-config factory pattern) ─────────────────────
	//
	// Load + validate config from env vars ONCE, here — the same place ports
	// and adapters are wired. domain.AlertConfigCodec is the env contract:
	// name (APP_ALERT_THRESHOLD), type coercion, MinFloat(0) constraint, and
	// the 50.0 default when unset. The pipeline factories close over the
	// typed config — pipeline code never touches os.Getenv.
	alertCfg, err := format.FromEnv(domain.AlertConfigCodec, "APP_ALERT_")
	must(err, "load alert config from env")

	// ── Database ───────────────────────────────────────────────────────────
	sqlDB, err := sql.Open("sqlite", "file::memory:?cache=private")
	must(err, "open db")
	defer sqlDB.Close()

	migrator, err := sqladapter.NewMigrator(sqlDB, migrationsFS, "migrations", "sqlite3")
	must(err, "new migrator")
	must(migrator.Up(ctx, sqladapter.MigrateOptions{}), "migrate up") // observer from ctx
	fmt.Println("✓ Migrations applied")

	store := adapters.NewReadingStore(db.New(sqlDB))

	// ── HTTP mux (declared early — HandlerLatest wires into it) ───────────
	mux := http.NewServeMux()

	// ── MQTT ingestion: bind the Sensors port ─────────────────────────────
	//
	// ioports.Sensors declared the topic + params once (ports.EventPattern,
	// registered internally against ioports.EventsBuilder). Here we derive
	// the handle and pick the transport — the ONLY place MQTT appears on the
	// inbound path.
	mqttClient := adapters.NewMockMQTTClient()
	readingHandle, ok := ports.EventHandle[domain.MQTTPayload](ioports.Sensors)
	if !ok {
		must(errors.New("ioports.Sensors: no EventPattern declared"), "derive reading channel handle")
	}
	ioports.Sensors.Bind(ctx, adaptermqtt.SubscribeAdapter(mqttClient, readingHandle, 0,
		format.JSON(domain.MQTTPayloadCodec),
		adaptermqtt.SubscribeAdapterOptions{TopicFilter: "sensors/+/data"}))
	sensors := ioports.Sensors.Stream(ctx)

	// ── Persistence: bind the Readings IO port ────────────────────────────
	//
	// The pipeline maps payloads to insert params with a PURE forge function
	// and persists through ioports.Readings — an IOPort whose adapter is
	// chosen HERE. sql.QueryEachAdapter runs the save closure per item and
	// validates each returned row through domain.ReadingCodec (the post-read
	// check); Table/Op default from the port's SQLPattern via context.
	must(ioports.Readings.Bind(ctx, sqladapter.QueryEachAdapter(domain.ReadingCodec,
		func(qctx context.Context, params db.InsertReadingParams) ([]db.Reading, error) {
			if err := store.Save(qctx, params); err != nil {
				return nil, err
			}
			row, err := store.Queries().GetReading(qctx, params.ID)
			if err != nil {
				return nil, err
			}
			return []db.Reading{row}, nil
		},
		sqladapter.QueryEachStreamOptions{}, // Table/Op default from the port's SQLPattern
	)), "bind readings persistence port")

	// ── Business logic: assemble the stream pipeline ──────────────────────
	//
	// pipeline.Build composes decode → map (pure) → persist (port) → tap →
	// tee → filter → alert over the port's stream. It sees only ports and the
	// validated config — zero adapter imports.
	res := pipeline.Build(ctx, sensors, pipeline.Deps{
		Persist: ioports.Readings,
		Cfg:     alertCfg,
		Logger:  logger,
	})

	// ── HTTP reactive cache: bind the Latest port ─────────────────────────
	//
	// ioports.Latest is a ports.LatestPort: Feed drains the stream into the
	// port's atomic cell; the bound adapter answers every GET from that cell —
	// no DB query per request. Before the first reading arrives the endpoint
	// returns 503 + NoLatestValueError. The cache outlives the stream, so the
	// endpoint keeps serving after the MQTT pipeline shuts down.
	latestHandle, ok := ports.RESTHandle[struct{}, db.Reading](ioports.Latest)
	if !ok {
		must(errors.New("ioports.Latest: no RESTPattern declared"), "derive latest route handle")
	}
	must(ioports.Latest.Bind(ctx, nethttp.LatestAdapter(mux, latestHandle, nethttp.Options{})),
		"bind latest port")
	go ioports.Latest.Feed(ctx, res.LatestReadings)

	// ── MQTT alert publishing: bind the Alerts port ────────────────────────
	//
	// Fan-out to additional sinks (e.g. SSE, file) requires only additional
	// Bind calls — no pipeline changes:
	//
	//	ioports.Alerts.Bind(ctx, file.DrainWriteAdapter(...)) // also write to file
	alertHandle, ok := ports.EventHandle[domain.SensorAlert](ioports.Alerts)
	if !ok {
		must(errors.New("ioports.Alerts: no EventPattern declared"), "derive alert channel handle")
	}
	ioports.Alerts.Bind(ctx, adaptermqtt.PublishAdapter(mqttClient, alertHandle,
		format.JSON(domain.AlertCodec),
		adaptermqtt.MQTTDrainPublishOptions{
			Vars: nil, // topic vars resolved per-item — alertHandle.Topic uses {sensorID}
			OnError: func(err error) {
				var sae gstream.StreamApplyError
				var sde gstream.StreamDecodeError
				switch {
				case errors.As(err, &sae):
					logger.Warn("stream apply error", "error", sae)
				case errors.As(err, &sde):
					logger.Warn("stream decode error", "error", sde)
				default:
					logger.Warn("alert publish error", "error", err)
				}
			},
		}))
	pipelineDone := make(chan struct{})
	go func() {
		defer close(pipelineDone)
		ioports.Alerts.Feed(ctx, res.AlertPayloads)
	}()

	fmt.Println("✓ Stream pipeline active: MQTT → decode → map (pure) → persist (port) → tee → filter → alert")

	// ── Time series: bind the History port + tool port ────────────────────
	//
	// GET /sensors/{sensorID}/readings is a ToolPort whose pipeline Connects
	// through the History SQL port — REST layer and database never meet
	// directly. The route was declared once (ports.RESTPattern on
	// ioports.HistoryTool, registered against ioports.RESTBuilder); the SQL
	// metadata once (ports.SQLPattern on ioports.History).
	must(ioports.History.Bind(ctx, sqladapter.QueryEachAdapter(domain.TimeSeriesCodec,
		func(qctx context.Context, q domain.SensorQuery) ([]domain.TimeSeries, error) {
			rows, err := store.Queries().ListReadingsBySensor(qctx, q.SensorID)
			if err != nil {
				return nil, err
			}
			return []domain.TimeSeries{{SensorID: q.SensorID, Readings: rows}}, nil
		},
		sqladapter.QueryEachStreamOptions{}, // Table/Op default from the port's SQLPattern
	)), "bind history port")

	ioports.HistoryTool.SetPipeline(pipeline.NewTimeSeriesPipeline(ioports.History,
		func(ctx context.Context) string {
			r, _ := nethttp.RequestFromContext(ctx)
			return r.PathValue("sensorID") // already codec-validated by the RESTPattern's PathParam
		}))
	historyHandle, ok := ports.RESTHandle[struct{}, domain.TimeSeries](ioports.HistoryTool)
	if !ok {
		must(errors.New("ioports.HistoryTool: no RESTPattern declared"), "derive history route handle")
	}
	must(ioports.HistoryTool.Bind(ctx, nethttp.PipelineAdapter(mux, historyHandle,
		nethttp.PipelineAdapterOptions{})), "bind history tool port")

	// ── Export: SQL query port → file sink port → tool port ───────────────
	//
	// POST /export: query all readings (ExportQuery, SQLPattern), hand the
	// snapshot to the Exports SinkPort (FilePattern {exportID}.json +
	// file.DrainWriteFileAdapter), and answer with the deterministic path
	// from the SAME FilePattern declaration (FileHandle.BuildPath).
	// Exports land in ./exports next to this example's README so the file
	// output is inspectable after a run (wiped and recreated per run;
	// gitignored). Resolved from this source file's location — independent
	// of the working directory `go run` was started from.
	exportDir := exportsDir()
	must(os.RemoveAll(exportDir), "clean export dir")
	must(os.MkdirAll(exportDir, 0o750), "create export dir")

	must(ioports.ExportQuery.Bind(ctx, sqladapter.QueryEachAdapter(domain.ExportSnapshotCodec,
		func(qctx context.Context, _ domain.ExportRequest) ([]domain.ExportSnapshot, error) {
			rows, err := store.Queries().ListReadings(qctx)
			if err != nil {
				return nil, err
			}
			return []domain.ExportSnapshot{domain.NewExportSnapshot(rows)}, nil
		},
		sqladapter.QueryEachStreamOptions{}, // Table/Op default from the port's SQLPattern
	)), "bind export query port")

	exportsPort, err := ioports.NewExportsPort(exportDir)
	must(err, "construct exports port")
	exportFile, ok := ports.FileHandle[domain.ExportSnapshot](exportsPort)
	if !ok {
		must(errors.New("exports port: no FilePattern declared"), "derive export file handle")
	}
	// exportCtx is independent of the MQTT pipeline ctx (which the demo
	// cancels early) — the export sink must outlive it. Same observer.
	exportCtx := stats.WithObserver(context.Background(), obs)

	exportVars := func(s domain.ExportSnapshot) map[string]string {
		return map[string]string{"exportID": s.ExportID}
	}
	exportsPort.Bind(exportCtx, fileadapter.DrainWriteFileAdapter(exportFile, exportVars,
		fileadapter.DrainWriteFileAdapterOptions{}))

	// Request-driven feed: Start owns the channel and drain goroutine that
	// used to be hand-rolled here; the export tool pipeline submits snapshots
	// with Push, and Close (deferred below) waits for the file adapter to
	// finish draining.
	exportsPort.Start(exportCtx)

	ioports.ExportTool.SetPipeline(pipeline.NewExportPipeline(ioports.ExportQuery,
		func(s domain.ExportSnapshot) { _ = exportsPort.Push(exportCtx, s) },
		func(s domain.ExportSnapshot) (string, error) { return exportFile.BuildPath(exportVars(s)) }))
	exportHandle, ok := ports.RESTHandle[domain.ExportRequest, domain.ExportResult](ioports.ExportTool)
	if !ok {
		must(errors.New("ioports.ExportTool: no RESTPattern declared"), "derive export route handle")
	}
	must(ioports.ExportTool.Bind(ctx, nethttp.PipelineAdapter(mux, exportHandle,
		nethttp.PipelineAdapterOptions{})), "bind export tool port")

	// ── HTTP — register remaining routes ──────────────────────────────────
	nethttp.Register(mux, ioports.CreateHandle, adapters.NewCreateHandler(store), nethttp.Options{})
	nethttp.Register(mux, ioports.GetHandle, adapters.NewGetHandler(store), nethttp.Options{})

	// Wrap with ObserverMiddleware so every HTTP request gets obs injected
	// into r.Context() — handlers resolve the observer per-request.
	srvHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(stats.WithObserver(r.Context(), obs))
		mux.ServeHTTP(w, r)
	})
	srv := httptest.NewServer(srvHandler)
	defer srv.Close()
	fmt.Printf("✓ HTTP server started: %s\n\n", srv.URL)

	// ── Run the demo scenario ──────────────────────────────────────────────
	runDemo(demoEnv{
		ctx:            ctx,
		cancelPipeline: cancelPipeline,
		pipelineDone:   pipelineDone,
		mqttClient:     mqttClient,
		srvURL:         srv.URL,
		cfg:            alertCfg,
		counting:       counting,
		buildParams:    res.BuildParams,
	})

	// Shut down the export sink: Close waits for in-flight Push calls and
	// for the file adapter to finish draining.
	must(exportsPort.Close(), "close exports port")
}

// exportsDir returns <this source file's directory>/exports, so the demo's
// file output sits next to the README regardless of the caller's cwd.
func exportsDir() string {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return "exports" // fallback: relative to cwd
	}
	return filepath.Join(filepath.Dir(thisFile), "exports")
}

func must(err error, msg string) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL %s: %v\n", msg, err)
		os.Exit(1)
	}
}
