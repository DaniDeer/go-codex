package reqreply_test

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/DaniDeer/go-codex/api/reqreply"
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

var computeRoute = reqreply.NewRoute[computeReq, computeResp](
	"compute/add", reqCodec, respCodec,
	reqreply.RouteMeta{OperationID: "computeAdd", Summary: "Add two integers."},
)

// ── helpers ───────────────────────────────────────────────────────────────────

func newBuilder() *reqreply.Builder {
	b := reqreply.NewBuilder(reqreply.Info{Title: "Compute API", Version: "1.0.0"})
	b.AddServer("zmq", reqreply.Server{URL: "tcp://localhost:5556", Protocol: "zmq"})
	return b
}

func mustSpec(t *testing.T, b *reqreply.Builder) string {
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

// ── NewRoute and Register tests ───────────────────────────────────────────────

func TestNewRoute_Infallible(t *testing.T) {
	// NewRoute must never panic — validation is deferred to Register.
	_ = reqreply.NewRoute[computeReq, computeResp](
		"", reqCodec, respCodec, // empty topic is allowed at declaration time
	)
}

func TestRoute_Register_ReturnsHandle(t *testing.T) {
	b := newBuilder()
	handle, err := computeRoute.Register(b)
	if err != nil {
		t.Fatalf("Register() error: %v", err)
	}
	if handle == nil {
		t.Fatal("handle must not be nil")
	}
	if handle.Topic != "compute/add" {
		t.Fatalf("expected Topic='compute/add', got %q", handle.Topic)
	}
	if handle.Decode == nil || handle.Encode == nil ||
		handle.EncodeRequest == nil || handle.DecodeResponse == nil {
		t.Fatal("handle codec functions must not be nil")
	}
}

func TestRoute_Register_DuplicateTopicError(t *testing.T) {
	b := newBuilder()
	_, _ = computeRoute.Register(b)

	_, err := computeRoute.Register(b) // same topic again
	var dup reqreply.DuplicateRouteError
	if !errors.As(err, &dup) {
		t.Fatalf("expected DuplicateRouteError, got %T: %v", err, err)
	}
	if dup.Topic != "compute/add" {
		t.Fatalf("expected Topic='compute/add', got %q", dup.Topic)
	}
}

// ── RouteHandle codec round-trip tests ───────────────────────────────────────

func TestRouteHandle_DecodeRoundTrip(t *testing.T) {
	b := newBuilder()
	handle, _ := computeRoute.Register(b)

	payload, err := handle.EncodeRequest(computeReq{X: 3, Y: 4})
	if err != nil {
		t.Fatalf("EncodeRequest error: %v", err)
	}
	got, err := handle.Decode(payload)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	if got.X != 3 || got.Y != 4 {
		t.Fatalf("expected {3,4}, got %+v", got)
	}
}

func TestRouteHandle_EncodeDecodeResponse(t *testing.T) {
	b := newBuilder()
	handle, _ := computeRoute.Register(b)

	payload, err := handle.Encode(computeResp{Sum: 7})
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}
	got, err := handle.DecodeResponse(payload)
	if err != nil {
		t.Fatalf("DecodeResponse error: %v", err)
	}
	if got.Sum != 7 {
		t.Fatalf("expected Sum=7, got %d", got.Sum)
	}
}

func TestRouteHandle_WithRequestFormats_SetsField(t *testing.T) {
	b := newBuilder()
	handle, _ := computeRoute.Register(b)
	if len(handle.RequestFormats) != 0 {
		t.Fatal("RequestFormats must be empty before WithRequestFormats")
	}
	// WithRequestFormats with no args clears the slice (mirrors RouteHandle behaviour).
	h2 := handle.WithRequestFormats()
	if h2 != handle {
		t.Fatal("WithRequestFormats must return the same pointer")
	}
}

