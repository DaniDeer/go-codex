// Package adapters-mqtt-contract demonstrates the "codec-as-contract" pattern
// for MQTT event-driven services.
//
// A shared [contract] sub-package defines types, codecs, and channel specs once.
// Both the producer service and the consumer service import it. The Go compiler
// enforces the contract: any field rename, type change, or constraint modification
// breaks compilation on both sides immediately — no stale AsyncAPI YAML, no schema
// drift, no code-generation step.
//
// # Workflow
//
//	Channel spec (contract/) ──→ .Register(producerBuilder) ──→ ChannelHandle ──→ Publish (producer)
//	                         └──→ .Register(consumerBuilder) ──→ ChannelHandle ──→ SubscribeHandler (consumer)
//
// Same [events.Channel] value is the single source of truth. Codecs, topic params,
// and AsyncAPI operations flow automatically into both runtime behaviour and spec.
//
// # Comparison with gob-contract
//
// [examples/gob-contract] shows the same pattern with binary gob encoding —
// efficient for internal Go-to-Go communication but opaque to tooling.
// This example uses JSON:
//   - The AsyncAPI 3.0 spec is fully machine-readable (Swagger UI, event catalog, etc.)
//   - Cross-language brokers (Node.js consumers, Python producers) can participate
//   - Codec constraints still enforced at both ends at runtime
//
// Run with: go run ./examples/adapters-mqtt-contract
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
	"github.com/DaniDeer/go-codex/examples/adapters-mqtt-contract/contract"
	"github.com/DaniDeer/go-codex/stats"
)

// ── Observer ──────────────────────────────────────────────────────────────────

// CountingObserver is an in-memory implementation of [stats.Observer].
// In production replace with Prometheus / OpenTelemetry — the interface is identical.
type CountingObserver struct {
	mu             sync.Mutex
	subscribes     int
	subSuccess     int
	publishes      int
	pubSuccess     int
	valErrorsByLoc map[string]int
	subLatencies   []time.Duration
	pubLatencies   []time.Duration
}

func (o *CountingObserver) RecordRequest(_ string, _ string, _ int, _ time.Duration) {}

func (o *CountingObserver) RecordSubscribe(_ string, success bool, d time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.subscribes++
	if success {
		o.subSuccess++
	}
	o.subLatencies = append(o.subLatencies, d)
}

func (o *CountingObserver) RecordPublish(_ string, success bool, d time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.publishes++
	if success {
		o.pubSuccess++
	}
	o.pubLatencies = append(o.pubLatencies, d)
}

func (o *CountingObserver) RecordValidationError(location, constraintName, field string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.valErrorsByLoc == nil {
		o.valErrorsByLoc = make(map[string]int)
	}
	o.valErrorsByLoc[location]++
	// No logging — use stats.NewLoggingObserver via stats.NewFanout for structured logging.
}

func (o *CountingObserver) Print() {
	o.mu.Lock()
	defer o.mu.Unlock()
	fmt.Printf("  subscribes     : %d total, %d success, %d failed\n",
		o.subscribes, o.subSuccess, o.subscribes-o.subSuccess)
	fmt.Printf("  publishes      : %d total, %d success, %d failed\n",
		o.publishes, o.pubSuccess, o.publishes-o.pubSuccess)
	for loc, n := range o.valErrorsByLoc {
		fmt.Printf("  val errs %-10s: %d\n", "("+loc+")", n)
	}
	if len(o.subLatencies) > 0 {
		var sum time.Duration
		for _, l := range o.subLatencies {
			sum += l
		}
		fmt.Printf("  avg sub latency: %v\n", sum/time.Duration(len(o.subLatencies)))
	}
	if len(o.pubLatencies) > 0 {
		var sum time.Duration
		for _, l := range o.pubLatencies {
			sum += l
		}
		fmt.Printf("  avg pub latency: %v\n", sum/time.Duration(len(o.pubLatencies)))
	}
}

var _ stats.Observer = (*CountingObserver)(nil)

// ── Mock MQTT broker ──────────────────────────────────────────────────────────
//
// Replace with a real Paho client connected to Mosquitto / EMQX / HiveMQ.
// The SubscribeHandler and Publish calls are identical — only the client changes.

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
	fmt.Printf("[broker] published to %-40s : %s\n", topic, payload.([]byte))
	// Fan-out: deliver to any handler whose subscription matches this topic.
	c.mu.Lock()
	var matched []pahomqtt.MessageHandler
	for sub, h := range c.handlers {
		if topicMatchesSub(sub, topic) {
			matched = append(matched, h)
		}
	}
	c.mu.Unlock()
	for _, h := range matched {
		h(c, &mockMessage{topic: topic, payload: payload.([]byte)})
	}
	return newMockToken()
}

