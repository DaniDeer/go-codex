package iotedge

import (
	"testing"

	mcp "github.com/DaniDeer/go-codex/api/mcp"
)

func TestUpdateModuleImageReqCodec_RoundTrip(t *testing.T) {
	req := UpdateModuleImageReq{
		BasePath:    "/tmp/edge",
		UseCaseName: "usecase1",
		ModuleName:  "factory-dashboard",
		ImageURL:    "ghcr.io/org/repo:1.2.3",
	}

	encoded, err := UpdateModuleImageReqCodec.Encode(req)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	decoded, err := UpdateModuleImageReqCodec.Decode(encoded)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if decoded != req {
		t.Errorf("decoded = %+v, want %+v", decoded, req)
	}
}

func TestUpdateModuleImageReqCodec_RejectsEmptyBasePath(t *testing.T) {
	req := UpdateModuleImageReq{BasePath: "", UseCaseName: "usecase1", ModuleName: "factory-dashboard", ImageURL: "ghcr.io/org/repo:1.2.3"}
	if err := UpdateModuleImageReqCodec.Validate(req); err == nil {
		t.Error("Validate: want error for empty BasePath, got nil")
	}
}

func TestUpdateModuleImageReqCodec_RejectsEmptyUseCaseName(t *testing.T) {
	req := UpdateModuleImageReq{BasePath: "/tmp/edge", UseCaseName: "", ModuleName: "factory-dashboard", ImageURL: "ghcr.io/org/repo:1.2.3"}
	if err := UpdateModuleImageReqCodec.Validate(req); err == nil {
		t.Error("Validate: want error for empty UseCaseName, got nil")
	}
}

func TestUpdateModuleImageReqCodec_RejectsInvalidModuleName(t *testing.T) {
	req := UpdateModuleImageReq{BasePath: "/tmp/edge", UseCaseName: "usecase1", ModuleName: "", ImageURL: "ghcr.io/org/repo:1.2.3"}
	if err := UpdateModuleImageReqCodec.Validate(req); err == nil {
		t.Error("Validate: want error for empty ModuleName, got nil")
	}
}

func TestUpdateModuleImageReqCodec_RejectsEmptyImageURL(t *testing.T) {
	req := UpdateModuleImageReq{BasePath: "/tmp/edge", UseCaseName: "usecase1", ModuleName: "factory-dashboard", ImageURL: ""}
	if err := UpdateModuleImageReqCodec.Validate(req); err == nil {
		t.Error("Validate: want error for empty ImageURL, got nil")
	}
}

func TestUpdateModuleImageTool_RegistersSuccessfully(t *testing.T) {
	builder := mcp.NewBuilder(mcp.Info{Name: "test", Version: "1.0.0"})
	if _, err := UpdateModuleImageTool.Register(builder); err != nil {
		t.Fatalf("UpdateModuleImageTool.Register: %v", err)
	}
}
