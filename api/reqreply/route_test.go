package reqreply_test

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	asyncapiv3 "github.com/DaniDeer/go-codex/render/asyncapi/v3"
	"github.com/DaniDeer/go-codex/validate"
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

var computeRouteWithErrorReply = reqreply.NewRoute[computeReq, computeResp](
	"compute/add", reqCodec, respCodec,
	reqreply.RouteMeta{OperationID: "computeAdd", Summary: "Add two integers."},
	reqreply.ErrorReplyMeta{
		Code:        "conflict",
		Description: "Business conflict error reply.",
		Schema:      codex.String().Schema,
		SchemaName:  "ConflictError",
	},
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

func TestRoute_AsyncAPISpec_ErrorReplyChannelAndOperation(t *testing.T) {
	b := newBuilder()
	_, _ = computeRouteWithErrorReply.Register(b)
	out := mustSpec(t, b)
	if !strings.Contains(out, "computeAddReplyErrorConflict:") {
		t.Errorf("want error reply channel key in spec:\n%s", out)
	}
	if !strings.Contains(out, "address: compute/add/reply/error/conflict") {
		t.Errorf("want error reply address in spec:\n%s", out)
	}
	if !strings.Contains(out, "receiveComputeAddReplyErrorConflict:") {
		t.Errorf("want error reply operation id in spec:\n%s", out)
	}
	if !strings.Contains(out, "ConflictError:") {
		t.Errorf("want error reply schema registered in components:\n%s", out)
	}
}

func TestRoute_AsyncAPISpec_ErrorReplyCustomOperationAndAddress(t *testing.T) {
	customErrRoute := reqreply.NewRoute[computeReq, computeResp](
		"compute/add", reqCodec, respCodec,
		reqreply.RouteMeta{OperationID: "computeAdd"},
		reqreply.ErrorReplyMeta{
			Code:           "validation",
			Schema:         codex.String().Schema,
			OperationID:    "receiveValidationErrorReply",
			ChannelAddress: "compute/add/reply/validation",
		},
	)
	b := newBuilder()
	_, _ = customErrRoute.Register(b)
	out := mustSpec(t, b)
	if !strings.Contains(out, "receiveValidationErrorReply:") {
		t.Errorf("want custom error operation id in spec:\n%s", out)
	}
	if !strings.Contains(out, "address: compute/add/reply/validation") {
		t.Errorf("want custom error reply address in spec:\n%s", out)
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

// ── ClientHandle tests ────────────────────────────────────────────────────────

func TestRoute_ClientHandle_returnsNonNilHandle(t *testing.T) {
	h := computeRoute.ClientHandle()
	if h == nil {
		t.Fatal("ClientHandle returned nil")
	}
}

func TestRoute_ClientHandle_topicMatches(t *testing.T) {
	h := computeRoute.ClientHandle()
	if h.Topic != "compute/add" {
		t.Errorf("expected topic %q, got %q", "compute/add", h.Topic)
	}
}

func TestRoute_ClientHandle_encodeDecodeRoundTrip(t *testing.T) {
	h := computeRoute.ClientHandle()

	// EncodeRequest + Decode (server path)
	payload, err := h.EncodeRequest(computeReq{X: 3, Y: 4})
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}
	decoded, err := h.Decode(payload)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if decoded.X != 3 || decoded.Y != 4 {
		t.Errorf("round-trip mismatch: got %+v", decoded)
	}

	// Encode + DecodeResponse (client path)
	respPayload, err := h.Encode(computeResp{Sum: 7})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	resp, err := h.DecodeResponse(respPayload)
	if err != nil {
		t.Fatalf("DecodeResponse: %v", err)
	}
	if resp.Sum != 7 {
		t.Errorf("response round-trip: got sum=%d, want 7", resp.Sum)
	}
}

func TestRoute_ClientHandle_noBuilderRequired(t *testing.T) {
	// ClientHandle must not panic and must produce a usable handle
	// even when no Builder is created.
	h := computeRoute.ClientHandle()
	if h.Decode == nil || h.EncodeRequest == nil {
		t.Fatal("ClientHandle fields must not be nil")
	}
}

func TestRoute_ClientHandle_topicParamsPreserved(t *testing.T) {
	uuidCodec := codex.String()
	templateRoute := reqreply.NewRoute[computeReq, computeResp](
		"compute/{tenantID}/add", reqCodec, respCodec,
		reqreply.TopicParam{Name: "tenantID"}.WithCodec(uuidCodec),
	)
	h := templateRoute.ClientHandle()
	topic, err := h.BuildTopic(map[string]string{"tenantID": "acme"})
	if err != nil {
		t.Fatalf("BuildTopic: %v", err)
	}
	if topic != "compute/acme/add" {
		t.Errorf("expected %q, got %q", "compute/acme/add", topic)
	}
}

// ── AppendTo ──────────────────────────────────────────────────────────────────

func TestBuilder_AppendTo_writesChannelsToExternalBuilder(t *testing.T) {
	b := newBuilder()
	if _, err := computeRoute.Register(b); err != nil {
		t.Fatalf("Register: %v", err)
	}

	db := asyncapiv3.NewDocumentBuilder(asyncapiv3.Info{Title: "Test", Version: "1.0.0"})
	if err := b.AppendTo(db); err != nil {
		t.Fatalf("AppendTo: %v", err)
	}
	doc, err := db.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	out, _ := doc.MarshalYAML()
	for _, want := range []string{"computeAdd:", "computeAddReply:"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("spec missing %q\nfull output:\n%s", want, string(out))
		}
	}
}

func TestBuilder_AppendTo_AsyncAPISpec_consistent(t *testing.T) {
	// AppendTo then Build must produce the same channels as AsyncAPISpec.
	b := newBuilder()
	if _, err := computeRoute.Register(b); err != nil {
		t.Fatalf("Register: %v", err)
	}

	specDirect, err := b.AsyncAPISpec()
	if err != nil {
		t.Fatalf("AsyncAPISpec: %v", err)
	}

	db := asyncapiv3.NewDocumentBuilder(asyncapiv3.Info{Title: "Compute API", Version: "1.0.0"})
	db.AddServer("zmq", asyncapiv3.Server{URL: "tcp://localhost:5556", Protocol: "zmq"})
	if err := b.AppendTo(db); err != nil {
		t.Fatalf("AppendTo: %v", err)
	}
	specCombined, err := db.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	direct, _ := specDirect.MarshalYAML()
	combined, _ := specCombined.MarshalYAML()
	if string(direct) != string(combined) {
		t.Errorf("channels differ\ndirect:\n%s\ncombined:\n%s", direct, combined)
	}
}

// ── RequestFormats / Formats RouteOpt ─────────────────────────────────────────

var reqreplyPngCodec = codex.Bytes().Refine(validate.PNG)

// TestRequestFormats_AppliesInline verifies reqreply.RequestFormats declared
// inline in NewRoute's opts is equivalent to calling WithRequestFormats
// after Register.
func TestRequestFormats_AppliesInline(t *testing.T) {
	b := reqreply.NewBuilder(reqreply.Info{})
	route := reqreply.NewRoute[[]byte, computeResp]("images/upload", reqreplyPngCodec, respCodec,
		reqreply.RequestFormats(format.Binary(reqreplyPngCodec).WithContentType("image/png")),
	)
	handle, err := route.Register(b)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if len(handle.RequestFormats) != 1 || handle.RequestFormats[0].ContentType() != "image/png" {
		t.Errorf("want 1 RequestFormats entry with image/png, got %+v", handle.RequestFormats)
	}
}

// TestFormats_AppliesInline verifies reqreply.Formats declared inline is
// equivalent to calling WithFormats after Register.
func TestFormats_AppliesInline(t *testing.T) {
	b := reqreply.NewBuilder(reqreply.Info{})
	route := reqreply.NewRoute[computeReq, []byte]("images/download", reqCodec, reqreplyPngCodec,
		reqreply.Formats(format.Binary(reqreplyPngCodec).WithContentType("image/png")),
	)
	handle, err := route.Register(b)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if len(handle.Formats) != 1 || handle.Formats[0].ContentType() != "image/png" {
		t.Errorf("want 1 Formats entry with image/png, got %+v", handle.Formats)
	}
}

// TestRequestFormats_TypeMismatch verifies a wrong-typed RequestFormats
// option returns FormatOptError, reachable via errors.As, with a
// structured LogValue.
func TestRequestFormats_TypeMismatch(t *testing.T) {
	b := reqreply.NewBuilder(reqreply.Info{})
	route := reqreply.NewRoute[computeReq, computeResp]("compute/add", reqCodec, respCodec,
		reqreply.RequestFormats(format.Binary(reqreplyPngCodec)),
	)
	_, err := route.Register(b)
	var fe reqreply.FormatOptError
	if !errors.As(err, &fe) || fe.Direction != "request" {
		t.Fatalf("want FormatOptError{request}, got %v", err)
	}
	if fe.Unwrap() == nil {
		t.Error("want non-nil Unwrap")
	}
	v := fe.LogValue()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("want KindGroup, got %v", v.Kind())
	}
	keys := map[string]bool{}
	for _, a := range v.Group() {
		keys[a.Key] = true
	}
	for _, want := range []string{"direction", "err"} {
		if !keys[want] {
			t.Errorf("missing LogValue key %q", want)
		}
	}
}

