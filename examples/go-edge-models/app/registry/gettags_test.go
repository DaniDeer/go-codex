package registry

import (
	"context"
	"errors"
	"net/http"
	"testing"

	regmodels "github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker/registry"
)

func TestNewGetTagsToolHandler_PropagatesInvalidImageURL(t *testing.T) {
	// No network round trip needed: ParseImageRef fails before any HTTP
	// call is made for a malformed ImageURL, exactly like GetTags itself
	// (see models/docker/registry's TestImageRefCodec_RejectsInvalidShape
	// for the same underlying codec's own coverage) — this keeps the test
	// offline-safe, consistent with this package's IO-free-by-default test
	// philosophy.
	handler := NewGetTagsToolHandler(http.DefaultClient)
	_, err := handler(context.Background(), regmodels.GetTagsToolReq{ImageURL: "INVALID IMAGE REF!!"})
	if err == nil {
		t.Fatal("handler: want error for invalid ImageURL, got nil")
	}
	var parseErr regmodels.ImageRefParseError
	if !errors.As(err, &parseErr) {
		t.Errorf("handler error = %v, want ImageRefParseError", err)
	}
}
