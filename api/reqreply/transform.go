package reqreply

import (
	"context"
	"slices"

	"github.com/DaniDeer/go-codex/codex"
)

// MiddlewareHandler is the type-erased, SERVER-side runtime dispatch unit
// built by [Transform] — the codec-backed-middleware counterpart to
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
	// one map (see docs/roadmap/reqreply-codec-declared-middleware.md's
	// "Round 2 correction"). Returns the decoded In boxed as `any`.
	DecodeIn func(topicVars, propertyVars map[string]string) (any, error)

	// Fn is the type-erased business Fn — concretely
	// func(ctx context.Context, req *Req, in In) (Out, error) — reflect-
	// called by the adapter.
	Fn any

	// EncodeOut derives reply topic AND property values from the Out
	// value returned by Fn, using ONLY this middleware's own response
	// merge-field declarations — again as TWO SEPARATE return maps.
	EncodeOut func(out any) (topicVars, propertyVars map[string]string, err error)

	// Agnostic is true when this handler was built from a route-AGNOSTIC
	// attachment (plain .Use(mw), mw bundled via [Middleware.WithReceive])
	// rather than [Transform] — in that case Fn's ACTUAL shape is
	// func(ctx context.Context, in In) (Out, error) (no *Req parameter at
	// all). The adapter must branch on this flag when reflect-calling Fn.
	Agnostic bool
}

// ClientMiddlewareHandler is the type-erased, CLIENT-side runtime dispatch
// unit built by [ClientTransform] — the client-side mirror of
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
	EncodeIn func(in any) (topicVars, propertyVars map[string]string, err error)

	// DecodeOut derives the middleware's own Out value from the reply's
	// actual topic AND property vars — mechanical, no Fn — TWO SEPARATE
	// map parameters.
	DecodeOut func(topicVars, propertyVars map[string]string) (any, error)

	// Agnostic is true when this handler was built from a route-AGNOSTIC
	// attachment (plain .Use(mw), mw bundled via [Middleware.WithSend])
	// rather than [ClientTransform] — in that case Fn's ACTUAL shape is
	// func(ctx context.Context) (In, error) (no Req parameter at all).
	Agnostic bool
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

// buildDecodeIn — mirrors rest's identical function, adapted to
// reqreply's two axes (topic, property) instead of REST's three (header,
// cookie, query).
func buildDecodeIn[In, Out any](mw Middleware[In, Out]) func(topicVars, propertyVars map[string]string) (any, error) {
	topicFields := topicFieldsOf(mw.topicMergeFieldsIn)
	propertyFields := propertyFieldsOf(mw.propertyMergeFieldsIn)

	return func(topicVars, propertyVars map[string]string) (any, error) {
		var in In
		if len(topicFields) > 0 {
			if err := codex.DecodeVars(&in, topicVars, topicFields...); err != nil {
				return nil, MiddlewareInputError{Name: mw.Name, Err: err}
			}
		}
		if len(propertyFields) > 0 {
			if err := codex.DecodeVars(&in, propertyVars, propertyFields...); err != nil {
				return nil, MiddlewareInputError{Name: mw.Name, Err: err}
			}
		}
		if err := mw.InCodec.Validate(in); err != nil {
			return nil, MiddlewareInputError{Name: mw.Name, Err: err}
		}
		return in, nil
	}
}

// buildEncodeOut is [buildDecodeIn]'s response-side sibling.
func buildEncodeOut[In, Out any](mw Middleware[In, Out]) func(outAny any) (map[string]string, map[string]string, error) {
	topicFields := topicFieldsOf(mw.topicMergeFieldsOut)
	propertyFields := propertyFieldsOf(mw.propertyMergeFieldsOut)

	return func(outAny any) (map[string]string, map[string]string, error) {
		out, _ := outAny.(Out)
		if err := mw.OutCodec.Validate(out); err != nil {
			return nil, nil, err
		}
		var topicVars, propertyVars map[string]string
		var err error
		if len(topicFields) > 0 {
			if topicVars, err = codex.EncodeVars(out, topicFields...); err != nil {
				return nil, nil, err
			}
		}
		if len(propertyFields) > 0 {
			if propertyVars, err = codex.EncodeVars(out, propertyFields...); err != nil {
				return nil, nil, err
			}
		}
		return topicVars, propertyVars, nil
	}
}

// buildMiddlewareHandler builds a type-erased [MiddlewareHandler] from a
// concrete [Middleware][In, Out] and its fn — In/Out are known here (the
// generic call site) and erased into plain closures where needed.
func buildMiddlewareHandler[Req, Resp, In, Out any](mw Middleware[In, Out], fn func(ctx context.Context, req *Req, in In) (Out, error)) MiddlewareHandler {
	return MiddlewareHandler{
		Name:      mw.Name,
		DecodeIn:  buildDecodeIn(mw),
		Fn:        fn,
		EncodeOut: buildEncodeOut(mw),
	}
}

