package versioning

import "testing"

func TestParse_Classification(t *testing.T) {
	tests := []struct {
		name   string
		value  string
		bucket int // 0=SemVer, 1=SemVerLike, 2=Other
	}{
		{"plain semver", "1.2.3", 0},
		{"semver with v prefix", "v2.0.0", 0},
		{"semver with prerelease", "1.2.3-rc.1", 0},
		{"semver with build metadata", "1.2.3+build.5", 0},
		// Real-world Docker tag: 3 numeric parts + numeric prerelease —
		// looks like it could ALSO match the semver-like shape, but must
		// be claimed by the semver branch since it's tried first.
		{"semver claims dash-numeric prerelease over semver-like", "3.1.15-18", 0},
		{"semver-like two parts with suffix", "3.1-debian", 1},
		{"semver-like two parts no suffix", "18.04", 1},
		{"semver-like one part", "20", 1},
		{"semver-like four parts", "2024.01.15.2", 1},
		{"opaque latest", "latest", 2},
		{"opaque stable", "stable", 2},
		{"opaque arbitrary hash", "sha-abcdef1", 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Parse(tt.value)
			if bucketOf(got) != tt.bucket {
				t.Errorf("Parse(%q) bucket = %d, want %d (rank=%+v)", tt.value, bucketOf(got), tt.bucket, got)
			}
		})
	}
}

func TestParse_SemVerFields(t *testing.T) {
	got := Parse("v1.2.3-rc.1+build.5")
	if got.SemVer == nil {
		t.Fatalf("Parse: want SemVer branch, got %+v", got)
	}
	want := SemVerRank{Major: 1, Minor: 2, Patch: 3, Prerelease: "rc.1", Raw: "v1.2.3-rc.1+build.5"}
	if *got.SemVer != want {
		t.Errorf("Parse(...).SemVer = %+v, want %+v", *got.SemVer, want)
	}
}

func TestParse_SemVerLikeFields(t *testing.T) {
	got := Parse("3.1-debian")
	if got.SemVerLike == nil {
		t.Fatalf("Parse: want SemVerLike branch, got %+v", got)
	}
	if len(got.SemVerLike.Parts) != 2 || got.SemVerLike.Parts[0] != 3 || got.SemVerLike.Parts[1] != 1 {
		t.Errorf("SemVerLike.Parts = %v, want [3 1]", got.SemVerLike.Parts)
	}
	if got.SemVerLike.Suffix != "debian" {
		t.Errorf("SemVerLike.Suffix = %q, want %q", got.SemVerLike.Suffix, "debian")
	}
}

func TestParse_SemVerLikeMultiHyphenSuffix(t *testing.T) {
	// Real-world Docker tag "3.1.15-16-minimal" — only the FIRST hyphen
	// splits numeric parts from suffix; the rest of the string (which may
	// itself contain hyphens/dots) becomes the whole suffix as-is.
	got := Parse("20.04-16-minimal")
	if got.SemVerLike == nil {
		t.Fatalf("Parse: want SemVerLike branch, got %+v", got)
	}
	if got.SemVerLike.Suffix != "16-minimal" {
		t.Errorf("SemVerLike.Suffix = %q, want %q", got.SemVerLike.Suffix, "16-minimal")
	}
}

func TestParse_OtherFields(t *testing.T) {
	got := Parse("latest")
	if got.Other == nil || *got.Other != "latest" {
		t.Errorf("Parse(\"latest\").Other = %v, want \"latest\"", got.Other)
	}
}

func TestParse_GenericOverNamedStringType(t *testing.T) {
	type fakeTag string
	got := Parse(fakeTag("2.0.0"))
	if got.SemVer == nil {
		t.Errorf("Parse(fakeTag(\"2.0.0\")) = %+v, want SemVer branch (generic ~string must work on non-Docker types too)", got)
	}
}

func TestVersionCodec_EncodeRoundTrip(t *testing.T) {
	cases := []string{"1.2.3", "1.2.3-rc.1", "3.1-debian", "18.04", "latest"}
	for _, value := range cases {
		rank := Parse(value)
		raw, err := VersionCodec.Encode(rank)
		if err != nil {
			t.Fatalf("Encode(%+v): unexpected error: %v", rank, err)
		}
		s, ok := raw.(string)
		if !ok {
			t.Fatalf("Encode(%+v) = %T, want string", rank, raw)
		}
		back, err := VersionCodec.Decode(s)
		if err != nil {
			t.Fatalf("Decode(%q): unexpected error: %v", s, err)
		}
		if bucketOf(back) != bucketOf(rank) {
			t.Errorf("round-trip bucket mismatch for %q: got %d, want %d", value, bucketOf(back), bucketOf(rank))
		}
	}
}

func TestCompare_BucketPrecedence(t *testing.T) {
	semver := Parse("1.0.0")
	semverLike := Parse("18.04")
	other := Parse("latest")

	if Compare(semver, semverLike) >= 0 {
		t.Error("Compare: SemVer should rank before SemVerLike")
	}
	if Compare(semverLike, other) >= 0 {
		t.Error("Compare: SemVerLike should rank before Other")
	}
	if Compare(semver, other) >= 0 {
		t.Error("Compare: SemVer should rank before Other")
	}
}

func TestCompare_SemVerNumericDescending(t *testing.T) {
	v2 := Parse("2.0.0")
	v10 := Parse("10.0.0")
	// 10.0.0 is "more recent" than 2.0.0 — must sort BEFORE it (negative).
	if Compare(v10, v2) >= 0 {
		t.Error("Compare: 10.0.0 should rank before 2.0.0 (numeric, not lexicographic)")
	}
}

func TestCompare_ReleaseBeforePrerelease(t *testing.T) {
	release := Parse("1.2.3")
	prerelease := Parse("1.2.3-rc.1")
	if Compare(release, prerelease) >= 0 {
		t.Error("Compare: a release should rank before its own prerelease")
	}
}

func TestCompare_SemVerLikeNumericDescending(t *testing.T) {
	a := Parse("18.04")
	b := Parse("20.04")
	if Compare(b, a) >= 0 {
		t.Error("Compare: 20.04 should rank before 18.04")
	}
}

func TestCompare_OtherAlphabetical(t *testing.T) {
	a := Parse("alpha")
	b := Parse("beta")
	if Compare(a, b) >= 0 {
		t.Error("Compare: \"alpha\" should sort before \"beta\" (alphabetical)")
	}
}
