// Package main demonstrates using go-codex for CLI tool configuration:
// loading a TOML config file and overlaying environment variable overrides.
//
// Pattern:
//  1. Define AppConfig and configCodec once — the single source of truth for
//     field names, types, validations, and documentation.
//  2. Decode the config file through the codec (catches all file errors upfront).
//  3. Apply environment variable overrides to the decoded struct.
//  4. Re-validate the final struct (catches bad env values with the same constraints).
//
// Run with: go run ./examples/cli-config
package main

import (
	"fmt"
	"os"
	"strconv"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/render/openapi"
	"github.com/DaniDeer/go-codex/schema"
	"github.com/DaniDeer/go-codex/validate"
)

// AppConfig holds all configuration for a hypothetical CLI tool.
type AppConfig struct {
	Host        string
	Port        int
	LogLevel    string
	DatabaseURL string
	Workers     int
}

// configCodec is the single source of truth: encoding, decoding, validation,
// and JSON Schema documentation all derive from this one definition.
var configCodec = codex.Struct[AppConfig](
	codex.Field[AppConfig, string]{
		Name: "host",
		Codec: codex.String().
			Refine(validate.NonEmptyString).
			WithTitle("Host").
			WithDescription("Hostname or IP address the server binds to."),
		Get:      func(c AppConfig) string { return c.Host },
		Set:      func(c *AppConfig, v string) { c.Host = v },
		Required: true,
	},
	codex.Field[AppConfig, int]{
		Name: "port",
		Codec: codex.Int().
			Refine(validate.RangeInt(1, 65535)).
			WithTitle("Port").
			WithDescription("TCP port the server listens on (1–65535)."),
		Get:      func(c AppConfig) int { return c.Port },
		Set:      func(c *AppConfig, v int) { c.Port = v },
		Required: true,
	},
	codex.Field[AppConfig, string]{
		Name: "log_level",
		Codec: codex.String().
			Refine(validate.OneOf("debug", "info", "warn", "error")).
			WithTitle("Log Level").
			WithDescription("Minimum log severity: debug, info, warn, or error."),
		Get:      func(c AppConfig) string { return c.LogLevel },
		Set:      func(c *AppConfig, v string) { c.LogLevel = v },
		Required: false,
	},
	codex.Field[AppConfig, string]{
		Name: "database_url",
		Codec: codex.String().
			Refine(validate.NonEmptyString).
			WithTitle("Database URL").
			WithDescription("Connection URL for the primary database."),
		Get:      func(c AppConfig) string { return c.DatabaseURL },
		Set:      func(c *AppConfig, v string) { c.DatabaseURL = v },
		Required: true,
	},
	codex.Field[AppConfig, int]{
		Name: "workers",
		Codec: codex.Int().
			Refine(validate.RangeInt(1, 256)).
			WithTitle("Workers").
			WithDescription("Number of worker goroutines (1–256)."),
		Get:      func(c AppConfig) int { return c.Workers },
		Set:      func(c *AppConfig, v int) { c.Workers = v },
		Required: true,
	},
)

var tomlFmt = format.TOML(configCodec)

// loadConfig decodes a TOML config file and overlays environment variable
// overrides. All validation errors — from the file and from env values — are
// collected before returning.
func loadConfig(tomlData []byte) (AppConfig, error) {
	cfg, err := tomlFmt.Unmarshal(tomlData)
	if err != nil {
		return AppConfig{}, fmt.Errorf("config file: %w", err)
	}

	applyEnvOverrides(&cfg)

	if err := configCodec.Validate(cfg); err != nil {
		return AppConfig{}, fmt.Errorf("env overrides: %w", err)
	}

	return cfg, nil
}

