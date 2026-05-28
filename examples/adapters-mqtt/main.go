// Package adapters-mqtt demonstrates the three-layer codec pipeline pattern for
// event-driven / MQTT applications.
//
// Use case: our service subscribes to measurement points published by a sensor
// network, writes each measurement to a time series database, and publishes an
// alert if a measurement exceeds a configured threshold.
//
// Three boundary codecs:
//
// Codec[MeasurementEvent]  — MQTT subscribe contract (what the sensor network sends)
// Codec[TimeSeriesRecord]  — database contract (the TSDB schema)
// Codec[AlertEvent]        — MQTT publish contract (what we send on threshold breach)
//
// Two pipelines driven by the same three-layer model:
//
// measurementEventCodec ─ decode ─▶ MeasurementEvent ─▶ buildTimeSeriesRecord ─▶ TimeSeriesRecord
//
//	│                                           ↓ (store IO via Codec[TimeSeriesRecord])
//	└──▶ shouldAlert ──true──▶ buildAlertEvent ─▶ alertEventCodec ─ encode ─▶ MQTT publish
//
// The infrastructure layer owns all IO: MQTT subscribe/publish, database reads
// and writes. The store uses Codec[TimeSeriesRecord] for encode/decode — the
// database schema is defined once in the codec, exactly like the MQTT contracts.
//
// Pure domain functions (Layer 2) have zero IO and can be unit-tested with
// plain Go structs and no broker, no store, no setup.
//
// Run with: go run ./examples/adapters-mqtt
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"

	adaptermqtt "github.com/DaniDeer/go-codex/adapters/mqtt"
	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/validate"
)

// ── Layer 1: Domain models and codec contracts ─────────────────────────────────
//
// Each boundary is described by a codec. Shared field codec variables define
// domain constraints once; they propagate to every struct codec that uses them —
// MQTT subscribe, database schema, MQTT publish.

// sensorIDFieldCodec is the domain contract for a sensor identifier.
var sensorIDFieldCodec = codex.String().
	Refine(validate.NonEmptyString).
	WithDescription("Unique sensor identifier.")

// measurementValueCodec is the domain contract for a measurement reading.
var measurementValueCodec = codex.Float64().
	Refine(validate.NonZeroFloat).
	WithDescription("Measured value.")

// unitFieldCodec is the domain contract for a physical unit label.
var unitFieldCodec = codex.String().
	Refine(validate.NonEmptyString).
	WithDescription("Physical unit (e.g. celsius, bar, rpm).")

// ── MQTT subscribe boundary ───────────────────────────────────────────────────

// MeasurementEvent is the domain entity received from the sensor network.
type MeasurementEvent struct {
	SensorID  string
	Value     float64
	Unit      string
	Timestamp string // RFC 3339
}

// measurementEventCodec describes what the sensor network publishes.
// The codec enforces domain invariants on every incoming message.
var measurementEventCodec = codex.Struct[MeasurementEvent](
	codex.RequiredField[MeasurementEvent, string]("sensor_id", sensorIDFieldCodec,
		func(m MeasurementEvent) string { return m.SensorID },
		func(m *MeasurementEvent, v string) { m.SensorID = v },
	),
	codex.RequiredField[MeasurementEvent, float64]("value", measurementValueCodec,
		func(m MeasurementEvent) float64 { return m.Value },
		func(m *MeasurementEvent, v float64) { m.Value = v },
	),
	codex.RequiredField[MeasurementEvent, string]("unit", unitFieldCodec,
		func(m MeasurementEvent) string { return m.Unit },
		func(m *MeasurementEvent, v string) { m.Unit = v },
	),
	codex.RequiredField[MeasurementEvent, string]("timestamp",
		codex.String().Refine(validate.DateTime).WithDescription("Measurement time (RFC 3339)."),
		func(m MeasurementEvent) string { return m.Timestamp },
		func(m *MeasurementEvent, v string) { m.Timestamp = v },
	),
)