// buildAgnosticMiddlewareHandler builds a type-erased [MiddlewareHandler]
// from a route-AGNOSTIC [Middleware][In, Out] — one attached via plain
// .Use(mw), bundled via [Middleware.WithReceive].
func buildAgnosticMiddlewareHandler[In, Out any](mw Middleware[In, Out]) MiddlewareHandler {
	return MiddlewareHandler{
		Name:      mw.Name,
		DecodeIn:  buildDecodeIn(mw),
		Fn:        mw.receiveFn,
		EncodeOut: buildEncodeOut(mw),
		Agnostic:  true,
	}
}

// buildEncodeIn builds the EncodeIn closure shared by
// [buildClientMiddlewareHandler] and [buildAgnosticClientMiddlewareHandler].
func buildEncodeIn[In, Out any](mw Middleware[In, Out]) func(inAny any) (map[string]string, map[string]string, error) {
	topicFields := topicFieldsOf(mw.topicMergeFieldsIn)
	propertyFields := propertyFieldsOf(mw.propertyMergeFieldsIn)

	return func(inAny any) (map[string]string, map[string]string, error) {
		in, _ := inAny.(In)
		if err := mw.InCodec.Validate(in); err != nil {
			return nil, nil, err
		}
		var topicVars, propertyVars map[string]string
		var err error
		if len(topicFields) > 0 {
			if topicVars, err = codex.EncodeVars(in, topicFields...); err != nil {
				return nil, nil, err
			}
		}
		if len(propertyFields) > 0 {
			if propertyVars, err = codex.EncodeVars(in, propertyFields...); err != nil {
				return nil, nil, err
			}
		}
		return topicVars, propertyVars, nil
	}
}

// buildDecodeOut is [buildEncodeIn]'s response-side sibling.
func buildDecodeOut[In, Out any](mw Middleware[In, Out]) func(topicVars, propertyVars map[string]string) (any, error) {
	topicFields := topicFieldsOf(mw.topicMergeFieldsOut)
	propertyFields := propertyFieldsOf(mw.propertyMergeFieldsOut)

	return func(topicVars, propertyVars map[string]string) (any, error) {
		var out Out
		if len(topicFields) > 0 {
			if err := codex.DecodeVars(&out, topicVars, topicFields...); err != nil {
				return nil, MiddlewareInputError{Name: mw.Name, Err: err}
			}
		}
		if len(propertyFields) > 0 {
			if err := codex.DecodeVars(&out, propertyVars, propertyFields...); err != nil {
				return nil, MiddlewareInputError{Name: mw.Name, Err: err}
			}
		}
		return out, nil
	}
}

// buildClientMiddlewareHandler builds a type-erased
// [ClientMiddlewareHandler] from a concrete [Middleware][In, Out] and its
// fn, mirroring [buildMiddlewareHandler]'s technique for the client role.
func buildClientMiddlewareHandler[Req, Resp, In, Out any](mw Middleware[In, Out], fn func(ctx context.Context, req Req) (In, error)) ClientMiddlewareHandler {
	return ClientMiddlewareHandler{
		Name:      mw.Name,
		Fn:        fn,
		EncodeIn:  buildEncodeIn(mw),
		DecodeOut: buildDecodeOut(mw),
	}
}

// buildAgnosticClientMiddlewareHandler builds a type-erased
// [ClientMiddlewareHandler] from a route-AGNOSTIC [Middleware][In, Out] —
// one attached via plain .Use(mw), bundled via [Middleware.WithSend].
func buildAgnosticClientMiddlewareHandler[In, Out any](mw Middleware[In, Out]) ClientMiddlewareHandler {
	return ClientMiddlewareHandler{
		Name:      mw.Name,
		Fn:        mw.sendFn,
		EncodeIn:  buildEncodeIn(mw),
		DecodeOut: buildDecodeOut(mw),
		Agnostic:  true,
	}
}

// middlewareHandlerOpt is the [RouteOpt] returned by [Transform].
type middlewareHandlerOpt struct{ handler MiddlewareHandler }

func (o middlewareHandlerOpt) applyRoute(rb *routeBuilder) {
	rb.middlewareHandlers = append(rb.middlewareHandlers, o.handler)
}

// clientMiddlewareHandlerOpt is the [RouteOpt] returned by [ClientTransform].
type clientMiddlewareHandlerOpt struct{ handler ClientMiddlewareHandler }

