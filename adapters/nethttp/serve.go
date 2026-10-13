package nethttp

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"reflect"
	"strings"

	"github.com/DaniDeer/go-codex/adapters/internal/restdispatch"
	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/internal/middleware"
)

// RouteError pairs a route's identity with what went wrong on it — one
// entry of a [MultiRouteError].
type RouteError struct {
	Method string
	Path   string
	Err    error
}

func (e RouteError) Error() string {
	return fmt.Sprintf("%s %s: %v", e.Method, e.Path, e.Err)
}

func (e RouteError) Unwrap() error { return e.Err }

// MultiRouteError is returned by [serve]/[serveSSE] when one or more
// handler-bearing routes fail validation. Carries EVERY individual
// failure found during the pre-wiring validation pass — not just the
// first — so a caller sees the complete list of what's wrong in one
// error. Unwrap() []error (Go 1.20+ multi-error support) lets
// errors.As/Is reach into ANY individual route's error directly.
type MultiRouteError struct {
	Errors []RouteError
}

func (e MultiRouteError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d route(s) failed validation:", len(e.Errors))
	for _, re := range e.Errors {
		fmt.Fprintf(&b, "\n  - %s", re.Error())
	}
	return b.String()
}

func (e MultiRouteError) Unwrap() []error {
	out := make([]error, len(e.Errors))
	for i, re := range e.Errors {
		out[i] = re
	}
	return out
}

// DuplicateRouteError is returned (wrapped in a [RouteError] inside a
// [MultiRouteError]) when two handler-bearing routes declare the SAME
// Method+Path — a *http.ServeMux.Handle panic waiting to happen.
type DuplicateRouteError struct {
	Method string
	Path   string
}

func (e DuplicateRouteError) Error() string {
	return fmt.Sprintf("duplicate route: %s %s already registered", e.Method, e.Path)
}

// PathShapeConflictError is returned (wrapped in a [RouteError] inside a
// [MultiRouteError]) when two handler-bearing routes declare the SAME
// Method and the SAME structural path shape (see [rest.PathShape]) but
// DIFFERENT variable names — e.g. "/users/{id}" and "/users/{name}". This
// is not a literal [DuplicateRouteError], but *http.ServeMux treats it as
// an unresolvable routing conflict and panics at registration time; this
// check surfaces it as a typed error BEFORE that panic can occur.
type PathShapeConflictError struct {
	Method        string
	Path          string
	ConflictsWith string
}

func (e PathShapeConflictError) Error() string {
	return fmt.Sprintf("route %s %s has the same structural shape as already-registered %s %s",
		e.Method, e.Path, e.Method, e.ConflictsWith)
}

func (e PathShapeConflictError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("method", e.Method),
		slog.String("path", e.Path),
		slog.String("conflicts_with", e.ConflictsWith),
	)
}

// OptionsShapeError is returned when a [rest.Route.WithOptions]/
// [rest.SSERoute.WithOptions] value's concrete type is not [Options] —
// mirrors [rest.FormatOptError]'s pattern.
type OptionsShapeError struct {
	Method, Path string
	Got          any
}

func (e OptionsShapeError) Error() string {
	return fmt.Sprintf("nethttp: %s %s: WithOptions value has wrong type: want nethttp.Options, got %T", e.Method, e.Path, e.Got)
}

// resolveOptions type-asserts a route's type-erased HandlerOpts (from
// [rest.Route.WithOptions]) to [Options]. nil means WithOptions was never
// called — the adapter's zero-value Options apply.
func resolveOptions(method, path string, handlerOpts any) (Options, error) {
	if handlerOpts == nil {
		return Options{}, nil
	}
	opts, ok := handlerOpts.(Options)
	if !ok {
		return Options{}, OptionsShapeError{Method: method, Path: path, Got: handlerOpts}
	}
	return opts, nil
}

// wiredRoute pairs a built http.Handler with its Method+Path, staged for
// the final wiring loop after ALL routes have passed validation.
type wiredRoute struct {
	method, path string
	handler      http.Handler
}

