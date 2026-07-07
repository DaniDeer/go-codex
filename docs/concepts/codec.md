# Codec — declare once

> See also: [`codex` package on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/codex)

A `Codec[T]` is the single source of truth for a type. It bundles three concerns in one value:

```go
type Codec[T any] struct {
    Schema  schema.Schema          // shape + constraints as data
    Encode  func(T) (any, error)   // T → intermediate (e.g. map[string]any for JSON)
    Decode  func(any) (T, error)   // intermediate → T, validates constraints
}
```

## Why one value?

Traditional approaches scatter the same information across multiple files:
- A struct with JSON tags for encoding
- A validation library for constraints
- A Swagger annotation comment for the schema

go-codex collapses all three into a single `Codec[T]` that you define once and pass around. Every format (JSON, YAML, OpenAPI, AsyncAPI) is derived from the same definition automatically.

## Primitive codecs

| Constructor | Go type | JSON wire | Schema |
|---|---|---|---|
| `codex.Int()` | `int` | number | `{type:integer}` |
| `codex.Int32()` | `int32` | number | `{type:integer,format:int32}` |
| `codex.Int64()` | `int64` | number | `{type:integer,format:int64}` |
| `codex.Uint()` | `uint` | number | `{type:integer,minimum:0}` |
| `codex.Uint64()` | `uint64` | number | `{type:integer,minimum:0}` |
| `codex.Float32()` | `float32` | number | `{type:number,format:float}` |
| `codex.Float64()` | `float64` | number | `{type:number}` |
| `codex.String()` | `string` | string | `{type:string}` |
| `codex.Bool()` | `bool` | boolean | `{type:boolean}` |
| `codex.Bytes()` | `[]byte` | base64 string | `{type:string,format:byte}` |
| `codex.Time()` | `time.Time` | RFC 3339 string | `{type:string,format:date-time}` |
| `codex.Date()` | `time.Time` | `YYYY-MM-DD` | `{type:string,format:date}` |
| `codex.Duration()` | `time.Duration` | duration string | `{type:string,format:duration}` |
| `codex.Nullable(inner)` | `*T` | value or `null` | inner schema + `nullable:true` |
| `codex.SliceOf(elem)` | `[]T` | array | `{type:array,items:{...}}` |
| `codex.StringMap(value)` | `map[string]V` | object | `{type:object,additionalProperties:{...}}` |
| `codex.Map(keyCodec, valueCodec)` | `map[K]V` | object | `{type:object,propertyNames:{...},additionalProperties:{...}}` |
| `codex.Struct[T](fields...)` | any struct | object | `{type:object,properties:{...}}` |
| `codex.TaggedUnion[T](tag, variants...)` | any interface | object | `{oneOf:[...],discriminator:{...}}` |
| `codex.UntaggedUnion[T](which, variants...)` | any interface | object | `{oneOf:[...]}` |
| `codex.Either2(ca, cb)` | `Either[A,B]` | value | `{oneOf:[schemaA,schemaB]}` |
| `codex.Any()` | `any` | any | `{}` |
| `codex.Pure(value)` | `T` | fixed wire value | `{enum:[value]}` |
| `codex.Eq(base, value)` | `T comparable` | validated by base | base schema + `{enum:[value]}` |

### Composition at a glance

The following constructors accept another codec as an argument, letting you compose types of arbitrary depth and shape:

| Composer | Use for | Example |
|---|---|---|
| `codex.Struct[T](fields...)` | object with named, typed fields | `Struct[Order](RequiredField("customer", customerCodec, ...))` |
| `codex.SliceOf(elem)` | homogeneous array | `SliceOf(lineItemCodec)` → `Codec[[]LineItem]` |
| `codex.StringMap(value)` | `map[string]V` — string keys, typed values | `StringMap(codex.Int())` → `Codec[map[string]int]` |
| `codex.Map(keyCodec, valCodec)` | `map[K]V` — both key and value validated | `Map(sensorIDCodec, codex.Float64())` |
| `codex.Nullable(inner)` | optional pointer `*T` (present or nil) | `Nullable(codex.String())` → `Codec[*string]` |
| `codex.TaggedUnion[T](tag, variants...)` | discriminated union — tag field selects variant | `TaggedUnion[Shape]("type", circleVariant, rectVariant)` |
| `codex.UntaggedUnion[T](which, variants...)` | structural union — first-match decode | `UntaggedUnion[Shape](selector, variants...)` |
| `codex.Either2(ca, cb)` | two-branch sum — `Either[A, B]` | `Either2(codex.String(), dbConfigCodec)` |
| `codex.Nullable(SliceOf(inner))` | optional array | compose freely to any depth |

