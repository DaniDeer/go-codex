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
| `codex.StringOrInt64()` (and `StringOrInt`/`StringOrInt32`/`StringOrUint`/`StringOrUint64`/`StringOrFloat32`/`StringOrFloat64`) | `Either[string,int64]` (etc.) | string or number | `{oneOf:[{type:string},{type:integer,...}]}` |
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
| `codex.StringOrInt64()` (family) | value is a string OR a number — `Either[string, int64]` (etc.) | `StringOrInt64()` for a Docker/IoT-Edge-style env var value, `"5"` or `5` |
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

`get`/`set` are a **getter** (`func(T) F`) and a **setter** (`func(*T, F)`)
for that one field — how `RequiredField`/`OptionalField`/`DefaultField`
read a value out of your struct (encode) and write a decoded value back
into it (decode). Go has no reflection-free way to do this automatically,
so plain closures are the mechanism; they are not go-codex-internal
plumbing, they're what you write at every call site:

```go
codex.RequiredField("name", nameCodec,
    func(u User) string { return u.Name },
    func(u *User, v string) { u.Name = v },
)
```

They reach nested sub-structs the same way (`func(u User) string { return u.Meta.Region }`).
See ["Field factory functions"](#field-factory-functions--reusing-field-groups-across-structs)
below for how to avoid repeating them when several struct types share the
same field.

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
| `OmitEmptyField`/`OmitEmptyFieldFunc` | Go zero value (`""`, `0`, `nil`, …) | OMITTED when current value is "empty" (`==` zero or the `isEmpty` predicate), else encoded | field not in `required` |
| `OmitDefaultField` | declared default applied | OMITTED when current value equals the declared default, else encoded | field not in `required`; `default: <value>` |

> **Encode note:** `RequiredField`/`OptionalField`/`DefaultField` never omit a field on encode — every one of them always writes the key to the output object, even at the Go zero value. For an OPT-IN way to omit a zero-valued (or default-valued) field's key entirely, see ["`OmitEmptyField`/`OmitEmptyFieldFunc`/`OmitDefaultField`"](#omitemptyfieldomitemptyfieldfuncomitdefaultfield-omitting-a-zero-valued-field-on-encode) below.

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
for string fields, or `codex.StringCodec(parse, format, schema)`/
`codex.TextCodec[V]()`/`codex.IntString()` (etc.) for typed fields like
`int`/`uuid.UUID` (vars are always string-valued at the wire level: path
segments, topic segments, header/query/cookie values) — see "Typed
(non-string) path/topic/query/header/cookie/key vars" below.

**Per-boundary sugar**: `rest.NewPathParam[T, V]`/`NewRequiredQueryParam[T, V]`/
`NewOptionalQueryParam[T, V]` (+ Header/Cookie equivalents),
`events.NewTopicParam[T, V]`, `reqreply.NewTopicParam[T, V]`, and
`ports.NewFilePathParam[T, V]` declare BOTH the boundary's spec Param (for
OpenAPI/AsyncAPI generation, unchanged) AND a merge field in ONE call — see
[Ports feature — File](../features/ports.md) and
[REST API feature](../features/rest-api.md) for the full per-boundary
API. The plain `PathParam`/`QueryParam`/`FilePathParam`/`TopicParam`
struct literals remain available as the low-level, validate-only
alternative (`codex.Param`'s own escape-hatch role — see below).

Every one of these boundary-specific `PathParam`/`TopicParam`/
`FilePathParam` types is a thin wrapper over ONE shared pair of primitives
in `codex`: `codex.Param{Name, Description, Codec}` (the validate-only
escape hatch) and `codex.MergedParam[T]`/`codex.NewParam[T, V]` (the
merge-capable counterpart, built via the SAME `RequiredField` mechanism
above). `codex.ValidateParams`/`codex.ValidateDeclaredParams`/
`codex.BuildFromParams` are the shared validate-loop/declaration-check/
build-substitution bodies every boundary's `BuildPath`/`BuildTopic`/
`ValidatePathParams`/`ValidateTopicVars` methods delegate to internally —
one canonical implementation instead of five independently-duplicated
copies (`api/rest`, `api/events`, `api/reqreply`, `ports/file.go`,
`ports/dir.go`). Each boundary keeps its OWN struct name/vocabulary
(`PathParam` reads as "a REST path variable", `TopicParam` as "an
event/reqreply topic variable") since Go requires a package-owned type to
satisfy that package's own sealed option interface (`RouteOpt`/
`ChannelOpt`/`FileOpt`/`DirOpt`) — but the validation/build LOGIC, and the
error shapes (`ParamError`/`MissingParamError`/`InvalidParamError`), are
shared. `ports`' own `FilePathParamError`/`DirPathParamError` are the one
exception, kept as their own distinct types rather than aliased, since
their `LogValue()` keys predate and differ from `codex.Param`'s.

A struct can mix BOTH sources at once — some fields decoded from the body,
others merged from vars — as long as the body codec and the merge fields
declare different field names (`RouteHandle.DecodeMerged` decodes the body
first, then merges vars into the same value, touching only the declared
merge fields). See [REST API — Mixing body fields and merged params](../features/rest-api.md#mixing-body-fields-and-merged-params-on-one-struct).

The SAME mechanism applies in the RESPONSE direction —
`rest.NewRequiredResponseHeaderParam[Resp, V]`/`NewOptionalResponseHeaderParam[Resp, V]`
(+ Cookie equivalents, also generic over `V` — a typed response header/
cookie merges directly into an `int`/`uuid.UUID`/etc. exactly like the
request-side constructors above) declare a response header/cookie merge field on
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

### `HasCodec[T]`: letting a type carry its own codec

`Codec[T].New`/`Validate` above always need the codec value in scope at the
call site (`emailCodec.New(...)`). `codex.HasCodec[T]` is an opt-in
interface — the closest Go gets to Pydantic's "the model knows how to
validate/serialize itself" without inheritance (Go has none) — that lets a
type declare its OWN canonical codec once, so generic helper functions can
then work on it without repeating the codec's name:

```go
// HasCodec is implemented by types that declare their canonical Codec.
type HasCodec[T any] interface {
    Codec() Codec[T]
}

func Validate[T HasCodec[T]](v T) error
func New[T HasCodec[T]](v T) (T, error)
func EncodeSelf[T HasCodec[T]](v T) (any, error)
func DecodeAs[T HasCodec[T]](raw any) (T, error)
func SchemaOf[T HasCodec[T]]() schema.Schema
```

A type implements it with one method:

```go
type Image struct{ Name, Tag string }

var imageCodec = codex.Struct[Image](
    codex.RequiredField("name", codex.String().Refine(validate.NonEmptyString),
        func(i Image) string { return i.Name },
        func(i *Image, v string) { i.Name = v }),
    codex.OptionalField("tag", codex.String(),
        func(i Image) string { return i.Tag },
        func(i *Image, v string) { i.Tag = v }),
)

// Value receiver, package-level codec — the common case (no per-instance state).
func (Image) Codec() codex.Codec[Image] { return imageCodec }
```

...and every generic helper works on it, with zero further per-type code:

```go
img, err := codex.New(Image{Name: "alpine", Tag: "latest"})
err = codex.Validate(img)
raw, err := codex.EncodeSelf(img)
back, err := codex.DecodeAs[Image](raw)
s := codex.SchemaOf[Image]()
```

**Zero-value-call contract for `DecodeAs`/`SchemaOf`.** Neither has a `T`
value to call `.Codec()` on yet, so both call it on `T`'s zero value
(`var zero T; zero.Codec()...`). This is correct and side-effect-free for
the documented common case (a stateless `Codec()`), but a `HasCodec`
implementation whose `Codec()` genuinely depends on instance state must
NOT be used with `DecodeAs`/`SchemaOf` — its zero value would return the
WRONG codec. `Validate`/`New`/`EncodeSelf` have no such restriction; they
always call `Codec()` on the actual value passed in.

**This does not change the "no auto-derived `NewUser(...)`" decision
above.** `HasCodec` gives you generic DISPATCH over a codec you already
declared by hand — it is not a way to synthesize a constructor from field
names, and the same reflection/struct-tag/variadic-arity limitations still
apply. Adoption is entirely opt-in, exactly like `Codec[T].New` itself —
no existing type or package is required to implement `HasCodec`.

### `PartialField`/`PartialStruct`: patching an existing struct

`Struct[T]`'s own `Encode` unconditionally writes EVERY declared field
into its output map — `RequiredField` and `OptionalField` alike. There is
no built-in "omit this field from the encoded object when it was never
set" behavior, so a "patch"/"partial update" struct (only SOME fields
set, and only those written to the wire) can't be expressed with
`Struct` directly. `PartialField`/`PartialStruct` are a parallel
interface/constructor pair — not a modification of `FieldCodec`/`Struct`
— for exactly that case:

```go
type ProfilePatch struct {
    Nickname *string
    Age      *int
}
var profilePatchCodec = codex.PartialStruct[ProfilePatch](
    codex.PartialField("nickname", codex.String(),
        func(p ProfilePatch) *string { return p.Nickname },
        func(p *ProfilePatch, v *string) { p.Nickname = v }),
    codex.PartialField("age", codex.Int(),
        func(p ProfilePatch) *int { return p.Age },
        func(p *ProfilePatch, v *int) { p.Age = v }),
)

raw, _ := profilePatchCodec.Encode(ProfilePatch{Nickname: ptr("bob")})
// raw == map[string]any{"nickname": "bob"} — "age" key is ABSENT, not null.
```

Every `PartialField`-declared field is required to be a pointer `*F` on
`T`: nil means "not set, leave untouched" when encoding; non-nil means
"set to this value". `codec` is the SAME field-level `Codec[F]` an
existing full-struct declaration for this concept already uses —
reused completely unchanged, so a patch field's validation is *inherited*
from the base type's own codec with zero new logic. Decode is the
mirror: only fields whose keys are present in the input get assigned;
absent keys leave the target pointer nil. If every field is nil,
`Encode` returns an empty `map[string]any` (not an error — a caller for
whom an entirely-empty patch is meaningless should reject it themselves
via their own domain error).

**Why not just extend `OptionalField`?** `FieldCodec[T]`'s sealed
`encode` method returns `(string, any, error)` — no room for a "was this
actually set" signal without either a breaking interface change touching
every existing `Field[T,F]` consumer, or an ad-hoc sentinel value
threaded through `any` (exactly the kind of implicit-behavior hack this
library avoids elsewhere). A parallel interface avoids touching
`FieldCodec`/`Struct` at all, and keeps `Struct`'s well-tested
"always-write-every-field" semantics completely separate from
`PartialStruct`'s "omit-when-unset" semantics — two clearly-named,
single-purpose entry points instead of one function with two presence
models a reader would have to hold in their head at once.

**Nesting needs no special mechanism.** A `PartialStruct`-built `Codec[F]`
can be used as another `PartialField[Outer, F]`'s `codec` — `PartialField`
accepts any `Codec[F]`, and `PartialStruct` returns a plain `Codec[T]`,
so this is ordinary `Codec[T]` composability (the same way `Struct`
already nests inside `Struct` today). Presence for the nested field is
decided exactly like any other field: is the OUTER pointer nil? The
caller only allocates the nested pointer (`&SettingsPatch{Image: &img}`)
when it actually means to include a change inside it — there is no
"auto-collapse an empty nested result" heuristic involved.

**Nesting gotcha**: presence is decided by `!= nil`, not "has any inner
field set" — so a non-nil-but-entirely-empty nested pointer (e.g.
`&SettingsPatch{}` with every one of ITS OWN fields left nil) still
encodes as present (`"settings": {}` — a key with an empty object, not an
absent key). This is a natural consequence of the nil-means-unset rule,
not a bug, but it's a real footgun: only allocate a nested patch pointer
when you're about to set at least one field inside it.

