package filetest

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/api/file"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/stats"
)

// record is the canonical domain type every scenario below declares a
// [file.Resource] against — deliberately minimal so an Assert failure is
// attributable to the Backend/dispatch mechanism itself, not an
// incidental codec detail.
type record struct {
	ID    string
	Value int
}

func recordCodec() codex.Codec[record] {
	return codex.Struct[record](
		codex.RequiredField("id", codex.String(),
			func(r record) string { return r.ID },
			func(r *record, v string) { r.ID = v }),
		codex.RequiredField("value", codex.Int(),
			func(r record) int { return r.Value },
			func(r *record, v int) { r.Value = v }),
	)
}

func newClient(t *testing.T, backend file.Backend) (*file.Client, string) {
	t.Helper()
	dir := t.TempDir()
	client := file.NewClient()
	if err := client.Attach(backend); err != nil {
		t.Fatalf("filetest: Attach: %v", err)
	}
	return client, dir
}

// ── Scenario 1: basic Create/Read round trip ────────────────────────────

// AssertReadWriteRoundTrip proves backend correctly persists and
// retrieves a declared [file.Resource] — mirrors
// [github.com/DaniDeer/go-codex/api/rest/resttest.AssertEchoRoute]'s role
// as the baseline "does dispatch even work" scenario.
func AssertReadWriteRoundTrip(t *testing.T, backend file.Backend) {
	t.Helper()
	client, dir := newClient(t, backend)
	res := file.NewResource[record](filepath.Join(dir, "{id}.json"), format.JSON(recordCodec()), file.PathParam{Name: "id"})
	ctx := context.Background()
	vars := map[string]string{"id": "alice"}

	t.Run("create_then_read_round_trips", func(t *testing.T) {
		if err := client.Create(ctx, res.WithCreate(file.Create{}), record{ID: "alice", Value: 42}, vars); err != nil {
			t.Fatalf("Create: %v", err)
		}
		gotAny, err := client.Read(ctx, res.WithRead(file.Read{}), vars)
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		if got := gotAny.(record); got.Value != 42 {
			t.Fatalf("Read: got Value=%d, want 42", got.Value)
		}
	})

	t.Run("read_missing_reports_FileNotFoundError", func(t *testing.T) {
		_, err := client.Read(ctx, res.WithRead(file.Read{}), map[string]string{"id": "ghost"})
		var notFound file.FileNotFoundError
		if !errors.As(err, &notFound) {
			t.Fatalf("want FileNotFoundError, got %v (%T)", err, err)
		}
	})
}

// ── Scenario 2: path-param merge field ──────────────────────────────────

type mergedRecord struct {
	ID    string
	Value int
}

func mergedRecordCodec() codex.Codec[mergedRecord] {
	return codex.Struct[mergedRecord](
		codex.RequiredField("value", codex.Int(),
			func(r mergedRecord) int { return r.Value },
			func(r *mergedRecord, v int) { r.Value = v }),
	)
}

// AssertPathParamMergeField proves a [file.MergedPathParam] decodes the
// path variable directly into T's own field on Read, WITHOUT the codec
// needing to carry that field itself — mirrors
// [github.com/DaniDeer/go-codex/api/rest/resttest.AssertPathParamRoute].
func AssertPathParamMergeField(t *testing.T, backend file.Backend) {
	t.Helper()
	client, dir := newClient(t, backend)
	res := file.NewResource[mergedRecord](filepath.Join(dir, "{id}.json"), format.JSON(mergedRecordCodec()),
		file.NewPathParam("id", codex.String(),
			func(r mergedRecord) string { return r.ID },
			func(r *mergedRecord, v string) { r.ID = v }),
	)
	ctx := context.Background()
	vars := map[string]string{"id": "bob"}

	if err := client.Create(ctx, res.WithCreate(file.Create{}), mergedRecord{ID: "bob", Value: 7}, vars); err != nil {
		t.Fatalf("Create: %v", err)
	}
	gotAny, err := client.Read(ctx, res.WithRead(file.Read{}), vars)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	got := gotAny.(mergedRecord)
	if got.ID != "bob" {
		t.Fatalf("Read: want ID merged from path var = %q, got %q", "bob", got.ID)
	}
}

