package zeromq

import (
	"context"
	"testing"

	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/route"
)

// docs/design/d-0007-declarative-middleware-layering.md's Rollout Phase C:
// proves reqreply's OWN GrantedScopes merge-and-enforce wiring (pc-13) —
// a bound HandleMW Security middleware granting a REAL scope lets a
// scope-requiring route dispatch succeed; granting the WRONG scope (or
// none) gets rejected by CheckScopes, not silently ignored. zeromq is the
// riskiest adapter to prove this on (its legacy Fn shape has NO grants
// concept at all — see scopesmerge.HasSatisfyingHandler's own gating
// rationale), making this the most valuable regression target.

type gsReqreplyIn struct{ Token string }
type gsReqreplyOut struct {
	Sum           int
	GrantedScopes map[string][]string
}

var gsReqreplyBearerScheme = reqreply.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")}

// NOTE — a confirmed, PRE-EXISTING cross-package gap found while writing
// this test, NOT fixed here (out of this phase's scope, affects REST
// too): the roadmap doc's own worked examples show
// `.Use(mw).HandleMW(&mw, fn)` for a bound Security middleware, but this
// combination FAILS with DuplicateMiddlewareNameError when mw carries
// ONLY a Security declaration (no merge fields) — both `.Use(mw)`
// (applyAgnosticRoute) and the bound `.HandleMW(&mw, fn)` path
// (applyBoundRoute) unconditionally append a middlewareSpecContribution
// under the SAME mw.Name, and checkMiddlewareNameUniquenessAndAttachment
// treats this as a genuine duplicate. Confirmed via code that
// `api/rest`'s identical checkMiddlewareNameUniquenessAndAttachment has
// the SAME structure — this is NOT reqreply-specific. events does NOT
// have this issue (it bundles spec metadata directly on the handler, no
// separate spec-contribution list). WORKAROUND used below: declare
// RouteMeta.Security directly (skip `.Use(mw)` entirely) — sufficient
// for CheckCoverage/CheckScopes correctness, though it means
// AsyncAPISpec() won't auto-register mw's securitySchemes entry this
// way (a separate, spec-rendering-only concern, irrelevant to THIS
// test). Flagged for separate follow-up resolution.

func TestHandleMW_GrantedScopes_MergedIntoCheckScopes(t *testing.T) {
	for _, tc := range []struct {
		name        string
		grant       map[string][]string
		wantHandled bool
	}{
		{"correct scope granted", map[string][]string{"bearer": {"compute:write"}}, true},
		{"wrong scope granted", map[string][]string{"bearer": {"compute:read"}}, false},
		{"no scopes granted", map[string][]string{"bearer": {}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mw := reqreply.SecurityMiddleware[gsReqreplyIn, gsReqreplyOut]("bearer", gsReqreplyBearerScheme, []string{"compute:write"})
			handlerCalled := false
			fn := func(_ context.Context, r securedComputeReq) (securedComputeResp, error) {
				handlerCalled = true
				return securedComputeResp{Sum: r.X + r.Y}, nil
			}

			route := reqreply.NewRoute[securedComputeReq, securedComputeResp](
				"/secured-compute-gs",
				securedComputeReqCodec, securedComputeRespCodec,
				reqreply.RouteMeta{
					OperationID: "securedComputeGS",
					Security:    []route.SecurityRequirement{route.Require("bearer", "compute:write")},
				},
			).HandleMW(&mw,
				func(_ context.Context, req *securedComputeReq, _ gsReqreplyIn) (gsReqreplyOut, error) {
					return gsReqreplyOut{GrantedScopes: tc.grant}, nil
				},
			).WithHandler(fn)

			server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
			if _, err := route.Register(server); err != nil {
				t.Fatalf("Register: %v", err)
			}

			repSock, reqSock := newChanSocketPair()
			if err := server.Attach(NewServerTransport(ServerTransportOptions{Sockets: map[string]FramedSocket{"/secured-compute-gs": repSock}})); err != nil {
				t.Fatalf("AttachServer: %v", err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			serveErrCh := make(chan error, 1)
			go func() { serveErrCh <- server.Serve(ctx) }()

			if err := reqSock.SendFrames([][]byte{[]byte(`{"x":3,"y":4}`)}); err != nil {
				t.Fatalf("send: %v", err)
			}
			frames, err := reqSock.RecvFrames()
			if err != nil {
				t.Fatalf("recv: %v", err)
			}
			gotOK := string(frames[0]) == "ok"
			if gotOK != tc.wantHandled {
				t.Errorf("status = %q (ok=%v), want handled=%v (grant %v)", frames[0], gotOK, tc.wantHandled, tc.grant)
			}
			if handlerCalled != tc.wantHandled {
				t.Errorf("handlerCalled = %v, want %v", handlerCalled, tc.wantHandled)
			}
		})
	}
}
