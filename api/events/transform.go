package events

import (
	"context"
	"slices"

	"github.com/DaniDeer/go-codex/codex"
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
	DecodeIn func(topicVars, propertyVars map[string]string) (any, error)

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
	EncodeOut func(out any) (topicVars, propertyVars map[string]string, err error)

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
}

// buildDecodeIn builds the DecodeIn closure shared by [buildMiddlewareHandler]
// (channel-BOUND) and [buildAgnosticMiddlewareHandler] (channel-AGNOSTIC).
// Decodes topicVars and propertyVars against &in SEPARATELY, in two
// sequential [codex.DecodeVars] calls, never combining the two maps — each
// call only touches the fields THAT AXIS declared, so calling DecodeVars
// twice against one &in is safe and side-effect-free across axes (mirrors
// REST's real multi-axis buildDecodeIn).
func buildDecodeIn[In, Out any](mw Middleware[In, Out]) func(topicVars, propertyVars map[string]string) (any, error) {
	topicFields := mw.topicMergeFieldsIn
	propFields := mw.propertyMergeFieldsIn
	return func(topicVars, propertyVars map[string]string) (any, error) {
		var in In
		if len(topicFields) > 0 {
			if err := codex.DecodeVars(&in, topicVars, topicFields...); err != nil {
				return nil, MiddlewareInputError{Name: mw.Name, Err: err}
			}
		}
		if len(propFields) > 0 {
			if err := codex.DecodeVars(&in, propertyVars, propFields...); err != nil {
				return nil, MiddlewareInputError{Name: mw.Name, Err: err}
			}
		}
		if err := mw.InCodec.Validate(in); err != nil {
			return nil, MiddlewareInputError{Name: mw.Name, Err: err}
		}
		return in, nil
	}
}

// buildEncodeOut builds the EncodeOut closure shared by
// [buildClientMiddlewareHandler] (channel-BOUND) and
// [buildAgnosticClientMiddlewareHandler] (channel-AGNOSTIC). Returns topic
// vars and property vars as TWO SEPARATE maps, never combined.
func buildEncodeOut[In, Out any](mw Middleware[In, Out]) func(outAny any) (topicVars, propertyVars map[string]string, err error) {
	topicFields := mw.topicMergeFieldsOut
	propFields := mw.propertyMergeFieldsOut
	return func(outAny any) (map[string]string, map[string]string, error) {
		out, _ := outAny.(Out)
		if err := mw.OutCodec.Validate(out); err != nil {
			return nil, nil, MiddlewareOutputError{Name: mw.Name, Err: err}
		}
		var topicVars, propertyVars map[string]string
		var err error
		if len(topicFields) > 0 {
			topicVars, err = codex.EncodeVars(out, topicFields...)
			if err != nil {
				return nil, nil, MiddlewareOutputError{Name: mw.Name, Err: err}
			}
		}
		if len(propFields) > 0 {
			propertyVars, err = codex.EncodeVars(out, propFields...)
			if err != nil {
				return nil, nil, MiddlewareOutputError{Name: mw.Name, Err: err}
			}
		}
		return topicVars, propertyVars, nil
	}
}

// buildMiddlewareHandler builds a type-erased [MiddlewareHandler] from a
// concrete [Middleware][In, Out] and its fn — In/Out are known here (the
// generic call site) and erased into plain closures where needed, so
// api/events never needs reflection for the decode half.
func buildMiddlewareHandler[T, In, Out any](mw Middleware[In, Out], fn func(ctx context.Context, msg *T, in In) error) MiddlewareHandler {
	return MiddlewareHandler{
		Name:           mw.Name,
		DecodeIn:       buildDecodeIn(mw),
		Fn:             fn,
		dualAttached:   mw.isBundled(),
		propertyParams: mw.propertyParamsIn,
	}
}

