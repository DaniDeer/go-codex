package reqreply_test

import (
	"context"
	"errors"
	"testing"

	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/stats"
)

// This file tests F4's fix (session review finding): the roadmap's own
// test plan calls for ObserveErrorResponseFor/HasErrorPatterns/
// ErrorPatternObserver tests across api/rest, api/events, api/reqreply —
// only api/rest had them before this file. Mirrors
// api/rest/observability_test.go's structure exactly.

type reqreplyErrorPatternObserverSpy struct {
	stats.NoopObserver
	matches, misses int
	lastLocation    string
	lastCode        string
	lastAction      string
	lastMissLoc     string
	tags            int
	lastTagValue    string
}

func (s *reqreplyErrorPatternObserverSpy) RecordErrorPatternMatch(location, code, action string) {
	s.matches++
	s.lastLocation, s.lastCode, s.lastAction = location, code, action
}

func (s *reqreplyErrorPatternObserverSpy) RecordErrorPatternMiss(location string) {
	s.misses++
	s.lastMissLoc = location
}

func (s *reqreplyErrorPatternObserverSpy) TagSpan(_ context.Context, _, value string) {
	s.tags++
	s.lastTagValue = value
}

type observeReqreplyTestErr struct{ msg string }

func (e observeReqreplyTestErr) Error() string { return e.msg }

func newObserveTestRouteHandle(t *testing.T) *reqreply.RouteHandle[computeReq, computeResp] {
	t.Helper()
	b := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	h, err := reqreply.NewRoute[computeReq, computeResp]("observe/errors", reqCodec, respCodec,
		reqreply.ErrorPattern[observeReqreplyTestErr, computeResp](respCodec,
			func(e observeReqreplyTestErr) (computeResp, error) {
				return computeResp{Sum: -1}, nil
			},
		).WithCode("observe-err"),
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	return h
}

func TestObserveErrorResponseFor_RecordsMatch(t *testing.T) {
	h := newObserveTestRouteHandle(t)
	spy := &reqreplyErrorPatternObserverSpy{}
	resp, matched, applyErr := h.ObserveErrorResponseFor(context.Background(), spy, observeReqreplyTestErr{msg: "boom"})
	if applyErr != nil {
		t.Fatalf("applyErr: %v", applyErr)
	}
	if !matched {
		t.Fatal("want matched=true")
	}
	if resp.Code != "observe-err" {
		t.Errorf("want Code=observe-err, got %q", resp.Code)
	}
	if spy.matches != 1 {
		t.Fatalf("want 1 RecordErrorPatternMatch call, got %d", spy.matches)
	}
	if spy.misses != 0 {
		t.Errorf("want 0 RecordErrorPatternMiss calls on a match, got %d", spy.misses)
	}
}

func TestObserveErrorResponseFor_RecordsMiss_OnlyWhenPatternsDeclared(t *testing.T) {
	h := newObserveTestRouteHandle(t)
	spy := &reqreplyErrorPatternObserverSpy{}
	_, matched, _ := h.ObserveErrorResponseFor(context.Background(), spy, errors.New("unrelated"))
	if matched {
		t.Fatal("want matched=false")
	}
	if spy.misses != 1 {
		t.Fatalf("want 1 RecordErrorPatternMiss call, got %d", spy.misses)
	}

	// Negative case: a route with NO declared ErrorPattern never calls
	// RecordErrorPatternMiss at all.
	b := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	noPatterns, err := reqreply.NewRoute[computeReq, computeResp]("observe/no-patterns", reqCodec, respCodec).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	spy2 := &reqreplyErrorPatternObserverSpy{}
	_, matched2, _ := noPatterns.ObserveErrorResponseFor(context.Background(), spy2, errors.New("unrelated"))
	if matched2 {
		t.Fatal("want matched=false")
	}
	if spy2.misses != 0 {
		t.Errorf("want 0 RecordErrorPatternMiss calls when no patterns declared, got %d", spy2.misses)
	}
}

func TestObserveErrorResponseFor_TagsSpan_OnMatch(t *testing.T) {
	h := newObserveTestRouteHandle(t)
	spy := &reqreplyErrorPatternObserverSpy{}
	_, matched, _ := h.ObserveErrorResponseFor(context.Background(), spy, observeReqreplyTestErr{msg: "boom"})
	if !matched {
		t.Fatal("want matched=true")
	}
	if spy.tags != 1 {
		t.Fatalf("want 1 TagSpan call, got %d", spy.tags)
	}
	if spy.lastTagValue != "observe-err" {
		t.Errorf("want tag value=observe-err, got %q", spy.lastTagValue)
	}
}

func TestObserveErrorResponseFor_PlainObserver_NoPanic(t *testing.T) {
	h := newObserveTestRouteHandle(t)
	plain := stats.NoopObserver{}
	if _, _, err := h.ObserveErrorResponseFor(context.Background(), plain, observeReqreplyTestErr{msg: "boom"}); err != nil {
		t.Fatalf("unexpected applyErr: %v", err)
	}
	if _, _, err := h.ObserveErrorResponseFor(context.Background(), plain, errors.New("unrelated")); err != nil {
		t.Fatalf("unexpected applyErr: %v", err)
	}
}

func TestRouteHandle_HasErrorPatterns(t *testing.T) {
	h := newObserveTestRouteHandle(t)
	if !h.HasErrorPatterns() {
		t.Error("want HasErrorPatterns()=true for a route with a declared ErrorPattern")
	}

	b := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	noPatterns, err := reqreply.NewRoute[computeReq, computeResp]("observe/no-patterns-2", reqCodec, respCodec).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if noPatterns.HasErrorPatterns() {
		t.Error("want HasErrorPatterns()=false for a route with no declared ErrorPattern")
	}
}
