// Package adapters-chi demonstrates the three-layer codec pipeline pattern
// using the chi router. The architecture mirrors adapters-nethttp exactly —
// the only difference is the HTTP router and how path variables are extracted.
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
// Routes:
//   - POST /users      — body validate, response headers + cookies (codec-validated), content negotiation
//   - GET  /users/{id} — UUID path param, BuildPath type-safe URL construction
//   - GET  /users      — query params (page, search)
//   - GET  /profile    — request-side cookie + header validation
//
// A CountingObserver wired into every route collects per-request metrics and
// per-field validation errors without any metrics library dependency.
//
// Run with: go run ./examples/adapters-chi
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

	gochi "github.com/go-chi/chi/v5"

	chiadapter "github.com/DaniDeer/go-codex/adapters/chi"
	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/validate"
)

// ── Observer ──────────────────────────────────────────────────────────────────

// CountingObserver is an in-memory implementation of [stats.Observer].
// In production, replace the counters with Prometheus / OpenTelemetry instruments.
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
type UserRecord struct {
	ID    string
	Name  string
	Email string
}

// userRecordCodec describes the SQL model. The store uses this codec to
// encode records for persistence and decode rows on retrieval.
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

// ── Layer 2: Business logic (pure domain functions) ───────────────────────────
//
// Pure domain functions transform between domain types. Zero IO — no database,
// no HTTP, no external services.

// buildUserRecord creates a database record from a user creation request.
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

// withDomainLogging is a decorator that wraps a handler function, logging
// success (Info) or failure (Error) after the handler returns.
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
type UserStore struct {
	mu   sync.RWMutex
	rows map[string]map[string]any
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

// Get decodes a UserRecord from a stored row using userRecordCodec.
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
		record := buildUserRecord(req)
		if err := store.Save(record); err != nil {
			return User{}, err
		}
		user := buildUserResponse(record)
		// Deposit the Location header — validated by the adapter against the
		// ResponseHeaderParam codec after this function returns.
		h := make(http.Header)
		h.Set("Location", "/users/"+user.ID)
		chiadapter.WithResponseHeaders(ctx, h)
		// Deposit the session cookie — validated by the adapter against the
		// ResponseCookieParam codec after this function returns.
		chiadapter.WithResponseCookies(ctx, chiadapter.PendingCookie{
			Name:  "session",
			Value: "sess-" + user.ID + "-token",
			Opts:  chiadapter.CookieOptions{MaxAge: 3600, Insecure: true},
		})
		return user, nil
	}
}

// makeGetUserHandler orchestrates the get-user pipeline.
// Chi path vars are extracted via chi.URLParam from the raw *http.Request.
func makeGetUserHandler(store *UserStore) func(context.Context, emptyReq) (User, error) {
	return func(ctx context.Context, _ emptyReq) (User, error) {
		r, _ := chiadapter.RequestFromContext(ctx)
		id := gochi.URLParam(r, "id") // L3: HTTP path parameter (chi-specific)
		record, ok := store.Get(id)
		if !ok {
			return User{}, fmt.Errorf("user %q not found", id)
		}
		return buildUserResponse(record), nil
	}
}

// makeListUsersHandler handles GET /users with query params.
// The chi adapter validates ?page before this handler is called.
func makeListUsersHandler() func(context.Context, emptyReq) (PagedUsersResp, error) {
	return func(ctx context.Context, _ emptyReq) (PagedUsersResp, error) {
		r, _ := chiadapter.RequestFromContext(ctx)
		q := r.URL.Query()
		page := 0
		if p := q.Get("page"); p != "" {
			_, _ = fmt.Sscanf(p, "%d", &page)
		}
		search := q.Get("search")
		return PagedUsersResp{Page: page, Search: search, Users: nil}, nil
	}
}

