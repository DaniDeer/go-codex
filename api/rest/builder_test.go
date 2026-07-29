package rest_test

import (
	"bytes"
	"context"
	"encoding/gob"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DaniDeer/go-codex/adapters/nethttp"
	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/schema"
	"github.com/DaniDeer/go-codex/validate"
)

// testInfo is reused across test cases.
var testInfo = rest.Info{Title: "Test API", Version: "1.0.0"}

// createReqCodec decodes a simple create-user request.
var createReqCodec = codex.Struct[createReq](
	codex.RequiredField("name", codex.String().Refine(validate.NonEmptyString),
		func(r createReq) string { return r.Name },
		func(r *createReq, v string) { r.Name = v },
	),
)

// userCodec encodes a user response.
var userCodec = codex.Struct[userResp](
	codex.OptionalField("id", codex.String(),
		func(u userResp) string { return u.ID },
		func(u *userResp, v string) { u.ID = v },
	),
	codex.OptionalField("name", codex.String(),
		func(u userResp) string { return u.Name },
		func(u *userResp, v string) { u.Name = v },
	),
)

type createReq struct{ Name string }
type sseEvent struct{ Message string }

var sseEventCodec = codex.Struct[sseEvent](
	codex.RequiredField("message", codex.String().Refine(validate.NonEmptyString),
		func(e sseEvent) string { return e.Message },
		func(e *sseEvent, v string) { e.Message = v },
	),
)

type userResp struct {
	ID   string
	Name string
}

func TestAddRoute_returnsHandleWithDecodeEncode(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	h, err := rest.NewRoute[createReq, userResp]("POST", "/users", createReqCodec, userCodec).Register(b)
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
	h, err := rest.NewRoute[createReq, userResp]("POST", "/users", createReqCodec, userCodec).Register(b)
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
	config := rest.RouteMeta{OperationID: "createUser", Tags: []string{"users"}}
	h, err := rest.NewRoute[createReq, userResp]("POST", "/users", createReqCodec, userCodec, config).Register(b)
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
	h, err := rest.NewRoute[createReq, userResp]("POST", "/users", createReqCodec, userCodec).Register(b)
	if err != nil {
		t.Fatalf("AddRoute error: %v", err)
	}

	if len(h.Descriptor.Responses) == 0 || h.Descriptor.Responses[0].Status != "201" {
		t.Errorf("POST default status: got %v, want 201", h.Descriptor.Responses)
	}
}

func TestAddRoute_getDefaultStatus200(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	h, err := rest.NewRoute[createReq, userResp]("GET", "/users", createReqCodec, userCodec).Register(b)
	if err != nil {
		t.Fatalf("AddRoute error: %v", err)
	}

	if len(h.Descriptor.Responses) == 0 || h.Descriptor.Responses[0].Status != "200" {
		t.Errorf("GET default status: got %v, want 200", h.Descriptor.Responses)
	}
}

