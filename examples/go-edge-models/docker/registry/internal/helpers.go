package internal

import (
	"fmt"
	"regexp"
	"strings"
)

// This file holds the helpers wrapping codec decode/encode — the
// low-level parse/format functions each codec.go's MapCodecValidated
// composes as its `to`/`from` directions. Kept separate from codecs.go
// (which declares only Codec[T] VALUES) and types.go (which declares only
// plain struct/type DEFINITIONS), so each file answers exactly one
// question: what shape is it (types.go), how is it validated/composed
// (codecs.go), or how is the wire string actually parsed/formatted
// (this file).

// reChallengeParam matches one key="value" pair inside a WWW-Authenticate
// Bearer challenge header, e.g. `realm="https://auth.example.com/token"`.
var reChallengeParam = regexp.MustCompile(`([a-zA-Z_]+)="([^"]*)"`)

// ParseChallengeString parses a WWW-Authenticate Bearer challenge header
// (RFC 6750 / Docker Distribution's auth spec), e.g.:
//
//	Bearer realm="https://auth.example.com/token",service="registry.example.com",scope="repository:my/repo:pull"
//
// into its realm/service/scope parameters. Exported as the `to` direction
// of ChallengeCodec (codecs.go).
func ParseChallengeString(header string) (Challenge, error) {
	if !strings.HasPrefix(header, "Bearer ") {
		return Challenge{}, fmt.Errorf(`missing "Bearer " prefix`)
	}
	params := make(map[string]string)
	for _, m := range reChallengeParam.FindAllStringSubmatch(header, -1) {
		params[m[1]] = m[2]
	}
	return Challenge{Realm: params["realm"], Service: params["service"], Scope: params["scope"]}, nil
}

// FormatChallengeString reconstructs a WWW-Authenticate Bearer challenge
// header value from ch. Exported as the `from` direction of ChallengeCodec
// (codecs.go).
func FormatChallengeString(ch Challenge) (string, error) {
	var b strings.Builder
	b.WriteString(`Bearer realm="`)
	b.WriteString(ch.Realm)
	b.WriteString(`"`)
	if ch.Service != "" {
		b.WriteString(`,service="`)
		b.WriteString(ch.Service)
		b.WriteString(`"`)
	}
	if ch.Scope != "" {
		b.WriteString(`,scope="`)
		b.WriteString(ch.Scope)
		b.WriteString(`"`)
	}
	return b.String(), nil
}

// ParseDockerScopeString splits a "type:name:action1,action2" scope
// string into its three segments — the name segment never contains ":"
// (Docker repository/resource names are colon-free by grammar), so a
// simple 3-way SplitN is exact, no ambiguity heuristics needed. Exported
// as the `to` direction of DockerScopeCodec (codecs.go).
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
// (codecs.go).
func FormatDockerScopeString(s DockerScope) (string, error) {
	return s.ResourceType + ":" + s.Name + ":" + strings.Join(s.Actions, ","), nil
}
