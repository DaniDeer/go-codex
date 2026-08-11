package internal

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"

	c "github.com/DaniDeer/go-codex/codex"
	v "github.com/DaniDeer/go-codex/validate"
)

// This file holds the WWW-Authenticate challenge concept — the struct,
// its codecs, and the low-level parse/format functions those codecs
// compose as their `to`/`from` directions.

// Challenge is a parsed WWW-Authenticate Bearer challenge (RFC 6750 / the
// Docker Distribution auth spec), decoded/encoded via ChallengeCodec
// (below).
type Challenge struct {
	Realm   string
	Service string
	Scope   string
}

// challengeStructCodec validates Challenge's Realm field once parsed —
// Service/Scope are OptionalField since real registries vary in which of
// the two they actually populate in a challenge.
var challengeStructCodec = c.Struct[Challenge](
	c.RequiredField("realm", c.String().Refine(v.URI),
		func(ch Challenge) string { return ch.Realm },
		func(ch *Challenge, val string) { ch.Realm = val },
	),
	c.OptionalField("service", c.String(),
		func(ch Challenge) string { return ch.Service },
		func(ch *Challenge, val string) { ch.Service = val },
	),
	c.OptionalField("scope", c.String(),
		func(ch Challenge) string { return ch.Scope },
		func(ch *Challenge, val string) { ch.Scope = val },
	),
)

// ChallengeCodec decodes/encodes a WWW-Authenticate Bearer challenge
// header value to/from the structured Challenge.
var ChallengeCodec = c.MapCodecValidated(
	c.String(), challengeStructCodec,
	ParseChallengeString, FormatChallengeString,
)

// headerCodec is a trivial passthrough Codec[http.Header] — Decode
// type-asserts the input as http.Header (an in-memory Go value, not a
// JSON document, so there is nothing to parse at this layer); it exists
// purely so WWWAuthenticateCodec below can compose via the SAME
// MapCodecValidated pattern every other codec in this package uses.
var headerCodec = c.Codec[http.Header]{
	Decode: func(v any) (http.Header, error) {
		h, ok := v.(http.Header)
		if !ok {
			return nil, fmt.Errorf("expected http.Header, got %T", v)
		}
		return h, nil
	},
	Encode: func(h http.Header) (any, error) { return h, nil },
}

// WWWAuthenticateCodec decodes a Challenge DIRECTLY from an http.Header
// set — extracting the "WWW-Authenticate" entry and delegating to the
// SAME ParseChallengeString/FormatChallengeString ChallengeCodec itself
// uses — so header extraction is a single codec Decode call, not a plain
// Header.Get(...) followed by a separate parse step.
var WWWAuthenticateCodec = c.MapCodecValidated(
	headerCodec, challengeStructCodec,
	func(h http.Header) (Challenge, error) { return ParseChallengeString(h.Get("WWW-Authenticate")) },
	func(ch Challenge) (http.Header, error) {
		s, err := FormatChallengeString(ch)
		if err != nil {
			return nil, err
		}
		h := make(http.Header, 1)
		h.Set("WWW-Authenticate", s) // Set canonicalizes the key, unlike a map literal.
		return h, nil
	},
)

// reChallengeParam matches one key="value" pair inside a WWW-Authenticate
// Bearer challenge header, e.g. `realm="https://auth.example.com/token"`.
var reChallengeParam = regexp.MustCompile(`([a-zA-Z_]+)="([^"]*)"`)

// ParseChallengeString parses a WWW-Authenticate Bearer challenge header
// (RFC 6750 / Docker Distribution's auth spec), e.g.:
//
//	Bearer realm="https://auth.example.com/token",service="registry.example.com",scope="repository:my/repo:pull"
//
// into its realm/service/scope parameters. Exported as the `to` direction
// of ChallengeCodec (above).
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
// (above).
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
