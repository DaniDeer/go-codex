package reqreply

import (
	"fmt"
	"log/slog"
	"slices"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/schema"
)

// routeMiddlewareOpt is the [RouteOpt] returned by [Route.Use]. Only
// accumulates mws into rb.middlewares — the actual Security merge (into
// the route's security requirements, plus scheme registration) happens
// ONCE, at Register/ClientHandle time, via [applySecurityDeclarations] —
// order-independent regardless of where Use appears among a route's other
// RouteOpts. Mirrors [rest.routeMiddlewareOpt]'s core mechanism, scoped
// down to plain [middleware.Middleware] only — reqreply does not (yet)
// support D-0003's codec-declared [middleware.Middleware[In,Out]]
// bundling; every [middleware.RouteMiddleware] value attached here is
// expected to be a plain [middleware.Middleware].
type routeMiddlewareOpt struct{ mws []middleware.RouteMiddleware }

func (o routeMiddlewareOpt) applyRoute(rb *routeBuilder) {
	for _, mw := range o.mws {
		switch v := mw.(type) {
		case middleware.Middleware:
			rb.middlewares = append(rb.middlewares, v)
		case routeMiddlewareContributor:
			// A codec-backed Middleware[In,Out] (this doc's SECOND
			// mechanism) attached via plain .Use() — mirrors
			// rest.routeMiddlewareOpt's identical dispatch. Its spec
			// contribution is ALWAYS layered; its runtime dispatch
			// handler is registered ONLY when bundled via
			// WithReceive/WithSend (see Middleware.applyAgnosticRoute).
			v.applyAgnosticRoute(rb)
		}
	}
}

// routeMiddlewareContributor is implemented by [Middleware][In, Out]
// (transform.go/middleware_declaration.go) — the unexported interface
// [routeMiddlewareOpt.applyRoute] uses to recognize a codec-backed
// Middleware value attached via plain .Use(), mirroring
// [rest.routeMiddlewareContributor] exactly.
type routeMiddlewareContributor interface {
	applyAgnosticRoute(rb *routeBuilder)
}

// Use returns a NEW [Route] with mws chained onto it — declaration-time
// sugar mirroring [rest.Route.Use]/[events.Subscriber.Use] exactly
// (IDENTICAL signature). A middleware carrying a non-nil Security
// declares a security scheme + requirement for THIS route (fed into the
// AsyncAPI spec exactly as if the route had set [RouteMeta.Security] and
// called [WithSecurityScheme] manually) — [WithSecurityScheme] itself is
// REPLACED by this mechanism as the declare-time path going forward (kept
// as a deprecated-but-functional alias for existing callers, unchanged).
//
// Chainable — `.Use(mw1).Use(mw2)` and `.Use(mw1, mw2)` are equivalent, in
// attachment order. [Route] is an immutable value; Use never mutates the
// receiver.
func (r Route[Req, Resp]) Use(mws ...middleware.RouteMiddleware) Route[Req, Resp] {
	r.opts = append(slices.Clone(r.opts), routeMiddlewareOpt{mws: mws})
	return r
}

// handleMWOpt is the [RouteOpt] returned by [Route.HandleMW]. Builds a
// [middleware.ServerImplementation] internally from mw/fn — mirrors
// [rest.handleMWOpt] exactly.
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

