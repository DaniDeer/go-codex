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
| `codex`           | PUBLIC API: `Codec[T]`, primitives (`Int`, `Int32`, `Int64`, `Uint`, `Uint64`, `Float32`, `Float64`, `String`, `Bool`, `Bytes`, `Time`, `Date`, `Duration`, `Any`, `Pure`, `Eq`), `Nullable[T]`, `SliceOf[T]`, `StringMap[V]`, struct, `TaggedUnion`, `UntaggedUnion`, `Either[A,B]`, `Either2`, `MapCodecSafe`, `MapCodecValidated`, `Must`, `Constraint`, `Refine`, `RefineFunc`, `ValidationError`, `ValidationErrors`, `ConstraintError`, `TypeMismatchError`, `ElementError`, `KeyError`, `UnknownVariantError`, `VariantError`, `EitherError`, `ErrMissingField` | `schema`     |
| `schema`          | Schema model (pure data, no codec logic)                                                  | none                             |
| `validate`        | Reusable `Constraint` functions: numbers, strings, format, bytes                          | `codex`, `schema`                |
| `format`          | Bridges `Codec[T]` to wire formats: JSON, YAML, TOML, streaming; `FromEnv[T]` for schema-driven env var loading; `NewStreamed` for chunked/SSE writes | `codex`, `schema`, external libs |
| `route`           | HTTP route descriptors: `Route`, `Param`, `Body`, `Response`                             | `schema`                         |
| `render/internal/schemarender` | Shared schema-to-map rendering logic used by both OpenAPI and AsyncAPI renderers | `schema`               |
| `render/openapi`  | Renders `schema.Schema` as OpenAPI 3.1 `components/schemas`; `DocumentBuilder` for full spec | `schema`, `route`, `render/internal/schemarender`, external libs |
| `render/asyncapi` | Renders channels and schemas as a full AsyncAPI 2.6 document                             | `schema`, `render/internal/schemarender`, external libs |
| `api/internal`    | Shared helpers for `api/rest` and `api/events` (template variable parsing and substitution); not part of the public API | `codex` |
| `api/rest`        | Transport-agnostic REST API builder; typed Decode/Encode + OpenAPI spec; `AddSSERoute` for Server-Sent Events; `SSERouteHandle` with `BuildPath` | `codex`, `format`, `route`, `render/openapi`, `schema`, `api/internal` |
| `api/events`      | Transport-agnostic event channel builder; typed Decode/Encode + AsyncAPI spec            | `codex`, `format`, `render/asyncapi`, `schema`, `api/internal` |
| `adapters/nethttp` | net/http adapter: `Handler`, `Register`, `SSEHandler`, `RegisterSSE`, `SSEHandlerFunc`, `RequestFromContext`, `WithResponseHeaders`, `ResponseHeadersFromContext`, `WithResponseCookies`, `ResponseCookiesFromContext`, `PendingCookie`, `SetCookie`, `CookieOptions`, `Options` (with `Observer stats.Observer`) | `api/rest`, `net/http`, `stats`, `format` |
| `adapters/chi`    | chi adapter: same API surface as `adapters/nethttp` plus `SSEHandler`, `RegisterSSE`, `SSEHandlerFunc`; path vars via `chi.URLParam`; `Handler`, `Register`, `RequestFromContext`, `WithResponseHeaders`, `WithResponseCookies`, `PendingCookie`, `SetCookie`, `CookieOptions`, `Options` | `api/rest`, `net/http`, `stats`, `format`, chi lib |
| `adapters/mqtt`   | Paho MQTT adapter: `SubscribeHandler`, `Publish`, `TopicVarsFromMessage`, `TopicMismatchError`, `SubscribeError`, `ErrorKind`, `SubscribeOptions` (with `Observer stats.Observer`), `PublishOptions` (with `Observer stats.Observer`) | `api/events`, `stats`, Paho MQTT lib |
| `adapters/templ`  | templ SSR format plug-in: `Format[Props](codec, component) format.Format[Props]`, `StreamingFormat[Props](codec, component) format.Format[Props]`, `DecodeNotSupportedError`; add to a route's `ResponseFormats` to serve `text/html` alongside JSON via the existing nethttp/chi adapters | `codex`, `format`, `github.com/a-h/templ` |
| `stats`           | Observer hooks: `ValidationObserver` (codec-level, 1 method); `Observer` (adapter-level, embeds `ValidationObserver` + transport hooks); `NoopObserver`; `ReportErrors(obs, location, err)`; `ConstraintName(err)` | `codex`, `time` (stdlib only) |

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

// WithExample returns a new Codec with Schema.Example set (any value).
func (c Codec[T]) WithExample(v any) Codec[T]

// WithDeprecated returns a new Codec with Schema.Deprecated = true.
func (c Codec[T]) WithDeprecated() Codec[T]
```

These are typically chained after `Refine`:

```go
var AgeCodec = codex.Int().
    Refine(validate.RangeInt(0, 150)).
    WithTitle("Age").
    WithDescription("Age in years.").
    WithExample(25)

var LegacyIPCodec = codex.String().
    Refine(validate.IPv4).
    WithDescription("IPv4 of last login. Deprecated: use hostname.").
    WithDeprecated()
```

### `Validate`, `New`, and `Must`: Construction-Time Validation

**`Codec.Validate(v T) error`** checks a Go value by encoding it (which runs Refine constraints) and then decoding it back. Returns only the error; the value is discarded.

**`Codec.New(v T) (T, error)`** validates and returns the value. Use as a smart constructor — validate at the point of construction, get a typed result back:

```go
// Validate is declared on Codec[T]:
func (c Codec[T]) New(v T) (T, error)

// Example:
email, err := emailCodec.New(Email("user@example.com"))
if err != nil {
    return err
}
// email is valid here
```

**`Must[T any](v T, err error) T`** is a generic panic-on-error helper. Use it for package-level validated constants and test data — not for user-facing code:

```go
// Package-level constant validated at init time:
var guestUser = codex.Must(usernameCodec.New(Username("guest")))

// Test helper:
got := codex.Must(emailCodec.Decode("user@example.com"))
```

**When to use each:**

| | `Validate` | `New` | `Must` |
|---|---|---|---|
| Returns value | no | yes | yes |
| Returns error | yes | yes | panics |
| Typical use | check before store/send | smart constructor | constants, tests |

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
- Schema is inherited from `Codec[A]`.
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

## `MapCodecValidated`: Fallible Mapping with Post-Decode Validation

`MapCodecValidated[A, B any]` transforms `Codec[A]` into `Codec[B]` where both mapping directions may fail, and the mapped `B` value is validated using a provided `Codec[B]`.

```go
// MapCodecValidated creates a Codec[B] from Codec[A] and Codec[B] using two fallible mapping functions.
// After mapping to B in the decode direction, cb.Validate enforces all Refine constraints on cb.
// The resulting codec carries cb's schema.
func MapCodecValidated[A, B any](ca codex.Codec[A], cb codex.Codec[B], to func(A) (B, error), from func(B) (A, error)) codex.Codec[B]
```

- `to` is the decode direction: fallible — returns `(B, error)`.
- `from` is the encode direction: fallible — returns `(A, error)`.
- After `to(a)` succeeds, `cb.Validate(b)` runs all `Refine` constraints defined on `cb`.
- On encode, `cb.Validate(b)` is called before `from(b)` to prevent encoding invalid values.
- Schema comes from `cb` (the domain type with its constraints).
- Use when the mapping itself can fail **and** the target type `B` carries its own validation rules.

```go
type Celsius float64

var celsiusBaseCodec = codex.MapCodecSafe(
    codex.Float64().
        Refine(validate.MinFloat(-273.15)).
        Refine(validate.MaxFloat(1_000_000)),
    func(f float64) Celsius { return Celsius(f) },
    func(c Celsius) (float64, error) { return float64(c), nil },
)

