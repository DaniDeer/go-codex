package zeromq

import (
	"context"
	"errors"
	"fmt"
	"sync"
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

// SubscribeOptions configures [subscribe]/[SubscribeWithHandle]. Generic
// over T (BREAKING change from the previous non-generic SubscribeOptions)
// since [SubscribeOptions.SecurityFunc] needs read/write access to the
// decoded message — zeromq's [topic, payload] frames carry nothing beyond
// what's already decoded into T, so there is no raw-message-equivalent
// parameter to pass instead (unlike mqtt5's *pahomqtt5.Publish) — see
// docs/roadmap/pubsub-workflow-simplification.md's Decision 3 "zeromq —
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
	// docs/roadmap/pubsub-workflow-simplification.md's "Confirmed bug,
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

	// SecurityFunc, when non-nil, is called for channels with non-empty
	// security requirements before fn is invoked — the message-level
	// security mechanism zeromq had NONE of before this pass (confirmed
	// via docs/roadmap/pubsub-workflow-simplification.md's escape-hatch
	// #5 discussion: "zeromq has literally no security mechanism at any
	// layer"). msg is a pointer to the decoded value — read it to extract
	// an in-payload credential field, and/or mutate it (the same
	// read/write access [nethttp.Transform]-equivalent unpaired usage
	// gets for free). Return a non-nil error to reject the message.
	//
	//	opts.SecurityFunc = func(ctx context.Context, msg *Reading, reqs []route.SecurityRequirement) error {
	//	    if msg.AuthToken == "" {
	//	        return errors.New("missing credential")
	//	    }
	//	    return verifyJWT(msg.AuthToken, reqs)
	//	}
	SecurityFunc func(ctx context.Context, msg *T, reqs []route.SecurityRequirement) error
}

// PublishOptions configures [Publish]/[PublishHandle]. Generic over T
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

	// CredentialFunc, when non-nil, is called for channels that declare
	// non-nil Publish.Security (or inherit non-empty GlobalSecurity) —
	// the publish-side mirror of [SubscribeOptions.SecurityFunc], SAME
	// shape as the subscribe side (deliberate symmetry — zeromq's frames
	// carry nothing beyond T in either direction, unlike mqtt/mqtt5's
	// asymmetric subscribe-side raw-message access). msg is a pointer to
	// the value about to be encoded — mutate it to embed a credential as
	// an ordinary payload field before it is marshalled.
	//
	//	opts.CredentialFunc = func(ctx context.Context, msg *Reading, reqs []route.SecurityRequirement) error {
	//	    msg.AuthToken = "Bearer " + token
	//	    return nil
	//	}
	CredentialFunc func(ctx context.Context, msg *T, reqs []route.SecurityRequirement) error
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

// resolveCallFormat type-asserts overrideAny (a [CallOptions.RequestFormats]/
// [CallOptions.ResponseFormats] value) against []format.Format[T], falling
// back to declared when overrideAny is nil. Returns an error on a type
// mismatch — callers wrap it in [CallError].
func resolveCallFormat[T any](declared []format.Format[T], overrideAny any) ([]format.Format[T], error) {
	if overrideAny == nil {
		return declared, nil
	}
	fmts, ok := overrideAny.([]format.Format[T])
	if !ok {
		return nil, fmt.Errorf("format option: want []format.Format[%T], got %T", *new(T), overrideAny)
	}
	return fmts, nil
}

