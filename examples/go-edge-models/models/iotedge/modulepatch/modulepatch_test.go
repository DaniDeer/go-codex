package modulepatch

import (
	"testing"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge"
)

func TestNewModulePatch_ReturnsValueOnSuccess(t *testing.T) {
	got, err := NewModulePatch(iotedge.ModuleName("temp-sensor"), "ghcr.io/org/repo:1.2.3")
	if err != nil {
		t.Fatalf("NewModulePatch: unexpected error: %v", err)
	}
	want := ModulePatch{ModuleName: "temp-sensor", ImageURL: "ghcr.io/org/repo:1.2.3"}
	if got != want {
		t.Errorf("NewModulePatch(...) = %+v, want %+v", got, want)
	}
}

func TestNewModulePatch_RejectsEmptyImageURL(t *testing.T) {
	if _, err := NewModulePatch(iotedge.ModuleName("temp-sensor"), ""); err == nil {
		t.Error("NewModulePatch with empty imageURL: want error, got nil")
	}
}

func TestModulePatch_ImplementsHasCodec(t *testing.T) {
	mp, err := NewModulePatch(iotedge.ModuleName("temp-sensor"), "alpine:latest")
	if err != nil {
		t.Fatalf("NewModulePatch: %v", err)
	}
	if err := codex.Validate(mp); err != nil {
		t.Errorf("codex.Validate(mp) = %v, want nil", err)
	}
	raw, err := codex.EncodeSelf(mp)
	if err != nil {
		t.Fatalf("codex.EncodeSelf: %v", err)
	}
	back, err := codex.DecodeAs[ModulePatch](raw)
	if err != nil {
		t.Fatalf("codex.DecodeAs: %v", err)
	}
	if back != mp {
		t.Errorf("DecodeAs(EncodeSelf(mp)) = %+v, want %+v", back, mp)
	}
}
