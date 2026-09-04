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

// CountingObserver implements [stats.Observer] and [stats.SecurityObserver].
// It accumulates counters for a final summary.
// Combine with [stats.NewLoggingObserver] via [stats.NewFanout] for logging.
type CountingObserver struct {
	stats.NoopObserver // satisfies RecordRequest (unused for MQTT)

	mu         sync.Mutex
	subscribed int
	published  int
	valErrors  int
	rejections int
}

func (o *CountingObserver) RecordSubscribe(topic string, success bool, d time.Duration) {
	o.mu.Lock()
	o.subscribed++
	o.mu.Unlock()
}

func (o *CountingObserver) RecordPublish(topic string, success bool, d time.Duration) {
	o.mu.Lock()
	o.published++
	o.mu.Unlock()
}

func (o *CountingObserver) RecordValidationError(location, constraintName, field string) {
	o.mu.Lock()
	o.valErrors++
	o.mu.Unlock()
}

func (o *CountingObserver) RecordSecurityRejection(location, scheme string) {
	o.mu.Lock()
	o.rejections++
	o.mu.Unlock()
}

func (o *CountingObserver) Print() {
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

// ── Mock MQTT client ──────────────────────────────────────────────────────────

// completedToken is a pahomqtt.Token that is already resolved — used so
// mockClient.Subscribe/Connect/Disconnect return immediately, mirroring
// adapters/mqtt's own test mock (adapter_test.go).
type completedToken struct{ done chan struct{} }

func newCompletedToken() *completedToken {
	t := &completedToken{done: make(chan struct{})}
	close(t.done)
	return t
}

func (t *completedToken) Wait() bool                       { return true }
func (t *completedToken) WaitTimeout(_ time.Duration) bool { return true }
func (t *completedToken) Done() <-chan struct{}            { return t.done }
func (t *completedToken) Error() error                     { return nil }

// mockClient implements pahomqtt.Client without a live broker. Only Subscribe
// is exercised — [adaptermqtt.NewSubscribeTransport]'s Subscribe method calls
// client.Subscribe to register a handler, then blocks on ctx.Done() (see
// adapters/mqtt/handletransport.go's doc comment). The registered handler is
// captured under mu so this demo can snapshot it and invoke it directly to
// simulate an incoming broker message, exactly mirroring
// adapters/mqtt/transport_test.go's TestNewSubscribeTransport_SubscribeHandle_RoundTrip.
type mockClient struct {
	mu      sync.Mutex
	handler pahomqtt.MessageHandler
}

func (c *mockClient) IsConnected() bool       { return true }
func (c *mockClient) IsConnectionOpen() bool  { return true }
func (c *mockClient) Connect() pahomqtt.Token { return newCompletedToken() }
func (c *mockClient) Disconnect(_ uint)       {}
func (c *mockClient) Publish(_ string, _ byte, _ bool, _ interface{}) pahomqtt.Token {
	return newCompletedToken()
}
func (c *mockClient) Subscribe(_ string, _ byte, handler pahomqtt.MessageHandler) pahomqtt.Token {
	c.mu.Lock()
	c.handler = handler
	c.mu.Unlock()
	return newCompletedToken()
}

// handlerSnapshot returns the last registered handler under mu — use this
// (not the bare field) from a goroutine that races with Subscribe.
func (c *mockClient) handlerSnapshot() pahomqtt.MessageHandler {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.handler
}

func (c *mockClient) SubscribeMultiple(_ map[string]byte, _ pahomqtt.MessageHandler) pahomqtt.Token {
	return newCompletedToken()
}
func (c *mockClient) Unsubscribe(_ ...string) pahomqtt.Token       { return newCompletedToken() }
func (c *mockClient) AddRoute(_ string, _ pahomqtt.MessageHandler) {}
func (c *mockClient) OptionsReader() pahomqtt.ClientOptionsReader {
	return pahomqtt.ClientOptionsReader{}
}

// runSecurityScenario registers a subscription on a fresh mock client via
// [adaptermqtt.NewSubscribeTransport] + [events.SubscribeHandle] (run in a
// goroutine, since mqtt v3's SubscribeTransport blocks until ctx is
// cancelled — see adapters/mqtt/handletransport.go's doc comment), waits for
// the handler to be registered, invokes deliver with the handler to simulate
// one or more incoming broker messages, then cancels ctx and waits for
// SubscribeHandle to return.
func runSecurityScenario(
	sensorDataSub events.Subscriber[SensorReading],
	opts adaptermqtt.SubscribeOptions,
	fn func(context.Context, SensorReading) error,
	deliver func(handler pahomqtt.MessageHandler, client pahomqtt.Client),
) {
	client := &mockClient{}
	transport := adaptermqtt.NewSubscribeTransport[SensorReading](client, 1, opts)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- events.SubscribeHandle(ctx, sensorDataSub, transport, fn)
	}()

	deadline := time.After(2 * time.Second)
	var handler pahomqtt.MessageHandler
	for handler == nil {
		handler = client.handlerSnapshot()
		if handler != nil {
			break
		}
		select {
		case <-deadline:
			cancel()
			fmt.Printf("  [error] timed out waiting for subscription registration; SubscribeHandle: %v\n", <-done)
			return
		case <-time.After(time.Millisecond):
		}
	}

	deliver(handler, client)

	cancel()
	if err := <-done; err != nil {
		fmt.Printf("  [error] SubscribeHandle returned: %v\n", err)
	}
}

