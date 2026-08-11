package docker

import (
	"reflect"
	"testing"

	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/versioning"
)

// These tests prove the delegation to models/versioning preserves
// behavior byte-for-byte — same cases as the original, pre-generalization
// implementation. See models/versioning's own test suite for deeper
// coverage of the classification/comparison logic itself.

func TestFilterTags_DefaultVersionDescAllTags(t *testing.T) {
	in := []Tag{"1.0.0", "2.0.0", "latest", "1.5.0"}
	got := FilterTags(in)
	want := []Tag{"2.0.0", "1.5.0", "1.0.0", "latest"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("FilterTags(%v) = %v, want %v", in, got, want)
	}
}

func TestFilterTags_DoesNotMutateInput(t *testing.T) {
	in := []Tag{"2.0.0", "1.0.0"}
	original := append([]Tag(nil), in...)
	_ = FilterTags(in)
	if !reflect.DeepEqual(in, original) {
		t.Errorf("FilterTags mutated input: got %v, want unchanged %v", in, original)
	}
}

func TestFilterTags_WithLimit(t *testing.T) {
	in := []Tag{"1.0.0", "3.0.0", "2.0.0"}
	got := FilterTags(in, WithLimit(2))
	want := []Tag{"3.0.0", "2.0.0"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("FilterTags with limit 2 = %v, want %v", got, want)
	}
}

func TestFilterTags_LimitZeroOrNegativeMeansAll(t *testing.T) {
	in := []Tag{"1.0.0", "2.0.0"}
	for _, n := range []int{0, -1, -100} {
		got := FilterTags(in, WithLimit(n))
		if len(got) != 2 {
			t.Errorf("FilterTags with limit %d = %v, want all 2 tags", n, got)
		}
	}
}

func TestFilterTags_LimitLargerThanLength(t *testing.T) {
	in := []Tag{"1.0.0", "2.0.0"}
	got := FilterTags(in, WithLimit(100))
	if len(got) != 2 {
		t.Errorf("FilterTags with limit 100 = %v, want all 2 tags", got)
	}
}

func TestFilterTags_SortAlphabetical(t *testing.T) {
	in := []Tag{"2.0.0", "1.0.0", "latest"}
	got := FilterTags(in, WithSort(SortAlphabetical))
	want := []Tag{"1.0.0", "2.0.0", "latest"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("FilterTags(SortAlphabetical) = %v, want %v", got, want)
	}
}

func TestFilterTags_SortNonePassesThrough(t *testing.T) {
	in := []Tag{"latest", "2.0.0", "1.0.0"}
	got := FilterTags(in, WithSort(SortNone))
	if !reflect.DeepEqual(got, in) {
		t.Errorf("FilterTags(SortNone) = %v, want unchanged order %v", got, in)
	}
}

func TestFilterTags_MixedBucketOrdering(t *testing.T) {
	in := []Tag{"latest", "3.1-debian", "1.0.0", "stable", "18.04", "2.0.0"}
	got := FilterTags(in)
	want := []Tag{"2.0.0", "1.0.0", "18.04", "3.1-debian", "latest", "stable"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("FilterTags(mixed) = %v, want %v", got, want)
	}
}

func TestFilterTags_SortAndLimitCombined(t *testing.T) {
	in := []Tag{"latest", "3.1-debian", "1.0.0", "2.0.0"}
	got := FilterTags(in, WithLimit(2))
	want := []Tag{"2.0.0", "1.0.0"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("FilterTags(sort+limit) = %v, want %v", got, want)
	}
}

func TestFilterTags_EmptyInput(t *testing.T) {
	got := FilterTags(nil)
	if len(got) != 0 {
		t.Errorf("FilterTags(nil) = %v, want empty", got)
	}
}

// TestSortMode_IsAliasOfVersioningSortMode proves the type-alias identity
// (not just equal underlying values) between docker's re-exported
// constants and models/versioning's originals — a genuine `type X = Y`
// alias, not a separate lookalike type.
func TestSortMode_IsAliasOfVersioningSortMode(t *testing.T) {
	var _ versioning.SortMode = SortByVersionDesc
	var _ SortMode = versioning.SortByVersionDesc
	if SortByVersionDesc != versioning.SortByVersionDesc {
		t.Errorf("SortByVersionDesc = %v, want %v", SortByVersionDesc, versioning.SortByVersionDesc)
	}
	if SortAlphabetical != versioning.SortAlphabetical {
		t.Errorf("SortAlphabetical = %v, want %v", SortAlphabetical, versioning.SortAlphabetical)
	}
	if SortNone != versioning.SortNone {
		t.Errorf("SortNone = %v, want %v", SortNone, versioning.SortNone)
	}
}

func TestParseTagRank_DelegatesToVersioningParse(t *testing.T) {
	// Version's fields are pointers, so two independently parsed values
	// with the same content are never == (different pointer identity) —
	// reflect.DeepEqual compares pointee values instead.
	got := ParseTagRank("1.2.3")
	want := versioning.Parse(Tag("1.2.3"))
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseTagRank(\"1.2.3\") = %+v, want %+v", got, want)
	}
}

func TestCompareTagRank_DelegatesToVersioningCompare(t *testing.T) {
	a, b := ParseTagRank("2.0.0"), ParseTagRank("1.0.0")
	if CompareTagRank(a, b) != versioning.Compare(a, b) {
		t.Error("CompareTagRank should delegate to versioning.Compare unchanged")
	}
}
