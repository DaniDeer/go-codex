package events

import (
	"context"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/middleware"
)

// MiddlewareHandler is the type-erased, RECEIVING-role (subscribe) runtime
// dispatch unit built by [Transform] — the codec-backed-middleware
// counterpart to [middleware.ServerImplementation], adapted for events'
// asymmetric shape (no Out on subscribe — see [Middleware]'s doc comment).
// Stored on [ChannelHandle.MiddlewareHandlers]; consumed by each adapter's
// own subscribe dispatch — never constructed directly by callers.
type MiddlewareHandler struct {
	// Name identifies this middleware in errors and observability.
	Name string

	// DecodeIn decodes+validates this middleware's own In value from the
	// SAME raw topic var map the channel's own DecodeMerged uses, AND a
	// SEPARATE property var map (MQTT5 User Properties/future AMQP message
	// headers) — independent of T, using ONLY this middleware's own topic/
	// property merge-field declarations and the middleware's own InCodec.
	// The two maps are NEVER combined (mirrors reqreply's/REST's own
	// multi-map-never-combined pattern) — each is decoded against &in
	// separately, only touching the fields THAT AXIS declared. Returns the
	// decoded In boxed as `any` (its concrete type is recovered by the
	// adapter via reflection, mirroring how
	// [middleware.ServerImplementation.Fn] is already reflect-called).
	DecodeIn func(ctx context.Context, topicVars, propertyVars map[string]string) (any, error)

	// propertyParams holds this handler's OWN property-param declarations
	// in spec-level form (Name/Required/Codec) — mirrors [MiddlewareHandler.Name]'s
	// role of carrying spec-relevant metadata alongside the runtime
	// dispatch unit; consumed by [applyEventsPropertyDeclarations] for
	// conflict-detection/AsyncAPI-rendering. Empty when mw carried no
	// WithSubscribeProperty declarations.
	propertyParams []PropertyParam

	// Fn is the type-erased business Fn — concretely
	// func(ctx context.Context, msg *T, in In) error (channel-BOUND) or
	// func(ctx context.Context, in In) error (channel-AGNOSTIC, see
	// Agnostic) — reflect-called by the adapter.
	Fn any

	// Agnostic is true when this handler was built from a channel-AGNOSTIC
	// attachment (plain .Use(mw), mw bundled via [Middleware.WithReceive])
	// rather than [Transform] — in that case Fn's ACTUAL shape has no
	// *T parameter at all. The adapter must branch on this flag when
	// reflect-calling Fn.
	Agnostic bool

	// dualAttached is true ONLY for a handler built by [Transform] whose mw
	// ALSO carries a WithReceive Fn — exactly D7's ambiguous case (bound
	// AND agnostic attachment styles combined on the SAME value). A
	// handler built from the plain .Use() path never sets this — bundled
	// there is the WHOLE POINT of that attachment style, not a conflict.
	dualAttached bool

	// Satisfies names the security scheme(s) this handler's Fn satisfies
	// when it is Security-shaped (derived from mw's own
	// [Middleware.SecurityDeclaration] via [satisfiesOf]) — empty for a
	// general-purpose (non-Security) middleware, which always runs
	// regardless of the channel's declared requirements. Mirrors
	// [rest.MiddlewareHandler.Satisfies] exactly (docs/roadmap/
	// declarative-middleware-layering.md's Rollout Phase B) — lets
	// [CheckCoverage] recognize a bound-or-agnostic-attached codec-backed
	// Security middleware as satisfying a declared requirement, the same
	// way it already recognizes a legacy [middleware.ServerImplementation].
	Satisfies []string

	// HasOut is true when Fn uses the NEW, ADDITIVE, Out-carrying bound
	// shape (func(ctx, *T, In) (Out, error), detected via
	// [isBoundSubscribeMWShapeWithOut]) rather than the ORIGINAL,
	// UNCHANGED shape (func(ctx, *T, In) error). docs/roadmap/
	// declarative-middleware-layering.md's "Prerequisite for Phase 2
	// (api/events)": Subscribe's bound Fn structurally had no way to
	// return a value at all — this field distinguishes the two shapes so
	// [DispatchSubscribeMiddlewareHandlers] knows how many return values
	// to expect. False for EVERY pre-existing handler (general-purpose
	// middleware never needs this) — fully additive, zero behavior change
	// for the original shape.
	HasOut bool

	// ValidateOut validates a HasOut handler's decoded Out value via mw's
	// OWN OutCodec (mirrors [ClientMiddlewareHandler.EncodeOut]'s
	// identical validation step on the publish side) — nil when
	// HasOut is false. Subscribe's Out is NEVER wire-encoded (there is no
	// outgoing message to encode into); it exists ONLY so a
	// Security-carrying middleware can return `GrantedScopes`, read the
	// SAME way Publish's Out already is (see the merge-and-enforce
	// mechanism this Out value feeds).
	ValidateOut func(out any) error
}

