package rest

import (
	"context"
	"fmt"
	"log/slog"
	"slices"

	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/route"
)

// middlewareOpt is the [RouteOpt] returned by [WithMiddleware]. It only
// accumulates mws into rb.middlewares — the actual Security/RequestParams/
// ResponseParams application (merging into the route's security
// requirements/params, plus conflict detection) happens ONCE, at
// Register/ValidateRoute time, via [applyMiddlewareDeclarations] — order-
// independent regardless of where WithMiddleware appears among a route's
// other RouteOpts.
type middlewareOpt struct{ mws []middleware.Middleware }

func (o middlewareOpt) applyRoute(rb *routeBuilder) {
	rb.middlewares = append(rb.middlewares, o.mws...)
}

// WithMiddleware attaches one or more [middleware.Middleware] values to a
// route at declaration time — the spec-relevant attachment point. A
// middleware carrying a non-nil Security declares a security scheme +
// requirement for THIS route (fed into the OpenAPI spec exactly as if the
// route had set [RouteMeta.Security] and a [SecurityScheme] manually); a
// middleware carrying RequestParams/ResponseParams declares additional
// header/cookie/query param spec entries. A route needs ZERO manual
// RouteMeta.Security/param calls when the attached middleware already
// carries a complete declaration.
//
// Attaching a general-purpose (non-declarative) middleware — one with a nil
// Security and empty RequestParams/ResponseParams — is also valid here; it
// simply has no spec effect.
func WithMiddleware(mws ...middleware.Middleware) RouteOpt {
	return middlewareOpt{mws: mws}
}

// FromHeaderParam/FromCookieParam/FromQueryParam/FromResponseHeaderParam/
// FromResponseCookieParam bridge an EXISTING [HeaderParam]/[CookieParam]/
// [QueryParam]/[ResponseHeaderParam]/[ResponseCookieParam] value (e.g. a
// package-level var shared across several routes) into a real
// [middleware.Middleware], usable with [Route.Use] exactly like one built
// from scratch. Mirror [FromSecurityScheme]'s "wrap what you already have"
// pattern.
//
// Live in api/rest, NOT middleware — for the SAME reason [FromSecurityScheme]
// does: middleware cannot import api/rest without a cycle (api/rest already
// imports middleware). Each is a 1-line field copy into the matching
// middleware-package-local spec type (see [middleware.HeaderParamSpec]'s
// doc comment).
//
//	var apiKeyHeader = rest.HeaderParam{Name: "X-API-Key", Required: true}
//
//	route := rest.NewRoute[Req, Resp]("GET", "/data", reqCodec, respCodec,
//	    rest.RouteMeta{OperationID: "getData"},
//	).Use(rest.FromHeaderParam(apiKeyHeader)).
//	    HandleMW(nil, func(ctx context.Context, r *http.Request, req *Req) (map[string][]string, error) {
//	        return nil, verify(ctx, r.Header.Get("X-API-Key"))
//	    })
func FromHeaderParam(p HeaderParam) middleware.Middleware {
	return middleware.Middleware{
		Name: "declare-header-param:" + p.Name,
		RequestHeaderParams: []middleware.HeaderParamSpec{
			{Name: p.Name, Description: p.Description, Required: p.Required, Codec: p.Codec},
		},
	}
}

// FromCookieParam is [FromHeaderParam]'s cookie-request-param sibling.
func FromCookieParam(p CookieParam) middleware.Middleware {
	return middleware.Middleware{
		Name: "declare-cookie-param:" + p.Name,
		RequestCookieParams: []middleware.CookieParamSpec{
			{Name: p.Name, Description: p.Description, Required: p.Required, Codec: p.Codec},
		},
	}
}

// FromQueryParam is [FromHeaderParam]'s query-param sibling.
func FromQueryParam(p QueryParam) middleware.Middleware {
	return middleware.Middleware{
		Name: "declare-query-param:" + p.Name,
		RequestQueryParams: []middleware.QueryParamSpec{
			{Name: p.Name, Description: p.Description, Required: p.Required, Codec: p.Codec},
		},
	}
}

// FromResponseHeaderParam is [FromHeaderParam]'s response-header sibling.
func FromResponseHeaderParam(p ResponseHeaderParam) middleware.Middleware {
	return middleware.Middleware{
		Name: "declare-response-header-param:" + p.Name,
		ResponseHeaderParams: []middleware.ResponseHeaderParamSpec{
			{Name: p.Name, Description: p.Description, Required: p.Required, Codec: p.Codec},
		},
	}
}

