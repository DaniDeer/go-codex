package mqtt5

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	pahomqtt5 "github.com/eclipse/paho.golang/paho"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/stats"
)

// eventsPkgPath is api/events' import path — used to distinguish a
// genuine events.Publisher[T]/events.Subscriber[T] value (for ANY T)
// from an unrelated/wrong-package value passed by caller mistake to
// [Client.Publish]/[Client.Subscribe].
const eventsPkgPath = "github.com/DaniDeer/go-codex/api/events"

// defaultQoS is the fallback used by the reflection shim's error-channel
// reply publish (see [transport.Subscribe]) — matches MQTT's own
// protocol-level default (at-most-once) and every other error-channel
// dispatch site's fixed QoS-0/non-retained choice in this package,
// deliberately NOT affected by a declared Capabilities value. The
// MAIN-PATH Publish/Subscribe QoS is resolved from a declared
// [Publisher.WithOptions]/[Subscriber.WithOptions] value as of
// docs/design/d-0006-protocol-native-capabilities.md's Phase 4c — see
// [subscribeHandlerOptsFields]/[publishHandlerOptsFields].
const defaultQoS byte = 0

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
// surface for an mqtt5 [events.Transport] (docs/design/
// d-0006-protocol-native-capabilities.md's Phase 4d: a single Options
// struct, no positional params, even for these two REQUIRED fields — a
// deliberate, uniform, declarative shape across every adapter's
// `New*Transport` factory).
type TransportOptions struct {
	// Client is the MQTT 5 broker connection. Required.
	Client MQTTClient
	// Router dispatches incoming messages to registered handlers.
	// Required.
	Router MQTTRouter
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
// Call/Consume are FULL-FEATURED (docs/design/
// d-0006-protocol-native-capabilities.md's Phase 4e — the former "v1
// scope" narrowing is CLOSED for this package): declared Capabilities
// (Phase 4c), per-call [format.Format] overrides
// ([events.ClientPublishOptions]/[events.ClientSubscribeOptions]),
// property-merge, User-Property-param validation, codec-based AND
// declarative SubscribeMW/PublishMW security enforcement (credential-
// paired or general-purpose wrapping), and codec-backed Middleware/
// Transform dispatch are ALL supported — there is no remaining "v1
// scope" asterisk, mirroring [adapters/nethttp/clienttransport.go]'s
// own "no remaining v1 scope asterisk" wording. [stats.Observer]
// (RecordPublish/RecordSubscribe, TraceObserver) is fully wired,
// resolved from ctx same as [subscribe]/[Publish]; every pipeline
// failure point consults a declared [events.ErrorChannel]/
// [events.DeadLetter]/a declared OnError callback, in that fallback
// order — see
// docs/design/d-0002-pubsub-workflow-simplification.md's Decision 8 for the
// fix history.
//
// Unlike [subscribeWithHandle] (non-blocking — registers with the router
// and returns immediately, dispatch happens via the router's OWN
// callback mechanism), [Client.Subscribe] BLOCKS until ctx is cancelled —
// a deliberate uniform contract across every [events.Transport]
// implementation (zeromq's own Subscribe genuinely blocks on its receive
// loop; mqtt5's shim adds the SAME guarantee on top of its naturally
// non-blocking primitive, for a consistent Client.Subscribe caller
// experience regardless of which adapter is attached).
//
//	transport := mqtt5.NewTransport(mqtt5.TransportOptions{Client: mqttClient, Router: router})
//	if err := evClient.Attach(transport); err != nil { ... }
func NewTransport(opts TransportOptions) events.Transport {
	return &transport{caller: newCaller(opts.Client, opts.Router, nil)}
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
// mirrors [adapters/zeromq]'s identical helper.
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

// Capabilities extraction now happens via [subscribeHandlerOptsFields]/
// [publishHandlerOptsFields] (transport_dispatch.go) — extended, Phase
// 4e versions of this file's former resolveHandlerOptsCapabilities,
// which also extract OnError/UserPropertyParams/ContentType/
// UserProperties from the SAME type-erased HandlerOpts value.

// Publish implements [events.Transport]. Resolves [stats.Observer] from
// ctx and calls RecordPublish on EVERY exit path, mirroring [publish]'s
// own convention.
//
// docs/design/d-0006-protocol-native-capabilities.md's Phase 4e: this
// shim now runs the FULL [publish][T] pipeline, matched step-for-step
// (property-merge → codec-Middleware/Transform dispatch → security
// credential resolution+validation → general-purpose PublishMW wrapping
// around encode+transmit), not just Capabilities (Phase 4c) — closing
// the "v1 scope" gap entirely. opts is an OPTIONAL, PER-CALL
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
		ctx = to.StartSpan(ctx, "mqtt5.publish", topic)
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
	generalFnType := generalWrapFnType(tType)
	if err = validateClientImplementationShapesReflect(clientImplementations, secFnType, generalFnType); err != nil {
		obs.RecordPublish(topic, false, time.Since(start))
		return err
	}

	// valuePtr is addressable so a security credential Fn can mutate the
	// outgoing message in-place (in-payload credential embedding) — see
	// [runPublishSecurityImpls][T]'s own doc comment.
	valuePtr := reflect.New(tType)
	valuePtr.Elem().Set(msgVal)

	// Channel-level MergedPropertyParam merge fields declared DIRECTLY on
	// NewChannel derive their values FROM msg — mirrors [publish][T]'s
	// own property-merge step, run BEFORE codec-Middleware dispatch (a
	// middleware's own WithPublishProperty-derived value overrides this
	// channel-own-derived value on a key collision, same precedence
	// direction topic vars use).
	var propertyVars map[string]string
	propertyMergeFieldsLen := handleVal.MethodByName("PropertyMergeFields").Call(nil)[0].Len()
	if propertyMergeFieldsLen > 0 {
		propResults := handleVal.MethodByName("EncodePropertyVars").Call([]reflect.Value{msgVal})
		if propErr, _ := propResults[1].Interface().(error); propErr != nil {
			stats.ReportErrors(obs, "property_var", propErr)
			obs.RecordPublish(topic, false, time.Since(start))
			err = propErr
			bestEffort, _ := handleVal.MethodByName("Encode").Call([]reflect.Value{msgVal})[0].Interface().([]byte)
			publishDeadLetterReflect(ctx, t, handleVal, obs, topic, bestEffort, err)
			return err
		}
		propertyVars, _ = propResults[0].Interface().(map[string]string)
	}

	// Codec-backed middleware dispatch (ClientTransform and bundled
	// .Use()) — derived BEFORE security, mirroring [publish][T]'s own
	// ordering (a middleware's own Out may contribute additional topic
	// vars BuildTopic needs, plus property vars merged into userProps
	// below).
	clientMiddlewareHandlersLen := elem.FieldByName("ClientMiddlewareHandlers").Len()
	var vars map[string]string
	if clientMiddlewareHandlersLen > 0 {
		mwResults := handleVal.MethodByName("DispatchPublishMiddleware").Call([]reflect.Value{reflect.ValueOf(ctx), msgVal})
		mwTopicVars, _ := mwResults[0].Interface().(map[string]string)
		mwPropVars, _ := mwResults[1].Interface().(map[string]string)
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
			publishDeadLetterReflect(ctx, t, handleVal, obs, topic, bestEffort, err)
			return err
		}
		vars = events.OverrideDerivedVars(vars, mwTopicVars)
		propertyVars = events.OverrideDerivedVars(propertyVars, mwPropVars)
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

	// Security requirement resolution + credential Fn dispatch — BEFORE
	// encoding, so any *msg mutation (in-payload credential embedding) is
	// captured by the encode step below — mirrors [publish][T]'s own
	// ordering exactly.
	contentType, declaredUserProps, caps := publishHandlerOptsFields(elem.FieldByName("HandlerOpts"))
	secReqs := resolveSecReqsReflect(elem, "Publish")
	userProps := append([]UserProperty(nil), declaredUserProps...)
	for k, v := range propertyVars {
		userProps = append(userProps, UserProperty{Key: k, Value: v})
	}
	var credentialRan bool
	if len(clientImplementations) > 0 {
		implProps, secErr := runPublishSecurityImplsReflect(reflect.ValueOf(ctx), valuePtr, secReqs, clientImplementations, secFnType)
		if secErr != nil {
			obs.RecordPublish(finalTopic, false, time.Since(start))
			err = secErr
			return err
		}
		if implProps != nil {
			credentialRan = true
		}
		userProps = append(userProps, implProps...)
	}
	if len(secReqs) > 0 && credentialRan {
		securitySchemes, _ := elem.FieldByName("SecuritySchemes").Interface().(map[string]events.SecurityScheme)
		schemeTypes := make(map[string]route.SecurityScheme, len(securitySchemes))
		schemeCodecs := make(map[string]*codex.Codec[string], len(securitySchemes))
		for name, s := range securitySchemes {
			schemeTypes[name] = s.SecurityScheme
			schemeCodecs[name] = s.Codec
		}
		if name, credErr := validateSecurityCredentials(userProps, secReqs, schemeTypes, schemeCodecs); credErr != nil {
			if secObs, ok := obs.(stats.SecurityObserver); ok {
				secObs.RecordSecurityRejection(finalTopic, route.FirstSchemeName(secReqs))
			}
			obs.RecordPublish(finalTopic, false, time.Since(start))
			err = events.SecurityCredentialError{Scheme: name, Err: credErr}
			return err
		}
	}

	// docs/design/d-0006-protocol-native-capabilities.md's Phase 4c: a
	// declared [Publisher.WithOptions]([PublishOptions][T]{Capabilities:
	// ...}) value is resolved and applied via [events.ApplyCapabilities].
	var wire WireAttributes
	events.ApplyCapabilities(caps, &wire, obs, finalTopic)

	props := &pahomqtt5.PublishProperties{}
	if contentType != "" {
		props.ContentType = contentType
	}
	if len(userProps) > 0 {
		props.User = userProps
	}

	// transmit is wrapped via [reflect.MakeFunc] so every attached
	// general-purpose PublishMW Fn (a real, concretely-typed Go closure)
	// can compose around it — mirrors [wrapPublishGeneral][T]'s exact
	// wrap boundary (encode → send, NOT security/middleware — those run
	// OUTSIDE the wrap, same as [publish][T]).
	transmit := reflect.MakeFunc(wantHandlerFnType, func(args []reflect.Value) []reflect.Value {
		mVal := args[1]
		encodeResults := encodeWithFormatsMethod.CallSlice([]reflect.Value{mVal, formatsOverride})
		if encErr, _ := encodeResults[1].Interface().(error); encErr != nil {
			stats.ReportErrors(obs, "payload", encErr)
			return []reflect.Value{reflect.ValueOf(PublishEncodeError{Topic: finalTopic, Err: encErr}).Convert(dispatchErrType)}
		}
		payload, _ := encodeResults[0].Interface().([]byte)
		if _, pubErr := t.caller.client.Publish(ctx, &pahomqtt5.Publish{
			Topic:      finalTopic,
			QoS:        wire.QoS,
			Retain:     wire.Retained,
			Payload:    payload,
			Properties: props,
		}); pubErr != nil {
			return []reflect.Value{reflect.ValueOf(BrokerError{Op: "publish", Err: pubErr}).Convert(dispatchErrType)}
		}
		return []reflect.Value{reflect.Zero(dispatchErrType)}
	})
	transmit = wrapClientGeneralDecoratorReflect(transmit, clientImplementations, generalFnType)

	transmitResults := transmit.Call([]reflect.Value{reflect.ValueOf(ctx), valuePtr.Elem()})
	if txErr, _ := transmitResults[0].Interface().(error); txErr != nil {
		obs.RecordPublish(finalTopic, false, time.Since(start))
		err = txErr
		bestEffort, _ := handleVal.MethodByName("Encode").Call([]reflect.Value{msgVal})[0].Interface().([]byte)
		publishDeadLetterReflect(ctx, t, handleVal, obs, finalTopic, bestEffort, err)
		return err
	}
	obs.RecordPublish(finalTopic, true, time.Since(start))
	return nil
}

// publishDeadLetterReflect is the publish-side reflection-only mirror of
// adapter.go's tryDeadLetter — Topic 4: a failed publish (never reached
// the broker) is ALSO dead-letterable, alongside the synchronous error
// returned to the caller. No ErrorChannel step exists on the publish
// side (ErrorChannel is a subscribe-side-only mechanism — a publish
// failure has no "declared error response channel" to consult).
func publishDeadLetterReflect(ctx context.Context, t *transport, handleVal reflect.Value, obs stats.Observer, topic string, payload []byte, err error) {
	dlResults := handleVal.MethodByName("DeadLetterFor").Call([]reflect.Value{
		reflect.ValueOf(obs), reflect.ValueOf(topic), reflect.ValueOf(payload), reflect.ValueOf(&err).Elem(),
	})
	dlTopic, _ := dlResults[0].Interface().(string)
	dlBody, _ := dlResults[1].Interface().([]byte)
	dlOk, _ := dlResults[2].Interface().(bool)
	if dlOk {
		_, _ = t.caller.client.Publish(ctx, &pahomqtt5.Publish{Topic: dlTopic, QoS: defaultQoS, Payload: dlBody})
	}
}

// Subscribe implements [events.Transport]. Registers a reflection-built
// [pahomqtt5.MessageHandler] with the router (mirrors
// [subscribeWithHandle]'s registration step, since that function itself
// cannot be called with a runtime-only T) and issues the broker SUBSCRIBE,
// then BLOCKS until ctx is cancelled, unregistering the handler on exit —
// see [Attach]'s doc comment for why this blocks (a deliberate, uniform
// Client.Subscribe contract) even though the underlying mqtt5 dispatch
// mechanism itself is callback-driven, not loop-driven.
//
// docs/design/d-0006-protocol-native-capabilities.md's Phase 4e: this
// shim now runs the FULL [subscribeHandler][T] pipeline, matched
// step-for-step (property-merge → user-property-param validation →
// codec-based security credential check → Implementations-based security
// Fn dispatch → codec-Middleware/Transform dispatch → general-purpose
// SubscribeMW wrapping around the handler call), not just Capabilities
// (Phase 4c) — closing the "v1 scope" gap entirely. Every one of those
// failure points consults a declared [events.ErrorChannel]/
// [events.DeadLetter]/opts.OnError, in that fallback order, exactly as
// [makeSubscribeMessageHandler] already does — see [publishErrorReply]'s
// sibling dispatch below. opts is an OPTIONAL, PER-CALL
// [events.ClientSubscribeOptions] format override.
//
// [stats.Observer] is resolved from ctx ONCE and RecordSubscribe is
// called PER INCOMING MESSAGE (mirrors [subscribeHandler]'s own
// per-message convention, not a single call for the whole blocking
// Subscribe).
func (t *transport) Subscribe(ctx context.Context, subAny, fnAny any, optsVariadic ...events.ClientSubscribeOptions) error {
	obs := stats.ObserverFromContext(ctx)

	handleVal, elem, err := recoverHandle("Subscriber", subAny, t.caller.events)
	if err != nil {
		return err
	}
	topic := elem.FieldByName("Topic").String()

	decodeMergedMethod := handleVal.MethodByName("DecodeMergedWithFormats") // (payload []byte, vars map[string]string, formats ...format.Format[T]) (T, error)
	errorResponseForMethod := handleVal.MethodByName("ErrorResponseFor")
	deadLetterForMethod := handleVal.MethodByName("DeadLetterFor") // (obs stats.Observer, sourceTopic string, rawPayload []byte, err error) (topic string, body []byte, ok bool)
	mergePropertyVarsMethod := handleVal.MethodByName("MergePropertyVars")
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

	onErrorFn, userPropertyParams, caps := subscribeHandlerOptsFields(elem.FieldByName("HandlerOpts"))

	// Every attached [events.ChannelHandle.Implementations] Fn (from
	// [Subscriber.SubscribeMW]) is shape-validated EAGERLY here, before
	// the broker subscription is made — mirrors
	// [subscribeWithHandle]'s own "validated EAGERLY... a malformed Fn
	// fails loudly and immediately" timing (docs/design/
	// d-0006-protocol-native-capabilities.md's Phase 4e design decision:
	// per-call, not Attach-time, validation).
	implementations, _ := elem.FieldByName("Implementations").Interface().([]middleware.ServerImplementation)
	if err := validateSubscribeImplementationShapesReflect(tType, implementations); err != nil {
		return err
	}
	// General-purpose wrapping composes ONCE, outside the per-message
	// loop — mirrors [subscribeWithHandle]'s `fn = wrapSubscribeGeneral(fn, ...)`.
	fnVal = wrapHandlerGeneralReflect(fnVal, implementations)

	filter := deriveWildcardFilter(topic)

	var wire WireAttributes
	events.ApplyCapabilities(caps, &wire, obs, topic)

	secReqs := resolveSecReqsReflect(elem, "Subscribe")
	securitySchemes, _ := elem.FieldByName("SecuritySchemes").Interface().(map[string]events.SecurityScheme)
	middlewareHandlersLen := elem.FieldByName("MiddlewareHandlers").Len()
	propertyMergeFieldsLen := handleVal.MethodByName("PropertyMergeFields").Call(nil)[0].Len()

	// dispatchFailure is the shared ErrorChannel→DeadLetter→OnError
	// fallback triplet every pipeline step below consults on failure —
	// mirrors [tryPublishErrorChannel]/[tryDeadLetter]/opts.OnError's
	// exact fallback order, factored into one closure to avoid repeating
	// it at all 6 call sites (decode, property-merge, user-property-param,
	// Implementations-security, codec-Middleware, handler).
	dispatchFailure := func(kind ErrorKind, sourceTopic string, payload []byte, failErr error) {
		errResults := errorResponseForMethod.Call([]reflect.Value{reflect.ValueOf(&failErr).Elem()})
		resp, _ := errResults[0].Interface().(events.ErrorChannelResponse)
		matched, _ := errResults[1].Interface().(bool)
		matchErrI, _ := errResults[2].Interface().(error)
		if matched && matchErrI == nil && resp.Action == events.ErrorRespond {
			// handled=true (mirrors tryPublishErrorChannel's exact
			// contract): a matched, published ErrorRespond skips
			// DeadLetter AND opts.OnError entirely — the caller must
			// return immediately, never falling through to its own
			// OnError fallback.
			_, _ = t.caller.client.Publish(ctx, &pahomqtt5.Publish{Topic: resp.Topic, QoS: defaultQoS, Payload: resp.Body})
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
				_, _ = t.caller.client.Publish(ctx, &pahomqtt5.Publish{Topic: dlTopic, QoS: defaultQoS, Payload: dlBody})
				return
			}
		}
		// matched but a non-Respond action (ErrorHandle/ErrorLog): falls
		// through DIRECTLY to opts.OnError, skipping DeadLetter — same
		// as an unmatched-and-no-DeadLetter-declared case.
		if onErrorFn != nil {
			onErrorFn(SubscribeError{Kind: kind, Topic: sourceTopic, Err: failErr})
		}
	}

	handler := func(msg *pahomqtt5.Publish) {
		start := time.Now()
		// docs/design/d-0006-protocol-native-capabilities.md's Phase 4e
		// addendum: msgCtx stores msg AND its User Properties, closing a
		// gap where [MessageFromContext]/[UserPropertiesFromContext]
		// never worked through Client.Subscribe — mirrors
		// [makeSubscribeMessageHandler]'s IDENTICAL, pre-decode placement
		// exactly (a caller reading these inside its handler must see
		// them regardless of which dispatch path decoded the message).
		msgCtx := context.WithValue(ctx, contextKey{}, msg)
		if msg.Properties != nil && len(msg.Properties.User) > 0 {
			msgCtx = context.WithValue(msgCtx, userPropsKey{}, msg.Properties.User)
		}
		vars, matchErr := matchTopicTemplate(topic, msg.Topic)
		if matchErr != nil {
			return // broader wildcard subscription received a non-matching topic — expected, not an error
		}
		decodeResults := decodeMergedMethod.CallSlice([]reflect.Value{reflect.ValueOf(msg.Payload), reflect.ValueOf(vars), formatsOverride})
		if decErr, _ := decodeResults[1].Interface().(error); decErr != nil {
			stats.ReportErrors(obs, "payload", decErr)
			obs.RecordSubscribe(msg.Topic, false, time.Since(start))
			dispatchFailure(KindDecode, msg.Topic, msg.Payload, decErr)
			return
		}
		valuePtr := reflect.New(tType)
		valuePtr.Elem().Set(decodeResults[0])

		// Property vocabulary axis: extract real MQTT5 User Properties
		// into a plain map for codec-Middleware dispatch AND
		// channel-level MergedPropertyParam merge fields — mirrors
		// [makeSubscribeMessageHandler]'s identical extraction, kept
		// SEPARATE from topic vars (independent namespace/consumer).
		var propertyVars map[string]string
		if middlewareHandlersLen > 0 || propertyMergeFieldsLen > 0 {
			propertyVars = make(map[string]string)
			if msg.Properties != nil {
				for _, p := range msg.Properties.User {
					propertyVars[p.Key] = p.Value
				}
			}
			if propertyMergeFieldsLen > 0 {
				mergeResults := mergePropertyVarsMethod.Call([]reflect.Value{valuePtr, reflect.ValueOf(propertyVars)})
				if mergeErr, _ := mergeResults[0].Interface().(error); mergeErr != nil {
					stats.ReportErrors(obs, "property_var", mergeErr)
					obs.RecordSubscribe(msg.Topic, false, time.Since(start))
					dispatchFailure(KindDecode, msg.Topic, msg.Payload, mergeErr)
					return
				}
			}
		}

		// User Property param validation — runs before security
		// enforcement, mirrors [makeSubscribeMessageHandler]'s ordering.
		if propErr := validateUserProperties(msg, userPropertyParams); propErr != nil {
			obs.RecordValidationError("user_property", stats.ConstraintName(propErr), userPropertyName(propErr))
			obs.RecordSubscribe(msg.Topic, false, time.Since(start))
			dispatchFailure(KindSecurity, msg.Topic, msg.Payload, propErr)
			return
		}

		// Built-in codec-based credential check — runs BEFORE any
		// declarative SubscribeMW security implementation. Deliberately
		// does NOT consult ErrorChannel/DeadLetter (mirrors
		// [makeSubscribeMessageHandler]'s own codec-credential-check
		// call site exactly — falls straight to OnError).
		if len(secReqs) > 0 {
			if err := runErasedBuiltinSecurityCheck(msg, secReqs, securitySchemes); err != nil {
				if secObs, ok := obs.(stats.SecurityObserver); ok {
					secObs.RecordSecurityRejection(msg.Topic, route.FirstSchemeName(secReqs))
				}
				obs.RecordSubscribe(msg.Topic, false, time.Since(start))
				if onErrorFn != nil {
					onErrorFn(SubscribeError{Kind: KindSecurity, Topic: msg.Topic, Err: err})
				}
				return
			}
		}

		// Implementations-based security check (from SubscribeMW) — runs
		// UNCONDITIONALLY, mirroring [makeSubscribeMessageHandler].
		if len(implementations) > 0 {
			if err := runSubscribeSecurityImplsReflect(msgCtx, msg, valuePtr, secReqs, implementations); err != nil {
				if secObs, ok := obs.(stats.SecurityObserver); ok {
					secObs.RecordSecurityRejection(msg.Topic, route.FirstSchemeName(secReqs))
				}
				obs.RecordSubscribe(msg.Topic, false, time.Since(start))
				dispatchFailure(KindSecurity, msg.Topic, msg.Payload, events.SecurityError{Err: err})
				return
			}
		}

		// Codec-backed middleware dispatch (Transform and bundled
		// .Use()) — SAME pre-handler dispatch point the security
		// Implementations check just ran at, reusing the SAME
		// topicVars/propertyVars derived above.
		if middlewareHandlersLen > 0 {
			mwResults := dispatchSubscribeMiddlewareMethod.Call([]reflect.Value{
				reflect.ValueOf(msgCtx), valuePtr, reflect.ValueOf(vars), reflect.ValueOf(propertyVars),
			})
			if mwErr, _ := mwResults[0].Interface().(error); mwErr != nil {
				obs.RecordSubscribe(msg.Topic, false, time.Since(start))
				dispatchErr, _ := events.AsMiddlewareDispatchError(mwErr)
				if dispatchErr.IsFnError {
					stats.ReportErrors(obs, "middleware:fn", dispatchErr.Err)
					dispatchFailure(KindHandler, msg.Topic, msg.Payload, events.MiddlewareError{Name: dispatchErr.Name, Err: dispatchErr.Err})
				} else {
					stats.ReportErrors(obs, "middleware:in", dispatchErr.Err)
					dispatchFailure(KindDecode, msg.Topic, msg.Payload, dispatchErr.Err)
				}
				return
			}
		}

		spanCtx := msgCtx
		if to, ok := obs.(stats.TraceObserver); ok {
			spanCtx = to.StartSpan(msgCtx, "mqtt5.subscribe", msg.Topic)
		}
		fnResults := fnVal.Call([]reflect.Value{reflect.ValueOf(spanCtx), valuePtr.Elem()})
		handlerErr, _ := fnResults[0].Interface().(error)
		if to, ok := obs.(stats.TraceObserver); ok {
			to.EndSpan(spanCtx, handlerErr)
		}
		if handlerErr == nil {
			obs.RecordSubscribe(msg.Topic, true, time.Since(start))
			return
		}
		stats.ReportErrors(obs, "topic_var", handlerErr)
		obs.RecordSubscribe(msg.Topic, false, time.Since(start))
		dispatchFailure(KindHandler, msg.Topic, msg.Payload, handlerErr)
	}

	t.caller.router.RegisterHandler(filter, handler)
	if _, err := t.caller.client.Subscribe(ctx, &pahomqtt5.Subscribe{
		Subscriptions: []pahomqtt5.SubscribeOptions{{Topic: filter, QoS: wire.QoS}},
	}); err != nil {
		t.caller.router.UnregisterHandler(filter)
		return BrokerError{Op: "subscribe", Err: err}
	}

	<-ctx.Done()
	t.caller.router.UnregisterHandler(filter)
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
