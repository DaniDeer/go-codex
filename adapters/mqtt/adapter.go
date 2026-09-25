package mqtt

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/stats"
)

// ErrorKind classifies the origin of a [SubscribeError].
type ErrorKind int

const (
	// KindDecode indicates the message payload could not be decoded or
	// failed codec validation.
	KindDecode ErrorKind = iota

	// KindHandler indicates the application handler returned an error after
	// successful decoding.
	KindHandler

	// KindSecurity indicates security enforcement rejected the message.
	KindSecurity
)

func (k ErrorKind) String() string {
	switch k {
	case KindDecode:
		return "decode"
	case KindHandler:
		return "handler"
	case KindSecurity:
		return "security"
	default:
		return "unknown"
	}
}

// SubscribeError is returned to the onErr callback with a typed Kind so callers
// can distinguish decode/validation failures from application handler errors
// without string matching.
type SubscribeError struct {
	Kind  ErrorKind
	Topic string
	Err   error
}

func (e SubscribeError) Error() string {
	return fmt.Sprintf("mqtt %s %s: %v", e.Kind, e.Topic, e.Err)
}

func (e SubscribeError) Unwrap() error { return e.Err }

// As is a thin, self-documenting convenience wrapper over
// errors.As(e.Err, target) — collapses the two-step
// "errors.As(subErr.Err, &target)" dance into "subErr.As(&target)" from
// inside an [SubscribeOptions.OnError] callback. See
// docs/design/d-0005-error-handling.md's Topic 7.
func (e SubscribeError) As(target any) bool {
	return errors.As(e.Err, target)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e SubscribeError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("kind", e.Kind.String()),
		slog.String("topic", e.Topic),
		slog.Any("err", e.Err),
	)
}

// PublishEncodeError is returned by [publish] when encoding the outgoing
// message payload fails (codec validation or marshal error).
//
// Use [errors.As] to extract the topic and underlying error for structured logging:
//
//	var encErr mqtt.PublishEncodeError
//	if errors.As(err, &encErr) {
//	    slog.Error("mqtt publish encode failed",
//	        "topic", encErr.Topic,
//	        "cause", encErr.Err,
//	    )
//	}
type PublishEncodeError struct {
	// Topic is the concrete topic (after template substitution) to which the
	// publish was attempted.
	Topic string
	// Err is the underlying codec validation or marshal error.
	Err error
}

func (e PublishEncodeError) Error() string {
	return fmt.Sprintf("mqtt encode %s: %s", e.Topic, e.Err)
}

// Unwrap allows [errors.Is] and [errors.As] to traverse the underlying error.
func (e PublishEncodeError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e PublishEncodeError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("topic", e.Topic),
		slog.Any("err", e.Err),
	)
}

// contextKey is the unexported type for values stored in context by this package.
type contextKey struct{}

// SubscribeOptions configures [subscribeHandler].
type SubscribeOptions struct {
	// TopicFilter is the MQTT subscription filter passed to [pahomqtt.Client.Subscribe].
	// Use this when the handle's topic template uses {varName} placeholders (e.g.
	// "sensors/{sensorID}/data") but the MQTT broker requires MQTT wildcard syntax
	// (e.g. "sensors/+/data"). When empty, a filter is derived automatically from
	// handle.Topic via [deriveWildcardFilter] (replacing each {varName} placeholder
	// with "+") — the common case needs no manual restatement.
	//
	// Consumed by [SubscribeAdapter] and the internal value-based
	// subscribe/serveOneSubscriber path behind [Attach] — direct
	// [subscribeHandler] callers build and pass the filter to
	// [pahomqtt.Client.Subscribe] themselves (subscribeHandler only builds the
	// [pahomqtt.MessageHandler] closure, it never calls the broker).
	TopicFilter string

	// QoS, when [events.Subscriber.WithOptions] attaches a SubscribeOptions
	// value as a channel's declare-time [events.ChannelHandle.HandlerOpts],
	// is the MQTT quality-of-service level the internal ServeSubscribers
	// path (behind [Attach]) uses to subscribe that channel — there is no
	// other way for ServeSubscribers to learn a per-channel QoS, since it
	// has no call-time qos parameter (unlike the internal subscribe path,
	// whose own qos parameter ALWAYS takes precedence and never reads this
	// field). Defaults to 0.
	QoS byte

	// OnError, when non-nil, is called with a typed [SubscribeError] on decode
	// or application handler failure. If nil, errors are silently discarded.
	OnError func(SubscribeError)

	// Observer, when non-nil, receives per-message lifecycle events: success/
	// failure counts and processing duration per topic. Per-field payload validation
	// errors are reported via [stats.Observer.RecordValidationError] with location
	// "payload". Topic variable errors from [TopicVarsFromMessage] propagated through
	// the handler are reported with location "topic_var" (per-variable codec failures)
	// or "topic" (topic-level codec or structural mismatch).
	// Defaults to [stats.NoopObserver] when nil.
	Observer stats.Observer
}