// ── Database boundary (TSDB schema) ──────────────────────────────────────────

// TimeSeriesRecord is the domain entity written to the time series database.
// Column names and types are defined once — in the codec.
type TimeSeriesRecord struct {
	SensorID   string
	Value      float64
	Unit       string
	Timestamp  string // original measurement time (RFC 3339)
	ReceivedAt string // ingestion time (RFC 3339)
}

// timeSeriesRecordCodec describes the TSDB schema. The store uses this codec
// to encode records on write and decode rows on read — exactly the same
// mechanism as the MQTT boundaries.
var timeSeriesRecordCodec = codex.Struct[TimeSeriesRecord](
	codex.RequiredField[TimeSeriesRecord, string]("sensor_id", sensorIDFieldCodec,
		func(r TimeSeriesRecord) string { return r.SensorID },
		func(r *TimeSeriesRecord, v string) { r.SensorID = v },
	),
	codex.RequiredField[TimeSeriesRecord, float64]("value", measurementValueCodec,
		func(r TimeSeriesRecord) float64 { return r.Value },
		func(r *TimeSeriesRecord, v float64) { r.Value = v },
	),
	codex.RequiredField[TimeSeriesRecord, string]("unit", unitFieldCodec,
		func(r TimeSeriesRecord) string { return r.Unit },
		func(r *TimeSeriesRecord, v string) { r.Unit = v },
	),
	codex.RequiredField[TimeSeriesRecord, string]("timestamp",
		codex.String().Refine(validate.DateTime).WithDescription("Measurement time (RFC 3339)."),
		func(r TimeSeriesRecord) string { return r.Timestamp },
		func(r *TimeSeriesRecord, v string) { r.Timestamp = v },
	),
	codex.RequiredField[TimeSeriesRecord, string]("received_at",
		codex.String().Refine(validate.DateTime).WithDescription("Ingestion time (RFC 3339)."),
		func(r TimeSeriesRecord) string { return r.ReceivedAt },
		func(r *TimeSeriesRecord, v string) { r.ReceivedAt = v },
	),
)

// ── MQTT publish boundary ─────────────────────────────────────────────────────

// AlertEvent is the domain entity published when a measurement exceeds a threshold.
type AlertEvent struct {
	SensorID  string
	Value     float64
	Unit      string
	Threshold float64
	Timestamp string // RFC 3339
}

// alertEventCodec describes what this service publishes on threshold breach.
// Shared field codecs propagate the same constraints from the incoming contract.
var alertEventCodec = codex.Struct[AlertEvent](
	codex.RequiredField[AlertEvent, string]("sensor_id", sensorIDFieldCodec,
		func(a AlertEvent) string { return a.SensorID },
		func(a *AlertEvent, v string) { a.SensorID = v },
	),
	codex.RequiredField[AlertEvent, float64]("value", measurementValueCodec,
		func(a AlertEvent) float64 { return a.Value },
		func(a *AlertEvent, v float64) { a.Value = v },
	),
	codex.RequiredField[AlertEvent, string]("unit", unitFieldCodec,
		func(a AlertEvent) string { return a.Unit },
		func(a *AlertEvent, v string) { a.Unit = v },
	),
	codex.RequiredField[AlertEvent, float64]("threshold",
		codex.Float64().Refine(validate.NonZeroFloat).WithDescription("Configured threshold value."),
		func(a AlertEvent) float64 { return a.Threshold },
		func(a *AlertEvent, v float64) { a.Threshold = v },
	),
	codex.RequiredField[AlertEvent, string]("timestamp",
		codex.String().Refine(validate.DateTime).WithDescription("Measurement time (RFC 3339)."),
		func(a AlertEvent) string { return a.Timestamp },
		func(a *AlertEvent, v string) { a.Timestamp = v },
	),
)

// ── Layer 2: Business logic (pure domain functions) ───────────────────────────
//
// Pure domain functions transform between domain types. Zero IO — no database,
// no MQTT, no side effects. They encode business rules as data transformations
// and can be unit-tested with plain Go structs.

