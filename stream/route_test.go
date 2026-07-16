package stream_test

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/stats"
	stream "github.com/DaniDeer/go-codex/stream"
)

func chanStream[T any](vals ...T) (stream.Stream[T], chan error) {
	valCh := make(chan T, len(vals))
	for _, v := range vals {
		valCh <- v
	}
	close(valCh)
	errCh := make(chan error, 2)
	return stream.Stream[T]{Values: valCh, Errors: errCh}, errCh
}

// ── GroupBy ───────────────────────────────────────────────────────────────────

type keyed struct {
	Key string
	N   int
}

// G1
func TestGroupBy_OneStreamPerKey(t *testing.T) {
	ctx := context.Background()
	src, errCh := chanStream(
		keyed{"a", 1}, keyed{"b", 10}, keyed{"a", 2}, keyed{"b", 20}, keyed{"a", 3})
	close(errCh)

	var mu sync.Mutex
	got := map[string][]int{}
	var wg sync.WaitGroup

	stream.GroupBy(ctx, src,
		func(k keyed) string { return k.Key },
		func(key string, s stream.Stream[keyed]) {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for v := range s.Values {
					mu.Lock()
					got[key] = append(got[key], v.N)
					mu.Unlock()
				}
				for range s.Errors {
				}
			}()
		},
		stream.GroupByOptions{Buffer: 4})
	wg.Wait()

	if len(got) != 2 {
		t.Fatalf("want 2 keys, got %v", got)
	}
	if fmt.Sprint(got["a"]) != "[1 2 3]" || fmt.Sprint(got["b"]) != "[10 20]" {
		t.Errorf("want per-key order preserved, got %v", got)
	}
}

// G2
func TestGroupBy_SubStreamsCloseOnParentClose(t *testing.T) {
	ctx := context.Background()
	src, errCh := chanStream(keyed{"a", 1})
	close(errCh)

	closed := make(chan struct{})
	stream.GroupBy(ctx, src,
		func(k keyed) string { return k.Key },
		func(_ string, s stream.Stream[keyed]) {
			go func() {
				for range s.Values {
				}
				for range s.Errors {
				}
				close(closed) // both channels closed
			}()
		},
		stream.GroupByOptions{Buffer: 1})

	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("want sub-stream channels closed after parent close")
	}
}

// G3
func TestGroupBy_ErrorsFanOutToActiveKeys(t *testing.T) {
	ctx := context.Background()
	valCh := make(chan keyed)
	errCh := make(chan error)
	boom := errors.New("boom")

	keysSeen := make(chan struct{}, 2)
	var mu sync.Mutex
	errCount := 0
	var wg sync.WaitGroup

	done := make(chan struct{})
	go func() {
		defer close(done)
		stream.GroupBy(ctx, stream.Stream[keyed]{Values: valCh, Errors: errCh},
			func(k keyed) string { return k.Key },
			func(_ string, s stream.Stream[keyed]) {
				keysSeen <- struct{}{}
				wg.Add(1)
				go func() {
					defer wg.Done()
					for range s.Values {
					}
					for e := range s.Errors {
						if errors.Is(e, boom) {
							mu.Lock()
							errCount++
							mu.Unlock()
						}
					}
				}()
			},
			stream.GroupByOptions{Buffer: 4})
	}()

	// Sequence deterministically: both keys active BEFORE the error arrives.
	valCh <- keyed{"a", 1}
	valCh <- keyed{"b", 2}
	<-keysSeen
	<-keysSeen
	errCh <- boom
	close(valCh)
	close(errCh)
	<-done
	wg.Wait()

	if errCount != 2 {
		t.Errorf("want error fanned out to 2 active keys, got %d", errCount)
	}
}

// G4
func TestGroupBy_CtxCancelUnblocks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	valCh := make(chan keyed, 2)
	valCh <- keyed{"a", 1}
	valCh <- keyed{"a", 2} // second item blocks: unbuffered sub-stream, unread
	errCh := make(chan error)

	done := make(chan struct{})
	go func() {
		defer close(done)
		stream.GroupBy(ctx, stream.Stream[keyed]{Values: valCh, Errors: errCh},
			func(k keyed) string { return k.Key },
			func(_ string, _ stream.Stream[keyed]) {}, // nobody reads
			stream.GroupByOptions{})                   // Buffer 0
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("want GroupBy to unblock on ctx cancel")
	}
}