// FromResponseCookieParam is [FromHeaderParam]'s response-cookie sibling.
func FromResponseCookieParam(p ResponseCookieParam) middleware.Middleware {
	return middleware.Middleware{
		Name: "declare-response-cookie-param:" + p.Name,
		ResponseCookieParams: []middleware.ResponseCookieParamSpec{
			{Name: p.Name, Description: p.Description, Required: p.Required, Codec: p.Codec},
		},
	}
}

// Use returns a NEW [Route] with mws chained onto it — chi/net-http-style
// declaration-time sugar for [WithMiddleware], usable AFTER [NewRoute]
// instead of only as one of its variadic opts:
//
//	handle, err := rest.NewRoute[GetProfileReq, ProfileResp]("GET", "/profile",
//	    reqCodec, respCodec, rest.RouteMeta{OperationID: "getProfile"},
//	).Use(scopesFromProxy).Register(builder)
//
// Chainable — `.Use(mw1).Use(mw2)` and `.Use(mw1, mw2)` are equivalent, in
// attachment order. [Route] is an immutable value; Use never mutates the
// receiver — it returns a distinct Route, leaving any Route it was called
// on (and any OTHER Route derived from the same base via a different
// `.Use(...)` call) unchanged. Register/ClientHandle still perform the
// SAME, order-independent application pass documented on [WithMiddleware]
// — Use is purely how the opts list is assembled beforehand; it introduces
// no new runtime mechanism.
func (r Route[Req, Resp]) Use(mws ...middleware.Middleware) Route[Req, Resp] {
	r.opts = append(slices.Clone(r.opts), WithMiddleware(mws...))
	return r
}

// Use is [SSERoute]'s equivalent of [Route.Use] — see its doc comment for
// the full chaining/immutability contract, which applies identically here.
func (s SSERoute[Req, Event]) Use(mws ...middleware.Middleware) SSERoute[Req, Event] {
	s.opts = append(slices.Clone(s.opts), WithMiddleware(mws...))
	return s
}

// NOTE: WithClientMiddleware/Route.UseClient (the client-side counterpart
// to WithMiddleware/Route.Use, attaching [middleware.ClientImplementation]
// values) were REMOVED — fully replaced by [Route.ClientMW], which pairs
// a client-side implementation directly against the SAME [middleware.Middleware]
// value attached via [Route.Use], eliminating the schemeName-typo risk
// UseClient's separate re-declaration carried. See
// docs/design/middleware-workflow-simplification.md's "Decision:
// symmetric client-side declarative wiring".

// handlerOpt is the [RouteOpt] returned by [Route.WithHandler]/
// [SSERoute.WithHandler]. Stores fn type-erased into rb.handlerFn,
// resolved by the consuming adapter (nethttp/chi) at Register/Serve time.
type handlerOpt struct{ fn any }

func (o handlerOpt) applyRoute(rb *routeBuilder) { rb.handlerFn = o.fn }

// WithHandler returns a NEW [Route] with fn attached as the route's own
// business handler — the LAST step before [Route.Register], consumed by
// [nethttp.Serve]/[chi.Serve] (or [nethttp.ServeOne]) to wire the actual
// mux.Handle(...) call. A route with NO WithHandler call is spec-only —
// Serve skips it entirely (see "Serve's whole-builder failure semantics"
// in docs/design/middleware-workflow-simplification.md).
func (r Route[Req, Resp]) WithHandler(fn func(ctx context.Context, req Req) (Resp, error)) Route[Req, Resp] {
	r.opts = append(slices.Clone(r.opts), handlerOpt{fn: fn})
	return r
}

// WithHandler is [SSERoute]'s equivalent of [Route.WithHandler] — fn
// streams MULTIPLE events per request instead of returning ONE Resp
// (mirrors the existing SSE handler shape: ctx, req, and a send callback).
func (s SSERoute[Req, Event]) WithHandler(fn func(ctx context.Context, req Req, send func(Event) error) error) SSERoute[Req, Event] {
	s.opts = append(slices.Clone(s.opts), handlerOpt{fn: fn})
	return s
}

// optionsOpt is the [RouteOpt] returned by [Route.WithOptions]/
// [SSERoute.WithOptions]. opts is type-erased (any) — api/rest cannot
// import any adapters/* package's Options type without an import cycle
// (adapters already import api/rest); the consuming adapter type-asserts
// it to its own Options type at Serve time, returning an
// OptionsShapeError-style error on mismatch.
type optionsOpt struct{ opts any }

