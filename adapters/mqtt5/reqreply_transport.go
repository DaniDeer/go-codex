package mqtt5

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/stats"
	pahomqtt5 "github.com/eclipse/paho.golang/paho"
	"github.com/google/uuid"
)

// reqreplyPkgPath is api/reqreply's import path — used to distinguish a
// genuine reqreply.Route[Req,Resp]/*reqreply.RouteHandle[Req,Resp] value
// (for ANY Req/Resp) from an unrelated/wrong-package value passed by
// caller mistake to [AttachServer]/[AttachClient]'s resulting
// [reqreply.ServerTransport]/[reqreply.ClientTransport].
const reqreplyPkgPath = "github.com/DaniDeer/go-codex/api/reqreply"

// recoverRouteHandleValue reflects routeAny into a *reqreply.RouteHandle[Req,Resp]
// reflect.Value, accepting EITHER shape [reqreply.Client.Call]'s confirmed
// dual mode allows: a raw, unregistered reqreply.Route[Req,Resp] (calls
// its ClientHandle() method reflectively to derive one, mirroring
// [adapters/nethttp]'s recoverHandle) OR an already-registered
// *reqreply.RouteHandle[Req,Resp] (used as-is). Returns the handle's
// reflect.Value (always a pointer) and its Elem() struct value for field
// access.
func recoverRouteHandleValue(routeAny any) (reflect.Value, reflect.Value, error) {
	rv := reflect.ValueOf(routeAny)
	if !rv.IsValid() {
		return reflect.Value{}, reflect.Value{}, reqreply.TransportTypeMismatchError{
			Want: "reqreply.Route[Req, Resp] or *reqreply.RouteHandle[Req, Resp]", Got: fmt.Sprintf("%T", routeAny),
		}
	}
	t := rv.Type()
	switch {
	case t.PkgPath() == reqreplyPkgPath && strings.HasPrefix(t.Name(), "Route["):
		// Raw, unregistered Route — derive a *RouteHandle via ClientHandle(),
		// mirroring reqreply.Client.Call's confirmed dual-mode acceptance:
		// GlobalSecurity stays invisible here (ClientHandle sources no
		// Server), the SAME accepted limitation rest.Route.ClientHandle has.
		handleVal := rv.MethodByName("ClientHandle").Call(nil)[0]
		return handleVal, handleVal.Elem(), nil
	case t.Kind() == reflect.Ptr && t.Elem().PkgPath() == reqreplyPkgPath && strings.HasPrefix(t.Elem().Name(), "RouteHandle["):
		// Already-registered *RouteHandle — GlobalSecurity IS populated
		// (from Server.AddGlobalSecurity, via Route.Register), visible and
		// enforced below exactly as it would be for handle.Security.
		return rv, rv.Elem(), nil
	default:
		return reflect.Value{}, reflect.Value{}, reqreply.TransportTypeMismatchError{
			Want: "reqreply.Route[Req, Resp] or *reqreply.RouteHandle[Req, Resp]", Got: fmt.Sprintf("%T", routeAny),
		}
	}
}

// resolveCallFormatReflect type-asserts overrideAny (a
// [reqreply.ClientCallOptions.RequestFormats]/[reqreply.ClientCallOptions.
// ResponseFormats] value) against declaredFieldType (the reflect.Type of
// the route's own []format.Format[Req]/[]format.Format[Resp] field, e.g.
// elem.FieldByName("RequestFormats").Type()) — the reflection-only
// equivalent of [resolveCallFormat] (which can't be called here directly:
// it's generic over T, and this dispatcher never knows T at compile
// time). Returns reflect.Zero(declaredFieldType) (an empty slice of the
// right type) when overrideAny is nil — passing it to
// [reqreply.RouteHandle.EncodeRequestWithFormats]/
// [reqreply.RouteHandle.DecodeResponseWithFormats]'s variadic `formats`
// parameter then correctly falls through to THEIR OWN declared-field
// fallback (`len(formats) == 0`), so this dispatcher never needs to read
// the declared field itself for the fallback case. Returns an error on a
// type mismatch — callers wrap it in [CallError] with the
// direction-appropriate [ErrorKind], mirroring [resolveCallFormat]'s own
// error shape.
func resolveCallFormatReflect(overrideAny any, declaredFieldType reflect.Type) (reflect.Value, error) {
	if overrideAny == nil {
		return reflect.Zero(declaredFieldType), nil
	}
	v := reflect.ValueOf(overrideAny)
	if v.Type() != declaredFieldType {
		return reflect.Value{}, fmt.Errorf("format option: want %s, got %T", declaredFieldType, overrideAny)
	}
	return v, nil
}

// publishHandlerErrorReplyReflect is the reflection-based counterpart of
// [publishHandlerErrorReply] — used by [serverTransport.Serve], which has
// no concretely-typed *reqreply.RouteHandle[Req,Resp] to call the generic
// function with. observeErrorResponseForMethod is
// rv.MethodByName("ObserveErrorResponseFor") — see [serverTransport]'s
// doc comment. Mirrors [publishHandlerErrorReply]'s logic exactly:
// consults ObserveErrorResponseFor(ctx, obs, err) first (which ALSO
// reports match/miss/span-tag observability internally — the
// RECOMMENDED single call site, see docs/roadmap/
// d-0005-error-handling.md's Topic 1/5); on a match,
// publishes the declared codec-backed typed payload instead of plain
// text; on no match, or a mapping/encoding failure within the matched
// pattern, falls back to [publishErrorReply]'s plain-text behavior
// unchanged.
func publishHandlerErrorReplyReflect(
	ctx context.Context,
	client MQTTClient,
	observeErrorResponseForMethod reflect.Value,
	responseTopic string,
	correlationData []byte,
	err error,
	obs stats.Observer,
	propertyVars map[string]string,
) {
	if responseTopic == "" {
		return
	}
	results := observeErrorResponseForMethod.Call([]reflect.Value{
		reflect.ValueOf(ctx), reflect.ValueOf(&obs).Elem(), reflect.ValueOf(&err).Elem(),
	})
	resp, _ := results[0].Interface().(reqreply.ErrorPatternResponse)
	matched, _ := results[1].Interface().(bool)
	mapErr, _ := results[2].Interface().(error)
	if matched && mapErr == nil {
		props := &pahomqtt5.PublishProperties{
			ContentType:     errorReplyContentType,
			CorrelationData: correlationData,
		}
		// Case 3 write-side wiring (see
		// docs/design/d-0003-codec-declared-middlewares.md's Addendum's
		// "Write-side wiring"):
		// a Middleware's WithResponseProperty-declared value is written
		// onto the ERROR reply's User Properties too, not just the
		// success reply — a route's declared response property is a
		// property of the RESPONSE MESSAGE, regardless of whether that
		// message carries a success or error-pattern body.
		if len(propertyVars) > 0 {
			props.User = userPropertiesFromMap(propertyVars)
		}
		// NEW: transmit the matched pattern's Code as a dedicated,
		// RESERVED User Property — additive alongside any
		// WithResponseProperty-declared values above — so the CLIENT
		// can look up which pattern produced this reply via
		// reqreply.RouteHandle.DecodeErrorFor. Never set on the
		// plain-text fallback path below.
		props.User = append(props.User, UserProperty{Key: errorCodePropertyKey, Value: resp.Code})
		_, _ = client.Publish(ctx, &pahomqtt5.Publish{
			Topic:      responseTopic,
			QoS:        1,
			Payload:    resp.Body,
			Properties: props,
		})
		return
	}
	if matched && mapErr != nil {
		stats.ReportErrors(obs, "error_pattern", mapErr)
	}
	publishErrorReply(ctx, client, responseTopic, correlationData, err)
}

// tryDeadLetterReflect is the reflection-based counterpart of
// [tryDeadLetter] (the pub/sub adapters' helper) — used by
// [serverTransport.Serve], which has no concretely-typed
// *reqreply.RouteHandle[Req,Resp] to call [reqreply.RouteHandle.
// DeadLetterFor] with directly. sourceTopic is the request's OWN concrete
// topic (msg.Topic) — a route's declared dead-letter destination is keyed
// off the REQUEST side, mirroring events' dead-letter's use of the
// subscribe-side source topic (a reqreply route has no separate
// "subscribe topic" of its own). Returns true when a dead-letter was
// attempted (published), false when the route declares no dead-letter
// rule for this request.
func tryDeadLetterReflect(
	ctx context.Context, client MQTTClient, deadLetterForMethod reflect.Value,
	obs stats.Observer, sourceTopic string, rawPayload []byte, err error,
) bool {
	results := deadLetterForMethod.Call([]reflect.Value{
		reflect.ValueOf(&obs).Elem(), reflect.ValueOf(sourceTopic), reflect.ValueOf(rawPayload), reflect.ValueOf(&err).Elem(),
	})
	topic, _ := results[0].Interface().(string)
	body, _ := results[1].Interface().([]byte)
	ok, _ := results[2].Interface().(bool)
	if !ok {
		return false
	}
	_, _ = client.Publish(ctx, &pahomqtt5.Publish{
		Topic:   topic,
		QoS:     1,
		Payload: body,
	})
	return true
}

// userPropertiesFromMap converts a plain map[string]string into
// [pahomqtt5.UserProperties] — the wire shape MQTT5 User Properties
// require — used by BOTH the property axis's write-side wiring (Case 1/
// Case 3, see docs/design/d-0003-codec-declared-middlewares.md's Addendum) and
// (indirectly, via the same conversion) anywhere else a plain var map
// needs to become actual User Properties.
func userPropertiesFromMap(vars map[string]string) pahomqtt5.UserProperties {
	if len(vars) == 0 {
		return nil
	}
	out := make(pahomqtt5.UserProperties, 0, len(vars))
	for k, v := range vars {
		out = append(out, UserProperty{Key: k, Value: v})
	}
	return out
}

