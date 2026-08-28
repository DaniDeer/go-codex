package rest

import (
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
// route had called [WithSecurityScheme] and set [RouteMeta.Security]
// manually); a middleware carrying RequestParams/ResponseParams declares
// additional header/cookie/query param spec entries. A route needs ZERO
// manual WithSecurityScheme/RouteMeta.Security/param calls when the
// attached middleware already carries a complete declaration.
//
// Attaching a general-purpose (non-declarative) middleware — one with a nil
// Security and empty RequestParams/ResponseParams — is also valid here; it
// simply has no spec effect.
func WithMiddleware(mws ...middleware.Middleware) RouteOpt {
	return middlewareOpt{mws: mws}
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

// clientMiddlewareOpt is the [RouteOpt] returned by [WithClientMiddleware].
// It only accumulates mws into rb.clientMiddlewares — unlike
// [middlewareOpt], there is NO further application step: a
// [middleware.ClientMiddleware] carries no Security/RequestParams/
// ResponseParams to merge into the spec at all.
type clientMiddlewareOpt struct{ mws []middleware.ClientMiddleware }

func (o clientMiddlewareOpt) applyRoute(rb *routeBuilder) {
	rb.clientMiddlewares = append(rb.clientMiddlewares, o.mws...)
}

// WithClientMiddleware attaches one or more [middleware.ClientMiddleware]
// values to a route at declaration time — the CLIENT-side counterpart to
// [WithMiddleware]. A ClientMiddleware NEVER affects the route's spec
// (Security/RequestParams/ResponseParams are declared server-side only,
// via WithMiddleware/[RequireScopes]/[middleware.DeclareSecurity]); it
// only carries the runtime Fn a client-building call ([Route.ClientHandle]
// + [nethttp.Call]/[CallHandle]) uses to fulfill whatever the route
// already declares.
//
// Prefer [Route.UseClient] for the common case of attaching after
// [NewRoute] — this exists so a ClientMiddleware can also be passed
// inline as one of NewRoute's variadic opts, mirroring WithMiddleware's
// own flexibility.
func WithClientMiddleware(mws ...middleware.ClientMiddleware) RouteOpt {
	return clientMiddlewareOpt{mws: mws}
}

// UseClient returns a NEW [Route] with mws chained onto it as CLIENT-side
// declarations — the counterpart to [Route.Use]:
//
//	handle := rest.NewRoute[GetTagsReq, TagsList]("GET", "/v2/{name}/tags/list",
//	    reqCodec, respCodec, rest.RouteMeta{OperationID: "getTags"},
//	).Use(middleware.DeclareSecurity("bearerAuth", scheme, nil, codec)). // server declares
//	    UseClient(authMw). // client fulfills
//	    ClientHandle()
//
// A [middleware.ClientMiddleware] attached this way is combined
// automatically with [nethttp.Call]/[CallHandle]'s own call-time variadic
// (see [RouteHandle.ClientMiddlewares]) — no need to ALSO pass it there,
// though doing so remains a valid, explicit per-call override (e.g. to
// deliberately test a DIFFERENT credential for one specific call without
// changing what the route declares generally).
//
// Chainable and immutable exactly like [Route.Use] — `.UseClient(mw1).UseClient(mw2)`
// and `.UseClient(mw1, mw2)` are equivalent, in attachment order; Use never
// mutates the receiver.
func (r Route[Req, Resp]) UseClient(mws ...middleware.ClientMiddleware) Route[Req, Resp] {
	r.opts = append(slices.Clone(r.opts), WithClientMiddleware(mws...))
	return r
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
//  1. Detects conflicting security declarations (manual RouteMeta.Security/
//     WithSecurityScheme vs. middleware-contributed Security, AND
//     middleware-vs-middleware) for the SAME scheme name.
//  2. Merges every middleware-contributed Security declaration into the
//     route's combined (AND) security requirement and registers its scheme.
//  3. Detects conflicting RequestParams/ResponseParams contributions (manual
//     vs. middleware, and middleware-vs-middleware) for the SAME param name.
//  4. Applies every middleware-contributed RequestParams/ResponseParams
//     entry not already manually declared.
//  5. Runs drift-closing validation: every scheme referenced anywhere in the
//     route's security requirements must have at least one attached
//     middleware whose Satisfies names it — otherwise the route would
//     enforce nothing at runtime despite declaring a scheme in its spec.
func applyMiddlewareDeclarations(rb *routeBuilder, routeLabel string) error {
	if err := applySecurityDeclarations(rb, routeLabel); err != nil {
		return err
	}
	if err := applyParamDeclarations(rb, routeLabel); err != nil {
		return err
	}
	return checkSecurityMiddlewareCoverage(rb, routeLabel)
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
			// RouteMeta.Security entry with no matching WithSecurityScheme
			// call) is treated as "unspecified, compatible with anything" —
			// only compared on scopes. Two sources both declaring a type
			// are compared exactly. Source NAME equality is NOT used to
			// skip comparison: middleware.RequireScopes-built values for
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

func checkSecurityMiddlewareCoverage(rb *routeBuilder, routeLabel string) error {
	for _, req := range rb.meta.Security {
		for schemeName := range req {
			satisfied := false
			for _, mw := range rb.middlewares {
				if slices.Contains(mw.Satisfies, schemeName) {
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

// requestParamInfo extracts (name, kind, required) from a RequestParams
// entry. Only [HeaderParam], [CookieParam], and [QueryParam] are supported
// in Phase 1 — any other concrete type is a [ParamContributionShapeError].
func requestParamInfo(entry any) (name, kind string, required bool, err error) {
	switch p := entry.(type) {
	case HeaderParam:
		return p.Name, "header", p.Required, nil
	case CookieParam:
		return p.Name, "cookie", p.Required, nil
	case QueryParam:
		return p.Name, "query", p.Required, nil
	default:
		return "", "", false, fmt.Errorf("unsupported RequestParams entry type %T", entry)
	}
}

// responseParamInfo extracts (name, kind) from a ResponseParams entry. Only
// [ResponseHeaderParam] and [ResponseCookieParam] are supported in Phase 1.
func responseParamInfo(entry any) (name, kind string, err error) {
	switch p := entry.(type) {
	case ResponseHeaderParam:
		return p.Name, "response-header", nil
	case ResponseCookieParam:
		return p.Name, "response-cookie", nil
	default:
		return "", "", fmt.Errorf("unsupported ResponseParams entry type %T", entry)
	}
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
		for _, entry := range mw.RequestParams {
			name, kind, required, err := requestParamInfo(entry)
			if err != nil {
				return ParamContributionShapeError{Name: mw.Name, Expected: "HeaderParam/CookieParam/QueryParam", Got: fmt.Sprintf("%T", entry)}
			}
			request[name] = append(request[name], paramContribution{source: mw.Name, kind: kind, required: required})
		}
		for _, entry := range mw.ResponseParams {
			name, kind, err := responseParamInfo(entry)
			if err != nil {
				return ParamContributionShapeError{Name: mw.Name, Expected: "ResponseHeaderParam/ResponseCookieParam", Got: fmt.Sprintf("%T", entry)}
			}
			response[name] = append(response[name], paramContribution{source: mw.Name, kind: kind})
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
		for _, entry := range mw.RequestParams {
			name, _, _, _ := requestParamInfo(entry)
			if manualRequestNames[name] {
				continue // already manually declared — don't duplicate
			}
			if opt, ok := entry.(RouteOpt); ok {
				opt.applyRoute(rb)
			}
		}
		for _, entry := range mw.ResponseParams {
			name, _, _ := responseParamInfo(entry)
			if manualResponseNames[name] {
				continue
			}
			if opt, ok := entry.(RouteOpt); ok {
				opt.applyRoute(rb)
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
// route declares a security scheme (via manual [RouteMeta.Security]/
// [WithSecurityScheme] or a middleware's Security field) with NO attached
// middleware whose Satisfies names that scheme — the route would enforce
// nothing at runtime despite declaring a scheme in its spec.
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

// ParamContributionShapeError is returned when a [middleware.Middleware]'s
// RequestParams/ResponseParams entry is not a recognized param type
// ([HeaderParam]/[CookieParam]/[QueryParam]/[ResponseHeaderParam]/
// [ResponseCookieParam]).
type ParamContributionShapeError struct {
	Name     string
	Expected string
	Got      string
}

func (e ParamContributionShapeError) Error() string {
	return fmt.Sprintf("api/rest: middleware %q: param contribution: expected %s, got %s", e.Name, e.Expected, e.Got)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e ParamContributionShapeError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("name", e.Name),
		slog.String("expected", e.Expected),
		slog.String("got", e.Got),
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
func ValidateRoute[Req, Resp any](meta RouteMeta, opts ...RouteOpt) error {
	var rb routeBuilder
	meta.applyRoute(&rb)
	for _, opt := range opts {
		opt.applyRoute(&rb)
	}
	return applyMiddlewareDeclarations(&rb, "")
}
