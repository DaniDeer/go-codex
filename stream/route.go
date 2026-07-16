package stream

import (
	"context"
	"fmt"
	"time"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/stats"
)

// ── GroupBy ───────────────────────────────────────────────────────────────────

// GroupByOptions configures [GroupBy].
type GroupByOptions struct {
	// Buffer is each per-key channel's buffer. Default 0. A slow per-key
	// consumer backpressures the WHOLE GroupBy (single dispatch goroutine) —
	// buffer accordingly.
	Buffer int
	// Observer receives per-item [stats.StreamObserver.RecordStreamItem]
	// events with location "groupby". Nil means resolved from ctx.
	Observer stats.Observer
}

// keyedStream is one per-key sub-stream's channel pair.
type keyedStream[T any] struct {
	values chan T
	errs   chan error
}

// GroupBy splits src into per-key sub-streams. onKey is called once for each
// new key — from the GroupBy dispatch goroutine, so it must return promptly:
// START the per-key pipeline in it (usually a goroutine), don't run it. Each
// per-key [Stream] receives only that key's items and closes when src
// terminates or ctx is cancelled — the caller-owned per-key pipelines then
// drain out naturally.
//
// Errors from src are forwarded to every ACTIVE per-key stream (each per-key
// consumer is an independent pipeline; an error is context every consumer
// should see — matching SinkPort fan-out semantics).
//
// GroupBy blocks until src terminates — run it in a goroutine like
// SinkPort.Feed when the caller must continue concurrently:
//
//	stream.GroupBy(ctx, readings,
//	    func(r Reading) string { return r.SensorID },
//	    func(id string, s stream.Stream[Reading]) {
//	        go runSensorPipeline(ctx, id, s) // caller owns the goroutine
//	    },
//	    stream.GroupByOptions{Buffer: 8})
//
// The key set is unbounded by design — every distinct key allocates a
// sub-stream that lives until src terminates. If keys are adversarial or
// high-cardinality, bound them upstream (e.g. Filter) before grouping.
func GroupBy[T any, K comparable](
	ctx context.Context,
	src Stream[T],
	key func(T) K,
	onKey func(K, Stream[T]),
	opts GroupByOptions,
) {
	obs := opts.Observer
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}
	groups := make(map[K]*keyedStream[T])
	defer func() {
		for _, g := range groups {
			close(g.values)
			close(g.errs)
		}
	}()

	valCh := src.Values
	errCh := src.Errors
	for valCh != nil || errCh != nil {
		select {
		case <-ctx.Done():
			return
		case v, ok := <-valCh:
			if !ok {
				valCh = nil
				continue
			}
			start := time.Now()
			k := key(v)
			g, exists := groups[k]
			if !exists {
				g = &keyedStream[T]{
					values: make(chan T, opts.Buffer),
					errs:   make(chan error, opts.Buffer),
				}
				groups[k] = g
				onKey(k, Stream[T]{Values: g.values, Errors: g.errs})
			}
			select {
			case g.values <- v:
			case <-ctx.Done():
				return
			}
			if so, ok2 := obs.(stats.StreamObserver); ok2 {
				so.RecordStreamItem("groupby", true, time.Since(start))
			}
		case e, ok := <-errCh:
			if !ok {
				errCh = nil
				continue
			}
			// Fan out to all active keys; drop per key when its consumer is
			// not keeping up with errors (non-blocking, like SinkPort.Feed).
			for _, g := range groups {
				select {
				case g.errs <- e:
				default:
				}
			}
		}
	}
}

// ── Switch ────────────────────────────────────────────────────────────────────

// Case is one [Switch] branch: a name (observability, topology, and error
// identity) and a predicate. Build one from a [codex.Constraint] with
// [CaseConstraint] — the same declarative rule then serves wire validation,
// the spec, and routing.
type Case[T any] struct {
	// Name labels the case in observer events and topology steps.
	Name string
	// When selects items for this case.
	When func(T) bool
}

