package portstest

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/ports"
	gstream "github.com/DaniDeer/go-codex/stream"
)

// Reading is the canonical item type [AssertSourceAdapterRoundTrip]
// round-trips through [ports.SourcePort] — deliberately the SAME
// ID/Value shape convention already established in
// api/events/eventstest's SensorReading, kept consistent across this
// roadmap's conformance packages.
type Reading struct {
	ID    string
	Value float64
}

// ReadingCodec is [Reading]'s codec.
var ReadingCodec = codex.Struct[Reading](
	codex.RequiredField("id", codex.String(),
		func(r Reading) string { return r.ID },
		func(r *Reading, v string) { r.ID = v },
	),
	codex.RequiredField("value", codex.Float64(),
		func(r Reading) float64 { return r.Value },
		func(r *Reading, v float64) { r.Value = v },
	),
)

// WantReadings is the fixed, canonical item set every conforming
// [ports.SourceAdapter] test constructs its own adapter to emit — see
// [AssertSourceAdapterRoundTrip]'s doc comment for how an adapter that
// emits this set repeatedly (rather than exactly once) is still
// correctly asserted.
var WantReadings = []Reading{
	{ID: "reading-1", Value: 1.5},
	{ID: "reading-2", Value: 2.5},
}

// AssertSourceAdapterRoundTrip proves adapter (constructed by the caller
// to emit [WantReadings], using codec to encode/decode [Reading] values)
// correctly round-trips through [ports.SourcePort]'s own binding
// lifecycle: [ports.SourcePort.Bind] followed by
// [ports.SourcePort.Stream], consumed via [gstream.Collect] — the SAME
// idiom adapters/sql's own existing SourcePort tests already use.
//
// Asserts the FIRST len(want) collected items equal want exactly — not
// that exactly len(want) items were collected overall — since some
// adapters (e.g. adapters/file.ScanAdapter) emit their items exactly
// once then terminate, while others (e.g. adapters/sql.QueryAdapter)
// poll continuously and re-emit the SAME items every tick. Both shapes
// are conforming; this assertion accommodates both without forcing a
// false assumption about either.
func AssertSourceAdapterRoundTrip(t *testing.T, codec codex.Codec[Reading], adapter ports.SourceAdapter[Reading], want []Reading) {
	t.Helper()

	p, err := ports.NewSourcePort[Reading]("portstest-source", codec, ports.PortOptions{Buffer: 8})
	if err != nil {
		t.Fatalf("NewSourcePort: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	p.Bind(ctx, adapter)
	vals, errs := gstream.Collect(ctx, p.Stream(ctx))
	if len(errs) != 0 {
		t.Fatalf("want 0 errors, got %d: %v", len(errs), errs)
	}
	if len(vals) < len(want) {
		t.Fatalf("want >= %d collected items, got %d: %+v", len(want), len(vals), vals)
	}
	got := vals[:len(want)]
	if !slices.Equal(got, want) {
		t.Errorf("first %d collected items = %+v, want %+v", len(want), got, want)
	}
}
