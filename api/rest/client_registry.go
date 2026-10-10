package rest

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/DaniDeer/go-codex/internal/templatematch"
)

// clientRegistryPkgPath is api/rest's own import path — used to recognize
// a genuine rest.Route[Req,Resp]/rest.SSERoute[Req,Event]/
// *rest.RouteHandle[Req,Resp]/*rest.SSERouteHandle[Req,Event] value
// (for ANY type params), mirroring adapters/nethttp's identical
// restPkgPath-based discrimination in recoverClientRouteHandleValue/
// recoverClientSSERouteHandleValue — the SAME technique, now needed
// INSIDE api/rest itself since the redirect-target registry's
// auto-populate step runs at the API layer, before any adapter is
// involved.
const clientRegistryPkgPath = "github.com/DaniDeer/go-codex/api/rest"

// recoverRegistryEntry reflects routeAny into this Client's redirect-
// target registry shape: a [routeKey] (Method+PathTemplate) plus the
// recovered handle (dynamic type *RouteHandle[Req,Resp] or
// *SSERouteHandle[Req,Event]) and whether it's an SSE (streaming) route.
// Accepts the SAME dual-mode shapes [Client.Call]/[Client.Consume] do: a
// raw, unregistered Route[Req,Resp]/SSERoute[Req,Event] (calls its own
// ClientHandle() method reflectively), or an already-built
// *RouteHandle[Req,Resp]/*SSERouteHandle[Req,Event] (used as-is).
func recoverRegistryEntry(routeAny any) (key routeKey, handle any, isSSE bool, err error) {
	rv := reflect.ValueOf(routeAny)
	if !rv.IsValid() {
		return routeKey{}, nil, false, TransportTypeMismatchError{
			Want: "rest.Route[Req, Resp], rest.SSERoute[Req, Event], *rest.RouteHandle[Req, Resp], or *rest.SSERouteHandle[Req, Event]",
			Got:  fmt.Sprintf("%T", routeAny),
		}
	}
	t := rv.Type()
	switch {
	case t.PkgPath() == clientRegistryPkgPath && strings.HasPrefix(t.Name(), "Route["):
		handleVal := rv.MethodByName("ClientHandle").Call(nil)[0]
		return descriptorKeyOf(handleVal), handleVal.Interface(), false, nil
	case t.Kind() == reflect.Pointer && t.Elem().PkgPath() == clientRegistryPkgPath && strings.HasPrefix(t.Elem().Name(), "RouteHandle["):
		return descriptorKeyOf(rv), rv.Interface(), false, nil
	case t.PkgPath() == clientRegistryPkgPath && strings.HasPrefix(t.Name(), "SSERoute["):
		handleVal := rv.MethodByName("ClientHandle").Call(nil)[0]
		return descriptorKeyOf(handleVal), handleVal.Interface(), true, nil
	case t.Kind() == reflect.Pointer && t.Elem().PkgPath() == clientRegistryPkgPath && strings.HasPrefix(t.Elem().Name(), "SSERouteHandle["):
		return descriptorKeyOf(rv), rv.Interface(), true, nil
	default:
		return routeKey{}, nil, false, TransportTypeMismatchError{
			Want: "rest.Route[Req, Resp], rest.SSERoute[Req, Event], *rest.RouteHandle[Req, Resp], or *rest.SSERouteHandle[Req, Event]",
			Got:  fmt.Sprintf("%T", routeAny),
		}
	}
}

// descriptorKeyOf reads Method/Path off handleVal's Descriptor field
// (handleVal is always a *RouteHandle[Req,Resp]/*SSERouteHandle[Req,Event]
// reflect.Value — both expose the SAME Descriptor RouteDescriptor field).
func descriptorKeyOf(handleVal reflect.Value) routeKey {
	descriptor := handleVal.Elem().FieldByName("Descriptor")
	return routeKey{
		Method: descriptor.FieldByName("Method").String(),
		Path:   descriptor.FieldByName("Path").String(),
	}
}

// RegisterRoute warms c's redirect-target registry with route without
// making a live call against it — for a route that is only ever reached
// as a redirect target, never called directly (routes called directly
// via [Client.Call]/[Client.Consume] are indexed automatically). route
// may be a [Route]/[*RouteHandle] (indexed into the Call-decodable
// registry) or an [SSERoute]/[*SSERouteHandle] (indexed into the
// Consume-streamable registry) — discriminated via reflection.
//
// Re-registering the SAME Method+PathTemplate (even via a DIFFERENT
// credential-bound ClientHandle variant of the identical route) is a
// safe no-op — first-registered-wins, mirrors [events.Client]'s own
// specByTopic dedup policy.
func (c *Client) RegisterRoute(route any) error {
	key, handle, isSSE, err := recoverRegistryEntry(route)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.registerLocked(key, handle, isSSE)
	return nil
}

