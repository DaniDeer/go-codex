package route

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
