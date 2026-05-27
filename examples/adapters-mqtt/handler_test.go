package main

import (
	"context"
	"testing"
	"time"
)

// ── Layer 2 pure function tests ───────────────────────────────────────────────
// No broker, no store, no setup — just plain Go structs.

var (
	testMeasurement = MeasurementEvent{
		SensorID:  "temp-01",
		Value:     87.3,
		Unit:      "celsius",
		Timestamp: "2024-01-15T10:31:00Z",
	}
	testThreshold = 75.0
	testReceived  = "2024-01-15T10:31:05Z"
)

func TestBuildTimeSeriesRecord(t *testing.T) {
	rec := buildTimeSeriesRecord(testMeasurement, testReceived)
	if rec.SensorID != testMeasurement.SensorID {
		t.Errorf("SensorID: want %q, got %q", testMeasurement.SensorID, rec.SensorID)
	}
	if rec.Value != testMeasurement.Value {
		t.Errorf("Value: want %f, got %f", testMeasurement.Value, rec.Value)
	}
	if rec.Unit != testMeasurement.Unit {
		t.Errorf("Unit: want %q, got %q", testMeasurement.Unit, rec.Unit)
	}
	if rec.Timestamp != testMeasurement.Timestamp {
		t.Errorf("Timestamp: want %q, got %q", testMeasurement.Timestamp, rec.Timestamp)
	}
	if rec.ReceivedAt != testReceived {
		t.Errorf("ReceivedAt: want %q, got %q", testReceived, rec.ReceivedAt)
	}
}

func TestShouldAlert(t *testing.T) {
	cases := []struct {
		name      string
		value     float64
		threshold float64
		want      bool
	}{
		{"below threshold", 50.0, 75.0, false},
		{"at threshold", 75.0, 75.0, false},
		{"above threshold", 87.3, 75.0, true},
	}
	for _, tc := range cases {
		m := MeasurementEvent{Value: tc.value}
		got := shouldAlert(m, tc.threshold)
		if got != tc.want {
			t.Errorf("%s: shouldAlert(%f, %f) = %v, want %v", tc.name, tc.value, tc.threshold, got, tc.want)
		}
	}
}

func TestBuildAlertEvent(t *testing.T) {
	alert := buildAlertEvent(testMeasurement, testThreshold)
	if alert.SensorID != testMeasurement.SensorID {
		t.Errorf("SensorID: want %q, got %q", testMeasurement.SensorID, alert.SensorID)
	}
	if alert.Value != testMeasurement.Value {
		t.Errorf("Value: want %f, got %f", testMeasurement.Value, alert.Value)
	}
	if alert.Threshold != testThreshold {
		t.Errorf("Threshold: want %f, got %f", testThreshold, alert.Threshold)
	}
	if alert.Timestamp != testMeasurement.Timestamp {
		t.Errorf("Timestamp: want %q, got %q", testMeasurement.Timestamp, alert.Timestamp)
	}
}

// ── Store codec roundtrip ─────────────────────────────────────────────────────

func TestTimeSeriesStoreRoundtrip(t *testing.T) {
	store := newTimeSeriesStore()
	rec := buildTimeSeriesRecord(testMeasurement, testReceived)
	if err := store.Append(rec); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if store.Count() != 1 {
		t.Fatalf("Count: want 1, got %d", store.Count())
	}
	rows := store.All()
	if len(rows) != 1 {
		t.Fatalf("All: want 1 row, got %d", len(rows))
	}
	got := rows[0]
	if got.SensorID != rec.SensorID || got.Value != rec.Value || got.ReceivedAt != rec.ReceivedAt {
		t.Errorf("roundtrip mismatch: want %+v, got %+v", rec, got)
	}
}

// ── Full pipeline test (no real broker) ──────────────────────────────────────

func TestHandleMeasurementPipeline(t *testing.T) {
	store := newTimeSeriesStore()
	var capturedAlerts []AlertEvent
	publishAlert := func(_ context.Context, a AlertEvent) error {
		capturedAlerts = append(capturedAlerts, a)
		return nil
	}
	handler := makeHandleMeasurement(store, testThreshold, publishAlert)
	ctx := context.Background()

	// below threshold — stored, no alert
	below := MeasurementEvent{
		SensorID:  "temp-01",
		Value:     60.0,
		Unit:      "celsius",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}
	if err := handler(ctx, below); err != nil {
		t.Fatalf("handler below threshold: %v", err)
	}
	if store.Count() != 1 {
		t.Errorf("store count: want 1, got %d", store.Count())
	}
	if len(capturedAlerts) != 0 {
		t.Errorf("alerts: want 0, got %d", len(capturedAlerts))
	}

	// above threshold — stored + alert
	if err := handler(ctx, testMeasurement); err != nil {
		t.Fatalf("handler above threshold: %v", err)
	}
	if store.Count() != 2 {
		t.Errorf("store count: want 2, got %d", store.Count())
	}
	if len(capturedAlerts) != 1 {
		t.Fatalf("alerts: want 1, got %d", len(capturedAlerts))
	}
	if capturedAlerts[0].SensorID != testMeasurement.SensorID {
		t.Errorf("alert SensorID: want %q, got %q", testMeasurement.SensorID, capturedAlerts[0].SensorID)
	}
	if capturedAlerts[0].Threshold != testThreshold {
		t.Errorf("alert Threshold: want %f, got %f", testThreshold, capturedAlerts[0].Threshold)
	}
}
