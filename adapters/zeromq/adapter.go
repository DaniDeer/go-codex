package zeromq

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/stats"
)

// recvPollInterval is the receive timeout used by blocking loops so that
// context cancellation is checked at least this often.
const recvPollInterval = 100 * time.Millisecond

// statusOK and statusError are the status frame values for REQ/REP framing.
var (
	statusOK    = []byte("ok")
	statusError = []byte("error")
)

// SubscribeOptions configures [subscribe]/[subscribeWithHandle]. Generic
// over T so declarative security implementations (attached via
// [events.Subscriber.SubscribeMW]) can grant read/write access to the
// decoded message — zeromq's [topic, payload] frames carry nothing beyond
// what's already decoded into T, so there is no raw-message-equivalent
// parameter to pass instead (unlike mqtt5's *pahomqtt5.Publish) — see
// docs/design/d-0002-pubsub-workflow-simplification.md's Decision 3 "zeromq —
// message-level mechanism now TRACTABLE" subsection.
type SubscribeOptions[T any] struct {
	// TopicFilter is the ZeroMQ SUB-socket prefix filter passed to
	// [FramedSocket.SetSubscription]. Use this when the handle's topic
	// template uses {varName} placeholders (e.g.
	// "sensors/{sensorID}/data") — ZeroMQ subscription filtering is plain
	// byte-prefix matching (no wildcard concept), so a literal template
	// string never matches a real published topic. When empty (the common
	// case), a prefix filter is derived automatically from handle.Topic
	// via [deriveTopicPrefix] (returns everything up to the first "{"
	// placeholder). Set explicitly only for a filter that differs from
	// this derivation. BUG FIX this pass — see
	// docs/design/d-0002-pubsub-workflow-simplification.md's "Confirmed bug,
	// fixed this pass" subsection: a templated topic's placeholders were
	// previously sent VERBATIM as the subscription filter, which never
	// matches any real published topic.
	TopicFilter string

	// OnError, when non-nil, is called with a typed [SubscribeError] on decode,
	// handler, or security failure. If nil, errors are silently discarded.
	OnError func(SubscribeError)

	// Observer, when non-nil, receives per-message lifecycle events:
	// [stats.Observer.RecordSubscribe] is called with success=true on clean
	// handler completion and success=false on any failure. Per-field payload
	// validation errors are reported via [stats.Observer.RecordValidationError]
	// with location "payload".
	// Defaults to [stats.NoopObserver] when nil.
	Observer stats.Observer
}

// PublishOptions configures [publish]/[publishHandle]. Generic over T
// (BREAKING change from the previous non-generic PublishOptions) for the
// SAME reason as [SubscribeOptions] — see its doc comment.
type PublishOptions[T any] struct {
	// Observer, when non-nil, receives per-publish lifecycle events:
	// [stats.Observer.RecordPublish] is called with success=true on broker send
	// and success=false on encode failure or send error. Per-field payload encode
	// errors are reported via [stats.Observer.RecordValidationError] with location
	// "payload". Topic variable errors are reported with location "topic_var".
	// Defaults to [stats.NoopObserver] when nil.
	Observer stats.Observer
}

// ServeOptions configures [Serve].
type ServeOptions struct {
	// OnError, when non-nil, is called with a typed [ServeError] on decode,
	// handler, or encode failure. The REP socket always sends an error reply
	// frame to avoid leaving the REQ peer stuck; OnError is informational.
	// If nil, errors are silently discarded (the error reply is still sent).
	OnError func(ServeError)

	// Observer, when non-nil, receives per-request lifecycle events:
	// [stats.Observer.RecordRequest] is called with method "ZMQ-REP", the
	// route path, status 200 on success, and status 0 on failure.
	// Per-field validation errors are reported with location "body".
	// Defaults to [stats.NoopObserver] when nil.
	Observer stats.Observer
}

// CallOptions configures [Call] and [CallDealer].
type CallOptions struct {
	// Observer, when non-nil, receives per-call lifecycle events:
	// [stats.Observer.RecordRequest] is called with method "ZMQ-REQ" or
	// "ZMQ-DEALER", the route path, status 200 on success, and status 0 or
	// 500 on failure. Per-field decode errors are reported with location "body".
	// Topic variable errors are reported with location "topic_var".
	// Defaults to [stats.NoopObserver] when nil.
	Observer stats.Observer

	// Vars, when non-nil, substitutes {varName} placeholders in the route topic
	// template before encoding. Uses [reqreply.RouteHandle.BuildTopic] to
	// resolve and codec-validate each variable.
	//
	// In ZMQ REQ/REP, the resolved topic is used for observer reporting only —
	// the actual routing is socket-based. Validation still runs on each variable.
	//
	// Example — template topic "compute/{tenantID}/add":
	//
	//	zeromq.Call(ctx, sock, handle, req,
	//	    zeromq.CallOptions{Vars: map[string]string{"tenantID": "acme"}})
	//
	// Returns [CallError] wrapping [reqreply.RouteParamError] or
	// [reqreply.MissingRouteParamError] on validation failure.
	Vars map[string]string

	// RequestFormats, when non-nil, OVERRIDES the route's declared request
	// encode format for THIS call only. Type-erased ([]format.Format[Req])
	// since CallOptions itself is not generic; [Call]/[CallDealer]
	// type-assert it once Req is concrete, returning [CallError] on a
	// type mismatch — mirrors [nethttp.CallOptions.RequestFormats]
	// exactly.
	//
	// Priority: RequestFormats (this field) > handle.RequestFormats
	// (route-declared) > handle.EncodeRequest (JSON default).
	RequestFormats any

	// ResponseFormats, when non-nil, OVERRIDES the route's declared
	// response decode format for THIS call only — same type-erasure and
	// priority-chain contract as [CallOptions.RequestFormats], mirrored
	// for the response direction ([]format.Format[Resp]).
	ResponseFormats any
}

