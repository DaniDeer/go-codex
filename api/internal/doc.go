// Package internal provides shared helpers used by [api/rest] and [api/events].
// It is not part of the public API and must not be imported by any non-api package.
//
// # Template variable parsing
//
// [ParseTemplateVars] parses `{varName}` placeholders from a path or topic template
// and returns the set of variable names. Used by builders to validate [PathParam]
// and [TopicParam] names against the template at [Register] time.
//
// [StripTemplateVars] replaces `{varName}` placeholders with `x` before builder-level
// codec validation — ensuring constraints work correctly on parameterised templates.
//
// [BuildFromTemplate] substitutes concrete variable values into a template, validating
// each value against its registered codec. Used by [BuildPath] and [BuildTopic].
package internal
