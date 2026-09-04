package mqtt

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"time"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"
)

// ConnectOptions configures [Connect]'s broker-connection setup — the
// [pahomqtt.ClientOptions] fields [Connect] is a thin, faithful wrapper
// around (see examples/adapters-mqtt's package doc comment for the manual
// pattern this wraps).
type ConnectOptions struct {
	// ClientID identifies this connection to the broker. Empty is valid
	// (paho generates one) but disables session persistence across
	// reconnects.
	ClientID string
	// Username, when non-empty, is sent on the CONNECT packet.
	Username string
	// Password, when non-empty, is sent on the CONNECT packet alongside
	// Username.
	Password string
	// KeepAlive is the MQTT keep-alive interval — [pahomqtt.ClientOptions.SetKeepAlive]'s
	// own duration, verbatim. Zero uses paho's own default.
	KeepAlive time.Duration
	// CleanSession, when true (the default, matching paho's own default),
	// discards any prior session state on connect.
	CleanSession bool
	// TLS, when non-nil, connects over TLS instead of a plain TCP socket —
	// passed to [pahomqtt.ClientOptions.SetTLSConfig].
	TLS *tls.Config
}

// Connect dials brokerURL (a full broker URL with scheme — e.g.
// "tcp://localhost:1883" or "ssl://broker.example.com:8883" — matching
// [pahomqtt.ClientOptions.AddBroker]'s own convention, unlike
// [mqtt5.Connect]'s bare "host:port" address) and performs the full paho
// broker-connection handshake: build the [pahomqtt.ClientOptions], construct
// the [pahomqtt.Client] via [pahomqtt.NewClient], then call
// [pahomqtt.Client.Connect] and wait for the result — the exact manual
// pattern examples/adapters-mqtt's package doc comment documents today, now
// wrapped as a single call:
//
//	opts := pahomqtt.NewClientOptions().AddBroker("tcp://localhost:1883").SetClientID("my-service")
//	client := pahomqtt.NewClient(opts)
//	token := client.Connect()
//	token.Wait()
//
// Connect is ADDITIVE — [Attach] (which takes an already-connected
// [pahomqtt.Client]) remains available, unchanged, for callers who want to
// build their own [pahomqtt.Client] with options this wrapper does not
// expose (custom [pahomqtt.ClientOptions] fields such as OnConnectionLost,
// AutoReconnect, a custom Store, etc.).
//
//	client, err := mqtt.Connect(ctx, "tcp://localhost:1883", mqtt.ConnectOptions{
//	    ClientID: "my-service", CleanSession: true,
//	})
//	if err != nil { /* handle */ }
//	if err := mqtt.Attach(eventsClient, client); err != nil { /* handle */ }
//
// On CONNECT failure (including ctx cancellation before the broker
// acknowledges), returns a [ConnectError] wrapping the underlying error —
// the client, if partially connected, is disconnected before returning.
func Connect(ctx context.Context, brokerURL string, opts ConnectOptions) (pahomqtt.Client, error) {
	clientOpts := pahomqtt.NewClientOptions().
		AddBroker(brokerURL).
		SetClientID(opts.ClientID).
		SetCleanSession(opts.CleanSession)
	if opts.Username != "" {
		clientOpts.SetUsername(opts.Username)
	}
	if opts.Password != "" {
		clientOpts.SetPassword(opts.Password)
	}
	if opts.KeepAlive > 0 {
		clientOpts.SetKeepAlive(opts.KeepAlive)
	}
	if opts.TLS != nil {
		clientOpts.SetTLSConfig(opts.TLS)
	}

	client := pahomqtt.NewClient(clientOpts)
	token := client.Connect()

	done := make(chan struct{})
	go func() {
		token.Wait()
		close(done)
	}()

	select {
	case <-ctx.Done():
		client.Disconnect(0)
		return nil, ConnectError{Op: "connect", Err: ctx.Err()}
	case <-done:
	}

	if err := token.Error(); err != nil {
		client.Disconnect(0)
		return nil, ConnectError{Op: "connect", Err: err}
	}
	return client, nil
}

// ConnectError wraps broker-connection setup failures from [Connect] — the
// MQTT CONNECT handshake itself, or ctx cancellation before it completes.
//
//	var connErr mqtt.ConnectError
//	if errors.As(err, &connErr) {
//	    slog.Error("mqtt connect failed", "error", connErr)
//	}
type ConnectError struct {
	// Op identifies the connection step that failed — currently always
	// "connect" (paho's own client construction never fails synchronously).
	Op string
	// Err is the underlying network or protocol error.
	Err error
}

func (e ConnectError) Error() string {
	return fmt.Sprintf("mqtt connect %s: %v", e.Op, e.Err)
}

// Unwrap allows [errors.Is] and [errors.As] to traverse the underlying error.
func (e ConnectError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e ConnectError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("op", e.Op),
		slog.Any("err", e.Err),
	)
}
