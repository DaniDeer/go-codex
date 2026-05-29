// Package stats-observer-http demonstrates how to wire a [stats.Observer] into the
// nethttp adapter to collect request metrics without importing any metrics
// library into go-codex itself.
//
// Two routes are registered:
//   - GET /search — query parameter validation (location="query")
//   - POST /items — request body validation (location="body")
//
// Five requests are made covering both locations:
//   - GET /search?q=cats            → 200, no errors
//   - GET /search?q=fish&page=abc   → 400, one query validation error
//   - POST /items (valid body)      → 201, no errors
//   - POST /items (invalid body)    → 400, two body validation errors (name + email)
//
// The CountingObserver buckets validation errors by location so the summary
// distinguishes body errors from query errors.
//
// A real Prometheus integration would look identical — replace CountingObserver
// with a struct holding *prometheus.CounterVec / *prometheus.HistogramVec and
// call the relevant Observe/Inc methods inside each RecordXxx method.
//
// Run with: go run ./examples/stats-observer-http
package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"

	nethttp "github.com/DaniDeer/go-codex/adapters/nethttp"
	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/validate"
)

// ── Domain model ─────────────────────────────────────────────────────────────

// search route (GET /search) — body-less; query params carry the input.

type searchReq struct{}

type searchResp struct {
	Query string `json:"query"`
	Page  int    `json:"page"`
}

var searchReqCodec = codex.Struct[searchReq]()

var searchRespCodec = codex.Struct[searchResp](
	codex.Field[searchResp, string]{
		Name:  "query",
		Codec: codex.String(),
		Get:   func(r searchResp) string { return r.Query },
		Set:   func(r *searchResp, v string) { r.Query = v },
	},
	codex.Field[searchResp, int]{
		Name:  "page",
		Codec: codex.Int(),
		Get:   func(r searchResp) int { return r.Page },
		Set:   func(r *searchResp, v int) { r.Page = v },
	},
)

// items route (POST /items) — validated request body.

type createItemReq struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

type createItemResp struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

var createItemReqCodec = codex.Struct[createItemReq](
	codex.Field[createItemReq, string]{
		Name:     "name",
		Codec:    codex.String().Refine(validate.NonEmptyString),
		Required: true,
		Get:      func(r createItemReq) string { return r.Name },
		Set:      func(r *createItemReq, v string) { r.Name = v },
	},
	codex.Field[createItemReq, string]{
		Name:     "email",
		Codec:    codex.String().Refine(validate.Email),
		Required: true,
		Get:      func(r createItemReq) string { return r.Email },
		Set:      func(r *createItemReq, v string) { r.Email = v },
	},
)

var createItemRespCodec = codex.Struct[createItemResp](
	codex.Field[createItemResp, int]{
		Name:  "id",
		Codec: codex.Int(),
		Get:   func(r createItemResp) int { return r.ID },
		Set:   func(r *createItemResp, v int) { r.ID = v },
	},
	codex.Field[createItemResp, string]{
		Name:  "name",
		Codec: codex.String(),
		Get:   func(r createItemResp) string { return r.Name },
		Set:   func(r *createItemResp, v string) { r.Name = v },
	},
	codex.Field[createItemResp, string]{
		Name:  "email",
		Codec: codex.String(),
		Get:   func(r createItemResp) string { return r.Email },
		Set:   func(r *createItemResp, v string) { r.Email = v },
	},
)

// ── Observer ─────────────────────────────────────────────────────────────────

// CountingObserver is an in-memory implementation of [stats.Observer].
// It buckets validation errors by location so body and query failures
// can be distinguished in the summary.
//
// A real Prometheus observer would have *prometheus.CounterVec and
// *prometheus.HistogramVec fields instead of plain counters.
type CountingObserver struct {
	mu             sync.Mutex
	total          int
	byStatus       map[int]int
	valErrorsByLoc map[string]int // keyed by location: "body", "query", "payload"
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
	fmt.Printf("  [observer] validation error — location=%q constraint=%q field=%q\n",
		location, constraintName, field)
}

func (o *CountingObserver) Print() {
	o.mu.Lock()
	defer o.mu.Unlock()
	fmt.Printf("  total requests : %d\n", o.total)
	for code, n := range o.byStatus {
		fmt.Printf("  HTTP %-3d        : %d\n", code, n)
	}
	for loc, n := range o.valErrorsByLoc {
		fmt.Printf("  val errs %-7s: %d\n", "("+loc+")", n)
	}
	if len(o.latencies) > 0 {
		var sum time.Duration
		for _, l := range o.latencies {
			sum += l
		}
		fmt.Printf("  avg latency    : %v\n", sum/time.Duration(len(o.latencies)))
	}
}

