package templatematch

import (
	"fmt"
	"path/filepath"
	"strings"
)

// This file adds a filesystem-glob matching mode alongside
// [MatchNonWildcard] (named-var-only) and [MatchMQTTWildcard] (MQTT topic
// wildcards) — used by [github.com/DaniDeer/go-codex/ports]'s [File]/[Dir]
// path templates, modeled directly on shell glob / [path/filepath.Match]
// semantics rather than MQTT's `+`/`#` convention, since filesystem users
// already know shell glob.
//
// Glob segment syntax (each "/"-delimited template segment is classified
// independently):
//
//   - "{varName}" — a named placeholder occupying the WHOLE segment (unlike
//     [MatchNonWildcard], a glob-enabled template's {varName} may NOT share
//     a segment with literal text — mutually exclusive with glob chars).
//   - "**" (globstar) — matches zero or more WHOLE segments. At MOST ONE per
//     template — see [ValidateGlobstarCount] — keeps matching deterministic
//     prefix/suffix arithmetic instead of a general backtracking matcher.
//   - Any other segment containing '*', '?', or '[' — matched via
//     [path/filepath.Match] (may share a segment with literal text, e.g.
//     "app-*.json").
//   - Any other segment — literal, matched by exact string equality.
//
// A template is "glob-enabled" ([IsGlobEnabled]) when it contains at least
// one "**" or filepath.Match-style segment; a template with ONLY literal/
// named-var segments is unaffected and keeps using [MatchNonWildcard]
// exactly as before.

// isVarSegment reports whether seg is a whole "{varName}" placeholder.
func isVarSegment(seg string) bool {
	return len(seg) > 2 && seg[0] == '{' && seg[len(seg)-1] == '}'
}

// isGlobSegment reports whether seg is a filepath.Match-style glob segment
// (contains '*', '?', or '[') or the globstar marker "**". A {varName}
// segment is never also a glob segment — the two are mutually exclusive.
func isGlobSegment(seg string) bool {
	if isVarSegment(seg) {
		return false
	}
	return seg == "**" || strings.ContainsAny(seg, "*?[")
}

// IsGlobEnabled reports whether template contains at least one glob
// segment ("**" or a filepath.Match-style segment). Templates with only
// literal/named-var segments return false and are unaffected by this file
// — they keep using [MatchNonWildcard].
func IsGlobEnabled(template string) bool {
	for _, seg := range strings.Split(template, "/") {
		if isGlobSegment(seg) {
			return true
		}
	}
	return false
}

// ValidateGlobstarCount returns a descriptive error when template contains
// more than one "**" segment — Phase 1 caps glob-enabled templates at one
// globstar marker so matching stays deterministic prefix/suffix segment
// arithmetic instead of a general backtracking matcher. Callers (typically
// NewDir/NewFile) are expected to PANIC on a non-nil return — this is a
// structural template error (a programming mistake), not a runtime
// condition, mirroring this codebase's existing declaration-time panics
// for merge-field type mismatches and var-name collisions.
func ValidateGlobstarCount(template string) error {
	count := 0
	for _, seg := range strings.Split(template, "/") {
		if seg == "**" {
			count++
		}
	}
	if count > 1 {
		return fmt.Errorf("template %q contains %d \"**\" segments; at most one is allowed", template, count)
	}
	return nil
}

// LiteralPrefix returns the leading run of template segments that are
// NEITHER a "{varName}" placeholder NOR a glob segment, joined by "/" —
// the safe, unambiguous filesystem path a glob-discovery walk (e.g.
// [github.com/DaniDeer/go-codex/ports.Dir.List]'s glob-discovery mode) can
// start from. Returns "" when template's first segment is already a
// placeholder or glob segment (the walk then starts from the caller's own
// base directory, e.g. [github.com/DaniDeer/go-codex/ports.WithBaseDir],
// defaulting to ".").
func LiteralPrefix(template string) string {
	var lit []string
	for _, seg := range strings.Split(template, "/") {
		if isVarSegment(seg) || isGlobSegment(seg) {
			break
		}
		lit = append(lit, seg)
	}
	return strings.Join(lit, "/")
}

// splitSegments splits a "/"-delimited path/template into its segments,
// treating "" as ZERO segments (not one empty segment, unlike a bare
// [strings.Split]) — needed so an empty concrete path (a glob-discovery
// walk's root directory, relative to itself) matches a template whose
// remaining structure is entirely a "**" globstar (which can match zero
// segments), e.g. template "data/**" matching concrete "data".
func splitSegments(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "/")
}

// MatchGlob matches a concrete path against a glob-enabled template,
// returning the captured named-var values (glob segments — "**", "*",
// "?", "[...]" — are always anonymous and never appear in the result,
// exactly like a shell glob has no named captures).
//
// Requires the template to satisfy [ValidateGlobstarCount] (at most one
// "**"); behavior is undefined (best-effort, first "**" wins) if called
// with an invalid template — callers are expected to reject/panic on an
// invalid template at declaration time, not at match time.
//
// Returns wrapMismatch(template, concrete) when the concrete path's
// structure does not match the template (wrong segment count, a literal
// segment differs, or a glob segment's [path/filepath.Match] fails).
func MatchGlob(template, concrete string, wrapMismatch func(template, concrete string) error) (map[string]string, error) {
	tmplSegs := splitSegments(template)
	concreteSegs := splitSegments(concrete)

	globstarIdx := -1
	for i, seg := range tmplSegs {
		if seg == "**" {
			globstarIdx = i
			break
		}
	}

	vars := make(map[string]string)

	matchSeg := func(tmplSeg, concreteSeg string) (bool, error) {
		switch {
		case isVarSegment(tmplSeg):
			vars[tmplSeg[1:len(tmplSeg)-1]] = concreteSeg
			return true, nil
		case isGlobSegment(tmplSeg):
			return filepath.Match(tmplSeg, concreteSeg)
		default:
			return tmplSeg == concreteSeg, nil
		}
	}

	if globstarIdx == -1 {
		// No globstar: segment counts must match exactly (same rigidity as
		// MatchMQTTWildcard's non-wildcard segments).
		if len(tmplSegs) != len(concreteSegs) {
			return nil, wrapMismatch(template, concrete)
		}
		for i, tmplSeg := range tmplSegs {
			ok, err := matchSeg(tmplSeg, concreteSegs[i])
			if err != nil || !ok {
				return nil, wrapMismatch(template, concrete)
			}
		}
		return vars, nil
	}

	// Exactly one globstar: split into a fixed prefix (before "**") and a
	// fixed suffix (after "**") — deterministic arithmetic, no backtracking.
	prefix := tmplSegs[:globstarIdx]
	suffix := tmplSegs[globstarIdx+1:]
	if len(concreteSegs) < len(prefix)+len(suffix) {
		return nil, wrapMismatch(template, concrete)
	}
	for i, tmplSeg := range prefix {
		ok, err := matchSeg(tmplSeg, concreteSegs[i])
		if err != nil || !ok {
			return nil, wrapMismatch(template, concrete)
		}
	}
	offset := len(concreteSegs) - len(suffix)
	for i, tmplSeg := range suffix {
		ok, err := matchSeg(tmplSeg, concreteSegs[offset+i])
		if err != nil || !ok {
			return nil, wrapMismatch(template, concrete)
		}
	}
	return vars, nil
}
