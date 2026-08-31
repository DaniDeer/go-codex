# Codec as Domain Boundary

A codec is not just a validator — it is the **public contract** of a module boundary. Every boundary in a system — HTTP request, database schema, HTTP response, MQTT payload — can be modelled as a codec. Constraints defined on shared field codecs propagate to all boundaries that reference them: one definition, zero duplication.

## The three-layer architecture

```
┌─────────────────────────────────────────────────────────────────────┐
│  LAYER 1 — DOMAIN MODELS + CONSTRAINTS                              │
│                                                                     │
│  emailFieldCodec  = codex.String().Refine(validate.Email)           │
│  nameFieldCodec   = codex.String().Refine(validate.NonEmptyString)  │
│                                         ↑                           │
│  createUserReqCodec  Codec[CreateUserReq]  │ shared field codecs    │
│  userRecordCodec     Codec[UserRecord]     ┤ defined once           │
│  userCodec           Codec[User]           ┘ used in all three      │
├─────────────────────────────────────────────────────────────────────┤
│  LAYER 2 — BUSINESS LOGIC (pure domain functions, zero IO)          │
│                                                                     │
│  buildUserRecord(CreateUserReq) UserRecord                          │
│  buildUserResponse(UserRecord) User                                 │
│    ← no database, no HTTP, no side effects                          │
│    ← independently unit-testable with plain Go structs              │
├─────────────────────────────────────────────────────────────────────┤
│  LAYER 3 — INFRASTRUCTURE (HTTP + database + external services)     │
│                                                                     │
│  UserStore — uses userRecordCodec.Encode/Decode for all DB IO       │
│  makeCreateUserHandler(store) — orchestrates L2 + L3               │
│  nethttp.Serve(mux, builder) — the only HTTP line                   │
│  b.OpenAPISpec()               — the only OpenAPI line              │
│    ← swap to gRPC, CLI, or test without touching L1 or L2           │
└─────────────────────────────────────────────────────────────────────┘
```

Layer 1 owns all domain rules. Layer 2 transforms between domain types with zero IO. Layer 3 orchestrates all IO but contains no business logic.

## The pipeline for POST /users

```
Codec[Req] ─ decode ─▶ CreateUserReq ─▶ buildUserRecord ─▶ UserRecord
                                                               ↓ store.Save (Codec[UserRecord].Encode)
Codec[Resp] ─ encode ─▶ User ◀─ buildUserResponse ◀─ UserRecord
```

## Shared field codecs

Define each domain constraint **once** and compose it into multiple boundary codecs. The constraint propagates automatically — no copy-paste, no drift:

```go
// Define once at Layer 1.
var emailFieldCodec = codex.String().Refine(validate.Email).WithDescription("Email address.")
var nameFieldCodec  = codex.String().Refine(validate.NonEmptyString).WithDescription("Display name.")

// Reuse in all three boundary codecs — same constraint, zero duplication.
var createUserReqCodec = codex.Struct[CreateUserReq](
    codex.RequiredField("name",  nameFieldCodec,  ...),
    codex.RequiredField("email", emailFieldCodec, ...),
)
var userRecordCodec = codex.Struct[UserRecord](
    codex.RequiredField("name",  nameFieldCodec,  ...),
    codex.RequiredField("email", emailFieldCodec, ...),
)
var userCodec = codex.Struct[User](
    codex.OptionalField("name",  nameFieldCodec,  ...),
    codex.OptionalField("email", emailFieldCodec, ...),
)
```

Rename `emailFieldCodec` to use `.WithDescription("Contact email.")` and all three boundary codecs update automatically — without touching the struct definitions or the HTTP layer.

## Database store uses the codec for all IO

The store is not special — it uses the same codec mechanism as the HTTP layer. Schema definition and serialization are the same object:

```go
// Save encodes the record using userRecordCodec (analogous to SQL INSERT).
func (s *UserStore) Save(r UserRecord) error {
    encoded, _ := userRecordCodec.Encode(r)   // → map[string]any, like a SQL row
    s.rows[r.ID] = encoded.(map[string]any)
    return nil
}

// Get decodes and validates on read (analogous to SQL scan).
func (s *UserStore) Get(id string) (UserRecord, bool) {
    row := s.rows[id]
    record, _ := userRecordCodec.Decode(row)  // constraints enforced on read
    return record, true
}
```

## Pure domain functions are independently testable

Layer 2 functions have no IO — no HTTP server, no database, no mock required:

```go
func TestBuildUserRecord(t *testing.T) {
    req := CreateUserReq{Name: "Alice", Email: "alice@example.com"}
    record := buildUserRecord(req) // Layer 2 called directly — zero setup
    // assert on record fields
}
```

## Swappable infrastructure

Because all business logic lives in Layer 2, you can swap the entire Layer 3 implementation — from HTTP to gRPC, from Paho MQTT to a different broker, from a real database to an in-memory store — without touching Layer 1 or Layer 2.

## MapCodecSafe vs this pattern

`MapCodecSafe`/`MapCodecValidated` are designed for **same-wire bidirectional type mappings** (newtypes, DSN strings — `float64` ↔ `Celsius`). They produce a single `Codec[B]` where encode and decode use the same wire format.

The three-layer pattern above is for **cross-boundary mappings** (HTTP request → database row → HTTP response) where the two wire formats deliberately differ. Use codecs per boundary; use Layer 2 functions to transform between domain types.

## See also

- [Concept: Codec — declare once](codec.md) — `Codec[T]`, `RequiredField`, `OptionalField`, shared field codecs
- [Feature: REST API & HTTP Adapters](../features/rest-api.md) — three-layer pipeline in HTTP context
- [Feature: Event Channels & MQTT](../features/events.md) — same pattern for event-driven systems
- [examples/adapters-nethttp](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-nethttp) — full three-layer pipeline demo with `UserStore`, pure domain functions, and HTTP adapter
- [examples/adapters-mqtt](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-mqtt) — three-layer pipeline for MQTT (MeasurementEvent → TSDB → AlertEvent)
