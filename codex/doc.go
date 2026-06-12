// Package codex is the public API for go-codex: a self-documenting codec library for Go.
//
// A [Codec][T] is a single value that simultaneously describes how to encode,
// decode, and document a type. Write the codec once; derive JSON, YAML, TOML,
// OpenAPI schemas, AsyncAPI schemas, and more from the same definition — no
// struct tags, no reflection, no code generation.
//
// # Core type
//
// [Codec][T] bundles three functions in one value:
//
//   - Encode: transforms a Go value into an intermediate (e.g. map[string]any for JSON)
//   - Decode: transforms the intermediate back to T, running all constraints
//   - Schema: carries the data shape and constraints as a [schema.Schema] value
//
// # Primitive codecs
//
// Use these to build up more complex codecs:
//
//	codex.String()   // string
//	codex.Int()      // int
//	codex.Float64()  // float64
//	codex.Bool()     // bool
//	codex.Time()     // time.Time ↔ RFC 3339 string
//	codex.Any()      // any
//
// # Struct codecs
//
// Build struct codecs with [RequiredField] and [OptionalField]:
//
//	var UserCodec = codex.Struct[User](
//	    codex.RequiredField("name",
//	        codex.String().Refine(validate.NonEmptyString).WithDescription("Display name."),
//	        func(u User) string { return u.Name },
//	        func(u *User, v string) { u.Name = v },
//	    ),
//	    codex.RequiredField("email",
//	        codex.String().Refine(validate.Email),
//	        func(u User) string { return u.Email },
//	        func(u *User, v string) { u.Email = v },
//	    ),
//	)
//
// # Constraints
//
// Add constraints with [Codec.Refine]. Constraints run on both Encode and Decode:
//
//	var AgeCodec = codex.Int().
//	    Refine(validate.RangeInt(0, 150)).
//	    WithTitle("Age").
//	    WithDescription("Age in years.")
//
// Constraint violations return structured [ValidationErrors] that are
// fully inspectable with [errors.As].
//
// # Composition
//
// Codecs compose — build complex codecs from simpler ones:
//
//	// Define a field codec once, reuse across multiple structs.
//	var emailField = codex.String().Refine(validate.Email).
//	    WithDescription("Email address.")
//
//	var UserCodec    = codex.Struct[User](   codex.RequiredField("email", emailField, ...), ...)
//	var ProfileCodec = codex.Struct[Profile](codex.RequiredField("email", emailField, ...), ...)
//
// # Smart constructors
//
// Use [Codec.New] to validate at construction time:
//
//	email, err := emailCodec.New(Email("user@example.com"))
//	// err != nil if the email is invalid
//
// Use [Must] for package-level validated constants:
//
//	var defaultUser = codex.Must(usernameCodec.New(Username("guest")))
//
// # Further reading
//
//   - [validate] — reusable constraints (Email, UUID, URL, ranges, …)
//   - [format] — format bridges (JSON, YAML, TOML, Gob, streaming)
//   - [api/rest] — REST API builder using codecs
//   - [api/events] — event channel builder using codecs
//   - [forge] — governed computation pipeline using codecs
package codex
