// Package adapters-mqtt-security demonstrates SecurityFunc-based authentication
// for MQTT subscribe channels built with go-codex.
//
// # MQTT credential extraction — the limitation and the workarounds
//
// The paho.mqtt.golang library implements MQTT 3.1.1. Its pahomqtt.Message
// interface does not expose MQTT 5.0 User Properties, so the go-codex adapter
// cannot extract credentials from message metadata. Codec-level validation is
// intentionally skipped for MQTT; SecurityFunc is the enforcement point.
//
// Three patterns are shown in this example:
//
//  1. Closure (MQTT 3.1.1 / Paho): credentials are established out-of-band at
//     CONNECT time (username/password or a pre-shared API key) and captured in a
//     closure passed to SubscribeOptions. This is the recommended pattern for Paho.
//
//  2. msg extraction (MQTT 5.0 / custom broker): SecurityFunc receives the
//     pahomqtt.Message directly. If using an MQTT 5.0-capable library, extract
//     credentials from msg User Properties (e.g. a simulated "X-API-Key" header).
//     This example simulates the approach using a mock message with a headers map.
//
//  3. MessageFromContext (handler fn): after SecurityFunc approves the message,
//     the application handler can call [adaptermqtt.MessageFromContext] to access
//     the raw pahomqtt.Message — e.g. to inspect QoS, retained flag, or topic.
//
// This example shows:
//   - sensor/data/{sensorId} channel — secured with apiKeyAuth
//   - sensor/alerts channel         — publish only, no security (outbound)
//   - Pattern 1: API key via closure
//   - Pattern 2: API key via simulated msg user-property (X-API-Key header)
//   - Pattern 3: MessageFromContext inside the handler fn
//   - KindSecurity in SubscribeError on rejection
//   - SecurityObserver logging rejections
//   - AsyncAPI 3.0 spec output with securitySchemes
//
// Run with: go run ./examples/adapters-mqtt-security
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"

	adaptermqtt "github.com/DaniDeer/go-codex/adapters/mqtt"
	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/stats"
	"github.com/DaniDeer/go-codex/validate"
)

// ── Domain types ─────────────────────────────────────────────────────────────

// SensorReading is a measurement published by a sensor node.
type SensorReading struct {
	SensorID string
	Value    float64
	Unit     string
}

// SensorAlert is sent by this service when a threshold is breached.
type SensorAlert struct {
	SensorID string
	Message  string
}

// ── Codecs ────────────────────────────────────────────────────────────────────

var sensorReadingCodec = codex.Struct[SensorReading](
	codex.RequiredField("sensorId",
		codex.String().Refine(validate.NonEmptyString).WithDescription("Sensor identifier."),
		func(r SensorReading) string { return r.SensorID },
		func(r *SensorReading, v string) { r.SensorID = v },
	),
	codex.RequiredField("value",
		codex.Float64().WithDescription("Measured value."),
		func(r SensorReading) float64 { return r.Value },
		func(r *SensorReading, v float64) { r.Value = v },
	),
	codex.RequiredField("unit",
		codex.String().Refine(validate.NonEmptyString).WithDescription("Unit of measure (e.g. celsius, bar)."),
		func(r SensorReading) string { return r.Unit },
		func(r *SensorReading, v string) { r.Unit = v },
	),
)

var sensorAlertCodec = codex.Struct[SensorAlert](
	codex.RequiredField("sensorId",
		codex.String().Refine(validate.NonEmptyString).WithDescription("Sensor identifier."),
		func(a SensorAlert) string { return a.SensorID },
		func(a *SensorAlert, v string) { a.SensorID = v },
	),
	codex.RequiredField("message",
		codex.String().Refine(validate.NonEmptyString).WithDescription("Alert description."),
		func(a SensorAlert) string { return a.Message },
		func(a *SensorAlert, v string) { a.Message = v },
	),
)

// ── Mock API key store ────────────────────────────────────────────────────────

// validAPIKeys is a mock set of trusted API keys.
// In production this would be a database lookup or a signed token check.
var validAPIKeys = map[string]bool{
	"sensor-key-abc123": true,
	"sensor-key-xyz789": true,
}

// ── Observer ──────────────────────────────────────────────────────────────────

