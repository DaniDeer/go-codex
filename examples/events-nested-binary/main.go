// Package events-nested-binary demonstrates the Phase 2 "one struct, one
// call" merge-field convenience for MQTT/event channels (api/events +
// adapters/mqtt5) — the pub/sub mirror of examples/rest-nested-binary.
//
// Two things worth proving here, exactly like the REST case:
//
//  1. Nested struct composition — the payload is built from sub-structs
//     (Meta for the topic-derived field, Value for the body) instead of
//     flat top-level fields. events.NewTopicParam's get/set are plain
//     closures, so nested access needs zero framework changes.
//  2. Non-JSON payload formats — format.Gob composes with topic merge
//     fields exactly like JSON/YAML/TOML would, since payload decode/
//     encode is completely orthogonal to var-merge.
//
// This example is transport-agnostic — it calls the same primitives
// adapters/mqtt5's Subscribe/PublishHandle call internally
// (ChannelHandle.DecodeMerged and codex.EncodeVars via MergeFields), so it
// runs without a broker connection. See adapters/mqtt5/adapter_test.go's
// TestPublishHandleSubscribe_NestedGobPayload_RoundTrip for the full
// live-wiring version (via Subscribe/PublishHandle against a fake broker).
//
// Run with: go run ./examples/events-nested-binary
package main

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"os"

	"github.com/google/uuid"

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
// a merge-capable topic param targeting the NESTED Meta.SensorID field.
var sensorChannel = events.NewChannel[SensorReading]("sensors/{sensorID}/readings", readingCodec,
	events.NewTopicParam("sensorID", codex.TextCodec[uuid.UUID](),
		func(r SensorReading) uuid.UUID { return r.Meta.SensorID },
		func(r *SensorReading, v uuid.UUID) { r.Meta.SensorID = v },
	).WithDescription("Sensor ID (UUID) — merged from the topic, never the payload"),
)

func main() {
	b := events.NewBuilder(events.Info{Title: "Sensor Events API", Version: "1.0.0"})
	handle, err := sensorChannel.Register(b)
	if err != nil {
		fmt.Fprintln(os.Stderr, "register:", err)
		os.Exit(1)
	}
	handle.WithFormats(gobFormat)

	// ── Publisher side: one struct, encode the payload + derive the topic ──
	fmt.Println("=== Publisher: one struct in, Gob body + topic derived automatically ===")

	reading := SensorReading{
		Meta:  SensorMeta{SensorID: uuid.MustParse("f47ac10b-58cc-4372-a567-0e02b2c3d479")},
		Value: 22.5,
	}

	// This is exactly what adapters/mqtt5.PublishHandle does internally —
	// derive the topic vars from the SAME struct passed to Marshal, no
	// manual vars map needed.
	vars, err := codex.EncodeVars(reading, handle.MergeFields()...)
	if err != nil {
		fmt.Fprintln(os.Stderr, "encode vars:", err)
		os.Exit(1)
	}
	topic, err := handle.BuildTopic(vars)
	if err != nil {
		fmt.Fprintln(os.Stderr, "build topic:", err)
		os.Exit(1)
	}
	payload, err := gobFormat.Marshal(reading)
	if err != nil {
		fmt.Fprintln(os.Stderr, "marshal payload:", err)
		os.Exit(1)
	}
	fmt.Printf("topic:   %s\n", topic)
	fmt.Printf("payload: %d bytes (binary — SensorID is NOT in these bytes)\n\n", len(payload))

	// ── Subscriber side: one struct out, payload + topic merged automatically ──
	fmt.Println("=== Subscriber: payload decoded AND topic var merged into ONE struct ===")

	// This is exactly what adapters/mqtt5.Subscribe does internally when
	// the channel declares merge fields: decode the payload via the
	// registered format (gobFormat here — ChannelHandle.DecodeMerged
	// itself always uses the default JSON Decode, so a non-default format
	// must be applied first, same as REST's DecodeMerged/multi-format
	// split), then merge topic vars into the SAME value via
	// codex.DecodeVars — no manual TopicVarsFromMessage call needed in the
	// handler function.
	received, err := gobFormat.Unmarshal(payload)
	if err != nil {
		fmt.Fprintln(os.Stderr, "unmarshal payload:", err)
		os.Exit(1)
	}
	if err := codex.DecodeVars(&received, vars, handle.MergeFields()...); err != nil {
		fmt.Fprintln(os.Stderr, "merge topic vars:", err)
		os.Exit(1)
	}
	fmt.Printf("received: %+v\n", received)
	fmt.Println("(received.Meta.SensorID was merged from the topic, received.Value decoded from the Gob body)")
}
