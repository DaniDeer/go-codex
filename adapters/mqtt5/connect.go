package mqtt5

import (
	"context"
	"crypto/tls"
	"net"

	pahomqtt5 "github.com/eclipse/paho.golang/paho"
)

// ConnectOptions configures [Connect]'s broker-connection setup: the CONNECT
// packet fields ([pahomqtt5.Connect]'s KeepAlive/CleanStart/Username/Password
// verbatim, since [Connect] is a thin, faithful wrapper around
// paho.golang's own client construction — see examples/adapters-mqtt5's
// package doc comment for the manual pattern this wraps) plus an optional
// TLS config for a secured broker connection.
type ConnectOptions struct {
	// ClientID identifies this connection to the broker. Empty is valid
	// (the broker assigns one) but disables session persistence across
	// reconnects.
	ClientID string
	// Username, when non-empty, is sent on the CONNECT packet.
	Username string
	// Password, when non-empty, is sent on the CONNECT packet alongside
	// Username.
	Password string
	// KeepAlive is the MQTT keep-alive interval in seconds — [pahomqtt5.Connect]'s
	// own KeepAlive field, verbatim.
	KeepAlive uint16
	// CleanStart, when true, discards any prior session state on connect.
	CleanStart bool
	// TLS, when non-nil, connects over TLS ([tls.Dial]) instead of a plain
	// TCP socket ([net.Dial]).
	TLS *tls.Config
}

// Connect dials brokerURL (a bare "host:port" address — e.g.
// "localhost:1883" or "broker.example.com:8883" for TLS — NOT a URL with a
// scheme prefix, matching [net.Dial]'s own address convention) and performs
// the full paho.golang broker-connection handshake: build the
// [pahomqtt5.ClientConfig] (Conn + a fresh [pahomqtt5.NewStandardRouter]),
// construct the [*pahomqtt5.Client] via [pahomqtt5.NewClient], then send
// the CONNECT packet via opts' fields — the exact manual pattern
// examples/adapters-mqtt5's package doc comment documents today, now
// wrapped as a single call:
//
//	conn, _ := net.Dial("tcp", "localhost:1883")
//	router := paho.NewStandardRouter()
//	client := paho.NewClient(paho.ClientConfig{Conn: conn, Router: router})
//	client.Connect(ctx, &paho.Connect{ClientID: "my-service", CleanStart: true})
//
// Connect is ADDITIVE — [Attach] (which takes an already-connected
// MQTTClient/MQTTRouter pair) remains available, unchanged, for callers
// who want to build their own [*pahomqtt5.Client] with options this
// wrapper does not expose (custom [pahomqtt5.ClientConfig] fields such as
// OnClientError, EnableManualAcknowledgment, a non-default Router, etc.).
//
//	client, router, err := mqtt5.Connect(ctx, "localhost:1883", mqtt5.ConnectOptions{
//	    ClientID: "my-service", CleanStart: true,
//	})
//	if err != nil { /* handle */ }
//	if err := mqtt5.Attach(eventsClient, client, router); err != nil { /* handle */ }
//
// On dial or CONNECT failure, returns a [ConnectError] wrapping the
// underlying error — the connection, if partially established, is closed
// before returning.
func Connect(ctx context.Context, brokerURL string, opts ConnectOptions) (MQTTClient, MQTTRouter, error) {
	var conn net.Conn
	var err error
	if opts.TLS != nil {
		dialer := &tls.Dialer{Config: opts.TLS}
		conn, err = dialer.DialContext(ctx, "tcp", brokerURL)
	} else {
		var d net.Dialer
		conn, err = d.DialContext(ctx, "tcp", brokerURL)
	}
	if err != nil {
		return nil, nil, ConnectError{Op: "dial", Err: err}
	}

	router := pahomqtt5.NewStandardRouter()
	client := pahomqtt5.NewClient(pahomqtt5.ClientConfig{
		ClientID: opts.ClientID,
		Conn:     conn,
		Router:   router,
	})

	connectPacket := &pahomqtt5.Connect{
		ClientID:   opts.ClientID,
		KeepAlive:  opts.KeepAlive,
		CleanStart: opts.CleanStart,
	}
	if opts.Username != "" {
		connectPacket.Username = opts.Username
		connectPacket.UsernameFlag = true
	}
	if opts.Password != "" {
		connectPacket.Password = []byte(opts.Password)
		connectPacket.PasswordFlag = true
	}

	if _, err := client.Connect(ctx, connectPacket); err != nil {
		_ = conn.Close()
		return nil, nil, ConnectError{Op: "connect", Err: err}
	}
	return client, router, nil
}
