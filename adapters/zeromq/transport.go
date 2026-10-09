package zeromq

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/DaniDeer/go-codex/adapters/internal/scopesmerge"
	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/internal/middleware"
	"github.com/DaniDeer/go-codex/internal/route"
	"github.com/DaniDeer/go-codex/stats"
)

// eventsPkgPath is api/events' import path — used to distinguish a
// genuine events.Publisher[T]/events.Subscriber[T] value (for ANY T)
// from an unrelated/wrong-package value passed by caller mistake to
// [Client.Publish]/[Client.Subscribe].
const eventsPkgPath = "github.com/DaniDeer/go-codex/api/events"

// transport implements [events.Transport] AND [events.ClientAwareTransport]
// (via [transport.BindClient]), wrapping an internal [*caller] — built by
// [NewTransport]. See docs/design/d-0002-pubsub-workflow-simplification.md's
// Decision 5 for the full design and the reflection technique this type
// relies on (Go forbids generic methods, so Publish/Subscribe/
// ServeSubscribers recover the concrete payload type at runtime via
// reflection against the ALREADY-CONCRETE closures on the type-erased
// events.ChannelHandle — never via reflecting a generic FUNCTION, which
// Go does not support).
type transport struct {
	caller *caller
}

// TransportOptions configures [NewTransport] — the SOLE configuration
// surface for a zeromq [events.Transport] (docs/design/
// d-0006-protocol-native-capabilities.md's Phase 4d: a single Options
// struct, no positional params, even for this one REQUIRED field — a
// deliberate, uniform, declarative shape across every adapter's
// `New*Transport` factory).
type TransportOptions struct {
	// Socket is the ZeroMQ PUB/SUB socket. Required.
	Socket FramedSocket
}

// NewTransport returns an [events.Transport] configured per opts — the
// adapter's ONLY job in the attach workflow (docs/design/
// d-0006-protocol-native-capabilities.md's Phase 4d): construct a
// fully-configured, attachable value. Attaching it is EXCLUSIVELY
// [events.Client.Attach]'s job — there is no adapter-namespaced Attach
// function anymore (REMOVED, breaking, per that phase's explicit
// "zero backdoor between the api layer and the adapters" directive).
// The returned value also implements [events.ClientAwareTransport] —
// [events.Client.Attach] supplies the [*events.Client] reference this
// shim needs (spec-registration + [ServeSubscribers]'s registry walk)
// via [transport.BindClient], immediately after storing it; NewTransport
// itself never needs a [*events.Client] parameter.
//
//	client := events.NewClient(events.WithInfo(events.Info{Title: "Sensor Network", Version: "1.0.0"}))
//	if err := client.Attach(zeromq.NewTransport(zeromq.TransportOptions{Socket: sock})); err != nil { ... }
//	sub := ReadingsChannel.WithSubscribe(events.Subscribe{})
//	pub := ReadingsChannel.WithPublish(events.Publish{})
//	err := client.Subscribe(ctx, sub, func(ctx context.Context, r SensorReading) error { ... })
//	err = client.Publish(ctx, pub, reading)
//
// Publish/Subscribe are FULL-FEATURED (docs/design/
// d-0006-protocol-native-capabilities.md's Phase 4e — the former "v1
// scope" narrowing is CLOSED for this package): declared Capabilities
// (Phase 4c), per-call [format.Format] overrides
// ([events.ClientPublishOptions]/[events.ClientSubscribeOptions]),
// declarative SubscribeMW/PublishMW security enforcement (credential-
// paired or general-purpose wrapping), and codec-backed Middleware/
// SubscribeBoundMW/PublishBoundMW dispatch are ALL supported (zeromq has no property-
// vocabulary axis and no built-in codec-based credential check, unlike
// mqtt5 — those 2 pipeline steps simply don't exist for this adapter,
// matching [subscribeHandler][T]/[publish][T]'s own narrower real
// pipeline) — there is no remaining "v1 scope" asterisk.
// [stats.Observer] (RecordPublish/RecordSubscribe, TraceObserver) IS
// fully wired; a subscribe handler's returned error also consults a
// declared [events.ErrorChannel]/[events.DeadLetter]/a declared OnError
// callback, in that fallback order — see
// docs/design/d-0002-pubsub-workflow-simplification.md's Decision 8 for the
// fix history.
func NewTransport(opts TransportOptions) events.Transport {
	return &transport{caller: newCaller(opts.Socket, nil)}
}

