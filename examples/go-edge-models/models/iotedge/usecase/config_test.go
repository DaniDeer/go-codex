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

func TestNewBasePath_ReturnsValueOnSuccess(t *testing.T) {
	bp, err := NewBasePath("/tmp/edge-configs")
	if err != nil {
		t.Fatalf("NewBasePath: %v", err)
	}
	if bp != "/tmp/edge-configs" {
		t.Errorf("NewBasePath = %q, want /tmp/edge-configs", bp)
	}
}

func TestNewBasePath_RejectsEmptyString(t *testing.T) {
	if _, err := NewBasePath(""); err == nil {
		t.Error("NewBasePath: want error for empty string, got nil")
	}
}

func TestNewBasePath_CanonicalizesViaFilepathClean(t *testing.T) {
	bp, err := NewBasePath("/tmp/./edge-configs/../edge-configs")
	if err != nil {
		t.Fatalf("NewBasePath: %v", err)
	}
	if bp != "/tmp/edge-configs" {
		t.Errorf("NewBasePath = %q, want cleaned /tmp/edge-configs", bp)
	}
}

func TestBasePathCodec_EncodeDecodeRoundTrip(t *testing.T) {
	bp := BasePath("/tmp/edge-configs")
	raw, err := BasePathCodec.Encode(bp)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := BasePathCodec.Decode(raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got != bp {
		t.Errorf("round-trip = %q, want %q", got, bp)
	}
}

func TestBasePathCodec_DecodeRejectsEmptyString(t *testing.T) {
	if _, err := BasePathCodec.Decode(""); err == nil {
		t.Error("Decode(\"\"): want error, got nil")
	}
}

func TestBasePathCodec_SchemaHasDescription(t *testing.T) {
	if BasePathCodec.Schema.Description == "" {
		t.Error("BasePathCodec.Schema.Description is empty, want the use-case-layout description")
	}
}

// ── Path patterns ────────────────────────────────────────────────────────

func TestBaselinePathPattern_String(t *testing.T) {
	if got := baselinePathPattern.String(); got != "baseline/baseline.json" {
		t.Errorf("baselinePathPattern.String() = %q, want %q", got, "baseline/baseline.json")
	}
}

func TestUseCasePathPattern_String(t *testing.T) {
	if got := useCasePathPattern.String(); got != "usecases/{usecase_name}.json" {
		t.Errorf("useCasePathPattern.String() = %q, want %q", got, "usecases/{usecase_name}.json")
	}
}

func TestUseCasePathPattern_Build(t *testing.T) {
	got, err := useCasePathPattern.Build(Name("usecase1"))
	if err != nil {
		t.Fatalf("Build: unexpected error: %v", err)
	}
	if want := "usecases/usecase1.json"; got != want {
		t.Errorf("Build = %q, want %q", got, want)
	}
}

func TestUseCasePathPattern_Build_RejectsInvalidName(t *testing.T) {
	if _, err := useCasePathPattern.Build(Name("")); err == nil {
		t.Error("Build(empty Name): want error, got nil")
	}
}

func TestDeviceDirPathPattern_Build(t *testing.T) {
	got, err := deviceDirPathPattern.Build(Name("usecase1"))
	if err != nil {
		t.Fatalf("Build: unexpected error: %v", err)
	}
	if want := "devices/usecase1"; got != want {
		t.Errorf("Build = %q, want %q", got, want)
	}
}

func TestDeviceFilePathPattern_Build(t *testing.T) {
	got, err := deviceFilePathPattern.Build(deviceFileVars{Name: Name("usecase1"), DeviceID: DeviceID("sensor-1")})
	if err != nil {
		t.Fatalf("Build: unexpected error: %v", err)
	}
	if want := "devices/usecase1/sensor-1.json"; got != want {
		t.Errorf("Build = %q, want %q", got, want)
	}
}

func TestDeviceFilePathPattern_Build_RejectsInvalidDeviceID(t *testing.T) {
	if _, err := deviceFilePathPattern.Build(deviceFileVars{Name: Name("usecase1"), DeviceID: DeviceID("")}); err == nil {
		t.Error("Build(valid Name, empty DeviceID): want error, got nil")
	}
}

func TestUseCaseEntryShape_String(t *testing.T) {
	if got := useCaseEntryShape.String(); got != "{useCase}.json" {
		t.Errorf("useCaseEntryShape.String() = %q, want %q", got, "{useCase}.json")
	}
}

func TestDeviceEntryShape_String(t *testing.T) {
	if got := deviceEntryShape.String(); got != "{device_id}.json" {
		t.Errorf("deviceEntryShape.String() = %q, want %q", got, "{device_id}.json")
	}
}

// ── Path construction helpers ────────────────────────────────────────────

func TestBaselineFilePath(t *testing.T) {
	if got, want := baselineFilePath(BasePath("/tmp/edge")), "/tmp/edge/baseline/baseline.json"; got != want {
		t.Errorf("baselineFilePath = %q, want %q", got, want)
	}
}

func TestUseCasesDirPath(t *testing.T) {
	if got, want := useCasesDirPath(BasePath("/tmp/edge")), "/tmp/edge/usecases"; got != want {
		t.Errorf("useCasesDirPath = %q, want %q", got, want)
	}
}

func TestUseCaseFilePathTemplate(t *testing.T) {
	if got, want := useCaseFilePathTemplate(BasePath("/tmp/edge")), "/tmp/edge/usecases/{usecase_name}.json"; got != want {
		t.Errorf("useCaseFilePathTemplate = %q, want %q", got, want)
	}
}

func TestDeviceFilePathTemplate(t *testing.T) {
	if got, want := deviceFilePathTemplate(BasePath("/tmp/edge")), "/tmp/edge/devices/{usecase_name}/{device_id}.json"; got != want {
		t.Errorf("deviceFilePathTemplate = %q, want %q", got, want)
	}
}

func TestDeviceDirPathTemplate(t *testing.T) {
	if got, want := deviceDirPathTemplate(BasePath("/tmp/edge")), "/tmp/edge/devices/{usecase_name}"; got != want {
		t.Errorf("deviceDirPathTemplate = %q, want %q", got, want)
	}
}
