// Package adapters-nethttp-client demonstrates the HTTP client-side adapter.
//
// go-codex routes are declarative values — the same [rest.Route] that drives
// the server (nethttp.Handler / nethttp.Register) can be used on the client
// (nethttp.Call) to make typed HTTP calls with full codec validation.
//
// # Two usage patterns
//
// ## 1. Shared contract (import pattern)
//
// Define the route, codecs, and types in a shared Go package (here: contract/).
// Both the server and the client import that package. The Go compiler enforces
// the contract: any field rename, type change, or constraint modification breaks
// compilation on both sides immediately — no stale YAML, no schema drift.
//
//	Server:
//	    handle, _ := contract.CreateUser.Register(builder)
//	    nethttp.Register(mux, handle, myHandler, nethttp.Options{})
//
//	Client:
//	    handle, _ := contract.CreateUser.Register(builder) // or:
//	    handle  := contract.CreateUser.ClientHandle()       // no builder needed
//	    user, err := nethttp.Call(ctx, http.DefaultClient, serverURL, handle, req, nil, nethttp.CallOptions{})
//
// ## 2. Client-only (ClientHandle)
//
// When the client has no server role — it only calls a remote API — no [rest.Builder]
// is needed. Call [rest.Route.ClientHandle] directly to get a handle with all codec
// helpers and parameter validation, without registering for spec generation.
//
//	handle := rest.NewRoute[MyReq, MyResp]("GET", "/external/{id}", reqCodec, respCodec,
//	    rest.PathParam{Name: "id"}.WithCodec(uuidCodec),
//	).ClientHandle()
//	resp, err := nethttp.Call(ctx, http.DefaultClient, "https://external.api", handle, req, vars, opts)
//
// Run with: go run ./examples/adapters-nethttp-client
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"time"

	nethttp "github.com/DaniDeer/go-codex/adapters/nethttp"
	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/examples/adapters-nethttp-client/contract"
	"github.com/DaniDeer/go-codex/validate"
)

// ── Observer ──────────────────────────────────────────────────────────────────

// CountingObserver is an in-memory implementation of [stats.Observer].
// It records call counts, HTTP status codes, validation error locations, and
// latencies. In production replace the counters with Prometheus / OpenTelemetry
// instruments — the interface is identical.
//
// The observer is for metrics collection only. Structured error logging is done
// separately in application code using [errors.As] after each [nethttp.Call].
type CountingObserver struct {
	mu             sync.Mutex
	total          int
	byStatus       map[int]int
	valErrorsByLoc map[string]int // keyed by location: "path", "query", "cookie", "header", "body"
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
	fmt.Printf("  total calls    : %d\n", o.total)
	for code, n := range o.byStatus {
		label := "ok"
		if code == 0 {
			label = "pre-flight abort"
		} else if code >= 400 {
			label = "error"
		}
		fmt.Printf("  HTTP %-3d %-16s: %d\n", code, "("+label+")", n)
	}
	for loc, n := range o.valErrorsByLoc {
		fmt.Printf("  val errs %-8s: %d\n", "("+loc+")", n)
	}
	if len(o.latencies) > 0 {
		var sum time.Duration
		for _, l := range o.latencies {
			sum += l
		}
		fmt.Printf("  avg latency    : %v\n", sum/time.Duration(len(o.latencies)))
	}
}

// --- in-memory user store for the demo server ---

type store struct {
	mu    sync.RWMutex
	users map[string]contract.User
	seq   int
}

func (s *store) create(req contract.CreateUserReq) contract.User {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	id := fmt.Sprintf("u%d", s.seq)
	u := contract.User{ID: id, Name: req.Name, Email: req.Email}
	s.users[id] = u
	return u
}

func (s *store) get(id string) (contract.User, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, ok := s.users[id]
	return u, ok
}

