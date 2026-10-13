package restdispatch

import (
	"net/http"

	"github.com/DaniDeer/go-codex/api/rest"
)

// HTTPCarrier is the net/http-family adapter tree's own per-REQUEST value
// satisfying [rest.HeaderCapableTransport]/[rest.CookieCapableTransport]/
// [rest.QueryCapableTransport] (docs/design/
// d-0006-protocol-native-capabilities.md's Phase 6 — promoted from the
// former zero-cost, data-less `transportCapabilities{}` marker to a
// REAL, callable extraction interface). Every request-dispatch call site
// constructs its OWN `HTTPCarrier{r}` from the real, live *http.Request
// and calls its Extract* methods exactly once, reusing the extracted
// maps for both codec validation AND merge-field building.
//
// PROMOTED (Phase 4, REST family dispatch-core consolidation — see the
// roadmap doc) from adapters/nethttp's and adapters/chi's previously
// separate, byte-for-byte-identical definitions. Both packages now alias
// their own (unexported) `httpCarrier`/`httpTransport` names to this
// type, so every pre-existing call site (including
// adapters/nethttp/serve_sse.go and adapters/chi/serve_sse.go,
// deliberately untouched by this consolidation pass) keeps compiling
// and behaving identically — each updated to the keyed literal
// `httpCarrier{R: r}` (`go vet` flags unkeyed literals for a struct now
// defined in a different package).
type HTTPCarrier struct{ R *http.Request }

// ExtractQuery extracts all query parameters into a flat
// map[string]string. When a key appears multiple times, the first
// value is used — mirrors [rest.RouteHandle.ValidateQuery]'s expected
// shape.
func (c HTTPCarrier) ExtractQuery() map[string]string {
	q := c.R.URL.Query()
	m := make(map[string]string, len(q))
	for k, vs := range q {
		if len(vs) > 0 {
			m[k] = vs[0]
		}
	}
	return m
}

// ExtractQueryMulti returns the full multi-value query map — mirrors
// [rest.RouteHandle.ValidateQueryMulti]'s expected shape, used when
// `Options.MultiValueQueryParams` is set.
func (c HTTPCarrier) ExtractQueryMulti() map[string][]string {
	return c.R.URL.Query()
}

// ExtractCookies extracts all cookies into a flat map[string]string.
// When a cookie name appears multiple times, the first value is used.
func (c HTTPCarrier) ExtractCookies() map[string]string {
	cookies := c.R.Cookies()
	m := make(map[string]string, len(cookies))
	for _, ck := range cookies {
		if _, exists := m[ck.Name]; !exists {
			m[ck.Name] = ck.Value
		}
	}
	return m
}

// ExtractHeaders extracts HTTP headers into a flat map[string]string.
// When a header has multiple values, only the first is kept.
func (c HTTPCarrier) ExtractHeaders() map[string]string {
	m := make(map[string]string, len(c.R.Header))
	for k, vs := range c.R.Header {
		if len(vs) > 0 {
			m[k] = vs[0]
		}
	}
	return m
}

// HTTPTransport is a zero-value carrier (nil *http.Request) used SOLELY
// for the Attach/serve-time [rest.CheckParamKindCoverage] type assertion
// — that check never invokes the method, only asserts the interface is
// implemented, so a nil-field zero value is safe here. This check can
// never fail for an adapter built on net/http, proving the Tier 2
// mechanism coexists with HTTP's existing dispatch with ZERO behavior
// change for any existing route.
var HTTPTransport = HTTPCarrier{}

var (
	_ rest.HeaderCapableTransport = HTTPTransport
	_ rest.CookieCapableTransport = HTTPTransport
	_ rest.QueryCapableTransport  = HTTPTransport
)