// propertyVarsFromUserProperties extracts an incoming MQTT5 message's
// User Properties into a plain map[string]string — the adapter-supplied
// property-value map [reqreply.MiddlewareHandler.DecodeIn]/
// [reqreply.ClientMiddlewareHandler.DecodeOut] decode the property axis
// from. Reuses the SAME wire shape [validateUserProperties] already
// reads from, kept as a SEPARATE map from topicVars throughout (never
// combined — see docs/design/d-0003-codec-declared-middlewares.md's Addendum's
// "Round 2 correction").
func propertyVarsFromUserProperties(msg *pahomqtt5.Publish) map[string]string {
	if msg == nil || msg.Properties == nil || len(msg.Properties.User) == 0 {
		return nil
	}
	out := make(map[string]string, len(msg.Properties.User))
	for _, p := range msg.Properties.User {
		out[p.Key] = p.Value
	}
	return out
}

// dispatchServerMiddlewareHandlers runs every attached
// [reqreply.MiddlewareHandler] in registration order — AFTER the paired
// security Fn, mirroring D1's dispatch order. reqPtr is the route's own
// decoded *Req (addressable) — read AND potentially enriched by each
// bound handler's fn (Transform-attached; an Agnostic/bundled handler's
// fn never sees it). Returns the accumulated reply-side topic/property
// vars every handler's EncodeOut produced (later handlers win on a name
// conflict — D6(c), "last-applied-wins"). failKind distinguishes a
// DecodeIn failure ("in", wraps as [reqreply.MiddlewareInputError]) from
// the fn's own business error ("fn", wraps as [reqreply.MiddlewareError],
// D2's fallback) from an EncodeOut failure ("out", building the REPLY's
// Out struct) — the caller reports "middleware:in"/"middleware:fn"/
// "middleware:out" accordingly (see
// docs/design/d-0003-codec-declared-middlewares.md's Addendum 2,
// Candidate-3-equivalent adapter-dispatch review, which found EncodeOut
// failures here previously collapsed into the SAME bucket as DecodeIn
// failures, both reported as "middleware:in").
func dispatchServerMiddlewareHandlers(
	ctx context.Context,
	reqPtr reflect.Value,
	handlers []reqreply.MiddlewareHandler,
	topicVars, propertyVars map[string]string,
) (outTopicVars, outPropertyVars map[string]string, name string, failKind string, err error) {
	for _, h := range handlers {
		inAny, decErr := h.DecodeIn(topicVars, propertyVars)
		if decErr != nil {
			return nil, nil, h.Name, "in", decErr
		}
		fnVal := reflect.ValueOf(h.Fn)
		var results []reflect.Value
		if h.Agnostic {
			results = fnVal.Call([]reflect.Value{reflect.ValueOf(ctx), reflect.ValueOf(inAny)})
		} else {
			results = fnVal.Call([]reflect.Value{reflect.ValueOf(ctx), reqPtr, reflect.ValueOf(inAny)})
		}
		if errI, _ := results[1].Interface().(error); errI != nil {
			return nil, nil, h.Name, "fn", reqreply.MiddlewareError{Name: h.Name, Err: errI}
		}
		outAny := results[0].Interface()
		tVars, pVars, encErr := h.EncodeOut(outAny)
		if encErr != nil {
			return nil, nil, h.Name, "out", encErr
		}
		outTopicVars = mergeVarsOverride(outTopicVars, tVars)
		outPropertyVars = mergeVarsOverride(outPropertyVars, pVars)
	}
	return outTopicVars, outPropertyVars, "", "", nil
}

// dispatchClientMiddlewareIn is [dispatchServerMiddlewareHandlers]'s
// CLIENT-side, request-encode-direction sibling — runs every attached
// [reqreply.ClientMiddlewareHandler] in registration order, producing In
// (via Fn) then encoding it into topic/property vars — accumulated with
// later handlers winning on a name conflict, mirroring the server side.
func dispatchClientMiddlewareIn(
	ctx context.Context,
	reqVal reflect.Value,
	handlers []reqreply.ClientMiddlewareHandler,
) (topicVars, propertyVars map[string]string, name string, err error) {
	for _, h := range handlers {
		fnVal := reflect.ValueOf(h.Fn)
		var results []reflect.Value
		if h.Agnostic {
			results = fnVal.Call([]reflect.Value{reflect.ValueOf(ctx)})
		} else {
			results = fnVal.Call([]reflect.Value{reflect.ValueOf(ctx), reqVal})
		}
		if errI, _ := results[1].Interface().(error); errI != nil {
			return nil, nil, h.Name, reqreply.MiddlewareError{Name: h.Name, Err: errI}
		}
		inAny := results[0].Interface()
		tVars, pVars, encErr := h.EncodeIn(inAny)
		if encErr != nil {
			return nil, nil, h.Name, encErr
		}
		topicVars = mergeVarsOverride(topicVars, tVars)
		propertyVars = mergeVarsOverride(propertyVars, pVars)
	}
	return topicVars, propertyVars, "", nil
}

// dispatchClientMiddlewareOut is [dispatchClientMiddlewareIn]'s reply-
// decode-direction sibling — mechanically decodes every attached
// [reqreply.ClientMiddlewareHandler]'s own Out value from the reply's
// actual topic/property vars, no Fn involved (mirrors
// [rest.dispatchClientMiddlewareOut]'s identical "no Fn, no reply-
// inspection Fn needed" design). Only the first decode failure is
// reported — a malformed reply fails the call; the decoded values
// themselves are not currently surfaced further (no context-accessor API
// is part of this doc's scope).
func dispatchClientMiddlewareOut(
	topicVars, propertyVars map[string]string,
	handlers []reqreply.ClientMiddlewareHandler,
) error {
	for _, h := range handlers {
		// h.DecodeOut already returns a properly-wrapped
		// reqreply.MiddlewareOutputError on failure (see
		// api/reqreply/transform.go's buildDecodeOut) — no re-wrap needed.
		if _, err := h.DecodeOut(topicVars, propertyVars); err != nil {
			return err
		}
	}
	return nil
}

// mergeVarsOverride merges src into dst, src's values WINNING on a key
// conflict — mirrors D3's real, shipped precedence rule (see
// docs/design/d-0003-codec-declared-middlewares.md's Addendum's "Value
// precedence" section: "middleware-derived ALWAYS wins over route-own-
// derived").
func mergeVarsOverride(dst, src map[string]string) map[string]string {
	if len(src) == 0 {
		return dst
	}
	out := make(map[string]string, len(dst)+len(src))
	for k, v := range dst {
		out[k] = v
	}
	for k, v := range src {
		out[k] = v
	}
	return out
}

// effectiveSecurity resolves elem's effective security requirements
// (Security falling back to GlobalSecurity) and its SecuritySchemes,
// flattened into the two maps [validateSecurityCredentials] expects —
// mirrors [resolveClientSecurity]'s reqreply analogue (REST's own
// version lives in adapters/nethttp/clienttransport.go).
func effectiveSecurity(elem reflect.Value) (reqs []route.SecurityRequirement, schemeTypes map[string]route.SecurityScheme, schemeCodecs map[string]*codex.Codec[string]) {
	reqs, _ = elem.FieldByName("Security").Interface().([]route.SecurityRequirement)
	if reqs == nil {
		reqs, _ = elem.FieldByName("GlobalSecurity").Interface().([]route.SecurityRequirement)
	}
	schemes, _ := elem.FieldByName("SecuritySchemes").Interface().(map[string]reqreply.SecurityScheme)
	schemeTypes = make(map[string]route.SecurityScheme, len(schemes))
	schemeCodecs = make(map[string]*codex.Codec[string], len(schemes))
	for name, s := range schemes {
		schemeTypes[name] = s.SecurityScheme
		schemeCodecs[name] = s.Codec
	}
	return reqs, schemeTypes, schemeCodecs
}

// ── Server side ──────────────────────────────────────────────────────────

// serverTransport implements [reqreply.ServerTransport], wrapping client+
// router+opts — built by [AttachServer]. A reflection shim (mirroring
// [adapters/nethttp]'s clientTransport/[adapters/mqtt5]'s events
// transport): Go forbids generic methods, so Serve recovers the concrete
// Req/Resp types at runtime via reflection against the type-erased
// *reqreply.RouteHandle[Req,Resp] fields (Decode/Encode are struct FIELDS
// holding func values here, not methods — reflect.Value.Call works
// identically either way).
//
// Capability parity with [Serve] (Phase 0 of
// docs/design/d-0004-reqreply-workflow-simplification.md's Addendum, SHIPPED): route-declared
// [reqreply.RouteHandle.RequestFormats]/[reqreply.RouteHandle.Formats],
// [reqreply.NewTopicParam] merge-field topic-var merging, and
// [reqreply.ErrorPattern]-typed error replies (for handler/encode
// failures) are all honored via [reqreply.RouteHandle.DecodeMergedWithFormats]/
// [reqreply.RouteHandle.EncodeWithFormats]/[reqreply.RouteHandle.ErrorResponseFor],
// reached via reflection against rv (the *RouteHandle[Req,Resp] pointer
// Value) — the same technique already used for the Decode/Encode struct
// fields, just applied to METHODS instead.
type serverTransport struct {
	client MQTTClient
	router MQTTRouter
	opts   ServeOptions
}

// AttachServer binds server+client+router (via an internal ServerTransport
// shim) as server's [reqreply.ServerTransport] — the "attach the adapter
// to the server" step behind [reqreply.Server.Serve]. opts (0 or 1 value)
// configures every route dispatched through this transport uniformly
// (Observer, UserPropertyParams, SecurityFunc, OnError) — see
// [serverTransport]'s doc comment for this shim's documented v1 scope.
//
// Returns [reqreply.ServerTransportAlreadyAttachedError] if server
// already has a transport attached.
//
//	server := reqreply.NewServer(reqreply.Info{Title: "Compute API", Version: "1.0.0"})
//	handle, _ := ComputeRoute.WithHandler(computeHandler).Register(server)
//	_ = mqtt5.AttachServer(server, client, router)
//	err := server.Serve(ctx) // dispatches ComputeRoute concurrently with every other registered route
func AttachServer(server *reqreply.Server, client MQTTClient, router MQTTRouter, opts ...ServeOptions) error {
	var o ServeOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	return server.Attach(&serverTransport{client: client, router: router, opts: o})
}

