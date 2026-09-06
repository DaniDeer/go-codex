package routes

import (
	"github.com/google/uuid"

	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
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
// mock bearer token for a valid username/password pair.
var LoginRoute = rest.NewRoute[LoginReq, TokenResp]("POST", "/login",
	LoginReqCodec, TokenRespCodec,
	rest.RouteMeta{OperationID: "login", Summary: "Authenticate and receive a bearer token", Tags: []string{"auth"}},
)

// CreateUserRoute — POST /users — requires the "admin" scope. Demonstrates
// the full three-layer codec pipeline, multi-format request/response
// bodies (JSON + YAML), and codec-validated response header + cookie.
var CreateUserRoute = rest.NewRoute[CreateUserReq, User]("POST", "/users",
	CreateUserReqCodec, UserCodec,
	rest.RouteMeta{
		OperationID:    "createUser",
		Summary:        "Create a user",
		ReqSchemaName:  "CreateUserRequest",
		RespSchemaName: "User",
		Tags:           []string{"user"},
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
