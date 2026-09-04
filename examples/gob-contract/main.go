// Package gob-contract demonstrates the "Go library as contract" pattern.
//
// Instead of an OpenAPI or AsyncAPI document as the cross-service contract,
// a shared Go module (the contract/ subpackage in this example) defines:
//
//   - The domain type (Order) with exported fields (required by encoding/gob)
//   - The codec (OrderCodec) — shape, constraints, and schema in one value
//   - The wire format (GobFormat = format.Gob(OrderCodec)) — binary, Go-native
//   - The REST route (OrderRoute) AND the pub/sub channel (OrderChannel) —
//     BOTH declare GobFormat inline as their format, so the Go compiler AND
//     the shared codec/format enforce the contract identically across
//     REST client/server and pub/sub roles.
//
// Both services import this package. The Go compiler enforces the contract:
// any field rename, type change, or constraint modification breaks compilation
// on both sides immediately — no stale YAML, no schema drift, no code-generation.
//
// # When to use this pattern
//
// Use the "Go library as contract" pattern when:
//   - All services communicating over this channel are written in Go
//   - You want binary-efficient wire encoding without a schema compiler (protobuf, Avro)
//   - Compile-time contract enforcement matters more than cross-language interoperability
//
// For external-facing APIs (consumed by non-Go clients or documented via tooling),
// use JSON/YAML formats and generate OpenAPI or AsyncAPI specs from the same codec.
//
// # What about OpenAPI/AsyncAPI with Gob?
//
// You can add format.Gob to a route or channel and the spec renderer will emit
// "application/gob" as the content type alongside the JSON Schema body. The schema
// documents the logical data shape — useful for humans — but tooling (Swagger UI,
// API gateways, code generators) cannot interpret or validate binary gob payloads.
// Keep "application/gob" out of external-facing specs; use it only for internal
// Go-to-Go channels where the Go library is the authoritative contract.
//
// # Proving Decision 9's declared-format fix, on BOTH APIs
//
// `Client.Attach`'s reflection shims (REST's `Call`, and events' `Publish`/
// `Subscribe`) used to always assume plain JSON, silently ignoring a
// route/channel's declared Gob format — a confirmed bug fixed by
// centralizing format resolution onto the handle itself
// (`RouteHandle.EncodeRequestWithFormats`/`DecodeResponseWithFormats`,
// `ChannelHandle.EncodeWithFormats`/`DecodeMergedWithFormats`). See
// docs/design/d-0001-rest-middleware-workflow-simplification.md's Addendum 2 (REST)
// and docs/design/d-0002-pubsub-workflow-simplification.md's Decision 9
// (events). This example proves BOTH fixes with the SAME shared Gob
// contract:
//
//   - "REST client/server" — a real server (nethttp.ServeOne) and a real
//     client (rest.NewClient + nethttp.Attach + Client.Call) exchange an
//     Order over Gob in BOTH directions, entirely through Client.Call —
//     no ClientHandle()/WithRequestFormats escape hatch needed.
//   - "Pub/Sub" — a publisher and a subscriber (events.NewClient +
//     adapters/mqtt.Attach + Client.Publish/Client.Subscribe, backed by
//     an in-process mock MQTT client) exchange the SAME Order over Gob,
//     entirely through Client.Attach.
//
// Run with: go run ./examples/gob-contract
package main

import (
	"bytes"
	"context"
	"encoding/gob"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"time"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"

	adaptermqtt "github.com/DaniDeer/go-codex/adapters/mqtt"
	nethttp "github.com/DaniDeer/go-codex/adapters/nethttp"
	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/examples/gob-contract/contract"
)

