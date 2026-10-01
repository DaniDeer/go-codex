package websocket

import (
	"net/http"

	"github.com/DaniDeer/go-codex/api/rest"
)

// wsCarrier is adapters/websocket's own per-REQUEST value satisfying
// [rest.HeaderCapableTransport]/[rest.CookieCapableTransport]/
// [rest.QueryCapableTransport] (docs/design/
// d-0006-protocol-native-capabilities.md's Phase 6a — bringing
// adapters/websocket into the SAME real-interface mechanism
// adapters/nethttp/adapters/chi's httpCarrier already implements,
// Phase 6). Package-local, not a reuse of nethttp's httpCarrier — Go
// disallows cross-package unexported reuse, and this mirrors the
// established per-adapter-duplication precedent elsewhere in this
// roadmap (e.g. Capability/WireAttributes being separate types per
// MQTT adapter).
type wsCarrier struct{ r *http.Request }

// ExtractQuery extracts all query parameters into a flat
// map[string]string. When a key appears multiple times, the first
// value is used — moved verbatim from the former package-level
// queryValues function.
func (c wsCarrier) ExtractQuery() map[string]string {
	m := make(map[string]string, len(c.r.URL.Query()))
	for k, vals := range c.r.URL.Query() {
		if len(vals) > 0 {
			m[k] = vals[0]
		}
	}
	return m
}

// ExtractQueryMulti returns the full multi-value query map. Required to
// satisfy [rest.QueryCapableTransport] — adapters/websocket has no
// MultiValueQueryParams-style toggle of its own (upgradeAndValidate
// never calls this), but the interface requires both methods.
func (c wsCarrier) ExtractQueryMulti() map[string][]string {
	return c.r.URL.Query()
}

// ExtractCookies extracts all cookies into a flat map[string]string.
// When a cookie name appears multiple times, the first value is used —
// NEW (Phase 6a Finding 2's cookie-support gap; a WebSocket upgrade
// request is an ordinary HTTP GET and can carry cookies exactly like
// any REST request).
func (c wsCarrier) ExtractCookies() map[string]string {
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
// When a header has multiple values, only the first is kept — moved
// verbatim from the former package-level headerValues function.
func (c wsCarrier) ExtractHeaders() map[string]string {
	m := make(map[string]string, len(c.r.Header))
	for k, vals := range c.r.Header {
		if len(vals) > 0 {
			m[k] = vals[0]
		}
	}
	return m
}

var (
	_ rest.HeaderCapableTransport = wsCarrier{}
	_ rest.CookieCapableTransport = wsCarrier{}
	_ rest.QueryCapableTransport  = wsCarrier{}
)

// checkSocketParamKindCoverage runs [rest.CheckParamKindCoverage] against
// route's declared Header/Cookie/Query param requirements — called ONCE,
// at each of IngestSocketAdapter/BroadcastSocketAdapter/DuplexSocketAdapter's
// construction time (this adapter's "Attach"-equivalent moment, mirroring
// nethttp/chi's own Attach-time placement), NOT per-request. Shared by all
// 3 constructors to avoid tripling the same 4-line check.
func checkSocketParamKindCoverage(route *rest.RouteHandle[struct{}, struct{}]) error {
	requiredKinds := rest.RequiredParamKinds(
		route.HeaderParamNames(), route.CookieParamNames(), route.QueryParamNames(),
		route.SecuritySchemes)
	return rest.CheckParamKindCoverage("websocket", requiredKinds, wsCarrier{})
}
