package schema

// Property is a named schema entry inside an object's properties list.
// Using a slice of Property (rather than a map) preserves the registration
// order, giving deterministic YAML/JSON output across runs.
type Property struct {
	Name   string
	Schema Schema
}

// DiscriminatorSchema describes the polymorphism discriminator field used in
// TaggedUnion schemas. It maps to the OpenAPI 3.x / AsyncAPI discriminator object.
type DiscriminatorSchema struct {
	// PropertyName is the name of the property that holds the type tag.
	PropertyName string `json:",omitempty"`
	// Mapping is an optional explicit tag-value → $ref map.
	Mapping map[string]string `json:",omitempty"`
}

// Schema describes the shape of a value for documentation and validation purposes.
type Schema struct {
	Type        string     `json:",omitempty"`
	Title       string     `json:",omitempty"`
	Description string     `json:",omitempty"`
	Format      string     `json:",omitempty"`
	Example     any        `json:",omitempty"`
	Properties  []Property `json:",omitempty"`
	Required    []string   `json:",omitempty"`
	Enum        []any      `json:",omitempty"`
	OneOf       []Schema   `json:",omitempty"`
	Items       *Schema    `json:",omitempty"`

	// Nullable marks the value as accepting null in addition to its type.
	// Renders as "nullable: true" in OpenAPI 3.0 / AsyncAPI.
	Nullable bool `json:",omitempty"`

	// AdditionalProperties controls whether undeclared properties are allowed.
	// nil = unset (spec default), false = no additional properties, true = any allowed.
	AdditionalProperties *bool `json:",omitempty"`

	// Discriminator describes the polymorphism tag for TaggedUnion schemas.
	Discriminator *DiscriminatorSchema `json:",omitempty"`

	// Numeric constraints.
	Minimum          *float64 `json:",omitempty"`
	Maximum          *float64 `json:",omitempty"`
	ExclusiveMinimum bool     `json:",omitempty"`
	ExclusiveMaximum bool     `json:",omitempty"`

	// String constraints.
	MinLength *int   `json:",omitempty"`
	MaxLength *int   `json:",omitempty"`
	Pattern   string `json:",omitempty"`
}

// Prop returns the schema for the named property, and true if it was found.
func (s Schema) Prop(name string) (Schema, bool) {
	for _, p := range s.Properties {
		if p.Name == name {
			return p.Schema, true
		}
	}
	return Schema{}, false
}
