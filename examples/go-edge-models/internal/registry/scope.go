package internal

import (
	"fmt"
	"strings"

	c "github.com/DaniDeer/go-codex/codex"
	v "github.com/DaniDeer/go-codex/validate"
)

// This file holds the Docker Distribution auth scope concept — the
// struct, its constraint, its codec, and the low-level parse/format
// functions the codec composes as its `to`/`from` directions.

// DockerScope is a parsed Docker Distribution auth scope, e.g.
// "repository:library/alpine:pull" — the format request/response bodies
// and query parameters carry when negotiating pull/push permissions for a
// resource. Decoded/encoded via DockerScopeCodec.
type DockerScope struct {
	// ResourceType is the resource kind, e.g. "repository".
	ResourceType string
	// Name is the resource name, e.g. "library/alpine".
	Name string
	// Actions is the requested permissions, e.g. ["pull"], ["pull","push"].
	Actions []string
}

// ActionsConstraint requires DockerScope.Actions to contain at least one
// action — an empty actions list would encode to a scope string with a
// trailing empty segment ("repository:name:"), which no real registry
// treats as meaningful.
var ActionsConstraint = c.Constraint[[]string]{
	Name:  "actions",
	Check: func(v []string) bool { return len(v) > 0 },
	Message: func(v []string) string {
		return "actions must contain at least one action (e.g. \"pull\")"
	},
}

var dockerScopeStructCodec = c.Struct[DockerScope](
	c.RequiredField("resourceType", c.String().Refine(v.NonEmptyString),
		func(s DockerScope) string { return s.ResourceType },
		func(s *DockerScope, val string) { s.ResourceType = val },
	),
	c.RequiredField("name", c.String().Refine(v.NonEmptyString),
		func(s DockerScope) string { return s.Name },
		func(s *DockerScope, val string) { s.Name = val },
	),
	c.RequiredField("actions", c.SliceOf(c.String()).Refine(ActionsConstraint),
		func(s DockerScope) []string { return s.Actions },
		func(s *DockerScope, val []string) { s.Actions = val },
	),
)

// DockerScopeCodec decodes/encodes a Docker Distribution auth scope
// string ("repository:library/alpine:pull") to/from the structured
// DockerScope. Same three-layer MapCodecValidated pattern as
// ImageRefCodec/ChallengeCodec (parent package): the wire codec accepts
// any string, ParseDockerScopeString/FormatDockerScopeString do the
// actual field extraction/reconstruction, and dockerScopeStructCodec
// validates the parsed fields.
var DockerScopeCodec = c.MapCodecValidated(
	c.String(), dockerScopeStructCodec,
	ParseDockerScopeString, FormatDockerScopeString,
)

// ParseDockerScopeString splits a "type:name:action1,action2" scope
// string into its three segments — the name segment never contains ":"
// (Docker repository/resource names are colon-free by grammar), so a
// simple 3-way SplitN is exact, no ambiguity heuristics needed. Exported
// as the `to` direction of DockerScopeCodec (above).
func ParseDockerScopeString(s string) (DockerScope, error) {
	parts := strings.SplitN(s, ":", 3)
	if len(parts) != 3 {
		return DockerScope{}, fmt.Errorf(
			"invalid scope %q: expected \"type:name:action1,action2,...\"", s)
	}
	return DockerScope{
		ResourceType: parts[0],
		Name:         parts[1],
		Actions:      strings.Split(parts[2], ","),
	}, nil
}

// FormatDockerScopeString reconstructs the "type:name:action1,action2"
// wire string from s. Exported as the `from` direction of DockerScopeCodec
// (above).
func FormatDockerScopeString(s DockerScope) (string, error) {
	return s.ResourceType + ":" + s.Name + ":" + strings.Join(s.Actions, ","), nil
}