// ── Scenario 3: security gate ───────────────────────────────────────────

type secGrant struct {
	GrantedScopes map[string][]string
}

// AssertSecurityGate proves a [file.BoundSecurityReadMiddleware] genuinely
// gates dispatch: an unmet Security requirement is rejected BEFORE
// backend is ever called (proven via the file remaining readable through
// an UNGATED handle afterward — the backend was never given a chance to
// reject it on its own). Mirrors
// [github.com/DaniDeer/go-codex/api/rest/resttest.AssertSecurityRoute].
func AssertSecurityGate(t *testing.T, backend file.Backend) {
	t.Helper()
	client, dir := newClient(t, backend)
	res := file.NewResource[record](filepath.Join(dir, "{id}.json"), format.JSON(recordCodec()), file.PathParam{Name: "id"})
	ctx := context.Background()
	vars := map[string]string{"id": "secured"}
	if err := client.Create(ctx, res.WithCreate(file.Create{}), record{ID: "secured", Value: 1}, vars); err != nil {
		t.Fatalf("Create: %v", err)
	}

	bm := file.BoundSecurityReadMiddleware[record, struct{}, secGrant](
		"apiKey", file.SecurityScheme{}, []string{"read"},
		func(ctx context.Context, v *record, in struct{}) (secGrant, error) {
			return secGrant{GrantedScopes: map[string][]string{"apiKey": {"wrong-scope"}}}, nil
		},
	)
	gated := res.WithRead(file.Read{}).ReadBoundMW(bm).WithSecurity(file.SecurityRequirement{"apiKey": {"read"}})
	if _, err := client.Read(ctx, gated, vars); err == nil {
		t.Fatalf("want rejection for an unmet Security requirement")
	}

	// An ungated Read for the SAME resource still succeeds — proves the
	// backend itself was never asked to reject it; the gate did.
	if _, err := client.Read(ctx, res.WithRead(file.Read{}), vars); err != nil {
		t.Fatalf("ungated Read should still succeed: %v", err)
	}
}

// ── Scenario 4: ordered middleware chain ────────────────────────────────

// AssertOrderedMiddlewareChain proves 2+ attached BoundReadMiddleware
// values run in attachment order — mirrors
// [github.com/DaniDeer/go-codex/api/rest/resttest.AssertMiddlewareChainRoute].
func AssertOrderedMiddlewareChain(t *testing.T, backend file.Backend) {
	t.Helper()
	client, dir := newClient(t, backend)
	res := file.NewResource[record](filepath.Join(dir, "{id}.json"), format.JSON(recordCodec()), file.PathParam{Name: "id"})
	ctx := context.Background()
	vars := map[string]string{"id": "ordered"}
	if err := client.Create(ctx, res.WithCreate(file.Create{}), record{ID: "ordered", Value: 1}, vars); err != nil {
		t.Fatalf("Create: %v", err)
	}

	var order []string
	first := file.NewBoundReadMiddleware[record, struct{}, struct{}](
		file.NewDeclaration("first", codex.Struct[struct{}](), codex.Struct[struct{}]()),
		func(ctx context.Context, v *record, in struct{}) (struct{}, error) {
			order = append(order, "first")
			return struct{}{}, nil
		},
	)
	second := file.NewBoundReadMiddleware[record, struct{}, struct{}](
		file.NewDeclaration("second", codex.Struct[struct{}](), codex.Struct[struct{}]()),
		func(ctx context.Context, v *record, in struct{}) (struct{}, error) {
			order = append(order, "second")
			return struct{}{}, nil
		},
	)
	reader := res.WithRead(file.Read{}).ReadBoundMW(first).ReadBoundMW(second)
	if _, err := client.Read(ctx, reader, vars); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(order) != 2 || order[0] != "first" || order[1] != "second" {
		t.Fatalf("want [first second] in order, got %v", order)
	}
}