func (o optionsOpt) applyRoute(rb *routeBuilder) { rb.handlerOpts = o.opts }

// WithOptions returns a NEW [Route] with opts attached as this route's
// own adapter Options (e.g. nethttp.Options) — per-route customization
// (a different ErrorHandler for different routes on the same server,
// say). Defaults to the adapter's zero-value Options when never called.
// [nethttp.Serve]/[chi.Serve] take NO Options parameter at all — they use
// whatever each route declared via WithOptions.
func (r Route[Req, Resp]) WithOptions(opts any) Route[Req, Resp] {
	r.opts = append(slices.Clone(r.opts), optionsOpt{opts: opts})
	return r
}

// WithOptions is [SSERoute]'s equivalent of [Route.WithOptions].
func (s SSERoute[Req, Event]) WithOptions(opts any) SSERoute[Req, Event] {
	s.opts = append(slices.Clone(s.opts), optionsOpt{opts: opts})
	return s
}

// handleMWOpt is the [RouteOpt] returned by [Route.HandleMW]/
// [SSERoute.HandleMW]. Builds a [middleware.ServerImplementation]
// internally from mw/fn — mw non-nil with Security set derives Satisfies
// from mw.Security.SchemeName (the PAIRED, security-verifying case,
// matched against a previously-.Use()'d declaration); mw nil (or
// Security nil) leaves Satisfies empty (UNPAIRED, general-purpose — runs
// unconditionally).
type handleMWOpt struct {
	impl middleware.ServerImplementation
}

func (o handleMWOpt) applyRoute(rb *routeBuilder) {
	rb.impls = append(rb.impls, o.impl)
}

func buildServerImplementation(mw *middleware.Middleware, fn any) middleware.ServerImplementation {
	if mw != nil && mw.Security != nil {
		return middleware.ServerImplementation{
			Name:      "implement:" + mw.Security.SchemeName,
			Satisfies: []string{mw.Security.SchemeName},
			Fn:        fn,
		}
	}
	return middleware.ServerImplementation{Name: "implement:general", Fn: fn}
}

// HandleMW is the ONLY server-side implementation-attachment method — mw
// is NILABLE:
//   - non-nil AND mw.Security != nil: PAIRED — fn is matched against a
//     PREVIOUSLY-.Use()'d security declaration, mw being the SAME
//     middleware.Middleware value (not a re-typed string) — matched
//     internally by [Route.RegisterHandle]/[Route.Register]'s
//     reverse-Satisfies check.
//   - nil (or mw.Security == nil): UNPAIRED, general-purpose — fn runs
//     unconditionally, nothing to satisfy (e.g. a raw
//     func(http.Handler) http.Handler closure, or [nethttp.Transform]/
//     [nethttp.Observability]'s output).
//
// fn is deliberately untyped (any) — resolved by the adapter at
// Register/Serve time via a type-switch, mirroring
// [middleware.ServerImplementation.Fn]'s existing type-erasure. A
// wrong-shaped fn fails with a typed error at that point, never
// silently.
func (r Route[Req, Resp]) HandleMW(mw *middleware.Middleware, fn any) Route[Req, Resp] {
	r.opts = append(slices.Clone(r.opts), handleMWOpt{impl: buildServerImplementation(mw, fn)})
	return r
}

// HandleMW is [SSERoute]'s equivalent of [Route.HandleMW] — identical
// nilable-mw semantics.
func (s SSERoute[Req, Event]) HandleMW(mw *middleware.Middleware, fn any) SSERoute[Req, Event] {
	s.opts = append(slices.Clone(s.opts), handleMWOpt{impl: buildServerImplementation(mw, fn)})
	return s
}

// clientMWOpt is the [RouteOpt] returned by [Route.ClientMW]. Builds a
// [middleware.ClientImplementation] internally from mw/fn, mirroring
// [handleMWOpt]'s server-side derivation exactly.
type clientMWOpt struct {
	impl middleware.ClientImplementation
}

func (o clientMWOpt) applyRoute(rb *routeBuilder) {
	rb.clientImpls = append(rb.clientImpls, o.impl)
}