Any of these can be used as the codec argument of `RequiredField` / `OptionalField`, so nesting is unlimited: `Struct` → `SliceOf(Struct)` → `Nullable(Struct)` → …

```go
// Nullable pointer field
var noteCodec = codex.Nullable(codex.String())  // Codec[*string]
note, _ := noteCodec.Decode(nil)                // → (*string)(nil)
s := "hello"
enc, _ := noteCodec.Encode(&s)                  // → "hello"

// Time
var createdAtCodec = codex.Time()
enc, _ = createdAtCodec.Encode(time.Now())     // → "2024-06-15T12:00:00Z"

// StringMap
var tagsCodec = codex.StringMap(codex.String())
enc, _ = tagsCodec.Encode(map[string]string{"env": "prod"})

// Map[K, V] — keys validated via a key codec.
// Key codec must encode K to a string (JSON/YAML require string map keys).
// The schema emits "propertyNames" for the key constraint.
var sensorIDCodec = codex.String().
    Refine(validate.Pattern(regexp.MustCompile(`^[a-z]+-\d+$`))).
    WithTitle("SensorID")
var sensorsCodec = codex.Map[string, float64](sensorIDCodec, codex.Float64())
// Schema: {type:object, propertyNames:{type:string,title:"SensorID",pattern:"..."}, additionalProperties:{type:number}}
_, _ = sensorsCodec.Encode(map[string]float64{"temp-01": 22.5}) // ok
_, err := sensorsCodec.Encode(map[string]float64{"INVALID": 22.5})
// → KeyError{Key:"INVALID", Err: constraint failed (pattern)}
_ = err

// Any — opaque passthrough, no type enforcement
var rawCodec = codex.Any()
val, _ := rawCodec.Decode(map[string]any{"x": 1}) // passes through unchanged
_ = val
```

## Struct codecs

```go
var UserCodec = codex.Struct[User](
    codex.RequiredField("name", nameCodec, get, set),
    codex.OptionalField("bio",  codex.String(), get, set),
)
```

### Missing fields on decode

When a field key is absent from the incoming object, the codec follows this decision tree:

```
field key absent from wire object?
├─ DefaultField  → apply declared default, continue
├─ RequiredField → return ErrMissingField (decode fails)
└─ OptionalField → do nothing; Go zero value remains ("", 0, nil, …)
```

**`RequiredField`** — missing key → `ErrMissingField` sentinel, surfaced as `ValidationError{Field: "name", Err: ErrMissingField}` inside `ValidationErrors`. Check with `errors.Is(err, codex.ErrMissingField)`.

**`OptionalField`** — missing key → the Go field retains its zero value. The `set` function is never called. There is no distinction between "key absent" and "key present with zero value" at the Go struct level — if you need that, use `Nullable`:

```go
// "note" absent  → Note == nil  (key was not in the object)
// "note": null   → Note == nil  (key was present, value was null)
// "note": "hi"   → Note == &"hi"
codex.OptionalField("note", codex.Nullable(codex.String()),
    func(u User) *string { return u.Note },
    func(u *User, v *string) { u.Note = v },
)
```

**`DefaultField`** — missing key → declared default applied via `set`. The default also appears in the generated OpenAPI/AsyncAPI schema as `"default": <value>`. A zero-value default is valid — `DefaultField` uses a pointer internally to distinguish "no default" from "default is zero":

```go
codex.DefaultField("log_level",
    codex.String().Refine(validate.OneOf("debug", "info", "warn", "error")),
    "info",   // applied when key absent; emitted as "default" in schema
    func(c Config) string { return c.LogLevel },
    func(c *Config, v string) { c.LogLevel = v },
)
```

### Missing fields — summary

| Constructor | Field absent (decode) | Field absent (encode) | Schema |
|---|---|---|---|
| `RequiredField` | `ErrMissingField` → decode fails | always encoded | `required: [field]` |
| `OptionalField` | Go zero value (`""`, `0`, `nil`, …) | always encoded | field not in `required` |
| `DefaultField` | declared default applied | always encoded | field not in `required`; `default: <value>` |
| `OptionalField` + `Nullable` | `nil` pointer (type-safe absent) | encoded as `null` | field not in `required`; `nullable: true` |