// ── Scenario 5: declared ErrorPattern mapping ────────────────────────────

type notFoundResponse struct {
	Code string
}

// AssertErrorPatternMapping proves a declared [file.ErrorPatterns] is
// AUTOMATICALLY consulted on a Backend error — mirrors
// [github.com/DaniDeer/go-codex/api/rest/resttest.AssertErrorMappingRoute].
func AssertErrorPatternMapping(t *testing.T, backend file.Backend) {
	t.Helper()
	client, dir := newClient(t, backend)
	res := file.NewResource[record](filepath.Join(dir, "{id}.json"), format.JSON(recordCodec()), file.PathParam{Name: "id"})
	ctx := context.Background()

	pattern := file.NewMappedErrorPattern(func(e file.FileNotFoundError) (notFoundResponse, error) {
		return notFoundResponse{Code: "NOT_FOUND"}, nil
	})
	patterns := file.Append(file.ErrorPatterns[notFoundResponse]{}, pattern)
	reader := res.WithRead(file.Read{}).ErrorPatterns(patterns)

	_, err := client.Read(ctx, reader, map[string]string{"id": "ghost"})
	var matched file.ErrorPatternMatchedError
	if !errors.As(err, &matched) {
		t.Fatalf("want ErrorPatternMatchedError, got %v (%T)", err, err)
	}
	resp, ok := matched.Payload.(notFoundResponse)
	if !ok || resp.Code != "NOT_FOUND" {
		t.Fatalf("want matched Payload = notFoundResponse{Code: NOT_FOUND}, got %+v", matched.Payload)
	}
}

// ── Scenario 6: capability-requirement rejection ────────────────────────

// AssertCapabilityRejected proves err is a [file.CapabilityCoverageError]
// — the construction/attach-time rejection every declared
// [file.CapabilityRequirement] without a matching supplied [file.Capability]
// must produce. Mirrors
// [github.com/DaniDeer/go-codex/api/rest/resttest.AssertCapabilityRejected]'s
// identical role (a plain error-shape assertion, no Backend/handler
// involved — the rejection happens before either would ever run).
func AssertCapabilityRejected(t *testing.T, err error) {
	t.Helper()
	var covErr *file.CapabilityCoverageError
	if !errors.As(err, &covErr) {
		t.Fatalf("want *file.CapabilityCoverageError, got %v (%T)", err, err)
	}
}

// ── Scenario 7: observability hook ──────────────────────────────────────

// SpyObserver is a minimal [stats.FileObserver] spy recording every
// RecordFileRead/RecordFileWrite call — mirrors
// [github.com/DaniDeer/go-codex/api/rest/resttest.SpyObserver]'s identical
// role, adapted to file's own Observer extension.
type SpyObserver struct {
	stats.NoopObserver
	Reads  []SpyCall
	Writes []SpyCall
}

// SpyCall is one recorded Record* call.
type SpyCall struct {
	Path    string
	Success bool
}

func (s *SpyObserver) RecordFileRead(path string, success bool, _ time.Duration) {
	s.Reads = append(s.Reads, SpyCall{Path: path, Success: success})
}

func (s *SpyObserver) RecordFileWrite(path string, success bool, _ time.Duration) {
	s.Writes = append(s.Writes, SpyCall{Path: path, Success: success})
}

var _ stats.FileObserver = (*SpyObserver)(nil)

// AssertObservability proves backend's dispatch calls
// [stats.FileObserver.RecordFileRead]/RecordFileWrite exactly once per
// operation, with the correct success flag — mirrors
// [github.com/DaniDeer/go-codex/api/rest/resttest.AssertObservabilityRoute].
func AssertObservability(t *testing.T, backend file.Backend, spy *SpyObserver) {
	t.Helper()
	client, dir := newClient(t, backend)
	res := file.NewResource[record](filepath.Join(dir, "{id}.json"), format.JSON(recordCodec()), file.PathParam{Name: "id"})
	ctx := stats.WithObserver(context.Background(), spy)
	vars := map[string]string{"id": "observed"}

	if err := client.Create(ctx, res.WithCreate(file.Create{}), record{ID: "observed", Value: 1}, vars); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(spy.Writes) != 1 || !spy.Writes[0].Success {
		t.Fatalf("want exactly 1 successful RecordFileWrite call, got %+v", spy.Writes)
	}
	if _, err := client.Read(ctx, res.WithRead(file.Read{}), vars); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(spy.Reads) != 1 || !spy.Reads[0].Success {
		t.Fatalf("want exactly 1 successful RecordFileRead call, got %+v", spy.Reads)
	}
}

