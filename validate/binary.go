package validate

import (
	"bytes"

	"github.com/DaniDeer/go-codex/codex"
)

// Binary file format constraints.
//
// Each constant validates the magic bytes (file signature) at the start of a
// byte slice — the same mechanism as [HasPrefix], but with a human-readable
// constraint name ("png", "jpeg", …) instead of a hex string, and with
// format-specific error messages.
//
// Prefer these constants over [HasPrefix] for the formats listed here. Use
// [HasPrefix] only for custom or proprietary formats not covered by this set.
//
// Typical usage with [format.Binary]:
//
//	pngCodec := codex.Bytes().
//	    Refine(validate.MaxBytes(5 * 1024 * 1024)). // size check first
//	    Refine(validate.PNG)                         // then format check
//
//	var pngFile = ports.NewFile(
//	    "images/{name}.png",
//	    format.Binary(pngCodec).WithContentType("image/png"),
//	    ports.FilePathParam{Name: "name"},
//	)
//
// All constraints produce a [codex.ConstraintError] navigable via [errors.As].

// PNG is a Constraint that requires the byte slice to be a PNG image.
// It checks the 8-byte PNG signature: \x89PNG\r\n\x1a\n.
var PNG = codex.Constraint[[]byte]{
	Name: "png",
	Check: func(v []byte) bool {
		return len(v) >= 8 && bytes.Equal(v[:8], []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A})
	},
	Message: func([]byte) string {
		return `expected PNG file: missing or invalid magic bytes (\x89PNG\r\n\x1a\n)`
	},
}

// JPEG is a Constraint that requires the byte slice to be a JPEG image.
// It checks the 3-byte SOI marker: \xFF\xD8\xFF.
// This covers all JPEG subtypes (JFIF, Exif, JFIF-Exif, ICC, etc.).
var JPEG = codex.Constraint[[]byte]{
	Name: "jpeg",
	Check: func(v []byte) bool {
		return len(v) >= 3 && bytes.Equal(v[:3], []byte{0xFF, 0xD8, 0xFF})
	},
	Message: func([]byte) string {
		return `expected JPEG file: missing or invalid SOI marker (\xFF\xD8\xFF)`
	},
}

// GIF is a Constraint that requires the byte slice to be a GIF image.
// It accepts both GIF87a and GIF89a by checking the first 6 bytes.
var GIF = codex.Constraint[[]byte]{
	Name: "gif",
	Check: func(v []byte) bool {
		if len(v) < 6 {
			return false
		}
		return bytes.Equal(v[:6], []byte("GIF87a")) || bytes.Equal(v[:6], []byte("GIF89a"))
	},
	Message: func([]byte) string {
		return "expected GIF file: missing or invalid magic bytes (GIF87a or GIF89a)"
	},
}

// WebP is a Constraint that requires the byte slice to be a WebP image.
// It checks for RIFF at bytes 0–3 and WEBP at bytes 8–11 (minimum 12 bytes).
var WebP = codex.Constraint[[]byte]{
	Name: "webp",
	Check: func(v []byte) bool {
		return len(v) >= 12 &&
			bytes.Equal(v[:4], []byte("RIFF")) &&
			bytes.Equal(v[8:12], []byte("WEBP"))
	},
	Message: func([]byte) string {
		return "expected WebP file: missing or invalid RIFF/WEBP container signature"
	},
}

// PDF is a Constraint that requires the byte slice to be a PDF document.
// It checks the 5-byte magic: %PDF-.
var PDF = codex.Constraint[[]byte]{
	Name: "pdf",
	Check: func(v []byte) bool {
		return len(v) >= 5 && bytes.Equal(v[:5], []byte("%PDF-"))
	},
	Message: func([]byte) string {
		return "expected PDF file: missing or invalid magic bytes (%PDF-)"
	},
}

// ZIP is a Constraint that requires the byte slice to be a ZIP archive.
// It checks the 4-byte local file header signature: PK\x03\x04.
// This also covers ZIP-based formats such as DOCX, XLSX, APK, and JAR.
var ZIP = codex.Constraint[[]byte]{
	Name: "zip",
	Check: func(v []byte) bool {
		return len(v) >= 4 && bytes.Equal(v[:4], []byte{0x50, 0x4B, 0x03, 0x04})
	},
	Message: func([]byte) string {
		return `expected ZIP archive: missing or invalid local file header (PK\x03\x04)`
	},
}
