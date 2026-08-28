// Package contract defines the shared HTTP API contract for the
// adapters-nethttp-client example.
//
// This package is the single source of truth for:
//   - Domain types (CreateUserReq, User)
//   - Codecs (CreateUserReqCodec, UserCodec)
//   - Route definitions (CreateUser, GetUser)
//
// Both the server and the client import this package. The Go compiler enforces
// the contract: any field rename, type change, or constraint modification breaks
// compilation on both sides immediately — no stale OpenAPI YAML, no schema drift,
// no code generation step.
//
// This mirrors the pattern in examples/gob-contract but uses JSON over HTTP
// instead of gob over MQTT.
package contract

import (
	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/validate"
)

// ── Domain types ──────────────────────────────────────────────────────────────

// CreateUserReq is the request body for the CreateUser route.
type CreateUserReq struct {
	Name  string
	Email string
}

// User is the domain type returned by the GetUser and CreateUser routes.
//
// RequestID is NOT part of the JSON body — UserCodec below deliberately
// does not declare it. It exists purely for the GetUserActivity route's
// response header merge field ([rest.NewRequiredResponseHeaderParam]): the
// server sets it from this field automatically, and the client merges the
// HTTP response header back into it automatically — no manual
// w.Header().Set()/resp.Header.Get() needed on either side.
type User struct {
	ID        string
	Name      string
	Email     string
	RequestID string
}

// Profile is the authenticated user's profile, returned by GetProfile and
// GetSecuredData. The route requires a session cookie and a tracing header.
type Profile struct {
	ID    string
	Name  string
	Email string
	Role  string
}

// ── Codec field contracts ──────────────────────────────────────────────────────
//
// Shared field-level codecs define constraints once. Every struct codec that
// references them inherits the constraint automatically — no duplication.

var nameCodec = codex.String().Refine(validate.NonEmptyString).WithDescription("Display name.")
var emailCodec = codex.String().Refine(validate.Email).WithDescription("Email address.")

// ── Struct codecs ─────────────────────────────────────────────────────────────

// CreateUserReqCodec is the canonical codec for CreateUserReq.
var CreateUserReqCodec = codex.Struct[CreateUserReq](
	codex.RequiredField("name", nameCodec,
		func(r CreateUserReq) string { return r.Name },
		func(r *CreateUserReq, v string) { r.Name = v },
	),
	codex.RequiredField("email", emailCodec,
		func(r CreateUserReq) string { return r.Email },
		func(r *CreateUserReq, v string) { r.Email = v },
	),
)

// UserCodec is the canonical codec for User.
var UserCodec = codex.Struct[User](
	codex.RequiredField("id", codex.String(),
		func(u User) string { return u.ID },
		func(u *User, v string) { u.ID = v },
	),
	codex.RequiredField("name", nameCodec,
		func(u User) string { return u.Name },
		func(u *User, v string) { u.Name = v },
	),
	codex.RequiredField("email", emailCodec,
		func(u User) string { return u.Email },
		func(u *User, v string) { u.Email = v },
	),
)

// ProfileCodec is the canonical codec for Profile.
var ProfileCodec = codex.Struct[Profile](
	codex.RequiredField("id", codex.String(),
		func(p Profile) string { return p.ID },
		func(p *Profile, v string) { p.ID = v },
	),
	codex.RequiredField("name", nameCodec,
		func(p Profile) string { return p.Name },
		func(p *Profile, v string) { p.Name = v },
	),
	codex.RequiredField("email", emailCodec,
		func(p Profile) string { return p.Email },
		func(p *Profile, v string) { p.Email = v },
	),
	codex.RequiredField("role", codex.String(),
		func(p Profile) string { return p.Role },
		func(p *Profile, v string) { p.Role = v },
	),
)

// ── Param codecs ──────────────────────────────────────────────────────────────
//
// Param codecs are exported so the server and client share the exact same
// constraint. One definition enforces the contract at both ends.

