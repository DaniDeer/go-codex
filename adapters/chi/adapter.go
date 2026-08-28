package chi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"

	gochi "github.com/go-chi/chi/v5"

	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/stats"
)

// maxRequestBodyBytes is the maximum number of bytes read from a request body.
const maxRequestBodyBytes = 1 << 20 // 1 MiB

// contextKey is the unexported type for request values stored in context by this package.
type contextKey struct{}

// responseHeadersKey is the unexported type for response header values stored in context.
type responseHeadersKey struct{}

// responseCookiesKey is the unexported type for pending response cookies stored in context.
type responseCookiesKey struct{}

// CookieOptions configures the security attributes and optional value validation
// applied by [SetCookie].
//
// Safe defaults: Secure=true, HttpOnly=true, SameSite=Strict, Path="/".
// Use the opt-in fields to relax specific attributes for legitimate use cases.
type CookieOptions struct {
	// Path is the cookie path. Defaults to "/" when empty.
	Path string

	// Domain scopes the cookie to a specific host. Defaults to the current host.
	Domain string

	// MaxAge is the cookie lifetime in seconds.
	// 0 means session cookie (deleted when browser closes).
	// Negative means delete the cookie immediately.
	MaxAge int

	// SameSite controls the cross-site request behaviour.
	// Defaults to [http.SameSiteStrictMode] when zero.
	SameSite http.SameSite

	// Insecure, when true, omits the Secure attribute.
	// Use only for non-TLS environments such as localhost development.
	Insecure bool

	// AllowJS, when true, omits the HttpOnly attribute.
	AllowJS bool

	// Codec, when non-nil, validates value before the Set-Cookie header is written.
	// Set via [CookieOptions.WithCodec] to avoid address-of boilerplate.
	Codec *codex.Codec[string]
}

// WithCodec sets the validation codec and returns the updated CookieOptions.
// Avoids the temporary-variable + address-of pattern required when setting Codec inline:
//
//	err := chiadapter.SetCookie(w, "session_token", token,
//	    chiadapter.CookieOptions{MaxAge: 3600}.WithCodec(sessionCodec))
func (o CookieOptions) WithCodec(c codex.Codec[string]) CookieOptions {
	o.Codec = &c
	return o
}

// SetCookie writes a Set-Cookie header on w with secure defaults:
// Secure, HttpOnly, SameSite=Strict, Path="/".
//
// If [CookieOptions.Codec] is non-nil, value is validated first.
// A validation failure returns [rest.CookieParamError] without writing any header.
func SetCookie(w http.ResponseWriter, name, value string, opts CookieOptions) error {
	if opts.Codec != nil {
		if err := opts.Codec.Validate(value); err != nil {
			return rest.CookieParamError{Name: name, Value: value, Err: err}
		}
	}
	path := opts.Path
	if path == "" {
		path = "/"
	}
	sameSite := opts.SameSite
	if sameSite == 0 {
		sameSite = http.SameSiteStrictMode
	}
	// #nosec G124 -- Secure/HttpOnly/SameSite enforced by default; Insecure/AllowJS are intentional opt-outs.
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     path,
		Domain:   opts.Domain,
		MaxAge:   opts.MaxAge,
		Secure:   !opts.Insecure,
		HttpOnly: !opts.AllowJS,
		SameSite: sameSite,
	})
	return nil
}

// PendingCookie is a cookie queued to be validated and written as a Set-Cookie
// response header by [Handler].
type PendingCookie struct {
	Name  string
	Value string
	Opts  CookieOptions
}

// HandlerFunc is the typed application handler called by [Handler].
// ctx is the request context. req is the decoded request value; for body-less
// methods it is the zero value of Req.
// Use [RequestFromContext] to access the underlying *http.Request for path
// parameters and headers. Chi path params are available via chi.URLParam(r, "name").
type HandlerFunc[Req, Resp any] func(ctx context.Context, req Req) (Resp, error)

// RequestFromContext retrieves the *http.Request stored in ctx by [Handler].
// Returns false if the context was not created by this package.
func RequestFromContext(ctx context.Context) (*http.Request, bool) {
	r, ok := ctx.Value(contextKey{}).(*http.Request)
	return r, ok
}

// WithResponseHeaders copies the key-value pairs from h into the response
// header map stored in ctx (pre-allocated by [Handler] before calling the
// [HandlerFunc]). Call this inside a [HandlerFunc] to emit response headers
// such as Location, ETag, or custom headers without direct access to
// [http.ResponseWriter].
func WithResponseHeaders(ctx context.Context, h http.Header) {
	if existing, ok := ctx.Value(responseHeadersKey{}).(http.Header); ok {
		for k, vs := range h {
			existing[k] = vs
		}
	}
}

// ResponseHeadersFromContext retrieves response headers previously stored by
// [WithResponseHeaders]. Returns false if no headers were set.
func ResponseHeadersFromContext(ctx context.Context) (http.Header, bool) {
	h, ok := ctx.Value(responseHeadersKey{}).(http.Header)
	return h, ok
}

// WithResponseCookies deposits one or more [PendingCookie] values into ctx.
// [Handler] validates their values against the route's [ResponseCookieParam]
// codecs and writes Set-Cookie headers on success.
func WithResponseCookies(ctx context.Context, cookies ...PendingCookie) {
	if pending, ok := ctx.Value(responseCookiesKey{}).(*[]PendingCookie); ok {
		*pending = append(*pending, cookies...)
	}
}

