package reqreply_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
)

// ── forwardFakeClientTransport — scripted Call()/CallRaw() results, for
// exercising [reqreply.Client.Call]'s chain-following loop WITHOUT a real
// adapter/wire signal — the loop operates purely on Go error values, so
// a fake transport is enough (mirrors forward_test.go's own in-isolation
// approach for Forward() itself).

type forwardFakeCallRawResult struct {
	bytes []byte
	err   error
}

type forwardFakeClientTransport struct {
	callResp any
	callErr  error

	callRawSeq []forwardFakeCallRawResult
	callRawIdx int

	// callRawProperties RECORDS the properties argument each CallRaw
	// invocation received, in order — lets Phase 1.5 tests confirm
	// followForward actually threads fwd.Properties through, not just
	// the topic/payload.
	callRawProperties []map[string]string
}

func (t *forwardFakeClientTransport) Call(_ context.Context, _ any, _ any, _ ...reqreply.ClientCallOptions) (any, error) {
	return t.callResp, t.callErr
}

func (t *forwardFakeClientTransport) CallRaw(_ context.Context, _ string, _ []byte, properties map[string]string) ([]byte, error) {
	t.callRawProperties = append(t.callRawProperties, properties)
	if t.callRawIdx >= len(t.callRawSeq) {
		return nil, fmt.Errorf("forwardFakeClientTransport.CallRaw: no more scripted results (call %d)", t.callRawIdx)
	}
	r := t.callRawSeq[t.callRawIdx]
	t.callRawIdx++
	return r.bytes, r.err
}

var _ reqreply.ClientTransport = (*forwardFakeClientTransport)(nil)

// ── fixtures — reuse computeReq/computeResp/reqCodec/respCodec from
// route_test.go; declare a handful of forward-target routes, some with
// security requirements, to exercise the scheme-mismatch check.

var (
	forwardOriginatingRoute = reqreply.NewRoute[computeReq, computeResp](
		"compute/forward-originating", reqCodec, respCodec,
		reqreply.RouteMeta{Security: []reqreply.SecurityRequirement{reqreply.Require("bearerAuth")}},
	)
	forwardTargetBearerRoute = reqreply.NewRoute[computeReq, computeResp](
		"compute/forward-target-bearer", reqCodec, respCodec,
		reqreply.RouteMeta{Security: []reqreply.SecurityRequirement{reqreply.Require("bearerAuth")}},
	)
	forwardTargetAPIKeyRoute = reqreply.NewRoute[computeReq, computeResp](
		"compute/forward-target-apikey", reqCodec, respCodec,
		reqreply.RouteMeta{Security: []reqreply.SecurityRequirement{reqreply.Require("apiKeyAuth")}},
	)
	forwardTargetNoSecurityRoute = reqreply.NewRoute[computeReq, computeResp](
		"compute/forward-target-no-security", reqCodec, respCodec,
	)
	forwardTargetChainRoute = reqreply.NewRoute[computeReq, computeResp](
		"compute/forward-target-chain", reqCodec, respCodec,
	)
	forwardTargetErrorPatternRoute = reqreply.NewRoute[computeReq, computeResp](
		"compute/forward-target-error-pattern", reqCodec, respCodec,
		reqreply.ErrorPattern[forwardConflictErr, forwardErrPayload](forwardErrPayloadCodec,
			func(e forwardConflictErr) (forwardErrPayload, error) {
				return forwardErrPayload{Code: "conflict", Message: e.msg}, nil
			},
		).WithCode("conflict"),
	)
)

type forwardConflictErr struct{ msg string }

func (e forwardConflictErr) Error() string { return e.msg }

type forwardErrPayload struct {
	Code    string
	Message string
}

var forwardErrPayloadCodec = codex.Struct[forwardErrPayload](
	codex.RequiredField("code", codex.String(),
		func(p forwardErrPayload) string { return p.Code },
		func(p *forwardErrPayload, v string) { p.Code = v },
	),
	codex.RequiredField("message", codex.String(),
		func(p forwardErrPayload) string { return p.Message },
		func(p *forwardErrPayload, v string) { p.Message = v },
	),
)

func mustEncodeComputeResp(t *testing.T, resp computeResp) []byte {
	t.Helper()
	// Every forward-target fixture route shares the SAME computeResp
	// codec — any of their handles' Encode works identically; picking
	// the no-security one keeps this helper independent of which
	// specific target a given test exercises.
	b, err := forwardTargetNoSecurityRoute.ClientHandle().Encode(resp)
	if err != nil {
		t.Fatalf("Encode(computeResp): %v", err)
	}
	return b
}

