// Package routes declares every channel used by this example — pure spec
// values, no adapter attached yet (that happens in mqtt5broker/zeromqbroker/
// mqttbroker/client, mirroring examples/reqreply-api/routes's identical
// separation of concerns). This package is THE shared contract: every
// broker package, client package, and demo file imports it, so the Go
// compiler enforces byte-identical channel/codec declarations across all
// three transports simultaneously — see main.go's own package doc for the
// project-wide framing.
package routes

import (
	"fmt"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/validate"
)

// ── Sensor domain (used by most demos: security, client-attach, escape
// hatch, wildcard subscription, error-path ergonomics, observability) ───────

// SensorReading is a single measurement published by a sensor node.
type SensorReading struct {
	SensorID string
	Value    float64
}

var SensorReadingCodec = codex.Struct[SensorReading](
	codex.RequiredField("sensor_id",
		codex.String().Refine(validate.UUID).WithTitle("SensorID"),
		func(r SensorReading) string { return r.SensorID },
		func(r *SensorReading, v string) { r.SensorID = v },
	),
	codex.RequiredField("value",
		codex.Float64().WithTitle("Value"),
		func(r SensorReading) float64 { return r.Value },
		func(r *SensorReading, v float64) { r.Value = v },
	),
)

// TenantSensorReading is [SensorReading] plus a TenantID field populated
// via [events.NewPropertyParam]'s direct (Middleware-free) channel
// attachment — see routes.PropertyMergeChannel and
// demo_property_merge_direct_attachment.go.
type TenantSensorReading struct {
	SensorID string
	Value    float64
	TenantID string
}

var TenantSensorReadingCodec = codex.Struct[TenantSensorReading](
	codex.RequiredField("sensor_id",
		codex.String().Refine(validate.UUID).WithTitle("SensorID"),
		func(r TenantSensorReading) string { return r.SensorID },
		func(r *TenantSensorReading, v string) { r.SensorID = v },
	),
	codex.RequiredField("value",
		codex.Float64().WithTitle("Value"),
		func(r TenantSensorReading) float64 { return r.Value },
		func(r *TenantSensorReading, v float64) { r.Value = v },
	),
	codex.OptionalField("tenant_id",
		codex.String().WithTitle("TenantID"),
		func(r TenantSensorReading) string { return r.TenantID },
		func(r *TenantSensorReading, v string) { r.TenantID = v },
	),
)

// ── Domain boundary pipeline (MeasurementEvent → TimeSeriesRecord →
// AlertEvent) — preserves the three-layer scenario docs/concepts/
// codec-as-domain-boundary.md illustrates itself with. Shared field codecs
// (sensorIDFieldCodec/measurementValueCodec/unitFieldCodec) declare domain
// constraints ONCE; they propagate to every struct codec that uses them —
// MQTT subscribe, database schema, MQTT publish. ─────────────────────────────

var sensorIDFieldCodec = codex.String().
	Refine(validate.NonEmptyString).
	WithDescription("Unique sensor identifier.")

var measurementValueCodec = codex.Float64().
	Refine(validate.NonZeroFloat).
	WithDescription("Measured value.")

var unitFieldCodec = codex.String().
	Refine(validate.NonEmptyString).
	WithDescription("Physical unit (e.g. celsius, bar, rpm).")

// MeasurementEvent is the domain entity received from the sensor network.
type MeasurementEvent struct {
	SensorID  string
	Value     float64
	Unit      string
	Timestamp string // RFC 3339
}

var MeasurementEventCodec = codex.Struct[MeasurementEvent](
	codex.RequiredField("sensor_id", sensorIDFieldCodec,
		func(m MeasurementEvent) string { return m.SensorID },
		func(m *MeasurementEvent, v string) { m.SensorID = v },
	),
	codex.RequiredField("value", measurementValueCodec,
		func(m MeasurementEvent) float64 { return m.Value },
		func(m *MeasurementEvent, v float64) { m.Value = v },
	),
	codex.RequiredField("unit", unitFieldCodec,
		func(m MeasurementEvent) string { return m.Unit },
		func(m *MeasurementEvent, v string) { m.Unit = v },
	),
	codex.RequiredField("timestamp",
		codex.String().Refine(validate.DateTime).WithDescription("Measurement time (RFC 3339)."),
		func(m MeasurementEvent) string { return m.Timestamp },
		func(m *MeasurementEvent, v string) { m.Timestamp = v },
	),
)