func TestAddRoute_bodyOnlyForBodyMethods(t *testing.T) {
	b := rest.NewBuilder(testInfo)

	post, err := rest.NewRoute[createReq, userResp]("POST", "/users", createReqCodec, userCodec).Register(b)
	if err != nil {
		t.Fatalf("AddRoute error: %v", err)
	}
	get, err := rest.NewRoute[createReq, userResp]("GET", "/users", createReqCodec, userCodec).Register(b)
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
	config := []rest.RouteOpt{rest.ResponseMeta{Status: "400", Description: "Bad request", Schema: &errSchema}, rest.ResponseMeta{Status: "404", Description: "Not found"}}

	h, err := rest.NewRoute[createReq, userResp]("POST", "/users", createReqCodec, userCodec, config...).Register(b)
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

	if _, err := rest.NewRoute[createReq, userResp]("POST", "/users", createReqCodec, userCodec, rest.RouteMeta{OperationID: "createUser",
		Summary:        "Create a user",
		ReqSchemaName:  "CreateUserRequest",
		RespSchemaName: "User"}, rest.ResponseMeta{Status: "400", Description: "Validation error"},
	).Register(b); err != nil {
		t.Fatalf("AddRoute error: %v", err)
	}
	if _, err := rest.NewRoute[createReq, userResp]("GET", "/users/{id}", createReqCodec, userCodec, rest.RouteMeta{OperationID: "getUser"}, rest.PathParam{Name: "id"}).Register(b); err != nil {
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
	if _, err := rest.NewRoute[createReq, userResp]("POST", "/users", createReqCodec, userCodec).Register(b); err != nil {
		t.Fatalf("AddRoute error: %v", err)
	}
	if _, err := rest.NewRoute[createReq, userResp]("POST", "/users", createReqCodec, userCodec).Register(b); err != nil {
		t.Fatalf("AddRoute error: %v", err)
	}

	_, err := b.OpenAPISpec()
	if err == nil {
		t.Fatal("expected error for duplicate POST /users, got nil")
	}
}

func TestBuilder_openAPISpec_multipleRoutesOnSamePath(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	if _, err := rest.NewRoute[createReq, userResp]("GET", "/users", createReqCodec, userCodec, rest.RouteMeta{OperationID: "listUsers"}).Register(b); err != nil {
		t.Fatalf("AddRoute error: %v", err)
	}
	if _, err := rest.NewRoute[createReq, userResp]("POST", "/users", createReqCodec, userCodec, rest.RouteMeta{OperationID: "createUser"}).Register(b); err != nil {
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
	if _, err := rest.NewRoute[createReq, userResp]("POST", "/users", createReqCodec, userCodec, rest.RouteMeta{RespSchemaName: "UserResponse"}).Register(b); err != nil {
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
	if _, err := rest.NewRoute[createReq, userResp]("GET", "/health", createReqCodec, userCodec, rest.RouteMeta{OperationID: "healthCheck"}).Register(b); err != nil {
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
	if _, err := rest.NewRoute[createReq, userResp]("POST", "/users", createReqCodec, userCodec).Register(b); err != nil {
		t.Fatalf("AddRoute error: %v", err)
	}
	if _, err := rest.NewRoute[createReq, userResp]("GET", "/users/{id}", createReqCodec, userCodec, rest.PathParam{Name: "id"}).Register(b); err != nil {
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
	_, err := rest.NewRoute[createReq, userResp]("POST", "users", createReqCodec, userCodec).Register(b)
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
	_, err := rest.NewRoute[createReq, userResp]("POST", "no-slash", createReqCodec, userCodec).Register(b)
	if err == nil {
		t.Fatal("expected error for path missing leading slash, got nil")
	}
	if !strings.Contains(err.Error(), "no-slash") {
		t.Errorf("error should mention the invalid path, got: %v", err)
	}

	_, err = rest.NewRoute[createReq, userResp]("GET", "also-bad", createReqCodec, userCodec).Register(b)
	if err == nil {
		t.Fatal("expected error for path missing leading slash, got nil")
	}
	if !strings.Contains(err.Error(), "also-bad") {
		t.Errorf("error should mention the invalid path, got: %v", err)
	}
}

func TestBuilder_noPathCodec_anyPathAccepted(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	if _, err := rest.NewRoute[createReq, userResp]("POST", "no-slash", createReqCodec, userCodec).Register(b); err != nil {
		t.Fatalf("AddRoute error: %v", err)
	}
}

func TestAddRoute_unknownPathParamCodecKey(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	uuidCodec := codex.String().Refine(validate.UUID)
	strCodec := codex.String()
	_, err := rest.NewRoute[createReq, userResp]("GET", "/users/{id}", createReqCodec, userCodec,
		rest.PathParam{Name: "id", Codec: &uuidCodec},
		rest.PathParam{Name: "missing", Codec: &strCodec}, // not in template
	).Register(b)

	if err == nil {
		t.Fatal("expected error for unknown PathParams name, got nil")
	}
	var paramErr rest.InvalidPathParamError
	if !errors.As(err, &paramErr) {
		t.Fatalf("expected InvalidPathParamError, got %T: %v", err, err)
	}
	if paramErr.Name != "missing" {
		t.Errorf("InvalidPathParamError.Name = %q, want %q", paramErr.Name, "missing")
	}
}

func TestBuildPath_validVars(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	uuidCodec := codex.String().Refine(validate.UUID)
	h, err := rest.NewRoute[createReq, userResp]("GET", "/users/{id}", createReqCodec, userCodec, rest.PathParam{Name: "id", Codec: &uuidCodec}).Register(b)
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
	h, err := rest.NewRoute[createReq, userResp]("GET", "/users/{id}", createReqCodec, userCodec).Register(b)
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
	uuidCodec := codex.String().Refine(validate.UUID)
	h, err := rest.NewRoute[createReq, userResp]("GET", "/users/{id}", createReqCodec, userCodec, rest.PathParam{Name: "id", Codec: &uuidCodec}).Register(b)
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
	h, err := rest.NewRoute[createReq, userResp]("GET", "/users/{id}", createReqCodec, userCodec).Register(b)
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

// TestBuilder_withPathConstraints_templateTransparent verifies that a constraint
// that does not mention braces still accepts template paths. The constraint sees
// "/users/x" (with {id} replaced by "x"), not "/users/{id}".
func TestBuilder_withPathConstraints_templateTransparent(t *testing.T) {
	noBraces := codex.Constraint[string]{
		Name:    "no-braces",
		Check:   func(v string) bool { return !strings.ContainsAny(v, "{}") },
		Message: func(v string) string { return fmt.Sprintf("path must not contain braces: %q", v) },
	}
	b := rest.NewBuilder(testInfo, rest.WithPathConstraints(noBraces))

	// Without template-transparent stripping this would return an InvalidPathError.
	if _, err := rest.NewRoute[createReq, userResp]("GET", "/users/{id}", createReqCodec, userCodec).Register(b); err != nil {
		t.Fatalf("expected template path to pass brace-free constraint after stripping, got: %v", err)
	}
}

// TestBuildPath_finalPathReValidatedAgainstBuilderCodec verifies that if a
// variable value passes its PathParamCodecs codec but the final assembled path
// would fail the builder's path codec, BuildPath returns an InvalidPathError.
func TestBuildPath_finalPathReValidatedAgainstBuilderCodec(t *testing.T) {
	noSpaces := codex.Constraint[string]{
		Name:    "no-spaces",
		Check:   func(v string) bool { return !strings.ContainsRune(v, ' ') },
		Message: func(v string) string { return fmt.Sprintf("path must not contain spaces: %q", v) },
	}
	b := rest.NewBuilder(testInfo, rest.WithPathConstraints(noSpaces))
	// PathParams only checks non-empty — does NOT forbid spaces.
	nonEmptyCodec := codex.String().Refine(validate.NonEmptyString)
	h, err := rest.NewRoute[createReq, userResp]("GET", "/users/{name}", createReqCodec, userCodec, rest.PathParam{Name: "name", Codec: &nonEmptyCodec}).Register(b)
	if err != nil {
		t.Fatalf("AddRoute error: %v", err)
	}

	// "alice" is fine: NonEmptyString passes, final "/users/alice" passes no-spaces.
	path, err := h.BuildPath(map[string]string{"name": "alice"})
	if err != nil {
		t.Fatalf("BuildPath(alice) error: %v", err)
	}
	if path != "/users/alice" {
		t.Errorf("BuildPath = %q, want /users/alice", path)
	}

	// "hello world" passes NonEmptyString but final "/users/hello world" fails no-spaces.
	_, err = h.BuildPath(map[string]string{"name": "hello world"})
	if err == nil {
		t.Fatal("expected error for space in final path, got nil")
	}
	var pathErr rest.InvalidPathError
	if !errors.As(err, &pathErr) {
		t.Errorf("expected InvalidPathError, got %T: %v", err, err)
	}
	if pathErr.Path != "/users/hello world" {
		t.Errorf("InvalidPathError.Path = %q, want /users/hello world", pathErr.Path)
	}
}

func TestValidateQuery_validValues(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	uuidCodec := codex.String().Refine(validate.UUID)
	pageCodec := codex.String().Refine(validate.NonNegativeIntString)
	h, err := rest.NewRoute[createReq, userResp]("GET", "/users", createReqCodec, userCodec, rest.QueryParam{Name: "id", Description: "User UUID", Codec: &uuidCodec}, rest.QueryParam{Name: "page", Description: "Page number", Required: false, Codec: &pageCodec}).Register(b)
	if err != nil {
		t.Fatalf("AddRoute: %v", err)
	}
	if err := h.ValidateQuery(map[string]string{
		"id":   "f47ac10b-58cc-4372-a567-0e02b2c3d479",
		"page": "3",
	}); err != nil {
		t.Errorf("ValidateQuery with valid values: %v", err)
	}
}

func TestValidateQuery_invalidValue(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	uuidCodec := codex.String().Refine(validate.UUID)
	h, err := rest.NewRoute[createReq, userResp]("GET", "/users", createReqCodec, userCodec, rest.QueryParam{Name: "id", Codec: &uuidCodec}).Register(b)
	if err != nil {
		t.Fatalf("AddRoute: %v", err)
	}
	err = h.ValidateQuery(map[string]string{"id": "not-a-uuid"})
	if err == nil {
		t.Fatal("expected QueryParamError, got nil")
	}
	var paramErr rest.QueryParamError
	if !errors.As(err, &paramErr) {
		t.Fatalf("expected QueryParamError, got %T: %v", err, err)
	}
	if paramErr.Name != "id" {
		t.Errorf("QueryParamError.Name = %q, want id", paramErr.Name)
	}
	if paramErr.Value != "not-a-uuid" {
		t.Errorf("QueryParamError.Value = %q, want not-a-uuid", paramErr.Value)
	}
}

func TestValidateQuery_nilCodecSkipped(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	h, err := rest.NewRoute[createReq, userResp]("GET", "/users", createReqCodec, userCodec,
		rest.QueryParam{Name: "search"}, // no codec
	).Register(b)

	if err != nil {
		t.Fatalf("AddRoute: %v", err)
	}
	if err := h.ValidateQuery(map[string]string{"search": "anything goes"}); err != nil {
		t.Errorf("ValidateQuery with nil codec: %v", err)
	}
}

func TestValidateQuery_missingKeySkipped(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	uuidCodec := codex.String().Refine(validate.UUID)
	h, err := rest.NewRoute[createReq, userResp]("GET", "/users", createReqCodec, userCodec, rest.QueryParam{Name: "id", Codec: &uuidCodec}).Register(b)
	if err != nil {
		t.Fatalf("AddRoute: %v", err)
	}
	// "id" not in params — should silently skip, not error
	if err := h.ValidateQuery(map[string]string{}); err != nil {
		t.Errorf("ValidateQuery with missing key: %v", err)
	}
}

func TestValidateQueryMulti_validFirstValue(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	uuidCodec := codex.String().Refine(validate.UUID)
	h, err := rest.NewRoute[createReq, userResp]("GET", "/items", createReqCodec, userCodec, rest.QueryParam{Name: "id", Codec: &uuidCodec}).Register(b)
	if err != nil {
		t.Fatalf("AddRoute: %v", err)
	}
	params := map[string][]string{
		"id": {"550e8400-e29b-41d4-a716-446655440000", "ignored-second-value"},
	}
	if err := h.ValidateQueryMulti(params); err != nil {
		t.Errorf("ValidateQueryMulti with valid first value: %v", err)
	}
}

func TestValidateQueryMulti_invalidFirstValue(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	uuidCodec := codex.String().Refine(validate.UUID)
	h, err := rest.NewRoute[createReq, userResp]("GET", "/items", createReqCodec, userCodec, rest.QueryParam{Name: "id", Codec: &uuidCodec}).Register(b)
	if err != nil {
		t.Fatalf("AddRoute: %v", err)
	}
	params := map[string][]string{"id": {"not-a-uuid"}}
	err = h.ValidateQueryMulti(params)
	if err == nil {
		t.Fatal("expected error for invalid first value")
	}
	var qpe rest.QueryParamError
	if !errors.As(err, &qpe) {
		t.Errorf("expected QueryParamError, got %T: %v", err, err)
	}
	if qpe.Name != "id" || qpe.Value != "not-a-uuid" {
		t.Errorf("unexpected QueryParamError fields: %+v", qpe)
	}
}

func TestValidateQueryMulti_missingKeySkipped(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	uuidCodec := codex.String().Refine(validate.UUID)
	h, err := rest.NewRoute[createReq, userResp]("GET", "/items", createReqCodec, userCodec, rest.QueryParam{Name: "id", Codec: &uuidCodec}).Register(b)
	if err != nil {
		t.Fatalf("AddRoute: %v", err)
	}
	if err := h.ValidateQueryMulti(map[string][]string{}); err != nil {
		t.Errorf("ValidateQueryMulti with missing key: %v", err)
	}
}

func TestQueryParam_schemaFlowsToSpec(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	uuidCodec := codex.String().Refine(validate.UUID)
	_, err := rest.NewRoute[createReq, userResp]("GET", "/users", createReqCodec, userCodec, rest.QueryParam{Name: "id", Description: "User UUID", Required: true, Codec: &uuidCodec}).Register(b)
	if err != nil {
		t.Fatalf("AddRoute: %v", err)
	}
	doc, err := b.OpenAPISpec()
	if err != nil {
		t.Fatalf("OpenAPISpec: %v", err)
	}
	raw, err := doc.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	spec := string(raw)
	if !strings.Contains(spec, `"id"`) {
		t.Errorf("spec missing query param 'id': %s", spec)
	}
	if !strings.Contains(spec, `"uuid"`) {
		t.Errorf("spec missing 'uuid' format from codec schema: %s", spec)
	}
}

func TestValidateCookies_valid(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	tokenCodec := codex.String().Refine(validate.NonEmptyString)
	h, err := rest.NewRoute[createReq, userResp]("GET", "/protected", createReqCodec, userCodec, rest.CookieParam{Name: "session_token", Required: true, Codec: &tokenCodec}).Register(b)
	if err != nil {
		t.Fatalf("AddRoute: %v", err)
	}
	if err := h.ValidateCookies(map[string]string{"session_token": "abc123"}); err != nil {
		t.Fatalf("want nil, got %v", err)
	}
}

func TestValidateCookies_invalid(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	tokenCodec := codex.String().Refine(validate.NonEmptyString)
	h, err := rest.NewRoute[createReq, userResp]("GET", "/protected", createReqCodec, userCodec, rest.CookieParam{Name: "session_token", Required: true, Codec: &tokenCodec}).Register(b)
	if err != nil {
		t.Fatalf("AddRoute: %v", err)
	}
	err = h.ValidateCookies(map[string]string{"session_token": ""})
	var cookieErr rest.CookieParamError
	if !errors.As(err, &cookieErr) {
		t.Fatalf("want CookieParamError, got %T: %v", err, err)
	}
	if cookieErr.Name != "session_token" {
		t.Errorf("want Name=session_token, got %q", cookieErr.Name)
	}
}

func TestValidateCookies_missingParam_notRequired_skipped(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	tokenCodec := codex.String().Refine(validate.NonEmptyString)
	h, err := rest.NewRoute[createReq, userResp]("GET", "/protected", createReqCodec, userCodec, rest.CookieParam{Name: "session_token", Required: false, Codec: &tokenCodec}).Register(b)
	if err != nil {
		t.Fatalf("AddRoute: %v", err)
	}
	// Optional cookie not present — silently skipped.
	if err := h.ValidateCookies(map[string]string{}); err != nil {
		t.Fatalf("want nil for missing optional cookie, got %v", err)
	}
}

func TestValidateCookies_missingParam_required_returnsError(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	tokenCodec := codex.String().Refine(validate.NonEmptyString)
	h, err := rest.NewRoute[createReq, userResp]("GET", "/protected", createReqCodec, userCodec, rest.CookieParam{Name: "session_token", Required: true, Codec: &tokenCodec}).Register(b)
	if err != nil {
		t.Fatalf("AddRoute: %v", err)
	}
	err = h.ValidateCookies(map[string]string{})
	if !errors.Is(err, rest.ErrRequiredParam) {
		t.Fatalf("want ErrRequiredParam, got %v", err)
	}
}

func TestValidateHeaders_valid(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	uuidCodec := codex.String().Refine(validate.UUID)
	h, err := rest.NewRoute[createReq, userResp]("POST", "/items", createReqCodec, userCodec, rest.HeaderParam{Name: "X-Request-ID", Required: true, Codec: &uuidCodec}).Register(b)
	if err != nil {
		t.Fatalf("AddRoute: %v", err)
	}
	if err := h.ValidateHeaders(map[string]string{"X-Request-ID": "f47ac10b-58cc-4372-a567-0e02b2c3d479"}); err != nil {
		t.Fatalf("want nil, got %v", err)
	}
}

func TestValidateHeaders_invalid(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	uuidCodec := codex.String().Refine(validate.UUID)
	h, err := rest.NewRoute[createReq, userResp]("POST", "/items", createReqCodec, userCodec, rest.HeaderParam{Name: "X-Request-ID", Required: true, Codec: &uuidCodec}).Register(b)
	if err != nil {
		t.Fatalf("AddRoute: %v", err)
	}
	err = h.ValidateHeaders(map[string]string{"X-Request-ID": "not-a-uuid"})
	var headerErr rest.HeaderParamError
	if !errors.As(err, &headerErr) {
		t.Fatalf("want HeaderParamError, got %T: %v", err, err)
	}
	if headerErr.Name != "X-Request-ID" {
		t.Errorf("want Name=X-Request-ID, got %q", headerErr.Name)
	}
}

func TestCookieParam_schemaFlowsToSpec(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	tokenCodec := codex.String().Refine(validate.NonEmptyString)
	_, err := rest.NewRoute[createReq, userResp]("GET", "/protected", createReqCodec, userCodec, rest.CookieParam{Name: "session_token", Description: "Auth cookie", Required: true, Codec: &tokenCodec}).Register(b)
	if err != nil {
		t.Fatalf("AddRoute: %v", err)
	}
	doc, err := b.OpenAPISpec()
	if err != nil {
		t.Fatalf("OpenAPISpec: %v", err)
	}
	raw, err := doc.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	spec := string(raw)
	if !strings.Contains(spec, `"session_token"`) {
		t.Errorf("spec missing cookie param 'session_token': %s", spec)
	}
	if !strings.Contains(spec, `"cookie"`) {
		t.Errorf("spec missing 'cookie' in param location: %s", spec)
	}
}

func TestHeaderParam_schemaFlowsToSpec(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	uuidCodec := codex.String().Refine(validate.UUID)
	_, err := rest.NewRoute[createReq, userResp]("POST", "/items", createReqCodec, userCodec, rest.HeaderParam{Name: "X-Request-ID", Description: "Idempotency key", Required: true, Codec: &uuidCodec}).Register(b)
	if err != nil {
		t.Fatalf("AddRoute: %v", err)
	}
	doc, err := b.OpenAPISpec()
	if err != nil {
		t.Fatalf("OpenAPISpec: %v", err)
	}
	raw, err := doc.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	spec := string(raw)
	if !strings.Contains(spec, `"X-Request-ID"`) {
		t.Errorf("spec missing header param 'X-Request-ID': %s", spec)
	}
	if !strings.Contains(spec, `"header"`) {
		t.Errorf("spec missing 'header' in param location: %s", spec)
	}
}

func TestResponseHeaderParam_validateValid(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	uuidCodec := codex.String().Refine(validate.UUID)
	h, err := rest.NewRoute[createReq, userResp]("POST", "/users", createReqCodec, userCodec, rest.ResponseHeaderParam{Name: "Location", Description: "URL of created resource", Codec: &uuidCodec}).Register(b)
	if err != nil {
		t.Fatalf("AddRoute: %v", err)
	}
	err = h.ValidateResponseHeaders(map[string]string{
		"Location": "f47ac10b-58cc-4372-a567-0e02b2c3d479",
	})
	if err != nil {
		t.Errorf("want nil, got %v", err)
	}
}

func TestResponseHeaderParam_validateInvalid(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	uuidCodec := codex.String().Refine(validate.UUID)
	h, err := rest.NewRoute[createReq, userResp]("POST", "/users", createReqCodec, userCodec, rest.ResponseHeaderParam{Name: "ETag", Description: "Entity tag", Codec: &uuidCodec}).Register(b)
	if err != nil {
		t.Fatalf("AddRoute: %v", err)
	}
	err = h.ValidateResponseHeaders(map[string]string{"ETag": "not-a-uuid"})
	if err == nil {
		t.Fatal("want error, got nil")
	}
	var rhe rest.ResponseHeaderParamError
	if !errors.As(err, &rhe) {
		t.Fatalf("want ResponseHeaderParamError, got %T: %v", err, err)
	}
	if rhe.Name != "ETag" {
		t.Errorf("want Name=ETag, got %q", rhe.Name)
	}
	if rhe.Value != "not-a-uuid" {
		t.Errorf("want Value='not-a-uuid', got %q", rhe.Value)
	}
}

func TestResponseHeaderParam_nilCodecSkipped(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	h, err := rest.NewRoute[createReq, userResp]("POST", "/users", createReqCodec, userCodec, rest.ResponseHeaderParam{Name: "Location", Description: "URL of created resource", Codec: nil}).Register(b)
	if err != nil {
		t.Fatalf("AddRoute: %v", err)
	}
	// nil codec means no validation — any value is accepted.
	if err := h.ValidateResponseHeaders(map[string]string{"Location": "anything"}); err != nil {
		t.Errorf("want nil for nil-codec param, got %v", err)
	}
}

func TestResponseHeaderParam_schemaFlowsToSpec(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	uuidCodec := codex.String().Refine(validate.UUID)
	_, err := rest.NewRoute[createReq, userResp]("POST", "/users", createReqCodec, userCodec, rest.ResponseHeaderParam{Name: "Location", Description: "URL of created resource", Codec: &uuidCodec}).Register(b)
	if err != nil {
		t.Fatalf("AddRoute: %v", err)
	}
	doc, err := b.OpenAPISpec()
	if err != nil {
		t.Fatalf("OpenAPISpec: %v", err)
	}
	raw, err := doc.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	spec := string(raw)
	if !strings.Contains(spec, `"Location"`) {
		t.Errorf("spec missing response header 'Location': %s", spec)
	}
	if !strings.Contains(spec, `"headers"`) {
		t.Errorf("spec missing 'headers' key in response: %s", spec)
	}
}

func TestResponseCookieParam_validateValid(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	sessionCodec := codex.String().Refine(validate.MinLen(8))
	handle, err := rest.NewRoute[createReq, userResp]("POST", "/users", createReqCodec, userCodec, rest.ResponseCookieParam{Name: "session", Codec: &sessionCodec}).Register(b)
	if err != nil {
		t.Fatalf("AddRoute: %v", err)
	}
	if err := handle.ValidateResponseCookies(map[string]string{"session": "abc12345"}); err != nil {
		t.Errorf("expected valid cookie to pass: %v", err)
	}
}

func TestResponseCookieParam_validateInvalid(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	sessionCodec := codex.String().Refine(validate.MinLen(8))
	handle, err := rest.NewRoute[createReq, userResp]("POST", "/users", createReqCodec, userCodec, rest.ResponseCookieParam{Name: "session", Codec: &sessionCodec}).Register(b)
	if err != nil {
		t.Fatalf("AddRoute: %v", err)
	}
	err = handle.ValidateResponseCookies(map[string]string{"session": "short"})
	if err == nil {
		t.Fatal("expected error for too-short cookie value")
	}
	var rcErr rest.ResponseCookieParamError
	if !errors.As(err, &rcErr) {
		t.Fatalf("expected ResponseCookieParamError, got %T: %v", err, err)
	}
	if rcErr.Name != "session" {
		t.Errorf("want Name=session, got %q", rcErr.Name)
	}
}

func TestResponseCookieParam_nilCodecSkipped(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	handle, err := rest.NewRoute[createReq, userResp]("POST", "/users", createReqCodec, userCodec,
		rest.ResponseCookieParam{Name: "session"}, // no codec
	).Register(b)

	if err != nil {
		t.Fatalf("AddRoute: %v", err)
	}
	if err := handle.ValidateResponseCookies(map[string]string{"session": "anything"}); err != nil {
		t.Errorf("expected nil codec to skip validation: %v", err)
	}
}

func TestResponseCookieParam_schemaFlowsToSpec(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	sessionCodec := codex.String().Refine(validate.MinLen(8))
	_, err := rest.NewRoute[createReq, userResp]("POST", "/users", createReqCodec, userCodec, rest.ResponseCookieParam{Name: "session", Description: "Session token", Codec: &sessionCodec}).Register(b)
	if err != nil {
		t.Fatalf("AddRoute: %v", err)
	}
	doc, err := b.OpenAPISpec()
	if err != nil {
		t.Fatalf("OpenAPISpec: %v", err)
	}
	raw, err := doc.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	spec := string(raw)
	if !strings.Contains(spec, `"Set-Cookie"`) {
		t.Errorf("spec missing Set-Cookie response header: %s", spec)
	}
	if !strings.Contains(spec, `"headers"`) {
		t.Errorf("spec missing 'headers' key in response: %s", spec)
	}
}

// --- Security builder tests ---

func TestSecurityScheme_WithCodec_setsCodec(t *testing.T) {
	c := codex.String().Refine(validate.NonEmptyString)
	s := rest.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")}.WithCodec(c)
	if s.Codec == nil {
		t.Fatal("expected Codec to be non-nil after WithCodec")
	}
}

func TestSecurityScheme_WithCodec_returnsDistinctCopy(t *testing.T) {
	c := codex.String().Refine(validate.NonEmptyString)
	orig := rest.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")}
	updated := orig.WithCodec(c)
	if orig.Codec != nil {
		t.Fatal("WithCodec must not mutate the original")
	}
	if updated.Codec == nil {
		t.Fatal("updated copy must have non-nil Codec")
	}
}

func TestBuilder_AddSecurityScheme_propagatesToRouteHandle(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	c := codex.String().Refine(validate.NonEmptyString)
	b.AddSecurityScheme("bearer", rest.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")}.WithCodec(c))

	handle, err := rest.NewRoute[createReq, userResp]("GET", "/secure", createReqCodec, userCodec).Register(b)
	if err != nil {
		t.Fatalf("AddRoute: %v", err)
	}
	if _, ok := handle.SecuritySchemes["bearer"]; !ok {
		t.Fatal("expected SecuritySchemes to contain 'bearer'")
	}
	if handle.SecuritySchemes["bearer"].Codec == nil {
		t.Fatal("expected Codec to be propagated to RouteHandle")
	}
}

func TestBuilder_AddGlobalSecurity_appearsInOpenAPISpec(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	b.AddSecurityScheme("bearer", rest.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")})
	b.AddGlobalSecurity(route.Require("bearer"))

	_, err := rest.NewRoute[createReq, userResp]("GET", "/secure", createReqCodec, userCodec).Register(b)
	if err != nil {
		t.Fatalf("AddRoute: %v", err)
	}
	doc, err := b.OpenAPISpec()
	if err != nil {
		t.Fatalf("OpenAPISpec: %v", err)
	}
	raw, err := doc.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	spec := string(raw)
	if !strings.Contains(spec, `"bearer"`) {
		t.Errorf("expected OpenAPI spec to contain global security scheme 'bearer'; got:\n%s", spec)
	}
	if !strings.Contains(spec, `"securitySchemes"`) {
		t.Errorf("expected OpenAPI spec to contain securitySchemes; got:\n%s", spec)
	}
}

func TestBuilder_AddGlobalSecurity_populatesRouteHandleGlobalSecurity(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	b.AddGlobalSecurity(route.Require("bearer"))

	handle, err := rest.NewRoute[createReq, userResp]("GET", "/secure", createReqCodec, userCodec).Register(b)
	if err != nil {
		t.Fatalf("AddRoute: %v", err)
	}
	if len(handle.GlobalSecurity) == 0 {
		t.Fatal("expected GlobalSecurity to be populated on RouteHandle")
	}
	req := handle.GlobalSecurity[0]
	if _, ok := req["bearer"]; !ok {
		t.Errorf("expected GlobalSecurity to contain 'bearer' requirement, got %v", req)
	}
}

// --- SSERouteHandle param validation tests ---

func TestSSERouteHandle_ValidateQuery_valid(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	uuidCodec := codex.String().Refine(validate.UUID)
	h, err := rest.NewSSERoute[createReq, sseEvent]("/stream",
		createReqCodec, sseEventCodec,
		rest.QueryParam{Name: "id", Codec: &uuidCodec},
	).Register(b)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.ValidateQuery(map[string]string{"id": "f47ac10b-58cc-4372-a567-0e02b2c3d479"}); err != nil {
		t.Errorf("want nil, got %v", err)
	}
}

func TestSSERouteHandle_ValidateQuery_invalid(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	uuidCodec := codex.String().Refine(validate.UUID)
	h, err := rest.NewSSERoute[createReq, sseEvent]("/stream",
		createReqCodec, sseEventCodec,
		rest.QueryParam{Name: "id", Codec: &uuidCodec},
	).Register(b)
	if err != nil {
		t.Fatal(err)
	}
	err = h.ValidateQuery(map[string]string{"id": "not-a-uuid"})
	var paramErr rest.QueryParamError
	if !errors.As(err, &paramErr) {
		t.Fatalf("want QueryParamError, got %T: %v", err, err)
	}
	if paramErr.Name != "id" {
		t.Errorf("want Name=id, got %q", paramErr.Name)
	}
}

func TestSSERouteHandle_ValidateQueryMulti_valid(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	uuidCodec := codex.String().Refine(validate.UUID)
	h, err := rest.NewSSERoute[createReq, sseEvent]("/stream",
		createReqCodec, sseEventCodec,
		rest.QueryParam{Name: "id", Codec: &uuidCodec},
	).Register(b)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.ValidateQueryMulti(map[string][]string{"id": {"f47ac10b-58cc-4372-a567-0e02b2c3d479"}}); err != nil {
		t.Errorf("want nil, got %v", err)
	}
}

func TestSSERouteHandle_ValidateQueryMulti_invalid(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	uuidCodec := codex.String().Refine(validate.UUID)
	h, err := rest.NewSSERoute[createReq, sseEvent]("/stream",
		createReqCodec, sseEventCodec,
		rest.QueryParam{Name: "id", Codec: &uuidCodec},
	).Register(b)
	if err != nil {
		t.Fatal(err)
	}
	err = h.ValidateQueryMulti(map[string][]string{"id": {"not-a-uuid"}})
	var paramErr rest.QueryParamError
	if !errors.As(err, &paramErr) {
		t.Fatalf("want QueryParamError, got %T: %v", err, err)
	}
}

func TestSSERouteHandle_ValidateCookies_valid(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	tokenCodec := codex.String().Refine(validate.NonEmptyString)
	h, err := rest.NewSSERoute[createReq, sseEvent]("/stream",
		createReqCodec, sseEventCodec,
		rest.CookieParam{Name: "session", Codec: &tokenCodec},
	).Register(b)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.ValidateCookies(map[string]string{"session": "abc123"}); err != nil {
		t.Errorf("want nil, got %v", err)
	}
}

func TestSSERouteHandle_ValidateCookies_invalid(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	tokenCodec := codex.String().Refine(validate.NonEmptyString)
	h, err := rest.NewSSERoute[createReq, sseEvent]("/stream",
		createReqCodec, sseEventCodec,
		rest.CookieParam{Name: "session", Codec: &tokenCodec},
	).Register(b)
	if err != nil {
		t.Fatal(err)
	}
	err = h.ValidateCookies(map[string]string{"session": ""})
	var cookieErr rest.CookieParamError
	if !errors.As(err, &cookieErr) {
		t.Fatalf("want CookieParamError, got %T: %v", err, err)
	}
	if cookieErr.Name != "session" {
		t.Errorf("want Name=session, got %q", cookieErr.Name)
	}
}

func TestSSERouteHandle_ValidateHeaders_valid(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	uuidCodec := codex.String().Refine(validate.UUID)
	h, err := rest.NewSSERoute[createReq, sseEvent]("/stream",
		createReqCodec, sseEventCodec,
		rest.HeaderParam{Name: "X-Request-Id", Codec: &uuidCodec},
	).Register(b)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.ValidateHeaders(map[string]string{"X-Request-Id": "f47ac10b-58cc-4372-a567-0e02b2c3d479"}); err != nil {
		t.Errorf("want nil, got %v", err)
	}
}

func TestSSERouteHandle_ValidateHeaders_invalid(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	uuidCodec := codex.String().Refine(validate.UUID)
	h, err := rest.NewSSERoute[createReq, sseEvent]("/stream",
		createReqCodec, sseEventCodec,
		rest.HeaderParam{Name: "X-Request-Id", Codec: &uuidCodec},
	).Register(b)
	if err != nil {
		t.Fatal(err)
	}
	err = h.ValidateHeaders(map[string]string{"X-Request-Id": "bad"})
	var headerErr rest.HeaderParamError
	if !errors.As(err, &headerErr) {
		t.Fatalf("want HeaderParamError, got %T: %v", err, err)
	}
	if headerErr.Name != "X-Request-Id" {
		t.Errorf("want Name=X-Request-Id, got %q", headerErr.Name)
	}
}

func TestSSERouteHandle_GlobalSecurity_populated(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	b.AddSecurityScheme("bearerAuth", rest.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")})
	b.AddGlobalSecurity(route.Require("bearerAuth"))

	h, err := rest.NewSSERoute[createReq, sseEvent]("/stream",
		createReqCodec, sseEventCodec,
	).Register(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.GlobalSecurity) == 0 {
		t.Fatal("want GlobalSecurity populated on SSERouteHandle")
	}
	if _, ok := h.GlobalSecurity[0]["bearerAuth"]; !ok {
		t.Errorf("want bearerAuth in GlobalSecurity, got %v", h.GlobalSecurity[0])
	}
	if _, ok := h.SecuritySchemes["bearerAuth"]; !ok {
		t.Error("want SecuritySchemes populated on SSERouteHandle")
	}
}

func TestSSERouteHandle_ValidateResponseHeaders_valid(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	strCodec := codex.String()

	h, err := rest.NewSSERoute[createReq, sseEvent]("/stream",
		createReqCodec, sseEventCodec,
		rest.ResponseHeaderParam{Name: "X-Correlation-Id", Codec: &strCodec},
	).Register(b)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.ValidateResponseHeaders(map[string]string{"X-Correlation-Id": "abc-123"}); err != nil {
		t.Errorf("want nil, got %v", err)
	}
}

func TestSSERouteHandle_ValidateResponseHeaders_invalid(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	// Use a string codec with a length constraint so we can trigger a validation error.
	strCodec := codex.String().Refine(codex.Constraint[string]{
		Name:  "maxLen3",
		Check: func(s string) bool { return len(s) <= 3 },
		Message: func(s string) string {
			return fmt.Sprintf("value %q exceeds max length 3", s)
		},
	})

	h, err := rest.NewSSERoute[createReq, sseEvent]("/stream2",
		createReqCodec, sseEventCodec,
		rest.ResponseHeaderParam{Name: "X-Short", Codec: &strCodec},
	).Register(b)
	if err != nil {
		t.Fatal(err)
	}
	err = h.ValidateResponseHeaders(map[string]string{"X-Short": "toolongvalue"})
	if err == nil {
		t.Fatal("want error for invalid response header, got nil")
	}
	var rhe rest.ResponseHeaderParamError
	if !errors.As(err, &rhe) {
		t.Fatalf("want ResponseHeaderParamError, got %T: %v", err, err)
	}
	if rhe.Name != "X-Short" {
		t.Errorf("want Name=X-Short, got %q", rhe.Name)
	}
}

func TestSSERouteHandle_responseHeaderParams_populated(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	strCodec := codex.String()

	h, err := rest.NewSSERoute[createReq, sseEvent]("/stream3",
		createReqCodec, sseEventCodec,
		rest.ResponseHeaderParam{Name: "X-Trace-Id", Codec: &strCodec},
	).Register(b)
	if err != nil {
		t.Fatal(err)
	}
	// nil response header map → no error (nothing to validate)
	if err := h.ValidateResponseHeaders(nil); err != nil {
		t.Errorf("want nil for empty map, got %v", err)
	}
}

// --- G1: ValidatePathParams ---

func TestRouteHandle_ValidatePathParams_valid(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	uuidCodec := codex.String().Refine(validate.UUID)
	h, err := rest.NewRoute[createReq, userResp]("GET", "/users/{id}", createReqCodec, userCodec,
		rest.PathParam{Name: "id", Codec: &uuidCodec},
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := h.ValidatePathParams(map[string]string{"id": "550e8400-e29b-41d4-a716-446655440000"}); err != nil {
		t.Errorf("want nil, got %v", err)
	}
}

func TestRouteHandle_ValidatePathParams_invalid(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	uuidCodec := codex.String().Refine(validate.UUID)
	h, err := rest.NewRoute[createReq, userResp]("GET", "/users/{id}", createReqCodec, userCodec,
		rest.PathParam{Name: "id", Codec: &uuidCodec},
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	err = h.ValidatePathParams(map[string]string{"id": "not-a-uuid"})
	var pathErr rest.PathParamError
	if !errors.As(err, &pathErr) {
		t.Fatalf("want PathParamError, got %T: %v", err, err)
	}
	if pathErr.Name != "id" {
		t.Errorf("want Name=id, got %q", pathErr.Name)
	}
}

func TestSSERouteHandle_ValidatePathParams_valid(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	uuidCodec := codex.String().Refine(validate.UUID)
	h, err := rest.NewSSERoute[createReq, sseEvent]("/stream/{id}",
		createReqCodec, sseEventCodec,
		rest.PathParam{Name: "id", Codec: &uuidCodec},
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := h.ValidatePathParams(map[string]string{"id": "550e8400-e29b-41d4-a716-446655440000"}); err != nil {
		t.Errorf("want nil, got %v", err)
	}
}

func TestSSERouteHandle_ValidatePathParams_invalid(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	uuidCodec := codex.String().Refine(validate.UUID)
	h, err := rest.NewSSERoute[createReq, sseEvent]("/stream/{id}",
		createReqCodec, sseEventCodec,
		rest.PathParam{Name: "id", Codec: &uuidCodec},
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	err = h.ValidatePathParams(map[string]string{"id": "bad"})
	var pathErr rest.PathParamError
	if !errors.As(err, &pathErr) {
		t.Fatalf("want PathParamError, got %T: %v", err, err)
	}
}

// --- G2: Required enforcement ---

func TestValidateQuery_required_missing_returnsError(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	pageCodec := codex.String()
	h, err := rest.NewRoute[createReq, userResp]("GET", "/items", createReqCodec, userCodec,
		rest.QueryParam{Name: "page", Required: true, Codec: &pageCodec},
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := h.ValidateQuery(map[string]string{}); !errors.Is(err, rest.ErrRequiredParam) {
		t.Fatalf("want ErrRequiredParam, got %v", err)
	}
}

func TestValidateQuery_notRequired_missing_skipped(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	pageCodec := codex.String()
	h, err := rest.NewRoute[createReq, userResp]("GET", "/items", createReqCodec, userCodec,
		rest.QueryParam{Name: "page", Required: false, Codec: &pageCodec},
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := h.ValidateQuery(map[string]string{}); err != nil {
		t.Errorf("want nil for optional missing param, got %v", err)
	}
}

func TestValidateHeaders_required_missing_returnsError(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	uuidCodec := codex.String().Refine(validate.UUID)
	h, err := rest.NewRoute[createReq, userResp]("POST", "/items", createReqCodec, userCodec,
		rest.HeaderParam{Name: "X-Request-Id", Required: true, Codec: &uuidCodec},
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := h.ValidateHeaders(map[string]string{}); !errors.Is(err, rest.ErrRequiredParam) {
		t.Fatalf("want ErrRequiredParam, got %v", err)
	}
}

// --- G4: ValidateResponseCookies on SSERouteHandle ---

func TestSSERouteHandle_ValidateResponseCookies_valid(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	strCodec := codex.String()
	h, err := rest.NewSSERoute[createReq, sseEvent]("/stream-cookies",
		createReqCodec, sseEventCodec,
		rest.ResponseCookieParam{Name: "X-Session", Codec: &strCodec},
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := h.ValidateResponseCookies(map[string]string{"X-Session": "token123"}); err != nil {
		t.Errorf("want nil, got %v", err)
	}
}

func TestSSERouteHandle_ValidateResponseCookies_invalid(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	nonEmpty := codex.String().Refine(validate.NonEmptyString)
	h, err := rest.NewSSERoute[createReq, sseEvent]("/stream-cookies2",
		createReqCodec, sseEventCodec,
		rest.ResponseCookieParam{Name: "X-Session", Codec: &nonEmpty},
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	err = h.ValidateResponseCookies(map[string]string{"X-Session": ""})
	var rce rest.ResponseCookieParamError
	if !errors.As(err, &rce) {
		t.Fatalf("want ResponseCookieParamError, got %T: %v", err, err)
	}
	if rce.Name != "X-Session" {
		t.Errorf("want Name=X-Session, got %q", rce.Name)
	}
}

// --- G6: SSERouteHandle.BuildPath ---

func TestSSERouteHandle_BuildPath_noParams(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	h, err := rest.NewSSERoute[createReq, sseEvent]("/stream",
		createReqCodec, sseEventCodec,
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	got, err := h.BuildPath(nil)
	if err != nil {
		t.Fatalf("BuildPath: %v", err)
	}
	if got != "/stream" {
		t.Errorf("want /stream, got %q", got)
	}
}

func TestSSERouteHandle_BuildPath_withPathParam(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	uuidCodec := codex.String().Refine(validate.UUID)
	h, err := rest.NewSSERoute[createReq, sseEvent]("/stream/{id}",
		createReqCodec, sseEventCodec,
		rest.PathParam{Name: "id", Codec: &uuidCodec},
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	got, err := h.BuildPath(map[string]string{"id": "550e8400-e29b-41d4-a716-446655440000"})
	if err != nil {
		t.Fatalf("BuildPath: %v", err)
	}
	if got != "/stream/550e8400-e29b-41d4-a716-446655440000" {
		t.Errorf("want /stream/550e8400..., got %q", got)
	}
}

func TestSSERouteHandle_BuildPath_invalidParam_returnsError(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	uuidCodec := codex.String().Refine(validate.UUID)
	h, err := rest.NewSSERoute[createReq, sseEvent]("/stream/{id}",
		createReqCodec, sseEventCodec,
		rest.PathParam{Name: "id", Codec: &uuidCodec},
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	_, err = h.BuildPath(map[string]string{"id": "not-a-uuid"})
	var pathErr rest.PathParamError
	if !errors.As(err, &pathErr) {
		t.Fatalf("want PathParamError, got %T: %v", err, err)
	}
}

type sseMergedEvent struct {
	PathID string
	Since  string
	Tenant string
	Trace  string
}

var sseMergedEventCodec = codex.Struct[sseMergedEvent](
	codex.OptionalField("path_id", codex.String(), func(e sseMergedEvent) string { return e.PathID }, func(e *sseMergedEvent, v string) { e.PathID = v }),
	codex.OptionalField("since", codex.String(), func(e sseMergedEvent) string { return e.Since }, func(e *sseMergedEvent, v string) { e.Since = v }),
	codex.OptionalField("tenant", codex.String(), func(e sseMergedEvent) string { return e.Tenant }, func(e *sseMergedEvent, v string) { e.Tenant = v }),
	codex.OptionalField("trace", codex.String(), func(e sseMergedEvent) string { return e.Trace }, func(e *sseMergedEvent, v string) { e.Trace = v }),
)

func TestSSERouteHandle_MergeEvent_HappyPath(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	str := codex.String().Refine(validate.NonEmptyString)
	h, err := rest.NewSSERoute[createReq, sseMergedEvent]("/stream/{path_id}",
		createReqCodec, sseMergedEventCodec,
		rest.NewRequiredSSEEventParam("path_id", str, func(e sseMergedEvent) string { return e.PathID }, func(e *sseMergedEvent, v string) { e.PathID = v }),
		rest.NewOptionalSSEEventParam("since", str, func(e sseMergedEvent) string { return e.Since }, func(e *sseMergedEvent, v string) { e.Since = v }),
		rest.NewOptionalSSEEventParam("tenant", str, func(e sseMergedEvent) string { return e.Tenant }, func(e *sseMergedEvent, v string) { e.Tenant = v }),
		rest.NewOptionalSSEEventParam("trace", str, func(e sseMergedEvent) string { return e.Trace }, func(e *sseMergedEvent, v string) { e.Trace = v }),
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	got, err := h.MergeEvent(sseMergedEvent{},
		map[string]string{"path_id": "machine-1"},
		map[string]string{"since": "now"},
		map[string]string{"tenant": "acme"},
		map[string]string{"trace": "t-1"},
	)
	if err != nil {
		t.Fatalf("MergeEvent: %v", err)
	}
	if got.PathID != "machine-1" || got.Since != "now" || got.Tenant != "acme" || got.Trace != "t-1" {
		t.Fatalf("merged event mismatch: %+v", got)
	}
}

func TestSSERouteHandle_MergeEvent_MissingRequired(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	str := codex.String().Refine(validate.NonEmptyString)
	h, err := rest.NewSSERoute[createReq, sseMergedEvent]("/stream/{path_id}",
		createReqCodec, sseMergedEventCodec,
		rest.NewRequiredSSEEventParam("path_id", str, func(e sseMergedEvent) string { return e.PathID }, func(e *sseMergedEvent, v string) { e.PathID = v }),
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	_, err = h.MergeEvent(sseMergedEvent{}, nil, nil, nil, nil)
	if err == nil {
		t.Fatal("want error")
	}
	var ve codex.ValidationErrors
	if !errors.As(err, &ve) {
		t.Fatalf("want ValidationErrors, got %T: %v", err, err)
	}
}

func TestNewRequiredSSEEventParam_WithDescription(t *testing.T) {
	p := rest.NewRequiredSSEEventParam("trace", codex.String(),
		func(e sseMergedEvent) string { return e.Trace },
		func(e *sseMergedEvent, v string) { e.Trace = v },
	).WithDescription("trace id")
	if p.Description != "trace id" {
		t.Fatalf("want description set, got %q", p.Description)
	}
}

func TestPathParam_WithCodec_setsCodecWithoutAddressOf(t *testing.T) {
	uuidCodec := codex.String().Refine(validate.UUID)
	p := rest.PathParam{Name: "id"}.WithCodec(uuidCodec)
	if p.Codec == nil {
		t.Fatal("expected Codec to be non-nil after WithCodec")
	}
	if err := p.Codec.Validate("550e8400-e29b-41d4-a716-446655440000"); err != nil {
		t.Errorf("expected valid UUID to pass: %v", err)
	}
}

func TestPathParam_WithCodec_returnsDistinctCopy(t *testing.T) {
	uuidCodec := codex.String().Refine(validate.UUID)
	original := rest.PathParam{Name: "id"}
	updated := original.WithCodec(uuidCodec)
	if original.Codec != nil {
		t.Error("original PathParam must not be mutated")
	}
	if updated.Codec == nil {
		t.Fatal("updated PathParam must have Codec set")
	}
}

func TestQueryParam_WithCodec_setsCodecWithoutAddressOf(t *testing.T) {
	nonEmptyCodec := codex.String().Refine(validate.NonEmptyString)
	p := rest.QueryParam{Name: "q"}.WithCodec(nonEmptyCodec)
	if p.Codec == nil {
		t.Fatal("expected Codec to be non-nil after WithCodec")
	}
}

func TestCookieParam_WithCodec_setsCodecWithoutAddressOf(t *testing.T) {
	c := rest.CookieParam{Name: "session"}.WithCodec(codex.String())
	if c.Codec == nil {
		t.Fatal("expected Codec to be non-nil after WithCodec")
	}
}

func TestHeaderParam_WithCodec_setsCodecWithoutAddressOf(t *testing.T) {
	h := rest.HeaderParam{Name: "X-Trace-Id"}.WithCodec(codex.String())
	if h.Codec == nil {
		t.Fatal("expected Codec to be non-nil after WithCodec")
	}
}

func TestResponseHeaderParam_WithCodec_setsCodecWithoutAddressOf(t *testing.T) {
	rh := rest.ResponseHeaderParam{Name: "X-Request-Id"}.WithCodec(codex.String())
	if rh.Codec == nil {
		t.Fatal("expected Codec to be non-nil after WithCodec")
	}
}

func TestResponseCookieParam_WithCodec_setsCodecWithoutAddressOf(t *testing.T) {
	rc := rest.ResponseCookieParam{Name: "token"}.WithCodec(codex.String())
	if rc.Codec == nil {
		t.Fatal("expected Codec to be non-nil after WithCodec")
	}
}

// --- EncodeRequest / DecodeResponse (client-side codec helpers) ---

func TestRouteHandle_EncodeRequest_roundTrip(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	h, err := rest.NewRoute[createReq, userResp]("POST", "/users",
		createReqCodec, userCodec).Register(b)
	if err != nil {
		t.Fatal(err)
	}
	if h.EncodeRequest == nil {
		t.Fatal("EncodeRequest must not be nil after Register")
	}
	body, err := h.EncodeRequest(createReq{Name: "Alice"})
	if err != nil {
		t.Fatalf("EncodeRequest error: %v", err)
	}
	if len(body) == 0 {
		t.Error("EncodeRequest returned empty body")
	}
	// Decode must round-trip through the same codec.
	got, err := h.Decode(body)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	if got.Name != "Alice" {
		t.Errorf("round-trip name = %q, want 'Alice'", got.Name)
	}
}

func TestRouteHandle_DecodeResponse_roundTrip(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	h, err := rest.NewRoute[createReq, userResp]("POST", "/users",
		createReqCodec, userCodec).Register(b)
	if err != nil {
		t.Fatal(err)
	}
	if h.DecodeResponse == nil {
		t.Fatal("DecodeResponse must not be nil after Register")
	}
	// Encode a response and decode it back.
	body, err := h.Encode(userResp{ID: "1", Name: "Alice"})
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}
	got, err := h.DecodeResponse(body)
	if err != nil {
		t.Fatalf("DecodeResponse error: %v", err)
	}
	if got.ID != "1" || got.Name != "Alice" {
		t.Errorf("round-trip = %+v, want {ID:1 Name:Alice}", got)
	}
}

// --- Route.ClientHandle ---

func TestRoute_ClientHandle_returnsHandle(t *testing.T) {
	route := rest.NewRoute[createReq, userResp]("POST", "/items",
		createReqCodec, userCodec)
	h := route.ClientHandle()
	if h == nil {
		t.Fatal("ClientHandle returned nil")
	}
	if h.Descriptor.Method != "POST" || h.Descriptor.Path != "/items" {
		t.Errorf("descriptor = %+v", h.Descriptor)
	}
	if h.EncodeRequest == nil {
		t.Error("EncodeRequest must not be nil")
	}
	if h.DecodeResponse == nil {
		t.Error("DecodeResponse must not be nil")
	}
}

func TestRoute_ClientHandle_notRegisteredWithBuilder(t *testing.T) {
	// ClientHandle must not add the route to any builder; verifiable by
	// calling it without constructing a builder at all.
	route := rest.NewRoute[createReq, userResp]("GET", "/items/{id}",
		createReqCodec, userCodec,
		rest.PathParam{Name: "id"}.WithCodec(codex.String()),
	)
	h := route.ClientHandle()
	// BuildPath should still work.
	path, err := h.BuildPath(map[string]string{"id": "42"})
	if err != nil {
		t.Fatalf("BuildPath error: %v", err)
	}
	if path != "/items/42" {
		t.Errorf("path = %q, want '/items/42'", path)
	}
}

func TestRoute_ClientHandle_encodeDecodeRoundTrip(t *testing.T) {
	route := rest.NewRoute[createReq, userResp]("POST", "/items",
		createReqCodec, userCodec)
	h := route.ClientHandle()

	// Encode a request, decode it back.
	body, err := h.EncodeRequest(createReq{Name: "Widget"})
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}
	got, err := h.Decode(body)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got.Name != "Widget" {
		t.Errorf("name = %q, want 'Widget'", got.Name)
	}
}

// --- Example functions (shown on pkg.go.dev as runnable snippets) ---

func ExampleNewRoute() {
	type CreateUserReq struct{ Name string }
	type User struct{ ID, Name string }

	reqCodec := codex.Struct[CreateUserReq](
		codex.RequiredField("name", codex.String(),
			func(r CreateUserReq) string { return r.Name },
			func(r *CreateUserReq, v string) { r.Name = v },
		),
	)
	userCodec := codex.Struct[User](
		codex.RequiredField("id", codex.String(),
			func(u User) string { return u.ID },
			func(u *User, v string) { u.ID = v },
		),
		codex.RequiredField("name", codex.String(),
			func(u User) string { return u.Name },
			func(u *User, v string) { u.Name = v },
		),
	)

	b := rest.NewBuilder(rest.Info{Title: "User API", Version: "1.0.0"})

	// NewRoute declares a typed route as a value — no builder coupling.
	route := rest.NewRoute[CreateUserReq, User]("POST", "/users",
		reqCodec, userCodec,
		rest.RouteMeta{OperationID: "createUser", Summary: "Create a user"},
	)

	handle, err := route.Register(b)
	if err != nil {
		fmt.Println("register error:", err)
		return
	}

	// Decode a request body.
	req, err := handle.Decode([]byte(`{"name":"Alice"}`))
	if err != nil {
		fmt.Println("decode error:", err)
		return
	}
	fmt.Println(req.Name)
	// Output: Alice
}

// ── RequestFormats / Formats RouteOpt ─────────────────────────────────────────

var pngCodec = codex.Bytes().Refine(validate.PNG)

// TestRequestFormats_AppliesInline verifies rest.RequestFormats declared
// inline in NewRoute's opts is equivalent to calling WithRequestFormats
// after Register.
func TestRequestFormats_AppliesInline(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	route := rest.NewRoute[[]byte, userResp]("PUT", "/images/{id}", pngCodec, userCodec,
		rest.RequestFormats(format.Binary(pngCodec).WithContentType("image/png")),
	)
	handle, err := route.Register(b)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if len(handle.RequestFormats) != 1 || handle.RequestFormats[0].ContentType() != "image/png" {
		t.Errorf("want 1 RequestFormats entry with image/png, got %+v", handle.RequestFormats)
	}
}

// TestFormats_AppliesInline verifies rest.Formats declared inline is
// equivalent to calling WithFormats after Register.
func TestFormats_AppliesInline(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	route := rest.NewRoute[createReq, []byte]("GET", "/images/{id}", createReqCodec, pngCodec,
		rest.Formats(format.Binary(pngCodec).WithContentType("image/png")),
	)
	handle, err := route.Register(b)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if len(handle.Formats) != 1 || handle.Formats[0].ContentType() != "image/png" {
		t.Errorf("want 1 Formats entry with image/png, got %+v", handle.Formats)
	}
}

// TestRequestFormats_TypeMismatch verifies a wrong-typed RequestFormats
// option returns FormatOptError, reachable via errors.As, with a structured
// LogValue.
func TestRequestFormats_TypeMismatch(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	// Route's request type is createReq, but RequestFormats is declared
	// for []byte — a caller mistake only detectable once Req is concrete.
	route := rest.NewRoute[createReq, userResp]("POST", "/users", createReqCodec, userCodec,
		rest.RequestFormats(format.Binary(pngCodec)),
	)
	_, err := route.Register(b)
	var fe rest.FormatOptError
	if !errors.As(err, &fe) || fe.Direction != "request" {
		t.Fatalf("want FormatOptError{request}, got %v", err)
	}
	if fe.Unwrap() == nil {
		t.Error("want non-nil Unwrap")
	}
	v := fe.LogValue()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("want KindGroup, got %v", v.Kind())
	}
	keys := map[string]bool{}
	for _, a := range v.Group() {
		keys[a.Key] = true
	}
	for _, want := range []string{"direction", "err"} {
		if !keys[want] {
			t.Errorf("missing LogValue key %q", want)
		}
	}
}

// TestFormats_TypeMismatch mirrors TestRequestFormats_TypeMismatch for the
// response direction.
func TestFormats_TypeMismatch(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	route := rest.NewRoute[createReq, userResp]("POST", "/users", createReqCodec, userCodec,
		rest.Formats(format.Binary(pngCodec)),
	)
	_, err := route.Register(b)
	var fe rest.FormatOptError
	if !errors.As(err, &fe) || fe.Direction != "response" {
		t.Fatalf("want FormatOptError{response}, got %v", err)
	}
}

// ── NewPathParam / NewRequiredQueryParam / NewOptionalQueryParam / MergeFields / DecodeMerged ──

type getUserReq struct {
	ID     string
	Filter string
}

var getUserRespCodec = codex.Struct[userResp](
	codex.OptionalField("id", codex.String(),
		func(u userResp) string { return u.ID },
		func(u *userResp, v string) { u.ID = v },
	),
)

// P1: rest.NewPathParam registers both spec Param and merge field.
func TestNewPathParam_RegistersSpecAndMergeField(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	h, err := rest.NewRoute[getUserReq, userResp]("GET", "/users/{id}", codex.Struct[getUserReq](), getUserRespCodec,
		rest.NewPathParam("id", codex.String().Refine(validate.NonEmptyString),
			func(r getUserReq) string { return r.ID },
			func(r *getUserReq, v string) { r.ID = v }),
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if len(h.Descriptor.PathParams) != 1 || h.Descriptor.PathParams[0].Name != "id" {
		t.Fatalf("Descriptor.PathParams: unexpected %+v", h.Descriptor.PathParams)
	}
	if len(h.MergeFields()) != 1 {
		t.Fatalf("MergeFields: want 1 field, got %d", len(h.MergeFields()))
	}
}

// G4-1: rest.NewRequiredQueryParam registers a Required:true spec Param and a
// merge field — the merge-field-constructor counterpart to
// NewOptionalQueryParam's coverage (which was already exercised elsewhere).
func TestNewRequiredQueryParam_RegistersSpecAndMergeField(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	h, err := rest.NewRoute[getUserReq, userResp]("GET", "/users", codex.Struct[getUserReq](), getUserRespCodec,
		rest.NewRequiredQueryParam("filter", codex.String(),
			func(r getUserReq) string { return r.Filter },
			func(r *getUserReq, v string) { r.Filter = v }),
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if len(h.Descriptor.QueryParams) != 1 || h.Descriptor.QueryParams[0].Name != "filter" || !h.Descriptor.QueryParams[0].Required {
		t.Fatalf("Descriptor.QueryParams: unexpected %+v", h.Descriptor.QueryParams)
	}
	if len(h.MergeFields()) != 1 {
		t.Fatalf("MergeFields: want 1 field, got %d", len(h.MergeFields()))
	}
}

// G4-2: DecodeMerged returns a ValidationErrors when a NewRequiredQueryParam
// var is missing — mirrors TestDecodeMerged_MergeFailure's path-param case
// for the query-param merge-field constructor.
func TestDecodeMerged_RequiredQueryParam_MissingReturnsError(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	h, err := rest.NewRoute[getUserReq, userResp]("GET", "/users", codex.Struct[getUserReq](), getUserRespCodec,
		rest.NewRequiredQueryParam("filter", codex.String(),
			func(r getUserReq) string { return r.Filter },
			func(r *getUserReq, v string) { r.Filter = v }),
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	_, err = h.DecodeMerged(nil, nil, map[string]string{}, nil, nil)
	if err == nil {
		t.Fatal("expected error for missing required query var")
	}
	var ve codex.ValidationErrors
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationErrors, got %T: %v", err, err)
	}
}

// G4-3: rest.NewRequiredCookieParam registers a Required:true spec Param and
// a merge field — the merge-field-constructor counterpart to
// NewOptionalCookieParam's coverage.
func TestNewRequiredCookieParam_RegistersSpecAndMergeField(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	h, err := rest.NewRoute[getUserReq, userResp]("GET", "/users", codex.Struct[getUserReq](), getUserRespCodec,
		rest.NewRequiredCookieParam("session", codex.String(),
			func(r getUserReq) string { return r.Filter },
			func(r *getUserReq, v string) { r.Filter = v }),
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if len(h.Descriptor.CookieParams) != 1 || h.Descriptor.CookieParams[0].Name != "session" || !h.Descriptor.CookieParams[0].Required {
		t.Fatalf("Descriptor.CookieParams: unexpected %+v", h.Descriptor.CookieParams)
	}
	if len(h.MergeFields()) != 1 {
		t.Fatalf("MergeFields: want 1 field, got %d", len(h.MergeFields()))
	}
}

// G4-4: DecodeMerged returns a ValidationErrors when a NewRequiredCookieParam
// var is missing — mirrors TestDecodeMerged_MergeFailure's path-param case
// for the cookie-param merge-field constructor.
func TestDecodeMerged_RequiredCookieParam_MissingReturnsError(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	h, err := rest.NewRoute[getUserReq, userResp]("GET", "/users", codex.Struct[getUserReq](), getUserRespCodec,
		rest.NewRequiredCookieParam("session", codex.String(),
			func(r getUserReq) string { return r.Filter },
			func(r *getUserReq, v string) { r.Filter = v }),
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	_, err = h.DecodeMerged(nil, nil, nil, nil, map[string]string{})
	if err == nil {
		t.Fatal("expected error for missing required cookie var")
	}
	var ve codex.ValidationErrors
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationErrors, got %T: %v", err, err)
	}
}

// P2: RouteHandle.DecodeMerged happy path.
func TestDecodeMerged_HappyPath(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	h, err := rest.NewRoute[getUserReq, userResp]("GET", "/users/{id}", codex.Struct[getUserReq](), getUserRespCodec,
		rest.NewPathParam("id", codex.String().Refine(validate.NonEmptyString),
			func(r getUserReq) string { return r.ID },
			func(r *getUserReq, v string) { r.ID = v }),
		rest.NewOptionalQueryParam("filter", codex.String(),
			func(r getUserReq) string { return r.Filter },
			func(r *getUserReq, v string) { r.Filter = v }),
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	req, err := h.DecodeMerged(nil,
		map[string]string{"id": "abc-123"},
		map[string]string{"filter": "active"},
		nil, nil,
	)
	if err != nil {
		t.Fatalf("DecodeMerged: %v", err)
	}
	if req.ID != "abc-123" || req.Filter != "active" {
		t.Errorf("unexpected merged req: %+v", req)
	}
}

// P3: RouteHandle.DecodeMerged merge failure.
func TestDecodeMerged_MergeFailure(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	h, err := rest.NewRoute[getUserReq, userResp]("GET", "/users/{id}", codex.Struct[getUserReq](), getUserRespCodec,
		rest.NewPathParam("id", codex.String().Refine(validate.NonEmptyString),
			func(r getUserReq) string { return r.ID },
			func(r *getUserReq, v string) { r.ID = v }),
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	_, err = h.DecodeMerged(nil, map[string]string{}, nil, nil, nil)
	if err == nil {
		t.Fatal("expected error for missing required path var")
	}
	var ve codex.ValidationErrors
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationErrors, got %T: %v", err, err)
	}
}

// P4: RouteHandle.DecodeMerged with zero merge fields behaves like plain Decode.
func TestDecodeMerged_NoMergeFieldsIsNoop(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	h, err := rest.NewRoute[createReq, userResp]("POST", "/users", createReqCodec, userCodec).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	body := []byte(`{"name":"Alice"}`)
	viaDecode, err := h.Decode(body)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	viaMerged, err := h.DecodeMerged(body, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("DecodeMerged: %v", err)
	}
	if viaDecode != viaMerged {
		t.Errorf("DecodeMerged should match plain Decode when no merge fields declared: %+v vs %+v", viaDecode, viaMerged)
	}
}

// P8: codex.FieldCodec[T] export — confirms RequiredField remains assignable
// and existing Struct callers are unaffected by the rename.
func TestFieldCodecExport_CompileTimeCompat(t *testing.T) {
	var _ codex.FieldCodec[getUserReq] = codex.RequiredField("id", codex.String(),
		func(r getUserReq) string { return r.ID },
		func(r *getUserReq, v string) { r.ID = v })
}

// P9: MergedPathParam.WithDescription sets the PARAM-level description,
// distinct from the codec's schema-level description.
func TestNewPathParam_WithDescription(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	h, err := rest.NewRoute[getUserReq, userResp]("GET", "/users/{id}", codex.Struct[getUserReq](), getUserRespCodec,
		rest.NewPathParam("id",
			codex.String().Refine(validate.NonEmptyString).WithDescription("schema-level"),
			func(r getUserReq) string { return r.ID },
			func(r *getUserReq, v string) { r.ID = v },
		).WithDescription("param-level"),
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if len(h.Descriptor.PathParams) != 1 {
		t.Fatalf("Descriptor.PathParams: unexpected %+v", h.Descriptor.PathParams)
	}
	p := h.Descriptor.PathParams[0]
	if p.Description != "param-level" {
		t.Errorf("PathParam.Description: want %q, got %q", "param-level", p.Description)
	}
	if p.Schema.Description != "schema-level" {
		t.Errorf("PathParam.Schema.Description: want %q, got %q", "schema-level", p.Schema.Description)
	}
}

// P10: mixing NewPathParam (merge-capable) and plain PathParam
// (validate-only) on the same route.
func TestMixedMergeAndValidateOnlyParams(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	h, err := rest.NewRoute[getUserReq, userResp]("GET", "/users/{id}/{trace}", codex.Struct[getUserReq](), getUserRespCodec,
		rest.NewPathParam("id", codex.String().Refine(validate.NonEmptyString),
			func(r getUserReq) string { return r.ID },
			func(r *getUserReq, v string) { r.ID = v }),
		rest.PathParam{Name: "trace"}.WithCodec(codex.String()), // validate-only, no merge
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if len(h.Descriptor.PathParams) != 2 {
		t.Fatalf("Descriptor.PathParams: want 2, got %d: %+v", len(h.Descriptor.PathParams), h.Descriptor.PathParams)
	}
	if len(h.MergeFields()) != 1 {
		t.Fatalf("MergeFields: want 1 (only the NewPathParam one), got %d", len(h.MergeFields()))
	}
	req, err := h.DecodeMerged(nil, map[string]string{"id": "abc", "trace": "xyz"}, nil, nil, nil)
	if err != nil {
		t.Fatalf("DecodeMerged: %v", err)
	}
	if req.ID != "abc" {
		t.Errorf("req.ID: want %q, got %q", "abc", req.ID)
	}
}

// P11: role-specific merge-field accessors each return only their own
// subset, and MergeFields() aggregates all four.
func TestRoleSpecificMergeFields_ReturnOnlyOwnRole(t *testing.T) {
	type req struct {
		ID     string
		Filter string
		Auth   string
		Sess   string
	}
	respCodec := codex.Struct[userResp](
		codex.OptionalField("id", codex.String(),
			func(u userResp) string { return u.ID },
			func(u *userResp, v string) { u.ID = v },
		),
	)
	b := rest.NewBuilder(testInfo)
	h, err := rest.NewRoute[req, userResp]("GET", "/users/{id}", codex.Struct[req](), respCodec,
		rest.NewPathParam("id", codex.String(),
			func(r req) string { return r.ID },
			func(r *req, v string) { r.ID = v }),
		rest.NewOptionalQueryParam("filter", codex.String(),
			func(r req) string { return r.Filter },
			func(r *req, v string) { r.Filter = v }),
		rest.NewRequiredHeaderParam("Authorization", codex.String(),
			func(r req) string { return r.Auth },
			func(r *req, v string) { r.Auth = v }),
		rest.NewOptionalCookieParam("session", codex.String(),
			func(r req) string { return r.Sess },
			func(r *req, v string) { r.Sess = v }),
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if len(h.PathMergeFields()) != 1 {
		t.Errorf("PathMergeFields: want 1, got %d", len(h.PathMergeFields()))
	}
	if len(h.QueryMergeFields()) != 1 {
		t.Errorf("QueryMergeFields: want 1, got %d", len(h.QueryMergeFields()))
	}
	if len(h.HeaderMergeFields()) != 1 {
		t.Errorf("HeaderMergeFields: want 1, got %d", len(h.HeaderMergeFields()))
	}
	if len(h.CookieMergeFields()) != 1 {
		t.Errorf("CookieMergeFields: want 1, got %d", len(h.CookieMergeFields()))
	}
	if len(h.MergeFields()) != 4 {
		t.Errorf("MergeFields (aggregate): want 4, got %d", len(h.MergeFields()))
	}
}

// P12: client-side round-trip — a route with BOTH a path merge field AND a
// query merge field encodes each role independently via codex.EncodeVars
// and nethttp.Call routes them to the correct HTTP location, with no
// cross-role leakage (the historical bug this role-aware split fixes).
func TestClientEncode_RoleAwareMergeFields_NoLeakage(t *testing.T) {
	type req struct {
		ID     string
		Filter string
	}
	respCodec := codex.Struct[userResp](
		codex.OptionalField("id", codex.String(),
			func(u userResp) string { return u.ID },
			func(u *userResp, v string) { u.ID = v },
		),
	)
	b := rest.NewBuilder(testInfo)
	h, err := rest.NewRoute[req, userResp]("GET", "/users/{id}", codex.Struct[req](), respCodec,
		rest.NewPathParam("id", codex.String(),
			func(r req) string { return r.ID },
			func(r *req, v string) { r.ID = v }),
		rest.NewOptionalQueryParam("filter", codex.String(),
			func(r req) string { return r.Filter },
			func(r *req, v string) { r.Filter = v }),
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	var gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.Query().Get("filter")
		if r.URL.Query().Get("id") != "" {
			t.Errorf("path value %q leaked into the query string", r.URL.Query().Get("id"))
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"abc-123"}`))
	}))
	defer srv.Close()

	item := req{ID: "abc-123", Filter: "active"}
	pathVars, err := codex.EncodeVars(item, h.PathMergeFields()...)
	if err != nil {
		t.Fatalf("EncodeVars(path): %v", err)
	}
	query, err := codex.EncodeVars(item, h.QueryMergeFields()...)
	if err != nil {
		t.Fatalf("EncodeVars(query): %v", err)
	}

	_, err = nethttp.Call(t.Context(), srv.Client(), srv.URL, h, item, pathVars,
		nethttp.CallOptions{QueryParams: query})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if gotPath != "/users/abc-123" {
		t.Errorf("path: want %q, got %q", "/users/abc-123", gotPath)
	}
	if gotQuery != "active" {
		t.Errorf("query filter: want %q, got %q", "active", gotQuery)
	}
}

// ── Response merge fields (Round 3) ──────────────────────────────────────

// userRespWithMeta carries response header/cookie merge fields alongside
// the JSON body fields, for the response-merge test matrix (R1-R5).
type userRespWithMeta struct {
	ID        string
	Name      string
	RequestID string // response header
	Session   string // response cookie
}

var userRespWithMetaBodyCodec = codex.Struct[userRespWithMeta](
	codex.RequiredField("id", codex.String(),
		func(u userRespWithMeta) string { return u.ID },
		func(u *userRespWithMeta, v string) { u.ID = v },
	),
	codex.RequiredField("name", codex.String(),
		func(u userRespWithMeta) string { return u.Name },
		func(u *userRespWithMeta, v string) { u.Name = v },
	),
)

// R1: NewRequiredResponseHeaderParam/NewOptionalResponseHeaderParam register
// both spec ResponseHeaderParam and merge field.
func TestResponseHeaderMergeParam_RegistersSpecAndMergeField(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	h, err := rest.NewRoute[createReq, userRespWithMeta]("POST", "/users", createReqCodec, userRespWithMetaBodyCodec,
		rest.NewRequiredResponseHeaderParam("X-Request-Id", codex.String().Refine(validate.NonEmptyString),
			func(u userRespWithMeta) string { return u.RequestID },
			func(u *userRespWithMeta, v string) { u.RequestID = v }),
		rest.NewOptionalResponseHeaderParam("X-Trace", codex.String(),
			func(u userRespWithMeta) string { return "" },
			func(u *userRespWithMeta, v string) {}),
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if len(h.Descriptor.Responses) == 0 || len(h.Descriptor.Responses[0].Headers) < 2 {
		t.Fatalf("Descriptor.Responses[0].Headers: want >= 2, got %+v", h.Descriptor.Responses)
	}
	if len(h.ResponseHeaderMergeFields()) != 2 {
		t.Fatalf("ResponseHeaderMergeFields: want 2, got %d", len(h.ResponseHeaderMergeFields()))
	}
}

// R2: NewRequiredResponseCookieParam/NewOptionalResponseCookieParam register
// both spec ResponseCookieParam and merge field.
func TestResponseCookieMergeParam_RegistersSpecAndMergeField(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	h, err := rest.NewRoute[createReq, userRespWithMeta]("POST", "/users", createReqCodec, userRespWithMetaBodyCodec,
		rest.NewRequiredResponseCookieParam("session", codex.String().Refine(validate.NonEmptyString),
			func(u userRespWithMeta) string { return u.Session },
			func(u *userRespWithMeta, v string) { u.Session = v }),
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if len(h.ResponseCookieMergeFields()) != 1 {
		t.Fatalf("ResponseCookieMergeFields: want 1, got %d", len(h.ResponseCookieMergeFields()))
	}
}

// R3: RouteHandle.DecodeMergedResponse happy path — body, response header,
// and response cookie all merged into one Resp.
func TestDecodeMergedResponse_HappyPath(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	h, err := rest.NewRoute[createReq, userRespWithMeta]("POST", "/users", createReqCodec, userRespWithMetaBodyCodec,
		rest.NewRequiredResponseHeaderParam("X-Request-Id", codex.String().Refine(validate.NonEmptyString),
			func(u userRespWithMeta) string { return u.RequestID },
			func(u *userRespWithMeta, v string) { u.RequestID = v }),
		rest.NewOptionalResponseCookieParam("session", codex.String(),
			func(u userRespWithMeta) string { return u.Session },
			func(u *userRespWithMeta, v string) { u.Session = v }),
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	body := []byte(`{"id":"u1","name":"Alice"}`)
	resp, err := h.DecodeMergedResponse(body,
		map[string]string{"X-Request-Id": "req-123"},
		map[string]string{"session": "sess-abc"},
	)
	if err != nil {
		t.Fatalf("DecodeMergedResponse: %v", err)
	}
	if resp.ID != "u1" || resp.Name != "Alice" || resp.RequestID != "req-123" || resp.Session != "sess-abc" {
		t.Errorf("unexpected merged resp: %+v", resp)
	}
}

// R4: RouteHandle.DecodeMergedResponse merge failure — required response
// header missing.
func TestDecodeMergedResponse_MergeFailure(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	h, err := rest.NewRoute[createReq, userRespWithMeta]("POST", "/users", createReqCodec, userRespWithMetaBodyCodec,
		rest.NewRequiredResponseHeaderParam("X-Request-Id", codex.String().Refine(validate.NonEmptyString),
			func(u userRespWithMeta) string { return u.RequestID },
			func(u *userRespWithMeta, v string) { u.RequestID = v }),
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	body := []byte(`{"id":"u1","name":"Alice"}`)
	_, err = h.DecodeMergedResponse(body, map[string]string{}, nil)
	if err == nil {
		t.Fatal("expected error for missing required response header")
	}
	var ve codex.ValidationErrors
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationErrors, got %T: %v", err, err)
	}
}

// R5: RouteHandle.DecodeMergedResponse with zero response merge fields
// behaves like plain DecodeResponse.
func TestDecodeMergedResponse_NoMergeFieldsIsNoop(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	h, err := rest.NewRoute[createReq, userResp]("POST", "/users", createReqCodec, userCodec).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	body := []byte(`{"id":"u1","name":"Alice"}`)
	viaDecodeResponse, err := h.DecodeResponse(body)
	if err != nil {
		t.Fatalf("DecodeResponse: %v", err)
	}
	viaMerged, err := h.DecodeMergedResponse(body, nil, nil)
	if err != nil {
		t.Fatalf("DecodeMergedResponse: %v", err)
	}
	if viaDecodeResponse != viaMerged {
		t.Errorf("DecodeMergedResponse should match plain DecodeResponse when no response merge fields declared: %+v vs %+v", viaDecodeResponse, viaMerged)
	}
}

// ── Round 4: nested struct composition + non-JSON body formats ────────────

// N1: merge-field get/set closures reach into a NESTED sub-struct exactly
// as easily as a top-level field — no framework change needed, since
// get/set are plain Go closures, not reflection over Req's direct fields.
func TestNestedStructMergeFields_GetSetReachIntoSubstruct(t *testing.T) {
	type meta struct {
		ContentHash string
		Compress    string
	}
	type req struct {
		ID   string
		Meta meta
	}
	respCodec := codex.Struct[userResp](
		codex.OptionalField("id", codex.String(),
			func(u userResp) string { return u.ID },
			func(u *userResp, v string) { u.ID = v },
		),
	)
	b := rest.NewBuilder(testInfo)
	h, err := rest.NewRoute[req, userResp]("GET", "/things/{id}", codex.Struct[req](), respCodec,
		rest.NewPathParam("id", codex.String(),
			func(r req) string { return r.ID },
			func(r *req, v string) { r.ID = v }),
		rest.NewOptionalHeaderParam("X-Content-Hash", codex.String(),
			func(r req) string { return r.Meta.ContentHash },
			func(r *req, v string) { r.Meta.ContentHash = v }),
		rest.NewOptionalQueryParam("compress", codex.String(),
			func(r req) string { return r.Meta.Compress },
			func(r *req, v string) { r.Meta.Compress = v }),
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	// Decode direction: server merges path/header/query into nested Meta fields.
	decoded, err := h.DecodeMerged(nil,
		map[string]string{"id": "abc"},
		map[string]string{"compress": "gzip"},
		map[string]string{"X-Content-Hash": "sha256:xyz"},
		nil,
	)
	if err != nil {
		t.Fatalf("DecodeMerged: %v", err)
	}
	if decoded.ID != "abc" || decoded.Meta.ContentHash != "sha256:xyz" || decoded.Meta.Compress != "gzip" {
		t.Errorf("unexpected decoded nested req: %+v", decoded)
	}

	// Encode direction: client role-aware accessors read from nested Meta fields.
	item := req{ID: "abc", Meta: meta{ContentHash: "sha256:xyz", Compress: "gzip"}}
	pathVars, err := codex.EncodeVars(item, h.PathMergeFields()...)
	if err != nil {
		t.Fatalf("EncodeVars(path): %v", err)
	}
	headers, err := codex.EncodeVars(item, h.HeaderMergeFields()...)
	if err != nil {
		t.Fatalf("EncodeVars(header): %v", err)
	}
	query, err := codex.EncodeVars(item, h.QueryMergeFields()...)
	if err != nil {
		t.Fatalf("EncodeVars(query): %v", err)
	}
	if pathVars["id"] != "abc" || headers["X-Content-Hash"] != "sha256:xyz" || query["compress"] != "gzip" {
		t.Errorf("unexpected encoded vars: path=%v headers=%v query=%v", pathVars, headers, query)
	}
}

// N2: a non-JSON body format (format.NewTyped projecting Gob onto a nested
// Payload sub-field) composes with header/query merge fields on the SAME
// nested Req — full client-server round trip via CallHandle/Register, no
// field collision between the Gob-encoded Payload and the merge-populated
// Meta/ID fields.
func TestGobBodyFormat_ComposesWithNestedMergeFields(t *testing.T) {
	type payload struct {
		Filename string
		Data     []byte
	}
	type meta struct {
		ContentHash string
	}
	type uploadReq struct {
		ID      string
		Meta    meta
		Payload payload
	}
	type uploadResp struct {
		Size int
	}

	reqCodec := codex.Struct[uploadReq]()
	respCodec := codex.Struct[uploadResp](
		codex.RequiredField("size", codex.Int(),
			func(r uploadResp) int { return r.Size },
			func(r *uploadResp, v int) { r.Size = v },
		),
	)

	gobFormat := format.NewTyped[uploadReq](
		reqCodec,
		func(r uploadReq) ([]byte, error) {
			var buf bytes.Buffer
			if err := gob.NewEncoder(&buf).Encode(r.Payload); err != nil {
				return nil, err
			}
			return buf.Bytes(), nil
		},
		func(data []byte) (uploadReq, error) {
			var p payload
			if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&p); err != nil {
				return uploadReq{}, err
			}
			return uploadReq{Payload: p}, nil
		},
		"application/gob",
	)

	route := rest.NewRoute[uploadReq, uploadResp]("POST", "/uploads/{id}", reqCodec, respCodec,
		rest.NewPathParam("id", codex.String(),
			func(r uploadReq) string { return r.ID },
			func(r *uploadReq, v string) { r.ID = v }),
		rest.NewOptionalHeaderParam("X-Content-Hash", codex.String(),
			func(r uploadReq) string { return r.Meta.ContentHash },
			func(r *uploadReq, v string) { r.Meta.ContentHash = v }),
	)

	b := rest.NewBuilder(testInfo)
	serverHandle, err := route.Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	serverHandle.WithRequestFormats(gobFormat)

	var gotID, gotHash string
	var gotPayload payload
	mux := http.NewServeMux()
	nethttp.Register(mux, serverHandle, func(_ context.Context, req uploadReq) (uploadResp, error) {
		gotID = req.ID
		gotHash = req.Meta.ContentHash
		gotPayload = req.Payload
		return uploadResp{Size: len(req.Payload.Data)}, nil
	}, nethttp.Options{})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	clientHandle := route.ClientHandle()
	clientHandle.WithRequestFormats(gobFormat)

	item := uploadReq{
		ID:      "f1",
		Meta:    meta{ContentHash: "sha256:abc"},
		Payload: payload{Filename: "x.bin", Data: []byte("hello world")},
	}
	resp, err := nethttp.CallHandle(context.Background(), srv.Client(), srv.URL, clientHandle, item, nethttp.CallOptions{})
	if err != nil {
		t.Fatalf("CallHandle: %v", err)
	}
	if gotID != "f1" {
		t.Errorf("server req.ID: want %q, got %q", "f1", gotID)
	}
	if gotHash != "sha256:abc" {
		t.Errorf("server req.Meta.ContentHash: want %q, got %q", "sha256:abc", gotHash)
	}
	if gotPayload.Filename != "x.bin" || string(gotPayload.Data) != "hello world" {
		t.Errorf("server req.Payload: unexpected %+v", gotPayload)
	}
	if resp.Size != len("hello world") {
		t.Errorf("resp.Size: want %d, got %d", len("hello world"), resp.Size)
	}
}

// TestMergeFieldTypeError_LogValue mirrors the FormatOptError LogValue test
// pattern above.
func TestMergeFieldTypeError_LogValue(t *testing.T) {
	err := rest.MergeFieldTypeError{Err: fmt.Errorf("boom")}
	lv := err.LogValue()
	if lv.Kind() != slog.KindGroup {
		t.Fatalf("LogValue: want KindGroup, got %v", lv.Kind())
	}
	attrs := lv.Group()
	keys := make(map[string]bool, len(attrs))
	for _, a := range attrs {
		keys[a.Key] = true
	}
	if !keys["err"] {
		t.Error("missing LogValue key \"err\"")
	}
}

type pipelineStatusConflictError struct{ msg string }

func (e pipelineStatusConflictError) Error() string {
	if e.msg != "" {
		return e.msg
	}
	return "pipeline conflict"
}

type pipelineStatusOtherError struct{ msg string }

func (e pipelineStatusOtherError) Error() string {
	if e.msg != "" {
		return e.msg
	}
	return "pipeline other"
}

func TestErrorStatusFor_TypeMatch(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	h, err := rest.NewRoute[createReq, userResp]("POST", "/pipeline/status",
		createReqCodec, userCodec,
		rest.ErrorStatus[pipelineStatusConflictError](409),
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	status, ok := h.ErrorStatusFor(
		fmt.Errorf("wrapped: %w", pipelineStatusConflictError{msg: "dup"}),
	)
	if !ok {
		t.Fatal("want match, got no match")
	}
	if status != 409 {
		t.Fatalf("want 409, got %d", status)
	}
}

func TestErrorStatusFor_FirstMatchWins(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	h, err := rest.NewRoute[createReq, userResp]("POST", "/pipeline/status-order",
		createReqCodec, userCodec,
		rest.ErrorStatus[error](499),
		rest.ErrorStatus[pipelineStatusConflictError](409),
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	status, ok := h.ErrorStatusFor(pipelineStatusConflictError{msg: "dup"})
	if !ok {
		t.Fatal("want match, got no match")
	}
	if status != 499 {
		t.Fatalf("want first-match status 499, got %d", status)
	}
}

func TestErrorStatusFor_NoMatch(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	h, err := rest.NewRoute[createReq, userResp]("POST", "/pipeline/status-none",
		createReqCodec, userCodec,
		rest.ErrorStatus[pipelineStatusConflictError](409),
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	status, ok := h.ErrorStatusFor(pipelineStatusOtherError{msg: "x"})
	if ok {
		t.Fatalf("want no match, got status %d", status)
	}
	if status != 0 {
		t.Fatalf("want status 0 when no match, got %d", status)
	}
}

type directPatternError struct {
	Code string
}

func (e directPatternError) Error() string { return "direct pattern " + e.Code }

var directPatternCodec = codex.Struct[directPatternError](
	codex.RequiredField("code", codex.String().Refine(validate.NonEmptyString),
		func(e directPatternError) string { return e.Code },
		func(e *directPatternError, v string) { e.Code = v },
	),
)

type mappedPatternError struct{ Msg string }

func (e mappedPatternError) Error() string { return e.Msg }

type mappedPatternPayload struct {
	Kind string
}

var mappedPatternCodec = codex.Struct[mappedPatternPayload](
	codex.RequiredField("kind", codex.String().Refine(validate.NonEmptyString),
		func(p mappedPatternPayload) string { return p.Kind },
		func(p *mappedPatternPayload, v string) { p.Kind = v },
	),
)

func TestErrorResponseFor_DirectPattern(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	h, err := rest.NewRoute[createReq, userResp]("POST", "/errors/direct",
		createReqCodec, userCodec,
		rest.ErrorPattern[directPatternError, directPatternError](409, directPatternCodec),
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	resp, ok, applyErr := h.ErrorResponseFor(fmt.Errorf("wrapped: %w", directPatternError{Code: "conflict"}))
	if applyErr != nil {
		t.Fatalf("ErrorResponseFor applyErr: %v", applyErr)
	}
	if !ok {
		t.Fatal("want pattern match, got no match")
	}
	if resp.Status != 409 {
		t.Fatalf("want status 409, got %d", resp.Status)
	}
	var payload map[string]string
	if err := json.Unmarshal(resp.Body, &payload); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if payload["code"] != "conflict" {
		t.Fatalf("want code=conflict, got %q", payload["code"])
	}
}

func TestErrorResponseFor_MappedPattern(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	h, err := rest.NewRoute[createReq, userResp]("POST", "/errors/mapped",
		createReqCodec, userCodec,
		rest.ErrorPattern[mappedPatternError, mappedPatternPayload](422, mappedPatternCodec,
			func(e mappedPatternError) (mappedPatternPayload, error) {
				return mappedPatternPayload{Kind: e.Msg}, nil
			}),
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	resp, ok, applyErr := h.ErrorResponseFor(mappedPatternError{Msg: "validation"})
	if applyErr != nil {
		t.Fatalf("ErrorResponseFor applyErr: %v", applyErr)
	}
	if !ok {
		t.Fatal("want pattern match, got no match")
	}
	if resp.Status != 422 {
		t.Fatalf("want status 422, got %d", resp.Status)
	}
	var payload map[string]string
	if err := json.Unmarshal(resp.Body, &payload); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if payload["kind"] != "validation" {
		t.Fatalf("want kind=validation, got %q", payload["kind"])
	}
}

func TestErrorPattern_DefaultAction_IsRespond(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	h, err := rest.NewRoute[createReq, userResp]("POST", "/errors/default-action",
		createReqCodec, userCodec,
		rest.ErrorPattern[directPatternError, directPatternError](409, directPatternCodec),
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	resp, ok, applyErr := h.ErrorResponseFor(directPatternError{Code: "x"})
	if applyErr != nil {
		t.Fatalf("ErrorResponseFor applyErr: %v", applyErr)
	}
	if !ok {
		t.Fatal("want match")
	}
	if resp.Action != rest.ErrorRespond {
		t.Fatalf("want default action ErrorRespond, got %q", resp.Action)
	}
}

func TestErrorPattern_WithAction_Handle(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	h, err := rest.NewRoute[createReq, userResp]("POST", "/errors/handle-action",
		createReqCodec, userCodec,
		rest.ErrorPattern[directPatternError, directPatternError](409, directPatternCodec).
			WithAction(rest.ErrorHandle),
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	resp, ok, applyErr := h.ErrorResponseFor(directPatternError{Code: "x"})
	if applyErr != nil {
		t.Fatalf("ErrorResponseFor applyErr: %v", applyErr)
	}
	if !ok {
		t.Fatal("want match")
	}
	if resp.Action != rest.ErrorHandle {
		t.Fatalf("want action ErrorHandle, got %q", resp.Action)
	}
}

func TestErrorPattern_WithAction_Log(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	h, err := rest.NewRoute[createReq, userResp]("POST", "/errors/log-action",
		createReqCodec, userCodec,
		rest.ErrorPattern[directPatternError, directPatternError](409, directPatternCodec).
			WithAction(rest.ErrorLog),
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	resp, ok, applyErr := h.ErrorResponseFor(directPatternError{Code: "x"})
	if applyErr != nil {
		t.Fatalf("ErrorResponseFor applyErr: %v", applyErr)
	}
	if !ok {
		t.Fatal("want match")
	}
	if resp.Action != rest.ErrorLog {
		t.Fatalf("want action ErrorLog, got %q", resp.Action)
	}
}

// TestErrorPattern_StatusAndSpecStillWork_AfterActionRefactor is a regression
// guard: ErrorStatusFor and the AsyncAPI-equivalent ResponseMeta/status
// side effects that ErrorPattern.applyRoute installs must be unaffected by
// the ErrorPatternOpt[E,B] chainable-return refactor.
func TestErrorPattern_StatusAndSpecStillWork_AfterActionRefactor(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	h, err := rest.NewRoute[createReq, userResp]("POST", "/errors/status-regress",
		createReqCodec, userCodec,
		rest.ErrorPattern[directPatternError, directPatternError](409, directPatternCodec),
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	status, ok := h.ErrorStatusFor(directPatternError{Code: "x"})
	if !ok || status != 409 {
		t.Fatalf("want ErrorStatusFor to return (409,true), got (%d,%v)", status, ok)
	}
}

// --- Client-side error decode parity (DecodeErrorFor) ---

// CDP7: DecodeErrorFor happy path.
func TestDecodeErrorFor_MatchedPattern(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	h, err := rest.NewRoute[createReq, userResp]("POST", "/errors/client-decode",
		createReqCodec, userCodec,
		rest.ErrorPattern[directPatternError, directPatternError](409, directPatternCodec),
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	body := []byte(`{"code":"conflict"}`)
	resp, ok, applyErr := h.DecodeErrorFor(409, body)
	if applyErr != nil {
		t.Fatalf("DecodeErrorFor applyErr: %v", applyErr)
	}
	if !ok {
		t.Fatal("want pattern match, got no match")
	}
	if resp.Status != 409 {
		t.Fatalf("want status 409, got %d", resp.Status)
	}
	payload, ok := resp.Value.(directPatternError)
	if !ok {
		t.Fatalf("want Value of type directPatternError, got %T", resp.Value)
	}
	if payload.Code != "conflict" {
		t.Fatalf("want code=conflict, got %q", payload.Code)
	}
}

// CDP2: no declared pattern for that status -> no match.
func TestDecodeErrorFor_NoMatch_UnknownStatus(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	h, err := rest.NewRoute[createReq, userResp]("POST", "/errors/client-decode-nomatch",
		createReqCodec, userCodec,
		rest.ErrorPattern[directPatternError, directPatternError](409, directPatternCodec),
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	resp, ok, applyErr := h.DecodeErrorFor(500, []byte(`{}`))
	if applyErr != nil {
		t.Fatalf("DecodeErrorFor applyErr: %v", applyErr)
	}
	if ok {
		t.Fatalf("want no match, got %+v", resp)
	}
}

// CDP3: pattern tagged WithAction(ErrorHandle) is never matched client-side.
func TestDecodeErrorFor_SkipsErrorHandleAction(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	h, err := rest.NewRoute[createReq, userResp]("POST", "/errors/client-decode-handle",
		createReqCodec, userCodec,
		rest.ErrorPattern[directPatternError, directPatternError](409, directPatternCodec).
			WithAction(rest.ErrorHandle),
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	resp, ok, applyErr := h.DecodeErrorFor(409, []byte(`{"code":"conflict"}`))
	if applyErr != nil {
		t.Fatalf("DecodeErrorFor applyErr: %v", applyErr)
	}
	if ok {
		t.Fatalf("want no match for ErrorHandle-tagged pattern, got %+v", resp)
	}
}

// CDP4: pattern tagged WithAction(ErrorLog) is never matched client-side.
func TestDecodeErrorFor_SkipsErrorLogAction(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	h, err := rest.NewRoute[createReq, userResp]("POST", "/errors/client-decode-log",
		createReqCodec, userCodec,
		rest.ErrorPattern[directPatternError, directPatternError](409, directPatternCodec).
			WithAction(rest.ErrorLog),
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	resp, ok, applyErr := h.DecodeErrorFor(409, []byte(`{"code":"conflict"}`))
	if applyErr != nil {
		t.Fatalf("DecodeErrorFor applyErr: %v", applyErr)
	}
	if ok {
		t.Fatalf("want no match for ErrorLog-tagged pattern, got %+v", resp)
	}
}

// CDP5: matched status but body fails to decode against the declared codec ->
// matched=true, applyErr!=nil. Callers must treat this the same as no match.
func TestDecodeErrorFor_MatchedStatus_DecodeFailure(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	h, err := rest.NewRoute[createReq, userResp]("POST", "/errors/client-decode-fail",
		createReqCodec, userCodec,
		rest.ErrorPattern[directPatternError, directPatternError](409, directPatternCodec),
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	// directPatternCodec requires a non-empty "code" field; send a body
	// that fails that constraint.
	resp, ok, applyErr := h.DecodeErrorFor(409, []byte(`{"code":""}`))
	if applyErr == nil {
		t.Fatal("want decode failure, got nil applyErr")
	}
	if !ok {
		t.Fatal("want ok=true (status matched) even though decode failed")
	}
	_ = resp
}

// CDP6: first-declared-match-wins precedence with two patterns sharing the
// same status.
func TestDecodeErrorFor_FirstMatchWins_SameStatus(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	h, err := rest.NewRoute[createReq, userResp]("POST", "/errors/client-decode-precedence",
		createReqCodec, userCodec,
		rest.ErrorPattern[directPatternError, directPatternError](409, directPatternCodec),
		rest.ErrorPattern[mappedPatternError, mappedPatternPayload](409, mappedPatternCodec,
			func(e mappedPatternError) (mappedPatternPayload, error) {
				return mappedPatternPayload{Kind: e.Msg}, nil
			}),
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	resp, ok, applyErr := h.DecodeErrorFor(409, []byte(`{"code":"conflict"}`))
	if applyErr != nil {
		t.Fatalf("DecodeErrorFor applyErr: %v", applyErr)
	}
	if !ok {
		t.Fatal("want match")
	}
	if _, isDirect := resp.Value.(directPatternError); !isDirect {
		t.Fatalf("want first-declared pattern (directPatternError) to win, got %T", resp.Value)
	}
}

// CDP8: ErrorPatternResponse-adjacent LogValue shape check lives in
// adapters/nethttp/client_test.go (nethttp.ErrorPatternResponse). This test
// only locks DecodeErrorFor's own zero-value behavior on an empty handle.
func TestDecodeErrorFor_NoPatternsDeclared(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	h, err := rest.NewRoute[createReq, userResp]("POST", "/errors/client-decode-none",
		createReqCodec, userCodec,
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	resp, ok, applyErr := h.DecodeErrorFor(409, []byte(`{}`))
	if applyErr != nil {
		t.Fatalf("DecodeErrorFor applyErr: %v", applyErr)
	}
	if ok {
		t.Fatalf("want no match when no patterns declared, got %+v", resp)
	}
}