// validateSubscribeImplementationShapes checks every attached impl.Fn
// against the two shapes [events.Subscriber.SubscribeMW] recognizes for T
// — the security shape (func(context.Context, *T,
// []route.SecurityRequirement) error) or the general-purpose wrapping
// shape (func(next func(context.Context, T) error) func(context.Context,
// T) error) — EAGERLY at [SubscribeWithHandle] construction time rather
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
// docs/roadmap/pubsub-workflow-simplification.md's wildcard/prefix
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
// AFTER opts.SecurityFunc.
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
	effectiveFmts := formats
	if len(effectiveFmts) == 0 {
		effectiveFmts = handle.SubscribeFormats
	}
	if len(effectiveFmts) == 0 {
		effectiveFmts = handle.Formats
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

		var value T
		var decErr error
		if len(effectiveFmts) > 0 {
			value, decErr = effectiveFmts[0].Unmarshal(payload)
		} else {
			value, decErr = handle.Decode(payload)
		}
		if decErr != nil {
			stats.ReportErrors(obs, "payload", decErr)
			obs.RecordSubscribe(topic, false, time.Since(start))
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
		if mergeFields := handle.MergeFields(); len(mergeFields) > 0 {
			vars, varErr := TopicVarsFromMessage(handle, topic)
			if varErr != nil {
				stats.ReportErrors(obs, "topic_var", varErr)
				obs.RecordSubscribe(topic, false, time.Since(start))
				if opts.OnError != nil {
					opts.OnError(SubscribeError{Kind: KindDecode, Topic: topic, Err: varErr})
				}
				continue
			}
			if mergeErr := codex.DecodeVars(&value, vars, mergeFields...); mergeErr != nil {
				stats.ReportErrors(obs, "topic_var", mergeErr)
				obs.RecordSubscribe(topic, false, time.Since(start))
				if opts.OnError != nil {
					opts.OnError(SubscribeError{Kind: KindDecode, Topic: topic, Err: mergeErr})
				}
				continue
			}
		}

		// Security enforcement — opts.SecurityFunc runs first (if any),
		// then every attached handle.Implementations security-shaped Fn
		// (populated by [events.Subscriber.SubscribeMW]) runs
		// UNCONDITIONALLY (mirrors mqtt5's ordering: an UNPAIRED,
		// general-purpose Satisfies-empty Fn must run even on a channel
		// with no declared security — e.g. a Transform-equivalent reading
		// an in-payload field into *T before fn runs). Shapes were
		// already validated eagerly above.
		if opts.SecurityFunc != nil {
			if err := opts.SecurityFunc(ctx, &value, secReqs); err != nil {
				if secObs, ok := obs.(stats.SecurityObserver); ok {
					secObs.RecordSecurityRejection(topic, firstSchemeName(secReqs))
				}
				obs.RecordSubscribe(topic, false, time.Since(start))
				if opts.OnError != nil {
					opts.OnError(SubscribeError{Kind: KindSecurity, Topic: topic, Err: events.SecurityError{Err: err}})
				}
				continue
			}
		}
		if len(handle.Implementations) > 0 {
			if err := runSubscribeSecurityImpls(ctx, &value, secReqs, handle.Implementations); err != nil {
				if secObs, ok := obs.(stats.SecurityObserver); ok {
					secObs.RecordSecurityRejection(topic, firstSchemeName(secReqs))
				}
				obs.RecordSubscribe(topic, false, time.Since(start))
				if opts.OnError != nil {
					opts.OnError(SubscribeError{Kind: KindSecurity, Topic: topic, Err: events.SecurityError{Err: err}})
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
			if opts.OnError != nil {
				opts.OnError(SubscribeError{Kind: KindHandler, Topic: topic, Err: fnErr})
			}
			continue
		}
		obs.RecordSubscribe(topic, true, time.Since(start))
	}
}

// firstSchemeName returns the first scheme name from the security
// requirements — mirrors [adapters/mqtt5.firstScheme], used for
// [stats.SecurityObserver.RecordSecurityRejection] reporting.
func firstSchemeName(reqs []route.SecurityRequirement) string {
	for _, req := range reqs {
		for name := range req {
			return name
		}
	}
	return ""
}

// Subscribe is the NEW value-based convenience tier — mirrors
// [nethttp.Call] taking a [rest.Route] value directly. Builds the handle
// internally via sub.Handle(caller.events) (caller.events MAY be nil for
// a spec-free handle — the common case for a typical application
// subscribing without also registering a spec), then behaves identically
// to [SubscribeWithHandle]. fn is STILL a call-time param, unchanged from
// today's imperative "here's my handler, start consuming now" mental
// model — see docs/roadmap/pubsub-workflow-simplification.md's two-tier
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
// Security/credential resolution (opts.CredentialFunc AND every attached
// [events.ChannelHandle.ClientImplementations] Fn from
// [events.Publisher.PublishMW]) runs BEFORE msg is encoded — both
// mechanisms get write-access to *msg (in-payload credential embedding),
// so the encode step downstream observes any mutation. Every attached
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

	topic := handle.Topic
	if vars != nil {
		var buildErr error
		topic, buildErr = handle.BuildTopic(vars)
		if buildErr != nil {
			err = buildErr
			stats.ReportErrors(obs, "topic_var", buildErr)
			obs.RecordPublish(handle.Topic, false, time.Since(start))
			return err
		}
	}

	// Security/credential resolution — opts.CredentialFunc runs first (if
	// any), then every attached handle.ClientImplementations security-shaped
	// Fn, mirroring SubscribeWithHandle's ordering on the subscribe side.
	var secReqs []route.SecurityRequirement
	if handle.Descriptor.Publish != nil {
		secReqs = handle.Descriptor.Publish.Security
	}
	if secReqs == nil {
		secReqs = handle.GlobalSecurity
	}
	if len(secReqs) > 0 && opts.CredentialFunc != nil {
		if err = opts.CredentialFunc(ctx, &msg, secReqs); err != nil {
			obs.RecordPublish(topic, false, time.Since(start))
			return err
		}
	}
	if len(handle.ClientImplementations) > 0 {
		if err = runPublishSecurityImpls(ctx, &msg, secReqs, handle.ClientImplementations); err != nil {
			obs.RecordPublish(topic, false, time.Since(start))
			return err
		}
	}

	effectiveFmts := formats
	if len(effectiveFmts) == 0 {
		effectiveFmts = handle.PublishFormats
	}
	if len(effectiveFmts) == 0 {
		effectiveFmts = handle.Formats
	}

	transmit := func(ctx context.Context, m T) error {
		var payload []byte
		var encErr error
		if len(effectiveFmts) > 0 {
			payload, encErr = effectiveFmts[0].Marshal(m)
		} else {
			payload, encErr = handle.Encode(m)
		}
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
	return publish(ctx, sock, handle, msg, vars, opts, formats...)
}

// Serve runs a blocking REP loop: receives requests, calls fn, sends replies.
// It is the server side of a ZMQ REQ/REP contract.
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
	obs := opts.Observer
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}
	if err := sock.SetRecvTimeout(recvPollInterval); err != nil {
		return SocketError{Op: "set_recv_timeout", Err: err}
	}
	path := handle.Topic
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
		if len(frames) == 0 {
			continue
		}
		start := time.Now()
		serveRequest(ctx, sock, handle, fn, obs, opts, path, frames[0], start)
	}
}