// ResponseCookiesFromContext retrieves pending cookies previously stored by
// [WithResponseCookies].
func ResponseCookiesFromContext(ctx context.Context) ([]PendingCookie, bool) {
	pending, ok := ctx.Value(responseCookiesKey{}).(*[]PendingCookie)
	if !ok || pending == nil {
		return nil, false
	}
	return *pending, true
}

// Options configures the behaviour of [Handler] and [Register].
//
// BREAKING: Observer and SecurityFunc are REMOVED — replaced by
// [middleware.Middleware] attached via [rest.WithMiddleware] (declaration
// time, spec-relevant) and/or [Handler]/[Register]'s variadic mws parameter
// (call time). Use [nethttp.RequireScopes] for the security replacement
// (chi has no scheme-specific RequireScopes of its own — it reuses
// nethttp's directly, identical *http.Request Raw type) and
// [nethttp.ObservabilityMiddleware] for the observer replacement (same
// general-purpose func(http.Handler) http.Handler shape this package's
// Handler/Register already recognize).
type Options struct {
	// ErrorHandler, when non-nil, is called instead of the default JSON error
	// envelope when a request fails. status is the suggested HTTP status code.
	// Implementations must write the response header and body.
	ErrorHandler func(w http.ResponseWriter, r *http.Request, status int, err error)

	// MaxBodyBytes limits the number of bytes read from the request body.
	// Zero means the default (1 MiB).
	MaxBodyBytes int64

	// ContentType is the expected Content-Type for body-bearing methods.
	// Defaults to "application/json".
	ContentType string

	// MultiValueQueryParams, when true, uses [rest.RouteHandle.ValidateQueryMulti].
	MultiValueQueryParams bool
}

// validateMiddlewareShapes checks every attached mw.Fn against the two
// concrete shapes this package recognizes, EAGERLY at Register/Handler
// construction time rather than deferring to the first incoming request —
// a malformed Fn fails loudly and immediately.
func validateMiddlewareShapes[Req any](mws []middleware.Middleware) error {
	for _, mw := range mws {
		switch mw.Fn.(type) {
		case nil:
			continue // spec-only/no-op middleware (e.g. RequestParams-only) — allowed
		case func(http.Handler) http.Handler:
			continue
		case func(context.Context, *http.Request, *Req) (map[string][]string, error):
			continue
		default:
			return middleware.MiddlewareShapeError{
				Name:     mw.Name,
				Expected: "func(http.Handler) http.Handler or func(context.Context, *http.Request, *Req) (map[string][]string, error)",
				Got:      fmt.Sprintf("%T", mw.Fn),
			}
		}
	}
	return nil
}

// applyGeneralMiddleware wraps h with every general-purpose Fn found in mws,
// OUTERMOST-in, in attachment order (mws[0] is outermost — the first
// attached middleware runs first and returns last).
func applyGeneralMiddleware(h http.Handler, mws []middleware.Middleware) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		fn, ok := mws[i].Fn.(func(http.Handler) http.Handler)
		if !ok {
			continue
		}
		h = fn(h)
	}
	return h
}

// runSecurityMiddleware runs every attached security-specific Fn IN
// ATTACHMENT ORDER (fail-fast on the FIRST one whose OWN credential
// extraction errors), merges their returned grants into ONE map, then
// performs a SINGLE [middleware.CheckScopes] call — see "L4" in
// docs/roadmap/declarative-middleware.md for why each Fn does NOT
// independently decide pass/fail against the route's full requirement set.
//
// A middleware with an EMPTY Satisfies (a pure presence/format check, e.g.
// [nethttp.RequireAPIKey], contributing no scope grants) ALWAYS runs,
// regardless of whether the route declares any Security — that is its
// whole design point (see docs/roadmap/declarative-middleware.md's
// "Header/cookie param auto-contribution" section). A middleware with a
// NON-EMPTY Satisfies (e.g. [nethttp.RequireScopes]) only runs when the
// route actually declares a security requirement — an unsecured route must
// not authenticate credentials it never asked for.
func runSecurityMiddleware[Req any](ctx context.Context, r *http.Request, req *Req, mws []middleware.Middleware, secReqs []route.SecurityRequirement) error {
	granted := make(map[string][]string)
	for _, mw := range mws {
		fn, ok := mw.Fn.(func(context.Context, *http.Request, *Req) (map[string][]string, error))
		if !ok {
			continue
		}
		if len(mw.Satisfies) > 0 && len(secReqs) == 0 {
			continue
		}
		g, err := fn(ctx, r, req)
		if err != nil {
			return err
		}
		for k, v := range g {
			granted[k] = v
		}
	}
	return middleware.CheckScopes(secReqs, granted)
}

// diagnosticObserver adapts ctx-based [stats.RecordDiagnostic] to the
// [stats.ValidationObserver] interface, so [stats.ReportErrors]'s existing
// per-field error-walking logic can be reused UNCHANGED.
type diagnosticObserver struct{ ctx context.Context }

func (d diagnosticObserver) RecordValidationError(location, constraintName, field string) {
	stats.RecordDiagnostic(d.ctx, stats.Diagnostic{Location: location, ConstraintName: constraintName, Field: field})
}

