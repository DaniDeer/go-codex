package openapi_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/DaniDeer/go-codex/render/openapi"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/schema"
)

var testInfo = openapi.Info{Title: "Test API", Version: "1.0.0"}

// userSchema is a minimal schema used across test cases.
var userSchema = schema.Schema{
	Type: "object",
	Properties: []schema.Property{
		{Name: "id", Schema: schema.Schema{Type: "string"}},
		{Name: "name", Schema: schema.Schema{Type: "string"}},
	},
	Required: []string{"id", "name"},
}

func TestDocumentBuilder_emptyBuildProducesMinimalDocument(t *testing.T) {
	doc, err := openapi.NewDocumentBuilder(testInfo).Build()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	b, err := doc.MarshalYAML()
	if err != nil {
		t.Fatalf("MarshalYAML error: %v", err)
	}
	out := string(b)
	if !strings.Contains(out, "openapi:") {
		t.Error("missing openapi field in output")
	}
	if !strings.Contains(out, "Test API") {
		t.Error("missing title in output")
	}
}

func TestDocumentBuilder_addRoute_populatesPaths(t *testing.T) {
	doc, err := openapi.NewDocumentBuilder(testInfo).
		AddRoute(route.Route{
			Method:      "GET",
			Path:        "/users",
			OperationID: "listUsers",
			Summary:     "List users",
			Responses: []route.Response{
				{Status: "200", Description: "OK"},
			},
		}).
		Build()
	if err != nil {
		t.Fatalf("Build error: %v", err)
	}

	b, err := doc.MarshalYAML()
	if err != nil {
		t.Fatalf("MarshalYAML error: %v", err)
	}
	out := string(b)
	if !strings.Contains(out, "/users") {
		t.Error("missing path /users in output")
	}
	if !strings.Contains(out, "listUsers") {
		t.Error("missing operationId listUsers in output")
	}
	if !strings.Contains(out, "get:") {
		t.Error("missing get method in output")
	}
}

func TestDocumentBuilder_bodyWithSchemaName_emitsRef(t *testing.T) {
	doc, err := openapi.NewDocumentBuilder(testInfo).
		AddRoute(route.Route{
			Method: "POST",
			Path:   "/users",
			RequestBody: &route.Body{
				Required:   true,
				Schema:     userSchema,
				SchemaName: "User",
			},
			Responses: []route.Response{
				{Status: "201", Description: "Created"},
			},
		}).
		Build()
	if err != nil {
		t.Fatalf("Build error: %v", err)
	}

	b, err := doc.MarshalYAML()
	if err != nil {
		t.Fatalf("MarshalYAML error: %v", err)
	}
	out := string(b)
	if !strings.Contains(out, "$ref: '#/components/schemas/User'") {
		t.Errorf("missing $ref in output:\n%s", out)
	}
	if !strings.Contains(out, "components:") {
		t.Error("missing components section in output")
	}
}

func TestDocumentBuilder_bodyWithoutSchemaName_inlinesSchema(t *testing.T) {
	inlineSchema := schema.Schema{Type: "object", Properties: []schema.Property{{Name: "x", Schema: schema.Schema{Type: "integer"}}}}
	doc, err := openapi.NewDocumentBuilder(testInfo).
		AddRoute(route.Route{
			Method: "POST",
			Path:   "/items",
			RequestBody: &route.Body{
				Required: true,
				Schema:   inlineSchema,
				// No SchemaName — should inline.
			},
			Responses: []route.Response{
				{Status: "201", Description: "Created"},
			},
		}).
		Build()
	if err != nil {
		t.Fatalf("Build error: %v", err)
	}

	b, err := doc.MarshalYAML()
	if err != nil {
		t.Fatalf("MarshalYAML error: %v", err)
	}
	out := string(b)
	if strings.Contains(out, "$ref") {
		t.Error("unexpected $ref — schema should be inlined")
	}
	if !strings.Contains(out, "type: object") {
		t.Errorf("missing inlined schema in output:\n%s", out)
	}
}

func TestDocumentBuilder_responseWithSchemaName_emitsRef(t *testing.T) {
	doc, err := openapi.NewDocumentBuilder(testInfo).
		AddRoute(route.Route{
			Method: "GET",
			Path:   "/users",
			Responses: []route.Response{
				{
					Status:      "200",
					Description: "OK",
					Schema:      &userSchema,
					SchemaName:  "User",
				},
			},
		}).
		Build()
	if err != nil {
		t.Fatalf("Build error: %v", err)
	}

	b, err := doc.MarshalYAML()
	if err != nil {
		t.Fatalf("MarshalYAML error: %v", err)
	}
	out := string(b)
	if !strings.Contains(out, "$ref: '#/components/schemas/User'") {
		t.Errorf("missing $ref in response:\n%s", out)
	}
}

