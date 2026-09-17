package routes

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/validate"
)

// Every route below is an UNATTACHED rest.Route SPEC value — method, path,
// codecs, RouteMeta, params, formats, and (for secured routes) a .Use(...)
// security declaration. NONE of them call .WithHandler/.HandleMW/.ClientMW
// — that's handlers/ (server business logic + security enforcement) and
// client/ (client-side credential + general-purpose middleware) attaching
// their OWN half, separately, onto the SAME declared value. chiserver/ and
// nethttpserver/ each import these same package-level vars and assemble
// them onto a different adapter, unchanged.

var (
	locationCodec = codex.String().Refine(validate.NonEmptyString)
	sessionCodec  = codex.String().Refine(validate.MinLen(8))
)

// LoginRoute — POST /login — PUBLIC, no security declaration. Issues a
// mock bearer token for a valid username/password pair. The declared
// rest.ErrorPattern below replaces a FORMER hand-rolled errors.As
// dispatch that used to live inside chiserver/nethttpserver's shared
// adapter ErrorHandler — invalid credentials now get a typed,
// OpenAPI-documented 401 body instead of the generic {"error": "..."}
// envelope, with zero imperative dispatch code in the adapter layer. See
// demo_login.go's negative-path call, which recovers the payload
// client-side via rest.ErrorPatternAs.
var LoginRoute = rest.NewRoute[LoginReq, TokenResp]("POST", "/login",
	LoginReqCodec, TokenRespCodec,
	rest.RouteMeta{OperationID: "login", Summary: "Authenticate and receive a bearer token", Tags: []string{"auth"}},
	rest.ErrorPattern[InvalidCredentialsError, LoginErrorPayload](http.StatusUnauthorized, LoginErrorPayloadCodec,
		func(e InvalidCredentialsError) (LoginErrorPayload, error) {
			return LoginErrorPayload{Message: e.Error()}, nil
		},
	),
)

// CreateUserConflictRoute demonstrates rest.ErrorPattern end-to-end — a
// SCRATCH route (separate from CreateUserRoute, which stays untouched)
// whose handler (see demo_error_pattern.go) returns ConflictError for a
// duplicate email. The declared pattern below intercepts it and writes a
// typed 409 ConflictPayload instead of the generic error envelope; the
// SAME payload is recovered client-side via 3 DIFFERENT matching
// mechanisms — see demo_error_pattern.go: (1) rest.ErrorPatternAs[B],
// (2) rest.HandleErrorPattern+Case, (3) ConflictErrorPattern.Match
// (the declaration value's OWN method — declared once, used both
// directions).
//
// ConflictErrorPattern is exported (not inlined into CreateUserConflictRoute's
// opts) SPECIFICALLY so demo_error_pattern.go can call
// ConflictErrorPattern.Match(err) — client-matching mechanism #3.
var ConflictErrorPattern = rest.ErrorPattern[ConflictError, ConflictPayload](http.StatusConflict, ConflictPayloadCodec,
	func(e ConflictError) (ConflictPayload, error) {
		return ConflictPayload{Code: "email_conflict", Email: e.Email}, nil
	},
)

var CreateUserConflictRoute = rest.NewRoute[CreateUserReq, User]("POST", "/users-conflict-demo",
	CreateUserReqCodec, UserCodec,
	rest.RouteMeta{OperationID: "createUserConflictDemo", Summary: "Create a user (ErrorPattern Mapped-mode demo)", Tags: []string{"error-pattern"}},
	ConflictErrorPattern,
)

// RateLimitDirectRoute demonstrates rest.ErrorPattern's DIRECT mode (no
// mapFn — RateLimitError itself IS the response payload, codec-backed) —
// the 2nd of REST's 3 declaration mechanisms (Mapped mode above,
// ErrorStatus below).
var RateLimitDirectRoute = rest.NewRoute[CreateUserReq, User]("POST", "/users-ratelimit-demo",
	CreateUserReqCodec, UserCodec,
	rest.RouteMeta{OperationID: "rateLimitDirectDemo", Summary: "Create a user (ErrorPattern Direct-mode demo)", Tags: []string{"error-pattern"}},
	rest.ErrorPattern[RateLimitError, RateLimitError](http.StatusTooManyRequests, RateLimitErrorCodec),
)

