package nethttp

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"time"

	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/internal/middleware"
	"github.com/DaniDeer/go-codex/internal/route"
	"github.com/DaniDeer/go-codex/stats"
)

// restPkgPath is api/rest's import path — used to distinguish a genuine
// rest.Route[Req,Resp]/rest.SSERoute[Req,Event] value (for ANY type
// params) from an unrelated/wrong-package value passed by caller mistake
// to [rest.Client.Call]/[rest.Client.Consume].
const restPkgPath = "github.com/DaniDeer/go-codex/api/rest"

// recoverClientRouteHandleValue reflects routeAny into a
// *rest.RouteHandle[Req,Resp] reflect.Value, accepting EITHER shape
// [rest.CallWithTransport]'s confirmed dual-mode acceptance allows
// (docs/design/d-0006-protocol-native-capabilities.md's Phase 5a,
// mirroring [reqreply]'s identical, already-shipped
// recoverRouteHandleValue helper exactly): a raw, unregistered
// rest.Route[Req,Resp] (calls its ClientHandle() method reflectively to
// derive one — the SAME derivation [clientTransport.Call] already
// performed before this helper existed) OR an already-built
// *rest.RouteHandle[Req,Resp] (used as-is — needed for
// [rest.CallWithTransport]'s own target caller: a bare-handle caller
// with no rest.Route value at all, e.g. adapters/mcprest bridging a REST
// route into a different protocol). Returns the handle's reflect.Value
// (always a pointer) and its Elem() struct value for field access.
func recoverClientRouteHandleValue(routeAny any) (reflect.Value, reflect.Value, error) {
	rv := reflect.ValueOf(routeAny)
	if !rv.IsValid() {
		return reflect.Value{}, reflect.Value{}, rest.TransportTypeMismatchError{
			Want: "rest.Route[Req, Resp] or *rest.RouteHandle[Req, Resp]", Got: fmt.Sprintf("%T", routeAny),
		}
	}
	t := rv.Type()
	switch {
	case t.PkgPath() == restPkgPath && strings.HasPrefix(t.Name(), "Route["):
		handleVal := rv.MethodByName("ClientHandle").Call(nil)[0]
		return handleVal, handleVal.Elem(), nil
	case t.Kind() == reflect.Pointer && t.Elem().PkgPath() == restPkgPath && strings.HasPrefix(t.Elem().Name(), "RouteHandle["):
		return rv, rv.Elem(), nil
	default:
		return reflect.Value{}, reflect.Value{}, rest.TransportTypeMismatchError{
			Want: "rest.Route[Req, Resp] or *rest.RouteHandle[Req, Resp]", Got: fmt.Sprintf("%T", routeAny),
		}
	}
}

// recoverClientSSERouteHandleValue is [recoverClientRouteHandleValue]'s
// SSE-stream sibling, for [clientTransport.Consume] — same dual-mode
// acceptance (raw rest.SSERoute[Req,Event] OR an already-built
// *rest.SSERouteHandle[Req,Event]), closing the identical GlobalSecurity-
// visibility gap [recoverClientRouteHandleValue] already closes for
// [clientTransport.Call] (docs/design/d-0001-rest-middleware-workflow-simplification.md's
// Addendum 6). Returns the handle's reflect.Value (always a pointer) and
// its Elem() struct value for field access.
func recoverClientSSERouteHandleValue(sseRouteAny any) (reflect.Value, reflect.Value, error) {
	rv := reflect.ValueOf(sseRouteAny)
	if !rv.IsValid() {
		return reflect.Value{}, reflect.Value{}, rest.TransportTypeMismatchError{
			Want: "rest.SSERoute[Req, Event] or *rest.SSERouteHandle[Req, Event]", Got: fmt.Sprintf("%T", sseRouteAny),
		}
	}
	t := rv.Type()
	switch {
	case t.PkgPath() == restPkgPath && strings.HasPrefix(t.Name(), "SSERoute["):
		handleVal := rv.MethodByName("ClientHandle").Call(nil)[0]
		return handleVal, handleVal.Elem(), nil
	case t.Kind() == reflect.Pointer && t.Elem().PkgPath() == restPkgPath && strings.HasPrefix(t.Elem().Name(), "SSERouteHandle["):
		return rv, rv.Elem(), nil
	default:
		return reflect.Value{}, reflect.Value{}, rest.TransportTypeMismatchError{
			Want: "rest.SSERoute[Req, Event] or *rest.SSERouteHandle[Req, Event]", Got: fmt.Sprintf("%T", sseRouteAny),
		}
	}
}

// clientTransport implements [rest.ClientTransport], wrapping an internal
// [*caller] — built by [NewClientTransport]. See
// docs/design/d-0001-rest-middleware-workflow-simplification.md's Addendum 4 for the full design
// and the reflection technique both Call and Consume rely on (Go forbids
// generic methods, so both recover the concrete Req/Resp/Event types at
// runtime via reflection against the ALREADY-CONCRETE closures/methods on
// the route's type-erased *rest.RouteHandle/*rest.SSERouteHandle — never
// via reflecting a generic FUNCTION, which Go does not support; this is
// exactly why [rest.RouteHandle.EncodeVars]/[SSERouteHandle.EncodeVars]
// exist as monomorphized METHODS rather than direct [codex.EncodeVars]
// calls).
type clientTransport struct {
	caller *caller
}

// ClientTransportOptions configures [NewClientTransport] — the SOLE
// configuration surface for a nethttp [rest.ClientTransport] (docs/design/
// d-0006-protocol-native-capabilities.md's Phase 4d: a single Options
// struct, no positional params, even for these REQUIRED fields — a
// deliberate, uniform, declarative shape across every adapter's
// `New*Transport` factory).
type ClientTransportOptions struct {
	// HTTPClient issues every outgoing request. Required.
	HTTPClient *http.Client
	// BaseURL is prepended to every route's derived path. Required.
	BaseURL string
}