// serveRequest handles one REQ/REP exchange. Extracted from the Serve loop to
// allow explicit (non-deferred) span management within a loop body.
func serveRequest[Req, Resp any](
	ctx context.Context,
	sock FramedSocket,
	handle *reqreply.RouteHandle[Req, Resp],
	fn func(context.Context, Req) (Resp, error),
	obs stats.Observer,
	opts ServeOptions,
	path string,
	payload []byte,
	start time.Time,
) {
	var spanCtx = ctx
	var serveErr error
	if to, ok := obs.(stats.TraceObserver); ok {
		spanCtx = to.StartSpan(ctx, "zmq.serve", path)
	}
	defer func() {
		if to, ok := obs.(stats.TraceObserver); ok {
			to.EndSpan(spanCtx, serveErr)
		}
	}()

	// decode request
	var req Req
	if len(handle.RequestFormats) > 0 {
		req, serveErr = handle.RequestFormats[0].Unmarshal(payload)
	} else {
		req, serveErr = handle.Decode(payload)
	}
	if serveErr != nil {
		stats.ReportErrors(obs, "body", serveErr)
		obs.RecordRequest("ZMQ-REP", path, 0, time.Since(start))
		sendErrorReply(sock, serveErr)
		if opts.OnError != nil {
			opts.OnError(ServeError{Kind: KindDecode, Err: serveErr})
		}
		return
	}

	// call handler
	var resp Resp
	resp, serveErr = fn(spanCtx, req)
	if serveErr != nil {
		obs.RecordRequest("ZMQ-REP", path, 0, time.Since(start))
		sendHandlerErrorReply(sock, handle, serveErr, obs)
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
		obs.RecordRequest("ZMQ-REP", path, 0, time.Since(start))
		sendHandlerErrorReply(sock, handle, serveErr, obs)
		if opts.OnError != nil {
			opts.OnError(ServeError{Kind: KindEncode, Err: serveErr})
		}
		return
	}

	if sendErr := sock.SendFrames([][]byte{statusOK, respPayload}); sendErr != nil {
		serveErr = sendErr
		obs.RecordRequest("ZMQ-REP", path, 0, time.Since(start))
		if opts.OnError != nil {
			opts.OnError(ServeError{Kind: KindEncode, Err: sendErr})
		}
		return
	}
	obs.RecordRequest("ZMQ-REP", path, 200, time.Since(start))
}

