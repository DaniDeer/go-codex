package iothub_test

import (
	"testing"

	iothub "github.com/DaniDeer/go-codex/examples/go-edge-models/models/azure/iothub"
)

func TestSchemaVersionCodec_AcceptsKnownVersions(t *testing.T) {
	for _, ver := range []string{"1.0", "1.1"} {
		got, err := iothub.SchemaVersionCodec.Decode(ver)
		if err != nil {
			t.Errorf("Decode(%q): %v", ver, err)
		}
		if string(got) != ver {
			t.Errorf("Decode(%q) = %q, want %q", ver, got, ver)
		}
	}
}

func TestSchemaVersionCodec_RejectsUnknownVersion(t *testing.T) {
	if _, err := iothub.SchemaVersionCodec.Decode("9.9"); err == nil {
		t.Error("Decode(\"9.9\"): want error, got nil")
	}
}
