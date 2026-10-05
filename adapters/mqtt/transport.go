package mqtt

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"

	"github.com/DaniDeer/go-codex/adapters/internal/scopesmerge"
	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/stats"
)

// eventsPkgPath is api/events' import path — used to distinguish a
// genuine events.Publisher[T]/events.Subscriber[T] value (for ANY T)
// from an unrelated/wrong-package value passed by caller mistake to
// [Client.Publish]/[Client.Subscribe].
const eventsPkgPath = "github.com/DaniDeer/go-codex/api/events"

// defaultQoS is the fixed QoS/non-retained value used for error-channel
// replies and dead-letter publishes (docs/design/
// d-0006-protocol-native-capabilities.md's Phase 4c) — matches MQTT's
// own protocol-level default (at-most-once) and deliberately never
// consults a declared Capabilities value, mirroring every other
// error-channel/dead-letter dispatch site
// across this codebase (these always use QoS 0/non-retained, regardless
// of the route's own declared Capabilities). Ordinary Publish/Subscribe
// dispatch resolves Capabilities via [events.ApplyCapabilities] against a
// [WireAttributes] value instead (Phase 5) — see [publish]'s doc comment.
const defaultQoS byte = 0

// resolveHandlerOptsCapabilities extracts the []Capability slice from a
// type-erased HandlerOpts value (a concrete mqtt.SubscribeOptions or
// mqtt.PublishOptions[T] boxed as any) via reflection against the fixed
// "Capabilities" field name — works regardless of T, since that field's
// type ([]Capability) doesn't depend on T. Returns nil (not an error)
// when handlerOpts is nil/wrong-shape/has no such field — a declared
// channel with no Capabilities is the common, valid case. Mirrors
// [adapters/mqtt5]'s identical helper.
func resolveHandlerOptsCapabilities(handlerOptsField reflect.Value) []Capability {
	if !handlerOptsField.IsValid() || handlerOptsField.IsNil() {
		return nil
	}
	v := reflect.ValueOf(handlerOptsField.Interface())
	if v.Kind() != reflect.Struct {
		return nil
	}
	capsField := v.FieldByName("Capabilities")
	if !capsField.IsValid() {
		return nil
	}
	caps, _ := capsField.Interface().([]Capability)
	return caps
}

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
// surface for an mqtt (v3) [events.Transport] (docs/design/
// d-0006-protocol-native-capabilities.md's Phase 4d: a single Options
// struct, no positional params, even for this one REQUIRED field — a
// deliberate, uniform, declarative shape across every adapter's
// `New*Transport` factory).
type TransportOptions struct {
	// Client is the MQTT v3.1.1 broker connection. Required.
	Client pahomqtt.Client
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
// Publish/Subscribe are FULL-FEATURED (docs/design/
// d-0006-protocol-native-capabilities.md's Phase 4e — the former "v1
// scope" narrowing is CLOSED for this package): declared Capabilities
// (Phase 5, resolved via [events.ApplyCapabilities] against a
// [WireAttributes] value — mirrors adapters/mqtt5's identical shape),
// per-call [format.Format] overrides
// ([events.ClientPublishOptions]/[events.ClientSubscribeOptions]),
// declarative SubscribeMW/PublishMW security enforcement (credential-
// paired or general-purpose wrapping), and codec-backed Middleware/
// SubscribeBoundMW/PublishBoundMW dispatch are ALL supported (mqtt v3 has no property-
// vocabulary axis and no built-in codec-based credential check, unlike
// mqtt5 — those 2 pipeline steps simply don't exist for this adapter) —
// there is no remaining "v1 scope" asterisk.
// [stats.Observer] (RecordPublish/RecordSubscribe, TraceObserver) IS
// fully wired, resolved from ctx same as [subscribe]/[publish]; a
// subscribe handler's returned error also consults a declared
// [events.ErrorChannel]/[events.DeadLetter]/a declared OnError callback,
// in that fallback order — see
// docs/design/d-0002-pubsub-workflow-simplification.md's Decision 8 for the
// fix history.
//
// [Client.Subscribe] BLOCKS until ctx is cancelled — a deliberate uniform
// contract across every [events.Transport] implementation, mirroring
// adapters/zeromq's and adapters/mqtt5's own shims.
//
//	transport := mqtt.NewTransport(mqtt.TransportOptions{Client: mqttClient})
//	if err := evClient.Attach(transport); err != nil { ... }
func NewTransport(opts TransportOptions) events.Transport {
	return &transport{caller: newCaller(opts.Client, nil)}
}

// BindClient implements [events.ClientAwareTransport] — called by
// [events.Client.Attach] immediately after storing t, supplying the
// [*events.Client] reference [recoverHandle] (spec-registration) and
// [ServeSubscribers] (registry walk) need.
func (t *transport) BindClient(c *events.Client) error {
	t.caller.events = c
	return nil
}

// recoverHandle calls anyAny's Handle(client) method via reflection —
// mirrors [adapters/zeromq]'s/[adapters/mqtt5]'s identical helper.
func recoverHandle(kind string, anyAny any, client *events.Client) (reflect.Value, reflect.Value, error) {
	v := reflect.ValueOf(anyAny)
	if !v.IsValid() || v.Type().PkgPath() != eventsPkgPath || !strings.HasPrefix(v.Type().Name(), kind+"[") {
		return reflect.Value{}, reflect.Value{}, events.TransportTypeMismatchError{
			Want: fmt.Sprintf("events.%s[T]", kind), Got: fmt.Sprintf("%T", anyAny),
		}
	}
	handleMethod := v.MethodByName("Handle")
	results := handleMethod.Call([]reflect.Value{reflect.ValueOf(client)})
	if errI, _ := results[1].Interface().(error); errI != nil {
		return reflect.Value{}, reflect.Value{}, errI
	}
	handleVal := results[0]
	return handleVal, handleVal.Elem(), nil
}

// Publish implements [events.Transport]. Resolves [stats.Observer] from
// ctx (this shim has no per-call Options struct to carry an explicit
// override) and calls RecordPublish on EVERY exit path, mirroring
// [publish]'s own convention.
//
// docs/design/d-0006-protocol-native-capabilities.md's Phase 4e: this
// shim now runs the FULL [publish][T] pipeline, matched step-for-step
// (codec-Middleware/SubscribeBoundMW dispatch → Implementations-based
// PublishMW security → general-purpose PublishMW wrapping around the
// send), not just Capabilities (Phase 4c) — closing the "v1 scope" gap
// entirely. Capabilities resolution is now via [events.ApplyCapabilities]
// against a [WireAttributes] value (Phase 5) — see [defaultQoS]'s doc
// comment. opts is an OPTIONAL, PER-CALL [events.ClientPublishOptions]
// format override.
func (t *transport) Publish(ctx context.Context, pubAny, msgAny any, optsVariadic ...events.ClientPublishOptions) (err error) {
	obs := stats.ObserverFromContext(ctx)
	start := time.Now()

	handleVal, elem, err := recoverHandle("Publisher", pubAny, t.caller.events)
	if err != nil {
		return err
	}
	topic := elem.FieldByName("Topic").String()

	if to, ok := obs.(stats.TraceObserver); ok {
		ctx = to.StartSpan(ctx, "mqtt.publish", topic)
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
	generalFnType := buildGeneralDecoratorFnType(wantHandlerFnType)
	if err = validateClientImplementationShapesReflect(clientImplementations, secFnType, generalFnType); err != nil {
		obs.RecordPublish(topic, false, time.Since(start))
		return err
	}

	valuePtr := reflect.New(tType)
	valuePtr.Elem().Set(msgVal)

	// Codec-backed middleware dispatch (PublishBoundMW and bundled
	// .Use()) — mirrors [publish][T]'s own ordering (derived BEFORE
	// security). mqtt v3 has no property mechanism — supplies a nil
	// property-value map.
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
			return err
		}
		vars = events.OverrideDerivedVars(vars, mwTopicVars)
	}

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
	// ordering. mqtt v3 has no built-in codec-based credential check —
	// Implementations IS the entire security mechanism.
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

	// docs/design/d-0006-protocol-native-capabilities.md's Phase 5: a
	// declared Capabilities value is applied via [events.ApplyCapabilities]
	// against a [WireAttributes] value — mirrors adapters/mqtt5's
	// identical, already-shipped shape exactly.
	caps := resolveHandlerOptsCapabilities(elem.FieldByName("HandlerOpts"))
	var wire WireAttributes
	events.ApplyCapabilities(caps, &wire, obs, finalTopic)

	// transmit is wrapped via [reflect.MakeFunc] so every attached
	// general-purpose PublishMW Fn can compose around it — mirrors
	// [wrapPublishGeneral][T]'s exact wrap boundary (encode → send).
	transmit := reflect.MakeFunc(wantHandlerFnType, func(args []reflect.Value) []reflect.Value {
		mVal := args[1]
		encodeResults := encodeWithFormatsMethod.CallSlice([]reflect.Value{mVal, formatsOverride})
		if encErr, _ := encodeResults[1].Interface().(error); encErr != nil {
			return []reflect.Value{reflect.ValueOf(fmt.Errorf("mqtt: encode: %w", encErr)).Convert(dispatchErrType)}
		}
		payload, _ := encodeResults[0].Interface().([]byte)
		token := t.caller.client.Publish(finalTopic, wire.QoS, wire.Retained, payload)
		token.Wait()
		if tokErr := token.Error(); tokErr != nil {
			return []reflect.Value{reflect.ValueOf(tokErr).Convert(dispatchErrType)}
		}
		return []reflect.Value{reflect.Zero(dispatchErrType)}
	})
	transmit = wrapClientGeneralDecoratorReflect(transmit, clientImplementations, generalFnType)

	transmitResults := transmit.Call([]reflect.Value{reflect.ValueOf(ctx), valuePtr.Elem()})
	if txErr, _ := transmitResults[0].Interface().(error); txErr != nil {
		obs.RecordPublish(finalTopic, false, time.Since(start))
		err = txErr
		return err
	}
	obs.RecordPublish(finalTopic, true, time.Since(start))
	return nil
}

