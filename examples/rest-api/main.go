// Package rest-api demonstrates generating a full OpenAPI 3.1 document from
// route descriptors and Codec-derived schemas using the render/openapi package.
//
// Run with: go run ./examples/rest-api
package main

import (
	"fmt"
	"os"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/render/openapi"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/schema"
	"github.com/DaniDeer/go-codex/validate"
)

// User is a domain type whose codec is the single source of truth for
// encoding, decoding, validation, and schema documentation.
type User struct {
	ID    string
	Name  string
	Email string
}

var UserCodec = codex.Struct[User](
	codex.Field[User, string]{
		Name:     "id",
		Codec:    codex.String().Refine(validate.UUID).WithDescription("Unique user ID (UUID)."),
		Get:      func(u User) string { return u.ID },
		Set:      func(u *User, v string) { u.ID = v },
		Required: true,
	},
	codex.Field[User, string]{
		Name:  "name",
		Codec: codex.String().Refine(validate.NonEmptyString).Refine(validate.MaxLen(100)).WithDescription("Full display name."),
		Get:   func(u User) string { return u.Name },
		Set:   func(u *User, v string) { u.Name = v },

		Required: true,
	},
	codex.Field[User, string]{
		Name:     "email",
		Codec:    codex.String().Refine(validate.Email).WithDescription("Primary email address."),
		Get:      func(u User) string { return u.Email },
		Set:      func(u *User, v string) { u.Email = v },
		Required: true,
	},
)

// CreateUserRequest is the request body for POST /users.
type CreateUserRequest struct {
	Name  string
	Email string
}

var CreateUserRequestCodec = codex.Struct[CreateUserRequest](
	codex.Field[CreateUserRequest, string]{
		Name:     "name",
		Codec:    codex.String().Refine(validate.NonEmptyString).Refine(validate.MaxLen(100)).WithDescription("Full display name."),
		Get:      func(r CreateUserRequest) string { return r.Name },
		Set:      func(r *CreateUserRequest, v string) { r.Name = v },
		Required: true,
	},
	codex.Field[CreateUserRequest, string]{
		Name:     "email",
		Codec:    codex.String().Refine(validate.Email).WithDescription("Primary email address."),
		Get:      func(r CreateUserRequest) string { return r.Email },
		Set:      func(r *CreateUserRequest, v string) { r.Email = v },
		Required: true,
	},
)

func main() {
	userSchema := UserCodec.Schema
	createSchema := CreateUserRequestCodec.Schema

	doc, err := openapi.NewDocumentBuilder(openapi.Info{
		Title:       "User API",
		Version:     "1.0.0",
		Description: "CRUD API for managing users.",
	}).
		AddServer(openapi.Server{
			URL:         "https://api.example.com/v1",
			Description: "Production",
		}).
		AddServer(openapi.Server{
			URL:         "http://localhost:8080/v1",
			Description: "Local development",
		}).
		AddRoute(route.Route{
			Method:      "GET",
			Path:        "/users",
			OperationID: "listUsers",
			Summary:     "List users",
			Tags:        []string{"users"},
			QueryParams: []route.Param{
				{Name: "limit", Description: "Maximum number of results.", Schema: schema.Schema{Type: "integer"}},
				{Name: "offset", Description: "Number of results to skip.", Schema: schema.Schema{Type: "integer"}},
			},
			Responses: []route.Response{
				{Status: "200", Description: "List of users.", Schema: &schema.Schema{Type: "array", Items: &userSchema}, SchemaName: ""},
			},
		}).
		AddRoute(route.Route{
			Method:      "POST",
			Path:        "/users",
			OperationID: "createUser",
			Summary:     "Create a user",
			Tags:        []string{"users"},
			RequestBody: &route.Body{
				Required:   true,
				Schema:     createSchema,
				SchemaName: "CreateUserRequest",
			},
			Responses: []route.Response{
				{Status: "201", Description: "User created.", Schema: &userSchema, SchemaName: "User"},
				{Status: "400", Description: "Validation error."},
			},
		}).
		AddRoute(route.Route{
			Method:      "GET",
			Path:        "/users/{id}",
			OperationID: "getUser",
			Summary:     "Get a user",
			Tags:        []string{"users"},
			PathParams: []route.Param{
				{Name: "id", Required: true, Description: "User ID (UUID).", Schema: schema.Schema{Type: "string", Format: "uuid"}},
			},
			Responses: []route.Response{
				{Status: "200", Description: "User found.", Schema: &userSchema, SchemaName: "User"},
				{Status: "404", Description: "User not found."},
			},
		}).
		AddRoute(route.Route{
			Method:      "DELETE",
			Path:        "/users/{id}",
			OperationID: "deleteUser",
			Summary:     "Delete a user",
			Tags:        []string{"users"},
			PathParams: []route.Param{
				{Name: "id", Required: true, Description: "User ID (UUID).", Schema: schema.Schema{Type: "string", Format: "uuid"}},
			},
			Responses: []route.Response{
				{Status: "204", Description: "User deleted."},
				{Status: "404", Description: "User not found."},
			},
		}).
		Build()
	if err != nil {
		fmt.Fprintf(os.Stderr, "build error: %v\n", err)
		os.Exit(1)
	}

	yamlBytes, err := doc.MarshalYAML()
	if err != nil {
		fmt.Fprintf(os.Stderr, "render error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("# Full OpenAPI 3.1 document (YAML)")
	fmt.Println()
	fmt.Print(string(yamlBytes))
}
