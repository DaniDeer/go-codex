package file_test

import (
	"testing"

	adapterfile "github.com/DaniDeer/go-codex/adapters/file"
	"github.com/DaniDeer/go-codex/api/file"
	"github.com/DaniDeer/go-codex/api/file/filetest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
)

type conformanceCapRecord struct{ X int }

func conformanceCapRecordCodec() codex.Codec[conformanceCapRecord] {
	return codex.Struct[conformanceCapRecord](
		codex.RequiredField("x", codex.Int(),
			func(r conformanceCapRecord) int { return r.X },
			func(r *conformanceCapRecord, v int) { r.X = v }),
	)
}

// TestFiletestConformance wires every api/file/filetest Assert* scenario
// into this adapter's real Backend — proving adapters/file genuinely
// conforms to api/file's own declared contract, not just that the
// filetest package itself compiles. Mirrors
// api/rest/api/events/api/reqreply's identical "conformance suite proven
// via a real adapter" pattern (D-0010).
func TestFiletestConformance(t *testing.T) {
	t.Run("ReadWriteRoundTrip", func(t *testing.T) {
		filetest.AssertReadWriteRoundTrip(t, adapterfile.NewBackend())
	})
	t.Run("PathParamMergeField", func(t *testing.T) {
		filetest.AssertPathParamMergeField(t, adapterfile.NewBackend())
	})
	t.Run("SecurityGate", func(t *testing.T) {
		filetest.AssertSecurityGate(t, adapterfile.NewBackend())
	})
	t.Run("OrderedMiddlewareChain", func(t *testing.T) {
		filetest.AssertOrderedMiddlewareChain(t, adapterfile.NewBackend())
	})
	t.Run("ErrorPatternMapping", func(t *testing.T) {
		filetest.AssertErrorPatternMapping(t, adapterfile.NewBackend())
	})
	t.Run("CapabilityRejected", func(t *testing.T) {
		dir := t.TempDir()
		res := file.NewResource[conformanceCapRecord](dir+"/{id}.json", format.JSON(conformanceCapRecordCodec()))
		_, err := res.WithRead(file.Read{}).RequireCapabilities(file.RequireAccessMode()).Handle(file.NewClient())
		filetest.AssertCapabilityRejected(t, err)
	})
	t.Run("Observability", func(t *testing.T) {
		filetest.AssertObservability(t, adapterfile.NewBackend(), &filetest.SpyObserver{})
	})
	t.Run("MetadataIntegration", func(t *testing.T) {
		filetest.AssertMetadataIntegration(t, adapterfile.NewBackend())
	})
	t.Run("StreamRoundTrip", func(t *testing.T) {
		filetest.AssertStreamRoundTrip(t, adapterfile.NewBackend())
	})
}
