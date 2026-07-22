// Package pipeline is the sensor service's business logic layer: forge
// functions and the stream topology. It composes domain rules over typed
// streams and depends only on domain + the go-codex core — no adapters, no
// concrete clients, no IO of its own.
//
// The MQTT pipeline is segmented into named [ports.PipePort] stages wired by
// [ports.Chain]/[ports.ChainStream] (Raw → Params → Saved → AlertStage).
// Raw and AlertStage are declared in ioports (they carry the MQTT
// EventPattern — the actual IO/adapter boundary); Params and Saved are
// declared HERE, as pipeline-internal stages with no adapters of their own.
// pipeline never imports ioports (see main.go's import-direction doc
// comment: pipeline → domain only) — Build takes raw/alertStage as
// parameters instead, exactly as it previously took a plain
// gstream.Stream[MQTTPayload].
//
// Persistence is NOT hidden inside a forge function, and — per this
// package's PipePort segmentation — NOT hidden inside a larger transform's
// closure either: forge functions stay pure (payload → insert params), and
// the save happens through a protocol-agnostic [ports.IOPort]
// (ioports.Readings) Connected inside its OWN Params→Saved ChainStream edge,
// visible to [ports.PipelineSpec] as a real edge between two named pipes —
// never buried alongside unrelated taps/filters in one bigger stage.
package pipeline

import (
	"context"
	"log/slog"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/examples/sensor-service/db"
	"github.com/DaniDeer/go-codex/examples/sensor-service/domain"
	"github.com/DaniDeer/go-codex/forge"
	"github.com/DaniDeer/go-codex/ports"
	gstream "github.com/DaniDeer/go-codex/stream"
)

// Params is the pipeline-internal stage between the pure
// MQTTPayload→InsertReadingParams map and the SQL persistence hop. No
// adapters bind to it directly — it exists purely to give the persistence
// edge (Params → Saved) a real, named boundary on both sides, so
// [ports.PipelineSpec] can describe it without hiding it inside a larger
// transform's closure.
var Params = codex.Must(ports.NewPipePort[db.InsertReadingParams](
	"params", domain.InsertParamsCodec, ports.PortOptions{}))

// Saved is the pipeline-internal stage AFTER persistence: the stored row,
// re-emitted by ioports.Readings' IOPort.Connect call inside the
// Params→Saved ChainStream transform. Feeds the HTTP reactive cache
// (via Saved.Stream) and the alerting stage (via the Saved→AlertStage
// ChainStream edge).
var Saved = codex.Must(ports.NewPipePort[db.Reading](
	"saved", domain.ReadingCodec, ports.PortOptions{}))

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

// Result exposes the pipeline's HTTP-facing stream plus the forge function
// used, for topology documentation.
type Result struct {
	// LatestReadings feeds the HTTP reactive cache (GET /readings/latest).
	LatestReadings gstream.Stream[db.Reading]
	// BuildParams is the pure forge function applied to every sensor payload —
	// exposed for spec documentation (see [ports.PipelineSpec] usage in demo.go).
	BuildParams *forge.Function[domain.MQTTPayload, db.InsertReadingParams]
}

// Build wires the segmented MQTT pipeline as three [ports.Chain]/
// [ports.ChainStream] edges over already-declared PipePort stages:
//
//	raw (ioports.Raw) --Chain(buildParams, pure)--> Params
//	Params --ChainStream(Persist.Connect + Tap(log))--> Saved
//	Saved --ChainStream(Filter+FlatMap)--> alertStage (ioports.AlertStage)
//
// raw and alertStage are parameters, not package vars, so this package never
// imports ioports (see the package doc comment on the import-direction
// rule) — main() passes ioports.Raw/ioports.AlertStage in, exactly as it
// previously passed a plain gstream.Stream[MQTTPayload].
//
// The persistence step (Params → Saved) is deliberately its OWN
// ChainStream edge — not folded into the Raw→Params or Saved→AlertStage
// transforms — so [ports.PipelineSpec] sees the SQL hop as a real edge
// between two named pipes, never hidden inside a bigger transform's
// closure (see the package doc comment).
//
// Caller must Connect raw, Params, Saved, and alertStage AFTER calling
// Build (Chain/ChainStream registrations must precede the UPSTREAM pipe's
// Connect — see [ports.Chain]'s ordering rule).
func Build(
	ctx context.Context,
	raw *ports.PipePort[domain.MQTTPayload],
	alertStage *ports.PipePort[domain.SensorAlert],
	deps Deps,
) Result {
	buildParams := NewBuildInsertParams()
	shouldAlert := domain.NewShouldAlert(deps.Cfg)
	buildAlert := domain.NewBuildAlert(deps.Cfg)

	// Stage 1: Raw → Params — pure transformation, zero IO.
	ports.Chain(ctx, raw, buildParams.Apply, Params)

	// Stage 2: Params → Saved — the persistence hop THROUGH the port, its
	// own real edge. The stored row comes back out; swapping SQL → HTTP →
	// test double changes only the Bind call in main(), same as before.
	ports.ChainStream(ctx, Params, func(s gstream.Stream[db.InsertReadingParams]) gstream.Stream[db.Reading] {
		saved := deps.Persist.Connect(ctx, s)
		// Tap: domain event observation — log every stored reading.
		return gstream.Tap(ctx, saved, func(r db.Reading) {
			deps.Logger.Info("reading saved", "sensor", r.SensorID, "value", r.Value, "unit", r.Unit)
		})
	}, Saved)

	// Stage 3: Saved → AlertStage — filter to threshold breaches, map to
	// SensorAlert for the MQTT alert topic.
	ports.ChainStream(ctx, Saved, func(s gstream.Stream[db.Reading]) gstream.Stream[domain.SensorAlert] {
		aboveThreshold := gstream.Filter(ctx, s, shouldAlert)
		return gstream.FlatMapSlice(ctx, aboveThreshold,
			func(r db.Reading) []domain.SensorAlert { return []domain.SensorAlert{buildAlert(r)} })
	}, alertStage)

	// The HTTP reactive cache taps Saved directly — a fourth, independent
	// Stream() consumer registered on the SAME pipe (must precede Saved's
	// own Connect, same ordering rule as Chain/ChainStream).
	latestReadings := Saved.Stream(ctx)

	return Result{
		LatestReadings: latestReadings,
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

// Spec generation (Gap 4 of docs/roadmap/pipe-port-composition-hardening.md)
// no longer lives here as a hand-typed pipeline.Topology function — it is
// derived directly from the real wiring via [ports.PipelineSpec], called in
// main.go/demo.go with (ioports.Raw, Params, Saved, ioports.AlertStage) —
// see demo.go's spec-printing section. Params/Saved are declared in this
// package; Raw/AlertStage live in ioports (see the package doc comment on
// the import-direction rule), so the PipelineSpec call itself happens in a
// package that imports both, not here.
