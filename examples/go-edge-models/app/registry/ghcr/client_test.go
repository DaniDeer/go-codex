package ghcr_test

import (
	"net/http"
	"testing"

	registry "github.com/DaniDeer/go-codex/examples/go-edge-models/app/registry"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/app/registry/ghcr"
)

func TestNew_ReturnsNonNilClient(t *testing.T) {
	c := ghcr.New(http.DefaultClient)
	if c == nil {
		t.Fatal("New: want non-nil *Client")
	}
}

// Compile-time assertion: *Client must implement registry.Client
// STRUCTURALLY — see dockerhub's own identical test for the full
// "why an external test package" rationale.
var _ registry.Client = (*ghcr.Client)(nil)