// MessageFromContext retrieves the [pahomqtt.Message] stored in ctx by the
// message handler built internally by [NewSubscribeTransport] (via
// [events.SubscribeHandle]) or by [Attach]'s own subscription wiring.
// Returns false if the context was not created by this package.
func MessageFromContext(ctx context.Context) (pahomqtt.Message, bool) {
	msg, ok := ctx.Value(contextKey{}).(pahomqtt.Message)
	return msg, ok
}

// subscribeHandler returns a [pahomqtt.MessageHandler] that decodes the message
// payload using handle's codec, validates it, and calls fn.
//
// ctx is threaded through to fn for cancellation and deadline propagation.
// Pass [SubscribeOptions] to handle errors and observe lifecycle events. Pass a
// zero-value [SubscribeOptions]{} for no-op behaviour.
//
// The optional formats parameter specifies the payload format to use for
// decoding. When provided, the first format is used; when omitted, the default
// JSON codec of the channel handle is used. MQTT 3.1.1 carries no content-type
// metadata, so the format must be agreed out-of-band per subscription.
//
// The Topic field of [SubscribeError] reflects the concrete topic of the incoming
// message (from msg.Topic()), which is useful when the channel was registered with
// a template topic.
//
// subscribeHandler is an internal mechanical primitive only — it builds the
// callback but does not itself subscribe or block. Application code should
// use [NewSubscribeTransport] together with [events.SubscribeHandle], or
// [Attach]'s [events.Client]-based workflow, instead.
//
// When fn returns a non-nil error, a declared [events.ErrorChannel] on
// handle is consulted via handle.ErrorResponseFor — on an
// [events.ErrorRespond] match, the typed payload is published (via
// client) to the declared error-output topic BEFORE falling through to
// opts.OnError; any other action (or no match) falls through to
// opts.OnError unchanged, identical to this function's pre-existing
// behavior. Mirrors mqtt5's makeSubscribeMessageHandler's identical
// extension — see docs/design/d-0002-pubsub-workflow-simplification.md's
// Decision 8.

// tryPublishErrorChannel is the RECOMMENDED single call site for every
// Category-A failure point on the subscribe side (docs/roadmap/
// d-0005-error-handling.md's Topic 1/5) — mirrors mqtt5's
// identical helper exactly, using this package's own token-based
// Publish API.
//
// Returns (handled, matched bool) — see mqtt5's identical helper's doc
// comment for the full 3-outcome contract (handled=return immediately;
// matched-with-!handled=skip tryDeadLetter but still call opts.OnError;
// !matched=fall through to tryDeadLetter as before). Session-review
// round-3 fix (G4): a type-matched ErrorChannel with a non-Respond
// action must never ALSO be dead-lettered — Topic 4 scopes DeadLetter
// to the genuinely UNMATCHED case only.
func tryPublishErrorChannel[T any](
	ctx context.Context, client pahomqtt.Client, handle *events.ChannelHandle[T], obs stats.Observer, err error,
) (handled, matched bool) {
	resp, isMatch, matchErr := handle.ObserveErrorResponseFor(ctx, obs, err)
	if !isMatch || matchErr != nil {
		return false, false
	}
	if resp.Action != "" && resp.Action != events.ErrorRespond {
		return false, true
	}
	token := client.Publish(resp.Topic, 0, false, resp.Body)
	token.Wait()
	if pubErr := token.Error(); pubErr != nil {
		stats.ReportErrors(obs, "error_channel", pubErr)
	}
	return true, true
}