// validateSubscribeImplementationShapes checks every attached impl.Fn
// against the two shapes [events.Subscriber.SubscribeMW] recognizes for T
// — the security shape (func(context.Context, *T,
// []route.SecurityRequirement) error) or the general-purpose wrapping
// shape (func(next func(context.Context, T) error) func(context.Context,
// T) error) — EAGERLY at [subscribeWithHandle] construction time rather
// than deferring to the first incoming message. Mirrors
// adapters/mqtt5.validateSubscribeImplementationShapes, adapted for
// zeromq's simpler (no-grants) security shape.
func validateSubscribeImplementationShapes[T any](impls []middleware.ServerImplementation) error {
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

// runSubscribeSecurityImpls runs every attached
// [middleware.ServerImplementation] whose Fn matches the security shape
// (func(context.Context, *T, []route.SecurityRequirement) error) IN
// ATTACHMENT ORDER, fail-fast on the first error — the zeromq mirror of
// [adapters/mqtt5.runSubscribeSecurityImpls], simplified: zeromq's Fn
// shape returns a plain error (no grants map to merge/CheckScopes,
// unlike mqtt5's User-Property-driven design) since zeromq has no
// built-in credential-extraction mechanism of its own. General-purpose
// wrapping-shaped Fns are silently skipped here (consumed instead by
// [wrapSubscribeGeneral]).
func runSubscribeSecurityImpls[T any](ctx context.Context, msg *T, secReqs []route.SecurityRequirement, impls []middleware.ServerImplementation) error {
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

// wrapSubscribeGeneral wraps fn with every general-purpose Fn found in
// impls (shape func(next func(context.Context, T) error) func(context.Context, T) error),
// OUTERMOST-in, in attachment order — mirrors
// [adapters/mqtt5.wrapSubscribeGeneral] exactly. This is the mechanism
// [Observability] uses. Security-shaped Fns are silently skipped here
// (consumed instead by [runSubscribeSecurityImpls]).
func wrapSubscribeGeneral[T any](fn func(context.Context, T) error, impls []middleware.ServerImplementation) func(context.Context, T) error {
	for i := len(impls) - 1; i >= 0; i-- {
		wrap, ok := impls[i].Fn.(func(func(context.Context, T) error) func(context.Context, T) error)
		if !ok {
			continue
		}
		fn = wrap(fn)
	}
	return fn
}

// SubscribeWithHandle blocks and processes incoming messages from a SUB or
// PULL socket, dispatching decoded values to fn. RENAMED from this
// package's previous bare Subscribe (BREAKING — existing callers of the
// old handle-based Subscribe must rename their call site to
// SubscribeWithHandle to keep identical behavior); see [subscribe] for the
// new value-based convenience that takes a [*Caller] and an
// [events.Subscriber] instead. This is the handle-based primitive, kept
// as the lower-level path on raw sock+handle params — used directly by
// ports/SubscribeAdapter-style callers who already own a pre-built
// handle (confirmed via code: zeromq's own [ports]-binding
// zmqSubscribeAdapter DOES call this exported function, unlike mqtt5's
// ports binding — see this package's migration notes).
//
// The broker subscription filter resolves opts.TopicFilter if non-empty,
// else a prefix derived from handle.Topic via [deriveTopicPrefix] (fixes
// a pre-existing bug where a templated topic's placeholders were sent
// VERBATIM as the subscription filter, which never matches any real
// published topic — see
// docs/design/d-0002-pubsub-workflow-simplification.md's wildcard/prefix
// bug-fix subsection). A non-templated topic's behavior is unchanged. For
// PULL sockets the filter is a no-op — call
// [FramedSocket.SetSubscription]("") separately if needed.
//
// Messages are expected in [topic, payload] frame format. The topic frame is
// used for observer reporting; the payload frame is decoded by the codec.
//
// Every attached [events.ChannelHandle.Implementations] Fn (from
// [events.Subscriber.SubscribeMW]) is validated EAGERLY here, before the
// broker subscription is made, via [validateSubscribeImplementationShapes]
// — a malformed Fn fails loudly and immediately, never silently at
// message time. General-purpose Fns wrap fn ([wrapSubscribeGeneral]);
// security-shaped Fns run per-message ([runSubscribeSecurityImpls]),
// AFTER the built-in codec-based credential check.
//
// The loop runs until ctx is cancelled (returns nil) or a socket error occurs
// (returns the error). Run SubscribeWithHandle in a dedicated goroutine.
//
// The optional formats parameter overrides the channel handle's default JSON
// codec. Priority: call-time formats > handle.SubscribeFormats > handle.Formats
// > handle.Decode (JSON fallback).
//
// Example (PUB/SUB sensor readings):
//
//	go func() {
//	    if err := zeromq.SubscribeWithHandle(ctx, sock, readingsHandle, func(ctx context.Context, r SensorReading) error {
//	        return store.Save(ctx, r)
//	    }, zeromq.SubscribeOptions[SensorReading]{Observer: obs}); err != nil {
//	        log.Error("subscribe stopped", "err", err)
//	    }
//	}()
//
// tryPublishErrorChannel is the RECOMMENDED single call site for every
// Category-A failure point on the subscribe side (docs/roadmap/
// error-handling-rest-events-reqreply.md's Topic 1/5) — mirrors mqtt5's
// identical helper exactly, using this package's own frame-based
// SendFrames API.
//
// Returns (handled, matched bool) — see mqtt5's identical helper's doc
// comment for the full 3-outcome contract (handled=return/continue
// immediately; matched-with-!handled=skip tryDeadLetter but still call
// opts.OnError; !matched=fall through to tryDeadLetter as before).
// Session-review round-3 fix (G4): a type-matched ErrorChannel with a
// non-Respond action must never ALSO be dead-lettered — Topic 4 scopes
// DeadLetter to the genuinely UNMATCHED case only.
func tryPublishErrorChannel[T any](
	ctx context.Context, sock FramedSocket, handle *events.ChannelHandle[T], obs stats.Observer, err error,
) (handled, matched bool) {
	resp, isMatch, matchErr := handle.ObserveErrorResponseFor(ctx, obs, err)
	if !isMatch || matchErr != nil {
		return false, false
	}
	if resp.Action != "" && resp.Action != events.ErrorRespond {
		return false, true
	}
	if pubErr := sock.SendFrames([][]byte{[]byte(resp.Topic), resp.Body}); pubErr != nil {
		stats.ReportErrors(obs, "error_channel", pubErr)
	}
	return true, true
}

// tryDeadLetter is the RECOMMENDED single call site for Topic 4's
// dead-letter fallback (docs/roadmap/error-handling-rest-events-reqreply.md)
// — mirrors mqtt5's/mqtt's identical helper, using this package's own
// frame-based FramedSocket.SendFrames API.
func tryDeadLetter[T any](
	sock FramedSocket, handle *events.ChannelHandle[T], obs stats.Observer, sourceTopic string, rawPayload []byte, err error,
) bool {
	topic, body, ok := handle.DeadLetterFor(obs, sourceTopic, rawPayload, err)
	if !ok {
		return false
	}
	_ = sock.SendFrames([][]byte{[]byte(topic), body})
	return true
}

func subscribeWithHandle[T any](
	ctx context.Context,
	sock FramedSocket,
	handle *events.ChannelHandle[T],
	fn func(context.Context, T) error,
	opts SubscribeOptions[T],
	formats ...format.Format[T],
) error {
	obs := opts.Observer
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}

	if err := validateSubscribeImplementationShapes[T](handle.Implementations); err != nil {
		return err
	}
	fn = wrapSubscribeGeneral(fn, handle.Implementations)

	filter := opts.TopicFilter
	if filter == "" {
		filter = deriveTopicPrefix(handle.Topic)
	}
	if err := sock.SetSubscription(filter); err != nil {
		return SocketError{Op: "set_subscription", Err: err}
	}
	if err := sock.SetRecvTimeout(recvPollInterval); err != nil {
		return SocketError{Op: "set_recv_timeout", Err: err}
	}
	var secReqs []route.SecurityRequirement
	if handle.Descriptor.Subscribe != nil {
		secReqs = handle.Descriptor.Subscribe.Security
	}
	if secReqs == nil {
		secReqs = handle.GlobalSecurity
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		frames, err := sock.RecvFrames()
		if errors.Is(err, ErrTimeout) {
			continue
		}
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				return SocketError{Op: "recv", Err: err}
			}
		}
		if len(frames) < 2 {
			continue // malformed: expect [topic, payload]
		}
		topic := string(frames[0])
		payload := frames[1]
		start := time.Now()

		// The channel's OWN declaration (WithFormats/WithSubscribeFormats)
		// is the single source of truth for which format applies —
		// DecodeWithFormats resolves it, this adapter never duplicates
		// that resolution logic itself.
		value, decErr := handle.DecodeWithFormats(payload, formats...)
		if decErr != nil {
			stats.ReportErrors(obs, "payload", decErr)
			obs.RecordSubscribe(topic, false, time.Since(start))
			if handled, matched := tryPublishErrorChannel(ctx, sock, handle, obs, decErr); handled {
				continue
			} else if !matched {
				if tryDeadLetter(sock, handle, obs, topic, payload, decErr) {
					continue
				}
			}
			if opts.OnError != nil {
				opts.OnError(SubscribeError{Kind: KindDecode, Topic: topic, Err: decErr})
			}
			continue
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
			vars, varErr := TopicVarsFromMessage(handle, topic)
			if varErr != nil {
				reportInvalidTopicErrors(varErr, obs)
				reportTopicMismatchErrors(varErr, obs)
				stats.ReportErrors(obs, "topic_var", varErr)
				obs.RecordSubscribe(topic, false, time.Since(start))
				if handled, matched := tryPublishErrorChannel(ctx, sock, handle, obs, varErr); handled {
					continue
				} else if !matched {
					if tryDeadLetter(sock, handle, obs, topic, payload, varErr) {
						continue
					}
				}
				if opts.OnError != nil {
					opts.OnError(SubscribeError{Kind: KindDecode, Topic: topic, Err: varErr})
				}
				continue
			}
			topicVars = vars
			if mergeErr := codex.DecodeVars(&value, vars, mergeFields...); mergeErr != nil {
				stats.ReportErrors(obs, "topic_var", mergeErr)
				obs.RecordSubscribe(topic, false, time.Since(start))
				if handled, matched := tryPublishErrorChannel(ctx, sock, handle, obs, mergeErr); handled {
					continue
				} else if !matched {
					if tryDeadLetter(sock, handle, obs, topic, payload, mergeErr) {
						continue
					}
				}
				if opts.OnError != nil {
					opts.OnError(SubscribeError{Kind: KindDecode, Topic: topic, Err: mergeErr})
				}
				continue
			}
		}

		// Security enforcement — every attached handle.Implementations
		// security-shaped Fn (populated by [events.Subscriber.SubscribeMW])
		// runs UNCONDITIONALLY (mirrors mqtt5's ordering: an UNPAIRED,
		// general-purpose Satisfies-empty Fn must run even on a channel
		// with no declared security — e.g. a Transform-equivalent reading
		// an in-payload field into *T before fn runs). Shapes were
		// already validated eagerly above.
		if len(handle.Implementations) > 0 {
			if err := runSubscribeSecurityImpls(ctx, &value, secReqs, handle.Implementations); err != nil {
				if secObs, ok := obs.(stats.SecurityObserver); ok {
					secObs.RecordSecurityRejection(topic, route.FirstSchemeName(secReqs))
				}
				obs.RecordSubscribe(topic, false, time.Since(start))
				// Security middleware Fn error IS ErrorChannel-eligible
				// now (Topic 1's Category A fix).
				wrapped := events.SecurityError{Err: err}
				if handled, matched := tryPublishErrorChannel(ctx, sock, handle, obs, wrapped); handled {
					continue
				} else if !matched {
					if tryDeadLetter(sock, handle, obs, topic, payload, wrapped) {
						continue
					}
				}
				if opts.OnError != nil {
					opts.OnError(SubscribeError{Kind: KindSecurity, Topic: topic, Err: wrapped})
				}
				continue
			}
		}

		// Codec-backed middleware dispatch (Transform and bundled .Use())
		// — SAME pre-handler dispatch point runSubscribeSecurityImpls just
		// ran at (D1), reusing the SAME topicVars derived above. A fn
		// error is ErrorPattern-eligible (D2), falling back to
		// events.MiddlewareError when unmatched — mirrors mqtt5's
		// identical resolution.
		if len(handle.MiddlewareHandlers) > 0 {
			// zeromq has no property mechanism at all — supplies an
			// empty property-value map (nil), mirroring mqtt5's
			// identical extraction-then-supply pattern minus the
			// extraction (nothing to extract from). A channel declaring
			// a REQUIRED property fails naturally with the SAME
			// MiddlewareInputError a missing topic var would.
			if mwErr := dispatchSubscribeMiddlewareHandlers(ctx, &value, handle.MiddlewareHandlers, topicVars, nil); mwErr != nil {
				obs.RecordSubscribe(topic, false, time.Since(start))
				var dispatchErr middlewareDispatchError
				errors.As(mwErr, &dispatchErr)
				if dispatchErr.isFnError {
					stats.ReportErrors(obs, "middleware:fn", dispatchErr.err)
					if handled, matched := tryPublishErrorChannel(ctx, sock, handle, obs, dispatchErr.err); handled {
						continue
					} else if !matched {
						if tryDeadLetter(sock, handle, obs, topic, payload, dispatchErr.err) {
							continue
						}
					}
					if opts.OnError != nil {
						opts.OnError(SubscribeError{Kind: KindHandler, Topic: topic, Err: events.MiddlewareError{Name: dispatchErr.name, Err: dispatchErr.err}})
					}
					continue
				}
				stats.ReportErrors(obs, "middleware:in", dispatchErr.err)
				if handled, matched := tryPublishErrorChannel(ctx, sock, handle, obs, dispatchErr.err); handled {
					continue
				} else if !matched {
					if tryDeadLetter(sock, handle, obs, topic, payload, dispatchErr.err) {
						continue
					}
				}
				if opts.OnError != nil {
					opts.OnError(SubscribeError{Kind: KindDecode, Topic: topic, Err: dispatchErr.err})
				}
				continue
			}
		}

		var spanCtx = ctx
		if to, ok := obs.(stats.TraceObserver); ok {
			spanCtx = to.StartSpan(ctx, "zmq.subscribe", topic)
		}
		fnErr := fn(spanCtx, value)
		if to, ok := obs.(stats.TraceObserver); ok {
			to.EndSpan(spanCtx, fnErr)
		}
		if fnErr != nil {
			obs.RecordSubscribe(topic, false, time.Since(start))
			if handled, matched := tryPublishErrorChannel(ctx, sock, handle, obs, fnErr); handled {
				continue
			} else if !matched {
				if tryDeadLetter(sock, handle, obs, topic, payload, fnErr) {
					continue
				}
			}
			if opts.OnError != nil {
				opts.OnError(SubscribeError{Kind: KindHandler, Topic: topic, Err: fnErr})
			}
			continue
		}
		obs.RecordSubscribe(topic, true, time.Since(start))
	}
}