var celsiusCodec = codex.MapCodecValidated(
    codex.Float64(),    // ca: wire codec
    celsiusBaseCodec,   // cb: domain codec with range constraints
    func(f float64) (Celsius, error) {
        if f != f { return 0, errors.New("NaN is not a valid temperature") }
        return Celsius(f), nil
    },
    func(c Celsius) (float64, error) { return float64(c), nil },
)
```

**When to choose `MapCodecSafe` vs `MapCodecValidated`:**

| | `MapCodecSafe` | `MapCodecValidated` |
|---|---|---|
| `to` direction | infallible `func(A) B` | fallible `func(A) (B, error)` |
| Post-decode validation | none | `cb.Validate(b)` |
| Pre-encode validation | none | `cb.Validate(b)` |
| Schema source | `ca` | `cb` |
| Typical use | newtype wrappers | domain types with constraints |

## `Downcast`: Type Assertion Helper

`Downcast[A, B any]` attempts to cast a value of type `B` to type `A` using a type assertion.

```go
// Downcast attempts to cast a value of type B to type A.
// Useful for tagged unions where variants share a common interface.
func Downcast[A any, B any](v B) (A, error)
```

- Use with `TaggedUnion` when variant types share a common interface and you need to convert to a concrete type.

## `Refine` and `Constraint`

`Refine[T]` wraps an existing `Codec[T]` with one or more `Constraint[T]` predicates. All constraints run on **both Encode and Decode** — a value that fails a constraint cannot be serialised OR deserialised. This ensures the codec is the single source of truth for validity.

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

// Refine adds constraints to a codec. Constraints run on both Encode and Decode.
// If Constraint.Schema is non-nil, it is applied to the codec's schema.
func Refine[T any](c codex.Codec[T], constraints ...codex.Constraint[T]) codex.Codec[T]
```

- `Constraint.Name` identifies the constraint in error messages.
- `Constraint.Message` produces the human-readable failure description.
- `Constraint.Schema` is optional. Set it to annotate the codec's schema (e.g. `MinLength`, `Minimum`). Nil = no-op; all existing constraints are unaffected.
- Reusable constraints live in `validate/`; domain-specific ones live next to the type.

For cross-field validation without defining a named `Constraint[T]`, use `RefineFunc`:

```go
// RefineFunc wraps a func(T) error as a constraint applied on both Encode and Decode.
// On failure, returns ConstraintError{Name:"refine", Message: err.Error()}.
func (c Codec[T]) RefineFunc(fn func(T) error) Codec[T]
```

```go
// Good example — cross-field constraint
var rangeCodec = codex.Struct[DateRange](...).
    RefineFunc(func(r DateRange) error {
        if !r.End.After(r.Start) {
            return errors.New("end must be after start")
        }
        return nil
    })
```

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

`codex.Pure` and `codex.Eq` are related fixed-value combinators:

```go
// Pure: always decodes to value, always encodes value. Schema: {enum:[value]}.
// Use for protocol version fields, derived fields set automatically.
func Pure[T any](value T) Codec[T]

// Eq: wraps base with an equality constraint. Decode: base decodes, then checks == value.
// Schema: inherits base schema with Enum set to [value].
// Use a typed base codec so wire-type coercion is handled: Eq(Int(), 42) accepts JSON float64(42).
func Eq[T comparable](base Codec[T], value T) Codec[T]
```

```go
// Good example — CloudEvents spec version (always "1.0")
var specVersionCodec = codex.Pure("1.0")

// Good example — only accept one specific event type
var orderEventTypeCodec = codex.Eq(codex.String(), "com.example.order.placed")
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
- Use `codex.RequiredField` / `codex.OptionalField` / `codex.DefaultField` instead of `Field{..., Required: true/false}` for clearer intent:

```go
// Preferred — intent explicit from constructor name
var PointCodec = codex.Struct[Point](
    codex.RequiredField[Point, float64]("x", codex.Float64(),
        func(p Point) float64 { return p.X },
        func(p *Point, v float64) { p.X = v },
    ),
    codex.OptionalField[Point, float64]("y", codex.Float64(),
        func(p Point) float64 { return p.Y },
        func(p *Point, v float64) { p.Y = v },
    ),
    // DefaultField: absent key uses "info"; default is also reflected in schema
    codex.DefaultField[Config, string]("log_level", codex.String(), "info",
        func(c Config) string { return c.LogLevel },
        func(c *Config, v string) { c.LogLevel = v },
    ),
)
```

`DefaultField` sets `Required: false` and stores the default as `*F` (pointer, to distinguish zero-value defaults from "no default"). The default is reflected in `Schema.Default` and rendered as `default` in OpenAPI/AsyncAPI.

## Union Codec: Tagged and Untagged Unions

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
- `TaggedUnion` automatically sets `Schema.Discriminator = &schema.DiscriminatorSchema{PropertyName: tag}` on the returned codec's schema. This is reflected in OpenAPI/AsyncAPI specs via the shared `render/internal/schemarender` package.

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

`codex.UntaggedUnion` is the complement for cases where no discriminator field is present in the encoded form.

```go
// UntaggedVariant[T] pairs a documentation name with a Codec[T].
type UntaggedVariant[T any] struct {
    Name  string
    Codec Codec[T]
}

// UntaggedUnion tries each variant in order during decode (first match wins).
// `which` selects the encode branch by 0-based variant index.
func UntaggedUnion[T any](which func(T) int, variants ...UntaggedVariant[T]) Codec[T]
```

- Schema: `{oneOf: [...variant schemas...]}` — no `discriminator` block.
- Decode failure (all variants fail): returns `EitherError{Errors: [...]}`

`codex.Either2` produces a `Codec[Either[A,B]]` that tries codec A first, then B.

```go
type Either[A, B any] struct {
    Left  *A  // non-nil if decoded as A
    Right *B  // non-nil if decoded as B
}