// tryDeadLetter is the RECOMMENDED single call site for Topic 4's
// dead-letter fallback (docs/design/d-0005-error-handling.md)
// — mirrors mqtt5's identical helper exactly, using this package's own
// token-based Publish API.
func tryDeadLetter[T any](
	client pahomqtt.Client, handle *events.ChannelHandle[T], obs stats.Observer, sourceTopic string, rawPayload []byte, err error,
) bool {
	topic, body, ok := handle.DeadLetterFor(obs, sourceTopic, rawPayload, err)
	if !ok {
		return false
	}
	token := client.Publish(topic, 0, false, body)
	token.Wait()
	return true
}

func subscribeHandler[T any](
	ctx context.Context,
	client pahomqtt.Client,
	handle *events.ChannelHandle[T],
	fn func(context.Context, T) error,
	opts SubscribeOptions,
	formats ...format.Format[T],
) pahomqtt.MessageHandler {
	obs := opts.Observer
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}
	return func(_ pahomqtt.Client, msg pahomqtt.Message) {
		start := time.Now()
		ctx := context.WithValue(ctx, contextKey{}, msg)
		// The channel's OWN declaration (WithFormats/WithSubscribeFormats)
		// is the single source of truth for which format applies —
		// DecodeWithFormats resolves it (call-time formats override >
		// handle.SubscribeFormats > handle.Formats > JSON fallback), this
		// adapter never duplicates that resolution logic itself.
		value, err := handle.DecodeWithFormats(msg.Payload(), formats...)
		if err != nil {
			reportPayloadErrors(err, obs)
			obs.RecordSubscribe(msg.Topic(), false, time.Since(start))
			if handled, matched := tryPublishErrorChannel(ctx, client, handle, obs, err); handled {
				return
			} else if !matched {
				if tryDeadLetter(client, handle, obs, msg.Topic(), msg.Payload(), err) {
					return
				}
			}
			if opts.OnError != nil {
				opts.OnError(SubscribeError{Kind: KindDecode, Topic: msg.Topic(), Err: err})
			}
			return
		}

		// Merge topic variables declared via events.NewTopicParam into the
		// SAME decoded value — additive, only runs when the channel has
		// merge-capable topic params (backward compatible: identical
		// behavior to today when none are declared). Mirrors mqtt5's
		// makeSubscribeMessageHandler wiring.
		//
		// topicVars is ALSO needed by codec-backed middleware dispatch
		// below (Transform/bundled .Use()) — computed once here, unioned
		// with the mergeFields>0 gate so it's derived whenever EITHER
		// needs it.
		var topicVars map[string]string
		if mergeFields := handle.MergeFields(); len(mergeFields) > 0 || len(handle.MiddlewareHandlers) > 0 {
			vars, varErr := TopicVarsFromMessage(handle, msg)
			if varErr != nil {
				reportTopicMismatchErrors(varErr, obs)
				reportInvalidTopicErrors(varErr, obs)
				stats.ReportErrors(obs, "topic_var", varErr)
				obs.RecordSubscribe(msg.Topic(), false, time.Since(start))
				if handled, matched := tryPublishErrorChannel(ctx, client, handle, obs, varErr); handled {
					return
				} else if !matched {
					if tryDeadLetter(client, handle, obs, msg.Topic(), msg.Payload(), varErr) {
						return
					}
				}
				if opts.OnError != nil {
					opts.OnError(SubscribeError{Kind: KindDecode, Topic: msg.Topic(), Err: varErr})
				}
				return
			}
			topicVars = vars
			if mergeErr := codex.DecodeVars(&value, vars, mergeFields...); mergeErr != nil {
				stats.ReportErrors(obs, "topic_var", mergeErr)
				obs.RecordSubscribe(msg.Topic(), false, time.Since(start))
				if handled, matched := tryPublishErrorChannel(ctx, client, handle, obs, mergeErr); handled {
					return
				} else if !matched {
					if tryDeadLetter(client, handle, obs, msg.Topic(), msg.Payload(), mergeErr) {
						return
					}
				}
				if opts.OnError != nil {
					opts.OnError(SubscribeError{Kind: KindDecode, Topic: msg.Topic(), Err: mergeErr})
				}
				return
			}
		}

		// Enforce security: per-operation requirements take precedence; nil falls
		// back to global security declared via Builder.AddGlobalSecurity.
		var secReqs []route.SecurityRequirement
		if handle.Descriptor.Subscribe != nil {
			secReqs = handle.Descriptor.Subscribe.Security
		}
		if secReqs == nil {
			secReqs = handle.GlobalSecurity
		}
		if len(secReqs) > 0 {
			if credErr := validateSecurityCredentials(msg, secReqs, handle.SecuritySchemes); credErr != nil {
				if secObs, ok := obs.(stats.SecurityObserver); ok {
					secObs.RecordSecurityRejection(msg.Topic(), route.FirstSchemeName(secReqs))
				}
				obs.RecordSubscribe(msg.Topic(), false, time.Since(start))
				if opts.OnError != nil {
					opts.OnError(SubscribeError{Kind: KindSecurity, Topic: msg.Topic(), Err: credErr})
				}
				return
			}
		}

		// Implementations-based security dispatch (events.Subscriber.
		// SubscribeMW-attached security-shaped Fns) — SAME pre-handler
		// dispatch point the built-in credential check above just ran
		// at, moved HERE (session-review fix: previously lived in
		// caller.go's subscribeHandle, coupled into fn's own error path,
		// so a rejection was indistinguishable from a plain handler
		// error — misreported as KindHandler, never events.SecurityError,
		// never ErrorChannel/DeadLetter-eligible under its own security
		// classification). Mirrors mqtt5's/zeromq's identical placement
		// and events.SecurityError wrapping exactly.
		if len(handle.Implementations) > 0 {
			if err := runSubscribeSecurityImpls(ctx, msg, &value, secReqs, handle.Implementations); err != nil {
				if secObs, ok := obs.(stats.SecurityObserver); ok {
					secObs.RecordSecurityRejection(msg.Topic(), route.FirstSchemeName(secReqs))
				}
				obs.RecordSubscribe(msg.Topic(), false, time.Since(start))
				wrapped := events.SecurityError{Err: err}
				if handled, matched := tryPublishErrorChannel(ctx, client, handle, obs, wrapped); handled {
					return
				} else if !matched {
					if tryDeadLetter(client, handle, obs, msg.Topic(), msg.Payload(), wrapped) {
						return
					}
				}
				if opts.OnError != nil {
					opts.OnError(SubscribeError{Kind: KindSecurity, Topic: msg.Topic(), Err: wrapped})
				}
				return
			}
		}

		// Codec-backed middleware dispatch (Transform and bundled .Use())
		// — SAME pre-handler dispatch point plain security enforcement
		// above just ran at (D1), reusing the SAME topicVars derived
		// above. A fn error is ErrorPattern-eligible (D2), falling back
		// to events.MiddlewareError when unmatched — mirrors mqtt5's
		// identical resolution.
		if len(handle.MiddlewareHandlers) > 0 {
			// mqtt (v3) has no property mechanism at all — passes nil for
			// propertyVars, mirroring zeromq's identical carve-out.
			if mwErr := events.DispatchSubscribeMiddlewareHandlers(ctx, &value, handle.MiddlewareHandlers, topicVars, nil); mwErr != nil {
				obs.RecordSubscribe(msg.Topic(), false, time.Since(start))
				dispatchErr, _ := events.AsMiddlewareDispatchError(mwErr)
				if dispatchErr.IsFnError {
					stats.ReportErrors(obs, "middleware:fn", dispatchErr.Err)
					if handled, matched := tryPublishErrorChannel(ctx, client, handle, obs, dispatchErr.Err); handled {
						return
					} else if !matched {
						if tryDeadLetter(client, handle, obs, msg.Topic(), msg.Payload(), dispatchErr.Err) {
							return
						}
					}
					if opts.OnError != nil {
						opts.OnError(SubscribeError{Kind: KindHandler, Topic: msg.Topic(), Err: events.MiddlewareError{Name: dispatchErr.Name, Err: dispatchErr.Err}})
					}
					return
				}
				stats.ReportErrors(obs, "middleware:in", dispatchErr.Err)
				if handled, matched := tryPublishErrorChannel(ctx, client, handle, obs, dispatchErr.Err); handled {
					return
				} else if !matched {
					if tryDeadLetter(client, handle, obs, msg.Topic(), msg.Payload(), dispatchErr.Err) {
						return
					}
				}
				if opts.OnError != nil {
					opts.OnError(SubscribeError{Kind: KindDecode, Topic: msg.Topic(), Err: dispatchErr.Err})
				}
				return
			}
		}

		if to, ok := obs.(stats.TraceObserver); ok {
			ctx = to.StartSpan(ctx, "mqtt.subscribe", msg.Topic())
			defer func() { to.EndSpan(ctx, err) }()
		}

		if err = fn(ctx, value); err != nil {
			reportTopicMismatchErrors(err, obs)
			reportInvalidTopicErrors(err, obs)
			stats.ReportErrors(obs, "topic_var", err)
			obs.RecordSubscribe(msg.Topic(), false, time.Since(start))
			if handled, matched := tryPublishErrorChannel(ctx, client, handle, obs, err); handled {
				return
			} else if !matched {
				if tryDeadLetter(client, handle, obs, msg.Topic(), msg.Payload(), err) {
					return
				}
			}
			if opts.OnError != nil {
				opts.OnError(SubscribeError{Kind: KindHandler, Topic: msg.Topic(), Err: err})
			}
			return
		}
		obs.RecordSubscribe(msg.Topic(), true, time.Since(start))
	}
}

