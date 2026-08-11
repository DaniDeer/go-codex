package versioning

import "testing"

func TestIsSemVer(t *testing.T) {
	valid := []string{"1.2.3", "v2.0.0-rc.1+build.5"}
	invalid := []string{"3.1-debian", "18.04", "latest", ""}
	for _, s := range valid {
		if !IsSemVer(s) {
			t.Errorf("IsSemVer(%q) = false, want true", s)
		}
	}
	for _, s := range invalid {
		if IsSemVer(s) {
			t.Errorf("IsSemVer(%q) = true, want false", s)
		}
	}
}

func TestIsSemVerLike(t *testing.T) {
	valid := []string{"3.1-debian", "18.04", "2024.01.15"}
	invalid := []string{"latest", "not-a-version", ""}
	for _, s := range valid {
		if !IsSemVerLike(s) {
			t.Errorf("IsSemVerLike(%q) = false, want true", s)
		}
	}
	for _, s := range invalid {
		if IsSemVerLike(s) {
			t.Errorf("IsSemVerLike(%q) = true, want false", s)
		}
	}
}

func TestIsSemVerLike_OverlapsIsSemVer(t *testing.T) {
	// Documents IsSemVerLike's own doc comment: strict semver strings also
	// satisfy IsSemVerLike — not mutually exclusive.
	s := "1.2.3"
	if !IsSemVer(s) {
		t.Fatalf("test setup: %q should satisfy IsSemVer", s)
	}
	if !IsSemVerLike(s) {
		t.Errorf("IsSemVerLike(%q) = false, want true (overlap with IsSemVer is documented, not accidental)", s)
	}
}