// Call encodes req, sends it to a REQ socket, and decodes the reply.
// It is the client side of a ZMQ REQ/REP contract.
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
	obs := opts.Observer
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}
	var zero Resp
	start := time.Now()
	path := handle.Topic
	var callErr error

	// Resolve template topic vars if provided.
	if opts.Vars != nil {
		var buildErr error
		path, buildErr = handle.BuildTopic(opts.Vars)
		if buildErr != nil {
			stats.ReportErrors(obs, "topic_var", buildErr)
			obs.RecordRequest("ZMQ-REQ", handle.Topic, 0, time.Since(start))
			callErr = CallError{Err: buildErr}
			return zero, callErr
		}
	}

	if to, ok := obs.(stats.TraceObserver); ok {
		ctx = to.StartSpan(ctx, "zmq.request", path)
		defer func() { to.EndSpan(ctx, callErr) }()
	}

	// Resolve per-call request format override.
	reqFormats, fmtErr := resolveCallFormat[Req](handle.RequestFormats, opts.RequestFormats)
	if fmtErr != nil {
		obs.RecordRequest("ZMQ-REQ", path, 0, time.Since(start))
		callErr = CallError{Err: fmtErr}
		return zero, callErr
	}

	// encode request
	var payload []byte
	if len(reqFormats) > 0 {
		payload, callErr = reqFormats[0].Marshal(req)
	} else {
		payload, callErr = handle.EncodeRequest(req)
	}
	if callErr != nil {
		stats.ReportErrors(obs, "body", callErr)
		obs.RecordRequest("ZMQ-REQ", path, 0, time.Since(start))
		callErr = CallError{Err: callErr}
		return zero, callErr
	}

	// send
	if err := sock.SendFrames([][]byte{payload}); err != nil {
		callErr = CallError{Err: fmt.Errorf("send: %w", err)}
		obs.RecordRequest("ZMQ-REQ", path, 0, time.Since(start))
		return zero, callErr
	}

	// configure recv timeout for ctx-cancellation polling
	if err := sock.SetRecvTimeout(recvPollInterval); err != nil {
		callErr = CallError{Err: fmt.Errorf("set recv timeout: %w", err)}
		return zero, callErr
	}

	// receive reply
	var frames [][]byte
	for {
		select {
		case <-ctx.Done():
			callErr = CallError{Err: ctx.Err()}
			obs.RecordRequest("ZMQ-REQ", path, 0, time.Since(start))
			return zero, callErr
		default:
		}
		var recvErr error
		frames, recvErr = sock.RecvFrames()
		if errors.Is(recvErr, ErrTimeout) {
			continue
		}
		if recvErr != nil {
			callErr = CallError{Err: fmt.Errorf("recv: %w", recvErr)}
			obs.RecordRequest("ZMQ-REQ", path, 0, time.Since(start))
			return zero, callErr
		}
		break
	}

	// validate framing
	if len(frames) < 2 {
		callErr = CallError{Err: fmt.Errorf("malformed reply: expected [status, payload], got %d frame(s)", len(frames))}
		obs.RecordRequest("ZMQ-REQ", path, 0, time.Since(start))
		return zero, callErr
	}

	// check status
	if string(frames[0]) == "error" {
		callErr = CallError{Err: fmt.Errorf("server error: %s", frames[1])}
		obs.RecordRequest("ZMQ-REQ", path, 500, time.Since(start))
		return zero, callErr
	}

	// Resolve per-call response format override.
	respFormats, fmtErr := resolveCallFormat[Resp](handle.Formats, opts.ResponseFormats)
	if fmtErr != nil {
		callErr = CallError{Err: fmtErr}
		obs.RecordRequest("ZMQ-REQ", path, 0, time.Since(start))
		return zero, callErr
	}

	// decode response
	var resp Resp
	if len(respFormats) > 0 {
		resp, callErr = respFormats[0].Unmarshal(frames[1])
	} else {
		resp, callErr = handle.DecodeResponse(frames[1])
	}
	if callErr != nil {
		stats.ReportErrors(obs, "body", callErr)
		callErr = CallError{Err: fmt.Errorf("decode response: %w", callErr)}
		obs.RecordRequest("ZMQ-REQ", path, 0, time.Since(start))
		return zero, callErr
	}
	obs.RecordRequest("ZMQ-REQ", path, 200, time.Since(start))
	return resp, nil
}