func TestClient_Call_FollowsSingleForward_DecodesTargetResponse(t *testing.T) {
	ft := &forwardFakeClientTransport{
		callErr: reqreply.ForwardError{TargetTopic: forwardTargetBearerRoute.ClientHandle().Topic, Payload: []byte(`{"x":1,"y":2}`)},
		callRawSeq: []forwardFakeCallRawResult{
			{bytes: mustEncodeComputeResp(t, computeResp{Sum: 42})},
		},
	}
	c := reqreply.NewClient()
	if err := c.Attach(ft); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if err := c.RegisterRoute(forwardTargetBearerRoute); err != nil {
		t.Fatalf("RegisterRoute(target): %v", err)
	}

	respAny, err := c.Call(context.Background(), forwardOriginatingRoute, computeReq{X: 1, Y: 2})
	if err != nil {
		t.Fatalf("Call: want the forward followed and resolved, got error: %v", err)
	}
	resp, ok := respAny.(computeResp)
	if !ok {
		t.Fatalf("Call resp type = %T, want computeResp", respAny)
	}
	if resp.Sum != 42 {
		t.Errorf("resp.Sum = %d, want 42", resp.Sum)
	}
	if ft.callRawIdx != 1 {
		t.Errorf("CallRaw invocations = %d, want 1", ft.callRawIdx)
	}
}

// TestClient_Call_FollowsForward_ThreadsPropertiesIntoCallRaw is Phase
// 1.5's own wire-level properties addendum test (docs/roadmap/
// reqreply-forward-bounce.md's "Addendum"): followForward's loop must
// pass the received ForwardError's Properties through to CallRaw
// UNMODIFIED — the mechanism a dead-lettered credential (Phase 2) relies
// on to actually reach the wire.
func TestClient_Call_FollowsForward_ThreadsPropertiesIntoCallRaw(t *testing.T) {
	properties := map[string]string{"X-Replay-Reason": "retry"}
	ft := &forwardFakeClientTransport{
		callErr: reqreply.ForwardError{
			TargetTopic: forwardTargetBearerRoute.ClientHandle().Topic,
			Payload:     []byte(`{"x":1,"y":2}`),
			Properties:  properties,
		},
		callRawSeq: []forwardFakeCallRawResult{
			{bytes: mustEncodeComputeResp(t, computeResp{Sum: 42})},
		},
	}
	c := reqreply.NewClient()
	if err := c.Attach(ft); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if err := c.RegisterRoute(forwardTargetBearerRoute); err != nil {
		t.Fatalf("RegisterRoute(target): %v", err)
	}

	if _, err := c.Call(context.Background(), forwardOriginatingRoute, computeReq{X: 1, Y: 2}); err != nil {
		t.Fatalf("Call: want the forward followed, got error: %v", err)
	}
	if len(ft.callRawProperties) != 1 {
		t.Fatalf("CallRaw invocations recorded = %d, want 1", len(ft.callRawProperties))
	}
	if ft.callRawProperties[0]["X-Replay-Reason"] != "retry" {
		t.Errorf("CallRaw's properties argument = %+v, want {X-Replay-Reason: retry}", ft.callRawProperties[0])
	}
}

func TestClient_Call_FollowsChainedForward_TwoHops(t *testing.T) {
	ft := &forwardFakeClientTransport{
		callErr: reqreply.ForwardError{TargetTopic: forwardTargetChainRoute.ClientHandle().Topic, Payload: []byte(`{"x":1,"y":2}`)},
		callRawSeq: []forwardFakeCallRawResult{
			{err: reqreply.ForwardError{TargetTopic: forwardTargetNoSecurityRoute.ClientHandle().Topic, Payload: []byte(`{"x":3,"y":4}`)}},
			{bytes: mustEncodeComputeResp(t, computeResp{Sum: 99})},
		},
	}
	c := reqreply.NewClient()
	if err := c.Attach(ft); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if err := c.RegisterRoute(forwardTargetChainRoute); err != nil {
		t.Fatalf("RegisterRoute(chain target): %v", err)
	}
	if err := c.RegisterRoute(forwardTargetNoSecurityRoute); err != nil {
		t.Fatalf("RegisterRoute(final target): %v", err)
	}

	respAny, err := c.Call(context.Background(), forwardOriginatingRoute, computeReq{X: 1, Y: 2})
	if err != nil {
		t.Fatalf("Call: want the 2-hop chain followed and resolved, got error: %v", err)
	}
	resp, ok := respAny.(computeResp)
	if !ok {
		t.Fatalf("Call resp type = %T, want computeResp", respAny)
	}
	if resp.Sum != 99 {
		t.Errorf("resp.Sum = %d, want 99", resp.Sum)
	}
	if ft.callRawIdx != 2 {
		t.Errorf("CallRaw invocations = %d, want 2", ft.callRawIdx)
	}
}

