package chi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"

	gochi "github.com/go-chi/chi/v5"

	"github.com/DaniDeer/go-codex/adapters/internal/httpsecurity"
	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/internal/middleware"
	"github.com/DaniDeer/go-codex/internal/route"
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
// response header by the request pipeline.
//
// Type alias (docs/design/d-0006-protocol-native-capabilities.md's
// Phase 7 — promoted to api/rest, since nethttp/chi's own copies were
// byte-for-byte structurally identical): [rest.PendingCookie]'s
// `Attrs CookieAttributes` field replaces this former `Opts
// CookieOptions` field — see [cookieOptionsFrom] for the write-time
// conversion back to this package's own [CookieOptions].
type PendingCookie = rest.PendingCookie

// cookieOptionsFrom translates a codec-declared [rest.CookieAttributes]
// (the transport-agnostic api/rest declaration, populated via
// [rest.MergedResponseCookieParam.WithAttributes]) into this adapter's own
// [CookieOptions] — the zero value of rest.CookieAttributes translates to
// the zero value of CookieOptions, so a merge-derived cookie with no
// declared attributes behaves EXACTLY as it did before this mechanism
// existed.
func cookieOptionsFrom(a rest.CookieAttributes) CookieOptions {
	return CookieOptions{
		Path:     a.Path,
		Domain:   a.Domain,
		MaxAge:   a.MaxAge,
		SameSite: sameSiteFrom(a.SameSite),
		Insecure: a.Insecure,
		AllowJS:  a.AllowJS,
	}
}

// sameSiteFrom translates [rest.CookieSameSite] to [http.SameSite].
// rest.SameSiteDefault maps to the zero http.SameSite value, which
// [SetCookie] itself already defaults to http.SameSiteStrictMode.
func sameSiteFrom(s rest.CookieSameSite) http.SameSite {
	switch s {
	case rest.SameSiteLax:
		return http.SameSiteLaxMode
	case rest.SameSiteStrict:
		return http.SameSiteStrictMode
	case rest.SameSiteNone:
		return http.SameSiteNoneMode
	default:
		return 0
	}
}

// HandlerFunc is the typed application handler called by [serve]/[serveOne]'s dispatched request pipeline.
// ctx is the request context. req is the decoded request value; for body-less
// methods it is the zero value of Req.
// Use [RequestFromContext] to access the underlying *http.Request for path
// parameters and headers. Chi path params are available via chi.URLParam(r, "name").
type HandlerFunc[Req, Resp any] func(ctx context.Context, req Req) (Resp, error)

// RequestFromContext retrieves the *http.Request stored in ctx by the request pipeline (see [serve]/[serveOne]).
// Returns false if the context was not created by this package.
func RequestFromContext(ctx context.Context) (*http.Request, bool) {
	r, ok := ctx.Value(contextKey{}).(*http.Request)
	return r, ok
}

// WithResponseHeaders copies the key-value pairs from h into the response
// header map stored in ctx (pre-allocated by the request pipeline before calling the
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
// The request pipeline validates their values against the route's [ResponseCookieParam]
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

// Options configures the behaviour of [serve]/[serveOne]/[serveSSE].
//
// BREAKING: Observer and SecurityFunc are REMOVED — replaced by
// [middleware.Middleware] attached via [rest.Route.Use] (declaration
// time, spec-relevant) and/or [rest.Route.HandleMW] (register-time).
// Use [nethttp.Observability] for the observer replacement (same
// general-purpose func(http.Handler) http.Handler shape this package
// recognizes).
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

// applyGeneralMiddleware wraps h with every general-purpose Fn found in
// impls, OUTERMOST-in, in attachment order (impls[0] is outermost — the
// first attached implementation runs first and returns last).
func applyGeneralMiddleware(h http.Handler, impls []rest.ServerImplementation) http.Handler {
	for i := len(impls) - 1; i >= 0; i-- {
		fn, ok := impls[i].Fn.(func(http.Handler) http.Handler)
		if !ok {
			continue
		}
		h = fn(h)
	}
	return h
}