// CallHandle is the single-call convenience wrapper around [Call]: it
// derives [CallOptions.Vars] from req automatically, using the route's
// merge-capable topic params ([reqreply.RouteHandle.MergeFields] +
// [codex.EncodeVars]) — mirrors [nethttp.CallWithHandle]/[mqtt5.CallHandle].
//
// An explicit [CallOptions.Vars] takes PRECEDENCE over the derived value.
// [Call] remains the lower-level escape hatch.
//
// Note: this convenience is CLIENT-SIDE only. ZMQ REQ/REP routing is
// socket-based, not topic-based — [Serve]'s incoming messages carry no
// per-message topic string to extract vars FROM (unlike MQTT's
// broker-routed topics), so there is no server-side decode-merge
// equivalent for zeromq. The resolved topic here is used only for codec
// validation and observer reporting, matching [CallOptions.Vars]'s
// existing documented behavior.
//
//	resp, err := zeromq.CallHandle(ctx, sock, computeRoute, req, zeromq.CallOptions{})
func CallHandle[Req, Resp any](
	ctx context.Context,
	sock FramedSocket,
	handle *reqreply.RouteHandle[Req, Resp],
	req Req,
	opts CallOptions,
) (Resp, error) {
	var zero Resp
	derived, err := codex.EncodeVars(req, handle.MergeFields()...)
	if err != nil {
		return zero, err
	}
	if len(derived) == 0 {
		derived = nil
	}
	if opts.Vars != nil {
		merged := make(map[string]string, len(derived)+len(opts.Vars))
		for k, v := range derived {
			merged[k] = v
		}
		for k, v := range opts.Vars {
			merged[k] = v
		}
		opts.Vars = merged
	} else {
		opts.Vars = derived
	}
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

// sendHandlerErrorReply is the [reqreply.ErrorPattern]-aware counterpart of
// [sendErrorReply], used for handler/encode failures — errors that originate
// from application business logic, where a declared ErrorPattern may apply.
// It consults handle.ErrorResponseFor(err) first: on a match, the declared
// codec-backed typed payload is sent instead of plain text. On no match, or
// on a mapping/encoding failure within the matched pattern itself, it falls
// back to [sendErrorReply]'s plain-text behavior unchanged (backward
// compatible — existing ErrorReplyMeta-only or no-declaration routes see no
// behavior change).
func sendHandlerErrorReply[Req, Resp any](
	sock FramedSocket,
	handle *reqreply.RouteHandle[Req, Resp],
	err error,
	obs stats.Observer,
) {
	resp, matched, mapErr := handle.ErrorResponseFor(err)
	if matched && mapErr == nil {
		_ = sock.SendFrames([][]byte{statusError, resp.Body})
		return
	}
	if matched && mapErr != nil {
		stats.ReportErrors(obs, "error_pattern", mapErr)
	}
	sendErrorReply(sock, err)
}

// emptyDelimiter is the empty frame separating the identity stack from the
// payload in DEALER/ROUTER ZMQ envelope format.
var emptyDelimiter = []byte{}

// ServeRouter runs a blocking ROUTER loop. Each incoming request is dispatched
// concurrently in its own goroutine. Identity frames are extracted automatically
// and re-prepended to every reply so the DEALER peer can correlate responses.
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
	obs := opts.Observer
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}
	if err := sock.SetRecvTimeout(recvPollInterval); err != nil {
		return SocketError{Op: "set_recv_timeout", Err: err}
	}
	path := handle.Topic

	var wg sync.WaitGroup
	for {
		select {
		case <-ctx.Done():
			wg.Wait()
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
				wg.Wait()
				return nil
			default:
				return SocketError{Op: "recv", Err: err}
			}
		}
		// Expect at least [identity, delimiter, payload].
		if len(frames) < 3 {
			continue
		}
		identity := frames[0]
		// frames[1] = empty delimiter
		payload := frames[2]

		wg.Add(1)
		go func(id, pl []byte) {
			defer wg.Done()
			start := time.Now()
			serveRouterRequest(ctx, sock, handle, fn, obs, opts, path, id, pl, start)
		}(identity, payload)
	}
}

