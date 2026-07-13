package mqtt5

import (
	"context"
	"fmt"
	"sync"
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
}

// Serve subscribes to the route path as an MQTT 5 request topic and
// replies to each request using the ResponseTopic and CorrelationData MQTT 5
// properties.
//
// For each incoming message, Serve:
//  1. Decodes the payload using handle's codec.
//  2. Calls fn with the decoded value.
//  3. Encodes the response and publishes it to msg.Properties.ResponseTopic
//     with the same CorrelationData.
//
// When fn or encoding fails, an error reply is published to ResponseTopic so the
// requester receives a [CallError] rather than blocking indefinitely.
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
	obs := opts.Observer
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}
	path := handle.Topic

	router.RegisterHandler(path, func(msg *pahomqtt5.Publish) {
		start := time.Now()
		msgCtx := context.WithValue(ctx, contextKey{}, msg)

		var spanCtx = msgCtx
		var serveErr error
		if to, ok := obs.(stats.TraceObserver); ok {
			spanCtx = to.StartSpan(msgCtx, "mqtt5.serve", path)
		}
		defer func() {
			if to, ok := obs.(stats.TraceObserver); ok {
				to.EndSpan(spanCtx, serveErr)
			}
		}()

		// Extract response routing properties.
		var responseTopic string
		var correlationData []byte
		if msg.Properties != nil {
			responseTopic = msg.Properties.ResponseTopic
			correlationData = msg.Properties.CorrelationData
		}

		// User Property param validation (before decode).
		if propErr := validateUserProperties(msg, opts.UserPropertyParams); propErr != nil {
			obs.RecordValidationError("user_property", stats.ConstraintName(propErr), userPropertyName(propErr))
			serveErr = propErr
			obs.RecordRequest("MQTT5-REP", path, 0, time.Since(start))
			publishErrorReply(spanCtx, client, responseTopic, correlationData, propErr)
			if opts.OnError != nil {
				opts.OnError(ServeError{Kind: KindSecurity, Err: propErr})
			}
			return
		}

		// decode request
		var req Req
		if len(handle.RequestFormats) > 0 {
			req, serveErr = handle.RequestFormats[0].Unmarshal(msg.Payload)
		} else {
			req, serveErr = handle.Decode(msg.Payload)
		}
		if serveErr != nil {
			stats.ReportErrors(obs, "body", serveErr)
			obs.RecordRequest("MQTT5-REP", path, 0, time.Since(start))
			publishErrorReply(spanCtx, client, responseTopic, correlationData, serveErr)
			if opts.OnError != nil {
				opts.OnError(ServeError{Kind: KindDecode, Err: serveErr})
			}
			return
		}

		// call handler
		var resp Resp
		resp, serveErr = fn(spanCtx, req)
		if serveErr != nil {
			obs.RecordRequest("MQTT5-REP", path, 0, time.Since(start))
			publishErrorReply(spanCtx, client, responseTopic, correlationData, serveErr)
			if opts.OnError != nil {
				opts.OnError(ServeError{Kind: KindHandler, Err: serveErr})
			}
			return
		}

		// encode response
		var respPayload []byte
		if len(handle.Formats) > 0 {
			respPayload, serveErr = handle.Formats[0].Marshal(resp)
		} else {
			respPayload, serveErr = handle.Encode(resp)
		}
		if serveErr != nil {
			obs.RecordRequest("MQTT5-REP", path, 0, time.Since(start))
			publishErrorReply(spanCtx, client, responseTopic, correlationData, serveErr)
			if opts.OnError != nil {
				opts.OnError(ServeError{Kind: KindEncode, Err: serveErr})
			}
			return
		}

		// publish reply to ResponseTopic
		if responseTopic != "" {
			replyProps := &pahomqtt5.PublishProperties{}
			if correlationData != nil {
				replyProps.CorrelationData = correlationData
			}
			if _, pubErr := client.Publish(spanCtx, &pahomqtt5.Publish{
				Topic:      responseTopic,
				QoS:        1,
				Payload:    respPayload,
				Properties: replyProps,
			}); pubErr != nil {
				serveErr = pubErr
				obs.RecordRequest("MQTT5-REP", path, 0, time.Since(start))
				if opts.OnError != nil {
					opts.OnError(ServeError{Kind: KindEncode, Err: pubErr})
				}
				return
			}
		}
		obs.RecordRequest("MQTT5-REP", path, 200, time.Since(start))
	})

	_, err := client.Subscribe(ctx, &pahomqtt5.Subscribe{
		Subscriptions: []pahomqtt5.SubscribeOptions{
			{Topic: path, QoS: 1},
		},
	})
	if err != nil {
		router.UnregisterHandler(path)
		return BrokerError{Op: "subscribe", Err: err}
	}
	return nil
}