**Gotcha: named types over `Map`/`SliceOf`.** `codex.Map[K,V]`/
`codex.SliceOf[T]` return a `Codec` over the PLAIN underlying type
(`map[K]V`/`[]T`), not any named type built on top of it. If `F` in
`PartialField[T,F]` is a named type (e.g. `type EnvVars
map[EnvVarName]EnvVar`), passing that raw `Map`-built codec directly to
`PartialField` fails Go's generic type inference — even though the
underlying representation is identical. Fix: wrap it in a trivial
identity retype via `codex.MapCodecSafe` (`Codec[map[K]V]` →
`Codec[NamedType]`) before passing it to `PartialField` — see
`modulepatch.envVarsCodec` for the reference pattern.

No new structured error types and no observer integration are needed:
`PartialStruct`/`PartialField` reuse whatever errors the per-field
`Codec[F]` values already produce (`ValidationError`/`ValidationErrors`,
exactly like `Struct`), and are pure codec-construction primitives with
no I/O, same as `Struct`/`UntaggedUnion`/`TaggedUnion`. See
`examples/go-edge-models/models/iotedge/modulepatch.ModuleFieldsPatch`
for a real-world worked example (patching one IoT-Edge module's fields,
including a nested `ModuleSettingsPatch` grouping) — this subsection and
`.github/instructions/go-codex.instructions.md`'s matching section are
now the authoritative design reference (the feature's roadmap doc was
retired once shipped).

### `OmitEmptyField`/`OmitEmptyFieldFunc`/`OmitDefaultField`: omitting a zero-valued field on encode

`Struct[T]`'s `Encode` unconditionally writes EVERY declared field into its
output map — `RequiredField`, `OptionalField`, and `DefaultField` alike (see
the "Encode note" callout above). `PartialField`/`PartialStruct` already
solve "omit unset" — but only by converting **every** field of the struct to
a pointer, a shape suited to a dedicated "patch" type, not to retrofitting
onto an ordinary value-typed struct just to reduce noise on a handful of
fields. `OmitEmptyField`/`OmitEmptyFieldFunc`/`OmitDefaultField` are a
parallel, opt-in mechanism for exactly that case — no pointer reshape, and
freely mixable with `Required`/`Optional`/`Default` fields in the SAME
`Struct(...)` call:

```go
type Build struct {
    Context    string
    Dockerfile string
}
var buildCodec = codex.Struct[Build](
    codex.OmitEmptyField("context", codex.String(),
        func(b Build) string { return b.Context },
        func(b *Build, v string) { b.Context = v }),
    codex.OmitEmptyFieldFunc("dockerfile", codex.String(),
        func(b Build) string { return b.Dockerfile },
        func(b *Build, v string) { b.Dockerfile = v },
        func(v string) bool { return v == "" }),
)

raw, _ := buildCodec.Encode(Build{Context: "./app"})
// raw == map[string]any{"context": "./app"} — "dockerfile" key is ABSENT, not "".
```

**Hard usage rule — not a soft suggestion.** Comparing a field's current
value to Go's zero value to decide "omit it" is fundamentally ambiguous
whenever the zero value is ALSO a legitimate, deliberately-chosen value
(e.g. a `Retries int` field where `0` means "explicitly no retries," not
"never touched") — the exact same long-standing criticism of
`encoding/json`'s `omitempty` tag. **Only use this family for fields whose
own documented type convention already treats the Go zero value as a
first-class "absent" sentinel** (this codebase's own convention for
`MemLimit==0`, `Healthcheck==Healthcheck{}`, `Build.Context==""`). For a
field where the zero value is itself meaningful, distinguishable data, use
`PartialField`/`PartialStruct` instead — the pointer-based approach is the
only one that can actually make that distinction.

- **`OmitEmptyField[T, F comparable]`** — the ergonomic shorthand: omits
  when the current value equals `F`'s Go zero value via `==`.
- **`OmitEmptyFieldFunc[T, F any]`** — the escape hatch: an explicit
  `isEmpty func(F) bool` predicate, required whenever `F` isn't
  `comparable` (slices, maps) or "empty" means something other than Go's
  zero value (e.g. a domain type's own `IsSet()`-style check).
- **`OmitDefaultField[T, F comparable]`** — decodes exactly like
  `DefaultField` (absent key → declared default applied) but omits the key
  on encode whenever the current value equals that SAME declared default —
  a "minimal diff" round trip. This does **not** modify `DefaultField`
  itself, which keeps its existing "always show the resolved value"
  contract (relied on by real configs that want the effective value
  explicit) — it's a separate, additive sibling.

**Why not just change `OptionalField`/`DefaultField` directly?** Both are
used across dozens of existing call sites where zero (or the declared
default) is legitimate, already-relied-upon data — silently flipping their
encode behavior would change wire output for every existing caller with no
migration path. Keeping this opt-in, per-field, and separately named avoids
that blast radius entirely — exactly the same reasoning `PartialField`
already established for staying a parallel mechanism instead of a breaking
`FieldCodec`/`Struct` change.

**Nil-vs-empty correctness for slices/maps.** `OmitEmptyFieldFunc`'s
predicate should compare against `nil`, not `len(s) == 0` — a `nil` slice
means "the key was never in the wire object" (exactly what `OptionalField`'s
own decode already produces for an absent key), while a non-nil,
zero-length slice (`[]string{}`) means "the wire explicitly had an empty
array" — a genuinely different state a `len()==0` check would incorrectly
conflate. `codex.IsZeroValue[F any](v F) bool` is a ready-made,
reflection-based predicate that gets this right automatically (it checks
`IsNil()` for slices/maps/pointers, not length) — pass it explicitly to
`OmitEmptyFieldFunc` when you don't want to hand-write `s == nil` yourself:

```go
codex.OmitEmptyFieldFunc("tags", codex.SliceOf(codex.String()),
    get, set, codex.IsZeroValue)
```

**This is go-codex's only use of reflection, and it is entirely opt-in** —
never a hidden default. `Struct`'s own field constructors
(`RequiredField`/`OptionalField`/`DefaultField`) and this family's own
zero-value shorthand (`OmitEmptyField[T, F comparable]`) use plain `==`
comparison, no reflection at all; `codex.IsZeroValue` exists purely as a
convenience a caller may choose to pass to `OmitEmptyFieldFunc`'s `isEmpty`
parameter. This deliberately does NOT become `OmitEmptyField`'s automatic
default behavior for non-comparable types — a blind structural zero-check
knows nothing about a type's own business meaning of "empty" (e.g. a
domain type whose `IsSet()` checks only ONE of its fields, not the whole
struct), so an implicit reflect-based default could silently disagree with
that type's own convention.

**Interaction with `Template`/`DottedKeyCodec`/`DecodeVars`/`EncodeVars`.**
`FieldCodec[T]` has four consumers besides `Struct`/`StrictStruct` —
`DottedKeyCodec`, `Template`'s `NewTemplate`/`Fields()`, and
`DecodeVars`/`EncodeVars`. None of them route through `Struct`'s Encode
loop — they call a field's plain `encode()` method directly. A sparse
field's plain `encode()` ALWAYS encodes the current value, exactly like
`OptionalField` — it deliberately does NOT consult `isEmpty`. Only
`encodeSparse()` (consulted exclusively by `Struct`'s own type-assertion
check) honors the omit rule. This guarantees a path/topic/dotted-key var
declared with `OmitEmptyField` never silently goes missing from a
URI/topic/key build merely because its value happened to be zero — in
practice this scenario shouldn't arise, since path/topic template vars are
conventionally always-required, but the guarantee holds regardless.