// NewClientTransport returns a [rest.ClientTransport] configured per opts
// — the adapter's ONLY job in the attach workflow (docs/design/
// d-0006-protocol-native-capabilities.md's Phase 4d): construct a
// fully-configured, attachable value. Attaching it is EXCLUSIVELY
// [rest.Client.Attach]'s job — there is no adapter-namespaced Attach
// function anymore (REMOVED, breaking, per that phase's explicit
// "zero backdoor between the api layer and the adapters" directive).
//
// Call and Consume are FULL-FEATURED (see
// docs/design/d-0001-rest-middleware-workflow-simplification.md's Addendum 4): path/query/header/
// cookie param derivation, security/credential ClientMW, per-call format
// overrides ([rest.ClientCallOptions]/[rest.ClientConsumeOptions]), and
// general-purpose ClientMW wrapping are ALL supported — there is no
// remaining "v1 scope" asterisk. A caller needing a pre-built
// *rest.RouteHandle/*rest.SSERouteHandle directly (e.g. adapters/mcprest
// bridging a REST route into another protocol), or finer per-call control
// [ClientCallOptions]/[ClientConsumeOptions] don't expose (OnCredentialRejected,
// ExtraHeaders, MaxBackoff, OnError), uses [rest.CallWithTransport]/
// [CallSSEAdapter] directly instead.
//
//	client := rest.NewClient()
//	transport := nethttp.NewClientTransport(nethttp.ClientTransportOptions{HTTPClient: httpClient, BaseURL: baseURL})
//	if err := client.Attach(transport); err != nil { ... }
//	respAny, err := client.Call(ctx, getUserRoute, GetUserReq{ID: "f47ac10b"})
//	resp := respAny.(GetUserResp)
func NewClientTransport(opts ClientTransportOptions) rest.ClientTransport {
	return &clientTransport{caller: newCaller(opts.HTTPClient, opts.BaseURL)}
}

var (
	ctxType = reflect.TypeOf((*context.Context)(nil)).Elem()
	errType = reflect.TypeOf((*error)(nil)).Elem()
)

// reflectErrValue builds a reflect.Value of the `error` interface type
// holding err (or the interface's zero value when err is nil) — the
// established codebase idiom for a [reflect.MakeFunc]-constructed
// function's error return slot (mirrors adapters/nethttp/serve_sse.go's
// identical `retErr` helper).
func reflectErrValue(err error) reflect.Value {
	v := reflect.New(errType).Elem()
	if err != nil {
		v.Set(reflect.ValueOf(err))
	}
	return v
}

// callVarsMethod invokes handleVal's methodName (one of EncodeVars/
// EncodeQueryVars/EncodeHeaderVars/EncodeCookieVars) with reqVal — the
// reflection-callable path/query/header/cookie var derivation every
// non-generic caller in this file needs. See
// [rest.RouteHandle.EncodeVars]'s doc comment for why these exist as
// monomorphized methods.
func callVarsMethod(handleVal, reqVal reflect.Value, methodName string) (map[string]string, error) {
	results := handleVal.MethodByName(methodName).Call([]reflect.Value{reqVal})
	if errI, _ := results[1].Interface().(error); errI != nil {
		return nil, errI
	}
	vars, _ := results[0].Interface().(map[string]string)
	return vars, nil
}

// resolveClientSecurity extracts the plain, non-generic security-related
// fields every *rest.RouteHandle[Req,Resp]/*rest.SSERouteHandle[Req,Event]
// carries (Descriptor.Security falling back to GlobalSecurity,
// ClientImplementations, SecuritySchemes) — no reflection is needed to
// CALL [mergeCredentialHeaders]/[rest.ValidateSecurityCredentials] themselves
// (both are already non-generic), only to REACH these plain-typed struct
// fields on a type-erased handle value.
func resolveClientSecurity(elem, descriptor reflect.Value) (secReqs []route.SecurityRequirement, clientImpls []rest.ClientImplementation, secSchemes map[string]rest.SecurityScheme) {
	secReqs, _ = descriptor.FieldByName("Security").Interface().([]route.SecurityRequirement)
	if secReqs == nil {
		secReqs, _ = elem.FieldByName("GlobalSecurity").Interface().([]route.SecurityRequirement)
	}
	clientImpls, _ = elem.FieldByName("ClientImplementations").Interface().([]rest.ClientImplementation)
	secSchemes, _ = elem.FieldByName("SecuritySchemes").Interface().(map[string]rest.SecurityScheme)
	return secReqs, clientImpls, secSchemes
}

// resolveFormatsArg type-asserts overrideAny (a [rest.ClientCallOptions.RequestFormats]/
// [ResponseFormats]/[rest.ClientConsumeOptions.Formats] value) against
// expectedType (the reflected handle method's variadic slice parameter
// type, e.g. []format.Format[Req]) — the reflection-based sibling of
// [resolveCallFormat], performed via [reflect.Value] comparison since
// Req/Resp/Event are runtime-only here. A nil overrideAny resolves to the
// slice type's zero value (nil slice — equivalent to "no override,"
// letting the reflected handle method fall back to its OWN declared
// Formats internally, exactly like calling it with zero variadic args).
func resolveFormatsArg(overrideAny any, expectedType reflect.Type, direction string) (reflect.Value, error) {
	if overrideAny == nil {
		return reflect.Zero(expectedType), nil
	}
	v := reflect.ValueOf(overrideAny)
	if v.Type() != expectedType {
		return reflect.Value{}, CallFormatOptError{Direction: direction,
			Err: fmt.Errorf("want %s, got %T", expectedType, overrideAny)}
	}
	return v, nil
}

// wrapGeneralPurposeFn iterates impls in REVERSE order (outermost-in, in
// attachment order), wrapping next with every Fn whose reflect.Type
// exactly matches wrapType — mirrors [wrapCallGeneral]/[wrapSubscribeGeneral]'s
// shared contract via reflection instead of a static generic type
// assertion (Req/Resp/Event are runtime-only here). Credential-shaped Fns
// (non-empty Satisfies) are skipped — consumed elsewhere.
func wrapGeneralPurposeFn(impls []rest.ClientImplementation, wrapType reflect.Type, next reflect.Value) reflect.Value {
	for i := len(impls) - 1; i >= 0; i-- {
		impl := impls[i]
		if len(impl.Satisfies) > 0 {
			continue
		}
		fnVal := reflect.ValueOf(impl.Fn)
		if !fnVal.IsValid() || fnVal.Type() != wrapType {
			continue
		}
		next = fnVal.Call([]reflect.Value{next})[0]
	}
	return next
}

