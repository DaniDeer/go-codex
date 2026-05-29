// Package stats-observer-mqtt demonstrates how to wire a [stats.Observer] into
// both the subscribe and publish paths of the MQTT adapter — without connecting
// to a real broker.
//
// Two observer hooks are exercised:
//
//   - [RecordSubscribe] — called once per [SubscribeHandler] invocation with
//     the topic, success flag, and processing duration.
//
//   - [RecordPublish] — called once per [Publish] call with the resolved topic,
//     success flag (false on broker error or context cancel), and duration.
//
//   - [RecordValidationError] — called for each field-level codec failure during
//     payload decode (subscribe, location="payload") or encode (publish,
//     location="payload"), with the constraint name and field.
//
// The example uses an in-memory mock client and mock messages so it runs without
// any network access or broker setup.
//
// Run with: go run ./examples/stats-observer-mqtt
package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"

	adaptermqtt "github.com/DaniDeer/go-codex/adapters/mqtt"
	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/validate"
)

// ── Domain model ─────────────────────────────────────────────────────────────

type sensorReading struct {
	SensorID string  `json:"sensor_id"`
	Value    float64 `json:"value"`
}

var sensorReadingCodec = codex.Struct[sensorReading](
	codex.Field[sensorReading, string]{
		Name:     "sensor_id",
		Codec:    codex.String().Refine(validate.UUID),
		Required: true,
		Get:      func(r sensorReading) string { return r.SensorID },
		Set:      func(r *sensorReading, v string) { r.SensorID = v },
	},
	codex.Field[sensorReading, float64]{
		Name:     "value",
		Codec:    codex.Float64(),
		Required: true,
		Get:      func(r sensorReading) float64 { return r.Value },
		Set:      func(r *sensorReading, v float64) { r.Value = v },
	},
)

// ── Observer ─────────────────────────────────────────────────────────────────

// MQTTObserver tracks subscribe and publish lifecycle events separately.
// A production implementation would use separate *prometheus.CounterVec fields
// for subscribed and published messages.
type MQTTObserver struct {
	mu         sync.Mutex
	subscribed []messageEvent
	published  []messageEvent
	valErrors  []valError
}

type messageEvent struct {
	topic   string
	success bool
	elapsed time.Duration
}

type valError struct {
	location   string
	constraint string
	field      string
}

func (o *MQTTObserver) RecordRequest(_, _ string, _ int, _ time.Duration) {}

func (o *MQTTObserver) RecordSubscribe(topic string, success bool, d time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.subscribed = append(o.subscribed, messageEvent{topic: topic, success: success, elapsed: d})
	status := "ok"
	if !success {
		status = "fail"
	}
	fmt.Printf("  [observer] RecordSubscribe topic=%q success=%s elapsed=%v\n",
		topic, status, d.Round(time.Microsecond))
}

func (o *MQTTObserver) RecordPublish(topic string, success bool, d time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.published = append(o.published, messageEvent{topic: topic, success: success, elapsed: d})
	status := "ok"
	if !success {
		status = "fail"
	}
	fmt.Printf("  [observer] RecordPublish   topic=%q success=%s elapsed=%v\n",
		topic, status, d.Round(time.Microsecond))
}

func (o *MQTTObserver) RecordValidationError(location, constraint, field string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.valErrors = append(o.valErrors, valError{location: location, constraint: constraint, field: field})
	fmt.Printf("  [observer] RecordValidationError location=%q constraint=%q field=%q\n",
		location, constraint, field)
}

func (o *MQTTObserver) Print() {
	o.mu.Lock()
	defer o.mu.Unlock()
	subOK, subFail := countSuccess(o.subscribed)
	pubOK, pubFail := countSuccess(o.published)
	fmt.Printf("  subscribed : %d total (%d ok, %d fail)\n", len(o.subscribed), subOK, subFail)
	fmt.Printf("  published  : %d total (%d ok, %d fail)\n", len(o.published), pubOK, pubFail)
	fmt.Printf("  val errors : %d\n", len(o.valErrors))
}

func countSuccess(events []messageEvent) (ok, fail int) {
	for _, e := range events {
		if e.success {
			ok++
		} else {
			fail++
		}
	}
	return
}

// ── Broker mocks (no network required) ───────────────────────────────────────

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

type completedToken struct {
	err  error
	done chan struct{}
}

func newToken(err error) *completedToken {
	t := &completedToken{err: err, done: make(chan struct{})}
	close(t.done)
	return t
}

