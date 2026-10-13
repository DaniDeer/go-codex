package restdispatch

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strconv"
	"strings"

	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/internal/route"
	"github.com/DaniDeer/go-codex/stats"
)

// MaxRequestBodyBytes is the default request-body size limit (1 MiB)
// used when a route's [Options.MaxBodyBytes]-equivalent is unset.
// Promoted (Phase 4, REST family dispatch-core consolidation) verbatim
// from adapters/nethttp's/adapters/chi's own identical constant.
const MaxRequestBodyBytes = 1 << 20

// ResponseWriter is what [DispatchRoute] needs from the caller's own
// response-writer wrapper: a plain [http.ResponseWriter] plus
// [rest.ErrorResponseWriter] (the error-pattern dispatch's write-back
// seam). Deliberately an INTERFACE, not a shared concrete type:
// adapters/nethttp's/adapters/chi's own `statusResponseWriter` (and its
// `WriteErrorResponse` method, which calls each package's own exported
// `SetCookie`/`CookieOptions`-translation helper directly) stays
// package-local and UNCHANGED — promoting it would require exporting its
// otherwise-private fields purely to support cross-package struct-literal
// construction at adapter.go's/serve_sse.go's many existing call sites,
// a worse trade than simply accepting it here as an interface.
type ResponseWriter interface {
	http.ResponseWriter
	rest.ErrorResponseWriter
}

// errorBody is the JSON error envelope used by [DefaultErrorHandler].
type errorBody struct {
	Error string `json:"error"`
}

// DefaultErrorHandler is the fallback error renderer used when a route's
// Options has no custom ErrorHandler — promoted (Phase 4) verbatim from
// adapters/nethttp's/adapters/chi's own identical function.
func DefaultErrorHandler(w http.ResponseWriter, _ *http.Request, status int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	body, _ := json.Marshal(errorBody{Error: err.Error()})
	_, _ = w.Write(body)
}

// tryRespondErrorPattern is the RECOMMENDED single call site for every
// Category-A failure point (docs/design/d-0005-error-handling.md's
// Topic 1/5): consults a declared [rest.ErrorPattern] via
// ObserveErrorResponseFor (which ALSO reports match/miss/span-tag
// observability internally) and, on a matched [rest.ErrorRespond] action,
// writes the typed response. Returns true when the response was fully
// handled — the caller should return immediately without invoking its
// own fixed-shape errFn call. Returns false (unmatched, a non-Respond
// action, or a write failure) when the caller should fall through to its
// EXISTING fixed-shape errFn call. *err is mutated in place to the
// pattern's own applyErr (a mapFn/encode failure) or a response-write
// failure — callers must use the (possibly updated) *err in their own
// subsequent errFn call. Promoted (Phase 4) verbatim from
// adapters/nethttp's/adapters/chi's own identical function (the
// reflect-based one, used by buildRouteHandler — NOT
// tryRespondErrorPatternGeneric, handlerFunc's generic counterpart,
// which remains package-local; see docs/roadmap/
// adapter-integration-architecture.md's REST family step 6/7 scope).
func tryRespondErrorPattern(
	ctx context.Context, sw ResponseWriter, elem reflect.Value,
	obs stats.Observer, respHeaders http.Header, pendingCookies *[]rest.PendingCookie, err *error,
) bool {
	handled, updatedErr := rest.CallDispatchErrorResponse(elem.Addr(), ctx, obs, sw, respHeaders, *pendingCookies, *err)
	*err = updatedErr
	return handled
}

// CallErr reflect-calls target.MethodByName(method)(arg) and type-asserts
// the single result as error — promoted (Phase 4) verbatim.
func CallErr(target reflect.Value, method string, arg reflect.Value) error {
	results := target.MethodByName(method).Call([]reflect.Value{arg})
	err, _ := results[0].Interface().(error)
	return err
}

// CallErrStatusFor reflect-calls ErrorStatusFor(err) on target —
// promoted (Phase 4) verbatim.
func CallErrStatusFor(target reflect.Value, err error) (int, bool) {
	results := target.MethodByName("ErrorStatusFor").Call([]reflect.Value{reflect.ValueOf(&err).Elem()})
	status, _ := results[0].Interface().(int)
	ok, _ := results[1].Interface().(bool)
	return status, ok
}

// SatisfiesPerHandler extracts handlers[i].Satisfies into a parallel
// [][]string slice — [httpsecurity.MergeMiddlewareHandlerGrants]'s own
// input shape. Promoted (Phase 4) verbatim.
func SatisfiesPerHandler(handlers []rest.MiddlewareHandler) [][]string {
	out := make([][]string, len(handlers))
	for i, h := range handlers {
		out[i] = h.Satisfies
	}
	return out
}

