package reqreply_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/format"
)

// ── docs/design/d-0004-reqreply-workflow-simplification.md's own Phase 2.1 tests —
// reuses forwardFakeClientTransport (forward_resolve_test.go's own
// scripted fake) since ReplayDeadLetter's hop 1 dispatches via the EXACT
// SAME ClientTransport.CallRaw primitive [Client.followForwardFromHandle]
// does for hop 2+.

var deadLetterReplaySourceRoute = reqreply.NewRoute[computeReq, computeResp](
	"compute/dead-letter-replay-source", reqCodec, respCodec,
)

func TestClient_ReplayDeadLetter_HappyPath_DecodesSourceResponse(t *testing.T) {
	ft := &forwardFakeClientTransport{
		callRawSeq: []forwardFakeCallRawResult{
			{bytes: mustEncodeComputeResp(t, computeResp{Sum: 42})},
		},
	}
	c := reqreply.NewClient()
	if err := c.Attach(ft); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if err := c.RegisterRoute(deadLetterReplaySourceRoute); err != nil {
		t.Fatalf("RegisterRoute: %v", err)
	}

	envelope := reqreply.DeadLetterEnvelope{
		SourceTopic: deadLetterReplaySourceRoute.ClientHandle().Topic,
		Payload:     []byte(`{"x":1,"y":2}`),
		Error:       "decode failed",
		Timestamp:   time.Now().UTC(),
		Properties:  map[string]string{"X-Replay-Reason": "retry"},
	}
	respAny, err := c.ReplayDeadLetter(context.Background(), envelope)
	if err != nil {
		t.Fatalf("ReplayDeadLetter: want a resolved response, got error: %v", err)
	}
	resp, ok := respAny.(computeResp)
	if !ok {
		t.Fatalf("ReplayDeadLetter resp type = %T, want computeResp", respAny)
	}
	if resp.Sum != 42 {
		t.Errorf("resp.Sum = %d, want 42", resp.Sum)
	}
	if len(ft.callRawProperties) != 1 {
		t.Fatalf("CallRaw invocations recorded = %d, want 1", len(ft.callRawProperties))
	}
	if ft.callRawProperties[0]["X-Replay-Reason"] != "retry" {
		t.Errorf("CallRaw's properties argument = %+v, want {X-Replay-Reason: retry}", ft.callRawProperties[0])
	}
}

func TestClient_ReplayDeadLetter_UnrecognizedSource_ReturnsUnrecognizedDeadLetterSourceError(t *testing.T) {
	ft := &forwardFakeClientTransport{}
	c := reqreply.NewClient()
	if err := c.Attach(ft); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	envelope := reqreply.DeadLetterEnvelope{SourceTopic: "compute/never-registered-anywhere", Payload: []byte(`{}`)}
	_, err := c.ReplayDeadLetter(context.Background(), envelope)
	if err == nil {
		t.Fatal("ReplayDeadLetter: want UnrecognizedDeadLetterSourceError, got nil")
	}
	var unrecognized reqreply.UnrecognizedDeadLetterSourceError
	if !errors.As(err, &unrecognized) {
		t.Fatalf("ReplayDeadLetter error: want UnrecognizedDeadLetterSourceError, got %v (%T)", err, err)
	}
	if unrecognized.SourceTopic != "compute/never-registered-anywhere" {
		t.Errorf("UnrecognizedDeadLetterSourceError.SourceTopic = %q, want %q", unrecognized.SourceTopic, "compute/never-registered-anywhere")
	}
	if len(ft.callRawProperties) != 0 {
		t.Errorf("CallRaw invocations recorded = %d, want 0 (fails before any republish)", len(ft.callRawProperties))
	}
}

func TestClient_ReplayDeadLetter_PoisonedSource_ReturnsDuplicateTopicTypeMismatchError(t *testing.T) {
	conflictTopic := "compute/dead-letter-replay-poisoned-topic"
	first := reqreply.NewRoute[computeReq, computeResp](conflictTopic, reqCodec, respCodec)
	conflicting := reqreply.NewRoute[tenantComputeReq, computeResp](conflictTopic, tenantComputeReqCodec, respCodec)

	ft := &forwardFakeClientTransport{callResp: computeResp{}}
	c := reqreply.NewClient()
	if err := c.Attach(ft); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if err := c.RegisterRoute(first); err != nil {
		t.Fatalf("RegisterRoute(first): %v", err)
	}
	// Poisons the registry entry — mirrors TestClient_Call_
	// PoisonedForwardTarget_ReturnsDuplicateTopicTypeMismatchError's own
	// setup exactly.
	_, _ = c.Call(context.Background(), conflicting, tenantComputeReq{})

	envelope := reqreply.DeadLetterEnvelope{SourceTopic: conflictTopic, Payload: []byte(`{}`)}
	_, err := c.ReplayDeadLetter(context.Background(), envelope)
	if err == nil {
		t.Fatal("ReplayDeadLetter: want DuplicateTopicTypeMismatchError, got nil")
	}
	var poisoned reqreply.DuplicateTopicTypeMismatchError
	if !errors.As(err, &poisoned) {
		t.Fatalf("ReplayDeadLetter error: want DuplicateTopicTypeMismatchError, got %v (%T)", err, err)
	}
}

