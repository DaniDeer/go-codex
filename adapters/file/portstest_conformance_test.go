package file_test

import (
	"os"
	"path/filepath"
	"testing"

	fileadapter "github.com/DaniDeer/go-codex/adapters/file"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/ports/portstest"
)

// TestPortstestConformance_SourceAdapterRoundTrip runs adapters/file
// against ports/portstest's canonical SourceAdapter conformance scenario
// — this package's own, concrete proof that it satisfies
// ports.SourceAdapter's contract, independent of this package's own
// hand-written tests. Phase 5's "API-defined conformance test suites"
// mechanism (docs/roadmap/adapter-integration-architecture.md, Round 3
// decision 1), ports-family instance.
func TestPortstestConformance_SourceAdapterRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "readings.ndjson")
	content := `{"id":"reading-1","value":1.5}` + "\n" + `{"id":"reading-2","value":2.5}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	adapter := fileadapter.ScanAdapter(path, format.JSON(portstest.ReadingCodec), fileadapter.ScanAdapterOptions{})

	portstest.AssertSourceAdapterRoundTrip(t, portstest.ReadingCodec, adapter, portstest.WantReadings)
}
