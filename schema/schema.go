package schema

// Schema describes the shape of a value for documentation and validation purposes.
type Schema struct {
	Type       string            `json:",omitempty"`
	Properties map[string]Schema `json:",omitempty"`
	Required   []string          `json:",omitempty"`
	Enum       []any             `json:",omitempty"`
	OneOf      []Schema          `json:",omitempty"`
	Items      *Schema           `json:",omitempty"`
}