// ThrottledStatusRoute demonstrates rest.ErrorStatus — the SIMPLEST of
// REST's 3 declaration mechanisms: error-type → HTTP-status ONLY, no
// typed response body (the generic error envelope still applies).
var ThrottledStatusRoute = rest.NewRoute[CreateUserReq, User]("POST", "/users-throttled-demo",
	CreateUserReqCodec, UserCodec,
	rest.RouteMeta{OperationID: "throttledStatusDemo", Summary: "Create a user (ErrorStatus demo)", Tags: []string{"error-pattern"}},
	rest.ErrorStatus[ThrottledError](http.StatusTooManyRequests),
)

// ── 3 ErrorActions (Respond/Handle/Log) — same ConflictError/
// ConflictPayload type, 3 sibling routes so each action's behavior is
// visible independently — see demo_error_pattern.go.

// ConflictRespondRoute uses the DEFAULT ErrorRespond action (identical to
// CreateUserConflictRoute) — kept as its own route purely so all 3 actions
// can be demoed side-by-side against otherwise-identical routes.
var ConflictRespondRoute = rest.NewRoute[CreateUserReq, User]("POST", "/users-action-respond-demo",
	CreateUserReqCodec, UserCodec,
	rest.RouteMeta{OperationID: "conflictActionRespondDemo", Summary: "Create a user (ErrorAction: Respond)", Tags: []string{"error-pattern"}},
	rest.ErrorPattern[ConflictError, ConflictPayload](http.StatusConflict, ConflictPayloadCodec,
		func(e ConflictError) (ConflictPayload, error) {
			return ConflictPayload{Code: "email_conflict", Email: e.Email}, nil
		},
	), // Action defaults to ErrorRespond when WithAction is not called.
)

// ConflictHandleRoute uses ErrorHandle — the typed body is NOT
// auto-written; the adapter falls through to Options.ErrorHandler
// instead (still using this pattern's declared status).
var ConflictHandleRoute = rest.NewRoute[CreateUserReq, User]("POST", "/users-action-handle-demo",
	CreateUserReqCodec, UserCodec,
	rest.RouteMeta{OperationID: "conflictActionHandleDemo", Summary: "Create a user (ErrorAction: Handle)", Tags: []string{"error-pattern"}},
	rest.ErrorPattern[ConflictError, ConflictPayload](http.StatusConflict, ConflictPayloadCodec,
		func(e ConflictError) (ConflictPayload, error) {
			return ConflictPayload{Code: "email_conflict", Email: e.Email}, nil
		},
	).WithAction(rest.ErrorHandle),
)

// ConflictLogRoute uses ErrorLog — behaves identically to ErrorHandle for
// REST (both fall through to Options.ErrorHandler, since REST has only
// one such hook) — kept distinct purely for cross-boundary vocabulary
// parity with events/reqreply's own 3-action model.
var ConflictLogRoute = rest.NewRoute[CreateUserReq, User]("POST", "/users-action-log-demo",
	CreateUserReqCodec, UserCodec,
	rest.RouteMeta{OperationID: "conflictActionLogDemo", Summary: "Create a user (ErrorAction: Log)", Tags: []string{"error-pattern"}},
	rest.ErrorPattern[ConflictError, ConflictPayload](http.StatusConflict, ConflictPayloadCodec,
		func(e ConflictError) (ConflictPayload, error) {
			return ConflictPayload{Code: "email_conflict", Email: e.Email}, nil
		},
	).WithAction(rest.ErrorLog),
)

// ── Security middleware + ErrorPattern combination ───────────────────────────

// ErrorPatternScopeMw declares a THIRD "bearerAuth" scope requirement
// ("billing") — attached via .Use(...) below, paired against a security
// Fn (see demo_error_pattern.go) that deliberately rejects every caller
// with InsufficientScopeError, proving a declared ErrorPattern intercepts
// a SECURITY-MIDDLEWARE Fn failure (not just a business-handler failure).
var ErrorPatternScopeMw = middleware.SecurityScheme("bearerAuth", route.BearerScheme("JWT"), []string{"billing"}, &BearerCodec)

