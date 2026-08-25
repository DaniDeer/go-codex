// Package internal provides shared helpers used by [api/rest], [api/events],
// and [api/reqreply]. It is not part of the public API and must not be
// imported by any non-api package.
//
// # Template variable parsing
//
// [ParseTemplateVars] parses `{varName}` placeholders from a path or topic template
// and returns the set of variable names. Used by builders to validate declared
// path/topic parameter names against the template at Register time.
//
// [StripTemplateVars] replaces `{varName}` placeholders with `x` before builder-level
// codec validation — ensuring constraints work correctly on parameterised templates.
//
// [MatchTemplate] matches a concrete path/topic string against a template,
// extracting `{varName}` placeholder values — delegates to
// [github.com/DaniDeer/go-codex/internal/templatematch.MatchNonWildcard].
//
// The build-direction counterpart (substituting concrete values into a
// template) used to live here as BuildFromTemplate but has been SUPERSEDED
// by [github.com/DaniDeer/go-codex/codex.BuildFromParams]. api/rest,
// api/events, and api/reqreply now call codex.BuildFromParams directly.
package internal