// Handler wraps a [rest.RouteHandle] and a [HandlerFunc] into an [http.HandlerFunc]
// suitable for use with a chi.Router.
func Handler[Req, Resp any](handle *rest.RouteHandle[Req, Resp], fn HandlerFunc[Req, Resp], opts Options, mws ...middleware.Middleware) http.HandlerFunc {
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

	allMws := append(slices.Clone(handle.Middlewares), mws...)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &statusResponseWriter{ResponseWriter: w, code: http.StatusOK}

		ctx := middleware.EnsureContextFields(r.Context())
		ctx = context.WithValue(ctx, contextKey{}, r)
		respHeaders := make(http.Header)
		ctx = context.WithValue(ctx, responseHeadersKey{}, respHeaders)
		pendingCookies := make([]PendingCookie, 0)
		ctx = context.WithValue(ctx, responseCookiesKey{}, &pendingCookies)

		var req Req
		if handle.Descriptor.RequestBody != nil {
			ct, _, _ := strings.Cut(r.Header.Get("Content-Type"), ";")
			ct = strings.TrimSpace(ct)
			r.Body = http.MaxBytesReader(sw, r.Body, maxBody)
			body, err := io.ReadAll(r.Body)
			if err != nil {
				var mbe *http.MaxBytesError
				if errors.As(err, &mbe) {
					errFn(sw, r, http.StatusRequestEntityTooLarge, rest.BodyTooLargeError{Limit: maxBody})
				} else {
					errFn(sw, r, http.StatusBadRequest, err)
				}
				return
			}
			var decErr error
			if len(handle.RequestFormats) > 0 {
				// Multi-format: negotiate by Content-Type, 415 on no match.
				chosen, ok := negotiateRequestFormat(handle.RequestFormats, ct)
				if !ok {
					var supported []string
					for _, f := range handle.RequestFormats {
						supported = append(supported, f.ContentType())
					}
					errFn(sw, r, http.StatusUnsupportedMediaType,
						rest.UnsupportedMediaTypeError{Got: ct, Supported: supported})
					return
				}
				var v Req
				v, decErr = chosen.Unmarshal(body)
				req = v
			} else {
				// Single-format: enforce opts.ContentType (default application/json).
				if ct != expectedCT {
					errFn(sw, r, http.StatusUnsupportedMediaType,
						rest.UnsupportedMediaTypeError{Got: ct, Supported: []string{expectedCT}})
					return
				}
				req, decErr = handle.Decode(body)
			}
			if decErr != nil {
				reportBodyErrors(ctx, decErr)
				errFn(sw, r, http.StatusBadRequest, decErr)
				return
			}
		}

		if opts.MultiValueQueryParams {
			if err := handle.ValidateQueryMulti(r.URL.Query()); err != nil {
				reportQueryErrors(ctx, err)
				errFn(sw, r, http.StatusBadRequest, err)
				return
			}
		} else {
			if err := handle.ValidateQuery(queryValues(r)); err != nil {
				reportQueryErrors(ctx, err)
				errFn(sw, r, http.StatusBadRequest, err)
				return
			}
		}

		if err := handle.ValidateCookies(cookieValues(r)); err != nil {
			reportCookieErrors(ctx, err)
			errFn(sw, r, http.StatusBadRequest, err)
			return
		}

		if err := handle.ValidateHeaders(headerValues(r)); err != nil {
			reportHeaderErrors(ctx, err)
			errFn(sw, r, http.StatusBadRequest, err)
			return
		}

		// Validate path parameters against their registered codecs (if any).
		names := handle.PathParamNames()
		if len(names) > 0 {
			if err := handle.ValidatePathParams(pathValues(r, names)); err != nil {
				reportPathErrors(ctx, err)
				errFn(sw, r, http.StatusBadRequest, err)
				return
			}
		}

		// Merge path/query/header/cookie values declared via
		// rest.NewPathParam/NewRequiredQueryParam/etc. into req — additive,
		// only runs when the route has merge-capable params (backward
		// compatible: identical behavior to the block above when none are
		// declared). Values were already validated by the block above;
		// DecodeVars re-validates as a byproduct of decoding, which is
		// harmless (same codec, same value).
		if mergeFields := handle.MergeFields(); len(mergeFields) > 0 {
			vars := pathValues(r, names)
			for k, v := range queryValues(r) {
				vars[k] = v
			}
			for k, v := range headerValues(r) {
				vars[k] = v
			}
			for k, v := range cookieValues(r) {
				vars[k] = v
			}
			if err := codex.DecodeVars(&req, vars, mergeFields...); err != nil {
				reportBodyErrors(ctx, err)
				errFn(sw, r, http.StatusBadRequest, err)
				return
			}
		}

		// Enforce security: per-route requirements take precedence; nil falls back
		// to global security declared via Builder.AddGlobalSecurity.
		secReqs := handle.Descriptor.Security
		if secReqs == nil {
			secReqs = handle.GlobalSecurity
		}
		if len(secReqs) > 0 {
			if credErr := validateSecurityCredentials(r, secReqs, handle.SecuritySchemes); credErr != nil {
				if secObs, ok := stats.ObserverFromContext(ctx).(stats.SecurityObserver); ok {
					secObs.RecordSecurityRejection(handle.Descriptor.Path, firstScheme(secReqs))
				}
				errFn(sw, r, http.StatusUnauthorized, credErr)
				return
			}
		}
		// Run every attached security-specific Fn, merge grants, ONE
		// middleware.CheckScopes call — see "L4" in
		// docs/roadmap/declarative-middleware.md. Called even when secReqs
		// is empty: a middleware with an EMPTY Satisfies (a pure
		// presence/format check, e.g. nethttp.RequireAPIKey) must still
		// run — see runSecurityMiddleware's own doc comment.
		if err := runSecurityMiddleware(ctx, r, &req, allMws, secReqs); err != nil {
			secErr := rest.SecurityError{Err: err}
			errFn(sw, r, http.StatusUnauthorized, secErr)
			return
		}

		var (
			resp Resp
			err  error
		)

		resp, err = fn(ctx, req)
		if err != nil {
			if patternResp, matched, applyErr := handle.ErrorResponseFor(err); matched {
				if applyErr == nil {
					// ErrorRespond (default): write the typed body directly.
					// ErrorHandle/ErrorLog: skip the auto-write and fall
					// through to errFn below (Options.ErrorHandler), same
					// as an unmatched error, but still using this pattern's
					// declared status via ErrorStatusFor.
					if patternResp.Action == "" || patternResp.Action == rest.ErrorRespond {
						if writeErr := writeErrorPatternResponse(ctx, sw, handle, patternResp, respHeaders, pendingCookies); writeErr == nil {
							return
						} else {
							err = writeErr
						}
					}
				} else {
					err = applyErr
				}
			}
			status := http.StatusInternalServerError
			if mappedStatus, ok := handle.ErrorStatusFor(err); ok {
				status = mappedStatus
			}
			errFn(sw, r, status, err)
			return
		}

		// Encode response header/cookie values declared via
		// rest.NewRequiredResponseHeaderParam/etc. into the SAME
		// respHeaders/pendingCookies values WithResponseHeaders/
		// WithResponseCookies already write to — additive, only runs
		// when the route has response merge-capable params. The
		// existing ValidateResponseHeaders/ValidateResponseCookies +
		// write loop below picks these up unchanged, exactly like the
		// manual ResponseHeadersFromContext/WithResponseCookies escape
		// hatch.
		if headerFields := handle.ResponseHeaderMergeFields(); len(headerFields) > 0 {
			values, encErr := codex.EncodeVars(resp, headerFields...)
			if encErr != nil {
				reportResponseHeaderErrors(ctx, encErr)
				errFn(sw, r, http.StatusInternalServerError, encErr)
				return
			}
			for k, v := range values {
				respHeaders.Set(k, v)
			}
		}
		if cookieFields := handle.ResponseCookieMergeFields(); len(cookieFields) > 0 {
			values, encErr := codex.EncodeVars(resp, cookieFields...)
			if encErr != nil {
				reportResponseCookieErrors(ctx, encErr)
				errFn(sw, r, http.StatusInternalServerError, encErr)
				return
			}
			for k, v := range values {
				pendingCookies = append(pendingCookies, PendingCookie{Name: k, Value: v})
			}
		}

		var out []byte
		var respCT string
		if len(handle.Formats) > 0 {
			chosen, ok := negotiateFormat(handle.Formats, r.Header.Get("Accept"))
			if !ok {
				supported := make([]string, 0, len(handle.Formats))
				for _, f := range handle.Formats {
					if ct := f.ContentType(); ct != "" {
						supported = append(supported, ct)
					}
				}
				errFn(sw, r, http.StatusNotAcceptable,
					rest.NotAcceptableError{Accept: r.Header.Get("Accept"), Supported: supported})
				return
			}
			if chosen.IsStreamable() {
				if valErr := chosen.Validate(resp); valErr != nil {
					reportBodyErrors(ctx, valErr)
					errFn(sw, r, http.StatusInternalServerError, valErr)
					return
				}
				respCT = chosen.ContentType()

				if err := handle.ValidateResponseHeaders(responseHeaderValues(respHeaders)); err != nil {
					reportResponseHeaderErrors(ctx, err)
					errFn(sw, r, http.StatusInternalServerError, err)
					return
				}
				if err := handle.ValidateResponseCookies(responseCookieValues(pendingCookies)); err != nil {
					reportResponseCookieErrors(ctx, err)
					errFn(sw, r, http.StatusInternalServerError, err)
					return
				}
				for key, vals := range respHeaders {
					for _, v := range vals {
						sw.Header().Add(key, v)
					}
				}
				for i := range pendingCookies {
					pc := &pendingCookies[i]
					writeOpts := pc.Opts
					writeOpts.Codec = nil
					if err := SetCookie(sw, pc.Name, pc.Value, writeOpts); err != nil {
						errFn(sw, r, http.StatusInternalServerError, err)
						return
					}
				}
				sw.Header().Set("Content-Type", respCT)
				sw.WriteHeader(primaryStatus(handle))
				if streamErr := chosen.MarshalTo(resp, sw); streamErr != nil {
					reportBodyErrors(ctx, streamErr)
				}
				return
			}
			var encErr error
			out, encErr = chosen.Marshal(resp)
			if encErr != nil {
				reportBodyErrors(ctx, encErr)
				errFn(sw, r, http.StatusInternalServerError, encErr)
				return
			}
			respCT = chosen.ContentType()
		} else {
			var encErr error
			out, encErr = handle.Encode(resp)
			if encErr != nil {
				reportBodyErrors(ctx, encErr)
				errFn(sw, r, http.StatusInternalServerError, encErr)
				return
			}
			respCT = "application/json"
		}

		if err := handle.ValidateResponseHeaders(responseHeaderValues(respHeaders)); err != nil {
			reportResponseHeaderErrors(ctx, err)
			errFn(sw, r, http.StatusInternalServerError, err)
			return
		}

		if err := handle.ValidateResponseCookies(responseCookieValues(pendingCookies)); err != nil {
			reportResponseCookieErrors(ctx, err)
			errFn(sw, r, http.StatusInternalServerError, err)
			return
		}

		for key, vals := range respHeaders {
			for _, v := range vals {
				sw.Header().Add(key, v)
			}
		}
		for i := range pendingCookies {
			pc := &pendingCookies[i]
			writeOpts := pc.Opts
			writeOpts.Codec = nil
			if err := SetCookie(sw, pc.Name, pc.Value, writeOpts); err != nil {
				errFn(sw, r, http.StatusInternalServerError, err)
				return
			}
		}

		status := primaryStatus(handle)
		sw.Header().Set("Content-Type", respCT)
		sw.WriteHeader(status)
		_, _ = sw.Write(out) // #nosec G705 -- content is application-generated, not user-controlled
	})

	// General-purpose middlewares (e.g. nethttp.ObservabilityMiddleware) wrap
	// the WHOLE call, outermost-in, in attachment order — see "Two
	// attachment points" in docs/roadmap/declarative-middleware.md.
	// Malformed Fn shapes are silently skipped here (best-effort, since
	// Handler returns no error); use [Register] for eager, loud
	// [middleware.MiddlewareShapeError] validation at wiring time instead
	// of first-request time.
	return applyGeneralMiddleware(inner, allMws).ServeHTTP
}

