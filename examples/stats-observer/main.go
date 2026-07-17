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
// Run with: go run ./examples/stats-observer
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

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
	// stats.ObserverFromContext(ctx) when Options.Observer is nil.
	//
	// This example is codec-only (stats.ReportErrors, codex.Codec — no adapters),
	// so there is no ctx-carrying adapter call to demonstrate here. The pattern
	// lives at the adapter layer.
	//
	// But the stats API itself can be demonstrated directly:
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
}
