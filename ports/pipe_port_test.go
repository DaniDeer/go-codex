package ports_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/ports"
	"github.com/DaniDeer/go-codex/stats"
	gstream "github.com/DaniDeer/go-codex/stream"
)

// ── PP-01: PipePort construction ────────────────────────────────────────────

func TestPipePort_NewPipePort_Valid(t *testing.T) {
	pp, err := ports.NewPipePort[int]("broadcast", intCodec, ports.PortOptions{Buffer: 16})
	if err != nil {
		t.Fatalf("NewPipePort: %v", err)
	}
	if pp.Name() != "broadcast" {
		t.Fatalf("want name 'broadcast', got %s", pp.Name())
	}
}

// ── PP-02: InputPort returns same instance for same name ────────────────────

func TestPipePort_InputPort_SameName(t *testing.T) {
	pp, _ := ports.NewPipePort[int]("test", intCodec, ports.PortOptions{Buffer: 8})
	in1 := pp.InputPort("mqtt")
	in2 := pp.InputPort("mqtt")
	if in1 != in2 {
		t.Fatal("InputPort with same name must return same port")
	}
}

// ── PP-03: OutputPort returns same instance for same name ───────────────────

func TestPipePort_OutputPort_SameName(t *testing.T) {
	pp, _ := ports.NewPipePort[int]("test", intCodec, ports.PortOptions{Buffer: 8})
	out1 := pp.OutputPort("sse")
	out2 := pp.OutputPort("sse")
	if out1 != out2 {
		t.Fatal("OutputPort with same name must return same port")
	}
}

// ── PP-04: InputPort and OutputPort names are scoped independently ──────────

func TestPipePort_InputOutput_Scoped(t *testing.T) {
	pp, _ := ports.NewPipePort[int]("test", intCodec, ports.PortOptions{Buffer: 8})
	in := pp.InputPort("same")
	out := pp.OutputPort("same")
	if in.Name() == out.Name() {
		t.Fatal("InputPort and OutputPort with same local name have different full names")
	}
	// Verify full names are qualified differently.
	wantIn := "test/in/same"
	wantOut := "test/out/same"
	if in.Name() != wantIn {
		t.Fatalf("want input port name %s, got %s", wantIn, in.Name())
	}
	if out.Name() != wantOut {
		t.Fatalf("want output port name %s, got %s", wantOut, out.Name())
	}
}

// ── PP-05: single input → single output ─────────────────────────────────────

func TestPipePort_SingleInputSingleOutput(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pp, _ := ports.NewPipePort[int]("pipe", intCodec, ports.PortOptions{Buffer: 16})
	in := pp.InputPort("from-chan")
	out := pp.OutputPort("to-chan")

	// Wire adapters.
	inCh := feedChan(1, 2, 3)
	in.Bind(ctx, ports.ChanSourceAdapter(inCh))

	outCh := make(chan int, 8)
	out.Bind(ctx, ports.ChanSinkAdapter(outCh))

	// Connect the pipe.
	pp.Connect(ctx)

	// Wait for items to propagate.
	var got []int
	for i := 0; i < 3; i++ {
		v := <-outCh
		got = append(got, v)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 values, got %d: %v", len(got), got)
	}
}

// ── PP-06: fan-in: multiple inputs → single output ──────────────────────────

func TestPipePort_FanIn(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pp, _ := ports.NewPipePort[int]("pipe", intCodec, ports.PortOptions{Buffer: 16})
	in1 := pp.InputPort("mqtt")
	in2 := pp.InputPort("file")
	out := pp.OutputPort("to-sse")

	in1.Bind(ctx, ports.ChanSourceAdapter(feedChan(1, 2)))
	in2.Bind(ctx, ports.ChanSourceAdapter(feedChan(3, 4)))

	outCh := make(chan int, 8)
	out.Bind(ctx, ports.ChanSinkAdapter(outCh))

	pp.Connect(ctx)

	var got []int
	for i := 0; i < 4; i++ {
		v := <-outCh
		got = append(got, v)
	}
	if len(got) != 4 {
		t.Fatalf("want 4 values from fan-in, got %d: %v", len(got), got)
	}
}

// ── PP-07: fan-out: single input → multiple outputs ─────────────────────────

func TestPipePort_FanOut(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pp, _ := ports.NewPipePort[int]("pipe", intCodec, ports.PortOptions{Buffer: 16})
	in := pp.InputPort("from-chan")
	out1 := pp.OutputPort("to-mqtt")
	out2 := pp.OutputPort("to-sse")

	in.Bind(ctx, ports.ChanSourceAdapter(feedChan(7, 8, 9)))

	ch1 := make(chan int, 8)
	ch2 := make(chan int, 8)
	out1.Bind(ctx, ports.ChanSinkAdapter(ch1))
	out2.Bind(ctx, ports.ChanSinkAdapter(ch2))

	pp.Connect(ctx)

	var got1, got2 []int
	for i := 0; i < 3; i++ {
		v1 := <-ch1
		v2 := <-ch2
		got1 = append(got1, v1)
		got2 = append(got2, v2)
	}
	if len(got1) != 3 || len(got2) != 3 {
		t.Fatalf("want 3 items in both outputs, got %d and %d", len(got1), len(got2))
	}
}