func TestClient_ReplayDeadLetter_HopOneForwardsFurther_ChainsIntoFollowForward(t *testing.T) {
	ft := &forwardFakeClientTransport{
		callRawSeq: []forwardFakeCallRawResult{
			{err: reqreply.ForwardError{TargetTopic: forwardTargetNoSecurityRoute.ClientHandle().Topic, Payload: []byte(`{"x":3,"y":4}`)}},
			{bytes: mustEncodeComputeResp(t, computeResp{Sum: 99})},
		},
	}
	c := reqreply.NewClient()
	if err := c.Attach(ft); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if err := c.RegisterRoute(deadLetterReplaySourceRoute); err != nil {
		t.Fatalf("RegisterRoute(source): %v", err)
	}
	if err := c.RegisterRoute(forwardTargetNoSecurityRoute); err != nil {
		t.Fatalf("RegisterRoute(target): %v", err)
	}

	envelope := reqreply.DeadLetterEnvelope{
		SourceTopic: deadLetterReplaySourceRoute.ClientHandle().Topic,
		Payload:     []byte(`{"x":1,"y":2}`),
	}
	respAny, err := c.ReplayDeadLetter(context.Background(), envelope, reqreply.ClientCallOptions{MaxForwards: 2})
	if err != nil {
		t.Fatalf("ReplayDeadLetter: want hop 1's forward followed, got error: %v", err)
	}
	resp, ok := respAny.(computeResp)
	if !ok {
		t.Fatalf("ReplayDeadLetter resp type = %T, want computeResp", respAny)
	}
	if resp.Sum != 99 {
		t.Errorf("resp.Sum = %d, want 99", resp.Sum)
	}
	// MaxForwards accounting: hop 1 lives OUTSIDE the shared loop (the
	// SAME structural analogue as Client.Call's own hop 1) — only hop
	// 2's single CallRaw invocation is counted against the cap, so a
	// MaxForwards of 2 is never exhausted by a chain this short. Both
	// CallRaw invocations are nonetheless recorded by the fake.
	if len(ft.callRawProperties) != 2 {
		t.Fatalf("CallRaw invocations recorded = %d, want 2 (hop 1 + hop 2)", len(ft.callRawProperties))
	}
}

// TestClient_ReplayDeadLetter_HopTwoSecurityScheme_UsesSourceTopicAsOriginating
// is `deep-dive-bug-hunt` Round 206's own test-coverage-gap fix: the
// roadmap doc's whole "key simplification" rests on hop 2+ comparing
// EVERY later hop's target scheme against the JUST-RESOLVED SourceTopic
// route's OWN declared scheme (passed as followForwardFromHandle's
// originatingHandle) — this was implemented but had ZERO test coverage
// confirming it actually works, for EITHER outcome (mismatch rejected,
// match proceeds). forwardOriginatingRoute (reused from forward_resolve_
// test.go) declares bearerAuth — standing in as a SECURED SourceTopic
// route here.
func TestClient_ReplayDeadLetter_HopTwoSecurityScheme_UsesSourceTopicAsOriginating(t *testing.T) {
	t.Run("mismatch rejected", func(t *testing.T) {
		ft := &forwardFakeClientTransport{
			callRawSeq: []forwardFakeCallRawResult{
				{err: reqreply.ForwardError{TargetTopic: forwardTargetAPIKeyRoute.ClientHandle().Topic, Payload: []byte(`{}`)}},
			},
		}
		c := reqreply.NewClient()
		if err := c.Attach(ft); err != nil {
			t.Fatalf("Attach: %v", err)
		}
		if err := c.RegisterRoute(forwardOriginatingRoute); err != nil {
			t.Fatalf("RegisterRoute(source): %v", err)
		}
		if err := c.RegisterRoute(forwardTargetAPIKeyRoute); err != nil {
			t.Fatalf("RegisterRoute(target): %v", err)
		}

		envelope := reqreply.DeadLetterEnvelope{SourceTopic: forwardOriginatingRoute.ClientHandle().Topic, Payload: []byte(`{}`)}
		_, err := c.ReplayDeadLetter(context.Background(), envelope)
		if err == nil {
			t.Fatal("ReplayDeadLetter: want ForwardSecuritySchemeMismatchError, got nil")
		}
		var mismatch reqreply.ForwardSecuritySchemeMismatchError
		if !errors.As(err, &mismatch) {
			t.Fatalf("ReplayDeadLetter error: want ForwardSecuritySchemeMismatchError, got %v (%T)", err, err)
		}
		if mismatch.OriginatingScheme != "bearerAuth" || mismatch.TargetScheme != "apiKeyAuth" {
			t.Errorf("mismatch = %+v, want {OriginatingScheme: bearerAuth, TargetScheme: apiKeyAuth}", mismatch)
		}
	})

	t.Run("match proceeds", func(t *testing.T) {
		ft := &forwardFakeClientTransport{
			callRawSeq: []forwardFakeCallRawResult{
				{err: reqreply.ForwardError{TargetTopic: forwardTargetBearerRoute.ClientHandle().Topic, Payload: []byte(`{}`)}},
				{bytes: mustEncodeComputeResp(t, computeResp{Sum: 11})},
			},
		}
		c := reqreply.NewClient()
		if err := c.Attach(ft); err != nil {
			t.Fatalf("Attach: %v", err)
		}
		if err := c.RegisterRoute(forwardOriginatingRoute); err != nil {
			t.Fatalf("RegisterRoute(source): %v", err)
		}
		if err := c.RegisterRoute(forwardTargetBearerRoute); err != nil {
			t.Fatalf("RegisterRoute(target): %v", err)
		}

		envelope := reqreply.DeadLetterEnvelope{SourceTopic: forwardOriginatingRoute.ClientHandle().Topic, Payload: []byte(`{}`)}
		respAny, err := c.ReplayDeadLetter(context.Background(), envelope)
		if err != nil {
			t.Fatalf("ReplayDeadLetter: want a same-scheme hop 2 to proceed, got error: %v", err)
		}
		if resp, _ := respAny.(computeResp); resp.Sum != 11 {
			t.Errorf("resp = %+v, want Sum=11", resp)
		}
	})
}