// reportPayloadErrors extracts per-field validation errors from a payload decode
// error and reports them to obs with location "payload".
func reportPayloadErrors(err error, obs stats.Observer) {
	stats.ReportErrors(obs, "payload", err)
}

// reportInvalidTopicErrors extracts the constraint from an [events.InvalidTopicError]
// and reports it to obs with location "topic".
func reportInvalidTopicErrors(err error, obs stats.Observer) {
	var ie events.InvalidTopicError
	if !errors.As(err, &ie) {
		return
	}
	obs.RecordValidationError("topic", stats.ConstraintName(ie.Err), "")
}

// reportTopicMismatchErrors reports a [TopicMismatchError] to obs with location "topic"
// and constraint name "topic-mismatch".
func reportTopicMismatchErrors(err error, obs stats.Observer) {
	var mm TopicMismatchError
	if !errors.As(err, &mm) {
		return
	}
	obs.RecordValidationError("topic", "topic-mismatch", "")
}

// PublishOptions configures [publish]/[publishHandle]. Generic over T so
// declarative security implementations (attached via
// [events.Publisher.PublishMW]) can grant write-access to the outgoing
// message before it is encoded.
type PublishOptions[T any] struct {
	// Observer, when non-nil, receives per-publish lifecycle events:
	// [stats.Observer.RecordPublish] is called with success=true on broker
	// acknowledgement and success=false on encode failure, broker error, or
	// context cancellation. Per-field payload encode errors are reported via
	// [stats.Observer.RecordValidationError] with location "payload".
	// Topic variable errors from [events.ChannelHandle.BuildTopic] are reported
	// with location "topic_var" (per-variable codec failures) or "topic"
	// (topic-level codec failures).
	// Defaults to [stats.NoopObserver] when nil.
	Observer stats.Observer
}

