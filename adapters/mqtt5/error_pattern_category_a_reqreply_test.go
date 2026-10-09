package mqtt5

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/internal/middleware"
	"github.com/DaniDeer/go-codex/internal/route"
	"github.com/DaniDeer/go-codex/validate"
	pahomqtt5 "github.com/eclipse/paho.golang/paho"
)

// This file tests Topic 1's Category A full enumeration fix for reqreply
// (see docs/design/d-0005-error-handling.md): the
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
	if err := server.Attach(NewServerTransport(ServerTransportOptions{Client: serverClient, Router: serverRouter})); err != nil {
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
	// NO Codec attached to this scheme (unlike bearerSecBoundMw's shared
	// bearerAuthTestCodec) — deliberately, so the built-in codec-based
	// pre-check (which runs BEFORE any paired Fn) stays a no-op here and
	// this test exercises ONLY the paired Fn's OWN rejection.
	rejectingImpl := func(context.Context, *computeReq, mwSecIn) (mwSecOut, error) {
		return mwSecOut{}, errSecurityRejected
	}
	rejectingMw := reqreply.BoundSecurityMiddleware[computeReq, mwSecIn, mwSecOut](
		"bearer", reqreply.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")}, nil, rejectingImpl,
	)
	epRoute := reqreply.NewRoute[computeReq, computeResp]("compute/security-mw-ep", computeReqCodec, computeRespCodec,
		reqreply.RouteMeta{OperationID: "computeSecurityMw", Security: []route.SecurityRequirement{route.Require("bearer")}},
		reqreply.ErrorPattern[reqreply.SecurityError, serveErrPayload](serveErrPayloadCodec,
			func(e reqreply.SecurityError) (serveErrPayload, error) {
				return serveErrPayload{Code: "security_rejected", Message: e.Error()}, nil
			},
		),
	).HandleBoundMW(rejectingMw)
	if _, err := epRoute.WithHandler(handler).Register(server); err != nil {
		t.Fatalf("Register: %v", err)
	}

	serverClient := &mockClient{}
	serverRouter := newMockRouter()
	if err := server.Attach(NewServerTransport(ServerTransportOptions{Client: serverClient, Router: serverRouter})); err != nil {
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

// TestErrorPattern_BoundSecurityMiddlewareFn_Matched_Publishes_ReqReply
// covers the Middleware-dispatched (`.Use()`-attached, reusable class)
// Security Fn failure case — distinct from
// TestErrorPattern_SecurityMiddlewareFn_Matched_Publishes_ReqReply above,
// which exercises the BOUND class's own Security Fn failure path
// (HandleBoundMW-attached, per docs/roadmap/retire-legacy-security-
// middleware.md — the test's own fn was migrated from the now-rejected
// legacy raw-adapter-Fn-pairing path, HandleMW(&mw, rawFn), to
// HandleBoundMW(bearerSecBoundMw(fn)), with NO change to the behavior
// under test). This test closes the
// blind spot that let a confirmed cross-pattern inconsistency (reqreply
// wrapping a Security-carrying Middleware Fn's failure as the GENERIC
// reqreply.MiddlewareError, rather than reqreply.SecurityError like
// REST's own `isSecuritySatisfyingHandler`-gated behavior) go unnoticed
// across 3 prior Phase C review rounds — see this session's 4th Phase C
// review round for the full writeup. Also asserts the previously-missing
// stats.SecurityObserver.RecordSecurityRejection call now fires for this
// specific failure mode.
func TestErrorPattern_BoundSecurityMiddlewareFn_Matched_Publishes_ReqReply(t *testing.T) {
	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	handler := func(_ context.Context, _ computeReq) (computeResp, error) {
		return computeResp{}, nil
	}
	rejectingMw := reqreply.SecurityMiddleware[struct{}, struct{}]("bearer3",
		reqreply.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")}, nil,
	).WithReceive(func(context.Context, struct{}) (struct{}, error) {
		return struct{}{}, errSecurityRejected
	})
	epRoute := reqreply.NewRoute[computeReq, computeResp]("compute/bound-security-mw-ep", computeReqCodec, computeRespCodec,
		reqreply.RouteMeta{OperationID: "computeBoundSecurityMw", Security: []route.SecurityRequirement{route.Require("bearer3")}},
		reqreply.ErrorPattern[reqreply.SecurityError, serveErrPayload](serveErrPayloadCodec,
			func(e reqreply.SecurityError) (serveErrPayload, error) {
				return serveErrPayload{Code: "security_rejected", Message: e.Error()}, nil
			},
		),
	).Use(rejectingMw)
	if _, err := epRoute.WithHandler(handler).Register(server); err != nil {
		t.Fatalf("Register: %v", err)
	}

	serverClient := &mockClient{}
	serverRouter := newMockRouter()
	obs := &testObserver{}
	if err := server.Attach(NewServerTransport(ServerTransportOptions{Client: serverClient, Router: serverRouter, Serve: ServeOptions{Observer: obs}})); err != nil {
		t.Fatalf("AttachServer: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	go func() { _ = server.Serve(ctx) }()
	serverRouter.waitHandler("compute/bound-security-mw-ep")

	serverRouter.dispatch("compute/bound-security-mw-ep", &pahomqtt5.Publish{
		Topic:   "compute/bound-security-mw-ep",
		Payload: []byte(validComputeJSON),
		Properties: &pahomqtt5.PublishProperties{
			ResponseTopic:   "replies/client-1",
			CorrelationData: []byte("corr-bound-security"),
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
	if len(obs.secRejections) != 1 {
		t.Errorf("want 1 RecordSecurityRejection call, got %d", len(obs.secRejections))
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
	mw := reqreply.NewBoundMiddleware[computeReq](middleware.NewDeclaration("tenant-policy", mwDecodeFailInCodec, mwPropOutCodec),
		func(ctx context.Context, req *computeReq, in mwDecodeFailIn) (mwPropOut, error) {
			return mwPropOut{}, nil
		})
	handler := func(_ context.Context, _ computeReq) (computeResp, error) {
		return computeResp{}, nil
	}
	rt := reqreply.NewRoute[computeReq, computeResp]("compute/mw-decode-in-ep", computeReqCodec, computeRespCodec,
		reqreply.ErrorPattern[reqreply.MiddlewareInputError, serveErrPayload](serveErrPayloadCodec,
			func(e reqreply.MiddlewareInputError) (serveErrPayload, error) {
				return serveErrPayload{Code: "middleware_input", Message: e.Error()}, nil
			},
		),
	).HandleBoundMW(mw)

	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	if _, err := rt.WithHandler(handler).Register(server); err != nil {
		t.Fatalf("Register: %v", err)
	}
	serverClient := &mockClient{}
	serverRouter := newMockRouter()
	if err := server.Attach(NewServerTransport(ServerTransportOptions{Client: serverClient, Router: serverRouter})); err != nil {
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
	if err := server.Attach(NewServerTransport(ServerTransportOptions{Client: serverClient, Router: serverRouter, Serve: ServeOptions{
		UserPropertyParams: []UserPropertyParam{
			UserPropertyParam{Name: "TenantID", Required: true}.WithCodec(
				codex.String().Refine(validate.NonEmptyString),
			),
		},
	}})); err != nil {
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

// TestErrorPattern_SecurityCredentialFormat_Matched_Publishes is a
// REGRESSION GUARD: the built-in codec-based credential FORMAT check
// (validateSecurityCredentials, gating on SecurityScheme.Codec) was the
// ONLY Category-A failure branch in this file's dispatch loop that used
// the plain, non-reflect publishErrorReply (bypassing
// ObserveErrorResponseFor entirely) AND never consulted DeadLetter —
// every OTHER failure branch (decode, SecurityMiddlewareFn,
// BoundSecurityMiddlewareFn, MiddlewareDecodeIn, UserPropertyParam) is
// already wired to both. A malformed credential (failing
// SecurityScheme.Codec.Validate) should be just as ErrorPattern/
// DeadLetter-eligible as any other Category-A failure.
func TestErrorPattern_SecurityCredentialFormat_Matched_Publishes(t *testing.T) {
	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	handler := func(_ context.Context, _ computeReq) (computeResp, error) {
		return computeResp{}, nil
	}
	bearerScheme := reqreply.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")}.
		WithCodec(codex.String().Refine(validate.NonEmptyString))
	// An ALWAYS-ACCEPTING paired implementation satisfies the Register/
	// Serve-time coverage check (a declared Security scheme with no
	// attached middleware is a hard Serve-time error) — this test
	// exercises ONLY the built-in Codec-based credential FORMAT check,
	// which runs BEFORE this paired Fn ever gets a chance to run.
	acceptingImpl := func(context.Context, *computeReq, mwSecIn) (mwSecOut, error) {
		return mwSecOut{GrantedScopes: map[string][]string{"bearerAuth": nil}}, nil
	}
	acceptingMw := reqreply.BoundSecurityMiddleware[computeReq, mwSecIn, mwSecOut](
		"bearerAuth", bearerScheme, nil, acceptingImpl,
	)
	epRoute := reqreply.NewRoute[computeReq, computeResp]("compute/cred-format-ep", computeReqCodec, computeRespCodec,
		reqreply.RouteMeta{Security: []route.SecurityRequirement{route.Require("bearerAuth")}},
		reqreply.ErrorPattern[reqreply.SecurityCredentialError, serveErrPayload](serveErrPayloadCodec,
			func(e reqreply.SecurityCredentialError) (serveErrPayload, error) {
				return serveErrPayload{Code: "bad_credential", Message: e.Error()}, nil
			},
		),
	).HandleBoundMW(acceptingMw)
	if _, err := epRoute.WithHandler(handler).Register(server); err != nil {
		t.Fatalf("Register: %v", err)
	}

	serverClient := &mockClient{}
	serverRouter := newMockRouter()
	if err := server.Attach(NewServerTransport(ServerTransportOptions{Client: serverClient, Router: serverRouter})); err != nil {
		t.Fatalf("AttachServer: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	go func() { _ = server.Serve(ctx) }()
	serverRouter.waitHandler("compute/cred-format-ep")

	serverRouter.dispatch("compute/cred-format-ep", &pahomqtt5.Publish{
		Topic:   "compute/cred-format-ep",
		Payload: []byte(`{"x":1,"y":2}`),
		Properties: &pahomqtt5.PublishProperties{
			ResponseTopic:   "replies/client-1",
			CorrelationData: []byte("corr-credformat"),
			User:            pahomqtt5.UserProperties{{Key: "Authorization", Value: "Bearer "}}, // empty token fails NonEmptyString
		},
	})
	time.Sleep(50 * time.Millisecond)

	pub := serverClient.lastPublished()
	if pub == nil {
		t.Fatal("expected reply to be published")
	}
	if !strings.Contains(string(pub.Payload), `"code":"bad_credential"`) {
		t.Errorf("want typed payload with code=bad_credential (ErrorPattern-matched), got plain-text reply: %s", pub.Payload)
	}
}