// serve walks every [rest.Route] registered into b (via [rest.Route.Register]/
// [rest.Route.RegisterHandle]) and wires each handler-bearing one onto mux
// — the SOLE public server-side entry point for regular (non-SSE) routes
// (see docs/design/d-0001-rest-middleware-workflow-simplification.md's "Decision:
// serve is the only public server-side entry point").
//
// Part 1 — routes with NO [rest.Route.WithHandler] call are spec-only and
// SKIPPED entirely: no validation, no wiring, no error (see "Decision:
// serve's whole-builder failure semantics").
//
// Part 2 — every handler-bearing route is validated FIRST (implementation
// shape + security coverage), collecting ALL failures into ONE
// [MultiRouteError] — serve wires NOTHING (zero mux.Handle calls) if even
// one fails.
//
// Part 3 — duplicate Method+Path is detected proactively (folded into the
// SAME validation pass, surfacing as [DuplicateRouteError]); a defensive
// recover() around the wiring loop converts any other unanticipated
// mux.Handle panic into a returned error.
//
// Generic dispatch: serve is non-generic and walks a HETEROGENEOUS
// collection of routes with different Req/Resp per route — building each
// one's http.Handler uses reflect.Value.Call against the route's
// ALREADY-CONCRETE exported closures (Decode/Encode/HandlerFn/each
// [rest.ServerImplementation.Fn]), never reflective generic
// instantiation (which Go does not support) — see "Decision: serve's
// generic dispatch mechanism" in the roadmap doc for the full rationale.
func serve(mux *http.ServeMux, b *rest.Server) error {
	entries := b.RouteEntries()

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

		h, err := buildRouteHandler(e.Handle())
		if err != nil {
			routeErrs = append(routeErrs, RouteError{Method: method, Path: path, Err: err})
			continue
		}
		toWire = append(toWire, wiredRoute{method: method, path: path, handler: h})
	}
	if len(routeErrs) > 0 {
		return MultiRouteError{Errors: routeErrs}
	}

	return wireRoutes(mux, toWire)
}

func wireRoutes(mux *http.ServeMux, toWire []wiredRoute) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("nethttp.serve: panic wiring routes: %v", r)
		}
	}()
	for _, rt := range toWire {
		mux.Handle(rt.method+" "+rt.path, rt.handler)
	}
	return nil
}

// ServeOne builds a bare [http.Handler] for exactly ONE route — pure
// sugar, implemented as "build a scratch single-route Builder, register
// route into it, call serve, return the resulting mux." Reuses serve's
// exact validation path; not a bypass. Takes no [Options] parameter —
// call route.WithOptions(opts) first for a custom Options on this route.
func ServeOne[Req, Resp any](r rest.Route[Req, Resp]) (http.Handler, error) {
	b := rest.NewServer(rest.Info{})
	if err := r.Register(b); err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	if err := serve(mux, b); err != nil {
		return nil, err
	}
	return mux, nil
}

