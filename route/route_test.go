package route_test

import (
	"testing"

	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/schema"
)

func TestRoute_construction(t *testing.T) {
	r := route.Route{
		Method:      "GET",
		Path:        "/users/{id}",
		OperationID: "getUser",
		Summary:     "Get a user",
		Description: "Returns a single user.",
		Tags:        []string{"users"},
		PathParams: []route.Param{
			{Name: "id", Required: true, Schema: schema.Schema{Type: "string"}},
		},
		QueryParams: []route.Param{
			{Name: "include", Required: false, Schema: schema.Schema{Type: "string"}},
		},
		Responses: []route.Response{
			{
				Status:      "200",
				Description: "OK",
				Schema:      &schema.Schema{Type: "object"},
				SchemaName:  "User",
			},
			{
				Status:      "404",
				Description: "Not Found",
			},
		},
	}

	if r.Method != "GET" {
		t.Errorf("Method = %q, want %q", r.Method, "GET")
	}
	if r.Path != "/users/{id}" {
		t.Errorf("Path = %q, want %q", r.Path, "/users/{id}")
	}
	if r.OperationID != "getUser" {
		t.Errorf("OperationID = %q, want %q", r.OperationID, "getUser")
	}
	if len(r.Tags) != 1 || r.Tags[0] != "users" {
		t.Errorf("Tags = %v, want [users]", r.Tags)
	}
	if len(r.PathParams) != 1 {
		t.Fatalf("PathParams length = %d, want 1", len(r.PathParams))
	}
	if r.PathParams[0].Name != "id" || !r.PathParams[0].Required {
		t.Errorf("PathParams[0] = %+v, want {Name:id Required:true}", r.PathParams[0])
	}
	if len(r.QueryParams) != 1 {
		t.Fatalf("QueryParams length = %d, want 1", len(r.QueryParams))
	}
	if r.QueryParams[0].Required {
		t.Errorf("optional QueryParam[0].Required = true, want false")
	}
	if len(r.Responses) != 2 {
		t.Fatalf("Responses length = %d, want 2", len(r.Responses))
	}
	if r.Responses[0].Status != "200" {
		t.Errorf("Responses[0].Status = %q, want %q", r.Responses[0].Status, "200")
	}
	if r.Responses[1].Schema != nil {
		t.Errorf("Responses[1].Schema = %v, want nil (no-body response)", r.Responses[1].Schema)
	}
}

func TestRoute_withRequestBody(t *testing.T) {
	bodySchema := schema.Schema{Type: "object"}
	r := route.Route{
		Method: "POST",
		Path:   "/users",
		RequestBody: &route.Body{
			Required:    true,
			Schema:      bodySchema,
			SchemaName:  "CreateUserRequest",
			ContentType: "application/json",
		},
	}

	if r.RequestBody == nil {
		t.Fatal("RequestBody = nil, want non-nil")
	}
	if !r.RequestBody.Required {
		t.Error("RequestBody.Required = false, want true")
	}
	if r.RequestBody.SchemaName != "CreateUserRequest" {
		t.Errorf("RequestBody.SchemaName = %q, want %q", r.RequestBody.SchemaName, "CreateUserRequest")
	}
	if r.RequestBody.Schema.Type != "object" {
		t.Errorf("RequestBody.Schema.Type = %q, want object", r.RequestBody.Schema.Type)
	}
}

func TestBody_zeroValueDefaults(t *testing.T) {
	b := route.Body{}
	// Zero value: not required, no schema name, empty content type.
	if b.Required {
		t.Error("zero Body.Required = true, want false")
	}
	if b.SchemaName != "" {
		t.Errorf("zero Body.SchemaName = %q, want empty", b.SchemaName)
	}
	if b.ContentType != "" {
		t.Errorf("zero Body.ContentType = %q, want empty (renderer applies default)", b.ContentType)
	}
}

func TestResponse_statusFormats(t *testing.T) {
	cases := []struct {
		name   string
		status string
	}{
		{"numeric 200", "200"},
		{"numeric 201", "201"},
		{"numeric 204", "204"},
		{"default", "default"},
		{"range 2XX", "2XX"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := route.Response{Status: tc.status, Description: "test"}
			if r.Status != tc.status {
				t.Errorf("Status = %q, want %q", r.Status, tc.status)
			}
		})
	}
}

func TestParam_construction(t *testing.T) {
	p := route.Param{
		Name:        "page",
		Description: "Page number.",
		Required:    false,
		Schema:      schema.Schema{Type: "integer"},
	}
	if p.Name != "page" {
		t.Errorf("Name = %q, want page", p.Name)
	}
	if p.Schema.Type != "integer" {
		t.Errorf("Schema.Type = %q, want integer", p.Schema.Type)
	}
}
