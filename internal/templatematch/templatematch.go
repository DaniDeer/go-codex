// Package templatematch is the shared, module-internal core for matching a
// concrete path/topic string against a "{varName}"-style template and
// extracting the variable values — the inverse of building a concrete
// path/topic FROM a template + a vars map (that direction is handled per
// package, e.g. [github.com/DaniDeer/go-codex/api/internal.BuildFromTemplate]).
//
// This package exists because the matching algorithm was independently
// duplicated FOUR times across the codebase (adapters/mqtt, adapters/mqtt5,
// adapters/zeromq, ports/file.go) — none of those packages can import
// api/internal (Go's internal/ import-visibility rule restricts it to code
// rooted at api/'s own subtree). A package under the repository's TOP-LEVEL
// internal/ directory is importable from anywhere in the module
// (api/*, adapters/*, ports/, ...), which is why the shared core lives here
// instead of under api/internal.
package templatematch

import (
	"regexp"
	"strings"
)

// templateVarRe matches {varName} placeholders in a path or topic template.
var templateVarRe = regexp.MustCompile(`\{([^}]+)\}`)

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