// G5
func TestGroupBy_ObserverPerItem(t *testing.T) {
	ctx := context.Background()
	obs := &countingStreamObserver{}
	src, errCh := chanStream(keyed{"a", 1}, keyed{"a", 2})
	close(errCh)
	stream.GroupBy(ctx, src,
		func(k keyed) string { return k.Key },
		func(_ string, s stream.Stream[keyed]) {
			go func() {
				for range s.Values {
				}
				for range s.Errors {
				}
			}()
		},
		stream.GroupByOptions{Buffer: 4, Observer: obs})
	if n := obs.count("groupby"); n != 2 {
		t.Errorf("want 2 groupby observer events, got %d", n)
	}
}

// countingStreamObserver counts RecordStreamItem per location.
type countingStreamObserver struct {
	mu     sync.Mutex
	counts map[string]int
}

func (o *countingStreamObserver) RecordRequest(string, string, int, time.Duration) {}
func (o *countingStreamObserver) RecordSubscribe(string, bool, time.Duration)      {}
func (o *countingStreamObserver) RecordPublish(string, bool, time.Duration)        {}
func (o *countingStreamObserver) RecordValidationError(string, string, string)     {}

func (o *countingStreamObserver) RecordStreamItem(location string, _ bool, _ time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.counts == nil {
		o.counts = map[string]int{}
	}
	o.counts[location]++
}

func (o *countingStreamObserver) count(location string) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.counts[location]
}

// ── Switch ────────────────────────────────────────────────────────────────────

// S1
func TestSwitch_FirstMatchWins(t *testing.T) {
	ctx := context.Background()
	src, errCh := chanStream(95) // matches BOTH alert (>90) and warning (>70)
	close(errCh)

	out, rest := stream.Switch(ctx, src, []stream.Case[int]{
		{Name: "alert", When: func(v int) bool { return v > 90 }},
		{Name: "warning", When: func(v int) bool { return v > 70 }},
	}, stream.SwitchOptions{Buffer: 2})

	alerts, _ := stream.Collect(ctx, out[0])
	warnings, _ := stream.Collect(ctx, out[1])
	restVals, _ := stream.Collect(ctx, rest)
	if len(alerts) != 1 || len(warnings) != 0 || len(restVals) != 0 {
		t.Errorf("want item ONLY in first matching case, got alerts=%v warnings=%v rest=%v",
			alerts, warnings, restVals)
	}
}

// S2
func TestSwitch_DefaultStream(t *testing.T) {
	ctx := context.Background()
	src, errCh := chanStream(50, 95)
	close(errCh)

	out, rest := stream.Switch(ctx, src, []stream.Case[int]{
		{Name: "alert", When: func(v int) bool { return v > 90 }},
	}, stream.SwitchOptions{Buffer: 2})

	alerts, _ := stream.Collect(ctx, out[0])
	restVals, _ := stream.Collect(ctx, rest)
	if len(alerts) != 1 || alerts[0] != 95 {
		t.Errorf("want [95] in alert case, got %v", alerts)
	}
	if len(restVals) != 1 || restVals[0] != 50 {
		t.Errorf("want [50] in rest, got %v", restVals)
	}
}

// S3
func TestSwitch_ErrorsToDefaultOnly(t *testing.T) {
	ctx := context.Background()
	valCh := make(chan int)
	errCh := make(chan error, 1)
	boom := errors.New("boom")
	errCh <- boom
	close(valCh)
	close(errCh)

	out, rest := stream.Switch(ctx, stream.Stream[int]{Values: valCh, Errors: errCh},
		[]stream.Case[int]{{Name: "c", When: func(int) bool { return true }}},
		stream.SwitchOptions{Buffer: 2})

	_, caseErrs := stream.Collect(ctx, out[0])
	_, restErrs := stream.Collect(ctx, rest)
	if len(caseErrs) != 0 {
		t.Errorf("want case stream error-free, got %v", caseErrs)
	}
	if len(restErrs) != 1 || !errors.Is(restErrs[0], boom) {
		t.Errorf("want src error once on rest, got %v", restErrs)
	}
}

// S4
func TestSwitch_InvalidCasesPanic(t *testing.T) {
	ctx := context.Background()
	for name, cases := range map[string][]stream.Case[int]{
		"empty-name":     {{Name: "", When: func(int) bool { return true }}},
		"duplicate-name": {{Name: "x", When: func(int) bool { return true }}, {Name: "x", When: func(int) bool { return true }}},
		"nil-when":       {{Name: "x", When: nil}},
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Error("want panic for malformed cases")
				}
			}()
			src, errCh := chanStream[int]()
			close(errCh)
			stream.Switch(ctx, src, cases, stream.SwitchOptions{})
		})
	}
}