// Register registers the route on r using its method and path from the route
// descriptor. Chi uses the same {param} placeholder syntax as go-codex path
// templates, so no translation is needed.
//
// mws combines with handle.Middlewares (declaration-time, attached via
// [rest.WithMiddleware]) — declaration-time middleware runs first, then
// call-time mws, in that combined order.
//
// Register validates every attached middleware's Fn shape EAGERLY, at
// wiring time — before any request arrives — returning
// [middleware.MiddlewareShapeError] immediately for a malformed Fn, instead
// of silently skipping it on every incoming request the way [Handler] does
// when called directly. This is why Register returns an error (BREAKING —
// was previously void).
func Register[Req, Resp any](r gochi.Router, handle *rest.RouteHandle[Req, Resp], fn HandlerFunc[Req, Resp], opts Options, mws ...middleware.Middleware) error {
	allMws := append(slices.Clone(handle.Middlewares), mws...)
	if err := validateMiddlewareShapes[Req](allMws); err != nil {
		return err
	}
	method := strings.ToUpper(handle.Descriptor.Method)
	r.Method(method, handle.Descriptor.Path, Handler(handle, fn, opts, mws...))
	return nil
}

// SSEHandlerFunc is the typed application handler called by [SSEHandler].
// ctx is the request context (cancelled when the client disconnects).
// req is the decoded request (zero value for body-less GET requests).
// send encodes, validates, and writes one SSE event; it returns an error if
// the event fails codec validation or if the underlying write fails.
type SSEHandlerFunc[Req, Event any] func(ctx context.Context, req Req, send func(Event) error) error

