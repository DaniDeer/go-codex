package format

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/schema"
)

// FromEnv loads T from environment variables using schema-driven type coercion.
//
// Naming convention: strings.ToUpper(prefix + field_name).
// Underscores in field names are preserved:
//
//	field "log_level"  + prefix "APP_" → "APP_LOG_LEVEL"
//	field "db"         + prefix "APP_" → recurse with prefix "APP_DB_"
//	nested field "host"                → "APP_DB_HOST"
//
// Supported types (determined from the codec's schema):
//
//	flat primitives — direct env var, string coerced by schema type
//	nested structs  — prefix expansion (APP_DB_HOST) OR JSON object (APP_DB='{"host":"..."}')
//	slices          — comma-separated (APP_TAGS=a,b,c) OR JSON array (APP_TAGS='["a","b","c"]')
//	StringMap       — JSON object only (APP_LABELS='{"k":"v"}')
//	Nullable[T]     — absent = nil; present = coerce as inner type
//
// JSON detection: when a field's env var is set and the value starts with '{' or '['
// matching the field's schema type, it is parsed as JSON. JSON takes precedence
// over prefix expansion and comma-split when both would apply (e.g. APP_DB='{...}'
// takes priority over APP_DB_HOST=...).
//
// Silently skipped: TaggedUnion, slices of objects.
//
// Errors are returned as [codex.ValidationErrors]. Parse errors (an env var is
// set but its value cannot be coerced to the field's type) are collected and
// returned before the codec's Decode runs. Missing required fields and
// constraint violations are reported by Decode in the same error shape.
func FromEnv[T any](c codex.Codec[T], prefix string) (T, error) {
	intermediate, parseErrs := buildEnvIntermediate(c.Schema, prefix)
	if len(parseErrs) > 0 {
		var zero T
		return zero, parseErrs
	}
	return c.Decode(intermediate)
}

// FromEnvVar loads a single typed value from one environment variable.
//
// The codec's schema determines the string coercion (integer, number, boolean,
// string). All Refine constraints run after coercion — the same rules apply as
// in any codec Decode call.
//
// Returns [EnvVarError] wrapping a [codex.ValidationErrors] when coercion or
// constraint validation fails. Returns the zero value of T when the variable is
// not set. Use [errors.As] to inspect the structured error:
//
//	port, err := format.FromEnvVar("APP_PORT", codex.Int().Refine(validate.RangeInt(1, 65535)))
//	if err != nil {
//	    var envErr format.EnvVarError
//	    if errors.As(err, &envErr) {
//	        slog.Warn("env var invalid", "key", envErr.Key, "cause", envErr.Err)
//	    }
//	}
func FromEnvVar[T any](key string, c codex.Codec[T]) (T, error) {
	var zero T
	raw, ok := os.LookupEnv(key)
	if !ok {
		return zero, nil
	}
	coerced, err := coercePrimitive(raw, c.Schema.Type)
	if err != nil {
		return zero, EnvVarError{Key: key, Err: fmt.Errorf("%w", codex.ValidationErrors{{
			Field: key,
			Err:   fmt.Errorf("%s", err.Error()),
		}})}
	}
	v, decErr := c.Decode(coerced)
	if decErr != nil {
		return zero, EnvVarError{Key: key, Err: decErr}
	}
	return v, nil
}

// EnvVarError is returned by [FromEnvVar] when coercion or codec validation
// fails for a single environment variable.
//
// Use [errors.As] to extract the key and structured cause:
//
//	var envErr format.EnvVarError
//	if errors.As(err, &envErr) {
//	    slog.Warn("env var invalid", "key", envErr.Key, "cause", envErr.Err)
//	    stats.ReportErrors(obs, "env", envErr.Err)
//	}
type EnvVarError struct {
	// Key is the environment variable name (e.g. "APP_PORT").
	Key string
	// Err is the underlying coercion or validation error.
	// Typically wraps [codex.ValidationErrors].
	Err error
}

func (e EnvVarError) Error() string {
	return fmt.Sprintf("env var %q: %s", e.Key, e.Err)
}

// Unwrap allows [errors.Is] and [errors.As] to traverse the underlying error.
func (e EnvVarError) Unwrap() error { return e.Err }

