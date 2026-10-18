package smb

import (
	"testing"

	apifile "github.com/DaniDeer/go-codex/api/file"
	"github.com/DaniDeer/go-codex/api/file/filetest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
)

type conformanceSMBRecord struct{ X int }

func conformanceSMBRecordCodec() codex.Codec[conformanceSMBRecord] {
	return codex.Struct[conformanceSMBRecord](
		codex.RequiredField("x", codex.Int(),
			func(r conformanceSMBRecord) int { return r.X },
			func(r *conformanceSMBRecord, v int) { r.X = v }),
	)
}

// TestFiletestConformance wires every api/file/filetest Assert*
// scenario into adapters/smb's own Backend — proving adapters/smb
// genuinely conforms to api/file's own declared contract, not just that
// the filetest package itself compiles. Mirrors
// adapters/file/conformance_test.go's and adapters/s3/conformance_test.go's
// identical pattern (D-0010) — a THIRD real Backend proving the same
// conformance suite.
func TestFiletestConformance(t *testing.T) {
	newBackend := func() *Backend { return newBackendFromOps(newFakeShare()) }

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
	t.Run("CapabilityRejected", func(t *testing.T) {
		dir := "scratch"
		res := apifile.NewResource[conformanceSMBRecord](dir+"/{id}.json", format.JSON(conformanceSMBRecordCodec()))
		_, err := res.WithRead(apifile.Read{}).RequireCapabilities(apifile.RequireAccessMode()).Handle(apifile.NewClient())
		filetest.AssertCapabilityRejected(t, err)
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
