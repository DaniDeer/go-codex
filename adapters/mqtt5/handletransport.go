package mqtt5

import (
	"context"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/format"
)

// NewPublishTransport builds an [events.PublishTransport][T] bound to
// client — the GENERIC, per-T constructor Decision 7
// (docs/design/d-0002-pubsub-workflow-simplification.md) uses to satisfy
// api/events' new inverted, no-*Client-needed [events.PublishHandle] call
// surface, mirroring [ports.SourceAdapter]/[ports.SinkAdapter]'s own
// generic-constructor convention exactly (fully type-safe, no reflection
// — unlike [events.Transport], which is a method-based, `any`-typed
// interface out of structural necessity, not stylistic choice).
//
//	transport := mqtt5.NewPublishTransport[SensorReading](client, 1, false, mqtt5.PublishOptions[SensorReading]{})
//	err := events.PublishHandle(ctx, ReadingsChannel.WithPublish(events.Publish{}), transport, reading)
func NewPublishTransport[T any](client MQTTClient, qos byte, retained bool, opts PublishOptions[T], formats ...format.Format[T]) events.PublishTransport[T] {
	return &publishTransport[T]{client: client, qos: qos, retained: retained, opts: opts, formats: formats}
}

type publishTransport[T any] struct {
	client   MQTTClient
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
func (t *publishTransport[T]) AdapterName() string { return "mqtt5.PublishTransport" }

var _ events.PublishTransport[struct{}] = (*publishTransport[struct{}])(nil)

// NewSubscribeTransport builds an [events.SubscribeTransport][T] bound to
// client+router — the GENERIC, per-T constructor Decision 7 uses to
// satisfy api/events' new inverted, no-*Client-needed
// [events.SubscribeHandle] call surface. See [NewPublishTransport]'s doc
// comment for the full design rationale.
//
//	transport := mqtt5.NewSubscribeTransport[SensorReading](client, router, 1, mqtt5.SubscribeOptions{})
//	err := events.SubscribeHandle(ctx, ReadingsChannel.WithSubscribe(events.Subscribe{}), transport,
//	    func(ctx context.Context, r SensorReading) error { ... })
func NewSubscribeTransport[T any](client MQTTClient, router MQTTRouter, qos byte, opts SubscribeOptions, formats ...format.Format[T]) events.SubscribeTransport[T] {
	return &subscribeTransport[T]{client: client, router: router, qos: qos, opts: opts, formats: formats}
}

type subscribeTransport[T any] struct {
	client  MQTTClient
	router  MQTTRouter
	qos     byte
	opts    SubscribeOptions
	formats []format.Format[T]
}

// Subscribe implements [events.SubscribeTransport]. Delegates to the same
// internal logic [subscribeWithHandle] (formerly the public
// `SubscribeWithHandle`) always has — topic filter derivation, format
// resolution, decode+merge, security enforcement, Observer/tracing —
// unchanged.
func (t *subscribeTransport[T]) Subscribe(ctx context.Context, handle *events.ChannelHandle[T], fn func(context.Context, T) error) error {
	return subscribeWithHandle(ctx, t.client, t.router, handle, t.qos, fn, t.opts, t.formats...)
}

// AdapterName implements [events.SubscribeTransport].
func (t *subscribeTransport[T]) AdapterName() string { return "mqtt5.SubscribeTransport" }

var _ events.SubscribeTransport[struct{}] = (*subscribeTransport[struct{}])(nil)
