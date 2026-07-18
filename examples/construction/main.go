package main

import (
	"fmt"
	"regexp"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/validate"
)

// ── Domain types ──────────────────────────────────────────────────────────────

type Username string
type Score int

// ── Codecs ────────────────────────────────────────────────────────────────────

var usernameCodec = codex.MapCodecSafe(
	codex.String().
		Refine(validate.MinLen(3)).
		Refine(validate.MaxLen(20)).
		Refine(validate.Pattern(regexp.MustCompile(`^[a-z0-9_]+$`))),
	func(s string) Username { return Username(s) },
	func(u Username) (string, error) { return string(u), nil },
)

var scoreCodec = codex.MapCodecSafe(
	codex.Int().Refine(validate.RangeInt(0, 100)),
	func(n int) Score { return Score(n) },
	func(s Score) (int, error) { return int(s), nil },
)

// Profile is a multi-field struct — the case where a "named constructor" is
// most useful: callers want NewProfile(handle, score) instead of a struct
// literal, but still get full codec validation on every field.
type Profile struct {
	Handle Username
	Score  Score
}

var profileCodec = codex.Struct[Profile](
	codex.RequiredField("handle", usernameCodec,
		func(p Profile) Username { return p.Handle },
		func(p *Profile, v Username) { p.Handle = v }),
	codex.RequiredField("score", scoreCodec,
		func(p Profile) Score { return p.Score },
		func(p *Profile, v Score) { p.Score = v }),
)

// NewProfile is a named per-field constructor derived from profileCodec.
//
// go-codex has no reflection and no struct-tag codec wiring (all wiring is
// explicit Go code), and Go generics cannot express a variadic-arity
// "derive NewT(field1, field2, ...) automatically from any Codec[T]" helper.
// So the idiomatic pattern is this thin hand-written wrapper: it takes
// positional field values and delegates to Codec.New, which still runs the
// full codec validation (both fields' Refine constraints) before returning.
func NewProfile(handle Username, score Score) (Profile, error) {
	return profileCodec.New(Profile{Handle: handle, Score: score})
}

// ── Package-level validated constants ────────────────────────────────────────

// Must panics at init time if the value doesn't satisfy the codec constraints.
// Use for constants that are known-valid at compile time.
var guestUser = codex.Must(usernameCodec.New(Username("guest")))

// Must also works with Decode — useful for seeding known-good wire data at startup.
// The map literal is the "wire" representation (what you'd get from JSON/YAML).
var defaultScore = codex.Must(scoreCodec.Decode(75))

// reservedUsernames is a validated allow-list built at init time.
// If any entry violates the codec, the program fails immediately rather than
// silently accepting an invalid name that only surfaces later at runtime.
var reservedUsernames = func() []Username {
	raw := []string{"admin", "root", "system", "support"}
	names := make([]Username, len(raw))
	for i, r := range raw {
		names[i] = codex.Must(usernameCodec.New(Username(r)))
	}
	return names
}()

// ── Main ──────────────────────────────────────────────────────────────────────

func main() {
	// New: smart constructor — validate at creation time, get (value, error) back.
	u, err := usernameCodec.New(Username("alice_42"))
	if err != nil {
		fmt.Println("username error:", err)
	} else {
		fmt.Printf("created username: %q\n", u)
	}

	// New: short name violates MinLen(3).
	_, err = usernameCodec.New(Username("ab"))
	fmt.Println("too short:      ", err)

	// New: invalid character violates Pattern.
	_, err = usernameCodec.New(Username("Alice!"))
	fmt.Println("invalid chars:  ", err)

	// New with Score type.
	s, err := scoreCodec.New(Score(87))
	if err != nil {
		fmt.Println("score error:", err)
	} else {
		fmt.Printf("created score:   %d\n", s)
	}

	_, err = scoreCodec.New(Score(150))
	fmt.Println("score too high: ", err)

	// Must: package-level constant was validated at startup.
	fmt.Printf("\nguest user (const):  %q\n", guestUser)

	// Must with Decode: defaultScore was decoded and validated from a raw int at init.
	fmt.Printf("default score (wire): %d\n", defaultScore)

	// Must with a validated allow-list: all reserved names passed the codec at startup.
	fmt.Printf("reserved usernames:  %v\n", reservedUsernames)

	// Must: panics if the value is invalid — useful in test helpers or init code.
	// Uncomment to see the panic:
	// _ = codex.Must(scoreCodec.New(Score(-1)))

	// NewProfile: named per-field constructor wrapping profileCodec.New.
	// Both fields are validated (Username pattern/length, Score range) before
	// the caller ever sees a Profile value.
	p, err := NewProfile(Username("alice_42"), Score(87))
	if err != nil {
		fmt.Println("profile error:  ", err)
	} else {
		fmt.Printf("created profile: %+v\n", p)
	}

	// NewProfile: invalid score fails inside profileCodec.New — Profile is
	// never returned, only the zero value + a typed validation error.
	_, err = NewProfile(Username("alice_42"), Score(150))
	fmt.Println("invalid profile:", err)
}
