package codex_test

import (
	"testing"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/validate"
)

// hcProfile is a small test-local type implementing codex.HasCodec[hcProfile]
// — mirrors examples/construction's Profile, but with a Codec() method so
// it can be used with the generic HasCodec helpers under test.
type hcProfile struct {
	Handle string
	Score  int
}

var hcProfileCodec = codex.Struct[hcProfile](
	codex.RequiredField("handle",
		codex.String().Refine(validate.MinLen(3)),
		func(p hcProfile) string { return p.Handle },
		func(p *hcProfile, v string) { p.Handle = v },
	),
	codex.RequiredField("score",
		codex.Int().Refine(validate.RangeInt(0, 100)),
		func(p hcProfile) int { return p.Score },
		func(p *hcProfile, v int) { p.Score = v },
	),
)

// Codec implements codex.HasCodec[hcProfile] — a stateless, package-level
// codec reference returned via a value receiver, per HasCodec's own
// documented convention.
func (hcProfile) Codec() codex.Codec[hcProfile] { return hcProfileCodec }

func TestHasCodec_Validate_PassesForValidValue(t *testing.T) {
	p := hcProfile{Handle: "alice", Score: 42}
	if err := codex.Validate(p); err != nil {
		t.Errorf("Validate(%+v) = %v, want nil", p, err)
	}
}

func TestHasCodec_Validate_FailsForInvalidValue(t *testing.T) {
	p := hcProfile{Handle: "al", Score: 42} // handle too short
	if err := codex.Validate(p); err == nil {
		t.Error("Validate: want error for short handle, got nil")
	}
}

func TestHasCodec_New_ReturnsValueOnSuccess(t *testing.T) {
	p := hcProfile{Handle: "alice", Score: 42}
	got, err := codex.New(p)
	if err != nil {
		t.Fatalf("New(%+v): unexpected error: %v", p, err)
	}
	if got != p {
		t.Errorf("New(%+v) = %+v, want %+v", p, got, p)
	}
}

func TestHasCodec_New_ReturnsErrorAndZeroValueOnFailure(t *testing.T) {
	p := hcProfile{Handle: "alice", Score: 999} // score out of range
	got, err := codex.New(p)
	if err == nil {
		t.Fatal("New: want error for out-of-range score, got nil")
	}
	if got != (hcProfile{}) {
		t.Errorf("New (failure path) = %+v, want zero value", got)
	}
}

func TestHasCodec_EncodeSelf_And_DecodeAs_RoundTrip(t *testing.T) {
	p := hcProfile{Handle: "alice", Score: 42}

	encoded, err := codex.EncodeSelf(p)
	if err != nil {
		t.Fatalf("EncodeSelf(%+v): unexpected error: %v", p, err)
	}

	decoded, err := codex.DecodeAs[hcProfile](encoded)
	if err != nil {
		t.Fatalf("DecodeAs: unexpected error: %v", err)
	}
	if decoded != p {
		t.Errorf("DecodeAs(EncodeSelf(%+v)) = %+v, want %+v", p, decoded, p)
	}
}

func TestHasCodec_DecodeAs_PropagatesError(t *testing.T) {
	if _, err := codex.DecodeAs[hcProfile]("not an object"); err == nil {
		t.Error("DecodeAs: want error for non-object input, got nil")
	}
}

func TestHasCodec_SchemaOf_MatchesCodecSchema(t *testing.T) {
	got := codex.SchemaOf[hcProfile]()
	want := hcProfileCodec.Schema
	if got.Type != want.Type || len(got.Properties) != len(want.Properties) {
		t.Errorf("SchemaOf() = %+v, want %+v", got, want)
	}
}
