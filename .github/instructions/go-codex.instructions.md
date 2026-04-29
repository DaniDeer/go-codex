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
| `codex`    | PUBLIC API: `Codec[T]`, primitives, struct, union, slice, `MapCodecSafe`, `Constraint`, `Refine` | `schema` |
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
// to is the decode direction and must always succeed (total).
// from is the encode direction and may return an error.
func MapCodecSafe[A, B any](c codex.Codec[A], to func(A) B, from func(B) (A, error)) codex.Codec[B]
```

- Use when a type wraps a primitive: e.g., `type Email string` over `primitive.String()`.
- `to` is the decode direction: transforms the decoded `A` into `B`. Must be total.
- `from` is the encode direction: transforms a `B` back to `A` for encoding. May fail.
- For validation on decode, use `Refine` instead of `MapCodecSafe`.

```go
// Good example — Email newtype codec
type Email string

var EmailCodec = codex.MapCodecSafe(
    codex.String(),
    func(s string) Email { return Email(s) },
    func(e Email) (string, error) { return string(e), nil },
)

// Validation belongs in Refine, not MapCodecSafe:
var ValidEmailCodec = EmailCodec.Refine(codex.Constraint[Email]{
    Name:    "email",
    Check:   func(e Email) bool { return strings.Contains(string(e), "@") },
    Message: func(e Email) string { return fmt.Sprintf("invalid email: %q", e) },
})
```

## `Downcast`: Type Assertion Helper

`Downcast[A, B any]` attempts to cast a value of type `B` to type `A` using a type assertion.

```go
// Downcast attempts to cast a value of type B to type A.
// Useful for tagged unions where variants share a common interface.
func Downcast[A any, B any](v B) (A, error)
```

- Use with `TaggedUnion` when variant types share a common interface and you need to convert to a concrete type.

## `Refine` and `Constraint`

`Refine[T]` wraps an existing `Codec[T]` with one or more `Constraint[T]` predicates. All constraints must pass during decoding; encoding is unaffected.

```go
// Constraint is a named validation predicate.
type Constraint[T any] struct {
    Name    string
    Check   func(T) bool
    Message func(T) string
}

// Refine adds constraints to a codec. Constraints are checked during Decode.
func Refine[T any](c codex.Codec[T], constraints ...codex.Constraint[T]) codex.Codec[T]
```

- `Constraint.Name` identifies the constraint in error messages.
- `Constraint.Message` produces the human-readable failure description.
- Reusable constraints live in `validate/`; domain-specific ones live next to the type.

```go
// Good example — constrained integer
var PositiveIntCodec = codex.Refine(
    codex.Int(),
    validate.PositiveInt,
)
```

## Object Codec: Struct Composition

`codex.Struct` builds a codec for a struct by composing field codecs. Modelled after autodocodec's `ObjectCodec` with `RequiredKey` / `OptionalKey`.

```go
// Field describes a single struct field and its codec.
type Field[S, F any] struct {
    Name     string
    Codec    codex.Codec[F]
    Get      func(S) F          // for encoding
    Set      func(*S, F)        // for decoding
    Required bool
}
```

- `Field.Name` is the explicit key string used in the encoded representation.
- Compose fields into a struct codec using `codex.Struct`.

```go
// Good example — Point struct
type Point struct {
    X float64
    Y float64
}

