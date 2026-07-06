// Package adapters-mqtt5 demonstrates the MQTT 5.0 adapter and its features
// that are specific to MQTT 5.0, compared to the MQTT 3.1.1 adapter:
//
//   - User Properties: per-message key-value metadata sent with each PUBLISH
//   - UserPropertyParam: codec validation on User Properties (mirrors rest.HeaderParam)
//   - ContentType: messages carry their own format identifier for auto-selection
//   - Request-Reply: typed RPC over MQTT using ResponseTopic + CorrelationData
//
// The example uses an in-process mock broker to avoid requiring a real MQTT 5.0
// broker. In production, connect to Mosquitto ≥ 2.0 (MQTT 5.0 enabled by default):
//
//	conn, _ := net.Dial("tcp", "localhost:1883")
//	router := paho.NewStandardRouter()
//	client := paho.NewClient(paho.ClientConfig{Conn: conn, Router: router})
//	client.Connect(ctx, &paho.Connect{ClientID: "my-service", CleanStart: true})
//
// # Layer structure
//
// Layer 1: Codec[SensorReading] + Codec[ComputeReq] + Codec[ComputeResp]
// Layer 2: events.NewChannel  (PUB/SUB → AsyncAPI with protocol: mqtt5)
//
//	rest.NewRoute       (REQ/REP → AsyncAPI request-reply via reqreply.Builder)
//
// Layer 3: mqtt5adapter.Subscribe / Publish / ServeRequestReply / Request
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	mqtt5adapter "github.com/DaniDeer/go-codex/adapters/mqtt5"
	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/stats"
	"github.com/DaniDeer/go-codex/validate"
	pahomqtt5 "github.com/eclipse/paho.golang/paho"
)

// ── Layer 1: domain types and codecs ─────────────────────────────────────────

type SensorReading struct {
	SensorID string
	Value    float64
}

