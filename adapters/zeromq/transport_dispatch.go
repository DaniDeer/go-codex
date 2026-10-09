package zeromq

import (
	"context"
	"fmt"
	"reflect"

	"github.com/DaniDeer/go-codex/internal/middleware"
	"github.com/DaniDeer/go-codex/internal/route"
)

// This file holds the reflection-only mirrors of adapter.go's generic
// security/general-middleware dispatch helpers
// (runSubscribeSecurityImpls[T]/wrapSubscribeGeneral[T]/
// validateSubscribeImplementationShapes[T] and their publish-side
// siblings runPublishSecurityImpls[T]/wrapPublishGeneral[T]/
// validatePublishImplementationShapes[T]) plus a few small
// HandlerOpts-extraction/security-requirement-resolution/format-override
// helpers — needed because transport.go's Client.Publish/Subscribe shim
// recovers T only at runtime (via reflection against the type-erased
// *events.ChannelHandle), so it cannot call the generic versions
// directly.
//
// The SUBSCRIBE-side shape-validation helper
// (validateSubscribeImplementationShapesReflect) ALREADY EXISTS in
// serve_subscribers.go, built for [(*caller).ServeSubscribers]'s own
// reflection-only dispatch — reused directly here rather than
// duplicated. zeromq has NO property-vocabulary axis (no MQTT5-style
// User Properties equivalent) and NO built-in codec-based credential
// check (unlike mqtt5) — only Implementations-based (SubscribeMW/
// PublishMW) security, matching adapter.go's own subscribeHandler[T]/
// publish[T] pipeline exactly.
//
// Per docs/design/d-0006-protocol-native-capabilities.md's Phase 4e:
// middleware.ServerImplementation/ClientImplementation are ALREADY
// non-generic (Fn any) — impl.Fn's boxed value is ALREADY a concretely-T
// Go closure built at declare time, reachable via
// reflect.ValueOf(impl.Fn).Call(...) without this package ever needing
// to know T at compile time.

var (
	dispatchCtxType = reflect.TypeOf((*context.Context)(nil)).Elem()
	dispatchErrType = reflect.TypeOf((*error)(nil)).Elem()
	dispatchSecReqs = reflect.TypeOf([]route.SecurityRequirement(nil))
)

// buildSubscribeSecurityFnType returns the reflect.Type every
// security-shaped [middleware.ServerImplementation.Fn] must match for a
// channel whose decoded payload type is tType —
// func(context.Context, *T, []route.SecurityRequirement) error.
func buildSubscribeSecurityFnType(tType reflect.Type) reflect.Type {
	return reflect.FuncOf(
		[]reflect.Type{dispatchCtxType, reflect.PointerTo(tType), dispatchSecReqs},
		[]reflect.Type{dispatchErrType},
		false,
	)
}

// buildPublishSecurityFnType returns the reflect.Type every
// security-shaped [middleware.ClientImplementation.Fn] must match for a
// channel whose payload type is tType —
// func(context.Context, *T, []route.SecurityRequirement) error — the
// SAME shape as the subscribe side (zeromq's simpler, no-grants design,
// unlike mqtt5's User-Property-driven shape).
func buildPublishSecurityFnType(tType reflect.Type) reflect.Type {
	return buildSubscribeSecurityFnType(tType)
}

// buildPubSubGeneralDecoratorFnType returns the reflect.Type every
// general-purpose wrapping Fn (subscribe- or publish-side) must match —
// func(next handlerFnType) handlerFnType, where handlerFnType is
// func(context.Context, T) error. Named distinctly from
// reqreply_transport.go's OWN [buildGeneralDecoratorFnType] (a
// different, 2-arg Req/Resp-shaped decorator for the REQUEST/REPLY
// pattern, unrelated to pub/sub's single-T handler shape) to avoid a
// same-package name collision.
func buildPubSubGeneralDecoratorFnType(handlerFnType reflect.Type) reflect.Type {
	return reflect.FuncOf([]reflect.Type{handlerFnType}, []reflect.Type{handlerFnType}, false)
}

