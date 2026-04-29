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

## Encode, Decode, and Validation

### The trust boundary

go-codex draws a deliberate line between trusted and untrusted data:

| Direction  | What runs                              | Rationale                                                                                                                                   |
| ---------- | -------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------- |
| **Decode** | type checks + all `Refine` constraints | Input comes from outside — JSON on the wire, YAML from a file, a CLI flag. You cannot trust it. Every constraint runs.                      |
| **Encode** | type conversion only                   | The Go value was constructed by your own code. You already trust it. Running constraints on every encode would be redundant and surprising. |

This mirrors the design of [autodocodec](https://hackage.haskell.org/package/autodocodec): constraints are a guard on ingress, not a restriction on your own domain logic.

### Decode — validates automatically

```go
// Constraints run during Decode. Invalid input is rejected with field-path errors.
user, err := jsonFmt.Unmarshal([]byte(`{"name":"","age":-5}`))
// err: field name: constraint failed (non-empty): expected non-empty string
```

### Encode — trusted, no constraints

```go
// Encoding the value you constructed always succeeds (no constraints run).
// You are responsible for the correctness of values you build.
data, err := jsonFmt.Marshal(User{Name: "", Age: -5}) // succeeds
```

### Validate — explicit bidirectional check

When you need to validate a Go value you constructed — before storing it, after building it programmatically, or to surface errors early — call `Validate` explicitly. It reuses the exact same `Refine` constraints, with no duplication:

```go
// Codec.Validate — no format required.
if err := UserCodec.Validate(u); err != nil {
    return fmt.Errorf("constructed invalid user: %w", err)
}

// Format.Validate — same check, accessed through a Format binding.
if err := jsonFmt.Validate(u); err != nil {
    return err
}
```

`Validate` is always explicit. `Marshal` and `Encode` never silently validate.

## Builtin Format Constraints

`validate/` ships format constraints for common string types. Each constraint validates the value **and** annotates `schema.Schema` so the format appears in OpenAPI output automatically.

| Constraint          | Validates                        | OpenAPI format |
| ------------------- | -------------------------------- | -------------- |
| `validate.Email`    | `user@domain.tld`                | `email`        |
| `validate.UUID`     | RFC 4122 UUID (case-insensitive) | `uuid`         |
| `validate.URL`      | absolute http/https URL          | `uri`          |
| `validate.IPv4`     | dotted-decimal IPv4              | `ipv4`         |
| `validate.IPv6`     | IPv6 address                     | `ipv6`         |
| `validate.Date`     | `YYYY-MM-DD` (ISO 8601)          | `date`         |
| `validate.DateTime` | RFC 3339 date-time               | `date-time`    |
| `validate.Slug`     | `lowercase-hyphen-slug`          | `pattern`      |

```go
var ContactCodec = codex.Struct[Contact](
    codex.Field[Contact, string]{
        Name:     "email",
        Codec:    codex.String().Refine(validate.Email).WithDescription("Primary email."),
        Get:      func(c Contact) string { return c.Email },
        Set:      func(c *Contact, v string) { c.Email = v },
        Required: true,
    },
    codex.Field[Contact, string]{
        Name:     "id",
        Codec:    codex.String().Refine(validate.UUID),
        Get:      func(c Contact) string { return c.ID },
        Set:      func(c *Contact, v string) { c.ID = v },
        Required: true,
    },
)

// Decode validates format automatically — no extra step.
contact, err := ContactCodec.Decode(map[string]any{
    "email": "not-an-email",   // → constraint failed (email): invalid email address: "not-an-email"
    "id":    "bad-uuid",       // → constraint failed (uuid): invalid UUID: "bad-uuid"
})

// OpenAPI schema includes format: email, format: uuid automatically.
yamlBytes, _ := openapi.MarshalYAML(map[string]schema.Schema{"Contact": ContactCodec.Schema})
```

See `examples/formats/` for a runnable demo covering all constraints.

## OpenAPI Schema Generation

`Codec[T]` carries a `schema.Schema` that describes the type: field names, types, constraints, descriptions, and examples. The `render/openapi` package converts that schema into an OpenAPI 3.x `components/schemas` map — no manual YAML authoring, no drift.

```go
import (
    "github.com/DaniDeer/go-codex/render/openapi"
    "github.com/DaniDeer/go-codex/validate"
)

var UserCodec = codex.Struct[User](
    codex.Field[User, string]{
        Name: "name",
        Codec: codex.String().
            Refine(validate.NonEmptyString).
            Refine(validate.MaxLen(100)).
            WithTitle("Full Name").
            WithDescription("The user's full display name."),
        Get:      func(u User) string { return u.Name },
        Set:      func(u *User, v string) { u.Name = v },
        Required: true,
    },
    codex.Field[User, int]{
        Name: "age",
        Codec: codex.Int().
            Refine(validate.RangeInt(0, 150)).
            WithDescription("Age in years."),
        Get:      func(u User) int { return u.Age },
        Set:      func(u *User, v int) { u.Age = v },
        Required: true,
    },
)

// Render components/schemas as YAML — ready to paste into openapi.yaml.
yamlBytes, err := openapi.MarshalYAML(map[string]schema.Schema{
    "User": UserCodec.Schema,
})
```

Output (trimmed):

```yaml
User:
  type: object
  properties:
    name:
      type: string
      title: Full Name
      description: The user's full display name.
      minLength: 1
      maxLength: 100
    age:
      type: integer
      description: Age in years.
      minimum: 0
      maximum: 150
  required: [name, age]
```

The same `UserCodec` encodes, decodes, validates, and documents — written once.

Constraint schema reflection is opt-in: `validate.*` constraints (e.g. `MinLen`, `RangeInt`, `OneOf`, `Pattern`) automatically annotate the schema. Custom constraints can do the same by setting `Constraint.Schema`.

See `examples/openapi/` for a runnable demonstration.

## Protobuf Integration

go-codex and Protobuf solve different problems. In a proto-first workflow the two complement each other cleanly.

**Ownership model:**

| Concern                                                        | Owner                      |
| -------------------------------------------------------------- | -------------------------- |
| Wire format, field numbers, binary encoding                    | `.proto` + `protoc-gen-go` |
| Validation rules, richer documentation, format-agnostic decode | `Codec[T]`                 |

**Workflow:**

1. Define your `.proto` file — this is the source of truth for the wire format.
2. Run `protoc-gen-go` to generate Go structs.
3. Write a `Codec[T]` on top of the generated struct to add what proto cannot express: validation constraints, field descriptions, examples, and format-agnostic (JSON/YAML/TOML) decode.

```go
// Generated by protoc-gen-go — do not edit.
type CreateUserRequest struct {
    Name  string
    Email string
    Age   int32
}

// Defined by you — the codec adds validation + documentation.
var CreateUserRequestCodec = codex.Struct[CreateUserRequest](
    codex.Field[CreateUserRequest, string]{
        Name:     "name",
        Codec:    codex.String().Refine(validate.NonEmptyString).WithDescription("Display name."),
        Get:      func(r CreateUserRequest) string { return r.Name },
        Set:      func(r *CreateUserRequest, v string) { r.Name = v },
        Required: true,
    },
    // ...
)
```

**What this gives you:**

- gRPC handles binary transport; the codec handles REST/JSON/YAML config validation.
- `render/openapi` renders the codec's schema as OpenAPI documentation — no separate YAML file.
- Validation rules (`Refine`) live in Go, next to the type, not scattered across proto options.

**What this is not:** go-codex does not generate `.proto` files from codecs, and does not read `.proto` files. The proto file is the wire-format source of truth; the codec is the validation-and-documentation source of truth. These concerns are intentionally separate.

## Project Structure

```TEXT
go-codex/
├── go.mod
├── README.md

├── codex/                  # ⭐ PUBLIC API: codecs, primitives, struct, union, slice
│   ├── codec.go            # Codec[T], WithDescription, WithTitle
│   ├── map.go              # MapCodecSafe, Downcast
│   ├── refine.go           # Constraint + Refine (Constraint.Schema for schema reflection)
│   ├── primitives.go       # Int, Int64, Float64, String, Bool
│   ├── object.go           # Field[T,F], Struct[T]
│   ├── union.go            # TaggedUnion[T]
│   ├── slice.go            # SliceOf[T]
│
├── format/                 # format bridges: JSON, YAML, TOML
│   ├── format.go           # Format[T], JSON(), YAML(), TOML()
│
├── render/                 # schema renderers (import schema only)
│   └── openapi/            # OpenAPI 3.x components/schemas renderer
│       └── openapi.go      # SchemaObject, ComponentsSchemas, MarshalJSON, MarshalYAML
│
├── schema/                 # schema model (pure data, zero dependencies)
│   ├── schema.go
│
├── validate/               # reusable constraints (reflect into schema automatically)
│   ├── int.go
│   ├── float.go
│   ├── string.go
│
└── examples/
    ├── formats/            # builtin format constraints demo (Email, UUID, URL, ...)
    ├── openapi/            # OpenAPI schema generation from a Codec
    ├── shape/              # tagged union + Downcast demo
    ├── order/              # nested structs + SliceOf demo
    ├── multiformat/        # JSON / YAML / TOML with one codec
    └── validate/           # explicit Validate before marshal
```
