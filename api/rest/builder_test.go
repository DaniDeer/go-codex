package rest_test

import (
	"encoding/json"
	"errors"
	"fmt"
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
type sseEvent struct{ Message string }

var sseEventCodec = codex.Struct[sseEvent](
	codex.Field[sseEvent, string]{
		Name:     "message",
		Codec:    codex.String().Refine(validate.NonEmptyString),
		Required: true,
		Get:      func(e sseEvent) string { return e.Message },
		Set:      func(e *sseEvent, v string) { e.Message = v },
	},
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

func TestValidateCookies_missingParam_skipped(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	tokenCodec := codex.String().Refine(validate.NonEmptyString)
	h, err := rest.NewRoute[createReq, userResp]("GET", "/protected", createReqCodec, userCodec, rest.CookieParam{Name: "session_token", Required: true, Codec: &tokenCodec}).Register(b)
	if err != nil {
		t.Fatalf("AddRoute: %v", err)
	}
	// Cookie not present in map — silently skipped.
	if err := h.ValidateCookies(map[string]string{}); err != nil {
		t.Fatalf("want nil for missing cookie, got %v", err)
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
