package rest

import (
	"context"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/internal/middleware"
)

// MiddlewareHandler is the type-erased, RECEIVING-role runtime dispatch
// unit built by [Route.HandleBoundMW] (bound class) or a plain .Use()'d
// [Middleware] carrying a [Middleware.WithReceive] Fn (reusable class) —
// the codec-backed-middleware counterpart to
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
	// is already reflect-called against a concrete Req). ctx is the
	// SAME request-scoped ctx [DispatchMiddlewareHandlers] was called
	// with — needed so an attached [Middleware.SetContextFieldFromIn]
	// ContextFieldSetter can publish into the SAME box
	// [middleware.EnsureContextFields] pre-allocated (docs/roadmap/
	// declarative-middleware-layering.md's Rollout Phase A).
	DecodeIn func(ctx context.Context, headerVars, cookieVars, queryVars map[string]string) (any, error)

	// Fn is the type-erased business Fn — concretely
	// func(ctx context.Context, req *Req, in In) (Out, error) — reflect-
	// called by the adapter, mirroring [middleware.ServerImplementation.Fn]'s
	// existing type-erasure technique exactly.
	Fn any

	// EncodeOut derives response header/cookie values from the Out value
	// returned by Fn, using ONLY this middleware's own response
	// merge-field declarations and the middleware's own OutCodec. ctx is
	// the SAME request-scoped ctx — needed for
	// [Middleware.SetContextFieldFromOut] (see [MiddlewareHandler.DecodeIn]'s
	// doc comment for the full rationale).
	EncodeOut func(ctx context.Context, out any) (headers map[string]string, cookies map[string]string, err error)

	// EncodeOutCookieAttrs derives declared [CookieAttributes] for every
	// response cookie mw.WithResponseCookie(...).WithAttributes(...)
	// declared, from the SAME Out value EncodeOut already derives cookie
	// VALUES from. nil when no cookie on this middleware declared
	// attributes.
	EncodeOutCookieAttrs func(out any) (map[string]CookieAttributes, error)

	// Agnostic is true when this handler was built from a route/channel-
	// AGNOSTIC attachment (plain .Use(mw), mw bundled via
	// [Middleware.WithReceive]) rather than a [BoundMiddleware] attached
	// via [Route.HandleBoundMW] — in that case Fn's ACTUAL shape is
	// func(ctx context.Context, in In) (Out, error) (no *Req parameter at
	// all), since an agnostic mw is reused verbatim across routes with
	// different Req types and never accesses one. The adapter must
	// branch on this flag when reflect-calling Fn.
	Agnostic bool

	// Satisfies names the security scheme(s) this handler PAIRS against,
	// derived from mw's own Security declaration (see [satisfiesOf]) —
	// empty for a general-purpose (no Security) middleware, which
	// [CheckCoverage] always treats as non-covering.
	// Mirrors [middleware.ServerImplementation.Satisfies] exactly, so
	// [CheckCoverage] can check BOTH lists uniformly.
	Satisfies []string
}

