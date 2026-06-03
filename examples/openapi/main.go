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
	Name   string
	Email  string
	Age    int
	Role   string
	Avatar []byte  // base64-encoded profile image (optional)
	Note   *string // optional free-text note
}

var UserCodec = codex.Struct[User](
	codex.RequiredField("name", codex.String().
		Refine(validate.NonEmptyString).
		Refine(validate.MaxLen(100)).
		WithTitle("Full Name").
		WithDescription("The user's full display name."), func(u User) string { return u.Name }, func(u *User, v string) { u.Name = v }),
	codex.RequiredField("email", codex.String().
		Refine(validate.Pattern(emailPattern)).
		WithTitle("Email Address").
		WithDescription("Contact email. Must be a valid RFC 5321 address."), func(u User) string { return u.Email }, func(u *User, v string) { u.Email = v }),
	codex.RequiredField("age", codex.Int().
		Refine(validate.RangeInt(0, 150)).
		WithTitle("Age").
		WithDescription("Age in years. Must be between 0 and 150."), func(u User) int { return u.Age }, func(u *User, v int) { u.Age = v }),
	codex.RequiredField("role", codex.String().
		Refine(validate.OneOf("admin", "editor", "viewer")).
		WithTitle("Role").
		WithDescription("Access role assigned to the user."), func(u User) string { return u.Role }, func(u *User, v string) { u.Role = v }),
	// Bytes: base64-encoded avatar image. Schema: {type:string, format:byte}.
	// MaxBytes limits the decoded payload to 64 KiB.
	codex.OptionalField("avatar", codex.Bytes().Refine(validate.MaxBytes(65536)).WithDescription("Profile image as base64-encoded bytes (max 64 KiB)."), func(u User) []byte { return u.Avatar }, func(u *User, v []byte) { u.Avatar = v }),
	// Nullable: note is absent when nil; present when non-nil.
	codex.OptionalField("note", codex.Nullable(codex.String()).WithDescription("Optional admin note about the user."), func(u User) *string { return u.Note }, func(u *User, v *string) { u.Note = v }),
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
		"name":   "Alice",
		"email":  "alice@example.com",
		"age":    30,
		"role":   "admin",
		"avatar": "aGVsbG8=", // base64("hello")
		"note":   nil,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "decode error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("# Codec decode/validation: OK (same Codec[T], no duplication)")
}