// SSEHandler wraps an [rest.SSERouteHandle] and a user-supplied [SSEHandlerFunc]
// into an [http.HandlerFunc] that streams Server-Sent Events.
//
// The handler sets Content-Type: text/event-stream, Cache-Control: no-cache,
// and Connection: keep-alive, then calls fn. The send func provided to fn
// validates the event via the codec, encodes it as JSON, writes
// "data: <json>\n\n" to the response, and flushes. If the event fails
// validation, send returns an error without writing anything.
//
// fn should honour ctx.Done() for clean client-disconnect handling.
func SSEHandler[Req, Event any](handle *rest.SSERouteHandle[Req, Event], fn SSEHandlerFunc[Req, Event], opts Options, mws ...middleware.Middleware) http.HandlerFunc {
	if opts.ErrorHandler == nil {
		opts.ErrorHandler = defaultErrorHandler
	}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &statusResponseWriter{ResponseWriter: w, code: http.StatusOK}

		var req Req
		ctx := middleware.EnsureContextFields(r.Context())
		ctx = context.WithValue(ctx, contextKey{}, r)
		responseHeaders := make(http.Header)
		ctx = context.WithValue(ctx, responseHeadersKey{}, responseHeaders)
		ctx = context.WithValue(ctx, responseCookiesKey{}, &[]PendingCookie{})

		// Validate query parameters against their registered codecs (if any).
		if opts.MultiValueQueryParams {
			if err := handle.ValidateQueryMulti(r.URL.Query()); err != nil {
				reportQueryErrors(ctx, err)
				opts.ErrorHandler(sw, r, http.StatusBadRequest, err)
				return
			}
		} else {
			if err := handle.ValidateQuery(queryValues(r)); err != nil {
				reportQueryErrors(ctx, err)
				opts.ErrorHandler(sw, r, http.StatusBadRequest, err)
				return
			}
		}

		// Validate cookie parameters against their registered codecs (if any).
		if err := handle.ValidateCookies(cookieValues(r)); err != nil {
			reportCookieErrors(ctx, err)
			opts.ErrorHandler(sw, r, http.StatusBadRequest, err)
			return
		}

		// Validate header parameters against their registered codecs (if any).
		if err := handle.ValidateHeaders(headerValues(r)); err != nil {
			reportHeaderErrors(ctx, err)
			opts.ErrorHandler(sw, r, http.StatusBadRequest, err)
			return
		}

		// Validate path parameters against their registered codecs (if any).
		if names := handle.PathParamNames(); len(names) > 0 {
			if err := handle.ValidatePathParams(pathValues(r, names)); err != nil {
				reportPathErrors(ctx, err)
				opts.ErrorHandler(sw, r, http.StatusBadRequest, err)
				return
			}
		}
		pathVars := pathValues(r, handle.PathParamNames())
		queryVars := queryValues(r)
		headerVars := headerValues(r)
		cookieVars := cookieValues(r)

		// Enforce security: per-route requirements take precedence; nil falls back
		// to global security declared via Builder.AddGlobalSecurity.
		secReqs := handle.Descriptor.Security
		if secReqs == nil {
			secReqs = handle.GlobalSecurity
		}
		if len(secReqs) > 0 {
			if credErr := validateSecurityCredentials(r, secReqs, handle.SecuritySchemes); credErr != nil {
				if secObs, ok := stats.ObserverFromContext(ctx).(stats.SecurityObserver); ok {
					secObs.RecordSecurityRejection(handle.Descriptor.Path, firstScheme(secReqs))
				}
				opts.ErrorHandler(sw, r, http.StatusUnauthorized, credErr)
				return
			}
		}
		// Called even when secReqs is empty — see runSecurityMiddleware's
		// own doc comment (a middleware with an EMPTY Satisfies must still
		// run).
		if err := runSecurityMiddleware(ctx, r, &req, mws, secReqs); err != nil {
			secErr := rest.SecurityError{Err: err}
			opts.ErrorHandler(sw, r, http.StatusUnauthorized, secErr)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")

		flusher, canFlush := w.(http.Flusher)

		// pick event encoder: first EventFormat or fallback to EncodeEvent (JSON).
		// When Formats is non-empty, negotiate using the Accept header; return 406
		// if the client requests a specific format not offered. Accept: text/event-stream
		// or */* falls back to the first registered format.
		encode := handle.EncodeEvent
		if len(handle.Formats) > 0 {
			accept := r.Header.Get("Accept")
			f, ok := negotiateFormat(handle.Formats, accept)
			if !ok {
				// text/event-stream is the stream transport type, not a data format —
				// treat it as "use the default".
				if strings.Contains(accept, "text/event-stream") {
					f = handle.Formats[0]
				} else {
					var supported []string
					for _, fmt := range handle.Formats {
						supported = append(supported, fmt.ContentType())
					}
					opts.ErrorHandler(sw, r, http.StatusNotAcceptable,
						rest.NotAcceptableError{Accept: accept, Supported: supported})
					return
				}
			}
			encode = func(e Event) ([]byte, error) { return f.Marshal(e) }
		}

		validate := handle.ValidateEvent

		headersCommitted := false
		send := func(e Event) error {
			if len(handle.MergeFields()) > 0 {
				var err error
				e, err = handle.MergeEvent(e, pathVars, queryVars, headerVars, cookieVars)
				if err != nil {
					stats.ReportErrors(diagnosticObserver{ctx}, "response", err)
					return err
				}
			}
			if !headersCommitted {
				headersCommitted = true
				// Commit staged response headers/cookies on first send, before
				// any data is written (headers are not yet sent to the client).
				if err := handle.ValidateResponseHeaders(responseHeaderValues(responseHeaders)); err != nil {
					reportResponseHeaderErrors(ctx, err)
					return err
				}
				if pending, ok := ctx.Value(responseCookiesKey{}).(*[]PendingCookie); ok {
					if err := handle.ValidateResponseCookies(responseCookieValues(*pending)); err != nil {
						reportResponseCookieErrors(ctx, err)
						return err
					}
					for i := range *pending {
						pc := &(*pending)[i]
						writeOpts := pc.Opts
						writeOpts.Codec = nil
						if err := SetCookie(sw, pc.Name, pc.Value, writeOpts); err != nil {
							return err
						}
					}
				}
				for key, vals := range responseHeaders {
					for _, v := range vals {
						sw.Header().Add(key, v)
					}
				}
			}
			if err := validate(e); err != nil {
				stats.RecordDiagnostic(ctx, stats.Diagnostic{Location: "response", ConstraintName: stats.ConstraintName(err), Field: "event"})
				return err
			}
			data, err := encode(e)
			if err != nil {
				return err
			}
			if _, werr := fmt.Fprintf(sw, "data: %s\n\n", data); werr != nil {
				return werr
			}
			if canFlush {
				flusher.Flush()
			}
			return nil
		}

		if err := fn(ctx, req, send); err != nil {
			if sw.code == http.StatusOK {
				opts.ErrorHandler(sw, r, http.StatusInternalServerError, err)
			}
		}
	})

	// General-purpose middlewares wrap the WHOLE call, outermost-in, in
	// attachment order — see [Handler]'s equivalent wrapping.
	return applyGeneralMiddleware(inner, mws).ServeHTTP
}

