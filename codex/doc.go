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
// # Binary codecs — Bytes vs Base64
//
// Both [Bytes] and [Base64] work with []byte in Go, but serialize differently:
//
//	codex.Bytes()    // raw []byte pass-through; schema format "binary" (OpenAPI binary body)
//	codex.Base64()   // base64 string encoding; schema format "byte" (OpenAPI base64 field)
//
// Use [Bytes] for binary file I/O and HTTP binary request/response bodies — the wire
// representation is the raw bytes themselves. Combine with [format.Binary] and
// [validate.HasPrefix] for magic-byte validation:
//
//	pngSignature := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
//	pngCodec := codex.Bytes().
//	    Refine(validate.MaxBytes(5 * 1024 * 1024)).
//	    Refine(validate.HasPrefix(pngSignature))
//
// Use [Base64] when the binary data is embedded inside a JSON document as a
// base64-encoded string field (e.g. an "avatar" field in a user profile):
//
//	codex.OptionalField("avatar",
//	    codex.Base64().Refine(validate.MaxBytes(65536)).
//	        WithDescription("Profile image (base64, max 64 KiB)."),
//	    ...
//	)
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
// Key composing constructors:
//
//   - [SliceOf] — homogeneous array
//   - [StringMap] — map[string]V
//   - [Map] — map[K]V with validated keys
//   - [EntrySlice] — JSON/YAML/TOML object where key+value are merged into a single element type
//   - [Nullable] — optional pointer *T
//   - [TaggedUnion] — discriminated union
//   - [UntaggedUnion] — structural union (first-match decode)
//   - [Either2] — two-branch sum type, Either[A,B]
//   - [StringOrInt], [StringOrInt32], [StringOrInt64], [StringOrUint], [StringOrUint64],
//     [StringOrFloat32], [StringOrFloat64] — named convenience over Either2(String(), Xxx())
//     for the common "value is a string OR a number" wire pattern (Docker/IoT-Edge env vars,
//     Kubernetes IntOrString, Terraform/HCL); works uniformly across JSON, YAML, and TOML
//
// [EntrySlice] is particularly useful when the object key carries domain meaning:
//
//	var containersCodec = codex.EntrySlice(
//	    containerKeyCodec,   // validates + strips prefix from wire key
//	    moduleCodec,         // decodes value
//	    func(name string, m ModuleConfig) Container {
//	        return Container{Name: name, Image: m.Image, Status: m.Status}
//	    },
//	    func(c Container) (string, ModuleConfig) {
//	        return c.Name, ModuleConfig{Image: c.Image, Status: c.Status}
//	    },
//	)
//	// Codec[[]Container] — no post-processing needed
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
