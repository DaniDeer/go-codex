package registry

import (
	"testing"

	mcp "github.com/DaniDeer/go-codex/api/mcp"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker"
)

func TestGetImageMetadataReqCodec_RoundTrip(t *testing.T) {
	req := GetImageMetadataReq{ImageURL: "alpine:latest", Platform: "linux/amd64"}

	encoded, err := GetImageMetadataReqCodec.Encode(req)
	if err != nil {
		t.Fatalf("GetImageMetadataReqCodec.Encode: %v", err)
	}
	decoded, err := GetImageMetadataReqCodec.Decode(encoded)
	if err != nil {
		t.Fatalf("GetImageMetadataReqCodec.Decode: %v", err)
	}
	if decoded != req {
		t.Errorf("decoded = %+v, want %+v", decoded, req)
	}
}

func TestGetImageMetadataReqCodec_PlatformOptional(t *testing.T) {
	req := GetImageMetadataReq{ImageURL: "alpine:latest"}

	encoded, err := GetImageMetadataReqCodec.Encode(req)
	if err != nil {
		t.Fatalf("GetImageMetadataReqCodec.Encode: %v", err)
	}
	decoded, err := GetImageMetadataReqCodec.Decode(encoded)
	if err != nil {
		t.Fatalf("GetImageMetadataReqCodec.Decode: %v", err)
	}
	if decoded != req {
		t.Errorf("decoded = %+v, want %+v", decoded, req)
	}
}

func TestGetImageMetadataReqCodec_RejectsEmptyImageURL(t *testing.T) {
	if err := GetImageMetadataReqCodec.Validate(GetImageMetadataReq{Platform: "linux/amd64"}); err == nil {
		t.Error("Validate: want error for empty ImageURL, got nil")
	}
}

func TestManifestMetadataCodec_RoundTrip(t *testing.T) {
	meta := ManifestMetadata{
		Image: docker.Image{
			Name:   "ghcr.io/org/repo",
			Tag:    "1.2.3",
			Digest: "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b85",
		},
		SchemaVersion:  2,
		MediaType:      "application/vnd.docker.distribution.manifest.v2+json",
		TotalSizeBytes: 42005000,
	}

	encoded, err := ManifestMetadataCodec.Encode(meta)
	if err != nil {
		t.Fatalf("ManifestMetadataCodec.Encode: %v", err)
	}
	decoded, err := ManifestMetadataCodec.Decode(encoded)
	if err != nil {
		t.Fatalf("ManifestMetadataCodec.Decode: %v", err)
	}
	if decoded != meta {
		t.Errorf("decoded = %+v, want %+v", decoded, meta)
	}
}

func TestManifestMetadataCodec_RejectsEmptyMediaType(t *testing.T) {
	meta := ManifestMetadata{
		Image:          docker.Image{Name: "alpine", Tag: "latest"},
		SchemaVersion:  2,
		TotalSizeBytes: 100,
	}
	if err := ManifestMetadataCodec.Validate(meta); err == nil {
		t.Error("Validate: want error for empty MediaType, got nil")
	}
}

func TestManifestMetadataCodec_RejectsNonPositiveTotalSizeBytes(t *testing.T) {
	meta := ManifestMetadata{
		Image:         docker.Image{Name: "alpine", Tag: "latest"},
		SchemaVersion: 2,
		MediaType:     "application/vnd.docker.distribution.manifest.v2+json",
	}
	if err := ManifestMetadataCodec.Validate(meta); err == nil {
		t.Error("Validate: want error for zero TotalSizeBytes, got nil")
	}
}

func TestGetImageMetadataTool_RegistersSuccessfully(t *testing.T) {
	builder := mcp.NewBuilder(mcp.Info{Name: "test", Version: "1.0.0"})
	if _, err := GetImageMetadataTool.Register(builder); err != nil {
		t.Fatalf("GetImageMetadataTool.Register: %v", err)
	}
}
