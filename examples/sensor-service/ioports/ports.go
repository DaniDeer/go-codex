// Package ioports declares every IO boundary of the sensor service as a
// protocol-agnostic port or route — the service's complete IO surface,
// readable as a compact spec, with ZERO adapter imports.
//
// Each port declares its communication pattern once (ports.EventPattern /
// SQLPattern / FilePattern); main() derives the concrete handles
// (ports.EventHandle / FileHandle) and binds transport adapters to them.
// Swapping MQTT → ZeroMQ, or file → SQL enrichment, changes only main().
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
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/ports"
	"github.com/DaniDeer/go-codex/validate"
)

// ── Shared AsyncAPI builder ───────────────────────────────────────────────────

// EventsBuilder is shared by every EventPattern-based port below via
// PortOptions.EventBuilder. Configuring WithTopicConstraints here — exactly
// the same call examples/adapters-mqtt makes on its own events.NewBuilder —
// means every port's Register call (run internally by ports, not by hand)
// enforces domain.SensorTopicConstraint too: an invalid topic on any
// EventPattern declared with EventBuilder: EventsBuilder fails port
// construction immediately (PatternRegisterError wrapping
// events.InvalidTopicError), exactly as it would if declared by hand via
// events.NewChannel(...).Register(EventsBuilder).
//
// After wiring, main() prints the AsyncAPI spec straight from this builder —
// the port declarations below ARE the channel declarations.
var EventsBuilder = events.NewBuilder(events.Info{Title: "sensor-service", Version: "1.0.0"},
	events.WithTopicConstraints(validate.MQTTPublishTopic, domain.SensorTopicConstraint),
)

// ── Event ports (MQTT in the demo — any pub/sub adapter fits) ────────────────

// Sensors is the inbound boundary: sensor readings published by the sensor
// network. The topic + params are declared once here; main() derives the
// *events.ChannelHandle via ports.EventHandle and binds mqtt.SubscribeAdapter.
var Sensors = codex.Must(ports.NewSourcePort[domain.MQTTPayload](
	"mqtt/sensors/+/data", domain.MQTTPayloadCodec,
	ports.PortOptions{
		Buffer: 64,
		Patterns: []ports.Pattern{
			ports.EventPattern{
				Topic: "sensors/{sensorID}/data",
				Opts: []events.ChannelOpt{
					events.ChannelMeta{Description: "Sensor readings published by the sensor network."},
					events.Subscribe{Summary: "Receive sensor reading"},
					events.TopicParam{Name: "sensorID", Description: "UUID of the publishing sensor"},
				},
			},
		},
		EventBuilder: EventsBuilder,
	}))

// Alerts is the outbound boundary: threshold-breach alerts. Fan-out to
// additional sinks (e.g. SSE, file) requires only additional Bind calls in
// main() — no pipeline changes.
var Alerts = codex.Must(ports.NewSinkPort[domain.SensorAlert](
	"mqtt/alerts", domain.AlertCodec,
	ports.PortOptions{
		Patterns: []ports.Pattern{
			ports.EventPattern{
				Topic: "alerts/{sensorID}",
				Opts: []events.ChannelOpt{
					events.ChannelMeta{Description: "Threshold-breach alerts."},
					events.Publish{Summary: "Publish threshold-breach alert"},
					events.TopicParam{Name: "sensorID", Description: "UUID of the sensor that triggered the alert"},
				},
			},
		},
		EventBuilder: EventsBuilder,
	}))

// ── Shared OpenAPI builder ────────────────────────────────────────────────────

// RESTBuilder is shared by the RESTPattern-based tool ports below via
// PortOptions.RESTBuilder — the same declare-once story as EventsBuilder:
// after wiring, main() prints the OpenAPI spec straight from this builder;
// the port declarations ARE the route declarations.
var RESTBuilder = rest.NewBuilder(rest.Info{Title: "sensor-service", Version: "1.0.0"})

// ── SQL ports ─────────────────────────────────────────────────────────────────

// Readings is the persistence boundary INSIDE the pipeline: each mapped
// insert-params item is saved and re-emitted as the stored row (1→1
// intermediate IO). The pipeline stays pure — it maps payloads to insert
// params with a forge function and hands persistence to this port; main()
// binds sql.QueryEachAdapter (whose codec validation is the post-read check).
//
// The SQL metadata is declared ONCE here via ports.SQLPattern — the adapter
// defaults its options' Table/Op from it, and it feeds SQLValidationError
// context + observer location strings.
var Readings = codex.Must(ports.NewIOPort[db.InsertReadingParams, db.Reading](
	"sql/readings/save", domain.InsertParamsCodec, domain.ReadingCodec,
	ports.PortOptions{
		Patterns: []ports.Pattern{
			ports.SQLPattern{Table: "readings", Op: "insert_reading"},
		},
	}))

