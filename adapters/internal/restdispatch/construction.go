package restdispatch

import (
	"context"
	"fmt"
	"net/http"
	"reflect"

	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/internal/route"
)

// ConstructedRoute bundles every field a net/http-family adapter's
// per-request dispatch body needs AFTER [ValidateRouteConstruction]
// succeeds — recovered once, by reflection, from a type-erased
// *rest.RouteHandle[Req, Resp] (handle is Req/Resp-generic; this
// package, like adapters/nethttp's/adapters/chi's own reflect-based
// dispatch, operates on it with Req/Resp erased).
type ConstructedRoute struct {
	// HV is the recovered, non-nil *rest.RouteHandle[Req, Resp] pointer
	// value (i.e. reflect.ValueOf(handle)) — callers needing to invoke a
	// pointer-receiver method (e.g. PathParamNames, DecodeMerged) use
	// HV directly; Elem is HV.Elem(), kept alongside for field access.
	HV   reflect.Value
	Elem reflect.Value

	Descriptor         route.Route
	SecSchemes         map[string]rest.SecurityScheme
	GlobalSecurity     []route.SecurityRequirement
	Impls              []rest.ServerImplementation
	MiddlewareHandlers []rest.MiddlewareHandler
	HandlerOptsAny     any
	// HandlerFn is the unwrapped, concrete func(context.Context, Req)
	// (Resp, error) value (handle.HandlerFn's `any` field, unwrapped via
	// reflect.Value.Elem()).
	HandlerFn reflect.Value
	ReqType   reflect.Type
	RespType  reflect.Type
}

// ValidateRouteConstruction recovers every field a net/http-family
// adapter's buildRouteHandler-equivalent construction-time dispatch
// needs from handle (a *rest.RouteHandle[Req, Resp] stored as any) via
// reflection, and runs the four Tier construction-time checks EAGERLY —
// shape validation, security coverage, capability coverage, and
// param-kind coverage — failing loudly (a returned error, never a
// silent pass-through) exactly once per route, at serve-construction
// time rather than letting a gap surface only at runtime.
//
// This is the shared, byte-for-byte-identical logic PREVIOUSLY
// independently duplicated in adapters/nethttp's and adapters/chi's own
// buildRouteHandler (confirmed via direct diff during Phase 4's REST
// family dispatch-core consolidation — see
// docs/roadmap/adapter-integration-architecture.md's "REST family
// dispatch-core consolidation design"). adapterName labels error
// messages and the [rest.CheckParamKindCoverage] call ("nethttp" or
// "chi") — the ONE genuine per-caller parameter this logic ever needed.
func ValidateRouteConstruction(adapterName string, handle any) (ConstructedRoute, error) {
	hv, elem, err := recoverHandleValue(adapterName, handle)
	if err != nil {
		return ConstructedRoute{}, err
	}
	descriptor, _ := elem.FieldByName("Descriptor").Interface().(route.Route)
	impls, _ := elem.FieldByName("Implementations").Interface().([]rest.ServerImplementation)
	handlerFnVal := elem.FieldByName("HandlerFn")
	if handlerFnVal.IsNil() {
		return ConstructedRoute{}, fmt.Errorf("%s.serve: route %s %s has no handler (internal error — HasHandler should have skipped it)", adapterName, descriptor.Method, descriptor.Path)
	}
	handlerFn := handlerFnVal.Elem() // unwrap the `any` field to the concrete func value
	return validateRouteConstruction(adapterName, hv, elem, handlerFn, impls)
}

// ValidateRouteConstructionForHandler is [ValidateRouteConstruction]'s
// sibling for call shapes where the handler Fn and attached
// implementations are NOT read from handle itself — confirmed (Phase 4,
// REST family step 6) to be the case for adapters/nethttp's/
// adapters/chi's own `handlerFunc` (the generic, port/stream-binding
// dispatch function): its callers (IngestAdapter/HandlerLatest/
// PipelineHandler/etc.) never call handle.WithHandler(fn), so
// handle.HandlerFn is unset, and handlerFunc's entire body reads ONLY
// its own call-time variadic `impls` parameter, never
// handle.Implementations. fn and impls are therefore supplied
// EXPLICITLY here instead of recovered from handle — every other field
// (MiddlewareHandlers, Requirements, SecuritySchemes, etc.) is still
// recovered from handle exactly as [ValidateRouteConstruction] does,
// confirmed via exhaustive grep to be read directly from handle in both
// call shapes.
func ValidateRouteConstructionForHandler(adapterName string, handle any, fn any, impls []rest.ServerImplementation) (ConstructedRoute, error) {
	hv, elem, err := recoverHandleValue(adapterName, handle)
	if err != nil {
		return ConstructedRoute{}, err
	}
	handlerFn := reflect.ValueOf(fn)
	return validateRouteConstruction(adapterName, hv, elem, handlerFn, impls)
}

