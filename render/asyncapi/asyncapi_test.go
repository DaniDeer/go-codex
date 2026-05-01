package asyncapi_test

import (
	"github.com/DaniDeer/go-codex/render/asyncapi"
	"github.com/DaniDeer/go-codex/schema"
)

// testInfo is the shared document metadata used across asyncapi tests.
var testInfo = asyncapi.Info{Title: "User Events", Version: "1.0.0"}

// userSchema is a minimal object schema used across asyncapi tests.
var userSchema = schema.Schema{
	Type: "object",
	Properties: map[string]schema.Schema{
		"id":   {Type: "string"},
		"name": {Type: "string"},
	},
	Required: []string{"id", "name"},
}
