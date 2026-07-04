package zeromq_test

import (
	"strings"
	"testing"

	"github.com/DaniDeer/go-codex/api/rest"
	zeromq "github.com/DaniDeer/go-codex/api/zeromq"
	"github.com/DaniDeer/go-codex/codex"
)

// ── shared types and codecs ───────────────────────────────────────────────────

type computeReq struct{ X, Y int }
type computeResp struct{ Sum int }

var reqCodec = codex.Struct[computeReq](
	codex.RequiredField("x", codex.Int(),
		func(r computeReq) int { return r.X },
		func(r *computeReq, v int) { r.X = v },
	),
	codex.RequiredField("y", codex.Int(),
		func(r computeReq) int { return r.Y },
		func(r *computeReq, v int) { r.Y = v },
	),
)

var respCodec = codex.Struct[computeResp](
	codex.RequiredField("sum", codex.Int(),
		func(r computeResp) int { return r.Sum },
		func(r *computeResp, v int) { r.Sum = v },
	),
)

var computeRoute = rest.NewRoute[computeReq, computeResp](
	"POST", "/compute",
	reqCodec, respCodec,
	rest.RouteMeta{OperationID: "compute", Summary: "Add two integers."},
)

// ── helpers ───────────────────────────────────────────────────────────────────

func newBuilder() *zeromq.Builder {
	b := zeromq.NewBuilder(zeromq.Info{Title: "Compute API", Version: "1.0.0"})
	b.AddServer("zmq", zeromq.Server{URL: "tcp://localhost:5556", Protocol: "zmq"})
	return b
}

func mustSpec(t *testing.T, b *zeromq.Builder) string {
	t.Helper()
	doc, err := b.AsyncAPISpec()
	if err != nil {
		t.Fatalf("AsyncAPISpec() error: %v", err)
	}
	yaml, err := doc.MarshalYAML()
	if err != nil {
		t.Fatalf("MarshalYAML() error: %v", err)
	}
	return string(yaml)
}

// ── tests ─────────────────────────────────────────────────────────────────────

func TestRegister_AsyncAPISpec_HasRequestChannel(t *testing.T) {
	b := newBuilder()
	_, err := zeromq.Register(b, computeRoute, zeromq.SocketMeta{OperationID: "compute"})
	if err != nil {
		t.Fatalf("Register error: %v", err)
	}
	out := mustSpec(t, b)
	if !strings.Contains(out, "compute:") {
		t.Errorf("want request channel 'compute:' in spec:\n%s", out)
	}
	if !strings.Contains(out, "address: /compute") {
		t.Errorf("want address '/compute' in spec:\n%s", out)
	}
}

func TestRegister_AsyncAPISpec_HasReplyChannel(t *testing.T) {
	b := newBuilder()
	_, _ = zeromq.Register(b, computeRoute, zeromq.SocketMeta{OperationID: "compute"})
	out := mustSpec(t, b)
	if !strings.Contains(out, "computeReply:") {
		t.Errorf("want reply channel 'computeReply:' in spec:\n%s", out)
	}
	if !strings.Contains(out, "address: /compute/reply") {
		t.Errorf("want reply address '/compute/reply' in spec:\n%s", out)
	}
}

func TestRegister_AsyncAPISpec_SendOpHasReply(t *testing.T) {
	b := newBuilder()
	_, _ = zeromq.Register(b, computeRoute, zeromq.SocketMeta{OperationID: "compute"})
	out := mustSpec(t, b)
	if !strings.Contains(out, "reply:") {
		t.Errorf("want 'reply:' block in send operation:\n%s", out)
	}
	if !strings.Contains(out, "'#/channels/computeReply'") {
		t.Errorf("want $ref to computeReply channel:\n%s", out)
	}
}