// ClientMW is the ONLY client-side implementation-attachment method — the
// CLIENT-side mirror of [Route.HandleMW]. mw is NILABLE with the SAME
// derivation rule: non-nil with Security set PAIRS fn against a
// previously-.Use()'d declaration (Satisfies gates which implementations
// [nethttp.Call] runs, vs. the route's declared security requirements);
// nil (or Security nil) leaves Satisfies empty — general-purpose, always
// runs.
//
// fn is deliberately untyped (any) for the SAME reason as HandleMW's —
// resolved by the client adapter (e.g. nethttp.Call's credential-
// providing shape) at Call time.
//
// Name includes a per-route attachment-order index (e.g.
// "fulfill:bearerAuth#1") so that TWO ClientMW calls attached for the
// SAME scheme on the SAME route (an unusual but valid pattern — e.g.
// A/B-testing two credential sources) still get DISTINCT Names. This
// matters for [nethttp]'s ConflictingCredentialHeaderError: its "same
// Name means same source, skip conflict check" heuristic would otherwise
// incorrectly treat two same-scheme ClientMW attachments as one source,
// silently picking a value instead of flagging a genuine conflict.
func (r Route[Req, Resp]) ClientMW(mw *middleware.Middleware, fn any) Route[Req, Resp] {
	idx := 0
	for _, o := range r.opts {
		if _, ok := o.(clientMWOpt); ok {
			idx++
		}
	}
	impl := middleware.ClientImplementation{Fn: fn}
	if mw != nil && mw.Security != nil {
		impl.Name = fmt.Sprintf("fulfill:%s#%d", mw.Security.SchemeName, idx)
		impl.Satisfies = []string{mw.Security.SchemeName}
	} else {
		impl.Name = fmt.Sprintf("fulfill:general#%d", idx)
	}
	r.opts = append(slices.Clone(r.opts), clientMWOpt{impl: impl})
	return r
}

// ClientMW is [SSERoute]'s client-side implementation-attachment method —
// identical Satisfies-gating mechanics to [Route.ClientMW]: mw non-nil
// with Security set gates fn to run only when the route's declared
// security requirements include that scheme; mw nil (or Security nil)
// marks fn general-purpose (always runs). Consumed by
// [nethttp.Consume]/[nethttp.CallSSEAdapter] the same way
// [nethttp.Call] consumes [Route.ClientMW]'s attached implementations.
func (s SSERoute[Req, Event]) ClientMW(mw *middleware.Middleware, fn any) SSERoute[Req, Event] {
	idx := 0
	for _, o := range s.opts {
		if _, ok := o.(clientMWOpt); ok {
			idx++
		}
	}
	impl := middleware.ClientImplementation{Fn: fn}
	if mw != nil && mw.Security != nil {
		impl.Name = fmt.Sprintf("fulfill:%s#%d", mw.Security.SchemeName, idx)
		impl.Satisfies = []string{mw.Security.SchemeName}
	} else {
		impl.Name = fmt.Sprintf("fulfill:general#%d", idx)
	}
	s.opts = append(slices.Clone(s.opts), clientMWOpt{impl: impl})
	return s
}

// securityContribution is one source's declaration for a single security
// scheme name, tracked for conflict detection.
type securityContribution struct {
	source     string // "manual" or a middleware's Name
	schemeType route.SecuritySchemeType
	scopes     []string
}

// paramContribution is one source's declaration for a single header/cookie/
// query param name, tracked for conflict detection.
type paramContribution struct {
	source   string
	kind     string // "header", "cookie", "query", "response-header", "response-cookie"
	required bool
}

// applyMiddlewareDeclarations is the SINGLE validation/application pass run
// once by [Route.Register] and [ValidateRoute] — never per-[RouteOpt] — so
// it sees the COMPLETE picture regardless of attachment order. It:
//  1. Detects conflicting security declarations (manual RouteMeta.Security
//     vs. middleware-contributed Security, AND middleware-vs-middleware)
//     for the SAME scheme name.
//  2. Merges every middleware-contributed Security declaration into the
//     route's combined (AND) security requirement and registers its scheme.
//  3. Detects conflicting RequestParams/ResponseParams contributions (manual
//     vs. middleware, and middleware-vs-middleware) for the SAME param name.
//  4. Applies every middleware-contributed RequestParams/ResponseParams
//     entry not already manually declared.
//
// Unlike before Revision 2 (see docs/roadmap/declarative-middleware.md),
// this does NOT check that every declared scheme has an ENFORCING
// implementation — [middleware.Middleware] no longer carries a runtime Fn
// at all, so there is nothing here to check coverage against yet. That
// check is deliberately DEFERRED to adapter Register/Handler time (see
// [CheckCoverage]), the point where the caller has ALSO supplied
// the [middleware.ServerImplementation] values meant to satisfy each
// declared scheme. A route declaring Security via
// [middleware.SecurityScheme] with NO implementation supplied ANYWHERE
// YET is a legitimate, intermediate state at THIS point in the lifecycle.
func applyMiddlewareDeclarations(rb *routeBuilder, routeLabel string) error {
	if err := applySecurityDeclarations(rb, routeLabel); err != nil {
		return err
	}
	return applyParamDeclarations(rb, routeLabel)
}

