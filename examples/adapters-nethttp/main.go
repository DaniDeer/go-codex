// Package adapters-nethttp demonstrates the three-layer codec pipeline pattern
// where every boundary — HTTP request, database, HTTP response — is modelled
// as a codec contract.
//
// Three boundary codecs:
//
// Codec[CreateUserReq]  — HTTP request contract (what the client sends)
// Codec[UserRecord]     — database contract (the SQL model)
// Codec[User]           — HTTP response contract (what the client receives)
//
// Two pure domain functions connect them (Layer 2):
//
// buildUserRecord(CreateUserReq) UserRecord   — request → DB record
// buildUserResponse(UserRecord) User          — DB record → response
//
// The pipeline for POST /users:
//
// Codec[Req] ─ decode ─▶ CreateUserReq ─▶ buildUserRecord ─▶ UserRecord
//
//	↓ (store IO via Codec[UserRecord])
//
// Codec[Resp] ─ encode ─▶ User ◀─ buildUserResponse ◀─ UserRecord
//
// The infrastructure layer (Layer 3) owns all IO: HTTP adapter, database reads
// and writes. The store uses Codec[UserRecord] to encode/decode rows — the
// database schema is defined exactly once in the codec, just like the HTTP
// contracts.
//
// Pure domain functions have zero IO and can be unit-tested with plain Go
// structs and no setup. Swap the entire infrastructure layer (HTTP → gRPC →
// CLI) without touching the domain layer or the business logic.
//
// Routes:
//   - POST /users      — body validate, JSON + YAML request formats, response headers + cookies (codec-validated), content negotiation
//   - GET  /users/{id} — UUID path param, BuildPath type-safe URL construction
//   - GET  /users      — query params (page, search)
//   - GET  /profile    — request-side cookie + header validation
//
// A CountingObserver wired into every route collects per-request metrics and
// per-field validation errors without any metrics library dependency.
//
// Run with: go run ./examples/adapters-nethttp
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"time"

	nethttp "github.com/DaniDeer/go-codex/adapters/nethttp"
	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/validate"
)

// ── Observer ──────────────────────────────────────────────────────────────────