func Either2[A, B any](ca Codec[A], cb Codec[B]) Codec[Either[A, B]]
```

- Decode: try `ca`; if it fails, try `cb`; if both fail, return `EitherError{Errors: []error{errA, errB}}`.
- Encode: if `Left != nil`, use `ca`; else use `cb`.
- Schema: `{oneOf: [schemaA, schemaB]}`.
- Left branch wins on ambiguity (order-dependent, documented).

## Schema Model

The `schema` package defines pure data structures that describe a codec. No codec logic lives here.

- `schema.Schema` is the root type; it carries `Type`, `Title`, `Description`, `Format`, `Example`, `Properties` (ordered `[]schema.Property`), `Required`, `Enum`, `OneOf`, `Items`, and numeric/string constraint fields (`Minimum`, `Maximum`, `ExclusiveMinimum`, `ExclusiveMaximum`, `MinLength`, `MaxLength`, `Pattern`).
- `schema.Property` is `{Name string; Schema Schema}` — using a slice instead of a map preserves registration order for deterministic YAML/JSON output.
- Use `s.Prop(name)` to look up a property by name (returns `(Schema, bool)`).
- Additional fields on `schema.Schema`:
  - `Nullable bool` — marks the value as accepting null; renders as `nullable: true` in OpenAPI/AsyncAPI.
  - `AdditionalProperties *bool` — nil = unset (spec default), `false` = no extra properties, `true` = any allowed.
  - `Discriminator *schema.DiscriminatorSchema` — describes the polymorphism tag for `TaggedUnion` schemas. Set automatically by `TaggedUnion`.
  - `Deprecated bool` — renders as `deprecated: true` in OpenAPI/AsyncAPI. Set by `Codec.WithDeprecated()`.
  - `Default any` — the declared default value for a field. Set by `DefaultField` and rendered as `default` in generated schemas.
- `schema.DiscriminatorSchema` holds `PropertyName string` and optional `Mapping map[string]string`.
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

All decode failures are concrete structured types. Every type implements `error`, `slog.LogValuer`, and (where applicable) `Unwrap`.

| Type | Returned by | Key fields |
|------|-------------|------------|
| `ValidationErrors` | `Struct` decode | `[]ValidationError`; `Unwrap() []error` for `errors.Is`/`As` traversal |
| `ValidationError` | each failing field in `Struct` decode | `Field string`, `Err error`; `Unwrap()` returns `Err` |
| `ConstraintError` | `Refine`/`RefineFunc` on any codec when constraint fails | `Name string`, `Message string` |
| `TypeMismatchError` | any codec receiving wrong Go type | `Expected string`, `Got string` |
| `ElementError` | `SliceOf` decode/encode | `Index int`, `Err error`; `Unwrap()` returns `Err` |
| `KeyError` | `StringMap` decode/encode | `Key string`, `Err error`; `Unwrap()` returns `Err` |
| `UnknownVariantError` | `TaggedUnion` when tag value has no matching codec | `Tag string`, `Variant string`; no `Unwrap` |
| `VariantError` | `TaggedUnion` when a known variant fails to decode/encode | `Tag string`, `Variant string`, `Err error`; `Err` is always non-nil; `Unwrap()` returns `Err` |
| `EitherError` | `Either2`/`UntaggedUnion` when all branches fail | `Errors []error`; `Unwrap() []error` for `errors.Is`/`As` traversal |
| `ErrMissingField` | required `Field` when key absent | exported sentinel; use `errors.Is` |

- Struct decode collects **all** field errors before returning — the error is always `ValidationErrors`, never a partial slice.
- Use `errors.As(err, &ve)` to extract `ValidationErrors`. Then inspect each `ValidationError.Err` for the underlying cause.
- `ValidationErrors.Unwrap() []error` enables `errors.Is`/`errors.As` to traverse the full list directly.
- Encode errors are exceptional; prefer designs where encoding is total.

```go
// Struct decode: collect all field errors, inspect constraint name.
_, err := MyCodec.Decode(input)
var ve codex.ValidationErrors
if errors.As(err, &ve) {
    for _, fe := range ve {
        var ce codex.ConstraintError
        if errors.As(fe.Err, &ce) {
            fmt.Printf("field %q: constraint %q failed: %s\n", fe.Field, ce.Name, ce.Message)
        }
        if errors.Is(fe.Err, codex.ErrMissingField) {
            fmt.Printf("field %q: required but absent\n", fe.Field)
        }
    }
}

// slog: all structured error types implement slog.LogValuer; wrapping types
// use slog.Any("cause", e.Err) so nested LogValue() is preserved.
logger.Error("decode failed", slog.Any("validation_errors", ve))
// → validation_errors.name.constraint=non-empty validation_errors.name.message="..."
```

See `examples/error-types/` for a runnable demo of every error type with `errors.As` and slog.
See `examples/decode-errors/` for struct validation errors and HTTP 400 response patterns.

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

### Time and Date Codecs

```go
// Codec[time.Time] — RFC 3339 strings; schema {type:string, format:date-time}
var CreatedAtCodec = codex.Time()

// Codec[time.Time] — date-only strings (2006-01-02); schema {type:string, format:date}
var BirthDateCodec = codex.Date()
```

### Nullable Codec

Wraps any codec to handle pointer fields (`*T`). `nil` encodes as JSON null.
The generated schema inherits the inner schema and sets `nullable: true`.

```go
// Codec[*string] — accepts nil (null) or a string value
var NoteCodec = codex.Nullable(codex.String())
```

### Bytes Codec

Encodes `[]byte` as a base64 standard-encoded string.
Schema: `{type:string, format:byte}`.

```go
var AvatarCodec = codex.Bytes()
```

### StringMap Codec

Encodes `map[string]V` where all values share the same codec.
Schema: `{type:object, additionalProperties:{...valueSchema}}`.

```go
var TagsCodec = codex.StringMap(codex.String())         // map[string]string
var CountsCodec = codex.StringMap(codex.Int())          // map[string]int
```

### Optional Field in Object

Set `Required: false` on the field. The field is omitted from the encoded object when missing during decode; no error is returned.

## Validation

- `validate/` contains reusable `Constraint[T]` factory functions.
- `int` constraints: `PositiveInt`, `NegativeInt`, `NonZeroInt`, `MinInt(n)`, `MaxInt(n)`, `RangeInt(min, max)`.
- `int32` constraints: `PositiveInt32`, `NegativeInt32`, `MinInt32(n)`, `MaxInt32(n)`, `RangeInt32(min, max)`.
- `int64` constraints: `PositiveInt64`, `NegativeInt64`, `MinInt64(n)`, `MaxInt64(n)`, `RangeInt64(min, max)`.
- `uint` constraints: `PositiveUint`, `MinUint(n)`, `MaxUint(n)`, `RangeUint(min, max)`. No `NegativeUint` — unsigned type.
- `uint64` constraints: `PositiveUint64`, `MinUint64(n)`, `MaxUint64(n)`, `RangeUint64(min, max)`.
- Float constraints: `PositiveFloat`, `NegativeFloat`, `NonZeroFloat`, `MinFloat(n)`, `MaxFloat(n)`, `RangeFloat(min, max)`.
- `time.Duration` constraints: `PositiveDuration`, `NonNegativeDuration`, `MinDuration(d)`, `MaxDuration(d)`. No schema annotation (no JSON Schema standard for duration bounds).
- String constraints: `NonEmptyString`, `MinLen(n)`, `MaxLen(n)`, `Pattern(re)`, `OneOf(values...)`.
- Numeric string constraints (for path/topic variables): `IntString` (valid signed integer), `PositiveIntString` (> 0), `NonNegativeIntString` (≥ 0), `IntStringInRange(min, max)` (bounded). No schema annotation. Designed for use in `PathParamCodecs`/`TopicParamCodecs`.
- Protocol path/topic constraints: `MQTTTopic` (non-empty, no null byte, max 65535 UTF-8 bytes), `MQTTPublishTopic` (same + no `+`/`#` wildcards), `HTTPPath` (must start with `/`, no spaces or null bytes, OpenAPI-style `{param}` allowed). None carry schema annotations (no JSON Schema standard keywords for these rules).
- Format constraints: `Email`, `UUID`, `URL`, `URLWithSchemes(schemes...)`, `URI`, `Hostname`, `IPv4`, `IPv6`, `IP`, `Date`, `Time`, `DateTime`, `SemVer`, `Slug`, `CIDR`.
- Byte-size constraints: `MaxBytes(n)`, `MinBytes(n)` — validate decoded `[]byte` length; no schema annotation (JSON Schema has no standard keyword for decoded-byte-count limits).
- Constraints in `validate/` must not depend on any specific codec; they depend only on `codex.Constraint[T]` and `schema.Schema`.
- All built-in `validate/` constraints carry a `Schema` transformer that annotates the codec's schema automatically when applied via `Refine`, **except** `MaxBytes`/`MinBytes` and Duration constraints (runtime-only).

## OpenAPI Schema Rendering

The `render/openapi` package converts `schema.Schema` into OpenAPI 3.x schema objects. It delegates to the shared `render/internal/schemarender` package — no codec logic, no wire format.

The shared `render/internal/schemarender.SchemaObject(s schema.Schema) map[string]any` function handles all schema fields including `Nullable`, `AdditionalProperties`, `AdditionalPropertiesSchema`, `Discriminator`, `OneOf`, numeric bounds, string constraints, and enum. Both `render/openapi` and `render/asyncapi` use it; adding a new `schema.Schema` field requires updating only `schemarender`.