// validateClientImplementationShapesReflect is the reflection-only
// mirror of [validatePublishImplementationShapes][T] — every attached
// [middleware.ClientImplementation.Fn] must match EITHER secFnType or
// generalFnType, checked EAGERLY (once, before any network activity) —
// a malformed Fn fails loudly via [middleware.MiddlewareShapeError],
// never silently.
func validateClientImplementationShapesReflect(impls []middleware.ClientImplementation, secFnType, generalFnType reflect.Type) error {
	for _, impl := range impls {
		if impl.Fn == nil {
			continue
		}
		fnVal := reflect.ValueOf(impl.Fn)
		if fnVal.Type() == secFnType || fnVal.Type() == generalFnType {
			continue
		}
		return middleware.MiddlewareShapeError{
			Name:     impl.Name,
			Expected: fmt.Sprintf("%s or %s", secFnType, generalFnType),
			Got:      fmt.Sprintf("%T", impl.Fn),
		}
	}
	return nil
}

// wrapServerGeneralDecoratorReflect wraps fn (a reflect.Value of type
// handlerFnType — func(context.Context, T) error) with every
// general-purpose Fn found in impls (generalFnType shape), OUTERMOST-in,
// in attachment order — the reflection-only mirror of
// [wrapSubscribeGeneral][T]. Security-shaped Fns are silently skipped
// (consumed instead by [runSubscribeSecurityImplsReflect]).
func wrapServerGeneralDecoratorReflect(fn reflect.Value, impls []middleware.ServerImplementation, generalFnType reflect.Type) reflect.Value {
	for i := len(impls) - 1; i >= 0; i-- {
		fnVal := reflect.ValueOf(impls[i].Fn)
		if !fnVal.IsValid() || fnVal.Type() != generalFnType {
			continue
		}
		fn = fnVal.Call([]reflect.Value{fn})[0]
	}
	return fn
}

// wrapClientGeneralDecoratorReflect is
// [wrapServerGeneralDecoratorReflect]'s publish-side sibling — the
// reflection-only mirror of [wrapPublishGeneral][T].
func wrapClientGeneralDecoratorReflect(fn reflect.Value, impls []middleware.ClientImplementation, generalFnType reflect.Type) reflect.Value {
	for i := len(impls) - 1; i >= 0; i-- {
		fnVal := reflect.ValueOf(impls[i].Fn)
		if !fnVal.IsValid() || fnVal.Type() != generalFnType {
			continue
		}
		fn = fnVal.Call([]reflect.Value{fn})[0]
	}
	return fn
}

// runSubscribeSecurityImplsReflect is the reflection-only mirror of
// [runSubscribeSecurityImpls][T]. ctxVal/valuePtr are reflect.Value
// wrappers around (context.Context, *T) — valuePtr MUST be addressable
// (a security Fn may write into it).
func runSubscribeSecurityImplsReflect(ctxVal, valuePtr reflect.Value, secReqs []route.SecurityRequirement, impls []middleware.ServerImplementation, secFnType reflect.Type) error {
	secReqsVal := reflect.ValueOf(secReqs)
	for _, impl := range impls {
		fnVal := reflect.ValueOf(impl.Fn)
		if !fnVal.IsValid() || fnVal.Type() != secFnType {
			continue // general-purpose or nil
		}
		if len(impl.Satisfies) > 0 && len(secReqs) == 0 {
			continue
		}
		results := fnVal.Call([]reflect.Value{ctxVal, valuePtr, secReqsVal})
		if fnErr, _ := results[0].Interface().(error); fnErr != nil {
			return fnErr
		}
	}
	return nil
}

