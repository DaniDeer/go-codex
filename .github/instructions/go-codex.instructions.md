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

| Package           | Responsibility                                                                            | Imports allowed from             |
|-------------------|-------------------------------------------------------------------------------------------|----------------------------------|
| `codex`           | PUBLIC API: `Codec[T]`, primitives, struct, union, slice, `MapCodecSafe`, `Constraint`, `Refine` | `schema`                  |
| `schema`          | Schema model (pure data, no codec logic)                                                  | none                             |
| `validate`        | Reusable `Constraint` functions for numbers, strings, etc.                                | `codex`, `schema`                |
| `format`          | Bridges `Codec[T]` to wire formats: JSON, YAML, TOML                                     | `codex`, external libs           |
| `route`           | HTTP route descriptors: `Route`, `Param`, `Body`, `Response`                             | `schema`                         |
| `render/openapi`  | Renders `schema.Schema` as OpenAPI 3.1 `components/schemas`; `DocumentBuilder` for full spec | `schema`, `route`, external libs |
| `render/asyncapi` | Renders channels and schemas as a full AsyncAPI 2.6 document                             | `schema`, external libs          |
| `examples`        | Usage demonstrations — not importable by other packages                                   | all                              |

- No circular imports.
- `schema` has zero dependencies inside this module.
- `route` imports only `schema` — no renderer or codec logic.
- `render/openapi` imports `schema` and `route` — no codec logic in the renderer layer.
- `render/asyncapi` imports only `schema` — channels are independent of HTTP route concepts.
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

### Annotating Codecs

Use fluent methods to attach human-readable metadata to the schema:

```go
// WithDescription returns a new Codec with Schema.Description set.
func (c Codec[T]) WithDescription(desc string) Codec[T]

// WithTitle returns a new Codec with Schema.Title set.
func (c Codec[T]) WithTitle(title string) Codec[T]
```

These are typically chained after `Refine`:

```go
var AgeCodec = codex.Int().
    Refine(validate.RangeInt(0, 150)).
    WithTitle("Age").
    WithDescription("Age in years.")
```

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
// The optional Schema field annotates the codec's schema when the constraint
// is applied via Refine. Set it to propagate constraint metadata (e.g. bounds,
// patterns) into the schema for renderers such as render/openapi.
type Constraint[T any] struct {
    Name    string
    Check   func(T) bool
    Message func(T) string
    Schema  func(schema.Schema) schema.Schema // optional: mutates schema when Refine is applied
}

// Refine adds constraints to a codec. Constraints are checked during Decode.
// If Constraint.Schema is non-nil, it is applied to the codec's schema.
func Refine[T any](c codex.Codec[T], constraints ...codex.Constraint[T]) codex.Codec[T]
```

- `Constraint.Name` identifies the constraint in error messages.
- `Constraint.Message` produces the human-readable failure description.
- `Constraint.Schema` is optional. Set it to annotate the codec's schema (e.g. `MinLength`, `Minimum`). Nil = no-op; all existing constraints are unaffected.
- Reusable constraints live in `validate/`; domain-specific ones live next to the type.

```go
// Good example — constrained integer
var PositiveIntCodec = codex.Refine(
    codex.Int(),
    validate.PositiveInt,
)

// Good example — custom constraint with schema annotation
var ShortStringCodec = codex.String().Refine(codex.Constraint[string]{
    Name:    "maxLen(50)",
    Check:   func(v string) bool { return len(v) <= 50 },
    Message: func(v string) string { return "string too long" },
    Schema: func(s schema.Schema) schema.Schema {
        n := 50
        s.MaxLength = &n
        return s
    },
})
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

- `schema.Schema` is the root type; it carries `Type`, `Title`, `Description`, `Format`, `Example`, `Properties`, `Required`, `Enum`, `OneOf`, `Items`, and numeric/string constraint fields (`Minimum`, `Maximum`, `ExclusiveMinimum`, `ExclusiveMaximum`, `MinLength`, `MaxLength`, `Pattern`).
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
- Format constraints: `Email`, `UUID`, `URL`, `IPv4`, `IPv6`, `Date`, `DateTime`, `Slug`.
- Constraints in `validate/` must not depend on any specific codec; they depend only on `codex.Constraint[T]` and `schema.Schema`.
- All built-in `validate/` constraints carry a `Schema` transformer that annotates the codec's schema automatically when applied via `Refine`.

## OpenAPI Schema Rendering

The `render/openapi` package converts `schema.Schema` into OpenAPI 3.x schema objects. It imports only `schema` — no codec logic, no wire format.

```go
// SchemaObject converts s to an OpenAPI 3.x schema object (map[string]any).
func SchemaObject(s schema.Schema) map[string]any

// ComponentsSchemas produces the map for components.schemas in an OpenAPI doc.
func ComponentsSchemas(named map[string]schema.Schema) map[string]any

// MarshalJSON renders named schemas as JSON bytes.
func MarshalJSON(named map[string]schema.Schema) ([]byte, error)

// MarshalYAML renders named schemas as YAML bytes.
func MarshalYAML(named map[string]schema.Schema) ([]byte, error)
```

```go
// Good example — render OpenAPI schemas from codecs
yamlBytes, err := openapi.MarshalYAML(map[string]schema.Schema{
    "User": UserCodec.Schema,
    "Order": OrderCodec.Schema,
})
```

- The renderer is a pure function over `schema.Schema` — it never touches `Codec[T]` or any codec logic.
- Constraint annotations (`MinLength`, `Minimum`, `Pattern`, `Enum`, etc.) flow from `Refine` automatically when using `validate.*` constraints.
- Set `Constraint.Schema` on custom constraints to opt into schema annotation.

## HTTP Route Descriptors (`route/`)

