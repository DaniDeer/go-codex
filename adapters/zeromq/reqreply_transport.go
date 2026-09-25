package zeromq

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/stats"
)

// reqreplyPkgPath is api/reqreply's import path — used to distinguish a
// genuine reqreply.Route[Req,Resp]/*reqreply.RouteHandle[Req,Resp] value
// (for ANY Req/Resp) from an unrelated/wrong-package value passed by
// caller mistake to [AttachServer]/[AttachClient]/[AttachRouterServer]/
// [AttachDealerClient]'s resulting [reqreply.ServerTransport]/
// [reqreply.ClientTransport]. Mirrors [adapters/mqtt5]'s identical
// constant.
const reqreplyPkgPath = "github.com/DaniDeer/go-codex/api/reqreply"

// recoverRouteHandleValue reflects routeAny into a *reqreply.RouteHandle[Req,Resp]
// reflect.Value, accepting EITHER shape [reqreply.Client.Call]'s confirmed
// dual mode allows: a raw, unregistered reqreply.Route[Req,Resp] (calls
// its ClientHandle() method reflectively to derive one) OR an
// already-registered *reqreply.RouteHandle[Req,Resp] (used as-is).
// Mirrors [adapters/mqtt5]'s identical helper (duplicated rather than
// shared — the two adapter packages do not import each other).
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
		handleVal := rv.MethodByName("ClientHandle").Call(nil)[0]
		return handleVal, handleVal.Elem(), nil
	case t.Kind() == reflect.Ptr && t.Elem().PkgPath() == reqreplyPkgPath && strings.HasPrefix(t.Elem().Name(), "RouteHandle["):
		return rv, rv.Elem(), nil
	default:
		return reflect.Value{}, reflect.Value{}, reqreply.TransportTypeMismatchError{
			Want: "reqreply.Route[Req, Resp] or *reqreply.RouteHandle[Req, Resp]", Got: fmt.Sprintf("%T", routeAny),
		}
	}
}

// resolveCallFormatReflect type-asserts overrideAny (a
// [reqreply.ClientCallOptions.RequestFormats]/[reqreply.ClientCallOptions.
// ResponseFormats] value, or a [CallOptions.RequestFormats]/
// [ResponseFormats] value) against declaredFieldType (the reflect.Type of
// the route's own []format.Format[Req]/[]format.Format[Resp] field) — the
// reflection-only equivalent of [resolveCallFormat] (which can't be
// called here directly: it's generic over T, and this dispatcher never
// knows T at compile time). Returns reflect.Zero(declaredFieldType) (an
// empty slice of the right type) when overrideAny is nil — passing it to
// [reqreply.RouteHandle.EncodeRequestWithFormats]/[DecodeResponseWithFormats]/
// [DecodeWithFormats]/[EncodeWithFormats]'s variadic `formats` parameter
// then correctly falls through to THEIR OWN declared-field fallback.
// Mirrors [adapters/mqtt5]'s identical helper (duplicated rather than
// shared — the two adapter packages do not import each other).
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

// sendHandlerErrorReplyReflect is the reflection-based counterpart of
// [sendHandlerErrorReply] — used by [serverTransport.Serve], which has
// no concretely-typed *reqreply.RouteHandle[Req,Resp] to call the generic
// function with. observeErrorResponseForMethod is
// rv.MethodByName("ObserveErrorResponseFor"). Mirrors
// [sendHandlerErrorReply]'s logic exactly: consults
// ObserveErrorResponseFor(ctx, obs, err) first (which ALSO reports
// match/miss/span-tag observability internally — the RECOMMENDED single
// call site, see docs/design/d-0005-error-handling.md's
// Topic 1/5); on a match, sends the declared codec-backed typed payload
// instead of plain text; on no match, or a mapping/encoding failure
// within the matched pattern, falls back to [sendErrorReply]'s
// plain-text behavior unchanged.
func sendHandlerErrorReplyReflect(ctx context.Context, sock FramedSocket, observeErrorResponseForMethod reflect.Value, err error, obs stats.Observer) {
	results := observeErrorResponseForMethod.Call([]reflect.Value{
		reflect.ValueOf(ctx), reflect.ValueOf(&obs).Elem(), reflect.ValueOf(&err).Elem(),
	})
	resp, _ := results[0].Interface().(reqreply.ErrorPatternResponse)
	matched, _ := results[1].Interface().(bool)
	mapErr, _ := results[2].Interface().(error)
	if matched && mapErr == nil {
		// NEW: 3rd frame carries the matched pattern's Code — additive,
		// backward-compatible (the plain-text fallback below stays
		// 2-frame) — lets the CLIENT look up which pattern produced
		// this reply via reqreply.RouteHandle.DecodeErrorFor.
		_ = sock.SendFrames([][]byte{statusError, []byte(resp.Code), resp.Body})
		return
	}
	if matched && mapErr != nil {
		stats.ReportErrors(obs, "error_pattern", mapErr)
	}
	sendErrorReply(sock, err)
}

// tryDeadLetterReflect is the reflection-based counterpart of
// [tryDeadLetter] (the pub/sub adapter's helper) — used by
// [serverTransport.Serve], which has no concretely-typed
// *reqreply.RouteHandle[Req,Resp] to call [reqreply.RouteHandle.
// DeadLetterFor] with directly. sourceTopic is the route's OWN concrete
// topic (path) — REQ/REP sockets are point-to-point (one socket per
// route, no topic frame on the wire), so path is the only meaningful
// "source topic" available, mirroring [tryDeadLetterReflect]'s mqtt5
// counterpart's use of the request's concrete topic.
//
// UNLIKE mqtt5 (one shared client can Publish to ANY topic), a REQ/REP
// socket is point-to-point: there is no broker to address an arbitrary
// dead-letter topic through. sockets is the SAME topic→socket map passed
// to [AttachServer] — the declared dead-letter topic MUST have its own
// entry there (typically a PUSH socket feeding a dead-letter consumer)
// for a dead-letter to actually be reachable. When no such entry exists,
// this is a silent no-op (NOT a fallback onto the route's own REP
// socket — sending an extra, unsolicited message there would violate
// REQ/REP's strict one-reply-per-request protocol invariant). Returns
// true only when a dead-letter was both declared AND successfully routed
// to its own socket.
func tryDeadLetterReflect(
	sockets map[string]FramedSocket, deadLetterForMethod reflect.Value,
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
	dlqSock, sockOK := sockets[topic]
	if !sockOK {
		return false
	}
	_ = dlqSock.SendFrames([][]byte{[]byte(topic), body})
	return true
}

// sendRouterHandlerErrorReplyReflect is [sendHandlerErrorReplyReflect]'s
// ROUTER-socket counterpart — preserves the identity frame, mirroring
// [sendRouterHandlerErrorReply]'s logic exactly.
func sendRouterHandlerErrorReplyReflect(ctx context.Context, sock FramedSocket, identity []byte, observeErrorResponseForMethod reflect.Value, err error, obs stats.Observer) {
	results := observeErrorResponseForMethod.Call([]reflect.Value{
		reflect.ValueOf(ctx), reflect.ValueOf(&obs).Elem(), reflect.ValueOf(&err).Elem(),
	})
	resp, _ := results[0].Interface().(reqreply.ErrorPatternResponse)
	matched, _ := results[1].Interface().(bool)
	mapErr, _ := results[2].Interface().(error)
	if matched && mapErr == nil {
		// NEW: 4th frame carries the matched pattern's Code — see
		// sendHandlerErrorReplyReflect's identical rationale.
		_ = sock.SendFrames([][]byte{identity, emptyDelimiter, statusError, []byte(resp.Code), resp.Body})
		return
	}
	if matched && mapErr != nil {
		stats.ReportErrors(obs, "error_pattern", mapErr)
	}
	sendRouterErrorReply(sock, identity, err)
}

// MissingSocketError is returned by [AttachServer]/[AttachRouterServer]
// when server has a route registered whose topic is not a key in the
// sockets map, and by [AttachClient]/[AttachDealerClient]'s resulting
// transport when a call is made for a route whose topic is likewise
// missing. Unlike MQTT5's single shared client+router, ZMQ REQ/REP and
// ROUTER/DEALER sockets are point-to-point (one socket per logical
// contract, no topic-multiplexing built in the way a SUB socket has) —
// so [reqreply.Server.RegisteredTopics] is used to validate full
// topic/socket coverage at Attach time, before [reqreply.Server.Serve]
// ever runs, rather than discovering a missing socket only when a
// request for it actually arrives.
// ── Security Fn-shape dispatch (docs/design/d-0004-reqreply-workflow-simplification.md's Addendum) ─────────
//
// Adds the `.Use()`/`HandleMW`/`ClientMW` declare/implement split to
// zeromq reqreply — mirrors `adapters/mqtt5/reqreply_transport.go`'s
// Phase 1 mechanism structurally, but with a genuinely different paired
// Fn shape: zeromq has no raw-message-equivalent type (unlike mqtt5's
// *pahomqtt5.Publish) to operate on instead, so the paired Fn reads/
// writes the DECODED *Req directly — mirroring zeromq's OWN pub/sub
// security-shaped SubscribeMW/PublishMW Fn shape exactly (plain error
// return, no scope-grant map, unlike mqtt5's `(map[string][]string, error)`).
// Because Req/Resp are only known at Serve/Attach RUNTIME (erased,
// reflection-only dispatch), converting a reflect.Value holding a
// decoded Req BY VALUE into an addressable *Req the Fn can read/write
// requires a `reflect.New(reqType)` + `.Elem().Set(reqVal)` round trip —
// a technique this package has not needed before Phase 1's mqtt5
// equivalent never required it either, since mqtt5's paired Fn takes the
// ALREADY-concrete raw message type instead.