// runPublishSecurityImplsReflect is [runSubscribeSecurityImplsReflect]'s
// publish-side sibling — the reflection-only mirror of
// [runPublishSecurityImpls][T]. GATED by Satisfies vs secReqs, mirroring
// adapter.go's own gating.
func runPublishSecurityImplsReflect(ctxVal, valuePtr reflect.Value, secReqs []route.SecurityRequirement, impls []middleware.ClientImplementation, secFnType reflect.Type) error {
	reqSchemes := make(map[string]bool, len(secReqs))
	for _, req := range secReqs {
		for scheme := range req {
			reqSchemes[scheme] = true
		}
	}
	secReqsVal := reflect.ValueOf(secReqs)
	for _, impl := range impls {
		fnVal := reflect.ValueOf(impl.Fn)
		if !fnVal.IsValid() || fnVal.Type() != secFnType {
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
		results := fnVal.Call([]reflect.Value{ctxVal, valuePtr, secReqsVal})
		if fnErr, _ := results[0].Interface().(error); fnErr != nil {
			return fnErr
		}
	}
	return nil
}

// handlerOptsStructValue returns handlerOptsField's boxed struct value as
// a [reflect.Value], or (zero, false) when handlerOptsField is invalid,
// nil, or not a struct (a declared channel with no
// [Subscriber.WithOptions]/[Publisher.WithOptions] value is the common,
// valid case).
func handlerOptsStructValue(handlerOptsField reflect.Value) (reflect.Value, bool) {
	if !handlerOptsField.IsValid() || handlerOptsField.IsNil() {
		return reflect.Value{}, false
	}
	v := reflect.ValueOf(handlerOptsField.Interface())
	if v.Kind() != reflect.Struct {
		return reflect.Value{}, false
	}
	return v, true
}

// subscribeHandlerOptsFields extracts every non-T-dependent field
// [SubscribeOptions][T] carries (OnError, Capabilities — neither depends
// on T) from a type-erased [events.ChannelHandle.HandlerOpts] value.
// Returns zero values when handlerOptsField is nil/wrong-shape.
func subscribeHandlerOptsFields(handlerOptsField reflect.Value) (onError func(SubscribeError), caps []Capability) {
	v, ok := handlerOptsStructValue(handlerOptsField)
	if !ok {
		return nil, nil
	}
	onError, _ = v.FieldByName("OnError").Interface().(func(SubscribeError))
	caps, _ = v.FieldByName("Capabilities").Interface().([]Capability)
	return onError, caps
}

// publishHandlerOptsFields is [subscribeHandlerOptsFields]'s publish-side
// sibling — extracts [PublishOptions][T]'s non-T-dependent Capabilities
// field (zeromq's PublishOptions[T] has no OnError/ContentType/
// UserProperties equivalent, unlike mqtt5's).
func publishHandlerOptsFields(handlerOptsField reflect.Value) (caps []Capability) {
	v, ok := handlerOptsStructValue(handlerOptsField)
	if !ok {
		return nil
	}
	caps, _ = v.FieldByName("Capabilities").Interface().([]Capability)
	return caps
}

// resolveSecReqsReflect resolves the effective [route.SecurityRequirement]
// slice for a channel's Subscribe or Publish operation (opField is
// "Subscribe" or "Publish"), falling back to GlobalSecurity. elem is the
// [events.ChannelHandle] struct value (NOT a pointer) recovered via
// [recoverHandle].
func resolveSecReqsReflect(elem reflect.Value, opField string) []route.SecurityRequirement {
	descriptor := elem.FieldByName("Descriptor")
	opPtr := descriptor.FieldByName(opField)
	if opPtr.IsValid() && !opPtr.IsNil() {
		secField := opPtr.Elem().FieldByName("Security")
		if secField.IsValid() {
			if secReqs, _ := secField.Interface().([]route.SecurityRequirement); secReqs != nil {
				return secReqs
			}
		}
	}
	global, _ := elem.FieldByName("GlobalSecurity").Interface().([]route.SecurityRequirement)
	return global
}

// resolveFormatsOverride type-asserts overrideAny (a
// [events.ClientPublishOptions.Formats]/[events.ClientSubscribeOptions.Formats]
// value) against expectedType (the reflected handle method's variadic
// slice parameter type, e.g. []format.Format[T]) — performed via
// [reflect.Value] comparison since T is runtime-only here. A nil
// overrideAny resolves to the slice type's zero value (nil slice —
// equivalent to "no override").
func resolveFormatsOverride(overrideAny any, expectedType reflect.Type) (reflect.Value, error) {
	if overrideAny == nil {
		return reflect.Zero(expectedType), nil
	}
	v := reflect.ValueOf(overrideAny)
	if v.Type() != expectedType {
		return reflect.Value{}, fmt.Errorf("zeromq: format override: want %s, got %T", expectedType, overrideAny)
	}
	return v, nil
}
