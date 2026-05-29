# GO Codex

[![CI](https://github.com/DaniDeer/go-codex/actions/workflows/ci.yml/badge.svg)](https://github.com/DaniDeer/go-codex/actions/workflows/ci.yml)

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

### Shared Contract

go-codex codecs are plain Go values — they can live in a shared package and be imported by any number of services.

```
pkg/contract/
    user.go       ← UserCodec, CreateUserRequestCodec, ...
```

```go
// server — decodes and validates incoming payloads
import "yourorg/pkg/contract"

user, err := contract.UserCodec.Decode(raw) // invalid input is rejected

// client — encodes outgoing payloads; generates OpenAPI spec from the same codec
spec, _ := openapi.MarshalYAML(map[string]schema.Schema{
    "User": contract.UserCodec.Schema,
})
```

A field rename in `contract.User` breaks compilation on both sides immediately — no stale YAML, no schema drift, no separate code-generation step. This is the key difference from protobuf or OpenAPI-first workflows: **the Go source is the contract**.

## Installation & Usage

```bash
go get github.com/DaniDeer/go-codex@latest
```

Requires Go 1.25 or later.

### Import paths

| Package                           | Import path                                     |
| --------------------------------- | ----------------------------------------------- |
| Core codecs                       | `github.com/DaniDeer/go-codex/codex`            |
| Format bridges (JSON, YAML, TOML) | `github.com/DaniDeer/go-codex/format`           |
| Built-in constraints              | `github.com/DaniDeer/go-codex/validate`         |
| HTTP route descriptors            | `github.com/DaniDeer/go-codex/route`            |
| REST API builder                  | `github.com/DaniDeer/go-codex/api/rest`         |
| Event channel builder             | `github.com/DaniDeer/go-codex/api/events`       |
| net/http adapter                  | `github.com/DaniDeer/go-codex/adapters/nethttp` |
| Paho MQTT adapter                 | `github.com/DaniDeer/go-codex/adapters/mqtt`    |
| OpenAPI 3.1 renderer              | `github.com/DaniDeer/go-codex/render/openapi`   |
| AsyncAPI 2.6 renderer             | `github.com/DaniDeer/go-codex/render/asyncapi`  |
| Schema model                      | `github.com/DaniDeer/go-codex/schema`           |

## Features

- **Multi-Format Support** — one `Codec[T]` reads and writes JSON, YAML, and TOML unchanged
- **Encode, Decode, and Validation** — constraints run on decode; encode is trusted; validate is explicit
- **Builtin Format Constraints** — `email`, `uuid`, `url`, `date`, `date-time` validated and reflected into schema automatically
- **Protocol Path/Topic Constraints** — `validate.HTTPPath`, `validate.MQTTPublishTopic`, `validate.MQTTTopic` validate path and topic strings; compose with custom constraints via `WithPathConstraints` / `WithTopicConstraints`
- **Rich Codec Types** — primitives, `Time`/`Date`, `Nullable[T]`, `Bytes`, `SliceOf[T]`, `StringMap[V]`, structs, tagged unions
- **Structured Decode Errors** — all failure types are concrete structs (`ValidationErrors`, `ConstraintError`, `TypeMismatchError`, `ElementError`, `KeyError`, `UnknownVariantError`, `VariantError`); use `errors.As` to inspect them, or pass them directly to `log/slog`
- **OpenAPI Schema Generation** — `components/schemas` map from codec-derived schemas, no manual YAML
- **Full OpenAPI 3.1 Document** — complete REST API spec (paths, operations, params) from `route.Route` descriptors
- **AsyncAPI 2.6 Document** — complete event-driven spec from channel descriptors; same schemas, no duplication
- **REST API Builder** — typed `Decode`/`Encode` helpers per route + OpenAPI spec generation; immediate path validation; `BuildPath` for runtime path construction with per-variable codec checks and final path re-validation
- **Event Channel Builder** — typed `Decode`/`Encode` helpers per channel + AsyncAPI spec generation; immediate topic validation; `BuildTopic` for runtime topic construction with per-variable codec checks and final topic re-validation
- **net/http Adapter** — wire `RouteHandle` to `net/http.ServeMux` with one call; 400/500 error handling included
- **Paho MQTT Adapter** — wire `ChannelHandle` to Paho MQTT subscribe callbacks; unified `Publish` handles both static and template topics

### Multi-Format Support

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

### Encode, Decode, and Validation

#### The trust boundary

go-codex draws a deliberate line between trusted and untrusted data:

| Direction  | What runs                              | Rationale                                                                                                                                   |
| ---------- | -------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------- |
| **Decode** | type checks + all `Refine` constraints | Input comes from outside — JSON on the wire, YAML from a file, a CLI flag. You cannot trust it. Every constraint runs.                      |
| **Encode** | type conversion only                   | The Go value was constructed by your own code. You already trust it. Running constraints on every encode would be redundant and surprising. |

This mirrors the design of [autodocodec](https://hackage.haskell.org/package/autodocodec): constraints are a guard on ingress, not a restriction on your own domain logic.

#### Decode — validates automatically

```go
// Constraints run during Decode. Invalid input is rejected with field-path errors.
user, err := jsonFmt.Unmarshal([]byte(`{"name":"","age":-5}`))
// err: field name: constraint failed (non-empty): expected non-empty string
```

#### Encode — trusted, no constraints

```go
// Encoding the value you constructed always succeeds (no constraints run).
// You are responsible for the correctness of values you build.
data, err := jsonFmt.Marshal(User{Name: "", Age: -5}) // succeeds
```

#### Validate — explicit bidirectional check

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

#### New — smart constructor

`Codec.New` validates and returns the value in a single call. Use it as a smart constructor when you want to create a validated domain value from a Go value:

```go
// Validate + return in one call.
email, err := emailCodec.New(Email("user@example.com"))
if err != nil {
    return err
}
// email is guaranteed valid here
```

`New` is equivalent to calling `Validate` and then returning the original value. It is a thin wrapper — no new constraint logic.

#### Must — panic on invalid (for constants and test data)

`codex.Must` is a generic panic-on-error helper, following the convention of `template.Must` and `regexp.MustCompile`. Use it for package-level validated constants or test data setup — places where an invalid value is a programming error, not a recoverable runtime condition:

```go
// Package-level constant — panics at startup if "guest" is somehow invalid.
var guestUser = codex.Must(usernameCodec.New(Username("guest")))

// Test helper — panics immediately rather than hiding setup errors.
got := codex.Must(emailCodec.Decode("user@example.com"))
```

`Must` is generic and works with any `(T, error)` pair — `New`, `Decode`, `MapCodecValidated`, or your own functions.

### Error Handling

All decode failures are structured types. Use `errors.As` to inspect them precisely, or pass them directly to `log/slog` — every type implements `slog.LogValuer`.

#### Error types

| Type                  | Returned by                                                | Key fields                                              |
| --------------------- | ---------------------------------------------------------- | ------------------------------------------------------- |
| `ValidationErrors`    | `Struct` decode                                            | `[]ValidationError`; also implements `Unwrap() []error` |
| `ValidationError`     | each field in `Struct` decode                              | `Field string`, `Err error`                             |
| `ConstraintError`     | `Refine` on any codec; also `Int`/`Int64` for non-integral float | `Name string`, `Message string`                         |
| `TypeMismatchError`   | any codec receiving wrong Go type                          | `Expected string`, `Got string`                         |
| `ElementError`        | `SliceOf` decode                                           | `Index int`, `Err error`                                |
| `KeyError`            | `StringMap` decode                                         | `Key string`, `Err error`                               |
| `UnknownVariantError` | `TaggedUnion` when tag value has no matching codec         | `Tag string`, `Variant string`                          |
| `VariantError`        | `TaggedUnion` when a known variant fails to decode/encode  | `Tag string`, `Variant string`, `Err error`             |
| `ErrMissingField`     | required `Field` when key absent                           | sentinel; use `errors.Is`                               |

#### Inspecting errors with `errors.As`

```go
var ve codex.ValidationErrors
if errors.As(err, &ve) {
    for _, fieldErr := range ve {
        var ce codex.ConstraintError
        if errors.As(fieldErr.Err, &ce) {
            // ce.Name   — constraint identifier, e.g. "email", "minLen(3)"
            // ce.Message — human-readable description of the failure
            fmt.Printf("field %q: constraint %q failed: %s\n",
                fieldErr.Field, ce.Name, ce.Message)
        }
        if errors.Is(fieldErr.Err, codex.ErrMissingField) {
            fmt.Printf("field %q is required but absent\n", fieldErr.Field)
        }
    }
}
```

#### Structured logging with `log/slog`

All error types implement `slog.LogValuer`. Pass them as slog attributes to get structured key-value output instead of a flat error string:

```go
logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

var ve codex.ValidationErrors
if errors.As(err, &ve) {
    // Emits each field name and its error as separate slog attributes.
    logger.Error("request validation failed", slog.Any("validation_errors", ve))

    for _, fieldErr := range ve {
        var ce codex.ConstraintError
        if errors.As(fieldErr.Err, &ce) {
            // Emits field.field, field.error, constraint.constraint, constraint.message.
            logger.Warn("field constraint failed",
                slog.Any("field", fieldErr),
                slog.Any("constraint", ce),
            )
        }
    }
}
```

See `examples/error-types/` for a runnable demo of every error type with `errors.As` and slog.
See `examples/decode-errors/` for struct validation errors and HTTP 400 response patterns.

### Builtin Format Constraints

`validate/` ships format constraints for common string types. Each constraint validates the value **and** annotates `schema.Schema` so the format appears in OpenAPI output automatically.

| Constraint                        | Validates                                  | OpenAPI format |
| --------------------------------- | ------------------------------------------ | -------------- |
| `validate.Email`                  | `user@domain.tld`                          | `email`        |
| `validate.UUID`                   | RFC 4122 UUID (case-insensitive)           | `uuid`         |
| `validate.URL`                    | absolute http/https URL                    | `uri`          |
| `validate.URLWithSchemes(s...)`   | absolute URL restricted to given schemes   | `uri`          |
| `validate.URI`                    | absolute URI with any scheme               | `uri`          |
| `validate.Hostname`               | RFC 1123 hostname                          | `hostname`     |
| `validate.IPv4`                   | dotted-decimal IPv4                        | `ipv4`         |
| `validate.IPv6`                   | IPv6 address                               | `ipv6`         |
| `validate.IP`                     | IPv4 or IPv6 address                       | `ip`           |
| `validate.Date`                   | `YYYY-MM-DD` (ISO 8601)                    | `date`         |
| `validate.Time`                   | RFC 3339 time-only (`HH:MM:SS[.frac]Z/±`) | `time`         |
| `validate.DateTime`               | RFC 3339 date-time (with fractional secs)  | `date-time`    |
| `validate.SemVer`                 | semantic version (`1.2.3`, `v2.0.0-beta`)  | `pattern`      |
| `validate.Slug`                   | `lowercase-hyphen-slug`                    | `pattern`      |
| `validate.CIDR`                   | CIDR notation (`192.168.0.0/24`, `::/0`)   | _(none)_       |

`URLWithSchemes` enables scheme-specific URL validation:
```go
validate.URLWithSchemes("https")        // HTTPS only
validate.URLWithSchemes("ws", "wss")    // WebSocket
validate.URLWithSchemes("grpc")         // gRPC
```

Range / length constraints (with automatic schema annotation):

| Constraint                                                 | Applies to      | Validates       |
| ---------------------------------------------------------- | --------------- | --------------- |
| `validate.MinLen(n)` / `MaxLen(n)`                         | `string`        | character count |
| `validate.NonEmptyString`                                  | `string`        | not empty       |
| `validate.OneOf(values...)`                                | `string`        | enum membership |
| `validate.Pattern(re)`                                     | `string`        | regexp match    |
| `validate.PositiveInt` / `NegativeInt` / `NonZeroInt`      | `int`           | sign            |
| `validate.MinInt(n)` / `MaxInt(n)` / `RangeInt(a,b)`       | `int`           | integer bounds  |
| `validate.PositiveInt32` / `NegativeInt32`                 | `int32`         | sign            |
| `validate.MinInt32(n)` / `MaxInt32(n)` / `RangeInt32(a,b)` | `int32`         | integer bounds  |
| `validate.PositiveInt64` / `NegativeInt64`                 | `int64`         | sign            |
| `validate.MinInt64(n)` / `MaxInt64(n)` / `RangeInt64(a,b)` | `int64`         | integer bounds  |
| `validate.PositiveUint` / `MinUint(n)` / `MaxUint(n)` / `RangeUint(a,b)`         | `uint`          | integer bounds  |
| `validate.PositiveUint64` / `MinUint64(n)` / `MaxUint64(n)` / `RangeUint64(a,b)` | `uint64`        | integer bounds  |
| `validate.PositiveFloat` / `NegativeFloat` / `NonZeroFloat` | `float64`      | sign            |
| `validate.MinFloat(n)` / `MaxFloat(n)` / `RangeFloat(a,b)` | `float64`       | float bounds    |
| `validate.PositiveDuration` / `NonNegativeDuration`        | `time.Duration` | sign            |
| `validate.MinDuration(d)` / `MaxDuration(d)`               | `time.Duration` | duration bounds |

Byte-size constraints (runtime-only, no schema annotation):

| Constraint             | Applies to | Validates              |
| ---------------------- | ---------- | ---------------------- |
| `validate.MaxBytes(n)` | `[]byte`   | decoded byte count ≤ n |
| `validate.MinBytes(n)` | `[]byte`   | decoded byte count ≥ n |

Protocol path/topic constraints (for use with `WithPathConstraints` / `WithTopicConstraints`):

| Constraint                    | Applies to    | Validates                                           |
| ----------------------------- | ------------- | --------------------------------------------------- |
| `validate.HTTPPath`           | `string`      | starts with `/`, no null bytes or spaces; `{param}` placeholders allowed |
| `validate.MQTTPublishTopic`   | `string`      | valid MQTT topic, no wildcard chars (`+`, `#`)      |
| `validate.MQTTTopic`          | `string`      | valid MQTT topic, wildcards allowed (for subscribing) |

Numeric string constraints (for use in `PathParamCodecs` / `TopicParamCodecs`):

| Constraint                         | Applies to | Validates                       |
| ---------------------------------- | ---------- | ------------------------------- |
| `validate.IntString`               | `string`   | valid signed integer string     |
| `validate.PositiveIntString`       | `string`   | integer > 0                     |
| `validate.NonNegativeIntString`    | `string`   | integer ≥ 0                     |
| `validate.IntStringInRange(n, m)`  | `string`   | integer within [n, m] inclusive |

These have no OpenAPI schema annotation — path/topic segments have no standard JSON Schema format keyword.

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

### Custom Constraints

`codex.Constraint[T]` is the public API for defining your own validation rules. Pass any constraint directly to `.Refine()`.

**Inline (one-off):**

```go
var AvatarCodec = codex.Bytes().Refine(codex.Constraint[[]byte]{
    Name:    "maxBytes(65536)",
    Check:   func(v []byte) bool { return len(v) <= 65536 },
    Message: func(v []byte) string {
        return fmt.Sprintf("expected at most 65536 bytes, got %d", len(v))
    },
})
```

**Reusable (like `validate/*`):**

```go
func MaxBytes(n int) codex.Constraint[[]byte] {
    return codex.Constraint[[]byte]{
        Name:  fmt.Sprintf("maxBytes(%d)", n),
        Check: func(v []byte) bool { return len(v) <= n },
        Message: func(v []byte) string {
            return fmt.Sprintf("expected at most %d bytes, got %d", n, len(v))
        },
    }
}

var AvatarCodec = codex.Bytes().Refine(MaxBytes(65536))
```

**With schema annotation** — set `Constraint.Schema` to propagate constraint metadata into the generated OpenAPI/AsyncAPI schema:

```go
func MaxLen(n int) codex.Constraint[string] {
    return codex.Constraint[string]{
        Name:  fmt.Sprintf("maxLen(%d)", n),
        Check: func(v string) bool { return len(v) <= n },
        Message: func(v string) string {
            return fmt.Sprintf("expected at most %d characters, got %d", n, len(v))
        },
        Schema: func(s schema.Schema) schema.Schema {
            s.MaxLength = &n    // ← reflected into OpenAPI output automatically
            return s
        },
    }
}
```

The `validate/` package ships ready-made constraints using this exact pattern (`MinLen`, `MaxLen`, `RangeInt`, `Email`, etc.). `validate.MaxBytes` and `validate.MinBytes` are built-in for `[]byte` byte-count limits.

### Cross-Field Constraints: `RefineFunc`

`RefineFunc` wraps a plain `func(T) error` as a post-decode constraint. Use it on struct codecs to validate relationships between fields without defining a named `Constraint[T]`.

```go
type DateRange struct {
    Start time.Time
    End   time.Time
}

var dateRangeCodec = codex.Struct[DateRange](
    codex.RequiredField[DateRange, time.Time]("start", codex.Time(),
        func(r DateRange) time.Time { return r.Start },
        func(r *DateRange, v time.Time) { r.Start = v }),
    codex.RequiredField[DateRange, time.Time]("end", codex.Time(),
        func(r DateRange) time.Time { return r.End },
        func(r *DateRange, v time.Time) { r.End = v }),
).RefineFunc(func(r DateRange) error {
    if !r.End.After(r.Start) {
        return errors.New("end must be after start")
    }
    return nil
})
```

On failure, `RefineFunc` produces a `ConstraintError{Name:"refine", ...}` — the same error type as `Refine`.

### Available Codec Types

| Constructor                              | Go type        | JSON wire           | Schema                                     |
| ---------------------------------------- | -------------- | ------------------- | ------------------------------------------ |
| `codex.Int()`                            | `int`          | number              | `{type:integer}`                           |
| `codex.Int32()`                          | `int32`        | number              | `{type:integer,format:int32}`              |
| `codex.Int64()`                          | `int64`        | number              | `{type:integer}`                           |
| `codex.Uint()`                           | `uint`         | number              | `{type:integer,minimum:0}`                 |
| `codex.Uint64()`                         | `uint64`       | number              | `{type:integer,minimum:0}`                 |
| `codex.Float32()`                        | `float32`      | number              | `{type:number,format:float}`               |
| `codex.Float64()`                        | `float64`      | number              | `{type:number}`                            |
| `codex.String()`                         | `string`       | string              | `{type:string}`                            |
| `codex.Bool()`                           | `bool`         | boolean             | `{type:boolean}`                           |
| `codex.Bytes()`                          | `[]byte`       | base64 string       | `{type:string,format:byte}`                |
| `codex.Time()`                           | `time.Time`    | RFC 3339 string     | `{type:string,format:date-time}`           |
| `codex.Date()`                           | `time.Time`    | `YYYY-MM-DD` string | `{type:string,format:date}`                |
| `codex.Duration()`                       | `time.Duration`| duration string     | `{type:string,format:duration}`            |
| `codex.Nullable(inner)`                  | `*T`           | value or `null`     | inner schema + `nullable:true`             |
| `codex.SliceOf(elem)`                    | `[]T`          | array               | `{type:array,items:{...}}`                 |
| `codex.StringMap(value)`                 | `map[string]V` | object              | `{type:object,additionalProperties:{...}}` |
| `codex.Struct[T](fields...)`                    | any struct     | object              | `{type:object,properties:{...}}`           |
| `codex.TaggedUnion[T](tag, variants...)`        | any interface  | object              | `{oneOf:[...],discriminator:{...}}`        |
| `codex.UntaggedUnion[T](which, variants...)`    | any interface  | object              | `{oneOf:[...]}` (no discriminator)         |
| `codex.Either2(ca, cb)`                         | `Either[A,B]`  | value               | `{oneOf:[schemaA,schemaB]}`                |
| `codex.Any()`                                   | `any`          | any                 | `{}` (accepts all)                         |
| `codex.Pure(value)`                             | `T`            | fixed wire value    | `{enum:[value]}`                           |
| `codex.Eq(base, value)`                         | `T comparable` | validated by base   | base schema + `{enum:[value]}`             |

```go
// Nullable pointer field
var noteCodec = codex.Nullable(codex.String())  // Codec[*string]
note, _ := noteCodec.Decode(nil)                // → (*string)(nil)
s := "hello"
enc, _ := noteCodec.Encode(&s)                  // → "hello"
enc, _ = noteCodec.Encode(nil)                  // → nil (JSON null)

// Time and Date
var createdAtCodec = codex.Time()               // Codec[time.Time]
enc, _ := createdAtCodec.Encode(time.Now())     // → "2024-06-15T12:00:00Z"

// StringMap
var tagsCodec = codex.StringMap(codex.String()) // Codec[map[string]string]
enc, _ := tagsCodec.Encode(map[string]string{"env":"prod"})
// → map[string]any{"env":"prod"}

// Any — opaque passthrough, no type enforcement
var rawCodec = codex.Any()
val, _ := rawCodec.Decode(map[string]any{"x": 1}) // passes through unchanged
```

### `Either[A, B]` — Typed Sum Type

`Either2` tries codec A first; if decode fails, tries codec B. Encode uses whichever branch is non-nil. Schema emits `{oneOf: [schemaA, schemaB]}`.

```go
// A config value that is either a string DSN or a structured DBConfig
type DBConfig struct { Host string; Port uint }
var dbConfigCodec = codex.Struct[DBConfig](...)

var dsnOrConfig = codex.Either2(codex.String(), dbConfigCodec)
// Codec[codex.Either[string, DBConfig]]

// Decode from a plain string
left, _ := dsnOrConfig.Decode("postgres://localhost/db")
// left.Left = &"postgres://localhost/db", left.Right = nil

// Decode from a structured object
right, _ := dsnOrConfig.Decode(map[string]any{"host": "localhost", "port": float64(5432)})
// right.Left = nil, right.Right = &DBConfig{...}
```

If both branches fail, `Decode` returns `EitherError{Errors: []error{errA, errB}}`. Left branch wins on ambiguity.

### `UntaggedUnion[T]` — Interface Union Without Discriminator

`UntaggedUnion` is the complement to `TaggedUnion` for cases where the encoded form has no discriminator field. Decode tries each variant in order; encode uses the explicit `which` selector.

```go
type Shape interface{ area() float64 }
type Circle struct{ Radius float64 }
type Rect   struct{ W, H   float64 }

var shapeCodec = codex.UntaggedUnion[Shape](
    func(s Shape) int {
        switch s.(type) {
        case Circle: return 0
        case Rect:   return 1
        }
        return -1
    },
    codex.UntaggedVariant[Shape]{Name: "circle", Codec: codex.MapCodecSafe(circleCodec, ...)},
    codex.UntaggedVariant[Shape]{Name: "rect",   Codec: codex.MapCodecSafe(rectCodec, ...)},
)
```

Decode: first-match wins. Encode: `which(v)` returns the variant index (0-based). Schema: `{oneOf: [{...circle...}, {...rect...}]}`.

If all branches fail, returns `EitherError{Errors: [...]}`.

### `Pure[T]` and `Eq[T]` — Fixed and Single-Value Codecs

`Pure` always decodes to a fixed value (ignoring wire input) and always encodes that value (ignoring the Go value). Use for protocol version fields, derived fields set automatically, or any field that must always carry one specific value.

`Eq` wraps a base codec with an equality constraint. The base codec handles type coercion; `Eq` then rejects anything that doesn't equal `value`. Schema sets `{enum: [value]}`.

```go
// CloudEvents 1.0 envelope
type CloudEvent struct {
    SpecVersion string
    Type        string
    ID          string
}

var CloudEventCodec = codex.Struct[CloudEvent](
    codex.Field[CloudEvent, string]{
        Name:  "specversion",
        // Pure: always decodes to "1.0"; always encodes "1.0". Wire value is ignored.
        Codec: codex.Pure("1.0").WithDescription("CloudEvents spec version."),
        ...
    },
    codex.Field[CloudEvent, string]{
        Name:  "type",
        // Eq: String() handles type coercion; then only "com.example.order.placed" passes.
        Codec: codex.Eq(codex.String(), "com.example.order.placed"),
        ...
    },
)

// Decode: specversion wire value is ignored
event, _ := CloudEventCodec.Decode(map[string]any{
    "specversion": "ignored",
    "type":        "com.example.order.placed",
    "id":          "550e8400-...",
})
// event.SpecVersion == "1.0"  ← Pure always returns the fixed value

// Eq rejects wrong type
_, err := CloudEventCodec.Decode(map[string]any{
    "specversion": "1.0",
    "type":        "com.example.user.created",  // wrong
    "id":          "550e8400-...",
})
// err: constraint failed (eq(com.example.order.placed)): expected ...
```

See [`examples/event-driven`](examples/event-driven/main.go) for a runnable demo.

### Codec Transformations: `MapCodecSafe` and `MapCodecValidated`

Both combinators build a `Codec[B]` from an existing `Codec[A]` by supplying mapping functions. Choose based on how much validation you need.

#### `MapCodecSafe` — type mapping, infallible decode direction

```go
func MapCodecSafe[A, B any](c Codec[A], to func(A) B, from func(B) (A, error)) Codec[B]
```

- `to` (decode direction) must always succeed — it is a total function.
- `from` (encode direction) may return an error.
- Schema is inherited from `Codec[A]` (the wire codec).
- Use for newtype wrappers: `type Email string` over `codex.String()`.

```go
type Email string

var EmailCodec = codex.MapCodecSafe(
    codex.String(),
    func(s string) Email { return Email(s) },
    func(e Email) (string, error) { return string(e), nil },
)
```

#### `MapCodecValidated` — fallible mapping with post-decode validation

```go
func MapCodecValidated[A, B any](ca Codec[A], cb Codec[B], to func(A) (B, error), from func(B) (A, error)) Codec[B]
```

- Both `to` and `from` may return an error.
- After mapping `A → B`, `cb.Validate(b)` enforces all `Refine` constraints defined on `cb`.
- Validation also runs on the encode direction before `from` is called.
- Schema comes from `cb` (the domain type with its constraints).
- Use when the mapping itself is fallible **and** the target type `B` carries its own validation rules.

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
        if f != f { // NaN
            return 0, errors.New("NaN is not a valid temperature")
        }
        return Celsius(f), nil
    },
    func(c Celsius) (float64, error) { return float64(c), nil },
)

temp, err := celsiusCodec.Decode(float64(36.6)) // → Celsius(36.6), nil
_, err = celsiusCodec.Decode(float64(-300))     // → error: below absolute zero
_, err = celsiusCodec.Encode(Celsius(2e6))      // → error: exceeds maximum
```

See [`examples/codec-mapping`](examples/codec-mapping/main.go) for a full example showing all three codec reuse patterns: shared field codec variables, sub-codec direct reuse, `MapCodecSafe`, and `MapCodecValidated`.

### OpenAPI Schema Generation

[Spec: openapis.org - OpenAPI 3.2.0](https://spec.openapis.org/oas/v3.2.0.html)

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

### Full OpenAPI 3.1 Document

`render/openapi` can emit a complete OpenAPI 3.1 document — not just `components/schemas` — using the `DocumentBuilder`. Define HTTP routes with `route.Route` descriptors that reference codec schemas; the builder assembles paths, operations, parameters, request bodies, responses, and `components/schemas` in one step.

Schemas named via `Body.SchemaName` or `Response.SchemaName` are automatically registered in `components/schemas` and referenced with `$ref`. Unnamed schemas are inlined.

```go
import (
    "github.com/DaniDeer/go-codex/render/openapi"
    "github.com/DaniDeer/go-codex/route"
)

doc, err := openapi.NewDocumentBuilder(openapi.Info{
    Title:   "User API",
    Version: "1.0.0",
}).
    AddServer(openapi.Server{URL: "https://api.example.com/v1"}).
    AddRoute(route.Route{
        Method:      "POST",
        Path:        "/users",
        OperationID: "createUser",
        Summary:     "Create a user",
        RequestBody: &route.Body{
            Required:   true,
            Schema:     CreateUserRequestCodec.Schema,
            SchemaName: "CreateUserRequest", // → $ref + registered in components
        },
        Responses: []route.Response{
            {Status: "201", Description: "Created", Schema: &UserCodec.Schema, SchemaName: "User"},
            {Status: "400", Description: "Validation error."},
        },
    }).
    AddRoute(route.Route{
        Method: "GET",
        Path:   "/users/{id}",
        PathParams: []route.Param{
            {Name: "id", Required: true, Schema: schema.Schema{Type: "string", Format: "uuid"}},
        },
        Responses: []route.Response{
            {Status: "200", Description: "OK", Schema: &UserCodec.Schema, SchemaName: "User"},
            {Status: "204", Description: "No Content"}, // no body — content omitted
        },
    }).
    Build()

yamlBytes, err := doc.MarshalYAML()
```

`Build()` validates:

- No duplicate `(method, path)` pairs.
- `PathParams` names exactly match `{placeholder}` segments in the path.
- Path parameters are always `required: true` in the output.

See `examples/rest-api/` for a runnable demonstration.

### AsyncAPI 2.6 Document

[Spec: asyncapi.com - specification 3.1.0](https://www.asyncapi.com/docs/reference/specification/v3.1.0)

`render/asyncapi` produces a full AsyncAPI 2.6 document from channel descriptors. The same `schema.Schema` that drives OpenAPI output also describes AsyncAPI message payloads — no duplication.

```go
import "github.com/DaniDeer/go-codex/render/asyncapi"

doc, err := asyncapi.NewDocumentBuilder(asyncapi.Info{
    Title:   "User Events",
    Version: "1.0.0",
}).
    AddServer("production", asyncapi.Server{
        URL:      "amqp://broker.example.com",
        Protocol: "amqp",
    }).
    AddChannel("user/created", asyncapi.ChannelItem{
        Subscribe: &asyncapi.Operation{
            Summary: "User created",
            Message: asyncapi.Message{
                Schema:     UserCreatedEventCodec.Schema,
                SchemaName: "UserCreatedEvent", // → $ref + registered in components
            },
        },
    }).
    Build()

yamlBytes, err := doc.MarshalYAML()
```

Output (trimmed):

```yaml
asyncapi: 2.6.0
info:
  title: User Events
  version: 1.0.0
channels:
  user/created:
    subscribe:
      summary: User created
      message:
        payload:
          $ref: "#/components/schemas/UserCreatedEvent"
components:
  schemas:
    UserCreatedEvent:
      type: object
      properties:
        id: { type: string, format: uuid }
        name: { type: string, minLength: 1 }
```

See `examples/event-driven/` for a runnable demonstration.

### REST API Builder

`api/rest` is a transport-agnostic REST API builder. Register routes with codec-backed request and response types; the builder returns a `RouteHandle` with typed `Decode` and `Encode` helpers. Pass those helpers to any HTTP framework — this package imports **no HTTP library**.

The same builder generates a complete OpenAPI 3.1 spec from all registered routes.

```go
import (
    "github.com/DaniDeer/go-codex/api/rest"
    "github.com/DaniDeer/go-codex/validate"
)

// WithPathConstraints validates every registered path at AddRoute time.
b := rest.NewBuilder(
    rest.Info{Title: "User API", Version: "1.0.0"},
    rest.WithPathConstraints(validate.HTTPPath),
)
b.AddServer(rest.Server{URL: "https://api.example.com/v1"})

// AddRoute returns (RouteHandle, error) — path is validated immediately.
createUser, err := rest.AddRoute[CreateUserRequest, User](b, "POST", "/users",
    createUserCodec, userCodec,
    rest.RouteConfig{
        OperationID:    "createUser",
        Summary:        "Create a user",
        ReqSchemaName:  "CreateUserRequest",
        RespSchemaName: "User",
        Responses: []rest.ResponseMeta{
            {Status: "400", Description: "Validation error."},
        },
    })
if err != nil {
    log.Fatal(err) // *rest.InvalidPathError if path is invalid
}

// Route with a path variable. PathParam.Codec validates {id} at BuildPath time
// and its schema (UUID) flows into the OpenAPI spec automatically.
uuidCodec := codex.String().Refine(validate.UUID)
getUser, err := rest.AddRoute[struct{}, User](b, "GET", "/users/{id}",
    codex.Struct[struct{}](), userCodec,
    rest.RouteConfig{
        OperationID:    "getUser",
        Summary:        "Get a user by ID",
        RespSchemaName: "User",
        PathParams: []rest.PathParam{{
            Name:        "id",
            Description: "User UUID",
            Codec:       &uuidCodec, // validates at BuildPath time; UUID schema → spec
        }},
    })
if err != nil {
    log.Fatal(err)
}

// BuildPath substitutes {id} and validates it against the UUID codec.
path, err := getUser.BuildPath(map[string]string{"id": "f47ac10b-58cc-4372-a567-0e02b2c3d479"})
if err != nil {
    var missingVar *rest.MissingPathVarError
    var paramErr *rest.PathParamError
    switch {
    case errors.As(err, &missingVar):
        log.Fatalf("missing path variable: %s", missingVar.Name)
    case errors.As(err, &paramErr):
        log.Fatalf("invalid value for path variable %s: %v", paramErr.Name, paramErr.Err)
    }
}
// path == "/users/f47ac10b-58cc-4372-a567-0e02b2c3d479"

// Route with query parameters. QueryParam.Codec validates values at ValidateQuery time
// and its schema flows into the OpenAPI query parameter spec automatically.
pageCodec := codex.String().Refine(validate.NonNegativeIntString)
listUsers, err := rest.AddRoute[struct{}, []User](b, "GET", "/users",
    codex.Struct[struct{}](), codex.SliceOf(userCodec),
    rest.RouteConfig{
        OperationID: "listUsers",
        Summary:     "List users",
        QueryParams: []rest.QueryParam{
            {Name: "page", Description: "Page number (0-based)", Codec: &pageCodec},
            {Name: "search", Description: "Name filter (no validation)"},
        },
    })
if err != nil {
    log.Fatal(err)
}

// ValidateQuery checks codec-backed params; skips params with nil Codec or absent keys.
if err := listUsers.ValidateQuery(map[string]string{"page": "abc"}); err != nil {
    var qErr *rest.QueryParamError
    if errors.As(err, &qErr) {
        log.Fatalf("bad query param %q: %v", qErr.Name, qErr.Err)
    }
}

// In your HTTP handler — works with net/http, Gin, Chi, Echo, anything:
req, err := createUser.Decode(body)   // JSON → CreateUserRequest, validates
user, err := myService.Create(req)
out, err  := createUser.Encode(user)  // User → JSON

// Route descriptor for your framework's router:
fmt.Println(createUser.Descriptor.Method, createUser.Descriptor.Path) // POST /users

// OpenAPI 3.1 spec from all registered routes:
doc, err := b.OpenAPISpec()
yamlBytes, _ := doc.MarshalYAML()
```

**Builder options:**

| Option | Effect |
| --- | --- |
| `WithPathCodec(c)` | Validates paths against codec `c` at `AddRoute` time |
| `WithPathConstraints(cs...)` | Validates paths against one or more constraints at `AddRoute` time |

**Template-transparent validation:** constraints run on the *structural shape* of the path, not the literal template syntax. `{varName}` placeholders are replaced with `x` before validation (`/users/{id}` → `/users/x`). Any constraint — including ones that reject `{` or `}` — works correctly on parameterised routes. The stored `Descriptor.Path` is always the original template.

**Final path re-validation:** `BuildPath` re-validates the fully assembled path (e.g. `/users/hello world`) against the builder-level codec after substitution. This catches variable values that pass their `PathParamCodecs` codec but violate the global path constraint. Returns `InvalidPathError` with the final path in the `Path` field.

**Error types from `AddRoute`, `BuildPath`, and `ValidateQuery`:**

| Error type | When returned | `errors.As` target |
| --- | --- | --- |
| `*rest.InvalidPathError` | Path fails builder-level validation | `InvalidPathError{Path, Err}` |
| `*rest.PathParamError` | A path variable value fails its codec | `PathParamError{Name, Value, Err}` |
| `*rest.MissingPathVarError` | A template variable is absent from the `vars` map | `MissingPathVarError{Name}` |
| `*rest.InvalidPathParamError` | A `PathParams` entry names a variable not in the template | `InvalidPathParamError{Name, Path}` |
| `*rest.QueryParamError` | A query parameter value fails its codec | `QueryParamError{Name, Value, Err}` |

**Codec schema → OpenAPI spec:** `PathParam.Codec` schema flows automatically into the OpenAPI path parameter spec. `QueryParam.Codec` schema flows into the OpenAPI query parameter spec. When `Codec` is nil, the parameter is still declared in the spec (minimal entry).

**Future:** framework-specific adapters (`adapters/gin`, `adapters/chi`, etc.) will wrap `RouteHandle` for zero-boilerplate integration. The `api/rest` core stays dependency-free.

See `examples/api-rest/` for a runnable demonstration, and `examples/adapters-nethttp/` for the net/http adapter.

### net/http Adapter

`adapters/nethttp` wires a `RouteHandle` to `net/http` in one line. No boilerplate for body reading, JSON encoding, or error response formatting.

```go
import nethttp "github.com/DaniDeer/go-codex/adapters/nethttp"

mux := http.NewServeMux()

// Register uses the Go 1.22+ "METHOD /path" ServeMux pattern automatically.
nethttp.Register(mux, createUser, func(ctx context.Context, req CreateUserReq) (User, error) {
    return svc.CreateUser(ctx, req)
}, nethttp.Options{})

http.ListenAndServe(":8080", mux)
```

- POST/PUT/PATCH: body read → `handle.Decode` (validates) → handler → `handle.Encode` → write
- GET/HEAD/DELETE: handler called with zero value of `Req`; path/query extraction via `RequestFromContext`
- **Query param validation**: `ValidateQuery` is called automatically before the handler; codec-backed `QueryParam` entries are validated from `r.URL.Query()`
- Errors: `{"error":"..."}` JSON — 400 for decode/query validation failures, 500 for handler/encode failures
- Response status: taken from the route descriptor's primary response (e.g. 201 for POST)
- **Custom error handling**: `Options.ErrorHandler func(w, r, status, err)` overrides the default JSON envelope
- **Metrics / observability**: `Options.Observer stats.Observer` — implement [`stats.Observer`](#stats--observer) to receive per-request events; defaults to `stats.NoopObserver`

### Codec as domain boundary: the functional pipeline

A codec is not just a validator — it is the **public contract** of a module boundary. Every boundary — HTTP request, database, HTTP response — is modelled as a codec. Constraints defined on shared field codec variables propagate to all boundaries that reference them: one definition, zero duplication.

The architecture has three cleanly separated layers:

```
┌────────────────────────────────────────────────────────────────────┐
│  LAYER 1 — DOMAIN MODELS + CONSTRAINTS                             │
│                                                                     │
│  emailFieldCodec  = codex.String().Refine(validate.Email)          │
│  nameFieldCodec   = codex.String().Refine(validate.NonEmpty)       │
│                                         ↑                           │
│  createUserReqCodec  Codec[CreateUserReq]  │ shared field codecs   │
│  userRecordCodec     Codec[UserRecord]     ┤ defined once          │
│  userCodec           Codec[User]           ┘ used in all three     │
├────────────────────────────────────────────────────────────────────┤
│  LAYER 2 — BUSINESS LOGIC (pure domain functions, zero IO)         │
│                                                                     │
│  buildUserRecord(CreateUserReq) UserRecord                         │
│  buildUserResponse(UserRecord) User                                │
│    ← no database, no HTTP, no side effects                         │
│    ← independently unit-testable with plain Go structs             │
├────────────────────────────────────────────────────────────────────┤
│  LAYER 3 — INFRASTRUCTURE (HTTP + database + external services)    │
│                                                                     │
│  UserStore — uses userRecordCodec.Encode/Decode for all DB IO      │
│  makeCreateUserHandler(store) — orchestrates L2 + L3               │
│  nethttp.Register(mux, route, handler) — the only HTTP line        │
│  b.OpenAPISpec()               — the only OpenAPI line             │
│    ← swap to gRPC, CLI, or test without touching L1 or L2          │
└────────────────────────────────────────────────────────────────────┘
```

**The pipeline for POST /users:**

```
Codec[Req] ─ decode ─▶ CreateUserReq ─▶ buildUserRecord ─▶ UserRecord
                                                               ↓ store.Save (Codec[UserRecord].Encode)
Codec[Resp] ─ encode ─▶ User ◀─ buildUserResponse ◀─ UserRecord
```

**Shared field codecs** define each domain constraint once and propagate it to all three boundary codecs — HTTP request (required), database schema (required), HTTP response (optional):

```go
var emailFieldCodec = codex.String().Refine(validate.Email).WithDescription("Email address.")

var createUserReqCodec = codex.Struct[CreateUserReq](
    codex.RequiredField[CreateUserReq, string]("email", emailFieldCodec, ...),
)
var userRecordCodec = codex.Struct[UserRecord](
    codex.RequiredField[UserRecord, string]("email", emailFieldCodec, ...),
)
var userCodec = codex.Struct[User](
    codex.OptionalField[User, string]("email", emailFieldCodec, ...),
)
```

**The database store uses the codec for all IO** — schema definition and serialization are the same object:

```go
func (s *UserStore) Save(r UserRecord) error {
    encoded, _ := userRecordCodec.Encode(r)   // map[string]any — like SQL INSERT
    s.rows[r.ID] = encoded.(map[string]any)
    return nil
}

func (s *UserStore) Get(id string) (UserRecord, bool) {
    row := s.rows[id]
    record, _ := userRecordCodec.Decode(row)  // validates on read — like SQL scan
    return record, true
}
```

**Pure domain functions** are independently testable — no store, no HTTP server required:

```go
func TestBuildUserRecord(t *testing.T) {
    req := CreateUserReq{Name: "Alice", Email: "alice@example.com"}
    record := buildUserRecord(req) // Layer 2 called directly
    // assert on record — zero setup
}
```

See [`examples/adapters-nethttp`](examples/adapters-nethttp/main.go) for the full runnable demonstration including tests.

**`MapCodecSafe` / `MapCodecValidated` are different:** they produce a single `Codec[B]` where encode uses A's wire format. They are designed for **same-wire bidirectional mappings** (newtypes, DSN strings) — not for HTTP request→response where the two wire formats differ. See [Codec Transformations](#codec-transformations-mapcodecsafe-and-mapcodecvalidated).

### Event Channel Builder

`api/events` is a transport-agnostic event channel builder. Register channels with codec-backed payload types; the builder returns a `ChannelHandle` with typed `Decode` and `Encode` helpers. Pass those helpers to any message broker — this package imports **no messaging library**.

The same builder generates a complete AsyncAPI 2.6 spec from all registered channels.

```go
import (
    "github.com/DaniDeer/go-codex/api/events"
    "github.com/DaniDeer/go-codex/validate"
)

// WithTopicConstraints validates every registered topic at AddChannel time.
b := events.NewBuilder(
    events.Info{Title: "User Events", Version: "1.0.0"},
    events.WithTopicConstraints(validate.MQTTPublishTopic),
)
b.AddServer("production", events.Server{URL: "amqp://broker.example.com", Protocol: "amqp"})

// AddChannel returns (ChannelHandle, error) — topic is validated immediately.
userCreated, err := events.AddChannel[UserCreatedEvent](b, "user/created", userCreatedCodec,
    events.ChannelConfig{
        Subscribe: &events.OperationConfig{
            Summary:    "A user was created",
            SchemaName: "UserCreatedEvent",
        },
    })
if err != nil {
    log.Fatal(err) // *events.InvalidTopicError if topic is invalid
}

// Channel with a topic template. TopicParam.Codec validates {sensorID} at
// BuildTopic time; the UUID schema flows into the AsyncAPI parameters: block.
sensorUUIDCodec := codex.String().Refine(validate.UUID)
sensorMeasurement, err := events.AddChannel[Measurement](b, "sensors/{sensorID}/measurements",
    measurementCodec,
    events.ChannelConfig{
        Subscribe: &events.OperationConfig{Summary: "Sensor measurement received"},
        // TopicParam.Codec validates + flows schema to spec. Description enriches spec.
        TopicParams: []events.TopicParam{{
            Name:        "sensorID",
            Description: "UUID of the sensor publishing the measurement.",
            Codec:       &sensorUUIDCodec,
        }},
    })
if err != nil {
    log.Fatal(err)
}

// BuildTopic substitutes {sensorID} and validates it against the UUID codec.
topic, err := sensorMeasurement.BuildTopic(map[string]string{
    "sensorID": "f47ac10b-58cc-4372-a567-0e02b2c3d479",
})
if err != nil {
    var missingVar *events.MissingTopicVarError
    var paramErr *events.TopicParamError
    switch {
    case errors.As(err, &missingVar):
        log.Fatalf("missing topic variable: %s", missingVar.Name)
    case errors.As(err, &paramErr):
        log.Fatalf("invalid value for topic variable %s: %v", paramErr.Name, paramErr.Err)
    }
}
// topic == "sensors/f47ac10b-58cc-4372-a567-0e02b2c3d479/measurements"

// In your broker callback — works with Paho MQTT, AMQP, Kafka, NATS, anything:
event, err := userCreated.Decode(msg.Payload()) // JSON → UserCreatedEvent, validates
handleUserCreated(event)

// AsyncAPI 2.6 spec from all registered channels:
doc, err := b.AsyncAPISpec()
yamlBytes, _ := doc.MarshalYAML()
```

Both subscribe and publish directions can be registered on the same channel:

```go
events.AddChannel[UserEvent](b, "user/events", codec, events.ChannelConfig{
    Subscribe: &events.OperationConfig{Summary: "Receive user events"},
    Publish:   &events.OperationConfig{Summary: "Send user events"},
})
```

**Builder options:**

| Option | Effect |
| --- | --- |
| `WithTopicCodec(c)` | Validates topics against codec `c` at `AddChannel` time |
| `WithTopicConstraints(cs...)` | Validates topics against one or more constraints at `AddChannel` time |

**Template-transparent validation:** constraints run on the *structural shape* of the topic, not the literal template syntax. `{varName}` placeholders are replaced with `x` before validation (`sensors/{sensorID}/measurements` → `sensors/x/measurements`). Any constraint works correctly on template topics. The stored `ChannelHandle.Topic` is always the original template.

**Final topic re-validation:** `BuildTopic` re-validates the fully assembled topic against the builder-level codec after substitution. Catches variable values that pass their `TopicParamCodecs` codec but violate the global topic constraint. Returns `InvalidTopicError` with the final topic in the `Topic` field.

**Error types from `AddChannel` and `BuildTopic`:**

| Error type | When returned | `errors.As` target |
| --- | --- | --- |
| `*events.InvalidTopicError` | Topic fails builder-level validation | `InvalidTopicError{Topic, Err}` |
| `*events.TopicParamError` | A topic variable value fails its codec | `TopicParamError{Name, Value, Err}` |
| `*events.MissingTopicVarError` | A template variable is absent from the `vars` map | `MissingTopicVarError{Name}` |
| `*events.InvalidTopicParamError` | A `TopicParams` entry names a variable not in the template | `InvalidTopicParamError{Name, Topic}` |

**Codec schema → AsyncAPI spec:** `TopicParam.Codec` schema flows automatically into the AsyncAPI `parameters:` block. Every `{varName}` placeholder in the topic gets a parameter entry — auto-generated as `{type: string}` when no `TopicParam` is declared. Set `TopicParam.Codec` to add runtime validation and emit the codec schema; set `TopicParam.Description` to enrich the spec without validation.

**Future:** broker-specific adapters (`adapters/amqp`, `adapters/kafka`, etc.) will wrap `ChannelHandle` for zero-boilerplate integration.

See `examples/api-events/` for a runnable demonstration, and `examples/adapters-mqtt/` for the Paho MQTT adapter.

### Paho MQTT Adapter

`adapters/mqtt` wires a `ChannelHandle` to Paho MQTT with production-ready error handling and observability. `SubscribeHandler` returns a `mqtt.MessageHandler` ready to pass to `client.Subscribe`. `Publish` encodes the value and publishes it, waiting for broker acknowledgement with context-aware cancellation.

**Pattern: separate loggers for domain and transport concerns:**

```go
import (
    "errors"
    "log/slog"
    mqtt    "github.com/eclipse/paho.mqtt.golang"
    amqtt   "github.com/DaniDeer/go-codex/adapters/mqtt"
    "github.com/DaniDeer/go-codex/codex"
)

// Create separate loggers for business logic and transport errors.
domainLogger  := slog.Default().With("layer", "domain")
mqttLogger    := slog.Default().With("transport", "mqtt")

// Domain logging decorator — separates logging concern from handler body.
handler := withDomainLoggingErr("measurement.process",
    makeHandleMeasurement(store, threshold, publishAlert),
    domainLogger,
    extractMeasurementAttrs,
)

// Subscribe with structured error handling — distinguish decode vs handler failures.
// For template topics (e.g. "sensors/{sensorID}/measurements"), build the concrete
// topic with BuildTopic before subscribing.
topic, _ := sensorMeasurement.BuildTopic(map[string]string{"sensorID": sensorUUID})
client.Subscribe(topic, 1,
    amqtt.SubscribeHandler(ctx, sensorMeasurement, handler,
        amqtt.SubscribeOptions{
            OnError: func(e amqtt.SubscribeError) {
                // e.Topic is the concrete incoming topic (msg.Topic()), not the template.
                switch e.Kind {
                case amqtt.KindDecode:
                    var validationErrs codex.ValidationErrors
                    if errors.As(e.Err, &validationErrs) {
                        mqttLogger.Warn("decode failed: validation errors",
                            "topic", e.Topic,
                            "errors", validationErrs, // triggers ValidationErrors.LogValue()
                        )
                    } else {
                        mqttLogger.Warn("decode failed", "topic", e.Topic, "error", e.Err)
                    }
                case amqtt.KindHandler:
                    mqttLogger.Error("handler failed", "topic", e.Topic, "error", e.Err)
                }
            },
        },
    ),
)

// Publish — static topic: pass nil for vars.
err := amqtt.Publish(ctx, client, alertChannel, 1, false, AlertEvent{...}, nil,
    amqtt.PublishOptions{})

// Publish — template topic: pass vars map; BuildTopic is called internally.
err = amqtt.Publish(ctx, client, sensorMeasurement, 1, false, m,
    map[string]string{"sensorID": sensorUUID}, amqtt.PublishOptions{Observer: mqttObs})
```

**`SubscribeError.Kind` distinguishes:**
- `KindDecode` — codec validation failure (user error, log Warn)
- `KindHandler` — application logic failure (system error, log Error)

**`SubscribeError.Topic`** — always the concrete incoming message topic, even for template channels (e.g. `sensors/abc-123/measurements` not `sensors/{sensorID}/measurements`).

**`MessageFromContext`** — access the raw `pahomqtt.Message` from inside a handler, analogous to `nethttp.RequestFromContext`. Gives access to `Qos()`, `Retained()`, `MessageID()`, `Duplicate()` without breaking the typed handler signature:

```go
amqtt.SubscribeHandler(ctx, measurementChannel,
    func(ctx context.Context, m MeasurementEvent) error {
        if msg, ok := amqtt.MessageFromContext(ctx); ok {
            if msg.Retained() {
                return nil // skip stale retained message on reconnect
            }
        }
        return svc.HandleMeasurement(ctx, m)
    }, amqtt.SubscribeOptions{})
```

**`Publish` signature:**
```go
func Publish[T any](
    ctx      context.Context,
    client   pahomqtt.Client,
    handle   *events.ChannelHandle[T],
    qos      byte,
    retained bool,
    msg      T,
    vars     map[string]string,  // nil → use handle.Topic; non-nil → BuildTopic(vars)
    opts     PublishOptions,     // zero value = NoopObserver
) error
```

**Structured logging:** all codec error types (`ValidationErrors`, `ConstraintError`, `TypeMismatchError`, etc.) implement `slog.LogValuer`. Using `slog.Any("errors", err)` triggers the full nested structure — field names, constraint details, type mismatches — without string parsing.

See [`examples/adapters-mqtt`](examples/adapters-mqtt/main.go) for the full runnable demonstration including tests — measurement ingestion from a sensor network, time series storage, and threshold-breach alerts using the three-layer codec pipeline pattern.

### Stats / Observer

`stats` exposes two levels of observability, both dependency-free:

**Codec-level** — use when calling codecs directly (config validation, protocol parsing, any non-HTTP/MQTT use case). Implement just `stats.ValidationObserver` (one method) and call `stats.ReportErrors`:

```go
// Implement only the codec-level hook — no HTTP/MQTT stubs
type ConfigObserver struct{}
func (o *ConfigObserver) RecordValidationError(location, constraint, field string) {
    // increment counter, emit log, etc.
}

// After any codec.Decode call:
val, err := appConfigCodec.Decode(rawData)
stats.ReportErrors(&ConfigObserver{}, "config", err) // calls RecordValidationError per failing field
```

`stats.ConstraintName(err)` extracts a stable label from any field-level error (`ConstraintError.Name`, `"type-mismatch"`, `"required"`, or `""`).

**Adapter-level** — use `stats.Observer` (embeds `ValidationObserver`) when wiring to HTTP or MQTT adapters:

```go
// stats/observer.go
type ValidationObserver interface {
    RecordValidationError(location, constraintName, field string)
}

type Observer interface {
    ValidationObserver
    RecordRequest(method, path string, statusCode int, duration time.Duration)
    RecordSubscribe(topic string, success bool, duration time.Duration)  // MQTT
    RecordPublish(topic string, success bool, duration time.Duration)    // MQTT
}
```

**Wiring to nethttp:**
```go
nethttp.Register(mux, createUser, handler, nethttp.Options{
    Observer: myObserver, // stats.Observer implementation
})
```

**Wiring to mqtt (subscribe + publish):**
```go
amqtt.SubscribeHandler(ctx, channel, handler, amqtt.SubscribeOptions{
    Observer: myObserver, // RecordSubscribe + RecordValidationError("payload", ...)
    OnError:  onErr,
})

amqtt.Publish(ctx, client, channel, qos, retained, msg, vars, amqtt.PublishOptions{
    Observer: myObserver, // RecordPublish on broker ack or error
})
```

**Prometheus example (user-side, no lib dependency):**
```go
type PrometheusObserver struct {
    requests   *prometheus.CounterVec   // labels: method, path, status
    subscribed *prometheus.CounterVec   // labels: topic, success
    published  *prometheus.CounterVec   // labels: topic, success
    valErrors  *prometheus.CounterVec   // labels: location, constraint, field
    latency    *prometheus.HistogramVec // labels: method, path
}

func (o *PrometheusObserver) RecordRequest(method, path string, code int, d time.Duration) {
    status := strconv.Itoa(code)
    o.requests.WithLabelValues(method, path, status).Inc()
    o.latency.WithLabelValues(method, path).Observe(d.Seconds())
}
func (o *PrometheusObserver) RecordSubscribe(topic string, ok bool, d time.Duration) {
    o.subscribed.WithLabelValues(topic, strconv.FormatBool(ok)).Inc()
}
func (o *PrometheusObserver) RecordPublish(topic string, ok bool, d time.Duration) {
    o.published.WithLabelValues(topic, strconv.FormatBool(ok)).Inc()
}
func (o *PrometheusObserver) RecordValidationError(loc, constraint, field string) {
    o.valErrors.WithLabelValues(loc, constraint, field).Inc()
}
```

Pass `stats.NoopObserver{}` (or omit the field — adapter defaults to it) for zero overhead when observability is not needed.

| location value | adapter / use case |
|---|---|
| `"body"` | nethttp — request body decode |
| `"query"` | nethttp — query parameter validation |
| `"payload"` | mqtt — message payload decode/encode |
| any string | codec-only: choose a label meaningful to your domain (`"config"`, `"input"`, …) |

See [`examples/stats-observer`](examples/stats-observer/main.go) for a runnable codec-only observer example (config file validation, no adapters). See [`examples/stats-observer-http`](examples/stats-observer-http/main.go) for the HTTP body + query validation demo. See [`examples/stats-observer-mqtt`](examples/stats-observer-mqtt/main.go) for the MQTT subscribe + publish observer demo.

## Special Topics

### Protobuf Integration

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

### CLI Tools

Go is a popular language for CLI tools. go-codex is well suited for **config file decoding** (YAML, TOML, JSON): define a codec once and get type-safe parsing, structured validation errors, and auto-generated JSON Schema documentation for free.

**For command-line flag and argument parsing** (the `--flag value` part), use [cobra](https://github.com/spf13/cobra), [pflag](https://github.com/spf13/pflag), or the standard `flag` package — they handle `--help`, shell completion, and usage text that codecs are not designed for.

**Where go-codex fits in a CLI:** read the config file → decode with the codec → get typed struct with all validation errors collected upfront.

```go
// Config is the application configuration struct.
type Config struct {
    Port    int
    LogLevel string
}

var configCodec = codex.Struct[Config](
    codex.RequiredField[Config, int]("port", codex.Int().Refine(validate.RangeInt(1, 65535)),
        func(c Config) int { return c.Port },
        func(c *Config, v int) { c.Port = v },
    ),
    codex.OptionalField[Config, string]("log_level",
        codex.String().Refine(validate.OneOf("debug", "info", "warn", "error")),
        func(c Config) string { return c.LogLevel },
        func(c *Config, v string) { c.LogLevel = v },
    ),
)

// In main() or cobra's PersistentPreRunE:
data, _ := os.ReadFile("config.toml")
cfg, err := format.TOML(configCodec).Unmarshal(data)
if err != nil {
    // err is a codex.ValidationErrors — all field errors collected at once.
    log.Fatal(err)
}
// cfg is fully validated and typed.
_ = cfg.Port
```

**What you get for free:**

- All field validation errors collected in one pass (not stop-at-first).
- `render/openapi` can render the codec's schema as JSON Schema for documentation or editor autocomplete.
- The same codec works with JSON, YAML, and TOML config files — swap `format.TOML` for `format.YAML` or `format.JSON` without touching the codec.

**Environment variables only (12-factor / containers):** use `format.FromEnv` — the codec's schema drives string-to-type coercion so you don't write per-field `strconv` code:

```go
// Env var names: strings.ToUpper(prefix + field_name)
// "port"      + "APP_" → APP_PORT
// "log_level" + "APP_" → APP_LOG_LEVEL
cfg, err := format.FromEnv(configCodec, "APP_")
if err != nil {
    // err is a codex.ValidationErrors — parse errors, missing required
    // fields, and constraint violations all collected in one pass.
    log.Fatal(err)
}
```

Nested struct fields expand the prefix (`db.host` → `APP_DB_HOST`). Slices use comma separation (`APP_TAGS=web,api,v2`). Complex fields also accept JSON — **no separate codec needed**: `format.FromEnv` parses the JSON string into the same intermediate `map[string]any` that `format.TOML` and `format.JSON` produce, then calls the same `codec.Decode`. All field validations run unchanged.

```sh
# Nested struct as JSON object — replaces prefix expansion (APP_DB_HOST etc.)
APP_DB='{"host":"localhost","port":5432,"name":"mydb"}'

# Slice as JSON array — replaces comma-separated
APP_TAGS='["web","api","v2"]'

# StringMap as JSON object — only supported format
APP_LABELS='{"env":"prod","team":"platform"}'
```

JSON takes precedence when the value starts with `{` or `[`. See [`examples/env-config`](examples/env-config/main.go) for a full example.

**Environment variable overrides on top of a config file:** decode the file, apply `os.LookupEnv` overrides to the struct, then call `configCodec.Validate(cfg)`. See [`examples/cli-config`](examples/cli-config/main.go).

**What go-codex does not do:** parse `os.Args`, generate `--help` output, or handle subcommands. Use cobra/flag for those.

### Schema Metadata: WithExample, WithDeprecated, DefaultField

Codecs carry their schema. Three methods annotate that schema for documentation purposes:

| Method / Constructor | Effect |
| --- | --- |
| `codec.WithExample(v any)` | Sets `example` in the generated schema |
| `codec.WithDeprecated()` | Sets `deprecated: true` in the generated schema |
| `DefaultField(name, codec, default, get, set)` | Optional field with a declared default; absent key uses the default; `default` appears in schema |

```go
var emailCodec = codex.String().
    Refine(validate.Email).
    WithDescription("Primary contact email.").
    WithExample("alice@example.com")   // → example: alice@example.com in OpenAPI

var legacyIPCodec = codex.String().
    Refine(validate.IPv4).
    WithDescription("IPv4 of last login. Deprecated: use hostname instead.").
    WithDeprecated()                   // → deprecated: true in OpenAPI

var configCodec = codex.Struct[Config](
    codex.RequiredField[Config, int]("port", ...),
    // Absent APP_LOG_LEVEL → "info" is used; default visible in generated schema
    codex.DefaultField[Config, string](
        "log_level",
        codex.String().Refine(validate.OneOf("debug", "info", "warn", "error")),
        "info",
        func(c Config) string { return c.LogLevel },
        func(c *Config, v string) { c.LogLevel = v },
    ),
)
```

The `DefaultField` constructor sets `Required: false` and propagates the default value into the schema's `default` property. Zero-value defaults are supported (the `Default *F` field uses a pointer to distinguish "no default" from `""` or `0`).

See [`examples/formats`](examples/formats/main.go) for `WithExample` and `WithDeprecated` in context, and [`examples/env-config`](examples/env-config/main.go) for `DefaultField`.

## Project Structure

```TEXT
go-codex/
├── go.mod
├── README.md

├── codex/                  # ⭐ PUBLIC API: codecs, primitives, struct, union, slice
│   ├── codec.go            # Codec[T], WithDescription, WithTitle, WithExample, WithDeprecated, Validate, New
│   ├── either.go           # Either[A,B] type, Either2 codec
│   ├── errors.go           # ValidationError, ValidationErrors, EitherError
│   ├── map.go              # MapCodecSafe, MapCodecValidated, Downcast
│   ├── must.go             # Must[T] — generic panic-on-error helper
│   ├── nullable.go         # Nullable[T]
│   ├── object.go           # Field[T,F], RequiredField, OptionalField, DefaultField, Struct[T]
│   ├── primitives.go       # Int, Int32, Int64, Uint, Uint64, Float32, Float64, String, Bool, Bytes, Any, Pure
│   ├── refine.go           # Constraint[T], Refine, RefineFunc, Eq (Constraint.Schema for schema reflection)
│   ├── slice.go            # SliceOf[T]
│   ├── stringmap.go        # StringMap[V]
│   ├── time.go             # Time(), Date(), Duration()
│   └── union.go            # TaggedUnion[T], UntaggedUnion[T], UntaggedVariant[T]
│
├── format/                 # format bridges: JSON, YAML, TOML
│   └── format.go           # Format[T], JSON(), YAML(), TOML(), New()
│
├── route/                  # HTTP route descriptors (no renderer logic)
│   └── route.go            # Route, Param, Body, Response
│
├── api/                    # API builders (no HTTP or messaging library imports)
│   ├── internal/           # shared helpers (not public API)
│   │   └── template.go     # ParseTemplateVars, BuildFromTemplate, StripTemplateVars — used by rest + events
│   ├── rest/               # REST API builder: typed Decode/Encode + OpenAPI spec
│   │   └── builder.go      # Builder, AddRoute[Req,Resp], AddServer, AddSchema, RouteHandle, BuildPath
│   └── events/             # Event channel builder: typed Decode/Encode + AsyncAPI spec
│       └── builder.go      # Builder, AddChannel[T], AddServer, AddSchema, ChannelHandle, BuildTopic
│
├── adapters/               # transport-specific adapters (wrap api/rest or api/events)
│   ├── nethttp/            # net/http adapter for api/rest RouteHandles
│   │   └── adapter.go      # Handler, Register, RequestFromContext, Options (with Observer)
│   └── mqtt/               # Paho MQTT adapter for api/events ChannelHandles
│       └── adapter.go      # SubscribeHandler, SubscribeOptions, Publish, SubscribeError, ErrorKind, MessageFromContext
│
├── render/                 # spec renderers (import schema only, or schema + route)
│   ├── internal/
│   │   └── schemarender/   # shared schema-to-map renderer (used by openapi + asyncapi)
│   │       └── schemarender.go  # SchemaObject
│   ├── openapi/            # OpenAPI 3.1 renderer
│   │   ├── openapi.go      # SchemaObject, ComponentsSchemas, MarshalJSON, MarshalYAML
│   │   └── document.go     # DocumentBuilder, Document, Info, Server — full 3.1 spec
│   └── asyncapi/           # AsyncAPI 2.6 renderer
│       ├── asyncapi.go     # delegates schema rendering to render/internal/schemarender
│       └── document.go     # DocumentBuilder, Document, ChannelItem, Operation, Message
│
├── schema/                 # schema model (pure data, zero dependencies)
│   └── schema.go           # Schema, Property, DiscriminatorSchema
│
├── validate/               # reusable constraints (reflect into schema automatically)
│   ├── bytes.go            # MaxBytes(n), MinBytes(n)
│   ├── duration.go         # PositiveDuration, NonNegativeDuration, MinDuration, MaxDuration
│   ├── float.go            # PositiveFloat, NegativeFloat, NonZeroFloat, MinFloat, MaxFloat, RangeFloat
│   ├── format.go           # Email, UUID, URL, URLWithSchemes, URI, Hostname, IPv4, IPv6, IP, Date, Time, DateTime, SemVer, Slug, CIDR
│   ├── int.go              # PositiveInt, NegativeInt, NonZeroInt, MinInt, MaxInt, RangeInt; int32 + int64 variants
│   ├── uint.go             # PositiveUint, MinUint, MaxUint, RangeUint; uint64 variants
│   └── string.go           # NonEmptyString, MinLen, MaxLen, Pattern, OneOf, HTTPPath, MQTTTopic, MQTTPublishTopic, IntString, PositiveIntString, NonNegativeIntString, IntStringInRange
│
├── stats/                  # dependency-free metrics observer interface
│   └── observer.go         # Observer interface, NoopObserver
│
└── examples/               # usage demonstrations — not importable
    ├── adapters-mqtt/      # Paho MQTT adapter: wiring api/events to Paho client
    ├── adapters-nethttp/   # net/http adapter: wiring api/rest to ServeMux
    ├── api-events/         # Event channel builder: typed helpers + AsyncAPI spec
    ├── api-rest/           # REST API builder: typed helpers + OpenAPI spec
    ├── decode-errors/      # multi-field ValidationErrors + errors.As demo
    ├── event-driven/       # full AsyncAPI 2.6 document from channel descriptors
    ├── formats/            # builtin format constraints demo (Email, UUID, URL, ...)
    ├── html-sanitize/      # sanitizing untrusted HTML input with a codec
    ├── multiformat/        # JSON / YAML / TOML with one codec
    ├── openapi/            # OpenAPI components/schemas generation from a Codec
    ├── order/              # nested structs, SliceOf, Time, Nullable, StringMap demo
    ├── rest-api/           # full OpenAPI 3.1 document from route descriptors
    ├── shape/              # tagged union + Downcast demo
    ├── stats-observer/          # stats.ValidationObserver with codecs directly: config validation, no adapter
    ├── stats-observer-http/     # stats.Observer wired to nethttp: request counts, latency, validation errors (body + query)
    ├── stats-observer-mqtt/     # stats.Observer wired to mqtt subscribe + publish: message counts, latency, payload errors
    ├── templ-mapper/       # mapping codec-validated data to templ components
    ├── validate/           # explicit Validate before marshal
    ├── codec-mapping/      # shared field codecs, sub-codec reuse, MapCodecSafe, MapCodecValidated
    └── construction/       # New + Must: construction-time validation demo
```
