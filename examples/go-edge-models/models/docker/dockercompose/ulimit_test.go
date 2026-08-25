package dockercompose

import (
	"testing"

	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker"
)

func TestComposeUlimitCodec_DecodesBareInt(t *testing.T) {
	got, err := ComposeUlimitCodec.Decode(float64(1024))
	if err != nil {
		t.Fatalf("Decode: unexpected error: %v", err)
	}
	want := ComposeUlimit{Soft: 1024, Hard: 1024}
	if got != want {
		t.Errorf("Decode(1024) = %+v, want %+v", got, want)
	}
}

func TestComposeUlimitCodec_DecodesObject(t *testing.T) {
	got, err := ComposeUlimitCodec.Decode(map[string]any{
		"soft": float64(1024),
		"hard": float64(2048),
	})
	if err != nil {
		t.Fatalf("Decode: unexpected error: %v", err)
	}
	want := ComposeUlimit{Soft: 1024, Hard: 2048}
	if got != want {
		t.Errorf("Decode(object) = %+v, want %+v", got, want)
	}
}

func TestComposeUlimitCodec_EncodesBareIntWhenEqual(t *testing.T) {
	raw, err := ComposeUlimitCodec.Encode(ComposeUlimit{Soft: 1024, Hard: 1024})
	if err != nil {
		t.Fatalf("Encode: unexpected error: %v", err)
	}
	n, ok := raw.(int64)
	if !ok || n != 1024 {
		t.Errorf("Encode(equal soft/hard) = %v (%T), want int64(1024)", raw, raw)
	}
}

func TestComposeUlimitCodec_EncodesObjectWhenDifferent(t *testing.T) {
	raw, err := ComposeUlimitCodec.Encode(ComposeUlimit{Soft: 1024, Hard: 2048})
	if err != nil {
		t.Fatalf("Encode: unexpected error: %v", err)
	}
	obj, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("Encode(different soft/hard) type = %T, want map[string]any", raw)
	}
	if obj["soft"] != int64(1024) || obj["hard"] != int64(2048) {
		t.Errorf("Encode(different soft/hard) = %+v, want soft=1024 hard=2048", obj)
	}
}

func TestComposeUlimitCodec_RejectsInvalid(t *testing.T) {
	if _, err := ComposeUlimitCodec.Decode("not-a-ulimit"); err == nil {
		t.Error("Decode(string): want error, got nil")
	}
}

func TestUlimitsCodec_DecodesToDockerUlimits(t *testing.T) {
	got, err := UlimitsCodec.Decode(map[string]any{
		"nofile": float64(1024),
	})
	if err != nil {
		t.Fatalf("Decode: unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].Name != "nofile" || got[0].Soft != 1024 || got[0].Hard != 1024 {
		t.Errorf("got = %+v", got)
	}
}

func TestUlimitsCodec_RejectsInvalidName(t *testing.T) {
	if _, err := UlimitsCodec.Decode(map[string]any{"not-a-real-limit": float64(1)}); err == nil {
		t.Error("Decode with invalid ulimit name: want error, got nil")
	}
}

func TestUlimitsCodec_EncodeRoundTrip(t *testing.T) {
	encoded, err := UlimitsCodec.Encode([]docker.Ulimit{{Name: "nofile", Soft: 1024, Hard: 2048}})
	if err != nil {
		t.Fatalf("Encode: unexpected error: %v", err)
	}
	obj, ok := encoded.(map[string]any)
	if !ok {
		t.Fatalf("Encode result type = %T", encoded)
	}
	if _, ok := obj["nofile"]; !ok {
		t.Errorf("encoded = %+v, want \"nofile\" key", obj)
	}
}
