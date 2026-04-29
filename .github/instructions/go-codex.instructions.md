---
description: 'Design instructions for go-codex: an autodocodec-inspired self-documenting codec library for Go'
applyTo: '**/*.go,**/go.mod'
---

# go-codex Development Instructions

go-codex is a Go port of the core ideas from Haskell's [autodocodec](https://hackage.haskell.org/package/autodocodec). A single `Codec[T]` value simultaneously describes how to encode, decode, and document a type. Write once; derive JSON, YAML, OpenAPI, and other representations from the same definition.

**Module:** `github.com/DaniDeer/go-codex`
**Go version:** 1.25.9

## Design Philosophy

- One `Codec[T]` is the single source of truth for encode, decode, and schema.
- Codecs compose: build complex codecs from primitive ones; never duplicate logic.
- Codecs are values, not magic; pass them, return them, store them.
- Errors carry context; decoding failures include field path and expected type.
- No reflection, no struct tags for codec logic; all wiring is explicit in Go code.

## Package Structure and Responsibilities

| Package    | Responsibility                                               | Imports allowed from        |
|------------|--------------------------------------------------------------|-----------------------------|
| `codex`    | PUBLIC API: `Codec[T]`, `MapCodecSafe`, `Constraint`, `Refine` | `schema`                  |
| `primitive`| Leaf codecs: `String`, `Int`, `Bool`, `Float64`, etc.        | `codex`, `schema`           |
| `object`   | Struct composition via field descriptors                     | `codex`, `schema`           |
| `union`    | Tagged union / discriminated union support                   | `codex`, `schema`           |
| `schema`   | Schema model (pure data, no codec logic)                     | none                        |
| `validate` | Reusable `Constraint` functions for numbers, strings, etc.   | `codex`                     |
| `examples` | Usage demonstrations — not importable by other packages      | all                         |

- No circular imports.
- `schema` has zero dependencies inside this module.
- `examples/` must not be imported by any non-example package.

## Core Abstraction: `Codec[T]`

`Codec[T]` lives in the `codex` package. It bundles encode, decode, and schema in one value.

```go
// Codec encodes values of type T to an intermediate representation,
// decodes that representation back to T, and describes the schema.
type Codec[T any] struct {
    Schema  schema.Schema
    Encode  func(T) (any, error)
    Decode  func(any) (T, error)
}
```

- `Encode` transforms a Go value into an intermediate (e.g., `map[string]any` for JSON).
- `Decode` transforms the intermediate back into a Go value, returning an error on failure.
- `Schema` carries documentation: type name, description, examples, constraints.
- Keep `Codec[T]` fields exported so callers can inspect or wrap them.

## `HasCodec` Interface

Types that have a canonical codec implement `HasCodec[T]`:

```go
// HasCodec is implemented by types that declare their canonical Codec.
type HasCodec[T any] interface {
    Codec() codex.Codec[T]
}
```

- Prefer defining `Codec()` as a package-level function `func Codec() codex.Codec[MyType]` when the type is a value type.
- Use a method receiver only when the codec depends on instance state.

## `MapCodecSafe`: Bidirectional Codec Transformation

`MapCodecSafe[A, B any]` transforms `Codec[A]` into `Codec[B]`. Equivalent to autodocodec's `BimapCodec`.

```go
// MapCodecSafe creates a new Codec[B] from Codec[A] using two mapping functions.
// toB may return an error (used during decoding).
// toA must be total (used during encoding).
func MapCodecSafe[A, B any](c codex.Codec[A], toB func(A) (B, error), toA func(B) A) codex.Codec[B]
```

- Use when a type wraps a primitive: e.g., `type Email string` over `primitive.String`.
- `toB` is the decode direction and may fail (return `error`).
- `toA` is the encode direction and must always succeed.

```go
// Good example — Email codec derived from String codec
var EmailCodec = codex.MapCodecSafe(
    primitive.String,
    func(s string) (Email, error) {
        if !strings.Contains(s, "@") {
            return "", fmt.Errorf("invalid email: %q", s)
        }
        return Email(s), nil
    },
    func(e Email) string { return string(e) },
)
```

## `Refine` and `Constraint`

`Refine[T]` wraps an existing `Codec[T]` with one or more `Constraint[T]` predicates. All constraints must pass during decoding; encoding is unaffected.

```go
// Constraint is a named validation predicate.
type Constraint[T any] struct {
    Description string
    Check       func(T) error
}

// Refine adds constraints to a codec. Constraints are checked during Decode.
func Refine[T any](c codex.Codec[T], constraints ...codex.Constraint[T]) codex.Codec[T]
```

- `Constraint.Description` appears in the schema for documentation.
- Reusable constraints live in `validate/`; domain-specific ones live next to the type.

```go
// Good example — constrained integer
var PositiveInt = codex.Refine(
    primitive.Int,
    validate.Min(1),
)
```

## Object Codec: Struct Composition

The `object` package builds codecs for structs by composing field codecs. Modelled after autodocodec's `ObjectCodec` with `RequiredKey` / `OptionalKey`.

