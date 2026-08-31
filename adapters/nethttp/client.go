package nethttp

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/stats"
)

// CredentialFunc names the credential-providing shape [Call]/[CallWithHandle]
// recognize on an attached [middleware.ClientImplementation.Fn] — a type ALIAS
// (not a new defined type). Lets callers (and [NewCachingCredentialFunc])
// name the shape instead of repeating the inline function type everywhere;
// attach it to a route via [rest.Route.ClientMW] — examples/go-edge-models/
// docker/registry's own package-level credentialFunc type alias is the
// precedent this mirrors.
type CredentialFunc = func(ctx context.Context, reqs []route.SecurityRequirement) (http.Header, error)

// CallOptions configures an outgoing HTTP request made via [Call].
type CallOptions struct {
	// QueryParams appends query string parameters to the URL.
	// Each value is validated against its registered [rest.QueryParam] codec (if any)
	// before the request is sent.
	QueryParams map[string]string

	// CookieParams adds cookies to the outgoing request.
	// Each value is validated against its registered [rest.CookieParam] codec (if any)
	// before the request is sent.
	CookieParams map[string]string

	// HeaderParams adds declared request headers to the outgoing request.
	// Each value is validated against its registered [rest.HeaderParam] codec (if any)
	// before the request is sent.
	//
	// Do not pass the Authorization header here — use [CallOptions.ExtraHeaders] or
	// a credential-providing [middleware.ClientImplementation] for security credentials.
	HeaderParams map[string]string

	// ExtraHeaders adds arbitrary HTTP headers to the outgoing request without
	// codec validation. Use for non-declared headers such as X-Request-ID,
	// User-Agent, or static Authorization values.
	ExtraHeaders http.Header

	// OnCredentialRejected, when non-nil, is called when the server responds
	// with HTTP 401 AND at least one credential-providing [middleware.ClientImplementation]
	// was attached to this call (mirrors the "only if the credential mechanism
	// actually engaged" gating used for the symmetric client-side format check
	// below). Purely a notification hook — Call does NOT retry the request
	// automatically.
	//
	// [NewCachingCredentialFunc]'s returned invalidate function is designed
	// to be wired here: a 401 invalidates the cached credential so the
	// NEXT call (a simple, explicit retry the caller writes) fetches a
	// fresh one instead of reusing the rejected one.
	OnCredentialRejected func()

	// Observer, when non-nil, receives per-call lifecycle events.
	// [stats.Observer.RecordRequest] is called on every code path — including
	// early-exit validation failures — with the HTTP method, route path template
	// (not the concrete URL), HTTP status code, and total duration.
	// Status 0 is used when validation fails before any HTTP request is sent
	// (path var, query, cookie, or header codec failure; credential func error;
	// or request build error). This allows observers to count all call attempts,
	// including those that never reach the network.
	// Per-field validation errors are reported via [stats.Observer.RecordValidationError].
	// Defaults to [stats.NoopObserver] when nil.
	Observer stats.Observer
}

// UnexpectedStatusError is returned by [Call] when the server responds with a
// non-2xx HTTP status code.
//
// Use [errors.As] to extract the structured fields for slog logging:
//
//	var statusErr nethttp.UnexpectedStatusError
//	if errors.As(err, &statusErr) {
//	    slog.Error("unexpected response",
//	        "method", statusErr.Method,
//	        "path",   statusErr.Path,
//	        "status", statusErr.StatusCode,
//	        "body",   string(statusErr.Body),
//	    )
//	}
type UnexpectedStatusError struct {
	// Method is the HTTP method used for the request (e.g. "GET", "POST").
	Method string
	// Path is the route path template, not the concrete URL (e.g. "/users/{id}").
	// Use this for log grouping and metrics — it does not contain the base URL or
	// concrete path variable values.
	Path string
	// StatusCode is the HTTP response status code returned by the server.
	StatusCode int
	// Body is the raw response body returned by the server (may be nil or empty).
	Body []byte
	// Header is the raw HTTP response header set returned by the server
	// (e.g. WWW-Authenticate on a 401 challenge). Response header/cookie
	// merge fields declared via [rest.NewRequiredResponseHeaderParam] only
	// apply on a successful (2xx) response — Header is the declarative
	// escape hatch for callers that need a response header on a non-2xx
	// response, without hand-rolling their own HTTP request.
	Header http.Header
}