// handlerFunc wraps a [rest.RouteHandle] and a [HandlerFunc] into an
// [http.HandlerFunc] suitable for use with a chi.Router — the shared
// implementation behind [serve]/[serveOne]'s reflect dispatch (via
// [buildRouteHandler]) and [HandlerLatest]/[PipelineHandler] (in
// stream.go), which call it directly since Req/Resp are concrete at those
// call sites.
func handlerFunc[Req, Resp any](handle *rest.RouteHandle[Req, Resp], fn HandlerFunc[Req, Resp], opts Options, impls ...rest.ServerImplementation) http.HandlerFunc {
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

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &statusResponseWriter{ResponseWriter: w, code: http.StatusOK}

		ctx := middleware.EnsureContextFields(r.Context())
		ctx = context.WithValue(ctx, contextKey{}, r)
		respHeaders := make(http.Header)
		ctx = context.WithValue(ctx, responseHeadersKey{}, respHeaders)
		pendingCookies := make([]PendingCookie, 0)
		ctx = context.WithValue(ctx, responseCookiesKey{}, &pendingCookies)
		// Session-review finding (H1): resolved once here so every
		// Category-A call site below can consult the SAME declared
		// rest.ErrorPattern via ObserveErrorResponseFor — mirrors
		// serve.go's own resolution exactly, closing a previously-total
		// gap in this SEPARATE (port/stream-adapter-facing) dispatch
		// function.
		obs := stats.ObserverFromContext(ctx)

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
				rest.ReportBodyErrors(ctx, decErr)
				if tryRespondErrorPatternGeneric(ctx, sw, handle, obs, respHeaders, &pendingCookies, &decErr) {
					return
				}
				errFn(sw, r, http.StatusBadRequest, decErr)
				return
			}
		}

		// carrier is constructed ONCE and its extracted maps reused for
		// BOTH the validation calls below AND the merge-field building
		// further down (docs/design/d-0006-protocol-native-capabilities.md's
		// Phase 6 "Decision A" — eliminates the former double extraction).
		carrier := httpCarrier{r}
		queryVars := carrier.ExtractQuery()
		headerVars := rest.NormalizeHeaderVars(carrier.ExtractHeaders(), handle.HeaderParamNames())
		cookieVars := carrier.ExtractCookies()

		if opts.MultiValueQueryParams {
			if err := handle.ValidateQueryMulti(carrier.ExtractQueryMulti()); err != nil {
				rest.ReportQueryErrors(ctx, err)
				if tryRespondErrorPatternGeneric(ctx, sw, handle, obs, respHeaders, &pendingCookies, &err) {
					return
				}
				errFn(sw, r, http.StatusBadRequest, err)
				return
			}
		} else {
			if err := handle.ValidateQuery(queryVars); err != nil {
				rest.ReportQueryErrors(ctx, err)
				if tryRespondErrorPatternGeneric(ctx, sw, handle, obs, respHeaders, &pendingCookies, &err) {
					return
				}
				errFn(sw, r, http.StatusBadRequest, err)
				return
			}
		}

		if err := handle.ValidateCookies(cookieVars); err != nil {
			rest.ReportCookieErrors(ctx, err)
			if tryRespondErrorPatternGeneric(ctx, sw, handle, obs, respHeaders, &pendingCookies, &err) {
				return
			}
			errFn(sw, r, http.StatusBadRequest, err)
			return
		}

		if err := handle.ValidateHeaders(headerVars); err != nil {
			rest.ReportHeaderErrors(ctx, err)
			if tryRespondErrorPatternGeneric(ctx, sw, handle, obs, respHeaders, &pendingCookies, &err) {
				return
			}
			errFn(sw, r, http.StatusBadRequest, err)
			return
		}

		// Validate path parameters against their registered codecs (if any).
		names := handle.PathParamNames()
		if len(names) > 0 {
			if err := handle.ValidatePathParams(pathValues(r, names)); err != nil {
				rest.ReportPathErrors(ctx, err)
				if tryRespondErrorPatternGeneric(ctx, sw, handle, obs, respHeaders, &pendingCookies, &err) {
					return
				}
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
		// harmless (same codec, same value). Reuses queryVars/headerVars/
		// cookieVars captured above instead of re-extracting.
		if mergeFields := handle.MergeFields(); len(mergeFields) > 0 {
			vars := pathValues(r, names)
			for k, v := range queryVars {
				vars[k] = v
			}
			for k, v := range headerVars {
				vars[k] = v
			}
			for k, v := range cookieVars {
				vars[k] = v
			}
			if err := codex.DecodeVars(&req, vars, mergeFields...); err != nil {
				rest.ReportBodyErrors(ctx, err)
				if tryRespondErrorPatternGeneric(ctx, sw, handle, obs, respHeaders, &pendingCookies, &err) {
					return
				}
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
			if credErr := rest.ValidateSecurityCredentials(credentialExtractorFor(r), secReqs, handle.SecuritySchemes); credErr != nil {
				if secObs, ok := stats.ObserverFromContext(ctx).(stats.SecurityObserver); ok {
					secObs.RecordSecurityRejection(handle.Descriptor.Path, route.FirstSchemeName(secReqs))
				}
				errFn(sw, r, http.StatusUnauthorized, credErr)
				return
			}
		}
		// Gap-1 review fix (docs/design/d-0007-declarative-middleware-layering.md's
		// Rollout Phase A review): this ports-facing handlerFunc previously
		// never dispatched handle.MiddlewareHandlers at all (neither the
		// pre-Phase-A agnostic .Use(mw)-attached codec-backed middleware NOR
		// Phase A's new bound-HandleMW style) — only serve.go's Attach path
		// did. Mirrors serve.go's EXACT unified dispatch sequence now:
		// legacy security Fns' grants (CollectGrantsReflect, no CheckScopes
		// yet) -> every codec-backed MiddlewareHandler (DispatchMiddlewareHandlers,
		// which ALSO runs any Security-shaped middleware's Fn) -> merge
		// GrantedScopes from both sources -> ONE rest.CheckScopes call.
		reqPtr := reflect.ValueOf(&req)
		granted, grantErr := httpsecurity.CollectGrantsReflect(ctx, r, reqPtr, impls, secReqs)
		if grantErr != nil {
			// Security middleware Fn error IS ErrorPattern-eligible now
			// (session-review finding H1, mirroring serve.go's own
			// Category-A fix) — previously bypassed ErrorResponseFor
			// entirely, always producing SecurityError.
			var secErr error = rest.SecurityError{Err: grantErr}
			if tryRespondErrorPatternGeneric(ctx, sw, handle, obs, respHeaders, &pendingCookies, &secErr) {
				return
			}
			errFn(sw, r, http.StatusUnauthorized, secErr)
			return
		}
		middlewareOuts, mwErr := rest.DispatchMiddlewareHandlers(ctx, reqPtr, handle.MiddlewareHandlers, headerVars, cookieVars, queryVars)
		if mwErr != nil {
			dispatchErr, _ := rest.AsMiddlewareDispatchError(mwErr)
			if !dispatchErr.IsFnError {
				// Middleware DecodeIn failure IS ErrorPattern-eligible
				// (Topic 1's Category A fix), already wrapped in
				// MiddlewareInputError by DispatchMiddlewareHandlers.
				if tryRespondErrorPatternGeneric(ctx, sw, handle, obs, respHeaders, &pendingCookies, &dispatchErr.Err) {
					return
				}
				errFn(sw, r, http.StatusBadRequest, dispatchErr.Err)
				return
			}
			// A FAILING handler whose OWN Satisfies is non-empty (i.e. a
			// Security-paired MiddlewareHandler) keeps Security's own,
			// DISTINCT fallback (401, rest.SecurityError) — mirrors
			// runSecurityMiddleware's prior, established behavior exactly,
			// even though both now dispatch through the SAME mechanism. An
			// ordinary (non-Security) Fn error keeps the generic fallback
			// (400, rest.MiddlewareError).
			fnErr := dispatchErr.Err
			if isSecuritySatisfyingHandler(handle.MiddlewareHandlers, dispatchErr.Name) {
				if tryRespondErrorPatternGeneric(ctx, sw, handle, obs, respHeaders, &pendingCookies, &fnErr) {
					return
				}
				errFn(sw, r, http.StatusUnauthorized, rest.SecurityError{Err: fnErr})
				return
			}
			if tryRespondErrorPatternGeneric(ctx, sw, handle, obs, respHeaders, &pendingCookies, &fnErr) {
				return
			}
			errFn(sw, r, http.StatusBadRequest, rest.MiddlewareError{Name: dispatchErr.Name, Err: fnErr})
			return
		}
		httpsecurity.MergeMiddlewareHandlerGrants(granted, satisfiesPerHandler(handle.MiddlewareHandlers), middlewareOuts)
		if err := rest.CheckScopes(secReqs, granted); err != nil {
			if tryRespondErrorPatternGeneric(ctx, sw, handle, obs, respHeaders, &pendingCookies, &err) {
				return
			}
			errFn(sw, r, http.StatusUnauthorized, rest.SecurityError{Err: err})
			return
		}

		var (
			resp Resp
			err  error
		)

		resp, err = fn(ctx, req)
		if err != nil {
			// docs/roadmap/rest-typed-redirects.md: a RedirectError
			// (constructed via rest.Redirect/rest.RedirectToSSE) is
			// recognized BEFORE ErrorPattern/ErrorStatus matching —
			// renders status + Location, no body — mirrors
			// adapters/nethttp's identical handlerFunc check.
			if redirErr, ok := asRedirectError(err); ok {
				sw.Header().Set("Location", redirErr.Location)
				sw.WriteHeader(redirErr.Status)
				return
			}
			// H1: upgraded from the bare ErrorResponseFor this branch
			// used before to tryRespondErrorPatternGeneric/
			// ObserveErrorResponseFor — now ALSO reports match/miss/
			// span-tag observability, mirroring serve.go's identical
			// handler-error site.
			if tryRespondErrorPatternGeneric(ctx, sw, handle, obs, respHeaders, &pendingCookies, &err) {
				return
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
			// Gap-1 review fix: was codex.EncodeVars (does NOT honor
			// omit-empty) — chi's own serve.go Attach path already calls
			// the shared RouteHandle.EncodeResponseMergeFields, which
			// correctly uses codex.EncodeMergeVars internally; this
			// hand-inlined copy had silently drifted out of sync with
			// nethttp's identical block, which already used
			// EncodeMergeVars. Matches nethttp's handlerFunc exactly now.
			values, encErr := codex.EncodeMergeVars(resp, headerFields...)
			if encErr != nil {
				rest.ReportResponseHeaderErrors(ctx, encErr)
				if tryRespondErrorPatternGeneric(ctx, sw, handle, obs, respHeaders, &pendingCookies, &encErr) {
					return
				}
				errFn(sw, r, http.StatusInternalServerError, encErr)
				return
			}
			for k, v := range values {
				respHeaders.Set(k, v)
			}
		}
		if cookieFields := handle.ResponseCookieMergeFields(); len(cookieFields) > 0 {
			// Gap-1 review fix — see the header block's identical note above.
			values, encErr := codex.EncodeMergeVars(resp, cookieFields...)
			if encErr != nil {
				rest.ReportResponseCookieErrors(ctx, encErr)
				if tryRespondErrorPatternGeneric(ctx, sw, handle, obs, respHeaders, &pendingCookies, &encErr) {
					return
				}
				errFn(sw, r, http.StatusInternalServerError, encErr)
				return
			}
			cookieAttrs := handle.EncodeResponseCookieAttributes(resp)
			for k, v := range values {
				pendingCookies = append(pendingCookies, PendingCookie{Name: k, Value: v, Attrs: cookieAttrs[k]})
			}
		}

		// Compose every middleware's OWN response header/cookie values
		// (derived from its Out, already produced pre-handler above) —
		// registration-order, last-applied-wins alongside the route's own
		// values just merged above. Gap-1 review fix — mirrors serve.go's
		// identical post-handler loop, previously missing here entirely.
		for i, h := range handle.MiddlewareHandlers {
			mwHeaders, mwCookies, encErr := h.EncodeOut(ctx, middlewareOuts[i])
			if encErr != nil {
				stats.ReportErrors(rest.DiagnosticObserver{Ctx: ctx}, "middleware:out", encErr)
				if tryRespondErrorPatternGeneric(ctx, sw, handle, obs, respHeaders, &pendingCookies, &encErr) {
					return
				}
				errFn(sw, r, http.StatusInternalServerError, encErr)
				return
			}
			for k, v := range mwHeaders {
				respHeaders.Set(k, v)
			}
			var mwCookieAttrs map[string]rest.CookieAttributes
			if h.EncodeOutCookieAttrs != nil {
				mwCookieAttrs, encErr = h.EncodeOutCookieAttrs(middlewareOuts[i])
				if encErr != nil {
					stats.ReportErrors(rest.DiagnosticObserver{Ctx: ctx}, "middleware:out", encErr)
					if tryRespondErrorPatternGeneric(ctx, sw, handle, obs, respHeaders, &pendingCookies, &encErr) {
						return
					}
					errFn(sw, r, http.StatusInternalServerError, encErr)
					return
				}
			}
			for k, v := range mwCookies {
				pendingCookies = append(pendingCookies, PendingCookie{Name: k, Value: v, Attrs: mwCookieAttrs[k]})
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
					rest.ReportBodyErrors(ctx, valErr)
					if tryRespondErrorPatternGeneric(ctx, sw, handle, obs, respHeaders, &pendingCookies, &valErr) {
						return
					}
					errFn(sw, r, http.StatusInternalServerError, valErr)
					return
				}
				respCT = chosen.ContentType()

				if err := handle.ValidateResponseHeaders(rest.NormalizeHeaderVars(responseHeaderValues(respHeaders), handle.ResponseHeaderParamNames())); err != nil {
					rest.ReportResponseHeaderErrors(ctx, err)
					errFn(sw, r, http.StatusInternalServerError, err)
					return
				}
				if err := handle.ValidateResponseCookies(responseCookieValues(pendingCookies)); err != nil {
					rest.ReportResponseCookieErrors(ctx, err)
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
					writeOpts := cookieOptionsFrom(pc.Attrs) // Codec is always nil here — cookieOptionsFrom never sets it
					if err := SetCookie(sw, pc.Name, pc.Value, writeOpts); err != nil {
						errFn(sw, r, http.StatusInternalServerError, err)
						return
					}
				}
				sw.Header().Set("Content-Type", respCT)
				sw.WriteHeader(primaryStatus(handle))
				if streamErr := chosen.MarshalTo(resp, sw); streamErr != nil {
					rest.ReportBodyErrors(ctx, streamErr)
				}
				return
			}
			var encErr error
			out, encErr = chosen.Marshal(resp)
			if encErr != nil {
				rest.ReportBodyErrors(ctx, encErr)
				if tryRespondErrorPatternGeneric(ctx, sw, handle, obs, respHeaders, &pendingCookies, &encErr) {
					return
				}
				errFn(sw, r, http.StatusInternalServerError, encErr)
				return
			}
			respCT = chosen.ContentType()
		} else {
			var encErr error
			out, encErr = handle.Encode(resp)
			if encErr != nil {
				rest.ReportBodyErrors(ctx, encErr)
				if tryRespondErrorPatternGeneric(ctx, sw, handle, obs, respHeaders, &pendingCookies, &encErr) {
					return
				}
				errFn(sw, r, http.StatusInternalServerError, encErr)
				return
			}
			respCT = "application/json"
		}

		if err := handle.ValidateResponseHeaders(rest.NormalizeHeaderVars(responseHeaderValues(respHeaders), handle.ResponseHeaderParamNames())); err != nil {
			rest.ReportResponseHeaderErrors(ctx, err)
			errFn(sw, r, http.StatusInternalServerError, err)
			return
		}

		if err := handle.ValidateResponseCookies(responseCookieValues(pendingCookies)); err != nil {
			rest.ReportResponseCookieErrors(ctx, err)
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
			writeOpts := cookieOptionsFrom(pc.Attrs) // Codec is always nil here — cookieOptionsFrom never sets it
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

	// General-purpose middlewares (e.g. nethttp.Observability) wrap
	// the WHOLE call, outermost-in, in attachment order.
	return applyGeneralMiddleware(inner, impls).ServeHTTP
}

// SSEHandlerFunc is the typed application handler called by SSE dispatch.
// ctx is the request context (cancelled when the client disconnects).
// req is the decoded request (zero value for body-less GET requests).
// send encodes, validates, and writes one SSE event; it returns an error if
// the event fails codec validation or if the underlying write fails.
type SSEHandlerFunc[Req, Event any] func(ctx context.Context, req Req, send func(Event) error) error

// sseHandlerFunc wraps a [rest.SSERouteHandle] and a user-supplied
// [SSEHandlerFunc] into an [http.HandlerFunc] that streams Server-Sent
// Events — the shared implementation behind [serveSSE]'s reflect dispatch
// and binding.go's SSEAdapter, which calls it directly since Req/Event are
// concrete at that call site.
//
// The handler sets Content-Type: text/event-stream, Cache-Control: no-cache,
// and Connection: keep-alive, then calls fn. The send func provided to fn
// validates the event via the codec, encodes it as JSON, writes
// "data: <json>\n\n" to the response, and flushes. If the event fails
// validation, send returns an error without writing anything.
//
// fn should honour ctx.Done() for clean client-disconnect handling.
func sseHandlerFunc[Req, Event any](handle *rest.SSERouteHandle[Req, Event], fn SSEHandlerFunc[Req, Event], opts Options, impls ...rest.ServerImplementation) http.HandlerFunc {
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
		pendingCookies := make([]PendingCookie, 0)
		ctx = context.WithValue(ctx, responseCookiesKey{}, &pendingCookies)

		// carrier is constructed ONCE and its extracted maps reused for
		// BOTH the validation calls below AND the per-event MergeEvent
		// closure captured further down (docs/design/
		// d-0006-protocol-native-capabilities.md's Phase 6 "Decision A"
		// — eliminates the former double extraction).
		carrier := httpCarrier{r}
		queryVars := carrier.ExtractQuery()
		headerVars := rest.NormalizeHeaderVars(carrier.ExtractHeaders(), handle.HeaderParamNames())
		cookieVars := carrier.ExtractCookies()

		// Validate query parameters against their registered codecs (if any).
		if opts.MultiValueQueryParams {
			if err := handle.ValidateQueryMulti(carrier.ExtractQueryMulti()); err != nil {
				rest.ReportQueryErrors(ctx, err)
				opts.ErrorHandler(sw, r, http.StatusBadRequest, err)
				return
			}
		} else {
			if err := handle.ValidateQuery(queryVars); err != nil {
				rest.ReportQueryErrors(ctx, err)
				opts.ErrorHandler(sw, r, http.StatusBadRequest, err)
				return
			}
		}

		// Validate cookie parameters against their registered codecs (if any).
		if err := handle.ValidateCookies(cookieVars); err != nil {
			rest.ReportCookieErrors(ctx, err)
			opts.ErrorHandler(sw, r, http.StatusBadRequest, err)
			return
		}

		// Validate header parameters against their registered codecs (if any).
		if err := handle.ValidateHeaders(headerVars); err != nil {
			rest.ReportHeaderErrors(ctx, err)
			opts.ErrorHandler(sw, r, http.StatusBadRequest, err)
			return
		}

		// Validate path parameters against their registered codecs (if any).
		if names := handle.PathParamNames(); len(names) > 0 {
			if err := handle.ValidatePathParams(pathValues(r, names)); err != nil {
				rest.ReportPathErrors(ctx, err)
				opts.ErrorHandler(sw, r, http.StatusBadRequest, err)
				return
			}
		}
		pathVars := pathValues(r, handle.PathParamNames())

		// Enforce security: per-route requirements take precedence; nil falls back
		// to global security declared via Builder.AddGlobalSecurity.
		secReqs := handle.Descriptor.Security
		if secReqs == nil {
			secReqs = handle.GlobalSecurity
		}
		if len(secReqs) > 0 {
			if credErr := rest.ValidateSecurityCredentials(credentialExtractorFor(r), secReqs, handle.SecuritySchemes); credErr != nil {
				if secObs, ok := stats.ObserverFromContext(ctx).(stats.SecurityObserver); ok {
					secObs.RecordSecurityRejection(handle.Descriptor.Path, route.FirstSchemeName(secReqs))
				}
				opts.ErrorHandler(sw, r, http.StatusUnauthorized, credErr)
				return
			}
		}
		// Gap-1 review fix (docs/design/d-0007-declarative-middleware-layering.md's
		// Rollout Phase A review): this ports-facing sseHandlerFunc
		// previously never dispatched handle.MiddlewareHandlers at all —
		// only serve_sse.go's Attach path did. Mirrors serve_sse.go's
		// identical unified dispatch sequence (see handlerFunc's twin
		// rewrite for the full rationale).
		reqPtr := reflect.ValueOf(&req)
		granted, grantErr := httpsecurity.CollectGrantsReflect(ctx, r, reqPtr, impls, secReqs)
		if grantErr != nil {
			opts.ErrorHandler(sw, r, http.StatusUnauthorized, rest.SecurityError{Err: grantErr})
			return
		}
		middlewareOuts, mwErr := rest.DispatchMiddlewareHandlers(ctx, reqPtr, handle.MiddlewareHandlers, headerVars, cookieVars, queryVars)
		if mwErr != nil {
			dispatchErr, _ := rest.AsMiddlewareDispatchError(mwErr)
			if !dispatchErr.IsFnError {
				opts.ErrorHandler(sw, r, http.StatusBadRequest, dispatchErr.Err)
				return
			}
			if isSecuritySatisfyingHandler(handle.MiddlewareHandlers, dispatchErr.Name) {
				opts.ErrorHandler(sw, r, http.StatusUnauthorized, rest.SecurityError{Err: dispatchErr.Err})
				return
			}
			opts.ErrorHandler(sw, r, http.StatusBadRequest, rest.MiddlewareError{Name: dispatchErr.Name, Err: dispatchErr.Err})
			return
		}
		httpsecurity.MergeMiddlewareHandlerGrants(granted, satisfiesPerHandler(handle.MiddlewareHandlers), middlewareOuts)
		if err := rest.CheckScopes(secReqs, granted); err != nil {
			opts.ErrorHandler(sw, r, http.StatusUnauthorized, rest.SecurityError{Err: err})
			return
		}
		// Compose every middleware's OWN response header/cookie values
		// (derived from its Out, already produced pre-handler above) —
		// BEFORE SSE's own headers are committed below, mirroring
		// serve_sse.go's identical "Out-side" composition point.
		for i, h := range handle.MiddlewareHandlers {
			mwHeaders, mwCookies, encErr := h.EncodeOut(ctx, middlewareOuts[i])
			if encErr != nil {
				stats.ReportErrors(rest.DiagnosticObserver{Ctx: ctx}, "middleware:out", encErr)
				opts.ErrorHandler(sw, r, http.StatusInternalServerError, encErr)
				return
			}
			for k, v := range mwHeaders {
				responseHeaders.Set(k, v)
			}
			var mwCookieAttrs map[string]rest.CookieAttributes
			if h.EncodeOutCookieAttrs != nil {
				mwCookieAttrs, encErr = h.EncodeOutCookieAttrs(middlewareOuts[i])
				if encErr != nil {
					stats.ReportErrors(rest.DiagnosticObserver{Ctx: ctx}, "middleware:out", encErr)
					opts.ErrorHandler(sw, r, http.StatusInternalServerError, encErr)
					return
				}
			}
			for k, v := range mwCookies {
				pendingCookies = append(pendingCookies, PendingCookie{Name: k, Value: v, Attrs: mwCookieAttrs[k]})
			}
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
					stats.ReportErrors(rest.DiagnosticObserver{Ctx: ctx}, "response", err)
					return err
				}
			}
			if !headersCommitted {
				headersCommitted = true
				// Commit staged response headers/cookies on first send, before
				// any data is written (headers are not yet sent to the client).
				if err := handle.ValidateResponseHeaders(rest.NormalizeHeaderVars(responseHeaderValues(responseHeaders), handle.ResponseHeaderParamNames())); err != nil {
					rest.ReportResponseHeaderErrors(ctx, err)
					return err
				}
				if pending, ok := ctx.Value(responseCookiesKey{}).(*[]PendingCookie); ok {
					if err := handle.ValidateResponseCookies(responseCookieValues(*pending)); err != nil {
						rest.ReportResponseCookieErrors(ctx, err)
						return err
					}
					for i := range *pending {
						pc := &(*pending)[i]
						writeOpts := cookieOptionsFrom(pc.Attrs) // Codec is always nil here — cookieOptionsFrom never sets it
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
			if _, werr := writeSSEData(sw, data); werr != nil {
				return werr
			}
			if canFlush {
				flusher.Flush()
			}
			return nil
		}

		if err := fn(ctx, req, send); err != nil {
			// docs/roadmap/rest-typed-redirects.md round 2 finding E:
			// gated on headersCommitted, not sw.code — mirrors
			// adapters/nethttp's identical sseHandlerFunc check.
			if !headersCommitted {
				if redirErr, ok := asRedirectError(err); ok {
					sw.Header().Set("Location", redirErr.Location)
					sw.WriteHeader(redirErr.Status)
					return
				}
			}
			if sw.code == http.StatusOK {
				opts.ErrorHandler(sw, r, http.StatusInternalServerError, err)
			}
		}
	})

	// General-purpose middlewares wrap the WHOLE call, outermost-in, in
	// attachment order — see handlerFunc's equivalent wrapping.
	return applyGeneralMiddleware(inner, impls).ServeHTTP
}

func primaryStatus[Req, Resp any](handle *rest.RouteHandle[Req, Resp]) int {
	return primaryStatusFor(handle.Descriptor)
}

// primaryStatusFor is [primaryStatus]'s non-generic-signature equivalent —
// used by [serve], which only has a route.Route descriptor (via reflect),
// never a concrete *rest.RouteHandle[Req, Resp].
func primaryStatusFor(descriptor route.Route) int {
	if len(descriptor.Responses) == 0 {
		return http.StatusOK
	}
	code, err := strconv.Atoi(descriptor.Responses[0].Status)
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

// tryRespondErrorPatternGeneric is [handlerFunc]'s own Req/Resp-generic
// (no reflection needed, unlike serve.go's [tryRespondErrorPattern]
// reflection-based helper of a similar name) counterpart of the
// RECOMMENDED single call site for every Category-A failure point
// (docs/design/d-0005-error-handling.md's Topic 1/5):
// consults a declared [rest.ErrorPattern] via ObserveErrorResponseFor
// (which ALSO reports match/miss/span-tag observability internally) and,
// on a matched [rest.ErrorRespond] action, writes the typed response.
//
// Session-review finding (H1): handlerFunc — the dispatch behind
// port/stream bindings (IngestAdapter, LatestAdapter, HandlerLatest,
// PipelineHandler) — is a SEPARATE dispatch function from serve.go's
// fully Category-A-wired one; it never received Topic 1/5's wiring even
// though the roadmap's own North Star principle states any new failure
// point should, by default, be wired through this SAME enforcement
// point. This closes that gap for handlerFunc specifically (mirrors
// adapters/nethttp's identical fix exactly).
//
// Returns true when the response was fully handled — the caller should
// return immediately without invoking its own fixed-shape errFn call.
// Returns false (unmatched, a non-Respond action, or a write failure)
// when the caller should fall through to its EXISTING fixed-shape errFn
// call — purely additive, never a behavior change for routes with no
// matching declared pattern. *err is mutated in place to the pattern's
// own applyErr (a mapFn/encode failure) or a response-write failure,
// mirroring serve.go's identical contract — callers must use the
// (possibly updated) *err in their own subsequent errFn call.
// Delegates ENTIRELY to [rest.RouteHandle.DispatchErrorResponse]
// (docs/design/d-0006-protocol-native-capabilities.md's Phase 7) —
// this thin wrapper exists only to preserve every existing call site's
// bool-return/*err-mutation contract unchanged; the match/encode/
// validate/write orchestration itself now lives entirely in api/rest.
func tryRespondErrorPatternGeneric[Req, Resp any](
	ctx context.Context, sw *statusResponseWriter, handle *rest.RouteHandle[Req, Resp],
	obs stats.Observer, respHeaders http.Header, pendingCookies *[]PendingCookie, err *error,
) bool {
	handled, updatedErr := handle.DispatchErrorResponse(ctx, obs, sw, respHeaders, *pendingCookies, *err)
	*err = updatedErr
	return handled
}

// WriteErrorResponse implements [rest.ErrorResponseWriter] — the ONLY
// genuinely protocol-specific remainder of the former
// `writeErrorPatternResponse` (docs/design/
// d-0006-protocol-native-capabilities.md's Phase 7): match, encode, and
// validate all now happen inside [rest.RouteHandle.DispatchErrorResponse]
// itself; this method only writes the already-validated headers,
// cookies, status, and body onto the wire.
func (sw *statusResponseWriter) WriteErrorResponse(headers map[string][]string, cookies []PendingCookie, status int, body []byte) error {
	for key, vals := range headers {
		for _, v := range vals {
			sw.Header().Add(key, v)
		}
	}
	for _, pc := range cookies {
		writeOpts := cookieOptionsFrom(pc.Attrs) // Codec is always nil here — cookieOptionsFrom never sets it
		if err := SetCookie(sw, pc.Name, pc.Value, writeOpts); err != nil {
			return err
		}
	}
	sw.Header().Set("Content-Type", "application/json")
	sw.WriteHeader(status)
	_, err := sw.Write(body)
	return err
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

// credentialExtractorFor builds a [rest.CredentialExtractor] closure from
// r — the ONLY piece of this package's security-credential validation
// that still touches *http.Request directly. The actual codec
// validation/dispatch logic lives in [rest.ValidateSecurityCredentials],
// which needs no net/http import at all.
func credentialExtractorFor(r *http.Request) rest.CredentialExtractor {
	return func(location, name string) string {
		switch location {
		case "header":
			return r.Header.Get(name)
		case "query":
			return r.URL.Query().Get(name)
		case "cookie":
			if c, err := r.Cookie(name); err == nil {
				return c.Value
			}
		}
		return ""
	}
}