// effectiveSecurity returns the route's own declared Security
// requirements, falling back to GlobalSecurity when nil — the same
// precedence every other adapter uses. Unlike mqtt5, zeromq has no
// built-in credential-FORMAT check layer (no SecuritySchemes/Codec
// consultation) — the paired Fn is the ONLY enforcement mechanism,
// mirroring zeromq's OWN pub/sub security-shaped SubscribeMW/PublishMW
// precedent (custom Fn only, no built-in check to run first).
func effectiveSecurity(elem reflect.Value) []route.SecurityRequirement {
	reqs, _ := elem.FieldByName("Security").Interface().([]route.SecurityRequirement)
	if reqs == nil {
		reqs, _ = elem.FieldByName("GlobalSecurity").Interface().([]route.SecurityRequirement)
	}
	return reqs
}

// buildPairedSecurityFnType returns the expected paired security/
// credential Fn shape for a route whose decoded request type is reqType:
// func(context.Context, *Req, []route.SecurityRequirement) error — used
// for BOTH the server-side security Fn (HandleMW) and the client-side
// credential-supplying Fn (ClientMW); the shapes are IDENTICAL (mirrors
// zeromq pub/sub's security-shaped SubscribeMW/PublishMW Fn, which also
// share one shape both directions) — only the CALL SITE semantics differ (server:
// read, optionally enrich, before dispatch; client: write a credential,
// before encode).
func buildPairedSecurityFnType(reqType reflect.Type) reflect.Type {
	return reflect.FuncOf(
		[]reflect.Type{
			reflect.TypeOf((*context.Context)(nil)).Elem(),
			reflect.PointerTo(reqType),
			reflect.TypeOf([]route.SecurityRequirement(nil)),
		},
		[]reflect.Type{reflect.TypeOf((*error)(nil)).Elem()},
		false,
	)
}

// buildGeneralDecoratorFnType returns the expected general-purpose
// (UNPAIRED) decorator Fn shape for a route with request type reqType
// and response type respType: func(next func(context.Context, Req)
// (Resp, error)) func(context.Context, Req) (Resp, error) — wraps the
// FULL decoded request/response handler directly (zeromq has no raw
// pre-decode form to wrap instead, unlike mqtt5's server-side raw
// *pahomqtt5.Publish wrap) — the SAME shape applies on BOTH server and
// client dispatch for this reason (mirrors mqtt5's CLIENT-side decorator
// shape, which already has no raw form to wrap either).
func buildGeneralDecoratorFnType(reqType, respType reflect.Type) reflect.Type {
	inner := reflect.FuncOf(
		[]reflect.Type{reflect.TypeOf((*context.Context)(nil)).Elem(), reqType},
		[]reflect.Type{respType, reflect.TypeOf((*error)(nil)).Elem()},
		false,
	)
	return reflect.FuncOf([]reflect.Type{inner}, []reflect.Type{inner}, false)
}

// validateServerImplementationShapes checks every attached
// [middleware.ServerImplementation]'s Fn against the two shapes this
// package recognizes for THIS route's concrete Req/Resp types, EAGERLY
// at Serve construction time (once per route, not per message) —
// mirrors [adapters/mqtt5]'s identical eager-validation discipline.
func validateServerImplementationShapes(routeLabel string, impls []middleware.ServerImplementation, securityFnType, generalDecoratorFnType reflect.Type) error {
	for _, impl := range impls {
		if impl.Fn == nil {
			continue
		}
		fnType := reflect.TypeOf(impl.Fn)
		if fnType == securityFnType || fnType == generalDecoratorFnType {
			continue
		}
		return middleware.MiddlewareShapeError{
			Name:     impl.Name,
			Expected: "func(context.Context, *Req, []route.SecurityRequirement) error or func(func(context.Context, Req) (Resp, error)) func(context.Context, Req) (Resp, error)",
			Got:      fmt.Sprintf("%T", impl.Fn),
		}
	}
	return nil
}

// validateClientImplementationShapes is
// [validateServerImplementationShapes]'s client-side mirror, for
// [middleware.ClientImplementation] values.
func validateClientImplementationShapes(impls []middleware.ClientImplementation, credentialFnType, generalDecoratorFnType reflect.Type) error {
	for _, impl := range impls {
		if impl.Fn == nil {
			continue
		}
		fnType := reflect.TypeOf(impl.Fn)
		if fnType == credentialFnType || fnType == generalDecoratorFnType {
			continue
		}
		return middleware.MiddlewareShapeError{
			Name:     impl.Name,
			Expected: "func(context.Context, *Req, []route.SecurityRequirement) error or func(func(context.Context, Req) (Resp, error)) func(context.Context, Req) (Resp, error)",
			Got:      fmt.Sprintf("%T", impl.Fn),
		}
	}
	return nil
}

// runPairedServerSecurity runs every attached PAIRED (Satisfies
// non-empty) server-side security implementation in attachment order
// against reqPtr (a fresh, addressable *Req copy of the just-decoded
// request), returning the FIRST error encountered — mirrors zeromq
// pub/sub's own SecurityFunc semantics (plain error, no scope-grant,
// unlike mqtt5's reqreply Fn). reqPtr may be MUTATED by any
// implementation (read/write access to *Req, matching pub/sub's
// identical contract) — the caller re-reads reqPtr.Elem() afterward to
// pick up any enrichment.
func runPairedServerSecurity(ctx context.Context, reqPtr reflect.Value, impls []middleware.ServerImplementation, secReqs []route.SecurityRequirement) error {
	ctxVal := reflect.ValueOf(ctx)
	secReqsVal := reflect.ValueOf(secReqs)
	for _, impl := range impls {
		if len(impl.Satisfies) == 0 {
			continue // unpaired (general-purpose) — handled separately
		}
		fnVal := reflect.ValueOf(impl.Fn)
		results := fnVal.Call([]reflect.Value{ctxVal, reqPtr, secReqsVal})
		if errI, _ := results[0].Interface().(error); errI != nil {
			return errI
		}
	}
	return nil
}

// runPairedClientCredential is [runPairedServerSecurity]'s client-side
// mirror, for [middleware.ClientImplementation] values — writes a
// credential field INTO reqPtr rather than merely reading it.
func runPairedClientCredential(ctx context.Context, reqPtr reflect.Value, impls []middleware.ClientImplementation, secReqs []route.SecurityRequirement) error {
	ctxVal := reflect.ValueOf(ctx)
	secReqsVal := reflect.ValueOf(secReqs)
	for _, impl := range impls {
		if len(impl.Satisfies) == 0 {
			continue
		}
		fnVal := reflect.ValueOf(impl.Fn)
		results := fnVal.Call([]reflect.Value{ctxVal, reqPtr, secReqsVal})
		if errI, _ := results[0].Interface().(error); errI != nil {
			return errI
		}
	}
	return nil
}

// applyGeneralServerMiddleware wraps fnVal (the concrete
// func(context.Context, Req) (Resp, error) handler passed to Serve) with
// every general-purpose (UNPAIRED, Satisfies-empty) implementation found
// in impls, OUTERMOST-in, in attachment order — mirrors
// [adapters/mqtt5]'s applyGeneralServerMiddleware, adapted to zeromq's
// simpler dispatch: the decorator IS the same shape as fnVal itself, so
// no reflect.MakeFunc bridging is needed server-side (unlike the client
// side, which has no pre-existing single Fn value to wrap this way).
func applyGeneralServerMiddleware(fnVal reflect.Value, impls []middleware.ServerImplementation) reflect.Value {
	for i := len(impls) - 1; i >= 0; i-- {
		if len(impls[i].Satisfies) > 0 {
			continue
		}
		decoratorVal := reflect.ValueOf(impls[i].Fn)
		fnVal = decoratorVal.Call([]reflect.Value{fnVal})[0]
	}
	return fnVal
}

// ── docs/design/d-0003-codec-declared-middlewares.md's Addendum dispatch ─────────

type MissingSocketError struct {
	// Topic is the route topic with no corresponding socket entry.
	Topic string
}

func (e MissingSocketError) Error() string {
	return fmt.Sprintf("zeromq: no socket registered for route topic %q (add it to the sockets map passed to AttachServer/AttachClient/AttachRouterServer/AttachDealerClient)", e.Topic)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e MissingSocketError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("topic", e.Topic),
	)
}

// ── REQ/REP server ───────────────────────────────────────────────────────

// serverTransport implements [reqreply.ServerTransport] for ZMQ REQ/REP
// sockets, wrapping a topic→socket map — built by [AttachServer]. A
// reflection shim (mirrors [adapters/mqtt5]'s identical technique): Go
// forbids generic methods, so Serve recovers the concrete Req/Resp types
// at runtime via reflection against the type-erased
// *reqreply.RouteHandle[Req,Resp] fields (Decode/Encode are struct
// FIELDS holding func values, not methods — reflect.Value.Call works
// identically either way).
//
// v1 scope (documented, matching [adapters/mqtt5]'s reqreply shim's own
// precedent): route-declared [reqreply.RouteHandle.RequestFormats]/
// [reqreply.RouteHandle.Formats] overrides and [reqreply.ErrorPattern]-
// typed error replies are NOT honored by this shim — it always uses
// handle.Decode/handle.Encode (JSON) and always sends plain-text error
// replies via [sendErrorReply]. A caller needing either of these should
// use [Serve] directly (fully featured, completely unaffected by this
// addition) instead of the [reqreply.Server]/[AttachServer] workflow.
//
// Serve BLOCKS until ctx is cancelled or a fatal socket error occurs —
// unlike [adapters/mqtt5]'s non-blocking Serve (which registers +
// returns immediately), ZMQ has no built-in dispatch loop of its own to
// delegate to, so this shim runs its own receive loop, exactly like
// [Serve] does. This is the confirmed BLOCKING-transport branch
// [reqreply.Server.Serve]'s concurrent dispatch (one goroutine per
// route) is designed to accommodate.
type serverTransport struct {
	sockets map[string]FramedSocket
	opts    ServeOptions
}

