package rest

import (
	"context"
	"slices"

	"github.com/DaniDeer/go-codex/codex"
)

// MiddlewareHandler is the type-erased, RECEIVING-role runtime dispatch
// unit built by [Transform] — the codec-backed-middleware counterpart to
// [middleware.ServerImplementation]. Stored on [RouteHandle.MiddlewareHandlers];
// consumed by each server adapter's own dispatch (nethttp/chi) via its
// reflect-based route serving — never constructed directly by callers.
//
// Deliberately has NO *http.Request (or any adapter-specific type)
// anywhere in its shape — api/rest stays transport-agnostic; DecodeIn
// works from plain string-keyed var maps (exactly like
// [RouteHandle.ApplyMergeFields] does for a route's own Req), and Fn is
// reflect-called by the adapter with the SAME already-decoded *Req the
// handler will also receive.
type MiddlewareHandler struct {
	// Name identifies this middleware in errors and observability.
	Name string

	// DecodeIn decodes+validates this middleware's own In value from the
	// SAME raw header/cookie/query var maps the route's own Req decode
	// uses — independent of Req, using ONLY this middleware's own
	// merge-field declarations and the middleware's own InCodec. Returns
	// the decoded In boxed as `any` (its concrete type is recovered by the
	// adapter via reflection, mirroring how [middleware.ServerImplementation.Fn]
	// is already reflect-called against a concrete Req).
	DecodeIn func(headerVars, cookieVars, queryVars map[string]string) (any, error)

	// Fn is the type-erased business Fn — concretely
	// func(ctx context.Context, req *Req, in In) (Out, error) — reflect-
	// called by the adapter, mirroring [middleware.ServerImplementation.Fn]'s
	// existing type-erasure technique exactly.
	Fn any

	// EncodeOut derives response header/cookie values from the Out value
	// returned by Fn, using ONLY this middleware's own response
	// merge-field declarations and the middleware's own OutCodec.
	EncodeOut func(out any) (headers map[string]string, cookies map[string]string, err error)

	// EncodeOutCookieAttrs derives declared [CookieAttributes] for every
	// response cookie mw.WithResponseCookie(...).WithAttributes(...)
	// declared, from the SAME Out value EncodeOut already derives cookie
	// VALUES from. nil when no cookie on this middleware declared
	// attributes.
	EncodeOutCookieAttrs func(out any) (map[string]CookieAttributes, error)

	// Agnostic is true when this handler was built from a route/channel-
	// AGNOSTIC attachment (plain .Use(mw), mw bundled via
	// [Middleware.WithReceive]) rather than [Transform] — in that case Fn's
	// ACTUAL shape is func(ctx context.Context, in In) (Out, error) (no
	// *Req parameter at all), since an agnostic mw is reused verbatim
	// across routes with different Req types and never accesses one. The
	// adapter must branch on this flag when reflect-calling Fn.
	Agnostic bool
}

// ClientMiddlewareHandler is the type-erased, SENDING-role runtime
// dispatch unit built by [ClientTransform] — the client-side mirror of
// [MiddlewareHandler]. Stored on [RouteHandle.ClientMiddlewareHandlers];
// consumed by [nethttp.Call]/[nethttp.CallWithHandle].
type ClientMiddlewareHandler struct {
	// Name identifies this middleware in errors and observability.
	Name string

	// Fn is the type-erased business Fn — concretely
	// func(ctx context.Context, req Req) (In, error) — reflect-called by
	// the client adapter with the caller's own already-built Req.
	Fn any

	// EncodeIn derives outgoing header/cookie/query values from the In
	// value returned by Fn, using ONLY this middleware's own request
	// merge-field declarations.
	EncodeIn func(in any) (headers, cookies, query map[string]string, err error)

	// DecodeOut derives the middleware's own Out value from the HTTP
	// response's actual headers/cookies — mechanical, no Fn — using ONLY
	// this middleware's own response merge-field declarations. Returns
	// the decoded Out boxed as `any`.
	DecodeOut func(headers, cookies map[string]string) (any, error)

	// Agnostic is true when this handler was built from a route/channel-
	// AGNOSTIC attachment (plain .Use(mw), mw bundled via
	// [Middleware.WithSend]) rather than [ClientTransform] — in that case
	// Fn's ACTUAL shape is func(ctx context.Context) (In, error) (no Req
	// parameter at all). The adapter must branch on this flag when
	// reflect-calling Fn.
	Agnostic bool
}

