package rest_test

import (
	"encoding/json"
	"errors"
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
	h, err := rest.AddRoute[createReq, userResp](b, "POST", "/users", createReqCodec, userCodec, rest.RouteConfig{})
	if err != nil {
		t.Fatalf("AddRoute error: %v", err)
	}

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
	h, err := rest.AddRoute[createReq, userResp](b, "POST", "/users", createReqCodec, userCodec, rest.RouteConfig{})
	if err != nil {
		t.Fatalf("AddRoute error: %v", err)
	}

	_, err = h.Decode([]byte(`{"name":""}`))
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
	h, err := rest.AddRoute[createReq, userResp](b, "POST", "/users", createReqCodec, userCodec, config)
	if err != nil {
		t.Fatalf("AddRoute error: %v", err)
	}

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
	h, err := rest.AddRoute[createReq, userResp](b, "POST", "/users", createReqCodec, userCodec, rest.RouteConfig{})
	if err != nil {
		t.Fatalf("AddRoute error: %v", err)
	}

	if len(h.Descriptor.Responses) == 0 || h.Descriptor.Responses[0].Status != "201" {
		t.Errorf("POST default status: got %v, want 201", h.Descriptor.Responses)
	}
}

func TestAddRoute_getDefaultStatus200(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	h, err := rest.AddRoute[createReq, userResp](b, "GET", "/users", createReqCodec, userCodec, rest.RouteConfig{})
	if err != nil {
		t.Fatalf("AddRoute error: %v", err)
	}

	if len(h.Descriptor.Responses) == 0 || h.Descriptor.Responses[0].Status != "200" {
		t.Errorf("GET default status: got %v, want 200", h.Descriptor.Responses)
	}
}

func TestAddRoute_bodyOnlyForBodyMethods(t *testing.T) {
	b := rest.NewBuilder(testInfo)

	post, err := rest.AddRoute[createReq, userResp](b, "POST", "/users", createReqCodec, userCodec, rest.RouteConfig{})
	if err != nil {
		t.Fatalf("AddRoute error: %v", err)
	}
	get, err := rest.AddRoute[createReq, userResp](b, "GET", "/users", createReqCodec, userCodec, rest.RouteConfig{})
	if err != nil {
		t.Fatalf("AddRoute error: %v", err)
	}

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
	h, err := rest.AddRoute[createReq, userResp](b, "POST", "/users", createReqCodec, userCodec, config)
	if err != nil {
		t.Fatalf("AddRoute error: %v", err)
	}

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

	if _, err := rest.AddRoute[createReq, userResp](b, "POST", "/users", createReqCodec, userCodec, rest.RouteConfig{
		OperationID:    "createUser",
		Summary:        "Create a user",
		ReqSchemaName:  "CreateUserRequest",
		RespSchemaName: "User",
		Responses: []rest.ResponseMeta{
			{Status: "400", Description: "Validation error"},
		},
	}); err != nil {
		t.Fatalf("AddRoute error: %v", err)
	}
	if _, err := rest.AddRoute[createReq, userResp](b, "GET", "/users/{id}", createReqCodec, userCodec, rest.RouteConfig{
		OperationID: "getUser",
		PathParams: []route.Param{
			{Name: "id", Required: true, Schema: schema.Schema{Type: "string"}},
		},
	}); err != nil {
		t.Fatalf("AddRoute error: %v", err)
	}

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
	if _, err := rest.AddRoute[createReq, userResp](b, "POST", "/users", createReqCodec, userCodec, rest.RouteConfig{}); err != nil {
		t.Fatalf("AddRoute error: %v", err)
	}
	if _, err := rest.AddRoute[createReq, userResp](b, "POST", "/users", createReqCodec, userCodec, rest.RouteConfig{}); err != nil {
		t.Fatalf("AddRoute error: %v", err)
	}

	_, err := b.OpenAPISpec()
	if err == nil {
		t.Fatal("expected error for duplicate POST /users, got nil")
	}
}

