// Package adapters-zeromq demonstrates the ZeroMQ PUB/SUB adapter using
// go-codex's api/events channel declarations, via the inverted-control
// Client.Attach/.Publish/.Subscribe workflow (Decision 5 of
// docs/design/d-0002-pubsub-workflow-simplification.md).
//
// The example wires together a publisher and a subscriber in-process using
// a mock FramedSocket to avoid requiring a real libzmq installation. In
// production, replace mockSocket with the pebbe/zmq4 wrapper shown in
// docs/guides/zeromq.md.
//
// The workflow, end to end:
//  1. SensorReading codec       (Layer 1 — domain type)
//  2. events.NewChannel         (Layer 2 — AsyncAPI contract: topic + codec + merge-capable topic param)
//  3. .WithSubscribe/.WithPublish — fork the shared declaration into its two roles;
//     each declares its own requirements (operation metadata, security if any)
//  4. events.NewClient          (Layer 2 — spec/registry)
//  5. zeromq.Attach(client, sock) — ATTACH the adapter to the client: the
//     one place transport specifics (the socket) enter the picture
//  6. client.Subscribe(ctx, sub, fn) / client.Publish(ctx, pub, msg) — the
//     adapter FULFILLS each declaration's requirements when called; no
//     package-qualified zeromq.Subscribe/zeromq.Publish call needed at the
//     actual usage site anymore, only at Attach time
//
// Already the reference example for this workflow before Decision 8/9
// existed: obs (stats.NewLoggingObserver) is stored on ctx via
// stats.WithObserver ONCE, so Client.Attach's Publish/Subscribe already
// routed through it correctly (Decision 8's Observer fix, confirmed with
// zero changes needed here) — same for the channel's declared format
// (Decision 9's centralized-resolution fix), which this example's plain
// JSON codec doesn't exercise directly but which now applies identically
// whether called via Client.Attach or the escape-hatch primitives.
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

// ReadingsChannel declares SensorID as a MERGE-CAPABLE topic param (via
// events.NewTopicParam, not the validate-only events.TopicParam) — this is
// what lets Client.Publish derive the topic ("sensors/{sensorID}/readings"
// → "sensors/<uuid>/readings") directly from the SensorReading VALUE, with
// no separate vars map at the call site: the "one struct, one call"
// promise this codebase's declarative APIs make everywhere.
// readingsSubscribeMeta/readingsPublishMeta are declared once and reused on
// BOTH the base channel (inline) and each role-scoped fork below — see the
// comment on ReadingsSubscriber/ReadingsPublisher for why.
var readingsSubscribeMeta = events.Subscribe{
	OperationID: "receiveSensorReading",
	Summary:     "Receive a sensor reading from the ZMQ broker.",
}

var readingsPublishMeta = events.Publish{
	OperationID: "publishSensorReading",
	Summary:     "Publish a sensor reading to the ZMQ broker.",
}

var ReadingsChannel = events.NewChannel[SensorReading](
	"sensors/{sensorID}/readings",
	sensorCodec,
	events.NewTopicParam("sensorID", codex.String().Refine(validate.UUID),
		func(r SensorReading) string { return r.SensorID },
		func(r *SensorReading, v string) { r.SensorID = v },
	).WithDescription("UUID of the sensor producing the reading."),
	readingsSubscribeMeta,
	readingsPublishMeta,
)

// ReadingsSubscriber/ReadingsPublisher fork ReadingsChannel's shared
// topic/codec/TopicParam declaration into its two roles — see
// [events.Channel.WithSubscribe]/[events.Channel.WithPublish]. Each
// REDECLARES the SAME metadata already set inline on ReadingsChannel above
// (harmless — identical content, last-applied wins) so that WHICHEVER role
// registers its spec into the shared Client FIRST (Client.Subscribe runs in
// a background goroutine here, so the order isn't deterministic) already
// carries BOTH operations — Client's spec-registry dedups by topic,
// first-registered-wins on CONTENT, so a role registering with only ITS
// OWN operation populated would silently leave the other one out of the
// printed AsyncAPI spec.
var ReadingsSubscriber = ReadingsChannel.WithSubscribe(readingsSubscribeMeta)

var ReadingsPublisher = ReadingsChannel.WithPublish(readingsPublishMeta)

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

	// Layer 2: Client owns the spec/registry — declarations register into
	// it lazily, the first time Client.Publish/Subscribe is called for
	// each one (no separate up-front .Handle(client) call needed).
	client := events.NewClient(events.WithInfo(events.Info{Title: "Sensor Network", Version: "1.0.0"}))
	client.AddServer("zmq", events.Server{
		URL:         "tcp://broker:5555",
		Protocol:    "zmq",
		Description: "ZeroMQ broker for sensor readings",
	})

	// Shared in-process pipe replaces a real ZMQ PUB ↔ SUB socket pair.
	pipe := newPipe()

	// ATTACH the adapter to the client — the one place transport specifics
	// (the socket) enter the picture. From here on, client.Publish/
	// client.Subscribe fulfill each declaration's requirements without any
	// further zeromq.* package-qualified calls at the usage site.
	if err := zeromq.Attach(client, pipe); err != nil {
		fmt.Fprintf(os.Stderr, "attach: %v\n", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	ctx = stats.WithObserver(ctx, obs) // default observer for all adapter calls
	defer cancel()

	// client.Subscribe fulfills ReadingsSubscriber's declared requirements
	// (topic template, codec) — runs in a background goroutine, blocking
	// until ctx is cancelled.
	received := make(chan SensorReading, 4)
	go func() {
		_ = client.Subscribe(ctx, ReadingsSubscriber, func(_ context.Context, r SensorReading) error {
			fmt.Printf("received: sensorID=%s value=%.1f\n", r.SensorID, r.Value)
			received <- r
			return nil
		})
	}()

	// client.Publish fulfills ReadingsPublisher's declared requirements —
	// ONE struct in (r), no separate vars map: the topic
	// "sensors/{sensorID}/readings" is derived automatically from r.SensorID
	// via ReadingsChannel's merge-capable topic param.
	sensorID := "f47ac10b-58cc-4372-a567-0e02b2c3d479"
	readings := []SensorReading{
		{SensorID: sensorID, Value: 22.5},
		{SensorID: sensorID, Value: 23.1},
		{SensorID: sensorID, Value: 21.8},
	}
	for _, r := range readings {
		if err := client.Publish(ctx, ReadingsPublisher, r); err != nil {
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

	// Print the AsyncAPI spec (protocol: zmq) — populated lazily by the
	// client.Publish/client.Subscribe calls above (each internally calls
	// the same Handle/Register machinery a manual .Handle(client) would).
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
	fmt.Println("\n── AsyncAPI spec (zmq) ──")
	fmt.Println(string(specYAML))
}
