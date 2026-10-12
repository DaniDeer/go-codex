package reqreply

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"

	"github.com/DaniDeer/go-codex/internal/route"
)

// Forward resolves target's topic template against vars (via
// target.ClientHandle(opts...).BuildTopic) and returns a single error a
// handler returns as its own error value: the [ForwardError] itself on
// success, or a typed [ForwardTargetVarError] on failure — mirrors
// [rest.Redirect]'s identical "single sentinel error return" shape
// (docs/roadmap/reqreply-forward-bounce.md's "API surface" section).
//
// Unlike [rest.Redirect], Forward has no "from" parameter and no status
// code: reqreply's pub/sub round trip has no HTTP-style redirect-family
// status, and target's Req/Resp types are BOTH free to differ from the
// originating route's (not constrained the way HTTP 307/308 constrains
// method/body preservation) — newReq is the brand new request value the
// handler constructs explicitly for target.
//
// properties (Phase 1.5, docs/roadmap/reqreply-forward-bounce.md's own
// "Addendum" section) carries wire-level properties/headers (e.g. an
// mqtt5 User Property delivering a credential) the handler wants carried
// through to target — nil when there are none. EXPLICIT, not derived
// from any ClientMW/context-ambient mechanism: Forward is called from
// INSIDE a server handler, which has no *Client/ClientTransport to
// dispatch target's own declared ClientMW against, so there is nothing
// to derive this from automatically — this is the "hard part" moved to
// the caller's own declaration, matching this library's general
// declarative, explicit-over-magic philosophy.
//
// opts is passed straight through to target.ClientHandle(opts...) —
// pass [WithRouter] when target is (or will be) grouped under a
// [Router], the same way a client-side caller of target would need to.
//
//	func(ctx context.Context, req ComputeReq) (ComputeResp, error) {
//	    var zero ComputeResp
//	    return zero, reqreply.Forward(sumRoute, SumReq{A: req.X, B: req.Y}, nil, nil)
//	}
func Forward[TargetReq, TargetResp any](
	target Route[TargetReq, TargetResp],
	newReq TargetReq,
	vars map[string]string,
	properties map[string]string,
	opts ...ClientHandleOpt,
) error {
	handle := target.ClientHandle(opts...)
	topic, err := handle.BuildTopic(vars)
	if err != nil {
		return ForwardTargetVarError{Topic: handle.Topic, Err: err}
	}
	payload, err := handle.EncodeRequestWithFormats(newReq)
	if err != nil {
		return ForwardTargetVarError{Topic: handle.Topic, Err: err}
	}
	return ForwardError{TargetTopic: topic, Payload: payload, Properties: properties}
}

// ForwardError signals that a handler wants the CALLER'S client to
// republish to another registered route within the SAME correlation-ID
// round trip, instead of returning a normal response body — pub/sub has
// no broker-level "redirect" the way HTTP does, so the SERVER signals
// "ask topic X instead" and the CLIENT does the actual re-publish (see
// docs/roadmap/reqreply-forward-bounce.md's "Why this exists").
// Returned as the error value from a handler — recognized by each
// adapter's `call()`/`CallRaw()` dispatch via a reserved wire-level
// signal (mqtt5's ContentType+User Property; zeromq's frames[0]),
// BEFORE [ErrorPattern] matching, mirroring [rest.RedirectError]'s
// identical "recognized before ErrorPattern/ErrorStatus" precedent.
//
// Construct via [Forward] — never directly.
type ForwardError struct {
	// TargetTopic is the already-BuildTopic-resolved CONCRETE topic
	// (not the template).
	TargetTopic string
	// Payload is newReq, already encoded via target's own request codec
	// — the reply body legitimately carries this (unlike an initial
	// "body stays empty" read, which would only have held if Req types
	// had been constrained to match).
	Payload []byte
	// Properties carries wire-level properties/headers (e.g. an mqtt5
	// User Property delivering a credential) the handler explicitly
	// supplied to [Forward] — nil when there are none. Threaded through
	// to [ClientTransport.CallRaw]'s own properties parameter by
	// [Client.followForward] at every hop (Phase 1.5,
	// docs/roadmap/reqreply-forward-bounce.md's "Addendum").
	Properties map[string]string
}

func (e ForwardError) Error() string {
	return fmt.Sprintf("api/reqreply: forward to %q", e.TargetTopic)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e ForwardError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("target_topic", e.TargetTopic),
		slog.Int("payload_len", len(e.Payload)),
		slog.Int("property_count", len(e.Properties)),
	)
}