func TestClient_Call_MaxForwardsExhausted_ReturnsForwardChainTooDeepError(t *testing.T) {
	targetTopic := forwardTargetNoSecurityRoute.ClientHandle().Topic
	// Script enough "forward again, same topic" results to exceed a
	// small MaxForwards — a cycle (A forwards to itself) is exactly the
	// case this cap exists for.
	seq := make([]forwardFakeCallRawResult, 5)
	for i := range seq {
		seq[i] = forwardFakeCallRawResult{err: reqreply.ForwardError{TargetTopic: targetTopic, Payload: []byte(`{}`)}}
	}
	ft := &forwardFakeClientTransport{
		callErr:    reqreply.ForwardError{TargetTopic: targetTopic, Payload: []byte(`{}`)},
		callRawSeq: seq,
	}
	c := reqreply.NewClient()
	if err := c.Attach(ft); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if err := c.RegisterRoute(forwardTargetNoSecurityRoute); err != nil {
		t.Fatalf("RegisterRoute: %v", err)
	}

	_, err := c.Call(context.Background(), forwardOriginatingRoute, computeReq{X: 1, Y: 2}, reqreply.ClientCallOptions{MaxForwards: 2})
	if err == nil {
		t.Fatal("Call: want ForwardChainTooDeepError, got nil")
	}
	var tooDeep reqreply.ForwardChainTooDeepError
	if !errors.As(err, &tooDeep) {
		t.Fatalf("Call error: want ForwardChainTooDeepError, got %v (%T)", err, err)
	}
	if tooDeep.Depth != 2 {
		t.Errorf("ForwardChainTooDeepError.Depth = %d, want 2", tooDeep.Depth)
	}
}

func TestClient_Call_UnrecognizedForwardTarget_ReturnsUnrecognizedForwardError(t *testing.T) {
	ft := &forwardFakeClientTransport{
		callErr: reqreply.ForwardError{TargetTopic: "compute/never-registered-anywhere", Payload: []byte(`{}`)},
	}
	c := reqreply.NewClient()
	if err := c.Attach(ft); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	_, err := c.Call(context.Background(), forwardOriginatingRoute, computeReq{X: 1, Y: 2})
	if err == nil {
		t.Fatal("Call: want UnrecognizedForwardError, got nil")
	}
	var unrecognized reqreply.UnrecognizedForwardError
	if !errors.As(err, &unrecognized) {
		t.Fatalf("Call error: want UnrecognizedForwardError, got %v (%T)", err, err)
	}
	if unrecognized.TargetTopic != "compute/never-registered-anywhere" {
		t.Errorf("UnrecognizedForwardError.TargetTopic = %q, want %q", unrecognized.TargetTopic, "compute/never-registered-anywhere")
	}
	if ft.callRawIdx != 0 {
		t.Errorf("CallRaw invocations = %d, want 0 (fails before any republish)", ft.callRawIdx)
	}
}

func TestClient_Call_PoisonedForwardTarget_ReturnsDuplicateTopicTypeMismatchError(t *testing.T) {
	conflictTopic := "compute/forward-poisoned-topic"
	first := reqreply.NewRoute[computeReq, computeResp](conflictTopic, reqCodec, respCodec)
	conflicting := reqreply.NewRoute[tenantComputeReq, computeResp](conflictTopic, tenantComputeReqCodec, respCodec)

	ft := &forwardFakeClientTransport{
		callResp: computeResp{},
	}
	c := reqreply.NewClient()
	if err := c.Attach(ft); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if err := c.RegisterRoute(first); err != nil {
		t.Fatalf("RegisterRoute(first): %v", err)
	}
	// registerAutoFromCall's own "never errors" contract poisons the
	// entry instead of rejecting outright — mirrors
	// client_registry_test.go's established convention for provoking
	// this exact state. ft.callErr is set to nil for THIS bootstrap call
	// only — forwardFakeClientTransport.Call ignores its routeAny
	// argument entirely, so leaving callErr set to the eventual
	// ForwardError here would wrongly trigger the chain-following loop
	// a call early, before the entry is even poisoned.
	if _, err := c.Call(context.Background(), conflicting, tenantComputeReq{}); err != nil {
		t.Fatalf("Call(conflicting) to poison the registry entry: want no error, got %v", err)
	}
	ft.callErr = reqreply.ForwardError{TargetTopic: conflictTopic, Payload: []byte(`{}`)}

	_, err := c.Call(context.Background(), forwardOriginatingRoute, computeReq{X: 1, Y: 2})
	if err == nil {
		t.Fatal("Call: want DuplicateTopicTypeMismatchError, got nil")
	}
	var dup reqreply.DuplicateTopicTypeMismatchError
	if !errors.As(err, &dup) {
		t.Fatalf("Call error: want DuplicateTopicTypeMismatchError, got %v (%T)", err, err)
	}
	if dup.Topic != conflictTopic {
		t.Errorf("DuplicateTopicTypeMismatchError.Topic = %q, want %q", dup.Topic, conflictTopic)
	}
}