// serveRouterRequest handles one ROUTER request in its own goroutine.
func serveRouterRequest[Req, Resp any](
	ctx context.Context,
	sock FramedSocket,
	handle *reqreply.RouteHandle[Req, Resp],
	fn func(context.Context, Req) (Resp, error),
	obs stats.Observer,
	opts ServeOptions,
	path string,
	identity []byte,
	payload []byte,
	start time.Time,
) {
	var spanCtx = ctx
	var serveErr error
	if to, ok := obs.(stats.TraceObserver); ok {
		spanCtx = to.StartSpan(ctx, "zmq.serve", path)
	}
	defer func() {
		if to, ok := obs.(stats.TraceObserver); ok {
			to.EndSpan(spanCtx, serveErr)
		}
	}()

	// decode request
	var req Req
	if len(handle.RequestFormats) > 0 {
		req, serveErr = handle.RequestFormats[0].Unmarshal(payload)
	} else {
		req, serveErr = handle.Decode(payload)
	}
	if serveErr != nil {
		stats.ReportErrors(obs, "body", serveErr)
		obs.RecordRequest("ZMQ-ROUTER", path, 0, time.Since(start))
		sendRouterErrorReply(sock, identity, serveErr)
		if opts.OnError != nil {
			opts.OnError(ServeError{Kind: KindDecode, Err: serveErr})
		}
		return
	}

	// call handler
	var resp Resp
	resp, serveErr = fn(spanCtx, req)
	if serveErr != nil {
		obs.RecordRequest("ZMQ-ROUTER", path, 0, time.Since(start))
		sendRouterHandlerErrorReply(sock, identity, handle, serveErr, obs)
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
		obs.RecordRequest("ZMQ-ROUTER", path, 0, time.Since(start))
		sendRouterHandlerErrorReply(sock, identity, handle, serveErr, obs)
		if opts.OnError != nil {
			opts.OnError(ServeError{Kind: KindEncode, Err: serveErr})
		}
		return
	}

	if sendErr := sock.SendFrames([][]byte{identity, emptyDelimiter, statusOK, respPayload}); sendErr != nil {
		serveErr = sendErr
		obs.RecordRequest("ZMQ-ROUTER", path, 0, time.Since(start))
		if opts.OnError != nil {
			opts.OnError(ServeError{Kind: KindEncode, Err: sendErr})
		}
		return
	}
	obs.RecordRequest("ZMQ-ROUTER", path, 200, time.Since(start))
}

// sendRouterErrorReply sends an error reply to a ROUTER peer, preserving
// identity frames, as plain text. Used directly for decode-level errors (no
// [reqreply.ErrorPattern] can apply yet).
func sendRouterErrorReply(sock FramedSocket, identity []byte, err error) {
	_ = sock.SendFrames([][]byte{identity, emptyDelimiter, statusError, []byte(err.Error())})
}

// sendRouterHandlerErrorReply is the [reqreply.ErrorPattern]-aware
// counterpart of [sendRouterErrorReply], used for handler/encode failures —
// see [sendHandlerErrorReply] for the matching/fallback semantics.
func sendRouterHandlerErrorReply[Req, Resp any](
	sock FramedSocket,
	identity []byte,
	handle *reqreply.RouteHandle[Req, Resp],
	err error,
	obs stats.Observer,
) {
	resp, matched, mapErr := handle.ErrorResponseFor(err)
	if matched && mapErr == nil {
		_ = sock.SendFrames([][]byte{identity, emptyDelimiter, statusError, resp.Body})
		return
	}
	if matched && mapErr != nil {
		stats.ReportErrors(obs, "error_pattern", mapErr)
	}
	sendRouterErrorReply(sock, identity, err)
}