func (t *completedToken) Wait() bool                       { return true }
func (t *completedToken) WaitTimeout(_ time.Duration) bool { return true }
func (t *completedToken) Done() <-chan struct{}            { return t.done }
func (t *completedToken) Error() error                     { return t.err }

type mockClient struct {
	token pahomqtt.Token
}

func (c *mockClient) IsConnected() bool      { return true }
func (c *mockClient) IsConnectionOpen() bool { return true }
func (c *mockClient) Connect() pahomqtt.Token {
	return newToken(nil)
}
func (c *mockClient) Disconnect(_ uint) {}
func (c *mockClient) Publish(_ string, _ byte, _ bool, _ interface{}) pahomqtt.Token {
	return c.token
}
func (c *mockClient) Subscribe(_ string, _ byte, _ pahomqtt.MessageHandler) pahomqtt.Token {
	return newToken(nil)
}
func (c *mockClient) SubscribeMultiple(_ map[string]byte, _ pahomqtt.MessageHandler) pahomqtt.Token {
	return newToken(nil)
}
func (c *mockClient) Unsubscribe(_ ...string) pahomqtt.Token       { return newToken(nil) }
func (c *mockClient) AddRoute(_ string, _ pahomqtt.MessageHandler) {}
func (c *mockClient) OptionsReader() pahomqtt.ClientOptionsReader {
	return pahomqtt.ClientOptionsReader{}
}

// ── Main ──────────────────────────────────────────────────────────────────────

func main() {
	b := events.NewBuilder(events.Info{Title: "Sensor API", Version: "1.0.0"})
	readingChannel, err := events.AddChannel[sensorReading](b, "sensors/readings", sensorReadingCodec,
		events.ChannelConfig{
			Subscribe: &events.OperationConfig{Summary: "Ingest a sensor reading"},
			Publish:   &events.OperationConfig{Summary: "Publish a sensor reading"},
		},
	)
	if err != nil {
		panic(err)
	}

	obs := &MQTTObserver{}
	subOpts := adaptermqtt.SubscribeOptions{
		Observer: obs,
		OnError: func(e adaptermqtt.SubscribeError) {
			fmt.Printf("  subscribe error [%s]: %v\n", e.Kind, e.Err)
		},
	}
	pubOpts := adaptermqtt.PublishOptions{Observer: obs}

	// ── Subscribe path ────────────────────────────────────────────────────────

	handler := adaptermqtt.SubscribeHandler(context.Background(), readingChannel,
		func(_ context.Context, r sensorReading) error {
			fmt.Printf("  handler received: sensor_id=%q value=%.2f\n", r.SensorID, r.Value)
			return nil
		}, subOpts,
	)

	// Message 1 (subscribe): valid payload → RecordSubscribe(success=true)
	fmt.Println("=== Subscribe: valid payload ===")
	handler(nil, &mockMessage{
		topic:   "sensors/readings",
		payload: []byte(`{"sensor_id":"f47ac10b-58cc-4372-a567-0e02b2c3d479","value":42.5}`),
	})

	// Message 2 (subscribe): invalid payload — bad UUID + missing value →
	// RecordValidationError("payload", ...) for each failing field, then
	// RecordSubscribe(success=false)
	fmt.Println("=== Subscribe: invalid payload (bad UUID, missing value) ===")
	handler(nil, &mockMessage{
		topic:   "sensors/readings",
		payload: []byte(`{"sensor_id":"not-a-uuid"}`),
	})

	// ── Publish path ──────────────────────────────────────────────────────────

	// Message 3 (publish): broker acks → RecordPublish(success=true)
	fmt.Println("=== Publish: broker ack ===")
	reading := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 23.1}
	if err := adaptermqtt.Publish(context.Background(),
		&mockClient{token: newToken(nil)},
		readingChannel, 1, false, reading, nil, pubOpts,
	); err != nil {
		fmt.Printf("  publish error: %v\n", err)
	}

	// Message 4 (publish): broker returns error → RecordPublish(success=false)
	fmt.Println("=== Publish: broker error ===")
	brokerErr := errors.New("broker unavailable")
	if err := adaptermqtt.Publish(context.Background(),
		&mockClient{token: newToken(brokerErr)},
		readingChannel, 1, false, reading, nil, pubOpts,
	); err != nil {
		fmt.Printf("  publish error: %v\n", err)
	}

	fmt.Println("\n=== Observer summary ===")
	obs.Print()
}