// IsSecuritySatisfyingHandler reports whether handlers contains an entry
// named name with a non-empty Satisfies — i.e. a Security-paired
// MiddlewareHandler — used to keep Security's OWN distinct error
// fallback (401, rest.SecurityError) for a failing Security-gated Fn.
// Promoted (Phase 4) verbatim.
func IsSecuritySatisfyingHandler(handlers []rest.MiddlewareHandler, name string) bool {
	for _, h := range handlers {
		if h.Name == name {
			return len(h.Satisfies) > 0
		}
	}
	return false
}

// CredentialExtractorFor builds a [rest.CredentialExtractor] reading
// header/query/cookie values from r — promoted (Phase 4) verbatim.
func CredentialExtractorFor(r *http.Request) rest.CredentialExtractor {
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

// formatContentTypesReflect returns the ContentType() of every entry in
// formats (a reflect.Value of type []format.Format[T] for an erased T) —
// used to build the Supported list on [rest.UnsupportedMediaTypeError]/
// [rest.NotAcceptableError]. Promoted (Phase 4) verbatim.
func formatContentTypesReflect(formats reflect.Value) []string {
	n := formats.Len()
	if n == 0 {
		return nil
	}
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		if ct := formats.Index(i).MethodByName("ContentType").Call(nil)[0].String(); ct != "" {
			out = append(out, ct)
		}
	}
	return out
}

// NegotiateFormatReflect (Resp-direction) picks a format.Format[T]
// (T erased) by Accept header — promoted (Phase 4) verbatim from
// adapters/nethttp's/adapters/chi's own identical function (previously
// duplicated in each package's own serve_sse.go). Exported since
// serve_sse.go in both packages, deliberately untouched by this
// consolidation pass otherwise, now references this shared version via a
// thin package-local func-var alias.
func NegotiateFormatReflect(formatsVal reflect.Value, accept string) (reflect.Value, bool) {
	if accept == "" || accept == "*/*" {
		return formatsVal.Index(0), true
	}
	for _, part := range strings.Split(accept, ",") {
		want := strings.TrimSpace(strings.SplitN(part, ";", 2)[0])
		if want == "*/*" {
			return formatsVal.Index(0), true
		}
		for i := 0; i < formatsVal.Len(); i++ {
			f := formatsVal.Index(i)
			ct := f.MethodByName("ContentType").Call(nil)[0].String()
			ct, _, _ = strings.Cut(ct, ";")
			if strings.TrimSpace(ct) == want {
				return f, true
			}
		}
	}
	return reflect.Value{}, false
}

// negotiateRequestFormatReflect picks the format.Format[Req] (Req erased)
// whose ContentType exactly matches contentType (parameters stripped) —
// promoted (Phase 4) verbatim.
func negotiateRequestFormatReflect(formats reflect.Value, contentType string) (reflect.Value, bool) {
	n := formats.Len()
	for i := 0; i < n; i++ {
		f := formats.Index(i)
		fmtMediaType, _, _ := strings.Cut(f.MethodByName("ContentType").Call(nil)[0].String(), ";")
		if strings.TrimSpace(fmtMediaType) == contentType {
			return f, true
		}
	}
	return reflect.Value{}, false
}

// ResponseHeaderValues converts an http.Header into a flat
// map[string]string for use with [rest.RouteHandle.ValidateResponseHeaders].
// When a header has multiple values, only the first is kept. Promoted
// (Phase 4) verbatim.
func ResponseHeaderValues(h http.Header) map[string]string {
	m := make(map[string]string, len(h))
	for k, vs := range h {
		if len(vs) > 0 {
			m[k] = vs[0]
		}
	}
	return m
}

// ResponseCookieValues extracts name→value from cookies for use with
// [rest.RouteHandle.ValidateResponseCookies]. When a name appears more
// than once, the last value wins. Promoted (Phase 4) verbatim.
func ResponseCookieValues(cookies []rest.PendingCookie) map[string]string {
	m := make(map[string]string, len(cookies))
	for _, pc := range cookies {
		m[pc.Name] = pc.Value
	}
	return m
}

// PrimaryStatusFor returns descriptor's first declared response status,
// defaulting to 200 when none is declared or it fails to parse. Promoted
// (Phase 4) verbatim.
func PrimaryStatusFor(descriptor route.Route) int {
	if len(descriptor.Responses) == 0 {
		return http.StatusOK
	}
	code, err := strconv.Atoi(descriptor.Responses[0].Status)
	if err != nil {
		return http.StatusOK
	}
	return code
}

// AsRedirectError is a small, explicit errors.As wrapper — kept as a
// named helper purely for readability at call sites checking whether a
// handler's returned error signals a redirect. Promoted (Phase 4)
// verbatim.
func AsRedirectError(err error) (rest.RedirectError, bool) {
	var redirErr rest.RedirectError
	ok := errors.As(err, &redirErr)
	return redirErr, ok
}
