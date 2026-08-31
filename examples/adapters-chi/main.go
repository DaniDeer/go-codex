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
	"github.com/google/uuid"

	chiadapter "github.com/DaniDeer/go-codex/adapters/chi"
	"github.com/DaniDeer/go-codex/adapters/nethttp"
	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/stats"
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
type UserRecord struct {
	ID    string
	Name  string
	Email string
}

// userRecordCodec describes the SQL model. The store uses this codec to
// encode records for persistence and decode rows on retrieval.
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

// GetUserReq carries the {id} path variable — merged in automatically by
// rest.NewPathParam + chi's Serve's RouteHandle.DecodeMerged wiring (no
// body: this route is a GET with no request payload). ID is a REAL
// uuid.UUID, not a string — codex.TextCodec[uuid.UUID]() parses/formats
// it directly at the path-var boundary, so the handler never calls
// uuid.Parse itself.
type GetUserReq struct {
	ID uuid.UUID
}

// getUserReqCodec has no declared fields — GetUserReq.ID is populated
// exclusively via the path-var merge, never from a JSON body.
var getUserReqCodec = codex.Struct[GetUserReq]()

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
// GetUserReq.ID arrives ALREADY merged and codec-validated by chi's Serve
// (via rest.NewPathParam + RouteHandle.DecodeMerged) — chi's Serve calls
// DecodeMerged internally exactly like nethttp.Serve does, so no manual
// chi.URLParam extraction is needed here.
func makeGetUserHandler(store *UserStore) func(context.Context, GetUserReq) (User, error) {
	return func(_ context.Context, req GetUserReq) (User, error) {
		record, ok := store.Get(req.ID.String())
		if !ok {
			return User{}, fmt.Errorf("user %q not found", req.ID)
		}
		return buildUserResponse(record), nil
	}
}

// UpdateUserReq MIXES two sources on ONE struct: ID comes from the path
// (merged automatically, like GetUserReq.ID above), Name/Email come from
// the JSON body (decoded by updateUserReqCodec). RouteHandle.DecodeMerged
// does body-decode THEN var-merge on the SAME value — a route can freely
// combine both without any special handling, as long as the body codec and
// the merge fields declare different field names.
type UpdateUserReq struct {
	ID    string // from path — merged, not declared in updateUserReqCodec
	Name  string // from JSON body
	Email string // from JSON body
}

// updateUserReqCodec deliberately does NOT declare "id" — ID is populated
// exclusively via the path-var merge (see rest.NewPathParam in main()).
var updateUserReqCodec = codex.Struct[UpdateUserReq](
	codex.RequiredField("name", nameFieldCodec,
		func(r UpdateUserReq) string { return r.Name },
		func(r *UpdateUserReq, v string) { r.Name = v },
	),
	codex.RequiredField("email", emailFieldCodec,
		func(r UpdateUserReq) string { return r.Email },
		func(r *UpdateUserReq, v string) { r.Email = v },
	),
)

// makeUpdateUserHandler orchestrates the update-user pipeline:
//
// req.ID (path, merged) + req.Name/req.Email (body, decoded) → Save (store IO) → buildUserResponse
func makeUpdateUserHandler(store *UserStore) func(context.Context, UpdateUserReq) (User, error) {
	return func(_ context.Context, req UpdateUserReq) (User, error) {
		record := UserRecord(req)
		if err := store.Save(record); err != nil {
			return User{}, err
		}
		return buildUserResponse(record), nil
	}
}

// makeListUsersHandler handles GET /users with query params.
// The chi adapter validates ?page before this handler is called.
func makeListUsersHandler() func(context.Context, struct{}) (PagedUsersResp, error) {
	return func(ctx context.Context, _ struct{}) (PagedUsersResp, error) {
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

// mustServe exits the program if Route.Register/RegisterHandle or
// chiadapter.Serve returns an error — e.g. a malformed middleware Fn
// shape, caught eagerly at wiring time rather than on the first incoming
// request.
func mustServe(err error, what string) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s failed: %v\n", what, err)
		os.Exit(1)
	}
}