// SecuredConflictRoute demonstrates ErrorPattern matching a security
// middleware Fn's returned error (InsufficientScopeError), NOT a handler
// error — see demo_error_pattern.go's securityFn, which always rejects.
var SecuredConflictRoute = rest.NewRoute[CreateUserReq, User]("POST", "/users-security-errorpattern-demo",
	CreateUserReqCodec, UserCodec,
	rest.RouteMeta{OperationID: "securedConflictErrorPatternDemo", Summary: "Create a user (security-middleware ErrorPattern demo)", Tags: []string{"error-pattern"}},
	rest.ErrorPattern[InsufficientScopeError, InsufficientScopePayload](http.StatusForbidden, InsufficientScopePayloadCodec,
		func(e InsufficientScopeError) (InsufficientScopePayload, error) {
			return InsufficientScopePayload{Code: "insufficient_scope", RequiredScope: e.RequiredScope}, nil
		},
	),
).Use(ErrorPatternScopeMw)

// IngestConflictRoute demonstrates that a declared rest.ErrorPattern is
// now ALSO consulted through the port/stream-adapter dispatch path
// (nethttp.IngestAdapter's handlerFunc) — not just the normal
// AttachRouter/AttachMux serving path (serve.go) — closing a gap found and
// fixed in this session's review round. Resp=struct{} is required by
// IngestAdapter; the declared pattern matches a request BODY decode/
// validation failure (codex.ValidationErrors), which IngestAdapter's own
// dispatch encounters before ever reaching the pipeline. See
// demo_error_pattern.go.
var IngestConflictRoute = rest.NewRoute[CreateUserReq, struct{}]("POST", "/users-ingest-demo",
	CreateUserReqCodec, codex.Struct[struct{}](),
	rest.RouteMeta{OperationID: "ingestConflictDemo", Summary: "Ingest a user creation request (ErrorPattern demo)", Tags: []string{"error-pattern"}},
	rest.ErrorPattern[codex.ValidationErrors, ValidationPayload](http.StatusUnprocessableEntity, ValidationPayloadCodec,
		func(e codex.ValidationErrors) (ValidationPayload, error) {
			return ValidationPayload{Code: "validation_failed"}, nil
		},
	),
)

// CreateUserRoute — POST /users — requires the "admin" scope. Demonstrates
// the full three-layer codec pipeline, multi-format request/response
// bodies (JSON + YAML), and a FULLY DECLARATIVE response header + cookie
// (value AND Set-Cookie attributes) — both derived straight from the
// handler's returned User, no adapter-specific ResponseDepositor escape
// hatch needed.
var CreateUserRoute = rest.NewRoute[CreateUserReq, User]("POST", "/users",
	CreateUserReqCodec, UserCodec,
	rest.RouteMeta{
		OperationID:    "createUser",
		Summary:        "Create a user",
		ReqSchemaName:  "CreateUserRequest",
		RespSchemaName: "User",
		Tags:           []string{"user"},
	},
	rest.NewRequiredResponseHeaderParam("Location", locationCodec,
		func(u User) string { return "/users/" + u.ID },
		func(u *User, v string) {}, // no User field to decode Location back into
	).WithDescription("URL of the newly created user resource"),
	rest.NewRequiredResponseCookieParam("session", sessionCodec,
		func(u User) string { return "sess-" + u.ID + "-token" },
		func(u *User, v string) {}, // no User field to decode session back into
	).WithDescription("Session token for the new user").
		WithAttributes(func(u User) rest.CookieAttributes {
			return rest.CookieAttributes{MaxAge: 3600, Insecure: true}
		}),
	rest.Formats(
		format.JSON(UserCodec),
		format.YAML(UserCodec),
	),
	rest.RequestFormats(
		format.JSON(CreateUserReqCodec),
		format.YAML(CreateUserReqCodec),
	),
).Use(AdminScopeMw)

// GetUserRoute — GET /users/{id} — requires the "profile" scope.
// NewPathParam declares BOTH the spec/validation Param AND a merge field
// — the server merges {id} into GetUserReq.ID automatically, and the
// CLIENT derives {id} FROM GetUserReq.ID automatically too (via
// RouteHandle.EncodeVars — see client/client.go).
// codex.TextCodec[uuid.UUID]() merges the path segment directly into a
// uuid.UUID field instead of a validated-but-still-string codec.
var GetUserRoute = rest.NewRoute[GetUserReq, User]("GET", "/users/{id}",
	GetUserReqCodec, UserCodec,
	rest.RouteMeta{
		OperationID:    "getUser",
		Summary:        "Get a user by ID",
		RespSchemaName: "User",
		Tags:           []string{"user"},
	},
	rest.NewPathParam("id",
		codex.TextCodec[uuid.UUID](),
		func(r GetUserReq) uuid.UUID { return r.ID },
		func(r *GetUserReq, v uuid.UUID) { r.ID = v },
	).WithDescription("User UUID"),
	rest.Formats(
		format.JSON(UserCodec),
		format.YAML(UserCodec),
	),
).Use(ProfileScopeMw)

