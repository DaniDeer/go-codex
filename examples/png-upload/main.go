// Package png-upload demonstrates how to define REST routes for PNG binary
// transfer using go-codex:
//
//   - Upload (PUT /images/{id}): binary PNG request body, JSON response.
//   - Download (POST /images/{id}/download): JSON request body, binary PNG response.
//
// Both routes validate a path parameter ({id} as UUID) and a session cookie
// via codec constraints.
//
// The example is transport-agnostic: it does not import net/http or any HTTP
// framework. The same RouteHandle helpers (BuildPath, ValidateCookies,
// WithRequestFormats, WithFormats) work unchanged with net/http, Chi,
// Gin, or Echo.
//
// Run with: go run ./examples/png-upload
package main

import (
	"fmt"
	"os"

	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/validate"
)

// ImageMeta is the JSON response returned after a successful upload.
type ImageMeta struct {
	ID          string
	SizeBytes   int
	ContentType string
}

// DownloadImageRequest is the JSON request body for the download route.
type DownloadImageRequest struct {
	Quality string // "original" or "thumbnail"
}

// --- Codecs ---

// maxPNGBytes is the maximum accepted upload size (5 MiB).
const maxPNGBytes = 5 * 1024 * 1024

// pngCodec validates PNG payloads with two constraints applied in order:
//  1. validate.MaxBytes — rejects payloads that exceed the size limit.
//  2. validate.PNG — rejects data that is not a PNG file (magic-byte check).
//
// MaxBytes runs first so oversized bodies are rejected with a clear size error
// before the format check reads any content.
//
// codex.Bytes() passes []byte through without any encoding — the raw binary
// payload is the wire form for HTTP bodies and file I/O.
var pngCodec = codex.Bytes().
	Refine(validate.MaxBytes(maxPNGBytes)).
	Refine(validate.PNG)

// pngFormat reads and writes raw PNG bytes.
// format.Binary uses the NewTyped path internally: bytes are stored as-is,
// and pngCodec constraints are validated on both write and read.
var pngFormat = format.Binary(pngCodec).WithContentType("image/png")

// uuidCodec validates that a string is a valid UUID (used for the {id} path param).
var uuidCodec = codex.String().Refine(validate.UUID)

// sessionTokenCodec validates that the session_token cookie is non-empty and
// within a reasonable length.
var sessionTokenCodec = codex.String().
	Refine(validate.NonEmptyString).
	Refine(validate.MaxLen(256)).
	WithDescription("Opaque session token issued at login.")

// imageMetaCodec encodes the JSON response for the upload route.
var imageMetaCodec = codex.Struct[ImageMeta](
	codex.RequiredField("id",
		codex.String().Refine(validate.UUID).WithDescription("Image resource ID (UUID)."),
		func(m ImageMeta) string { return m.ID },
		func(m *ImageMeta, v string) { m.ID = v },
	),
	codex.RequiredField("size_bytes",
		codex.Int().WithDescription("Size of the uploaded image in bytes."),
		func(m ImageMeta) int { return m.SizeBytes },
		func(m *ImageMeta, v int) { m.SizeBytes = v },
	),
	codex.RequiredField("content_type",
		codex.String().WithDescription("MIME type of the stored image."),
		func(m ImageMeta) string { return m.ContentType },
		func(m *ImageMeta, v string) { m.ContentType = v },
	),
)

// downloadRequestCodec decodes and validates the JSON download request body.
var downloadRequestCodec = codex.Struct[DownloadImageRequest](
	codex.RequiredField("quality",
		codex.String().Refine(validate.OneOf("original", "thumbnail")).
			WithDescription(`Requested image quality. "original" returns the full-resolution PNG; "thumbnail" returns a downscaled version.`),
		func(r DownloadImageRequest) string { return r.Quality },
		func(r *DownloadImageRequest, v string) { r.Quality = v },
	),
)

