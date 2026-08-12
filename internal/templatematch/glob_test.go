package templatematch

import "testing"

func TestIsGlobEnabled(t *testing.T) {
	cases := []struct {
		template string
		want     bool
	}{
		{"readings/{sensorID}/{date}.json", false},
		{"logs/app-*/errors", true},
		{"logs/app-?/errors", true},
		{"logs/app-[0-9]/errors", true},
		{"data/**/errors", true},
		{"data", false},
	}
	for _, c := range cases {
		if got := IsGlobEnabled(c.template); got != c.want {
			t.Errorf("IsGlobEnabled(%q) = %v, want %v", c.template, got, c.want)
		}
	}
}

func TestValidateGlobstarCount(t *testing.T) {
	if err := ValidateGlobstarCount("data/**/errors"); err != nil {
		t.Errorf("ValidateGlobstarCount(one **) = %v, want nil", err)
	}
	if err := ValidateGlobstarCount("a/**/b/**/c"); err == nil {
		t.Error("ValidateGlobstarCount(two **) = nil, want error")
	}
}

func TestLiteralPrefix(t *testing.T) {
	cases := []struct {
		template string
		want     string
	}{
		{"logs/app-*/errors", "logs"},
		{"configs/{env}/app-*", "configs"},
		{"*/errors", ""},
		{"**/secret.json", ""},
		{"a/b/c", "a/b/c"},
	}
	for _, c := range cases {
		if got := LiteralPrefix(c.template); got != c.want {
			t.Errorf("LiteralPrefix(%q) = %q, want %q", c.template, got, c.want)
		}
	}
}

func TestMatchGlob_SingleSegmentStar(t *testing.T) {
	vars, err := MatchGlob("logs/app-*/errors", "logs/app-1/errors", wrapMismatchTest)
	if err != nil {
		t.Fatalf("MatchGlob: %v", err)
	}
	if len(vars) != 0 {
		t.Errorf("vars = %v, want empty", vars)
	}
	if _, err := MatchGlob("logs/app-*/errors", "logs/other/errors", wrapMismatchTest); err == nil {
		t.Fatal("MatchGlob: want mismatch for non-matching literal-adjacent glob segment")
	}
}

func TestMatchGlob_QuestionMarkAndCharClass(t *testing.T) {
	if _, err := MatchGlob("logs/app-?", "logs/app-1", wrapMismatchTest); err != nil {
		t.Errorf("MatchGlob(?): %v", err)
	}
	if _, err := MatchGlob("logs/app-?", "logs/app-10", wrapMismatchTest); err == nil {
		t.Error("MatchGlob(?): want mismatch for two-char segment")
	}
	if _, err := MatchGlob("logs/app-[0-9]", "logs/app-5", wrapMismatchTest); err != nil {
		t.Errorf("MatchGlob([0-9]): %v", err)
	}
	if _, err := MatchGlob("logs/app-[0-9]", "logs/app-x", wrapMismatchTest); err == nil {
		t.Error("MatchGlob([0-9]): want mismatch for non-digit")
	}
}

func TestMatchGlob_GlobstarMatchesZeroOrMoreSegments(t *testing.T) {
	cases := []string{"data/errors", "data/a/errors", "data/a/b/errors"}
	for _, concrete := range cases {
		if _, err := MatchGlob("data/**/errors", concrete, wrapMismatchTest); err != nil {
			t.Errorf("MatchGlob(data/**/errors, %q): %v", concrete, err)
		}
	}
	if _, err := MatchGlob("data/**/errors", "data/a/notmatched", wrapMismatchTest); err == nil {
		t.Error("MatchGlob: want mismatch for wrong suffix")
	}
}

func TestMatchGlob_GlobstarZeroSegmentsAtRoot(t *testing.T) {
	if _, err := MatchGlob("data/**", "data", wrapMismatchTest); err != nil {
		t.Errorf("MatchGlob(data/**, data): %v", err)
	}
}

func TestMatchGlob_NamedVarStillCaptured(t *testing.T) {
	vars, err := MatchGlob("logs/{env}/app-*", "logs/prod/app-1", wrapMismatchTest)
	if err != nil {
		t.Fatalf("MatchGlob: %v", err)
	}
	if vars["env"] != "prod" {
		t.Errorf("vars[env] = %q, want %q", vars["env"], "prod")
	}
}

func TestMatchGlob_NoGlobstar_SegmentCountMustMatchExactly(t *testing.T) {
	if _, err := MatchGlob("logs/app-*", "logs/app-1/extra", wrapMismatchTest); err == nil {
		t.Error("MatchGlob: want mismatch for extra segment with no globstar")
	}
}
