// Package mqtt5 provides codec-backed adapters for MQTT 5.0 using the
// github.com/eclipse/paho.golang library.
//
// It follows the same declare → register → handle → adapt pattern as
// [api/rest] and [api/events], with the same consistent options structs and
// observer integration used by [adapters/mqtt], [adapters/nethttp], and
// [adapters/zeromq].
//
// # Patterns
//
// Three patterns are supported:
//
//   - PUB/SUB — via [api/events] channel declarations + [Attach]
//   - REQ/REP — via [api/reqreply] route declarations + [Serve]/[Call]
//
// # caller — bundling client+router+events.Client
//
// [caller] (built via [newCaller]) bundles the repeated client/router/
// events.Client params for the value-based pub/sub surface, internally;
// none of this is publicly reachable — call [Attach] and use the returned
// [events.Client]'s Publish/Subscribe/ServeSubscribers methods instead:
//
//	_ = mqtt5.Attach(eventsClient, client, router)
//	sub := SensorReadings.WithSubscribe(events.Subscribe{})
//	err := eventsClient.Subscribe(ctx, sub, fn)
//
// [subscribe] builds the handle internally via [events.Subscriber.Handle]
// and delegates to [SubscribeWithHandle], the handle-based primitive that
// remains public for callers (e.g. [SubscribeAdapter]) that already own a
// pre-built handle.
//
// [(*caller).ServeSubscribers] implements [events.SubscriberServer] — the
// whole-client entry point that walks every [events.Subscriber] registered
// via [events.Subscriber.Register] and starts consuming each one, one
// goroutine per channel, blocking until ctx is cancelled (available once
// [Attach] has bound a transport, via [events.Client.ServeSubscribers]).
// Per-channel QoS/TopicFilter are recovered from
// [events.ChannelHandle.HandlerOpts] (attached via
// [events.Subscriber.WithOptions] with a [SubscribeOptions] value).
// [serveOneSubscriber] is the zero-ceremony shortcut for a single channel.
//
// Publishing goes through [events.Client.Publish] (once [Attach] has bound
// a transport), which satisfies [events.PublisherClient] with just
// (ctx, msg) — the publish-side mirror of the subscribe-side abstractions
// above. [Publish]/[PublishHandle] remain the lower-level primitives.
//
// [Observability] builds a declare-time, general-purpose
// `func(next func(context.Context, T) error) func(context.Context, T) error`
// closure — attach via `sub.SubscribeMW(nil, mqtt5.Observability[T](topic, obs))`
// or `pub.PublishMW(nil, mqtt5.Observability[T](topic, obs))` for
// consistent observability regardless of call site. This is ADDITIONAL to,
// not a replacement for, [SubscribeOptions.Observer]/[PublishOptions.Observer]
// (still the primary, ctx-resolved path).
//
// # Fn shapes for SubscribeMW/PublishMW
//
// Every [events.Subscriber.SubscribeMW]/[events.Publisher.PublishMW]-attached
// Fn is validated eagerly (at [subscribe]/[SubscribeWithHandle]/
// [(*caller).ServeSubscribers]/[Publish] construction/dispatch time) against
// two recognized shapes, returning [middleware.MiddlewareShapeError] on any
// other shape:
//
//   - Security-shaped: subscribe side
//     func(context.Context, *paho.Publish, *T) (map[string][]string, error);
//     publish side func(context.Context, msg *T, reqs []route.SecurityRequirement)
//     ([]UserProperty, error) — write-access to *T lets a credential be
//     embedded as an ordinary payload field, and/or (publish side only)
//     returned as protocol-native User Properties.
//   - General-purpose wrapping: func(next func(context.Context, T) error)
//     func(context.Context, T) error, for BOTH roles — the mechanism
//     [Observability] uses.
//
// # MQTT 5 enhancements over MQTT 3.1.1
//
//   - User Properties: per-message key-value pairs exposed in SecurityFunc and
//     via [UserPropertiesFromContext] — enables proper per-message authentication.
//   - Content-Type auto-selection: when the incoming message carries a ContentType
//     property, [subscribe] auto-selects the matching format from the formats slice
//     by comparing with [format.Format.ContentType]. Set ContentType on outgoing
//     messages via [PublishOptions.ContentType].
//   - Request-Reply: [Serve] reads the ResponseTopic and CorrelationData
//     MQTT 5 message properties to reply to the caller. [Call] publishes
//     a request with a per-call reply topic and waits for the response.
//     Customise reply topic generation via [CallOptions.ReplyTopicBuilder];
//     use [UUIDReplyTopic] (default) or [SharedReplyTopic] for shared subscriptions.
//
// # Client and router
//
// The adapter accepts [MQTTClient] (satisfied by [*paho.Client]) and [MQTTRouter]
// (satisfied by [*paho.StandardRouter]). Provide the same client and router for
// all Subscribe and ServeRequestReply calls on a connection.
//
// # Observer
//
// Pass a [stats.Observer] via the options structs. Implement [stats.TraceObserver]
// for distributed tracing spans with operations "mqtt5.subscribe", "mqtt5.publish",
// "mqtt5.serve", "mqtt5.request".
package mqtt5
