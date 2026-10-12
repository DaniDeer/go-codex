package util

import "testing"

func TestPlatformSelectorCodec_RoundTrips(t *testing.T) {
	sel, err := PlatformSelectorCodec.Decode("linux/amd64")
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if sel.OS != "linux" || sel.Architecture != "amd64" {
		t.Errorf("Decode(\"linux/amd64\") = %+v, want OS=linux Architecture=amd64", sel)
	}

	raw, err := PlatformSelectorCodec.Encode(sel)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if raw.(string) != "linux/amd64" {
		t.Errorf("Encode(...) = %q, want %q", raw, "linux/amd64")
	}
}

func TestPlatformSelectorCodec_RejectsMalformedSelector(t *testing.T) {
	if _, err := PlatformSelectorCodec.Decode("not-a-valid-selector!!"); err == nil {
		t.Error("Decode: want error for malformed selector, got nil")
	}
}

// TestPlatformConstraint_RejectsUnsupportedButSyntacticallyValidPlatform
// confirms PlatformConstraint is a real ENUM (SupportedPlatforms), not an
// open "os/arch"-shaped regex — "linux/riscv64" matches the OLD regex
// shape but is not in SupportedPlatforms, so it must now be rejected.
func TestPlatformConstraint_RejectsUnsupportedButSyntacticallyValidPlatform(t *testing.T) {
	if _, err := PlatformSelectorCodec.Decode("linux/riscv64"); err == nil {
		t.Error("Decode(\"linux/riscv64\"): want error (not a member of SupportedPlatforms), got nil")
	}
}

func TestDefaultPlatformSelector_MatchesDefaultPlatform(t *testing.T) {
	want, err := PlatformSelectorCodec.Decode(DefaultPlatform)
	if err != nil {
		t.Fatalf("PlatformSelectorCodec.Decode(DefaultPlatform): %v", err)
	}
	if DefaultPlatformSelector != want {
		t.Errorf("DefaultPlatformSelector = %+v, want %+v (decoded from DefaultPlatform %q)", DefaultPlatformSelector, want, DefaultPlatform)
	}
}

func TestDefaultPlatformCodec_EmptyStringDecodesToLeft(t *testing.T) {
	e, err := DefaultPlatformCodec.Decode("")
	if err != nil {
		t.Fatalf("Decode(\"\"): %v", err)
	}
	if !e.IsLeft() {
		t.Errorf("Decode(\"\"): want Left (default), got %+v", e)
	}
}

func TestDefaultPlatformCodec_ValidPlatformDecodesToRight(t *testing.T) {
	e, err := DefaultPlatformCodec.Decode("linux/arm64")
	if err != nil {
		t.Fatalf("Decode(\"linux/arm64\"): %v", err)
	}
	if !e.IsRight() {
		t.Fatalf("Decode(\"linux/arm64\"): want Right, got %+v", e)
	}
	if e.Right.OS != "linux" || e.Right.Architecture != "arm64" {
		t.Errorf("Decode(\"linux/arm64\").Right = %+v, want OS=linux Architecture=arm64", *e.Right)
	}
}

func TestDefaultPlatformCodec_RejectsInvalidPlatform(t *testing.T) {
	if _, err := DefaultPlatformCodec.Decode("not-empty-and-not-a-platform!!"); err == nil {
		t.Error("Decode: want error for neither empty nor a valid platform, got nil")
	}
}