// ── Scenario 8: BoundMiddleware metadata integration (Create) ──────────

type recordWithOwner struct {
	ID    string
	Value int
	Owner string
}

func recordWithOwnerCodec() codex.Codec[recordWithOwner] {
	return codex.Struct[recordWithOwner](
		codex.RequiredField("id", codex.String(),
			func(r recordWithOwner) string { return r.ID },
			func(r *recordWithOwner, v string) { r.ID = v }),
		codex.RequiredField("value", codex.Int(),
			func(r recordWithOwner) int { return r.Value },
			func(r *recordWithOwner, v int) { r.Value = v }),
	)
}

type metaIn struct{ Owner string }
type metaOut struct{ Owner string }

// AssertMetadataIntegration proves the file-metadata axis AND its
// BoundCreateMiddleware integration both work end-to-end against
// backend: a resource-level MergedMetadataParam persists/retrieves
// Owner automatically, and an attached BoundCreateMiddleware can
// override it via WithMetadataOut (a newer capability than the other 7
// scenarios, added once BoundMiddleware's metadata integration shipped).
func AssertMetadataIntegration(t *testing.T, backend file.Backend) {
	t.Helper()
	client, dir := newClient(t, backend)
	res := file.NewResource[recordWithOwner](filepath.Join(dir, "{id}.json"), format.JSON(recordWithOwnerCodec()),
		file.PathParam{Name: "id"},
		file.NewMetadataParam("owner", codex.String(),
			func(r recordWithOwner) string { return r.Owner },
			func(r *recordWithOwner, v string) { r.Owner = v }),
	)
	ctx := context.Background()

	t.Run("resource_level_metadata_round_trips", func(t *testing.T) {
		vars := map[string]string{"id": "plain"}
		if err := client.Create(ctx, res.WithCreate(file.Create{}), recordWithOwner{ID: "plain", Value: 1, Owner: "alice"}, vars); err != nil {
			t.Fatalf("Create: %v", err)
		}
		gotAny, err := client.Read(ctx, res.WithRead(file.Read{}), vars)
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		if got := gotAny.(recordWithOwner); got.Owner != "alice" {
			t.Fatalf("Owner = %q, want alice", got.Owner)
		}
	})

	t.Run("bound_middleware_metadata_out_overrides", func(t *testing.T) {
		bm := file.NewBoundCreateMiddleware[recordWithOwner, metaIn, metaOut](
			file.NewDeclaration[metaIn, metaOut]("owner-override", codex.Struct[metaIn](), codex.Struct[metaOut]()),
			func(ctx context.Context, v *recordWithOwner, in metaIn) (metaOut, error) {
				return metaOut{Owner: "overridden"}, nil
			},
		).WithMetadataOut(file.NewMetadataParam("owner", codex.String(),
			func(m metaOut) string { return m.Owner },
			func(m *metaOut, v string) { m.Owner = v }))
		vars := map[string]string{"id": "gated"}
		creator := res.WithCreate(file.Create{}).CreateBoundMW(bm)
		if err := client.Create(ctx, creator, recordWithOwner{ID: "gated", Value: 1, Owner: "original"}, vars); err != nil {
			t.Fatalf("Create: %v", err)
		}
		gotAny, err := client.Read(ctx, res.WithRead(file.Read{}), vars)
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		if got := gotAny.(recordWithOwner); got.Owner != "overridden" {
			t.Fatalf("Owner = %q, want overridden (BoundCreateMiddleware's WithMetadataOut should win)", got.Owner)
		}
	})
}
