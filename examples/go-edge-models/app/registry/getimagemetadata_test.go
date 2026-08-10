package registry

import (
	"context"
	"errors"
	"net/http"
	"testing"

	regmodels "github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker/registry"
)

func TestNewGetImageMetadataToolHandler_PropagatesInvalidImageURL(t *testing.T) {
	// Same offline-safe rationale as
	// TestNewGetTagsToolHandler_PropagatesInvalidImageURL (gettags_test.go)
	// — ParseImageRef fails before any HTTP call for a malformed ImageURL.
	handler := NewGetImageMetadataToolHandler(http.DefaultClient)
	_, err := handler(context.Background(), regmodels.GetImageMetadataReq{ImageURL: "INVALID IMAGE REF!!"})
	if err == nil {
		t.Fatal("handler: want error for invalid ImageURL, got nil")
	}
	var parseErr regmodels.ImageRefParseError
	if !errors.As(err, &parseErr) {
		t.Errorf("handler error = %v, want ImageRefParseError", err)
	}
}
