package nethttp

import "github.com/DaniDeer/go-codex/api/rest"

// transportCapabilities is nethttp's own zero-cost marker satisfying
// [rest.HeaderCapableTransport]/[rest.CookieCapableTransport]/
// [rest.QueryCapableTransport] (docs/roadmap/
// capability-requirement-composition.md's Phase 3) — HTTP structurally
// ALWAYS supports all three (baseline reality, made explicit in code, not
// just doc comments), so there is no per-connection variability to check
// the way a socket-based adapter's transport type would need: ONE static
// value represents "HTTP itself" for the coverage check, called ONCE per
// Serve/AttachServer/Call/AttachClient setup (never per-request).
type transportCapabilities struct{}

func (transportCapabilities) SupportsHeaderParams() {}
func (transportCapabilities) SupportsCookieParams() {}
func (transportCapabilities) SupportsQueryParams()  {}

// httpTransport is the single shared value passed to
// [rest.CheckParamKindCoverage] — this check can never fail for nethttp,
// proving the Tier 2 mechanism coexists with HTTP's existing dispatch
// with ZERO behavior change for any existing route.
var httpTransport = transportCapabilities{}

var (
	_ rest.HeaderCapableTransport = httpTransport
	_ rest.CookieCapableTransport = httpTransport
	_ rest.QueryCapableTransport  = httpTransport
)