func applySecurityDeclarations(rb *routeBuilder, routeLabel string) error {
	contributions := map[string][]securityContribution{}

	for _, req := range rb.meta.Security {
		for schemeName, scopes := range req {
			var schemeType route.SecuritySchemeType
			if s, ok := rb.securitySchemes[schemeName]; ok {
				schemeType = s.Type
			}
			contributions[schemeName] = append(contributions[schemeName], securityContribution{
				source: "manual", schemeType: schemeType, scopes: scopes,
			})
		}
	}
	for _, mw := range rb.middlewares {
		if mw.Security == nil {
			continue
		}
		contributions[mw.Security.SchemeName] = append(contributions[mw.Security.SchemeName], securityContribution{
			source: mw.Name, schemeType: mw.Security.Scheme.Type, scopes: mw.Security.Scopes,
		})
	}

	for schemeName, list := range contributions {
		first := list[0]
		for _, c := range list[1:] {
			// A source with NO declared scheme type (e.g. a manual
			// RouteMeta.Security entry with no matching [SecurityScheme]
			// declared) is treated as "unspecified, compatible with anything" —
			// only compared on scopes. Two sources both declaring a type
			// are compared exactly. Source NAME equality is NOT used to
			// skip comparison: middleware.SecurityScheme-built values for
			// the SAME scheme name share the SAME auto-generated Name by
			// construction, so name-equality is not a reliable
			// same-attachment signal — only the declared content is.
			typesDiffer := first.schemeType != "" && c.schemeType != "" && first.schemeType != c.schemeType
			if typesDiffer || !sameScopeSet(c.scopes, first.scopes) {
				return ConflictingSecurityDeclarationError{
					Route: routeLabel, Scheme: schemeName,
					FirstSource: first.source, SecondSource: c.source,
					FirstScopes: first.scopes, SecondScopes: c.scopes,
				}
			}
		}
	}

	for _, mw := range rb.middlewares {
		if mw.Security == nil {
			continue
		}
		if len(rb.meta.Security) == 0 {
			rb.meta.Security = []route.SecurityRequirement{{}}
		}
		rb.meta.Security[0][mw.Security.SchemeName] = mw.Security.Scopes
		if rb.securitySchemes == nil {
			rb.securitySchemes = make(map[string]SecurityScheme, 1)
		}
		if _, exists := rb.securitySchemes[mw.Security.SchemeName]; !exists {
			rb.securitySchemes[mw.Security.SchemeName] = SecurityScheme{
				SecurityScheme: mw.Security.Scheme,
				Codec:          mw.Security.Codec,
			}
		}
	}
	return nil
}

// applyMiddlewareSecurityForClient merges middleware-declared Security into
// rb.securitySchemes/rb.meta.Security WITHOUT conflict detection or coverage
// checking — used by [Route.ClientHandle], which stays infallible. Full
// validation (conflict detection, drift-closing coverage) only happens via
// [Route.Register] or [ValidateRoute].
func applyMiddlewareSecurityForClient(rb *routeBuilder) {
	for _, mw := range rb.middlewares {
		if mw.Security == nil {
			continue
		}
		if len(rb.meta.Security) == 0 {
			rb.meta.Security = []route.SecurityRequirement{{}}
		}
		rb.meta.Security[0][mw.Security.SchemeName] = mw.Security.Scopes
		if rb.securitySchemes == nil {
			rb.securitySchemes = make(map[string]SecurityScheme, 1)
		}
		if _, exists := rb.securitySchemes[mw.Security.SchemeName]; !exists {
			rb.securitySchemes[mw.Security.SchemeName] = SecurityScheme{
				SecurityScheme: mw.Security.Scheme,
				Codec:          mw.Security.Codec,
			}
		}
	}
}

func sameScopeSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	as := slices.Clone(a)
	bs := slices.Clone(b)
	slices.Sort(as)
	slices.Sort(bs)
	return slices.Equal(as, bs)
}

