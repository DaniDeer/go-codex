package chi

import (
	"context"
	"net/http"

	"github.com/DaniDeer/go-codex/adapters/internal/restdispatch"
	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/internal/middleware"
	gochi "github.com/go-chi/chi/v5"
)

// writeSSEData is PROMOTED (Phase 4, SSE family dispatch-core
// consolidation) to [restdispatch.WriteSSEData] — aliased here (not
// copied), mirroring adapters/nethttp's identical promotion, so every
// existing call site in this package (including adapter.go's
// sseHandlerFunc) keeps compiling and behaving identically.
var writeSSEData = restdispatch.WriteSSEData

// serveSSE walks every [rest.SSERoute] registered into b (via
// [rest.SSERoute.Register]/[rest.SSERoute.RegisterHandle]) and wires each
// handler-bearing one onto mux — the SSE counterpart to [serve], sharing
// its EXACT failure semantics (Part 1 skip-no-handler, Part 2
// all-or-nothing validation, Part 3 duplicate detection + recover safety
// net) and its reflect-based generic dispatch mechanism (see "Decision:
// serve's generic dispatch mechanism" in
// docs/design/d-0001-rest-middleware-workflow-simplification.md) — SSERouteHandle's
// EncodeEvent/ValidateEvent/MergeEvent and the send func(Event) error
// callback shape are all invoked via reflect.Value.Call since Req/Event
// are erased at this call site.
func serveSSE(r gochi.Router, b *rest.Server) error {
	entries := b.SSEEntries()

	var routeErrs []RouteError
	var toWire []wiredRoute
	seen := make(map[string]bool, len(entries))
	seenShapes := make(map[string]string, len(entries))

	for _, e := range entries {
		if !e.HasHandler() {
			continue // Part 1: spec-only, never validated, never wired.
		}
		method, path := e.Method(), e.Path()
		if seen[method+" "+path] {
			routeErrs = append(routeErrs, RouteError{Method: method, Path: path,
				Err: DuplicateRouteError{Method: method, Path: path}})
			continue
		}
		seen[method+" "+path] = true

		shapeKey := method + " " + rest.PathShape(path)
		if conflictsWith, ok := seenShapes[shapeKey]; ok {
			routeErrs = append(routeErrs, RouteError{Method: method, Path: path,
				Err: PathShapeConflictError{Method: method, Path: path, ConflictsWith: conflictsWith}})
			continue
		}
		seenShapes[shapeKey] = path

		h, err := buildSSERouteHandler(e.Handle())
		if err != nil {
			routeErrs = append(routeErrs, RouteError{Method: method, Path: path, Err: err})
			continue
		}
		toWire = append(toWire, wiredRoute{method: method, path: path, handler: h})
	}
	if len(routeErrs) > 0 {
		return MultiRouteError{Errors: routeErrs}
	}

	return wireRoutes(r, toWire)
}

// buildSSERouteHandler is [buildRouteHandler]'s SSE counterpart — see its
// doc comment for the shared reflect-based rationale. handle is a
// *rest.SSERouteHandle[Req, Event] stored as any.
func buildSSERouteHandler(handle any) (http.Handler, error) {
	cr, err := restdispatch.ValidateRouteConstruction("chi", handle)
	if err != nil {
		return nil, err
	}

	opts, err := resolveOptions(cr.Descriptor.Method, cr.Descriptor.Path, cr.HandlerOptsAny)
	if err != nil {
		return nil, err
	}
	errFn := opts.ErrorHandler
	if errFn == nil {
		errFn = defaultErrorHandler
	}
	writeCookie := func(w http.ResponseWriter, name, value string, attrs rest.CookieAttributes) error {
		return SetCookie(w, name, value, cookieOptionsFrom(attrs))
	}

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &statusResponseWriter{ResponseWriter: w, code: http.StatusOK}
		ctx := middleware.EnsureContextFields(r.Context())
		ctx = context.WithValue(ctx, contextKey{}, r)
		responseHeaders := make(http.Header)
		ctx = context.WithValue(ctx, responseHeadersKey{}, responseHeaders)
		pendingCookies := make([]PendingCookie, 0)
		ctx = context.WithValue(ctx, responseCookiesKey{}, &pendingCookies)
		restdispatch.DispatchSSERoute(ctx, cr, sw, r, responseHeaders, &pendingCookies, pathValues, writeCookie, errFn, opts.MultiValueQueryParams)
	})

	return applyGeneralMiddleware(inner, cr.Impls), nil
}

// negotiateFormatReflect is PROMOTED (Phase 4, REST family dispatch-core
// consolidation) to [restdispatch.NegotiateFormatReflect] — aliased here
// (not copied), mirroring adapters/nethttp's identical promotion.
var negotiateFormatReflect = restdispatch.NegotiateFormatReflect
