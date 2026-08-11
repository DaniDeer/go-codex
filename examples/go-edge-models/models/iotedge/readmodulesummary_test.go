package iotedge

import (
	"testing"

	mcp "github.com/DaniDeer/go-codex/api/mcp"
)

func TestReadModuleSummaryReqCodec_RoundTrip(t *testing.T) {
	req := ReadModuleSummaryReq{ManifestPath: "/tmp/manifest.json", ModuleName: "factory-dashboard"}

	encoded, err := ReadModuleSummaryReqCodec.Encode(req)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	decoded, err := ReadModuleSummaryReqCodec.Decode(encoded)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if decoded != req {
		t.Errorf("decoded = %+v, want %+v", decoded, req)
	}
}

func TestReadModuleSummaryReqCodec_RejectsEmptyManifestPath(t *testing.T) {
	req := ReadModuleSummaryReq{ManifestPath: "", ModuleName: "factory-dashboard"}
	if err := ReadModuleSummaryReqCodec.Validate(req); err == nil {
		t.Error("Validate: want error for empty ManifestPath, got nil")
	}
}

func TestReadModuleSummaryReqCodec_RejectsInvalidModuleName(t *testing.T) {
	req := ReadModuleSummaryReq{ManifestPath: "/tmp/manifest.json", ModuleName: ""}
	if err := ReadModuleSummaryReqCodec.Validate(req); err == nil {
		t.Error("Validate: want error for empty ModuleName, got nil")
	}
}

func TestReadModuleSummaryTool_RegistersSuccessfully(t *testing.T) {
	builder := mcp.NewBuilder(mcp.Info{Name: "test", Version: "1.0.0"})
	if _, err := ReadModuleSummaryTool.Register(builder); err != nil {
		t.Fatalf("ReadModuleSummaryTool.Register: %v", err)
	}
}