// middlewareFieldsToDecodeVarsFields converts a slice of Merged*Param
// values (each embedding a spec Param plus an unexported merge field) into
// plain []codex.FieldCodec[T] — shared by both the DecodeIn (request-side)
// and EncodeIn (client request-side) closures below.
func headerFieldsOf[T any](ps []MergedHeaderParam[T]) []codex.FieldCodec[T] {
	out := make([]codex.FieldCodec[T], len(ps))
	for i, p := range ps {
		out[i] = p.field
	}
	return out
}

func cookieFieldsOf[T any](ps []MergedCookieParam[T]) []codex.FieldCodec[T] {
	out := make([]codex.FieldCodec[T], len(ps))
	for i, p := range ps {
		out[i] = p.field
	}
	return out
}

func queryFieldsOf[T any](ps []MergedQueryParam[T]) []codex.FieldCodec[T] {
	out := make([]codex.FieldCodec[T], len(ps))
	for i, p := range ps {
		out[i] = p.field
	}
	return out
}

func responseHeaderFieldsOf[T any](ps []MergedResponseHeaderParam[T]) []codex.FieldCodec[T] {
	out := make([]codex.FieldCodec[T], len(ps))
	for i, p := range ps {
		out[i] = p.field
	}
	return out
}

func responseCookieFieldsOf[T any](ps []MergedResponseCookieParam[T]) []codex.FieldCodec[T] {
	out := make([]codex.FieldCodec[T], len(ps))
	for i, p := range ps {
		out[i] = p.field
	}
	return out
}

// buildDecodeIn builds the DecodeIn closure shared by [buildMiddlewareHandler]
// (route-BOUND) and [buildAgnosticMiddlewareHandler] (route-AGNOSTIC) — the
// decode logic itself never depends on Req, only on mw's own In/InCodec and
// merge-field declarations.
func buildDecodeIn[In, Out any](mw Middleware[In, Out]) func(headerVars, cookieVars, queryVars map[string]string) (any, error) {
	reqHeaderFields := headerFieldsOf(mw.reqHeaderParams)
	reqCookieFields := cookieFieldsOf(mw.reqCookieParams)
	reqQueryFields := queryFieldsOf(mw.reqQueryParams)

	return func(headerVars, cookieVars, queryVars map[string]string) (any, error) {
		var in In
		if len(reqHeaderFields) > 0 {
			if err := codex.DecodeVars(&in, headerVars, reqHeaderFields...); err != nil {
				return nil, MiddlewareInputError{Name: mw.Name, Err: err}
			}
		}
		if len(reqCookieFields) > 0 {
			if err := codex.DecodeVars(&in, cookieVars, reqCookieFields...); err != nil {
				return nil, MiddlewareInputError{Name: mw.Name, Err: err}
			}
		}
		if len(reqQueryFields) > 0 {
			if err := codex.DecodeVars(&in, queryVars, reqQueryFields...); err != nil {
				return nil, MiddlewareInputError{Name: mw.Name, Err: err}
			}
		}
		if err := mw.InCodec.Validate(in); err != nil {
			return nil, MiddlewareInputError{Name: mw.Name, Err: err}
		}
		return in, nil
	}
}

// buildEncodeOut is [buildDecodeIn]'s response-side sibling, shared by
// [buildMiddlewareHandler] and [buildAgnosticMiddlewareHandler].
func buildEncodeOut[In, Out any](mw Middleware[In, Out]) func(outAny any) (map[string]string, map[string]string, error) {
	respHeaderFields := responseHeaderFieldsOf(mw.respHeaderParams)
	respCookieFields := responseCookieFieldsOf(mw.respCookieParams)

	return func(outAny any) (map[string]string, map[string]string, error) {
		out, _ := outAny.(Out)
		if err := mw.OutCodec.Validate(out); err != nil {
			return nil, nil, MiddlewareOutputError{Name: mw.Name, Err: err}
		}
		var headers, cookies map[string]string
		var err error
		if len(respHeaderFields) > 0 {
			if headers, err = codex.EncodeVars(out, respHeaderFields...); err != nil {
				return nil, nil, MiddlewareOutputError{Name: mw.Name, Err: err}
			}
		}
		if len(respCookieFields) > 0 {
			if cookies, err = codex.EncodeVars(out, respCookieFields...); err != nil {
				return nil, nil, MiddlewareOutputError{Name: mw.Name, Err: err}
			}
		}
		return headers, cookies, nil
	}
}

