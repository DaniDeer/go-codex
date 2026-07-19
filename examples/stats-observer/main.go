// Package stats-observer demonstrates how to use [stats.ValidationObserver] and
// [stats.ReportErrors] with codecs directly — without any HTTP or MQTT adapter.
//
// This is the codec-only observability path: implement just [stats.ValidationObserver]
// (one method) and call [stats.ReportErrors] after each [codex.Codec.Decode].
//
// Scenario: an application validates its config file on startup. Invalid config
// fields emit [stats.RecordValidationError] events that a production observer
// would route to Prometheus counters, structured logs, or alerting.
//
// The final section demonstrates [stats.TraceObserver] — an additive optional
// interface for distributed tracing. Implement StartSpan/EndSpan against your
// tracing backend (OpenTelemetry, Datadog, etc.) and wire it via [stats.NewFanout]
// alongside metrics and logging observers.
//
// The last section demonstrates the observer pattern in PLAIN BUSINESS LOGIC —
// a function with no pipeline, no adapter, just a direct call — using the same
// [stats.ObserverFromContext] lookup adapters use internally, a manual
// [stats.TraceObserver] span, and a custom domain-specific observer interface
// (OrderObserver) defined outside the stats package. It also demonstrates the
// one real gotcha: [stats.NewFanout] never forwards custom interfaces.
//
// Run with: go run ./examples/stats-observer
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/stats"
	"github.com/DaniDeer/go-codex/validate"
)

// ── Domain model ─────────────────────────────────────────────────────────────

type AppConfig struct {
	ServerURL  string `json:"server_url"`
	MaxWorkers int    `json:"max_workers"`
	APIKey     string `json:"api_key"`
}

var appConfigCodec = codex.Struct[AppConfig](
	codex.RequiredField("server_url", codex.String().Refine(validate.URL), func(c AppConfig) string { return c.ServerURL }, func(c *AppConfig, v string) { c.ServerURL = v }),
	codex.RequiredField("max_workers", codex.Int().Refine(validate.MinInt(1)), func(c AppConfig) int { return c.MaxWorkers }, func(c *AppConfig, v int) { c.MaxWorkers = v }),
	codex.RequiredField("api_key", codex.String().Refine(validate.MinLen(16)), func(c AppConfig) string { return c.APIKey }, func(c *AppConfig, v string) { c.APIKey = v }),
)

// ── Observer ─────────────────────────────────────────────────────────────────

// ConfigObserver implements [stats.Observer] — pure metrics counting, no logging.
// Embed [stats.NoopObserver] to satisfy the full Observer interface without boilerplate.
// Combine with [stats.NewLoggingObserver] via [stats.NewFanout] for logging.
//
// A production observer would call prometheus.CounterVec.With(...).Inc() here.
type ConfigObserver struct {
	stats.NoopObserver
	errors []configError
}

type configError struct {
	location   string
	constraint string
	field      string
}

// RecordValidationError implements [stats.ValidationObserver].
func (o *ConfigObserver) RecordValidationError(location, constraint, field string) {
	o.errors = append(o.errors, configError{location: location, constraint: constraint, field: field})
}

// demoTraceObserver implements [stats.TraceObserver] — records spans in memory.
// A production implementation would call otel.Tracer("go-codex").Start(ctx, operation, ...).
type demoTraceObserver struct {
	stats.NoopObserver
	operations []string
	errCount   int
}

func (t *demoTraceObserver) StartSpan(ctx context.Context, operation, name string) context.Context {
	t.operations = append(t.operations, operation+":"+name)
	return ctx
}

func (t *demoTraceObserver) EndSpan(ctx context.Context, err error) {
	if err != nil {
		t.errCount++
	}
}

func (o *ConfigObserver) Print() {
	fmt.Printf("  total validation errors: %d\n", len(o.errors))
	for _, e := range o.errors {
		fmt.Printf("    %-20s constraint=%-20s field=%s\n", e.location, e.constraint, e.field)
	}
}