// CallDealer encodes req and sends it via a DEALER socket using the ZMQ envelope
// format (empty delimiter + payload), then synchronously waits for one reply.
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
	obs := opts.Observer
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}
	var zero Resp
	start := time.Now()
	path := handle.Topic
	var callErr error

	// Resolve template topic vars if provided.
	if opts.Vars != nil {
		var buildErr error
		path, buildErr = handle.BuildTopic(opts.Vars)
		if buildErr != nil {
			stats.ReportErrors(obs, "topic_var", buildErr)
			obs.RecordRequest("ZMQ-DEALER", handle.Topic, 0, time.Since(start))
			callErr = CallError{Err: buildErr}
			return zero, callErr
		}
	}

	if to, ok := obs.(stats.TraceObserver); ok {
		ctx = to.StartSpan(ctx, "zmq.request", path)
		defer func() { to.EndSpan(ctx, callErr) }()
	}

	// Resolve per-call request format override.
	reqFormats, fmtErr := resolveCallFormat[Req](handle.RequestFormats, opts.RequestFormats)
	if fmtErr != nil {
		obs.RecordRequest("ZMQ-DEALER", path, 0, time.Since(start))
		callErr = CallError{Err: fmtErr}
		return zero, callErr
	}

	// encode request
	var payload []byte
	if len(reqFormats) > 0 {
		payload, callErr = reqFormats[0].Marshal(req)
	} else {
		payload, callErr = handle.EncodeRequest(req)
	}
	if callErr != nil {
		stats.ReportErrors(obs, "body", callErr)
		obs.RecordRequest("ZMQ-DEALER", path, 0, time.Since(start))
		callErr = CallError{Err: callErr}
		return zero, callErr
	}

	// send with empty delimiter (DEALER envelope)
	if err := sock.SendFrames([][]byte{emptyDelimiter, payload}); err != nil {
		callErr = CallError{Err: fmt.Errorf("send: %w", err)}
		obs.RecordRequest("ZMQ-DEALER", path, 0, time.Since(start))
		return zero, callErr
	}

	// configure recv timeout for ctx-cancellation polling
	if err := sock.SetRecvTimeout(recvPollInterval); err != nil {
		callErr = CallError{Err: fmt.Errorf("set recv timeout: %w", err)}
		return zero, callErr
	}

	// receive reply
	var frames [][]byte
	for {
		select {
		case <-ctx.Done():
			callErr = CallError{Err: ctx.Err()}
			obs.RecordRequest("ZMQ-DEALER", path, 0, time.Since(start))
			return zero, callErr
		default:
		}
		var recvErr error
		frames, recvErr = sock.RecvFrames()
		if errors.Is(recvErr, ErrTimeout) {
			continue
		}
		if recvErr != nil {
			callErr = CallError{Err: fmt.Errorf("recv: %w", recvErr)}
			obs.RecordRequest("ZMQ-DEALER", path, 0, time.Since(start))
			return zero, callErr
		}
		break
	}

	// validate framing: expect ["", status, payload]
	if len(frames) < 3 {
		callErr = CallError{Err: fmt.Errorf("malformed reply: expected [\"\", status, payload], got %d frame(s)", len(frames))}
		obs.RecordRequest("ZMQ-DEALER", path, 0, time.Since(start))
		return zero, callErr
	}
	// frames[0] = empty delimiter

	// check status
	if string(frames[1]) == "error" {
		callErr = CallError{Err: fmt.Errorf("server error: %s", frames[2])}
		obs.RecordRequest("ZMQ-DEALER", path, 500, time.Since(start))
		return zero, callErr
	}

	// Resolve per-call response format override.
	respFormats, fmtErr := resolveCallFormat[Resp](handle.Formats, opts.ResponseFormats)
	if fmtErr != nil {
		callErr = CallError{Err: fmtErr}
		obs.RecordRequest("ZMQ-DEALER", path, 0, time.Since(start))
		return zero, callErr
	}

	// decode response
	var resp Resp
	if len(respFormats) > 0 {
		resp, callErr = respFormats[0].Unmarshal(frames[2])
	} else {
		resp, callErr = handle.DecodeResponse(frames[2])
	}
	if callErr != nil {
		stats.ReportErrors(obs, "body", callErr)
		callErr = CallError{Err: fmt.Errorf("decode response: %w", callErr)}
		obs.RecordRequest("ZMQ-DEALER", path, 0, time.Since(start))
		return zero, callErr
	}
	obs.RecordRequest("ZMQ-DEALER", path, 200, time.Since(start))
	return resp, nil
}
