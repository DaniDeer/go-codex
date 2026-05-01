package rest_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/schema"
	"github.com/DaniDeer/go-codex/validate"
)

// testInfo is reused across test cases.
var testInfo = rest.Info{Title: "Test API", Version: "1.0.0"}

// createReqCodec decodes a simple create-user request.
var createReqCodec = codex.Struct[createReq](
	codex.Field[createReq, string]{
		Name:     "name",
		Codec:    codex.String().Refine(validate.NonEmptyString),
		Get:      func(r createReq) string { return r.Name },
		Set:      func(r *createReq, v string) { r.Name = v },
		Required: true,
	},
)

// userCodec encodes a user response.
var userCodec = codex.Struct[userResp](
	codex.Field[userResp, string]{
		Name:  "id",
		Codec: codex.String(),
		Get:   func(u userResp) string { return u.ID },
		Set:   func(u *userResp, v string) { u.ID = v },
	},
	codex.Field[userResp, string]{
		Name:  "name",
		Codec: codex.String(),
		Get:   func(u userResp) string { return u.Name },
		Set:   func(u *userResp, v string) { u.Name = v },
	},
)

type createReq struct{ Name string }
type userResp struct {
	ID   string
	Name string
}

func TestAddRoute_returnsHandleWithDecodeEncode(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	h := rest.AddRoute[createReq, userResp](b, "POST", "/users", createReqCodec, userCodec, rest.RouteConfig{})

	// Decode valid JSON body.
	req, err := h.Decode([]byte(`{"name":"Alice"}`))
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	if req.Name != "Alice" {
		t.Errorf("got name %q, want %q", req.Name, "Alice")
	}

	// Encode response.
	out, err := h.Encode(userResp{ID: "1", Name: "Alice"})
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("Encode output not valid JSON: %v", err)
	}
	if m["id"] != "1" {
		t.Errorf("encoded id = %v, want 1", m["id"])
	}
}

func TestAddRoute_decodeRunsValidation(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	h := rest.AddRoute[createReq, userResp](b, "POST", "/users", createReqCodec, userCodec, rest.RouteConfig{})

	_, err := h.Decode([]byte(`{"name":""}`))
	if err == nil {
		t.Fatal("expected validation error for empty name, got nil")
	}
	if !strings.Contains(err.Error(), "non-empty") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestAddRoute_descriptorFrozenAtRegistration(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	config := rest.RouteConfig{OperationID: "createUser", Tags: []string{"users"}}
	h := rest.AddRoute[createReq, userResp](b, "POST", "/users", createReqCodec, userCodec, config)

	// Mutate the original config after registration.
	config.OperationID = "mutated"
	config.Tags[0] = "mutated"

	if h.Descriptor.OperationID != "createUser" {
		t.Errorf("descriptor OperationID mutated: got %q", h.Descriptor.OperationID)
	}
	if len(h.Descriptor.Tags) > 0 && h.Descriptor.Tags[0] != "users" {
		t.Errorf("descriptor Tags mutated: got %v", h.Descriptor.Tags)
	}
}

func TestAddRoute_postDefaultStatus201(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	h := rest.AddRoute[createReq, userResp](b, "POST", "/users", createReqCodec, userCodec, rest.RouteConfig{})

	if len(h.Descriptor.Responses) == 0 || h.Descriptor.Responses[0].Status != "201" {
		t.Errorf("POST default status: got %v, want 201", h.Descriptor.Responses)
	}
}

func TestAddRoute_getDefaultStatus200(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	h := rest.AddRoute[createReq, userResp](b, "GET", "/users", createReqCodec, userCodec, rest.RouteConfig{})

	if len(h.Descriptor.Responses) == 0 || h.Descriptor.Responses[0].Status != "200" {
		t.Errorf("GET default status: got %v, want 200", h.Descriptor.Responses)
	}
}

func TestAddRoute_bodyOnlyForBodyMethods(t *testing.T) {
	b := rest.NewBuilder(testInfo)

	post := rest.AddRoute[createReq, userResp](b, "POST", "/users", createReqCodec, userCodec, rest.RouteConfig{})
	get := rest.AddRoute[createReq, userResp](b, "GET", "/users", createReqCodec, userCodec, rest.RouteConfig{})

	if post.Descriptor.RequestBody == nil {
		t.Error("POST descriptor: expected RequestBody, got nil")
	}
	if get.Descriptor.RequestBody != nil {
		t.Error("GET descriptor: expected no RequestBody, got non-nil")
	}
}

func TestAddRoute_additionalResponsesAppended(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	errSchema := schema.Schema{Type: "object"}
	config := rest.RouteConfig{
		Responses: []rest.ResponseMeta{
			{Status: "400", Description: "Bad request", Schema: &errSchema},
			{Status: "404", Description: "Not found"},
		},
	}
	h := rest.AddRoute[createReq, userResp](b, "POST", "/users", createReqCodec, userCodec, config)

	if len(h.Descriptor.Responses) != 3 {
		t.Fatalf("expected 3 responses (1 success + 2 extra), got %d", len(h.Descriptor.Responses))
	}
	if h.Descriptor.Responses[1].Status != "400" {
		t.Errorf("second response status: got %q, want 400", h.Descriptor.Responses[1].Status)
	}
	if h.Descriptor.Responses[2].Status != "404" {
		t.Errorf("third response status: got %q, want 404", h.Descriptor.Responses[2].Status)
	}
}

func TestBuilder_openAPISpec_containsRegisteredRoutes(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	b.AddServer("production", rest.Server{URL: "https://api.example.com"})

	rest.AddRoute[createReq, userResp](b, "POST", "/users", createReqCodec, userCodec, rest.RouteConfig{
		OperationID:    "createUser",
		Summary:        "Create a user",
		ReqSchemaName:  "CreateUserRequest",
		RespSchemaName: "User",
		Responses: []rest.ResponseMeta{
			{Status: "400", Description: "Validation error"},
		},
	})
	rest.AddRoute[createReq, userResp](b, "GET", "/users/{id}", createReqCodec, userCodec, rest.RouteConfig{
		OperationID: "getUser",
		PathParams: []route.Param{
			{Name: "id", Required: true, Schema: schema.Schema{Type: "string"}},
		},
	})

	doc, err := b.OpenAPISpec()
	if err != nil {
		t.Fatalf("OpenAPISpec error: %v", err)
	}

	yamlBytes, err := doc.MarshalYAML()
	if err != nil {
		t.Fatalf("MarshalYAML error: %v", err)
	}
	out := string(yamlBytes)

	for _, want := range []string{
		"openapi:",
		"Test API",
		"/users:",
		"/users/{id}:",
		"post:",
		"get:",
		"createUser",
		"getUser",
		"components:",
		"CreateUserRequest",
		"User",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("OpenAPI spec missing %q\nfull output:\n%s", want, out)
		}
	}
}

