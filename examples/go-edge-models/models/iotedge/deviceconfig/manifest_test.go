package deviceconfig

import "testing"

func TestManifestCodec_EncodeDecodeRoundTrip(t *testing.T) {
	dm := Manifest{DisplayName: "Sensor 42", Enabled: true}

	encoded, err := ManifestCodec.Encode(dm)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	decoded, err := ManifestCodec.Decode(encoded)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if decoded != dm {
		t.Errorf("decoded = %+v, want %+v", decoded, dm)
	}
}

func TestManifestCodec_RejectsEmptyDisplayName(t *testing.T) {
	dm := Manifest{DisplayName: "", Enabled: true}
	if err := ManifestCodec.Validate(dm); err == nil {
		t.Error("Validate: want error for empty DisplayName, got nil")
	}
}