// buildRouteHandler validates handle's attached implementation shapes
// EAGERLY (returning an error immediately for a malformed Fn, never
// silently) and returns the [http.Handler] that runs the full
// decode/validate/security/handle/encode pipeline per request — the
// SAME pipeline `handlerFunc` runs, invoked via reflect.Value.Call since
// Req/Resp are erased at this call site. handle is a *rest.RouteHandle[Req, Resp]
// stored as any.
//
// The construction-time recovery + four-check validation (shape, security
// coverage, capability coverage, param-kind coverage) is now shared with
// adapters/chi via [restdispatch.ValidateRouteConstruction] (Phase 4, REST
// family dispatch-core consolidation — previously independently
// duplicated, confirmed byte-for-byte identical, in both packages' own
// buildRouteHandler).
func buildRouteHandler(handle any) (http.Handler, error) {
	cr, err := restdispatch.ValidateRouteConstruction("nethttp", handle)
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
	maxBody := opts.MaxBodyBytes
	if maxBody <= 0 {
		maxBody = maxRequestBodyBytes
	}
	expectedCT := opts.ContentType
	if expectedCT == "" {
		expectedCT = "application/json"
	}
	writeCookie := func(w http.ResponseWriter, name, value string, attrs rest.CookieAttributes) error {
		return SetCookie(w, name, value, cookieOptionsFrom(attrs))
	}

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &statusResponseWriter{ResponseWriter: w, code: http.StatusOK}
		ctx := middleware.EnsureContextFields(r.Context())
		ctx = context.WithValue(ctx, contextKey{}, r)
		respHeaders := make(http.Header)
		ctx = context.WithValue(ctx, responseHeadersKey{}, respHeaders)
		pendingCookies := make([]PendingCookie, 0)
		ctx = context.WithValue(ctx, responseCookiesKey{}, &pendingCookies)
		restdispatch.DispatchRoute(ctx, cr, sw, r, respHeaders, &pendingCookies, pathValues, writeCookie, errFn, maxBody, expectedCT, opts.MultiValueQueryParams)
	})

	return applyGeneralMiddleware(inner, cr.Impls), nil
}

// callErr/callErrStatusFor are PROMOTED (Phase 4) to restdispatch's
// identical functions — aliased here (not copied).
func callErr(target reflect.Value, method string, arg reflect.Value) error {
	return restdispatch.CallErr(target, method, arg)
}

func callErrStatusFor(target reflect.Value, err error) (int, bool) {
	return restdispatch.CallErrStatusFor(target, err)
}

// formatContentTypesReflect/negotiateRequestFormatReflect/
// tryRespondErrorPattern (the reflect-based one, used only by
// buildRouteHandler) are PROMOTED (Phase 4) into
// adapters/internal/restdispatch's DispatchRoute — buildRouteHandler now
// calls restdispatch.DispatchRoute directly, so these three are DELETED
// here rather than aliased (confirmed via grep: nothing else in this
// package referenced them).

// validateImplementationShapesReflect checks every attached impl.Fn against
// the two concrete shapes this package recognizes, EAGERLY at serve
// construction time rather than deferring to the first incoming request —
// a malformed Fn fails loudly and immediately. reqType is the route's Req
// type, recovered at runtime via reflect (see [buildRouteHandler]), letting
// serve build the EXACT expected security Fn shape
// (func(context.Context, *http.Request, *Req) (map[string][]string, error))
// dynamically instead of via a static type parameter.
// satisfiesPerHandler/isSecuritySatisfyingHandler are PROMOTED (Phase 4)
// to restdispatch's identical functions — aliased here (not copied).
var satisfiesPerHandler = restdispatch.SatisfiesPerHandler
var isSecuritySatisfyingHandler = restdispatch.IsSecuritySatisfyingHandler

func validateImplementationShapesReflect(routeLabel string, reqType reflect.Type, impls []rest.ServerImplementation) error {
	generalType := reflect.TypeOf((func(http.Handler) http.Handler)(nil))
	securityType := reflect.FuncOf(
		[]reflect.Type{
			reflect.TypeOf((*context.Context)(nil)).Elem(),
			reflect.TypeOf((*http.Request)(nil)),
			reflect.PointerTo(reqType),
		},
		[]reflect.Type{
			reflect.TypeOf(map[string][]string(nil)),
			reflect.TypeOf((*error)(nil)).Elem(),
		},
		false,
	)
	for _, impl := range impls {
		if impl.Fn == nil {
			continue
		}
		fnType := reflect.TypeOf(impl.Fn)
		if fnType == generalType || fnType == securityType {
			continue
		}
		return rest.MiddlewareShapeError{
			Name:     impl.Name,
			Expected: "func(http.Handler) http.Handler or func(context.Context, *http.Request, *Req) (map[string][]string, error)",
			Got:      fmt.Sprintf("%T", impl.Fn),
		}
	}
	return nil
}
