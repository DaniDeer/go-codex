package docker

import "testing"

func TestEnvCodec_RoundTrip(t *testing.T) {
	raw := []string{"PATH=/usr/bin", "DEBUG=", "KEY=a=b"}
	rawWire := make([]any, len(raw))
	for i, s := range raw {
		rawWire[i] = s
	}
	want := Env{
		{Name: "PATH", Value: "/usr/bin"},
		{Name: "DEBUG", Value: ""},
		{Name: "KEY", Value: "a=b"},
	}

	got, err := EnvCodec.Decode(rawWire)
	if err != nil {
		t.Fatalf("Decode(%v): unexpected error: %v", raw, err)
	}
	if len(got) != len(want) {
		t.Fatalf("Decode(%v) = %+v, want %+v", raw, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Decode(%v)[%d] = %+v, want %+v", raw, i, got[i], want[i])
		}
	}

	encoded, err := EnvCodec.Encode(got)
	if err != nil {
		t.Fatalf("Encode(%+v): unexpected error: %v", got, err)
	}
	encSlice, ok := encoded.([]any)
	if !ok {
		t.Fatalf("Encode(%+v) returned %T, want []any", got, encoded)
	}
	for i := range raw {
		if encSlice[i] != raw[i] {
			t.Errorf("Encode roundtrip[%d] = %q, want %q", i, encSlice[i], raw[i])
		}
	}
}

func TestEnvCodec_BareNameNoValue(t *testing.T) {
	got, err := EnvCodec.Decode([]any{"NO_EQUALS_SIGN"})
	if err != nil {
		t.Fatalf("Decode: unexpected error: %v", err)
	}
	want := Env{{Name: "NO_EQUALS_SIGN", Value: ""}}
	if len(got) != 1 || got[0] != want[0] {
		t.Errorf("Decode(bare name) = %+v, want %+v", got, want)
	}
}
