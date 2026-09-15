// Package handlers holds SERVER-side business logic (subscribe handlers,
// domain transforms) — adapter-agnostic, imported by every broker package
// and demo file. Kept separate from routes/ (pure spec declarations) and
// observability/ (this example's own stats.Observer implementation),
// mirroring examples/reqreply-api's own package layout.
package handlers

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/DaniDeer/go-codex/examples/events-api/routes"
)

// PrintReading is the simplest possible subscribe handler — used by the
// Client.Attach workflow, wildcard subscription, and zeromq roundtrip demos.
func PrintReading(label string) func(context.Context, routes.SensorReading) error {
	return func(_ context.Context, r routes.SensorReading) error {
		fmt.Printf("  [%s] sensorId=%s value=%.1f\n", label, r.SensorID, r.Value)
		return nil
	}
}

// ── Domain-boundary pipeline (pure domain functions, zero IO) ────────────────
//
// Preserves adapters-mqtt's Layer 2 business-logic separation: pure
// transforms between domain types, unit-testable with plain Go structs.

// BuildTimeSeriesRecord maps an incoming measurement to a database record.
func BuildTimeSeriesRecord(m routes.MeasurementEvent, receivedAt string) routes.TimeSeriesRecord {
	return routes.TimeSeriesRecord{
		SensorID:   m.SensorID,
		Value:      m.Value,
		Unit:       m.Unit,
		Timestamp:  m.Timestamp,
		ReceivedAt: receivedAt,
	}
}

// ShouldAlert returns true when the measurement value exceeds the threshold.
func ShouldAlert(m routes.MeasurementEvent, threshold float64) bool {
	return m.Value > threshold
}

// BuildAlertEvent creates an alert payload from a threshold-breaching measurement.
func BuildAlertEvent(m routes.MeasurementEvent, threshold float64) routes.AlertEvent {
	return routes.AlertEvent{
		SensorID:  m.SensorID,
		Value:     m.Value,
		Unit:      m.Unit,
		Threshold: threshold,
		Timestamp: m.Timestamp,
	}
}

// TimeSeriesStore is a mock TSDB — replace with a real InfluxDB/TimescaleDB/
// Prometheus client; the codec encode/decode mechanism (via
// routes.TimeSeriesRecordCodec) stays unchanged either way.
type TimeSeriesStore struct {
	mu      sync.Mutex
	records []routes.TimeSeriesRecord
}

// Write appends a record to the store (in-memory, for this demo).
func (s *TimeSeriesStore) Write(r routes.TimeSeriesRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, r)
}

// Count returns the number of records written so far.
func (s *TimeSeriesStore) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.records)
}

// MeasurementHandler returns a subscribe handler implementing the full
// domain-boundary pipeline: decode (already done by the adapter) → write
// to the TSDB → evaluate the alert threshold → publish an AlertEvent when
// breached, via the supplied publish func (adapter-specific, injected as a
// dependency — swappable without touching this handler).
func MeasurementHandler(store *TimeSeriesStore, threshold float64, publishAlert func(context.Context, routes.AlertEvent) error) func(context.Context, routes.MeasurementEvent) error {
	return func(ctx context.Context, m routes.MeasurementEvent) error {
		store.Write(BuildTimeSeriesRecord(m, time.Now().UTC().Format(time.RFC3339)))
		if !ShouldAlert(m, threshold) {
			return nil
		}
		alert := BuildAlertEvent(m, threshold)
		fmt.Printf("  ⚠ threshold breached: sensorId=%s value=%.1f > %.1f\n", m.SensorID, m.Value, threshold)
		return publishAlert(ctx, alert)
	}
}