// CountingObserver is an in-memory implementation of [stats.Observer].
// It records request counts, HTTP status codes, validation error locations, and
// latencies. In production, replace the counters with Prometheus / OpenTelemetry
// instruments — the interface is identical.
type CountingObserver struct {
	mu             sync.Mutex
	total          int
	byStatus       map[int]int
	valErrorsByLoc map[string]int // keyed by location: "body", "query", "cookie", "header"
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

// ── Layer 1: Domain models and codec contracts ────────────────────────────────
//
// Each boundary is described by a codec. Shared field codec variables (e.g.
// emailFieldCodec) define a constraint once; it propagates to every struct
// codec that references it — HTTP request, database schema, HTTP response.

// nameFieldCodec is the domain contract for a user's display name.
var nameFieldCodec = codex.String().
	Refine(validate.NonEmptyString).
	WithDescription("Display name.")

// emailFieldCodec is the domain contract for an email address.
var emailFieldCodec = codex.String().
	Refine(validate.Email).
	WithDescription("Email address.")

// ── HTTP request boundary ─────────────────────────────────────────────────────

// CreateUserReq is the domain entity for a user creation request.
type CreateUserReq struct {
	Name  string
	Email string
}

// createUserReqCodec describes what the HTTP client sends and enforces the
// domain invariants on the incoming request.
var createUserReqCodec = codex.Struct[CreateUserReq](
	codex.RequiredField("name", nameFieldCodec,
		func(r CreateUserReq) string { return r.Name },
		func(r *CreateUserReq, v string) { r.Name = v },
	),
	codex.RequiredField("email", emailFieldCodec,
		func(r CreateUserReq) string { return r.Email },
		func(r *CreateUserReq, v string) { r.Email = v },
	),
)

// ── Database boundary (SQL model) ─────────────────────────────────────────────

// UserRecord is the domain entity mapped to a database row.
// Column names and types are defined here — once — in the codec.
type UserRecord struct {
	ID    string
	Name  string
	Email string
}

// userRecordCodec describes the SQL model. The store uses this codec to
// encode records for persistence and decode rows on retrieval — exactly
// the same mechanism as the HTTP boundaries.
var userRecordCodec = codex.Struct[UserRecord](
	codex.RequiredField("id",
		codex.String().Refine(validate.UUID).WithDescription("Primary key."),
		func(r UserRecord) string { return r.ID },
		func(r *UserRecord, v string) { r.ID = v },
	),
	codex.RequiredField("name", nameFieldCodec,
		func(r UserRecord) string { return r.Name },
		func(r *UserRecord, v string) { r.Name = v },
	),
	codex.RequiredField("email", emailFieldCodec,
		func(r UserRecord) string { return r.Email },
		func(r *UserRecord, v string) { r.Email = v },
	),
)

// ── HTTP response boundary ────────────────────────────────────────────────────

// User is the domain entity returned to the HTTP client.
type User struct {
	ID    string
	Name  string
	Email string
}

// userCodec describes what the HTTP client receives.
// Shared field codecs propagate the same constraints from the request and
// database contracts into the response schema — no duplication.
var userCodec = codex.Struct[User](
	codex.OptionalField("id",
		codex.String().Refine(validate.UUID).WithDescription("User UUID."),
		func(u User) string { return u.ID },
		func(u *User, v string) { u.ID = v },
	),
	codex.OptionalField("name", nameFieldCodec,
		func(u User) string { return u.Name },
		func(u *User, v string) { u.Name = v },
	),
	codex.OptionalField("email", emailFieldCodec,
		func(u User) string { return u.Email },
		func(u *User, v string) { u.Email = v },
	),
)

// PagedUsersResp is the response for a paginated user list.
type PagedUsersResp struct {
	Page   int
	Search string
	Users  []User
}

// pagedUsersRespCodec describes the paginated response.
var pagedUsersRespCodec = codex.Struct[PagedUsersResp](
	codex.RequiredField("page",
		codex.Int().WithDescription("Current page number."),
		func(r PagedUsersResp) int { return r.Page },
		func(r *PagedUsersResp, v int) { r.Page = v },
	),
	codex.OptionalField("search",
		codex.String().WithDescription("Active name filter."),
		func(r PagedUsersResp) string { return r.Search },
		func(r *PagedUsersResp, v string) { r.Search = v },
	),
)

// makeListUsersHandler handles GET /users with query params.
// The nethttp adapter validates ?page against the NonNegativeIntString codec
// before this handler is called — no manual parsing needed for bad input.
func makeListUsersHandler() func(context.Context, struct{}) (PagedUsersResp, error) {
	return func(ctx context.Context, _ struct{}) (PagedUsersResp, error) {
		r, _ := nethttp.RequestFromContext(ctx)
		q := r.URL.Query()
		page := 0
		if p := q.Get("page"); p != "" {
			_, _ = fmt.Sscanf(p, "%d", &page) // safe: already validated as non-negative int
		}
		search := q.Get("search")
		return PagedUsersResp{Page: page, Search: search, Users: nil}, nil
	}
}

// ── Layer 2: Business logic (pure domain functions) ───────────────────────────
//
// Pure domain functions transform between domain types. Zero IO — no database,
// no HTTP, no external services. They encode business rules as data
// transformations and can be unit-tested with plain Go structs.

// buildUserRecord creates a database record from a user creation request.
// This is where business rules live: ID assignment, default fields, etc.
func buildUserRecord(req CreateUserReq) UserRecord {
	return UserRecord{
		ID:    "f47ac10b-58cc-4372-a567-0e02b2c3d479",
		Name:  req.Name,
		Email: req.Email,
	}
}

// buildUserResponse projects a database record into the HTTP response entity.
func buildUserResponse(record UserRecord) User {
	return User(record)
}

// ── Layer 3: Infrastructure (HTTP + database + external services) ─────────────
//
// Infrastructure closures orchestrate all IO. The UserStore uses
// userRecordCodec to encode rows on save and decode rows on fetch — the
// database schema and the codec are the same definition.

// withDomainLogging is a decorator that wraps a handler function, logging
// success (Info) or failure (Error) after the handler returns. This pattern
// separates the logging concern from the handler body, keeping L2/L3 business
// logic clean while providing consistent observability.
func withDomainLogging[Req, Resp any](
	name string,
	handler func(context.Context, Req) (Resp, error),
	logger *slog.Logger,
	extractAttrs func(Req, Resp) []slog.Attr,
) func(context.Context, Req) (Resp, error) {
	return func(ctx context.Context, req Req) (Resp, error) {
		resp, err := handler(ctx, req)
		if err != nil {
			logger.ErrorContext(ctx, name+" failed", "error", err)
		} else {
			attrs := extractAttrs(req, resp)
			// Convert []slog.Attr to []any for InfoContext
			args := make([]any, 0, len(attrs)*2)
			for _, attr := range attrs {
				args = append(args, attr.Key, attr.Value.Any())
			}
			logger.InfoContext(ctx, name+" succeeded", args...)
		}
		return resp, err
	}
}

// UserStore is a mock database that operates via userRecordCodec.
// Replace with a real SQL driver; the codec encode/decode mechanism stays.
type UserStore struct {
	mu   sync.RWMutex
	rows map[string]map[string]any // simulates SQL table rows
}

func newUserStore() *UserStore {
	return &UserStore{rows: make(map[string]map[string]any)}
}

// Save encodes the UserRecord using userRecordCodec (analogous to SQL INSERT).
func (s *UserStore) Save(r UserRecord) error {
	encoded, err := userRecordCodec.Encode(r)
	if err != nil {
		return err
	}
	row, ok := encoded.(map[string]any)
	if !ok {
		return fmt.Errorf("unexpected encoded type %T", encoded)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows[r.ID] = row
	return nil
}

// Get decodes a UserRecord from a stored row using userRecordCodec (analogous to SQL SELECT + scan).
func (s *UserStore) Get(id string) (UserRecord, bool) {
	s.mu.RLock()
	row, ok := s.rows[id]
	s.mu.RUnlock()
	if !ok {
		return UserRecord{}, false
	}
	record, err := userRecordCodec.Decode(row)
	if err != nil {
		return UserRecord{}, false
	}
	return record, true
}

// makeCreateUserHandler orchestrates the create-user pipeline:
//
// decode (codec) → buildUserRecord (L2) → Save (store IO) → buildUserResponse (L2) → encode (codec)
func makeCreateUserHandler(store *UserStore) func(context.Context, CreateUserReq) (User, error) {
	return func(ctx context.Context, req CreateUserReq) (User, error) {
		record := buildUserRecord(req)             // L2: pure business rule
		if err := store.Save(record); err != nil { // L3: database IO
			return User{}, err
		}
		user := buildUserResponse(record) // L2: pure projection
		// Deposit the Location header — validated by the adapter against the
		// ResponseHeaderParam codec after this function returns.
		h := make(http.Header)
		h.Set("Location", "/users/"+user.ID)
		nethttp.WithResponseHeaders(ctx, h)
		// Deposit the session cookie — validated by the adapter against the
		// ResponseCookieParam codec after this function returns.
		nethttp.WithResponseCookies(ctx, nethttp.PendingCookie{
			Name:  "session",
			Value: "sess-" + user.ID + "-token",
			Opts:  nethttp.CookieOptions{MaxAge: 3600, Insecure: true},
		})
		return user, nil
	}
}

// makeGetUserHandler orchestrates the get-user pipeline:
//
// path param (HTTP infra) → Get (store IO) → buildUserResponse (L2) → encode (codec)
func makeGetUserHandler(store *UserStore) func(context.Context, struct{}) (User, error) {
	return func(ctx context.Context, _ struct{}) (User, error) {
		r, _ := nethttp.RequestFromContext(ctx)
		id := r.PathValue("id") // L3: HTTP path parameter
		record, ok := store.Get(id)
		if !ok {
			return User{}, fmt.Errorf("user %q not found", id)
		}
		return buildUserResponse(record), nil // L2: pure projection
	}
}

func main() {
	store := newUserStore()

	// Create separate loggers for domain and transport concerns.
	domainLogger := slog.Default().With("layer", "domain")
	httpLogger := slog.Default().With("transport", "http")

	// Build the REST API description (transport-agnostic).
	b := rest.NewBuilder(rest.Info{
		Title:       "User API",
		Version:     "1.0.0",
		Description: "Three-layer codec pipeline: HTTP ↔ domain ↔ database.",
	},
		// WithPathConstraints is optional. When set, AddRoute returns an
		// InvalidPathError immediately if the path violates the constraint.
		// HTTPPath requires a leading '/' and forbids unencoded spaces and null
		// bytes. OpenAPI-style path parameters like {id} are allowed, so
		// /users/{id} passes correctly.
		rest.WithPathConstraints(validate.HTTPPath),
	)
	b.AddServer("local", rest.Server{URL: "http://localhost:8080"})

	// locationCodec validates Location response header values as non-empty strings.
	// sessionCodec validates the session cookie value (at least 8 chars).
	// The same codecs enforce the contract at route-definition time and at
	// request-handling time — the adapter rejects invalid values with 500.
	locationCodec := codex.String().Refine(validate.NonEmptyString)
	sessionCodec := codex.String().Refine(validate.MinLen(8))

	createUserRoute, err := rest.NewRoute[CreateUserReq, User]("POST", "/users",
		createUserReqCodec, userCodec,
		rest.RouteMeta{
			OperationID:    "createUser",
			Summary:        "Create a user",
			ReqSchemaName:  "CreateUserRequest",
			RespSchemaName: "User",
		},
		// ResponseHeaderParam declares the Location header returned on 201.
		// The adapter validates it after the handler returns; a violation → 500.
		// The codec schema flows into the OpenAPI response header spec automatically.
		rest.ResponseHeaderParam{
			Name:        "Location",
			Description: "URL of the newly created user resource",
			Required:    true,
			Codec:       &locationCodec,
		},
		// ResponseCookieParam declares the session cookie written on 201.
		// The adapter validates the value after the handler returns; a violation → 500.
		rest.ResponseCookieParam{
			Name:        "session",
			Description: "Session token for the new user",
			Required:    true,
			Codec:       &sessionCodec,
		},
	).Register(b)
	if err != nil {
		fmt.Fprintf(os.Stderr, "route registration failed: %v\n", err)
		os.Exit(1)
	}
	// WithRequestFormats enables multi-format request bodies.
	// The adapter negotiates by Content-Type; unsupported types return 415.
	// The OpenAPI spec gains additional content-type entries automatically.
	createUserRoute = createUserRoute.WithRequestFormats(
		format.JSON(createUserReqCodec),
		format.YAML(createUserReqCodec),
	)

	uuidCodec := codex.String().Refine(validate.UUID)
	getUserRoute, err := rest.NewRoute[struct{}, User]("GET", "/users/{id}",
		codex.Empty, userCodec,
		rest.RouteMeta{
			OperationID:    "getUser",
			Summary:        "Get a user by ID",
			RespSchemaName: "User",
		},
		// PathParam.Codec validates {id} as a UUID at BuildPath time and
		// flows the UUID schema into the OpenAPI spec automatically.
		rest.PathParam{
			Name:        "id",
			Description: "User UUID",
			Codec:       &uuidCodec,
		},
	).Register(b)
	if err != nil {
		fmt.Fprintf(os.Stderr, "route registration failed: %v\n", err)
		os.Exit(1)
	}

	// GET /users — list users with optional query parameters.
	// QueryParam.Codec validates the ?page value at request time via the nethttp
	// adapter (auto-called before the handler). The schema flows into the OpenAPI
	// spec automatically. ?search has no codec — it is documented only.
	qPageCodec := codex.String().Refine(validate.NonNegativeIntString)
	listUsersRoute, err := rest.NewRoute[struct{}, PagedUsersResp]("GET", "/users",
		codex.Empty, pagedUsersRespCodec,
		rest.RouteMeta{
			OperationID: "listUsers",
			Summary:     "List users",
		},
		rest.QueryParam{
			Name:        "page",
			Description: "Page number (0-based, non-negative integer)",
			Codec:       &qPageCodec,
		},
		rest.QueryParam{
			Name:        "search",
			Description: "Filter by name prefix (no validation)",
		},
	).Register(b)
	if err != nil {
		fmt.Fprintf(os.Stderr, "route registration failed: %v\n", err)
		os.Exit(1)
	}

	// GET /profile — demonstrates CookieParam and HeaderParam validation.
	// session_token cookie: required, non-empty (validates auth session).
	// X-Request-ID header: required UUID (idempotency/tracing key).
	// The nethttp adapter calls ValidateCookies and ValidateHeaders automatically
	// before the handler runs — no manual extraction needed.
	profileSessionCodec := codex.String().Refine(validate.NonEmptyString)
	profileRequestIDCodec := codex.String().Refine(validate.UUID)
	profileRoute, err := rest.NewRoute[struct{}, User]("GET", "/profile",
		codex.Empty, userCodec,
		rest.RouteMeta{
			OperationID: "getProfile",
			Summary:     "Get the current user profile",
		},
		rest.CookieParam{
			Name:        "session_token",
			Description: "Active session token",
			Required:    true,
			Codec:       &profileSessionCodec,
		},
		rest.HeaderParam{
			Name:        "X-Request-Id",
			Description: "Idempotency and tracing UUID",
			Required:    true,
			Codec:       &profileRequestIDCodec,
		},
	).Register(b)
	if err != nil {
		fmt.Fprintf(os.Stderr, "route registration failed: %v\n", err)
		os.Exit(1)
	}

	// Distinguishes validation failures (400, Warn) from system errors (500, Error).
	errorHandler := func(w http.ResponseWriter, r *http.Request, status int, err error) {
		var validationErrs codex.ValidationErrors
		if errors.As(err, &validationErrs) {
			httpLogger.Warn("request validation failed",
				"method", r.Method,
				"path", r.URL.Path,
				"errors", validationErrs, // triggers ValidationErrors.LogValue()
			)
		} else {
			httpLogger.Error("handler error",
				"method", r.Method,
				"path", r.URL.Path,
				"error", err, // triggers ConstraintError.LogValue() etc.
			)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		body, _ := json.Marshal(map[string]string{"error": err.Error()})
		_, _ = w.Write(body)
	}

	// Attribute extractors for domain logging — extract business-relevant fields from responses.
	extractUserAttrs := func(_ CreateUserReq, u User) []slog.Attr {
		return []slog.Attr{
			slog.String("id", u.ID),
			slog.String("name", u.Name),
			slog.String("email", u.Email),
		}
	}
	extractGetUserAttrs := func(_ struct{}, u User) []slog.Attr {
		return []slog.Attr{slog.String("id", u.ID)}
	}

	obs := &CountingObserver{}
	opts := nethttp.Options{ErrorHandler: errorHandler, Observer: obs}

	// Wire infrastructure handlers to HTTP routes with custom error handling + domain logging decorator.
	mux := http.NewServeMux()
	nethttp.Register(mux, createUserRoute,
		withDomainLogging("user.create", makeCreateUserHandler(store), domainLogger, extractUserAttrs),
		opts)
	nethttp.Register(mux, getUserRoute,
		withDomainLogging("user.get", makeGetUserHandler(store), domainLogger, extractGetUserAttrs),
		opts)
	nethttp.Register(mux, listUsersRoute,
		withDomainLogging("user.list", makeListUsersHandler(), domainLogger,
			func(_ struct{}, _ PagedUsersResp) []slog.Attr { return nil }),
		opts)
	nethttp.Register(mux, profileRoute,
		func(_ context.Context, _ struct{}) (User, error) {
			// Handler only runs when both cookie and header are valid.
			return User{ID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Name: "Alice", Email: "alice@example.com"}, nil
		},
		opts)

	// Demo requests against an in-process test server.
	srv := httptest.NewServer(mux)
	defer srv.Close()

	fmt.Println("=== POST /users ===")
	resp, err := http.Post(srv.URL+"/users", "application/json", //nolint:noctx
		strings.NewReader(`{"name":"Alice","email":"alice@example.com"}`))
	if err != nil {
		fmt.Fprintf(os.Stderr, "POST error: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	var created User
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		fmt.Fprintf(os.Stderr, "decode error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Status:     %d\nUser:       %+v\nLocation:   %s\nSet-Cookie: %s\n\n",
		resp.StatusCode, created, resp.Header.Get("Location"), resp.Header.Get("Set-Cookie"))

	fmt.Println("=== POST /users (YAML body — multi-format request) ===")
	// WithRequestFormats enables content negotiation for incoming request bodies.
	// The adapter picks the decoder matching the Content-Type header.
	yamlReq, _ := http.NewRequest(http.MethodPost, srv.URL+"/users", //nolint:noctx
		strings.NewReader("name: Bob\nemail: bob@example.com\n"))
	yamlReq.Header.Set("Content-Type", "application/yaml")
	respYAML, err := http.DefaultClient.Do(yamlReq)
	if err != nil {
		fmt.Fprintf(os.Stderr, "POST error: %v\n", err)
		os.Exit(1)
	}
	defer respYAML.Body.Close()
	var createdYAML User
	_ = json.NewDecoder(respYAML.Body).Decode(&createdYAML)
	fmt.Printf("Status: %d\nUser:   %+v\n\n", respYAML.StatusCode, createdYAML)

	fmt.Println("=== POST /users (unsupported Content-Type → 415) ===")
	xmlReq, _ := http.NewRequest(http.MethodPost, srv.URL+"/users", //nolint:noctx
		strings.NewReader(`<name>Carol</name>`))
	xmlReq.Header.Set("Content-Type", "application/xml")
	respXML, err := http.DefaultClient.Do(xmlReq)
	if err != nil {
		fmt.Fprintf(os.Stderr, "POST error: %v\n", err)
		os.Exit(1)
	}
	defer respXML.Body.Close()
	var xmlErrBody map[string]string
	_ = json.NewDecoder(respXML.Body).Decode(&xmlErrBody)
	fmt.Printf("Status: %d\nError:  %s\n\n", respXML.StatusCode, xmlErrBody["error"])

	fmt.Println("=== POST /users (body constraint violation) ===")
	resp2, err := http.Post(srv.URL+"/users", "application/json", //nolint:noctx
		strings.NewReader(`{"name":"","email":"bad"}`))
	if err != nil {
		fmt.Fprintf(os.Stderr, "POST error: %v\n", err)
		os.Exit(1)
	}
	defer resp2.Body.Close()
	var errBody map[string]string
	_ = json.NewDecoder(resp2.Body).Decode(&errBody)
	fmt.Printf("Status: %d\nError:  %s\n\n", resp2.StatusCode, errBody["error"])

	fmt.Println("=== GET /users/{id} — BuildPath validates UUID codec ===")
	// BuildPath substitutes {id} and validates the value against the UUID codec.
	userPath, err := getUserRoute.BuildPath(map[string]string{"id": created.ID})
	if err != nil {
		fmt.Fprintf(os.Stderr, "BuildPath error: %v\n", err)
		os.Exit(1)
	}
	resp3, err := http.Get(srv.URL + userPath) //nolint:noctx
	if err != nil {
		fmt.Fprintf(os.Stderr, "GET error: %v\n", err)
		os.Exit(1)
	}
	defer resp3.Body.Close()
	var fetched User
	_ = json.NewDecoder(resp3.Body).Decode(&fetched)
	fmt.Printf("Status: %d\nUser:   %+v\n", resp3.StatusCode, fetched)

	// BuildPath rejects a non-UUID value before any HTTP request is made.
	if _, err := getUserRoute.BuildPath(map[string]string{"id": "not-a-uuid"}); err != nil {
		fmt.Printf("BuildPath(not-a-uuid) rejected: %v\n\n", err)
	}

	fmt.Println("=== GET /users?page=2&search=alice (query params) ===")
	// ?page is validated against NonNegativeIntString by the nethttp adapter.
	// ?search has no codec — passed through to the handler as-is.
	resp4, err := http.Get(srv.URL + "/users?page=2&search=alice") //nolint:noctx
	if err != nil {
		fmt.Fprintf(os.Stderr, "GET error: %v\n", err)
		os.Exit(1)
	}
	defer resp4.Body.Close()
	var listResp PagedUsersResp
	_ = json.NewDecoder(resp4.Body).Decode(&listResp)
	fmt.Printf("Status: %d\nResult: %+v\n\n", resp4.StatusCode, listResp)

	fmt.Println("=== GET /users?page=abc (invalid query param — auto-rejected) ===")
	// The nethttp adapter calls ValidateQuery before the handler.
	// "abc" fails NonNegativeIntString → 400 returned, handler never runs.
	resp5, err := http.Get(srv.URL + "/users?page=abc") //nolint:noctx
	if err != nil {
		fmt.Fprintf(os.Stderr, "GET error: %v\n", err)
		os.Exit(1)
	}
	defer resp5.Body.Close()
	var qErrBody map[string]string
	_ = json.NewDecoder(resp5.Body).Decode(&qErrBody)
	fmt.Printf("Status: %d\nError:  %s\n\n", resp5.StatusCode, qErrBody["error"])

	fmt.Println("=== GET /profile (valid cookie + header) ===")
	// Both session_token cookie and X-Request-Id header are valid.
	req6, err := http.NewRequest(http.MethodGet, srv.URL+"/profile", nil) //nolint:noctx
	if err != nil {
		fmt.Fprintf(os.Stderr, "NewRequest error: %v\n", err)
		os.Exit(1)
	}
	req6.AddCookie(&http.Cookie{Name: "session_token", Value: "my-valid-session-token", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	req6.Header.Set("X-Request-Id", "f47ac10b-58cc-4372-a567-0e02b2c3d479")
	resp6, err := http.DefaultClient.Do(req6)
	if err != nil {
		fmt.Fprintf(os.Stderr, "GET error: %v\n", err)
		os.Exit(1)
	}
	defer resp6.Body.Close()
	var profile User
	_ = json.NewDecoder(resp6.Body).Decode(&profile)
	fmt.Printf("Status: %d\nUser:   %+v\n\n", resp6.StatusCode, profile)

	fmt.Println("=== GET /profile (invalid cookie — auto-rejected) ===")
	req7, err := http.NewRequest(http.MethodGet, srv.URL+"/profile", nil) //nolint:noctx
	if err != nil {
		fmt.Fprintf(os.Stderr, "NewRequest error: %v\n", err)
		os.Exit(1)
	}
	req7.AddCookie(&http.Cookie{Name: "session_token", Value: "", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	req7.Header.Set("X-Request-Id", "f47ac10b-58cc-4372-a567-0e02b2c3d479")
	resp7, err := http.DefaultClient.Do(req7)
	if err != nil {
		fmt.Fprintf(os.Stderr, "GET error: %v\n", err)
		os.Exit(1)
	}
	defer resp7.Body.Close()
	var cookieErrBody map[string]string
	_ = json.NewDecoder(resp7.Body).Decode(&cookieErrBody)
	fmt.Printf("Status: %d\nError:  %s\n\n", resp7.StatusCode, cookieErrBody["error"])

	fmt.Println("=== GET /profile (invalid header — auto-rejected) ===")
	req8, err := http.NewRequest(http.MethodGet, srv.URL+"/profile", nil) //nolint:noctx
	if err != nil {
		fmt.Fprintf(os.Stderr, "NewRequest error: %v\n", err)
		os.Exit(1)
	}
	req8.AddCookie(&http.Cookie{Name: "session_token", Value: "my-valid-session-token", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	req8.Header.Set("X-Request-Id", "not-a-uuid")
	resp8, err := http.DefaultClient.Do(req8)
	if err != nil {
		fmt.Fprintf(os.Stderr, "GET error: %v\n", err)
		os.Exit(1)
	}
	defer resp8.Body.Close()
	var headerErrBody map[string]string
	_ = json.NewDecoder(resp8.Body).Decode(&headerErrBody)
	fmt.Printf("Status: %d\nError:  %s\n\n", resp8.StatusCode, headerErrBody["error"])

	fmt.Println("=== ResponseHeaderParams + ResponseCookieParams — contract violation demos ===")
	// A separate server uses a handler that:
	//   (a) deposits an empty Location value → fails NonEmptyString codec → adapter returns 500
	//   (b) deposits a too-short session value → fails MinLen(8) codec → adapter returns 500
	violationMux := http.NewServeMux()
	nethttp.Register(violationMux, createUserRoute,
		func(ctx context.Context, req CreateUserReq) (User, error) {
			h := make(http.Header)
			h.Set("Location", "") // empty → fails NonEmptyString → 500
			nethttp.WithResponseHeaders(ctx, h)
			nethttp.WithResponseCookies(ctx, nethttp.PendingCookie{
				Name:  "session",
				Value: "short", // < 8 chars → fails MinLen(8) → 500
				Opts:  nethttp.CookieOptions{Insecure: true},
			})
			return buildUserResponse(buildUserRecord(req)), nil
		},
		nethttp.Options{Observer: obs})
	violationSrv := httptest.NewServer(violationMux)
	defer violationSrv.Close()

	violResp, err := http.Post(violationSrv.URL+"/users", "application/json", //nolint:noctx
		strings.NewReader(`{"name":"Carol","email":"carol@example.com"}`))
	if err != nil {
		fmt.Fprintf(os.Stderr, "POST error: %v\n", err)
		os.Exit(1)
	}
	defer violResp.Body.Close()
	var violBody map[string]string
	_ = json.NewDecoder(violResp.Body).Decode(&violBody)
	fmt.Printf("Contract violation → Status: %d, error: %s\n\n", violResp.StatusCode, violBody["error"])

	fmt.Println("=== Response body encode violation — symmetric validation ===")
	// The same codec that rejects an invalid request body at 400 now also rejects
	// an invalid response body at 500. Refine constraints run on both Encode and Decode.
	bodyViolMux := http.NewServeMux()
	nethttp.Register(bodyViolMux, createUserRoute,
		func(ctx context.Context, req CreateUserReq) (User, error) {
			// Handler deliberately returns a User with invalid field values to
			// demonstrate that handle.Encode now validates the response body.
			return User{
				ID:    "not-a-uuid",   // fails UUID constraint
				Name:  "",             // fails NonEmptyString constraint
				Email: "not-an-email", // fails Email constraint
			}, nil
		},
		nethttp.Options{Observer: obs})
	bodyViolSrv := httptest.NewServer(bodyViolMux)
	defer bodyViolSrv.Close()

	bodyViolResp, err := http.Post(bodyViolSrv.URL+"/users", "application/json", //nolint:noctx
		strings.NewReader(`{"name":"Dave","email":"dave@example.com"}`))
	if err != nil {
		fmt.Fprintf(os.Stderr, "POST error: %v\n", err)
		os.Exit(1)
	}
	defer bodyViolResp.Body.Close()
	var bodyViolBody map[string]string
	_ = json.NewDecoder(bodyViolResp.Body).Decode(&bodyViolBody)
	fmt.Printf("Response body violation → Status: %d, error: %s\n\n", bodyViolResp.StatusCode, bodyViolBody["error"])

	// nethttp.SetCookie writes a Set-Cookie header with secure defaults
	// (Secure, HttpOnly, SameSite=Strict). When CookieOptions.Codec is set,
	// the value is validated against that codec before writing — the same codec
	// used in CookieParam for the read path. One definition, two boundaries.
	setRec := httptest.NewRecorder()
	if err := nethttp.SetCookie(setRec, "session_token", "refreshed-session-token", nethttp.CookieOptions{
		Codec:  &profileSessionCodec, // same codec as read-side CookieParam
		MaxAge: 3600,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "SetCookie error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Valid:    Set-Cookie: %s\n", setRec.Header().Get("Set-Cookie"))

	setRec2 := httptest.NewRecorder()
	setErr := nethttp.SetCookie(setRec2, "session_token", "", nethttp.CookieOptions{}.WithCodec(profileSessionCodec))
	fmt.Printf("Invalid:  error=%v, Set-Cookie=%q\n\n", setErr, setRec2.Header().Get("Set-Cookie"))

	fmt.Println("=== Observer summary ===")
	obs.Print()
	fmt.Println()

	fmt.Println("=== OpenAPI 3.1 spec (derived from domain codecs) ===")
	doc, err := b.OpenAPISpec()
	if err != nil {
		fmt.Fprintf(os.Stderr, "OpenAPISpec error: %v\n", err)
		os.Exit(1)
	}
	yaml, err := doc.MarshalYAML()
	if err != nil {
		fmt.Fprintf(os.Stderr, "MarshalYAML error: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(string(yaml))
}
