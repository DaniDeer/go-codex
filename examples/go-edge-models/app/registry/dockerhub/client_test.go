package dockerhub_test

import (
	"net/http"
	"testing"

	registry "github.com/DaniDeer/go-codex/examples/go-edge-models/app/registry"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/app/registry/dockerhub"
)

func TestNew_ReturnsNonNilClient(t *testing.T) {
	c := dockerhub.New(http.DefaultClient)
	if c == nil {
		t.Fatal("New: want non-nil *Client")
	}
}

// Compile-time assertion: *Client must implement registry.Client
// STRUCTURALLY (it embeds nothing — it's a fully self-contained client)
// — a regression guard against this package's own GetTags/GetImageMetadata
// signatures drifting out of sync with the shared interface. An external
// test package (dockerhub_test, not dockerhub) is required here
// specifically to avoid an import cycle: app/registry's own Dispatcher
// imports this package in production code, so this package's OWN
// (internal) test files cannot import app/registry back.
var _ registry.Client = (*dockerhub.Client)(nil)
