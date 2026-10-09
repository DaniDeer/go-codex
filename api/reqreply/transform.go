package reqreply

import (
	"context"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/internal/middleware"
)

// MiddlewareHandler is the type-erased, SERVER-side runtime dispatch unit
// built by [Route.HandleMW] — the codec-backed-middleware counterpart to
// [middleware.ServerImplementation]. Stored on
// [RouteHandle.MiddlewareHandlers]; consumed by each adapter's own
// reflect-based Serve dispatch (mqtt5/zeromq).
//
// Deliberately has no adapter-specific message type anywhere in its shape
// — api/reqreply stays transport-agnostic; DecodeIn works from plain
// string-keyed var maps, and Fn is reflect-called by the adapter with the
// SAME already-decoded *Req the handler will also receive.
type MiddlewareHandler struct {
	// Name identifies this middleware in errors and observability.
	Name string

	// DecodeIn decodes+validates this middleware's own In value from the
	// SAME raw topic-var map the route's own Req decode uses, AND a
	// SEPARATE adapter-supplied property-var map (e.g. MQTT5 User
	// Properties) — kept as TWO SEPARATE parameters, never combined into
	// one map (see docs/design/d-0003-codec-declared-middlewares.md's Addendum's
	// "Round 2 correction"). Returns the decoded In boxed as `any`.
	DecodeIn func(ctx context.Context, topicVars, propertyVars map[string]string) (any, error)

	// Fn is the type-erased business Fn — concretely
	// func(ctx context.Context, req *Req, in In) (Out, error) — reflect-
	// called by the adapter.
	Fn any

	// EncodeOut derives reply topic AND property values from the Out
	// value returned by Fn, using ONLY this middleware's own response
	// merge-field declarations — again as TWO SEPARATE return maps.
	EncodeOut func(ctx context.Context, out any) (topicVars, propertyVars map[string]string, err error)

	// Agnostic is true when this handler was built from a route-AGNOSTIC
	// attachment (plain .Use(mw), mw bundled via [Middleware.WithReceive])
	// rather than [Route.HandleMW] — in that case Fn's ACTUAL shape is
	// func(ctx context.Context, in In) (Out, error) (no *Req parameter at
	// all). The adapter must branch on this flag when reflect-calling Fn.
	Agnostic bool

	// Satisfies names the security scheme(s) this handler's Fn satisfies
	// when it is Security-shaped (derived from mw's own
	// [Middleware.SecurityDeclaration] via [satisfiesOf]) — empty for a
	// general-purpose (non-Security) middleware, which always runs
	// regardless of the route's declared requirements. Mirrors
	// [rest.MiddlewareHandler.Satisfies]/[events.MiddlewareHandler.Satisfies]
	// exactly (docs/design/d-0007-declarative-middleware-layering.md's Rollout
	// Phase C) — lets [CheckCoverage] recognize a bound-or-agnostic-
	// attached codec-backed Security middleware as satisfying a declared
	// requirement, the same way it already recognizes a legacy
	// [middleware.ServerImplementation].
	Satisfies []string
}

// ClientMiddlewareHandler is the type-erased, CLIENT-side runtime dispatch
// unit built by [Route.ClientMW] — the client-side mirror of
// [MiddlewareHandler]. Stored on [RouteHandle.ClientMiddlewareHandlers];
// consumed by each adapter's Call dispatch.
type ClientMiddlewareHandler struct {
	// Name identifies this middleware in errors and observability.
	Name string

	// Fn is the type-erased business Fn — concretely
	// func(ctx context.Context, req Req) (In, error) — reflect-called by
	// the client adapter with the caller's own already-built Req.
	Fn any

	// EncodeIn derives outgoing topic AND property values from the In
	// value returned by Fn — TWO SEPARATE return maps, mirroring
	// [MiddlewareHandler.EncodeOut]'s shape.
	EncodeIn func(ctx context.Context, in any) (topicVars, propertyVars map[string]string, err error)

	// DecodeOut derives the middleware's own Out value from the reply's
	// actual topic AND property vars — mechanical, no Fn — TWO SEPARATE
	// map parameters.
	DecodeOut func(ctx context.Context, topicVars, propertyVars map[string]string) (any, error)

	// Agnostic is true when this handler was built from a route-AGNOSTIC
	// attachment (plain .Use(mw), mw bundled via [Middleware.WithSend])
	// rather than [Route.ClientMW] — in that case Fn's ACTUAL shape is
	// func(ctx context.Context) (In, error) (no Req parameter at all).
	Agnostic bool

	// Satisfies mirrors [MiddlewareHandler.Satisfies]'s identical
	// rationale, for the SENDING (client) role.
	Satisfies []string
}

