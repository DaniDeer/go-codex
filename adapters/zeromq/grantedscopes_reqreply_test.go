package zeromq

import (
	"context"
	"testing"

	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/internal/route"
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

// Phase C's BoundMiddleware embeds fn at construction, so ONE
// HandleBoundMW call does both — no separate .Use(mw) needed, and the
// FORMER DuplicateMiddlewareNameError workaround (declaring
// RouteMeta.Security directly, skipping `.Use(mw)`) is no longer
// required: applyBoundRoute synthesizes the legacy Security entry into
// rb.middlewares itself, which applySecurityDeclarations merges into
// rb.meta.Security automatically.
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
			mw := reqreply.BoundSecurityMiddleware[securedComputeReq, gsReqreplyIn, gsReqreplyOut]("bearer", gsReqreplyBearerScheme, []string{"compute:write"},
				func(_ context.Context, req *securedComputeReq, _ gsReqreplyIn) (gsReqreplyOut, error) {
					return gsReqreplyOut{GrantedScopes: tc.grant}, nil
				})
			handlerCalled := false
			fn := func(_ context.Context, r securedComputeReq) (securedComputeResp, error) {
				handlerCalled = true
				return securedComputeResp{Sum: r.X + r.Y}, nil
			}

			route := reqreply.NewRoute[securedComputeReq, securedComputeResp](
				"/secured-compute-gs",
				securedComputeReqCodec, securedComputeRespCodec,
				reqreply.RouteMeta{OperationID: "securedComputeGS"},
			).HandleBoundMW(mw).WithHandler(fn)

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
