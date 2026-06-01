// Package png-upload demonstrates how to define a REST route that:
//
//  1. Accepts a PNG binary request body (Content-Type: image/png).
//  2. Validates a path parameter ({id}) against a UUID codec.
//  3. Validates an incoming cookie (session_token) against a codec.
//
// The example is transport-agnostic: it does not import net/http or any HTTP
// framework. The same RouteHandle helpers (BuildPath, ValidateCookies, and
// the registered pngFormat) work unchanged with net/http, Chi, Gin, or Echo.
//
// Run with: go run ./examples/png-upload
package main

import (
	"bytes"
	"fmt"
	"os"

	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/schema"
	"github.com/DaniDeer/go-codex/validate"
)

// ImageMeta is the JSON response returned after a successful upload.
type ImageMeta struct {
	ID          string
	SizeBytes   int
	ContentType string
}

// --- Codecs ---

// pngMagic is the 8-byte PNG signature defined in the PNG specification.
var pngMagic = []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}

// rawBytesCodec passes []byte through without base64 encoding.
// Unlike codex.Bytes(), which encodes to/from base64, this codec treats the
// value as opaque binary data — suitable for binary HTTP request bodies.
var rawBytesCodec = codex.Codec[[]byte]{
	Schema: schema.Schema{Type: "string", Format: "binary"},
	Encode: func(v []byte) (any, error) { return v, nil },
	Decode: func(v any) ([]byte, error) {
		b, ok := v.([]byte)
		if !ok {
			return nil, codex.TypeMismatchError{Expected: "bytes", Got: fmt.Sprintf("%T", v)}
		}
		return b, nil
	},
}

// maxPNGBytes is the maximum accepted upload size (5 MiB).
const maxPNGBytes = 5 * 1024 * 1024

// pngCodec extends rawBytesCodec with two constraints applied in order:
//  1. validate.MaxBytes — rejects payloads that exceed the size limit.
//  2. PNG magic-bytes check — rejects data that is not a PNG file.
//
// MaxBytes runs first so oversized bodies are rejected with a clear size error
// before the magic-byte check reads any content.
var pngCodec = rawBytesCodec.
	Refine(validate.MaxBytes(maxPNGBytes)).
	Refine(codex.Constraint[[]byte]{
		Name: "png-header",
		Check: func(v []byte) bool {
			return len(v) >= 8 && bytes.Equal(v[:8], pngMagic)
		},
		Message: func([]byte) string {
			return "expected PNG data: missing or invalid PNG magic bytes (\\x89PNG\\r\\n\\x1a\\n)"
		},
	})

// pngFormat is a format.Format[[]byte] that reads and writes raw PNG bytes.
// format.NewTyped is used because the wire representation is binary, not a
// map[string]any intermediate. The codec validates (PNG magic bytes) on both
// the marshal path (via codec.Encode inside format.Marshal) and on the unmarshal
// path (explicitly, because format.NewTyped's unmarshal bypasses the codec).
var pngFormat = format.NewTyped(
	pngCodec,
	func(v []byte) ([]byte, error) { return v, nil }, // marshal: identity — bytes are already the wire form
	func(data []byte) ([]byte, error) { // unmarshal: validate then return
		if err := pngCodec.Validate(data); err != nil {
			return nil, err
		}
		return data, nil
	},
	"image/png",
)

// uuidCodec validates that a string is a valid UUID (used for the {id} path param).
var uuidCodec = codex.String().Refine(validate.UUID)

// sessionTokenCodec validates that the session_token cookie is non-empty and
// within a reasonable length.
var sessionTokenCodec = codex.String().
	Refine(validate.NonEmptyString).
	Refine(validate.MaxLen(256)).
	WithDescription("Opaque session token issued at login.")

// imageMetaCodec encodes the JSON response.
var imageMetaCodec = codex.Struct[ImageMeta](
	codex.RequiredField[ImageMeta, string]("id",
		codex.String().Refine(validate.UUID).WithDescription("Image resource ID (UUID)."),
		func(m ImageMeta) string { return m.ID },
		func(m *ImageMeta, v string) { m.ID = v },
	),
	codex.RequiredField[ImageMeta, int]("size_bytes",
		codex.Int().WithDescription("Size of the uploaded image in bytes."),
		func(m ImageMeta) int { return m.SizeBytes },
		func(m *ImageMeta, v int) { m.SizeBytes = v },
	),
	codex.RequiredField[ImageMeta, string]("content_type",
		codex.String().WithDescription("MIME type of the stored image."),
		func(m ImageMeta) string { return m.ContentType },
		func(m *ImageMeta, v string) { m.ContentType = v },
	),
)

