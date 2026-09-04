// Package adapters-mqtt5 demonstrates the MQTT 5.0 adapter and its features
// that are specific to MQTT 5.0, compared to the MQTT 3.1.1 adapter:
//
//   - User Properties: per-message key-value metadata sent with each PUBLISH
//   - UserPropertyParam: codec validation on User Properties (mirrors rest.HeaderParam)
//   - ContentType: messages carry their own format identifier for auto-selection
//   - Request-Reply: typed RPC over MQTT using ResponseTopic + CorrelationData
//
// The example uses an in-process mock broker to avoid requiring a real MQTT 5.0
// broker. In production, either connect manually to Mosquitto ≥ 2.0 (MQTT 5.0
// enabled by default):
//
//	conn, _ := net.Dial("tcp", "localhost:1883")
//	router := paho.NewStandardRouter()
//	client := paho.NewClient(paho.ClientConfig{Conn: conn, Router: router})
//	client.Connect(ctx, &paho.Connect{ClientID: "my-service", CleanStart: true})
//
// or use the newer, connection-owning [mqtt5adapter.Connect] helper, which
// wraps exactly the manual pattern above into a single call:
//
//	client, router, err := mqtt5adapter.Connect(ctx, "localhost:1883", mqtt5adapter.ConnectOptions{
//	    ClientID: "my-service", CleanStart: true,
//	})
//
// # Layer structure
//
// Layer 1: Codec[SensorReading] + Codec[ComputeReq] + Codec[ComputeResp]
// Layer 2: events.NewChannel  (PUB/SUB → AsyncAPI with protocol: mqtt5)
//
//	rest.NewRoute       (REQ/REP → AsyncAPI request-reply via reqreply.Builder)
//
// Layer 3 (PUB/SUB) — TWO workflows, in order of preference:
//
//  1. PREFERRED — events.Client + mqtt5adapter.Attach + Client.Publish/.Subscribe
//     (Decision 5 of docs/roadmap/pubsub-workflow-simplification.md). Attach
//     the adapter to the client ONCE; from there, client.Publish/client.Subscribe
//     fulfill each declared Subscriber[T]/Publisher[T]'s requirements directly —
//     no further mqtt5adapter.* calls needed at the usage site. The SAME client
//     doubles as the spec source (client.AsyncAPISpec()) with zero extra
//     ceremony, since Attach/Publish/Subscribe register into it automatically.
//     See Demo 1 below.
//
//     client := events.NewClient(events.WithInfo(events.Info{Title: "Sensor Network", Version: "1.0.0"}))
//     if err := mqtt5adapter.Attach(client, broker, router); err != nil { ... }
//     go client.Subscribe(ctx, ReadingsSubscriber, func(ctx context.Context, r SensorReading) error {
//     ...
//     return nil
//     })
//     err := client.Publish(ctx, ReadingsPublisher, reading)
//
//  2. ESCAPE HATCH — mqtt5adapter.NewSubscribeTransport / NewPublishTransport
//     build per-T [events.SubscribeTransport]/[events.PublishTransport] values
//     bound to a client+router; events.SubscribeHandle / events.PublishHandle
//     then drive them against a declared events.Subscriber[T]/events.Publisher[T]
//     with NO *events.Client/spec required (Decision 7's inverted, handle-based
//     call surface). Use this ONLY when Client.Attach's v1 reflection shim
//     can't express what's needed: custom OnError, a per-call Observer
//     override, non-default QoS, ContentType/UserProperties, or
//     UserPropertyParam validation — see Demo 2 below, which needs exactly
//     these. Building a spec from THIS workflow requires a SEPARATE,
//     throwaway events.Client purely for .Handle(builder) — contrast with
//     workflow 1's spec-for-free.
//
//     transport := mqtt5adapter.NewSubscribeTransport[SensorReading](client, router, 1, mqtt5adapter.SubscribeOptions{})
//     err := events.SubscribeHandle(ctx, ReadingsSubscriber, transport, func(ctx context.Context, r SensorReading) error {
//     ...
//     return nil
//     })
//
// Layer 3 (REQ/REP): mqtt5adapter.Serve / Call — reqreply has no Client/Attach
// equivalent yet; see docs/roadmap/reqreply-workflow-simplification.md.
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
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/ports"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/stats"
	gstream "github.com/DaniDeer/go-codex/stream"
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
	events.TopicParam{
		Name:        "sensorID",
		Description: "UUID of the originating sensor.",
	}.WithCodec(codex.String().Refine(validate.UUID)),
)