var sensorCodec = codex.Struct[SensorReading](
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

type ComputeReq struct{ X, Y int }
type ComputeResp struct{ Sum int }

var computeReqCodec = codex.Struct[ComputeReq](
	codex.RequiredField("x", codex.Int(),
		func(r ComputeReq) int { return r.X },
		func(r *ComputeReq, v int) { r.X = v },
	),
	codex.RequiredField("y", codex.Int(),
		func(r ComputeReq) int { return r.Y },
		func(r *ComputeReq, v int) { r.Y = v },
	),
)

var computeRespCodec = codex.Struct[ComputeResp](
	codex.RequiredField("sum", codex.Int(),
		func(r ComputeResp) int { return r.Sum },
		func(r *ComputeResp, v int) { r.Sum = v },
	),
)

// ── Layer 2: channel and route declarations ───────────────────────────────────

var ReadingsChannel = events.NewChannel[SensorReading](
	"sensors/{sensorID}/readings",
	sensorCodec,
	events.Subscribe{
		OperationID: "receiveSensorReading",
		Summary:     "Receive a sensor reading.",
	},
	events.Publish{
		OperationID: "publishSensorReading",
		Summary:     "Publish a sensor reading.",
	},
	events.TopicParam{
		Name:        "sensorID",
		Description: "UUID of the originating sensor.",
	}.WithCodec(codex.String().Refine(validate.UUID)),
)

var ComputeRoute = reqreply.NewRoute[ComputeReq, ComputeResp](
	"compute/add",
	computeReqCodec, computeRespCodec,
	reqreply.RouteMeta{OperationID: "computeAdd", Summary: "Add two integers via MQTT 5 request-reply."},
)

// ── in-process mock broker ────────────────────────────────────────────────────
//
// The mock broker simulates an MQTT 5.0 broker: published messages are
// routed to handlers registered on the mockRouter. This pattern is
// identical to how the MQTT 3 example uses a mockClient.
//
// mockRouter implements [mqtt5adapter.MQTTRouter] and dispatches published
// messages to topic-matched handlers. It supports wildcard prefix matching
// for reply topics (e.g. "replies/#" matches "replies/uuid").
type mockRouter struct {
	mu       sync.RWMutex
	handlers map[string]pahomqtt5.MessageHandler
}

func newMockRouter() *mockRouter {
	return &mockRouter{handlers: make(map[string]pahomqtt5.MessageHandler)}
}

func (r *mockRouter) RegisterHandler(topic string, h pahomqtt5.MessageHandler) {
	r.mu.Lock()
	r.handlers[topic] = h
	r.mu.Unlock()
}

func (r *mockRouter) UnregisterHandler(topic string) {
	r.mu.Lock()
	delete(r.handlers, topic)
	r.mu.Unlock()
}

// dispatch finds all registered handlers whose topic matches msg.Topic and
// calls them. Supports exact match and prefix match (handlers registered for
// "replies/<uuid>" match messages published to "replies/<uuid>").
func (r *mockRouter) dispatch(msg *pahomqtt5.Publish) {
	r.mu.RLock()
	var matched []pahomqtt5.MessageHandler
	for topic, h := range r.handlers {
		if topic == msg.Topic || topicMatches(topic, msg.Topic) {
			matched = append(matched, h)
		}
	}
	r.mu.RUnlock()
	for _, h := range matched {
		h(msg)
	}
}

// topicMatches checks if a registered pattern (MQTT wildcard or template topic)
// matches a concrete topic string.
// Supports:
//   - MQTT single-level wildcard: "+" matches one segment
//   - MQTT multi-level wildcard: "#" matches remaining segments
//   - Template placeholders: "{varName}" treated as "+" (one segment)
func topicMatches(pattern, topic string) bool {
	if pattern == topic {
		return true
	}
	// Normalise template placeholders to MQTT + wildcards for matching.
	norm := normaliseTopic(pattern)
	return mqttTopicMatch(norm, topic)
}

// normaliseTopic replaces "{varName}" placeholders with the "+" MQTT wildcard.
func normaliseTopic(pattern string) string {
	result := make([]byte, 0, len(pattern))
	i := 0
	for i < len(pattern) {
		if pattern[i] == '{' {
			j := i + 1
			for j < len(pattern) && pattern[j] != '}' {
				j++
			}
			result = append(result, '+')
			i = j + 1
		} else {
			result = append(result, pattern[i])
			i++
		}
	}
	return string(result)
}

// mqttTopicMatch implements MQTT wildcard matching for + and #.
func mqttTopicMatch(pattern, topic string) bool {
	pParts := splitTopic(pattern)
	tParts := splitTopic(topic)
	return matchParts(pParts, tParts)
}

func splitTopic(topic string) []string {
	var parts []string
	start := 0
	for i := 0; i <= len(topic); i++ {
		if i == len(topic) || topic[i] == '/' {
			parts = append(parts, topic[start:i])
			start = i + 1
		}
	}
	return parts
}

func matchParts(pattern, topic []string) bool {
	if len(pattern) == 0 {
		return len(topic) == 0
	}
	if pattern[0] == "#" {
		return true
	}
	if len(topic) == 0 {
		return false
	}
	if pattern[0] == "+" || pattern[0] == topic[0] {
		return matchParts(pattern[1:], topic[1:])
	}
	return false
}

// mockBroker implements [mqtt5adapter.MQTTClient] and routes published
// messages to the shared mockRouter, simulating a broker round-trip.
type mockBroker struct {
	mu        sync.Mutex
	router    *mockRouter
	published []*pahomqtt5.Publish
}

func newMockBroker() (*mockBroker, *mockRouter) {
	router := newMockRouter()
	return &mockBroker{router: router}, router
}

func (b *mockBroker) Publish(_ context.Context, p *pahomqtt5.Publish) (*pahomqtt5.PublishResponse, error) {
	b.mu.Lock()
	b.published = append(b.published, p)
	b.mu.Unlock()
	// Simulate broker routing: dispatch to all matching handlers.
	go b.router.dispatch(p)
	return &pahomqtt5.PublishResponse{}, nil
}

func (b *mockBroker) Subscribe(_ context.Context, _ *pahomqtt5.Subscribe) (*pahomqtt5.Suback, error) {
	return &pahomqtt5.Suback{}, nil
}

func (b *mockBroker) Unsubscribe(_ context.Context, _ *pahomqtt5.Unsubscribe) (*pahomqtt5.Unsuback, error) {
	return &pahomqtt5.Unsuback{}, nil
}

// ── observer ──────────────────────────────────────────────────────────────────

// eventCounter tracks publish and subscribe counts for demonstration purposes.
// Logging is handled separately via stats.NewLoggingObserver — keeping metrics
// and logging concerns distinct per the stats.NewFanout pattern.
type eventCounter struct {
	mu         sync.Mutex
	subscribes int
	publishes  int
	requests   int
}

func (c *eventCounter) RecordSubscribe(_ string, _ bool, _ time.Duration) {
	c.mu.Lock()
	c.subscribes++
	c.mu.Unlock()
}
func (c *eventCounter) RecordPublish(_ string, _ bool, _ time.Duration) {
	c.mu.Lock()
	c.publishes++
	c.mu.Unlock()
}
func (c *eventCounter) RecordRequest(_, _ string, _ int, _ time.Duration) {
	c.mu.Lock()
	c.requests++
	c.mu.Unlock()
}
func (c *eventCounter) RecordValidationError(_, _, _ string) {}

// ── main ──────────────────────────────────────────────────────────────────────

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	counter := &eventCounter{}
	// stats.NewFanout separates metrics (counter) from logging (NewLoggingObserver)
	// — neither observer mixes both concerns.
	obs := stats.NewFanout(counter, stats.NewLoggingObserver(logger))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	fmt.Println("═══════════════════════════════════════════════════════")
	fmt.Println(" MQTT 5 adapter — features specific to MQTT 5.0")
	fmt.Println("═══════════════════════════════════════════════════════")

	runPubSubDemo(ctx, obs, logger)
	runRequestReplyDemo(ctx, obs, logger)
	printSpecs(logger)
}

