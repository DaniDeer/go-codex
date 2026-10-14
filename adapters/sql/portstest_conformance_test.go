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

// TestPortstestConformance_IOAdapterRoundTrip runs adapters/sql against
// ports/portstest's canonical IOAdapter conformance scenario — this
// package's own, concrete proof that it satisfies ports.IOAdapter's
// contract. queryFn looks up each fed [portstest.WantReadingRequests]
// entry BY ID (no real database needed, mirroring
// TestPortstestConformance_SourceAdapterRoundTrip's own closure-based
// queryFn convention) and returns the matching full
// [portstest.WantReadings] record.
func TestPortstestConformance_IOAdapterRoundTrip(t *testing.T) {
	byID := make(map[string]portstest.Reading, len(portstest.WantReadings))
	for _, r := range portstest.WantReadings {
		byID[r.ID] = r
	}
	queryFn := func(_ context.Context, req portstest.Reading) ([]portstest.Reading, error) {
		return []portstest.Reading{byID[req.ID]}, nil
	}
	adapter := adaptersql.QueryEachAdapter(portstest.ReadingCodec, queryFn,
		adaptersql.QueryEachStreamOptions{Table: "readings", Op: "get_by_id"})

	portstest.AssertIOAdapterRoundTrip(t, portstest.ReadingCodec, portstest.ReadingCodec, adapter)
}