func TestBuilder_openAPISpec_multipleRoutesOnSamePath(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	if _, err := rest.AddRoute[createReq, userResp](b, "GET", "/users", createReqCodec, userCodec, rest.RouteConfig{OperationID: "listUsers"}); err != nil {
		t.Fatalf("AddRoute error: %v", err)
	}
	if _, err := rest.AddRoute[createReq, userResp](b, "POST", "/users", createReqCodec, userCodec, rest.RouteConfig{OperationID: "createUser"}); err != nil {
		t.Fatalf("AddRoute error: %v", err)
	}

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
	if _, err := rest.AddRoute[createReq, userResp](b, "POST", "/users", createReqCodec, userCodec, rest.RouteConfig{
		RespSchemaName: "UserResponse",
	}); err != nil {
		t.Fatalf("AddRoute error: %v", err)
	}

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
	if _, err := rest.AddRoute[createReq, userResp](b, "GET", "/health", createReqCodec, userCodec, rest.RouteConfig{
		OperationID: "healthCheck",
	}); err != nil {
		t.Fatalf("AddRoute error: %v", err)
	}

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

func TestBuilder_withPathCodec_validPathPasses(t *testing.T) {
	b := rest.NewBuilder(testInfo, rest.WithPathCodec(
		codex.String().Refine(validate.HTTPPath),
	))
	if _, err := rest.AddRoute[createReq, userResp](b, "POST", "/users", createReqCodec, userCodec, rest.RouteConfig{}); err != nil {
		t.Fatalf("AddRoute error: %v", err)
	}
	if _, err := rest.AddRoute[createReq, userResp](b, "GET", "/users/{id}", createReqCodec, userCodec, rest.RouteConfig{
		PathParams: []route.Param{{Name: "id", Required: true, Schema: schema.Schema{Type: "string"}}},
	}); err != nil {
		t.Fatalf("AddRoute error: %v", err)
	}

	_, err := b.OpenAPISpec()
	if err != nil {
		t.Fatalf("expected no error for valid paths, got: %v", err)
	}
}

func TestBuilder_withPathCodec_invalidPathSurfacesError(t *testing.T) {
	b := rest.NewBuilder(testInfo, rest.WithPathCodec(
		codex.String().Refine(validate.HTTPPath),
	))
	_, err := rest.AddRoute[createReq, userResp](b, "POST", "users", createReqCodec, userCodec, rest.RouteConfig{})
	if err == nil {
		t.Fatal("expected error for path missing leading slash, got nil")
	}
	if !strings.Contains(err.Error(), "users") {
		t.Errorf("error message should mention the invalid path, got: %v", err)
	}
	var pathErr rest.InvalidPathError
	if !errors.As(err, &pathErr) {
		t.Errorf("expected InvalidPathError, got %T: %v", err, err)
	}
	if pathErr.Path != "users" {
		t.Errorf("InvalidPathError.Path = %q, want %q", pathErr.Path, "users")
	}
	if pathErr.Err == nil {
		t.Error("InvalidPathError.Err should be non-nil")
	}
}

func TestBuilder_withPathConstraints_multipleInvalidPathsCollected(t *testing.T) {
	b := rest.NewBuilder(testInfo, rest.WithPathConstraints(validate.HTTPPath))
	_, err := rest.AddRoute[createReq, userResp](b, "POST", "no-slash", createReqCodec, userCodec, rest.RouteConfig{})
	if err == nil {
		t.Fatal("expected error for path missing leading slash, got nil")
	}
	if !strings.Contains(err.Error(), "no-slash") {
		t.Errorf("error should mention the invalid path, got: %v", err)
	}

	_, err = rest.AddRoute[createReq, userResp](b, "GET", "also-bad", createReqCodec, userCodec, rest.RouteConfig{})
	if err == nil {
		t.Fatal("expected error for path missing leading slash, got nil")
	}
	if !strings.Contains(err.Error(), "also-bad") {
		t.Errorf("error should mention the invalid path, got: %v", err)
	}
}

func TestBuilder_noPathCodec_anyPathAccepted(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	if _, err := rest.AddRoute[createReq, userResp](b, "POST", "no-slash", createReqCodec, userCodec, rest.RouteConfig{}); err != nil {
		t.Fatalf("AddRoute error: %v", err)
	}
}

func TestAddRoute_unknownPathParamCodecKey(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	_, err := rest.AddRoute[createReq, userResp](b, "GET", "/users/{id}", createReqCodec, userCodec, rest.RouteConfig{
		PathParamCodecs: map[string]codex.Codec[string]{
			"id":      codex.String().Refine(validate.UUID),
			"missing": codex.String(), // not in template
		},
	})
	if err == nil {
		t.Fatal("expected error for unknown PathParamCodecs key, got nil")
	}
	if !strings.Contains(err.Error(), "missing") {
		t.Errorf("error should mention the unknown key, got: %v", err)
	}
}

func TestBuildPath_validVars(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	h, err := rest.AddRoute[createReq, userResp](b, "GET", "/users/{id}", createReqCodec, userCodec, rest.RouteConfig{
		PathParamCodecs: map[string]codex.Codec[string]{
			"id": codex.String().Refine(validate.UUID),
		},
	})
	if err != nil {
		t.Fatalf("AddRoute error: %v", err)
	}

	path, err := h.BuildPath(map[string]string{"id": "f47ac10b-58cc-4372-a567-0e02b2c3d479"})
	if err != nil {
		t.Fatalf("BuildPath error: %v", err)
	}
	if path != "/users/f47ac10b-58cc-4372-a567-0e02b2c3d479" {
		t.Errorf("BuildPath = %q, want /users/f47ac10b-58cc-4372-a567-0e02b2c3d479", path)
	}
}

func TestBuildPath_missingVar(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	h, err := rest.AddRoute[createReq, userResp](b, "GET", "/users/{id}", createReqCodec, userCodec, rest.RouteConfig{})
	if err != nil {
		t.Fatalf("AddRoute error: %v", err)
	}

	_, err = h.BuildPath(map[string]string{})
	if err == nil {
		t.Fatal("expected error for missing template variable, got nil")
	}
	var missingErr rest.MissingPathVarError
	if !errors.As(err, &missingErr) {
		t.Fatalf("expected MissingPathVarError, got %T: %v", err, err)
	}
	if missingErr.Name != "id" {
		t.Errorf("MissingPathVarError.Name = %q, want id", missingErr.Name)
	}
}

func TestBuildPath_codecFailure(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	h, err := rest.AddRoute[createReq, userResp](b, "GET", "/users/{id}", createReqCodec, userCodec, rest.RouteConfig{
		PathParamCodecs: map[string]codex.Codec[string]{
			"id": codex.String().Refine(validate.UUID),
		},
	})
	if err != nil {
		t.Fatalf("AddRoute error: %v", err)
	}

	_, err = h.BuildPath(map[string]string{"id": "not-a-uuid"})
	if err == nil {
		t.Fatal("expected PathParamError for invalid UUID, got nil")
	}
	var paramErr rest.PathParamError
	if !errors.As(err, &paramErr) {
		t.Fatalf("expected PathParamError, got %T: %v", err, err)
	}
	if paramErr.Name != "id" {
		t.Errorf("PathParamError.Name = %q, want id", paramErr.Name)
	}
	if paramErr.Value != "not-a-uuid" {
		t.Errorf("PathParamError.Value = %q, want not-a-uuid", paramErr.Value)
	}
}

func TestBuildPath_extraKeysIgnored(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	h, err := rest.AddRoute[createReq, userResp](b, "GET", "/users/{id}", createReqCodec, userCodec, rest.RouteConfig{})
	if err != nil {
		t.Fatalf("AddRoute error: %v", err)
	}

	path, err := h.BuildPath(map[string]string{"id": "42", "extra": "ignored"})
	if err != nil {
		t.Fatalf("BuildPath error: %v", err)
	}
	if path != "/users/42" {
		t.Errorf("BuildPath = %q, want /users/42", path)
	}
}
