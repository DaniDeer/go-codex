package auth

import "testing"

func TestParseChallengeString_ParsesRealmServiceScope(t *testing.T) {
	ch, err := ParseChallengeString(`Bearer realm="https://auth.example.com/token",service="registry.example.com",scope="repository:my/repo:pull"`)
	if err != nil {
		t.Fatalf("ParseChallengeString: %v", err)
	}
	want := Challenge{Realm: "https://auth.example.com/token", Service: "registry.example.com", Scope: "repository:my/repo:pull"}
	if ch != want {
		t.Errorf("ParseChallengeString(...) = %+v, want %+v", ch, want)
	}
}

func TestParseChallengeString_RejectsMissingBearerPrefix(t *testing.T) {
	if _, err := ParseChallengeString(`Basic realm="x"`); err == nil {
		t.Error("ParseChallengeString: want error for missing Bearer prefix, got nil")
	}
}

func TestFormatChallengeString_RoundTrips(t *testing.T) {
	ch := Challenge{Realm: "https://auth.example.com/token", Service: "registry.example.com", Scope: "repository:my/repo:pull"}
	s, err := FormatChallengeString(ch)
	if err != nil {
		t.Fatalf("FormatChallengeString: %v", err)
	}
	got, err := ParseChallengeString(s)
	if err != nil {
		t.Fatalf("ParseChallengeString(FormatChallengeString(...)): %v", err)
	}
	if got != ch {
		t.Errorf("round-trip = %+v, want %+v", got, ch)
	}
}
