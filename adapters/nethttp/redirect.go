package nethttp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"

	"github.com/DaniDeer/go-codex/api/rest"
)

// defaultMaxRedirects is the chain-depth cap used when
// [rest.ClientCallOptions.MaxRedirects]/[rest.ClientConsumeOptions.MaxRedirects]
// is left at its zero value — mirrors net/http's own 10-hop default
// precedent (docs/roadmap/rest-typed-redirects.md, round 3 finding G2).
const defaultMaxRedirects = 10

// redirectFollowUpInfo captures exactly what [followRedirect] needs from
// the ORIGINATING call to build a follow-up request — reusing the
// ORIGINATING call's own already-computed credentials/headers/body
// (round 4 findings H1/H1b), never the registered target handle's own
// baked-in ClientMW (round 2 finding A). Populated by [clientTransport.
// Call]'s own networkStep closure (same lexical scope, assigned into an
// outer variable) right before it returns a [rest.RedirectError].
type redirectFollowUpInfo struct {
	method       string
	credHeaders  http.Header
	cookieVars   map[string]string
	extraHeaders map[string][]string
	bodyBytes    []byte // only meaningful for 307/308 (method/body preserved)
	contentType  string
}

// followRedirect is the entry point for resolving and following a REST
// redirect chain starting from first (the [rest.RedirectError] returned
// by the originating call) — the TOP-level wrapper that resolves
// maxRedirects' default and client's nil case, then delegates to
// [followRedirectHop] for the actual (possibly chained) work. This is a
// PARALLEL, low-level code path — NOT a recursive call into
// [rest.Client.Call]/[rest.Client.Consume] (round 4 finding H2):
// recursing would re-derive credentials from the TARGET route's own
// ClientMW, reintroducing the exact credential-identity bug round 2
// finding A closed. client is nil when this transport was never attached
// via [rest.Client.Attach] (i.e. used directly via
// [rest.CallWithTransport], which deliberately stays registry-free,
// round 2 finding #5) — in that case there is no registry to consult at
// all, so first is returned as-is (a typed [rest.RedirectError] the
// caller follows up on manually).
func followRedirect(ctx context.Context, client *rest.Client, httpClient *http.Client, baseURL string, first rest.RedirectError, info redirectFollowUpInfo, maxRedirects int) (any, error) {
	if client == nil {
		return nil, first
	}
	if maxRedirects <= 0 {
		maxRedirects = defaultMaxRedirects
	}
	return followRedirectHop(ctx, client, httpClient, baseURL, first, info, maxRedirects, maxRedirects)
}

// followRedirectHop performs ONE hop of the chain — remaining is how
// many hops (THIS one included) are still allowed. Resolves current's
// target against client's registry (Call-decodable routes first,
// Consume-streamable sseRoutes second — a match in the latter alone is
// [rest.RedirectToStreamUnsupportedError], round 2 finding D), then
// issues the follow-up request via [doFollowUpRequest].
func followRedirectHop(ctx context.Context, client *rest.Client, httpClient *http.Client, baseURL string, current rest.RedirectError, info redirectFollowUpInfo, remaining, configuredMax int) (any, error) {
	if remaining <= 0 {
		return nil, rest.RedirectChainTooDeepError{Location: current.Location, Depth: configuredMax}
	}

	nextMethod := info.method
	body := info.bodyBytes
	contentType := info.contentType
	// 301/302/303 switch to GET, dropping any body (RFC 9110); 307/308
	// preserve the originating method+body — already validated server-
	// side by [rest.Redirect] against the target route's own Method.
	if current.Status == http.StatusMovedPermanently || current.Status == http.StatusFound || current.Status == http.StatusSeeOther {
		nextMethod = http.MethodGet
		body = nil
		contentType = ""
	}

	if handle, ok := client.MatchRedirectRoute(nextMethod, current.Location); ok {
		return doFollowUpRequest(ctx, client, httpClient, baseURL, nextMethod, current.Location, body, contentType, info, handle, remaining, configuredMax)
	}
	if _, ok := client.MatchRedirectSSERoute(nextMethod, current.Location); ok {
		return nil, rest.RedirectToStreamUnsupportedError{Location: current.Location}
	}
	return nil, rest.UnrecognizedRedirectError{Location: current.Location, Status: current.Status}
}

