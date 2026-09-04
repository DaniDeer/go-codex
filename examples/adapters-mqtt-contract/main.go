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

// waitForSubscription polls until filter has been registered by
// [mockClient.Subscribe], or timeout elapses. events.SubscribeHandle (Decision
// 7) registers the subscription then blocks until ctx is cancelled — so
// callers run it in a goroutine and must synchronize before delivering
// simulated broker messages.
func (c *mockClient) waitForSubscription(filter string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		_, ok := c.handlers[filter]
		c.mu.Unlock()
		if ok {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return false
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

	// Decision 7: events.SubscribeHandle registers the subscription then
	// blocks until ctx is cancelled (mqtt v3 has no router — paho dispatches
	// on its own goroutines once client.Subscribe registers the filter), so
	// run it in a goroutine. subCtx is shared by every subscribe demo below
	// and cancelled once when main returns.
	subCtx, subCancel := context.WithCancel(ctx)
	defer subCancel()

	// ── Consumer service: register channels ───────────────────────────────────
	//
	// The consumer imports the shared contract and registers both channels with
	// its own events.Client. The builder accumulates route information for
	// AsyncAPI spec generation — it has no runtime effect on the handler.

	consumerBuilder := events.NewClient(events.WithInfo(events.Info{
		Title:       "Alert Service",
		Version:     "1.0.0",
		Description: "Subscribes to sensor readings; publishes alerts on threshold breach.",
	}))
	consumerBuilder.AddServer("broker", events.Server{
		URL:         "mqtt://broker.example.com:1883",
		Protocol:    "mqtt",
		Description: "Production MQTT broker",
	})

	// Handle ReadingsSubscriber for subscribe — returns a *ChannelHandle with
	// Decode, Encode, BuildTopic, ValidateTopicVars, and all codec helpers.
	readingsHandle, err := contract.ReadingsSubscriber.Handle(consumerBuilder)
	if err != nil {
		fmt.Fprintln(os.Stderr, "consumer: register readings channel:", err)
		os.Exit(1)
	}

	// Handle AlertsPublisher for publish — consumer publishes alerts here.
	// The runtime call site below uses contract.AlertsPublisher directly
	// (events.PublishHandle builds its own handle internally); only spec
	// registration needs the returned handle, which is unused here.
	_, err = contract.AlertsPublisher.Handle(consumerBuilder)
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
	// {sensorID} is the MQTT routing key (UUID); SensorReading.SensorID is the
	// application-level identifier (may differ). Decision 7's NewTopicParam
	// merge field on contract.ReadingsChannel populates r.RoutingID with the
	// validated routing key automatically — no manual TopicVarsFromMessage call
	// needed.
	//
	// TopicFilter pins this subscription to sensor A's specific stream only —
	// contrast with §4 below, which subscribes to ALL sensors via "sensors/#".
	alertTransport := adaptermqtt.NewPublishTransport[contract.Alert](client, 1, false, adaptermqtt.PublishOptions[contract.Alert]{}) // observer from ctx
	readingsTransport := adaptermqtt.NewSubscribeTransport[contract.SensorReading](client, 1,
		adaptermqtt.SubscribeOptions{
			TopicFilter: readingsTopic,
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
	)
	go func() {
		err := events.SubscribeHandle(subCtx, contract.ReadingsSubscriber, readingsTransport,
			func(ctx context.Context, r contract.SensorReading) error {
				routingSensorID := r.RoutingID // populated automatically by the merge field

				if !shouldAlert(r) {
					fmt.Printf("  [consumer] reading OK  sensor=%.8s… value=%.1f %s\n",
						routingSensorID, r.Value, r.Unit)
					return nil
				}
				alert := buildAlert(r)
				alert.RoutingID = routingSensorID // routing key, not payload field
				fmt.Printf("  [consumer] ALERT       sensor=%.8s… value=%.1f > threshold=%.1f\n",
					routingSensorID, r.Value, alertThreshold)
				return events.PublishHandle(ctx, contract.AlertsPublisher, alertTransport, alert)
			},
		)
		if err != nil {
			logger.Error("consumer: subscribe failed", "error", err)
		}
	}()
	client.waitForSubscription(readingsTopic, time.Second)

	// ── Producer service: register channels ───────────────────────────────────
	//
	// The producer imports the same shared contract and registers ReadingsChannel
	// with its own builder. Both services get identical ChannelHandles from the
	// same spec. If the contract changes, both fail to compile — no silent drift.

	producerBuilder := events.NewClient(events.WithInfo(events.Info{
		Title:       "Sensor Gateway",
		Version:     "1.0.0",
		Description: "Forwards raw sensor measurements to the MQTT broker.",
	}))
	producerBuilder.AddServer("broker", events.Server{
		URL:      "mqtt://broker.example.com:1883",
		Protocol: "mqtt",
	})

	// The runtime call sites below use contract.ReadingsPublisher directly
	// (events.PublishHandle builds its own handle internally); only spec
	// registration needs the returned handle, which is unused here.
	_, err = contract.ReadingsPublisher.Handle(producerBuilder)
	if err != nil {
		fmt.Fprintln(os.Stderr, "producer: register readings channel:", err)
		os.Exit(1)
	}
	producerTransport := adaptermqtt.NewPublishTransport[contract.SensorReading](client, 1, false, adaptermqtt.PublishOptions[contract.SensorReading]{}) // observer from ctx

	// ── 1. Normal readings ────────────────────────────────────────────────────
	fmt.Println("=== 1. Normal readings (producer → broker → consumer) ===")

	// RoutingID carries the {sensorID} MQTT topic routing key — Decision 7's
	// NewTopicParam merge field on contract.ReadingsChannel derives the
	// concrete topic from it automatically, no separate vars map needed.
	readings := []contract.SensorReading{
		{SensorID: "temp-01", Value: 62.5, Unit: "celsius", Timestamp: "2024-01-15T10:30:00Z", RoutingID: sensorA},
		{SensorID: "temp-01", Value: 87.3, Unit: "celsius", Timestamp: "2024-01-15T10:31:00Z", RoutingID: sensorA}, // exceeds threshold
		{SensorID: "temp-01", Value: 55.0, Unit: "celsius", Timestamp: "2024-01-15T10:32:00Z", RoutingID: sensorA},
	}
	for _, r := range readings {
		if err := events.PublishHandle(ctx, contract.ReadingsPublisher, producerTransport, r); err != nil {
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
		RoutingID: sensorB, // valid — isolates the payload failure path below
	}
	err = events.PublishHandle(ctx, contract.ReadingsPublisher, producerTransport, badReading)
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

	// ── 3. Invalid topic param — EncodeVars rejects non-UUID sensorID ────────
	fmt.Println("=== 3. Invalid topic param (rejected before broker call) ===")

	// Decision 7 derives the topic var straight from SensorReading.RoutingID
	// (the NewTopicParam merge field). "not-a-uuid" fails the UUID codec at
	// the EncodeVars step, returning codex.ValidationErrors before Encode or
	// broker I/O is attempted (TopicParamError is only reachable via a
	// directly-supplied vars map, e.g. a manual BuildTopic call).
	err = events.PublishHandle(ctx, contract.ReadingsPublisher, producerTransport,
		contract.SensorReading{
			SensorID: "temp-01", Value: 70.0, Unit: "celsius",
			Timestamp: "2024-01-15T10:34:00Z",
			RoutingID: "not-a-uuid",
		},
	)
	if err != nil {
		var valErrs codex.ValidationErrors
		if errors.As(err, &valErrs) {
			logger.Warn("producer: topic var rejected (no broker call)",
				"errors", valErrs,
			)
		} else {
			logger.Error("producer: publish error", "error", err)
		}
	}
	fmt.Println()

	// ── 4. Multiple sensors via wildcard subscription ─────────────────────────
	fmt.Println("=== 4. Multiple sensors via wildcard subscription (sensors/#) ===")

	// Handle a second ReadingsSubscriber registration for a separate builder
	// context — purely for spec-registration purposes; the runtime call
	// below uses contract.ReadingsSubscriber directly.
	_, err = contract.ReadingsSubscriber.Handle(
		events.NewClient(events.WithInfo(events.Info{Title: "Wildcard Monitor", Version: "1.0.0"})),
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, "wildcard: register:", err)
		os.Exit(1)
	}

	// The NewTopicParam merge field populates r.RoutingID automatically on
	// every decoded message — no manual TopicVarsFromMessage call needed.
	wildcardTransport := adaptermqtt.NewSubscribeTransport[contract.SensorReading](client, 1,
		adaptermqtt.SubscribeOptions{
			TopicFilter: "sensors/#",
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
	)
	go func() {
		err := events.SubscribeHandle(subCtx, contract.ReadingsSubscriber, wildcardTransport,
			func(_ context.Context, r contract.SensorReading) error {
				fmt.Printf("  [wildcard] sensor=%-42s value=%.1f %s\n",
					r.RoutingID, r.Value, r.Unit)
				return nil
			},
		)
		if err != nil {
			logger.Error("wildcard: subscribe failed", "error", err)
		}
	}()
	client.waitForSubscription("sensors/#", time.Second)

	// Publish from two different sensors — both arrive at the wildcard handler.
	for _, sID := range []string{sensorA, sensorB} {
		_ = events.PublishHandle(ctx, contract.ReadingsPublisher, producerTransport,
			contract.SensorReading{
				SensorID: "temp-01", Value: 65.0 + float64(len(sID)%10),
				Unit: "celsius", Timestamp: "2024-01-15T11:00:00Z",
				RoutingID: sID,
			})
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