// Serve implements [reqreply.ServerTransport]. Mirrors [Serve]'s core
// logic (decode → merge-field topic vars → security enforcement → call
// fn → encode → publish reply) via reflection against routeAny/fnAny —
// see [serverTransport]'s doc comment for this shim's documented v1
// scope. Registers a router handler and subscribes, then returns nil
// immediately — NON-BLOCKING, matching [Serve]'s own existing contract
// exactly (this is the confirmed non-blocking-transport branch
// [reqreply.Server.Serve]'s concurrent dispatch relies on).
func (t *serverTransport) Serve(ctx context.Context, routeAny any, fnAny any) error {
	obs := t.opts.Observer
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}

	rv, elem, err := recoverRouteHandleValue(routeAny)
	if err != nil {
		return err
	}
	path := elem.FieldByName("Topic").String()

	decodeField := elem.FieldByName("Decode") // func([]byte) (Req, error)
	encodeField := elem.FieldByName("Encode") // func(Resp) ([]byte, error)
	reqType := decodeField.Type().Out(0)      // Req
	wantFnType := reflect.FuncOf(
		[]reflect.Type{reflect.TypeOf((*context.Context)(nil)).Elem(), reqType},
		[]reflect.Type{encodeField.Type().In(0), reflect.TypeOf((*error)(nil)).Elem()},
		false,
	)
	fnVal := reflect.ValueOf(fnAny)
	if !fnVal.IsValid() || fnVal.Type() != wantFnType {
		return reqreply.TransportTypeMismatchError{Topic: path, Want: wantFnType.String(), Got: fmt.Sprintf("%T", fnAny)}
	}

	// decodeMergedWithFormatsMethod is *RouteHandle[Req,Resp].
	// DecodeMergedWithFormats(payload []byte, topicVars map[string]string,
	// formats ...format.Format[Req]) (Req, error) — reached via rv (the
	// pointer Value recoverRouteHandleValue returns), not elem, since this
	// is a METHOD, not a field. Honors handle.RequestFormats AND merges
	// NewTopicParam topic vars in one call; behaves identically to a
	// plain Decode when the route declares neither (confirmed via its own
	// godoc) — closes Phase 0 work items 1 and half of 2 (server-side
	// RequestFormats honoring + merge-field support).
	decodeMergedWithFormatsMethod := rv.MethodByName("DecodeMergedWithFormats")
	// encodeWithFormatsMethod is *RouteHandle[Req,Resp].EncodeWithFormats(
	// resp Resp, formats ...format.Format[Resp]) ([]byte, error) — honors
	// handle.Formats, falling back to plain Encode — closes the other
	// half of Phase 0 work item 2 (server-side Formats honoring).
	encodeWithFormatsMethod := rv.MethodByName("EncodeWithFormats")
	// mergeFieldsMethod is *RouteHandle[Req,Resp].MergeFields() []codex.
	// FieldCodec[Req] — used ONLY to decide whether topic-var extraction
	// (matchTopicTemplate) is worth attempting at all, mirroring [Serve]'s
	// own "if len(mergeFields) > 0" guard exactly (avoids a spurious
	// topic-var-mismatch rejection for routes that declare no merge
	// params and whose concrete topic doesn't cleanly "match" the
	// template for unrelated reasons).
	mergeFieldsMethod := rv.MethodByName("MergeFields")
	hasMergeFields := mergeFieldsMethod.Call(nil)[0].Len() > 0
	// propertyMergeFieldsMethod is *RouteHandle[Req,Resp].
	// PropertyMergeFields() []codex.FieldCodec[Req] — non-empty when a
	// [reqreply.MergedPropertyParam] was attached DIRECTLY to [reqreply.
	// NewRoute] (no Middleware wrapper needed). Gates the property-var
	// merge step below, mirroring hasMergeFields's own gate for topic
	// vars exactly (the symmetry-bug fix — previously the ONLY way to
	// merge a property value into Req was via a Middleware[In,Out]).
	propertyMergeFieldsMethod := rv.MethodByName("PropertyMergeFields")
	hasPropertyMergeFields := propertyMergeFieldsMethod.Call(nil)[0].Len() > 0
	// observeErrorResponseForMethod is *RouteHandle[Req,Resp].
	// ObserveErrorResponseFor(ctx, obs, err) (ErrorPatternResponse, bool,
	// error) — closes Phase 0 work item 3, now the RECOMMENDED
	// observability-aware call (see docs/roadmap/
	// d-0005-error-handling.md's Topic 1/5).
	observeErrorResponseForMethod := rv.MethodByName("ObserveErrorResponseFor")
	// deadLetterForMethod is *RouteHandle[Req,Resp].DeadLetterFor(obs,
	// sourceTopic, rawPayload, err) (topic string, body []byte, ok bool)
	// — Topic 4's dead-letter fallback (docs/roadmap/
	// d-0005-error-handling.md), attempted alongside/after
	// ObserveErrorResponseFor at every Category-A failure site, mirroring
	// the pub/sub adapters' collapsed single-rule wiring exactly.
	deadLetterForMethod := rv.MethodByName("DeadLetterFor")

	// Phase 1: declarative middleware (docs/design/d-0004-reqreply-workflow-simplification.md's Addendum).
	// impls are the [reqreply.Route.HandleMW]-attached implementations —
	// validated for shape EAGERLY (once, at Serve construction time, not
	// per message) and coverage-checked against the route's declared
	// security requirements, mirroring adapters/nethttp's identical
	// build-time checks.
	impls, _ := elem.FieldByName("Implementations").Interface().([]middleware.ServerImplementation)
	if err := validateServerImplementationShapes(path, impls); err != nil {
		return err
	}
	coverageReqs, _, _ := effectiveSecurity(elem)
	if err := reqreply.CheckCoverage(path, coverageReqs, impls); err != nil {
		return err
	}

	// Phase 1b: header-param-as-middleware (docs/roadmap/reqreply-
	// middleware.md). requestHeaderParams are declared via [reqreply.
	// Route.Use]/[FromUserPropertyParam] — validated against the real
	// incoming message's User Properties, ADDITIVELY alongside the OLD
	// [ServeOptions.UserPropertyParams] escape hatch (unchanged, checked
	// separately inside baseHandler below). Reuses the SAME
	// [validateUserProperties]/[MissingUserPropertyError]/
	// [UserPropertyError] machinery the old mechanism already uses — the
	// failure MODE is identical, only the attachment surface differs.
	requestHeaderSpecs, _ := elem.FieldByName("RequestHeaderParams").Interface().([]middleware.HeaderParamSpec)
	requestHeaderParams := userPropertyParamsFromHeaderSpecs(requestHeaderSpecs)

	// docs/design/d-0003-codec-declared-middlewares.md's Addendum: codec-backed
	// Middleware[In,Out] dispatch (Transform-attached or bundled via
	// plain .Use()) — dispatched AFTER the paired security Fn, mirroring
	// D1's precedent exactly (see the dispatch call site inside
	// baseHandler below).
	middlewareHandlers, _ := elem.FieldByName("MiddlewareHandlers").Interface().([]reqreply.MiddlewareHandler)

	baseHandler := func(msg *pahomqtt5.Publish) {
		start := time.Now()
		msgCtx := context.WithValue(ctx, contextKey{}, msg)

		// TraceObserver span — mirrors the escape hatch's [Serve] exactly
		// (span name "mqtt5.serve"): a capability that was silently absent
		// from this reflection-based dispatcher before Serve/Call became
		// thin wrappers delegating here (found and closed as part of that
		// delegation, not originally part of Phase 0's 4 work items).
		var spanCtx = msgCtx
		var serveErr error
		if to, ok := obs.(stats.TraceObserver); ok {
			spanCtx = to.StartSpan(msgCtx, "mqtt5.serve", path)
		}
		defer func() {
			if to, ok := obs.(stats.TraceObserver); ok {
				to.EndSpan(spanCtx, serveErr)
			}
		}()

		var responseTopic string
		var correlationData []byte
		if msg.Properties != nil {
			responseTopic = msg.Properties.ResponseTopic
			correlationData = msg.Properties.CorrelationData
		}

		// Session-review fix (F3): User Property param validation is now
		// ErrorPattern/DeadLetter-eligible, closing an asymmetry with
		// REST's own wired header-param validation (Category A row 5) —
		// previously went straight to the plain-text publishErrorReply
		// fallback, bypassing ObserveErrorResponseFor entirely.
		if propErr := validateUserProperties(msg, t.opts.UserPropertyParams); propErr != nil {
			obs.RecordValidationError("user_property", stats.ConstraintName(propErr), userPropertyName(propErr))
			serveErr = propErr
			obs.RecordRequest("MQTT5-REP", path, 0, time.Since(start))
			publishHandlerErrorReplyReflect(spanCtx, t.client, observeErrorResponseForMethod, responseTopic, correlationData, propErr, obs, nil)
			tryDeadLetterReflect(spanCtx, t.client, deadLetterForMethod, obs, msg.Topic, msg.Payload, propErr)
			if t.opts.OnError != nil {
				t.opts.OnError(ServeError{Kind: KindSecurity, Err: propErr})
			}
			return
		}
		if propErr := validateUserProperties(msg, requestHeaderParams); propErr != nil {
			obs.RecordValidationError("user_property", stats.ConstraintName(propErr), userPropertyName(propErr))
			serveErr = propErr
			obs.RecordRequest("MQTT5-REP", path, 0, time.Since(start))
			publishHandlerErrorReplyReflect(spanCtx, t.client, observeErrorResponseForMethod, responseTopic, correlationData, propErr, obs, nil)
			tryDeadLetterReflect(spanCtx, t.client, deadLetterForMethod, obs, msg.Topic, msg.Payload, propErr)
			if t.opts.OnError != nil {
				t.opts.OnError(ServeError{Kind: KindSecurity, Err: propErr})
			}
			return
		}

		// Topic-var extraction ONLY runs when the route declares
		// NewTopicParam merge fields — mirrors [Serve]'s own "if
		// len(mergeFields) > 0" guard exactly, avoiding a spurious
		// topic-var-mismatch rejection for routes that declare no merge
		// params (matchTopicTemplate is skipped entirely for them, same
		// as before this change).
		var topicVars map[string]string
		// A Middleware's own WithRequestTopic/WithResponseTopic merge
		// fields ALSO need topicVars extracted, even when the route's
		// own Req declares no NewTopicParam merge fields itself.
		if hasMergeFields || len(middlewareHandlers) > 0 {
			var varErr error
			topicVars, varErr = matchTopicTemplate(path, msg.Topic)
			if varErr == nil {
				validateResults := rv.MethodByName("ValidateTopicVars").Call([]reflect.Value{reflect.ValueOf(topicVars)})
				if errI, _ := validateResults[0].Interface().(error); errI != nil {
					varErr = errI
				}
			}
			if varErr != nil {
				stats.ReportErrors(obs, "topic_var", varErr)
				serveErr = varErr
				obs.RecordRequest("MQTT5-REP", path, 0, time.Since(start))
				publishHandlerErrorReplyReflect(spanCtx, t.client, observeErrorResponseForMethod, responseTopic, correlationData, varErr, obs, nil)
				tryDeadLetterReflect(spanCtx, t.client, deadLetterForMethod, obs, msg.Topic, msg.Payload, varErr)
				if t.opts.OnError != nil {
					t.opts.OnError(ServeError{Kind: KindDecode, Err: varErr})
				}
				return
			}
		}

		// DecodeMergedWithFormats honors handle.RequestFormats (falling
		// back to plain Decode) AND merges topicVars in one call — closes
		// Phase 0 work items 1 and 2 (server-side).
		decodeResults := decodeMergedWithFormatsMethod.CallSlice([]reflect.Value{
			reflect.ValueOf(msg.Payload),
			reflect.ValueOf(topicVars),
			elem.FieldByName("RequestFormats"),
		})
		if errI, _ := decodeResults[1].Interface().(error); errI != nil {
			stats.ReportErrors(obs, "body", errI)
			serveErr = errI
			obs.RecordRequest("MQTT5-REP", path, 0, time.Since(start))
			publishHandlerErrorReplyReflect(spanCtx, t.client, observeErrorResponseForMethod, responseTopic, correlationData, errI, obs, nil)
			tryDeadLetterReflect(spanCtx, t.client, deadLetterForMethod, obs, msg.Topic, msg.Payload, errI)
			if t.opts.OnError != nil {
				t.opts.OnError(ServeError{Kind: KindDecode, Err: errI})
			}
			return
		}
		reqVal := decodeResults[0]

		// Route-level (Middleware-free) property merge — a
		// [reqreply.MergedPropertyParam] attached DIRECTLY to [reqreply.
		// NewRoute] merges the real incoming MQTT5 User Properties into
		// reqVal here, the SAME way topicVars was merged above via
		// DecodeMergedWithFormats — mirrors [RouteHandle.MergePropertyVars]'s
		// own godoc precedent.
		if hasPropertyMergeFields {
			reqPropVars := propertyVarsFromUserProperties(msg)
			reqPtr := reflect.New(reqType)
			reqPtr.Elem().Set(reqVal)
			mergeResults := rv.MethodByName("MergePropertyVars").Call([]reflect.Value{reqPtr, reflect.ValueOf(reqPropVars)})
			if errI, _ := mergeResults[0].Interface().(error); errI != nil {
				stats.ReportErrors(obs, "property_var", errI)
				serveErr = errI
				obs.RecordRequest("MQTT5-REP", path, 0, time.Since(start))
				publishHandlerErrorReplyReflect(spanCtx, t.client, observeErrorResponseForMethod, responseTopic, correlationData, errI, obs, nil)
				tryDeadLetterReflect(spanCtx, t.client, deadLetterForMethod, obs, msg.Topic, msg.Payload, errI)
				if t.opts.OnError != nil {
					t.opts.OnError(ServeError{Kind: KindDecode, Err: errI})
				}
				return
			}
			reqVal = reqPtr.Elem()
		}

		secReqs, schemeTypes, schemeCodecs := effectiveSecurity(elem)
		if len(secReqs) > 0 {
			var userProps pahomqtt5.UserProperties
			if msg.Properties != nil {
				userProps = msg.Properties.User
			}
			if name, credErr := validateSecurityCredentials(userProps, secReqs, schemeTypes, schemeCodecs); credErr != nil {
				if secObs, ok := obs.(stats.SecurityObserver); ok {
					secObs.RecordSecurityRejection(path, route.FirstSchemeName(secReqs))
				}
				wrapped := reqreply.SecurityCredentialError{Scheme: name, Err: credErr}
				serveErr = wrapped
				obs.RecordRequest("MQTT5-REP", path, 0, time.Since(start))
				publishErrorReply(spanCtx, t.client, responseTopic, correlationData, wrapped)
				if t.opts.OnError != nil {
					t.opts.OnError(ServeError{Kind: KindSecurity, Err: wrapped})
				}
				return
			}
		}
		// runServerSecurityMiddleware runs UNCONDITIONALLY (not gated on
		// len(secReqs)>0) — an implementation with an EMPTY Satisfies is a
		// general-purpose presence/format check that always runs,
		// mirrors adapters/nethttp's identical "runSecurityMiddlewareReflect
		// runs regardless of secReqs" structure exactly. Replaces the OLD
		// ServeOptions.SecurityFunc call (Phase 1, breaking removal).
		if err := runServerSecurityMiddleware(msgCtx, msg, impls, secReqs); err != nil {
			if secObs, ok := obs.(stats.SecurityObserver); ok {
				secObs.RecordSecurityRejection(path, route.FirstSchemeName(secReqs))
			}
			// Security middleware Fn error IS ErrorPattern-eligible now
			// (Topic 1's Category A fix) — previously bypassed
			// ErrorResponseFor entirely, always producing
			// reqreply.SecurityError.
			wrapped := reqreply.SecurityError{Err: err}
			serveErr = wrapped
			obs.RecordRequest("MQTT5-REP", path, 0, time.Since(start))
			publishHandlerErrorReplyReflect(spanCtx, t.client, observeErrorResponseForMethod, responseTopic, correlationData, wrapped, obs, nil)
			tryDeadLetterReflect(spanCtx, t.client, deadLetterForMethod, obs, msg.Topic, msg.Payload, wrapped)
			if t.opts.OnError != nil {
				t.opts.OnError(ServeError{Kind: KindSecurity, Err: wrapped})
			}
			return
		}

		// docs/design/d-0003-codec-declared-middlewares.md's Addendum: codec-backed
		// Middleware[In,Out] dispatch — runs AFTER the paired security Fn
		// above (D1), reading (Transform-bound handlers) AND potentially
		// enriching the route's own decoded *Req via a fresh, addressable
		// pointer copy — reqVal is re-read afterward to pick up any
		// mutation, mirroring zeromq's identical paired-security pattern.
		// middlewarePropertyVars accumulates every handler's own
		// WithResponseProperty-derived reply value (Case 3 write-side
		// wiring — see below).
		var middlewarePropertyVars map[string]string
		if len(middlewareHandlers) > 0 {
			reqPropVars := propertyVarsFromUserProperties(msg)
			reqPtr := reflect.New(reqType)
			reqPtr.Elem().Set(reqVal)
			_, outPropVars, mwName, failKind, mwErr := dispatchServerMiddlewareHandlers(spanCtx, reqPtr, middlewareHandlers, topicVars, reqPropVars)
			if mwErr != nil {
				kind := KindDecode
				loc := "middleware:in"
				switch failKind {
				case "fn":
					kind = KindMiddleware
					loc = "middleware:fn"
				case "out":
					kind = KindEncode
					loc = "middleware:out"
				}
				stats.ReportErrors(obs, loc, mwErr)
				serveErr = mwErr
				obs.RecordRequest("MQTT5-REP", path, 0, time.Since(start))
				// Middleware DecodeIn/Fn/EncodeOut errors are ALL
				// ErrorPattern-eligible now (Topic 1's Category A fix) —
				// previously only the Fn case consulted ErrorResponseFor.
				publishHandlerErrorReplyReflect(spanCtx, t.client, observeErrorResponseForMethod, responseTopic, correlationData, mwErr, obs, nil)
				tryDeadLetterReflect(spanCtx, t.client, deadLetterForMethod, obs, msg.Topic, msg.Payload, mwErr)
				if t.opts.OnError != nil {
					t.opts.OnError(ServeError{Kind: kind, Err: mwErr})
				}
				_ = mwName
				return
			}
			reqVal = reqPtr.Elem()
			middlewarePropertyVars = outPropVars
		}

		fnResults := fnVal.Call([]reflect.Value{reflect.ValueOf(spanCtx), reqVal})
		if errI, _ := fnResults[1].Interface().(error); errI != nil {
			serveErr = errI
			obs.RecordRequest("MQTT5-REP", path, 0, time.Since(start))
			publishHandlerErrorReplyReflect(spanCtx, t.client, observeErrorResponseForMethod, responseTopic, correlationData, errI, obs, middlewarePropertyVars)
			tryDeadLetterReflect(spanCtx, t.client, deadLetterForMethod, obs, msg.Topic, msg.Payload, errI)
			if t.opts.OnError != nil {
				t.opts.OnError(ServeError{Kind: KindHandler, Err: errI})
			}
			return
		}
		respVal := fnResults[0]

		// EncodeWithFormats honors handle.Formats, falling back to plain
		// Encode — closes the response-direction half of Phase 0 work
		// item 2 (server-side).
		encodeResults := encodeWithFormatsMethod.CallSlice([]reflect.Value{respVal, elem.FieldByName("Formats")})
		if errI, _ := encodeResults[1].Interface().(error); errI != nil {
			serveErr = errI
			obs.RecordRequest("MQTT5-REP", path, 0, time.Since(start))
			publishHandlerErrorReplyReflect(spanCtx, t.client, observeErrorResponseForMethod, responseTopic, correlationData, errI, obs, middlewarePropertyVars)
			tryDeadLetterReflect(spanCtx, t.client, deadLetterForMethod, obs, msg.Topic, msg.Payload, errI)
			if t.opts.OnError != nil {
				t.opts.OnError(ServeError{Kind: KindEncode, Err: errI})
			}
			return
		}
		respPayload, _ := encodeResults[0].Interface().([]byte)

		if responseTopic != "" {
			replyProps := &pahomqtt5.PublishProperties{}
			if correlationData != nil {
				replyProps.CorrelationData = correlationData
			}
			// Case 3 write-side wiring (see
			// docs/design/d-0003-codec-declared-middlewares.md's Addendum's
			// "Write-side
			// wiring") — a Middleware's WithResponseProperty-declared
			// value is written onto the SUCCESS reply's User Properties.
			// BRAND NEW capability: no existing mechanism wrote outgoing
			// reply User Properties before this.
			if len(middlewarePropertyVars) > 0 {
				replyProps.User = userPropertiesFromMap(middlewarePropertyVars)
			}
			if _, pubErr := t.client.Publish(spanCtx, &pahomqtt5.Publish{
				Topic:      responseTopic,
				QoS:        1,
				Payload:    respPayload,
				Properties: replyProps,
			}); pubErr != nil {
				serveErr = pubErr
				obs.RecordRequest("MQTT5-REP", path, 0, time.Since(start))
				// Session-review fix (G1): a broker-level rejection of
				// the SUCCESSFULLY-encoded reply is ALSO dead-letterable
				// — mirrors events' pub/sub publish side's own
				// broker-rejection handling (Topic 4's explicit design
				// decision), previously missing here even though every
				// OTHER Category-A failure point in this dispatch
				// already consults DeadLetterFor.
				tryDeadLetterReflect(spanCtx, t.client, deadLetterForMethod, obs, msg.Topic, msg.Payload, pubErr)
				if t.opts.OnError != nil {
					t.opts.OnError(ServeError{Kind: KindEncode, Err: pubErr})
				}
				return
			}
		}
		obs.RecordRequest("MQTT5-REP", path, 200, time.Since(start))
	}

	// applyGeneralServerMiddleware wraps baseHandler with every
	// general-purpose (unpaired) HandleMW implementation, OUTERMOST-in —
	// mirrors adapters/nethttp's applyGeneralMiddleware exactly.
	t.router.RegisterHandler(path, applyGeneralServerMiddleware(baseHandler, impls))

	if _, err := t.client.Subscribe(ctx, &pahomqtt5.Subscribe{
		Subscriptions: []pahomqtt5.SubscribeOptions{{Topic: path, QoS: 1}},
	}); err != nil {
		t.router.UnregisterHandler(path)
		return BrokerError{Op: "subscribe", Err: err}
	}
	return nil
}

