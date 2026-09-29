package mqtt5

import (
	"context"
	"time"

	"github.com/DaniDeer/go-codex/stats"
	pahomqtt5 "github.com/eclipse/paho.golang/paho"
	"github.com/google/uuid"
)

// ServeOptions configures [Serve].
type ServeOptions struct {
	// OnError, when non-nil, is called with a typed [ServeError] on decode,
	// handler, or reply-encode failure. If nil, errors are silently discarded.
	// A reply is always attempted (even on failure) to avoid leaving the caller
	// waiting — the reply payload carries the error string on failure.
	OnError func(ServeError)

	// Observer receives per-request lifecycle events.
	// [stats.Observer.RecordRequest] is called with method "MQTT5-REP",
	// the route path, status 200 on success, and status 0 on failure.
	// Defaults to [stats.NoopObserver] when nil.
	Observer stats.Observer

	// UserPropertyParams, when non-nil, are validated against the incoming
	// request's MQTT 5 User Properties before the payload is decoded.
	// Validation failure delivers [ServeError]{Kind: KindSecurity}
	// and sends an error reply to the caller.
	UserPropertyParams []UserPropertyParam

	// Capabilities supplies this server's concrete protocol-native
	// capability values (docs/roadmap/capability-requirement-composition.md's
	// Phase 2) — e.g. [QoS]/[Retained] — checked against the route's
	// declared [reqreply.CapabilityRequirement]s via
	// [reqreply.VerifyCapabilityCoverage] at Serve setup, then applied to
	// EVERY reply publish (success, error-pattern-matched, and
	// dead-letter alike).
	Capabilities []Capability
}

// REMOVED (Phase 1 of docs/design/d-0004-reqreply-workflow-simplification.md's Addendum, BREAKING):
// ServeOptions.SecurityFunc. Declare a paired security implementation via
// [reqreply.Route.Use] + [reqreply.Route.HandleMW] instead — the SAME
// codec-based credential check (via [reqreply.SecurityScheme.Codec])
// still runs first, unconditionally; the attached implementation runs
// after it, mirroring the OLD SecurityFunc's ordering exactly. Mirrors
// REST's own D-0001 precedent (`adapters/nethttp.Options`'s "BREAKING:
// ... SecurityFunc are REMOVED" — the SAME reasoning applies here: ONE
// declarative security mechanism, not a permanent parallel imperative
// escape hatch). `SubscribeOptions.SecurityFunc` (events pub/sub) is
// UNAFFECTED by this — it stays, a distinct decision scoped to reqreply
// only in this phase.

// CallOptions configures [Call].
type CallOptions struct {
	// ReplyTopicPrefix is the prefix for the auto-generated reply topic.
	// Call generates: "<ReplyTopicPrefix>/<uuid>" per call.
	// When empty, defaults to "replies".
	// Ignored when [ReplyTopicBuilder] is non-nil.
	ReplyTopicPrefix string

	// ReplyTopicBuilder, when non-nil, overrides [ReplyTopicPrefix] and controls
	// how the reply topic pair is generated for each call. It must return
	// (responseTopic, subscribeFilter):
	//   - responseTopic is set as the MQTT 5 ResponseTopic property on the
	//     outgoing request and must be a valid publish topic (no wildcards).
	//   - subscribeFilter is passed to client.Subscribe. For regular
	//     subscriptions it equals responseTopic. For shared subscriptions it
	//     carries the "$share/<group>/" prefix.
	//
	// An empty responseTopic is a programmer error and causes [Call] to
	// return [CallError]{Kind: [KindEncode]}. An empty subscribeFilter
	// falls back to responseTopic.
	//
	// Use [UUIDReplyTopic] or [SharedReplyTopic] for built-in builders.
	ReplyTopicBuilder ReplyTopicBuilder

	// Timeout is how long Call waits for a reply before returning
	// [CallError]{Kind: [KindTimeout]}.
	// When zero, defaults to 30 seconds.
	Timeout time.Duration

	// Observer receives per-call lifecycle events.
	// [stats.Observer.RecordRequest] is called with method "MQTT5-REQ",
	// the route path, status 200 on success, status 0 on timeout/error,
	// and status 500 on server-side error reply.
	// Defaults to [stats.NoopObserver] when nil.
	Observer stats.Observer

	// QoS is the MQTT QoS level for the outgoing request message (0, 1, or 2).
	// Defaults to 1.
	QoS byte

	// UserProperties, when non-nil, are attached to the outgoing request message.
	UserProperties []UserProperty

	// Vars, when non-nil, substitutes {varName} placeholders in the route topic
	// template before publishing. Uses [reqreply.RouteHandle.BuildTopic] to
	// resolve and codec-validate each variable.
	//
	// Example — template topic "compute/{tenantID}/add":
	//
	//	mqtt5.Call(ctx, client, router, handle, req,
	//	    mqtt5.CallOptions{Vars: map[string]string{"tenantID": "acme"}})
	//
	// Returns [reqreply.RouteParamError] or [reqreply.MissingRouteParamError] on
	// validation failure.
	Vars map[string]string

	// RequestFormats, when non-nil, OVERRIDES the route's declared request
	// encode format for THIS call only. Type-erased ([]format.Format[Req])
	// since CallOptions itself is not generic; [Call] type-asserts it once
	// Req is concrete, returning [CallError]{Kind: [KindEncode]} on a type
	// mismatch — mirrors [nethttp.CallOptions.RequestFormats] exactly.
	//
	// Priority: RequestFormats (this field) > handle.RequestFormats
	// (route-declared) > handle.EncodeRequest (JSON default).
	RequestFormats any

	// ResponseFormats, when non-nil, OVERRIDES the route's declared
	// response decode format for THIS call only — same type-erasure and
	// priority-chain contract as [CallOptions.RequestFormats], mirrored
	// for the response direction ([]format.Format[Resp]); a type mismatch
	// returns [CallError]{Kind: [KindDecode]}.
	ResponseFormats any

	// Capabilities supplies this call's concrete protocol-native
	// capability values (docs/roadmap/capability-requirement-composition.md's
	// Phase 2) — e.g. [QoS]/[Retained] — applied to the outgoing request
	// publish. Overrides [CallOptions.QoS] when a [QoS] capability is
	// present (its dedicated MinLevel-checked declaration path). No
	// coverage check runs on the client/Call side, mirroring events' own
	// "publish side never auto-checks coverage" precedent.
	Capabilities []Capability
}

