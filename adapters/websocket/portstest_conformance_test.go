package websocket_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	adapterws "github.com/DaniDeer/go-codex/adapters/websocket"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/ports"
	"github.com/DaniDeer/go-codex/ports/portstest"
)

// TestPortstestConformance_SinkAdapterRoundTrip runs
// [adapterws.BroadcastSocketAdapter] against ports/portstest's canonical
// SinkAdapter conformance scenario — this package's own, concrete proof
// that it satisfies ports.SinkAdapter's contract, independent of this
// package's own hand-written tests. D-0010's "API-defined conformance
// test suite" mechanism, ports-family instance — BroadcastSocketAdapter
// is the FIRST adapter outside ports/portstest's original sql/file pair
// to prove a conformance scenario, and the first adapter to prove the
// SinkAdapter scenario specifically (alongside adapters/file).
//
// Unlike adapters/file's io.Writer-based [adapterws.DrainWriteAdapter],
// [adapterws.BroadcastSocketAdapter] needs a port-DERIVED handle (via
// [ports.SinkPort.PluginSocketPattern]) at construction time, before
// Bind — so this test owns the full port→handle→adapter→Bind sequence
// itself (per [portstest.AssertSinkAdapterRoundTrip]'s own doc comment),
// reusing this package's existing fakeSocket/fakeUpgrader/connectMux
// test harness (the SAME one TestBroadcast_* uses) rather than a real
// network connection.
//
// Delivery is genuinely asynchronous here (unlike adapters/file's
// direct io.Writer write): Activate hands each item to the Hub's own
// per-session `out` channel, drained by a SEPARATE writer goroutine —
// so verify polls via waitFor rather than asserting immediately after
// Feed returns.
func TestPortstestConformance_SinkAdapterRoundTrip(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mux := http.NewServeMux()
	hub := adapterws.NewHub(0)
	sock := newFakeSocket()
	up := &fakeUpgrader{socks: []*fakeSocket{sock}}

	port, err := ports.NewSinkPort[portstest.Reading]("portstest-ws-sink", portstest.ReadingCodec, ports.PortOptions{Buffer: 8})
	if err != nil {
		t.Fatalf("NewSinkPort: %v", err)
	}
	handle, err := port.PluginSocketPattern(ports.SocketPattern{Path: "/portstest-sink"})
	if err != nil {
		t.Fatalf("PluginSocketPattern: %v", err)
	}
	adapter := codex.Must(adapterws.BroadcastSocketAdapter(mux, hub, up, handle, adapterws.BroadcastSocketAdapterOptions{}))
	port.Bind(ctx, adapter)

	// Let Activate's mux.Handle registration land, then connect the
	// scripted client socket, then let the Hub finish registering it —
	// mirrors every existing TestBroadcast_* test's exact timing.
	time.Sleep(20 * time.Millisecond)
	connectMux(mux, "/portstest-sink")
	time.Sleep(20 * time.Millisecond)

	portstest.AssertSinkAdapterRoundTrip(t, port, func(t *testing.T) {
		t.Helper()
		waitFor(t, func() bool { return len(sock.writtenFrames()) >= len(portstest.WantReadings) })

		frames := sock.writtenFrames()
		if len(frames) != len(portstest.WantReadings) {
			t.Fatalf("want %d written frames, got %d: %q", len(portstest.WantReadings), len(frames), frames)
		}
		fmt := format.JSON(portstest.ReadingCodec)
		for i, frame := range frames {
			got, err := fmt.Unmarshal(frame)
			if err != nil {
				t.Fatalf("unmarshal frame %d: %v", i, err)
			}
			if got != portstest.WantReadings[i] {
				t.Errorf("frame %d = %+v, want %+v", i, got, portstest.WantReadings[i])
			}
		}
	})
}
