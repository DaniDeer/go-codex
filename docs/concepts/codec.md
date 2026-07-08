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