func TestRegister_AsyncAPISpec_ReceiveOp(t *testing.T) {
	b := newBuilder()
	_, _ = zeromq.Register(b, computeRoute, zeromq.SocketMeta{OperationID: "compute"})
	out := mustSpec(t, b)
	if !strings.Contains(out, "receiveComputeReply:") {
		t.Errorf("want receiveComputeReply operation in spec:\n%s", out)
	}
	if !strings.Contains(out, "action: receive") {
		t.Errorf("want action: receive in spec:\n%s", out)
	}
}

func TestRegister_AsyncAPISpec_CustomOperationID(t *testing.T) {
	b := newBuilder()
	_, _ = zeromq.Register(b, computeRoute, zeromq.SocketMeta{OperationID: "addIntegers"})
	out := mustSpec(t, b)
	if !strings.Contains(out, "sendAddIntegers:") {
		t.Errorf("want sendAddIntegers operation in spec:\n%s", out)
	}
	if !strings.Contains(out, "receiveAddIntegersReply:") {
		t.Errorf("want receiveAddIntegersReply operation in spec:\n%s", out)
	}
}

func TestRegister_AsyncAPISpec_DefaultOperationID_DerivedFromPath(t *testing.T) {
	b := newBuilder()
	_, _ = zeromq.Register(b, computeRoute, zeromq.SocketMeta{}) // empty OperationID
	out := mustSpec(t, b)
	// path "/compute" → base "compute" → "sendCompute" / "receiveComputeReply"
	if !strings.Contains(out, "sendCompute:") {
		t.Errorf("want sendCompute operation derived from path:\n%s", out)
	}
}

func TestRegister_AsyncAPISpec_ServerProtocol(t *testing.T) {
	b := newBuilder()
	_, _ = zeromq.Register(b, computeRoute, zeromq.SocketMeta{OperationID: "compute"})
	out := mustSpec(t, b)
	if !strings.Contains(out, "protocol: zmq") {
		t.Errorf("want protocol: zmq in spec:\n%s", out)
	}
}

func TestRegister_ReturnsUsableRouteHandle(t *testing.T) {
	b := newBuilder()
	handle, err := zeromq.Register(b, computeRoute, zeromq.SocketMeta{OperationID: "compute"})
	if err != nil {
		t.Fatalf("Register error: %v", err)
	}
	if handle == nil {
		t.Fatal("handle must not be nil")
	}
	// Decode a valid request payload.
	req, err := handle.Decode([]byte(`{"x":3,"y":4}`))
	if err != nil {
		t.Fatalf("handle.Decode error: %v", err)
	}
	if req.X != 3 || req.Y != 4 {
		t.Fatalf("unexpected decoded req: %+v", req)
	}
	// Encode a valid response.
	payload, err := handle.Encode(computeResp{Sum: 7})
	if err != nil {
		t.Fatalf("handle.Encode error: %v", err)
	}
	if !strings.Contains(string(payload), "7") {
		t.Fatalf("unexpected encoded payload: %s", payload)
	}
}

func TestRegister_MultipleRoutes(t *testing.T) {
	multiplyRoute := rest.NewRoute[computeReq, computeResp](
		"POST", "/multiply",
		reqCodec, respCodec,
		rest.RouteMeta{OperationID: "multiply"},
	)
	b := newBuilder()
	_, _ = zeromq.Register(b, computeRoute, zeromq.SocketMeta{OperationID: "compute"})
	_, _ = zeromq.Register(b, multiplyRoute, zeromq.SocketMeta{OperationID: "multiply"})
	out := mustSpec(t, b)
	if !strings.Contains(out, "computeReply:") {
		t.Errorf("want computeReply channel:\n%s", out)
	}
	if !strings.Contains(out, "multiplyReply:") {
		t.Errorf("want multiplyReply channel:\n%s", out)
	}
	if !strings.Contains(out, "sendCompute:") {
		t.Errorf("want sendCompute operation:\n%s", out)
	}
	if !strings.Contains(out, "sendMultiply:") {
		t.Errorf("want sendMultiply operation:\n%s", out)
	}
}
