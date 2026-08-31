// Package ioports declares every IO boundary of the sensor service as a
// protocol-agnostic port or route — the service's complete IO surface,
// readable as a compact spec, with ZERO adapter imports.
//
// Every boundary follows the SAME three-step model: (1) declare the port's
// structural shape via NewXxxPort — no communication pattern baked in; (2)
// plug in a communication pattern (ports.EventPattern/RESTPattern/
// SQLPattern/FilePattern) via the port's PluginXxxPattern method, which
// registers it and returns the typed handle in one call; (3) bind a
// concrete adapter to the returned handle in main.go. Patterns needing a
// shared, application-wide builder (EventsBuilder/RESTBuilder below) are
// plugged in against THAT builder automatically — its channel/route shows
// up in the printed AsyncAPI/OpenAPI spec alongside every other boundary.
//
// For ports whose Pattern is fully known upfront (no late-binding need),
// the Plugin step happens right here via a protocol-named convenience
// constructor (ports.NewSQLPort, ports.NewRestToolPort) — a thin wrapper
// combining steps 1+2 into one call. For Sensors/Alerts — the MQTT
// ingestion/egress boundary — the Pattern is declared as a standalone
// value (SensorsPattern/AlertsPattern) and plugged in separately, in
// main.go's wiring section, right where the adapter gets bound: the
// clearest illustration of the three-step model's "declare port here,
// declare Pattern here, plug in + bind at wiring time" shape.
//
// The package is named ioports (not ports) to avoid colliding with the
// library's ports package.
package ioports

import (
	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/examples/sensor-service/db"
	"github.com/DaniDeer/go-codex/examples/sensor-service/domain"
	"github.com/DaniDeer/go-codex/ports"
	"github.com/DaniDeer/go-codex/validate"
)

// ── Shared AsyncAPI builder ───────────────────────────────────────────────────

// EventsBuilder is shared by every EventPattern-based port below via
// PortOptions.EventBuilder. Configuring WithTopicConstraints here — exactly
// the same call examples/adapters-mqtt makes on its own events.NewBuilder —
// means every port's Plugin call enforces domain.SensorTopicConstraint too:
// an invalid topic on any EventPattern plugged into a port constructed with
// EventBuilder: EventsBuilder fails immediately (PatternRegisterError
// wrapping events.InvalidTopicError), exactly as it would if declared by
// hand via events.NewChannel(...).Register(EventsBuilder).
//
// After wiring, main() prints the AsyncAPI spec straight from this builder —
// the port declarations below ARE the channel declarations.
var EventsBuilder = events.NewBuilder(events.Info{Title: "sensor-service", Version: "1.0.0"},
	events.WithTopicConstraints(validate.MQTTPublishTopic, domain.SensorTopicConstraint),
)

// ── Shared OpenAPI builder ────────────────────────────────────────────────────

// RESTBuilder is shared by the RESTPattern-based tool ports below via
// PortOptions.RESTBuilder — the same declare-once story as EventsBuilder:
// after wiring, main() prints the OpenAPI spec straight from this builder;
// the port declarations ARE the route declarations.
var RESTBuilder = rest.NewBuilder(rest.Info{Title: "sensor-service", Version: "1.0.0"})

// ── Event ports (MQTT in the demo — any pub/sub adapter fits) ────────────────
//
// Sensors and Alerts are the pipeline's first/last stages AND its MQTT
// ingestion/egress boundary — plain SourcePort/SinkPort, not PipePort:
// pipeline.Build wires Sensors --Chain--> Params --ChainStream--> Saved
// --ChainStream--> Alerts using the generalized Chain/ChainStream (accepts
// boundary ports directly, not just PipePort-to-PipePort).

// SensorsPattern is Sensors' EventPattern, declared standalone — a
// reusable, self-contained value, independent of the port declaration
// below. Plugged in at wiring time in main.go, right where the MQTT
// adapter gets bound — the clearest illustration of the three-step model.
var SensorsPattern = ports.EventPattern{
	Topic: "sensors/{sensorID}/data",
	Opts: []events.ChannelOpt{
		events.ChannelMeta{Description: "Sensor readings published by the sensor network."},
		events.Subscribe{Summary: "Receive sensor reading"},
		events.TopicParam{Name: "sensorID", Description: "UUID of the publishing sensor"},
	},
}

// Sensors is the inbound boundary: sensor readings published by the sensor
// network. Structural shape declared here; SensorsPattern plugged in at
// wiring time in main.go.
var Sensors = codex.Must(ports.NewSourcePort[domain.MQTTPayload](
	"mqtt/sensors/+/data", domain.MQTTPayloadCodec,
	ports.PortOptions{Buffer: 64, EventBuilder: EventsBuilder}))