// doFollowUpRequest issues ONE follow-up HTTP request against handle
// (the matched target's *RouteHandle[TargetReq,TargetResp], as `any`,
// recovered via reflection) and either decodes the response (done) or,
// if the response is ITSELF a redirect, continues the chain via
// [followRedirectHop] with one fewer hop remaining.
func doFollowUpRequest(ctx context.Context, client *rest.Client, httpClient *http.Client, baseURL, method, location string, body []byte, contentType string, info redirectFollowUpInfo, handle any, remaining, configuredMax int) (any, error) {
	rawURL := strings.TrimRight(baseURL, "/") + location

	var bodyReader io.Reader
	if len(body) > 0 {
		bodyReader = bytes.NewReader(body)
	}
	httpReq, err := http.NewRequestWithContext(ctx, method, rawURL, bodyReader)
	if err != nil {
		return nil, RequestBuildError{Err: err}
	}
	if contentType != "" {
		httpReq.Header.Set("Content-Type", contentType)
	}
	for k, vs := range info.credHeaders {
		for _, v := range vs {
			httpReq.Header.Add(k, v)
		}
	}
	for k, vs := range info.extraHeaders {
		for _, v := range vs {
			httpReq.Header.Add(k, v)
		}
	}
	for k, v := range info.cookieVars {
		httpReq.AddCookie(&http.Cookie{Name: k, Value: v})
	}

	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return nil, RequestError{Method: method, Path: location, Err: err}
	}
	defer resp.Body.Close()

	respBody, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil, ResponseBodyError{Err: readErr}
	}

	if isRedirectStatus(resp.StatusCode) {
		next := rest.RedirectError{Status: resp.StatusCode, Location: resp.Header.Get("Location")}
		return followRedirectHop(ctx, client, httpClient, baseURL, next, info, remaining-1, configuredMax)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, UnexpectedStatusError{Method: method, Path: location, StatusCode: resp.StatusCode, Body: respBody, Header: resp.Header}
	}

	return decodeTargetResponse(handle, respBody, resp)
}

// isRedirectStatus reports whether status is one of the 5 statuses
// [rest.Redirect]/[rest.RedirectToSSE] support.
func isRedirectStatus(status int) bool {
	switch status {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther,
		http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		return true
	}
	return false
}

// decodeTargetResponse decodes respBody into the target handle's own
// Resp type (recovered via reflection — handle is *RouteHandle[TargetReq,
// TargetResp] for a TargetResp this package never names statically),
// using the SAME [rest.RouteHandle.DecodeMergedResponse] method every
// OTHER client dispatch path in this package already uses — the
// established idiom for "reflect against an already-concrete method on a
// type-erased handle" rather than duplicating decode logic.
func decodeTargetResponse(handle any, respBody []byte, resp *http.Response) (any, error) {
	handleVal := reflect.ValueOf(handle)
	if !handleVal.IsValid() || handleVal.Kind() != reflect.Pointer {
		return nil, fmt.Errorf("api/rest internal: redirect target handle has unexpected type %T", handle)
	}
	respHeaders := make(map[string]string, len(resp.Header))
	for k := range resp.Header {
		respHeaders[k] = resp.Header.Get(k)
	}
	respHeaderNames, _ := handleVal.MethodByName("ResponseHeaderParamNames").Call(nil)[0].Interface().([]string)
	respHeaders = rest.NormalizeHeaderVars(respHeaders, respHeaderNames)
	respCookies := make(map[string]string)
	for _, c := range resp.Cookies() {
		respCookies[c.Name] = c.Value
	}
	results := handleVal.MethodByName("DecodeMergedResponse").Call([]reflect.Value{
		reflect.ValueOf(respBody), reflect.ValueOf(respHeaders), reflect.ValueOf(respCookies),
	})
	if errI, _ := results[1].Interface().(error); errI != nil {
		return nil, errI
	}
	return results[0].Interface(), nil
}

// asRedirectError is a small, explicit errors.As wrapper — kept as a
// named helper purely for readability at call sites checking whether
// networkStep's returned error signals a redirect.
func asRedirectError(err error) (rest.RedirectError, bool) {
	var redirErr rest.RedirectError
	ok := errors.As(err, &redirErr)
	return redirErr, ok
}

// isTerminalRedirectError reports whether err is one of the redirect-
// resolution errors [clientTransport.Consume]'s reconnect loop must
// return IMMEDIATELY rather than silently retry — a mismatched/missing
// registry entry or an exceeded chain depth is a configuration problem,
// not a transient connection failure, and retrying it forever would
// hide the real cause until ctx eventually cancels (returning nil).
func isTerminalRedirectError(err error) bool {
	if err == nil {
		return false
	}
	var unrecognized rest.UnrecognizedRedirectError
	var notStreamable rest.RedirectTargetNotStreamableError
	var tooDeep rest.RedirectChainTooDeepError
	return errors.As(err, &unrecognized) || errors.As(err, &notStreamable) || errors.As(err, &tooDeep)
}
