// Package main demonstrates two templ + go-codex patterns on a single server:
//
//  1. Chunked HTML streaming — GET /stream/dashboard
//     adapttempl.StreamingFormat renders the templ component directly to the
//     ResponseWriter without an intermediate bytes.Buffer, enabling true
//     chunked delivery of large HTML pages.
//
//  2. SSE with HTML fragments — GET /sse/notifications
//     rest.AddSSERoute uses adapttempl.Format as the event format.
//     Each event is a templ-rendered <li> HTML fragment pushed over the
//     text/event-stream wire format — compatible with HTMX sse-swap.
//     Events with invalid props are rejected by the codec before rendering.
//
// Components are implemented with templ.ComponentFunc — no code generation
// required to run this example.
//
// Run with: go run ./examples/adapters-streaming-sse-templ
package main

import (
	"bufio"
	"context"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
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
	"github.com/DaniDeer/go-codex/stats"
	"github.com/DaniDeer/go-codex/validate"
)

// ── Domain types ──────────────────────────────────────────────────────────────

// DashboardProps is the validated data for the full-page streaming response.
type DashboardProps struct {
	Title    string
	Subtitle string
	Section1 string
	Section2 string
	Section3 string
}

// NotifProps is the validated payload for each SSE notification event.
// Level must be one of "info", "warn", or "error" — enforced by codec Refine.
type NotifProps struct {
	ID      string
	Message string
	Level   string
}

// ── Codecs ────────────────────────────────────────────────────────────────────

var dashPropsCodec = codex.Struct[DashboardProps](
	codex.RequiredField("title", codex.String().Refine(validate.NonEmptyString).WithTitle("Title"), func(p DashboardProps) string { return p.Title }, func(p *DashboardProps, v string) { p.Title = v }),
	codex.OptionalField("subtitle", codex.String().WithTitle("Subtitle"), func(p DashboardProps) string { return p.Subtitle }, func(p *DashboardProps, v string) { p.Subtitle = v }),
	codex.RequiredField("section1", codex.String().Refine(validate.NonEmptyString).WithTitle("Section 1"), func(p DashboardProps) string { return p.Section1 }, func(p *DashboardProps, v string) { p.Section1 = v }),
	codex.RequiredField("section2", codex.String().Refine(validate.NonEmptyString).WithTitle("Section 2"), func(p DashboardProps) string { return p.Section2 }, func(p *DashboardProps, v string) { p.Section2 = v }),
	codex.RequiredField("section3", codex.String().Refine(validate.NonEmptyString).WithTitle("Section 3"), func(p DashboardProps) string { return p.Section3 }, func(p *DashboardProps, v string) { p.Section3 = v }),
)

// validLevel enforces notification severity values.
var validLevel = codex.Constraint[string]{
	Name: "validLevel",
	Check: func(s string) bool {
		return s == "info" || s == "warn" || s == "error"
	},
	Message: func(s string) string {
		return fmt.Sprintf("level must be info, warn, or error; got %q", s)
	},
}

var notifCodec = codex.Struct[NotifProps](
	codex.RequiredField("id", codex.String().Refine(validate.NonEmptyString).WithTitle("ID"), func(p NotifProps) string { return p.ID }, func(p *NotifProps, v string) { p.ID = v }),
	codex.RequiredField("message", codex.String().Refine(validate.NonEmptyString).WithTitle("Message"), func(p NotifProps) string { return p.Message }, func(p *NotifProps, v string) { p.Message = v }),
	codex.RequiredField("level", codex.String().Refine(validLevel).WithTitle("Level"), func(p NotifProps) string { return p.Level }, func(p *NotifProps, v string) { p.Level = v }),
)

// ── templ components ──────────────────────────────────────────────────────────

// dashboardPage renders a full HTML dashboard page.
// Used with adapttempl.StreamingFormat — writes directly to ResponseWriter.
func dashboardPage(p DashboardProps) atempl.Component {
	return atempl.ComponentFunc(func(_ context.Context, w io.Writer) error {
		_, err := fmt.Fprintf(w,
			`<!DOCTYPE html>`+
				`<html><head><title>%s</title></head>`+
				`<body>`+
				`<header><h1>%s</h1><p>%s</p></header>`+
				`<main>`+
				`<section><h2>Metrics</h2><p>%s</p></section>`+
				`<section><h2>Alerts</h2><p>%s</p></section>`+
				`<section><h2>Activity</h2><p>%s</p></section>`+
				`</main>`+
				`</body></html>`,
			html.EscapeString(p.Title),
			html.EscapeString(p.Title),
			html.EscapeString(p.Subtitle),
			html.EscapeString(p.Section1),
			html.EscapeString(p.Section2),
			html.EscapeString(p.Section3),
		)
		return err
	})
}

// notifFragment renders one notification as an HTML <li> fragment.
// Used with adapttempl.Format as SSE event format — each SSE data line
// contains the rendered HTML, ready for HTMX sse-swap.
func notifFragment(p NotifProps) atempl.Component {
	return atempl.ComponentFunc(func(_ context.Context, w io.Writer) error {
		_, err := fmt.Fprintf(w,
			`<li class="notif notif-%s"><strong>[%s]</strong> %s</li>`,
			html.EscapeString(p.Level),
			html.EscapeString(p.ID),
			html.EscapeString(p.Message),
		)
		return err
	})
}