The `route` package describes HTTP operations without any renderer or codec logic. It imports only `schema`.

```go
// Route describes a single HTTP operation.
type Route struct {
    Method, Path, OperationID, Summary, Description string
    Tags        []string
    PathParams  []Param
    QueryParams []Param
    RequestBody *Body
    Responses   []Response
}

// Body describes a request body.
// SchemaName non-empty → renderer emits $ref and registers Schema in components/schemas.
type Body struct {
    Description string
    Required    bool
    Schema      schema.Schema
    SchemaName  string
    ContentType string // defaults to "application/json"
}

// Response describes one HTTP response.
// Status is a string: "200", "201", "default", "2XX", etc.
// Schema nil → description-only response (e.g. 204, 404 without body).
type Response struct {
    Status      string
    Description string
    Schema      *schema.Schema
    SchemaName  string
    ContentType string // defaults to "application/json"
}
```

- `route` is purely a data descriptor — no HTTP server logic, no encoding.
- Use codec schemas (`UserCodec.Schema`) as `Body.Schema` / `Response.Schema`.

## Full OpenAPI 3.1 Document (`render/openapi`)

In addition to `SchemaObject`/`ComponentsSchemas`/`MarshalYAML`, `render/openapi` provides `DocumentBuilder` for emitting a full 3.1 spec.

```go
// NewDocumentBuilder returns a builder for a full OpenAPI 3.1 document.
func NewDocumentBuilder(info Info) *DocumentBuilder

// Build validates routes and produces a Document. Returns error on:
// - duplicate (method, path) pair
// - PathParam name not matching a {placeholder} in the path (or vice versa)
func (b *DocumentBuilder) Build() (Document, error)

func (d Document) MarshalJSON() ([]byte, error)
func (d Document) MarshalYAML() ([]byte, error)
```

Key rules:
- `render/openapi` imports `route` and `schema`. No codec logic.
- Path parameters are always `required: true` in the output (OpenAPI 3.1 requirement).
- `Body.SchemaName != ""` → `$ref` emitted + schema auto-registered in `components/schemas`.
- `Response.Schema == nil` → no `content` block (correct for 204, no-body errors).
- Existing `SchemaObject`, `ComponentsSchemas`, `MarshalJSON`, `MarshalYAML` remain unchanged.

## AsyncAPI 2.6 Document (`render/asyncapi`)

`render/asyncapi` produces a full AsyncAPI 2.6 document. It imports only `schema`.

```go
// NewDocumentBuilder returns a builder for a full AsyncAPI 2.6 document.
func NewDocumentBuilder(info Info) *DocumentBuilder

// Build validates channels (each must have at least one operation) and produces a Document.
func (b *DocumentBuilder) Build() (Document, error)

func (d Document) MarshalJSON() ([]byte, error)
func (d Document) MarshalYAML() ([]byte, error)
```

Key types:
```go
type ChannelItem struct {
    Description string
    Subscribe   *Operation // app receives
    Publish     *Operation // app sends
}

type Operation struct {
    Summary, Description string
    Tags    []string
    Message Message
}

type Message struct {
    Name        string
    Schema      schema.Schema
    SchemaName  string // non-empty → $ref in payload + auto-registered in components/schemas
    ContentType string
}
```

Key rules:
- `render/asyncapi` imports only `schema` — channels are independent of HTTP route concepts.
- `Message.SchemaName != ""` → `$ref` in `message.payload` + schema auto-registered.
- `Message.Schema` zero-value with empty `SchemaName` → empty payload `{}` inline.
- Each channel must have at least one of `Subscribe` or `Publish`; `Build()` rejects channels with neither.
- AsyncAPI 3.0 upgrade path: isolate version-specific serialisation so a v3 variant can be added as `render/asyncapi/v3` without breaking 2.6.


## Multi-Format Output

`Codec[T]` is format-agnostic: `Encode`/`Decode` operate on `any` (typically `map[string]any`).
The `format` package adds a thin bridge to wire formats.

```go
// One codec — three formats.
jsonFmt := format.JSON(configCodec)
yamlFmt := format.YAML(configCodec)
tomlFmt := format.TOML(configCodec)

cfg, err := jsonFmt.Unmarshal(jsonBytes)
cfg, err  = yamlFmt.Unmarshal(yamlBytes)
cfg, err  = tomlFmt.Unmarshal(tomlBytes)

out, err := tomlFmt.Marshal(cfg)
```

`Format[T]` has four methods: `Marshal(T) ([]byte, error)`, `Unmarshal([]byte) (T, error)`, `Validate(T) error`, `Schema() any`.

`format.New[T]` accepts custom marshal/unmarshal functions for formats not built-in.

**Important**: primitive codecs handle the numeric types each format produces:
- JSON produces `float64` for all numbers
- YAML produces `int` for integers, `float64` for floats
- TOML produces `int64` for integers, `float64` for floats

`Int()` handles `int`, `int64`, and integral `float64`. Add new numeric types to this list when extending.

## Explicit Validation (bidirectional)

By design, `Refine` constraints run only in the **decode direction** — they guard external input you don't control.
`Encode` is trusted: you constructed the value yourself.

When bidirectional validation is needed, call `Validate` explicitly:

```go
// Codec.Validate — no format required.
if err := userCodec.Validate(u); err != nil { ... }

// Format.Validate — delegates to the codec, format-independent.
if err := jsonFmt.Validate(u); err != nil { ... }
```

`Validate` reuses the exact same `Refine` constraints — builtin (`validate.*`) and self-defined — with no duplication. It encodes `v` to the intermediate and decodes it back, running all constraints in the decode path.

**Never change `Refine` to also wrap `Encode`.** The encode direction must remain unconstrained to preserve the trusted-code design principle.

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
