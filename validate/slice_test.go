package validate_test

import (
	"testing"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/validate"
)

func TestMinItems(t *testing.T) {
	c := validate.MinItems[int](2)
	cases := []struct {
		v    []int
		pass bool
	}{
		{[]int{1, 2}, true}, {[]int{1, 2, 3}, true},
		{[]int{1}, false}, {nil, false},
	}
	for _, tc := range cases {
		if got := c.Check(tc.v); got != tc.pass {
			t.Errorf("MinItems(2).Check(%v) = %v, want %v", tc.v, got, tc.pass)
		}
	}
	if msg := c.Message([]int{1}); msg == "" {
		t.Error("MinItems.Message should not be empty")
	}
}

func TestMinItems_SchemaAnnotation(t *testing.T) {
	itemsCodec := codex.SliceOf(codex.Int()).Refine(validate.MinItems[int](2))
	s := itemsCodec.Schema
	if s.MinItems == nil || *s.MinItems != 2 {
		t.Errorf("Schema.MinItems = %v, want 2", s.MinItems)
	}
}

func TestMaxItems(t *testing.T) {
	c := validate.MaxItems[int](3)
	cases := []struct {
		v    []int
		pass bool
	}{
		{[]int{1, 2, 3}, true}, {nil, true}, {[]int{}, true},
		{[]int{1, 2, 3, 4}, false},
	}
	for _, tc := range cases {
		if got := c.Check(tc.v); got != tc.pass {
			t.Errorf("MaxItems(3).Check(%v) = %v, want %v", tc.v, got, tc.pass)
		}
	}
	if msg := c.Message([]int{1, 2, 3, 4}); msg == "" {
		t.Error("MaxItems.Message should not be empty")
	}
}

func TestMaxItems_SchemaAnnotation(t *testing.T) {
	itemsCodec := codex.SliceOf(codex.Int()).Refine(validate.MaxItems[int](3))
	s := itemsCodec.Schema
	if s.MaxItems == nil || *s.MaxItems != 3 {
		t.Errorf("Schema.MaxItems = %v, want 3", s.MaxItems)
	}
}

func TestNonEmptySlice(t *testing.T) {
	c := validate.NonEmptySlice[string]()
	if !c.Check([]string{"a"}) {
		t.Error("Check([]string{\"a\"}) should pass")
	}
	if c.Check(nil) {
		t.Error("Check(nil) should fail")
	}
	if c.Check([]string{}) {
		t.Error("Check([]string{}) should fail")
	}
	if msg := c.Message(nil); msg == "" {
		t.Error("NonEmptySlice.Message should not be empty")
	}
}

func TestNonEmptySlice_SchemaAnnotation(t *testing.T) {
	itemsCodec := codex.SliceOf(codex.String()).Refine(validate.NonEmptySlice[string]())
	s := itemsCodec.Schema
	if s.MinItems == nil || *s.MinItems != 1 {
		t.Errorf("Schema.MinItems = %v, want 1", s.MinItems)
	}
}

func TestNonEmptySlice_ComposesWithDecode(t *testing.T) {
	itemsCodec := codex.SliceOf(codex.String()).Refine(validate.NonEmptySlice[string]())
	if _, err := itemsCodec.Decode([]any{}); err == nil {
		t.Error("Decode([]) should fail non-empty constraint")
	}
	got, err := itemsCodec.Decode([]any{"a", "b"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("got %v, want 2 elements", got)
	}
}

func TestUniqueItems(t *testing.T) {
	c := validate.UniqueItems[int]()
	if !c.Check([]int{1, 2, 3}) {
		t.Error("Check([1,2,3]) should pass (all unique)")
	}
	if !c.Check(nil) {
		t.Error("Check(nil) should pass (vacuously unique)")
	}
	if c.Check([]int{1, 2, 2}) {
		t.Error("Check([1,2,2]) should fail (duplicate)")
	}
	if msg := c.Message([]int{1, 2, 2}); msg == "" {
		t.Error("UniqueItems.Message should not be empty")
	}
}

func TestUniqueItems_SchemaAnnotation(t *testing.T) {
	itemsCodec := codex.SliceOf(codex.Int()).Refine(validate.UniqueItems[int]())
	s := itemsCodec.Schema
	if !s.UniqueItems {
		t.Error("Schema.UniqueItems should be true")
	}
}

func TestUniqueItems_ComposesWithDecode(t *testing.T) {
	itemsCodec := codex.SliceOf(codex.Int()).Refine(validate.UniqueItems[int]())
	if _, err := itemsCodec.Decode([]any{1.0, 1.0}); err == nil {
		t.Error("Decode([1,1]) should fail uniqueItems constraint")
	}
	got, err := itemsCodec.Decode([]any{1.0, 2.0})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("got %v, want 2 elements", got)
	}
}
