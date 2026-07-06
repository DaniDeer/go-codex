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
//   - PUB/SUB — via [api/events] channel declarations + [Subscribe]/[Publish]
//   - REQ/REP — via [api/reqreply] route declarations + [Serve]/[Call]
//
// # MQTT 5 enhancements over MQTT 3.1.1
//
//   - User Properties: per-message key-value pairs exposed in SecurityFunc and
//     via [UserPropertiesFromContext] — enables proper per-message authentication.
//   - Content-Type auto-selection: when the incoming message carries a ContentType
//     property, [Subscribe] auto-selects the matching format from the formats slice
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