// CaseConstraint adapts a [codex.Constraint] into a [Case] — the validation
// vocabulary doubles as routing predicates:
//
//	hot := stream.CaseConstraint("hot", domain.HotReading(cfg.Threshold))
func CaseConstraint[T any](name string, c codex.Constraint[T]) Case[T] {
	return Case[T]{Name: name, When: c.Check}
}

// SwitchOptions configures [Switch], [SwitchKey], [SwitchType2],
// [SwitchType3], and [SplitEither].
type SwitchOptions struct {
	// Buffer is each output channel's buffer. Default 0.
	Buffer int
	// Observer receives per-item [stats.StreamObserver.RecordStreamItem]
	// events (location = the case name / key / type-case index). Nil means
	// resolved from ctx.
	Observer stats.Observer
}

// validateCases panics on malformed case slices — these are programming
// errors in a literal slice (empty name, duplicate name, nil predicate),
// caught on first run; not runtime conditions.
func validateCases[T any](cases []Case[T]) {
	seen := make(map[string]bool, len(cases))
	for i, c := range cases {
		if c.Name == "" {
			panic(fmt.Sprintf("stream.Switch: case %d has an empty Name", i))
		}
		if seen[c.Name] {
			panic(fmt.Sprintf("stream.Switch: duplicate case name %q", c.Name))
		}
		if c.When == nil {
			panic(fmt.Sprintf("stream.Switch: case %q has a nil When predicate", c.Name))
		}
		seen[c.Name] = true
	}
}

// Switch routes each item to the FIRST case whose predicate matches; items
// matching no case go to the returned default stream. All case streams exist
// up front — the case set is static, unlike [GroupBy]'s dynamic keys — and
// out[i] corresponds to cases[i] (positional, compile-time checkable at call
// sites; names serve observability and topology).
//
// Errors from src are forwarded to the default stream only (single
// ownership — no duplicate error handling across cases). All streams close
// when src terminates or ctx is cancelled.
//
// Malformed cases (empty or duplicate Name, nil When) panic at call time —
// programming errors, not runtime conditions.
//
//	caseStreams, rest := stream.Switch(ctx, readings, []stream.Case[Reading]{
//	    {Name: "alert",   When: func(r Reading) bool { return r.Value > 90 }},
//	    {Name: "warning", When: func(r Reading) bool { return r.Value > 70 }},
//	}, stream.SwitchOptions{})
//	go alertsPort.Feed(ctx, caseStreams[0])
//	go warningsPort.Feed(ctx, caseStreams[1])
//	go archivePort.Feed(ctx, rest)
func Switch[T any](
	ctx context.Context,
	src Stream[T],
	cases []Case[T],
	opts SwitchOptions,
) (out []Stream[T], rest Stream[T]) {
	validateCases(cases)
	names := make([]string, len(cases))
	for i, c := range cases {
		names[i] = c.Name
	}
	select_ := func(v T) int {
		for i, c := range cases {
			if c.When(v) {
				return i
			}
		}
		return -1
	}
	return switchBy(ctx, src, len(cases), names, select_, opts)
}

// SwitchKey routes each item by key to the case stream whose key matches —
// the keyed sibling of [Switch] (predicates) and the static sibling of
// [GroupBy] (dynamic keys). keys is the declared case set; out[i]
// corresponds to keys[i]. Items whose key is not in keys — and src errors —
// go to rest.
//
// SwitchKey pairs naturally with [codex.TaggedUnion]: declare the
// discriminator as a NAMED function once and pass it to both the codec and
// the router — wire format, schema, spec, and routing then share one source
// of truth:
//
//	func orderEventKind(e OrderEvent) string { return e.Kind }
//	var orderEventCodec = codex.TaggedUnion("kind", variants,
//	    func(e OrderEvent) (string, error) { return orderEventKind(e), nil })
//
//	streams, rest := stream.SwitchKey(ctx, events,
//	    []string{"created", "cancelled"}, orderEventKind, stream.SwitchOptions{})
func SwitchKey[T any, K comparable](
	ctx context.Context,
	src Stream[T],
	keys []K,
	keyOf func(T) K,
	opts SwitchOptions,
) (out []Stream[T], rest Stream[T]) {
	index := make(map[K]int, len(keys))
	names := make([]string, len(keys))
	for i, k := range keys {
		if _, dup := index[k]; dup {
			panic(fmt.Sprintf("stream.SwitchKey: duplicate key %v", k))
		}
		index[k] = i
		names[i] = fmt.Sprint(k)
	}
	select_ := func(v T) int {
		if i, ok := index[keyOf(v)]; ok {
			return i
		}
		return -1
	}
	return switchBy(ctx, src, len(keys), names, select_, opts)
}