// applyEnvOverrides applies environment variable overrides on top of the
// already-decoded config. Unknown or missing variables are silently skipped;
// parse failures for typed fields are also silently skipped so that the
// subsequent Validate call reports them as structured validation errors.
//
// Supported variables:
//
//	APP_HOST        string
//	APP_PORT        integer
//	APP_LOG_LEVEL   string
//	APP_DB_URL      string
//	APP_WORKERS     integer
func applyEnvOverrides(cfg *AppConfig) {
	if v, ok := os.LookupEnv("APP_HOST"); ok {
		cfg.Host = v
	}
	if v, ok := os.LookupEnv("APP_PORT"); ok {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Port = n
		}
	}
	if v, ok := os.LookupEnv("APP_LOG_LEVEL"); ok {
		cfg.LogLevel = v
	}
	if v, ok := os.LookupEnv("APP_DB_URL"); ok {
		cfg.DatabaseURL = v
	}
	if v, ok := os.LookupEnv("APP_WORKERS"); ok {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Workers = n
		}
	}
}

func mustSetenv(k, v string) {
	if err := os.Setenv(k, v); err != nil {
		panic(err)
	}
}

func main() {
	validTOML := []byte(`
host         = "localhost"
port         = 8080
log_level    = "info"
database_url = "postgres://user:pass@localhost/mydb"
workers      = 4
`)

	// ── 1. Valid config from file only ────────────────────────────────────────
	fmt.Println("=== 1. Valid config from TOML file ===")

	cfg, err := loadConfig(validTOML)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Printf("host:         %s\n", cfg.Host)
	fmt.Printf("port:         %d\n", cfg.Port)
	fmt.Printf("log_level:    %s\n", cfg.LogLevel)
	fmt.Printf("database_url: %s\n", cfg.DatabaseURL)
	fmt.Printf("workers:      %d\n", cfg.Workers)

	// ── 2. Env var overrides on top of the file ───────────────────────────────
	fmt.Println("\n=== 2. Env var overrides (APP_PORT, APP_LOG_LEVEL, APP_WORKERS) ===")

	mustSetenv("APP_PORT", "9090")
	mustSetenv("APP_LOG_LEVEL", "debug")
	mustSetenv("APP_WORKERS", "16")

	cfg, err = loadConfig(validTOML)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Printf("port:      %d  (overridden from 8080)\n", cfg.Port)
	fmt.Printf("log_level: %s  (overridden from info)\n", cfg.LogLevel)
	fmt.Printf("workers:   %d  (overridden from 4)\n", cfg.Workers)

	os.Unsetenv("APP_PORT")
	os.Unsetenv("APP_LOG_LEVEL")
	os.Unsetenv("APP_WORKERS")

	// ── 3. Invalid env overrides — all errors collected in one pass ──────────
	// The TOML file is valid; the env vars carry invalid values.
	// loadConfig decodes the file successfully, applies the bad env values,
	// then re-validates and returns all constraint violations at once.
	fmt.Println("\n=== 3. Invalid env overrides — errors collected in one pass ===")

	mustSetenv("APP_PORT", "99999")      // exceeds RangeInt(1, 65535)
	mustSetenv("APP_LOG_LEVEL", "trace") // not in OneOf list
	mustSetenv("APP_WORKERS", "512")     // exceeds RangeInt(1, 256)
	defer func() {
		os.Unsetenv("APP_PORT")
		os.Unsetenv("APP_LOG_LEVEL")
		os.Unsetenv("APP_WORKERS")
	}()

	_, err = loadConfig(validTOML)
	if err != nil {
		fmt.Println("errors:", err)
	}

	// ── 4. JSON Schema for editor autocomplete ────────────────────────────────
	fmt.Println("\n=== 4. JSON Schema (for editor autocomplete / documentation) ===")

	jsonBytes, schemaErr := openapi.MarshalJSON(map[string]schema.Schema{
		"AppConfig": configCodec.Schema,
	})
	if schemaErr != nil {
		fmt.Fprintln(os.Stderr, "schema error:", schemaErr)
		os.Exit(1)
	}
	fmt.Println(string(jsonBytes))
}