func topicMatchesSub(sub, topic string) bool {
	subParts := strings.Split(sub, "/")
	topicParts := strings.Split(topic, "/")
	for i, seg := range subParts {
		if seg == "#" {
			return true
		}
		if i >= len(topicParts) {
			return false
		}
		if seg != "+" && seg != topicParts[i] {
			return false
		}
	}
	return len(subParts) == len(topicParts)
}

func (c *mockClient) IsConnected() bool      { return true }
func (c *mockClient) IsConnectionOpen() bool { return true }
func (c *mockClient) Connect() pahomqtt.Token {
	return newMockToken()
}
func (c *mockClient) Disconnect(_ uint) {}
func (c *mockClient) SubscribeMultiple(_ map[string]byte, _ pahomqtt.MessageHandler) pahomqtt.Token {
	return newMockToken()
}
func (c *mockClient) Unsubscribe(_ ...string) pahomqtt.Token       { return newMockToken() }
func (c *mockClient) AddRoute(_ string, _ pahomqtt.MessageHandler) {}
func (c *mockClient) OptionsReader() pahomqtt.ClientOptionsReader {
	return pahomqtt.ClientOptionsReader{}
}

// ── Domain (Layer 2) ──────────────────────────────────────────────────────────

const alertThreshold = 75.0

// shouldAlert returns true when a reading value exceeds the configured threshold.
func shouldAlert(r contract.SensorReading) bool {
	return r.Value > alertThreshold
}

// buildAlert creates an Alert from a threshold-breaching SensorReading.
func buildAlert(r contract.SensorReading) contract.Alert {
	return contract.Alert{
		SensorID:  r.SensorID,
		Value:     r.Value,
		Unit:      r.Unit,
		Threshold: alertThreshold,
		Timestamp: r.Timestamp,
	}
}

// ── Demo sensor IDs ───────────────────────────────────────────────────────────

const (
	sensorA = "f47ac10b-58cc-4372-a567-0e02b2c3d479"
	sensorB = "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
)