func main() {
	b := rest.NewBuilder(rest.Info{
		Title:       "Image Upload API",
		Version:     "1.0.0",
		Description: "Demonstrates PNG binary request bodies with codec-validated path and cookie parameters.",
	}, rest.WithPathConstraints(validate.HTTPPath))

	// PUT /images/{id} — upload a PNG image identified by a UUID.
	//
	// PathParam{Codec}: BuildPath validates {id} as a UUID before substitution.
	// CookieParam{Codec}: ValidateCookies validates session_token is non-empty
	// and at most 256 characters.
	// WithRequestFormats(pngFormat): the adapter (nethttp/chi) negotiates
	// Content-Type: image/png and decodes the body via pngFormat.Unmarshal.
	uploadImage, err := rest.AddRoute[[]byte, ImageMeta](b, "PUT", "/images/{id}",
		pngCodec, imageMetaCodec,
		rest.RouteMeta{
			OperationID:     "uploadImage",
			Summary:         "Upload a PNG image",
			Description:     "Replaces the image for the given resource ID. Requires a valid session cookie.",
			Tags:            []string{"images"},
			RespStatus:      "200",
			RespDescription: "Image stored successfully.",
			RespSchemaName:  "ImageMeta",
		},
		rest.PathParam{
			Name:        "id",
			Description: "Resource ID (UUID) to associate the image with.",
			Codec:       &uuidCodec,
		},
		rest.CookieParam{
			Name:        "session_token",
			Description: "Session token issued at login. Required for all write operations.",
			Required:    true,
			Codec:       &sessionTokenCodec,
		},
		rest.ResponseMeta{Status: "400", Description: "Invalid PNG data or parameter validation failure."},
		rest.ResponseMeta{Status: "401", Description: "Missing or invalid session cookie."},
		rest.ResponseMeta{Status: "404", Description: "Resource not found."},
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "route registration error: %v\n", err)
		os.Exit(1)
	}

	// Register PNG as the accepted request format.
	// The adapter picks this format when the client sends Content-Type: image/png.
	uploadImage.WithRequestFormats(pngFormat)

	// --- Transport-agnostic demo (no net/http required) ---

	fmt.Println("=== BuildPath: path parameter validation ===")
	fmt.Println()

	validID := "f47ac10b-58cc-4372-a567-0e02b2c3d479"
	path, err := uploadImage.BuildPath(map[string]string{"id": validID})
	if err != nil {
		fmt.Fprintf(os.Stderr, "unexpected error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Valid UUID  → %s\n", path)

	_, err = uploadImage.BuildPath(map[string]string{"id": "not-a-uuid"})
	fmt.Printf("Invalid UUID → error: %v\n", err)
	fmt.Println()

	// --- Cookie validation ---

	fmt.Println("=== ValidateCookies: cookie validation ===")
	fmt.Println()

	err = uploadImage.ValidateCookies(map[string]string{
		"session_token": "tok_abc123_valid_session",
	})
	fmt.Printf("Valid token   → error: %v\n", err)

	err = uploadImage.ValidateCookies(map[string]string{
		"session_token": "",
	})
	fmt.Printf("Empty token   → error: %v\n", err)
	fmt.Println()

	// --- PNG format: Unmarshal (binary body decoding) ---

	fmt.Println("=== pngFormat.Unmarshal: PNG body validation ===")
	fmt.Println()

	// Minimal syntactically-valid 1×1 pixel PNG (67 bytes).
	validPNG := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, // PNG signature
		0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52, // IHDR chunk length + type
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, // width=1, height=1
		0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53, // bit depth, color type, ...
		0xDE, 0x00, 0x00, 0x00, 0x0C, 0x49, 0x44, 0x41, // IDAT chunk
		0x54, 0x08, 0xD7, 0x63, 0xF8, 0xCF, 0xC0, 0x00, // compressed pixel data
		0x00, 0x00, 0x02, 0x00, 0x01, 0xE2, 0x21, 0xBC, // ...
		0x33, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4E, // IEND chunk
		0x44, 0xAE, 0x42, 0x60, 0x82, // ...
	}

	decoded, err := pngFormat.Unmarshal(validPNG)
	fmt.Printf("Valid PNG (%d bytes) → decoded %d bytes, error: %v\n", len(validPNG), len(decoded), err)

	notPNG := []byte("this is not a PNG file")
	_, err = pngFormat.Unmarshal(notPNG)
	fmt.Printf("Not PNG           → error: %v\n", err)
	fmt.Println()

	// --- OpenAPI 3.1 spec ---

	fmt.Println("=== OpenAPI 3.1 spec ===")
	fmt.Println()

	doc, err := b.OpenAPISpec()
	if err != nil {
		fmt.Fprintf(os.Stderr, "OpenAPISpec error: %v\n", err)
		os.Exit(1)
	}
	yamlBytes, err := doc.MarshalYAML()
	if err != nil {
		fmt.Fprintf(os.Stderr, "MarshalYAML error: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(string(yamlBytes))
}