func TestDocumentBuilder_noBodResponse_omitsContent(t *testing.T) {
	doc, err := openapi.NewDocumentBuilder(testInfo).
		AddRoute(route.Route{
			Method: "DELETE",
			Path:   "/users/{id}",
			PathParams: []route.Param{
				{Name: "id", Required: true, Schema: schema.Schema{Type: "string"}},
			},
			Responses: []route.Response{
				{Status: "204", Description: "No Content"},
			},
		}).
		Build()
	if err != nil {
		t.Fatalf("Build error: %v", err)
	}

	b, err := doc.MarshalYAML()
	if err != nil {
		t.Fatalf("MarshalYAML error: %v", err)
	}
	out := string(b)
	// "204" response should have description but no content key.
	if !strings.Contains(out, "204") {
		t.Error("missing 204 status in output")
	}
	if strings.Contains(out, "content:") {
		t.Errorf("unexpected content for no-body response:\n%s", out)
	}
}

func TestDocumentBuilder_multipleRouteSamePath(t *testing.T) {
	doc, err := openapi.NewDocumentBuilder(testInfo).
		AddRoute(route.Route{
			Method: "GET",
			Path:   "/users",
			Responses: []route.Response{
				{Status: "200", Description: "OK"},
			},
		}).
		AddRoute(route.Route{
			Method: "POST",
			Path:   "/users",
			RequestBody: &route.Body{
				Required:   true,
				Schema:     userSchema,
				SchemaName: "User",
			},
			Responses: []route.Response{
				{Status: "201", Description: "Created"},
			},
		}).
		Build()
	if err != nil {
		t.Fatalf("Build error: %v", err)
	}

	b, err := doc.MarshalYAML()
	if err != nil {
		t.Fatalf("MarshalYAML error: %v", err)
	}
	out := string(b)
	if !strings.Contains(out, "get:") {
		t.Error("missing get method in output")
	}
	if !strings.Contains(out, "post:") {
		t.Error("missing post method in output")
	}
}

func TestDocumentBuilder_duplicateMethodPath_returnsError(t *testing.T) {
	_, err := openapi.NewDocumentBuilder(testInfo).
		AddRoute(route.Route{
			Method: "GET", Path: "/users",
			Responses: []route.Response{{Status: "200", Description: "OK"}},
		}).
		AddRoute(route.Route{
			Method: "GET", Path: "/users",
			Responses: []route.Response{{Status: "200", Description: "OK"}},
		}).
		Build()
	if err == nil {
		t.Error("expected error for duplicate (GET, /users), got nil")
	}
}

func TestDocumentBuilder_pathParamMismatch_returnsError(t *testing.T) {
	cases := []struct {
		name   string
		path   string
		params []route.Param
	}{
		{
			name: "declared param not in path",
			path: "/users/{id}",
			params: []route.Param{
				{Name: "id", Required: true, Schema: schema.Schema{Type: "string"}},
				{Name: "extra", Required: true, Schema: schema.Schema{Type: "string"}},
			},
		},
		{
			name:   "placeholder with no declared param",
			path:   "/users/{id}",
			params: []route.Param{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := openapi.NewDocumentBuilder(testInfo).
				AddRoute(route.Route{
					Method:     "GET",
					Path:       tc.path,
					PathParams: tc.params,
					Responses:  []route.Response{{Status: "200", Description: "OK"}},
				}).
				Build()
			if err == nil {
				t.Error("expected validation error for path param mismatch, got nil")
			}
		})
	}
}

func TestDocumentBuilder_explicitSchemaWinOverRoute(t *testing.T) {
	routeSchema := schema.Schema{Type: "string"}
	explicitSchema := schema.Schema{Type: "object", Title: "Explicit"}
	doc, err := openapi.NewDocumentBuilder(testInfo).
		AddSchema("MySchema", explicitSchema).
		AddRoute(route.Route{
			Method: "POST",
			Path:   "/items",
			RequestBody: &route.Body{
				Required:   true,
				Schema:     routeSchema,
				SchemaName: "MySchema",
			},
			Responses: []route.Response{{Status: "201", Description: "Created"}},
		}).
		Build()
	if err != nil {
		t.Fatalf("Build error: %v", err)
	}

	b, err := doc.MarshalYAML()
	if err != nil {
		t.Fatalf("MarshalYAML error: %v", err)
	}
	out := string(b)
	if !strings.Contains(out, "Explicit") {
		t.Errorf("explicit schema title not found in output:\n%s", out)
	}
}

