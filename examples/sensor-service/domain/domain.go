// Package domain is Layer 1 + Layer 2 of the sensor service: models, codecs,
// and pure business rules. It imports nothing but codex, validate, and the
// sqlc-generated db package — no adapters, no ports, no IO.
//
// Everything IO-shaped (ports, adapters, wiring) builds ON these declarations;
// nothing here depends on how data arrives or leaves.
package domain

import (
	"fmt"
	"strings"
	"time"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/examples/sensor-service/db"
	"github.com/DaniDeer/go-codex/validate"
)

// ── Models ────────────────────────────────────────────────────────────────────

// CreateReadingReq is the HTTP POST /readings request body.
// The server assigns ID and RecordedAt.
type CreateReadingReq struct {
	SensorID string
	Value    float64
	Unit     string
}

// MQTTPayload is what an external sensor publishes over MQTT.
type MQTTPayload struct {
	SensorID string
	Value    float64
	Unit     string
}

// SensorAlert is published over MQTT when a reading exceeds the threshold.
type SensorAlert struct {
	SensorID  string
	Value     float64
	Threshold float64
	At        string // RFC3339
}

// SensorQuery asks for the stored time series of one sensor.
type SensorQuery struct {
	SensorID string
}

// TimeSeries is the ordered readings history of one sensor, queried from the
// database through the ioports.History IOPort.
type TimeSeries struct {
	SensorID string
	Readings []db.Reading
}

// ExportRequest triggers a readings export (POST /export). Empty for now —
// a real service would add filters (sensor, time range).
type ExportRequest struct{}

// ExportSnapshot is the exported dataset written to a typed file through the
// ioports.Exports sink port (FilePattern {exportID}.json).
type ExportSnapshot struct {
	ExportID   string
	ExportedAt string // RFC3339
	Readings   []db.Reading
}

// ExportResult is the POST /export response: where the snapshot was written
// and how many readings it contains.
type ExportResult struct {
	File  string
	Count int64
}

// AlertConfig parameterizes the alerting pipeline functions. Loaded from env
// vars in main() via format.FromEnv — AlertConfigCodec is the single source of
// truth for the env contract (name, type, constraint, default, docs):
//
//	APP_ALERT_THRESHOLD=80 go run ./examples/sensor-service
//
// When unset, DefaultField supplies 50.0 — the example runs with zero env setup.
type AlertConfig struct {
	Threshold float64
}

// AlertConfigCodec is the env contract for [AlertConfig].
var AlertConfigCodec = codex.Struct[AlertConfig](
	codex.DefaultField("threshold",
		codex.Float64().Refine(validate.MinFloat(0)).WithDescription("Alert when a reading's value exceeds this threshold."),
		50.0,
		func(c AlertConfig) float64 { return c.Threshold },
		func(c *AlertConfig, v float64) { c.Threshold = v }),
)

// ── Field factory functions ───────────────────────────────────────────────────
//
// Each factory captures one field's Refine rules and returns a [codex.Field]
// bound to the concrete struct type T. Reusing factories across db.Reading and
// db.InsertReadingParams ensures both types share identical validation rules
// with no duplication.

func sensorIDField[T any](
	get func(T) string,
	set func(*T, string),
) codex.Field[T, string] {
	return codex.RequiredField("sensor_id",
		codex.String().Refine(validate.UUID),
		get, set)
}

func valueField[T any](
	get func(T) float64,
	set func(*T, float64),
) codex.Field[T, float64] {
	return codex.RequiredField("value",
		codex.Float64().Refine(validate.RangeFloat(-9999, 9999)),
		get, set)
}

func unitField[T any](
	get func(T) string,
	set func(*T, string),
) codex.Field[T, string] {
	return codex.RequiredField("unit",
		codex.String().Refine(validate.OneOf("C", "F", "pct", "Pa", "ms")),
		get, set)
}

func recordedAtField[T any](
	get func(T) string,
	set func(*T, string),
) codex.Field[T, string] {
	return codex.RequiredField("recorded_at",
		codex.String().Refine(validate.DateTime),
		get, set)
}

// ── Codecs ────────────────────────────────────────────────────────────────────

// CreateReadingCodec — HTTP POST /readings request body.
var CreateReadingCodec = codex.Struct(
	sensorIDField(
		func(r CreateReadingReq) string { return r.SensorID },
		func(r *CreateReadingReq, v string) { r.SensorID = v }),
	valueField(
		func(r CreateReadingReq) float64 { return r.Value },
		func(r *CreateReadingReq, v float64) { r.Value = v }),
	unitField(
		func(r CreateReadingReq) string { return r.Unit },
		func(r *CreateReadingReq, v string) { r.Unit = v }),
)