// SessionTokenCodec validates a session cookie value: non-empty string.
var SessionTokenCodec = codex.String().Refine(validate.NonEmptyString).
	WithDescription("Active session token.")

// RequestIDCodec validates the X-Request-Id tracing header value: UUID v4.
// Note: net/http canonicalizes header names — use "X-Request-Id" (not "X-Request-ID").
var RequestIDCodec = codex.String().Refine(validate.UUID).
	WithDescription("Idempotency and tracing UUID (RFC 4122).")

// ── Route specs ───────────────────────────────────────────────────────────────

// CreateUser is the declarative route spec for POST /users.
// Register it with a rest.Builder on the server; import it in the client to call
// the server with the same codec and parameter constraints.
//
// rest.ErrorPattern declares a typed 409 response for EmailConflictError —
// this is what closes the loop between "server writes a typed error body"
// and "client decodes it back into a typed value" (see main.go section 6).
// The default action is rest.ErrorRespond, so both nethttp.Handler (server)
// and nethttp.Call (client) participate automatically — no extra wiring.
var CreateUser = rest.NewRoute[CreateUserReq, User](
	"POST", "/users",
	CreateUserReqCodec, UserCodec,
	rest.RouteMeta{
		OperationID:    "createUser",
		Summary:        "Create a user",
		ReqSchemaName:  "CreateUserRequest",
		RespSchemaName: "User",
		RespStatus:     "201",
	},
	rest.ErrorPattern[EmailConflictError, EmailConflictError](409, EmailConflictCodec),
)

// GetUser is the declarative route spec for GET /users/{id}.
var GetUser = rest.NewRoute[struct{}, User](
	"GET", "/users/{id}",
	codex.Empty, UserCodec,
	rest.RouteMeta{
		OperationID:    "getUser",
		Summary:        "Get a user by ID",
		RespSchemaName: "User",
	},
	rest.PathParam{Name: "id", Description: "User ID"}.WithCodec(
		codex.String().Refine(validate.NonEmptyString),
	),
)

// GetUserActivityReq is the request for GetUserActivity — ID comes from the
// URL path, Filter from a query parameter. Both are merge-capable
// ([rest.NewPathParam]/[rest.NewRequiredQueryParam]), so the client can
// build BOTH the URL vars AND the query params directly from one value via
// [codex.EncodeVars] instead of hand-writing two separate maps.
type GetUserActivityReq struct {
	ID     string
	Filter string
}

// GetUserActivity is the declarative route spec for
// GET /users/{id}/activity?filter=... — demonstrates role-aware merge
// fields on the CLIENT (encode) side: [rest.RouteHandle.PathMergeFields]
// and [rest.RouteHandle.QueryMergeFields] each return only their own
// role's field, so encoding one for the URL path and one for the query
// string never leaks a value into the wrong HTTP location.
//
// It ALSO declares a response header merge field
// ([rest.NewRequiredResponseHeaderParam]) on User.RequestID: the server
// sets X-Request-Id automatically from the returned User value (no
// nethttp.WithResponseHeaders call needed), and the client reads it back
// into the SAME field automatically (no resp.Header.Get call needed) — the
// response-direction half of the "single codec, every aspect, both
// directions" story.
var GetUserActivity = rest.NewRoute[GetUserActivityReq, User](
	"GET", "/users/{id}/activity",
	codex.Struct[GetUserActivityReq](), UserCodec,
	rest.RouteMeta{
		OperationID:    "getUserActivity",
		Summary:        "Get a user's activity, filtered",
		RespSchemaName: "User",
	},
	rest.NewPathParam("id",
		codex.String().Refine(validate.NonEmptyString),
		func(r GetUserActivityReq) string { return r.ID },
		func(r *GetUserActivityReq, v string) { r.ID = v },
	).WithDescription("User ID"),
	rest.NewOptionalQueryParam("filter",
		codex.String(),
		func(r GetUserActivityReq) string { return r.Filter },
		func(r *GetUserActivityReq, v string) { r.Filter = v },
	).WithDescription("Activity filter"),
	rest.NewRequiredResponseHeaderParam("X-Request-Id",
		RequestIDCodec,
		func(u User) string { return u.RequestID },
		func(u *User, v string) { u.RequestID = v },
	).WithDescription("Server-generated tracing ID for this activity lookup"),
)