```go
// Field describes a single struct field and its codec.
type Field[S, F any] struct {
    Key      string
    Codec    codex.Codec[F]
    Get      func(S) F          // for encoding
    Set      func(*S, F)        // for decoding
    Required bool
}
```

- Use `object.RequiredField` for mandatory fields; `object.OptionalField` for nullable/absent fields.
- Field keys are explicit strings — do not infer from struct field names.
- Compose fields into an `ObjectCodec[S]` using `object.Build`.

```go
// Good example — Point struct
type Point struct {
    X float64
    Y float64
}

var PointCodec = object.Build[Point](
    object.RequiredField("x", primitive.Float64,
        func(p Point) float64 { return p.X },
        func(p *Point, v float64) { p.X = v }),
    object.RequiredField("y", primitive.Float64,
        func(p Point) float64 { return p.Y },
        func(p *Point, v float64) { p.Y = v }),
)
```

## Union Codec: Tagged Unions

The `union` package handles discriminated unions via a string tag field.

```go
// Tagged builds a Codec[T] for a sum type discriminated by a tag field.
func Tagged[T any](tagKey string, variants ...Variant[T]) codex.Codec[T]

// Variant associates a tag value with a sub-codec.
type Variant[T any] struct {
    Tag   string
    Codec codex.Codec[T]
}
```

- `tagKey` is the JSON key used to identify the variant (e.g., `"type"`).
- Each `Variant` maps a tag string to a codec that handles that case.
- Return an error during decode when no variant matches the tag.

```go
// Good example — Shape union
var ShapeCodec = union.Tagged[Shape]("type",
    union.Variant[Shape]{Tag: "circle",    Codec: CircleCodec},
    union.Variant[Shape]{Tag: "rectangle", Codec: RectangleCodec},
)
```

## Schema Model

The `schema` package defines pure data structures that describe a codec. No codec logic lives here.

- `schema.Schema` is the root type; it carries `Type`, `Description`, `Properties`, `Constraints`, etc.
- Codec constructors populate `Schema` when building a `Codec[T]`.
- Downstream renderers (JSON Schema, OpenAPI) read `schema.Schema` without touching codec logic.

## Naming Conventions

| Concept             | Convention                                      | Example                    |
|---------------------|-------------------------------------------------|----------------------------|
| Codec variable      | `<Type>Codec` (exported) or `codec` (unexported) | `EmailCodec`, `PointCodec` |
| Constraint variable | descriptive noun/adjective                      | `validate.Min(1)`, `validate.NonEmpty` |
| Field key string    | camelCase matching external representation      | `"firstName"`, `"createdAt"` |
| Tag key string      | `"type"` by default unless domain differs       | `"type"`, `"kind"`         |
| Package function    | `func Codec() codex.Codec[T]` for canonical codec | `func Codec() codex.Codec[Email]` |

## Error Handling in Codecs

- Decode errors must include the field path (e.g., `"user.address.zip"`).
- Wrap errors with `fmt.Errorf("decoding %s: %w", field, err)`.
- Encode errors are exceptional; prefer designs where encoding is total (never fails).
- `Constraint.Check` returns `nil` on success, a descriptive `error` on failure.

## Common Patterns

### Wrapping a Primitive Type

```go
type UserID string

var UserIDCodec = codex.MapCodecSafe(
    primitive.String,
    func(s string) (UserID, error) {
        if s == "" {
            return "", errors.New("user ID must not be empty")
        }
        return UserID(s), nil
    },
    func(id UserID) string { return string(id) },
)
```

### Slice Codec

```go
var EmailListCodec = codex.SliceOf(EmailCodec)
```

### Optional Field in Object

```go
object.OptionalField("nickname", primitive.String,
    func(u User) *string { return u.Nickname },
    func(u *User, v *string) { u.Nickname = v }),
```

## Validation

- `validate/` contains reusable `Constraint[T]` factory functions.
- Number constraints: `Min`, `Max`, `Range`, `Positive`, `NonNegative`.
- String constraints: `NonEmpty`, `MinLen`, `MaxLen`, `Pattern`, `OneOf`.
- Constraints in `validate/` must not depend on any specific codec; they depend only on `codex.Constraint[T]`.

## Tooling

This project uses [`just`](https://just.systems/) as the task runner. All common development tasks have a `just` recipe. Run `just` with no arguments to list available recipes.

| Recipe | Tool | Purpose |
|--------|------|---------|
| `just build` | `go build` | Compile all packages |
| `just test` | `go test` | Run tests |
| `just test-verbose` | `go test -v` | Run tests with verbose output |
| `just cover` | `go test` + `go tool cover` | Generate HTML coverage report |
| `just fmt` | `gofmt` | List files with formatting issues |
| `just staticcheck` | `staticcheck` | Static analysis (supersedes `go vet`) |
| `just gosec` | `gosec` | Security scan (config: `gosec.config.json`) |
| `just check` | fmt + staticcheck + gosec | All quality gates |
| `just tidy` | `go mod tidy` | Clean up module dependencies |

**Note:** `staticcheck` supersedes `go vet` in this project. Do not run `go vet` directly; use `just staticcheck` or `just check`.

## Verification

```sh
just build    # compile
just check    # fmt + staticcheck + gosec
just test     # run tests
```
