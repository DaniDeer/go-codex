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
| `codex.HexColor()` | `codex.Color` | hex string (`#RGB`/`#RRGGBB`/`#RGBA`/`#RRGGBBAA`) | `{type:string,pattern:...}` |
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
| `codex.EntrySlice(keyCodec, valCodec, merge, split)` | object → `[]R`, key merged into element | `EntrySlice(containerKeyCodec, moduleCodec, merge, split)` |
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

### Rejecting unknown keys — `StrictStruct`

`Struct` is forward-compatible by default: any input key not matching a declared field is
silently ignored on decode, and the generated schema leaves `additionalProperties` unset (JSON
Schema default: extra properties allowed). Use `codex.StrictStruct` — same signature as
`Struct`, same `RequiredField`/`OptionalField`/`DefaultField` vocabulary — to reject unknown
keys instead (catches typo'd field names):

```go
var StrictUserCodec = codex.StrictStruct[User](
    codex.RequiredField("name", nameCodec, get, set),
    codex.OptionalField("bio",  codex.String(), get, set),
)

_, err := StrictUserCodec.Decode(map[string]any{"name": "Alice", "boi": "typo"})
// err: field "boi": unknown field (codex.ErrUnknownField) — errors.Is-navigable
```

- Sets `Schema.AdditionalProperties = false` (JSON Schema `additionalProperties: false`) —
  `Struct` never sets this field at all.
- Unknown-key errors are collected ALONGSIDE normal per-field errors (missing required fields,
  constraint failures) in one `ValidationErrors` — a request with both a missing required
  field and a typo'd key reports both, not just one.
- **Not viral/recursive across nesting** — a nested field declared with plain `Struct` stays
  non-strict even when the OUTER struct uses `StrictStruct`. Opt in explicitly at each nesting
  level, exactly like `Required`/`Optional`/`Default` are declared independently per level (see
  [Nested structs](#nested-structs)).
- `Encode` is unchanged from `Struct` — "unknown field" is a decode-only (external input)
  concept.

### Reusing Field declarations for path/topic/header/query vars

This is the mechanism behind a general library design principle: **one
struct, one call** (see [API Contracts — Design principle](api-contracts.md#design-principle-one-struct-one-call)) —
a caller on either side of a boundary should be able to do an entire
encode-or-decode direction with one struct value and one call, not manual
map assembly. `RequiredField`/`OptionalField`/`DefaultField` aren't just for
JSON object keys — the same declarations work for any string-keyed source:
HTTP path/query/header/cookie parameters, MQTT/event topic variables, and
file path segments. `codex.DecodeVars`/`EncodeVars` decode/encode a
`map[string]string` (instead of a JSON `map[string]any`) using those exact
same `Field` declarations — no new declaration API to learn:

```go
var idField = codex.RequiredField("id", codex.String().Refine(validate.UUID),
    func(r GetUserReq) string { return r.ID },
    func(r *GetUserReq, v string) { r.ID = v })

// Decode: partial merge into an existing struct (only "id" is touched).
var req GetUserReq
err := codex.DecodeVars(&req, map[string]string{"id": r.PathValue("id")}, idField)

// Encode: extract field values into a vars map (replaces hand-written
// varsFor func(T) map[string]string closures used by adapter constructors).
vars, err := codex.EncodeVars(req, idField)
```

A field's codec must accept a string on `Decode` — plain `codex.String()`
for string fields, or `codex.MapCodecSafe(codex.String()..., parse, format)`
for typed fields like `int`/`time.Time` (vars are always string-valued at
the wire level: path segments, topic segments, header/query/cookie values).

**Per-boundary sugar**: `rest.NewPathParam[T]`/`NewRequiredQueryParam[T]`/
`NewOptionalQueryParam[T]` (+ Header/Cookie equivalents) and
`ports.NewFilePathParam[T]` declare BOTH the boundary's spec Param (for
OpenAPI/AsyncAPI generation, unchanged) AND a merge field in ONE call — see
[Ports feature — File](../features/ports.md) and
[REST API feature](../features/rest-api.md) for the full per-boundary
API. The plain `PathParam`/`QueryParam`/`FilePathParam` struct literals
remain available as the low-level, validate-only alternative.

A struct can mix BOTH sources at once — some fields decoded from the body,
others merged from vars — as long as the body codec and the merge fields
declare different field names (`RouteHandle.DecodeMerged` decodes the body
first, then merges vars into the same value, touching only the declared
merge fields). See [REST API — Mixing body fields and merged params](../features/rest-api.md#mixing-body-fields-and-merged-params-on-one-struct).

The SAME mechanism applies in the RESPONSE direction —
`rest.NewRequiredResponseHeaderParam[Resp]`/`NewOptionalResponseHeaderParam[Resp]`
(+ Cookie equivalents) declare a response header/cookie merge field on
`Resp`: the server sets it automatically from the returned struct's field,
and the client merges the HTTP response back into the same field
automatically. On top of the four merge-field roles (path/query/header/cookie),
`nethttp.CallHandle` is the single-call client convenience that derives
every request-side map from ONE struct automatically — this is the
concrete "one struct, one call" experience end to end. See
[REST API — Response merge fields](../features/rest-api.md#response-merge-fields)
and [REST API — One-line client calls](../features/rest-api.md#one-line-client-calls-callhandle).

This also means the pattern is neither JSON-specific nor
flat-struct-specific: `get`/`set` on `RequiredField`/`OptionalField` (and
every REST merge-field constructor built on them) are plain Go closures,
not reflection over a struct's direct fields — a closure reaching into a
NESTED sub-struct (`func(r Req) string { return r.Meta.X }`) works exactly
like a top-level field, with zero framework changes. And since body
decode/encode is completely orthogonal to var-merge, ANY `format.Format[T]`
— JSON, YAML, TOML, `format.Gob`, `format.Binary`, or a custom
`format.NewTyped` — composes with merge fields unchanged. See
[REST API — Nested structs & binary body formats](../features/rest-api.md#nested-structs-binary-body-formats)
and `examples/rest-nested-binary` for the full runnable version.

## Constraints with Refine

```go
var EmailCodec = codex.String().Refine(validate.Email)
// Schema gains: format: email
// Decode rejects invalid emails at runtime
// Encode validates before serialising
```

Constraints run symmetrically — on both Encode and Decode — ensuring the codec is the single source of truth for validity.

### Stacking and combining constraints

`.Refine(...)` is variadic and chainable. Every call wraps the previous codec, building a validation chain:

```go
// All three are equivalent — same chain, same execution order:
codex.String().Refine(A, B, C)
codex.String().Refine(A).Refine(B).Refine(C)
codex.String().Refine(A).Refine(B, C)
```

Constraints run **in declaration order**. The first failure stops the chain — later constraints do not run.

**Mix built-in and custom constraints freely:**

```go
var containerNameCodec = codex.String().
    Refine(validate.NonEmptyString).           // built-in: non-empty
    Refine(validate.MaxLen(63)).               // built-in: DNS label max length
    Refine(codex.Constraint[string]{           // custom: no spaces/underscores/slashes
        Name:  "container-name",
        Check: func(v string) bool { return !strings.ContainsAny(v, " _/") },
        Message: func(v string) string {
            return fmt.Sprintf("container name %q must not contain spaces, underscores, or slashes", v)
        },
    }).
    Refine(codex.Constraint[string]{           // custom: must start with a letter
        Name:  "starts-with-letter",
        Check: func(v string) bool { return len(v) > 0 && unicode.IsLetter(rune(v[0])) },
        Message: func(v string) string {
            return fmt.Sprintf("container name %q must start with a letter", v)
        },
    })
```

**Order matters:** put cheaper constraints first (length, empty check) so they short-circuit before expensive ones (regex, external lookup).

### Schema annotation from custom constraints

Set the optional `Constraint.Schema` function to propagate constraint metadata into the generated OpenAPI/AsyncAPI schema:

```go
var slugCodec = codex.String().
    Refine(validate.NonEmptyString).
    Refine(codex.Constraint[string]{
        Name:  "slug",
        Check: func(v string) bool { return slugPattern.MatchString(v) },
        Message: func(v string) string {
            return fmt.Sprintf("%q is not a valid slug (lowercase letters, digits, hyphens only)", v)
        },
        Schema: func(s schema.Schema) schema.Schema {
            s.Pattern = `^[a-z0-9-]+$`   // emitted in the OpenAPI/AsyncAPI spec
            return s
        },
    })
// Schema: {type: string, minLength: 1, pattern: "^[a-z0-9-]+$"}
```

Each `Constraint.Schema` function receives the schema produced by all previous constraints and returns the augmented version — they compose without interfering.

Built-in constraints in the `validate` package already set `Schema` where appropriate (e.g. `validate.Email` sets `format: email`, `validate.RangeInt` sets `minimum`/`maximum`). Custom constraints without a `Schema` function simply add runtime validation without changing the spec.

### Whole-struct (cross-field) constraints

`.Refine(...)` isn't limited to scalar codecs — `codex.Struct[T](...)` returns a plain `Codec[T]`, so the same mechanism validates INVARIANTS THAT SPAN MULTIPLE FIELDS, something no single per-field `Constraint` can see:

```go
type DateRange struct {
    Start, End time.Time
}

var dateRangeCodec = codex.Struct[DateRange](
    codex.RequiredField("start", codex.Time(), func(d DateRange) time.Time { return d.Start }, func(d *DateRange, v time.Time) { d.Start = v }),
    codex.RequiredField("end",   codex.Time(), func(d DateRange) time.Time { return d.End },   func(d *DateRange, v time.Time) { d.End = v }),
).Refine(codex.Constraint[DateRange]{
    Name:  "start-before-end",
    Check: func(d DateRange) bool { return d.Start.Before(d.End) },
    Message: func(d DateRange) string {
        return fmt.Sprintf("start (%s) must be before end (%s)", d.Start, d.End)
    },
})
```

- The whole-struct `Constraint[DateRange]`'s `Check` receives the FULLY DECODED `DateRange` — it
  runs AFTER every per-field `Required`/`Codec`/nested-struct check has already succeeded, so a
  missing/invalid individual field is reported first (per-field `ValidationErrors`), and the
  cross-field constraint only runs once the struct is otherwise well-formed.
- Symmetric on Encode too — an in-memory `DateRange{Start: later, End: earlier}` fails to
  encode, exactly like any other `Refine`d codec.
- Use this for "at least one of A or B must be set", "field X only valid when field Y is a
  certain value", range checks across two fields, and similar invariants that a single
  `RequiredField`/`OptionalField` declaration cannot express.

## Composition — shared field codecs

Define field codecs once and reuse across struct codecs:

```go
var emailFieldCodec = codex.String().Refine(validate.Email).WithDescription("Email address.")

var UserCodec    = codex.Struct[User](   codex.RequiredField("email", emailFieldCodec, ...), ...)
var ProfileCodec = codex.Struct[Profile](codex.RequiredField("email", emailFieldCodec, ...), ...)
// Both carry the same constraint and description — no duplication.
```

### Field factory functions — reusing field groups across structs

When the same field (same name, same codec, same validation rules) appears in multiple structs, combine shared field codecs with a **field factory function**. A factory function bakes in the field name and codec rules and accepts only the getter/setter — the one thing that is unavoidably struct-specific.

```go
import "time"

// ── Shared field codecs ───────────────────────────────────────────────────────

var idCodec = codex.String().Refine(validate.UUID).
    WithDescription("Unique identifier (UUID v4).")

var createdAtCodec = codex.Time().
    WithDescription("Creation timestamp (RFC 3339).")

// ── Field factory functions ───────────────────────────────────────────────────
// The type parameter T makes each factory work for any struct that has the
// appropriate field, without repeating the field name or validation rules.

func IDField[T any](get func(T) string, set func(*T, string)) codex.Field[T, string] {
    return codex.RequiredField("id", idCodec, get, set)
}

func CreatedAtField[T any](get func(T) time.Time, set func(*T, time.Time)) codex.Field[T, time.Time] {
    return codex.RequiredField("created_at", createdAtCodec, get, set)
}

// ── Structs using Go embedding for DRY field access ──────────────────────────

type AuditBase struct {
    ID        string
    CreatedAt time.Time
}

type User struct {
    AuditBase               // embedded — promotes ID and CreatedAt
    Name  string
    Email string
}

type Device struct {
    AuditBase               // same embedding
    Hostname string
    IPAddr   string
}

// ── Codecs — each struct provides only its own getters/setters ───────────────

var UserCodec = codex.Struct[User](
    IDField(
        func(u User) string  { return u.ID },
        func(u *User, v string) { u.ID = v },
    ),
    CreatedAtField(
        func(u User) time.Time  { return u.CreatedAt },
        func(u *User, v time.Time) { u.CreatedAt = v },
    ),
    codex.RequiredField("name",
        codex.String().Refine(validate.NonEmptyString),
        func(u User) string { return u.Name },
        func(u *User, v string) { u.Name = v },
    ),
    codex.RequiredField("email",
        codex.String().Refine(validate.Email),
        func(u User) string { return u.Email },
        func(u *User, v string) { u.Email = v },
    ),
)

var DeviceCodec = codex.Struct[Device](
    IDField(
        func(d Device) string  { return d.ID },
        func(d *Device, v string) { d.ID = v },
    ),
    CreatedAtField(
        func(d Device) time.Time  { return d.CreatedAt },
        func(d *Device, v time.Time) { d.CreatedAt = v },
    ),
    codex.RequiredField("hostname",
        codex.String().Refine(validate.Hostname),
        func(d Device) string { return d.Hostname },
        func(d *Device, v string) { d.Hostname = v },
    ),
    // ...
)
```

**What is shared:** the field name (`"id"`), the codec (`idCodec`), and all its constraints and schema metadata. A change to `idCodec` propagates to every struct that uses `IDField` — one declaration, zero drift.

**What is not shared:** the getter/setter functions — they are unavoidably struct-specific because Go's type system cannot derive `func(Device) string` from `func(User) string` automatically.

**Why not a library feature:** Go does not have inheritance, and reflecting on struct fields to auto-derive getters/setters would violate the no-reflection, no-struct-tags design principle. Field factory functions achieve the same result with full type safety and zero magic.

## Nested structs

A field codec can be any `Codec[F]` — including another `Struct[...]` codec. Nesting is unlimited and composes with `SliceOf`, `Nullable`, and `StringMap`:

```go
type Address struct { Street, City, Country string }
type Customer struct { Name, Email string }
type LineItem struct { Product string; Quantity int; Price float64 }
type Order struct {
    ID             string
    Customer       Customer
    Shipping       Address
    BillingAddress Address // same Address type/codec as Shipping — see below
    Items          []LineItem
    Tags           map[string]string
    Note           *string
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
    codex.RequiredField("customer", customerCodec,              ...),  // nested Struct, required
    codex.RequiredField("shipping", addressCodec,               ...),  // nested Struct, required
    codex.OptionalField("billingAddress", addressCodec,         ...),  // SAME nested Struct, optional
    codex.RequiredField("items",    codex.SliceOf(lineItemCodec).Refine(validate.NonEmptySlice[LineItem](), validate.MaxItems[LineItem](20)), ...), // slice of structs, 1-20 items
    codex.OptionalField("tags",     codex.StringMap(codex.String()), ...), // map
    codex.OptionalField("note",     codex.Nullable(codex.String()),  ...), // optional
)
```

**Required vs optional is orthogonal to whether a field's codec is a nested `Struct` or a scalar.** `RequiredField`/`OptionalField`/`DefaultField` only decide PRESENCE — they never inspect what codec they wrap. `shipping` and `billingAddress` above use the exact same `addressCodec`; only `RequiredField` vs `OptionalField` differs:

- **Absent + Required** → decode fails with `ErrMissingField` for that field name.
- **Absent + Optional** → the nested field is silently left at its Go zero value (`Address{}`) — decode succeeds.
- **Present** (either case) → decodes through the nested struct's OWN codec, which enforces its OWN required/optional/default rules completely independently. A present-but-incomplete `billingAddress` (e.g. missing `country`) still fails — declaring the OUTER field Optional never weakens the INNER struct's own validation, it only controls whether the key itself must exist.

**What you get for free:**
- Encode/decode recurses automatically — `order.Customer` is a Go struct, the JSON `"customer"` is an object.
- Validation cascades — a constraint failure on `customer.email` surfaces as a nested `ValidationErrors`: the top-level error names `"customer"`, and unwrapping it one level (`errors.As` into `codex.ValidationErrors`) yields the inner error naming `"email"` (message reads `field customer: field email: ...` — nested attribution, not a flattened dotted-path string).
- Schema generation — `OrderCodec.Schema` produces a nested `$object` with inline `Customer`/`Address` schemas, and each nesting level's `Required` array is independent: `billingAddress` is absent from the OUTER `Required` list, but its own embedded schema still declares `["street","city","country"]` as required.
- Encoding a never-populated `Optional` nested struct still emits the key with its zero value — codex has no "omit empty" semantics; `Required`/`Optional`/`Default` only affect DECODE and the generated schema's `Required` array, never what ENCODE writes.

See [`examples/order`](https://github.com/DaniDeer/go-codex/tree/main/examples/order) for a complete runnable demo with all five nesting patterns, including both a required (`shipping`) and an optional (`billingAddress`) nested struct built from the identical codec.

## Slices — array-level constraints

`codex.SliceOf(elem)` returns a plain `Codec[[]T]`, so it composes with `.Refine(...)` exactly
like any scalar codec — required/optional at the FIELD level is orthogonal to constraints on
the array's LENGTH or CONTENTS:

```go
var itemsCodec = codex.SliceOf(lineItemCodec).
    Refine(validate.NonEmptySlice[LineItem]()).  // at least 1 element
    Refine(validate.MaxItems[LineItem](20))      // at most 20 elements
// Schema: {type: array, items: {...}, minItems: 1, maxItems: 20}
```

Built-in `validate` constructors for slices (all generic, in `validate/slice.go`):

| Constructor | Enforces | Schema keyword |
|---|---|---|
| `validate.MinItems[T](n)` | at least `n` elements | `minItems` |
| `validate.MaxItems[T](n)` | at most `n` elements | `maxItems` |
| `validate.NonEmptySlice[T]()` | at least 1 element (equivalent to `MinItems[T](1)`) | `minItems: 1` |
| `validate.UniqueItems[T comparable]()` | no duplicate elements (Go `==` equality, `map[T]struct{}` dedup) | `uniqueItems` |

`UniqueItems` requires `T comparable` — narrower than the other three's `any` — since it needs
Go equality to detect duplicates. For element types containing slices/maps/funcs (not
comparable), write a custom `codex.Constraint[[]T]` using `reflect.DeepEqual` or a
domain-specific key extractor instead.

These constructors are functions, not package-level `var`s like `validate.NonEmptyString` —
Go has no generic package-level variables, so the element type must be supplied at the call
site (`validate.MinItems[LineItem](1)`, not `validate.MinItems(1)`).

Array-level required/optional (whether the KEY itself must be present) is controlled the same
way as any other field — `RequiredField`/`OptionalField`/`DefaultField` wrapping a `SliceOf(...)`
codec, exactly as shown for `items` above. Absent + Optional leaves the field at its Go zero
value (`nil` slice); absent + Required fails with `ErrMissingField`; present (either case) runs
the length/uniqueness constraints declared via `.Refine(...)`.

## Maps — size constraints

`codex.Map[K,V]`/`codex.StringMap[V]` return plain `Codec[map[K]V]` codecs, so entry-count
constraints compose with `.Refine(...)` exactly like slice-length constraints:

```go
var tagsCodec = codex.StringMap(codex.String()).
    Refine(validate.MaxProperties[string, string](5))  // at most 5 entries
// Schema: {type: object, additionalProperties: {...}, maxProperties: 5}
```

Built-in `validate` constructors for maps (all generic, in `validate/map.go`):

| Constructor | Enforces | Schema keyword |
|---|---|---|
| `validate.MinProperties[K, V](n)` | at least `n` entries | `minProperties` |
| `validate.MaxProperties[K, V](n)` | at most `n` entries | `maxProperties` |
| `validate.NonEmptyMap[K, V]()` | at least 1 entry (equivalent to `MinProperties[K,V](1)`) | `minProperties: 1` |

These mirror `validate/slice.go`'s array constructors exactly, one level up (entries instead
of elements). Map KEY constraints (pattern, format, enum, length) are a SEPARATE, already-fully
-supported concern: pass a `.Refine(...)`-composed `Codec[K]` as `Map`'s `keyCodec` argument —
it renders into the schema's `propertyNames` and is validated per-key on every decode. Do not
confuse the two: `MinProperties`/`MaxProperties` constrain how MANY entries the map has;
key-codec constraints constrain what EACH key looks like.

`EntrySlice[K,V,R]` decodes to `Codec[[]R]` (a slice), not `Codec[map[K]V]` — its entry-count
constraint is therefore `validate.MinItems[R]`/`MaxItems[R]`/`NonEmptySlice[R]` (the SLICE
constructors), not the map ones above, even though `EntrySlice`'s generated schema is
object-shaped (`type: object`, matching its wire format). This is a known, harmless
schema/type mismatch: a JSON Schema validator ignores `minItems`/`maxItems` on a
`type: object` schema (those keywords only apply where `type: array`), so the RUNTIME
constraint still enforces correctly via `len()`, but the constraint won't appear in the
rendered OpenAPI/AsyncAPI spec for that field. If the spec annotation matters for an
`EntrySlice` field, use a custom `codex.Constraint[[]R]` with a `Schema` callback that sets
`MinProperties`/`MaxProperties` directly instead.

## Merging key and value into one type (`EntrySlice`)

`EntrySlice[K, V, R]` handles the case where the **object key itself carries domain meaning** that belongs inside the value struct. It decodes a JSON/YAML/TOML object into `[]R`, merging the decoded key and value into each element.

**Use case:** Azure IoT Edge device twins, Kubernetes ConfigMaps, and similar formats use dotted keys like `"properties.desired.modules.cv-writer-kvrocks"` where the last segment is the container name. With `EntrySlice`, this decodes directly into `[]Container` — no post-processing loop needed.

### Recommended key codec: `MapCodecValidated` (Option B)

Put the prefix logic in the key codec, not in `merge`/`split`. This way:
- The key codec validates the full wire key **and** the extracted container name separately
- `merge` receives only the already-extracted name — no `strings.TrimPrefix` needed
- `split` returns only the name — the codec re-adds the prefix on encode

```go
const prefix = "properties.desired.modules."

var containerNameConstraint = codex.Constraint[string]{
    Name:  "container-name",
    Check: func(v string) bool { return len(v) > 0 && !strings.ContainsAny(v, " /_") },
}
var moduleKeyConstraint = codex.Constraint[string]{
    Name:  "module-key-path",
    Check: func(v string) bool { return strings.HasPrefix(v, prefix) && len(v) > len(prefix) },
}

// containerKeyCodec: wire validates full dotted key; domain validates container name.
var containerKeyCodec = codex.MapCodecValidated(
    codex.String().Refine(moduleKeyConstraint),      // wire: "properties.desired.modules.cv-writer"
    codex.String().Refine(containerNameConstraint),   // domain: "cv-writer"
    func(fullKey string) (string, error) {            // decode: strip prefix
        return strings.TrimPrefix(fullKey, prefix), nil
    },
    func(name string) (string, error) {               // encode: add prefix
        return prefix + name, nil
    },
)

var containersCodec = codex.EntrySlice(
    containerKeyCodec,   // K = string (container name)
    moduleCodec,         // V = ModuleConfig
    func(name string, m ModuleConfig) Container {     // merge: name already extracted
        return Container{Name: name, Image: m.Image, Status: m.Status}
    },
    func(c Container) (string, ModuleConfig) {        // split: returns container name
        return c.Name, ModuleConfig{Image: c.Image, Status: c.Status}
    },
)
// → Codec[[]Container]
```

### Decode path — multiple containers

Given the wire object:

```json
{
  "properties.desired.modules.cv-writer-kvrocks": {"image": "myregistry/cv-writer:1.0", "status": "running"},
  "properties.desired.modules.cv-writer-gateway": {"image": "myregistry/gateway:3.1",  "status": "running"},
  "properties.desired.modules.analytics-engine":  {"image": "myregistry/analytics:2.1", "status": "stopped"}
}
```

`containersCodec.Decode(rawJSON)` iterates every key-value pair:

| Wire key | After `containerKeyCodec.Decode` | After `moduleCodec.Decode` | After `merge` |
|---|---|---|---|
| `"...cv-writer-kvrocks"` | `"cv-writer-kvrocks"` | `ModuleConfig{Image:"...", Status:"running"}` | `Container{Name:"cv-writer-kvrocks", Image:"...", Status:"running"}` |
| `"...cv-writer-gateway"` | `"cv-writer-gateway"` | `ModuleConfig{Image:"...", Status:"running"}` | `Container{Name:"cv-writer-gateway", ...}` |
| `"...analytics-engine"` | `"analytics-engine"` | `ModuleConfig{Image:"...", Status:"stopped"}` | `Container{Name:"analytics-engine", ...}` |

Result: `[]Container` with 3 elements. **Sort after decode** if order matters — JSON object key order is not guaranteed.

### Encode path — multiple containers

`containersCodec.Encode(containers)` processes each element:

1. `split(c)` → `(c.Name, ModuleConfig{c.Image, c.Status})`
2. `containerKeyCodec.Encode(c.Name)` → validates name → `"properties.desired.modules." + c.Name`
3. `moduleCodec.Encode(ModuleConfig{...})` → `{"image":"...","status":"..."}`
4. Writes one key-value pair into the output map

Result: the same flat dotted-key object as the input. All three containers are present. Key validation happens for every element — a single invalid name (e.g. one with an underscore) stops encoding and returns `KeyError{Key: "bad_name", Err: ConstraintError{Name:"container-name",...}}`.

### Format compatibility

| Format | Flat dotted key | Notes |
|---|---|---|
| JSON | ✅ | Always flat string keys |
| YAML (quoted) | ✅ | `"properties.desired.modules.cv-writer":` → flat key |
| TOML (quoted headers) | ✅ | `["properties.desired.modules.cv-writer"]` → flat key |
| TOML (bare dotted) | ⚠️ | `[properties.desired.modules]` → **nested** per TOML spec — use quoted headers |

### Extracting multiple fields from a key

K doesn't have to be `string`. Any `comparable` Go type works — including a struct. Use a `Struct[ModuleKey]` domain codec with `MapCodecValidated` to parse and validate each segment independently.

**Key format:** `"properties.desired.modules.<tenant>.<container-name>"`  
Two segments → `ModuleKey{Tenant, Name}` — extracted in one key codec, no parsing in `merge`/`split`.

```go
type ModuleKey struct {
    Tenant string // "tenant-acme"
    Name   string // "cv-writer-kvrocks"
}

type TenantContainer struct {
    Tenant string
    Name   string
    Image  string
    Status string
}

// Domain codec validates each field independently.
var moduleKeyStructCodec = codex.Struct[ModuleKey](
    codex.RequiredField("tenant", codex.String().Refine(tenantConstraint),
        func(k ModuleKey) string { return k.Tenant },
        func(k *ModuleKey, v string) { k.Tenant = v },
    ),
    codex.RequiredField("name", codex.String().Refine(containerNameConstraint),
        func(k ModuleKey) string { return k.Name },
        func(k *ModuleKey, v string) { k.Name = v },
    ),
)

// Two-layer key codec:
//   wire  — validates the full dotted key
//   domain — validates each extracted segment via moduleKeyStructCodec
var twoPartKeyCodec = codex.MapCodecValidated(
    codex.String().Refine(twoPartKeyConstraint),  // "properties.desired.modules.t.n" ✓
    moduleKeyStructCodec,                          // {Tenant:"t", Name:"n"} ✓
    func(fullKey string) (ModuleKey, error) {      // decode: strip prefix + split
        rest := strings.TrimPrefix(fullKey, prefix)
        parts := strings.SplitN(rest, ".", 2)
        return ModuleKey{Tenant: parts[0], Name: parts[1]}, nil
    },
    func(k ModuleKey) (string, error) {            // encode: reassemble
        return prefix + k.Tenant + "." + k.Name, nil
    },
)

// K = ModuleKey (comparable struct — all fields are strings)
var tenantContainersCodec = codex.EntrySlice(
    twoPartKeyCodec,
    moduleCodec,
    func(k ModuleKey, m ModuleConfig) TenantContainer {
        return TenantContainer{Tenant: k.Tenant, Name: k.Name, Image: m.Image, Status: m.Status}
    },
    func(c TenantContainer) (ModuleKey, ModuleConfig) {
        return ModuleKey{Tenant: c.Tenant, Name: c.Name},
               ModuleConfig{Image: c.Image, Status: c.Status}
    },
)
// → Codec[[]TenantContainer]
```

**Decode table — wire to Go:**

| Wire key | `twoPartKeyCodec.Decode` | After `merge` |
|---|---|---|
| `"...tenant-acme.cv-writer-kvrocks"` | `ModuleKey{Tenant:"tenant-acme", Name:"cv-writer-kvrocks"}` | `TenantContainer{Tenant:"tenant-acme", Name:"cv-writer-kvrocks", ...}` |
| `"...tenant-acme.cv-writer-gateway"` | `ModuleKey{Tenant:"tenant-acme", Name:"cv-writer-gateway"}` | `TenantContainer{...}` |
| `"...tenant-beta.analytics-engine"` | `ModuleKey{Tenant:"tenant-beta", Name:"analytics-engine"}` | `TenantContainer{...}` |

**Field-level validation on encode:** `TenantContainer{Tenant: "tenant_acme", ...}` (underscore in tenant) → `moduleKeyStructCodec.Validate` → `ValidationErrors[{Field:"tenant", Err:ConstraintError{Name:"tenant-name"}}]` → wrapped in `KeyError{Key:"{tenant_acme ...}", Err:...}`. The error identifies the failing *field*, not just "the key is invalid".

**General rule:** for N segments in a key, use `K = struct` with N fields. Go structs are comparable when all fields are comparable types (strings, ints, etc.).

See [`examples/flat-key-patch` Section 10](https://github.com/DaniDeer/go-codex/tree/main/examples/flat-key-patch) for the full runnable demo.

### Static key — constant name injected via `MapCodecSafe`

When the key is known at compile time (it never varies), the container name is **not on the wire** — it only exists as the field name literal in the codec. There is nothing to decode. Inject it as a constant.

**Wire format** (the key is always exactly this):
```json
{
  "properties.desired.modules.cv-writer-kvrocks": {"image": "myregistry/cv-writer:1.0", "status": "running"}
}
```

**Goal:** `Container{Name: "cv-writer-kvrocks", Image: "...", Status: "running"}` — name comes from the key, which we know statically.

**Pattern:** wrap the value codec with `MapCodecSafe` to inject the constant name during decode and strip it on encode (name is not serialized into the value object):

```go
const cvWriterKeyName = "cv-writer-kvrocks"
const cvWriterKey     = "properties.desired.modules." + cvWriterKeyName

// valueWithNameCodec: decodes ModuleConfig → Container{Name: constant, ...}
//                     encodes Container     → ModuleConfig (drops Name — not on wire)
var valueWithNameCodec = codex.MapCodecSafe(
    moduleCodec,
    func(m ModuleConfig) Container {          // decode: inject constant
        return Container{Name: cvWriterKeyName, Image: m.Image, Status: m.Status}
    },
    func(c Container) (ModuleConfig, error) { // encode: drop name
        return ModuleConfig{Image: c.Image, Status: c.Status}, nil
    },
)

// Outer codec: the full dotted key is the field name. Dots are valid field names.
var cvWriterCodec = codex.Struct[Container](
    codex.RequiredField(cvWriterKey, valueWithNameCodec,
        func(c Container) Container { return c },
        func(outer *Container, inner Container) { *outer = inner },
    ),
)
// Decode: {"properties.desired.modules.cv-writer-kvrocks": {"image":"...","status":"running"}}
// Result: Container{Name:"cv-writer-kvrocks", Image:"...", Status:"running"}
//
// Encode: Container{Name:"cv-writer-kvrocks", Image:"...", Status:"running"}
// Result: {"properties.desired.modules.cv-writer-kvrocks": {"image":"...","status":"running"}}
//          ↑ Name is NOT in the value object — only the key encodes it
```

**When to use which pattern:**

| Scenario | Key known at | Name on wire? | Use |
|---|---|---|---|
| Always the same container | Compile time | No | `codex.Struct` + `MapCodecSafe` to inject constant |
| Any container name (1 entry) | Runtime | In key | `EntrySlice` + `MapCodecValidated` to unwrap `[]R` |
| Multiple containers | Runtime | In keys | `EntrySlice` directly → `Codec[[]Container]` |
| Multi-tenant containers | Runtime | In key (2 segments) | `EntrySlice` + `MapCodecValidated` key codec |

See [`examples/flat-key-patch` Section 11](https://github.com/DaniDeer/go-codex/tree/main/examples/flat-key-patch) for the full runnable demo.

### Schema generation and OpenAPI / AsyncAPI embedding

`EntrySlice` emits the **same schema as `Map[K, V]`** — an object schema. The Go type is `[]R` (a slice), but the wire format is a JSON object, so the schema correctly documents what is on the wire:

```yaml
type: object
propertyNames:           # key codec schema — validates each JSON key
  type: string
  title: ContainerName   # set via .WithTitle() on the key codec
  minLength: 1
additionalProperties:    # value codec schema — validates each JSON value
  type: object
  properties:
    image:
      type: string
      minLength: 1
    status:
      type: string
      enum: [running, stopped]
  required: [image, status]
```

**It embeds transparently** into OpenAPI and AsyncAPI — use it anywhere you'd use any other `Codec[T]`:

```go
// OpenAPI — as a response body
rest.NewRoute[GetReq, []Container]("GET", "/twins/{id}/modules",
    reqCodec,
    containersCodec,    // Codec[[]Container] — emits object schema in spec
    rest.RouteMeta{OperationID: "listModules"},
)

// AsyncAPI — as a channel payload
events.NewChannel[[]Container]("twins/{id}/modules", containersCodec,
    events.Subscribe{Summary: "Module container list from device twin."},
)
```

Both produce the same `type: object` schema in their respective specs.

**`propertyNames.title` documents the key role.** Add `.WithTitle(...)` on the domain string codec in your key codec to give the key a readable name in the schema:

```go
codex.String().Refine(validate.NonEmptyString).
    WithTitle("ContainerName").
    WithDescription("Container name — appears as the JSON object key, not a value field.")
```

**What does NOT appear in the schema:** the `Name` field of `Container` — because `name` is not in `moduleCodec`. The schema documents the wire format (key = container name, value = `{image, status}`), not the Go struct. This is correct: on the wire, `name` IS the key.

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

### Named per-field constructors for struct codecs

`Codec[T].New` is the one smart-constructor primitive — go-codex does not
(and will not) auto-derive a `NewUser(field1, field2, ...)` constructor from
a `codex.Struct[T]` codec. Two hard constraints rule that out: go-codex has
no reflection and no struct-tag codec wiring (all field wiring is explicit
Go code via `Field`/`RequiredField`/`OptionalField`/`DefaultField`), and Go
generics cannot express a variadic-arity type parameter list, so one generic
helper can't cover struct codecs of every arity.

The idiomatic pattern is a thin, hand-written wrapper that takes positional
field values and delegates to `Codec.New` — the full codec validation
(every field's `Refine` constraints) still runs on every call:

```go
var UserCodec = codex.Struct[User](
    codex.RequiredField("name", nameCodec,
        func(u User) string { return u.Name },
        func(u *User, v string) { u.Name = v }),
    codex.RequiredField("age", ageCodec,
        func(u User) int { return u.Age },
        func(u *User, v int) { u.Age = v }),
)

func NewUser(name string, age int) (User, error) {
    return UserCodec.New(User{Name: name, Age: age})
}

u, err := NewUser("alice", 30)
if err != nil { return err }
// u.Name and u.Age both passed their Refine constraints
```

See `examples/construction/main.go` (`Profile`/`NewProfile`) for a runnable
version.

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

### Encoding / decoding escaped or re-serialised fields

A recurring pattern in APIs and event systems is a field whose wire value is itself a serialised string — JSON, YAML, or TOML encoded as a string, or URL-encoded, or HTML-escaped content. The `format` package provides built-in functions for all three serialisation formats:

#### JSON-in-string, YAML-in-string, TOML-in-string

Common in CloudEvents `data` as string, database JSONB stored via REST API, Kafka message headers, and device-twin configuration fields:

```json
{"event": "user.created", "payload": "{\"id\":\"123\",\"name\":\"Alice\"}"}
```

Use `format.EmbeddedJSON`, `format.EmbeddedYAML`, or `format.EmbeddedTOML`:

```go
import "github.com/DaniDeer/go-codex/format"

// Wire: {"event":"user.created","payload":"{\"id\":\"123\",\"name\":\"Alice\"}"}
// Go:   Event{Type:"user.created", Payload:User{ID:"123", Name:"Alice"}}
var eventCodec = codex.Struct[Event](
    codex.RequiredField("event",   codex.String(), ...),
    codex.RequiredField("payload", format.EmbeddedJSON(userCodec), ...),
)

// YAML or TOML — same pattern, different format
var configEventCodec = codex.Struct[ConfigEvent](
    codex.RequiredField("config", format.EmbeddedYAML(configCodec), ...),
)
var twinFieldCodec = codex.Struct[TwinField](
    codex.RequiredField("settings", format.EmbeddedTOML(settingsCodec), ...),
)
```

**Decode path:** `format.EmbeddedJSON` (1) parses the string with `json.Unmarshal` → `map[string]any`, then (2) calls `inner.Decode` which applies the codec constraints. Passing the raw string directly to `inner.Decode` would fail — the codec expects an intermediate, not a byte slice.

**Encode path:** (1) `inner.Encode` → `map[string]any`, (2) `json.Marshal` → JSON string.

**Both paths covered:** encode and decode are symmetric; inner codec constraints run on both.

**Error types** (both implement `slog.LogValuer`):
- `format.EmbeddedDecodeError{Format, Err}` — when the string cannot be parsed as the expected format
- `format.EmbeddedEncodeError{Format, Err}` — when the Go value cannot be marshalled to the format string
- Codec validation errors from the inner codec propagate unchanged (they are not wrapped)

**Format numeric type compatibility** — YAML integers unmarshal as `int`, TOML integers as `int64`. The built-in `codex.Int()`, `codex.Int64()` primitives handle all three (`int`, `int64`, `float64`) so existing codecs work without changes.

#### URL-encoded and HTML-escaped fields

The same pattern applies to other string encodings — only the mapping functions change:

```go
import (
    "html"
    "net/url"
)

// URL-encoded field (e.g. query parameters stored in a JSON field)
var urlDecodedCodec = codex.MapCodecSafe(
    codex.String(),
    func(s string) string {
        decoded, _ := url.QueryUnescape(s)
        return decoded
    },
    func(s string) (string, error) {
        return url.QueryEscape(s), nil
    },
)

// HTML-escaped field (e.g. legacy API returning HTML entities in JSON strings)
var htmlUnescapedCodec = codex.MapCodecSafe(
    codex.String(),
    func(s string) string { return html.UnescapeString(s) },
    func(s string) (string, error) { return html.EscapeString(s), nil },
)
```

`MapCodecSafe` is correct here because unescaping is infallible (any string is valid input) and escaping always succeeds.

#### Summary

| Wire value | Pattern | Function |
|---|---|---|
| `"{\"id\":\"123\"}"` | JSON-in-string | `format.EmbeddedJSON(innerCodec)` |
| `"{name: alice, value: 42}"` | YAML-in-string | `format.EmbeddedYAML(innerCodec)` |
| `"name = \"alice\"\nvalue = 42"` | TOML-in-string | `format.EmbeddedTOML(innerCodec)` |
| `"user%3A123"` | URL-encoded | `MapCodecSafe` with `url.QueryUnescape`/`url.QueryEscape` |
| `"&lt;b&gt;Hello&lt;/b&gt;"` | HTML-escaped | `MapCodecSafe` with `html.UnescapeString`/`html.EscapeString` |

`format.EmbeddedJSON`, `format.EmbeddedYAML`, and `format.EmbeddedTOML` are library functions in the `format` package. On format parse failure they return `format.EmbeddedDecodeError{Format, Err}`; on marshal failure `format.EmbeddedEncodeError{Format, Err}` — both implement `slog.LogValuer`. Codec validation errors from the inner codec propagate unchanged.

#### Composing with `ports.File[T]`

Because `EmbeddedJSON` is just a `Codec[T]`, it composes transparently with `ports.File[T]` which means `EmbeddedJSON` codec participates in the decode pass exactly like any other field codec. The outer file format (JSON/YAML/TOML) handles the file bytes; the inner `EmbeddedJSON` field codec handles the string-to-struct conversion — no special wiring needed.

```go
// File: events/user-created.json
// {
//   "event": "user.created",
//   "payload": "{\"id\":\"123\",\"name\":\"Alice\"}"
// }

var eventCodec = codex.Struct[Event](
    codex.RequiredField("event",   codex.String(), ...),
    codex.RequiredField("payload", format.EmbeddedJSON(userCodec), ...),
)
var eventFile = ports.NewFile("events/user-created.json", format.JSON(eventCodec))

event, err := eventFile.Read(nil, ports.FileOptions{})
// event.Payload == User{ID:"123", Name:"Alice"}

// Write — EmbeddedJSON encodes User → JSON string on the way out
err = eventFile.Write(nil, event, ports.FileOptions{Perm: 0644})
// Writes: {"event":"user.created","payload":"{\"id\":\"123\",\"name\":\"Alice\"}"}
```

The decode chain: `os.ReadFile` → `json.Unmarshal` → `map[string]any` → `eventCodec.Decode` → for the `payload` field: `EmbeddedJSON` parses the string value → `userCodec.Decode` → `User`. Each layer only sees its own responsibility.

This works identically with `format.YAML(eventCodec)` and `format.TOML(eventCodec)` as the outer format, and with template paths:

```go
// Template path with codec-validated path variable
var userEventFile = ports.NewFile(
    "events/{userID}/latest.json",
    format.JSON(eventCodec),
    ports.FilePathParam{Name: "userID"}.WithCodec(codex.String().Refine(validate.UUID)),
)
event, err = userEventFile.Read(
    map[string]string{"userID": "f47ac10b-58cc-4372-a567-0e02b2c3d479"},
    ports.FileOptions{},
)
```

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
