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

   **Bonus — `StringOrInt64` and family**: the common special case of `Either2` where the two branches are a string and a number (e.g. Docker/IoT-Edge env var values `"5"` vs `5`, Kubernetes `IntOrString`) has a named one-line convenience: `codex.StringOrInt64()` (plus `StringOrInt`/`StringOrInt32`/`StringOrUint`/`StringOrUint64`/`StringOrFloat32`/`StringOrFloat64`). It's exactly `Either2(String(), Int64())` — verified format-agnostic across JSON/YAML/TOML since every numeric primitive already normalizes each format library's native number representation (`float64` for JSON, `int`/`float64` for YAML, `int64`/`float64` for TOML) in its own `Decode`.

→ [examples/enum-union-sum](https://github.com/DaniDeer/go-codex/tree/main/examples/enum-union-sum)