// CheckCoverage verifies that every security scheme named
// anywhere in secReqs has at least one [middleware.ServerImplementation] in
// impls whose Satisfies names it — otherwise the route would enforce
// nothing at runtime despite declaring a scheme in its spec. Returns
// [MissingSecurityMiddlewareError] on the first uncovered scheme found.
//
// This is the RELOCATED coverage check (see docs/roadmap/declarative-middleware.md's
// "Revision 2 — the declare/implement split") — it used to run automatically
// inside [Route.Register], back when [middleware.Middleware] still carried
// both the Security declaration AND the enforcing Fn together. Now that the
// two are separate types (Middleware declares; ServerImplementation
// implements), this check can only run once BOTH are known — which is
// adapter Serve time, not builder time. Server adapters (nethttp.Serve/
// ServeSSE and their chi mirrors) call this explicitly from their reflect
// dispatch (buildRouteHandler/buildSSERouteHandler), passing
// handle.Descriptor.Security (falling back to handle.GlobalSecurity, same
// resolution rule used everywhere else) and the route's attached
// []middleware.ServerImplementation values.
func CheckCoverage(routeLabel string, secReqs []route.SecurityRequirement, impls []middleware.ServerImplementation) error {
	for _, req := range secReqs {
		for schemeName := range req {
			satisfied := false
			for _, impl := range impls {
				if slices.Contains(impl.Satisfies, schemeName) {
					satisfied = true
					break
				}
			}
			if !satisfied {
				return MissingSecurityMiddlewareError{Route: routeLabel, Scheme: schemeName}
			}
		}
	}
	return nil
}

// checkImplementationsDeclared is the REVERSE-direction sibling to
// [CheckCoverage]: instead of "every DECLARED scheme has a covering
// implementation," it verifies "every IMPLEMENTED (non-empty-Satisfies)
// scheme was actually declared" — catching a [Route.HandleMW] call
// PAIRED against a security scheme name that was never [Route.Use]'d on
// the SAME route (e.g. a copy-paste mistake reusing a different route's
// [middleware.Middleware]). Called by [Route.Register]/[Route.RegisterHandle]
// (and their [SSERoute] equivalents) — unlike CheckCoverage, this runs
// UNCONDITIONALLY, regardless of whether the route has a handler attached,
// since a mismatched pairing is a route-internal-consistency bug, not an
// EH2-style "declared but intentionally unimplemented" case.
func checkImplementationsDeclared(routeLabel string, mws []middleware.Middleware, impls []middleware.ServerImplementation) error {
	declared := make(map[string]bool, len(mws))
	for _, mw := range mws {
		if mw.Security != nil {
			declared[mw.Security.SchemeName] = true
		}
	}
	for _, impl := range impls {
		for _, scheme := range impl.Satisfies {
			if !declared[scheme] {
				return UnknownMiddlewareImplementationError{Route: routeLabel, Scheme: scheme}
			}
		}
	}
	return nil
}

// toHeaderParam/toCookieParam/toQueryParam/toResponseHeaderParam/
// toResponseCookieParam convert a middleware-package-local typed spec
// (see [middleware.HeaderParamSpec]'s doc comment) into the matching
// api/rest param type — a straightforward, infallible field copy. There is
// no shape-mismatch case anymore: the Go compiler enforces the correct
// type at the [middleware.Middleware] field declaration itself, replacing
// the former runtime type-switch + [ParamContributionShapeError].
func toHeaderParam(s middleware.HeaderParamSpec) HeaderParam {
	return HeaderParam{Name: s.Name, Description: s.Description, Required: s.Required, Codec: s.Codec}
}
func toCookieParam(s middleware.CookieParamSpec) CookieParam {
	return CookieParam{Name: s.Name, Description: s.Description, Required: s.Required, Codec: s.Codec}
}
func toQueryParam(s middleware.QueryParamSpec) QueryParam {
	return QueryParam{Name: s.Name, Description: s.Description, Required: s.Required, Codec: s.Codec}
}
func toResponseHeaderParam(s middleware.ResponseHeaderParamSpec) ResponseHeaderParam {
	return ResponseHeaderParam{Name: s.Name, Description: s.Description, Required: s.Required, Codec: s.Codec}
}
func toResponseCookieParam(s middleware.ResponseCookieParamSpec) ResponseCookieParam {
	return ResponseCookieParam{Name: s.Name, Description: s.Description, Required: s.Required, Codec: s.Codec}
}

