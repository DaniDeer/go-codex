package mqtt

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"time"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"

	"github.com/DaniDeer/go-codex/stats"
)

// mqttReturnCodeNames maps MQTT 3.1.1's small, fixed CONNACK return-code
// enum (0-5, per the spec) to its human-readable name — unlike MQTT5's
// open-ended reason-code space (see [mqtt5.ConnectError.ReasonCode]), v3
// has exactly six defined codes and no equivalent of MQTT5's
// ConnackProperties.ReasonString.
var mqttReturnCodeNames = map[byte]string{
	0: "Accepted",
	1: "Unacceptable protocol version",
	2: "Identifier rejected",
	3: "Server unavailable",
	4: "Bad username or password",
	5: "Not authorized",
}

// ConnectOptions configures [Connect]'s broker-connection setup — the
// [pahomqtt.ClientOptions] fields [Connect] is a thin, faithful wrapper
// around (see [Connect]'s own doc comment for the manual pattern this
// wraps).
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
	// Observer, when set and implementing [stats.SecurityObserver], has
	// [stats.SecurityObserver.RecordSecurityRejection] called on a
	// broker-rejected CONNECT whose CONNACK return code is 4 ("Bad
	// username or password") or 5 ("Not authorized") — MQTT 3.1.1's own
	// two auth-specific codes (unlike MQTT5's open `>= 0x80` range; see
	// [mqtt5.ConnectOptions.Observer]). Mirrors [NewSecuredClient]'s
	// existing [WithObserver] pattern, extended here to the REAL CONNECT
	// handshake itself. Defaults to [stats.NoopObserver] behavior when
	// nil (never called).
	//
	// docs/design/d-0007-declarative-middleware-layering.md's Rollout Phase B
	// follow-up: closes a confirmed mqtt-vs-mqtt5 parity gap.
	Observer stats.Observer
}

// recordConnectSecurityRejection reports a broker-rejected CONNECT to
// opts.Observer, when set and implementing [stats.SecurityObserver], and
// returnCode is one of MQTT 3.1.1's two auth-specific CONNACK return codes:
// 4 ("Bad username or password") or 5 ("Not authorized").
func recordConnectSecurityRejection(opts ConnectOptions, returnCode byte) {
	if returnCode != 4 && returnCode != 5 {
		return
	}
	if opts.Observer == nil {
		return
	}
	if secObs, ok := opts.Observer.(stats.SecurityObserver); ok {
		secObs.RecordSecurityRejection("connect", "broker-credentials")
	}
}

// Connect dials brokerURL (a full broker URL with scheme — e.g.
// "tcp://localhost:1883" or "ssl://broker.example.com:8883" — matching
// [pahomqtt.ClientOptions.AddBroker]'s own convention, unlike
// [mqtt5.Connect]'s bare "host:port" address) and performs the full paho
// broker-connection handshake: build the [pahomqtt.ClientOptions], construct
// the [pahomqtt.Client] via [pahomqtt.NewClient], then call
// [pahomqtt.Client.Connect] and wait for the result — the exact manual
// pattern paho.mqtt.golang callers write by hand today, now wrapped as a
// single call:
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
//	if err := eventsClient.Attach(mqtt.NewTransport(mqtt.TransportOptions{Client: client})); err != nil { /* handle */ }
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
		connErr := ConnectError{Op: "connect", Err: err}
		// Populate the structured CONNACK return code when the broker
		// actually sent one — previously discarded, only the generic
		// wrapped error string survived (docs/roadmap/
		// declarative-middleware-layering.md's Rollout Phase B follow-up,
		// mirroring mqtt5's own equivalent fix).
		if ct, ok := token.(*pahomqtt.ConnectToken); ok {
			connErr.ReturnCode = ct.ReturnCode()
			recordConnectSecurityRejection(opts, ct.ReturnCode())
		}
		return nil, connErr
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
	// ReturnCode is the CONNACK return code (0-5, per MQTT 3.1.1's small
	// fixed enum — e.g. 4 "Bad username or password", 5 "Not authorized")
	// when Op=="connect" and a CONNACK was actually received from the
	// broker — populated from [pahomqtt.ConnectToken.ReturnCode], which
	// was previously discarded. Zero value when no CONNACK was ever
	// received, OR when the broker accepted the connection (0 is also
	// "Accepted") — callers must not treat a zero ReturnCode alone as
	// evidence of failure, only [Err] indicates that.
	//
	// docs/design/d-0007-declarative-middleware-layering.md's Rollout Phase B
	// follow-up: closes a confirmed mqtt-vs-mqtt5 parity gap. Unlike
	// [mqtt5.ConnectError], there is no ReasonString equivalent — MQTT
	// 3.1.1's CONNACK carries no properties.
	ReturnCode byte
}

func (e ConnectError) Error() string {
	if name, ok := mqttReturnCodeNames[e.ReturnCode]; ok && e.ReturnCode != 0 {
		return fmt.Sprintf("mqtt connect %s: %v (return code %d: %s)", e.Op, e.Err, e.ReturnCode, name)
	}
	return fmt.Sprintf("mqtt connect %s: %v", e.Op, e.Err)
}

// Unwrap allows [errors.Is] and [errors.As] to traverse the underlying error.
func (e ConnectError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e ConnectError) LogValue() slog.Value {
	attrs := []slog.Attr{
		slog.String("op", e.Op),
		slog.Any("err", e.Err),
	}
	if e.ReturnCode != 0 {
		attrs = append(attrs, slog.Int("return_code", int(e.ReturnCode)))
	}
	return slog.GroupValue(attrs...)
}
