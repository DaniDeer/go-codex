package validate_test

import (
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/validate"
)

func TestPositiveDuration(t *testing.T) {
	c := validate.PositiveDuration
	cases := []struct {
		v    time.Duration
		pass bool
	}{
		{time.Second, true}, {time.Nanosecond, true},
		{0, false}, {-time.Second, false},
	}
	for _, tc := range cases {
		if got := c.Check(tc.v); got != tc.pass {
			t.Errorf("PositiveDuration.Check(%s) = %v, want %v", tc.v, got, tc.pass)
		}
	}
	if msg := c.Message(0); msg == "" {
		t.Error("PositiveDuration.Message should not be empty")
	}
}

func TestNonNegativeDuration(t *testing.T) {
	c := validate.NonNegativeDuration
	cases := []struct {
		v    time.Duration
		pass bool
	}{
		{0, true}, {time.Second, true},
		{-time.Nanosecond, false},
	}
	for _, tc := range cases {
		if got := c.Check(tc.v); got != tc.pass {
			t.Errorf("NonNegativeDuration.Check(%s) = %v, want %v", tc.v, got, tc.pass)
		}
	}
}

func TestMinDuration(t *testing.T) {
	c := validate.MinDuration(5 * time.Second)
	cases := []struct {
		v    time.Duration
		pass bool
	}{
		{5 * time.Second, true}, {10 * time.Second, true},
		{4 * time.Second, false}, {0, false},
	}
	for _, tc := range cases {
		if got := c.Check(tc.v); got != tc.pass {
			t.Errorf("MinDuration(5s).Check(%s) = %v, want %v", tc.v, got, tc.pass)
		}
	}
	if msg := c.Message(0); msg == "" {
		t.Error("MinDuration.Message should not be empty")
	}
}

func TestMaxDuration(t *testing.T) {
	c := validate.MaxDuration(30 * time.Second)
	cases := []struct {
		v    time.Duration
		pass bool
	}{
		{0, true}, {30 * time.Second, true},
		{31 * time.Second, false},
	}
	for _, tc := range cases {
		if got := c.Check(tc.v); got != tc.pass {
			t.Errorf("MaxDuration(30s).Check(%s) = %v, want %v", tc.v, got, tc.pass)
		}
	}
}