// TestFormats_TypeMismatch mirrors TestRequestFormats_TypeMismatch for the
// response direction.
func TestFormats_TypeMismatch(t *testing.T) {
	b := reqreply.NewBuilder(reqreply.Info{})
	route := reqreply.NewRoute[computeReq, computeResp]("compute/sub", reqCodec, respCodec,
		reqreply.Formats(format.Binary(reqreplyPngCodec)),
	)
	_, err := route.Register(b)
	var fe reqreply.FormatOptError
	if !errors.As(err, &fe) || fe.Direction != "response" {
		t.Fatalf("want FormatOptError{response}, got %v", err)
	}
}

// ── Phase 2: reqreply.NewTopicParam / RouteHandle.DecodeMerged ────────────

type tenantComputeReq struct {
	TenantID string
	X, Y     int
}

var tenantComputeReqCodec = codex.Struct[tenantComputeReq](
	codex.RequiredField("x", codex.Int(),
		func(r tenantComputeReq) int { return r.X },
		func(r *tenantComputeReq, v int) { r.X = v }),
	codex.RequiredField("y", codex.Int(),
		func(r tenantComputeReq) int { return r.Y },
		func(r *tenantComputeReq, v int) { r.Y = v }),
)

// RR1: reqreply.NewTopicParam registers both spec TopicParam and merge field.
func TestNewTopicParam_RegistersSpecAndMergeField(t *testing.T) {
	b := reqreply.NewBuilder(reqreply.Info{})
	h, err := reqreply.NewRoute[tenantComputeReq, computeResp]("compute/{tenantID}/add",
		tenantComputeReqCodec, respCodec,
		reqreply.NewTopicParam("tenantID", codex.String().Refine(validate.NonEmptyString),
			func(r tenantComputeReq) string { return r.TenantID },
			func(r *tenantComputeReq, v string) { r.TenantID = v }),
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if len(h.MergeFields()) != 1 {
		t.Fatalf("MergeFields: want 1, got %d", len(h.MergeFields()))
	}
}

// RR2: RouteHandle.DecodeMerged happy path — payload decoded AND topic var
// merged into the SAME Req.
func TestDecodeMerged_HappyPath(t *testing.T) {
	b := reqreply.NewBuilder(reqreply.Info{})
	h, err := reqreply.NewRoute[tenantComputeReq, computeResp]("compute/{tenantID}/add",
		tenantComputeReqCodec, respCodec,
		reqreply.NewTopicParam("tenantID", codex.String().Refine(validate.NonEmptyString),
			func(r tenantComputeReq) string { return r.TenantID },
			func(r *tenantComputeReq, v string) { r.TenantID = v }),
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	req, err := h.DecodeMerged([]byte(`{"x":1,"y":2}`), map[string]string{"tenantID": "acme"})
	if err != nil {
		t.Fatalf("DecodeMerged: %v", err)
	}
	if req.TenantID != "acme" || req.X != 1 || req.Y != 2 {
		t.Errorf("unexpected merged req: %+v", req)
	}
}

// RR3: RouteHandle.DecodeMerged with zero merge fields behaves like plain
// Decode (regression guard).
func TestDecodeMerged_NoMergeFieldsIsNoop(t *testing.T) {
	b := reqreply.NewBuilder(reqreply.Info{})
	h, err := reqreply.NewRoute[computeReq, computeResp]("compute/add", reqCodec, respCodec).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	body := []byte(`{"x":1,"y":2}`)
	viaDecode, err := h.Decode(body)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	viaMerged, err := h.DecodeMerged(body, nil)
	if err != nil {
		t.Fatalf("DecodeMerged: %v", err)
	}
	if viaDecode != viaMerged {
		t.Errorf("DecodeMerged should match plain Decode when no merge fields declared: %+v vs %+v", viaDecode, viaMerged)
	}
}
