package codex_test

import (
	"errors"
	"testing"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/validate"
)

func TestMust_ReturnsValueWhenNoError(t *testing.T) {
	got := codex.Must(42, nil)
	if got != 42 {
		t.Errorf("Must returned %d, want 42", got)
	}
}

func TestMust_PanicsOnError(t *testing.T) {
	sentinel := errors.New("validation failed")
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("Must did not panic")
		}
		if r != sentinel {
			t.Errorf("panicked with %v, want sentinel error", r)
		}
	}()
	codex.Must(0, sentinel)
}

func TestMust_WorksWithCodecNew(t *testing.T) {
	c := codex.Int().Refine(validate.PositiveInt)
	got := codex.Must(c.New(10))
	if got != 10 {
		t.Errorf("Must(New) returned %d, want 10", got)
	}
}

func TestMust_WorksWithCodecDecode(t *testing.T) {
	c := codex.String()
	got := codex.Must(c.Decode("hello"))
	if got != "hello" {
		t.Errorf("Must(Decode) returned %q, want %q", got, "hello")
	}
}

func TestMust_PanicsWithCodecNewOnInvalidValue(t *testing.T) {
	c := codex.Int().Refine(validate.PositiveInt)
	defer func() {
		if recover() == nil {
			t.Fatal("Must did not panic on invalid value")
		}
	}()
	codex.Must(c.New(-1))
}
