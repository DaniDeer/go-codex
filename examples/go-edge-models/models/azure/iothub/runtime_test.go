package iothub_test

import (
	"testing"

	iothub "github.com/DaniDeer/go-codex/examples/go-edge-models/models/azure/iothub"
)

func TestRegistryCredentialCodec_RoundTrip(t *testing.T) {
	rc := iothub.RegistryCredential{Address: "ghcr.io", Username: "user", Password: "${SOME_PAT}"}
	enc, err := iothub.RegistryCredentialCodec.Encode(rc)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	dec, err := iothub.RegistryCredentialCodec.Decode(enc)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if dec != rc {
		t.Errorf("round trip = %+v, want %+v", dec, rc)
	}
}

func TestRegistryCredentialCodec_UsernameOptional(t *testing.T) {
	rc := iothub.RegistryCredential{Address: "ghcr.io", Password: "${SOME_PAT}"}
	if err := iothub.RegistryCredentialCodec.Validate(rc); err != nil {
		t.Errorf("Validate: %v, want nil (username is optional)", err)
	}
}

func TestRegistryCredentialCodec_RejectsEmptyPassword(t *testing.T) {
	rc := iothub.RegistryCredential{Address: "ghcr.io"}
	if err := iothub.RegistryCredentialCodec.Validate(rc); err == nil {
		t.Error("Validate: want error for empty password, got nil")
	}
}

func TestRegistryCredentialsCodec_RoundTrip(t *testing.T) {
	rcs := iothub.RegistryCredentials{
		"myAlias": {Address: "ghcr.io", Username: "user", Password: "${SOME_PAT}"},
	}
	enc, err := iothub.RegistryCredentialsCodec.Encode(rcs)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	encMap := enc.(map[string]any)
	if _, ok := encMap["myAlias"]; !ok {
		t.Errorf("Encode result = %+v, want alias key present", encMap)
	}
	dec, err := iothub.RegistryCredentialsCodec.Decode(encMap)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if dec["myAlias"].Address != "ghcr.io" {
		t.Errorf("Decode = %+v, want round-tripped alias entry", dec)
	}
}

func TestRuntimeSettingsCodec_RegistryCredentialsOptional(t *testing.T) {
	rs := iothub.RuntimeSettings{MinDockerVersion: "v1.25"}
	if err := iothub.RuntimeSettingsCodec.Validate(rs); err != nil {
		t.Errorf("Validate: %v, want nil (registryCredentials is optional)", err)
	}
}

func TestRuntimeCodec_RoundTrip(t *testing.T) {
	r := iothub.Runtime{
		Settings: iothub.RuntimeSettings{
			MinDockerVersion:    "v1.25",
			RegistryCredentials: iothub.RegistryCredentials{"myAlias": {Address: "ghcr.io", Password: "${PAT}"}},
		},
		Type: "docker",
	}
	enc, err := iothub.RuntimeCodec.Encode(r)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	dec, err := iothub.RuntimeCodec.Decode(enc)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if dec.Settings.MinDockerVersion != "v1.25" || dec.Type != "docker" {
		t.Errorf("round trip = %+v, want %+v", dec, r)
	}
}
