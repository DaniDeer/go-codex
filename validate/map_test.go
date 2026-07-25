package validate_test

import (
	"testing"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/validate"
)

func TestMinProperties(t *testing.T) {
	c := validate.MinProperties[string, int](2)
	cases := []struct {
		v    map[string]int
		pass bool
	}{
		{map[string]int{"a": 1, "b": 2}, true},
		{map[string]int{"a": 1, "b": 2, "c": 3}, true},
		{map[string]int{"a": 1}, false},
		{nil, false},
	}
	for _, tc := range cases {
		if got := c.Check(tc.v); got != tc.pass {
			t.Errorf("MinProperties(2).Check(%v) = %v, want %v", tc.v, got, tc.pass)
		}
	}
	if msg := c.Message(map[string]int{"a": 1}); msg == "" {
		t.Error("MinProperties.Message should not be empty")
	}
}

func TestMinProperties_SchemaAnnotation(t *testing.T) {
	tagsCodec := codex.StringMap(codex.Int()).Refine(validate.MinProperties[string, int](2))
	s := tagsCodec.Schema
	if s.MinProperties == nil || *s.MinProperties != 2 {
		t.Errorf("Schema.MinProperties = %v, want 2", s.MinProperties)
	}
}

func TestMaxProperties(t *testing.T) {
	c := validate.MaxProperties[string, int](3)
	cases := []struct {
		v    map[string]int
		pass bool
	}{
		{map[string]int{"a": 1, "b": 2, "c": 3}, true},
		{nil, true},
		{map[string]int{}, true},
		{map[string]int{"a": 1, "b": 2, "c": 3, "d": 4}, false},
	}
	for _, tc := range cases {
		if got := c.Check(tc.v); got != tc.pass {
			t.Errorf("MaxProperties(3).Check(%v) = %v, want %v", tc.v, got, tc.pass)
		}
	}
	if msg := c.Message(map[string]int{"a": 1, "b": 2, "c": 3, "d": 4}); msg == "" {
		t.Error("MaxProperties.Message should not be empty")
	}
}

func TestMaxProperties_SchemaAnnotation(t *testing.T) {
	tagsCodec := codex.StringMap(codex.Int()).Refine(validate.MaxProperties[string, int](3))
	s := tagsCodec.Schema
	if s.MaxProperties == nil || *s.MaxProperties != 3 {
		t.Errorf("Schema.MaxProperties = %v, want 3", s.MaxProperties)
	}
}

func TestNonEmptyMap(t *testing.T) {
	c := validate.NonEmptyMap[string, int]()
	if !c.Check(map[string]int{"a": 1}) {
		t.Error("Check({a:1}) should pass")
	}
	if c.Check(nil) {
		t.Error("Check(nil) should fail")
	}
	if c.Check(map[string]int{}) {
		t.Error("Check({}) should fail")
	}
	if msg := c.Message(nil); msg == "" {
		t.Error("NonEmptyMap.Message should not be empty")
	}
}

func TestNonEmptyMap_SchemaAnnotation(t *testing.T) {
	tagsCodec := codex.StringMap(codex.Int()).Refine(validate.NonEmptyMap[string, int]())
	s := tagsCodec.Schema
	if s.MinProperties == nil || *s.MinProperties != 1 {
		t.Errorf("Schema.MinProperties = %v, want 1", s.MinProperties)
	}
}

func TestNonEmptyMap_ComposesWithDecode(t *testing.T) {
	tagsCodec := codex.StringMap(codex.Int()).Refine(validate.NonEmptyMap[string, int]())
	if _, err := tagsCodec.Decode(map[string]any{}); err == nil {
		t.Error("Decode({}) should fail non-empty constraint")
	}
	got, err := tagsCodec.Decode(map[string]any{"a": 1.0, "b": 2.0})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("got %v, want 2 entries", got)
	}
}
