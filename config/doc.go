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
// codec's schema for schema-driven type coercion, and returns it wrapped in
// a freshly-constructed, already-Set [*codex.Immutable][T] — not a bare T:
//
//	appConfig, err := config.FromEnv(configCodec, "APP_")
//	// err is codex.ValidationErrors — parse errors + missing required + constraints
//	// ... elsewhere, for the rest of the process's lifetime:
//	cfg := appConfig.Get()
//
// This is deliberate, not incidental: config loading happens exactly ONCE,
// at startup, before any pipeline/transport/goroutine exists to race
// against it — precisely the shape [codex.Immutable][T] exists for. Wrapping
// the result means "config was loaded, validated, and is now frozen for the
// rest of the process" is a TYPE-LEVEL guarantee (a second, accidental
// [config.FromEnv] call targeting the SAME [*codex.Immutable][T] would need
// its own fresh instance — Immutable enforces "set exactly once" per
// instance), not just a comment asking callers to treat the returned value
// as read-only.
//
// Naming convention: strings.ToUpper(prefix + field_name). Nested structs
// expand to prefixed vars (APP_DB_HOST) or accept a JSON object
// (APP_DB='{"host":"..."}'); slices accept comma-separated values
// (APP_TAGS=a,b,c) or a JSON array. See [FromEnv]'s doc for the full
// coercion table.
//
// # Single variable — FromEnvVar
//
// [FromEnvVar][T] loads one typed value from a single environment variable,
// returning a plain T (NOT wrapped in [codex.Immutable][T]):
//
//	port, err := config.FromEnvVar("APP_PORT", codex.Int().Refine(validate.RangeInt(1, 65535)))
//
// Returns the zero value of T when the variable is not set — this is not
// an error. Returns [EnvVarError] wrapping [codex.ValidationErrors] when
// coercion or a Refine constraint fails.
//
// FromEnvVar deliberately stays plain-value, unlike [FromEnv]: it is
// typically used for a single, OPTIONAL OVERRIDE merged into other,
// already-mutable state (e.g. a log-level flag patched onto a config
// struct loaded from a file) rather than "the one true, process-wide
// config" — a use case [codex.Immutable][T]'s set-once contract does not
// fit well. Wrap the result yourself in a [codex.Immutable][T] (or
// [codex.Const][T]) if a specific single value genuinely needs that
// guarantee.
package config
