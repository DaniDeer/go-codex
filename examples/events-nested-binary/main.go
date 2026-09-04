// Package events-nested-binary demonstrates the Phase 2 "one struct, one
// call" merge-field convenience for MQTT/event channels (api/events +
// adapters/mqtt) — the pub/sub mirror of examples/rest-nested-binary.
//
// Two things worth proving here, exactly like the REST case:
//
//  1. Nested struct composition — the payload is built from sub-structs
//     (Meta for the topic-derived field, Value for the body) instead of
//     flat top-level fields. events.NewTopicParam's get/set are plain
//     closures, so nested access needs zero framework changes.
//  2. Non-JSON payload formats — format.Gob (via a custom
//     format.NewTyped projection) composes with topic merge fields
//     exactly like JSON/YAML/TOML would, since payload decode/encode is
//     completely orthogonal to var-merge, AND — the point this example
//     exists to prove — that composition now works correctly through the
//     PREFERRED Client.Attach workflow, not just the escape-hatch
//     primitives.
//
// Client.Attach's Publish/Subscribe reflection shims used to always
// assume plain JSON, silently ignoring a channel's declared format —
// this was fixed by centralizing format resolution onto ChannelHandle
// itself (EncodeWithFormats/DecodeMergedWithFormats), so every caller —
// escape-hatch AND Client.Attach — is a thin caller of the SAME method.
// See docs/roadmap/pubsub-workflow-simplification.md's Decision 9. This
// example runs the round trip through a REAL adapters/mqtt.Attach +
// Client.Publish/Client.Subscribe pair (backed by an in-process mock MQTT
// client, so it needs no real broker) — not a hand-simulation of what the
// adapter does internally, unlike this example's previous version.
//
// Run with: go run ./examples/events-nested-binary
package main

import (
	"bytes"
	"context"
	"encoding/gob"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/google/uuid"

	adaptermqtt "github.com/DaniDeer/go-codex/adapters/mqtt"
	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
)

// ── Domain types (nested composition) ──────────────────────────────────────

// SensorMeta holds the topic-derived field — populated purely via the
// merge field from the concrete topic, never part of the payload wire
// bytes. SensorID is a REAL uuid.UUID (via codex.TextCodec[uuid.UUID](),
// not codex.String().Refine(validate.UUID)) — no manual uuid.Parse needed
// on the subscriber side.
type SensorMeta struct {
	SensorID uuid.UUID
}

// SensorReading is the NESTED payload: Meta.SensorID comes from the topic,
// Value is the actual Gob-encoded body.
type SensorReading struct {
	Meta  SensorMeta
	Value float64
}

// ── Codecs ──────────────────────────────────────────────────────────────────

// readingCodec is a placeholder Codec[SensorReading] with no declared
// fields — the actual wire bytes are produced by gobFormat below (which
// projects onto ONLY Value, the same pattern examples/rest-nested-binary
// uses for its Gob body). Meta.SensorID is populated exclusively via the
// topic merge field, never via a payload codec field.
var readingCodec = codex.Struct[SensorReading]()

// gobFormat projects the Gob wire bytes onto JUST reading.Value.
//
// format.Gob(readingCodec) would instead gob-encode EVERY exported field
// of SensorReading (Meta AND Value) — Gob serialises the typed value
// directly via reflection, bypassing the codec's Encode/Decode entirely.
// format.NewTyped with a custom marshal/unmarshal projects onto/from the
// nested sub-field instead — see docs/features/rest-api.md's "Nested
// structs & binary body formats" section for the full explanation.
var gobFormat = format.NewTyped[SensorReading](
	readingCodec,
	func(r SensorReading) ([]byte, error) {
		var buf bytes.Buffer
		err := gob.NewEncoder(&buf).Encode(r.Value)
		return buf.Bytes(), err
	},
	func(data []byte) (SensorReading, error) {
		var v float64
		if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&v); err != nil {
			return SensorReading{}, err
		}
		return SensorReading{Value: v}, nil
	},
	"application/gob",
)

// sensorChannel is the shared contract — declares the topic template with
// a merge-capable topic param targeting the NESTED Meta.SensorID field,
// AND declares gobFormat inline via events.Formats — a first-class part
// of the channel's own declaration, not a post-hoc handle.WithFormats
// call. Client.Attach's Publish/Subscribe now resolve this declared
// format automatically (Decision 9), same as the escape-hatch primitives
// always did.
var sensorChannel = events.NewChannel[SensorReading]("sensors/{sensorID}/readings", readingCodec,
	events.NewTopicParam("sensorID", codex.TextCodec[uuid.UUID](),
		func(r SensorReading) uuid.UUID { return r.Meta.SensorID },
		func(r *SensorReading, v uuid.UUID) { r.Meta.SensorID = v },
	).WithDescription("Sensor ID (UUID) — merged from the topic, never the payload"),
	events.Formats(gobFormat),
)

var sensorSubscriber = sensorChannel.WithSubscribe(events.Subscribe{})
var sensorPublisher = sensorChannel.WithPublish(events.Publish{})

func main() {
	client := events.NewClient(events.WithInfo(events.Info{Title: "Sensor Events API", Version: "1.0.0"}))

	// ATTACH the adapter to the client — the one place transport specifics
	// (the mock MQTT client) enter the picture. Everything below calls
	// ONLY client.Publish/client.Subscribe — no adaptermqtt.* call at the
	// usage site.
	mock := newAutoDispatchMockClient()
	if err := adaptermqtt.Attach(client, mock); err != nil {
		fmt.Fprintln(os.Stderr, "attach:", err)
		os.Exit(1)
	}

	fmt.Println("=== Subscriber: Client.Attach decodes Gob payload AND merges the topic var ===")

	received := make(chan SensorReading, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go func() {
		// This is exactly what a real service would call — no manual
		// gobFormat.Unmarshal or codex.DecodeVars needed at the usage
		// site; Client.Attach's Subscribe shim resolves the channel's
		// declared gobFormat and merges the topic var into ONE struct
		// internally (ChannelHandle.DecodeMergedWithFormats).
		_ = client.Subscribe(ctx, sensorSubscriber, func(_ context.Context, r SensorReading) error {
			received <- r
			return nil
		})
	}()
	mock.waitForSubscription("sensors/+/readings", time.Second)

	fmt.Println("=== Publisher: Client.Attach encodes the Gob payload AND derives the topic ===")

	reading := SensorReading{
		Meta:  SensorMeta{SensorID: uuid.MustParse("f47ac10b-58cc-4372-a567-0e02b2c3d479")},
		Value: 22.5,
	}
	// Same story on publish: no manual codex.EncodeVars/BuildTopic/
	// gobFormat.Marshal — Client.Attach's Publish shim derives the topic
	// from Meta.SensorID and encodes the payload with the declared
	// gobFormat internally (ChannelHandle.EncodeWithFormats).
	if err := client.Publish(ctx, sensorPublisher, reading); err != nil {
		fmt.Fprintln(os.Stderr, "publish:", err)
		os.Exit(1)
	}

	select {
	case r := <-received:
		fmt.Printf("received: %+v\n", r)
		fmt.Println("(received.Meta.SensorID was merged from the topic, received.Value decoded from the Gob body — both via Client.Attach, zero manual encode/decode calls)")
	case <-ctx.Done():
		fmt.Fprintln(os.Stderr, "timed out waiting for the subscriber to receive the reading")
		os.Exit(1)
	}
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