// BindClient implements [events.ClientAwareTransport] — called by
// [events.Client.Attach] immediately after storing t, supplying the
// [*events.Client] reference [recoverHandle] (spec-registration) and
// [ServeSubscribers] (registry walk) need.
func (t *transport) BindClient(c *events.Client) error {
	t.caller.events = c
	return nil
}

// recoverHandle accepts EITHER a bare events.Subscriber[T]/
// events.Publisher[T] (calls its Handle(client) method via reflection) OR
// an already-built *events.ChannelHandle[T] (used directly, no further
// Handle() call — the caller already composed it themselves, e.g. via
// .Handle(client, events.WithRouter(rt))).
func recoverHandle(kind string, anyAny any, client *events.Client) (reflect.Value, reflect.Value, error) {
	v := reflect.ValueOf(anyAny)
	if !v.IsValid() {
		return reflect.Value{}, reflect.Value{}, events.TransportTypeMismatchError{
			Want: fmt.Sprintf("events.%s[T] or *events.ChannelHandle[T]", kind), Got: fmt.Sprintf("%T", anyAny),
		}
	}
	t := v.Type()
	switch {
	case t.PkgPath() == eventsPkgPath && strings.HasPrefix(t.Name(), kind+"["):
		handleMethod := v.MethodByName("Handle")
		results := handleMethod.Call([]reflect.Value{reflect.ValueOf(client)})
		if errI, _ := results[1].Interface().(error); errI != nil {
			return reflect.Value{}, reflect.Value{}, errI
		}
		handleVal := results[0]
		return handleVal, handleVal.Elem(), nil
	case t.Kind() == reflect.Pointer && t.Elem().PkgPath() == eventsPkgPath && strings.HasPrefix(t.Elem().Name(), "ChannelHandle["):
		// Already a pre-built *events.ChannelHandle[T] (e.g. from
		// Subscriber.Handle/Publisher.Handle(client, events.WithRouter(rt))
		// — the caller already composed the Router's prefix themselves) —
		// use it directly, no re-Handle() call (mirrors
		// adapters/nethttp.recoverClientRouteHandleValue's identical
		// dual-mode acceptance for api/rest).
		return v, v.Elem(), nil
	default:
		return reflect.Value{}, reflect.Value{}, events.TransportTypeMismatchError{
			Want: fmt.Sprintf("events.%s[T] or *events.ChannelHandle[T]", kind), Got: fmt.Sprintf("%T", anyAny),
		}
	}
}

// Capabilities extraction now happens via [subscribeHandlerOptsFields]/
// [publishHandlerOptsFields] (transport_dispatch.go) — extended, Phase
// 4e versions of this file's former resolveHandlerOptsCapabilities,
// which also extract OnError from the SAME type-erased HandlerOpts
// value.

