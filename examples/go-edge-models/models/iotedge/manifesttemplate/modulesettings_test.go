package manifesttemplate

import (
	"testing"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker"
)

func TestNewModuleSettings_ReturnsValueOnSuccess(t *testing.T) {
	img := docker.Image{Name: "ghcr.io/org/repo", Tag: "1.2.3"}
	got, err := NewModuleSettings(img, docker.CreateOptions{})
	if err != nil {
		t.Fatalf("NewModuleSettings: unexpected error: %v", err)
	}
	want := ModuleSettings{Image: img, CreateOptions: docker.CreateOptions{}}
	if got.Image != want.Image {
		t.Errorf("NewModuleSettings(...).Image = %+v, want %+v", got.Image, want.Image)
	}
}

func TestNewModuleSettings_RejectsInvalidImage(t *testing.T) {
	if _, err := NewModuleSettings(docker.Image{}, docker.CreateOptions{}); err == nil {
		t.Error("NewModuleSettings with empty Image.Name: want error, got nil")
	}
}

func TestModuleSettings_ImplementsHasCodec(t *testing.T) {
	ms, err := NewModuleSettings(docker.Image{Name: "alpine", Tag: "latest"}, docker.CreateOptions{})
	if err != nil {
		t.Fatalf("NewModuleSettings: %v", err)
	}
	if err := codex.Validate(ms); err != nil {
		t.Errorf("codex.Validate(ms) = %v, want nil", err)
	}
	raw, err := codex.EncodeSelf(ms)
	if err != nil {
		t.Fatalf("codex.EncodeSelf: %v", err)
	}
	back, err := codex.DecodeAs[ModuleSettings](raw)
	if err != nil {
		t.Fatalf("codex.DecodeAs: %v", err)
	}
	if back.Image != ms.Image {
		t.Errorf("DecodeAs(EncodeSelf(ms)).Image = %+v, want %+v", back.Image, ms.Image)
	}
}