// reportInvalidTopicErrors extracts the constraint from an [events.InvalidTopicError]
// and reports it to obs with location "topic". Mirrors adapters/mqtt's/
// adapters/mqtt5's identical reporter — not redundant with
// [stats.ReportErrors]'s generic walker, since [events.InvalidTopicError]
// unwraps to [codex.ConstraintError], which has no further Unwrap().
func reportInvalidTopicErrors(err error, obs stats.Observer) {
	var ie events.InvalidTopicError
	if !errors.As(err, &ie) {
		return
	}
	obs.RecordValidationError("topic", stats.ConstraintName(ie.Err), "")
}

// reportTopicMismatchErrors reports a [TopicMismatchError] to obs with
// location "topic" and constraint name "topic-mismatch". Mirrors
// adapters/mqtt's/adapters/mqtt5's identical reporter — TopicMismatchError
// is a flat struct with no Unwrap(), invisible to the generic
// stats.ReportErrors walker.
func reportTopicMismatchErrors(err error, obs stats.Observer) {
	var mm TopicMismatchError
	if !errors.As(err, &mm) {
		return
	}
	obs.RecordValidationError("topic", "topic-mismatch", "")
}

// Subscribe is the NEW value-based convenience tier — mirrors
// [nethttp.Call] taking a [rest.Route] value directly. Builds the handle
// internally via sub.Handle(caller.events) (caller.events MAY be nil for
// a spec-free handle — the common case for a typical application
// subscribing without also registering a spec), then behaves identically
// to [subscribeWithHandle]. fn is STILL a call-time param, unchanged from
// today's imperative "here's my handler, start consuming now" mental
// model — see docs/design/d-0002-pubsub-workflow-simplification.md's two-tier
// Subscribe subsection.
//
//	sub := SensorReadings.WithSubscribe(events.Subscribe{})
//	caller := zeromq.NewCaller(sock, nil) // nil = no spec
//	err := zeromq.Subscribe(ctx, caller, sub, fn, zeromq.SubscribeOptions[Reading]{})
func subscribe[T any](
	ctx context.Context,
	caller *caller,
	sub events.Subscriber[T],
	fn func(context.Context, T) error,
	opts SubscribeOptions[T],
	formats ...format.Format[T],
) error {
	handle, err := sub.Handle(caller.events)
	if err != nil {
		return err
	}
	return subscribeWithHandle(ctx, caller.sock, handle, fn, opts, formats...)
}