func main() {
	// logger is the structured logger for all HTTP client events.
	// In production attach attributes from the request context (trace IDs, tenant,
	// user) using logger.With(...). Here a simple transport label suffices.
	logger := slog.Default().With("transport", "http-client")

	// CountingObserver wired into every Call. Collects per-call metrics without
	// any metrics library dependency. In production swap in Prometheus / OTel.
	obs := &CountingObserver{}

	db := &store{users: make(map[string]contract.User)}

	// ── 1. Server: register routes from the shared contract ───────────────────
	fmt.Println("=== Server: registering routes from shared contract ===")

	b := rest.NewBuilder(rest.Info{Title: "User API", Version: "1.0.0"})

	createHandle, err := contract.CreateUser.Register(b)
	if err != nil {
		fmt.Fprintln(os.Stderr, "register createUser:", err)
		os.Exit(1)
	}
	getHandle, err := contract.GetUser.Register(b)
	if err != nil {
		fmt.Fprintln(os.Stderr, "register getUser:", err)
		os.Exit(1)
	}

	mux := http.NewServeMux()
	nethttp.Register(mux, createHandle, func(ctx context.Context, req contract.CreateUserReq) (contract.User, error) {
		u := db.create(req)
		w, _ := nethttp.ResponseHeadersFromContext(ctx)
		if w != nil {
			w.Set("Location", "/users/"+u.ID)
		}
		return u, nil
	}, nethttp.Options{})
	nethttp.Register(mux, getHandle, func(ctx context.Context, _ contract.GetUserReq) (contract.User, error) {
		r, _ := nethttp.RequestFromContext(ctx)
		id := r.PathValue("id")
		u, ok := db.get(id)
		if !ok {
			return contract.User{}, fmt.Errorf("user %q not found", id)
		}
		return u, nil
	}, nethttp.Options{})

	srv := httptest.NewServer(mux)
	defer srv.Close()
	fmt.Printf("server listening at %s\n\n", srv.URL)

	// ── 2. Client: shared-contract pattern with Observer ─────────────────────
	fmt.Println("=== Client: shared-contract pattern (same Route as server) ===")

	// The client reuses the same contract.CreateUser route.
	// Register gives a handle; ClientHandle() works if no spec is needed.
	clientCreate, err := contract.CreateUser.Register(
		rest.NewBuilder(rest.Info{Title: "User API", Version: "1.0.0"}),
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, "client register:", err)
		os.Exit(1)
	}

	// Observer tracks metrics for every call; logger handles structured error logging.
	alice, err := nethttp.Call(context.Background(), srv.Client(), srv.URL,
		clientCreate,
		contract.CreateUserReq{Name: "Alice", Email: "alice@example.com"},
		nil, nethttp.CallOptions{Observer: obs})
	if err != nil {
		fmt.Fprintln(os.Stderr, "create alice:", err)
		os.Exit(1)
	}
	fmt.Printf("created: %+v\n", alice)

	// Fetch via GET /users/{id} — path var validated by codec before sending.
	clientGet := contract.GetUser.ClientHandle() // no builder needed for GET
	fetched, err := nethttp.Call(context.Background(), srv.Client(), srv.URL,
		clientGet, contract.GetUserReq{},
		map[string]string{"id": alice.ID},
		nethttp.CallOptions{Observer: obs})
	if err != nil {
		fmt.Fprintln(os.Stderr, "get alice:", err)
		os.Exit(1)
	}
	fmt.Printf("fetched: %+v\n\n", fetched)

	// ── 3. Structured error logging with slog ─────────────────────────────────
	fmt.Println("=== Structured error logging with slog ===")

	// Trigger a 404 — UnexpectedStatusError carries method, path, status, body.
	_, err = nethttp.Call(context.Background(), srv.Client(), srv.URL,
		clientGet, contract.GetUserReq{},
		map[string]string{"id": "nonexistent"},
		nethttp.CallOptions{Observer: obs})
	if err != nil {
		var statusErr nethttp.UnexpectedStatusError
		if errors.As(err, &statusErr) {
			logger.Error("api call failed",
				"method", statusErr.Method,
				"path", statusErr.Path,
				"status", statusErr.StatusCode,
				"body", string(statusErr.Body),
			)
		}
	}

	// Trigger a pre-flight path param validation failure — rest.PathParamError.
	_, err = nethttp.Call(context.Background(), srv.Client(), srv.URL,
		clientGet, contract.GetUserReq{},
		map[string]string{"id": ""}, // fails NonEmptyString codec
		nethttp.CallOptions{Observer: obs})
	if err != nil {
		var pathErr rest.PathParamError
		if errors.As(err, &pathErr) {
			logger.Warn("invalid path variable (no request sent)",
				"param", pathErr.Name,
				"value", pathErr.Value,
				"cause", pathErr.Err,
			)
		}
	}
	fmt.Println()

	// ── 4. Client: client-only pattern (no server builder) ────────────────────
	fmt.Println("=== Client: client-only pattern (ClientHandle, no builder) ===")

	type SearchReq struct{}
	type SearchResp struct{ Count int }
	searchCodec := codex.Struct[SearchResp](
		codex.Field[SearchResp, int]{
			Name:  "count",
			Codec: codex.Int(),
			Get:   func(r SearchResp) int { return r.Count },
			Set:   func(r *SearchResp, v int) { r.Count = v },
		},
	)
	searchHandle := rest.NewRoute[SearchReq, SearchResp]("GET", "/search",
		codex.Struct[SearchReq](), searchCodec,
		rest.QueryParam{Name: "q", Required: true}.WithCodec(
			codex.String().Refine(validate.NonEmptyString),
		),
	).ClientHandle()

	searchSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"count":42}`)
	}))
	defer searchSrv.Close()

	result, err := nethttp.Call(context.Background(), searchSrv.Client(), searchSrv.URL,
		searchHandle, SearchReq{}, nil,
		nethttp.CallOptions{
			QueryParams: map[string]string{"q": "alice"},
			Observer:    obs,
		})
	if err != nil {
		fmt.Fprintln(os.Stderr, "search:", err)
		os.Exit(1)
	}
	fmt.Printf("search result: %+v\n\n", result)

	// Trigger a query param validation error — observer records it; logger logs it.
	_, err = nethttp.Call(context.Background(), searchSrv.Client(), searchSrv.URL,
		searchHandle, SearchReq{}, nil,
		nethttp.CallOptions{
			QueryParams: map[string]string{"q": ""}, // fails NonEmptyString
			Observer:    obs,
		})
	if err != nil {
		var qpErr rest.QueryParamError
		if errors.As(err, &qpErr) {
			logger.Warn("invalid query parameter (no request sent)",
				"param", qpErr.Name,
				"value", qpErr.Value,
				"cause", qpErr.Err,
			)
		}
	}

	// ── 5. Observer summary ────────────────────────────────────────────────────
	fmt.Println("=== Observer summary ===")
	obs.Print()
	fmt.Println()
	fmt.Println("done.")
}