// ForwardTargetVarError is returned by [Forward] when a var required by
// target's own topic template is missing, or fails target's own codec
// constraint, or newReq fails target's own request codec — wraps
// [RouteHandle.BuildTopic]'s/[RouteHandle.EncodeRequestWithFormats]'s
// already-typed error ([RouteParamError]/[MissingRouteParamError]/
// [InvalidTopicError]) the SAME way [rest.RedirectTargetVarError] wraps
// [RouteHandle.BuildPath]'s own already-typed error — verified during
// this design's in-depth review not to be redundant over-wrapping.
type ForwardTargetVarError struct {
	Topic string // the target route's (unresolved) topic template
	Err   error
}

func (e ForwardTargetVarError) Error() string {
	return fmt.Sprintf("api/reqreply: forward target %q: %v", e.Topic, e.Err)
}

// Unwrap allows [errors.Is]/[errors.As] to reach the underlying error.
func (e ForwardTargetVarError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e ForwardTargetVarError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("topic", e.Topic),
		slog.Any("err", e.Err),
	)
}

// UnrecognizedForwardError is returned by [Client.Call]/[Client.
// CallAsync] when a received [ForwardError]'s TargetTopic does not match
// any route registered on the [*Client] (via a prior [Client.Call]/
// [Client.CallAsync]/[Client.RegisterRoute]) — mirrors [rest.
// UnrecognizedRedirectError]'s identical rationale: carries the raw
// TargetTopic so a caller can fall back to a manual follow-up (e.g.
// their own [CallWithTransport]).
type UnrecognizedForwardError struct {
	TargetTopic string
}

func (e UnrecognizedForwardError) Error() string {
	return fmt.Sprintf("api/reqreply: forward to %q does not match any route registered on this Client", e.TargetTopic)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e UnrecognizedForwardError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("target_topic", e.TargetTopic),
	)
}

// ForwardChainTooDeepError is returned by [Client.Call]/[Client.
// CallAsync] when auto-following a forward chain exceeds
// [ClientCallOptions.MaxForwards] — covers both unreasonably long chains
// and cycles (A forwards to B forwards to A) with one mechanism, no
// separate cycle detector. Direct port of [rest.RedirectChainTooDeepError]'s
// identical mechanics/rationale.
type ForwardChainTooDeepError struct {
	TargetTopic string
	Depth       int
}

func (e ForwardChainTooDeepError) Error() string {
	return fmt.Sprintf("api/reqreply: forward chain exceeded depth %d at %q", e.Depth, e.TargetTopic)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e ForwardChainTooDeepError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("target_topic", e.TargetTopic),
		slog.Int("depth", e.Depth),
	)
}

// ForwardSecuritySchemeMismatchError is returned by [Client.Call]/
// [Client.CallAsync] when a forward's target route declares a security
// scheme that does not match the originating route's own declared
// scheme — a GENUINE DIVERGENCE from REST's own more permissive
// behavior (docs/roadmap/reqreply-forward-bounce.md's "Security-scheme
// mismatch check"): because a forward's target can be an ENTIRELY
// DIFFERENT route in an entirely different trust/feature domain (Req/
// Resp are free to differ, unlike HTTP 307/308), a genuine cross-domain
// scheme mismatch is more likely here than for REST's own redirect
// targets (which in practice stay within the same resource). REST
// instead discovers a mismatch only AFTER sending, over the wire,
// surfacing as the target's own ordinary rejection.
//
// This is scheme-NAME comparison only, never a value/credential
// comparison: [api/reqreply.Client] — never the adapter — never
// inspects or re-derives the actual credential from the target, it only
// confirms the SHAPE of what it's about to reuse is declared-compatible.
// A declared-compatible scheme whose ACTUAL credential the target still
// rejects (e.g. an expired token) still surfaces as an ordinary
// target-side rejection, same as REST — this check only catches the
// STRUCTURAL mismatch class.
type ForwardSecuritySchemeMismatchError struct {
	OriginatingScheme string
	TargetScheme      string
}

func (e ForwardSecuritySchemeMismatchError) Error() string {
	return fmt.Sprintf("api/reqreply: forward target's security scheme %q does not match originating route's scheme %q", e.TargetScheme, e.OriginatingScheme)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e ForwardSecuritySchemeMismatchError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("originating_scheme", e.OriginatingScheme),
		slog.String("target_scheme", e.TargetScheme),
	)
}

// defaultMaxForwards mirrors [rest]'s own defaultMaxRedirects precedent
// (adapters/nethttp/redirect.go) — the chain-depth cap used when
// [ClientCallOptions.MaxForwards] is <= 0 (the unset default).
const defaultMaxForwards = 10