// ── PP-08: fan-in + fan-out combined ────────────────────────────────────────

func TestPipePort_FanInFanOut(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pp, _ := ports.NewPipePort[int]("pipe", intCodec, ports.PortOptions{Buffer: 16})
	in1 := pp.InputPort("mqtt")
	in2 := pp.InputPort("file")
	out1 := pp.OutputPort("sse")
	out2 := pp.OutputPort("mqtt-out")

	in1.Bind(ctx, ports.ChanSourceAdapter(feedChan(1, 2)))
	in2.Bind(ctx, ports.ChanSourceAdapter(feedChan(3, 4)))

	ch1 := make(chan int, 8)
	ch2 := make(chan int, 8)
	out1.Bind(ctx, ports.ChanSinkAdapter(ch1))
	out2.Bind(ctx, ports.ChanSinkAdapter(ch2))

	pp.Connect(ctx)

	var got1, got2 []int
	for i := 0; i < 4; i++ {
		got1 = append(got1, <-ch1)
		got2 = append(got2, <-ch2)
	}
	if len(got1) != 4 || len(got2) != 4 {
		t.Fatalf("want 4 items in both outputs (fan-in × 2, fan-out × 2), got %d and %d", len(got1), len(got2))
	}
}

// ── PP-09: InputPort/OutputPort Name exposes pipe-scoped label ──────────────

func TestPipePort_InputOutputNames(t *testing.T) {
	pp, _ := ports.NewPipePort[int]("broadcast", intCodec, ports.PortOptions{Buffer: 8})
	in := pp.InputPort("from-sensors")
	out := pp.OutputPort("to-clients")

	if in.Name() != "broadcast/in/from-sensors" {
		t.Fatalf("want input port name 'broadcast/in/from-sensors', got %s", in.Name())
	}
	if out.Name() != "broadcast/out/to-clients" {
		t.Fatalf("want output port name 'broadcast/out/to-clients', got %s", out.Name())
	}
}

// ── PP-10: Push before Connect buffers, arrives once Connect drains it ──────

func TestPipePort_Push_BeforeConnect_Buffers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pp, _ := ports.NewPipePort[int]("pipe", intCodec, ports.PortOptions{Buffer: 8})
	out := pp.OutputPort("sink")
	ch := make(chan int, 8)
	out.Bind(ctx, ports.ChanSinkAdapter(ch))

	// Push BEFORE Connect — must not error or block (buffer has room).
	if err := pp.Push(ctx, 42); err != nil {
		t.Fatalf("Push before Connect: %v", err)
	}

	pp.Connect(ctx)

	got := <-ch
	if got != 42 {
		t.Fatalf("want 42, got %d", got)
	}
}

// ── PP-11: Connect with no inputs → no panic ────────────────────────────────

func TestPipePort_Connect_NoInputs(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pp, _ := ports.NewPipePort[int]("pipe", intCodec, ports.PortOptions{Buffer: 8})
	out := pp.OutputPort("sink")
	ch := make(chan int, 8)
	out.Bind(ctx, ports.ChanSinkAdapter(ch))
	pp.Connect(ctx) // no inputs — should not panic

	// No items should arrive.
	select {
	case v := <-ch:
		t.Fatalf("unexpected value from output with no inputs: %d", v)
	default:
	}
}

// ── PP-12: Concurrent access to InputPort/OutputPort ─────────────────────────

func TestPipePort_ConcurrentPortAccess(t *testing.T) {
	pp, _ := ports.NewPipePort[int]("pipe", intCodec, ports.PortOptions{Buffer: 8})
	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			pp.InputPort("in") // same name — idempotent under mu
			pp.OutputPort("out")
		}
		close(done)
	}()
	for i := 0; i < 100; i++ {
		pp.InputPort("in")
		pp.OutputPort("out")
	}
	<-done
}

// ── PP-13: Full pipeline segmentation round-trip ─────────────────────────────

func TestPipePort_FullRoundTrip(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// SourcePort → PipePort input → PipePort output → SinkPort.
	pp, _ := ports.NewPipePort[int]("pipeline", intCodec, ports.PortOptions{Buffer: 16})
	in := pp.InputPort("from-ingest")
	errAdapter := &errSourceAdapter{err: nil} // no-op; we test structure, not data flow
	in.Bind(ctx, errAdapter)

	out := pp.OutputPort("to-egress")
	egressCh := make(chan int, 8)
	out.Bind(ctx, ports.ChanSinkAdapter(egressCh))

	// Connect validates that all ports are wired without panic.
	pp.Connect(ctx)

	// errAdapter sends no items — verify no panics.
	select {
	case v := <-egressCh:
		t.Logf("received unexpected value: %d", v)
	default:
		// Expected: nothing arrived from error-only adapter.
	}
}

// ── PP-14: Chain wires two PipePorts through a transform ────────────────────

