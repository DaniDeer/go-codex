// Package client builds events.Client values wired to each adapter used by
// this example — a SEPARATE connection sharing the SAME in-process mock
// broker/router/socket a *broker package's own Build already attached,
// mirroring examples/reqreply-api/client's identical separation of
// concerns. events.Client is symmetric (one value handles BOTH publish
// AND subscribe, unlike reqreply's genuinely asymmetric Server/Client
// pair), so these constructors exist purely to simulate a DIFFERENT
// device/connection talking to the SAME broker — e.g. a publisher client
// distinct from the broker-side subscriber built in mqtt5broker.Build.
package client

import (
	adaptermqtt "github.com/DaniDeer/go-codex/adapters/mqtt"
	mqtt5adapter "github.com/DaniDeer/go-codex/adapters/mqtt5"
	"github.com/DaniDeer/go-codex/adapters/zeromq"
	"github.com/DaniDeer/go-codex/api/events"
	pahomqtt "github.com/eclipse/paho.mqtt.golang"
)

// BuildMQTT5 returns an events.Client attached to broker+router — the
// SAME in-process mock broker/router mqtt5broker.Build used to construct
// the server side, so messages published through this client are routed
// to that server via the shared broker.
func BuildMQTT5(info events.Info, broker mqtt5adapter.MQTTClient, router mqtt5adapter.MQTTRouter) (*events.Client, error) {
	c := events.NewClient(events.WithInfo(info))
	if err := mqtt5adapter.Attach(c, broker, router); err != nil {
		return nil, err
	}
	return c, nil
}

// BuildMQTT returns an events.Client attached to mqttClient — the SAME
// in-process mock client mqttbroker.Build used to construct the server
// side.
func BuildMQTT(info events.Info, mqttClient pahomqtt.Client) (*events.Client, error) {
	c := events.NewClient(events.WithInfo(info))
	if err := adaptermqtt.Attach(c, mqttClient); err != nil {
		return nil, err
	}
	return c, nil
}

// BuildZeroMQ returns an events.Client attached to sock — pass
// zeromqbroker.Built's own PubSocket/SubSocket to build a connection on
// either end of the shared in-process pipe.
func BuildZeroMQ(info events.Info, sock zeromq.FramedSocket) (*events.Client, error) {
	c := events.NewClient(events.WithInfo(info))
	if err := zeromq.Attach(c, sock); err != nil {
		return nil, err
	}
	return c, nil
}
