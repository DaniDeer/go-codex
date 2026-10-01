package mqtt5

import (
	"context"
	"fmt"
	"reflect"

	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/route"
)

// This file holds the PUBLISH-side reflection-only mirrors of adapter.go's
// generic security/general-middleware dispatch helpers
// (runPublishSecurityImpls[T]/wrapPublishGeneral[T]/
// validatePublishImplementationShapes[T]) plus a few small
// HandlerOpts-extraction/security-requirement-resolution helpers shared
// by BOTH transport.go's Publish and Subscribe — needed because
// transport.go's Client.Publish/Subscribe shim recovers T only at
// runtime (via reflection against the type-erased *events.ChannelHandle),
// so it cannot call the generic versions directly.
//
// The SUBSCRIBE-side equivalents (subscribeSecurityFnType/
// generalWrapFnType/validateSubscribeImplementationShapesReflect/
// runSubscribeSecurityImplsReflect/wrapHandlerGeneralReflect/
// runErasedBuiltinSecurityCheck) ALREADY EXIST in caller.go, built for
// [(*caller).ServeSubscribers]'s own reflection-only dispatch — this file
// reuses them directly rather than duplicating (confirming, via this
// reuse, that docs/design/d-0006-protocol-native-capabilities.md's Phase
// 4e design review correctly identified this as ALREADY-established,
// proven precedent, not a new technique). generalWrapFnType is shared by
// BOTH subscribe- and publish-side general-purpose decorator dispatch
// (see its own doc comment in caller.go).
//
// Per docs/design/d-0006-protocol-native-capabilities.md's Phase 4e:
// middleware.ServerImplementation/ClientImplementation are ALREADY
// non-generic (Fn any) — impl.Fn's boxed value is ALREADY a concretely-T
// Go closure built at declare time, reachable via
// reflect.ValueOf(impl.Fn).Call(...) without this package ever needing to
// know T at compile time. This is the SAME technique
// adapters/mqtt5/reqreply_transport.go's Call and
// adapters/nethttp/clienttransport.go's Call already use for their own
// client-side security dispatch.

var (
	dispatchCtxType   = reflect.TypeOf((*context.Context)(nil)).Elem()
	dispatchErrType   = reflect.TypeOf((*error)(nil)).Elem()
	dispatchUserProps = reflect.TypeOf([]UserProperty(nil))
	dispatchSecReqs   = reflect.TypeOf([]route.SecurityRequirement(nil))
)

// buildPublishSecurityFnType returns the reflect.Type every
// security-shaped [middleware.ClientImplementation.Fn] must match for a
// channel whose payload type is tType —
// func(context.Context, *T, []route.SecurityRequirement) ([]UserProperty, error)
// — the reflection-only mirror of [runPublishSecurityImpls][T]'s fixed
// shape.
func buildPublishSecurityFnType(tType reflect.Type) reflect.Type {
	return reflect.FuncOf(
		[]reflect.Type{dispatchCtxType, reflect.PointerTo(tType), dispatchSecReqs},
		[]reflect.Type{dispatchUserProps, dispatchErrType},
		false,
	)
}

// validateClientImplementationShapesReflect is the reflection-only mirror
// of [validatePublishImplementationShapes][T] — every attached
// [middleware.ClientImplementation.Fn] must match EITHER secFnType or
// generalFnType, checked EAGERLY (once, before any network activity) — a
// malformed Fn fails loudly via [middleware.MiddlewareShapeError], never
// silently.
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

// wrapClientGeneralDecoratorReflect wraps fn (a reflect.Value of type
// handlerFnType — func(context.Context, T) error) with every
// general-purpose Fn found in impls (generalFnType shape), OUTERMOST-in,
// in attachment order — the reflection-only mirror of
// [wrapPublishGeneral][T]. Security-shaped Fns are silently skipped
// (consumed instead by [runPublishSecurityImplsReflect]).
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

