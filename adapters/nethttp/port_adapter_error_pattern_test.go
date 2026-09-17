package nethttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/ports"
	"github.com/DaniDeer/go-codex/stats"
)

// This file tests H1's fix (session review round-4 finding):
// handlerFunc — the dispatch behind port/stream bindings (IngestAdapter,
// LatestAdapter, HandlerLatest, PipelineHandler) — is a SEPARATE
// dispatch function from serve.go's fully Category-A-wired one, and
// previously never consulted a declared rest.ErrorPattern (nor reported
// ErrorPatternObserver/SpanTagger observability) at ANY failure point
// except the handler's own business error (and even that used the bare,
// non-observability-aware ErrorResponseFor). This verifies the fix
// closes that gap for IngestAdapter specifically (representative of the
// same handlerFunc-backed surface HandlerLatest/PipelineHandler/
// LatestAdapter also share).

type portErrPayload struct {
	Code string `json:"code"`
}

var portErrPayloadCodec = codex.Struct[portErrPayload](
	codex.RequiredField("code", codex.String(),
		func(e portErrPayload) string { return e.Code },
		func(e *portErrPayload, v string) { e.Code = v },
	),
)

type portErrorPatternObserverSpy struct {
	stats.NoopObserver
	matches int
}

func (s *portErrorPatternObserverSpy) RecordErrorPatternMatch(_, _, _ string) {
	s.matches++
}

func (s *portErrorPatternObserverSpy) RecordErrorPatternMiss(_ string) {}

func TestIngestAdapter_ErrorPattern_MatchedOnDecodeFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	b := rest.NewServer(testInfo)
	handle, err := rest.NewRoute[createReq, struct{}]("POST", "/ingest-error-pattern",
		createReqCodec, codex.Struct[struct{}](),
		rest.RouteMeta{OperationID: "ingestErrorPattern"},
		rest.ErrorPattern[codex.ValidationErrors, portErrPayload](http.StatusUnprocessableEntity, portErrPayloadCodec,
			func(e codex.ValidationErrors) (portErrPayload, error) {
				return portErrPayload{Code: "bad_request"}, nil
			},
		),
	).RegisterHandle(b)
	if err != nil {
		t.Fatalf("RegisterHandle: %v", err)
	}

	mux := http.NewServeMux()
	spy := &portErrorPatternObserverSpy{}

	p, err := ports.NewSourcePort[createReq]("ingest-error-pattern", createReqCodec, ports.PortOptions{Buffer: 4})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	p.Bind(ctx, IngestAdapter(mux, handle, IngestAdapterOptions{Buffer: 4}))
	s := p.Stream(ctx)
	_ = s

	withObs := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(stats.WithObserver(r.Context(), spy))
		mux.ServeHTTP(w, r)
	})
	srv := httptest.NewServer(withObs)
	defer srv.Close()

	time.Sleep(20 * time.Millisecond)

	// Missing required "name" field -> codex.ValidationErrors.
	resp, err := http.Post(srv.URL+"/ingest-error-pattern", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("want 422 (declared ErrorPattern status), got %d", resp.StatusCode)
	}
	buf := make([]byte, 256)
	n, _ := resp.Body.Read(buf)
	if !strings.Contains(string(buf[:n]), `"code":"bad_request"`) {
		t.Errorf("want typed payload with code=bad_request, got: %s", buf[:n])
	}
	if spy.matches != 1 {
		t.Errorf("want 1 RecordErrorPatternMatch call (ObserveErrorResponseFor wiring), got %d", spy.matches)
	}
}

func TestIngestAdapter_ErrorPattern_NoMatch_FallsBackUnchanged(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	handle := newIngestRoute(t)

	mux := http.NewServeMux()
	p, err := ports.NewSourcePort[createReq]("ingest-no-pattern", createReqCodec, ports.PortOptions{Buffer: 4})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	p.Bind(ctx, IngestAdapter(mux, handle, IngestAdapterOptions{Buffer: 4}))
	s := p.Stream(ctx)
	_ = s

	srv := httptest.NewServer(mux)
	defer srv.Close()

	time.Sleep(20 * time.Millisecond)

	resp, err := http.Post(srv.URL+"/ingest", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("want unchanged 400 fallback for a route with no declared ErrorPattern, got %d", resp.StatusCode)
	}
}
