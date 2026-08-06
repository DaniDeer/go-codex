# Get Started

## Installation

```bash
go get github.com/DaniDeer/go-codex
```

**Go version required:** 1.26+

## Hello World codec

Define a codec once — it encodes, decodes, validates, and generates a JSON Schema automatically.

```go
package main

import (
    "fmt"
    "github.com/DaniDeer/go-codex/codex"
    "github.com/DaniDeer/go-codex/format"
    "github.com/DaniDeer/go-codex/validate"
)

type User struct {
    Name  string
    Email string
}

var UserCodec = codex.Struct[User](
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

func main() {
    json := format.JSON(UserCodec)

    // Encode
    data, _ := json.Marshal(User{Name: "Alice", Email: "alice@example.com"})
    fmt.Println(string(data))
    // {"email":"alice@example.com","name":"Alice"}

    // Decode + validate
    user, err := json.Unmarshal([]byte(`{"name":"","email":"not-an-email"}`))
    fmt.Println(err)
    // validation errors: [name: expected non-empty string] [email: invalid email: "not-an-email"]
    _ = user
}
```

Each `RequiredField` call takes two small closures after the codec — a
**getter** (`func(u User) string { return u.Name }`) and a **setter**
(`func(u *User, v string) { u.Name = v }`). These are how `Struct[T]`
reads and writes that one field on your struct: Go's type system has no
reflection-free way to do this automatically, so the closures are how you
tell the codec where the value lives. You write them once per field, not
per encode/decode call — see [Codec concepts](concepts/codec.md#struct-codecs)
for how to share them across multiple structs with the same field.

## The three layers

The codec above is **Layer 1** — it works standalone, with no other go-codex
package involved. go-codex grows with your system from there; use only what
you need:

| Layer | Packages | What you get |
|-------|---------|-------------|
| **1 — Codec** | `codex`, `format`, `validate`, `schema` | Encode, decode, validate, schema — what you just saw above |
| **2 — API contract** | `api/rest`, `api/events`, `api/reqreply`, `api/mcp` | Typed routes/channels/tools + generated OpenAPI/AsyncAPI/MCP spec |
| **3 — Application foundation** | `ports`, `app`, `stream`, `forge`, `adapters/*` | Protocol-agnostic IO boundaries, supervised lifecycle, governed pipelines, transport bindings |

See the [README's "three layers" section](../README.md#the-three-layers) for
a side-by-side code sample of all three, or jump straight into a guide below.

## Next steps

- [Concepts: Codec](concepts/codec.md) — understand `Codec[T]` deeply
- [Guide: HTTP Server](guides/http-server.md) — build a typed REST API
- [Guide: HTTP Client](guides/http-client.md) — call APIs with full codec validation
- [Guide: MQTT Events](guides/mqtt.md) — publish and subscribe typed events
- [Guide: Ports](guides/ports.md) — declare protocol-agnostic IO boundaries with zero transport imports in domain code
