package docker

import "testing"

func TestParseMemBytes_BareDigits(t *testing.T) {
	got, err := ParseMemBytes("536870912")
	if err != nil {
		t.Fatalf("ParseMemBytes: unexpected error: %v", err)
	}
	if got != 536870912 {
		t.Errorf("ParseMemBytes(%q) = %d, want 536870912", "536870912", got)
	}
}

func TestParseMemBytes_Suffixes(t *testing.T) {
	cases := []struct {
		in   string
		want int64
	}{
		{"128b", 128},
		{"128k", 128 * 1024},
		{"512m", 512 * 1024 * 1024},
		{"1g", 1024 * 1024 * 1024},
		{"1G", 1024 * 1024 * 1024}, // case-insensitive suffix
	}
	for _, tc := range cases {
		got, err := ParseMemBytes(tc.in)
		if err != nil {
			t.Errorf("ParseMemBytes(%q): unexpected error: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ParseMemBytes(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestParseMemBytes_RejectsInvalid(t *testing.T) {
	cases := []string{"", "abc", "-5m", "m", "5x", "5.5m"}
	for _, in := range cases {
		if _, err := ParseMemBytes(in); err == nil {
			t.Errorf("ParseMemBytes(%q): want error, got nil", in)
		}
	}
}

func TestFormatMemBytes(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{1024 * 1024 * 1024, "1g"},
		{512 * 1024 * 1024, "512m"},
		{128 * 1024, "128k"},
		{1536, "1536b"}, // not a whole number of KiB
		{0, "0b"},
	}
	for _, tc := range cases {
		got := FormatMemBytes(tc.in)
		if got != tc.want {
			t.Errorf("FormatMemBytes(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestMemBytesCodec_RoundTrip(t *testing.T) {
	got, err := MemBytesCodec.Decode("512m")
	if err != nil {
		t.Fatalf("Decode: unexpected error: %v", err)
	}
	if got != 512*1024*1024 {
		t.Errorf("Decode(%q) = %d, want %d", "512m", got, 512*1024*1024)
	}

	raw, err := MemBytesCodec.Encode(got)
	if err != nil {
		t.Fatalf("Encode: unexpected error: %v", err)
	}
	if raw != "512m" {
		t.Errorf("Encode(%d) = %v, want %q", got, raw, "512m")
	}
}

func TestMemBytesCodec_DecodeRejectsInvalid(t *testing.T) {
	if _, err := MemBytesCodec.Decode("not-a-size"); err == nil {
		t.Error("Decode(\"not-a-size\"): want error, got nil")
	}
}