// AttachServer binds server+sockets (via an internal ServerTransport
// shim) as server's [reqreply.ServerTransport] — the "attach the adapter
// to the server" step behind [reqreply.Server.Serve]. sockets maps each
// registered route's topic to the REP socket handling it (REQ/REP is
// point-to-point, so one socket serves exactly one route/topic — unlike
// pub/sub's topic-multiplexed SUB socket). opts (0 or 1 value) configures
// every route dispatched through this transport uniformly.
//
// Returns [MissingSocketError] if server has a registered route whose
// topic has no entry in sockets — checked upfront, at Attach time, not
// discovered later when a request for it arrives. Returns
// [reqreply.ServerTransportAlreadyAttachedError] if server already has a
// transport attached.
//
//	server := reqreply.NewServer(reqreply.Info{Title: "Compute API", Version: "1.0.0"})
//	handle, _ := ComputeRoute.WithHandler(computeHandler).Register(server)
//	err := zeromq.AttachServer(server, map[string]zeromq.FramedSocket{"compute/add": repSock})
//	err = server.Serve(ctx) // blocks, dispatching ComputeRoute (and every other registered route) concurrently
func AttachServer(server *reqreply.Server, sockets map[string]FramedSocket, opts ...ServeOptions) error {
	var o ServeOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	for _, topic := range server.RegisteredTopics() {
		if _, ok := sockets[topic]; !ok {
			return MissingSocketError{Topic: topic}
		}
	}
	return server.Attach(&serverTransport{sockets: sockets, opts: o})
}

// Serve implements [reqreply.ServerTransport]. Mirrors [Serve]'s core
// receive loop (recv → decode → call fn → encode → send reply) via
// reflection against routeAny/fnAny — see [serverTransport]'s doc
// comment for this shim's documented v1 scope and blocking contract.
func (t *serverTransport) Serve(ctx context.Context, routeAny any, fnAny any) error {
	rv, elem, err := recoverRouteHandleValue(routeAny)
	if err != nil {
		return err
	}
	path := elem.FieldByName("Topic").String()
	sock, ok := t.sockets[path]
	if !ok {
		return MissingSocketError{Topic: path}
	}

	obs := t.opts.Observer
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}

	decodeField := elem.FieldByName("Decode") // func([]byte) (Req, error)
	encodeField := elem.FieldByName("Encode") // func(Resp) ([]byte, error)
	reqType := decodeField.Type().Out(0)
	wantFnType := reflect.FuncOf(
		[]reflect.Type{reflect.TypeOf((*context.Context)(nil)).Elem(), reqType},
		[]reflect.Type{encodeField.Type().In(0), reflect.TypeOf((*error)(nil)).Elem()},
		false,
	)
	fnVal := reflect.ValueOf(fnAny)
	if !fnVal.IsValid() || fnVal.Type() != wantFnType {
		return reqreply.TransportTypeMismatchError{Topic: path, Want: wantFnType.String(), Got: fmt.Sprintf("%T", fnAny)}
	}

	// decodeWithFormatsMethod/encodeWithFormatsMethod honor
	// handle.RequestFormats/Formats, falling back to plain Decode/Encode
	// — closes Phase 0 work item 1 (server-side). No merge-field support
	// is added here (unlike mqtt5): zeromq's REQ/REP wire format carries
	// no topic frame at all (routing is entirely socket-based, one
	// socket per concrete topic, never a template) — NewTopicParam
	// merge-field decode was never applicable here, confirmed via the
	// escape hatch's own [Serve], which never called MergeFields/
	// DecodeVars either.
	decodeWithFormatsMethod := rv.MethodByName("DecodeWithFormats")
	encodeWithFormatsMethod := rv.MethodByName("EncodeWithFormats")
	// observeErrorResponseForMethod is *RouteHandle[Req,Resp].
	// ObserveErrorResponseFor(ctx, obs, err) — closes Phase 0 work item 2
	// (server-side), now the RECOMMENDED observability-aware call (see
	// docs/design/d-0005-error-handling.md's Topic 1/5).
	observeErrorResponseForMethod := rv.MethodByName("ObserveErrorResponseFor")
	// deadLetterForMethod is *RouteHandle[Req,Resp].DeadLetterFor(obs,
	// sourceTopic, rawPayload, err) (topic string, body []byte, ok bool)
	// — Topic 4's dead-letter fallback (docs/roadmap/
	// d-0005-error-handling.md), attempted alongside/after
	// ObserveErrorResponseFor at every Category-A failure site, mirroring
	// the pub/sub adapters' collapsed single-rule wiring exactly.
	deadLetterForMethod := rv.MethodByName("DeadLetterFor")

	// Security Fn-shape dispatch (docs/design/d-0004-reqreply-workflow-simplification.md's Addendum):
	// impls are the [reqreply.Route.HandleMW]-attached implementations —
	// validated for shape EAGERLY (once, at Serve construction, not per
	// message) and coverage-checked against the route's declared
	// security requirements, mirroring adapters/mqtt5's identical
	// build-time checks.
	respType := encodeField.Type().In(0)
	securityFnType := buildPairedSecurityFnType(reqType)
	generalDecoratorFnType := buildGeneralDecoratorFnType(reqType, respType)
	impls, _ := elem.FieldByName("Implementations").Interface().([]middleware.ServerImplementation)
	if err := validateServerImplementationShapes(path, impls, securityFnType, generalDecoratorFnType); err != nil {
		return err
	}
	secReqs := effectiveSecurity(elem)
	if err := reqreply.CheckCoverage(path, secReqs, impls); err != nil {
		return err
	}
	// dispatchFn is fnVal wrapped by every general-purpose (UNPAIRED)
	// HandleMW implementation, OUTERMOST-in — the paired security Fns
	// run SEPARATELY, between decode and this call, since they need
	// pointer access to *Req (see the loop body below).
	dispatchFn := applyGeneralServerMiddleware(fnVal, impls)

	// docs/design/d-0003-codec-declared-middlewares.md's Addendum: codec-backed
	// Middleware[In,Out] dispatch — dispatched AFTER the paired security
	// Fns above (D1), confirming the mechanism is genuinely transport-
	// agnostic (zero adapter-specific work beyond consulting the SAME
	// RouteHandle field mqtt5 does). zeromq supplies an ALWAYS-EMPTY
	// property-value map (no property mechanism exists here) and an
	// empty topic-var map (zeromq's REQ/REP wire carries no topic
	// frame/template) — a route declaring a REQUIRED property fails
	// naturally with [reqreply.MiddlewareInputError], no special-casing.
	middlewareHandlers, _ := elem.FieldByName("MiddlewareHandlers").Interface().([]reqreply.MiddlewareHandler)

	if err := sock.SetRecvTimeout(recvPollInterval); err != nil {
		return SocketError{Op: "set_recv_timeout", Err: err}
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		frames, recvErr := sock.RecvFrames()
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
		if len(frames) == 0 {
			continue
		}
		start := time.Now()
		payload := frames[0]

		// TraceObserver span — mirrors the escape hatch's [serveRequest]
		// exactly (span name "zmq.serve"): a capability that was
		// silently absent from this reflection-based dispatcher (found
		// and closed as part of Phase 0/0b, mirroring the SAME gap found
		// and fixed for mqtt5).
		spanCtx := ctx
		var serveErr error
		if to, ok := obs.(stats.TraceObserver); ok {
			spanCtx = to.StartSpan(ctx, "zmq.serve", path)
		}
		endSpan := func() {
			if to, ok := obs.(stats.TraceObserver); ok {
				to.EndSpan(spanCtx, serveErr)
			}
		}

		// DecodeWithFormats honors handle.RequestFormats, falling back
		// to plain Decode — closes Phase 0 work item 1 (server-side).
		decodeResults := decodeWithFormatsMethod.CallSlice([]reflect.Value{reflect.ValueOf(payload), elem.FieldByName("RequestFormats")})
		if errI, _ := decodeResults[1].Interface().(error); errI != nil {
			stats.ReportErrors(obs, "body", errI)
			serveErr = errI
			obs.RecordRequest("ZMQ-REP", path, 0, time.Since(start))
			sendHandlerErrorReplyReflect(spanCtx, sock, observeErrorResponseForMethod, errI, obs)
			tryDeadLetterReflect(t.sockets, deadLetterForMethod, obs, path, payload, errI)
			endSpan()
			if t.opts.OnError != nil {
				t.opts.OnError(ServeError{Kind: KindDecode, Err: errI})
			}
			continue
		}
		reqVal := decodeResults[0]

		// Paired security Fns (docs/design/d-0004-reqreply-workflow-simplification.md's Addendum) run
		// BETWEEN decode and dispatch, reading/optionally enriching the
		// decoded value via a fresh, addressable *Req copy — reqVal is
		// re-read afterward to pick up any mutation.
		if len(impls) > 0 {
			reqPtr := reflect.New(reqType)
			reqPtr.Elem().Set(reqVal)
			if secErr := runPairedServerSecurity(spanCtx, reqPtr, impls, secReqs); secErr != nil {
				// Security middleware Fn error IS ErrorPattern-eligible
				// now (Topic 1's Category A fix).
				wrapped := reqreply.SecurityError{Err: secErr}
				if secObs, ok := obs.(stats.SecurityObserver); ok {
					secObs.RecordSecurityRejection(path, route.FirstSchemeName(secReqs))
				}
				serveErr = wrapped
				obs.RecordRequest("ZMQ-REP", path, 0, time.Since(start))
				sendHandlerErrorReplyReflect(spanCtx, sock, observeErrorResponseForMethod, wrapped, obs)
				tryDeadLetterReflect(t.sockets, deadLetterForMethod, obs, path, payload, wrapped)
				endSpan()
				if t.opts.OnError != nil {
					t.opts.OnError(ServeError{Kind: KindSecurity, Err: wrapped})
				}
				continue
			}
			reqVal = reqPtr.Elem()
		}

		// docs/design/d-0003-codec-declared-middlewares.md's Addendum: codec-
		// backed Middleware[In,Out] dispatch — runs AFTER the paired
		// security Fns above (D1), reading/enriching the SAME reqVal.
		if len(middlewareHandlers) > 0 {
			reqPtr := reflect.New(reqType)
			reqPtr.Elem().Set(reqVal)
			_, _, mwName, failKind, mwErr := reqreply.DispatchServerMiddlewareHandlers(spanCtx, reqPtr, middlewareHandlers, nil, nil)
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
				obs.RecordRequest("ZMQ-REP", path, 0, time.Since(start))
				// Middleware DecodeIn/Fn/EncodeOut errors are ALL
				// ErrorPattern-eligible now (Topic 1's Category A fix).
				sendHandlerErrorReplyReflect(spanCtx, sock, observeErrorResponseForMethod, mwErr, obs)
				tryDeadLetterReflect(t.sockets, deadLetterForMethod, obs, path, payload, mwErr)
				endSpan()
				if t.opts.OnError != nil {
					t.opts.OnError(ServeError{Kind: kind, Err: mwErr})
				}
				_ = mwName
				continue
			}
			reqVal = reqPtr.Elem()
		}

		fnResults := dispatchFn.Call([]reflect.Value{reflect.ValueOf(spanCtx), reqVal})
		if errI, _ := fnResults[1].Interface().(error); errI != nil {
			serveErr = errI
			obs.RecordRequest("ZMQ-REP", path, 0, time.Since(start))
			sendHandlerErrorReplyReflect(spanCtx, sock, observeErrorResponseForMethod, errI, obs)
			tryDeadLetterReflect(t.sockets, deadLetterForMethod, obs, path, payload, errI)
			endSpan()
			if t.opts.OnError != nil {
				t.opts.OnError(ServeError{Kind: KindHandler, Err: errI})
			}
			continue
		}
		respVal := fnResults[0]

		// EncodeWithFormats honors handle.Formats, falling back to plain
		// Encode — closes the response-direction half of Phase 0 work
		// item 1 (server-side).
		encodeResults := encodeWithFormatsMethod.CallSlice([]reflect.Value{respVal, elem.FieldByName("Formats")})
		if errI, _ := encodeResults[1].Interface().(error); errI != nil {
			serveErr = errI
			obs.RecordRequest("ZMQ-REP", path, 0, time.Since(start))
			sendHandlerErrorReplyReflect(spanCtx, sock, observeErrorResponseForMethod, errI, obs)
			tryDeadLetterReflect(t.sockets, deadLetterForMethod, obs, path, payload, errI)
			endSpan()
			if t.opts.OnError != nil {
				t.opts.OnError(ServeError{Kind: KindEncode, Err: errI})
			}
			continue
		}
		respPayload, _ := encodeResults[0].Interface().([]byte)

		if sendErr := sock.SendFrames([][]byte{statusOK, respPayload}); sendErr != nil {
			serveErr = sendErr
			obs.RecordRequest("ZMQ-REP", path, 0, time.Since(start))
			// Session-review fix (G1): a socket-level rejection of the
			// SUCCESSFULLY-encoded reply is ALSO dead-letterable —
			// mirrors events' pub/sub publish side's own
			// broker-rejection handling (Topic 4's explicit design
			// decision), previously missing here even though every
			// OTHER Category-A failure point in this dispatch already
			// consults DeadLetterFor.
			tryDeadLetterReflect(t.sockets, deadLetterForMethod, obs, path, payload, sendErr)
			endSpan()
			if t.opts.OnError != nil {
				t.opts.OnError(ServeError{Kind: KindEncode, Err: sendErr})
			}
			continue
		}
		obs.RecordRequest("ZMQ-REP", path, 200, time.Since(start))
		endSpan()
	}
}

