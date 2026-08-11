package registry

import (
	"testing"

	mcp "github.com/DaniDeer/go-codex/api/mcp"
)

func TestGetTagsToolReqCodec_RoundTrip(t *testing.T) {
	req := GetTagsToolReq{ImageURL: "alpine:latest"}

	encoded, err := GetTagsToolReqCodec.Encode(req)
	if err != nil {
		t.Fatalf("GetTagsToolReqCodec.Encode: %v", err)
	}
	decoded, err := GetTagsToolReqCodec.Decode(encoded)
	if err != nil {
		t.Fatalf("GetTagsToolReqCodec.Decode: %v", err)
	}
	if decoded != req {
		t.Errorf("decoded = %+v, want %+v", decoded, req)
	}
}

func TestGetTagsToolReqCodec_RejectsEmptyImageURL(t *testing.T) {
	if err := GetTagsToolReqCodec.Validate(GetTagsToolReq{}); err == nil {
		t.Error("Validate: want error for empty ImageURL, got nil")
	}
}

func TestGetTagsTool_RegistersSuccessfully(t *testing.T) {
	builder := mcp.NewBuilder(mcp.Info{Name: "test", Version: "1.0.0"})
	if _, err := GetTagsTool.Register(builder); err != nil {
		t.Fatalf("GetTagsTool.Register: %v", err)
	}
}

func TestGetTagsToolReqCodec_RoundTripsLimitAndSort(t *testing.T) {
	req := GetTagsToolReq{ImageURL: "alpine", Limit: 5, Sort: "alphabetical"}

	encoded, err := GetTagsToolReqCodec.Encode(req)
	if err != nil {
		t.Fatalf("GetTagsToolReqCodec.Encode: %v", err)
	}
	decoded, err := GetTagsToolReqCodec.Decode(encoded)
	if err != nil {
		t.Fatalf("GetTagsToolReqCodec.Decode: %v", err)
	}
	if decoded != req {
		t.Errorf("decoded = %+v, want %+v", decoded, req)
	}
}

func TestGetTagsToolReqCodec_AbsentLimitAndSortDefaultToZeroValue(t *testing.T) {
	req := GetTagsToolReq{ImageURL: "alpine"}
	if err := GetTagsToolReqCodec.Validate(req); err != nil {
		t.Errorf("Validate(%+v) = %v, want nil (Limit/Sort absent should be valid)", req, err)
	}
}

func TestGetTagsToolReqCodec_RejectsInvalidSort(t *testing.T) {
	req := GetTagsToolReq{ImageURL: "alpine", Sort: "not-a-real-mode"}
	if err := GetTagsToolReqCodec.Validate(req); err == nil {
		t.Error("Validate: want error for invalid Sort value, got nil")
	}
}

func TestGetTagsToolReqCodec_AcceptsEachAllowedSortValue(t *testing.T) {
	for _, mode := range sortModeAllowedValues {
		req := GetTagsToolReq{ImageURL: "alpine", Sort: mode}
		if err := GetTagsToolReqCodec.Validate(req); err != nil {
			t.Errorf("Validate(Sort=%q) = %v, want nil", mode, err)
		}
	}
}