// ReadingsSubscriber/ReadingsPublisher fork ReadingsChannel's shared
// topic/codec/TopicParam declaration into its two roles — see
// [events.Channel.WithSubscribe]/[events.Channel.WithPublish]. Each
// produces its own independent *events.ChannelHandle[T] via .Handle(...);
// registering BOTH against the SAME events.Client for the SAME topic
// dedups to the FIRST-registered role's spec entry (documented,
// first-registered-wins behavior — see
// docs/roadmap/pubsub-workflow-simplification.md's Decision 1).
var ReadingsSubscriber = ReadingsChannel.WithSubscribe(events.Subscribe{
	OperationID: "receiveSensorReading",
	Summary:     "Receive a sensor reading.",
})

var ReadingsPublisher = ReadingsChannel.WithPublish(events.Publish{
	OperationID: "publishSensorReading",
	Summary:     "Publish a sensor reading.",
})

var ComputeRoute = reqreply.NewRoute[ComputeReq, ComputeResp](
	"compute/add",
	computeReqCodec, computeRespCodec,
	reqreply.RouteMeta{OperationID: "computeAdd", Summary: "Add two integers via MQTT 5 request-reply."},
)

// ── Security demo: WithSecurityScheme + CredentialFunc/SecurityFunc ──────────
//
// bearerAuth is declared ONCE and referenced by both SecuredComputeRoute
// (request-reply) below and SecuredReadingsChannel (in runSecurityDemo) —
// the SAME declaration is consumed identically by server (Serve/Subscribe)
// and client (Call/Publish), mirroring rest.WithSecurityScheme exactly.
var bearerAuth = reqreply.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")}.
	WithCodec(codex.String().Refine(validate.NonEmptyString))

var SecuredComputeRoute = reqreply.NewRoute[ComputeReq, ComputeResp](
	"compute/secured-add",
	computeReqCodec, computeRespCodec,
	reqreply.RouteMeta{
		OperationID: "securedComputeAdd",
		Summary:     "Add two integers via MQTT 5 request-reply — requires a bearer token.",
		Security:    []route.SecurityRequirement{route.Require("bearerAuth")},
	},
	reqreply.WithSecurityScheme("bearerAuth", bearerAuth),
)

// ── Error-path ergonomics: events.ErrorChannel ────────────────────────────────
//
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

var sensorErrorPayloadCodec = codex.Struct[SensorErrorPayload](
	codex.RequiredField("code", codex.String().Refine(validate.NonEmptyString),
		func(e SensorErrorPayload) string { return e.Code },
		func(e *SensorErrorPayload, v string) { e.Code = v },
	),
	codex.RequiredField("message", codex.String(),
		func(e SensorErrorPayload) string { return e.Message },
		func(e *SensorErrorPayload, v string) { e.Message = v },
	),
)

// ReadingsChannelWithErrors mirrors ReadingsChannel but additionally declares
// an events.ErrorChannel: when a SensorOutOfRangeError reaches the publish
// adapter (e.g. from an upstream pipeline stage), the typed payload is
// published to "sensors/{sensorID}/readings/errors" instead of just being
// forwarded to OnError.
var ReadingsChannelWithErrors = events.NewChannel[SensorReading](
	"sensors/{sensorID}/readings",
	sensorCodec,
	events.TopicParam{Name: "sensorID"}.WithCodec(codex.String().Refine(validate.UUID)),
	events.ErrorChannel[SensorOutOfRangeError, SensorErrorPayload](
		"sensors/{sensorID}/readings/errors", sensorErrorPayloadCodec,
		func(e SensorOutOfRangeError) (SensorErrorPayload, error) {
			return SensorErrorPayload{Code: "out_of_range", Message: e.Error()}, nil
		},
	),
).WithPublish(events.Publish{OperationID: "publishSensorReadingWithErrors"})

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