// buildTimeSeriesRecord maps an incoming measurement to a database record.
// ReceivedAt is the only field that depends on wall-clock time; in tests,
// pass a known value via the nowFn parameter.
func buildTimeSeriesRecord(m MeasurementEvent, receivedAt string) TimeSeriesRecord {
	return TimeSeriesRecord{
		SensorID:   m.SensorID,
		Value:      m.Value,
		Unit:       m.Unit,
		Timestamp:  m.Timestamp,
		ReceivedAt: receivedAt,
	}
}

// shouldAlert returns true when the measurement value exceeds the threshold.
func shouldAlert(m MeasurementEvent, threshold float64) bool {
	return m.Value > threshold
}

// buildAlertEvent creates an alert payload from a threshold-breaching measurement.
func buildAlertEvent(m MeasurementEvent, threshold float64) AlertEvent {
	return AlertEvent{
		SensorID:  m.SensorID,
		Value:     m.Value,
		Unit:      m.Unit,
		Threshold: threshold,
		Timestamp: m.Timestamp,
	}
}

// ── Layer 3: Infrastructure (MQTT + database) ─────────────────────────────────
//
// Infrastructure closures and types orchestrate all IO. The TimeSeriesStore
// uses timeSeriesRecordCodec to encode rows on write and decode rows on read.
// The MQTT client is injected as a dependency — swappable without touching L1/L2.

// withDomainLoggingErr is a decorator for handlers that return only error.
// Logs success (Info) or failure (Error) after the handler returns. This pattern
// separates the logging concern from the handler body, keeping L2/L3 business
// logic clean while providing consistent observability.
func withDomainLoggingErr[Req any](
	name string,
	handler func(context.Context, Req) error,
	logger *slog.Logger,
	extractAttrs func(Req) []slog.Attr,
) func(context.Context, Req) error {
	return func(ctx context.Context, req Req) error {
		err := handler(ctx, req)
		if err != nil {
			logger.ErrorContext(ctx, name+" failed", "error", err)
		} else {
			attrs := extractAttrs(req)
			// Convert []slog.Attr to []any for InfoContext
			args := make([]any, 0, len(attrs)*2)
			for _, attr := range attrs {
				args = append(args, attr.Key, attr.Value.Any())
			}
			logger.InfoContext(ctx, name+" succeeded", args...)
		}
		return err
	}
}

// TimeSeriesStore is a mock TSDB that operates via timeSeriesRecordCodec.
// Replace with a real InfluxDB / TimescaleDB / Prometheus client; the
// codec encode/decode mechanism stays.
type TimeSeriesStore struct {
	mu   sync.Mutex
	rows []map[string]any // simulates an append-only time series table
}

func newTimeSeriesStore() *TimeSeriesStore {
	return &TimeSeriesStore{}
}

// Append encodes the TimeSeriesRecord using timeSeriesRecordCodec
// (analogous to a TSDB INSERT / line protocol write).
func (s *TimeSeriesStore) Append(r TimeSeriesRecord) error {
	encoded, err := timeSeriesRecordCodec.Encode(r)
	if err != nil {
		return err
	}
	row, ok := encoded.(map[string]any)
	if !ok {
		return fmt.Errorf("unexpected encoded type %T", encoded)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows = append(s.rows, row)
	return nil
}

// Count returns the number of stored rows.
func (s *TimeSeriesStore) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.rows)
}

// All decodes and returns all stored rows via timeSeriesRecordCodec.
func (s *TimeSeriesStore) All() []TimeSeriesRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]TimeSeriesRecord, 0, len(s.rows))
	for _, row := range s.rows {
		r, err := timeSeriesRecordCodec.Decode(row)
		if err == nil {
			out = append(out, r)
		}
	}
	return out
}

