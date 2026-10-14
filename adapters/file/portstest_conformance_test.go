package file_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	fileadapter "github.com/DaniDeer/go-codex/adapters/file"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/ports"
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

// TestPortstestConformance_SinkAdapterRoundTrip runs adapters/file
// against ports/portstest's canonical SinkAdapter conformance scenario —
// this package's own, concrete proof that it satisfies
// ports.SinkAdapter's contract. Writes [portstest.WantReadings] through
// [fileadapter.DrainWriteAdapter] into an in-memory buffer (the shared
// [ports.SinkAdapter] contract only needs an io.Writer — no real file
// required, mirroring this adapter's own existing
// TestDrainWriteAdapter_EncodesAndWrites test), then verifies every
// written NDJSON line round-trips back to the exact fed Reading values.
func TestPortstestConformance_SinkAdapterRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	adapter := fileadapter.DrainWriteAdapter(&buf, format.JSON(portstest.ReadingCodec), fileadapter.DrainWriteAdapterOptions{})

	port, err := ports.NewSinkPort[portstest.Reading]("portstest-sink", portstest.ReadingCodec, ports.PortOptions{Buffer: 8})
	if err != nil {
		t.Fatalf("NewSinkPort: %v", err)
	}
	port.Bind(context.Background(), adapter)

	portstest.AssertSinkAdapterRoundTrip(t, port, func(t *testing.T) {
		t.Helper()
		lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
		if len(lines) != len(portstest.WantReadings) {
			t.Fatalf("want %d written lines, got %d: %q", len(portstest.WantReadings), len(lines), buf.String())
		}
		for i, line := range lines {
			var got portstest.Reading
			if err := json.Unmarshal([]byte(line), &got); err != nil {
				t.Fatalf("unmarshal line %d: %v", i, err)
			}
			if got != portstest.WantReadings[i] {
				t.Errorf("line %d = %+v, want %+v", i, got, portstest.WantReadings[i])
			}
		}
	})
}