// recoverHandleValue type-checks handle (expected *rest.RouteHandle[Req,
// Resp] stored as any) and returns its reflect.Value pointer/Elem pair.
func recoverHandleValue(adapterName string, handle any) (hv, elem reflect.Value, err error) {
	hv = reflect.ValueOf(handle)
	if hv.Kind() != reflect.Pointer || hv.IsNil() {
		return reflect.Value{}, reflect.Value{}, fmt.Errorf("%s.serve: expected non-nil *rest.RouteHandle[Req, Resp], got %T", adapterName, handle)
	}
	return hv, hv.Elem(), nil
}

// validateRouteConstruction is the shared core both
// [ValidateRouteConstruction] and [ValidateRouteConstructionForHandler]
// delegate to, once each has resolved handlerFn/impls from its own
// call shape — the four Tier construction-time checks (shape validation,
// security coverage, capability coverage, param-kind coverage), EAGERLY,
// failing loudly (a returned error, never a silent pass-through) exactly
// once per route, at construction time rather than letting a gap
// surface only at runtime. This is the shared, byte-for-byte-identical
// logic PREVIOUSLY independently duplicated in adapters/nethttp's and
// adapters/chi's own buildRouteHandler (confirmed via direct diff during
// Phase 4's REST family dispatch-core consolidation — see
// docs/roadmap/adapter-integration-architecture.md's "REST family
// dispatch-core consolidation design"). adapterName labels error
// messages and the [rest.CheckParamKindCoverage] call ("nethttp" or
// "chi").
func validateRouteConstruction(adapterName string, hv, elem, handlerFn reflect.Value, impls []rest.ServerImplementation) (ConstructedRoute, error) {
	descriptor, _ := elem.FieldByName("Descriptor").Interface().(route.Route)
	secSchemes, _ := elem.FieldByName("SecuritySchemes").Interface().(map[string]rest.SecurityScheme)
	globalSecurity, _ := elem.FieldByName("GlobalSecurity").Interface().([]route.SecurityRequirement)
	middlewareHandlers, _ := elem.FieldByName("MiddlewareHandlers").Interface().([]rest.MiddlewareHandler)
	handlerOptsAny := elem.FieldByName("HandlerOpts").Interface()
	reqType := handlerFn.Type().In(1)
	respType := handlerFn.Type().Out(0)

	routeLabel := descriptor.Method + " " + descriptor.Path
	if err := validateImplementationShapesReflect(routeLabel, reqType, impls); err != nil {
		return ConstructedRoute{}, err
	}

	// Coverage check: every scheme named in descriptor.Security (falling
	// back to GlobalSecurity, same resolution rule the runtime path
	// uses) must have a matching impls[i].Satisfies entry — catches a
	// route that declares a security requirement via .Use() but never
	// attaches a matching .HandleMW() implementation, EAGERLY at serve
	// time instead of letting every request fail closed at runtime with
	// no clear signal why.
	coverageReqs := descriptor.Security
	if coverageReqs == nil {
		coverageReqs = globalSecurity
	}
	if err := rest.CheckCoverage(routeLabel, coverageReqs, impls, middlewareHandlers); err != nil {
		return ConstructedRoute{}, err
	}

	// docs/design/d-0006-protocol-native-capabilities.md's Phase 3:
	// Tier 3a — a net/http-family adapter supplies NO concrete
	// Capability values at all (HTTP has no QoS/HWM concept), so a route
	// declaring RequireQoS/RequireHWM correctly, EAGERLY fails coverage
	// here rather than silently ignoring the requirement — exactly the
	// intended "adapter doesn't support this capability" outcome, not a
	// bug.
	requirements, _ := elem.FieldByName("Requirements").Interface().([]rest.CapabilityRequirement)
	if err := rest.VerifyCapabilityCoverage[rest.CapabilityName](routeLabel, requirements, nil); err != nil {
		return ConstructedRoute{}, err
	}
	// Tier 2 — HeaderParamNames/CookieParamNames/QueryParamNames already
	// reflect the FULL declared set (plain-opt AND middleware-merged),
	// scanned alongside every declared SecurityScheme's In field. This
	// check can never fail for a net/http-family adapter (HTTPTransport
	// implements all 3 capability interfaces), proving the mechanism
	// coexists with zero behavior change for existing routes.
	headerNames, _ := hv.MethodByName("HeaderParamNames").Call(nil)[0].Interface().([]string)
	cookieNames, _ := hv.MethodByName("CookieParamNames").Call(nil)[0].Interface().([]string)
	queryNames, _ := hv.MethodByName("QueryParamNames").Call(nil)[0].Interface().([]string)
	requiredKinds := rest.RequiredParamKinds(headerNames, cookieNames, queryNames, secSchemes)
	if err := rest.CheckParamKindCoverage(adapterName, requiredKinds, HTTPTransport); err != nil {
		return ConstructedRoute{}, err
	}

	return ConstructedRoute{
		HV:                 hv,
		Elem:               elem,
		Descriptor:         descriptor,
		SecSchemes:         secSchemes,
		GlobalSecurity:     globalSecurity,
		Impls:              impls,
		MiddlewareHandlers: middlewareHandlers,
		HandlerOptsAny:     handlerOptsAny,
		HandlerFn:          handlerFn,
		ReqType:            reqType,
		RespType:           respType,
	}, nil
}

