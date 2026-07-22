package ports_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/DaniDeer/go-codex/ports"
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
