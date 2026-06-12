# Get Started

## Installation

```bash
go get github.com/DaniDeer/go-codex
```

**Go version required:** 1.21+

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

## Next steps

- [Concepts: Codec](concepts/codec.md) — understand `Codec[T]` deeply
- [Guide: HTTP Server](guides/http-server.md) — build a typed REST API
- [Guide: HTTP Client](guides/http-client.md) — call APIs with full codec validation
- [Guide: MQTT Events](guides/mqtt.md) — publish and subscribe typed events
