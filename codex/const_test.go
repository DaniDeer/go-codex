package codex_test

import (
	"fmt"
	"log/slog"
	"testing"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/validate"
)

// ── Getter/Setter interface satisfaction ────────────────────────────────────

var _ codex.Getter[string] = codex.Const[string]{}
var _ codex.Getter[string] = (*codex.Immutable[string])(nil)
var _ codex.Setter[string] = (*codex.Immutable[string])(nil)
var _ codex.GetterSetter[string] = (*codex.Immutable[string])(nil)

func TestConst_ImplementsGetterInterface(t *testing.T) {
	var _ codex.Getter[string] = codex.Const[string]{}
}

func TestImmutable_ImplementsGetterSetterInterface(t *testing.T) {
	var _ codex.GetterSetter[string] = (*codex.Immutable[string])(nil)
}

// ── Const ────────────────────────────────────────────────────────────────

func TestMustConst_ValidValue_ReturnsConst(t *testing.T) {
	codec := codex.String().Refine(validate.NonEmptyString)
	got := codex.MustConst("hello", codec)
	if got.Get() != "hello" {
		t.Errorf("Get() = %q, want %q", got.Get(), "hello")
	}
}

func TestMustConst_InvalidValue_Panics(t *testing.T) {
	codec := codex.String().Refine(validate.NonEmptyString)
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("MustConst did not panic")
		}
		msg := fmt.Sprint(r)
		if msg == "" {
			t.Error("panic message is empty")
		}
	}()
	codex.MustConst("", codec)
}

func TestConst_Get_ReturnsUnderlyingValue(t *testing.T) {
	codec := codex.String()
	c := codex.MustConst("path/pattern", codec)
	if c.Get() != "path/pattern" {
		t.Errorf("Get() = %q, want %q", c.Get(), "path/pattern")
	}
}

func TestConst_String_UsesFmtSprint(t *testing.T) {
	strConst := codex.MustConst("hello", codex.String())
	if strConst.String() != "hello" {
		t.Errorf("String() = %q, want %q", strConst.String(), "hello")
	}

	intConst := codex.MustConst(42, codex.Int())
	if intConst.String() != "42" {
		t.Errorf("String() = %q, want %q", intConst.String(), "42")
	}
}

func TestNewConst_ValidValue_ReturnsConstNoError(t *testing.T) {
	codec := codex.String().Refine(validate.NonEmptyString)
	got, err := codex.NewConst("hello", codec)
	if err != nil {
		t.Fatalf("NewConst: unexpected error: %v", err)
	}
	if got.Get() != "hello" {
		t.Errorf("Get() = %q, want %q", got.Get(), "hello")
	}
}

func TestNewConst_InvalidValue_ReturnsError(t *testing.T) {
	codec := codex.String().Refine(validate.NonEmptyString)
	_, err := codex.NewConst("", codec)
	if err == nil {
		t.Fatal("NewConst(\"\"): want error, got nil")
	}
}

func TestMustConst_Unchanged_StillPanicsOnInvalid(t *testing.T) {
	codec := codex.String().Refine(validate.NonEmptyString)
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("MustConst did not panic")
		}
	}()
	codex.MustConst("", codec)
}

func ExampleNewConst() {
	pathPatternCodec := codex.String().Refine(validate.NonEmptyString)
	pattern, err := codex.NewConst("usecases/{usecase_name}.json", pathPatternCodec)
	if err != nil {
		panic(err)
	}
	fmt.Println(pattern.String())
	// Output: usecases/{usecase_name}.json
}

// ── Immutable ────────────────────────────────────────────────────────────

func TestImmutable_SetValid_GetReturnsIt(t *testing.T) {
	im := codex.NewImmutable(codex.String().Refine(validate.NonEmptyString))
	if err := im.Set("https://api.example.com"); err != nil {
		t.Fatalf("Set: unexpected error: %v", err)
	}
	if got := im.Get(); got != "https://api.example.com" {
		t.Errorf("Get() = %q, want %q", got, "https://api.example.com")
	}
}

func TestImmutable_SetInvalid_ReturnsCodecError(t *testing.T) {
	im := codex.NewImmutable(codex.String().Refine(validate.NonEmptyString))
	if err := im.Set(""); err == nil {
		t.Error("Set(\"\"): want error, got nil")
	}
	if _, ok := im.TryGet(); ok {
		t.Error("TryGet() after failed Set: want (_, false), got (_, true)")
	}
}

func TestImmutable_SetTwice_ReturnsAlreadySetError(t *testing.T) {
	im := codex.NewImmutable(codex.String())
	if err := im.Set("first"); err != nil {
		t.Fatalf("first Set: unexpected error: %v", err)
	}
	err := im.Set("second")
	if err == nil {
		t.Fatal("second Set: want error, got nil")
	}
	if _, ok := err.(codex.ImmutableAlreadySetError); !ok {
		t.Errorf("second Set error type = %T, want codex.ImmutableAlreadySetError", err)
	}
	if got := im.Get(); got != "first" {
		t.Errorf("Get() after rejected second Set = %q, want unchanged %q", got, "first")
	}
}

func TestImmutable_GetBeforeSet_Panics(t *testing.T) {
	im := codex.NewImmutable(codex.String())
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("Get() before Set did not panic")
		}
	}()
	im.Get()
}

func TestImmutable_TryGetBeforeSet_ReturnsFalse(t *testing.T) {
	im := codex.NewImmutable(codex.String())
	val, ok := im.TryGet()
	if ok {
		t.Error("TryGet() before Set: want ok=false, got true")
	}
	if val != "" {
		t.Errorf("TryGet() before Set: want zero value, got %q", val)
	}
}

func TestImmutable_TryGetAfterSet_ReturnsTrue(t *testing.T) {
	im := codex.NewImmutable(codex.String())
	if err := im.Set("configured"); err != nil {
		t.Fatalf("Set: unexpected error: %v", err)
	}
	val, ok := im.TryGet()
	if !ok {
		t.Error("TryGet() after Set: want ok=true, got false")
	}
	if val != "configured" {
		t.Errorf("TryGet() after Set = %q, want %q", val, "configured")
	}
}

// ── ImmutableAlreadySetError ────────────────────────────────────────────────

func TestImmutableAlreadySetError_LogValue(t *testing.T) {
	err := codex.ImmutableAlreadySetError{}
	if err.Error() == "" {
		t.Error("Error() is empty")
	}
	lv := err.LogValue()
	if lv.Kind() != slog.KindGroup {
		t.Fatalf("LogValue: want KindGroup, got %v", lv.Kind())
	}
}

// ── Examples ─────────────────────────────────────────────────────────────

func ExampleMustConst() {
	pathPatternCodec := codex.String().Refine(validate.NonEmptyString)
	pattern := codex.MustConst("usecases/{usecase_name}.json", pathPatternCodec)
	fmt.Println(pattern.String())
	// Output: usecases/{usecase_name}.json
}

func ExampleImmutable() {
	apiBaseURL := codex.NewImmutable(codex.String().Refine(validate.NonEmptyString))
	if err := apiBaseURL.Set("https://api.example.com"); err != nil {
		panic(err)
	}
	fmt.Println(apiBaseURL.Get())
	// Output: https://api.example.com
}