// ClientMiddlewareHandler is the type-erased, SENDING-role (publish)
// runtime dispatch unit built by [ClientTransform] — the publish-side
// mirror of [MiddlewareHandler]. Stored on
// [ChannelHandle.ClientMiddlewareHandlers]; consumed by each adapter's own
// publish dispatch.
type ClientMiddlewareHandler struct {
	// Name identifies this middleware in errors and observability.
	Name string

	// Fn is the type-erased business Fn — concretely
	// func(ctx context.Context, msg T) (Out, error) (channel-BOUND) or
	// func(ctx context.Context) (Out, error) (channel-AGNOSTIC, see
	// Agnostic) — reflect-called by the adapter.
	Fn any

	// EncodeOut derives outgoing topic-var AND property-var values (as TWO
	// SEPARATE maps, never combined — mirrors [MiddlewareHandler.DecodeIn]'s
	// identical pattern) from the Out value returned by Fn, using ONLY
	// this middleware's own publish-topic/publish-property merge-field
	// declarations and the middleware's own OutCodec.
	EncodeOut func(ctx context.Context, out any) (topicVars, propertyVars map[string]string, err error)

	// propertyParams mirrors [MiddlewareHandler.propertyParams] for the
	// publish (SENDING) role — this handler's OWN property-param
	// declarations in spec-level form, from WithPublishProperty.
	propertyParams []PropertyParam

	// Agnostic is true when this handler was built from a channel-AGNOSTIC
	// attachment (plain .Use(mw), mw bundled via [Middleware.WithSend])
	// rather than [ClientTransform] — in that case Fn's ACTUAL shape has
	// no T parameter at all. The adapter must branch on this flag when
	// reflect-calling Fn.
	Agnostic bool

	// dualAttached is true ONLY for a handler built by [ClientTransform]
	// whose mw ALSO carries a WithSend Fn — D7's ambiguous case. See
	// [MiddlewareHandler.dualAttached]'s identical rationale.
	dualAttached bool

	// Satisfies mirrors [MiddlewareHandler.Satisfies]'s identical
	// rationale, for the SENDING (publish/client) role.
	Satisfies []string
}

// buildDecodeIn builds the DecodeIn closure shared by [buildMiddlewareHandler]
// (channel-BOUND) and [buildAgnosticMiddlewareHandler] (channel-AGNOSTIC).
// Internally a thin wrapper over [middleware.DecodeLayer] (docs/roadmap/
// declarative-middleware-layering.md's Rollout Phase B — the mechanism's
// generality test, migrated from api/rest's own identical migration in
// Phase A) — behavior is UNCHANGED: same 2-axis order (topic, property),
// same fail-fast-at-first-error semantics, same [MiddlewareInputError]
// wrapping. Decodes topicVars and propertyVars against &in SEPARATELY,
// never combining the two maps — each axis only touches the fields THAT
// AXIS declared.
func buildDecodeIn[In, Out any](mw Middleware[In, Out]) func(ctx context.Context, topicVars, propertyVars map[string]string) (any, error) {
	topicFields := mw.topicMergeFieldsIn
	propFields := mw.propertyMergeFieldsIn
	wrapErr := func(err error) error { return MiddlewareInputError{Name: mw.Name, Err: err} }
	ctxFieldsFromIn := mw.ctxFieldsFromIn
	return func(ctx context.Context, topicVars, propertyVars map[string]string) (any, error) {
		in, err := middleware.DecodeLayer([]middleware.Axis[In]{
			{Fields: topicFields, Vars: topicVars},
			{Fields: propFields, Vars: propertyVars},
		}, wrapErr)
		if err != nil {
			return nil, err
		}
		if err := mw.InCodec.Validate(in); err != nil {
			return nil, MiddlewareInputError{Name: mw.Name, Err: err}
		}
		for _, cf := range ctxFieldsFromIn {
			if err := cf.field.Set(ctx, cf.get(in)); err != nil {
				return nil, MiddlewareInputError{Name: mw.Name, Err: err}
			}
		}
		return in, nil
	}
}

