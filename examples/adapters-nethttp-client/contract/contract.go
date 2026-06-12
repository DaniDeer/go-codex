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
type User struct {
	ID    string
	Name  string
	Email string
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

// GetSecuredData is the route spec for GET /data.
// Requires bearer authentication — the client must supply a token via
// CallOptions.CredentialFunc, which injects the Authorization header.
var GetSecuredData = rest.NewRoute[struct{}, Profile](
	"GET", "/data",
	codex.Empty, ProfileCodec,
	rest.RouteMeta{
		OperationID:    "getSecuredData",
		Summary:        "Get data (bearer-authenticated)",
		RespSchemaName: "Profile",
		// Per-route security overrides builder-level global security.
		Security: []route.SecurityRequirement{
			route.Require("bearerAuth"),
		},
	},
)
