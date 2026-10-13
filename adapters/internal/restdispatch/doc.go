// Package restdispatch is the shared, adapters-internal helper for the
// net/http-family REST adapter tree (adapters/nethttp, adapters/chi, and
// any future adapter built on net/http) — mirrors
// adapters/internal/httpsecurity's/adapters/internal/scopesmerge's own
// identical scoping convention.
//
// Phase 1's audit (docs/roadmap/adapter-integration-architecture.md)
// confirmed adapters/chi independently duplicates adapters/nethttp's
// entire construction-time dispatch-validation logic line-for-line, with
// no import relationship between the two packages — a Phase 4 REST
// family consolidation target. This package holds the first piece of
// that consolidation: [HTTPCarrier] (promoted, not copied, from the two
// packages' previously-separate, byte-for-byte-identical definitions)
// and [ValidateRouteConstruction] (the four-check construction-time
// core both adapters' own buildRouteHandler now call instead of
// independently reimplementing).
//
// Deliberately scoped to ONLY the construction-time validation piece for
// now (Phase 4's steps 1-3 of 8) — the per-request dispatch body
// (decode/validate/security/middleware/handle/encode) remains
// independently implemented per adapter pending a later round (step 4),
// and adapters/nethttp's/adapters/chi's own serve_sse.go files (flagged,
// not yet audited, as a near-certain follow-on duplication target) are
// untouched by this package so far.
package restdispatch