// makeHandleMeasurement returns the infrastructure handler for incoming measurements.
//
// Pipeline: decode (codec) → buildTimeSeriesRecord (L2) → Append (store IO)
//
// → shouldAlert (L2) → publishAlert (MQTT IO)
func makeHandleMeasurement(
	store *TimeSeriesStore,
	threshold float64,
	publishAlert func(context.Context, AlertEvent) error,
) func(context.Context, MeasurementEvent) error {
	return func(ctx context.Context, m MeasurementEvent) error {
		receivedAt := time.Now().UTC().Format(time.RFC3339)
		record := buildTimeSeriesRecord(m, receivedAt) // L2: pure transform
		if err := store.Append(record); err != nil {   // L3: database IO
			return fmt.Errorf("tsdb append: %w", err)
		}
		if shouldAlert(m, threshold) { // L2: pure business rule
			alert := buildAlertEvent(m, threshold)           // L2: pure transform
			if err := publishAlert(ctx, alert); err != nil { // L3: MQTT IO
				return fmt.Errorf("publish alert: %w", err)
			}
		}
		return nil
	}
}

// ── Mock MQTT client (replace with a real paho client in production) ──────────

type mockToken struct{ done chan struct{} }

func newMockToken() *mockToken {
	t := &mockToken{done: make(chan struct{})}
	close(t.done)
	return t
}

func (t *mockToken) Wait() bool                       { return true }
func (t *mockToken) WaitTimeout(_ time.Duration) bool { return true }
func (t *mockToken) Done() <-chan struct{}            { return t.done }
func (t *mockToken) Error() error                     { return nil }

type mockMessage struct {
	topic   string
	payload []byte
}

func (m *mockMessage) Duplicate() bool   { return false }
func (m *mockMessage) Qos() byte         { return 0 }
func (m *mockMessage) Retained() bool    { return false }
func (m *mockMessage) Topic() string     { return m.topic }
func (m *mockMessage) MessageID() uint16 { return 0 }
func (m *mockMessage) Payload() []byte   { return m.payload }
func (m *mockMessage) Ack()              {}

type mockClient struct {
	mu       sync.Mutex
	handlers map[string]pahomqtt.MessageHandler
}

func newMockClient() *mockClient {
	return &mockClient{handlers: make(map[string]pahomqtt.MessageHandler)}
}

func (c *mockClient) Subscribe(topic string, _ byte, handler pahomqtt.MessageHandler) pahomqtt.Token {
	c.mu.Lock()
	c.handlers[topic] = handler
	c.mu.Unlock()
	return newMockToken()
}

func (c *mockClient) Publish(topic string, _ byte, _ bool, payload interface{}) pahomqtt.Token {
	fmt.Printf("[broker] Published to %s: %s\n", topic, payload.([]byte))
	return newMockToken()
}

func (c *mockClient) deliver(topic string, payload []byte) {
	c.mu.Lock()
	h := c.handlers[topic]
	c.mu.Unlock()
	if h != nil {
		h(c, &mockMessage{topic: topic, payload: payload})
	}
}

func (c *mockClient) IsConnected() bool       { return true }
func (c *mockClient) IsConnectionOpen() bool  { return true }
func (c *mockClient) Connect() pahomqtt.Token { return newMockToken() }
func (c *mockClient) Disconnect(_ uint)       {}
func (c *mockClient) SubscribeMultiple(_ map[string]byte, _ pahomqtt.MessageHandler) pahomqtt.Token {
	return newMockToken()
}
func (c *mockClient) Unsubscribe(_ ...string) pahomqtt.Token       { return newMockToken() }
func (c *mockClient) AddRoute(_ string, _ pahomqtt.MessageHandler) {}
func (c *mockClient) OptionsReader() pahomqtt.ClientOptionsReader {
	return pahomqtt.ClientOptionsReader{}
}