// followForward is [Client.Call]'s CENTRALIZED chain-following loop
// (docs/roadmap/reqreply-forward-bounce.md's "Client-side resolution") —
// NOT inside the adapter, a deliberate divergence from REST's own
// adapter-side resolution mechanism (see docs/roadmap/
// reqreply-client-registry.md's "Adapter↔registry bridge" for the full
// rationale). originatingRoute is the route [Client.Call] was ORIGINALLY
// dispatched against (needed for its OWN declared security requirements,
// compared against each hop's target below) — fwd is the FIRST
// [ForwardError] [Client.Call] caught from t.Call's ordinary return.
//
// Each iteration: (1) resolves fwd.TargetTopic against the registry to
// recover the target's *RouteHandle (dynamic, via recoverRegistryEntry's
// SAME registry — [UnrecognizedForwardError] if absent,
// [DuplicateTopicTypeMismatchError] if the registry entry is poisoned);
// (2) compares the originating and target routes' declared security
// SCHEME NAME(S) — [ForwardSecuritySchemeMismatchError] on a structural
// mismatch, fails fast, client-side, BEFORE the network round-trip (see
// docs/roadmap/reqreply-forward-bounce.md's "Security-scheme mismatch
// check" — this check runs HERE, in api/reqreply.Client, never the
// adapter); (3) decrements a [ClientCallOptions.MaxForwards]-capped
// counter — [ForwardChainTooDeepError] once exhausted (mirrors
// adapters/nethttp/redirect.go's followRedirectHop's remaining-counter
// shape exactly); (4) calls CallRaw on the SAME attached ClientTransport
// instance (Observer/Capabilities resolution free — t is the SAME
// transport t.Call itself used) to publish fwd.Payload verbatim to
// fwd.TargetTopic and classify the reply: an ordinary successful reply
// decodes immediately via the resolved target RouteHandle's own Resp
// codec, DONE; a reply itself tagged as a forward loops to step 1 with
// the NEW ForwardError; a reply matching a declared ErrorPattern
// ([ForwardRawErrorReply], Phase 0's own CallRaw contract) is decoded via
// the resolved target RouteHandle's DecodeErrorFor, producing the SAME
// typed [ErrorPatternResponse] an ordinary (non-forwarded) Call would.
func (c *Client) followForward(ctx context.Context, t ClientTransport, originatingRoute any, fwd ForwardError, opts ...ClientCallOptions) (any, error) {
	_, originatingHandle, err := recoverRegistryEntry(originatingRoute)
	if err != nil {
		return nil, err
	}
	return c.followForwardFromHandle(ctx, t, originatingHandle, fwd, opts...)
}

// followForwardFromHandle is followForward's shared core, factored out
// (Phase 2.1, docs/roadmap/reqreply-dead-letter-replay.md's own
// "followForward signature adjustment") so [Client.ReplayDeadLetter] can
// hand off to the SAME chain-following loop from hop 2 onward WITHOUT a
// Route[Req,Resp]/*RouteHandle[Req,Resp] VALUE to pass through
// [recoverRegistryEntry] — Replay's hop 1 resolves its target directly
// off the registry (there is no "originating call" to recover a value
// from, see [Client.ReplayDeadLetter]'s own doc comment), so it already
// HAS the resolved originatingHandle in hand. [followForward] itself
// remains the entry point [Client.Call]/[Client.CallAsync] use
// (a Route[Req,Resp]/*RouteHandle[Req,Resp] VALUE to resolve), now a
// thin wrapper over this.
func (c *Client) followForwardFromHandle(ctx context.Context, t ClientTransport, originatingHandle any, fwd ForwardError, opts ...ClientCallOptions) (any, error) {
	var o ClientCallOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	maxForwards := o.MaxForwards
	if maxForwards <= 0 {
		maxForwards = defaultMaxForwards
	}
	remaining := maxForwards

	originatingSchemes := effectiveSecurityRequirements(originatingHandle)

	for {
		if remaining <= 0 {
			return nil, ForwardChainTooDeepError{TargetTopic: fwd.TargetTopic, Depth: maxForwards}
		}
		remaining--

		c.mu.RLock()
		entry, ok := c.routes[fwd.TargetTopic]
		c.mu.RUnlock()
		if !ok {
			return nil, UnrecognizedForwardError{TargetTopic: fwd.TargetTopic}
		}
		if entry.poisoned {
			return nil, DuplicateTopicTypeMismatchError{
				Topic:        fwd.TargetTopic,
				ExistingType: reflect.TypeOf(entry.Handle).String(),
				NewType:      entry.conflictingType,
			}
		}
		targetHandle := entry.Handle

		targetSchemes := effectiveSecurityRequirements(targetHandle)
		if mismatch, isMismatch := checkForwardSecurityScheme(originatingSchemes, targetSchemes); isMismatch {
			return nil, mismatch
		}

		replyBytes, callErr := t.CallRaw(ctx, fwd.TargetTopic, fwd.Payload, fwd.Properties)
		if callErr == nil {
			return decodeResponseReflect(targetHandle, replyBytes)
		}

		var nextFwd ForwardError
		if errors.As(callErr, &nextFwd) {
			fwd = nextFwd
			continue
		}

		var rawErr ForwardRawErrorReply
		if errors.As(callErr, &rawErr) {
			return decodeErrorForReflect(targetHandle, rawErr)
		}

		return nil, callErr
	}
}

