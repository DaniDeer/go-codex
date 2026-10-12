package util

import "testing"

func TestManifestEnvelopeCodec_RoundTripsSingleManifest(t *testing.T) {
	wire := map[string]any{
		"schemaVersion": float64(2),
		"mediaType":     "application/vnd.docker.distribution.manifest.v2+json",
		"config": map[string]any{
			"mediaType": "application/vnd.docker.container.image.v1+json",
			"digest":    "sha256:" + stringsRepeat("a", 64),
			"size":      float64(1234),
		},
		"layers": []any{},
	}
	env, err := ManifestEnvelopeCodec.Decode(wire)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if env.Single == nil {
		t.Fatal("Decode: want Single populated, got nil")
	}
	if env.Single.SchemaVersion != 2 {
		t.Errorf("Single.SchemaVersion = %d, want 2", env.Single.SchemaVersion)
	}

	reEncoded, err := ManifestEnvelopeCodec.Encode(env)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	reDecoded, err := ManifestEnvelopeCodec.Decode(reEncoded)
	if err != nil {
		t.Fatalf("re-Decode: %v", err)
	}
	if reDecoded.Single.MediaType != env.Single.MediaType {
		t.Errorf("round-trip MediaType = %q, want %q", reDecoded.Single.MediaType, env.Single.MediaType)
	}
}

func TestManifestEnvelopeCodec_RoundTripsManifestList(t *testing.T) {
	wire := map[string]any{
		"schemaVersion": float64(2),
		"mediaType":     "application/vnd.docker.distribution.manifest.list.v2+json",
		"manifests": []any{
			map[string]any{
				"mediaType": "application/vnd.docker.distribution.manifest.v2+json",
				"digest":    "sha256:" + stringsRepeat("b", 64),
				"size":      float64(100),
				"platform":  map[string]any{"architecture": "amd64", "os": "linux"},
			},
		},
	}
	env, err := ManifestEnvelopeCodec.Decode(wire)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if env.List == nil {
		t.Fatal("Decode: want List populated, got nil")
	}
	if len(env.List.Manifests) != 1 {
		t.Fatalf("List.Manifests len = %d, want 1", len(env.List.Manifests))
	}
	if env.List.Manifests[0].Platform == nil || env.List.Manifests[0].Platform.OS != "linux" {
		t.Errorf("Manifests[0].Platform = %+v, want OS=linux", env.List.Manifests[0].Platform)
	}
}

func TestDigestCodec_RejectsMalformedDigest(t *testing.T) {
	if _, err := DigestCodec.Decode("not-a-digest"); err == nil {
		t.Error("Decode: want error for malformed digest, got nil")
	}
}

func stringsRepeat(s string, n int) string {
	out := make([]byte, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, s[0])
	}
	return string(out)
}