func TestClient_ReplayDeadLetter_ErrorPatternMatchedHopOneReply_DecodesViaErrorPattern(t *testing.T) {
	body, err := format.JSON(forwardErrPayloadCodec).Marshal(forwardErrPayload{Code: "conflict", Message: "duplicate"})
	if err != nil {
		t.Fatalf("encode forwardErrPayload: %v", err)
	}
	ft := &forwardFakeClientTransport{
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

	envelope := reqreply.DeadLetterEnvelope{SourceTopic: forwardTargetErrorPatternRoute.ClientHandle().Topic, Payload: []byte(`{}`)}
	_, callErr := c.ReplayDeadLetter(context.Background(), envelope)
	if callErr == nil {
		t.Fatal("ReplayDeadLetter: want a decoded ErrorPatternResponse, got nil error")
	}
	var epr reqreply.ErrorPatternResponse
	if !errors.As(callErr, &epr) {
		t.Fatalf("ReplayDeadLetter error: want ErrorPatternResponse, got %v (%T)", callErr, callErr)
	}
	if epr.Code != "conflict" {
		t.Errorf("ErrorPatternResponse.Code = %q, want %q", epr.Code, "conflict")
	}
}

func TestClient_ReplayDeadLetterAsync_ResolvesFuture(t *testing.T) {
	ft := &forwardFakeClientTransport{
		callRawSeq: []forwardFakeCallRawResult{
			{bytes: mustEncodeComputeResp(t, computeResp{Sum: 77})},
		},
	}
	c := reqreply.NewClient()
	if err := c.Attach(ft); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if err := c.RegisterRoute(deadLetterReplaySourceRoute); err != nil {
		t.Fatalf("RegisterRoute: %v", err)
	}

	envelope := reqreply.DeadLetterEnvelope{
		SourceTopic: deadLetterReplaySourceRoute.ClientHandle().Topic,
		Payload:     []byte(`{"x":1,"y":2}`),
	}
	futureAny, err := c.ReplayDeadLetterAsync(context.Background(), envelope)
	if err != nil {
		t.Fatalf("ReplayDeadLetterAsync: %v", err)
	}
	future := futureAny.(*reqreply.Future[computeResp])
	resp, waitErr := future.Wait(context.Background())
	if waitErr != nil {
		t.Fatalf("future.Wait: want the replay resolved via ReplayDeadLetterAsync too, got error: %v", waitErr)
	}
	if resp.Sum != 77 {
		t.Errorf("resp.Sum = %d, want 77", resp.Sum)
	}
}

func TestClient_ReplayDeadLetter_NoTransportAttached_ReturnsNoClientTransportAttachedError(t *testing.T) {
	c := reqreply.NewClient()
	envelope := reqreply.DeadLetterEnvelope{SourceTopic: "compute/whatever", Payload: []byte(`{}`)}
	_, err := c.ReplayDeadLetter(context.Background(), envelope)
	var want reqreply.NoClientTransportAttachedError
	if !errors.As(err, &want) {
		t.Fatalf("ReplayDeadLetter error: want NoClientTransportAttachedError, got %v (%T)", err, err)
	}
}