// publish encodes msg using handle's codec and publishes it to the broker.
//
// vars controls the topic used for publishing:
//   - nil: publish to handle.Topic directly (use for static topics).
//   - non-nil: call handle.BuildTopic(vars) to build a concrete topic from the
//     template, validating each variable against its registered codec. An error
//     is returned if any variable is missing or fails validation.
//
// Pass [PublishOptions] to observe publish lifecycle events via a [stats.Observer].
// Pass a zero-value [PublishOptions]{} for no-op behaviour.
//
// The optional formats parameter specifies the payload format to use for
// encoding. When provided, the first format is used; when omitted, the default
// JSON codec of the channel handle is used.
//
// publish is an internal mechanical primitive only. Application code should
// use one of the two public entry points instead:
//
// Example — [NewPublishTransport] + [events.PublishHandle] (handle-based,
// no manual client plumbing):
//
//	transport := mqtt.NewPublishTransport[NotificationCommand](client, 1, false, mqtt.PublishOptions[NotificationCommand]{})
//	err := events.PublishHandle(ctx, notifChannel.WithPublish(events.Publish{}), transport, notification)
//
// Example — [Attach] + [events.Client.Publish] (full pub/sub workflow,
// static or template topics resolved from msg automatically):
//
//	_ = mqtt.Attach(eventsClient, client)
//	err := eventsClient.Publish(ctx, notifChannel.WithPublish(events.Publish{}), notification)
//
// publish waits for broker acknowledgement, respecting ctx cancellation. If the
// context is cancelled before the broker responds, ctx.Err() is returned.
//
// Every attached [events.ChannelHandle.ClientImplementations] Fn (from
// [events.Publisher.PublishMW]) is validated EAGERLY here, before any
// encoding or network activity, via [validatePublishImplementationShapes] —
// a malformed Fn fails loudly and immediately. Security-shaped
// implementations (func(context.Context, *T, []route.SecurityRequirement)
// error) run in attachment order, mutating msg. General-purpose Fns
// (func(func(context.Context, T) error) func(context.Context, T) error)
// wrap the internal "encode + transmit" step, outermost-in — this lets a
// PublishMW-attached Fn add tracing, mutate/log msg, or implement retry
// logic around the actual encode/publish call.
func publish[T any](ctx context.Context, client pahomqtt.Client, handle *events.ChannelHandle[T], qos byte, retained bool, msg T, vars map[string]string, opts PublishOptions[T], formats ...format.Format[T]) error {
	obs := opts.Observer
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}
	start := time.Now()
	var err error
	if to, ok := obs.(stats.TraceObserver); ok {
		ctx = to.StartSpan(ctx, "mqtt.publish", handle.Topic)
		defer func() { to.EndSpan(ctx, err) }()
	}

	if err = validatePublishImplementationShapes[T](handle.ClientImplementations); err != nil {
		obs.RecordPublish(handle.Topic, false, time.Since(start))
		return err
	}

	// Codec-backed middleware dispatch (ClientTransform and bundled
	// .Use()) — derived FIRST since a middleware's own Out may contribute
	// ADDITIONAL topic vars BuildTopic needs. D3-equivalent precedence:
	// explicit/channel-own vars (the vars param) wins over
	// middleware-derived vars on a key collision. Mirrors mqtt5's
	// identical wiring.
	if len(handle.ClientMiddlewareHandlers) > 0 {
		// mqtt (v3) has no property mechanism — discards the propertyVars
		// return, mirroring zeromq's identical carve-out.
		mwVars, _, mwErr := events.DispatchPublishMiddlewareHandlers(ctx, msg, handle.ClientMiddlewareHandlers)
		if mwErr != nil {
			loc := "middleware:fn"
			// dispatchErr.Err is ALREADY a properly-wrapped
			// events.MiddlewareError (fn case) or events.MiddlewareOutputError
			// (encode case) — see api/events/transform.go's buildEncodeOut
			// and events.DispatchPublishMiddlewareHandlers — so BOTH
			// branches unwrap to the already-typed inner error here, no
			// re-wrap needed.
			reported := mwErr
			if dispatchErr, ok := events.AsMiddlewareDispatchError(mwErr); ok {
				if dispatchErr.IsEncodeErr {
					loc = "middleware:out"
				}
				reported = dispatchErr.Err
				mwErr = dispatchErr.Err
			}
			stats.ReportErrors(obs, loc, reported)
			obs.RecordPublish(handle.Topic, false, time.Since(start))
			err = mwErr
			// Topic 4: a failed publish (never reached the broker) is
			// ALSO dead-letterable, alongside the synchronous error
			// returned to the caller.
			bestEffortPayload, _ := handle.Encode(msg)
			tryDeadLetter(client, handle, obs, handle.Topic, bestEffortPayload, err)
			return err
		}
		vars = events.OverrideDerivedVars(mwVars, vars)
	}

	topic := handle.Topic
	if vars != nil {
		var err2 error
		topic, err2 = handle.BuildTopic(vars)
		if err2 != nil {
			err = err2
			reportInvalidTopicErrors(err2, obs)
			stats.ReportErrors(obs, "topic_var", err2)
			obs.RecordPublish(handle.Topic, false, time.Since(start))
			return err
		}
	}

	// Resolve effective security requirements (per-operation overrides
	// global), then run security-shaped implementations — they grant
	// write-access to msg, mirroring subscribeHandler's server-side
	// security-shaped read/write access.
	var secReqs []route.SecurityRequirement
	if handle.Descriptor.Publish != nil {
		secReqs = handle.Descriptor.Publish.Security
	}
	if secReqs == nil {
		secReqs = handle.GlobalSecurity
	}
	if len(secReqs) > 0 {
		if err = runPublishSecurityImpls(ctx, &msg, secReqs, handle.ClientImplementations); err != nil {
			obs.RecordPublish(topic, false, time.Since(start))
			return err
		}
	}

	transmit := func(_ context.Context, m T) error {
		// The channel's OWN declaration (WithFormats/WithPublishFormats)
		// is the single source of truth for which format applies —
		// EncodeWithFormats resolves it, this adapter never duplicates
		// that resolution logic itself.
		payload, encErr := handle.EncodeWithFormats(m, formats...)
		if encErr != nil {
			reportPayloadErrors(encErr, obs)
			obs.RecordPublish(topic, false, time.Since(start))
			pubErr := PublishEncodeError{Topic: topic, Err: encErr}
			// Topic 4: even an encode failure is dead-letterable — the
			// raw pre-encode payload doesn't exist, so fall back to a
			// best-effort re-encode via handle.Encode for the envelope.
			bestEffortPayload, _ := handle.Encode(m)
			tryDeadLetter(client, handle, obs, topic, bestEffortPayload, pubErr)
			return pubErr
		}
		token := client.Publish(topic, qos, retained, payload)
		select {
		case <-ctx.Done():
			obs.RecordPublish(topic, false, time.Since(start))
			return ctx.Err()
		case <-token.Done():
			if token.Error() != nil {
				obs.RecordPublish(topic, false, time.Since(start))
				tryDeadLetter(client, handle, obs, topic, payload, token.Error())
				return token.Error()
			}
			obs.RecordPublish(topic, true, time.Since(start))
			return nil
		}
	}
	transmit = wrapPublishGeneral(transmit, handle.ClientImplementations)
	err = transmit(ctx, msg)
	return err
}