// validatePublishImplementationShapes checks every attached impl.Fn
// against the two shapes [events.Publisher.PublishMW] recognizes for T —
// the revised security shape (func(context.Context, *T,
// []route.SecurityRequirement) error) or the general-purpose wrapping
// shape (func(next func(context.Context, T) error) func(context.Context, T) error)
// — EAGERLY at the top of every [Publish] call. Mirrors
// [adapters/mqtt5.validatePublishImplementationShapes], adapted for
// zeromq's simpler (no-UserProperty) credential shape.
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

// runPublishSecurityImpls runs every attached
// [middleware.ClientImplementation] whose Fn matches the revised security
// shape (func(context.Context, *T, []route.SecurityRequirement) error) —
// GATED by Satisfies vs secReqs, mirroring
// [adapters/mqtt5.runPublishSecurityImpls] (an implementation with a
// NON-EMPTY Satisfies only runs when at least one of its scheme names is
// present in secReqs; an implementation with an EMPTY Satisfies
// (general-purpose) always runs). msg is a pointer — an Fn may write into
// it (in-payload credential embedding). General-purpose wrapping-shaped
// Fns are silently skipped here (consumed instead by
// [wrapPublishGeneral]).
func runPublishSecurityImpls[T any](ctx context.Context, msg *T, secReqs []route.SecurityRequirement, impls []middleware.ClientImplementation) error {
	reqSchemes := make(map[string]bool, len(secReqs))
	for _, req := range secReqs {
		for scheme := range req {
			reqSchemes[scheme] = true
		}
	}
	for _, impl := range impls {
		fn, ok := impl.Fn.(func(context.Context, *T, []route.SecurityRequirement) error)
		if !ok {
			continue // general-purpose or nil
		}
		if len(impl.Satisfies) > 0 {
			matched := false
			for _, s := range impl.Satisfies {
				if reqSchemes[s] {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		if err := fn(ctx, msg, secReqs); err != nil {
			return err
		}
	}
	return nil
}

// wrapPublishGeneral wraps fn (the adapter's own "encode and transmit"
// step) with every general-purpose Fn found in impls (shape
// func(next func(context.Context, T) error) func(context.Context, T) error),
// OUTERMOST-in, in attachment order — mirrors
// [wrapSubscribeGeneral]'s publish-side sibling, deliberately symmetric,
// and [adapters/mqtt5.wrapPublishGeneral]. Security-shaped Fns are
// silently skipped here (consumed instead by [runPublishSecurityImpls]).
// This is the mechanism [Observability] uses on the publish side.
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

// Publish encodes msg using handle's codec and sends it to a PUB or PUSH socket.
//
// The message is framed as [topic, payload]:
//   - topic: the resolved topic string (after BuildTopic if vars are provided)
//   - payload: the codec-encoded message bytes
//
// For PUB sockets the topic frame is used for ZMQ prefix-filter matching.
// For PUSH sockets the topic frame is sent but ignored by PULL receivers.
//
// vars controls topic resolution:
//   - nil: use handle.Topic directly (static topics).
//   - non-nil: call handle.BuildTopic(vars) to resolve a template topic.
//
// Security/credential resolution (every attached
// [events.ChannelHandle.ClientImplementations] Fn from
// [events.Publisher.PublishMW]) runs BEFORE msg is encoded — it gets
// write-access to *msg (in-payload credential embedding), so the encode
// step downstream observes any mutation. Every attached
// ClientImplementations Fn is shape-validated EAGERLY via
// [validatePublishImplementationShapes] before anything else runs.
// General-purpose Fns wrap the internal "encode and transmit" step
// ([wrapPublishGeneral]); security-shaped Fns run once each
// ([runPublishSecurityImpls]).
//
// The optional formats parameter overrides the channel handle's default JSON
// codec. Priority: call-time formats > handle.PublishFormats > handle.Formats
// > handle.Encode (JSON fallback).
func publish[T any](
	ctx context.Context,
	sock FramedSocket,
	handle *events.ChannelHandle[T],
	msg T,
	vars map[string]string,
	isExplicitVars bool,
	opts PublishOptions[T],
	formats ...format.Format[T],
) error {
	obs := opts.Observer
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}

	if err := validatePublishImplementationShapes[T](handle.ClientImplementations); err != nil {
		return err
	}

	start := time.Now()
	var err error
	if to, ok := obs.(stats.TraceObserver); ok {
		ctx = to.StartSpan(ctx, "zmq.publish", handle.Topic)
		defer func() { to.EndSpan(ctx, err) }()
	}

	// Codec-backed middleware dispatch (ClientTransform and bundled
	// .Use()) — derived FIRST since a middleware's own Out may contribute
	// ADDITIONAL topic vars BuildTopic needs. isExplicitVars distinguishes
	// vars' OWN provenance (explicit PublishOptions.Vars vs. channel-own-
	// derived — see [PublishAdapter]'s mutually-exclusive call sites)
	// since that distinction is otherwise erased by the time vars reaches
	// this function — explicit ALWAYS wins over middleware-derived;
	// middleware-derived wins over channel-own-derived (the Bug 1 fix —
	// previously ALWAYS backwards, channel-own beat middleware). Mirrors
	// mqtt5's identical wiring. The property-vars return value is
	// discarded — zeromq has no write-target for it (no property
	// mechanism); a required property still fails naturally at DecodeIn
	// time on the subscribe side.
	if len(handle.ClientMiddlewareHandlers) > 0 {
		mwTopicVars, _, mwErr := dispatchPublishMiddlewareHandlers(ctx, msg, handle.ClientMiddlewareHandlers)
		if mwErr != nil {
			var dispatchErr middlewareDispatchError
			loc := "middleware:fn"
			// dispatchErr.err is ALREADY a properly-wrapped
			// events.MiddlewareError (fn case) or events.MiddlewareOutputError
			// (encode case) — see api/events/transform.go's buildEncodeOut
			// and this package's dispatchPublishMiddlewareHandlers — so
			// BOTH branches unwrap to the already-typed inner error here,
			// no re-wrap needed.
			reported := mwErr
			if errors.As(mwErr, &dispatchErr) {
				if dispatchErr.isEncodeErr {
					loc = "middleware:out"
				}
				reported = dispatchErr.err
				mwErr = dispatchErr.err
			}
			stats.ReportErrors(obs, loc, reported)
			obs.RecordPublish(handle.Topic, false, time.Since(start))
			err = mwErr
			// Topic 4: a failed publish (never reached the broker) is
			// ALSO dead-letterable, alongside the synchronous error
			// returned to the caller.
			bestEffortPayload, _ := handle.Encode(msg)
			tryDeadLetter(sock, handle, obs, handle.Topic, bestEffortPayload, err)
			return err
		}
		if isExplicitVars {
			vars = overrideDerivedVars(mwTopicVars, vars)
		} else {
			vars = overrideDerivedVars(vars, mwTopicVars)
		}
	}

	topic := handle.Topic
	if vars != nil {
		var buildErr error
		topic, buildErr = handle.BuildTopic(vars)
		if buildErr != nil {
			err = buildErr
			reportInvalidTopicErrors(buildErr, obs)
			stats.ReportErrors(obs, "topic_var", buildErr)
			obs.RecordPublish(handle.Topic, false, time.Since(start))
			return err
		}
	}

	// Security/credential resolution — every attached
	// handle.ClientImplementations security-shaped Fn runs, mirroring
	// SubscribeWithHandle's ordering on the subscribe side.
	var secReqs []route.SecurityRequirement
	if handle.Descriptor.Publish != nil {
		secReqs = handle.Descriptor.Publish.Security
	}
	if secReqs == nil {
		secReqs = handle.GlobalSecurity
	}
	if len(handle.ClientImplementations) > 0 {
		if err = runPublishSecurityImpls(ctx, &msg, secReqs, handle.ClientImplementations); err != nil {
			obs.RecordPublish(topic, false, time.Since(start))
			return err
		}
	}

	transmit := func(ctx context.Context, m T) error {
		// The channel's OWN declaration (WithFormats/WithPublishFormats)
		// is the single source of truth for which format applies —
		// EncodeWithFormats resolves it, this adapter never duplicates
		// that resolution logic itself.
		payload, encErr := handle.EncodeWithFormats(m, formats...)
		if encErr != nil {
			stats.ReportErrors(obs, "payload", encErr)
			return PublishEncodeError{Topic: topic, Err: encErr}
		}
		if sendErr := sock.SendFrames([][]byte{[]byte(topic), payload}); sendErr != nil {
			return SocketError{Op: "send", Err: sendErr}
		}
		return nil
	}
	transmit = wrapPublishGeneral(transmit, handle.ClientImplementations)

	if err = transmit(ctx, msg); err != nil {
		obs.RecordPublish(topic, false, time.Since(start))
		// Topic 4: a failed publish (encode failure, or never reached
		// the broker) is ALSO dead-letterable, alongside the synchronous
		// error returned to the caller.
		bestEffortPayload, _ := handle.Encode(msg)
		tryDeadLetter(sock, handle, obs, topic, bestEffortPayload, err)
		return err
	}
	obs.RecordPublish(topic, true, time.Since(start))
	return nil
}

// PublishHandle is the single-call convenience wrapper around [Publish]: it
// derives the topic vars map from msg automatically, using the channel's
// merge-capable topic params ([events.ChannelHandle.MergeFields] +
// [codex.EncodeVars]) — one struct in, no manual vars map, mirroring
// [mqtt5.PublishHandle]'s convenience for MQTT 5 events.
//
// [Publish] remains available as the lower-level escape hatch for callers
// that build the vars map themselves (e.g. no merge fields declared, or
// vars come from a non-struct source).
//
//	err := zeromq.PublishHandle(ctx, sock, sensorChannel, reading, zeromq.PublishOptions[Reading]{})
func publishHandle[T any](
	ctx context.Context,
	sock FramedSocket,
	handle *events.ChannelHandle[T],
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
	return publish(ctx, sock, handle, msg, vars, false, opts, formats...)
}

// Serve runs a blocking REP loop: receives requests, calls fn, sends replies.
// It is the server side of a ZMQ REQ/REP contract.
//
// Serve is a thin, single-route wrapper around [reqreply.ServerTransport.
// Serve] — builds a [serverTransport] directly from sock/opts (a
// single-entry socket map keyed by handle.Topic) and delegates to it (the
// SAME reflection-based dispatch [AttachServer]'s registered routes use),
// rather than duplicating the decode/handler/encode/error-pattern
// pipeline inline. Zero duplicate logic — full capability parity with
// [AttachServer] is therefore automatic (see
// docs/design/d-0004-reqreply-workflow-simplification.md's Addendum's
// Phase 0/0b for the history — this used to be
// a separate, hand-written implementation, mirroring the SAME
// de-duplication mqtt5's [adapters/mqtt5.Serve] already shipped).
//
// Message framing:
//   - Incoming request: [payload]
//   - Reply on success: ["ok", encoded_response]
//   - Reply on failure: ["error", error_message]
//
// The REP socket always sends a reply (even on error) to avoid leaving the
// REQ peer blocked. Per-error details are delivered via [ServeOptions.OnError].
//
// The loop runs until ctx is cancelled (returns nil) or a socket error occurs.
// Run Serve in a dedicated goroutine.
//
// Format overrides are applied via [reqreply.RouteHandle.WithRequestFormats] and
// [reqreply.RouteHandle.WithFormats] on the handle before calling Serve.
//
// Example (REP compute server):
//
//	go func() {
//	    if err := zeromq.Serve(ctx, sock, computeHandle, handler, zeromq.ServeOptions{Observer: obs}); err != nil {
//	        log.Error("serve stopped", "err", err)
//	    }
//	}()
func Serve[Req, Resp any](
	ctx context.Context,
	sock FramedSocket,
	handle *reqreply.RouteHandle[Req, Resp],
	fn func(context.Context, Req) (Resp, error),
	opts ServeOptions,
) error {
	t := &serverTransport{sockets: map[string]FramedSocket{handle.Topic: sock}, opts: opts}
	return t.Serve(ctx, handle, fn)
}

// Call encodes req, sends it to a REQ socket, and decodes the reply.
// It is the client side of a ZMQ REQ/REP contract.
//
// Call is a thin, single-call wrapper around [reqreply.ClientTransport.
// Call] — builds a [clientTransport] directly from sock/opts (a
// single-entry socket map keyed by handle.Topic) and delegates to it (the
// SAME reflection-based dispatch [AttachClient] uses), rather than
// duplicating the encode/send/recv/decode pipeline inline. Zero duplicate
// logic — full capability parity with [AttachClient] is therefore
// automatic, including [CallOptions.Vars] (explicit override, PRECEDENCE
// over any [reqreply.NewTopicParam] merge-field-derived value — used
// ONLY for observability path/span naming, never socket selection, since
// zeromq REQ/REP routing is socket-based, not topic-based) and
// [CallOptions.RequestFormats]/[ResponseFormats] (per-call format
// overrides). See docs/design/d-0004-reqreply-workflow-simplification.md's Addendum's Phase 0/0b for
// the history — this used to be a separate, hand-written implementation,
// mirroring the SAME de-duplication mqtt5's [adapters/mqtt5.Call]
// already shipped.
//
// Message framing:
//   - Outgoing request:  [payload]
//   - Expected reply OK: ["ok", encoded_response]
//   - Server error reply:["error", message] → returns [CallError]
//
// ctx cancellation is honoured during the reply receive loop. Call blocks
// until a reply arrives, ctx is cancelled, or a socket error occurs.
//
// Format overrides are applied via [reqreply.RouteHandle.WithRequestFormats] and
// [reqreply.RouteHandle.WithFormats] on the handle before calling Call.
//
// Example (REQ compute client):
//
//	result, err := zeromq.Call(ctx, sock, computeHandle.ClientHandle(), req,
//	    zeromq.CallOptions{Observer: obs})
func Call[Req, Resp any](
	ctx context.Context,
	sock FramedSocket,
	handle *reqreply.RouteHandle[Req, Resp],
	req Req,
	opts CallOptions,
) (Resp, error) {
	var zero Resp
	t := &clientTransport{sockets: map[string]FramedSocket{handle.Topic: sock}, opts: opts}
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
// underlying [clientTransport.call] gained it directly (Phase 0/0b of
// docs/design/d-0004-reqreply-workflow-simplification.md's Addendum, mirroring mqtt5's identical
// outcome). An explicit [CallOptions.Vars] still takes PRECEDENCE over
// the derived value for the same key. Kept for existing callers — prefer
// [Call] directly in new code, since it is now identical.
//
// Note: this derivation is OBSERVABILITY-only (span/RecordRequest path
// naming) — it never affects socket selection. ZMQ REQ/REP routing is
// socket-based, not topic-based.
//
//	resp, err := zeromq.CallHandle(ctx, sock, computeRoute, req, zeromq.CallOptions{})
func CallHandle[Req, Resp any](
	ctx context.Context,
	sock FramedSocket,
	handle *reqreply.RouteHandle[Req, Resp],
	req Req,
	opts CallOptions,
) (Resp, error) {
	return Call(ctx, sock, handle, req, opts)
}

// sendErrorReply sends an error reply frame to the REQ peer as plain text.
// Always called in the Serve loop on handler, decode, or encode failures
// to prevent the REQ socket from blocking indefinitely. Used directly for
// decode-level errors (no [reqreply.ErrorPattern] can apply — there is no
// business error to match yet).
func sendErrorReply(sock FramedSocket, err error) {
	_ = sock.SendFrames([][]byte{statusError, []byte(err.Error())})
}

// emptyDelimiter is the empty frame separating the identity stack from the
// payload in DEALER/ROUTER ZMQ envelope format.
var emptyDelimiter = []byte{}

// ServeRouter runs a blocking ROUTER loop. Each incoming request is dispatched
// concurrently in its own goroutine. Identity frames are extracted automatically
// and re-prepended to every reply so the DEALER peer can correlate responses.
//
// ServeRouter is a thin, single-route wrapper around
// [reqreply.ServerTransport.Serve] — builds a [routerServerTransport]
// directly from sock/opts (a single-entry socket map keyed by
// handle.Topic) and delegates to it (the SAME reflection-based dispatch
// [AttachRouterServer]'s registered routes use), rather than duplicating
// the decode/handler/encode/error-pattern pipeline inline. Zero duplicate
// logic — full capability parity with [AttachRouterServer] is therefore
// automatic. See docs/design/d-0004-reqreply-workflow-simplification.md's Addendum's Phase 0/0b for the
// history — this used to be a separate, hand-written implementation,
// mirroring the SAME de-duplication mqtt5's escape hatch already shipped.
//
// ROUTER message framing (server receives):
//
//	[identity, "", payload]
//
// Reply framing (server sends):
//
//	[identity, "", "ok", encoded_response]
//	[identity, "", "error", error_message]
//
// The loop runs until ctx is cancelled; it waits for all in-flight goroutines
// to drain before returning nil. A socket recv error stops the loop immediately.
//
// Errors are delivered via [ServeOptions.OnError] using the same [ServeError]
// type as [Serve]. Format overrides are applied via [reqreply.RouteHandle.WithRequestFormats]
// and [reqreply.RouteHandle.WithFormats] before calling ServeRouter.
//
// Example (ROUTER compute server):
//
//	go func() {
//	    if err := zeromq.ServeRouter(ctx, sock, handle, handler,
//	        zeromq.ServeOptions{Observer: obs}); err != nil {
//	        log.Error("serve stopped", "err", err)
//	    }
//	}()
func ServeRouter[Req, Resp any](
	ctx context.Context,
	sock FramedSocket,
	handle *reqreply.RouteHandle[Req, Resp],
	fn func(context.Context, Req) (Resp, error),
	opts ServeOptions,
) error {
	t := &routerServerTransport{sockets: map[string]FramedSocket{handle.Topic: sock}, opts: opts}
	return t.Serve(ctx, handle, fn)
}

// sendRouterErrorReply sends an error reply to a ROUTER peer, preserving
// identity frames, as plain text. Used directly for decode-level errors (no
// [reqreply.ErrorPattern] can apply yet).
func sendRouterErrorReply(sock FramedSocket, identity []byte, err error) {
	_ = sock.SendFrames([][]byte{identity, emptyDelimiter, statusError, []byte(err.Error())})
}

// CallDealer encodes req and sends it via a DEALER socket using the ZMQ envelope
// format (empty delimiter + payload), then synchronously waits for one reply.
//
// CallDealer is a thin, single-call wrapper around
// [reqreply.ClientTransport.Call] — builds a [dealerClientTransport]
// directly from sock/opts (a single-entry socket map keyed by
// handle.Topic) and delegates to it (the SAME reflection-based dispatch
// [AttachDealerClient] uses), rather than duplicating the encode/send/
// recv/decode pipeline inline. Zero duplicate logic — full capability
// parity with [AttachDealerClient] is therefore automatic (same
// [CallOptions.Vars]/[RequestFormats]/[ResponseFormats] handling as
// [Call] — see its doc comment for the full rationale). See
// docs/design/d-0004-reqreply-workflow-simplification.md's Addendum's Phase 0/0b for the history.
//
// DEALER message framing (client sends):
//
//	["", payload]
//
// Expected reply framing (client receives):
//
//	["", "ok", encoded_response]
//	["", "error", error_message]
//
// For concurrent use, call CallDealer from multiple goroutines; each invocation
// manages its own independent send/recv cycle.
//
// ctx cancellation is honoured during the reply receive loop.
//
// Errors are wrapped in [CallError], the same type used by [Call].
//
// Example (DEALER compute client):
//
//	result, err := zeromq.CallDealer(ctx, sock, handle, ComputeReq{X: 3, Y: 4},
//	    zeromq.CallOptions{Observer: obs})
func CallDealer[Req, Resp any](
	ctx context.Context,
	sock FramedSocket,
	handle *reqreply.RouteHandle[Req, Resp],
	req Req,
	opts CallOptions,
) (Resp, error) {
	var zero Resp
	t := &dealerClientTransport{sockets: map[string]FramedSocket{handle.Topic: sock}, opts: opts}
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