func TestPipePort_Chain(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	from, _ := ports.NewPipePort[int]("from", intCodec, ports.PortOptions{Buffer: 8})
	to, _ := ports.NewPipePort[string]("to", strCodec, ports.PortOptions{Buffer: 8})

	outCh := make(chan string, 8)
	to.OutputPort("sink").Bind(ctx, ports.ChanSinkAdapter(outCh))

	double := func(v int) (string, error) {
		return fmt.Sprintf("%d", v*2), nil
	}
	ports.Chain(ctx, from, double, to)

	inCh := make(chan int, 8)
	from.InputPort("src").Bind(ctx, ports.ChanSourceAdapter(inCh))

	from.Connect(ctx)
	to.Connect(ctx)

	inCh <- 5
	inCh <- 10
	close(inCh)

	got1 := <-outCh
	got2 := <-outCh
	if got1 != "10" || got2 != "20" {
		t.Fatalf("want [10 20], got [%s %s]", got1, got2)
	}
}

// ── PP-15: Chain works when to.Connect happens BEFORE from.Connect ─────────

func TestPipePort_Chain_OrderIndependentDownstreamConnect(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	from, _ := ports.NewPipePort[int]("from2", intCodec, ports.PortOptions{Buffer: 8})
	to, _ := ports.NewPipePort[int]("to2", intCodec, ports.PortOptions{Buffer: 8})

	outCh := make(chan int, 8)
	to.OutputPort("sink").Bind(ctx, ports.ChanSinkAdapter(outCh))

	increment := func(v int) (int, error) { return v + 1, nil }
	ports.Chain(ctx, from, increment, to)

	inCh := make(chan int, 8)
	from.InputPort("src").Bind(ctx, ports.ChanSourceAdapter(inCh))

	// Connect to BEFORE from — order must not matter.
	to.Connect(ctx)
	from.Connect(ctx)

	inCh <- 1
	close(inCh)

	got := <-outCh
	if got != 2 {
		t.Fatalf("want 2, got %d", got)
	}
}

// ── PP-16: Connect called twice is safe (no duplicate delivery, no panic) ──

func TestPipePort_Connect_DoubleInvocation_Safe(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pp, _ := ports.NewPipePort[int]("pipe", intCodec, ports.PortOptions{Buffer: 8})
	in := pp.InputPort("src")
	out := pp.OutputPort("sink")

	inCh := make(chan int, 8)
	in.Bind(ctx, ports.ChanSourceAdapter(inCh))
	outCh := make(chan int, 8)
	out.Bind(ctx, ports.ChanSinkAdapter(outCh))

	pp.Connect(ctx)
	pp.Connect(ctx) // second call must be a no-op, not a panic or duplicate delivery

	inCh <- 1
	close(inCh)

	got := <-outCh
	if got != 1 {
		t.Fatalf("want 1, got %d", got)
	}
	// Verify no duplicate delivery from a second internal consumer.
	select {
	case v := <-outCh:
		t.Fatalf("unexpected duplicate delivery: %d", v)
	default:
	}
}

// ── PP-17: ChainStream wires a multi-step Map sub-pipeline ──────────────────

func TestPipePort_ChainStream_MultiStepMap(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	from, _ := ports.NewPipePort[int]("cs-from", intCodec, ports.PortOptions{Buffer: 8})
	to, _ := ports.NewPipePort[string]("cs-to", strCodec, ports.PortOptions{Buffer: 8})

	outCh := make(chan string, 8)
	to.OutputPort("sink").Bind(ctx, ports.ChanSinkAdapter(outCh))

	// Three sequential steps: double, then add 1, then stringify — the
	// general case ChainStream exists for, which a single Chain call
	// (one func(In)(Out,error)) cannot express directly.
	ports.ChainStream(ctx, from, func(s gstream.Stream[int]) gstream.Stream[string] {
		doubled := gstream.Map(ctx, s, func(v int) (int, error) { return v * 2, nil }, gstream.MapOptions{})
		incremented := gstream.Map(ctx, doubled, func(v int) (int, error) { return v + 1, nil }, gstream.MapOptions{})
		return gstream.Map(ctx, incremented, func(v int) (string, error) { return fmt.Sprintf("%d", v), nil }, gstream.MapOptions{})
	}, to)

	inCh := make(chan int, 8)
	from.InputPort("src").Bind(ctx, ports.ChanSourceAdapter(inCh))

	from.Connect(ctx)
	to.Connect(ctx)

	inCh <- 5  // (5*2)+1 = 11
	inCh <- 10 // (10*2)+1 = 21
	close(inCh)

	got1 := <-outCh
	got2 := <-outCh
	if got1 != "11" || got2 != "21" {
		t.Fatalf("want [11 21], got [%s %s]", got1, got2)
	}
}

// ── PP-18: ChainStream supports Filter + Map (not just Map chains) ──────────

func TestPipePort_ChainStream_FilterThenMap(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	from, _ := ports.NewPipePort[int]("cs-filter-from", intCodec, ports.PortOptions{Buffer: 8})
	to, _ := ports.NewPipePort[int]("cs-filter-to", intCodec, ports.PortOptions{Buffer: 8})

	outCh := make(chan int, 8)
	to.OutputPort("sink").Bind(ctx, ports.ChanSinkAdapter(outCh))

	ports.ChainStream(ctx, from, func(s gstream.Stream[int]) gstream.Stream[int] {
		evens := gstream.Filter(ctx, s, func(v int) bool { return v%2 == 0 })
		return gstream.Map(ctx, evens, func(v int) (int, error) { return v * 10, nil }, gstream.MapOptions{})
	}, to)

	inCh := make(chan int, 8)
	from.InputPort("src").Bind(ctx, ports.ChanSourceAdapter(inCh))

	from.Connect(ctx)
	to.Connect(ctx)

	inCh <- 1 // filtered out (odd)
	inCh <- 2 // 2*10 = 20
	inCh <- 3 // filtered out (odd)
	inCh <- 4 // 4*10 = 40
	close(inCh)

	got1 := <-outCh
	got2 := <-outCh
	if got1 != 20 || got2 != 40 {
		t.Fatalf("want [20 40], got [%d %d]", got1, got2)
	}
}