// ── Demo 1: PUB/SUB with User Properties, ContentType, UserPropertyParam ─────

func runPubSubDemo(ctx context.Context, obs stats.Observer, logger *slog.Logger) {
	fmt.Println("\n── Demo 1: PUB/SUB (User Properties + ContentType + UserPropertyParam) ──")

	broker, router := newMockBroker()
	sensorID := "f47ac10b-58cc-4372-a567-0e02b2c3d479"

	evtBuilder := events.NewBuilder(events.Info{Title: "Sensor Network", Version: "1.0.0"})
	evtBuilder.AddServer("mqtt5", events.Server{
		URL:         "mqtt://broker:1883",
		Protocol:    "mqtt5",
		Description: "MQTT 5.0 broker for sensor data",
	})
	subHandle, _ := ReadingsChannel.Register(evtBuilder)

	// ── MQTT 5 feature 1: UserPropertyParam ─────────────────────────────────
	// Validate the TenantID User Property on every incoming message.
	// This mirrors rest.HeaderParam for HTTP — no equivalent in MQTT 3.1.1.
	if err := mqtt5adapter.Subscribe(ctx, broker, router, subHandle, 1,
		func(ctx context.Context, r SensorReading) error {
			// ── MQTT 5 feature 2: UserPropertiesFromContext ─────────────────
			// Access the full User Properties bag inside the handler.
			if props, ok := mqtt5adapter.UserPropertiesFromContext(ctx); ok {
				tenantID := ""
				for _, p := range props {
					if p.Key == "TenantID" {
						tenantID = p.Value
					}
				}
				fmt.Printf("  received: sensorID=%s value=%.1f tenant=%s\n",
					r.SensorID, r.Value, tenantID)
			}
			return nil
		},
		mqtt5adapter.SubscribeOptions{
			Observer: obs,
			OnError: func(e mqtt5adapter.SubscribeError) {
				logger.Warn("subscribe error", "error", e)
			},
			UserPropertyParams: []mqtt5adapter.UserPropertyParam{
				// Required: every message must carry a non-empty TenantID.
				mqtt5adapter.UserPropertyParam{Name: "TenantID", Required: true}.
					WithCodec(codex.String().Refine(validate.NonEmptyString)),
			},
		},
	); err != nil {
		fmt.Fprintf(os.Stderr, "subscribe: %v\n", err)
		os.Exit(1)
	}

	pubHandle, _ := ReadingsChannel.Register(evtBuilder)

	// Publish a valid reading: TenantID provided, ContentType set.
	fmt.Println("\n  → Publishing valid reading with TenantID=acme and ContentType=application/json")
	_ = mqtt5adapter.Publish(ctx, broker, pubHandle, 1, false,
		SensorReading{SensorID: sensorID, Value: 22.5},
		map[string]string{"sensorID": sensorID},
		mqtt5adapter.PublishOptions{
			Observer:    obs,
			ContentType: "application/json", // ← MQTT 5 feature 3: ContentType property
			UserProperties: []mqtt5adapter.UserProperty{
				{Key: "TenantID", Value: "acme"},
				{Key: "Source", Value: "factory-floor"},
			},
		},
	)

	time.Sleep(50 * time.Millisecond)

	// Publish a message WITHOUT TenantID — UserPropertyParam should reject it.
	fmt.Println("\n  → Publishing reading WITHOUT TenantID (should be rejected by UserPropertyParam):")
	var rejected mqtt5adapter.SubscribeError
	broker2, router2 := newMockBroker()
	subHandle2, _ := ReadingsChannel.Register(evtBuilder)
	_ = mqtt5adapter.Subscribe(ctx, broker2, router2, subHandle2, 1,
		func(_ context.Context, _ SensorReading) error { return nil },
		mqtt5adapter.SubscribeOptions{
			OnError: func(e mqtt5adapter.SubscribeError) { rejected = e },
			UserPropertyParams: []mqtt5adapter.UserPropertyParam{
				mqtt5adapter.UserPropertyParam{Name: "TenantID", Required: true}.
					WithCodec(codex.String().Refine(validate.NonEmptyString)),
			},
		},
	)
	_, _ = broker2.Publish(ctx, &pahomqtt5.Publish{
		Topic:   "sensors/" + sensorID + "/readings",
		Payload: []byte(`{"sensor_id":"f47ac10b-58cc-4372-a567-0e02b2c3d479","value":1.0}`),
		// No User Properties — TenantID missing
	})
	time.Sleep(50 * time.Millisecond)
	if rejected.Kind == mqtt5adapter.KindSecurity {
		var missing mqtt5adapter.MissingUserPropertyError
		if errors.As(rejected, &missing) {
			fmt.Printf("  ✓ rejected: KindSecurity, missing property %q\n", missing.Name)
		}
	}
}

