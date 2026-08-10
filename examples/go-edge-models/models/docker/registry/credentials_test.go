package registry

import "testing"

// This file tests credentials.go's pure codecs — no HTTP, no auth flow
// (that's the sibling app/registry package's auth_test.go instead).

func TestRegistryCredentialsCodec_RejectsUnknownRegistryHost(t *testing.T) {
	err := RegistryCredentialsCodec.Validate(RegistryCredentials{
		"quay.io": {Username: "u", Password: "p"},
	})
	if err == nil {
		t.Fatal("RegistryCredentialsCodec.Validate: want error for unknown registry host, got nil")
	}
}

func TestRegistryCredentialsCodec_RoundTrip(t *testing.T) {
	creds := RegistryCredentials{
		dockerHubRegistryHost: {Username: "docker-user", Password: "docker-pass"},
		ghcrRegistryHost:      {Username: "", Password: "ghp_examplePAT"},
		mcrRegistryHost:       {Username: "mcr-user", Password: "mcr-pass"},
	}

	encoded, err := RegistryCredentialsCodec.Encode(creds)
	if err != nil {
		t.Fatalf("RegistryCredentialsCodec.Encode: %v", err)
	}
	decoded, err := RegistryCredentialsCodec.Decode(encoded)
	if err != nil {
		t.Fatalf("RegistryCredentialsCodec.Decode: %v", err)
	}
	if len(decoded) != len(creds) {
		t.Fatalf("decoded = %+v, want %+v", decoded, creds)
	}
	for host, want := range creds {
		if got := decoded[host]; got != want {
			t.Errorf("decoded[%q] = %+v, want %+v", host, got, want)
		}
	}
}

func TestCredentialsCodec_RejectsEmptyPasswordButAllowsEmptyUsername(t *testing.T) {
	// GHCR (and similar) authenticate correctly with an empty/arbitrary
	// username and the PAT carried entirely in Password — Username must
	// stay unconstrained while Password remains required.
	if err := CredentialsCodec.Validate(Credentials{Username: "", Password: "ghp_examplePAT"}); err != nil {
		t.Errorf("CredentialsCodec.Validate with empty Username: want nil error, got %v", err)
	}
	if err := CredentialsCodec.Validate(Credentials{Username: "user", Password: ""}); err == nil {
		t.Error("CredentialsCodec.Validate with empty Password: want error, got nil")
	}
}