// ── PP-19: Chain is implemented in terms of ChainStream (behavior parity) ──

func TestPipePort_Chain_IsChainStreamSpecialCase(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Two equivalent pipelines: one wired with Chain, one with ChainStream
	// wrapping a single gstream.Map — results must be identical.
	fromA, _ := ports.NewPipePort[int]("parity-a-from", intCodec, ports.PortOptions{Buffer: 8})
	toA, _ := ports.NewPipePort[int]("parity-a-to", intCodec, ports.PortOptions{Buffer: 8})
	fromB, _ := ports.NewPipePort[int]("parity-b-from", intCodec, ports.PortOptions{Buffer: 8})
	toB, _ := ports.NewPipePort[int]("parity-b-to", intCodec, ports.PortOptions{Buffer: 8})

	outA := make(chan int, 8)
	outB := make(chan int, 8)
	toA.OutputPort("sink").Bind(ctx, ports.ChanSinkAdapter(outA))
	toB.OutputPort("sink").Bind(ctx, ports.ChanSinkAdapter(outB))

	triple := func(v int) (int, error) { return v * 3, nil }
	ports.Chain(ctx, fromA, triple, toA)
	ports.ChainStream(ctx, fromB, func(s gstream.Stream[int]) gstream.Stream[int] {
		return gstream.Map(ctx, s, triple, gstream.MapOptions{})
	}, toB)

	inA := make(chan int, 8)
	inB := make(chan int, 8)
	fromA.InputPort("src").Bind(ctx, ports.ChanSourceAdapter(inA))
	fromB.InputPort("src").Bind(ctx, ports.ChanSourceAdapter(inB))

	fromA.Connect(ctx)
	toA.Connect(ctx)
	fromB.Connect(ctx)
	toB.Connect(ctx)

	inA <- 7
	inB <- 7
	close(inA)
	close(inB)

	gotA := <-outA
	gotB := <-outB
	if gotA != gotB || gotA != 21 {
		t.Fatalf("want both == 21, got Chain=%d ChainStream=%d", gotA, gotB)
	}
}

// ── PP-20: SourcePort.BoundAdapters reports real bound adapter names ───────

func TestSourcePort_BoundAdapters(t *testing.T) {
	ctx := context.Background()
	p := intPort("bound-adapters", 8)

	if got := p.BoundAdapters(); len(got) != 0 {
		t.Fatalf("want no bound adapters before Bind, got %v", got)
	}

	p.Bind(ctx, ports.ChanSourceAdapter(feedChan(1, 2)))
	p.Bind(ctx, ports.ChanSourceAdapter(feedChan(3)))

	got := p.BoundAdapters()
	if len(got) != 2 || got[0] != "ports.ChanSourceAdapter" || got[1] != "ports.ChanSourceAdapter" {
		t.Fatalf("want 2x ports.ChanSourceAdapter, got %v", got)
	}
}

// ── PP-21: SinkPort.BoundAdapters reports real bound adapter names ─────────

func TestSinkPort_BoundAdapters(t *testing.T) {
	ctx := context.Background()
	p := intSinkPort("bound-adapters-sink", 8)

	if got := p.BoundAdapters(); len(got) != 0 {
		t.Fatalf("want no bound adapters before Bind, got %v", got)
	}

	out1 := make(chan int, 8)
	out2 := make(chan int, 8)
	p.Bind(ctx, ports.ChanSinkAdapter(out1))
	p.Bind(ctx, ports.ChanSinkAdapter(out2))

	got := p.BoundAdapters()
	if len(got) != 2 || got[0] != "ports.ChanSinkAdapter" || got[1] != "ports.ChanSinkAdapter" {
		t.Fatalf("want 2x ports.ChanSinkAdapter, got %v", got)
	}
}

// ── PP-22: Chain records a real ChainEdge (Kind, To, Func) ─────────────────

func chainEdgeTestFn(v int) (string, error) { return fmt.Sprintf("%d", v), nil }

func TestPipePort_Chain_RecordsEdge(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	from, _ := ports.NewPipePort[int]("edge-from", intCodec, ports.PortOptions{Buffer: 8})
	to, _ := ports.NewPipePort[string]("edge-to", strCodec, ports.PortOptions{Buffer: 8})

	if edges := from.OutEdges(); len(edges) != 0 {
		t.Fatalf("want no edges before Chain, got %v", edges)
	}

	ports.Chain(ctx, from, chainEdgeTestFn, to)

	edges := from.OutEdges()
	if len(edges) != 1 {
		t.Fatalf("want 1 recorded edge, got %d", len(edges))
	}
	e := edges[0]
	if e.Kind != "chain" {
		t.Fatalf("want Kind=chain, got %s", e.Kind)
	}
	if e.To != "edge-to" {
		t.Fatalf("want To=edge-to, got %s", e.To)
	}
	if !strings.Contains(e.Func, "chainEdgeTestFn") {
		t.Fatalf("want Func to contain real function name 'chainEdgeTestFn', got %s", e.Func)
	}
}

