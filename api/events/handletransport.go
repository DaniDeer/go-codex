package events

import (
	"context"

	"github.com/DaniDeer/go-codex/format"
)

// PublishTransport is a GENERIC interface (parameterized by the channel's
// own payload type T) satisfied by an adapter-provided value that can
// publish a decoded T on the channel handle's topic — the publish-side
// half of Decision 7's handle-based inversion
// (docs/design/d-0002-pubsub-workflow-simplification.md). Mirrors
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
	// Publish sends msg on the topic derived from handle — a
	// hand-written implementation typically calls [EncodeAndBuildTopic]
	// internally to derive the topic and encode the payload (itself a
	// thin wrapper over [ChannelHandle.EncodeVars]/[ChannelHandle.
	// EncodeWithFormats]/[ChannelHandle.BuildTopic] — the SAME canonical,
	// single-source-of-truth methods every shipped adapter's own thin
	// primitives call directly), then layers its own protocol-specific
	// send around the result.
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
	// cancelled. A hand-written implementation typically calls
	// [DecodeAndMergeVars] internally to decode the payload and merge
	// topic variables into the SAME value before calling fn (itself a
	// thin wrapper over [ChannelHandle.DecodeMergedWithFormats] — the
	// SAME canonical, single-source-of-truth method every shipped
	// adapter's own thin primitives call directly).
	Subscribe(ctx context.Context, handle *ChannelHandle[T], fn func(context.Context, T) error) error

	// AdapterName returns a descriptor for observability/error messages.
	AdapterName() string
}

// EncodeAndBuildTopic derives msg's topic variables (via
// [ChannelHandle.EncodeVars]), builds the concrete topic (via
// [ChannelHandle.BuildTopic]), and encodes msg to wire bytes via
// [ChannelHandle.EncodeWithFormats] — a thin, hand-written-
// [PublishTransport]-facing wrapper over the SAME canonical,
// single-source-of-truth encode method every shipped adapter's own thin
// primitive (`publish`/`publishHandle`, etc.) already calls directly.
// Kept for callers writing their OWN [PublishTransport][T] against a
// transport this package doesn't ship an adapter for; the three shipped
// adapters (`adapters/mqtt5`, `adapters/mqtt`, `adapters/zeromq`) do NOT
// call this function themselves — see [ChannelHandle.EncodeWithFormats]'s
// doc comment for the full centralized-resolution rationale (Decision 9,
// docs/design/d-0002-pubsub-workflow-simplification.md).
//
// formats, when non-empty, overrides the channel's declared
// PublishFormats/Formats for this call only — see
// [ChannelHandle.EncodeWithFormats]'s doc comment for the exact
// precedence.
func EncodeAndBuildTopic[T any](handle *ChannelHandle[T], msg T, formats ...format.Format[T]) (topic string, payload []byte, err error) {
	vars, err := handle.EncodeVars(msg)
	if err != nil {
		return "", nil, err
	}
	payload, err = handle.EncodeWithFormats(msg, formats...)
	if err != nil {
		return "", nil, err
	}
	topic, err = handle.BuildTopic(vars)
	if err != nil {
		return "", nil, err
	}
	return topic, payload, nil
}

// DecodeAndMergeVars decodes payload and merges topicVars (already
// extracted by the caller from the concrete received topic, e.g. via
// [TopicVarsFromMessage]-style matching against handle.Topic) into the
// SAME decoded value, via [ChannelHandle.DecodeMergedWithFormats] — a
// thin, hand-written-[SubscribeTransport]-facing wrapper over that SAME
// canonical, single-source-of-truth decode+merge method every shipped
// adapter's own thin primitive (`subscribeHandler`/`subscribeWithHandle`,
// etc.) already calls directly (or, for the two adapters needing
// separately-reported decode-vs-merge errors, its `DecodeWithFormats`
// half). Kept for callers writing their OWN [SubscribeTransport][T]
// against a transport this package doesn't ship an adapter for; the
// three shipped adapters do NOT call this function themselves — see
// [ChannelHandle.DecodeMergedWithFormats]'s doc comment for the full
// centralized-resolution rationale (Decision 9,
// docs/design/d-0002-pubsub-workflow-simplification.md).
//
// formats, when non-empty, overrides the channel's declared
// SubscribeFormats/Formats for this call only. When handle declares no
// merge-capable topic params (MergeFields() is empty), this behaves
// identically to a bare format-aware decode — topicVars is ignored.
func DecodeAndMergeVars[T any](handle *ChannelHandle[T], payload []byte, topicVars map[string]string, formats ...format.Format[T]) (T, error) {
	return handle.DecodeMergedWithFormats(payload, topicVars, formats...)
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