// RegisterSSE wires an [rest.SSERouteHandle] onto a chi router as a GET SSE endpoint.
//
// mws is call-time-only (SSERouteHandle has no declaration-time Middlewares
// field). Validates every attached middleware's Fn shape EAGERLY, returning
// [middleware.MiddlewareShapeError] immediately for a malformed Fn
// (BREAKING — RegisterSSE was previously void).
func RegisterSSE[Req, Event any](r gochi.Router, handle *rest.SSERouteHandle[Req, Event], fn SSEHandlerFunc[Req, Event], opts Options, mws ...middleware.Middleware) error {
	if err := validateMiddlewareShapes[Req](mws); err != nil {
		return err
	}
	r.Get(handle.Descriptor.Path, SSEHandler(handle, fn, opts, mws...))
	return nil
}

func primaryStatus[Req, Resp any](handle *rest.RouteHandle[Req, Resp]) int {
	if len(handle.Descriptor.Responses) == 0 {
		return http.StatusOK
	}
	code, err := strconv.Atoi(handle.Descriptor.Responses[0].Status)
	if err != nil {
		return http.StatusOK
	}
	return code
}

type statusResponseWriter struct {
	http.ResponseWriter
	code int
}

func (rw *statusResponseWriter) WriteHeader(code int) {
	rw.code = code
	rw.ResponseWriter.WriteHeader(code)
}

