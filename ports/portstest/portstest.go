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

// ── Scenario 2: SinkAdapter round trip ──────────────────────────────────────

// AssertSinkAdapterRoundTrip proves port (an already-constructed
// [ports.SinkPort] with its adapter ALREADY [ports.SinkPort.Bind]-ed by
// the caller) correctly delivers [WantReadings] — the SAME canonical
// item set [AssertSourceAdapterRoundTrip] uses, kept consistent across
// this package's scenarios since a Sink is simply a Source's round trip
// run in the opposite direction — via [ports.SinkPort.Feed].
//
// Takes an already-bound port, rather than constructing one internally
// (unlike [AssertSourceAdapterRoundTrip]/[AssertIOAdapterRoundTrip]):
// some [ports.SinkAdapter] implementations (e.g.
// [[websocket.BroadcastSocketAdapter]]) need a port-DERIVED handle (via
// `port.PluginSocketPattern`) at CONSTRUCTION time, before Bind — so the
// caller must own the port→handle→adapter→Bind sequence itself; this
// function only runs the shared Feed+verify half once that's done.
//
// Unlike [AssertSourceAdapterRoundTrip] (which can observe delivery
// directly, by collecting FROM the port's own stream), a Sink's
// delivery target is adapter-specific — a file, a live socket broadcast,
// a cache write — so this function cannot verify WHAT was delivered
// itself. verify is called AFTER [ports.SinkPort.Feed] returns (which
// blocks until the fed stream terminates AND the adapter has finished
// draining it — [ports.SinkPort.Feed]'s own documented contract) —
// callers assert delivery via their own adapter-specific capture
// mechanism (e.g. reading back a temp file's contents, inspecting a mock
// socket's captured sent frames) inside verify.
func AssertSinkAdapterRoundTrip(t *testing.T, port *ports.SinkPort[Reading], verify func(t *testing.T)) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	ch := make(chan Reading, len(WantReadings))
	for _, r := range WantReadings {
		ch <- r
	}
	close(ch)
	port.Feed(ctx, gstream.From(ctx, ch))

	verify(t)
}

// ── Scenario 3: IOAdapter round trip ────────────────────────────────────────

// WantReadingRequests is the canonical "lookup by ID" request set
// [AssertIOAdapterRoundTrip] feeds through [ports.IOPort] — the SAME IDs
// as [WantReadings], with Value left at its zero value: the caller only
// supplies the ID, and a conforming [ports.IOAdapter] looks up/computes
// the real Value, returning the full [WantReadings] record — exercising
// a genuine "enrichment" round trip (SQL query by ID, LLM call echoing a
// looked-up fact), not a trivial identity transform.
var WantReadingRequests = []Reading{
	{ID: "reading-1"},
	{ID: "reading-2"},
}

// AssertIOAdapterRoundTrip proves adapter (constructed by the caller to
// resolve [WantReadingRequests] into [WantReadings], e.g. via a SQL
// lookup-by-ID query function or a mocked LLM call) correctly
// round-trips through [ports.IOPort]'s own binding lifecycle:
// [ports.IOPort.Bind] followed by [ports.IOPort.Connect], consumed via
// [gstream.Collect].
func AssertIOAdapterRoundTrip(t *testing.T, reqCodec, respCodec codex.Codec[Reading], adapter ports.IOAdapter[Reading, Reading]) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	p, err := ports.NewIOPort[Reading, Reading]("portstest-io", reqCodec, respCodec, ports.PortOptions{})
	if err != nil {
		t.Fatalf("NewIOPort: %v", err)
	}
	if err := p.Bind(ctx, adapter); err != nil {
		t.Fatalf("Bind: %v", err)
	}

	ch := make(chan Reading, len(WantReadingRequests))
	for _, r := range WantReadingRequests {
		ch <- r
	}
	close(ch)
	vals, errs := gstream.Collect(ctx, p.Connect(ctx, gstream.From(ctx, ch)))
	if len(errs) != 0 {
		t.Fatalf("want 0 errors, got %d: %v", len(errs), errs)
	}
	if !slices.Equal(vals, WantReadings) {
		t.Errorf("collected responses = %+v, want %+v", vals, WantReadings)
	}
}