var _ reqreply.ServerTransport = (*serverTransport)(nil)

// ── Client side ──────────────────────────────────────────────────────────

// clientTransport implements [reqreply.ClientTransport], wrapping client+
// router+opts — built by [AttachClient]. Capability parity with [Call]/
// [CallHandle] (Phase 0 of docs/design/d-0004-reqreply-workflow-simplification.md's Addendum, SHIPPED):
// route-declared [reqreply.RouteHandle.RequestFormats]/
// [reqreply.RouteHandle.Formats], a per-call [reqreply.ClientCallOptions]
// format override, AND [reqreply.NewTopicParam] merge-field topic-var
// derivation (mirroring [CallHandle]'s auto-derive-from-req convenience)
// are all honored — see [serverTransport]'s doc comment for the shared
// reflection technique.
type clientTransport struct {
	client MQTTClient
	router MQTTRouter
	opts   CallOptions
}

// AttachClient binds client+mqttClient+router (via an internal
// ClientTransport shim) as client's [reqreply.ClientTransport] — the
// "attach the adapter to the client" step behind [reqreply.Client.Call]/
// [reqreply.Client.CallAsync]. opts (0 or 1 value) configures every call
// dispatched through this transport uniformly (Timeout, QoS,
// CredentialFunc, ReplyTopicPrefix/Builder).
//
// Returns [reqreply.ClientTransportAlreadyAttachedError] if client
// already has a transport attached.
//
//	client := reqreply.NewClient()
//	_ = mqtt5.AttachClient(client, mqttClient, router)
//	respAny, err := client.Call(ctx, ComputeRoute, ComputeReq{X: 1, Y: 2})
func AttachClient(client *reqreply.Client, mqttClient MQTTClient, router MQTTRouter, opts ...CallOptions) error {
	var o CallOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	return client.Attach(&clientTransport{client: mqttClient, router: router, opts: o})
}