type errorBody struct {
	Error string `json:"error"`
}

func defaultErrorHandler(w http.ResponseWriter, _ *http.Request, status int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	body, _ := json.Marshal(errorBody{Error: err.Error()})
	_, _ = w.Write(body)
}

// pathValues extracts the path variable values named in names from r using
// chi.URLParam.
func pathValues(r *http.Request, names []string) map[string]string {
	m := make(map[string]string, len(names))
	for _, name := range names {
		m[name] = gochi.URLParam(r, name)
	}
	return m
}

func queryValues(r *http.Request) map[string]string {
	q := r.URL.Query()
	m := make(map[string]string, len(q))
	for k, vs := range q {
		if len(vs) > 0 {
			m[k] = vs[0]
		}
	}
	return m
}

func cookieValues(r *http.Request) map[string]string {
	cookies := r.Cookies()
	m := make(map[string]string, len(cookies))
	for _, c := range cookies {
		if _, exists := m[c.Name]; !exists {
			m[c.Name] = c.Value
		}
	}
	return m
}

func headerValues(r *http.Request) map[string]string {
	m := make(map[string]string, len(r.Header))
	for k, vs := range r.Header {
		if len(vs) > 0 {
			m[k] = vs[0]
		}
	}
	return m
}

func responseHeaderValues(h http.Header) map[string]string {
	m := make(map[string]string, len(h))
	for k, vs := range h {
		if len(vs) > 0 {
			m[k] = vs[0]
		}
	}
	return m
}

func responseCookieValues(cookies []PendingCookie) map[string]string {
	m := make(map[string]string, len(cookies))
	for _, pc := range cookies {
		m[pc.Name] = pc.Value
	}
	return m
}

