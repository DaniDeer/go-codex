// Package templatematch is the shared, module-internal core for both
// directions of "{varName}"-style template handling: matching a concrete
// path/topic string against a template to EXTRACT variable values, and
// BUILDING a concrete string FROM a template + a vars map by substitution.
//
// This package exists because both directions were independently
// duplicated across the codebase (the match direction: adapters/mqtt,
// adapters/mqtt5, adapters/zeromq, ports/file.go; the build direction:
// api/internal.BuildFromTemplate, ports/file.go, ports/dir.go,
// codex/dottedkey.go) — none of those packages can import api/internal
// (Go's internal/ import-visibility rule restricts it to code rooted at
// api/'s own subtree), and codex/ports cannot import each other. A package
// under the repository's TOP-LEVEL internal/ directory is importable from
// anywhere in the module (codex/, api/*, adapters/*, ports/, ...), which is
// why the shared core lives here. This package deliberately has NO
// dependency on codex (codex depends on templatematch, not the reverse) —
// per-variable CODEC validation is layered on top by each caller (see
// [github.com/DaniDeer/go-codex/codex.Template] for the canonical,
// codec-aware wrapper built on this package's Build/Match functions).
package templatematch

import (
	"regexp"
	"strings"
)

// templateVarRe matches {varName} placeholders in a path or topic template.
var templateVarRe = regexp.MustCompile(`\{([^}]+)\}`)

// Build substitutes each "{varName}" placeholder in template with the
// corresponding value from vars, returning the concrete string. This is
// the delimiter-agnostic CORE substitution algorithm — it works
// identically for "/"-delimited (path/topic) and "."-delimited (dotted
// key) templates, since it operates on the raw placeholder tokens
// wherever they appear, not on delimiter-split segments.
//
// wrapMissing is called to produce an error when a template variable has
// no entry in vars — the caller supplies this so the returned error has
// the caller's OWN typed shape (this package has no error types of its
// own). No per-variable CODEC validation happens here — vars is assumed
// to already contain validated string values (see this package's own doc
// comment: codec validation is layered on top by the caller, e.g.
// [github.com/DaniDeer/go-codex/codex.Template], typically via
// codex.EncodeVars BEFORE calling Build).
func Build(template string, vars map[string]string, wrapMissing func(name string) error) (string, error) {
	var firstErr error
	result := templateVarRe.ReplaceAllStringFunc(template, func(placeholder string) string {
		if firstErr != nil {
			return placeholder
		}
		name := placeholder[1 : len(placeholder)-1] // strip { and }
		value, ok := vars[name]
		if !ok {
			firstErr = wrapMissing(name)
			return placeholder
		}
		return value
	})
	if firstErr != nil {
		return "", firstErr
	}
	return result, nil
}

// ParseVars returns the set of "{varName}" placeholder names (without
// braces) found in template — e.g. {"id": true} for "/users/{id}". Used
// by [github.com/DaniDeer/go-codex/codex.Template] to validate, at
// construction time, that every placeholder has a matching declared
// field (a template referencing an undeclared var is a programming
// error, caught immediately rather than surfacing as a confusing runtime
// "missing var" failure).
func ParseVars(template string) map[string]bool {
	vars := make(map[string]bool)
	for _, m := range templateVarRe.FindAllStringSubmatch(template, -1) {
		vars[m[1]] = true
	}
	return vars
}

