package modulesummary

import (
	"testing"

	mcp "github.com/DaniDeer/go-codex/api/mcp"
)

func TestReadReqCodec_RoundTrip(t *testing.T) {
	req := ReadReq{BasePath: "/tmp/edge", UseCaseName: "usecase1", ModuleName: "factory-dashboard"}

	encoded, err := ReadReqCodec.Encode(req)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	decoded, err := ReadReqCodec.Decode(encoded)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if decoded != req {
		t.Errorf("decoded = %+v, want %+v", decoded, req)
	}
}

func TestReadReqCodec_RejectsEmptyBasePath(t *testing.T) {
	req := ReadReq{BasePath: "", UseCaseName: "usecase1", ModuleName: "factory-dashboard"}
	if err := ReadReqCodec.Validate(req); err == nil {
		t.Error("Validate: want error for empty BasePath, got nil")
	}
}

func TestReadReqCodec_RejectsEmptyUseCaseName(t *testing.T) {
	req := ReadReq{BasePath: "/tmp/edge", UseCaseName: "", ModuleName: "factory-dashboard"}
	if err := ReadReqCodec.Validate(req); err == nil {
		t.Error("Validate: want error for empty UseCaseName, got nil")
	}
}

func TestReadReqCodec_RejectsInvalidModuleName(t *testing.T) {
	req := ReadReq{BasePath: "/tmp/edge", UseCaseName: "usecase1", ModuleName: ""}
	if err := ReadReqCodec.Validate(req); err == nil {
		t.Error("Validate: want error for empty ModuleName, got nil")
	}
}

func TestReadTool_RegistersSuccessfully(t *testing.T) {
	builder := mcp.NewBuilder(mcp.Info{Name: "test", Version: "1.0.0"})
	if _, err := ReadTool.Register(builder); err != nil {
		t.Fatalf("ReadTool.Register: %v", err)
	}
}
