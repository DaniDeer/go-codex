package validate_test

import (
	"testing"

	"github.com/DaniDeer/go-codex/validate"
)

func TestPositiveUint(t *testing.T) {
	c := validate.PositiveUint
	cases := []struct {
		v    uint
		pass bool
	}{
		{1, true}, {100, true},
		{0, false},
	}
	for _, tc := range cases {
		if got := c.Check(tc.v); got != tc.pass {
			t.Errorf("PositiveUint.Check(%d) = %v, want %v", tc.v, got, tc.pass)
		}
	}
	if msg := c.Message(0); msg == "" {
		t.Error("PositiveUint.Message should not be empty")
	}
}

func TestMinUint(t *testing.T) {
	c := validate.MinUint(5)
	cases := []struct {
		v    uint
		pass bool
	}{
		{5, true}, {100, true},
		{4, false}, {0, false},
	}
	for _, tc := range cases {
		if got := c.Check(tc.v); got != tc.pass {
			t.Errorf("MinUint(5).Check(%d) = %v, want %v", tc.v, got, tc.pass)
		}
	}
}

func TestMaxUint(t *testing.T) {
	c := validate.MaxUint(10)
	cases := []struct {
		v    uint
		pass bool
	}{
		{0, true}, {10, true},
		{11, false},
	}
	for _, tc := range cases {
		if got := c.Check(tc.v); got != tc.pass {
			t.Errorf("MaxUint(10).Check(%d) = %v, want %v", tc.v, got, tc.pass)
		}
	}
}

func TestRangeUint(t *testing.T) {
	c := validate.RangeUint(1, 65535)
	cases := []struct {
		v    uint
		pass bool
	}{
		{1, true}, {8080, true}, {65535, true},
		{0, false}, {65536, false},
	}
	for _, tc := range cases {
		if got := c.Check(tc.v); got != tc.pass {
			t.Errorf("RangeUint(1,65535).Check(%d) = %v, want %v", tc.v, got, tc.pass)
		}
	}
	if msg := c.Message(0); msg == "" {
		t.Error("RangeUint.Message should not be empty")
	}
}

func TestPositiveUint64(t *testing.T) {
	c := validate.PositiveUint64
	cases := []struct {
		v    uint64
		pass bool
	}{
		{1, true}, {1 << 40, true},
		{0, false},
	}
	for _, tc := range cases {
		if got := c.Check(tc.v); got != tc.pass {
			t.Errorf("PositiveUint64.Check(%d) = %v, want %v", tc.v, got, tc.pass)
		}
	}
}

func TestRangeUint64(t *testing.T) {
	c := validate.RangeUint64(100, 1000)
	cases := []struct {
		v    uint64
		pass bool
	}{
		{100, true}, {500, true}, {1000, true},
		{99, false}, {1001, false},
	}
	for _, tc := range cases {
		if got := c.Check(tc.v); got != tc.pass {
			t.Errorf("RangeUint64(100,1000).Check(%d) = %v, want %v", tc.v, got, tc.pass)
		}
	}
	if msg := c.Message(0); msg == "" {
		t.Error("RangeUint64.Message should not be empty")
	}
}