// ── Routes ────────────────────────────────────────────────────────────────────

func main() {
	b := rest.NewBuilder(rest.Info{Title: "Demo API", Version: "1.0.0"})

	// GET /search — query param validation
	pageCodec := codex.String().Refine(validate.NonNegativeIntString)
	searchRoute, err := rest.AddRoute[searchReq, searchResp](b, "GET", "/search",
		searchReqCodec, searchRespCodec,
		rest.RouteConfig{
			OperationID: "search",
			QueryParams: []rest.QueryParam{
				{Name: "q", Description: "search query"},
				{Name: "page", Codec: &pageCodec, Description: "page number (non-negative integer)"},
			},
		},
	)
	if err != nil {
		panic(err)
	}

	// POST /items — body validation
	itemsRoute, err := rest.AddRoute[createItemReq, createItemResp](b, "POST", "/items",
		createItemReqCodec, createItemRespCodec,
		rest.RouteConfig{
			OperationID: "createItem",
			Responses: []rest.ResponseMeta{
				{Status: "201", Description: "item created"},
			},
		},
	)
	if err != nil {
		panic(err)
	}

	obs := &CountingObserver{}
	opts := nethttp.Options{Observer: obs}

	mux := http.NewServeMux()

	nethttp.Register(mux, searchRoute, func(ctx context.Context, _ searchReq) (searchResp, error) {
		r, _ := nethttp.RequestFromContext(ctx)
		q := r.URL.Query().Get("q")
		var page int
		_, _ = fmt.Sscanf(r.URL.Query().Get("page"), "%d", &page)
		return searchResp{Query: q, Page: page}, nil
	}, opts)

	var nextID int
	nethttp.Register(mux, itemsRoute, func(_ context.Context, req createItemReq) (createItemResp, error) {
		nextID++
		return createItemResp{ID: nextID, Name: req.Name, Email: req.Email}, nil
	}, opts)

	srv := httptest.NewServer(mux)
	defer srv.Close()

	// ── Query param validation ────────────────────────────────────────────────

	// Request 1: valid — q=cats, page omitted
	fmt.Println("=== GET /search?q=cats ===")
	resp1, _ := http.Get(srv.URL + "/search?q=cats") //nolint:noctx
	fmt.Printf("  HTTP %d\n", resp1.StatusCode)
	_, _ = io.Copy(io.Discard, resp1.Body)
	_ = resp1.Body.Close()

	// Request 2: invalid page — triggers RecordValidationError(location="query", ...)
	fmt.Println("=== GET /search?q=fish&page=abc (invalid query param) ===")
	resp2, _ := http.Get(srv.URL + "/search?q=fish&page=abc") //nolint:noctx
	fmt.Printf("  HTTP %d\n", resp2.StatusCode)
	_, _ = io.Copy(io.Discard, resp2.Body)
	_ = resp2.Body.Close()

	// ── Body validation ───────────────────────────────────────────────────────

	// Request 3: valid body
	fmt.Println(`=== POST /items {"name":"Alice","email":"alice@example.com"} ===`)
	resp3, _ := http.Post( //nolint:noctx
		srv.URL+"/items",
		"application/json",
		bytes.NewBufferString(`{"name":"Alice","email":"alice@example.com"}`),
	)
	fmt.Printf("  HTTP %d\n", resp3.StatusCode)
	_, _ = io.Copy(io.Discard, resp3.Body)
	_ = resp3.Body.Close()

	// Request 4: invalid body — empty name + bad email.
	// Both fields fail their constraints → two RecordValidationError("body", ...) calls.
	fmt.Println(`=== POST /items {"name":"","email":"not-an-email"} (invalid body) ===`)
	resp4, _ := http.Post( //nolint:noctx
		srv.URL+"/items",
		"application/json",
		bytes.NewBufferString(`{"name":"","email":"not-an-email"}`),
	)
	fmt.Printf("  HTTP %d\n", resp4.StatusCode)
	_, _ = io.Copy(io.Discard, resp4.Body)
	_ = resp4.Body.Close()

	fmt.Println("\n=== Observer summary ===")
	obs.Print()
}