// ── Business logic without pipelines ────────────────────────────────────────
//
// Everything above wires the observer into codec calls. This section shows
// the observer used directly inside a plain business function — no
// forge.Function, no stream.Stream, just a normal Go call.

// Order is the domain type placeOrder operates on — plain business data,
// no codec involved (that's a deliberate contrast with the codec-only
// sections above: the observer pattern works identically whether or not
// codecs are in the picture).
type Order struct {
	ID     string
	Amount float64
}

// OrderObserver is a domain-specific observer extension — NOT part of the
// stats package. Business code defines its own optional interfaces exactly
// like the built-in ones (SQLObserver, CacheObserver): small, RecordXxx-named,
// implemented optionally, type-asserted at the call site.
type OrderObserver interface {
	RecordOrderPlaced(orderID string, amount float64, d time.Duration)
}

// OrderMetrics implements OrderObserver. A production version would push to
// Prometheus (counter.Inc(), histogram.Observe(amount)) instead of counting
// in memory.
type OrderMetrics struct {
	stats.NoopObserver
	placed int
	total  float64
}

func (m *OrderMetrics) RecordOrderPlaced(_ string, amount float64, _ time.Duration) {
	m.placed++
	m.total += amount
}

// orderObservers fans out RecordOrderPlaced to multiple OrderObserver
// implementations — the same one-line pattern stats.NewFanout uses
// internally, scoped to the custom interface. It embeds stats.NoopObserver
// so it ALSO satisfies stats.Observer — required by stats.WithObserver —
// letting it be stored directly in ctx and picked up transparently by
// placeOrder's obs.(OrderObserver) assertion (unlike stats.NewFanout(...),
// which never forwards custom interfaces — see main() below).
type orderObservers struct {
	stats.NoopObserver
	observers []OrderObserver
}

func (o orderObservers) RecordOrderPlaced(id string, amount float64, d time.Duration) {
	for _, obs := range o.observers {
		obs.RecordOrderPlaced(id, amount, d)
	}
}

// placeOrder is a plain business function: no pipeline, no adapter. It still
// participates fully in the observer pattern:
//  1. resolves the observer from ctx exactly like adapters do internally
//  2. wraps itself in a manual TraceObserver span
//  3. emits a custom domain event (RecordOrderPlaced) via type assertion
func placeOrder(ctx context.Context, order Order) (err error) {
	start := time.Now()
	obs := stats.ObserverFromContext(ctx) // same lookup every adapter uses

	if to, ok := obs.(stats.TraceObserver); ok {
		ctx = to.StartSpan(ctx, "business.op", "placeOrder")
		defer func() { to.EndSpan(ctx, err) }()
	}

	// ... business logic would go here (validate stock, charge payment, etc.) ...
	if order.Amount <= 0 {
		err = fmt.Errorf("order %s: amount must be positive", order.ID)
		return err
	}

	if oo, ok := obs.(OrderObserver); ok {
		oo.RecordOrderPlaced(order.ID, order.Amount, time.Since(start))
	}
	return nil
}