func (e UnexpectedStatusError) Error() string {
	return fmt.Sprintf("%s %s: unexpected status %d", e.Method, e.Path, e.StatusCode)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e UnexpectedStatusError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("method", e.Method),
		slog.String("path", e.Path),
		slog.Int("status", e.StatusCode),
	)
}

// ErrorPatternResponse is returned by [Call] instead of [UnexpectedStatusError]
// when the response status matches a route-declared [rest.ErrorPattern]
// (tagged with the default [rest.ErrorRespond] action) and the body decodes
// successfully via that pattern's declared codec.
//
// Use [errors.As] to extract the decoded typed payload:
//
//	var epr nethttp.ErrorPatternResponse
//	if errors.As(err, &epr) {
//	    payload, ok := epr.Value.(MyErrorPayload)
//	    ...
//	}
type ErrorPatternResponse struct {
	// StatusCode is the HTTP response status code returned by the server.
	StatusCode int
	// Value is the decoded typed error payload (the pattern's declared B type).
	Value any
	// Body is the raw response body returned by the server.
	Body []byte
}

func (e ErrorPatternResponse) Error() string {
	return fmt.Sprintf("unexpected status %d: %T", e.StatusCode, e.Value)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e ErrorPatternResponse) LogValue() slog.Value {
	return slog.GroupValue(
		slog.Int("status", e.StatusCode),
		slog.Any("value", e.Value),
	)
}

// RequestBuildError is returned by [Call] when constructing the outgoing
// *http.Request fails (e.g. malformed base URL or context already cancelled).
//
// Use [errors.As] to extract the underlying error for slog logging:
//
//	var buildErr nethttp.RequestBuildError
//	if errors.As(err, &buildErr) {
//	    slog.Error("failed to build request", "cause", buildErr.Err)
//	}
type RequestBuildError struct {
	// Err is the underlying error from [http.NewRequestWithContext].
	Err error
}

func (e RequestBuildError) Error() string {
	return fmt.Sprintf("nethttp client: build request: %s", e.Err)
}

// Unwrap allows [errors.Is] and [errors.As] to traverse the underlying error.
func (e RequestBuildError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e RequestBuildError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.Any("cause", e.Err),
	)
}

// RequestError is returned by [Call] when executing the HTTP call fails
// (network error, DNS failure, TLS error, or context cancellation).
//
// Use [errors.As] to extract the structured fields for slog logging:
//
//	var reqErr nethttp.RequestError
//	if errors.As(err, &reqErr) {
//	    slog.Error("http call failed",
//	        "method", reqErr.Method,
//	        "path",   reqErr.Path,
//	        "cause",  reqErr.Err,
//	    )
//	}
type RequestError struct {
	// Method is the HTTP method (e.g. "GET", "POST").
	Method string
	// Path is the route path template (e.g. "/users/{id}").
	Path string
	// Err is the underlying transport error from [http.Client.Do].
	Err error
}

func (e RequestError) Error() string {
	return fmt.Sprintf("nethttp client: %s %s: %s", e.Method, e.Path, e.Err)
}

// Unwrap allows [errors.Is] and [errors.As] to traverse the underlying error.
func (e RequestError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e RequestError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("method", e.Method),
		slog.String("path", e.Path),
		slog.Any("cause", e.Err),
	)
}

// ResponseBodyError is returned by [Call] when reading the HTTP response body
// fails after a successful connection.
//
// Use [errors.As] to extract the underlying error for slog logging:
//
//	var bodyErr nethttp.ResponseBodyError
//	if errors.As(err, &bodyErr) {
//	    slog.Error("failed to read response body", "cause", bodyErr.Err)
//	}
type ResponseBodyError struct {
	// Err is the underlying error from reading the response body.
	Err error
}

func (e ResponseBodyError) Error() string {
	return fmt.Sprintf("nethttp client: read response body: %s", e.Err)
}