// ── PP-23: ChainStream records a real ChainEdge with Kind=chainStream ──────

func TestPipePort_ChainStream_RecordsEdge(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	from, _ := ports.NewPipePort[int]("edge-cs-from", intCodec, ports.PortOptions{Buffer: 8})
	to, _ := ports.NewPipePort[int]("edge-cs-to", intCodec, ports.PortOptions{Buffer: 8})

	ports.ChainStream(ctx, from, func(s gstream.Stream[int]) gstream.Stream[int] {
		return gstream.Map(ctx, s, func(v int) (int, error) { return v, nil }, gstream.MapOptions{})
	}, to)

	edges := from.OutEdges()
	if len(edges) != 1 {
		t.Fatalf("want 1 recorded edge, got %d", len(edges))
	}
	if edges[0].Kind != "chainStream" {
		t.Fatalf("want Kind=chainStream, got %s", edges[0].Kind)
	}
	if edges[0].To != "edge-cs-to" {
		t.Fatalf("want To=edge-cs-to, got %s", edges[0].To)
	}
	if edges[0].Func == "" {
		t.Fatal("want non-empty Func (real, if closure-opaque, function identity)")
	}
}

// ── PP-24: PipePort.Buffer/InputAdapters/OutputAdapters reflect real state ──

func TestPipePort_Buffer_InputAdapters_OutputAdapters(t *testing.T) {
	ctx := context.Background()

	pp, _ := ports.NewPipePort[int]("diag", intCodec, ports.PortOptions{Buffer: 16})
	if pp.Buffer() != 16 {
		t.Fatalf("want Buffer()=16, got %d", pp.Buffer())
	}

	if ins := pp.InputAdapters(); len(ins) != 0 {
		t.Fatalf("want no input adapters before Bind, got %v", ins)
	}
	if outs := pp.OutputAdapters(); len(outs) != 0 {
		t.Fatalf("want no output adapters before Bind, got %v", outs)
	}

	in := pp.InputPort("mqtt")
	in.Bind(ctx, ports.ChanSourceAdapter(feedChan(1)))

	out := pp.OutputPort("sse")
	out.Bind(ctx, ports.ChanSinkAdapter(make(chan int, 8)))

	ins := pp.InputAdapters()
	if got := ins["mqtt"]; len(got) != 1 || got[0] != "ports.ChanSourceAdapter" {
		t.Fatalf("want InputAdapters[mqtt]=[ports.ChanSourceAdapter], got %v", ins)
	}
	outs := pp.OutputAdapters()
	if got := outs["sse"]; len(got) != 1 || got[0] != "ports.ChanSinkAdapter" {
		t.Fatalf("want OutputAdapters[sse]=[ports.ChanSinkAdapter], got %v", outs)
	}
}

// ── PP-25: PipelineSpec derives a full spec from real pipe wiring ──────────

func TestPipelineSpec_DerivesRealData(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	from, _ := ports.NewPipePort[int]("spec-from", intCodec, ports.PortOptions{Buffer: 4})
	to, _ := ports.NewPipePort[string]("spec-to", strCodec, ports.PortOptions{Buffer: 4})

	from.InputPort("ingest").Bind(ctx, ports.ChanSourceAdapter(feedChan(1)))
	sink := make(chan string, 8)
	to.OutputPort("egress").Bind(ctx, ports.ChanSinkAdapter(sink))

	ports.Chain(ctx, from, chainEdgeTestFn, to)

	spec := ports.PipelineSpec("Test Pipeline", "1.0.0", from, to)

	if spec.Info.Title != "Test Pipeline" || spec.Info.Version != "1.0.0" {
		t.Fatalf("want Info={Test Pipeline, 1.0.0}, got %+v", spec.Info)
	}
	// Expect: [port(spec-from), apply(chainEdgeTestFn), port(spec-to)]
	if len(spec.Steps) != 3 {
		t.Fatalf("want 3 steps, got %d: %+v", len(spec.Steps), spec.Steps)
	}
	if spec.Steps[0].Kind != gstream.StepKindPort || spec.Steps[0].Name != "spec-from" {
		t.Fatalf("want step 0 = port(spec-from), got %+v", spec.Steps[0])
	}
	if !strings.Contains(spec.Steps[0].Description, "Buffer=4") {
		t.Fatalf("want step 0 description to contain real Buffer=4, got %q", spec.Steps[0].Description)
	}
	if !strings.Contains(spec.Steps[0].Description, "ports.ChanSourceAdapter") {
		t.Fatalf("want step 0 description to contain real adapter name, got %q", spec.Steps[0].Description)
	}
	if spec.Steps[1].Kind != gstream.StepKindApply || !strings.Contains(spec.Steps[1].Name, "chainEdgeTestFn") {
		t.Fatalf("want step 1 = apply(chainEdgeTestFn), got %+v", spec.Steps[1])
	}
	if spec.Steps[2].Kind != gstream.StepKindPort || spec.Steps[2].Name != "spec-to" {
		t.Fatalf("want step 2 = port(spec-to), got %+v", spec.Steps[2])
	}
	if !strings.Contains(spec.Steps[2].Description, "ports.ChanSinkAdapter") {
		t.Fatalf("want step 2 description to contain real adapter name, got %q", spec.Steps[2].Description)
	}
}