// buildEnvIntermediate walks the schema, reads matching env vars, coerces their
// string values to the expected types, and returns the intermediate map that
// codec.Decode expects, plus any parse errors encountered.
func buildEnvIntermediate(s schema.Schema, prefix string) (map[string]any, codex.ValidationErrors) {
	out := map[string]any{}
	var errs codex.ValidationErrors

	for _, prop := range s.Properties {
		key := envVarKey(prefix, prop.Name)

		switch {
		case isNestedStruct(prop.Schema):
			// JSON object wins over prefix expansion when APP_FIELD='{...}' is set.
			if raw, ok := os.LookupEnv(key); ok && strings.HasPrefix(raw, "{") {
				var parsed map[string]any
				if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
					errs = append(errs, codex.ValidationError{
						Field: prop.Name,
						Err:   fmt.Errorf("env %s: invalid JSON object: %w", key, err),
					})
				} else {
					out[prop.Name] = parsed
				}
				break
			}
			// Fall through to prefix expansion.
			nested, nestedErrs := buildEnvIntermediate(prop.Schema, key+"_")
			errs = append(errs, nestedErrs...)
			if len(nested) > 0 {
				out[prop.Name] = nested
			}

		case isStringMap(prop.Schema):
			// StringMap is only supported via JSON object: APP_LABELS='{"k":"v"}'.
			raw, ok := os.LookupEnv(key)
			if !ok || !strings.HasPrefix(raw, "{") {
				break
			}
			var parsed map[string]any
			if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
				errs = append(errs, codex.ValidationError{
					Field: prop.Name,
					Err:   fmt.Errorf("env %s: invalid JSON object: %w", key, err),
				})
			} else {
				out[prop.Name] = parsed
			}

		case prop.Schema.Type == "array" && prop.Schema.Items != nil:
			raw, ok := os.LookupEnv(key)
			if !ok {
				break
			}
			// JSON array wins over comma-split when the value starts with '['.
			if strings.HasPrefix(raw, "[") {
				var parsed []any
				if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
					errs = append(errs, codex.ValidationError{
						Field: prop.Name,
						Err:   fmt.Errorf("env %s: invalid JSON array: %w", key, err),
					})
				} else {
					out[prop.Name] = parsed
				}
				break
			}
			items, itemErrs := parseSliceEnv(raw, prop.Name, key, prop.Schema.Items.Type)
			errs = append(errs, itemErrs...)
			if len(itemErrs) == 0 {
				out[prop.Name] = items
			}

		default:
			raw, ok := os.LookupEnv(key)
			if !ok {
				break
			}
			val, err := coercePrimitive(raw, prop.Schema.Type)
			if err != nil {
				errs = append(errs, codex.ValidationError{
					Field: prop.Name,
					Err:   fmt.Errorf("env %s: %w", key, err),
				})
			} else {
				out[prop.Name] = val
			}
		}
	}

	return out, errs
}

// envVarKey computes the env var name for a field: strings.ToUpper(prefix + fieldName).
func envVarKey(prefix, fieldName string) string {
	return strings.ToUpper(prefix + fieldName)
}

// isNestedStruct reports whether s describes a regular struct (as opposed to a
// StringMap or TaggedUnion). Both StringMap and nested structs have Type="object",
// but StringMap sets AdditionalPropertiesSchema while structs set Properties.
func isNestedStruct(s schema.Schema) bool {
	return s.Type == "object" && s.AdditionalPropertiesSchema == nil && len(s.Properties) > 0
}

// isStringMap reports whether s describes a StringMap (map[string]V).
func isStringMap(s schema.Schema) bool {
	return s.Type == "object" && s.AdditionalPropertiesSchema != nil
}

// coercePrimitive converts a string env var value to the Go type expected by
// the codec's intermediate layer, guided by the schema type.
func coercePrimitive(raw, schemaType string) (any, error) {
	switch schemaType {
	case "integer":
		n, err := strconv.Atoi(raw)
		if err != nil {
			return nil, fmt.Errorf("expected integer, got %q", raw)
		}
		return n, nil
	case "number":
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return nil, fmt.Errorf("expected number, got %q", raw)
		}
		return f, nil
	case "boolean":
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, fmt.Errorf("expected boolean (true/false/1/0), got %q", raw)
		}
		return b, nil
	default:
		return raw, nil
	}
}

// parseSliceEnv splits a comma-separated env var value and coerces each element
// to the type expected by the slice codec's element schema.
func parseSliceEnv(raw, fieldName, key, elemType string) ([]any, codex.ValidationErrors) {
	parts := strings.Split(raw, ",")
	out := make([]any, 0, len(parts))
	var errs codex.ValidationErrors

	for i, p := range parts {
		p = strings.TrimSpace(p)
		val, err := coercePrimitive(p, elemType)
		if err != nil {
			errs = append(errs, codex.ValidationError{
				Field: fmt.Sprintf("%s[%d]", fieldName, i),
				Err:   fmt.Errorf("env %s: element %d: %w", key, i, err),
			})
		} else {
			out = append(out, val)
		}
	}

	return out, errs
}
