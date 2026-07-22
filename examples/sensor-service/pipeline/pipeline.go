// Package pipeline is the sensor service's business logic layer: pure
// mapping functions and the stream topology. It composes domain rules over
// typed streams and depends only on domain + the go-codex core — no
// adapters, no concrete clients, no IO of its own.
//
// The MQTT pipeline is segmented into named stages wired by
// [ports.Chain]/[ports.ChainStream] (Sensors → Params → Saved → Alerts).
// Sensors and Alerts are declared in ioports as plain [ports.SourcePort]/
// [ports.SinkPort] (they carry the MQTT EventPattern — the actual IO/
// adapter boundary, plugged in at wiring time); Params and Saved are
// declared HERE, as pipeline-internal [ports.PipePort] stages with no
// adapters of their own. pipeline never imports ioports (see main.go's
// import-direction doc comment: pipeline → domain only) — Build takes
// sensors/alerts as parameters instead, exactly as it previously took a
// plain gstream.Stream[MQTTPayload].
//
// Persistence is not hidden inside a pure mapping function, and — per this
// package's stage segmentation — not hidden inside a larger transform's
// closure either: mapping functions stay pure (payload → insert params),
// and the save happens through a protocol-agnostic [ports.IOPort]
// (ioports.Readings) Connected inside its OWN Params→Saved ChainStream edge,
// visible to [ports.PipelineSpec] as a real edge between two named pipes —
// never buried alongside unrelated taps/filters in one bigger stage.
//
// Pipeline-internal glue (buildInsertParams, buildExportResult below) is
// plain Go functions, not [forge.Function] — forge's contract-hash/signing
// governance is valuable for published KPI calculations, not ungoverned
// per-item mapping; [ports.Chain]/[gstream.Apply] derive real function
// identity via reflection regardless (see [ports.PipelineSpec]).
package pipeline