var _ reqreply.ServerTransport = (*serverTransport)(nil)

// ── REQ/REP client ───────────────────────────────────────────────────────

// clientTransport implements [reqreply.ClientTransport] for ZMQ REQ
// sockets, wrapping a topic→socket map — built by [AttachClient]. See
// [serverTransport]'s doc comment for this shim's identical reflection
// technique and documented v1 scope.
type clientTransport struct {
	sockets map[string]FramedSocket
	opts    CallOptions
}

// AttachClient binds client+sockets (via an internal ClientTransport
// shim) as client's [reqreply.ClientTransport] — the "attach the adapter
// to the client" step behind [reqreply.Client.Call]/
// [reqreply.Client.CallAsync]. sockets maps each route's topic to the
// REQ socket used to call it. opts (0 or 1 value) configures every call
// dispatched through this transport uniformly.
//
// Returns [reqreply.ClientTransportAlreadyAttachedError] if client
// already has a transport attached. A call for a route whose topic has
// no entry in sockets returns [MissingSocketError] at call time (no
// upfront coverage check on the client side — unlike [AttachServer],
// the client may only ever need a subset of the server's registered
// routes).
//
//	client := reqreply.NewClient()
//	_ = zeromq.AttachClient(client, map[string]zeromq.FramedSocket{"compute/add": reqSock})
//	respAny, err := client.Call(ctx, ComputeRoute, ComputeReq{X: 1, Y: 2})
func AttachClient(client *reqreply.Client, sockets map[string]FramedSocket, opts ...CallOptions) error {
	var o CallOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	return client.Attach(&clientTransport{sockets: sockets, opts: o})
}

// Call implements [reqreply.ClientTransport]. Mirrors [Call]'s core logic
// (encode request → send → await reply → decode) via reflection against
// routeAny/reqAny — see [clientTransport]'s doc comment. opts carries a
// per-call format override — see [clientTransport.call].
func (t *clientTransport) Call(ctx context.Context, routeAny any, reqAny any, opts ...reqreply.ClientCallOptions) (any, error) {
	var o reqreply.ClientCallOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	return t.call(ctx, routeAny, reqAny, o)
}

