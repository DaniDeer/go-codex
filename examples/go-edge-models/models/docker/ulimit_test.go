package docker

import (
	"testing"

	"github.com/DaniDeer/go-codex/codex"
)

func TestNewUlimit_ReturnsValueOnSuccess(t *testing.T) {
	got, err := NewUlimit("nofile", 1024, 2048)
	if err != nil {
		t.Fatalf("NewUlimit: unexpected error: %v", err)
	}
	want := Ulimit{Name: "nofile", Soft: 1024, Hard: 2048}
	if got != want {
		t.Errorf("NewUlimit(...) = %+v, want %+v", got, want)
	}
}

func TestNewUlimit_RejectsUnknownName(t *testing.T) {
	if _, err := NewUlimit("not-a-real-limit", 1024, 2048); err == nil {
		t.Error("NewUlimit with unknown name: want error, got nil")
	}
}

func TestUlimit_ImplementsHasCodec(t *testing.T) {
	u, err := NewUlimit("nofile", 1024, 2048)
	if err != nil {
		t.Fatalf("NewUlimit: %v", err)
	}
	if err := codex.Validate(u); err != nil {
		t.Errorf("codex.Validate(u) = %v, want nil", err)
	}
	raw, err := codex.EncodeSelf(u)
	if err != nil {
		t.Fatalf("codex.EncodeSelf: %v", err)
	}
	back, err := codex.DecodeAs[Ulimit](raw)
	if err != nil {
		t.Fatalf("codex.DecodeAs: %v", err)
	}
	if back != u {
		t.Errorf("DecodeAs(EncodeSelf(u)) = %+v, want %+v", back, u)
	}
}
