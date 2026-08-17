package usecase

import "testing"

func TestNewName_ReturnsValueOnSuccess(t *testing.T) {
	name, err := NewName("usecase1")
	if err != nil {
		t.Fatalf("NewName: %v", err)
	}
	if name != "usecase1" {
		t.Errorf("NewName = %q, want usecase1", name)
	}
}

func TestNewName_RejectsEmptyString(t *testing.T) {
	if _, err := NewName(""); err == nil {
		t.Error("NewName: want error for empty string, got nil")
	}
}

func TestNameCodec_EncodeDecodeRoundTrip(t *testing.T) {
	name := Name("usecase1")
	raw, err := NameCodec.Encode(name)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := NameCodec.Decode(raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got != name {
		t.Errorf("round-trip = %q, want %q", got, name)
	}
}

func TestNewDeviceID_ReturnsValueOnSuccess(t *testing.T) {
	id, err := NewDeviceID("sensor-1")
	if err != nil {
		t.Fatalf("NewDeviceID: %v", err)
	}
	if id != "sensor-1" {
		t.Errorf("NewDeviceID = %q, want sensor-1", id)
	}
}

func TestNewDeviceID_RejectsEmptyString(t *testing.T) {
	if _, err := NewDeviceID(""); err == nil {
		t.Error("NewDeviceID: want error for empty string, got nil")
	}
}

func TestDeviceIDCodec_EncodeDecodeRoundTrip(t *testing.T) {
	id := DeviceID("sensor-1")
	raw, err := DeviceIDCodec.Encode(id)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := DeviceIDCodec.Decode(raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got != id {
		t.Errorf("round-trip = %q, want %q", got, id)
	}
}