// Call implements [reqreply.ClientTransport]. Mirrors [Call]'s core logic
// (resolve reply topic → encode request → security credential resolution
// → publish → await reply → decode) via reflection against
// routeAny/reqAny — see [clientTransport]'s doc comment.
func (t *clientTransport) Call(ctx context.Context, routeAny any, reqAny any, opts ...reqreply.ClientCallOptions) (any, error) {
	var o reqreply.ClientCallOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	return t.call(ctx, routeAny, reqAny, o)
}

// call's named return values (result, err) let the deferred TraceObserver
// span-end below observe the eventual error from EVERY return statement in
// this function without needing to touch each one individually — Go
// assigns to named return variables on any `return`, explicit or bare,
// mirroring [Call]'s own `var callErr error` + assign-before-every-return
// pattern with less repetition.
func (t *clientTransport) call(ctx context.Context, routeAny any, reqAny any, callOpts reqreply.ClientCallOptions) (result any, err error) {
	obs := t.opts.Observer
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}
	start := time.Now()

	rv, elem, err := recoverRouteHandleValue(routeAny)
	if err != nil {
		return nil, err
	}
	path := elem.FieldByName("Topic").String()

	// TraceObserver span — mirrors the escape hatch's [Call] exactly (span
	// name "mqtt5.request"): a capability that was silently absent from
	// this reflection-based dispatcher before Serve/Call became thin
	// wrappers delegating here (found and closed as part of that
	// delegation, not originally part of Phase 0's 4 work items).
	if to, ok := obs.(stats.TraceObserver); ok {
		ctx = to.StartSpan(ctx, "mqtt5.request", path)
		defer func() { to.EndSpan(ctx, err) }()
	}

	encodeRequestField := elem.FieldByName("EncodeRequest") // func(Req) ([]byte, error)
	reqType := encodeRequestField.Type().In(0)

	reqVal := reflect.ValueOf(reqAny)
	if !reqVal.IsValid() || reqVal.Type() != reqType {
		obs.RecordRequest("MQTT5-REQ", path, 0, time.Since(start))
		return nil, reqreply.TransportTypeMismatchError{Topic: path, Want: reqType.String(), Got: fmt.Sprintf("%T", reqAny)}
	}

	// Resolve per-call format overrides, in priority order:
	// callOpts.RequestFormats/ResponseFormats (the NEW, Attach-based
	// reqreply.ClientCallOptions parameter, Phase 0) > t.opts.
	// RequestFormats/ResponseFormats (the mqtt5.CallOptions fields the
	// escape hatch's Call/CallHandle have always supported — a fresh
	// clientTransport is built per Call invocation via Call[Req,Resp]'s
	// thin-wrapper form, so this IS effectively per-call too) > nil
	// (meaning "use the route's own declared RequestFormats/Formats, or
	// plain EncodeRequest/DecodeResponse") — closes Phase 0 work item 2
	// (client-side per-call override half) WITHOUT dropping the
	// pre-existing t.opts.RequestFormats/ResponseFormats behavior.
	requestOverrideAny := callOpts.RequestFormats
	if requestOverrideAny == nil {
		requestOverrideAny = t.opts.RequestFormats
	}
	requestFormatsOverride, fmtErr := resolveCallFormatReflect(requestOverrideAny, elem.FieldByName("RequestFormats").Type())
	if fmtErr != nil {
		obs.RecordRequest("MQTT5-REQ", path, 0, time.Since(start))
		return nil, CallError{Kind: KindEncode, Err: fmtErr}
	}
	responseOverrideAny := callOpts.ResponseFormats
	if responseOverrideAny == nil {
		responseOverrideAny = t.opts.ResponseFormats
	}
	responseFormatsOverride, fmtErr := resolveCallFormatReflect(responseOverrideAny, elem.FieldByName("Formats").Type())
	if fmtErr != nil {
		obs.RecordRequest("MQTT5-REQ", path, 0, time.Since(start))
		return nil, CallError{Kind: KindDecode, Err: fmtErr}
	}

	// Topic-var derivation — closes Phase 0 work item 4 (client-side
	// merge-field parity): when the route declares NewTopicParam merge
	// fields, derive vars from req via RouteHandle.EncodeVars (the
	// reflection-callable wrapper around codex.EncodeVars). t.opts.Vars
	// (the SAME CallOptions.Vars field the escape hatch's CallHandle/Call
	// already expose) takes PRECEDENCE over the derived value for the
	// same key — mirrors CallHandle's exact merge precedence. Either a
	// non-empty derived map OR a non-nil t.opts.Vars triggers a
	// RouteHandle.BuildTopic rebuild (mirrors Call's own "opts.Vars !=
	// nil" check, generalized to ALSO cover the derived-only case
	// CallHandle adds on top of it).
	var vars map[string]string
	hasMergeFields := rv.MethodByName("MergeFields").Call(nil)[0].Len() > 0
	if hasMergeFields {
		encodeVarsResults := rv.MethodByName("EncodeVars").Call([]reflect.Value{reqVal})
		if errI, _ := encodeVarsResults[1].Interface().(error); errI != nil {
			obs.RecordRequest("MQTT5-REQ", path, 0, time.Since(start))
			return nil, CallError{Kind: KindEncode, Err: errI}
		}
		vars, _ = encodeVarsResults[0].Interface().(map[string]string)
	}

	// docs/design/d-0003-codec-declared-middlewares.md's Addendum: codec-backed
	// ClientMiddlewareHandler dispatch (ClientTransform-attached or
	// bundled via plain .Use()) — produces In (via Fn), encoded into
	// topic/property vars. Middleware-derived values ALWAYS override
	// route-own-derived ones for the SAME name (D3 mirror, "Value
	// precedence") — sits BETWEEN route-own-derived (vars, above) and
	// explicit t.opts.Vars (below), mirroring REST's real 3-tier
	// precedence (explicit > middleware-derived > route-own-derived).
	clientMiddlewareHandlers, _ := elem.FieldByName("ClientMiddlewareHandlers").Interface().([]reqreply.ClientMiddlewareHandler)
	// Route-level (Middleware-free) property derivation — a [reqreply.
	// MergedPropertyParam] attached DIRECTLY to [reqreply.NewRoute]
	// derives its value FROM req here, the SAME way vars (topic vars)
	// was derived above via RouteHandle.EncodeVars — mirrors
	// [RouteHandle.EncodePropertyVars]'s own godoc precedent. A
	// ClientMiddlewareHandler's own WithRequestProperty-derived value
	// (below) then OVERRIDES this route-own-derived value on a key
	// collision, same precedence direction topic vars use.
	var middlewarePropertyVarsOut map[string]string
	hasPropertyMergeFields := rv.MethodByName("PropertyMergeFields").Call(nil)[0].Len() > 0
	if hasPropertyMergeFields {
		encodePropResults := rv.MethodByName("EncodePropertyVars").Call([]reflect.Value{reqVal})
		if errI, _ := encodePropResults[1].Interface().(error); errI != nil {
			obs.RecordRequest("MQTT5-REQ", path, 0, time.Since(start))
			return nil, CallError{Kind: KindEncode, Err: errI}
		}
		middlewarePropertyVarsOut, _ = encodePropResults[0].Interface().(map[string]string)
	}
	if len(clientMiddlewareHandlers) > 0 {
		mwTopicVars, mwPropertyVars, mwName, mwErr := dispatchClientMiddlewareIn(ctx, reqVal, clientMiddlewareHandlers)
		if mwErr != nil {
			_, isFnErr := mwErr.(reqreply.MiddlewareError)
			kind := KindEncode
			loc := "middleware:in"
			if isFnErr {
				kind = KindMiddleware
				loc = "middleware:fn"
			}
			stats.ReportErrors(obs, loc, mwErr)
			obs.RecordRequest("MQTT5-REQ", path, 0, time.Since(start))
			_ = mwName
			return nil, CallError{Kind: kind, Err: mwErr}
		}
		vars = mergeVarsOverride(vars, mwTopicVars)
		middlewarePropertyVarsOut = mergeVarsOverride(middlewarePropertyVarsOut, mwPropertyVars)
	}
	// NOTE: t.opts.Vars != nil (not len(...) > 0) — an explicit, even
	// EMPTY, Vars map must still trigger BuildTopic below, so a route
	// with an unresolved template var (e.g. "{tenantID}") surfaces
	// MissingRouteParamError instead of silently publishing the raw
	// template string as a topic. Mirrors the escape hatch's own
	// "opts.Vars != nil" check exactly (reqreply.go's original Call).
	if t.opts.Vars != nil {
		merged := make(map[string]string, len(vars)+len(t.opts.Vars))
		for k, v := range vars {
			merged[k] = v
		}
		for k, v := range t.opts.Vars {
			merged[k] = v
		}
		vars = merged
	}
	if hasMergeFields || t.opts.Vars != nil {
		buildTopicResults := rv.MethodByName("BuildTopic").Call([]reflect.Value{reflect.ValueOf(vars)})
		if errI, _ := buildTopicResults[1].Interface().(error); errI != nil {
			stats.ReportErrors(obs, "topic_var", errI)
			obs.RecordRequest("MQTT5-REQ", path, 0, time.Since(start))
			return nil, CallError{Kind: KindEncode, Err: errI}
		}
		path = buildTopicResults[0].String()
	}

	timeout := t.opts.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	qos := t.opts.QoS
	if qos == 0 {
		qos = 1
	}

	var replyTopic, subscribeFilter string
	if t.opts.ReplyTopicBuilder != nil {
		replyTopic, subscribeFilter = t.opts.ReplyTopicBuilder()
		if replyTopic == "" {
			obs.RecordRequest("MQTT5-REQ", path, 0, time.Since(start))
			return nil, CallError{Kind: KindEncode, Err: fmt.Errorf("reply topic builder returned empty response topic")}
		}
		if subscribeFilter == "" {
			subscribeFilter = replyTopic
		}
	} else {
		prefix := t.opts.ReplyTopicPrefix
		if prefix == "" {
			prefix = "replies"
		}
		replyTopic = prefix + "/" + uuid.New().String()
		subscribeFilter = replyTopic
	}

	corrID := uuid.New()
	corrData := corrID[:]
	replyCh := make(chan *pahomqtt5.Publish, 1)

	t.router.RegisterHandler(replyTopic, func(msg *pahomqtt5.Publish) {
		if msg.Properties == nil || string(msg.Properties.CorrelationData) != string(corrData) {
			return
		}
		select {
		case replyCh <- msg:
		default:
		}
	})

	if _, err := t.client.Subscribe(ctx, &pahomqtt5.Subscribe{
		Subscriptions: []pahomqtt5.SubscribeOptions{{Topic: subscribeFilter, QoS: qos}},
	}); err != nil {
		t.router.UnregisterHandler(replyTopic)
		obs.RecordRequest("MQTT5-REQ", path, 0, time.Since(start))
		return nil, CallError{Kind: KindEncode, Err: fmt.Errorf("subscribe reply topic: %w", err)}
	}
	var once sync.Once
	cleanup := func() {
		once.Do(func() {
			t.router.UnregisterHandler(replyTopic)
			_, _ = t.client.Unsubscribe(ctx, &pahomqtt5.Unsubscribe{Topics: []string{subscribeFilter}})
		})
	}
	defer cleanup()

	// Phase 1: declarative middleware (docs/design/d-0004-reqreply-workflow-simplification.md's Addendum).
	// clientImpls are the [reqreply.Route.ClientMW]-attached
	// implementations — validated for shape EAGERLY, mirroring
	// adapters/nethttp's validateCallImplementationShapes. respType/
	// wantGeneralFnType let this reflection-only dispatcher recognize the
	// SAME general-purpose decorator shape
	// (func(next func(ctx,Req)(Resp,error)) func(ctx,Req)(Resp,error))
	// used elsewhere in the codebase, without knowing Req/Resp at compile
	// time.
	clientImpls, _ := elem.FieldByName("ClientImplementations").Interface().([]middleware.ClientImplementation)
	respType := elem.FieldByName("DecodeResponse").Type().Out(0)
	wantGeneralFnType := reflect.FuncOf(
		[]reflect.Type{reflect.TypeOf((*context.Context)(nil)).Elem(), reqType},
		[]reflect.Type{respType, reflect.TypeOf((*error)(nil)).Elem()},
		false,
	)
	// wantGeneralDecoratorFnType is the DECORATOR shape ClientMW's
	// general-purpose Fn actually has — func(next func(ctx,Req)(Resp,error))
	// func(ctx,Req)(Resp,error) — distinct from wantGeneralFnType (the
	// INNER shape the decorator wraps/produces).
	wantGeneralDecoratorFnType := reflect.FuncOf([]reflect.Type{wantGeneralFnType}, []reflect.Type{wantGeneralFnType}, false)
	if err := validateClientImplementationShapes(clientImpls, wantGeneralDecoratorFnType); err != nil {
		obs.RecordRequest("MQTT5-REQ", path, 0, time.Since(start))
		return nil, err
	}

	// Phase 1b: header-param-as-middleware (docs/roadmap/reqreply-
	// middleware.md) — reply/response-side. responseHeaderParams are
	// declared via [reqreply.Route.Use]/[FromResponseUserPropertyParam],
	// validated against the reply message's User Properties inside
	// innerCall below, reusing the SAME [validateUserProperties]
	// machinery the request side (and the OLD escape hatch) already use.
	responseHeaderSpecs, _ := elem.FieldByName("ResponseHeaderParams").Interface().([]middleware.ResponseHeaderParamSpec)
	responseHeaderParams := userPropertyParamsFromResponseHeaderSpecs(responseHeaderSpecs)

	// innerCall is the "encode → security/credential → publish → await
	// reply → decode" sequence, wrapped via [reflect.MakeFunc] into a
	// concretely-typed func(context.Context, Req) (Resp, error) value so
	// every attached general-purpose ClientMW decorator (itself a real,
	// concretely-typed Go closure) can wrap it — mirrors
	// adapters/nethttp's wrapCallGeneral, adapted to this
	// reflection-based dispatcher's type-erased Req/Resp.
	innerCall := reflect.MakeFunc(wantGeneralFnType, func(args []reflect.Value) []reflect.Value {
		// ctx is read from args[0], NOT the outer captured ctx variable
		// — a general-purpose ClientMW decorator wrapping this closure
		// may call next(modifiedCtx, req) with a context it mutated
		// (added a value, deadline, span, etc.); shadowing the outer
		// name here means every subsequent use of ctx in this closure
		// (mergeCredentialUserProperties, Publish, ctx.Done/Err) sees
		// that decorator's context, not the original one captured
		// before any decorator ran. A prior revision used the outer
		// ctx here, silently discarding any such decorator mutation —
		// found via a later review pass, fixed here.
		ctx := args[0].Interface().(context.Context)
		innerReqVal := args[1]
		zeroResp := reflect.Zero(respType)
		errType := reflect.TypeOf((*error)(nil)).Elem()

		// EncodeRequestWithFormats honors the per-call override (falling
		// back to route-declared RequestFormats, then plain
		// EncodeRequest) — closes Phase 0 work item 2 (client-side).
		encodeResults := rv.MethodByName("EncodeRequestWithFormats").CallSlice([]reflect.Value{innerReqVal, requestFormatsOverride})
		if errI, _ := encodeResults[1].Interface().(error); errI != nil {
			stats.ReportErrors(obs, "body", errI)
			obs.RecordRequest("MQTT5-REQ", path, 0, time.Since(start))
			return []reflect.Value{zeroResp, reflect.ValueOf(CallError{Kind: KindEncode, Err: errI}).Convert(errType)}
		}
		payload, _ := encodeResults[0].Interface().([]byte)

		secReqs, schemeTypes, schemeCodecs := effectiveSecurity(elem)
		userProps := append(pahomqtt5.UserProperties(nil), t.opts.UserProperties...)
		// Case 1 write-side wiring (see
		// docs/design/d-0003-codec-declared-middlewares.md's Addendum's
		// "Write-side wiring"):
		// a Middleware's WithRequestProperty-declared value merges into
		// the SAME outgoing userProps mechanism the security-credential-
		// derived properties below also use.
		if len(middlewarePropertyVarsOut) > 0 {
			userProps = append(userProps, userPropertiesFromMap(middlewarePropertyVarsOut)...)
		}
		// mergeCredentialUserProperties runs every attached PAIRED
		// credential-supplying ClientMW implementation, merging their
		// returned User Properties — replaces the OLD
		// CallOptions.CredentialFunc call (Phase 1, breaking removal).
		credProps, ran, credErr := mergeCredentialUserProperties(ctx, secReqs, clientImpls)
		if credErr != nil {
			obs.RecordRequest("MQTT5-REQ", path, 0, time.Since(start))
			return []reflect.Value{zeroResp, reflect.ValueOf(credErr).Convert(errType)}
		}
		userProps = append(userProps, credProps...)
		if len(secReqs) > 0 && ran {
			if name, credErr := validateSecurityCredentials(userProps, secReqs, schemeTypes, schemeCodecs); credErr != nil {
				if secObs, ok := obs.(stats.SecurityObserver); ok {
					secObs.RecordSecurityRejection(path, route.FirstSchemeName(secReqs))
				}
				wrapped := reqreply.SecurityCredentialError{Scheme: name, Err: credErr}
				obs.RecordRequest("MQTT5-REQ", path, 0, time.Since(start))
				return []reflect.Value{zeroResp, reflect.ValueOf(wrapped).Convert(errType)}
			}
		}

		reqProps := &pahomqtt5.PublishProperties{ResponseTopic: replyTopic, CorrelationData: corrData}
		if len(userProps) > 0 {
			reqProps.User = userProps
		}
		if _, err := t.client.Publish(ctx, &pahomqtt5.Publish{
			Topic:      path,
			QoS:        qos,
			Payload:    payload,
			Properties: reqProps,
		}); err != nil {
			obs.RecordRequest("MQTT5-REQ", path, 0, time.Since(start))
			return []reflect.Value{zeroResp, reflect.ValueOf(CallError{Kind: KindEncode, Err: fmt.Errorf("publish request: %w", err)}).Convert(errType)}
		}

		timer := time.NewTimer(timeout)
		defer timer.Stop()

		select {
		case <-ctx.Done():
			obs.RecordRequest("MQTT5-REQ", path, 0, time.Since(start))
			return []reflect.Value{zeroResp, reflect.ValueOf(CallError{Kind: KindTimeout, Err: ctx.Err()}).Convert(errType)}
		case <-timer.C:
			obs.RecordRequest("MQTT5-REQ", path, 0, time.Since(start))
			return []reflect.Value{zeroResp, reflect.ValueOf(CallError{Kind: KindTimeout, Err: fmt.Errorf("no reply within %s", timeout)}).Convert(errType)}
		case replyMsg := <-replyCh:
			if isErrorReply(replyMsg) {
				obs.RecordRequest("MQTT5-REQ", path, 500, time.Since(start))
				if code := errorCodeFromUserProperties(replyMsg); code != "" {
					decodeResults := rv.MethodByName("DecodeErrorFor").Call([]reflect.Value{reflect.ValueOf(code), reflect.ValueOf(replyMsg.Payload)})
					errResp, _ := decodeResults[0].Interface().(reqreply.ErrorPatternResponse)
					matched, _ := decodeResults[1].Interface().(bool)
					decErr, _ := decodeResults[2].Interface().(error)
					if matched && decErr == nil {
						return []reflect.Value{zeroResp, reflect.ValueOf(CallError{Kind: KindHandler, Err: ErrorPatternResponse{
							Code: errResp.Code, Value: errResp.Value, Body: errResp.Body,
						}}).Convert(errType)}
					}
				}
				// UNCHANGED fallback — no code property, unmatched code, or
				// decode failed.
				return []reflect.Value{zeroResp, reflect.ValueOf(CallError{Kind: KindHandler, Err: fmt.Errorf("server error: %s", replyMsg.Payload)}).Convert(errType)}
			}
			if propErr := validateUserProperties(replyMsg, responseHeaderParams); propErr != nil {
				obs.RecordValidationError("user_property", stats.ConstraintName(propErr), userPropertyName(propErr))
				obs.RecordRequest("MQTT5-REQ", path, 0, time.Since(start))
				return []reflect.Value{zeroResp, reflect.ValueOf(CallError{Kind: KindSecurity, Err: propErr}).Convert(errType)}
			}
			if len(clientMiddlewareHandlers) > 0 {
				replyPropertyVars := propertyVarsFromUserProperties(replyMsg)
				if mwErr := dispatchClientMiddlewareOut(nil, replyPropertyVars, clientMiddlewareHandlers); mwErr != nil {
					// mwErr is UNAMBIGUOUSLY an Out-decode failure — no Fn
					// involved in dispatchClientMiddlewareOut at all —
					// reported as "middleware:out" (client-side decode of
					// the reply's Out struct), symmetric with
					// "middleware:in" already covering the client-side
					// ENCODE of the request's In struct (dispatchClientMiddlewareIn).
					stats.ReportErrors(obs, "middleware:out", mwErr)
					obs.RecordRequest("MQTT5-REQ", path, 0, time.Since(start))
					return []reflect.Value{zeroResp, reflect.ValueOf(CallError{Kind: KindDecode, Err: mwErr}).Convert(errType)}
				}
			}
			// DecodeResponseWithFormats honors the per-call override
			// (falling back to route-declared Formats, then plain
			// DecodeResponse) — closes the response-direction half of
			// Phase 0 work item 2 (client-side).
			decodeResults := rv.MethodByName("DecodeResponseWithFormats").CallSlice([]reflect.Value{reflect.ValueOf(replyMsg.Payload), responseFormatsOverride})
			if errI, _ := decodeResults[1].Interface().(error); errI != nil {
				stats.ReportErrors(obs, "body", errI)
				obs.RecordRequest("MQTT5-REQ", path, 0, time.Since(start))
				return []reflect.Value{zeroResp, reflect.ValueOf(CallError{Kind: KindDecode, Err: fmt.Errorf("decode response: %w", errI)}).Convert(errType)}
			}
			obs.RecordRequest("MQTT5-REQ", path, 200, time.Since(start))
			return []reflect.Value{decodeResults[0], reflect.Zero(errType)}
		}
	})

	// applyGeneralClientMiddleware wraps innerCall with every
	// general-purpose ClientMW implementation, OUTERMOST-in — mirrors
	// adapters/nethttp's wrapCallGeneral exactly, reflection-based since
	// Req/Resp are erased at this dispatcher's call site.
	wrappedCall := innerCall
	for i := len(clientImpls) - 1; i >= 0; i-- {
		fnVal := reflect.ValueOf(clientImpls[i].Fn)
		if !fnVal.IsValid() || fnVal.Type() != wantGeneralDecoratorFnType {
			continue
		}
		wrappedCall = fnVal.Call([]reflect.Value{wrappedCall})[0]
	}

	finalResults := wrappedCall.Call([]reflect.Value{reflect.ValueOf(ctx), reqVal})
	if errI, _ := finalResults[1].Interface().(error); errI != nil {
		return nil, errI
	}
	return finalResults[0].Interface(), nil
}