// REMOVED (Phase 1 of docs/design/d-0004-reqreply-workflow-simplification.md's Addendum, BREAKING):
// CallOptions.CredentialFunc. Declare a paired credential-supplying
// implementation via [reqreply.Route.Use] + [reqreply.Route.ClientMW]
// instead — same shape (`func(ctx, reqs) ([]UserProperty, error)`), same
// "nil/unmatched Satisfies is not an error" semantics, now attached to
// the ROUTE rather than passed per-Attach. Mirrors REST's own D-0001
// precedent exactly. `PublishOptions.CredentialFunc` (events pub/sub) is
// UNAFFECTED — it stays, a distinct decision scoped to reqreply only in
// this phase.

// Serve/Call/CallHandle were REMOVED (docs/roadmap/capability-
// requirement-composition.md's Phase 5a, a deliberate breaking change):
// they were thin, single-route/single-call wrappers that built a
// [serverTransport]/[clientTransport] directly from client/router/opts
// and delegated immediately — the EXACT SAME construction
// [NewServerTransport]/[NewClientTransport] (Phase 4d's factories)
// already perform. The api-layer-owned [reqreply.ServeWithTransport]/
// [reqreply.CallWithTransport] now serve this exact use case (a caller
// wanting compile-time type safety and zero [*reqreply.Server]/
// [*reqreply.Client] registration ceremony for a single route), mirroring
// [events.SubscribeHandle]/[events.PublishHandle]'s already-correct
// split exactly:
//
//	transport := mqtt5.NewServerTransport(mqtt5.ServerTransportOptions{
//	    Client: client, Router: router, Serve: mqtt5.ServeOptions{},
//	})
//	err := reqreply.ServeWithTransport(ctx, transport, computeRoute.ClientHandle(), computeHandler)
//
//	transport := mqtt5.NewClientTransport(mqtt5.ClientTransportOptions{Client: client, Router: router})
//	resp, err := reqreply.CallWithTransport(ctx, transport, computeRoute.ClientHandle(), req)

// isErrorReply checks whether an incoming reply was sent as an error by the responder.
// Error replies carry a special ContentType "application/mqtt5-error".
func isErrorReply(msg *pahomqtt5.Publish) bool {
	return msg.Properties != nil &&
		msg.Properties.ContentType == errorReplyContentType
}

// errorReplyContentType is the ContentType set on error replies by [Serve].
const errorReplyContentType = "application/mqtt5-error"

// errorCodePropertyKey is the RESERVED MQTT5 User Property key carrying a
// matched [reqreply.ErrorPattern]'s Code — set ONLY by
// [publishHandlerErrorReplyReflect]'s matched branch, never by
// [publishErrorReply]'s plain-text fallback. Lets the CLIENT look up
// which declared pattern (if any) produced a given error reply's body —
// see [reqreply.RouteHandle.DecodeErrorFor].
const errorCodePropertyKey = "x-error-code"

