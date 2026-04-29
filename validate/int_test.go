package validate_test

import (
	"testing"

	"github.com/DaniDeer/go-codex/validate"
)

func TestPositiveInt(t *testing.T) {
	c := validate.PositiveInt
	cases := []struct {
		v    int
		pass bool
	}{
		{1, true}, {100, true},
		{0, false}, {-1, false},
	}
	for _, tc := range cases {
		if got := c.Check(tc.v); got != tc.pass {
			t.Errorf("PositiveInt.Check(%d) = %v, want %v", tc.v, got, tc.pass)
		}
	}
	if msg := c.Message(0); msg == "" {
		t.Error("PositiveInt.Message should not be empty")
	}
}

func TestNegativeInt(t *testing.T) {
	c := validate.NegativeInt
	cases := []struct {
		v    int
		pass bool
	}{
		{-1, true}, {-100, true},
		{0, false}, {1, false},
	}
	for _, tc := range cases {
		if got := c.Check(tc.v); got != tc.pass {
			t.Errorf("NegativeInt.Check(%d) = %v, want %v", tc.v, got, tc.pass)
		}
	}
}

func TestMinInt(t *testing.T) {
	c := validate.MinInt(5)
	cases := []struct {
		v    int
		pass bool
	}{
		{5, true}, {10, true},
		{4, false}, {0, false},
	}
	for _, tc := range cases {
		if got := c.Check(tc.v); got != tc.pass {
			t.Errorf("MinInt(5).Check(%d) = %v, want %v", tc.v, got, tc.pass)
		}
	}
	if msg := c.Message(3); msg == "" {
		t.Error("MinInt.Message should not be empty")
	}
}

func TestMaxInt(t *testing.T) {
	c := validate.MaxInt(10)
	cases := []struct {
		v    int
		pass bool
	}{
		{10, true}, {0, true},
		{11, false}, {100, false},
	}
	for _, tc := range cases {
		if got := c.Check(tc.v); got != tc.pass {
			t.Errorf("MaxInt(10).Check(%d) = %v, want %v", tc.v, got, tc.pass)
		}
	}
}

func TestRangeInt(t *testing.T) {
	c := validate.RangeInt(1, 10)
	cases := []struct {
		v    int
		pass bool
	}{
		{1, true}, {5, true}, {10, true},
		{0, false}, {11, false},
	}
	for _, tc := range cases {
		if got := c.Check(tc.v); got != tc.pass {
			t.Errorf("RangeInt(1,10).Check(%d) = %v, want %v", tc.v, got, tc.pass)
		}
	}
	if msg := c.Message(0); msg == "" {
		t.Error("RangeInt.Message should not be empty")
	}
}
