// Package main demonstrates where go-codex shines in a comment moderation
// use case: a single codec definition simultaneously escapes HTML, enforces
// length limits, and documents the schema — all derived from one value.
//
// The key insight:
//
//	SafeTextCodec                            // transform: html.EscapeString on decode
//	  .Refine(validate.NonEmptyString)       // validate: reject empty
//	  .Refine(validate.MaxLen(500))          // validate: reject too-long
//	  .WithTitle("Body")                     // document: field name
//	  .WithDescription("...")               // document: human description
//
// One codec = escaping + validation + OpenAPI schema. No separate validation
// function, no separate schema definition, no risk of them drifting apart.
//
// Run with: go run ./examples/html-sanitize
package main

import (
	"fmt"
	"html"
	"os"
	"strings"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/render/openapi"
	"github.com/DaniDeer/go-codex/schema"
	"github.com/DaniDeer/go-codex/validate"
)

// SafeTextCodec transforms a string by escaping HTML special characters on
// decode. Every downstream constraint and the schema it produces are layered
// on top of this base — composing freely with Refine.
var SafeTextCodec = codex.MapCodecSafe(
	codex.String(),
	html.EscapeString,
	func(s string) (string, error) { return s, nil },
)

// CommentAuthorCodec is the single source of truth for a comment author field:
//   - html.EscapeString on decode (via SafeTextCodec)
//   - reject empty
//   - reject > 100 chars
//   - schema: type string, minLength 1, maxLength 100 — generated for free
var CommentAuthorCodec = SafeTextCodec.
	Refine(validate.NonEmptyString).
	Refine(validate.MaxLen(100)).
	WithTitle("Author").
	WithDescription("Display name of the comment author. HTML-escaped, 1–100 characters.")

// CommentBodyCodec is the single source of truth for a comment body field:
//   - html.EscapeString on decode
//   - reject empty
//   - reject > 500 chars
//   - schema: type string, minLength 1, maxLength 500 — generated for free
var CommentBodyCodec = SafeTextCodec.
	Refine(validate.NonEmptyString).
	Refine(validate.MaxLen(500)).
	WithTitle("Body").
	WithDescription("Content of the comment. HTML-escaped, 1–500 characters.")

// CommentResponse is the shape of a user-submitted comment from the API.
type CommentResponse struct {
	ID     string
	Author string
	Body   string
}

// CommentCodec decodes a comment payload. One Decode call handles type
// checking, HTML escaping, and all constraint validation for every field.
var CommentCodec = codex.Struct[CommentResponse](
	codex.Field[CommentResponse, string]{
		Name:     "id",
		Codec:    codex.String().Refine(validate.UUID).WithTitle("ID").WithDescription("Comment identifier (UUID v4)."),
		Get:      func(c CommentResponse) string { return c.ID },
		Set:      func(c *CommentResponse, v string) { c.ID = v },
		Required: true,
	},
	codex.Field[CommentResponse, string]{
		Name:     "author",
		Codec:    CommentAuthorCodec,
		Get:      func(c CommentResponse) string { return c.Author },
		Set:      func(c *CommentResponse, v string) { c.Author = v },
		Required: true,
	},
	codex.Field[CommentResponse, string]{
		Name:     "body",
		Codec:    CommentBodyCodec,
		Get:      func(c CommentResponse) string { return c.Body },
		Set:      func(c *CommentResponse, v string) { c.Body = v },
		Required: true,
	},
)

func main() {
	// ── 1. Valid input ────────────────────────────────────────────────────────
	fmt.Println("=== Valid input ===")

	valid := map[string]any{
		"id":     "550e8400-e29b-41d4-a716-446655440000",
		"author": "Alice",
		"body":   "Great post, very helpful!",
	}

	comment, err := CommentCodec.Decode(valid)
	if err != nil {
		fmt.Fprintln(os.Stderr, "unexpected error:", err)
		os.Exit(1)
	}
	fmt.Printf("author: %q\n", comment.Author)
	fmt.Printf("body:   %q\n", comment.Body)

	// ── 2. What the composed codec rejects — one Decode, all checks ───────────
	//
	// A single CommentCodec.Decode call enforces:
	//   • type check (field must be a string)
	//   • HTML escaping (not a rejection — a transform; see section 3)
	//   • non-empty
	//   • max length
	//
	// Compare this to maintaining a separate validation function AND a separate
	// schema definition AND an escaping step — three things that can drift apart.
	fmt.Println("\n=== What the codec rejects (one Decode, all checks) ===")

	withPatch := func(patch map[string]any) map[string]any {
		m := make(map[string]any, len(valid))
		for k, v := range valid {
			m[k] = v
		}
		for k, v := range patch {
			m[k] = v
		}
		return m
	}

	rejectCases := []struct {
		label string
		input map[string]any
	}{
		{"author empty", withPatch(map[string]any{"author": ""})},
		{"body empty", withPatch(map[string]any{"body": ""})},
		{"author too long (101 chars)", withPatch(map[string]any{"author": strings.Repeat("a", 101)})},
		{"body too long (501 chars)", withPatch(map[string]any{"body": strings.Repeat("x", 501)})},
		{"author wrong type (int)", withPatch(map[string]any{"author": 42})},
		{"body wrong type (bool)", withPatch(map[string]any{"body": true})},
		{"id not a UUID", withPatch(map[string]any{"id": "not-a-uuid"})},
	}

	for _, rc := range rejectCases {
		_, err := CommentCodec.Decode(rc.input)
		fmt.Printf("  %-30s → %v\n", rc.label+":", err)
	}

	// ── 3. What the codec transforms (escaping) — not rejected, neutralized ───
	//
	// XSS payloads are not rejected — they are escaped on decode so the stored
	// value is always safe, regardless of how many times it is re-rendered or
	// forwarded to other services.
	fmt.Println("\n=== What the codec transforms (XSS neutralized, not rejected) ===")

	xssCases := []struct {
		label string
		body  string
	}{
		{"script tag", `<script>alert("xss")</script>`},
		{"event handler", `<img src=x onerror="stealCookies()">`},
		{"CSS injection", `<style>body{visibility:hidden}</style>`},
		{"entity smuggling", `&lt;script&gt;alert("pre-encoded")&lt;/script&gt;`},
	}

	for _, xc := range xssCases {
		decoded, err := CommentCodec.Decode(withPatch(map[string]any{"body": xc.body}))
		if err != nil {
			fmt.Printf("  %-20s → rejected: %v\n", xc.label+":", err)
			continue
		}
		fmt.Printf("  %-20s → %q\n", xc.label+":", decoded.Body)
	}

	// ── 4. Schema — the codec documents itself ────────────────────────────────
	//
	// The constraints applied above (NonEmpty → minLength:1, MaxLen → maxLength)
	// flow into the schema automatically. There is no separate schema to write
	// or keep in sync.
	fmt.Println("\n=== Schema auto-generated from the codec ===")

	yamlBytes, err := openapi.MarshalYAML(map[string]schema.Schema{
		"Comment": CommentCodec.Schema,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "render error:", err)
		os.Exit(1)
	}
	fmt.Print(string(yamlBytes))
}
