# Type Modeling — Enums, Unions, Sum Types

> See also: [`codex` package on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/codex)
>
> Runnable demo: [`examples/enum-union-sum`](https://github.com/DaniDeer/go-codex/tree/main/examples/enum-union-sum)

Go has three distinct ways to model "a value that can be one of several things." go-codex supports all three with different codec primitives. Choosing the right one depends on two questions:

1. **Is the set of types fixed at compile time (closed) or open?**
2. **Do the variants carry different data, or are they just labels?**

## Decision guide

| Pattern | Set | Variants carry data? | Codec primitive | Schema |
|---|---|---|---|---|
| **Iota enum** (string wire) | Closed | No — label only | `MapCodecSafe` + `validate.OneOf` | `{enum: [...]}` |
| **Iota enum** (integer wire) | Closed | No — label only | `MapCodecSafe` + `validate.RangeInt` | `{minimum, maximum}` |
| **Open union** | Open | Any | `codex.Any()` | `{}` |
| **Sum type — tagged** | Closed | Yes — different shapes | `codex.TaggedUnion[T]` | `{oneOf, discriminator}` |
| **Sum type — untagged** | Closed | Yes — decode by shape | `codex.UntaggedUnion[T]` | `{oneOf}` |
| **Sum type — binary** | Closed | Yes — two branches | `codex.Either2(ca, cb)` | `{oneOf}` |

**Rule of thumb:**
- All variants are just labels with no payload → **iota enum**
- You don't control all types at compile time → **`codex.Any()`**
- Variants carry different data, wire has a discriminator field → **`TaggedUnion`**
- Variants carry different data, decode by shape alone → **`UntaggedUnion`**
- Exactly two types, one tried before the other → **`Either2`**

---

## 1. Iota enum — string wire encoding

Go has no `enum` keyword. The idiomatic substitute is a named integer type with `iota` constants. Bridge it to go-codex via `MapCodecSafe`:

```go
type OrderStatus int

const (
    Pending   OrderStatus = iota
    Approved
    Shipped
    Cancelled
)

var OrderStatusCodec = codex.MapCodecSafe(
    // Wire: string with OneOf validation.
    // Schema: {type: string, enum: ["pending", "approved", "shipped", "cancelled"]}
    codex.String().Refine(validate.OneOf("pending", "approved", "shipped", "cancelled")),
    func(s string) OrderStatus { return byName[s] },          // decode direction (total)
    func(s OrderStatus) (string, error) { return names[s], nil }, // encode direction
)
```

**When to use:** REST APIs, event payloads, YAML config — anywhere human-readable labels matter and the schema should document the valid values as an enum.

**Trade-off vs integer encoding:** String encoding produces `{enum: ["pending", ...]}` in OpenAPI/AsyncAPI, which tooling (Swagger UI, client generators) renders as a dropdown. Integer encoding only produces `{minimum: 0, maximum: 3}` — loses variant names in the spec.

## 2. Iota enum — integer wire encoding

When the integer representation matters (binary protocol, database column):

```go
var OrderStatusIntCodec = codex.MapCodecSafe(
    codex.Int().Refine(validate.RangeInt(0, 3)),
    func(n int) OrderStatus { return OrderStatus(n) },
    func(s OrderStatus) (int, error) { return int(s), nil },
)
// Schema: {type: integer, minimum: 0, maximum: 3}
```

## 3. Open union — codex.Any()

When the set of possible types is open or caller-controlled (dynamic config, JSON blobs, expression evaluators):

```go
var DynamicValueCodec = codex.Any()
// Schema: {} — accepts anything, no type constraint
```

`codex.Any()` is the correct choice when:
- Different callers may pass different types
- The value is forwarded transparently without processing
- No schema annotation is needed or possible

## 4. Sum type — TaggedUnion (sealed interface)

A sum type is a closed set of variants where each variant carries **different data**. Go's approximation is a sealed interface (unexported method):

```go
type PaymentStatus interface {
    paymentStatusTag() // unexported — external packages cannot add variants
}

type PendingPayment   struct{}
type CompletedPayment struct{ TxID string }
type FailedPayment    struct{ Reason string }

// Each implements paymentStatusTag()
```

Bridge each variant to the interface via `MapCodecSafe`, then compose with `TaggedUnion`:

```go
var PaymentStatusCodec = codex.TaggedUnion[PaymentStatus](
    "status",  // discriminator field name in JSON
    map[string]codex.Codec[PaymentStatus]{
        "pending":   pendingCodec,
        "completed": completedCodec,
        "failed":    failedCodec,
    },
    func(p PaymentStatus) (string, error) {
        switch p.(type) {
        case PendingPayment:   return "pending", nil
        case CompletedPayment: return "completed", nil
        case FailedPayment:    return "failed", nil
        default: return "", fmt.Errorf("unknown: %T", p)
        }
    },
)
// Wire:   {"status":"completed","tx_id":"abc123"}
// Schema: {oneOf:[...], discriminator:{propertyName:"status", mapping:{...}}}
```

**When to use:** Domain state machines, event types, AST nodes — any closed set where variants carry structurally different data and the wire format can include a discriminator field.

## 5. Sum type — UntaggedUnion (no discriminator)

When the wire format has no discriminator field — decode tries each variant in order, first match wins:

```go
var shapeCodec = codex.UntaggedUnion[Geometry](
    func(g Geometry) int {
        switch g.(type) {
        case Rectangle: return 0
        case Square:    return 1
        }
        return -1
    },
    codex.UntaggedVariant[Geometry]{Name: "rectangle", Codec: rectGeoCodec},
    codex.UntaggedVariant[Geometry]{Name: "square",    Codec: squareGeoCodec},
)
// Schema: {oneOf: [{...rectangle...}, {...square...}]}
```

**When to use:** When the wire format is defined externally (no discriminator field), or when each variant's shape is unique enough for first-match to be reliable.

## 6. Sum type — Either2 (binary)

The simplest sum type — exactly two branches:

```go
// A field that is either a plain SKU string OR an inline ProductRef object.
var SkuOrInline = codex.Either2(
    codex.String().Refine(validate.NonEmptyString), // Left branch — tried first
    productRefCodec,                                // Right branch — fallback
)
// Wire:   "SKU-123"  OR  {"sku":"SKU-123"}
// Schema: {oneOf: [{type:string,minLength:1}, {type:object,...}]}
```

**When to use:** When exactly two types can appear in a field and their wire representations are unambiguous enough for ordered matching. Classic example: "reference by name OR inline definition."

## Relation to Pure and Eq

`codex.Pure(value)` and `codex.Eq(base, value)` are special cases of single-variant "enums":

```go
// Pure: always decodes to "1.0", always encodes "1.0" — ignores wire value.
codex.Pure("1.0")
// Schema: {enum: ["1.0"]}

// Eq: decodes using base codec, then rejects anything that isn't exactly "com.example.order".
codex.Eq(codex.String(), "com.example.order")
// Schema: base schema + {enum: ["com.example.order"]}
```

Use `Pure` for protocol version fields or derived constants. Use `Eq` when the wire codec handles type conversion but only one specific value is acceptable.

## See also

- [Concept: Codec — declare once](codec.md) — `MapCodecSafe`, `MapCodecValidated`, full codec reference
- [Guide: Enums, Unions & Sum Types](../guides/enum-union-sum.md) — example walkthrough
- [examples/shape](https://github.com/DaniDeer/go-codex/tree/main/examples/shape) — `TaggedUnion`, `UntaggedUnion`, `Either2`, `Downcast`
- [examples/event-driven](https://github.com/DaniDeer/go-codex/tree/main/examples/event-driven) — `Pure` + `Eq` with CloudEvents
- [examples/enum-union-sum](https://github.com/DaniDeer/go-codex/tree/main/examples/enum-union-sum) — all patterns in one file with decision guide