// buildEncodeOutCookieAttrs builds the EncodeOutCookieAttrs closure shared
// by [buildMiddlewareHandler] (route-BOUND) and
// [buildAgnosticMiddlewareHandler] (route-AGNOSTIC) — reads each declared
// response cookie's attrsFn (set via
// [MergedResponseCookieParam.WithAttributes]) and derives its
// [CookieAttributes] from the SAME Out value [buildEncodeOut] derives
// cookie VALUES from. Returns nil when no cookie on mw declared
// attributes.
func buildEncodeOutCookieAttrs[In, Out any](mw Middleware[In, Out]) func(outAny any) (map[string]CookieAttributes, error) {
	return func(outAny any) (map[string]CookieAttributes, error) {
		out, _ := outAny.(Out)
		var attrs map[string]CookieAttributes
		for _, p := range mw.respCookieParams {
			if p.attrsFn == nil {
				continue
			}
			if attrs == nil {
				attrs = make(map[string]CookieAttributes, len(mw.respCookieParams))
			}
			attrs[p.Name] = p.attrsFn(out)
		}
		return attrs, nil
	}
}

// buildMiddlewareHandler builds a type-erased [MiddlewareHandler] from a
// concrete [Middleware][In, Out] and its fn — In/Out are known here (the
// generic call site) and erased into plain closures where needed, so
// api/rest never needs reflection for the decode/encode halves (unlike
// [middleware.ServerImplementation.Fn], which IS resolved via reflection
// by the ADAPTER, since it predates this generic mechanism and the
// adapter's own dispatch is itself fully reflect-based).
func buildMiddlewareHandler[Req, Resp, In, Out any](mw Middleware[In, Out], fn func(ctx context.Context, req *Req, in In) (Out, error)) MiddlewareHandler {
	return MiddlewareHandler{
		Name:                 mw.Name,
		DecodeIn:             buildDecodeIn(mw),
		Fn:                   fn,
		EncodeOut:            buildEncodeOut(mw),
		EncodeOutCookieAttrs: buildEncodeOutCookieAttrs(mw),
	}
}

// buildAgnosticMiddlewareHandler builds a type-erased [MiddlewareHandler]
// from a route/channel-AGNOSTIC [Middleware][In, Out] — one attached via
// plain .Use(mw), bundled via [Middleware.WithReceive] — mirroring
// [buildMiddlewareHandler] except Fn is mw's OWN bundled receiveFn
// (func(ctx, In) (Out, error), no *Req) and [MiddlewareHandler.Agnostic]
// is set so the adapter reflect-calls Fn with the matching arity.
func buildAgnosticMiddlewareHandler[In, Out any](mw Middleware[In, Out]) MiddlewareHandler {
	return MiddlewareHandler{
		Name:                 mw.Name,
		DecodeIn:             buildDecodeIn(mw),
		Fn:                   mw.receiveFn,
		EncodeOut:            buildEncodeOut(mw),
		EncodeOutCookieAttrs: buildEncodeOutCookieAttrs(mw),
		Agnostic:             true,
	}
}

// buildEncodeIn builds the EncodeIn closure shared by
// [buildClientMiddlewareHandler] (route-BOUND) and
// [buildAgnosticClientMiddlewareHandler] (route-AGNOSTIC).
func buildEncodeIn[In, Out any](mw Middleware[In, Out]) func(inAny any) (map[string]string, map[string]string, map[string]string, error) {
	reqHeaderFields := headerFieldsOf(mw.reqHeaderParams)
	reqCookieFields := cookieFieldsOf(mw.reqCookieParams)
	reqQueryFields := queryFieldsOf(mw.reqQueryParams)

	return func(inAny any) (map[string]string, map[string]string, map[string]string, error) {
		in, _ := inAny.(In)
		if err := mw.InCodec.Validate(in); err != nil {
			return nil, nil, nil, err
		}
		var headers, cookies, query map[string]string
		var err error
		if len(reqHeaderFields) > 0 {
			if headers, err = codex.EncodeVars(in, reqHeaderFields...); err != nil {
				return nil, nil, nil, err
			}
		}
		if len(reqCookieFields) > 0 {
			if cookies, err = codex.EncodeVars(in, reqCookieFields...); err != nil {
				return nil, nil, nil, err
			}
		}
		if len(reqQueryFields) > 0 {
			if query, err = codex.EncodeVars(in, reqQueryFields...); err != nil {
				return nil, nil, nil, err
			}
		}
		return headers, cookies, query, nil
	}
}