// S5
func TestSwitch_CaseConstraint(t *testing.T) {
	ctx := context.Background()
	hot := codex.Constraint[int]{
		Name:    "hot",
		Check:   func(v int) bool { return v > 90 },
		Message: func(int) string { return "too hot" },
	}
	src, errCh := chanStream(95, 20)
	close(errCh)

	out, rest := stream.Switch(ctx, src,
		[]stream.Case[int]{stream.CaseConstraint("hot", hot)},
		stream.SwitchOptions{Buffer: 2})
	hots, _ := stream.Collect(ctx, out[0])
	restVals, _ := stream.Collect(ctx, rest)
	if len(hots) != 1 || hots[0] != 95 || len(restVals) != 1 {
		t.Errorf("constraint case: want [95]/[20], got %v/%v", hots, restVals)
	}
}

// S6
func TestSwitch_AllStreamsCloseOnSrcEnd(t *testing.T) {
	ctx := context.Background()
	src, errCh := chanStream(1)
	close(errCh)

	out, rest := stream.Switch(ctx, src,
		[]stream.Case[int]{{Name: "c", When: func(int) bool { return true }}},
		stream.SwitchOptions{Buffer: 2})

	// Collect returns only when Values AND Errors are closed.
	done := make(chan struct{})
	go func() {
		stream.Collect(ctx, out[0])
		stream.Collect(ctx, rest)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("want all Switch streams to close when src ends")
	}
}

// ── SwitchKey ─────────────────────────────────────────────────────────────────

type taggedEvent struct {
	Kind string
	N    int
}

// the SHARED discriminator: one named function drives codec AND routing.
func taggedEventKind(e taggedEvent) string { return e.Kind }

// K1
func TestSwitchKey_RoutesByKey(t *testing.T) {
	ctx := context.Background()
	src, errCh := chanStream(
		taggedEvent{"created", 1}, taggedEvent{"cancelled", 2}, taggedEvent{"unknown", 3})
	close(errCh)

	out, rest := stream.SwitchKey(ctx, src,
		[]string{"created", "cancelled"}, taggedEventKind, stream.SwitchOptions{Buffer: 2})

	created, _ := stream.Collect(ctx, out[0])
	cancelled, _ := stream.Collect(ctx, out[1])
	restVals, _ := stream.Collect(ctx, rest)
	if len(created) != 1 || created[0].N != 1 {
		t.Errorf("want created=[1], got %v", created)
	}
	if len(cancelled) != 1 || cancelled[0].N != 2 {
		t.Errorf("want cancelled=[2], got %v", cancelled)
	}
	if len(restVals) != 1 || restVals[0].N != 3 {
		t.Errorf("want unknown key in rest, got %v", restVals)
	}
}

// K2 — the no-drift proof: the SAME named discriminator drives a TaggedUnion
// codec round-trip AND SwitchKey routing.
func TestSwitchKey_SharedTaggedUnionDiscriminator(t *testing.T) {
	ctx := context.Background()

	variantCodec := codex.Struct[taggedEvent](
		codex.RequiredField("n", codex.Int(),
			func(e taggedEvent) int { return e.N },
			func(e *taggedEvent, v int) { e.N = v }),
	)
	union := codex.TaggedUnion("kind",
		map[string]codex.Codec[taggedEvent]{
			"created":   variantCodec,
			"cancelled": variantCodec,
		},
		func(e taggedEvent) (string, error) { return taggedEventKind(e), nil },
	)

	// Codec round-trip via the discriminator…
	encoded, err := union.Encode(taggedEvent{Kind: "created", N: 7})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	decoded, err := union.Decode(encoded)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded.N != 7 {
		t.Fatalf("round-trip lost data: %+v", decoded)
	}
	decoded.Kind = "created" // wire tag is the discriminator; restore for routing

	// …and routing via the SAME function.
	src, errCh := chanStream(decoded)
	close(errCh)
	out, _ := stream.SwitchKey(ctx, src,
		[]string{"created", "cancelled"}, taggedEventKind, stream.SwitchOptions{Buffer: 1})
	created, _ := stream.Collect(ctx, out[0])
	if len(created) != 1 || created[0].N != 7 {
		t.Errorf("want the decoded item routed to created, got %v", created)
	}
}

// ── SplitEither ───────────────────────────────────────────────────────────────