// ── PP-26: PipelineSpec accepts heterogeneous PipePort[T] via PipeSpecSource ─

func TestPipelineSpec_HeterogeneousTypes(t *testing.T) {
	a, _ := ports.NewPipePort[int]("het-a", intCodec, ports.PortOptions{Buffer: 8})
	b, _ := ports.NewPipePort[string]("het-b", strCodec, ports.PortOptions{Buffer: 8})
	c, _ := ports.NewPipePort[cfgItem]("het-c", cfgCodec, ports.PortOptions{Buffer: 8})

	// Compiles only if *PipePort[int], *PipePort[string], *PipePort[cfgItem]
	// all satisfy ports.PipeSpecSource despite differing type parameters.
	pipes := []ports.PipeSpecSource{a, b, c}
	spec := ports.PipelineSpec("Heterogeneous", "1.0.0", pipes...)

	if len(spec.Steps) != 3 {
		t.Fatalf("want 3 port steps (no edges), got %d", len(spec.Steps))
	}
	names := []string{spec.Steps[0].Name, spec.Steps[1].Name, spec.Steps[2].Name}
	if names[0] != "het-a" || names[1] != "het-b" || names[2] != "het-c" {
		t.Fatalf("want [het-a het-b het-c], got %v", names)
	}
}

// ── PP-27: Push path calls RecordSubscribe (PCH-01) ─────────────────────────

type pubSubSpy struct {
	mu         sync.Mutex
	subscribes []bool // true = success
	publishes  []bool
}

func (s *pubSubSpy) RecordValidationError(_, _, _ string)              {}
func (s *pubSubSpy) RecordRequest(_, _ string, _ int, _ time.Duration) {}
func (s *pubSubSpy) RecordSubscribe(_ string, success bool, _ time.Duration) {
	s.mu.Lock()
	s.subscribes = append(s.subscribes, success)
	s.mu.Unlock()
}
func (s *pubSubSpy) RecordPublish(_ string, success bool, _ time.Duration) {
	s.mu.Lock()
	s.publishes = append(s.publishes, success)
	s.mu.Unlock()
}
func (s *pubSubSpy) counts() (subs, pubs int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.subscribes), len(s.publishes)
}

func TestPipePort_Push_RecordsSubscribe(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	spy := &pubSubSpy{}
	ctx = stats.WithObserver(ctx, spy)

	pp, _ := ports.NewPipePort[int]("obs-push", intCodec, ports.PortOptions{Buffer: 8})
	out := pp.OutputPort("sink")
	outCh := make(chan int, 8)
	out.Bind(ctx, ports.ChanSinkAdapter(outCh))

	pp.Connect(ctx)
	if err := pp.Push(ctx, 42); err != nil {
		t.Fatalf("Push: %v", err)
	}
	<-outCh

	subs, _ := spy.counts()
	if subs < 1 {
		t.Fatalf("want at least 1 RecordSubscribe call for pushed item, got %d", subs)
	}
}

// ── PP-28: fanOut calls RecordPublish per destination (PCH-02) ──────────────

func TestPipePort_FanOut_RecordsPublishPerDestination(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	spy := &pubSubSpy{}
	ctx = stats.WithObserver(ctx, spy)

	pp, _ := ports.NewPipePort[int]("obs-fanout", intCodec, ports.PortOptions{Buffer: 8})
	out1 := pp.OutputPort("sink1")
	out2 := pp.OutputPort("sink2")
	ch1 := make(chan int, 8)
	ch2 := make(chan int, 8)
	out1.Bind(ctx, ports.ChanSinkAdapter(ch1))
	out2.Bind(ctx, ports.ChanSinkAdapter(ch2))

	pp.Connect(ctx)
	if err := pp.Push(ctx, 7); err != nil {
		t.Fatalf("Push: %v", err)
	}
	<-ch1
	<-ch2

	_, pubs := spy.counts()
	if pubs != 2 {
		t.Fatalf("want 2 RecordPublish calls (one per destination), got %d", pubs)
	}
}

// ── PP-29: fanOut records failure on ctx-done mid-delivery (PCH-03) ─────────

func TestPipePort_FanOut_RecordsPublishFailureOnCtxDone(t *testing.T) {
	spy := &pubSubSpy{}
	ctx, cancel := context.WithCancel(stats.WithObserver(context.Background(), spy))

	pp, _ := ports.NewPipePort[int]("obs-fanout-fail", intCodec, ports.PortOptions{Buffer: 1})
	// Output with buffer 0-effective (Bind creates its own buffered channel
	// sized p.buffer=1) — fill it, then cancel ctx to force a blocked send.
	out := pp.OutputPort("sink")
	blockedCh := make(chan int) // unbuffered — never drained, forces the send to block
	out.Bind(ctx, ports.ChanSinkAdapter(blockedCh))

	pp.Connect(ctx)
	go func() {
		_ = pp.Push(ctx, 1)
	}()
	// Give the push goroutine a moment to reach the blocked fan-out send,
	// then cancel — the pending send must resolve via ctx.Done() and record
	// a failed RecordPublish, not hang forever.
	time.Sleep(20 * time.Millisecond)
	cancel()
	time.Sleep(20 * time.Millisecond)

	_, pubs := spy.counts()
	if pubs < 1 {
		t.Fatal("want at least 1 RecordPublish call (failure) recorded on ctx-done mid-delivery")
	}
}

