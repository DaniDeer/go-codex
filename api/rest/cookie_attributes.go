package rest

// CookieSameSite mirrors http.SameSite's four states without importing
// net/http (api/rest stays transport-agnostic by design) — each adapter
// (adapters/nethttp, adapters/chi) translates this to its own http.SameSite
// constant at dispatch time.
type CookieSameSite int

const (
	// SameSiteDefault defers to the adapter's own default (adapters/nethttp
	// and adapters/chi both default to http.SameSiteStrictMode).
	SameSiteDefault CookieSameSite = iota
	SameSiteLax
	SameSiteStrict
	SameSiteNone
)

// CookieAttributes declares the Set-Cookie attributes for ONE response
// cookie — the codec-declared counterpart to adapters/nethttp's/
// adapters/chi's own (adapter-owned, http.SameSite-based) CookieOptions.
// Plain Go types only — no codex.Codec needed, since every field already
// has a closed, safely-defaulted value space (no external wire format to
// validate against).
//
// Attach via [MergedResponseCookieParam.WithAttributes].
type CookieAttributes struct {
	// Path is the cookie path. Empty means the adapter's own default ("/").
	Path string

	// Domain scopes the cookie to a specific host. Empty means the current host.
	Domain string

	// MaxAge is the cookie lifetime in seconds. 0 means session cookie
	// (deleted when the browser closes). Negative means delete immediately.
	MaxAge int

	// SameSite controls cross-site request behavior. SameSiteDefault defers
	// to the adapter's own default (Strict).
	SameSite CookieSameSite

	// Insecure, when true, omits the Secure attribute. Use only for
	// non-TLS/local-development environments.
	Insecure bool

	// AllowJS, when true, omits the HttpOnly attribute, making the cookie
	// accessible via document.cookie (e.g. for CSRF tokens read by
	// client-side JavaScript).
	AllowJS bool
}