// sensorTopicConstraint is a custom Constraint[string] that enforces the
// sensors/{sensorID}/<action> topic structure used by this service.
// It validates that:
//   - The topic has exactly 3 slash-separated segments.
//   - The first segment is "sensors".
//
// Template variable placeholders such as {sensorID} are accepted — the UUID
// content of that segment is validated at runtime via TopicParamCodecs when
// BuildTopic is called with a concrete sensor ID.
//
// Composed with validate.MQTTPublishTopic via WithTopicConstraints, any topic
// that violates either rule causes AddChannel to return an InvalidTopicError
// immediately — before the channel is registered.
var sensorTopicConstraint = codex.Constraint[string]{
	Name: "sensor-topic-format",
	Check: func(v string) bool {
		parts := strings.SplitN(v, "/", 4)
		return len(parts) == 3 && parts[0] == "sensors"
	},
	Message: func(v string) string {
		return fmt.Sprintf("topic must follow sensors/<id>/<action> format, got %q", v)
	},
}

// sensorUUID is the fixed sensor identifier used in this demo.
const sensorUUID = "f47ac10b-58cc-4372-a567-0e02b2c3d479"

func main() {
	ctx := context.Background()
	store := newTimeSeriesStore()
	const threshold = 75.0 // alert when value > 75

	// Create separate loggers for domain and transport concerns.
	domainLogger := slog.Default().With("layer", "domain")
	mqttLogger := slog.Default().With("transport", "mqtt")

	// Build the event API description (transport-agnostic).
	b := events.NewBuilder(events.Info{
		Title:       "Measurement Ingestion Service",
		Version:     "1.0.0",
		Description: "Subscribe to sensor measurements, persist to TSDB, alert on threshold breach.",
	},
		// WithTopicConstraints is optional. Compose built-in and custom constraints:
		// - MQTTPublishTopic: non-empty, no wildcard characters (+ or #).
		// - sensorTopicConstraint: topic must follow sensors/<uuid>/<action> format.
		// AddChannel returns an InvalidTopicError immediately if either check fails.
		events.WithTopicConstraints(validate.MQTTPublishTopic, sensorTopicConstraint),
	)
	b.AddServer("production", events.Server{
		URL:      "mqtt://broker.example.com:1883",
		Protocol: "mqtt",
	})

	measurementChannel, err := events.AddChannel[MeasurementEvent](b, "sensors/{sensorID}/measurements", measurementEventCodec,
		events.ChannelConfig{
			Description: "Measurement points published by the sensor network.",
			Subscribe: &events.OperationConfig{
				Summary:    "Receive sensor measurement",
				SchemaName: "MeasurementEvent",
			},
			// TopicParams enriches the AsyncAPI spec with a description for {sensorID}.
			TopicParams: []events.TopicParam{
				{Name: "sensorID", Description: "UUID of the sensor publishing the measurement."},
			},
			// TopicParamCodecs validates {sensorID} as a UUID when BuildTopic is called.
			// The UUID codec schema (format: uuid, type: string) also flows into the spec.
			TopicParamCodecs: map[string]codex.Codec[string]{
				"sensorID": codex.String().Refine(validate.UUID),
			},
		})
	if err != nil {
		fmt.Fprintf(os.Stderr, "channel registration failed: %v\n", err)
		os.Exit(1)
	}

	alertChannel, err := events.AddChannel[AlertEvent](b, "sensors/{sensorID}/alerts", alertEventCodec,
		events.ChannelConfig{
			Description: "Threshold breach alerts published by this service.",
			Publish: &events.OperationConfig{
				Summary:    "Publish threshold alert",
				SchemaName: "AlertEvent",
			},
			// TopicParams enriches the AsyncAPI spec with a description for {sensorID}.
			TopicParams: []events.TopicParam{
				{Name: "sensorID", Description: "UUID of the sensor that triggered the alert."},
			},
			// TopicParamCodecs validates {sensorID} as a UUID when BuildTopic is called.
			// The UUID codec schema (format: uuid, type: string) also flows into the spec.
			TopicParamCodecs: map[string]codex.Codec[string]{
				"sensorID": codex.String().Refine(validate.UUID),
			},
		})
	if err != nil {
		fmt.Fprintf(os.Stderr, "channel registration failed: %v\n", err)
		os.Exit(1)
	}

	// Wire infrastructure: inject store + alert publish function.
	// Use domain logging decorator to separate logging concern from handler body.
	client := newMockClient()

	// BuildTopic substitutes {sensorID} and validates it against the UUID codec.
	// The concrete topic is needed for client.Subscribe and client.deliver.
	measurementTopic, err := measurementChannel.BuildTopic(map[string]string{"sensorID": sensorUUID})
	if err != nil {
		fmt.Fprintf(os.Stderr, "BuildTopic error: %v\n", err)
		os.Exit(1)
	}

	// Publish uses the vars map to build and validate the concrete alert topic at publish time.
	// Pass nil for static topics (no template variables).
	publishAlert := func(ctx context.Context, alert AlertEvent) error {
		return adaptermqtt.Publish(ctx, client, alertChannel, 1, false, alert,
			map[string]string{"sensorID": sensorUUID})
	}

	// Attribute extractor for domain logging — extract business-relevant fields from requests.
	extractMeasurementAttrs := func(m MeasurementEvent) []slog.Attr {
		return []slog.Attr{
			slog.String("sensor_id", m.SensorID),
			slog.Float64("value", m.Value),
			slog.String("unit", m.Unit),
		}
	}

	handler := withDomainLoggingErr("measurement.process",
		makeHandleMeasurement(store, threshold, publishAlert),
		domainLogger,
		extractMeasurementAttrs,
	)

	client.Subscribe(measurementTopic, 1,
		adaptermqtt.SubscribeHandler(ctx, measurementChannel, handler,
			func(e adaptermqtt.SubscribeError) {
				// Use mqttLogger for transport-level errors.
				// Switch on Kind to distinguish decode vs handler failures.
				switch e.Kind {
				case adaptermqtt.KindDecode:
					var validationErrs codex.ValidationErrors
					if errors.As(e.Err, &validationErrs) {
						mqttLogger.Warn("decode failed: validation errors",
							"topic", e.Topic,
							"errors", validationErrs, // triggers ValidationErrors.LogValue()
						)
					} else {
						mqttLogger.Warn("decode failed",
							"topic", e.Topic,
							"error", e.Err, // triggers TypeMismatchError.LogValue() etc.
						)
					}
				case adaptermqtt.KindHandler:
					mqttLogger.Error("handler failed",
						"topic", e.Topic,
						"error", e.Err,
					)
				}
			},
		),
	)

	// Simulate the broker delivering messages.
	fmt.Println("=== Measurement within threshold (no alert) ===")
	client.deliver(measurementTopic,
		[]byte(`{"sensor_id":"temp-01","value":62.5,"unit":"celsius","timestamp":"2024-01-15T10:30:00Z"}`))
	fmt.Printf("TSDB rows: %d\n\n", store.Count())

	fmt.Println("=== Measurement exceeding threshold (alert published) ===")
	client.deliver(measurementTopic,
		[]byte(`{"sensor_id":"temp-01","value":87.3,"unit":"celsius","timestamp":"2024-01-15T10:31:00Z"}`))
	fmt.Printf("TSDB rows: %d\n\n", store.Count())

	fmt.Println("=== Malformed message (decode error, no store write) ===")
	client.deliver(measurementTopic,
		[]byte(`{"sensor_id":"","value":"not-a-number"}`))
	fmt.Printf("TSDB rows: %d (unchanged)\n\n", store.Count())

	// Generate AsyncAPI 2.6 spec from the same codec definitions.
	fmt.Println("=== AsyncAPI 2.6 spec (derived from domain codecs) ===")
	doc, err := b.AsyncAPISpec()
	if err != nil {
		fmt.Fprintf(os.Stderr, "AsyncAPISpec error: %v\n", err)
		os.Exit(1)
	}
	yaml, err := doc.MarshalYAML()
	if err != nil {
		fmt.Fprintf(os.Stderr, "MarshalYAML error: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(string(yaml))
}