// ── PP-30: TraceObserver span brackets Chain/ChainStream edge setup (PCH-04) ─

type traceSpy struct {
	pubSubSpy
	spans []string // operation+name of each StartSpan call
	ended int
}

type traceSpanKey struct{}

func (s *traceSpy) StartSpan(ctx context.Context, operation, name string) context.Context {
	s.spans = append(s.spans, operation+" "+name)
	return context.WithValue(ctx, traceSpanKey{}, true)
}
func (s *traceSpy) EndSpan(ctx context.Context, _ error) {
	if ctx.Value(traceSpanKey{}) != nil {
		s.ended++
	}
}

func TestPipePort_Chain_StartsTraceSpan(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	spy := &traceSpy{}
	ctx = stats.WithObserver(ctx, spy)

	from, _ := ports.NewPipePort[int]("trace-from", intCodec, ports.PortOptions{Buffer: 8})
	to, _ := ports.NewPipePort[int]("trace-to", intCodec, ports.PortOptions{Buffer: 8})

	ports.Chain(ctx, from, func(v int) (int, error) { return v, nil }, to)

	if len(spy.spans) != 1 {
		t.Fatalf("want 1 StartSpan call for the Chain edge, got %d: %v", len(spy.spans), spy.spans)
	}
	if spy.spans[0] != "pipe.chain trace-from->trace-to" {
		t.Fatalf("want span 'pipe.chain trace-from->trace-to', got %q", spy.spans[0])
	}
	if spy.ended != 1 {
		t.Fatalf("want 1 EndSpan call, got %d", spy.ended)
	}
}

func TestPipePort_ChainStream_StartsTraceSpan(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	spy := &traceSpy{}
	ctx = stats.WithObserver(ctx, spy)

	from, _ := ports.NewPipePort[int]("trace-cs-from", intCodec, ports.PortOptions{Buffer: 8})
	to, _ := ports.NewPipePort[int]("trace-cs-to", intCodec, ports.PortOptions{Buffer: 8})

	ports.ChainStream(ctx, from, func(s gstream.Stream[int]) gstream.Stream[int] {
		return gstream.Map(ctx, s, func(v int) (int, error) { return v, nil }, gstream.MapOptions{})
	}, to)

	if len(spy.spans) != 1 {
		t.Fatalf("want 1 StartSpan call for the ChainStream edge, got %d: %v", len(spy.spans), spy.spans)
	}
	if spy.ended != 1 {
		t.Fatalf("want 1 EndSpan call, got %d", spy.ended)
	}
}

// ── PP-31: PipePort.Done() closes only after Connect's goroutines fully exit (PCH-05) ─

func TestPipePort_Done_ClosesAfterGoroutinesExit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	pp, _ := ports.NewPipePort[int]("done-test", intCodec, ports.PortOptions{Buffer: 8})
	in := pp.InputPort("src")
	inCh := make(chan int, 8)
	in.Bind(ctx, ports.ChanSourceAdapter(inCh))

	out := pp.OutputPort("sink")
	outCh := make(chan int, 8)
	out.Bind(ctx, ports.ChanSinkAdapter(outCh))

	pp.Connect(ctx)

	select {
	case <-pp.Done():
		t.Fatal("Done() closed before Connect's goroutines had any reason to exit")
	default:
	}

	inCh <- 1
	<-outCh
	cancel()

	select {
	case <-pp.Done():
		// expected
	case <-time.After(2 * time.Second):
		t.Fatal("Done() did not close within 2s of ctx cancellation")
	}
}

func TestPipePort_Done_NeverClosesWithoutConnect(t *testing.T) {
	pp, _ := ports.NewPipePort[int]("done-no-connect", intCodec, ports.PortOptions{Buffer: 8})
	select {
	case <-pp.Done():
		t.Fatal("Done() must not close when Connect was never called")
	case <-time.After(50 * time.Millisecond):
		// expected: still open
	}
}

// ── PP-32/33: InputPortWithPatterns/OutputPortWithPatterns build real handles (PCH-09/10) ─

func TestPipePort_InputPortWithPatterns_BuildsRealHandle(t *testing.T) {
	pp, _ := ports.NewPipePort[cfgItem]("patterns-test", cfgCodec, ports.PortOptions{Buffer: 8})

	in, err := pp.InputPortWithPatterns("configured", []ports.Pattern{
		ports.RESTPattern{Method: "GET", Path: "/cfg"},
	})
	if err != nil {
		t.Fatalf("InputPortWithPatterns: %v", err)
	}

	handle, ok := ports.RESTHandle[cfgItem, struct{}](in)
	if !ok {
		t.Fatal("want RESTHandle to find the declared RESTPattern, got (nil, false)")
	}
	if handle == nil {
		t.Fatal("want non-nil handle")
	}
}