func TestClient_Call_ForwardSecuritySchemeMismatch_FailsFastBeforeCallRaw(t *testing.T) {
	ft := &forwardFakeClientTransport{
		callErr: reqreply.ForwardError{TargetTopic: forwardTargetAPIKeyRoute.ClientHandle().Topic, Payload: []byte(`{}`)},
		callRawSeq: []forwardFakeCallRawResult{
			{bytes: mustEncodeComputeResp(t, computeResp{Sum: 1})}, // must NEVER be consumed
		},
	}
	c := reqreply.NewClient()
	if err := c.Attach(ft); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if err := c.RegisterRoute(forwardTargetAPIKeyRoute); err != nil {
		t.Fatalf("RegisterRoute: %v", err)
	}

	_, err := c.Call(context.Background(), forwardOriginatingRoute, computeReq{X: 1, Y: 2})
	if err == nil {
		t.Fatal("Call: want ForwardSecuritySchemeMismatchError, got nil")
	}
	var mismatch reqreply.ForwardSecuritySchemeMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("Call error: want ForwardSecuritySchemeMismatchError, got %v (%T)", err, err)
	}
	if mismatch.OriginatingScheme != "bearerAuth" || mismatch.TargetScheme != "apiKeyAuth" {
		t.Errorf("mismatch = %+v, want {bearerAuth apiKeyAuth}", mismatch)
	}
	if ft.callRawIdx != 0 {
		t.Errorf("CallRaw invocations = %d, want 0 (fails fast, BEFORE the network round-trip)", ft.callRawIdx)
	}
}

func TestClient_Call_ForwardSecuritySchemeMatch_Proceeds(t *testing.T) {
	ft := &forwardFakeClientTransport{
		callErr: reqreply.ForwardError{TargetTopic: forwardTargetBearerRoute.ClientHandle().Topic, Payload: []byte(`{}`)},
		callRawSeq: []forwardFakeCallRawResult{
			{bytes: mustEncodeComputeResp(t, computeResp{Sum: 7})},
		},
	}
	c := reqreply.NewClient()
	if err := c.Attach(ft); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if err := c.RegisterRoute(forwardTargetBearerRoute); err != nil {
		t.Fatalf("RegisterRoute: %v", err)
	}

	respAny, err := c.Call(context.Background(), forwardOriginatingRoute, computeReq{X: 1, Y: 2})
	if err != nil {
		t.Fatalf("Call: want same-scheme forward to proceed, got error: %v", err)
	}
	if resp, _ := respAny.(computeResp); resp.Sum != 7 {
		t.Errorf("resp = %+v, want Sum=7", resp)
	}
}

func TestClient_Call_ForwardTargetRequiresNoSecurity_AlwaysProceeds(t *testing.T) {
	ft := &forwardFakeClientTransport{
		callErr: reqreply.ForwardError{TargetTopic: forwardTargetNoSecurityRoute.ClientHandle().Topic, Payload: []byte(`{}`)},
		callRawSeq: []forwardFakeCallRawResult{
			{bytes: mustEncodeComputeResp(t, computeResp{Sum: 3})},
		},
	}
	c := reqreply.NewClient()
	if err := c.Attach(ft); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if err := c.RegisterRoute(forwardTargetNoSecurityRoute); err != nil {
		t.Fatalf("RegisterRoute: %v", err)
	}

	respAny, err := c.Call(context.Background(), forwardOriginatingRoute, computeReq{X: 1, Y: 2})
	if err != nil {
		t.Fatalf("Call: want a no-security target to always proceed, got error: %v", err)
	}
	if resp, _ := respAny.(computeResp); resp.Sum != 3 {
		t.Errorf("resp = %+v, want Sum=3", resp)
	}
}

