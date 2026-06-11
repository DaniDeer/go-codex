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
	"github.com/DaniDeer/go-codex/validate"
)

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

// CreateUserReqCodec is the canonical codec for CreateUserReq.
var CreateUserReqCodec = codex.Struct[CreateUserReq](
	codex.Field[CreateUserReq, string]{
		Name:     "name",
		Codec:    codex.String().Refine(validate.NonEmptyString),
		Get:      func(r CreateUserReq) string { return r.Name },
		Set:      func(r *CreateUserReq, v string) { r.Name = v },
		Required: true,
	},
	codex.Field[CreateUserReq, string]{
		Name:     "email",
		Codec:    codex.String().Refine(validate.Email),
		Get:      func(r CreateUserReq) string { return r.Email },
		Set:      func(r *CreateUserReq, v string) { r.Email = v },
		Required: true,
	},
)

// GetUserReq is the (empty) request type for the GetUser route.
// Path and query parameters are passed separately via CallOptions.PathVars / QueryParams.
type GetUserReq struct{}

// GetUserReqCodec is the canonical codec for GetUserReq.
var GetUserReqCodec = codex.Struct[GetUserReq]()

// UserCodec is the canonical codec for User.
var UserCodec = codex.Struct[User](
	codex.Field[User, string]{
		Name:  "id",
		Codec: codex.String(),
		Get:   func(u User) string { return u.ID },
		Set:   func(u *User, v string) { u.ID = v },
	},
	codex.Field[User, string]{
		Name:  "name",
		Codec: codex.String(),
		Get:   func(u User) string { return u.Name },
		Set:   func(u *User, v string) { u.Name = v },
	},
	codex.Field[User, string]{
		Name:  "email",
		Codec: codex.String(),
		Get:   func(u User) string { return u.Email },
		Set:   func(u *User, v string) { u.Email = v },
	},
)

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
var GetUser = rest.NewRoute[GetUserReq, User](
	"GET", "/users/{id}",
	GetUserReqCodec, UserCodec,
	rest.RouteMeta{
		OperationID:    "getUser",
		Summary:        "Get a user by ID",
		RespSchemaName: "User",
	},
	rest.PathParam{Name: "id", Description: "User ID"}.WithCodec(
		codex.String().Refine(validate.NonEmptyString),
	),
)
