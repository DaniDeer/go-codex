// Package main demonstrates config.FromEnv: loading application configuration
// exclusively from environment variables using the codec as the single source
// of truth for field names, types, validations, and documentation.
//
// No config file is involved. The codec's schema drives type coercion so that
// env var strings are converted to the correct Go types before decoding.
//
// One codec, all input formats — the same configCodec defined below is used
// for every config.FromEnv call. Whether env vars are plain strings ("8080"),
// comma-separated lists ("web,api"), or full JSON objects/arrays, config.FromEnv
// parses the raw string and builds an intermediate map[string]any, then calls
// configCodec.Decode — the exact same Decode path used by format.JSON, format.TOML,
// and format.YAML. No special codec is needed for JSON-formatted env vars.
//
// Naming convention: strings.ToUpper(prefix + field_name)
//
//	field "port"        + prefix "APP_" → APP_PORT
//	field "log_level"   + prefix "APP_" → APP_LOG_LEVEL
//	field "db" (nested) + prefix "APP_" → APP_DB_ prefix for its sub-fields
//	nested field "host"                 → APP_DB_HOST
//
// The example also shows validate.EnvVarName and validate.EnvVarPrefix for
// validating user-supplied env var names before passing them to FromEnvVar.
//
// Run with: go run ./examples/env-config
package main

import (
	"fmt"
	"os"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/config"
	"github.com/DaniDeer/go-codex/render/openapi"
	"github.com/DaniDeer/go-codex/schema"
	"github.com/DaniDeer/go-codex/validate"
)

// DBConfig holds database connection settings.
type DBConfig struct {
	Host string
	Port int
	Name string
}

// AppConfig is the full application configuration.
type AppConfig struct {
	Host       string
	Port       int
	LogLevel   string
	Workers    int
	DB         DBConfig
	Tags       []string
	Labels     map[string]string
	Extensions any // arbitrary extra config — Any() passes it through unchanged
}

var dbCodec = codex.Struct[DBConfig](
	codex.RequiredField("host", codex.String().Refine(validate.NonEmptyString).WithDescription("Database host."), func(c DBConfig) string { return c.Host }, func(c *DBConfig, v string) { c.Host = v }),
	codex.RequiredField("port", codex.Int().Refine(validate.RangeInt(1, 65535)).WithDescription("Database port (1–65535)."), func(c DBConfig) int { return c.Port }, func(c *DBConfig, v int) { c.Port = v }),
	codex.RequiredField("name", codex.String().Refine(validate.NonEmptyString).WithDescription("Database name."), func(c DBConfig) string { return c.Name }, func(c *DBConfig, v string) { c.Name = v }),
)

var configCodec = codex.Struct[AppConfig](
	codex.RequiredField("host", codex.String().Refine(validate.NonEmptyString).WithDescription("Server bind address."), func(c AppConfig) string { return c.Host }, func(c *AppConfig, v string) { c.Host = v }),
	codex.RequiredField("port", codex.Int().Refine(validate.RangeInt(1, 65535)).WithDescription("Server port (1–65535)."), func(c AppConfig) int { return c.Port }, func(c *AppConfig, v int) { c.Port = v }),
	// DefaultField: when APP_LOG_LEVEL is absent, "info" is used automatically.
	// The default value is also reflected in the generated JSON Schema.
	codex.DefaultField(
		"log_level",
		codex.String().Refine(validate.OneOf("debug", "info", "warn", "error")).WithDescription("Minimum log severity."),
		"info",
		func(c AppConfig) string { return c.LogLevel },
		func(c *AppConfig, v string) { c.LogLevel = v },
	),
	codex.RequiredField("workers", codex.Int().Refine(validate.RangeInt(1, 256)).WithDescription("Worker goroutines (1–256)."), func(c AppConfig) int { return c.Workers }, func(c *AppConfig, v int) { c.Workers = v }),
	codex.RequiredField("db", dbCodec.WithDescription("Database connection settings."), func(c AppConfig) DBConfig { return c.DB }, func(c *AppConfig, v DBConfig) { c.DB = v }),
	codex.OptionalField("tags", codex.SliceOf(codex.String()).WithDescription("Optional deployment tags (comma-separated)."), func(c AppConfig) []string { return c.Tags }, func(c *AppConfig, v []string) { c.Tags = v }),
	codex.OptionalField("labels", codex.StringMap(codex.String()).WithDescription("Arbitrary key-value labels (JSON object)."), func(c AppConfig) map[string]string { return c.Labels }, func(c *AppConfig, v map[string]string) { c.Labels = v }),
	// Any(): Extensions passes the raw value through without type enforcement.
	// config.FromEnv parses JSON-prefixed strings into map[string]any automatically,
	// so APP_EXTENSIONS='{"flag":true}' arrives as map[string]any after env loading.
	// Any() accepts it as-is with no further schema constraint.
	codex.OptionalField("extensions", codex.Any().WithDescription("Arbitrary extension config (raw JSON object or nil)."), func(c AppConfig) any { return c.Extensions }, func(c *AppConfig, v any) { c.Extensions = v }),
)

