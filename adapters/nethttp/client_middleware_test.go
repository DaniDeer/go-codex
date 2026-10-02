package nethttp

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
)

// CallWithHandle-specific ClientTransform tests (HappyPath/FnError/
// AgnosticMiddleware/D3Precedence — all 4 REMOVED, docs/design/
// d-0006-protocol-native-capabilities.md's Phase 5a): CallWithHandle's
// OWN body was, at the time, the ONLY reachable place on the plain-Call
// (non-SSE) path that ever dispatched rest.ClientTransform/bundled
// `.Use()` codec-backed middleware. This gap (neither
// [rest.CallWithTransport] nor [clientTransport.Call]/[clientTransport.Consume]
// dispatched handle.ClientMiddlewareHandlers) was closed in
// docs/roadmap/declarative-middleware-layering.md's Rollout Phase A —
// see [TestAttach_ClientCall_CodecBackedClientMW_EncodesInAndDecodesOut]/
// [TestAttach_ClientConsume_CodecBackedClientMW_EncodesInAndDecodesOut]
// (clienttransport_test.go) for the current, passing coverage.
// [TestConsumeSSE_ClientTransformSSE_EncodesInAndDecodesOutOnce] below is
// UNCHANGED — it exercises consumeSSE directly, a separate,
// still-fully-capable escape-hatch primitive this phase does not touch.

// ── ClientTransformSSE: encodes In into the connect request, decodes Out
// ONCE at connection-open time ──

func TestConsumeSSE_ClientTransformSSE_EncodesInAndDecodesOutOnce(t *testing.T) {
	mw := rest.NewMiddleware(newTDDeclaration("api-key-policy")).
		WithRequestHeader(rest.NewRequiredHeaderParam("X-Api-Key", codex.String(),
			func(in tdIn) string { return in.Key },
			func(in *tdIn, v string) { in.Key = v },
		)).
		WithResponseHeader(rest.NewRequiredResponseHeaderParam("X-Policy-Version", codex.String(),
			func(out tdOut) string { return out.Value },
			func(out *tdOut, v string) { out.Value = v },
		))

	route := rest.NewSSERoute[sseTestReq, userResp]("/stream/{id}", sseTestReqCodec, userRespCodec,
		rest.NewPathParam("id", codex.String(),
			func(r sseTestReq) string { return r.ID },
			func(r *sseTestReq, v string) { r.ID = v }),
	)
	route = route.ClientMW(mw, func(ctx context.Context, req sseTestReq) (tdIn, error) {
		return tdIn{Key: "secret-" + req.ID}, nil
	})
	handle := route.ClientHandle()

	var gotHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-Api-Key")
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("X-Policy-Version", "v1")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "data: {\"id\":\"1\",\"name\":\"Alice\"}\n\n")
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ctx = WithClientMiddlewareOut(ctx)

	done := make(chan struct{})
	_ = consumeSSE(ctx, srv.Client(), srv.URL, handle, sseTestReq{ID: "machine-1"},
		func(_ context.Context, e userResp) error {
			close(done)
			cancel()
			return nil
		}, ConsumeOptions{})

	select {
	case <-done:
	default:
		t.Fatal("want fn to be called with the decoded event")
	}
	if gotHeader != "secret-machine-1" {
		t.Errorf("want X-Api-Key %q sent, got %q", "secret-machine-1", gotHeader)
	}

	outs := ClientMiddlewareOutFromContext(ctx)
	out, ok := outs["api-key-policy"].(tdOut)
	if !ok {
		t.Fatalf("want decoded tdOut for api-key-policy, got %+v", outs)
	}
	if out.Value != "v1" {
		t.Errorf("want decoded Out.Value %q, got %q", "v1", out.Value)
	}
}
