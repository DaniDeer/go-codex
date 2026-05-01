// Package adapters-nethttp demonstrates wiring the api/rest builder to a
// standard net/http server using the adapters/nethttp adapter.
//
// 1. Define codecs and build routes with api/rest (transport-agnostic).
// 2. Wire each RouteHandle to net/http with adapters/nethttp.Register.
// 3. Generate the OpenAPI 3.1 spec from the same builder.
//
// Run with: go run ./examples/adapters-nethttp
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"

	nethttp "github.com/DaniDeer/go-codex/adapters/nethttp"
	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/validate"
)

// --- Domain types ---

type CreateUserReq struct {
	Name  string
	Email string
}

type User struct {
	ID    string
	Name  string
	Email string
}

// emptyReq is used for routes that carry no request body (e.g. GET).
type emptyReq struct{}

// --- Codecs ---

var createUserReqCodec = codex.Struct[CreateUserReq](
	codex.RequiredField[CreateUserReq, string]("name",
		codex.String().Refine(validate.NonEmptyString).WithDescription("Display name."),
		func(r CreateUserReq) string { return r.Name },
		func(r *CreateUserReq, v string) { r.Name = v },
	),
	codex.RequiredField[CreateUserReq, string]("email",
		codex.String().Refine(validate.Email).WithDescription("Primary email."),
		func(r CreateUserReq) string { return r.Email },
		func(r *CreateUserReq, v string) { r.Email = v },
	),
)

var userCodec = codex.Struct[User](
	codex.OptionalField[User, string]("id",
		codex.String().WithDescription("User UUID."),
		func(u User) string { return u.ID },
		func(u *User, v string) { u.ID = v },
	),
	codex.OptionalField[User, string]("name",
		codex.String(),
		func(u User) string { return u.Name },
		func(u *User, v string) { u.Name = v },
	),
	codex.OptionalField[User, string]("email",
		codex.String(),
		func(u User) string { return u.Email },
		func(u *User, v string) { u.Email = v },
	),
)

var emptyReqCodec = codex.Struct[emptyReq]()

func main() {
	// Step 1: build the REST API (transport-agnostic).
	b := rest.NewBuilder(rest.Info{
		Title:       "User API",
		Version:     "1.0.0",
		Description: "Example REST API wired to net/http via adapters/nethttp.",
	})
	b.AddServer("local", rest.Server{URL: "http://localhost:8080"})

	createUser := rest.AddRoute[CreateUserReq, User](b, "POST", "/users",
		createUserReqCodec, userCodec, rest.RouteConfig{
			OperationID:    "createUser",
			Summary:        "Create a user",
			ReqSchemaName:  "CreateUserRequest",
			RespSchemaName: "User",
		})

	getUser := rest.AddRoute[emptyReq, User](b, "GET", "/users/{id}",
		emptyReqCodec, userCodec, rest.RouteConfig{
			OperationID:    "getUser",
			Summary:        "Get a user by ID",
			RespSchemaName: "User",
			PathParams: []rest.Param{
				{Name: "id", Description: "User UUID"},
			},
		})

	// Step 2: wire routes to net/http via the adapter.
	mux := http.NewServeMux()

	nethttp.Register(mux, createUser, func(ctx context.Context, req CreateUserReq) (User, error) {
		return User{ID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Name: req.Name, Email: req.Email}, nil
	})

	// GET handler: use RequestFromContext to read path parameters.
	nethttp.Register(mux, getUser, func(ctx context.Context, _ emptyReq) (User, error) {
		r, _ := nethttp.RequestFromContext(ctx)
		id := r.PathValue("id")
		return User{ID: id, Name: "Alice", Email: "alice@example.com"}, nil
	})

	// Step 3: demo requests against an in-process httptest server.
	srv := httptest.NewServer(mux)
	defer srv.Close()

	fmt.Println("=== POST /users ===")
	resp, err := http.Post(srv.URL+"/users", "application/json", //nolint:noctx
		strings.NewReader(`{"name":"Alice","email":"alice@example.com"}`))
	if err != nil {
		fmt.Fprintf(os.Stderr, "POST error: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	var created User
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		fmt.Fprintf(os.Stderr, "decode error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Status: %d\nUser:   %+v\n\n", resp.StatusCode, created)

	fmt.Println("=== POST /users (validation error) ===")
	resp2, err := http.Post(srv.URL+"/users", "application/json", //nolint:noctx
		strings.NewReader(`{"name":"","email":"bad"}`))
	if err != nil {
		fmt.Fprintf(os.Stderr, "POST error: %v\n", err)
		os.Exit(1)
	}
	defer resp2.Body.Close()
	var errBody map[string]string
	_ = json.NewDecoder(resp2.Body).Decode(&errBody)
	fmt.Printf("Status: %d\nError:  %s\n\n", resp2.StatusCode, errBody["error"])

	fmt.Println("=== GET /users/{id} ===")
	resp3, err := http.Get(srv.URL + "/users/f47ac10b-58cc-4372-a567-0e02b2c3d479") //nolint:noctx
	if err != nil {
		fmt.Fprintf(os.Stderr, "GET error: %v\n", err)
		os.Exit(1)
	}
	defer resp3.Body.Close()
	var fetched User
	_ = json.NewDecoder(resp3.Body).Decode(&fetched)
	fmt.Printf("Status: %d\nUser:   %+v\n\n", resp3.StatusCode, fetched)

	// Step 4: generate the OpenAPI spec from the same builder.
	fmt.Println("=== OpenAPI 3.1 spec ===")
	doc, err := b.OpenAPISpec()
	if err != nil {
		fmt.Fprintf(os.Stderr, "OpenAPISpec error: %v\n", err)
		os.Exit(1)
	}
	yaml, err := doc.MarshalYAML()
	if err != nil {
		fmt.Fprintf(os.Stderr, "MarshalYAML error: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(string(yaml))
}
