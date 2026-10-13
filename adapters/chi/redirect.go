package chi

import (
	"github.com/DaniDeer/go-codex/adapters/internal/restdispatch"
)

// asRedirectError reports whether err is (or wraps) a [rest.RedirectError]
// — mirrors [adapters/nethttp]'s identical helper. chi has no client-side
// transport (docs/features/rest-redirects.md scopes auto-follow to
// adapters/nethttp only), so this package only needs the server-side
// recognition half: a handler returning a [rest.RedirectError]
// (constructed via [rest.Redirect]/[rest.RedirectToSSE]) renders as a
// bare status + Location header, no body. PROMOTED (Phase 4) to
// [restdispatch.AsRedirectError] — aliased here (not copied).
var asRedirectError = restdispatch.AsRedirectError
