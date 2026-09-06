// Package rest-builder demonstrates the api/rest builder: define routes with
// codec-backed types, get typed Decode/Encode helpers, and generate a full
// OpenAPI 3.1 spec — all without importing net/http or any HTTP framework.
//
// The same RouteHandle.Decode and RouteHandle.Encode helpers work unchanged
// with net/http, Gin, Chi, Echo, or any other HTTP library. See also
// examples/rest-api (full adapter-based project via chi/net/http),
// examples/rest-schema-docs (schema-only, no routes), and
// examples/rest-nested-binary (nested-struct merge + non-JSON body format).
//
// Run with: go run ./examples/rest-builder
package main

import (
	"fmt"
	"os"

	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/validate"
)

// --- Domain types ---

type CreateUserRequest struct {
	Name  string
	Email string
}

type User struct {
	ID    string
	Name  string
	Email string
}

// --- Codecs: single source of truth for encode, decode, validation, schema ---

var createUserCodec = codex.Struct[CreateUserRequest](
	codex.RequiredField("name",
		codex.String().Refine(validate.NonEmptyString).Refine(validate.MaxLen(100)).WithDescription("Full display name."),
		func(r CreateUserRequest) string { return r.Name },
		func(r *CreateUserRequest, v string) { r.Name = v },
	),
	codex.RequiredField("email",
		codex.String().Refine(validate.Email).WithDescription("Primary email address."),
		func(r CreateUserRequest) string { return r.Email },
		func(r *CreateUserRequest, v string) { r.Email = v },
	),
)

var userCodec = codex.Struct[User](
	codex.RequiredField("id",
		codex.String().Refine(validate.UUID).WithDescription("Unique user ID (UUID)."),
		func(u User) string { return u.ID },
		func(u *User, v string) { u.ID = v },
	),
	codex.RequiredField("name",
		codex.String().Refine(validate.NonEmptyString).Refine(validate.MaxLen(100)).WithDescription("Full display name."),
		func(u User) string { return u.Name },
		func(u *User, v string) { u.Name = v },
	),
	codex.RequiredField("email",
		codex.String().Refine(validate.Email).WithDescription("Primary email address."),
		func(u User) string { return u.Email },
		func(u *User, v string) { u.Email = v },
	),
)

var _ = struct{}{} // placeholder removed — use codex.Empty