// History is the time-series query boundary: one SensorQuery in, that
// sensor's full stored TimeSeries out (1→1 intermediate IO). It backs the
// GET /sensors/{sensorID}/readings tool pipeline — the REST layer never
// touches SQL; it Connects through this port, and main() picks the adapter
// (sql.QueryEachAdapter over the sqlc ListReadingsBySensor query).
var History = codex.Must(ports.NewIOPort[domain.SensorQuery, domain.TimeSeries](
	"sql/readings/history", codex.Struct[domain.SensorQuery](), domain.TimeSeriesCodec,
	ports.PortOptions{
		Patterns: []ports.Pattern{
			ports.SQLPattern{Table: "readings", Op: "list_by_sensor"},
		},
	}))

// ExportQuery is the export dataset boundary: one ExportRequest in, one
// ExportSnapshot (all stored readings + fresh export ID) out. It backs the
// POST /export tool pipeline the same way History backs the time series.
var ExportQuery = codex.Must(ports.NewIOPort[domain.ExportRequest, domain.ExportSnapshot](
	"sql/readings/export", domain.ExportRequestCodec, domain.ExportSnapshotCodec,
	ports.PortOptions{
		Patterns: []ports.Pattern{
			ports.SQLPattern{Table: "readings", Op: "list_readings"},
		},
	}))

// ── File port ─────────────────────────────────────────────────────────────────

// NewExportsPort declares the export file boundary: each ExportSnapshot fed
// into this sink is written as one typed JSON file. The file (path template +
// JSON format derived from the port's codec + path-param codec) is declared
// ONCE here via ports.FilePattern; main() derives the format.File via
// ports.FileHandle and binds file.DrainWriteFileAdapter. The export tool
// pipeline computes the response path from the SAME declaration
// (FileHandle.BuildPath) — one source of truth for where exports live.
//
// A constructor because the export directory is created at runtime.
func NewExportsPort(dir string) (*ports.SinkPort[domain.ExportSnapshot], error) {
	return ports.NewSinkPort[domain.ExportSnapshot](
		"file/exports", domain.ExportSnapshotCodec,
		ports.PortOptions{
			Buffer: 4,
			Patterns: []ports.Pattern{
				ports.FilePattern{ // JSON is the default FileFormatKind
					Path: dir + "/{exportID}.json",
					Opts: []format.FileOpt{
						format.FilePathParam{Name: "exportID"}.WithCodec(domain.SensorIDCodec),
					},
				},
			},
		})
}

// ── Tool ports (REST request/response, pipeline-backed) ──────────────────────

// HistoryTool exposes the time-series pipeline as GET /sensors/{sensorID}/readings.
// The route (method, path, params) is declared ONCE here via ports.RESTPattern,
// registered internally against RESTBuilder; main() derives the *RouteHandle
// via ports.RESTHandle and binds nethttp.PipelineAdapter. The pipeline itself
// (pipeline.NewTimeSeriesPipeline) Connects through the History SQL port.
var HistoryTool = codex.Must(ports.NewToolPort[struct{}, domain.TimeSeries](
	"rest/sensor-history", codex.Struct[struct{}](), domain.TimeSeriesCodec,
	ports.PortOptions{
		Patterns: []ports.Pattern{
			ports.RESTPattern{
				Method: "GET",
				Path:   "/sensors/{sensorID}/readings",
				Opts: []rest.RouteOpt{
					rest.RouteMeta{OperationID: "getSensorTimeSeries", Description: "Stored time series of one sensor, queried from the database."},
					rest.PathParam{Name: "sensorID", Description: "UUID of the sensor"}.WithCodec(domain.SensorIDCodec),
				},
			},
		},
		RESTBuilder: RESTBuilder,
	}))

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
var ExportTool = codex.Must(ports.NewToolPort[domain.ExportRequest, domain.ExportResult](
	"rest/export", domain.ExportRequestCodec, domain.ExportResultCodec,
	ports.PortOptions{
		Patterns: []ports.Pattern{
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
		},
		RESTBuilder: RESTBuilder,
	}))

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
var CreateHandle = codex.Must(CreateRoute.Register(RESTBuilder))

// GetRoute — GET /readings/{id}.
var GetRoute = rest.NewRoute("GET", "/readings/{id}",
	codex.Struct[struct{}](), domain.ReadingCodec,
	rest.RouteMeta{OperationID: "getReading", Description: "Fetch one stored reading by ID."},
	rest.PathParam{Name: "id", Description: "Reading UUID"},
)

// GetHandle is GetRoute registered against RESTBuilder.
var GetHandle = codex.Must(GetRoute.Register(RESTBuilder))

// LatestRoute — GET /readings/latest, served by nethttp.HandlerLatest
// (reactive cache). Returns the most recently saved sensor reading without
// querying the DB.
var LatestRoute = rest.NewRoute("GET", "/readings/latest",
	codex.Struct[struct{}](), domain.ReadingCodec,
	rest.RouteMeta{OperationID: "getLatestReading", Description: "Most recent reading, served from the stream's reactive cache — no DB query."},
)

// LatestHandle is LatestRoute registered against RESTBuilder.
var LatestHandle = codex.Must(LatestRoute.Register(RESTBuilder))
