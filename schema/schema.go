package schema

// Schema describes the shape of a value for documentation and validation purposes.
type Schema struct {
	Type        string            `json:",omitempty"`
	Title       string            `json:",omitempty"`
	Description string            `json:",omitempty"`
	Format      string            `json:",omitempty"`
	Example     any               `json:",omitempty"`
	Properties  map[string]Schema `json:",omitempty"`
	Required    []string          `json:",omitempty"`
	Enum        []any             `json:",omitempty"`
	OneOf       []Schema          `json:",omitempty"`
	Items       *Schema           `json:",omitempty"`

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
