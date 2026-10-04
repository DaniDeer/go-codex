package mqtt5

import (
	"context"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/route"
)

// docs/design/d-0007-declarative-middleware-layering.md's Rollout Phase C:
// mqtt5's own end-to-end proof of reqreply's GrantedScopes merge-and-
// enforce wiring (pc-13) — mirrors adapters/zeromq/
// grantedscopes_reqreply_test.go's identical structure/intent, closing
// the asymmetric test-coverage gap a Phase C re-review found (zeromq had
// this proof, mqtt5's reqreply wiring — while implemented and covered by
// its own pre-existing regression tests — had no dedicated end-to-end
// GrantedScopes test of its own). mqtt5's legacy security mechanism
// genuinely produces real grants (unlike zeromq's pure binary accept/
// reject), so its CheckScopes gate is the simpler, unconditional
// `len(secReqs) > 0` — this test proves the UNIFIED merge still rejects
// a wrong/missing bound grant correctly, not just that it doesn't panic.

type gsReqreplyIn struct{ Token string }
type gsReqreplyOut struct {
	GrantedScopes map[string][]string
}

var gsReqreplyBearerScheme = reqreply.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")}

// NOTE — the SAME confirmed, pre-existing cross-package
// `.Use(mw).HandleMW(&mw, fn)` DuplicateMiddlewareNameError gap
// documented in adapters/zeromq/grantedscopes_reqreply_test.go applies
// here too (api/rest has the identical structure; not reqreply- or
// adapter-specific). WORKAROUND used below, identical to zeromq's: declare
// RouteMeta.Security directly, skip `.Use(mw)` — sufficient for
// CheckCoverage/CheckScopes correctness, which is all this test needs.
func TestAttachServer_HandleMW_GrantedScopes_MergedIntoCheckScopes(t *testing.T) {
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
			handler := func(_ context.Context, req computeReq) (computeResp, error) {
				handlerCalled = true
				return computeResp{Sum: req.X + req.Y}, nil
			}

			rt := reqreply.NewRoute[computeReq, computeResp](
				"compute/gs-mqtt5",
				computeReqCodec, computeRespCodec,
				reqreply.RouteMeta{
					OperationID: "computeGSMqtt5",
					Security:    []route.SecurityRequirement{route.Require("bearer", "compute:write")},
				},
			).HandleMW(&mw,
				func(_ context.Context, req *computeReq, _ gsReqreplyIn) (gsReqreplyOut, error) {
					return gsReqreplyOut{GrantedScopes: tc.grant}, nil
				},
			).WithHandler(handler)

			server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
			if _, err := rt.Register(server); err != nil {
				t.Fatalf("Register: %v", err)
			}
			serverClient := &mockClient{}
			serverRouter := newMockRouter()
			if err := server.Attach(NewServerTransport(ServerTransportOptions{Client: serverClient, Router: serverRouter})); err != nil {
				t.Fatalf("AttachServer: %v", err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
			defer cancel()
			errCh := make(chan error, 1)
			go func() { errCh <- server.Serve(ctx) }()
			serverRouter.waitHandler("compute/gs-mqtt5")

			client := reqreply.NewClient()
			clientClient := &mockClient{}
			clientRouter := newMockRouter()
			if err := client.Attach(NewClientTransport(ClientTransportOptions{Client: clientClient, Router: clientRouter})); err != nil {
				t.Fatalf("AttachClient: %v", err)
			}
			wireBrokers(t, serverClient, clientRouter)
			wireBrokers(t, clientClient, serverRouter)

			callRoute := reqreply.NewRoute[computeReq, computeResp]("compute/gs-mqtt5", computeReqCodec, computeRespCodec)
			_, err := client.Call(context.Background(), callRoute, computeReq{X: 3, Y: 4})
			gotHandled := err == nil
			if gotHandled != tc.wantHandled {
				t.Errorf("Call err = %v (handled=%v), want handled=%v (grant %v)", err, gotHandled, tc.wantHandled, tc.grant)
			}
			if handlerCalled != tc.wantHandled {
				t.Errorf("handlerCalled = %v, want %v", handlerCalled, tc.wantHandled)
			}
			cancel()
			<-errCh
		})
	}
}
