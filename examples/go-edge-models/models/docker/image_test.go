package docker

import (
	"strings"
	"testing"

	"github.com/DaniDeer/go-codex/codex"
)

func TestImageCodec_RoundTrip(t *testing.T) {
	tests := []struct {
		name  string
		raw   string
		want  Image
		want2 string // expected re-encoded string (usually == raw)
	}{
		{
			name: "name only",
			raw:  "alpine",
			want: Image{Name: "alpine"},
		},
		{
			name: "name and tag",
			raw:  "alpine:3.19",
			want: Image{Name: "alpine", Tag: "3.19"},
		},
		{
			name: "registry-prefixed name and tag",
			raw:  "ghcr.io/org/repo:1.2.3",
			want: Image{Name: "ghcr.io/org/repo", Tag: "1.2.3"},
		},
		{
			name: "name and digest",
			raw:  "alpine@sha256:" + strings.Repeat("a", 64),
			want: Image{Name: "alpine", Digest: Digest("sha256:" + strings.Repeat("a", 64))},
		},
		{
			name: "name, tag, and digest",
			raw:  "ghcr.io/org/repo:1.2.3@sha256:" + strings.Repeat("b", 64),
			want: Image{Name: "ghcr.io/org/repo", Tag: "1.2.3", Digest: Digest("sha256:" + strings.Repeat("b", 64))},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ImageCodec.Decode(tt.raw)
			if err != nil {
				t.Fatalf("Decode(%q): unexpected error: %v", tt.raw, err)
			}
			if got != tt.want {
				t.Errorf("Decode(%q) = %+v, want %+v", tt.raw, got, tt.want)
			}

			encoded, err := ImageCodec.Encode(got)
			if err != nil {
				t.Fatalf("Encode(%+v): unexpected error: %v", got, err)
			}
			if encoded != tt.raw {
				t.Errorf("Encode(%+v) = %q, want %q", got, encoded, tt.raw)
			}

			if got.String() != tt.raw {
				t.Errorf("String() = %q, want %q", got.String(), tt.raw)
			}
		})
	}
}

func TestImageCodec_RejectsInvalidShape(t *testing.T) {
	if _, err := ImageCodec.Decode("INVALID IMAGE REF!!"); err == nil {
		t.Error("Decode(\"INVALID IMAGE REF!!\"): want error, got nil")
	}
}

func TestImageCodec_RejectsEmptyName(t *testing.T) {
	if _, err := ImageCodec.Encode(Image{Tag: "1.0.0"}); err == nil {
		t.Error("Encode(Image{Tag: \"1.0.0\"}) with empty Name: want error, got nil")
	}
}

func TestImageCodec_RejectsInvalidTag(t *testing.T) {
	if _, err := ImageCodec.Decode("alpine: not-a-valid-tag!"); err == nil {
		t.Error("Decode with invalid tag: want error, got nil")
	}
}

func TestImageCodec_AcceptsValidTagAndDigest(t *testing.T) {
	raw := "alpine:3.19@sha256:" + strings.Repeat("c", 64)
	if _, err := ImageCodec.Decode(raw); err != nil {
		t.Errorf("Decode(%q): unexpected error: %v", raw, err)
	}
}

func TestTagConstraint_RejectsInvalidShape(t *testing.T) {
	if tagConstraint.Check("!!not valid!!") {
		t.Error("tagConstraint.Check(invalid tag) = true, want false")
	}
	if !tagConstraint.Check("") {
		t.Error("tagConstraint.Check(\"\") = false, want true (optional field)")
	}
	if !tagConstraint.Check("1.2.3") {
		t.Error("tagConstraint.Check(\"1.2.3\") = false, want true")
	}
}

func TestDigestConstraint_RejectsInvalidShape(t *testing.T) {
	if digestConstraint.Check("not-a-digest") {
		t.Error("digestConstraint.Check(invalid digest) = true, want false")
	}
	if !digestConstraint.Check("") {
		t.Error("digestConstraint.Check(\"\") = false, want true (optional field)")
	}
	valid := "sha256:" + strings.Repeat("d", 64)
	if !digestConstraint.Check(valid) {
		t.Errorf("digestConstraint.Check(%q) = false, want true", valid)
	}
}

func TestNewImage_ReturnsValueOnSuccess(t *testing.T) {
	got, err := NewImage("alpine", "3.19", "")
	if err != nil {
		t.Fatalf("NewImage: unexpected error: %v", err)
	}
	want := Image{Name: "alpine", Tag: "3.19"}
	if got != want {
		t.Errorf("NewImage(...) = %+v, want %+v", got, want)
	}
}

func TestNewImage_RejectsEmptyName(t *testing.T) {
	if _, err := NewImage("", "3.19", ""); err == nil {
		t.Error("NewImage with empty name: want error, got nil")
	}
}

func TestNewImage_RejectsInvalidTag(t *testing.T) {
	if _, err := NewImage("alpine", "!!not valid!!", ""); err == nil {
		t.Error("NewImage with invalid tag: want error, got nil")
	}
}

func TestImage_ImplementsHasCodec(t *testing.T) {
	img, err := NewImage("alpine", "latest", "")
	if err != nil {
		t.Fatalf("NewImage: %v", err)
	}
	if err := codex.Validate(img); err != nil {
		t.Errorf("codex.Validate(img) = %v, want nil", err)
	}
	raw, err := codex.EncodeSelf(img)
	if err != nil {
		t.Fatalf("codex.EncodeSelf: %v", err)
	}
	back, err := codex.DecodeAs[Image](raw)
	if err != nil {
		t.Fatalf("codex.DecodeAs: %v", err)
	}
	if back != img {
		t.Errorf("DecodeAs(EncodeSelf(img)) = %+v, want %+v", back, img)
	}
}
