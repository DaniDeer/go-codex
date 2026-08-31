# What is go-codex?

In standard Go, encoding, decoding, validation, and documentation are separate concerns that drift apart.
Rename a field and you must update struct tags, the validator, and the schema docs independently — one missed update causes a silent bug or a stale spec.

go-codex is inspired by Haskell's [autodocodec](https://hackage.haskell.org/package/autodocodec).
A single `Codec[T]` value is the source of truth for **encode**, **decode**, **validation**, and **schema** — written once, never duplicated.

## The Problem

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

Rename `Name` to `DisplayName`: you must update the struct tag, the validator, and the YAML. Miss any one of them and the bug is silent until production.

## The Solution

```go
// One Codec[User] is encode + decode + validate + schema.
type User struct {
    Name string
    Age  int
}

var UserCodec = codex.Struct[User](
    codex.RequiredField("name",
        codex.String().Refine(validate.NonEmptyString),
        func(u User) string { return u.Name },
        func(u *User, v string) { u.Name = v },
    ),
    codex.RequiredField("age",
        codex.Int().Refine(validate.PositiveInt),
        func(u User) int { return u.Age },
        func(u *User, v int) { u.Age = v },
    ),
)

// Decode and validate in one step — error includes field path and constraint name.
user, err := UserCodec.Decode(map[string]any{"name": "Alice", "age": 30})

// Encode back to the intermediate representation.
data, err := UserCodec.Encode(user)

// Schema derived automatically — no separate YAML needed.
schemaJSON, _ := json.MarshalIndent(UserCodec.Schema, "", "  ")
```

Rename `Name` to `DisplayName`: change one `"name"` string in the codec. The struct tag, the validator, and the schema all update automatically — nothing to forget.

## Shared Contract

go-codex codecs are plain Go values — they live in a shared package and can be imported by any number of services.

```
pkg/contract/
    user.go  ←  UserCodec, CreateUserRequestCodec, ...
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

A field rename in `contract.User` breaks compilation on both sides immediately — no stale YAML, no schema drift, no separate code-generation step. **The Go source is the contract.**

## Three Layers

go-codex grows with your system. Each layer is independent — use what you need:

| Layer | Package | Role |
|---|---|---|
| **1 — Codecs** | `codex` | Encode, decode, validate, schema — one value, no duplication |
| **2 — API contracts** | `api/rest`, `api/events`, `api/mcp` | Declarative route and channel specs; generate OpenAPI 3.1 and AsyncAPI 3.0 |
| **3 — Pipelines** | `forge` | Named, versioned, governed computation functions; generate a signed pipeline spec |

All three follow the same declarative pattern: **define as a value, register separately**.

```go
// Layer 1 — codec: plain value, used everywhere
var userCodec = codex.Struct[User](
    codex.RequiredField("name", codex.String().Refine(validate.NonEmptyString), ...),
    codex.RequiredField("email", codex.String().Refine(validate.Email), ...),
)
user, err := userCodec.Decode(raw) // validate + decode in one step

// Layer 2 — route: declare as value, register with builder → typed handle + OpenAPI spec
var createUser = rest.NewRoute[CreateUserReq, User]("POST", "/users", reqCodec, userCodec,
    rest.RouteMeta{OperationID: "createUser", Summary: "Create a user"},
)
handle, _ := createUser.RegisterHandle(b)
req, _    := handle.Decode(body)

// Layer 2 — channel: same pattern for events → typed handle + AsyncAPI spec
var sensorCh = events.NewChannel[SensorReading]("sensors/{id}/readings", sensorCodec,
    events.Subscribe{Summary: "Receive sensor readings"},
)
handle, _ = sensorCh.Register(b)
reading, _ := handle.Decode(payload)

// Layer 3 — pipeline: declare a governed computation, use directly, register for spec
availCalc := forge.NewFunction[AvailabilityIn, Availability]("availCalc", "1.0.0",
    availInCodec, availabilityCodec,
    computeAvailability,
    forge.FunctionMeta{Author: "OT Engineering"},
)
result, _ := availCalc.Apply(in)  // validate → compute → validate output
```

## Next steps

- [Get Started](get-started.md) — 5-minute hands-on intro
- [Concept: Codec — declare once](concepts/codec.md) — deep dive into `Codec[T]`
- [Feature: REST API & HTTP Adapters](features/rest-api.md) — Layer 2 in action
- [Concept: Forge Pipelines](concepts/pipelines.md) — Layer 3 in action