// buildAgnosticMiddlewareHandler builds a type-erased [MiddlewareHandler]
// from a channel-AGNOSTIC [Middleware][In, Out] — one attached via plain
// .Use(mw), bundled via [Middleware.WithReceive].
func buildAgnosticMiddlewareHandler[In, Out any](mw Middleware[In, Out]) MiddlewareHandler {
	return MiddlewareHandler{
		Name:           mw.Name,
		DecodeIn:       buildDecodeIn(mw),
		Fn:             mw.receiveFn,
		Agnostic:       true,
		propertyParams: mw.propertyParamsIn,
	}
}

// buildClientMiddlewareHandler builds a type-erased [ClientMiddlewareHandler]
// from a concrete [Middleware][In, Out] and its fn, mirroring
// [buildMiddlewareHandler]'s technique for the SENDING role.
func buildClientMiddlewareHandler[T, In, Out any](mw Middleware[In, Out], fn func(ctx context.Context, msg T) (Out, error)) ClientMiddlewareHandler {
	return ClientMiddlewareHandler{
		Name:           mw.Name,
		Fn:             fn,
		EncodeOut:      buildEncodeOut(mw),
		dualAttached:   mw.isBundled(),
		propertyParams: mw.propertyParamsOut,
	}
}

// buildAgnosticClientMiddlewareHandler builds a type-erased
// [ClientMiddlewareHandler] from a channel-AGNOSTIC [Middleware][In, Out]
// — one attached via plain .Use(mw), bundled via [Middleware.WithSend].
func buildAgnosticClientMiddlewareHandler[In, Out any](mw Middleware[In, Out]) ClientMiddlewareHandler {
	return ClientMiddlewareHandler{
		Name:           mw.Name,
		Fn:             mw.sendFn,
		EncodeOut:      buildEncodeOut(mw),
		Agnostic:       true,
		propertyParams: mw.propertyParamsOut,
	}
}

// Transform attaches mw's declaration AND its runtime fn to s in ONE call
// — the channel-BOUND, RECEIVING-role (subscribe) attachment point for a
// codec-backed [Middleware]. fn receives ctx, the channel's OWN
// already-decoded *T (POINTER — fn may both read AND enrich it with
// derived data the wire message never carried), and mw's own decoded+
// validated In (declaratively extracted from topic vars the channel's T
// does NOT model). Dispatches at the SAME pre-handler point
// runSubscribeSecurityImpls already runs at (D1) — see
// docs/design/d-0003-codec-declared-middlewares.md for the full design.
//
//	route = events.Transform(subscriber, regionPolicy,
//	    func(ctx context.Context, msg *SensorReading, in RegionIn) error {
//	        msg.Region = in.Region
//	        return nil
//	    })
func Transform[T, In, Out any](
	s Subscriber[T],
	mw Middleware[In, Out],
	fn func(ctx context.Context, msg *T, in In) error,
) Subscriber[T] {
	s.middlewareHandlers = append(slices.Clone(s.middlewareHandlers), buildMiddlewareHandler(mw, fn))
	return s
}

// ClientTransform is [Transform]'s channel-BOUND, SENDING-role (publish)
// counterpart. fn PRODUCES mw's own Out value from msg (the caller's OWN
// already-built value, available to inspect) — mw's own publish-topic
// merge fields then encode the returned Out into ADDITIONAL outgoing
// topic vars, merged alongside the channel's own topic vars.
//
//	pub = events.ClientTransform(publisher, regionPolicy,
//	    func(ctx context.Context, msg SensorReading) (RegionOut, error) {
//	        return RegionOut{Region: resolveRegion(msg)}, nil
//	    })
func ClientTransform[T, In, Out any](
	p Publisher[T],
	mw Middleware[In, Out],
	fn func(ctx context.Context, msg T) (Out, error),
) Publisher[T] {
	p.clientMiddlewareHandlers = append(slices.Clone(p.clientMiddlewareHandlers), buildClientMiddlewareHandler(mw, fn))
	return p
}