// Subscribe implements [events.Transport]. Registers a reflection-built
// [pahomqtt.MessageHandler] directly via client.Subscribe (v3 has no
// router — the Subscribe call itself both registers AND performs the
// broker SUBSCRIBE, unlike mqtt5's separate router-registration step),
// then BLOCKS until ctx is cancelled, unsubscribing on exit — see
// [Attach]'s doc comment for why this blocks even though v3's dispatch
// mechanism itself is callback-driven, not loop-driven.
//
// docs/design/d-0006-protocol-native-capabilities.md's Phase 4e: this
// shim now runs the FULL [subscribeHandler][T] pipeline, matched
// step-for-step (Implementations-based SubscribeMW security → codec-
// Middleware/PublishBoundMW dispatch → general-purpose SubscribeMW wrapping
// around the handler call), not just Capabilities (Phase 4c) — closing
// the "v1 scope" gap entirely (mqtt v3 has no property-vocabulary axis
// and no built-in codec-based credential check, unlike mqtt5). Every
// failure point consults a declared [events.ErrorChannel]/
// [events.DeadLetter]/a declared OnError callback, in that fallback
// order — CLOSING a previously-silent gap where this shim never
// consulted DeadLetter at all. Capabilities resolution is now via
// [events.ApplyCapabilities] against a [WireAttributes] value (Phase 5).
// opts is an OPTIONAL, PER-CALL [events.ClientSubscribeOptions] format
// override.
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

	opts, err := resolveHandlerOpts(topic, elem.FieldByName("HandlerOpts").Interface())
	if err != nil {
		return err
	}

	// Every attached [events.ChannelHandle.Implementations] Fn (from
	// [Subscriber.SubscribeMW]) is shape-validated EAGERLY here, before
	// the broker subscription is made.
	implementations, _ := elem.FieldByName("Implementations").Interface().([]middleware.ServerImplementation)
	generalFnType := buildGeneralDecoratorFnType(wantFnType)
	if err := validateSubscribeImplementationShapesReflect(topic, tType, implementations); err != nil {
		return err
	}
	// General-purpose wrapping composes ONCE, outside the per-message
	// loop — mirrors [subscribeHandle]'s inline wrap loop.
	fnVal = wrapServerGeneralDecoratorReflect(fnVal, implementations, generalFnType)

	filter := deriveWildcardFilter(topic)
	secReqs := resolveSecReqsReflect(elem, "Subscribe")
	middlewareHandlers, _ := elem.FieldByName("MiddlewareHandlers").Interface().([]events.MiddlewareHandler)
	middlewareHandlersLen := len(middlewareHandlers)
	middlewareSatisfies := make([][]string, len(middlewareHandlers))
	for i, h := range middlewareHandlers {
		middlewareSatisfies[i] = h.Satisfies
	}

	// dispatchFailure is the shared ErrorChannel→DeadLetter→OnError
	// fallback triplet every pipeline step below consults on failure.
	dispatchFailure := func(kind ErrorKind, sourceTopic string, payload []byte, failErr error) {
		errResults := errorResponseForMethod.Call([]reflect.Value{reflect.ValueOf(&failErr).Elem()})
		resp, _ := errResults[0].Interface().(events.ErrorChannelResponse)
		matched, _ := errResults[1].Interface().(bool)
		matchErrI, _ := errResults[2].Interface().(error)
		if matched && matchErrI == nil && resp.Action == events.ErrorRespond {
			// handled=true (mirrors tryPublishErrorChannel's exact
			// contract): a matched, published ErrorRespond skips
			// DeadLetter AND opts.OnError entirely.
			token := t.caller.client.Publish(resp.Topic, defaultQoS, false, resp.Body)
			token.Wait()
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
				token := t.caller.client.Publish(dlTopic, defaultQoS, false, dlBody)
				token.Wait()
				return
			}
		}
		// matched but a non-Respond action (ErrorHandle/ErrorLog): falls
		// through DIRECTLY to opts.OnError, skipping DeadLetter.
		if opts.OnError != nil {
			opts.OnError(SubscribeError{Kind: kind, Topic: sourceTopic, Err: failErr})
		}
	}

	handler := func(client pahomqtt.Client, msg pahomqtt.Message) {
		start := time.Now()
		// docs/design/d-0006-protocol-native-capabilities.md's Phase 4e
		// addendum: msgCtx stores msg, closing a gap where
		// [MessageFromContext] never worked through Client.Subscribe —
		// mirrors [subscribeHandler][T]'s IDENTICAL, pre-decode placement
		// exactly.
		msgCtx := middleware.EnsureContextFields(context.WithValue(ctx, contextKey{}, msg))
		ctxVal := reflect.ValueOf(msgCtx)
		vars, matchErr := matchTopicTemplate(topic, msg.Topic())
		if matchErr != nil {
			return // broader wildcard subscription received a non-matching topic — expected, not an error
		}
		decodeResults := decodeMergedMethod.CallSlice([]reflect.Value{reflect.ValueOf(msg.Payload()), reflect.ValueOf(vars), formatsOverride})
		if decErr, _ := decodeResults[1].Interface().(error); decErr != nil {
			stats.ReportErrors(obs, "payload", decErr)
			obs.RecordSubscribe(msg.Topic(), false, time.Since(start))
			dispatchFailure(KindDecode, msg.Topic(), msg.Payload(), decErr)
			return
		}
		valuePtr := reflect.New(tType)
		valuePtr.Elem().Set(decodeResults[0])

		// Implementations-based security (SubscribeMW) — runs
		// UNCONDITIONALLY, mirroring [subscribeHandler][T]. mqtt v3 has
		// no built-in codec-based credential check — Implementations IS
		// the entire security mechanism.
		granted := make(map[string][]string)
		if len(implementations) > 0 {
			g, secErr := runSubscribeSecurityImplsReflect(msgCtx, msg, valuePtr, secReqs, implementations)
			if secErr != nil {
				if secObs, ok := obs.(stats.SecurityObserver); ok {
					secObs.RecordSecurityRejection(msg.Topic(), route.FirstSchemeName(secReqs))
				}
				obs.RecordSubscribe(msg.Topic(), false, time.Since(start))
				dispatchFailure(KindSecurity, msg.Topic(), msg.Payload(), events.SecurityError{Err: secErr})
				return
			}
			granted = g
		}

		// Codec-backed middleware dispatch (SubscribeBoundMW and bundled
		// .Use()) — SAME pre-handler dispatch point the security
		// Implementations check just ran at. mqtt v3 has no property
		// mechanism — supplies a nil property-value map. CORRECTED: a
		// bound MiddlewareHandler's own GrantedScopes-carrying Out is now
		// merged into the SAME `granted` map before the unified
		// CheckScopes call below (previously discarded entirely).
		if middlewareHandlersLen > 0 {
			mwResults := dispatchSubscribeMiddlewareMethod.Call([]reflect.Value{
				reflect.ValueOf(msgCtx), valuePtr, reflect.ValueOf(vars), reflect.Zero(reflect.TypeOf(map[string]string(nil))),
			})
			outs, _ := mwResults[0].Interface().([]any)
			if mwErr, _ := mwResults[1].Interface().(error); mwErr != nil {
				obs.RecordSubscribe(msg.Topic(), false, time.Since(start))
				dispatchErr, _ := events.AsMiddlewareDispatchError(mwErr)
				if dispatchErr.IsFnError {
					stats.ReportErrors(obs, "middleware:fn", dispatchErr.Err)
					dispatchFailure(KindHandler, msg.Topic(), msg.Payload(), events.MiddlewareError{Name: dispatchErr.Name, Err: dispatchErr.Err})
				} else {
					stats.ReportErrors(obs, "middleware:in", dispatchErr.Err)
					dispatchFailure(KindDecode, msg.Topic(), msg.Payload(), dispatchErr.Err)
				}
				return
			}
			scopesmerge.MergeHandlerGrants(granted, middlewareSatisfies, outs)
		}

		// ONE unified CheckScopes call covering grants from BOTH the
		// legacy Implementations path AND any bound MiddlewareHandler's
		// own GrantedScopes (merged above).
		if len(secReqs) > 0 {
			if err := middleware.CheckScopes(secReqs, granted); err != nil {
				if secObs, ok := obs.(stats.SecurityObserver); ok {
					secObs.RecordSecurityRejection(msg.Topic(), route.FirstSchemeName(secReqs))
				}
				obs.RecordSubscribe(msg.Topic(), false, time.Since(start))
				dispatchFailure(KindSecurity, msg.Topic(), msg.Payload(), events.SecurityError{Err: err})
				return
			}
		}

		fnResults := fnVal.Call([]reflect.Value{ctxVal, valuePtr.Elem()})
		handlerErr, _ := fnResults[0].Interface().(error)
		if handlerErr == nil {
			obs.RecordSubscribe(msg.Topic(), true, time.Since(start))
			return
		}
		obs.RecordSubscribe(msg.Topic(), false, time.Since(start))
		dispatchFailure(KindHandler, msg.Topic(), msg.Payload(), handlerErr)
	}

	// docs/design/d-0006-protocol-native-capabilities.md's Phase 5: a
	// declared Capabilities value is applied via
	// [events.ApplyCapabilities] against a [WireAttributes] value —
	// mirrors adapters/mqtt5's identical, already-shipped shape exactly.
	caps := resolveHandlerOptsCapabilities(elem.FieldByName("HandlerOpts"))
	var wire WireAttributes
	events.ApplyCapabilities(caps, &wire, obs, topic)

	subToken := t.caller.client.Subscribe(filter, wire.QoS, handler)
	subToken.Wait()
	if err := subToken.Error(); err != nil {
		return err
	}

	<-ctx.Done()
	unsubToken := t.caller.client.Unsubscribe(filter)
	unsubToken.Wait()
	return nil
}

// ServeSubscribers implements [events.Transport] — delegates directly to
// the wrapped [*caller]'s own ServeSubscribers (non-generic, no
// reflection needed), which walks every [events.Subscriber] registered
// against t.caller.events via [events.Subscriber.Register].
func (t *transport) ServeSubscribers(ctx context.Context) error {
	return t.caller.ServeSubscribers(ctx)
}

var _ events.Transport = (*transport)(nil)
