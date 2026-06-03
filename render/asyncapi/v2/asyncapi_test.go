package v2_test

import (
	v2 "github.com/DaniDeer/go-codex/render/asyncapi/v2"
	"github.com/DaniDeer/go-codex/schema"
)

// testInfo is the shared document metadata used across asyncapi tests.
var testInfo = v2.Info{Title: "User Events", Version: "1.0.0"}

// userSchema is a minimal object schema used across asyncapi tests.
var userSchema = schema.Schema{
	Type: "object",
	Properties: []schema.Property{
		{Name: "id", Schema: schema.Schema{Type: "string"}},
		{Name: "name", Schema: schema.Schema{Type: "string"}},
	},
	Required: []string{"id", "name"},
}