// satisfiesOf derives mw's security scheme name(s) for [MiddlewareHandler.Satisfies]/
// [ClientMiddlewareHandler.Satisfies] — empty when mw carries no
// [middleware.SecurityDeclaration] (a general-purpose, non-Security
// middleware). Mirrors [events.satisfiesOf]/[rest]'s identical helper.
func satisfiesOf[In, Out any](mw Middleware[In, Out]) []string {
	if sec := mw.SecurityDeclaration(); sec != nil {
		return []string{sec.SchemeName}
	}
	return nil
}

// topicFieldsOf/propertyFieldsOf convert a slice of Merged*Param values
// (each embedding a spec Param plus an unexported merge field) into plain
// []codex.FieldCodec[T] — shared by DecodeIn/EncodeIn closures below.
func topicFieldsOf[T any](ps []MergedTopicParam[T]) []codex.FieldCodec[T] {
	out := make([]codex.FieldCodec[T], len(ps))
	for i, p := range ps {
		out[i] = p.Field
	}
	return out
}

func propertyFieldsOf[T any](ps []MergedPropertyParam[T]) []codex.FieldCodec[T] {
	out := make([]codex.FieldCodec[T], len(ps))
	for i, p := range ps {
		out[i] = p.Field
	}
	return out
}

// buildDecodeIn builds the DecodeIn closure shared by
// [buildMiddlewareHandler] (route-BOUND) and [buildAgnosticMiddlewareHandler]
// (route-AGNOSTIC). Internally a thin wrapper over [middleware.DecodeLayer]
// (docs/design/d-0007-declarative-middleware-layering.md's Rollout Phase C — the
// mechanism's 3RD consumer, migrated from rest's/events' identical Phase
// A/B migrations) — behavior is UNCHANGED: same 2-axis order (topic,
// property), same fail-fast-at-first-error semantics, same
// [MiddlewareInputError] wrapping.
func buildDecodeIn[In, Out any](mw Middleware[In, Out]) func(ctx context.Context, topicVars, propertyVars map[string]string) (any, error) {
	topicFields := topicFieldsOf(mw.topicMergeFieldsIn)
	propertyFields := propertyFieldsOf(mw.propertyMergeFieldsIn)
	wrapErr := func(err error) error { return MiddlewareInputError{Name: mw.Name, Err: err} }
	ctxFieldsFromIn := mw.ctxFieldsFromIn

	return func(ctx context.Context, topicVars, propertyVars map[string]string) (any, error) {
		in, err := middleware.DecodeLayer([]middleware.Axis[In]{
			{Fields: topicFields, Vars: topicVars},
			{Fields: propertyFields, Vars: propertyVars},
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

// buildEncodeOut is [buildDecodeIn]'s response-side sibling — a thin
// wrapper over [middleware.EncodeLayer]. [middleware.EncodeLayer] uses
// [codex.EncodeMergeVars] internally for EVERY axis (not just property) —
// a safe, behavior-preserving change for the topic axis specifically,
// since topic merge fields never use [codex.OmitEmptyField]/
// [codex.OmitEmptyFieldFunc] (topics have no Required/Optional split to
// begin with — confirmed [codex.EncodeMergeVars] is byte-identical to
// [codex.EncodeVars] for any non-sparse field), mirroring rest's/events'
// identical migration.
func buildEncodeOut[In, Out any](mw Middleware[In, Out]) func(ctx context.Context, outAny any) (map[string]string, map[string]string, error) {
	topicFields := topicFieldsOf(mw.topicMergeFieldsOut)
	propertyFields := propertyFieldsOf(mw.propertyMergeFieldsOut)
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
		vars, err := middleware.EncodeLayer(out, [][]codex.FieldCodec[Out]{topicFields, propertyFields}, wrapErr)
		if err != nil {
			return nil, nil, err
		}
		return vars[0], vars[1], nil
	}
}

// buildMiddlewareHandlerAny builds a type-erased [MiddlewareHandler] from a
// concrete [Middleware][In, Out] and an UNTYPED fn — the SOLE builder for
// the route-BOUND, RECEIVING role, used by both [Route.HandleMW]'s bound
// path (see [BoundMiddleware.ApplyBoundRoute]) and
// [buildAgnosticMiddlewareHandler] (docs/design/d-0007-declarative-middleware-layering.md's Rollout Phase C, mirroring rest's/events' identical DRY
// consolidation): fn is already `any` on [MiddlewareHandler.Fn] itself,
// so a SEPARATE `any`-typed builder needs zero new type parameters beyond
// In/Out (the receiver mw already carries).
func buildMiddlewareHandlerAny[In, Out any](mw Middleware[In, Out], fn any) MiddlewareHandler {
	return MiddlewareHandler{
		Name:      mw.Name,
		DecodeIn:  buildDecodeIn(mw),
		Fn:        fn,
		EncodeOut: buildEncodeOut(mw),
		Satisfies: satisfiesOf(mw),
	}
}

// buildAgnosticMiddlewareHandler builds a type-erased [MiddlewareHandler]
// from a route-AGNOSTIC [Middleware][In, Out] — one attached via plain
// .Use(mw), bundled via [Middleware.WithReceive] — mirroring the bound
// case (see [buildMiddlewareHandlerAny]) except Fn is mw's OWN bundled
// receiveFn (func(ctx, In) (Out, error), no *Req) and
// [MiddlewareHandler.Agnostic] is set so the adapter reflect-calls Fn
// with the matching arity.
func buildAgnosticMiddlewareHandler[In, Out any](mw Middleware[In, Out]) MiddlewareHandler {
	h := buildMiddlewareHandlerAny(mw, mw.receiveFn)
	h.Agnostic = true
	return h
}

// buildEncodeIn builds the EncodeIn closure shared by
// [buildClientMiddlewareHandler] and [buildAgnosticClientMiddlewareHandler]
// — a thin wrapper over [middleware.EncodeLayer], mirroring [buildEncodeOut]'s
// identical migration. Wraps every failure in [MiddlewareInputError] — the
// "In"-side counterpart of [buildDecodeIn]'s identical wrapping (deep-dive
// review round fix: this was previously the ONLY one of the 4 build*
// functions in this file returning a bare, unwrapped error, leaving a
// caller with no way to recover the failing middleware's Name via
// errors.As — buildDecodeIn/buildEncodeOut/buildDecodeOut all already
// wrapped their own failures).
func buildEncodeIn[In, Out any](mw Middleware[In, Out]) func(ctx context.Context, inAny any) (map[string]string, map[string]string, error) {
	topicFields := topicFieldsOf(mw.topicMergeFieldsIn)
	propertyFields := propertyFieldsOf(mw.propertyMergeFieldsIn)
	wrapErr := func(err error) error { return MiddlewareInputError{Name: mw.Name, Err: err} }
	ctxFieldsFromIn := mw.ctxFieldsFromIn

	return func(ctx context.Context, inAny any) (map[string]string, map[string]string, error) {
		in, _ := inAny.(In)
		if err := mw.InCodec.Validate(in); err != nil {
			return nil, nil, MiddlewareInputError{Name: mw.Name, Err: err}
		}
		for _, cf := range ctxFieldsFromIn {
			if err := cf.field.Set(ctx, cf.get(in)); err != nil {
				return nil, nil, MiddlewareInputError{Name: mw.Name, Err: err}
			}
		}
		vars, err := middleware.EncodeLayer(in, [][]codex.FieldCodec[In]{topicFields, propertyFields}, wrapErr)
		if err != nil {
			return nil, nil, err
		}
		return vars[0], vars[1], nil
	}
}

// buildDecodeOut is [buildEncodeIn]'s response-side sibling — the
// CLIENT-side decode of the middleware's OWN Out value from the reply's
// topic/property vars. Previously (a confirmed bug, fixed alongside
// docs/design/d-0003-codec-declared-middlewares.md's Addendum 2) this
// wrapped failures in [MiddlewareInputError] — semantically wrong, since
// this decodes the Out struct (the REPLY), not the In struct (the
// REQUEST). Now correctly wraps in [MiddlewareOutputError], matching
// [buildEncodeOut]'s (the server-side Out-encode sibling) error type —
// a thin wrapper over [middleware.DecodeLayer], mirroring [buildDecodeIn]'s
// identical migration.
func buildDecodeOut[In, Out any](mw Middleware[In, Out]) func(ctx context.Context, topicVars, propertyVars map[string]string) (any, error) {
	topicFields := topicFieldsOf(mw.topicMergeFieldsOut)
	propertyFields := propertyFieldsOf(mw.propertyMergeFieldsOut)
	wrapErr := func(err error) error { return MiddlewareOutputError{Name: mw.Name, Err: err} }
	ctxFieldsFromOut := mw.ctxFieldsFromOut

	return func(ctx context.Context, topicVars, propertyVars map[string]string) (any, error) {
		out, err := middleware.DecodeLayer([]middleware.Axis[Out]{
			{Fields: topicFields, Vars: topicVars},
			{Fields: propertyFields, Vars: propertyVars},
		}, wrapErr)
		if err != nil {
			return nil, err
		}
		for _, cf := range ctxFieldsFromOut {
			if err := cf.field.Set(ctx, cf.get(out)); err != nil {
				return nil, MiddlewareOutputError{Name: mw.Name, Err: err}
			}
		}
		return out, nil
	}
}

// buildClientMiddlewareHandlerAny builds a type-erased
// [ClientMiddlewareHandler] from a concrete [Middleware][In, Out] and an
// UNTYPED fn — the SENDING-role mirror of [buildMiddlewareHandlerAny],
// used by both [Route.ClientMW]'s bound path (see
// [BoundClientMiddleware.ApplyBoundClientRoute]) and
// [buildAgnosticClientMiddlewareHandler].
func buildClientMiddlewareHandlerAny[In, Out any](mw Middleware[In, Out], fn any) ClientMiddlewareHandler {
	return ClientMiddlewareHandler{
		Name:      mw.Name,
		Fn:        fn,
		EncodeIn:  buildEncodeIn(mw),
		DecodeOut: buildDecodeOut(mw),
		Satisfies: satisfiesOf(mw),
	}
}

// buildAgnosticClientMiddlewareHandler builds a type-erased
// [ClientMiddlewareHandler] from a route-AGNOSTIC [Middleware][In, Out] —
// one attached via plain .Use(mw), bundled via [Middleware.WithSend] —
// mirroring the bound case (see [buildClientMiddlewareHandlerAny]) except
// Fn is mw's OWN bundled sendFn (func(ctx) (In, error), no Req) and
// [ClientMiddlewareHandler.Agnostic] is set so the adapter reflect-calls
// Fn with the matching arity.
func buildAgnosticClientMiddlewareHandler[In, Out any](mw Middleware[In, Out]) ClientMiddlewareHandler {
	h := buildClientMiddlewareHandlerAny(mw, mw.sendFn)
	h.Agnostic = true
	return h
}

// middlewareSpecContribution captures the spec-relevant param
// declarations from ONE attached codec-backed [Middleware] — converted to
// plain, Req/Resp-agnostic spec types at the GENERIC call site
// ([specContributionOf]/[boundSpecContributionOf]) where In/Out are still
// concrete. Fed into [applyParamDeclarations]'s conflict-detection/
// layering pass, unified with Phase 1b's flat mechanism.
type middlewareSpecContribution struct {
	name string

	topicParamsIn     []TopicParam
	topicParamsOut    []TopicParam
	propertyParamsIn  []PropertyParam
	propertyParamsOut []PropertyParam

	// presencePropertyParamsIn/Out carry PRESENCE-ONLY (non-merged)
	// property contributions ONLY (from WithRequestPropertySpec/
	// WithResponsePropertySpec) — kept SEPARATE from
	// propertyParamsIn/Out (which also carries merge-field contributions
	// that deliberately stay schema-only, see applyParamDeclarations'
	// own comment) because presence-only entries have NO alternate
	// runtime-validation path of their own and must ALSO reach
	// RouteHandle.RequestHeaderParams/ResponseHeaderParams — the gap
	// closed by docs/design/d-0003-codec-declared-middlewares.md's Phase D0.
	presencePropertyParamsIn  []PropertyParam
	presencePropertyParamsOut []PropertyParam
}

// boundSpecContributionOf is [specContributionOf]'s passthrough, used by
// [BoundMiddleware.ApplyBoundRoute]/[BoundClientMiddleware.ApplyBoundClientRoute]
// — kept as a separate named function (rather than inlining
// specContributionOf at both call sites) purely for call-site symmetry
// with the agnostic path's own [specContributionOf] call.
func boundSpecContributionOf[In, Out any](mw Middleware[In, Out]) middlewareSpecContribution {
	return specContributionOf(mw)
}

// specContributionOf extracts mw's plain spec Param values (discarding
// the merge field half, which is only needed at DecodeIn/EncodeOut time,
// already handled by buildMiddlewareHandler/buildClientMiddlewareHandler).
func specContributionOf[In, Out any](mw Middleware[In, Out]) middlewareSpecContribution {
	c := middlewareSpecContribution{name: mw.Name}
	for _, p := range mw.topicMergeFieldsIn {
		c.topicParamsIn = append(c.topicParamsIn, TopicParam{Name: p.Name, Description: p.Description, Codec: p.Codec})
	}
	for _, p := range mw.topicMergeFieldsOut {
		c.topicParamsOut = append(c.topicParamsOut, TopicParam{Name: p.Name, Description: p.Description, Codec: p.Codec})
	}
	for _, p := range mw.propertyMergeFieldsIn {
		c.propertyParamsIn = append(c.propertyParamsIn, PropertyParam{Param: p.Param, Required: p.Required})
	}
	for _, p := range mw.propertyMergeFieldsOut {
		c.propertyParamsOut = append(c.propertyParamsOut, PropertyParam{Param: p.Param, Required: p.Required})
	}
	c.presencePropertyParamsIn = append(c.presencePropertyParamsIn, mw.propertySpecsIn...)
	c.presencePropertyParamsOut = append(c.presencePropertyParamsOut, mw.propertySpecsOut...)
	return c
}

// Transform/ClientTransform (an EARLIER, free-function route-bound
// attachment point predating even the reflection-based HandleMW/ClientMW
// mechanism) never existed in this package by this name — any reference
// to them elsewhere is stale/aspirational. The CURRENT route-bound
// attachment point is [Route.HandleBoundMW]/[Route.ClientBoundMW], using
// the dedicated [BoundMiddleware]/[BoundClientMiddleware] types (see
// bound_middleware.go) — see docs/design/d-0003-codec-declared-middlewares.md's Addendum 7.
