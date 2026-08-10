package registry

import (
	"context"
	"errors"
	"net/http"
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

func TestNewGetTagsToolHandler_PropagatesInvalidImageURL(t *testing.T) {
	// No network round trip needed: ParseImageRef fails before any HTTP
	// call is made for a malformed ImageURL, exactly like GetTags itself
	// (see TestImageRefCodec_RejectsInvalidShape's equivalent coverage of
	// the same underlying codec) — this keeps the test offline-safe,
	// consistent with this package's IO-free-by-default test philosophy.
	handler := NewGetTagsToolHandler(http.DefaultClient)
	_, err := handler(context.Background(), GetTagsToolReq{ImageURL: "INVALID IMAGE REF!!"})
	if err == nil {
		t.Fatal("handler: want error for invalid ImageURL, got nil")
	}
	var parseErr ImageRefParseError
	if !errors.As(err, &parseErr) {
		t.Errorf("handler error = %v, want ImageRefParseError", err)
	}
}
