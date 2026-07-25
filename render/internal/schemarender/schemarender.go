package schemarender

import "github.com/DaniDeer/go-codex/schema"

// SchemaObject converts s into a JSON-schema-compatible map[string]any.
// Only fields that are set in s are included in the output.
func SchemaObject(s schema.Schema) map[string]any {
	obj := map[string]any{}

	if s.Type != "" {
		obj["type"] = s.Type
	}
	if s.Title != "" {
		obj["title"] = s.Title
	}
	if s.Description != "" {
		obj["description"] = s.Description
	}
	if s.Format != "" {
		obj["format"] = s.Format
	}
	if s.Example != nil {
		obj["example"] = s.Example
	}
	if s.Nullable {
		obj["nullable"] = true
	}

	// Numeric bounds.
	if s.Minimum != nil {
		obj["minimum"] = *s.Minimum
	}
	if s.Maximum != nil {
		obj["maximum"] = *s.Maximum
	}
	if s.ExclusiveMinimum {
		obj["exclusiveMinimum"] = true
	}
	if s.ExclusiveMaximum {
		obj["exclusiveMaximum"] = true
	}

	// String constraints.
	if s.MinLength != nil {
		obj["minLength"] = *s.MinLength
	}
	if s.MaxLength != nil {
		obj["maxLength"] = *s.MaxLength
	}
	if s.Pattern != "" {
		obj["pattern"] = s.Pattern
	}
	if s.Deprecated {
		obj["deprecated"] = true
	}
	if s.Default != nil {
		obj["default"] = s.Default
	}

	// Enum.
	if len(s.Enum) > 0 {
		obj["enum"] = s.Enum
	}

	// Object properties.
	if len(s.Properties) > 0 {
		props := map[string]any{}
		for _, p := range s.Properties {
			props[p.Name] = SchemaObject(p.Schema)
		}
		obj["properties"] = props
	}
	if len(s.Required) > 0 {
		obj["required"] = s.Required
	}
	if s.AdditionalPropertiesSchema != nil {
		obj["additionalProperties"] = SchemaObject(*s.AdditionalPropertiesSchema)
	} else if s.AdditionalProperties != nil {
		obj["additionalProperties"] = *s.AdditionalProperties
	}
	if s.PropertyNames != nil {
		obj["propertyNames"] = SchemaObject(*s.PropertyNames)
	}
	if s.MinProperties != nil {
		obj["minProperties"] = *s.MinProperties
	}
	if s.MaxProperties != nil {
		obj["maxProperties"] = *s.MaxProperties
	}

	// Discriminator (TaggedUnion).
	if s.Discriminator != nil {
		d := map[string]any{"propertyName": s.Discriminator.PropertyName}
		if len(s.Discriminator.Mapping) > 0 {
			d["mapping"] = s.Discriminator.Mapping
		}
		obj["discriminator"] = d
	}

	// Array items.
	if s.Items != nil {
		obj["items"] = SchemaObject(*s.Items)
	}
	if s.MinItems != nil {
		obj["minItems"] = *s.MinItems
	}
	if s.MaxItems != nil {
		obj["maxItems"] = *s.MaxItems
	}
	if s.UniqueItems {
		obj["uniqueItems"] = true
	}

	// Polymorphism.
	if len(s.OneOf) > 0 {
		oneOf := make([]any, len(s.OneOf))
		for i, variant := range s.OneOf {
			oneOf[i] = SchemaObject(variant)
		}
		obj["oneOf"] = oneOf
	}

	return obj
}
