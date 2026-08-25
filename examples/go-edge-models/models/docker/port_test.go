package docker

import "testing"

func TestParsePortMapping_ContainerOnly(t *testing.T) {
	port, binding, err := ParsePortMapping("80")
	if err != nil {
		t.Fatalf("ParsePortMapping: unexpected error: %v", err)
	}
	if port != "80/tcp" {
		t.Errorf("port = %q, want %q", port, "80/tcp")
	}
	if binding != nil {
		t.Errorf("binding = %+v, want nil", binding)
	}
}

func TestParsePortMapping_HostAndContainer(t *testing.T) {
	port, binding, err := ParsePortMapping("8080:80")
	if err != nil {
		t.Fatalf("ParsePortMapping: unexpected error: %v", err)
	}
	if port != "80/tcp" {
		t.Errorf("port = %q, want %q", port, "80/tcp")
	}
	if binding == nil {
		t.Fatal("binding = nil, want non-nil")
	}
	if binding.HostPort != "8080" {
		t.Errorf("binding.HostPort = %q, want %q", binding.HostPort, "8080")
	}
}

func TestParsePortMapping_ExplicitProtocol(t *testing.T) {
	port, binding, err := ParsePortMapping("8080:80/udp")
	if err != nil {
		t.Fatalf("ParsePortMapping: unexpected error: %v", err)
	}
	if port != "80/udp" {
		t.Errorf("port = %q, want %q", port, "80/udp")
	}
	if binding == nil || binding.HostPort != "8080" {
		t.Errorf("binding = %+v, want HostPort=8080", binding)
	}
}

func TestParsePortMapping_RejectsInterfacePrefixed(t *testing.T) {
	_, _, err := ParsePortMapping("127.0.0.1:8080:80")
	if err == nil {
		t.Fatal("ParsePortMapping with IP-prefixed form: want error, got nil")
	}
	var pmErr PortMappingError
	if !asPortMappingError(err, &pmErr) {
		t.Errorf("error type = %T, want PortMappingError", err)
	}
}

func TestParsePortMapping_RejectsInvalidPortNumber(t *testing.T) {
	cases := []string{"", "abc", "8080:abc", "abc:80", "70000", "0"}
	for _, in := range cases {
		if _, _, err := ParsePortMapping(in); err == nil {
			t.Errorf("ParsePortMapping(%q): want error, got nil", in)
		}
	}
}

func TestParsePortMapping_RejectsUnrecognizedProtocol(t *testing.T) {
	if _, _, err := ParsePortMapping("8080:80/sctp"); err == nil {
		t.Error("ParsePortMapping with unrecognized protocol: want error, got nil")
	}
}

func asPortMappingError(err error, target *PortMappingError) bool {
	pmErr, ok := err.(PortMappingError)
	if ok {
		*target = pmErr
	}
	return ok
}

func TestFormatPortMapping_ContainerOnly(t *testing.T) {
	got, err := FormatPortMapping("80/tcp", "")
	if err != nil {
		t.Fatalf("FormatPortMapping: unexpected error: %v", err)
	}
	if got != "80" {
		t.Errorf("got %q, want %q", got, "80")
	}
}

func TestFormatPortMapping_HostAndContainer(t *testing.T) {
	got, err := FormatPortMapping("80/tcp", "8080")
	if err != nil {
		t.Fatalf("FormatPortMapping: unexpected error: %v", err)
	}
	if got != "8080:80" {
		t.Errorf("got %q, want %q", got, "8080:80")
	}
}

func TestFormatPortMapping_UDP(t *testing.T) {
	got, err := FormatPortMapping("80/udp", "8080")
	if err != nil {
		t.Fatalf("FormatPortMapping: unexpected error: %v", err)
	}
	if got != "8080:80/udp" {
		t.Errorf("got %q, want %q", got, "8080:80/udp")
	}
}

func TestFormatPortMapping_RejectsMissingProtocol(t *testing.T) {
	if _, err := FormatPortMapping("80", ""); err == nil {
		t.Error("FormatPortMapping without protocol suffix: want error, got nil")
	}
}

func TestFormatPortMapping_RejectsInvalidHostPort(t *testing.T) {
	if _, err := FormatPortMapping("80/tcp", "abc"); err == nil {
		t.Error("FormatPortMapping with invalid host port: want error, got nil")
	}
}

func TestPortMapping_RoundTrip(t *testing.T) {
	cases := []string{"80", "8080:80", "8080:80/udp"}
	for _, raw := range cases {
		port, binding, err := ParsePortMapping(raw)
		if err != nil {
			t.Fatalf("ParsePortMapping(%q): unexpected error: %v", raw, err)
		}
		hostPort := ""
		if binding != nil {
			hostPort = binding.HostPort
		}
		got, err := FormatPortMapping(port, hostPort)
		if err != nil {
			t.Fatalf("FormatPortMapping: unexpected error: %v", err)
		}
		if got != raw {
			t.Errorf("round trip %q -> %q, want unchanged", raw, got)
		}
	}
}

func TestPortMappingCodec_DecodeContainerOnly(t *testing.T) {
	got, err := PortMappingCodec.Decode("80")
	if err != nil {
		t.Fatalf("PortMappingCodec.Decode(%q): unexpected error: %v", "80", err)
	}
	want := PortMapping{Port: "80/tcp", HostPort: ""}
	if got != want {
		t.Errorf("PortMappingCodec.Decode(%q) = %+v, want %+v", "80", got, want)
	}
}

func TestPortMappingCodec_DecodeHostAndContainer(t *testing.T) {
	got, err := PortMappingCodec.Decode("8080:80/udp")
	if err != nil {
		t.Fatalf("PortMappingCodec.Decode: unexpected error: %v", err)
	}
	want := PortMapping{Port: "80/udp", HostPort: "8080"}
	if got != want {
		t.Errorf("PortMappingCodec.Decode = %+v, want %+v", got, want)
	}
}

func TestPortMappingCodec_DecodeRejectsInvalid(t *testing.T) {
	if _, err := PortMappingCodec.Decode("127.0.0.1:8080:80"); err == nil {
		t.Error("PortMappingCodec.Decode with interface-prefixed form: want error, got nil")
	}
}

func TestPortMappingCodec_EncodeRoundTrip(t *testing.T) {
	cases := []string{"80", "8080:80", "8080:80/udp"}
	for _, raw := range cases {
		decoded, err := PortMappingCodec.Decode(raw)
		if err != nil {
			t.Fatalf("PortMappingCodec.Decode(%q): unexpected error: %v", raw, err)
		}
		encoded, err := PortMappingCodec.Encode(decoded)
		if err != nil {
			t.Fatalf("PortMappingCodec.Encode(%+v): unexpected error: %v", decoded, err)
		}
		if encoded != raw {
			t.Errorf("round trip %q -> %v, want unchanged", raw, encoded)
		}
	}
}