func mustSetenv(k, v string) {
	if err := os.Setenv(k, v); err != nil {
		panic(err)
	}
}

func main() {
	// ── 1. Valid config — flat + nested + slice ───────────────────────────────
	fmt.Println("=== 1. Valid config from env vars ===")

	mustSetenv("APP_HOST", "0.0.0.0")
	mustSetenv("APP_PORT", "8080")
	mustSetenv("APP_LOG_LEVEL", "info")
	mustSetenv("APP_WORKERS", "4")
	mustSetenv("APP_DB_HOST", "db.internal")
	mustSetenv("APP_DB_PORT", "5432")
	mustSetenv("APP_DB_NAME", "myapp")
	mustSetenv("APP_TAGS", "production, web, v2")
	defer func() {
		for _, k := range []string{
			"APP_HOST", "APP_PORT", "APP_LOG_LEVEL", "APP_WORKERS",
			"APP_DB_HOST", "APP_DB_PORT", "APP_DB_NAME", "APP_TAGS", "APP_LABELS",
			"APP_EXTENSIONS",
		} {
			os.Unsetenv(k)
		}
	}()

	cfg, err := config.FromEnv(configCodec, "APP_")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Printf("host:      %s\n", cfg.Host)
	fmt.Printf("port:      %d\n", cfg.Port)
	fmt.Printf("log_level: %s\n", cfg.LogLevel)
	fmt.Printf("workers:   %d\n", cfg.Workers)
	fmt.Printf("db.host:   %s\n", cfg.DB.Host)
	fmt.Printf("db.port:   %d\n", cfg.DB.Port)
	fmt.Printf("db.name:   %s\n", cfg.DB.Name)
	fmt.Printf("tags:      %v\n", cfg.Tags)

	// ── 2. Parse error — wrong type ───────────────────────────────────────────
	fmt.Println("\n=== 2. Parse errors (env vars set but wrong type) ===")

	mustSetenv("APP_PORT", "notanumber")
	mustSetenv("APP_WORKERS", "toomanythreads")

	_, err = config.FromEnv(configCodec, "APP_")
	if err != nil {
		fmt.Println("errors:", err)
	}
	mustSetenv("APP_PORT", "8080")
	mustSetenv("APP_WORKERS", "4")

	// ── 3. Missing required — errors collected in one pass ───────────────────
	fmt.Println("\n=== 3. Missing required fields (errors collected in one pass) ===")

	os.Unsetenv("APP_DB_HOST")
	os.Unsetenv("APP_DB_NAME")

	_, err = config.FromEnv(configCodec, "APP_")
	if err != nil {
		fmt.Println("errors:", err)
	}
	mustSetenv("APP_DB_HOST", "db.internal")
	mustSetenv("APP_DB_NAME", "myapp")

	// ── 4. Constraint violation ───────────────────────────────────────────────
	fmt.Println("\n=== 4. Constraint violations ===")

	mustSetenv("APP_PORT", "99999")        // exceeds RangeInt(1, 65535)
	mustSetenv("APP_LOG_LEVEL", "verbose") // not in OneOf list
	mustSetenv("APP_WORKERS", "512")       // exceeds RangeInt(1, 256)

	_, err = config.FromEnv(configCodec, "APP_")
	if err != nil {
		fmt.Println("errors:", err)
	}
	mustSetenv("APP_PORT", "8080")
	mustSetenv("APP_LOG_LEVEL", "info")
	mustSetenv("APP_WORKERS", "4")

	// ── 5. JSON-in-env: nested struct, slice, StringMap ─────────────────────
	// Complex fields can be set as a single JSON value instead of individual
	// keys or comma-separated lists.
	//
	// No new codec is needed — configCodec is the same definition above.
	// config.FromEnv detects the JSON prefix ('{' or '['), unmarshals the
	// string into a map[string]any or []any, then calls configCodec.Decode
	// with that intermediate value — identical to what format.TOML or
	// format.JSON would do when parsing the same structure from a file.
	// All field validations (NonEmptyString, RangeInt, etc.) run unchanged.
	//
	// JSON takes precedence over prefix expansion and comma-split when set.
	fmt.Println("\n=== 5. JSON-in-env (nested struct, slice, StringMap) ===")

	mustSetenv("APP_DB", `{"host":"json-db.internal","port":5432,"name":"myapp"}`)
	mustSetenv("APP_TAGS", `["production","web","v2"]`)
	mustSetenv("APP_LABELS", `{"env":"prod","team":"platform","version":"2.0"}`)

	cfg, err = config.FromEnv(configCodec, "APP_")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Printf("db (from JSON): host=%s port=%d name=%s\n", cfg.DB.Host, cfg.DB.Port, cfg.DB.Name)
	fmt.Printf("tags (from JSON array): %v\n", cfg.Tags)
	fmt.Printf("labels (from JSON object): %v\n", cfg.Labels)

	os.Unsetenv("APP_DB")
	mustSetenv("APP_DB_HOST", "db.internal")
	mustSetenv("APP_DB_PORT", "5432")
	mustSetenv("APP_DB_NAME", "myapp")
	mustSetenv("APP_TAGS", "production, web, v2")

	// ── 6. DefaultField: absent env var uses declared default ─────────────────
	// APP_LOG_LEVEL is defined as DefaultField("info"). When not set, the codec
	// automatically fills in "info" — no special env-loading logic needed.
	// The default is also visible in the JSON Schema output below.
	fmt.Println("\n=== 6. DefaultField — absent APP_LOG_LEVEL uses default 'info' ===")

	os.Unsetenv("APP_LOG_LEVEL")

	cfg, err = config.FromEnv(configCodec, "APP_")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Printf("log_level (not set): %q  ← default applied\n", cfg.LogLevel)

	mustSetenv("APP_LOG_LEVEL", "debug")
	cfg, err = config.FromEnv(configCodec, "APP_")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Printf("log_level (set to debug): %q  ← override wins\n", cfg.LogLevel)
	mustSetenv("APP_LOG_LEVEL", "info")

	// ── 7. Any() field — raw extension config passthrough ────────────────────
	// Extensions is declared with Any(). config.FromEnv detects the JSON prefix
	// and parses the env var string into map[string]any before calling Decode.
	// Any() accepts the resulting value as-is — no schema constraint applied.
	fmt.Println("\n=== 7. Any() field — extensions as raw JSON object ===")

	mustSetenv("APP_EXTENSIONS", `{"feature_flags":{"dark_mode":true},"max_upload_mb":50}`)

	cfg, err = config.FromEnv(configCodec, "APP_")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Printf("extensions (%T): %v\n", cfg.Extensions, cfg.Extensions)

	os.Unsetenv("APP_EXTENSIONS")

	// ── 9. Validate env var names from external input ────────────────────────
	// When env var names come from outside Go code (a config file, CLI flag, or
	// user form), validate them before passing to config.FromEnvVar or os.LookupEnv.
	//
	// Two composable constraints:
	//   validate.EnvVarName         — POSIX format: [A-Z_][A-Z0-9_]*
	//   validate.EnvVarPrefix("X_") — namespace prefix check
	//
	// Hardcoded Go literals like "APP_PORT" do not need runtime validation;
	// use these constraints only when the name itself arrives as user input.
	fmt.Println("\n=== 8. Validate env var names from external input ===")

	// Format only — valid POSIX env var name format
	nameCodec := codex.String().Refine(validate.EnvVarName)

	for _, name := range []string{"APP_PORT", "LOG_LEVEL", "_INTERNAL", "X1"} {
		fmt.Printf("  valid format:   %q\n", name)
		_ = nameCodec.Validate(name) // always passes
	}
	for _, name := range []string{"log_level", "APP-PORT", "1STVAR", "APP PORT"} {
		if err := nameCodec.Validate(name); err != nil {
			fmt.Printf("  invalid format: %q → %v\n", name, err)
		}
	}

	// Format + namespace — must be valid POSIX AND start with "APP_"
	appVarCodec := codex.String().
		Refine(validate.EnvVarName).
		Refine(validate.EnvVarPrefix("APP_")).
		WithDescription("Valid APP_ namespace env var name")

	fmt.Println()
	for _, name := range []string{"APP_PORT", "APP_DB_HOST", "APP_LOG_LEVEL"} {
		fmt.Printf("  valid APP_ name:   %q\n", name)
		_ = appVarCodec.Validate(name)
	}
	for _, name := range []string{"DB_HOST", "LOG_LEVEL", "app_port"} {
		if err := appVarCodec.Validate(name); err != nil {
			fmt.Printf("  rejected:         %q → %v\n", name, err)
		}
	}

	// Use a validated name with FromEnvVar
	fmt.Println()
	userKey := "APP_PORT" // e.g. from a config file
	if err := appVarCodec.Validate(userKey); err != nil {
		fmt.Printf("  invalid key %q: %v\n", userKey, err)
	} else {
		// Safe to use: name is both valid POSIX format and in the APP_ namespace
		port, err := config.FromEnvVar(userKey, codex.Int().Refine(validate.RangeInt(1, 65535)))
		if err != nil {
			fmt.Printf("  APP_PORT read error: %v\n", err)
		} else {
			fmt.Printf("  APP_PORT value: %d\n", port)
		}
	}

	// ── 9. JSON Schema for documentation ─────────────────────────────────────
	fmt.Println("\n=== 9. JSON Schema (default value visible in log_level field) ===")

	jsonBytes, err := openapi.MarshalJSON(map[string]schema.Schema{
		"AppConfig": configCodec.Schema,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "schema error:", err)
		os.Exit(1)
	}
	fmt.Println(string(jsonBytes))
}