import (
	"context"
	"log/slog"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/examples/sensor-service/db"
	"github.com/DaniDeer/go-codex/examples/sensor-service/domain"
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

// buildInsertParams maps an MQTT payload to SQL insert params (domain rule
// — assigns ID + RecordedAt). No IO — persistence happens through
// Deps.Persist, not inside this function. A plain function — see the
// package doc comment on why this isn't a [forge.Function].
func buildInsertParams(payload domain.MQTTPayload) (db.InsertReadingParams, error) {
	return domain.BuildInsertParamsFromMQTT(payload), nil
}

// Result exposes the pipeline's HTTP-facing stream and a lifecycle signal.
type Result struct {
	// LatestReadings feeds the HTTP reactive cache (GET /readings/latest).
	LatestReadings gstream.Stream[db.Reading]
	// Done closes once every item Saved ever produced has been drained —
	// a real, if slightly indirect, completion signal for the whole
	// pipeline (Sensors/Alerts are plain boundary ports with no Done() of
	// their own, unlike a PipePort; this is a THIRD independent Stream()
	// consumer of Saved, purely for lifecycle tracking, draining alongside
	// the Saved→Alerts edge and the reactive-cache Stream() above).
	Done <-chan struct{}
}

// Build wires the segmented MQTT pipeline as three [ports.Chain]/
// [ports.ChainStream] edges — boundary ports at each end, PipePort stages
// in between, all through the SAME call shape:
//
//	sensors (ioports.Sensors) --Chain(buildInsertParams, pure)--> Params
//	Params --ChainStream(Persist.Connect + Tap(log))--> Saved
//	Saved --ChainStream(Filter+FlatMap)--> alerts (ioports.Alerts)
//
// sensors and alerts are parameters, not package vars, so this package
// never imports ioports (see the package doc comment on the
// import-direction rule) — main() passes ioports.Sensors/ioports.Alerts
// in, exactly as it previously passed a plain gstream.Stream[MQTTPayload].
//
// The persistence step (Params → Saved) is deliberately its OWN
// ChainStream edge — not folded into the Sensors→Params or Saved→Alerts
// transforms — so [ports.PipelineSpec] sees the SQL hop as a real edge
// between two named pipes, never hidden inside a bigger transform's
// closure (see the package doc comment).
//
// Caller must Connect sensors' upstream registration ordering: Chain/
// ChainStream registrations must precede the UPSTREAM value's Connect/Bind
// — see [ports.Chain]'s ordering rule. Params and Saved (both PipePorts)
// need an explicit Connect call after Build; sensors/alerts (boundary
// ports) only need their adapter Bind — no Connect method of their own.
func Build(
	ctx context.Context,
	sensors *ports.SourcePort[domain.MQTTPayload],
	alerts *ports.SinkPort[domain.SensorAlert],
	deps Deps,
) Result {
	shouldAlert := domain.NewShouldAlert(deps.Cfg)
	buildAlert := domain.NewBuildAlert(deps.Cfg)

	// Stage 1: Sensors → Params — pure transformation, zero IO.
	ports.Chain(ctx, sensors, buildInsertParams, Params)

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

	// Stage 3: Saved → Alerts — filter to threshold breaches, map to
	// SensorAlert for the MQTT alert topic.
	ports.ChainStream(ctx, Saved, func(s gstream.Stream[db.Reading]) gstream.Stream[domain.SensorAlert] {
		aboveThreshold := gstream.Filter(ctx, s, shouldAlert)
		return gstream.FlatMapSlice(ctx, aboveThreshold,
			func(r db.Reading) []domain.SensorAlert { return []domain.SensorAlert{buildAlert(r)} })
	}, alerts)

	// The HTTP reactive cache taps Saved directly — a fourth, independent
	// Stream() consumer registered on the SAME pipe (must precede Saved's
	// own Connect, same ordering rule as Chain/ChainStream).
	latestReadings := Saved.Stream(ctx)

	// A fifth, independent Stream() consumer purely for lifecycle
	// tracking — drains until Saved's upstream (and therefore the whole
	// pipeline) terminates, then closes done.
	done := make(chan struct{})
	doneSignal := Saved.Stream(ctx)
	go func() {
		gstream.Drain(ctx, doneSignal, func(context.Context, db.Reading) error { return nil }, nil, gstream.DrainOptions{})
		close(done)
	}()

	return Result{
		LatestReadings: latestReadings,
		Done:           done,
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

// NewExportPipeline builds the POST /export tool pipeline:
//
//	ExportRequest → ExportQuery port (SQL: all readings → snapshot)
//	             → submit (hand the snapshot to the Exports file sink)
//	             → Map(buildExportResult, pure)
//
// submit decouples this package from the file adapter: main() wires it to
// the channel feeding the Exports SinkPort. pathFor is pure computation
// (FilePattern template substitution + path-param validation via
// File.BuildPath — no IO): the SAME declaration that writes the file names
// the response path.
func NewExportPipeline(
	query *ports.IOPort[domain.ExportRequest, domain.ExportSnapshot],
	submit func(domain.ExportSnapshot),
	pathFor func(domain.ExportSnapshot) (string, error),
) func(ctx context.Context, req domain.ExportRequest) gstream.Stream[domain.ExportResult] {
	buildExportResult := func(s domain.ExportSnapshot) (domain.ExportResult, error) {
		path, err := pathFor(s)
		if err != nil {
			return domain.ExportResult{}, err
		}
		return domain.ExportResult{File: path, Count: int64(len(s.Readings))}, nil
	}
	return func(ctx context.Context, req domain.ExportRequest) gstream.Stream[domain.ExportResult] {
		snapshots := query.Connect(ctx, gstream.Single(ctx, req))
		// Hand each snapshot to the file sink (async IO boundary).
		snapshots = gstream.Tap(ctx, snapshots, submit)
		return gstream.Map(ctx, snapshots, buildExportResult, gstream.MapOptions{Name: "buildExportResult"})
	}
}

// Spec generation (Gap 4 of the now-shipped pipe-port-composition-hardening
// roadmap) no longer lives here as a hand-typed pipeline.Topology function
// — it is derived directly from the real wiring via [ports.PipelineSpec],
// called in demo.go with (ioports.Sensors, Params, Saved, ioports.Alerts) —
// see demo.go's spec-printing section. Params/Saved are declared in this
// package; Sensors/Alerts live in ioports (see the package doc comment on
// the import-direction rule), so the PipelineSpec call itself happens in a
// package that imports both, not here.
