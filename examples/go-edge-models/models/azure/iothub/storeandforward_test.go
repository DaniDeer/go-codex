package iothub_test

import (
	"testing"

	iothub "github.com/DaniDeer/go-codex/examples/go-edge-models/models/azure/iothub"
)

func TestStoreAndForwardConfigurationCodec_RoundTrip(t *testing.T) {
	sf := iothub.StoreAndForwardConfiguration{TimeToLiveSecs: 259200}
	enc, err := iothub.StoreAndForwardConfigurationCodec.Encode(sf)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	dec, err := iothub.StoreAndForwardConfigurationCodec.Decode(enc)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if dec != sf {
		t.Errorf("round trip = %+v, want %+v", dec, sf)
	}
}

func TestStoreAndForwardConfigurationCodec_RejectsNonPositive(t *testing.T) {
	sf := iothub.StoreAndForwardConfiguration{TimeToLiveSecs: 0}
	if err := iothub.StoreAndForwardConfigurationCodec.Validate(sf); err == nil {
		t.Error("Validate: want error for non-positive TimeToLiveSecs, got nil")
	}
}
