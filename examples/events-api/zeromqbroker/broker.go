// Package zeromqbroker assembles routes/+handlers/ onto an in-process
// mock ZeroMQ PUB/SUB socket pair via zeromq.NewTransport+Client.Attach — the events analogue
// of examples/reqreply-api's zeromqserver package. Demonstrates: ZeroMQ
// PUB/SUB full roundtrip and channel-BOUND security
// (auth.NewAPIKeyAuthMW) via auth.ZeromqSecurityImpl.
package zeromqbroker

import (
	"strings"
	"sync"
	"time"

	zeromqadapter "github.com/DaniDeer/go-codex/adapters/zeromq"
	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/examples/events-api/auth"
	"github.com/DaniDeer/go-codex/examples/events-api/handlers"
	"github.com/DaniDeer/go-codex/examples/events-api/routes"
)

// Built bundles a PUBLISHER-side events.Client and a SUBSCRIBER-side
// events.Client, wired together via an in-process [PipeSocket] pair (no
// real ZeroMQ broker/socket library required) — mirrors a real PUB/SUB
// topology where publisher and subscriber are separate connections.
type Built struct {
	Publisher  *events.Client
	Subscriber *events.Client
	PubSocket  *PipeSocket
	SubSocket  *PipeSocket
}

// Build attaches routes.ZeromqReadingsPub/Sub onto a fresh PipeSocket
// pair, and routes.SensorDataSub (secured, via auth.ZeromqSecurityImpl)
// onto the subscriber side.
func Build() (*Built, error) {
	pubSock, subSock := NewPipe()

	pub := events.NewClient(events.WithInfo(events.Info{
		Title:       "Sensor Network Events (ZeroMQ, publisher)",
		Version:     "1.0.0",
		Description: "Publisher-side connection for sensor readings over ZeroMQ PUB/SUB.",
	}))
	if err := pub.Attach(zeromqadapter.NewTransport(zeromqadapter.TransportOptions{Socket: pubSock})); err != nil {
		return nil, err
	}
	// routes.ZeromqReadingsChannel declares a RELATIVE topic
	// ("zeromq/readings") — Mounted under a REAL Mount
	// (events.NewRouter("sensor")), the SAME Router prefix
	// routes.SensorDataSub Mounts under below, composing back to the
	// SAME, byte-identical absolute topic ("sensor/zeromq/readings")
	// this broker has always used.
	sensorRouter := events.NewRouter("sensor")
	if _, err := routes.ZeromqReadingsPub.Handle(pub, events.WithRouter(sensorRouter)); err != nil {
		return nil, err
	}

	sub := events.NewClient(events.WithInfo(events.Info{
		Title:       "Sensor Network Events (ZeroMQ, subscriber)",
		Version:     "1.0.0",
		Description: "Subscriber-side connection for sensor readings over ZeroMQ PUB/SUB.",
	}))
	if err := sub.Attach(zeromqadapter.NewTransport(zeromqadapter.TransportOptions{Socket: subSock})); err != nil {
		return nil, err
	}
	roundtripSub := routes.ZeromqReadingsSub.WithHandler(handlers.PrintReading("zeromq"))

	// routes.SensorDataSub declares a RELATIVE topic ("data") — Mounted
	// below under a REAL docs/design/d-0008-declarative-router-groups.md
	// Mount (events.NewRouter("sensor")), composing back to the SAME,
	// byte-identical absolute topic ("sensor/data") this broker has
	// always used (mirrors mqtt5broker/broker.go's own identical Mount).
	// roundtripSub (routes.ZeromqReadingsSub) shares the SAME "sensor"
	// Mount, Grouped alongside — both leaves registered via ONE Router
	// value on this subscriber client.
	securedSub := routes.SensorDataSub.
		SubscribeBoundMW(auth.NewAPIKeyAuthMW(auth.ZeromqSecurityImpl)).
		WithHandler(handlers.PrintReading("zeromq-secured"))
	if err := sensorRouter.Route(roundtripSub).Route(securedSub).Register(sub); err != nil {
		return nil, err
	}

	return &Built{Publisher: pub, Subscriber: sub, PubSocket: pubSock, SubSocket: subSock}, nil
}

// ── in-process mock socket pair ───────────────────────────────────────────────

// PipeSocket implements [zeromqadapter.FramedSocket] as one end of an
// in-memory pipe — SendFrames on one end delivers to RecvFrames on the
// other, simulating a real PUB/SUB broker round-trip without any
// external dependency. Multiple SetSubscription calls ACCUMULATE filters
// (matches real ZMQ SUB-socket semantics — a socket can subscribe to
// several topic prefixes simultaneously), needed here since one socket
// serves multiple registered channels via ServeSubscribers's single
// shared receive loop.
type PipeSocket struct {
	ch          chan [][]byte
	mu          sync.Mutex
	subTopics   []string
	recvTimeout time.Duration
}

// NewPipe returns a connected (pub, sub) PipeSocket pair sharing one
// underlying channel — frames sent on pub are received on sub (and vice
// versa, since the channel is shared).
func NewPipe() (pub *PipeSocket, sub *PipeSocket) {
	ch := make(chan [][]byte, 16)
	return &PipeSocket{ch: ch, recvTimeout: 100 * time.Millisecond},
		&PipeSocket{ch: ch, recvTimeout: 100 * time.Millisecond}
}

func (p *PipeSocket) SendFrames(frames [][]byte) error {
	cp := make([][]byte, len(frames))
	for i, f := range frames {
		cp[i] = append([]byte{}, f...)
	}
	p.ch <- cp
	return nil
}

func (p *PipeSocket) RecvFrames() ([][]byte, error) {
	p.mu.Lock()
	topics := append([]string(nil), p.subTopics...)
	timeout := p.recvTimeout
	p.mu.Unlock()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case frames := <-p.ch:
			if p.matchesAny(topics, frames) {
				return frames, nil
			}
			// Doesn't match any of this socket's subscription filters —
			// drop and keep polling within the same deadline.
		case <-time.After(5 * time.Millisecond):
		}
	}
	return nil, zeromqadapter.ErrTimeout
}

func (p *PipeSocket) matchesAny(topics []string, frames [][]byte) bool {
	if len(topics) == 0 {
		return true // no filter registered yet -> accept everything
	}
	if len(frames) == 0 {
		return false
	}
	for _, t := range topics {
		if t == "" || strings.HasPrefix(string(frames[0]), t) {
			return true
		}
	}
	return false
}

func (p *PipeSocket) SetSubscription(topic string) error {
	p.mu.Lock()
	p.subTopics = append(p.subTopics, topic)
	p.mu.Unlock()
	return nil
}

func (p *PipeSocket) SetRecvTimeout(d time.Duration) error {
	p.mu.Lock()
	p.recvTimeout = d
	p.mu.Unlock()
	return nil
}