No new structured error types and no observer integration are needed:
these constructors reuse whatever errors the per-field `Codec[F]` already
produces (`ValidationError`/`ValidationErrors`, exactly like `Struct`), and
are pure codec-construction primitives with no I/O.

### `Maybe[T]`: definitive presence tracking

`OmitEmptyField`/`OmitEmptyFieldFunc` (above) solve "omit a zero-valued
field on Encode" via a HEURISTIC: compare the current value to Go's zero
value (or a custom predicate). This heuristic is fundamentally UNABLE to
distinguish two states that are byte-identical in Go's memory model: "this
field was never touched" vs. "this field was explicitly set to exactly the
zero-equivalent value" (e.g. a caller who genuinely means `Retries: 0`,
`Nick: ""`). `codex.Maybe[T]` — go-codex's Go rendering of Haskell's
`Maybe`/Rust's `Option` — is a lightweight, PER-FIELD alternative: a small
value type pairing a `T` with an explicit "was this ever set" bit
(`Just`/`Nothing`), usable as the type of ONE struct field (not the whole
struct) — giving `OmitEmptyField`-style omission a DEFINITIVE signal
instead of a heuristic, without forcing every OTHER field in the struct to
become a pointer too (contrast with `PartialField`/`PartialStruct`, which
solves this same ambiguity but requires an ALL-pointer struct reshape).

**`Maybe[T]` does NOT replace `OmitEmptyField`/`OmitEmptyFieldFunc`.** They
remain the right choice for the common case — a field whose zero-means-
absent convention is already safe and documented (as with every field in
`dockercompose.Service`). `Maybe[T]` is the precise escape hatch for the
genuinely ambiguous cases where that heuristic isn't good enough.

```go
type Profile struct {
    Name string
    Nick codex.Maybe[string]
}
var profileCodec = codex.Struct[Profile](
    codex.RequiredField("name", codex.String(),
        func(p Profile) string { return p.Name },
        func(p *Profile, v string) { p.Name = v }),
    codex.MaybeField("nick", codex.String(),
        func(p Profile) codex.Maybe[string] { return p.Nick },
        func(p *Profile, v codex.Maybe[string]) { p.Nick = v }),
)

var p Profile
p.Nick.Set("")     // explicitly set to "" -- NOT the same as never touched
raw, _ := profileCodec.Encode(p)
// raw == map[string]any{"name": "", "nick": ""} -- "nick" key IS present,
// because it was explicitly Set, even though its value is the zero value.
// A never-touched p.Nick (Maybe[string]{}) would omit the "nick" key
// entirely -- the exact distinction OmitEmptyField cannot make.
```

`Maybe[T]`'s API: `Just(v)`/`Nothing[T]()` construct a set/unset value;
`Set(v)` is REPEATABLE (every call overwrites — contrast with
`Immutable.Set`'s exactly-once contract); `Get()` NEVER panics (returns
`T`'s zero value when unset — contrast with `Immutable.Get`, which panics
before the first `Set`); `IsSet()`/`TryGet()` are the explicit-presence-
check siblings. Four combinators round out the type for working with it
functionally: `MaybeMap[T,R](m, fn)` (a free function, not a method — Go
generic methods cannot introduce a new type parameter `R`, the same
constraint `forge.NewFunction[In, Out]` already documents elsewhere;
applies `fn` only if set, `Nothing` in → `Nothing` out) — named `MaybeMap`
rather than the shorter `Map` to avoid colliding with the existing
`codex.Map[K,V]` map-codec constructor; `MaybeFlatMap[T,R](m, fn)` (also a
free function, same reasoning) chains a `Maybe`-returning function
instead — Haskell's `>>=`/Rust's `and_then` — for the case where the
transformation ITSELF might not produce a value (e.g. parsing a
`Maybe[string]` into a `Maybe[int]`, where the string might not parse): a
`Just` input can still yield `Nothing` if `fn` does, unlike `MaybeMap`,
whose `fn` always returns a plain `R`; `OrElse(fallback)` (the safe-
default-value idiom, Rust's `unwrap_or`/Haskell's `fromMaybe`); `Filter(pred)`
(narrows "set" down to "set AND satisfies some condition," returning
`Nothing` otherwise).

**`Maybe[T]` the TYPE has no ambient codec — but `MaybeCodec[T]` derives
one, symmetric with `Either2`.** Unlike `Immutable`/`Mutable` (which both
REQUIRE a `Codec[T]` at construction), a bare `Maybe[T]` value carries no
codec of its own — a zero-initialized `Maybe[V]{}` is already meaningful
(`Nothing`) with no constructor call needed at all. This mirrors `Either`
exactly: the bare `Either[A,B]` struct ALSO has no ambient codec — you
always derive one from inner codecs (`Either2(ca, cb)`). `MaybeCodec[T](inner
Codec[T]) Codec[Maybe[T]]` is `Maybe`'s exact counterpart to `Either2` —
both are plain, general codec-derivation functions, usable with ANY
composer (`RequiredField`, `OptionalField`, `SliceOf`, `Map`, or
standalone), not just via a dedicated field constructor:

```go
codex.MaybeCodec(codex.String()).Decode("hi")      // Just("hi")
codex.SliceOf(codex.MaybeCodec(codex.Int()))       // Codec[[]Maybe[int]]
codex.RequiredField("note", codex.MaybeCodec(codex.String()), get, set)
// -- decodes Just/Nothing correctly, but Encode ALWAYS shows the key
// (Nothing renders as "", the inner codec's zero value) -- no omission.
// Use MaybeField instead when omission-on-Nothing is what you want.
```

**`MaybeField` is a documented, provable special case of this general
composition** — `MaybeField(name, codec, get, set)` behaves EXACTLY like:

```go
codex.OmitEmptyFieldFunc(name, codex.MaybeCodec(codec), get, set,
    func(m codex.Maybe[V]) bool { return !m.IsSet() })
```

(`TestMaybeField_EquivalentToOmitEmptyFieldFuncPlusMaybeCodec` proves this
by constructing a `Struct` BOTH ways and asserting identical Encode
output for every presence state.) This is the exact same relationship
`StringOrInt64()` already has to `Either2(String(), Int64())` — a named,
common-case convenience built directly on a general composition, not a
separate mechanism. `MaybeField`'s own internal implementation is left
as its existing, already-tested hand-written form (not literally rewritten
to call `OmitEmptyFieldFunc`) — the equivalence is proven by test, not by
sharing code, avoiding churn to working code for a purely cosmetic reason.

**How does `MaybeField` render in Schema?** IDENTICALLY to a plain
`OptionalField` of the same type — no `oneOf`/`nullable`/any marker
distinguishing "definitively presence-tracked" from "always-shown
optional." Verified by an actual render:

```go
codex.MaybeField("nick", codex.String(), get, set)
// Schema -> {"Type": "string"} -- indistinguishable from
// codex.OptionalField("nick", codex.String(), get, set)'s OWN schema,
// and NOT present in the Struct's "Required" array.
```

**This is correct, not a gap.** `MaybeCodec[T]`'s `Schema` field is
literally `inner.Schema`, untouched, and `sparseField.schema()` (shared by
every sparse-family constructor — `OmitEmptyField`/`OmitDefaultField`/
`MaybeField` alike) always reports `Required=false`. The WIRE contract
genuinely is identical between `MaybeField` and `OptionalField` for the
same `V` — a valid document either omits the key or includes `V`'s shape,
in BOTH cases. `Just`/`Nothing` vs. "Go zero value" is a Go-side,
POST-decode distinction with no wire-visible difference to describe — a
schema correctly stays silent about a distinction that doesn't exist on
the wire. (Contrast with `EitherField`'s schema, which DOES differ
visibly from a plain field's — see the Either ergonomics section below.)

**Why not just use a pointer (`*T`), like `PartialField` does?** A fair
question, since `PartialField`/`PartialStruct` already ship and already
solve this ambiguity definitively via pointers. Pointers remain the right
tool for a DEDICATED patch/partial-update type where EVERY field is
independently optional — the all-pointer reshape is the point there, not a
cost. But reusing bare pointers as the GENERAL per-field answer has real
downsides `Maybe[T]` avoids:

- **No struct reshape required.** Making one ordinary domain field's type
  `*string` instead of `string` touches every existing read site of that
  field (arithmetic, string ops, anything not expecting a pointer) — real,
  spread-out churn. `Maybe[T]` only changes what the FIELD wraps, not what
  every consumer must additionally nil-check.
- **Safe-by-default, not panic-by-forgetting.** Every pointer read site
  must remember to nil-check or safely dereference. `Maybe[T]`'s `Get()`
  is ALWAYS safe (zero value, never panics); the presence check
  (`IsSet()`/`TryGet()`) is the deliberate opt-in, inverting the usual
  pointer footgun.
- **No aliasing.** Copying a struct with pointer fields means BOTH copies
  share the same underlying value — mutating through one pointer silently
  affects the other. `Maybe[T]` carries `T` BY VALUE inside the wrapper;
  copying a `Maybe[T]`-containing struct copies the value too, exactly
  like every other value-typed field in Go.
- **No extra heap allocation.** Each non-nil pointer is a separate heap
  allocation; `Maybe[T]{value T; set bool}` stores `T` INLINE — no
  additional indirection beyond the struct's own layout, a real difference
  under load for structs with many optional fields (e.g. a hot decode
  path).
- **No collision with `Nullable[T]`'s existing meaning for `*T`.**
  go-codex already uses `*T` (via `codex.Nullable[T]`) for a DIFFERENT
  axis: wire-level explicit `null` vs. a real value. Reusing bare pointers
  ALSO for "was the Go field ever assigned" would overload one Go pattern
  with two different meanings depending on context.

**A concrete, already-shipped example of this exact ambiguity**: re-read
the `OptionalField`+`Nullable` example earlier in this doc —

```go
// "note" absent  → Note == nil  (key was not in the object)
// "note": null   → Note == nil  (key was present, value was null)
```

`OptionalField`+`Nullable` cannot distinguish "absent" from "explicit
null" either — BOTH produce `Note == nil` today (only `RequiredField`+
`Nullable` avoids this, since a required key is guaranteed present). This
is the exact same shape of ambiguity this section is about, just on the
"wire-null vs. absent" axis instead of "zero-value vs. absent."

