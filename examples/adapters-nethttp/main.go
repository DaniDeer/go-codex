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

	nethttp "github.com/DaniDeer/go-codex/adapters/nethttp"
	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/validate"
)

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
	codex.RequiredField[CreateUserReq, string]("name", nameFieldCodec,
		func(r CreateUserReq) string { return r.Name },
		func(r *CreateUserReq, v string) { r.Name = v },
	),
	codex.RequiredField[CreateUserReq, string]("email", emailFieldCodec,
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
	codex.RequiredField[UserRecord, string]("id",
		codex.String().Refine(validate.UUID).WithDescription("Primary key."),
		func(r UserRecord) string { return r.ID },
		func(r *UserRecord, v string) { r.ID = v },
	),
	codex.RequiredField[UserRecord, string]("name", nameFieldCodec,
		func(r UserRecord) string { return r.Name },
		func(r *UserRecord, v string) { r.Name = v },
	),
	codex.RequiredField[UserRecord, string]("email", emailFieldCodec,
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
	codex.OptionalField[User, string]("id",
		codex.String().Refine(validate.UUID).WithDescription("User UUID."),
		func(u User) string { return u.ID },
		func(u *User, v string) { u.ID = v },
	),
	codex.OptionalField[User, string]("name", nameFieldCodec,
		func(u User) string { return u.Name },
		func(u *User, v string) { u.Name = v },
	),
	codex.OptionalField[User, string]("email", emailFieldCodec,
		func(u User) string { return u.Email },
		func(u *User, v string) { u.Email = v },
	),
)

// emptyReq is used for routes that carry no request body (e.g. GET).
type emptyReq struct{}

var emptyReqCodec = codex.Struct[emptyReq]()

// PagedUsersResp is the response for a paginated user list.
type PagedUsersResp struct {
	Page   int
	Search string
	Users  []User
}

// pagedUsersRespCodec describes the paginated response.
var pagedUsersRespCodec = codex.Struct[PagedUsersResp](
	codex.RequiredField[PagedUsersResp, int]("page",
		codex.Int().WithDescription("Current page number."),
		func(r PagedUsersResp) int { return r.Page },
		func(r *PagedUsersResp, v int) { r.Page = v },
	),
	codex.OptionalField[PagedUsersResp, string]("search",
		codex.String().WithDescription("Active name filter."),
		func(r PagedUsersResp) string { return r.Search },
		func(r *PagedUsersResp, v string) { r.Search = v },
	),
)

// makeListUsersPageHandler returns a paginated user list for the given page number.
func makeListUsersPageHandler() func(context.Context, emptyReq) (PagedUsersResp, error) {
	return func(ctx context.Context, _ emptyReq) (PagedUsersResp, error) {
		r, _ := nethttp.RequestFromContext(ctx)
		page := r.PathValue("page") // L3: HTTP path parameter (already validated by BuildPath)
		_ = page
		return PagedUsersResp{Page: 1, Users: nil}, nil
	}
}

// makeListUsersHandler handles GET /users with query params.
// The nethttp adapter validates ?page against the NonNegativeIntString codec
// before this handler is called — no manual parsing needed for bad input.
func makeListUsersHandler() func(context.Context, emptyReq) (PagedUsersResp, error) {
	return func(ctx context.Context, _ emptyReq) (PagedUsersResp, error) {
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
	return func(_ context.Context, req CreateUserReq) (User, error) {
		record := buildUserRecord(req)             // L2: pure business rule
		if err := store.Save(record); err != nil { // L3: database IO
			return User{}, err
		}
		return buildUserResponse(record), nil // L2: pure projection
	}
}

// makeGetUserHandler orchestrates the get-user pipeline:
//
// path param (HTTP infra) → Get (store IO) → buildUserResponse (L2) → encode (codec)
func makeGetUserHandler(store *UserStore) func(context.Context, emptyReq) (User, error) {
	return func(ctx context.Context, _ emptyReq) (User, error) {
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

	createUserRoute, err := rest.AddRoute[CreateUserReq, User](b, "POST", "/users",
		createUserReqCodec, userCodec, rest.RouteConfig{
			OperationID:    "createUser",
			Summary:        "Create a user",
			ReqSchemaName:  "CreateUserRequest",
			RespSchemaName: "User",
		})
	if err != nil {
		fmt.Fprintf(os.Stderr, "route registration failed: %v\n", err)
		os.Exit(1)
	}

	uuidCodec := codex.String().Refine(validate.UUID)
	getUserRoute, err := rest.AddRoute[emptyReq, User](b, "GET", "/users/{id}",
		emptyReqCodec, userCodec, rest.RouteConfig{
			OperationID:    "getUser",
			Summary:        "Get a user by ID",
			RespSchemaName: "User",
			// PathParam.Codec validates {id} as a UUID at BuildPath time and
			// flows the UUID schema into the OpenAPI spec automatically.
			PathParams: []rest.PathParam{{
				Name:        "id",
				Description: "User UUID",
				Codec:       &uuidCodec,
			}},
		})
	if err != nil {
		fmt.Fprintf(os.Stderr, "route registration failed: %v\n", err)
		os.Exit(1)
	}

	// GET /users/page/{page} — list users by page number.
	// PathParam.Codec (PositiveIntString) rejects "0", "-1", "abc" — all before
	// any HTTP request is made. The UUID schema flows into the OpenAPI spec.
	// The final assembled path is also re-validated against the builder-level
	// HTTPPath codec, catching any path-level constraint violations.
	pageCodec := codex.String().Refine(validate.PositiveIntString)
	listUsersPageRoute, err := rest.AddRoute[emptyReq, PagedUsersResp](b, "GET", "/users/page/{page}",
		emptyReqCodec, pagedUsersRespCodec, rest.RouteConfig{
			OperationID: "listUsersPage",
			Summary:     "List users by page",
			PathParams: []rest.PathParam{{
				Name:        "page",
				Description: "Page number (positive integer)",
				Codec:       &pageCodec,
			}},
		})
	if err != nil {
		fmt.Fprintf(os.Stderr, "route registration failed: %v\n", err)
		os.Exit(1)
	}

	// GET /users — list users with optional query parameters.
	// QueryParam.Codec validates the ?page value at request time via the nethttp
	// adapter (auto-called before the handler). The schema flows into the OpenAPI
	// spec automatically. ?search has no codec — it is documented only.
	qPageCodec := codex.String().Refine(validate.NonNegativeIntString)
	listUsersRoute, err := rest.AddRoute[emptyReq, PagedUsersResp](b, "GET", "/users",
		emptyReqCodec, pagedUsersRespCodec, rest.RouteConfig{
			OperationID: "listUsers",
			Summary:     "List users",
			QueryParams: []rest.QueryParam{
				{
					Name:        "page",
					Description: "Page number (0-based, non-negative integer)",
					Codec:       &qPageCodec,
				},
				{
					Name:        "search",
					Description: "Filter by name prefix (no validation)",
				},
			},
		})
	if err != nil {
		fmt.Fprintf(os.Stderr, "route registration failed: %v\n", err)
		os.Exit(1)
	}

	// Custom ErrorHandler using httpLogger for transport-level errors.
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
	extractGetUserAttrs := func(_ emptyReq, u User) []slog.Attr {
		return []slog.Attr{slog.String("id", u.ID)}
	}

	// Wire infrastructure handlers to HTTP routes with custom error handling + domain logging decorator.
	mux := http.NewServeMux()
	nethttp.Register(mux, createUserRoute,
		withDomainLogging("user.create", makeCreateUserHandler(store), domainLogger, extractUserAttrs),
		nethttp.Options{ErrorHandler: errorHandler})
	nethttp.Register(mux, getUserRoute,
		withDomainLogging("user.get", makeGetUserHandler(store), domainLogger, extractGetUserAttrs),
		nethttp.Options{ErrorHandler: errorHandler})
	nethttp.Register(mux, listUsersPageRoute,
		withDomainLogging("user.list-page", makeListUsersPageHandler(), domainLogger,
			func(_ emptyReq, _ PagedUsersResp) []slog.Attr { return nil }),
		nethttp.Options{ErrorHandler: errorHandler})
	nethttp.Register(mux, listUsersRoute,
		withDomainLogging("user.list", makeListUsersHandler(), domainLogger,
			func(_ emptyReq, _ PagedUsersResp) []slog.Attr { return nil }),
		nethttp.Options{ErrorHandler: errorHandler})

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
	fmt.Printf("Status: %d\nUser:   %+v\n\n", resp.StatusCode, created)

	fmt.Println("=== POST /users (domain constraint violation) ===")
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

	fmt.Println("=== GET /users/{id} ===")
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
	fmt.Printf("Status: %d\nUser:   %+v\n\n", resp3.StatusCode, fetched)

	fmt.Println("=== GET /users/page/{page} (valid page) ===")
	// BuildPath validates {page} via PositiveIntString before making any request.
	pagePath, err := listUsersPageRoute.BuildPath(map[string]string{"page": "2"})
	if err != nil {
		fmt.Fprintf(os.Stderr, "BuildPath error: %v\n", err)
		os.Exit(1)
	}
	resp4, err := http.Get(srv.URL + pagePath) //nolint:noctx
	if err != nil {
		fmt.Fprintf(os.Stderr, "GET error: %v\n", err)
		os.Exit(1)
	}
	defer resp4.Body.Close()
	var pagedResp PagedUsersResp
	_ = json.NewDecoder(resp4.Body).Decode(&pagedResp)
	fmt.Printf("Status: %d\nPage:   %+v\n\n", resp4.StatusCode, pagedResp)

	fmt.Println("=== GET /users/page/{page} (invalid page — BuildPath error) ===")
	// "abc" fails PositiveIntString: not a valid integer.
	if _, err := listUsersPageRoute.BuildPath(map[string]string{"page": "abc"}); err != nil {
		fmt.Printf("BuildPath(abc) rejected: %v\n", err)
	}
	// "0" fails PositiveIntString: not positive.
	if _, err := listUsersPageRoute.BuildPath(map[string]string{"page": "0"}); err != nil {
		fmt.Printf("BuildPath(0) rejected:   %v\n\n", err)
	}

	fmt.Println("=== GET /users?page=2&search=alice (query params) ===")
	// ?page is validated against NonNegativeIntString by the nethttp adapter.
	// ?search has no codec — passed through to the handler as-is.
	resp5, err := http.Get(srv.URL + "/users?page=2&search=alice") //nolint:noctx
	if err != nil {
		fmt.Fprintf(os.Stderr, "GET error: %v\n", err)
		os.Exit(1)
	}
	defer resp5.Body.Close()
	var listResp PagedUsersResp
	_ = json.NewDecoder(resp5.Body).Decode(&listResp)
	fmt.Printf("Status: %d\nResult: %+v\n\n", resp5.StatusCode, listResp)

	fmt.Println("=== GET /users?page=abc (invalid query param — auto-rejected) ===")
	// The nethttp adapter calls ValidateQuery before the handler.
	// "abc" fails NonNegativeIntString → 400 returned, handler never runs.
	resp6, err := http.Get(srv.URL + "/users?page=abc") //nolint:noctx
	if err != nil {
		fmt.Fprintf(os.Stderr, "GET error: %v\n", err)
		os.Exit(1)
	}
	defer resp6.Body.Close()
	var qErrBody map[string]string
	_ = json.NewDecoder(resp6.Body).Decode(&qErrBody)
	fmt.Printf("Status: %d\nError:  %s\n\n", resp6.StatusCode, qErrBody["error"])

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