func main() {
	// ── 1. Producer side: encode Order to gob bytes ───────────────────────────
	fmt.Println("=== 1. Producer: marshal Order to gob bytes ===")

	order := contract.Order{
		ID:       "f47ac10b-58cc-4372-a567-0e02b2c3d479",
		Product:  "Wireless Keyboard",
		Quantity: 2,
		Price:    49.99,
	}

	data, err := contract.GobFormat.Marshal(order)
	if err != nil {
		fmt.Fprintln(os.Stderr, "marshal error:", err)
		os.Exit(1)
	}
	fmt.Printf("encoded %d bytes (binary — not human-readable)\n", len(data))
	fmt.Println()

	// ── 2. Consumer side: decode gob bytes back to Order ─────────────────────
	fmt.Println("=== 2. Consumer: unmarshal gob bytes to Order ===")

	// Both services use the same contract.GobFormat — same codec, same constraints.
	received, err := contract.GobFormat.Unmarshal(data)
	if err != nil {
		fmt.Fprintln(os.Stderr, "unmarshal error:", err)
		os.Exit(1)
	}
	fmt.Printf("received: %+v\n", received)
	fmt.Println()

	// ── 3. Constraint enforcement on the producer side ────────────────────────
	fmt.Println("=== 3. Constraint enforcement (producer rejects invalid Order) ===")

	badOrders := []struct {
		label string
		order contract.Order
	}{
		{"negative price", contract.Order{ID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Product: "Widget", Quantity: 1, Price: -5.0}},
		{"zero quantity", contract.Order{ID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Product: "Widget", Quantity: 0, Price: 9.99}},
		{"invalid UUID", contract.Order{ID: "not-a-uuid", Product: "Widget", Quantity: 1, Price: 9.99}},
		{"empty product", contract.Order{ID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Product: "", Quantity: 1, Price: 9.99}},
	}

	for _, bc := range badOrders {
		_, marshalErr := contract.GobFormat.Marshal(bc.order)
		var ve codex.ValidationErrors
		if errors.As(marshalErr, &ve) {
			fmt.Printf("  %-16s → %v\n", bc.label+":", marshalErr)
		}
	}
	fmt.Println()

	// ── 4. Constraint enforcement on the consumer side ────────────────────────
	fmt.Println("=== 4. Constraint enforcement (consumer validates on unmarshal) ===")

	// Simulate a misbehaving producer: encode an invalid Order directly via
	// encoding/gob (bypassing contract.GobFormat's codec validation).
	// The consumer's Unmarshal still validates via the codec after decoding.
	var buf bytes.Buffer
	if encErr := gob.NewEncoder(&buf).Encode(contract.Order{
		ID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Product: "Widget", Quantity: -1, Price: 0.0,
	}); encErr != nil {
		fmt.Fprintln(os.Stderr, "raw gob encode error:", encErr)
		os.Exit(1)
	}
	_, err = contract.GobFormat.Unmarshal(buf.Bytes())
	fmt.Printf("  consumer rejected tampered payload: %v\n", err)
	fmt.Println()

	// ── 5. REST client/server: Gob request + response, entirely via Client.Call ──
	fmt.Println("=== 5. REST client/server: Gob request + response via Client.Attach/Call ===")

	// SERVER side — registers contract.OrderRoute (declares
	// rest.RequestFormats(GobFormat)/rest.Formats(GobFormat) inline) with
	// an echo handler. nethttp.ServeOne builds a scratch single-route
	// server internally — no separate rest.NewServer call needed.
	handler, err := nethttp.ServeOne(contract.OrderRoute.WithHandler(
		func(_ context.Context, req contract.Order) (contract.Order, error) {
			fmt.Printf("  [server] received: %+v\n", req)
			return req, nil // echo — proves BOTH directions round-trip over Gob
		}))
	if err != nil {
		fmt.Fprintln(os.Stderr, "register OrderRoute:", err)
		os.Exit(1)
	}
	srv := httptest.NewServer(handler)
	defer srv.Close()

	// CLIENT side — rest.NewClient + nethttp.Attach is the PREFERRED
	// workflow: one Attach call, then Client.Call resolves
	// contract.OrderRoute's declared Gob formats automatically (both
	// directions) — no ClientHandle()/WithRequestFormats escape hatch.
	client := rest.NewClient()
	if err := nethttp.Attach(client, srv.Client(), srv.URL); err != nil {
		fmt.Fprintln(os.Stderr, "attach rest client:", err)
		os.Exit(1)
	}
	respAny, err := client.Call(context.Background(), contract.OrderRoute, order)
	if err != nil {
		fmt.Fprintln(os.Stderr, "call:", err)
		os.Exit(1)
	}
	resp := respAny.(contract.Order)
	fmt.Printf("  [client] received: %+v\n", resp)
	fmt.Println("  (request AND response both traveled as Gob bytes — Client.Call resolved contract.OrderRoute's declared format with zero manual encode/decode)")
	fmt.Println()

	// ── 6. Pub/Sub: Gob round-trip, entirely via Client.Attach ────────────────
	fmt.Println("=== 6. Pub/Sub: Gob round-trip via Client.Attach (MQTT, mocked) ===")

	eventsClient := events.NewClient(events.WithInfo(events.Info{
		Title:       "Order Service",
		Version:     "1.0.0",
		Description: "Internal order events — Go-to-Go binary channel (gob).",
	}))
	mock := newAutoDispatchMockClient()
	if err := adaptermqtt.Attach(eventsClient, mock); err != nil {
		fmt.Fprintln(os.Stderr, "attach events client:", err)
		os.Exit(1)
	}

	orderReceived := make(chan contract.Order, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go func() {
		_ = eventsClient.Subscribe(ctx, contract.OrderSubscriber, func(_ context.Context, o contract.Order) error {
			orderReceived <- o
			return nil
		})
	}()
	mock.waitForSubscription("orders/+", time.Second)

	if err := eventsClient.Publish(ctx, contract.OrderPublisher, order); err != nil {
		fmt.Fprintln(os.Stderr, "publish:", err)
		os.Exit(1)
	}

	select {
	case o := <-orderReceived:
		fmt.Printf("  [subscriber] received: %+v\n", o)
		fmt.Println("  (topic-derived orderId AND Gob-decoded body arrived in ONE struct — Client.Attach resolved contract.OrderChannel's declared format with zero manual encode/decode)")
	case <-ctx.Done():
		fmt.Fprintln(os.Stderr, "timed out waiting for the subscriber to receive the order")
		os.Exit(1)
	}
	fmt.Println()

	// ── 7. AsyncAPI spec ───────────────────────────────────────────────────────
	fmt.Println("=== 7. AsyncAPI spec (human documentation; Go library is the authoritative contract) ===")
	fmt.Println()

	doc, err := eventsClient.AsyncAPISpec()
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
}

// ── in-process mock MQTT client (replaces a real paho client in this demo) ──
//
// Copied from examples/adapters-mqtt's own mockClient — a minimal
// pahomqtt.Client implementation with auto-dispatch (Publish immediately
// fans out to matching Subscribe handlers), so no manual delivery step is
// needed for this example's single publish/subscribe round trip.

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

// newAutoDispatchMockClient returns a mockClient whose Publish immediately
// delivers to matching subscribers.
func newAutoDispatchMockClient() *mockClient {
	return &mockClient{handlers: make(map[string]pahomqtt.MessageHandler)}
}

func (c *mockClient) Subscribe(topic string, _ byte, handler pahomqtt.MessageHandler) pahomqtt.Token {
	c.mu.Lock()
	c.handlers[topic] = handler
	c.mu.Unlock()
	return newMockToken()
}

func (c *mockClient) Publish(topic string, _ byte, _ bool, payload interface{}) pahomqtt.Token {
	c.deliver(topic, payload.([]byte))
	return newMockToken()
}

func (c *mockClient) deliver(topic string, payload []byte) {
	c.mu.Lock()
	var matched []pahomqtt.MessageHandler
	for sub, h := range c.handlers {
		if topicMatchesSub(sub, topic) {
			matched = append(matched, h)
		}
	}
	c.mu.Unlock()
	for _, h := range matched {
		h(c, &mockMessage{topic: topic, payload: payload})
	}
}

// topicMatchesSub reports whether a concrete MQTT topic matches a
// subscription pattern. It handles MQTT single-level (+) and multi-level
// (#) wildcards.
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
// [mockClient.Subscribe], or timeout elapses. Client.Subscribe registers
// the subscription then blocks until ctx is cancelled — so callers run it
// in a goroutine and must synchronize before publishing.
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
