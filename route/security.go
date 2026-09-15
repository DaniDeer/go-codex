package route

import (
	"reflect"

	"github.com/DaniDeer/go-codex/codex"
)

// CodecSchemaMismatch reports whether a and b conflict for a param-
// conflict-detection check's purposes: nil-vs-non-nil is itself a
// mismatch (differing validation strictness); both non-nil compares
// Schema via reflect.DeepEqual; both nil is not a mismatch.
//
// Moved here from 2 near-identical per-package copies
// (`api/events`'s codecSchemaMismatch, `api/reqreply`'s inline
// checkReqReplyContributionMap comparison) plus `api/rest`'s OWN
// Candidate-2 addition (see
// docs/roadmap/rest-middleware-conflict-detection-improvements.md) — this
// logic operates purely on [codex.Codec], the core `codex` package's own
// type, with zero API-specific behavior, mirroring [FirstSchemeName]'s
// existing de-duplication precedent above.
func CodecSchemaMismatch(a, b *codex.Codec[string]) bool {
	if (a == nil) != (b == nil) {
		return true
	}
	if a == nil {
		return false
	}
	return !reflect.DeepEqual(a.Schema, b.Schema)
}

// FirstSchemeName returns the first scheme name found across reqs, or ""
// if reqs is empty. Used for [stats.SecurityObserver.RecordSecurityRejection]
// reporting — a rejection needs SOME scheme name to report, and the first
// one found is as good as any for that purpose (an OR-composed
// requirement's schemes are all equally "the" requirement that failed).
//
// Moved here from 5 byte-identical private per-adapter copies
// (`adapters/nethttp`, `adapters/chi`, `adapters/mqtt`, `adapters/mqtt5`,
// `adapters/zeromq` — zeromq's own copy was named `firstSchemeName`) —
// this logic operates purely on [SecurityRequirement], the core `route`
// package's own type, with zero adapter-specific behavior.
func FirstSchemeName(reqs []SecurityRequirement) string {
	for _, req := range reqs {
		for name := range req {
			return name
		}
	}
	return ""
}

// Satisfied reports whether granted — a scheme name → granted-scopes map —
// satisfies AT LEAST ONE requirement in reqs (OR across requirements, AND
// within one requirement's scheme+scopes — the same semantics
// []SecurityRequirement already has in the OpenAPI/AsyncAPI spec itself).
//
// A scheme present in granted with a nil/empty scope slice is treated as
// "authenticated, no scope restriction" — it satisfies any requirement for
// that scheme with an empty scopes list (e.g. plain apiKey/bearer schemes
// that don't use OAuth2 scopes at all).
//
// An empty reqs means "nothing required" and is always satisfied,
// regardless of granted (mirrors SecurityRequirement's own doc comment:
// "an empty []SecurityRequirement means no auth required").
func Satisfied(reqs []SecurityRequirement, granted map[string][]string) bool {
	if len(reqs) == 0 {
		return true
	}
	for _, req := range reqs {
		if requirementSatisfied(req, granted) {
			return true
		}
	}
	return false
}

// requirementSatisfied reports whether one SecurityRequirement (AND across
// every scheme it names) is satisfied by granted.
func requirementSatisfied(req SecurityRequirement, granted map[string][]string) bool {
	for scheme, wantScopes := range req {
		haveScopes, ok := granted[scheme]
		if !ok {
			return false
		}
		if !scopesSatisfied(wantScopes, haveScopes) {
			return false
		}
	}
	return true
}

// scopesSatisfied reports whether haveScopes covers every entry in
// wantScopes. An empty/nil haveScopes with a non-empty wantScopes means the
// scheme is authenticated but grants no scopes — this does NOT satisfy a
// wantScopes requirement (only an empty wantScopes is satisfied trivially).
func scopesSatisfied(wantScopes, haveScopes []string) bool {
	if len(wantScopes) == 0 {
		return true
	}
	have := make(map[string]struct{}, len(haveScopes))
	for _, s := range haveScopes {
		have[s] = struct{}{}
	}
	for _, want := range wantScopes {
		if _, ok := have[want]; !ok {
			return false
		}
	}
	return true
}
