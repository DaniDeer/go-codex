package mqtt

import (
	"context"
	"testing"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/ports"
	"github.com/DaniDeer/go-codex/stats"
	gstream "github.com/DaniDeer/go-codex/stream"
)

// This file tests H2's fix (session review round-5 finding):
// PublishAdapter.Activate's handleUpstreamError closure previously
// hand-rolled its own events.ErrorChannel dispatch via the bare
// handle.ErrorResponseFor, silently skipping stats.ErrorPatternObserver
// observability that every other Category-A dispatch site (subscribe
// side, publish()'s own internal error paths) already reports. This
// verifies a declared ErrorChannel match on an UPSTREAM pipeline error
// (not a publish-call failure) now fires RecordErrorPatternMatch, via
// the shared tryPublishErrorChannel helper.

type mqttErrorPatternObserverSpy struct {
	stats.NoopObserver
	matches int
}

func (s *mqttErrorPatternObserverSpy) RecordErrorPatternMatch(_, _, _ string) {
	s.matches++
}

func TestPublishAdapter_UpstreamError_RecordsErrorPatternMatch(t *testing.T) {
	ctx := context.Background()
	client := &mockClient{token: newCompletedToken(nil)}

	b := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	handle, err := events.NewChannel[userEvent]("user/upstream-observer", userEventCodec,
		events.ErrorChannel[userValidationErr, userErrPayload](
			"user/upstream-observer/errors", userErrPayloadCodec,
			func(e userValidationErr) (userErrPayload, error) {
				return userErrPayload{Code: "validation", Message: e.msg}, nil
			},
		),
	).WithPublish(events.Publish{Summary: "test"}).Handle(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	errCh := make(chan error, 1)
	valCh := make(chan userEvent)
	errCh <- userValidationErr{msg: "out of range"}
	close(errCh)
	close(valCh)
	src := gstream.Stream[userEvent]{Values: valCh, Errors: errCh}

	spy := &mqttErrorPatternObserverSpy{}
	p, perr := ports.NewSinkPort[userEvent]("test", userEventCodec, ports.PortOptions{Buffer: 4})
	if perr != nil {
		t.Fatalf("construct port: %v", perr)
	}
	p.Bind(ctx, PublishAdapter(client, handle, format.JSON(userEventCodec),
		MQTTDrainPublishOptions{Observer: spy}))
	p.Feed(ctx, src)

	if spy.matches != 1 {
		t.Fatalf("want 1 RecordErrorPatternMatch call for an upstream pipeline error, got %d", spy.matches)
	}
}