func main() {
	store := newUserStore()

	domainLogger := slog.Default().With("layer", "domain")
	httpLogger := slog.Default().With("transport", "http")

	b := rest.NewBuilder(rest.Info{
		Title:       "User API (chi)",
		Version:     "1.0.0",
		Description: "Three-layer codec pipeline: HTTP ↔ domain ↔ database.",
	},
		rest.WithPathConstraints(validate.HTTPPath),
	)
	b.AddServer("local", rest.Server{URL: "http://localhost:8080"})

	locationCodec := codex.String().Refine(validate.NonEmptyString)
	sessionCodec := codex.String().Refine(validate.MinLen(8))

	createUserRoute, err := rest.AddRoute[CreateUserReq, User](b, "POST", "/users",
		createUserReqCodec, userCodec, rest.RouteConfig{
			OperationID:    "createUser",
			Summary:        "Create a user",
			ReqSchemaName:  "CreateUserRequest",
			RespSchemaName: "User",
			ResponseHeaderParams: []rest.ResponseHeaderParam{{
				Name:        "Location",
				Description: "URL of the newly created user resource",
				Required:    true,
				Codec:       &locationCodec,
			}},
			ResponseCookieParams: []rest.ResponseCookieParam{{
				Name:        "session",
				Description: "Session token for the new user",
				Required:    true,
				Codec:       &sessionCodec,
			}},
		},
		format.JSON[User](userCodec),
		format.YAML[User](userCodec),
	)
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
			PathParams: []rest.PathParam{{
				Name:        "id",
				Description: "User UUID",
				Codec:       &uuidCodec,
			}},
		},
		format.JSON[User](userCodec),
		format.YAML[User](userCodec),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "route registration failed: %v\n", err)
		os.Exit(1)
	}

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
		},
		format.JSON[PagedUsersResp](pagedUsersRespCodec),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "route registration failed: %v\n", err)
		os.Exit(1)
	}

	profileSessionCodec := codex.String().Refine(validate.NonEmptyString)
	profileRequestIDCodec := codex.String().Refine(validate.UUID)
	profileRoute, err := rest.AddRoute[emptyReq, User](b, "GET", "/profile",
		emptyReqCodec, userCodec, rest.RouteConfig{
			OperationID: "getProfile",
			Summary:     "Get the current user profile",
			CookieParams: []rest.CookieParam{
				{
					Name:        "session_token",
					Description: "Active session token",
					Required:    true,
					Codec:       &profileSessionCodec,
				},
			},
			HeaderParams: []rest.HeaderParam{
				{
					Name:        "X-Request-Id",
					Description: "Idempotency and tracing UUID",
					Required:    true,
					Codec:       &profileRequestIDCodec,
				},
			},
		},
		format.JSON[User](userCodec),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "route registration failed: %v\n", err)
		os.Exit(1)
	}

	errorHandler := func(w http.ResponseWriter, r *http.Request, status int, err error) {
		var validationErrs codex.ValidationErrors
		if errors.As(err, &validationErrs) {
			httpLogger.Warn("request validation failed",
				"method", r.Method,
				"path", r.URL.Path,
				"errors", validationErrs,
			)
		} else {
			httpLogger.Error("handler error",
				"method", r.Method,
				"path", r.URL.Path,
				"error", err,
			)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		body, _ := json.Marshal(map[string]string{"error": err.Error()})
		_, _ = w.Write(body)
	}

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

	obs := &CountingObserver{}
	opts := chiadapter.Options{ErrorHandler: errorHandler, Observer: obs}

	r := gochi.NewRouter()
	chiadapter.Register(r, createUserRoute,
		withDomainLogging("user.create", makeCreateUserHandler(store), domainLogger, extractUserAttrs),
		opts)
	chiadapter.Register(r, getUserRoute,
		withDomainLogging("user.get", makeGetUserHandler(store), domainLogger, extractGetUserAttrs),
		opts)
	chiadapter.Register(r, listUsersRoute,
		withDomainLogging("user.list", makeListUsersHandler(), domainLogger,
			func(_ emptyReq, _ PagedUsersResp) []slog.Attr { return nil }),
		opts)
	chiadapter.Register(r, profileRoute,
		func(_ context.Context, _ emptyReq) (User, error) {
			return User{ID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Name: "Alice", Email: "alice@example.com"}, nil
		},
		opts)

	srv := httptest.NewServer(r)
	defer srv.Close()

	fmt.Println("=== chi adapter demo ===")

	fmt.Println("\n--- POST /users (JSON) ---")
	func() {
		body := strings.NewReader(`{"name":"Alice","email":"alice@example.com"}`)
		resp, err := http.Post(srv.URL+"/users", "application/json", body) //nolint:noctx
		if err != nil {
			panic(err)
		}
		defer resp.Body.Close()
		var created User
		if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
			panic(err)
		}
		fmt.Printf("Status:     %s\nUser:       %+v\nLocation:   %s\nSet-Cookie: %s\n",
			resp.Status, created, resp.Header.Get("Location"), resp.Header.Get("Set-Cookie"))
	}()

	fmt.Println("\n--- POST /users (YAML response via Accept header) ---")
	func() {
		body := strings.NewReader(`{"name":"Bob","email":"bob@example.com"}`)
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/users", body) //nolint:noctx
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/yaml")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			panic(err)
		}
		defer resp.Body.Close()
		fmt.Printf("Status: %s\nContent-Type: %s\nLocation: %s\n",
			resp.Status, resp.Header.Get("Content-Type"), resp.Header.Get("Location"))
	}()

	fmt.Println("\n--- POST /users (body constraint violation) ---")
	func() {
		body := strings.NewReader(`{"name":"","email":"bad"}`)
		resp, err := http.Post(srv.URL+"/users", "application/json", body) //nolint:noctx
		if err != nil {
			panic(err)
		}
		defer resp.Body.Close()
		var errBody map[string]string
		_ = json.NewDecoder(resp.Body).Decode(&errBody)
		fmt.Printf("Status: %s\nError:  %s\n", resp.Status, errBody["error"])
	}()

	fmt.Println("\n--- GET /users/{id} — BuildPath validates UUID codec ---")
	func() {
		// BuildPath substitutes {id} and validates the value against the UUID codec.
		userPath, err := getUserRoute.BuildPath(map[string]string{"id": "f47ac10b-58cc-4372-a567-0e02b2c3d479"})
		if err != nil {
			fmt.Fprintf(os.Stderr, "BuildPath error: %v\n", err)
			return
		}
		resp, err := http.Get(srv.URL + userPath) //nolint:noctx
		if err != nil {
			panic(err)
		}
		defer resp.Body.Close()
		var fetched User
		_ = json.NewDecoder(resp.Body).Decode(&fetched)
		fmt.Printf("Status: %s\nUser:   %+v\n", resp.Status, fetched)

		if _, err := getUserRoute.BuildPath(map[string]string{"id": "not-a-uuid"}); err != nil {
			fmt.Printf("BuildPath(not-a-uuid) rejected: %v\n", err)
		}
	}()

	fmt.Println("\n--- GET /users?page=2&search=alice (query params) ---")
	func() {
		resp, err := http.Get(srv.URL + "/users?page=2&search=alice") //nolint:noctx
		if err != nil {
			panic(err)
		}
		defer resp.Body.Close()
		var listResp PagedUsersResp
		_ = json.NewDecoder(resp.Body).Decode(&listResp)
		fmt.Printf("Status: %s\nResult: %+v\n", resp.Status, listResp)
	}()

	fmt.Println("\n--- GET /users?page=abc (invalid query param — auto-rejected) ---")
	func() {
		resp, err := http.Get(srv.URL + "/users?page=abc") //nolint:noctx
		if err != nil {
			panic(err)
		}
		defer resp.Body.Close()
		var errBody map[string]string
		_ = json.NewDecoder(resp.Body).Decode(&errBody)
		fmt.Printf("Status: %s\nError:  %s\n", resp.Status, errBody["error"])
	}()

	fmt.Println("\n--- GET /profile (valid cookie + header) ---")
	func() {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/profile", nil) //nolint:noctx
		req.AddCookie(&http.Cookie{Name: "session_token", Value: "my-valid-session-token", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
		req.Header.Set("X-Request-Id", "f47ac10b-58cc-4372-a567-0e02b2c3d479")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			panic(err)
		}
		defer resp.Body.Close()
		var profile User
		_ = json.NewDecoder(resp.Body).Decode(&profile)
		fmt.Printf("Status: %s\nUser:   %+v\n", resp.Status, profile)
	}()

	fmt.Println("\n--- GET /profile (invalid cookie — auto-rejected) ---")
	func() {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/profile", nil) //nolint:noctx
		req.AddCookie(&http.Cookie{Name: "session_token", Value: "", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
		req.Header.Set("X-Request-Id", "f47ac10b-58cc-4372-a567-0e02b2c3d479")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			panic(err)
		}
		defer resp.Body.Close()
		var errBody map[string]string
		_ = json.NewDecoder(resp.Body).Decode(&errBody)
		fmt.Printf("Status: %s\nError:  %s\n", resp.Status, errBody["error"])
	}()

	fmt.Println("\n--- GET /profile (invalid header — auto-rejected) ---")
	func() {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/profile", nil) //nolint:noctx
		req.AddCookie(&http.Cookie{Name: "session_token", Value: "my-valid-session-token", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
		req.Header.Set("X-Request-Id", "not-a-uuid")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			panic(err)
		}
		defer resp.Body.Close()
		var errBody map[string]string
		_ = json.NewDecoder(resp.Body).Decode(&errBody)
		fmt.Printf("Status: %s\nError:  %s\n", resp.Status, errBody["error"])
	}()

	fmt.Println("\n--- ResponseHeaderParams + ResponseCookieParams — contract violation demo ---")
	func() {
		violationRouter := gochi.NewRouter()
		chiadapter.Register(violationRouter, createUserRoute,
			func(ctx context.Context, req CreateUserReq) (User, error) {
				h := make(http.Header)
				h.Set("Location", "") // empty → fails NonEmptyString → 500
				chiadapter.WithResponseHeaders(ctx, h)
				chiadapter.WithResponseCookies(ctx, chiadapter.PendingCookie{
					Name:  "session",
					Value: "short", // < 8 chars → fails MinLen(8) → 500
					Opts:  chiadapter.CookieOptions{Insecure: true},
				})
				return buildUserResponse(buildUserRecord(req)), nil
			},
			chiadapter.Options{Observer: obs})
		violationSrv := httptest.NewServer(violationRouter)
		defer violationSrv.Close()

		body := strings.NewReader(`{"name":"Carol","email":"carol@example.com"}`)
		resp, err := http.Post(violationSrv.URL+"/users", "application/json", body) //nolint:noctx
		if err != nil {
			panic(err)
		}
		defer resp.Body.Close()
		var errBody map[string]string
		_ = json.NewDecoder(resp.Body).Decode(&errBody)
		fmt.Printf("Contract violation → Status: %s, error: %s\n", resp.Status, errBody["error"])
	}()

	fmt.Println("\n--- Response body encode violation — symmetric validation ---")
	// The same codec that rejects an invalid request body at 400 now also rejects
	// an invalid response body at 500. Refine constraints run on both Encode and Decode.
	func() {
		bodyViolRouter := gochi.NewRouter()
		chiadapter.Register(bodyViolRouter, createUserRoute,
			func(ctx context.Context, req CreateUserReq) (User, error) {
				// Handler deliberately returns a User with invalid field values to
				// demonstrate that handle.Encode now validates the response body.
				return User{
					ID:    "not-a-uuid",   // fails UUID constraint
					Name:  "",             // fails NonEmptyString constraint
					Email: "not-an-email", // fails Email constraint
				}, nil
			},
			chiadapter.Options{Observer: obs})
		bodyViolSrv := httptest.NewServer(bodyViolRouter)
		defer bodyViolSrv.Close()

		body := strings.NewReader(`{"name":"Dave","email":"dave@example.com"}`)
		resp, err := http.Post(bodyViolSrv.URL+"/users", "application/json", body) //nolint:noctx
		if err != nil {
			panic(err)
		}
		defer resp.Body.Close()
		var errBody map[string]string
		_ = json.NewDecoder(resp.Body).Decode(&errBody)
		fmt.Printf("Response body violation → Status: %s, error: %s\n", resp.Status, errBody["error"])
	}()

	// chiadapter.SetCookie validates the value before writing Set-Cookie — the
	// same codec used in CookieParam for the read path. One definition, two boundaries.
	func() {
		setRec := httptest.NewRecorder()
		if err := chiadapter.SetCookie(setRec, "session_token", "refreshed-session-token", chiadapter.CookieOptions{
			Codec:  &profileSessionCodec,
			MaxAge: 3600,
		}); err != nil {
			fmt.Fprintf(os.Stderr, "SetCookie error: %v\n", err)
			return
		}
		fmt.Printf("Valid:   Set-Cookie: %s\n", setRec.Header().Get("Set-Cookie"))

		setRec2 := httptest.NewRecorder()
		setErr := chiadapter.SetCookie(setRec2, "session_token", "", chiadapter.CookieOptions{
			Codec: &profileSessionCodec,
		})
		fmt.Printf("Invalid: error=%v, Set-Cookie=%q\n", setErr, setRec2.Header().Get("Set-Cookie"))
	}()

	fmt.Println("\n--- Observer summary ---")
	obs.Print()

	fmt.Println("\n--- OpenAPI 3.1 spec ---")
	doc, err := b.OpenAPISpec()
	if err != nil {
		fmt.Fprintf(os.Stderr, "OpenAPISpec error: %v\n", err)
		os.Exit(1)
	}
	yamlBytes, err := doc.MarshalYAML()
	if err != nil {
		fmt.Fprintf(os.Stderr, "MarshalYAML error: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(string(yamlBytes))
}
