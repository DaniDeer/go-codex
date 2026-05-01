package codex

// Nullable wraps inner to produce a Codec[*T] that treats nil as JSON null.
// The generated schema inherits all fields from inner and sets Nullable to true.
func Nullable[T any](inner Codec[T]) Codec[*T] {
	s := inner.Schema
	s.Nullable = true
	return Codec[*T]{
		Schema: s,
		Encode: func(v *T) (any, error) {
			if v == nil {
				return nil, nil
			}
			return inner.Encode(*v)
		},
		Decode: func(v any) (*T, error) {
			if v == nil {
				return nil, nil
			}
			decoded, err := inner.Decode(v)
			if err != nil {
				return nil, err
			}
			return &decoded, nil
		},
	}
}
