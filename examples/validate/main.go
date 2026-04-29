// Package main shows how to use Codec.Validate and Format.Validate for
// explicit bidirectional validation.
//
// By design, go-codex only validates in the decode direction — constraints
// guard external input (JSON, YAML, TOML) that you don't control.
// The encode direction is trusted: you constructed the value yourself.
//
// When you need to validate a Go value you constructed (e.g. before storing it,
// or after building it programmatically), call Validate explicitly.
// It reuses the exact same Refine constraints — no duplication needed.
package main

import (
	"fmt"
	"regexp"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/validate"
)

type User struct {
	Name  string
	Email string
	Age   int
}

var emailPattern = regexp.MustCompile(`^[^@]+@[^@]+\.[^@]+$`)

var userCodec = codex.Struct[User](
	codex.Field[User, string]{
		Name:     "name",
		Codec:    codex.String().Refine(validate.NonEmptyString),
		Get:      func(u User) string { return u.Name },
		Set:      func(u *User, v string) { u.Name = v },
		Required: true,
	},
	codex.Field[User, string]{
		Name:     "email",
		Codec:    codex.String().Refine(validate.Pattern(emailPattern)),
		Get:      func(u User) string { return u.Email },
		Set:      func(u *User, v string) { u.Email = v },
		Required: true,
	},
	codex.Field[User, int]{
		Name:     "age",
		Codec:    codex.Int().Refine(validate.PositiveInt),
		Get:      func(u User) int { return u.Age },
		Set:      func(u *User, v int) { u.Age = v },
		Required: true,
	},
)

func main() {
	validUser := User{Name: "Alice", Email: "alice@example.com", Age: 30}
	invalidUser := User{Name: "", Email: "not-an-email", Age: -5}

	// ── Codec.Validate ────────────────────────────────────────────────────────
	// Use the codec directly — no format involved.

	fmt.Println("=== Codec.Validate ===")

	if err := userCodec.Validate(validUser); err != nil {
		fmt.Println("valid user failed:", err)
	} else {
		fmt.Println("valid user:   ok")
	}

	if err := userCodec.Validate(invalidUser); err != nil {
		fmt.Println("invalid user:", err)
	}

	// ── Format.Validate ───────────────────────────────────────────────────────
	// Same constraints, but accessed through a format binding.
	// Useful when you already have a Format[T] in scope.

	fmt.Println("\n=== Format.Validate ===")

	jsonFmt := format.JSON(userCodec)

	if err := jsonFmt.Validate(validUser); err != nil {
		fmt.Println("valid user failed:", err)
	} else {
		fmt.Println("valid user:   ok")
	}

	if err := jsonFmt.Validate(invalidUser); err != nil {
		fmt.Println("invalid user:", err)
	}

	// ── Marshal is NOT constrained ────────────────────────────────────────────
	// The encode direction is trusted. Marshal succeeds even for invalid values.
	// Validate explicitly before marshaling when you need the guarantee.

	fmt.Println("\n=== Marshal without Validate (trusted encode) ===")
	out, err := jsonFmt.Marshal(invalidUser)
	if err != nil {
		fmt.Println("marshal error:", err)
	} else {
		fmt.Printf("marshaled: %s\n", out)
	}

	fmt.Println("\n=== Validate before Marshal (explicit guard) ===")
	if err := jsonFmt.Validate(invalidUser); err != nil {
		fmt.Println("validation failed, skipping marshal:", err)
	} else {
		out, _ = jsonFmt.Marshal(invalidUser)
		fmt.Printf("marshaled: %s\n", out)
	}
}
