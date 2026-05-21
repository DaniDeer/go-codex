// Package decode-errors demonstrates multi-field validation errors in go-codex.
//
// Before: struct Decode stopped at the first failing field — callers got one
// error and had to re-submit to discover the next one.
//
// Now: all fields are validated in a single pass. Every failing field is
// collected into a codex.ValidationErrors slice, so callers see the complete
// picture immediately.
//
// Structured error types allow callers to inspect each failure precisely:
//   - codex.ValidationErrors — slice of per-field errors
//   - codex.ValidationError  — field name + underlying error
//   - codex.ConstraintError  — constraint name + human-readable message
//
// All types implement slog.LogValuer for structured logging.
// For a comprehensive tour of all error types, see examples/error-types.
//
// Run with: go run ./examples/decode-errors
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/validate"
)

type CreateUserRequest struct {
	Name  string
	Email string
	Age   int
}

var createUserCodec = codex.Struct[CreateUserRequest](
	codex.RequiredField[CreateUserRequest, string]("name",
		codex.String().Refine(validate.NonEmptyString).Refine(validate.MaxLen(100)),
		func(r CreateUserRequest) string { return r.Name },
		func(r *CreateUserRequest, v string) { r.Name = v },
	),
	codex.RequiredField[CreateUserRequest, string]("email",
		codex.String().Refine(validate.Email),
		func(r CreateUserRequest) string { return r.Email },
		func(r *CreateUserRequest, v string) { r.Email = v },
	),
	codex.RequiredField[CreateUserRequest, int]("age",
		codex.Int().Refine(validate.PositiveInt),
		func(r CreateUserRequest) int { return r.Age },
		func(r *CreateUserRequest, v int) { r.Age = v },
	),
)

func main() {
	jsonFmt := format.JSON(createUserCodec)

	// ── Section 1: all errors collected in one decode pass ───────────────────
	//
	// All three fields violate their constraints. Previously only the first
	// failure was returned. Now all three are collected before returning.

	fmt.Println("=== 1. All field errors collected ===")
	fmt.Println()

	bad := []byte(`{"name":"","email":"not-an-email","age":-5}`)
	_, err := jsonFmt.Unmarshal(bad)
	if err != nil {
		fmt.Println("decode error:")
		fmt.Println(" ", err)
	}
	fmt.Println()

	// ── Section 2: structured access via errors.As ───────────────────────────
	//
	// Use errors.As to extract ValidationErrors, then walk the chain:
	//   ValidationErrors → ValidationError → ConstraintError
	// Each level adds more structured context: field name, then constraint
	// name and message as separate typed fields.

	fmt.Println("=== 2. Structured access via errors.As ===")
	fmt.Println()

	var ve codex.ValidationErrors
	if errors.As(err, &ve) {
		fmt.Printf("  %d field(s) failed:\n", len(ve))
		for _, fieldErr := range ve {
			var ce codex.ConstraintError
			if errors.As(fieldErr.Err, &ce) {
				fmt.Printf("    field=%q  constraint=%q  message=%q\n",
					fieldErr.Field, ce.Name, ce.Message)
			} else {
				fmt.Printf("    field=%q  err=%v\n", fieldErr.Field, fieldErr.Err)
			}
		}
	}
	fmt.Println()

	// ── Section 3: practical — structured 400 response body ─────────────────
	//
	// In an HTTP handler, map ValidationErrors → {"field": "message"} and
	// return it as a 400 JSON body so clients know exactly what to fix.

	fmt.Println("=== 3. Structured HTTP 400 response body ===")
	fmt.Println()

	fieldErrors := make(map[string]string, len(ve))
	for _, e := range ve {
		fieldErrors[e.Field] = e.Err.Error()
	}
	body, _ := json.MarshalIndent(map[string]any{
		"error":  "validation failed",
		"fields": fieldErrors,
	}, "", "  ")
	fmt.Println(string(body))
	fmt.Println()

	// ── Section 4: slog structured logging ───────────────────────────────────
	//
	// ValidationErrors, ValidationError, and ConstraintError all implement
	// slog.LogValuer. Pass them directly as slog attributes — fields and
	// constraint names are emitted as structured key-value pairs, not flat strings.

	fmt.Println("=== 4. slog structured logging ===")
	fmt.Println()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	if errors.As(err, &ve) {
		logger.Error("request validation failed",
			slog.Any("validation_errors", ve),
		)

		// Log individual field errors with constraint detail.
		for _, fieldErr := range ve {
			var ce codex.ConstraintError
			if errors.As(fieldErr.Err, &ce) {
				logger.Warn("field constraint failed",
					slog.Any("field", fieldErr),
					slog.Any("constraint", ce),
				)
			}
		}
	}
	fmt.Println()

	// ── Section 5: valid input ───────────────────────────────────────────────

	fmt.Println("=== 5. Valid input ===")
	fmt.Println()

	good := []byte(`{"name":"Alice","email":"alice@example.com","age":30}`)
	req, err := jsonFmt.Unmarshal(good)
	if err != nil {
		fmt.Println("unexpected error:", err)
		return
	}
	fmt.Printf("  decoded: %+v\n", req)
}