// ── Main ─────────────────────────────────────────────────────────────────────

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	slog.SetDefault(logger)

	configObs := &ConfigObserver{}
	obs := stats.NewFanout(configObs, stats.NewLoggingObserver(logger))

	// ── Valid config ──────────────────────────────────────────────────────────

	fmt.Println("=== Valid config ===")
	validRaw := map[string]any{
		"server_url":  "https://api.example.com",
		"max_workers": 4,
		"api_key":     "supersecretkey1234",
	}
	cfg, err := appConfigCodec.Decode(validRaw)
	stats.ReportErrors(obs, "config", err)
	if err != nil {
		fmt.Printf("  config error: %v\n", err)
	} else {
		fmt.Printf("  config ok: url=%q workers=%d\n", cfg.ServerURL, cfg.MaxWorkers)
	}

	// ── Invalid config: bad URL, zero workers, short key ─────────────────────

	fmt.Println("\n=== Invalid config (bad URL, zero workers, short key) ===")
	badRaw := map[string]any{
		"server_url":  "not-a-url",
		"max_workers": 0,
		"api_key":     "short",
	}
	_, err = appConfigCodec.Decode(badRaw)
	stats.ReportErrors(obs, "config", err) // RecordValidationError called per failing field
	if err != nil {
		fmt.Printf("  config error: %v\n", err)
	}

	// ── Partial config: missing required field ────────────────────────────────

	fmt.Println("\n=== Partial config (missing api_key) ===")
	partialRaw := map[string]any{
		"server_url":  "https://api.example.com",
		"max_workers": 2,
		// api_key intentionally missing
	}
	_, err = appConfigCodec.Decode(partialRaw)
	stats.ReportErrors(obs, "config", err) // constraint="required" for missing field
	if err != nil {
		fmt.Printf("  config error: %v\n", err)
	}

	fmt.Println("\n=== Observer summary ===")
	configObs.Print()

	// ── TraceObserver demo ─────────────────────────────────────────────────────

	fmt.Println("\n=== TraceObserver demo ===")
	traceObs := &demoTraceObserver{}
	tracedObs := stats.NewFanout(configObs, stats.NewLoggingObserver(logger), traceObs)

	// Simulate a request that creates a root span and a child span.
	ctx := context.Background()
	if to, ok := tracedObs.(stats.TraceObserver); ok {
		ctx = to.StartSpan(ctx, "http.request", "/users/{id}")
		if childTo, ok := tracedObs.(stats.TraceObserver); ok {
			childCtx := childTo.StartSpan(ctx, "db.query", "SELECT * FROM users WHERE id = $1")
			childTo.EndSpan(childCtx, errors.New("connection timeout"))
		}
		to.EndSpan(ctx, nil)
	}

	// Simulate a failed request.
	if to, ok := tracedObs.(stats.TraceObserver); ok {
		ctx = to.StartSpan(context.Background(), "forge.apply", "oeeCalc")
		to.EndSpan(ctx, errors.New("input validation failed"))
	}

	fmt.Printf("  TraceObserver: operations=%v errors=%d\n", traceObs.operations, traceObs.errCount)

	// ── Default observer via context ───────────────────────────────────────────
	//
	// stats.WithObserver stores an observer in a context.Context. Adapters,
	// stream bridges, and ports.File read it automatically via
	// stats.ObserverFromContext(ctx) when Options.Observer is nil. The next
	// section (placeOrder) demonstrates that this same lookup works from
	// plain business logic too — not just adapters.
	fmt.Println("\n=== Default observer via context ===")

	// Establish a default observer in a context:
	defaultObs := &ConfigObserver{}
	ctxWithObs := stats.WithObserver(context.Background(), defaultObs)

	// Retrieve it anywhere downstream — adapters do this internally:
	retrieved := stats.ObserverFromContext(ctxWithObs)
	_, validRaw2 := appConfigCodec.Decode(map[string]any{
		"server_url": "not-a-url", "max_workers": 0, "api_key": "x",
	})
	stats.ReportErrors(retrieved, "config", validRaw2)
	fmt.Printf("  default observer recorded %d error(s)\n", len(defaultObs.errors))

	// Override: explicit observer beats the context default.
	// Create an overriding observer for a sensitive sub-operation:
	overrideObs := &ConfigObserver{}
	// Even though ctxWithObs carries defaultObs, passing overrideObs explicitly
	// to ReportErrors uses overrideObs — explicit always wins.
	stats.ReportErrors(overrideObs, "config", validRaw2) // explicit
	fmt.Printf("  override observer recorded %d error(s) (explicit beats context)\n",
		len(overrideObs.errors))
	// defaultObs is unaffected by the explicit call above.
	fmt.Printf("  default observer still at %d error(s) (not called for override)\n",
		len(defaultObs.errors))

	// No context observer set → ObserverFromContext returns NoopObserver:
	noop := stats.ObserverFromContext(context.Background())
	_, isNoop := noop.(stats.NoopObserver)
	fmt.Printf("  ObserverFromContext(context.Background()) is NoopObserver: %v\n", isNoop)

	// For the full adapter-level context observer pattern, see:
	// - examples/adapters-nethttp — ObserverMiddleware injects obs per HTTP request
	// - examples/sensor-service   — ctx = stats.WithObserver(ctx, obs) for MQTT + stream + SQL

	// ── Business logic without pipelines ────────────────────────────────────
	//
	// placeOrder is a plain business function — no forge.Function, no
	// stream.Stream, just a direct call. It still fully participates in the
	// observer pattern: ctx-resolved observer, a manual TraceObserver span,
	// and a custom domain event (OrderObserver) defined outside stats.
	fmt.Println("\n=== Business logic without pipelines ===")

	orderMetrics := &OrderMetrics{}
	orderTracer := &demoTraceObserver{}

	// Pitfall: wiring only a stats.NewFanout(...) into ctx means placeOrder's
	// obs.(OrderObserver) assertion ALWAYS fails — NewFanout only forwards
	// the nine BUILT-IN stats interfaces; a custom interface defined outside
	// the stats package (OrderObserver) is invisible to it, even though
	// orderMetrics (one of the fanned-out observers) implements it.
	fanoutOnly := stats.NewFanout(orderMetrics, stats.NewLoggingObserver(logger), orderTracer)
	_, fanoutHasOrderObserver := fanoutOnly.(OrderObserver)
	fmt.Printf("  stats.NewFanout(...).(OrderObserver) ok=%v (always false — custom interfaces are never forwarded)\n",
		fanoutHasOrderObserver)

	pitfallCtx := stats.WithObserver(context.Background(), fanoutOnly)
	_ = placeOrder(pitfallCtx, Order{ID: "ord-pitfall", Amount: 42.50})
	fmt.Printf("  after placeOrder via fanout-only ctx: placed=%d (RecordOrderPlaced never fired)\n",
		orderMetrics.placed)

	// Safe pattern (a): type-assert / call the concrete observer directly for
	// custom events, keeping the fanout for the nine standard interfaces.
	// orderMetrics still receives RecordValidationError etc. via fanoutOnly
	// (it embeds NoopObserver and satisfies Observer); only the CUSTOM event
	// needs the direct reference:
	if err := placeOrder(pitfallCtx, Order{ID: "ord-1", Amount: 42.50}); err == nil {
		orderMetrics.RecordOrderPlaced("ord-1", 42.50, time.Millisecond) // pattern (a)
	}
	fmt.Printf("  after calling orderMetrics.RecordOrderPlaced directly: placed=%d total=%.2f\n",
		orderMetrics.placed, orderMetrics.total)

	// Safe pattern (b): a tiny multi-observer scoped to the custom interface —
	// stored directly in ctx instead of a stats.NewFanout(...), so placeOrder's
	// obs.(OrderObserver) assertion succeeds without any extra plumbing at the
	// call site:
	multiOrderObs := orderObservers{observers: []OrderObserver{orderMetrics}}
	workingCtx := stats.WithObserver(context.Background(), multiOrderObs)
	if err := placeOrder(workingCtx, Order{ID: "ord-2", Amount: 17.25}); err != nil {
		fmt.Printf("  order error: %v\n", err)
	}
	fmt.Printf("  after placeOrder via custom-multi-observer ctx: placed=%d total=%.2f\n",
		orderMetrics.placed, orderMetrics.total)

	// Rejected order: RecordOrderPlaced never fires for invalid input.
	if err := placeOrder(workingCtx, Order{ID: "ord-3", Amount: -5}); err != nil {
		fmt.Printf("  order error: %v\n", err)
	}
	fmt.Printf("  TraceObserver spans during placeOrder calls: %v\n", orderTracer.operations)
}
