package versioning

import (
	"log/slog"
	"testing"
)

func TestInvalidVersionError_Error(t *testing.T) {
	err := InvalidVersionError{Version: Version{}}
	if err.Error() == "" {
		t.Error("InvalidVersionError.Error() should not be empty")
	}
}

func TestInvalidVersionError_LogValue(t *testing.T) {
	err := InvalidVersionError{Version: Version{}}
	lv := err.LogValue()
	if lv.Kind() != slog.KindGroup {
		t.Fatalf("LogValue: want KindGroup, got %v", lv.Kind())
	}
	attrs := lv.Group()
	keys := make(map[string]bool, len(attrs))
	for _, a := range attrs {
		keys[a.Key] = true
	}
	for _, want := range []string{"has_semver", "has_semver_like", "has_other"} {
		if !keys[want] {
			t.Errorf("LogValue missing attribute %q", want)
		}
	}
}

func TestInvalidVersionError_LogValue_ReflectsSetFields(t *testing.T) {
	other := "latest"
	err := InvalidVersionError{Version: Version{Other: &other}}
	lv := err.LogValue()
	for _, a := range lv.Group() {
		switch a.Key {
		case "has_other":
			if !a.Value.Bool() {
				t.Error("has_other = false, want true (Other is set)")
			}
		case "has_semver", "has_semver_like":
			if a.Value.Bool() {
				t.Errorf("%s = true, want false", a.Key)
			}
		}
	}
}
