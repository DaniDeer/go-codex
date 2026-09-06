// Command sensor-service is the go-codex flagship example: a small but
// complete sensor-readings service structured as a real project, with each
// concern in its own package:
//
//	domain/         — Layer 1+2: models, codecs, field factories, constraints,
//	                  pure business rules (validated-config factories included)
//	pipeline/       — business logic: pure mapping functions + the segmented
//	                  MQTT pipeline (ports.Chain/ChainStream over boundary
//	                  SourcePort/SinkPort + internal PipePort stages),
//	                  parameterized by the consumer-defined Store interface
//	ioports/        — the service's complete IO surface: every boundary
//	                  follows the SAME three-step model (declare port shape →
//	                  plug in a Pattern → bind an adapter); EventPattern/
//	                  SQLPattern/FilePattern/RESTPattern + REST routes
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
//   - [ports.SourcePort] + [ports.SourcePort.PluginEventPattern] +
//     [adaptermqtt.SubscribeAdapter] — MQTT ingestion wired to the
//     pipeline's first stage (ioports.Sensors); pipeline code has no MQTT
//     import. The topic + params are declared once as a standalone value
//     (ioports.SensorsPattern), plugged in at wiring time — PluginEventPattern
//     registers it AND returns the typed handle in one call, no separate
//     events.NewChannel/Register step needed.
//   - [ports.SinkPort] + [ports.SinkPort.PluginEventPattern] +
//     [adaptermqtt.PublishAdapter] — MQTT alert publishing wired to the
//     pipeline's LAST stage (ioports.Alerts); supports fan-out to
//     additional sinks via additional Bind calls.
//   - [ports.Chain] + [ports.ChainStream] — pipeline.Build segments the MQTT
//     pipeline into named stages (Sensors → Params → Saved → Alerts) using
//     the SAME call shape whether the endpoint is a boundary SourcePort/
//     SinkPort or an internal PipePort. Persistence is its OWN Params→Saved
//     edge — [ports.IOPort] + [sqladapter.QueryEachAdapter] (ioports.Readings):
//     the pipeline's mapping function stays PURE (payload → insert params);
//     the save happens through the port, whose adapter is chosen here,
//     INSIDE that edge's transform — never buried inside a bigger,
//     multi-purpose stage. Table/Op metadata declared once via
//     [ports.SQLPattern], plugged in via [ports.NewSQLPort] (ioports.Readings/
//     History/ExportQuery are all declared this way — a thin convenience
//     constructor combining "declare port" + "plug in SQLPattern").
//   - [ports.PipelineSpec] — the MQTT pipeline's shape (pipe/port names,
//     buffer sizes, bound adapter identities, every Chain/ChainStream edge
//     with its transform's real Go function identity) derived directly from
//     the four stages, printed in demo.go — no hand-typed topology to keep
//     in sync.
//   - [ports.ToolPort] + [nethttp.PipelineAdapter] — GET /sensors/{sensorID}/readings
//     (ioports.HistoryTool, declared via [ports.NewRestToolPort]): the tool
//     pipeline Connects through ioports.History (IOPort, SQLPattern) — REST
//     layer and database never meet directly.
//   - [ports.SinkPort] + [fileadapter.DrainWriteFileAdapter] — POST /export
//     (ioports.ExportTool): query through ioports.ExportQuery (SQLPattern),
//     write the snapshot through ioports.NewExportsPort's [ports.FilePattern]
//     ({exportID}.json); the response path comes from the SAME declaration
//     via File.BuildPath.
//   - [nethttp.HandlerLatest] — reactive cache endpoint; GET /readings/latest
//     returns the most recently saved reading without querying the DB.
//   - Validated-config factory pattern — main() loads domain.AlertConfig once
//     via config.FromEnv (APP_ALERT_THRESHOLD, default 50.0); the pipeline
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
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"time"

	_ "modernc.org/sqlite"

	fileadapter "github.com/DaniDeer/go-codex/adapters/file"
	adaptermqtt "github.com/DaniDeer/go-codex/adapters/mqtt"
	nethttp "github.com/DaniDeer/go-codex/adapters/nethttp"
	sqladapter "github.com/DaniDeer/go-codex/adapters/sql"
	"github.com/DaniDeer/go-codex/app"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/config"
	"github.com/DaniDeer/go-codex/examples/sensor-service/adapters"
	"github.com/DaniDeer/go-codex/examples/sensor-service/db"
	"github.com/DaniDeer/go-codex/examples/sensor-service/domain"
	"github.com/DaniDeer/go-codex/examples/sensor-service/ioports"
	"github.com/DaniDeer/go-codex/examples/sensor-service/observability"
	"github.com/DaniDeer/go-codex/examples/sensor-service/pipeline"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/stats"
	gstream "github.com/DaniDeer/go-codex/stream"
)