// buildEncodeOut builds the EncodeOut closure shared by
// [buildClientMiddlewareHandler] (channel-BOUND) and
// [buildAgnosticClientMiddlewareHandler] (channel-AGNOSTIC). Internally a
// thin wrapper over [middleware.EncodeLayer] — behavior is UNCHANGED: same
// 2-axis order (topic, property), same fail-fast semantics, same
// [MiddlewareOutputError] wrapping, same nil-map-when-no-fields contract.
// Returns topic vars and property vars as TWO SEPARATE maps, never
// combined. [middleware.EncodeLayer] uses [codex.EncodeMergeVars]
// internally for EVERY axis (not just property) — a safe, behavior-
// preserving change for the topic axis specifically, since topic merge
// fields never use [codex.OmitEmptyField]/[OmitEmptyFieldFunc] (topics
// have no Required/Optional split to begin with — confirmed
// [codex.EncodeMergeVars] is byte-identical to [codex.EncodeVars] for any
// non-sparse field).
func buildEncodeOut[In, Out any](mw Middleware[In, Out]) func(ctx context.Context, outAny any) (topicVars, propertyVars map[string]string, err error) {
	topicFields := mw.topicMergeFieldsOut
	propFields := mw.propertyMergeFieldsOut
	wrapErr := func(err error) error { return MiddlewareOutputError{Name: mw.Name, Err: err} }
	ctxFieldsFromOut := mw.ctxFieldsFromOut
	return func(ctx context.Context, outAny any) (map[string]string, map[string]string, error) {
		out, _ := outAny.(Out)
		if err := mw.OutCodec.Validate(out); err != nil {
			return nil, nil, MiddlewareOutputError{Name: mw.Name, Err: err}
		}
		for _, cf := range ctxFieldsFromOut {
			if err := cf.field.Set(ctx, cf.get(out)); err != nil {
				return nil, nil, MiddlewareOutputError{Name: mw.Name, Err: err}
			}
		}
		vars, err := middleware.EncodeLayer(out, [][]codex.FieldCodec[Out]{
			topicFields,
			propFields,
		}, wrapErr)
		if err != nil {
			return nil, nil, err
		}
		return vars[0], vars[1], nil
	}
}

// satisfiesOf mirrors [rest.satisfiesOf] exactly — derives the
// [MiddlewareHandler.Satisfies]/[ClientMiddlewareHandler.Satisfies] value
// shared by EVERY handler-building function below (bound AND agnostic,
// both roles), so [CheckCoverage] can treat a codec-backed Middleware's
// dispatch handler identically to a legacy
// [middleware.ServerImplementation]/[middleware.ClientImplementation]
// regardless of which attachment style produced it.
func satisfiesOf[In, Out any](mw Middleware[In, Out]) []string {
	if sec := mw.SecurityDeclaration(); sec != nil {
		return []string{sec.SchemeName}
	}
	return nil
}