// E1
func TestSplitEither_TotalSplit(t *testing.T) {
	ctx := context.Background()
	l1, r1, l2 := 1, "x", 2
	src, errCh := chanStream(
		codex.Either[int, string]{Left: &l1},
		codex.Either[int, string]{Right: &r1},
		codex.Either[int, string]{Left: &l2},
	)
	close(errCh)

	lefts, rights := stream.SplitEither(ctx, src, stream.SwitchOptions{Buffer: 4})
	var gotL []int
	var gotR []string
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for v := range lefts.Values {
			gotL = append(gotL, v)
		}
		for range lefts.Errors {
		}
	}()
	go func() {
		defer wg.Done()
		for v := range rights.Values {
			gotR = append(gotR, v)
		}
		for range rights.Errors {
		}
	}()
	wg.Wait()
	if fmt.Sprint(gotL) != "[1 2]" || fmt.Sprint(gotR) != "[x]" {
		t.Errorf("want total split [1 2]/[x], got %v/%v", gotL, gotR)
	}
}

// E2
func TestSplitEither_ErrorsFanOutToBoth(t *testing.T) {
	ctx := context.Background()
	valCh := make(chan codex.Either[int, string])
	errCh := make(chan error, 1)
	boom := errors.New("boom")
	errCh <- boom
	close(valCh)
	close(errCh)

	lefts, rights := stream.SplitEither(ctx,
		stream.Stream[codex.Either[int, string]]{Values: valCh, Errors: errCh},
		stream.SwitchOptions{Buffer: 2})
	_, lErrs := stream.Collect(ctx, lefts)
	var rErrs []error
	for range rights.Values {
	}
	for e := range rights.Errors {
		rErrs = append(rErrs, e)
	}
	if len(lErrs) != 1 || !errors.Is(lErrs[0], boom) {
		t.Errorf("want error on left branch, got %v", lErrs)
	}
	if len(rErrs) != 1 || !errors.Is(rErrs[0], boom) {
		t.Errorf("want error on right branch, got %v", rErrs)
	}
}

// ── Type routing ──────────────────────────────────────────────────────────────

type domainEvent interface{ isEvent() }

type evCreated struct{ ID int }
type evCancelled struct{ ID int }
type evOther struct{ ID int }

func (evCreated) isEvent()   {}
func (evCancelled) isEvent() {}
func (evOther) isEvent()     {}

// T1
func TestOfType_FiltersDynamicType(t *testing.T) {
	ctx := context.Background()
	valCh := make(chan domainEvent, 3)
	valCh <- evCreated{1}
	valCh <- evCancelled{2}
	valCh <- evCreated{3}
	close(valCh)
	errCh := make(chan error, 1)
	boom := errors.New("boom")
	errCh <- boom
	close(errCh)

	created := stream.OfType[evCreated](ctx, stream.Stream[domainEvent]{Values: valCh, Errors: errCh})
	vals, errs := stream.Collect(ctx, created)
	ids := []int{}
	for _, v := range vals {
		ids = append(ids, v.ID)
	}
	sort.Ints(ids)
	if fmt.Sprint(ids) != "[1 3]" {
		t.Errorf("want typed [1 3], got %v", ids)
	}
	if len(errs) != 1 || !errors.Is(errs[0], boom) {
		t.Errorf("want error forwarded, got %v", errs)
	}
}

// T1b — OfType resolves the observer from ctx and records "oftype" per match.
func TestOfType_ObserverFromContext(t *testing.T) {
	obs := &countingStreamObserver{}
	ctx := stats.WithObserver(context.Background(), obs)
	valCh := make(chan domainEvent, 3)
	valCh <- evCreated{1}
	valCh <- evOther{2}
	valCh <- evCreated{3}
	close(valCh)
	errCh := make(chan error)
	close(errCh)

	created := stream.OfType[evCreated](ctx, stream.Stream[domainEvent]{Values: valCh, Errors: errCh})
	vals, _ := stream.Collect(ctx, created)
	if len(vals) != 2 {
		t.Fatalf("want 2 matches, got %d", len(vals))
	}
	obs.mu.Lock()
	defer obs.mu.Unlock()
	if obs.counts["oftype"] != 2 {
		t.Errorf("want 2 oftype observer events, got %d", obs.counts["oftype"])
	}
}

// TP1 — WithSwitch/WithGroupBy add steps of the routing kinds.
func TestTopology_WithSwitchAndGroupBy(t *testing.T) {
	topo := stream.NewTopology("routing", "1.0.0").
		WithSwitch("route alerts by severity").
		WithGroupBy("per-sensor sub-streams")
	steps := topo.Spec().Steps
	if len(steps) != 2 {
		t.Fatalf("want 2 steps, got %d", len(steps))
	}
	if steps[0].Kind != stream.StepKindSwitch || steps[1].Kind != stream.StepKindGroupBy {
		t.Errorf("want [switch groupBy], got [%s %s]", steps[0].Kind, steps[1].Kind)
	}
}

