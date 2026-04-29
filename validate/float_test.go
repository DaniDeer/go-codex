package validate_test

import (
	"testing"

	"github.com/DaniDeer/go-codex/validate"
)

func TestPositiveFloat(t *testing.T) {
	c := validate.PositiveFloat
	cases := []struct {
		v    float64
		pass bool
	}{
		{0.1, true}, {1.0, true}, {1e10, true},
		{0.0, false}, {-0.1, false},
	}
	for _, tc := range cases {
		if got := c.Check(tc.v); got != tc.pass {
			t.Errorf("PositiveFloat.Check(%g) = %v, want %v", tc.v, got, tc.pass)
		}
	}
	if msg := c.Message(0); msg == "" {
		t.Error("PositiveFloat.Message should not be empty")
	}
}

func TestNegativeFloat(t *testing.T) {
	c := validate.NegativeFloat
	cases := []struct {
		v    float64
		pass bool
	}{
		{-0.1, true}, {-1.0, true},
		{0.0, false}, {0.1, false},
	}
	for _, tc := range cases {
		if got := c.Check(tc.v); got != tc.pass {
			t.Errorf("NegativeFloat.Check(%g) = %v, want %v", tc.v, got, tc.pass)
		}
	}
}

func TestNonZeroFloat(t *testing.T) {
	c := validate.NonZeroFloat
	cases := []struct {
		v    float64
		pass bool
	}{
		{1.0, true}, {-1.0, true}, {0.001, true},
		{0.0, false},
	}
	for _, tc := range cases {
		if got := c.Check(tc.v); got != tc.pass {
			t.Errorf("NonZeroFloat.Check(%g) = %v, want %v", tc.v, got, tc.pass)
		}
	}
	if msg := c.Message(0); msg == "" {
		t.Error("NonZeroFloat.Message should not be empty")
	}
}

func TestMinFloat(t *testing.T) {
	c := validate.MinFloat(2.5)
	cases := []struct {
		v    float64
		pass bool
	}{
		{2.5, true}, {10.0, true},
		{2.4, false}, {0.0, false},
	}
	for _, tc := range cases {
		if got := c.Check(tc.v); got != tc.pass {
			t.Errorf("MinFloat(2.5).Check(%g) = %v, want %v", tc.v, got, tc.pass)
		}
	}
	if msg := c.Message(1.0); msg == "" {
		t.Error("MinFloat.Message should not be empty")
	}
}

func TestMaxFloat(t *testing.T) {
	c := validate.MaxFloat(5.0)
	cases := []struct {
		v    float64
		pass bool
	}{
		{5.0, true}, {0.0, true}, {-1.0, true},
		{5.1, false}, {100.0, false},
	}
	for _, tc := range cases {
		if got := c.Check(tc.v); got != tc.pass {
			t.Errorf("MaxFloat(5.0).Check(%g) = %v, want %v", tc.v, got, tc.pass)
		}
	}
}

func TestRangeFloat(t *testing.T) {
	c := validate.RangeFloat(0.0, 1.0)
	cases := []struct {
		v    float64
		pass bool
	}{
		{0.0, true}, {0.5, true}, {1.0, true},
		{-0.1, false}, {1.1, false},
	}
	for _, tc := range cases {
		if got := c.Check(tc.v); got != tc.pass {
			t.Errorf("RangeFloat(0,1).Check(%g) = %v, want %v", tc.v, got, tc.pass)
		}
	}
	if msg := c.Message(-1.0); msg == "" {
		t.Error("RangeFloat.Message should not be empty")
	}
}