// validateClientImplementationShapesReflect is [validateCallImplementationShapes]'s
// reflection-based mirror (docs/design/
// d-0006-protocol-native-capabilities.md's Phase 5a) — checks every
// attached impl.Fn against the SAME 2 shapes THIS package recognizes
// client-side (the fixed
// credential shape, and the general-purpose wrap shape matching
// wrapType, Req/Resp-concrete at THIS call site even though this
// function itself never learns Req/Resp by name) — a nil Fn (spec-only/
// no-op implementation) is always allowed.
func validateClientImplementationShapesReflect(impls []rest.ClientImplementation, wrapType reflect.Type) error {
	credType := reflect.TypeOf(func(context.Context, []route.SecurityRequirement) (http.Header, error) { return nil, nil })
	for _, impl := range impls {
		if impl.Fn == nil {
			continue
		}
		fnType := reflect.TypeOf(impl.Fn)
		if fnType == credType || fnType == wrapType {
			continue
		}
		return rest.MiddlewareShapeError{
			Name:     impl.Name,
			Expected: "func(context.Context, []route.SecurityRequirement) (http.Header, error) or func(next func(context.Context, Req) (Resp, error)) func(context.Context, Req) (Resp, error)",
			Got:      fmt.Sprintf("%T", impl.Fn),
		}
	}
	return nil
}