// buildDecodeOut is [buildEncodeIn]'s response-side sibling, shared by
// [buildClientMiddlewareHandler] and [buildAgnosticClientMiddlewareHandler].
func buildDecodeOut[In, Out any](mw Middleware[In, Out]) func(headers, cookies map[string]string) (any, error) {
	respHeaderFields := responseHeaderFieldsOf(mw.respHeaderParams)
	respCookieFields := responseCookieFieldsOf(mw.respCookieParams)

	return func(headers, cookies map[string]string) (any, error) {
		var out Out
		if len(respHeaderFields) > 0 {
			if err := codex.DecodeVars(&out, headers, respHeaderFields...); err != nil {
				return nil, MiddlewareInputError{Name: mw.Name, Err: err}
			}
		}
		if len(respCookieFields) > 0 {
			if err := codex.DecodeVars(&out, cookies, respCookieFields...); err != nil {
				return nil, MiddlewareInputError{Name: mw.Name, Err: err}
			}
		}
		return out, nil
	}
}

// buildClientMiddlewareHandler builds a type-erased [ClientMiddlewareHandler]
// from a concrete [Middleware][In, Out] and its fn, mirroring
// [buildMiddlewareHandler]'s technique for the SENDING role.
func buildClientMiddlewareHandler[Req, Resp, In, Out any](mw Middleware[In, Out], fn func(ctx context.Context, req Req) (In, error)) ClientMiddlewareHandler {
	return ClientMiddlewareHandler{
		Name:      mw.Name,
		Fn:        fn,
		EncodeIn:  buildEncodeIn(mw),
		DecodeOut: buildDecodeOut(mw),
	}
}

// buildAgnosticClientMiddlewareHandler builds a type-erased
// [ClientMiddlewareHandler] from a route/channel-AGNOSTIC
// [Middleware][In, Out] — one attached via plain .Use(mw), bundled via
// [Middleware.WithSend] — mirroring [buildClientMiddlewareHandler] except
// Fn is mw's OWN bundled sendFn (func(ctx) (In, error), no Req) and
// [ClientMiddlewareHandler.Agnostic] is set so the adapter reflect-calls
// Fn with the matching arity.
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
// applyParamDeclarations' conflict-detection/layering pass, the SAME one
// legacy middleware.Middleware values already use (D4).
type middlewareSpecContribution struct {
	name             string
	reqHeaderParams  []HeaderParam
	reqCookieParams  []CookieParam
	reqQueryParams   []QueryParam
	respHeaderParams []ResponseHeaderParam
	respCookieParams []ResponseCookieParam

	// dualAttached is true ONLY for a contribution built from [Transform]/
	// [ClientTransform] whose mw ALSO carries a WithReceive/WithSend Fn —
	// exactly D7's ambiguous case (bound AND agnostic attachment styles
	// combined on the SAME value). A contribution built from the plain
	// .Use() path (applyAgnosticRoute) never sets this — bundled there is
	// the WHOLE POINT of that attachment style, not a conflict.
	dualAttached bool
}

