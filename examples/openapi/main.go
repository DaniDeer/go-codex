// Package openapi demonstrates generating an OpenAPI components/schemas section
// from Codec definitions using the render/openapi package.
//
// Run with: go run ./examples/openapi
package main

import (
	"fmt"
	"os"
	"regexp"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/render/openapi"
	"github.com/DaniDeer/go-codex/schema"
	"github.com/DaniDeer/go-codex/validate"
)

var emailPattern = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

// User is a domain type whose codec is the single source of truth for
// encoding, decoding, validation, and schema documentation.
type User struct {
	Name  string
	Email string
	Age   int
	Role  string
}

var UserCodec = codex.Struct[User](
	codex.Field[User, string]{
		Name: "name",
		Codec: codex.String().
			Refine(validate.NonEmptyString).
			Refine(validate.MaxLen(100)).
			WithTitle("Full Name").
			WithDescription("The user's full display name."),
		Get:      func(u User) string { return u.Name },
		Set:      func(u *User, v string) { u.Name = v },
		Required: true,
	},
	codex.Field[User, string]{
		Name: "email",
		Codec: codex.String().
			Refine(validate.Pattern(emailPattern)).
			WithTitle("Email Address").
			WithDescription("Contact email. Must be a valid RFC 5321 address."),
		Get:      func(u User) string { return u.Email },
		Set:      func(u *User, v string) { u.Email = v },
		Required: true,
	},
	codex.Field[User, int]{
		Name: "age",
		Codec: codex.Int().
			Refine(validate.RangeInt(0, 150)).
			WithTitle("Age").
			WithDescription("Age in years. Must be between 0 and 150."),
		Get:      func(u User) int { return u.Age },
		Set:      func(u *User, v int) { u.Age = v },
		Required: true,
	},
	codex.Field[User, string]{
		Name: "role",
		Codec: codex.String().
			Refine(validate.OneOf("admin", "editor", "viewer")).
			WithTitle("Role").
			WithDescription("Access role assigned to the user."),
		Get:      func(u User) string { return u.Role },
		Set:      func(u *User, v string) { u.Role = v },
		Required: true,
	},
)

func main() {
	schemas := map[string]schema.Schema{
		"User": UserCodec.Schema,
	}

	// Render as OpenAPI components/schemas YAML.
	yamlBytes, err := openapi.MarshalYAML(schemas)
	if err != nil {
		fmt.Fprintf(os.Stderr, "render error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("# OpenAPI components/schemas (YAML)")
	fmt.Println("# ---- paste under components: schemas: in your openapi.yaml ----")
	fmt.Println()
	fmt.Print(string(yamlBytes))

	// Verify: the same codec still decodes and validates correctly.
	_, err = UserCodec.Decode(map[string]any{
		"name":  "Alice",
		"email": "alice@example.com",
		"age":   30,
		"role":  "admin",
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "decode error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("# Codec decode/validation: OK (same Codec[T], no duplication)")
}