func (o clientMiddlewareHandlerOpt) applyRoute(rb *routeBuilder) {
	rb.clientMiddlewareHandlers = append(rb.clientMiddlewareHandlers, o.handler)
}

// middlewareSpecContribution captures the spec-relevant param
// declarations from ONE attached codec-backed [Middleware] — converted to
// plain, Req/Resp-agnostic spec types at the GENERIC call site (Transform/
// ClientTransform) where In/Out are still concrete. Fed into
// [applyParamDeclarations]'s conflict-detection/layering pass, unified
// with Phase 1b's flat mechanism.
type middlewareSpecContribution struct {
	name string

	topicParamsIn     []TopicParam
	topicParamsOut    []TopicParam
	propertyParamsIn  []PropertyParam
	propertyParamsOut []PropertyParam

	// dualAttached is true ONLY for a contribution built from [Transform]/
	// [ClientTransform] whose mw ALSO carries a WithReceive/WithSend Fn —
	// exactly D7's ambiguous case. A contribution built from the plain
	// .Use() path (applyAgnosticRoute) never sets this.
	dualAttached bool
}

// boundSpecContributionOf is [specContributionOf] plus D7's dualAttached
// flag — used ONLY by [Transform]/[ClientTransform].
func boundSpecContributionOf[In, Out any](mw Middleware[In, Out]) middlewareSpecContribution {
	c := specContributionOf(mw)
	c.dualAttached = mw.receiveFn != nil || mw.sendFn != nil
	return c
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
	return c
}

// middlewareSpecContributionOpt is the [RouteOpt] that layers a
// [middlewareSpecContribution] into rb.middlewareSpecContributions —
// attached alongside middlewareHandlerOpt/clientMiddlewareHandlerOpt by
// both Transform and ClientTransform.
type middlewareSpecContributionOpt struct{ contribution middlewareSpecContribution }

func (o middlewareSpecContributionOpt) applyRoute(rb *routeBuilder) {
	rb.middlewareSpecContributions = append(rb.middlewareSpecContributions, o.contribution)
}

// Transform attaches mw's declaration AND its runtime fn to r in ONE call
// — the route-BOUND, server-side attachment point for a codec-backed
// [Middleware]. fn receives ctx, the route's OWN already-decoded *Req
// (POINTER — fn may both read AND enrich it with derived data the wire
// request never carried), and mw's own decoded+validated In
// (declaratively extracted from REQUEST-side topic vars AND the
// adapter-supplied property vars the route's Req does NOT model at all).
// fn's returned Out is, in turn, derived declaratively into the reply's
// topic AND property vars via mw's own response merge-field
// declarations, composing with (not replacing) the route's own reply
// encoding.
//
// Dispatches AFTER the paired security Fn (if any), both still
// pre-handler — mirrors REST's/events' D1 precedent exactly.
//
// Multiple Transform/ClientTransform calls MAY be chained onto the SAME
// route — [RouteHandle.MiddlewareHandlers]/[RouteHandle.
// ClientMiddlewareHandlers] accumulate ACROSS calls, in registration
// order (mirrors [rest.Transform]'s real `append(slices.Clone(r.opts),
// ...)` pattern).
func Transform[Req, Resp, In, Out any](
	r Route[Req, Resp],
	mw Middleware[In, Out],
	fn func(ctx context.Context, req *Req, in In) (Out, error),
) Route[Req, Resp] {
	r.opts = append(slices.Clone(r.opts),
		middlewareHandlerOpt{handler: buildMiddlewareHandler[Req, Resp](mw, fn)},
		middlewareSpecContributionOpt{contribution: boundSpecContributionOf(mw)},
	)
	return r
}

// ClientTransform is [Transform]'s route-BOUND, client-side counterpart —
// fn PRODUCES mw's own In value from req (the caller's OWN already-built
// value, VALUE not pointer), encoded into the OUTGOING request's topic
// AND property vars via mw's own request merge-field declarations. AFTER
// the reply arrives, mw's own reply merge-field declarations MECHANICALLY
// decode Out from the reply's actual topic/property vars — no Fn needed
// for this half.
func ClientTransform[Req, Resp, In, Out any](
	r Route[Req, Resp],
	mw Middleware[In, Out],
	fn func(ctx context.Context, req Req) (In, error),
) Route[Req, Resp] {
	r.opts = append(slices.Clone(r.opts),
		clientMiddlewareHandlerOpt{handler: buildClientMiddlewareHandler[Req, Resp](mw, fn)},
		middlewareSpecContributionOpt{contribution: boundSpecContributionOf(mw)},
	)
	return r
}