func TestBuilder_openAPISpec_duplicateRouteError(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	rest.AddRoute[createReq, userResp](b, "POST", "/users", createReqCodec, userCodec, rest.RouteConfig{})
	rest.AddRoute[createReq, userResp](b, "POST", "/users", createReqCodec, userCodec, rest.RouteConfig{})

	_, err := b.OpenAPISpec()
	if err == nil {
		t.Fatal("expected error for duplicate POST /users, got nil")
	}
}

func TestBuilder_openAPISpec_multipleRoutesOnSamePath(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	rest.AddRoute[createReq, userResp](b, "GET", "/users", createReqCodec, userCodec, rest.RouteConfig{OperationID: "listUsers"})
	rest.AddRoute[createReq, userResp](b, "POST", "/users", createReqCodec, userCodec, rest.RouteConfig{OperationID: "createUser"})

	doc, err := b.OpenAPISpec()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	b2, _ := doc.MarshalYAML()
	out := string(b2)
	if !strings.Contains(out, "listUsers") || !strings.Contains(out, "createUser") {
		t.Error("both operations should appear in spec")
	}
}

func TestBuilder_openAPISpec_schemaRefInComponents(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	rest.AddRoute[createReq, userResp](b, "POST", "/users", createReqCodec, userCodec, rest.RouteConfig{
		RespSchemaName: "UserResponse",
	})

	doc, err := b.OpenAPISpec()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	b2, _ := doc.MarshalYAML()
	out := string(b2)
	if !strings.Contains(out, "UserResponse") {
		t.Error("spec missing UserResponse schema name")
	}
	if !strings.Contains(out, "$ref") {
		t.Error("spec missing $ref for named schema")
	}
}

func TestBuilder_openAPISpec_jsonOutput(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	rest.AddRoute[createReq, userResp](b, "GET", "/health", createReqCodec, userCodec, rest.RouteConfig{
		OperationID: "healthCheck",
	})

	doc, err := b.OpenAPISpec()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	jsonBytes, err := doc.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON error: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(jsonBytes, &m); err != nil {
		t.Fatalf("output not valid JSON: %v", err)
	}
	if m["openapi"] != "3.1.0" {
		t.Errorf("openapi version: got %v, want 3.1.0", m["openapi"])
	}
}