// waitHandler blocks until the handler for topic is registered or 1 second
// passes. Client.Subscribe (Decision 5's Attach-based workflow) registers
// asynchronously in a background goroutine — a caller publishing immediately
// afterward must synchronize against registration first, since (unlike
// zeromq's buffered pipe socket) this mock router's dispatch has no buffer:
// a message published before the handler is registered is simply dropped.
func (r *mockRouter) waitHandler(topic string) {
	for i := 0; i < 200; i++ {
		r.mu.Lock()
		_, ok := r.handlers[topic]
		r.mu.Unlock()
		if ok {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
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

// Print reports the accumulated counts — called after Demo 1 to prove
// Client.Attach's Publish/Subscribe route through the SAME stats.Observer
// as the escape-hatch primitives (Decision 8's Observer fix).
func (c *eventCounter) Print() {
	c.mu.Lock()
	defer c.mu.Unlock()
	fmt.Printf("  [metrics] subscribes=%d publishes=%d requests=%d\n", c.subscribes, c.publishes, c.requests)
}

// ── main ──────────────────────────────────────────────────────────────────────

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	counter := &eventCounter{}
	// stats.NewFanout separates metrics (counter) from logging (NewLoggingObserver)
	// — neither observer mixes both concerns.
	obs := stats.NewFanout(counter, stats.NewLoggingObserver(logger))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	// Store obs in ctx once — Subscribe/Publish/Serve/Call all resolve it
	// automatically from ctx when Options.Observer is nil.
	ctx = stats.WithObserver(ctx, obs)
	defer cancel()

	fmt.Println("═══════════════════════════════════════════════════════")
	fmt.Println(" MQTT 5 adapter — features specific to MQTT 5.0")
	fmt.Println("═══════════════════════════════════════════════════════")

	runClientAttachDemo(ctx, logger, counter)
	runPubSubDemo(ctx, logger)
	runErrorChannelDemo(ctx)
	runRequestReplyDemo(ctx, logger)
	runSecurityDemo(ctx, logger)
	runConnectSecurityDemo(ctx)
	printSpecs(logger)
}

// ── Demo 1: Client.Attach — the PREFERRED workflow ───────────────────────────
//
// The simple, no-frills case (plain JSON payload, default QoS, no custom
// OnError/User Properties) should use events.Client + mqtt5adapter.Attach +
// Client.Publish/.Subscribe — mirroring examples/adapters-zeromq exactly.
// Attach the adapter to the client ONCE; from there, client.Publish/
// client.Subscribe fulfill ReadingsPublisher/ReadingsSubscriber's declared
// requirements directly, with NO further mqtt5adapter.* calls at the usage
// site. The SAME client doubles as the spec source below — Attach/Publish/
// Subscribe register into it automatically, so client.AsyncAPISpec() needs
// no separate builder. Contrast with Demo 2, which needs the handle-based
// escape hatch (and a SEPARATE throwaway builder) purely because it uses
// capabilities Client.Attach's v1 reflection shim doesn't support.
func runClientAttachDemo(ctx context.Context, logger *slog.Logger, counter *eventCounter) {
	fmt.Println("\n── Demo 1: Client.Attach — the PREFERRED workflow ──")

	broker, router := newMockBroker()

	client := events.NewClient(events.WithInfo(events.Info{Title: "Sensor Network", Version: "1.0.0"}))
	client.AddServer("mqtt5", events.Server{
		URL:         "mqtt://broker:1883",
		Protocol:    "mqtt5",
		Description: "MQTT 5.0 broker for sensor data",
	})

	// ATTACH the adapter to the client — the one place transport specifics
	// (broker + router) enter the picture.
	if err := mqtt5adapter.Attach(client, broker, router); err != nil {
		fmt.Fprintf(os.Stderr, "attach: %v\n", err)
		os.Exit(1)
	}

	received := make(chan SensorReading, 4)
	go func() {
		_ = client.Subscribe(ctx, ReadingsSubscriber, func(_ context.Context, r SensorReading) error {
			fmt.Printf("  received: sensorID=%s value=%.1f\n", r.SensorID, r.Value)
			received <- r
			return nil
		})
	}()
	// Client.Subscribe registers with the router asynchronously — wait for
	// it before publishing (see waitHandler's doc comment). The registered
	// filter is the topic template with {varName} placeholders replaced by
	// the MQTT "+" wildcard (mirrors mqtt5adapter's own internal derivation).
	router.waitHandler(normaliseTopic("sensors/{sensorID}/readings"))

	sensorID := "f47ac10b-58cc-4372-a567-0e02b2c3d479"
	readings := []SensorReading{
		{SensorID: sensorID, Value: 22.5},
		{SensorID: sensorID, Value: 23.1},
	}
	for _, r := range readings {
		if err := client.Publish(ctx, ReadingsPublisher, r); err != nil {
			fmt.Fprintf(os.Stderr, "publish: %v\n", err)
			os.Exit(1)
		}
	}

	for i := 0; i < len(readings); i++ {
		select {
		case <-received:
		case <-ctx.Done():
			fmt.Fprintln(os.Stderr, "timeout waiting for readings")
			os.Exit(1)
		}
	}

	// The spec is a free byproduct of the calls above — no separate builder.
	doc, err := client.AsyncAPISpec()
	if err != nil {
		fmt.Fprintf(os.Stderr, "spec: %v\n", err)
		os.Exit(1)
	}
	specYAML, err := doc.MarshalYAML()
	if err != nil {
		fmt.Fprintf(os.Stderr, "marshal spec: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("\n  [spec — printed directly from the SAME client, zero extra ceremony]")
	fmt.Println(string(specYAML))
	counter.Print()
	_ = logger
}

// ── Demo 2b: Error-path ergonomics — events.ErrorChannel ─────────────────────
//
// Demonstrates the pub/sub analogue of rest.ErrorPattern: a declared
// events.ErrorChannel on ReadingsChannelWithErrors causes
// mqtt5.PublishAdapter to publish a typed error payload to a dedicated
// error-output topic whenever a matching domain error reaches it — instead
// of only calling MQTT5DrainPublishOptions.OnError.
func runErrorChannelDemo(ctx context.Context) {
	fmt.Println("\n── Demo 2b: Error-path ergonomics (events.ErrorChannel) ──")

	broker, _ := newMockBroker()
	evtBuilder := events.NewClient(events.WithInfo(events.Info{Title: "Sensor Network", Version: "1.0.0"}))
	handle, err := ReadingsChannelWithErrors.Handle(evtBuilder)
	if err != nil {
		fmt.Fprintf(os.Stderr, "register: %v\n", err)
		os.Exit(1)
	}

	sensorID := "f47ac10b-58cc-4372-a567-0e02b2c3d479"

	// A SinkPort + PublishAdapter fed a stream — the mqtt5 port-adapter path
	// (as opposed to the direct mqtt5adapter.Publish calls in Demo 2).
	port, err := ports.NewSinkPort[SensorReading]("readings-with-errors", sensorCodec, ports.PortOptions{Buffer: 4})
	if err != nil {
		fmt.Fprintf(os.Stderr, "construct port: %v\n", err)
		os.Exit(1)
	}
	var onErrorCalled bool
	port.Bind(ctx, mqtt5adapter.PublishAdapter(broker, handle, format.JSON(sensorCodec),
		mqtt5adapter.MQTT5DrainPublishOptions{
			Vars: map[string]string{"sensorID": sensorID},
			OnError: func(e error) {
				onErrorCalled = true
				fmt.Printf("  ✗ OnError fallback called (unexpected for a matched pattern): %v\n", e)
			},
		}))

	// Simulate an upstream pipeline stage emitting a validation error instead
	// of a value — gstream.Stream carries it on Errors.
	errCh := make(chan error, 1)
	valCh := make(chan SensorReading)
	errCh <- SensorOutOfRangeError{SensorID: sensorID, Value: 999.9}
	close(errCh)
	close(valCh)
	port.Feed(ctx, gstream.Stream[SensorReading]{Values: valCh, Errors: errCh})

	time.Sleep(50 * time.Millisecond)
	if !onErrorCalled {
		fmt.Printf("  ✓ matched SensorOutOfRangeError → published typed payload to %q (OnError NOT called)\n",
			"sensors/"+sensorID+"/readings/errors")
	}
}

// ── Demo 2: PUB/SUB escape hatch — User Properties, ContentType, UserPropertyParam ─
//
// Unlike Demo 1's Client.Attach workflow, this demo needs the handle-based
// escape hatch: custom OnError, UserPropertyParam validation, and
// ContentType/UserProperties on publish are ALL capabilities Client.Attach's
// v1 reflection shim does not support (see the package doc comment above).
// Building a spec from this workflow (see printSpecs below) needs its OWN
// throwaway events.Client, unlike Demo 1's spec-for-free.

func runPubSubDemo(ctx context.Context, logger *slog.Logger) {
	fmt.Println("\n── Demo 2: PUB/SUB escape hatch (User Properties + ContentType + UserPropertyParam) ──")

	broker, router := newMockBroker()
	sensorID := "f47ac10b-58cc-4372-a567-0e02b2c3d479"

	evtBuilder := events.NewClient(events.WithInfo(events.Info{Title: "Sensor Network", Version: "1.0.0"}))
	evtBuilder.AddServer("mqtt5", events.Server{
		URL:         "mqtt://broker:1883",
		Protocol:    "mqtt5",
		Description: "MQTT 5.0 broker for sensor data",
	})

	// ── MQTT 5 feature 1: UserPropertyParam ─────────────────────────────────
	// Validate the TenantID User Property on every incoming message.
	// This mirrors rest.HeaderParam for HTTP — no equivalent in MQTT 3.1.1.
	//
	// Decision 7's inverted, no-*Client-needed call shape: build a
	// per-T transport bound to the broker+router, then hand it (plus the
	// declared ReadingsSubscriber) to events.SubscribeHandle.
	subTransport := mqtt5adapter.NewSubscribeTransport[SensorReading](broker, router, 1,
		mqtt5adapter.SubscribeOptions{ // Observer resolved from ctx
			OnError: func(e mqtt5adapter.SubscribeError) {
				logger.Warn("subscribe error", "error", e)
			},
			UserPropertyParams: []mqtt5adapter.UserPropertyParam{
				// Required: every message must carry a non-empty TenantID.
				mqtt5adapter.UserPropertyParam{Name: "TenantID", Required: true}.
					WithCodec(codex.String().Refine(validate.NonEmptyString)),
			},
		},
	)
	if err := events.SubscribeHandle(ctx, ReadingsSubscriber, subTransport,
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
	); err != nil {
		fmt.Fprintf(os.Stderr, "subscribe: %v\n", err)
		os.Exit(1)
	}

	// Publish a valid reading: TenantID provided, ContentType set.
	fmt.Println("\n  → Publishing valid reading with TenantID=acme and ContentType=application/json")
	pubTransport := mqtt5adapter.NewPublishTransport[SensorReading](broker, 1, false,
		mqtt5adapter.PublishOptions[SensorReading]{ // Observer resolved from ctx
			ContentType: "application/json", // ← MQTT 5 feature 3: ContentType property
			UserProperties: []mqtt5adapter.UserProperty{
				{Key: "TenantID", Value: "acme"},
				{Key: "Source", Value: "factory-floor"},
			},
		},
	)
	_ = events.PublishHandle(ctx, ReadingsPublisher, pubTransport,
		SensorReading{SensorID: sensorID, Value: 22.5},
	)

	time.Sleep(50 * time.Millisecond)

	// Publish a message WITHOUT TenantID — UserPropertyParam should reject it.
	fmt.Println("\n  → Publishing reading WITHOUT TenantID (should be rejected by UserPropertyParam):")
	var rejected mqtt5adapter.SubscribeError
	broker2, router2 := newMockBroker()
	subTransport2 := mqtt5adapter.NewSubscribeTransport[SensorReading](broker2, router2, 1,
		mqtt5adapter.SubscribeOptions{
			OnError: func(e mqtt5adapter.SubscribeError) { rejected = e },
			UserPropertyParams: []mqtt5adapter.UserPropertyParam{
				mqtt5adapter.UserPropertyParam{Name: "TenantID", Required: true}.
					WithCodec(codex.String().Refine(validate.NonEmptyString)),
			},
		},
	)
	_ = events.SubscribeHandle(ctx, ReadingsSubscriber, subTransport2,
		func(_ context.Context, _ SensorReading) error { return nil },
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

// ── Demo 3: Request-Reply (ResponseTopic + CorrelationData) ───────────────────
//
// reqreply has no Client/Attach equivalent yet — Serve/Call remain the sole
// entry points; see docs/roadmap/reqreply-workflow-simplification.md for the
// design that would bring reqreply the same Client.Attach workflow PUB/SUB
// already has.

func runRequestReplyDemo(ctx context.Context, logger *slog.Logger) {
	fmt.Println("\n── Demo 3: Request-Reply (ResponseTopic + CorrelationData) ──")
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
		mqtt5adapter.ServeOptions{}, // Observer resolved from ctx
	)

	// Requester: publishes with ResponseTopic + CorrelationData; waits for reply.
	reqs := []ComputeReq{{X: 3, Y: 4}, {X: 10, Y: 20}, {X: -5, Y: 5}}
	for _, req := range reqs {
		resp, err := mqtt5adapter.Call(ctx, broker, router, handle, req,
			mqtt5adapter.CallOptions{ // Observer resolved from ctx
				ReplyTopicPrefix: "replies",
				Timeout:          2 * time.Second,
			})
		if err != nil {
			fmt.Fprintf(os.Stderr, "request error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("  compute(%d + %d) = %d\n", req.X, req.Y, resp.Sum)
	}
	_ = logger
}

// ── Demo 4: Security — WithSecurityScheme + CredentialFunc/SecurityFunc ──────
//
// SecuredComputeRoute declares bearerAuth ONCE via reqreply.WithSecurityScheme.
// The server (Serve) runs a BUILT-IN codec-based credential check — reading
// the "Authorization" MQTT 5 User Property, stripping "Bearer " — BEFORE the
// optional custom SecurityFunc. The client (Call) supplies the credential via
// CredentialFunc; the SAME built-in check runs client-side before publishing,
// so a malformed credential never reaches the wire.
func runSecurityDemo(ctx context.Context, logger *slog.Logger) {
	fmt.Println("\n── Demo 4: Security — WithSecurityScheme + CredentialFunc/SecurityFunc ──")

	broker, router := newMockBroker()

	rrBuilder := reqreply.NewBuilder(reqreply.Info{Title: "Secured Compute API", Version: "1.0.0"})
	rrBuilder.AddServer("mqtt5", reqreply.Server{URL: "mqtt://broker:1883", Protocol: "mqtt5"})
	handle, _ := SecuredComputeRoute.Register(rrBuilder)

	_ = mqtt5adapter.Serve(ctx, broker, router, handle,
		func(_ context.Context, req ComputeReq) (ComputeResp, error) {
			return ComputeResp{Sum: req.X + req.Y}, nil
		},
		mqtt5adapter.ServeOptions{
			// Runs AFTER the built-in Codec check passes — add extra
			// business logic here (e.g. a token-revocation check).
			SecurityFunc: func(_ context.Context, _ *pahomqtt5.Publish, _ []route.SecurityRequirement) error {
				return nil
			},
		},
	)

	// Happy path: CredentialFunc supplies a well-formed bearer token.
	fmt.Println("\n  → Call with a valid bearer token:")
	resp, err := mqtt5adapter.Call(ctx, broker, router, handle, ComputeReq{X: 7, Y: 8},
		mqtt5adapter.CallOptions{
			Timeout: 2 * time.Second,
			CredentialFunc: func(context.Context, []route.SecurityRequirement) ([]mqtt5adapter.UserProperty, error) {
				return []mqtt5adapter.UserProperty{{Key: "Authorization", Value: "Bearer valid-token-123"}}, nil
			},
		})
	if err != nil {
		fmt.Fprintf(os.Stderr, "unexpected error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("  ✓ compute(7 + 8) = %d (credential accepted)\n", resp.Sum)

	// Rejected path: CredentialFunc returns a malformed (empty) token — the
	// client-side built-in check catches this BEFORE the request is ever
	// published, returning reqreply.SecurityCredentialError.
	fmt.Println("\n  → Call with a malformed (empty) bearer token:")
	_, err = mqtt5adapter.Call(ctx, broker, router, handle, ComputeReq{X: 1, Y: 2},
		mqtt5adapter.CallOptions{
			Timeout: 2 * time.Second,
			CredentialFunc: func(context.Context, []route.SecurityRequirement) ([]mqtt5adapter.UserProperty, error) {
				return []mqtt5adapter.UserProperty{{Key: "Authorization", Value: "Bearer "}}, nil
			},
		})
	var credErr reqreply.SecurityCredentialError
	if errors.As(err, &credErr) {
		fmt.Printf("  ✓ rejected client-side: scheme=%q (request never published)\n", credErr.Scheme)
	} else {
		fmt.Fprintf(os.Stderr, "expected SecurityCredentialError, got: %v\n", err)
		os.Exit(1)
	}
	_ = logger
}

// ── Demo 4b: Connect-level security — mqtt5.NewSecuredClient ─────────────────
//
// Contrasts CONNECTION-level security against Demo 4's MESSAGE-level
// security: the SAME connectBearerAuth scheme is declared ONCE, but
// validated a single time at construction (via NewSecuredClient), right
// after the caller's own client.Connect(...) — NOT per message. The
// resulting *mqtt5adapter.SecuredClient is a drop-in replacement for the
// raw broker client: every Subscribe/Publish/Serve/Call call site below is
// completely UNCHANGED from how it would look with the raw client.
// MinLen(3) rejects the combined "username:password" string when either
// part is empty (e.g. "" + "" -> ":" is only 1 character) — a stand-in for
// a real-world constraint like "password must be a non-trivial token".
var connectBearerAuth = mqtt5adapter.ConnectSecurityScheme{SecurityScheme: route.BasicScheme()}.
	WithCodec(codex.String().Refine(validate.MinLen(3)))

func runConnectSecurityDemo(ctx context.Context) {
	fmt.Println("\n── Demo 4b: Connect-level security — mqtt5.NewSecuredClient ──")

	broker, router := newMockBroker()

	// The caller connects their own client FIRST (go-codex never calls
	// Connect() itself) — here the mock broker is already "connected" by
	// construction. In production: client.Connect(ctx, &paho.Connect{
	// Username: "svc-account", Password: []byte(token)}).
	fmt.Println("\n  → Wrapping an already-connected client with a valid credential:")
	secured, err := mqtt5adapter.NewSecuredClient(broker, connectBearerAuth, "svc-account", "s3cr3t-token")
	if err != nil {
		fmt.Fprintf(os.Stderr, "unexpected error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("  ✓ credential accepted — secured is a drop-in replacement for broker")

	// Every existing call site is UNCHANGED — secured satisfies MQTTClient
	// transparently via struct embedding, so it plugs into
	// NewPublishTransport exactly like a raw client would.
	securedTransport := mqtt5adapter.NewPublishTransport[SensorReading](secured, 1, false,
		mqtt5adapter.PublishOptions[SensorReading]{})
	_ = events.PublishHandle(ctx, ReadingsPublisher, securedTransport,
		SensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 22.5})
	fmt.Println("  ✓ Publish through the wrapper works exactly like the raw client")
	_ = router

	// Rejected path: a malformed (empty) credential is caught at
	// construction — the underlying client is never touched or used.
	fmt.Println("\n  → Wrapping the SAME client with a malformed (empty) credential:")
	_, err = mqtt5adapter.NewSecuredClient(broker, connectBearerAuth, "", "")
	var credErr2 mqtt5adapter.ConnectSecurityCredentialError
	if errors.As(err, &credErr2) {
		fmt.Printf("  ✓ rejected at construction: scheme_type=%q (client never used)\n", credErr2.Scheme.Type)
	} else {
		fmt.Fprintf(os.Stderr, "expected ConnectSecurityCredentialError, got: %v\n", err)
		os.Exit(1)
	}
}

// ── Remaining spec: Request-Reply ────────────────────────────────────────────
//
// The PUB/SUB spec was ALREADY printed in full back in Demo 1 — directly
// from the SAME events.Client that Attach/Publish/Subscribe used, with zero
// extra ceremony. reqreply has no Client/Attach equivalent (see Demo 3's own
// comment), so ITS spec still needs a dedicated, throwaway reqreply.Builder
// purely for .Register(builder) — this is the one spec print this example
// still needs a separate builder for.

func printSpecs(logger *slog.Logger) {
	fmt.Println("\n── AsyncAPI spec: Request-Reply (protocol: mqtt5) ──")

	rrBuilder := reqreply.NewBuilder(reqreply.Info{Title: "Compute API", Version: "1.0.0"})
	rrBuilder.AddServer("mqtt5", reqreply.Server{URL: "mqtt://broker:1883", Protocol: "mqtt5"})
	_, _ = ComputeRoute.Register(rrBuilder)

	rrDoc, _ := rrBuilder.AsyncAPISpec()
	rrYAML, _ := rrDoc.MarshalYAML()
	fmt.Println(string(rrYAML))

	_ = logger
}
