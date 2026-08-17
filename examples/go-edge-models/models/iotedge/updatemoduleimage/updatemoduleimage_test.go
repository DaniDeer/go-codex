package updatemoduleimage

import (
	"testing"

	mcp "github.com/DaniDeer/go-codex/api/mcp"
)

func TestReqCodec_RoundTrip(t *testing.T) {
	req := Req{
		BasePath:    "/tmp/edge",
		UseCaseName: "usecase1",
		ModuleName:  "factory-dashboard",
		ImageURL:    "ghcr.io/org/repo:1.2.3",
	}

	encoded, err := ReqCodec.Encode(req)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	decoded, err := ReqCodec.Decode(encoded)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if decoded != req {
		t.Errorf("decoded = %+v, want %+v", decoded, req)
	}
}

func TestReqCodec_RejectsEmptyBasePath(t *testing.T) {
	req := Req{BasePath: "", UseCaseName: "usecase1", ModuleName: "factory-dashboard", ImageURL: "ghcr.io/org/repo:1.2.3"}
	if err := ReqCodec.Validate(req); err == nil {
		t.Error("Validate: want error for empty BasePath, got nil")
	}
}

func TestReqCodec_RejectsEmptyUseCaseName(t *testing.T) {
	req := Req{BasePath: "/tmp/edge", UseCaseName: "", ModuleName: "factory-dashboard", ImageURL: "ghcr.io/org/repo:1.2.3"}
	if err := ReqCodec.Validate(req); err == nil {
		t.Error("Validate: want error for empty UseCaseName, got nil")
	}
}

func TestReqCodec_RejectsInvalidModuleName(t *testing.T) {
	req := Req{BasePath: "/tmp/edge", UseCaseName: "usecase1", ModuleName: "", ImageURL: "ghcr.io/org/repo:1.2.3"}
	if err := ReqCodec.Validate(req); err == nil {
		t.Error("Validate: want error for empty ModuleName, got nil")
	}
}

func TestReqCodec_RejectsEmptyImageURL(t *testing.T) {
	req := Req{BasePath: "/tmp/edge", UseCaseName: "usecase1", ModuleName: "factory-dashboard", ImageURL: ""}
	if err := ReqCodec.Validate(req); err == nil {
		t.Error("Validate: want error for empty ImageURL, got nil")
	}
}

func TestTool_RegistersSuccessfully(t *testing.T) {
	builder := mcp.NewBuilder(mcp.Info{Name: "test", Version: "1.0.0"})
	if _, err := Tool.Register(builder); err != nil {
		t.Fatalf("Tool.Register: %v", err)
	}
}