// Publish implements [events.Transport]. Resolves [stats.Observer] from
// ctx and calls RecordPublish on EVERY exit path, mirroring [publish]'s
// own convention.
//
// docs/design/d-0006-protocol-native-capabilities.md's Phase 4e: this
// shim now runs the FULL [publish][T] pipeline, matched step-for-step
// (codec-Middleware/SubscribeBoundMW dispatch → Implementations-based
// PublishMW security → general-purpose PublishMW wrapping around the
// send), not just Capabilities (Phase 4c) — closing the "v1 scope" gap
// entirely (zeromq has no property-vocabulary axis and no built-in
// codec-based credential check, unlike mqtt5 — those 2 pipeline steps
// simply don't exist for this adapter, matching [publish][T]'s own,
// narrower real pipeline). opts is an OPTIONAL, PER-CALL
// [events.ClientPublishOptions] format override.
func (t *transport) Publish(ctx context.Context, pubAny, msgAny any, optsVariadic ...events.ClientPublishOptions) (err error) {
	obs := stats.ObserverFromContext(ctx)
	start := time.Now()

	handleVal, elem, err := recoverHandle("Publisher", pubAny, t.caller.events)
	if err != nil {
		return err
	}
	topic := elem.FieldByName("Topic").String()

	if to, ok := obs.(stats.TraceObserver); ok {
		ctx = to.StartSpan(ctx, "zmq.publish", topic)
		defer func() { to.EndSpan(ctx, err) }()
	}

	encodeWithFormatsMethod := handleVal.MethodByName("EncodeWithFormats") // func(T, ...format.Format[T]) ([]byte, error)
	tType := encodeWithFormatsMethod.Type().In(0)
	msgVal := reflect.ValueOf(msgAny)
	if !msgVal.IsValid() || msgVal.Type() != tType {
		obs.RecordPublish(topic, false, time.Since(start))
		err = events.TransportTypeMismatchError{Topic: topic, Want: tType.String(), Got: fmt.Sprintf("%T", msgAny)}
		return err
	}

	var callOpts events.ClientPublishOptions
	if len(optsVariadic) > 0 {
		callOpts = optsVariadic[0]
	}
	formatsOverride, formatsErr := resolveFormatsOverride(callOpts.Formats, encodeWithFormatsMethod.Type().In(1))
	if formatsErr != nil {
		obs.RecordPublish(topic, false, time.Since(start))
		err = events.TransportTypeMismatchError{Topic: topic, Want: encodeWithFormatsMethod.Type().In(1).String(), Got: fmt.Sprintf("%T", callOpts.Formats)}
		return err
	}

	clientImplementations, _ := elem.FieldByName("ClientImplementations").Interface().([]middleware.ClientImplementation)
	wantHandlerFnType := reflect.FuncOf([]reflect.Type{dispatchCtxType, tType}, []reflect.Type{dispatchErrType}, false)
	secFnType := buildPublishSecurityFnType(tType)
	generalFnType := buildPubSubGeneralDecoratorFnType(wantHandlerFnType)
	if err = validateClientImplementationShapesReflect(clientImplementations, secFnType, generalFnType); err != nil {
		obs.RecordPublish(topic, false, time.Since(start))
		return err
	}

	valuePtr := reflect.New(tType)
	valuePtr.Elem().Set(msgVal)

	// Codec-backed middleware dispatch (PublishBoundMW and bundled
	// .Use()) — mirrors [publish][T]'s own ordering (derived BEFORE
	// security). zeromq has no property mechanism — supplies a nil
	// property-value map, mirroring adapter.go's identical
	// extraction-then-supply pattern.
	clientMiddlewareHandlersLen := elem.FieldByName("ClientMiddlewareHandlers").Len()
	var vars map[string]string
	if clientMiddlewareHandlersLen > 0 {
		mwResults := handleVal.MethodByName("DispatchPublishMiddleware").Call([]reflect.Value{reflect.ValueOf(ctx), msgVal})
		mwTopicVars, _ := mwResults[0].Interface().(map[string]string)
		if mwErr, _ := mwResults[2].Interface().(error); mwErr != nil {
			loc := "middleware:fn"
			reported := mwErr
			if dispatchErr, ok := events.AsMiddlewareDispatchError(mwErr); ok {
				if dispatchErr.IsEncodeErr {
					loc = "middleware:out"
				}
				reported = dispatchErr.Err
				mwErr = dispatchErr.Err
			}
			stats.ReportErrors(obs, loc, reported)
			obs.RecordPublish(topic, false, time.Since(start))
			err = mwErr
			bestEffort, _ := handleVal.MethodByName("Encode").Call([]reflect.Value{msgVal})[0].Interface().([]byte)
			publishDeadLetterReflect(t, handleVal, obs, topic, bestEffort, err)
			return err
		}
		vars = events.OverrideDerivedVars(vars, mwTopicVars)
	}

	// Derive topic vars from msg's merge-capable NewTopicParam fields
	// (empty map when the channel declares none) via the reflection-
	// friendly ChannelHandle.EncodeVars method.
	varsResults := handleVal.MethodByName("EncodeVars").Call([]reflect.Value{msgVal})
	if errI, _ := varsResults[1].Interface().(error); errI != nil {
		obs.RecordPublish(topic, false, time.Since(start))
		err = errI
		return err
	}
	channelVars, _ := varsResults[0].Interface().(map[string]string)
	vars = events.OverrideDerivedVars(channelVars, vars)

	finalTopic := topic
	if len(vars) > 0 {
		topicResults := handleVal.MethodByName("BuildTopic").Call([]reflect.Value{reflect.ValueOf(vars)})
		if errI, _ := topicResults[1].Interface().(error); errI != nil {
			obs.RecordPublish(topic, false, time.Since(start))
			err = errI
			return err
		}
		finalTopic, _ = topicResults[0].Interface().(string)
	}

	// Implementations-based security (PublishMW) — mirrors [publish][T]'s
	// ordering. zeromq has no built-in codec-based credential check
	// (unlike mqtt5) — Implementations IS the entire security mechanism.
	secReqs := resolveSecReqsReflect(elem, "Publish")
	if len(clientImplementations) > 0 {
		if secErr := runPublishSecurityImplsReflect(reflect.ValueOf(ctx), valuePtr, secReqs, clientImplementations, secFnType); secErr != nil {
			if secObs, ok := obs.(stats.SecurityObserver); ok {
				secObs.RecordSecurityRejection(finalTopic, route.FirstSchemeName(secReqs))
			}
			obs.RecordPublish(finalTopic, false, time.Since(start))
			err = secErr
			return err
		}
	}

	// docs/design/d-0006-protocol-native-capabilities.md's Phase 4c: a
	// declared [Publisher.WithOptions]([PublishOptions]{Capabilities:
	// ...}) value is resolved and applied via [events.ApplyCapabilities]
	// against t.caller.sock.
	caps := publishHandlerOptsFields(elem.FieldByName("HandlerOpts"))
	// Publish-side Tier 1 coverage check — confirmed, PREVIOUSLY-MISSING
	// gap: a channel's own declared Requirements was verified on the
	// SUBSCRIBE side (ServeSubscribers) but never on this ports.Pattern
	// binding's PUBLISH side. Mirrors Transport.Subscribe's identical fix.
	if requirements, ok := elem.FieldByName("Requirements").Interface().([]events.CapabilityRequirement); ok {
		if covErr := events.VerifyCapabilityCoverage(finalTopic, requirements, caps); covErr != nil {
			obs.RecordPublish(finalTopic, false, time.Since(start))
			return covErr
		}
	}
	events.ApplyCapabilities(caps, t.caller.sock, obs, finalTopic)

	// transmit is wrapped via [reflect.MakeFunc] so every attached
	// general-purpose PublishMW Fn can compose around it — mirrors
	// [wrapPublishGeneral][T]'s exact wrap boundary (encode → send).
	transmit := reflect.MakeFunc(wantHandlerFnType, func(args []reflect.Value) []reflect.Value {
		mVal := args[1]
		// The channel's OWN declaration (WithFormats/WithPublishFormats)
		// is the single source of truth for which format applies —
		// EncodeWithFormats resolves it.
		encodeResults := encodeWithFormatsMethod.CallSlice([]reflect.Value{mVal, formatsOverride})
		if encErr, _ := encodeResults[1].Interface().(error); encErr != nil {
			stats.ReportErrors(obs, "payload", encErr)
			return []reflect.Value{reflect.ValueOf(PublishEncodeError{Topic: finalTopic, Err: encErr}).Convert(dispatchErrType)}
		}
		payload, _ := encodeResults[0].Interface().([]byte)
		if sendErr := t.caller.sock.SendFrames([][]byte{[]byte(finalTopic), payload}); sendErr != nil {
			return []reflect.Value{reflect.ValueOf(SocketError{Op: "send", Err: sendErr}).Convert(dispatchErrType)}
		}
		return []reflect.Value{reflect.Zero(dispatchErrType)}
	})
	transmit = wrapClientGeneralDecoratorReflect(transmit, clientImplementations, generalFnType)

	transmitResults := transmit.Call([]reflect.Value{reflect.ValueOf(ctx), valuePtr.Elem()})
	if txErr, _ := transmitResults[0].Interface().(error); txErr != nil {
		obs.RecordPublish(finalTopic, false, time.Since(start))
		err = txErr
		bestEffort, _ := handleVal.MethodByName("Encode").Call([]reflect.Value{msgVal})[0].Interface().([]byte)
		publishDeadLetterReflect(t, handleVal, obs, finalTopic, bestEffort, err)
		return err
	}
	obs.RecordPublish(finalTopic, true, time.Since(start))
	return nil
}

