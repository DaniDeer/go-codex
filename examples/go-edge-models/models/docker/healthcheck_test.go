package docker

import (
	"testing"
	"time"
)

func TestHealthcheckCLICodec_RoundTrip(t *testing.T) {
	h := Healthcheck{
		Test:        []string{"CMD", "curl", "-f", "http://localhost/"},
		Interval:    30 * time.Second,
		Timeout:     5 * time.Second,
		StartPeriod: 10 * time.Second,
		Retries:     3,
	}

	raw, err := HealthcheckCLICodec.Encode(h)
	if err != nil {
		t.Fatalf("Encode: unexpected error: %v", err)
	}
	obj, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("Encode result type = %T, want map[string]any", raw)
	}
	if obj["Interval"] != "30s" {
		t.Errorf("Interval = %v, want %q", obj["Interval"], "30s")
	}
	if obj["Timeout"] != "5s" {
		t.Errorf("Timeout = %v, want %q", obj["Timeout"], "5s")
	}

	back, err := HealthcheckCLICodec.Decode(raw)
	if err != nil {
		t.Fatalf("Decode: unexpected error: %v", err)
	}
	if back.Interval != 30*time.Second {
		t.Errorf("Interval = %v, want 30s", back.Interval)
	}
	if back.Timeout != 5*time.Second {
		t.Errorf("Timeout = %v, want 5s", back.Timeout)
	}
	if back.StartPeriod != 10*time.Second {
		t.Errorf("StartPeriod = %v, want 10s", back.StartPeriod)
	}
	if back.Retries != 3 {
		t.Errorf("Retries = %d, want 3", back.Retries)
	}
}

func TestHealthcheckCLICodec_DecodeRejectsInvalidDuration(t *testing.T) {
	_, err := HealthcheckCLICodec.Decode(map[string]any{
		"Interval": "not-a-duration",
	})
	if err == nil {
		t.Error("Decode with invalid duration string: want error, got nil")
	}
}

func TestCLIDurationCodec_IsCoreDuration(t *testing.T) {
	got, err := CLIDurationCodec.Decode("1m30s")
	if err != nil {
		t.Fatalf("Decode: unexpected error: %v", err)
	}
	want := time.Minute + 30*time.Second
	if got != want {
		t.Errorf("Decode(%q) = %v, want %v", "1m30s", got, want)
	}
}
