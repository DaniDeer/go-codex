package codex

import "github.com/DaniDeer/go-codex/schema"

// Codec encodes values of type T to an intermediate representation,
// decodes that representation back to T, and describes the schema.
type Codec[T any] struct {
	Encode func(T) (any, error)
	Decode func(any) (T, error)
	Schema schema.Schema
}
