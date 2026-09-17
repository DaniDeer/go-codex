package zeromq

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

type zmqErrorPatternObserverSpy struct {
	stats.NoopObserver
	matches int
}

func (s *zmqErrorPatternObserverSpy) RecordErrorPatternMatch(_, _, _ string) {
	s.matches++
}

func TestZeromqPublishAdapter_UpstreamError_RecordsErrorPatternMatch(t *testing.T) {
	ctx := context.Background()
	sock := &mockSocket{}

	b := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	handle, err := events.NewChannel[sensorReading]("sensors/upstream-observer", sensorCodec,
		events.ErrorChannel[sensorZmqValidationErr, sensorZmqErrPayload](
			"sensors/upstream-observer/errors", sensorZmqErrPayloadCodec,
			func(e sensorZmqValidationErr) (sensorZmqErrPayload, error) {
				return sensorZmqErrPayload{Code: "validation", Message: e.msg}, nil
			},
		),
	).WithPublish(events.Publish{Summary: "test"}).Handle(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	errCh := make(chan error, 1)
	valCh := make(chan sensorReading)
	errCh <- sensorZmqValidationErr{msg: "out of range"}
	close(errCh)
	close(valCh)
	src := gstream.Stream[sensorReading]{Values: valCh, Errors: errCh}

	spy := &zmqErrorPatternObserverSpy{}
	p, perr := ports.NewSinkPort[sensorReading]("test", sensorCodec, ports.PortOptions{Buffer: 4})
	if perr != nil {
		t.Fatalf("construct port: %v", perr)
	}
	p.Bind(ctx, PublishAdapter(sock, handle, format.JSON(sensorCodec),
		DrainPublishOptions{Observer: spy}))
	p.Feed(ctx, src)

	if spy.matches != 1 {
		t.Fatalf("want 1 RecordErrorPatternMatch call for an upstream pipeline error, got %d", spy.matches)
	}
}
