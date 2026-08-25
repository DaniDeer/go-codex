package dockercompose

import (
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker"
)

func TestComposeHealthcheckCodec_RoundTrip(t *testing.T) {
	raw := map[string]any{
		"test":         []any{"CMD", "curl", "-f", "http://localhost/"},
		"interval":     "30s",
		"timeout":      "5s",
		"start_period": "10s",
		"retries":      float64(3),
	}
	got, err := ComposeHealthcheckCodec.Decode(raw)
	if err != nil {
		t.Fatalf("Decode: unexpected error: %v", err)
	}
	if got.Interval != 30*time.Second {
		t.Errorf("Interval = %v, want 30s", got.Interval)
	}
	if got.Timeout != 5*time.Second {
		t.Errorf("Timeout = %v, want 5s", got.Timeout)
	}
	if got.StartPeriod != 10*time.Second {
		t.Errorf("StartPeriod = %v, want 10s", got.StartPeriod)
	}
	if got.Retries != 3 {
		t.Errorf("Retries = %d, want 3", got.Retries)
	}
	if got.Disable {
		t.Error("Disable = true, want false (not set on the wire)")
	}
}

func TestComposeHealthcheckCodec_Disable(t *testing.T) {
	got, err := ComposeHealthcheckCodec.Decode(map[string]any{"disable": true})
	if err != nil {
		t.Fatalf("Decode: unexpected error: %v", err)
	}
	if !got.Disable {
		t.Error("Disable = false, want true")
	}
}

func TestComposeHealthcheckCodec_RejectsInvalidDuration(t *testing.T) {
	_, err := ComposeHealthcheckCodec.Decode(map[string]any{"interval": "not-a-duration"})
	if err == nil {
		t.Error("Decode with invalid duration: want error, got nil")
	}
}

func TestHealthcheckFromComposeCodec_DecodesToDockerHealthcheck(t *testing.T) {
	raw := map[string]any{
		"test":     []any{"CMD", "curl", "-f", "http://localhost/"},
		"interval": "30s",
		"retries":  float64(3),
	}
	got, err := HealthcheckFromComposeCodec.Decode(raw)
	if err != nil {
		t.Fatalf("Decode: unexpected error: %v", err)
	}
	if got.Interval != 30*time.Second || got.Retries != 3 {
		t.Errorf("got = %+v", got)
	}
}

func TestHealthcheckFromComposeCodec_DisableBecomesNoneSentinel(t *testing.T) {
	got, err := HealthcheckFromComposeCodec.Decode(map[string]any{"disable": true})
	if err != nil {
		t.Fatalf("Decode: unexpected error: %v", err)
	}
	if len(got.Test) != 1 || got.Test[0] != "NONE" {
		t.Errorf("Test = %+v, want [\"NONE\"]", got.Test)
	}
}

func TestHealthcheckFromComposeCodec_EncodeReversesNoneSentinel(t *testing.T) {
	encoded, err := HealthcheckFromComposeCodec.Encode(docker.Healthcheck{Test: []string{"NONE"}})
	if err != nil {
		t.Fatalf("Encode: unexpected error: %v", err)
	}
	obj, ok := encoded.(map[string]any)
	if !ok {
		t.Fatalf("Encode result type = %T", encoded)
	}
	if disable, _ := obj["disable"].(bool); !disable {
		t.Errorf("disable = %v, want true", obj["disable"])
	}
}