// Unwrap allows [errors.Is] and [errors.As] to traverse the underlying error.
func (e ResponseBodyError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e ResponseBodyError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.Any("cause", e.Err),
	)
}

// ConflictingCredentialHeaderError is returned by [Call] when TWO attached
// credential-providing middlewares (Fn shape
// func(context.Context, []route.SecurityRequirement) (http.Header, error))
// return DIFFERENT values for the SAME outgoing header key — see "L9" in
// docs/roadmap/declarative-middleware.md. Identical values from two
// middlewares for the same key are merged silently; only DIFFERING values
// conflict.
type ConflictingCredentialHeaderError struct {
	Header                    string
	FirstSource, SecondSource string
}

func (e ConflictingCredentialHeaderError) Error() string {
	return fmt.Sprintf("nethttp: conflicting credential header %q: %q vs %q", e.Header, e.FirstSource, e.SecondSource)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e ConflictingCredentialHeaderError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("header", e.Header),
		slog.String("first_source", e.FirstSource),
		slog.String("second_source", e.SecondSource),
	)
}

// validateClientImplementationShapes checks every attached impl.Fn against the
// ONE concrete shape [Call] recognizes ([CredentialFunc]'s shape),
// EAGERLY before any network activity rather than letting
// [mergeCredentialHeaders] silently skip a malformed Fn — a
// [middleware.ClientImplementation] built for the wrong adapter (e.g. a Fn
// shape meant for a different transport, passed here by mistake) fails
// loudly and immediately instead.
func validateClientImplementationShapes(impls []middleware.ClientImplementation) error {
	for _, impl := range impls {
		switch impl.Fn.(type) {
		case nil:
			continue // spec-only/no-op implementation — allowed
		case func(context.Context, []route.SecurityRequirement) (http.Header, error):
			continue
		default:
			return middleware.MiddlewareShapeError{
				Name:     impl.Name,
				Expected: "func(context.Context, []route.SecurityRequirement) (http.Header, error)",
				Got:      fmt.Sprintf("%T", impl.Fn),
			}
		}
	}
	return nil
}

