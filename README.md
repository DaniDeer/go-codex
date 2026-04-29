# GO Codex

## What is go-codex?

In standard Go, encoding, decoding, validation, and documentation are separate concerns that drift apart.
Rename a field and you must update struct tags, the validator, and the schema docs independently — one missed update causes a silent bug or a stale spec.

go-codex is inspired by Haskell's [autodocodec](https://hackage.haskell.org/package/autodocodec).
A single `Codec[T]` value is the source of truth for **encode**, **decode**, **validation**, and **schema** — written once, never duplicated.

### The Problem

```go
// Three separate sources of truth — they drift.
type User struct {
    Name string `json:"name"`
    Age  int    `json:"age"`
}

func decodeUser(data []byte) (User, error) {
    var u User
    return u, json.Unmarshal(data, &u) // no validation
}

func validateUser(u User) error {
    if u.Name == "" {
        return errors.New("name: must not be empty")
    }
    if u.Age <= 0 {
        return errors.New("age: must be positive")
    }
    return nil
}

// Schema lives in a separate openapi.yaml — updated by hand.
```

### The Solution

```go
// One Codec[User] is encode + decode + validate + schema.
type User struct {
    Name string
    Age  int
}

var UserCodec = codex.Struct[User](
    codex.Field[User, string]{
        Name:     "name",
        Codec:    codex.String().Refine(validate.NonEmptyString),
        Get:      func(u User) string { return u.Name },
        Set:      func(u *User, v string) { u.Name = v },
        Required: true,
    },
    codex.Field[User, int]{
        Name:     "age",
        Codec:    codex.Int().Refine(validate.PositiveInt),
        Get:      func(u User) int { return u.Age },
        Set:      func(u *User, v int) { u.Age = v },
        Required: true,
    },
)

// Decode and validate in one step — error includes field path.
user, err := UserCodec.Decode(map[string]any{"name": "Alice", "age": 30})

// Encode back to the intermediate representation.
data, err := UserCodec.Encode(user)

// Schema derived automatically — no separate YAML needed.
schemaJSON, _ := json.MarshalIndent(UserCodec.Schema, "", "  ")
```

## Multi-Format Support

`Codec[T]` operates on an intermediate representation (`map[string]any`) that is format-agnostic.
The `format` package bridges that intermediate to concrete wire formats — the same codec reads and writes JSON, YAML, and TOML unchanged.

```go
jsonFmt := format.JSON(UserCodec)
yamlFmt := format.YAML(UserCodec)
tomlFmt := format.TOML(UserCodec)

// All three produce identical Go values; validation runs on all three.
user, err := jsonFmt.Unmarshal([]byte(`{"name":"Alice","age":30}`))
user, err  = yamlFmt.Unmarshal([]byte("name: Alice\nage: 30\n"))
user, err  = tomlFmt.Unmarshal([]byte("name = \"Alice\"\nage = 30\n"))

// Encode to any format.
jsonBytes, _ := jsonFmt.Marshal(user)
tomlBytes, _ := tomlFmt.Marshal(user)
```

Validation errors and field paths are identical regardless of which format is used.

## Project Structure

```TEXT
go-codex/
├── go.mod
├── README.md

├── codex/                  # ⭐ PUBLIC API: codecs, primitives, struct, union, slice
│   ├── codec.go            # Codec[T]
│   ├── map.go              # MapCodecSafe, Downcast
│   ├── refine.go           # Constraint + Refine
│   ├── primitives.go       # Int, Int64, Float64, String, Bool
│   ├── object.go           # Field[T,F], Struct[T]
│   ├── union.go            # TaggedUnion[T]
│   ├── slice.go            # SliceOf[T]
│
├── format/                 # format bridges: JSON, YAML, TOML
│   ├── format.go           # Format[T], JSON(), YAML(), TOML()
│
├── schema/                 # schema model
│   ├── schema.go
│
├── validate/               # reusable constraints
│   ├── int.go
│   ├── float.go
│   ├── string.go
│
└── examples/
    ├── shape/              # tagged union + Downcast demo
    ├── order/              # nested structs + SliceOf demo
    └── multiformat/        # JSON / YAML / TOML with one codec
```
