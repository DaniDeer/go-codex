package mqtt5

import (
	"context"
	"fmt"
	"time"

	"github.com/DaniDeer/go-codex/api/reqreply"
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

// Serve subscribes to the route path as an MQTT 5 request topic and
// replies to each request using the ResponseTopic and CorrelationData MQTT 5
// properties.
//
// Serve is a thin, single-route wrapper around [reqreply.ServerTransport.
// Serve] — builds a [serverTransport] directly from client/router/opts and
// delegates to it (the SAME reflection-based dispatch [AttachServer]'s
// registered routes use), rather than duplicating the decode/merge/
// security/encode/error-pattern pipeline inline. Zero duplicate logic —
// full capability parity with [AttachServer] is therefore automatic (see
// docs/design/d-0004-reqreply-workflow-simplification.md's Addendum's Phase 0/0b for the history: this
// used to be a separate, hand-written implementation; Phase 0 closed the
// capability gap, Phase 0b collapsed the duplication).
//
// For each incoming message, the underlying dispatch:
//  1. Decodes the payload using handle's codec (honoring RequestFormats
//     and NewTopicParam merge fields).
//  2. Calls fn with the decoded value.
//  3. Encodes the response (honoring Formats) and publishes it to
//     msg.Properties.ResponseTopic with the same CorrelationData.
//
// When fn or encoding fails, an error reply is published to ResponseTopic
// (honoring a declared [reqreply.ErrorPattern]) so the requester receives
// a [CallError] rather than blocking indefinitely.
//
// Errors per-request are delivered via [ServeOptions.OnError].
//
// Serve registers the handler with router and calls client.Subscribe
// once. It returns nil immediately; messages are processed asynchronously as
// they arrive via the router.
func Serve[Req, Resp any](
	ctx context.Context,
	client MQTTClient,
	router MQTTRouter,
	handle *reqreply.RouteHandle[Req, Resp],
	fn func(context.Context, Req) (Resp, error),
	opts ServeOptions,
) error {
	t := &serverTransport{client: client, router: router, opts: opts}
	return t.Serve(ctx, handle, fn)
}

// Request encodes req, publishes it to the route path with MQTT 5 ResponseTopic
// and CorrelationData properties, then waits for a matching reply.
//
// Call is a thin, single-call wrapper around [reqreply.ClientTransport.
// Call] — builds a [clientTransport] directly from client/router/opts and
// delegates to it (the SAME reflection-based dispatch [AttachClient]
// uses), rather than duplicating the encode/merge/security/decode
// pipeline inline. Zero duplicate logic — full capability parity with
// [AttachClient] is therefore automatic, including [CallOptions.Vars]
// (explicit override, takes PRECEDENCE over any [reqreply.NewTopicParam]
// merge-field-derived value for the same key — mirrors [CallHandle]'s own
// documented precedence) and [CallOptions.RequestFormats]/[ResponseFormats]
// (per-call format overrides). See docs/design/d-0004-reqreply-workflow-simplification.md's Addendum's
// Phase 0/0b for the history: this used to be a separate, hand-written
// implementation; Phase 0 closed the capability gap, Phase 0b collapsed
// the duplication.
//
// Each call generates a unique reply topic: "<opts.ReplyTopicPrefix>/<uuid>".
// Call subscribes to this topic before publishing (avoiding a race), waits
// for a message with matching CorrelationData, then unsubscribes.
//
// On success, returns the decoded response.
// On timeout, returns [CallError]{Kind: [KindTimeout]}.
// On server error reply, returns [CallError]{Kind: [KindHandler]}.
// On decode failure, returns [CallError]{Kind: [KindDecode]}.
func Call[Req, Resp any](
	ctx context.Context,
	client MQTTClient,
	router MQTTRouter,
	handle *reqreply.RouteHandle[Req, Resp],
	req Req,
	opts CallOptions,
) (Resp, error) {
	var zero Resp
	t := &clientTransport{client: client, router: router, opts: opts}
	respAny, err := t.Call(ctx, handle, req)
	if err != nil {
		return zero, err
	}
	resp, ok := respAny.(Resp)
	if !ok {
		return zero, reqreply.TransportTypeMismatchError{Topic: handle.Topic, Want: fmt.Sprintf("%T", zero), Got: fmt.Sprintf("%T", respAny)}
	}
	return resp, nil
}

// CallHandle is a deprecated-but-kept alias for [Call] — [Call] itself
// now auto-derives [CallOptions.Vars] from req (via the route's
// merge-capable topic params, [reqreply.RouteHandle.MergeFields] +
// [reqreply.RouteHandle.EncodeVars]), the SAME auto-derivation this
// function used to add on top of [Call] before [AttachClient]'s
// underlying [clientTransport.call] gained it directly (Phase 0 of
// docs/design/d-0004-reqreply-workflow-simplification.md's Addendum). An explicit [CallOptions.Vars]
// still takes PRECEDENCE over the derived value for the same key.
// Kept for existing callers — prefer [Call] directly in new code, since
// it is now identical.
//
//	resp, err := mqtt5.CallHandle(ctx, client, router, computeRoute, req, mqtt5.CallOptions{})
func CallHandle[Req, Resp any](
	ctx context.Context,
	client MQTTClient,
	router MQTTRouter,
	handle *reqreply.RouteHandle[Req, Resp],
	req Req,
	opts CallOptions,
) (Resp, error) {
	return Call(ctx, client, router, handle, req, opts)
}

// isErrorReply checks whether an incoming reply was sent as an error by the responder.
// Error replies carry a special ContentType "application/mqtt5-error".
func isErrorReply(msg *pahomqtt5.Publish) bool {
	return msg.Properties != nil &&
		msg.Properties.ContentType == errorReplyContentType
}

// errorReplyContentType is the ContentType set on error replies by [Serve].
const errorReplyContentType = "application/mqtt5-error"

// publishErrorReply sends an error reply to the requester's ResponseTopic as
// a plain-text payload. Used directly for transport/decode-level errors that
// occur before the application handler runs (no [reqreply.ErrorPattern] can
// apply — there is no business error to match yet).
func publishErrorReply(ctx context.Context, client MQTTClient, responseTopic string, correlationData []byte, err error) {
	if responseTopic == "" {
		return
	}
	props := &pahomqtt5.PublishProperties{
		ContentType:     errorReplyContentType,
		CorrelationData: correlationData,
	}
	_, _ = client.Publish(ctx, &pahomqtt5.Publish{
		Topic:      responseTopic,
		QoS:        1,
		Payload:    []byte(err.Error()),
		Properties: props,
	})
}

// reportRouteParamErrors reports topic variable errors from [reqreply.RouteHandle.BuildTopic]
// to obs with location "topic_var", extracting variable names and using "required" as the
// constraint name for missing variables.
func reportRouteParamErrors(err error, obs stats.Observer) {
	if e, ok := err.(reqreply.MissingRouteParamError); ok {
		obs.RecordValidationError("topic_var", "required", e.Name)
		return
	}
	if e, ok := err.(reqreply.RouteParamError); ok {
		obs.RecordValidationError("topic_var", stats.ConstraintName(e.Err), e.Name)
	}
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
