package reqreply_test

import (
	"context"
	"errors"
	"testing"

	"github.com/DaniDeer/go-codex/api/reqreply"
)

// ── Phase 5a: ServeWithTransport/CallWithTransport ────────────────────────────

// mockServerTransport is a minimal reqreply.ServerTransport for testing
// ServeWithTransport's dispatch.
type mockServerTransport struct {
	called bool
	route  any
	fn     any
	err    error
}

func (m *mockServerTransport) Serve(_ context.Context, route, fn any) error {
	m.called = true
	m.route = route
	m.fn = fn
	return m.err
}

var _ reqreply.ServerTransport = (*mockServerTransport)(nil)

// mockClientTransport is a minimal reqreply.ClientTransport for testing
// CallWithTransport's dispatch.
type mockClientTransport struct {
	called bool
	route  any
	req    any
	resp   any
	err    error
}

func (m *mockClientTransport) Call(_ context.Context, route, req any, _ ...reqreply.ClientCallOptions) (any, error) {
	m.called = true
	m.route = route
	m.req = req
	return m.resp, m.err
}

func (m *mockClientTransport) CallAsync(_ context.Context, route, req any, _ ...reqreply.ClientCallOptions) (any, error) {
	return nil, nil
}

var _ reqreply.ClientTransport = (*mockClientTransport)(nil)

func TestServeWithTransport_DelegatesToTransport(t *testing.T) {
	handle := computeRoute.ClientHandle()
	mt := &mockServerTransport{}
	fn := func(_ context.Context, r computeReq) (computeResp, error) { return computeResp{Sum: r.X + r.Y}, nil }

	if err := reqreply.ServeWithTransport(context.Background(), mt, handle, fn); err != nil {
		t.Fatalf("ServeWithTransport: %v", err)
	}
	if !mt.called {
		t.Fatal("expected ServerTransport.Serve to be called")
	}
	if mt.route != handle {
		t.Errorf("route = %+v, want the same *RouteHandle passed in", mt.route)
	}
}

func TestServeWithTransport_TransportError_Propagates(t *testing.T) {
	handle := computeRoute.ClientHandle()
	wantErr := errors.New("boom")
	mt := &mockServerTransport{err: wantErr}
	fn := func(_ context.Context, r computeReq) (computeResp, error) { return computeResp{}, nil }

	err := reqreply.ServeWithTransport(context.Background(), mt, handle, fn)
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
}

func TestCallWithTransport_DelegatesToTransport(t *testing.T) {
	handle := computeRoute.ClientHandle()
	want := computeResp{Sum: 12}
	mt := &mockClientTransport{resp: want}

	got, err := reqreply.CallWithTransport(context.Background(), mt, handle, computeReq{X: 5, Y: 7})
	if err != nil {
		t.Fatalf("CallWithTransport: %v", err)
	}
	if !mt.called {
		t.Fatal("expected ClientTransport.Call to be called")
	}
	if got != want {
		t.Errorf("got = %+v, want %+v", got, want)
	}
	if mt.route != handle {
		t.Errorf("route = %+v, want the same *RouteHandle passed in", mt.route)
	}
}

func TestCallWithTransport_TransportError_Propagates(t *testing.T) {
	handle := computeRoute.ClientHandle()
	wantErr := errors.New("boom")
	mt := &mockClientTransport{err: wantErr}

	_, err := reqreply.CallWithTransport(context.Background(), mt, handle, computeReq{X: 1, Y: 2})
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
}

func TestCallWithTransport_RespTypeMismatch_ReturnsTransportTypeMismatchError(t *testing.T) {
	handle := computeRoute.ClientHandle()
	mt := &mockClientTransport{resp: "not-a-computeResp"}

	_, err := reqreply.CallWithTransport(context.Background(), mt, handle, computeReq{X: 1, Y: 2})
	var mismatch reqreply.TransportTypeMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("err = %v, want TransportTypeMismatchError", err)
	}
	if mismatch.Topic != handle.Topic {
		t.Errorf("Topic = %q, want %q", mismatch.Topic, handle.Topic)
	}
}
