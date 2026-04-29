// Package formats demonstrates the builtin format constraints in validate/.
//
// Each constraint validates a common string format (email, UUID, URL, etc.)
// and also annotates the codec's schema so the format appears in OpenAPI output
// automatically — no extra work needed.
//
// Run with: go run ./examples/formats
package main

import (
	"fmt"
	"os"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/render/openapi"
	"github.com/DaniDeer/go-codex/schema"
	"github.com/DaniDeer/go-codex/validate"
)

// Contact uses every builtin format constraint.
type Contact struct {
	ID        string // UUID
	Email     string
	Website   string // URL
	IP        string // IPv4
	BirthDate string // Date (YYYY-MM-DD)
	CreatedAt string // DateTime (RFC 3339)
	Handle    string // Slug
}

var ContactCodec = codex.Struct[Contact](
	codex.Field[Contact, string]{
		Name:     "id",
		Codec:    codex.String().Refine(validate.UUID).WithTitle("ID").WithDescription("Unique contact identifier (UUID v4)."),
		Get:      func(c Contact) string { return c.ID },
		Set:      func(c *Contact, v string) { c.ID = v },
		Required: true,
	},
	codex.Field[Contact, string]{
		Name:     "email",
		Codec:    codex.String().Refine(validate.Email).WithTitle("Email").WithDescription("Primary contact email address."),
		Get:      func(c Contact) string { return c.Email },
		Set:      func(c *Contact, v string) { c.Email = v },
		Required: true,
	},
	codex.Field[Contact, string]{
		Name:     "website",
		Codec:    codex.String().Refine(validate.URL).WithTitle("Website").WithDescription("Contact's personal or company website (http/https)."),
		Get:      func(c Contact) string { return c.Website },
		Set:      func(c *Contact, v string) { c.Website = v },
		Required: false,
	},
	codex.Field[Contact, string]{
		Name:     "ip",
		Codec:    codex.String().Refine(validate.IPv4).WithTitle("IP Address").WithDescription("IPv4 address of last login."),
		Get:      func(c Contact) string { return c.IP },
		Set:      func(c *Contact, v string) { c.IP = v },
		Required: false,
	},
	codex.Field[Contact, string]{
		Name:     "birthDate",
		Codec:    codex.String().Refine(validate.Date).WithTitle("Birth Date").WithDescription("Date of birth in YYYY-MM-DD format."),
		Get:      func(c Contact) string { return c.BirthDate },
		Set:      func(c *Contact, v string) { c.BirthDate = v },
		Required: false,
	},
	codex.Field[Contact, string]{
		Name:     "createdAt",
		Codec:    codex.String().Refine(validate.DateTime).WithTitle("Created At").WithDescription("Account creation timestamp (RFC 3339)."),
		Get:      func(c Contact) string { return c.CreatedAt },
		Set:      func(c *Contact, v string) { c.CreatedAt = v },
		Required: true,
	},
	codex.Field[Contact, string]{
		Name:     "handle",
		Codec:    codex.String().Refine(validate.Slug).WithTitle("Handle").WithDescription("URL-friendly handle (lowercase, hyphens allowed)."),
		Get:      func(c Contact) string { return c.Handle },
		Set:      func(c *Contact, v string) { c.Handle = v },
		Required: true,
	},
)

func main() {
	// ── 1. Valid decode ───────────────────────────────────────────────────────
	fmt.Println("=== Valid input ===")

	valid := map[string]any{
		"id":        "550e8400-e29b-41d4-a716-446655440000",
		"email":     "alice@example.com",
		"website":   "https://alice.dev",
		"ip":        "192.168.0.1",
		"birthDate": "1990-06-15",
		"createdAt": "2024-01-15T10:30:00Z",
		"handle":    "alice-dev",
	}

	contact, err := ContactCodec.Decode(valid)
	if err != nil {
		fmt.Fprintln(os.Stderr, "unexpected error:", err)
		os.Exit(1)
	}
	fmt.Printf("decoded: %+v\n", contact)

	// ── 2. Invalid inputs — one bad field at a time ───────────────────────────
	fmt.Println("\n=== Invalid inputs ===")

	badCases := []struct {
		field string
		value any
	}{
		{"id", "not-a-uuid"},
		{"email", "missing-at-sign"},
		{"website", "ftp://not-http.com"},
		{"ip", "999.0.0.1"},
		{"birthDate", "15/06/1990"},
		{"createdAt", "2024-01-15 10:30:00"},
		{"handle", "Has_Uppercase"},
	}

	for _, bc := range badCases {
		input := copyMap(valid)
		input[bc.field] = bc.value
		_, err := ContactCodec.Decode(input)
		fmt.Printf("  %-12s → %v\n", bc.field+":", err)
	}

	// ── 3. OpenAPI schema — formats reflected automatically ───────────────────
	fmt.Println("\n=== OpenAPI components/schemas (YAML) ===")
	yamlBytes, err := openapi.MarshalYAML(map[string]schema.Schema{
		"Contact": ContactCodec.Schema,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "render error:", err)
		os.Exit(1)
	}
	fmt.Print(string(yamlBytes))
}

func copyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