// AlertsPattern is Alerts' EventPattern, declared standalone — same
// declare-once story as SensorsPattern.
var AlertsPattern = ports.EventPattern{
	Topic: "alerts/{sensorID}",
	Opts: []events.ChannelOpt{
		events.ChannelMeta{Description: "Threshold-breach alerts."},
		events.Publish{Summary: "Publish threshold-breach alert"},
		events.TopicParam{Name: "sensorID", Description: "UUID of the sensor that triggered the alert"},
	},
}

// Alerts is the outbound boundary: threshold-breach alerts. Fan-out to
// additional sinks (e.g. SSE, file) requires only additional Bind calls in
// main() — no pipeline changes.
var Alerts = codex.Must(ports.NewSinkPort[domain.SensorAlert](
	"mqtt/alerts", domain.AlertCodec,
	ports.PortOptions{EventBuilder: EventsBuilder}))

// ── SQL ports ─────────────────────────────────────────────────────────────────
//
// Each SQL boundary's Pattern is fully known upfront (no late-binding
// need), so it's plugged in right here via ports.NewSQLPort — a thin
// convenience constructor combining "declare port" + "plug in SQLPattern"
// into one call.

// Readings is the persistence boundary INSIDE the pipeline: each mapped
// insert-params item is saved and re-emitted as the stored row (1→1
// intermediate IO). The pipeline stays pure — it maps payloads to insert
// params with a plain function and hands persistence to this port; main()
// binds sql.QueryEachAdapter (whose codec validation is the post-read check).
//
// The SQL metadata is declared ONCE here via ports.SQLPattern — the adapter
// defaults its options' Table/Op from it, and it feeds SQLValidationError
// context + observer location strings.
var Readings = codex.Must(ports.NewSQLPort[db.InsertReadingParams, db.Reading](
	"sql/readings/save", domain.InsertParamsCodec, domain.ReadingCodec,
	ports.SQLPattern{Table: "readings", Op: "insert_reading"}, ports.PortOptions{}))

// History is the time-series query boundary: one SensorQuery in, that
// sensor's full stored TimeSeries out (1→1 intermediate IO). It backs the
// GET /sensors/{sensorID}/readings tool pipeline — the REST layer never
// touches SQL; it Connects through this port, and main() picks the adapter
// (sql.QueryEachAdapter over the sqlc ListReadingsBySensor query).
var History = codex.Must(ports.NewSQLPort[domain.SensorQuery, domain.TimeSeries](
	"sql/readings/history", codex.Struct[domain.SensorQuery](), domain.TimeSeriesCodec,
	ports.SQLPattern{Table: "readings", Op: "list_by_sensor"}, ports.PortOptions{}))

// ExportQuery is the export dataset boundary: one ExportRequest in, one
// ExportSnapshot (all stored readings + fresh export ID) out. It backs the
// POST /export tool pipeline the same way History backs the time series.
var ExportQuery = codex.Must(ports.NewSQLPort[domain.ExportRequest, domain.ExportSnapshot](
	"sql/readings/export", domain.ExportRequestCodec, domain.ExportSnapshotCodec,
	ports.SQLPattern{Table: "readings", Op: "list_readings"}, ports.PortOptions{}))

// ── File port ─────────────────────────────────────────────────────────────────

// NewExportsPort declares the export file boundary: each ExportSnapshot fed
// into this sink is written as one typed JSON file. The file (path template +
// JSON format derived from the port's codec + path-param codec) is plugged
// in via ports.FilePattern here — main() binds file.DrainWriteFileAdapter
// to the returned handle. The export tool pipeline computes the response
// path from the SAME handle (File.BuildPath) — one source of truth for
// where exports live.
//
// A constructor (not a package var) because the export directory is
// created at runtime; returns (port, file handle, error) — main() unwraps
// both non-error values in one call via codex.Must2.
func NewExportsPort(dir string) (*ports.SinkPort[domain.ExportSnapshot], ports.File[domain.ExportSnapshot], error) {
	p, err := ports.NewSinkPort[domain.ExportSnapshot](
		"file/exports", domain.ExportSnapshotCodec, ports.PortOptions{Buffer: 4})
	if err != nil {
		return nil, ports.File[domain.ExportSnapshot]{}, err
	}
	f, err := p.PluginFilePattern(ports.FilePattern{ // JSON is the default FileFormatKind
		Path: dir + "/{exportID}.json",
		Opts: []ports.FileOpt{
			ports.FilePathParam{Name: "exportID"}.WithCodec(domain.SensorIDCodec),
		},
	})
	if err != nil {
		return nil, ports.File[domain.ExportSnapshot]{}, err
	}
	return p, f, nil
}

// ── Tool ports (REST request/response, pipeline-backed) ──────────────────────
//
// Each tool port's Pattern is fully known upfront, so it's plugged in right
// here via ports.NewRestToolPort — declaring the port AND the route (and
// registering it against RESTBuilder) in one call; main() takes the
// returned handle directly instead of deriving it separately.