// ParseVarsInOrder returns the "{varName}" placeholder names (without
// braces) found in template, in FIRST-OCCURRENCE, left-to-right order,
// each name appearing once even if referenced multiple times in template.
// Used by callers (e.g. codex.BuildFromParams) that need to preserve
// left-to-right, first-error-wins validation semantics across multiple
// placeholders — [ParseVars]'s map has no defined iteration order.
func ParseVarsInOrder(template string) []string {
	seen := make(map[string]bool)
	var names []string
	for _, m := range templateVarRe.FindAllStringSubmatch(template, -1) {
		name := m[1]
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	return names
}

// MatchNonWildcard matches a concrete path/topic string against a template
// containing "{varName}" placeholders and literal text, returning the
// extracted variable values. Each "{varName}" placeholder captures
// everything up to the next "/" (it never crosses a path/topic segment
// boundary), but — unlike a naive segment-by-segment split — a placeholder
// MAY share a segment with literal text, e.g. "{date}.json" in
// "readings/{sensorID}/{date}.json" correctly captures "2024-01-15" from
// ".../2024-01-15.json".
//
// No wildcard support ({+}/{#}-style MQTT semantics) — this is the
// protocol-agnostic core used by REST paths, file paths, MCP resource URIs,
// and ZeroMQ topics, none of which have a wildcard concept. Use
// [MatchMQTTWildcard] for MQTT (3.1.1/5) topic templates instead.
//
// Returns wrapMismatch(template, concrete) when the concrete string's
// structure does not match the template (wrong segment count, or literal
// text does not match).
func MatchNonWildcard(template, concrete string, wrapMismatch func(template, concrete string) error) (map[string]string, error) {
	var pattern strings.Builder
	pattern.WriteString("^")
	var names []string
	lastEnd := 0
	for _, loc := range templateVarRe.FindAllStringIndex(template, -1) {
		start, end := loc[0], loc[1]
		pattern.WriteString(regexp.QuoteMeta(template[lastEnd:start])) // literal text before the var
		names = append(names, template[start+1:end-1])                 // strip { and }
		pattern.WriteString("([^/]+)")                                 // captures the var; never crosses "/"
		lastEnd = end
	}
	pattern.WriteString(regexp.QuoteMeta(template[lastEnd:]))
	pattern.WriteString("$")

	re := regexp.MustCompile(pattern.String())
	m := re.FindStringSubmatch(concrete)
	if m == nil {
		return nil, wrapMismatch(template, concrete)
	}
	vars := make(map[string]string, len(names))
	for i, name := range names {
		vars[name] = m[i+1]
	}
	return vars, nil
}

// MatchMQTTWildcard performs level-by-level matching of a concrete MQTT
// topic against a go-codex topic template — the shared core for
// adapters/mqtt (3.1.1) and adapters/mqtt5.
//
// Template syntax:
//   - {varName} — matches exactly one topic level; the segment value is
//     captured under "varName" in the returned map.
//   - Literal segments — must match the concrete topic exactly.
//   - MQTT single-level wildcard (+) — matches exactly one level (anonymous
//     capture is omitted from the result map).
//   - MQTT multi-level wildcard (#) as the last template segment — matches
//     all remaining levels (zero or more); the remaining path is captured
//     under "#" in the result map.
//
// Unlike [MatchNonWildcard], each {varName} placeholder must occupy an
// ENTIRE topic level — it cannot share a segment with literal text or
// another placeholder (MQTT topics have no such convention; this mirrors
// the pre-existing adapters/mqtt/adapters/mqtt5 behavior exactly).
//
// Returns wrapMismatch(template, concrete) when the concrete topic's
// structure does not match the template (wrong number of levels, or a
// literal segment does not match).
//
// Delegates to matchWildcard with "/" as the level delimiter — see
// [MatchDottedWildcard] for the SAME syntax applied to dotted wire keys
// (codec map buckets) instead of MQTT topics, "." as the delimiter.
func MatchMQTTWildcard(template, concrete string, wrapMismatch func(template, concrete string) error) (map[string]string, error) {
	return matchWildcard(template, concrete, "/", wrapMismatch)
}

// MatchDottedWildcard is [MatchMQTTWildcard]'s dotted-key counterpart —
// identical {varName}/+/# semantics, but "." is the level delimiter
// instead of "/". Used by codec.DottedKeyCodec/DottedPatchMapCodec to
// validate/extract dotted wire keys (e.g.
// "properties.desired.modules.factory-gw.env.API_URL") the SAME
// declarative way MQTT topic templates already work.
func MatchDottedWildcard(template, concrete string, wrapMismatch func(template, concrete string) error) (map[string]string, error) {
	return matchWildcard(template, concrete, ".", wrapMismatch)
}

// matchWildcard is the delimiter-parameterized core both
// [MatchMQTTWildcard] (delimiter "/") and [MatchDottedWildcard]
// (delimiter ".") build on — level-by-level matching of a concrete
// string against a template using {varName}/+/# syntax (see
// [MatchMQTTWildcard]'s own doc comment for the full syntax reference).
func matchWildcard(template, concrete, delimiter string, wrapMismatch func(template, concrete string) error) (map[string]string, error) {
	tmplParts := strings.Split(template, delimiter)
	concreteParts := strings.Split(concrete, delimiter)

	vars := make(map[string]string)

	for i, seg := range tmplParts {
		// Multi-level wildcard — must be last segment; captures everything remaining.
		if seg == "#" {
			vars["#"] = strings.Join(concreteParts[i:], delimiter)
			return vars, nil
		}

		if i >= len(concreteParts) {
			return nil, wrapMismatch(template, concrete)
		}

		concreteSeg := concreteParts[i]

		switch {
		case len(seg) > 2 && seg[0] == '{' && seg[len(seg)-1] == '}':
			// {varName} placeholder — capture the concrete segment value.
			name := seg[1 : len(seg)-1]
			vars[name] = concreteSeg

		case seg == "+":
			// Anonymous single-level wildcard — match any segment, no capture.

		default:
			// Literal segment — must match exactly.
			if seg != concreteSeg {
				return nil, wrapMismatch(template, concrete)
			}
		}
	}

	// All template segments consumed; the concrete string must have no extra segments.
	if len(concreteParts) != len(tmplParts) {
		return nil, wrapMismatch(template, concrete)
	}

	return vars, nil
}
