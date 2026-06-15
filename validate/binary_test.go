package validate_test

import (
	"errors"
	"testing"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/validate"
)

// ── test helpers ──────────────────────────────────────────────────────────────

// mustRefinePass encodes v through a codec with the given constraint and fails
// the test if an error is returned.
func mustRefinePass(t *testing.T, c codex.Constraint[[]byte], v []byte) {
	t.Helper()
	codec := codex.Bytes().Refine(c)
	if _, err := codec.Encode(v); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

// mustRefineFail encodes v through a codec with the given constraint and fails
// the test if no error is returned. Returns the error for further assertion.
func mustRefineFail(t *testing.T, c codex.Constraint[[]byte], v []byte) error {
	t.Helper()
	codec := codex.Bytes().Refine(c)
	_, err := codec.Encode(v)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	return err
}

// ── PNG ───────────────────────────────────────────────────────────────────────

func TestPNG_Name(t *testing.T) {
	if validate.PNG.Name == "" {
		t.Fatal("PNG constraint Name must not be empty")
	}
}

func TestPNG_ValidSignature_Passes(t *testing.T) {
	valid := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00}
	mustRefinePass(t, validate.PNG, valid)
}

func TestPNG_WrongSignature_Fails(t *testing.T) {
	err := mustRefineFail(t, validate.PNG, []byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07})
	var ce codex.ConstraintError
	if !errors.As(err, &ce) {
		t.Fatalf("expected ConstraintError, got %T: %v", err, err)
	}
}

func TestPNG_TooShort_Fails(t *testing.T) {
	mustRefineFail(t, validate.PNG, []byte{0x89, 0x50, 0x4E})
}

func TestPNG_Empty_Fails(t *testing.T) {
	mustRefineFail(t, validate.PNG, []byte{})
}

// ── JPEG ──────────────────────────────────────────────────────────────────────

func TestJPEG_Name(t *testing.T) {
	if validate.JPEG.Name == "" {
		t.Fatal("JPEG constraint Name must not be empty")
	}
}

func TestJPEG_ValidSOI_Passes(t *testing.T) {
	// Minimal JPEG: FF D8 FF E0 (JFIF marker)
	mustRefinePass(t, validate.JPEG, []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10})
}

func TestJPEG_ExifVariant_Passes(t *testing.T) {
	// JPEG with Exif: FF D8 FF E1
	mustRefinePass(t, validate.JPEG, []byte{0xFF, 0xD8, 0xFF, 0xE1, 0x00, 0x1C})
}

func TestJPEG_WrongSignature_Fails(t *testing.T) {
	err := mustRefineFail(t, validate.JPEG, []byte{0x89, 0x50, 0x4E, 0x47})
	var ce codex.ConstraintError
	if !errors.As(err, &ce) {
		t.Fatalf("expected ConstraintError, got %T: %v", err, err)
	}
}

func TestJPEG_TooShort_Fails(t *testing.T) {
	mustRefineFail(t, validate.JPEG, []byte{0xFF, 0xD8})
}

// ── GIF ───────────────────────────────────────────────────────────────────────

func TestGIF_Name(t *testing.T) {
	if validate.GIF.Name == "" {
		t.Fatal("GIF constraint Name must not be empty")
	}
}

func TestGIF_GIF87a_Passes(t *testing.T) {
	mustRefinePass(t, validate.GIF, append([]byte("GIF87a"), 0x00, 0x00))
}

func TestGIF_GIF89a_Passes(t *testing.T) {
	mustRefinePass(t, validate.GIF, append([]byte("GIF89a"), 0x00, 0x00))
}

func TestGIF_WrongSignature_Fails(t *testing.T) {
	err := mustRefineFail(t, validate.GIF, []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A})
	var ce codex.ConstraintError
	if !errors.As(err, &ce) {
		t.Fatalf("expected ConstraintError, got %T: %v", err, err)
	}
}

func TestGIF_TooShort_Fails(t *testing.T) {
	mustRefineFail(t, validate.GIF, []byte("GIF8"))
}

// ── WebP ──────────────────────────────────────────────────────────────────────

func TestWebP_Name(t *testing.T) {
	if validate.WebP.Name == "" {
		t.Fatal("WebP constraint Name must not be empty")
	}
}

func TestWebP_ValidSignature_Passes(t *testing.T) {
	// RIFF + 4-byte file size + WEBP
	data := []byte("RIFF\x00\x00\x00\x00WEBP")
	mustRefinePass(t, validate.WebP, data)
}

func TestWebP_WrongRIFF_Fails(t *testing.T) {
	data := []byte("XXXX\x00\x00\x00\x00WEBP")
	err := mustRefineFail(t, validate.WebP, data)
	var ce codex.ConstraintError
	if !errors.As(err, &ce) {
		t.Fatalf("expected ConstraintError, got %T: %v", err, err)
	}
}

func TestWebP_WrongFourCC_Fails(t *testing.T) {
	data := []byte("RIFF\x00\x00\x00\x00WAVE")
	mustRefineFail(t, validate.WebP, data)
}

func TestWebP_TooShort_Fails(t *testing.T) {
	mustRefineFail(t, validate.WebP, []byte("RIFF\x00\x00\x00"))
}

// ── PDF ───────────────────────────────────────────────────────────────────────

func TestPDF_Name(t *testing.T) {
	if validate.PDF.Name == "" {
		t.Fatal("PDF constraint Name must not be empty")
	}
}

func TestPDF_ValidSignature_Passes(t *testing.T) {
	mustRefinePass(t, validate.PDF, append([]byte("%PDF-1.7"), make([]byte, 10)...))
}

func TestPDF_PDF20_Passes(t *testing.T) {
	mustRefinePass(t, validate.PDF, append([]byte("%PDF-2.0"), make([]byte, 10)...))
}

func TestPDF_WrongSignature_Fails(t *testing.T) {
	err := mustRefineFail(t, validate.PDF, []byte{0x89, 0x50, 0x4E, 0x47, 0x0D})
	var ce codex.ConstraintError
	if !errors.As(err, &ce) {
		t.Fatalf("expected ConstraintError, got %T: %v", err, err)
	}
}

func TestPDF_TooShort_Fails(t *testing.T) {
	mustRefineFail(t, validate.PDF, []byte("%PDF"))
}

// ── ZIP ───────────────────────────────────────────────────────────────────────

func TestZIP_Name(t *testing.T) {
	if validate.ZIP.Name == "" {
		t.Fatal("ZIP constraint Name must not be empty")
	}
}

func TestZIP_ValidSignature_Passes(t *testing.T) {
	// PK\x03\x04 local file header
	mustRefinePass(t, validate.ZIP, []byte{0x50, 0x4B, 0x03, 0x04, 0x14, 0x00})
}

func TestZIP_WrongSignature_Fails(t *testing.T) {
	err := mustRefineFail(t, validate.ZIP, []byte{0x89, 0x50, 0x4E, 0x47})
	var ce codex.ConstraintError
	if !errors.As(err, &ce) {
		t.Fatalf("expected ConstraintError, got %T: %v", err, err)
	}
}

func TestZIP_TooShort_Fails(t *testing.T) {
	mustRefineFail(t, validate.ZIP, []byte{0x50, 0x4B, 0x03})
}

func TestZIP_Empty_Fails(t *testing.T) {
	mustRefineFail(t, validate.ZIP, []byte{})
}
