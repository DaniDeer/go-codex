package mqtt

import (
	"context"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/format"
)

// NewPublishTransport builds an [events.PublishTransport][T] bound to
// client — the GENERIC, per-T constructor Decision 7
// (docs/roadmap/pubsub-workflow-simplification.md) uses to satisfy
// api/events' new inverted, no-*Client-needed [events.PublishHandle] call
// surface, mirroring [ports.SourceAdapter]/[ports.SinkAdapter]'s own
// generic-constructor convention exactly (fully type-safe, no reflection
// — unlike [events.Transport], which is a method-based, `any`-typed
// interface out of structural necessity, not stylistic choice).
//
//	transport := mqtt.NewPublishTransport[Reading](client, 1, false, mqtt.PublishOptions[Reading]{})
//	err := events.PublishHandle(ctx, sensorChannel.WithPublish(events.Publish{}), transport, reading)
func NewPublishTransport[T any](client pahomqtt.Client, qos byte, retained bool, opts PublishOptions[T], formats ...format.Format[T]) events.PublishTransport[T] {
	return &publishTransport[T]{client: client, qos: qos, retained: retained, opts: opts, formats: formats}
}

type publishTransport[T any] struct {
	client   pahomqtt.Client
	qos      byte
	retained bool
	opts     PublishOptions[T]
	formats  []format.Format[T]
}

// Publish implements [events.PublishTransport]. Delegates to the same
// internal logic [publishHandle] (formerly the public `PublishHandle`)
// always has — merge-field topic-var derivation, security/credential
// resolution, Observer/tracing, general-purpose middleware wrapping —
// unchanged.
func (t *publishTransport[T]) Publish(ctx context.Context, handle *events.ChannelHandle[T], msg T) error {
	return publishHandle(ctx, t.client, handle, t.qos, t.retained, msg, t.opts, t.formats...)
}

// AdapterName implements [events.PublishTransport].
func (t *publishTransport[T]) AdapterName() string { return "mqtt.PublishTransport" }

var _ events.PublishTransport[struct{}] = (*publishTransport[struct{}])(nil)

// NewSubscribeTransport builds an [events.SubscribeTransport][T] bound to
// client — the GENERIC, per-T constructor Decision 7 uses to satisfy
// api/events' new inverted, no-*Client-needed [events.SubscribeHandle]
// call surface. See [NewPublishTransport]'s doc comment for the full
// design rationale.
//
//	transport := mqtt.NewSubscribeTransport[Reading](client, 1, mqtt.SubscribeOptions{})
//	err := events.SubscribeHandle(ctx, sensorChannel.WithSubscribe(events.Subscribe{}), transport,
//	    func(ctx context.Context, r Reading) error { ... })
func NewSubscribeTransport[T any](client pahomqtt.Client, qos byte, opts SubscribeOptions, formats ...format.Format[T]) events.SubscribeTransport[T] {
	return &subscribeTransport[T]{client: client, qos: qos, opts: opts, formats: formats}
}

type subscribeTransport[T any] struct {
	client  pahomqtt.Client
	qos     byte
	opts    SubscribeOptions
	formats []format.Format[T]
}

// Subscribe implements [events.SubscribeTransport]. Unlike zeromq/mqtt5's
// own SubscribeTransport implementations — where the underlying primitive
// itself blocks in a receive loop until ctx is cancelled — mqtt v3's
// [subscribeHandle] does NOT block: it only registers the subscription
// with the broker (via [pahomqtt.Client.Subscribe]) and waits for the
// broker's SUBACK, because paho's own [pahomqtt.Client] dispatches
// incoming messages on its own internal goroutines, not from a call that
// this package controls. Subscribe therefore replicates the exact
// two-step "register once via [subscribeHandle], then block on
// ctx.Done()" pattern [caller.ServeSubscribers] already uses: register
// the subscription (returning immediately if registration itself fails),
// then block until ctx is cancelled before returning nil — satisfying
// [events.SubscribeTransport]'s documented "runs a receive loop... until
// ctx is cancelled" contract even though the receiving itself happens on
// paho's own goroutines in the background.
func (t *subscribeTransport[T]) Subscribe(ctx context.Context, handle *events.ChannelHandle[T], fn func(context.Context, T) error) error {
	if err := subscribeHandle(ctx, t.client, handle, t.qos, fn, t.opts, t.formats...); err != nil {
		return err
	}
	<-ctx.Done()
	return nil
}

// AdapterName implements [events.SubscribeTransport].
func (t *subscribeTransport[T]) AdapterName() string { return "mqtt.SubscribeTransport" }

var _ events.SubscribeTransport[struct{}] = (*subscribeTransport[struct{}])(nil)
