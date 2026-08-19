package modulesummary

import (
	"testing"

	mcp "github.com/DaniDeer/go-codex/api/mcp"
	iothub "github.com/DaniDeer/go-codex/examples/go-edge-models/models/azure/iothub"
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

func TestReadReqCodec_AcceptsSystemModuleNames(t *testing.T) {
	for _, name := range []string{"edgeAgent", "edgeHub"} {
		req := ReadReq{BasePath: "/tmp/edge", UseCaseName: "usecase1", ModuleName: iothub.ModuleName(name)}
		if err := ReadReqCodec.Validate(req); err != nil {
			t.Errorf("Validate(%q): %v, want nil", name, err)
		}
	}
}

func TestReadReqCodec_RejectsUnknownSystemModuleName(t *testing.T) {
	req := ReadReq{BasePath: "/tmp/edge", UseCaseName: "usecase1", ModuleName: "edgeFoo"}
	if err := ReadReqCodec.Validate(req); err == nil {
		t.Error("Validate: want error for \"edgeFoo\" (neither a slug nor a reserved system module name), got nil")
	}
}

func TestReadTool_RegistersSuccessfully(t *testing.T) {
	builder := mcp.NewBuilder(mcp.Info{Name: "test", Version: "1.0.0"})
	if _, err := ReadTool.Register(builder); err != nil {
		t.Fatalf("ReadTool.Register: %v", err)
	}
}

func TestReadReqCodec_DeviceIDIsOptional(t *testing.T) {
	req := ReadReq{BasePath: "/tmp/edge", UseCaseName: "usecase1", ModuleName: "factory-dashboard"}
	if err := ReadReqCodec.Validate(req); err != nil {
		t.Errorf("Validate: want no error for absent DeviceID, got %v", err)
	}
}

func TestReadReqCodec_DeviceIDRoundTrip(t *testing.T) {
	req := ReadReq{BasePath: "/tmp/edge", UseCaseName: "usecase1", ModuleName: "factory-dashboard", DeviceID: "sensor-1"}

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