When `AdditionalPropertiesSchema` is set on a `schema.Schema`, it renders as a schema object (`additionalProperties: {type: ...}`). This takes precedence over the boolean `AdditionalProperties` field. Used by `StringMap[V]` codec.

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
    Tags         []string
    PathParams   []Param
    QueryParams  []Param
    CookieParams []Param
    HeaderParams []Param
    RequestBody  *Body
    Responses    []Response
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
    Parameters  map[string]Parameter // {varName} → Parameter; auto-populated by api/events builder
    Subscribe   *Operation           // app receives
    Publish     *Operation           // app sends
}

type Parameter struct {
    Description string
    Schema      schema.Schema // zero-value → default {type: string} in spec output
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
- `ChannelItem.Parameters` non-empty → `parameters:` block emitted in spec. Schema zero-value → `{type: string}`.
- Each channel must have at least one of `Subscribe` or `Publish`; `Build()` rejects channels with neither.
- AsyncAPI 3.0 upgrade path: isolate version-specific serialisation so a v3 variant can be added as `render/asyncapi/v3` without breaking 2.6.

## REST API Builder (`api/rest`)

`api/rest` is a transport-agnostic REST API builder layered on top of `render/openapi`. It imports **no HTTP library**. Users receive typed `Decode`/`Encode` helpers per route; they wire those into any HTTP framework.

```go
// NewBuilder returns a Builder for REST route registration.
// opts are applied in order; use WithPathCodec or WithPathConstraints to validate paths.
func NewBuilder(info Info, opts ...BuilderOption) *Builder

// WithPathCodec sets a codec used to validate every path registered via AddRoute.
// If validation fails, AddRoute returns an InvalidPathError immediately.
func WithPathCodec(c codex.Codec[string]) BuilderOption

// WithPathConstraints is a convenience wrapper: builds codex.String() refined with cons
// and delegates to WithPathCodec.
func WithPathConstraints(cons ...codex.Constraint[string]) BuilderOption

// AddRoute is a free function (generic type params require free functions in Go).
// Registers a route; returns a RouteHandle with frozen descriptor and typed helpers.
// Returns InvalidPathError immediately if path codec validation fails.
func AddRoute[Req, Resp any](
    b *Builder,
    method, path string,
    reqCodec codex.Codec[Req],
    respCodec codex.Codec[Resp],
    config RouteConfig,
) (*RouteHandle[Req, Resp], error)

// InvalidPathError is returned by AddRoute when path codec validation fails.
// Use errors.As to extract it and inspect Path or the underlying constraint Err.
type InvalidPathError struct {
    Path string
    Err  error
}

// OpenAPISpec builds a full OpenAPI 3.1 document from all registered routes.
// Returns an error if there are dangling $refs.
func (b *Builder) OpenAPISpec() (openapi.Document, error)
```

Example — enforce HTTP path format:

```go
import "github.com/DaniDeer/go-codex/validate"

b := rest.NewBuilder(info, rest.WithPathConstraints(validate.HTTPPath))

createUser, err := rest.AddRoute[CreateUserReq, User](b, "POST", "/users", reqCodec, respCodec, cfg)
if err != nil {
    // err is an InvalidPathError — path failed validation immediately
    var pathErr rest.InvalidPathError
    errors.As(err, &pathErr) // pathErr.Path, pathErr.Err available
    return err
}
```

`RouteHandle[Req, Resp]`:
- `Descriptor route.Route` — frozen at registration; use for framework routing
- `Decode(body []byte) (Req, error)` — JSON decode + Refine validation
- `Encode(resp Resp) ([]byte, error)` — JSON encode
- `BuildPath(vars map[string]string) (string, error)` — substitutes `{varName}` placeholders in the path template, validating each against its `PathParam.Codec`. After substitution, if a builder-level `pathCodec` is set, the final assembled path is re-validated against it (no template stripping — this is the real path). Returns `MissingPathVarError` for missing variables, `PathParamError` for per-variable codec failures, `InvalidPathError` if the final path fails the builder codec. Extra keys in `vars` are silently ignored.

`PathParamError` is returned by `BuildPath` when a path variable fails its codec:

```go
type PathParamError struct {
    Name  string // the {varName} that failed
    Value string // the value that was rejected
    Err   error  // the underlying codec error
}
```

`MissingPathVarError` is returned by `BuildPath` when a template variable has no entry in `vars`:

```go
type MissingPathVarError struct {
    Name string // the variable name (without braces) that had no value
}
```

`InvalidPathParamError` is returned by `AddRoute` when a `PathParams` entry names a variable not in the path template:

```go
type InvalidPathParamError struct {
    Name string // the variable name (without braces) not found in the template
    Path string // the path template that was validated against
}
```

`RouteConfig` fields: `OperationID`, `Summary`, `Description`, `Tags`, `PathParams []PathParam`, `QueryParams []QueryParam`, `CookieParams []CookieParam`, `HeaderParams []HeaderParam`, `ResponseHeaderParams []ResponseHeaderParam`, `ResponseCookieParams []ResponseCookieParam`, `ReqSchemaName`, `RespStatus` (default POST→"201", others→"200"), `RespDescription`, `RespSchemaName`, `Responses []ResponseMeta`.

`PathParam{Name, Description, Codec *codex.Codec[string]}` — optional per-variable metadata. `Name` must correspond to a `{varName}` placeholder in the path template. `Codec` (pointer, `nil` = no validation) provides runtime validation and auto-flows its schema into the OpenAPI spec. An unknown `Name` causes `AddRoute` to return `InvalidPathParamError` immediately.

`QueryParam{Name, Description string, Required bool, Codec *codex.Codec[string]}` — optional query parameter metadata. `Name` is the query key (no template syntax). `Codec` (pointer, `nil` = no validation) provides runtime validation via `RouteHandle.ValidateQuery` and auto-flows its schema into the OpenAPI spec. Unlike `PathParam`, query params are not auto-generated for template placeholders — only entries explicitly listed in `QueryParams` appear in the spec.

```go
type QueryParam struct {
    Name        string
    Description string
    Required    bool
    Codec       *codex.Codec[string] // nil = no validation
}
```

`RouteHandle.ValidateQuery(params map[string]string) error` — validates each `QueryParam` with a non-nil `Codec` against the provided map. Missing keys are silently skipped (no error). Returns `QueryParamError` on first failure.

`RouteHandle.ValidateQueryMulti(params map[string][]string) error` — same as `ValidateQuery` but accepts the multi-value map returned by `r.URL.Query()`. Validates the first value per key. Use when handling repeated query keys (`?tags=a&tags=b`). Called by the adapter when `Options.MultiValueQueryParams` is true.

```go
type QueryParamError struct {
    Name  string
    Value string
    Err   error // wrapped codec validation error
}
```

`CookieParam{Name, Description string, Required bool, Codec *codex.Codec[string]}` — optional cookie parameter metadata. Follows the same pattern as `QueryParam`. `Codec` provides runtime validation via `RouteHandle.ValidateCookies` and auto-flows its schema into the OpenAPI spec (`in: cookie`).

```go
type CookieParam struct {
    Name        string
    Description string
    Required    bool
    Codec       *codex.Codec[string] // nil = no validation
}

type CookieParamError struct {
    Name  string
    Value string
    Err   error
}
```

`HeaderParam{Name, Description string, Required bool, Codec *codex.Codec[string]}` — optional HTTP header parameter metadata. Follows the same pattern as `QueryParam`. `Codec` provides runtime validation via `RouteHandle.ValidateHeaders` and auto-flows its schema into the OpenAPI spec (`in: header`). Do **not** declare `Accept`, `Content-Type`, or `Authorization` as `HeaderParam` entries — OpenAPI reserves these for `requestBody` and security schemes respectively.

```go
type HeaderParam struct {
    Name        string
    Description string
    Required    bool
    Codec       *codex.Codec[string] // nil = no validation
}

type HeaderParamError struct {
    Name  string
    Value string
    Err   error
}

// UnsupportedMediaTypeError — returned by the adapter when Content-Type does not match expected.
type UnsupportedMediaTypeError struct {
    Got      string // actual Content-Type (without parameters)
    Expected string // configured expected type (default "application/json")
}

// BodyTooLargeError — returned by the adapter when body exceeds Options.MaxBodyBytes.
type BodyTooLargeError struct {
    Limit int64 // configured byte limit
}

// NotAcceptableError — returned by the adapter when client's Accept header has no match.
type NotAcceptableError struct {
    Accept    string   // client's Accept header value
    Supported []string // content types the route can produce
}
```

`ResponseHeaderParam{Name, Description string, Required bool, Codec *codex.Codec[string]}` — declares an outgoing response header. Symmetric to `HeaderParam` but for the server side. Codec is validated by the adapter **after** the handler returns; a violation returns `ResponseHeaderParamError` and the adapter responds with 500 (server contract violation). Schema auto-flows into `responses[status].headers` in the OpenAPI spec. Use in `RouteConfig.ResponseHeaderParams`.

`ResponseCookieParam{Name, Description string, Required bool, Codec *codex.Codec[string]}` — declares a `Set-Cookie` header returned in the primary success response. Same flow as `ResponseHeaderParam` but for cookies. The adapter validates cookie values via `ValidateResponseCookies` after the handler returns. The handler deposits cookies via `WithResponseCookies(ctx, ...PendingCookie)`. A codec violation returns `ResponseCookieParamError` and adapter responds with 500. Schema flows into `responses[status].headers["Set-Cookie"]` in spec (OpenAPI 3.1 has no first-class response cookie object). Use in `RouteConfig.ResponseCookieParams`.

```go
type ResponseHeaderParam struct {
    Name        string
    Description string
    Required    bool
    Codec       *codex.Codec[string] // nil = no validation
}

type ResponseHeaderParamError struct {
    Name  string
    Value string
    Err   error
}

type ResponseCookieParam struct {
    Name        string
    Description string
    Required    bool
    Codec       *codex.Codec[string] // nil = no validation
}

type ResponseCookieParamError struct {
    Name  string
    Value string
    Err   error
}
```

**Codec schema → spec**: `PathParam.Codec` schema automatically flows into the OpenAPI path parameter spec. `QueryParam.Codec`, `CookieParam.Codec`, and `HeaderParam.Codec` schemas automatically flow into their respective OpenAPI parameter specs (`in: query`, `in: cookie`, `in: header`). `ResponseHeaderParam.Codec` schema flows into `responses[status].headers`. `ResponseCookieParam.Codec` flows into `responses[status].headers["Set-Cookie"]`. When `Codec` is nil, the parameter is still declared (minimal entry with no schema).

Key rules:
- `api/rest` encodes responses as JSON by default. To enable content negotiation, pass `format.Format[Resp]` values as variadic trailing args to `AddRoute`; the adapter picks the format matching the client's `Accept` header (406 on mismatch via `rest.NotAcceptableError`).
- `route.Response.ContentTypes []string` — when non-empty, the OpenAPI renderer emits the schema under all listed content types in `responses[N].content`. Set automatically from registered `ResponseFormats` content types.
- Request body (`RequestBody`) is only added to the spec for `POST`, `PUT`, `PATCH`.
- The descriptor is built and frozen at `AddRoute` call time; later config mutations do not affect the registered route.
- Path validation is **immediate**: if a `pathCodec` is set, `AddRoute` returns `InvalidPathError` at call time. The route is not registered on failure.
- **Template-transparent validation**: before running the path codec, `{varName}` placeholders are replaced with the literal `x` (e.g. `"/users/{id}"` → `"/users/x"`). Constraints run on the structural shape of the path, not the template syntax. This means any path constraint — including ones that do not mention braces — works correctly on parameterised routes. The stored `Descriptor.Path` is always the original template.
- **Final path re-validation**: `BuildPath` re-validates the fully assembled path (e.g. `"/users/hello world"`) against the builder-level `pathCodec` after substitution. This catches values that pass their `PathParam.Codec` but violate the global path constraint (e.g. a space introduced by a loose param codec). Returns `InvalidPathError{Path: finalPath, Err: ...}`.
- `Info = openapi.Info` and `Server = openapi.Server` are type aliases to avoid drift.
- `api/rest` may import `codex`, `format`, `route`, `render/openapi`, `schema`. No `net/http`.
- `adapters/nethttp` wraps `RouteHandle` for `net/http`. It imports `api/rest`, `net/http`, `format`, and `stats`.
  - `Handler[Req,Resp](handle, fn, opts Options) http.Handler` — decodes body (POST/PUT/PATCH), calls fn, encodes response; instruments via `opts.Observer`.
  - `Options.ErrorHandler func(w, r, status, err)` — custom error response writer; default is JSON `{"error":"..."}`.
  - `Options.Observer stats.Observer` — receives `RecordRequest` and `RecordValidationError` events; defaults to `stats.NoopObserver`.
  - `Options.MaxBodyBytes int64` — max request body size for POST/PUT/PATCH; 0 = default (1 MiB). Requests exceeding the limit are rejected with 413 Request Entity Too Large and a `rest.BodyTooLargeError`.
  - `Options.ContentType string` — expected `Content-Type` for body-bearing methods; default `"application/json"`. Wrong type → 415 Unsupported Media Type with a `rest.UnsupportedMediaTypeError`. Parameters (e.g. `; charset=utf-8`) are stripped before comparison.
  - `Options.MultiValueQueryParams bool` — when true, passes `r.URL.Query()` (`map[string][]string`) to `ValidateQueryMulti` instead of the flat single-value map. Use for repeated keys like `?tags=a&tags=b`.
  - `Register[Req,Resp](mux, handle, fn, opts Options)` — registers on `*http.ServeMux` via Go 1.22+ `"METHOD /path"` pattern.
  - `RequestFromContext(ctx) (*http.Request, bool)` — retrieves the underlying `*http.Request` for path params, headers, etc. Use `r.PathValue("id")` for Go 1.22+ path segments.
  - Non-body methods (GET/HEAD/DELETE): fn called with zero value of Req; body reader not touched.
  - **Content-Type check**: for POST/PUT/PATCH, `Content-Type` is checked before reading body; 415 on mismatch.
  - **Content negotiation**: when `handle.ResponseFormats` is non-empty, the adapter reads `Accept`, picks the matching format, encodes with it. No match → 406 `rest.NotAcceptableError`. `*/*` picks first format. The chosen format's `ContentType()` is set as the response `Content-Type` header.
  - **Query validation**: `ValidateQuery` is called automatically before the handler function. Codec-backed `QueryParam` entries are validated from `r.URL.Query()`; 400 is returned on failure. Use `ValidateQueryMulti` (via `Options.MultiValueQueryParams`) for repeated keys.
  - **Cookie validation**: `ValidateCookies` is called automatically before the handler function. Codec-backed `CookieParam` entries are validated from `r.Cookies()`; 400 is returned on failure.
  - **Header validation**: `ValidateHeaders` is called automatically before the handler function. Codec-backed `HeaderParam` entries are validated from `r.Header`; 400 is returned on failure. Observer reports with `location="cookie"` or `location="header"` respectively.
  - **Response header validation**: `ValidateResponseHeaders` is called automatically after the handler function succeeds. Codec-backed `ResponseHeaderParam` entries are validated against collected response headers; 500 is returned on failure (server contract violation). Observer reports with `location="response_header"`.
  - **`WithResponseHeaders(ctx, h http.Header)`** — mutates the pre-allocated header map in `ctx` in-place; call from inside `HandlerFunc` to attach extra response headers. Returns nothing (void); maps are reference types. `ResponseHeadersFromContext(ctx) (http.Header, bool)` retrieves the collected headers (useful for testing/middleware).
  - **Response cookie validation**: `ValidateResponseCookies` is called automatically after the handler function succeeds. Codec-backed `ResponseCookieParam` entries are validated against collected cookie values; 500 is returned on failure. Observer reports with `location="response_cookie"`.
  - **`WithResponseCookies(ctx, cookies ...PendingCookie)`** — deposits `PendingCookie` values into `ctx`; call from inside `HandlerFunc` to queue `Set-Cookie` headers. The adapter validates values, then writes `Set-Cookie` headers on success. `ResponseCookiesFromContext(ctx) ([]PendingCookie, bool)` retrieves queued cookies (useful for testing/middleware).
  - **`PendingCookie{Name, Value string, Opts CookieOptions}`** — a cookie queued for response writing. `Opts` controls `Secure`, `HttpOnly`, `SameSite`, `MaxAge`, `Path`, `Domain`. `Opts.Codec` is cleared by the adapter before writing (route-level validation already ran via `ValidateResponseCookies`).
  - **`SetCookie(w, name, value, opts CookieOptions) error`** — writes a `Set-Cookie` header with secure defaults (`Secure=true`, `HttpOnly=true`, `SameSite=Strict`, `Path="/"`). If `opts.Codec` is non-nil, value is validated before writing; on failure returns `rest.CookieParamError` without writing the header. Use the same `*codex.Codec[string]` as the read-side `CookieParam` for symmetric validation.
    - `CookieOptions.Insecure bool` — omit `Secure` (for non-TLS, e.g. localhost dev)
    - `CookieOptions.AllowJS bool` — omit `HttpOnly` (for JS-readable cookies, e.g. CSRF tokens)
    - `CookieOptions.SameSite http.SameSite` — override; defaults to `SameSiteStrictMode`
    - `CookieOptions.MaxAge int` — 0 = session; negative = delete immediately
    - `CookieOptions.Path string` — defaults to `"/"`
    - `CookieOptions.Domain string` — defaults to current host
    - `CookieOptions.Codec *codex.Codec[string]` — optional write-side validator; returns `rest.CookieParamError` on failure without writing header
  - **`SSEHandler[Req,Event](handle *rest.SSERouteHandle[Req,Event], fn SSEHandlerFunc[Req,Event], opts Options) http.Handler`** — SSE streaming handler. Sets `Content-Type: text/event-stream`, `Cache-Control: no-cache`, `Connection: keep-alive`. Calls `fn`; the `send` func validates each event via the codec before writing `data: <json>\n\n` and flushing.
  - **`RegisterSSE[Req,Event](mux, handle, fn, opts Options)`** — registers the SSE handler on `*http.ServeMux` under `GET <path>`.
  - **`SSEHandlerFunc[Req,Event]`** = `func(ctx context.Context, req Req, send func(Event) error) error` — typed handler signature for SSE. `send` returns error if codec rejects the event; no bytes are written. Honour `ctx.Done()` for clean disconnect handling.

- `adapters/chi` wraps `RouteHandle` for `github.com/go-chi/chi/v5`. It has the same API surface as `adapters/nethttp` with one key difference: path variables are extracted via `chi.URLParam(r, "name")` instead of `r.PathValue("name")`. Chi uses the same `{param}` placeholder syntax as go-codex path templates.
  - `Handler[Req,Resp](handle, fn, opts Options) http.HandlerFunc` — same pipeline as nethttp.
  - `Register[Req,Resp](r gochi.Router, handle, fn, opts Options)` — calls `r.Method(method, path, handler)`.
  - `SSEHandler[Req,Event](handle, fn, opts Options) http.HandlerFunc` — SSE streaming handler; same contract as `nethttp.SSEHandler`.
  - `RegisterSSE[Req,Event](r gochi.Router, handle, fn, opts Options)` — calls `r.Get(path, handler)`.
  - `RequestFromContext(ctx) (*http.Request, bool)` — retrieve request for `chi.URLParam(r, "id")`.
  - All validation, response header/cookie, content negotiation, SSE features are identical to `adapters/nethttp`.
  - `SetCookie`, `CookieOptions`, `PendingCookie`, `WithResponseCookies`, `WithResponseHeaders` all present with identical signatures.

- `adapters/templ` is a plug-in for the templ SSR library (`github.com/a-h/templ`). It does **not** implement an HTTP adapter — it produces a `format.Format[Props]` value that participates in the existing content negotiation pipeline of `adapters/nethttp` and `adapters/chi`.
  - `Format[Props](c codex.Codec[Props], component func(Props) atempl.Component) format.Format[Props]` — wraps a templ component as a `format.Format` with `ContentType: "text/html; charset=utf-8"`. Add it to a route's `ResponseFormats`.
  - `StreamingFormat[Props](c codex.Codec[Props], component func(Props) atempl.Component) format.Format[Props]` — streaming variant built with `format.NewStreamed`; renders directly to `ResponseWriter` without buffering.
  - `DecodeNotSupportedError{ContentType string}` — returned by the format's `Unmarshal`; HTML cannot be decoded back to a typed value. Use `errors.As` to detect it.
  - Props are validated via the codec's `Refine` constraints before the component renders. Validation failure → HTTP 500 via the hosting adapter; the component is never called with invalid data.
  - The component receives `context.Background()` during rendering; pass all data the component needs through the Props struct.
  - Works with both `adapters/nethttp` and `adapters/chi` — no chi-specific variant needed.

  **Composability with SSE (HTMX HTML-over-the-wire):** `adapttempl.Format` can be passed as an `EventFormat` to `rest.AddSSERoute`. Each SSE `data:` line contains a rendered HTML fragment — events with invalid props are rejected before the component renders. `adapttempl.StreamingFormat` can be used as a `ResponseFormat` on any regular route for chunked HTML delivery. See `examples/adapters-streaming-sse-templ` for both patterns together.

  ```go
  import (
      adapttempl "github.com/DaniDeer/go-codex/adapters/templ"
      nethttp    "github.com/DaniDeer/go-codex/adapters/nethttp"
      atempl     "github.com/a-h/templ"
      "github.com/DaniDeer/go-codex/format"
  )

  // Define a templ component (or use a real templ-generated one):
  func ArticleCard(p ArticleProps) atempl.Component {
      return atempl.ComponentFunc(func(ctx context.Context, w io.Writer) error {
          _, err := fmt.Fprintf(w, "<h2>%s</h2>", html.EscapeString(p.Title))
          return err
      })
  }

  // Register both formats on one route:
  articleRoute, _ := rest.AddRoute(b, "GET", "/article",
      articleReqCodec, articlePropsCodec,
      rest.RouteConfig{},
      adapttempl.Format(articlePropsCodec, ArticleCard), // Accept: text/html
      format.JSON(articlePropsCodec),                     // Accept: application/json
  )

  // One handler, one registration — adapter handles content negotiation:
  nethttp.Register(mux, articleRoute, func(ctx context.Context, req Req) (ArticleProps, error) {
      return svc.GetArticle(ctx, req.ID)
  }, nethttp.Options{})
  ```

- `StreamingFormat[Props](c codex.Codec[Props], component func(Props) atempl.Component) format.Format[Props]` — streaming variant that renders directly to `ResponseWriter` via `format.NewStreamed`, bypassing the intermediate `bytes.Buffer`. Use when you want true chunked delivery of large HTML responses.

### `adapters/nethttp` + `adapters/chi` — SSE (Server-Sent Events)

Both adapters expose `SSEHandler` and `RegisterSSE` for streaming Server-Sent Events from a `rest.SSERouteHandle`.

```go
import (
    nethttp "github.com/DaniDeer/go-codex/adapters/nethttp"
    "github.com/DaniDeer/go-codex/api/rest"
)

// Register an SSE route — always GET.
sensorRoute, err := rest.AddSSERoute[emptyReq, sensorReading](
    b, "/sensors/{id}/readings",
    emptyReqCodec, sensorReadingCodec,
    rest.RouteConfig{
        OperationID: "streamSensor",
        PathParams: []rest.PathParam{
            {Name: "id", Description: "Sensor ID", Codec: &sensorIDCodec},
        },
    },
)

// Wire onto net/http.
nethttp.RegisterSSE(mux, sensorRoute,
    func(ctx context.Context, _ emptyReq, send func(sensorReading) error) error {
        r, _ := nethttp.RequestFromContext(ctx)
        id := r.PathValue("id")
        for {
            select {
            case <-ctx.Done():
                return nil // client disconnected
            default:
            }
            if err := send(svc.Read(id)); err != nil {
                return err // codec rejected value — no bytes written
            }
            time.Sleep(time.Second)
        }
    }, nethttp.Options{Observer: obs})
```

Key contract rules:
- `send(event)` validates via the event codec → encodes to JSON → writes `data: <json>\n\n` → flushes. If validation fails, `send` returns an error without writing anything; the stream remains clean.
- `ctx.Done()` signals client disconnects; always respect it to avoid goroutine leaks.
- `SSERouteHandle.BuildPath(vars)` validates path variables via per-param codecs and the builder-level path codec — same contract as `RouteHandle.BuildPath`.
- `rest.AddSSERoute` also accepts `...format.Format[Event]` as variadic trailing args (`EventFormats`); the adapter uses the first format for event data serialisation (defaults to JSON when empty).
- The route appears in the OpenAPI spec as a GET operation with `Content-Type: text/event-stream`.
- Stats observer receives `RecordValidationError("response", constraint, "event")` for each rejected event.
- For chi: use `chiadapter.SSEHandler` / `chiadapter.RegisterSSE`; path vars via `chi.URLParam(r, "id")`.



`api/events` is a transport-agnostic event channel builder layered on top of `render/asyncapi`. It imports **no messaging library**. Users receive typed `Decode`/`Encode` helpers per channel; they wire those into any message broker.

```go
// NewBuilder returns a Builder for event channel registration.
// opts are applied in order; use WithTopicCodec or WithTopicConstraints to validate topics.
func NewBuilder(info Info, opts ...BuilderOption) *Builder

// WithTopicCodec sets a codec used to validate every topic registered via AddChannel.
// If validation fails, AddChannel returns an InvalidTopicError immediately.
func WithTopicCodec(c codex.Codec[string]) BuilderOption

// WithTopicConstraints is a convenience wrapper: builds codex.String() refined with cons
// and delegates to WithTopicCodec.
func WithTopicConstraints(cons ...codex.Constraint[string]) BuilderOption

// AddChannel is a free function (generic type params require free functions in Go).
// Registers a channel; returns a ChannelHandle with frozen descriptor and typed helpers.
// Returns InvalidTopicError immediately if topic codec validation fails.
func AddChannel[T any](
    b *Builder,
    topic string,
    codec codex.Codec[T],
    config ChannelConfig,
) (*ChannelHandle[T], error)

// InvalidTopicError is returned by AddChannel when topic codec validation fails.
// Use errors.As to extract it and inspect Topic or the underlying constraint Err.
type InvalidTopicError struct {
    Topic string
    Err   error
}

// AsyncAPISpec builds a full AsyncAPI 2.6 document from all registered channels.
// Returns an error if there are dangling $refs.
func (b *Builder) AsyncAPISpec() (asyncapi.Document, error)
```

Example — enforce MQTT publish topic rules:

```go
import "github.com/DaniDeer/go-codex/validate"

b := events.NewBuilder(info, events.WithTopicConstraints(validate.MQTTPublishTopic))
// Use validate.MQTTTopic (without the publish restriction) for subscribe-only builders.

ch, err := events.AddChannel[MeasurementEvent](b, "sensors/+/data", codec, cfg)
if err != nil {
    // err is an InvalidTopicError — topic failed validation immediately
    var topicErr events.InvalidTopicError
    errors.As(err, &topicErr) // topicErr.Topic, topicErr.Err available
    return err
}
```

`ChannelHandle[T]`:
- `Topic string`
- `Descriptor asyncapi.ChannelItem` — frozen at registration
- `Decode(payload []byte) (T, error)` — JSON decode + Refine validation
- `Encode(msg T) ([]byte, error)` — JSON encode
- `BuildTopic(vars map[string]string) (string, error)` — substitutes `{varName}` placeholders in the topic template, validating each against its `TopicParam.Codec`. After substitution, if a builder-level `topicCodec` is set, the final assembled topic is re-validated against it (no template stripping). Returns `MissingTopicVarError` for missing variables, `TopicParamError` for per-variable codec failures, `InvalidTopicError` if the final topic fails the builder codec. Extra keys in `vars` are silently ignored.
- `ValidateTopic(topic string) error` — validates a received concrete topic string against the builder-level topic codec. Returns `InvalidTopicError` on failure; nil if no topic codec is registered. Call after a wildcard subscription delivers a message.
- `ValidateTopicVars(vars map[string]string) error` — validates extracted topic variable values against registered `TopicParam` codecs. Returns `TopicParamError` for the first variable that fails. Call after `TopicVarsFromMessage` or directly on a vars map. Both methods are called internally by `TopicVarsFromMessage`.

`TopicParamError` is returned by `BuildTopic` and `ValidateTopicVars` when a topic variable fails its codec:

```go
type TopicParamError struct {
    Name  string // the {varName} that failed
    Value string // the value that was rejected
    Err   error  // the underlying codec error
}
```

`MissingTopicVarError` is returned by `BuildTopic` when a template variable has no entry in `vars`:

```go
type MissingTopicVarError struct {
    Name string // the variable name (without braces) that had no value
}
```

`InvalidTopicParamError` is returned by `AddChannel` when a `TopicParams` entry names a variable not in the topic template:

```go
type InvalidTopicParamError struct {
    Name  string // the variable name (without braces) not found in the template
    Topic string // the topic template that was validated against
}
```

`ChannelConfig` fields: `Description`, `Subscribe *OperationConfig`, `Publish *OperationConfig`, `TopicParams []TopicParam`. At least one of `Subscribe`/`Publish` must be non-nil.

`TopicParam{Name, Description, Codec *codex.Codec[string]}` — optional per-variable metadata. `Name` must correspond to a `{varName}` placeholder in the topic template. `Codec` (pointer, `nil` = no validation) provides runtime validation and auto-flows its schema into the AsyncAPI `parameters:` block. An unknown `Name` causes `AddChannel` to return `InvalidTopicParamError` immediately.

**Codec schema → spec**: `TopicParam.Codec` schema automatically flows into the AsyncAPI channel `parameters:` block. For each `{varName}` in the topic template, a parameter entry is always emitted — using the codec schema when a `TopicParam.Codec` is set, or `{type: string}` as default. `TopicParams` is only needed to add a description or runtime validation.

`OperationConfig` fields: `Summary`, `Description`, `Tags`, `SchemaName`.

Key rules:
- `api/events` uses `format.JSON(codec)` internally — explicitly JSON-only.
- The descriptor is built and frozen at `AddChannel` call time.
- Topic validation is **immediate**: if a `topicCodec` is set, `AddChannel` returns `InvalidTopicError` at call time. The channel is not registered on failure.
- **Template-transparent validation**: before running the topic codec, `{varName}` placeholders are replaced with the literal `x` (e.g. `"sensors/{sensorID}/data"` → `"sensors/x/data"`). Constraints run on the structural shape of the topic, not the template syntax. The stored `ChannelHandle.Topic` is always the original template.
- **Final topic re-validation**: `BuildTopic` re-validates the fully assembled topic against the builder-level `topicCodec` after substitution. Catches values that pass their `TopicParam.Codec` but violate the global topic constraint. Returns `InvalidTopicError{Topic: finalTopic, Err: ...}`.
- `Info = asyncapi.Info` and `Server = asyncapi.Server` are type aliases.
- `api/events` may import `codex`, `format`, `render/asyncapi`, `schema`. No messaging library.
- `adapters/mqtt` wraps `ChannelHandle` for Paho MQTT. It imports `api/events`, `stats`, and `github.com/eclipse/paho.mqtt.golang`.
  - `SubscribeHandler[T](ctx, handle, fn, opts SubscribeOptions) mqtt.MessageHandler` — decodes payload, calls fn, routes typed errors to `opts.OnError`; instruments via `opts.Observer` (`RecordSubscribe` + `RecordValidationError` for payload and topic errors). `SubscribeError.Topic` reflects the concrete incoming message topic (`msg.Topic()`).
  - `SubscribeOptions{OnError func(SubscribeError), Observer stats.Observer}` — zero value is safe (nil `OnError` discards errors, nil `Observer` defaults to `NoopObserver`).
  - `MessageFromContext(ctx) (pahomqtt.Message, bool)` — retrieves the raw `pahomqtt.Message` stored in context by `SubscribeHandler`. Analogous to `nethttp.RequestFromContext`. Gives access to `Qos()`, `Retained()`, `MessageID()`, `Duplicate()` without breaking the typed handler signature. Returns false on a plain context.
  - `SubscribeError{Kind ErrorKind, Topic string, Err error}` — typed error; `Kind` is `KindDecode` or `KindHandler`.
  - `Publish[T](ctx, client, handle, qos, retained, msg, vars map[string]string, opts PublishOptions) error` — unified publish: `nil` vars → use `handle.Topic` (static topics); non-nil vars → call `handle.BuildTopic(vars)`. Instruments via `opts.Observer`: calls `RecordPublish(topic, success, duration)` on all exit paths; calls `RecordValidationError("payload", ...)` on encode errors; calls `RecordValidationError("topic_var", ...)` or `RecordValidationError("topic", ...)` on `BuildTopic` failures. Returns `TopicParamError` or `MissingTopicVarError` if `BuildTopic` fails. Context-aware token wait.
  - `PublishOptions{Observer stats.Observer}` — zero value is safe (nil `Observer` defaults to `NoopObserver`).
  - `TopicVarsFromMessage[T](handle, msg) (map[string]string, error)` — inverse of `BuildTopic`. Matches the concrete MQTT topic (`msg.Topic()`) against the channel's topic template, extracting `{varName}` values into the returned map. Template rules: `{varName}` captures one level; `+` matches one level (anonymous, not captured); `#` as last segment captures all remaining levels under key `"#"`. Applies full validation chain (symmetric with `BuildTopic`): (1) structural match → `TopicMismatchError{Template, Topic}`; (2) builder-level topic codec → `InvalidTopicError{Topic, Err}`; (3) per-param `TopicParam.Codec` validation → `TopicParamError{Name, Value, Err}`.
  - `TopicMismatchError{Template, Topic string}` — returned by `TopicVarsFromMessage` when the received topic does not match the template structure.

- `stats` — dependency-free observability package.
  - `ValidationObserver` — codec-level interface: `RecordValidationError(location, constraintName, field string)`. Implement this when using codecs directly (no adapter). The `location` is a user-chosen label (e.g. `"config"`, `"input"`).
  - `Observer` — adapter-level interface: embeds `ValidationObserver` + `RecordRequest`, `RecordSubscribe`, `RecordPublish`. Use this with `adapters/nethttp` and `adapters/mqtt`.
  - `NoopObserver{}` — satisfies both interfaces, zero-cost default.
  - `ReportErrors(obs ValidationObserver, location string, err error)` — iterates `codex.ValidationErrors` from a decode error, calls `obs.RecordValidationError` per field. Codec-only users call this after `codec.Decode`.
  - `ConstraintName(err error) string` — extracts stable constraint label: `ConstraintError.Name`, `"type-mismatch"`, `"required"`, or `""`.
  - `location` values by adapter: `"body"` (nethttp/chi body decode/encode), `"query"` (nethttp/chi query), `"cookie"` (nethttp/chi request cookie), `"header"` (nethttp/chi request header), `"response_header"` (nethttp/chi response header), `"response_cookie"` (nethttp/chi response cookie), `"payload"` (mqtt payload), `"topic_var"` (mqtt per-variable codec failure), `"topic"` (mqtt topic-level codec or structural mismatch), user-defined string (codec-only).

### Package import table (updated)

| Package            | Imports allowed from                                          |
|--------------------|---------------------------------------------------------------|
| `api/rest`         | `codex`, `format`, `route`, `render/openapi`, `schema`        |
| `api/events`       | `codex`, `format`, `render/asyncapi`, `schema`                |
| `adapters/nethttp` | `api/rest`, `net/http` (stdlib), `stats`, `format`            |
| `adapters/chi`     | `api/rest`, `net/http` (stdlib), `stats`, `format`, chi lib   |
| `adapters/mqtt`    | `api/events`, `stats`, `github.com/eclipse/paho.mqtt.golang`  |
| `adapters/templ`   | `codex`, `format`, `github.com/a-h/templ`                     |
| `stats`            | `codex`, `errors`, `time` (stdlib only)                       |


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

`Format[T]` has four methods: `Marshal(T) ([]byte, error)`, `Unmarshal([]byte) (T, error)`, `Validate(T) error`, `Schema() schema.Schema`.

`format.New[T]` accepts custom marshal/unmarshal functions for formats not built-in.

**Important**: primitive codecs handle the numeric types each format produces:
- JSON produces `float64` for all numbers
- YAML produces `int` for integers, `float64` for floats
- TOML produces `int64` for integers, `float64` for floats

`Int()` handles `int`, `int64`, and integral `float64`. Add new numeric types to this list when extending.

## Environment Variable Loading (`format.FromEnv`)

`format.FromEnv[T]` loads a struct from environment variables using the codec's schema for schema-driven string coercion. It is a standalone function, not a `Format[T]` (env vars are read-only; no Marshal direction).

```go
// Naming: strings.ToUpper(prefix + field_name)
// "port"         + "APP_" → APP_PORT
// "log_level"    + "APP_" → APP_LOG_LEVEL
// nested "db.host"        → APP_DB_HOST
cfg, err := format.FromEnv(configCodec, "APP_")
// err is codex.ValidationErrors — parse errors + missing required + constraints.
```

**Supported types** (determined from codec schema):

| Schema type | Coercion |
|---|---|
| `"integer"` | `strconv.Atoi` → `int` |
| `"number"` | `strconv.ParseFloat(64)` → `float64` |
| `"boolean"` | `strconv.ParseBool` → `bool` |
| `"string"` (any other) | pass as-is |
| nested struct (`Type="object"`, `Properties!=nil`, `AdditionalPropertiesSchema==nil`) | prefix expansion (`APP_DB_HOST`) OR JSON object (`APP_DB='{"host":"..."}'`) |
| slice (`Type="array"`, `Items!=nil`) | comma-separated (`APP_TAGS=a,b,c`) OR JSON array (`APP_TAGS='["a","b","c"]'`) |
| StringMap (`AdditionalPropertiesSchema!=nil`) | JSON object only (`APP_LABELS='{"k":"v"}'`) |
| `Nullable[T]` | absent = nil; present = coerce inner type |

**JSON detection**: when the env var value starts with `{` (for object fields) or `[` (for array fields), it is parsed as JSON. JSON takes precedence over prefix expansion and comma-split. Malformed JSON returns a `ValidationError` for that field.

**Silently skipped**: `TaggedUnion`, slices of objects.

**Error shape**: `codex.ValidationErrors` — parse errors are collected before Decode runs; decode errors (missing required, constraint violations) follow in the same type.

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