func applyParamDeclarations(rb *routeBuilder, routeLabel string) error {
	request := map[string][]paramContribution{}
	response := map[string][]paramContribution{}

	for _, p := range rb.headerParams {
		request[p.Name] = append(request[p.Name], paramContribution{source: "manual", kind: "header", required: p.Required})
	}
	for _, p := range rb.cookieParams {
		request[p.Name] = append(request[p.Name], paramContribution{source: "manual", kind: "cookie", required: p.Required})
	}
	for _, p := range rb.queryParams {
		request[p.Name] = append(request[p.Name], paramContribution{source: "manual", kind: "query", required: p.Required})
	}
	for _, p := range rb.respHeaders {
		response[p.Name] = append(response[p.Name], paramContribution{source: "manual", kind: "response-header"})
	}
	for _, p := range rb.respCookies {
		response[p.Name] = append(response[p.Name], paramContribution{source: "manual", kind: "response-cookie"})
	}

	for _, mw := range rb.middlewares {
		for _, s := range mw.RequestHeaderParams {
			request[s.Name] = append(request[s.Name], paramContribution{source: mw.Name, kind: "header", required: s.Required})
		}
		for _, s := range mw.RequestCookieParams {
			request[s.Name] = append(request[s.Name], paramContribution{source: mw.Name, kind: "cookie", required: s.Required})
		}
		for _, s := range mw.RequestQueryParams {
			request[s.Name] = append(request[s.Name], paramContribution{source: mw.Name, kind: "query", required: s.Required})
		}
		for _, s := range mw.ResponseHeaderParams {
			response[s.Name] = append(response[s.Name], paramContribution{source: mw.Name, kind: "response-header"})
		}
		for _, s := range mw.ResponseCookieParams {
			response[s.Name] = append(response[s.Name], paramContribution{source: mw.Name, kind: "response-cookie"})
		}
	}

	if err := checkParamConflicts(routeLabel, request); err != nil {
		return err
	}
	if err := checkParamConflicts(routeLabel, response); err != nil {
		return err
	}

	manualRequestNames := paramNameSet(rb.headerParams, rb.cookieParams, rb.queryParams)
	manualResponseNames := paramNameSetResponse(rb.respHeaders, rb.respCookies)

	for _, mw := range rb.middlewares {
		for _, s := range mw.RequestHeaderParams {
			if !manualRequestNames[s.Name] {
				toHeaderParam(s).applyRoute(rb)
			}
		}
		for _, s := range mw.RequestCookieParams {
			if !manualRequestNames[s.Name] {
				toCookieParam(s).applyRoute(rb)
			}
		}
		for _, s := range mw.RequestQueryParams {
			if !manualRequestNames[s.Name] {
				toQueryParam(s).applyRoute(rb)
			}
		}
		for _, s := range mw.ResponseHeaderParams {
			if !manualResponseNames[s.Name] {
				toResponseHeaderParam(s).applyRoute(rb)
			}
		}
		for _, s := range mw.ResponseCookieParams {
			if !manualResponseNames[s.Name] {
				toResponseCookieParam(s).applyRoute(rb)
			}
		}
	}
	return nil
}

func checkParamConflicts(routeLabel string, contributions map[string][]paramContribution) error {
	for name, list := range contributions {
		first := list[0]
		for _, c := range list[1:] {
			if c.kind != first.kind || c.required != first.required {
				return ConflictingParamContributionError{
					Route: routeLabel, ParamName: name,
					FirstSource: first.source, SecondSource: c.source,
				}
			}
		}
	}
	return nil
}

func paramNameSet(headers []HeaderParam, cookies []CookieParam, queries []QueryParam) map[string]bool {
	names := make(map[string]bool, len(headers)+len(cookies)+len(queries))
	for _, p := range headers {
		names[p.Name] = true
	}
	for _, p := range cookies {
		names[p.Name] = true
	}
	for _, p := range queries {
		names[p.Name] = true
	}
	return names
}

func paramNameSetResponse(headers []ResponseHeaderParam, cookies []ResponseCookieParam) map[string]bool {
	names := make(map[string]bool, len(headers)+len(cookies))
	for _, p := range headers {
		names[p.Name] = true
	}
	for _, p := range cookies {
		names[p.Name] = true
	}
	return names
}

// MissingSecurityMiddlewareError is returned by [Route.Register] when a
// route declares a security scheme (via manual [RouteMeta.Security] or a
// middleware's Security field) with NO attached middleware whose Satisfies
// names that scheme — the route would enforce nothing at runtime despite
// declaring a scheme in its spec.
type MissingSecurityMiddlewareError struct {
	Route  string
	Scheme string
}

