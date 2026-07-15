// Package pipeline is the sensor service's business logic layer: forge
// functions and the stream topology. It composes domain rules over typed
// streams and depends only on domain + the go-codex core — no adapters, no
// concrete clients, no IO of its own.
//
// Persistence is NOT hidden inside a forge function: forge functions stay
// pure (payload → insert params), and the save happens through a
// protocol-agnostic [ports.IOPort] (ioports.Readings) that main() binds to a
// concrete adapter. The pipeline sees only the port.
package pipeline

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/DaniDeer/go-codex/examples/sensor-service/db"
	"github.com/DaniDeer/go-codex/examples/sensor-service/domain"
	"github.com/DaniDeer/go-codex/forge"
	"github.com/DaniDeer/go-codex/ports"
	gstream "github.com/DaniDeer/go-codex/stream"
)

// Deps carries everything the pipeline is parameterized by: the persistence
// port (declared in ioports, bound to a concrete adapter in main()), the
// validated env config (see domain.AlertConfigCodec), and a logger for
// domain-event taps.
type Deps struct {
	// Persist is the intermediate-IO persistence boundary: insert params in,
	// stored row out. The pipeline never knows whether it is backed by SQL,
	// HTTP, or a test adapter.
	Persist *ports.IOPort[db.InsertReadingParams, db.Reading]
	Cfg     domain.AlertConfig
	Logger  *slog.Logger
}

// NewBuildInsertParams builds the PURE forge function
// MQTTPayload → db.InsertReadingParams: map the payload to insert params
// (domain rule — assigns ID + RecordedAt). No IO — persistence happens
// through Deps.Persist, not inside the function.
func NewBuildInsertParams() *forge.Function[domain.MQTTPayload, db.InsertReadingParams] {
	return forge.NewFunction(
		"buildInsertParams", "1.0.0",
		domain.MQTTPayloadCodec, domain.InsertParamsCodec,
		func(payload domain.MQTTPayload) (db.InsertReadingParams, error) {
			return domain.BuildInsertParamsFromMQTT(payload), nil
		},
		forge.FunctionMeta{Description: "Map MQTT sensor payload to SQL insert params (assigns ID + RecordedAt)."},
	)
}

// Result exposes the pipeline's two outbound streams plus the forge function
// used, for topology documentation.
type Result struct {
	// LatestReadings feeds the HTTP reactive cache (GET /readings/latest).
	LatestReadings gstream.Stream[db.Reading]
	// AlertPayloads feeds the alerts sink port.
	AlertPayloads gstream.Stream[domain.SensorAlert]
	// BuildParams is the pure forge function applied to every sensor payload —
	// exposed for stream.Topology documentation.
	BuildParams *forge.Function[domain.MQTTPayload, db.InsertReadingParams]
}

// Build assembles the stream topology over an already-active sensors stream:
//
//	sensors → Apply(buildInsertParams, pure) → Persist port (IO) → Tap(log)
//	        → Tee ─→ LatestReadings
//	              └→ Filter(shouldAlert) → FlatMap(buildAlert) → AlertPayloads
//
// Each operator is a typed free function — compose like Unix pipes. The
// observer is resolved from ctx by every operator (ApplyOptions.Observer nil).
func Build(ctx context.Context, sensors gstream.Stream[domain.MQTTPayload], deps Deps) Result {
	buildParams := NewBuildInsertParams()
	shouldAlert := domain.NewShouldAlert(deps.Cfg)
	buildAlert := domain.NewBuildAlert(deps.Cfg)

	// Pure transformation: payload → insert params. Zero IO.
	params := gstream.Apply(ctx, sensors, buildParams,
		gstream.ApplyOptions{}) // observer from ctx

	// Persistence as an explicit IO step THROUGH the port — the stored row
	// comes back out. Swapping SQL → HTTP → test double changes only the
	// Bind call in main().
	readings := deps.Persist.Connect(ctx, params)

	// Tap: domain event observation — log every stored reading.
	readings = gstream.Tap(ctx, readings, func(r db.Reading) {
		deps.Logger.Info("reading saved", "sensor", r.SensorID, "value", r.Value, "unit", r.Unit)
	})

	// Tee: fan-out — one copy feeds the HTTP reactive cache, one feeds alerting.
	latestReadings, alertReadings := gstream.Tee(ctx, readings)

	// Filter: keep only readings that cross the alert threshold.
	aboveThreshold := gstream.Filter(ctx, alertReadings, shouldAlert)

	// Convert db.Reading → SensorAlert for the MQTT alert topic.
	alertPayloads := gstream.FlatMapSlice(ctx, aboveThreshold,
		func(r db.Reading) []domain.SensorAlert { return []domain.SensorAlert{buildAlert(r)} })

	return Result{
		LatestReadings: latestReadings,
		AlertPayloads:  alertPayloads,
		BuildParams:    buildParams,
	}
}