// runPublishSecurityImpls runs every attached
// [middleware.ClientImplementation] whose Fn matches the security shape
// (func(context.Context, *T, []route.SecurityRequirement) error) IN
// ATTACHMENT ORDER, fail-fast on the first error — the mqtt v3 mirror of
// subscribeHandler's server-side security-shaped implementation check, but
// client-side and generic over T (concrete at this call site). General-purpose
// wrapping-shaped Fns are silently skipped here (consumed instead by
// [wrapPublishGeneral]).
func runPublishSecurityImpls[T any](ctx context.Context, msg *T, secReqs []route.SecurityRequirement, impls []middleware.ClientImplementation) error {
	for _, impl := range impls {
		fn, ok := impl.Fn.(func(context.Context, *T, []route.SecurityRequirement) error)
		if !ok {
			continue // general-purpose or nil
		}
		if len(impl.Satisfies) > 0 && len(secReqs) == 0 {
			continue
		}
		if err := fn(ctx, msg, secReqs); err != nil {
			return err
		}
	}
	return nil
}

// wrapPublishGeneral wraps fn (the "encode + transmit" step) with every
// general-purpose Fn found in impls (shape func(next func(context.Context, T)
// error) func(context.Context, T) error), OUTERMOST-in, in attachment order
// (impls[0] is outermost) — deliberate symmetry with [wrapSubscribeGeneral].
func wrapPublishGeneral[T any](fn func(context.Context, T) error, impls []middleware.ClientImplementation) func(context.Context, T) error {
	for i := len(impls) - 1; i >= 0; i-- {
		wrap, ok := impls[i].Fn.(func(func(context.Context, T) error) func(context.Context, T) error)
		if !ok {
			continue
		}
		fn = wrap(fn)
	}
	return fn
}

