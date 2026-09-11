// Package client builds reqreply.Client values wired to each adapter used
// by this example, mirroring examples/rest-api/client's identical
// separation of concerns.
package client

import (
	mqtt5adapter "github.com/DaniDeer/go-codex/adapters/mqtt5"
	"github.com/DaniDeer/go-codex/adapters/zeromq"
	"github.com/DaniDeer/go-codex/api/reqreply"
)

// BuildMQTT5 returns a reqreply.Client attached to broker+router — the SAME
// in-process mock broker/router mqtt5server.Build used to construct the
// server side, so calls made through this client are routed to that
// server via the shared broker.
func BuildMQTT5(broker mqtt5adapter.MQTTClient, router mqtt5adapter.MQTTRouter) (*reqreply.Client, error) {
	c := reqreply.NewClient()
	if err := mqtt5adapter.AttachClient(c, broker, router); err != nil {
		return nil, err
	}
	return c, nil
}

// BuildZeroMQ returns a reqreply.Client attached to sockets (REQ side) —
// the SAME topic→socket map zeromqserver.Build's ClientSockets field
// exposes.
func BuildZeroMQ(sockets map[string]zeromq.FramedSocket) (*reqreply.Client, error) {
	c := reqreply.NewClient()
	if err := zeromq.AttachClient(c, sockets); err != nil {
		return nil, err
	}
	return c, nil
}

// BuildZeroMQDealer returns a reqreply.Client attached to a single DEALER
// socket — the ROUTER/DEALER counterpart of BuildZeroMQ, for
// zeromqrouterserver's socket topology.
func BuildZeroMQDealer(topic string, sock zeromq.FramedSocket) (*reqreply.Client, error) {
	c := reqreply.NewClient()
	if err := zeromq.AttachDealerClient(c, map[string]zeromq.FramedSocket{topic: sock}); err != nil {
		return nil, err
	}
	return c, nil
}