// switchBy is the shared dispatch loop behind Switch and SwitchKey:
// select_ returns the case index for an item, or -1 for the rest stream.
func switchBy[T any](
	ctx context.Context,
	src Stream[T],
	n int,
	names []string,
	select_ func(T) int,
	opts SwitchOptions,
) (out []Stream[T], rest Stream[T]) {
	obs := opts.Observer
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}
	caseVals := make([]chan T, n)
	caseErrs := make([]chan error, n)
	out = make([]Stream[T], n)
	for i := range caseVals {
		caseVals[i] = make(chan T, opts.Buffer)
		caseErrs[i] = make(chan error) // stays empty; closed with the case
		out[i] = Stream[T]{Values: caseVals[i], Errors: caseErrs[i]}
	}
	// The rest stream owns non-matches AND src errors.
	restVals := make(chan T, opts.Buffer)
	restErrs := make(chan error, opts.Buffer)
	rest = Stream[T]{Values: restVals, Errors: restErrs}

	go func() {
		defer func() {
			for i := range caseVals {
				close(caseVals[i])
				close(caseErrs[i])
			}
			close(restVals)
			close(restErrs)
		}()
		valCh := src.Values
		errCh := src.Errors
		for valCh != nil || errCh != nil {
			select {
			case <-ctx.Done():
				return
			case v, ok := <-valCh:
				if !ok {
					valCh = nil
					continue
				}
				start := time.Now()
				idx := select_(v)
				var dst chan T
				location := "rest"
				if idx >= 0 {
					dst = caseVals[idx]
					location = names[idx]
				} else {
					dst = restVals
				}
				select {
				case dst <- v:
				case <-ctx.Done():
					return
				}
				if so, ok2 := obs.(stats.StreamObserver); ok2 {
					so.RecordStreamItem(location, true, time.Since(start))
				}
			case e, ok := <-errCh:
				if !ok {
					errCh = nil
					continue
				}
				select {
				case restErrs <- e:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out, rest
}

// ── Type routing ──────────────────────────────────────────────────────────────

// OfType filters src to items whose dynamic type is U, emitting them as a
// typed Stream[U]. Meaningful when T is an interface (sum type) — in a
// concretely-typed stream every item already IS a T. Items of other types
// are dropped silently; errors are forwarded. The single-case building
// block — for multi-case routing use [SwitchType2]/[SwitchType3].
//
// The observer is resolved from ctx ([stats.ObserverFromContext]) and
// receives RecordStreamItem with location "oftype" per matched item.
//
//	events  := … // Stream[DomainEvent] (interface)
//	created := stream.OfType[OrderCreated](ctx, events)
func OfType[U any, T any](ctx context.Context, src Stream[T]) Stream[U] {
	obs := stats.ObserverFromContext(ctx)
	so, hasSO := obs.(stats.StreamObserver)
	values := make(chan U)
	errs := make(chan error)
	go func() {
		defer close(values)
		defer close(errs)
		valCh := src.Values
		errCh := src.Errors
		for valCh != nil || errCh != nil {
			select {
			case <-ctx.Done():
				return
			case v, ok := <-valCh:
				if !ok {
					valCh = nil
					continue
				}
				start := time.Now()
				u, ok2 := any(v).(U)
				if !ok2 {
					continue // other types dropped silently
				}
				select {
				case values <- u:
					if hasSO {
						so.RecordStreamItem("oftype", true, time.Since(start))
					}
				case <-ctx.Done():
					return
				}
			case e, ok := <-errCh:
				if !ok {
					errCh = nil
					continue
				}
				select {
				case errs <- e:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return Stream[U]{Values: values, Errors: errs}
}

// SwitchType2 routes a sum-typed stream into two TYPED case streams plus the
// untyped rest — the pattern-matching shape Go can express without variadic
// type parameters. First match wins (A before B — relevant when types
// overlap via embedding); items of other types and src errors go to rest.
//
// For more cases use [SwitchType3] or compose on the rest stream — the same
// nesting guidance as CombineLatest beyond 4 sources.
//
//	created, cancelled, other := stream.SwitchType2[OrderCreated, OrderCancelled](ctx, events, opts)
func SwitchType2[A any, B any, T any](
	ctx context.Context,
	src Stream[T],
	opts SwitchOptions,
) (Stream[A], Stream[B], Stream[T]) {
	obs := opts.Observer
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}
	aVals := make(chan A, opts.Buffer)
	aErrs := make(chan error)
	bVals := make(chan B, opts.Buffer)
	bErrs := make(chan error)
	restVals := make(chan T, opts.Buffer)
	restErrs := make(chan error, opts.Buffer)

	go func() {
		defer func() {
			close(aVals)
			close(aErrs)
			close(bVals)
			close(bErrs)
			close(restVals)
			close(restErrs)
		}()
		valCh := src.Values
		errCh := src.Errors
		for valCh != nil || errCh != nil {
			select {
			case <-ctx.Done():
				return
			case v, ok := <-valCh:
				if !ok {
					valCh = nil
					continue
				}
				start := time.Now()
				location := "switchtype.rest"
				delivered := false
				if a, ok2 := any(v).(A); ok2 {
					location = "switchtype.0"
					select {
					case aVals <- a:
						delivered = true
					case <-ctx.Done():
						return
					}
				} else if b, ok2 := any(v).(B); ok2 {
					location = "switchtype.1"
					select {
					case bVals <- b:
						delivered = true
					case <-ctx.Done():
						return
					}
				}
				if !delivered {
					select {
					case restVals <- v:
					case <-ctx.Done():
						return
					}
				}
				if so, ok2 := obs.(stats.StreamObserver); ok2 {
					so.RecordStreamItem(location, true, time.Since(start))
				}
			case e, ok := <-errCh:
				if !ok {
					errCh = nil
					continue
				}
				select {
				case restErrs <- e:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return Stream[A]{Values: aVals, Errors: aErrs},
		Stream[B]{Values: bVals, Errors: bErrs},
		Stream[T]{Values: restVals, Errors: restErrs}
}

// SwitchType3 routes a sum-typed stream into three TYPED case streams plus
// the untyped rest. Same contract as [SwitchType2]: first match wins (A,
// then B, then C); other types and src errors go to rest.
func SwitchType3[A any, B any, C any, T any](
	ctx context.Context,
	src Stream[T],
	opts SwitchOptions,
) (Stream[A], Stream[B], Stream[C], Stream[T]) {
	obs := opts.Observer
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}
	aVals := make(chan A, opts.Buffer)
	aErrs := make(chan error)
	bVals := make(chan B, opts.Buffer)
	bErrs := make(chan error)
	cVals := make(chan C, opts.Buffer)
	cErrs := make(chan error)
	restVals := make(chan T, opts.Buffer)
	restErrs := make(chan error, opts.Buffer)

	go func() {
		defer func() {
			close(aVals)
			close(aErrs)
			close(bVals)
			close(bErrs)
			close(cVals)
			close(cErrs)
			close(restVals)
			close(restErrs)
		}()
		valCh := src.Values
		errCh := src.Errors
		for valCh != nil || errCh != nil {
			select {
			case <-ctx.Done():
				return
			case v, ok := <-valCh:
				if !ok {
					valCh = nil
					continue
				}
				start := time.Now()
				location := "switchtype.rest"
				delivered := false
				if a, ok2 := any(v).(A); ok2 {
					location = "switchtype.0"
					select {
					case aVals <- a:
						delivered = true
					case <-ctx.Done():
						return
					}
				} else if b, ok2 := any(v).(B); ok2 {
					location = "switchtype.1"
					select {
					case bVals <- b:
						delivered = true
					case <-ctx.Done():
						return
					}
				} else if c, ok2 := any(v).(C); ok2 {
					location = "switchtype.2"
					select {
					case cVals <- c:
						delivered = true
					case <-ctx.Done():
						return
					}
				}
				if !delivered {
					select {
					case restVals <- v:
					case <-ctx.Done():
						return
					}
				}
				if so, ok2 := obs.(stats.StreamObserver); ok2 {
					so.RecordStreamItem(location, true, time.Since(start))
				}
			case e, ok := <-errCh:
				if !ok {
					errCh = nil
					continue
				}
				select {
				case restErrs <- e:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return Stream[A]{Values: aVals, Errors: aErrs},
		Stream[B]{Values: bVals, Errors: bErrs},
		Stream[C]{Values: cVals, Errors: cErrs},
		Stream[T]{Values: restVals, Errors: restErrs}
}

// ── SplitEither ───────────────────────────────────────────────────────────────

// SplitEither splits a stream of [codex.Either] values into its two typed
// branches — the codec-native alternative to [SwitchType2] when the boundary
// decoded a wire-level union via codex.Either2. The split is TOTAL: every
// item is Left or Right, so there is no rest stream (a strictly stronger
// contract than SwitchType2, and no interface type required).
//
// Errors from src are forwarded to BOTH branches (independent consumers —
// GroupBy fan-out semantics). Both branches close when src terminates.
//
//	// boundary decodes into the sum; the pipeline splits it — typed end to end
//	created, cancelled := stream.SplitEither(ctx, events, stream.SwitchOptions{})
func SplitEither[A any, B any](
	ctx context.Context,
	src Stream[codex.Either[A, B]],
	opts SwitchOptions,
) (Stream[A], Stream[B]) {
	obs := opts.Observer
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}
	aVals := make(chan A, opts.Buffer)
	aErrs := make(chan error, opts.Buffer)
	bVals := make(chan B, opts.Buffer)
	bErrs := make(chan error, opts.Buffer)

	go func() {
		defer func() {
			close(aVals)
			close(aErrs)
			close(bVals)
			close(bErrs)
		}()
		valCh := src.Values
		errCh := src.Errors
		for valCh != nil || errCh != nil {
			select {
			case <-ctx.Done():
				return
			case v, ok := <-valCh:
				if !ok {
					valCh = nil
					continue
				}
				start := time.Now()
				location := "either.left"
				if v.Left != nil {
					select {
					case aVals <- *v.Left:
					case <-ctx.Done():
						return
					}
				} else if v.Right != nil {
					location = "either.right"
					select {
					case bVals <- *v.Right:
					case <-ctx.Done():
						return
					}
				} else {
					continue // zero Either — nothing to deliver
				}
				if so, ok2 := obs.(stats.StreamObserver); ok2 {
					so.RecordStreamItem(location, true, time.Since(start))
				}
			case e, ok := <-errCh:
				if !ok {
					errCh = nil
					continue
				}
				// Fan out to both branches (non-blocking per branch).
				select {
				case aErrs <- e:
				default:
				}
				select {
				case bErrs <- e:
				default:
				}
			}
		}
	}()
	return Stream[A]{Values: aVals, Errors: aErrs}, Stream[B]{Values: bVals, Errors: bErrs}
}
