package chi

import (
	"errors"

	"github.com/DaniDeer/go-codex/api/rest"
)

// asRedirectError reports whether err is (or wraps) a [rest.RedirectError]
// — mirrors [adapters/nethttp]'s identical helper. chi has no client-side
// transport (docs/roadmap/rest-typed-redirects.md scopes auto-follow to
// adapters/nethttp only), so this package only needs the server-side
// recognition half: a handler returning a [rest.RedirectError]
// (constructed via [rest.Redirect]/[rest.RedirectToSSE]) renders as a
// bare status + Location header, no body.
func asRedirectError(err error) (rest.RedirectError, bool) {
	var redirErr rest.RedirectError
	ok := errors.As(err, &redirErr)
	return redirErr, ok
}
