package mqtt5

import (
	"context"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/ports"
	"github.com/DaniDeer/go-codex/stats"
	gstream "github.com/DaniDeer/go-codex/stream"
	pahomqtt5 "github.com/eclipse/paho.golang/paho"
)

// This file tests F4's fix (session review finding): the roadmap's own
// test plan calls for an adapter-level test proving
// RecordErrorPatternMatch/RecordErrorPatternMiss actually fire during
// REAL dispatch (not just the core-layer ObserveErrorResponseFor unit
// tests in api/events/api/reqreply) — previously no such test existed
// anywhere.

type mqtt5ErrorPatternObserverSpy struct {
	stats.NoopObserver
	matches, misses int
	lastLocation    string
}

func (s *mqtt5ErrorPatternObserverSpy) RecordErrorPatternMatch(location, _, _ string) {
	s.matches++
	s.lastLocation = location
}

func (s *mqtt5ErrorPatternObserverSpy) RecordErrorPatternMiss(location string) {
	s.misses++
	s.lastLocation = location
}

func TestEventsSubscribe_RealDispatch_RecordsErrorPatternMatch(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()

	b := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	handle, err := events.NewChannel[sensorReading]("sensors/observer-match", sensorCodec,
		events.ErrorChannel[codex.ValidationErrors, sensorErrPayload](
			"sensors/observer-match/errors", sensorErrPayloadCodec,
			func(e codex.ValidationErrors) (sensorErrPayload, error) {
				return sensorErrPayload{Code: "decode_error", Message: "bad payload"}, nil
			},
		),
	).WithSubscribe(events.Subscribe{Summary: "test"}).Handle(b)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	spy := &mqtt5ErrorPatternObserverSpy{}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_ = subscribeWithHandle(ctx, client, router, handle, 1,
		func(_ context.Context, _ sensorReading) error { return nil },
		SubscribeOptions{Observer: spy})

	router.dispatch("sensors/observer-match", &pahomqtt5.Publish{
		Topic: "sensors/observer-match", Payload: []byte(`{}`), // missing required fields
	})

	if spy.matches != 1 {
		t.Fatalf("want 1 RecordErrorPatternMatch call during real dispatch, got %d", spy.matches)
	}
	if spy.misses != 0 {
		t.Errorf("want 0 RecordErrorPatternMiss calls on a match, got %d", spy.misses)
	}
}

func TestEventsSubscribe_RealDispatch_RecordsErrorPatternMiss(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()

	b := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	handle, err := events.NewChannel[sensorReading]("sensors/observer-miss", sensorCodec,
		events.ErrorChannel[unrelatedObserverTestErr, sensorErrPayload](
			"sensors/observer-miss/errors", sensorErrPayloadCodec,
			func(e unrelatedObserverTestErr) (sensorErrPayload, error) {
				return sensorErrPayload{Code: "unrelated", Message: "n/a"}, nil
			},
		),
	).WithSubscribe(events.Subscribe{Summary: "test"}).Handle(b)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	spy := &mqtt5ErrorPatternObserverSpy{}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	// The declared ErrorChannel matches a DIFFERENT error type than the
	// real decode failure produces (codex.ValidationErrors), so this is
	// a genuine miss, not an absence of any declared pattern at all.
	_ = subscribeWithHandle(ctx, client, router, handle, 1,
		func(_ context.Context, _ sensorReading) error { return nil },
		SubscribeOptions{Observer: spy})

	router.dispatch("sensors/observer-miss", &pahomqtt5.Publish{
		Topic: "sensors/observer-miss", Payload: []byte(`{}`), // missing required fields
	})

	if spy.misses != 1 {
		t.Fatalf("want 1 RecordErrorPatternMiss call during real dispatch, got %d", spy.misses)
	}
	if spy.matches != 0 {
		t.Errorf("want 0 RecordErrorPatternMatch calls on a miss, got %d", spy.matches)
	}
}

type unrelatedObserverTestErr struct{}

