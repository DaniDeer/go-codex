package chi

import (
	"net/http"

	"github.com/DaniDeer/go-codex/api/rest"
)

// httpCarrier is chi's own per-REQUEST value satisfying
// [rest.HeaderCapableTransport]/[rest.CookieCapableTransport]/
// [rest.QueryCapableTransport] (docs/roadmap/
// capability-requirement-composition.md's Phase 6 — promoted from the
// former zero-cost, data-less `transportCapabilities{}` marker to a
// REAL, callable extraction interface) — mirrors [nethttp]'s identical
// `httpCarrier` exactly. Every request-dispatch call site constructs
// its OWN `httpCarrier{r}` from the real, live `*http.Request` and
// calls its `Extract*` methods exactly once, reusing the extracted
// maps for both codec validation AND merge-field building.
type httpCarrier struct{ r *http.Request }

// ExtractQuery extracts all query parameters into a flat
// map[string]string. When a key appears multiple times, the first
// value is used.
func (c httpCarrier) ExtractQuery() map[string]string {
	q := c.r.URL.Query()
	m := make(map[string]string, len(q))
	for k, vs := range q {
		if len(vs) > 0 {
			m[k] = vs[0]
		}
	}
	return m
}

// ExtractQueryMulti returns the full multi-value query map, used when
// `Options.MultiValueQueryParams` is set.
func (c httpCarrier) ExtractQueryMulti() map[string][]string {
	return c.r.URL.Query()
}

// ExtractCookies extracts all cookies into a flat map[string]string.
// When a cookie name appears multiple times, the first value is used.
func (c httpCarrier) ExtractCookies() map[string]string {
	cookies := c.r.Cookies()
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
func (c httpCarrier) ExtractHeaders() map[string]string {
	m := make(map[string]string, len(c.r.Header))
	for k, vs := range c.r.Header {
		if len(vs) > 0 {
			m[k] = vs[0]
		}
	}
	return m
}

// httpTransport is a zero-value carrier (nil *http.Request) used
// SOLELY for the Attach-time [rest.CheckParamKindCoverage] type
// assertion — that check never invokes the method, only asserts the
// interface is implemented, so a nil-field zero value is safe here.
// This check can never fail for chi, proving the Tier 2 mechanism
// coexists with chi's existing dispatch with ZERO behavior change for
// any existing route.
var httpTransport = httpCarrier{}

var (
	_ rest.HeaderCapableTransport = httpTransport
	_ rest.CookieCapableTransport = httpTransport
	_ rest.QueryCapableTransport  = httpTransport
)