// boundSpecContributionOf is [specContributionOf] plus D7's dualAttached
// flag — used ONLY by [Transform]/[ClientTransform] (the route/channel-
// BOUND attachment path), never by the .Use()-agnostic path.
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
	for _, p := range mw.reqHeaderParams {
		c.reqHeaderParams = append(c.reqHeaderParams, p.HeaderParam)
	}
	for _, p := range mw.reqCookieParams {
		c.reqCookieParams = append(c.reqCookieParams, p.CookieParam)
	}
	for _, p := range mw.reqQueryParams {
		c.reqQueryParams = append(c.reqQueryParams, p.QueryParam)
	}
	for _, p := range mw.respHeaderParams {
		c.respHeaderParams = append(c.respHeaderParams, p.ResponseHeaderParam)
	}
	for _, p := range mw.respCookieParams {
		c.respCookieParams = append(c.respCookieParams, p.ResponseCookieParam)
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
// — the route/channel-BOUND, RECEIVING-role attachment point for a
// codec-backed [Middleware]. fn receives ctx, the route's OWN
// already-decoded *Req (POINTER — fn may both read AND enrich it with
// derived data the wire request never carried, mirroring the
// security-shaped Fn's existing *Req access), and mw's own decoded+
// validated In (declaratively extracted from header/cookie/query values
// the route's Req does NOT model at all). fn's returned Out is, in turn,
// derived declaratively into actual HTTP response headers/cookies via
// mw's own response merge-field declarations, composing with (not
// replacing) the route's own [RouteHandle.EncodeResponseMergeFields]
// output.
//
// Adopts the name of the ALREADY-EXISTING [nethttp.Transform] — the SAME
// concept ("a Req-bound enrichment Fn"), generalized here to also cover
// mw's own In/Out — see docs/design/d-0003-codec-declared-middlewares.md.
//
// Transform is a free function, not a method — Go disallows type
// parameters on a method beyond its receiver's own; In/Out are inferred
// from mw/fn directly, so a wrong-shaped fn is a Go COMPILE error, never a
// runtime one.
//
//	sessionCookiePolicy := rest.NewMiddleware(
//	    middleware.NewDeclaration[struct{}, CookieAttrs]("session-cookie-policy", codex.Struct[struct{}](), cookieAttrsCodec),
//	).WithResponseCookie(rest.NewRequiredResponseCookieParam("session", valueCodec,
//	    func(a CookieAttrs) string { return a.Value },
//	    func(a *CookieAttrs, v string) { a.Value = v },
//	))
//
//	route = rest.Transform(route, sessionCookiePolicy,
//	    func(ctx context.Context, req *LoginReq, _ struct{}) (CookieAttrs, error) {
//	        return CookieAttrs{Value: newSessionToken(), MaxAge: 3600, Insecure: true}, nil
//	    })
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

// ClientTransform is [Transform]'s route/channel-BOUND, SENDING-role
// counterpart — the client-side mirror. fn PRODUCES mw's own In value
// (mirrors a credential Fn producing a value to send, rather than
// decoding one that arrived), with the caller's OWN already-built Req
// value (VALUE, not pointer — the caller already owns and can mutate its
// own Req directly before calling Call at all) available to inspect. mw's
// own request header/cookie/query merge fields are reused for ENCODING
// the returned In, merged into the outgoing call per the documented
// 3-tier precedence (explicit CallOptions > middleware-derived >
// route-own-derived). After the response, mw's own response merge fields
// mechanically decode Out from the response's actual headers/cookies.
//
//	route = rest.ClientTransform(route, apiKeyPolicy,
//	    func(ctx context.Context, req GetProfileReq) (APIKeyIn, error) {
//	        return APIKeyIn{Key: os.Getenv("MY_API_KEY")}, nil
//	    })
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

// TransformSSE is [Transform]'s [SSERoute] counterpart — the SAME
// route/channel-BOUND, RECEIVING-role attachment point, migrated onto
// SSE's Req/Event shape (Event plays [Transform]'s Resp role: mw's own
// response header/cookie merge fields, once EncodeOut runs, compose
// alongside [SSERouteHandle.EncodeResponseMergeFields]'s own Event-derived
// values — see docs/design/d-0003-codec-declared-middlewares.md's SSE
// migration section for the exact dispatch timing: BEFORE SSE's own
// headers (Content-Type: text/event-stream, etc.) are committed).
//
//	sseRoute = rest.TransformSSE(sseRoute, sessionCookiePolicy,
//	    func(ctx context.Context, req *StreamReq, _ struct{}) (CookieAttrs, error) {
//	        return CookieAttrs{Value: newSessionToken(), MaxAge: 3600, Insecure: true}, nil
//	    })
func TransformSSE[Req, Event, In, Out any](
	s SSERoute[Req, Event],
	mw Middleware[In, Out],
	fn func(ctx context.Context, req *Req, in In) (Out, error),
) SSERoute[Req, Event] {
	s.opts = append(slices.Clone(s.opts),
		middlewareHandlerOpt{handler: buildMiddlewareHandler[Req, Event](mw, fn)},
		middlewareSpecContributionOpt{contribution: boundSpecContributionOf(mw)},
	)
	return s
}

// ClientTransformSSE is [ClientTransform]'s [SSERoute] counterpart — the
// SAME route/channel-BOUND, SENDING-role attachment point, migrated onto
// SSE's Req/Event shape. mw's own response merge fields decode Out ONCE,
// at connection-open time (NOT re-decoded per event) — see
// docs/design/d-0003-codec-declared-middlewares.md's SSE migration section.
//
//	sseRoute = rest.ClientTransformSSE(sseRoute, apiKeyPolicy,
//	    func(ctx context.Context, req StreamReq) (APIKeyIn, error) {
//	        return APIKeyIn{Key: os.Getenv("MY_API_KEY")}, nil
//	    })
func ClientTransformSSE[Req, Event, In, Out any](
	s SSERoute[Req, Event],
	mw Middleware[In, Out],
	fn func(ctx context.Context, req Req) (In, error),
) SSERoute[Req, Event] {
	s.opts = append(slices.Clone(s.opts),
		clientMiddlewareHandlerOpt{handler: buildClientMiddlewareHandler[Req, Event](mw, fn)},
		middlewareSpecContributionOpt{contribution: boundSpecContributionOf(mw)},
	)
	return s
}
