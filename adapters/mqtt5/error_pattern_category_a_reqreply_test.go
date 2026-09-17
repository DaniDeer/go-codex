package mqtt5

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/validate"
	pahomqtt5 "github.com/eclipse/paho.golang/paho"
)

// This file tests Topic 1's Category A full enumeration fix for reqreply
// (see docs/roadmap/error-handling-rest-events-reqreply.md): the
// server-side failure points beyond handler/middleware-Fn errors
// (already covered elsewhere) are now reqreply.ErrorPattern-eligible
// too — mirrors mqtt5's own equivalent events test file.

func TestErrorPattern_RequestPayloadDecode_Matched_Publishes(t *testing.T) {
	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	handler := func(_ context.Context, _ computeReq) (computeResp, error) {
		return computeResp{}, nil
	}
	epRoute := reqreply.NewRoute[computeReq, computeResp]("compute/payload-decode-ep", computeReqCodec, computeRespCodec,
		reqreply.ErrorPattern[codex.ValidationErrors, serveErrPayload](serveErrPayloadCodec,
			func(e codex.ValidationErrors) (serveErrPayload, error) {
				return serveErrPayload{Code: "decode_error", Message: "bad payload"}, nil
			},
		),
	)
	if _, err := epRoute.WithHandler(handler).Register(server); err != nil {
		t.Fatalf("Register: %v", err)
	}

	serverClient := &mockClient{}
	serverRouter := newMockRouter()
	if err := AttachServer(server, serverClient, serverRouter); err != nil {
		t.Fatalf("AttachServer: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	go func() { _ = server.Serve(ctx) }()
	serverRouter.waitHandler("compute/payload-decode-ep")

	serverRouter.dispatch("compute/payload-decode-ep", &pahomqtt5.Publish{
		Topic:   "compute/payload-decode-ep",
		Payload: []byte(`{}`), // missing required fields -> codex.ValidationErrors
		Properties: &pahomqtt5.PublishProperties{
			ResponseTopic:   "replies/client-1",
			CorrelationData: []byte("corr-decode"),
		},
	})
	time.Sleep(50 * time.Millisecond)

	pub := serverClient.lastPublished()
	if pub == nil {
		t.Fatal("expected reply to be published")
	}
	if !strings.Contains(string(pub.Payload), `"code":"decode_error"`) {
		t.Errorf("want typed payload with code=decode_error, got: %s", pub.Payload)
	}
}

func TestErrorPattern_SecurityMiddlewareFn_Matched_Publishes_ReqReply(t *testing.T) {
	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	handler := func(_ context.Context, _ computeReq) (computeResp, error) {
		return computeResp{}, nil
	}
	bearerMw := middleware.SecurityScheme("bearer", route.BearerScheme("JWT"), nil, nil)
	rejectingImpl := func(context.Context, *pahomqtt5.Publish, []route.SecurityRequirement) (map[string][]string, error) {
		return nil, errSecurityRejected
	}
	epRoute := reqreply.NewRoute[computeReq, computeResp]("compute/security-mw-ep", computeReqCodec, computeRespCodec,
		reqreply.RouteMeta{OperationID: "computeSecurityMw", Security: []route.SecurityRequirement{route.Require("bearer")}},
		reqreply.ErrorPattern[reqreply.SecurityError, serveErrPayload](serveErrPayloadCodec,
			func(e reqreply.SecurityError) (serveErrPayload, error) {
				return serveErrPayload{Code: "security_rejected", Message: e.Error()}, nil
			},
		),
	).Use(bearerMw).HandleMW(&bearerMw, rejectingImpl)
	if _, err := epRoute.WithHandler(handler).Register(server); err != nil {
		t.Fatalf("Register: %v", err)
	}

	serverClient := &mockClient{}
	serverRouter := newMockRouter()
	if err := AttachServer(server, serverClient, serverRouter); err != nil {
		t.Fatalf("AttachServer: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	go func() { _ = server.Serve(ctx) }()
	serverRouter.waitHandler("compute/security-mw-ep")

	serverRouter.dispatch("compute/security-mw-ep", &pahomqtt5.Publish{
		Topic:   "compute/security-mw-ep",
		Payload: []byte(validComputeJSON),
		Properties: &pahomqtt5.PublishProperties{
			ResponseTopic:   "replies/client-1",
			CorrelationData: []byte("corr-security"),
		},
	})
	time.Sleep(50 * time.Millisecond)

	pub := serverClient.lastPublished()
	if pub == nil {
		t.Fatal("expected reply to be published")
	}
	if !strings.Contains(string(pub.Payload), `"code":"security_rejected"`) {
		t.Errorf("want typed payload with code=security_rejected, got: %s", pub.Payload)
	}
}

type mwDecodeFailIn struct{ TenantID string }

var mwDecodeFailInCodec = codex.Struct[mwDecodeFailIn](
	codex.RequiredField("tenant_id", codex.String().Refine(validate.NonEmptyString),
		func(in mwDecodeFailIn) string { return in.TenantID },
		func(in *mwDecodeFailIn, v string) { in.TenantID = v },
	),
)

func TestErrorPattern_MiddlewareDecodeIn_Matched_Publishes_ReqReply(t *testing.T) {
	// mwDecodeFailIn has a REQUIRED field with no topic/property/header
	// source attached — DecodeIn always sees the zero value, which
	// always fails validation, isolating a genuine middleware DecodeIn
	// failure (mirrors events' newDecodeInFailingMiddleware).
	mw := reqreply.NewMiddleware(middleware.NewDeclaration("tenant-policy", mwDecodeFailInCodec, mwPropOutCodec))
	handler := func(_ context.Context, _ computeReq) (computeResp, error) {
		return computeResp{}, nil
	}
	rt := reqreply.Transform(
		reqreply.NewRoute[computeReq, computeResp]("compute/mw-decode-in-ep", computeReqCodec, computeRespCodec,
			reqreply.ErrorPattern[reqreply.MiddlewareInputError, serveErrPayload](serveErrPayloadCodec,
				func(e reqreply.MiddlewareInputError) (serveErrPayload, error) {
					return serveErrPayload{Code: "middleware_input", Message: e.Error()}, nil
				},
			),
		),
		mw,
		func(ctx context.Context, req *computeReq, in mwDecodeFailIn) (mwPropOut, error) {
			return mwPropOut{}, nil
		},
	)

	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	if _, err := rt.WithHandler(handler).Register(server); err != nil {
		t.Fatalf("Register: %v", err)
	}
	serverClient := &mockClient{}
	serverRouter := newMockRouter()
	if err := AttachServer(server, serverClient, serverRouter); err != nil {
		t.Fatalf("AttachServer: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	go func() { _ = server.Serve(ctx) }()
	serverRouter.waitHandler("compute/mw-decode-in-ep")

	serverRouter.dispatch("compute/mw-decode-in-ep", &pahomqtt5.Publish{
		Topic:   "compute/mw-decode-in-ep",
		Payload: []byte(validComputeJSON),
		Properties: &pahomqtt5.PublishProperties{
			ResponseTopic:   "replies/client-1",
			CorrelationData: []byte("corr-mw-decode-in"),
		},
	})
	time.Sleep(50 * time.Millisecond)

	pub := serverClient.lastPublished()
	if pub == nil {
		t.Fatal("expected reply to be published")
	}
	if !strings.Contains(string(pub.Payload), `"code":"middleware_input"`) {
		t.Errorf("want typed payload with code=middleware_input, got: %s", pub.Payload)
	}
}

// TestErrorPattern_UserPropertyParam_Matched_Publishes tests F3's fix
// (session review finding): reqreply's server-side User Property param
// validation failures are now ErrorPattern/DeadLetter-eligible, closing
// an asymmetry with REST's own wired header-param validation.
func TestErrorPattern_UserPropertyParam_Matched_Publishes(t *testing.T) {
	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	handler := func(_ context.Context, _ computeReq) (computeResp, error) {
		return computeResp{}, nil
	}
	epRoute := reqreply.NewRoute[computeReq, computeResp]("compute/user-property-ep", computeReqCodec, computeRespCodec,
		reqreply.ErrorPattern[MissingUserPropertyError, serveErrPayload](serveErrPayloadCodec,
			func(e MissingUserPropertyError) (serveErrPayload, error) {
				return serveErrPayload{Code: "missing_user_property", Message: e.Error()}, nil
			},
		),
	)
	if _, err := epRoute.WithHandler(handler).Register(server); err != nil {
		t.Fatalf("Register: %v", err)
	}

	serverClient := &mockClient{}
	serverRouter := newMockRouter()
	if err := AttachServer(server, serverClient, serverRouter, ServeOptions{
		UserPropertyParams: []UserPropertyParam{
			UserPropertyParam{Name: "TenantID", Required: true}.WithCodec(
				codex.String().Refine(validate.NonEmptyString),
			),
		},
	}); err != nil {
		t.Fatalf("AttachServer: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	go func() { _ = server.Serve(ctx) }()
	serverRouter.waitHandler("compute/user-property-ep")

	serverRouter.dispatch("compute/user-property-ep", &pahomqtt5.Publish{
		Topic:   "compute/user-property-ep",
		Payload: []byte(`{"x":1,"y":2}`),
		Properties: &pahomqtt5.PublishProperties{
			ResponseTopic:   "replies/client-1",
			CorrelationData: []byte("corr-userprop"),
			// TenantID deliberately absent.
		},
	})
	time.Sleep(50 * time.Millisecond)

	pub := serverClient.lastPublished()
	if pub == nil {
		t.Fatal("expected reply to be published")
	}
	if !strings.Contains(string(pub.Payload), `"code":"missing_user_property"`) {
		t.Errorf("want typed payload with code=missing_user_property, got: %s", pub.Payload)
	}
}
