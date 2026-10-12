package auth

import "testing"

func TestBearerTokenCodec_RoundTrips(t *testing.T) {
	raw, err := BearerTokenCodec.Encode("abc123")
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if raw.(string) != "Bearer abc123" {
		t.Errorf("Encode(%q) = %q, want %q", "abc123", raw, "Bearer abc123")
	}

	token, err := BearerTokenCodec.Decode("Bearer abc123")
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if token != "abc123" {
		t.Errorf("Decode(...) = %q, want %q", token, "abc123")
	}
}

func TestNewCredentials_RequiresNonEmptyPassword(t *testing.T) {
	if _, err := NewCredentials("user", ""); err == nil {
		t.Error("NewCredentials: want error for empty password, got nil")
	}
}

func TestNewCredentials_AllowsEmptyUsername(t *testing.T) {
	creds, err := NewCredentials("", "pw")
	if err != nil {
		t.Fatalf("NewCredentials: %v", err)
	}
	if creds.Password != "pw" {
		t.Errorf("NewCredentials(...) = %+v, want Password=pw", creds)
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

func TestBasicAuthCodec_RoundTrips(t *testing.T) {
	creds := Credentials{Username: "user", Password: "pass"}
	raw, err := BasicAuthCodec.Encode(creds)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	decoded, err := BasicAuthCodec.Decode(raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if decoded != creds {
		t.Errorf("round-trip = %+v, want %+v", decoded, creds)
	}
}
