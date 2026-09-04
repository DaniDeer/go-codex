package events

import (
	"context"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
)

// PublishTransport is a GENERIC interface (parameterized by the channel's
// own payload type T) satisfied by an adapter-provided value that can
// publish a decoded T on the channel handle's topic — the publish-side
// half of Decision 7's handle-based inversion
// (docs/roadmap/pubsub-workflow-simplification.md). Mirrors
// [ports.SourceAdapter]/[ports.SinkAdapter]'s own proven generic-interface
// convention exactly: each adapter provides a GENERIC constructor function
// (e.g. zeromq.NewPublishTransport[T]) returning a per-T-instantiated
// concrete type satisfying this interface — fully type-safe, no
// reflection anywhere in this call path (unlike [Transport], whose
// Publish/Subscribe methods are `any`-typed because they are METHODS on
// the single, non-generic [Client] value; [PublishHandle]/[SubscribeHandle]
// below are FREE FUNCTIONS, which CAN introduce their own type parameters,
// so there is no structural reason to fall back to reflection here).
type PublishTransport[T any] interface {
	// Publish sends msg on the topic derived from handle — implementations
	// typically call [EncodeAndBuildTopic] internally to derive the topic
	// and encode the payload, then perform their own protocol-specific
	// send.
	Publish(ctx context.Context, handle *ChannelHandle[T], msg T) error

	// AdapterName returns a descriptor for observability/error messages —
	// mirrors [ports.SourceAdapter.AdapterName]/[ports.SinkAdapter.AdapterName].
	AdapterName() string
}

// SubscribeTransport is a GENERIC interface (parameterized by the
// channel's own payload type T) satisfied by an adapter-provided value
// that can run a receive loop decoding incoming messages into T — the
// subscribe-side half of Decision 7's handle-based inversion. See
// [PublishTransport]'s doc comment for the full design rationale.
type SubscribeTransport[T any] interface {
	// Subscribe runs a receive loop matching handle's topic (derived via
	// the adapter's own native wildcard/prefix filter syntax),
	// dispatching each successfully-decoded message to fn, until ctx is
	// cancelled. Implementations typically call [DecodeAndMergeVars]
	// internally to decode the payload and merge topic variables into the
	// SAME value before calling fn.
	Subscribe(ctx context.Context, handle *ChannelHandle[T], fn func(context.Context, T) error) error

	// AdapterName returns a descriptor for observability/error messages.
	AdapterName() string
}

// EncodeAndBuildTopic derives msg's topic variables (via
// [ChannelHandle.EncodeVars]), builds the concrete topic (via
// [ChannelHandle.BuildTopic]), and encodes msg to wire bytes — the SHARED,
// adapter-agnostic mechanical core every adapter's own Publish/
// PublishHandle implementation duplicated before Decision 7 (each
// adapter's [PublishTransport] implementation calls this once internally,
// then layers its own protocol-specific send around the result).
//
// formats, when non-empty, overrides handle.PublishFormats/handle.Formats
// for this call only (formats[0] wins) — mirrors every adapter's existing
// "explicit formats > PublishFormats > Formats > default Decode/Encode"
// precedence exactly.
func EncodeAndBuildTopic[T any](handle *ChannelHandle[T], msg T, formats ...format.Format[T]) (topic string, payload []byte, err error) {
	vars, err := handle.EncodeVars(msg)
	if err != nil {
		return "", nil, err
	}

	effectiveFmts := formats
	if len(effectiveFmts) == 0 {
		effectiveFmts = handle.PublishFormats
	}
	if len(effectiveFmts) == 0 {
		effectiveFmts = handle.Formats
	}
	if len(effectiveFmts) > 0 {
		payload, err = effectiveFmts[0].Marshal(msg)
	} else {
		payload, err = handle.Encode(msg)
	}
	if err != nil {
		return "", nil, err
	}

	topic, err = handle.BuildTopic(vars)
	if err != nil {
		return "", nil, err
	}
	return topic, payload, nil
}

// DecodeAndMergeVars decodes payload (via the adapter's chosen format —
// see formats' precedence below) and merges topicVars (already extracted
// by the adapter from the concrete received topic, e.g. via
// [TopicVarsFromMessage]-style matching against handle.Topic) into the
// SAME decoded value, via [ChannelHandle.MergeFields] — the SHARED,
// adapter-agnostic mechanical core every adapter's own Subscribe/
// SubscribeWithHandle implementation duplicated before Decision 7.
//
// formats, when non-empty, overrides handle.SubscribeFormats/handle.Formats
// for this call only (formats[0] wins) — mirrors every adapter's existing
// format-resolution precedence exactly. When handle declares no
// merge-capable topic params (MergeFields() is empty), this behaves
// identically to a bare format-aware decode — topicVars is ignored.
func DecodeAndMergeVars[T any](handle *ChannelHandle[T], payload []byte, topicVars map[string]string, formats ...format.Format[T]) (T, error) {
	var value T

	effectiveFmts := formats
	if len(effectiveFmts) == 0 {
		effectiveFmts = handle.SubscribeFormats
	}
	if len(effectiveFmts) == 0 {
		effectiveFmts = handle.Formats
	}

	var err error
	if len(effectiveFmts) > 0 {
		value, err = effectiveFmts[0].Unmarshal(payload)
	} else {
		value, err = handle.Decode(payload)
	}
	if err != nil {
		return value, err
	}

	if mergeFields := handle.MergeFields(); len(mergeFields) > 0 {
		if err := codex.DecodeVars(&value, topicVars, mergeFields...); err != nil {
			return value, err
		}
	}
	return value, nil
}

// PublishHandle sends msg on pub's declared channel via adapter — the
// no-spec, no-*Client, handle-based call surface Decision 7 inverts into
// api/events itself (mirrors [Client.Publish]'s call shape exactly, minus
// the spec/registry — pub.Handle(nil) builds a fresh, spec-free
// [ChannelHandle] every call, identical to how [Client.Publish]'s
// reflection-based Transport internally calls pub.Handle(client)).
//
//	handle := zeromq.NewPublishTransport[SensorReading](sock, zeromq.PublishTransportOptions{})
//	err := events.PublishHandle(ctx, ReadingsChannel.WithPublish(events.Publish{}), handle, reading)
func PublishHandle[T any](ctx context.Context, pub Publisher[T], adapter PublishTransport[T], msg T) error {
	handle, err := pub.Handle(nil)
	if err != nil {
		return err
	}
	return adapter.Publish(ctx, handle, msg)
}

// SubscribeHandle runs adapter's receive loop for sub's declared channel,
// dispatching each decoded message to fn — the no-spec, no-*Client,
// handle-based call surface Decision 7 inverts into api/events itself
// (mirrors [Client.Subscribe]'s call shape exactly, minus the
// spec/registry). Blocks until ctx is cancelled or adapter returns a
// fatal error.
//
//	transport := zeromq.NewSubscribeTransport[SensorReading](sock, zeromq.SubscribeTransportOptions{})
//	err := events.SubscribeHandle(ctx, ReadingsChannel.WithSubscribe(events.Subscribe{}), transport,
//	    func(ctx context.Context, r SensorReading) error { ... })
func SubscribeHandle[T any](ctx context.Context, sub Subscriber[T], adapter SubscribeTransport[T], fn func(context.Context, T) error) error {
	handle, err := sub.Handle(nil)
	if err != nil {
		return err
	}
	return adapter.Subscribe(ctx, handle, fn)
}