// ConstructionError wraps a failed construction-time check (shape
// validation, security coverage, capability coverage, or param-kind
// coverage) for callers whose call shape has no error-return path to
// surface it through normally — currently only
// adapters/nethttp's/adapters/chi's own `handlerFunc`, whose public
// signature (used directly by IngestAdapter/HandlerLatest/
// PipelineHandler/etc.) returns only http.Handler, with no error
// alongside it, and backward compatibility is explicitly a non-goal of
// this roadmap (see docs/roadmap/adapter-integration-architecture.md's
// "Constraint: backward compatibility is not a goal"). Such a caller
// panics with this type (never a bare panic(err)) — a route that
// previously silently served with an unenforced coverage gap now fails
// loudly and immediately at construction/Activate time instead,
// mirroring buildRouteHandler's "fail loudly, never silently" principle
// via the only mechanism available at this call shape.
type ConstructionError struct {
	Adapter string
	Route   string
	Err     error
}

func (e ConstructionError) Error() string {
	return fmt.Sprintf("%s: route construction failed for %s: %v", e.Adapter, e.Route, e.Err)
}

func (e ConstructionError) Unwrap() error {
	return e.Err
}

// validateImplementationShapesReflect checks every attached
// [rest.ServerImplementation.Fn] against the two shapes
// [rest.Route.HandleMW] recognizes for a route whose request type is
// reqType — func(http.Handler) http.Handler (general-purpose) or
// func(context.Context, *http.Request, *Req) (map[string][]string, error)
// (security) — EAGERLY, before any network activity. Promoted (Phase 4,
// REST family dispatch-core consolidation) verbatim from
// adapters/nethttp's/adapters/chi's own byte-for-byte-identical function
// of the same name (confirmed via direct diff).
func validateImplementationShapesReflect(routeLabel string, reqType reflect.Type, impls []rest.ServerImplementation) error {
	generalType := reflect.TypeOf((func(http.Handler) http.Handler)(nil))
	securityType := reflect.FuncOf(
		[]reflect.Type{
			reflect.TypeOf((*context.Context)(nil)).Elem(),
			reflect.TypeOf((*http.Request)(nil)),
			reflect.PointerTo(reqType),
		},
		[]reflect.Type{
			reflect.TypeOf(map[string][]string(nil)),
			reflect.TypeOf((*error)(nil)).Elem(),
		},
		false,
	)
	for _, impl := range impls {
		if impl.Fn == nil {
			continue
		}
		fnType := reflect.TypeOf(impl.Fn)
		if fnType == generalType || fnType == securityType {
			continue
		}
		return rest.MiddlewareShapeError{
			Name:     impl.Name,
			Expected: "func(http.Handler) http.Handler or func(context.Context, *http.Request, *Req) (map[string][]string, error)",
			Got:      fmt.Sprintf("%T", impl.Fn),
		}
	}
	return nil
}