func main() {
	ctx := context.Background()

	// logger is the structured logger for transport-level events.
	// In production attach trace IDs or tenant context via logger.With(...).
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	slog.SetDefault(logger)
	logger = logger.With("transport", "mqtt")

	metrics := &CountingObserver{}
	obs := stats.NewFanout(
		metrics,
		stats.NewLoggingObserver(logger),
	)
	// Store obs in ctx once — Subscribe/Publish resolve it automatically.
	ctx = stats.WithObserver(ctx, obs)
	client := newMockClient()

	// ── Consumer service: register channels ───────────────────────────────────
	//
	// The consumer imports the shared contract and registers both channels with
	// its own events.Builder. The builder accumulates route information for
	// AsyncAPI spec generation — it has no runtime effect on the handler.

	consumerBuilder := events.NewBuilder(events.Info{
		Title:       "Alert Service",
		Version:     "1.0.0",
		Description: "Subscribes to sensor readings; publishes alerts on threshold breach.",
	})
	consumerBuilder.AddServer("broker", events.Server{
		URL:         "mqtt://broker.example.com:1883",
		Protocol:    "mqtt",
		Description: "Production MQTT broker",
	})

	// Register ReadingsChannel for subscribe — returns a *ChannelHandle with
	// Decode, Encode, BuildTopic, ValidateTopicVars, and all codec helpers.
	readingsHandle, err := contract.ReadingsChannel.Register(consumerBuilder)
	if err != nil {
		fmt.Fprintln(os.Stderr, "consumer: register readings channel:", err)
		os.Exit(1)
	}

	// Register AlertsChannel for publish — consumer publishes alerts here.
	alertsHandle, err := contract.AlertsChannel.Register(consumerBuilder)
	if err != nil {
		fmt.Fprintln(os.Stderr, "consumer: register alerts channel:", err)
		os.Exit(1)
	}

	// Build the concrete subscribe topic for sensor A.
	// BuildTopic validates {sensorID} against SensorIDCodec (UUID) before returning.
	readingsTopic, err := readingsHandle.BuildTopic(map[string]string{"sensorID": sensorA})
	if err != nil {
		fmt.Fprintln(os.Stderr, "consumer: BuildTopic:", err)
		os.Exit(1)
	}

	// Wire the consumer handler: decode SensorReading → domain logic → publish Alert.
	//
	// Note: the {sensorID} topic variable is the MQTT routing key (UUID); the
	// SensorReading.SensorID payload field is the application-level identifier
	// (may differ). Use TopicVarsFromMessage to extract the validated routing key
	// from the incoming topic — do not assume the payload field is a UUID.
	client.Subscribe(readingsTopic, 1,
		adaptermqtt.SubscribeHandler(ctx, readingsHandle,
			func(ctx context.Context, r contract.SensorReading) error {
				msg, _ := adaptermqtt.MessageFromContext(ctx)
				vars, err := adaptermqtt.TopicVarsFromMessage(readingsHandle, msg)
				if err != nil {
					return err
				}
				routingSensorID := vars["sensorID"] // validated UUID by TopicParam.Codec

				if !shouldAlert(r) {
					fmt.Printf("  [consumer] reading OK  sensor=%.8s… value=%.1f %s\n",
						routingSensorID, r.Value, r.Unit)
					return nil
				}
				alert := buildAlert(r)
				fmt.Printf("  [consumer] ALERT       sensor=%.8s… value=%.1f > threshold=%.1f\n",
					routingSensorID, r.Value, alertThreshold)
				return adaptermqtt.Publish(ctx, client, alertsHandle, 1, false, alert,
					map[string]string{"sensorID": routingSensorID}, // routing key, not payload field
					adaptermqtt.PublishOptions{})                   // observer from ctx
			},
			adaptermqtt.SubscribeOptions{
				// observer from ctx (resolved automatically)
				OnError: func(e adaptermqtt.SubscribeError) {
					switch e.Kind {
					case adaptermqtt.KindDecode:
						var validationErrs codex.ValidationErrors
						if errors.As(e.Err, &validationErrs) {
							logger.Warn("consumer: decode validation error",
								"topic", e.Topic,
								"errors", validationErrs,
							)
						} else {
							logger.Warn("consumer: decode error",
								"topic", e.Topic,
								"error", e.Err,
							)
						}
					case adaptermqtt.KindHandler:
						logger.Error("consumer: handler error",
							"topic", e.Topic,
							"error", e.Err,
						)
					}
				},
			},
		),
	)

	// ── Producer service: register channels ───────────────────────────────────
	//
	// The producer imports the same shared contract and registers ReadingsChannel
	// with its own builder. Both services get identical ChannelHandles from the
	// same spec. If the contract changes, both fail to compile — no silent drift.

	producerBuilder := events.NewBuilder(events.Info{
		Title:       "Sensor Gateway",
		Version:     "1.0.0",
		Description: "Forwards raw sensor measurements to the MQTT broker.",
	})
	producerBuilder.AddServer("broker", events.Server{
		URL:      "mqtt://broker.example.com:1883",
		Protocol: "mqtt",
	})

	producerReadingsHandle, err := contract.ReadingsChannel.Register(producerBuilder)
	if err != nil {
		fmt.Fprintln(os.Stderr, "producer: register readings channel:", err)
		os.Exit(1)
	}

	// ── 1. Normal readings ────────────────────────────────────────────────────
	fmt.Println("=== 1. Normal readings (producer → broker → consumer) ===")

	readings := []contract.SensorReading{
		{SensorID: "temp-01", Value: 62.5, Unit: "celsius", Timestamp: "2024-01-15T10:30:00Z"},
		{SensorID: "temp-01", Value: 87.3, Unit: "celsius", Timestamp: "2024-01-15T10:31:00Z"}, // exceeds threshold
		{SensorID: "temp-01", Value: 55.0, Unit: "celsius", Timestamp: "2024-01-15T10:32:00Z"},
	}
	for _, r := range readings {
		if err := adaptermqtt.Publish(ctx, client, producerReadingsHandle, 1, false, r,
			map[string]string{"sensorID": sensorA},
			adaptermqtt.PublishOptions{}); /* observer from ctx */ err != nil {
			logger.Error("producer: publish failed", "error", err)
		}
	}
	fmt.Println()

	// ── 2. Invalid payload — codec rejects before broker call ─────────────────
	fmt.Println("=== 2. Invalid payload (codec rejects before broker call) ===")

	badReading := contract.SensorReading{
		SensorID:  "", // fails NonEmptyString
		Value:     0,  // fails NonZeroFloat
		Unit:      "celsius",
		Timestamp: "2024-01-15T10:33:00Z",
	}
	err = adaptermqtt.Publish(ctx, client, producerReadingsHandle, 1, false, badReading,
		map[string]string{"sensorID": sensorB},
		adaptermqtt.PublishOptions{}) // observer from ctx
	if err != nil {
		var valErrs codex.ValidationErrors
		if errors.As(err, &valErrs) {
			logger.Warn("producer: payload validation failed",
				"errors", valErrs,
			)
		} else {
			logger.Error("producer: publish error", "error", err)
		}
	}
	fmt.Println()

	// ── 3. Invalid topic param — BuildTopic rejects non-UUID sensorID ─────────
	fmt.Println("=== 3. Invalid topic param (BuildTopic rejects before broker call) ===")

	err = adaptermqtt.Publish(ctx, client, producerReadingsHandle, 1, false,
		contract.SensorReading{
			SensorID: "temp-01", Value: 70.0, Unit: "celsius",
			Timestamp: "2024-01-15T10:34:00Z",
		},
		map[string]string{"sensorID": "not-a-uuid"}, // fails UUID codec
		adaptermqtt.PublishOptions{},                // observer from ctx
	)
	if err != nil {
		var paramErr events.TopicParamError
		if errors.As(err, &paramErr) {
			logger.Warn("producer: topic param rejected (no broker call)",
				"param", paramErr.Name,
				"value", paramErr.Value,
				"cause", paramErr.Err,
			)
		} else {
			logger.Error("producer: publish error", "error", err)
		}
	}
	fmt.Println()

	// ── 4. Multiple sensors via wildcard subscription ─────────────────────────
	fmt.Println("=== 4. Multiple sensors via wildcard subscription (sensors/#) ===")

	// Register a second ReadingsChannel handle for wildcard subscription.
	// TopicVarsFromMessage extracts and validates {sensorID} from each incoming topic.
	wildcardHandle, err := contract.ReadingsChannel.Register(
		events.NewBuilder(events.Info{Title: "Wildcard Monitor", Version: "1.0.0"}),
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, "wildcard: register:", err)
		os.Exit(1)
	}

	client.Subscribe("sensors/#", 1,
		adaptermqtt.SubscribeHandler(ctx, wildcardHandle,
			func(ctx context.Context, r contract.SensorReading) error {
				msg, _ := adaptermqtt.MessageFromContext(ctx)
				vars, err := adaptermqtt.TopicVarsFromMessage(wildcardHandle, msg)
				if err != nil {
					return err
				}
				fmt.Printf("  [wildcard] sensor=%-42s value=%.1f %s\n",
					vars["sensorID"], r.Value, r.Unit)
				return nil
			},
			adaptermqtt.SubscribeOptions{
				// observer from ctx (resolved automatically)
				OnError: func(e adaptermqtt.SubscribeError) {
					var paramErr events.TopicParamError
					if errors.As(e.Err, &paramErr) {
						logger.Warn("wildcard: topic param rejected",
							"var", paramErr.Name,
							"value", paramErr.Value,
							"cause", paramErr.Err,
						)
					} else {
						logger.Warn("wildcard: error", "topic", e.Topic, "error", e.Err)
					}
				},
			},
		),
	)

	// Publish from two different sensors — both arrive at the wildcard handler.
	for _, sID := range []string{sensorA, sensorB} {
		_ = adaptermqtt.Publish(ctx, client, producerReadingsHandle, 1, false,
			contract.SensorReading{
				SensorID: "temp-01", Value: 65.0 + float64(len(sID)%10),
				Unit: "celsius", Timestamp: "2024-01-15T11:00:00Z",
			},
			map[string]string{"sensorID": sID},
			adaptermqtt.PublishOptions{}) // observer from ctx
	}
	// Non-UUID sensorID — wildcard handler receives it, TopicVarsFromMessage rejects.
	if len("sensors/not-a-uuid/readings") > 0 {
		client.Publish("sensors/not-a-uuid/readings", 0, false, //nolint:errcheck
			[]byte(`{"sensor_id":"temp-01","value":60.0,"unit":"celsius","timestamp":"2024-01-15T11:01:00Z"}`))
	}
	fmt.Println()

	// ── 5. AsyncAPI spec (from consumer builder) ───────────────────────────────
	//
	// The consumer builder accumulated Subscribe + Publish operations from both
	// registered channels. The spec is generated from the same codec definitions
	// that drive runtime decode/encode — one source of truth.
	fmt.Println("=== 5. AsyncAPI 3.0 spec (derived from shared contract) ===")

	doc, err := consumerBuilder.AsyncAPISpec()
	if err != nil {
		fmt.Fprintln(os.Stderr, "AsyncAPISpec error:", err)
		os.Exit(1)
	}
	yamlBytes, err := doc.MarshalYAML()
	if err != nil {
		fmt.Fprintln(os.Stderr, "MarshalYAML error:", err)
		os.Exit(1)
	}
	fmt.Print(string(yamlBytes))

	// ── 6. Observer summary ───────────────────────────────────────────────────
	fmt.Println("=== Observer summary ===")
	metrics.Print()
}
