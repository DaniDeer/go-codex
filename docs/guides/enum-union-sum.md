# Guide: Enums, Union Types & Sum Types

This guide walks through the `examples/enum-union-sum` example.

**Concept:** [Type Modeling — Enums, Unions, Sum Types](../concepts/type-modeling.md)

## examples/enum-union-sum

Demonstrates all three Go type-modeling patterns side-by-side, with commentary on when to use each codec primitive:

1. **Iota enum (string wire)** — `type OrderStatus int` with `iota` constants. Bridged to string via `MapCodecSafe` + `validate.OneOf`. Schema: `{enum: ["pending", "approved", ...]}`.

2. **Iota enum (integer wire)** — Same named type, bridged to `int` via `MapCodecSafe` + `validate.RangeInt`. Schema: `{minimum: 0, maximum: 3}`. Use when integer representation matters on the wire (binary protocols, DB columns).

3. **Open union (`codex.Any()`)** — For dynamic config values or JSON blobs where the type set is open. Passes through unchanged with empty schema `{}`.

4. **Sum type (`TaggedUnion`)** — `PaymentStatus` sealed interface with `Pending`, `Completed{TxID}`, `Failed{Reason}`. Discriminated by `"status"` field. Schema: `{oneOf:[...], discriminator:{propertyName:"status"}}`.

5. **Binary sum type (`Either2`)** — A product field that is either a plain SKU string OR an inline ProductRef object. Left branch tried first; right branch as fallback. Schema: `{oneOf:[string, object]}`.

→ [examples/enum-union-sum](https://github.com/DaniDeer/go-codex/tree/main/examples/enum-union-sum)