func TestRouteHandle_WithFormats_SetsField(t *testing.T) {
	b := newBuilder()
	handle, _ := computeRoute.Register(b)
	if len(handle.Formats) != 0 {
		t.Fatal("Formats must be empty before WithFormats")
	}
	h2 := handle.WithFormats()
	if h2 != handle {
		t.Fatal("WithFormats must return the same pointer")
	}
}

// ── AsyncAPI spec tests ───────────────────────────────────────────────────────

func TestRoute_AsyncAPISpec_RequestChannel(t *testing.T) {
	b := newBuilder()
	_, _ = computeRoute.Register(b)
	out := mustSpec(t, b)
	if !strings.Contains(out, "computeAdd:") {
		t.Errorf("want request channel key 'computeAdd:' in spec:\n%s", out)
	}
	if !strings.Contains(out, "address: compute/add") {
		t.Errorf("want address 'compute/add' in spec:\n%s", out)
	}
}

func TestRoute_AsyncAPISpec_ReplyChannel(t *testing.T) {
	b := newBuilder()
	_, _ = computeRoute.Register(b)
	out := mustSpec(t, b)
	if !strings.Contains(out, "computeAddReply:") {
		t.Errorf("want reply channel 'computeAddReply:' in spec:\n%s", out)
	}
	if !strings.Contains(out, "address: compute/add/reply") {
		t.Errorf("want reply address 'compute/add/reply' in spec:\n%s", out)
	}
}

func TestRoute_AsyncAPISpec_SendOpHasReplyBlock(t *testing.T) {
	b := newBuilder()
	_, _ = computeRoute.Register(b)
	out := mustSpec(t, b)
	if !strings.Contains(out, "reply:") {
		t.Errorf("want 'reply:' block in send operation:\n%s", out)
	}
	if !strings.Contains(out, "'#/channels/computeAddReply'") {
		t.Errorf("want $ref to computeAddReply in reply block:\n%s", out)
	}
}

func TestRoute_AsyncAPISpec_DefaultOperationID_DerivedFromTopic(t *testing.T) {
	noIDRoute := reqreply.NewRoute[computeReq, computeResp](
		"compute/add", reqCodec, respCodec,
		// No OperationID — derived from topic "compute/add"
	)
	b := newBuilder()
	_, _ = noIDRoute.Register(b)
	out := mustSpec(t, b)
	if !strings.Contains(out, "sendComputeAdd:") {
		t.Errorf("want sendComputeAdd derived from topic:\n%s", out)
	}
}

func TestRoute_AsyncAPISpec_ServerProtocol(t *testing.T) {
	b := newBuilder()
	_, _ = computeRoute.Register(b)
	out := mustSpec(t, b)
	if !strings.Contains(out, "protocol: zmq") {
		t.Errorf("want protocol: zmq in spec:\n%s", out)
	}
}

func TestRoute_AsyncAPISpec_MultipleRoutes(t *testing.T) {
	multiplyRoute := reqreply.NewRoute[computeReq, computeResp](
		"compute/multiply", reqCodec, respCodec,
		reqreply.RouteMeta{OperationID: "computeMultiply"},
	)
	b := newBuilder()
	_, _ = computeRoute.Register(b)
	_, _ = multiplyRoute.Register(b)
	out := mustSpec(t, b)
	if !strings.Contains(out, "computeAddReply:") {
		t.Errorf("want computeAddReply channel:\n%s", out)
	}
	if !strings.Contains(out, "computeMultiplyReply:") {
		t.Errorf("want computeMultiplyReply channel:\n%s", out)
	}
}

// ── DuplicateRouteError tests ─────────────────────────────────────────────────

func TestDuplicateRouteError_ErrorString(t *testing.T) {
	e := reqreply.DuplicateRouteError{Topic: "compute/add"}
	if !strings.Contains(e.Error(), "compute/add") {
		t.Fatalf("Error() must contain topic, got %q", e.Error())
	}
}

func TestDuplicateRouteError_LogValue(t *testing.T) {
	e := reqreply.DuplicateRouteError{Topic: "compute/add"}
	v := e.LogValue()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("expected Group log value, got %v", v.Kind())
	}
}

