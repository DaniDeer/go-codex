// Package config loads typed values from OS environment variables using
// schema-driven coercion.
//
// [FromEnv] and [FromEnvVar] are pure functions — no adapter, no
// [ports.Pattern], no port. Config loading is standalone by nature: it
// happens once at startup, before any pipeline or transport exists, so
// there is nothing to bind it to. This is different from [ports.File] and
// [ports.Cache], which ARE declarative descriptors bound via a Pattern to
// an adapter family (adapters/file, adapters/redis) — see
// [ports]'s "Design pattern: declarative descriptor + plain function"
// section for the full comparison across building blocks.
//
// # Struct loading — FromEnv
//
// [FromEnv][T] loads an entire struct from environment variables using the
// codec's schema for schema-driven type coercion:
//
//	cfg, err := config.FromEnv(configCodec, "APP_")
//	// err is codex.ValidationErrors — parse errors + missing required + constraints
//
// Naming convention: strings.ToUpper(prefix + field_name). Nested structs
// expand to prefixed vars (APP_DB_HOST) or accept a JSON object
// (APP_DB='{"host":"..."}'); slices accept comma-separated values
// (APP_TAGS=a,b,c) or a JSON array. See [FromEnv]'s doc for the full
// coercion table.
//
// # Single variable — FromEnvVar
//
// [FromEnvVar][T] loads one typed value from a single environment variable:
//
//	port, err := config.FromEnvVar("APP_PORT", codex.Int().Refine(validate.RangeInt(1, 65535)))
//
// Returns the zero value of T when the variable is not set — this is not
// an error. Returns [EnvVarError] wrapping [codex.ValidationErrors] when
// coercion or a Refine constraint fails.
package config
