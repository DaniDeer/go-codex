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
// Run with: go run ./examples/stats-observer
package main

import (
	"fmt"

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

// ConfigObserver implements [stats.ValidationObserver] — the narrow codec-level
// interface. No HTTP or MQTT stubs required.
//
// A production observer would call prometheus.CounterVec.With(...).Inc() here.
type ConfigObserver struct {
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
	fmt.Printf("  [observer] validation error — location=%q constraint=%q field=%q\n",
		location, constraint, field)
}

func (o *ConfigObserver) Print() {
	fmt.Printf("  total validation errors: %d\n", len(o.errors))
	for _, e := range o.errors {
		fmt.Printf("    %-20s constraint=%-20s field=%s\n", e.location, e.constraint, e.field)
	}
}

// ── Main ─────────────────────────────────────────────────────────────────────

func main() {
	obs := &ConfigObserver{}

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
	obs.Print()
}