> **Encode note:** there is no "omit if zero" logic on encode — every field is always written to the output object. If you want a field to be absent rather than `null` on encode, handle that outside the codec.

## Constraints with Refine

```go
var EmailCodec = codex.String().Refine(validate.Email)
// Schema gains: format: email
// Decode rejects invalid emails at runtime
// Encode validates before serialising
```

Constraints run symmetrically — on both Encode and Decode — ensuring the codec is the single source of truth for validity.

## Composition — shared field codecs

Define field codecs once and reuse across struct codecs:

```go
var emailFieldCodec = codex.String().Refine(validate.Email).WithDescription("Email address.")

var UserCodec    = codex.Struct[User](   codex.RequiredField("email", emailFieldCodec, ...), ...)
var ProfileCodec = codex.Struct[Profile](codex.RequiredField("email", emailFieldCodec, ...), ...)
// Both carry the same constraint and description — no duplication.
```

## Nested structs

A field codec can be any `Codec[F]` — including another `Struct[...]` codec. Nesting is unlimited and composes with `SliceOf`, `Nullable`, and `StringMap`:

```go
type Address struct { Street, City, Country string }
type Customer struct { Name, Email string }
type LineItem struct { Product string; Quantity int; Price float64 }
type Order struct {
    ID       string
    Customer Customer
    Shipping Address
    Items    []LineItem
    Tags     map[string]string
    Note     *string
}

var addressCodec = codex.Struct[Address](
    codex.RequiredField("street",  codex.String().Refine(validate.NonEmptyString), ...),
    codex.RequiredField("city",    codex.String().Refine(validate.NonEmptyString), ...),
    codex.RequiredField("country", codex.String().Refine(validate.NonEmptyString), ...),
)

var lineItemCodec = codex.Struct[LineItem](
    codex.RequiredField("product",  codex.String(), ...),
    codex.RequiredField("quantity", codex.Int().Refine(validate.PositiveInt), ...),
    codex.RequiredField("price",    codex.Float64().Refine(validate.PositiveFloat), ...),
)

var orderCodec = codex.Struct[Order](
    codex.RequiredField("id",       codex.String(), ...),
    codex.RequiredField("customer", customerCodec,              ...),  // nested Struct
    codex.RequiredField("shipping", addressCodec,               ...),  // nested Struct
    codex.RequiredField("items",    codex.SliceOf(lineItemCodec), ...), // slice of structs
    codex.OptionalField("tags",     codex.StringMap(codex.String()), ...), // map
    codex.OptionalField("note",     codex.Nullable(codex.String()),  ...), // optional
)
```

**What you get for free:**
- Encode/decode recurses automatically — `order.Customer` is a Go struct, the JSON `"customer"` is an object.
- Validation cascades — a constraint failure on `customer.email` surfaces as `ValidationErrors` with path `"customer.email"`.
- Schema generation — `OrderCodec.Schema` produces a nested `$object` with inline `Customer` and `Address` schemas.

