package versioning

import (
	"reflect"
	"testing"
)

func TestFilter_DefaultVersionDescAllValues(t *testing.T) {
	in := []string{"1.0.0", "2.0.0", "latest", "1.5.0"}
	got := Filter(in)
	want := []string{"2.0.0", "1.5.0", "1.0.0", "latest"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Filter(%v) = %v, want %v", in, got, want)
	}
}

func TestFilter_DoesNotMutateInput(t *testing.T) {
	in := []string{"2.0.0", "1.0.0"}
	original := append([]string(nil), in...)
	_ = Filter(in)
	if !reflect.DeepEqual(in, original) {
		t.Errorf("Filter mutated input: got %v, want unchanged %v", in, original)
	}
}

func TestFilter_WithLimit(t *testing.T) {
	in := []string{"1.0.0", "3.0.0", "2.0.0"}
	got := Filter(in, WithLimit(2))
	want := []string{"3.0.0", "2.0.0"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Filter with limit 2 = %v, want %v", got, want)
	}
}

func TestFilter_LimitZeroOrNegativeMeansAll(t *testing.T) {
	in := []string{"1.0.0", "2.0.0"}
	for _, n := range []int{0, -1, -100} {
		got := Filter(in, WithLimit(n))
		if len(got) != 2 {
			t.Errorf("Filter with limit %d = %v, want all 2 values", n, got)
		}
	}
}

func TestFilter_LimitLargerThanLength(t *testing.T) {
	in := []string{"1.0.0", "2.0.0"}
	got := Filter(in, WithLimit(100))
	if len(got) != 2 {
		t.Errorf("Filter with limit 100 = %v, want all 2 values", got)
	}
}

func TestFilter_SortAlphabetical(t *testing.T) {
	in := []string{"2.0.0", "1.0.0", "latest"}
	got := Filter(in, WithSort(SortAlphabetical))
	want := []string{"1.0.0", "2.0.0", "latest"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Filter(SortAlphabetical) = %v, want %v", got, want)
	}
}

func TestFilter_SortNonePassesThrough(t *testing.T) {
	in := []string{"latest", "2.0.0", "1.0.0"}
	got := Filter(in, WithSort(SortNone))
	if !reflect.DeepEqual(got, in) {
		t.Errorf("Filter(SortNone) = %v, want unchanged order %v", got, in)
	}
}

func TestFilter_MixedBucketOrdering(t *testing.T) {
	in := []string{"latest", "3.1-debian", "1.0.0", "stable", "18.04", "2.0.0"}
	got := Filter(in)
	want := []string{"2.0.0", "1.0.0", "18.04", "3.1-debian", "latest", "stable"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Filter(mixed) = %v, want %v", got, want)
	}
}

func TestFilter_SortAndLimitCombined(t *testing.T) {
	in := []string{"latest", "3.1-debian", "1.0.0", "2.0.0"}
	got := Filter(in, WithLimit(2))
	want := []string{"2.0.0", "1.0.0"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Filter(sort+limit) = %v, want %v", got, want)
	}
}

func TestFilter_EmptyInput(t *testing.T) {
	got := Filter[string](nil)
	if len(got) != 0 {
		t.Errorf("Filter(nil) = %v, want empty", got)
	}
}

func TestFilter_GenericOverNamedStringType(t *testing.T) {
	type fakeTag string
	in := []fakeTag{"1.0.0", "3.0.0", "2.0.0"}
	got := Filter(in, WithLimit(2))
	want := []fakeTag{"3.0.0", "2.0.0"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Filter[fakeTag](...) = %v, want %v (generic ~string must work end-to-end on non-Docker types too)", got, want)
	}
}