// runPublishSecurityImplsReflect is the reflection-only mirror of
// [runPublishSecurityImpls][T]. ctxVal/valuePtr are reflect.Value
// wrappers around (context.Context, *T) — valuePtr MUST be addressable
// (a credential Fn may write into it for in-payload embedding).
func runPublishSecurityImplsReflect(ctxVal, valuePtr reflect.Value, secReqs []route.SecurityRequirement, impls []middleware.ClientImplementation, secFnType reflect.Type) ([]UserProperty, error) {
	reqSchemes := make(map[string]bool, len(secReqs))
	for _, req := range secReqs {
		for scheme := range req {
			reqSchemes[scheme] = true
		}
	}
	secReqsVal := reflect.ValueOf(secReqs)
	var combined []UserProperty
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
		if fnErr, _ := results[1].Interface().(error); fnErr != nil {
			return combined, fnErr
		}
		props, _ := results[0].Interface().([]UserProperty)
		combined = append(combined, props...)
	}
	return combined, nil
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
// [SubscribeOptions] carries (OnError, UserPropertyParams, Capabilities —
// SubscribeOptions itself is NOT generic, so these are plain,
// concretely-typed fields regardless of the channel's T) from a
// type-erased [events.ChannelHandle.HandlerOpts] value. Returns zero
// values when handlerOptsField is nil/wrong-shape.
func subscribeHandlerOptsFields(handlerOptsField reflect.Value) (onError func(SubscribeError), userPropertyParams []UserPropertyParam, caps []Capability) {
	v, ok := handlerOptsStructValue(handlerOptsField)
	if !ok {
		return nil, nil, nil
	}
	onError, _ = v.FieldByName("OnError").Interface().(func(SubscribeError))
	userPropertyParams, _ = v.FieldByName("UserPropertyParams").Interface().([]UserPropertyParam)
	caps, _ = v.FieldByName("Capabilities").Interface().([]Capability)
	return onError, userPropertyParams, caps
}

// publishHandlerOptsFields is [subscribeHandlerOptsFields]'s publish-side
// sibling — extracts [PublishOptions][T]'s non-T-dependent fields
// (ContentType, UserProperties, Capabilities — PublishOptions[T] has NO
// field whose TYPE depends on T, so these are reachable regardless of
// the channel's T).
func publishHandlerOptsFields(handlerOptsField reflect.Value) (contentType string, userProps []UserProperty, caps []Capability) {
	v, ok := handlerOptsStructValue(handlerOptsField)
	if !ok {
		return "", nil, nil
	}
	contentType, _ = v.FieldByName("ContentType").Interface().(string)
	userProps, _ = v.FieldByName("UserProperties").Interface().([]UserProperty)
	caps, _ = v.FieldByName("Capabilities").Interface().([]Capability)
	return contentType, userProps, caps
}

// resolveSecReqsReflect resolves the effective [route.SecurityRequirement]
// slice for a channel's Subscribe or Publish operation (opField is
// "Subscribe" or "Publish"), falling back to GlobalSecurity — the
// reflection-only mirror of adapter.go's repeated
// `if handle.Descriptor.Subscribe != nil { secReqs = ... }; if secReqs
// == nil { secReqs = handle.GlobalSecurity }` pattern. elem is the
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
// slice parameter type, e.g. []format.Format[T]) — the reflection-based
// mirror of adapters/nethttp's resolveFormatsArg, performed via
// [reflect.Value] comparison since T is runtime-only here. A nil
// overrideAny resolves to the slice type's zero value (nil slice —
// equivalent to "no override").
func resolveFormatsOverride(overrideAny any, expectedType reflect.Type) (reflect.Value, error) {
	if overrideAny == nil {
		return reflect.Zero(expectedType), nil
	}
	v := reflect.ValueOf(overrideAny)
	if v.Type() != expectedType {
		return reflect.Value{}, fmt.Errorf("mqtt5: format override: want %s, got %T", expectedType, overrideAny)
	}
	return v, nil
}