// TimeSeriesRecord is the domain entity written to the time series database.
type TimeSeriesRecord struct {
	SensorID   string
	Value      float64
	Unit       string
	Timestamp  string // original measurement time (RFC 3339)
	ReceivedAt string // ingestion time (RFC 3339)
}

var TimeSeriesRecordCodec = codex.Struct[TimeSeriesRecord](
	codex.RequiredField("sensor_id", sensorIDFieldCodec,
		func(r TimeSeriesRecord) string { return r.SensorID },
		func(r *TimeSeriesRecord, v string) { r.SensorID = v },
	),
	codex.RequiredField("value", measurementValueCodec,
		func(r TimeSeriesRecord) float64 { return r.Value },
		func(r *TimeSeriesRecord, v float64) { r.Value = v },
	),
	codex.RequiredField("unit", unitFieldCodec,
		func(r TimeSeriesRecord) string { return r.Unit },
		func(r *TimeSeriesRecord, v string) { r.Unit = v },
	),
	codex.RequiredField("timestamp",
		codex.String().Refine(validate.DateTime).WithDescription("Measurement time (RFC 3339)."),
		func(r TimeSeriesRecord) string { return r.Timestamp },
		func(r *TimeSeriesRecord, v string) { r.Timestamp = v },
	),
	codex.RequiredField("received_at",
		codex.String().Refine(validate.DateTime).WithDescription("Ingestion time (RFC 3339)."),
		func(r TimeSeriesRecord) string { return r.ReceivedAt },
		func(r *TimeSeriesRecord, v string) { r.ReceivedAt = v },
	),
)

// AlertEvent is the domain entity published when a measurement exceeds a threshold.
type AlertEvent struct {
	SensorID  string
	Value     float64
	Unit      string
	Threshold float64
	Timestamp string // RFC 3339
}

var AlertEventCodec = codex.Struct[AlertEvent](
	codex.RequiredField("sensor_id", sensorIDFieldCodec,
		func(a AlertEvent) string { return a.SensorID },
		func(a *AlertEvent, v string) { a.SensorID = v },
	),
	codex.RequiredField("value", measurementValueCodec,
		func(a AlertEvent) float64 { return a.Value },
		func(a *AlertEvent, v float64) { a.Value = v },
	),
	codex.RequiredField("unit", unitFieldCodec,
		func(a AlertEvent) string { return a.Unit },
		func(a *AlertEvent, v string) { a.Unit = v },
	),
	codex.RequiredField("threshold",
		codex.Float64().Refine(validate.NonZeroFloat).WithDescription("Configured threshold value."),
		func(a AlertEvent) float64 { return a.Threshold },
		func(a *AlertEvent, v float64) { a.Threshold = v },
	),
	codex.RequiredField("timestamp",
		codex.String().Refine(validate.DateTime).WithDescription("Measurement time (RFC 3339)."),
		func(a AlertEvent) string { return a.Timestamp },
		func(a *AlertEvent, v string) { a.Timestamp = v },
	),
)

// ── Error-path ergonomics (events.ErrorChannel) ──────────────────────────────

// SensorOutOfRangeError is a domain validation error a downstream pipeline
// stage might emit for a reading outside acceptable bounds.
type SensorOutOfRangeError struct {
	SensorID string
	Value    float64
}

func (e SensorOutOfRangeError) Error() string {
	return fmt.Sprintf("sensor %s value %.1f out of range", e.SensorID, e.Value)
}

// SensorErrorPayload is the typed, codec-backed error reply published to the
// declared error-output topic when a SensorOutOfRangeError matches.
type SensorErrorPayload struct {
	Code    string
	Message string
}

var SensorErrorPayloadCodec = codex.Struct[SensorErrorPayload](
	codex.RequiredField("code", codex.String().Refine(validate.NonEmptyString),
		func(e SensorErrorPayload) string { return e.Code },
		func(e *SensorErrorPayload, v string) { e.Code = v },
	),
	codex.RequiredField("message", codex.String(),
		func(e SensorErrorPayload) string { return e.Message },
		func(e *SensorErrorPayload, v string) { e.Message = v },
	),
)
