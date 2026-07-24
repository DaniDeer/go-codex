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
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/stats"
)

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
	// [CallOptions.CredentialFunc] for security credentials.
	HeaderParams map[string]string

	// ExtraHeaders adds arbitrary HTTP headers to the outgoing request without
	// codec validation. Use for non-declared headers such as X-Request-ID,
	// User-Agent, or static Authorization values.
	ExtraHeaders http.Header

	// CredentialFunc, when non-nil, is called for routes that declare non-nil
	// Security requirements. It receives the effective security requirements and
	// must return headers to merge into the outgoing request (e.g. Authorization).
	// Return a non-nil error to abort the call before the request is sent.
	//
	// Use [CallOptions.ExtraHeaders] for simple static credentials;
	// use CredentialFunc for structured or dynamic credential injection —
	// it mirrors the server-side SecurityFunc pattern.
	CredentialFunc func(ctx context.Context, reqs []route.SecurityRequirement) (http.Header, error)

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

// Call executes a typed HTTP request for the given route handle against baseURL.
//
// The concrete URL is built as baseURL + handle.BuildPath(vars) + "?" + queryString.
// For body-bearing methods (POST, PUT, PATCH) req is JSON-encoded as the request
// body; for other methods (GET, HEAD, DELETE) req is ignored.
//
// All parameters are validated against their registered codecs before the request
// is sent: path variables via [rest.RouteHandle.BuildPath], query parameters via
// [rest.RouteHandle.ValidateQuery], cookies via [rest.RouteHandle.ValidateCookies],
// and request headers via [rest.RouteHandle.ValidateHeaders]. A validation failure
// returns the corresponding [rest] error type (e.g. [rest.PathParamError],
// [rest.QueryParamError]) without sending any request.
//
// Security requirements: if the route declares non-nil Security (or inherits
// global security), [CallOptions.CredentialFunc] is called to obtain the
// Authorization headers. A nil CredentialFunc on a secured route is not an error —
// the request is sent without credential injection; use [CallOptions.ExtraHeaders]
// to supply static credentials instead.
//
// On a 2xx response the body is decoded into Resp using the route's response codec.
// On a non-2xx response [UnexpectedStatusError] is returned.
//
// [CallOptions.Observer] receives [stats.Observer.RecordRequest] after every call
// (success or failure) with the route path template (not the concrete URL), status
// code, and total duration. Per-field validation errors are reported separately via
// [stats.Observer.RecordValidationError].
//
// Example — GET with path variable:
//
//	handle := getUserRoute.ClientHandle()
//	user, err := nethttp.Call(ctx, http.DefaultClient, "https://api.example.com",
//	    handle, struct{}{}, map[string]string{"id": "f47ac10b"},
//	    nethttp.CallOptions{Observer: obs})
//
// Example — POST with body and bearer token:
//
//	handle := createUserRoute.ClientHandle()
//	resp, err := nethttp.Call(ctx, http.DefaultClient, "https://api.example.com",
//	    handle, createReq, nil,
//	    nethttp.CallOptions{
//	        CredentialFunc: func(ctx context.Context, reqs []route.SecurityRequirement) (http.Header, error) {
//	            h := make(http.Header)
//	            h.Set("Authorization", "Bearer "+token)
//	            return h, nil
//	        },
//	    })
func Call[Req, Resp any](
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

	method := strings.ToUpper(handle.Descriptor.Method)
	routePath := handle.Descriptor.Path
	start := time.Now()

	// 1. Build and validate path.
	concretePath, err := handle.BuildPath(vars)
	if err != nil {
		reportPathErrors(err, obs)
		obs.RecordRequest(method, routePath, 0, time.Since(start))
		return zero, err
	}

	// 2. Validate query parameters.
	if err := handle.ValidateQuery(opts.QueryParams); err != nil {
		reportQueryErrors(err, obs)
		obs.RecordRequest(method, routePath, 0, time.Since(start))
		return zero, err
	}

	// 3. Validate cookie parameters.
	if err := handle.ValidateCookies(opts.CookieParams); err != nil {
		reportCookieErrors(err, obs)
		obs.RecordRequest(method, routePath, 0, time.Since(start))
		return zero, err
	}

	// 4. Validate declared header parameters.
	if err := handle.ValidateHeaders(opts.HeaderParams); err != nil {
		reportHeaderErrors(err, obs)
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

	// 6. Resolve security requirements and obtain credentials.
	secReqs := handle.Descriptor.Security
	if secReqs == nil {
		secReqs = handle.GlobalSecurity
	}
	var credHeaders http.Header
	if len(secReqs) > 0 && opts.CredentialFunc != nil {
		var credErr error
		credHeaders, credErr = opts.CredentialFunc(ctx, secReqs)
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
			reportBodyErrors(err, obs)
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
		reportBodyErrors(err, obs)
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
			reportBodyErrors(err, obs)
			return zero, err
		}
	}

	return result, nil
}

// CallHandle is the single-call convenience wrapper around [Call]: it
// derives the path vars AND [CallOptions.QueryParams]/[HeaderParams]/
// [CookieParams] from req automatically, using the route's role-aware
// merge-field accessors ([rest.RouteHandle.PathMergeFields]/
// [QueryMergeFields]/[HeaderMergeFields]/[CookieMergeFields]) and
// [codex.EncodeVars] — one line instead of building each map by hand.
//
// Any entry already present in opts.QueryParams/HeaderParams/CookieParams
// takes PRECEDENCE over the corresponding derived value for the same key —
// this lets a caller override a struct field's value, or add ad-hoc
// params the struct doesn't declare, without losing the one-line
// convenience for the common case. opts fields left nil are populated
// entirely from the derived values (or left nil if the route declares no
// merge fields for that role).
//
// [Call] remains available as the lower-level escape hatch for callers
// that build the maps themselves — e.g. no merge fields declared, path
// vars from a non-struct source, or a route shared between multiple
// unrelated Req shapes.
//
// Example:
//
//	handle := getUserActivity.ClientHandle()
//	activity, err := nethttp.CallHandle(ctx, client, baseURL, handle,
//	    GetUserActivityReq{ID: userID, Filter: "logins"}, nethttp.CallOptions{})
func CallHandle[Req, Resp any](
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

	return Call(ctx, client, baseURL, handle, req, vars, opts)
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
