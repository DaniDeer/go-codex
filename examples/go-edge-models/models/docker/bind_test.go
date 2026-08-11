package docker

import (
	"testing"

	"github.com/DaniDeer/go-codex/codex"
)

func TestBindCodec_RoundTrip(t *testing.T) {
	raw := "/etc/ssl/local/pubkey.pem:/etc/traefik/ssl/pubkey.pem:ro"
	got, err := BindCodec.Decode(raw)
	if err != nil {
		t.Fatalf("BindCodec.Decode(%q): %v", raw, err)
	}
	want := Bind{HostPath: "/etc/ssl/local/pubkey.pem", ContainerPath: "/etc/traefik/ssl/pubkey.pem", Mode: "ro"}
	if got != want {
		t.Errorf("Decode(%q) = %+v, want %+v", raw, got, want)
	}
	encoded, err := BindCodec.Encode(got)
	if err != nil {
		t.Fatalf("BindCodec.Encode(%+v): %v", got, err)
	}
	if encoded != raw {
		t.Errorf("Encode(%+v) = %q, want %q", got, encoded, raw)
	}
}

func TestNewBind_ReturnsValueOnSuccess(t *testing.T) {
	got, err := NewBind("/host/path", "/container/path", "ro")
	if err != nil {
		t.Fatalf("NewBind: unexpected error: %v", err)
	}
	want := Bind{HostPath: "/host/path", ContainerPath: "/container/path", Mode: "ro"}
	if got != want {
		t.Errorf("NewBind(...) = %+v, want %+v", got, want)
	}
}

func TestNewBind_AllowsEmptyMode(t *testing.T) {
	got, err := NewBind("/host/path", "/container/path", "")
	if err != nil {
		t.Fatalf("NewBind with empty mode: unexpected error: %v", err)
	}
	if got.Mode != "" {
		t.Errorf("NewBind(...).Mode = %q, want empty", got.Mode)
	}
}

func TestNewBind_RejectsEmptyHostPath(t *testing.T) {
	if _, err := NewBind("", "/container/path", ""); err == nil {
		t.Error("NewBind with empty hostPath: want error, got nil")
	}
}

func TestNewBind_RejectsEmptyContainerPath(t *testing.T) {
	if _, err := NewBind("/host/path", "", ""); err == nil {
		t.Error("NewBind with empty containerPath: want error, got nil")
	}
}

func TestBind_ImplementsHasCodec(t *testing.T) {
	b, err := NewBind("/host/path", "/container/path", "ro")
	if err != nil {
		t.Fatalf("NewBind: %v", err)
	}
	if err := codex.Validate(b); err != nil {
		t.Errorf("codex.Validate(b) = %v, want nil", err)
	}
	raw, err := codex.EncodeSelf(b)
	if err != nil {
		t.Fatalf("codex.EncodeSelf: %v", err)
	}
	back, err := codex.DecodeAs[Bind](raw)
	if err != nil {
		t.Fatalf("codex.DecodeAs: %v", err)
	}
	if back != b {
		t.Errorf("DecodeAs(EncodeSelf(b)) = %+v, want %+v", back, b)
	}
}