func main() {
	baseLogger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	slog.SetDefault(baseLogger)

	store := newUserStore()

	domainLogger := baseLogger.With("layer", "domain")
	httpLogger := baseLogger.With("transport", "http")

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

	// createUserRouteSpec is the DECLARATIVE half only (method, path,
	// codecs, RouteMeta, response header/cookie params, request/response
	// formats) — no handler attached yet. Kept as a separate value so the
	// two "contract violation" demos further below can derive their OWN
	// Route (different handler, via ServeOne) from the SAME spec, without
	// re-registering the real /users route a second time onto b.
	createUserRouteSpec := rest.NewRoute[CreateUserReq, User]("POST", "/users",
		createUserReqCodec, userCodec,
		rest.RouteMeta{
			OperationID:    "createUser",
			Summary:        "Create a user",
			ReqSchemaName:  "CreateUserRequest",
			RespSchemaName: "User",
		},
		rest.ResponseHeaderParam{
			Name:        "Location",
			Description: "URL of the newly created user resource",
			Required:    true,
			Codec:       &locationCodec,
		},
		rest.ResponseCookieParam{
			Name:        "session",
			Description: "Session token for the new user",
			Required:    true,
			Codec:       &sessionCodec,
		},
		rest.Formats(
			format.JSON(userCodec),
			format.YAML(userCodec),
		),
		// RequestFormats enables multi-format request body decoding.
		// The adapter negotiates by Content-Type; unsupported types → 415.
		rest.RequestFormats(
			format.JSON(createUserReqCodec),
			format.YAML(createUserReqCodec),
		),
	)

	// NewPathParam declares BOTH the spec/validation Param AND a merge
	// field — chi's Serve merges {id} into GetUserReq.ID automatically
	// via RouteHandle.DecodeMerged. codex.TextCodec[uuid.UUID]() merges
	// the path segment directly into a uuid.UUID field instead of a
	// validated-but-still-string codex.String().Refine(validate.UUID).
	getUserRouteSpec := rest.NewRoute[GetUserReq, User]("GET", "/users/{id}",
		getUserReqCodec, userCodec,
		rest.RouteMeta{
			OperationID:    "getUser",
			Summary:        "Get a user by ID",
			RespSchemaName: "User",
		},
		rest.NewPathParam("id",
			codex.TextCodec[uuid.UUID](),
			func(r GetUserReq) uuid.UUID { return r.ID },
			func(r *GetUserReq, v uuid.UUID) { r.ID = v },
		).WithDescription("User UUID"),
		rest.Formats(
			format.JSON(userCodec),
			format.YAML(userCodec),
		),
	)

	// PUT /users/{id} — MIXES a path field (ID) with body fields (Name,
	// Email) on the SAME UpdateUserReq struct. DecodeMerged decodes the
	// JSON body first, then merges {id} in.
	updateUserRouteSpec := rest.NewRoute[UpdateUserReq, User]("PUT", "/users/{id}",
		updateUserReqCodec, userCodec,
		rest.RouteMeta{
			OperationID:    "updateUser",
			Summary:        "Update a user by ID",
			RespSchemaName: "User",
		},
		rest.NewPathParam("id",
			codex.String().Refine(validate.UUID),
			func(r UpdateUserReq) string { return r.ID },
			func(r *UpdateUserReq, v string) { r.ID = v },
		).WithDescription("User UUID"),
	)

	qPageCodec := codex.String().Refine(validate.NonNegativeIntString)
	listUsersRouteSpec := rest.NewRoute[struct{}, PagedUsersResp]("GET", "/users",
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
		rest.Formats(
			format.JSON(pagedUsersRespCodec),
		),
	)

	profileSessionCodec := codex.String().Refine(validate.NonEmptyString)
	profileRequestIDCodec := codex.String().Refine(validate.UUID)
	profileRouteSpec := rest.NewRoute[struct{}, User]("GET", "/profile",
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
		rest.Formats(
			format.JSON(userCodec),
		),
	)

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
	extractGetUserAttrs := func(_ GetUserReq, u User) []slog.Attr {
		return []slog.Attr{slog.String("id", u.ID)}
	}

	metrics := &CountingObserver{}
	obs := stats.NewFanout(metrics, stats.NewLoggingObserver(baseLogger.With("component", "http")))
	// chi reuses nethttp.Observability directly — same
	// general-purpose func(http.Handler) http.Handler shape chi's
	// Serve already recognizes via Route.HandleMW(nil, obsFn).
	obsFn := nethttp.Observability(obs)
	opts := chiadapter.Options{ErrorHandler: errorHandler}

	createUserRoute := createUserRouteSpec.WithHandler(
		withDomainLogging("user.create", makeCreateUserHandler(store), domainLogger, extractUserAttrs),
	).HandleMW(nil, obsFn).WithOptions(opts)
	mustServe(createUserRoute.Register(b), "register POST /users")

	getUserRoute := getUserRouteSpec.WithHandler(
		withDomainLogging("user.get", makeGetUserHandler(store), domainLogger, extractGetUserAttrs),
	).HandleMW(nil, obsFn).WithOptions(opts)
	getUserHandle, err := getUserRoute.RegisterHandle(b)
	mustServe(err, "register GET /users/{id}")

	updateUserRoute := updateUserRouteSpec.WithHandler(
		withDomainLogging("user.update", makeUpdateUserHandler(store), domainLogger,
			func(req UpdateUserReq, u User) []slog.Attr {
				return []slog.Attr{slog.String("id", req.ID), slog.String("name", u.Name)}
			}),
	).HandleMW(nil, obsFn).WithOptions(opts)
	updateUserHandle, err := updateUserRoute.RegisterHandle(b)
	mustServe(err, "register PUT /users/{id}")

	listUsersRoute := listUsersRouteSpec.WithHandler(
		withDomainLogging("user.list", makeListUsersHandler(), domainLogger,
			func(_ struct{}, _ PagedUsersResp) []slog.Attr { return nil }),
	).HandleMW(nil, obsFn).WithOptions(opts)
	mustServe(listUsersRoute.Register(b), "register GET /users")

	profileRoute := profileRouteSpec.WithHandler(
		func(_ context.Context, _ struct{}) (User, error) {
			return User{ID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Name: "Alice", Email: "alice@example.com"}, nil
		},
	).HandleMW(nil, obsFn).WithOptions(opts)
	mustServe(profileRoute.Register(b), "register GET /profile")

	r := gochi.NewRouter()
	mustServe(chiadapter.Serve(r, b), "Serve")

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

	fmt.Println("\n--- POST /users (YAML request body — multi-format) ---")
	func() {
		// WithRequestFormats enables decoding request bodies by Content-Type.
		// Sending application/yaml routes to the YAML decoder; codec validation still runs.
		body := strings.NewReader("name: Carol\nemail: carol@example.com\n")
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/users", body) //nolint:noctx
		req.Header.Set("Content-Type", "application/yaml")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			panic(err)
		}
		defer resp.Body.Close()
		var created User
		_ = json.NewDecoder(resp.Body).Decode(&created)
		fmt.Printf("Status: %s\nUser:   %+v\n", resp.Status, created)
	}()

	fmt.Println("\n--- POST /users (unsupported Content-Type → 415) ---")
	func() {
		body := strings.NewReader(`<name>Dave</name>`)
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/users", body) //nolint:noctx
		req.Header.Set("Content-Type", "application/xml")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			panic(err)
		}
		defer resp.Body.Close()
		var errBody map[string]string
		_ = json.NewDecoder(resp.Body).Decode(&errBody)
		fmt.Printf("Status: %s\nError:  %s\n", resp.Status, errBody["error"])
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
		userPath, err := getUserHandle.BuildPath(map[string]string{"id": "f47ac10b-58cc-4372-a567-0e02b2c3d479"})
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

		if _, err := getUserHandle.BuildPath(map[string]string{"id": "not-a-uuid"}); err != nil {
			fmt.Printf("BuildPath(not-a-uuid) rejected: %v\n", err)
		}
	}()

	fmt.Println("\n--- PUT /users/{id} — mixed body + path merge on ONE struct ---")
	func() {
		// UpdateUserReq.ID comes from the path (merged), Name/Email come
		// from the JSON body (decoded) — DecodeMerged populates BOTH on
		// the same struct in one call.
		updatePath, err := updateUserHandle.BuildPath(map[string]string{"id": "f47ac10b-58cc-4372-a567-0e02b2c3d479"})
		if err != nil {
			fmt.Fprintf(os.Stderr, "BuildPath error: %v\n", err)
			return
		}
		body := strings.NewReader(`{"name":"Alice Updated","email":"alice.updated@example.com"}`)
		req, err := http.NewRequest(http.MethodPut, srv.URL+updatePath, body)
		if err != nil {
			panic(err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			panic(err)
		}
		defer resp.Body.Close()
		var updated User
		_ = json.NewDecoder(resp.Body).Decode(&updated)
		fmt.Printf("Status: %s\nUser:   %+v\n", resp.Status, updated)
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
		// Derives a FRESH Route from createUserRouteSpec (the declarative
		// half only — no handler yet) with its own violation-demonstrating
		// handler, and wires it via ServeOne — sugar for "scratch
		// single-route Builder + Register + Serve" — instead of
		// re-registering the real /users route a second time onto b.
		violationRoute := createUserRouteSpec.WithHandler(
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
		).HandleMW(nil, obsFn)
		violationHandler, err := chiadapter.ServeOne(violationRoute)
		mustServe(err, "ServeOne violation route")
		violationSrv := httptest.NewServer(violationHandler)
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
		bodyViolRoute := createUserRouteSpec.WithHandler(
			func(ctx context.Context, req CreateUserReq) (User, error) {
				// Handler deliberately returns a User with invalid field values to
				// demonstrate that handle.Encode now validates the response body.
				return User{
					ID:    "not-a-uuid",   // fails UUID constraint
					Name:  "",             // fails NonEmptyString constraint
					Email: "not-an-email", // fails Email constraint
				}, nil
			},
		).HandleMW(nil, obsFn)
		bodyViolHandler, err := chiadapter.ServeOne(bodyViolRoute)
		mustServe(err, "ServeOne body-violation route")
		bodyViolSrv := httptest.NewServer(bodyViolHandler)
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
		setErr := chiadapter.SetCookie(setRec2, "session_token", "", chiadapter.CookieOptions{}.WithCodec(profileSessionCodec))
		fmt.Printf("Invalid: error=%v, Set-Cookie=%q\n", setErr, setRec2.Header().Get("Set-Cookie"))
	}()

	fmt.Println("\n--- Observer summary ---")
	metrics.Print()

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