// CallAsync implements [reqreply.ClientTransport]. Non-blocking
// counterpart to [clientTransport.Call]: recovers a *reqreply.Future[Resp]
// (as any) via routeAny's [reqreply.FutureFactory] (confirmed zero-
// reflection-for-generics mechanism, see [reqreply.RouteHandle.NewFutureAny]),
// then runs the SAME dispatch [clientTransport.Call] does in a background
// goroutine, resolving the future exactly once when it completes — the
// "send here, resolve elsewhere" mechanism [reqreply.Client.CallAsync]
// documents.
func (t *clientTransport) CallAsync(ctx context.Context, routeAny any, reqAny any, opts ...reqreply.ClientCallOptions) (any, error) {
	var o reqreply.ClientCallOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	handleVal, _, err := recoverRouteHandleValue(routeAny)
	if err != nil {
		return nil, err
	}
	ff, ok := handleVal.Interface().(reqreply.FutureFactory)
	if !ok {
		return nil, reqreply.TransportTypeMismatchError{Want: "reqreply.FutureFactory", Got: fmt.Sprintf("%T", routeAny)}
	}
	future, resolve := ff.NewFutureAny()
	go func() {
		resp, err := t.call(ctx, routeAny, reqAny, o)
		resolve(resp, err)
	}()
	return future, nil
}