// call's named return values (result, err) let the deferred
// TraceObserver span-end observe the eventual error from EVERY return
// statement in this function without touching each one individually —
// mirrors [adapters/mqtt5]'s identical pattern.
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

	sock, ok := t.sockets[path]
	if !ok {
		obs.RecordRequest("ZMQ-REQ", path, 0, time.Since(start))
		return nil, MissingSocketError{Topic: path}
	}

	reqType := elem.FieldByName("EncodeRequest").Type().In(0)
	reqVal := reflect.ValueOf(reqAny)
	if !reqVal.IsValid() || reqVal.Type() != reqType {
		obs.RecordRequest("ZMQ-REQ", path, 0, time.Since(start))
		return nil, reqreply.TransportTypeMismatchError{Topic: path, Want: reqType.String(), Got: fmt.Sprintf("%T", reqAny)}
	}

	// Resolve an OBSERVABILITY-only path override (span name/RecordRequest
	// argument), mirroring the escape hatch's [CallHandle]/[Call]
	// CallOptions.Vars precedence EXACTLY: auto-derive from req via
	// RouteHandle.EncodeVars when the route declares NewTopicParam merge
	// fields, then let an explicit t.opts.Vars entry win on the same key.
	// Does NOT affect socket selection (sock is already resolved above,
	// via the route's literal registered topic — zeromq REQ/REP routing
	// is socket-based, never topic-template-based, confirmed via the
	// escape hatch's own doc comment on [CallHandle]). A BuildTopic
	// failure here IS FATAL (mirrors the escape hatch's [Call] exactly —
	// confirmed via TestCall_Vars_MissingVar_ReturnsCallError, which
	// expects a CallError{MissingRouteParamError} BEFORE anything is
	// ever sent) — an earlier revision of this fix wrongly treated it as
	// a non-fatal, ignorable observability-labeling concern, which left
	// the socket send/recv path to run with NO reply ever coming,
	// hanging indefinitely.
	if mergeFieldsMethod := rv.MethodByName("MergeFields"); mergeFieldsMethod.Call(nil)[0].Len() > 0 || t.opts.Vars != nil {
		var vars map[string]string
		if mergeFieldsMethod.Call(nil)[0].Len() > 0 {
			encodeVarsResults := rv.MethodByName("EncodeVars").Call([]reflect.Value{reqVal})
			vars, _ = encodeVarsResults[0].Interface().(map[string]string)
		}
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
		buildTopicResults := rv.MethodByName("BuildTopic").Call([]reflect.Value{reflect.ValueOf(vars)})
		if errI, _ := buildTopicResults[1].Interface().(error); errI != nil {
			stats.ReportErrors(obs, "topic_var", errI)
			obs.RecordRequest("ZMQ-REQ", path, 0, time.Since(start))
			return nil, CallError{Err: errI}
		}
		path = buildTopicResults[0].String()
	}

	// TraceObserver span — mirrors the escape hatch's [Call] exactly
	// (span name "zmq.request"): a capability that was silently absent
	// from this reflection-based dispatcher, found and closed as part of
	// Phase 0/0b, mirroring the SAME gap found and fixed for mqtt5.
	if to, ok := obs.(stats.TraceObserver); ok {
		ctx = to.StartSpan(ctx, "zmq.request", path)
		defer func() { to.EndSpan(ctx, err) }()
	}

	// Resolve per-call format overrides, in priority order:
	// callOpts.RequestFormats/ResponseFormats (the NEW, Attach-based
	// reqreply.ClientCallOptions parameter) > t.opts.RequestFormats/
	// ResponseFormats (the mqtt5.CallOptions-style fields the escape
	// hatch's Call/CallHandle have always supported) > nil (route-
	// declared, or plain EncodeRequest/DecodeResponse) — mirrors
	// [adapters/mqtt5]'s identical priority chain exactly.
	requestOverrideAny := callOpts.RequestFormats
	if requestOverrideAny == nil {
		requestOverrideAny = t.opts.RequestFormats
	}
	requestFormatsOverride, fmtErr := resolveCallFormatReflect(requestOverrideAny, elem.FieldByName("RequestFormats").Type())
	if fmtErr != nil {
		obs.RecordRequest("ZMQ-REQ", path, 0, time.Since(start))
		return nil, CallError{Err: fmtErr}
	}
	responseOverrideAny := callOpts.ResponseFormats
	if responseOverrideAny == nil {
		responseOverrideAny = t.opts.ResponseFormats
	}
	responseFormatsOverride, fmtErr := resolveCallFormatReflect(responseOverrideAny, elem.FieldByName("Formats").Type())
	if fmtErr != nil {
		obs.RecordRequest("ZMQ-REQ", path, 0, time.Since(start))
		return nil, CallError{Err: fmtErr}
	}

	// Security Fn-shape dispatch (docs/design/d-0004-reqreply-workflow-simplification.md's Addendum).
	// clientImpls are the [reqreply.Route.ClientMW]-attached
	// implementations — validated for shape EAGERLY, mirroring
	// adapters/mqtt5's identical build-time check. respType/innerType
	// let this reflection-only dispatcher recognize the general-purpose
	// decorator shape without knowing Req/Resp at compile time.
	respType := elem.FieldByName("DecodeResponse").Type().Out(0)
	clientImpls, _ := elem.FieldByName("ClientImplementations").Interface().([]middleware.ClientImplementation)
	credentialFnType := buildPairedSecurityFnType(reqType)
	generalDecoratorFnType := buildGeneralDecoratorFnType(reqType, respType)
	if err := validateClientImplementationShapes(clientImpls, credentialFnType, generalDecoratorFnType); err != nil {
		obs.RecordRequest("ZMQ-REQ", path, 0, time.Since(start))
		return nil, err
	}
	secReqs := effectiveSecurity(elem)
	errType := reflect.TypeOf((*error)(nil)).Elem()

	// docs/design/d-0003-codec-declared-middlewares.md's Addendum: codec-backed
	// ClientMiddlewareHandler dispatch — runs AFTER the paired
	// credential Fns (D1 mirror), inside innerCall below. zeromq has no
	// wire mechanism to carry the produced topic/property vars (no
	// topic template, no property side channel) — dispatched purely for
	// the Fn's own business logic/error semantics; the produced vars
	// themselves are N/A here (see docs' "adapters/zeromq: N/A for all
	// 3 cases").
	clientMiddlewareHandlers, _ := elem.FieldByName("ClientMiddlewareHandlers").Interface().([]reqreply.ClientMiddlewareHandler)

	// innerCall is the "credential → encode → send → recv → decode"
	// sequence, wrapped via [reflect.MakeFunc] into a concretely-typed
	// func(context.Context, Req) (Resp, error) value so every attached
	// general-purpose ClientMW decorator (itself a real, concretely-typed
	// Go closure) can wrap it — mirrors adapters/mqtt5's identical
	// reflect.MakeFunc technique, adapted to zeromq's simpler (no
	// User-Property side channel) in-payload credential model.
	innerType := reflect.FuncOf(
		[]reflect.Type{reflect.TypeOf((*context.Context)(nil)).Elem(), reqType},
		[]reflect.Type{respType, errType},
		false,
	)
	innerCall := reflect.MakeFunc(innerType, func(args []reflect.Value) []reflect.Value {
		// ctx is read from args[0], NOT the outer captured ctx variable
		// — a general-purpose ClientMW decorator wrapping this closure
		// may call next(modifiedCtx, req) with a context it mutated
		// (added a value, deadline, span, etc.); shadowing the outer
		// name here means every subsequent use of ctx in this closure
		// sees that decorator's context, not the original one captured
		// before any decorator ran. Mirrors the identical fix applied
		// to adapters/mqtt5's own innerCall (found via a later review
		// pass — the bug was faithfully mirrored here from mqtt5's
		// Phase 1, now fixed in both places).
		ctx := args[0].Interface().(context.Context)
		innerReqVal := args[1]
		zeroResp := reflect.Zero(respType)

		// Paired credential Fns write a credential field INTO a fresh
		// *Req copy, BEFORE encode — mirrors [serverTransport.Serve]'s
		// identical read/write mechanic, mirrored for the write
		// direction.
		if len(clientImpls) > 0 {
			reqPtr := reflect.New(reqType)
			reqPtr.Elem().Set(innerReqVal)
			if credErr := runPairedClientCredential(ctx, reqPtr, clientImpls, secReqs); credErr != nil {
				wrapped := reqreply.SecurityCredentialError{Scheme: route.FirstSchemeName(secReqs), Err: credErr}
				if secObs, ok := obs.(stats.SecurityObserver); ok {
					secObs.RecordSecurityRejection(path, route.FirstSchemeName(secReqs))
				}
				obs.RecordRequest("ZMQ-REQ", path, 0, time.Since(start))
				return []reflect.Value{zeroResp, reflect.ValueOf(CallError{Err: wrapped}).Convert(errType)}
			}
			innerReqVal = reqPtr.Elem()
		}

		// docs/design/d-0003-codec-declared-middlewares.md's Addendum: codec-
		// backed ClientMiddlewareHandler dispatch — runs AFTER the
		// paired credential Fns above (D1).
		if len(clientMiddlewareHandlers) > 0 {
			_, _, mwName, mwErr := reqreply.DispatchClientMiddlewareIn(ctx, innerReqVal, clientMiddlewareHandlers)
			if mwErr != nil {
				_, isFnErr := mwErr.(reqreply.MiddlewareError)
				loc := "middleware:in"
				if isFnErr {
					loc = "middleware:fn"
				}
				stats.ReportErrors(obs, loc, mwErr)
				obs.RecordRequest("ZMQ-REQ", path, 0, time.Since(start))
				_ = mwName
				return []reflect.Value{zeroResp, reflect.ValueOf(CallError{Err: mwErr}).Convert(errType)}
			}
		}

		// EncodeRequestWithFormats honors the per-call/t.opts override
		// (falling back to route-declared RequestFormats, then plain
		// EncodeRequest) — closes Phase 0 work item 1 (client-side).
		encodeResults := rv.MethodByName("EncodeRequestWithFormats").CallSlice([]reflect.Value{innerReqVal, requestFormatsOverride})
		if errI, _ := encodeResults[1].Interface().(error); errI != nil {
			stats.ReportErrors(obs, "body", errI)
			obs.RecordRequest("ZMQ-REQ", path, 0, time.Since(start))
			return []reflect.Value{zeroResp, reflect.ValueOf(CallError{Err: errI}).Convert(errType)}
		}
		payload, _ := encodeResults[0].Interface().([]byte)

		if sendErr := sock.SendFrames([][]byte{payload}); sendErr != nil {
			obs.RecordRequest("ZMQ-REQ", path, 0, time.Since(start))
			return []reflect.Value{zeroResp, reflect.ValueOf(CallError{Err: fmt.Errorf("send: %w", sendErr)}).Convert(errType)}
		}

		if err := sock.SetRecvTimeout(recvPollInterval); err != nil {
			return []reflect.Value{zeroResp, reflect.ValueOf(CallError{Err: fmt.Errorf("set recv timeout: %w", err)}).Convert(errType)}
		}

		var frames [][]byte
		for {
			select {
			case <-ctx.Done():
				obs.RecordRequest("ZMQ-REQ", path, 0, time.Since(start))
				return []reflect.Value{zeroResp, reflect.ValueOf(CallError{Err: ctx.Err()}).Convert(errType)}
			default:
			}
			var recvErr error
			frames, recvErr = sock.RecvFrames()
			if errors.Is(recvErr, ErrTimeout) {
				continue
			}
			if recvErr != nil {
				obs.RecordRequest("ZMQ-REQ", path, 0, time.Since(start))
				return []reflect.Value{zeroResp, reflect.ValueOf(CallError{Err: fmt.Errorf("recv: %w", recvErr)}).Convert(errType)}
			}
			break
		}

		if len(frames) < 2 {
			obs.RecordRequest("ZMQ-REQ", path, 0, time.Since(start))
			return []reflect.Value{zeroResp, reflect.ValueOf(CallError{Err: fmt.Errorf("malformed reply: expected [status, payload], got %d frame(s)", len(frames))}).Convert(errType)}
		}

		if string(frames[0]) == "error" {
			obs.RecordRequest("ZMQ-REQ", path, 500, time.Since(start))
			// A matched ErrorPattern reply is 3-frame: [status, code, body].
			// The plain-text fallback stays 2-frame: [status, body].
			if len(frames) >= 3 {
				code := string(frames[1])
				body := frames[2]
				decodeResults := rv.MethodByName("DecodeErrorFor").Call([]reflect.Value{reflect.ValueOf(code), reflect.ValueOf(body)})
				errResp, _ := decodeResults[0].Interface().(reqreply.ErrorPatternResponse)
				matched, _ := decodeResults[1].Interface().(bool)
				decErr, _ := decodeResults[2].Interface().(error)
				if matched && decErr == nil {
					return []reflect.Value{zeroResp, reflect.ValueOf(CallError{Err: ErrorPatternResponse{
						Code: errResp.Code, Value: errResp.Value, Body: errResp.Body,
					}}).Convert(errType)}
				}
			}
			// UNCHANGED fallback — 2-frame reply, unmatched code, or
			// decode failed. frames[len(frames)-1] is the body in both
			// the 2-frame and unmatched-3-frame case.
			return []reflect.Value{zeroResp, reflect.ValueOf(CallError{Err: fmt.Errorf("server error: %s", frames[len(frames)-1])}).Convert(errType)}
		}

		if len(clientMiddlewareHandlers) > 0 {
			// zeromq has neither a reply-topic nor a property mechanism
			// (confirmed: REQ/REP frames carry only [status, payload]) —
			// both maps are always empty; a route declaring a REQUIRED
			// WithResponseTopic/WithResponseProperty naturally fails here,
			// no special-casing needed, mirrors mqtt5's identical call.
			// mwErr is UNAMBIGUOUSLY an Out-decode failure — reported as
			// "middleware:out", symmetric with "middleware:in" already
			// covering the client-side ENCODE of the request's In struct.
			if mwErr := reqreply.DispatchClientMiddlewareOut(nil, nil, clientMiddlewareHandlers); mwErr != nil {
				stats.ReportErrors(obs, "middleware:out", mwErr)
				obs.RecordRequest("ZMQ-REQ", path, 0, time.Since(start))
				return []reflect.Value{zeroResp, reflect.ValueOf(CallError{Err: mwErr}).Convert(errType)}
			}
		}

		// DecodeResponseWithFormats honors the per-call/t.opts override
		// (falling back to route-declared Formats, then plain
		// DecodeResponse) — closes the response-direction half of Phase
		// 0 work item 1 (client-side).
		decodeResults := rv.MethodByName("DecodeResponseWithFormats").CallSlice([]reflect.Value{reflect.ValueOf(frames[1]), responseFormatsOverride})
		if errI, _ := decodeResults[1].Interface().(error); errI != nil {
			stats.ReportErrors(obs, "body", errI)
			obs.RecordRequest("ZMQ-REQ", path, 0, time.Since(start))
			return []reflect.Value{zeroResp, reflect.ValueOf(CallError{Err: fmt.Errorf("decode response: %w", errI)}).Convert(errType)}
		}
		obs.RecordRequest("ZMQ-REQ", path, 200, time.Since(start))
		return []reflect.Value{decodeResults[0], reflect.Zero(errType)}
	})

	// applyGeneralClientMiddleware wraps innerCall with every
	// general-purpose ClientMW implementation, OUTERMOST-in — mirrors
	// adapters/mqtt5's identical composition, reflection-based since
	// Req/Resp are erased at this dispatcher's call site.
	wrappedCall := innerCall
	for i := len(clientImpls) - 1; i >= 0; i-- {
		if len(clientImpls[i].Satisfies) > 0 {
			continue
		}
		decoratorVal := reflect.ValueOf(clientImpls[i].Fn)
		wrappedCall = decoratorVal.Call([]reflect.Value{wrappedCall})[0]
	}

	finalResults := wrappedCall.Call([]reflect.Value{reflect.ValueOf(ctx), reqVal})
	if errI, _ := finalResults[1].Interface().(error); errI != nil {
		return nil, errI
	}
	return finalResults[0].Interface(), nil
}

