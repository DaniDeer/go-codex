// Package api-rest demonstrates the api/rest builder: define routes with
// codec-backed types, get typed Decode/Encode helpers, and generate a full
// OpenAPI 3.1 spec — all without importing net/http or any HTTP framework.
//
// The same RouteHandle.Decode and RouteHandle.Encode helpers work unchanged
// with net/http, Gin, Chi, Echo, or any other HTTP library.
//
// Run with: go run ./examples/api-rest
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
	b := rest.NewBuilder(rest.Info{
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
	b.AddServer("production", rest.Server{URL: "https://api.example.com/v1", Description: "Production"})
	b.AddServer("local", rest.Server{URL: "http://localhost:8080/v1", Description: "Local development"})

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
	).Register(b)
	if err != nil {
		fmt.Fprintf(os.Stderr, "route registration failed: %v\n", err)
		os.Exit(1)
	}

	// Path parameter is extracted at the HTTP layer (e.g. r.PathValue("id")).
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
	).Register(b)
	if err != nil {
		fmt.Fprintf(os.Stderr, "route registration failed: %v\n", err)
		os.Exit(1)
	}

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