// errorCodeFromUserProperties reads [errorCodePropertyKey] from an error
// reply's User Properties — "" when absent (the plain-text fallback path
// never sets it, and older servers predating this feature never will).
func errorCodeFromUserProperties(msg *pahomqtt5.Publish) string {
	if msg.Properties == nil {
		return ""
	}
	for _, p := range msg.Properties.User {
		if p.Key == errorCodePropertyKey {
			return p.Value
		}
	}
	return ""
}

// publishErrorReply sends an error reply to the requester's ResponseTopic as
// a plain-text payload. Used directly for transport/decode-level errors that
// occur before the application handler runs (no [reqreply.ErrorPattern] can
// apply — there is no business error to match yet).
//
// qos/retained are the EFFECTIVE values resolved once at Serve setup from
// [ServeOptions.Capabilities] (docs/roadmap/capability-requirement-
// composition.md's Phase 2) — applied here so this error-reply path
// honors a supplied capability exactly like the success/dead-letter reply
// paths do, closing a gap where this path previously hardcoded QoS 1 and
// never set Retained at all.
func publishErrorReply(ctx context.Context, client MQTTClient, responseTopic string, correlationData []byte, err error, qos byte, retained bool) {
	if responseTopic == "" {
		return
	}
	props := &pahomqtt5.PublishProperties{
		ContentType:     errorReplyContentType,
		CorrelationData: correlationData,
	}
	_, _ = client.Publish(ctx, &pahomqtt5.Publish{
		Topic:      responseTopic,
		QoS:        qos,
		Retain:     retained,
		Payload:    []byte(err.Error()),
		Properties: props,
	})
}

// ReplyTopicBuilder generates the reply topic pair for a single [Request] call.
//
// It returns two strings:
//   - responseTopic: the concrete MQTT topic written into the MQTT 5
//     ResponseTopic property of the outgoing request. Must be a valid publish
//     topic (no wildcards, no $share prefix).
//   - subscribeFilter: the topic filter passed to [MQTTClient.Subscribe]. For
//     regular subscriptions this equals responseTopic. For shared subscriptions
//     it carries the "$share/<group>/" prefix while responseTopic does not.
//
// When nil, [Request] falls back to [UUIDReplyTopic]("replies") behaviour.
// Use [UUIDReplyTopic] or [SharedReplyTopic] for built-in builders.
type ReplyTopicBuilder func() (responseTopic, subscribeFilter string)

// UUIDReplyTopic returns a [ReplyTopicBuilder] that generates "<prefix>/<uuid>"
// for both responseTopic and subscribeFilter. It is the explicit form of the
// default behaviour when [RequestOptions.ReplyTopicBuilder] is nil.
//
// When prefix is empty, "replies" is used.
//
// Example:
//
//	mqtt5.Request(ctx, client, router, handle, req,
//	    mqtt5.CallOptions{
//	        ReplyTopicBuilder: mqtt5.UUIDReplyTopic("replies"),
//	    })
func UUIDReplyTopic(prefix string) ReplyTopicBuilder {
	if prefix == "" {
		prefix = "replies"
	}
	return func() (string, string) {
		topic := prefix + "/" + uuid.New().String()
		return topic, topic
	}
}

// SharedReplyTopic returns a [ReplyTopicBuilder] that generates a unique
// responseTopic "<prefix>/<uuid>" and a shared subscribeFilter
// "$share/<group>/<prefix>/<uuid>".
//
// Use shared subscriptions when a pool of [Request] callers shares a single
// reply consumer. The MQTT broker delivers each reply to exactly one subscriber
// in the group. The responseTopic sent to the responder is a plain publish topic
// (no $share prefix); the broker routes it to the shared group transparently.
//
// When prefix is empty, "replies" is used.
//
// Example:
//
//	mqtt5.Request(ctx, client, router, handle, req,
//	    mqtt5.CallOptions{
//	        ReplyTopicBuilder: mqtt5.SharedReplyTopic("replies", "gateway-pool"),
//	        // ResponseTopic sent:   "replies/<uuid>"
//	        // client.Subscribe on:  "$share/gateway-pool/replies/<uuid>"
//	    })
func SharedReplyTopic(prefix, group string) ReplyTopicBuilder {
	if prefix == "" {
		prefix = "replies"
	}
	return func() (string, string) {
		id := uuid.New().String()
		responseTopic := prefix + "/" + id
		subscribeFilter := "$share/" + group + "/" + responseTopic
		return responseTopic, subscribeFilter
	}
}