// CallAsync implements [reqreply.ClientTransport]. Non-blocking
// counterpart to [clientTransport.Call]: recovers a *reqreply.Future[Resp]
// (as any) via routeAny's [reqreply.FutureFactory], then runs the SAME
// dispatch [clientTransport.Call] does in a background goroutine,
// resolving the future exactly once when it completes. opts is [Call]'s
// identical per-call format-override parameter, applied inside the
// background goroutine.
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

// ── ROUTER/DEALER server ─────────────────────────────────────────────────

// routerServerTransport implements [reqreply.ServerTransport] for ZMQ
// ROUTER sockets, wrapping a topic→socket map — built by
// [AttachRouterServer]. Mirrors [serverTransport]'s reflection technique
// and documented v1 scope; dispatches each request in its own goroutine
// (mirroring [ServeRouter]'s own per-request concurrency), preserving
// the identity frame so the reply reaches the correct DEALER peer.
type routerServerTransport struct {
	sockets map[string]FramedSocket
	opts    ServeOptions
}

// AttachRouterServer binds server+sockets (via an internal
// ServerTransport shim) as server's [reqreply.ServerTransport] for
// ROUTER sockets — the ROUTER/DEALER counterpart of [AttachServer].
// sockets maps each registered route's topic to the ROUTER socket
// handling it.
//
// Returns [MissingSocketError] if server has a registered route whose
// topic has no entry in sockets, checked upfront at Attach time.
// Returns [reqreply.ServerTransportAlreadyAttachedError] if server
// already has a transport attached.
//
//	err := zeromq.AttachRouterServer(server, map[string]zeromq.FramedSocket{"compute/add": routerSock})
func AttachRouterServer(server *reqreply.Server, sockets map[string]FramedSocket, opts ...ServeOptions) error {
	var o ServeOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	for _, topic := range server.RegisteredTopics() {
		if _, ok := sockets[topic]; !ok {
			return MissingSocketError{Topic: topic}
		}
	}
	return server.Attach(&routerServerTransport{sockets: sockets, opts: o})
}

// Serve implements [reqreply.ServerTransport]. Mirrors [ServeRouter]'s
// core receive loop (recv identity+payload → dispatch to its own
// goroutine → decode → call fn → encode → send identity-addressed
// reply) via reflection against routeAny/fnAny.
func (t *routerServerTransport) Serve(ctx context.Context, routeAny any, fnAny any) error {
	rv, elem, err := recoverRouteHandleValue(routeAny)
	if err != nil {
		return err
	}
	path := elem.FieldByName("Topic").String()
	sock, ok := t.sockets[path]
	if !ok {
		return MissingSocketError{Topic: path}
	}

	obs := t.opts.Observer
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}

	decodeField := elem.FieldByName("Decode")
	encodeField := elem.FieldByName("Encode")
	reqType := decodeField.Type().Out(0)
	wantFnType := reflect.FuncOf(
		[]reflect.Type{reflect.TypeOf((*context.Context)(nil)).Elem(), reqType},
		[]reflect.Type{encodeField.Type().In(0), reflect.TypeOf((*error)(nil)).Elem()},
		false,
	)
	fnVal := reflect.ValueOf(fnAny)
	if !fnVal.IsValid() || fnVal.Type() != wantFnType {
		return reqreply.TransportTypeMismatchError{Topic: path, Want: wantFnType.String(), Got: fmt.Sprintf("%T", fnAny)}
	}

	// decodeWithFormatsMethod/encodeWithFormatsMethod/errorResponseForMethod
	// close Phase 0 work items 1-2 (server-side) — same rationale as
	// [serverTransport.Serve] (no merge-field support: ROUTER frames
	// carry no topic either, routing is entirely socket-based).
	decodeWithFormatsMethod := rv.MethodByName("DecodeWithFormats")
	encodeWithFormatsMethod := rv.MethodByName("EncodeWithFormats")
	observeErrorResponseForMethod := rv.MethodByName("ObserveErrorResponseFor")
	// deadLetterForMethod is *RouteHandle[Req,Resp].DeadLetterFor(obs,
	// sourceTopic, rawPayload, err) (topic string, body []byte, ok bool)
	// — Topic 4's dead-letter fallback (docs/roadmap/
	// d-0005-error-handling.md), attempted alongside/after
	// ObserveErrorResponseFor at every Category-A failure site, mirroring
	// the pub/sub adapters' collapsed single-rule wiring exactly.
	deadLetterForMethod := rv.MethodByName("DeadLetterFor")

	// Security Fn-shape dispatch (docs/design/d-0004-reqreply-workflow-simplification.md's Addendum) — same
	// mechanism as [serverTransport.Serve], duplicated for the ROUTER
	// variant (mirrors how Phase 0's capability-parity work was ALSO
	// duplicated, not shared, across these same 4 transports).
	respType := encodeField.Type().In(0)
	securityFnType := buildPairedSecurityFnType(reqType)
	generalDecoratorFnType := buildGeneralDecoratorFnType(reqType, respType)
	impls, _ := elem.FieldByName("Implementations").Interface().([]middleware.ServerImplementation)
	if err := validateServerImplementationShapes(path, impls, securityFnType, generalDecoratorFnType); err != nil {
		return err
	}
	secReqs := effectiveSecurity(elem)
	if err := reqreply.CheckCoverage(path, secReqs, impls); err != nil {
		return err
	}
	dispatchFn := applyGeneralServerMiddleware(fnVal, impls)

	// docs/design/d-0003-codec-declared-middlewares.md's Addendum: codec-backed
	// Middleware[In,Out] dispatch — dispatched AFTER the paired security
	// Fns above (D1), mirrors [serverTransport.Serve]'s identical
	// mechanism for the ROUTER variant (one of the 4 transports this
	// mechanism must be transport-agnostic across). zeromq supplies an
	// ALWAYS-EMPTY property-value map and topic-var map (ROUTER frames
	// carry no topic/property) — a route declaring a REQUIRED property
	// fails naturally, no special-casing.
	middlewareHandlers, _ := elem.FieldByName("MiddlewareHandlers").Interface().([]reqreply.MiddlewareHandler)

	if err := sock.SetRecvTimeout(recvPollInterval); err != nil {
		return SocketError{Op: "set_recv_timeout", Err: err}
	}

	var wg sync.WaitGroup
	for {
		select {
		case <-ctx.Done():
			wg.Wait()
			return nil
		default:
		}
		frames, recvErr := sock.RecvFrames()
		if errors.Is(recvErr, ErrTimeout) {
			continue
		}
		if recvErr != nil {
			select {
			case <-ctx.Done():
				wg.Wait()
				return nil
			default:
				return SocketError{Op: "recv", Err: recvErr}
			}
		}
		if len(frames) < 3 {
			continue // expect [identity, delimiter, payload]
		}
		identity := frames[0]
		payload := frames[2]

		wg.Add(1)
		go func(id, pl []byte) {
			defer wg.Done()
			start := time.Now()

			// TraceObserver span — mirrors [serverTransport.Serve]'s
			// identical addition (span name "zmq.serve"); closes the
			// SAME gap found for the ROUTER variant.
			spanCtx := ctx
			var serveErr error
			if to, ok := obs.(stats.TraceObserver); ok {
				spanCtx = to.StartSpan(ctx, "zmq.serve", path)
			}
			defer func() {
				if to, ok := obs.(stats.TraceObserver); ok {
					to.EndSpan(spanCtx, serveErr)
				}
			}()

			decodeResults := decodeWithFormatsMethod.CallSlice([]reflect.Value{reflect.ValueOf(pl), elem.FieldByName("RequestFormats")})
			if errI, _ := decodeResults[1].Interface().(error); errI != nil {
				stats.ReportErrors(obs, "body", errI)
				serveErr = errI
				obs.RecordRequest("ZMQ-ROUTER", path, 0, time.Since(start))
				sendRouterHandlerErrorReplyReflect(spanCtx, sock, id, observeErrorResponseForMethod, errI, obs)
				tryDeadLetterReflect(t.sockets, deadLetterForMethod, obs, path, pl, errI)
				if t.opts.OnError != nil {
					t.opts.OnError(ServeError{Kind: KindDecode, Err: errI})
				}
				return
			}
			reqVal := decodeResults[0]

			// Paired security Fns run BETWEEN decode and dispatch —
			// mirrors [serverTransport.Serve]'s identical mechanism.
			if len(impls) > 0 {
				reqPtr := reflect.New(reqType)
				reqPtr.Elem().Set(reqVal)
				if secErr := runPairedServerSecurity(spanCtx, reqPtr, impls, secReqs); secErr != nil {
					// Security middleware Fn error IS ErrorPattern-
					// eligible now (Topic 1's Category A fix).
					wrapped := reqreply.SecurityError{Err: secErr}
					if secObs, ok := obs.(stats.SecurityObserver); ok {
						secObs.RecordSecurityRejection(path, route.FirstSchemeName(secReqs))
					}
					serveErr = wrapped
					obs.RecordRequest("ZMQ-ROUTER", path, 0, time.Since(start))
					sendRouterHandlerErrorReplyReflect(spanCtx, sock, id, observeErrorResponseForMethod, wrapped, obs)
					tryDeadLetterReflect(t.sockets, deadLetterForMethod, obs, path, pl, wrapped)
					if t.opts.OnError != nil {
						t.opts.OnError(ServeError{Kind: KindSecurity, Err: wrapped})
					}
					return
				}
				reqVal = reqPtr.Elem()
			}

			// docs/design/d-0003-codec-declared-middlewares.md's Addendum: codec-
			// backed Middleware[In,Out] dispatch — runs AFTER the paired
			// security Fns above (D1), reading/enriching the SAME reqVal.
			if len(middlewareHandlers) > 0 {
				reqPtr := reflect.New(reqType)
				reqPtr.Elem().Set(reqVal)
				_, _, mwName, failKind, mwErr := reqreply.DispatchServerMiddlewareHandlers(spanCtx, reqPtr, middlewareHandlers, nil, nil)
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
					obs.RecordRequest("ZMQ-ROUTER", path, 0, time.Since(start))
					// Middleware DecodeIn/Fn/EncodeOut errors are ALL
					// ErrorPattern-eligible now (Topic 1's Category A fix).
					sendRouterHandlerErrorReplyReflect(spanCtx, sock, id, observeErrorResponseForMethod, mwErr, obs)
					tryDeadLetterReflect(t.sockets, deadLetterForMethod, obs, path, pl, mwErr)
					if t.opts.OnError != nil {
						t.opts.OnError(ServeError{Kind: kind, Err: mwErr})
					}
					_ = mwName
					return
				}
				reqVal = reqPtr.Elem()
			}

			fnResults := dispatchFn.Call([]reflect.Value{reflect.ValueOf(spanCtx), reqVal})
			if errI, _ := fnResults[1].Interface().(error); errI != nil {
				serveErr = errI
				obs.RecordRequest("ZMQ-ROUTER", path, 0, time.Since(start))
				sendRouterHandlerErrorReplyReflect(spanCtx, sock, id, observeErrorResponseForMethod, errI, obs)
				tryDeadLetterReflect(t.sockets, deadLetterForMethod, obs, path, pl, errI)
				if t.opts.OnError != nil {
					t.opts.OnError(ServeError{Kind: KindHandler, Err: errI})
				}
				return
			}
			respVal := fnResults[0]

			encodeResults := encodeWithFormatsMethod.CallSlice([]reflect.Value{respVal, elem.FieldByName("Formats")})
			if errI, _ := encodeResults[1].Interface().(error); errI != nil {
				serveErr = errI
				obs.RecordRequest("ZMQ-ROUTER", path, 0, time.Since(start))
				sendRouterHandlerErrorReplyReflect(spanCtx, sock, id, observeErrorResponseForMethod, errI, obs)
				tryDeadLetterReflect(t.sockets, deadLetterForMethod, obs, path, pl, errI)
				if t.opts.OnError != nil {
					t.opts.OnError(ServeError{Kind: KindEncode, Err: errI})
				}
				return
			}
			respPayload, _ := encodeResults[0].Interface().([]byte)

			if sendErr := sock.SendFrames([][]byte{id, emptyDelimiter, statusOK, respPayload}); sendErr != nil {
				serveErr = sendErr
				obs.RecordRequest("ZMQ-ROUTER", path, 0, time.Since(start))
				// Session-review fix (G1): a socket-level rejection of
				// the SUCCESSFULLY-encoded reply is ALSO dead-letterable
				// — mirrors events' pub/sub publish side's own
				// broker-rejection handling (Topic 4's explicit design
				// decision), previously missing here even though every
				// OTHER Category-A failure point in this dispatch
				// already consults DeadLetterFor.
				tryDeadLetterReflect(t.sockets, deadLetterForMethod, obs, path, pl, sendErr)
				if t.opts.OnError != nil {
					t.opts.OnError(ServeError{Kind: KindEncode, Err: sendErr})
				}
				return
			}
			obs.RecordRequest("ZMQ-ROUTER", path, 200, time.Since(start))
		}(identity, payload)
	}
}