// UpdateUserRoute — PUT /users/{id} — requires the "admin" scope. MIXES a
// path field (ID) with body fields (Name, Email) on the SAME
// UpdateUserReq struct — RouteHandle.DecodeMerged (server) and
// RouteHandle.EncodeVars+EncodeRequestWithFormats (client) both derive
// from/to the SAME struct in one call.
var UpdateUserRoute = rest.NewRoute[UpdateUserReq, User]("PUT", "/users/{id}",
	UpdateUserReqCodec, UserCodec,
	rest.RouteMeta{
		OperationID:    "updateUser",
		Summary:        "Update a user by ID",
		RespSchemaName: "User",
		Tags:           []string{"user"},
	},
	rest.NewPathParam("id",
		codex.String().Refine(validate.UUID),
		func(r UpdateUserReq) string { return r.ID },
		func(r *UpdateUserReq, v string) { r.ID = v },
	).WithDescription("User UUID"),
).Use(AdminScopeMw)

// ListUsersRoute — GET /users — requires the "profile" scope. "page" is
// codec-validated (non-negative integer string) AND merged into
// ListUsersReq.Page; "search" is a plain, unvalidated merge field.
var ListUsersRoute = rest.NewRoute[ListUsersReq, PagedUsersResp]("GET", "/users",
	ListUsersReqCodec, PagedUsersRespCodec,
	rest.RouteMeta{
		OperationID: "listUsers",
		Summary:     "List users",
		Tags:        []string{"user"},
	},
	rest.NewOptionalQueryParam("page",
		codex.IntString(),
		func(r ListUsersReq) int { return r.Page },
		func(r *ListUsersReq, v int) { r.Page = v },
	).WithDescription("Page number (0-based, non-negative integer)"),
	rest.NewOptionalQueryParam("search",
		codex.String(),
		func(r ListUsersReq) string { return r.Search },
		func(r *ListUsersReq, v string) { r.Search = v },
	).WithDescription("Filter by name prefix (no validation)"),
	rest.Formats(
		format.JSON(PagedUsersRespCodec),
	),
).Use(ProfileScopeMw)

// ProfileRoute — GET /profile — requires the "profile" scope, LAYERED
// with request-side cookie + header validation on the SAME route: a
// secured route can ALSO declare ordinary request params, no special
// casing needed. Both session_token and X-Request-Id are merge-capable —
// the client derives them from ProfileReq automatically.
var ProfileRoute = rest.NewRoute[ProfileReq, User]("GET", "/profile",
	ProfileReqCodec, UserCodec,
	rest.RouteMeta{
		OperationID: "getProfile",
		Summary:     "Get the current user profile",
		Tags:        []string{"user"},
	},
	rest.NewRequiredCookieParam("session_token",
		codex.String().Refine(validate.NonEmptyString),
		func(r ProfileReq) string { return r.SessionToken },
		func(r *ProfileReq, v string) { r.SessionToken = v },
	).WithDescription("Active session token"),
	rest.NewRequiredHeaderParam("X-Request-Id",
		codex.String().Refine(validate.UUID),
		func(r ProfileReq) string { return r.RequestID },
		func(r *ProfileReq, v string) { r.RequestID = v },
	).WithDescription("Idempotency and tracing UUID"),
	rest.Formats(
		format.JSON(UserCodec),
	),
).Use(ProfileScopeMw)

// AdminActionRoute — POST /admin/action — requires the "admin" scope.
// Pure scope-gated action, no other params.
var AdminActionRoute = rest.NewRoute[AdminActionReq, AdminActionResp]("POST", "/admin/action",
	AdminActionReqCodec, AdminActionRespCodec,
	rest.RouteMeta{
		OperationID: "adminAction",
		Summary:     "Perform a privileged admin action",
		Tags:        []string{"admin"},
	},
).Use(AdminScopeMw)