// T2
func TestSwitchType2_RoutesTyped(t *testing.T) {
	ctx := context.Background()
	valCh := make(chan domainEvent, 3)
	valCh <- evCreated{1}
	valCh <- evCancelled{2}
	valCh <- evOther{3}
	close(valCh)
	errCh := make(chan error, 1)
	boom := errors.New("boom")
	errCh <- boom
	close(errCh)

	created, cancelled, rest := stream.SwitchType2[evCreated, evCancelled](
		ctx, stream.Stream[domainEvent]{Values: valCh, Errors: errCh},
		stream.SwitchOptions{Buffer: 4})

	cVals, _ := stream.Collect(ctx, created)
	xVals, _ := stream.Collect(ctx, cancelled)
	rVals, rErrs := stream.Collect(ctx, rest)
	if len(cVals) != 1 || cVals[0].ID != 1 {
		t.Errorf("want typed created [1], got %v", cVals)
	}
	if len(xVals) != 1 || xVals[0].ID != 2 {
		t.Errorf("want typed cancelled [2], got %v", xVals)
	}
	if len(rVals) != 1 {
		t.Errorf("want other in rest, got %v", rVals)
	}
	if len(rErrs) != 1 || !errors.Is(rErrs[0], boom) {
		t.Errorf("want src error on rest, got %v", rErrs)
	}
}

// T3 — SwitchType3 covers three typed cases; also exercises the
// first-match-wins ordering.
func TestSwitchType3_RoutesTyped(t *testing.T) {
	ctx := context.Background()
	valCh := make(chan domainEvent, 4)
	valCh <- evCreated{1}
	valCh <- evCancelled{2}
	valCh <- evOther{3}
	close(valCh)
	errCh := make(chan error)
	close(errCh)

	a, b, c, rest := stream.SwitchType3[evCreated, evCancelled, evOther](
		ctx, stream.Stream[domainEvent]{Values: valCh, Errors: errCh},
		stream.SwitchOptions{Buffer: 4})

	aV, _ := stream.Collect(ctx, a)
	bV, _ := stream.Collect(ctx, b)
	cV, _ := stream.Collect(ctx, c)
	rV, _ := stream.Collect(ctx, rest)
	if len(aV) != 1 || len(bV) != 1 || len(cV) != 1 || len(rV) != 0 {
		t.Errorf("want 1/1/1/0, got %d/%d/%d/%d", len(aV), len(bV), len(cV), len(rV))
	}
}

// ── Examples ──────────────────────────────────────────────────────────────────

// ExampleSwitch routes readings into alert/warning cases with a default
// (archive) stream — first match wins, positional outputs.
func ExampleSwitch() {
	ctx := context.Background()
	src := make(chan int, 3)
	src <- 95
	src <- 75
	src <- 20
	close(src)

	out, rest := stream.Switch(ctx, stream.From(ctx, src), []stream.Case[int]{
		{Name: "alert", When: func(v int) bool { return v > 90 }},
		{Name: "warning", When: func(v int) bool { return v > 70 }},
	}, stream.SwitchOptions{Buffer: 4})

	alerts, _ := stream.Collect(ctx, out[0])
	warnings, _ := stream.Collect(ctx, out[1])
	archive, _ := stream.Collect(ctx, rest)
	fmt.Println("alerts:", alerts)
	fmt.Println("warnings:", warnings)
	fmt.Println("archive:", archive)
	// Output:
	// alerts: [95]
	// warnings: [75]
	// archive: [20]
}

// ExampleGroupBy splits a stream into per-key sub-pipelines: the callback
// STARTS each per-key consumer; sub-streams close when the source ends.
func ExampleGroupBy() {
	ctx := context.Background()
	src := make(chan string, 4)
	src <- "a:1"
	src <- "b:1"
	src <- "a:2"
	close(src)

	var mu sync.Mutex
	counts := map[string]int{}
	var wg sync.WaitGroup
	stream.GroupBy(ctx, stream.From(ctx, src),
		func(s string) string { return s[:1] }, // key = first character
		func(key string, sub stream.Stream[string]) {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for range sub.Values {
					mu.Lock()
					counts[key]++
					mu.Unlock()
				}
				for range sub.Errors {
				}
			}()
		},
		stream.GroupByOptions{Buffer: 2})
	wg.Wait()

	fmt.Println("a:", counts["a"], "b:", counts["b"])
	// Output:
	// a: 2 b: 1
}
