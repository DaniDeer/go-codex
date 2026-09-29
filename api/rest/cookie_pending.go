package rest

// PendingCookie is a protocol-agnostic, staged outgoing cookie —
// docs/roadmap/capability-requirement-composition.md's Phase 7 promoted
// this from a per-adapter type (`nethttp.PendingCookie`/
// `chi.PendingCookie`, both `{Name, Value string; Opts CookieOptions}`)
// into `api/rest` itself, since both adapters' shapes were already
// byte-for-byte structurally identical. Attrs reuses the ALREADY-shared
// [CookieAttributes] type (no new attribute type needed) — the adapter's
// own write-time `CookieOptions` (which additionally carries a
// validation-only `Codec` field, irrelevant once a cookie is already
// staged for writing) is derived from Attrs at the actual write call
// site, not stored here.
//
// `adapters/nethttp.PendingCookie`/`adapters/chi.PendingCookie` are now
// type ALIASES of this type — existing code that only passes the slice
// around (never touching the Attrs/Opts field directly) needed zero
// changes.
type PendingCookie struct {
	Name, Value string
	Attrs       CookieAttributes
}

// responseHeaderValuesFrom flattens a multi-value header map into a
// single-value map for [RouteHandle.ValidateResponseHeaders] — mirrors
// the former per-adapter `responseHeaderValues(http.Header)` helper,
// generalized to a plain `map[string][]string` (which `http.Header`
// already IS, structurally, so callers convert via a free, zero-cost
// type conversion).
func responseHeaderValuesFrom(h map[string][]string) map[string]string {
	m := make(map[string]string, len(h))
	for k, vs := range h {
		if len(vs) > 0 {
			m[k] = vs[0]
		}
	}
	return m
}

// responseCookieValuesFrom flattens a []PendingCookie into a
// name->value map for [RouteHandle.ValidateResponseCookies] — mirrors
// the former per-adapter `responseCookieValues([]PendingCookie)`
// helper. When a name appears more than once, the last value wins.
func responseCookieValuesFrom(cookies []PendingCookie) map[string]string {
	m := make(map[string]string, len(cookies))
	for _, c := range cookies {
		m[c.Name] = c.Value
	}
	return m
}