// ReadingCodec — HTTP GET response + SQL post-read validation.
// Uses the same field factories as InsertParamsCodec for identical Refine rules.
var ReadingCodec = codex.Struct(
	codex.RequiredField("id",
		codex.String().Refine(validate.UUID),
		func(r db.Reading) string { return r.ID },
		func(r *db.Reading, v string) { r.ID = v }),
	sensorIDField(
		func(r db.Reading) string { return r.SensorID },
		func(r *db.Reading, v string) { r.SensorID = v }),
	valueField(
		func(r db.Reading) float64 { return r.Value },
		func(r *db.Reading, v float64) { r.Value = v }),
	unitField(
		func(r db.Reading) string { return r.Unit },
		func(r *db.Reading, v string) { r.Unit = v }),
	recordedAtField(
		func(r db.Reading) string { return r.RecordedAt },
		func(r *db.Reading, v string) { r.RecordedAt = v }),
)

// InsertParamsCodec — SQL pre-insert validation.
// Shares sensorIDField/valueField/unitField with ReadingCodec — same Refine
// rules, different struct type T, zero duplication.
var InsertParamsCodec = codex.Struct(
	codex.RequiredField("id",
		codex.String().Refine(validate.UUID),
		func(p db.InsertReadingParams) string { return p.ID },
		func(p *db.InsertReadingParams, v string) { p.ID = v }),
	sensorIDField(
		func(p db.InsertReadingParams) string { return p.SensorID },
		func(p *db.InsertReadingParams, v string) { p.SensorID = v }),
	valueField(
		func(p db.InsertReadingParams) float64 { return p.Value },
		func(p *db.InsertReadingParams, v float64) { p.Value = v }),
	unitField(
		func(p db.InsertReadingParams) string { return p.Unit },
		func(p *db.InsertReadingParams, v string) { p.Unit = v }),
	recordedAtField(
		func(p db.InsertReadingParams) string { return p.RecordedAt },
		func(p *db.InsertReadingParams, v string) { p.RecordedAt = v }),
)

// MQTTPayloadCodec — what external sensors publish over MQTT.
var MQTTPayloadCodec = codex.Struct(
	sensorIDField(
		func(p MQTTPayload) string { return p.SensorID },
		func(p *MQTTPayload, v string) { p.SensorID = v }),
	valueField(
		func(p MQTTPayload) float64 { return p.Value },
		func(p *MQTTPayload, v float64) { p.Value = v }),
	unitField(
		func(p MQTTPayload) string { return p.Unit },
		func(p *MQTTPayload, v string) { p.Unit = v }),
)

// AlertCodec — alert events published when a reading exceeds the threshold.
var AlertCodec = codex.Struct(
	sensorIDField(
		func(a SensorAlert) string { return a.SensorID },
		func(a *SensorAlert, v string) { a.SensorID = v }),
	valueField(
		func(a SensorAlert) float64 { return a.Value },
		func(a *SensorAlert, v float64) { a.Value = v }),
	codex.RequiredField("threshold",
		codex.Float64(),
		func(a SensorAlert) float64 { return a.Threshold },
		func(a *SensorAlert, v float64) { a.Threshold = v }),
	codex.RequiredField("at",
		codex.String().Refine(validate.DateTime),
		func(a SensorAlert) string { return a.At },
		func(a *SensorAlert, v string) { a.At = v }),
)

// TimeSeriesCodec — GET /sensors/{sensorID}/readings response body.
// Readings reuses ReadingCodec via codex.SliceOf — the same field rules
// validate single rows and whole series.
var TimeSeriesCodec = codex.Struct(
	sensorIDField(
		func(t TimeSeries) string { return t.SensorID },
		func(t *TimeSeries, v string) { t.SensorID = v }),
	codex.RequiredField("readings",
		codex.SliceOf(ReadingCodec),
		func(t TimeSeries) []db.Reading { return t.Readings },
		func(t *TimeSeries, v []db.Reading) { t.Readings = v }),
)

// ExportRequestCodec — POST /export request body (empty object).
var ExportRequestCodec = codex.Struct[ExportRequest]()

// ExportSnapshotCodec — content of an export file ({exportID}.json).
var ExportSnapshotCodec = codex.Struct(
	codex.RequiredField("export_id",
		codex.String().Refine(validate.UUID),
		func(e ExportSnapshot) string { return e.ExportID },
		func(e *ExportSnapshot, v string) { e.ExportID = v }),
	codex.RequiredField("exported_at",
		codex.String().Refine(validate.DateTime),
		func(e ExportSnapshot) string { return e.ExportedAt },
		func(e *ExportSnapshot, v string) { e.ExportedAt = v }),
	codex.RequiredField("readings",
		codex.SliceOf(ReadingCodec),
		func(e ExportSnapshot) []db.Reading { return e.Readings },
		func(e *ExportSnapshot, v []db.Reading) { e.Readings = v }),
)