// buildMiddlewareHandlerAny builds a type-erased [MiddlewareHandler] from a
// concrete [Middleware][In, Out] and an UNTYPED fn — the SOLE builder for
// the channel-BOUND, RECEIVING role, used by both [Subscriber.SubscribeMW]'s
// bound path (see [Middleware.applyBoundSubscriber]) and
// [buildAgnosticMiddlewareHandler] (docs/roadmap/
// declarative-middleware-layering.md's Rollout Phase B — events' own
// Architecture-revision fold-in, mirroring api/rest's identical Phase A
// consolidation): fn is already `any` on [MiddlewareHandler.Fn] itself, so
// a single `any`-typed builder needs zero new type parameters beyond
// In/Out (the receiver mw already carries).
// dualAttached is DELIBERATELY NOT set here — this builder is shared by
// BOTH the bound path (Transform/applyBoundSubscriber) AND
// [buildAgnosticMiddlewareHandler]; mw.isBundled() is naturally true for
// a LEGITIMATE agnostic-only attachment (bundled IS the whole point of
// that style), so setting it unconditionally here would misfire D7's
// ambiguous-attachment check against pure agnostic usage (confirmed via
// an actual test failure — see git history). Only a BOUND call site may
// set dualAttached, and only when mw is ALSO bundled.
func buildMiddlewareHandlerAny[In, Out any](mw Middleware[In, Out], fn any) MiddlewareHandler {
	return MiddlewareHandler{
		Name:           mw.Name,
		DecodeIn:       buildDecodeIn(mw),
		Fn:             fn,
		propertyParams: mw.propertyParamsIn,
		Satisfies:      satisfiesOf(mw),
	}
}

// buildAgnosticMiddlewareHandler builds a type-erased [MiddlewareHandler]
// from a channel-AGNOSTIC [Middleware][In, Out] — one attached via plain
// .Use(mw), bundled via [Middleware.WithReceive] — mirroring the bound
// case (see [buildMiddlewareHandlerAny]) except Fn is mw's OWN bundled
// receiveFn (func(ctx, In) error, no *T) and [MiddlewareHandler.Agnostic]
// is set so the adapter reflect-calls Fn with the matching arity.
func buildAgnosticMiddlewareHandler[In, Out any](mw Middleware[In, Out]) MiddlewareHandler {
	h := buildMiddlewareHandlerAny(mw, mw.receiveFn)
	h.Agnostic = true
	return h
}

// buildClientMiddlewareHandlerAny builds a type-erased
// [ClientMiddlewareHandler] from a concrete [Middleware][In, Out] and an
// UNTYPED fn — the SENDING-role mirror of [buildMiddlewareHandlerAny],
// used by both [Publisher.PublishMW]'s bound path (see
// [Middleware.applyBoundPublisher]) and
// [buildAgnosticClientMiddlewareHandler].
// dualAttached is DELIBERATELY NOT set here — see [buildMiddlewareHandlerAny]'s
// identical rationale.
func buildClientMiddlewareHandlerAny[In, Out any](mw Middleware[In, Out], fn any) ClientMiddlewareHandler {
	return ClientMiddlewareHandler{
		Name:           mw.Name,
		Fn:             fn,
		EncodeOut:      buildEncodeOut(mw),
		propertyParams: mw.propertyParamsOut,
		Satisfies:      satisfiesOf(mw),
	}
}

// buildAgnosticClientMiddlewareHandler builds a type-erased
// [ClientMiddlewareHandler] from a channel-AGNOSTIC [Middleware][In, Out]
// — one attached via plain .Use(mw), bundled via [Middleware.WithSend] —
// mirroring the bound case (see [buildClientMiddlewareHandlerAny]) except
// Fn is mw's OWN bundled sendFn (func(ctx) (Out, error), no T) and
// [ClientMiddlewareHandler.Agnostic] is set so the adapter reflect-calls
// Fn with the matching arity.
func buildAgnosticClientMiddlewareHandler[In, Out any](mw Middleware[In, Out]) ClientMiddlewareHandler {
	h := buildClientMiddlewareHandlerAny(mw, mw.sendFn)
	h.Agnostic = true
	return h
}

// Transform/ClientTransform (the channel-BOUND, free-function attachment
// point) were REMOVED (docs/design/d-0007-declarative-middleware-layering.md's
// Rollout Phase B — events' own Architecture revision, mirroring Phase
// A's identical REST removal) — folded into [Subscriber.SubscribeMW]/
// [Publisher.PublishMW] directly via reflection-based shape detection
// ([isBoundSubscribeMWShape]/[isBoundPublishMWShape]). Use
// `subscriber.SubscribeMW(mw, fn)`/`publisher.PublishMW(mw, fn)` instead —
// identical Fn signatures, identical dispatch, reached through the
// ordinary method-chain API instead of a separate free function.