// ── Channel builder ───────────────────────────────────────────────────────────

func buildChannels() (
	sensorData *events.ChannelHandle[SensorReading],
	sensorDataSub events.Subscriber[SensorReading],
	sensorAlerts *events.ChannelHandle[SensorAlert],
	b *events.Client,
) {
	b = events.NewClient(events.WithInfo(events.Info{
		Title:       "Sensor Network Events",
		Version:     "1.0.0",
		Description: "Channels for the sensor data ingestion service.",
	}))

	b.AddServer("production", events.Server{
		URL:         "mqtt.example.com",
		Protocol:    "mqtt",
		Description: "Production MQTT broker",
	})

	// Declare an API key security scheme once — referenced via
	// events.FromSecurityScheme + Subscriber/Publisher.Use on any channel
	// that needs it (there is no builder-level registry).
	// The Codec field is omitted (nil) — MQTT 3.1.1 does not carry credentials in
	// message metadata, so there is nothing to extract for codec-level validation.
	// SecurityFunc is the enforcement point for MQTT.
	apiKeyAuth := events.SecurityScheme{
		SecurityScheme: route.APIKeyScheme("X-API-Key", "header"),
	}

	var err error

	// sensor/data/{sensorId} — action: receive — secured with apiKeyAuth.
	// apiKeyAuthMW pairs with the SubscribeMW attachment below — CheckCoverage
	// (run unconditionally by Subscriber.Handle) rejects a declared security
	// scheme with no attached implementation satisfying it. The Fn here is a
	// no-op placeholder: actual enforcement happens via SubscribeOptions.SecurityFunc
	// at the adapter layer (see Scenarios 1-3 below), not in this dispatcher middleware.
	apiKeyAuthMW := events.FromSecurityScheme("apiKeyAuth", apiKeyAuth, nil)
	sensorDataSub = events.NewChannel[SensorReading]("sensor/data", sensorReadingCodec,
		events.ChannelMeta{Description: "Sensor readings received from the sensor network."},
	).WithSubscribe(events.Subscribe{
		Summary:    "Receive sensor reading",
		Tags:       []string{"sensor", "iot"},
		SchemaName: "SensorReading",
		// Security: requires apiKeyAuth. Enforced by SecurityFunc.
		Security: []route.SecurityRequirement{route.Require("apiKeyAuth")},
	}).Use(apiKeyAuthMW).
		SubscribeMW(&apiKeyAuthMW, func(_ context.Context, _ pahomqtt.Message, _ *SensorReading) (map[string][]string, error) {
			return map[string][]string{"apiKeyAuth": {}}, nil
		})
	sensorData, err = sensorDataSub.Handle(b)
	if err != nil {
		fmt.Fprintf(os.Stderr, "channel registration failed: %v\n", err)
		os.Exit(1)
	}

	// sensor/alerts — action: send — no security (outbound publish).
	sensorAlerts, err = events.NewChannel[SensorAlert]("sensor/alerts", sensorAlertCodec,
		events.ChannelMeta{Description: "Alert events produced by this service on threshold breach."},
	).WithPublish(events.Publish{
		Summary:    "Send sensor alert",
		Tags:       []string{"sensor", "alerts"},
		SchemaName: "SensorAlert",
	}).Handle(b)
	if err != nil {
		fmt.Fprintf(os.Stderr, "channel registration failed: %v\n", err)
		os.Exit(1)
	}

	return
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	slog.SetDefault(logger)

	sensorData, sensorDataSub, sensorAlerts, b := buildChannels()
	metrics := &CountingObserver{}
	obs := stats.NewFanout(metrics, stats.NewLoggingObserver(logger.With("component", "mqtt-security")))

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

	payload := []byte(`{"sensorId":"temp-01","value":42.5,"unit":"celsius"}`)

	// ── Scenario 1: valid API key ─────────────────────────────────────────────
	// events.SubscribeHandle/NewSubscribeTransport (Decision 7) blocks until
	// ctx is cancelled, so runSecurityScenario registers the subscription on a
	// mock client in a goroutine, waits for the handler to be registered, then
	// invokes it directly to simulate an incoming broker message — mirroring
	// adapters/mqtt/transport_test.go's TestNewSubscribeTransport_SubscribeHandle_RoundTrip.
	fmt.Println("--- Scenario 1: valid API key ---")
	accepted := false
	runSecurityScenario(sensorDataSub, makeHandler("sensor-key-abc123"),
		func(_ context.Context, r SensorReading) error {
			fmt.Printf("  ✓ handler called: sensorId=%s value=%.1f unit=%s\n",
				r.SensorID, r.Value, r.Unit)
			accepted = true
			return nil
		},
		func(handler pahomqtt.MessageHandler, client pahomqtt.Client) {
			handler(client, &mockMessage{topic: sensorData.Topic, payload: payload})
		},
	)
	if !accepted {
		fmt.Println("  [unexpected] handler was not called")
	}
	fmt.Println()

	// ── Scenario 2: invalid API key ───────────────────────────────────────────
	fmt.Println("--- Scenario 2: invalid API key (KindSecurity rejection) ---")
	runSecurityScenario(sensorDataSub, makeHandler("bad-key"),
		func(_ context.Context, r SensorReading) error {
			fmt.Println("  [unexpected] handler should not be called")
			return nil
		},
		func(handler pahomqtt.MessageHandler, client pahomqtt.Client) {
			handler(client, &mockMessage{topic: sensorData.Topic, payload: payload})
		},
	)
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

	// Both messages are delivered through the SAME registered subscription
	// (msgOpts never changes between them) — no need to re-register. Only
	// validMsg should ever reach fn; invalidMsg is rejected by SecurityFunc
	// (KindSecurity, logged via OnError above) before fn is called.
	runSecurityScenario(sensorDataSub, msgOpts,
		func(_ context.Context, r SensorReading) error {
			fmt.Printf("  ✓ handler called: sensorId=%s (key from msg user-property)\n", r.SensorID)
			return nil
		},
		func(handler pahomqtt.MessageHandler, client pahomqtt.Client) {
			handler(client, validMsg)
			handler(client, invalidMsg)
		},
	)
	fmt.Println()

	// ── Scenario 4: Pattern 3 — MessageFromContext inside handler fn ──────────
	// After SecurityFunc approves the message, the application handler can call
	// adaptermqtt.MessageFromContext(ctx) to access the original pahomqtt.Message.
	// This is useful for inspecting QoS, retained flag, or raw topic.
	fmt.Println("--- Scenario 4: Pattern 3 — MessageFromContext inside handler fn ---")
	runSecurityScenario(sensorDataSub, makeHandler("sensor-key-abc123"),
		func(ctx context.Context, r SensorReading) error {
			if msg, ok := adaptermqtt.MessageFromContext(ctx); ok {
				fmt.Printf("  ✓ handler: sensorId=%s | msg.QoS=%d retained=%v topic=%s\n",
					r.SensorID, msg.Qos(), msg.Retained(), msg.Topic())
			}
			return nil
		},
		func(handler pahomqtt.MessageHandler, client pahomqtt.Client) {
			handler(client, &mockMessage{topic: sensorData.Topic, payload: payload})
		},
	)
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
	metrics.Print()
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