func main() {
	b := rest.NewBuilder(rest.Info{
		Title:       "Image Transfer API",
		Version:     "1.0.0",
		Description: "Demonstrates PNG binary transfer: upload (binary request → JSON response) and download (JSON request → binary response).",
	}, rest.WithPathConstraints(validate.HTTPPath))

	// PUT /images/{id} — upload a PNG image identified by a UUID.
	//
	// PathParam{Codec}: BuildPath validates {id} as a UUID before substitution.
	// CookieParam{Codec}: ValidateCookies validates session_token is non-empty
	// and at most 256 characters.
	// WithRequestFormats(pngFormat): the adapter (nethttp/chi) negotiates
	// Content-Type: image/png and decodes the body via pngFormat.Unmarshal.
	uploadImage, err := rest.NewRoute[[]byte, ImageMeta]("PUT", "/images/{id}",
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
		}.WithCodec(uuidCodec),
		rest.CookieParam{
			Name:        "session_token",
			Description: "Session token issued at login. Required for all write operations.",
			Required:    true,
		}.WithCodec(sessionTokenCodec),
		rest.ResponseMeta{Status: "400", Description: "Invalid PNG data or parameter validation failure."},
		rest.ResponseMeta{Status: "401", Description: "Missing or invalid session cookie."},
		rest.ResponseMeta{Status: "404", Description: "Resource not found."},
	).Register(b)
	if err != nil {
		fmt.Fprintf(os.Stderr, "route registration error: %v\n", err)
		os.Exit(1)
	}

	// Register PNG as the accepted request format.
	// The adapter picks this format when the client sends Content-Type: image/png.
	uploadImage.WithRequestFormats(pngFormat)

	// POST /images/{id}/download — download a PNG image.
	//
	// The request body is JSON (quality selector); the response is a raw PNG
	// binary. WithFormats(pngFormat) puts pngFormat on the response
	// side: the adapter negotiates Accept: image/png and calls pngFormat.Marshal
	// to encode the handler's []byte return value before writing it to the client.
	//
	// The same MaxBytes + magic-bytes constraints on pngCodec protect both
	// directions: Unmarshal validates incoming PNG on upload; Marshal validates
	// outgoing PNG on download, preventing the server from emitting corrupt data.
	downloadImage, err := rest.NewRoute[DownloadImageRequest, []byte]("POST", "/images/{id}/download",
		downloadRequestCodec, pngCodec,
		rest.RouteMeta{
			OperationID:     "downloadImage",
			Summary:         "Download a PNG image",
			Description:     "Returns the stored PNG for the given resource ID at the requested quality level.",
			Tags:            []string{"images"},
			RespStatus:      "200",
			RespDescription: "PNG image bytes.",
			ReqSchemaName:   "DownloadImageRequest",
		},
		rest.PathParam{
			Name:        "id",
			Description: "Resource ID (UUID) of the image to download.",
		}.WithCodec(uuidCodec),
		rest.CookieParam{
			Name:        "session_token",
			Description: "Session token issued at login.",
			Required:    true,
		}.WithCodec(sessionTokenCodec),
		rest.ResponseMeta{Status: "400", Description: "Invalid request body or parameter validation failure."},
		rest.ResponseMeta{Status: "401", Description: "Missing or invalid session cookie."},
		rest.ResponseMeta{Status: "404", Description: "Image not found."},
	).Register(b)
	if err != nil {
		fmt.Fprintf(os.Stderr, "route registration error: %v\n", err)
		os.Exit(1)
	}

	// Register PNG as the produced response format.
	// The adapter picks this format when the client sends Accept: image/png.
	downloadImage.WithFormats(pngFormat)

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

	// --- Download route: JSON request decode + PNG response encode ---

	fmt.Println("=== Download route: Decode JSON request ===")
	fmt.Println()

	// Valid JSON body: quality is one of the allowed values.
	req, err := downloadImage.Decode([]byte(`{"quality":"original"}`))
	if err != nil {
		fmt.Fprintf(os.Stderr, "unexpected error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Valid quality    → %+v\n", req)

	// Invalid JSON body: quality value not in the allowed set.
	_, err = downloadImage.Decode([]byte(`{"quality":"ultra-hd"}`))
	fmt.Printf("Invalid quality  → error: %v\n", err)
	fmt.Println()

	fmt.Println("=== Download route: pngFormat.Marshal PNG response ===")
	fmt.Println()

	// Marshal validates the outgoing PNG (MaxBytes + magic bytes) before
	// returning the bytes to the caller or adapter. This prevents the server
	// from sending corrupt or oversized data to the client.
	out, err := pngFormat.Marshal(validPNG)
	fmt.Printf("Valid PNG (%d bytes) → marshalled %d bytes, error: %v\n", len(validPNG), len(out), err)

	_, err = pngFormat.Marshal(notPNG)
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