func (e MissingSecurityMiddlewareError) Error() string {
	return fmt.Sprintf("api/rest: route %q declares security scheme %q with no attached middleware satisfying it", e.Route, e.Scheme)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e MissingSecurityMiddlewareError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("route", e.Route),
		slog.String("scheme", e.Scheme),
	)
}

// UnknownMiddlewareImplementationError is returned by [Route.Register]/
// [Route.RegisterHandle] (and their [SSERoute] equivalents) when a
// [Route.HandleMW] call is PAIRED (non-nil mw with non-nil Security)
// against a security scheme name that was never [Route.Use]'d on the SAME
// route — the reverse-direction sibling to [MissingSecurityMiddlewareError]/
// [CheckCoverage].
type UnknownMiddlewareImplementationError struct {
	Route  string
	Scheme string
}

func (e UnknownMiddlewareImplementationError) Error() string {
	return fmt.Sprintf("api/rest: route %q attaches an implementation satisfying security scheme %q, which was never declared via .Use() on this route", e.Route, e.Scheme)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e UnknownMiddlewareImplementationError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("route", e.Route),
		slog.String("scheme", e.Scheme),
	)
}

// ConflictingSecurityDeclarationError is returned by [Route.Register] when
// two DIFFERENT sources (a manual declaration or a specific middleware's
// Name) declare the SAME security scheme with a DIFFERENT scheme type or
// scopes. Identical redundant declarations for the same scheme are allowed
// silently — only DIFFERING ones conflict.
type ConflictingSecurityDeclarationError struct {
	Route                     string
	Scheme                    string
	FirstSource, SecondSource string
	FirstScopes, SecondScopes []string
}

func (e ConflictingSecurityDeclarationError) Error() string {
	return fmt.Sprintf("api/rest: route %q: conflicting security declaration for scheme %q: %q declares scopes %v, but %q declares scopes %v",
		e.Route, e.Scheme, e.FirstSource, e.FirstScopes, e.SecondSource, e.SecondScopes)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e ConflictingSecurityDeclarationError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("route", e.Route),
		slog.String("scheme", e.Scheme),
		slog.String("first_source", e.FirstSource),
		slog.Any("first_scopes", e.FirstScopes),
		slog.String("second_source", e.SecondSource),
		slog.Any("second_scopes", e.SecondScopes),
	)
}

// ConflictingParamContributionError is returned by [Route.Register] when
// two DIFFERENT sources (a manual declaration or a specific middleware's
// Name) declare the SAME header/cookie/query param name with a DIFFERENT
// concrete param kind or Required value. Identical redundant declarations
// are allowed silently.
type ConflictingParamContributionError struct {
	Route                     string
	ParamName                 string
	FirstSource, SecondSource string
}

func (e ConflictingParamContributionError) Error() string {
	return fmt.Sprintf("api/rest: route %q: conflicting param contribution for %q: %q vs %q",
		e.Route, e.ParamName, e.FirstSource, e.SecondSource)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e ConflictingParamContributionError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("route", e.Route),
		slog.String("param_name", e.ParamName),
		slog.String("first_source", e.FirstSource),
		slog.String("second_source", e.SecondSource),
	)
}

// ValidateRoute runs the IDENTICAL validation [Route.Register] would run —
// the FULL opts list (manual RouteOpts AND [WithMiddleware]-attached
// middleware together), via a scratch [routeBuilder] that is discarded
// afterward — without needing a live [Builder] or registering anything.
// Use this in a domain package that declares routes/middleware but doesn't
// own the [Builder] used to wire them (typically main.go).
//
// meta and opts are the SAME arguments [NewRoute] would receive (minus
// method/path/codecs, which don't affect middleware/security validation).
//
// Does NOT catch a missing security implementation — since Revision 2 (see
// docs/roadmap/declarative-middleware.md), that check ([CheckCoverage])
// only runs once a [middleware.ServerImplementation] has actually been
// supplied, which happens at adapter Register/Handler time, not here.
func ValidateRoute[Req, Resp any](meta RouteMeta, opts ...RouteOpt) error {
	var rb routeBuilder
	meta.applyRoute(&rb)
	for _, opt := range opts {
		opt.applyRoute(&rb)
	}
	return applyMiddlewareDeclarations(&rb, "")
}