// HistoryTool exposes the time-series pipeline as GET /sensors/{sensorID}/readings.
// The route (method, path, params) is declared and registered against
// RESTBuilder in one call; main() binds nethttp.PipelineAdapter to the
// returned handle. The pipeline itself (pipeline.NewTimeSeriesPipeline)
// Connects through the History SQL port.
var HistoryTool, HistoryToolHandle = codex.Must2(ports.NewRestToolPort[struct{}, domain.TimeSeries](
	"rest/sensor-history", codex.Struct[struct{}](), domain.TimeSeriesCodec,
	ports.RESTPattern{
		Method: "GET",
		Path:   "/sensors/{sensorID}/readings",
		Opts: []rest.RouteOpt{
			rest.RouteMeta{OperationID: "getSensorTimeSeries", Description: "Stored time series of one sensor, queried from the database."},
			rest.PathParam{Name: "sensorID", Description: "UUID of the sensor"}.WithCodec(domain.SensorIDCodec),
		},
	},
	ports.PortOptions{RESTBuilder: RESTBuilder}))

// ExportTool exposes the export pipeline as POST /export: query all readings
// through ExportQuery, write the snapshot file through the Exports sink, and
// respond with the deterministic file path + reading count.
//
// The X-Api-Key header is a codec-validated auth header declared HERE, on
// the port's RESTPattern — the exact same rest.HeaderParam vocabulary a
// hand-declared route uses. The nethttp adapter validates it BEFORE the
// pipeline runs (missing/invalid → 400 + rest.HeaderParamError; the request
// never reaches the pipeline), and the codec's schema appears in the OpenAPI
// spec's parameter entry. No wiring change in main.go.
var ExportTool, ExportToolHandle = codex.Must2(ports.NewRestToolPort[domain.ExportRequest, domain.ExportResult](
	"rest/export", domain.ExportRequestCodec, domain.ExportResultCodec,
	ports.RESTPattern{
		Method: "POST",
		Path:   "/export",
		Opts: []rest.RouteOpt{
			rest.RouteMeta{OperationID: "exportReadings", Description: "Export all stored readings to a typed JSON file."},
			rest.HeaderParam{
				Name:        "X-Api-Key",
				Description: `API key authorizing the export ("sk-" prefix).`,
				Required:    true,
			}.WithCodec(domain.APIKeyCodec),
		},
	},
	ports.PortOptions{RESTBuilder: RESTBuilder}))

// ── REST routes ───────────────────────────────────────────────────────────────
//
// Route declarations are pure value expressions. Each is registered against
// the shared RESTBuilder right here — the same builder the RESTPattern-based
// tool ports use — so the OpenAPI spec printed in the demo covers ALL five
// HTTP endpoints from one source of truth. main() takes the *Handle values
// and wires handlers; no ClientHandle/Register call appears there.

// CreateRoute — POST /readings.
var CreateRoute = rest.NewRoute("POST", "/readings",
	domain.CreateReadingCodec, domain.ReadingCodec,
	rest.RouteMeta{OperationID: "createReading", Description: "Create a sensor reading (server assigns ID and RecordedAt)."},
)

// CreateHandle is CreateRoute registered against RESTBuilder.
var CreateHandle = codex.Must(CreateRoute.RegisterHandle(RESTBuilder))

// GetRoute — GET /readings/{id}.
var GetRoute = rest.NewRoute("GET", "/readings/{id}",
	codex.Struct[struct{}](), domain.ReadingCodec,
	rest.RouteMeta{OperationID: "getReading", Description: "Fetch one stored reading by ID."},
	rest.PathParam{Name: "id", Description: "Reading UUID"},
)

// GetHandle is GetRoute registered against RESTBuilder.
var GetHandle = codex.Must(GetRoute.RegisterHandle(RESTBuilder))

// ── Cache port ────────────────────────────────────────────────────────────────

// Latest is the reactive-cache boundary: GET /readings/latest serves the most
// recently saved reading straight from the stream — no DB query per request.
// The cache outlives the stream: after the MQTT pipeline shuts down, the
// port keeps serving the last value.
var Latest = codex.Must(ports.NewLatestPort[db.Reading](
	"rest/latest", domain.ReadingCodec,
	ports.PortOptions{RESTBuilder: RESTBuilder}))

// LatestHandle is Latest's RESTPattern, plugged in right here — fully known
// upfront, no late-binding need.
var LatestHandle = codex.Must(Latest.PluginRESTPattern(ports.RESTPattern{
	Method: "GET",
	Path:   "/readings/latest",
	Opts: []rest.RouteOpt{
		rest.RouteMeta{OperationID: "getLatestReading", Description: "Most recent reading, served from the stream's reactive cache — no DB query."},
	},
}))
