// Package format bridges Codec[T] to concrete serialization formats (JSON, YAML, TOML, Gob).
//
// A codec works with an intermediate representation (map[string]any) that is
// format-agnostic. Format wraps that intermediate layer so the same codec can
// read and write multiple wire formats without any changes to the codec itself.
//
// Text-based formats (JSON, YAML, TOML) pass through the map[string]any intermediate.
// Binary and typed formats (Gob) bypass the intermediate and operate on the typed
// value directly via the marshalTyped/unmarshalTyped path.
package format