// publishDeadLetterReflect is the publish-side reflection-only mirror of
// adapter.go's tryDeadLetter — Topic 4: a failed publish (never reached
// the broker) is ALSO dead-letterable, alongside the synchronous error
// returned to the caller. No ErrorChannel step exists on the publish
// side (ErrorChannel is a subscribe-side-only mechanism).
func publishDeadLetterReflect(t *transport, handleVal reflect.Value, obs stats.Observer, topic string, payload []byte, err error) {
	dlResults := handleVal.MethodByName("DeadLetterFor").Call([]reflect.Value{
		reflect.ValueOf(obs), reflect.ValueOf(topic), reflect.ValueOf(payload), reflect.ValueOf(&err).Elem(),
	})
	dlTopic, _ := dlResults[0].Interface().(string)
	dlBody, _ := dlResults[1].Interface().([]byte)
	dlOk, _ := dlResults[2].Interface().(bool)
	if dlOk {
		_ = t.caller.sock.SendFrames([][]byte{[]byte(dlTopic), dlBody})
	}
}

// Subscribe implements [events.Transport]. Unlike a scratch-client
// approach (which would register sub's spec into a THROWAWAY client,
// never the one the caller attached), this registers into the REAL
// attached client (via sub.Handle(t.caller.events) — spec-only, does NOT
// touch any subscriber registry, so it never conflicts with OTHER
// subscriptions on the same client) so [events.Client.AsyncAPISpec]
// correctly includes this operation, then runs a DEDICATED receive loop
// scoped to just this one channel.
//
// docs/design/d-0006-protocol-native-capabilities.md's Phase 4e: this
// shim now runs the FULL [subscribeHandler][T] pipeline, matched
// step-for-step (codec-Middleware/SubscribeBoundMW dispatch → Implementations-
// based SubscribeMW security → general-purpose SubscribeMW wrapping
// around the handler call), not just Capabilities (Phase 4c) — closing
// the "v1 scope" gap entirely (zeromq has no property-vocabulary axis
// and no built-in codec-based credential check, unlike mqtt5 — those 2
// pipeline steps simply don't exist for this adapter). Every failure
// point consults a declared [events.ErrorChannel]/[events.DeadLetter]/a
// declared OnError callback, in that fallback order — CLOSING a
// previously-silent gap where this shim never consulted DeadLetter at
// all. opts is an OPTIONAL, PER-CALL [events.ClientSubscribeOptions]
// format override.
func (t *transport) Subscribe(ctx context.Context, subAny, fnAny any, optsVariadic ...events.ClientSubscribeOptions) error {
	obs := stats.ObserverFromContext(ctx)

	handleVal, elem, err := recoverHandle("Subscriber", subAny, t.caller.events)
	if err != nil {
		return err
	}
	topic := elem.FieldByName("Topic").String()

	decodeMergedMethod := handleVal.MethodByName("DecodeMergedWithFormats") // (payload []byte, vars map[string]string, formats ...format.Format[T]) (T, error)
	errorResponseForMethod := handleVal.MethodByName("ErrorResponseFor")
	deadLetterForMethod := handleVal.MethodByName("DeadLetterFor")
	dispatchSubscribeMiddlewareMethod := handleVal.MethodByName("DispatchSubscribeMiddleware")
	fnVal := reflect.ValueOf(fnAny)
	tType := decodeMergedMethod.Type().Out(0)
	wantFnType := reflect.FuncOf(
		[]reflect.Type{dispatchCtxType, tType},
		[]reflect.Type{dispatchErrType},
		false,
	)
	if !fnVal.IsValid() || fnVal.Type() != wantFnType {
		return events.TransportTypeMismatchError{Topic: topic, Want: wantFnType.String(), Got: fmt.Sprintf("%T", fnAny)}
	}

	var callOpts events.ClientSubscribeOptions
	if len(optsVariadic) > 0 {
		callOpts = optsVariadic[0]
	}
	formatsOverride, formatsErr := resolveFormatsOverride(callOpts.Formats, decodeMergedMethod.Type().In(2))
	if formatsErr != nil {
		return events.TransportTypeMismatchError{Topic: topic, Want: decodeMergedMethod.Type().In(2).String(), Got: fmt.Sprintf("%T", callOpts.Formats)}
	}

	onErrorFn, caps := subscribeHandlerOptsFields(elem.FieldByName("HandlerOpts"))

	// Every attached [events.ChannelHandle.Implementations] Fn (from
	// [Subscriber.SubscribeMW]) is shape-validated EAGERLY here, before
	// the socket subscription is made.
	implementations, _ := elem.FieldByName("Implementations").Interface().([]middleware.ServerImplementation)
	secFnType := buildSubscribeSecurityFnType(tType)
	generalFnType := buildPubSubGeneralDecoratorFnType(wantFnType)
	if err := validateSubscribeImplementationShapesReflect(topic, tType, implementations); err != nil {
		return err
	}
	// General-purpose wrapping composes ONCE, outside the per-message
	// loop — mirrors [subscribeWithHandle]'s `fn = wrapSubscribeGeneral(fn, ...)`.
	fnVal = wrapServerGeneralDecoratorReflect(fnVal, implementations, generalFnType)

	filter := deriveTopicPrefix(topic)
	if err := t.caller.sock.SetSubscription(filter); err != nil {
		return SocketError{Op: "set_subscription", Err: err}
	}
	if err := t.caller.sock.SetRecvTimeout(recvPollInterval); err != nil {
		return SocketError{Op: "set_recv_timeout", Err: err}
	}

	// Subscribe-side Tier 1 coverage check — this ports.Pattern-binding
	// reflection path (Transport.Subscribe) is a SEPARATE dispatch path
	// from ServeSubscribers (which already has this check) and had the
	// SAME confirmed-missing gap. Mirrors Transport.Publish's identical fix.
	if requirements, ok := elem.FieldByName("Requirements").Interface().([]events.CapabilityRequirement); ok {
		if covErr := events.VerifyCapabilityCoverage(topic, requirements, caps); covErr != nil {
			return covErr
		}
	}

	// docs/design/d-0006-protocol-native-capabilities.md's Phase 4c: a
	// declared [Subscriber.WithOptions]([SubscribeOptions]{Capabilities:
	// ...}) value is resolved and applied via [events.ApplyCapabilities]
	// against t.caller.sock.
	events.ApplyCapabilities(caps, t.caller.sock, obs, topic)

	secReqs := resolveSecReqsReflect(elem, "Subscribe")
	middlewareHandlers, _ := elem.FieldByName("MiddlewareHandlers").Interface().([]events.MiddlewareHandler)
	middlewareHandlersLen := len(middlewareHandlers)
	middlewareSatisfies := make([][]string, len(middlewareHandlers))
	for i, h := range middlewareHandlers {
		middlewareSatisfies[i] = h.Satisfies
	}

	// dispatchFailure is the shared ErrorChannel→DeadLetter→OnError
	// fallback triplet every pipeline step below consults on failure —
	// mirrors [tryPublishErrorChannel]/[tryDeadLetter]/opts.OnError's
	// exact fallback order, factored into one closure to avoid repeating
	// it at every call site.
	dispatchFailure := func(kind ErrorKind, sourceTopic string, payload []byte, failErr error) {
		errResults := errorResponseForMethod.Call([]reflect.Value{reflect.ValueOf(&failErr).Elem()})
		resp, _ := errResults[0].Interface().(events.ErrorChannelResponse)
		matched, _ := errResults[1].Interface().(bool)
		matchErrI, _ := errResults[2].Interface().(error)
		if matched && matchErrI == nil && resp.Action == events.ErrorRespond {
			// handled=true (mirrors tryPublishErrorChannel's exact
			// contract): a matched, published ErrorRespond skips
			// DeadLetter AND opts.OnError entirely.
			if pubErr := t.caller.sock.SendFrames([][]byte{[]byte(resp.Topic), resp.Body}); pubErr != nil {
				stats.ReportErrors(obs, "error_channel", pubErr)
			}
			return
		}
		if !matched {
			dlResults := deadLetterForMethod.Call([]reflect.Value{
				reflect.ValueOf(obs), reflect.ValueOf(sourceTopic), reflect.ValueOf(payload), reflect.ValueOf(&failErr).Elem(),
			})
			dlTopic, _ := dlResults[0].Interface().(string)
			dlBody, _ := dlResults[1].Interface().([]byte)
			dlOk, _ := dlResults[2].Interface().(bool)
			if dlOk {
				_ = t.caller.sock.SendFrames([][]byte{[]byte(dlTopic), dlBody})
				return
			}
		}
		// matched but a non-Respond action (ErrorHandle/ErrorLog): falls
		// through DIRECTLY to opts.OnError, skipping DeadLetter.
		if onErrorFn != nil {
			onErrorFn(SubscribeError{Kind: kind, Topic: sourceTopic, Err: failErr})
		}
	}

	ctx = middleware.EnsureContextFields(ctx)
	ctxVal := reflect.ValueOf(ctx)
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		frames, recvErr := t.caller.sock.RecvFrames()
		if errors.Is(recvErr, ErrTimeout) {
			continue
		}
		if recvErr != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				return SocketError{Op: "recv", Err: recvErr}
			}
		}
		if len(frames) < 2 {
			continue // malformed: expect [topic, payload]
		}
		gotTopic := string(frames[0])
		payload := frames[1]
		start := time.Now()

		vars, matchErr := matchTopicTemplate(topic, gotTopic)
		if matchErr != nil {
			continue // topic prefix subscription received a non-matching topic — expected, not an error
		}

		decodeResults := decodeMergedMethod.CallSlice([]reflect.Value{reflect.ValueOf(payload), reflect.ValueOf(vars), formatsOverride})
		if decErr, _ := decodeResults[1].Interface().(error); decErr != nil {
			stats.ReportErrors(obs, "payload", decErr)
			obs.RecordSubscribe(gotTopic, false, time.Since(start))
			dispatchFailure(KindDecode, gotTopic, payload, decErr)
			continue
		}
		valuePtr := reflect.New(tType)
		valuePtr.Elem().Set(decodeResults[0])

		// Implementations-based security (SubscribeMW) — runs
		// UNCONDITIONALLY, mirroring [subscribeHandler][T]. zeromq has no
		// built-in codec-based credential check (unlike mqtt5) —
		// Implementations IS the entire security mechanism.
		if len(implementations) > 0 {
			if secErr := runSubscribeSecurityImplsReflect(ctxVal, valuePtr, secReqs, implementations, secFnType); secErr != nil {
				if secObs, ok := obs.(stats.SecurityObserver); ok {
					secObs.RecordSecurityRejection(gotTopic, route.FirstSchemeName(secReqs))
				}
				obs.RecordSubscribe(gotTopic, false, time.Since(start))
				dispatchFailure(KindSecurity, gotTopic, payload, events.SecurityError{Err: secErr})
				continue
			}
		}

		// Codec-backed middleware dispatch (SubscribeBoundMW and bundled
		// .Use()) — SAME pre-handler dispatch point the security
		// Implementations check just ran at. zeromq has no property
		// mechanism — supplies a nil property-value map. CORRECTED: a
		// bound MiddlewareHandler's own GrantedScopes-carrying Out is now
		// merged and checked — zeromq's legacy mechanism has NO grants
		// concept (pure binary accept/reject), so the unified CheckScopes
		// call is gated on scopesmerge.HasSatisfyingHandler, NOT on
		// secReqs alone (mirrors adapter.go's own established zeromq
		// gate).
		if middlewareHandlersLen > 0 {
			mwResults := dispatchSubscribeMiddlewareMethod.Call([]reflect.Value{
				reflect.ValueOf(ctx), valuePtr, reflect.ValueOf(vars), reflect.Zero(reflect.TypeOf(map[string]string(nil))),
			})
			outs, _ := mwResults[0].Interface().([]any)
			if mwErr, _ := mwResults[1].Interface().(error); mwErr != nil {
				obs.RecordSubscribe(gotTopic, false, time.Since(start))
				dispatchErr, _ := events.AsMiddlewareDispatchError(mwErr)
				if dispatchErr.IsFnError {
					stats.ReportErrors(obs, "middleware:fn", dispatchErr.Err)
					dispatchFailure(KindHandler, gotTopic, payload, events.MiddlewareError{Name: dispatchErr.Name, Err: dispatchErr.Err})
				} else {
					stats.ReportErrors(obs, "middleware:in", dispatchErr.Err)
					dispatchFailure(KindDecode, gotTopic, payload, dispatchErr.Err)
				}
				continue
			}
			granted := make(map[string][]string)
			scopesmerge.MergeHandlerGrants(granted, middlewareSatisfies, outs)
			if len(secReqs) > 0 && scopesmerge.HasSatisfyingHandler(middlewareSatisfies) {
				if err := middleware.CheckScopes(secReqs, granted); err != nil {
					if secObs, ok := obs.(stats.SecurityObserver); ok {
						secObs.RecordSecurityRejection(gotTopic, route.FirstSchemeName(secReqs))
					}
					obs.RecordSubscribe(gotTopic, false, time.Since(start))
					dispatchFailure(KindSecurity, gotTopic, payload, events.SecurityError{Err: err})
					continue
				}
			}
		}

		fnResults := fnVal.Call([]reflect.Value{ctxVal, valuePtr.Elem()})
		handlerErr, _ := fnResults[0].Interface().(error)
		if handlerErr == nil {
			obs.RecordSubscribe(gotTopic, true, time.Since(start))
			continue
		}
		obs.RecordSubscribe(gotTopic, false, time.Since(start))
		dispatchFailure(KindHandler, gotTopic, payload, handlerErr)
	}
}

// ServeSubscribers implements [events.Transport] — delegates directly to
// the wrapped [*Caller]'s own ServeSubscribers (non-generic, no
// reflection needed), which walks every [events.Subscriber] registered
// against t.caller.events via [events.Subscriber.Register].
func (t *transport) ServeSubscribers(ctx context.Context) error {
	return t.caller.ServeSubscribers(ctx)
}

var _ events.Transport = (*transport)(nil)