// Request encodes req, publishes it to the route path with MQTT 5 ResponseTopic
// and CorrelationData properties, then waits for a matching reply.
//
// Each call generates a unique reply topic: "<opts.ReplyTopicPrefix>/<uuid>".
// Request subscribes to this topic before publishing (avoiding a race), waits
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
	obs := opts.Observer
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}
	start := time.Now()
	var callErr error
	// Resolve template topic vars if provided.
	path := handle.Topic
	if opts.Vars != nil {
		var buildErr error
		path, buildErr = handle.BuildTopic(opts.Vars)
		if buildErr != nil {
			reportRouteParamErrors(buildErr, obs)
			callErr = CallError{Kind: KindEncode, Err: buildErr}
			obs.RecordRequest("MQTT5-REQ", handle.Topic, 0, time.Since(start))
			return zero, callErr
		}
	}
	if to, ok := obs.(stats.TraceObserver); ok {
		ctx = to.StartSpan(ctx, "mqtt5.request", path)
		defer func() { to.EndSpan(ctx, callErr) }()
	}

	timeout := opts.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	qos := opts.QoS
	if qos == 0 {
		qos = 1
	}

	// Resolve the reply topic pair.
	var replyTopic, subscribeFilter string
	if opts.ReplyTopicBuilder != nil {
		replyTopic, subscribeFilter = opts.ReplyTopicBuilder()
		if replyTopic == "" {
			callErr = CallError{Kind: KindEncode, Err: fmt.Errorf("reply topic builder returned empty response topic")}
			obs.RecordRequest("MQTT5-REQ", path, 0, time.Since(start))
			return zero, callErr
		}
		if subscribeFilter == "" {
			subscribeFilter = replyTopic
		}
	} else {
		prefix := opts.ReplyTopicPrefix
		if prefix == "" {
			prefix = "replies"
		}
		replyTopic = prefix + "/" + uuid.New().String()
		subscribeFilter = replyTopic
	}

	// Generate unique correlation data for this call.
	corrID := uuid.New()
	corrData := corrID[:]

	// Channel to receive the reply message.
	replyCh := make(chan *pahomqtt5.Publish, 1)

	// Register reply handler before subscribing. The handler key is the plain
	// replyTopic — the router dispatches on the actual message topic, not the
	// $share-prefixed subscribe filter.
	router.RegisterHandler(replyTopic, func(msg *pahomqtt5.Publish) {
		if msg.Properties == nil || string(msg.Properties.CorrelationData) != string(corrData) {
			return // not our reply
		}
		select {
		case replyCh <- msg:
		default:
		}
	})

	// Subscribe using the filter (may carry a $share prefix).
	if _, err := client.Subscribe(ctx, &pahomqtt5.Subscribe{
		Subscriptions: []pahomqtt5.SubscribeOptions{
			{Topic: subscribeFilter, QoS: qos},
		},
	}); err != nil {
		router.UnregisterHandler(replyTopic)
		callErr = CallError{Kind: KindEncode, Err: fmt.Errorf("subscribe reply topic: %w", err)}
		obs.RecordRequest("MQTT5-REQ", path, 0, time.Since(start))
		return zero, callErr
	}

	// Unsubscribe and deregister on function exit.
	var once sync.Once
	cleanup := func() {
		once.Do(func() {
			router.UnregisterHandler(replyTopic)
			_, _ = client.Unsubscribe(ctx, &pahomqtt5.Unsubscribe{Topics: []string{subscribeFilter}})
		})
	}
	defer cleanup()

	// Encode request payload.
	var payload []byte
	if len(handle.RequestFormats) > 0 {
		payload, callErr = handle.RequestFormats[0].Marshal(req)
	} else {
		payload, callErr = handle.EncodeRequest(req)
	}
	if callErr != nil {
		stats.ReportErrors(obs, "body", callErr)
		callErr = CallError{Kind: KindEncode, Err: callErr}
		obs.RecordRequest("MQTT5-REQ", path, 0, time.Since(start))
		return zero, callErr
	}

	// Publish request with ResponseTopic and CorrelationData.
	reqProps := &pahomqtt5.PublishProperties{
		ResponseTopic:   replyTopic,
		CorrelationData: corrData,
	}
	if len(opts.UserProperties) > 0 {
		reqProps.User = pahomqtt5.UserProperties(opts.UserProperties)
	}

	if _, err := client.Publish(ctx, &pahomqtt5.Publish{
		Topic:      path,
		QoS:        qos,
		Payload:    payload,
		Properties: reqProps,
	}); err != nil {
		callErr = CallError{Kind: KindEncode, Err: fmt.Errorf("publish request: %w", err)}
		obs.RecordRequest("MQTT5-REQ", path, 0, time.Since(start))
		return zero, callErr
	}

	// Wait for reply.
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		callErr = CallError{Kind: KindTimeout, Err: ctx.Err()}
		obs.RecordRequest("MQTT5-REQ", path, 0, time.Since(start))
		return zero, callErr
	case <-timer.C:
		callErr = CallError{Kind: KindTimeout, Err: fmt.Errorf("no reply within %s", timeout)}
		obs.RecordRequest("MQTT5-REQ", path, 0, time.Since(start))
		return zero, callErr
	case replyMsg := <-replyCh:
		// Check for server error reply.
		if isErrorReply(replyMsg) {
			callErr = CallError{Kind: KindHandler, Err: fmt.Errorf("server error: %s", replyMsg.Payload)}
			obs.RecordRequest("MQTT5-REQ", path, 500, time.Since(start))
			return zero, callErr
		}

		// Decode response.
		var resp Resp
		if len(handle.Formats) > 0 {
			resp, callErr = handle.Formats[0].Unmarshal(replyMsg.Payload)
		} else {
			resp, callErr = handle.DecodeResponse(replyMsg.Payload)
		}
		if callErr != nil {
			stats.ReportErrors(obs, "body", callErr)
			callErr = CallError{Kind: KindDecode, Err: fmt.Errorf("decode response: %w", callErr)}
			obs.RecordRequest("MQTT5-REQ", path, 0, time.Since(start))
			return zero, callErr
		}
		obs.RecordRequest("MQTT5-REQ", path, 200, time.Since(start))
		return resp, nil
	}
}

// isErrorReply checks whether an incoming reply was sent as an error by the responder.
// Error replies carry a special ContentType "application/mqtt5-error".
func isErrorReply(msg *pahomqtt5.Publish) bool {
	return msg.Properties != nil &&
		msg.Properties.ContentType == errorReplyContentType
}

// errorReplyContentType is the ContentType set on error replies by [Serve].
const errorReplyContentType = "application/mqtt5-error"

// publishErrorReply sends an error reply to the requester's ResponseTopic.
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
