// Package routes declares every domain model, codec, middleware, and
// rest.Route SPEC for this example — the "declare" phase. Nothing in this
// package attaches a handler, a server-side security implementation, or a
// client-side credential — those live in handlers/ (server-side business
// logic + security enforcement) and client/ (client-side credential
// fulfillment + call variants). chiserver/ and nethttpserver/ each import
// routes+handlers and assemble the SAME declarations onto a different
// adapter — proving the declarations themselves are adapter-agnostic.
package routes

import (
	"github.com/google/uuid"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/validate"
)

// ── Shared field codecs ──────────────────────────────────────────────────────
//
// Declaring a constraint once and reusing it across every boundary that
// needs it (HTTP request, database record, HTTP response) is the whole
// point of Codec[T] as a single source of truth.

var nameFieldCodec = codex.String().
	Refine(validate.NonEmptyString).
	WithDescription("Display name.")

var emailFieldCodec = codex.String().
	Refine(validate.Email).
	WithDescription("Email address.")

// ── HTTP request boundary: user creation/update ─────────────────────────────

// CreateUserReq is what an HTTP client sends to create a user.
type CreateUserReq struct {
	Name  string
	Email string
}

// CreateUserReqCodec enforces the domain invariants on the incoming request.
var CreateUserReqCodec = codex.Struct[CreateUserReq](
	codex.RequiredField("name", nameFieldCodec,
		func(r CreateUserReq) string { return r.Name },
		func(r *CreateUserReq, v string) { r.Name = v },
	),
	codex.RequiredField("email", emailFieldCodec,
		func(r CreateUserReq) string { return r.Email },
		func(r *CreateUserReq, v string) { r.Email = v },
	),
)

// ── Database boundary (mock SQL model) ──────────────────────────────────────

// UserRecord is the domain entity mapped to a database row.
type UserRecord struct {
	ID    string
	Name  string
	Email string
}

// UserRecordCodec describes the SQL model — the store uses this codec to
// encode records for persistence and decode rows on retrieval.
var UserRecordCodec = codex.Struct[UserRecord](
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

// ── HTTP response boundary: user ─────────────────────────────────────────────

// User is the domain entity returned to the HTTP client.
type User struct {
	ID    string
	Name  string
	Email string
}

// UserCodec describes what the HTTP client receives.
var UserCodec = codex.Struct[User](
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
// rest.NewPathParam's merge field (see routes.go). ID is a REAL uuid.UUID,
// not a string: codex.TextCodec[uuid.UUID]() parses/formats it directly
// at the path-var boundary, so no handler ever calls uuid.Parse itself.
type GetUserReq struct {
	ID uuid.UUID
}

// GetUserReqCodec has no declared fields — GetUserReq.ID is populated
// exclusively via the path-var merge, never from a JSON body.
var GetUserReqCodec = codex.Struct[GetUserReq]()

// UpdateUserReq MIXES two sources on ONE struct: ID comes from the path
// (merged automatically), Name/Email come from the JSON body (decoded via
// UpdateUserReqCodec). RouteHandle.DecodeMerged does body-decode THEN
// var-merge on the SAME value — a route can freely combine both without
// any special handling, as long as the body codec and the merge fields
// declare different field names.
type UpdateUserReq struct {
	ID    string // from path — merged, not declared in UpdateUserReqCodec
	Name  string // from JSON body
	Email string // from JSON body
}

// UpdateUserReqCodec deliberately does NOT declare "id" — ID is populated
// exclusively via the path-var merge (see rest.NewPathParam in routes.go).
var UpdateUserReqCodec = codex.Struct[UpdateUserReq](
	codex.RequiredField("name", nameFieldCodec,
		func(r UpdateUserReq) string { return r.Name },
		func(r *UpdateUserReq, v string) { r.Name = v },
	),
	codex.RequiredField("email", emailFieldCodec,
		func(r UpdateUserReq) string { return r.Email },
		func(r *UpdateUserReq, v string) { r.Email = v },
	),
)

// PagedUsersResp is the response for a paginated user list.
type PagedUsersResp struct {
	Page   int
	Search string
	Users  []User
}

// PagedUsersRespCodec describes the paginated response.
var PagedUsersRespCodec = codex.Struct[PagedUsersResp](
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

// ListUsersReq carries the query params — merged in automatically by
// rest.NewOptionalQueryParam's merge fields (see routes.go).
type ListUsersReq struct {
	Page   int
	Search string
}

// ListUsersReqCodec has no declared fields — ListUsersReq is populated
// exclusively via query-var merge, never from a JSON body.
var ListUsersReqCodec = codex.Struct[ListUsersReq]()

// ProfileReq carries the request-side cookie + header vars — merged in
// automatically by rest.NewRequiredCookieParam/NewRequiredHeaderParam's
// merge fields (see routes.go).
type ProfileReq struct {
	SessionToken string
	RequestID    string
}

// ProfileReqCodec has no declared fields — ProfileReq is populated
// exclusively via cookie/header-var merge.
var ProfileReqCodec = codex.Struct[ProfileReq]()

// ── Auth boundary: login ─────────────────────────────────────────────────────

// LoginReq is what an HTTP client sends to authenticate.
type LoginReq struct {
	Username string
	Password string
}

// LoginReqCodec enforces non-empty username/password.
var LoginReqCodec = codex.Struct[LoginReq](
	codex.RequiredField("username",
		codex.String().Refine(validate.NonEmptyString).WithDescription("Username."),
		func(r LoginReq) string { return r.Username },
		func(r *LoginReq, v string) { r.Username = v },
	),
	codex.RequiredField("password",
		codex.String().Refine(validate.NonEmptyString).WithDescription("Password."),
		func(r LoginReq) string { return r.Password },
		func(r *LoginReq, v string) { r.Password = v },
	),
)

// TokenResp carries the issued bearer token.
type TokenResp struct {
	Token string
}

// TokenRespCodec describes the login response.
var TokenRespCodec = codex.Struct[TokenResp](
	codex.RequiredField("token",
		codex.String().WithDescription("****** for subsequent requests."),
		func(r TokenResp) string { return r.Token },
		func(r *TokenResp, v string) { r.Token = v },
	),
)

// ── Auth boundary: admin action ──────────────────────────────────────────────

// AdminActionReq is a privileged admin request.
type AdminActionReq struct {
	Action string
}

// AdminActionReqCodec enforces a non-empty action name.
var AdminActionReqCodec = codex.Struct[AdminActionReq](
	codex.RequiredField("action",
		codex.String().Refine(validate.NonEmptyString).WithDescription("Admin action name."),
		func(r AdminActionReq) string { return r.Action },
		func(r *AdminActionReq, v string) { r.Action = v },
	),
)

// AdminActionResp is the admin action's outcome.
type AdminActionResp struct {
	Result string
}

// AdminActionRespCodec describes the admin action response.
var AdminActionRespCodec = codex.Struct[AdminActionResp](
	codex.OptionalField("result",
		codex.String().WithDescription("Outcome of the admin action."),
		func(r AdminActionResp) string { return r.Result },
		func(r *AdminActionResp, v string) { r.Result = v },
	),
)