// ExportResultCodec — POST /export response body.
var ExportResultCodec = codex.Struct(
	codex.RequiredField("file",
		codex.String().Refine(validate.NonEmptyString),
		func(r ExportResult) string { return r.File },
		func(r *ExportResult, v string) { r.File = v }),
	codex.RequiredField("count",
		codex.Int64(),
		func(r ExportResult) int64 { return r.Count },
		func(r *ExportResult, v int64) { r.Count = v }),
)

// SensorIDCodec validates a sensor ID path/topic/file-path variable.
var SensorIDCodec = codex.String().Refine(validate.UUID)

// ── Constraints ───────────────────────────────────────────────────────────────

// SensorTopicConstraint is a custom Constraint[string] enforcing the two known
// topic shapes registered against the shared events.Builder (see ioports):
// "sensors/<id>/data" (3 segments) and "alerts/<id>" (2 segments). Mirrors
// examples/adapters-mqtt's sensorTopicConstraint, composed the same way via
// events.WithTopicConstraints — but here it is applied to Patterns declared
// directly on ports.SourcePort/SinkPort rather than to a hand-built
// events.Channel.
var SensorTopicConstraint = codex.Constraint[string]{
	Name: "sensor-service-topic-format",
	Check: func(v string) bool {
		parts := strings.Split(v, "/")
		switch len(parts) {
		case 3:
			return parts[0] == "sensors" && parts[2] == "data"
		case 2:
			return parts[0] == "alerts"
		default:
			return false
		}
	},
	Message: func(v string) string {
		return fmt.Sprintf("topic must follow sensors/<id>/data or alerts/<id> format, got %q", v)
	},
}

// ── Pure business rules ───────────────────────────────────────────────────────

// NewReadingID returns a deterministic UUID-shaped ID from the current
// timestamp — avoids a uuid dependency.
func NewReadingID() string {
	t := time.Now().UnixNano()
	return fmt.Sprintf("%08x-0001-4000-8000-%012x", t>>32, t&0xffffffffffff)
}

// BuildInsertParams maps an HTTP create request to SQL insert params,
// assigning ID and RecordedAt.
func BuildInsertParams(req CreateReadingReq) db.InsertReadingParams {
	return db.InsertReadingParams{
		ID:         NewReadingID(),
		SensorID:   req.SensorID,
		Value:      req.Value,
		Unit:       req.Unit,
		RecordedAt: time.Now().UTC().Format(time.RFC3339),
	}
}

// BuildInsertParamsFromMQTT maps an MQTT sensor payload to SQL insert params,
// assigning ID and RecordedAt.
func BuildInsertParamsFromMQTT(p MQTTPayload) db.InsertReadingParams {
	return db.InsertReadingParams{
		ID:         NewReadingID(),
		SensorID:   p.SensorID,
		Value:      p.Value,
		Unit:       p.Unit,
		RecordedAt: time.Now().UTC().Format(time.RFC3339),
	}
}

// NewExportID returns a UUID-shaped export identifier — it becomes the
// {exportID} file-path variable of the ioports.Exports FilePattern.
func NewExportID() string {
	t := time.Now().UnixNano()
	return fmt.Sprintf("%08x-0002-4000-8000-%012x", t>>32, t&0xffffffffffff)
}

// NewExportSnapshot wraps the queried rows into an export snapshot with a
// fresh export ID and timestamp — a pure domain rule (the query and the file
// write happen through ports).
func NewExportSnapshot(rows []db.Reading) ExportSnapshot {
	return ExportSnapshot{
		ExportID:   NewExportID(),
		ExportedAt: time.Now().UTC().Format(time.RFC3339),
		Readings:   rows,
	}
}

// NewShouldAlert and NewBuildAlert are validated-config factories: they take
// TYPED, already-validated config and close over it — the returned pipeline
// functions stay pure and testable (tests pass any AlertConfig directly, no
// env manipulation needed). Zero env access in this layer; loading and
// validation happen once in main() via format.FromEnv.

// NewShouldAlert returns the alert filter predicate for cfg.
func NewShouldAlert(cfg AlertConfig) func(db.Reading) bool {
	return func(r db.Reading) bool { return r.Value > cfg.Threshold }
}

// NewBuildAlert returns the reading→alert mapper for cfg.
func NewBuildAlert(cfg AlertConfig) func(db.Reading) SensorAlert {
	return func(r db.Reading) SensorAlert {
		return SensorAlert{
			SensorID:  r.SensorID,
			Value:     r.Value,
			Threshold: cfg.Threshold,
			At:        time.Now().UTC().Format(time.RFC3339),
		}
	}
}
