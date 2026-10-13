package reqreply_test

import (
	"errors"
	"testing"

	"github.com/DaniDeer/go-codex/api/reqreply"
)

// ── Forward()'s own fixtures — a SECOND route the first can forward to,
// one plain and one {var}-templated, exercising both the happy path and
// ForwardTargetVarError. Reuses computeReq/computeResp/reqCodec/respCodec
// from route_test.go — no new wire types needed.

var forwardTargetRoute = reqreply.NewRoute[computeReq, computeResp](
	"compute/sum-forward-target", reqCodec, respCodec,
	reqreply.RouteMeta{OperationID: "computeSumForwardTarget"},
)

var forwardTemplateTargetRoute = reqreply.NewRoute[computeReq, computeResp](
	"compute/{tenantID}/forward-target",
	reqCodec, respCodec,
	reqreply.RouteMeta{OperationID: "computeTemplateForwardTarget"},
	reqreply.TopicParam{Name: "tenantID"},
)

func TestForward_HappyPath_ReturnsForwardErrorWithResolvedTopicAndEncodedPayload(t *testing.T) {
	err := reqreply.Forward(forwardTargetRoute, computeReq{X: 3, Y: 4}, nil, nil)
	if err == nil {
		t.Fatal("Forward: want a non-nil ForwardError, got nil")
	}
	var fwd reqreply.ForwardError
	if !errors.As(err, &fwd) {
		t.Fatalf("Forward error: want ForwardError, got %v (%T)", err, err)
	}
	if fwd.TargetTopic != "compute/sum-forward-target" {
		t.Errorf("ForwardError.TargetTopic = %q, want %q", fwd.TargetTopic, "compute/sum-forward-target")
	}
	if len(fwd.Payload) == 0 {
		t.Error("ForwardError.Payload: want non-empty encoded computeReq")
	}

	// Payload decodes back to the SAME request the handler constructed —
	// confirms Forward used the target's OWN request codec, not the
	// originating route's.
	handle := forwardTargetRoute.ClientHandle()
	decoded, decodeErr := handle.Decode(fwd.Payload)
	if decodeErr != nil {
		t.Fatalf("DecodeRequest(fwd.Payload): %v", decodeErr)
	}
	if decoded != (computeReq{X: 3, Y: 4}) {
		t.Errorf("decoded payload = %+v, want {X:3 Y:4}", decoded)
	}
}

// TestForward_WithProperties_ReturnsForwardErrorWithProperties is Phase
// 1.5's own wire-level properties addendum test (docs/design/d-0004-reqreply-workflow-simplification.md's "Addendum"): the properties param
// supplied to Forward() must be carried, unmodified, onto the returned
// ForwardError.Properties — the field a conforming adapter's dispatch
// (e.g. mqtt5) later writes onto the actual wire reply.
func TestForward_WithProperties_ReturnsForwardErrorWithProperties(t *testing.T) {
	properties := map[string]string{"X-Replay-Reason": "retry"}
	err := reqreply.Forward(forwardTargetRoute, computeReq{X: 3, Y: 4}, nil, properties)
	var fwd reqreply.ForwardError
	if !errors.As(err, &fwd) {
		t.Fatalf("Forward error: want ForwardError, got %v (%T)", err, err)
	}
	if fwd.Properties["X-Replay-Reason"] != "retry" {
		t.Errorf("ForwardError.Properties = %+v, want {X-Replay-Reason: retry}", fwd.Properties)
	}
}

func TestForward_TemplatedTarget_ResolvesVarsViaBuildTopic(t *testing.T) {
	err := reqreply.Forward(forwardTemplateTargetRoute, computeReq{X: 1, Y: 2}, map[string]string{"tenantID": "acme"}, nil)
	var fwd reqreply.ForwardError
	if !errors.As(err, &fwd) {
		t.Fatalf("Forward error: want ForwardError, got %v (%T)", err, err)
	}
	if fwd.TargetTopic != "compute/acme/forward-target" {
		t.Errorf("ForwardError.TargetTopic = %q, want %q", fwd.TargetTopic, "compute/acme/forward-target")
	}
}

func TestForward_MissingRequiredVar_ReturnsForwardTargetVarError(t *testing.T) {
	err := reqreply.Forward(forwardTemplateTargetRoute, computeReq{X: 1, Y: 2}, nil, nil)
	if err == nil {
		t.Fatal("Forward: want a non-nil error for a missing required var, got nil")
	}
	var varErr reqreply.ForwardTargetVarError
	if !errors.As(err, &varErr) {
		t.Fatalf("Forward error: want ForwardTargetVarError, got %v (%T)", err, err)
	}
	if varErr.Topic != "compute/{tenantID}/forward-target" {
		t.Errorf("ForwardTargetVarError.Topic = %q, want the unresolved template", varErr.Topic)
	}
	var missing reqreply.MissingRouteParamError
	if !errors.As(varErr.Err, &missing) {
		t.Errorf("ForwardTargetVarError.Err: want it to wrap MissingRouteParamError, got %v (%T)", varErr.Err, varErr.Err)
	}
}

func TestForwardError_LogValue_IncludesTargetTopicAndPayloadLen(t *testing.T) {
	fwd := reqreply.ForwardError{TargetTopic: "compute/sum-forward-target", Payload: []byte("abc")}
	if got := fwd.LogValue().Kind().String(); got != "Group" {
		t.Errorf("ForwardError.LogValue().Kind() = %q, want %q", got, "Group")
	}
	if got := fwd.Error(); got == "" {
		t.Error("ForwardError.Error(): want a non-empty message")
	}
}

func TestForwardChainTooDeepError_ErrorMessage(t *testing.T) {
	err := reqreply.ForwardChainTooDeepError{TargetTopic: "compute/x", Depth: 10}
	if got := err.Error(); got == "" {
		t.Error("ForwardChainTooDeepError.Error(): want a non-empty message")
	}
}

func TestUnrecognizedForwardError_ErrorMessage(t *testing.T) {
	err := reqreply.UnrecognizedForwardError{TargetTopic: "compute/x"}
	if got := err.Error(); got == "" {
		t.Error("UnrecognizedForwardError.Error(): want a non-empty message")
	}
}

func TestForwardSecuritySchemeMismatchError_ErrorMessage(t *testing.T) {
	err := reqreply.ForwardSecuritySchemeMismatchError{OriginatingScheme: "bearerAuth", TargetScheme: "apiKeyAuth"}
	if got := err.Error(); got == "" {
		t.Error("ForwardSecuritySchemeMismatchError.Error(): want a non-empty message")
	}
}
