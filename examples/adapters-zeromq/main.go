// Package adapters-zeromq demonstrates the ZeroMQ PUB/SUB adapter using
// go-codex's api/events channel declarations.
//
// The example wires together a publisher and a subscriber in-process using
// a mock FramedSocket to avoid requiring a real libzmq installation. In
// production, replace mockSocket with the pebbe/zmq4 wrapper shown in
// docs/guides/zeromq.md.
//
// Pattern: PUB/SUB sensor readings
//
//	SensorReading codec  (Layer 1 — domain type)
//	events.NewChannel    (Layer 2 — AsyncAPI contract)
//	zeromq.Publish       (Layer 3 — ZMQ PUB socket adapter)
//	zeromq.Subscribe     (Layer 3 — ZMQ SUB socket adapter)
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	zeromq "github.com/DaniDeer/go-codex/adapters/zeromq"
	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/stats"
	"github.com/DaniDeer/go-codex/validate"
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

// ── Layer 2: channel declaration ──────────────────────────────────────────────

var ReadingsChannel = events.NewChannel[SensorReading](
	"sensors/{sensorID}/readings",
	sensorCodec,
	events.Subscribe{
		OperationID: "receiveSensorReading",
		Summary:     "Receive a sensor reading from the ZMQ broker.",
	},
	events.Publish{
		OperationID: "publishSensorReading",
		Summary:     "Publish a sensor reading to the ZMQ broker.",
	},
	events.TopicParam{
		Name:        "sensorID",
		Description: "UUID of the sensor producing the reading.",
	}.WithCodec(codex.String().Refine(validate.UUID)),
)

// ── in-process mock socket (replaces pebbe/zmq4 in this demo) ────────────────

// pipeSocket is a simple in-process socket backed by a channel of frames.
// It implements zeromq.FramedSocket without any ZMQ library dependency.
type pipeSocket struct {
	pipe chan [][]byte
}

func newPipe() *pipeSocket { return &pipeSocket{pipe: make(chan [][]byte, 16)} }

func (s *pipeSocket) SendFrames(frames [][]byte) error {
	cp := make([][]byte, len(frames))
	for i, f := range frames {
		cp[i] = append([]byte{}, f...)
	}
	s.pipe <- cp
	return nil
}

func (s *pipeSocket) RecvFrames() ([][]byte, error) {
	select {
	case frames := <-s.pipe:
		return frames, nil
	case <-time.After(100 * time.Millisecond):
		return nil, zeromq.ErrTimeout
	}
}

func (s *pipeSocket) SetSubscription(_ string) error       { return nil }
func (s *pipeSocket) SetRecvTimeout(_ time.Duration) error { return nil }

// ── main ──────────────────────────────────────────────────────────────────────

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	obs := stats.NewLoggingObserver(logger)

	// Layer 2: register channel with builder — builder generates AsyncAPI spec.
	builder := events.NewBuilder(events.Info{Title: "Sensor Network", Version: "1.0.0"})
	// Store obs once in the context — every adapter call that receives this ctx
	// will pick it up automatically when Options.Observer is nil.
	builder.AddServer("zmq", events.Server{
		URL:         "tcp://broker:5555",
		Protocol:    "zmq",
		Description: "ZeroMQ broker for sensor readings",
	})
	pubHandle, err := ReadingsChannel.Register(builder)
	if err != nil {
		fmt.Fprintf(os.Stderr, "register: %v\n", err)
		os.Exit(1)
	}
	subHandle, err := ReadingsChannel.Register(builder)
	if err != nil {
		fmt.Fprintf(os.Stderr, "register: %v\n", err)
		os.Exit(1)
	}

	// Shared in-process pipe replaces a real ZMQ PUB ↔ SUB socket pair.
	pipe := newPipe()

	// Layer 3: subscriber — runs in background goroutine.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	ctx = stats.WithObserver(ctx, obs) // default observer for all adapter calls
	defer cancel()

	received := make(chan SensorReading, 4)
	go func() {
		_ = zeromq.Subscribe(ctx, pipe, subHandle,
			func(_ context.Context, r SensorReading) error {
				fmt.Printf("received: sensorID=%s value=%.1f\n", r.SensorID, r.Value)
				received <- r
				return nil
			},
			zeromq.SubscribeOptions{}, // observer resolved from ctx
		)
	}()

	// Layer 3: publisher — sends three readings.
	sensorID := "f47ac10b-58cc-4372-a567-0e02b2c3d479"
	readings := []SensorReading{
		{SensorID: sensorID, Value: 22.5},
		{SensorID: sensorID, Value: 23.1},
		{SensorID: sensorID, Value: 21.8},
	}
	for _, r := range readings {
		if err := zeromq.Publish(ctx, pipe, pubHandle, r,
			map[string]string{"sensorID": sensorID},
			zeromq.PublishOptions{}, // observer resolved from ctx
		); err != nil {
			fmt.Fprintf(os.Stderr, "publish: %v\n", err)
			os.Exit(1)
		}
	}

	// Wait for all readings to be received.
	for i := 0; i < len(readings); i++ {
		select {
		case <-received:
		case <-ctx.Done():
			fmt.Fprintln(os.Stderr, "timeout waiting for readings")
			os.Exit(1)
		}
	}

	// Print the AsyncAPI spec (protocol: zmq).
	doc, err := builder.AsyncAPISpec()
	if err != nil {
		fmt.Fprintf(os.Stderr, "spec: %v\n", err)
		os.Exit(1)
	}
	specYAML, err := doc.MarshalYAML()
	if err != nil {
		fmt.Fprintf(os.Stderr, "marshal spec: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("\n── AsyncAPI spec (zmq) ──")
	fmt.Println(string(specYAML))
}
