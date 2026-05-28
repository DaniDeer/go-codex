// Package internal provides shared helpers for api/rest and api/events.
// It is not part of the public API.
package internal

import (
	"regexp"

	"github.com/DaniDeer/go-codex/codex"
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

// BuildFromTemplate substitutes {varName} placeholders in template with values
// from vars, validating each against the corresponding codec in paramCodecs.
// wrapMissing is called to produce a typed error when a template variable has no
// entry in vars. wrapErr is called to produce a typed error when codec validation
// fails.
func BuildFromTemplate(
	template string,
	vars map[string]string,
	paramCodecs map[string]codex.Codec[string],
	wrapMissing func(name string) error,
	wrapErr func(name, value string, err error) error,
) (string, error) {
	var firstErr error
	result := TemplateVarRe.ReplaceAllStringFunc(template, func(placeholder string) string {
		if firstErr != nil {
			return placeholder
		}
		name := placeholder[1 : len(placeholder)-1] // strip { and }
		value, ok := vars[name]
		if !ok {
			firstErr = wrapMissing(name)
			return placeholder
		}
		if c, hasCodec := paramCodecs[name]; hasCodec {
			if err := c.Validate(value); err != nil {
				firstErr = wrapErr(name, value, err)
				return placeholder
			}
		}
		return value
	})
	if firstErr != nil {
		return "", firstErr
	}
	return result, nil
}