var _ reqreply.ClientTransport = (*clientTransport)(nil)

// ── Phase 1: declarative middleware (docs/design/d-0004-reqreply-workflow-simplification.md's Addendum) ──
//
// Fn-shape dispatch for [reqreply.Route.HandleMW]/[reqreply.Route.ClientMW]
// implementations, mirroring [adapters/nethttp]'s identical mechanism
// (server-side scope-grant security + general-purpose decorators;
// client-side credential-supply + general-purpose decorators) — folded
// into this file (not a separate "reqreply_middleware.go") to match
// [adapters/nethttp]'s own file-layout convention: its equivalent
// dispatch code (`validateImplementationShapesReflect`/
// `runSecurityMiddlewareReflect`/`applyGeneralMiddleware` server-side,
// `validateCallImplementationShapes`/`wrapCallGeneral`/
// `mergeCredentialHeaders` client-side) lives inline in `serve.go`/
// `client.go`, not a dedicated middleware file.
//
// mqtt5's OLD `ServeOptions.SecurityFunc`/`CallOptions.CredentialFunc`
// fields are REMOVED entirely (a breaking change, confirmed intentional —
// mirrors REST's own D-0001 precedent, reversing this doc's earlier
// Decision #4 which had followed events pub/sub's "keep both forever"
// precedent instead). `handle.Implementations`/`ClientImplementations`
// are now the ONLY mechanism, consulted by BOTH the escape hatch
// (`Serve`/`Call`/`CallHandle`, which delegate to these same transports
// since Phase 0b) and `Attach`-based dispatch — exactly one path, not two.