// registerLocked indexes (key, handle) into the appropriate registry map
// (and its companion order slice, for deterministic ambiguous-match
// resolution — see [Client.routesOrder]'s doc comment) — c.mu MUST
// already be held (write lock) by the caller. First-registered-wins: a
// pre-existing entry for key is never overwritten, and the order slice
// only ever grows on a genuinely NEW key.
func (c *Client) registerLocked(key routeKey, handle any, isSSE bool) {
	m, order := &c.routes, &c.routesOrder
	if isSSE {
		m, order = &c.sseRoutes, &c.sseRoutesOrder
	}
	if *m == nil {
		*m = make(map[routeKey]*registeredRoute)
	}
	if _, exists := (*m)[key]; exists {
		return
	}
	(*m)[key] = &registeredRoute{Handle: handle}
	*order = append(*order, key)
}

// registerAutoFromCall is called by [Client.Call]/[Client.Consume]
// BEFORE delegating to the attached [ClientTransport] — auto-populates
// the registry the first time a given route is seen, per [Client.
// RegisterRoute]'s own doc comment ("routes called directly via Call/
// Consume are indexed automatically"). Best-effort: a route value that
// doesn't match the recognized shapes is silently skipped here (NOT an
// error) — the attached [ClientTransport]'s own dispatch is the
// authoritative place a genuine type-mismatch is reported, this is pure
// bookkeeping that must never mask or duplicate that error.
func (c *Client) registerAutoFromCall(route any) {
	key, handle, isSSE, err := recoverRegistryEntry(route)
	if err != nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.registerLocked(key, handle, isSSE)
}

// MatchRedirectRoute consults c's Call-decodable registry for a route
// whose Method+PathTemplate matches method+location — the adapter-facing
// accessor [adapters/nethttp]'s client transport calls (via
// [ClientAwareTransport.BindClient]'s retained [*Client] reference) to
// resolve an auto-followed redirect's target. Returns the matched
// *RouteHandle[Req,Resp] (as `any`) and the vars [internal/templatematch.
// MatchNonWildcard] extracted from location, or ok=false if no registered
// template matches (the caller then returns [UnrecognizedRedirectError]).
// First-registered-wins when more than one registered template matches
// the same concrete location+method (mirrors [events.Client]'s own
// specByTopic dedup policy — see [Client.RegisterRoute]'s doc comment).
//
// Exported for adapter consumption — not part of the declarative
// vocabulary a route/channel-declaring caller uses directly, same
// convention as [RouteHandle.EncodeVars]/[BuildPath].
func (c *Client) MatchRedirectRoute(method, location string) (handle any, ok bool) {
	return c.matchRegistry(c.routes, c.routesOrder, method, location)
}

// MatchRedirectSSERoute is [MatchRedirectRoute]'s Consume-streamable
// sibling, consulting c.sseRoutes instead of c.routes.
func (c *Client) MatchRedirectSSERoute(method, location string) (handle any, ok bool) {
	return c.matchRegistry(c.sseRoutes, c.sseRoutesOrder, method, location)
}

// templateMismatchErr is [templatematch.MatchNonWildcard]'s required
// wrapMismatch callback — the registry match loop only ever checks
// err == nil/non-nil (trying every candidate template in turn), so the
// error's own content is never inspected; this just satisfies the
// non-nil-func requirement cheaply.
func templateMismatchErr(template, concrete string) error {
	return fmt.Errorf("api/rest: %q does not match template %q", concrete, template)
}

// matchRegistry iterates order (NOT registry directly — a plain Go map
// has no defined iteration order, and ambiguous-match resolution
// requires deterministic first-registered-wins) trying each registered
// template in REGISTRATION order against location, Method-filtered
// first.
func (c *Client) matchRegistry(registry map[routeKey]*registeredRoute, order []routeKey, method, location string) (any, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, key := range order {
		if key.Method != method {
			continue
		}
		if _, err := templatematch.MatchNonWildcard(key.Path, location, templateMismatchErr); err == nil {
			return registry[key].Handle, true
		}
	}
	return nil, false
}