// HandleMW is the ONLY server-side implementation-attachment method — the
// IDENTICAL signature and nilable-mw semantics as [rest.Route.HandleMW]:
//   - non-nil AND mw.Security != nil: PAIRED — fn is matched against a
//     PREVIOUSLY-.Use()'d security declaration (via mw's Satisfies-
//     name), matched by [checkImplementationsDeclared] at Register time
//     and consulted by the attached [ServerTransport] at Serve time.
//   - nil (or mw.Security == nil): UNPAIRED, general-purpose — fn runs
//     unconditionally.
//
// fn is deliberately untyped (any) — resolved by the attached adapter
// (e.g. mqtt5's `AttachServer`) via a type-switch/reflection, mirroring
// [middleware.ServerImplementation.Fn]'s existing type-erasure. A
// wrong-shaped fn fails with a typed error at Serve time, never silently.
func (r Route[Req, Resp]) HandleMW(mw *middleware.Middleware, fn any) Route[Req, Resp] {
	r.opts = append(slices.Clone(r.opts), handleMWOpt{impl: buildServerImplementation(mw, fn)})
	return r
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
// CLIENT-side mirror of [Route.HandleMW], IDENTICAL signature to
// [rest.Route.ClientMW]. mw is NILABLE with the SAME derivation rule:
// non-nil with Security set PAIRS fn against a previously-.Use()'d
// declaration (Satisfies gates which implementations the attached
// [ClientTransport] runs, vs. the route's declared security
// requirements); nil (or Security nil) leaves Satisfies empty —
// general-purpose, always runs.
//
// Name includes a per-route attachment-order index (e.g.
// "fulfill:bearerAuth#1") so that TWO ClientMW calls attached for the
// SAME scheme on the SAME route still get DISTINCT Names — mirrors
// [rest.Route.ClientMW]'s identical rationale.
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

// applySecurityDeclarations merges every middleware-contributed Security
// declaration (via [Route.Use]) into rb's aggregate security requirement
// and scheme map — mirrors [rest.applySecurityDeclarations], SIMPLIFIED:
// reqreply routes are declared one Req/Resp pair per topic (no
// path/query/header/cookie param declarations to also merge the way REST
// does), so this covers ONLY the security half. Conflict detection
// against manual [WithSecurityScheme]/[RouteMeta.Security] declarations
// for the SAME scheme name is intentionally NOT included in Phase 1 (no
// confirmed real-world case requiring it yet for reqreply — can be added
// later without breaking callers, mirrors this doc's own "don't invent
// unrequested API" discipline).
func applySecurityDeclarations(rb *routeBuilder) {
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

// applyParamDeclarations is Phase 1b's header-param-as-middleware
// counterpart to [applySecurityDeclarations] — collects every
// [middleware.Middleware.RequestHeaderParams]/[middleware.Middleware.ResponseHeaderParams]
// contributed via [Route.Use] (e.g. via
// [mqtt5.FromUserPropertyParam]/[mqtt5.FromResponseUserPropertyParam])
// and renders them into two AsyncAPI "headers" schemas — one for the
// request message, one for the reply message. Mirrors [rest.
// applyParamDeclarations], SCOPED DOWN: reqreply's [RouteMeta] has no
// manual header-param declaration fields to merge/conflict-check against
// (unlike REST's rb.headerParams/rb.respHeaders), so this only needs to
// dedup by name across attached middlewares — two middlewares
// contributing the SAME name are folded into one property, first-seen
// wins (mirrors the manual-vs-middleware name-collision policy used
// elsewhere in this file: last write to the map is irrelevant since
// property/required values are identical for a well-formed declaration).
// Returns two zero [schema.Schema] values when no header params were
// declared — [render/asyncapi/v3.Message.Headers] treats a zero Schema as
// "omit the headers field entirely" (see its own [schema.Schema.IsZero]
// check in buildMessage). Also returns the deduped raw param specs
// themselves (reqParams/respParams) — populated onto
// [RouteHandle.RequestHeaderParams]/[RouteHandle.ResponseHeaderParams]
// for the attached adapter (e.g. mqtt5's `AttachServer`/`AttachClient`)
// to validate at dispatch time, mirroring how [RouteHandle.
// Implementations]/[RouteHandle.ClientImplementations] carry the
// SECURITY-side middleware for the SAME "spec AND runtime both consult
// what .Use() declared" reason.
func applyParamDeclarations(rb *routeBuilder) (reqParams []middleware.HeaderParamSpec, respParams []middleware.ResponseHeaderParamSpec, reqHeaders, respHeaders schema.Schema) {
	seenReq := make(map[string]bool)
	var reqProps []schema.Property
	var reqRequired []string
	seenResp := make(map[string]bool)
	var respProps []schema.Property
	var respRequired []string

	for _, mw := range rb.middlewares {
		for _, p := range mw.RequestHeaderParams {
			if seenReq[p.Name] {
				continue
			}
			seenReq[p.Name] = true
			reqParams = append(reqParams, p)
			reqProps = append(reqProps, headerParamProperty(p.Name, p.Description, p.Codec))
			if p.Required {
				reqRequired = append(reqRequired, p.Name)
			}
		}
		for _, p := range mw.ResponseHeaderParams {
			if seenResp[p.Name] {
				continue
			}
			seenResp[p.Name] = true
			respParams = append(respParams, p)
			respProps = append(respProps, headerParamProperty(p.Name, p.Description, p.Codec))
			if p.Required {
				respRequired = append(respRequired, p.Name)
			}
		}
	}

	// Unify the codec-backed Middleware[In,Out] axis's property
	// contributions (WithRequestProperty/WithResponseProperty, attached
	// via Transform/ClientTransform or plain .Use()) into the SAME
	// reqHeaders/respHeaders schema Phase 1b's flat mechanism already
	// builds above — Round 16: a property's Required propagates into
	// reqRequired/respRequired exactly like an existing header-as-
	// middleware declaration's Required already does.
	//
	// NOTE: unlike Phase 1b's flat mechanism above, these contributions
	// are layered ONLY into the SCHEMA (reqProps/respProps/reqRequired/
	// respRequired) — deliberately NOT appended to reqParams/respParams
	// (RouteHandle.RequestHeaderParams/ResponseHeaderParams), which feed
	// Phase 1b's OWN separate runtime validation
	// (validateUserProperties/UserPropertyParam, adapter-side). The NEW
	// property axis has its OWN separate runtime validation path
	// (Middleware.DecodeIn, dispatched via RouteHandle.
	// MiddlewareHandlers) — also feeding reqParams/respParams here would
	// cause DOUBLE, differently-shaped validation and let Phase 1b's
	// mechanism preempt the new axis's own dispatch/error semantics.
	for _, c := range rb.middlewareSpecContributions {
		for _, p := range c.propertyParamsIn {
			if seenReq[p.Name] {
				continue
			}
			seenReq[p.Name] = true
			reqProps = append(reqProps, headerParamProperty(p.Name, p.Description, p.Codec))
			if p.Required {
				reqRequired = append(reqRequired, p.Name)
			}
		}
		for _, p := range c.propertyParamsOut {
			if seenResp[p.Name] {
				continue
			}
			seenResp[p.Name] = true
			respProps = append(respProps, headerParamProperty(p.Name, p.Description, p.Codec))
			if p.Required {
				respRequired = append(respRequired, p.Name)
			}
		}
	}
	// The route's own manually-declared PropertyParam entries (rb.
	// propertyParams — a bare PropertyParam/MergedPropertyParam[Req]
	// passed directly as a RouteOpt, mirroring TopicParam's own
	// validate-only escape hatch) also render into the schema ONLY, for
	// the same reason as above.
	for _, p := range rb.propertyParams {
		if !seenReq[p.Name] {
			seenReq[p.Name] = true
			reqProps = append(reqProps, headerParamProperty(p.Name, p.Description, p.Codec))
			if p.Required {
				reqRequired = append(reqRequired, p.Name)
			}
		}
	}

	if len(reqProps) > 0 {
		reqHeaders = schema.Schema{Type: "object", Properties: reqProps, Required: reqRequired}
	}
	if len(respProps) > 0 {
		respHeaders = schema.Schema{Type: "object", Properties: respProps, Required: respRequired}
	}
	return reqParams, respParams, reqHeaders, respHeaders
}

// headerParamProperty renders one header/User-Property param spec into an
// AsyncAPI Property entry — mirrors how [codex.Struct]/[codex.Object]
// build their own object-schema properties (Name + Schema, Description
// folded into the property's own Schema).
func headerParamProperty(name, description string, codec *codex.Codec[string]) schema.Property {
	propSchema := schema.Schema{Description: description}
	if codec != nil {
		propSchema = codec.Schema
		if description != "" {
			propSchema.Description = description
		}
	}
	return schema.Property{Name: name, Schema: propSchema}
}

// checkMiddlewareNameUniquenessAndAttachment enforces D6(b) and D7 from
// docs/design/d-0003-codec-declared-middlewares.md, mirroring
// [rest.checkMiddlewareNameUniquenessAndAttachment] exactly — reqreply's
// own codec-backed [Middleware][In,Out] axis:
//
//   - D6(b): every attached Middleware's Declaration.Name must be unique
//     per route (across ALL Transform/ClientTransform/.Use()
//     attachments) — returns [DuplicateMiddlewareNameError] on the first
//     repeat encountered.
//   - D7: a Middleware value that is ALSO bundled (carries a
//     WithReceive/WithSend Fn, the route-AGNOSTIC .Use() attachment
//     shape) must NOT ALSO be attached via Transform/ClientTransform on
//     the SAME route — returns [AmbiguousMiddlewareAttachmentError] when
//     both are detected for one mw value.
func checkMiddlewareNameUniquenessAndAttachment(rb *routeBuilder, routeLabel string) error {
	seen := make(map[string]bool, len(rb.middlewareSpecContributions))
	for _, mw := range rb.middlewareSpecContributions {
		if seen[mw.name] {
			return DuplicateMiddlewareNameError{Route: routeLabel, Name: mw.name}
		}
		seen[mw.name] = true
		if mw.dualAttached {
			return AmbiguousMiddlewareAttachmentError{Name: mw.name}
		}
	}
	return nil
}

// reqreplyParamContribution is one source's declaration for a single
// topic-var/property name, tracked for [checkReqReplyParamConflicts]'s
// conflict detection — mirrors [rest.paramContribution], PLUS a Codec
// field (Round 15's deliberate divergence from REST's real precedent,
// which has no Codec field at all — see
// docs/design/d-0003-codec-declared-middlewares.md's Addendum's
// decision #8).
type reqreplyParamContribution struct {
	source   string
	required bool
	codec    *codex.Codec[string]
}

// checkReqReplyParamConflicts is decision #5's (Round 18-revised) UNIFORM
// conflict-detection algorithm — applies to ALL topic-var/property
// contributions alike, regardless of which mechanism declared them
// (Phase 1b's flat .Use(mqtt5.FromUserPropertyParam(...)) mechanism AND
// the codec-backed Middleware[In,Out] axis's WithRequestTopic/
// WithRequestProperty/etc.). Phase 1b's OWN historical silent-first-seen-
// wins dedupe for MISMATCHED declarations is RETIRED — a deliberate,
// narrow, accepted breaking change (see the doc's decision #5, Round 18).
//
// Two INDEPENDENT namespaces (Round 15) — a topic var and a property
// sharing the SAME name never conflict, since they come from genuinely
// different wire locations (the topic template string vs. out-of-band
// message metadata).
func checkReqReplyParamConflicts(rb *routeBuilder, routeLabel string) error {
	topicContributions := map[string][]reqreplyParamContribution{}
	propertyContributions := map[string][]reqreplyParamContribution{}

	for _, p := range rb.topicParams {
		topicContributions[p.Name] = append(topicContributions[p.Name], reqreplyParamContribution{source: "manual", required: true, codec: p.Codec})
	}
	for _, p := range rb.propertyParams {
		propertyContributions[p.Name] = append(propertyContributions[p.Name], reqreplyParamContribution{source: "manual", required: p.Required, codec: p.Codec})
	}
	for _, mw := range rb.middlewares {
		for _, p := range mw.RequestHeaderParams {
			propertyContributions[p.Name] = append(propertyContributions[p.Name], reqreplyParamContribution{source: mw.Name, required: p.Required, codec: p.Codec})
		}
		for _, p := range mw.ResponseHeaderParams {
			propertyContributions[p.Name] = append(propertyContributions[p.Name], reqreplyParamContribution{source: mw.Name, required: p.Required, codec: p.Codec})
		}
	}
	for _, c := range rb.middlewareSpecContributions {
		for _, p := range c.topicParamsIn {
			topicContributions[p.Name] = append(topicContributions[p.Name], reqreplyParamContribution{source: c.name, required: true, codec: p.Codec})
		}
		for _, p := range c.topicParamsOut {
			topicContributions[p.Name] = append(topicContributions[p.Name], reqreplyParamContribution{source: c.name, required: true, codec: p.Codec})
		}
		for _, p := range c.propertyParamsIn {
			propertyContributions[p.Name] = append(propertyContributions[p.Name], reqreplyParamContribution{source: c.name, required: p.Required, codec: p.Codec})
		}
		for _, p := range c.propertyParamsOut {
			propertyContributions[p.Name] = append(propertyContributions[p.Name], reqreplyParamContribution{source: c.name, required: p.Required, codec: p.Codec})
		}
	}

	if err := checkReqReplyContributionMap(routeLabel, topicContributions); err != nil {
		return err
	}
	return checkReqReplyContributionMap(routeLabel, propertyContributions)
}

// checkReqReplyContributionMap is [checkReqReplyParamConflicts]'s
// per-namespace comparison loop — two contributions for the SAME name
// conflict if Required differs, OR their codec schemas mismatch per
// [route.CodecSchemaMismatch] (Round 15's original inline copy of this
// comparison was later extracted into that shared helper — see
// docs/design/d-0003-codec-declared-middlewares.md's Addendum 2,
// Candidate 2 — REST has since gained the identical comparison too, so
// all 3 APIs are consistent).
func checkReqReplyContributionMap(routeLabel string, contributions map[string][]reqreplyParamContribution) error {
	for name, list := range contributions {
		first := list[0]
		for _, c := range list[1:] {
			if c.required != first.required || route.CodecSchemaMismatch(first.codec, c.codec) {
				return ConflictingParamContributionError{Route: routeLabel, ParamName: name, FirstSource: first.source, SecondSource: c.source}
			}
		}
	}
	return nil
}

// checkImplementationsDeclared is the REVERSE-direction sibling to
// [CheckCoverage]: verifies "every IMPLEMENTED (non-empty-Satisfies)
// scheme was actually declared" — catching a [Route.HandleMW]/
// [Route.ClientMW] call PAIRED against a security scheme name that was
// never [Route.Use]'d on the SAME route. Mirrors
// [rest.checkImplementationsDeclared] exactly. Called by
// [Route.Register]/[Route.ClientHandle] — runs UNCONDITIONALLY.
func checkImplementationsDeclared(routeLabel string, mws []middleware.Middleware, impls []middleware.ServerImplementation, clientImpls []middleware.ClientImplementation) error {
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
	for _, impl := range clientImpls {
		for _, scheme := range impl.Satisfies {
			if !declared[scheme] {
				return UnknownMiddlewareImplementationError{Route: routeLabel, Scheme: scheme}
			}
		}
	}
	return nil
}

// CheckCoverage verifies that every security scheme named anywhere in
// secReqs has at least one [middleware.ServerImplementation] in impls
// whose Satisfies names it — otherwise the route would enforce nothing
// at runtime despite declaring a scheme in its spec. Returns
// [MissingSecurityMiddlewareError] on the first uncovered scheme found.
// Mirrors [rest.CheckCoverage] exactly — called explicitly by the
// attached [ServerTransport] (e.g. mqtt5's `AttachServer`-built
// `serverTransport.Serve`) at Serve time, the point where the route's
// declared security requirements AND its attached
// []middleware.ServerImplementation values are BOTH known.
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

// MissingSecurityMiddlewareError is returned by [CheckCoverage] (called
// from an attached [ServerTransport] at Serve time) when a route
// declares a security scheme (via [WithSecurityScheme]/manual
// [RouteMeta.Security] or a middleware's Security field) with NO
// attached implementation whose Satisfies names that scheme — the route
// would enforce nothing at runtime despite declaring a scheme in its
// spec. Mirrors [rest.MissingSecurityMiddlewareError] — reqreply keeps
// its OWN package-local copy (not a shared cross-package type), matching
// [api/rest]/[api/events]'s own precedent.
type MissingSecurityMiddlewareError struct {
	Route  string
	Scheme string
}

func (e MissingSecurityMiddlewareError) Error() string {
	return fmt.Sprintf("api/reqreply: route %q declares security scheme %q with no attached middleware satisfying it", e.Route, e.Scheme)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e MissingSecurityMiddlewareError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("route", e.Route),
		slog.String("scheme", e.Scheme),
	)
}

// UnknownMiddlewareImplementationError is returned by [Route.Register]/
// [Route.ClientHandle] when a [Route.HandleMW]/[Route.ClientMW] call is
// PAIRED (non-nil mw with non-nil Security) against a security scheme
// name that was never [Route.Use]'d on the SAME route — the
// reverse-direction sibling to [MissingSecurityMiddlewareError]/
// [CheckCoverage]. Mirrors [rest.UnknownMiddlewareImplementationError].
type UnknownMiddlewareImplementationError struct {
	Route  string
	Scheme string
}

func (e UnknownMiddlewareImplementationError) Error() string {
	return fmt.Sprintf("api/reqreply: route %q attaches an implementation satisfying security scheme %q, which was never declared via .Use() on this route", e.Route, e.Scheme)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e UnknownMiddlewareImplementationError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("route", e.Route),
		slog.String("scheme", e.Scheme),
	)
}
