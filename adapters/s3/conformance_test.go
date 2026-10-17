package s3_test

import (
	"testing"

	adapters3 "github.com/DaniDeer/go-codex/adapters/s3"
	"github.com/DaniDeer/go-codex/api/file/filetest"
)

// TestFiletestConformance wires every api/file/filetest Assert*
// scenario into adapters/s3's own Backend — proving adapters/s3
// genuinely conforms to api/file's own declared contract, not just
// that the filetest package itself compiles. Mirrors
// adapters/file/conformance_test.go's identical pattern (D-0010) — a
// SECOND real Backend proving the same conformance suite.
func TestFiletestConformance(t *testing.T) {
	newBackend := func() *adapters3.Backend {
		return adapters3.NewBackend(newFakeS3("bucket"), "bucket")
	}
	t.Run("ReadWriteRoundTrip", func(t *testing.T) {
		filetest.AssertReadWriteRoundTrip(t, newBackend())
	})
	t.Run("PathParamMergeField", func(t *testing.T) {
		filetest.AssertPathParamMergeField(t, newBackend())
	})
	t.Run("SecurityGate", func(t *testing.T) {
		filetest.AssertSecurityGate(t, newBackend())
	})
	t.Run("OrderedMiddlewareChain", func(t *testing.T) {
		filetest.AssertOrderedMiddlewareChain(t, newBackend())
	})
	t.Run("ErrorPatternMapping", func(t *testing.T) {
		filetest.AssertErrorPatternMapping(t, newBackend())
	})
	t.Run("Observability", func(t *testing.T) {
		filetest.AssertObservability(t, newBackend(), &filetest.SpyObserver{})
	})
	t.Run("MetadataIntegration", func(t *testing.T) {
		filetest.AssertMetadataIntegration(t, newBackend())
	})
	t.Run("StreamRoundTrip", func(t *testing.T) {
		filetest.AssertStreamRoundTrip(t, newBackend())
	})
}