// Call implements [rest.ClientTransport]. Resolves [stats.Observer] from
// ctx (this shim has no per-call Options struct to carry an explicit
// override) and calls RecordRequest on EVERY exit path — status 0 before
// a response is ever received (pre-flight/build/network failures), the
// real status code once received — mirroring [callWithVars]'s own
// convention exactly.
//
// Per docs/design/d-0001-rest-middleware-workflow-simplification.md's Addendum 4, Call performs the
// SAME steps [callWithVars] does: path/query/header/cookie param
// derivation (via [rest.RouteHandle.EncodeVars]/EncodeQueryVars/
// EncodeHeaderVars/EncodeCookieVars), security/credential ClientMW
// resolution ([mergeCredentialHeaders]/[rest.ValidateSecurityCredentials]),
// per-call format overrides (opts.RequestFormats/ResponseFormats), and
// general-purpose ClientMW wrapping — the LAST of which wraps ONLY the
// network round-trip (encode → send → decode), exactly mirroring
// [callWithVars]'s own wrap boundary; path/query/header/cookie derivation
// and credential resolution stay OUTSIDE the wrap.
func (t *clientTransport) Call(ctx context.Context, routeAny, reqAny any, optsVariadic ...rest.ClientCallOptions) (result any, err error) {
	var opts rest.ClientCallOptions
	if len(optsVariadic) > 0 {
		opts = optsVariadic[0]
	}
	obs := opts.Observer
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}
	start := time.Now()

	// docs/design/d-0007-declarative-middleware-layering.md's Rollout Phase A:
	// pre-allocate the shared ContextField box BEFORE any attached
	// ClientMW's EncodeIn/DecodeOut runs (mirrors every server adapter's
	// identical EnsureContextFields call) — a ClientMW using
	// [rest.Middleware.SetContextFieldFromIn]/[SetContextFieldFromOut]
	// needs this to be a no-op burden on the caller, not a documented
	// prerequisite. Idempotent — safe even if the caller already prepared
	// ctx themselves.
	ctx = middleware.EnsureContextFields(ctx)

	// docs/design/d-0006-protocol-native-capabilities.md's Phase 5a:
	// ferry per-field validation errors (Class B) out via ctx, then
	// drain them into obs.RecordValidationError exactly once before
	// returning — mirrors [callWithVars]'s identical deferred-drain
	// convention exactly (a confirmed, previously-missing gap in this
	// reflection dispatch found via CallWithHandle's own migrated test
	// suite).
	ctx = stats.WithDiagnostics(ctx)
	defer func() {
		for _, d := range stats.DiagnosticsFromContext(ctx) {
			obs.RecordValidationError(d.Location, d.ConstraintName, d.Field)
		}
	}()

	handleVal, elem, herr := recoverClientRouteHandleValue(routeAny)
	if herr != nil {
		obs.RecordRequest("", "", 0, time.Since(start))
		err = herr
		return nil, err
	}

	descriptor := elem.FieldByName("Descriptor")
	method := strings.ToUpper(descriptor.FieldByName("Method").String())
	path := descriptor.FieldByName("Path").String()

	encodeReqWithFormatsMethod := handleVal.MethodByName("EncodeRequestWithFormats") // func(Req, ...format.Format[Req]) ([]byte, string, error)
	reqVal := reflect.ValueOf(reqAny)
	if !reqVal.IsValid() || reqVal.Type() != encodeReqWithFormatsMethod.Type().In(0) {
		obs.RecordRequest(method, path, 0, time.Since(start))
		err = rest.TransportTypeMismatchError{
			Path: path, Want: encodeReqWithFormatsMethod.Type().In(0).String(), Got: fmt.Sprintf("%T", reqAny),
		}
		return nil, err
	}
	reqType := reqVal.Type()

	// Named return `err` lets this deferred EndSpan see whichever error (if
	// any) the function ultimately returns, mirroring [callWithVars]'s own
	// TraceObserver wiring exactly.
	if to, ok := obs.(stats.TraceObserver); ok {
		ctx = to.StartSpan(ctx, "http.request", path)
		defer func() { to.EndSpan(ctx, err) }()
	}

	// 1. Derive path/query/header/cookie vars from req via the new
	// EncodeVars-family methods (docs/design/d-0001-rest-middleware-workflow-simplification.md's Addendum 4).
	pathVars, verr := callVarsMethod(handleVal, reqVal, "EncodeVars")
	if verr == nil {
		var queryVars, headerVars, cookieVars map[string]string
		queryVars, verr = callVarsMethod(handleVal, reqVal, "EncodeQueryVars")
		if verr == nil {
			headerVars, verr = callVarsMethod(handleVal, reqVal, "EncodeHeaderVars")
		}
		if verr == nil {
			cookieVars, verr = callVarsMethod(handleVal, reqVal, "EncodeCookieVars")
		}
		if verr != nil {
			obs.RecordRequest(method, path, 0, time.Since(start))
			err = verr
			return nil, err
		}

		buildPathResults := handleVal.MethodByName("BuildPath").Call([]reflect.Value{reflect.ValueOf(pathVars)})
		if errI, _ := buildPathResults[1].Interface().(error); errI != nil {
			rest.ReportPathErrors(ctx, errI)
			obs.RecordRequest(method, path, 0, time.Since(start))
			err = errI
			return nil, err
		}
		concretePath, _ := buildPathResults[0].Interface().(string)

		// docs/design/declarative-middleware-layering.md's Rollout
		// Phase A: dispatch every attached codec-backed ClientMW's
		// EncodeIn, feeding its header/cookie/query contribution into
		// the SAME D3 precedence chain [consumeOnce] already applies —
		// a confirmed, previously-missing gap: Call never dispatched
		// handle.ClientMiddlewareHandlers at all (only the legacy,
		// general-purpose handle.ClientImplementations).
		clientMWHandlers, _ := elem.FieldByName("ClientMiddlewareHandlers").Interface().([]rest.ClientMiddlewareHandler)
		var mwContributed bool
		if len(clientMWHandlers) > 0 {
			mwHeaders, mwCookies, mwQuery, mwErr := dispatchClientMiddlewareIn(ctx, reqAny, clientMWHandlers)
			if mwErr != nil {
				stats.ReportErrors(rest.DiagnosticObserver{Ctx: ctx}, clientMiddlewareErrorLocation(mwErr), mwErr)
				obs.RecordRequest(method, path, 0, time.Since(start))
				err = mwErr
				return nil, err
			}
			mwContributed = len(mwHeaders) > 0 || len(mwCookies) > 0 || len(mwQuery) > 0
			queryVars = overrideDerived(queryVars, mwQuery)
			headerVars = overrideDerived(headerVars, mwHeaders)
			cookieVars = overrideDerived(cookieVars, mwCookies)
		}

		// docs/design/d-0006-protocol-native-capabilities.md's Phase
		// 5a: an explicit opts.QueryParams/HeaderParams/CookieParams
		// entry takes PRECEDENCE over the derived value for the same
		// key — mirrors [callWithVars]'s own D3 precedence chain
		// exactly (explicit > middleware-derived > route-own-derived).
		queryVars = overrideDerived(queryVars, opts.QueryParams)
		headerVars = overrideDerived(headerVars, opts.HeaderParams)
		cookieVars = overrideDerived(cookieVars, opts.CookieParams)

		// Validate the FINAL, fully-merged query/cookie/header vars —
		// NOT the raw opts.QueryParams/CookieParams/HeaderParams (a
		// confirmed, severe bug this review round found and fixed: the
		// previous code validated ONLY the caller's explicit overrides,
		// so a Required merge-field-derived or ClientMiddlewareHandler-
		// derived value — the FLAGSHIP "auto-derive from Req, one
		// struct, one call" convenience this package advertises — was
		// REJECTED as "required parameter missing" unless the caller
		// ALSO redundantly passed the identical value via opts, which
		// defeats the entire point of auto-derivation. Validating here,
		// AFTER both the middleware-dispatch merge and the opts-
		// override merge, checks each declared param's codec/
		// Required-ness against its TRUE final value regardless of
		// which of the three sources (Req merge field, middleware,
		// explicit opts override) produced it.
		if errI, _ := handleVal.MethodByName("ValidateQuery").Call([]reflect.Value{reflect.ValueOf(queryVars)})[0].Interface().(error); errI != nil {
			rest.ReportQueryErrors(ctx, errI)
			obs.RecordRequest(method, path, 0, time.Since(start))
			err = errI
			return nil, err
		}
		if errI, _ := handleVal.MethodByName("ValidateCookies").Call([]reflect.Value{reflect.ValueOf(cookieVars)})[0].Interface().(error); errI != nil {
			rest.ReportCookieErrors(ctx, errI)
			obs.RecordRequest(method, path, 0, time.Since(start))
			err = errI
			return nil, err
		}
		if errI, _ := handleVal.MethodByName("ValidateHeaders").Call([]reflect.Value{reflect.ValueOf(headerVars)})[0].Interface().(error); errI != nil {
			rest.ReportHeaderErrors(ctx, errI)
			obs.RecordRequest(method, path, 0, time.Since(start))
			err = errI
			return nil, err
		}

		rawURL := strings.TrimRight(t.caller.baseURL, "/") + concretePath
		if len(queryVars) > 0 {
			qv := make(url.Values, len(queryVars))
			for k, v := range queryVars {
				qv.Set(k, v)
			}
			rawURL += "?" + qv.Encode()
		}

		// 2. Security/credential resolution — runs ONCE per Call, OUTSIDE
		// any general-purpose wrap (mirrors [callWithVars] step 6).
		secReqs, clientImpls, secSchemes := resolveClientSecurity(elem, descriptor)
		var credHeaders http.Header
		var credentialFnRan bool
		if len(secReqs) > 0 {
			credHeaders, credentialFnRan, err = mergeCredentialHeaders(ctx, secReqs, clientImpls)
			if err != nil {
				obs.RecordRequest(method, path, 0, time.Since(start))
				return nil, err
			}
			// Also count a codec-backed ClientMW (securedMw.WithSend,
			// docs/design/d-0003-codec-declared-middlewares.md's Addendum 7) matching one of
			// secReqs' schemes as a credential Fn having run — confirmed
			// gap: mergeCredentialHeaders only ever sees the LEGACY
			// clientImpls list, so a 401 with ONLY a codec-backed
			// credential middleware attached never triggered
			// opts.OnCredentialRejected before this fix.
			if !credentialFnRan && clientMiddlewareSatisfiesAny(clientMWHandlers, secReqs) {
				credentialFnRan = true
			}
		}
		// credentialProduced is DELIBERATELY a separate signal from
		// credentialFnRan (which only tracks "did a credential Fn run,
		// regardless of what it returned" for opts.OnCredentialRejected
		// purposes): it gates [rest.ValidateSecurityCredentials] below,
		// and must stay false when a credential Fn deliberately ran but
		// returned (nil, nil)/empty merge fields to mean "no credential
		// needed for this call" (see
		// TestCall_CredentialFunc_ReturnsNilHeader_SkipsValidation) —
		// true only when EITHER mechanism actually PRODUCED at least
		// one header/cookie/query value.
		credentialProduced := len(credHeaders) > 0 || mwContributed

		// 3. Per-call format override resolution (opts.RequestFormats/
		// ResponseFormats) — a nil override resolves to the reflected
		// method's own zero-variadic fallback (declared Formats > JSON).
		reqFormatsVal, ferr := resolveFormatsArg(opts.RequestFormats, encodeReqWithFormatsMethod.Type().In(1), "request")
		if ferr != nil {
			obs.RecordRequest(method, path, 0, time.Since(start))
			err = ferr
			return nil, err
		}
		decodeRespMethod := handleVal.MethodByName("DecodeResponseWithFormats") // func([]byte, ...format.Format[Resp]) (Resp, error)
		respFormatsVal, ferr := resolveFormatsArg(opts.ResponseFormats, decodeRespMethod.Type().In(1), "response")
		if ferr != nil {
			obs.RecordRequest(method, path, 0, time.Since(start))
			err = ferr
			return nil, err
		}
		respType := decodeRespMethod.Type().Out(0)

		// Accept header from the resolved response format (override > declared).
		acceptContentType := "application/json"
		responseFormatResults := handleVal.MethodByName("ResponseFormat").CallSlice([]reflect.Value{respFormatsVal})
		if ok, _ := responseFormatResults[1].Interface().(bool); ok {
			if ct, _ := responseFormatResults[0].MethodByName("ContentType").Call(nil)[0].Interface().(string); ct != "" {
				acceptContentType = ct
			}
		}

		// 4. The network round trip (encode → send → decode), wrapped so
		// any attached general-purpose ClientMW Fn can compose around it
		// — mirrors [callWithVars]'s exact wrap boundary.
		nextType := reflect.FuncOf([]reflect.Type{ctxType, reqType}, []reflect.Type{respType, errType}, false)
		wrapType := reflect.FuncOf([]reflect.Type{nextType}, []reflect.Type{nextType}, false)

		// docs/design/d-0006-protocol-native-capabilities.md's Phase
		// 5a: validate every attached impl.Fn against the shapes THIS
		// package recognizes client-side, EAGERLY before any network
		// activity — mirrors [validateCallImplementationShapes]'s
		// identical, generic-side check exactly (a confirmed,
		// previously-missing gap in this reflection dispatch: a
		// malformed Fn was silently ignored by [wrapGeneralPurposeFn]
		// instead of failing loudly).
		if verr := validateClientImplementationShapesReflect(clientImpls, wrapType); verr != nil {
			obs.RecordRequest(method, path, 0, time.Since(start))
			err = verr
			return nil, err
		}

		networkStep := reflect.MakeFunc(nextType, func(args []reflect.Value) []reflect.Value {
			stepCtx, _ := args[0].Interface().(context.Context)
			stepReqVal := args[1]

			var body io.Reader
			var contentType string
			if method == http.MethodPost || method == http.MethodPut || method == http.MethodPatch {
				encodeResults := encodeReqWithFormatsMethod.CallSlice([]reflect.Value{stepReqVal, reqFormatsVal})
				if errI, _ := encodeResults[2].Interface().(error); errI != nil {
					obs.RecordRequest(method, path, 0, time.Since(start))
					return []reflect.Value{reflect.Zero(respType), reflectErrValue(fmt.Errorf("nethttp: encode request: %w", errI))}
				}
				bodyBytes, _ := encodeResults[0].Interface().([]byte)
				contentType, _ = encodeResults[1].Interface().(string)
				body = bytes.NewReader(bodyBytes)
			}

			httpReq, buildErr := http.NewRequestWithContext(stepCtx, method, rawURL, body)
			if buildErr != nil {
				obs.RecordRequest(method, path, 0, time.Since(start))
				return []reflect.Value{reflect.Zero(respType), reflectErrValue(RequestBuildError{Err: buildErr})}
			}
			if contentType != "" {
				httpReq.Header.Set("Content-Type", contentType)
			}
			httpReq.Header.Set("Accept", acceptContentType)
			for k, v := range headerVars {
				httpReq.Header.Set(k, v)
			}
			// docs/design/d-0006-protocol-native-capabilities.md's Phase
			// 5a: opts.ExtraHeaders adds arbitrary, non-codec-validated
			// headers — mirrors [callWithVars]'s identical merge.
			for k, vs := range opts.ExtraHeaders {
				for _, v := range vs {
					httpReq.Header.Add(k, v)
				}
			}
			for k, vs := range credHeaders {
				for _, v := range vs {
					httpReq.Header.Add(k, v)
				}
			}
			for k, v := range cookieVars {
				httpReq.AddCookie(&http.Cookie{Name: k, Value: v})
			}

			// Gated on credentialProduced (true when EITHER the legacy
			// http.Header-returning ClientMW OR a ClientMiddlewareHandler
			// actually PRODUCED at least one header/cookie/query value —
			// see its computation above), NOT len(credHeaders) > 0 alone
			// — a confirmed, severe bug this review round found and
			// fixed: len(credHeaders) > 0 only ever reflected the
			// LEGACY mechanism's own contribution, so a credential
			// supplied EXCLUSIVELY via the modern ClientBoundMW/
			// BoundSecurityClientMiddleware axis (dispatchClientMiddlewareIn,
			// merged into headerVars/cookieVars/queryVars, never into
			// credHeaders) never reached this check at all — an invalid
			// credential failing its OWN declared SecurityScheme codec
			// was silently sent to the server with zero client-side
			// pre-flight validation. credentialProduced correctly
			// reflects BOTH mechanisms' ACTUAL contributions, while
			// still preserving the legacy mechanism's own "deliberately
			// returns (nil, nil)/empty to mean no credential needed"
			// escape hatch (a credential Fn that RAN but produced
			// nothing stays a non-error, same as before this fix).
			if len(secReqs) > 0 && credentialProduced {
				if credErr := rest.ValidateSecurityCredentials(credentialExtractorFor(httpReq), secReqs, secSchemes); credErr != nil {
					if secObs, ok := obs.(stats.SecurityObserver); ok {
						secObs.RecordSecurityRejection(path, route.FirstSchemeName(secReqs))
					}
					obs.RecordRequest(method, path, 0, time.Since(start))
					return []reflect.Value{reflect.Zero(respType), reflectErrValue(credErr)}
				}
			}

			resp, doErr := t.caller.client.Do(httpReq)
			if doErr != nil {
				obs.RecordRequest(method, path, 0, time.Since(start))
				return []reflect.Value{reflect.Zero(respType), reflectErrValue(RequestError{Method: method, Path: path, Err: doErr})}
			}
			defer resp.Body.Close()

			statusCode := resp.StatusCode
			obs.RecordRequest(method, path, statusCode, time.Since(start))

			respBody, readErr := io.ReadAll(resp.Body)
			if readErr != nil {
				return []reflect.Value{reflect.Zero(respType), reflectErrValue(ResponseBodyError{Err: readErr})}
			}
			if statusCode < 200 || statusCode >= 300 {
				// docs/design/d-0006-protocol-native-capabilities.md's
				// Phase 5a: a 401 with an engaged credential-providing
				// implementation notifies opts.OnCredentialRejected (if
				// set) so a caching wrapper can invalidate its cached
				// credential — mirrors [callWithVars]'s identical hook,
				// orthogonal to the ErrorPattern decode below.
				if statusCode == http.StatusUnauthorized && credentialFnRan && opts.OnCredentialRejected != nil {
					opts.OnCredentialRejected()
				}
				decodeErrResults := handleVal.MethodByName("DecodeErrorFor").Call([]reflect.Value{
					reflect.ValueOf(statusCode), reflect.ValueOf(respBody),
				})
				errResp, _ := decodeErrResults[0].Interface().(rest.ErrorPatternResponse)
				matched, _ := decodeErrResults[1].Interface().(bool)
				decErrI, _ := decodeErrResults[2].Interface().(error)
				if matched && decErrI == nil {
					return []reflect.Value{reflect.Zero(respType), reflectErrValue(ErrorPatternResponse{StatusCode: errResp.Status, Value: errResp.Value, Body: errResp.Body})}
				}
				return []reflect.Value{reflect.Zero(respType), reflectErrValue(UnexpectedStatusError{Method: method, Path: path, StatusCode: statusCode, Body: respBody, Header: resp.Header})}
			}

			decodeResults := decodeRespMethod.CallSlice([]reflect.Value{reflect.ValueOf(respBody), respFormatsVal})
			if errI, _ := decodeResults[1].Interface().(error); errI != nil {
				return []reflect.Value{decodeResults[0], decodeResults[1]}
			}

			// docs/design/d-0006-protocol-native-capabilities.md's
			// Phase 5a: merge every registered response header/cookie
			// value into the SAME decoded result — mirrors
			// [callWithVars]'s identical step 13 exactly (a confirmed,
			// previously-missing gap in this reflection dispatch).
			// [rest.RouteHandle.ApplyResponseMergeFields] itself no-ops
			// when the route declares no response merge-capable params.
			respPtr := reflect.New(respType)
			respPtr.Elem().Set(decodeResults[0])
			respHeaders := make(map[string]string, len(resp.Header))
			for k := range resp.Header {
				respHeaders[k] = resp.Header.Get(k)
			}
			respHeaderNames, _ := handleVal.MethodByName("ResponseHeaderParamNames").Call(nil)[0].Interface().([]string)
			respHeaders = rest.NormalizeHeaderVars(respHeaders, respHeaderNames)
			respCookies := make(map[string]string)
			for _, c := range resp.Cookies() {
				respCookies[c.Name] = c.Value
			}
			applyResults := handleVal.MethodByName("ApplyResponseMergeFields").Call(
				[]reflect.Value{respPtr, reflect.ValueOf(respHeaders), reflect.ValueOf(respCookies)})
			if errI, _ := applyResults[0].Interface().(error); errI != nil {
				return []reflect.Value{reflect.Zero(respType), reflectErrValue(errI)}
			}

			// docs/design/declarative-middleware-layering.md's Rollout
			// Phase A: decode every attached codec-backed ClientMW's OWN
			// Out value from the SAME response headers/cookies —
			// mirrors [callWithVars]'s identical step 13b exactly (a
			// confirmed, previously-missing gap in this reflection
			// dispatch: Call never dispatched
			// handle.ClientMiddlewareHandlers's Out side at all).
			if len(clientMWHandlers) > 0 {
				if mwErr := dispatchClientMiddlewareOut(ctx, resp, clientMWHandlers); mwErr != nil {
					rest.ReportBodyErrors(ctx, mwErr)
					return []reflect.Value{reflect.Zero(respType), reflectErrValue(mwErr)}
				}
			}
			return []reflect.Value{respPtr.Elem(), decodeResults[1]}
		})

		finalStep := wrapGeneralPurposeFn(clientImpls, wrapType, networkStep)

		results := finalStep.Call([]reflect.Value{reflect.ValueOf(ctx), reqVal})
		if errI, _ := results[1].Interface().(error); errI != nil {
			err = errI
			return nil, err
		}
		return results[0].Interface(), nil
	}
	obs.RecordRequest(method, path, 0, time.Since(start))
	err = verr
	return nil, err
}