// validatePublishImplementationShapes checks every attached impl.Fn against
// the two shapes [events.Publisher.PublishMW] recognizes for T — the
// security shape (func(context.Context, *T, []route.SecurityRequirement)
// error) or the general-purpose wrapping shape (func(next
// func(context.Context, T) error) func(context.Context, T) error) — EAGERLY
// at [publish] construction time rather than deferring to the first
// outgoing message. Mirrors [validateSubscribeImplementationShapes].
func validatePublishImplementationShapes[T any](impls []middleware.ClientImplementation) error {
	for _, impl := range impls {
		if impl.Fn == nil {
			continue
		}
		switch impl.Fn.(type) {
		case func(context.Context, *T, []route.SecurityRequirement) error:
		case func(func(context.Context, T) error) func(context.Context, T) error:
		default:
			return middleware.MiddlewareShapeError{
				Name:     impl.Name,
				Expected: "func(context.Context, *T, []route.SecurityRequirement) error or func(func(context.Context, T) error) func(context.Context, T) error",
				Got:      fmt.Sprintf("%T", impl.Fn),
			}
		}
	}
	return nil
}

// publishHandle is the single-call convenience wrapper around [publish]: it
// derives the topic vars map from msg automatically, using the channel's
// merge-capable topic params ([events.ChannelHandle.MergeFields] +
// [codex.EncodeVars]) — one struct in, no manual vars map, mirroring
// mqtt5's own PublishHandle convenience for MQTT 5 events.
//
// [publish] remains available as the lower-level escape hatch for callers
// that build the vars map themselves (e.g. no merge fields declared, or
// vars come from a non-struct source).
//
// publishHandle is an internal mechanical primitive — see [publish]'s doc
// comment for the two public entry points ([NewPublishTransport] and
// [Attach]) that wrap it.
func publishHandle[T any](
	ctx context.Context,
	client pahomqtt.Client,
	handle *events.ChannelHandle[T],
	qos byte,
	retained bool,
	msg T,
	opts PublishOptions[T],
	formats ...format.Format[T],
) error {
	vars, err := codex.EncodeVars(msg, handle.MergeFields()...)
	if err != nil {
		return err
	}
	if len(vars) == 0 {
		vars = nil
	}
	return publish(ctx, client, handle, qos, retained, msg, vars, opts, formats...)
}

// validateSecurityCredentials checks registered SecurityScheme codecs against
// credentials extracted from the MQTT message.
//
// Because the paho.mqtt.golang library does not expose MQTT 5.0 User Properties
// through the pahomqtt.Message interface, credential extraction is always a
// no-op and Codec validation is deliberately skipped. Use a security-shaped
// SubscribeMW-paired implementation with MessageFromContext for runtime
// credential inspection instead.
func validateSecurityCredentials(_ pahomqtt.Message, _ []route.SecurityRequirement, _ map[string]events.SecurityScheme) error {
	// Codec validation skipped: pahomqtt.Message (MQTT 3.1.1) does not expose
	// per-message credentials. Use a security-shaped SubscribeMW-paired
	// implementation for runtime enforcement instead.
	return nil
}
