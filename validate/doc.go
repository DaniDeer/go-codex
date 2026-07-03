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
//	codex.String().Refine(validate.Email)         // format: email
//	codex.String().Refine(validate.UUID)          // format: uuid
//	codex.String().Refine(validate.URL)           // format: uri
//	codex.String().Refine(validate.DateTime)      // format: date-time
//	codex.String().Refine(validate.ContainerImage) // OCI container image reference
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
// # Environment variable name constraints
//
// Validate environment variable names from external input (config files, CLI flags,
// user-supplied overrides) before passing them to [format.FromEnvVar] or [os.LookupEnv]:
//
//	// POSIX format: [A-Z_][A-Z0-9_]*
//	codex.String().Refine(validate.EnvVarName)
//
//	// Format + namespace — combine for full validation
//	appVarCodec := codex.String().
//	    Refine(validate.EnvVarName).
//	    Refine(validate.EnvVarPrefix("APP_"))
//
// These constraints are not needed when env var names are Go code literals —
// use them only when names arrive as runtime string input.
//
// # Binary byte constraints
//
// Byte size constraints work with any []byte value:
//
//	codex.Bytes().Refine(validate.MaxBytes(5 * 1024 * 1024)) // at most 5 MiB
//	codex.Bytes().Refine(validate.MinBytes(1))               // non-empty
//
// # Binary file format constraints
//
// Predefined constants validate common binary file formats by checking their
// magic bytes (file signatures). Use them with [codex.Bytes] and [format.Binary]:
//
//	validate.PNG   // \x89PNG\r\n\x1a\n  — PNG images
//	validate.JPEG  // \xFF\xD8\xFF        — JPEG images (all subtypes)
//	validate.GIF   // GIF87a / GIF89a    — GIF images
//	validate.WebP  // RIFF....WEBP       — WebP images
//	validate.PDF   // %PDF-              — PDF documents
//	validate.ZIP   // PK\x03\x04         — ZIP archives (also DOCX, XLSX, APK, JAR)
//
// # When to use which
//
// Use a built-in constant ([PNG], [JPEG], [GIF], [WebP], [PDF], [ZIP]) for
// known file formats — they produce readable error names ("png", "jpeg", …)
// in logs and [codex.ConstraintError] values.
//
// Use [HasPrefix] for custom or proprietary binary formats not covered by the
// built-in set (e.g. an internal protocol header or a vendor-specific file type).
//
// Use [MaxBytes] and [MinBytes] to enforce size limits on any []byte value.
//
// # Composition and ordering
//
// When combining constraints with [codex.Codec.Refine], put size checks
// before format checks — rejecting oversized input before reading the magic
// bytes avoids unnecessary work:
//
//	pngCodec := codex.Bytes().
//	    Refine(validate.MaxBytes(5 * 1024 * 1024)). // 1. size (cheap, fails fast)
//	    Refine(validate.PNG)                         // 2. format (reads 8 bytes)
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