func TestClient_Call_ForwardedErrorPatternReply_DecodesViaChain(t *testing.T) {
	body, err := format.JSON(forwardErrPayloadCodec).Marshal(forwardErrPayload{Code: "conflict", Message: "duplicate"})
	if err != nil {
		t.Fatalf("encode forwardErrPayload: %v", err)
	}
	ft := &forwardFakeClientTransport{
		callErr: reqreply.ForwardError{TargetTopic: forwardTargetErrorPatternRoute.ClientHandle().Topic, Payload: []byte(`{}`)},
		callRawSeq: []forwardFakeCallRawResult{
			{err: reqreply.ForwardRawErrorReply{Code: "conflict", Body: body}},
		},
	}
	c := reqreply.NewClient()
	if err := c.Attach(ft); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if err := c.RegisterRoute(forwardTargetErrorPatternRoute); err != nil {
		t.Fatalf("RegisterRoute: %v", err)
	}

	_, callErr := c.Call(context.Background(), forwardOriginatingRoute, computeReq{X: 1, Y: 2})
	if callErr == nil {
		t.Fatal("Call: want a decoded ErrorPatternResponse, got nil error")
	}
	var epr reqreply.ErrorPatternResponse
	if !errors.As(callErr, &epr) {
		t.Fatalf("Call error: want ErrorPatternResponse, got %v (%T)", callErr, callErr)
	}
	if epr.Code != "conflict" {
		t.Errorf("ErrorPatternResponse.Code = %q, want %q", epr.Code, "conflict")
	}
	payload, ok := epr.Value.(forwardErrPayload)
	if !ok {
		t.Fatalf("ErrorPatternResponse.Value type = %T, want forwardErrPayload", epr.Value)
	}
	if payload.Message != "duplicate" {
		t.Errorf("payload.Message = %q, want %q", payload.Message, "duplicate")
	}
}

func TestClient_Call_ForwardedErrorPatternReply_UnmatchedCode_FallsBackToRawReply(t *testing.T) {
	ft := &forwardFakeClientTransport{
		callErr: reqreply.ForwardError{TargetTopic: forwardTargetErrorPatternRoute.ClientHandle().Topic, Payload: []byte(`{}`)},
		callRawSeq: []forwardFakeCallRawResult{
			{err: reqreply.ForwardRawErrorReply{Code: "no-such-pattern", Body: []byte("plain text error")}},
		},
	}
	c := reqreply.NewClient()
	if err := c.Attach(ft); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if err := c.RegisterRoute(forwardTargetErrorPatternRoute); err != nil {
		t.Fatalf("RegisterRoute: %v", err)
	}

	_, callErr := c.Call(context.Background(), forwardOriginatingRoute, computeReq{X: 1, Y: 2})
	if callErr == nil {
		t.Fatal("Call: want the raw ForwardRawErrorReply as a fallback, got nil error")
	}
	var raw reqreply.ForwardRawErrorReply
	if !errors.As(callErr, &raw) {
		t.Fatalf("Call error: want ForwardRawErrorReply fallback, got %v (%T)", callErr, callErr)
	}
	if raw.Code != "no-such-pattern" {
		t.Errorf("raw.Code = %q, want %q", raw.Code, "no-such-pattern")
	}
}

func TestClient_CallAsync_FollowsForward(t *testing.T) {
	ft := &forwardFakeClientTransport{
		callErr: reqreply.ForwardError{TargetTopic: forwardTargetNoSecurityRoute.ClientHandle().Topic, Payload: []byte(`{}`)},
		callRawSeq: []forwardFakeCallRawResult{
			{bytes: mustEncodeComputeResp(t, computeResp{Sum: 55})},
		},
	}
	c := reqreply.NewClient()
	if err := c.Attach(ft); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if err := c.RegisterRoute(forwardTargetNoSecurityRoute); err != nil {
		t.Fatalf("RegisterRoute: %v", err)
	}

	futureAny, err := c.CallAsync(context.Background(), forwardOriginatingRoute.ClientHandle(), computeReq{X: 1, Y: 2})
	if err != nil {
		t.Fatalf("CallAsync: %v", err)
	}
	future := futureAny.(*reqreply.Future[computeResp])
	resp, waitErr := future.Wait(context.Background())
	if waitErr != nil {
		t.Fatalf("future.Wait: want the forward followed and resolved via CallAsync too, got error: %v", waitErr)
	}
	if resp.Sum != 55 {
		t.Errorf("resp.Sum = %d, want 55", resp.Sum)
	}
}