func main() {
	// Build the API: register routes with codecs.
	// No net/http import required.
	b := rest.NewServer(rest.Info{
		Title:       "User API",
		Version:     "1.0.0",
		Description: "CRUD API for managing users.",
	},
		// WithPathConstraints is optional. When set, AddRoute returns an
		// InvalidPathError immediately if the path violates the constraint.
		// HTTPPath requires a leading '/' and forbids unencoded spaces and null
		// bytes. OpenAPI-style path parameters like {id} are allowed.
		rest.WithPathConstraints(validate.HTTPPath),
	)
	b.AddServer("production", rest.ServerEntry{URL: "https://api.example.com/v1", Description: "Production"})
	b.AddServer("local", rest.ServerEntry{URL: "http://localhost:8080/v1", Description: "Local development"})

	// POST /users — creates a user.
	// createUser.Decode(body) and createUser.Encode(user) are the codec helpers.
	createUser, err := rest.NewRoute[CreateUserRequest, User]("POST", "/users",
		createUserCodec, userCodec,
		rest.RouteMeta{
			OperationID:     "createUser",
			Summary:         "Create a user",
			Tags:            []string{"users"},
			ReqSchemaName:   "CreateUserRequest",
			RespSchemaName:  "User",
			RespDescription: "User created.",
		},
		rest.ResponseMeta{Status: "400", Description: "Validation error."},
	).RegisterHandle(b)
	if err != nil {
		fmt.Fprintf(os.Stderr, "route registration failed: %v\n", err)
		os.Exit(1)
	}

	// This route uses a body-less struct{} Req (spec/validation demo only,
	// no live handler here) with plain rest.PathParam (validate-only). For
	// a typed Req that wants the path value merged in automatically (no
	// manual r.PathValue("id") extraction), use rest.NewPathParam instead
	// — see examples/rest-api's handlers.MakeGetUserHandler and
	// docs/features/rest-api.md's "Path/query/header params with
	// automatic merge" section for the full pattern.
	getUser, err := rest.NewRoute[struct{}, User]("GET", "/users/{id}",
		codex.Empty, userCodec,
		rest.RouteMeta{
			OperationID:     "getUser",
			Summary:         "Get a user by ID",
			Tags:            []string{"users"},
			RespSchemaName:  "User",
			RespDescription: "User found.",
		},
		rest.PathParam{Name: "id", Description: "User ID (UUID)."},
		rest.ResponseMeta{Status: "404", Description: "User not found."},
	).RegisterHandle(b)
	if err != nil {
		fmt.Fprintf(os.Stderr, "route registration failed: %v\n", err)
		os.Exit(1)
	}

	// --- rest.Path: reusing a template+params shape (opt-in, NOT the default) ---
	//
	// The plain-string form above (rest.NewRoute[Req,Resp]("GET", "/users/{id}", ...))
	// remains the default and primary way to declare a route — nothing about
	// it changes. rest.Path is a SECOND, additional, opt-in constructor —
	// reach for it only when the SAME path template + PathParam declaration
	// would otherwise be copy-pasted across two or more routes (here: GET and
	// DELETE on the same resource path), giving that shape exactly one source
	// of truth.
	userByIDPath := rest.NewPath("/users/{id}",
		rest.PathParam{Name: "id", Description: "User ID (UUID)."},
	)
	deleteUser, err := rest.NewRouteFromPath[struct{}, struct{}]("DELETE", userByIDPath,
		codex.Empty, codex.Empty,
		rest.RouteMeta{OperationID: "deleteUser", Summary: "Delete a user", Tags: []string{"users"}},
		rest.ResponseMeta{Status: "204", Description: "User deleted."},
		rest.ResponseMeta{Status: "404", Description: "User not found."},
	).RegisterHandle(b)
	if err != nil {
		fmt.Fprintf(os.Stderr, "route registration failed: %v\n", err)
		os.Exit(1)
	}
	// Standalone use — no request/response codec involved at all:
	standalonePath, err := userByIDPath.BuildPath(map[string]string{"id": "f47ac10b-58cc-4372-a567-0e02b2c3d479"})
	if err != nil {
		fmt.Fprintf(os.Stderr, "BuildPath error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("=== rest.Path: shared template+params shape ===")
	fmt.Printf("deleteUser descriptor: %s %s\n", deleteUser.Descriptor.Method, deleteUser.Descriptor.Path)
	fmt.Printf("standalone BuildPath (no request/response codec): %s\n", standalonePath)
	fmt.Println()

	// --- Demonstrate codec-backed Decode/Encode ---
	// These helpers work with any HTTP library; pass them to your handler.

	fmt.Println("=== Decode + Encode demo (transport-agnostic) ===")
	fmt.Println()

	// Valid request body → decoded and validated.
	body := []byte(`{"name":"Alice","email":"alice@example.com"}`)
	req, err := createUser.Decode(body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Decode error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Decoded request:  %+v\n", req)

	// Invalid request body → validation error from codec.
	_, err = createUser.Decode([]byte(`{"name":"","email":"not-an-email"}`))
	fmt.Printf("Validation error: %v\n", err)
	fmt.Println()

	// Encode a response (same userCodec for both POST and GET routes).
	user := User{ID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Name: req.Name, Email: req.Email}
	respBytes, _ := getUser.Encode(user)
	fmt.Printf("Encoded response: %s\n", respBytes)
	fmt.Println()

	// Route descriptors for routing in your HTTP library.
	fmt.Printf("createUser descriptor: %s %s\n", createUser.Descriptor.Method, createUser.Descriptor.Path)
	fmt.Printf("getUser    descriptor: %s %s\n", getUser.Descriptor.Method, getUser.Descriptor.Path)
	fmt.Println()

	// --- Generate OpenAPI 3.1 spec from the same builder ---
	fmt.Println("=== OpenAPI 3.1 spec ===")
	fmt.Println()

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
