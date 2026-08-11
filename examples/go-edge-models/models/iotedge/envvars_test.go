package iotedge

import (
	"testing"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker"
)

func strPtr(s string) *string     { return &s }
func intPtr(i int64) *int64       { return &i }
func floatPtr(f float64) *float64 { return &f }

func TestFlattenEnvVars_MixedTypesSortedByName(t *testing.T) {
	vars := EnvVars{
		"PORT":    EnvVar{Value: EnvVarValue{IntValue: intPtr(8080)}},
		"DEBUG":   EnvVar{Value: EnvVarValue{StringValue: strPtr("true")}},
		"RATIO":   EnvVar{Value: EnvVarValue{FloatValue: floatPtr(0.5)}},
		"API_KEY": EnvVar{Value: EnvVarValue{StringValue: strPtr("secret")}},
	}

	want := docker.Env{
		{Name: "API_KEY", Value: "secret"},
		{Name: "DEBUG", Value: "true"},
		{Name: "PORT", Value: "8080"},
		{Name: "RATIO", Value: "0.5"},
	}

	got := FlattenEnvVars(vars)
	if len(got) != len(want) {
		t.Fatalf("FlattenEnvVars() = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("FlattenEnvVars()[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestFlattenEnvVars_Empty(t *testing.T) {
	got := FlattenEnvVars(EnvVars{})
	if len(got) != 0 {
		t.Errorf("FlattenEnvVars(empty) = %+v, want empty", got)
	}
}

func TestNewEnvVarValueString(t *testing.T) {
	v := NewEnvVarValueString("prod")
	if v.StringValue == nil || *v.StringValue != "prod" {
		t.Fatalf("NewEnvVarValueString(%q) = %+v, want StringValue=%q", "prod", v, "prod")
	}
	if v.IntValue != nil || v.FloatValue != nil {
		t.Errorf("NewEnvVarValueString: other branches set: %+v", v)
	}
}

func TestNewEnvVarValueInt(t *testing.T) {
	v := NewEnvVarValueInt(42)
	if v.IntValue == nil || *v.IntValue != 42 {
		t.Fatalf("NewEnvVarValueInt(42) = %+v, want IntValue=42", v)
	}
	if v.StringValue != nil || v.FloatValue != nil {
		t.Errorf("NewEnvVarValueInt: other branches set: %+v", v)
	}
}

func TestNewEnvVarValueFloat(t *testing.T) {
	v := NewEnvVarValueFloat(3.14)
	if v.FloatValue == nil || *v.FloatValue != 3.14 {
		t.Fatalf("NewEnvVarValueFloat(3.14) = %+v, want FloatValue=3.14", v)
	}
	if v.StringValue != nil || v.IntValue != nil {
		t.Errorf("NewEnvVarValueFloat: other branches set: %+v", v)
	}
}

func TestEnvVarValue_ImplementsHasCodec(t *testing.T) {
	cases := []EnvVarValue{
		NewEnvVarValueString("prod"),
		NewEnvVarValueInt(42),
		NewEnvVarValueFloat(3.14),
	}
	for _, ev := range cases {
		if err := codex.Validate(ev); err != nil {
			t.Errorf("codex.Validate(%+v) = %v, want nil", ev, err)
		}
		raw, err := codex.EncodeSelf(ev)
		if err != nil {
			t.Fatalf("codex.EncodeSelf(%+v): %v", ev, err)
		}
		back, err := codex.DecodeAs[EnvVarValue](raw)
		if err != nil {
			t.Fatalf("codex.DecodeAs: %v", err)
		}
		if formatEnvVarValue(back) != formatEnvVarValue(ev) {
			t.Errorf("DecodeAs(EncodeSelf(%+v)) = %+v, want equivalent value", ev, back)
		}
	}
}