func (unrelatedObserverTestErr) Error() string { return "unrelated" }

func TestReqReplyServe_RealDispatch_RecordsErrorPatternMatch(t *testing.T) {
	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	handler := func(_ context.Context, _ computeReq) (computeResp, error) {
		return computeResp{}, serveConflictErr{msg: "duplicate"}
	}
	epRoute := reqreply.NewRoute[computeReq, computeResp]("compute/observer-match", computeReqCodec, computeRespCodec,
		reqreply.ErrorPattern[serveConflictErr, serveErrPayload](serveErrPayloadCodec,
			func(e serveConflictErr) (serveErrPayload, error) {
				return serveErrPayload{Code: "conflict", Message: e.msg}, nil
			},
		),
	)
	if _, err := epRoute.WithHandler(handler).Register(server); err != nil {
		t.Fatalf("Register: %v", err)
	}

	serverClient := &mockClient{}
	serverRouter := newMockRouter()
	spy := &mqtt5ErrorPatternObserverSpy{}
	if err := AttachServer(server, serverClient, serverRouter, ServeOptions{Observer: spy}); err != nil {
		t.Fatalf("AttachServer: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	go func() { _ = server.Serve(ctx) }()
	serverRouter.waitHandler("compute/observer-match")

	serverRouter.dispatch("compute/observer-match", &pahomqtt5.Publish{
		Topic:   "compute/observer-match",
		Payload: []byte(validComputeJSON),
		Properties: &pahomqtt5.PublishProperties{
			ResponseTopic:   "replies/client-1",
			CorrelationData: []byte("corr-1"),
		},
	})
	time.Sleep(50 * time.Millisecond)

	if spy.matches != 1 {
		t.Fatalf("want 1 RecordErrorPatternMatch call during real dispatch, got %d", spy.matches)
	}
}

// TestPublishAdapter_UpstreamError_RecordsErrorPatternMatch tests H2's fix
// (session review round-5 finding): PublishAdapter.Activate's
// handleUpstreamError closure previously hand-rolled its own ErrorChannel
// dispatch via the bare handle.ErrorResponseFor, silently skipping
// stats.ErrorPatternObserver observability that every other Category-A
// dispatch site (subscribe side, publish()'s own internal error paths)
// already reports. This verifies a declared ErrorChannel match on an
// UPSTREAM pipeline error (not a publish-call failure) now fires
// RecordErrorPatternMatch, via the shared tryPublishErrorChannel helper.
func TestPublishAdapter_UpstreamError_RecordsErrorPatternMatch(t *testing.T) {
	ctx := context.Background()
	client := &mockClient{}

	b := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	handle, err := events.NewChannel[sensorReading]("sensors/upstream-observer", sensorCodec,
		events.ErrorChannel[sensorValidationErr, sensorErrPayload](
			"sensors/upstream-observer/errors", sensorErrPayloadCodec,
			func(e sensorValidationErr) (sensorErrPayload, error) {
				return sensorErrPayload{Code: "validation", Message: e.msg}, nil
			},
		),
	).WithPublish(events.Publish{Summary: "test"}).Handle(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	errCh := make(chan error, 1)
	valCh := make(chan sensorReading)
	errCh <- sensorValidationErr{msg: "out of range"}
	close(errCh)
	close(valCh)
	src := gstream.Stream[sensorReading]{Values: valCh, Errors: errCh}

	spy := &mqtt5ErrorPatternObserverSpy{}
	p, err := ports.NewSinkPort[sensorReading]("test", sensorCodec, ports.PortOptions{Buffer: 4})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	p.Bind(ctx, PublishAdapter(client, handle, format.JSON(sensorCodec),
		MQTT5DrainPublishOptions{Observer: spy}))
	p.Feed(ctx, src)

	if spy.matches != 1 {
		t.Fatalf("want 1 RecordErrorPatternMatch call for an upstream pipeline error, got %d", spy.matches)
	}
}