// ── TopicParam and BuildTopic tests ──────────────────────────────────────────

var templateRoute = reqreply.NewRoute[computeReq, computeResp](
	"compute/{tenantID}/add",
	reqCodec, respCodec,
	reqreply.RouteMeta{OperationID: "computeAdd"},
	reqreply.TopicParam{
		Name:        "tenantID",
		Description: "Tenant namespace.",
	}.WithCodec(codex.String().RefineFunc(func(v string) error {
		if v == "" {
			return fmt.Errorf("tenantID must not be empty")
		}
		return nil
	})),
)

func TestTopicParam_WithCodec_ReturnsCopy(t *testing.T) {
	p := reqreply.TopicParam{Name: "tenantID"}
	codec := codex.String()
	p2 := p.WithCodec(codec)
	if p.Codec != nil {
		t.Fatal("WithCodec must not mutate original")
	}
	if p2.Codec == nil {
		t.Fatal("WithCodec must set Codec on copy")
	}
}

func TestRouteHandle_BuildTopic_Happy(t *testing.T) {
	b := newBuilder()
	handle, _ := templateRoute.Register(b)

	topic, err := handle.BuildTopic(map[string]string{"tenantID": "acme"})
	if err != nil {
		t.Fatalf("BuildTopic error: %v", err)
	}
	if topic != "compute/acme/add" {
		t.Fatalf("expected 'compute/acme/add', got %q", topic)
	}
}

func TestRouteHandle_BuildTopic_MissingVar(t *testing.T) {
	b := newBuilder()
	handle, _ := templateRoute.Register(b)

	_, err := handle.BuildTopic(map[string]string{}) // tenantID missing
	var missing reqreply.MissingRouteParamError
	if !errors.As(err, &missing) {
		t.Fatalf("expected MissingRouteParamError, got %T: %v", err, err)
	}
	if missing.Name != "tenantID" {
		t.Fatalf("expected Name=tenantID, got %q", missing.Name)
	}
}

func TestRouteHandle_BuildTopic_CodecFailure(t *testing.T) {
	b := newBuilder()
	handle, _ := templateRoute.Register(b)

	_, err := handle.BuildTopic(map[string]string{"tenantID": ""}) // empty → codec fails
	var paramErr reqreply.RouteParamError
	if !errors.As(err, &paramErr) {
		t.Fatalf("expected RouteParamError, got %T: %v", err, err)
	}
	if paramErr.Name != "tenantID" {
		t.Fatalf("expected Name=tenantID, got %q", paramErr.Name)
	}
}

func TestRouteHandle_ValidateTopicVars_Happy(t *testing.T) {
	b := newBuilder()
	handle, _ := templateRoute.Register(b)
	if err := handle.ValidateTopicVars(map[string]string{"tenantID": "acme"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRouteHandle_ValidateTopicVars_Missing(t *testing.T) {
	b := newBuilder()
	handle, _ := templateRoute.Register(b)
	err := handle.ValidateTopicVars(map[string]string{})
	var missing reqreply.MissingRouteParamError
	if !errors.As(err, &missing) {
		t.Fatalf("expected MissingRouteParamError, got %T: %v", err, err)
	}
}

func TestRouteParamError_LogValue(t *testing.T) {
	e := reqreply.RouteParamError{Name: "tenantID", Value: "", Err: errors.New("must not be empty")}
	v := e.LogValue()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("expected Group, got %v", v.Kind())
	}
}

func TestMissingRouteParamError_LogValue(t *testing.T) {
	e := reqreply.MissingRouteParamError{Name: "tenantID"}
	v := e.LogValue()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("expected Group, got %v", v.Kind())
	}
}

func TestRouteParamError_ErrorsAs(t *testing.T) {
	inner := errors.New("constraint failed")
	outer := reqreply.RouteParamError{Name: "tenantID", Value: "x", Err: inner}
	if !errors.Is(outer, inner) {
		t.Fatal("errors.Is must traverse Unwrap")
	}
}
