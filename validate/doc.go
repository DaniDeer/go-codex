// Package validate provides reusable [codex.Constraint] values for common
// validation rules.
//
// Every constraint in this package does two things:
//  1. Checks the value at runtime (on both Encode and Decode).
//  2. Annotates [schema.Schema] automatically so the constraint appears in
//     generated OpenAPI / AsyncAPI specs — no extra wiring required.
//
// # String format constraints
//
// Format constraints validate common string patterns and set the corresponding
// OpenAPI format keyword in the schema:
//
//	codex.String().Refine(validate.Email)    // format: email
//	codex.String().Refine(validate.UUID)     // format: uuid
//	codex.String().Refine(validate.URL)      // format: uri
//	codex.String().Refine(validate.DateTime) // format: date-time
//
// # Range and length constraints
//
// Numeric and string range constraints annotate minimum/maximum/minLength/
// maxLength in the schema:
//
//	codex.Int().Refine(validate.RangeInt(1, 100))    // minimum: 1, maximum: 100
//	codex.String().Refine(validate.MaxLen(255))       // maxLength: 255
//	codex.String().Refine(validate.OneOf("a", "b"))   // enum: [a, b]
//
// # Protocol constraints
//
// Path and topic constraints are used with [api/rest.WithPathConstraints] and
// [api/events.WithTopicConstraints]:
//
//	rest.NewBuilder(info, rest.WithPathConstraints(validate.HTTPPath))
//	events.NewBuilder(info, events.WithTopicConstraints(validate.MQTTPublishTopic))
//
// # Custom constraints
//
// Use [codex.Constraint][T] to define your own rules; the validate package
// uses the same mechanism internally:
//
//	func MaxBytes(n int) codex.Constraint[[]byte] {
//	    return codex.Constraint[[]byte]{
//	        Name:  fmt.Sprintf("maxBytes(%d)", n),
//	        Check: func(v []byte) bool { return len(v) <= n },
//	        Message: func(v []byte) string {
//	            return fmt.Sprintf("expected at most %d bytes, got %d", n, len(v))
//	        },
//	    }
//	}
package validate
