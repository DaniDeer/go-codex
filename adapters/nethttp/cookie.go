package nethttp

import (
	"net/http"

	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
)

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
	// Default: false (Secure is always set).
	Insecure bool

	// AllowJS, when true, omits the HttpOnly attribute, making the cookie
	// accessible via document.cookie. Required for patterns such as
	// CSRF tokens that must be read by client-side JavaScript.
	// Default: false (HttpOnly is always set).
	AllowJS bool

	// Codec, when non-nil, validates value before the Set-Cookie header is
	// written. Use the same [codex.Codec] as the matching [rest.CookieParam]
	// for symmetric read/write validation from a single definition:
	//
	//	sessionCodec := codex.String().Refine(validate.MinLen(32))
	//
	//	// Read: incoming cookie validated against sessionCodec.
	//	rest.RouteConfig{
	//	    CookieParams: []rest.CookieParam{
	//	        {Name: "session_token", Required: true, Codec: &sessionCodec},
	//	    },
	//	}
	//
	//	// Write: same codec validates the outgoing value before Set-Cookie.
	//	err := nethttp.SetCookie(w, "session_token", newToken, nethttp.CookieOptions{
	//	    Codec: &sessionCodec,
	//	})
	//
	// If validation fails, SetCookie returns [rest.CookieParamError] and does
	// NOT write the Set-Cookie header.
	Codec *codex.Codec[string]
}

// SetCookie writes a Set-Cookie header on w with secure defaults:
// Secure, HttpOnly, SameSite=Strict, Path="/".
//
// If [CookieOptions.Codec] is non-nil, value is validated first using that
// codec. A validation failure returns a [rest.CookieParamError] without
// writing any header — the same error type returned by
// [rest.RouteHandle.ValidateCookies] on the read side.
//
// Example — symmetric read/write validation from a single codec:
//
//	sessionCodec := codex.String().Refine(validate.MinLen(32))
//
//	// Read (adapter validates automatically via CookieParam):
//	RouteConfig{CookieParams: []rest.CookieParam{{Name: "session_token", Codec: &sessionCodec}}}
//
//	// Write (handler sets the cookie with the same codec):
//	if err := nethttp.SetCookie(w, "session_token", token, nethttp.CookieOptions{
//	    MaxAge: 3600,
//	    Codec:  &sessionCodec,
//	}); err != nil {
//	    http.Error(w, err.Error(), http.StatusInternalServerError)
//	    return
//	}
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

	// #nosec G124 -- Secure/HttpOnly/SameSite are enforced by default; opts.Insecure/AllowJS are intentional opt-outs.
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
