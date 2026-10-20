package mcp_test

import (
	"context"
	"errors"
	"testing"

	apimcp "github.com/DaniDeer/go-codex/api/mcp"
	"github.com/DaniDeer/go-codex/stats"
)

// This file closes docs/design/d-0005-error-handling.md's Topic 5
// cross-reference for api/mcp (deep-dive-bug-hunt, Phase B re-review of
// the ErrorPattern round) — mirrors api/reqreply/error_pattern_observability_test.go's
// structure exactly.

type mcpErrorPatternObserverSpy struct {
	stats.NoopObserver
	matches, misses int
	lastLocation    string
	lastCode        string
	lastAction      string
	lastMissLoc     string
	tags            int
	lastTagValue    string
}

func (s *mcpErrorPatternObserverSpy) RecordErrorPatternMatch(location, code, action string) {
	s.matches++
	s.lastLocation, s.lastCode, s.lastAction = location, code, action
}

func (s *mcpErrorPatternObserverSpy) RecordErrorPatternMiss(location string) {
	s.misses++
	s.lastMissLoc = location
}

func (s *mcpErrorPatternObserverSpy) TagSpan(_ context.Context, _, value string) {
	s.tags++
	s.lastTagValue = value
}

type observeMCPTestErr struct{ msg string }

func (e observeMCPTestErr) Error() string { return e.msg }

func newObserveTestToolHandle(t *testing.T) *apimcp.ToolHandle[calcInput, calcOutput] {
	t.Helper()
	b := apimcp.NewBuilder(apimcp.Info{Name: "test", Version: "1.0.0"})
	h, err := apimcp.NewTool[calcInput, calcOutput]("observe-errors", calcInputCodec, calcOutputCodec,
		apimcp.ErrorPattern[observeMCPTestErr, calcOutput](calcOutputCodec,
			func(e observeMCPTestErr) (calcOutput, error) {
				return calcOutput{Result: -1}, nil
			},
		),
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	return h
}

func TestObserveErrorResponseFor_RecordsMatch(t *testing.T) {
	h := newObserveTestToolHandle(t)
	spy := &mcpErrorPatternObserverSpy{}
	_, matched, applyErr := h.ObserveErrorResponseFor(context.Background(), spy, observeMCPTestErr{msg: "boom"})
	if applyErr != nil {
		t.Fatalf("applyErr: %v", applyErr)
	}
	if !matched {
		t.Fatal("want matched=true")
	}
	if spy.matches != 1 {
		t.Fatalf("want 1 RecordErrorPatternMatch call, got %d", spy.matches)
	}
	if spy.misses != 0 {
		t.Errorf("want 0 RecordErrorPatternMiss calls on a match, got %d", spy.misses)
	}
	if spy.lastCode == "" {
		t.Error("want a non-empty code derived from the matched payload type")
	}
}

func TestObserveErrorResponseFor_RecordsMiss_OnlyWhenPatternsDeclared(t *testing.T) {
	h := newObserveTestToolHandle(t)
	spy := &mcpErrorPatternObserverSpy{}
	_, matched, _ := h.ObserveErrorResponseFor(context.Background(), spy, errors.New("unrelated"))
	if matched {
		t.Fatal("want matched=false")
	}
	if spy.misses != 1 {
		t.Fatalf("want 1 RecordErrorPatternMiss call, got %d", spy.misses)
	}

	// Negative case: a tool with NO declared ErrorPattern never calls
	// RecordErrorPatternMiss at all.
	b := apimcp.NewBuilder(apimcp.Info{Name: "test", Version: "1.0.0"})
	noPatterns, err := apimcp.NewTool[calcInput, calcOutput]("no-patterns", calcInputCodec, calcOutputCodec).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	spy2 := &mcpErrorPatternObserverSpy{}
	_, matched2, _ := noPatterns.ObserveErrorResponseFor(context.Background(), spy2, errors.New("unrelated"))
	if matched2 {
		t.Fatal("want matched=false")
	}
	if spy2.misses != 0 {
		t.Errorf("want 0 RecordErrorPatternMiss calls when no patterns declared, got %d", spy2.misses)
	}
}

func TestObserveErrorResponseFor_TagsSpan_OnMatch(t *testing.T) {
	h := newObserveTestToolHandle(t)
	spy := &mcpErrorPatternObserverSpy{}
	_, matched, _ := h.ObserveErrorResponseFor(context.Background(), spy, observeMCPTestErr{msg: "boom"})
	if !matched {
		t.Fatal("want matched=true")
	}
	if spy.tags != 1 {
		t.Fatalf("want 1 TagSpan call, got %d", spy.tags)
	}
	if spy.lastTagValue == "" {
		t.Error("want a non-empty tag value")
	}
}

func TestObserveErrorResponseFor_PlainObserver_NoPanic(t *testing.T) {
	h := newObserveTestToolHandle(t)
	plain := stats.NoopObserver{}
	if _, _, err := h.ObserveErrorResponseFor(context.Background(), plain, observeMCPTestErr{msg: "boom"}); err != nil {
		t.Fatalf("unexpected applyErr: %v", err)
	}
	if _, _, err := h.ObserveErrorResponseFor(context.Background(), plain, errors.New("unrelated")); err != nil {
		t.Fatalf("unexpected applyErr: %v", err)
	}
}

func TestToolHandle_HasErrorPatterns(t *testing.T) {
	h := newObserveTestToolHandle(t)
	if !h.HasErrorPatterns() {
		t.Error("want HasErrorPatterns()=true for a tool with a declared ErrorPattern")
	}

	b := apimcp.NewBuilder(apimcp.Info{Name: "test", Version: "1.0.0"})
	noPatterns, err := apimcp.NewTool[calcInput, calcOutput]("no-patterns-2", calcInputCodec, calcOutputCodec).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if noPatterns.HasErrorPatterns() {
		t.Error("want HasErrorPatterns()=false for a tool with no declared ErrorPattern")
	}
}
