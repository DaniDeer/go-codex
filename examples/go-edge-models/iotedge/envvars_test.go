package iotedge

import (
	"testing"

	"github.com/DaniDeer/go-codex/examples/go-edge-models/docker"
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