// NewTimeSeriesPipeline builds the GET /sensors/{sensorID}/readings tool
// pipeline: extract the (already codec-validated) sensorID via sensorIDFor,
// then Connect through the History SQL port — the REST layer never touches
// the database.
//
// sensorIDFor decouples this package from HTTP: main() supplies a closure
// over nethttp.RequestFromContext, tests can supply anything.
func NewTimeSeriesPipeline(
	history *ports.IOPort[domain.SensorQuery, domain.TimeSeries],
	sensorIDFor func(ctx context.Context) string,
) func(ctx context.Context, _ struct{}) gstream.Stream[domain.TimeSeries] {
	return func(ctx context.Context, _ struct{}) gstream.Stream[domain.TimeSeries] {
		query := gstream.Single(ctx, domain.SensorQuery{SensorID: sensorIDFor(ctx)})
		return history.Connect(ctx, query)
	}
}

// NewBuildExportResult builds the PURE forge function
// ExportSnapshot → ExportResult. pathFor is pure computation (FilePattern
// template substitution + path-param validation via FileHandle.BuildPath —
// no IO): the SAME declaration that writes the file names the response path.
func NewBuildExportResult(
	pathFor func(domain.ExportSnapshot) (string, error),
) *forge.Function[domain.ExportSnapshot, domain.ExportResult] {
	return forge.NewFunction(
		"buildExportResult", "1.0.0",
		domain.ExportSnapshotCodec, domain.ExportResultCodec,
		func(s domain.ExportSnapshot) (domain.ExportResult, error) {
			path, err := pathFor(s)
			if err != nil {
				return domain.ExportResult{}, err
			}
			return domain.ExportResult{File: path, Count: int64(len(s.Readings))}, nil
		},
		forge.FunctionMeta{Description: "Map export snapshot to its response (file path from the FilePattern declaration + reading count)."},
	)
}

// NewExportPipeline builds the POST /export tool pipeline:
//
//	ExportRequest → ExportQuery port (SQL: all readings → snapshot)
//	             → submit (hand the snapshot to the Exports file sink)
//	             → Apply(buildExportResult, pure)
//
// submit decouples this package from the file adapter: main() wires it to
// the channel feeding the Exports SinkPort.
func NewExportPipeline(
	query *ports.IOPort[domain.ExportRequest, domain.ExportSnapshot],
	submit func(domain.ExportSnapshot),
	pathFor func(domain.ExportSnapshot) (string, error),
) func(ctx context.Context, req domain.ExportRequest) gstream.Stream[domain.ExportResult] {
	buildResult := NewBuildExportResult(pathFor)
	return func(ctx context.Context, req domain.ExportRequest) gstream.Stream[domain.ExportResult] {
		snapshots := query.Connect(ctx, gstream.Single(ctx, req))
		// Hand each snapshot to the file sink (async IO boundary).
		snapshots = gstream.Tap(ctx, snapshots, submit)
		return gstream.Apply(ctx, snapshots, buildResult,
			gstream.ApplyOptions{}) // observer from ctx
	}
}

// Topology documents the pipeline shape as a stream.Topology spec — the same
// structure Build assembles, described declaratively for printing/inspection.
func Topology(cfg domain.AlertConfig, buildParams *forge.Function[domain.MQTTPayload, db.InsertReadingParams]) *gstream.Topology {
	topo := gstream.NewTopology("Sensor Service MQTT Pipeline", "1.0.0").
		WithDescription("Real-time sensor readings: decode → map → persist → filter → alert.").
		WithSource("mqtt/sensors/+/data", "Raw MQTT payloads from sensor network")
	gstream.WithApply(topo, buildParams)
	topo.WithTap("persist via sql/readings/save IOPort — stored row re-emitted (1→1)").
		WithFilter(fmt.Sprintf("value > %.0f (alert threshold)", cfg.Threshold)).
		WithSink("mqtt/alerts/{sensorID}", "Low-performance alert events")
	return topo
}