// serverSecurityFnType is the PAIRED server-side security Fn shape:
// verifies credentials (already codec-format-validated by
// validateSecurityCredentials before this runs) and returns a scope-grant
// map, combined across every attached paired implementation via
// [middleware.CheckScopes] — mirrors [adapters/nethttp]'s identical
// scope-grant model (Decision #4 of docs/design/d-0004-reqreply-workflow-simplification.md's Addendum),
// NOT zeromq's own in-payload *Req shape (each transport adapter mirrors
// its OWN precedent, an intentional per-adapter difference, not an
// inconsistency).
var serverSecurityFnType = reflect.TypeOf((func(context.Context, *pahomqtt5.Publish, []route.SecurityRequirement) (map[string][]string, error))(nil))

// serverGeneralFnType is the UNPAIRED, general-purpose server-side
// decorator shape — wraps the per-message dispatch closure itself,
// mirroring [adapters/nethttp]'s func(http.Handler) http.Handler
// decorator exactly, adapted to mqtt5's per-message handler shape.
var serverGeneralFnType = reflect.TypeOf((func(func(*pahomqtt5.Publish)) func(*pahomqtt5.Publish))(nil))

// validateServerImplementationShapes checks every attached impl.Fn against
// the two concrete shapes this package recognizes, EAGERLY at Serve
// construction time (once per route, not per message) — a malformed Fn
// fails loudly and immediately, mirroring
// [adapters/nethttp]'s validateImplementationShapesReflect.
func validateServerImplementationShapes(routeLabel string, impls []middleware.ServerImplementation) error {
	for _, impl := range impls {
		if impl.Fn == nil {
			continue
		}
		fnType := reflect.TypeOf(impl.Fn)
		if fnType == serverSecurityFnType || fnType == serverGeneralFnType {
			continue
		}
		return middleware.MiddlewareShapeError{
			Name:     impl.Name,
			Expected: "func(context.Context, *pahomqtt5.Publish, []route.SecurityRequirement) (map[string][]string, error) or func(func(*pahomqtt5.Publish)) func(*pahomqtt5.Publish)",
			Got:      fmt.Sprintf("%T", impl.Fn),
		}
	}
	return nil
}

// applyGeneralServerMiddleware wraps h with every general-purpose Fn found
// in impls, OUTERMOST-in, in attachment order — mirrors
// [adapters/nethttp]'s applyGeneralMiddleware exactly, adapted to mqtt5's
// per-message handler shape.
func applyGeneralServerMiddleware(h func(*pahomqtt5.Publish), impls []middleware.ServerImplementation) func(*pahomqtt5.Publish) {
	for i := len(impls) - 1; i >= 0; i-- {
		fn, ok := impls[i].Fn.(func(func(*pahomqtt5.Publish)) func(*pahomqtt5.Publish))
		if !ok {
			continue
		}
		h = fn(h)
	}
	return h
}

// runServerSecurityMiddleware runs every attached paired security Fn IN
// ATTACHMENT ORDER (fail-fast on the first one whose OWN verification
// errors), merges their returned grants into ONE map, then performs a
// SINGLE [middleware.CheckScopes] call — mirrors
// [adapters/nethttp]'s runSecurityMiddleware exactly. An implementation
// with an EMPTY Satisfies always runs; a NON-EMPTY Satisfies only runs
// when secReqs is non-empty (mirrors REST's identical gating rationale:
// an unsecured route must not authenticate credentials it never asked
// for).
func runServerSecurityMiddleware(ctx context.Context, msg *pahomqtt5.Publish, impls []middleware.ServerImplementation, secReqs []route.SecurityRequirement) error {
	granted := make(map[string][]string)
	for _, impl := range impls {
		fn, ok := impl.Fn.(func(context.Context, *pahomqtt5.Publish, []route.SecurityRequirement) (map[string][]string, error))
		if !ok {
			continue
		}
		if len(impl.Satisfies) > 0 && len(secReqs) == 0 {
			continue
		}
		g, err := fn(ctx, msg, secReqs)
		if err != nil {
			return err
		}
		for k, v := range g {
			granted[k] = v
		}
	}
	return middleware.CheckScopes(secReqs, granted)
}

// clientCredentialFnType is the PAIRED client-side credential-supplying Fn
// shape — REPLACES the old `CallOptions.CredentialFunc`'s role and shape
// exactly (same parameters, same return type: `[]UserProperty, error`),
// now attached via [reqreply.Route.ClientMW] instead of a per-Attach
// Options field.
var clientCredentialFnType = reflect.TypeOf((func(context.Context, []route.SecurityRequirement) ([]UserProperty, error))(nil))

// validateClientImplementationShapes checks every attached impl.Fn against
// the two concrete shapes this package recognizes client-side, EAGERLY
// before any network activity — mirrors
// [adapters/nethttp]'s validateCallImplementationShapes. generalFnType is
// built by the caller (reflect-only, since Req/Resp are erased at this
// dispatcher's call site).
func validateClientImplementationShapes(impls []middleware.ClientImplementation, generalFnType reflect.Type) error {
	for _, impl := range impls {
		if impl.Fn == nil {
			continue
		}
		fnType := reflect.TypeOf(impl.Fn)
		if fnType == clientCredentialFnType || fnType == generalFnType {
			continue
		}
		return middleware.MiddlewareShapeError{
			Name:     impl.Name,
			Expected: fmt.Sprintf("func(context.Context, []route.SecurityRequirement) ([]mqtt5.UserProperty, error) or %s", generalFnType),
			Got:      fmt.Sprintf("%T", impl.Fn),
		}
	}
	return nil
}

// mergeCredentialUserProperties runs every attached credential-providing
// Fn IN ATTACHMENT ORDER, merging their returned []UserProperty values
// into ONE combined slice — mirrors [adapters/nethttp]'s
// mergeCredentialHeaders exactly (a MERGE, not an authorization check;
// the client never judges its own authorization, only the server does).
// GATED by Satisfies vs secReqs, the SAME correctness rule
// mergeCredentialHeaders applies.
func mergeCredentialUserProperties(ctx context.Context, secReqs []route.SecurityRequirement, impls []middleware.ClientImplementation) (combined []UserProperty, ran bool, err error) {
	reqSchemes := make(map[string]bool, len(secReqs))
	for _, req := range secReqs {
		for scheme := range req {
			reqSchemes[scheme] = true
		}
	}
	for _, impl := range impls {
		fn, ok := impl.Fn.(func(context.Context, []route.SecurityRequirement) ([]UserProperty, error))
		if !ok {
			continue
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
		ran = true
		props, ferr := fn(ctx, secReqs)
		if ferr != nil {
			return nil, ran, ferr
		}
		combined = append(combined, props...)
	}
	return combined, ran, nil
}

// userPropertyParamsFromHeaderSpecs converts declared
// [middleware.HeaderParamSpec] values (attached via [reqreply.Route.Use]/
// [FromUserPropertyParam]) back into [UserPropertyParam] values —
// field-for-field identical shapes (Name, Description, Required, Codec
// *codex.Codec[string]) — so [validateUserProperties] can be reused
// unchanged for Phase 1b's new request-side attachment surface.
func userPropertyParamsFromHeaderSpecs(specs []middleware.HeaderParamSpec) []UserPropertyParam {
	if len(specs) == 0 {
		return nil
	}
	out := make([]UserPropertyParam, len(specs))
	for i, s := range specs {
		out[i] = UserPropertyParam{Name: s.Name, Description: s.Description, Required: s.Required, Codec: s.Codec}
	}
	return out
}

// userPropertyParamsFromResponseHeaderSpecs is
// [userPropertyParamsFromHeaderSpecs]'s reply/response-side sibling —
// converts [middleware.ResponseHeaderParamSpec] values (attached via
// [FromResponseUserPropertyParam]) into [UserPropertyParam] values for
// [AttachClient]'s reply-message validation.
func userPropertyParamsFromResponseHeaderSpecs(specs []middleware.ResponseHeaderParamSpec) []UserPropertyParam {
	if len(specs) == 0 {
		return nil
	}
	out := make([]UserPropertyParam, len(specs))
	for i, s := range specs {
		out[i] = UserPropertyParam{Name: s.Name, Description: s.Description, Required: s.Required, Codec: s.Codec}
	}
	return out
}

// FromUserPropertyParam bridges an EXISTING [UserPropertyParam] value
// into a real [middleware.Middleware], usable with [reqreply.Route.Use]
// exactly like one built from scratch — Phase 1b of
// docs/design/d-0004-reqreply-workflow-simplification.md's Addendum, mirroring [rest.FromHeaderParam]'s
// "wrap what you already have" pattern. Populates
// [middleware.Middleware.RequestHeaderParams] — consulted by
// [reqreply.Route.Register] (rendered into the request message's
// AsyncAPI "headers" schema) AND by this package's own [AttachServer]
// (validated against the real incoming *pahomqtt5.Publish's User
// Properties, additively alongside [ServeOptions.UserPropertyParams],
// which remains unchanged).
//
// Lives in adapters/mqtt5, NOT middleware/api/reqreply — the same
// import-direction reason [rest.FromHeaderParam] lives in api/rest:
// neither middleware nor api/reqreply may import an adapter package, and
// [UserPropertyParam] is an mqtt5-adapter-specific type.
//
//	var authProp = mqtt5.UserPropertyParam{Name: "Authorization", Required: true}
//
//	route := reqreply.NewRoute[Req, Resp]("compute/add", reqCodec, respCodec,
//	).Use(mqtt5.FromUserPropertyParam(authProp))
func FromUserPropertyParam(p UserPropertyParam) middleware.Middleware {
	return middleware.Middleware{
		Name: "declare-user-property-param:" + p.Name,
		RequestHeaderParams: []middleware.HeaderParamSpec{
			{Name: p.Name, Description: p.Description, Required: p.Required, Codec: p.Codec},
		},
	}
}

// FromResponseUserPropertyParam is [FromUserPropertyParam]'s reply/
// response-side sibling — populates
// [middleware.Middleware.ResponseHeaderParams], rendered into the reply
// message's AsyncAPI "headers" schema and validated by this package's
// [AttachClient] against the reply message's User Properties.
func FromResponseUserPropertyParam(p UserPropertyParam) middleware.Middleware {
	return middleware.Middleware{
		Name: "declare-response-user-property-param:" + p.Name,
		ResponseHeaderParams: []middleware.ResponseHeaderParamSpec{
			{Name: p.Name, Description: p.Description, Required: p.Required, Codec: p.Codec},
		},
	}
}