// ── Observer ──────────────────────────────────────────────────────────────────

type statsObserver struct {
	mu             sync.Mutex
	requests       int
	byStatus       map[int]int
	valErrorsByLoc map[string]int
}

func (o *statsObserver) RecordRequest(_ string, _ string, statusCode int, _ time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.requests++
	if o.byStatus == nil {
		o.byStatus = make(map[int]int)
	}
	o.byStatus[statusCode]++
}

func (o *statsObserver) RecordValidationError(location, constraintName, field string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.valErrorsByLoc == nil {
		o.valErrorsByLoc = make(map[string]int)
	}
	o.valErrorsByLoc[location]++
}

func (o *statsObserver) RecordSubscribe(_ string, _ bool, _ time.Duration) {}
func (o *statsObserver) RecordPublish(_ string, _ bool, _ time.Duration)   {}

func (o *statsObserver) print() {
	o.mu.Lock()
	defer o.mu.Unlock()
	fmt.Printf("  total requests : %d\n", o.requests)
	for code, n := range o.byStatus {
		fmt.Printf("  HTTP %-3d        : %d\n", code, n)
	}
	for loc, n := range o.valErrorsByLoc {
		fmt.Printf("  val errs (%s): %d\n", loc, n)
	}
}

// ── Main ──────────────────────────────────────────────────────────────────────