// ClientMiddlewareHandler is the type-erased, SENDING-role runtime
// dispatch unit built by [Route.ClientBoundMW] (bound class) or a plain
// .Use()'d [Middleware] carrying a [Middleware.WithSend] Fn (reusable
// class) — the client-side mirror of [MiddlewareHandler]. Stored on
// [RouteHandle.ClientMiddlewareHandlers];
// consumed by nethttp's internal client dispatch (shared by
// [CallWithTransport] and the handle-based binding adapters).
type ClientMiddlewareHandler struct {
	// Name identifies this middleware in errors and observability.
	Name string

	// Fn is the type-erased business Fn — concretely
	// func(ctx context.Context, req Req) (In, error) — reflect-called by
	// the client adapter with the caller's own already-built Req.
	Fn any

	// EncodeIn derives outgoing header/cookie/query values from the In
	// value returned by Fn, using ONLY this middleware's own request
	// merge-field declarations. ctx is the SAME call-scoped ctx — needed
	// for [Middleware.SetContextFieldFromIn] on the client side (see
	// [MiddlewareHandler.DecodeIn]'s doc comment for the full rationale).
	EncodeIn func(ctx context.Context, in any) (headers, cookies, query map[string]string, err error)

	// DecodeOut derives the middleware's own Out value from the HTTP
	// response's actual headers/cookies — mechanical, no Fn — using ONLY
	// this middleware's own response merge-field declarations. Returns
	// the decoded Out boxed as `any`. ctx is the SAME call-scoped ctx —
	// needed for [Middleware.SetContextFieldFromOut] on the client side.
	DecodeOut func(ctx context.Context, headers, cookies map[string]string) (any, error)

	// Agnostic is true when this handler was built from a route/channel-
	// AGNOSTIC attachment (plain .Use(mw), mw bundled via
	// [Middleware.WithSend]) rather than a [BoundClientMiddleware]
	// attached via [Route.ClientBoundMW] — in that case Fn's ACTUAL shape
	// is func(ctx context.Context) (In, error) (no Req parameter at all).
	// The adapter must branch on this flag when reflect-calling Fn.
	Agnostic bool

	// Satisfies names the security scheme(s) this handler PAIRS against —
	// see [MiddlewareHandler.Satisfies]'s doc comment for the full
	// rationale, applied identically to the client/SENDING role.
	Satisfies []string
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
// merge-field declarations. Internally a thin wrapper over
// [middleware.DecodeLayer] (docs/design/d-0007-declarative-middleware-layering.md's
// Rollout Phase A) — behavior is UNCHANGED: same 3-axis order
// (header, cookie, query), same fail-fast-at-first-error semantics, same
// [MiddlewareInputError] wrapping.
func buildDecodeIn[In, Out any](mw Middleware[In, Out]) func(ctx context.Context, headerVars, cookieVars, queryVars map[string]string) (any, error) {
	reqHeaderFields := headerFieldsOf(mw.reqHeaderParams)
	reqCookieFields := cookieFieldsOf(mw.reqCookieParams)
	reqQueryFields := queryFieldsOf(mw.reqQueryParams)
	wrapErr := func(err error) error { return MiddlewareInputError{Name: mw.Name, Err: err} }
	ctxFieldsFromIn := mw.ctxFieldsFromIn

	return func(ctx context.Context, headerVars, cookieVars, queryVars map[string]string) (any, error) {
		in, err := middleware.DecodeLayer([]middleware.Axis[In]{
			{Fields: reqHeaderFields, Vars: headerVars},
			{Fields: reqCookieFields, Vars: cookieVars},
			{Fields: reqQueryFields, Vars: queryVars},
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

// buildEncodeOut is [buildDecodeIn]'s response-side sibling, shared by
// [buildMiddlewareHandler] and [buildAgnosticMiddlewareHandler]. Internally
// a thin wrapper over [middleware.EncodeLayer] — behavior is UNCHANGED:
// same 2-axis order (header, cookie), same fail-fast semantics, same
// [MiddlewareOutputError] wrapping, same nil-map-when-no-fields contract.
func buildEncodeOut[In, Out any](mw Middleware[In, Out]) func(ctx context.Context, outAny any) (map[string]string, map[string]string, error) {
	respHeaderFields := responseHeaderFieldsOf(mw.respHeaderParams)
	respCookieFields := responseCookieFieldsOf(mw.respCookieParams)
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
			respHeaderFields,
			respCookieFields,
		}, wrapErr)
		if err != nil {
			return nil, nil, err
		}
		return vars[0], vars[1], nil
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

// satisfiesOf derives the Satisfies slice shared by every
// MiddlewareHandler/ClientMiddlewareHandler builder (bound AND agnostic,
// both roles) from mw's own [middleware.SecurityCarrier]-derived Security
// declaration — empty for a general-purpose (no Security) middleware, so
// [CheckCoverage] can treat a codec-backed Middleware's dispatch handler
// identically to a general-purpose [middleware.ServerImplementation]/
// [middleware.ClientImplementation] regardless of which attachment style
// produced it.
func satisfiesOf[In, Out any](mw Middleware[In, Out]) []string {
	if sec := mw.SecurityDeclaration(); sec != nil {
		return []string{sec.SchemeName}
	}
	return nil
}

// buildMiddlewareHandlerAny builds a type-erased [MiddlewareHandler] from
// a concrete [Middleware][In, Out] and an UNTYPED fn — the SOLE builder
// for the RECEIVING role, used by both [BoundMiddleware.ApplyBoundRoute]
// (bound class, attached via [Route.HandleBoundMW]) and
// [buildAgnosticMiddlewareHandler] (reusable class): fn is already `any`
// on [MiddlewareHandler.Fn] itself, so a SEPARATE `any`-typed builder
// needs zero new type parameters beyond In/Out (the receiver mw already
// carries).
func buildMiddlewareHandlerAny[In, Out any](mw Middleware[In, Out], fn any) MiddlewareHandler {
	return MiddlewareHandler{
		Name:                 mw.Name,
		DecodeIn:             buildDecodeIn(mw),
		Fn:                   fn,
		EncodeOut:            buildEncodeOut(mw),
		EncodeOutCookieAttrs: buildEncodeOutCookieAttrs(mw),
		Satisfies:            satisfiesOf(mw),
	}
}

// buildAgnosticMiddlewareHandler builds a type-erased [MiddlewareHandler]
// from a route/channel-AGNOSTIC [Middleware][In, Out] — one attached via
// plain .Use(mw), bundled via [Middleware.WithReceive] — mirroring the
// bound class's [BoundMiddleware.ApplyBoundRoute] except Fn is mw's OWN
// bundled receiveFn (func(ctx, In) (Out, error), no *Req) and
// [MiddlewareHandler.Agnostic] is set so the adapter reflect-calls Fn
// with the matching arity.
func buildAgnosticMiddlewareHandler[In, Out any](mw Middleware[In, Out]) MiddlewareHandler {
	h := buildMiddlewareHandlerAny(mw, mw.receiveFn)
	h.Agnostic = true
	return h
}

// buildEncodeIn builds the EncodeIn closure shared by
// [buildClientMiddlewareHandler] (route-BOUND) and
// [buildAgnosticClientMiddlewareHandler] (route-AGNOSTIC). Internally a
// thin wrapper over [middleware.EncodeLayer] — behavior is UNCHANGED: same
// 3-axis order (header, cookie, query), same fail-fast semantics, same
// raw (unwrapped) error return.
func buildEncodeIn[In, Out any](mw Middleware[In, Out]) func(ctx context.Context, inAny any) (map[string]string, map[string]string, map[string]string, error) {
	reqHeaderFields := headerFieldsOf(mw.reqHeaderParams)
	reqCookieFields := cookieFieldsOf(mw.reqCookieParams)
	reqQueryFields := queryFieldsOf(mw.reqQueryParams)
	wrapErr := func(err error) error { return err }
	ctxFieldsFromIn := mw.ctxFieldsFromIn

	return func(ctx context.Context, inAny any) (map[string]string, map[string]string, map[string]string, error) {
		in, _ := inAny.(In)
		if err := mw.InCodec.Validate(in); err != nil {
			return nil, nil, nil, err
		}
		for _, cf := range ctxFieldsFromIn {
			if err := cf.field.Set(ctx, cf.get(in)); err != nil {
				return nil, nil, nil, err
			}
		}
		vars, err := middleware.EncodeLayer(in, [][]codex.FieldCodec[In]{
			reqHeaderFields,
			reqCookieFields,
			reqQueryFields,
		}, wrapErr)
		if err != nil {
			return nil, nil, nil, err
		}
		return vars[0], vars[1], vars[2], nil
	}
}

// buildDecodeOut is [buildEncodeIn]'s response-side sibling, shared by
// [buildClientMiddlewareHandler] and [buildAgnosticClientMiddlewareHandler].
// Internally a thin wrapper over [middleware.DecodeLayer] — behavior is
// UNCHANGED: same 2-axis order (header, cookie), same fail-fast semantics,
// same [MiddlewareInputError] wrapping (preserved verbatim even on the
// OUT-decode direction — matches this function's own prior, established
// behavior exactly, not "fixed" by this refactor).
func buildDecodeOut[In, Out any](mw Middleware[In, Out]) func(ctx context.Context, headers, cookies map[string]string) (any, error) {
	respHeaderFields := responseHeaderFieldsOf(mw.respHeaderParams)
	respCookieFields := responseCookieFieldsOf(mw.respCookieParams)
	wrapErr := func(err error) error { return MiddlewareInputError{Name: mw.Name, Err: err} }
	ctxFieldsFromOut := mw.ctxFieldsFromOut

	return func(ctx context.Context, headers, cookies map[string]string) (any, error) {
		out, err := middleware.DecodeLayer([]middleware.Axis[Out]{
			{Fields: respHeaderFields, Vars: headers},
			{Fields: respCookieFields, Vars: cookies},
		}, wrapErr)
		if err != nil {
			return nil, err
		}
		for _, cf := range ctxFieldsFromOut {
			if err := cf.field.Set(ctx, cf.get(out)); err != nil {
				return nil, MiddlewareInputError{Name: mw.Name, Err: err}
			}
		}
		return out, nil
	}
}

// buildClientMiddlewareHandlerAny builds a type-erased
// [ClientMiddlewareHandler] from a concrete [Middleware][In, Out] and an
// UNTYPED fn — the SENDING-role mirror of [buildMiddlewareHandlerAny],
// used by both [BoundClientMiddleware.ApplyBoundClientRoute] (bound
// class, attached via [Route.ClientBoundMW]) and
// [buildAgnosticClientMiddlewareHandler] (reusable class).
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
// [ClientMiddlewareHandler] from a route/channel-AGNOSTIC
// [Middleware][In, Out] — one attached via plain .Use(mw), bundled via
// [Middleware.WithSend] — mirroring the bound class's
// [BoundClientMiddleware.ApplyBoundClientRoute] except Fn is mw's OWN
// bundled sendFn (func(ctx) (In, error), no Req) and
// [ClientMiddlewareHandler.Agnostic] is set so the adapter reflect-calls
// Fn with the matching arity.
func buildAgnosticClientMiddlewareHandler[In, Out any](mw Middleware[In, Out]) ClientMiddlewareHandler {
	h := buildClientMiddlewareHandlerAny(mw, mw.sendFn)
	h.Agnostic = true
	return h
}

// middlewareSpecContribution captures the spec-relevant param
// declarations from ONE attached codec-backed middleware value — either
// class (the reusable [Middleware], via its own applyAgnosticRoute
// method, or the bound [BoundMiddleware]/[BoundClientMiddleware], via
// their own ApplyBoundRoute/ApplyBoundClientRoute methods) — converted
// to plain, Req/Resp-agnostic spec types at the GENERIC call site where
// In/Out are still concrete. Fed into applyParamDeclarations'
// conflict-detection/layering pass, the SAME one legacy
// middleware.Middleware values already use (D4).
type middlewareSpecContribution struct {
	name             string
	reqHeaderParams  []HeaderParam
	reqCookieParams  []CookieParam
	reqQueryParams   []QueryParam
	respHeaderParams []ResponseHeaderParam
	respCookieParams []ResponseCookieParam
}

// boundSpecContributionOf is [specContributionOf], called from the bound
// class's own ApplyBoundRoute/ApplyBoundClientRoute methods
// (bound_middleware.go) — kept as a separate, identically-named entry
// point for call-site clarity/symmetry with the reusable class's own
// applyAgnosticRoute call to specContributionOf directly, even though it
// is now a plain passthrough: it used to ALSO compute a dualAttached
// flag for D7's (now-deleted, docs/design/d-0003-codec-declared-middlewares.md's Addendum 7)
// ambiguous-dual-attachment check, which is structurally impossible
// since BoundMiddleware/BoundClientMiddleware are different Go types
// from Middleware and can never carry a WithReceive/WithSend Fn.
func boundSpecContributionOf[In, Out any](mw Middleware[In, Out]) middlewareSpecContribution {
	return specContributionOf(mw)
}

// specContributionOf extracts mw's plain spec Param values (discarding
// the merge field half, which is only needed at DecodeIn/EncodeOut time,
// already handled by buildMiddlewareHandlerAny/buildClientMiddlewareHandlerAny).
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
	c.reqHeaderParams = append(c.reqHeaderParams, mw.reqHeaderSpecs...)
	c.reqCookieParams = append(c.reqCookieParams, mw.reqCookieSpecs...)
	c.reqQueryParams = append(c.reqQueryParams, mw.reqQuerySpecs...)
	c.respHeaderParams = append(c.respHeaderParams, mw.respHeaderSpecs...)
	c.respCookieParams = append(c.respCookieParams, mw.respCookieSpecs...)
	return c
}