func writeErrorPatternResponse[Req, Resp any](
	ctx context.Context,
	w http.ResponseWriter,
	handle *rest.RouteHandle[Req, Resp],
	pattern rest.ErrorPatternResponse,
	respHeaders http.Header,
	pendingCookies []PendingCookie,
) error {
	if respVal, ok := pattern.Value.(Resp); ok {
		if headerFields := handle.ResponseHeaderMergeFields(); len(headerFields) > 0 {
			values, encErr := codex.EncodeVars(respVal, headerFields...)
			if encErr != nil {
				reportResponseHeaderErrors(ctx, encErr)
				return encErr
			}
			for k, v := range values {
				respHeaders.Set(k, v)
			}
		}
		if cookieFields := handle.ResponseCookieMergeFields(); len(cookieFields) > 0 {
			values, encErr := codex.EncodeVars(respVal, cookieFields...)
			if encErr != nil {
				reportResponseCookieErrors(ctx, encErr)
				return encErr
			}
			for k, v := range values {
				pendingCookies = append(pendingCookies, PendingCookie{Name: k, Value: v})
			}
		}
	}

	if err := handle.ValidateResponseHeaders(responseHeaderValues(respHeaders)); err != nil {
		reportResponseHeaderErrors(ctx, err)
		return err
	}
	if err := handle.ValidateResponseCookies(responseCookieValues(pendingCookies)); err != nil {
		reportResponseCookieErrors(ctx, err)
		return err
	}

	for key, vals := range respHeaders {
		for _, v := range vals {
			w.Header().Add(key, v)
		}
	}
	for i := range pendingCookies {
		pc := &pendingCookies[i]
		writeOpts := pc.Opts
		writeOpts.Codec = nil
		if err := SetCookie(w, pc.Name, pc.Value, writeOpts); err != nil {
			return err
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(pattern.Status)
	_, err := w.Write(pattern.Body)
	return err
}

func reportBodyErrors(ctx context.Context, err error) {
	stats.ReportErrors(diagnosticObserver{ctx}, "body", err)
}

func reportQueryErrors(ctx context.Context, err error) {
	var qe rest.QueryParamError
	if !errors.As(err, &qe) {
		return
	}
	stats.RecordDiagnostic(ctx, stats.Diagnostic{Location: "query", ConstraintName: stats.ConstraintName(qe.Err), Field: qe.Name})
}

func reportCookieErrors(ctx context.Context, err error) {
	var ce rest.CookieParamError
	if !errors.As(err, &ce) {
		return
	}
	stats.RecordDiagnostic(ctx, stats.Diagnostic{Location: "cookie", ConstraintName: stats.ConstraintName(ce.Err), Field: ce.Name})
}

func reportHeaderErrors(ctx context.Context, err error) {
	var he rest.HeaderParamError
	if !errors.As(err, &he) {
		return
	}
	stats.RecordDiagnostic(ctx, stats.Diagnostic{Location: "header", ConstraintName: stats.ConstraintName(he.Err), Field: he.Name})
}

func reportResponseHeaderErrors(ctx context.Context, err error) {
	var rhe rest.ResponseHeaderParamError
	if !errors.As(err, &rhe) {
		return
	}
	stats.RecordDiagnostic(ctx, stats.Diagnostic{Location: "response_header", ConstraintName: stats.ConstraintName(rhe.Err), Field: rhe.Name})
}

func reportResponseCookieErrors(ctx context.Context, err error) {
	var rce rest.ResponseCookieParamError
	if !errors.As(err, &rce) {
		return
	}
	stats.RecordDiagnostic(ctx, stats.Diagnostic{Location: "response_cookie", ConstraintName: stats.ConstraintName(rce.Err), Field: rce.Name})
}

func reportPathErrors(ctx context.Context, err error) {
	var pe rest.PathParamError
	if !errors.As(err, &pe) {
		return
	}
	stats.RecordDiagnostic(ctx, stats.Diagnostic{Location: "path", ConstraintName: stats.ConstraintName(pe.Err), Field: pe.Name})
}

func negotiateFormat[T any](formats []format.Format[T], accept string) (format.Format[T], bool) {
	if len(formats) == 0 {
		return format.Format[T]{}, false
	}
	if accept == "" || accept == "*/*" {
		return formats[0], true
	}
	for _, entry := range strings.Split(accept, ",") {
		mediaType, _, _ := strings.Cut(strings.TrimSpace(entry), ";")
		mediaType = strings.TrimSpace(mediaType)
		if mediaType == "*/*" {
			return formats[0], true
		}
		for _, f := range formats {
			fmtMediaType, _, _ := strings.Cut(f.ContentType(), ";")
			if strings.TrimSpace(fmtMediaType) == mediaType {
				return f, true
			}
		}
	}
	return format.Format[T]{}, false
}

// negotiateRequestFormat picks the format whose ContentType matches the given
// Content-Type header value (exact match after stripping parameters).
func negotiateRequestFormat[T any](formats []format.Format[T], contentType string) (format.Format[T], bool) {
	for _, f := range formats {
		fmtMediaType, _, _ := strings.Cut(f.ContentType(), ";")
		if strings.TrimSpace(fmtMediaType) == contentType {
			return f, true
		}
	}
	return format.Format[T]{}, false
}

// validateSecurityCredentials extracts credentials from the request and validates
// them against the registered SecurityScheme codecs for the declared requirements.
func validateSecurityCredentials(r *http.Request, reqs []route.SecurityRequirement, schemes map[string]rest.SecurityScheme) error {
	for _, req := range reqs {
		for name := range req {
			s, ok := schemes[name]
			if !ok || s.Codec == nil {
				continue
			}
			cred := extractCredential(r, s)
			if err := s.Codec.Validate(cred); err != nil {
				return rest.SecurityCredentialError{Scheme: name, Err: err}
			}
		}
	}
	return nil
}

// extractCredential returns the raw credential string from the request based
// on the scheme type and location.
func extractCredential(r *http.Request, s rest.SecurityScheme) string {
	switch s.Type {
	case route.SecuritySchemeHTTP:
		auth := r.Header.Get("Authorization")
		switch strings.ToLower(s.Scheme) {
		case "bearer":
			// RFC 7235 §2.1: scheme names are case-insensitive.
			if len(auth) >= 7 && strings.EqualFold(auth[:7], "Bearer ") {
				return auth[7:]
			}
			return auth
		case "basic":
			if len(auth) >= 6 && strings.EqualFold(auth[:6], "Basic ") {
				return auth[6:]
			}
			return auth
		}
		return auth
	case route.SecuritySchemeOAuth2, route.SecuritySchemeOpenIDConnect:
		auth := r.Header.Get("Authorization")
		if len(auth) >= 7 && strings.EqualFold(auth[:7], "Bearer ") {
			return auth[7:]
		}
		return auth
	case route.SecuritySchemeAPIKey:
		switch strings.ToLower(s.In) {
		case "header":
			return r.Header.Get(s.Name)
		case "query":
			return r.URL.Query().Get(s.Name)
		case "cookie":
			if c, err := r.Cookie(s.Name); err == nil {
				return c.Value
			}
		}
	}
	return ""
}

// firstScheme returns the first scheme name from the security requirements.
func firstScheme(reqs []route.SecurityRequirement) string {
	for _, req := range reqs {
		for name := range req {
			return name
		}
	}
	return ""
}
