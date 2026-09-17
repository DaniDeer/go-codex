package events_test

import (
	"context"
	"errors"
	"testing"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/stats"
)

// This file tests F4's fix (session review finding): the roadmap's own
// test plan calls for ObserveErrorResponseFor/HasErrorPatterns/
// ErrorPatternObserver tests across api/rest, api/events, api/reqreply —
// only api/rest had them before this file. Mirrors
// api/rest/observability_test.go's structure exactly.

type eventsErrorPatternObserverSpy struct {
	stats.NoopObserver
	matches, misses int
	lastLocation    string
	lastTopic       string
	lastAction      string
	lastMissLoc     string
	tags            int
	lastTagValue    string
}

func (s *eventsErrorPatternObserverSpy) RecordErrorPatternMatch(location, code, action string) {
	s.matches++
	s.lastLocation, s.lastTopic, s.lastAction = location, code, action
}

func (s *eventsErrorPatternObserverSpy) RecordErrorPatternMiss(location string) {
	s.misses++
	s.lastMissLoc = location
}

func (s *eventsErrorPatternObserverSpy) TagSpan(_ context.Context, _, value string) {
	s.tags++
	s.lastTagValue = value
}

type observeTestErr struct{ msg string }

func (e observeTestErr) Error() string { return e.msg }

func newObserveTestHandle(t *testing.T) *events.ChannelHandle[userEvent] {
	t.Helper()
	b := events.NewClient(events.WithInfo(testInfo))
	h, err := events.NewChannel[userEvent]("observe/errors", userEventCodec,
		events.ErrorChannel[observeTestErr, userEvent](
			"observe/errors/errors",
			userEventCodec,
			func(e observeTestErr) (userEvent, error) {
				return userEvent{ID: "err", Name: e.msg}, nil
			},
		),
	).WithSubscribe(events.Subscribe{Summary: "test"}).Handle(b)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	return h
}

func TestObserveErrorResponseFor_RecordsMatch(t *testing.T) {
	h := newObserveTestHandle(t)
	spy := &eventsErrorPatternObserverSpy{}
	resp, matched, applyErr := h.ObserveErrorResponseFor(context.Background(), spy, observeTestErr{msg: "boom"})
	if applyErr != nil {
		t.Fatalf("applyErr: %v", applyErr)
	}
	if !matched {
		t.Fatal("want matched=true")
	}
	if resp.Topic != "observe/errors/errors" {
		t.Errorf("want Topic=observe/errors/errors, got %q", resp.Topic)
	}
	if spy.matches != 1 {
		t.Fatalf("want 1 RecordErrorPatternMatch call, got %d", spy.matches)
	}
	if spy.misses != 0 {
		t.Errorf("want 0 RecordErrorPatternMiss calls on a match, got %d", spy.misses)
	}
}

func TestObserveErrorResponseFor_RecordsMiss_OnlyWhenPatternsDeclared(t *testing.T) {
	h := newObserveTestHandle(t)
	spy := &eventsErrorPatternObserverSpy{}
	_, matched, _ := h.ObserveErrorResponseFor(context.Background(), spy, errors.New("unrelated"))
	if matched {
		t.Fatal("want matched=false")
	}
	if spy.misses != 1 {
		t.Fatalf("want 1 RecordErrorPatternMiss call, got %d", spy.misses)
	}

	// Negative case: a channel with NO declared ErrorChannel never calls
	// RecordErrorPatternMiss at all.
	b := events.NewClient(events.WithInfo(testInfo))
	noPatterns, err := events.NewChannel[userEvent]("observe/no-patterns", userEventCodec).
		WithSubscribe(events.Subscribe{Summary: "test"}).Handle(b)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	spy2 := &eventsErrorPatternObserverSpy{}
	_, matched2, _ := noPatterns.ObserveErrorResponseFor(context.Background(), spy2, errors.New("unrelated"))
	if matched2 {
		t.Fatal("want matched=false")
	}
	if spy2.misses != 0 {
		t.Errorf("want 0 RecordErrorPatternMiss calls when no patterns declared, got %d", spy2.misses)
	}
}

func TestObserveErrorResponseFor_TagsSpan_OnMatch(t *testing.T) {
	h := newObserveTestHandle(t)
	spy := &eventsErrorPatternObserverSpy{}
	_, matched, _ := h.ObserveErrorResponseFor(context.Background(), spy, observeTestErr{msg: "boom"})
	if !matched {
		t.Fatal("want matched=true")
	}
	if spy.tags != 1 {
		t.Fatalf("want 1 TagSpan call, got %d", spy.tags)
	}
	if spy.lastTagValue != "observe/errors/errors" {
		t.Errorf("want tag value=observe/errors/errors, got %q", spy.lastTagValue)
	}
}

func TestObserveErrorResponseFor_PlainObserver_NoPanic(t *testing.T) {
	h := newObserveTestHandle(t)
	plain := stats.NoopObserver{}
	if _, _, err := h.ObserveErrorResponseFor(context.Background(), plain, observeTestErr{msg: "boom"}); err != nil {
		t.Fatalf("unexpected applyErr: %v", err)
	}
	if _, _, err := h.ObserveErrorResponseFor(context.Background(), plain, errors.New("unrelated")); err != nil {
		t.Fatalf("unexpected applyErr: %v", err)
	}
}

func TestChannelHandle_HasErrorPatterns(t *testing.T) {
	h := newObserveTestHandle(t)
	if !h.HasErrorPatterns() {
		t.Error("want HasErrorPatterns()=true for a channel with a declared ErrorChannel")
	}

	b := events.NewClient(events.WithInfo(testInfo))
	noPatterns, err := events.NewChannel[userEvent]("observe/no-patterns-2", userEventCodec).
		WithSubscribe(events.Subscribe{Summary: "test"}).Handle(b)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if noPatterns.HasErrorPatterns() {
		t.Error("want HasErrorPatterns()=false for a channel with no declared ErrorChannel")
	}
}