// GetProfile is the route spec for GET /profile.
// The client must supply a valid session_token cookie and an X-Request-ID header.
// Both are codec-validated before the request is sent — invalid values are
// rejected client-side without making any network call.
var GetProfile = rest.NewRoute[struct{}, Profile](
	"GET", "/profile",
	codex.Empty, ProfileCodec,
	rest.RouteMeta{
		OperationID:    "getProfile",
		Summary:        "Get the current user's profile",
		RespSchemaName: "Profile",
	},
	// CookieParam: session_token must be a non-empty string.
	// The client passes the value via CallOptions.CookieParams.
	rest.CookieParam{
		Name:        "session_token",
		Description: "Active session token",
		Required:    true,
	}.WithCodec(SessionTokenCodec),
	// HeaderParam: X-Request-Id must be a valid UUID.
	// net/http canonicalizes header names; use the canonical form "X-Request-Id".
	// The client passes the value via CallOptions.HeaderParams.
	rest.HeaderParam{
		Name:        "X-Request-Id",
		Description: "Idempotency and tracing UUID",
		Required:    true,
	}.WithCodec(RequestIDCodec),
)

// ── Typed error payload ───────────────────────────────────────────────────────

// EmailConflictError is returned by the CreateUser handler when the
// requested email is already registered. It is a domain error type — the
// handler returns it as a plain Go `error`, never touching HTTP directly.
type EmailConflictError struct {
	Email string
}

func (e EmailConflictError) Error() string {
	return "email already registered: " + e.Email
}

// EmailConflictCodec is the canonical codec for EmailConflictError — the
// SAME codec declared once via [rest.ErrorPattern] below drives BOTH the
// server's automatic typed body encode AND the client's automatic typed
// body decode (see main.go section 6).
var EmailConflictCodec = codex.Struct[EmailConflictError](
	codex.RequiredField("email", emailCodec,
		func(e EmailConflictError) string { return e.Email },
		func(e *EmailConflictError, v string) { e.Email = v },
	),
)

// BearerAuthScheme declares the "bearerAuth" security scheme's spec
// metadata and credential-format codec ONCE, for use by main.go's
// nethttp.RequireScopes-built middleware — exported so main.go (which owns
// the actual token-verification logic, an adapter/application concern this
// adapter-agnostic contract package deliberately stays free of) can build
// that middleware with the IDENTICAL scheme metadata/codec used here.
var BearerAuthScheme = route.BearerScheme("JWT")

// BearerCredentialCodec validates the raw credential format (non-empty) —
// shared by both the server's verification middleware and the client's
// credential-format pre-flight check, both via the SAME
// middleware.SecurityDeclaration this route's attached middleware carries.
var BearerCredentialCodec = codex.String().Refine(validate.NonEmptyString)

// GetSecuredData is the route spec for GET /data — a FUNCTION, not a bare
// var, because the actual security middleware (which knows HOW to verify a
// token) is an adapter/application concern; this contract package only
// supplies the reusable scheme/codec above. mw is BOTH the spec declaration
// (Security + SecuritySchemes, identical on server Register AND client
// ClientHandle) AND the runtime enforcement Fn on the server side — see
// main.go for how it's built via nethttp.RequireScopes.
func GetSecuredData(mw middleware.Middleware) rest.Route[struct{}, Profile] {
	return rest.NewRoute[struct{}, Profile](
		"GET", "/data",
		codex.Empty, ProfileCodec,
		rest.RouteMeta{
			OperationID:    "getSecuredData",
			Summary:        "Get data (bearer-authenticated)",
			RespSchemaName: "Profile",
		},
	).Use(mw)
}