// effectiveSecurityRequirements reflects handleAny (dynamic
// *RouteHandle[Req,Resp]) into its OWN effective []SecurityRequirement —
// Security if set, else GlobalSecurity — mirrors the SAME resolution
// rule [RouteHandle.Security]'s own doc comment documents for adapters
// ("reqs := handle.Security; if reqs == nil { reqs = handle.
// GlobalSecurity }"), just performed reflectively since handleAny is
// type-erased here.
func effectiveSecurityRequirements(handleAny any) []SecurityRequirement {
	elem := reflect.ValueOf(handleAny).Elem()
	reqsVal := elem.FieldByName("Security")
	if reqsVal.IsNil() {
		reqsVal = elem.FieldByName("GlobalSecurity")
	}
	reqs, _ := reqsVal.Interface().([]SecurityRequirement)
	return reqs
}

// checkForwardSecurityScheme compares originating's and target's
// effective security requirements' SCHEME NAME(S) — scheme-NAME
// comparison only, never a value/credential comparison (docs/roadmap/
// reqreply-forward-bounce.md's "Security-scheme mismatch check"). The
// target requiring NO security at all (empty requirement list) always
// matches — there is nothing to reuse incompatibly. Otherwise, the
// target's first declared scheme name must also appear somewhere in
// originating's own declared scheme name(s); any other shape mismatch
// returns [ForwardSecuritySchemeMismatchError].
func checkForwardSecurityScheme(originating, target []SecurityRequirement) (ForwardSecuritySchemeMismatchError, bool) {
	targetScheme := route.FirstSchemeName(target)
	if targetScheme == "" {
		return ForwardSecuritySchemeMismatchError{}, false
	}
	originatingScheme := route.FirstSchemeName(originating)
	for _, req := range originating {
		if _, ok := req[targetScheme]; ok {
			return ForwardSecuritySchemeMismatchError{}, false
		}
	}
	return ForwardSecuritySchemeMismatchError{OriginatingScheme: originatingScheme, TargetScheme: targetScheme}, true
}

// decodeResponseReflect reflects targetHandleAny (dynamic
// *RouteHandle[TargetReq,TargetResp]) to decode payload via ITS OWN
// declared Resp codec (DecodeResponseWithFormats, 0 format overrides —
// the SAME reflection-dispatch mechanism [call]'s own ordinary-reply leg
// already uses in each adapter, just living here instead since
// api/reqreply.Client — not the adapter — is doing the decode for a
// FORWARDED reply).
func decodeResponseReflect(targetHandleAny any, payload []byte) (any, error) {
	rv := reflect.ValueOf(targetHandleAny)
	results := rv.MethodByName("DecodeResponseWithFormats").Call([]reflect.Value{reflect.ValueOf(payload)})
	resp := results[0].Interface()
	errI, _ := results[1].Interface().(error)
	return resp, errI
}

// decodeErrorForReflect reflects targetHandleAny (dynamic
// *RouteHandle[TargetReq,TargetResp]) to finish [ForwardRawErrorReply]'s
// deliberately-deferred decode via RouteHandle.DecodeErrorFor(Code,
// Body) — see [ClientTransport.CallRaw]'s own doc comment for the
// extraction-vs-decode split rationale this closes. On a genuine match,
// returns the SAME typed [ErrorPatternResponse] an ordinary
// (non-forwarded) [Client.Call] would, as the error value. On no match
// (or a decode failure), falls back to rawErr itself — a caller still
// gets the raw code+body, same as an unmatched plain-text reply.
func decodeErrorForReflect(targetHandleAny any, rawErr ForwardRawErrorReply) (any, error) {
	rv := reflect.ValueOf(targetHandleAny)
	results := rv.MethodByName("DecodeErrorFor").Call([]reflect.Value{
		reflect.ValueOf(rawErr.Code), reflect.ValueOf(rawErr.Body),
	})
	resp, _ := results[0].Interface().(ErrorPatternResponse)
	matched, _ := results[1].Interface().(bool)
	decErr, _ := results[2].Interface().(error)
	if matched && decErr == nil {
		return nil, resp
	}
	return nil, rawErr
}