// telemetryObserver implements [stats.Observer] and [stats.SecurityObserver].
// It logs every event via slog and accumulates counters for a final summary.
// Replace the slog calls with Prometheus/OpenTelemetry instruments in production.
type telemetryObserver struct {
	stats.NoopObserver // satisfies RecordRequest (unused for MQTT)

	mu         sync.Mutex
	subscribed int
	published  int
	valErrors  int
	rejections int
	log        *slog.Logger
}

func newTelemetryObserver() *telemetryObserver {
	return &telemetryObserver{
		log: slog.Default().With("component", "observer"),
	}
}

func (o *telemetryObserver) RecordSubscribe(topic string, success bool, d time.Duration) {
	o.mu.Lock()
	o.subscribed++
	o.mu.Unlock()
	o.log.Info("subscribe", "topic", topic, "success", success, "duration_ms", d.Milliseconds())
}

func (o *telemetryObserver) RecordPublish(topic string, success bool, d time.Duration) {
	o.mu.Lock()
	o.published++
	o.mu.Unlock()
	o.log.Info("publish", "topic", topic, "success", success, "duration_ms", d.Milliseconds())
}

func (o *telemetryObserver) RecordValidationError(location, constraintName, field string) {
	o.mu.Lock()
	o.valErrors++
	o.mu.Unlock()
	o.log.Warn("validation error", "location", location, "constraint", constraintName, "field", field)
}

func (o *telemetryObserver) RecordSecurityRejection(location, scheme string) {
	o.mu.Lock()
	o.rejections++
	o.mu.Unlock()
	o.log.Warn("security rejection", "topic", location, "scheme", scheme)
	fmt.Printf("  ✗ security rejection: topic=%s scheme=%s\n", location, scheme)
}

func (o *telemetryObserver) Print() {
	o.mu.Lock()
	defer o.mu.Unlock()
	fmt.Printf("  messages received   : %d\n", o.subscribed)
	fmt.Printf("  messages published  : %d\n", o.published)
	fmt.Printf("  validation errors   : %d\n", o.valErrors)
	fmt.Printf("  security rejections : %d\n", o.rejections)
}

// ── Mock MQTT message ─────────────────────────────────────────────────────────

// mockMessage implements pahomqtt.Message for testing without a real broker.
// The headers map simulates MQTT 5.0 User Properties — a real MQTT 5.0 library
// would expose these via a Properties() API on the message.
type mockMessage struct {
	topic   string
	payload []byte
	headers map[string]string // simulated MQTT 5.0 User Properties
}

func (m *mockMessage) Duplicate() bool   { return false }
func (m *mockMessage) Qos() byte         { return 1 }
func (m *mockMessage) Retained() bool    { return false }
func (m *mockMessage) Topic() string     { return m.topic }
func (m *mockMessage) MessageID() uint16 { return 1 }
func (m *mockMessage) Payload() []byte   { return m.payload }
func (m *mockMessage) Ack()              {}

// userProperty simulates msg.Properties().User.Get(key) from an MQTT 5.0 library.
func (m *mockMessage) userProperty(key string) string { return m.headers[key] }

// ── Channel builder ───────────────────────────────────────────────────────────

func buildChannels() (
	sensorData *events.ChannelHandle[SensorReading],
	sensorAlerts *events.ChannelHandle[SensorAlert],
	b *events.Builder,
) {
	b = events.NewBuilder(events.Info{
		Title:       "Sensor Network Events",
		Version:     "1.0.0",
		Description: "Channels for the sensor data ingestion service.",
	})

	b.AddServer("production", events.Server{
		URL:         "mqtt.example.com",
		Protocol:    "mqtt",
		Description: "Production MQTT broker",
	})

	// Register an API key security scheme.
	// The Codec field is omitted (nil) — MQTT 3.1.1 does not carry credentials in
	// message metadata, so there is nothing to extract for codec-level validation.
	// SecurityFunc is the enforcement point for MQTT.
	b.AddSecurityScheme("apiKeyAuth", events.SecurityScheme{
		SecurityScheme: route.APIKeyScheme("X-API-Key", "header"),
	})

	var err error

	// sensor/data/{sensorId} — action: receive — secured with apiKeyAuth.
	sensorData, err = events.AddChannel[SensorReading](b, "sensor/data", sensorReadingCodec,
		events.ChannelMeta{Description: "Sensor readings received from the sensor network."},
		events.Subscribe{
			Summary:    "Receive sensor reading",
			Tags:       []string{"sensor", "iot"},
			SchemaName: "SensorReading",
			// Security: requires apiKeyAuth. Enforced by SecurityFunc.
			Security: []route.SecurityRequirement{route.Require("apiKeyAuth")},
		},
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "channel registration failed: %v\n", err)
		os.Exit(1)
	}

	// sensor/alerts — action: send — no security (outbound publish).
	sensorAlerts, err = events.AddChannel[SensorAlert](b, "sensor/alerts", sensorAlertCodec,
		events.ChannelMeta{Description: "Alert events produced by this service on threshold breach."},
		events.Publish{
			Summary:    "Send sensor alert",
			Tags:       []string{"sensor", "alerts"},
			SchemaName: "SensorAlert",
		},
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "channel registration failed: %v\n", err)
		os.Exit(1)
	}

	return
}