// migrations are embedded here — embed paths are package-relative and the
// migrations directory sits at the example root, next to main.go.
//
//go:embed migrations/*.sql
var migrationsFS embed.FS

func main() {
	// ── Observability + lifecycle (cross-cutting) ──────────────────────────
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	counting := observability.NewCountingObserver()
	obs := stats.NewFanout(counting, stats.NewLoggingObserver(logger))

	// app.New owns the root context (observer pre-injected — MQTT, stream,
	// sql, file, and HTTP adapters all resolve it automatically when their
	// Options.Observer is nil) and the ordered teardown: hooks registered
	// with a.OnShutdown run LIFO when the demo calls a.Shutdown() at the end.
	// A real service would call a.Run(ctx) instead and get SIGINT/SIGTERM
	// handling for free — same teardown path.
	a := app.New(app.Options{Observer: obs, Logger: logger})

	// The MQTT pipeline gets its own child context: the demo cancels it
	// mid-run (after scene 1) while the HTTP ports keep serving on the
	// app context.
	ctx, cancelPipeline := context.WithCancel(a.Context())

	// ── Env config (validated-config factory pattern) ─────────────────────
	//
	// Load + validate config from env vars ONCE, here — the same place ports
	// and adapters are wired. domain.AlertConfigCodec is the env contract:
	// name (APP_ALERT_THRESHOLD), type coercion, MinFloat(0) constraint, and
	// the 50.0 default when unset. The pipeline factories close over the
	// typed config — pipeline code never touches os.Getenv.
	//
	// config.FromEnv returns a *codex.Immutable[T], not a bare struct — the
	// loaded config is frozen from this point on (a second FromEnv call
	// targeting the SAME instance would fail, enforced by the type, not
	// just by convention). This demo extracts the plain value once via
	// Get() immediately, since the structs below want alertCfg BY VALUE.
	alertImmutable, err := config.FromEnv(domain.AlertConfigCodec, "APP_ALERT_")
	must(err, "load alert config from env")
	alertCfg := alertImmutable.Get()

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

	// ── MQTT ingestion: plug in Sensors' EventPattern, bind the adapter ────
	//
	// ioports.Sensors is a plain SourcePort — the pipeline's first stage AND
	// the MQTT ingestion boundary. ioports.SensorsPattern declared the topic
	// + params once, as a standalone value; PluginEventPattern registers it
	// (against the SAME ioports.EventsClient the port was constructed with
	// — its channel shows up in the same printed AsyncAPI spec as every
	// hand-declared channel) AND returns the typed handle in one call. This
	// is the ONLY place MQTT appears on the inbound path.
	mqttClient := adapters.NewMockMQTTClient()
	readingHandle, err := ioports.Sensors.PluginEventPattern(ioports.SensorsPattern)
	must(err, "plug in sensors EventPattern")
	ioports.Sensors.Bind(ctx, adaptermqtt.SubscribeAdapter(mqttClient, readingHandle, 0,
		format.JSON(domain.MQTTPayloadCodec),
		adaptermqtt.SubscribeAdapterOptions{TopicFilter: "sensors/+/data"}))

	// ── MQTT alert publishing: plug in Alerts' EventPattern, bind adapter ──
	//
	// ioports.Alerts is a plain SinkPort — the pipeline's LAST stage AND the
	// MQTT egress boundary. Fan-out to additional sinks (e.g. SSE, file)
	// requires only additional Bind calls — no pipeline changes:
	//
	//	ioports.Alerts.Bind(ctx, file.DrainWriteAdapter(...))
	alertHandle, err := ioports.Alerts.PluginEventPattern(ioports.AlertsPattern)
	must(err, "plug in alerts EventPattern")
	ioports.Alerts.Bind(ctx, adaptermqtt.PublishAdapter(mqttClient, alertHandle,
		format.JSON(domain.AlertCodec),
		adaptermqtt.MQTTDrainPublishOptions{
			Vars:    nil, // topic vars resolved per-item — alertHandle.Topic uses {sensorID}
			OnError: gstream.LogOnError(logger, "alert publish"),
		}))

	// ── Persistence: bind the Readings IO port ────────────────────────────
	//
	// The pipeline maps payloads to insert params with a PURE function and
	// persists through ioports.Readings — an IOPort whose adapter is chosen
	// HERE. sql.QueryEachAdapter runs the save closure per item and
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

	// ── Business logic: assemble the segmented pipeline ───────────────────
	//
	// pipeline.Build registers Sensors→Params (Chain, pure map), Params→Saved
	// (ChainStream, the SQL persistence hop through ioports.Readings — its
	// own real edge, not hidden inside a bigger transform), and
	// Saved→Alerts (ChainStream, filter+alert) — all BEFORE Params/Saved's
	// Connect calls, per the ordering rule. It sees only ports and the
	// validated config — zero adapter imports.
	res := pipeline.Build(ctx, ioports.Sensors, ioports.Alerts, pipeline.Deps{
		Persist: ioports.Readings,
		Cfg:     alertCfg,
		Logger:  logger,
	})

	// Params/Saved are the only two values here with their OWN Connect
	// method (internal PipePort stages) — Sensors/Alerts (boundary ports)
	// only needed their adapter Bind above, already done.
	pipeline.Params.Connect(ctx)
	pipeline.Saved.Connect(ctx)

	// ── HTTP reactive cache: bind the Latest port ─────────────────────────
	//
	// ioports.Latest is a ports.LatestPort: Feed drains the stream into the
	// port's atomic cell; the bound adapter answers every GET from that cell —
	// no DB query per request. Before the first reading arrives the endpoint
	// returns 503 + NoLatestValueError. The cache outlives the stream, so the
	// endpoint keeps serving after the MQTT pipeline shuts down.
	must(ioports.Latest.Bind(ctx, nethttp.LatestAdapter(mux, ioports.LatestHandle, nethttp.Options{})),
		"bind latest port")
	go ioports.Latest.Feed(ctx, res.LatestReadings)

	// pipelineDone comes from pipeline.Build's Result.Done — a lifecycle
	// signal tracking the whole segmented pipeline's actual drain (see
	// pipeline.Result's doc comment), since Sensors/Alerts (plain boundary
	// ports) have no Done() of their own the way an internal PipePort does.
	pipelineDone := res.Done

	fmt.Println("✓ Stream pipeline active: MQTT → decode → map (pure) → persist (port) → tee → filter → alert")

	// ── Time series: bind the History port + tool port ────────────────────
	//
	// GET /sensors/{sensorID}/readings is a ToolPort whose pipeline Connects
	// through the History SQL port — REST layer and database never meet
	// directly. The route (ports.RESTPattern) and the SQL metadata
	// (ports.SQLPattern) were both plugged in at declaration time in
	// ioports.go via the NewRestToolPort/NewSQLPort convenience
	// constructors; main() takes the already-derived handles directly.
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
			// ToolPort's Req here is struct{} (the pipeline input function
			// derives sensorID from the request context directly, not from
			// a decoded Req field), so rest.NewPathParam's automatic merge
			// doesn't apply to this ToolPort/pipeline shape — it requires a
			// typed Req struct field to merge into. r.PathValue("sensorID")
			// is still codec-validated by the RESTPattern's PathParam
			// before this closure runs. See examples/rest-api's
			// handlers.MakeGetUserHandler for the automatic-merge pattern
			// on a typed Req.
			r, _ := nethttp.RequestFromContext(ctx)
			return r.PathValue("sensorID")
		}))
	must(ioports.HistoryTool.Bind(ctx, nethttp.PipelineAdapter(mux, ioports.HistoryToolHandle,
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

	exportsPort, exportFile := codex.Must2(ioports.NewExportsPort(exportDir))
	// The export sink lives on the APP context — independent of the MQTT
	// pipeline ctx (which the demo cancels early); it must outlive it.
	exportCtx := a.Context()

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
	// Teardown is declared right where the lifecycle starts: Close waits for
	// in-flight Push calls and the file adapter's drain.
	a.OnShutdown("exports-port", func(context.Context) error { return exportsPort.Close() })

	ioports.ExportTool.SetPipeline(pipeline.NewExportPipeline(ioports.ExportQuery,
		func(s domain.ExportSnapshot) { _ = exportsPort.Push(exportCtx, s) },
		func(s domain.ExportSnapshot) (string, error) { return exportFile.BuildPath(exportVars(s)) }))
	must(ioports.ExportTool.Bind(ctx, nethttp.PipelineAdapter(mux, ioports.ExportToolHandle,
		nethttp.PipelineAdapterOptions{})), "bind export tool port")

	// ── HTTP — register remaining routes ──────────────────────────────────
	// ioports.CreateHandle/GetHandle are PRE-REGISTERED onto the
	// package-level ioports.RESTBuilder at var-init time (see
	// examples/sensor-service/ioports: CreateHandle :=
	// CreateRoute.RegisterHandle(RESTBuilder)), before any handler function
	// exists (store isn't constructed until here in main()).
	// *RouteHandle.WithHandler is Route.WithHandler's post-registration
	// equivalent for exactly this case: attach the handler now that store
	// exists, then let the ONE nethttp.Serve(mux, RESTBuilder) call below
	// wire it — same mechanism, different attachment order. The other
	// RESTBuilder entries (ExportToolHandle etc.) were already wired
	// directly by their own port adapters (RegisterPipeline) without ever
	// setting HandlerFn, so Serve's Part-1 gate skips them here — no
	// double registration.
	ioports.CreateHandle.WithHandler(adapters.NewCreateHandler(store))
	ioports.GetHandle.WithHandler(adapters.NewGetHandler(store))
	// AttachMux wires ioports.RESTBuilder's routes onto mux — the actual
	// wiring happens inside restCtx-scoped Serve below, which also owns its
	// own (unused-here) *http.Server; the wrapped srvHandler + httptest
	// server below is the ACTUAL listener demo requests hit, since
	// AttachMux's Handler must be the bare mux while every request here
	// still needs obs injected into its context first.
	restAddr := mustFreeAddr()
	must(nethttp.AttachMux(ioports.RESTBuilder, mux, restAddr), "AttachMux create/get routes")
	restCtx, restCancel := context.WithCancel(context.Background())
	go func() { _ = ioports.RESTBuilder.Serve(restCtx) }()
	waitForReady(restAddr) // blocks until AttachMux's wiring above has completed

	// Wrap with ObserverMiddleware so every HTTP request gets obs injected
	// into r.Context() — handlers resolve the observer per-request.
	srvHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(stats.WithObserver(r.Context(), obs))
		mux.ServeHTTP(w, r)
	})
	srv := httptest.NewServer(srvHandler)
	a.OnShutdown("http-server", func(context.Context) error { restCancel(); srv.Close(); return nil })
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
	})

	// Ordered teardown: hooks run LIFO (http-server, then exports-port —
	// exports Close waits for in-flight Push calls and the file adapter's
	// drain). A real service would reach this line via a.Run(ctx) on
	// SIGINT/SIGTERM instead.
	must(a.Shutdown(), "app shutdown")
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

// mustFreeAddr reserves an OS-assigned free TCP port on localhost, then
// releases it immediately so AttachMux's own *http.Server can bind to it.
func mustFreeAddr() string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintf(os.Stderr, "reserve free port failed: %v\n", err)
		os.Exit(1)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

// waitForReady polls addr until it accepts TCP connections — b.Serve wires
// mux synchronously before starting its listener goroutine, so a successful
// dial here guarantees mux is fully wired.
func waitForReady(addr string) {
	for range 100 {
		conn, err := net.DialTimeout("tcp", addr, 50*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}