// PCH-11: InputPortWithPatterns forwards NewPipePort's shared RESTBuilder —
// the sub-port's route must land in the SAME builder's OpenAPISpec, not a
// private single-use builder created ad hoc by buildEventPatternHandles.
func TestPipePort_InputPortWithPatterns_UsesSharedRESTBuilder(t *testing.T) {
	shared := rest.NewBuilder(rest.Info{Title: "shared", Version: "1.0.0"})
	pp, err := ports.NewPipePort[cfgItem]("shared-builder-test", cfgCodec, ports.PortOptions{
		Buffer: 8, RESTBuilder: shared,
	})
	if err != nil {
		t.Fatalf("NewPipePort: %v", err)
	}

	if _, err := pp.InputPortWithPatterns("ingest", []ports.Pattern{
		ports.RESTPattern{Method: "GET", Path: "/shared-in"},
	}); err != nil {
		t.Fatalf("InputPortWithPatterns: %v", err)
	}

	doc, err := shared.OpenAPISpec()
	if err != nil {
		t.Fatalf("OpenAPISpec: %v", err)
	}
	yamlBytes, err := doc.MarshalYAML()
	if err != nil {
		t.Fatalf("MarshalYAML: %v", err)
	}
	if !strings.Contains(string(yamlBytes), "/shared-in") {
		t.Fatalf("want /shared-in registered in the shared builder's spec, got:\n%s", yamlBytes)
	}
}

func TestPipePort_OutputPortWithPatterns_UsesSharedRESTBuilder(t *testing.T) {
	shared := rest.NewBuilder(rest.Info{Title: "shared", Version: "1.0.0"})
	pp, err := ports.NewPipePort[cfgItem]("shared-builder-out-test", cfgCodec, ports.PortOptions{
		Buffer: 8, RESTBuilder: shared,
	})
	if err != nil {
		t.Fatalf("NewPipePort: %v", err)
	}

	if _, err := pp.OutputPortWithPatterns("egress", []ports.Pattern{
		ports.RESTPattern{Method: "GET", Path: "/shared-out"},
	}); err != nil {
		t.Fatalf("OutputPortWithPatterns: %v", err)
	}

	doc, err := shared.OpenAPISpec()
	if err != nil {
		t.Fatalf("OpenAPISpec: %v", err)
	}
	yamlBytes, err := doc.MarshalYAML()
	if err != nil {
		t.Fatalf("MarshalYAML: %v", err)
	}
	if !strings.Contains(string(yamlBytes), "/shared-out") {
		t.Fatalf("want /shared-out registered in the shared builder's spec, got:\n%s", yamlBytes)
	}
}

func TestPipePort_OutputPortWithPatterns_BuildsRealHandle(t *testing.T) {
	pp, _ := ports.NewPipePort[cfgItem]("patterns-out-test", cfgCodec, ports.PortOptions{Buffer: 8})

	out, err := pp.OutputPortWithPatterns("configured", []ports.Pattern{
		ports.RESTPattern{Method: "GET", Path: "/cfg-out"},
	})
	if err != nil {
		t.Fatalf("OutputPortWithPatterns: %v", err)
	}

	handle, ok := ports.SSEHandle[cfgItem](out)
	if !ok {
		t.Fatal("want SSEHandle to find the declared RESTPattern (SinkPort SSE shape), got (nil, false)")
	}
	if handle == nil {
		t.Fatal("want non-nil handle")
	}
}

func TestPipePort_InputPortWithPatterns_SameNameReturnsExisting(t *testing.T) {
	pp, _ := ports.NewPipePort[int]("patterns-same-name", intCodec, ports.PortOptions{Buffer: 8})

	in1, err := pp.InputPortWithPatterns("x", nil)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	in2, err := pp.InputPortWithPatterns("x", nil)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if in1 != in2 {
		t.Fatal("want same instance for same name")
	}

	// Plain InputPort with the same name must also return the same instance.
	in3 := pp.InputPort("x")
	if in3 != in1 {
		t.Fatal("want InputPort(\"x\") to return the same instance InputPortWithPatterns(\"x\", ...) created")
	}
}

func TestPipePort_InputPortWithPatterns_InvalidPatternErrors(t *testing.T) {
	pp, _ := ports.NewPipePort[int]("patterns-invalid", intCodec, ports.PortOptions{Buffer: 8})

	// A CustomFormat type mismatch (format.Format[string] on an int-typed
	// port) reliably fails FilePattern construction — proof
	// InputPortWithPatterns surfaces the real PatternRegisterError instead
	// of silently ignoring it the way plain InputPort would (InputPort has
	// no way to declare Patterns at all, so there's nothing to fail).
	_, err := pp.InputPortWithPatterns("bad", []ports.Pattern{
		ports.FilePattern{
			Path:         "/tmp/{id}.txt",
			CustomFormat: format.JSON(codex.String()), // format.Format[string], not [int]
		},
	})
	if err == nil {
		t.Fatal("want PatternRegisterError for CustomFormat type mismatch, got nil")
	}
	var pre ports.PatternRegisterError
	if !errors.As(err, &pre) {
		t.Fatalf("want PatternRegisterError, got %T: %v", err, err)
	}
}