// Consume implements [rest.ClientTransport] — the SSE-stream mirror of
// [Call]. Mirrors [consumeSSE]/[consumeSSEOnce]'s reconnect-loop contract
// exactly (250ms initial backoff, doubling, capped at 30s; path/query/
// header/cookie vars AND the credential are RE-DERIVED fresh on EVERY
// reconnect attempt, never cached) — the differences from the escape
// hatch are: no [ConsumeOptions.OnError]/MaxBackoff/ExtraHeaders/
// OnCredentialRejected override (a caller needing those uses
// [nethttp.CallSSEAdapter] directly), and a general-purpose ClientMW Fn
// (shape func(next func(context.Context, Event) error)
// func(context.Context, Event) error) wraps the PER-EVENT dispatch to fn
// — mirrors [wrapSubscribeGeneral]'s identical per-message (not
// per-connection) wrap boundary, the natural SSE analogue since Consume
// has no single "Resp" the way Call does.
//
// Accepts EITHER a raw, unregistered rest.SSERoute[Req,Event] (derives a
// fresh ClientHandle() — GlobalSecurity stays invisible, the longstanding
// accepted limitation) OR an already-registered *rest.SSERouteHandle[Req,Event]
// (GlobalSecurity becomes visible to [resolveClientSecurity]'s existing
// fallback) — mirrors [Call]'s identical dual-mode acceptance via
// [recoverClientSSERouteHandleValue] (docs/design/
// d-0001-rest-middleware-workflow-simplification.md's Addendum 6).
func (t *clientTransport) Consume(ctx context.Context, sseRouteAny, reqAny, fnAny any, optsVariadic ...rest.ClientConsumeOptions) error {
	var opts rest.ClientConsumeOptions
	if len(optsVariadic) > 0 {
		opts = optsVariadic[0]
	}
	obs := stats.ObserverFromContext(ctx)

	// docs/design/d-0007-declarative-middleware-layering.md's Rollout Phase A:
	// see [clientTransport.Call]'s identical EnsureContextFields call for
	// the full rationale.
	ctx = middleware.EnsureContextFields(ctx)

	handleVal, elem, err := recoverClientSSERouteHandleValue(sseRouteAny)
	if err != nil {
		return err
	}
	descriptor := elem.FieldByName("Descriptor")
	method := http.MethodGet
	path := descriptor.FieldByName("Path").String()

	encodeVarsMethod := handleVal.MethodByName("EncodeVars")
	reqVal := reflect.ValueOf(reqAny)
	if !reqVal.IsValid() || reqVal.Type() != encodeVarsMethod.Type().In(0) {
		return rest.TransportTypeMismatchError{Path: path, Want: encodeVarsMethod.Type().In(0).String(), Got: fmt.Sprintf("%T", reqAny)}
	}

	resolveEventDecoderMethod := handleVal.MethodByName("ResolveEventDecoder") // func(string, ...format.Format[Event]) func([]byte) (Event, error)
	effectiveFormatsMethod := handleVal.MethodByName("EffectiveEventFormats")  // func(...format.Format[Event]) []format.Format[Event]
	decodeEventFuncType := resolveEventDecoderMethod.Type().Out(0)             // func([]byte) (Event, error)
	eventType := decodeEventFuncType.Out(0)

	fnVal := reflect.ValueOf(fnAny)
	wantFnType := reflect.FuncOf([]reflect.Type{ctxType, eventType}, []reflect.Type{errType}, false)
	if !fnVal.IsValid() || fnVal.Type() != wantFnType {
		return rest.TransportTypeMismatchError{Path: path, Want: wantFnType.String(), Got: fmt.Sprintf("%T", fnAny)}
	}

	formatsVal, ferr := resolveFormatsArg(opts.Formats, effectiveFormatsMethod.Type().In(0), "response")
	if ferr != nil {
		return ferr
	}

	// General-purpose ClientMW wrapping applies to the PER-EVENT dispatch
	// — mirrors [wrapSubscribeGeneral]'s identical wrap boundary (see this
	// method's own doc comment for the rationale). wrapFnType is the
	// SHAPE OF THE WRAPPER (func(next wantFnType) wantFnType), not
	// wantFnType itself.
	_, clientImpls, _ := resolveClientSecurity(elem, descriptor)
	wrapFnType := reflect.FuncOf([]reflect.Type{wantFnType}, []reflect.Type{wantFnType}, false)
	dispatchFn := wrapGeneralPurposeFn(clientImpls, wrapFnType, fnVal)

	backoff := sseInitialBackoff
	attempt := 0
	for {
		if ctx.Err() != nil {
			return nil
		}
		attempt++
		hadTraffic, connectErr := t.consumeOnce(ctx, handleVal, elem, descriptor, reqVal, path, method,
			resolveEventDecoderMethod, effectiveFormatsMethod, formatsVal, eventType, dispatchFn, obs)
		if ctx.Err() != nil {
			return nil
		}
		_ = connectErr // no OnError override in v1 — a caller needing one uses CallSSEAdapter directly.
		if hadTraffic {
			backoff = sseInitialBackoff
			attempt = 0
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
		if backoff *= 2; backoff > 30*time.Second {
			backoff = 30 * time.Second
		}
	}
}

// consumeOnce performs ONE [Consume] connection attempt — the reflection-
// based sibling of [consumeSSEOnce], re-deriving path/query/header/cookie
// vars AND the credential fresh every call (never cached across
// reconnects, per [Consume]'s own doc comment).
func (t *clientTransport) consumeOnce(
	ctx context.Context,
	handleVal, elem, descriptor, reqVal reflect.Value,
	path, method string,
	resolveEventDecoderMethod, effectiveFormatsMethod reflect.Value,
	formatsVal reflect.Value,
	eventType reflect.Type,
	dispatchFn reflect.Value,
	obs stats.Observer,
) (hadTraffic bool, connectErr error) {
	start := time.Now()

	pathVars, err := callVarsMethod(handleVal, reqVal, "EncodeVars")
	if err != nil {
		obs.RecordRequest(method, path, 0, time.Since(start))
		return false, err
	}
	queryVars, err := callVarsMethod(handleVal, reqVal, "EncodeQueryVars")
	if err != nil {
		obs.RecordRequest(method, path, 0, time.Since(start))
		return false, err
	}
	headerVars, err := callVarsMethod(handleVal, reqVal, "EncodeHeaderVars")
	if err != nil {
		obs.RecordRequest(method, path, 0, time.Since(start))
		return false, err
	}
	cookieVars, err := callVarsMethod(handleVal, reqVal, "EncodeCookieVars")
	if err != nil {
		obs.RecordRequest(method, path, 0, time.Since(start))
		return false, err
	}

	// docs/design/declarative-middleware-layering.md's Rollout Phase A:
	// dispatch every attached codec-backed ClientMW's EncodeIn, mirroring
	// [binding.go]'s consumeOnce's identical D3 precedence chain — a
	// confirmed, previously-missing gap: this reflection-based Consume
	// never dispatched handle.ClientMiddlewareHandlers at all.
	clientMWHandlers, _ := elem.FieldByName("ClientMiddlewareHandlers").Interface().([]rest.ClientMiddlewareHandler)
	var mwContributed bool
	if len(clientMWHandlers) > 0 {
		mwHeaders, mwCookies, mwQuery, mwErr := dispatchClientMiddlewareIn(ctx, reqVal.Interface(), clientMWHandlers)
		if mwErr != nil {
			stats.ReportErrors(rest.DiagnosticObserver{Ctx: ctx}, clientMiddlewareErrorLocation(mwErr), mwErr)
			obs.RecordRequest(method, path, 0, time.Since(start))
			return false, mwErr
		}
		mwContributed = len(mwHeaders) > 0 || len(mwCookies) > 0 || len(mwQuery) > 0
		queryVars = overrideDerived(queryVars, mwQuery)
		headerVars = overrideDerived(headerVars, mwHeaders)
		cookieVars = overrideDerived(cookieVars, mwCookies)
	}

	buildPathResults := handleVal.MethodByName("BuildPath").Call([]reflect.Value{reflect.ValueOf(pathVars)})
	if errI, _ := buildPathResults[1].Interface().(error); errI != nil {
		obs.RecordRequest(method, path, 0, time.Since(start))
		return false, errI
	}
	concretePath, _ := buildPathResults[0].Interface().(string)

	rawURL := strings.TrimRight(t.caller.baseURL, "/") + concretePath
	if len(queryVars) > 0 {
		qv := make(url.Values, len(queryVars))
		for k, v := range queryVars {
			qv.Set(k, v)
		}
		rawURL += "?" + qv.Encode()
	}

	secReqs, clientImpls, secSchemes := resolveClientSecurity(elem, descriptor)
	var credHeaders http.Header
	var credentialFnRan bool
	if len(secReqs) > 0 {
		credHeaders, credentialFnRan, err = mergeCredentialHeaders(ctx, secReqs, clientImpls)
		if err != nil {
			obs.RecordRequest(method, path, 0, time.Since(start))
			return false, err
		}
		if !credentialFnRan && clientMiddlewareSatisfiesAny(clientMWHandlers, secReqs) {
			credentialFnRan = true
		}
	}
	// See [clientTransport.Call]'s identical credentialProduced — a
	// SEPARATE signal from credentialFnRan, gating ValidateSecurityCredentials
	// specifically (true only when a mechanism actually PRODUCED a
	// value, preserving the legacy "(nil,nil) means no credential
	// needed" escape hatch).
	credentialProduced := len(credHeaders) > 0 || mwContributed

	httpReq, err := http.NewRequestWithContext(ctx, method, rawURL, nil)
	if err != nil {
		obs.RecordRequest(method, path, 0, time.Since(start))
		return false, RequestBuildError{Err: err}
	}

	eventFormatsResults := effectiveFormatsMethod.CallSlice([]reflect.Value{formatsVal})
	eventFormatsVal := eventFormatsResults[0]
	if eventFormatsVal.Len() > 0 {
		firstFmt := eventFormatsVal.Index(0)
		if ct, _ := firstFmt.MethodByName("ContentType").Call(nil)[0].Interface().(string); ct != "" {
			httpReq.Header.Set("Accept", ct)
		}
	}
	for k, v := range headerVars {
		httpReq.Header.Set(k, v)
	}
	for k, vs := range credHeaders {
		for _, v := range vs {
			httpReq.Header.Add(k, v)
		}
	}
	for k, v := range cookieVars {
		httpReq.AddCookie(&http.Cookie{Name: k, Value: v})
	}

	// See [clientTransport.Call]'s identical gate for the full
	// rationale — credentialProduced (not len(credHeaders) > 0) correctly
	// reflects a mechanism having ACTUALLY PRODUCED a credential value
	// via EITHER the legacy or modern ClientMiddlewareHandler mechanism.
	if len(secReqs) > 0 && credentialProduced {
		if credErr := rest.ValidateSecurityCredentials(credentialExtractorFor(httpReq), secReqs, secSchemes); credErr != nil {
			if secObs, ok := obs.(stats.SecurityObserver); ok {
				secObs.RecordSecurityRejection(path, route.FirstSchemeName(secReqs))
			}
			obs.RecordRequest(method, path, 0, time.Since(start))
			return false, credErr
		}
	}

	resp, err := t.caller.client.Do(httpReq)
	if err != nil {
		obs.RecordRequest(method, path, 0, time.Since(start))
		return false, err
	}
	defer resp.Body.Close()
	obs.RecordRequest(method, path, resp.StatusCode, time.Since(start))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return false, UnexpectedStatusError{Method: method, Path: path, StatusCode: resp.StatusCode, Body: body, Header: resp.Header}
	}

	// docs/design/declarative-middleware-layering.md's Rollout Phase A:
	// decode every attached codec-backed ClientMW's OWN Out value from
	// the connection's response headers/cookies ONCE, at connection-open
	// time — mirrors [binding.go]'s consumeOnce's identical step 2b
	// exactly (a confirmed, previously-missing gap: this reflection-based
	// Consume never dispatched the Out side at all).
	if len(clientMWHandlers) > 0 {
		if mwErr := dispatchClientMiddlewareOut(ctx, resp, clientMWHandlers); mwErr != nil {
			return false, mwErr
		}
	}

	decode := resolveEventDecoderMethod.CallSlice([]reflect.Value{reflect.ValueOf(httpReq.Header.Get("Accept")), formatsVal})[0]

	scanner := bufio.NewScanner(resp.Body)
	var dataLines []string
	dispatch := func() {
		if len(dataLines) == 0 {
			return
		}
		data := strings.Join(dataLines, "\n")
		dataLines = dataLines[:0]
		decodeResults := decode.Call([]reflect.Value{reflect.ValueOf([]byte(data))})
		if errI, _ := decodeResults[1].Interface().(error); errI != nil {
			return
		}
		fnResults := dispatchFn.Call([]reflect.Value{reflect.ValueOf(ctx), decodeResults[0]})
		if errI, _ := fnResults[0].Interface().(error); errI != nil {
			return
		}
		hadTraffic = true
	}
	for scanner.Scan() {
		if ctx.Err() != nil {
			return hadTraffic, nil
		}
		line := scanner.Text()
		if data, ok := strings.CutPrefix(line, "data: "); ok {
			dataLines = append(dataLines, data)
			continue
		}
		if line == "" {
			dispatch()
			continue
		}
	}
	dispatch()
	return hadTraffic, nil
}

var _ rest.ClientTransport = (*clientTransport)(nil)