// ── Demo 2: Request-Reply (ResponseTopic + CorrelationData) ───────────────────

func runRequestReplyDemo(ctx context.Context, obs stats.Observer, logger *slog.Logger) {
	fmt.Println("\n── Demo 2: Request-Reply (ResponseTopic + CorrelationData) ──")
	fmt.Println("  (No equivalent in MQTT 3.1.1 — MQTT 5.0 only)")

	broker, router := newMockBroker()

	rrBuilder := reqreply.NewBuilder(reqreply.Info{Title: "Compute API", Version: "1.0.0"})
	rrBuilder.AddServer("mqtt5", reqreply.Server{
		URL:      "mqtt://broker:1883",
		Protocol: "mqtt5",
	})
	handle, _ := ComputeRoute.Register(rrBuilder)

	// Responder: subscribes to "compute/add", replies to ResponseTopic.
	_ = mqtt5adapter.Serve(ctx, broker, router, handle,
		func(_ context.Context, req ComputeReq) (ComputeResp, error) {
			return ComputeResp{Sum: req.X + req.Y}, nil
		},
		mqtt5adapter.ServeOptions{Observer: obs},
	)

	// Requester: publishes with ResponseTopic + CorrelationData; waits for reply.
	reqs := []ComputeReq{{X: 3, Y: 4}, {X: 10, Y: 20}, {X: -5, Y: 5}}
	for _, req := range reqs {
		resp, err := mqtt5adapter.Call(ctx, broker, router, handle, req,
			mqtt5adapter.CallOptions{
				ReplyTopicPrefix: "replies",
				Timeout:          2 * time.Second,
				Observer:         obs,
			})
		if err != nil {
			fmt.Fprintf(os.Stderr, "request error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("  compute(%d + %d) = %d\n", req.X, req.Y, resp.Sum)
	}
	_ = logger
}

// ── Demo 3: AsyncAPI spec generation ─────────────────────────────────────────

func printSpecs(logger *slog.Logger) {
	fmt.Println("\n── AsyncAPI specs (protocol: mqtt5) ──")

	// PUB/SUB spec via events.Builder
	evtBuilder := events.NewBuilder(events.Info{Title: "Sensor Network", Version: "1.0.0"})
	evtBuilder.AddServer("mqtt5", events.Server{URL: "mqtt://broker:1883", Protocol: "mqtt5"})
	_, _ = ReadingsChannel.Register(evtBuilder)

	evtDoc, _ := evtBuilder.AsyncAPISpec()
	evtYAML, _ := evtDoc.MarshalYAML()
	fmt.Println("\n[PUB/SUB spec]")
	fmt.Println(string(evtYAML))

	// Request-Reply spec via reqreply.Builder
	rrBuilder := reqreply.NewBuilder(reqreply.Info{Title: "Compute API", Version: "1.0.0"})
	rrBuilder.AddServer("mqtt5", reqreply.Server{URL: "mqtt://broker:1883", Protocol: "mqtt5"})
	_, _ = ComputeRoute.Register(rrBuilder)

	rrDoc, _ := rrBuilder.AsyncAPISpec()
	rrYAML, _ := rrDoc.MarshalYAML()
	fmt.Println("[Request-Reply spec]")
	fmt.Println(string(rrYAML))

	_ = logger
}
