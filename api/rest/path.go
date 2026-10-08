package rest

import "github.com/DaniDeer/go-codex/api/internal"

// PathShape returns path's structural shape for routing-conflict
// comparison: every {varName} template placeholder is replaced with the
// same literal marker, so two path templates that differ only in their
// variable names (e.g. "/users/{id}" and "/users/{name}") normalize to the
// identical shape ("/users/x").
//
// Server adapters (adapters/nethttp, adapters/chi) use this to detect
// routes that are genuinely ambiguous/overlapping at the HTTP routing
// level even though they are not literal Method+Path duplicates — a class
// of conflict that underlying router implementations handle
// inconsistently (net/http's ServeMux panics at registration time; chi
// silently registers both and dispatches every matching request to
// whichever was registered last, permanently shadowing the other). Each
// adapter's own literal-duplicate check (its DuplicateRouteError) is
// unaffected — this is an additional, complementary check.
func PathShape(path string) string {
	return internal.StripTemplateVars(path)
}