func TestDocumentBuilder_pathParams_allMarkedRequired(t *testing.T) {
	doc, err := openapi.NewDocumentBuilder(testInfo).
		AddRoute(route.Route{
			Method: "GET",
			Path:   "/users/{id}",
			PathParams: []route.Param{
				// Required: false intentionally — builder should force required:true for path params.
				{Name: "id", Required: false, Schema: schema.Schema{Type: "string"}},
			},
			Responses: []route.Response{{Status: "200", Description: "OK"}},
		}).
		Build()
	if err != nil {
		t.Fatalf("Build error: %v", err)
	}

	b, err := doc.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON error: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	paths := raw["paths"].(map[string]any)
	pathItem := paths["/users/{id}"].(map[string]any)
	get := pathItem["get"].(map[string]any)
	params := get["parameters"].([]any)
	if len(params) == 0 {
		t.Fatal("no parameters in output")
	}
	param := params[0].(map[string]any)
	if param["required"] != true {
		t.Errorf("path param required = %v, want true", param["required"])
	}
}

func TestDocumentBuilder_queryParams_notForcedRequired(t *testing.T) {
	doc, err := openapi.NewDocumentBuilder(testInfo).
		AddRoute(route.Route{
			Method: "GET",
			Path:   "/users",
			QueryParams: []route.Param{
				{Name: "limit", Required: false, Schema: schema.Schema{Type: "integer"}},
			},
			Responses: []route.Response{{Status: "200", Description: "OK"}},
		}).
		Build()
	if err != nil {
		t.Fatalf("Build error: %v", err)
	}

	b, err := doc.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON error: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	paths := raw["paths"].(map[string]any)
	pathItem := paths["/users"].(map[string]any)
	get := pathItem["get"].(map[string]any)
	params := get["parameters"].([]any)
	param := params[0].(map[string]any)
	if param["required"] == true {
		t.Error("optional query param should not have required:true")
	}
}

func TestDocumentBuilder_addServer_includesInOutput(t *testing.T) {
	doc, err := openapi.NewDocumentBuilder(testInfo).
		AddServer(openapi.Server{URL: "https://api.example.com", Description: "Production"}).
		Build()
	if err != nil {
		t.Fatalf("Build error: %v", err)
	}

	b, err := doc.MarshalYAML()
	if err != nil {
		t.Fatalf("MarshalYAML error: %v", err)
	}
	out := string(b)
	if !strings.Contains(out, "https://api.example.com") {
		t.Error("missing server URL in output")
	}
	if !strings.Contains(out, "Production") {
		t.Error("missing server description in output")
	}
}

func TestDocument_marshalJSON_validJSON(t *testing.T) {
	doc, err := openapi.NewDocumentBuilder(testInfo).
		AddRoute(route.Route{
			Method: "GET", Path: "/ping",
			Responses: []route.Response{{Status: "200", Description: "pong"}},
		}).
		Build()
	if err != nil {
		t.Fatalf("Build error: %v", err)
	}

	b, err := doc.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON error: %v", err)
	}
	if !json.Valid(b) {
		t.Errorf("MarshalJSON produced invalid JSON: %s", b)
	}
}

func TestDocumentBuilder_defaultResponseStatus(t *testing.T) {
	doc, err := openapi.NewDocumentBuilder(testInfo).
		AddRoute(route.Route{
			Method: "GET",
			Path:   "/items",
			Responses: []route.Response{
				{Status: "default", Description: "Unexpected error"},
				{Status: "2XX", Description: "Success range"},
			},
		}).
		Build()
	if err != nil {
		t.Fatalf("Build error: %v", err)
	}

	b, err := doc.MarshalYAML()
	if err != nil {
		t.Fatalf("MarshalYAML error: %v", err)
	}
	out := string(b)
	if !strings.Contains(out, "default:") {
		t.Error("missing default response in output")
	}
	if !strings.Contains(out, "2XX:") {
		t.Error("missing 2XX response in output")
	}
}