func main() {
	sensorData, sensorAlerts, b := buildChannels()
	obs := newTelemetryObserver()

	fmt.Println("=== adapters-mqtt-security demo ===")
	fmt.Println()
	fmt.Println("MQTT 3.1.1 cannot carry credentials in message metadata.")
	fmt.Println("Three workaround patterns are demonstrated below.")
	fmt.Println()

	// ── Pattern 1: closure ────────────────────────────────────────────────────
	// Credentials are established out-of-band at CONNECT time and captured in a
	// closure. This is the recommended pattern for Paho / MQTT 3.1.1.

	// makeHandler simulates the per-connection setup: each MQTT client connects
	// with an API key (e.g. from CONNECT username/password) and the SecurityFunc
	// closes over that key for every message on the connection.
	makeHandler := func(clientAPIKey string) adaptermqtt.SubscribeOptions {
		return adaptermqtt.SubscribeOptions{
			Observer: obs,
			OnError: func(e adaptermqtt.SubscribeError) {
				fmt.Printf("  [error] kind=%s topic=%s: %v\n", e.Kind, e.Topic, e.Err)
			},
			// Pattern 1 — closure: clientAPIKey was set at CONNECT time.
			SecurityFunc: func(ctx context.Context, msg pahomqtt.Message, reqs []route.SecurityRequirement) error {
				if !validAPIKeys[clientAPIKey] {
					return fmt.Errorf("unknown API key %q", clientAPIKey)
				}
				return nil
			},
		}
	}

	ctx := context.Background()
	payload := []byte(`{"sensorId":"temp-01","value":42.5,"unit":"celsius"}`)

	// ── Scenario 1: valid API key ─────────────────────────────────────────────
	fmt.Println("--- Scenario 1: valid API key ---")
	accepted := false
	handler := adaptermqtt.SubscribeHandler(ctx, sensorData,
		func(_ context.Context, r SensorReading) error {
			fmt.Printf("  ✓ handler called: sensorId=%s value=%.1f unit=%s\n",
				r.SensorID, r.Value, r.Unit)
			accepted = true
			return nil
		},
		makeHandler("sensor-key-abc123"),
	)
	handler(nil, &mockMessage{topic: sensorData.Topic, payload: payload})
	if !accepted {
		fmt.Println("  [unexpected] handler was not called")
	}
	fmt.Println()

	// ── Scenario 2: invalid API key ───────────────────────────────────────────
	fmt.Println("--- Scenario 2: invalid API key (KindSecurity rejection) ---")
	handler = adaptermqtt.SubscribeHandler(ctx, sensorData,
		func(_ context.Context, r SensorReading) error {
			fmt.Println("  [unexpected] handler should not be called")
			return nil
		},
		makeHandler("bad-key"),
	)
	handler(nil, &mockMessage{topic: sensorData.Topic, payload: payload})
	fmt.Println()

	// ── Scenario 3: Pattern 2 — msg extraction (MQTT 5.0 / custom broker) ──────
	// SecurityFunc receives the pahomqtt.Message directly. An MQTT 5.0 library
	// would expose User Properties via msg.Properties().User — here we simulate
	// that via the mockMessage.userProperty helper (see type definition above).
	fmt.Println("--- Scenario 3: Pattern 2 — credential extracted from msg (MQTT 5.0 simulation) ---")
	msgOpts := adaptermqtt.SubscribeOptions{
		Observer: obs,
		OnError: func(e adaptermqtt.SubscribeError) {
			fmt.Printf("  [error] kind=%s topic=%s: %v\n", e.Kind, e.Topic, e.Err)
		},
		// Pattern 2 — msg extraction: read the credential from a User Property.
		// With a real MQTT 5.0 library replace m.userProperty("X-API-Key") with:
		//   msg.Properties().User.Get("X-API-Key")
		SecurityFunc: func(ctx context.Context, msg pahomqtt.Message, reqs []route.SecurityRequirement) error {
			m, ok := msg.(*mockMessage)
			if !ok {
				return fmt.Errorf("cannot read user properties from this message type")
			}
			apiKey := m.userProperty("X-API-Key")
			if !validAPIKeys[apiKey] {
				return fmt.Errorf("missing or unknown X-API-Key user property %q", apiKey)
			}
			return nil
		},
	}

	validMsg := &mockMessage{
		topic:   sensorData.Topic,
		payload: payload,
		headers: map[string]string{"X-API-Key": "sensor-key-abc123"},
	}
	invalidMsg := &mockMessage{
		topic:   sensorData.Topic,
		payload: payload,
		headers: map[string]string{"X-API-Key": ""},
	}

	handler = adaptermqtt.SubscribeHandler(ctx, sensorData,
		func(_ context.Context, r SensorReading) error {
			fmt.Printf("  ✓ handler called: sensorId=%s (key from msg user-property)\n", r.SensorID)
			return nil
		}, msgOpts)
	handler(nil, validMsg)

	handler = adaptermqtt.SubscribeHandler(ctx, sensorData,
		func(_ context.Context, r SensorReading) error {
			fmt.Println("  [unexpected] handler should not be called")
			return nil
		}, msgOpts)
	handler(nil, invalidMsg)
	fmt.Println()

	// ── Scenario 4: Pattern 3 — MessageFromContext inside handler fn ──────────
	// After SecurityFunc approves the message, the application handler can call
	// adaptermqtt.MessageFromContext(ctx) to access the original pahomqtt.Message.
	// This is useful for inspecting QoS, retained flag, or raw topic.
	fmt.Println("--- Scenario 4: Pattern 3 — MessageFromContext inside handler fn ---")
	handler = adaptermqtt.SubscribeHandler(ctx, sensorData,
		func(ctx context.Context, r SensorReading) error {
			if msg, ok := adaptermqtt.MessageFromContext(ctx); ok {
				fmt.Printf("  ✓ handler: sensorId=%s | msg.QoS=%d retained=%v topic=%s\n",
					r.SensorID, msg.Qos(), msg.Retained(), msg.Topic())
			}
			return nil
		},
		makeHandler("sensor-key-abc123"),
	)
	handler(nil, &mockMessage{topic: sensorData.Topic, payload: payload})
	fmt.Println()

	// ── Scenario 5: no security on publish channel ────────────────────────────
	fmt.Println("--- Scenario 5: encode and publish alert (no security) ---")
	alert := SensorAlert{SensorID: "temp-01", Message: "Temperature threshold exceeded: 42.5°C"}
	encoded, err := sensorAlerts.Encode(alert)
	if err != nil {
		fmt.Fprintf(os.Stderr, "encode error: %v\n", err)
	} else {
		fmt.Printf("  ✓ encoded alert payload: %s\n", encoded)
	}
	fmt.Println()

	// ── Observer summary ──────────────────────────────────────────────────────
	fmt.Println("=== Observer summary ===")
	obs.Print()
	fmt.Println()

	// ── AsyncAPI 3.0 spec ─────────────────────────────────────────────────────
	fmt.Println("=== AsyncAPI 3.0 spec ===")
	fmt.Println()
	doc, err := b.AsyncAPISpec()
	if err != nil {
		fmt.Fprintf(os.Stderr, "AsyncAPISpec error: %v\n", err)
		os.Exit(1)
	}
	yamlBytes, err := doc.MarshalYAML()
	if err != nil {
		fmt.Fprintf(os.Stderr, "MarshalYAML error: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(string(yamlBytes))

}