See [`examples/order`](https://github.com/DaniDeer/go-codex/tree/main/examples/order) for a complete runnable demo with all four nesting patterns.

## Cross-field constraints: RefineFunc

`RefineFunc` wraps a `func(T) error` applied on both Encode and Decode. Use it to validate relationships between fields:

```go
var dateRangeCodec = codex.Struct[DateRange](
    codex.RequiredField("start", codex.Time(), ...),
    codex.RequiredField("end",   codex.Time(), ...),
).RefineFunc(func(r DateRange) error {
    if !r.End.After(r.Start) {
        return errors.New("end must be after start")
    }
    return nil
})
```

## Unknown fields on decode

The decode loop iterates only over the fields you declared. Keys present in the incoming object but not in the codec are **silently dropped** — they never touch the result struct and do not cause an error.

```go
// Wire JSON: {"name":"Alice","email":"alice@example.com","role":"admin","_internal":123}
// Codec declares: "name", "email" only
// Result: User{Name: "Alice", Email: "alice@example.com"}  ← "role", "_internal" dropped
```

The encode side is symmetric: `Encode` writes only declared fields. Any Go struct fields without a codec entry are absent from the output.

This is intentional — it makes the codec resilient to API evolution. A producer can add new fields without breaking consumers that have not declared them yet.

If you need to reject unknown fields, add a `RefineFunc` that compares incoming keys against the declared set. The generated schema does **not** emit `"additionalProperties": false` by default.

## Encode, Decode, and Validation

| Direction | What runs | Rationale |
|---|---|---|
| **Decode** | type checks + all `Refine` constraints | Input is untrusted — every constraint runs |
| **Encode** | type conversion + all `Refine` constraints | Constraints also run on outgoing path — invalid values cannot be serialised |

```go
// Decode — validates automatically
user, err := jsonFmt.Unmarshal([]byte(`{"name":"","age":-5}`))
// err: field name: constraint failed (non-empty): expected non-empty string

// Encode — also validates
data, err := jsonFmt.Marshal(User{Name: "", Age: -5})
// err: field name: constraint failed (non-empty): ...

// Validate — explicit round-trip check
if err := UserCodec.Validate(u); err != nil {
    return fmt.Errorf("constructed invalid user: %w", err)
}
```

## Smart constructors: New and Must

```go
// New: validate + return in one call.
email, err := emailCodec.New(Email("user@example.com"))
if err != nil { return err }
// email is guaranteed valid

// Must: panic-on-error for package-level constants and test data.
var guestUser = codex.Must(usernameCodec.New(Username("guest")))
```

## Either — typed sum type

`Either2` tries codec A first; if decode fails, tries codec B. Encode uses whichever branch is non-nil:

```go
var dsnOrConfig = codex.Either2(codex.String(), dbConfigCodec)
// Codec[codex.Either[string, DBConfig]]

left, _ := dsnOrConfig.Decode("postgres://localhost/db")
// left.Left = &"postgres://...", left.Right = nil
```

If both branches fail, returns `EitherError{Errors: []error{errA, errB}}`.

## UntaggedUnion — interface union without discriminator

```go
var shapeCodec = codex.UntaggedUnion[Shape](
    func(s Shape) int {
        switch s.(type) {
        case Circle: return 0
        case Rect:   return 1
        }
        return -1
    },
    codex.UntaggedVariant[Shape]{Name: "circle", Codec: circleCodec},
    codex.UntaggedVariant[Shape]{Name: "rect",   Codec: rectCodec},
)
```

Decode: first-match wins. Schema: `{oneOf: [{...circle...}, {...rect...}]}`.

## Pure and Eq — fixed and single-value codecs

`Pure` always decodes to a fixed value (ignoring wire input). `Eq` rejects anything that doesn't equal a specific value:

```go
var CloudEventCodec = codex.Struct[CloudEvent](
    // Pure: always decodes to "1.0" regardless of wire value
    codex.RequiredField("specversion", codex.Pure("1.0"), ...),
    // Eq: only accepts exactly "com.example.order.placed"
    codex.RequiredField("type", codex.Eq(codex.String(), "com.example.order.placed"), ...),
)
```

## MapCodecSafe and MapCodecValidated

Both build `Codec[B]` from `Codec[A]` via mapping functions:

```go
// MapCodecSafe — total decode direction, fallible encode direction
type Email string
var EmailCodec = codex.MapCodecSafe(
    codex.String(),
    func(s string) Email { return Email(s) },
    func(e Email) (string, error) { return string(e), nil },
)

// MapCodecValidated — both directions may fail; post-decode validation via cb
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

**Rule:** use `MapCodecSafe` for newtypes and type-safe wrappers; use `MapCodecValidated` when both directions may fail and the target type carries its own constraints.

## Schema metadata

```go
var emailCodec = codex.String().
    Refine(validate.Email).
    WithDescription("Primary contact email.").
    WithExample("alice@example.com").   // → example: alice@example.com in OpenAPI
    WithTitle("Email")                   // → title in schema

var legacyIPCodec = codex.String().
    Refine(validate.IPv4).
    WithDeprecated()                     // → deprecated: true in OpenAPI
```

## Custom format extensibility

The `format` package provides two constructors for custom wire formats:

| Constructor | Intermediate | Use cases |
|---|---|---|
| `format.New[T](codec, marshal, unmarshal)` | `map[string]any` | CBOR, MessagePack, XML |
| `format.NewTyped[T](codec, marshal, unmarshal, ct)` | typed `T` directly | templ HTML, Protobuf, CSV |
| `format.NewStreamed[T](codec, marshalTo, unmarshal, ct)` | writes to `io.Writer` | SSR streaming, chunked responses |

```go
// Custom MessagePack format
msgpackFmt := format.New(userCodec,
    func(v any) ([]byte, error) { return msgpack.Marshal(v) },
    func(b []byte) (any, error) { var m any; return m, msgpack.Unmarshal(b, &m) },
).WithContentType("application/msgpack")
```

## Builtin constraints (`validate/`)

Format constraints (annotate schema automatically):

| Constraint | Validates | OpenAPI format |
|---|---|---|
| `validate.Email` | `user@domain.tld` | `email` |
| `validate.UUID` | RFC 4122 UUID | `uuid` |
| `validate.URL` | absolute http/https URL | `uri` |
| `validate.URLWithSchemes(s...)` | URL restricted to given schemes | `uri` |
| `validate.URI` | absolute URI with any scheme | `uri` |
| `validate.Hostname` | RFC 1123 hostname | `hostname` |
| `validate.IPv4` | dotted-decimal IPv4 | `ipv4` |
| `validate.IPv6` | IPv6 address | `ipv6` |
| `validate.IP` | IPv4 or IPv6 | `ip` |
| `validate.Date` | `YYYY-MM-DD` | `date` |
| `validate.Time` | RFC 3339 time-only | `time` |
| `validate.DateTime` | RFC 3339 date-time | `date-time` |
| `validate.SemVer` | semantic version | `pattern` |
| `validate.Slug` | `lowercase-hyphen-slug` | `pattern` |
| `validate.CIDR` | CIDR notation | _(none)_ |
| `validate.ContainerImage` | OCI container image reference (e.g. `alpine:latest`, `docker.io/library/nginx:1.25`) | _(none)_ |
| `validate.BearerToken` | non-empty, no leading/trailing whitespace | — |
| `validate.JWT` | compact JWT (header.payload.signature) | — |
| `validate.EnvVarName` | POSIX env var name (`^[A-Z_][A-Z0-9_]*$`) | `pattern` |
| `validate.EnvVarPrefix(prefix)` | starts with given prefix | — |
| `validate.IntString` / `PositiveIntString` / `NonNegativeIntString` / `IntStringInRange` | integer as string | — |

Range/length constraints:

| Constraint | Applies to | Validates |
|---|---|---|
| `validate.NonEmptyString` | `string` | not empty |
| `validate.MinLen(n)` / `MaxLen(n)` | `string` | character count |
| `validate.OneOf(values...)` | `string` | enum membership |
| `validate.Pattern(re)` | `string` | regexp match |
| `validate.PositiveInt` / `NegativeInt` / `NonZeroInt` | `int` | sign |
| `validate.MinInt(n)` / `MaxInt(n)` / `RangeInt(a,b)` | `int` | bounds |
| `validate.PositiveFloat` / `NonZeroFloat` | `float64` | sign |
| `validate.MinFloat(n)` / `MaxFloat(n)` / `RangeFloat(a,b)` | `float64` | bounds |
| `validate.PositiveDuration` / `MinDuration(d)` | `time.Duration` | duration bounds |
| `validate.MaxBytes(n)` / `MinBytes(n)` | `[]byte` | byte count |

### Custom constraints

```go
// Inline (one-off):
var AvatarCodec = codex.Bytes().Refine(codex.Constraint[[]byte]{
    Name:    "maxBytes(65536)",
    Check:   func(v []byte) bool { return len(v) <= 65536 },
    Message: func(v []byte) string {
        return fmt.Sprintf("expected at most 65536 bytes, got %d", len(v))
    },
})

// Reusable with schema annotation (propagates to OpenAPI):
func MaxLen(n int) codex.Constraint[string] {
    return codex.Constraint[string]{
        Name:  fmt.Sprintf("maxLen(%d)", n),
        Check: func(v string) bool { return len(v) <= n },
        Message: func(v string) string {
            return fmt.Sprintf("expected at most %d characters, got %d", n, len(v))
        },
        Schema: func(s schema.Schema) schema.Schema {
            s.MaxLength = &n    // reflected into OpenAPI output automatically
            return s
        },
    }
}
```

## See also

- [validate package](https://pkg.go.dev/github.com/DaniDeer/go-codex/validate) — built-in constraints
- [format package](https://pkg.go.dev/github.com/DaniDeer/go-codex/format) — JSON, YAML, TOML, Gob
- [Feature: Error Handling](../features/error-handling.md)
- [Examples: construction](https://github.com/DaniDeer/go-codex/tree/main/examples/construction)
- [Examples: codec-mapping](https://github.com/DaniDeer/go-codex/tree/main/examples/codec-mapping)
- [Examples: formats](https://github.com/DaniDeer/go-codex/tree/main/examples/formats)