var _ reqreply.ServerTransport = (*routerServerTransport)(nil)

// ── DEALER client ────────────────────────────────────────────────────────

// dealerClientTransport implements [reqreply.ClientTransport] for ZMQ
// DEALER sockets, wrapping a topic→socket map — built by
// [AttachDealerClient]. Mirrors [CallDealer]'s envelope framing (empty
// delimiter + payload).
type dealerClientTransport struct {
	sockets map[string]FramedSocket
	opts    CallOptions
}

// AttachDealerClient binds client+sockets (via an internal
// ClientTransport shim) as client's [reqreply.ClientTransport] for
// DEALER sockets — the ROUTER/DEALER counterpart of [AttachClient].
//
//	client := reqreply.NewClient()
//	_ = zeromq.AttachDealerClient(client, map[string]zeromq.FramedSocket{"compute/add": dealerSock})
func AttachDealerClient(client *reqreply.Client, sockets map[string]FramedSocket, opts ...CallOptions) error {
	var o CallOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	return client.Attach(&dealerClientTransport{sockets: sockets, opts: o})
}

// Call implements [reqreply.ClientTransport]. Mirrors [CallDealer]'s core
// logic (encode → send with empty-delimiter envelope → await reply →
// decode) via reflection against routeAny/reqAny. opts carries a
// per-call format override — see [dealerClientTransport.call].
func (t *dealerClientTransport) Call(ctx context.Context, routeAny any, reqAny any, opts ...reqreply.ClientCallOptions) (any, error) {
	var o reqreply.ClientCallOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	return t.call(ctx, routeAny, reqAny, o)
}