// mergeCredentialHeaders runs every attached credential-providing Fn IN
// ATTACHMENT ORDER, merging their returned http.Header values into ONE
// combined set — this is a MERGE, not an authorization check (the client
// never judges its own authorization, only the server does). Any Fn's own
// error aborts immediately (fail-fast). ran reports whether at least one
// credential-providing Fn was found and invoked (used to gate the
// client-side credential-format check and OnCredentialRejected below,
// mirroring the pre-existing "nil CredentialFunc is not an error"
// contract).
//
// GATED by Satisfies vs secReqs (the correctness improvement from
// docs/design/middleware-workflow-simplification.md's "Client-side
// Satisfies-gated implementations" — PREVENTS a mismatched implementation
// from running rather than merely detecting it): an implementation with a
// NON-EMPTY Satisfies only runs when at least one of its scheme names is
// actually present in secReqs; an implementation with an EMPTY Satisfies
// (general-purpose) always runs.
//
// Every impl.Fn here is already guaranteed to match this shape (or be
// nil) — [validateClientImplementationShapes] rejects anything else EAGERLY,
// at the top of [Call], before this function is ever reached.
func mergeCredentialHeaders(ctx context.Context, secReqs []route.SecurityRequirement, impls []middleware.ClientImplementation) (combined http.Header, ran bool, err error) {
	combined = make(http.Header)
	setBy := make(map[string]string)
	reqSchemes := make(map[string]bool, len(secReqs))
	for _, req := range secReqs {
		for scheme := range req {
			reqSchemes[scheme] = true
		}
	}
	for _, impl := range impls {
		fn, ok := impl.Fn.(func(context.Context, []route.SecurityRequirement) (http.Header, error))
		if !ok {
			continue
		}
		if len(impl.Satisfies) > 0 {
			matched := false
			for _, s := range impl.Satisfies {
				if reqSchemes[s] {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		ran = true
		h, ferr := fn(ctx, secReqs)
		if ferr != nil {
			return nil, ran, ferr
		}
		for key, vals := range h {
			if prior, exists := setBy[key]; exists && prior != impl.Name && !equalHeaderValues(combined[key], vals) {
				return nil, ran, ConflictingCredentialHeaderError{Header: key, FirstSource: prior, SecondSource: impl.Name}
			}
			combined[key] = vals
			setBy[key] = impl.Name
		}
	}
	return combined, ran, nil
}

func equalHeaderValues(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Call executes a typed HTTP request for r against c's baseURL — the SOLE
// public client-side entry point (see
// docs/design/middleware-workflow-simplification.md's "Decision:
// symmetric client-side declarative wiring"). r is a [rest.Route] value
// (typically the SAME value the server side declared via [rest.Route.Use]/
// [rest.Route.HandleMW]); Call derives a [*rest.RouteHandle] internally via
// [rest.Route.ClientHandle] and ALWAYS auto-derives path/query/header/cookie
// values from the route's declared merge fields (folding in what the
// former CallHandle did exclusively) — there is no manual-vars variant
// anymore; a route intended for client use must declare merge fields for
// every path/query/header/cookie value it needs.
//
// For body-bearing methods (POST, PUT, PATCH) req is JSON-encoded as the
// request body; for other methods (GET, HEAD, DELETE) req is ignored.
//
// Security requirements: if the route declares non-nil Security (or
// inherits global security), every attached credential-providing
// [middleware.ClientImplementation] (declared via [rest.Route.ClientMW],
// GATED by Satisfies vs the route's declared security requirements) is
// called to obtain the Authorization headers. No credential-providing
// implementation attached to a secured route is not an error — the
// request is sent without credential injection; use
// [CallOptions.ExtraHeaders] to supply static credentials instead.
//
// On a 2xx response the body is decoded into Resp using the route's response codec.
// On a non-2xx response [UnexpectedStatusError] is returned.
//
// [CallOptions.Observer] receives [stats.Observer.RecordRequest] after every call
// (success or failure) with the route path template (not the concrete URL), status
// code, and total duration. Per-field validation errors are reported separately via
// [stats.Observer.RecordValidationError].
//
// Example — GET with path variable declared via a merge field:
//
//	user, err := nethttp.Call(ctx, caller, getUserRoute, GetUserReq{ID: "f47ac10b"},
//	    nethttp.CallOptions{Observer: obs})
//
// Example — POST with body, on a route declaring a bearer credential via ClientMW:
//
//	resp, err := nethttp.Call(ctx, caller, createUserRoute, createReq, nethttp.CallOptions{})
func Call[Req, Resp any](
	ctx context.Context,
	c *Caller,
	r rest.Route[Req, Resp],
	req Req,
	opts CallOptions,
) (Resp, error) {
	handle := r.ClientHandle()
	return CallWithHandle(ctx, c.client, c.baseURL, handle, req, opts)
}

// callWithVars is the UNEXPORTED, handle-based call primitive — the
// actual call logic, shared internally by [Call] (via [CallWithHandle])
// AND [ports]' nethttp binding adapters (which own a *rest.RouteHandle
// directly, built once and called many times, and never a [rest.Route]
// value — see docs/design/middleware-workflow-simplification.md's
// "Decision: unexported handle-based primitive" for the full rationale).
// vars supplies path template values explicitly (no merge-field
// auto-derivation) — see [CallWithHandle] for that convenience.
func callWithVars[Req, Resp any](
	ctx context.Context,
	client *http.Client,
	baseURL string,
	handle *rest.RouteHandle[Req, Resp],
	req Req,
	vars map[string]string,
	opts CallOptions,
) (Resp, error) {
	var zero Resp

	obs := opts.Observer
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}

	// Ferry per-field validation errors (Class B) out via ctx, then drain
	// them into obs.RecordValidationError exactly once before returning —
	// Call has no outer wrapping concept the way Handler's
	// Observability does, so it drains inline via defer instead.
	ctx = stats.WithDiagnostics(ctx)
	defer func() {
		for _, d := range stats.DiagnosticsFromContext(ctx) {
			obs.RecordValidationError(d.Location, d.ConstraintName, d.Field)
		}
	}()

	method := strings.ToUpper(handle.Descriptor.Method)
	routePath := handle.Descriptor.Path
	start := time.Now()

	// 0. Validate every attached impl.Fn against the ONE concrete shape
	// this package recognizes client-side, EAGERLY before any network
	// activity; a malformed Fn fails loudly here instead of being
	// silently ignored by mergeCredentialHeaders below (see
	// middleware.ClientImplementation.Fn's own doc comment: "fails
	// LOUDLY... never silently").
	allImpls := handle.ClientImplementations
	if err := validateClientImplementationShapes(allImpls); err != nil {
		obs.RecordRequest(method, routePath, 0, time.Since(start))
		return zero, err
	}

	// 1. Build and validate path.
	concretePath, err := handle.BuildPath(vars)
	if err != nil {
		reportPathErrors(ctx, err)
		obs.RecordRequest(method, routePath, 0, time.Since(start))
		return zero, err
	}

	// 2. Validate query parameters.
	if err := handle.ValidateQuery(opts.QueryParams); err != nil {
		reportQueryErrors(ctx, err)
		obs.RecordRequest(method, routePath, 0, time.Since(start))
		return zero, err
	}

	// 3. Validate cookie parameters.
	if err := handle.ValidateCookies(opts.CookieParams); err != nil {
		reportCookieErrors(ctx, err)
		obs.RecordRequest(method, routePath, 0, time.Since(start))
		return zero, err
	}

	// 4. Validate declared header parameters.
	if err := handle.ValidateHeaders(opts.HeaderParams); err != nil {
		reportHeaderErrors(ctx, err)
		obs.RecordRequest(method, routePath, 0, time.Since(start))
		return zero, err
	}

	if to, ok := obs.(stats.TraceObserver); ok {
		ctx = to.StartSpan(ctx, "http.request", routePath)
		defer func() { to.EndSpan(ctx, err) }()
	}

	// 5. Build full URL.
	rawURL := strings.TrimRight(baseURL, "/") + concretePath
	if len(opts.QueryParams) > 0 {
		qv := make(url.Values, len(opts.QueryParams))
		for k, v := range opts.QueryParams {
			qv.Set(k, v)
		}
		rawURL += "?" + qv.Encode()
	}

	// 6. Resolve security requirements and obtain credentials — runs EVERY
	// attached credential-providing middleware and merges their returned
	// headers into ONE combined set (see "L9" in
	// docs/roadmap/declarative-middleware.md).
	secReqs := handle.Descriptor.Security
	if secReqs == nil {
		secReqs = handle.GlobalSecurity
	}
	var credHeaders http.Header
	var credentialFnRan bool
	if len(secReqs) > 0 {
		var credErr error
		credHeaders, credentialFnRan, credErr = mergeCredentialHeaders(ctx, secReqs, allImpls)
		if credErr != nil {
			obs.RecordRequest(method, routePath, 0, time.Since(start))
			return zero, credErr
		}
	}

	// 7. Encode request body (body-bearing methods only).
	var body io.Reader
	var contentType string
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch:
		var bodyBytes []byte
		if len(handle.RequestFormats) > 0 {
			bodyBytes, err = handle.RequestFormats[0].Marshal(req)
			contentType = handle.RequestFormats[0].ContentType()
		} else {
			bodyBytes, err = handle.EncodeRequest(req)
			contentType = "application/json"
		}
		if err != nil {
			reportBodyErrors(ctx, err)
			obs.RecordRequest(method, routePath, 0, time.Since(start))
			return zero, err
		}
		body = bytes.NewReader(bodyBytes)
	}

	// 8. Build the HTTP request.
	httpReq, err := http.NewRequestWithContext(ctx, method, rawURL, body)
	if err != nil {
		obs.RecordRequest(method, routePath, 0, time.Since(start))
		return zero, RequestBuildError{Err: err}
	}

	// Set Content-Type for body-bearing methods.
	if contentType != "" {
		httpReq.Header.Set("Content-Type", contentType)
	}

	// Set Accept header from registered response formats.
	if len(handle.Formats) > 0 {
		if ct := handle.Formats[0].ContentType(); ct != "" {
			httpReq.Header.Set("Accept", ct)
		}
	} else {
		httpReq.Header.Set("Accept", "application/json")
	}

	// Merge declared header params.
	for k, v := range opts.HeaderParams {
		httpReq.Header.Set(k, v)
	}

	// Merge extra headers (no codec validation).
	for k, vs := range opts.ExtraHeaders {
		for _, v := range vs {
			httpReq.Header.Add(k, v)
		}
	}

	// Merge credential headers.
	for k, vs := range credHeaders {
		for _, v := range vs {
			httpReq.Header.Add(k, v)
		}
	}

	// Add cookies.
	for k, v := range opts.CookieParams {
		httpReq.AddCookie(&http.Cookie{Name: k, Value: v})
	}

	// 8b. Validate the outgoing credential FORMAT — the client-side mirror
	// of the server-side check in Handler (validateSecurityCredentials).
	// httpReq now carries every header/cookie/query value that will be
	// sent, including credHeaders merged above, so it is a valid input to
	// the SAME extraction/validation helpers the server adapter uses on an
	// incoming request — reused here verbatim, zero duplication. A route
	// with no [rest.WithSecurityScheme] declaration (handle.SecuritySchemes
	// empty) or a scheme with a nil Codec is a no-op, identical to today's
	// behavior.
	//
	// Gated on len(credHeaders) > 0 — i.e. the merge actually PRODUCED at
	// least one header — NOT on credentialFnRan or len(secReqs) > 0 alone.
	// A credential-providing middleware that deliberately returns (nil,
	// nil) to mean "this call needs no credential" (e.g. an auth flow that
	// first probes whether the specific server instance requires auth at
	// all, like examples/go-edge-models/docker/registry's
	// NewAuthCredentialFunc) must stay a non-error — symmetric with the
	// pre-existing "nil CredentialFunc on a secured route is not an error"
	// contract. Without this gate, a route declaring both Security and a
	// non-empty-string Codec would wrongly reject every request where the
	// credential mechanism correctly determined no credential was needed,
	// since the resulting (absent) Authorization header extracts as ""
	// either way.
	if len(secReqs) > 0 && len(credHeaders) > 0 {
		if credErr := validateSecurityCredentials(httpReq, secReqs, handle.SecuritySchemes); credErr != nil {
			if secObs, ok := obs.(stats.SecurityObserver); ok {
				secObs.RecordSecurityRejection(routePath, firstScheme(secReqs))
			}
			obs.RecordRequest(method, routePath, 0, time.Since(start))
			return zero, credErr
		}
	}

	// 9. Execute the request.
	resp, err := client.Do(httpReq)
	if err != nil {
		obs.RecordRequest(method, routePath, 0, time.Since(start))
		return zero, RequestError{Method: method, Path: routePath, Err: err}
	}
	defer resp.Body.Close()

	statusCode := resp.StatusCode
	obs.RecordRequest(method, routePath, statusCode, time.Since(start))

	// 10. Read response body.
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return zero, ResponseBodyError{Err: err}
	}

	// 11. Non-2xx → structured error, preferring a declared ErrorPattern
	// decode over the untyped fallback when one matches.
	if statusCode < 200 || statusCode >= 300 {
		// A 401 with an engaged CredentialFunc means the credential we sent
		// was rejected — notify OnCredentialRejected (if configured) so a
		// caching wrapper can invalidate its cached credential before the
		// caller's own next attempt. This fires regardless of whether an
		// ErrorPattern also matches below — it is orthogonal to that
		// decoding concern.
		if statusCode == http.StatusUnauthorized && credentialFnRan && opts.OnCredentialRejected != nil {
			opts.OnCredentialRejected()
		}
		if errResp, matched, decErr := handle.DecodeErrorFor(statusCode, respBody); matched && decErr == nil {
			return zero, ErrorPatternResponse{
				StatusCode: errResp.Status,
				Value:      errResp.Value,
				Body:       errResp.Body,
			}
		}
		return zero, UnexpectedStatusError{
			Method:     method,
			Path:       routePath,
			StatusCode: statusCode,
			Body:       respBody,
			Header:     resp.Header,
		}
	}

	// 12. Decode typed response.
	var result Resp
	if len(handle.Formats) > 0 {
		result, err = handle.Formats[0].Unmarshal(respBody)
	} else {
		result, err = handle.DecodeResponse(respBody)
	}
	if err != nil {
		reportBodyErrors(ctx, err)
		return zero, err
	}

	// 13. Merge response header/cookie values declared via
	// rest.NewRequiredResponseHeaderParam/etc. into the SAME result value —
	// the response-direction mirror of how request merge fields are
	// applied server-side. Additive: only runs when the route has
	// response merge-capable params; identical behavior otherwise.
	headerFields := handle.ResponseHeaderMergeFields()
	cookieFields := handle.ResponseCookieMergeFields()
	if len(headerFields)+len(cookieFields) > 0 {
		vars := make(map[string]string, len(headerFields)+len(cookieFields))
		for _, c := range resp.Cookies() {
			vars[c.Name] = c.Value
		}
		for k := range resp.Header {
			vars[k] = resp.Header.Get(k)
		}
		mergeFields := make([]codex.FieldCodec[Resp], 0, len(headerFields)+len(cookieFields))
		mergeFields = append(mergeFields, headerFields...)
		mergeFields = append(mergeFields, cookieFields...)
		if err := codex.DecodeVars(&result, vars, mergeFields...); err != nil {
			reportBodyErrors(ctx, err)
			return zero, err
		}
	}

	return result, nil
}

// CallWithHandle is [callWithVars]'s single-call convenience wrapper,
// EXPORTED for callers that already have a *[rest.RouteHandle] but no
// [rest.Route] value to build one from — e.g. [ports]' handle-based
// binding adapters, and other packages bridging a REST route into a
// different protocol (see adapters/mcprest, which proxies MCP tool calls
// through an outbound REST call using a *rest.RouteHandle it was handed
// directly). Derives the path vars AND [CallOptions.QueryParams]/
// [HeaderParams]/[CookieParams] from req automatically, using the
// route's role-aware merge-field accessors
// ([rest.RouteHandle.PathMergeFields]/[QueryMergeFields]/
// [HeaderMergeFields]/[CookieMergeFields]) and [codex.EncodeVars] — the
// SAME auto-derivation the public [Call] performs internally.
//
// Prefer [Call] when a [rest.Route] value is available (the common
// case) — it additionally builds the handle for you via
// [rest.Route.ClientHandle].
//
// Any entry already present in opts.QueryParams/HeaderParams/CookieParams
// takes PRECEDENCE over the corresponding derived value for the same key.
func CallWithHandle[Req, Resp any](
	ctx context.Context,
	client *http.Client,
	baseURL string,
	handle *rest.RouteHandle[Req, Resp],
	req Req,
	opts CallOptions,
) (Resp, error) {
	var zero Resp

	vars, err := codex.EncodeVars(req, handle.PathMergeFields()...)
	if err != nil {
		return zero, err
	}
	query, err := codex.EncodeVars(req, handle.QueryMergeFields()...)
	if err != nil {
		return zero, err
	}
	headers, err := codex.EncodeVars(req, handle.HeaderMergeFields()...)
	if err != nil {
		return zero, err
	}
	cookies, err := codex.EncodeVars(req, handle.CookieMergeFields()...)
	if err != nil {
		return zero, err
	}

	opts.QueryParams = overrideDerived(query, opts.QueryParams)
	opts.HeaderParams = overrideDerived(headers, opts.HeaderParams)
	opts.CookieParams = overrideDerived(cookies, opts.CookieParams)

	return callWithVars(ctx, client, baseURL, handle, req, vars, opts)
}

// overrideDerived merges derived (from codex.EncodeVars) and explicit (from
// caller-supplied CallOptions) maps, with explicit taking precedence on key
// collision. Returns nil when both inputs are empty (preserves Call's
// existing nil-is-fine behavior for CallOptions map fields).
func overrideDerived(derived, explicit map[string]string) map[string]string {
	if len(derived) == 0 {
		return explicit
	}
	if len(explicit) == 0 {
		return derived
	}
	out := make(map[string]string, len(derived)+len(explicit))
	for k, v := range derived {
		out[k] = v
	}
	for k, v := range explicit {
		out[k] = v
	}
	return out
}