var PointCodec = codex.Struct[Point](
    codex.Field[Point, float64]{
        Name:     "x",
        Codec:    codex.Float64(),
        Get:      func(p Point) float64 { return p.X },
        Set:      func(p *Point, v float64) { p.X = v },
        Required: true,
    },
    codex.Field[Point, float64]{
        Name:     "y",
        Codec:    codex.Float64(),
        Get:      func(p Point) float64 { return p.Y },
        Set:      func(p *Point, v float64) { p.Y = v },
        Required: true,
    },
)
```

## Union Codec: Tagged Unions

`codex.TaggedUnion` handles discriminated unions via a string tag field.

```go
// TaggedUnion builds a Codec[T] for a sum type discriminated by a tag field.
func TaggedUnion[T any](
    tag string,
    variants map[string]codex.Codec[T],
    selectVariant func(T) (string, error),
) codex.Codec[T]
```

- `tag` is the JSON key used to identify the variant (e.g., `"type"`).
- `variants` maps tag strings to codecs that handle each case.
- `selectVariant` picks the tag for a given value during encoding.
- Return an error during decode when no variant matches the tag.

```go
// Good example — Shape union
var ShapeCodec = codex.TaggedUnion[Shape]("type",
    map[string]codex.Codec[Shape]{
        "circle":    CircleCodec,
        "rectangle": RectangleCodec,
    },
    func(s Shape) (string, error) { return s.Kind(), nil },
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
| Constraint variable | descriptive noun/adjective                      | `validate.PositiveInt`, `validate.NonEmptyString` |
| Field key string    | camelCase matching external representation      | `"firstName"`, `"createdAt"` |
| Tag key string      | `"type"` by default unless domain differs       | `"type"`, `"kind"`         |
| Package function    | `func Codec() codex.Codec[T]` for canonical codec | `func Codec() codex.Codec[Email]` |

## Error Handling in Codecs

- Decode errors must include the field path (e.g., `"field name: ..."`).
- Wrap errors with `fmt.Errorf("decoding %s: %w", field, err)`.
- Encode errors are exceptional; prefer designs where encoding is total (never fails).
- `Constraint.Check` returns `bool`; `Constraint.Message` returns the error string.

## Common Patterns

### Wrapping a Primitive Type

```go
type UserID string

var UserIDCodec = codex.MapCodecSafe(
    codex.String(),
    func(s string) UserID { return UserID(s) },
    func(id UserID) (string, error) { return string(id), nil },
)
```

### Slice Codec

```go
var EmailListCodec = codex.SliceOf(EmailCodec)
```

### Optional Field in Object

Set `Required: false` on the field. The field is omitted from the encoded object when missing during decode; no error is returned.

## Validation

- `validate/` contains reusable `Constraint[T]` factory functions.
- Number constraints: `PositiveInt`, `NegativeInt`, `MinInt(n)`, `MaxInt(n)`, `RangeInt(min, max)`.
- Float constraints: `PositiveFloat`, `NegativeFloat`, `NonZeroFloat`, `MinFloat(n)`, `MaxFloat(n)`, `RangeFloat(min, max)`.
- String constraints: `NonEmptyString`, `MinLen(n)`, `MaxLen(n)`, `Pattern(re)`, `OneOf(values...)`.
- Constraints in `validate/` must not depend on any specific codec; they depend only on `codex.Constraint[T]`.

## Testing

Tests use the standard `testing` package. No test framework dependency.

### File Placement

- `_test.go` files co-located with the package under test.
- Default: external test package (`package codex_test`) for black-box discipline.
- White-box (`package codex`) only when unexported internals must be accessed.

### Table-Driven Pattern

Use `t.Run` subtests with a slice of `{name, input, want, wantErr}` structs:

```go
cases := []struct {
    name    string
    input   any
    want    int
    wantErr bool
}{
    {"from int", 42, 42, false},
    {"wrong type", "x", 0, true},
}
for _, tc := range cases {
    t.Run(tc.name, func(t *testing.T) {
        got, err := codec.Decode(tc.input)
        if (err != nil) != tc.wantErr { ... }
    })
}
```

### What to Test for Every Codec

| Aspect | Test |
|--------|------|
| Happy path | Valid input decodes/encodes correctly |
| Round-trip | `decode(encode(v)) == v` |
| Error paths | Wrong type, missing field, constraint violation |
| Schema | `Schema.Type` and sub-fields correct |
| Error messages | Relevant field names / values included |

### What NOT to Test

- `Codec` struct function fields directly — test through behavior (`Encode`, `Decode`).
- `examples/` — run via `go run`, not `go test`.

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
