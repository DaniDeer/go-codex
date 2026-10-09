package reqreply

import (
	"fmt"
	"log/slog"
	"slices"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/internal/middleware"
	"github.com/DaniDeer/go-codex/internal/route"
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
			// Its Security declaration (folded into middleware.Declaration
			// per the middleware-consolidation effort,
			// docs/design/d-0003-codec-declared-middlewares.md) is NOT visible to
			// applyAgnosticRoute's spec contribution — synthesize a
			// legacy-shaped Middleware{Name, Security} entry into
			// rb.middlewares too, so applySecurityDeclarations (below)
			// sees it with ZERO changes to that function.
			if sc, ok := mw.(middleware.SecurityCarrier); ok {
				if sec := sc.SecurityDeclaration(); sec != nil {
					name := "declare-security:" + sec.SchemeName
					if named, ok := mw.(interface{ MiddlewareName() string }); ok {
						name = named.MiddlewareName()
					}
					rb.middlewares = append(rb.middlewares, middleware.Middleware{Name: name, Security: sec})
				}
			}
		}
	}
}

// routeMiddlewareContributor is implemented by [Middleware][In, Out]
// (transform.go/middleware_declaration.go) — the unexported interface
// [routeMiddlewareOpt.applyRoute] uses to recognize a codec-backed
// Middleware value attached via plain .Use(), mirroring
// [rest.routeMiddlewareContributor] exactly. The route-BOUND path no
// longer lives on this interface — see [BoundMiddleware]/
// [BoundClientMiddleware] (bound_middleware.go) and [boundContributor]/
// [boundClientContributor].
type routeMiddlewareContributor interface {
	applyAgnosticRoute(rb *routeBuilder)
	MiddlewareName() string
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

// buildServerImplementation always builds a GENERAL-PURPOSE
// [middleware.ServerImplementation] (Satisfies empty) — mw is only ever
// non-Security-carrying by the time this runs: the earlier
// [routeMiddlewareContributor] check in [Route.HandleMW] already
// intercepts the one type ([Middleware][In, Out]) that can carry a
// Security declaration, routing it to [MiddlewareMisattachedError]
// instead. A Security-carrying scheme is PAIRED exclusively via
// [Route.HandleBoundMW]/[BoundSecurityMiddleware] now.
func buildServerImplementation(fn any) middleware.ServerImplementation {
	return middleware.ServerImplementation{Name: "implement:general", Fn: fn}
}

// NOTE: isBoundHandleMWShape/isBoundClientMWShape (the reflection-based
// Fn-shape detectors HandleMW/ClientMW used to use to silently promote a
// codec-backed [Middleware][In, Out] to the route-BOUND dispatch path)
// were REMOVED as part of docs/design/d-0003-codec-declared-middlewares.md's Addendum 7 — the
// route-BOUND case is now ALWAYS explicit, via the dedicated
// [BoundMiddleware][Req, In, Out] type (see bound_middleware.go) and
// [Route.HandleBoundMW]/[Route.ClientBoundMW], never Fn-shape guessing.

// HandleMW is the server-side GENERAL-PURPOSE implementation-attachment
// method — mw is NILABLE, and is REJECTED if it's a codec-backed
// [Middleware][In, Out] or [BoundMiddleware][Req, In, Out] (via
// [MiddlewareMisattachedError] — those attach ONLY via plain .Use() and
// [Route.HandleBoundMW] respectively, never HandleMW; see
// docs/design/d-0003-codec-declared-middlewares.md's Addendum 7):
//   - nil (or any other non-codec-backed value): always GENERAL-PURPOSE —
//     fn runs unconditionally, nothing to satisfy. A Security scheme is
//     declared and PAIRED exclusively via [Route.HandleBoundMW] +
//     [BoundSecurityMiddleware] now — HandleMW never carries a Security
//     declaration.
//
// fn is deliberately untyped (any) — resolved by the attached adapter
// (e.g. mqtt5's `NewServerTransport`) via a type-switch/reflection, mirroring
// [middleware.ServerImplementation.Fn]'s existing type-erasure. A
// wrong-shaped fn fails with a typed error at Serve time, never silently.
func (r Route[Req, Resp]) HandleMW(mw middleware.RouteMiddleware, fn any) Route[Req, Resp] {
	if v, ok := mw.(routeMiddlewareContributor); ok {
		r.opts = append(slices.Clone(r.opts), misattachedOpt{route: r.topic, name: v.MiddlewareName()})
		return r
	}
	r.opts = append(slices.Clone(r.opts), handleMWOpt{impl: buildServerImplementation(fn)})
	return r
}

// misattachedOpt stashes a [MiddlewareMisattachedError] onto rb when a
// codec-backed [Middleware][In, Out] is passed to [Route.HandleMW]/
// [Route.ClientMW] instead of its own dedicated attachment point (.Use()
// or HandleBoundMW/ClientBoundMW) — see bound_middleware.go's
// [MiddlewareMisattachedError] doc comment.
type misattachedOpt struct {
	route string
	name  string
}

func (o misattachedOpt) applyRoute(rb *routeBuilder) {
	if rb.buildErr == nil {
		rb.buildErr = MiddlewareMisattachedError{Route: o.route, Name: o.name}
	}
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

// ClientMW is the client-side GENERAL-PURPOSE implementation-attachment
// method — the CLIENT-side mirror of [Route.HandleMW], including its
// REJECTION of a codec-backed [Middleware][In, Out] or
// [BoundClientMiddleware][Req, In, Out] (via [MiddlewareMisattachedError]
// — those attach ONLY via plain .Use() and [Route.ClientBoundMW]
// respectively, never ClientMW). mw is otherwise always GENERAL-PURPOSE —
// Satisfies always empty, fn always runs unconditionally. A Security
// scheme is supplied exclusively via [Route.ClientBoundMW] +
// [BoundSecurityClientMiddleware] now — ClientMW never carries a Security
// declaration.
//
// Name includes a per-route attachment-order index (e.g.
// "fulfill:general#1") so that TWO ClientMW calls attached on the SAME
// route still get DISTINCT Names — mirrors [rest.Route.ClientMW]'s
// identical rationale.
func (r Route[Req, Resp]) ClientMW(mw middleware.RouteMiddleware, fn any) Route[Req, Resp] {
	if v, ok := mw.(routeMiddlewareContributor); ok {
		r.opts = append(slices.Clone(r.opts), misattachedOpt{route: r.topic, name: v.MiddlewareName()})
		return r
	}
	idx := 0
	for _, o := range r.opts {
		if _, ok := o.(clientMWOpt); ok {
			idx++
		}
	}
	impl := middleware.ClientImplementation{Fn: fn, Name: fmt.Sprintf("fulfill:general#%d", idx)}
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

// applyParamDeclarations collects every property param contributed via
// [Route.Use] (the codec-backed [Middleware.WithRequestPropertySpec]/
// [Middleware.WithResponsePropertySpec] axis — the legacy
// [middleware.Middleware] type is Security-only now, see
// docs/design/d-0006-protocol-native-capabilities.md's
// middleware-consolidation effort) and renders them into two AsyncAPI
// "headers" schemas — one for the request message, one for the reply
// message. Mirrors [rest.applyParamDeclarations], SCOPED DOWN: reqreply's
// [RouteMeta] has no manual header-param declaration fields to
// merge/conflict-check against (unlike REST's rb.headerParams/
// rb.respHeaders), so this only needs to dedup by name across attached
// middlewares — two middlewares contributing the SAME name are folded
// into one property, first-seen wins. Returns two zero [schema.Schema]
// values when no header params were declared — [render/asyncapi/
// v3.Message.Headers] treats a zero Schema as "omit the headers field
// entirely" (see its own [schema.Schema.IsZero] check in buildMessage).
// Also returns the deduped raw param specs themselves
// (reqParams/respParams) — populated onto [RouteHandle.
// RequestHeaderParams]/[RouteHandle.ResponseHeaderParams] for the
// attached adapter (e.g. mqtt5's `NewServerTransport`/`NewClientTransport`)
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

	// Unify the codec-backed Middleware[In,Out] axis's property
	// contributions (WithRequestProperty/WithResponseProperty, attached
	// via HandleBoundMW/ClientBoundMW or plain .Use()) into the SAME
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
		// presencePropertyParamsIn/Out (WithRequestPropertySpec/
		// WithResponsePropertySpec) have NO alternate runtime-validation
		// path of their own (unlike the merge-field contributions above,
		// which are deliberately excluded here) — so they DO feed
		// reqParams/respParams too, exactly like Phase 1b's flat
		// mechanism, closing docs/design/d-0003-codec-declared-middlewares.md's
		// Phase D0 gap.
		for _, p := range c.presencePropertyParamsIn {
			spec := middleware.HeaderParamSpec{Name: p.Name, Description: p.Description, Required: p.Required, Codec: p.Codec}
			if seenReq[p.Name] {
				continue
			}
			seenReq[p.Name] = true
			reqParams = append(reqParams, spec)
			reqProps = append(reqProps, headerParamProperty(p.Name, p.Description, p.Codec))
			if p.Required {
				reqRequired = append(reqRequired, p.Name)
			}
		}
		for _, p := range c.presencePropertyParamsOut {
			spec := middleware.ResponseHeaderParamSpec{Name: p.Name, Description: p.Description, Required: p.Required, Codec: p.Codec}
			if seenResp[p.Name] {
				continue
			}
			seenResp[p.Name] = true
			respParams = append(respParams, spec)
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

// checkMiddlewareNameUniquenessAndAttachment enforces D6(b) from
// docs/design/d-0003-codec-declared-middlewares.md, mirroring
// [rest.checkMiddlewareNameUniquenessAndAttachment] — reqreply's own
// codec-backed [Middleware][In,Out] axis: every attached Middleware's
// Declaration.Name must be unique per route (across ALL
// HandleBoundMW/ClientBoundMW/.Use() attachments) — returns
// [DuplicateMiddlewareNameError] on the first repeat encountered. D7 (the
// ambiguous-dual-attachment check) is GONE — structurally impossible
// since docs/design/d-0003-codec-declared-middlewares.md's Addendum 7: a codec-backed
// [Middleware][In, Out] can only ever be attached via .Use() now (the
// bound attachment point is a SEPARATE, distinct type,
// [BoundMiddleware]/[BoundClientMiddleware] — see
// [Route.HandleBoundMW]/[Route.ClientBoundMW]), so one value can never
// satisfy both attachment styles at once.
func checkMiddlewareNameUniquenessAndAttachment(rb *routeBuilder, routeLabel string) error {
	seen := make(map[string]bool, len(rb.middlewareSpecContributions))
	for _, mw := range rb.middlewareSpecContributions {
		if seen[mw.name] {
			return DuplicateMiddlewareNameError{Route: routeLabel, Name: mw.name}
		}
		seen[mw.name] = true
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
// contributions alike, regardless of which mechanism declared them (the
// codec-backed Middleware[In,Out] axis's WithRequestTopic/
// WithRequestProperty/WithRequestPropertySpec/etc. — the legacy
// middleware.Middleware type is Security-only now, see
// docs/design/d-0006-protocol-native-capabilities.md's
// middleware-consolidation effort).
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
		for _, p := range c.presencePropertyParamsIn {
			propertyContributions[p.Name] = append(propertyContributions[p.Name], reqreplyParamContribution{source: c.name, required: p.Required, codec: p.Codec})
		}
		for _, p := range c.presencePropertyParamsOut {
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
// secReqs has at least one covering attachment — either a
// [middleware.ServerImplementation] in impls (the legacy raw-adapter-Fn
// path) whose Satisfies names it, OR a [MiddlewareHandler] in handlers
// (populated by BOTH `.Use()`, the reusable class, AND `HandleBoundMW`,
// the bound class — see [BoundMiddleware.ApplyBoundRoute]) whose own
// Satisfies names it — otherwise the route would enforce nothing at
// runtime despite declaring a scheme in its spec. Returns
// [MissingSecurityMiddlewareError] on the first uncovered scheme found.
// Mirrors [rest.CheckCoverage] exactly — called explicitly by the
// attached [ServerTransport] (e.g. mqtt5's `NewServerTransport`-built
// `serverTransport.Serve`) at Serve time, the point where the route's
// declared security requirements AND its attached
// []middleware.ServerImplementation/[]MiddlewareHandler values are BOTH
// known.
func CheckCoverage(routeLabel string, secReqs []route.SecurityRequirement, impls []middleware.ServerImplementation, handlers []MiddlewareHandler) error {
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
				for _, h := range handlers {
					if slices.Contains(h.Satisfies, schemeName) {
						satisfied = true
						break
					}
				}
			}
			if !satisfied {
				return MissingSecurityMiddlewareError{Route: routeLabel, Scheme: schemeName}
			}
		}
	}
	return nil
}

// IsSecuritySatisfyingHandler reports whether handlers contains an entry
// named name with a non-empty Satisfies — i.e. a Security-carrying
// [MiddlewareHandler] (populated by either `.Use()`, the reusable class,
// or `HandleBoundMW`, the bound class). Consuming adapters (mqtt5,
// zeromq) call this from their own `DispatchServerMiddlewareHandlers`
// error-handling to keep Security's OWN distinct error fallback
// ([SecurityError], not the generic [MiddlewareError]) for a FAILING
// Security-gated Fn, even though it dispatches through the SAME
// mechanism every other Middleware-attached Fn uses — mirrors
// `rest.isSecuritySatisfyingHandler`/`chi`'s identical, per-adapter
// helper exactly, centralized HERE (not duplicated per reqreply adapter)
// since both consuming adapters operate on reqreply's own
// [MiddlewareHandler] type directly.
func IsSecuritySatisfyingHandler(handlers []MiddlewareHandler, name string) bool {
	for _, h := range handlers {
		if h.Name == name {
			return len(h.Satisfies) > 0
		}
	}
	return false
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
