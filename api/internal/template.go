package internal

import (
	"regexp"

	"github.com/DaniDeer/go-codex/internal/templatematch"
)

// TemplateVarRe matches {varName} placeholders in path or topic templates.
var TemplateVarRe = regexp.MustCompile(`\{([^}]+)\}`)

// ParseTemplateVars returns the set of variable names (without braces) found
// in a path or topic template such as "/users/{id}/posts/{postId}".
func ParseTemplateVars(template string) map[string]bool {
	vars := make(map[string]bool)
	for _, m := range TemplateVarRe.FindAllStringSubmatch(template, -1) {
		vars[m[1]] = true
	}
	return vars
}

// StripTemplateVars replaces each {varName} placeholder in template with the
// literal segment placeholder (the single character "x"). The result has the
// same structural shape as the template but contains no brace syntax.
//
// This is used internally to make path/topic codec validation template-transparent:
// constraints run on the shape of the path/topic, not on the literal {varName} tokens.
func StripTemplateVars(template string) string {
	return TemplateVarRe.ReplaceAllString(template, "x")
}

// MatchTemplate matches a concrete path/topic string against a template
// containing {varName} placeholders and literal text, returning the
// extracted variable values. Each {varName} placeholder captures everything
// up to the next "/" (it never crosses a path/topic segment boundary), but
// — unlike a naive segment-by-segment split — a placeholder MAY share a
// segment with literal text, e.g. "{date}.json" in
// "readings/{sensorID}/{date}.json" correctly captures "2024-01-15" from
// ".../2024-01-15.json". No wildcard support ({+}/{#}-style MQTT
// semantics) — this is the shared, protocol-agnostic core; adapters/mqtt's
// matchTopicTemplate keeps its own wildcard handling separately since file
// paths and REST-shaped templates have no wildcard concept.
//
// Delegates to [templatematch.MatchNonWildcard] — the shared, module-internal
// core also used by adapters/zeromq and ports/file.go, which cannot import
// this api/internal package (Go's internal/ visibility rule restricts it to
// code rooted at api/'s own subtree). See
//
// Returns wrapMismatch(template, concrete) when the concrete string's
// structure does not match the template (wrong segment count, or literal
// text does not match).
func MatchTemplate(template, concrete string, wrapMismatch func(template, concrete string) error) (map[string]string, error) {
	return templatematch.MatchNonWildcard(template, concrete, wrapMismatch)
}

// BuildFromTemplate used to live here (the build-direction counterpart of
// [MatchTemplate]) but has been SUPERSEDED by
// [github.com/DaniDeer/go-codex/codex.BuildFromParams] — moving the logic
// to codex (importable everywhere, not just the api/ subtree Go's
// internal/ visibility rule restricts this package to) let api/rest,
// api/events, and api/reqreply share ONE canonical implementation instead
// of each repeating their own codecMap-building + call.