// call's named return values (result, err) let the deferred
// TraceObserver span-end observe the eventual error from EVERY return
// statement — mirrors [clientTransport.call]'s identical pattern.
func (t *dealerClientTransport) call(ctx context.Context, routeAny any, reqAny any, callOpts reqreply.ClientCallOptions) (result any, err error) {
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

	sock, ok := t.sockets[path]
	if !ok {
		obs.RecordRequest("ZMQ-DEALER", path, 0, time.Since(start))
		return nil, MissingSocketError{Topic: path}
	}

	reqType := elem.FieldByName("EncodeRequest").Type().In(0)
	reqVal := reflect.ValueOf(reqAny)
	if !reqVal.IsValid() || reqVal.Type() != reqType {
		obs.RecordRequest("ZMQ-DEALER", path, 0, time.Since(start))
		return nil, reqreply.TransportTypeMismatchError{Topic: path, Want: reqType.String(), Got: fmt.Sprintf("%T", reqAny)}
	}

	// Resolve an OBSERVABILITY-only path override — mirrors
	// [clientTransport.call]'s identical block (see its comment for the
	// full rationale: does NOT affect socket selection, sock is already
	// resolved above; a BuildTopic failure IS FATAL, matching the escape
	// hatch's [CallDealer] exactly).
	if mergeFieldsMethod := rv.MethodByName("MergeFields"); mergeFieldsMethod.Call(nil)[0].Len() > 0 || t.opts.Vars != nil {
		var vars map[string]string
		if mergeFieldsMethod.Call(nil)[0].Len() > 0 {
			encodeVarsResults := rv.MethodByName("EncodeVars").Call([]reflect.Value{reqVal})
			vars, _ = encodeVarsResults[0].Interface().(map[string]string)
		}
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
		buildTopicResults := rv.MethodByName("BuildTopic").Call([]reflect.Value{reflect.ValueOf(vars)})
		if errI, _ := buildTopicResults[1].Interface().(error); errI != nil {
			stats.ReportErrors(obs, "topic_var", errI)
			obs.RecordRequest("ZMQ-DEALER", path, 0, time.Since(start))
			return nil, CallError{Err: errI}
		}
		path = buildTopicResults[0].String()
	}

	// TraceObserver span — mirrors [clientTransport.call]'s identical
	// addition (span name "zmq.request").
	if to, ok := obs.(stats.TraceObserver); ok {
		ctx = to.StartSpan(ctx, "zmq.request", path)
		defer func() { to.EndSpan(ctx, err) }()
	}

	// Resolve per-call format overrides — mirrors [clientTransport.call]'s
	// identical priority chain (callOpts > t.opts > declared).
	requestOverrideAny := callOpts.RequestFormats
	if requestOverrideAny == nil {
		requestOverrideAny = t.opts.RequestFormats
	}
	requestFormatsOverride, fmtErr := resolveCallFormatReflect(requestOverrideAny, elem.FieldByName("RequestFormats").Type())
	if fmtErr != nil {
		obs.RecordRequest("ZMQ-DEALER", path, 0, time.Since(start))
		return nil, CallError{Err: fmtErr}
	}
	responseOverrideAny := callOpts.ResponseFormats
	if responseOverrideAny == nil {
		responseOverrideAny = t.opts.ResponseFormats
	}
	responseFormatsOverride, fmtErr := resolveCallFormatReflect(responseOverrideAny, elem.FieldByName("Formats").Type())
	if fmtErr != nil {
		obs.RecordRequest("ZMQ-DEALER", path, 0, time.Since(start))
		return nil, CallError{Err: fmtErr}
	}

	// Security Fn-shape dispatch (docs/design/d-0004-reqreply-workflow-simplification.md's Addendum) — same
	// mechanism as [clientTransport.call], duplicated for the DEALER
	// variant (mirrors how Phase 0's capability-parity work was ALSO
	// duplicated, not shared, across these same 4 transports).
	respType := elem.FieldByName("DecodeResponse").Type().Out(0)
	clientImpls, _ := elem.FieldByName("ClientImplementations").Interface().([]middleware.ClientImplementation)
	credentialFnType := buildPairedSecurityFnType(reqType)
	generalDecoratorFnType := buildGeneralDecoratorFnType(reqType, respType)
	if err := validateClientImplementationShapes(clientImpls, credentialFnType, generalDecoratorFnType); err != nil {
		obs.RecordRequest("ZMQ-DEALER", path, 0, time.Since(start))
		return nil, err
	}
	secReqs := effectiveSecurity(elem)
	errType := reflect.TypeOf((*error)(nil)).Elem()

	// docs/design/d-0003-codec-declared-middlewares.md's Addendum: codec-backed
	// ClientMiddlewareHandler dispatch — mirrors [clientTransport.call]'s
	// identical mechanism, duplicated for the DEALER variant (one of the
	// 4 transports this mechanism must be transport-agnostic across).
	// zeromq has no wire mechanism to carry produced topic/property vars
	// (no topic template, no property side channel) — dispatched purely
	// for the Fn's own business logic/error semantics.
	clientMiddlewareHandlers, _ := elem.FieldByName("ClientMiddlewareHandlers").Interface().([]reqreply.ClientMiddlewareHandler)

	innerType := reflect.FuncOf(
		[]reflect.Type{reflect.TypeOf((*context.Context)(nil)).Elem(), reqType},
		[]reflect.Type{respType, errType},
		false,
	)
	innerCall := reflect.MakeFunc(innerType, func(args []reflect.Value) []reflect.Value {
		// ctx read from args[0], NOT the outer captured ctx — see
		// clientTransport.call's identical fix/comment for the full
		// rationale (mirrors adapters/mqtt5's own fix too).
		ctx := args[0].Interface().(context.Context)
		innerReqVal := args[1]
		zeroResp := reflect.Zero(respType)

		if len(clientImpls) > 0 {
			reqPtr := reflect.New(reqType)
			reqPtr.Elem().Set(innerReqVal)
			if credErr := runPairedClientCredential(ctx, reqPtr, clientImpls, secReqs); credErr != nil {
				wrapped := reqreply.SecurityCredentialError{Scheme: route.FirstSchemeName(secReqs), Err: credErr}
				if secObs, ok := obs.(stats.SecurityObserver); ok {
					secObs.RecordSecurityRejection(path, route.FirstSchemeName(secReqs))
				}
				obs.RecordRequest("ZMQ-DEALER", path, 0, time.Since(start))
				return []reflect.Value{zeroResp, reflect.ValueOf(CallError{Err: wrapped}).Convert(errType)}
			}
			innerReqVal = reqPtr.Elem()
		}

		// docs/design/d-0003-codec-declared-middlewares.md's Addendum: codec-
		// backed ClientMiddlewareHandler dispatch — runs AFTER the
		// paired credential Fns above (D1).
		if len(clientMiddlewareHandlers) > 0 {
			_, _, mwName, mwErr := reqreply.DispatchClientMiddlewareIn(ctx, innerReqVal, clientMiddlewareHandlers)
			if mwErr != nil {
				_, isFnErr := mwErr.(reqreply.MiddlewareError)
				loc := "middleware:in"
				if isFnErr {
					loc = "middleware:fn"
				}
				stats.ReportErrors(obs, loc, mwErr)
				obs.RecordRequest("ZMQ-DEALER", path, 0, time.Since(start))
				_ = mwName
				return []reflect.Value{zeroResp, reflect.ValueOf(CallError{Err: mwErr}).Convert(errType)}
			}
		}

		encodeResults := rv.MethodByName("EncodeRequestWithFormats").CallSlice([]reflect.Value{innerReqVal, requestFormatsOverride})
		if errI, _ := encodeResults[1].Interface().(error); errI != nil {
			stats.ReportErrors(obs, "body", errI)
			obs.RecordRequest("ZMQ-DEALER", path, 0, time.Since(start))
			return []reflect.Value{zeroResp, reflect.ValueOf(CallError{Err: errI}).Convert(errType)}
		}
		payload, _ := encodeResults[0].Interface().([]byte)

		if sendErr := sock.SendFrames([][]byte{emptyDelimiter, payload}); sendErr != nil {
			obs.RecordRequest("ZMQ-DEALER", path, 0, time.Since(start))
			return []reflect.Value{zeroResp, reflect.ValueOf(CallError{Err: fmt.Errorf("send: %w", sendErr)}).Convert(errType)}
		}

		if err := sock.SetRecvTimeout(recvPollInterval); err != nil {
			return []reflect.Value{zeroResp, reflect.ValueOf(CallError{Err: fmt.Errorf("set recv timeout: %w", err)}).Convert(errType)}
		}

		var frames [][]byte
		for {
			select {
			case <-ctx.Done():
				obs.RecordRequest("ZMQ-DEALER", path, 0, time.Since(start))
				return []reflect.Value{zeroResp, reflect.ValueOf(CallError{Err: ctx.Err()}).Convert(errType)}
			default:
			}
			var recvErr error
			frames, recvErr = sock.RecvFrames()
			if errors.Is(recvErr, ErrTimeout) {
				continue
			}
			if recvErr != nil {
				obs.RecordRequest("ZMQ-DEALER", path, 0, time.Since(start))
				return []reflect.Value{zeroResp, reflect.ValueOf(CallError{Err: fmt.Errorf("recv: %w", recvErr)}).Convert(errType)}
			}
			break
		}

		// Expect [delimiter, status, payload].
		if len(frames) < 3 {
			obs.RecordRequest("ZMQ-DEALER", path, 0, time.Since(start))
			return []reflect.Value{zeroResp, reflect.ValueOf(CallError{Err: fmt.Errorf("malformed reply: expected [delimiter, status, payload], got %d frame(s)", len(frames))}).Convert(errType)}
		}

		if string(frames[1]) == "error" {
			obs.RecordRequest("ZMQ-DEALER", path, 500, time.Since(start))
			// A matched ErrorPattern reply is 4-frame:
			// [delimiter, status, code, body]. The plain-text fallback
			// stays 3-frame: [delimiter, status, body].
			if len(frames) >= 4 {
				code := string(frames[2])
				body := frames[3]
				decodeResults := rv.MethodByName("DecodeErrorFor").Call([]reflect.Value{reflect.ValueOf(code), reflect.ValueOf(body)})
				errResp, _ := decodeResults[0].Interface().(reqreply.ErrorPatternResponse)
				matched, _ := decodeResults[1].Interface().(bool)
				decErr, _ := decodeResults[2].Interface().(error)
				if matched && decErr == nil {
					return []reflect.Value{zeroResp, reflect.ValueOf(CallError{Err: ErrorPatternResponse{
						Code: errResp.Code, Value: errResp.Value, Body: errResp.Body,
					}}).Convert(errType)}
				}
			}
			// UNCHANGED fallback — 3-frame reply, unmatched code, or
			// decode failed. frames[len(frames)-1] is the body in both
			// the 3-frame and unmatched-4-frame case.
			return []reflect.Value{zeroResp, reflect.ValueOf(CallError{Err: fmt.Errorf("server error: %s", frames[len(frames)-1])}).Convert(errType)}
		}

		if len(clientMiddlewareHandlers) > 0 {
			// zeromq has neither a reply-topic nor a property mechanism
			// (ROUTER/DEALER frames carry only [delimiter, status,
			// payload]) — both maps are always empty; mirrors the
			// identical ZMQ-REQ variant's call. mwErr is UNAMBIGUOUSLY an
			// Out-decode failure — reported as "middleware:out".
			if mwErr := reqreply.DispatchClientMiddlewareOut(nil, nil, clientMiddlewareHandlers); mwErr != nil {
				stats.ReportErrors(obs, "middleware:out", mwErr)
				obs.RecordRequest("ZMQ-DEALER", path, 0, time.Since(start))
				return []reflect.Value{zeroResp, reflect.ValueOf(CallError{Err: mwErr}).Convert(errType)}
			}
		}

		decodeResults := rv.MethodByName("DecodeResponseWithFormats").CallSlice([]reflect.Value{reflect.ValueOf(frames[2]), responseFormatsOverride})
		if errI, _ := decodeResults[1].Interface().(error); errI != nil {
			stats.ReportErrors(obs, "body", errI)
			obs.RecordRequest("ZMQ-DEALER", path, 0, time.Since(start))
			return []reflect.Value{zeroResp, reflect.ValueOf(CallError{Err: fmt.Errorf("decode response: %w", errI)}).Convert(errType)}
		}
		obs.RecordRequest("ZMQ-DEALER", path, 200, time.Since(start))
		return []reflect.Value{decodeResults[0], reflect.Zero(errType)}
	})

	wrappedCall := innerCall
	for i := len(clientImpls) - 1; i >= 0; i-- {
		if len(clientImpls[i].Satisfies) > 0 {
			continue
		}
		decoratorVal := reflect.ValueOf(clientImpls[i].Fn)
		wrappedCall = decoratorVal.Call([]reflect.Value{wrappedCall})[0]
	}

	finalResults := wrappedCall.Call([]reflect.Value{reflect.ValueOf(ctx), reqVal})
	if errI, _ := finalResults[1].Interface().(error); errI != nil {
		return nil, errI
	}
	return finalResults[0].Interface(), nil
}

// CallAsync implements [reqreply.ClientTransport]. Non-blocking
// counterpart to [dealerClientTransport.Call] — same mechanism as
// [clientTransport.CallAsync]. opts is [Call]'s identical per-call
// format-override parameter, applied inside the background goroutine.
func (t *dealerClientTransport) CallAsync(ctx context.Context, routeAny any, reqAny any, opts ...reqreply.ClientCallOptions) (any, error) {
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

var _ reqreply.ClientTransport = (*dealerClientTransport)(nil)
