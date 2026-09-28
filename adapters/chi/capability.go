package chi

import "github.com/DaniDeer/go-codex/api/rest"

// transportCapabilities is chi's own zero-cost marker satisfying
// [rest.HeaderCapableTransport]/[rest.CookieCapableTransport]/
// [rest.QueryCapableTransport] (docs/roadmap/
// capability-requirement-composition.md's Phase 3) — mirrors
// [nethttp]'s identical marker exactly: HTTP structurally ALWAYS
// supports all three, so ONE static value represents "HTTP itself" for
// the coverage check, called ONCE per Serve setup (never per-request).
type transportCapabilities struct{}

func (transportCapabilities) SupportsHeaderParams() {}
func (transportCapabilities) SupportsCookieParams() {}
func (transportCapabilities) SupportsQueryParams()  {}

// httpTransport is the single shared value passed to
// [rest.CheckParamKindCoverage] — this check can never fail for chi,
// proving the Tier 2 mechanism coexists with chi's existing dispatch
// with ZERO behavior change for any existing route.
var httpTransport = transportCapabilities{}

var (
	_ rest.HeaderCapableTransport = httpTransport
	_ rest.CookieCapableTransport = httpTransport
	_ rest.QueryCapableTransport  = httpTransport
)