**One deferred idea remains, not designed here** — see
`docs/roadmap/maybe-nullable-and-codec.md` for a short "idea only" sketch:
a THREE-state `Maybe[Nullable[T]]` composition (absent / present-null /
present-value — the exact shape RFC 7396 JSON Merge Patch needs) that
could resolve the ambiguity above (already VERIFIED to compose today with
zero new code — `MaybeField("x", codex.Nullable(inner), ...)` on a
`Maybe[*T]` field genuinely distinguishes all three states; what's left
is a worked doc example, not a new primitive). The doc's OTHER idea — a
general-purpose public `codex.Codec[Maybe[T]]` — has SHIPPED as
`MaybeCodec[T]` above.

**Also see `docs/roadmap/optional-mutable.md`** for `OptionalMutable[T]`
— a DIFFERENT, NOT-yet-built idea (a `Mutable[T]` that may start unset)
that sounds superficially similar to `Maybe[T]` but solves an opposite
problem (a value's currentness over time, vs. `Maybe`'s provenance at a
point in time) — see that doc's own comparison table.

**Interaction with `Template`/`DottedKeyCodec`/`DecodeVars`/`EncodeVars`.**
`MaybeField` is built on the SAME `sparseField[T,F]` machinery as
`OmitEmptyField` (see that subsection's own "Interaction with..." note) —
so it inherits the identical caveat. `FieldCodec[T]` has four consumers
besides `Struct`/`StrictStruct` (`DottedKeyCodec`, `Template`'s
`NewTemplate`/`Fields()`, and `DecodeVars`/`EncodeVars`), and none of them
route through `Struct`'s Encode loop — they call a field's plain
`encode()` method directly. A `MaybeField`-declared field's plain
`encode()` ALWAYS encodes the current value (calling `Maybe[V].Get()`,
which returns `V`'s zero value for `Nothing`) — it deliberately does NOT
consult `IsSet()`. Only `encodeSparse()` (consulted exclusively by
`Struct`'s own type-assertion check) honors the omit rule. This guarantees
a path/topic/dotted-key var declared with `MaybeField` never silently goes
missing from a URI/topic/key build merely because it was never `Set` — in
practice this scenario shouldn't arise, since path/topic template vars are
conventionally always-required, but the guarantee holds regardless.

No new structured error types and no observer integration are needed:
`Maybe[T]`'s own `Set`/`Get` are infallible, and decode errors flow
through the enclosing field's `Codec[V]`, exactly like `OmitEmptyField`.

### Which presence tool do I use? A decision guide

Five different mechanisms in this codebase all touch "is this field
present/optional" in some way. They are NOT competing — each answers a
different question:

| Tool | Distinguishes absent from zero? | Struct reshape needed? | Extra allocation? | Use when... |
|---|---|---|---|---|
| `OptionalField` | No — zero value on absent, ALWAYS encoded | None | None | The field is genuinely optional AND its zero value is fine to always show on re-encode |
| `OmitEmptyField`/`OmitEmptyFieldFunc` | No (heuristic — zero value is TREATED as absent) | None | None | Zero value is ALREADY a safe, conventional "absent" sentinel for this field (a documented codebase convention) — the common case |
| `Maybe[T]` | **Yes, definitively** | None (only the ONE field's type changes) | None (value-typed, inline) | You need to tell "never touched" from "deliberately zero" for one or a few fields, without reshaping the whole struct |
| `Nullable[T]` (+ `RequiredField`) | Distinguishes wire `null` from a real value — a DIFFERENT axis, not absence | None | One pointer per field | You need wire-level explicit `null` vs. a value; combine with `RequiredField` if "absent" must also be impossible |
| `PartialField`/`PartialStruct` | **Yes, definitively**, for EVERY field at once | Whole struct becomes all-pointer (a dedicated "patch" type) | One pointer per field | You're building a dedicated PATCH/partial-update type where EVERY field is independently optional |

**Start with `OmitEmptyField`** — it's the cheapest and matches most real
zero-means-absent conventions already documented in a codebase (as
`dockercompose.Service` demonstrates for all 14 of its fields). **Reach
for `Maybe[T]`** only once you've concretely hit the ambiguity it solves —
don't reach for it defensively on every field "just in case." **Use
`PartialField`** when the WHOLE struct is a patch/partial-update BY
DESIGN, not for one-off fields on an otherwise-ordinary domain struct.
**`Nullable[T]`** solves a different problem (wire null) entirely and
composes with any of the above rather than competing with them.

#### Caveat: none of these belong on a path/topic param's merge `Field`

`rest.NewPathParam[T, V]`/`events.NewTopicParam[T, V]`/
`reqreply.NewTopicParam[T, V]` (and their shared foundation,
`codex.NewParam[T, V]`) always build their merge `Field` as a plain
`RequiredField` — there is no constructor argument to swap in
`OmitEmptyField`/`MaybeField`. A caller COULD hand-construct a
`codex.MergedParam[T]{Field: ...}` with one of these anyway (`Field` is
just an exported `codex.FieldCodec[T]`), but it would be inert:

- **`OmitEmptyField`/`MaybeField`** — decode never sees "absent", because a
  route/channel only matches when the path/topic segment is present in the
  first place; and `codex.EncodeVars` (used to build outgoing path/topic
  vars) always calls a field's plain `encode()`, never `encodeSparse()`, so
  the omission logic never fires either. Net effect: byte-for-byte
  identical to `RequiredField`, just with pointless `Maybe[V]`/sparse
  ceremony. This isn't a bug — it's confirmation that a path/topic
  variable structurally can't be "absent" (unlike a JSON body key): the
  segment is either present-and-matched, or the route/channel never
  matched at all. This holds regardless of V — typed params (see below)
  don't change this.
- **`EitherField`** is the one that DOES make sense here — a path/topic
  segment legitimately can be "one of two shapes" (e.g. a UUID-slug OR a
  numeric-string ID, mirroring Kubernetes' `IntOrString` but in a URL/topic
  segment). It composes because `EitherField` is just
  `RequiredField(name, Either2(ca, cb), get, set)` — same "always present"
  shape as a plain path param, just with a `oneOf` codec underneath.
  Requirement: BOTH `ca`/`cb` must decode from/encode to a Go `string`
  (`codex.DecodeVars`/`EncodeVars` box every path/topic var as `string`) —
  e.g. `Either2(codex.String().Refine(isSlug), codex.IntString())` now
  works (see below), whereas plain `codex.Int()` still doesn't (it only
  accepts `float64`/`int`/`int64`, never a raw string).
- A "`Maybe` that errors instead of returning `Nothing`" isn't a gap to
  fill either — that's just `RequiredField`'s existing behavior (decode
  fails when the key is absent). `MaybeField`'s entire purpose is safe
  optionality; making it throw on `Nothing` would defeat that purpose.

### Typed (non-string) path/topic/query/header/cookie/key vars

`codex.DecodeVars`/`codex.EncodeVars` box every var value as a plain Go
`string` on the wire — that's the ONLY hard requirement. Any `Codec[V]`
that decodes from a boxed `string` and encodes back to a `string` composes
with `RequiredField`/`OptionalField`, and therefore with `codex.NewParam`
and every per-boundary constructor built on it
(`rest.NewPathParam`/`NewRequired`/`OptionalQueryParam`/
`NewRequired`/`OptionalHeaderParam`/`NewRequired`/`OptionalCookieParam`,
`events.NewTopicParam`, `reqreply.NewTopicParam`,
`ports.NewFilePathParam`, `ports.NewCacheKeyParam` — all of them generic
over `V`, not hardcoded to `Codec[string]`). This lets a path/topic/query/
header/cookie/key variable merge directly into an `int`, a `uuid.UUID`, or
any other typed field instead of a bare `string` the handler has to
re-parse by hand:

```go
rest.NewPathParam("id", codex.IntString(),
    func(r GetUserReq) int { return r.ID },
    func(r *GetUserReq, v int) { r.ID = v },
)
```

Two primitives in `codex` build such a `Codec[V]`:

- **`codex.StringCodec[V](parse, format, schema)`** — explicit, zero
  magic: you supply the `string → V` parse function and the `V → string`
  format function directly. Use for anything without a pre-existing text
  form, or when you want full control (custom error wrapping, a stricter
  schema, etc.).
- **`codex.TextCodec[V]()`** — zero boilerplate for any `V` whose pointer
  already implements `encoding.TextMarshaler`/`encoding.TextUnmarshaler`
  (the standard Go convention for "this type has a canonical string
  form"), e.g. `uuid.UUID` from `github.com/google/uuid`:
  `codex.TextCodec[uuid.UUID]()`.

Ready-made convenience codecs built on `StringCodec` for the common stdlib
cases: `codex.IntString()`, `codex.Int64String()`, `codex.UintString()`,
`codex.BoolString()`.

Internally, `codex.NewParam` derives the param's spec-level `Codec[string]`
(used by `ValidateParams`/`BuildFromParams`, which only ever see the raw
wire string) from the typed `codec` via the exported
`codex.StringValidatorFrom[V]` helper — it parses the wire string through
`codec` to apply V's constraints, then discards the typed result and
returns the original string. An invalid path segment (e.g. `"abc"` for an
`IntString()`-typed param) is rejected at spec/pre-flight validation time,
before `Decode` ever runs — identical behavior to a plain
`codex.String().Refine(...)`-typed param, just generalized to any `V`.

### `Getter`/`Setter`: validated value containers built on `Codec[T]`

Everything above validates *runtime* values passed through `Encode`/
`Decode`. A separate, smaller need: giving a compile-time-authored
CONSTANT (a fixed path template, a protocol version string) or a
runtime value SET EXACTLY ONCE (a config/env var loaded once at
startup) — or reassigned REPEATEDLY at runtime, re-validated on every
reassignment (a rotating credential), optionally with an explicit
validity window (a memoized, TTL/invalidate-able value) — the same
declarative validation, instead of leaving it a bare, unchecked literal
or variable. `codex.Getter[T]`/`Setter[T]`/`FreshGetter[T]` (declared
standalone in `codex/getter.go`, so future codec-backed containers can
satisfy them too) and their four implementations, `Const[T]`/
`Immutable[T]` (`codex/const.go`), `Mutable[T]` (`codex/mutable.go`),
and `Cacheable[T]` (`codex/cacheable.go`), are a THIRD layer sitting on
top of the two that already exist:

| Layer | What it validates | Reference |
|---|---|---|
| 0 — `Codec[T]` | A wire SHAPE, on every `Encode`/`Decode` call | `codex/codec.go` |
| 1 — `HasCodec[T]` | "This TYPE knows its own Codec" — generic helpers (`Validate`/`New`/`EncodeSelf`/`DecodeAs`/`SchemaOf`) work on any type implementing it | `codex/hascodec.go` |
| 2 — `Getter[T]`/`Setter[T]`/`FreshGetter[T]` + `Const[T]`/`Immutable[T]`/`Mutable[T]`/`Cacheable[T]` | "This VALUE's identity, at a specific point in its OWN lifecycle (authored-at-compile-time / assigned-once-at-runtime / reassigned repeatedly at runtime / reassigned with an explicit validity window), is validated" — generic CONTAINERS parameterized by an externally supplied `Codec[T]` | `codex/const.go`, `codex/mutable.go`, `codex/cacheable.go` |

**Not literally "higher-kinded"** — Go generics have no abstraction over
type *constructors*, only over concrete types — but this table captures
a genuinely reusable recipe: wrap a `Codec[T]`, add ONE lifecycle rule,
expose `Getter`/`Setter`. Nothing new is invented at the codec level:
both consumers below validate their incoming value via the SAME
`Codec[T].Validate` every other part of go-codex already uses.

```go
type Getter[T any] interface{ Get() T }
type Setter[T any] interface{ Set(T) error }
type GetterSetter[T any] interface {
    Getter[T]
    Setter[T]
}
```

**`Const[T]`** — compile-time-authored, eagerly validated, PANICS if
invalid (mirrors `Must`/`Must2`'s existing panic-on-invalid convention
for authored, not runtime, input):

```go
var pathPatternCodec = codex.String().Refine(validate.NonEmptyString)

var useCasePathPattern = codex.MustConst(
    fmt.Sprintf("%s/{%s}.json", useCasesDirName, useCaseNameVar),
    pathPatternCodec,
)

useCasePathPattern.Get()      // "usecases/{usecase_name}.json"
useCasePathPattern.String()   // same, via fmt.Sprint — works for any T
```

`Const[T]` implements ONLY `Getter[T]` — its value is fixed forever at
construction, so there is no runtime "assign" to expose. It is a plain
value type (safe to copy freely, always fully valid).

**`Immutable[T]`** — runtime-supplied, validated exactly once, at the
ONE `Set` call — the "config/env var loaded once at startup, then
read-only for the rest of the process" shape. `config.FromEnv` (see
[Feature: Config](../features/config.md)) is now the batteries-included
way to get there for a whole struct-shaped config, returning the loaded
value ALREADY wrapped in a fresh `*Immutable[T]`:

```go
appConfig, err := config.FromEnv(configCodec, "APP_")
if err != nil {
    log.Fatal(err) // real external input — an error, never a panic
}
// ... elsewhere, for the rest of the process's lifetime:
cfg := appConfig.Get()
```

For a single scalar value (not a whole struct), construct an
`Immutable[T]` directly and `Set` it from `config.FromEnvVar`'s result
(or `os.Getenv`) yourself — `FromEnvVar` stays plain-value on purpose
(see `config.FromEnvVar`'s own doc comment for why):

```go
var apiBaseURL = codex.NewImmutable(codex.String().Refine(validate.NonEmptyString))

func main() {
    if err := apiBaseURL.Set(os.Getenv("API_BASE_URL")); err != nil {
        log.Fatal(err) // real external input — an error, never a panic
    }
    // ... elsewhere, for the rest of the process's lifetime:
    url := apiBaseURL.Get()
}
```

`Immutable[T]` implements `GetterSetter[T]` — both `Getter[T]` and
`Setter[T]` — and, unlike `Const[T]`, has a genuine "not yet set"
lifecycle state before the first `Set`, so it is used via a pointer
(`*Immutable[T]`) and guards its state with a mutex:

- `Set` validates via the SAME `codec.Validate` and returns an error
  (not a panic) on an invalid value — real runtime input, not an
  authored literal.
- A SECOND `Set` call always fails with `ImmutableAlreadySetError`,
  regardless of whether the new value would itself be valid — "set
  once" is enforced, not just documented.
- `Get()` PANICS if called before any successful `Set` (a real bug:
  reading config before startup finished loading it) — `TryGet() (T, bool)`
  is the safe, non-panicking alternative for optional/defensive access.

This asymmetry (`Const[T]` is `Getter[T]`-only; `Immutable[T]` is a full
`GetterSetter[T]`) documents the conceptual difference directly:
`Getter[T]` alone means "this value's identity is already settled";
`Getter[T]` + `Setter[T]` together means "this value's identity can
still be settled, exactly once, by someone." No structured error type
is needed beyond `ImmutableAlreadySetError` (empty — nothing to report
beyond the fact itself) and no observer integration applies (no I/O, no
adapter boundary — `MustConst`/`Set` each run once, not on a repeated
request/response path).

**`Mutable[T]`** — runtime-supplied, validated at EVERY `Set` call (not
just once) — the "config that can be hot-reloaded without a restart"
shape (a rotating JWKS key set, a rotating shared API key). Unlike
`Immutable[T]` (set exactly once, panics on a repeat `Set`), `Mutable[T]`
always holds a valid value from construction onward:

```go
keys, err := codex.NewMutable("jwks-signing-keys", "key-v1",
    codex.String().Refine(validate.NonEmptyString))
if err != nil {
    log.Fatal(err) // real external input — an error, never a panic
}

// A SecurityFunc-shaped closure always reads the CURRENT key:
current := keys.Get() // never panics — construction guarantees validity

// A background rotation loop (e.g. a JWKS refresh ticker) calls Set —
// re-validated, last-good-value-wins on an invalid Set:
if err := keys.Set("key-v2"); err != nil {
    // current value is UNCHANGED — the bad key never took effect
    log.Warn("rotation rejected", "err", err)
}
```

- `Get()` NEVER panics — construction (`NewMutable`) requires a valid
  `initial` value, so there is no "unset" state to guard against, unlike
  `Immutable[T]`; no `TryGet` needed either.
- Every valid `Set` succeeds and REPLACES the current value — no
  "already set" concept at all (the opposite of `Immutable[T]`'s
  exactly-once rule). An invalid `Set` leaves the current value
  UNCHANGED (last-good-value-wins) and returns the codec's own
  validation error — the SAME error shape every other `Codec[T].Validate`
  call already produces, no new error type.
- Guarded by `sync.RWMutex` (not `sync.Mutex`) — reads (`Get`) vastly
  outnumber writes (`Set`) for this container's intended use (every
  request/message reads the current key material; reloads happen on a
  schedule measured in minutes/hours).
- `WithReloadObserver` wires in an optional Observer whose `RecordReload`
  fires on every `Set`, success or failure — the first container in this
  family with an observable lifecycle event. `codex.ReloadObserver` is
  defined LOCALLY in `codex` (`codex/observer.go`), not in `stats` —
  `codex` has zero dependency on `stats` (deliberately; `stats` depends
  on `codex`, and the reverse would form a real import cycle). Any
  `stats.Observer` CONCRETE implementation that also defines
  `RecordReload` (`stats.NoopObserver`/`LoggingObserver`/the internal
  fanout type all do) satisfies `codex.ReloadObserver` STRUCTURALLY, with
  zero import needed in either direction — `stats` exposes the same
  interface as `stats.ReloadObserver` (a type alias) plus
  `stats.AsReloadObserver(obs)` to bridge an existing `stats.Observer`
  value into this position:

  ```go
  keys, err := codex.NewMutable("jwks-signing-keys", "key-v1", codec,
      codex.WithReloadObserver[string](myObserver)) // any value satisfying RecordReload
  ```

**`Cacheable[T]`** — a 4th sibling, adding a validity WINDOW on top of
`Mutable[T]`'s re-validating `Set` shape: a TTL, an explicit
`Invalidate()` call, or both. Where `Mutable[T]` answers "what's the
current value?", `Cacheable[T]` answers "what's the current value, and
can I still trust it?" — the in-process, stale-while-revalidate
memoization shape (an expensive computation, or any `SecurityFunc`/
`CredentialFunc` result a process wants to avoid recomputing too often):

```go
memo, err := codex.NewCacheable("expensive-computation", initial, codec, time.Hour)
if err != nil {
    log.Fatal(err)
}

value, fresh := memo.Get() // never panics — same guarantee as Mutable[T].Get
if !fresh {
    // stale-while-revalidate: serve value anyway, refresh in the background
    go func() {
        if v, err := recompute(); err == nil {
            memo.Set(v)
        }
    }()
}

// An upstream event (webhook, Redis keyspace notification) can mark the
// value stale BEFORE its TTL naturally expires:
memo.Invalidate()
```

- `Get()` returns `(T, bool)` — NOT `Mutable[T]`'s plain `T` — so
  `Cacheable[T]` satisfies `codex.FreshGetter[T]` (`Get() (T, bool)`),
  a THIRD sibling to `Getter[T]`/`GetterSetter[T]`, instead of forcing
  `Cacheable[T]`'s two-value shape onto `Getter[T]`.
  `Cacheable[T]` still satisfies plain `Setter[T]` unchanged.
- `IsStale()` reports staleness without reading the value — for a
  caller that wants to decide whether to refresh before paying for a
  `Get()`.
- `Set` reuses `Mutable[T]`'s exact `codex.ReloadObserver.RecordReload`
  event unchanged. `Invalidate()` fires a SEPARATE
  `codex.InvalidateObserver.RecordInvalidate(location)` — kept separate
  from `RecordReload` because an explicit invalidation is not a failed
  reload (conflating the two would make "how many `Set` calls failed
  validation" and "how many times was this invalidated"
  indistinguishable in a dashboard). A caller observing `Mutable[T]`
  alone is never forced to implement `RecordInvalidate` — it's checked
  with its own, independent type assertion, exactly like
  `ReloadObserver`'s. `stats.InvalidateObserver`/`stats.AsInvalidateObserver`
  mirror `ReloadObserver`'s shipped shape exactly.
- Every `Mutable[T]` is functionally a `Cacheable[T]` with an infinite
  TTL and no invalidation — `Cacheable[T]` adds a validity window on top
  of `Mutable[T]`'s shape, not a separate mechanism underneath.

| | `Const[T]` | `Immutable[T]` | `Mutable[T]` | `Cacheable[T]` |
|---|---|---|---|---|
| Re-`Set`-able? | Never | Once | Unlimited | Unlimited (same as `Mutable`) |
| Invalid `Set` | Panics (`MustConst`) — or returns an error (`NewConst`) | Returns a typed/codec error | Current value UNCHANGED, returns codec error | Same as `Mutable` — last-good-value-wins |
| Notion of time/staleness? | No | No | No — a value is just "current," forever, until replaced | **Yes** — TTL and/or explicit `Invalidate()`; a value can be *present but stale* |
| `Get()` return shape | `T` | `T` (panics if unset) | `T` (never panics) | **`(T, bool)`** — value + freshness flag, never panics |
| Interface satisfied | `Getter[T]` only | `GetterSetter[T]` | `GetterSetter[T]` | **`FreshGetter[T]`** + `Setter[T]` |
| Observer | None | None | `ReloadObserver.RecordReload` on `Set` | Same `ReloadObserver` for `Set`, PLUS `InvalidateObserver.RecordInvalidate` for `Invalidate()` |
| Typical driver | Path/topic pattern constants | Config/env var loaded once at startup | Rotating security credentials — "give me whatever is current" | Stale-while-revalidate memoization — "give me what you have, tell me if it's still good" |

**Composing with adapters — `SecurityFunc`/`CredentialFunc` need ZERO new
API.** `nethttp`/`chi`'s `Options.SecurityFunc` (server) and
`CallOptions.CredentialFunc` (client) — mirrored by `mqtt`/`mqtt5`'s own
`SecurityFunc`/`CredentialFunc` — are already plain closures. A caller
captures a `*Mutable[T]`/`*Cacheable[T]` and calls `.Get()` INSIDE the
closure body, exactly like capturing any other variable — no dedicated
`SecurityFuncFromMutable`-style constructor exists, or is planned,
because one would only save a line or two over the closure itself while
adding permanent API surface. A plain static value, a hand-rolled cache,
or one of these two containers are all EQUALLY valid choices for the
SAME `SecurityFunc`/`CredentialFunc` field — nothing about the route,
channel, or builder ever knows or cares which one a caller picked:

```go
// SERVER — Mutable[T]: a background rotation loop calls keys.Set on a
// schedule; SecurityFunc always sees the CURRENT key.
opts := nethttp.Options{
    SecurityFunc: func(_ context.Context, r *http.Request, _ []route.SecurityRequirement) error {
        current := keys.Get() // ← read INSIDE the closure, on every request
        if !validSignature(r, current) {
            return errors.New("signing key mismatch")
        }
        return nil
    },
}

// CLIENT — Cacheable[T]: a TTL-bearing credential cache. The freshness
// bool from Get() must be handled explicitly — Cacheable never hides it.
credentialFunc := func(ctx context.Context, reqs []route.SecurityRequirement) (http.Header, error) {
    val, fresh := cred.Get() // ← read INSIDE the closure, on every call
    if !fresh {
        val = fetchFreshCredential(ctx) // caller's own refresh — see below
        if err := cred.Set(val); err != nil {
            return nil, err
        }
    }
    h := make(http.Header)
    h.Set("Authorization", "Bearer "+val)
    return h, nil
}
```

**The one real gotcha:** `.Get()` MUST be called inside the closure body,
on every invocation — never hoisted to a local variable at construction
time (`current := keys.Get(); opts.SecurityFunc = func(...) { ... use current ... }`).
Hoisting silently freezes the value forever, defeating the entire point
of using a live container instead of a plain static one — the closure
would keep verifying against the FIRST key forever, even after a real
rotation. See `examples/mutable-security-keys` for a full runnable demo
wiring both containers into REAL `nethttp` server/client hooks (not just
simulated closures), including the `Cacheable[T]` case's manual
fetch-on-stale + `CallOptions.OnCredentialRejected → cred.Invalidate`
wiring (mirroring `nethttp.NewCachingCredentialFunc`'s own invalidate-on-401
pattern). Fully AUTOMATING that fetch-on-stale step — so a caller never
writes the `if !fresh { ... }` branch at all — is
`ports.RefreshingCacheable[T]`'s job (`GetOrRefresh(ctx) (T, error)`), a
separate, not-yet-shipped roadmap item that COMPOSES `Cacheable[T]`
rather than replacing it.

**Constructor naming convention.** Every `Getter`/`Setter`-family type
(`Const`, `Immutable`, `Mutable`, `Cacheable`, and the planned
`OptionalMutable`) follows the same naming pattern: `MustX` validates and
PANICS (authored input, invalid = a bug); `NewX` validates and returns an
ERROR (real runtime input, invalid = an expected condition); `Set(value)
error` on a `GetterSetter[T]` type revalidates against the SAME codec on
EVERY call.
This is a documented CONVENTION, not a Go interface requirement — Go has
no static/class methods, so a literal "Factory" interface can't express
"a type that can be constructed" the way it might in other languages.
See `.github/instructions/go-codex.instructions.md`'s "Constructor Naming
Convention for `Getter`/`Setter`-Family Types" section for the full
convention (including `Maybe[T]`'s documented exception: its `Set` is
infallible by design, since it has no codec of its own).

**Why isn't every struct field validated-on-assignment like `Mutable[T]`
is?** Deliberately not: `Mutable[T]`'s mutex + method-call-only
access + per-field heap allocation would be a severe cost applied to the
overwhelming common case (a value decoded once, read many times). go-codex
already validates at the BOUNDARY (`Codec[T].Decode`, once) and trusts the
Go type system from there — the same model virtually every statically
typed program already operates under. `Mutable[T]`'s real driver is
narrow: values reassigned repeatedly, during a running process, from an
EXTERNAL source that can change without warning (rotating credentials) —
a genuinely different risk profile from an ordinary decoded field. This is
exactly why `Const`/`Immutable`/`Mutable`/`Maybe` stay OPT-IN, per-field
wrapper types rather than a blanket mechanism — apply the extra protection
selectively, where the guarantee is worth the cost.

### Applying a patch: `ApplyPatch` (flat) vs. `ApplyDottedPatch` (dotted-path)

`PartialField`/`PartialStruct` build a validated PATCH value — but say
nothing about how to APPLY it onto a base `T`. `codex.ApplyPatch` closes
that gap for the FLAT case (patch fields overwrite the SAME top-level
keys `T`'s own codec declares):

```go
type ProfilePatch struct { Nickname *string; Age *int }
var profilePatchCodec = codex.PartialStruct[ProfilePatch](...)

updated, err := codex.ApplyPatch(currentProfile, profileCodec,
    ProfilePatch{Nickname: ptr("bob")}, profilePatchCodec)
// updated.Nickname == "bob"; updated.Age unchanged.
```

`ApplyPatch` encodes both `base` and `patch` to `map[string]any` via
their own codecs, deep-merges patch over base (`codex.DeepMerge`), and
decodes the merged map back through `baseCodec` — re-running every
`Refine` constraint on the result.

**Dotted-path patches** — when a patch key reaches ARBITRARILY DEEP
(e.g. `"factory-gw.env.API_URL"`, not just a top-level field), use
`codex.ApplyDottedPatch`/`ApplyDottedPatchTo` instead:

```go
patch := map[string]any{"factory-gw.env.API_URL": "http://new"}
updated, err := codex.ApplyDottedPatchTo(base, baseCodec, patch)
```

`ApplyDottedPatch(base, patch map[string]any) map[string]any` is the raw
map-level primitive (no codec involved) — each patch key's FIRST
dotted segment names a top-level key in `base`; remaining segments (via
`codex.BuildDottedPatch`) reach deeper via `codex.DeepMerge`. When a
patch key names a top-level key `base` doesn't have yet, the patch value
is used WHOLESALE — this is what lets a dotted patch introduce an
entirely NEW entry, not just override fields inside an existing one.

**Declaring a codec for a dynamic-dotted-key wire bucket — MQTT-style
templates**: if the PATCH itself needs a wire-level codec (not just an
already-decoded `map[string]any`), and its keys are prefix + dotted-path
(module name unknown ahead of time, e.g. `deviceconfig.Patch.EdgeAgent`'s
own wire bucket), use `codex.DottedPatchMapCodec` — it declares the key
SHAPE as an MQTT-style TEMPLATE, reusing the exact `{varName}`/`+`/`#`
vocabulary MQTT topic templates already use (`"." ` as the level
delimiter instead of `"/"`):

```go
var edgeAgentPatchCodec = codex.DottedPatchMapCodec(
    "properties.desired.modules.{moduleName}.#",
    codex.KeyVarConstraint{Name: "moduleName", Constraint: validate.Slug},
)
```

- `{moduleName}` captures and validates (via the registered
  `KeyVarConstraint`) exactly one segment.
- `#` (as the LAST template segment) matches the remaining path
  WHOLESALE — zero or more further segments, left OPAQUE — matching
  `PartialStruct`'s own untyped-leaf precedent for genuinely
  dynamic-shape patches. This is what lets a device patch reach a bare
  module key, one top-level field, or an arbitrarily deep env-var path,
  all with ONE template.
- `+` matches exactly ONE anonymous segment — reach for it instead of
  `#` when the shape should NOT go deeper than one more level, e.g.
  `"{moduleName}.env.+"` accepts exactly one env var name but rejects
  anything reaching further.

Wildcards are match-ONLY — see [`DottedKeyCodec`](#dottedkeycodec--dottedpatchmapcodec-mqtt-style-dotted-key-templates)
below for the complementary "build ONE typed key from named segments, no
wildcards" case. Typed per-path leaf validation (validate a SPECIFIC
path's value against a SPECIFIC codec) remains a deferred, still-open
idea.

### `Template[T]`: the shared build+match engine underneath every `{varName}` pattern

`codex.Template[T]` is the single, canonical build+match engine for every
`"{varName}"`-style pattern in go-codex — REST paths, event/reqreply
topics, MCP resource URIs, file/dir paths, and dotted wire keys (see
`DottedKeyCodec` below, which is a one-line wrapper over it). Construct
with `codex.NewTemplate(pattern, style, fields...)`, where `style` is one
of four named values selecting the matching dialect: `PathStyle`
(`"/"`-delimited, no wildcards — REST/events/reqreply/MCP/file paths),
`MQTTStyle` (`"/"`-delimited, `+`/`#` wildcards), `DottedStyle`
(`"."`-delimited, `+`/`#` wildcards — dotted wire keys), and `GlobStyle`
(`"/"`-delimited, glob metacharacters — `ports.File`/`Dir`). `Template[T]`
embeds `Const[string]` (the raw pattern text, via `Get()`/`String()`) and
implements `HasCodec[T]`: `Codec().Encode` builds a concrete string from a
`T` value (via `EncodeVars` + substitution), `Codec().Decode` matches a
concrete string against the pattern and decodes the extracted vars into a
`T` (via `DecodeVars`). `Template[T].Build(vars T) (string, error)` is a
convenience wrapper over `Codec().Encode` for callers who don't need the
full `Codec[T]` interface. A pattern with NO declared fields (e.g.
`Template[struct{}]`) is a static, zero-variable pattern — `Encode` passes
it through unchanged, `Decode` still confirms an exact match. `api/mcp`'s
`Resource[V,T]`/`ResourceHandle[V,T]` are built directly on `Template[V]`;
`api/rest`/`api/events`/`api/reqreply`/`ports.File`/`ports.Dir` instead
share the lighter-weight `codex.Param`/`MergedParam[T]` primitive (see
above) for their own `PathParam`/`TopicParam`/`FilePathParam`/
`DirPathParam` — `Template[T]` is reached for when a boundary's vars
naturally form ONE cohesive type with no separate merge target (like
MCP's resource content), `Param`/`MergedParam[T]` when a boundary needs
BOTH a validate-only escape hatch AND an optional merge into an
independently-typed `Req`/content value.

`Template[T].Fields() []FieldCodec[T]` returns the template's own declared
fields (mirrors the existing `.Vars()`, which returns just the names) —
used by callers that need to DECOMPOSE an already-built `Template` back
into individual field declarations, e.g. `mcp.NewResourceFromTemplate`
re-declaring them as `mcp.URIParam` options to delegate to the bare-string
primary constructor (`mcp.NewResource`), the exact same "escape hatch
decomposes into the primary constructor" idiom `rest.NewRouteFromPath`/
`events.NewChannelFromTopic`/`ports.NewFileFromPathTemplate` already use
via their own public `Path.Params`/`Topic.Params`/`FilePathTemplate.Params`.

`codex.IdentityField[V any](name string, codec Codec[V]) Field[V, V]` is a
`RequiredField` with `T=F=V` and identity get/set — for any single-var
`Template[V]` where `V` IS the bare scalar type (not a wrapper struct),
sparing the call site from repeating `func(v V) V { return v }`/
`func(v *V, val V) { *v = val }` by hand:

```go
var itemResource = mcp.NewResource[string]("items://{id}", itemCodec,
    mcp.ResourceMeta{Name: "Item", MimeType: "application/json"},
    mcp.URIParam(codex.IdentityField("id", codex.String().Refine(validate.NonEmptyString))),
)
```

— matching `rest.NewRoute`/`events.NewChannel`/`ports.NewFile`/
`ports.NewDir`'s exact "bare string + opts" call shape for the common
single-var-resource case, with zero `codex.NewTemplate`/`codex.PathStyle`
ceremony needed at the call site (`NewResource` builds the `Template[V]`
internally from the declared `URIParam`s). Reach for
`mcp.NewResourceFromTemplate` (taking a pre-built `codex.Template[V]`
directly) only when reusing the SAME URI shape across multiple `Resource`
declarations of different content types.

### `DottedKeyCodec`/`DottedPatchMapCodec`: MQTT-style dotted-key templates

go-codex already has an MQTT topic-matching engine
(`internal/templatematch.MatchMQTTWildcard`). `MatchDottedWildcard`
adapts the identical algorithm with `"."` as the level delimiter — giving
dotted wire keys the SAME declarative vocabulary MQTT topics already
have. Since wildcards (`+`/`#`) only make sense for MATCHING an
already-existing key (not building exactly one new key from named
values), this splits into two constructors:

```go
// DottedKeyCodec: named-vars-ONLY template (no wildcards — panics
// otherwise). Builds ONE typed key K via the SAME DecodeVars/EncodeVars/
// FieldCodec machinery PathParam/TopicParam already use.
var moduleKeyCodec = codex.DottedKeyCodec("properties.desired.modules.{tenant}.{name}",
    codex.RequiredField("tenant", codex.String().Refine(validate.Slug),
        func(k ModuleKey) string { return k.Tenant },
        func(k *ModuleKey, v string) { k.Tenant = v }),
    codex.RequiredField("name", codex.String().Refine(validate.Slug),
        func(k ModuleKey) string { return k.Name },
        func(k *ModuleKey, v string) { k.Name = v }),
)
// Compose with Map[K,V]/EntrySlice for a fully-typed dotted-key map:
var modulesCodec = codex.Map[ModuleKey, ModuleConfig](moduleKeyCodec, moduleConfigCodec)
```

`DottedKeyCodec` generalizes the "prefix + a fixed, known number of
dotted segments → a struct key" pattern (previously hand-rolled per
package, e.g. `examples/flat-key-patch`'s former `twoPartKeyCodec`) with
a template-string API instead of `numSegments`+`build`/`split` closures —
and reuses `DecodeVars`/`EncodeVars` rather than inventing a new
mechanism. A template with exactly ONE `{var}` covers `PrefixedKeyCodec`'s
own "prefix + name" shape too — e.g. `examples/go-edge-models`'s
`manifesttemplate.ModuleNameCodec`/`RouteNameCodec`:

```go
type ModuleName string

var moduleNameField = codex.RequiredField("name", codex.String().Refine(validate.Slug),
    func(n ModuleName) string { return string(n) },
    func(n *ModuleName, s string) { *n = ModuleName(s) },
)
var ModuleNameCodec = codex.DottedKeyCodec[ModuleName](
    "properties.desired.modules.{name}", moduleNameField,
)
// Same wire shape/validation/errors PrefixedKeyCodec produces — one
// FieldCodec instead of a bare Constraint, reusing the SAME template
// mechanism the package's other (multi-segment/wildcard) dotted keys use.
```

**`PrefixedKeyCodec` stays available and is NOT removed** — it remains
the more minimal constructor for "prefix + exactly one segment, whole
rest wrapped as one named string type" when a package has no OTHER
dotted-key needs (no `FieldCodec` ceremony, just a `Constraint`). Reach
for `DottedKeyCodec`'s single-`{var}` form instead when the same package
already uses `DottedKeyCodec`/`DottedPatchMapCodec` for other keys and
you want one consistent template-based vocabulary throughout. See
`docs/guides/wire-vocabulary.md`'s decision table for the full "which one
do I need" guide across all three (`PrefixedKeyCodec`/`DottedKeyCodec`/
`DottedPatchMapCodec`).

**Rejecting a no-op patch**: `codex.IsEmptyPatch(patchCodec, patch) bool`
reports whether a `PartialStruct`-built patch encodes to an empty object
(every field unset) — generalizing the "reject a no-op patch" guard
several packages hand-roll per patch type. `codex.NonEmptyPatch(patchCodec)`
wraps it as a `Constraint[P]`; `codex.EmptyPatchError{}` (implements
`slog.LogValuer`) is the generic error for callers who don't need extra
per-patch context.

**Governed pipeline use**: `forge.Patch` wraps `ApplyPatch` as a named,
versioned, contract-hashed pipeline step — see
[Pipelines](pipelines.md#applying-a-patch-forgepatch).

## Either — typed sum type

`Either2` tries codec A first; if decode fails, tries codec B. Encode uses whichever branch is non-nil:

```go
var dsnOrConfig = codex.Either2(codex.String(), dbConfigCodec)
// Codec[codex.Either[string, DBConfig]]

left, _ := dsnOrConfig.Decode("postgres://localhost/db")
// left.Left = &"postgres://...", left.Right = nil
```

If both branches fail, returns `EitherError{Errors: []error{errA, errB}}`.

### Ergonomic constructors, methods, and free functions

Constructing an `Either` used to require a manual struct literal
(`Either[A,B]{Left: &a}`) and consuming one required a manual nil-check or
type switch (as shown in the `switch { case e.Left != nil: ... }` pattern
in `Either`'s own doc comment — still the correct escape hatch, but no
longer the ONLY option). `Left`/`Right` construct directly; `IsLeft`/
`IsRight`/`Swap` are methods (legal since they don't introduce a new type
parameter beyond the receiver's own `A`/`B`); `EitherFold`/`EitherMapLeft`/
`EitherMapRight` are free functions (each introduces a NEW type parameter
— `R`, `C`, `C` respectively — which Go generic methods cannot do, the
same constraint `Maybe[T]`'s `MaybeMap`/`MaybeFlatMap` already document):

```go
e := codex.Left[string, int]("hello")   // Either[string, int]
e.IsLeft()                              // true
e.Swap()                                // Either[int, string]{Right: &"hello"}

msg := codex.EitherFold(e,
    func(s string) string { return "text: " + s },
    func(n int) string { return "number" },
) // "text: hello"

lengths := codex.EitherMapLeft(e, func(s string) int { return len(s) })
// Either[int, int]{Left: &5} -- a Right value would pass through untouched
```

**Edge case**: a zero-value `Either[A,B]{}` (no constructor used, no
successful decode) reports `false` for BOTH `IsLeft()` and `IsRight()` —
already an implicitly invalid state per `Either`'s own "Left and Right are
mutually exclusive" contract; always construct via `Left`/`Right`/a
successful `Either2` decode. `EitherMapLeft`/`EitherMapRight` degrade
GRACEFULLY on that same zero-value input (both branches nil in, both
branches nil out — no panic, since they never need to produce a bare `R`
without calling `fn`). `EitherFold`, by contrast, PANICS (nil-pointer
dereference) on a zero-value `Either` — it MUST call `onLeft` or `onRight`
to produce its `R` result, and has no branch left to fall back to once
both `e.Left`/`e.Right` are nil.

No new structured error types and no observer integration are needed:
these are pure in-memory combinators with no I/O — any failure mode
belongs to the caller's own `fn`/`onLeft`/`onRight` closures, not to
`codex` itself.

### `EitherField` — the Struct-field convenience for `Either`

`Either2(ca, cb)` derives a plain `Codec[Either[A,B]]` — usable directly
with `RequiredField`/`OptionalField`/`DefaultField` today, but every call
site has to spell out `Either2(ca, cb)` itself. `EitherField` is a
one-line convenience matching `MaybeField`'s own call-site shape (name +
codec(s) + get/set → `FieldCodec[T]`), literal sugar for
`RequiredField(name, Either2(ca, cb), get, set)`:

```go
var serviceCodec = codex.Struct[Service](
    codex.EitherField("port", codex.String(), codex.Int(),
        func(s Service) codex.Either[string, int] { return s.Port },
        func(s *Service, v codex.Either[string, int]) { s.Port = v }),
)
```

**Always `Required`, unlike `MaybeField`** — a valid `Either` always holds
EXACTLY one of `Left`/`Right` (see `Either`'s own "mutually exclusive"
contract), so there is no natural "absent" state for `EitherField` to make
optional the way `Maybe`'s `Nothing` is. For an OPTIONAL `Either` field
(may be absent, OR `Left`, OR `Right`), compose `MaybeField` with
`Either2` directly instead: `MaybeField(name, Either2(ca, cb), get, set)`
— a `Maybe[Either[A,B]]` field.

**How does `EitherField` render in Schema?** As `Either2`'s own `{oneOf:
[...]}` shape, AND present in the Struct's `Required` array — verified by
an actual render:

```go
codex.EitherField("value", codex.String(), codex.Int(), get, set)
// Schema -> {"OneOf": [{"Type": "string"}, {"Type": "integer"}]}
// AND "value" appears in the Struct's "Required" list.
```

Unlike `MaybeField` (above), `EitherField`'s schema genuinely DOES differ
visibly from what a plain scalar field's schema would look like — the
`oneOf` two-branch shape is real, wire-level information a schema
consumer (OpenAPI/AsyncAPI/JSON Schema tooling) needs to validate a
document correctly, not a Go-side-only distinction the way `Maybe[T]`'s
presence-tracking is.

This completes the full 2×2 grid of `Maybe`/`Either` field-declaration
tools:

| | Plain codec constructor | Struct-field convenience |
|---|---|---|
| **`Maybe`** | `MaybeCodec[T](inner)` | `MaybeField(name, codec, get, set)` |
| **`Either`** | `Either2(ca, cb)` | `EitherField(name, ca, cb, get, set)` |

Both rows compose freely with every OTHER field constructor too
(`OptionalField`/`DefaultField`/`OmitEmptyFieldFunc`) via their LEFT
column's plain codec — the right column is purely a named shortcut for
the single most common shape, not a separate mechanism.

### `StringOrInt64` and family — the "string or number" convenience

A config/env-style value that may be EITHER a string OR a number is a genuinely common wire
pattern — Docker/IoT-Edge module env vars (`"5"` vs `5`), Kubernetes' `apimachinery`
`IntOrString`, Terraform/HCL, Helm `values.yaml`. `codex.StringOrInt64()` (and its siblings
`StringOrInt`/`StringOrInt32`/`StringOrUint`/`StringOrUint64`/`StringOrFloat32`/`StringOrFloat64`,
one per numeric primitive) is a one-line named convenience over exactly the `Either2` pattern
above:

```go
func StringOrInt64() Codec[Either[string, int64]] { return Either2(String(), Int64()) }

var EnvVarCodec = codex.Struct[EnvVar](
    codex.RequiredField("value", codex.StringOrInt64(),
        func(e EnvVar) codex.Either[string, int64] { return e.Value },
        func(e *EnvVar, v codex.Either[string, int64]) { e.Value = v },
    ),
)
```

**Format-agnostic by construction**: `encoding/json` always decodes a bare JSON number into
`any` as `float64`; `yaml.v3` decodes a YAML integer as a native Go `int` (and a float as
`float64`); `BurntSushi/toml` decodes a TOML integer as `int64` (and a float as `float64`).
Every numeric primitive (`Int`/`Int32`/`Int64`/`Uint`/`Uint64`/`Float32`/`Float64`) already
type-switches over ALL of these native representations in its own `Decode` — so the SAME
`StringOrInt64()` codec works correctly whether the surrounding document is JSON, YAML, or
TOML, with zero format-specific handling.

A plain custom `Constraint[T]` cannot express this: it validates one FIXED type `T` after a
successful `Decode` into `T` — there is no `T` for which `Check` could ever see the "other"
type's raw value. The type-level choice has to happen in the codec itself, which is exactly
what `Either2`/`UntaggedUnion` provide.

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

### PrefixedKeyCodec — a prefix + validated-name-segment convenience

A recurring shape built on `MapCodecValidated` (see "Recommended key codec" above): a wire key is a fixed PREFIX followed by a validated NAME segment — `"properties.desired.modules.cv-writer"`, `"user:42"`, `"tenant-acme.cv-writer-kvrocks"`. `PrefixedKeyCodec[B ~string]` generalizes the two-layer recipe (full-key shape via an internal `Constraint`, name segment via the caller's own `Constraint`) into one constructor:

```go
type UserKey string

var UserKeyCodec = codex.PrefixedKeyCodec[UserKey](
    "user:", validate.Slug,
)

key, _ := UserKeyCodec.Encode(UserKey("42"))
// key == "user:42"

name, _ := UserKeyCodec.Decode("user:42")
// name == UserKey("42")
```

`B` can be a bare `string` (e.g. `examples/flat-key-patch`'s `containerKeyCodec`) or a named string type. Reach for `PrefixedKeyCodec` whenever the key shape is EXACTLY "prefix + name" and you want the MINIMAL constructor for it — no `FieldCodec`/template ceremony. `DottedKeyCodec` (above) covers this SAME single-segment case too (see `examples/go-edge-models`'s `manifesttemplate.ModuleNameCodec`/`RouteNameCodec`, both built on `DottedKeyCodec` over a one-`{var}` template) via the more general MQTT-style template mechanism — reach for `DottedKeyCodec` instead when a package already uses it for OTHER multi-segment/wildcard keys and you want one consistent vocabulary, or when the key shape might grow beyond one segment later. A key with a different internal structure (e.g. two segments joined by a delimiter, like `<tenant>.<name>`) needs `DottedKeyCodec`'s multi-`{var}` template (see "MQTT-style dotted-key templates" above) — `examples/flat-key-patch`'s `twoPartKeyCodec` is built on it.

See [Guide: Wire-format vocabulary](../guides/wire-vocabulary.md) for how this fits into a package's single-source-of-truth `keys.go`.

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
| `validate.SemVerLike` | version-shaped but not strict semver (e.g. `3.1-debian`, `18.04`) — overlaps SemVer's grammar for the common case, but not build metadata; check SemVer first when classifying | `pattern` |
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
