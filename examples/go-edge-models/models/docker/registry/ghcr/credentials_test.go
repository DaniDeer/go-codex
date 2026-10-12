package ghcr

import "testing"

func TestNewCredentials_RequiresNonEmptyUsername(t *testing.T) {
	if _, err := NewCredentials("", "pw"); err == nil {
		t.Error("NewCredentials: want error for empty username, got nil")
	}
}

func TestNewCredentials_RequiresNonEmptyPassword(t *testing.T) {
	if _, err := NewCredentials("user", ""); err == nil {
		t.Error("NewCredentials: want error for empty password, got nil")
	}
}

func TestNewCredentials_AcceptsValidInput(t *testing.T) {
	creds, err := NewCredentials("user", "pw")
	if err != nil {
		t.Fatalf("NewCredentials: %v", err)
	}
	if creds.Username != "user" || creds.Password != "pw" {
		t.Errorf("NewCredentials(...) = %+v, want Username=user Password=pw", creds)
	}
}

func TestCredentialsCodec_RoundTrips(t *testing.T) {
	creds := Credentials{Username: "user", Password: "pw"}
	raw, err := CredentialsCodec.Encode(creds)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	decoded, err := CredentialsCodec.Decode(raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if decoded != creds {
		t.Errorf("round-trip = %+v, want %+v", decoded, creds)
	}
}
