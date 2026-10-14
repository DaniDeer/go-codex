package sql_test

import (
	"context"
	"testing"
	"time"

	adaptersql "github.com/DaniDeer/go-codex/adapters/sql"
	"github.com/DaniDeer/go-codex/ports/portstest"
)

// TestPortstestConformance_SourceAdapterRoundTrip runs adapters/sql
// against ports/portstest's canonical SourceAdapter conformance scenario
// — this package's own, concrete proof that it satisfies
// ports.SourceAdapter's contract, independent of this package's own
// hand-written tests. Phase 5's "API-defined conformance test suites"
// mechanism (docs/roadmap/adapter-integration-architecture.md, Round 3
// decision 1), ports-family instance.
func TestPortstestConformance_SourceAdapterRoundTrip(t *testing.T) {
	queryFn := func(_ context.Context) ([]portstest.Reading, error) {
		return portstest.WantReadings, nil
	}
	adapter := adaptersql.QueryAdapter(portstest.ReadingCodec, queryFn, 20*time.Millisecond,
		adaptersql.QueryStreamOptions{Table: "readings", Op: "list"})

	portstest.AssertSourceAdapterRoundTrip(t, portstest.ReadingCodec, adapter, portstest.WantReadings)
}
