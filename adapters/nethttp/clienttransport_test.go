package nethttp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DaniDeer/go-codex/api/rest"
)

// ── rest.Client.Call via Attach (Decision 5 / transport-agnostic-serve-interface) ──────────

func TestAttach_ClientCall_RoundTrip(t *testing.T) {
	s := rest.NewServer(testInfo)
	route := rest.NewRoute[createReq, userResp]("POST", "/users",
		createReqCodec, userRespCodec, rest.RouteMeta{OperationID: "createUser"},
	).WithHandler(func(ctx context.Context, req createReq) (userResp, error) {
		return userResp{ID: "1", Name: req.Name}, nil
	})
	if err := route.Register(s); err != nil {
		t.Fatalf("Register: %v", err)
	}

	mux := http.NewServeMux()
	if err := serve(mux, s); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := rest.NewClient()
	if err := Attach(client, srv.Client(), srv.URL); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	respAny, err := client.Call(context.Background(), route, createReq{Name: "Alice"})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	resp, ok := respAny.(userResp)
	if !ok {
		t.Fatalf("Call returned %T, want userResp", respAny)
	}
	if resp.Name != "Alice" || resp.ID != "1" {
		t.Errorf("unexpected response: %+v", resp)
	}
}

func TestAttach_SecondCall_ReturnsClientTransportAlreadyAttachedError(t *testing.T) {
	client := rest.NewClient()
	if err := Attach(client, http.DefaultClient, "http://example.com"); err != nil {
		t.Fatalf("first Attach: %v", err)
	}
	err := Attach(client, http.DefaultClient, "http://example.com")
	var alreadyErr rest.ClientTransportAlreadyAttachedError
	if !errors.As(err, &alreadyErr) {
		t.Fatalf("want ClientTransportAlreadyAttachedError, got %v (%T)", err, err)
	}
}

func TestClientCall_NoTransportAttached_ReturnsNoClientTransportAttachedError(t *testing.T) {
	client := rest.NewClient()
	route := rest.NewRoute[createReq, userResp]("POST", "/users",
		createReqCodec, userRespCodec, rest.RouteMeta{OperationID: "createUser"},
	)
	_, err := client.Call(context.Background(), route, createReq{})
	var noTransportErr rest.NoClientTransportAttachedError
	if !errors.As(err, &noTransportErr) {
		t.Fatalf("want NoClientTransportAttachedError, got %v (%T)", err, err)
	}
}

func TestAttach_ClientCall_WrongRouteType_ReturnsTransportTypeMismatchError(t *testing.T) {
	client := rest.NewClient()
	if err := Attach(client, http.DefaultClient, "http://example.com"); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	_, err := client.Call(context.Background(), "not-a-route", createReq{})
	var mismatchErr rest.TransportTypeMismatchError
	if !errors.As(err, &mismatchErr) {
		t.Fatalf("want TransportTypeMismatchError, got %v (%T)", err, err)
	}
}

func TestAttach_ClientCall_WrongReqType_ReturnsTransportTypeMismatchError(t *testing.T) {
	route := rest.NewRoute[createReq, userResp]("POST", "/users",
		createReqCodec, userRespCodec, rest.RouteMeta{OperationID: "createUser"},
	)
	client := rest.NewClient()
	if err := Attach(client, http.DefaultClient, "http://example.com"); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	_, err := client.Call(context.Background(), route, "wrong-type")
	var mismatchErr rest.TransportTypeMismatchError
	if !errors.As(err, &mismatchErr) {
		t.Fatalf("want TransportTypeMismatchError, got %v (%T)", err, err)
	}
}

func TestAttach_ClientCall_NonSuccessStatus_ReturnsUnexpectedStatusError(t *testing.T) {
	s := rest.NewServer(testInfo)
	route := rest.NewRoute[createReq, userResp]("POST", "/users",
		createReqCodec, userRespCodec, rest.RouteMeta{OperationID: "createUser"},
	).WithHandler(func(ctx context.Context, req createReq) (userResp, error) {
		return userResp{}, errors.New("boom")
	})
	if err := route.Register(s); err != nil {
		t.Fatalf("Register: %v", err)
	}

	mux := http.NewServeMux()
	if err := serve(mux, s); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := rest.NewClient()
	if err := Attach(client, srv.Client(), srv.URL); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	_, err := client.Call(context.Background(), route, createReq{Name: "Alice"})
	var statusErr UnexpectedStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("want UnexpectedStatusError, got %v (%T)", err, err)
	}
}