// mustServe exits the program if Register/RegisterHandle/Serve/ServeSSE
// returns an error — e.g. a malformed middleware Fn shape, caught eagerly
// at wiring time.
func mustServe(err error, what string) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s failed: %v\n", what, err)
		os.Exit(1)
	}
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	slog.SetDefault(logger)

	metrics := &statsObserver{}
	obs := stats.NewFanout(metrics, stats.NewLoggingObserver(logger.With("component", "sse-templ")))
	b := rest.NewBuilder(rest.Info{Title: "Streaming + SSE + templ Demo", Version: "1.0.0"})
	obsFn := nethttp.Observability(obs)
	opts := nethttp.Options{}

	// ── Route 1: Chunked HTML streaming ──────────────────────────────────────
	//
	// adapttempl.StreamingFormat builds a format.Format[DashboardProps] backed
	// by format.NewStreamed. The adapter detects IsStreamable() == true and
	// calls MarshalTo(props, w) — writing directly to the ResponseWriter
	// without buffering the entire page in memory first.
	//
	// The same route also serves JSON via content negotiation:
	//   Accept: text/html        → streams the templ component (chunked)
	//   Accept: application/json → returns JSON-encoded DashboardProps
	//
	// rest.Formats declares the negotiable response formats inline — the
	// RouteOpt equivalent of the old post-registration
	// RouteHandle.WithFormats call.

	dashRoute := rest.NewRoute[struct{}, DashboardProps]("GET", "/stream/dashboard",
		codex.Empty, dashPropsCodec,
		rest.RouteMeta{OperationID: "streamDashboard"},
		rest.Formats(
			adapttempl.StreamingFormat(dashPropsCodec, dashboardPage), // chunked HTML
			format.JSON(dashPropsCodec),                               // JSON fallback
		),
	).WithHandler(func(_ context.Context, _ struct{}) (DashboardProps, error) {
		return DashboardProps{
			Title:    "Operations Dashboard",
			Subtitle: "Real-time system overview",
			Section1: "CPU 42% | Memory 61% | Disk 78%",
			Section2: "2 active alerts — see notification feed",
			Section3: "Last deployment: 2024-06-01 14:32 UTC",
		}, nil
	}).HandleMW(nil, obsFn).WithOptions(opts)
	mustServe(dashRoute.Register(b), "register /stream/dashboard")

	// ── Route 2: SSE with HTML fragment events ────────────────────────────────
	//
	// rest.NewSSERoute declares an SSE endpoint. Formats for SSE routes are
	// still attached post-registration via SSERouteHandle.WithFormats —
	// there is no pre-Register RouteOpt for SSE event formats — so we use
	// RegisterHandle here to obtain the handle. Using adapttempl.Format
	// makes each event's data field a rendered HTML <li> fragment instead
	// of JSON — the HTMX html-over-the-wire SSE pattern.
	//
	// The codec validates every NotifProps before rendering: events with an
	// invalid Level or empty Message are rejected by send() and never written
	// to the stream. The observer records each validation error.

	notifRoute := rest.NewSSERoute[struct{}, NotifProps]("/sse/notifications",
		codex.Empty, notifCodec,
		rest.RouteMeta{OperationID: "sseNotifications"},
	).WithHandler(func(ctx context.Context, _ struct{}, send func(NotifProps) error) error {
		events := []NotifProps{
			{ID: "n1", Message: "Deployment succeeded", Level: "info"},
			{ID: "n2", Message: "Disk usage above 75%", Level: "warn"},
			{ID: "n3", Message: "", Level: "critical"}, // invalid: empty message + unknown level
			{ID: "n4", Message: "Database connection restored", Level: "info"},
			{ID: "n5", Message: "CPU spike detected", Level: "error"},
		}
		for _, e := range events {
			if err := send(e); err != nil {
				fmt.Printf("  [sse] event rejected: id=%s err=%v\n", e.ID, err)
			}
		}
		return nil
	}).HandleMW(nil, obsFn).WithOptions(opts)

	notifHandle, err := notifRoute.RegisterHandle(b)
	if err != nil {
		fmt.Fprintln(os.Stderr, "register SSE /sse/notifications failed:", err)
		os.Exit(1)
	}
	notifHandle.WithFormats(
		adapttempl.Format(notifCodec, notifFragment), // events as HTML fragments
	)

	// ── Shared mux ───────────────────────────────────────────────────────────

	mux := http.NewServeMux()
	mustServe(nethttp.Serve(mux, b), "Serve")
	mustServe(nethttp.ServeSSE(mux, b), "ServeSSE")

	srv := httptest.NewServer(mux)
	defer srv.Close()

	// ── Demo 1: Chunked streaming — Accept: text/html ─────────────────────────
	fmt.Println("=== GET /stream/dashboard (Accept: text/html — chunked streaming) ===")
	resp1 := mustGet(srv.URL+"/stream/dashboard", "text/html")
	body1, _ := io.ReadAll(resp1.Body)
	_ = resp1.Body.Close()
	preview := strings.TrimSpace(string(body1))
	if len(preview) > 80 {
		preview = preview[:80] + "..."
	}
	fmt.Printf("  Status       : %d\n", resp1.StatusCode)
	fmt.Printf("  Content-Type : %s\n", resp1.Header.Get("Content-Type"))
	fmt.Printf("  Body (trim)  : %s\n\n", preview)

	// ── Demo 2: Same route, JSON via content negotiation ─────────────────────
	fmt.Println("=== GET /stream/dashboard (Accept: application/json) ===")
	resp2 := mustGet(srv.URL+"/stream/dashboard", "application/json")
	body2, _ := io.ReadAll(resp2.Body)
	_ = resp2.Body.Close()
	fmt.Printf("  Status       : %d\n", resp2.StatusCode)
	fmt.Printf("  Content-Type : %s\n", resp2.Header.Get("Content-Type"))
	fmt.Printf("  Body         : %s\n\n", strings.TrimSpace(string(body2)))

	// ── Demo 3: Streaming with invalid props → 500 ───────────────────────────
	//
	// The streaming adapter validates props before calling MarshalTo. When the
	// codec rejects the response the adapter returns 500 — the component never
	// receives or renders invalid data.

	invalidBuilder := rest.NewBuilder(rest.Info{Title: "Streaming + SSE + templ Demo (invalid)", Version: "1.0.0"})
	invalidDashRoute := dashRoute.WithHandler(func(_ context.Context, _ struct{}) (DashboardProps, error) {
		return DashboardProps{
			Title:    "", // fails NonEmptyString
			Section1: "ok",
			Section2: "ok",
			Section3: "ok",
		}, nil
	})
	mustServe(invalidDashRoute.Register(invalidBuilder), "register invalid /stream/dashboard")

	invalidMux := http.NewServeMux()
	mustServe(nethttp.Serve(invalidMux, invalidBuilder), "Serve(invalid)")
	invalidSrv := httptest.NewServer(invalidMux)
	defer invalidSrv.Close()

	fmt.Println("=== GET /stream/dashboard with invalid props (title empty) ===")
	resp3 := mustGet(invalidSrv.URL+"/stream/dashboard", "text/html")
	body3, _ := io.ReadAll(resp3.Body)
	_ = resp3.Body.Close()
	fmt.Printf("  Status       : %d (props rejected — component never rendered)\n\n", resp3.StatusCode)
	_ = body3

	// ── Demo 4: SSE notifications — HTML fragment events ─────────────────────
	//
	// Each valid event produces a "data: <li ...>" line. The invalid event
	// (n3: empty message, unknown level "critical") is rejected before the
	// notifFragment component is ever called. The observer counts the error.

	fmt.Println("=== GET /sse/notifications (SSE — events as HTML fragments) ===")
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
		srv.URL+"/sse/notifications", nil)
	sseResp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, "SSE request error:", err)
		os.Exit(1)
	}
	fmt.Printf("  Content-Type : %s\n", sseResp.Header.Get("Content-Type"))
	fmt.Println("  Events received:")
	scanner := bufio.NewScanner(sseResp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			fmt.Printf("    %s\n", line)
		}
	}
	_ = sseResp.Body.Close()
	fmt.Println()

	// ── Observer summary ──────────────────────────────────────────────────────
	fmt.Println("=== Observer summary ===")
	metrics.print()
}

func mustGet(url, accept string) *http.Response {
	req, _ := http.NewRequest(http.MethodGet, url, nil) //nolint:noctx
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := http.DefaultClient.Do(req) //nolint:noctx
	if err != nil {
		fmt.Fprintln(os.Stderr, "request error:", err)
		os.Exit(1)
	}
	return resp
}
