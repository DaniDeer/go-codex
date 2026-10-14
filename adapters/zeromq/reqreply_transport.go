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
	"github.com/DaniDeer/go-codex/internal/middleware"
	"github.com/DaniDeer/go-codex/internal/route"
	"github.com/DaniDeer/go-codex/stats"
)

// reqreplyPkgPath is api/reqreply's import path — used to distinguish a
// genuine reqreply.Route[Req,Resp]/*reqreply.RouteHandle[Req,Resp] value
// (for ANY Req/Resp) from an unrelated/wrong-package value passed by
// caller mistake to [NewServerTransport]/[NewClientTransport]/[NewRouterServerTransport]/
// [NewDealerClientTransport]'s resulting [reqreply.ServerTransport]/
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
// to [NewServerTransport] — the declared dead-letter topic MUST have its own
// entry there (typically a PUSH socket feeding a dead-letter consumer)
// for a dead-letter to actually be reachable. When no such entry exists,
// this is a silent no-op (NOT a fallback onto the route's own REP
// socket — sending an extra, unsolicited message there would violate
// REQ/REP's strict one-reply-per-request protocol invariant). Returns
// true only when a dead-letter was both declared AND successfully routed
// to its own socket.
// properties is always passed as nil — Phase 2.0's own confirmed no-op
// (docs/design/d-0004-reqreply-workflow-simplification.md's "Addendum"): zeromq's
// reqreply transport has no property/header wire mechanism of its own
// to capture from, mirroring Phase 1.5's identical CallRaw no-op.
func tryDeadLetterReflect(
	sockets map[string]FramedSocket, deadLetterForMethod reflect.Value,
	obs stats.Observer, sourceTopic string, rawPayload []byte, err error,
) bool {
	var properties map[string]string
	results := deadLetterForMethod.Call([]reflect.Value{
		reflect.ValueOf(&obs).Elem(), reflect.ValueOf(sourceTopic), reflect.ValueOf(rawPayload), reflect.ValueOf(properties), reflect.ValueOf(&err).Elem(),
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

// sendForwardReplyReflect sends fwd's wire signal on a REQ/REP reply
// socket — docs/design/d-0004-reqreply-workflow-simplification.md's Phase 1 server-
// publish side. Mirrors [sendHandlerErrorReplyReflect]'s EXACT 3-frame
// shape (normal reply is 2-frame [status, payload]; a Forward reply is
// 3-frame [statusForward, targetTopic, payload]), swapping statusError+
// code for statusForward+TargetTopic — a Forward is a deliberate
// instruction, never routed through [sendHandlerErrorReplyReflect]'s own
// ErrorPattern/plain-text-fallback logic.
func sendForwardReplyReflect(sock FramedSocket, fwd reqreply.ForwardError) {
	_ = sock.SendFrames([][]byte{statusForward, []byte(fwd.TargetTopic), fwd.Payload})
}

// sendRouterForwardReplyReflect is [sendForwardReplyReflect]'s
// ROUTER-socket counterpart — preserves the identity frame, mirroring
// [sendRouterHandlerErrorReplyReflect]'s EXACT 4-frame shape ([identity,
// emptyDelimiter, statusForward, targetTopic, payload]).
func sendRouterForwardReplyReflect(sock FramedSocket, identity []byte, fwd reqreply.ForwardError) {
	_ = sock.SendFrames([][]byte{identity, emptyDelimiter, statusForward, []byte(fwd.TargetTopic), fwd.Payload})
}

// MissingSocketError is returned by [NewServerTransport]/[NewRouterServerTransport]
// when server has a route registered whose topic is not a key in the
// sockets map, and by [NewClientTransport]/[NewDealerClientTransport]'s resulting
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
func effectiveSecurity(elem reflect.Value) []reqreply.SecurityRequirement {
	reqs, _ := elem.FieldByName("Security").Interface().([]reqreply.SecurityRequirement)
	if reqs == nil {
		reqs, _ = elem.FieldByName("GlobalSecurity").Interface().([]reqreply.SecurityRequirement)
	}
	return reqs
}

// buildPairedSecurityFnType returns the expected paired security/
// credential Fn shape for a route whose decoded request type is reqType:
// func(context.Context, *Req, []reqreply.SecurityRequirement) error — used
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
			reflect.TypeOf([]reqreply.SecurityRequirement(nil)),
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
// [reqreply.ServerImplementation]'s Fn against the two shapes this
// package recognizes for THIS route's concrete Req/Resp types, EAGERLY
// at Serve construction time (once per route, not per message) —
// mirrors [adapters/mqtt5]'s identical eager-validation discipline.
func validateServerImplementationShapes(routeLabel string, impls []reqreply.ServerImplementation, securityFnType, generalDecoratorFnType reflect.Type) error {
	for _, impl := range impls {
		if impl.Fn == nil {
			continue
		}
		fnType := reflect.TypeOf(impl.Fn)
		if fnType == securityFnType || fnType == generalDecoratorFnType {
			continue
		}
		return reqreply.MiddlewareShapeError{
			Name:     impl.Name,
			Expected: "func(context.Context, *Req, []reqreply.SecurityRequirement) error or func(func(context.Context, Req) (Resp, error)) func(context.Context, Req) (Resp, error)",
			Got:      fmt.Sprintf("%T", impl.Fn),
		}
	}
	return nil
}

// validateClientImplementationShapes is
// [validateServerImplementationShapes]'s client-side mirror, for
// [reqreply.ClientImplementation] values.
func validateClientImplementationShapes(impls []reqreply.ClientImplementation, credentialFnType, generalDecoratorFnType reflect.Type) error {
	for _, impl := range impls {
		if impl.Fn == nil {
			continue
		}
		fnType := reflect.TypeOf(impl.Fn)
		if fnType == credentialFnType || fnType == generalDecoratorFnType {
			continue
		}
		return reqreply.MiddlewareShapeError{
			Name:     impl.Name,
			Expected: "func(context.Context, *Req, []reqreply.SecurityRequirement) error or func(func(context.Context, Req) (Resp, error)) func(context.Context, Req) (Resp, error)",
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
func runPairedServerSecurity(ctx context.Context, reqPtr reflect.Value, impls []reqreply.ServerImplementation, secReqs []reqreply.SecurityRequirement) error {
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
// mirror, for [reqreply.ClientImplementation] values — writes a
// credential field INTO reqPtr rather than merely reading it.
func runPairedClientCredential(ctx context.Context, reqPtr reflect.Value, impls []reqreply.ClientImplementation, secReqs []reqreply.SecurityRequirement) error {
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
func applyGeneralServerMiddleware(fnVal reflect.Value, impls []reqreply.ServerImplementation) reflect.Value {
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
	return fmt.Sprintf("zeromq: no socket registered for route topic %q (add it to the sockets map passed to NewServerTransport/NewClientTransport/NewRouterServerTransport/NewDealerClientTransport)", e.Topic)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e MissingSocketError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("topic", e.Topic),
	)
}

// ── REQ/REP server ───────────────────────────────────────────────────────

// serverTransport implements [reqreply.ServerTransport] for ZMQ REQ/REP
// sockets, wrapping a topic→socket map — built by [NewServerTransport]. A
// reflection shim (mirrors [adapters/mqtt5]'s identical technique): Go
// forbids generic methods, so Serve recovers the concrete Req/Resp types
// at runtime via reflection against the type-erased
// *reqreply.RouteHandle[Req,Resp] fields (Decode/Encode are struct
// FIELDS holding func values, not methods — reflect.Value.Call works
// identically either way).
//
// Capability parity (Phase 0 of
// docs/design/d-0004-reqreply-workflow-simplification.md's Addendum,
// SHIPPED, mirroring [adapters/mqtt5]'s identical shim): route-declared
// [reqreply.RouteHandle.RequestFormats]/[reqreply.RouteHandle.Formats]
// overrides and [reqreply.ErrorPattern]-typed error replies (via
// [reqreply.RouteHandle.ObserveErrorResponseFor]/[reqreply.RouteHandle.DeadLetterFor])
// are honored via [reqreply.RouteHandle.DecodeWithFormats]/
// [reqreply.RouteHandle.EncodeWithFormats], reached through reflection
// against rv — there is no remaining feature gap against a route
// registered directly through [reqreply.Server]/[reqreply.Server.Attach].
// No merge-field support is added (unlike mqtt5): zeromq's REQ/REP wire
// format carries no topic frame at all (routing is entirely
// socket-based, one socket per concrete topic, never a template), so
// [reqreply.NewTopicParam] merge-field decode was never applicable here.
//
// Serve BLOCKS until ctx is cancelled or a fatal socket error occurs —
// unlike [adapters/mqtt5]'s non-blocking Serve (which registers +
// returns immediately), ZMQ has no built-in dispatch loop of its own to
// delegate to, so this shim runs its own receive loop. This is the
// confirmed BLOCKING-transport branch
// [reqreply.Server.Serve]'s concurrent dispatch (one goroutine per
// route) is designed to accommodate.
type serverTransport struct {
	sockets map[string]FramedSocket
	opts    ServeOptions
}

// ServerTransportOptions configures [NewServerTransport] — the SOLE
// configuration surface for a zeromq REQ/REP [reqreply.ServerTransport]
// (docs/design/d-0006-protocol-native-capabilities.md's Phase 4d: a
// single Options struct, no positional params). Serve nests the
// pre-existing [ServeOptions] type unchanged.
type ServerTransportOptions struct {
	// Sockets maps each registered route's topic to the REP socket
	// handling it (REQ/REP is point-to-point, so one socket serves
	// exactly one route/topic — unlike pub/sub's topic-multiplexed SUB
	// socket). Required.
	Sockets map[string]FramedSocket
	// Serve configures per-route dispatch behavior. Optional (zero
	// value is a valid, fully-functional configuration).
	Serve ServeOptions
}

// NewServerTransport returns a [reqreply.ServerTransport] configured per
// opts — the adapter's ONLY job in the attach workflow (docs/design/
// d-0006-protocol-native-capabilities.md's Phase 4d): construct a
// fully-configured, attachable value. Attaching it is EXCLUSIVELY
// [reqreply.Server.Attach]'s job — there is no adapter-namespaced Attach
// function anymore (REMOVED, breaking, per that phase's explicit
// "zero backdoor between the api layer and the adapters" directive). The
// returned value also implements [reqreply.ServerAwareTransport] —
// [reqreply.Server.Attach] supplies the [*reqreply.Server] reference
// this shim needs for its eager [MissingSocketError] coverage check (a
// registered route whose topic has no entry in Sockets — checked
// upfront, at Attach time, not discovered later when a request for it
// arrives) via [serverTransport.BindServer], immediately after storing
// it.
//
//	server := reqreply.NewServer(reqreply.Info{Title: "Compute API", Version: "1.0.0"})
//	handle, _ := ComputeRoute.WithHandler(computeHandler).Register(server)
//	transport := zeromq.NewServerTransport(zeromq.ServerTransportOptions{Sockets: map[string]zeromq.FramedSocket{"compute/add": repSock}})
//	if err := server.Attach(transport); err != nil { ... }
//	err := server.Serve(ctx) // blocks, dispatching ComputeRoute (and every other registered route) concurrently
func NewServerTransport(opts ServerTransportOptions) reqreply.ServerTransport {
	return &serverTransport{sockets: opts.Sockets, opts: opts.Serve}
}

// BindServer implements [reqreply.ServerAwareTransport] — called by
// [reqreply.Server.Attach] immediately after storing t, running the
// EAGER [MissingSocketError] coverage check the OLD `AttachServer`
// convenience function used to run BEFORE calling `server.Attach`.
func (t *serverTransport) BindServer(s *reqreply.Server) error {
	for _, topic := range s.RegisteredTopics() {
		if _, ok := t.sockets[topic]; !ok {
			return MissingSocketError{Topic: topic}
		}
	}
	return nil
}

// Serve implements [reqreply.ServerTransport]. Runs a recv → decode →
// call fn → encode → send-reply loop via reflection against
// routeAny/fnAny — see [serverTransport]'s doc comment for this shim's
// shipped capability parity and blocking contract.
func (t *serverTransport) Serve(ctx context.Context, routeAny any, fnAny any) error {
	// docs/design/d-0007-declarative-middleware-layering.md's Rollout Phase C:
	// pre-allocates the shared ContextField box ONCE for this Serve
	// call's whole lifetime, reused across every received message
	// (mirrors events' adapters/zeromq's identical, already-proven-safe
	// pattern).
	ctx = middleware.EnsureContextFields(ctx)

	_, elem, err := recoverRouteHandleValue(routeAny)
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

	// reqreplyServerSetupCore is the ONE shared setup phase (Phase 4
	// events/reqreply family, Workstream C) — also used by
	// (*routerServerTransport).Serve. See its own doc comment for the
	// full unification rationale.
	setup, err := reqreplyServerSetupCore(routeAny, fnAny, sock, obs, t.opts.Capabilities)
	if err != nil {
		return err
	}

	if err := sock.SetRecvTimeout(recvPollInterval); err != nil {
		return SocketError{Op: "set_recv_timeout", Err: err}
	}

	sink := reqreplyReplySink{
		label: "ZMQ-REP",
		sendSuccess: func(respPayload []byte) error {
			return sock.SendFrames([][]byte{statusOK, respPayload})
		},
		sendError: func(ctx context.Context, err error) {
			sendHandlerErrorReplyReflect(ctx, sock, setup.observeErrorResponseForMethod, err, obs)
		},
		sendForward: func(fwd reqreply.ForwardError) {
			sendForwardReplyReflect(sock, fwd)
		},
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
		payload := frames[0]
		reqreplyServerDispatchOne(ctx, setup, sink, t.sockets, obs, payload, t.opts.OnError)
	}
}

var _ reqreply.ServerTransport = (*serverTransport)(nil)

// ── REQ/REP client ───────────────────────────────────────────────────────

// clientTransport implements [reqreply.ClientTransport] for ZMQ REQ
// sockets, wrapping a topic→socket map — built by [NewClientTransport].
// See [serverTransport]'s doc comment for this shim's identical
// reflection technique and shipped capability parity.
type clientTransport struct {
	sockets map[string]FramedSocket
	opts    CallOptions
}

// ClientTransportOptions configures [NewClientTransport] — the SOLE
// configuration surface for a zeromq REQ/REP [reqreply.ClientTransport]
// (docs/design/d-0006-protocol-native-capabilities.md's Phase 4d: a
// single Options struct, no positional params). Call nests the
// pre-existing [CallOptions] type unchanged.
type ClientTransportOptions struct {
	// Sockets maps each route's topic to the REQ socket used to call
	// it. Required.
	Sockets map[string]FramedSocket
	// Call configures per-call dispatch behavior. Optional (zero value
	// is a valid, fully-functional configuration).
	Call CallOptions
}

// NewClientTransport returns a [reqreply.ClientTransport] configured per
// opts — the adapter's ONLY job in the attach workflow (docs/design/
// d-0006-protocol-native-capabilities.md's Phase 4d): construct a
// fully-configured, attachable value. Attaching it is EXCLUSIVELY
// [reqreply.Client.Attach]'s job — there is no adapter-namespaced Attach
// function anymore (REMOVED, breaking, per that phase's explicit
// "zero backdoor between the api layer and the adapters" directive). A
// call for a route whose topic has no entry in Sockets returns
// [MissingSocketError] at call time (no upfront coverage check on the
// client side — unlike [NewServerTransport], the client may only ever
// need a subset of the server's registered routes, so no
// [reqreply.ClientAwareTransport]-style bind hook is needed here).
//
//	client := reqreply.NewClient()
//	transport := zeromq.NewClientTransport(zeromq.ClientTransportOptions{Sockets: map[string]zeromq.FramedSocket{"compute/add": reqSock}})
//	if err := client.Attach(transport); err != nil { ... }
//	respAny, err := client.Call(ctx, ComputeRoute, ComputeReq{X: 1, Y: 2})
func NewClientTransport(opts ClientTransportOptions) reqreply.ClientTransport {
	return &clientTransport{sockets: opts.Sockets, opts: opts.Call}
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
	// docs/design/d-0007-declarative-middleware-layering.md's Rollout Phase C:
	// pre-allocates the shared ContextField box for THIS call.
	ctx = middleware.EnsureContextFields(ctx)
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

	// docs/design/d-0006-protocol-native-capabilities.md's Phase 2: apply
	// this call's supplied [CallOptions.Capabilities] to the socket via
	// the EXISTING [applyCapabilities] helper (re-application on every
	// call is idempotent — a minor, accepted inefficiency, not a
	// correctness issue).
	applyCapabilities(sock, t.opts.Capabilities, obs, path)
	// Client-side Tier 1 coverage check — deep-dive review round fix:
	// closes the "No coverage check on the client/Call side" gap this
	// file's own comment used to flag — mirrors api/events' identical,
	// previously-confirmed publish-side capability-coverage gap
	// (Round 178) and adapters/mqtt5's identical fix.
	if requirements, ok := elem.FieldByName("Requirements").Interface().([]reqreply.CapabilityRequirement); ok {
		if covErr := reqreply.VerifyCapabilityCoverage(path, requirements, t.opts.Capabilities); covErr != nil {
			obs.RecordRequest("ZMQ-REQ", path, 0, time.Since(start))
			return nil, covErr
		}
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
	clientImpls, _ := elem.FieldByName("ClientImplementations").Interface().([]reqreply.ClientImplementation)
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
				// A FAILING handler whose OWN Satisfies is non-empty (a
				// Security-carrying ClientMiddlewareHandler, confirmed
				// via Name lookup) keeps Security's own, DISTINCT
				// fallback (reqreply.SecurityError), mirroring the
				// SERVER-side isSecuritySatisfyingHandler precedent
				// (reqreply.IsSecuritySatisfyingHandler has no
				// ClientMiddlewareHandler-typed equivalent, so checked
				// inline here).
				effErr := mwErr
				if me, ok := mwErr.(reqreply.MiddlewareError); ok && isFnErr {
					for _, h := range clientMiddlewareHandlers {
						if h.Name == mwName && len(h.Satisfies) > 0 {
							if secObs, ok := obs.(stats.SecurityObserver); ok {
								secObs.RecordSecurityRejection(path, route.FirstSchemeName(secReqs))
							}
							effErr = reqreply.SecurityError{Err: me.Err}
							break
						}
					}
				}
				_ = mwName
				return []reflect.Value{zeroResp, reflect.ValueOf(CallError{Err: effErr}).Convert(errType)}
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

		// Dispatch ordering: a Forward reply (frames[0] == statusForward)
		// is recognized BEFORE an ErrorPattern reply — docs/design/d-0004-reqreply-workflow-simplification.md's documented "Forward recognized
		// before ErrorPattern" precedence, mirroring [rest.RedirectError]'s
		// identical rule. A Forward reply is 3-frame: [statusForward,
		// targetTopic, payload] — the SAME shape as the matched-
		// ErrorPattern 3-frame reply, 2nd frame reused as the target
		// topic instead of a pattern code.
		if string(frames[0]) == string(statusForward) && len(frames) >= 3 {
			// RecordRequest status choice: 200, NOT 500 — unlike an
			// error reply, a Forward is NOT a failure of THIS round
			// trip; it's a deliberate instruction the caller's own
			// [api/reqreply.Client] chain-following loop (a future
			// phase) is expected to act on successfully.
			obs.RecordRequest("ZMQ-REQ", path, 200, time.Since(start))
			return []reflect.Value{zeroResp, reflect.ValueOf(reqreply.ForwardError{
				TargetTopic: string(frames[1]), Payload: frames[2],
			}).Convert(errType)}
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
			if mwErr := reqreply.DispatchClientMiddlewareOut(ctx, nil, nil, clientMiddlewareHandlers); mwErr != nil {
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

// CallRaw implements [reqreply.ClientTransport]. docs/design/d-0004-reqreply-workflow-simplification.md Phase 0 infrastructure — sends payload
// VERBATIM (no route/Req/Resp reflection, unlike [call]) on topic's
// registered socket and awaits exactly one reply, reusing the SAME
// send/recv-poll mechanics [call] does, generalized to not need a route.
//
// Classifies the reply the SAME way [call] does for its OWN ordinary/
// error/forward legs: an ordinary 2-frame `[status, payload]` reply
// returns frames[1]; a `frames[0] == statusForward` reply (3-frame
// `[statusForward, targetTopic, payload]`, checked BEFORE the ErrorPattern
// leg below — docs/design/d-0004-reqreply-workflow-simplification.md's documented
// "Forward recognized before ErrorPattern" precedence) returns
// [reqreply.ForwardError{TargetTopic, Payload}] directly — unlike the
// ErrorPattern leg, a Forward needs no route-dependent decode step, so
// CallRaw returns the SAME typed value [call] does, not a raw/deferred
// variant; a `frames[0] == "error"` reply returns
// [reqreply.ForwardRawErrorReply{Code, Body}] — the RAW wire-level code
// (present on a 3-frame `[status, code, body]` reply, "" on the 2-frame
// plain-text fallback) and body, deliberately WITHOUT attempting
// [call]'s own matched-code DECODE (which needs a *reqreply.RouteHandle
// CallRaw never receives). A caller holding the REGISTRY-resolved
// target RouteHandle (e.g. [Client.followForward]'s/[Client.
// ReplayDeadLetter]'s own chain-following loop) finishes that decode
// itself via RouteHandle.DecodeErrorFor(Code, Body) — moving the "hard
// part" to api/reqreply, keeping this adapter method a pure wire-level
// extraction primitive.
//
// properties (Phase 1.5, docs/design/d-0004-reqreply-workflow-simplification.md's own
// "Addendum" section) is accepted for [reqreply.ClientTransport]
// interface symmetry ONLY — a confirmed, deliberate NO-OP here: zeromq's
// reqreply transport has no property/header mechanism of its own (no
// frame reserved for it, unlike mqtt5's User Properties), so there is
// nowhere on the wire to put it. This is NOT a partial implementation.
func (t *clientTransport) CallRaw(ctx context.Context, topic string, payload []byte, properties map[string]string) (result []byte, err error) {
	obs := t.opts.Observer
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}
	start := time.Now()

	// TraceObserver span — mirrors [call]'s identical convention (deep-
	// dive-bug-hunt finding: CallRaw originally omitted this, the ONLY
	// divergence from Call's own observability contract found during
	// the api/reqreply-focused round covering this new Phase 0
	// primitive).
	if to, ok := obs.(stats.TraceObserver); ok {
		ctx = to.StartSpan(ctx, "zmq.request", topic)
		defer func() { to.EndSpan(ctx, err) }()
	}

	sock, ok := t.sockets[topic]
	if !ok {
		obs.RecordRequest("ZMQ-REQ", topic, 0, time.Since(start))
		return nil, MissingSocketError{Topic: topic}
	}

	// docs/design/d-0006-protocol-native-capabilities.md's Phase 2:
	// apply this call's CallOptions.Capabilities to the socket, mirroring
	// [call]'s identical call — deep-dive-bug-hunt finding: CallRaw
	// originally omitted this (a SEPARATE dispatch implementation from
	// call(), so applying capabilities there does NOT cover this path).
	// HWM/Conflate are socket-level, route-independent configuration —
	// a caller configuring them expects them honored regardless of
	// whether the call goes through Call or CallRaw.
	applyCapabilities(sock, t.opts.Capabilities, obs, topic)

	if sendErr := sock.SendFrames([][]byte{payload}); sendErr != nil {
		obs.RecordRequest("ZMQ-REQ", topic, 0, time.Since(start))
		return nil, CallError{Err: fmt.Errorf("send: %w", sendErr)}
	}

	if err := sock.SetRecvTimeout(recvPollInterval); err != nil {
		return nil, CallError{Err: fmt.Errorf("set recv timeout: %w", err)}
	}

	var frames [][]byte
	for {
		select {
		case <-ctx.Done():
			obs.RecordRequest("ZMQ-REQ", topic, 0, time.Since(start))
			return nil, CallError{Err: ctx.Err()}
		default:
		}
		var recvErr error
		frames, recvErr = sock.RecvFrames()
		if errors.Is(recvErr, ErrTimeout) {
			continue
		}
		if recvErr != nil {
			obs.RecordRequest("ZMQ-REQ", topic, 0, time.Since(start))
			return nil, CallError{Err: fmt.Errorf("recv: %w", recvErr)}
		}
		break
	}

	if len(frames) < 2 {
		obs.RecordRequest("ZMQ-REQ", topic, 0, time.Since(start))
		return nil, CallError{Err: fmt.Errorf("malformed reply: expected [status, payload], got %d frame(s)", len(frames))}
	}

	// Dispatch ordering: a Forward reply (frames[0] == statusForward) is
	// recognized BEFORE an ErrorPattern reply — mirrors [call]'s
	// identical "Forward recognized before ErrorPattern" precedence. A
	// Forward reply is 3-frame: [statusForward, targetTopic, payload] —
	// returned directly as [reqreply.ForwardError] (route-free, same as
	// [call]'s own return — unlike the ErrorPattern leg below, which has
	// to defer decoding to a future caller via
	// [reqreply.ForwardRawErrorReply], a Forward's TargetTopic/Payload
	// need no route-dependent decode step at all).
	if string(frames[0]) == string(statusForward) && len(frames) >= 3 {
		obs.RecordRequest("ZMQ-REQ", topic, 200, time.Since(start))
		return nil, reqreply.ForwardError{TargetTopic: string(frames[1]), Payload: frames[2]}
	}

	if string(frames[0]) == "error" {
		obs.RecordRequest("ZMQ-REQ", topic, 500, time.Since(start))
		// Extracts the RAW code (route-free — a 3-frame
		// [status,code,body] reply) when present, returning it alongside
		// the body via reqreply.ForwardRawErrorReply, deliberately
		// WITHOUT attempting the route-dependent DecodeErrorFor decode
		// call() does — CallRaw has no route. A caller holding the
		// registry-resolved target *RouteHandle (Phase 1/2's own
		// chain-following loop) finishes that decode itself. The
		// plain-text fallback stays 2-frame ([status,body]), Code "".
		var code string
		if len(frames) >= 3 {
			code = string(frames[1])
		}
		return nil, reqreply.ForwardRawErrorReply{Code: code, Body: frames[len(frames)-1]}
	}

	obs.RecordRequest("ZMQ-REQ", topic, 200, time.Since(start))
	return frames[1], nil
}

// CallAsync is REMOVED from [reqreply.ClientTransport] — docs/design/d-0004-reqreply-workflow-simplification.md's "CallAsync/Future" section (traced
// against the real shipped code and found the adapter's own background
// goroutine resolved the Future entirely INSIDE the adapter, before
// [reqreply.Client] could ever intercept a chained forward reply).
// [reqreply.Client.CallAsync] now wraps [reqreply.Client.Call] directly
// instead — this adapter needs no CallAsync method at all, only [call]
// (shared by both [clientTransport.Call] and [clientTransport.CallRaw]).

var _ reqreply.ClientTransport = (*clientTransport)(nil)

// ── ROUTER/DEALER server ─────────────────────────────────────────────────

// routerServerTransport implements [reqreply.ServerTransport] for ZMQ
// ROUTER sockets, wrapping a topic→socket map — built by
// [NewRouterServerTransport]. Mirrors [serverTransport]'s reflection
// technique and shipped capability parity; dispatches each request in
// its own goroutine, preserving the identity frame so the reply reaches
// the correct DEALER peer.
type routerServerTransport struct {
	sockets map[string]FramedSocket
	opts    ServeOptions
}

// RouterServerTransportOptions configures [NewRouterServerTransport] —
// the SOLE configuration surface for a zeromq ROUTER
// [reqreply.ServerTransport] (docs/design/
// d-0006-protocol-native-capabilities.md's Phase 4d: a single Options
// struct, no positional params). Serve nests the pre-existing
// [ServeOptions] type unchanged.
type RouterServerTransportOptions struct {
	// Sockets maps each registered route's topic to the ROUTER socket
	// handling it. Required.
	Sockets map[string]FramedSocket
	// Serve configures per-route dispatch behavior. Optional (zero
	// value is a valid, fully-functional configuration).
	Serve ServeOptions
}

// NewRouterServerTransport returns a [reqreply.ServerTransport]
// configured per opts — the ROUTER/DEALER counterpart of
// [NewServerTransport]; see that function's doc comment for the full
// attach-workflow rationale (docs/design/
// d-0006-protocol-native-capabilities.md's Phase 4d). The returned value
// also implements [reqreply.ServerAwareTransport] — see
// [routerServerTransport.BindServer].
//
//	transport := zeromq.NewRouterServerTransport(zeromq.RouterServerTransportOptions{Sockets: map[string]zeromq.FramedSocket{"compute/add": routerSock}})
//	if err := server.Attach(transport); err != nil { ... }
func NewRouterServerTransport(opts RouterServerTransportOptions) reqreply.ServerTransport {
	return &routerServerTransport{sockets: opts.Sockets, opts: opts.Serve}
}

// BindServer implements [reqreply.ServerAwareTransport] — mirrors
// [serverTransport.BindServer]'s identical eager [MissingSocketError]
// coverage check.
func (t *routerServerTransport) BindServer(s *reqreply.Server) error {
	for _, topic := range s.RegisteredTopics() {
		if _, ok := t.sockets[topic]; !ok {
			return MissingSocketError{Topic: topic}
		}
	}
	return nil
}

// Serve implements [reqreply.ServerTransport]. Mirrors the former,
// now-deleted ServeRouter's core receive loop (recv identity+payload → dispatch to its own
// goroutine → decode → call fn → encode → send identity-addressed
// reply) via reflection against routeAny/fnAny.
func (t *routerServerTransport) Serve(ctx context.Context, routeAny any, fnAny any) error {
	// docs/design/d-0007-declarative-middleware-layering.md's Rollout Phase C:
	// pre-allocates the shared ContextField box ONCE for this Serve
	// call's whole lifetime, reused across every received message.
	ctx = middleware.EnsureContextFields(ctx)

	_, elem, err := recoverRouteHandleValue(routeAny)
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

	// reqreplyServerSetupCore is the ONE shared setup phase (Phase 4
	// events/reqreply family, Workstream C) — also used by
	// (*serverTransport).Serve. See its own doc comment for the full
	// unification rationale.
	setup, err := reqreplyServerSetupCore(routeAny, fnAny, sock, obs, t.opts.Capabilities)
	if err != nil {
		return err
	}

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
			// sink is built PER REQUEST (unlike serverTransport.Serve's
			// once-per-Serve-call sink) since the ROUTER variant's reply
			// framing needs this specific request's identity frame.
			sink := reqreplyReplySink{
				label: "ZMQ-ROUTER",
				sendSuccess: func(respPayload []byte) error {
					return sock.SendFrames([][]byte{id, emptyDelimiter, statusOK, respPayload})
				},
				sendError: func(ctx context.Context, err error) {
					sendRouterHandlerErrorReplyReflect(ctx, sock, id, setup.observeErrorResponseForMethod, err, obs)
				},
				sendForward: func(fwd reqreply.ForwardError) {
					sendRouterForwardReplyReflect(sock, id, fwd)
				},
			}
			reqreplyServerDispatchOne(ctx, setup, sink, t.sockets, obs, pl, t.opts.OnError)
		}(identity, payload)
	}
}

var _ reqreply.ServerTransport = (*routerServerTransport)(nil)

// ── DEALER client ────────────────────────────────────────────────────────

// dealerClientTransport implements [reqreply.ClientTransport] for ZMQ
// DEALER sockets, wrapping a topic→socket map — built by
// [NewDealerClientTransport]. Mirrors the former, now-deleted CallDealer's
// envelope framing
// (empty delimiter + payload).
type dealerClientTransport struct {
	sockets map[string]FramedSocket
	opts    CallOptions
}

// DealerClientTransportOptions configures [NewDealerClientTransport] —
// the SOLE configuration surface for a zeromq DEALER
// [reqreply.ClientTransport] (docs/design/
// d-0006-protocol-native-capabilities.md's Phase 4d: a single Options
// struct, no positional params). Call nests the pre-existing
// [CallOptions] type unchanged.
type DealerClientTransportOptions struct {
	// Sockets maps each route's topic to the DEALER socket used to call
	// it. Required.
	Sockets map[string]FramedSocket
	// Call configures per-call dispatch behavior. Optional (zero value
	// is a valid, fully-functional configuration).
	Call CallOptions
}

// NewDealerClientTransport returns a [reqreply.ClientTransport]
// configured per opts — the ROUTER/DEALER counterpart of
// [NewClientTransport]; see that function's doc comment for the full
// attach-workflow rationale (docs/design/
// d-0006-protocol-native-capabilities.md's Phase 4d).
//
//	client := reqreply.NewClient()
//	transport := zeromq.NewDealerClientTransport(zeromq.DealerClientTransportOptions{Sockets: map[string]zeromq.FramedSocket{"compute/add": dealerSock}})
//	if err := client.Attach(transport); err != nil { ... }
func NewDealerClientTransport(opts DealerClientTransportOptions) reqreply.ClientTransport {
	return &dealerClientTransport{sockets: opts.Sockets, opts: opts.Call}
}

// Call implements [reqreply.ClientTransport]. Mirrors the former,
// now-deleted CallDealer's core logic (encode → send with empty-delimiter envelope → await reply →
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
	// docs/design/d-0007-declarative-middleware-layering.md's Rollout Phase C:
	// pre-allocates the shared ContextField box for THIS call.
	ctx = middleware.EnsureContextFields(ctx)
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

	// docs/design/d-0006-protocol-native-capabilities.md's Phase 2: same
	// mechanism as [clientTransport.call], duplicated for the DEALER
	// variant (see the established 4-transport duplication precedent
	// cited on [routerServerTransport.Serve]).
	applyCapabilities(sock, t.opts.Capabilities, obs, path)
	// Client-side Tier 1 coverage check — deep-dive review round fix,
	// duplicated for the DEALER variant — see [clientTransport.call]'s
	// identical fix.
	if requirements, ok := elem.FieldByName("Requirements").Interface().([]reqreply.CapabilityRequirement); ok {
		if covErr := reqreply.VerifyCapabilityCoverage(path, requirements, t.opts.Capabilities); covErr != nil {
			obs.RecordRequest("ZMQ-DEALER", path, 0, time.Since(start))
			return nil, covErr
		}
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
	// resolved above; a BuildTopic failure IS FATAL, matching the former,
	// now-deleted CallDealer escape hatch exactly).
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
	clientImpls, _ := elem.FieldByName("ClientImplementations").Interface().([]reqreply.ClientImplementation)
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
				// A FAILING handler whose OWN Satisfies is non-empty (a
				// Security-carrying ClientMiddlewareHandler, confirmed
				// via Name lookup) keeps Security's own, DISTINCT
				// fallback (reqreply.SecurityError), mirroring the
				// SERVER-side isSecuritySatisfyingHandler precedent
				// (reqreply.IsSecuritySatisfyingHandler has no
				// ClientMiddlewareHandler-typed equivalent, so checked
				// inline here).
				effErr := mwErr
				if me, ok := mwErr.(reqreply.MiddlewareError); ok && isFnErr {
					for _, h := range clientMiddlewareHandlers {
						if h.Name == mwName && len(h.Satisfies) > 0 {
							if secObs, ok := obs.(stats.SecurityObserver); ok {
								secObs.RecordSecurityRejection(path, route.FirstSchemeName(secReqs))
							}
							effErr = reqreply.SecurityError{Err: me.Err}
							break
						}
					}
				}
				_ = mwName
				return []reflect.Value{zeroResp, reflect.ValueOf(CallError{Err: effErr}).Convert(errType)}
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

		// Dispatch ordering: a Forward reply (frames[1] == statusForward)
		// is recognized BEFORE an ErrorPattern reply — see
		// [clientTransport.call]'s identical check for the full
		// rationale. A Forward reply is 4-frame: [delimiter,
		// statusForward, targetTopic, payload] — the SAME shape as the
		// matched-ErrorPattern 4-frame reply, 3rd frame reused as the
		// target topic instead of a pattern code.
		if string(frames[1]) == string(statusForward) && len(frames) >= 4 {
			// RecordRequest status choice: 200 — see
			// [clientTransport.call]'s identical rationale.
			obs.RecordRequest("ZMQ-DEALER", path, 200, time.Since(start))
			return []reflect.Value{zeroResp, reflect.ValueOf(reqreply.ForwardError{
				TargetTopic: string(frames[2]), Payload: frames[3],
			}).Convert(errType)}
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
			if mwErr := reqreply.DispatchClientMiddlewareOut(ctx, nil, nil, clientMiddlewareHandlers); mwErr != nil {
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

// CallRaw implements [reqreply.ClientTransport]. docs/design/d-0004-reqreply-workflow-simplification.md Phase 0 infrastructure — mirrors
// [clientTransport.CallRaw] exactly, adapted to DEALER's envelope
// framing (empty delimiter + payload out; [delimiter, status, payload]
// in) the SAME way [dealerClientTransport.call] adapts [clientTransport.
// call]'s REQ/REP framing, including the Forward leg (4-frame
// [delimiter, statusForward, targetTopic, payload], checked before the
// `frames[1] == "error"` check below). See [clientTransport.CallRaw]'s
// doc comment for the full classification contract AND for properties'
// confirmed, deliberate no-op rationale (Phase 1.5) — identical here.
func (t *dealerClientTransport) CallRaw(ctx context.Context, topic string, payload []byte, properties map[string]string) (result []byte, err error) {
	obs := t.opts.Observer
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}
	start := time.Now()

	// TraceObserver span — mirrors [call]'s identical convention (deep-
	// dive-bug-hunt finding: CallRaw originally omitted this, the ONLY
	// divergence from Call's own observability contract found during
	// the api/reqreply-focused round covering this new Phase 0
	// primitive).
	if to, ok := obs.(stats.TraceObserver); ok {
		ctx = to.StartSpan(ctx, "zmq.request", topic)
		defer func() { to.EndSpan(ctx, err) }()
	}

	sock, ok := t.sockets[topic]
	if !ok {
		obs.RecordRequest("ZMQ-DEALER", topic, 0, time.Since(start))
		return nil, MissingSocketError{Topic: topic}
	}

	// docs/design/d-0006-protocol-native-capabilities.md's Phase 2:
	// apply this call's CallOptions.Capabilities to the socket, mirroring
	// [call]'s identical call — deep-dive-bug-hunt finding: CallRaw
	// originally omitted this (a SEPARATE dispatch implementation from
	// call(), so applying capabilities there does NOT cover this path).
	applyCapabilities(sock, t.opts.Capabilities, obs, topic)

	if sendErr := sock.SendFrames([][]byte{emptyDelimiter, payload}); sendErr != nil {
		obs.RecordRequest("ZMQ-DEALER", topic, 0, time.Since(start))
		return nil, CallError{Err: fmt.Errorf("send: %w", sendErr)}
	}

	if err := sock.SetRecvTimeout(recvPollInterval); err != nil {
		return nil, CallError{Err: fmt.Errorf("set recv timeout: %w", err)}
	}

	var frames [][]byte
	for {
		select {
		case <-ctx.Done():
			obs.RecordRequest("ZMQ-DEALER", topic, 0, time.Since(start))
			return nil, CallError{Err: ctx.Err()}
		default:
		}
		var recvErr error
		frames, recvErr = sock.RecvFrames()
		if errors.Is(recvErr, ErrTimeout) {
			continue
		}
		if recvErr != nil {
			obs.RecordRequest("ZMQ-DEALER", topic, 0, time.Since(start))
			return nil, CallError{Err: fmt.Errorf("recv: %w", recvErr)}
		}
		break
	}

	// Expect [delimiter, status, payload].
	if len(frames) < 3 {
		obs.RecordRequest("ZMQ-DEALER", topic, 0, time.Since(start))
		return nil, CallError{Err: fmt.Errorf("malformed reply: expected [delimiter, status, payload], got %d frame(s)", len(frames))}
	}

	// Dispatch ordering: a Forward reply (frames[1] == statusForward) is
	// recognized BEFORE an ErrorPattern reply — mirrors
	// [clientTransport.CallRaw]'s identical precedence/rationale. A
	// Forward reply is 4-frame: [delimiter, statusForward, targetTopic,
	// payload].
	if string(frames[1]) == string(statusForward) && len(frames) >= 4 {
		obs.RecordRequest("ZMQ-DEALER", topic, 200, time.Since(start))
		return nil, reqreply.ForwardError{TargetTopic: string(frames[2]), Payload: frames[3]}
	}

	if string(frames[1]) == "error" {
		obs.RecordRequest("ZMQ-DEALER", topic, 500, time.Since(start))
		// Extracts the RAW code (route-free — a 4-frame
		// [delimiter,status,code,body] reply) when present, returning it
		// alongside the body via reqreply.ForwardRawErrorReply,
		// deliberately WITHOUT attempting the route-dependent
		// DecodeErrorFor decode call() does — CallRaw has no route. A
		// caller holding the registry-resolved target *RouteHandle
		// (Phase 1/2's own chain-following loop) finishes that decode
		// itself. The plain-text fallback stays 3-frame
		// ([delimiter,status,body]), Code "".
		var code string
		if len(frames) >= 4 {
			code = string(frames[2])
		}
		return nil, reqreply.ForwardRawErrorReply{Code: code, Body: frames[len(frames)-1]}
	}

	obs.RecordRequest("ZMQ-DEALER", topic, 200, time.Since(start))
	return frames[2], nil
}

// CallAsync is REMOVED from [reqreply.ClientTransport] — see
// [clientTransport]'s identical doc comment above its own now-removed
// CallAsync for the full rationale.

var _ reqreply.ClientTransport = (*dealerClientTransport)(nil)
